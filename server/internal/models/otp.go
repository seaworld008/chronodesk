package models

import (
	"time"
)

// OTPType OTP类型枚举
type OTPType string

const (
	OTPTypeLogin             OTPType = "login"              // 登录验证
	OTPTypeRegister          OTPType = "register"           // 注册验证
	OTPTypePasswordReset     OTPType = "password_reset"     // 密码重置
	OTPTypeEmailVerification OTPType = "email_verification" // 邮箱验证
	OTPTypePhoneVerification OTPType = "phone_verification" // 手机验证
	OTPTypeTwoFactor         OTPType = "two_factor"         // 双因子认证
	OTPTypeAccountRecovery   OTPType = "account_recovery"   // 账户恢复
	OTPTypeSecurityCheck     OTPType = "security_check"     // 安全检查
)

// OTPStatus OTP状态枚举
type OTPStatus string

const (
	OTPStatusPending OTPStatus = "pending" // 待验证
	OTPStatusUsed    OTPStatus = "used"    // 已使用
	OTPStatusExpired OTPStatus = "expired" // 已过期
	OTPStatusRevoked OTPStatus = "revoked" // 已撤销
	OTPStatusFailed  OTPStatus = "failed"  // 验证失败
)

// OTPDeliveryMethod 发送方式枚举
type OTPDeliveryMethod string

const (
	OTPDeliveryEmail OTPDeliveryMethod = "email" // 邮件发送
	OTPDeliverySMS   OTPDeliveryMethod = "sms"   // 短信发送
	OTPDeliveryApp   OTPDeliveryMethod = "app"   // 应用内发送
	OTPDeliveryVoice OTPDeliveryMethod = "voice" // 语音发送
)

// OTPCode OTP验证码模型
type OTPCode struct {
	ID        uint       `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" gorm:"index"`

	// 关联用户
	UserID uint  `json:"user_id" gorm:"not null;index"`
	User   *User `json:"user,omitempty" gorm:"foreignKey:UserID"`

	// OTP信息
	Code      string    `json:"code" gorm:"size:20;not null;index" validate:"required"`
	Type      OTPType   `json:"type" gorm:"size:30;not null;index" validate:"required"`
	Status    OTPStatus `json:"status" gorm:"size:20;not null;default:'pending'" validate:"required"`
	ExpiresAt time.Time `json:"expires_at" gorm:"not null;index"`

	// 发送信息
	DeliveryMethod OTPDeliveryMethod `json:"delivery_method" gorm:"size:20;not null" validate:"required"`
	Recipient      string            `json:"recipient" gorm:"size:255;not null" validate:"required"` // 接收者（邮箱或手机号）
	SentAt         *time.Time        `json:"sent_at"`
	DeliveredAt    *time.Time        `json:"delivered_at"`

	// 验证信息
	Attempts      int        `json:"attempts" gorm:"default:0"`
	MaxAttempts   int        `json:"max_attempts" gorm:"default:3"`
	LastAttemptAt *time.Time `json:"last_attempt_at"`
	VerifiedAt    *time.Time `json:"verified_at"`
	UsedAt        *time.Time `json:"used_at"`

	// 安全信息
	SourceIP    string `json:"source_ip" gorm:"size:45"`
	UserAgent   string `json:"user_agent" gorm:"size:500"`
	SessionID   string `json:"session_id" gorm:"size:255;index"`
	Fingerprint string `json:"fingerprint" gorm:"size:255"` // 设备指纹

	// 配置信息
	Length          int  `json:"length" gorm:"default:6"`                // 验证码长度
	IsNumeric       bool `json:"is_numeric" gorm:"default:true"`         // 是否为纯数字
	IsCaseSensitive bool `json:"is_case_sensitive" gorm:"default:false"` // 是否区分大小写
	ValidityMinutes int  `json:"validity_minutes" gorm:"default:10"`     // 有效期（分钟）

	// 元数据
	Metadata      string `json:"metadata" gorm:"type:text"`          // JSON格式存储元数据
	Purpose       string `json:"purpose" gorm:"size:255"`            // 使用目的描述
	ReferenceID   string `json:"reference_id" gorm:"size:255;index"` // 关联的业务ID
	ReferenceType string `json:"reference_type" gorm:"size:50"`      // 关联的业务类型

	// 重发控制
	ResendCount    int        `json:"resend_count" gorm:"default:0"`
	MaxResends     int        `json:"max_resends" gorm:"default:3"`
	LastResendAt   *time.Time `json:"last_resend_at"`
	NextResendAt   *time.Time `json:"next_resend_at"`
	ResendInterval int        `json:"resend_interval" gorm:"default:60"` // 重发间隔（秒）

	// 失败信息
	FailureReason string     `json:"failure_reason" gorm:"size:255"`
	FailedAt      *time.Time `json:"failed_at"`
	RevokedAt     *time.Time `json:"revoked_at"`
	RevokedBy     *uint      `json:"revoked_by" gorm:"index"`
	RevokedUser   *User      `json:"revoked_user,omitempty" gorm:"foreignKey:RevokedBy"`
	RevokeReason  string     `json:"revoke_reason" gorm:"size:255"`
}

