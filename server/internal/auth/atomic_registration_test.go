package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/gorm"
)

type staticRegistrationEmailPolicy bool

func (policy staticRegistrationEmailPolicy) IsEmailVerificationEnabled(
	context.Context,
) (bool, error) {
	return bool(policy), nil
}

func registrationSessionBuilderForTest(
	user *User,
	ipAddress string,
	userAgent string,
) RegistrationSessionBuilder {
	return func(
		userID uint,
		committedAt time.Time,
	) (*RegistrationSession, error) {
		sessionID := "registration-session"
		refreshBearer := "registration-refresh-bearer"
		sessionUser := *user
		sessionUser.ID = userID
		sessionUser.LastLoginAt = timePtr(committedAt)
		return &RegistrationSession{
			AccessToken:  "registration-access-bearer",
			RefreshToken: refreshBearer,
			SessionID:    sessionID,
			RefreshAuthority: &RefreshToken{
				UserID:    userID,
				Token:     refreshBearer,
				SessionID: sessionID,
				ExpiresAt: committedAt.Add(24 * time.Hour),
				IPAddress: ipAddress,
				UserAgent: userAgent,
				CreatedAt: committedAt,
			},
			LoginHistory: newLoginHistorySuccess(
				&sessionUser,
				ipAddress,
				userAgent,
				sessionID,
				committedAt,
				models.LoginMethodPassword,
			),
			SuccessfulAttempt: &LoginAttempt{
				UserID:    &userID,
				Email:     user.Email,
				IPAddress: ipAddress,
				UserAgent: userAgent,
				Success:   true,
				CreatedAt: committedAt,
			},
		}, nil
	}
}

type registrationPrecheckFailureRepository struct {
	UserRepository
	emailErr    error
	usernameErr error
}

type nonAtomicRegistrationOutboxRepository struct {
	AuthEmailOutboxRepository
}

func TestRegistrationFailsClosedWithoutAtomicRepositoryCapability(t *testing.T) {
	service := NewAuthService(
		&registrationPrecheckFailureRepository{
			emailErr:    ErrUserNotFound,
			usernameErr: ErrUserNotFound,
		},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		staticRegistrationEmailPolicy(false),
		trustedLoginOTPService{},
		trustedLoginPasswordService{},
		mustTestJWTManager(t, time.Hour, 24*time.Hour),
		&AuthConfig{
			EnableRegistration:      true,
			AccessTokenExpire:       time.Hour,
			RefreshTokenExpire:      24 * time.Hour,
			EmailVerificationExpire: time.Hour,
		},
		WithAuthEmailOutboxRepository(
			&nonAtomicRegistrationOutboxRepository{},
		),
	)

	response, err := service.Register(
		context.Background(),
		&RegisterRequest{
			Username:        "missing-atomic-registration",
			Email:           "missing-atomic-registration@example.test",
			Password:        "CorrectPassword123!",
			ConfirmPassword: "CorrectPassword123!",
		},
		"127.0.0.1",
		"missing atomic registration test",
	)
	if response != nil ||
		!errors.Is(err, ErrAtomicRegistrationUnavailable) {
		t.Fatalf(
			"Register() response/error = %+v/%v, want nil/%v",
			response,
			err,
			ErrAtomicRegistrationUnavailable,
		)
	}
}

func TestRegistrationFailureHTTPResponseIsStable(t *testing.T) {
	for _, test := range []struct {
		name        string
		err         error
		wantStatus  int
		wantMessage string
	}{
		{
			name:        "duplicate identity",
			err:         ErrUserExists,
			wantStatus:  http.StatusConflict,
			wantMessage: "该用户已存在",
		},
		{
			name:        "policy unavailable",
			err:         ErrEmailVerificationPolicyUnavailable,
			wantStatus:  http.StatusServiceUnavailable,
			wantMessage: "注册服务暂时不可用",
		},
		{
			name:        "policy changed",
			err:         ErrEmailVerificationPolicyChanged,
			wantStatus:  http.StatusServiceUnavailable,
			wantMessage: "注册服务暂时不可用",
		},
		{
			name:        "atomic capability unavailable",
			err:         ErrAtomicRegistrationUnavailable,
			wantStatus:  http.StatusServiceUnavailable,
			wantMessage: "注册服务暂时不可用",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, message := registrationFailureHTTPResponse(test.err)
			if status != test.wantStatus || message != test.wantMessage {
				t.Fatalf(
					"registration failure response = (%d, %q), want (%d, %q)",
					status,
					message,
					test.wantStatus,
					test.wantMessage,
				)
			}
		})
	}
}

