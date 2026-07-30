package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const (
	webhookSecretsTable = "webhook_configs"
	emailSecretsTable   = "email_configs"
	a2aPushSecretsTable = "agent_push_notification_configs"
)

// SecretRotationReport reports only envelope counts and never secret values.
type SecretRotationReport struct {
	Rotated  int
	Verified int
}

// ValidateDatabaseSecrets validates through a privileged maintenance handle.
// Runtime startup must use ValidateRuntimeDatabaseSecrets so FORCE RLS cannot
// turn a project-owned table scan into a misleading empty result.
func ValidateDatabaseSecrets(ctx context.Context, db *gorm.DB, protector Protector) error {
	if err := validateDatabaseSecretInputs(db, protector); err != nil {
		return err
	}
	if err := validateProjectDatabaseSecrets(ctx, db, protector); err != nil {
		return err
	}
	return validateGlobalDatabaseSecrets(ctx, db, protector)
}

// ValidateRuntimeDatabaseSecrets is the least-privilege startup gate. It
// enumerates the server-owned project inventory, then validates each
// project-owned secret table inside a short RLS transaction. Project IDs come
// exclusively from the database and are never accepted from request data.
func ValidateRuntimeDatabaseSecrets(
	ctx context.Context,
	db *gorm.DB,
	protector Protector,
) error {
	if err := validateDatabaseSecretInputs(db, protector); err != nil {
		return err
	}
	if err := validateGlobalDatabaseSecrets(ctx, db, protector); err != nil {
		return err
	}

	var projects []models.Project
	if err := db.WithContext(ctx).
		Select("id", "organization_id").
		Order("organization_id ASC, id ASC").
		Find(&projects).Error; err != nil {
		return fmt.Errorf("list projects for secret validation: %w", err)
	}
	for _, project := range projects {
		scope := project.Scope()
		if err := scopeddb.WithProjectScopeTransaction(
			ctx,
			db,
			scope,
			func(tx *gorm.DB) error {
				return validateProjectDatabaseSecrets(ctx, tx, protector)
			},
		); err != nil {
			return fmt.Errorf(
				"validate project %d database secrets: %w",
				project.ID,
				err,
			)
		}
	}
	return nil
}

func validateDatabaseSecretInputs(db *gorm.DB, protector Protector) error {
	if db == nil {
		return errors.New("secret validation database is required")
	}
	if protector == nil {
		return ErrKeyringUnavailable
	}
	return nil
}

