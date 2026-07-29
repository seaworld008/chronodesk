package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"gorm.io/gorm"
)

// WebhookProvider 通知提供商枚举
type WebhookProvider string

const (
	WebhookProviderWeChat   WebhookProvider = "wechat"   // 企业微信
	WebhookProviderDingTalk WebhookProvider = "dingtalk" // 钉钉
	WebhookProviderLark     WebhookProvider = "lark"     // 飞书
	WebhookProviderSlack    WebhookProvider = "slack"    // Slack
	WebhookProviderTeams    WebhookProvider = "teams"    // Microsoft Teams
	WebhookProviderCustom   WebhookProvider = "custom"   // 自定义webhook
)

// WebhookStatus 配置状态枚举
type WebhookStatus string

const (
	WebhookStatusActive   WebhookStatus = "active"   // 活跃
	WebhookStatusInactive WebhookStatus = "inactive" // 未激活
	WebhookStatusDisabled WebhookStatus = "disabled" // 已禁用
	WebhookStatusError    WebhookStatus = "error"    // 错误状态
)

// WebhookEventType 事件类型枚举
type WebhookEventType string

const (
	WebhookEventTicketCreated          WebhookEventType = eventcontract.TicketCreatedEventType
	WebhookEventTicketAssigned         WebhookEventType = eventcontract.TicketAssignedEventType
	WebhookEventTicketUpdated          WebhookEventType = eventcontract.TicketUpdatedEventType
	WebhookEventTicketTransitioned     WebhookEventType = eventcontract.TicketTransitionedEventType
	WebhookEventTicketComment          WebhookEventType = eventcontract.TicketCommentCreatedEventType
	WebhookEventTicketAttachment       WebhookEventType = eventcontract.TicketAttachmentCreatedEventType
	WebhookEventTicketEscalated        WebhookEventType = eventcontract.TicketEscalatedEventType
	WebhookEventTicketSLABreached      WebhookEventType = eventcontract.TicketSLABreachedEventType
	WebhookEventTicketDeleted          WebhookEventType = eventcontract.TicketDeletedEventType
	WebhookEventAutomationNotification WebhookEventType = eventcontract.AutomationNotificationRequestedEventType
	WebhookEventSystemAlert            WebhookEventType = eventcontract.SystemAlertEventType
)

// WebhookFilterRules contains explicit predicates applied after the canonical
// CloudEvent type match. An empty transition_statuses list subscribes to every
// ticket.transitioned event.
type WebhookFilterRules struct {
	TransitionStatuses []TicketStatus `json:"transition_statuses,omitempty"`
}

func (filters *WebhookFilterRules) Validate() error {
	if filters == nil {
		return nil
	}
	statuses := make(map[TicketStatus]struct{}, len(filters.TransitionStatuses))
	for _, status := range filters.TransitionStatuses {
		if !status.IsValid() {
			return fmt.Errorf("unsupported transition status %q", status)
		}
		if _, exists := statuses[status]; exists {
			return fmt.Errorf("duplicate transition status %q", status)
		}
		statuses[status] = struct{}{}
	}
	return nil
}

// ValidateWebhookSubscriptions rejects aliases, duplicates, and predicates
// that are not attached to their canonical event type.
func ValidateWebhookSubscriptions(
	events []WebhookEventType,
	filters *WebhookFilterRules,
	requireEvent bool,
) error {
	if requireEvent && len(events) == 0 {
		return errors.New("at least one Webhook event is required")
	}
	seen := make(map[WebhookEventType]struct{}, len(events))
	hasTransitioned := false
	for _, eventType := range events {
		if !eventcontract.IsWebhookDeliveryEventType(string(eventType)) {
			return fmt.Errorf("unsupported Webhook event type %q", eventType)
		}
		if _, exists := seen[eventType]; exists {
			return fmt.Errorf("duplicate Webhook event type %q", eventType)
		}
		seen[eventType] = struct{}{}
		if eventType == WebhookEventTicketTransitioned {
			hasTransitioned = true
		}
	}
	if filters == nil {
		return nil
	}
	if err := filters.Validate(); err != nil {
		return err
	}
	if len(filters.TransitionStatuses) > 0 && !hasTransitioned {
		return errors.New("transition status predicates require the ticket.transitioned CloudEvent")
	}
	return nil
}

// DecodeWebhookFilterRules applies a closed JSON contract so misspelled or
// obsolete predicates cannot silently broaden an external subscription.
func DecodeWebhookFilterRules(value string) (*WebhookFilterRules, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	var filters WebhookFilterRules
	if err := decoder.Decode(&filters); err != nil {
		return nil, fmt.Errorf("decode Webhook filter rules: %w", err)
	}
	if err := ensureWebhookJSONEOF(decoder); err != nil {
		return nil, err
	}
	if err := filters.Validate(); err != nil {
		return nil, err
	}
	return &filters, nil
}

func ensureWebhookJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("webhook filter rules contain multiple JSON values")
		}
		return fmt.Errorf("decode trailing webhook filter data: %w", err)
	}
	return nil
}

