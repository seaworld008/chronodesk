package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gongdan-system/internal/models"
)

// NotificationServiceInterface 通知服务接口
type NotificationServiceInterface interface {
	// Webhook相关方法 (现有功能)
	SendNotification(ctx context.Context, event *NotificationEvent) error
	
	// 通知管理相关方法
	CreateNotification(ctx context.Context, req *models.NotificationCreateRequest) (*models.Notification, error)
	GetNotifications(ctx context.Context, filter *models.NotificationFilter) ([]*models.Notification, int64, error)
	MarkAsRead(ctx context.Context, notificationID uint, userID uint) error
	MarkAllAsRead(ctx context.Context, userID uint) error
	GetUnreadCount(ctx context.Context, userID uint) (int64, error)
	
	// 通知偏好设置
	GetNotificationPreferences(ctx context.Context, userID uint) ([]*models.NotificationPreference, error)
	UpdateNotificationPreferences(ctx context.Context, userID uint, preferences []models.NotificationPreference) error
	
	// 自动通知生成
	NotifyTicketStatusChanged(ctx context.Context, ticket *models.Ticket, oldStatus models.TicketStatus, userID uint) error
	NotifyTicketAssigned(ctx context.Context, ticket *models.Ticket, userID uint) error
	
	// 邮件通知相关方法
	ProcessPendingEmailNotifications(ctx context.Context) error
	RetryFailedEmailNotifications(ctx context.Context) error
	SetEmailNotificationService(emailService EmailNotificationServiceInterface)
}

// NotificationService 通知服务
type NotificationService struct {
	db                      *gorm.DB
	client                  *http.Client
	emailNotificationService EmailNotificationServiceInterface
	webhookMaxConcurrent    int
}

// NewNotificationService 创建通知服务实例
func NewNotificationService(db *gorm.DB) *NotificationService {
	return &NotificationService{
		db: db,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		webhookMaxConcurrent: getWebhookMaxConcurrent(),
	}
}

// SetEmailNotificationService 设置邮件通知服务（依赖注入）
func (ns *NotificationService) SetEmailNotificationService(emailService EmailNotificationServiceInterface) {
	ns.emailNotificationService = emailService
}

// NotificationEvent 通知事件
type NotificationEvent struct {
	Type       models.WebhookEventType `json:"type"`
	ResourceID uint                    `json:"resource_id"`
	ResourceType string                `json:"resource_type"`
	Title      string                  `json:"title"`
	Description string                 `json:"description"`
	Data       map[string]interface{}  `json:"data"`
	Metadata   map[string]string       `json:"metadata"`
	Timestamp  time.Time               `json:"timestamp"`
	UserID     *uint                   `json:"user_id,omitempty"`
}

// SendNotification 发送通知
func (ns *NotificationService) SendNotification(ctx context.Context, event *NotificationEvent) error {
	// 1. 获取符合条件的Webhook配置
	configs, err := ns.getActiveWebhooks(event.Type)
	if err != nil {
		return fmt.Errorf("获取webhook配置失败: %w", err)
	}

	if len(configs) == 0 {
		// 没有配置的webhook，正常返回
		return nil
	}

	// 2. 并发发送通知
	errChan := make(chan error, len(configs))
	maxConcurrent := ns.webhookMaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = 5
	}
	sem := make(chan struct{}, maxConcurrent)
	
	for _, config := range configs {
		sem <- struct{}{}
		go func(cfg *models.WebhookConfig) {
			defer func() { <-sem }()
			retries := cfg.RetryCount + 1
			if retries < 1 {
				retries = 1
			}
			baseInterval := time.Duration(cfg.RetryInterval) * time.Second
			if baseInterval <= 0 {
				baseInterval = time.Second
			}
			if err := runWithRetry(func() error {
				return ns.sendWebhook(ctx, cfg, event)
			}, retries, baseInterval); err != nil {
				errChan <- fmt.Errorf("webhook %s 发送失败: %w", cfg.Name, err)
			} else {
				errChan <- nil
			}
		}(config)
	}

	// 3. 收集结果
	var errors []string
	for i := 0; i < len(configs); i++ {
		if err := <-errChan; err != nil {
			errors = append(errors, err.Error())
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("部分webhook发送失败: %s", strings.Join(errors, "; "))
	}

	return nil
}

