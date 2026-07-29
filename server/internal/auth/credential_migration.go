package auth

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/security"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type CredentialMigrationReport struct {
	EncryptedOTPSecrets int
	HashedBackupCodes   int
	ClearedDisabledOTP  int
}

// MigrateLegacyAuthCredentials is an explicit, one-time migration. Runtime
// repositories intentionally reject plaintext credentials and never invoke it.
func MigrateLegacyAuthCredentials(
	ctx context.Context,
	db *gorm.DB,
	protector security.Protector,
) (CredentialMigrationReport, error) {
	var report CredentialMigrationReport
	if db == nil || protector == nil {
		return report, security.ErrKeyringUnavailable
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var users []models.User
		if err := tx.Unscoped().Select(
			"id",
			"two_factor_enabled",
			"two_factor_secret",
			"backup_codes",
		).Find(&users).Error; err != nil {
			return err
		}
		for i := range users {
			user := &users[i]
			updates := map[string]interface{}{}
			if !user.TwoFactorEnabled {
				if user.TwoFactorSecret != "" || user.BackupCodes != "" {
					updates["two_factor_secret"] = ""
					updates["backup_codes"] = ""
					report.ClearedDisabledOTP++
				}
			} else {
				if strings.TrimSpace(user.TwoFactorSecret) == "" {
					return fmt.Errorf("user %d has enabled OTP without a secret", user.ID)
				}
				aad := otpSecretAAD(user.ID)
				switch {
				case security.IsEnvelope(user.TwoFactorSecret):
					plaintext, err := protector.Open(user.TwoFactorSecret, aad)
					if err != nil {
						return fmt.Errorf("validate OTP secret for user %d: %w", user.ID, err)
					}
					clear(plaintext)
				case strings.HasPrefix(user.TwoFactorSecret, "cdsec:"):
					return fmt.Errorf(
						"OTP secret for user %d has an invalid encrypted envelope",
						user.ID,
					)
				default:
					envelope, err := protector.Seal([]byte(user.TwoFactorSecret), aad)
					if err != nil {
						return fmt.Errorf("protect OTP secret for user %d: %w", user.ID, err)
					}
					updates["two_factor_secret"] = envelope
					report.EncryptedOTPSecrets++
				}

				migratedHashes, changed, err := migrateBackupCodeHashes(user.BackupCodes)
				if err != nil {
					return fmt.Errorf("migrate backup codes for user %d: %w", user.ID, err)
				}
				if changed {
					updates["backup_codes"] = migratedHashes
					report.HashedBackupCodes++
				}
			}
			if len(updates) > 0 {
				result := tx.Unscoped().Model(&models.User{}).Where("id = ?", user.ID).Updates(updates)
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return fmt.Errorf("user %d credential migration lost its update", user.ID)
				}
			}
		}
		return nil
	})
	if err != nil {
		return CredentialMigrationReport{}, err
	}
	return report, nil
}

// ValidateAuthCredentialStorage is intended as a startup gate after the
// explicit migration. It rejects legacy password hashes, plaintext TOTP
// secrets, malformed ciphertext, and plaintext backup codes.
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
	return nil
}

func migrateBackupCodeHashes(serialized string) (string, bool, error) {
	serialized = strings.TrimSpace(serialized)
	if serialized == "" {
		return "", false, nil
	}
	codes := strings.Split(serialized, ",")
	hashes := make([]string, 0, len(codes))
	changed := false
	for _, rawCode := range codes {
		code := strings.TrimSpace(rawCode)
		if code == "" {
			return "", false, ErrInvalidBackupCodeStorage
		}
		if _, err := bcrypt.Cost([]byte(code)); err == nil {
			hashes = append(hashes, code)
			continue
		}
		if strings.HasPrefix(code, "$2") {
			return "", false, ErrInvalidBackupCodeStorage
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
		if err != nil {
			return "", false, fmt.Errorf("hash legacy backup code: %w", err)
		}
		hashes = append(hashes, string(hash))
		changed = true
	}
	return strings.Join(hashes, ","), changed, nil
}

func otpSecretAAD(userID uint) []byte {
	return security.FieldAAD(
		"users",
		strconv.FormatUint(uint64(userID), 10),
		"two_factor_secret",
	)
}
