package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gongdan-system/internal/models"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const (
	webhookSecretsTable = "webhook_configs"
	emailSecretsTable   = "email_configs"
	a2aPushSecretsTable = "agent_push_notification_configs"
)

// LegacySecretMigrationReport reports only counts and never secret values.
type LegacySecretMigrationReport struct {
	Encrypted int
	Rotated   int
	Verified  int
}

// ValidateDatabaseSecrets is the startup fail-fast gate. Any non-empty
// sensitive column must contain an authenticated envelope decryptable by the
// configured keyring. Existing plaintext is never treated as a usable value.
func ValidateDatabaseSecrets(ctx context.Context, db *gorm.DB, protector Protector) error {
	if db == nil {
		return errors.New("secret validation database is required")
	}
	if protector == nil {
		return ErrKeyringUnavailable
	}

	var webhooks []models.WebhookConfig
	if err := db.WithContext(ctx).Unscoped().
		Select("id", "secret", "access_token").
		Find(&webhooks).Error; err != nil {
		return fmt.Errorf("validate webhook secrets: %w", err)
	}
	for _, row := range webhooks {
		rowID := strconv.FormatUint(uint64(row.ID), 10)
		if err := validateEnvelope(protector, row.Secret, FieldAAD(webhookSecretsTable, rowID, "secret")); err != nil {
			return fmt.Errorf("webhook %d secret: %w", row.ID, err)
		}
		if err := validateEnvelope(protector, row.AccessToken, FieldAAD(webhookSecretsTable, rowID, "access_token")); err != nil {
			return fmt.Errorf("webhook %d access token: %w", row.ID, err)
		}
	}

	var emails []models.EmailConfig
	if err := db.WithContext(ctx).
		Select("id", "smtp_password").
		Find(&emails).Error; err != nil {
		return fmt.Errorf("validate SMTP secrets: %w", err)
	}
	for _, row := range emails {
		rowID := strconv.FormatUint(uint64(row.ID), 10)
		if err := validateEnvelope(protector, row.SMTPPassword, FieldAAD(emailSecretsTable, rowID, "smtp_password")); err != nil {
			return fmt.Errorf("email config %d SMTP password: %w", row.ID, err)
		}
	}

	var pushes []models.AgentPushNotificationConfig
	if err := db.WithContext(ctx).
		Select("id", "token", "authentication").
		Find(&pushes).Error; err != nil {
		return fmt.Errorf("validate A2A push secrets: %w", err)
	}
	for _, row := range pushes {
		if err := validateEnvelope(protector, row.Token, FieldAAD(a2aPushSecretsTable, row.ID, "token")); err != nil {
			return fmt.Errorf("A2A push config %q token: %w", row.ID, err)
		}
		authentication, err := storedJSONEnvelope(row.Authentication)
		if err != nil {
			return fmt.Errorf("A2A push config %q authentication: %w", row.ID, err)
		}
		if err := validateEnvelope(protector, authentication, FieldAAD(a2aPushSecretsTable, row.ID, "authentication")); err != nil {
			return fmt.Errorf("A2A push config %q authentication: %w", row.ID, err)
		}
	}
	return nil
}