func runWithRetry(send func() error, maxAttempts int, baseInterval time.Duration) error {
	var err error
	if maxAttempts <= 0 {
		return send()
	}
	for i := 0; i < maxAttempts; i++ {
		err = send()
		if err == nil {
			return nil
		}
		if i < maxAttempts-1 && baseInterval > 0 {
			time.Sleep(baseInterval * time.Duration(1<<i))
		}
	}
	return err
}

func getWebhookMaxConcurrent() int {
	if raw := os.Getenv("WEBHOOK_MAX_CONCURRENT"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return 5
}

// getActiveWebhooks 获取活跃的webhook配置
func (ns *NotificationService) getActiveWebhooks(eventType models.WebhookEventType) ([]*models.WebhookConfig, error) {
	var configs []*models.WebhookConfig
	
	// 查询活跃状态的webhook配置
	if err := ns.db.Where("status = ?", models.WebhookStatusActive).
		Find(&configs).Error; err != nil {
		return nil, err
	}

	// 过滤支持该事件类型的配置
	var filtered []*models.WebhookConfig
	for _, config := range configs {
		if config.IsEventEnabled(eventType) {
			filtered = append(filtered, config)
		}
	}

	return filtered, nil
}

// sendWebhook 发送单个webhook
func (ns *NotificationService) sendWebhook(ctx context.Context, config *models.WebhookConfig, event *NotificationEvent) error {
	startTime := time.Now()
	
	// 创建日志记录
	log := &models.WebhookLog{
		ConfigID:     config.ID,
		EventType:    event.Type,
		ResourceID:   event.ResourceID,
		ResourceType: event.ResourceType,
		Status:       "pending",
		MaxRetries:   config.RetryCount,
		Environment:  "development", // TODO: 从配置获取
	}

	// 序列化事件数据
	eventDataBytes, _ := json.Marshal(event)
	log.EventData = string(eventDataBytes)

	// 生成消息内容
	message, err := ns.generateMessage(config, event)
	if err != nil {
		log.Status = "failed"
		log.ErrorMessage = fmt.Sprintf("生成消息失败: %v", err)
		ns.saveLog(log)
		return err
	}

	// 构建请求
	requestBody, err := ns.buildRequestBody(config, message)
	if err != nil {
		log.Status = "failed"
		log.ErrorMessage = fmt.Sprintf("构建请求失败: %v", err)
		ns.saveLog(log)
		return err
	}

	log.RequestURL = config.WebhookURL
	log.RequestMethod = "POST"
	log.RequestBody = string(requestBody)

	// 创建HTTP请求
	req, err := http.NewRequestWithContext(ctx, "POST", config.WebhookURL, bytes.NewBuffer(requestBody))
	if err != nil {
		log.Status = "failed"
		log.ErrorMessage = fmt.Sprintf("创建请求失败: %v", err)
		ns.saveLog(log)
		return err
	}

	// 设置请求头
	ns.setRequestHeaders(req, config, requestBody)
	
	// 记录请求头
	headerBytes, _ := json.Marshal(req.Header)
	log.RequestHeaders = string(headerBytes)

	// 发送请求
	resp, err := ns.client.Do(req)
	log.ResponseTime = time.Since(startTime).Milliseconds()

	if err != nil {
		log.Status = "failed"
		log.ErrorMessage = fmt.Sprintf("请求发送失败: %v", err)
		ns.saveLog(log)
		return err
	}
	defer resp.Body.Close()

	// 读取响应
	respBody, _ := io.ReadAll(resp.Body)
	log.ResponseStatus = resp.StatusCode
	log.ResponseBody = string(respBody)

	// 记录响应头
	respHeaderBytes, _ := json.Marshal(resp.Header)
	log.ResponseHeaders = string(respHeaderBytes)

	// 判断请求是否成功
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Status = "success"
		
		// 更新配置统计
		ns.updateConfigStats(config.ID, true, nil)
	} else {
		log.Status = "failed"
		log.ErrorMessage = fmt.Sprintf("HTTP错误: %d %s", resp.StatusCode, string(respBody))
		
		// 更新配置统计
		ns.updateConfigStats(config.ID, false, fmt.Errorf("HTTP %d", resp.StatusCode))
		
		// 如果需要重试，设置重试时间
		if log.RetryCount < log.MaxRetries {
			nextRetry := time.Now().Add(time.Duration(config.RetryInterval) * time.Second)
			log.NextRetryAt = &nextRetry
			log.Status = "retrying"
		}
	}

	// 保存日志
	ns.saveLog(log)

	if log.Status == "failed" {
		return fmt.Errorf("webhook发送失败: HTTP %d", resp.StatusCode)
	}

	return nil
}

