package models

import (
	"time"
)

// NotificationType 通知类型枚举
type NotificationType string

const (
	NotificationTypeTicketAssigned      NotificationType = "ticket_assigned"      // 工单分配
	NotificationTypeTicketStatusChanged NotificationType = "ticket_status_changed" // 状态变更
	NotificationTypeTicketCommented     NotificationType = "ticket_commented"     // 评论通知
	NotificationTypeTicketCreated       NotificationType = "ticket_created"       // 工单创建
	NotificationTypeTicketOverdue       NotificationType = "ticket_overdue"       // 工单逾期
	NotificationTypeTicketResolved      NotificationType = "ticket_resolved"      // 工单解决
	NotificationTypeTicketClosed        NotificationType = "ticket_closed"        // 工单关闭
	NotificationTypeSystemMaintenance   NotificationType = "system_maintenance"   // 系统维护
	NotificationTypeUserMention         NotificationType = "user_mention"         // 用户提及
	NotificationTypeSystemAlert         NotificationType = "system_alert"         // 系统警报
)

// NotificationPriority 通知优先级
type NotificationPriority string

const (
	NotificationPriorityLow    NotificationPriority = "low"
	NotificationPriorityNormal NotificationPriority = "normal"
	NotificationPriorityHigh   NotificationPriority = "high"
	NotificationPriorityUrgent NotificationPriority = "urgent"
)

// NotificationChannel 通知渠道
type NotificationChannel string

const (
	NotificationChannelInApp    NotificationChannel = "in_app"    // 应用内通知
	NotificationChannelEmail    NotificationChannel = "email"     // 邮件通知
	NotificationChannelWebhook  NotificationChannel = "webhook"   // Webhook通知
	NotificationChannelWebSocket NotificationChannel = "websocket" // WebSocket实时通知
)

// Notification 通知模型
type Notification struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	// 基本信息
	Type        NotificationType     `json:"type" gorm:"size:50;not null;index" validate:"required"`
	Title       string               `json:"title" gorm:"size:255;not null" validate:"required,max=255"`
	Content     string               `json:"content" gorm:"type:text" validate:"required"`
	Priority    NotificationPriority `json:"priority" gorm:"size:20;not null;default:'normal'" validate:"required,oneof=low normal high urgent"`
	Channel     NotificationChannel  `json:"channel" gorm:"size:20;not null;default:'in_app'" validate:"required,oneof=in_app email webhook websocket"`

	// 接收者信息
	RecipientID uint  `json:"recipient_id" gorm:"not null;index"`
	Recipient   *User `json:"recipient,omitempty" gorm:"foreignKey:RecipientID"`

	// 发送者信息
	SenderID *uint `json:"sender_id" gorm:"index"`
	Sender   *User `json:"sender,omitempty" gorm:"foreignKey:SenderID"`

	// 关联信息
	RelatedType     string `json:"related_type" gorm:"size:50"` // 相关对象类型（如：ticket, user, system）
	RelatedID       *uint  `json:"related_id" gorm:"index"`     // 相关对象ID
	RelatedTicketID *uint  `json:"related_ticket_id" gorm:"index"`
	RelatedTicket   *Ticket `json:"related_ticket,omitempty" gorm:"foreignKey:RelatedTicketID"`

	// 状态信息
	IsRead     bool       `json:"is_read" gorm:"default:false"`
	ReadAt     *time.Time `json:"read_at"`
	IsSent     bool       `json:"is_sent" gorm:"default:false"`
	SentAt     *time.Time `json:"sent_at"`
	IsDelivered bool      `json:"is_delivered" gorm:"default:false"`
	DeliveredAt *time.Time `json:"delivered_at"`

	// 重试信息
	RetryCount   int        `json:"retry_count" gorm:"default:0"`
	LastRetryAt  *time.Time `json:"last_retry_at"`
	NextRetryAt  *time.Time `json:"next_retry_at"`
	MaxRetries   int        `json:"max_retries" gorm:"default:3"`

	// 元数据
	Metadata       string     `json:"metadata" gorm:"type:text"`        // JSON格式存储额外数据
	ActionURL      string     `json:"action_url" gorm:"size:500"`       // 操作链接
	ExpiresAt      *time.Time `json:"expires_at"`                       // 过期时间
	ScheduledAt    *time.Time `json:"scheduled_at"`                     // 计划发送时间
	ErrorMessage   string     `json:"error_message" gorm:"type:text"`   // 错误信息
	DeliveryStatus string     `json:"delivery_status" gorm:"size:50"`   // 投递状态
}

