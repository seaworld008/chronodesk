package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/gorm"
)

// 错误定义
var (
	ErrInvalidCredentials  = errors.New("invalid credentials")
	ErrUserNotFound        = errors.New("user not found")
	ErrUserExists          = errors.New("user already exists")
	ErrInvalidToken        = errors.New("invalid token")
	ErrTokenExpired        = errors.New("token expired")
	ErrInvalidOTP          = errors.New("invalid OTP")
	ErrOTPExpired          = errors.New("OTP expired")
	ErrEmailNotVerified    = errors.New("email not verified")
	ErrAccountLocked       = errors.New("account locked")
	ErrAccountInactive     = errors.New("account is inactive")
	ErrAccountSuspended    = errors.New("account is suspended")
	ErrAccountDeleted      = errors.New("account is deleted")
	ErrInvalidAccountState = errors.New("invalid account state")
	ErrPasswordTooWeak     = errors.New("password too weak")
)

var (
	defaultTrustedDeviceTTL        = 30 * 24 * time.Hour
	defaultTrustedDeviceMaxPerUser = 5
)

// UserRole 与领域用户模型共享同一组人类角色，避免认证与业务授权漂移。
type UserRole = models.UserRole

const (
	RoleCustomer   = models.RoleCustomer
	RoleAgent      = models.RoleAgent
	RoleSupervisor = models.RoleSupervisor
	RoleAdmin      = models.RoleAdmin
)

// UserStatus 与领域用户模型共享同一组持久化状态。临时锁定只由
// LockedUntil 表达，不再伪造一个无法写入数据库的 locked 状态。
type UserStatus = models.UserStatus

const (
	StatusActive    = models.UserStatusActive
	StatusInactive  = models.UserStatusInactive
	StatusSuspended = models.UserStatusSuspended
	StatusDeleted   = models.UserStatusDeleted
)

// User 用户模型
type User struct {
	ID                uint           `json:"id" gorm:"primaryKey"`
	Username          string         `json:"username" gorm:"uniqueIndex;not null"`
	Email             string         `json:"email" gorm:"uniqueIndex;not null"`
	PasswordHash      string         `json:"-" gorm:"not null"`
	Role              UserRole       `json:"role" gorm:"default:'customer'"`
	Status            UserStatus     `json:"status" gorm:"default:'active'"`
	EmailVerified     bool           `json:"email_verified" gorm:"default:false"`
	EmailVerifiedAt   *time.Time     `json:"email_verified_at"`
	LastLoginAt       *time.Time     `json:"last_login_at"`
	FailedLoginCount  int            `json:"failed_login_count" gorm:"default:0"`
	LockedUntil       *time.Time     `json:"locked_until"`
	OTPEnabled        bool           `json:"otp_enabled" gorm:"default:false"`
	OTPSecret         string         `json:"-"`
	BackupCodes       string         `json:"-"`
	PasswordChangedAt *time.Time     `json:"password_changed_at"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
	DeletedAt         gorm.DeletedAt `json:"-" gorm:"index"`
}

// UserProfile 用户资料
type UserProfile struct {
	ID          uint        `json:"id" gorm:"primaryKey"`
	UserID      uint        `json:"user_id" gorm:"uniqueIndex;not null"`
	FirstName   string      `json:"first_name"`
	LastName    string      `json:"last_name"`
	DisplayName string      `json:"display_name"`
	Avatar      string      `json:"avatar"`
	Phone       string      `json:"phone"`
	Department  string      `json:"department"`
	Position    string      `json:"position"`
	Timezone    string      `json:"timezone" gorm:"default:'UTC'"`
	Language    string      `json:"language" gorm:"default:'en'"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
	User        models.User `json:"user" gorm:"foreignKey:UserID"`
}

// LoginAttempt 登录尝试记录
type LoginAttempt struct {
	ID         uint         `json:"id" gorm:"primaryKey"`
	UserID     *uint        `json:"user_id"`
	Email      string       `json:"email"`
	IPAddress  string       `json:"ip_address"`
	UserAgent  string       `json:"user_agent"`
	Success    bool         `json:"success"`
	FailReason string       `json:"fail_reason"`
	CreatedAt  time.Time    `json:"created_at"`
	User       *models.User `json:"user,omitempty" gorm:"foreignKey:UserID"`
}

// RefreshToken 刷新令牌
type RefreshToken struct {
	ID              uint        `json:"id" gorm:"primaryKey"`
	UserID          uint        `json:"user_id" gorm:"not null;index:idx_refresh_tokens_session_active,priority:1"`
	Token           string      `json:"-" gorm:"uniqueIndex;not null"`
	SessionID       string      `json:"session_id" gorm:"size:128;not null;index;index:idx_refresh_tokens_session_active,priority:2"`
	ExpiresAt       time.Time   `json:"expires_at" gorm:"index:idx_refresh_tokens_session_active,priority:4"`
	Revoked         bool        `json:"revoked" gorm:"default:false;index:idx_refresh_tokens_session_active,priority:3"`
	RevokedAt       *time.Time  `json:"revoked_at"`
	RotatedAt       *time.Time  `json:"-" gorm:"index"`
	ReplacedByToken string      `json:"-" gorm:"size:64"`
	IPAddress       string      `json:"ip_address"`
	UserAgent       string      `json:"user_agent"`
	CreatedAt       time.Time   `json:"created_at"`
	User            models.User `json:"user" gorm:"foreignKey:UserID"`
}

// EmailVerification 邮箱验证
type EmailVerification struct {
	ID               uint        `json:"id" gorm:"primaryKey"`
	UserID           uint        `json:"user_id" gorm:"not null;index"`
	Email            string      `json:"email" gorm:"size:255;not null"`
	Token            string      `json:"-" gorm:"size:255;not null;uniqueIndex"`
	DeliverySecret   string      `json:"-" gorm:"type:text"`
	EmailDeliveredAt *time.Time  `json:"-"`
	Used             bool        `json:"used" gorm:"default:false"`
	ExpiresAt        time.Time   `json:"expires_at" gorm:"not null"`
	UsedAt           *time.Time  `json:"used_at"`
	CreatedAt        time.Time   `json:"created_at"`
	UpdatedAt        time.Time   `json:"updated_at"`
	User             models.User `json:"user" gorm:"foreignKey:UserID"`
}

// PasswordReset 密码重置
type PasswordReset struct {
	ID               uint        `json:"id" gorm:"primaryKey"`
	UserID           uint        `json:"user_id" gorm:"not null;index"`
	Email            string      `json:"email" gorm:"size:255;not null"`
	Token            string      `json:"-" gorm:"size:255;not null;uniqueIndex"`
	DeliverySecret   string      `json:"-" gorm:"type:text"`
	EmailDeliveredAt *time.Time  `json:"-"`
	Used             bool        `json:"used" gorm:"default:false"`
	ExpiresAt        time.Time   `json:"expires_at" gorm:"not null"`
	UsedAt           *time.Time  `json:"used_at"`
	CreatedAt        time.Time   `json:"created_at"`
	UpdatedAt        time.Time   `json:"updated_at"`
	User             models.User `json:"user" gorm:"foreignKey:UserID"`
}

// OTPCode OTP验证码
type OTPCode struct {
	ID        uint        `json:"id" gorm:"primaryKey"`
	UserID    uint        `json:"user_id" gorm:"not null"`
	Code      string      `json:"-" gorm:"not null"`
	Type      string      `json:"type" gorm:"not null"` // login, setup, backup
	ExpiresAt time.Time   `json:"expires_at"`
	Used      bool        `json:"used" gorm:"default:false"`
	UsedAt    *time.Time  `json:"used_at"`
	CreatedAt time.Time   `json:"created_at"`
	User      models.User `json:"user" gorm:"foreignKey:UserID"`
}

// RegisterRequest 注册请求
type RegisterRequest struct {
	Username        string `json:"username" binding:"required,min=3,max=50"`
	Email           string `json:"email" binding:"required,email"`
	Password        string `json:"password" binding:"required,min=8"`
	ConfirmPassword string `json:"confirm_password" binding:"required"`
	FirstName       string `json:"first_name" binding:"max=50"`
	LastName        string `json:"last_name" binding:"max=50"`
	Department      string `json:"department" binding:"max=100"`
	Position        string `json:"position" binding:"max=100"`
}