// generateMessage 生成消息内容
func (ns *NotificationService) generateMessage(config *models.WebhookConfig, event *NotificationEvent) (string, error) {
	// 如果有自定义模板，使用模板
	if config.MessageTemplate != "" {
		return ns.renderTemplate(config.MessageTemplate, event)
	}

	// 否则使用默认模板
	return ns.getDefaultMessage(config.Provider, event), nil
}

// renderTemplate 渲染消息模板
func (ns *NotificationService) renderTemplate(template string, event *NotificationEvent) (string, error) {
	// 简单的模板变量替换
	message := template
	
	// 替换基本变量
	replacements := map[string]string{
		"{{title}}":       event.Title,
		"{{description}}": event.Description,
		"{{type}}":        string(event.Type),
		"{{resource_id}}": strconv.Itoa(int(event.ResourceID)),
		"{{timestamp}}":   event.Timestamp.Format("2006-01-02 15:04:05"),
	}

	for placeholder, value := range replacements {
		message = strings.ReplaceAll(message, placeholder, value)
	}

	return message, nil
}

// GetDefaultMessage 获取默认消息内容（公开方法用于测试）
func (ns *NotificationService) GetDefaultMessage(provider models.WebhookProvider, event *NotificationEvent) string {
	return ns.getDefaultMessage(provider, event)
}

// getDefaultMessage 获取默认消息内容
func (ns *NotificationService) getDefaultMessage(provider models.WebhookProvider, event *NotificationEvent) string {
	switch provider {
	case models.WebhookProviderWeChat:
		return ns.getWeChatMessage(event)
	case models.WebhookProviderDingTalk:
		return ns.getDingTalkMessage(event)
	case models.WebhookProviderLark:
		return ns.getLarkMessage(event)
	default:
		return fmt.Sprintf("**%s**\n\n%s\n\n时间: %s", 
			event.Title, event.Description, event.Timestamp.Format("2006-01-02 15:04:05"))
	}
}

// getWeChatMessage 企业微信消息格式
func (ns *NotificationService) getWeChatMessage(event *NotificationEvent) string {
	var statusEmoji string
	switch event.Type {
	case models.WebhookEventTicketCreated:
		statusEmoji = "🎫"
	case models.WebhookEventTicketAssigned:
		statusEmoji = "👤"
	case models.WebhookEventTicketResolved:
		statusEmoji = "✅"
	case models.WebhookEventTicketClosed:
		statusEmoji = "🔒"
	case models.WebhookEventSystemAlert:
		statusEmoji = "⚠️"
	default:
		statusEmoji = "📋"
	}

	return fmt.Sprintf(`%s **%s**

> %s

**工单编号**: %v
**时间**: %s
**类型**: %s`, 
		statusEmoji, event.Title, event.Description,
		event.Data["ticket_number"], event.Timestamp.Format("2006-01-02 15:04:05"),
		string(event.Type))
}

// getDingTalkMessage 钉钉消息格式
func (ns *NotificationService) getDingTalkMessage(event *NotificationEvent) string {
	return fmt.Sprintf(`# %s

%s

- **工单编号**: %v
- **时间**: %s
- **类型**: %s`, 
		event.Title, event.Description,
		event.Data["ticket_number"], event.Timestamp.Format("2006-01-02 15:04:05"),
		string(event.Type))
}

