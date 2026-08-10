package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestPasswordResetIssuanceLocksTheAccountSerializationRow(t *testing.T) {
	db, emailRepository, _ := newAuthEmailOutboxTestRepository(t)
	user := seedAuthEmailOutboxUser(t, db)
	tokenRepository := NewGormTokenRepository(db)

	var lockedTables []string
	const callbackName = "test:password-reset-issuance-lock"
	if err := db.Callback().Query().After("gorm:query").Register(
		callbackName,
		func(query *gorm.DB) {
			lockClause, exists := query.Statement.Clauses["FOR"]
			if !exists {
				return
			}
			locking, ok := lockClause.Expression.(clause.Locking)
			if !ok || locking.Strength != "UPDATE" {
				return
			}
			lockedTables = append(lockedTables, query.Statement.Table)
		},
	); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Query().Remove(callbackName)
	})

	for token, issue := range map[string]func(*PasswordReset) error{
		"durable-password-reset-lock": func(reset *PasswordReset) error {
			return emailRepository.QueuePasswordReset(context.Background(), reset)
		},
		"repository-password-reset-lock": func(reset *PasswordReset) error {
			return tokenRepository.CreatePasswordReset(context.Background(), reset)
		},
	} {
		if err := issue(&PasswordReset{
			UserID:    user.ID,
			Email:     user.Email,
			Token:     token,
			ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatalf("issue %q: %v", token, err)
		}
	}
	if len(lockedTables) != 2 ||
		lockedTables[0] != "users" ||
		lockedTables[1] != "users" {
		t.Fatalf(
			"password-reset issuance locks = %v, want [users users]",
			lockedTables,
		)
	}
}

