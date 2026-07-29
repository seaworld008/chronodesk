package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"gongdan-system/internal/models"
	"gongdan-system/internal/security"
	"gorm.io/gorm"
)

// GormUserRepository GORM用户仓库实现
type GormUserRepository struct {
	db        *gorm.DB
	protector security.Protector
}

// NewGormUserRepository 创建GORM用户仓库
func NewGormUserRepository(db *gorm.DB, protectors ...security.Protector) UserRepository {
	var protector security.Protector
	if len(protectors) > 0 {
		protector = protectors[0]
	}
	return &GormUserRepository{db: db, protector: protector}
}

// Create 创建用户
func (r *GormUserRepository) Create(ctx context.Context, user *User) error {
	if user == nil {
		return ErrUserNotFound
	}
	if !user.OTPEnabled && (user.OTPSecret != "" || user.BackupCodes != "") {
		return errors.New("disabled OTP cannot retain credentials")
	}
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
		TwoFactorEnabled: false,
		TwoFactorSecret:  "",
		BackupCodes:      "",
		PasswordResetAt:  user.PasswordChangedAt,
	}

	if err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(modelUser).Error; err != nil {
			return err
		}
		if user.OTPEnabled {
			return r.configureOTPWithDB(
				tx,
				modelUser.ID,
				user.OTPSecret,
				user.BackupCodes,
				true,
			)
		}
		return nil
	}); err != nil {
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

	return r.convertToAuthUser(&modelUser)
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

	return r.convertToAuthUser(&modelUser)
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

	return r.convertToAuthUser(&modelUser)
}

// Update 更新用户
func (r *GormUserRepository) Update(ctx context.Context, user *User) error {
	if user == nil || user.ID == 0 {
		return ErrUserNotFound
	}
	// OTP凭据只允许通过专用方法写入，避免普通用户更新把已消费的备用码恢复。
	updates := map[string]interface{}{
		"username":          user.Username,
		"email":             user.Email,
		"password_hash":     user.PasswordHash,
		"role":              models.UserRole(user.Role),
		"status":            convertUserStatus(user.Status),
		"email_verified":    user.EmailVerified,
		"email_verified_at": user.EmailVerifiedAt,
		"last_login_at":     user.LastLoginAt,
		"login_attempts":    user.FailedLoginCount,
		"locked_until":      user.LockedUntil,
		"password_reset_at": user.PasswordChangedAt,
	}
	result := r.db.WithContext(ctx).Model(&models.User{}).
		Where("id = ?", user.ID).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrUserNotFound
	}
	var updated models.User
	if err := r.db.WithContext(ctx).Select("updated_at").First(&updated, user.ID).Error; err != nil {
		return err
	}
	user.UpdatedAt = updated.UpdatedAt
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
		converted, err := r.convertToAuthUser(&modelUser)
		if err != nil {
			return nil, 0, err
		}
		users[i] = converted
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

func (r *GormUserRepository) ConfigureOTP(
	ctx context.Context,
	userID uint,
	secret, backupCodeHashes string,
	enabled bool,
) error {
	return r.configureOTPWithDB(
		r.db.WithContext(ctx),
		userID,
		secret,
		backupCodeHashes,
		enabled,
	)
}

func (r *GormUserRepository) configureOTPWithDB(
	db *gorm.DB,
	userID uint,
	secret, backupCodeHashes string,
	enabled bool,
) error {
	if userID == 0 {
		return ErrUserNotFound
	}
	if !enabled {
		secret = ""
		backupCodeHashes = ""
	} else if strings.TrimSpace(secret) == "" {
		return errors.New("OTP secret is required")
	}
	if _, err := parseBackupCodeHashes(backupCodeHashes); err != nil {
		return err
	}
	protectedSecret, err := security.ProtectOptional(
		r.protector,
		secret,
		otpSecretAAD(userID),
	)
	if err != nil {
		return fmt.Errorf("protect OTP secret: %w", err)
	}
	result := db.Model(&models.User{}).
		Where("id = ?", userID).
		Updates(map[string]interface{}{
			"two_factor_enabled": enabled,
			"two_factor_secret":  protectedSecret,
			"backup_codes":       backupCodeHashes,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrUserNotFound
	}
	return nil
}

func (r *GormUserRepository) ReplaceBackupCodes(
	ctx context.Context,
	userID uint,
	backupCodeHashes string,
) error {
	if _, err := parseBackupCodeHashes(backupCodeHashes); err != nil {
		return err
	}
	result := r.db.WithContext(ctx).Model(&models.User{}).
		Where("id = ? AND two_factor_enabled = ?", userID, true).
		Update("backup_codes", backupCodeHashes)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrInvalidOTP
	}
	return nil
}

