package auth

import (
	"context"
	"errors"
	"time"

	"gongdan-system/internal/models"
	"gorm.io/gorm"
)

// GormUserRepository GORM用户仓库实现
type GormUserRepository struct {
	db *gorm.DB
}

// NewGormUserRepository 创建GORM用户仓库
func NewGormUserRepository(db *gorm.DB) UserRepository {
	return &GormUserRepository{db: db}
}

// Create 创建用户
func (r *GormUserRepository) Create(ctx context.Context, user *User) error {
	// 转换为models.User
	modelUser := &models.User{
		Username:         user.Username,
		Email:            user.Email,
		PasswordHash:     user.PasswordHash,
		Role:             models.UserRole(user.Role),
		Status:           convertUserStatus(user.Status),
		EmailVerified:    user.EmailVerified,
		EmailVerifiedAt:  user.EmailVerifiedAt,
		LastLoginAt:      user.LastLoginAt,
		LoginAttempts:    user.FailedLoginCount,
		LockedUntil:      user.LockedUntil,
		TwoFactorEnabled: user.OTPEnabled,
		TwoFactorSecret:  user.OTPSecret,
		BackupCodes:      user.BackupCodes,
		PasswordResetAt:  user.PasswordChangedAt,
	}

	if err := r.db.WithContext(ctx).Create(modelUser).Error; err != nil {
		return err
	}

	// 更新ID
	user.ID = modelUser.ID
	user.CreatedAt = modelUser.CreatedAt
	user.UpdatedAt = modelUser.UpdatedAt

	return nil
}

// GetByID 根据ID获取用户
func (r *GormUserRepository) GetByID(ctx context.Context, id uint) (*User, error) {
	var modelUser models.User
	if err := r.db.WithContext(ctx).First(&modelUser, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}

	return convertToAuthUser(&modelUser), nil
}

// GetByEmail 根据邮箱获取用户
func (r *GormUserRepository) GetByEmail(ctx context.Context, email string) (*User, error) {
	var modelUser models.User
	if err := r.db.WithContext(ctx).Where("email = ?", email).First(&modelUser).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}

	return convertToAuthUser(&modelUser), nil
}

// GetByUsername 根据用户名获取用户
func (r *GormUserRepository) GetByUsername(ctx context.Context, username string) (*User, error) {
	var modelUser models.User
	if err := r.db.WithContext(ctx).Where("username = ?", username).First(&modelUser).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}

	return convertToAuthUser(&modelUser), nil
}

// Update 更新用户
func (r *GormUserRepository) Update(ctx context.Context, user *User) error {
	// 转换为models.User
	modelUser := &models.User{
		ID:               user.ID,
		Username:         user.Username,
		Email:            user.Email,
		PasswordHash:     user.PasswordHash,
		Role:             models.UserRole(user.Role),
		Status:           convertUserStatus(user.Status),
		EmailVerified:    user.EmailVerified,
		EmailVerifiedAt:  user.EmailVerifiedAt,
		LastLoginAt:      user.LastLoginAt,
		LoginAttempts:    user.FailedLoginCount,
		LockedUntil:      user.LockedUntil,
		TwoFactorEnabled: user.OTPEnabled,
		TwoFactorSecret:  user.OTPSecret,
		BackupCodes:      user.BackupCodes,
		PasswordResetAt:  user.PasswordChangedAt,
	}

	if err := r.db.WithContext(ctx).Save(modelUser).Error; err != nil {
		return err
	}

	user.UpdatedAt = modelUser.UpdatedAt
	return nil
}

// Delete 删除用户
func (r *GormUserRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&models.User{}, id).Error
}

