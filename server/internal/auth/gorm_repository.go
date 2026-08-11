package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/security"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	if !user.PlatformRole.IsValid() {
		return errors.New("invalid platform role")
	}
	if !isValidUserStatus(user.Status) {
		return ErrInvalidAccountState
	}
	if !user.OTPEnabled && (user.OTPSecret != "" || user.BackupCodes != "") {
		return errors.New("disabled OTP cannot retain credentials")
	}
	// 转换为models.User
	modelUser := &models.User{
		Username:         user.Username,
		Email:            user.Email,
		PasswordHash:     user.PasswordHash,
		PlatformRole:     models.PlatformRole(user.PlatformRole),
		Status:           user.Status,
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
	if !user.PlatformRole.IsValid() {
		return errors.New("invalid platform role")
	}
	if !isValidUserStatus(user.Status) {
		return ErrInvalidAccountState
	}
	// OTP凭据只允许通过专用方法写入，避免普通用户更新把已消费的备用码恢复。
	updates := map[string]interface{}{
		"username":          user.Username,
		"email":             user.Email,
		"password_hash":     user.PasswordHash,
		"platform_role":     models.PlatformRole(user.PlatformRole),
		"status":            user.Status,
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

// ResetFailedLogin 重置失败登录次数，并清除已过期后成功登录的锁定时间。
func (r *GormUserRepository) ResetFailedLogin(ctx context.Context, userID uint) error {
	return r.db.WithContext(ctx).Model(&models.User{}).
		Where("id = ?", userID).
		Updates(map[string]interface{}{
			"login_attempts": 0,
			"locked_until": gorm.Expr(
				"CASE WHEN locked_until <= ? THEN NULL ELSE locked_until END",
				time.Now(),
			),
		}).Error
}

// ChangePasswordAndRevokeSessions atomically changes the password and
// invalidates every active human session. A failure in any statement rolls the
// password update back, so callers never observe a new password with old
// sessions still usable.
func (r *GormUserRepository) ChangePasswordAndRevokeSessions(
	ctx context.Context,
	userID uint,
	passwordHash string,
	changedAt time.Time,
) error {
	if userID == 0 {
		return ErrUserNotFound
	}
	if strings.TrimSpace(passwordHash) == "" || changedAt.IsZero() {
		return errors.New("password hash and change time are required")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.User{}).
			Where("id = ?", userID).
			Updates(map[string]interface{}{
				"password_hash":     passwordHash,
				"password_reset_at": &changedAt,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrUserNotFound
		}
		if err := tx.Model(&RefreshToken{}).
			Where("user_id = ? AND revoked = ?", userID, false).
			Updates(map[string]interface{}{
				"revoked":    true,
				"revoked_at": &changedAt,
			}).Error; err != nil {
			return err
		}
		return tx.Model(&models.LoginHistory{}).
			Where("user_id = ? AND is_active = ?", userID, true).
			Updates(map[string]interface{}{
				"is_active":        false,
				"logout_time":      changedAt,
				"last_activity_at": changedAt,
				"login_status":     models.LoginStatusExpired,
				"failure_reason":   "password_changed",
			}).Error
	})
}

func (r *GormUserRepository) ConfigureOTP(
	ctx context.Context,
	userID uint,
	secret, backupCodeHashes string,
	enabled bool,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := r.configureOTPWithDB(
			tx,
			userID,
			secret,
			backupCodeHashes,
			enabled,
		); err != nil {
			return err
		}
		// Any remembered device belongs to the previous MFA state. Revoke it
		// in the same transaction so enabling cannot trust a device created
		// without MFA and disabling cannot leave stale step-up credentials.
		now := time.Now()
		result := tx.Model(&models.OTPTrustedDevice{}).
			Where("user_id = ? AND revoked = ?", userID, false).
			Updates(map[string]interface{}{
				"revoked":    true,
				"expires_at": now,
				"updated_at": now,
			})
		if result.Error != nil {
			return result.Error
		}
		return nil
	})
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

