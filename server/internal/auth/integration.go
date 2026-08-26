package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	appconfig "github.com/seaworld008/chronodesk/server/internal/config"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/security"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/gorm"
)

// AuthModule 认证模块
type AuthModule struct {
	Handler             *AuthHandler
	EmailOutboxConsumer *AuthEmailOutboxConsumer
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
	authProjectScope, err := resolveActiveDefaultAuthProjectScope(
		context.Background(),
		db,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"resolve authentication DEFAULT project: %w",
			err,
		)
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
	otpService := NewSimpleOTPService("ChronoDesk")
	passwordService, err := NewSimplePasswordService(PasswordServiceConfig{
		MinLength:  config.PasswordMinLength,
		BcryptCost: cfg.Security.BcryptCost,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize password service: %w", err)
	}
	jwtManager, err := NewSimpleJWTManager(JWTManagerConfig{
		AccessSecret:  cfg.JWT.Secret,
		RefreshSecret: cfg.JWT.RefreshSecret,
		AccessExpire:  cfg.JWT.ExpiresIn,
		RefreshExpire: cfg.JWT.RefreshExpiresIn,
		Issuer:        cfg.JWT.Issuer,
		Audience:      cfg.JWT.Audience,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize human JWT manager: %w", err)
	}

	// 创建邮箱配置服务
	emailConfigService := services.NewEmailConfigServiceWithProtector(db, protector)
	emailService, err := NewConfiguredSMTPEmailService(
		emailConfigService,
		cfg.App.WebURL,
		cfg.Server.Environment,
	)
	if err != nil {
		return nil, fmt.Errorf("initialize authentication email sender: %w", err)
	}
	authEmailOutboxRepo, err := NewGormAuthEmailOutboxRepository(
		db,
		protector,
		authProjectScope,
		strings.TrimRight(cfg.Agent.Issuer, "/")+"/events",
	)
	if err != nil {
		return nil, fmt.Errorf("initialize authentication email Outbox: %w", err)
	}
	authEmailOutboxConsumer, err := NewAuthEmailOutboxConsumer(
		db,
		protector,
		emailService,
	)
	if err != nil {
		return nil, fmt.Errorf("initialize authentication email consumer: %w", err)
	}
	allowedBrowserOrigin, err := browserOriginFromApplicationWebURL(
		cfg.App.WebURL,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"initialize authentication browser origin: %w",
			err,
		)
	}

	// 创建认证服务
	authService := NewAuthService(
		userRepo,
		profileRepo,
		tokenRepo,
		loginAttemptRepo,
		loginHistoryRepo,
		trustedDeviceRepo,
		configService,
		emailConfigService,
		otpService,
		passwordService,
		jwtManager,
		config,
		WithAuthEmailOutboxRepository(authEmailOutboxRepo),
	)

	// 创建处理器
	authHandler := NewAuthHandler(
		authService,
		logger,
		WithSecureAuthCookies(cfg.Server.Environment == "production"),
		WithAllowedBrowserOrigin(allowedBrowserOrigin),
	)

	return &AuthModule{
		Handler:             authHandler,
		EmailOutboxConsumer: authEmailOutboxConsumer,
	}, nil
}

func resolveActiveDefaultAuthProjectScope(
	ctx context.Context,
	db *gorm.DB,
) (models.ProjectScope, error) {
	if ctx == nil || db == nil {
		return models.ProjectScope{}, errors.New(
			"authentication DEFAULT project database is required",
		)
	}
	var projects []models.Project
	if err := db.WithContext(ctx).
		Select("id", "organization_id").
		Where(
			"key = ? AND status = ?",
			models.ProjectKey("DEFAULT"),
			models.ProjectStatusActive,
		).
		Order("organization_id ASC, id ASC").
		Limit(2).
		Find(&projects).Error; err != nil {
		return models.ProjectScope{}, err
	}
	if len(projects) == 0 {
		return models.ProjectScope{}, errors.New(
			"one active DEFAULT project is required, found none",
		)
	}
	if len(projects) != 1 {
		return models.ProjectScope{}, fmt.Errorf(
			"one active DEFAULT project is required, found %d or more",
			len(projects),
		)
	}
	scope := projects[0].Scope()
	if err := scope.Validate(); err != nil {
		return models.ProjectScope{}, fmt.Errorf(
			"active DEFAULT project scope is invalid: %w",
			err,
		)
	}
	return scope, nil
}