// List 获取用户列表
func (r *GormUserRepository) List(ctx context.Context, offset, limit int) ([]*User, int64, error) {
	var modelUsers []models.User
	var total int64

	// 获取总数
	if err := r.db.WithContext(ctx).Model(&models.User{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 获取分页数据
	if err := r.db.WithContext(ctx).Offset(offset).Limit(limit).Find(&modelUsers).Error; err != nil {
		return nil, 0, err
	}

	// 转换为auth.User
	users := make([]*User, len(modelUsers))
	for i, modelUser := range modelUsers {
		users[i] = convertToAuthUser(&modelUser)
	}

	return users, total, nil
}

// UpdateLastLogin 更新最后登录时间
func (r *GormUserRepository) UpdateLastLogin(ctx context.Context, userID uint, loginTime time.Time) error {
	return r.db.WithContext(ctx).Model(&models.User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"last_login_at": loginTime,
		"last_login_ip": "", // 可以从context中获取IP
	}).Error
}

// IncrementFailedLogin 增加失败登录次数
func (r *GormUserRepository) IncrementFailedLogin(ctx context.Context, userID uint) error {
	return r.db.WithContext(ctx).Model(&models.User{}).Where("id = ?", userID).UpdateColumn("login_attempts", gorm.Expr("login_attempts + ?", 1)).Error
}

// ResetFailedLogin 重置失败登录次数
func (r *GormUserRepository) ResetFailedLogin(ctx context.Context, userID uint) error {
	return r.db.WithContext(ctx).Model(&models.User{}).Where("id = ?", userID).Update("login_attempts", 0).Error
}

// LockUser 锁定用户
func (r *GormUserRepository) LockUser(ctx context.Context, userID uint, until time.Time) error {
	return r.db.WithContext(ctx).Model(&models.User{}).Where("id = ?", userID).Update("locked_until", until).Error
}

// UnlockUser 解锁用户
func (r *GormUserRepository) UnlockUser(ctx context.Context, userID uint) error {
	return r.db.WithContext(ctx).Model(&models.User{}).Where("id = ?", userID).Update("locked_until", nil).Error
}

// 辅助函数：转换用户状态
func convertUserStatus(status UserStatus) models.UserStatus {
	switch status {
	case StatusActive:
		return models.UserStatusActive
	case StatusInactive:
		return models.UserStatusInactive
	case StatusLocked:
		return models.UserStatusSuspended
	case StatusSuspended:
		return models.UserStatusSuspended
	default:
		return models.UserStatusInactive
	}
}

// 辅助函数：转换为认证用户模型
func convertToAuthUser(modelUser *models.User) *User {
	return &User{
		ID:                modelUser.ID,
		Username:          modelUser.Username,
		Email:             modelUser.Email,
		PasswordHash:      modelUser.PasswordHash,
		Role:              UserRole(modelUser.Role),
		Status:            convertFromUserStatus(modelUser.Status),
		EmailVerified:     modelUser.EmailVerified,
		EmailVerifiedAt:   modelUser.EmailVerifiedAt,
		LastLoginAt:       modelUser.LastLoginAt,
		FailedLoginCount:  modelUser.LoginAttempts,
		LockedUntil:       modelUser.LockedUntil,
		OTPEnabled:        modelUser.TwoFactorEnabled,
		OTPSecret:         modelUser.TwoFactorSecret,
		BackupCodes:       modelUser.BackupCodes,
		PasswordChangedAt: modelUser.PasswordResetAt,
		CreatedAt:         modelUser.CreatedAt,
		UpdatedAt:         modelUser.UpdatedAt,
	}
}

// 辅助函数：从models.UserStatus转换
func convertFromUserStatus(status models.UserStatus) UserStatus {
	switch status {
	case models.UserStatusActive:
		return StatusActive
	case models.UserStatusInactive:
		return StatusInactive
	case models.UserStatusSuspended:
		return StatusSuspended
	case models.UserStatusDeleted:
		return StatusSuspended
	default:
		return StatusInactive
	}
}

// GormTokenRepository GORM令牌仓库实现
type GormTokenRepository struct {
	db *gorm.DB
}

// NewGormTokenRepository 创建GORM令牌仓库
func NewGormTokenRepository(db *gorm.DB) TokenRepository {
	return &GormTokenRepository{db: db}
}

// CreateRefreshToken 创建刷新令牌
func (r *GormTokenRepository) CreateRefreshToken(ctx context.Context, token *RefreshToken) error {
	return r.db.WithContext(ctx).Create(token).Error
}

// GetRefreshToken 获取刷新令牌
func (r *GormTokenRepository) GetRefreshToken(ctx context.Context, token string) (*RefreshToken, error) {
	var refreshToken RefreshToken
	if err := r.db.WithContext(ctx).Where("token = ? AND revoked = false AND expires_at > ?", token, time.Now()).First(&refreshToken).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInvalidToken
		}
		return nil, err
	}
	return &refreshToken, nil
}