func (r *GormUserRepository) RotateBackupCodesWithAudit(
	ctx context.Context,
	expected BackupCodeRotationSnapshot,
	replacementHashes string,
	audit AuthenticationSecurityAuditEvent,
) error {
	if expected.UserID == 0 ||
		!expected.OTPEnabled ||
		expected.PasswordHash == "" ||
		audit.UserID != expected.UserID {
		return ErrBackupCodesChanged
	}
	if _, err := parseBackupCodeHashes(expected.BackupCodes); err != nil {
		return err
	}
	if _, err := parseBackupCodeHashes(replacementHashes); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.User{}).
			Where(
				"id = ? AND two_factor_enabled = ? AND password_hash = ? AND backup_codes = ?",
				expected.UserID,
				expected.OTPEnabled,
				expected.PasswordHash,
				expected.BackupCodes,
			).
			Update("backup_codes", replacementHashes)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrBackupCodesChanged
		}
		if err := tx.Create(&audit).Error; err != nil {
			return err
		}
		return nil
	})
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

func isValidUserStatus(status UserStatus) bool {
	switch status {
	case StatusActive, StatusInactive, StatusSuspended, StatusDeleted:
		return true
	default:
		return false
	}
}

// 辅助函数：转换为认证用户模型
func (r *GormUserRepository) convertToAuthUser(modelUser *models.User) (*User, error) {
	if modelUser == nil {
		return nil, ErrUserNotFound
	}
	if !isValidUserStatus(modelUser.Status) {
		return nil, ErrInvalidAccountState
	}
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
		PlatformRole:      PlatformRole(modelUser.PlatformRole),
		Status:            modelUser.Status,
		EmailVerified:     modelUser.EmailVerified,
		EmailVerifiedAt:   modelUser.EmailVerifiedAt,
		LastLoginAt:       modelUser.LastLoginAt,
		FailedLoginCount:  modelUser.LoginAttempts,
		LockedUntil:       modelUser.LockedUntil,
		OTPEnabled:        modelUser.TwoFactorEnabled,
		OTPSecret:         otpSecret,
		OTPStorageHash:    loginOTPStorageHash(modelUser.TwoFactorSecret),
		BackupCodes:       modelUser.BackupCodes,
		PasswordChangedAt: modelUser.PasswordResetAt,
		CreatedAt:         modelUser.CreatedAt,
		UpdatedAt:         modelUser.UpdatedAt,
	}, nil
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

func loginOTPStorageHash(storedSecret string) string {
	if storedSecret == "" {
		return ""
	}
	return bearerTokenDigest("login-otp-storage", storedSecret)
}

type preparedLoginSessionRows struct {
	refresh RefreshToken
	history models.LoginHistory
	attempt LoginAttempt
}

func prepareLoginSessionRows(
	userID uint,
	committedAt time.Time,
	refresh *RefreshToken,
	history *models.LoginHistory,
	attempt *LoginAttempt,
) (*preparedLoginSessionRows, error) {
	if err := validateLoginSessionRows(
		userID,
		committedAt,
		refresh,
		history,
		attempt,
	); err != nil {
		return nil, err
	}
	prepared := &preparedLoginSessionRows{
		refresh: *refresh,
		history: *history,
		attempt: *attempt,
	}
	prepared.refresh.ID = 0
	prepared.refresh.Token = bearerTokenDigest(
		"refresh-token",
		refresh.Token,
	)
	prepared.history.ID = 0
	prepared.attempt.ID = 0
	prepared.attempt.User = nil
	return prepared, nil
}

func createLoginSessionRowsTx(
	tx *gorm.DB,
	prepared *preparedLoginSessionRows,
) error {
	if tx == nil || prepared == nil {
		return ErrAtomicLoginSessionUnavailable
	}
	if err := tx.Create(&prepared.refresh).Error; err != nil {
		return err
	}
	if err := tx.Create(&prepared.history).Error; err != nil {
		return err
	}
	return tx.Create(&prepared.attempt).Error
}

