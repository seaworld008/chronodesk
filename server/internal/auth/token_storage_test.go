package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"gongdan-system/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTokenStorageTestRepository(t *testing.T) (*gorm.DB, TokenRepository, User) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "-")+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&RefreshToken{},
		&EmailVerification{},
		&PasswordReset{},
		&OTPCode{},
		&models.LoginHistory{},
	); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	modelUser := models.User{
		Username:     "token-storage-user",
		Email:        "token-storage@example.test",
		PasswordHash: "not-a-real-password",
		Role:         models.RoleUser,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&modelUser).Error; err != nil {
		t.Fatal(err)
	}
	user := User{
		ID:           modelUser.ID,
		Username:     modelUser.Username,
		Email:        modelUser.Email,
		PasswordHash: modelUser.PasswordHash,
		Role:         RoleUser,
		Status:       StatusActive,
	}
	return db, NewGormTokenRepository(db), user
}

func TestBearerCredentialsAreStoredOnlyAsPurposeSeparatedDigests(t *testing.T) {
	db, repository, user := newTokenStorageTestRepository(t)
	ctx := context.Background()
	expiresAt := time.Now().Add(time.Hour)
	presentedSecret := "same-presented-secret"

	refreshPlaintext := presentedSecret
	refresh := &RefreshToken{
		UserID: user.ID, Token: refreshPlaintext, SessionID: "session-1",
		ExpiresAt: expiresAt,
	}
	if err := repository.CreateRefreshToken(ctx, refresh); err != nil {
		t.Fatal(err)
	}
	if refresh.Token != refreshPlaintext {
		t.Fatal("repository must not replace the caller's one-time credential")
	}
	var storedRefresh RefreshToken
	if err := db.First(&storedRefresh, refresh.ID).Error; err != nil {
		t.Fatal(err)
	}
	assertCredentialDigestAtRest(t, storedRefresh.Token, refreshPlaintext)
	if _, err := repository.GetRefreshToken(ctx, refreshPlaintext); err != nil {
		t.Fatalf("lookup by presented refresh token failed: %v", err)
	}

	verificationPlaintext := presentedSecret
	verification := &EmailVerification{
		UserID: user.ID, Email: user.Email, Token: verificationPlaintext,
		ExpiresAt: expiresAt,
	}
	if err := repository.CreateEmailVerification(ctx, verification); err != nil {
		t.Fatal(err)
	}
	var storedVerification EmailVerification
	if err := db.First(&storedVerification, verification.ID).Error; err != nil {
		t.Fatal(err)
	}
	assertCredentialDigestAtRest(t, storedVerification.Token, verificationPlaintext)
	if _, err := repository.GetEmailVerification(ctx, verificationPlaintext); err != nil {
		t.Fatalf("lookup by presented verification token failed: %v", err)
	}

	resetPlaintext := presentedSecret
	reset := &PasswordReset{
		UserID: user.ID, Email: user.Email, Token: resetPlaintext,
		ExpiresAt: expiresAt,
	}
	if err := repository.CreatePasswordReset(ctx, reset); err != nil {
		t.Fatal(err)
	}
	var storedReset PasswordReset
	if err := db.First(&storedReset, reset.ID).Error; err != nil {
		t.Fatal(err)
	}
	assertCredentialDigestAtRest(t, storedReset.Token, resetPlaintext)
	if _, err := repository.GetPasswordReset(ctx, resetPlaintext); err != nil {
		t.Fatalf("lookup by presented reset token failed: %v", err)
	}

	otpPlaintext := presentedSecret
	otp := &OTPCode{
		UserID: user.ID, Code: otpPlaintext, Type: "login", ExpiresAt: expiresAt,
	}
	if err := repository.CreateOTPCode(ctx, otp); err != nil {
		t.Fatal(err)
	}
	var storedOTP OTPCode
	if err := db.First(&storedOTP, otp.ID).Error; err != nil {
		t.Fatal(err)
	}
	assertCredentialDigestAtRest(t, storedOTP.Code, otpPlaintext)
	if _, err := repository.GetOTPCode(ctx, user.ID, otpPlaintext); err != nil {
		t.Fatalf("lookup by presented OTP failed: %v", err)
	}

	digests := []string{
		storedRefresh.Token,
		storedVerification.Token,
		storedReset.Token,
		storedOTP.Code,
	}
	for i := range digests {
		for j := i + 1; j < len(digests); j++ {
			if digests[i] == digests[j] {
				t.Fatalf("credential purposes share a digest: index %d and %d", i, j)
			}
		}
	}
}

