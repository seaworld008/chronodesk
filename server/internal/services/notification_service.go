package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/safeconv"
	"github.com/seaworld008/chronodesk/server/internal/security"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// NotificationServiceInterface 通知服务接口
type NotificationServiceInterface interface {
	// 通知管理相关方法
	CreateNotification(ctx context.Context, req *models.NotificationCreateRequest) (*models.Notification, error)
	GetNotifications(ctx context.Context, filter *models.NotificationFilter) ([]*models.Notification, int64, error)
	DeleteNotification(ctx context.Context, notificationID uint) error
	MarkAsRead(ctx context.Context, notificationID uint, userID uint) error
	MarkAllAsRead(ctx context.Context, userID uint) error
	GetUnreadCount(ctx context.Context, userID uint) (int64, error)

	// 通知偏好设置
	GetNotificationPreferences(ctx context.Context, userID uint) ([]*models.NotificationPreference, error)
	UpdateNotificationPreferences(ctx context.Context, userID uint, preferences []models.NotificationPreference) error

	// 邮件通知相关方法
	ProcessPendingEmailNotifications(ctx context.Context) error
	RetryFailedEmailNotifications(ctx context.Context) error
	SetEmailNotificationService(emailService EmailNotificationServiceInterface)
}

// NotificationService 通知服务
type NotificationService struct {
	db                       *gorm.DB
	emailNotificationService EmailNotificationServiceInterface
	outboxWebhookTimeout     time.Duration
	environment              string
	secretStore              security.Protector
	webhookClients           WebhookClientFactory
}

// WebhookClientFactory is the explicit outbound-network policy boundary.
// Production constructors install the DNS-pinning public-HTTPS
// implementation. Alternate policies require explicit composition.
type WebhookClientFactory interface {
	ClientFor(
		context.Context,
		*url.URL,
		time.Duration,
	) (*http.Client, error)
}

type WebhookClientFactoryFunc func(
	context.Context,
	*url.URL,
	time.Duration,
) (*http.Client, error)

func (f WebhookClientFactoryFunc) ClientFor(
	ctx context.Context,
	target *url.URL,
	timeout time.Duration,
) (*http.Client, error) {
	return f(ctx, target, timeout)
}

type publicWebhookClientFactory struct {
	resolver *net.Resolver
}

func (f publicWebhookClientFactory) ClientFor(
	ctx context.Context,
	target *url.URL,
	timeout time.Duration,
) (*http.Client, error) {
	return security.NewPinnedHTTPSClient(ctx, target, f.resolver, timeout)
}

const (
	defaultOutboxWebhookAttemptTimeout = 20 * time.Second

	// NotificationOutboxDestination is the durable in-app notification
	// consumer. It is deliberately distinct from "webhook": database-backed
	// notifications and external HTTP callbacks have different retry and
	// idempotency boundaries.
	NotificationOutboxDestination = "notification"

	notificationOutboxMaxAttempts  = 8
	notificationSourceKeyMaxLength = 191
)

// TicketAssignedNotificationOutboxTargets snapshots the intended recipient in
// the Outbox destination. The event payload carries display data, while the
// destination remains the authoritative fan-out decision made in the ticket
// transaction.
func TicketAssignedNotificationOutboxTargets(
	ticket *models.Ticket,
	actorID uint,
) []OutboxTarget {
	if ticket == nil ||
		ticket.AssignedToID == nil ||
		*ticket.AssignedToID == 0 ||
		*ticket.AssignedToID == actorID {
		return nil
	}
	return []OutboxTarget{newTicketNotificationOutboxTarget(
		models.NotificationTypeTicketAssigned,
		*ticket.AssignedToID,
	)}
}

