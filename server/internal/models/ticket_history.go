package models

import (
	"time"
)

// HistoryAction 历史操作类型枚举
type HistoryAction string

const (
	HistoryActionCreate         HistoryAction = "create"          // 创建工单
	HistoryActionUpdate         HistoryAction = "update"          // 更新工单
	HistoryActionStatusChange   HistoryAction = "status_change"   // 状态变更
	HistoryActionPriorityChange HistoryAction = "priority_change" // 优先级变更
	HistoryActionAssign         HistoryAction = "assign"          // 分配工单
	HistoryActionUnassign       HistoryAction = "unassign"        // 取消分配
	HistoryActionComment        HistoryAction = "comment"         // 添加评论
	HistoryActionAttachment     HistoryAction = "attachment"      // 添加附件
	HistoryActionClose          HistoryAction = "close"           // 关闭工单
	HistoryActionReopen         HistoryAction = "reopen"          // 重新打开
	HistoryActionEscalate       HistoryAction = "escalate"        // 升级
	HistoryActionMerge          HistoryAction = "merge"           // 合并
	HistoryActionSplit          HistoryAction = "split"           // 拆分
	HistoryActionTransfer       HistoryAction = "transfer"        // 转移
	HistoryActionResolve        HistoryAction = "resolve"         // 解决
	HistoryActionReject         HistoryAction = "reject"          // 拒绝
	HistoryActionApprove        HistoryAction = "approve"         // 批准
	HistoryActionSystem         HistoryAction = "system"          // 系统操作
)

// TicketHistory 工单历史记录模型
type TicketHistory struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	// 关联信息
	TicketID uint    `json:"ticket_id" gorm:"not null;index"`
	Ticket   *Ticket `json:"ticket,omitempty" gorm:"foreignKey:TicketID"`
	UserID   *uint   `json:"user_id" gorm:"index"` // 可为空，系统操作时为空
	User     *User   `json:"user,omitempty" gorm:"foreignKey:UserID"`

	// 操作信息
	Action      HistoryAction `json:"action" gorm:"size:50;not null;index" validate:"required"`
	Description string        `json:"description" gorm:"type:text;not null" validate:"required"`
	Details     string        `json:"details" gorm:"type:text"` // JSON格式存储详细信息

	// 变更信息
	FieldName string `json:"field_name" gorm:"size:100"` // 变更的字段名
	OldValue  string `json:"old_value" gorm:"type:text"` // 旧值
	NewValue  string `json:"new_value" gorm:"type:text"` // 新值

	// 元数据
	SourceIP  string `json:"source_ip" gorm:"size:45"`
	UserAgent string `json:"user_agent" gorm:"size:500"`
	Metadata  string `json:"metadata" gorm:"type:text"` // JSON格式存储元数据

	// 关联记录
	CommentID    *uint          `json:"comment_id" gorm:"index"` // 关联的评论ID
	Comment      *TicketComment `json:"comment,omitempty" gorm:"foreignKey:CommentID"`
	AttachmentID *uint          `json:"attachment_id" gorm:"index"` // 关联的附件ID

	// 时间信息
	Duration    *int       `json:"duration"`     // 操作持续时间（秒）
	ScheduledAt *time.Time `json:"scheduled_at"` // 计划执行时间
	CompletedAt *time.Time `json:"completed_at"` // 完成时间

	// 状态信息
	IsVisible   bool `json:"is_visible" gorm:"default:true"`    // 是否对用户可见
	IsSystem    bool `json:"is_system" gorm:"default:false"`    // 是否为系统操作
	IsAutomated bool `json:"is_automated" gorm:"default:false"` // 是否为自动化操作
	IsImportant bool `json:"is_important" gorm:"default:false"` // 是否为重要操作
}

// TableName 指定表名
func (TicketHistory) TableName() string {
	return "ticket_histories"
}

// IsUserAction 检查是否为用户操作
func (th *TicketHistory) IsUserAction() bool {
	return th.UserID != nil && !th.IsSystem
}

// IsSystemAction 检查是否为系统操作
func (th *TicketHistory) IsSystemAction() bool {
	return th.IsSystem || th.UserID == nil
}

// IsStatusChange 检查是否为状态变更
func (th *TicketHistory) IsStatusChange() bool {
	return th.Action == HistoryActionStatusChange
}

// IsPriorityChange 检查是否为优先级变更
func (th *TicketHistory) IsPriorityChange() bool {
	return th.Action == HistoryActionPriorityChange
}