// RevokeRefreshToken 撤销刷新令牌
func (r *GormTokenRepository) RevokeRefreshToken(ctx context.Context, token string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&RefreshToken{}).Where("token = ?", token).Updates(map[string]interface{}{
		"revoked":    true,
		"revoked_at": &now,
	}).Error
}

// RevokeAllUserTokens 撤销用户所有令牌
func (r *GormTokenRepository) RevokeAllUserTokens(ctx context.Context, userID uint) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&RefreshToken{}).Where("user_id = ? AND revoked = false", userID).Updates(map[string]interface{}{
		"revoked":    true,
		"revoked_at": &now,
	}).Error
}

// CleanupExpiredTokens 清理过期令牌
func (r *GormTokenRepository) CleanupExpiredTokens(ctx context.Context) error {
	return r.db.WithContext(ctx).Where("expires_at < ?", time.Now()).Delete(&RefreshToken{}).Error
}

// CreateEmailVerification 创建邮箱验证
func (r *GormTokenRepository) CreateEmailVerification(ctx context.Context, verification *EmailVerification) error {
	return r.db.WithContext(ctx).Create(verification).Error
}

// GetEmailVerification 获取邮箱验证
func (r *GormTokenRepository) GetEmailVerification(ctx context.Context, token string) (*EmailVerification, error) {
	var verification EmailVerification
	if err := r.db.WithContext(ctx).Where("token = ? AND used = false AND expires_at > ?", token, time.Now()).First(&verification).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInvalidToken
		}
		return nil, err
	}
	return &verification, nil
}

// UseEmailVerification 使用邮箱验证
func (r *GormTokenRepository) UseEmailVerification(ctx context.Context, token string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&EmailVerification{}).Where("token = ?", token).Updates(map[string]interface{}{
		"used":    true,
		"used_at": &now,
	}).Error
}

// CreatePasswordReset 创建密码重置
func (r *GormTokenRepository) CreatePasswordReset(ctx context.Context, reset *PasswordReset) error {
	return r.db.WithContext(ctx).Create(reset).Error
}

// GetPasswordReset 获取密码重置
func (r *GormTokenRepository) GetPasswordReset(ctx context.Context, token string) (*PasswordReset, error) {
	var reset PasswordReset
	if err := r.db.WithContext(ctx).Where("token = ? AND used = false AND expires_at > ?", token, time.Now()).First(&reset).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInvalidToken
		}
		return nil, err
	}
	return &reset, nil
}

// UsePasswordReset 使用密码重置
func (r *GormTokenRepository) UsePasswordReset(ctx context.Context, token string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&PasswordReset{}).Where("token = ?", token).Updates(map[string]interface{}{
		"used":    true,
		"used_at": &now,
	}).Error
}

// CreateOTPCode 创建OTP验证码
func (r *GormTokenRepository) CreateOTPCode(ctx context.Context, otp *OTPCode) error {
	return r.db.WithContext(ctx).Create(otp).Error
}

// GetOTPCode 获取OTP验证码
func (r *GormTokenRepository) GetOTPCode(ctx context.Context, userID uint, code string) (*OTPCode, error) {
	var otp OTPCode
	if err := r.db.WithContext(ctx).Where("user_id = ? AND code = ? AND used = false AND expires_at > ?", userID, code, time.Now()).First(&otp).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInvalidOTP
		}
		return nil, err
	}
	return &otp, nil
}

// UseOTPCode 使用OTP验证码
func (r *GormTokenRepository) UseOTPCode(ctx context.Context, userID uint, code string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&OTPCode{}).Where("user_id = ? AND code = ?", userID, code).Updates(map[string]interface{}{
		"used":    true,
		"used_at": &now,
	}).Error
}

// CleanupExpiredOTP 清理过期OTP
func (r *GormTokenRepository) CleanupExpiredOTP(ctx context.Context) error {
	return r.db.WithContext(ctx).Where("expires_at < ?", time.Now()).Delete(&OTPCode{}).Error
}

