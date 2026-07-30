package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newAuthAtomicityDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf(
			"file:auth-atomicity-%d?mode=memory&cache=shared&_foreign_keys=on",
			time.Now().UnixNano(),
		)),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("open authentication database: %v", err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.UserProfile{},
		&models.LoginHistory{},
		&RefreshToken{},
	); err != nil {
		t.Fatalf("migrate authentication database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get SQL database: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	return db
}

func seedRefreshService(
	t *testing.T,
) (*gorm.DB, *AuthService, *GormTokenRepository, string) {
	t.Helper()
	db := newAuthAtomicityDB(t)
	passwordChangedAt := time.Now().Add(-time.Hour)
	user := models.User{
		Username:        "refresh-atomic-user",
		Email:           "refresh-atomic@example.test",
		PasswordHash:    "unused-refresh-password",
		PlatformRole:    models.PlatformRoleMember,
		Status:          models.UserStatusActive,
		EmailVerified:   true,
		PasswordResetAt: &passwordChangedAt,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed refresh user: %v", err)
	}
	if err := db.Create(&models.UserProfile{UserID: user.ID}).Error; err != nil {
		t.Fatalf("seed refresh profile: %v", err)
	}
	manager := mustTestJWTManager(t, time.Hour, 24*time.Hour)
	_, oldRefresh, err := manager.GenerateTokenPair(
		user.ID,
		PlatformRoleMember,
		"atomic-refresh-session",
	)
	if err != nil {
		t.Fatalf("generate initial token pair: %v", err)
	}
	tokenRepository := &GormTokenRepository{db: db}
	if err := tokenRepository.CreateRefreshToken(context.Background(), &RefreshToken{
		UserID:    user.ID,
		Token:     oldRefresh,
		SessionID: "atomic-refresh-session",
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("persist initial refresh token: %v", err)
	}
	loginTime := time.Now()
	if err := db.Create(&models.LoginHistory{
		UserID:         user.ID,
		Username:       user.Username,
		Email:          user.Email,
		LoginTime:      loginTime,
		LastActivityAt: &loginTime,
		SessionID:      "atomic-refresh-session",
		LoginStatus:    models.LoginStatusSuccess,
		LoginMethod:    models.LoginMethodPassword,
		IsActive:       true,
	}).Error; err != nil {
		t.Fatalf("persist initial login history: %v", err)
	}
	service := &AuthService{
		userRepo:           NewGormUserRepository(db),
		profileRepo:        NewGormProfileRepository(db),
		tokenRepo:          tokenRepository,
		loginHistoryRepo:   NewGormLoginHistoryRepository(db),
		emailConfigService: sessionTestEmailConfig{},
		jwtManager:         manager,
		config: &AuthConfig{
			AccessTokenExpire:  time.Hour,
			RefreshTokenExpire: 24 * time.Hour,
		},
	}
	return db, service, tokenRepository, oldRefresh
}

func TestRefreshReplayReturnsExactCommittedPairWithoutPlaintextStorage(t *testing.T) {
	db, service, _, oldRefresh := seedRefreshService(t)
	request := &RefreshTokenRequest{RefreshToken: oldRefresh}

	first, err := service.RefreshToken(
		context.Background(),
		request,
		"127.0.0.1",
		"refresh-replay-test",
	)
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	replayed, err := service.RefreshToken(
		context.Background(),
		request,
		"127.0.0.1",
		"refresh-replay-test",
	)
	if err != nil {
		t.Fatalf("replay interrupted refresh: %v", err)
	}
	if first.AccessToken != replayed.AccessToken ||
		first.RefreshToken != replayed.RefreshToken {
		t.Fatal("refresh replay did not return the exact committed token pair")
	}

	var records []RefreshToken
	if err := db.Order("id ASC").Find(&records).Error; err != nil {
		t.Fatalf("load refresh token records: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("refresh token rows = %d, want 2", len(records))
	}
	if !records[0].Revoked ||
		records[0].RotatedAt == nil ||
		records[0].ReplacedByToken == "" {
		t.Fatalf("old token lacks recoverable rotation metadata: %+v", records[0])
	}
	for _, record := range records {
		if record.Token == oldRefresh ||
			record.Token == first.RefreshToken ||
			record.ReplacedByToken == first.RefreshToken {
			t.Fatal("database stored a usable refresh bearer token in plaintext")
		}
	}
}

type cancelAfterUserLookupRepository struct {
	UserRepository
	cancel context.CancelFunc
}

func (r *cancelAfterUserLookupRepository) GetByID(
	ctx context.Context,
	userID uint,
) (*User, error) {
	user, err := r.UserRepository.GetByID(ctx, userID)
	r.cancel()
	return user, err
}

func TestCanceledRefreshDoesNotRevokeCurrentToken(t *testing.T) {
	_, service, tokenRepository, oldRefresh := seedRefreshService(t)
	ctx, cancel := context.WithCancel(context.Background())
	service.userRepo = &cancelAfterUserLookupRepository{
		UserRepository: service.userRepo,
		cancel:         cancel,
	}

	if _, err := service.RefreshToken(
		ctx,
		&RefreshTokenRequest{RefreshToken: oldRefresh},
		"127.0.0.1",
		"refresh-cancel-test",
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled refresh error = %v, want context.Canceled", err)
	}
	if _, err := tokenRepository.GetRefreshToken(context.Background(), oldRefresh); err != nil {
		t.Fatalf("canceled refresh revoked the current token: %v", err)
	}
}

func TestConcurrentRefreshRequestsRecoverTheSameWinner(t *testing.T) {
	_, service, _, oldRefresh := seedRefreshService(t)
	const callers = 4
	results := make(chan *AuthResponse, callers)
	errs := make(chan error, callers)
	var wait sync.WaitGroup
	for i := 0; i < callers; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response, err := service.RefreshToken(
				context.Background(),
				&RefreshTokenRequest{RefreshToken: oldRefresh},
				"127.0.0.1",
				"refresh-concurrency-test",
			)
			results <- response
			errs <- err
		}()
	}
	wait.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent refresh failed: %v", err)
		}
	}
	var expectedAccess, expectedRefresh string
	for result := range results {
		if result == nil {
			t.Fatal("concurrent refresh returned nil response")
		}
		if expectedAccess == "" {
			expectedAccess = result.AccessToken
			expectedRefresh = result.RefreshToken
			continue
		}
		if result.AccessToken != expectedAccess ||
			result.RefreshToken != expectedRefresh {
			t.Fatal("concurrent refresh callers received different winning pairs")
		}
	}
}