func copyPreparedLoginSessionRowIDs(
	refresh *RefreshToken,
	history *models.LoginHistory,
	attempt *LoginAttempt,
	prepared *preparedLoginSessionRows,
) {
	if refresh == nil || history == nil || attempt == nil || prepared == nil {
		return
	}
	refresh.ID = prepared.refresh.ID
	history.ID = prepared.history.ID
	attempt.ID = prepared.attempt.ID
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

// CommitLoginSession is the sole successful-login persistence path. The user
// row is the linearization point shared with logout-all. Trusted-device
// revalidation or creation, refresh authority, and login-history activation
// therefore either commit together or leave no session behind.
func (r *GormTokenRepository) CommitLoginSession(
	ctx context.Context,
	command *LoginSessionCommit,
) error {
	if err := validateLoginSessionCommit(command); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	preparedSession, err := prepareLoginSessionRows(
		command.UserID,
		command.CommittedAt,
		command.RefreshToken,
		command.LoginHistory,
		command.SuccessfulAttempt,
	)
	if err != nil {
		return err
	}
	var storedNewDevice *models.OTPTrustedDevice
	if command.NewTrustedDevice != nil {
		copied := *command.NewTrustedDevice
		copied.ID = 0
		storedNewDevice = &copied
	}

	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		lockedUser, err := lockAuthUserForUpdate(tx, command.UserID)
		if err != nil {
			return err
		}
		if !loginPrincipalStillMatches(
			lockedUser,
			command.ExpectedPrincipal,
			command.CommittedAt,
		) {
			return ErrInvalidCredentials
		}
		if err := lockAndMatchEmailVerificationPolicyTx(
			tx,
			command.ExpectedEmailPolicy,
		); err != nil {
			return err
		}
		if err := consumeLoginBackupCodeTx(
			tx,
			lockedUser,
			command.BackupCode,
		); err != nil {
			return err
		}
		userUpdate := tx.Model(&models.User{}).
			Where("id = ?", command.UserID).
			Updates(map[string]interface{}{
				"login_attempts": 0,
				"locked_until":   nil,
				"last_login_at":  command.CommittedAt,
			})
		if userUpdate.Error != nil {
			return userUpdate.Error
		}
		if userUpdate.RowsAffected != 1 {
			return ErrUserNotFound
		}
		if command.TrustedDeviceTokenHash != "" {
			if err := revalidateAndTouchTrustedDeviceTx(
				tx,
				command,
			); err != nil {
				return err
			}
		} else if storedNewDevice != nil {
			if err := tx.Create(storedNewDevice).Error; err != nil {
				return err
			}
		}
		return createLoginSessionRowsTx(tx, preparedSession)
	})
	if err != nil {
		return err
	}
	copyPreparedLoginSessionRowIDs(
		command.RefreshToken,
		command.LoginHistory,
		command.SuccessfulAttempt,
		preparedSession,
	)
	if command.NewTrustedDevice != nil && storedNewDevice != nil {
		command.NewTrustedDevice.ID = storedNewDevice.ID
	}
	return nil
}

func validateLoginSessionCommit(command *LoginSessionCommit) error {
	if command == nil ||
		command.ExpectedPrincipal == nil ||
		command.ExpectedEmailPolicy == nil {
		return ErrInvalidToken
	}
	if err := validateLoginSessionRows(
		command.UserID,
		command.CommittedAt,
		command.RefreshToken,
		command.LoginHistory,
		command.SuccessfulAttempt,
	); err != nil {
		return err
	}
	principal := command.ExpectedPrincipal
	if strings.TrimSpace(principal.Email) == "" ||
		strings.TrimSpace(principal.PasswordHash) == "" ||
		!principal.PlatformRole.IsValid() ||
		principal.Status != StatusActive ||
		(principal.OTPEnabled && principal.OTPStorageHash == "") ||
		(!principal.OTPEnabled && principal.OTPStorageHash != "") {
		return ErrInvalidToken
	}
	if strings.TrimSpace(command.BackupCode) != "" &&
		!principal.OTPEnabled {
		return ErrInvalidToken
	}
	if command.TrustedDeviceTokenHash != "" &&
		command.NewTrustedDevice != nil {
		return ErrTrustedDeviceInvalid
	}
	if command.TrustedDeviceExpiresAt != nil &&
		(command.TrustedDeviceTokenHash == "" ||
			!command.TrustedDeviceExpiresAt.After(command.CommittedAt)) {
		return ErrTrustedDeviceInvalid
	}
	if device := command.NewTrustedDevice; device != nil &&
		(device.UserID != command.UserID ||
			strings.TrimSpace(device.DeviceTokenHash) == "" ||
			!device.ExpiresAt.After(command.CommittedAt) ||
			device.Revoked) {
		return ErrTrustedDeviceInvalid
	}
	return nil
}