// TableName 指定表名
func (Notification) TableName() string {
	return "notifications"
}

// MarkAsRead 标记为已读
func (n *Notification) MarkAsRead() {
	if !n.IsRead {
		n.IsRead = true
		now := time.Now()
		n.ReadAt = &now
		n.UpdatedAt = now
	}
}

// MarkAsSent 标记为已发送
func (n *Notification) MarkAsSent() {
	if !n.IsSent {
		n.IsSent = true
		now := time.Now()
		n.SentAt = &now
		n.UpdatedAt = now
	}
}

// MarkAsDelivered 标记为已投递
func (n *Notification) MarkAsDelivered() {
	if !n.IsDelivered {
		n.IsDelivered = true
		now := time.Now()
		n.DeliveredAt = &now
		n.UpdatedAt = now
	}
}

// ShouldRetry 检查是否应该重试
func (n *Notification) ShouldRetry() bool {
	return n.RetryCount < n.MaxRetries && !n.IsSent
}

// IncrementRetry 增加重试次数
func (n *Notification) IncrementRetry(nextRetryInterval time.Duration) {
	n.RetryCount++
	now := time.Now()
	n.LastRetryAt = &now
	nextRetry := now.Add(nextRetryInterval)
	n.NextRetryAt = &nextRetry
	n.UpdatedAt = now
}

// IsExpired 检查是否过期
func (n *Notification) IsExpired() bool {
	return n.ExpiresAt != nil && n.ExpiresAt.Before(time.Now())
}

// IsScheduled 检查是否为计划发送
func (n *Notification) IsScheduled() bool {
	return n.ScheduledAt != nil && n.ScheduledAt.After(time.Now())
}

// NotificationCreateRequest 通知创建请求
type NotificationCreateRequest struct {
	Type            NotificationType     `json:"type" validate:"required"`
	Title           string               `json:"title" validate:"required,max=255"`
	Content         string               `json:"content" validate:"required"`
	Priority        NotificationPriority `json:"priority" validate:"omitempty,oneof=low normal high urgent"`
	Channel         NotificationChannel  `json:"channel" validate:"omitempty,oneof=in_app email webhook websocket"`
	RecipientID     uint                 `json:"recipient_id" validate:"required"`
	SenderID        *uint                `json:"sender_id"`
	RelatedType     string               `json:"related_type"`
	RelatedID       *uint                `json:"related_id"`
	RelatedTicketID *uint                `json:"related_ticket_id"`
	ActionURL       string               `json:"action_url"`
	ScheduledAt     *time.Time           `json:"scheduled_at"`
	ExpiresAt       *time.Time           `json:"expires_at"`
	Metadata        map[string]interface{} `json:"metadata"`
}

// NotificationResponse 通知响应
type NotificationResponse struct {
	ID              uint                   `json:"id"`
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
	Type            NotificationType       `json:"type"`
	Title           string                 `json:"title"`
	Content         string                 `json:"content"`
	Priority        NotificationPriority   `json:"priority"`
	Channel         NotificationChannel    `json:"channel"`
	Recipient       *UserResponse          `json:"recipient,omitempty"`
	Sender          *UserResponse          `json:"sender,omitempty"`
	RelatedType     string                 `json:"related_type"`
	RelatedID       *uint                  `json:"related_id"`
	RelatedTicket   *TicketResponse        `json:"related_ticket,omitempty"`
	IsRead          bool                   `json:"is_read"`
	ReadAt          *time.Time             `json:"read_at"`
	IsSent          bool                   `json:"is_sent"`
	SentAt          *time.Time             `json:"sent_at"`
	IsDelivered     bool                   `json:"is_delivered"`
	DeliveredAt     *time.Time             `json:"delivered_at"`
	ActionURL       string                 `json:"action_url"`
	ScheduledAt     *time.Time             `json:"scheduled_at"`
	ExpiresAt       *time.Time             `json:"expires_at"`
	Metadata        map[string]interface{} `json:"metadata"`
	DeliveryStatus  string                 `json:"delivery_status"`
}

