package models

import (
	"time"
)

// CategoryStatus 分类状态枚举
type CategoryStatus string

const (
	CategoryStatusActive   CategoryStatus = "active"   // 激活
	CategoryStatusInactive CategoryStatus = "inactive" // 停用
	CategoryStatusArchived CategoryStatus = "archived" // 归档
)

// CategoryType 分类类型枚举
type CategoryType string

const (
	CategoryTypeGeneral   CategoryType = "general"   // 通用分类
	CategoryTypeTechnical CategoryType = "technical" // 技术分类
	CategoryTypeBusiness  CategoryType = "business"  // 业务分类
	CategoryTypeSupport   CategoryType = "support"   // 支持分类
	CategoryTypeIncident  CategoryType = "incident"  // 事件分类
	CategoryTypeRequest   CategoryType = "request"   // 请求分类
	CategoryTypeBilling   CategoryType = "billing"   // 账单分类
	CategoryTypeComplaint CategoryType = "complaint" // 投诉分类
)

// Category 分类模型
type Category struct {
	ID        uint       `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" gorm:"index"`

	// 基本信息
	Name        string `json:"name" gorm:"size:100;not null;uniqueIndex" validate:"required,max=100"`
	Slug        string `json:"slug" gorm:"size:100;not null;uniqueIndex" validate:"required,max=100"`
	Description string `json:"description" gorm:"type:text"`
	Icon        string `json:"icon" gorm:"size:50"`  // 图标名称
	Color       string `json:"color" gorm:"size:20"` // 颜色代码

	// 分类属性
	Type      CategoryType   `json:"type" gorm:"size:20;not null;default:'general'" validate:"required,oneof=general technical business support incident request"`
	Status    CategoryStatus `json:"status" gorm:"size:20;not null;default:'active'" validate:"required,oneof=active inactive archived"`
	SortOrder int            `json:"sort_order" gorm:"default:0;index"` // 排序顺序

	// 层级结构
	ParentID *uint      `json:"parent_id" gorm:"index"`
	Parent   *Category  `json:"parent,omitempty" gorm:"foreignKey:ParentID"`
	Children []Category `json:"children,omitempty" gorm:"foreignKey:ParentID"`
	Level    int        `json:"level" gorm:"default:0"` // 层级深度
	Path     string     `json:"path" gorm:"size:500"`   // 层级路径，如 "/1/2/3"

	// 统计信息
	TicketCount       int `json:"ticket_count" gorm:"default:0"`
	ActiveTicketCount int `json:"active_ticket_count" gorm:"default:0"`
	ChildrenCount     int `json:"children_count" gorm:"default:0"`

	// 配置信息
	IsDefault        bool   `json:"is_default" gorm:"default:false"`       // 是否为默认分类
	IsPublic         bool   `json:"is_public" gorm:"default:true"`         // 是否公开可见
	RequireApproval  bool   `json:"require_approval" gorm:"default:false"` // 是否需要审批
	AutoAssignUserID *uint  `json:"auto_assign_user_id" gorm:"index"`      // 自动分配的用户ID
	AutoAssignUser   *User  `json:"auto_assign_user,omitempty" gorm:"foreignKey:AutoAssignUserID"`
	SLAHours         *int   `json:"sla_hours"`                 // SLA时间（小时）
	Template         string `json:"template" gorm:"type:text"` // 工单模板

	// 权限控制
	AllowedRoles    string `json:"allowed_roles" gorm:"type:text"`    // JSON格式存储允许的角色
	RestrictedRoles string `json:"restricted_roles" gorm:"type:text"` // JSON格式存储限制的角色

	// 元数据
	Metadata string `json:"metadata" gorm:"type:text"` // JSON格式存储元数据
	Tags     string `json:"tags" gorm:"type:text"`     // JSON格式存储标签

	// 创建者信息
	CreatedBy uint  `json:"created_by" gorm:"not null;index"`
	Creator   *User `json:"creator,omitempty" gorm:"foreignKey:CreatedBy"`
	UpdatedBy *uint `json:"updated_by" gorm:"index"`
	Updater   *User `json:"updater,omitempty" gorm:"foreignKey:UpdatedBy"`

	// 关联工单
	Tickets []Ticket `json:"tickets,omitempty" gorm:"foreignKey:CategoryID"`
}

// TableName 指定表名
func (Category) TableName() string {
	return "categories"
}

// IsActive 检查分类是否激活
func (c *Category) IsActive() bool {
	return c.Status == CategoryStatusActive
}

// IsInactive 检查分类是否停用
func (c *Category) IsInactive() bool {
	return c.Status == CategoryStatusInactive
}

// IsArchived 检查分类是否归档
func (c *Category) IsArchived() bool {
	return c.Status == CategoryStatusArchived
}

// IsRootCategory 检查是否为根分类
func (c *Category) IsRootCategory() bool {
	return c.ParentID == nil
}

// HasChildren 检查是否有子分类
func (c *Category) HasChildren() bool {
	return c.ChildrenCount > 0
}

// HasTickets 检查是否有工单
func (c *Category) HasTickets() bool {
	return c.TicketCount > 0
}

// CanBeDeleted 检查是否可以删除
func (c *Category) CanBeDeleted() bool {
	return !c.IsDefault && c.TicketCount == 0 && c.ChildrenCount == 0
}

// GetFullName 获取完整名称（包含父级）
func (c *Category) GetFullName() string {
	if c.Parent != nil {
		return c.Parent.GetFullName() + " > " + c.Name
	}
	return c.Name
}

