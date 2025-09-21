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

	"gongdan-system/internal/models"
	"gongdan-system/internal/services"
)

// 错误定义
var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUserNotFound       = errors.New("user not found")
	ErrUserExists         = errors.New("user already exists")
	ErrInvalidToken       = errors.New("invalid token")
	ErrTokenExpired       = errors.New("token expired")
	ErrInvalidOTP         = errors.New("invalid OTP")
	ErrOTPExpired         = errors.New("OTP expired")
	ErrEmailNotVerified   = errors.New("email not verified")
	ErrAccountLocked      = errors.New("account locked")
	ErrPasswordTooWeak    = errors.New("password too weak")
)

var (
	defaultTrustedDeviceTTL        = 30 * 24 * time.Hour
	defaultTrustedDeviceMaxPerUser = 5
)

// UserRole 用户角色枚举
type UserRole string

const (
	RoleUser      UserRole = "user"
	RoleAgent     UserRole = "agent"
	RoleAdmin     UserRole = "admin"
	RoleSuperUser UserRole = "superuser"
)

// UserStatus 用户状态枚举
type UserStatus string

const (
	StatusActive    UserStatus = "active"
	StatusInactive  UserStatus = "inactive"
	StatusLocked    UserStatus = "locked"
	StatusSuspended UserStatus = "suspended"
)

// User 用户模型
type User struct {
	ID                uint       `json:"id" gorm:"primaryKey"`
	Username          string     `json:"username" gorm:"uniqueIndex;not null"`
	Email             string     `json:"email" gorm:"uniqueIndex;not null"`
	PasswordHash      string     `json:"-" gorm:"not null"`
	Role              UserRole   `json:"role" gorm:"default:'user'"`
	Status            UserStatus `json:"status" gorm:"default:'active'"`
	EmailVerified     bool       `json:"email_verified" gorm:"default:false"`
	EmailVerifiedAt   *time.Time `json:"email_verified_at"`
	LastLoginAt       *time.Time `json:"last_login_at"`
	FailedLoginCount  int        `json:"failed_login_count" gorm:"default:0"`
	LockedUntil       *time.Time `json:"locked_until"`
	OTPEnabled        bool       `json:"otp_enabled" gorm:"default:false"`
	OTPSecret         string     `json:"-"`
	BackupCodes       string     `json:"-"`
	PasswordChangedAt *time.Time `json:"password_changed_at"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// UserProfile 用户资料
type UserProfile struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	UserID      uint      `json:"user_id" gorm:"uniqueIndex;not null"`
	FirstName   string    `json:"first_name"`
	LastName    string    `json:"last_name"`
	DisplayName string    `json:"display_name"`
	Avatar      string    `json:"avatar"`
	Phone       string    `json:"phone"`
	Department  string    `json:"department"`
	Position    string    `json:"position"`
	Timezone    string    `json:"timezone" gorm:"default:'UTC'"`
	Language    string    `json:"language" gorm:"default:'en'"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	User        User      `json:"user" gorm:"foreignKey:UserID"`
}

// LoginAttempt 登录尝试记录
type LoginAttempt struct {
	ID         uint      `json:"id" gorm:"primaryKey"`
	UserID     *uint     `json:"user_id"`
	Email      string    `json:"email"`
	IPAddress  string    `json:"ip_address"`
	UserAgent  string    `json:"user_agent"`
	Success    bool      `json:"success"`
	FailReason string    `json:"fail_reason"`
	CreatedAt  time.Time `json:"created_at"`
	User       *User     `json:"user,omitempty" gorm:"foreignKey:UserID"`
}

// RefreshToken 刷新令牌
type RefreshToken struct {
	ID        uint       `json:"id" gorm:"primaryKey"`
	UserID    uint       `json:"user_id" gorm:"not null"`
	Token     string     `json:"token" gorm:"uniqueIndex;not null"`
	SessionID string     `json:"session_id" gorm:"size:128;index"`
	ExpiresAt time.Time  `json:"expires_at"`
	Revoked   bool       `json:"revoked" gorm:"default:false"`
	RevokedAt *time.Time `json:"revoked_at"`
	IPAddress string     `json:"ip_address"`
	UserAgent string     `json:"user_agent"`
	CreatedAt time.Time  `json:"created_at"`
	User      User       `json:"user" gorm:"foreignKey:UserID"`
}