func TestPlaintextLegacyCredentialRowsAreRejected(t *testing.T) {
	db, repository, user := newTokenStorageTestRepository(t)
	plaintext := "legacy-plaintext-refresh-token"
	row := RefreshToken{
		UserID: user.ID, Token: plaintext, SessionID: "legacy-session",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	// Direct insertion simulates a pre-hardening row. The current contract
	// intentionally has no plaintext compatibility lookup.
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetRefreshToken(context.Background(), plaintext); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("plaintext legacy row error = %v, want ErrInvalidToken", err)
	}
}

func TestOneTimeCredentialConsumersRejectReplay(t *testing.T) {
	_, repository, user := newTokenStorageTestRepository(t)
	ctx := context.Background()
	expiresAt := time.Now().Add(time.Hour)

	verification := &EmailVerification{
		UserID: user.ID, Email: user.Email, Token: "verification-once",
		ExpiresAt: expiresAt,
	}
	if err := repository.CreateEmailVerification(ctx, verification); err != nil {
		t.Fatal(err)
	}
	if err := repository.UseEmailVerification(ctx, verification.Token); err != nil {
		t.Fatal(err)
	}
	if err := repository.UseEmailVerification(ctx, verification.Token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("email verification replay error = %v, want ErrInvalidToken", err)
	}

	reset := &PasswordReset{
		UserID: user.ID, Email: user.Email, Token: "password-reset-once",
		ExpiresAt: expiresAt,
	}
	if err := repository.CreatePasswordReset(ctx, reset); err != nil {
		t.Fatal(err)
	}
	if err := repository.UsePasswordReset(ctx, reset.Token); err != nil {
		t.Fatal(err)
	}
	if err := repository.UsePasswordReset(ctx, reset.Token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("password reset replay error = %v, want ErrInvalidToken", err)
	}

	otp := &OTPCode{
		UserID: user.ID, Code: "otp-once", Type: "login", ExpiresAt: expiresAt,
	}
	if err := repository.CreateOTPCode(ctx, otp); err != nil {
		t.Fatal(err)
	}
	if err := repository.UseOTPCode(ctx, user.ID, otp.Code); err != nil {
		t.Fatal(err)
	}
	if err := repository.UseOTPCode(ctx, user.ID, otp.Code); !errors.Is(err, ErrInvalidOTP) {
		t.Fatalf("OTP replay error = %v, want ErrInvalidOTP", err)
	}
}

