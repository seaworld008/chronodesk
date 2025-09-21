package models

import (
	"time"
)

// PasswordReset 密码重置记录
type PasswordReset struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	// 关联用户
	UserID uint  `json:"user_id" gorm:"index;not null"`
	User   *User `json:"user,omitempty" gorm:"foreignKey:UserID"`

	// 重置信息
	Email     string    `json:"email" gorm:"size:100;not null;index"`
	Token     string    `json:"token" gorm:"size:255;uniqueIndex;not null"`
	Code      string    `json:"code" gorm:"size:10"`       // 验证码（可选）
	ExpiresAt time.Time `json:"expires_at" gorm:"not null;index"`

	// 状态
	Used     bool       `json:"used" gorm:"default:false"`
	UsedAt   *time.Time `json:"used_at,omitempty"`
	Revoked  bool       `json:"revoked" gorm:"default:false"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	
	// 请求信息
	IPAddress   string `json:"ip_address" gorm:"size:45"`
	UserAgent   string `json:"user_agent" gorm:"size:500"`
	RequestedBy string `json:"requested_by" gorm:"size:100"` // email or username
	
	// 重试信息
	AttemptCount int        `json:"attempt_count" gorm:"default:0"`
	LastAttempt  *time.Time `json:"last_attempt,omitempty"`
	
	// 安全信息
	Method        string `json:"method" gorm:"size:20;default:'email'"` // email, sms, admin
	VerifiedInfo  string `json:"verified_info" gorm:"size:500"`         // 验证的信息（如手机号、邮箱等）
	SecurityToken string `json:"security_token" gorm:"size:255"`        // 额外的安全令牌
	
	// 元数据
	Metadata string `json:"metadata" gorm:"type:text"` // JSON object
}

// TableName 指定表名
func (PasswordReset) TableName() string {
	return "password_resets"
}

// IsExpired 检查是否过期
func (p *PasswordReset) IsExpired() bool {
	return time.Now().After(p.ExpiresAt)
}

// IsValid 检查是否有效
func (p *PasswordReset) IsValid() bool {
	return !p.Used && !p.Revoked && !p.IsExpired()
}