func TestPasswordChangeRollsBackWhenSessionRevocationAuditFails(t *testing.T) {
	db := newAuthAtomicityDB(t)
	user := models.User{
		Username:     "password-rollback-user",
		Email:        "password-rollback@example.test",
		PasswordHash: "old-password-hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed password user: %v", err)
	}
	if err := db.Create(&RefreshToken{
		UserID:    user.ID,
		Token:     bearerTokenDigest("refresh-token", "password-session-token"),
		SessionID: "password-session",
		ExpiresAt: time.Now().Add(time.Hour),
	}).Error; err != nil {
		t.Fatalf("seed refresh token: %v", err)
	}
	loginTime := time.Now()
	if err := db.Create(&models.LoginHistory{
		UserID:         user.ID,
		Username:       user.Username,
		Email:          user.Email,
		LoginTime:      loginTime,
		LastActivityAt: &loginTime,
		SessionID:      "password-session",
		LoginStatus:    models.LoginStatusSuccess,
		IsActive:       true,
	}).Error; err != nil {
		t.Fatalf("seed login history: %v", err)
	}
	if err := db.Exec(`
		CREATE TRIGGER reject_password_session_audit
		BEFORE UPDATE ON login_histories
		BEGIN
			SELECT RAISE(FAIL, 'injected login history failure');
		END
	`).Error; err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	repository := &GormUserRepository{db: db}
	if err := repository.ChangePasswordAndRevokeSessions(
		context.Background(),
		user.ID,
		"new-password-hash",
		time.Now(),
	); err == nil {
		t.Fatal("password transaction unexpectedly succeeded")
	}
	var persisted models.User
	if err := db.First(&persisted, user.ID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if persisted.PasswordHash != "old-password-hash" {
		t.Fatal("failed session revocation left the new password committed")
	}
	var token RefreshToken
	if err := db.Where("user_id = ?", user.ID).First(&token).Error; err != nil {
		t.Fatalf("reload refresh token: %v", err)
	}
	if token.Revoked {
		t.Fatal("failed password transaction partially revoked refresh tokens")
	}
}

type loginFailureUserRepository struct {
	UserRepository
	user                 *User
	incrementErr         error
	incrementCalls       int
	resetCalls           int
	updateLastLoginCalls int
}

func (r *loginFailureUserRepository) GetByEmail(
	context.Context,
	string,
) (*User, error) {
	copy := *r.user
	return &copy, nil
}

func (r *loginFailureUserRepository) IncrementFailedLogin(
	context.Context,
	uint,
) error {
	r.incrementCalls++
	return r.incrementErr
}

func (r *loginFailureUserRepository) ResetFailedLogin(context.Context, uint) error {
	r.resetCalls++
	return nil
}

func (r *loginFailureUserRepository) UpdateLastLogin(
	context.Context,
	uint,
	time.Time,
) error {
	r.updateLastLoginCalls++
	return nil
}