// LoginRequest 登录请求
type LoginRequest struct {
	Email          string `json:"email" binding:"required,email"`
	Password       string `json:"password" binding:"required"`
	OTPCode        string `json:"otp_code,omitempty"`
	DeviceToken    string `json:"-"`
	RememberDevice bool   `json:"remember_device,omitempty"`
	DeviceName     string `json:"device_name,omitempty"`
}

// RefreshTokenRequest 刷新令牌请求
type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// ChangePasswordRequest 修改密码请求
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required,min=8"`
}

// ForgotPasswordRequest 忘记密码请求
type ForgotPasswordRequest struct {
	Email string `json:"email" binding:"required,email"`
}

// ResetPasswordRequest 重置密码请求
type ResetPasswordRequest struct {
	Token       string `json:"token" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=8"`
}

// ResendVerificationRequest 重发验证邮件请求
type ResendVerificationRequest struct {
	Email string `json:"email" binding:"required,email"`
}

// UpdateProfileRequest 更新用户资料请求
type UpdateProfileRequest struct {
	FirstName   *string `json:"first_name,omitempty"`
	LastName    *string `json:"last_name,omitempty"`
	PhoneNumber *string `json:"phone_number,omitempty"`
	Avatar      *string `json:"avatar,omitempty"`
	Timezone    *string `json:"timezone,omitempty"`
	Language    *string `json:"language,omitempty"`
}

// VerifyEmailRequest 验证邮箱请求
type VerifyEmailRequest struct {
	Token string `json:"token" binding:"required"`
}

// EnableOTPRequest 启用OTP请求
type EnableOTPRequest struct {
	Password string `json:"password" binding:"required"`
}

// VerifyOTPRequest 验证OTP请求
type VerifyOTPRequest struct {
	Code string `json:"code" binding:"required,len=6"`
}

// AuthResponse 认证响应
type AuthResponse struct {
	User                   *UserInfo `json:"user"`
	AccessToken            string    `json:"access_token"`
	RefreshToken           string    `json:"refresh_token"`
	ExpiresIn              int64     `json:"expires_in"`
	TokenType              string    `json:"token_type"`
	TrustedDeviceToken     string    `json:"-"`
	TrustedDeviceExpiresAt time.Time `json:"-"`
}

// UserInfo 用户信息
type UserInfo struct {
	ID            uint         `json:"id"`
	Username      string       `json:"username"`
	Email         string       `json:"email"`
	Role          UserRole     `json:"role"`
	Status        UserStatus   `json:"status"`
	EmailVerified bool         `json:"email_verified"`
	OTPEnabled    bool         `json:"otp_enabled"`
	LastLoginAt   *time.Time   `json:"last_login_at"`
	Profile       *UserProfile `json:"profile,omitempty"`
}

// OTPSetupResponse OTP设置响应
type OTPSetupResponse struct {
	Secret      string   `json:"secret"`
	QRCode      string   `json:"qr_code"`
	BackupCodes []string `json:"backup_codes"`
}

// UserRepository 用户仓库接口
type UserRepository interface {
	Create(ctx context.Context, user *User) error
	GetByID(ctx context.Context, id uint) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	GetByUsername(ctx context.Context, username string) (*User, error)
	Update(ctx context.Context, user *User) error
	Delete(ctx context.Context, id uint) error
	List(ctx context.Context, offset, limit int) ([]*User, int64, error)
	UpdateLastLogin(ctx context.Context, userID uint, loginTime time.Time) error
	IncrementFailedLogin(ctx context.Context, userID uint) error
	ResetFailedLogin(ctx context.Context, userID uint) error
	ChangePasswordAndRevokeSessions(
		ctx context.Context,
		userID uint,
		passwordHash string,
		changedAt time.Time,
	) error
	ConfigureOTP(ctx context.Context, userID uint, secret, backupCodeHashes string, enabled bool) error
	ReplaceBackupCodes(ctx context.Context, userID uint, backupCodeHashes string) error
	ConsumeBackupCode(ctx context.Context, userID uint, code string) (bool, error)
}

// ProfileRepository 用户资料仓库接口
type ProfileRepository interface {
	Create(ctx context.Context, profile *UserProfile) error
	GetByUserID(ctx context.Context, userID uint) (*UserProfile, error)
	Update(ctx context.Context, profile *UserProfile) error
	Delete(ctx context.Context, userID uint) error
}

// TokenRepository 令牌仓库接口
type TokenRepository interface {
	// 创建刷新令牌
	CreateRefreshToken(ctx context.Context, token *RefreshToken) error
	// 根据令牌获取
	GetRefreshToken(ctx context.Context, token string) (*RefreshToken, error)
	// 获取可轮换令牌；刚完成轮换的旧令牌会在短暂恢复窗口内返回，
	// 以便客户端在响应丢失后安全重放同一个结果。
	GetRefreshTokenForRotation(ctx context.Context, token string) (*RefreshToken, error)
	// 撤销令牌
	RevokeRefreshToken(ctx context.Context, token string) error
	// 原子轮换刷新令牌：旧令牌条件撤销与新令牌创建同事务提交
	RotateRefreshToken(
		ctx context.Context,
		currentToken string,
		replacement *RefreshToken,
		rotatedAt time.Time,
	) error
	// 撤销用户所有令牌
	RevokeAllUserTokens(ctx context.Context, userID uint) error
	// 撤销一个登录会话的全部刷新令牌，使该会话签发的访问令牌立即失效
	RevokeSession(ctx context.Context, userID uint, sessionID string) error
	// 检查数据库中是否仍存在该会话的有效刷新令牌
	IsSessionActive(ctx context.Context, userID uint, sessionID string) (bool, error)
	// 清理过期令牌
	CleanupExpiredTokens(ctx context.Context) error
	// 创建邮箱验证
	CreateEmailVerification(ctx context.Context, verification *EmailVerification) error
	// 获取邮箱验证
	GetEmailVerification(ctx context.Context, token string) (*EmailVerification, error)
	// 使用邮箱验证
	UseEmailVerification(ctx context.Context, token string) error
	// 原子消费邮箱验证令牌并更新用户验证状态
	VerifyEmailWithToken(ctx context.Context, token string, verifiedAt time.Time) (uint, error)
	// 创建密码重置
	CreatePasswordReset(ctx context.Context, reset *PasswordReset) error
	// 获取密码重置
	GetPasswordReset(ctx context.Context, token string) (*PasswordReset, error)
	// 使用密码重置
	UsePasswordReset(ctx context.Context, token string) error
	// 原子消费密码重置令牌、更新密码并撤销现有会话
	ResetPasswordWithToken(
		ctx context.Context,
		token, passwordHash string,
		changedAt time.Time,
	) (uint, error)

	CreateOTPCode(ctx context.Context, otp *OTPCode) error
	GetOTPCode(ctx context.Context, userID uint, code string) (*OTPCode, error)
	UseOTPCode(ctx context.Context, userID uint, code string) error
	CleanupExpiredOTP(ctx context.Context) error
}

// LoginAttemptRepository 登录尝试仓库接口
type LoginAttemptRepository interface {
	Create(ctx context.Context, attempt *LoginAttempt) error
	GetRecentAttempts(ctx context.Context, email string, since time.Time) ([]*LoginAttempt, error)
	GetRecentFailedAttempts(ctx context.Context, email string, since time.Time) (int, error)
	CleanupOldAttempts(ctx context.Context, before time.Time) error
}

// LoginHistoryRepository 登录历史仓库接口
type LoginHistoryRepository interface {
	Create(ctx context.Context, history *models.LoginHistory) error
	RefreshSession(ctx context.Context, userID uint, sessionID, ipAddress, userAgent string, at time.Time) error
	EndSession(ctx context.Context, userID uint, sessionID string, status models.LoginStatus, reason string, at time.Time) error
	EndAllSessions(ctx context.Context, userID uint, status models.LoginStatus, reason string, at time.Time) error
}

