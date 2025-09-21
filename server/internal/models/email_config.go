package models

import (
	"time"
)

// EmailConfig 邮箱配置模型
type EmailConfig struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	// 邮箱验证开关
	EmailVerificationEnabled bool `json:"email_verification_enabled" gorm:"default:false;not null"`

	// SMTP配置
	SMTPHost     string `json:"smtp_host" gorm:"size:255"`
	SMTPPort     int    `json:"smtp_port" gorm:"default:587"`
	SMTPUsername string `json:"smtp_username" gorm:"size:255"`
	SMTPPassword string `json:"-" gorm:"size:255"` // 不在JSON中返回密码
	SMTPUseTLS   bool   `json:"smtp_use_tls" gorm:"default:true"`
	SMTPUseSSL   bool   `json:"smtp_use_ssl" gorm:"default:false"`

	// 邮件发送配置
	FromEmail string `json:"from_email" gorm:"size:255"`
	FromName  string `json:"from_name" gorm:"size:255;default:'工单系统'"`

	// 邮件模板配置
	WelcomeEmailSubject  string `json:"welcome_email_subject" gorm:"size:255;default:'欢迎注册工单系统'"`
	WelcomeEmailTemplate string `json:"welcome_email_template" gorm:"type:text"`
	OTPEmailSubject      string `json:"otp_email_subject" gorm:"size:255;default:'邮箱验证码'"`
	OTPEmailTemplate     string `json:"otp_email_template" gorm:"type:text"`

	// 配置状态
	IsActive bool `json:"is_active" gorm:"default:true;not null"`

	// 最后更新者
	UpdatedByID *uint `json:"updated_by_id" gorm:"index"`
	UpdatedBy   *User `json:"updated_by,omitempty" gorm:"foreignKey:UpdatedByID"`
}

// TableName 指定表名
func (EmailConfig) TableName() string {
	return "email_configs"
}

// IsConfigured 检查SMTP是否已配置
func (ec *EmailConfig) IsConfigured() bool {
	return ec.SMTPHost != "" && ec.SMTPUsername != "" && ec.SMTPPassword != "" && ec.FromEmail != ""
}

// CanSendEmail 检查是否可以发送邮件
func (ec *EmailConfig) CanSendEmail() bool {
	return ec.EmailVerificationEnabled && ec.IsConfigured() && ec.IsActive
}

// EmailConfigCreateRequest 创建邮箱配置请求
type EmailConfigCreateRequest struct {
	EmailVerificationEnabled bool   `json:"email_verification_enabled"`
	SMTPHost                 string `json:"smtp_host" validate:"required_if=EmailVerificationEnabled true,omitempty,hostname"`
	SMTPPort                 int    `json:"smtp_port" validate:"required_if=EmailVerificationEnabled true,omitempty,min=1,max=65535"`
	SMTPUsername             string `json:"smtp_username" validate:"required_if=EmailVerificationEnabled true"`
	SMTPPassword             string `json:"smtp_password" validate:"required_if=EmailVerificationEnabled true"`
	SMTPUseTLS               bool   `json:"smtp_use_tls"`
	SMTPUseSSL               bool   `json:"smtp_use_ssl"`
	FromEmail                string `json:"from_email" validate:"required_if=EmailVerificationEnabled true,omitempty,email"`
	FromName                 string `json:"from_name"`
	WelcomeEmailSubject      string `json:"welcome_email_subject"`
	WelcomeEmailTemplate     string `json:"welcome_email_template"`
	OTPEmailSubject          string `json:"otp_email_subject"`
	OTPEmailTemplate         string `json:"otp_email_template"`
}

// EmailConfigUpdateRequest 更新邮箱配置请求
type EmailConfigUpdateRequest struct {
	EmailVerificationEnabled *bool   `json:"email_verification_enabled"`
	SMTPHost                 *string `json:"smtp_host" validate:"omitempty,hostname"`
	SMTPPort                 *int    `json:"smtp_port" validate:"omitempty,min=1,max=65535"`
	SMTPUsername             *string `json:"smtp_username"`
	SMTPPassword             *string `json:"smtp_password"`
	SMTPUseTLS               *bool   `json:"smtp_use_tls"`
	SMTPUseSSL               *bool   `json:"smtp_use_ssl"`
	FromEmail                *string `json:"from_email" validate:"omitempty,email"`
	FromName                 *string `json:"from_name"`
	WelcomeEmailSubject      *string `json:"welcome_email_subject"`
	WelcomeEmailTemplate     *string `json:"welcome_email_template"`
	OTPEmailSubject          *string `json:"otp_email_subject"`
	OTPEmailTemplate         *string `json:"otp_email_template"`
}

// EmailConfigResponse 邮箱配置响应
type EmailConfigResponse struct {
	ID                       uint      `json:"id"`
	CreatedAt                time.Time `json:"created_at"`
	UpdatedAt                time.Time `json:"updated_at"`
	EmailVerificationEnabled bool      `json:"email_verification_enabled"`
	SMTPHost                 string    `json:"smtp_host"`
	SMTPPort                 int       `json:"smtp_port"`
	SMTPUsername             string    `json:"smtp_username"`
	SMTPUseTLS               bool      `json:"smtp_use_tls"`
	SMTPUseSSL               bool      `json:"smtp_use_ssl"`
	FromEmail                string    `json:"from_email"`
	FromName                 string    `json:"from_name"`
	WelcomeEmailSubject      string    `json:"welcome_email_subject"`
	WelcomeEmailTemplate     string    `json:"welcome_email_template"`
	OTPEmailSubject          string    `json:"otp_email_subject"`
	OTPEmailTemplate         string    `json:"otp_email_template"`
	IsActive                 bool      `json:"is_active"`
	IsConfigured             bool      `json:"is_configured"`
	CanSendEmail             bool      `json:"can_send_email"`
	UpdatedByID              *uint     `json:"updated_by_id"`
}

// ToResponse 转换为响应格式
func (ec *EmailConfig) ToResponse() *EmailConfigResponse {
	return &EmailConfigResponse{
		ID:                       ec.ID,
		CreatedAt:                ec.CreatedAt,
		UpdatedAt:                ec.UpdatedAt,
		EmailVerificationEnabled: ec.EmailVerificationEnabled,
		SMTPHost:                 ec.SMTPHost,
		SMTPPort:                 ec.SMTPPort,
		SMTPUsername:             ec.SMTPUsername,
		SMTPUseTLS:               ec.SMTPUseTLS,
		SMTPUseSSL:               ec.SMTPUseSSL,
		FromEmail:                ec.FromEmail,
		FromName:                 ec.FromName,
		WelcomeEmailSubject:      ec.WelcomeEmailSubject,
		WelcomeEmailTemplate:     ec.WelcomeEmailTemplate,
		OTPEmailSubject:          ec.OTPEmailSubject,
		OTPEmailTemplate:         ec.OTPEmailTemplate,
		IsActive:                 ec.IsActive,
		IsConfigured:             ec.IsConfigured(),
		CanSendEmail:             ec.CanSendEmail(),
		UpdatedByID:              ec.UpdatedByID,
	}
}

// EmailTestRequest 邮件测试请求
type EmailTestRequest struct {
	ToEmail string `json:"to_email" validate:"required,email"`
	Subject string `json:"subject" validate:"required"`
	Content string `json:"content" validate:"required"`
}
