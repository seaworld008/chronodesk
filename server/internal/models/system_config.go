package models

import (
	"encoding/json"
	"time"
)

// SystemConfig 系统配置模型
type SystemConfig struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	// 配置键值
	Key         string `json:"key" gorm:"uniqueIndex;size:100;not null"`
	Value       string `json:"value" gorm:"type:text"`
	ValueType   string `json:"value_type" gorm:"size:20;not null;default:'string'"` // string, int, bool, json
	Description string `json:"description" gorm:"size:500"`

	// 配置分组和分类
	Category   string `json:"category" gorm:"size:50;index"` // system, cleanup, security, notification
	Group      string `json:"group" gorm:"size:50;index"`    // 更细的分组
	IsRequired bool   `json:"is_required" gorm:"default:false"`
	IsActive   bool   `json:"is_active" gorm:"default:true"`

	// 配置元数据
	DefaultValue string `json:"default_value" gorm:"type:text"`
	MinValue     *int   `json:"min_value,omitempty"`
	MaxValue     *int   `json:"max_value,omitempty"`
	ValidValues  string `json:"valid_values,omitempty" gorm:"type:text"` // JSON数组格式的有效值列表

	// 更新信息
	UpdatedBy   *uint  `json:"updated_by,omitempty" gorm:"index"`
	UpdatedUser *User  `json:"updated_user,omitempty" gorm:"foreignKey:UpdatedBy"`
	Version     int    `json:"version" gorm:"default:1"`
}

// TableName 指定表名
func (SystemConfig) TableName() string {
	return "system_configs"
}

// GetStringValue 获取字符串值
func (sc *SystemConfig) GetStringValue() string {
	return sc.Value
}

// GetIntValue 获取整数值
func (sc *SystemConfig) GetIntValue() int {
	if sc.ValueType != "int" {
		return 0
	}
	var value int
	if err := json.Unmarshal([]byte(sc.Value), &value); err != nil {
		return 0
	}
	return value
}

// GetBoolValue 获取布尔值
func (sc *SystemConfig) GetBoolValue() bool {
	if sc.ValueType != "bool" {
		return false
	}
	var value bool
	if err := json.Unmarshal([]byte(sc.Value), &value); err != nil {
		return false
	}
	return value
}

// GetJSONValue 获取JSON值并解析到指定结构
func (sc *SystemConfig) GetJSONValue(v interface{}) error {
	if sc.ValueType != "json" {
		return nil
	}
	return json.Unmarshal([]byte(sc.Value), v)
}

// SetValue 设置值
func (sc *SystemConfig) SetValue(value interface{}) error {
	switch v := value.(type) {
	case string:
		sc.Value = v
		sc.ValueType = "string"
	case int:
		jsonBytes, err := json.Marshal(v)
		if err != nil {
			return err
		}
		sc.Value = string(jsonBytes)
		sc.ValueType = "int"
	case bool:
		jsonBytes, err := json.Marshal(v)
		if err != nil {
			return err
		}
		sc.Value = string(jsonBytes)
		sc.ValueType = "bool"
	default:
		jsonBytes, err := json.Marshal(v)
		if err != nil {
			return err
		}
		sc.Value = string(jsonBytes)
		sc.ValueType = "json"
	}
	return nil
}

// CleanupConfig 清理配置结构
type CleanupConfig struct {
	LoginHistoryRetentionDays int  `json:"login_history_retention_days"` // 登录历史保留天数
	CleanupEnabled            bool `json:"cleanup_enabled"`              // 是否启用自动清理
	CleanupSchedule           string `json:"cleanup_schedule"`            // 清理计划（cron格式）
	MaxRecordsPerCleanup      int  `json:"max_records_per_cleanup"`      // 每次清理的最大记录数
}

// GetDefaultCleanupConfig 获取默认清理配置
func GetDefaultCleanupConfig() *CleanupConfig {
	return &CleanupConfig{
		LoginHistoryRetentionDays: 30, // 默认保留30天
		CleanupEnabled:            true,
		CleanupSchedule:           "0 2 * * *", // 每天凌晨2点执行
		MaxRecordsPerCleanup:      1000,
	}
}

// SystemConfigRequest 系统配置请求
type SystemConfigRequest struct {
	Key         string      `json:"key" validate:"required,max=100"`
	Value       interface{} `json:"value" validate:"required"`
	Description string      `json:"description" validate:"max=500"`
	Category    string      `json:"category" validate:"required,max=50"`
	Group       string      `json:"group" validate:"max=50"`
	IsRequired  *bool       `json:"is_required,omitempty"`
	IsActive    *bool       `json:"is_active,omitempty"`
}