func (repository *registrationPrecheckFailureRepository) GetByEmail(
	context.Context,
	string,
) (*User, error) {
	return nil, repository.emailErr
}

func (repository *registrationPrecheckFailureRepository) GetByUsername(
	context.Context,
	string,
) (*User, error) {
	return nil, repository.usernameErr
}

func TestRegistrationPrechecksFailClosedOnRepositoryErrors(t *testing.T) {
	tests := []struct {
		name        string
		emailErr    error
		usernameErr error
		want        error
	}{
		{
			name:        "email lookup failure",
			emailErr:    errors.New("injected email lookup failure"),
			usernameErr: ErrUserNotFound,
		},
		{
			name:        "username lookup failure",
			emailErr:    ErrUserNotFound,
			usernameErr: errors.New("injected username lookup failure"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.want = test.emailErr
			if errors.Is(test.emailErr, ErrUserNotFound) {
				test.want = test.usernameErr
			}
			service := NewAuthService(
				&registrationPrecheckFailureRepository{
					emailErr:    test.emailErr,
					usernameErr: test.usernameErr,
				},
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				otpAuditEmailConfig{},
				trustedLoginOTPService{},
				trustedLoginPasswordService{},
				nil,
				&AuthConfig{EnableRegistration: true},
			)

			response, err := service.Register(
				context.Background(),
				&RegisterRequest{
					Username:        "precheck-fail-closed",
					Email:           "precheck-fail-closed@example.test",
					Password:        "CorrectPassword123!",
					ConfirmPassword: "CorrectPassword123!",
				},
				"127.0.0.1",
				"registration precheck test",
			)
			if response != nil || !errors.Is(err, test.want) {
				t.Fatalf(
					"Register() response/error = %+v/%v, want nil/%v",
					response,
					err,
					test.want,
				)
			}
		})
	}
}

func TestRegistrationRefreshFailureRollsBackEntireRegistration(t *testing.T) {
	db, outboxRepository, protector := newAuthEmailOutboxTestRepository(t)
	seedAuthEmailVerificationPolicy(t, db, false)

	const callbackName = "test:registration-refresh-failure"
	if err := db.Callback().Create().
		Before("gorm:create").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement != nil &&
				tx.Statement.Schema != nil &&
				tx.Statement.Schema.Table == "refresh_tokens" {
				tx.AddError(errors.New("injected registration refresh failure"))
			}
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Create().Remove(callbackName)
	})

	service := NewAuthService(
		NewGormUserRepository(db, protector),
		NewGormProfileRepository(db),
		NewGormTokenRepository(db),
		NewGormLoginAttemptRepository(db),
		NewGormLoginHistoryRepository(db),
		nil,
		nil,
		otpAuditEmailConfig{},
		trustedLoginOTPService{},
		trustedLoginPasswordService{},
		mustTestJWTManager(t, time.Hour, 24*time.Hour),
		&AuthConfig{
			EnableRegistration:      true,
			AccessTokenExpire:       time.Hour,
			RefreshTokenExpire:      24 * time.Hour,
			EmailVerificationExpire: time.Hour,
		},
		WithAuthEmailOutboxRepository(outboxRepository),
	)

	response, err := service.Register(
		context.Background(),
		&RegisterRequest{
			Username:        "atomic-registration-refresh-failure",
			Email:           "atomic-registration-refresh-failure@example.test",
			Password:        "CorrectPassword123!",
			ConfirmPassword: "CorrectPassword123!",
			FirstName:       "原子",
			LastName:        "注册",
		},
		"127.0.0.1",
		"registration rollback test",
	)
	if err == nil || response != nil {
		t.Fatalf("Register() response/error = %+v/%v, want nil/error", response, err)
	}

	for table, model := range map[string]any{
		"users":             &models.User{},
		"user_profiles":     &models.UserProfile{},
		"refresh_tokens":    &RefreshToken{},
		"login_histories":   &models.LoginHistory{},
		"login_attempts":    &LoginAttempt{},
		"domain_events":     &models.DomainEvent{},
		"outbox_deliveries": &models.OutboxDelivery{},
	} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("%s count after rollback = %d, want 0", table, count)
		}
	}
}

