package models

import (
	"time"

	"gorm.io/datatypes"
)

// TicketStatus 工单状态枚举
type TicketStatus string

const (
	TicketStatusOpen       TicketStatus = "open"        // 开放
	TicketStatusInProgress TicketStatus = "in_progress" // 处理中
	TicketStatusPending    TicketStatus = "pending"     // 等待中
	TicketStatusResolved   TicketStatus = "resolved"    // 已解决
	TicketStatusClosed     TicketStatus = "closed"      // 已关闭
	TicketStatusCancelled  TicketStatus = "cancelled"   // 已取消
)

// IsValid reports whether the status is part of the persisted ticket state model.
func (s TicketStatus) IsValid() bool {
	switch s {
	case TicketStatusOpen,
		TicketStatusInProgress,
		TicketStatusPending,
		TicketStatusResolved,
		TicketStatusClosed,
		TicketStatusCancelled:
		return true
	default:
		return false
	}
}

// CanTransitionTo centralizes the ticket lifecycle used by API and UI workflows.
func (s TicketStatus) CanTransitionTo(next TicketStatus) bool {
	if s == next {
		return true
	}

	switch s {
	case TicketStatusOpen:
		return next == TicketStatusInProgress || next == TicketStatusPending || next == TicketStatusResolved || next == TicketStatusCancelled
	case TicketStatusInProgress:
		return next == TicketStatusPending || next == TicketStatusResolved || next == TicketStatusCancelled
	case TicketStatusPending:
		return next == TicketStatusInProgress || next == TicketStatusResolved || next == TicketStatusCancelled
	case TicketStatusResolved:
		return next == TicketStatusClosed || next == TicketStatusOpen
	case TicketStatusCancelled:
		return next == TicketStatusOpen
	default:
		return false
	}
}

// TicketPriority 工单优先级枚举
type TicketPriority string

const (
	TicketPriorityLow      TicketPriority = "low"      // 低
	TicketPriorityNormal   TicketPriority = "normal"   // 普通
	TicketPriorityHigh     TicketPriority = "high"     // 高
	TicketPriorityUrgent   TicketPriority = "urgent"   // 紧急
	TicketPriorityCritical TicketPriority = "critical" // 严重
)

// IsValid reports whether the priority is part of the persisted ticket model.
func (p TicketPriority) IsValid() bool {
	switch p {
	case TicketPriorityLow,
		TicketPriorityNormal,
		TicketPriorityHigh,
		TicketPriorityUrgent,
		TicketPriorityCritical:
		return true
	default:
		return false
	}
}

// TicketType 工单类型枚举
type TicketType string

const (
	TicketTypeIncident     TicketType = "incident"     // 事件
	TicketTypeRequest      TicketType = "request"      // 请求
	TicketTypeProblem      TicketType = "problem"      // 问题
	TicketTypeChange       TicketType = "change"       // 变更
	TicketTypeComplaint    TicketType = "complaint"    // 投诉
	TicketTypeConsultation TicketType = "consultation" // 咨询
)

// IsValid reports whether the type is part of the persisted ticket model.
func (t TicketType) IsValid() bool {
	switch t {
	case TicketTypeIncident,
		TicketTypeRequest,
		TicketTypeProblem,
		TicketTypeChange,
		TicketTypeComplaint,
		TicketTypeConsultation:
		return true
	default:
		return false
	}
}

// TicketSource 工单来源枚举
type TicketSource string

const (
	TicketSourceWeb    TicketSource = "web"    // 网页
	TicketSourceEmail  TicketSource = "email"  // 邮件
	TicketSourcePhone  TicketSource = "phone"  // 电话
	TicketSourceChat   TicketSource = "chat"   // 聊天
	TicketSourceAPI    TicketSource = "api"    // API
	TicketSourceMobile TicketSource = "mobile" // 移动端
	TicketSourceAgent  TicketSource = "agent"  // AI Agent
)

func (s TicketSource) IsValid() bool {
	switch s {
	case TicketSourceWeb,
		TicketSourceEmail,
		TicketSourcePhone,
		TicketSourceChat,
		TicketSourceAPI,
		TicketSourceMobile,
		TicketSourceAgent:
		return true
	default:
		return false
	}
}