// TicketStatusNotificationOutboxTargets keeps the historical recipient rules
// while deduplicating a creator who is also the assignee.
func TicketStatusNotificationOutboxTargets(
	ticket *models.Ticket,
	actorID uint,
) []OutboxTarget {
	if ticket == nil {
		return nil
	}
	recipients := make([]uint, 0, 2)
	seen := make(map[uint]struct{}, 2)
	add := func(recipientID uint) {
		if recipientID == 0 || recipientID == actorID {
			return
		}
		if _, exists := seen[recipientID]; exists {
			return
		}
		seen[recipientID] = struct{}{}
		recipients = append(recipients, recipientID)
	}
	if ticket.AssignedToID != nil {
		add(*ticket.AssignedToID)
	}
	add(ticket.CreatedByID)

	targets := make([]OutboxTarget, 0, len(recipients))
	for _, recipientID := range recipients {
		targets = append(targets, newTicketNotificationOutboxTarget(
			models.NotificationTypeTicketStatusChanged,
			recipientID,
		))
	}
	return targets
}

func newTicketNotificationOutboxTarget(
	notificationType models.NotificationType,
	recipientID uint,
) OutboxTarget {
	return OutboxTarget{
		Type: NotificationOutboxDestination,
		ID: fmt.Sprintf(
			"%s:%d",
			notificationType,
			recipientID,
		),
		MaxAttempts: notificationOutboxMaxAttempts,
	}
}

// NewNotificationService 创建通知服务实例
func NewNotificationService(db *gorm.DB) *NotificationService {
	protector, _ := security.LoadDeploymentKeyringFromEnvironment()
	return NewNotificationServiceWithProtector(db, protector)
}

// NewNotificationServiceWithProtector injects the at-rest encryption keyring.
// A missing keyring is fail-closed whenever a webhook credential is present.
func NewNotificationServiceWithProtector(
	db *gorm.DB,
	protector security.Protector,
) *NotificationService {
	return NewNotificationServiceWithClientFactory(db, protector, nil)
}

func NewNotificationServiceWithClientFactory(
	db *gorm.DB,
	protector security.Protector,
	factory WebhookClientFactory,
) *NotificationService {
	if factory == nil {
		factory = publicWebhookClientFactory{resolver: net.DefaultResolver}
	}
	return &NotificationService{
		db:                   db,
		outboxWebhookTimeout: defaultOutboxWebhookAttemptTimeout,
		environment:          getRuntimeEnvironment(),
		secretStore:          protector,
		webhookClients:       factory,
	}
}

// SetEmailNotificationService 设置邮件通知服务（依赖注入）
func (ns *NotificationService) SetEmailNotificationService(emailService EmailNotificationServiceInterface) {
	ns.emailNotificationService = emailService
}

// NotificationEvent 通知事件
type NotificationEvent struct {
	Type         models.WebhookEventType `json:"type"`
	ResourceID   uint                    `json:"resource_id"`
	ResourceType string                  `json:"resource_type"`
	Title        string                  `json:"title"`
	Description  string                  `json:"description"`
	Data         map[string]interface{}  `json:"data"`
	Metadata     map[string]string       `json:"metadata"`
	Timestamp    time.Time               `json:"timestamp"`
	UserID       *uint                   `json:"user_id,omitempty"`
}

// DeleteNotification 删除通知
func (ns *NotificationService) DeleteNotification(ctx context.Context, notificationID uint) error {
	var notification models.Notification
	if err := ns.db.WithContext(ctx).First(&notification, notificationID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("notification not found")
		}
		return fmt.Errorf("failed to find notification: %w", err)
	}

	if err := ns.db.WithContext(ctx).Delete(&notification).Error; err != nil {
		return fmt.Errorf("failed to delete notification: %w", err)
	}

	return nil
}

func getRuntimeEnvironment() string {
	env := strings.TrimSpace(os.Getenv("ENVIRONMENT"))
	if env == "" {
		return "development"
	}
	return env
}

// getActiveWebhooks 获取活跃的webhook配置
func (ns *NotificationService) getActiveWebhooks(
	ctx context.Context,
	eventType models.WebhookEventType,
) ([]*models.WebhookConfig, error) {
	var configs []*models.WebhookConfig

	// 查询活跃状态的webhook配置
	if err := ns.db.WithContext(ctx).Where("status = ?", models.WebhookStatusActive).
		Find(&configs).Error; err != nil {
		return nil, err
	}

	// 过滤支持该事件类型的配置
	var filtered []*models.WebhookConfig
	for _, config := range configs {
		if err := ns.revealWebhookSecrets(config); err != nil {
			return nil, fmt.Errorf("无法读取webhook凭据: %w", err)
		}
		if config.IsEventEnabled(eventType) {
			filtered = append(filtered, config)
		}
	}

	return filtered, nil
}