func TestAtomicRegistrationSuccessMatrix(t *testing.T) {
	tests := []struct {
		name                 string
		verificationEnabled  bool
		wantVerificationRows int64
		wantSessionRows      int64
		wantEventType        string
		wantDestination      string
	}{
		{
			name:                 "verification enabled",
			verificationEnabled:  true,
			wantVerificationRows: 1,
			wantEventType:        eventcontract.EmailVerificationRequestedEventType,
			wantDestination:      services.AuthVerificationEmailDestinationPrefix,
		},
		{
			name:            "verification disabled",
			wantSessionRows: 1,
			wantEventType:   eventcontract.UserRegisteredEventType,
			wantDestination: services.AuthWelcomeEmailDestinationPrefix,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, repository, protector := newAuthEmailOutboxTestRepository(t)
			seedAuthEmailVerificationPolicy(
				t,
				db,
				test.verificationEnabled,
			)
			manager := mustTestJWTManager(t, time.Hour, 24*time.Hour)
			service := NewAuthService(
				NewGormUserRepository(db, protector),
				NewGormProfileRepository(db),
				NewGormTokenRepository(db),
				NewGormLoginAttemptRepository(db),
				NewGormLoginHistoryRepository(db),
				nil,
				nil,
				staticRegistrationEmailPolicy(test.verificationEnabled),
				trustedLoginOTPService{},
				trustedLoginPasswordService{},
				manager,
				&AuthConfig{
					EnableRegistration:       true,
					AccessTokenExpire:        time.Hour,
					RefreshTokenExpire:       24 * time.Hour,
					EmailVerificationExpire:  time.Hour,
					PasswordMinLength:        8,
					MaxFailedLogins:          5,
					RequireEmailVerification: test.verificationEnabled,
				},
				WithAuthEmailOutboxRepository(repository),
			)

			response, err := service.Register(
				context.Background(),
				&RegisterRequest{
					Username:        "atomic-registration-success",
					Email:           "atomic-registration-success@example.test",
					Password:        "CorrectPassword123!",
					ConfirmPassword: "CorrectPassword123!",
					FirstName:       "原子",
					LastName:        "注册",
				},
				"127.0.0.1",
				"Atomic Registration Test",
			)
			if err != nil {
				t.Fatal(err)
			}
			if response == nil || response.User == nil || response.User.ID == 0 {
				t.Fatalf("registration response is incomplete: %+v", response)
			}

			var verificationCount, refreshCount, historyCount, attemptCount int64
			for model, count := range map[any]*int64{
				&EmailVerification{}:   &verificationCount,
				&RefreshToken{}:        &refreshCount,
				&models.LoginHistory{}: &historyCount,
				&LoginAttempt{}:        &attemptCount,
			} {
				if err := db.Model(model).Count(count).Error; err != nil {
					t.Fatal(err)
				}
			}
			if verificationCount != test.wantVerificationRows ||
				refreshCount != test.wantSessionRows ||
				historyCount != test.wantSessionRows ||
				attemptCount != test.wantSessionRows {
				t.Fatalf(
					"verification/refresh/history/attempt rows = %d/%d/%d/%d",
					verificationCount,
					refreshCount,
					historyCount,
					attemptCount,
				)
			}

			var storedUser models.User
			if err := db.First(&storedUser, response.User.ID).Error; err != nil {
				t.Fatal(err)
			}
			var event models.DomainEvent
			if err := db.First(&event).Error; err != nil {
				t.Fatal(err)
			}
			var delivery models.OutboxDelivery
			if err := db.First(&delivery).Error; err != nil {
				t.Fatal(err)
			}
			if event.Type != test.wantEventType ||
				!strings.HasPrefix(delivery.DestinationID, test.wantDestination) {
				t.Fatalf("event/outbox = %q/%q", event.Type, delivery.DestinationID)
			}

			if test.verificationEnabled {
				if response.AccessToken != "" ||
					response.RefreshToken != "" ||
					response.ExpiresIn != 0 ||
					response.TokenType != "" ||
					storedUser.LastLoginAt != nil {
					t.Fatalf(
						"verification-enabled registration exposed a session: %+v",
						response,
					)
				}
				return
			}

			if response.AccessToken == "" ||
				response.RefreshToken == "" ||
				response.TokenType != "Bearer" ||
				response.ExpiresIn != int64(time.Hour.Seconds()) {
				t.Fatalf("registration session response = %+v", response)
			}
			accessClaims, err := manager.VerifyAccessToken(response.AccessToken)
			if err != nil {
				t.Fatal(err)
			}
			refreshClaims, err := manager.VerifyRefreshToken(response.RefreshToken)
			if err != nil {
				t.Fatal(err)
			}
			var storedRefresh RefreshToken
			if err := db.First(&storedRefresh).Error; err != nil {
				t.Fatal(err)
			}
			var history models.LoginHistory
			if err := db.First(&history).Error; err != nil {
				t.Fatal(err)
			}
			var attempt LoginAttempt
			if err := db.First(&attempt).Error; err != nil {
				t.Fatal(err)
			}
			if storedRefresh.Token == response.RefreshToken ||
				storedRefresh.Token != bearerTokenDigest(
					"refresh-token",
					response.RefreshToken,
				) {
				t.Fatal("registration stored a plaintext or wrong refresh authority")
			}
			if accessClaims.SessionID == "" ||
				accessClaims.SessionID != refreshClaims.SessionID ||
				accessClaims.SessionID != storedRefresh.SessionID ||
				accessClaims.SessionID != history.SessionID {
				t.Fatalf(
					"session IDs are not aligned: access=%q refresh=%q stored=%q history=%q",
					accessClaims.SessionID,
					refreshClaims.SessionID,
					storedRefresh.SessionID,
					history.SessionID,
				)
			}
			if !storedUser.EmailVerified ||
				storedUser.EmailVerifiedAt == nil ||
				storedUser.LastLoginAt == nil ||
				!storedUser.EmailVerifiedAt.Equal(*storedUser.LastLoginAt) ||
				!history.LoginTime.Equal(*storedUser.LastLoginAt) ||
				!storedRefresh.CreatedAt.Equal(*storedUser.LastLoginAt) ||
				!attempt.CreatedAt.Equal(*storedUser.LastLoginAt) {
				t.Fatalf(
					"registration timestamps diverged: user=%+v refresh=%v history=%v attempt=%v",
					storedUser,
					storedRefresh.CreatedAt,
					history.LoginTime,
					attempt.CreatedAt,
				)
			}
			if history.LoginStatus != models.LoginStatusSuccess ||
				history.LoginMethod != models.LoginMethodPassword ||
				!history.IsActive ||
				attempt.UserID == nil ||
				*attempt.UserID != storedUser.ID ||
				!attempt.Success ||
				attempt.FailReason != "" {
				t.Fatalf(
					"registration history/attempt is invalid: history=%+v attempt=%+v",
					history,
					attempt,
				)
			}
			persistedEvent := string(event.Data) + fmt.Sprint(delivery)
			if strings.Contains(persistedEvent, response.AccessToken) ||
				strings.Contains(persistedEvent, response.RefreshToken) {
				t.Fatal("registration event/outbox leaked bearer credentials")
			}
		})
	}
}

