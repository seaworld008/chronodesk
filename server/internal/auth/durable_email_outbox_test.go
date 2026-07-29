package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/security"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newAuthEmailOutboxTestRepository(
	t *testing.T,
) (*gorm.DB, *GormAuthEmailOutboxRepository, security.Protector) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(
			"file:"+strings.ReplaceAll(t.Name(), "/", "-")+
				"?mode=memory&cache=shared",
		),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.UserProfile{},
		&EmailVerification{},
		&PasswordReset{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatal(err)
	}
	protector, err := security.NewKeyring(
		"test-email-outbox",
		map[string][]byte{
			"test-email-outbox": bytes.Repeat([]byte{0x4e}, 32),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewGormAuthEmailOutboxRepository(
		db,
		protector,
		"urn:test:auth",
	)
	if err != nil {
		t.Fatal(err)
	}
	return db, repository, protector
}

func seedAuthEmailOutboxUser(t *testing.T, db *gorm.DB) models.User {
	t.Helper()
	user := models.User{
		Username:     "durable-email-user",
		Email:        "durable-email@example.test",
		PasswordHash: "test-password-hash",
		Role:         models.RoleCustomer,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	return user
}

func TestParseEmailDestinationIDRejectsNativeUintNarrowing(t *testing.T) {
	prefix := services.AuthWelcomeEmailDestinationPrefix
	if _, err := parseEmailDestinationID(prefix+"0", prefix); err == nil {
		t.Fatal("parseEmailDestinationID() accepted zero")
	}
	if _, err := parseEmailDestinationID(prefix+"18446744073709551616", prefix); err == nil {
		t.Fatal("parseEmailDestinationID() accepted uint64 overflow")
	}
	if got, err := parseEmailDestinationID(prefix+"42", prefix); err != nil || got != 42 {
		t.Fatalf("parseEmailDestinationID() = (%d, %v), want (42, nil)", got, err)
	}
}

func TestAuthEmailIntentsCommitWithEncryptedOneTimeCredential(t *testing.T) {
	tests := []struct {
		name      string
		plaintext string
		run       func(
			context.Context,
			*GormAuthEmailOutboxRepository,
			models.User,
			string,
		) error
		loadSecret func(*gorm.DB) (string, string, error)
	}{
		{
			name:      "email verification",
			plaintext: "verification-secret-not-for-events",
			run: func(
				ctx context.Context,
				repository *GormAuthEmailOutboxRepository,
				user models.User,
				plaintext string,
			) error {
				return repository.QueueEmailVerification(
					ctx,
					&EmailVerification{
						UserID:    user.ID,
						Email:     user.Email,
						Token:     plaintext,
						ExpiresAt: time.Now().Add(time.Hour),
					},
					"resend",
				)
			},
			loadSecret: func(db *gorm.DB) (string, string, error) {
				var row EmailVerification
				err := db.First(&row).Error
				return row.Token, row.DeliverySecret, err
			},
		},
		{
			name:      "password reset",
			plaintext: "password-reset-secret-not-for-events",
			run: func(
				ctx context.Context,
				repository *GormAuthEmailOutboxRepository,
				user models.User,
				plaintext string,
			) error {
				return repository.QueuePasswordReset(
					ctx,
					&PasswordReset{
						UserID:    user.ID,
						Email:     user.Email,
						Token:     plaintext,
						ExpiresAt: time.Now().Add(time.Hour),
					},
				)
			},
			loadSecret: func(db *gorm.DB) (string, string, error) {
				var row PasswordReset
				err := db.First(&row).Error
				return row.Token, row.DeliverySecret, err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, repository, _ := newAuthEmailOutboxTestRepository(t)
			user := seedAuthEmailOutboxUser(t, db)
			if err := test.run(
				context.Background(),
				repository,
				user,
				test.plaintext,
			); err != nil {
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
			if delivery.EventID != event.ID ||
				delivery.DestinationType != "email" ||
				delivery.Status != models.OutboxDeliveryPending {
				t.Fatalf("unexpected durable email Outbox row: %+v", delivery)
			}
			digest, envelope, err := test.loadSecret(db)
			if err != nil {
				t.Fatal(err)
			}
			if digest == test.plaintext ||
				!security.IsEnvelope(envelope) ||
				strings.Contains(envelope, test.plaintext) {
				t.Fatalf(
					"credential storage is unsafe: digest=%q envelope=%q",
					digest,
					envelope,
				)
			}
			persisted := string(event.Data) + fmt.Sprint(delivery)
			if strings.Contains(persisted, test.plaintext) ||
				strings.Contains(persisted, user.Email) {
				t.Fatal("DomainEvent or Outbox leaked an email credential")
			}
		})
	}
}

func TestRegistrationRollsBackUserProfileCredentialEventAndOutbox(t *testing.T) {
	tests := []struct {
		name      string
		failTable string
	}{
		{name: "DomainEvent insert fails", failTable: "domain_events"},
		{name: "Outbox insert fails", failTable: "outbox_deliveries"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, repository, _ := newAuthEmailOutboxTestRepository(t)
			callbackName := "fail-" + strings.ReplaceAll(test.name, " ", "-")
			if err := db.Callback().Create().
				Before("gorm:create").
				Register(callbackName, func(tx *gorm.DB) {
					if tx.Statement != nil &&
						tx.Statement.Schema != nil &&
						tx.Statement.Schema.Table == test.failTable {
						tx.AddError(errors.New("injected durable email failure"))
					}
				}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = db.Callback().Create().Remove(callbackName)
			})

			user := &User{
				Username:      "registration-rollback",
				Email:         "registration-rollback@example.test",
				PasswordHash:  "test-password-hash",
				Role:          RoleCustomer,
				Status:        StatusActive,
				EmailVerified: false,
			}
			profile := &UserProfile{
				FirstName: "事务",
				LastName:  "回滚",
				Timezone:  "UTC",
				Language:  "zh-CN",
			}
			err := repository.Register(
				context.Background(),
				user,
				profile,
				&EmailVerification{
					Email:     user.Email,
					Token:     "registration-verification-secret",
					ExpiresAt: time.Now().Add(time.Hour),
				},
			)
			if err == nil {
				t.Fatal("expected injected registration failure")
			}

			for table, model := range map[string]any{
				"users":               &models.User{},
				"user_profiles":       &models.UserProfile{},
				"email_verifications": &EmailVerification{},
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
			if user.ID != 0 || profile.ID != 0 {
				t.Fatalf(
					"failed registration exposed committed IDs: user=%d profile=%d",
					user.ID,
					profile.ID,
				)
			}
		})
	}
}

func TestRegistrationCommitsUserProfileAndEmailIntentTogether(t *testing.T) {
	tests := []struct {
		name              string
		requireVerify     bool
		destinationPrefix string
	}{
		{
			name:              "verification required",
			requireVerify:     true,
			destinationPrefix: "auth-verification:",
		},
		{
			name:              "already verified",
			requireVerify:     false,
			destinationPrefix: "auth-welcome:",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, repository, _ := newAuthEmailOutboxTestRepository(t)
			verifiedAt := time.Now()
			user := &User{
				Username:          "registration-success",
				Email:             "registration-success@example.test",
				PasswordHash:      "test-password-hash",
				Role:              RoleCustomer,
				Status:            StatusActive,
				EmailVerified:     !test.requireVerify,
				PasswordChangedAt: &verifiedAt,
			}
			if !test.requireVerify {
				user.EmailVerifiedAt = &verifiedAt
			}
			profile := &UserProfile{
				FirstName:   "持久",
				LastName:    "注册",
				DisplayName: "持久 注册",
				Department:  "支持",
				Position:    "客户",
				Timezone:    "UTC",
				Language:    "zh-CN",
			}
			var verification *EmailVerification
			if test.requireVerify {
				verification = &EmailVerification{
					Email:     user.Email,
					Token:     "registration-success-token",
					ExpiresAt: time.Now().Add(time.Hour),
				}
			}
			if err := repository.Register(
				context.Background(),
				user,
				profile,
				verification,
			); err != nil {
				t.Fatal(err)
			}
			if user.ID == 0 || profile.ID == 0 || profile.UserID != user.ID {
				t.Fatalf("registration IDs are inconsistent: user=%+v profile=%+v", user, profile)
			}
			var storedUser models.User
			if err := db.First(&storedUser, user.ID).Error; err != nil {
				t.Fatal(err)
			}
			var storedProfile models.UserProfile
			if err := db.First(&storedProfile, profile.ID).Error; err != nil {
				t.Fatal(err)
			}
			if storedProfile.UserID != storedUser.ID ||
				storedUser.DisplayName != profile.DisplayName {
				t.Fatalf(
					"stored registration is inconsistent: user=%+v profile=%+v",
					storedUser,
					storedProfile,
				)
			}
			var eventCount, deliveryCount int64
			if err := db.Model(&models.DomainEvent{}).Count(&eventCount).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&models.OutboxDelivery{}).Count(&deliveryCount).Error; err != nil {
				t.Fatal(err)
			}
			if eventCount != 1 || deliveryCount != 1 {
				t.Fatalf(
					"registration event=%d delivery=%d, want one each",
					eventCount,
					deliveryCount,
				)
			}
			var delivery models.OutboxDelivery
			if err := db.First(&delivery).Error; err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(delivery.DestinationID, test.destinationPrefix) {
				t.Fatalf("registration destination = %q", delivery.DestinationID)
			}
		})
	}
}