func (r *GormUserRepository) ConsumeBackupCode(
	ctx context.Context,
	userID uint,
	code string,
) (bool, error) {
	if userID == 0 || strings.TrimSpace(code) == "" {
		return false, nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		var row struct {
			BackupCodes string
		}
		if err := r.db.WithContext(ctx).Model(&models.User{}).
			Select("backup_codes").
			Where("id = ? AND two_factor_enabled = ?", userID, true).
			Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return false, nil
			}
			return false, err
		}
		hashes, err := parseBackupCodeHashes(row.BackupCodes)
		if err != nil {
			return false, err
		}
		index := matchBackupCode(hashes, code)
		if index < 0 {
			return false, nil
		}
		remaining := append(append([]string{}, hashes[:index]...), hashes[index+1:]...)
		result := r.db.WithContext(ctx).Model(&models.User{}).
			Where(
				"id = ? AND two_factor_enabled = ? AND backup_codes = ?",
				userID,
				true,
				row.BackupCodes,
			).
			Update("backup_codes", strings.Join(remaining, ","))
		if result.Error != nil {
			return false, result.Error
		}
		if result.RowsAffected == 1 {
			return true, nil
		}
	}
	return false, nil
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
func (r *GormUserRepository) convertToAuthUser(modelUser *models.User) (*User, error) {
	otpSecret, err := security.RevealOptional(
		r.protector,
		modelUser.TwoFactorSecret,
		otpSecretAAD(modelUser.ID),
	)
	if err != nil {
		return nil, fmt.Errorf("reveal OTP secret for user %d: %w", modelUser.ID, err)
	}
	if _, err := parseBackupCodeHashes(modelUser.BackupCodes); err != nil {
		return nil, fmt.Errorf("load backup codes for user %d: %w", modelUser.ID, err)
	}
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
		OTPSecret:         otpSecret,
		BackupCodes:       modelUser.BackupCodes,
		PasswordChangedAt: modelUser.PasswordResetAt,
		CreatedAt:         modelUser.CreatedAt,
		UpdatedAt:         modelUser.UpdatedAt,
	}, nil
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

func bearerTokenDigest(purpose, token string) string {
	sum := sha256.Sum256([]byte("chronodesk:" + purpose + ":v1\x00" + token))
	return hex.EncodeToString(sum[:])
}

// CreateRefreshToken 创建刷新令牌
func (r *GormTokenRepository) CreateRefreshToken(ctx context.Context, token *RefreshToken) error {
	if token == nil || strings.TrimSpace(token.Token) == "" {
		return ErrInvalidToken
	}
	plaintext := token.Token
	token.Token = bearerTokenDigest("refresh-token", plaintext)
	defer func() {
		token.Token = plaintext
	}()
	return r.db.WithContext(ctx).Create(token).Error
}

// GetRefreshToken 获取刷新令牌
func (r *GormTokenRepository) GetRefreshToken(ctx context.Context, token string) (*RefreshToken, error) {
	var refreshToken RefreshToken
	if err := r.db.WithContext(ctx).
		Where(
			"token = ? AND revoked = false AND expires_at > ?",
			bearerTokenDigest("refresh-token", token),
			time.Now(),
		).
		First(&refreshToken).Error; err != nil {
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
	result := r.db.WithContext(ctx).Model(&RefreshToken{}).
		Where(
			"token = ? AND revoked = ? AND expires_at > ?",
			bearerTokenDigest("refresh-token", token),
			false,
			now,
		).
		Updates(map[string]interface{}{
			"revoked":    true,
			"revoked_at": &now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrInvalidToken
	}
	return nil
}

func (r *GormTokenRepository) RotateRefreshToken(
	ctx context.Context,
	currentToken string,
	replacement *RefreshToken,
) error {
	if strings.TrimSpace(currentToken) == "" ||
		replacement == nil ||
		strings.TrimSpace(replacement.Token) == "" ||
		replacement.UserID == 0 ||
		strings.TrimSpace(replacement.SessionID) == "" {
		return ErrInvalidToken
	}
	storedReplacement := *replacement
	storedReplacement.ID = 0
	storedReplacement.Token = bearerTokenDigest("refresh-token", replacement.Token)
	now := time.Now()
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&RefreshToken{}).
			Where(
				"token = ? AND revoked = ? AND expires_at > ? AND user_id = ? AND session_id = ?",
				bearerTokenDigest("refresh-token", currentToken),
				false,
				now,
				replacement.UserID,
				replacement.SessionID,
			).
			Updates(map[string]interface{}{
				"revoked":    true,
				"revoked_at": &now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrInvalidToken
		}
		return tx.Create(&storedReplacement).Error
	})
	if err != nil {
		return err
	}
	replacement.ID = storedReplacement.ID
	replacement.CreatedAt = storedReplacement.CreatedAt
	return nil
}