// getLarkMessage 飞书消息格式
func (ns *NotificationService) getLarkMessage(event *NotificationEvent) string {
	return fmt.Sprintf(`**%s**

%s

**详细信息**:
- 工单编号: %v
- 时间: %s  
- 类型: %s`, 
		event.Title, event.Description,
		event.Data["ticket_number"], event.Timestamp.Format("2006-01-02 15:04:05"),
		string(event.Type))
}

// BuildRequestBody 构建请求体（公开方法用于测试）
func (ns *NotificationService) BuildRequestBody(config *models.WebhookConfig, message string) ([]byte, error) {
	return ns.buildRequestBody(config, message)
}

// buildRequestBody 构建请求体
func (ns *NotificationService) buildRequestBody(config *models.WebhookConfig, message string) ([]byte, error) {
	switch config.Provider {
	case models.WebhookProviderWeChat:
		return ns.buildWeChatBody(message)
	case models.WebhookProviderDingTalk:
		return ns.buildDingTalkBody(message)
	case models.WebhookProviderLark:
		return ns.buildLarkBody(message)
	default:
		// 自定义webhook，使用通用格式
		return json.Marshal(map[string]interface{}{
			"text": message,
			"timestamp": time.Now().Unix(),
		})
	}
}

// buildWeChatBody 构建企业微信请求体
func (ns *NotificationService) buildWeChatBody(message string) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"content": message,
		},
	})
}

// buildDingTalkBody 构建钉钉请求体
func (ns *NotificationService) buildDingTalkBody(message string) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"msgtype": "markdown",
		"markdown": map[string]interface{}{
			"title": "工单系统通知",
			"text":  message,
		},
	})
}

// buildLarkBody 构建飞书请求体
func (ns *NotificationService) buildLarkBody(message string) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"msg_type": "text",
		"content": map[string]interface{}{
			"text": message,
		},
	})
}

// setRequestHeaders 设置请求头
func (ns *NotificationService) setRequestHeaders(req *http.Request, config *models.WebhookConfig, body []byte) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "TicketSystem-Webhook/1.0")

	// 钉钉签名
	if config.Provider == models.WebhookProviderDingTalk && config.Secret != "" {
		timestamp := time.Now().UnixMilli()
		sign := ns.generateDingTalkSign(timestamp, config.Secret)
		
		// 将签名添加到URL参数中
		originalURL := req.URL.String()
		if strings.Contains(originalURL, "?") {
			req.URL, _ = req.URL.Parse(originalURL + "&timestamp=" + strconv.FormatInt(timestamp, 10) + "&sign=" + sign)
		} else {
			req.URL, _ = req.URL.Parse(originalURL + "?timestamp=" + strconv.FormatInt(timestamp, 10) + "&sign=" + sign)
		}
	}

	// 飞书签名
	if config.Provider == models.WebhookProviderLark && config.Secret != "" {
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		sign := ns.generateLarkSign(timestamp, config.Secret)
		req.Header.Set("X-Lark-Request-Timestamp", timestamp)
		req.Header.Set("X-Lark-Request-Nonce", "ticket-system")
		req.Header.Set("X-Lark-Signature", sign)
	}
}

