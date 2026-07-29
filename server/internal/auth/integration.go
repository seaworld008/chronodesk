package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	appconfig "gongdan-system/internal/config"
	"gongdan-system/internal/security"
	"gongdan-system/internal/services"
	"gorm.io/gorm"
)

// AuthModule 认证模块
type AuthModule struct {
	AuthService *AuthService
	Handler     *AuthHandler
	Config      *AuthConfig
}

// NewAuthModule 创建认证模块
func NewAuthModule(
	db *gorm.DB,
	cfg *appconfig.Config,
	protector security.Protector,
) (*AuthModule, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	if protector == nil {
		return nil, security.ErrKeyringUnavailable
	}
	if err := ValidateAuthCredentialStorage(context.Background(), db, protector); err != nil {
		return nil, fmt.Errorf("validate authentication credential storage: %w", err)
	}

	// 创建配置
	config := &AuthConfig{
		JWTSecret:                cfg.JWT.Secret,
		JWTRefreshSecret:         cfg.JWT.RefreshSecret,
		AccessTokenExpire:        cfg.JWT.ExpiresIn,
		RefreshTokenExpire:       cfg.JWT.RefreshExpiresIn,
		EmailVerificationExpire:  24 * time.Hour,
		PasswordResetExpire:      1 * time.Hour,
		OTPExpire:                5 * time.Minute,
		MaxFailedLogins:          5,
		LockoutDuration:          30 * time.Minute,
		PasswordMinLength:        8,
		RequireEmailVerification: false,
		EnableOTP:                true,
		EnableRegistration:       true,
	}

	// 创建日志器
	logger := &SimpleLogger{}

	// 创建仓库
	userRepo := NewGormUserRepository(db, protector)
	profileRepo := NewGormProfileRepository(db) // 使用GORM版本
	tokenRepo := NewGormTokenRepository(db)
	loginAttemptRepo := NewGormLoginAttemptRepository(db)
	loginHistoryRepo := NewGormLoginHistoryRepository(db)
	trustedDeviceRepo := NewGormTrustedDeviceRepository(db)
	configService := services.NewConfigService(db)

	// 创建服务
	emailConfig := &EmailConfig{
		Host:     "localhost",
		Port:     "587",
		Username: "",
		Password: "",
		From:     "noreply@ticket-system.com",
	}
	emailService := NewSMTPEmailService(emailConfig)
	otpService := NewSimpleOTPService("Ticket System")
	passwordService := NewSimplePasswordService(config.PasswordMinLength, "ticket-system-salt")
	jwtManager := NewSimpleJWTManager(
		config.JWTSecret,
		config.JWTRefreshSecret,
		config.AccessTokenExpire,
		config.RefreshTokenExpire,
	)

	// 创建邮箱配置服务
	emailConfigService := services.NewEmailConfigServiceWithProtector(db, protector)

	// 创建认证服务
	authService := NewAuthService(
		userRepo,
		profileRepo,
		tokenRepo,
		loginAttemptRepo,
		loginHistoryRepo,
		trustedDeviceRepo,
		configService,
		emailService,
		emailConfigService,
		otpService,
		passwordService,
		jwtManager,
		config,
	)

	// 创建处理器
	authHandler := NewAuthHandler(authService, logger)

	return &AuthModule{
		AuthService: authService,
		Handler:     authHandler,
		Config:      config,
	}, nil
}

// GetAuthService 获取认证服务
func (m *AuthModule) GetAuthService() *AuthService {
	return m.AuthService
}

// GetHandler 获取处理器
func (m *AuthModule) GetHandler() *AuthHandler {
	return m.Handler
}

// GetConfig 获取配置
func (m *AuthModule) GetConfig() *AuthConfig {
	return m.Config
}