// EmailVerification 邮箱验证
type EmailVerification struct {
	ID        uint       `json:"id" gorm:"primaryKey"`
	UserID    uint       `json:"user_id" gorm:"not null;index"`
	Email     string     `json:"email" gorm:"size:255;not null"`
	Token     string     `json:"token" gorm:"size:255;not null;uniqueIndex"`
	Used      bool       `json:"used" gorm:"default:false"`
	ExpiresAt time.Time  `json:"expires_at" gorm:"not null"`
	UsedAt    *time.Time `json:"used_at"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	User      User       `json:"user" gorm:"foreignKey:UserID"`
}

// PasswordReset 密码重置
type PasswordReset struct {
	ID        uint       `json:"id" gorm:"primaryKey"`
	UserID    uint       `json:"user_id" gorm:"not null;index"`
	Email     string     `json:"email" gorm:"size:255;not null"`
	Token     string     `json:"token" gorm:"size:255;not null;uniqueIndex"`
	Used      bool       `json:"used" gorm:"default:false"`
	ExpiresAt time.Time  `json:"expires_at" gorm:"not null"`
	UsedAt    *time.Time `json:"used_at"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	User      User       `json:"user" gorm:"foreignKey:UserID"`
}

// OTPCode OTP验证码
type OTPCode struct {
	ID        uint       `json:"id" gorm:"primaryKey"`
	UserID    uint       `json:"user_id" gorm:"not null"`
	Code      string     `json:"code" gorm:"not null"`
	Type      string     `json:"type" gorm:"not null"` // login, setup, backup
	ExpiresAt time.Time  `json:"expires_at"`
	Used      bool       `json:"used" gorm:"default:false"`
	UsedAt    *time.Time `json:"used_at"`
	CreatedAt time.Time  `json:"created_at"`
	User      User       `json:"user" gorm:"foreignKey:UserID"`
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
	DeviceToken    string `json:"device_token,omitempty"`
	RememberDevice bool   `json:"remember_device,omitempty"`
	DeviceName     string `json:"device_name,omitempty"`
}

// RefreshTokenRequest 刷新令牌请求
type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// ChangePasswordRequest 修改密码请求
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" validate:"required"`
	NewPassword     string `json:"new_password" validate:"required,min=8"`
}

// ForgotPasswordRequest 忘记密码请求
type ForgotPasswordRequest struct {
	Email string `json:"email" validate:"required,email"`
}

// ResetPasswordRequest 重置密码请求
type ResetPasswordRequest struct {
	Token       string `json:"token" validate:"required"`
	NewPassword string `json:"new_password" validate:"required,min=8"`
}

// ResendVerificationRequest 重发验证邮件请求
type ResendVerificationRequest struct {
	Email string `json:"email" validate:"required,email"`
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
	User               *UserInfo `json:"user"`
	AccessToken        string    `json:"access_token"`
	RefreshToken       string    `json:"refresh_token"`
	ExpiresIn          int64     `json:"expires_in"`
	TokenType          string    `json:"token_type"`
	TrustedDeviceToken string    `json:"trusted_device_token,omitempty"`
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
	LockUser(ctx context.Context, userID uint, until time.Time) error
	UnlockUser(ctx context.Context, userID uint) error
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
	// 撤销令牌
	RevokeRefreshToken(ctx context.Context, token string) error
	// 撤销用户所有令牌
	RevokeAllUserTokens(ctx context.Context, userID uint) error
	// 清理过期令牌
	CleanupExpiredTokens(ctx context.Context) error
	// 创建邮箱验证
	CreateEmailVerification(ctx context.Context, verification *EmailVerification) error
	// 获取邮箱验证
	GetEmailVerification(ctx context.Context, token string) (*EmailVerification, error)
	// 使用邮箱验证
	UseEmailVerification(ctx context.Context, token string) error
	// 创建密码重置
	CreatePasswordReset(ctx context.Context, reset *PasswordReset) error
	// 获取密码重置
	GetPasswordReset(ctx context.Context, token string) (*PasswordReset, error)
	// 使用密码重置
	UsePasswordReset(ctx context.Context, token string) error

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
	SendOTPEmail(ctx context.Context, email, code string) error
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
	CanSendEmail(ctx context.Context) (bool, error)
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
	loginAttemptRepo   LoginAttemptRepository
	loginHistoryRepo   LoginHistoryRepository
	trustedDeviceRepo  TrustedDeviceRepository
	configService      *services.ConfigService
	emailService       EmailService
	emailConfigService EmailConfigService
	otpService         OTPService
	passwordService    PasswordService
	jwtManager         JWTManager
	config             *AuthConfig
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
	GenerateTokenPair(userID uint, role UserRole) (accessToken, refreshToken string, err error)
	VerifyAccessToken(token string) (*Claims, error)
	VerifyRefreshToken(token string) (*Claims, error)
	RevokeToken(token string) error
	ParseTokenClaims(token string) (*Claims, error)
}