func validateLoginSessionRows(
	userID uint,
	committedAt time.Time,
	refresh *RefreshToken,
	history *models.LoginHistory,
	attempt *LoginAttempt,
) error {
	if userID == 0 ||
		committedAt.IsZero() ||
		refresh == nil ||
		history == nil ||
		attempt == nil ||
		refresh.UserID != userID ||
		history.UserID != userID ||
		strings.TrimSpace(refresh.Token) == "" ||
		strings.TrimSpace(refresh.SessionID) == "" ||
		len(strings.TrimSpace(refresh.SessionID)) > 128 ||
		refresh.SessionID != history.SessionID ||
		!refresh.ExpiresAt.After(committedAt) ||
		!refresh.CreatedAt.Equal(committedAt) ||
		!history.LoginTime.Equal(committedAt) ||
		history.LastActivityAt == nil ||
		!history.LastActivityAt.Equal(committedAt) ||
		!history.IsActive ||
		history.LoginStatus != models.LoginStatusSuccess ||
		!history.LoginMethod.IsValid() {
		return ErrInvalidToken
	}
	if attempt.UserID == nil ||
		*attempt.UserID != userID ||
		strings.TrimSpace(attempt.Email) == "" ||
		attempt.Email != history.Email ||
		!attempt.Success ||
		strings.TrimSpace(attempt.FailReason) != "" ||
		!attempt.CreatedAt.Equal(committedAt) {
		return ErrInvalidToken
	}
	return nil
}

// lockAndMatchEmailVerificationPolicyTx closes the interval between reading
// the dynamic policy and committing authentication state. PostgreSQL's table
// lock covers the no-row/default-policy case as well as updates to an existing
// row; SQLite's transaction-level locking provides the equivalent test and
// development behavior.
func lockAndMatchEmailVerificationPolicyTx(
	tx *gorm.DB,
	expected *EmailVerificationPolicySnapshot,
) error {
	if tx == nil || expected == nil {
		return ErrEmailVerificationPolicyUnavailable
	}
	if tx.Dialector.Name() == "postgres" {
		if err := tx.Exec(
			`LOCK TABLE "email_configs" IN SHARE MODE`,
		).Error; err != nil {
			return fmt.Errorf(
				"%w: lock policy: %v",
				ErrEmailVerificationPolicyUnavailable,
				err,
			)
		}
	}

	var config models.EmailConfig
	err := emailVerificationPolicyLockQuery(tx).
		Take(&config).Error
	enabled := false
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf(
			"%w: load policy: %v",
			ErrEmailVerificationPolicyUnavailable,
			err,
		)
	}
	if err == nil {
		enabled = config.EmailVerificationEnabled
	}
	if enabled != expected.Enabled {
		return ErrEmailVerificationPolicyChanged
	}
	return nil
}

func emailVerificationPolicyLockQuery(tx *gorm.DB) *gorm.DB {
	return tx.Model(&models.EmailConfig{}).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("is_active = ?", true).
		Order("created_at DESC").
		Order("id DESC")
}

func consumeLoginBackupCodeTx(
	tx *gorm.DB,
	user *lockedAuthUser,
	code string,
) error {
	if strings.TrimSpace(code) == "" {
		return nil
	}
	if tx == nil || user == nil || user.ID == 0 || !user.TwoFactorEnabled {
		return ErrInvalidOTP
	}
	hashes, err := parseBackupCodeHashes(user.BackupCodes)
	if err != nil {
		return err
	}
	index := matchBackupCode(hashes, code)
	if index < 0 {
		return ErrInvalidOTP
	}
	remaining := append(append([]string{}, hashes[:index]...), hashes[index+1:]...)
	result := tx.Model(&models.User{}).
		Where(
			"id = ? AND two_factor_enabled = ? AND backup_codes = ?",
			user.ID,
			true,
			user.BackupCodes,
		).
		Update("backup_codes", strings.Join(remaining, ","))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrInvalidOTP
	}
	user.BackupCodes = strings.Join(remaining, ",")
	return nil
}

func revalidateAndTouchTrustedDeviceTx(
	tx *gorm.DB,
	command *LoginSessionCommit,
) error {
	var device models.OTPTrustedDevice
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"user_id = ? AND device_token_hash = ? AND revoked = ? AND expires_at > ?",
			command.UserID,
			command.TrustedDeviceTokenHash,
			false,
			command.CommittedAt,
		).
		Take(&device).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrTrustedDeviceInvalid
		}
		return err
	}
	updates := map[string]interface{}{
		"last_used_at": command.CommittedAt,
		"last_ip":      command.TrustedDeviceIP,
		"user_agent":   command.TrustedDeviceUserAgent,
	}
	if command.TrustedDeviceName != "" {
		updates["device_name"] = command.TrustedDeviceName
	}
	if command.TrustedDeviceExpiresAt != nil {
		updates["expires_at"] = *command.TrustedDeviceExpiresAt
	}
	result := tx.Model(&models.OTPTrustedDevice{}).
		Where(
			"id = ? AND user_id = ? AND device_token_hash = ? AND revoked = ? AND expires_at > ?",
			device.ID,
			command.UserID,
			command.TrustedDeviceTokenHash,
			false,
			command.CommittedAt,
		).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrTrustedDeviceInvalid
	}
	return nil
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