// TrustedDeviceRepository 可信设备仓库接口
type TrustedDeviceRepository interface {
	GetByTokenHash(ctx context.Context, tokenHash string) (*models.OTPTrustedDevice, error)
	Create(ctx context.Context, device *models.OTPTrustedDevice) error
	Update(ctx context.Context, device *models.OTPTrustedDevice) error
	ListActiveDevices(ctx context.Context, userID uint) ([]*models.OTPTrustedDevice, error)
}

// EmailService 邮件服务接口
type EmailService interface {
	SendVerificationEmail(ctx context.Context, email, token string) error
	SendPasswordResetEmail(ctx context.Context, email, token string) error
	SendWelcomeEmail(ctx context.Context, email, username string) error
}

// OTPService OTP服务接口
type OTPService interface {
	GenerateSecret() (string, error)
	GenerateQRCode(secret, email string) (string, error)
	GenerateCode(secret string) (string, error)
	VerifyCode(secret, code string) bool
	GenerateBackupCodes() ([]string, error)
}

// PasswordService 密码服务接口
type PasswordService interface {
	HashPassword(password string) (string, error)
	VerifyPassword(hashedPassword, password string) error
	ValidatePassword(password string) error
	GenerateRandomPassword(length int) (string, error)
}

// EmailConfigService 邮箱配置服务接口
type EmailConfigService interface {
	IsEmailVerificationEnabled(ctx context.Context) (bool, error)
}

// AuthServiceInterface 认证服务接口
type AuthServiceInterface interface {
	// 用户注册
	Register(ctx context.Context, req *RegisterRequest, ipAddress, userAgent string) (*AuthResponse, error)
	// 用户登录
	Login(ctx context.Context, req *LoginRequest, ipAddress, userAgent string) (*AuthResponse, error)
	// 刷新令牌
	RefreshToken(ctx context.Context, req *RefreshTokenRequest, ipAddress, userAgent string) (*AuthResponse, error)
	// 登出
	Logout(ctx context.Context, refreshToken string) error
	// 登出所有设备
	LogoutAll(ctx context.Context, userID uint) error
	// 忘记密码
	ForgotPassword(ctx context.Context, email string) error
	// 重置密码
	ResetPassword(ctx context.Context, token, newPassword string) error
	// 验证邮箱
	VerifyEmail(ctx context.Context, token string) error
	// 重发验证邮件
	ResendVerification(ctx context.Context, email string) error
	// 更新用户资料
	UpdateProfile(ctx context.Context, userID uint, req *UpdateProfileRequest) error
	// 修改密码
	ChangePassword(ctx context.Context, userID uint, currentPassword, newPassword string) error
	// 启用OTP
	EnableOTP(ctx context.Context, userID uint, password string) (*OTPSetupResponse, error)
	// 禁用OTP
	DisableOTP(ctx context.Context, userID uint, password string) error
	// 验证OTP
	VerifyOTP(ctx context.Context, userID uint, code string) error
	// 生成备用代码
	GenerateBackupCodes(ctx context.Context, userID uint) ([]string, error)
}

// AuthService 认证服务
type AuthService struct {
	userRepo           UserRepository
	profileRepo        ProfileRepository
	tokenRepo          TokenRepository
	emailOutboxRepo    AuthEmailOutboxRepository
	loginAttemptRepo   LoginAttemptRepository
	loginHistoryRepo   LoginHistoryRepository
	trustedDeviceRepo  TrustedDeviceRepository
	configService      *services.ConfigService
	emailConfigService EmailConfigService
	otpService         OTPService
	passwordService    PasswordService
	jwtManager         JWTManager
	config             *AuthConfig
}

type AuthServiceOption func(*AuthService)

func WithAuthEmailOutboxRepository(repository AuthEmailOutboxRepository) AuthServiceOption {
	return func(service *AuthService) {
		service.emailOutboxRepo = repository
	}
}

// AuthConfig 认证配置
type AuthConfig struct {
	JWTSecret                string
	JWTRefreshSecret         string
	AccessTokenExpire        time.Duration
	RefreshTokenExpire       time.Duration
	EmailVerificationExpire  time.Duration
	PasswordResetExpire      time.Duration
	OTPExpire                time.Duration
	MaxFailedLogins          int
	LockoutDuration          time.Duration
	PasswordMinLength        int
	RequireEmailVerification bool
	EnableOTP                bool
	EnableRegistration       bool
}

// JWTManager JWT管理器接口
type JWTManager interface {
	GenerateTokenPair(userID uint, role UserRole, sessionID string) (accessToken, refreshToken string, err error)
	GenerateRefreshTokenPair(
		userID uint,
		role UserRole,
		sessionID, rotationSeed string,
		issuedAt time.Time,
	) (accessToken, refreshToken string, err error)
	VerifyAccessToken(token string) (*Claims, error)
	VerifyRefreshToken(token string) (*Claims, error)
	ParseTokenClaims(token string) (*Claims, error)
}

// Claims JWT声明
type Claims struct {
	UserID    uint     `json:"user_id"`
	Role      UserRole `json:"role"`
	Type      string   `json:"type"` // access, refresh
	SessionID string   `json:"sid"`
	Exp       int64    `json:"exp"`
	Iat       int64    `json:"iat"`
	Jti       string   `json:"jti"`
}

