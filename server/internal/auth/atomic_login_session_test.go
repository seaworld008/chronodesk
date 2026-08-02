package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestAuthUserLockQueryEmitsPostgresForUpdate(t *testing.T) {
	db, err := gorm.Open(
		postgres.Open(
			"host=127.0.0.1 user=contract dbname=contract sslmode=disable",
		),
		&gorm.Config{
			DryRun:               true,
			DisableAutomaticPing: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var user lockedAuthUser
	statement := authUserLockQuery(db, 42).Take(&user).Statement
	sql := strings.Join(strings.Fields(statement.SQL.String()), " ")
	if !strings.Contains(sql, `FROM "users"`) ||
		!strings.Contains(sql, `WHERE id = $1`) ||
		!strings.HasSuffix(sql, "FOR UPDATE") {
		t.Fatalf("PostgreSQL auth user lock SQL = %q", sql)
	}
}

func TestEmailVerificationPolicyLockQueryEmitsPostgresForUpdate(t *testing.T) {
	db, err := gorm.Open(
		postgres.Open(
			"host=127.0.0.1 user=contract dbname=contract sslmode=disable",
		),
		&gorm.Config{
			DryRun:               true,
			DisableAutomaticPing: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var policy models.EmailConfig
	statement := emailVerificationPolicyLockQuery(db).Take(&policy).Statement
	sql := strings.Join(strings.Fields(statement.SQL.String()), " ")
	if !strings.Contains(sql, `FROM "email_configs"`) ||
		!strings.Contains(sql, `is_active = $1`) ||
		!strings.HasSuffix(sql, "FOR UPDATE") {
		t.Fatalf("PostgreSQL email policy lock SQL = %q", sql)
	}
}

func TestGormAtomicLoginCommitRejectsChangedAuthenticatedPrincipal(t *testing.T) {
	now := time.Now()
	futureLock := now.Add(time.Hour)
	cases := []struct {
		name    string
		updates map[string]any
	}{
		{
			name: "password reset",
			updates: map[string]any{
				"password_hash":     "changed-after-password-verification",
				"password_reset_at": now,
			},
		},
		{
			name: "account suspension",
			updates: map[string]any{
				"status": models.UserStatusSuspended,
			},
		},
		{
			name: "platform role change",
			updates: map[string]any{
				"platform_role": models.PlatformRolePlatformAdmin,
			},
		},
		{
			name: "email verification revoked",
			updates: map[string]any{
				"email_verified": false,
			},
		},
		{
			name: "MFA state changed",
			updates: map[string]any{
				"two_factor_enabled": true,
				"two_factor_secret":  "changed-encrypted-OTP-secret",
			},
		},
		{
			name: "account locked",
			updates: map[string]any{
				"locked_until": &futureLock,
			},
		},
	}

	for index, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			db := newTrustedDeviceSecurityDB(t)
			const userID = uint(42)
			seedTrustedDeviceSecurityUser(t, db, userID)
			if err := db.Model(&models.User{}).
				Where("id = ?", userID).
				Updates(testCase.updates).Error; err != nil {
				t.Fatal(err)
			}

			repository := NewGormTokenRepository(db).(*GormTokenRepository)
			command := atomicTrustedLoginCommitForTest(
				userID,
				"stale-principal-session-"+string(rune('a'+index)),
				"stale-principal-refresh-"+string(rune('a'+index)),
				"",
				now,
			)
			if err := repository.CommitLoginSession(
				context.Background(),
				command,
			); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("stale principal commit error = %v", err)
			}

			var refreshCount, historyCount, attemptCount int64
			if err := db.Model(&RefreshToken{}).Count(&refreshCount).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&models.LoginHistory{}).Count(&historyCount).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&LoginAttempt{}).Count(&attemptCount).Error; err != nil {
				t.Fatal(err)
			}
			if refreshCount != 0 || historyCount != 0 || attemptCount != 0 {
				t.Fatalf(
					"stale principal left refresh/history/attempt = %d/%d/%d",
					refreshCount,
					historyCount,
					attemptCount,
				)
			}
			var storedUser models.User
			if err := db.Select("last_login_at").
				First(&storedUser, userID).Error; err != nil {
				t.Fatal(err)
			}
			if storedUser.LastLoginAt != nil {
				t.Fatalf(
					"stale principal changed last_login_at = %v",
					storedUser.LastLoginAt,
				)
			}
		})
	}
}

