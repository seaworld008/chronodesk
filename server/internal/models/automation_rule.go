package models

import (
	"encoding/json"
	"time"
)

// AutomationRule 自动化规则模型
type AutomationRule struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	// 基本信息
	Name        string `json:"name" gorm:"size:100;not null"`
	Description string `json:"description" gorm:"type:text"`
	RuleType    string `json:"rule_type" gorm:"size:50;not null;index"` // assignment, classification, escalation, sla
	IsActive    bool   `json:"is_active" gorm:"default:true;index"`
	Priority    int    `json:"priority" gorm:"default:1;index"`         // 规则优先级，数字越小优先级越高

	// 触发条件
	TriggerEvent string `json:"trigger_event" gorm:"size:50;not null"` // ticket.created, ticket.updated, ticket.timeout
	Conditions   string `json:"conditions" gorm:"type:json"`           // JSON格式的条件配置

	// 执行动作
	Actions string `json:"actions" gorm:"type:json"` // JSON格式的动作配置

	// 执行统计
	ExecutionCount   int64     `json:"execution_count" gorm:"default:0"`
	LastExecutedAt   *time.Time `json:"last_executed_at,omitempty"`
	SuccessCount     int64     `json:"success_count" gorm:"default:0"`
	FailureCount     int64     `json:"failure_count" gorm:"default:0"`
	AverageExecTime  int64     `json:"average_exec_time" gorm:"default:0"` // 毫秒

	// 创建和更新者
	CreatedBy   uint  `json:"created_by" gorm:"index"`
	CreatedUser *User `json:"created_user,omitempty" gorm:"foreignKey:CreatedBy"`
	UpdatedBy   *uint `json:"updated_by,omitempty" gorm:"index"`
	UpdatedUser *User `json:"updated_user,omitempty" gorm:"foreignKey:UpdatedBy"`
}

// TableName 指定表名
func (AutomationRule) TableName() string {
	return "automation_rules"
}

// RuleCondition 规则条件结构
type RuleCondition struct {
	Field    string      `json:"field"`    // ticket字段名，如title、content、type、priority、status
	Operator string      `json:"operator"` // eq, ne, contains, starts_with, ends_with, in, not_in, gt, lt, gte, lte, regex
	Value    interface{} `json:"value"`    // 比较值
	LogicOp  string      `json:"logic_op"` // and, or (与下一个条件的逻辑关系)
}

// RuleAction 规则动作结构
type RuleAction struct {
	Type   string                 `json:"type"`   // assign, set_priority, set_status, add_comment, notify, escalate
	Params map[string]interface{} `json:"params"` // 动作参数
}

// GetConditions 解析条件JSON
func (ar *AutomationRule) GetConditions() ([]RuleCondition, error) {
	if ar.Conditions == "" {
		return []RuleCondition{}, nil
	}
	
	var conditions []RuleCondition
	err := json.Unmarshal([]byte(ar.Conditions), &conditions)
	return conditions, err
}

// SetConditions 设置条件JSON
func (ar *AutomationRule) SetConditions(conditions []RuleCondition) error {
	data, err := json.Marshal(conditions)
	if err != nil {
		return err
	}
	ar.Conditions = string(data)
	return nil
}

// GetActions 解析动作JSON
func (ar *AutomationRule) GetActions() ([]RuleAction, error) {
	if ar.Actions == "" {
		return []RuleAction{}, nil
	}
	
	var actions []RuleAction
	err := json.Unmarshal([]byte(ar.Actions), &actions)
	return actions, err
}

// SetActions 设置动作JSON
func (ar *AutomationRule) SetActions(actions []RuleAction) error {
	data, err := json.Marshal(actions)
	if err != nil {
		return err
	}
	ar.Actions = string(data)
	return nil
}

// UpdateExecutionStats 更新执行统计
func (ar *AutomationRule) UpdateExecutionStats(success bool, execTime time.Duration) {
	ar.ExecutionCount++
	now := time.Now()
	ar.LastExecutedAt = &now
	
	if success {
		ar.SuccessCount++
	} else {
		ar.FailureCount++
	}
	
	// 计算平均执行时间
	execMs := execTime.Milliseconds()
	if ar.ExecutionCount == 1 {
		ar.AverageExecTime = execMs
	} else {
		ar.AverageExecTime = (ar.AverageExecTime*(ar.ExecutionCount-1) + execMs) / ar.ExecutionCount
	}
}