// WebhookOutboxTarget describes one independently retryable webhook
// destination. MaxAttempts includes the initial attempt.
type WebhookOutboxTarget struct {
	ConfigID    uint
	MaxAttempts int
}

// ListWebhookOutboxTargets snapshots the active subscriptions for a domain
// event before the fan-out delivery is acknowledged.
func (ns *NotificationService) ListWebhookOutboxTargets(
	ctx context.Context,
	eventType models.WebhookEventType,
) ([]WebhookOutboxTarget, error) {
	configs, err := ns.getActiveWebhooks(ctx, eventType)
	if err != nil {
		return nil, err
	}
	targets := make([]WebhookOutboxTarget, 0, len(configs))
	for _, config := range configs {
		maxAttempts := config.RetryCount + 1
		if maxAttempts < 1 {
			maxAttempts = 1
		}
		if maxAttempts > 11 {
			maxAttempts = 11
		}
		targets = append(targets, WebhookOutboxTarget{
			ConfigID:    config.ID,
			MaxAttempts: maxAttempts,
		})
	}
	return targets, nil
}

// SendWebhookOutboxAttempt performs exactly one bounded HTTP attempt. It does
// not schedule WebhookLog retries: the durable Outbox owns retry timing through
// outbox_deliveries.next_attempt_at.
func (ns *NotificationService) SendWebhookOutboxAttempt(
	ctx context.Context,
	configID uint,
	event *NotificationEvent,
) error {
	if event == nil {
		return errors.New("webhook event is required")
	}
	var config models.WebhookConfig
	err := ns.db.WithContext(ctx).
		Where("id = ? AND status = ?", configID, models.WebhookStatusActive).
		First(&config).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// A removed or disabled subscription no longer needs delivery.
		return nil
	}
	if err != nil {
		return fmt.Errorf("load webhook configuration: %w", err)
	}
	if err := ns.revealWebhookSecrets(&config); err != nil {
		return fmt.Errorf("无法读取webhook凭据: %w", err)
	}
	if !config.IsEventEnabled(event.Type) {
		return nil
	}
	timeout := ns.outboxWebhookTimeout
	if timeout <= 0 || timeout > defaultOutboxWebhookAttemptTimeout {
		timeout = defaultOutboxWebhookAttemptTimeout
	}
	if config.TimeoutSeconds > 0 {
		configured := time.Duration(config.TimeoutSeconds) * time.Second
		if configured < timeout {
			timeout = configured
		}
	}
	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return ns.sendWebhookAttempt(attemptCtx, &config, event)
}