func TestGormAtomicLoginCommitRejectsChangedEmailVerificationPolicy(
	t *testing.T,
) {
	db := newTrustedDeviceSecurityDB(t)
	const userID = uint(42)
	seedTrustedDeviceSecurityUser(t, db, userID)
	if err := db.Model(&models.User{}).
		Where("id = ?", userID).
		Update("email_verified", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.EmailConfig{
		EmailVerificationEnabled: true,
		IsActive:                 true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	command := atomicTrustedLoginCommitForTest(
		userID,
		"stale-email-policy-session",
		"stale-email-policy-refresh",
		"",
		time.Now(),
	)
	command.ExpectedPrincipal.EmailVerified = false
	command.ExpectedEmailPolicy.Enabled = false

	repository := NewGormTokenRepository(db).(*GormTokenRepository)
	if err := repository.CommitLoginSession(
		context.Background(),
		command,
	); !errors.Is(err, ErrEmailVerificationPolicyChanged) {
		t.Fatalf("stale email policy commit error = %v", err)
	}
	var refreshCount, historyCount, attemptCount int64
	if err := db.Model(&RefreshToken{}).Count(&refreshCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.LoginHistory{}).Count(&historyCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&LoginAttempt{}).Count(&attemptCount).Error; err != nil {
		t.Fatal(err)
	}
	if refreshCount != 0 || historyCount != 0 || attemptCount != 0 {
		t.Fatalf(
			"stale policy left refresh/history/attempt = %d/%d/%d",
			refreshCount,
			historyCount,
			attemptCount,
		)
	}
}

func TestGormAtomicLoginBackupCodeRollsBackAndCanCommitOnlyOnce(
	t *testing.T,
) {
	db := newTrustedDeviceSecurityDB(t)
	const userID = uint(42)
	const code = "ABCDEF12"
	seedTrustedDeviceSecurityUser(t, db, userID)
	hashes, err := hashBackupCodes([]string{code})
	if err != nil {
		t.Fatal(err)
	}
	const storedSecret = "encrypted-otp-secret"
	if err := db.Model(&models.User{}).
		Where("id = ?", userID).
		Updates(map[string]any{
			"two_factor_enabled": true,
			"two_factor_secret":  storedSecret,
			"backup_codes":       hashes,
		}).Error; err != nil {
		t.Fatal(err)
	}
	command := atomicTrustedLoginCommitForTest(
		userID,
		"backup-rollback-session",
		"backup-rollback-refresh",
		"",
		time.Now(),
	)
	command.ExpectedPrincipal.OTPEnabled = true
	command.ExpectedPrincipal.OTPStorageHash = loginOTPStorageHash(storedSecret)
	command.BackupCode = code

	const callbackName = "fail-refresh-after-backup-code"
	if err := db.Callback().Create().
		Before("gorm:create").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement != nil &&
				tx.Statement.Schema != nil &&
				tx.Statement.Schema.Table == "refresh_tokens" {
				tx.AddError(errors.New("injected refresh insert failure"))
			}
		}); err != nil {
		t.Fatal(err)
	}
	repository := NewGormTokenRepository(db).(*GormTokenRepository)
	if err := repository.CommitLoginSession(
		context.Background(),
		command,
	); err == nil {
		t.Fatal("expected injected session failure")
	}
	if err := db.Callback().Create().Remove(callbackName); err != nil {
		t.Fatal(err)
	}
	var userAfterRollback models.User
	if err := db.Select("backup_codes", "last_login_at").
		First(&userAfterRollback, userID).Error; err != nil {
		t.Fatal(err)
	}
	if userAfterRollback.BackupCodes != hashes ||
		userAfterRollback.LastLoginAt != nil {
		t.Fatalf(
			"failed commit consumed backup code or updated login: %+v",
			userAfterRollback,
		)
	}

	if err := repository.CommitLoginSession(
		context.Background(),
		command,
	); err != nil {
		t.Fatalf("retry with rolled-back backup code failed: %v", err)
	}
	var userAfterCommit models.User
	if err := db.Select("backup_codes").
		First(&userAfterCommit, userID).Error; err != nil {
		t.Fatal(err)
	}
	if userAfterCommit.BackupCodes != "" {
		t.Fatalf("successful commit retained backup code hashes")
	}

	replay := atomicTrustedLoginCommitForTest(
		userID,
		"backup-replay-session",
		"backup-replay-refresh",
		"",
		time.Now().Add(time.Second),
	)
	replay.ExpectedPrincipal.OTPEnabled = true
	replay.ExpectedPrincipal.OTPStorageHash = loginOTPStorageHash(storedSecret)
	replay.BackupCode = code
	if err := repository.CommitLoginSession(
		context.Background(),
		replay,
	); !errors.Is(err, ErrInvalidOTP) {
		t.Fatalf("backup code replay error = %v", err)
	}
}