// MigrateLegacyDatabaseSecrets is an explicit operator action. It encrypts
// historical plaintext and rewraps envelopes that use a non-primary key.
// Normal application startup must call ValidateDatabaseSecrets, not this
// function, so an unexpected plaintext regression cannot be silently accepted.
func MigrateLegacyDatabaseSecrets(
	ctx context.Context,
	db *gorm.DB,
	protector Protector,
) (LegacySecretMigrationReport, error) {
	var report LegacySecretMigrationReport
	if db == nil {
		return report, errors.New("secret migration database is required")
	}
	if protector == nil {
		return report, ErrKeyringUnavailable
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var webhooks []models.WebhookConfig
		if err := tx.Unscoped().Select("id", "secret", "access_token").Find(&webhooks).Error; err != nil {
			return err
		}
		for _, row := range webhooks {
			rowID := strconv.FormatUint(uint64(row.ID), 10)
			updates := map[string]any{}
			if value, status, err := migrateValue(protector, row.Secret, FieldAAD(webhookSecretsTable, rowID, "secret")); err != nil {
				return fmt.Errorf("migrate webhook %d secret: %w", row.ID, err)
			} else if status != migrationUnchanged {
				updates["secret"] = value
				report.add(status)
			} else if row.Secret != "" {
				report.Verified++
			}
			if value, status, err := migrateValue(protector, row.AccessToken, FieldAAD(webhookSecretsTable, rowID, "access_token")); err != nil {
				return fmt.Errorf("migrate webhook %d access token: %w", row.ID, err)
			} else if status != migrationUnchanged {
				updates["access_token"] = value
				report.add(status)
			} else if row.AccessToken != "" {
				report.Verified++
			}
			if len(updates) > 0 {
				if err := tx.Unscoped().Model(&models.WebhookConfig{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
					return err
				}
			}
		}

		var emails []models.EmailConfig
		if err := tx.Select("id", "smtp_password").Find(&emails).Error; err != nil {
			return err
		}
		for _, row := range emails {
			rowID := strconv.FormatUint(uint64(row.ID), 10)
			value, status, err := migrateValue(protector, row.SMTPPassword, FieldAAD(emailSecretsTable, rowID, "smtp_password"))
			if err != nil {
				return fmt.Errorf("migrate email config %d SMTP password: %w", row.ID, err)
			}
			if status != migrationUnchanged {
				if err := tx.Model(&models.EmailConfig{}).Where("id = ?", row.ID).
					UpdateColumn("smtp_password", value).Error; err != nil {
					return err
				}
				report.add(status)
			} else if row.SMTPPassword != "" {
				report.Verified++
			}
		}

		var pushes []models.AgentPushNotificationConfig
		if err := tx.Select("id", "token", "authentication").Find(&pushes).Error; err != nil {
			return err
		}
		for _, row := range pushes {
			updates := map[string]any{}
			if value, status, err := migrateValue(protector, row.Token, FieldAAD(a2aPushSecretsTable, row.ID, "token")); err != nil {
				return fmt.Errorf("migrate A2A push config %q token: %w", row.ID, err)
			} else if status != migrationUnchanged {
				updates["token"] = value
				report.add(status)
			} else if row.Token != "" {
				report.Verified++
			}

			storedAuthentication, legacyAuthentication, err := storedSecretForMigration(row.Authentication)
			if err != nil {
				return fmt.Errorf("migrate A2A push config %q authentication: %w", row.ID, err)
			}
			var authenticationStatus migrationStatus
			var authenticationEnvelope string
			if legacyAuthentication {
				authenticationEnvelope, err = ProtectOptional(
					protector,
					storedAuthentication,
					FieldAAD(a2aPushSecretsTable, row.ID, "authentication"),
				)
				authenticationStatus = migrationEncrypted
			} else {
				authenticationEnvelope, authenticationStatus, err = migrateValue(
					protector,
					storedAuthentication,
					FieldAAD(a2aPushSecretsTable, row.ID, "authentication"),
				)
			}
			if err != nil {
				return fmt.Errorf("migrate A2A push config %q authentication: %w", row.ID, err)
			}
			if authenticationStatus != migrationUnchanged {
				encoded, err := json.Marshal(authenticationEnvelope)
				if err != nil {
					return err
				}
				updates["authentication"] = datatypes.JSON(encoded)
				report.add(authenticationStatus)
			} else if storedAuthentication != "" {
				report.Verified++
			}
			if len(updates) > 0 {
				if err := tx.Model(&models.AgentPushNotificationConfig{}).
					Where("id = ?", row.ID).Updates(updates).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	return report, err
}

type migrationStatus uint8

const (
	migrationUnchanged migrationStatus = iota
	migrationEncrypted
	migrationRotated
)

func (r *LegacySecretMigrationReport) add(status migrationStatus) {
	switch status {
	case migrationEncrypted:
		r.Encrypted++
	case migrationRotated:
		r.Rotated++
	}
}

func validateEnvelope(protector Protector, value string, aad []byte) error {
	if value == "" {
		return nil
	}
	plaintext, err := protector.Open(value, aad)
	clear(plaintext)
	return err
}

func migrateValue(protector Protector, value string, aad []byte) (string, migrationStatus, error) {
	if value == "" {
		return "", migrationUnchanged, nil
	}
	if !IsEnvelope(value) {
		if strings.HasPrefix(value, "cdsec:") {
			return "", migrationUnchanged, ErrInvalidEnvelope
		}
		envelope, err := ProtectOptional(protector, value, aad)
		return envelope, migrationEncrypted, err
	}
	plaintext, err := protector.Open(value, aad)
	if err != nil {
		return "", migrationUnchanged, err
	}
	defer clear(plaintext)
	keyID, err := EnvelopeKeyID(value)
	if err != nil {
		return "", migrationUnchanged, err
	}
	if keyID == protector.PrimaryKeyID() {
		return value, migrationUnchanged, nil
	}
	envelope, err := protector.Seal(plaintext, aad)
	return envelope, migrationRotated, err
}

func storedJSONEnvelope(raw datatypes.JSON) (string, error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" || string(raw) == "null" {
		return "", nil
	}
	var envelope string
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", ErrPlaintextSecret
	}
	if envelope != "" && !IsEnvelope(envelope) {
		return "", ErrPlaintextSecret
	}
	return envelope, nil
}

func storedSecretForMigration(raw datatypes.JSON) (value string, legacy bool, err error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" || string(raw) == "null" {
		return "", false, nil
	}
	var envelope string
	if json.Unmarshal(raw, &envelope) == nil {
		if IsEnvelope(envelope) {
			return envelope, false, nil
		}
		return "", false, ErrInvalidEnvelope
	}
	if !json.Valid(raw) {
		return "", false, ErrInvalidEnvelope
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil || len(object) == 0 {
		return "", false, ErrInvalidEnvelope
	}
	return string(raw), true, nil
}