// WebhookConfig Webhook配置模型
type WebhookConfig struct {
	ID        uint           `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time      `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time      `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`

	// 基本配置
	Name        string          `json:"name" gorm:"size:100;not null" validate:"required,max=100"`
	Description string          `json:"description" gorm:"size:500" validate:"max=500"`
	Provider    WebhookProvider `json:"provider" gorm:"size:20;not null" validate:"required"`
	WebhookURL  string          `json:"webhook_url" gorm:"size:500;not null" validate:"required,url"`
	Status      WebhookStatus   `json:"status" gorm:"size:20;not null;default:'active'" validate:"required"`

	// 认证配置
	// Secret and AccessToken persist only versioned AEAD envelopes. The
	// plaintext values exist in memory solely while an authorized delivery is
	// being prepared.
	Secret      string `json:"-" gorm:"size:2048"` // 签名密钥，不返回给前端
	AccessToken string `json:"-" gorm:"size:2048"` // 访问令牌，不返回给前端

	// 事件配置
	EnabledEvents    string             `json:"enabled_events" gorm:"type:text"`        // JSON数组存储启用的事件类型
	EnabledEventsObj []WebhookEventType `json:"enabled_events_list,omitempty" gorm:"-"` // 运行时解析字段

	// 消息配置
	MessageTemplate string `json:"message_template" gorm:"type:text"`                // 消息模板
	MessageFormat   string `json:"message_format" gorm:"size:20;default:'markdown'"` // markdown, text, card

	// 过滤配置
	FilterRules    string              `json:"filter_rules" gorm:"type:text"`       // JSON对象存储过滤规则
	FilterRulesObj *WebhookFilterRules `json:"filter_rules_obj,omitempty" gorm:"-"` // 运行时解析字段

	// 高级配置
	RetryCount     int  `json:"retry_count" gorm:"default:3" validate:"min=0,max=10"`
	RetryInterval  int  `json:"retry_interval" gorm:"default:60" validate:"min=5,max=3600"` // 秒
	TimeoutSeconds int  `json:"timeout_seconds" gorm:"default:30" validate:"min=5,max=300"`
	IsAsync        bool `json:"is_async" gorm:"default:true"` // 是否异步发送

	// 限流配置
	RateLimit       int `json:"rate_limit" gorm:"default:60" validate:"min=1,max=1000"`         // 每分钟最大请求数
	RateLimitWindow int `json:"rate_limit_window" gorm:"default:60" validate:"min=60,max=3600"` // 限流窗口(秒)

	// 监控统计
	LastTriggeredAt *time.Time `json:"last_triggered_at,omitempty"`
	LastSuccessAt   *time.Time `json:"last_success_at,omitempty"`
	LastErrorAt     *time.Time `json:"last_error_at,omitempty"`
	LastError       string     `json:"last_error" gorm:"type:text"`
	TotalSent       int64      `json:"total_sent" gorm:"default:0"`
	TotalSuccess    int64      `json:"total_success" gorm:"default:0"`
	TotalFailed     int64      `json:"total_failed" gorm:"default:0"`

	// 关联信息
	CreatedBy uint  `json:"created_by" gorm:"not null;index"`
	Creator   *User `json:"creator,omitempty" gorm:"foreignKey:CreatedBy"`
	UpdatedBy *uint `json:"updated_by,omitempty" gorm:"index"`
	Updater   *User `json:"updater,omitempty" gorm:"foreignKey:UpdatedBy"`
}

// BeforeSave GORM钩子 - 保存前处理
func (w *WebhookConfig) BeforeSave(tx *gorm.DB) error {
	// 将EnabledEventsObj序列化为JSON字符串
	if w.EnabledEventsObj != nil {
		eventsData, err := json.Marshal(w.EnabledEventsObj)
		if err != nil {
			return err
		}
		w.EnabledEvents = string(eventsData)
	}

	// 将FilterRulesObj序列化为JSON字符串
	if w.FilterRulesObj != nil {
		filterData, err := json.Marshal(w.FilterRulesObj)
		if err != nil {
			return err
		}
		w.FilterRules = string(filterData)
	}

	return w.ValidateSubscriptions(false)
}

// AfterFind GORM钩子 - 查询后处理
func (w *WebhookConfig) AfterFind(tx *gorm.DB) error {
	// 反序列化EnabledEvents
	if w.EnabledEvents != "" {
		var events []WebhookEventType
		if err := json.Unmarshal([]byte(w.EnabledEvents), &events); err != nil {
			return fmt.Errorf("decode Webhook enabled events: %w", err)
		}
		w.EnabledEventsObj = events
	}

	// 反序列化FilterRules
	if w.FilterRules != "" {
		filters, err := DecodeWebhookFilterRules(w.FilterRules)
		if err != nil {
			return err
		}
		w.FilterRulesObj = filters
	}

	return w.ValidateSubscriptions(false)
}

