package models

import (
	"time"
)

// TicketTag 工单标签
type TicketTag struct {
	ID        uint       `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" gorm:"index"`

	// 标签信息
	Name        string `json:"name" gorm:"size:50;uniqueIndex;not null"`
	Slug        string `json:"slug" gorm:"size:50;uniqueIndex;not null"`
	Description string `json:"description" gorm:"size:500"`
	Color       string `json:"color" gorm:"size:7;default:'#808080'"` // HEX color
	Icon        string `json:"icon" gorm:"size:50"`                   // Icon class or emoji
	
	// 分类和组织
	Category string `json:"category" gorm:"size:50;index"`         // 标签分类
	Group    string `json:"group" gorm:"size:50;index"`            // 标签组
	ParentID *uint  `json:"parent_id,omitempty" gorm:"index"`     // 父标签ID
	Parent   *TicketTag `json:"parent,omitempty" gorm:"foreignKey:ParentID"`
	Children []TicketTag `json:"children,omitempty" gorm:"foreignKey:ParentID"`
	
	// 使用统计
	UsageCount   int       `json:"usage_count" gorm:"default:0"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
	TrendingScore float64   `json:"trending_score" gorm:"default:0"` // 热度分数
	
	// 权限和可见性
	IsSystem    bool `json:"is_system" gorm:"default:false"`    // 系统标签
	IsPublic    bool `json:"is_public" gorm:"default:true"`     // 公开标签
	IsActive    bool `json:"is_active" gorm:"default:true"`     // 是否激活
	RequireRole string `json:"require_role" gorm:"size:20"`      // 需要的角色
	
	// 自动化
	AutoApply    bool   `json:"auto_apply" gorm:"default:false"`      // 自动应用
	ApplyRules   string `json:"apply_rules" gorm:"type:text"`         // JSON规则
	Keywords     string `json:"keywords" gorm:"type:text"`            // 关键词列表
	
	// 创建者
	CreatedBy uint  `json:"created_by" gorm:"index"`
	Creator   *User `json:"creator,omitempty" gorm:"foreignKey:CreatedBy"`
	
	// 元数据
	Metadata string `json:"metadata" gorm:"type:text"` // JSON object
	SortOrder int   `json:"sort_order" gorm:"default:0"`
}

// TableName 指定表名
func (TicketTag) TableName() string {
	return "ticket_tags"
}

// TicketTagMapping 工单与标签的关联表
type TicketTagMapping struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	
	TicketID uint       `json:"ticket_id" gorm:"not null;index:idx_ticket_tag,unique"`
	Ticket   *Ticket    `json:"ticket,omitempty" gorm:"foreignKey:TicketID"`
	
	TagID uint       `json:"tag_id" gorm:"not null;index:idx_ticket_tag,unique"`
	Tag   *TicketTag `json:"tag,omitempty" gorm:"foreignKey:TagID"`
	
	AddedBy uint  `json:"added_by" gorm:"index"`
	Adder   *User `json:"adder,omitempty" gorm:"foreignKey:AddedBy"`
	
	IsAuto bool   `json:"is_auto" gorm:"default:false"` // 是否自动添加
	Reason string `json:"reason" gorm:"size:255"`       // 添加原因
}

// TableName 指定表名
func (TicketTagMapping) TableName() string {
	return "ticket_tag_mappings"
}