// SLAConfig SLA配置模型
type SLAConfig struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	// 基本信息
	Name        string `json:"name" gorm:"size:100;not null"`
	Description string `json:"description" gorm:"type:text"`
	IsActive    bool   `json:"is_active" gorm:"default:true;index"`
	IsDefault   bool   `json:"is_default" gorm:"default:false;index"`

	// 适用条件
	TicketType     *string `json:"ticket_type,omitempty" gorm:"size:50"`   // 适用的工单类型
	Priority       *string `json:"priority,omitempty" gorm:"size:20"`      // 适用的优先级
	Category       *string `json:"category,omitempty" gorm:"size:50"`      // 适用的分类
	AssignedUserID *uint   `json:"assigned_user_id,omitempty" gorm:"index"` // 适用的处理人
	
	// SLA时限 (分钟)
	ResponseTime   int `json:"response_time" gorm:"not null"`   // 首次响应时限
	ResolutionTime int `json:"resolution_time" gorm:"not null"` // 解决时限
	
	// 工作时间配置
	WorkingHours    string `json:"working_hours" gorm:"type:json"` // 工作时间配置JSON
	ExcludeWeekends bool   `json:"exclude_weekends" gorm:"default:true"`
	ExcludeHolidays bool   `json:"exclude_holidays" gorm:"default:true"`
	
	// 升级规则
	EscalationRules string `json:"escalation_rules" gorm:"type:json"` // 升级规则JSON

	// 统计信息
	AppliedCount int64 `json:"applied_count" gorm:"default:0"`
	ViolationCount int64 `json:"violation_count" gorm:"default:0"`
	ComplianceRate float64 `json:"compliance_rate" gorm:"default:0"`
}

// TableName 指定表名
func (SLAConfig) TableName() string {
	return "sla_configs"
}

// WorkingHours 工作时间配置
type WorkingHours struct {
	Monday    TimeRange `json:"monday"`
	Tuesday   TimeRange `json:"tuesday"`
	Wednesday TimeRange `json:"wednesday"`
	Thursday  TimeRange `json:"thursday"`
	Friday    TimeRange `json:"friday"`
	Saturday  TimeRange `json:"saturday"`
	Sunday    TimeRange `json:"sunday"`
}

// TimeRange 时间范围
type TimeRange struct {
	Start string `json:"start"` // HH:MM 格式
	End   string `json:"end"`   // HH:MM 格式
}

// EscalationRule 升级规则
type EscalationRule struct {
	TriggerMinutes int    `json:"trigger_minutes"` // 触发升级的分钟数
	Action         string `json:"action"`          // escalate_to_manager, notify_admin, change_priority
	TargetUserID   *uint  `json:"target_user_id,omitempty"`
	NotifyUsers    []uint `json:"notify_users,omitempty"`
}

// GetWorkingHours 获取工作时间配置
func (sla *SLAConfig) GetWorkingHours() (*WorkingHours, error) {
	if sla.WorkingHours == "" {
		return &WorkingHours{
			Monday:    TimeRange{Start: "09:00", End: "18:00"},
			Tuesday:   TimeRange{Start: "09:00", End: "18:00"},
			Wednesday: TimeRange{Start: "09:00", End: "18:00"},
			Thursday:  TimeRange{Start: "09:00", End: "18:00"},
			Friday:    TimeRange{Start: "09:00", End: "18:00"},
			Saturday:  TimeRange{Start: "", End: ""},
			Sunday:    TimeRange{Start: "", End: ""},
		}, nil
	}
	
	var hours WorkingHours
	err := json.Unmarshal([]byte(sla.WorkingHours), &hours)
	return &hours, err
}

// GetEscalationRules 获取升级规则
func (sla *SLAConfig) GetEscalationRules() ([]EscalationRule, error) {
	if sla.EscalationRules == "" {
		return []EscalationRule{}, nil
	}
	
	var rules []EscalationRule
	err := json.Unmarshal([]byte(sla.EscalationRules), &rules)
	return rules, err
}

// TicketTemplate 工单模板模型
type TicketTemplate struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	// 基本信息
	Name        string `json:"name" gorm:"size:100;not null"`
	Description string `json:"description" gorm:"type:text"`
	Category    string `json:"category" gorm:"size:50;index"`
	IsActive    bool   `json:"is_active" gorm:"default:true;index"`

	// 模板内容
	TitleTemplate   string `json:"title_template" gorm:"size:200"`
	ContentTemplate string `json:"content_template" gorm:"type:text"`
	
	// 默认设置
	DefaultType     string  `json:"default_type" gorm:"size:50"`
	DefaultPriority string  `json:"default_priority" gorm:"size:20"`
	DefaultStatus   string  `json:"default_status" gorm:"size:20"`
	AssignToUserID  *uint   `json:"assign_to_user_id,omitempty" gorm:"index"`
	AssignToUser    *User   `json:"assign_to_user,omitempty" gorm:"foreignKey:AssignToUserID"`
	
	// 自定义字段
	CustomFields string `json:"custom_fields" gorm:"type:json"` // 自定义字段配置

	// 使用统计
	UsageCount int64 `json:"usage_count" gorm:"default:0"`
	
	// 创建者
	CreatedBy   uint  `json:"created_by" gorm:"index"`
	CreatedUser *User `json:"created_user,omitempty" gorm:"foreignKey:CreatedBy"`
}

// TableName 指定表名
func (TicketTemplate) TableName() string {
	return "ticket_templates"
}

// CustomField 自定义字段定义
type CustomField struct {
	Name        string      `json:"name"`
	Type        string      `json:"type"`        // text, textarea, select, checkbox, date
	Label       string      `json:"label"`
	Required    bool        `json:"required"`
	DefaultValue interface{} `json:"default_value,omitempty"`
	Options     []string    `json:"options,omitempty"` // for select type
}