type nonAtomicLoginTokenRepository struct {
	TokenRepository
	createCalls int
}

func (repository *nonAtomicLoginTokenRepository) CreateRefreshToken(
	context.Context,
	*RefreshToken,
) error {
	repository.createCalls++
	return nil
}

type unavailableEmailVerificationPolicy struct{}

func (unavailableEmailVerificationPolicy) IsEmailVerificationEnabled(
	context.Context,
) (bool, error) {
	return false, errors.New("injected email verification policy failure")
}

type policyFailureRegistrationUserRepository struct {
	UserRepository
	createCalls int
}

func (*policyFailureRegistrationUserRepository) GetByEmail(
	context.Context,
	string,
) (*User, error) {
	return nil, ErrUserNotFound
}

func (*policyFailureRegistrationUserRepository) GetByUsername(
	context.Context,
	string,
) (*User, error) {
	return nil, ErrUserNotFound
}

func (repository *policyFailureRegistrationUserRepository) Create(
	context.Context,
	*User,
) error {
	repository.createCalls++
	return nil
}

func TestAuthenticationFailsClosedWhenEmailVerificationPolicyIsUnavailable(
	t *testing.T,
) {
	t.Run("registration does not create an implicitly verified account", func(t *testing.T) {
		users := &policyFailureRegistrationUserRepository{}
		service := NewAuthService(
			users,
			nil,
			nil,
			nil,
			nil,
			nil,
			nil,
			unavailableEmailVerificationPolicy{},
			trustedLoginOTPService{},
			trustedLoginPasswordService{},
			nil,
			&AuthConfig{
				EnableRegistration:       true,
				RequireEmailVerification: false,
			},
		)
		response, err := service.Register(
			context.Background(),
			&RegisterRequest{
				Username:        "policy-unavailable",
				Email:           "policy-unavailable@example.test",
				Password:        "CorrectPassword123!",
				ConfirmPassword: "CorrectPassword123!",
			},
			"127.0.0.1",
			"Policy unavailable test",
		)
		if !errors.Is(err, ErrEmailVerificationPolicyUnavailable) ||
			response != nil ||
			users.createCalls != 0 {
			t.Fatalf(
				"registration response/error/createCalls = %+v/%v/%d",
				response,
				err,
				users.createCalls,
			)
		}
	})

	t.Run("login does not fall back to a disabled static default", func(t *testing.T) {
		const (
			userID   = uint(42)
			email    = "policy-login@example.test"
			password = "CorrectPassword123!"
		)
		deviceRepository := &trustedLoginDeviceRepository{}
		historyRepository := &trustedLoginHistoryRepository{}
		tokenRepository := &trustedLoginTokenRepository{
			deviceRepository:  deviceRepository,
			historyRepository: historyRepository,
		}
		service := NewAuthService(
			&trustedLoginUserRepository{user: &User{
				ID:            userID,
				Username:      "policy-login",
				Email:         email,
				PasswordHash:  password,
				PlatformRole:  PlatformRoleMember,
				Status:        StatusActive,
				EmailVerified: false,
			}},
			&trustedLoginProfileRepository{},
			tokenRepository,
			&trustedLoginAttemptRepository{},
			historyRepository,
			deviceRepository,
			nil,
			unavailableEmailVerificationPolicy{},
			trustedLoginOTPService{},
			trustedLoginPasswordService{},
			mustTestJWTManager(t, time.Hour, 24*time.Hour),
			&AuthConfig{
				AccessTokenExpire:        time.Hour,
				RefreshTokenExpire:       24 * time.Hour,
				MaxFailedLogins:          5,
				RequireEmailVerification: false,
			},
		)
		response, err := service.Login(
			context.Background(),
			&LoginRequest{Email: email, Password: password},
			"127.0.0.1",
			"Policy unavailable test",
		)
		if !errors.Is(err, ErrEmailVerificationPolicyUnavailable) ||
			response != nil ||
			len(tokenRepository.refreshTokens) != 0 {
			t.Fatalf(
				"login response/error/refreshes = %+v/%v/%d",
				response,
				err,
				len(tokenRepository.refreshTokens),
			)
		}
		status, _ := loginFailureHTTPResponse(err)
		if status != 503 {
			t.Fatalf("login policy failure status = %d", status)
		}
	})
}