func TestAtomicRegistrationInjectedFailureRollsBackEveryTable(t *testing.T) {
	tests := []struct {
		name                string
		verificationEnabled bool
		failTable           string
		builderFailure      bool
	}{
		{name: "user", verificationEnabled: false, failTable: "users"},
		{name: "profile", verificationEnabled: false, failTable: "user_profiles"},
		{
			name:                "verification",
			verificationEnabled: true,
			failTable:           "email_verifications",
		},
		{name: "event", verificationEnabled: false, failTable: "domain_events"},
		{name: "outbox", verificationEnabled: false, failTable: "outbox_deliveries"},
		{name: "session builder", verificationEnabled: false, builderFailure: true},
		{name: "refresh", verificationEnabled: false, failTable: "refresh_tokens"},
		{name: "history", verificationEnabled: false, failTable: "login_histories"},
		{name: "attempt", verificationEnabled: false, failTable: "login_attempts"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, repository, _ := newAuthEmailOutboxTestRepository(t)
			seedAuthEmailVerificationPolicy(
				t,
				db,
				test.verificationEnabled,
			)
			if test.failTable != "" {
				callbackName := "test:atomic-registration-fail-" + test.name
				if err := db.Callback().Create().
					Before("gorm:create").
					Register(callbackName, func(tx *gorm.DB) {
						if tx.Statement != nil &&
							tx.Statement.Schema != nil &&
							tx.Statement.Schema.Table == test.failTable {
							tx.AddError(errors.New("injected registration failure"))
						}
					}); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					_ = db.Callback().Create().Remove(callbackName)
				})
			}

			committedAt := time.Now().UTC()
			user := &User{
				Username:          "registration-failure-" + strings.ReplaceAll(test.name, " ", "-"),
				Email:             "registration-failure-" + strings.ReplaceAll(test.name, " ", "-") + "@example.test",
				PasswordHash:      "registration-password-hash",
				PlatformRole:      PlatformRoleMember,
				Status:            StatusActive,
				EmailVerified:     !test.verificationEnabled,
				PasswordChangedAt: &committedAt,
			}
			profile := &UserProfile{
				Timezone: "UTC",
				Language: DefaultProfileLanguage,
			}
			var verification *EmailVerification
			var builder RegistrationSessionBuilder
			if test.verificationEnabled {
				verification = &EmailVerification{
					Email:     user.Email,
					Token:     "registration-verification-bearer",
					ExpiresAt: committedAt.Add(time.Hour),
				}
			} else {
				user.EmailVerifiedAt = &committedAt
				user.LastLoginAt = &committedAt
				builder = registrationSessionBuilderForTest(
					user,
					"127.0.0.1",
					"registration failure test",
				)
				if test.builderFailure {
					builder = func(uint, time.Time) (*RegistrationSession, error) {
						return nil, errors.New("injected registration builder failure")
					}
				}
			}

			result, err := repository.CommitRegistration(
				context.Background(),
				&RegistrationCommit{
					CommittedAt:  committedAt,
					User:         user,
					Profile:      profile,
					Verification: verification,
					ExpectedEmailPolicy: &EmailVerificationPolicySnapshot{
						Enabled: test.verificationEnabled,
					},
					BuildSession: builder,
				},
			)
			if err == nil || result != nil {
				t.Fatalf(
					"CommitRegistration() result/error = %+v/%v, want nil/error",
					result,
					err,
				)
			}
			if errors.Is(err, ErrUserExists) && test.failTable != "users" {
				t.Fatalf("non-user unique failure was misreported as ErrUserExists: %v", err)
			}
			assertRegistrationTablesEmpty(t, db)
			if user.ID != 0 || profile.ID != 0 ||
				(verification != nil && verification.ID != 0) {
				t.Fatalf(
					"failed registration leaked IDs: user=%d profile=%d verification=%v",
					user.ID,
					profile.ID,
					verification,
				)
			}
		})
	}
}