// generateDingTalkSign 生成钉钉签名
func (ns *NotificationService) generateDingTalkSign(timestamp int64, secret string) string {
	stringToSign := fmt.Sprintf("%d\n%s", timestamp, secret)
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// generateLarkSign 生成飞书签名
func (ns *NotificationService) generateLarkSign(timestamp, secret string) string {
	stringToSign := timestamp + "\n" + "ticket-system" + "\n" + secret
	h := hmac.New(sha256.New, []byte(stringToSign))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// updateConfigStats 更新配置统计
func (ns *NotificationService) updateConfigStats(configID uint, success bool, err error) {
	updates := map[string]interface{}{
		"last_triggered_at": time.Now(),
		"total_sent":       gorm.Expr("total_sent + 1"),
	}

	if success {
		updates["last_success_at"] = time.Now()
		updates["total_success"] = gorm.Expr("total_success + 1")
		updates["last_error"] = "" // 清除错误信息
	} else {
		updates["last_error_at"] = time.Now()
		updates["total_failed"] = gorm.Expr("total_failed + 1")
		if err != nil {
			updates["last_error"] = err.Error()
		}
	}

	ns.db.Model(&models.WebhookConfig{}).Where("id = ?", configID).Updates(updates)
}

// saveLog 保存日志
func (ns *NotificationService) saveLog(log *models.WebhookLog) {
	if err := ns.db.Create(log).Error; err != nil {
		// 记录日志失败，但不影响主流程
		fmt.Printf("保存webhook日志失败: %v\n", err)
	}
}

// TestWebhook 测试webhook配置
func (ns *NotificationService) TestWebhook(ctx context.Context, configID uint) error {
	var config models.WebhookConfig
	if err := ns.db.First(&config, configID).Error; err != nil {
		return fmt.Errorf("webhook配置不存在: %w", err)
	}

	// 创建测试事件
	testEvent := &NotificationEvent{
		Type:        models.WebhookEventSystemAlert,
		ResourceID:  0,
		ResourceType: "test",
		Title:       "Webhook测试通知",
		Description: "这是一个测试消息，用于验证Webhook配置是否正常工作。",
		Data: map[string]interface{}{
			"ticket_number": "TEST-001",
			"test": true,
		},
		Metadata: map[string]string{
			"source": "webhook_test",
		},
		Timestamp: time.Now(),
	}

	return ns.sendWebhook(ctx, &config, testEvent)
}

// RetryFailedWebhooks 重试失败的webhook
func (ns *NotificationService) RetryFailedWebhooks(ctx context.Context) error {
	var logs []models.WebhookLog
	
	// 查找需要重试的日志
	if err := ns.db.Where("status = ? AND next_retry_at IS NOT NULL AND next_retry_at <= ? AND retry_count < max_retries", 
		"retrying", time.Now()).
		Preload("Config").
		Find(&logs).Error; err != nil {
		return fmt.Errorf("查询重试日志失败: %w", err)
	}

	for _, log := range logs {
		if log.Config == nil {
			continue
		}

		// 重新构建事件
		var eventData NotificationEvent
		if err := json.Unmarshal([]byte(log.EventData), &eventData); err != nil {
			continue
		}

		// 更新重试计数
		log.RetryCount++
		ns.db.Save(&log)

		// 重新发送
		if err := ns.sendWebhook(ctx, log.Config, &eventData); err != nil {
			fmt.Printf("重试webhook失败 (ID: %d): %v\n", log.ID, err)
		}
	}

	return nil
}

// === 通知管理相关方法实现 ===

// CreateNotification 创建通知
func (ns *NotificationService) CreateNotification(ctx context.Context, req *models.NotificationCreateRequest) (*models.Notification, error) {
	notification := &models.Notification{
		Type:            req.Type,
		Title:           req.Title,
		Content:         req.Content,
		Priority:        req.Priority,
		Channel:         req.Channel,
		RecipientID:     req.RecipientID,
		SenderID:        req.SenderID,
		RelatedType:     req.RelatedType,
		RelatedID:       req.RelatedID,
		RelatedTicketID: req.RelatedTicketID,
		ActionURL:       req.ActionURL,
		ScheduledAt:     req.ScheduledAt,
		ExpiresAt:       req.ExpiresAt,
	}

	// 设置默认值
	if notification.Priority == "" {
		notification.Priority = models.NotificationPriorityNormal
	}
	if notification.Channel == "" {
		notification.Channel = models.NotificationChannelInApp
	}

	// 处理metadata
	if req.Metadata != nil {
		metadataBytes, err := json.Marshal(req.Metadata)
		if err == nil {
			notification.Metadata = string(metadataBytes)
		}
	}

	if err := ns.db.Create(notification).Error; err != nil {
		return nil, fmt.Errorf("创建通知失败: %w", err)
	}

	// 如果是邮件通知，异步发送邮件
	if notification.Channel == models.NotificationChannelEmail && ns.emailNotificationService != nil {
		go func() {
			// 创建一个新的上下文用于后台任务
			bgCtx := context.Background()
			if err := ns.emailNotificationService.SendEmailNotification(bgCtx, notification); err != nil {
				// 记录错误，但不影响主流程
				fmt.Printf("发送邮件通知失败 (ID: %d): %v\n", notification.ID, err)
			}
		}()
	}

	// 性能优化：跳过预加载相关数据以提高创建速度
	// 如果需要完整数据，调用方可以单独查询
	// ns.db.Preload("Recipient").Preload("Sender").Preload("RelatedTicket").First(notification, notification.ID)

	return notification, nil
}

// GetNotifications 获取通知列表
func (ns *NotificationService) GetNotifications(ctx context.Context, filter *models.NotificationFilter) ([]*models.Notification, int64, error) {
    baseQuery := ns.db.WithContext(ctx).Model(&models.Notification{})

    // 应用过滤条件
    if filter.RecipientID != nil {
        baseQuery = baseQuery.Where("recipient_id = ?", *filter.RecipientID)
    }
    if filter.SenderID != nil {
        baseQuery = baseQuery.Where("sender_id = ?", *filter.SenderID)
    }
    if len(filter.Types) > 0 {
        baseQuery = baseQuery.Where("type IN ?", filter.Types)
    }
    if len(filter.Priorities) > 0 {
        baseQuery = baseQuery.Where("priority IN ?", filter.Priorities)
    }
    if len(filter.Channels) > 0 {
        baseQuery = baseQuery.Where("channel IN ?", filter.Channels)
    }
    if filter.IsRead != nil {
        baseQuery = baseQuery.Where("is_read = ?", *filter.IsRead)
    }
    if filter.IsSent != nil {
        baseQuery = baseQuery.Where("is_sent = ?", *filter.IsSent)
    }
    if filter.IsDelivered != nil {
        baseQuery = baseQuery.Where("is_delivered = ?", *filter.IsDelivered)
    }
    if filter.RelatedType != "" {
        baseQuery = baseQuery.Where("related_type = ?", filter.RelatedType)
    }
    if filter.RelatedID != nil {
        baseQuery = baseQuery.Where("related_id = ?", *filter.RelatedID)
    }
    if filter.RelatedTicketID != nil {
        baseQuery = baseQuery.Where("related_ticket_id = ?", *filter.RelatedTicketID)
    }
    if filter.CreatedAfter != nil {
        baseQuery = baseQuery.Where("created_at >= ?", *filter.CreatedAfter)
    }
    if filter.CreatedBefore != nil {
        baseQuery = baseQuery.Where("created_at <= ?", *filter.CreatedBefore)
    }
    if filter.Query != "" {
        keyword := fmt.Sprintf("%%%s%%", filter.Query)
        baseQuery = baseQuery.Where("title ILIKE ? OR content ILIKE ?", keyword, keyword)
    }

    // 统计总数
    var total int64
    if err := baseQuery.Count(&total).Error; err != nil {
        return nil, 0, fmt.Errorf("统计通知数量失败: %w", err)
    }

    // 构建数据查询
    dataQuery := baseQuery.Session(&gorm.Session{NewDB: true})

    // 排序
    orderBy := "created_at"
    orderDir := "desc"
    if filter.OrderBy != "" {
        orderBy = filter.OrderBy
    }
    if filter.OrderDir != "" {
        orderDir = filter.OrderDir
    }
    dataQuery = dataQuery.Order(fmt.Sprintf("%s %s", orderBy, orderDir))

    // 分页
    if filter.Limit > 0 {
        dataQuery = dataQuery.Limit(filter.Limit)
    }
    if filter.Offset > 0 {
        dataQuery = dataQuery.Offset(filter.Offset)
    }

    // 预加载关联数据
    dataQuery = dataQuery.Preload("Recipient").Preload("Sender").Preload("RelatedTicket")

    var notifications []*models.Notification
    if err := dataQuery.Find(&notifications).Error; err != nil {
        return nil, 0, fmt.Errorf("获取通知列表失败: %w", err)
    }

    return notifications, total, nil
}

// MarkAsRead 标记通知为已读
func (ns *NotificationService) MarkAsRead(ctx context.Context, notificationID uint, userID uint) error {
	var notification models.Notification
	if err := ns.db.First(&notification, notificationID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("通知不存在")
		}
		return fmt.Errorf("查询通知失败: %w", err)
	}

	// 检查权限
	if notification.RecipientID != userID {
		return fmt.Errorf("无权限操作此通知")
	}

	// 标记为已读
	notification.MarkAsRead()
	if err := ns.db.Save(&notification).Error; err != nil {
		return fmt.Errorf("标记已读失败: %w", err)
	}

	return nil
}

// MarkAllAsRead 标记所有通知为已读
func (ns *NotificationService) MarkAllAsRead(ctx context.Context, userID uint) error {
	now := time.Now()
	updates := map[string]interface{}{
		"is_read":    true,
		"read_at":    &now,
		"updated_at": now,
	}

	if err := ns.db.Model(&models.Notification{}).
		Where("recipient_id = ? AND is_read = false", userID).
		Updates(updates).Error; err != nil {
		return fmt.Errorf("批量标记已读失败: %w", err)
	}

	return nil
}

// GetUnreadCount 获取未读通知数量
func (ns *NotificationService) GetUnreadCount(ctx context.Context, userID uint) (int64, error) {
	var count int64
	if err := ns.db.Model(&models.Notification{}).
		Where("recipient_id = ? AND is_read = false", userID).
		Count(&count).Error; err != nil {
		return 0, fmt.Errorf("获取未读数量失败: %w", err)
	}
	return count, nil
}

// GetNotificationPreferences 获取用户通知偏好设置
func (ns *NotificationService) GetNotificationPreferences(ctx context.Context, userID uint) ([]*models.NotificationPreference, error) {
	var preferences []*models.NotificationPreference
	if err := ns.db.Where("user_id = ?", userID).Find(&preferences).Error; err != nil {
		return nil, fmt.Errorf("获取通知偏好设置失败: %w", err)
	}
	return preferences, nil
}

// UpdateNotificationPreferences 更新用户通知偏好设置
func (ns *NotificationService) UpdateNotificationPreferences(ctx context.Context, userID uint, preferences []models.NotificationPreference) error {
	return ns.db.Transaction(func(tx *gorm.DB) error {
		// 删除现有设置
		if err := tx.Where("user_id = ?", userID).Delete(&models.NotificationPreference{}).Error; err != nil {
			return err
		}

		// 插入新设置
		for _, pref := range preferences {
			pref.UserID = userID
			pref.ID = 0 // 确保新建
			if err := tx.Create(&pref).Error; err != nil {
				return err
			}
		}

		return nil
	})
}

// === 自动通知生成方法 ===

// NotifyTicketStatusChanged 工单状态变更通知
func (ns *NotificationService) NotifyTicketStatusChanged(ctx context.Context, ticket *models.Ticket, oldStatus models.TicketStatus, userID uint) error {
	// 确定通知接收者
	recipients := []uint{}
	if ticket.AssignedToID != nil && *ticket.AssignedToID != userID {
		recipients = append(recipients, *ticket.AssignedToID)
	}
	if ticket.CreatedByID != userID {
		recipients = append(recipients, ticket.CreatedByID)
	}

	// 生成通知内容
	title := fmt.Sprintf("工单状态已更新 - %s", ticket.Title)
	content := fmt.Sprintf("工单 #%s 的状态从 %s 更新为 %s", ticket.TicketNumber, oldStatus, ticket.Status)

	// 为每个接收者创建通知
	for _, recipientID := range recipients {
		req := &models.NotificationCreateRequest{
			Type:            models.NotificationTypeTicketStatusChanged,
			Title:           title,
			Content:         content,
			Priority:        models.NotificationPriorityNormal,
			Channel:         models.NotificationChannelInApp,
			RecipientID:     recipientID,
			SenderID:        &userID,
			RelatedType:     "ticket",
			RelatedID:       &ticket.ID,
			RelatedTicketID: &ticket.ID,
			ActionURL:       fmt.Sprintf("/tickets/%d", ticket.ID),
			Metadata: map[string]interface{}{
				"old_status":     string(oldStatus),
				"new_status":     string(ticket.Status),
				"ticket_number":  ticket.TicketNumber,
			},
		}

		if _, err := ns.CreateNotification(ctx, req); err != nil {
			return fmt.Errorf("创建状态变更通知失败: %w", err)
		}
	}

	return nil
}

// NotifyTicketAssigned 工单分配通知
func (ns *NotificationService) NotifyTicketAssigned(ctx context.Context, ticket *models.Ticket, userID uint) error {
	if ticket.AssignedToID == nil || *ticket.AssignedToID == userID {
		return nil // 没有分配或自己分配给自己，不发通知
	}

	req := &models.NotificationCreateRequest{
		Type:            models.NotificationTypeTicketAssigned,
		Title:           fmt.Sprintf("新工单已分配 - %s", ticket.Title),
		Content:         fmt.Sprintf("工单 #%s 已分配给您，请及时处理", ticket.TicketNumber),
		Priority:        models.NotificationPriorityHigh,
		Channel:         models.NotificationChannelInApp,
		RecipientID:     *ticket.AssignedToID,
		SenderID:        &userID,
		RelatedType:     "ticket",
		RelatedID:       &ticket.ID,
		RelatedTicketID: &ticket.ID,
		ActionURL:       fmt.Sprintf("/tickets/%d", ticket.ID),
		Metadata: map[string]interface{}{
			"ticket_number": ticket.TicketNumber,
			"priority":      string(ticket.Priority),
		},
	}

	_, err := ns.CreateNotification(ctx, req)
	return err
}

// === 邮件通知处理方法 ===

// ProcessPendingEmailNotifications 处理待发送的邮件通知
func (ns *NotificationService) ProcessPendingEmailNotifications(ctx context.Context) error {
	if ns.emailNotificationService == nil {
		return fmt.Errorf("邮件通知服务未初始化")
	}

	// 查询待发送的邮件通知
	var notifications []*models.Notification
	err := ns.db.Where("channel = ? AND is_sent = false AND (scheduled_at IS NULL OR scheduled_at <= ?)", 
		models.NotificationChannelEmail, time.Now()).
		Preload("Recipient").
		Preload("Sender").
		Preload("RelatedTicket").
		Find(&notifications).Error

	if err != nil {
		return fmt.Errorf("查询待发送邮件通知失败: %w", err)
	}

	if len(notifications) == 0 {
		return nil
	}

	successCount := 0
	failedCount := 0

	for _, notification := range notifications {
		if err := ns.emailNotificationService.SendEmailNotification(ctx, notification); err != nil {
			failedCount++
			fmt.Printf("发送邮件通知失败 (ID: %d): %v\n", notification.ID, err)
			continue
		}
		successCount++
	}

	fmt.Printf("邮件通知处理完成: 成功 %d, 失败 %d\n", successCount, failedCount)

	if failedCount > 0 {
		return fmt.Errorf("部分邮件发送失败: 成功 %d, 失败 %d", successCount, failedCount)
	}

	return nil
}

// RetryFailedEmailNotifications 重试失败的邮件通知
func (ns *NotificationService) RetryFailedEmailNotifications(ctx context.Context) error {
	if ns.emailNotificationService == nil {
		return fmt.Errorf("邮件通知服务未初始化")
	}

	// 查询需要重试的邮件通知
	var notifications []*models.Notification
	err := ns.db.Where(
		"channel = ? AND is_sent = false AND delivery_status = ? AND next_retry_at IS NOT NULL AND next_retry_at <= ? AND retry_count < max_retries",
		models.NotificationChannelEmail, "failed", time.Now()).
		Preload("Recipient").
		Preload("Sender").
		Preload("RelatedTicket").
		Find(&notifications).Error

	if err != nil {
		return fmt.Errorf("查询重试邮件通知失败: %w", err)
	}

	if len(notifications) == 0 {
		return nil
	}

	successCount := 0
	failedCount := 0

	for _, notification := range notifications {
		if err := ns.emailNotificationService.SendEmailNotification(ctx, notification); err != nil {
			failedCount++
			fmt.Printf("重试发送邮件通知失败 (ID: %d): %v\n", notification.ID, err)
			continue
		}
		successCount++
	}

	fmt.Printf("邮件通知重试完成: 成功 %d, 失败 %d\n", successCount, failedCount)

	if failedCount > 0 {
		return fmt.Errorf("部分邮件重试失败: 成功 %d, 失败 %d", successCount, failedCount)
	}

	return nil
}