func TestSuccessfulLoginFailsClosedWithoutAtomicSessionCapability(t *testing.T) {
	const (
		userID   = uint(42)
		email    = "missing-atomic-login@example.test"
		password = "CorrectPassword123!"
	)
	userRepository := &trustedLoginUserRepository{user: &User{
		ID:            userID,
		Username:      "missing-atomic-login",
		Email:         email,
		PasswordHash:  password,
		PlatformRole:  PlatformRoleMember,
		Status:        StatusActive,
		EmailVerified: true,
	}}
	tokenRepository := &nonAtomicLoginTokenRepository{}
	historyRepository := &trustedLoginHistoryRepository{}
	service := NewAuthService(
		userRepository,
		&trustedLoginProfileRepository{},
		tokenRepository,
		&trustedLoginAttemptRepository{},
		historyRepository,
		nil,
		nil,
		otpAuditEmailConfig{},
		trustedLoginOTPService{},
		trustedLoginPasswordService{},
		mustTestJWTManager(t, time.Hour, 24*time.Hour),
		&AuthConfig{
			AccessTokenExpire:        time.Hour,
			RefreshTokenExpire:       24 * time.Hour,
			MaxFailedLogins:          5,
			RequireEmailVerification: false,
		},
	)

	response, err := service.Login(
		context.Background(),
		&LoginRequest{Email: email, Password: password},
		"127.0.0.1",
		"Atomic capability test",
	)
	if !errors.Is(err, ErrAtomicLoginSessionUnavailable) || response != nil {
		t.Fatalf("login response/error = %+v/%v", response, err)
	}
	if tokenRepository.createCalls != 0 || len(historyRepository.history) != 0 {
		t.Fatalf(
			"non-atomic fallback wrote refresh/history = %d/%d",
			tokenRepository.createCalls,
			len(historyRepository.history),
		)
	}
}

type atomicLoginStateRepository struct {
	TokenRepository

	mu             sync.Mutex
	userID         uint
	deviceHash     string
	trustedActive  bool
	sessionCount   int
	commitEntered  chan struct{}
	continueCommit chan struct{}
}

func (repository *atomicLoginStateRepository) GetByTokenHash(
	_ context.Context,
	tokenHash string,
) (*models.OTPTrustedDevice, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if !repository.trustedActive || tokenHash != repository.deviceHash {
		return nil, gorm.ErrRecordNotFound
	}
	return &models.OTPTrustedDevice{
		ID:              7,
		UserID:          repository.userID,
		DeviceTokenHash: repository.deviceHash,
		ExpiresAt:       time.Now().Add(time.Hour),
	}, nil
}

func (*atomicLoginStateRepository) Create(
	context.Context,
	*models.OTPTrustedDevice,
) error {
	return errors.New("non-atomic trusted-device create must not be called")
}

func (*atomicLoginStateRepository) Update(
	context.Context,
	*models.OTPTrustedDevice,
) error {
	return errors.New("non-atomic trusted-device update must not be called")
}

func (repository *atomicLoginStateRepository) ListActiveDevices(
	context.Context,
	uint,
) ([]*models.OTPTrustedDevice, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if !repository.trustedActive {
		return nil, nil
	}
	return []*models.OTPTrustedDevice{{
		ID:              7,
		UserID:          repository.userID,
		DeviceTokenHash: repository.deviceHash,
		ExpiresAt:       time.Now().Add(time.Hour),
	}}, nil
}

