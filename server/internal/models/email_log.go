package models

import (
	"time"
)

// EmailStatus 邮件状态
type EmailStatus string

const (
	EmailStatusPending   EmailStatus = "pending"   // 待发送
	EmailStatusSending   EmailStatus = "sending"   // 发送中
	EmailStatusSent      EmailStatus = "sent"      // 已发送
	EmailStatusDelivered EmailStatus = "delivered" // 已送达
	EmailStatusOpened    EmailStatus = "opened"    // 已打开
	EmailStatusClicked   EmailStatus = "clicked"   // 已点击
	EmailStatusBounced   EmailStatus = "bounced"   // 退回
	EmailStatusFailed    EmailStatus = "failed"    // 失败
	EmailStatusSpam      EmailStatus = "spam"      // 垃圾邮件
)

// EmailLog 邮件日志
type EmailLog struct {
	ID        uint       `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" gorm:"index"`

	// 基本信息
	MessageID   string      `json:"message_id" gorm:"size:255;uniqueIndex"`
	Subject     string      `json:"subject" gorm:"size:500;not null"`
	Status      EmailStatus `json:"status" gorm:"size:20;not null;index;default:'pending'"`
	Priority    int         `json:"priority" gorm:"default:5"` // 1-10, 1最高
	Template    string      `json:"template" gorm:"size:100"`
	Category    string      `json:"category" gorm:"size:50;index"`
	
	// 发送者和接收者
	From        string `json:"from" gorm:"size:255;not null"`
	FromName    string `json:"from_name" gorm:"size:100"`
	To          string `json:"to" gorm:"type:text;not null"` // JSON array
	Cc          string `json:"cc" gorm:"type:text"`          // JSON array
	Bcc         string `json:"bcc" gorm:"type:text"`         // JSON array
	ReplyTo     string `json:"reply_to" gorm:"size:255"`
	
	// 内容
	ContentType string `json:"content_type" gorm:"size:50;default:'text/html'"`
	Content     string `json:"content" gorm:"type:text"`
	PlainText   string `json:"plain_text" gorm:"type:text"`
	Attachments string `json:"attachments" gorm:"type:text"` // JSON array
	
	// 关联信息
	UserID   *uint   `json:"user_id,omitempty" gorm:"index"`
	User     *User   `json:"user,omitempty" gorm:"foreignKey:UserID"`
	TicketID *uint   `json:"ticket_id,omitempty" gorm:"index"`
	Ticket   *Ticket `json:"ticket,omitempty" gorm:"foreignKey:TicketID"`
	RelatedType string `json:"related_type" gorm:"size:50;index"` // ticket, user, system
	RelatedID   uint   `json:"related_id" gorm:"index"`
	
	// 发送信息
	Provider     string     `json:"provider" gorm:"size:50"` // smtp, sendgrid, mailgun, ses
	ProviderID   string     `json:"provider_id" gorm:"size:255;index"`
	SentAt       *time.Time `json:"sent_at,omitempty" gorm:"index"`
	DeliveredAt  *time.Time `json:"delivered_at,omitempty"`
	OpenedAt     *time.Time `json:"opened_at,omitempty"`
	ClickedAt    *time.Time `json:"clicked_at,omitempty"`
	BouncedAt    *time.Time `json:"bounced_at,omitempty"`
	
	// 统计信息
	OpenCount   int `json:"open_count" gorm:"default:0"`
	ClickCount  int `json:"click_count" gorm:"default:0"`
	SendAttempts int `json:"send_attempts" gorm:"default:0"`
	
	// 错误信息
	Error        string `json:"error" gorm:"type:text"`
	ErrorCode    string `json:"error_code" gorm:"size:50"`
	BounceReason string `json:"bounce_reason" gorm:"size:255"`
	BounceType   string `json:"bounce_type" gorm:"size:50"` // hard, soft, block
	
	// 调度信息
	ScheduledAt  *time.Time `json:"scheduled_at,omitempty" gorm:"index"`
	RetryAt      *time.Time `json:"retry_at,omitempty"`
	RetryCount   int        `json:"retry_count" gorm:"default:0"`
	MaxRetries   int        `json:"max_retries" gorm:"default:3"`
	
	// 追踪信息
	IPAddress    string `json:"ip_address" gorm:"size:45"`
	UserAgent    string `json:"user_agent" gorm:"size:500"`
	ClickedLinks string `json:"clicked_links" gorm:"type:text"` // JSON array
	
	// 元数据
	Headers  string `json:"headers" gorm:"type:text"`  // JSON object
	Metadata string `json:"metadata" gorm:"type:text"` // JSON object
	Tags     string `json:"tags" gorm:"type:text"`     // JSON array
	
	// 配置
	ConfigID *uint        `json:"config_id,omitempty" gorm:"index"`
	Config   *EmailConfig `json:"config,omitempty" gorm:"foreignKey:ConfigID"`
}

// TableName 指定表名
func (EmailLog) TableName() string {
	return "email_logs"
}