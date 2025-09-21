package services

import (
	"context"
	"errors"
	"fmt"
	"net/smtp"

	"gorm.io/gorm"
	"gongdan-system/internal/models"
)

// EmailConfigServiceInterface defines the interface for email config service
type EmailConfigServiceInterface interface {
	GetEmailConfig(ctx context.Context) (*models.EmailConfig, error)
	UpdateEmailConfig(ctx context.Context, req *models.EmailConfigUpdateRequest, userID uint) (*models.EmailConfig, error)
	TestEmailConnection(ctx context.Context, req *models.EmailTestRequest) error
	IsEmailVerificationEnabled(ctx context.Context) (bool, error)
	CanSendEmail(ctx context.Context) (bool, error)
	GetSMTPConfig(ctx context.Context) (*models.EmailConfig, error)
}

// EmailConfigService implements EmailConfigServiceInterface
type EmailConfigService struct {
	db *gorm.DB
}

// NewEmailConfigService creates a new email config service
func NewEmailConfigService(db *gorm.DB) EmailConfigServiceInterface {
	return &EmailConfigService{
		db: db,
	}
}

// GetEmailConfig retrieves the current email configuration
func (s *EmailConfigService) GetEmailConfig(ctx context.Context) (*models.EmailConfig, error) {
	var config models.EmailConfig

	// 获取最新的活跃配置
	err := s.db.WithContext(ctx).
		Where("is_active = ?", true).
		Order("created_at DESC").
		First(&config).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 如果没有配置，创建默认配置
			return s.createDefaultConfig(ctx)
		}
		return nil, fmt.Errorf("failed to get email config: %w", err)
	}

	return &config, nil
}

// UpdateEmailConfig updates the email configuration
func (s *EmailConfigService) UpdateEmailConfig(ctx context.Context, req *models.EmailConfigUpdateRequest, userID uint) (*models.EmailConfig, error) {
	config, err := s.GetEmailConfig(ctx)
	if err != nil {
		return nil, err
	}

	// 更新字段
	if req.EmailVerificationEnabled != nil {
		config.EmailVerificationEnabled = *req.EmailVerificationEnabled
	}
	if req.SMTPHost != nil {
		config.SMTPHost = *req.SMTPHost
	}
	if req.SMTPPort != nil {
		config.SMTPPort = *req.SMTPPort
	}
	if req.SMTPUsername != nil {
		config.SMTPUsername = *req.SMTPUsername
	}
	if req.SMTPPassword != nil {
		config.SMTPPassword = *req.SMTPPassword
	}
	if req.SMTPUseTLS != nil {
		config.SMTPUseTLS = *req.SMTPUseTLS
	}
	if req.SMTPUseSSL != nil {
		config.SMTPUseSSL = *req.SMTPUseSSL
	}
	if req.FromEmail != nil {
		config.FromEmail = *req.FromEmail
	}
	if req.FromName != nil {
		config.FromName = *req.FromName
	}
	if req.WelcomeEmailSubject != nil {
		config.WelcomeEmailSubject = *req.WelcomeEmailSubject
	}
	if req.WelcomeEmailTemplate != nil {
		config.WelcomeEmailTemplate = *req.WelcomeEmailTemplate
	}
	if req.OTPEmailSubject != nil {
		config.OTPEmailSubject = *req.OTPEmailSubject
	}
	if req.OTPEmailTemplate != nil {
		config.OTPEmailTemplate = *req.OTPEmailTemplate
	}

	config.UpdatedByID = &userID

	// 如果启用了邮箱验证，验证SMTP配置
	if config.EmailVerificationEnabled {
		if !config.IsConfigured() {
			return nil, errors.New("SMTP配置不完整，无法启用邮箱验证")
		}

		// 测试SMTP连接
		if err := s.testSMTPConnection(config); err != nil {
			return nil, fmt.Errorf("SMTP连接测试失败: %w", err)
		}
	}

	// 保存配置
	if err := s.db.WithContext(ctx).Save(config).Error; err != nil {
		return nil, fmt.Errorf("failed to update email config: %w", err)
	}

	return config, nil
}

// TestEmailConnection tests the email connection with provided settings
func (s *EmailConfigService) TestEmailConnection(ctx context.Context, req *models.EmailTestRequest) error {
	config, err := s.GetEmailConfig(ctx)
	if err != nil {
		return err
	}

	if !config.IsConfigured() {
		return errors.New("SMTP配置不完整")
	}

	// 测试连接
	if err := s.testSMTPConnection(config); err != nil {
		return err
	}

	// 发送测试邮件
	return s.sendTestEmail(config, req)
}