// NewAuthService 创建认证服务
func NewAuthService(
	userRepo UserRepository,
	profileRepo ProfileRepository,
	tokenRepo TokenRepository,
	loginAttemptRepo LoginAttemptRepository,
	loginHistoryRepo LoginHistoryRepository,
	trustedDeviceRepo TrustedDeviceRepository,
	configService *services.ConfigService,
	emailConfigService EmailConfigService,
	otpService OTPService,
	passwordService PasswordService,
	jwtManager JWTManager,
	config *AuthConfig,
	options ...AuthServiceOption,
) *AuthService {
	service := &AuthService{
		userRepo:           userRepo,
		profileRepo:        profileRepo,
		tokenRepo:          tokenRepo,
		loginAttemptRepo:   loginAttemptRepo,
		loginHistoryRepo:   loginHistoryRepo,
		trustedDeviceRepo:  trustedDeviceRepo,
		configService:      configService,
		emailConfigService: emailConfigService,
		otpService:         otpService,
		passwordService:    passwordService,
		jwtManager:         jwtManager,
		config:             config,
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

// Register 用户注册
func (s *AuthService) Register(ctx context.Context, req *RegisterRequest, ipAddress, userAgent string) (*AuthResponse, error) {
	// 检查是否允许注册
	if !s.config.EnableRegistration {
		return nil, errors.New("registration is disabled")
	}

	// 验证密码确认
	if req.Password != req.ConfirmPassword {
		return nil, errors.New("passwords do not match")
	}

	// 验证密码强度
	if err := s.passwordService.ValidatePassword(req.Password); err != nil {
		return nil, err
	}

	// 检查用户是否已存在
	if _, err := s.userRepo.GetByEmail(ctx, req.Email); err == nil {
		return nil, ErrUserExists
	}

	if _, err := s.userRepo.GetByUsername(ctx, req.Username); err == nil {
		return nil, ErrUserExists
	}

	// 检查邮箱验证是否启用
	emailVerificationEnabled, err := s.emailConfigService.IsEmailVerificationEnabled(ctx)
	if err != nil {
		// 如果无法获取配置，使用默认配置
		emailVerificationEnabled = s.config.RequireEmailVerification
	}

	// 哈希密码
	hashedPassword, err := s.passwordService.HashPassword(req.Password)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	// 创建用户
	user := &User{
		Username:          req.Username,
		Email:             req.Email,
		PasswordHash:      hashedPassword,
		Role:              RoleCustomer,
		Status:            StatusActive,
		EmailVerified:     !emailVerificationEnabled,
		PasswordChangedAt: timePtr(time.Now()),
	}

	if !emailVerificationEnabled {
		user.EmailVerifiedAt = timePtr(time.Now())
	}

	// 创建用户资料
	profile := &UserProfile{
		UserID:      user.ID,
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		DisplayName: strings.TrimSpace(req.FirstName + " " + req.LastName),
		Department:  req.Department,
		Position:    req.Position,
		Timezone:    "UTC",
		Language:    "en",
	}
	var verification *EmailVerification
	if emailVerificationEnabled {
		token, err := generateSecureToken(32)
		if err != nil {
			return nil, fmt.Errorf("failed to generate email verification token: %w", err)
		}
		verification = &EmailVerification{
			Email:     user.Email,
			Token:     token,
			ExpiresAt: time.Now().Add(s.config.EmailVerificationExpire),
		}
	}
	if s.emailOutboxRepo == nil {
		return nil, errors.New("durable authentication email Outbox is unavailable")
	}
	if err := s.emailOutboxRepo.Register(ctx, user, profile, verification); err != nil {
		return nil, fmt.Errorf("failed to register user: %w", err)
	}

	// 如果需要邮箱验证，返回用户信息但不生成令牌
	if emailVerificationEnabled {
		return &AuthResponse{
			User: s.buildUserInfo(user, profile),
		}, nil
	}

	sessionID, err := GenerateSecureToken(16)
	if err != nil {
		return nil, fmt.Errorf("failed to generate session id: %w", err)
	}
	// 访问令牌与刷新令牌必须绑定同一个持久化会话。先生成会话 ID，
	// 再签发令牌，旧的不含 sid 的令牌会按最新安全契约失效。
	accessToken, refreshToken, err := s.jwtManager.GenerateTokenPair(user.ID, user.Role, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate tokens: %w", err)
	}
	loginTime := time.Now()

	// 保存刷新令牌
	if err := s.saveRefreshToken(ctx, user.ID, refreshToken, sessionID, ipAddress, userAgent); err != nil {
		return nil, fmt.Errorf("failed to save refresh token: %w", err)
	}

	if err := s.recordLoginHistorySuccess(
		ctx,
		user,
		ipAddress,
		userAgent,
		sessionID,
		loginTime,
		determineLoginMethod(user, nil, false, false),
	); err != nil {
		_ = s.tokenRepo.RevokeSession(ctx, user.ID, sessionID)
		return nil, fmt.Errorf("failed to persist login session: %w", err)
	}

	return &AuthResponse{
		User:         s.buildUserInfo(user, profile),
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int64(s.config.AccessTokenExpire.Seconds()),
		TokenType:    "Bearer",
	}, nil
}

// Login 用户登录
func (s *AuthService) Login(ctx context.Context, req *LoginRequest, ipAddress, userAgent string) (*AuthResponse, error) {
	// 检查最近的失败登录次数
	if err := s.checkLoginAttempts(ctx, req.Email); err != nil {
		if auditErr := s.recordLoginAttempt(
			ctx,
			nil,
			req.Email,
			ipAddress,
			userAgent,
			false,
			"login attempt rejected",
		); auditErr != nil {
			return nil, fmt.Errorf("failed to persist rejected login audit: %w", auditErr)
		}
		return nil, err
	}

	// 获取用户
	user, err := s.userRepo.GetByEmail(ctx, req.Email)
	if err != nil {
		auditReason := "principal lookup unavailable"
		if errors.Is(err, ErrUserNotFound) {
			auditReason = "user not found"
		}
		if auditErr := s.recordLoginAttempt(
			ctx,
			nil,
			req.Email,
			ipAddress,
			userAgent,
			false,
			auditReason,
		); auditErr != nil {
			return nil, fmt.Errorf("failed to persist unknown-user login audit: %w", auditErr)
		}
		if !errors.Is(err, ErrUserNotFound) {
			return nil, fmt.Errorf("failed to load authentication principal: %w", err)
		}
		return nil, ErrInvalidCredentials
	}

	var (
		trustedDevice *models.OTPTrustedDevice
		deviceTrusted bool
	)

	if req.DeviceToken != "" && s.trustedDeviceRepo != nil {
		tokenHash := hashTrustedDeviceToken(req.DeviceToken)
		if tokenHash != "" {
			if device, deviceErr := s.trustedDeviceRepo.GetByTokenHash(ctx, tokenHash); deviceErr == nil && device != nil {
				if device.UserID == user.ID && !device.Revoked && device.ExpiresAt.After(time.Now()) {
					trustedDevice = device
					deviceTrusted = true
				} else if device.UserID == user.ID && device.ExpiresAt.Before(time.Now()) && !device.Revoked {
					device.Revoked = true
					if updateErr := s.trustedDeviceRepo.Update(ctx, device); updateErr != nil {
						fmt.Printf("Warning: failed to revoke expired trusted device: %v\n", updateErr)
					}
				}
			}
		}
	}

	otpValidated := deviceTrusted

	// 检查账户状态
	if statusErr := s.checkUserStatus(ctx, user); statusErr != nil {
		method := determineLoginMethod(user, req, deviceTrusted, otpValidated)
		if err := s.recordLoginAttempt(
			ctx,
			&user.ID,
			req.Email,
			ipAddress,
			userAgent,
			false,
			authLogReason(statusErr),
		); err != nil {
			return nil, fmt.Errorf("failed to persist account-state login audit: %w", err)
		}
		if err := s.recordLoginHistoryFailure(
			ctx,
			user,
			ipAddress,
			userAgent,
			method,
			authLogReason(statusErr),
			loginStatusFromError(statusErr),
		); err != nil {
			return nil, fmt.Errorf("failed to persist account-state login history: %w", err)
		}
		return nil, statusErr
	}

	// 验证密码
	if err := s.passwordService.VerifyPassword(user.PasswordHash, req.Password); err != nil {
		method := determineLoginMethod(user, req, deviceTrusted, otpValidated)
		if incrementErr := s.userRepo.IncrementFailedLogin(ctx, user.ID); incrementErr != nil {
			return nil, fmt.Errorf("failed to persist failed-login counter: %w", incrementErr)
		}
		if auditErr := s.recordLoginAttempt(
			ctx,
			&user.ID,
			req.Email,
			ipAddress,
			userAgent,
			false,
			"invalid password",
		); auditErr != nil {
			return nil, fmt.Errorf("failed to persist password-failure audit: %w", auditErr)
		}
		if historyErr := s.recordLoginHistoryFailure(
			ctx,
			user,
			ipAddress,
			userAgent,
			method,
			"invalid password",
			models.LoginStatusFailed,
		); historyErr != nil {
			return nil, fmt.Errorf("failed to persist password-failure history: %w", historyErr)
		}
		return nil, ErrInvalidCredentials
	}

	// 检查是否需要OTP验证
	if user.OTPEnabled && !deviceTrusted {
		if req.OTPCode == "" {
			method := determineLoginMethod(user, req, deviceTrusted, otpValidated)
			if err := s.recordLoginAttempt(
				ctx,
				&user.ID,
				req.Email,
				ipAddress,
				userAgent,
				false,
				"otp required",
			); err != nil {
				return nil, fmt.Errorf("failed to persist OTP-required audit: %w", err)
			}
			if err := s.recordLoginHistoryFailure(
				ctx,
				user,
				ipAddress,
				userAgent,
				method,
				"otp required",
				models.LoginStatusFailed,
			); err != nil {
				return nil, fmt.Errorf("failed to persist OTP-required history: %w", err)
			}
			return nil, errors.New("OTP code required")
		}

		if !s.otpService.VerifyCode(user.OTPSecret, req.OTPCode) {
			// 检查是否是备用码
			consumed, consumeErr := s.userRepo.ConsumeBackupCode(ctx, user.ID, req.OTPCode)
			if consumeErr != nil {
				return nil, fmt.Errorf("failed to consume backup code: %w", consumeErr)
			}
			if !consumed {
				method := determineLoginMethod(user, req, deviceTrusted, false)
				if err := s.recordLoginAttempt(
					ctx,
					&user.ID,
					req.Email,
					ipAddress,
					userAgent,
					false,
					"invalid OTP",
				); err != nil {
					return nil, fmt.Errorf("failed to persist invalid-OTP audit: %w", err)
				}
				if err := s.recordLoginHistoryFailure(
					ctx,
					user,
					ipAddress,
					userAgent,
					method,
					"invalid OTP",
					models.LoginStatusFailed,
				); err != nil {
					return nil, fmt.Errorf("failed to persist invalid-OTP history: %w", err)
				}
				return nil, ErrInvalidOTP
			}
			otpValidated = true
		}
		otpValidated = true
	}

	// 获取用户资料
	profile, _ := s.profileRepo.GetByUserID(ctx, user.ID)

	// 重置失败登录计数
	if err := s.userRepo.ResetFailedLogin(ctx, user.ID); err != nil {
		return nil, fmt.Errorf("failed to reset failed-login counter: %w", err)
	}

	// 更新最后登录时间
	now := time.Now()
	trustedDeviceTTL := s.getTrustedDeviceTTL()
	maxTrustedDevices := s.getTrustedDeviceLimit()
	if err := s.userRepo.UpdateLastLogin(ctx, user.ID, now); err != nil {
		return nil, fmt.Errorf("failed to update last login: %w", err)
	}

	// 记录成功登录
	if err := s.recordLoginAttempt(
		ctx,
		&user.ID,
		req.Email,
		ipAddress,
		userAgent,
		true,
		"",
	); err != nil {
		return nil, fmt.Errorf("failed to persist successful login audit: %w", err)
	}

	sessionID, err := GenerateSecureToken(16)
	if err != nil {
		return nil, fmt.Errorf("failed to generate session id: %w", err)
	}
	accessToken, refreshToken, err := s.jwtManager.GenerateTokenPair(user.ID, user.Role, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate tokens: %w", err)
	}

	// 保存刷新令牌
	if err := s.saveRefreshToken(ctx, user.ID, refreshToken, sessionID, ipAddress, userAgent); err != nil {
		return nil, fmt.Errorf("failed to save refresh token: %w", err)
	}

	loginMethod := determineLoginMethod(user, req, deviceTrusted, otpValidated)
	if err := s.recordLoginHistorySuccess(
		ctx,
		user,
		ipAddress,
		userAgent,
		sessionID,
		now,
		loginMethod,
	); err != nil {
		_ = s.tokenRepo.RevokeSession(ctx, user.ID, sessionID)
		return nil, fmt.Errorf("failed to persist login session: %w", err)
	}

	var trustedDeviceToken string
	if s.trustedDeviceRepo != nil {
		if deviceTrusted && trustedDevice != nil {
			trustedDevice.LastUsedAt = now
			trustedDevice.LastIP = ipAddress
			trustedDevice.UserAgent = userAgent
			if req.RememberDevice {
				trustedDevice.ExpiresAt = now.Add(trustedDeviceTTL)
				trustedDeviceToken = req.DeviceToken
				if req.DeviceName != "" {
					trustedDevice.DeviceName = req.DeviceName
				}
			}
			if err := s.trustedDeviceRepo.Update(ctx, trustedDevice); err != nil {
				fmt.Printf("Warning: failed to update trusted device: %v\n", err)
			}
		} else if req.RememberDevice && (otpValidated || !user.OTPEnabled) {
			deviceToken, tokenErr := GenerateSecureToken(32)
			if tokenErr != nil {
				fmt.Printf("Warning: failed to generate trusted device token: %v\n", tokenErr)
			} else {
				hash := hashTrustedDeviceToken(deviceToken)
				device := &models.OTPTrustedDevice{
					UserID:          user.ID,
					DeviceTokenHash: hash,
					DeviceName:      resolveTrustedDeviceName(req.DeviceName, userAgent),
					LastUsedAt:      now,
					LastIP:          ipAddress,
					UserAgent:       userAgent,
					ExpiresAt:       now.Add(trustedDeviceTTL),
				}
				if err := s.trustedDeviceRepo.Create(ctx, device); err != nil {
					fmt.Printf("Warning: failed to persist trusted device: %v\n", err)
				} else {
					trustedDeviceToken = deviceToken
				}
			}
		}

		if maxTrustedDevices > 0 {
			s.enforceTrustedDeviceQuota(ctx, user.ID, maxTrustedDevices, now)
		}
	}

	return &AuthResponse{
		User:                   s.buildUserInfo(user, profile),
		AccessToken:            accessToken,
		RefreshToken:           refreshToken,
		ExpiresIn:              int64(s.config.AccessTokenExpire.Seconds()),
		TokenType:              "Bearer",
		TrustedDeviceToken:     trustedDeviceToken,
		TrustedDeviceExpiresAt: now.Add(trustedDeviceTTL),
	}, nil
}

// RefreshToken 刷新令牌
func (s *AuthService) RefreshToken(ctx context.Context, req *RefreshTokenRequest, ipAddress, userAgent string) (*AuthResponse, error) {
	return s.refreshToken(ctx, req, ipAddress, userAgent, true)
}

func (s *AuthService) refreshToken(
	ctx context.Context,
	req *RefreshTokenRequest,
	ipAddress, userAgent string,
	allowConcurrentReplay bool,
) (*AuthResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// 验证刷新令牌
	claims, err := s.jwtManager.VerifyRefreshToken(req.RefreshToken)
	if err != nil {
		return nil, ErrInvalidToken
	}

	// 刚完成轮换的旧令牌在短暂恢复窗口内仍可重放相同结果。数据库只
	// 保存替代令牌摘要和轮换时间，绝不保存可用的 bearer 明文。
	tokenRecord, err := s.tokenRepo.GetRefreshTokenForRotation(ctx, req.RefreshToken)
	if err != nil {
		return nil, ErrInvalidToken
	}
	sessionID := tokenRecord.SessionID
	if sessionID == "" || claims.SessionID != sessionID || claims.UserID != tokenRecord.UserID {
		return nil, ErrInvalidToken
	}
	sessionActive, err := s.tokenRepo.IsSessionActive(ctx, tokenRecord.UserID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to verify login session: %w", err)
	}
	if !sessionActive {
		return nil, ErrInvalidToken
	}

	// 获取用户
	user, err := s.userRepo.GetByID(ctx, claims.UserID)
	if err != nil {
		return nil, ErrUserNotFound
	}

	// 检查用户状态
	if err := s.checkUserStatus(ctx, user); err != nil {
		return nil, err
	}
	if !user.Role.IsValid() || user.Role != claims.Role {
		_ = s.tokenRepo.RevokeSession(ctx, tokenRecord.UserID, sessionID)
		return nil, ErrInvalidToken
	}
	// 密码变更会使此前签发的所有凭据失效。这里使用数据库中刷新令牌的
	// 高精度创建时间，而不是只有秒精度的 JWT iat，避免同一秒内修改密码
	// 时旧刷新令牌重新签发可用的访问令牌。
	if user.PasswordChangedAt != nil && !tokenRecord.CreatedAt.After(*user.PasswordChangedAt) {
		_ = s.tokenRepo.RevokeSession(ctx, tokenRecord.UserID, sessionID)
		return nil, ErrInvalidToken
	}

	issuedAt := time.Now().UTC().Truncate(time.Second)
	if tokenRecord.Revoked {
		if tokenRecord.RotatedAt == nil ||
			time.Since(*tokenRecord.RotatedAt) < 0 ||
			time.Since(*tokenRecord.RotatedAt) > refreshRotationReplayWindow {
			return nil, ErrInvalidToken
		}
		issuedAt = tokenRecord.RotatedAt.UTC().Truncate(time.Second)
	}

	accessToken, refreshToken, err := s.jwtManager.GenerateRefreshTokenPair(
		user.ID,
		user.Role,
		sessionID,
		req.RefreshToken,
		issuedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate tokens: %w", err)
	}

	if tokenRecord.Revoked {
		if tokenRecord.ReplacedByToken != bearerTokenDigest("refresh-token", refreshToken) {
			return nil, ErrInvalidToken
		}
	} else {
		// Check cancellation immediately before the irreversible transaction.
		// Cancellation during the transaction is also observed by GORM and rolls
		// both the revocation and replacement insertion back.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		replacement, err := s.newRefreshTokenRecordAt(
			user.ID,
			refreshToken,
			sessionID,
			ipAddress,
			userAgent,
			issuedAt,
		)
		if err != nil {
			return nil, err
		}
		if err := s.tokenRepo.RotateRefreshToken(
			ctx,
			req.RefreshToken,
			replacement,
			issuedAt,
		); err != nil {
			if allowConcurrentReplay && errors.Is(err, ErrInvalidToken) && ctx.Err() == nil {
				// Another request may have won the conditional rotation. Reload
				// its persisted timestamp and reproduce the exact same pair.
				return s.refreshToken(ctx, req, ipAddress, userAgent, false)
			}
			return nil, fmt.Errorf("failed to rotate refresh token: %w", err)
		}
	}

	if s.loginHistoryRepo != nil && sessionID != "" {
		if err := s.loginHistoryRepo.RefreshSession(ctx, user.ID, sessionID, ipAddress, userAgent, time.Now()); err != nil {
			return nil, fmt.Errorf("failed to persist refresh session audit: %w", err)
		}
	} else {
		return nil, errors.New("login session repository is unavailable")
	}

	// 获取用户资料
	if s.profileRepo == nil {
		return nil, errors.New("authentication profile repository is unavailable")
	}
	profile, profileErr := s.profileRepo.GetByUserID(ctx, user.ID)
	if profileErr != nil && !errors.Is(profileErr, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("failed to load authentication profile: %w", profileErr)
	}

	return &AuthResponse{
		User:         s.buildUserInfo(user, profile),
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int64(s.config.AccessTokenExpire.Seconds()),
		TokenType:    "Bearer",
	}, nil
}

// Logout 用户登出
func (s *AuthService) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}

	var (
		userID    uint
		sessionID string
	)

	if tokenRecord, err := s.tokenRepo.GetRefreshToken(ctx, refreshToken); err == nil {
		userID = tokenRecord.UserID
		sessionID = tokenRecord.SessionID
	}

	if userID != 0 && sessionID != "" {
		if err := s.tokenRepo.RevokeSession(ctx, userID, sessionID); err != nil {
			return err
		}
	} else {
		// Invalid and already-revoked refresh tokens keep logout idempotent without
		// allowing unsigned ParseTokenClaims data to revoke another user's session.
		if err := s.tokenRepo.RevokeRefreshToken(ctx, refreshToken); err != nil &&
			!errors.Is(err, ErrInvalidToken) {
			return err
		}
		return nil
	}
	if s.loginHistoryRepo != nil && userID != 0 && sessionID != "" {
		if err := s.loginHistoryRepo.EndSession(ctx, userID, sessionID, models.LoginStatusSuccess, "", time.Now()); err != nil {
			fmt.Printf("Warning: failed to mark session logout: %v\n", err)
		}
	}
	return nil
}

