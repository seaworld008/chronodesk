package services

import (
	"context"
	"errors"
	"fmt"
	"net/smtp"
	"strconv"

	"github.com/seaworld008/chronodesk/server/internal/mailer"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/security"
	"gorm.io/gorm"
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
	db        *gorm.DB
	protector security.Protector
}

// NewEmailConfigService creates a new email config service
func NewEmailConfigService(db *gorm.DB) EmailConfigServiceInterface {
	protector, _ := security.LoadDeploymentKeyringFromEnvironment()
	return NewEmailConfigServiceWithProtector(db, protector)
}

// NewEmailConfigServiceWithProtector injects the application data-encryption
// keyring. A nil protector is fail-closed whenever an SMTP password is present.
func NewEmailConfigServiceWithProtector(
	db *gorm.DB,
	protector security.Protector,
) EmailConfigServiceInterface {
	return &EmailConfigService{
		db:        db,
		protector: protector,
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

	if err := s.revealSMTPPassword(&config); err != nil {
		return nil, fmt.Errorf("无法读取SMTP凭据: %w", err)
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

	if config.FromEmail != "" {
		if _, err := mailer.CanonicalMailbox(config.FromEmail); err != nil {
			return nil, fmt.Errorf("发件邮箱无效: %w", err)
		}
	}
	for _, header := range []struct {
		name  string
		value string
	}{
		{name: "发件人名称", value: config.FromName},
		{name: "欢迎邮件主题", value: config.WelcomeEmailSubject},
		{name: "验证码邮件主题", value: config.OTPEmailSubject},
	} {
		if err := mailer.ValidateHeaderValue(header.name, header.value, true); err != nil {
			return nil, err
		}
	}

	config.UpdatedByID = &userID

	// 如果启用了邮箱验证，验证SMTP配置
	if config.EmailVerificationEnabled {
		if !config.IsConfigured() {
			return nil, errors.New("SMTP配置不完整，无法启用邮箱验证")
		}

		skipSMTPTest := req.SkipSMTPTest != nil && *req.SkipSMTPTest
		if !skipSMTPTest {
			// 测试SMTP连接
			if err := s.testSMTPConnection(config); err != nil {
				return nil, fmt.Errorf("SMTP连接测试失败: %w", err)
			}
		}
	}

	// 只将密文副本持久化；返回给调用方的运行时对象继续保留明文，以便
	// 立即执行连接测试或发信。
	persisted := *config
	persisted.SMTPPassword, err = security.ProtectOptional(
		s.protector,
		config.SMTPPassword,
		emailSMTPPasswordAAD(config.ID),
	)
	if err != nil {
		return nil, fmt.Errorf("无法加密SMTP凭据: %w", err)
	}
	if err := s.db.WithContext(ctx).Save(&persisted).Error; err != nil {
		return nil, fmt.Errorf("failed to update email config: %w", err)
	}
	config.CreatedAt = persisted.CreatedAt
	config.UpdatedAt = persisted.UpdatedAt

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

func (s *EmailConfigService) revealSMTPPassword(config *models.EmailConfig) error {
	if config == nil {
		return errors.New("SMTP配置不能为空")
	}
	password, err := security.RevealOptional(
		s.protector,
		config.SMTPPassword,
		emailSMTPPasswordAAD(config.ID),
	)
	if err != nil {
		return err
	}
	config.SMTPPassword = password
	return nil
}

func emailSMTPPasswordAAD(configID uint) []byte {
	return security.FieldAAD(
		"email_configs",
		strconv.FormatUint(uint64(configID), 10),
		"smtp_password",
	)
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
	recipient, err := mailer.CanonicalMailbox(req.ToEmail)
	if err != nil {
		return fmt.Errorf("收件邮箱无效: %w", err)
	}
	sender, err := mailer.CanonicalMailbox(config.FromEmail)
	if err != nil {
		return fmt.Errorf("发件邮箱无效: %w", err)
	}
	addr := fmt.Sprintf("%s:%d", config.SMTPHost, config.SMTPPort)
	auth := smtp.PlainAuth("", config.SMTPUsername, config.SMTPPassword, config.SMTPHost)

	// 测试内容是纯文本数据；MIME构建器负责严格的头校验与正文传输编码。
	msg, err := mailer.BuildTextMessage(
		sender,
		config.FromName,
		req.Subject,
		req.Content,
	)
	if err != nil {
		return fmt.Errorf("构建测试邮件失败: %w", err)
	}

	// 发送邮件
	err = smtp.SendMail(addr, auth, sender, []string{recipient}, msg)
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