// SetSubscriptions validates and serializes the exact Webhook contract.
func (w *WebhookConfig) SetSubscriptions(
	events []WebhookEventType,
	filters *WebhookFilterRules,
	requireEvent bool,
) error {
	if err := ValidateWebhookSubscriptions(events, filters, requireEvent); err != nil {
		return err
	}
	w.EnabledEventsObj = append([]WebhookEventType(nil), events...)
	if filters == nil {
		w.FilterRulesObj = nil
		w.FilterRules = ""
	} else {
		copyFilters := &WebhookFilterRules{
			TransitionStatuses: append(
				[]TicketStatus(nil),
				filters.TransitionStatuses...,
			),
		}
		w.FilterRulesObj = copyFilters
	}
	eventsData, err := json.Marshal(w.EnabledEventsObj)
	if err != nil {
		return err
	}
	w.EnabledEvents = string(eventsData)
	if w.FilterRulesObj != nil {
		filterData, err := json.Marshal(w.FilterRulesObj)
		if err != nil {
			return err
		}
		w.FilterRules = string(filterData)
	}
	return nil
}

// ValidateSubscriptions validates either hydrated runtime fields or their
// persisted JSON representation.
func (w *WebhookConfig) ValidateSubscriptions(requireEvent bool) error {
	events := w.EnabledEventsObj
	if events == nil && strings.TrimSpace(w.EnabledEvents) != "" {
		if err := json.Unmarshal([]byte(w.EnabledEvents), &events); err != nil {
			return fmt.Errorf("decode Webhook enabled events: %w", err)
		}
	}
	filters := w.FilterRulesObj
	if filters == nil && strings.TrimSpace(w.FilterRules) != "" {
		decoded, err := DecodeWebhookFilterRules(w.FilterRules)
		if err != nil {
			return err
		}
		filters = decoded
	}
	return ValidateWebhookSubscriptions(events, filters, requireEvent)
}

// IsEventEnabled 检查是否启用了特定事件
func (w *WebhookConfig) IsEventEnabled(eventType WebhookEventType) bool {
	for _, event := range w.EnabledEventsObj {
		if event == eventType {
			return true
		}
	}
	return false
}

// MatchesEvent evaluates the optional predicate after an exact canonical
// CloudEvent type match.
func (w *WebhookConfig) MatchesEvent(
	eventType WebhookEventType,
	transitionStatus TicketStatus,
) bool {
	if !w.IsEventEnabled(eventType) {
		return false
	}
	if eventType != WebhookEventTicketTransitioned ||
		w.FilterRulesObj == nil ||
		len(w.FilterRulesObj.TransitionStatuses) == 0 {
		return true
	}
	for _, allowed := range w.FilterRulesObj.TransitionStatuses {
		if transitionStatus == allowed {
			return true
		}
	}
	return false
}

// WebhookLog 通知日志模型
type WebhookLog struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`

	// 关联配置
	ConfigID uint           `json:"config_id" gorm:"not null;index"`
	Config   *WebhookConfig `json:"config,omitempty" gorm:"foreignKey:ConfigID"`

	// 事件信息
	EventType    WebhookEventType `json:"event_type" gorm:"size:50;not null;index"` // canonical CloudEvent type
	EventData    string           `json:"event_data" gorm:"type:text"`              // JSON格式的事件数据
	ResourceID   uint             `json:"resource_id" gorm:"index"`                 // 相关资源ID(如工单ID)
	ResourceType string           `json:"resource_type" gorm:"size:50;index"`       // 资源类型

	// 请求信息
	RequestURL     string `json:"request_url" gorm:"size:500"`
	RequestMethod  string `json:"request_method" gorm:"size:10;default:'POST'"`
	RequestHeaders string `json:"request_headers" gorm:"type:text"` // JSON格式
	RequestBody    string `json:"request_body" gorm:"type:text"`

	// 响应信息
	ResponseStatus  int    `json:"response_status"`
	ResponseHeaders string `json:"response_headers" gorm:"type:text"` // JSON格式
	ResponseBody    string `json:"response_body" gorm:"type:text"`
	ResponseTime    int64  `json:"response_time"` // 响应时间(毫秒)

	// 执行状态。WebhookLog 只记录单次 Outbox 尝试；跨尝试重试状态由
	// outbox_deliveries 持久化管理。
	Status       string     `json:"status" gorm:"size:20;not null;index"` // pending, success, failed
	ErrorMessage string     `json:"error_message" gorm:"type:text"`
	RetryCount   int        `json:"retry_count" gorm:"default:0"`
	MaxRetries   int        `json:"max_retries" gorm:"default:0"`
	NextRetryAt  *time.Time `json:"next_retry_at,omitempty"`

	// 元数据
	UserAgent   string `json:"user_agent" gorm:"size:500"`
	SourceIP    string `json:"source_ip" gorm:"size:45"`
	TraceID     string `json:"trace_id" gorm:"size:100;index"` // 分布式追踪ID
	Environment string `json:"environment" gorm:"size:20"`     // 环境标识
}
