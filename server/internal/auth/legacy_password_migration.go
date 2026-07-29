package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type UnsupportedPasswordQuarantineReport struct {
	Quarantined       int
	ActiveSuspended   int
	InactiveSanitized int
}

// QuarantineUnsupportedPasswordHashes makes accounts with unverifiable legacy
// password storage non-authenticatable without deleting their business
// records. This is an explicit offline remediation: active accounts are
// suspended, their password is replaced with random bcrypt material, and all
// sessions are revoked. A normal password-reset flow is required to reactivate
// the human identity.
func QuarantineUnsupportedPasswordHashes(
	ctx context.Context,
	db *gorm.DB,
) (UnsupportedPasswordQuarantineReport, error) {
	var report UnsupportedPasswordQuarantineReport
	if ctx == nil || db == nil {
		return report, errors.New("credential migration database is unavailable")
	}

	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var users []models.User
		if err := tx.Unscoped().
			Select("id", "password_hash", "status", "deleted_at").
			Find(&users).Error; err != nil {
			return err
		}
		for i := range users {
			user := &users[i]
			if _, err := bcrypt.Cost([]byte(user.PasswordHash)); err == nil {
				continue
			}

			randomPassword := make([]byte, 48)
			if _, err := rand.Read(randomPassword); err != nil {
				return fmt.Errorf("generate quarantine credential: %w", err)
			}
			replacementHash, err := bcrypt.GenerateFromPassword(
				[]byte(base64.RawURLEncoding.EncodeToString(randomPassword)),
				bcrypt.DefaultCost,
			)
			clear(randomPassword)
			if err != nil {
				return fmt.Errorf("hash quarantine credential: %w", err)
			}

			now := time.Now()
			updates := map[string]any{
				"password_hash":        string(replacementHash),
				"password_reset_at":    now,
				"password_reset_token": "",
			}
			if !user.DeletedAt.Valid && user.Status == models.UserStatusActive {
				updates["status"] = models.UserStatusSuspended
				report.ActiveSuspended++
			} else {
				report.InactiveSanitized++
			}
			result := tx.Unscoped().
				Model(&models.User{}).
				Where("id = ? AND password_hash = ?", user.ID, user.PasswordHash).
				Updates(updates)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("user %d credential changed concurrently", user.ID)
			}
			if err := revokeHumanSessions(tx, user.ID, now); err != nil {
				return err
			}
			report.Quarantined++
		}
		return nil
	})
	if err != nil {
		return UnsupportedPasswordQuarantineReport{}, err
	}
	return report, nil
}

func revokeHumanSessions(tx *gorm.DB, userID uint, now time.Time) error {
	if err := tx.Model(&RefreshToken{}).
		Where("user_id = ? AND revoked = ?", userID, false).
		Updates(map[string]any{
			"revoked":    true,
			"revoked_at": now,
		}).Error; err != nil {
		return fmt.Errorf("revoke migrated user sessions: %w", err)
	}
	if err := tx.Model(&models.LoginHistory{}).
		Where("user_id = ? AND is_active = ?", userID, true).
		Updates(map[string]any{
			"is_active":   false,
			"logout_time": now,
		}).Error; err != nil {
		return fmt.Errorf("close migrated user sessions: %w", err)
	}
	return nil
}
