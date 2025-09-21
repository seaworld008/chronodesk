package models

import (
	"time"
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
	TicketID uint    `json:"ticket_id" gorm:"not null;index"`
	Ticket   *Ticket `json:"ticket,omitempty" gorm:"foreignKey:TicketID"`
	UserID   uint    `json:"user_id" gorm:"not null;index"`
	User     *User   `json:"user,omitempty" gorm:"foreignKey:UserID"`

	// 评论内容
	Content     string      `json:"content" gorm:"type:text;not null" validate:"required"`
	ContentType string      `json:"content_type" gorm:"size:20;default:'text'"` // text, html, markdown
	Type        CommentType `json:"type" gorm:"size:20;not null;default:'public'" validate:"required,oneof=public internal system"`

	// 附件和元数据
	Attachments string `json:"attachments" gorm:"type:text"` // JSON格式存储附件列表
	Metadata    string `json:"metadata" gorm:"type:text"`    // JSON格式存储元数据
	SourceIP    string `json:"source_ip" gorm:"size:45"`
	UserAgent   string `json:"user_agent" gorm:"size:500"`

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

// IsPublic 检查是否为公开评论
func (tc *TicketComment) IsPublic() bool {
	return tc.Type == CommentTypePublic
}

// IsInternal 检查是否为内部评论
func (tc *TicketComment) IsInternal() bool {
	return tc.Type == CommentTypeInternal
}

// IsSystem 检查是否为系统评论
func (tc *TicketComment) IsSystem() bool {
	return tc.Type == CommentTypeSystem
}

// CanBeEdited 检查评论是否可以编辑
func (tc *TicketComment) CanBeEdited() bool {
	return !tc.IsDeleted && tc.Type != CommentTypeSystem
}

// CanBeDeleted 检查评论是否可以删除
func (tc *TicketComment) CanBeDeleted() bool {
	return !tc.IsDeleted
}

// IsReply 检查是否为回复
func (tc *TicketComment) IsReply() bool {
	return tc.ParentID != nil
}

// HasReplies 检查是否有回复
func (tc *TicketComment) HasReplies() bool {
	return tc.ReplyCount > 0
}

// TicketCommentCreateRequest 评论创建请求
type TicketCommentCreateRequest struct {
	TicketID     uint                   `json:"ticket_id" validate:"required"`
	Content      string                 `json:"content" validate:"required"`
	ContentType  string                 `json:"content_type" validate:"omitempty,oneof=text html markdown"`
	Type         CommentType            `json:"type" validate:"required,oneof=public internal system"`
	ParentID     *uint                  `json:"parent_id"`
	Attachments  []string               `json:"attachments"`
	TimeSpent    *int                   `json:"time_spent" validate:"omitempty,min=0"`
	BillableTime *int                   `json:"billable_time" validate:"omitempty,min=0"`
	WorkType     string                 `json:"work_type"`
	Metadata     map[string]interface{} `json:"metadata"`
}

// TicketCommentUpdateRequest 评论更新请求
type TicketCommentUpdateRequest struct {
	Content      *string                `json:"content" validate:"omitempty,min=1"`
	ContentType  *string                `json:"content_type" validate:"omitempty,oneof=text html markdown"`
	Type         *CommentType           `json:"type" validate:"omitempty,oneof=public internal system"`
	Attachments  []string               `json:"attachments"`
	TimeSpent    *int                   `json:"time_spent" validate:"omitempty,min=0"`
	BillableTime *int                   `json:"billable_time" validate:"omitempty,min=0"`
	WorkType     *string                `json:"work_type"`
	Metadata     map[string]interface{} `json:"metadata"`
}

// TicketCommentResponse 评论响应
type TicketCommentResponse struct {
	ID               uint                    `json:"id"`
	CreatedAt        time.Time               `json:"created_at"`
	UpdatedAt        time.Time               `json:"updated_at"`
	TicketID         uint                    `json:"ticket_id"`
	User             *UserResponse           `json:"user,omitempty"`
	Content          string                  `json:"content"`
	ContentType      string                  `json:"content_type"`
	Type             CommentType             `json:"type"`
	Attachments      []string                `json:"attachments"`
	Metadata         map[string]interface{}  `json:"metadata"`
	IsEdited         bool                    `json:"is_edited"`
	EditedAt         *time.Time              `json:"edited_at"`
	IsDeleted        bool                    `json:"is_deleted"`
	DeletedBy        *UserResponse           `json:"deleted_by,omitempty"`
	ParentID         *uint                   `json:"parent_id"`
	Replies          []TicketCommentResponse `json:"replies,omitempty"`
	ReplyCount       int                     `json:"reply_count"`
	TimeSpent        *int                    `json:"time_spent"`
	BillableTime     *int                    `json:"billable_time"`
	WorkType         string                  `json:"work_type"`
	NotificationSent bool                    `json:"notification_sent"`
	IsHelpful        *bool                   `json:"is_helpful"`
	HelpfulCount     int                     `json:"helpful_count"`
	UnhelpfulCount   int                     `json:"unhelpful_count"`
}

// ToResponse 转换为响应格式
func (tc *TicketComment) ToResponse() *TicketCommentResponse {
	response := &TicketCommentResponse{
		ID:               tc.ID,
		CreatedAt:        tc.CreatedAt,
		UpdatedAt:        tc.UpdatedAt,
		TicketID:         tc.TicketID,
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
		response.User = tc.User.ToResponse()
	}
	if tc.DeletedUser != nil {
		response.DeletedBy = tc.DeletedUser.ToResponse()
	}

	// 处理回复
	if len(tc.Replies) > 0 {
		response.Replies = make([]TicketCommentResponse, len(tc.Replies))
		for i, reply := range tc.Replies {
			response.Replies[i] = *reply.ToResponse()
		}
	}

	// TODO: 解析JSON字段
	// response.Attachments = parseAttachmentsFromJSON(tc.Attachments)
	// response.Metadata = parseMetadataFromJSON(tc.Metadata)

	return response
}
