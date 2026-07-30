package services

import (
	"context"
	"errors"
	"fmt"
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
			// 读取配置不得产生写入副作用。首次显式保存会在事务内创建记录，
			// 取得主键后再以记录专属 AAD 加密 SMTP 密码。
			return models.DefaultEmailConfig(), nil
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
	if config.SMTPUseTLS && config.SMTPUseSSL {
		return nil, errors.New("SMTP STARTTLS与隐式TLS不能同时启用")
	}

	// 如果启用了邮箱验证，验证SMTP配置
	if config.EmailVerificationEnabled {
		if !config.IsConfigured() {
			return nil, errors.New("SMTP配置不完整，无法启用邮箱验证")
		}

		skipSMTPTest := req.SkipSMTPTest != nil && *req.SkipSMTPTest
		if !skipSMTPTest {
			// 测试SMTP连接
			if err := s.testSMTPConnection(ctx, config); err != nil {
				return nil, fmt.Errorf("SMTP连接测试失败: %w", err)
			}
		}
	}

	// 只将密文副本持久化；返回给调用方的运行时对象继续保留明文。首次
	// 保存先在同一事务中插入不含密码的记录以取得主键，再用该主键生成
	// record-specific AAD，避免生成无法在重启后解密的 ID=0 密文。
	var persisted models.EmailConfig
	if err := transactionForContext(ctx, s.db, func(tx *gorm.DB) error {
		persisted = *config
		if persisted.ID == 0 {
			plaintextPassword := persisted.SMTPPassword
			persisted.SMTPPassword = ""
			if err := tx.Create(&persisted).Error; err != nil {
				return fmt.Errorf("创建邮箱配置失败: %w", err)
			}
			persisted.SMTPPassword = plaintextPassword
			config.ID = persisted.ID
			config.CreatedAt = persisted.CreatedAt
		}
		protectedPassword, protectErr := security.ProtectOptional(
			s.protector,
			config.SMTPPassword,
			emailSMTPPasswordAAD(persisted.ID),
		)
		if protectErr != nil {
			return fmt.Errorf("无法加密SMTP凭据: %w", protectErr)
		}
		persisted.SMTPPassword = protectedPassword
		if err := tx.Save(&persisted).Error; err != nil {
			return fmt.Errorf("更新邮箱配置失败: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
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
	if err := s.testSMTPConnection(ctx, config); err != nil {
		return err
	}

	// 发送测试邮件
	return s.sendTestEmail(ctx, config, req)
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
		return nil, errors.New("SMTP配置未启用或不完整")
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
func (s *EmailConfigService) testSMTPConnection(
	ctx context.Context,
	config *models.EmailConfig,
) error {
	transport, err := smtpTransportForEmailConfig(config)
	if err != nil {
		return err
	}
	if err := transport.TestConnection(ctx); err != nil {
		return fmt.Errorf("SMTP连接测试失败: %w", err)
	}
	return nil
}

// sendTestEmail sends a test email
func (s *EmailConfigService) sendTestEmail(
	ctx context.Context,
	config *models.EmailConfig,
	req *models.EmailTestRequest,
) error {
	if req == nil {
		return errors.New("测试邮件参数不能为空")
	}
	recipient, err := mailer.CanonicalMailbox(req.ToEmail)
	if err != nil {
		return fmt.Errorf("收件邮箱无效: %w", err)
	}
	sender, err := mailer.CanonicalMailbox(config.FromEmail)
	if err != nil {
		return fmt.Errorf("发件邮箱无效: %w", err)
	}
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

	transport, err := smtpTransportForEmailConfig(config)
	if err != nil {
		return err
	}
	if err := transport.Send(ctx, sender, []string{recipient}, msg); err != nil {
		return fmt.Errorf("发送测试邮件失败: %w", err)
	}

	return nil
}

func smtpTransportForEmailConfig(
	config *models.EmailConfig,
) (*mailer.SMTPTransport, error) {
	if config == nil {
		return nil, errors.New("SMTP配置不能为空")
	}
	transport, err := mailer.NewSMTPTransport(mailer.SMTPTransportConfig{
		Host:           config.SMTPHost,
		Port:           config.SMTPPort,
		Username:       config.SMTPUsername,
		Password:       config.SMTPPassword,
		UseSTARTTLS:    config.SMTPUseTLS,
		UseImplicitTLS: config.SMTPUseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("SMTP配置无效: %w", err)
	}
	return transport, nil
}