// SystemConfigResponse 系统配置响应
type SystemConfigResponse struct {
	ID          uint        `json:"id"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
	Key         string      `json:"key"`
	Value       interface{} `json:"value"`
	ValueType   string      `json:"value_type"`
	Description string      `json:"description"`
	Category    string      `json:"category"`
	Group       string      `json:"group"`
	IsRequired  bool        `json:"is_required"`
	IsActive    bool        `json:"is_active"`
	Version     int         `json:"version"`
}

// ToResponse 转换为响应格式
func (sc *SystemConfig) ToResponse() *SystemConfigResponse {
	var value interface{}
	
	switch sc.ValueType {
	case "int":
		value = sc.GetIntValue()
	case "bool":
		value = sc.GetBoolValue()
	case "json":
		var jsonValue interface{}
		if err := sc.GetJSONValue(&jsonValue); err == nil {
			value = jsonValue
		} else {
			value = sc.Value
		}
	default:
		value = sc.GetStringValue()
	}

	return &SystemConfigResponse{
		ID:          sc.ID,
		CreatedAt:   sc.CreatedAt,
		UpdatedAt:   sc.UpdatedAt,
		Key:         sc.Key,
		Value:       value,
		ValueType:   sc.ValueType,
		Description: sc.Description,
		Category:    sc.Category,
		Group:       sc.Group,
		IsRequired:  sc.IsRequired,
		IsActive:    sc.IsActive,
		Version:     sc.Version,
	}
}

// CleanupLog 清理日志模型
type CleanupLog struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`

	// 清理任务信息
	TaskType     string `json:"task_type" gorm:"size:50;not null;index"` // login_history, audit_logs, etc.
	Status       string `json:"status" gorm:"size:20;not null"`          // started, completed, failed
	StartTime    time.Time `json:"start_time" gorm:"not null"`
	EndTime      *time.Time `json:"end_time,omitempty"`
	Duration     *int64     `json:"duration,omitempty"` // 持续时间（毫秒）

	// 清理结果
	RecordsProcessed int    `json:"records_processed" gorm:"default:0"`
	RecordsDeleted   int    `json:"records_deleted" gorm:"default:0"`
	ErrorMessage     string `json:"error_message,omitempty" gorm:"type:text"`

	// 清理条件
	RetentionDays int        `json:"retention_days"`
	CutoffDate    time.Time  `json:"cutoff_date"`
	
	// 触发方式
	TriggerType string `json:"trigger_type" gorm:"size:20"` // manual, scheduled
	TriggerBy   *uint  `json:"trigger_by,omitempty" gorm:"index"`
	TriggerUser *User  `json:"trigger_user,omitempty" gorm:"foreignKey:TriggerBy"`
}

// TableName 指定表名
func (CleanupLog) TableName() string {
	return "cleanup_logs"
}

// GetDurationString 获取持续时间的字符串表示
func (cl *CleanupLog) GetDurationString() string {
	if cl.Duration == nil {
		return "未知"
	}
	
	duration := time.Duration(*cl.Duration) * time.Millisecond
	if duration < time.Second {
		return "< 1秒"
	} else if duration < time.Minute {
		return duration.Round(time.Second).String()
	} else {
		return duration.Round(time.Minute).String()
	}
}

// IsCompleted 检查任务是否已完成
func (cl *CleanupLog) IsCompleted() bool {
	return cl.Status == "completed" || cl.Status == "failed"
}

// CleanupLogResponse 清理日志响应
type CleanupLogResponse struct {
	ID               uint       `json:"id"`
	CreatedAt        time.Time  `json:"created_at"`
	TaskType         string     `json:"task_type"`
	Status           string     `json:"status"`
	StartTime        time.Time  `json:"start_time"`
	EndTime          *time.Time `json:"end_time,omitempty"`
	Duration         string     `json:"duration"`
	RecordsProcessed int        `json:"records_processed"`
	RecordsDeleted   int        `json:"records_deleted"`
	ErrorMessage     string     `json:"error_message,omitempty"`
	RetentionDays    int        `json:"retention_days"`
	CutoffDate       time.Time  `json:"cutoff_date"`
	TriggerType      string     `json:"trigger_type"`
	TriggerBy        *uint      `json:"trigger_by,omitempty"`
}

// ToResponse 转换为响应格式
func (cl *CleanupLog) ToResponse() *CleanupLogResponse {
	return &CleanupLogResponse{
		ID:               cl.ID,
		CreatedAt:        cl.CreatedAt,
		TaskType:         cl.TaskType,
		Status:           cl.Status,
		StartTime:        cl.StartTime,
		EndTime:          cl.EndTime,
		Duration:         cl.GetDurationString(),
		RecordsProcessed: cl.RecordsProcessed,
		RecordsDeleted:   cl.RecordsDeleted,
		ErrorMessage:     cl.ErrorMessage,
		RetentionDays:    cl.RetentionDays,
		CutoffDate:       cl.CutoffDate,
		TriggerType:      cl.TriggerType,
		TriggerBy:        cl.TriggerBy,
	}
}