func TestAtomicRegistrationPolicyLoadFailureRollsBackBeforeUserInsert(
	t *testing.T,
) {
	db, repository, _ := newAuthEmailOutboxTestRepository(t)
	seedAuthEmailVerificationPolicy(t, db, false)
	const callbackName = "test:atomic-registration-policy-load-failure"
	if err := db.Callback().Query().
		Before("gorm:query").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement != nil &&
				tx.Statement.Schema != nil &&
				tx.Statement.Schema.Table == "email_configs" {
				tx.AddError(errors.New("injected policy load failure"))
			}
		}); err != nil {
		t.Fatal(err)
	}

	command := atomicRegistrationTestCommand(
		"registration-policy-load-failure",
		"registration-policy-load-failure@example.test",
		false,
	)
	result, err := repository.CommitRegistration(
		context.Background(),
		command,
	)
	if removeErr := db.Callback().Query().Remove(callbackName); removeErr != nil {
		t.Fatal(removeErr)
	}
	if result != nil ||
		!errors.Is(err, ErrEmailVerificationPolicyUnavailable) {
		t.Fatalf(
			"policy load failure result/error = %+v/%v",
			result,
			err,
		)
	}
	assertRegistrationTablesEmpty(t, db)
}

func atomicRegistrationTestCommand(
	username string,
	email string,
	verificationEnabled bool,
) *RegistrationCommit {
	committedAt := time.Now().UTC()
	user := &User{
		Username:          username,
		Email:             email,
		PasswordHash:      "registration-password-hash",
		PlatformRole:      PlatformRoleMember,
		Status:            StatusActive,
		EmailVerified:     !verificationEnabled,
		PasswordChangedAt: &committedAt,
	}
	command := &RegistrationCommit{
		CommittedAt: committedAt,
		User:        user,
		Profile: &UserProfile{
			Timezone: "UTC",
			Language: DefaultProfileLanguage,
		},
		ExpectedEmailPolicy: &EmailVerificationPolicySnapshot{
			Enabled: verificationEnabled,
		},
	}
	if verificationEnabled {
		command.Verification = &EmailVerification{
			Email:     email,
			Token:     "registration-verification-token",
			ExpiresAt: committedAt.Add(time.Hour),
		}
		return command
	}
	user.EmailVerifiedAt = &committedAt
	user.LastLoginAt = &committedAt
	command.BuildSession = registrationSessionBuilderForTest(
		user,
		"127.0.0.1",
		"atomic registration test",
	)
	return command
}

