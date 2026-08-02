package auth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/security"
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
	if err := db.AutoMigrate(
		&models.User{},
		&models.OTPTrustedDevice{},
	); err != nil {
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
		PlatformRole: PlatformRoleMember,
		Status:       StatusActive,
	}
	if err := repository.Create(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	return db, repository, ring, user
}

func TestConfigureOTPRevokesTrustedDevicesInTheSameTransaction(t *testing.T) {
	db, repository, _, user := newOTPSecretStorageTest(t)
	now := time.Now()
	for _, token := range []string{"pre-mfa-one", "pre-mfa-two"} {
		if err := db.Create(&models.OTPTrustedDevice{
			UserID:          user.ID,
			DeviceTokenHash: hashTrustedDeviceToken(token),
			DeviceName:      token,
			LastUsedAt:      now,
			ExpiresAt:       now.Add(time.Hour),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
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
	var activeDevices int64
	if err := db.Model(&models.OTPTrustedDevice{}).
		Where(
			"user_id = ? AND revoked = ? AND expires_at > ?",
			user.ID,
			false,
			time.Now(),
		).
		Count(&activeDevices).Error; err != nil {
		t.Fatal(err)
	}
	if activeDevices != 0 {
		t.Fatalf("active pre-MFA trusted devices = %d", activeDevices)
	}
}

func TestConfigureOTPRollsBackWhenTrustedDeviceRevocationFails(t *testing.T) {
	db, repository, _, user := newOTPSecretStorageTest(t)
	now := time.Now()
	if err := db.Create(&models.OTPTrustedDevice{
		UserID:          user.ID,
		DeviceTokenHash: hashTrustedDeviceToken("rollback-pre-mfa"),
		DeviceName:      "rollback-pre-mfa",
		LastUsedAt:      now,
		ExpiresAt:       now.Add(time.Hour),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		CREATE TRIGGER reject_mfa_trusted_device_revoke
		BEFORE UPDATE ON otp_trusted_devices
		BEGIN
			SELECT RAISE(FAIL, 'injected trusted device failure');
		END
	`).Error; err != nil {
		t.Fatal(err)
	}
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
	); err == nil {
		t.Fatal("ConfigureOTP unexpectedly committed after revoke failure")
	}
	var stored models.User
	if err := db.First(&stored, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.TwoFactorEnabled ||
		stored.TwoFactorSecret != "" ||
		stored.BackupCodes != "" {
		t.Fatalf("MFA state escaped rollback: %+v", stored)
	}
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

func TestAuthCredentialValidationRejectsPlaintextOTPSecret(t *testing.T) {
	db, _, ring, user := newOTPSecretStorageTest(t)
	hashes, err := hashBackupCodes([]string{"ABCDEF12", "ZXCVBN34"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.User{}).Where("id = ?", user.ID).Updates(map[string]interface{}{
		"two_factor_enabled": true,
		"two_factor_secret":  "JBSWY3DPEHPK3PXP",
		"backup_codes":       hashes,
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := ValidateAuthCredentialStorage(
		context.Background(),
		db,
		ring,
	); !errors.Is(err, security.ErrPlaintextSecret) {
		t.Fatalf("validation error = %v, want ErrPlaintextSecret", err)
	}
}

func TestAuthCredentialValidationRejectsUnsupportedPasswordHash(t *testing.T) {
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

func TestAuthCredentialValidationRejectsDisabledCredentialsWithoutRewriting(t *testing.T) {
	db, _, ring, user := newOTPSecretStorageTest(t)
	if err := db.Model(&models.User{}).Where("id = ?", user.ID).Updates(map[string]interface{}{
		"two_factor_enabled": false,
		"two_factor_secret":  "unsupported-disabled-secret",
		"backup_codes":       "unsupported-disabled-code",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := ValidateAuthCredentialStorage(context.Background(), db, ring); err == nil ||
		!strings.Contains(err.Error(), "disabled OTP credentials remain") {
		t.Fatalf("validation error = %v, want disabled OTP credential rejection", err)
	}

	var stored models.User
	if err := db.Unscoped().Select("two_factor_secret", "backup_codes").
		First(&stored, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.TwoFactorSecret != "unsupported-disabled-secret" ||
		stored.BackupCodes != "unsupported-disabled-code" {
		t.Fatal("validation unexpectedly rewrote disabled OTP credentials")
	}
}

func TestAuthCredentialValidationChecksSoftDeletedBackupCodes(t *testing.T) {
	db, _, ring, user := newOTPSecretStorageTest(t)
	envelope, err := ring.Seal(
		[]byte("JBSWY3DPEHPK3PXP"),
		otpSecretAAD(user.ID),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.User{}).Where("id = ?", user.ID).Updates(map[string]interface{}{
		"two_factor_enabled": true,
		"two_factor_secret":  envelope,
		"backup_codes":       "ABCDEF12",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&models.User{}, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := ValidateAuthCredentialStorage(context.Background(), db, ring); !errors.Is(err, ErrInvalidBackupCodeStorage) {
		t.Fatalf("validation error = %v, want ErrInvalidBackupCodeStorage", err)
	}
	var stored models.User
	if err := db.Unscoped().Select("two_factor_secret", "backup_codes").
		First(&stored, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.TwoFactorSecret != envelope || stored.BackupCodes != "ABCDEF12" {
		t.Fatal("validation unexpectedly rewrote soft-deleted credentials")
	}
}