// RevokeAllUserTokens 撤销用户所有令牌
func (r *GormTokenRepository) RevokeAllUserTokens(ctx context.Context, userID uint) error {
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&RefreshToken{}).
			Where("user_id = ? AND revoked = false", userID).
			Updates(map[string]interface{}{
				"revoked":    true,
				"revoked_at": &now,
			}).Error; err != nil {
			return err
		}
		return tx.Model(&models.LoginHistory{}).
			Where("user_id = ? AND is_active = ?", userID, true).
			Updates(map[string]interface{}{
				"is_active":        false,
				"logout_time":      now,
				"last_activity_at": now,
				"login_status":     models.LoginStatusExpired,
				"failure_reason":   "logout_all",
			}).Error
	})
}

// RevokeSession revokes every refresh token issued for one login session.
// Refresh rotation keeps the same session ID, so this invalidates both the
// current refresh token and every access token carrying that sid.
func (r *GormTokenRepository) RevokeSession(ctx context.Context, userID uint, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if userID == 0 || sessionID == "" {
		return nil
	}
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&RefreshToken{}).
			Where("user_id = ? AND session_id = ? AND revoked = false", userID, sessionID).
			Updates(map[string]interface{}{
				"revoked":    true,
				"revoked_at": &now,
			}).Error; err != nil {
			return err
		}
		return tx.Model(&models.LoginHistory{}).
			Where("user_id = ? AND session_id = ? AND is_active = ?", userID, sessionID, true).
			Updates(map[string]interface{}{
				"is_active":        false,
				"logout_time":      now,
				"last_activity_at": now,
			}).Error
	})
}

