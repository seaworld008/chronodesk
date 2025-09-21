package auth

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// PostgreSQLUserRepository PostgreSQL用户仓库实现
type PostgreSQLUserRepository struct {
	db *sql.DB
}

// NewPostgreSQLUserRepository 创建PostgreSQL用户仓库
func NewPostgreSQLUserRepository(db *sql.DB) *PostgreSQLUserRepository {
	return &PostgreSQLUserRepository{db: db}
}

// Create 创建用户
func (r *PostgreSQLUserRepository) Create(ctx context.Context, user *User) error {
	query := `
		INSERT INTO users (username, email, password_hash, role, status, email_verified, 
					   otp_enabled, otp_secret, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id`

	now := time.Now()
	err := r.db.QueryRowContext(ctx, query,
		user.Username, user.Email, user.PasswordHash, user.Role, user.Status,
		user.EmailVerified, user.OTPEnabled, user.OTPSecret, now, now,
	).Scan(&user.ID)

	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	user.CreatedAt = now
	user.UpdatedAt = now
	return nil
}

// GetByID 根据ID获取用户
func (r *PostgreSQLUserRepository) GetByID(ctx context.Context, id uint) (*User, error) {
	query := `
		SELECT id, username, email, password_hash, role, status, email_verified,
			   otp_enabled, otp_secret, last_login_at, failed_login_attempts,
			   locked_until, created_at, updated_at
		FROM users WHERE id = $1`

	user := &User{}
	var lastLoginAt, lockedUntil sql.NullTime

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&user.ID, &user.Username, &user.Email, &user.PasswordHash,
		&user.Role, &user.Status, &user.EmailVerified, &user.OTPEnabled,
		&user.OTPSecret, &lastLoginAt, &user.FailedLoginCount,
		&lockedUntil, &user.CreatedAt, &user.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("failed to get user by ID: %w", err)
	}

	if lastLoginAt.Valid {
		user.LastLoginAt = &lastLoginAt.Time
	}
	if lockedUntil.Valid {
		user.LockedUntil = &lockedUntil.Time
	}

	return user, nil
}

// GetByEmail 根据邮箱获取用户
func (r *PostgreSQLUserRepository) GetByEmail(ctx context.Context, email string) (*User, error) {
	query := `
		SELECT id, username, email, password_hash, role, status, email_verified,
			   otp_enabled, otp_secret, last_login_at, failed_login_attempts,
			   locked_until, created_at, updated_at
		FROM users WHERE email = $1`

	user := &User{}
	var lastLoginAt, lockedUntil sql.NullTime

	err := r.db.QueryRowContext(ctx, query, email).Scan(
		&user.ID, &user.Username, &user.Email, &user.PasswordHash,
		&user.Role, &user.Status, &user.EmailVerified, &user.OTPEnabled,
		&user.OTPSecret, &lastLoginAt, &user.FailedLoginCount,
		&lockedUntil, &user.CreatedAt, &user.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("failed to get user by email: %w", err)
	}

	if lastLoginAt.Valid {
		user.LastLoginAt = &lastLoginAt.Time
	}
	if lockedUntil.Valid {
		user.LockedUntil = &lockedUntil.Time
	}

	return user, nil
}

// GetByUsername 根据用户名获取用户
func (r *PostgreSQLUserRepository) GetByUsername(ctx context.Context, username string) (*User, error) {
	query := `
		SELECT id, username, email, password_hash, role, status, email_verified,
			   otp_enabled, otp_secret, last_login_at, failed_login_attempts,
			   locked_until, created_at, updated_at
		FROM users WHERE username = $1`

	user := &User{}
	var lastLoginAt, lockedUntil sql.NullTime

	err := r.db.QueryRowContext(ctx, query, username).Scan(
		&user.ID, &user.Username, &user.Email, &user.PasswordHash,
		&user.Role, &user.Status, &user.EmailVerified, &user.OTPEnabled,
		&user.OTPSecret, &lastLoginAt, &user.FailedLoginCount,
		&lockedUntil, &user.CreatedAt, &user.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("failed to get user by username: %w", err)
	}

	if lastLoginAt.Valid {
		user.LastLoginAt = &lastLoginAt.Time
	}
	if lockedUntil.Valid {
		user.LockedUntil = &lockedUntil.Time
	}

	return user, nil
}

