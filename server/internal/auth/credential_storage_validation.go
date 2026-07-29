package auth

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/security"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// ValidateAuthCredentialStorage is the startup and operator fail-fast gate. It
// accepts only bcrypt password hashes, authenticated TOTP envelopes, and
// bcrypt backup-code hashes. Unsupported storage is never rewritten here.
func ValidateAuthCredentialStorage(
	ctx context.Context,
	db *gorm.DB,
	protector security.Protector,
) error {
	if db == nil {
		return errors.New("auth credential database is unavailable")
	}
	var users []models.User
	if err := db.WithContext(ctx).Unscoped().Select(
		"id",
		"password_hash",
		"two_factor_enabled",
		"two_factor_secret",
		"backup_codes",
	).Find(&users).Error; err != nil {
		return err
	}
	for i := range users {
		user := &users[i]
		if _, err := bcrypt.Cost([]byte(user.PasswordHash)); err != nil {
			return fmt.Errorf("user %d has an unsupported password hash", user.ID)
		}
		if !user.TwoFactorEnabled {
			if user.TwoFactorSecret != "" || user.BackupCodes != "" {
				return fmt.Errorf("disabled OTP credentials remain for user %d", user.ID)
			}
			continue
		}
		if protector == nil {
			return security.ErrKeyringUnavailable
		}
		plaintext, err := protector.Open(user.TwoFactorSecret, otpSecretAAD(user.ID))
		if err != nil {
			return fmt.Errorf("validate OTP secret for user %d: %w", user.ID, err)
		}
		clear(plaintext)
		if _, err := parseBackupCodeHashes(user.BackupCodes); err != nil {
			return fmt.Errorf("validate backup codes for user %d: %w", user.ID, err)
		}
	}
	var verifications []EmailVerification
	if err := db.WithContext(ctx).Select("id", "delivery_secret").
		Find(&verifications).Error; err != nil {
		return err
	}
	for i := range verifications {
		row := &verifications[i]
		if err := validateDeliverySecretEnvelope(
			protector,
			row.DeliverySecret,
			emailVerificationDeliverySecretAAD(row.ID),
		); err != nil {
			return fmt.Errorf("email verification %d delivery secret: %w", row.ID, err)
		}
	}
	var resets []PasswordReset
	if err := db.WithContext(ctx).Select("id", "delivery_secret").
		Find(&resets).Error; err != nil {
		return err
	}
	for i := range resets {
		row := &resets[i]
		if err := validateDeliverySecretEnvelope(
			protector,
			row.DeliverySecret,
			passwordResetDeliverySecretAAD(row.ID),
		); err != nil {
			return fmt.Errorf("password reset %d delivery secret: %w", row.ID, err)
		}
	}
	return nil
}

func validateDeliverySecretEnvelope(
	protector security.Protector,
	envelope string,
	aad []byte,
) error {
	if envelope == "" {
		return nil
	}
	if protector == nil {
		return security.ErrKeyringUnavailable
	}
	plaintext, err := protector.Open(envelope, aad)
	clear(plaintext)
	return err
}

func otpSecretAAD(userID uint) []byte {
	return security.FieldAAD(
		"users",
		strconv.FormatUint(uint64(userID), 10),
		"two_factor_secret",
	)
}