func assertRegistrationTablesEmpty(t *testing.T, db *gorm.DB) {
	t.Helper()
	for table, model := range map[string]any{
		"users":               &models.User{},
		"user_profiles":       &models.UserProfile{},
		"email_verifications": &EmailVerification{},
		"refresh_tokens":      &RefreshToken{},
		"login_histories":     &models.LoginHistory{},
		"login_attempts":      &LoginAttempt{},
		"domain_events":       &models.DomainEvent{},
		"outbox_deliveries":   &models.OutboxDelivery{},
	} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("%s count after rollback = %d, want 0", table, count)
		}
	}
}

func TestAtomicRegistrationDuplicateIdentityReturnsStableConflict(t *testing.T) {
	tests := []struct {
		name        string
		secondUser  string
		secondEmail string
	}{
		{
			name:        "duplicate email",
			secondUser:  "atomic-duplicate-email-second",
			secondEmail: "atomic-duplicate@example.test",
		},
		{
			name:        "duplicate username",
			secondUser:  "atomic-duplicate-username",
			secondEmail: "atomic-duplicate-username-second@example.test",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, repository, protector := newAuthEmailOutboxTestRepository(t)
			seedAuthEmailVerificationPolicy(t, db, false)
			service := NewAuthService(
				NewGormUserRepository(db, protector),
				NewGormProfileRepository(db),
				NewGormTokenRepository(db),
				NewGormLoginAttemptRepository(db),
				NewGormLoginHistoryRepository(db),
				nil,
				nil,
				staticRegistrationEmailPolicy(false),
				trustedLoginOTPService{},
				trustedLoginPasswordService{},
				mustTestJWTManager(t, time.Hour, 24*time.Hour),
				&AuthConfig{
					EnableRegistration:      true,
					AccessTokenExpire:       time.Hour,
					RefreshTokenExpire:      24 * time.Hour,
					EmailVerificationExpire: time.Hour,
				},
				WithAuthEmailOutboxRepository(repository),
			)

			firstUsername := "atomic-duplicate-username"
			firstEmail := "atomic-duplicate@example.test"
			first, err := service.Register(
				context.Background(),
				&RegisterRequest{
					Username:        firstUsername,
					Email:           firstEmail,
					Password:        "CorrectPassword123!",
					ConfirmPassword: "CorrectPassword123!",
				},
				"127.0.0.1",
				"duplicate registration winner",
			)
			if err != nil || first == nil || first.AccessToken == "" {
				t.Fatalf("first registration = %+v/%v", first, err)
			}
			second, err := service.Register(
				context.Background(),
				&RegisterRequest{
					Username:        test.secondUser,
					Email:           test.secondEmail,
					Password:        "CorrectPassword123!",
					ConfirmPassword: "CorrectPassword123!",
				},
				"127.0.0.2",
				"duplicate registration loser",
			)
			if second != nil || !errors.Is(err, ErrUserExists) {
				t.Fatalf("duplicate registration = %+v/%v", second, err)
			}

			for table, model := range map[string]any{
				"users":             &models.User{},
				"user_profiles":     &models.UserProfile{},
				"refresh_tokens":    &RefreshToken{},
				"login_histories":   &models.LoginHistory{},
				"login_attempts":    &LoginAttempt{},
				"domain_events":     &models.DomainEvent{},
				"outbox_deliveries": &models.OutboxDelivery{},
			} {
				var count int64
				if err := db.Model(model).Count(&count).Error; err != nil {
					t.Fatal(err)
				}
				if count != 1 {
					t.Errorf("%s count after duplicate = %d, want 1", table, count)
				}
			}
		})
	}
}