func (ns *NotificationService) sendWebhookAttempt(
	ctx context.Context,
	config *models.WebhookConfig,
	event *NotificationEvent,
) error {
	startTime := time.Now()

	// 创建日志记录
	log := &models.WebhookLog{
		ConfigID:     config.ID,
		EventType:    event.Type,
		ResourceID:   event.ResourceID,
		ResourceType: event.ResourceType,
		Status:       "pending",
		MaxRetries:   0,
		Environment:  ns.environment,
	}

	// 序列化事件数据
	eventDataBytes, _ := json.Marshal(event)
	log.EventData = string(eventDataBytes)

	// 生成消息内容
	message, err := ns.generateMessage(config, event)
	if err != nil {
		log.Status = "failed"
		log.ErrorMessage = fmt.Sprintf("生成消息失败: %v", err)
		ns.saveLog(log, config)
		return err
	}

	// 构建请求
	requestBody, err := ns.buildRequestBodyForEvent(config, message, event)
	if err != nil {
		log.Status = "failed"
		log.ErrorMessage = fmt.Sprintf("构建请求失败: %v", err)
		ns.saveLog(log, config)
		return err
	}

	log.RequestURL = webhookEndpointForLog(config.WebhookURL)
	log.RequestMethod = "POST"
	log.RequestBody = string(requestBody)

	// 创建HTTP请求
	req, err := http.NewRequestWithContext(ctx, "POST", config.WebhookURL, bytes.NewBuffer(requestBody))
	if err != nil {
		log.Status = "failed"
		log.ErrorMessage = "创建webhook请求失败"
		ns.saveLog(log, config)
		return errors.New("webhook请求地址无效")
	}

	// 设置请求头
	ns.setRequestHeaders(req, config, requestBody)
	if eventID := strings.TrimSpace(event.Metadata["event_id"]); eventID != "" &&
		!strings.ContainsAny(eventID, "\r\n") {
		req.Header.Set("X-CloudEvents-ID", eventID)
		idempotencyKey := strings.TrimSpace(event.Metadata["delivery_id"])
		if idempotencyKey == "" {
			idempotencyKey = fmt.Sprintf("%s:webhook:%d", eventID, config.ID)
		}
		if !strings.ContainsAny(idempotencyKey, "\r\n") {
			req.Header.Set("Idempotency-Key", idempotencyKey)
			req.Header.Set("X-ChronoDesk-Delivery-ID", idempotencyKey)
		}
	}

	// 日志只保留严格白名单中的非敏感头。Authorization、签名、Cookie
	// 以及所有未知扩展头永不落盘。
	log.RequestHeaders = headersForAuditLog(req.Header, requestHeaderAuditAllowlist)

	client, err := ns.webhookClients.ClientFor(
		ctx,
		req.URL,
		ns.outboxWebhookTimeout,
	)
	if err != nil || client == nil {
		log.Status = "failed"
		log.ErrorMessage = "webhook目标地址未通过安全校验"
		ns.saveLog(log, config)
		return errors.New("webhook目标地址未通过安全校验")
	}
	defer client.CloseIdleConnections()

	// 发送请求
	resp, err := client.Do(req)
	log.ResponseTime = time.Since(startTime).Milliseconds()

	if err != nil {
		log.Status = "failed"
		log.ErrorMessage = "webhook请求发送失败"
		ns.saveLog(log, config)
		return errors.New("webhook请求发送失败")
	}
	defer resp.Body.Close()

	// 读取响应
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	log.ResponseStatus = resp.StatusCode
	// 回调响应由外部系统控制，可能回显 Authorization、签名或 Cookie。
	// 响应正文不进入持久日志。
	log.ResponseBody = ""

	// 响应头同样采用严格白名单，排除 Set-Cookie、认证挑战和供应商
	// 自定义的敏感头。
	log.ResponseHeaders = headersForAuditLog(resp.Header, responseHeaderAuditAllowlist)

	// 判断请求是否成功
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Status = "success"

		// 更新配置统计
		ns.updateConfigStats(config.ID, true, nil)
	} else {
		log.Status = "failed"
		log.ErrorMessage = fmt.Sprintf("HTTP错误: %d", resp.StatusCode)

		// 更新配置统计
		ns.updateConfigStats(config.ID, false, fmt.Errorf("HTTP %d", resp.StatusCode))

	}

	// 保存日志
	ns.saveLog(log, config)

	// Any non-2xx response must remain an error. The durable Agent Outbox is
	// the recovery owner for domain-event delivery; returning nil for a
	// locally marked "retrying" log would permanently acknowledge a failed
	// external side effect.
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
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
	return ns.buildRequestBodyForEvent(config, message, nil)
}