// ToResponse 转换为响应格式
func (n *Notification) ToResponse() *NotificationResponse {
	response := &NotificationResponse{
		ID:             n.ID,
		CreatedAt:      n.CreatedAt,
		UpdatedAt:      n.UpdatedAt,
		Type:           n.Type,
		Title:          n.Title,
		Content:        n.Content,
		Priority:       n.Priority,
		Channel:        n.Channel,
		RelatedType:    n.RelatedType,
		RelatedID:      n.RelatedID,
		IsRead:         n.IsRead,
		ReadAt:         n.ReadAt,
		IsSent:         n.IsSent,
		SentAt:         n.SentAt,
		IsDelivered:    n.IsDelivered,
		DeliveredAt:    n.DeliveredAt,
		ActionURL:      n.ActionURL,
		ScheduledAt:    n.ScheduledAt,
		ExpiresAt:      n.ExpiresAt,
		DeliveryStatus: n.DeliveryStatus,
	}

	// 处理关联用户
	if n.Recipient != nil {
		response.Recipient = n.Recipient.ToResponse()
	}
	if n.Sender != nil {
		response.Sender = n.Sender.ToResponse()
	}
	if n.RelatedTicket != nil {
		response.RelatedTicket = n.RelatedTicket.ToResponse()
	}

	// TODO: 解析JSON字段
	// response.Metadata = parseMetadataFromJSON(n.Metadata)

	return response
}

// NotificationFilter 通知过滤器
type NotificationFilter struct {
	RecipientID    *uint                  `json:"recipient_id"`
	SenderID       *uint                  `json:"sender_id"`
	Types          []NotificationType     `json:"types"`
	Priorities     []NotificationPriority `json:"priorities"`
	Channels       []NotificationChannel  `json:"channels"`
	IsRead         *bool                  `json:"is_read"`
	IsSent         *bool                  `json:"is_sent"`
	IsDelivered    *bool                  `json:"is_delivered"`
	RelatedType    string                 `json:"related_type"`
	RelatedID      *uint                  `json:"related_id"`
	RelatedTicketID *uint                 `json:"related_ticket_id"`
	CreatedAfter   *time.Time             `json:"created_after"`
	CreatedBefore  *time.Time             `json:"created_before"`
	Query          string                 `json:"query"`
	Limit          int                    `json:"limit"`
	Offset         int                    `json:"offset"`
	OrderBy        string                 `json:"order_by"`  // created_at, priority, type
	OrderDir       string                 `json:"order_dir"` // asc, desc
}

// NotificationPreference 用户通知偏好设置
type NotificationPreference struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	UserID uint  `json:"user_id" gorm:"not null;uniqueIndex:idx_user_notification_type"`
	User   *User `json:"user,omitempty" gorm:"foreignKey:UserID"`

	// 通知类型和渠道设置
	NotificationType NotificationType    `json:"notification_type" gorm:"size:50;not null;uniqueIndex:idx_user_notification_type"`
	EmailEnabled     bool                `json:"email_enabled" gorm:"default:true"`
	InAppEnabled     bool                `json:"in_app_enabled" gorm:"default:true"`
	WebhookEnabled   bool                `json:"webhook_enabled" gorm:"default:false"`
	
	// 通知时间设置
	DoNotDisturbStart *time.Time `json:"do_not_disturb_start"`
	DoNotDisturbEnd   *time.Time `json:"do_not_disturb_end"`
	
	// 频率控制
	MaxDailyCount     int  `json:"max_daily_count" gorm:"default:50"`
	BatchDelivery     bool `json:"batch_delivery" gorm:"default:false"`
	BatchInterval     int  `json:"batch_interval" gorm:"default:60"` // 分钟
}

// TableName 指定表名
func (NotificationPreference) TableName() string {
	return "notification_preferences"
}