const refreshRotationReplayWindow = 30 * time.Second

func (r *GormTokenRepository) GetRefreshTokenForRotation(
	ctx context.Context,
	token string,
) (*RefreshToken, error) {
	var refreshToken RefreshToken
	now := time.Now().UTC()
	if err := r.db.WithContext(ctx).
		Where(
			`token = ? AND expires_at > ? AND (
				revoked = ? OR (
					revoked = ? AND rotated_at IS NOT NULL AND rotated_at > ?
					AND replaced_by_token <> ''
				)
			)`,
			bearerTokenDigest("refresh-token", token),
			now,
			false,
			true,
			now.Add(-refreshRotationReplayWindow),
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
	rotatedAt time.Time,
) error {
	if strings.TrimSpace(currentToken) == "" ||
		replacement == nil ||
		strings.TrimSpace(replacement.Token) == "" ||
		replacement.UserID == 0 ||
		strings.TrimSpace(replacement.SessionID) == "" ||
		rotatedAt.IsZero() {
		return ErrInvalidToken
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	storedReplacement := *replacement
	storedReplacement.ID = 0
	storedReplacement.Token = bearerTokenDigest("refresh-token", replacement.Token)
	rotatedAt = rotatedAt.UTC().Truncate(time.Microsecond)
	storedReplacement.CreatedAt = rotatedAt
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&RefreshToken{}).
			Where(
				"token = ? AND revoked = ? AND expires_at > ? AND user_id = ? AND session_id = ?",
				bearerTokenDigest("refresh-token", currentToken),
				false,
				rotatedAt,
				replacement.UserID,
				replacement.SessionID,
			).
			Updates(map[string]interface{}{
				"revoked":           true,
				"revoked_at":        &rotatedAt,
				"rotated_at":        &rotatedAt,
				"replaced_by_token": storedReplacement.Token,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrInvalidToken
		}
		if err := ctx.Err(); err != nil {
			return err
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

// RevokeAllUserTokens atomically revokes every persisted authentication state
// that can keep a human signed in or bypass OTP on a remembered device.
func (r *GormTokenRepository) RevokeAllUserTokens(ctx context.Context, userID uint) error {
	now := time.Now()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockAuthUserForUpdate(tx, userID); err != nil {
			return err
		}
		if err := tx.Model(&RefreshToken{}).
			Where("user_id = ? AND revoked = false", userID).
			Updates(map[string]interface{}{
				"revoked":    true,
				"revoked_at": &now,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.LoginHistory{}).
			Where("user_id = ? AND is_active = ?", userID, true).
			Updates(map[string]interface{}{
				"is_active":        false,
				"logout_time":      now,
				"last_activity_at": now,
				"login_status":     models.LoginStatusExpired,
				"failure_reason":   "logout_all",
			}).Error; err != nil {
			return err
		}
		return tx.Model(&models.OTPTrustedDevice{}).
			Where("user_id = ? AND revoked = ?", userID, false).
			Updates(map[string]interface{}{
				"revoked":    true,
				"expires_at": now,
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
	if verification == nil ||
		verification.UserID == 0 ||
		strings.TrimSpace(verification.Email) == "" ||
		strings.TrimSpace(verification.Token) == "" {
		return ErrInvalidToken
	}
	plaintext := verification.Token
	verification.Token = bearerTokenDigest("email-verification", plaintext)
	defer func() {
		verification.Token = plaintext
	}()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthCredentialUserEmail(
			tx,
			verification.UserID,
			verification.Email,
		); err != nil {
			return err
		}
		return tx.Create(verification).Error
	})
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
			"used":            true,
			"used_at":         &now,
			"delivery_secret": "",
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
		if err := lockAuthCredentialUserEmail(
			tx,
			verification.UserID,
			verification.Email,
		); err != nil {
			return err
		}
		// The account row is the serialization point for credential
		// consumption. Re-read after acquiring it so a concurrent consumer
		// cannot continue from a stale pre-lock snapshot.
		if err := tx.Where(
			"id = ? AND token = ? AND used = ? AND expires_at > ?",
			verification.ID,
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
				"used":            true,
				"used_at":         &verifiedAt,
				"delivery_secret": "",
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrInvalidToken
		}
		result = tx.Model(&models.User{}).
			Where(
				"id = ? AND email = ?",
				verification.UserID,
				verification.Email,
			).
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
	if reset == nil ||
		reset.UserID == 0 ||
		strings.TrimSpace(reset.Email) == "" ||
		strings.TrimSpace(reset.Token) == "" {
		return ErrInvalidToken
	}
	plaintext := reset.Token
	reset.Token = bearerTokenDigest("password-reset", plaintext)
	defer func() {
		reset.Token = plaintext
	}()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAuthCredentialUserEmail(
			tx,
			reset.UserID,
			reset.Email,
		); err != nil {
			return err
		}
		return tx.Create(reset).Error
	})
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
			"used":            true,
			"used_at":         &now,
			"delivery_secret": "",
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
		if err := lockAuthCredentialUserEmail(
			tx,
			reset.UserID,
			reset.Email,
		); err != nil {
			return err
		}
		// Different reset links for one account lock the same user row. The
		// presented token is revalidated after that lock, making the account
		// single-winner even when two valid links race.
		if err := tx.Where(
			"id = ? AND token = ? AND used = ? AND expires_at > ?",
			reset.ID,
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
				"user_id = ? AND used = ?",
				reset.UserID,
				false,
			).
			Updates(map[string]interface{}{
				"used":            true,
				"used_at":         &changedAt,
				"delivery_secret": "",
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected < 1 {
			return ErrInvalidToken
		}
		result = tx.Model(&models.User{}).
			Where("id = ? AND email = ?", reset.UserID, reset.Email).
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

// lockAuthCredentialUserEmail binds a one-time credential to the mailbox that
// received it and locks the account as the serialization point for concurrent
// credential consumption. Changing an account email therefore invalidates all
// previously issued verification and password-reset links without consuming
// them or applying partial side effects.
func lockAuthCredentialUserEmail(
	tx *gorm.DB,
	userID uint,
	credentialEmail string,
) error {
	if tx == nil || userID == 0 || strings.TrimSpace(credentialEmail) == "" {
		return ErrInvalidToken
	}
	user, err := lockAuthUserForUpdate(tx, userID)
	if err != nil {
		return err
	}
	if user.Email != credentialEmail {
		return ErrInvalidToken
	}
	return nil
}

type lockedAuthUser struct {
	ID               uint
	Email            string
	PasswordHash     string
	PlatformRole     models.PlatformRole
	Status           models.UserStatus
	EmailVerified    bool
	TwoFactorEnabled bool
	TwoFactorSecret  string
	BackupCodes      string
	LockedUntil      *time.Time
}

// authUserLockQuery is shared by every authentication state transition that
// must linearize against logout-all. Keeping the clause in one builder also
// makes its PostgreSQL SELECT ... FOR UPDATE contract directly testable.
func authUserLockQuery(tx *gorm.DB, userID uint) *gorm.DB {
	return tx.Model(&models.User{}).
		Select(
			"id",
			"email",
			"password_hash",
			"platform_role",
			"status",
			"email_verified",
			"two_factor_enabled",
			"two_factor_secret",
			"backup_codes",
			"locked_until",
		).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", userID)
}

func lockAuthUserForUpdate(
	tx *gorm.DB,
	userID uint,
) (*lockedAuthUser, error) {
	if tx == nil || userID == 0 {
		return nil, ErrUserNotFound
	}
	var user lockedAuthUser
	if err := authUserLockQuery(tx, userID).Take(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

func loginPrincipalStillMatches(
	user *lockedAuthUser,
	expected *LoginPrincipalSnapshot,
	at time.Time,
) bool {
	if user == nil || expected == nil || at.IsZero() {
		return false
	}
	if user.LockedUntil != nil && user.LockedUntil.After(at) {
		return false
	}
	return user.Email == expected.Email &&
		user.PasswordHash == expected.PasswordHash &&
		PlatformRole(user.PlatformRole) == expected.PlatformRole &&
		user.Status == expected.Status &&
		user.Status == StatusActive &&
		user.EmailVerified == expected.EmailVerified &&
		user.TwoFactorEnabled == expected.OTPEnabled &&
		loginOTPStorageHash(user.TwoFactorSecret) == expected.OTPStorageHash
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
		modelProfile.Language = DefaultProfileLanguage
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(modelProfile).Error; err != nil {
			return err
		}
		profile.ID = modelProfile.ID
		profile.CreatedAt = modelProfile.CreatedAt
		profile.UpdatedAt = modelProfile.UpdatedAt
		return syncProfileToUser(tx, profile, false)
	})
}

// GetByUserID 根据用户ID获取资料
func (r *GormProfileRepository) GetByUserID(ctx context.Context, userID uint) (*UserProfile, error) {
	var user models.User
	if err := r.db.WithContext(ctx).
		Select("id", "first_name", "last_name", "display_name", "avatar",
			"phone", "department", "job_title", "timezone", "language").
		Where("id = ?", userID).
		First(&user).Error; err != nil {
		return nil, err
	}

	var modelProfile models.UserProfile
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		First(&modelProfile).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		modelProfile = legacyUserProfileProjection(&user)
		if err := r.db.WithContext(ctx).
			Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "user_id"}},
				DoNothing: true,
			}).
			Create(&modelProfile).Error; err != nil {
			return nil, err
		}
		if modelProfile.ID == 0 {
			if err := r.db.WithContext(ctx).
				Where("user_id = ?", userID).
				First(&modelProfile).Error; err != nil {
				return nil, err
			}
		}
	} else if err != nil {
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

// Patch atomically applies only explicitly requested profile fields. Both this
// path and the controlled avatar upload path lock users first and user_profiles
// second so PostgreSQL serializes compatibility-projection writes without
// deadlocks. SQLite ignores the locking clause but retains identical SQL
// construction for focused repository tests.
func (r *GormProfileRepository) Patch(
	ctx context.Context,
	userID uint,
	patch ProfilePatch,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user models.User
		if err := tx.
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Select(
				"id",
				"first_name",
				"last_name",
				"display_name",
				"avatar",
				"phone",
				"timezone",
				"language",
			).
			Where("id = ?", userID).
			First(&user).Error; err != nil {
			return err
		}

		var modelProfile models.UserProfile
		err := tx.
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ?", userID).
			First(&modelProfile).Error
		profileExists := true
		if errors.Is(err, gorm.ErrRecordNotFound) {
			modelProfile = legacyUserProfileProjection(&user)
			profileExists = false
		} else if err != nil {
			return err
		}

		if patch.Avatar != nil &&
			*patch.Avatar != "" &&
			*patch.Avatar != modelProfile.Avatar {
			return ErrInvalidProfileAvatar
		}

		userUpdates := make(map[string]any)
		profileUpdates := make(map[string]any)

		firstName := user.FirstName
		lastName := user.LastName
		namesChanged := false
		if patch.FirstName != nil && *patch.FirstName != user.FirstName {
			firstName = *patch.FirstName
			userUpdates["first_name"] = firstName
			namesChanged = true
		}
		if patch.LastName != nil && *patch.LastName != user.LastName {
			lastName = *patch.LastName
			userUpdates["last_name"] = lastName
			namesChanged = true
		}
		if namesChanged {
			userUpdates["display_name"] = profileDisplayName(firstName, lastName)
		}

		if patch.Phone != nil && *patch.Phone != modelProfile.Phone {
			profileUpdates["phone"] = *patch.Phone
			userUpdates["phone"] = *patch.Phone
			userUpdates["phone_verified"] = false
			userUpdates["phone_verified_at"] = nil
		}
		if patch.Avatar != nil {
			switch {
			case *patch.Avatar == "":
				// Empty is an explicit clear. Clear both projections even if a
				// legacy users row drifted from the authoritative profile.
				if modelProfile.Avatar != "" {
					profileUpdates["avatar"] = ""
				}
				if user.Avatar != "" {
					userUpdates["avatar"] = ""
				}
			case *patch.Avatar == modelProfile.Avatar:
				// Preserving the exact authoritative value is a persistent
				// no-op; it must never restore a stale compatibility value.
			}
		}
		if patch.Timezone != nil && *patch.Timezone != modelProfile.Timezone {
			profileUpdates["timezone"] = *patch.Timezone
			userUpdates["timezone"] = *patch.Timezone
		}
		if patch.Language != nil && *patch.Language != modelProfile.Language {
			profileUpdates["language"] = *patch.Language
			userUpdates["language"] = *patch.Language
		}

		if !profileExists {
			if avatar, ok := profileUpdates["avatar"]; ok {
				modelProfile.Avatar = avatar.(string)
			}
			if phone, ok := profileUpdates["phone"]; ok {
				modelProfile.Phone = phone.(string)
			}
			if timezone, ok := profileUpdates["timezone"]; ok {
				modelProfile.Timezone = timezone.(string)
			}
			if language, ok := profileUpdates["language"]; ok {
				modelProfile.Language = language.(string)
			}
			if err := tx.Create(&modelProfile).Error; err != nil {
				return err
			}
		} else if len(profileUpdates) > 0 {
			if err := tx.Model(&models.UserProfile{}).
				Where("user_id = ?", userID).
				Updates(profileUpdates).Error; err != nil {
				return err
			}
		}

		if len(userUpdates) > 0 {
			if err := tx.Model(&models.User{}).
				Where("id = ?", userID).
				Updates(userUpdates).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// Delete 删除用户资料
func (r *GormProfileRepository) Delete(ctx context.Context, userID uint) error {
	return r.db.WithContext(ctx).Where("user_id = ?", userID).Delete(&models.UserProfile{}).Error
}

func syncProfileToUser(
	db *gorm.DB,
	profile *UserProfile,
	phoneChanged bool,
) error {
	updates := map[string]interface{}{
		"first_name":   profile.FirstName,
		"last_name":    profile.LastName,
		"display_name": profile.DisplayName,
		"avatar":       profile.Avatar,
		"phone":        profile.Phone,
		"department":   profile.Department,
		"job_title":    profile.Position,
		"timezone":     profile.Timezone,
		"language":     profile.Language,
	}
	if phoneChanged {
		updates["phone_verified"] = false
		updates["phone_verified_at"] = nil
	}
	return db.Model(&models.User{}).
		Where("id = ?", profile.UserID).
		Updates(updates).Error
}

func legacyUserProfileProjection(user *models.User) models.UserProfile {
	timezone := user.Timezone
	if timezone == "" {
		timezone = "Asia/Shanghai"
	}
	language := user.Language
	if language == "" {
		language = DefaultProfileLanguage
	}
	return models.UserProfile{
		UserID:   user.ID,
		Avatar:   user.Avatar,
		Phone:    user.Phone,
		Timezone: timezone,
		Language: language,
	}
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

// Update applies only mutable fields to the exact still-active owner row.
// Save is intentionally forbidden: a stale in-memory Revoked=false value must
// never resurrect a device that logout-all revoked concurrently.
func (r *GormTrustedDeviceRepository) Update(ctx context.Context, device *models.OTPTrustedDevice) error {
	if device == nil ||
		device.ID == 0 ||
		device.UserID == 0 ||
		strings.TrimSpace(device.DeviceTokenHash) == "" ||
		device.ExpiresAt.IsZero() {
		return ErrTrustedDeviceInvalid
	}
	now := time.Now()
	updates := map[string]interface{}{
		"device_name":  device.DeviceName,
		"last_used_at": device.LastUsedAt,
		"last_ip":      device.LastIP,
		"user_agent":   device.UserAgent,
		"expires_at":   device.ExpiresAt,
	}
	if device.Revoked {
		updates["revoked"] = true
	}
	result := r.db.WithContext(ctx).
		Model(&models.OTPTrustedDevice{}).
		Where(
			"id = ? AND user_id = ? AND device_token_hash = ? AND revoked = ? AND expires_at > ?",
			device.ID,
			device.UserID,
			device.DeviceTokenHash,
			false,
			now,
		).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrTrustedDeviceInvalid
	}
	return nil
}

// ListActiveDevices revokes expired records before returning the user's
// currently usable devices. Expired rows therefore never consume the quota or
// cause a still-valid remembered device to be evicted.
func (r *GormTrustedDeviceRepository) ListActiveDevices(ctx context.Context, userID uint) ([]*models.OTPTrustedDevice, error) {
	var devices []*models.OTPTrustedDevice
	now := time.Now()
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.OTPTrustedDevice{}).
			Where(
				"user_id = ? AND revoked = ? AND expires_at <= ?",
				userID,
				false,
				now,
			).
			Update("revoked", true).Error; err != nil {
			return err
		}
		return tx.
			Where(
				"user_id = ? AND revoked = ? AND expires_at > ?",
				userID,
				false,
				now,
			).
			Order("COALESCE(last_used_at, created_at) DESC").
			Find(&devices).Error
	})
	return devices, err
}