func TestAtomicRegistrationDoesNotMisreportRefreshUniqueConflict(t *testing.T) {
	db, repository, _ := newAuthEmailOutboxTestRepository(t)
	seedAuthEmailVerificationPolicy(t, db, false)
	const refreshBearer = "registration-refresh-bearer"
	if err := db.Create(&RefreshToken{
		UserID:    999,
		Token:     bearerTokenDigest("refresh-token", refreshBearer),
		SessionID: "preexisting-session",
		ExpiresAt: time.Now().Add(time.Hour),
		CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	committedAt := time.Now().UTC()
	user := &User{
		Username:          "refresh-conflict-registration",
		Email:             "refresh-conflict-registration@example.test",
		PasswordHash:      "registration-password-hash",
		PlatformRole:      PlatformRoleMember,
		Status:            StatusActive,
		EmailVerified:     true,
		EmailVerifiedAt:   &committedAt,
		LastLoginAt:       &committedAt,
		PasswordChangedAt: &committedAt,
	}
	result, err := repository.CommitRegistration(
		context.Background(),
		&RegistrationCommit{
			CommittedAt: committedAt,
			User:        user,
			Profile: &UserProfile{
				Timezone: "UTC",
				Language: DefaultProfileLanguage,
			},
			ExpectedEmailPolicy: &EmailVerificationPolicySnapshot{
				Enabled: false,
			},
			BuildSession: registrationSessionBuilderForTest(
				user,
				"127.0.0.1",
				"refresh unique conflict",
			),
		},
	)
	if err == nil || result != nil {
		t.Fatalf("refresh conflict result/error = %+v/%v", result, err)
	}
	if errors.Is(err, ErrUserExists) {
		t.Fatalf("refresh unique conflict was misreported as ErrUserExists: %v", err)
	}
	var userCount, profileCount, historyCount, attemptCount, eventCount, outboxCount int64
	for model, count := range map[any]*int64{
		&models.User{}:           &userCount,
		&models.UserProfile{}:    &profileCount,
		&models.LoginHistory{}:   &historyCount,
		&LoginAttempt{}:          &attemptCount,
		&models.DomainEvent{}:    &eventCount,
		&models.OutboxDelivery{}: &outboxCount,
	} {
		if err := db.Model(model).Count(count).Error; err != nil {
			t.Fatal(err)
		}
	}
	if userCount != 0 ||
		profileCount != 0 ||
		historyCount != 0 ||
		attemptCount != 0 ||
		eventCount != 0 ||
		outboxCount != 0 {
		t.Fatalf(
			"refresh conflict left fragments: user/profile/history/attempt/event/outbox=%d/%d/%d/%d/%d/%d",
			userCount,
			profileCount,
			historyCount,
			attemptCount,
			eventCount,
			outboxCount,
		)
	}
}