// Claims JWT声明
type Claims struct {
	UserID uint     `json:"user_id"`
	Role   UserRole `json:"role"`
	Type   string   `json:"type"` // access, refresh
	Exp    int64    `json:"exp"`
	Iat    int64    `json:"iat"`
	Jti    string   `json:"jti"`
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
	emailService EmailService,
	emailConfigService EmailConfigService,
	otpService OTPService,
	passwordService PasswordService,
	jwtManager JWTManager,
	config *AuthConfig,
) *AuthService {
	return &AuthService{
		userRepo:           userRepo,
		profileRepo:        profileRepo,
		tokenRepo:          tokenRepo,
		loginAttemptRepo:   loginAttemptRepo,
		loginHistoryRepo:   loginHistoryRepo,
		trustedDeviceRepo:  trustedDeviceRepo,
		configService:      configService,
		emailService:       emailService,
		emailConfigService: emailConfigService,
		otpService:         otpService,
		passwordService:    passwordService,
		jwtManager:         jwtManager,
		config:             config,
	}
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
		Role:              RoleUser,
		Status:            StatusActive,
		EmailVerified:     !emailVerificationEnabled,
		PasswordChangedAt: timePtr(time.Now()),
	}

	if !emailVerificationEnabled {
		user.EmailVerifiedAt = timePtr(time.Now())
	}

	if err := s.userRepo.Create(ctx, user); err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
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

	if err := s.profileRepo.Create(ctx, profile); err != nil {
		return nil, fmt.Errorf("failed to create user profile: %w", err)
	}

	// 记录登录尝试
	s.recordLoginAttempt(ctx, &user.ID, req.Email, ipAddress, userAgent, true, "")

	// 发送验证邮件（仅当邮箱验证启用且可以发送邮件时）
	if emailVerificationEnabled {
		canSendEmail, err := s.emailConfigService.CanSendEmail(ctx)
		if err != nil || !canSendEmail {
			// 如果无法发送邮件，记录错误但不阻止注册
			fmt.Printf("Email verification is enabled but cannot send email: %v\n", err)
		} else {
			if err := s.sendEmailVerification(ctx, user); err != nil {
				// 不阻止注册，只记录错误
				fmt.Printf("Failed to send verification email: %v\n", err)
			}
		}
	}

	// 发送欢迎邮件（仅当可以发送邮件时）
	canSendEmail, err := s.emailConfigService.CanSendEmail(ctx)
	if err == nil && canSendEmail {
		if err := s.emailService.SendWelcomeEmail(ctx, user.Email, user.Username); err != nil {
			// 不阻止注册，只记录错误
			fmt.Printf("Failed to send welcome email: %v\n", err)
		}
	}

	// 如果需要邮箱验证，返回用户信息但不生成令牌
	if emailVerificationEnabled {
		return &AuthResponse{
			User: s.buildUserInfo(user, profile),
		}, nil
	}

	// 生成令牌
	accessToken, refreshToken, err := s.jwtManager.GenerateTokenPair(user.ID, user.Role)
	if err != nil {
		return nil, fmt.Errorf("failed to generate tokens: %w", err)
	}

	sessionID, err := GenerateSecureToken(16)
	if err != nil {
		return nil, fmt.Errorf("failed to generate session id: %w", err)
	}
	loginTime := time.Now()

	// 保存刷新令牌
	if err := s.saveRefreshToken(ctx, user.ID, refreshToken, sessionID, ipAddress, userAgent); err != nil {
		return nil, fmt.Errorf("failed to save refresh token: %w", err)
	}

	s.recordLoginHistorySuccess(ctx, user, ipAddress, userAgent, sessionID, loginTime, determineLoginMethod(user, nil, false, false))

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
		s.recordLoginAttempt(ctx, nil, req.Email, ipAddress, userAgent, false, err.Error())
		return nil, err
	}

	// 获取用户
	user, err := s.userRepo.GetByEmail(ctx, req.Email)
	if err != nil {
		s.recordLoginAttempt(ctx, nil, req.Email, ipAddress, userAgent, false, "user not found")
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
		s.recordLoginAttempt(ctx, &user.ID, req.Email, ipAddress, userAgent, false, statusErr.Error())
		s.recordLoginHistoryFailure(ctx, user, ipAddress, userAgent, method, statusErr.Error(), loginStatusFromError(statusErr))
		return nil, statusErr
	}

	// 验证密码
	if err := s.passwordService.VerifyPassword(user.PasswordHash, req.Password); err != nil {
		method := determineLoginMethod(user, req, deviceTrusted, otpValidated)
		s.userRepo.IncrementFailedLogin(ctx, user.ID)
		s.recordLoginAttempt(ctx, &user.ID, req.Email, ipAddress, userAgent, false, "invalid password")
		s.recordLoginHistoryFailure(ctx, user, ipAddress, userAgent, method, "invalid password", models.LoginStatusFailed)
		return nil, ErrInvalidCredentials
	}

	// 检查是否需要OTP验证
	if user.OTPEnabled && !deviceTrusted {
		if req.OTPCode == "" {
			method := determineLoginMethod(user, req, deviceTrusted, otpValidated)
			s.recordLoginAttempt(ctx, &user.ID, req.Email, ipAddress, userAgent, false, "otp required")
			s.recordLoginHistoryFailure(ctx, user, ipAddress, userAgent, method, "otp required", models.LoginStatusFailed)
			return nil, errors.New("OTP code required")
		}

		if !s.otpService.VerifyCode(user.OTPSecret, req.OTPCode) {
			// 检查是否是备用码
			if !s.verifyBackupCode(user, req.OTPCode) {
				method := determineLoginMethod(user, req, deviceTrusted, false)
				s.recordLoginAttempt(ctx, &user.ID, req.Email, ipAddress, userAgent, false, "invalid OTP")
				s.recordLoginHistoryFailure(ctx, user, ipAddress, userAgent, method, "invalid OTP", models.LoginStatusFailed)
				return nil, ErrInvalidOTP
			}
			// 使用备用码后持久化剩余的备用码集合
			if err := s.userRepo.Update(ctx, user); err != nil {
				fmt.Printf("Warning: failed to persist backup code usage for user %d: %v\n", user.ID, err)
			}
			otpValidated = true
		}
		otpValidated = true
	}

	// 获取用户资料
	profile, _ := s.profileRepo.GetByUserID(ctx, user.ID)

	// 重置失败登录计数
	s.userRepo.ResetFailedLogin(ctx, user.ID)

	// 更新最后登录时间
	now := time.Now()
	trustedDeviceTTL := s.getTrustedDeviceTTL()
	maxTrustedDevices := s.getTrustedDeviceLimit()
	s.userRepo.UpdateLastLogin(ctx, user.ID, now)

	// 记录成功登录
	s.recordLoginAttempt(ctx, &user.ID, req.Email, ipAddress, userAgent, true, "")

	// 生成令牌
	accessToken, refreshToken, err := s.jwtManager.GenerateTokenPair(user.ID, user.Role)
	if err != nil {
		return nil, fmt.Errorf("failed to generate tokens: %w", err)
	}

	sessionID, err := GenerateSecureToken(16)
	if err != nil {
		return nil, fmt.Errorf("failed to generate session id: %w", err)
	}

	// 保存刷新令牌
	if err := s.saveRefreshToken(ctx, user.ID, refreshToken, sessionID, ipAddress, userAgent); err != nil {
		return nil, fmt.Errorf("failed to save refresh token: %w", err)
	}

	loginMethod := determineLoginMethod(user, req, deviceTrusted, otpValidated)
	s.recordLoginHistorySuccess(ctx, user, ipAddress, userAgent, sessionID, now, loginMethod)

	var trustedDeviceToken string
	if s.trustedDeviceRepo != nil {
		if deviceTrusted && trustedDevice != nil {
			trustedDevice.LastUsedAt = now
			trustedDevice.LastIP = ipAddress
			trustedDevice.UserAgent = userAgent
			if req.RememberDevice {
				trustedDevice.ExpiresAt = now.Add(trustedDeviceTTL)
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
		User:               s.buildUserInfo(user, profile),
		AccessToken:        accessToken,
		RefreshToken:       refreshToken,
		ExpiresIn:          int64(s.config.AccessTokenExpire.Seconds()),
		TokenType:          "Bearer",
		TrustedDeviceToken: trustedDeviceToken,
	}, nil
}

// RefreshToken 刷新令牌
func (s *AuthService) RefreshToken(ctx context.Context, req *RefreshTokenRequest, ipAddress, userAgent string) (*AuthResponse, error) {
	// 验证刷新令牌
	claims, err := s.jwtManager.VerifyRefreshToken(req.RefreshToken)
	if err != nil {
		return nil, ErrInvalidToken
	}

	// 检查令牌是否在数据库中
	tokenRecord, err := s.tokenRepo.GetRefreshToken(ctx, req.RefreshToken)
	if err != nil || tokenRecord.Revoked {
		return nil, ErrInvalidToken
	}
	sessionID := tokenRecord.SessionID
	if sessionID == "" {
		if generatedSessionID, genErr := GenerateSecureToken(16); genErr == nil {
			sessionID = generatedSessionID
		} else {
			sessionID = fmt.Sprintf("fallback-%d", time.Now().UnixNano())
		}
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

	// 撤销旧的刷新令牌
	s.tokenRepo.RevokeRefreshToken(ctx, req.RefreshToken)

	// 生成新的令牌对
	accessToken, refreshToken, err := s.jwtManager.GenerateTokenPair(user.ID, user.Role)
	if err != nil {
		return nil, fmt.Errorf("failed to generate tokens: %w", err)
	}

	// 保存新的刷新令牌
	if err := s.saveRefreshToken(ctx, user.ID, refreshToken, sessionID, ipAddress, userAgent); err != nil {
		return nil, fmt.Errorf("failed to save refresh token: %w", err)
	}

	if s.loginHistoryRepo != nil && sessionID != "" {
		if err := s.loginHistoryRepo.RefreshSession(ctx, user.ID, sessionID, ipAddress, userAgent, time.Now()); err != nil {
			fmt.Printf("Warning: failed to refresh login session: %v\n", err)
		}
	}

	// 获取用户资料
	profile, _ := s.profileRepo.GetByUserID(ctx, user.ID)

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
	} else if claims, parseErr := s.jwtManager.ParseTokenClaims(refreshToken); parseErr == nil {
		userID = claims.UserID
	}

	if s.loginHistoryRepo != nil && userID != 0 && sessionID != "" {
		if err := s.loginHistoryRepo.EndSession(ctx, userID, sessionID, models.LoginStatusSuccess, "", time.Now()); err != nil {
			fmt.Printf("Warning: failed to mark session logout: %v\n", err)
		}
	}

	return s.tokenRepo.RevokeRefreshToken(ctx, refreshToken)
}

// LogoutAll 登出所有设备
func (s *AuthService) LogoutAll(ctx context.Context, userID uint) error {
	if s.loginHistoryRepo != nil {
		if err := s.loginHistoryRepo.EndAllSessions(ctx, userID, models.LoginStatusExpired, "logout_all", time.Now()); err != nil {
			fmt.Printf("Warning: failed to end all sessions for user %d: %v\n", userID, err)
		}
	}
	return s.tokenRepo.RevokeAllUserTokens(ctx, userID)
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
		Email:     email,
		Token:     token,
		ExpiresAt: time.Now().Add(s.config.PasswordResetExpire),
	}

	err = s.tokenRepo.CreatePasswordReset(ctx, reset)
	if err != nil {
		return fmt.Errorf("failed to create password reset: %w", err)
	}

	// 发送重置邮件
	err = s.emailService.SendPasswordResetEmail(ctx, email, token)
	if err != nil {
		return fmt.Errorf("failed to send reset email: %w", err)
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

	// 获取用户
	user, err := s.userRepo.GetByID(ctx, reset.UserID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	// 哈希新密码
	hashedPassword, err := s.passwordService.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	// 更新用户密码
	user.PasswordHash = hashedPassword
	user.PasswordChangedAt = timePtr(time.Now())
	user.FailedLoginCount = 0
	user.LockedUntil = nil

	err = s.userRepo.Update(ctx, user)
	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}

	// 标记令牌为已使用
	err = s.tokenRepo.UsePasswordReset(ctx, token)
	if err != nil {
		return fmt.Errorf("failed to mark token as used: %w", err)
	}

	// 撤销所有刷新令牌
	_ = s.tokenRepo.RevokeAllUserTokens(ctx, user.ID)

	return nil
}

// VerifyEmail 验证邮箱
func (s *AuthService) VerifyEmail(ctx context.Context, token string) error {
	// 验证令牌
	verification, err := s.tokenRepo.GetEmailVerification(ctx, token)
	if err != nil {
		return ErrInvalidToken
	}

	if verification.Used || time.Now().After(verification.ExpiresAt) {
		return ErrInvalidToken
	}

	// 获取用户
	user, err := s.userRepo.GetByID(ctx, verification.UserID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	// 更新用户邮箱验证状态
	user.EmailVerified = true
	user.EmailVerifiedAt = timePtr(time.Now())

	err = s.userRepo.Update(ctx, user)
	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}

	// 标记验证为已使用
	err = s.tokenRepo.UseEmailVerification(ctx, token)
	if err != nil {
		return fmt.Errorf("failed to mark verification as used: %w", err)
	}

	// 发送欢迎邮件
	_ = s.emailService.SendWelcomeEmail(ctx, user.Email, user.Username)

	return nil
}

// ResendVerification 重发验证邮件
func (s *AuthService) ResendVerification(ctx context.Context, email string) error {
	// 查找用户
	user, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		return ErrUserNotFound
	}

	// 检查是否已验证
	if user.EmailVerified {
		return fmt.Errorf("email already verified")
	}

	// 发送验证邮件
	err = s.sendEmailVerification(ctx, user)
	if err != nil {
		return fmt.Errorf("failed to send verification email: %w", err)
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

	// 更新用户密码
	user.PasswordHash = hashedPassword
	user.PasswordChangedAt = timePtr(time.Now())

	err = s.userRepo.Update(ctx, user)
	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}

	// 撤销所有刷新令牌（强制重新登录）
	_ = s.tokenRepo.RevokeAllUserTokens(ctx, user.ID)

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

	// 更新用户OTP设置
	user.OTPSecret = secret
	user.OTPEnabled = true
	user.BackupCodes = strings.Join(backupCodes, ",")

	err = s.userRepo.Update(ctx, user)
	if err != nil {
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

	// 禁用OTP
	user.OTPEnabled = false
	user.OTPSecret = ""
	user.BackupCodes = ""

	err = s.userRepo.Update(ctx, user)
	if err != nil {
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
	if s.verifyBackupCode(user, code) {
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

	// 更新用户备用码
	user.BackupCodes = strings.Join(backupCodes, ",")

	err = s.userRepo.Update(ctx, user)
	if err != nil {
		return nil, fmt.Errorf("failed to update user: %w", err)
	}

	return backupCodes, nil
}

// 辅助方法

func (s *AuthService) checkLoginAttempts(ctx context.Context, email string) error {
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
	switch user.Status {
	case StatusInactive:
		return errors.New("account is inactive")
	case StatusSuspended:
		return errors.New("account is suspended")
	case StatusLocked:
		if user.LockedUntil != nil && time.Now().Before(*user.LockedUntil) {
			return ErrAccountLocked
		}
		// 自动解锁
		user.Status = StatusActive
		user.LockedUntil = nil
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

func (s *AuthService) recordLoginAttempt(ctx context.Context, userID *uint, email, ipAddress, userAgent string, success bool, failReason string) {
	attempt := &LoginAttempt{
		UserID:     userID,
		Email:      email,
		IPAddress:  ipAddress,
		UserAgent:  userAgent,
		Success:    success,
		FailReason: failReason,
	}
	s.loginAttemptRepo.Create(ctx, attempt)
}

func (s *AuthService) saveRefreshToken(ctx context.Context, userID uint, token, sessionID, ipAddress, userAgent string) error {
	refreshToken := &RefreshToken{
		UserID:    userID,
		Token:     token,
		SessionID: sessionID,
		ExpiresAt: time.Now().Add(s.config.RefreshTokenExpire),
		IPAddress: ipAddress,
		UserAgent: userAgent,
	}
	return s.tokenRepo.CreateRefreshToken(ctx, refreshToken)
}

func (s *AuthService) sendEmailVerification(ctx context.Context, user *User) error {
	token, err := generateSecureToken(32)
	if err != nil {
		return err
	}

	verification := &EmailVerification{
		UserID:    user.ID,
		Email:     user.Email,
		Token:     token,
		ExpiresAt: time.Now().Add(s.config.EmailVerificationExpire),
	}

	if err := s.tokenRepo.CreateEmailVerification(ctx, verification); err != nil {
		return err
	}

	return s.emailService.SendVerificationEmail(ctx, user.Email, token)
}

func (s *AuthService) verifyBackupCode(user *User, code string) bool {
	if user.BackupCodes == "" {
		return false
	}

	codes := strings.Split(user.BackupCodes, ",")
	for i, backupCode := range codes {
		if backupCode == code {
			// 移除已使用的备用码
			codes = append(codes[:i], codes[i+1:]...)
			user.BackupCodes = strings.Join(codes, ",")
			return true
		}
	}
	return false
}

func (s *AuthService) recordLoginHistorySuccess(ctx context.Context, user *User, ipAddress, userAgent, sessionID string, loginTime time.Time, method string) {
	if s.loginHistoryRepo == nil || user == nil {
		return
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

	if err := s.loginHistoryRepo.Create(ctx, history); err != nil {
		fmt.Printf("Warning: failed to record login history: %v\n", err)
	}
}

func (s *AuthService) recordLoginHistoryFailure(ctx context.Context, user *User, ipAddress, userAgent, method string, reason string, status models.LoginStatus) {
	if s.loginHistoryRepo == nil || user == nil {
		return
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

	if err := s.loginHistoryRepo.Create(ctx, history); err != nil {
		fmt.Printf("Warning: failed to record failed login history: %v\n", err)
	}
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

func determineLoginMethod(user *User, req *LoginRequest, deviceTrusted bool, otpValidated bool) string {
	if deviceTrusted {
		return "password+trusted"
	}

	if user != nil && user.OTPEnabled {
		if otpValidated {
			return "password+otp"
		}
		if req != nil && req.OTPCode != "" {
			return "password+otp"
		}
		return "password+otp_required"
	}

	return "password"
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

// ValidateRole 验证角色是否有效
func ValidateRole(role string) bool {
	switch UserRole(role) {
	case RoleUser, RoleAgent, RoleAdmin, RoleSuperUser:
		return true
	default:
		return false
	}
}

// ValidateStatus 验证状态是否有效
func ValidateStatus(status string) bool {
	switch UserStatus(status) {
	case StatusActive, StatusInactive, StatusLocked, StatusSuspended:
		return true
	default:
		return false
	}
}

// HasPermission 检查用户是否有指定权限
func (u *User) HasPermission(requiredRole UserRole) bool {
	roleHierarchy := map[UserRole]int{
		RoleUser:      1,
		RoleAgent:     2,
		RoleAdmin:     3,
		RoleSuperUser: 4,
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
	if u.Status == StatusLocked {
		if u.LockedUntil == nil {
			return true
		}
		return time.Now().Before(*u.LockedUntil)
	}
	return false
}

// GetDisplayName 获取用户显示名称
func (u *User) GetDisplayName() string {
	if u.Username != "" {
		return u.Username
	}
	return u.Email
}