// LogoutAll 登出所有设备
func (s *AuthService) LogoutAll(ctx context.Context, userID uint) error {
	if err := s.tokenRepo.RevokeAllUserTokens(ctx, userID); err != nil {
		return err
	}
	if s.loginHistoryRepo != nil {
		if err := s.loginHistoryRepo.EndAllSessions(ctx, userID, models.LoginStatusExpired, "logout_all", time.Now()); err != nil {
			fmt.Printf("Warning: failed to end all sessions for user %d: %v\n", userID, err)
		}
	}
	return nil
}

// ForgotPassword 忘记密码
func (s *AuthService) ForgotPassword(ctx context.Context, email string) error {
	// 查找用户
	user, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		// 为了安全，即使用户不存在也返回成功
		return nil
	}

	// 生成重置令牌
	token, err := generateSecureToken(32)
	if err != nil {
		return fmt.Errorf("failed to generate reset token: %w", err)
	}

	// 创建密码重置记录
	reset := &PasswordReset{
		UserID:    user.ID,
		Email:     user.Email,
		Token:     token,
		ExpiresAt: time.Now().Add(s.config.PasswordResetExpire),
	}

	if s.emailOutboxRepo == nil {
		return errors.New("durable authentication email Outbox is unavailable")
	}
	err = s.emailOutboxRepo.QueuePasswordReset(ctx, reset)
	if err != nil {
		return fmt.Errorf("failed to create password reset: %w", err)
	}

	return nil
}