// TicketTrustLevel describes how much the system trusts the ticket's external
// source. User-provided text remains untrusted regardless of this value.
type TicketTrustLevel string

const (
	TicketTrustLevelUntrusted TicketTrustLevel = "untrusted"
	TicketTrustLevelVerified  TicketTrustLevel = "verified"
	TicketTrustLevelTrusted   TicketTrustLevel = "trusted"
	TicketTrustLevelSystem    TicketTrustLevel = "system"
)

// AgentContext contains only task facts that are safe to exchange between
// agents. It intentionally has no prompt or chain-of-thought field.
type AgentContext struct {
	Goal               string   `json:"goal,omitempty"`
	Constraints        []string `json:"constraints,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	MissingInformation []string `json:"missing_information,omitempty"`
	RelatedResources   []string `json:"related_resources,omitempty"`
}

// Ticket 工单模型
type Ticket struct {
	ID        uint       `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" gorm:"index"`

	// 基本信息
	TicketNumber string `json:"ticket_number" gorm:"uniqueIndex;size:50;not null"` // 工单编号
	Title        string `json:"title" gorm:"size:255;not null" validate:"required,max=255"`
	Description  string `json:"description" gorm:"type:text" validate:"required"`

	// 分类信息
	Type     TicketType     `json:"type" gorm:"size:20;not null;default:'request';index" validate:"required,oneof=incident request problem change complaint consultation"`
	Priority TicketPriority `json:"priority" gorm:"size:20;not null;default:'normal';index" validate:"required,oneof=low normal high urgent critical"`
	Status   TicketStatus   `json:"status" gorm:"size:20;not null;default:'open';index" validate:"required,oneof=open in_progress pending resolved closed cancelled"`
	Source   TicketSource   `json:"source" gorm:"size:20;not null;default:'web';index" validate:"required,oneof=web email phone chat api mobile agent"`

	// Agent-native concurrency and provenance
	Version      uint64                           `json:"version" gorm:"not null;default:1"`
	AgentContext datatypes.JSONType[AgentContext] `json:"agent_context" gorm:"type:jsonb"`
	TrustLevel   TicketTrustLevel                 `json:"trust_level" gorm:"size:20;not null;default:'untrusted';index"`

	// 用户关联
	CreatedByID  uint  `json:"created_by_id" gorm:"not null;index"`
	CreatedBy    *User `json:"created_by,omitempty" gorm:"foreignKey:CreatedByID"`
	AssignedToID *uint `json:"assigned_to_id,omitempty" gorm:"index"`
	AssignedTo   *User `json:"assigned_to,omitempty" gorm:"foreignKey:AssignedToID"`

	// Actor fields are authoritative for Agent-native writes. The legacy user
	// fields above remain populated with the configured compatibility user.
	CreatedByActorType  ActorType `json:"created_by_actor_type" gorm:"size:32;not null;default:'human';index"`
	CreatedByActorID    string    `json:"created_by_actor_id" gorm:"size:128;index"`
	AssignedToActorType ActorType `json:"assigned_to_actor_type,omitempty" gorm:"size:32;index"`
	AssignedToActorID   string    `json:"assigned_to_actor_id,omitempty" gorm:"size:128;index"`

	CreatedByServicePrincipalID  *string           `json:"created_by_service_principal_id,omitempty" gorm:"size:36;index"`
	CreatedByServicePrincipal    *ServicePrincipal `json:"created_by_service_principal,omitempty" gorm:"foreignKey:CreatedByServicePrincipalID"`
	AssignedToServicePrincipalID *string           `json:"assigned_to_service_principal_id,omitempty" gorm:"size:36;index"`
	AssignedToServicePrincipal   *ServicePrincipal `json:"assigned_to_service_principal,omitempty" gorm:"foreignKey:AssignedToServicePrincipalID"`

	// 分类和标签
	CategoryID    *uint                       `json:"category_id,omitempty" gorm:"index"`
	Category      *Category                   `json:"category,omitempty" gorm:"foreignKey:CategoryID"`
	SubcategoryID *uint                       `json:"subcategory_id,omitempty" gorm:"index"`
	Subcategory   *Category                   `json:"subcategory,omitempty" gorm:"foreignKey:SubcategoryID"`
	Tags          datatypes.JSONSlice[string] `json:"tags" gorm:"type:jsonb"` // JSONB格式存储标签列表

	// 时间跟踪
	DueDate      *time.Time `json:"due_date,omitempty"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
	ClosedAt     *time.Time `json:"closed_at,omitempty"`
	FirstReplyAt *time.Time `json:"first_reply_at,omitempty"`

	// SLA相关
	SLABreached    bool       `json:"sla_breached" gorm:"default:false"`
	SLADueDate     *time.Time `json:"sla_due_date,omitempty"`
	ResponseTime   *int       `json:"response_time,omitempty"`   // 响应时间（分钟）
	ResolutionTime *int       `json:"resolution_time,omitempty"` // 解决时间（分钟）

	// 客户信息
	CustomerEmail string `json:"customer_email" gorm:"size:100"`
	CustomerPhone string `json:"customer_phone" gorm:"size:20"`
	CustomerName  string `json:"customer_name" gorm:"size:100"`

	// 附加信息
	Attachments   datatypes.JSONSlice[string]        `json:"attachments" gorm:"type:jsonb"`   // JSONB格式存储附件列表
	CustomFields  datatypes.JSONType[map[string]any] `json:"custom_fields" gorm:"type:jsonb"` // JSONB格式存储自定义字段
	InternalNotes string                             `json:"internal_notes" gorm:"type:text"` // 内部备注

	// 统计信息
	ViewCount     int    `json:"view_count" gorm:"default:0"`
	CommentCount  int    `json:"comment_count" gorm:"default:0"`
	Rating        *int   `json:"rating,omitempty"`                // 客户评分 1-5
	RatingComment string `json:"rating_comment" gorm:"type:text"` // 评分备注

	// 工作流扩展字段
	IsEscalated bool `json:"is_escalated" gorm:"default:false"` // 是否已升级

	// 关联关系
	Comments []TicketComment `json:"comments,omitempty" gorm:"foreignKey:TicketID"`
	History  []TicketHistory `json:"history,omitempty" gorm:"foreignKey:TicketID"`
}

// TableName 指定表名
func (Ticket) TableName() string {
	return "tickets"
}

// IsOpen 检查工单是否开放
func (t *Ticket) IsOpen() bool {
	return t.Status == TicketStatusOpen
}

// IsInProgress 检查工单是否处理中
func (t *Ticket) IsInProgress() bool {
	return t.Status == TicketStatusInProgress
}

// IsResolved 检查工单是否已解决
func (t *Ticket) IsResolved() bool {
	return t.Status == TicketStatusResolved
}

// IsClosed 检查工单是否已关闭
func (t *Ticket) IsClosed() bool {
	return t.Status == TicketStatusClosed
}

// IsOverdue 检查工单是否逾期
func (t *Ticket) IsOverdue() bool {
	return t.DueDate != nil && t.DueDate.Before(time.Now()) && !t.IsClosed() && !t.IsResolved()
}

// IsSLABreached 检查SLA是否违反
func (t *Ticket) IsSLABreached() bool {
	return t.SLABreached || (t.SLADueDate != nil && t.SLADueDate.Before(time.Now()) && !t.IsClosed() && !t.IsResolved())
}

// CanBeAssigned 检查工单是否可以分配
func (t *Ticket) CanBeAssigned() bool {
	return t.Status == TicketStatusOpen || t.Status == TicketStatusInProgress
}

// CanBeResolved 检查工单是否可以解决
func (t *Ticket) CanBeResolved() bool {
	return t.Status == TicketStatusInProgress || t.Status == TicketStatusPending
}

// CanBeClosed 检查工单是否可以关闭
func (t *Ticket) CanBeClosed() bool {
	return t.Status == TicketStatusResolved
}

// TicketCreateRequest 工单创建请求
type TicketCreateRequest struct {
	Title         string         `json:"title" binding:"required,max=255"`
	Description   string         `json:"description" binding:"required,max=10000"`
	Type          TicketType     `json:"type" binding:"required,oneof=incident request problem change complaint consultation"`
	Priority      TicketPriority `json:"priority" binding:"required,oneof=low normal high urgent critical"`
	Status        *TicketStatus  `json:"status" binding:"omitempty,oneof=open in_progress pending resolved closed cancelled"`
	Source        TicketSource   `json:"source" binding:"required,oneof=web email phone chat api mobile agent"`
	AssignedToID  *uint          `json:"assigned_to_id"`
	CategoryID    *uint          `json:"category_id"`
	SubcategoryID *uint          `json:"subcategory_id"`
	Tags          StringList     `json:"tags"`
	DueDate       *time.Time     `json:"due_date"`
	CustomerEmail string         `json:"customer_email" binding:"omitempty,email"`
	CustomerPhone string         `json:"customer_phone"`
	CustomerName  string         `json:"customer_name"`
	Attachments   []string       `json:"attachments"`
	CustomFields  *JSONMap       `json:"custom_fields"`
	AgentContext  *AgentContext  `json:"agent_context"`
}

// TicketUpdateRequest 工单更新请求
type TicketUpdateRequest struct {
	Title         *string         `json:"title" binding:"omitempty,min=1,max=255"`
	Description   *string         `json:"description" binding:"omitempty,min=1,max=10000"`
	Type          *TicketType     `json:"type" binding:"omitempty,oneof=incident request problem change complaint consultation"`
	Priority      *TicketPriority `json:"priority" binding:"omitempty,oneof=low normal high urgent critical"`
	Status        *TicketStatus   `json:"status" binding:"omitempty,oneof=open in_progress pending resolved closed cancelled"`
	Source        *TicketSource   `json:"source" binding:"omitempty,oneof=web email phone chat api mobile agent"`
	AssignedToID  *uint           `json:"assigned_to_id"`
	CategoryID    *uint           `json:"category_id"`
	SubcategoryID *uint           `json:"subcategory_id"`
	Tags          StringList      `json:"tags"`
	DueDate       *time.Time      `json:"due_date"`
	CustomerEmail *string         `json:"customer_email"`
	CustomerPhone *string         `json:"customer_phone"`
	CustomerName  *string         `json:"customer_name"`
	InternalNotes *string         `json:"internal_notes"`
	Rating        *int            `json:"rating" binding:"omitempty,min=1,max=5"`
	RatingComment *string         `json:"rating_comment"`
	CustomFields  *JSONMap        `json:"custom_fields"`
	AgentContext  *AgentContext   `json:"agent_context"`
}

// TicketResponse 工单响应
type TicketResponse struct {
	ID              uint                   `json:"id"`
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
	TicketNumber    string                 `json:"ticket_number"`
	Title           string                 `json:"title"`
	Description     string                 `json:"description"`
	Type            TicketType             `json:"type"`
	Priority        TicketPriority         `json:"priority"`
	Status          TicketStatus           `json:"status"`
	Source          TicketSource           `json:"source"`
	CreatedByID     uint                   `json:"created_by_id"`
	CreatedBy       *UserSummary           `json:"created_by,omitempty"`
	AssignedToID    *uint                  `json:"assigned_to_id,omitempty"`
	AssignedTo      *UserSummary           `json:"assigned_to,omitempty"`
	CategoryID      *uint                  `json:"category_id,omitempty"`
	Category        *CategoryResponse      `json:"category,omitempty"`
	SubcategoryID   *uint                  `json:"subcategory_id,omitempty"`
	Subcategory     *CategoryResponse      `json:"subcategory,omitempty"`
	Tags            []string               `json:"tags"`
	DueDate         *time.Time             `json:"due_date"`
	ResolvedAt      *time.Time             `json:"resolved_at"`
	ClosedAt        *time.Time             `json:"closed_at"`
	FirstReplyAt    *time.Time             `json:"first_reply_at"`
	SLABreached     bool                   `json:"sla_breached"`
	SLADueDate      *time.Time             `json:"sla_due_date"`
	ResponseTime    *int                   `json:"response_time"`
	ResolutionTime  *int                   `json:"resolution_time"`
	CustomerEmail   string                 `json:"customer_email"`
	CustomerPhone   string                 `json:"customer_phone"`
	CustomerName    string                 `json:"customer_name"`
	Attachments     []string               `json:"attachments"`
	CustomFields    map[string]interface{} `json:"custom_fields"`
	ViewCount       int                    `json:"view_count"`
	CommentCount    int                    `json:"comment_count"`
	Rating          *int                   `json:"rating"`
	RatingComment   string                 `json:"rating_comment"`
	Version         uint64                 `json:"version"`
	AgentContext    AgentContext           `json:"agent_context"`
	TrustLevel      TicketTrustLevel       `json:"trust_level"`
	CreatedByActor  ActorRef               `json:"created_by_actor"`
	AssignedToActor *ActorRef              `json:"assigned_to_actor,omitempty"`

	// 工作流计算字段
	IsOverdue   bool `json:"is_overdue"`   // 是否逾期
	IsEscalated bool `json:"is_escalated"` // 是否已升级
}

// ToResponse 转换为响应格式
func (t *Ticket) ToResponse() *TicketResponse {
	response := &TicketResponse{
		ID:             t.ID,
		CreatedAt:      t.CreatedAt,
		UpdatedAt:      t.UpdatedAt,
		TicketNumber:   t.TicketNumber,
		Title:          t.Title,
		Description:    t.Description,
		Type:           t.Type,
		Priority:       t.Priority,
		Status:         t.Status,
		Source:         t.Source,
		CreatedByID:    t.CreatedByID,
		AssignedToID:   t.AssignedToID,
		CategoryID:     t.CategoryID,
		SubcategoryID:  t.SubcategoryID,
		DueDate:        t.DueDate,
		ResolvedAt:     t.ResolvedAt,
		ClosedAt:       t.ClosedAt,
		FirstReplyAt:   t.FirstReplyAt,
		SLABreached:    t.SLABreached,
		SLADueDate:     t.SLADueDate,
		ResponseTime:   t.ResponseTime,
		ResolutionTime: t.ResolutionTime,
		CustomerEmail:  t.CustomerEmail,
		CustomerPhone:  t.CustomerPhone,
		CustomerName:   t.CustomerName,
		ViewCount:      t.ViewCount,
		CommentCount:   t.CommentCount,
		Rating:         t.Rating,
		RatingComment:  t.RatingComment,
		Version:        t.Version,
		AgentContext:   t.AgentContext.Data(),
		TrustLevel:     t.TrustLevel,

		// 计算字段
		IsOverdue:   t.IsOverdue(),
		IsEscalated: t.IsEscalated,
	}

	if t.CreatedByActorType != "" && t.CreatedByActorID != "" {
		response.CreatedByActor = ActorRef{Type: t.CreatedByActorType, ID: t.CreatedByActorID}
	} else {
		response.CreatedByActor = HumanActor(t.CreatedByID)
	}
	if t.AssignedToActorType != "" && t.AssignedToActorID != "" {
		assignedActor := ActorRef{Type: t.AssignedToActorType, ID: t.AssignedToActorID}
		response.AssignedToActor = &assignedActor
	} else if t.AssignedToID != nil {
		assignedActor := HumanActor(*t.AssignedToID)
		response.AssignedToActor = &assignedActor
	}

	// 处理关联用户
	if t.CreatedBy != nil {
		response.CreatedBy = t.CreatedBy.ToSummary()
	}
	if t.AssignedTo != nil {
		response.AssignedTo = t.AssignedTo.ToSummary()
	}

	// 处理分类
	if t.Category != nil {
		response.Category = t.Category.ToResponse()
	}
	if t.Subcategory != nil {
		response.Subcategory = t.Subcategory.ToResponse()
	}

	// 直接使用JSONB类型字段（无需手动解析）
	response.Tags = t.Tags
	response.Attachments = t.Attachments
	response.CustomFields = t.CustomFields.Data()

	return response
}
