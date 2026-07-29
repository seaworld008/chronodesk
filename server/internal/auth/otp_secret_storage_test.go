package auth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"gongdan-system/internal/models"
	"gongdan-system/internal/security"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newOTPSecretStorageTest(
	t *testing.T,
) (*gorm.DB, *GormUserRepository, security.Protector, *User) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "-")+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)

	ring, err := security.NewKeyring(
		"auth-test-v1",
		map[string][]byte{"auth-test-v1": bytes.Repeat([]byte{0x42}, 32)},
	)
	if err != nil {
		t.Fatal(err)
	}
	repository := NewGormUserRepository(db, ring).(*GormUserRepository)
	user := &User{
		Username:     "otp-storage-user",
		Email:        "otp-storage@example.test",
		PasswordHash: "$2a$10$3fG2XevM/i0vGg3tnBFDGuE6PoIgto7HGMlZosX8KCOj4I8tC9q2a",
		Role:         RoleUser,
		Status:       StatusActive,
	}
	if err := repository.Create(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	return db, repository, ring, user
}

func TestOTPSecretAndBackupCodesAreProtectedAtRest(t *testing.T) {
	db, repository, ring, user := newOTPSecretStorageTest(t)
	plaintextSecret := "JBSWY3DPEHPK3PXP"
	plaintextCodes := []string{"ABCDEF12", "ZXCVBN34"}
	hashes, err := hashBackupCodes(plaintextCodes)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ConfigureOTP(
		context.Background(),
		user.ID,
		plaintextSecret,
		hashes,
		true,
	); err != nil {
		t.Fatal(err)
	}

	var stored models.User
	if err := db.Select("id", "two_factor_secret", "backup_codes").First(&stored, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !security.IsEnvelope(stored.TwoFactorSecret) ||
		stored.TwoFactorSecret == plaintextSecret ||
		strings.Contains(stored.TwoFactorSecret, plaintextSecret) {
		t.Fatal("TOTP secret was not protected by a versioned envelope")
	}
	for _, code := range plaintextCodes {
		if strings.Contains(stored.BackupCodes, code) {
			t.Fatal("backup code was stored in plaintext")
		}
	}

	restarted := NewGormUserRepository(db, ring).(*GormUserRepository)
	loaded, err := restarted.GetByID(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.OTPSecret != plaintextSecret {
		t.Fatal("TOTP secret did not survive restart with the same keyring")
	}
	if _, err := parseBackupCodeHashes(loaded.BackupCodes); err != nil {
		t.Fatalf("stored backup codes are not strong hashes: %v", err)
	}
}

func TestOTPSecretFailsClosedWithoutCorrectKey(t *testing.T) {
	db, repository, _, user := newOTPSecretStorageTest(t)
	hashes, err := hashBackupCodes([]string{"ABCDEF12"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ConfigureOTP(
		context.Background(),
		user.ID,
		"JBSWY3DPEHPK3PXP",
		hashes,
		true,
	); err != nil {
		t.Fatal(err)
	}

	if _, err := NewGormUserRepository(db).GetByID(context.Background(), user.ID); !errors.Is(err, security.ErrKeyringUnavailable) {
		t.Fatalf("nil keyring error = %v, want ErrKeyringUnavailable", err)
	}
	wrong, err := security.NewKeyring(
		"auth-test-v1",
		map[string][]byte{"auth-test-v1": bytes.Repeat([]byte{0x24}, 32)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewGormUserRepository(db, wrong).GetByID(context.Background(), user.ID); !errors.Is(err, security.ErrAuthentication) {
		t.Fatalf("wrong key error = %v, want ErrAuthentication", err)
	}
}

func TestBackupCodeCanOnlyBeConsumedOnceConcurrently(t *testing.T) {
	_, repository, _, user := newOTPSecretStorageTest(t)
	code := "ABCDEF12"
	hashes, err := hashBackupCodes([]string{code})
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ConfigureOTP(
		context.Background(),
		user.ID,
		"JBSWY3DPEHPK3PXP",
		hashes,
		true,
	); err != nil {
		t.Fatal(err)
	}

	const callers = 8
	var wg sync.WaitGroup
	results := make(chan bool, callers)
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			consumed, consumeErr := repository.ConsumeBackupCode(
				context.Background(),
				user.ID,
				code,
			)
			results <- consumed
			errs <- consumeErr
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	successes := 0
	for consumed := range results {
		if consumed {
			successes++
		}
	}
	for consumeErr := range errs {
		if consumeErr != nil {
			t.Fatalf("concurrent consume error: %v", consumeErr)
		}
	}
	if successes != 1 {
		t.Fatalf("successful consumptions = %d, want exactly 1", successes)
	}
}

func TestLegacyOTPCredentialsRequireExplicitIdempotentMigration(t *testing.T) {
	db, _, ring, user := newOTPSecretStorageTest(t)
	if err := db.Model(&models.User{}).Where("id = ?", user.ID).Updates(map[string]interface{}{
		"two_factor_enabled": true,
		"two_factor_secret":  "JBSWY3DPEHPK3PXP",
		"backup_codes":       "ABCDEF12,ZXCVBN34",
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := ValidateAuthCredentialStorage(context.Background(), db, ring); err == nil {
		t.Fatal("runtime validation accepted plaintext OTP credentials")
	}
	report, err := MigrateLegacyAuthCredentials(context.Background(), db, ring)
	if err != nil {
		t.Fatal(err)
	}
	if report.EncryptedOTPSecrets != 1 || report.HashedBackupCodes != 1 {
		t.Fatalf("migration report = %+v, want one encrypted secret and one hashed code set", report)
	}
	if err := ValidateAuthCredentialStorage(context.Background(), db, ring); err != nil {
		t.Fatalf("post-migration validation failed: %v", err)
	}
	second, err := MigrateLegacyAuthCredentials(context.Background(), db, ring)
	if err != nil {
		t.Fatal(err)
	}
	if second != (CredentialMigrationReport{}) {
		t.Fatalf("second migration was not idempotent: %+v", second)
	}
}

func TestAuthCredentialValidationRejectsLegacyPasswordHash(t *testing.T) {
	db, _, ring, user := newOTPSecretStorageTest(t)
	if err := db.Model(&models.User{}).
		Where("id = ?", user.ID).
		Update("password_hash", strings.Repeat("a", 64)).Error; err != nil {
		t.Fatal(err)
	}
	err := ValidateAuthCredentialStorage(context.Background(), db, ring)
	if err == nil || !strings.Contains(err.Error(), "unsupported password hash") {
		t.Fatalf("validation error = %v, want unsupported password hash", err)
	}
}

func TestCredentialMigrationAlsoProtectsSoftDeletedUsers(t *testing.T) {
	db, _, ring, user := newOTPSecretStorageTest(t)
	if err := db.Model(&models.User{}).Where("id = ?", user.ID).Updates(map[string]interface{}{
		"two_factor_enabled": true,
		"two_factor_secret":  "JBSWY3DPEHPK3PXP",
		"backup_codes":       "ABCDEF12",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&models.User{}, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateLegacyAuthCredentials(context.Background(), db, ring); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAuthCredentialStorage(context.Background(), db, ring); err != nil {
		t.Fatal(err)
	}
	var stored models.User
	if err := db.Unscoped().Select("two_factor_secret", "backup_codes").
		First(&stored, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !security.IsEnvelope(stored.TwoFactorSecret) {
		t.Fatal("soft-deleted user's TOTP secret remained plaintext")
	}
	if strings.Contains(stored.BackupCodes, "ABCDEF12") {
		t.Fatal("soft-deleted user's backup code remained plaintext")
	}
}