func (repository *atomicLoginStateRepository) CommitLoginSession(
	ctx context.Context,
	command *LoginSessionCommit,
) error {
	if repository.commitEntered != nil {
		close(repository.commitEntered)
		select {
		case <-repository.continueCommit:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if command.TrustedDeviceTokenHash != "" &&
		(!repository.trustedActive ||
			command.TrustedDeviceTokenHash != repository.deviceHash) {
		return ErrTrustedDeviceInvalid
	}
	if command.NewTrustedDevice != nil {
		repository.deviceHash = command.NewTrustedDevice.DeviceTokenHash
		repository.trustedActive = true
	}
	repository.sessionCount++
	return nil
}

func (repository *atomicLoginStateRepository) RevokeAllUserTokens(
	context.Context,
	uint,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.sessionCount = 0
	repository.trustedActive = false
	return nil
}

func (repository *atomicLoginStateRepository) snapshot() (int, bool) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.sessionCount, repository.trustedActive
}

func TestAtomicLoginStateMachineSerializesTrustedLoginAndLogoutAll(t *testing.T) {
	const (
		userID      = uint(42)
		email       = "atomic-state@example.test"
		password    = "CorrectPassword123!"
		deviceToken = "atomic-state-device"
	)
	newService := func(
		repository *atomicLoginStateRepository,
		otpEnabled bool,
	) *AuthService {
		return NewAuthService(
			&trustedLoginUserRepository{user: &User{
				ID:            userID,
				Username:      "atomic-state",
				Email:         email,
				PasswordHash:  password,
				PlatformRole:  PlatformRoleMember,
				Status:        StatusActive,
				EmailVerified: true,
				OTPEnabled:    otpEnabled,
				OTPSecret:     "otp-secret",
			}},
			&trustedLoginProfileRepository{},
			repository,
			&trustedLoginAttemptRepository{},
			nil,
			repository,
			nil,
			otpAuditEmailConfig{},
			trustedLoginOTPService{},
			trustedLoginPasswordService{},
			mustTestJWTManager(t, time.Hour, 24*time.Hour),
			&AuthConfig{
				AccessTokenExpire:        time.Hour,
				RefreshTokenExpire:       24 * time.Hour,
				MaxFailedLogins:          5,
				RequireEmailVerification: false,
			},
		)
	}

	t.Run("logout linearizes before stale trusted commit", func(t *testing.T) {
		repository := &atomicLoginStateRepository{
			userID:         userID,
			deviceHash:     hashTrustedDeviceToken(deviceToken),
			trustedActive:  true,
			commitEntered:  make(chan struct{}),
			continueCommit: make(chan struct{}),
		}
		service := newService(repository, true)
		result := make(chan error, 1)
		go func() {
			_, err := service.Login(
				context.Background(),
				&LoginRequest{
					Email:       email,
					Password:    password,
					DeviceToken: deviceToken,
				},
				"127.0.0.1",
				"Atomic state test",
			)
			result <- err
		}()
		<-repository.commitEntered
		if err := service.LogoutAll(context.Background(), userID); err != nil {
			t.Fatal(err)
		}
		close(repository.continueCommit)
		if err := <-result; err == nil || err.Error() != "OTP code required" {
			t.Fatalf("stale trusted login error = %v", err)
		}
		if sessions, trusted := repository.snapshot(); sessions != 0 || trusted {
			t.Fatalf("state after logout-first = sessions:%d trusted:%v", sessions, trusted)
		}
	})

	t.Run("login linearizes before logout", func(t *testing.T) {
		repository := &atomicLoginStateRepository{
			userID:        userID,
			deviceHash:    hashTrustedDeviceToken(deviceToken),
			trustedActive: true,
		}
		service := newService(repository, true)
		if _, err := service.Login(
			context.Background(),
			&LoginRequest{
				Email:       email,
				Password:    password,
				DeviceToken: deviceToken,
			},
			"127.0.0.1",
			"Atomic state test",
		); err != nil {
			t.Fatal(err)
		}
		if sessions, trusted := repository.snapshot(); sessions != 1 || !trusted {
			t.Fatalf("state after login = sessions:%d trusted:%v", sessions, trusted)
		}
		if err := service.LogoutAll(context.Background(), userID); err != nil {
			t.Fatal(err)
		}
		if sessions, trusted := repository.snapshot(); sessions != 0 || trusted {
			t.Fatalf("state after login-then-logout = sessions:%d trusted:%v", sessions, trusted)
		}
	})

	t.Run("new remembered device can only appear after earlier logout", func(t *testing.T) {
		repository := &atomicLoginStateRepository{
			userID:         userID,
			commitEntered:  make(chan struct{}),
			continueCommit: make(chan struct{}),
		}
		service := newService(repository, false)
		result := make(chan error, 1)
		go func() {
			_, err := service.Login(
				context.Background(),
				&LoginRequest{
					Email:          email,
					Password:       password,
					RememberDevice: true,
				},
				"127.0.0.1",
				"Atomic state test",
			)
			result <- err
		}()
		<-repository.commitEntered
		if err := service.LogoutAll(context.Background(), userID); err != nil {
			t.Fatal(err)
		}
		close(repository.continueCommit)
		if err := <-result; err != nil {
			t.Fatal(err)
		}
		if sessions, trusted := repository.snapshot(); sessions != 1 || !trusted {
			t.Fatalf(
				"logout-before-new-login state = sessions:%d trusted:%v",
				sessions,
				trusted,
			)
		}
	})
}