func TestPasswordResetInvalidatesEveryOtherActiveTokenForTheAccount(
	t *testing.T,
) {
	db, repository, user := newTokenStorageTestRepository(t)
	ctx := context.Background()
	expiresAt := time.Now().Add(time.Hour)
	firstToken := "account-reset-first"
	secondToken := "account-reset-second"

	for _, token := range []string{firstToken, secondToken} {
		if err := repository.CreatePasswordReset(ctx, &PasswordReset{
			UserID:    user.ID,
			Email:     user.Email,
			Token:     token,
			ExpiresAt: expiresAt,
		}); err != nil {
			t.Fatalf("create password reset %q: %v", token, err)
		}
	}

	secondPasswordHash := "second-password-hash"
	if _, err := repository.ResetPasswordWithToken(
		ctx,
		secondToken,
		secondPasswordHash,
		time.Now(),
	); err != nil {
		t.Fatalf("consume second password reset: %v", err)
	}

	if _, err := repository.ResetPasswordWithToken(
		ctx,
		firstToken,
		"stale-first-password-hash",
		time.Now(),
	); !errors.Is(err, ErrInvalidToken) {
		t.Errorf(
			"first password reset after second succeeded = %v, want ErrInvalidToken",
			err,
		)
	}
	if _, err := repository.GetPasswordReset(ctx, firstToken); !errors.Is(
		err,
		ErrInvalidToken,
	) {
		t.Errorf("first password reset remains active: %v", err)
	}

	var storedUser models.User
	if err := db.First(&storedUser, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedUser.PasswordHash != secondPasswordHash {
		t.Errorf(
			"stale reset changed password hash to %q, want %q",
			storedUser.PasswordHash,
			secondPasswordHash,
		)
	}
}

func TestConcurrentPasswordResetTokensHaveOneAccountWideWinner(t *testing.T) {
	db, repository, user := newTokenStorageTestRepository(t)
	ctx := context.Background()
	expiresAt := time.Now().Add(time.Hour)
	candidates := []struct {
		token        string
		passwordHash string
	}{
		{
			token:        "concurrent-account-reset-a",
			passwordHash: "concurrent-password-hash-a",
		},
		{
			token:        "concurrent-account-reset-b",
			passwordHash: "concurrent-password-hash-b",
		},
	}
	for _, candidate := range candidates {
		if err := repository.CreatePasswordReset(ctx, &PasswordReset{
			UserID:    user.ID,
			Email:     user.Email,
			Token:     candidate.token,
			ExpiresAt: expiresAt,
		}); err != nil {
			t.Fatalf("create password reset %q: %v", candidate.token, err)
		}
	}

	type outcome struct {
		token        string
		passwordHash string
		err          error
	}
	outcomes := make(chan outcome, len(candidates))
	var waitGroup sync.WaitGroup
	for _, candidate := range candidates {
		candidate := candidate
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			_, err := repository.ResetPasswordWithToken(
				ctx,
				candidate.token,
				candidate.passwordHash,
				time.Now(),
			)
			outcomes <- outcome{
				token:        candidate.token,
				passwordHash: candidate.passwordHash,
				err:          err,
			}
		}()
	}
	waitGroup.Wait()
	close(outcomes)

	successes := 0
	winningHash := ""
	for result := range outcomes {
		switch {
		case result.err == nil:
			successes++
			winningHash = result.passwordHash
		case errors.Is(result.err, ErrInvalidToken):
		default:
			t.Errorf(
				"password reset %q failed with %v, want ErrInvalidToken",
				result.token,
				result.err,
			)
		}
	}
	if successes != 1 {
		t.Errorf("successful account-wide password resets = %d, want 1", successes)
	}

	var storedUser models.User
	if err := db.First(&storedUser, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if successes == 1 && storedUser.PasswordHash != winningHash {
		t.Errorf(
			"stored password hash = %q, want sole winner %q",
			storedUser.PasswordHash,
			winningHash,
		)
	}
	for _, candidate := range candidates {
		if _, err := repository.GetPasswordReset(
			ctx,
			candidate.token,
		); !errors.Is(err, ErrInvalidToken) {
			t.Errorf(
				"password reset %q remains active after account-wide winner: %v",
				candidate.token,
				err,
			)
		}
	}
}

func TestEmailVerificationTokenRejectsChangedAccountEmailWithoutSideEffects(
	t *testing.T,
) {
	db, emailRepository, _ := newAuthEmailOutboxTestRepository(t)
	migrateTokenInvariantSessionTables(t, db)
	user := seedAuthEmailOutboxUser(t, db)
	ctx := context.Background()
	token := "verification-bound-to-original-email"
	verification := &EmailVerification{
		UserID:    user.ID,
		Email:     user.Email,
		Token:     token,
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := emailRepository.QueueEmailVerification(
		ctx,
		verification,
		"email-binding-test",
	); err != nil {
		t.Fatal(err)
	}
	tokenRepository := NewGormTokenRepository(db)
	refreshToken, sessionID := seedTokenInvariantSession(
		t,
		db,
		tokenRepository,
		user.ID,
	)
	beforeEvents, beforeDeliveries := countTokenInvariantOutbox(t, db)

	changedEmail := "changed-verification@example.test"
	if err := db.Model(&models.User{}).
		Where("id = ?", user.ID).
		Updates(map[string]any{
			"email":             changedEmail,
			"email_verified":    false,
			"email_verified_at": nil,
		}).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := emailRepository.VerifyEmailAndQueueWelcome(
		ctx,
		token,
		time.Now(),
	); !errors.Is(err, ErrInvalidToken) {
		t.Errorf(
			"verification token for the previous email was consumed: %v",
			err,
		)
	}

	assertTokenInvariantUserUnchanged(
		t,
		db,
		user.ID,
		changedEmail,
		user.PasswordHash,
	)
	var storedVerification EmailVerification
	if err := db.First(&storedVerification, verification.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedVerification.Used ||
		storedVerification.UsedAt != nil ||
		storedVerification.DeliverySecret == "" {
		t.Errorf(
			"rejected verification token was mutated: %+v",
			storedVerification,
		)
	}
	assertTokenInvariantSessionActive(
		t,
		db,
		tokenRepository,
		user.ID,
		sessionID,
		refreshToken,
	)
	assertTokenInvariantOutboxCounts(t, db, beforeEvents, beforeDeliveries)
}

func TestPasswordResetTokenRejectsChangedAccountEmailWithoutSideEffects(
	t *testing.T,
) {
	db, emailRepository, _ := newAuthEmailOutboxTestRepository(t)
	migrateTokenInvariantSessionTables(t, db)
	user := seedAuthEmailOutboxUser(t, db)
	ctx := context.Background()
	token := "password-reset-bound-to-original-email"
	reset := &PasswordReset{
		UserID:    user.ID,
		Email:     user.Email,
		Token:     token,
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := emailRepository.QueuePasswordReset(ctx, reset); err != nil {
		t.Fatal(err)
	}
	tokenRepository := NewGormTokenRepository(db)
	refreshToken, sessionID := seedTokenInvariantSession(
		t,
		db,
		tokenRepository,
		user.ID,
	)
	beforeEvents, beforeDeliveries := countTokenInvariantOutbox(t, db)

	changedEmail := "changed-password-reset@example.test"
	if err := db.Model(&models.User{}).
		Where("id = ?", user.ID).
		Updates(map[string]any{
			"email":             changedEmail,
			"email_verified":    false,
			"email_verified_at": nil,
		}).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := tokenRepository.ResetPasswordWithToken(
		ctx,
		token,
		"must-not-replace-password-hash",
		time.Now(),
	); !errors.Is(err, ErrInvalidToken) {
		t.Errorf(
			"password reset token for the previous email was consumed: %v",
			err,
		)
	}

	assertTokenInvariantUserUnchanged(
		t,
		db,
		user.ID,
		changedEmail,
		user.PasswordHash,
	)
	var storedReset PasswordReset
	if err := db.First(&storedReset, reset.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedReset.Used ||
		storedReset.UsedAt != nil ||
		storedReset.DeliverySecret == "" {
		t.Errorf("rejected password reset token was mutated: %+v", storedReset)
	}
	assertTokenInvariantSessionActive(
		t,
		db,
		tokenRepository,
		user.ID,
		sessionID,
		refreshToken,
	)
	assertTokenInvariantOutboxCounts(t, db, beforeEvents, beforeDeliveries)
}

func migrateTokenInvariantSessionTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(&RefreshToken{}, &models.LoginHistory{}); err != nil {
		t.Fatal(err)
	}
}

func seedTokenInvariantSession(
	t *testing.T,
	db *gorm.DB,
	repository TokenRepository,
	userID uint,
) (string, string) {
	t.Helper()
	refreshToken := "email-binding-refresh-token"
	sessionID := "email-binding-session"
	if err := repository.CreateRefreshToken(
		context.Background(),
		&RefreshToken{
			UserID:    userID,
			Token:     refreshToken,
			SessionID: sessionID,
			ExpiresAt: time.Now().Add(time.Hour),
		},
	); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := db.Create(&models.LoginHistory{
		UserID:         userID,
		SessionID:      sessionID,
		LoginTime:      now,
		LastActivityAt: &now,
		LoginStatus:    models.LoginStatusSuccess,
		IsActive:       true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	return refreshToken, sessionID
}

func assertTokenInvariantUserUnchanged(
	t *testing.T,
	db *gorm.DB,
	userID uint,
	wantEmail,
	wantPasswordHash string,
) {
	t.Helper()
	var storedUser models.User
	if err := db.First(&storedUser, userID).Error; err != nil {
		t.Fatal(err)
	}
	if storedUser.Email != wantEmail ||
		storedUser.EmailVerified ||
		storedUser.EmailVerifiedAt != nil ||
		storedUser.PasswordHash != wantPasswordHash ||
		storedUser.PasswordResetAt != nil {
		t.Errorf("rejected old-email token changed user: %+v", storedUser)
	}
}

func assertTokenInvariantSessionActive(
	t *testing.T,
	db *gorm.DB,
	repository TokenRepository,
	userID uint,
	sessionID,
	refreshToken string,
) {
	t.Helper()
	if _, err := repository.GetRefreshToken(
		context.Background(),
		refreshToken,
	); err != nil {
		t.Errorf("rejected old-email token revoked refresh token: %v", err)
	}
	active, err := repository.IsSessionActive(
		context.Background(),
		userID,
		sessionID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Error("rejected old-email token ended the login session")
	}
	var activeHistory int64
	if err := db.Model(&models.LoginHistory{}).
		Where(
			"user_id = ? AND session_id = ? AND is_active = ?",
			userID,
			sessionID,
			true,
		).
		Count(&activeHistory).Error; err != nil {
		t.Fatal(err)
	}
	if activeHistory != 1 {
		t.Errorf("active login history rows = %d, want 1", activeHistory)
	}
}

func countTokenInvariantOutbox(t *testing.T, db *gorm.DB) (int64, int64) {
	t.Helper()
	var events int64
	if err := db.Model(&models.DomainEvent{}).Count(&events).Error; err != nil {
		t.Fatal(err)
	}
	var deliveries int64
	if err := db.Model(&models.OutboxDelivery{}).
		Count(&deliveries).Error; err != nil {
		t.Fatal(err)
	}
	return events, deliveries
}

func assertTokenInvariantOutboxCounts(
	t *testing.T,
	db *gorm.DB,
	wantEvents,
	wantDeliveries int64,
) {
	t.Helper()
	events, deliveries := countTokenInvariantOutbox(t, db)
	if events != wantEvents || deliveries != wantDeliveries {
		t.Errorf(
			"rejected old-email token changed Outbox counts to %d/%d, want %d/%d",
			events,
			deliveries,
			wantEvents,
			wantDeliveries,
		)
	}
}
