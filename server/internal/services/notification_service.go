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

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/safeconv"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
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
	webhookTestProjects      *ProjectService
	webhookTestEvents        *AgentNativeService
}

func withNotificationProjectOperation[T any](
	service *NotificationService,
	ctx context.Context,
	scope models.ProjectScope,
	run func(context.Context) (T, error),
) (T, error) {
	var zero T
	if service == nil || service.db == nil || run == nil {
		return zero, errors.New("notification project operation is unavailable")
	}
	if err := scope.Validate(); err != nil {
		return zero, err
	}
	if scopeddb.HasTransaction(ctx) {
		return run(ctx)
	}
	var (
		result       T
		operationErr error
	)
	transactionErr := scopeddb.WithProjectScopeContextTransaction(
		ctx,
		service.db,
		scope,
		func(scopedContext context.Context) error {
			result, operationErr = run(scopedContext)
			return operationErr
		},
	)
	if transactionErr != nil {
		return zero, transactionErr
	}
	return result, operationErr
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

var (
	ErrWebhookTestAccessDenied = errors.New("webhook test access denied")
	ErrWebhookTestNotFound     = errors.New("webhook test configuration not found")
	ErrWebhookTestUnavailable  = errors.New("webhook test command is unavailable")
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
	if ticket.CreatedByID != nil {
		add(*ticket.CreatedByID)
	}

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

// ConfigureWebhookTestCommands installs the authorization and event-writing
// collaborators used by the Human test-delivery command. The same
// NotificationService remains the Outbox worker's delivery adapter; only the
// command path uses these dependencies.
func (ns *NotificationService) ConfigureWebhookTestCommands(
	projects *ProjectService,
	events *AgentNativeService,
) {
	if ns == nil {
		return
	}
	ns.webhookTestProjects = projects
	ns.webhookTestEvents = events
}

// NotificationEvent 通知事件
type NotificationEvent struct {
	Type             models.WebhookEventType `json:"type"`
	TransitionStatus models.TicketStatus     `json:"transition_status,omitempty"`
	ResourceID       uint                    `json:"resource_id"`
	ResourceType     string                  `json:"resource_type"`
	Title            string                  `json:"title"`
	Description      string                  `json:"description"`
	Data             map[string]interface{}  `json:"data"`
	Metadata         map[string]string       `json:"metadata"`
	Timestamp        time.Time               `json:"timestamp"`
	UserID           *uint                   `json:"user_id,omitempty"`
}

// DeleteNotification 删除通知
func (ns *NotificationService) DeleteNotification(ctx context.Context, notificationID uint) error {
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return fmt.Errorf("notification project scope: %w", err)
	}
	if !scopeddb.HasTransaction(ctx) {
		_, err := withNotificationProjectOperation(
			ns,
			ctx,
			scope,
			func(scopedContext context.Context) (struct{}, error) {
				return struct{}{}, ns.DeleteNotification(
					scopedContext,
					notificationID,
				)
			},
		)
		return err
	}
	var notification models.Notification
	if err := ns.db.WithContext(ctx).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			notificationID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		First(&notification).Error; err != nil {
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
	scope models.ProjectScope,
	eventType models.WebhookEventType,
	transitionStatus models.TicketStatus,
) ([]*models.WebhookConfig, error) {
	if err := scope.Validate(); err != nil {
		return nil, fmt.Errorf("invalid webhook project scope: %w", err)
	}
	var configs []*models.WebhookConfig

	// 查询活跃状态的webhook配置
	if err := ns.db.WithContext(ctx).Where(
		"organization_id = ? AND project_id = ? AND status = ?",
		scope.OrganizationID,
		scope.ProjectID,
		models.WebhookStatusActive,
	).
		Find(&configs).Error; err != nil {
		return nil, err
	}

	// 过滤支持该事件类型的配置
	var filtered []*models.WebhookConfig
	for _, config := range configs {
		if !config.MatchesEvent(eventType, transitionStatus) {
			continue
		}
		if err := ns.revealWebhookSecrets(config); err != nil {
			return nil, fmt.Errorf("无法读取webhook凭据: %w", err)
		}
		filtered = append(filtered, config)
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
	scope models.ProjectScope,
	eventType models.WebhookEventType,
	transitionStatus models.TicketStatus,
) ([]WebhookOutboxTarget, error) {
	return withNotificationProjectOperation(
		ns,
		ctx,
		scope,
		func(scopedContext context.Context) (
			[]WebhookOutboxTarget,
			error,
		) {
			configs, err := ns.getActiveWebhooks(
				scopedContext,
				scope,
				eventType,
				transitionStatus,
			)
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
		},
	)
}

// SendWebhookOutboxAttempt performs exactly one bounded HTTP attempt. It does
// not schedule WebhookLog retries: the durable Outbox owns retry timing through
// outbox_deliveries.next_attempt_at.
func (ns *NotificationService) SendWebhookOutboxAttempt(
	ctx context.Context,
	scope models.ProjectScope,
	configID uint,
	event *NotificationEvent,
) error {
	if event == nil {
		return errors.New("webhook event is required")
	}
	if err := scope.Validate(); err != nil {
		return fmt.Errorf("invalid webhook project scope: %w", err)
	}
	config, err := withNotificationProjectOperation(
		ns,
		ctx,
		scope,
		func(scopedContext context.Context) (
			models.WebhookConfig,
			error,
		) {
			var config models.WebhookConfig
			err := ns.db.WithContext(scopedContext).
				Where(
					"id = ? AND organization_id = ? AND project_id = ? AND status = ?",
					configID,
					scope.OrganizationID,
					scope.ProjectID,
					models.WebhookStatusActive,
				).
				First(&config).Error
			if err != nil {
				return models.WebhookConfig{}, err
			}
			if err := ns.revealWebhookSecrets(&config); err != nil {
				return models.WebhookConfig{}, fmt.Errorf(
					"无法读取webhook凭据: %w",
					err,
				)
			}
			return config, nil
		},
	)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// A removed or disabled subscription no longer needs delivery.
		return nil
	}
	if err != nil {
		return fmt.Errorf("load webhook configuration: %w", err)
	}
	if !config.MatchesEvent(event.Type, event.TransitionStatus) {
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

// SendWebhookSnapshotOutboxAttempt performs one attempt using only the
// immutable configuration captured with the source DomainEvent. The mutable
// WebhookConfig row is never consulted for routing, filtering, credentials, or
// retry behavior.
func (ns *NotificationService) SendWebhookSnapshotOutboxAttempt(
	ctx context.Context,
	scope models.ProjectScope,
	snapshotID string,
	event *NotificationEvent,
) error {
	if event == nil {
		return errors.New("webhook event is required")
	}
	if err := scope.Validate(); err != nil {
		return fmt.Errorf("invalid webhook project scope: %w", err)
	}
	snapshotID = strings.TrimSpace(snapshotID)
	if snapshotID == "" {
		return errors.New("webhook delivery snapshot is required")
	}
	snapshot, err := withNotificationProjectOperation(
		ns,
		ctx,
		scope,
		func(scopedContext context.Context) (
			models.WebhookDeliverySnapshot,
			error,
		) {
			var snapshot models.WebhookDeliverySnapshot
			err := ns.db.WithContext(scopedContext).
				Where(
					"id = ? AND organization_id = ? AND project_id = ?",
					snapshotID,
					scope.OrganizationID,
					scope.ProjectID,
				).
				First(&snapshot).Error
			return snapshot, err
		},
	)
	if err != nil {
		return fmt.Errorf("load webhook delivery snapshot: %w", err)
	}
	if eventID := strings.TrimSpace(event.Metadata["event_id"]); eventID == "" ||
		eventID != snapshot.EventID {
		return errors.New("webhook delivery snapshot event mismatch")
	}
	config, err := snapshot.WebhookConfig()
	if err != nil {
		return fmt.Errorf("hydrate webhook delivery snapshot: %w", err)
	}
	if err := ns.revealWebhookSecrets(&config); err != nil {
		return fmt.Errorf("无法读取webhook快照凭据: %w", err)
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
	if config == nil {
		return errors.New("webhook配置不能为空")
	}
	if event == nil {
		return errors.New("webhook事件不能为空")
	}
	if err := requireExternalIOOutsideProjectTransaction(
		ctx,
		"webhook HTTP delivery",
	); err != nil {
		return err
	}
	startTime := time.Now()

	// 创建日志记录
	log := &models.WebhookLog{
		OrganizationID: config.OrganizationID,
		ProjectID:      config.ProjectID,
		ConfigID:       config.ID,
		EventType:      event.Type,
		ResourceID:     event.ResourceID,
		ResourceType:   event.ResourceType,
		Status:         "pending",
		MaxRetries:     0,
		Environment:    ns.environment,
	}

	// 序列化事件数据
	eventDataBytes, _ := json.Marshal(event)
	log.EventData = string(eventDataBytes)

	// Custom Webhooks implement the authenticated CloudEvents contract. An
	// unsigned delivery would contradict the public receiver contract and
	// must never be sent.
	if config.Provider == models.WebhookProviderCustom &&
		strings.TrimSpace(config.Secret) == "" {
		log.Status = "failed"
		log.ErrorMessage = "自定义webhook缺少签名密钥"
		ns.saveLog(ctx, log, config)
		return errors.New("自定义webhook缺少签名密钥")
	}

	// 生成消息内容
	message, err := ns.generateMessage(config, event)
	if err != nil {
		log.Status = "failed"
		log.ErrorMessage = fmt.Sprintf("生成消息失败: %v", err)
		ns.saveLog(ctx, log, config)
		return err
	}

	// 构建请求
	requestBody, err := ns.buildRequestBodyForEvent(config, message, event)
	if err != nil {
		log.Status = "failed"
		log.ErrorMessage = fmt.Sprintf("构建请求失败: %v", err)
		ns.saveLog(ctx, log, config)
		return err
	}
	eventID := strings.TrimSpace(event.Metadata["event_id"])
	if config.Provider == models.WebhookProviderCustom {
		var structuredEvent CloudEventEnvelope
		if err := json.Unmarshal(requestBody, &structuredEvent); err != nil {
			log.Status = "failed"
			log.ErrorMessage = "读取自定义webhook CloudEvent标识失败"
			ns.saveLog(ctx, log, config)
			return errors.New("读取自定义webhook CloudEvent标识失败")
		}
		if eventID != "" && eventID != structuredEvent.ID {
			log.Status = "failed"
			log.ErrorMessage = "自定义webhook CloudEvent标识不一致"
			ns.saveLog(ctx, log, config)
			return errors.New("自定义webhook CloudEvent标识不一致")
		}
		eventID = structuredEvent.ID
	}

	log.RequestURL = webhookEndpointForLog(config.WebhookURL)
	log.RequestMethod = "POST"
	log.RequestBody = string(requestBody)

	// 创建HTTP请求
	req, err := http.NewRequestWithContext(ctx, "POST", config.WebhookURL, bytes.NewBuffer(requestBody))
	if err != nil {
		log.Status = "failed"
		log.ErrorMessage = "创建webhook请求失败"
		ns.saveLog(ctx, log, config)
		return errors.New("webhook请求地址无效")
	}

	// 设置请求头
	ns.setRequestHeaders(req, config, requestBody)
	if eventID != "" && !strings.ContainsAny(eventID, "\r\n") {
		req.Header.Set("X-CloudEvents-ID", eventID)
		idempotencyKey := strings.TrimSpace(event.Metadata["delivery_id"])
		if idempotencyKey == "" || strings.ContainsAny(idempotencyKey, "\r\n") {
			idempotencyKey = fmt.Sprintf("%s:webhook:%d", eventID, config.ID)
		}
		req.Header.Set("Idempotency-Key", idempotencyKey)
		req.Header.Set("X-ChronoDesk-Delivery-ID", idempotencyKey)
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
		ns.saveLog(ctx, log, config)
		return errors.New("webhook目标地址未通过安全校验")
	}
	defer client.CloseIdleConnections()

	// 发送请求
	resp, err := client.Do(req)
	log.ResponseTime = time.Since(startTime).Milliseconds()

	if err != nil {
		log.Status = "failed"
		log.ErrorMessage = "webhook请求发送失败"
		ns.saveLog(ctx, log, config)
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
		ns.updateConfigStats(ctx, config, true, nil)
	} else {
		log.Status = "failed"
		log.ErrorMessage = fmt.Sprintf("HTTP错误: %d", resp.StatusCode)

		// 更新配置统计
		ns.updateConfigStats(
			ctx,
			config,
			false,
			fmt.Errorf("HTTP %d", resp.StatusCode),
		)

	}

	// 保存日志
	ns.saveLog(ctx, log, config)

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
		"{{resource_id}}": strconv.FormatUint(uint64(event.ResourceID), 10),
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
	case models.WebhookEventTicketTransitioned:
		switch event.TransitionStatus {
		case models.TicketStatusResolved:
			statusEmoji = "✅"
		case models.TicketStatusClosed:
			statusEmoji = "🔒"
		default:
			statusEmoji = "🔄"
		}
	case models.WebhookEventTicketSLABreached, models.WebhookEventSystemAlert:
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
	case models.WebhookProviderCustom:
		return ns.buildCustomCloudEventBody(event)
	default:
		payload := map[string]interface{}{
			"text":      message,
			"timestamp": time.Now().Unix(),
		}
		if event != nil {
			payload["timestamp"] = event.Timestamp.UTC().Unix()
			payload["event_type"] = event.Type
			payload["resource_id"] = event.ResourceID
			payload["resource_type"] = event.ResourceType
			if event.TransitionStatus != "" {
				payload["transition_status"] = event.TransitionStatus
			}
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

func (ns *NotificationService) buildCustomCloudEventBody(
	event *NotificationEvent,
) ([]byte, error) {
	if event == nil || event.Data == nil {
		return nil, errors.New("自定义webhook缺少CloudEvent")
	}
	value, exists := event.Data["cloud_event"]
	if !exists || value == nil {
		return nil, errors.New("自定义webhook缺少CloudEvent")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("编码自定义webhook CloudEvent失败: %w", err)
	}
	var envelope CloudEventEnvelope
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		return nil, fmt.Errorf("解析自定义webhook CloudEvent失败: %w", err)
	}
	if err := validateCustomWebhookCloudEvent(envelope); err != nil {
		return nil, err
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("编码自定义webhook CloudEvent失败: %w", err)
	}
	return body, nil
}

func validateCustomWebhookCloudEvent(event CloudEventEnvelope) error {
	switch {
	case event.SpecVersion != "1.0":
		return errors.New("自定义webhook CloudEvent specversion必须为1.0")
	case strings.TrimSpace(event.ID) == "":
		return errors.New("自定义webhook CloudEvent缺少id")
	case strings.ContainsAny(event.ID, "\r\n"):
		return errors.New("自定义webhook CloudEvent id包含非法字符")
	case strings.TrimSpace(event.Source) == "":
		return errors.New("自定义webhook CloudEvent缺少source")
	case strings.TrimSpace(event.Type) == "":
		return errors.New("自定义webhook CloudEvent缺少type")
	case strings.TrimSpace(event.Subject) == "":
		return errors.New("自定义webhook CloudEvent缺少subject")
	case event.Time.IsZero():
		return errors.New("自定义webhook CloudEvent缺少time")
	case event.DataContentType != "application/json":
		return errors.New("自定义webhook CloudEvent datacontenttype必须为application/json")
	case strings.TrimSpace(event.DataSchema) == "":
		return errors.New("自定义webhook CloudEvent缺少dataschema")
	case event.ResourceVersion == 0:
		return errors.New("自定义webhook CloudEvent缺少resourceversion")
	case len(event.Data) == 0 || !json.Valid(event.Data):
		return errors.New("自定义webhook CloudEvent data必须是有效JSON")
	}
	if err := (models.ActorRef{
		Type: event.ActorType,
		ID:   event.ActorID,
	}).Validate(); err != nil {
		return fmt.Errorf("自定义webhook CloudEvent actor无效: %w", err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(event.Data, &data); err != nil || data == nil {
		return errors.New("自定义webhook CloudEvent data必须是JSON对象")
	}
	var actor models.ActorRef
	if rawActor, exists := data["actor"]; !exists ||
		json.Unmarshal(rawActor, &actor) != nil ||
		actor.Validate() != nil ||
		actor != (models.ActorRef{Type: event.ActorType, ID: event.ActorID}) {
		return errors.New("自定义webhook CloudEvent data.actor必须匹配事件actor")
	}
	return nil
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
			"title": "ChronoDesk 通知",
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
	contentType := "application/json"
	if config.Provider == models.WebhookProviderCustom {
		contentType = "application/cloudevents+json"
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "ChronoDesk-Webhook/1.0")
	if config.AccessToken != "" && !strings.ContainsAny(config.AccessToken, "\r\n") {
		req.Header.Set("Authorization", "Bearer "+config.AccessToken)
	}

	if config.Provider == models.WebhookProviderCustom && config.Secret != "" {
		timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
		req.Header.Set("X-ChronoDesk-Timestamp", timestamp)
		req.Header.Set(
			"X-ChronoDesk-Signature",
			ns.generateCustomWebhookSign(timestamp, body, config.Secret),
		)
		if config.PreviousSecret != "" &&
			config.PreviousSecretExpiresAt != nil &&
			config.PreviousSecretExpiresAt.After(time.Now().UTC()) {
			req.Header.Set(
				"X-ChronoDesk-Signature-Previous",
				ns.generateCustomWebhookSign(
					timestamp,
					body,
					config.PreviousSecret,
				),
			)
		}
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
		req.Header.Set("X-Lark-Request-Nonce", "chronodesk")
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
		"X-Chronodesk-Timestamp":   {},
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
	for _, secret := range []string{
		config.Secret,
		config.PreviousSecret,
		config.AccessToken,
	} {
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

func (ns *NotificationService) generateCustomWebhookSign(
	timestamp string,
	body []byte,
	secret string,
) string {
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write([]byte(timestamp))
	_, _ = h.Write([]byte("."))
	_, _ = h.Write(body)
	return fmt.Sprintf("v1=%x", h.Sum(nil))
}

// generateLarkSign 生成飞书签名
func (ns *NotificationService) generateLarkSign(timestamp, secret string) string {
	stringToSign := timestamp + "\n" + "chronodesk" + "\n" + secret
	h := hmac.New(sha256.New, []byte(stringToSign))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// updateConfigStats 更新配置统计
func (ns *NotificationService) updateConfigStats(
	ctx context.Context,
	config *models.WebhookConfig,
	success bool,
	err error,
) {
	if config == nil ||
		config.ID == 0 ||
		config.OrganizationID == 0 ||
		config.ProjectID == 0 {
		return
	}
	finalizeContext, cancelFinalize := context.WithTimeout(
		context.WithoutCancel(ctx),
		5*time.Second,
	)
	defer cancelFinalize()
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

	_, _ = withNotificationProjectOperation(
		ns,
		finalizeContext,
		models.ProjectScope{
			OrganizationID: config.OrganizationID,
			ProjectID:      config.ProjectID,
		},
		func(scopedContext context.Context) (struct{}, error) {
			result := ns.db.WithContext(scopedContext).
				Model(&models.WebhookConfig{}).
				Where(
					"id = ? AND organization_id = ? AND project_id = ?",
					config.ID,
					config.OrganizationID,
					config.ProjectID,
				).
				Updates(updates)
			return struct{}{}, result.Error
		},
	)
}

// saveLog 保存日志
func (ns *NotificationService) saveLog(
	ctx context.Context,
	log *models.WebhookLog,
	config *models.WebhookConfig,
) {
	scrubWebhookLog(log, config)
	if log == nil || config == nil {
		return
	}
	finalizeContext, cancelFinalize := context.WithTimeout(
		context.WithoutCancel(ctx),
		5*time.Second,
	)
	defer cancelFinalize()
	_, err := withNotificationProjectOperation(
		ns,
		finalizeContext,
		models.ProjectScope{
			OrganizationID: config.OrganizationID,
			ProjectID:      config.ProjectID,
		},
		func(scopedContext context.Context) (struct{}, error) {
			result := ns.db.WithContext(scopedContext).Create(log)
			return struct{}{}, result.Error
		},
	)
	if err != nil {
		// 记录日志失败，但不影响主流程
		fmt.Printf("保存webhook日志失败: %v\n", err)
	}
}

// WebhookTestReceipt is returned after the complete durable test-delivery
// intent commits. Delivered is deliberately false: the external HTTP outcome
// belongs to the Outbox worker and WebhookLog.
type WebhookTestReceipt struct {
	OperationID          string `json:"operation_id"`
	EventID              string `json:"event_id"`
	DeliveryID           string `json:"delivery_id"`
	SnapshotID           string `json:"snapshot_id"`
	ConfigID             uint   `json:"config_id"`
	ConfigurationVersion string `json:"configuration_version"`
	Status               string `json:"status"`
	Queued               bool   `json:"queued"`
	Delivered            bool   `json:"delivered"`
}

// TestWebhook commits one Human test-delivery intent. It performs no external
// I/O and never reveals webhook credentials. The Outbox worker later hydrates
// the immutable snapshot and owns the bounded HTTP attempt plus delivery log.
func (ns *NotificationService) TestWebhook(
	ctx context.Context,
	scope models.ProjectScope,
	configID uint,
) (*WebhookTestReceipt, error) {
	if ns == nil || ns.db == nil ||
		ns.webhookTestProjects == nil ||
		ns.webhookTestEvents == nil {
		return nil, ErrWebhookTestUnavailable
	}
	if err := scope.Validate(); err != nil {
		return nil, fmt.Errorf("invalid webhook project scope: %w", err)
	}
	if configID == 0 {
		return nil, ErrWebhookTestNotFound
	}
	if scopeddb.HasTransaction(ctx) {
		return nil, fmt.Errorf(
			"%w: webhook test delivery must own its project transaction",
			ErrWebhookTestUnavailable,
		)
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil ||
		operation.Scope != scope ||
		operation.Source != SourceProtocolHumanREST ||
		operation.Actor.Type != models.ActorTypeHuman {
		return nil, ErrWebhookTestAccessDenied
	}
	userID, err := humanActorUserID(operation.Actor)
	if err != nil {
		return nil, ErrWebhookTestAccessDenied
	}
	var receipt *WebhookTestReceipt
	err = scopeddb.WithProjectScopeContextTransaction(
		ctx,
		ns.db,
		scope,
		func(scopedContext context.Context) error {
			// The context binding makes root-handle repository calls use the
			// scoped connection, but same-transaction appenders such as the
			// audit ledger must receive an explicit *gorm.DB whose ConnPool is
			// the real transaction. A nested savepoint provides that handle
			// without widening or extending the project transaction.
			return transactionForContext(
				scopedContext,
				ns.db,
				func(tx *gorm.DB) error {
					access, accessErr :=
						ns.webhookTestProjects.RevalidateHumanProjectAccess(
							scopedContext,
							scope,
							userID,
						)
					if accessErr != nil {
						return fmt.Errorf(
							"%w: %w",
							ErrWebhookTestAccessDenied,
							accessErr,
						)
					}
					if access == nil ||
						(access.Role != models.ProjectRoleAdmin &&
							access.Role != models.ProjectRoleManager) {
						return ErrWebhookTestAccessDenied
					}

					var config models.WebhookConfig
					queryErr := tx.WithContext(scopedContext).
						Clauses(clause.Locking{Strength: "UPDATE"}).
						Where(
							"id = ? AND organization_id = ? AND project_id = ? AND status = ?",
							configID,
							scope.OrganizationID,
							scope.ProjectID,
							models.WebhookStatusActive,
						).
						Take(&config).Error
					if errors.Is(queryErr, gorm.ErrRecordNotFound) {
						return fmt.Errorf(
							"%w: %w",
							ErrWebhookTestNotFound,
							queryErr,
						)
					}
					if queryErr != nil {
						return fmt.Errorf(
							"lock webhook test configuration: %w",
							queryErr,
						)
					}

					now := time.Now().UTC()
					operationID := newNativeID()
					eventID := newNativeID()
					configurationVersion :=
						webhookTestConfigurationVersion(config)
					snapshot, snapshotErr :=
						models.NewWebhookDeliverySnapshot(
							config,
							eventID,
						)
					if snapshotErr != nil {
						return fmt.Errorf(
							"freeze webhook test configuration: %w",
							snapshotErr,
						)
					}
					if createErr := tx.WithContext(scopedContext).
						Create(snapshot).Error; createErr != nil {
						return fmt.Errorf(
							"create webhook test delivery snapshot: %w",
							createErr,
						)
					}

					maxAttempts := config.RetryCount + 1
					if maxAttempts < 1 {
						maxAttempts = 1
					}
					if maxAttempts > 11 {
						maxAttempts = 11
					}
					event, appendErr :=
						ns.webhookTestEvents.AppendDomainEventTx(
							scopedContext,
							tx,
							DomainEventInput{
								ID:         eventID,
								Type:       eventcontract.SystemAlertEventType,
								Subject:    fmt.Sprintf("webhook/%d/test", config.ID),
								Time:       now,
								DataSchema: "urn:chronodesk:schema:webhook-test-delivery:v1",
								Data: map[string]any{
									"operation_id":      operationID,
									"test":              true,
									"title":             "Webhook测试通知",
									"description":       "这是一个测试消息，用于验证Webhook配置是否正常工作。",
									"webhook_config_id": config.ID,
								},
								TraceID:              operation.TraceID,
								CorrelationID:        operation.CorrelationID,
								Actor:                operation.Actor,
								ResourceVersion:      1,
								Scope:                scope,
								ConfigurationVersion: configurationVersion,
							},
							[]OutboxTarget{{
								Type: "webhook",
								ID: webhookSnapshotDestinationPrefix +
									snapshot.ID,
								MaxAttempts: maxAttempts,
							}},
						)
					if appendErr != nil {
						return fmt.Errorf(
							"append webhook test delivery intent: %w",
							appendErr,
						)
					}
					if event == nil || event.ID != eventID ||
						len(event.Deliveries) != 1 ||
						event.Deliveries[0].DestinationType != "webhook" ||
						event.Deliveries[0].DestinationID !=
							webhookSnapshotDestinationPrefix+snapshot.ID {
						return errors.New(
							"webhook test delivery intent did not create one snapshot Outbox delivery",
						)
					}
					receipt = &WebhookTestReceipt{
						OperationID:          operationID,
						EventID:              event.ID,
						DeliveryID:           event.Deliveries[0].ID,
						SnapshotID:           snapshot.ID,
						ConfigID:             config.ID,
						ConfigurationVersion: configurationVersion,
						Status:               "queued",
						Queued:               true,
						Delivered:            false,
					}
					return nil
				},
			)
		},
	)
	if err != nil {
		return nil, err
	}
	if receipt == nil {
		return nil, ErrWebhookTestUnavailable
	}
	return receipt, nil
}

func webhookTestConfigurationVersion(config models.WebhookConfig) string {
	return fmt.Sprintf(
		"webhook-config:%d@%s",
		config.ID,
		config.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
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
	previousSecret := ""
	if config.PreviousSecret != "" &&
		config.PreviousSecretExpiresAt != nil &&
		config.PreviousSecretExpiresAt.After(time.Now().UTC()) {
		previousSecret, err = security.RevealOptional(
			ns.secretStore,
			config.PreviousSecret,
			security.FieldAAD(
				"webhook_configs",
				rowID,
				"previous_secret",
			),
		)
		if err != nil {
			return err
		}
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
	config.PreviousSecret = previousSecret
	config.AccessToken = accessToken
	return nil
}

// === 通知管理相关方法实现 ===

// CreateNotification 创建通知
func (ns *NotificationService) CreateNotification(ctx context.Context, req *models.NotificationCreateRequest) (*models.Notification, error) {
	if req == nil {
		return nil, errors.New("通知请求不能为空")
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("通知项目上下文无效: %w", err)
	}
	if !scopeddb.HasTransaction(ctx) {
		return withNotificationProjectOperation(
			ns,
			ctx,
			operation.Scope,
			func(scopedContext context.Context) (*models.Notification, error) {
				return ns.CreateNotification(scopedContext, req)
			},
		)
	}
	if req.SenderID != nil {
		return nil, errors.New("通知发送者由可信身份上下文确定")
	}
	var senderID *uint
	if operation.Actor.Type == models.ActorTypeHuman {
		value, parseErr := safeconv.ParsePositiveUint(operation.Actor.ID)
		if parseErr != nil {
			return nil, errors.New("通知发送者身份无效")
		}
		senderID = &value
	}
	if req.RelatedTicketID == nil &&
		(req.RelatedID != nil ||
			strings.TrimSpace(req.RelatedType) != "" ||
			strings.TrimSpace(req.ActionURL) != "") {
		return nil, errors.New("通知关联对象必须使用项目内工单引用")
	}
	notification := &models.Notification{
		OrganizationID:  operation.Scope.OrganizationID,
		ProjectID:       operation.Scope.ProjectID,
		Type:            req.Type,
		Title:           req.Title,
		Content:         req.Content,
		Priority:        req.Priority,
		Channel:         req.Channel,
		RecipientID:     req.RecipientID,
		SenderID:        senderID,
		RelatedTicketID: req.RelatedTicketID,
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
		if err != nil {
			return nil, fmt.Errorf("通知元数据无效: %w", err)
		}
		if len(metadataBytes) > 16*1024 {
			return nil, errors.New("通知元数据超过 16 KiB 限制")
		}
		notification.Metadata = string(metadataBytes)
	}

	err = transactionForContext(ctx, ns.db, func(tx *gorm.DB) error {
		var membershipCount int64
		if err := tx.Model(&models.ProjectMembership{}).
			Where(
				"project_id = ? AND user_id = ? AND is_active = ?",
				operation.Scope.ProjectID,
				notification.RecipientID,
				true,
			).
			Count(&membershipCount).Error; err != nil {
			return fmt.Errorf("校验通知接收者项目成员关系: %w", err)
		}
		if membershipCount != 1 {
			return errors.New("通知接收者不是当前项目的有效成员")
		}
		if notification.RelatedTicketID != nil {
			var ticketCount int64
			if err := tx.Model(&models.Ticket{}).
				Where(
					"id = ? AND organization_id = ? AND project_id = ?",
					*notification.RelatedTicketID,
					operation.Scope.OrganizationID,
					operation.Scope.ProjectID,
				).
				Count(&ticketCount).Error; err != nil {
				return fmt.Errorf("校验通知关联工单: %w", err)
			}
			if ticketCount != 1 {
				return errors.New("通知关联工单不属于当前项目")
			}
			notification.RelatedType = "ticket"
			notification.RelatedID = notification.RelatedTicketID
			notification.ActionURL = fmt.Sprintf(
				"/tickets/%d",
				*notification.RelatedTicketID,
			)
		}
		if err := tx.Create(notification).Error; err != nil {
			return err
		}
		if notification.Channel != models.NotificationChannelEmail {
			return nil
		}
		availableAt := time.Time{}
		if notification.ScheduledAt != nil {
			availableAt = *notification.ScheduledAt
		}
		_, err := AppendEmailOutboxTx(ctx, tx, EmailOutboxEventInput{
			Scope:   operation.Scope,
			ID:      NotificationEmailEventID(notification.ID),
			Source:  "urn:chronodesk:notifications",
			Type:    eventcontract.NotificationEmailRequestedEventType,
			Subject: fmt.Sprintf("notification/%d", notification.ID),
			Actor:   operation.Actor,
			Data: EmailIntentReference{
				UserID:           notification.RecipientID,
				NotificationID:   notification.ID,
				NotificationType: string(notification.Type),
			},
			DestinationID: NotificationEmailDestinationPrefix +
				strconv.FormatUint(uint64(notification.ID), 10),
			AvailableAt: availableAt,
		})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("创建通知失败: %w", err)
	}

	// 性能优化：跳过预加载相关数据以提高创建速度
	// 如果需要完整数据，调用方可以单独查询
	// ns.db.Preload("Recipient").Preload("Sender").Preload("RelatedTicket").First(notification, notification.ID)

	return notification, nil
}

// DeliverEmailNotificationOutbox validates the immutable event reference and
// performs one bounded email attempt. Notification.IsSent is the durable
// replay receipt if a process stops after SMTP but before Outbox acknowledgement.
func (ns *NotificationService) DeliverEmailNotificationOutbox(
	ctx context.Context,
	event CloudEventEnvelope,
	destinationID string,
) error {
	if ns == nil || ns.db == nil {
		return errors.New("邮件通知服务不可用")
	}
	rawID, found := strings.CutPrefix(
		destinationID,
		NotificationEmailDestinationPrefix,
	)
	if !found || rawID == "" || strings.Contains(rawID, ":") {
		return errors.New("邮件通知 Outbox 目标无效")
	}
	notificationID, err := safeconv.ParsePositiveUint(rawID)
	if err != nil {
		return errors.New("邮件通知 Outbox 记录 ID 无效")
	}
	var reference EmailIntentReference
	if event.Type != eventcontract.NotificationEmailRequestedEventType ||
		json.Unmarshal(event.Data, &reference) != nil ||
		reference.NotificationID != notificationID ||
		reference.UserID == 0 {
		return errors.New("邮件通知 Outbox 事件引用不一致")
	}

	if ns.emailNotificationService == nil {
		return errors.New("邮件通知发送器未初始化")
	}
	scope := models.ProjectScope{
		OrganizationID: event.OrganizationID,
		ProjectID:      event.ProjectID,
	}
	if err := scope.Validate(); err != nil {
		return errors.New("邮件通知 Outbox 项目范围无效")
	}
	return ns.emailNotificationService.SendEmailNotificationOutboxAttempt(
		ctx,
		scope,
		&models.Notification{
			ID:             notificationID,
			OrganizationID: scope.OrganizationID,
			ProjectID:      scope.ProjectID,
			RecipientID:    reference.UserID,
			Type:           models.NotificationType(reference.NotificationType),
		},
	)
}

// GetNotifications 获取通知列表
func (ns *NotificationService) GetNotifications(ctx context.Context, filter *models.NotificationFilter) ([]*models.Notification, int64, error) {
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("notification project scope: %w", err)
	}
	if !scopeddb.HasTransaction(ctx) {
		type result struct {
			items []*models.Notification
			total int64
		}
		value, operationErr := withNotificationProjectOperation(
			ns,
			ctx,
			scope,
			func(scopedContext context.Context) (result, error) {
				items, total, queryErr := ns.GetNotifications(
					scopedContext,
					filter,
				)
				return result{items: items, total: total}, queryErr
			},
		)
		return value.items, value.total, operationErr
	}
	if filter == nil {
		filter = &models.NotificationFilter{}
	}
	baseQuery := ns.db.WithContext(ctx).
		Model(&models.Notification{}).
		Where(
			"organization_id = ? AND project_id = ?",
			scope.OrganizationID,
			scope.ProjectID,
		)

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
	orderColumns := []clause.OrderByColumn{order}
	if order.Column.Name != "id" {
		orderColumns = append(orderColumns, clause.OrderByColumn{
			Column: clause.Column{Name: "id"},
			Desc:   order.Desc,
		})
	}
	dataQuery = dataQuery.Clauses(clause.OrderBy{Columns: orderColumns})

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
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return fmt.Errorf("notification project scope: %w", err)
	}
	if !scopeddb.HasTransaction(ctx) {
		_, err := withNotificationProjectOperation(
			ns,
			ctx,
			scope,
			func(scopedContext context.Context) (struct{}, error) {
				return struct{}{}, ns.MarkAsRead(
					scopedContext,
					notificationID,
					userID,
				)
			},
		)
		return err
	}
	var notification models.Notification
	if err := ns.db.WithContext(ctx).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			notificationID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		First(&notification).Error; err != nil {
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
	if err := ns.db.WithContext(ctx).Save(&notification).Error; err != nil {
		return fmt.Errorf("标记已读失败: %w", err)
	}

	return nil
}

// MarkAllAsRead 标记所有通知为已读
func (ns *NotificationService) MarkAllAsRead(ctx context.Context, userID uint) error {
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return fmt.Errorf("notification project scope: %w", err)
	}
	if !scopeddb.HasTransaction(ctx) {
		_, err := withNotificationProjectOperation(
			ns,
			ctx,
			scope,
			func(scopedContext context.Context) (struct{}, error) {
				return struct{}{}, ns.MarkAllAsRead(
					scopedContext,
					userID,
				)
			},
		)
		return err
	}
	now := time.Now()
	updates := map[string]interface{}{
		"is_read":    true,
		"read_at":    &now,
		"updated_at": now,
	}

	if err := ns.db.WithContext(ctx).Model(&models.Notification{}).
		Where(
			"organization_id = ? AND project_id = ? AND recipient_id = ? AND is_read = false",
			scope.OrganizationID,
			scope.ProjectID,
			userID,
		).
		Updates(updates).Error; err != nil {
		return fmt.Errorf("批量标记已读失败: %w", err)
	}

	return nil
}

// GetUnreadCount 获取未读通知数量
func (ns *NotificationService) GetUnreadCount(ctx context.Context, userID uint) (int64, error) {
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return 0, fmt.Errorf("notification project scope: %w", err)
	}
	if !scopeddb.HasTransaction(ctx) {
		return withNotificationProjectOperation(
			ns,
			ctx,
			scope,
			func(scopedContext context.Context) (int64, error) {
				return ns.GetUnreadCount(scopedContext, userID)
			},
		)
	}
	var count int64
	if err := ns.db.WithContext(ctx).Model(&models.Notification{}).
		Where(
			"organization_id = ? AND project_id = ? AND recipient_id = ? AND is_read = false",
			scope.OrganizationID,
			scope.ProjectID,
			userID,
		).
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
			now := time.Now()
			persistedPreference := map[string]interface{}{
				"created_at":           now,
				"updated_at":           now,
				"user_id":              userID,
				"notification_type":    pref.NotificationType,
				"email_enabled":        pref.EmailEnabled,
				"in_app_enabled":       pref.InAppEnabled,
				"webhook_enabled":      pref.WebhookEnabled,
				"do_not_disturb_start": pref.DoNotDisturbStart,
				"do_not_disturb_end":   pref.DoNotDisturbEnd,
				"max_daily_count":      pref.MaxDailyCount,
				"batch_delivery":       pref.BatchDelivery,
				"batch_interval":       pref.BatchInterval,
			}
			if err := tx.Model(&models.NotificationPreference{}).
				Create(persistedPreference).Error; err != nil {
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
	eventActor := models.ActorRef{Type: event.ActorType, ID: event.ActorID}
	if err := eventActor.Validate(); err != nil {
		return nil, false, fmt.Errorf("notification CloudEvent has an invalid actor: %w", err)
	}
	var senderID *uint
	if event.ActorType == models.ActorTypeHuman {
		humanID, err := safeconv.ParsePositiveUint(event.ActorID)
		if err != nil {
			return nil, false, errors.New("notification CloudEvent has an invalid human actor")
		}
		senderID = &humanID
	}

	notificationType, recipientID, err := parseTicketNotificationDestination(destinationID)
	if err != nil {
		return nil, false, err
	}
	if senderID != nil && recipientID == *senderID {
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
		OrganizationID:  event.OrganizationID,
		ProjectID:       event.ProjectID,
		Type:            notificationType,
		Priority:        models.NotificationPriorityNormal,
		Channel:         models.NotificationChannelInApp,
		RecipientID:     recipientID,
		SenderID:        senderID,
		RelatedType:     "ticket",
		RelatedID:       &data.TicketID,
		RelatedTicketID: &data.TicketID,
		ActionURL:       fmt.Sprintf("/tickets/%d", data.TicketID),
	}
	metadata := map[string]any{
		"source_event_id":  event.ID,
		"ticket_number":    data.TicketNumber,
		"resource_version": event.ResourceVersion,
		"actor":            eventActor,
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
		if event.Type != eventcontract.TicketAssignedEventType &&
			event.Type != eventcontract.TicketUpdatedEventType {
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
		if event.Type != eventcontract.TicketTransitionedEventType &&
			event.Type != eventcontract.TicketUpdatedEventType {
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