func TestVerifyEmailRollsBackConsumptionWhenWelcomeIntentCannotPersist(t *testing.T) {
	db, repository, _ := newAuthEmailOutboxTestRepository(t)
	user := seedAuthEmailOutboxUser(t, db)
	verification := &EmailVerification{
		UserID:    user.ID,
		Email:     user.Email,
		Token:     "verify-and-welcome-atomic",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := repository.QueueEmailVerification(
		context.Background(),
		verification,
		"resend",
	); err != nil {
		t.Fatal(err)
	}
	// Remove the original request event so the post-condition counts below
	// describe only the verify transaction.
	if err := db.Exec("DELETE FROM outbox_deliveries").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DELETE FROM domain_events").Error; err != nil {
		t.Fatal(err)
	}

	const callbackName = "fail-verify-welcome-event"
	if err := db.Callback().Create().
		Before("gorm:create").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement != nil &&
				tx.Statement.Schema != nil &&
				tx.Statement.Schema.Table == "domain_events" {
				tx.AddError(errors.New("injected welcome event failure"))
			}
		}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = db.Callback().Create().Remove(callbackName)
	}()

	if _, err := repository.VerifyEmailAndQueueWelcome(
		context.Background(),
		verification.Token,
		time.Now(),
	); err == nil {
		t.Fatal("expected welcome event failure")
	}

	var storedVerification EmailVerification
	if err := db.First(&storedVerification, verification.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedVerification.Used || storedVerification.DeliverySecret == "" {
		t.Fatal("verification token consumption did not roll back")
	}
	var storedUser models.User
	if err := db.First(&storedUser, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedUser.EmailVerified || storedUser.EmailVerifiedAt != nil {
		t.Fatal("user verification state did not roll back")
	}
}

func TestAuthCredentialValidationRejectsPlaintextEmailDeliverySecret(t *testing.T) {
	db, _, protector := newAuthEmailOutboxTestRepository(t)
	passwordHash, err := bcrypt.GenerateFromPassword(
		[]byte("test-password"),
		bcrypt.MinCost,
	)
	if err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:     "plaintext-delivery-secret",
		Email:        "plaintext-delivery-secret@example.test",
		PasswordHash: string(passwordHash),
		Role:         models.RoleCustomer,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&PasswordReset{
		UserID:         user.ID,
		Email:          user.Email,
		Token:          bearerTokenDigest("password-reset", "presented-token"),
		DeliverySecret: "plaintext-token-must-not-start",
		ExpiresAt:      time.Now().Add(time.Hour),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := ValidateAuthCredentialStorage(
		context.Background(),
		db,
		protector,
	); !errors.Is(err, security.ErrPlaintextSecret) {
		t.Fatalf(
			"credential validation error = %v, want ErrPlaintextSecret",
			err,
		)
	}
}

func TestPublicEmailRequestsPreserveEnumerationResistance(t *testing.T) {
	db, repository, _ := newAuthEmailOutboxTestRepository(t)
	verifiedUser := models.User{
		Username:      "already-verified-user",
		Email:         "already-verified@example.test",
		PasswordHash:  "test-password-hash",
		Role:          models.RoleCustomer,
		Status:        models.UserStatusActive,
		EmailVerified: true,
	}
	if err := db.Create(&verifiedUser).Error; err != nil {
		t.Fatal(err)
	}
	service := &AuthService{
		userRepo:        NewGormUserRepository(db),
		emailOutboxRepo: repository,
		config: &AuthConfig{
			EmailVerificationExpire: time.Hour,
			PasswordResetExpire:     time.Hour,
		},
	}
	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "forgot password for unknown mailbox",
			run: func() error {
				return service.ForgotPassword(
					context.Background(),
					"unknown-forgot@example.test",
				)
			},
		},
		{
			name: "resend verification for unknown mailbox",
			run: func() error {
				return service.ResendVerification(
					context.Background(),
					"unknown-resend@example.test",
				)
			},
		},
		{
			name: "resend verification for already verified mailbox",
			run: func() error {
				return service.ResendVerification(
					context.Background(),
					verifiedUser.Email,
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); err != nil {
				t.Fatalf("public request exposed account state: %v", err)
			}
		})
	}
	var eventCount, deliveryCount int64
	if err := db.Model(&models.DomainEvent{}).Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.OutboxDelivery{}).Count(&deliveryCount).Error; err != nil {
		t.Fatal(err)
	}
	if eventCount != 0 || deliveryCount != 0 {
		t.Fatalf(
			"enumeration-safe no-op queued event=%d delivery=%d",
			eventCount,
			deliveryCount,
		)
	}
}