// GetCustomFields 获取自定义字段
func (tt *TicketTemplate) GetCustomFields() ([]CustomField, error) {
	if tt.CustomFields == "" {
		return []CustomField{}, nil
	}
	
	var fields []CustomField
	err := json.Unmarshal([]byte(tt.CustomFields), &fields)
	return fields, err
}

// AutomationLog 自动化执行日志
type AutomationLog struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`

	// 关联信息
	RuleID       uint             `json:"rule_id" gorm:"index"`
	Rule         *AutomationRule  `json:"rule,omitempty" gorm:"foreignKey:RuleID"`
	TicketID     uint             `json:"ticket_id" gorm:"index"`
	Ticket       *Ticket          `json:"ticket,omitempty" gorm:"foreignKey:TicketID"`

	// 执行信息
	TriggerEvent string    `json:"trigger_event" gorm:"size:50"`
	ExecutedAt   time.Time `json:"executed_at" gorm:"not null"`
	Success      bool      `json:"success" gorm:"index"`
	ErrorMessage string    `json:"error_message,omitempty" gorm:"type:text"`
	ExecutionTime int64    `json:"execution_time"` // 毫秒
	
	// 执行结果
	ActionsExecuted string `json:"actions_executed" gorm:"type:json"` // 执行的动作列表
	Changes         string `json:"changes" gorm:"type:json"`          // 产生的变更
}

// TableName 指定表名
func (AutomationLog) TableName() string {
	return "automation_logs"
}

// QuickReply 快速回复模板
type QuickReply struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	// 基本信息
	Name        string `json:"name" gorm:"size:100;not null"`
	Category    string `json:"category" gorm:"size:50;index"`
	Content     string `json:"content" gorm:"type:text;not null"`
	Tags        string `json:"tags" gorm:"size:200"`    // 标签，逗号分隔
	IsPublic    bool   `json:"is_public" gorm:"default:false;index"`
	
	// 使用统计
	UsageCount int64 `json:"usage_count" gorm:"default:0"`
	
	// 创建者
	CreatedBy   uint  `json:"created_by" gorm:"index"`
	CreatedUser *User `json:"created_user,omitempty" gorm:"foreignKey:CreatedBy"`
}

// TableName 指定表名
func (QuickReply) TableName() string {
	return "quick_replies"
}

// 请求和响应结构
type AutomationRuleRequest struct {
	Name         string          `json:"name" validate:"required,max=100"`
	Description  string          `json:"description" validate:"max=500"`
	RuleType     string          `json:"rule_type" validate:"required,oneof=assignment classification escalation sla"`
	IsActive     *bool           `json:"is_active,omitempty"`
	Priority     *int            `json:"priority,omitempty" validate:"omitempty,min=1,max=100"`
	TriggerEvent string          `json:"trigger_event" validate:"required"`
	Conditions   []RuleCondition `json:"conditions"`
	Actions      []RuleAction    `json:"actions"`
}

type SLAConfigRequest struct {
	Name            string             `json:"name" validate:"required,max=100"`
	Description     string             `json:"description" validate:"max=500"`
	IsActive        *bool              `json:"is_active,omitempty"`
	IsDefault       *bool              `json:"is_default,omitempty"`
	TicketType      *string            `json:"ticket_type,omitempty"`
	Priority        *string            `json:"priority,omitempty"`
	Category        *string            `json:"category,omitempty"`
	AssignedUserID  *uint              `json:"assigned_user_id,omitempty"`
	ResponseTime    int                `json:"response_time" validate:"required,min=1"`
	ResolutionTime  int                `json:"resolution_time" validate:"required,min=1"`
	WorkingHours    *WorkingHours      `json:"working_hours,omitempty"`
	ExcludeWeekends *bool              `json:"exclude_weekends,omitempty"`
	ExcludeHolidays *bool              `json:"exclude_holidays,omitempty"`
	EscalationRules []EscalationRule   `json:"escalation_rules,omitempty"`
}

type TicketTemplateRequest struct {
	Name            string        `json:"name" validate:"required,max=100"`
	Description     string        `json:"description" validate:"max=500"`
	Category        string        `json:"category" validate:"required,max=50"`
	IsActive        *bool         `json:"is_active,omitempty"`
	TitleTemplate   string        `json:"title_template" validate:"max=200"`
	ContentTemplate string        `json:"content_template"`
	DefaultType     string        `json:"default_type" validate:"max=50"`
	DefaultPriority string        `json:"default_priority" validate:"max=20"`
	DefaultStatus   string        `json:"default_status" validate:"max=20"`
	AssignToUserID  *uint         `json:"assign_to_user_id,omitempty"`
	CustomFields    []CustomField `json:"custom_fields,omitempty"`
}

type QuickReplyRequest struct {
	Name     string `json:"name" validate:"required,max=100"`
	Category string `json:"category" validate:"max=50"`
	Content  string `json:"content" validate:"required"`
	Tags     string `json:"tags" validate:"max=200"`
	IsPublic *bool  `json:"is_public,omitempty"`
}