// IsAssignmentChange 检查是否为分配变更
func (th *TicketHistory) IsAssignmentChange() bool {
	return th.Action == HistoryActionAssign || th.Action == HistoryActionUnassign
}

// HasFieldChange 检查是否有字段变更
func (th *TicketHistory) HasFieldChange() bool {
	return th.FieldName != "" && (th.OldValue != "" || th.NewValue != "")
}

// GetDurationString 获取持续时间字符串
func (th *TicketHistory) GetDurationString() string {
	if th.Duration == nil {
		return ""
	}
	duration := time.Duration(*th.Duration) * time.Second
	return duration.String()
}

// TicketHistoryCreateRequest 历史记录创建请求
type TicketHistoryCreateRequest struct {
	TicketID     uint                   `json:"ticket_id" validate:"required"`
	Action       HistoryAction          `json:"action" validate:"required"`
	Description  string                 `json:"description" validate:"required"`
	Details      map[string]interface{} `json:"details"`
	FieldName    string                 `json:"field_name"`
	OldValue     string                 `json:"old_value"`
	NewValue     string                 `json:"new_value"`
	CommentID    *uint                  `json:"comment_id"`
	AttachmentID *uint                  `json:"attachment_id"`
	IsVisible    *bool                  `json:"is_visible"`
	IsImportant  *bool                  `json:"is_important"`
	Metadata     map[string]interface{} `json:"metadata"`
}

// TicketHistoryResponse 历史记录响应
type TicketHistoryResponse struct {
	ID           uint                   `json:"id"`
	CreatedAt    time.Time              `json:"created_at"`
	TicketID     uint                   `json:"ticket_id"`
	User         *UserResponse          `json:"user,omitempty"`
	Action       HistoryAction          `json:"action"`
	Description  string                 `json:"description"`
	Details      map[string]interface{} `json:"details"`
	FieldName    string                 `json:"field_name"`
	OldValue     string                 `json:"old_value"`
	NewValue     string                 `json:"new_value"`
	CommentID    *uint                  `json:"comment_id"`
	AttachmentID *uint                  `json:"attachment_id"`
	Duration     *int                   `json:"duration"`
	ScheduledAt  *time.Time             `json:"scheduled_at"`
	CompletedAt  *time.Time             `json:"completed_at"`
	IsVisible    bool                   `json:"is_visible"`
	IsSystem     bool                   `json:"is_system"`
	IsAutomated  bool                   `json:"is_automated"`
	IsImportant  bool                   `json:"is_important"`
	Metadata     map[string]interface{} `json:"metadata"`
}

// ToResponse 转换为响应格式
func (th *TicketHistory) ToResponse() *TicketHistoryResponse {
	response := &TicketHistoryResponse{
		ID:           th.ID,
		CreatedAt:    th.CreatedAt,
		TicketID:     th.TicketID,
		Action:       th.Action,
		Description:  th.Description,
		FieldName:    th.FieldName,
		OldValue:     th.OldValue,
		NewValue:     th.NewValue,
		CommentID:    th.CommentID,
		AttachmentID: th.AttachmentID,
		Duration:     th.Duration,
		ScheduledAt:  th.ScheduledAt,
		CompletedAt:  th.CompletedAt,
		IsVisible:    th.IsVisible,
		IsSystem:     th.IsSystem,
		IsAutomated:  th.IsAutomated,
		IsImportant:  th.IsImportant,
	}

	// 处理关联用户
	if th.User != nil {
		response.User = th.User.ToResponse()
	}

	// TODO: 解析JSON字段
	// response.Details = parseDetailsFromJSON(th.Details)
	// response.Metadata = parseMetadataFromJSON(th.Metadata)

	return response
}

// HistoryFilter 历史记录过滤器
type HistoryFilter struct {
	TicketID    *uint           `json:"ticket_id"`
	UserID      *uint           `json:"user_id"`
	Actions     []HistoryAction `json:"actions"`
	FieldName   string          `json:"field_name"`
	IsVisible   *bool           `json:"is_visible"`
	IsSystem    *bool           `json:"is_system"`
	IsAutomated *bool           `json:"is_automated"`
	IsImportant *bool           `json:"is_important"`
	DateFrom    *time.Time      `json:"date_from"`
	DateTo      *time.Time      `json:"date_to"`
	Limit       int             `json:"limit"`
	Offset      int             `json:"offset"`
	OrderBy     string          `json:"order_by"`  // created_at, action, user_id
	OrderDir    string          `json:"order_dir"` // asc, desc
}
