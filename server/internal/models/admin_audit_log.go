package models

import "time"

// AdminAuditLog 管理员操作审计日志
type AdminAuditLog struct {
	ID           uint         `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt    time.Time    `json:"created_at" gorm:"autoCreateTime"`
	UserID       *uint        `json:"user_id" gorm:"index"`
	Username     string       `json:"username" gorm:"size:100"`
	PlatformRole PlatformRole `json:"platform_role" gorm:"column:platform_role;size:30;not null;default:'member';index;check:chk_admin_audit_logs_platform_role,platform_role IN ('platform_admin','security_auditor','emergency_operator','member')"`
	Action       string       `json:"action" gorm:"size:255"`
	Method       string       `json:"method" gorm:"size:20"`
	Path         string       `json:"path" gorm:"size:255;index"`
	StatusCode   int          `json:"status_code"`
	ClientIP     string       `json:"client_ip" gorm:"size:64"`
	UserAgent    string       `json:"user_agent" gorm:"size:255"`
	Query        string       `json:"query" gorm:"size:500"`
	LatencyMs    int64        `json:"latency_ms"`
	Result       string       `json:"result" gorm:"size:100"`
	Notes        string       `json:"notes" gorm:"type:text"`
}

// TableName 指定表名
func (AdminAuditLog) TableName() string {
	return "admin_audit_logs"
}