func TestRefreshRotationIsAtomicAndSingleWinner(t *testing.T) {
	db, repository, user := newTokenStorageTestRepository(t)
	ctx := context.Background()
	current := "refresh-current"
	if err := repository.CreateRefreshToken(ctx, &RefreshToken{
		UserID: user.ID, Token: current, SessionID: "rotation-session",
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	const callers = 8
	type outcome struct {
		token string
		err   error
	}
	outcomes := make(chan outcome, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			next := "refresh-next-" + string(rune('a'+index))
			err := repository.RotateRefreshToken(ctx, current, &RefreshToken{
				UserID: user.ID, Token: next, SessionID: "rotation-session",
				ExpiresAt: time.Now().Add(time.Hour),
			})
			outcomes <- outcome{token: next, err: err}
		}(i)
	}
	wg.Wait()
	close(outcomes)

	successes := 0
	for result := range outcomes {
		if result.err == nil {
			successes++
			continue
		}
		if !errors.Is(result.err, ErrInvalidToken) {
			t.Fatalf("rotation error = %v, want ErrInvalidToken", result.err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful rotations = %d, want exactly 1", successes)
	}
	var active int64
	if err := db.Model(&RefreshToken{}).
		Where("user_id = ? AND revoked = ?", user.ID, false).
		Count(&active).Error; err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("active refresh tokens = %d, want 1", active)
	}
}

func TestRefreshRotationRollsBackWhenReplacementCannotBeStored(t *testing.T) {
	_, repository, user := newTokenStorageTestRepository(t)
	ctx := context.Background()
	current := "refresh-current-rollback"
	duplicate := "refresh-duplicate"
	for _, token := range []string{current, duplicate} {
		if err := repository.CreateRefreshToken(ctx, &RefreshToken{
			UserID: user.ID, Token: token, SessionID: "rollback-session",
			ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.RotateRefreshToken(ctx, current, &RefreshToken{
		UserID: user.ID, Token: duplicate, SessionID: "rollback-session",
		ExpiresAt: time.Now().Add(time.Hour),
	}); err == nil {
		t.Fatal("rotation unexpectedly stored a duplicate replacement")
	}
	if _, err := repository.GetRefreshToken(ctx, current); err != nil {
		t.Fatalf("failed rotation revoked the current token: %v", err)
	}
}

func TestEmailVerificationTransactionHasSingleWinner(t *testing.T) {
	db, repository, user := newTokenStorageTestRepository(t)
	token := "verify-transaction"
	if err := repository.CreateEmailVerification(context.Background(), &EmailVerification{
		UserID: user.ID, Email: user.Email, Token: token,
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	const callers = 8
	results := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := repository.VerifyEmailWithToken(context.Background(), token, time.Now())
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("verification error = %v, want ErrInvalidToken", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful verifications = %d, want 1", successes)
	}
	var stored models.User
	if err := db.First(&stored, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !stored.EmailVerified || stored.EmailVerifiedAt == nil {
		t.Fatal("winning verification transaction did not update the user")
	}
}

func TestPasswordResetTransactionHasSingleWinnerAndRevokesSessions(t *testing.T) {
	db, repository, user := newTokenStorageTestRepository(t)
	ctx := context.Background()
	originalPassword := user.PasswordHash
	resetToken := "reset-transaction"
	if err := repository.CreatePasswordReset(ctx, &PasswordReset{
		UserID: user.ID, Email: user.Email, Token: resetToken,
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateRefreshToken(ctx, &RefreshToken{
		UserID: user.ID, Token: "active-before-reset", SessionID: "reset-session",
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	loginAt := time.Now()
	if err := db.Create(&models.LoginHistory{
		UserID: user.ID, SessionID: "reset-session", IsActive: true,
		LoginTime: loginAt, LastActivityAt: &loginAt,
		LoginStatus: models.LoginStatusSuccess,
	}).Error; err != nil {
		t.Fatal(err)
	}

	hashes := []string{
		"$2a$10$0KZ1mHspJcGE8C3yjdR6LeXdVXsq0fYiRhMN1OiKZghW9LueDq8Qe",
		"$2a$10$4CY1hw3FJ9u9z9Stj.iR7OzA5ZJZsPbDzs8Y1p0UDyTIjk8xIsaeK",
	}
	type outcome struct {
		hash string
		err  error
	}
	outcomes := make(chan outcome, len(hashes))
	var wg sync.WaitGroup
	for _, hash := range hashes {
		wg.Add(1)
		go func(candidate string) {
			defer wg.Done()
			_, err := repository.ResetPasswordWithToken(
				ctx,
				resetToken,
				candidate,
				time.Now(),
			)
			outcomes <- outcome{hash: candidate, err: err}
		}(hash)
	}
	wg.Wait()
	close(outcomes)

	successes := 0
	winningHash := ""
	for result := range outcomes {
		if result.err == nil {
			successes++
			winningHash = result.hash
			continue
		}
		if !errors.Is(result.err, ErrInvalidToken) {
			t.Fatalf("password reset error = %v, want ErrInvalidToken", result.err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful password resets = %d, want 1", successes)
	}
	var stored User
	if err := db.First(&stored, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.PasswordHash == originalPassword || stored.PasswordHash != winningHash {
		t.Fatal("password does not match the sole winning transaction")
	}
	var activeRefresh, activeHistory int64
	if err := db.Model(&RefreshToken{}).
		Where("user_id = ? AND revoked = ?", user.ID, false).
		Count(&activeRefresh).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.LoginHistory{}).
		Where("user_id = ? AND is_active = ?", user.ID, true).
		Count(&activeHistory).Error; err != nil {
		t.Fatal(err)
	}
	if activeRefresh != 0 || activeHistory != 0 {
		t.Fatalf("active refresh/history = %d/%d, want 0/0", activeRefresh, activeHistory)
	}
}

func TestPasswordResetRollsBackWhenSessionRevocationFails(t *testing.T) {
	db, repository, user := newTokenStorageTestRepository(t)
	resetToken := "reset-rollback"
	if err := repository.CreatePasswordReset(context.Background(), &PasswordReset{
		UserID: user.ID, Email: user.Email, Token: resetToken,
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropTable(&models.LoginHistory{}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ResetPasswordWithToken(
		context.Background(),
		resetToken,
		"$2a$10$0KZ1mHspJcGE8C3yjdR6LeXdVXsq0fYiRhMN1OiKZghW9LueDq8Qe",
		time.Now(),
	); err == nil {
		t.Fatal("reset unexpectedly succeeded without session revocation storage")
	}
	var reset PasswordReset
	if err := db.Where(
		"token = ?",
		bearerTokenDigest("password-reset", resetToken),
	).Take(&reset).Error; err != nil {
		t.Fatal(err)
	}
	if reset.Used {
		t.Fatal("failed reset transaction consumed the token")
	}
	var stored User
	if err := db.First(&stored, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.PasswordHash != user.PasswordHash {
		t.Fatal("failed reset transaction changed the password")
	}
}

func assertCredentialDigestAtRest(t *testing.T, stored, plaintext string) {
	t.Helper()
	if stored == "" || stored == plaintext || strings.Contains(stored, plaintext) {
		t.Fatalf("credential was not irreversibly protected at rest: %q", stored)
	}
	if len(stored) != 64 {
		t.Fatalf("digest length = %d, want 64 hexadecimal characters", len(stored))
	}
}