// CategoryCreateRequest 分类创建请求
type CategoryCreateRequest struct {
	Name             string                 `json:"name" validate:"required,max=100"`
	Slug             string                 `json:"slug" validate:"required,max=100"`
	Description      string                 `json:"description"`
	Icon             string                 `json:"icon"`
	Color            string                 `json:"color"`
	Type             CategoryType           `json:"type" validate:"required,oneof=general technical business support incident request"`
	Status           CategoryStatus         `json:"status" validate:"omitempty,oneof=active inactive archived"`
	ParentID         *uint                  `json:"parent_id"`
	SortOrder        int                    `json:"sort_order"`
	IsPublic         *bool                  `json:"is_public"`
	RequireApproval  *bool                  `json:"require_approval"`
	AutoAssignUserID *uint                  `json:"auto_assign_user_id"`
	SLAHours         *int                   `json:"sla_hours" validate:"omitempty,min=1"`
	Template         string                 `json:"template"`
	AllowedRoles     []string               `json:"allowed_roles"`
	RestrictedRoles  []string               `json:"restricted_roles"`
	Tags             []string               `json:"tags"`
	Metadata         map[string]interface{} `json:"metadata"`
}

// CategoryUpdateRequest 分类更新请求
type CategoryUpdateRequest struct {
	Name             *string                `json:"name" validate:"omitempty,max=100"`
	Slug             *string                `json:"slug" validate:"omitempty,max=100"`
	Description      *string                `json:"description"`
	Icon             *string                `json:"icon"`
	Color            *string                `json:"color"`
	Type             *CategoryType          `json:"type" validate:"omitempty,oneof=general technical business support incident request"`
	Status           *CategoryStatus        `json:"status" validate:"omitempty,oneof=active inactive archived"`
	ParentID         *uint                  `json:"parent_id"`
	SortOrder        *int                   `json:"sort_order"`
	IsPublic         *bool                  `json:"is_public"`
	RequireApproval  *bool                  `json:"require_approval"`
	AutoAssignUserID *uint                  `json:"auto_assign_user_id"`
	SLAHours         *int                   `json:"sla_hours" validate:"omitempty,min=1"`
	Template         *string                `json:"template"`
	AllowedRoles     []string               `json:"allowed_roles"`
	RestrictedRoles  []string               `json:"restricted_roles"`
	Tags             []string               `json:"tags"`
	Metadata         map[string]interface{} `json:"metadata"`
}

// CategoryResponse 分类响应
type CategoryResponse struct {
	ID                uint                   `json:"id"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
	Name              string                 `json:"name"`
	Slug              string                 `json:"slug"`
	Description       string                 `json:"description"`
	Icon              string                 `json:"icon"`
	Color             string                 `json:"color"`
	Type              CategoryType           `json:"type"`
	Status            CategoryStatus         `json:"status"`
	SortOrder         int                    `json:"sort_order"`
	ParentID          *uint                  `json:"parent_id"`
	Parent            *CategoryResponse      `json:"parent,omitempty"`
	Children          []CategoryResponse     `json:"children,omitempty"`
	Level             int                    `json:"level"`
	Path              string                 `json:"path"`
	TicketCount       int                    `json:"ticket_count"`
	ActiveTicketCount int                    `json:"active_ticket_count"`
	ChildrenCount     int                    `json:"children_count"`
	IsDefault         bool                   `json:"is_default"`
	IsPublic          bool                   `json:"is_public"`
	RequireApproval   bool                   `json:"require_approval"`
	AutoAssignUser    *UserResponse          `json:"auto_assign_user,omitempty"`
	SLAHours          *int                   `json:"sla_hours"`
	Template          string                 `json:"template"`
	AllowedRoles      []string               `json:"allowed_roles"`
	RestrictedRoles   []string               `json:"restricted_roles"`
	Tags              []string               `json:"tags"`
	Metadata          map[string]interface{} `json:"metadata"`
	Creator           *UserResponse          `json:"creator,omitempty"`
	Updater           *UserResponse          `json:"updater,omitempty"`
}

// ToResponse 转换为响应格式
func (c *Category) ToResponse() *CategoryResponse {
	response := &CategoryResponse{
		ID:                c.ID,
		CreatedAt:         c.CreatedAt,
		UpdatedAt:         c.UpdatedAt,
		Name:              c.Name,
		Slug:              c.Slug,
		Description:       c.Description,
		Icon:              c.Icon,
		Color:             c.Color,
		Type:              c.Type,
		Status:            c.Status,
		SortOrder:         c.SortOrder,
		ParentID:          c.ParentID,
		Level:             c.Level,
		Path:              c.Path,
		TicketCount:       c.TicketCount,
		ActiveTicketCount: c.ActiveTicketCount,
		ChildrenCount:     c.ChildrenCount,
		IsDefault:         c.IsDefault,
		IsPublic:          c.IsPublic,
		RequireApproval:   c.RequireApproval,
		SLAHours:          c.SLAHours,
		Template:          c.Template,
	}

	// 处理关联用户
	if c.AutoAssignUser != nil {
		response.AutoAssignUser = c.AutoAssignUser.ToResponse()
	}
	if c.Creator != nil {
		response.Creator = c.Creator.ToResponse()
	}
	if c.Updater != nil {
		response.Updater = c.Updater.ToResponse()
	}

	// 处理父级分类
	if c.Parent != nil {
		response.Parent = c.Parent.ToResponse()
	}

	// 处理子分类
	if len(c.Children) > 0 {
		response.Children = make([]CategoryResponse, len(c.Children))
		for i, child := range c.Children {
			response.Children[i] = *child.ToResponse()
		}
	}

	// TODO: 解析JSON字段
	// response.AllowedRoles = parseRolesFromJSON(c.AllowedRoles)
	// response.RestrictedRoles = parseRolesFromJSON(c.RestrictedRoles)
	// response.Tags = parseTagsFromJSON(c.Tags)
	// response.Metadata = parseMetadataFromJSON(c.Metadata)

	return response
}