// IsSessionActive is the database-authoritative access-token revocation check.
// It deliberately does not use Redis so a cache outage or stale entry can
// never resurrect a logged-out session.
func (r *GormTokenRepository) IsSessionActive(ctx context.Context, userID uint, sessionID string) (bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if userID == 0 || sessionID == "" {
		return false, nil
	}
	var count int64
	if err := r.db.WithContext(ctx).
		Table("login_histories AS login_session").
		Joins(`
			INNER JOIN refresh_tokens AS refresh_session
				ON refresh_session.user_id = login_session.user_id
				AND refresh_session.session_id = login_session.session_id
		`).
		Where(
			`login_session.user_id = ?
				AND login_session.session_id = ?
				AND login_session.is_active = ?
				AND refresh_session.revoked = ?
				AND refresh_session.expires_at > ?`,
			userID,
			sessionID,
			true,
			false,
			time.Now(),
		).
		Limit(1).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// CleanupExpiredTokens 清理过期令牌
func (r *GormTokenRepository) CleanupExpiredTokens(ctx context.Context) error {
	return r.db.WithContext(ctx).Where("expires_at < ?", time.Now()).Delete(&RefreshToken{}).Error
}

// CreateEmailVerification 创建邮箱验证
func (r *GormTokenRepository) CreateEmailVerification(ctx context.Context, verification *EmailVerification) error {
	if verification == nil || strings.TrimSpace(verification.Token) == "" {
		return ErrInvalidToken
	}
	plaintext := verification.Token
	verification.Token = bearerTokenDigest("email-verification", plaintext)
	defer func() {
		verification.Token = plaintext
	}()
	return r.db.WithContext(ctx).Create(verification).Error
}

// GetEmailVerification 获取邮箱验证
func (r *GormTokenRepository) GetEmailVerification(ctx context.Context, token string) (*EmailVerification, error) {
	var verification EmailVerification
	if err := r.db.WithContext(ctx).
		Where(
			"token = ? AND used = false AND expires_at > ?",
			bearerTokenDigest("email-verification", token),
			time.Now(),
		).
		First(&verification).Error; err != nil {
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
	result := r.db.WithContext(ctx).
		Model(&EmailVerification{}).
		Where(
			"token = ? AND used = ? AND expires_at > ?",
			bearerTokenDigest("email-verification", token),
			false,
			now,
		).
		Updates(map[string]interface{}{
			"used":    true,
			"used_at": &now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrInvalidToken
	}
	return nil
}

func (r *GormTokenRepository) VerifyEmailWithToken(
	ctx context.Context,
	token string,
	verifiedAt time.Time,
) (uint, error) {
	digest := bearerTokenDigest("email-verification", token)
	var userID uint
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var verification EmailVerification
		if err := tx.Where(
			"token = ? AND used = ? AND expires_at > ?",
			digest,
			false,
			verifiedAt,
		).Take(&verification).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInvalidToken
			}
			return err
		}
		result := tx.Model(&EmailVerification{}).
			Where(
				"id = ? AND used = ? AND expires_at > ?",
				verification.ID,
				false,
				verifiedAt,
			).
			Updates(map[string]interface{}{
				"used":    true,
				"used_at": &verifiedAt,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrInvalidToken
		}
		result = tx.Model(&models.User{}).
			Where("id = ?", verification.UserID).
			Updates(map[string]interface{}{
				"email_verified":    true,
				"email_verified_at": &verifiedAt,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrUserNotFound
		}
		userID = verification.UserID
		return nil
	})
	return userID, err
}

// CreatePasswordReset 创建密码重置
func (r *GormTokenRepository) CreatePasswordReset(ctx context.Context, reset *PasswordReset) error {
	if reset == nil || strings.TrimSpace(reset.Token) == "" {
		return ErrInvalidToken
	}
	plaintext := reset.Token
	reset.Token = bearerTokenDigest("password-reset", plaintext)
	defer func() {
		reset.Token = plaintext
	}()
	return r.db.WithContext(ctx).Create(reset).Error
}

// GetPasswordReset 获取密码重置
func (r *GormTokenRepository) GetPasswordReset(ctx context.Context, token string) (*PasswordReset, error) {
	var reset PasswordReset
	if err := r.db.WithContext(ctx).
		Where(
			"token = ? AND used = false AND expires_at > ?",
			bearerTokenDigest("password-reset", token),
			time.Now(),
		).
		First(&reset).Error; err != nil {
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
	result := r.db.WithContext(ctx).
		Model(&PasswordReset{}).
		Where(
			"token = ? AND used = ? AND expires_at > ?",
			bearerTokenDigest("password-reset", token),
			false,
			now,
		).
		Updates(map[string]interface{}{
			"used":    true,
			"used_at": &now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrInvalidToken
	}
	return nil
}

func (r *GormTokenRepository) ResetPasswordWithToken(
	ctx context.Context,
	token, passwordHash string,
	changedAt time.Time,
) (uint, error) {
	if strings.TrimSpace(passwordHash) == "" {
		return 0, ErrInvalidToken
	}
	digest := bearerTokenDigest("password-reset", token)
	var userID uint
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var reset PasswordReset
		if err := tx.Where(
			"token = ? AND used = ? AND expires_at > ?",
			digest,
			false,
			changedAt,
		).Take(&reset).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInvalidToken
			}
			return err
		}
		result := tx.Model(&PasswordReset{}).
			Where(
				"id = ? AND used = ? AND expires_at > ?",
				reset.ID,
				false,
				changedAt,
			).
			Updates(map[string]interface{}{
				"used":    true,
				"used_at": &changedAt,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrInvalidToken
		}
		result = tx.Model(&models.User{}).
			Where("id = ?", reset.UserID).
			Updates(map[string]interface{}{
				"password_hash":     passwordHash,
				"password_reset_at": &changedAt,
				"login_attempts":    0,
				"locked_until":      nil,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrUserNotFound
		}
		if err := tx.Model(&RefreshToken{}).
			Where("user_id = ? AND revoked = ?", reset.UserID, false).
			Updates(map[string]interface{}{
				"revoked":    true,
				"revoked_at": &changedAt,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.LoginHistory{}).
			Where("user_id = ? AND is_active = ?", reset.UserID, true).
			Updates(map[string]interface{}{
				"is_active":        false,
				"logout_time":      changedAt,
				"last_activity_at": changedAt,
				"login_status":     models.LoginStatusExpired,
				"failure_reason":   "password_reset",
			}).Error; err != nil {
			return err
		}
		userID = reset.UserID
		return nil
	})
	return userID, err
}

// CreateOTPCode 创建OTP验证码
func (r *GormTokenRepository) CreateOTPCode(ctx context.Context, otp *OTPCode) error {
	if otp == nil || strings.TrimSpace(otp.Code) == "" {
		return ErrInvalidOTP
	}
	plaintext := otp.Code
	otp.Code = bearerTokenDigest("otp-code", plaintext)
	defer func() {
		otp.Code = plaintext
	}()
	return r.db.WithContext(ctx).Create(otp).Error
}

// GetOTPCode 获取OTP验证码
func (r *GormTokenRepository) GetOTPCode(ctx context.Context, userID uint, code string) (*OTPCode, error) {
	var otp OTPCode
	if err := r.db.WithContext(ctx).
		Where(
			"user_id = ? AND code = ? AND used = false AND expires_at > ?",
			userID,
			bearerTokenDigest("otp-code", code),
			time.Now(),
		).
		First(&otp).Error; err != nil {
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
	result := r.db.WithContext(ctx).
		Model(&OTPCode{}).
		Where(
			"user_id = ? AND code = ? AND used = ? AND expires_at > ?",
			userID,
			bearerTokenDigest("otp-code", code),
			false,
			now,
		).
		Updates(map[string]interface{}{
			"used":    true,
			"used_at": &now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrInvalidOTP
	}
	return nil
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
	modelProfile := &models.UserProfile{
		UserID:   profile.UserID,
		Avatar:   profile.Avatar,
		Phone:    profile.Phone,
		Timezone: profile.Timezone,
		Language: profile.Language,
	}
	if modelProfile.Timezone == "" {
		modelProfile.Timezone = "Asia/Shanghai"
	}
	if modelProfile.Language == "" {
		modelProfile.Language = "zh-CN"
	}

	if err := r.db.WithContext(ctx).Create(modelProfile).Error; err != nil {
		return err
	}

	profile.ID = modelProfile.ID
	profile.CreatedAt = modelProfile.CreatedAt
	profile.UpdatedAt = modelProfile.UpdatedAt

	return r.syncUserFields(ctx, profile)
}

// GetByUserID 根据用户ID获取资料
func (r *GormProfileRepository) GetByUserID(ctx context.Context, userID uint) (*UserProfile, error) {
	var modelProfile models.UserProfile
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&modelProfile).Error; err != nil {
		return nil, err
	}

	var user models.User
	if err := r.db.WithContext(ctx).Select("id, first_name, last_name, display_name, department, job_title").
		Where("id = ?", userID).
		First(&user).Error; err != nil {
		return nil, err
	}

	displayName := user.DisplayName
	if displayName == "" {
		displayName = profileDisplayName(user.FirstName, user.LastName)
	}

	return &UserProfile{
		ID:          modelProfile.ID,
		UserID:      modelProfile.UserID,
		FirstName:   user.FirstName,
		LastName:    user.LastName,
		DisplayName: displayName,
		Avatar:      modelProfile.Avatar,
		Phone:       modelProfile.Phone,
		Department:  user.Department,
		Position:    user.JobTitle,
		Timezone:    modelProfile.Timezone,
		Language:    modelProfile.Language,
		CreatedAt:   modelProfile.CreatedAt,
		UpdatedAt:   modelProfile.UpdatedAt,
	}, nil
}

// Update 更新用户资料
func (r *GormProfileRepository) Update(ctx context.Context, profile *UserProfile) error {
	var modelProfile models.UserProfile
	err := r.db.WithContext(ctx).Where("user_id = ?", profile.UserID).First(&modelProfile).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		modelProfile = models.UserProfile{
			UserID: profile.UserID,
		}
	} else if err != nil {
		return err
	}

	modelProfile.Avatar = profile.Avatar
	modelProfile.Phone = profile.Phone
	if profile.Timezone != "" {
		modelProfile.Timezone = profile.Timezone
	}
	if profile.Language != "" {
		modelProfile.Language = profile.Language
	}

	if err := r.db.WithContext(ctx).Save(&modelProfile).Error; err != nil {
		return err
	}

	profile.ID = modelProfile.ID
	profile.CreatedAt = modelProfile.CreatedAt
	profile.UpdatedAt = modelProfile.UpdatedAt

	return r.syncUserFields(ctx, profile)
}

// Delete 删除用户资料
func (r *GormProfileRepository) Delete(ctx context.Context, userID uint) error {
	return r.db.WithContext(ctx).Where("user_id = ?", userID).Delete(&models.UserProfile{}).Error
}

func (r *GormProfileRepository) syncUserFields(ctx context.Context, profile *UserProfile) error {
	updates := map[string]interface{}{
		"first_name":   profile.FirstName,
		"last_name":    profile.LastName,
		"display_name": profile.DisplayName,
		"department":   profile.Department,
		"job_title":    profile.Position,
	}
	return r.db.WithContext(ctx).Model(&models.User{}).Where("id = ?", profile.UserID).Updates(updates).Error
}

func profileDisplayName(firstName, lastName string) string {
	if firstName == "" && lastName == "" {
		return ""
	}
	if firstName == "" {
		return lastName
	}
	if lastName == "" {
		return firstName
	}
	return firstName + " " + lastName
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
