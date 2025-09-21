package models

import (
	"time"
)

// EmailVerification 邮箱验证记录
type EmailVerification struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	// 关联用户
	UserID uint  `json:"user_id" gorm:"index;not null"`
	User   *User `json:"user,omitempty" gorm:"foreignKey:UserID"`

	// 验证信息
	Email    string    `json:"email" gorm:"size:100;not null;index"`
	Token    string    `json:"token" gorm:"size:255;uniqueIndex;not null"`
	Type     string    `json:"type" gorm:"size:20;not null;default:'email_verification'"` // email_verification, email_change
	NewEmail string    `json:"new_email" gorm:"size:100"`                                 // 用于邮箱变更
	Code     string    `json:"code" gorm:"size:10"`                                       // 验证码（可选）
	
	// 状态
	Verified   bool       `json:"verified" gorm:"default:false"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	ExpiresAt  time.Time  `json:"expires_at" gorm:"not null;index"`
	
	// 请求信息
	IPAddress string `json:"ip_address" gorm:"size:45"`
	UserAgent string `json:"user_agent" gorm:"size:500"`
	
	// 重试信息
	AttemptCount int       `json:"attempt_count" gorm:"default:0"`
	LastAttempt  *time.Time `json:"last_attempt,omitempty"`
	
	// 元数据
	Metadata string `json:"metadata" gorm:"type:text"` // JSON object
}

// TableName 指定表名
func (EmailVerification) TableName() string {
	return "email_verifications"
}

// IsExpired 检查是否过期
func (e *EmailVerification) IsExpired() bool {
	return time.Now().After(e.ExpiresAt)
}

// IsValid 检查是否有效
func (e *EmailVerification) IsValid() bool {
	return !e.Verified && !e.IsExpired()
}