func (ns *NotificationService) buildRequestBodyForEvent(
	config *models.WebhookConfig,
	message string,
	event *NotificationEvent,
) ([]byte, error) {
	switch config.Provider {
	case models.WebhookProviderWeChat:
		return ns.buildWeChatBody(message)
	case models.WebhookProviderDingTalk:
		return ns.buildDingTalkBody(message)
	case models.WebhookProviderLark:
		return ns.buildLarkBody(message)
	default:
		// 自定义webhook，使用通用格式
		payload := map[string]interface{}{
			"text":      message,
			"timestamp": time.Now().Unix(),
		}
		if event != nil {
			payload["timestamp"] = event.Timestamp.UTC().Unix()
			if eventID := strings.TrimSpace(event.Metadata["event_id"]); eventID != "" {
				payload["event_id"] = eventID
			}
			if deliveryID := strings.TrimSpace(event.Metadata["delivery_id"]); deliveryID != "" {
				payload["delivery_id"] = deliveryID
			}
		}
		return json.Marshal(payload)
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
	if config.AccessToken != "" && !strings.ContainsAny(config.AccessToken, "\r\n") {
		req.Header.Set("Authorization", "Bearer "+config.AccessToken)
	}

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

var (
	requestHeaderAuditAllowlist = map[string]struct{}{
		"Content-Type":             {},
		"User-Agent":               {},
		"X-Cloudevents-Id":         {},
		"Idempotency-Key":          {},
		"X-Chronodesk-Delivery-Id": {},
	}
	responseHeaderAuditAllowlist = map[string]struct{}{
		"Content-Type": {},
		"Date":         {},
		"Retry-After":  {},
	}
)

func headersForAuditLog(header http.Header, allowed map[string]struct{}) string {
	safe := make(http.Header, len(allowed))
	for rawName := range allowed {
		name := http.CanonicalHeaderKey(rawName)
		values := header.Values(name)
		if len(values) == 0 {
			continue
		}
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" || strings.ContainsAny(value, "\r\n") {
				continue
			}
			if len(value) > 512 {
				value = value[:512]
			}
			safe.Add(name, value)
		}
	}
	encoded, err := json.Marshal(safe)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func webhookEndpointForLog(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	// Webhook paths and query strings frequently contain bearer-like tokens.
	// The origin is sufficient for operations and cannot reveal those values.
	return parsed.Scheme + "://" + parsed.Host
}

func scrubWebhookLog(log *models.WebhookLog, config *models.WebhookConfig) {
	if log == nil || config == nil {
		return
	}
	fields := []*string{
		&log.EventData,
		&log.RequestURL,
		&log.RequestHeaders,
		&log.RequestBody,
		&log.ResponseHeaders,
		&log.ResponseBody,
		&log.ErrorMessage,
	}
	for _, secret := range []string{config.Secret, config.AccessToken} {
		if secret == "" {
			continue
		}
		for _, field := range fields {
			*field = strings.ReplaceAll(*field, secret, "[REDACTED]")
		}
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
		"total_sent":        gorm.Expr("total_sent + 1"),
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
func (ns *NotificationService) saveLog(
	log *models.WebhookLog,
	config *models.WebhookConfig,
) {
	scrubWebhookLog(log, config)
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
	if err := ns.revealWebhookSecrets(&config); err != nil {
		return fmt.Errorf("无法读取webhook凭据: %w", err)
	}

	// 创建测试事件
	testEvent := &NotificationEvent{
		Type:         models.WebhookEventSystemAlert,
		ResourceID:   0,
		ResourceType: "test",
		Title:        "Webhook测试通知",
		Description:  "这是一个测试消息，用于验证Webhook配置是否正常工作。",
		Data: map[string]interface{}{
			"ticket_number": "TEST-001",
			"test":          true,
		},
		Metadata: map[string]string{
			"source": "webhook_test",
		},
		Timestamp: time.Now(),
	}

	return ns.sendWebhookAttempt(ctx, &config, testEvent)
}

func (ns *NotificationService) revealWebhookSecrets(config *models.WebhookConfig) error {
	if config == nil {
		return errors.New("webhook配置不能为空")
	}
	rowID := strconv.FormatUint(uint64(config.ID), 10)
	secret, err := security.RevealOptional(
		ns.secretStore,
		config.Secret,
		security.FieldAAD("webhook_configs", rowID, "secret"),
	)
	if err != nil {
		return err
	}
	accessToken, err := security.RevealOptional(
		ns.secretStore,
		config.AccessToken,
		security.FieldAAD("webhook_configs", rowID, "access_token"),
	)
	if err != nil {
		return err
	}
	config.Secret = secret
	config.AccessToken = accessToken
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
			bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
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
	if filter == nil {
		filter = &models.NotificationFilter{}
	}
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

	// 在 Count 修改 statement 之前克隆一个保留全部对象级过滤条件的数据
	// 查询。NewDB 会清空 WHERE，曾导致 total 正确但 items 混入其他用户
	// 通知，形成对象级越权。
	dataQuery := baseQuery.Session(&gorm.Session{})

	// 统计总数
	var total int64
	if err := baseQuery.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计通知数量失败: %w", err)
	}

	// 排序列由固定映射构造，绝不把请求字符串作为 SQL 片段传给 GORM。
	order := clause.OrderByColumn{
		Column: clause.Column{Name: "created_at"},
		Desc:   true,
	}
	switch filter.OrderBy {
	case "id":
		order.Column.Name = "id"
	case "updated_at":
		order.Column.Name = "updated_at"
	case "priority":
		order.Column.Name = "priority"
	case "type":
		order.Column.Name = "type"
	case "channel":
		order.Column.Name = "channel"
	case "is_read":
		order.Column.Name = "is_read"
	case "created_at":
		order.Column.Name = "created_at"
	}
	if strings.EqualFold(filter.OrderDir, "asc") {
		order.Desc = false
	}
	dataQuery = dataQuery.Clauses(clause.OrderBy{Columns: []clause.OrderByColumn{order}})

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

type ticketNotificationEventData struct {
	TicketID       uint                  `json:"ticket_id"`
	TicketNumber   string                `json:"ticket_number"`
	TicketTitle    string                `json:"ticket_title"`
	TicketPriority models.TicketPriority `json:"ticket_priority"`
	AssignedToID   uint                  `json:"assigned_to_id"`
	OldStatus      models.TicketStatus   `json:"old_status"`
	NewStatus      models.TicketStatus   `json:"new_status"`
}

// DeliverTicketNotificationOutbox persists one in-app notification from a
// committed CloudEvent. The unique source_event_key closes the classic Outbox
// crash window: if the process stops after INSERT but before acknowledging the
// delivery, replay observes the existing row instead of creating a duplicate.
func (ns *NotificationService) DeliverTicketNotificationOutbox(
	ctx context.Context,
	event CloudEventEnvelope,
	destinationID string,
) (*models.Notification, bool, error) {
	if ns == nil || ns.db == nil {
		return nil, false, errors.New("notification service is unavailable")
	}
	if event.ID == "" {
		return nil, false, errors.New("notification CloudEvent ID is required")
	}
	if event.ActorType != models.ActorTypeHuman {
		return nil, false, fmt.Errorf(
			"notification Outbox requires a human actor, got %q",
			event.ActorType,
		)
	}
	senderID, err := safeconv.ParsePositiveUint(event.ActorID)
	if err != nil {
		return nil, false, errors.New("notification CloudEvent has an invalid human actor")
	}

	notificationType, recipientID, err := parseTicketNotificationDestination(destinationID)
	if err != nil {
		return nil, false, err
	}
	if recipientID == senderID {
		return nil, false, errors.New("notification recipient must differ from the actor")
	}

	var data ticketNotificationEventData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return nil, false, fmt.Errorf("decode notification event data: %w", err)
	}
	if data.TicketID == 0 ||
		strings.TrimSpace(data.TicketNumber) == "" ||
		strings.TrimSpace(data.TicketTitle) == "" {
		return nil, false, errors.New("notification event is missing its ticket snapshot")
	}

	notification := &models.Notification{
		Type:            notificationType,
		Priority:        models.NotificationPriorityNormal,
		Channel:         models.NotificationChannelInApp,
		RecipientID:     recipientID,
		SenderID:        &senderID,
		RelatedType:     "ticket",
		RelatedID:       &data.TicketID,
		RelatedTicketID: &data.TicketID,
		ActionURL:       fmt.Sprintf("/tickets/%d", data.TicketID),
	}
	metadata := map[string]any{
		"source_event_id":  event.ID,
		"ticket_number":    data.TicketNumber,
		"resource_version": event.ResourceVersion,
	}
	var ticketCount int64
	if err := ns.db.WithContext(ctx).
		Model(&models.Ticket{}).
		Where("id = ?", data.TicketID).
		Count(&ticketCount).Error; err != nil {
		return nil, false, fmt.Errorf("validate notification ticket reference: %w", err)
	}
	if ticketCount == 0 {
		// CloudEvent carries the immutable ticket snapshot, so the notification
		// remains useful after ticket deletion. Do not let a stale optional FK
		// poison the durable Outbox with endless retries.
		notification.RelatedTicketID = nil
		notification.ActionURL = ""
		metadata["ticket_deleted"] = true
	}

	switch notificationType {
	case models.NotificationTypeTicketAssigned:
		if event.Type != "io.chronodesk.ticket.assigned.v1" &&
			event.Type != "io.chronodesk.ticket.updated.v1" {
			return nil, false, fmt.Errorf(
				"ticket assignment notification does not support event %q",
				event.Type,
			)
		}
		if data.AssignedToID == 0 || data.AssignedToID != recipientID {
			return nil, false, errors.New("assignment notification recipient does not match event data")
		}
		notification.Title = truncateNotificationTitle(
			"新工单已分配 - "+data.TicketTitle,
			255,
		)
		notification.Content = fmt.Sprintf(
			"工单 #%s 已分配给您，请及时处理",
			data.TicketNumber,
		)
		notification.Priority = models.NotificationPriorityHigh
		metadata["priority"] = string(data.TicketPriority)
	case models.NotificationTypeTicketStatusChanged:
		if event.Type != "io.chronodesk.ticket.transitioned.v1" &&
			event.Type != "io.chronodesk.ticket.updated.v1" {
			return nil, false, fmt.Errorf(
				"ticket status notification does not support event %q",
				event.Type,
			)
		}
		if !data.OldStatus.IsValid() ||
			!data.NewStatus.IsValid() ||
			data.OldStatus == data.NewStatus {
			return nil, false, errors.New("status notification event has an invalid transition")
		}
		notification.Title = truncateNotificationTitle(
			"工单状态已更新 - "+data.TicketTitle,
			255,
		)
		notification.Content = fmt.Sprintf(
			"工单 #%s 的状态从 %s 更新为 %s",
			data.TicketNumber,
			data.OldStatus,
			data.NewStatus,
		)
		metadata["old_status"] = string(data.OldStatus)
		metadata["new_status"] = string(data.NewStatus)
	default:
		return nil, false, fmt.Errorf(
			"unsupported ticket notification type %q",
			notificationType,
		)
	}

	sourceEventKey := event.ID + ":" + destinationID
	if len(sourceEventKey) > notificationSourceKeyMaxLength {
		return nil, false, errors.New("notification source event key is too long")
	}
	notification.SourceEventKey = &sourceEventKey
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return nil, false, fmt.Errorf("encode notification metadata: %w", err)
	}
	notification.Metadata = string(metadataBytes)

	result := ns.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "source_event_key"}},
		DoNothing: true,
	}).Create(notification)
	if result.Error != nil {
		return nil, false, fmt.Errorf("persist Outbox notification: %w", result.Error)
	}
	if result.RowsAffected == 1 {
		return notification, true, nil
	}

	var existing models.Notification
	if err := ns.db.WithContext(ctx).
		Where("source_event_key = ?", sourceEventKey).
		First(&existing).Error; err != nil {
		return nil, false, fmt.Errorf("load idempotent Outbox notification: %w", err)
	}
	if existing.Type != notification.Type ||
		existing.RecipientID != notification.RecipientID ||
		!sameOptionalTicketReference(existing.RelatedTicketID, notification.RelatedTicketID) {
		return nil, false, errors.New("notification source event key collision")
	}
	return &existing, false, nil
}

func sameOptionalTicketReference(left, right *uint) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func parseTicketNotificationDestination(
	destinationID string,
) (models.NotificationType, uint, error) {
	rawType, rawRecipient, found := strings.Cut(destinationID, ":")
	if !found || strings.Contains(rawRecipient, ":") {
		return "", 0, errors.New("invalid notification Outbox destination")
	}
	notificationType := models.NotificationType(rawType)
	if notificationType != models.NotificationTypeTicketAssigned &&
		notificationType != models.NotificationTypeTicketStatusChanged {
		return "", 0, fmt.Errorf(
			"unsupported notification Outbox destination %q",
			destinationID,
		)
	}
	value, err := safeconv.ParsePositiveUint(rawRecipient)
	if err != nil {
		return "", 0, errors.New("invalid notification recipient")
	}
	return notificationType, value, nil
}

func truncateNotificationTitle(value string, maxRunes int) string {
	runes := []rune(value)
	if maxRunes <= 0 || len(runes) <= maxRunes {
		return value
	}
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
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
