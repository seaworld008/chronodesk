package models

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// WebhookProvider 通知提供商枚举
type WebhookProvider string

const (
	WebhookProviderWeChat     WebhookProvider = "wechat"     // 企业微信
	WebhookProviderDingTalk   WebhookProvider = "dingtalk"   // 钉钉
	WebhookProviderLark       WebhookProvider = "lark"       // 飞书
	WebhookProviderSlack      WebhookProvider = "slack"      // Slack
	WebhookProviderTeams      WebhookProvider = "teams"      // Microsoft Teams
	WebhookProviderCustom     WebhookProvider = "custom"     // 自定义webhook
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
	WebhookEventTicketCreated   WebhookEventType = "ticket.created"   // 工单创建
	WebhookEventTicketAssigned  WebhookEventType = "ticket.assigned"  // 工单分配
	WebhookEventTicketUpdated   WebhookEventType = "ticket.updated"   // 工单更新
	WebhookEventTicketResolved  WebhookEventType = "ticket.resolved"  // 工单解决
	WebhookEventTicketClosed    WebhookEventType = "ticket.closed"    // 工单关闭
	WebhookEventTicketComment   WebhookEventType = "ticket.comment"   // 工单评论
	WebhookEventTicketEscalated WebhookEventType = "ticket.escalated" // 工单升级
	WebhookEventUserRegistered  WebhookEventType = "user.registered"  // 用户注册
	WebhookEventSystemAlert     WebhookEventType = "system.alert"     // 系统告警
)

// WebhookConfig Webhook配置模型
type WebhookConfig struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" gorm:"index"`

	// 基本配置
	Name        string          `json:"name" gorm:"size:100;not null" validate:"required,max=100"`
	Description string          `json:"description" gorm:"size:500" validate:"max=500"`
	Provider    WebhookProvider `json:"provider" gorm:"size:20;not null" validate:"required"`
	WebhookURL  string          `json:"webhook_url" gorm:"size:500;not null" validate:"required,url"`
	Status      WebhookStatus   `json:"status" gorm:"size:20;not null;default:'active'" validate:"required"`

	// 认证配置
	Secret      string `json:"-" gorm:"size:255"` // 签名密钥，不返回给前端
	AccessToken string `json:"-" gorm:"size:255"` // 访问令牌，不返回给前端

	// 事件配置
	EnabledEvents    string `json:"enabled_events" gorm:"type:text"` // JSON数组存储启用的事件类型
	EnabledEventsObj []WebhookEventType `json:"enabled_events_list,omitempty" gorm:"-"` // 运行时解析字段

	// 消息配置
	MessageTemplate string `json:"message_template" gorm:"type:text"` // 消息模板
	MessageFormat   string `json:"message_format" gorm:"size:20;default:'markdown'"` // markdown, text, card

	// 过滤配置
	FilterRules    string          `json:"filter_rules" gorm:"type:text"` // JSON对象存储过滤规则
	FilterRulesObj json.RawMessage `json:"filter_rules_obj,omitempty" gorm:"-"` // 运行时解析字段

	// 高级配置
	RetryCount     int  `json:"retry_count" gorm:"default:3" validate:"min=0,max=10"`
	RetryInterval  int  `json:"retry_interval" gorm:"default:60" validate:"min=5,max=3600"` // 秒
	TimeoutSeconds int  `json:"timeout_seconds" gorm:"default:30" validate:"min=5,max=300"`
	IsAsync        bool `json:"is_async" gorm:"default:true"` // 是否异步发送

	// 限流配置
	RateLimit       int `json:"rate_limit" gorm:"default:60" validate:"min=1,max=1000"`     // 每分钟最大请求数
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
	if len(w.EnabledEventsObj) > 0 {
		eventsData, err := json.Marshal(w.EnabledEventsObj)
		if err != nil {
			return err
		}
		w.EnabledEvents = string(eventsData)
	}

	// 将FilterRulesObj序列化为JSON字符串
	if len(w.FilterRulesObj) > 0 {
		w.FilterRules = string(w.FilterRulesObj)
	}

	return nil
}

// AfterFind GORM钩子 - 查询后处理
func (w *WebhookConfig) AfterFind(tx *gorm.DB) error {
	// 反序列化EnabledEvents
	if w.EnabledEvents != "" {
		var events []WebhookEventType
		if err := json.Unmarshal([]byte(w.EnabledEvents), &events); err == nil {
			w.EnabledEventsObj = events
		}
	}

	// 反序列化FilterRules
	if w.FilterRules != "" {
		w.FilterRulesObj = json.RawMessage(w.FilterRules)
	}

	return nil
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

// GetProviderConfig 获取提供商特定配置
func (w *WebhookConfig) GetProviderConfig() map[string]interface{} {
	config := make(map[string]interface{})
	
	switch w.Provider {
	case WebhookProviderWeChat:
		config["msgtype"] = "markdown"
		config["webhook_url"] = w.WebhookURL
	case WebhookProviderDingTalk:
		config["msgtype"] = "markdown"
		config["webhook_url"] = w.WebhookURL
		if w.Secret != "" {
			config["secret"] = w.Secret
		}
	case WebhookProviderLark:
		config["msg_type"] = "interactive"
		config["webhook_url"] = w.WebhookURL
		if w.Secret != "" {
			config["sign"] = w.Secret
		}
	default:
		config["webhook_url"] = w.WebhookURL
	}

	return config
}

// WebhookLog 通知日志模型
type WebhookLog struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`

	// 关联配置
	ConfigID uint           `json:"config_id" gorm:"not null;index"`
	Config   *WebhookConfig `json:"config,omitempty" gorm:"foreignKey:ConfigID"`

	// 事件信息
	EventType   WebhookEventType `json:"event_type" gorm:"size:50;not null;index"`
	EventData   string           `json:"event_data" gorm:"type:text"` // JSON格式的事件数据
	ResourceID  uint             `json:"resource_id" gorm:"index"`    // 相关资源ID(如工单ID)
	ResourceType string          `json:"resource_type" gorm:"size:50;index"` // 资源类型

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

	// 执行状态
	Status       string `json:"status" gorm:"size:20;not null;index"` // pending, success, failed, retrying
	ErrorMessage string `json:"error_message" gorm:"type:text"`
	RetryCount   int    `json:"retry_count" gorm:"default:0"`
	MaxRetries   int    `json:"max_retries" gorm:"default:3"`
	NextRetryAt  *time.Time `json:"next_retry_at,omitempty"`

	// 元数据
	UserAgent   string `json:"user_agent" gorm:"size:500"`
	SourceIP    string `json:"source_ip" gorm:"size:45"`
	TraceID     string `json:"trace_id" gorm:"size:100;index"` // 分布式追踪ID
	Environment string `json:"environment" gorm:"size:20"`     // 环境标识
}