// GormLoginAttemptRepository GORM登录尝试仓库实现
type GormLoginAttemptRepository struct {
	db *gorm.DB
}

// NewGormLoginAttemptRepository 创建GORM登录尝试仓库
func NewGormLoginAttemptRepository(db *gorm.DB) LoginAttemptRepository {
	return &GormLoginAttemptRepository{db: db}
}

// GormProfileRepository GORM用户资料仓库实现
type GormProfileRepository struct {
	db *gorm.DB
}

// NewGormProfileRepository 创建GORM用户资料仓库
func NewGormProfileRepository(db *gorm.DB) ProfileRepository {
	return &GormProfileRepository{db: db}
}

// Create 创建用户资料
func (r *GormProfileRepository) Create(ctx context.Context, profile *UserProfile) error {
	return r.db.WithContext(ctx).Create(profile).Error
}

// GetByUserID 根据用户ID获取资料
func (r *GormProfileRepository) GetByUserID(ctx context.Context, userID uint) (*UserProfile, error) {
	var profile UserProfile
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&profile).Error
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

// Update 更新用户资料
func (r *GormProfileRepository) Update(ctx context.Context, profile *UserProfile) error {
	return r.db.WithContext(ctx).Save(profile).Error
}

// Delete 删除用户资料
func (r *GormProfileRepository) Delete(ctx context.Context, userID uint) error {
	return r.db.WithContext(ctx).Where("user_id = ?", userID).Delete(&UserProfile{}).Error
}

// Create 创建登录尝试记录
func (r *GormLoginAttemptRepository) Create(ctx context.Context, attempt *LoginAttempt) error {
	return r.db.WithContext(ctx).Create(attempt).Error
}

// GetRecentAttempts 获取最近的登录尝试
func (r *GormLoginAttemptRepository) GetRecentAttempts(ctx context.Context, email string, since time.Time) ([]*LoginAttempt, error) {
	var attempts []*LoginAttempt
	if err := r.db.WithContext(ctx).Where("email = ? AND created_at > ?", email, since).Order("created_at DESC").Find(&attempts).Error; err != nil {
		return nil, err
	}
	return attempts, nil
}

// GetRecentFailedAttempts 获取最近的失败登录尝试次数
func (r *GormLoginAttemptRepository) GetRecentFailedAttempts(ctx context.Context, email string, since time.Time) (int, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&LoginAttempt{}).Where("email = ? AND success = false AND created_at > ?", email, since).Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}

// CleanupOldAttempts 清理旧的登录尝试记录
func (r *GormLoginAttemptRepository) CleanupOldAttempts(ctx context.Context, before time.Time) error {
	return r.db.WithContext(ctx).Where("created_at < ?", before).Delete(&LoginAttempt{}).Error
}

// GormLoginHistoryRepository 登录历史仓库实现
type GormLoginHistoryRepository struct {
	db *gorm.DB
}

// NewGormLoginHistoryRepository 创建登录历史仓库
func NewGormLoginHistoryRepository(db *gorm.DB) LoginHistoryRepository {
	return &GormLoginHistoryRepository{db: db}
}

// Create 创建登录历史记录
func (r *GormLoginHistoryRepository) Create(ctx context.Context, history *models.LoginHistory) error {
	return r.db.WithContext(ctx).Create(history).Error
}

// RefreshSession 刷新会话活跃信息
func (r *GormLoginHistoryRepository) RefreshSession(ctx context.Context, userID uint, sessionID, ipAddress, userAgent string, at time.Time) error {
	if sessionID == "" {
		return nil
	}

	var history models.LoginHistory
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND session_id = ?", userID, sessionID).
		Order("login_time DESC").
		First(&history).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			assignErr := r.db.WithContext(ctx).
				Where("user_id = ? AND session_id = '' AND is_active = ?", userID, true).
				Order("login_time DESC").
				First(&history).Error
			if assignErr != nil {
				if errors.Is(assignErr, gorm.ErrRecordNotFound) {
					return nil
				}
				return assignErr
			}

			if updateErr := r.db.WithContext(ctx).Model(&history).Update("session_id", sessionID).Error; updateErr != nil {
				return updateErr
			}
		} else {
			return err
		}
	}

	updates := map[string]interface{}{
		"last_activity_at": at,
		"ip_address":       ipAddress,
		"user_agent":       userAgent,
	}

	if at.After(history.LoginTime) {
		duration := int64(at.Sub(history.LoginTime).Seconds())
		if duration < 0 {
			duration = 0
		}
		updates["session_duration"] = duration
	}

	return r.db.WithContext(ctx).Model(&history).Updates(updates).Error
}

