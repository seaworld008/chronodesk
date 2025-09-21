package auth

import (
	"time"

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
func NewAuthModule(db *gorm.DB) (*AuthModule, error) {
	// 创建配置
	config := &AuthConfig{
		JWTSecret:                "your-jwt-secret-key-change-in-production",
		JWTRefreshSecret:         "your-jwt-refresh-secret-key-change-in-production",
		AccessTokenExpire:        15 * time.Minute,
		RefreshTokenExpire:       7 * 24 * time.Hour,
		EmailVerificationExpire:  24 * time.Hour,
		PasswordResetExpire:      1 * time.Hour,
		OTPExpire:                5 * time.Minute,
		MaxFailedLogins:          5,
		LockoutDuration:          30 * time.Minute,
		PasswordMinLength:        8,
		RequireEmailVerification: false, // 开发环境设为false
		EnableOTP:                true,
		EnableRegistration:       true,
	}

	// 创建日志器
	logger := &SimpleLogger{}

	// 创建仓库
	userRepo := NewGormUserRepository(db)
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
	emailConfigService := services.NewEmailConfigService(db)

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

// SetupAuthRoutes 设置认证路由（从routes.go移动到这里以便集成）
func (m *AuthModule) SetupAuthRoutes(router interface{}) {
	// 这里可以根据实际的路由器类型进行设置
	// 由于我们使用Gin，这个方法可以在main.go中直接调用routes.go的函数
}