// TableName 指定表名
func (OTPCode) TableName() string {
	return "otp_codes"
}

// IsExpired 检查是否已过期
func (otp *OTPCode) IsExpired() bool {
	return time.Now().After(otp.ExpiresAt)
}

// IsValid 检查是否有效
func (otp *OTPCode) IsValid() bool {
	return otp.Status == OTPStatusPending && !otp.IsExpired() && otp.Attempts < otp.MaxAttempts
}

// IsUsed 检查是否已使用
func (otp *OTPCode) IsUsed() bool {
	return otp.Status == OTPStatusUsed
}

// IsRevoked 检查是否已撤销
func (otp *OTPCode) IsRevoked() bool {
	return otp.Status == OTPStatusRevoked
}

// CanResend 检查是否可以重发
func (otp *OTPCode) CanResend() bool {
	if otp.ResendCount >= otp.MaxResends {
		return false
	}
	if otp.NextResendAt != nil && time.Now().Before(*otp.NextResendAt) {
		return false
	}
	return otp.Status == OTPStatusPending
}

// GetRemainingAttempts 获取剩余尝试次数
func (otp *OTPCode) GetRemainingAttempts() int {
	remaining := otp.MaxAttempts - otp.Attempts
	if remaining < 0 {
		return 0
	}
	return remaining
}

// GetRemainingTime 获取剩余有效时间
func (otp *OTPCode) GetRemainingTime() time.Duration {
	if otp.IsExpired() {
		return 0
	}
	return time.Until(otp.ExpiresAt)
}

// MarkAsUsed 标记为已使用
func (otp *OTPCode) MarkAsUsed() {
	now := time.Now()
	otp.Status = OTPStatusUsed
	otp.UsedAt = &now
	otp.VerifiedAt = &now
}

// MarkAsExpired 标记为已过期
func (otp *OTPCode) MarkAsExpired() {
	otp.Status = OTPStatusExpired
}

// MarkAsRevoked 标记为已撤销
func (otp *OTPCode) MarkAsRevoked(revokedBy uint, reason string) {
	now := time.Now()
	otp.Status = OTPStatusRevoked
	otp.RevokedAt = &now
	otp.RevokedBy = &revokedBy
	otp.RevokeReason = reason
}

// IncrementAttempts 增加尝试次数
func (otp *OTPCode) IncrementAttempts() {
	now := time.Now()
	otp.Attempts++
	otp.LastAttemptAt = &now
	if otp.Attempts >= otp.MaxAttempts {
		otp.Status = OTPStatusFailed
		otp.FailedAt = &now
		otp.FailureReason = "Maximum attempts exceeded"
	}
}

// OTPCreateRequest OTP创建请求
type OTPCreateRequest struct {
	UserID          uint                   `json:"user_id" validate:"required"`
	Type            OTPType                `json:"type" validate:"required"`
	DeliveryMethod  OTPDeliveryMethod      `json:"delivery_method" validate:"required"`
	Recipient       string                 `json:"recipient" validate:"required"`
	Length          *int                   `json:"length" validate:"omitempty,min=4,max=10"`
	IsNumeric       *bool                  `json:"is_numeric"`
	ValidityMinutes *int                   `json:"validity_minutes" validate:"omitempty,min=1,max=1440"`
	MaxAttempts     *int                   `json:"max_attempts" validate:"omitempty,min=1,max=10"`
	MaxResends      *int                   `json:"max_resends" validate:"omitempty,min=0,max=10"`
	Purpose         string                 `json:"purpose"`
	ReferenceID     string                 `json:"reference_id"`
	ReferenceType   string                 `json:"reference_type"`
	Metadata        map[string]interface{} `json:"metadata"`
}