// ResetPassword 重置密码
func (s *AuthService) ResetPassword(ctx context.Context, token, newPassword string) error {
	// 验证令牌
	reset, err := s.tokenRepo.GetPasswordReset(ctx, token)
	if err != nil {
		return ErrInvalidToken
	}

	if reset.Used || time.Now().After(reset.ExpiresAt) {
		return ErrInvalidToken
	}

	// 验证新密码
	err = s.passwordService.ValidatePassword(newPassword)
	if err != nil {
		return err
	}

	// 哈希新密码
	hashedPassword, err := s.passwordService.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	// 条件消费令牌、更新密码与撤销会话在同一事务中完成。
	if _, err := s.tokenRepo.ResetPasswordWithToken(
		ctx,
		token,
		hashedPassword,
		time.Now(),
	); err != nil {
		if errors.Is(err, ErrInvalidToken) {
			return ErrInvalidToken
		}
		return fmt.Errorf("failed to reset password: %w", err)
	}

	return nil
}

// VerifyEmail 验证邮箱
func (s *AuthService) VerifyEmail(ctx context.Context, token string) error {
	if s.emailOutboxRepo == nil {
		return errors.New("durable authentication email Outbox is unavailable")
	}

	// 条件消费令牌、更新用户状态与欢迎邮件意图在同一事务中完成。
	if _, err := s.emailOutboxRepo.VerifyEmailAndQueueWelcome(ctx, token, time.Now()); err != nil {
		if errors.Is(err, ErrInvalidToken) {
			return ErrInvalidToken
		}
		return fmt.Errorf("failed to verify email: %w", err)
	}

	return nil
}

// ResendVerification 重发验证邮件
func (s *AuthService) ResendVerification(ctx context.Context, email string) error {
	// 查找用户
	user, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		// 防止通过响应差异枚举已注册邮箱。
		return nil
	}

	// 检查是否已验证
	if user.EmailVerified {
		return nil
	}

	token, err := generateSecureToken(32)
	if err != nil {
		return fmt.Errorf("failed to generate email verification token: %w", err)
	}
	verification := &EmailVerification{
		UserID:    user.ID,
		Email:     user.Email,
		Token:     token,
		ExpiresAt: time.Now().Add(s.config.EmailVerificationExpire),
	}
	if s.emailOutboxRepo == nil {
		return errors.New("durable authentication email Outbox is unavailable")
	}
	if err := s.emailOutboxRepo.QueueEmailVerification(ctx, verification, "resend"); err != nil {
		return fmt.Errorf("failed to queue verification email: %w", err)
	}

	return nil
}

// UpdateProfile 更新用户资料
func (s *AuthService) UpdateProfile(ctx context.Context, userID uint, req *UpdateProfileRequest) error {
	// 获取用户资料
	profile, err := s.profileRepo.GetByUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get profile: %w", err)
	}

	// 更新字段
	if req.FirstName != nil {
		profile.FirstName = *req.FirstName
	}
	if req.LastName != nil {
		profile.LastName = *req.LastName
	}
	if req.PhoneNumber != nil {
		profile.Phone = *req.PhoneNumber
	}
	if req.Avatar != nil {
		profile.Avatar = *req.Avatar
	}
	if req.Timezone != nil {
		profile.Timezone = *req.Timezone
	}
	if req.Language != nil {
		profile.Language = *req.Language
	}

	// 更新显示名称
	if profile.FirstName != "" || profile.LastName != "" {
		profile.DisplayName = strings.TrimSpace(profile.FirstName + " " + profile.LastName)
	}

	err = s.profileRepo.Update(ctx, profile)
	if err != nil {
		return fmt.Errorf("failed to update profile: %w", err)
	}

	return nil
}

// ChangePassword 修改密码
func (s *AuthService) ChangePassword(ctx context.Context, userID uint, currentPassword, newPassword string) error {
	// 获取用户
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	// 验证当前密码
	err = s.passwordService.VerifyPassword(user.PasswordHash, currentPassword)
	if err != nil {
		return ErrInvalidCredentials
	}

	// 验证新密码
	err = s.passwordService.ValidatePassword(newPassword)
	if err != nil {
		return err
	}

	// 哈希新密码
	hashedPassword, err := s.passwordService.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	changedAt := time.Now()
	if err := s.userRepo.ChangePasswordAndRevokeSessions(
		ctx,
		user.ID,
		hashedPassword,
		changedAt,
	); err != nil {
		return fmt.Errorf("failed to change password and revoke sessions: %w", err)
	}

	return nil
}