// EndSession 结束指定会话
func (r *GormLoginHistoryRepository) EndSession(ctx context.Context, userID uint, sessionID string, status models.LoginStatus, reason string, at time.Time) error {
	if sessionID == "" {
		return nil
	}

	var history models.LoginHistory
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND session_id = ? AND is_active = ?", userID, sessionID, true).
		Order("login_time DESC").
		First(&history).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}

	duration := int64(at.Sub(history.LoginTime).Seconds())
	if duration < 0 {
		duration = 0
	}
	updates := map[string]interface{}{
		"logout_time":      at,
		"last_activity_at": at,
		"session_duration": duration,
		"is_active":        false,
		"login_status":     status,
	}

	if reason != "" {
		updates["failure_reason"] = reason
	}

	return r.db.WithContext(ctx).Model(&history).Updates(updates).Error
}

// EndAllSessions 结束用户的所有活跃会话
func (r *GormLoginHistoryRepository) EndAllSessions(ctx context.Context, userID uint, status models.LoginStatus, reason string, at time.Time) error {
	var histories []models.LoginHistory
	if err := r.db.WithContext(ctx).Where("user_id = ? AND is_active = ?", userID, true).Find(&histories).Error; err != nil {
		return err
	}

	for _, history := range histories {
		duration := int64(at.Sub(history.LoginTime).Seconds())
		if duration < 0 {
			duration = 0
		}
		updates := map[string]interface{}{
			"logout_time":      at,
			"last_activity_at": at,
			"session_duration": duration,
			"is_active":        false,
			"login_status":     status,
		}

		if reason != "" {
			updates["failure_reason"] = reason
		}

		if err := r.db.WithContext(ctx).Model(&history).Updates(updates).Error; err != nil {
			return err
		}
	}

	return nil
}

// GormTrustedDeviceRepository 可信设备仓库实现
type GormTrustedDeviceRepository struct {
	db *gorm.DB
}

// NewGormTrustedDeviceRepository 创建可信设备仓库
func NewGormTrustedDeviceRepository(db *gorm.DB) TrustedDeviceRepository {
	return &GormTrustedDeviceRepository{db: db}
}

// GetByTokenHash 根据令牌哈希获取可信设备
func (r *GormTrustedDeviceRepository) GetByTokenHash(ctx context.Context, tokenHash string) (*models.OTPTrustedDevice, error) {
	if tokenHash == "" {
		return nil, gorm.ErrRecordNotFound
	}

	var device models.OTPTrustedDevice
	if err := r.db.WithContext(ctx).Where("device_token_hash = ?", tokenHash).First(&device).Error; err != nil {
		return nil, err
	}
	return &device, nil
}

// Create 新建设备
func (r *GormTrustedDeviceRepository) Create(ctx context.Context, device *models.OTPTrustedDevice) error {
	return r.db.WithContext(ctx).Create(device).Error
}

// Update 更新设备记录
func (r *GormTrustedDeviceRepository) Update(ctx context.Context, device *models.OTPTrustedDevice) error {
	return r.db.WithContext(ctx).Save(device).Error
}

// ListActiveDevices 返回用户当前未撤销的可信设备，按最近使用时间排序
func (r *GormTrustedDeviceRepository) ListActiveDevices(ctx context.Context, userID uint) ([]*models.OTPTrustedDevice, error) {
	var devices []*models.OTPTrustedDevice
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND revoked = ?", userID, false).
		Order("COALESCE(last_used_at, created_at) DESC").
		Find(&devices).Error
	return devices, err
}