// OTPVerifyRequest OTP验证请求
type OTPVerifyRequest struct {
	Code      string  `json:"code" validate:"required"`
	UserID    uint    `json:"user_id" validate:"required"`
	Type      OTPType `json:"type" validate:"required"`
	SessionID string  `json:"session_id"`
}

// OTPResendRequest OTP重发请求
type OTPResendRequest struct {
	OTPID          uint               `json:"otp_id" validate:"required"`
	DeliveryMethod *OTPDeliveryMethod `json:"delivery_method"`
	Recipient      *string            `json:"recipient"`
}

// OTPResponse OTP响应
type OTPResponse struct {
	ID             uint                   `json:"id"`
	CreatedAt      time.Time              `json:"created_at"`
	Type           OTPType                `json:"type"`
	Status         OTPStatus              `json:"status"`
	DeliveryMethod OTPDeliveryMethod      `json:"delivery_method"`
	Recipient      string                 `json:"recipient"`
	ExpiresAt      time.Time              `json:"expires_at"`
	SentAt         *time.Time             `json:"sent_at"`
	Attempts       int                    `json:"attempts"`
	MaxAttempts    int                    `json:"max_attempts"`
	ResendCount    int                    `json:"resend_count"`
	MaxResends     int                    `json:"max_resends"`
	NextResendAt   *time.Time             `json:"next_resend_at"`
	Purpose        string                 `json:"purpose"`
	ReferenceID    string                 `json:"reference_id"`
	ReferenceType  string                 `json:"reference_type"`
	RemainingTime  int64                  `json:"remaining_time"` // 剩余时间（秒）
	CanResend      bool                   `json:"can_resend"`
	Metadata       map[string]interface{} `json:"metadata"`
}

// ToResponse 转换为响应格式
func (otp *OTPCode) ToResponse() *OTPResponse {
	response := &OTPResponse{
		ID:             otp.ID,
		CreatedAt:      otp.CreatedAt,
		Type:           otp.Type,
		Status:         otp.Status,
		DeliveryMethod: otp.DeliveryMethod,
		Recipient:      otp.Recipient,
		ExpiresAt:      otp.ExpiresAt,
		SentAt:         otp.SentAt,
		Attempts:       otp.Attempts,
		MaxAttempts:    otp.MaxAttempts,
		ResendCount:    otp.ResendCount,
		MaxResends:     otp.MaxResends,
		NextResendAt:   otp.NextResendAt,
		Purpose:        otp.Purpose,
		ReferenceID:    otp.ReferenceID,
		ReferenceType:  otp.ReferenceType,
		CanResend:      otp.CanResend(),
	}

	// 计算剩余时间
	remainingTime := otp.GetRemainingTime()
	response.RemainingTime = int64(remainingTime.Seconds())

	// TODO: 解析JSON字段
	// response.Metadata = parseMetadataFromJSON(otp.Metadata)

	return response
}

// OTPFilter OTP过滤器
type OTPFilter struct {
	UserID          *uint               `json:"user_id"`
	Types           []OTPType           `json:"types"`
	Statuses        []OTPStatus         `json:"statuses"`
	DeliveryMethods []OTPDeliveryMethod `json:"delivery_methods"`
	Recipient       string              `json:"recipient"`
	ReferenceID     string              `json:"reference_id"`
	ReferenceType   string              `json:"reference_type"`
	DateFrom        *time.Time          `json:"date_from"`
	DateTo          *time.Time          `json:"date_to"`
	IsExpired       *bool               `json:"is_expired"`
	Limit           int                 `json:"limit"`
	Offset          int                 `json:"offset"`
	OrderBy         string              `json:"order_by"`  // created_at, expires_at, attempts
	OrderDir        string              `json:"order_dir"` // asc, desc
}
