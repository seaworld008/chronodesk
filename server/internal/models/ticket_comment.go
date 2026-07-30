package models

import (
	"time"

	"gorm.io/gorm"
)

// CommentType 评论类型枚举
type CommentType string

const (
	CommentTypePublic   CommentType = "public"   // 公开评论
	CommentTypeInternal CommentType = "internal" // 内部评论
	CommentTypeSystem   CommentType = "system"   // 系统评论
)

// TicketComment 工单评论模型
type TicketComment struct {
	ID        uint       `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" gorm:"index"`

	// 关联信息
	OrganizationID     uint              `json:"organization_id" gorm:"index"`
	ProjectID          uint              `json:"project_id" gorm:"index"`
	TicketID           uint              `json:"ticket_id" gorm:"not null;index"`
	Ticket             *Ticket           `json:"ticket,omitempty" gorm:"foreignKey:TicketID"`
	UserID             *uint             `json:"user_id,omitempty" gorm:"index"`
	User               *User             `json:"user,omitempty" gorm:"foreignKey:UserID"`
	ActorType          ActorType         `json:"actor_type" gorm:"size:32;not null;default:'human';index"`
	ActorID            string            `json:"actor_id" gorm:"size:128;index"`
	ServicePrincipalID *string           `json:"service_principal_id,omitempty" gorm:"size:36;index"`
	ServicePrincipal   *ServicePrincipal `json:"service_principal,omitempty" gorm:"foreignKey:ServicePrincipalID"`

	// 评论内容
	Content     string      `json:"content" gorm:"type:text;not null" validate:"required"`
	ContentType string      `json:"content_type" gorm:"size:20;default:'text'"` // text, html, markdown
	Type        CommentType `json:"type" gorm:"size:20;not null;default:'public'" validate:"required,oneof=public internal system"`

	// 元数据
	Metadata  string `json:"metadata" gorm:"type:text"` // JSON格式存储元数据
	SourceIP  string `json:"source_ip" gorm:"size:45"`
	UserAgent string `json:"user_agent" gorm:"size:500"`

	// 状态信息
	IsEdited    bool       `json:"is_edited" gorm:"default:false"`
	EditedAt    *time.Time `json:"edited_at,omitempty"`
	IsDeleted   bool       `json:"is_deleted" gorm:"default:false"`
	DeletedBy   *uint      `json:"deleted_by,omitempty" gorm:"index"`
	DeletedUser *User      `json:"deleted_user,omitempty" gorm:"foreignKey:DeletedBy"`

	// 回复相关
	ParentID   *uint           `json:"parent_id,omitempty" gorm:"index"`
	Parent     *TicketComment  `json:"parent,omitempty" gorm:"foreignKey:ParentID"`
	Replies    []TicketComment `json:"replies,omitempty" gorm:"foreignKey:ParentID"`
	ReplyCount int             `json:"reply_count" gorm:"default:0"`

	// 时间跟踪
	TimeSpent    *int   `json:"time_spent,omitempty"`     // 花费时间（分钟）
	BillableTime *int   `json:"billable_time,omitempty"`  // 计费时间（分钟）
	WorkType     string `json:"work_type" gorm:"size:50"` // 工作类型

	// 通知相关
	NotificationSent bool       `json:"notification_sent" gorm:"default:false"`
	NotificationAt   *time.Time `json:"notification_at,omitempty"`

	// 评分相关
	IsHelpful      *bool `json:"is_helpful,omitempty"` // 是否有帮助
	HelpfulCount   int   `json:"helpful_count" gorm:"default:0"`
	UnhelpfulCount int   `json:"unhelpful_count" gorm:"default:0"`
}

// TableName 指定表名
func (TicketComment) TableName() string {
	return "ticket_comments"
}

func (comment *TicketComment) BeforeCreate(tx *gorm.DB) error {
	return inheritTicketProjectScope(
		tx,
		comment.TicketID,
		&comment.OrganizationID,
		&comment.ProjectID,
	)
}

// Actor returns the authoritative ActorRef. Migration and database constraints
// guarantee that it is complete and consistent with the optional projection.
func (tc *TicketComment) Actor() ActorRef {
	return ActorRef{Type: tc.ActorType, ID: tc.ActorID}
}