// Update 更新用户
func (r *PostgreSQLUserRepository) Update(ctx context.Context, user *User) error {
	query := `
		UPDATE users SET username = $2, email = $3, password_hash = $4, role = $5,
					 status = $6, email_verified = $7, otp_enabled = $8, otp_secret = $9,
					 last_login_at = $10, failed_login_attempts = $11, locked_until = $12,
					 updated_at = $13
		WHERE id = $1`

	now := time.Now()
	user.UpdatedAt = now

	_, err := r.db.ExecContext(ctx, query,
		user.ID, user.Username, user.Email, user.PasswordHash, user.Role,
		user.Status, user.EmailVerified, user.OTPEnabled, user.OTPSecret,
		user.LastLoginAt, user.FailedLoginCount, user.LockedUntil, now,
	)

	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}

	return nil
}

// Delete 删除用户
func (r *PostgreSQLUserRepository) Delete(ctx context.Context, id uint) error {
	query := `DELETE FROM users WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return ErrUserNotFound
	}

	return nil
}

// List 列出用户
func (r *PostgreSQLUserRepository) List(ctx context.Context, offset, limit int) ([]*User, error) {
	query := `
		SELECT id, username, email, password_hash, role, status, email_verified,
			   otp_enabled, otp_secret, last_login_at, failed_login_attempts,
			   locked_until, created_at, updated_at
		FROM users ORDER BY created_at DESC LIMIT $1 OFFSET $2`

	rows, err := r.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		user := &User{}
		var lastLoginAt, lockedUntil sql.NullTime

		err := rows.Scan(
			&user.ID, &user.Username, &user.Email, &user.PasswordHash,
			&user.Role, &user.Status, &user.EmailVerified, &user.OTPEnabled,
			&user.OTPSecret, &lastLoginAt, &user.FailedLoginCount,
			&lockedUntil, &user.CreatedAt, &user.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}

		if lastLoginAt.Valid {
			user.LastLoginAt = &lastLoginAt.Time
		}
		if lockedUntil.Valid {
			user.LockedUntil = &lockedUntil.Time
		}

		users = append(users, user)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate users: %w", err)
	}

	return users, nil
}

// PostgreSQLProfileRepository PostgreSQL用户资料仓库实现
type PostgreSQLProfileRepository struct {
	db *sql.DB
}

// NewPostgreSQLProfileRepository 创建PostgreSQL用户资料仓库
func NewPostgreSQLProfileRepository(db *sql.DB) *PostgreSQLProfileRepository {
	return &PostgreSQLProfileRepository{db: db}
}

// Create 创建用户资料
func (r *PostgreSQLProfileRepository) Create(ctx context.Context, profile *UserProfile) error {
	query := `
		INSERT INTO user_profiles (user_id, first_name, last_name, avatar_url,
							   phone, department, position, bio, timezone,
							   language, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id`

	now := time.Now()
	err := r.db.QueryRowContext(ctx, query,
		profile.UserID, profile.FirstName, profile.LastName, profile.Avatar,
		profile.Phone, profile.Department, profile.Position, "",
		profile.Timezone, profile.Language, now, now,
	).Scan(&profile.ID)

	if err != nil {
		return fmt.Errorf("failed to create user profile: %w", err)
	}

	profile.CreatedAt = now
	profile.UpdatedAt = now
	return nil
}

// GetByUserID 根据用户ID获取资料
func (r *PostgreSQLProfileRepository) GetByUserID(ctx context.Context, userID uint) (*UserProfile, error) {
	query := `
		SELECT id, user_id, first_name, last_name, avatar_url, phone,
			   department, position, bio, timezone, language, created_at, updated_at
		FROM user_profiles WHERE user_id = $1`

	profile := &UserProfile{}
	var avatar, phone, department, position, timezone, language sql.NullString

	err := r.db.QueryRowContext(ctx, query, userID).Scan(
		&profile.ID, &profile.UserID, &profile.FirstName, &profile.LastName,
		&avatar, &phone, &department, &position, &timezone,
		&language, &profile.CreatedAt, &profile.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrProfileNotFound
		}
		return nil, fmt.Errorf("failed to get user profile: %w", err)
	}

	if avatar.Valid {
		profile.Avatar = avatar.String
	}
	if phone.Valid {
		profile.Phone = phone.String
	}
	if department.Valid {
		profile.Department = department.String
	}
	if position.Valid {
		profile.Position = position.String
	}

	if timezone.Valid {
		profile.Timezone = timezone.String
	}
	if language.Valid {
		profile.Language = language.String
	}

	return profile, nil
}

// Update 更新用户资料
func (r *PostgreSQLProfileRepository) Update(ctx context.Context, profile *UserProfile) error {
	query := `
		UPDATE user_profiles SET first_name = $2, last_name = $3, avatar_url = $4,
							 phone = $5, department = $6, position = $7,
							 timezone = $8, language = $9, updated_at = $10
		WHERE user_id = $1`

	now := time.Now()
	profile.UpdatedAt = now

	_, err := r.db.ExecContext(ctx, query,
		profile.UserID, profile.FirstName, profile.LastName, profile.Avatar,
		profile.Phone, profile.Department, profile.Position,
		profile.Timezone, profile.Language, now,
	)

	if err != nil {
		return fmt.Errorf("failed to update user profile: %w", err)
	}

	return nil
}

// Delete 删除用户资料
func (r *PostgreSQLProfileRepository) Delete(ctx context.Context, userID uint) error {
	query := `DELETE FROM user_profiles WHERE user_id = $1`

	result, err := r.db.ExecContext(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("failed to delete user profile: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return ErrProfileNotFound
	}

	return nil
}

// PostgreSQLTokenRepository PostgreSQL令牌仓库实现
type PostgreSQLTokenRepository struct {
	db *sql.DB
}

// NewPostgreSQLTokenRepository 创建PostgreSQL令牌仓库
func NewPostgreSQLTokenRepository(db *sql.DB) *PostgreSQLTokenRepository {
	return &PostgreSQLTokenRepository{db: db}
}

// Create 创建刷新令牌
func (r *PostgreSQLTokenRepository) Create(ctx context.Context, token *RefreshToken) error {
	query := `
		INSERT INTO refresh_tokens (user_id, token_hash, jti, expires_at,
								ip_address, user_agent, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`

	now := time.Now()
	err := r.db.QueryRowContext(ctx, query,
		token.UserID, token.Token, token.ExpiresAt,
		token.IPAddress, token.UserAgent, now,
	).Scan(&token.ID)

	if err != nil {
		return fmt.Errorf("failed to create refresh token: %w", err)
	}

	token.CreatedAt = now
	return nil
}

// GetByJTI 根据JTI获取令牌
func (r *PostgreSQLTokenRepository) GetByJTI(ctx context.Context, jti string) (*RefreshToken, error) {
	query := `
		SELECT id, user_id, token, expires_at, revoked,
			   ip_address, user_agent, created_at, revoked_at
		FROM refresh_tokens WHERE token = $1`

	token := &RefreshToken{}
	var revokedAt sql.NullTime

	err := r.db.QueryRowContext(ctx, query, jti).Scan(
		&token.ID, &token.UserID, &token.Token,
		&token.ExpiresAt, &token.Revoked, &token.IPAddress,
		&token.UserAgent, &token.CreatedAt, &revokedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrTokenNotFound
		}
		return nil, fmt.Errorf("failed to get refresh token: %w", err)
	}

	if revokedAt.Valid {
		token.RevokedAt = &revokedAt.Time
	}

	return token, nil
}

// RevokeByJTI 根据JTI撤销令牌
func (r *PostgreSQLTokenRepository) RevokeByJTI(ctx context.Context, token string) error {
	query := `UPDATE refresh_tokens SET revoked = true, revoked_at = $1 WHERE token = $2`

	now := time.Now()
	result, err := r.db.ExecContext(ctx, query, now, token)
	if err != nil {
		return fmt.Errorf("failed to revoke refresh token: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return ErrTokenNotFound
	}

	return nil
}

// RevokeByUserID 撤销用户的所有令牌
func (r *PostgreSQLTokenRepository) RevokeByUserID(ctx context.Context, userID uint) error {
	query := `UPDATE refresh_tokens SET revoked = true, revoked_at = $1 WHERE user_id = $2 AND revoked = false`

	now := time.Now()
	_, err := r.db.ExecContext(ctx, query, now, userID)
	if err != nil {
		return fmt.Errorf("failed to revoke user tokens: %w", err)
	}

	return nil
}

// CleanupExpired 清理过期令牌
func (r *PostgreSQLTokenRepository) CleanupExpired(ctx context.Context) error {
	query := `DELETE FROM refresh_tokens WHERE expires_at < $1`

	now := time.Now()
	_, err := r.db.ExecContext(ctx, query, now)
	if err != nil {
		return fmt.Errorf("failed to cleanup expired tokens: %w", err)
	}

	return nil
}

// PostgreSQLLoginAttemptRepository PostgreSQL登录尝试仓库实现
type PostgreSQLLoginAttemptRepository struct {
	db *sql.DB
}

// NewPostgreSQLLoginAttemptRepository 创建PostgreSQL登录尝试仓库
func NewPostgreSQLLoginAttemptRepository(db *sql.DB) *PostgreSQLLoginAttemptRepository {
	return &PostgreSQLLoginAttemptRepository{db: db}
}

// Create 创建登录尝试记录
func (r *PostgreSQLLoginAttemptRepository) Create(ctx context.Context, attempt *LoginAttempt) error {
	query := `
		INSERT INTO login_attempts (user_id, ip_address, user_agent, success,
								failure_reason, attempted_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`

	now := time.Now()
	err := r.db.QueryRowContext(ctx, query,
		attempt.UserID, attempt.IPAddress, attempt.UserAgent,
		attempt.Success, attempt.FailReason, now,
	).Scan(&attempt.ID)

	if err != nil {
		return fmt.Errorf("failed to create login attempt: %w", err)
	}

	attempt.CreatedAt = now
	return nil
}

// GetRecentFailures 获取最近的失败尝试
func (r *PostgreSQLLoginAttemptRepository) GetRecentFailures(ctx context.Context, userID uint, since time.Time) (int, error) {
	query := `
		SELECT COUNT(*) FROM login_attempts
		WHERE user_id = $1 AND success = false AND attempted_at > $2`

	var count int
	err := r.db.QueryRowContext(ctx, query, userID, since).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to get recent failures: %w", err)
	}

	return count, nil
}

// GetRecentFailuresByIP 获取IP的最近失败尝试
func (r *PostgreSQLLoginAttemptRepository) GetRecentFailuresByIP(ctx context.Context, ipAddress string, since time.Time) (int, error) {
	query := `
		SELECT COUNT(*) FROM login_attempts
		WHERE ip_address = $1 AND success = false AND attempted_at > $2`

	var count int
	err := r.db.QueryRowContext(ctx, query, ipAddress, since).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to get recent failures by IP: %w", err)
	}

	return count, nil
}

// CleanupOld 清理旧记录
func (r *PostgreSQLLoginAttemptRepository) CleanupOld(ctx context.Context, before time.Time) error {
	query := `DELETE FROM login_attempts WHERE attempted_at < $1`

	_, err := r.db.ExecContext(ctx, query, before)
	if err != nil {
		return fmt.Errorf("failed to cleanup old login attempts: %w", err)
	}

	return nil
}

// 错误定义
var (
	ErrProfileNotFound = fmt.Errorf("profile not found")
	ErrTokenNotFound   = fmt.Errorf("token not found")
)