// IsEmailVerificationEnabled checks if email verification is enabled
func (s *EmailConfigService) IsEmailVerificationEnabled(ctx context.Context) (bool, error) {
	config, err := s.GetEmailConfig(ctx)
	if err != nil {
		return false, err
	}

	return config.EmailVerificationEnabled, nil
}

// CanSendEmail checks if the system can send emails
func (s *EmailConfigService) CanSendEmail(ctx context.Context) (bool, error) {
	config, err := s.GetEmailConfig(ctx)
	if err != nil {
		return false, err
	}

	return config.CanSendEmail(), nil
}

// GetSMTPConfig retrieves SMTP configuration for email sending
func (s *EmailConfigService) GetSMTPConfig(ctx context.Context) (*models.EmailConfig, error) {
	config, err := s.GetEmailConfig(ctx)
	if err != nil {
		return nil, err
	}

	if !config.CanSendEmail() {
		return nil, errors.New("邮箱验证未启用或SMTP配置不完整")
	}

	return config, nil
}

// createDefaultConfig creates a default email configuration
func (s *EmailConfigService) createDefaultConfig(ctx context.Context) (*models.EmailConfig, error) {
	config := &models.EmailConfig{
		EmailVerificationEnabled: false,
		SMTPPort:                 587,
		SMTPUseTLS:               true,
		SMTPUseSSL:               false,
		FromName:                 "工单系统",
		WelcomeEmailSubject:      "欢迎注册工单系统",
		OTPEmailSubject:          "邮箱验证码",
		WelcomeEmailTemplate:     s.getDefaultWelcomeTemplate(),
		OTPEmailTemplate:         s.getDefaultOTPTemplate(),
		IsActive:                 true,
	}

	if err := s.db.WithContext(ctx).Create(config).Error; err != nil {
		return nil, fmt.Errorf("failed to create default email config: %w", err)
	}

	return config, nil
}

// testSMTPConnection tests the SMTP connection
func (s *EmailConfigService) testSMTPConnection(config *models.EmailConfig) error {
	addr := fmt.Sprintf("%s:%d", config.SMTPHost, config.SMTPPort)

	// 创建认证
	auth := smtp.PlainAuth("", config.SMTPUsername, config.SMTPPassword, config.SMTPHost)

	// 测试连接
	client, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("无法连接到SMTP服务器: %w", err)
	}
	defer client.Close()

	// 如果使用TLS
	if config.SMTPUseTLS {
		if err := client.StartTLS(nil); err != nil {
			return fmt.Errorf("TLS连接失败: %w", err)
		}
	}

	// 测试认证
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("SMTP认证失败: %w", err)
	}

	return nil
}

// sendTestEmail sends a test email
func (s *EmailConfigService) sendTestEmail(config *models.EmailConfig, req *models.EmailTestRequest) error {
	addr := fmt.Sprintf("%s:%d", config.SMTPHost, config.SMTPPort)
	auth := smtp.PlainAuth("", config.SMTPUsername, config.SMTPPassword, config.SMTPHost)

	// 构建邮件内容
	msg := fmt.Sprintf("To: %s\r\nSubject: %s\r\n\r\n%s",
		req.ToEmail, req.Subject, req.Content)

	// 发送邮件
	err := smtp.SendMail(addr, auth, config.FromEmail, []string{req.ToEmail}, []byte(msg))
	if err != nil {
		return fmt.Errorf("发送测试邮件失败: %w", err)
	}

	return nil
}

// getDefaultWelcomeTemplate returns the default welcome email template
func (s *EmailConfigService) getDefaultWelcomeTemplate() string {
	return `亲爱的 {{.Username}}，

欢迎注册工单系统！

您的账户已成功创建。您现在可以登录系统并开始使用我们的服务。

如果您有任何问题，请随时联系我们的支持团队。

祝好，
工单系统团队`
}

// getDefaultOTPTemplate returns the default OTP email template
func (s *EmailConfigService) getDefaultOTPTemplate() string {
	return `亲爱的用户，

您的邮箱验证码是：{{.OTP}}

此验证码将在10分钟后过期，请及时使用。

如果您没有请求此验证码，请忽略此邮件。

祝好，
工单系统团队`
}