// TicketCommentCreateRequest 评论创建请求
type TicketCommentCreateRequest struct {
	TicketID     uint                   `json:"ticket_id" validate:"required"`
	Content      string                 `json:"content" validate:"required"`
	ContentType  string                 `json:"content_type" validate:"omitempty,oneof=text html markdown"`
	Type         CommentType            `json:"type" validate:"required,oneof=public internal system"`
	ParentID     *uint                  `json:"parent_id"`
	TimeSpent    *int                   `json:"time_spent" validate:"omitempty,min=0"`
	BillableTime *int                   `json:"billable_time" validate:"omitempty,min=0"`
	WorkType     string                 `json:"work_type"`
	Metadata     map[string]interface{} `json:"metadata"`
}

// TicketCommentResponse 评论响应
type TicketCommentResponse struct {
	ID               uint                     `json:"id"`
	CreatedAt        time.Time                `json:"created_at"`
	UpdatedAt        time.Time                `json:"updated_at"`
	TicketID         uint                     `json:"ticket_id"`
	OrganizationID   uint                     `json:"organization_id"`
	ProjectID        uint                     `json:"project_id"`
	User             *UserSummary             `json:"user,omitempty"`
	Actor            ActorRef                 `json:"actor"`
	ServicePrincipal *ServicePrincipalSummary `json:"service_principal,omitempty"`
	Content          string                   `json:"content"`
	ContentType      string                   `json:"content_type"`
	Type             CommentType              `json:"type"`
	Metadata         map[string]interface{}   `json:"metadata"`
	IsEdited         bool                     `json:"is_edited"`
	EditedAt         *time.Time               `json:"edited_at"`
	IsDeleted        bool                     `json:"is_deleted"`
	DeletedBy        *UserSummary             `json:"deleted_by,omitempty"`
	ParentID         *uint                    `json:"parent_id"`
	Replies          []TicketCommentResponse  `json:"replies,omitempty"`
	ReplyCount       int                      `json:"reply_count"`
	TimeSpent        *int                     `json:"time_spent"`
	BillableTime     *int                     `json:"billable_time"`
	WorkType         string                   `json:"work_type"`
	NotificationSent bool                     `json:"notification_sent"`
	IsHelpful        *bool                    `json:"is_helpful"`
	HelpfulCount     int                      `json:"helpful_count"`
	UnhelpfulCount   int                      `json:"unhelpful_count"`
}

// ToResponse 转换为响应格式
func (tc *TicketComment) ToResponse() *TicketCommentResponse {
	response := &TicketCommentResponse{
		ID:               tc.ID,
		CreatedAt:        tc.CreatedAt,
		UpdatedAt:        tc.UpdatedAt,
		TicketID:         tc.TicketID,
		OrganizationID:   tc.OrganizationID,
		ProjectID:        tc.ProjectID,
		Actor:            tc.Actor(),
		Content:          tc.Content,
		ContentType:      tc.ContentType,
		Type:             tc.Type,
		IsEdited:         tc.IsEdited,
		EditedAt:         tc.EditedAt,
		IsDeleted:        tc.IsDeleted,
		ParentID:         tc.ParentID,
		ReplyCount:       tc.ReplyCount,
		TimeSpent:        tc.TimeSpent,
		BillableTime:     tc.BillableTime,
		WorkType:         tc.WorkType,
		NotificationSent: tc.NotificationSent,
		IsHelpful:        tc.IsHelpful,
		HelpfulCount:     tc.HelpfulCount,
		UnhelpfulCount:   tc.UnhelpfulCount,
	}

	// 处理关联用户
	if tc.User != nil {
		response.User = tc.User.ToSummary()
	}
	if tc.DeletedUser != nil {
		response.DeletedBy = tc.DeletedUser.ToSummary()
	}
	response.ServicePrincipal = tc.ServicePrincipal.ToSummary()

	// 处理回复
	if len(tc.Replies) > 0 {
		response.Replies = make([]TicketCommentResponse, len(tc.Replies))
		for i, reply := range tc.Replies {
			response.Replies[i] = *reply.ToResponse()
		}
	}

	response.Metadata = decodeJSONMap(tc.Metadata)

	return response
}