func validateProjectDatabaseSecrets(
	ctx context.Context,
	db *gorm.DB,
	protector Protector,
) error {
	var webhooks []models.WebhookConfig
	if err := db.WithContext(ctx).Unscoped().
		Select("id", "secret", "previous_secret", "access_token").
		Find(&webhooks).Error; err != nil {
		return fmt.Errorf("validate webhook secrets: %w", err)
	}
	for _, row := range webhooks {
		rowID := strconv.FormatUint(uint64(row.ID), 10)
		if err := validateEnvelope(protector, row.Secret, FieldAAD(webhookSecretsTable, rowID, "secret")); err != nil {
			return fmt.Errorf("webhook %d secret: %w", row.ID, err)
		}
		if err := validateEnvelope(
			protector,
			row.PreviousSecret,
			FieldAAD(webhookSecretsTable, rowID, "previous_secret"),
		); err != nil {
			return fmt.Errorf("webhook %d previous secret: %w", row.ID, err)
		}
		if err := validateEnvelope(protector, row.AccessToken, FieldAAD(webhookSecretsTable, rowID, "access_token")); err != nil {
			return fmt.Errorf("webhook %d access token: %w", row.ID, err)
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

func validateGlobalDatabaseSecrets(
	ctx context.Context,
	db *gorm.DB,
	protector Protector,
) error {
	var emails []models.EmailConfig
	if err := db.WithContext(ctx).
		Select("id", "smtp_password").
		Find(&emails).Error; err != nil {
		return fmt.Errorf("validate SMTP secrets: %w", err)
	}
	for _, row := range emails {
		rowID := strconv.FormatUint(uint64(row.ID), 10)
		if err := validateEnvelope(
			protector,
			row.SMTPPassword,
			FieldAAD(emailSecretsTable, rowID, "smtp_password"),
		); err != nil {
			return fmt.Errorf(
				"email config %d SMTP password: %w",
				row.ID,
				err,
			)
		}
	}
	return nil
}

// RotateDatabaseSecrets is an explicit operator action. It authenticates every
// non-empty current-format envelope and rewraps values that use a non-primary
// key. Plaintext and malformed envelopes fail closed and are never rewritten.
func RotateDatabaseSecrets(
	ctx context.Context,
	db *gorm.DB,
	protector Protector,
) (SecretRotationReport, error) {
	var report SecretRotationReport
	if db == nil {
		return report, errors.New("secret rotation database is required")
	}
	if protector == nil {
		return report, ErrKeyringUnavailable
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var webhooks []models.WebhookConfig
		if err := tx.Unscoped().
			Select("id", "secret", "previous_secret", "access_token").
			Find(&webhooks).Error; err != nil {
			return err
		}
		for _, row := range webhooks {
			rowID := strconv.FormatUint(uint64(row.ID), 10)
			updates := map[string]any{}
			if value, changed, err := rotateValue(protector, row.Secret, FieldAAD(webhookSecretsTable, rowID, "secret")); err != nil {
				return fmt.Errorf("rotate webhook %d secret: %w", row.ID, err)
			} else if changed {
				updates["secret"] = value
				report.Rotated++
			} else if row.Secret != "" {
				report.Verified++
			}
			if value, changed, err := rotateValue(
				protector,
				row.PreviousSecret,
				FieldAAD(webhookSecretsTable, rowID, "previous_secret"),
			); err != nil {
				return fmt.Errorf(
					"rotate webhook %d previous secret: %w",
					row.ID,
					err,
				)
			} else if changed {
				updates["previous_secret"] = value
				report.Rotated++
			} else if row.PreviousSecret != "" {
				report.Verified++
			}
			if value, changed, err := rotateValue(protector, row.AccessToken, FieldAAD(webhookSecretsTable, rowID, "access_token")); err != nil {
				return fmt.Errorf("rotate webhook %d access token: %w", row.ID, err)
			} else if changed {
				updates["access_token"] = value
				report.Rotated++
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
			value, changed, err := rotateValue(protector, row.SMTPPassword, FieldAAD(emailSecretsTable, rowID, "smtp_password"))
			if err != nil {
				return fmt.Errorf("rotate email config %d SMTP password: %w", row.ID, err)
			}
			if changed {
				if err := tx.Model(&models.EmailConfig{}).Where("id = ?", row.ID).
					UpdateColumn("smtp_password", value).Error; err != nil {
					return err
				}
				report.Rotated++
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
			if value, changed, err := rotateValue(protector, row.Token, FieldAAD(a2aPushSecretsTable, row.ID, "token")); err != nil {
				return fmt.Errorf("rotate A2A push config %q token: %w", row.ID, err)
			} else if changed {
				updates["token"] = value
				report.Rotated++
			} else if row.Token != "" {
				report.Verified++
			}

			storedAuthentication, err := storedJSONEnvelope(row.Authentication)
			if err != nil {
				return fmt.Errorf("rotate A2A push config %q authentication: %w", row.ID, err)
			}
			authenticationEnvelope, authenticationChanged, err := rotateValue(
				protector,
				storedAuthentication,
				FieldAAD(a2aPushSecretsTable, row.ID, "authentication"),
			)
			if err != nil {
				return fmt.Errorf("rotate A2A push config %q authentication: %w", row.ID, err)
			}
			if authenticationChanged {
				encoded, err := json.Marshal(authenticationEnvelope)
				if err != nil {
					return err
				}
				updates["authentication"] = datatypes.JSON(encoded)
				report.Rotated++
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
	if err != nil {
		return SecretRotationReport{}, err
	}
	return report, nil
}

func validateEnvelope(protector Protector, value string, aad []byte) error {
	if value == "" {
		return nil
	}
	plaintext, err := protector.Open(value, aad)
	clear(plaintext)
	return err
}

func rotateValue(protector Protector, value string, aad []byte) (string, bool, error) {
	if value == "" {
		return "", false, nil
	}
	if !IsEnvelope(value) {
		if strings.HasPrefix(value, "cdsec:") {
			return "", false, ErrInvalidEnvelope
		}
		return "", false, ErrPlaintextSecret
	}
	plaintext, err := protector.Open(value, aad)
	if err != nil {
		return "", false, err
	}
	defer clear(plaintext)
	keyID, err := EnvelopeKeyID(value)
	if err != nil {
		return "", false, err
	}
	if keyID == protector.PrimaryKeyID() {
		return value, false, nil
	}
	envelope, err := protector.Seal(plaintext, aad)
	return envelope, err == nil, err
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
		if strings.HasPrefix(envelope, "cdsec:") {
			return "", ErrInvalidEnvelope
		}
		return "", ErrPlaintextSecret
	}
	return envelope, nil
}
