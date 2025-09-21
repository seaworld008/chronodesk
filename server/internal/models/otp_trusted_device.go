package models

import "time"

// OTPTrustedDevice 记住设备记录
type OTPTrustedDevice struct {
	ID              uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt       time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt       time.Time `json:"updated_at" gorm:"autoUpdateTime"`
	UserID          uint      `json:"user_id" gorm:"index;not null"`
	DeviceTokenHash string    `json:"-" gorm:"size:128;uniqueIndex;not null"`
	DeviceName      string    `json:"device_name" gorm:"size:100"`
	LastUsedAt      time.Time `json:"last_used_at"`
	LastIP          string    `json:"last_ip" gorm:"size:64"`
	UserAgent       string    `json:"user_agent" gorm:"size:255"`
	ExpiresAt       time.Time `json:"expires_at" gorm:"index;not null"`
	Revoked         bool      `json:"revoked" gorm:"default:false"`
}

// TableName 指定表名
func (OTPTrustedDevice) TableName() string {
	return "otp_trusted_devices"
}