// EnableOTP 启用OTP
func (s *AuthService) EnableOTP(ctx context.Context, userID uint, password string) (*OTPSetupResponse, error) {
	// 获取用户
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	// 检查是否已启用OTP
	if user.OTPEnabled {
		return nil, errors.New("OTP already enabled")
	}

	if password == "" {
		return nil, ErrInvalidCredentials
	}

	// 验证密码
	if err := s.passwordService.VerifyPassword(user.PasswordHash, password); err != nil {
		return nil, ErrInvalidCredentials
	}

	// 生成OTP密钥
	secret, err := s.otpService.GenerateSecret()
	if err != nil {
		return nil, fmt.Errorf("failed to generate OTP secret: %w", err)
	}

	// 生成QR码
	qrCode, err := s.otpService.GenerateQRCode(secret, user.Email)
	if err != nil {
		return nil, fmt.Errorf("failed to generate QR code: %w", err)
	}

	// 生成备用码
	backupCodes, err := s.otpService.GenerateBackupCodes()
	if err != nil {
		return nil, fmt.Errorf("failed to generate backup codes: %w", err)
	}
	backupCodeHashes, err := hashBackupCodes(backupCodes)
	if err != nil {
		return nil, fmt.Errorf("failed to protect backup codes: %w", err)
	}

	// 更新用户OTP设置
	if err := s.userRepo.ConfigureOTP(
		ctx,
		user.ID,
		secret,
		backupCodeHashes,
		true,
	); err != nil {
		return nil, fmt.Errorf("failed to update user: %w", err)
	}

	return &OTPSetupResponse{
		Secret:      secret,
		QRCode:      qrCode,
		BackupCodes: backupCodes,
	}, nil
}

// DisableOTP 禁用OTP
func (s *AuthService) DisableOTP(ctx context.Context, userID uint, password string) error {
	// 获取用户
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	// 验证密码
	err = s.passwordService.VerifyPassword(user.PasswordHash, password)
	if err != nil {
		return ErrInvalidCredentials
	}

	// 禁用OTP并清除所有静态凭据。
	if err := s.userRepo.ConfigureOTP(ctx, user.ID, "", "", false); err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}

	return nil
}

// VerifyOTP 验证OTP
func (s *AuthService) VerifyOTP(ctx context.Context, userID uint, code string) error {
	// 获取用户
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	// 检查是否启用OTP
	if !user.OTPEnabled {
		return errors.New("OTP not enabled")
	}

	// 验证OTP码
	if s.otpService.VerifyCode(user.OTPSecret, code) {
		return nil
	}

	// 检查备用码
	consumed, err := s.userRepo.ConsumeBackupCode(ctx, user.ID, code)
	if err != nil {
		return fmt.Errorf("failed to consume backup code: %w", err)
	}
	if consumed {
		return nil
	}

	return ErrInvalidOTP
}

// GenerateBackupCodes 生成备用代码
func (s *AuthService) GenerateBackupCodes(ctx context.Context, userID uint) ([]string, error) {
	// 获取用户
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	// 检查是否启用OTP
	if !user.OTPEnabled {
		return nil, errors.New("OTP not enabled")
	}

	// 生成新的备用码
	backupCodes, err := s.otpService.GenerateBackupCodes()
	if err != nil {
		return nil, fmt.Errorf("failed to generate backup codes: %w", err)
	}
	backupCodeHashes, err := hashBackupCodes(backupCodes)
	if err != nil {
		return nil, fmt.Errorf("failed to protect backup codes: %w", err)
	}

	// 更新用户备用码
	if err := s.userRepo.ReplaceBackupCodes(ctx, user.ID, backupCodeHashes); err != nil {
		return nil, fmt.Errorf("failed to update user: %w", err)
	}

	return backupCodes, nil
}

// 辅助方法

func (s *AuthService) checkLoginAttempts(ctx context.Context, email string) error {
	if s.loginAttemptRepo == nil {
		return errors.New("login attempt repository is unavailable")
	}
	since := time.Now().Add(-time.Hour) // 检查最近1小时的尝试
	failedCount, err := s.loginAttemptRepo.GetRecentFailedAttempts(ctx, email, since)
	if err != nil {
		return err
	}

	if failedCount >= s.config.MaxFailedLogins {
		return errors.New("too many failed login attempts")
	}

	return nil
}

func (s *AuthService) checkUserStatus(ctx context.Context, user *User) error {
	if err := validateUserAccessState(user, time.Now()); err != nil {
		return err
	}

	// 动态获取邮箱验证配置
	emailVerificationEnabled, err := s.emailConfigService.IsEmailVerificationEnabled(ctx)
	if err != nil {
		// 如果无法获取配置，使用默认配置
		emailVerificationEnabled = s.config.RequireEmailVerification
	}

	if emailVerificationEnabled && !user.EmailVerified {
		return ErrEmailNotVerified
	}

	return nil
}

func validateUserAccessState(user *User, now time.Time) error {
	if user == nil {
		return ErrUserNotFound
	}

	switch user.Status {
	case StatusActive:
	case StatusInactive:
		return ErrAccountInactive
	case StatusSuspended:
		return ErrAccountSuspended
	case StatusDeleted:
		return ErrAccountDeleted
	default:
		return ErrInvalidAccountState
	}

	if user.isLockedAt(now) {
		return ErrAccountLocked
	}
	return nil
}

func (s *AuthService) recordLoginAttempt(
	ctx context.Context,
	userID *uint,
	email, ipAddress, userAgent string,
	success bool,
	failReason string,
) error {
	if s.loginAttemptRepo == nil {
		return errors.New("login attempt repository is unavailable")
	}
	attempt := &LoginAttempt{
		UserID:     userID,
		Email:      email,
		IPAddress:  ipAddress,
		UserAgent:  userAgent,
		Success:    success,
		FailReason: failReason,
	}
	return s.loginAttemptRepo.Create(ctx, attempt)
}

func (s *AuthService) saveRefreshToken(ctx context.Context, userID uint, token, sessionID, ipAddress, userAgent string) error {
	refreshToken, err := s.newRefreshTokenRecord(
		userID,
		token,
		sessionID,
		ipAddress,
		userAgent,
	)
	if err != nil {
		return err
	}
	return s.tokenRepo.CreateRefreshToken(ctx, refreshToken)
}

func (s *AuthService) newRefreshTokenRecord(
	userID uint,
	token, sessionID, ipAddress, userAgent string,
) (*RefreshToken, error) {
	return s.newRefreshTokenRecordAt(
		userID,
		token,
		sessionID,
		ipAddress,
		userAgent,
		time.Now(),
	)
}

func (s *AuthService) newRefreshTokenRecordAt(
	userID uint,
	token, sessionID, ipAddress, userAgent string,
	issuedAt time.Time,
) (*RefreshToken, error) {
	sessionID = strings.TrimSpace(sessionID)
	if userID == 0 || sessionID == "" || len(sessionID) > 128 || issuedAt.IsZero() {
		return nil, errors.New("valid user and session identifiers are required")
	}
	return &RefreshToken{
		UserID:    userID,
		Token:     token,
		SessionID: sessionID,
		ExpiresAt: issuedAt.Add(s.config.RefreshTokenExpire),
		IPAddress: ipAddress,
		UserAgent: userAgent,
		CreatedAt: issuedAt,
	}, nil
}