type loginFailureAttemptRepository struct {
	LoginAttemptRepository
	createErr   error
	createCalls int
}

func (r *loginFailureAttemptRepository) GetRecentFailedAttempts(
	context.Context,
	string,
	time.Time,
) (int, error) {
	return 0, nil
}

func (r *loginFailureAttemptRepository) Create(
	context.Context,
	*LoginAttempt,
) error {
	r.createCalls++
	return r.createErr
}

type loginFailureProfileRepository struct {
	ProfileRepository
}

func (*loginFailureProfileRepository) GetByUserID(
	context.Context,
	uint,
) (*UserProfile, error) {
	return nil, gorm.ErrRecordNotFound
}

func newLoginFailureService(
	t *testing.T,
	incrementErr, auditErr error,
	password string,
) (*AuthService, *loginFailureUserRepository, *loginFailureAttemptRepository) {
	t.Helper()
	passwordService := mustTestPasswordService(t)
	hash, err := passwordService.HashPassword("CorrectPassword123!")
	if err != nil {
		t.Fatalf("hash test password: %v", err)
	}
	userRepository := &loginFailureUserRepository{user: &User{
		ID:            77,
		Username:      "audit-failure-user",
		Email:         "audit-failure@example.test",
		PasswordHash:  hash,
		PlatformRole:  PlatformRoleMember,
		Status:        StatusActive,
		EmailVerified: true,
	}, incrementErr: incrementErr}
	attemptRepository := &loginFailureAttemptRepository{createErr: auditErr}
	return &AuthService{
		userRepo:           userRepository,
		profileRepo:        &loginFailureProfileRepository{},
		loginAttemptRepo:   attemptRepository,
		loginHistoryRepo:   &noopLoginHistoryRepository{},
		emailConfigService: sessionTestEmailConfig{},
		passwordService:    passwordService,
		config: &AuthConfig{
			MaxFailedLogins:          5,
			RequireEmailVerification: false,
		},
	}, userRepository, attemptRepository
}

type noopLoginHistoryRepository struct {
	LoginHistoryRepository
}

func (*noopLoginHistoryRepository) Create(
	context.Context,
	*models.LoginHistory,
) error {
	return nil
}

func TestFailedLoginCounterFailureStopsAuthentication(t *testing.T) {
	injected := errors.New("counter storage unavailable")
	service, users, attempts := newLoginFailureService(
		t,
		injected,
		nil,
		"WrongPassword123!",
	)
	_, err := service.Login(
		context.Background(),
		&LoginRequest{
			Email:    users.user.Email,
			Password: "WrongPassword123!",
		},
		"127.0.0.1",
		"audit-failure-test",
	)
	if !errors.Is(err, injected) {
		t.Fatalf("login error = %v, want injected counter failure", err)
	}
	if attempts.createCalls != 0 {
		t.Fatal("login continued into audit after failed counter persistence")
	}
}

func TestLoginAttemptAuditFailureStopsAuthentication(t *testing.T) {
	injected := errors.New("audit storage unavailable")
	service, users, attempts := newLoginFailureService(
		t,
		nil,
		injected,
		"WrongPassword123!",
	)
	_, err := service.Login(
		context.Background(),
		&LoginRequest{
			Email:    users.user.Email,
			Password: "WrongPassword123!",
		},
		"127.0.0.1",
		"audit-failure-test",
	)
	if !errors.Is(err, injected) {
		t.Fatalf("login error = %v, want injected audit failure", err)
	}
	if users.incrementCalls != 1 || attempts.createCalls != 1 {
		t.Fatalf(
			"counter calls=%d audit calls=%d, want 1/1",
			users.incrementCalls,
			attempts.createCalls,
		)
	}
}

func TestSuccessfulCredentialCheckCannotIssueTokenWhenAuditFails(t *testing.T) {
	injected := errors.New("success audit storage unavailable")
	service, users, attempts := newLoginFailureService(
		t,
		nil,
		injected,
		"CorrectPassword123!",
	)
	_, err := service.Login(
		context.Background(),
		&LoginRequest{
			Email:    users.user.Email,
			Password: "CorrectPassword123!",
		},
		"127.0.0.1",
		"audit-failure-test",
	)
	if !errors.Is(err, injected) {
		t.Fatalf("login error = %v, want injected success-audit failure", err)
	}
	if users.resetCalls != 1 ||
		users.updateLastLoginCalls != 1 ||
		attempts.createCalls != 1 {
		t.Fatalf(
			"reset=%d update=%d audit=%d, want 1/1/1",
			users.resetCalls,
			users.updateLastLoginCalls,
			attempts.createCalls,
		)
	}
}