func (s *AuthService) recordLoginHistorySuccess(
	ctx context.Context,
	user *User,
	ipAddress, userAgent, sessionID string,
	loginTime time.Time,
	method models.LoginMethod,
) error {
	if s.loginHistoryRepo == nil || user == nil {
		return errors.New("login session repository is unavailable")
	}

	deviceType, operatingSystem, browser := extractDeviceContext(userAgent)

	history := &models.LoginHistory{
		UserID:          user.ID,
		Username:        user.Username,
		Email:           user.Email,
		IPAddress:       ipAddress,
		UserAgent:       userAgent,
		LoginTime:       loginTime,
		LastActivityAt:  &loginTime,
		SessionID:       sessionID,
		LoginStatus:     models.LoginStatusSuccess,
		LoginMethod:     method,
		DeviceType:      deviceType,
		OperatingSystem: operatingSystem,
		Browser:         browser,
		IsActive:        true,
	}

	return s.loginHistoryRepo.Create(ctx, history)
}

func (s *AuthService) recordLoginHistoryFailure(
	ctx context.Context,
	user *User,
	ipAddress, userAgent string,
	method models.LoginMethod,
	reason string,
	status models.LoginStatus,
) error {
	if s.loginHistoryRepo == nil || user == nil {
		return errors.New("login session repository is unavailable")
	}

	loginTime := time.Now()
	deviceType, operatingSystem, browser := extractDeviceContext(userAgent)

	history := &models.LoginHistory{
		UserID:          user.ID,
		Username:        user.Username,
		Email:           user.Email,
		IPAddress:       ipAddress,
		UserAgent:       userAgent,
		LoginTime:       loginTime,
		LastActivityAt:  &loginTime,
		LoginStatus:     status,
		LoginMethod:     method,
		FailureReason:   reason,
		DeviceType:      deviceType,
		OperatingSystem: operatingSystem,
		Browser:         browser,
		IsActive:        false,
	}

	return s.loginHistoryRepo.Create(ctx, history)
}

func loginStatusFromError(err error) models.LoginStatus {
	switch err {
	case ErrAccountLocked:
		return models.LoginStatusBlocked
	case ErrOTPExpired:
		return models.LoginStatusExpired
	default:
		return models.LoginStatusFailed
	}
}

func determineLoginMethod(user *User, req *LoginRequest, deviceTrusted bool, otpValidated bool) models.LoginMethod {
	if deviceTrusted {
		return models.LoginMethodPasswordTrusted
	}

	if user != nil && user.OTPEnabled {
		if otpValidated {
			return models.LoginMethodPasswordOTP
		}
		if req != nil && req.OTPCode != "" {
			return models.LoginMethodPasswordOTP
		}
		return models.LoginMethodOTPRequired
	}

	return models.LoginMethodPassword
}

func extractDeviceContext(userAgent string) (deviceType, operatingSystem, browser string) {
	if userAgent == "" {
		return "unknown", "unknown", "unknown"
	}

	ua := strings.ToLower(userAgent)

	switch {
	case strings.Contains(ua, "mobile"):
		deviceType = "mobile"
	case strings.Contains(ua, "tablet"):
		deviceType = "tablet"
	case strings.Contains(ua, "iphone") || strings.Contains(ua, "ipad"):
		deviceType = "mobile"
	default:
		deviceType = "desktop"
	}

	switch {
	case strings.Contains(ua, "windows"):
		operatingSystem = "Windows"
	case strings.Contains(ua, "mac os") || strings.Contains(ua, "macintosh"):
		operatingSystem = "macOS"
	case strings.Contains(ua, "android"):
		operatingSystem = "Android"
	case strings.Contains(ua, "iphone") || strings.Contains(ua, "ipad") || strings.Contains(ua, "ios"):
		operatingSystem = "iOS"
	case strings.Contains(ua, "linux"):
		operatingSystem = "Linux"
	default:
		operatingSystem = "Unknown"
	}

	switch {
	case strings.Contains(ua, "chrome") && !strings.Contains(ua, "edg/"):
		browser = "Chrome"
	case strings.Contains(ua, "safari") && !strings.Contains(ua, "chrome"):
		browser = "Safari"
	case strings.Contains(ua, "firefox"):
		browser = "Firefox"
	case strings.Contains(ua, "edg/"):
		browser = "Edge"
	case strings.Contains(ua, "opera") || strings.Contains(ua, "opr/"):
		browser = "Opera"
	default:
		browser = "Unknown"
	}

	return
}

func (s *AuthService) getTrustedDeviceTTL() time.Duration {
	if s.configService != nil {
		if hours, err := s.configService.GetConfigInt(services.KeyTrustedDeviceTTLHours); err == nil {
			if hours > 0 {
				return time.Duration(hours) * time.Hour
			}
		}
	}
	return defaultTrustedDeviceTTL
}

func (s *AuthService) getTrustedDeviceLimit() int {
	if s.configService != nil {
		if limit, err := s.configService.GetConfigInt(services.KeyTrustedDeviceMaxPerUser); err == nil {
			if limit < 0 {
				return defaultTrustedDeviceMaxPerUser
			}
			return limit
		}
	}
	return defaultTrustedDeviceMaxPerUser
}

func (s *AuthService) enforceTrustedDeviceQuota(ctx context.Context, userID uint, maxDevices int, now time.Time) {
	if maxDevices <= 0 || s.trustedDeviceRepo == nil {
		return
	}

	devices, err := s.trustedDeviceRepo.ListActiveDevices(ctx, userID)
	if err != nil {
		fmt.Printf("Warning: failed to load trusted devices for pruning: %v\n", err)
		return
	}

	if len(devices) <= maxDevices {
		return
	}

	for _, device := range devices[maxDevices:] {
		device.Revoked = true
		device.ExpiresAt = now
		if err := s.trustedDeviceRepo.Update(ctx, device); err != nil {
			fmt.Printf("Warning: failed to revoke trusted device %d: %v\n", device.ID, err)
		}
	}
}

func hashTrustedDeviceToken(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func resolveTrustedDeviceName(providedName, userAgent string) string {
	name := strings.TrimSpace(providedName)
	if name != "" {
		return name
	}

	deviceType, operatingSystem, browser := extractDeviceContext(userAgent)
	capitalize := func(value string) string {
		if value == "" {
			return value
		}
		runes := []rune(value)
		runes[0] = unicode.ToUpper(runes[0])
		return string(runes)
	}

	parts := []string{}
	if deviceType != "unknown" {
		parts = append(parts, capitalize(deviceType))
	}
	if operatingSystem != "Unknown" {
		parts = append(parts, operatingSystem)
	}
	if browser != "Unknown" {
		parts = append(parts, browser)
	}

	if len(parts) == 0 {
		return "Trusted Device"
	}

	return strings.Join(parts, " - ")
}

func (s *AuthService) buildUserInfo(user *User, profile *UserProfile) *UserInfo {
	userInfo := &UserInfo{
		ID:            user.ID,
		Username:      user.Username,
		Email:         user.Email,
		Role:          user.Role,
		Status:        user.Status,
		EmailVerified: user.EmailVerified,
		OTPEnabled:    user.OTPEnabled,
		LastLoginAt:   user.LastLoginAt,
	}

	if profile != nil {
		userInfo.Profile = profile
	}

	return userInfo
}

// 工具函数

func generateSecureToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(bytes), nil
}

func timePtr(t time.Time) *time.Time {
	return &t
}

// HasPermission 检查用户是否有指定权限
func (u *User) HasPermission(requiredRole UserRole) bool {
	roleHierarchy := map[UserRole]int{
		RoleCustomer:   1,
		RoleAgent:      2,
		RoleSupervisor: 3,
		RoleAdmin:      4,
	}

	userLevel, exists := roleHierarchy[u.Role]
	if !exists {
		return false
	}

	requiredLevel, exists := roleHierarchy[requiredRole]
	if !exists {
		return false
	}

	return userLevel >= requiredLevel
}

// IsActive 检查用户是否处于活跃状态
func (u *User) IsActive() bool {
	return u.Status == StatusActive && (!u.OTPEnabled || u.EmailVerified)
}

// IsLocked 检查用户是否被锁定
func (u *User) IsLocked() bool {
	return u.isLockedAt(time.Now())
}

func (u *User) isLockedAt(now time.Time) bool {
	return u != nil && u.LockedUntil != nil && u.LockedUntil.After(now)
}

// GetDisplayName 获取用户显示名称
func (u *User) GetDisplayName() string {
	if u.Username != "" {
		return u.Username
	}
	return u.Email
}
