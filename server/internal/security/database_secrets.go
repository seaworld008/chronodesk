package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	maintenanceNow := time.Now().UTC()
	if err := validateDatabaseSecretInputs(db, protector); err != nil {
		return err
	}
	return validateDatabaseSecretsAt(ctx, db, protector, maintenanceNow)
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
	maintenanceNow := time.Now().UTC()
	if err := validateDatabaseSecretInputs(db, protector); err != nil {
		return err
	}
	return validateDatabaseSecretsAt(ctx, db, protector, maintenanceNow)
}

func validateDatabaseSecretsAt(
	ctx context.Context,
	db *gorm.DB,
	protector Protector,
	maintenanceNow time.Time,
) error {
	if err := validateGlobalDatabaseSecrets(ctx, db, protector); err != nil {
		return err
	}

	projects, err := listSecretMaintenanceProjects(ctx, db)
	if err != nil {
		return err
	}
	for _, project := range projects {
		scope := project.Scope()
		if err := scopeddb.WithProjectScopeTransaction(
			ctx,
			db,
			scope,
			func(tx *gorm.DB) error {
				return validateProjectDatabaseSecretsAt(
					ctx,
					tx,
					protector,
					scope,
					maintenanceNow,
				)
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

func listSecretMaintenanceProjects(
	ctx context.Context,
	db *gorm.DB,
) ([]models.Project, error) {
	var projects []models.Project
	if err := db.WithContext(ctx).
		Select("id", "organization_id").
		Order("organization_id ASC, id ASC").
		Find(&projects).Error; err != nil {
		return nil, fmt.Errorf("list projects for secret maintenance: %w", err)
	}
	return projects, nil
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

func validateProjectDatabaseSecretsAt(
	ctx context.Context,
	db *gorm.DB,
	protector Protector,
	scope models.ProjectScope,
	maintenanceNow time.Time,
) error {
	var webhooks []models.WebhookConfig
	if err := db.WithContext(ctx).Unscoped().
		Select("id", "secret", "previous_secret", "access_token").
		Where(
			"organization_id = ? AND project_id = ?",
			scope.OrganizationID,
			scope.ProjectID,
		).
		Order("id ASC").
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

	var snapshots []models.WebhookDeliverySnapshot
	if err := db.WithContext(ctx).
		Select(
			"id",
			"config_id",
			"secret",
			"previous_secret",
			"access_token",
			"credential_expires_at",
			"credential_shredded_at",
			"credential_shred_reason",
		).
		Where(
			"organization_id = ? AND project_id = ? AND credential_shredded_at IS NULL",
			scope.OrganizationID,
			scope.ProjectID,
		).
		Order("id ASC").
		Find(&snapshots).Error; err != nil {
		return fmt.Errorf("validate webhook snapshot secrets: %w", err)
	}
	for _, row := range snapshots {
		if row.CredentialExpiresAt.IsZero() {
			return errors.New(
				"validate webhook snapshot secrets: unshredded credential lifetime is missing",
			)
		}
		if !row.CredentialExpiresAt.After(maintenanceNow) {
			continue
		}
		rowID := strconv.FormatUint(uint64(row.ConfigID), 10)
		if err := validateEnvelope(
			protector,
			row.Secret,
			FieldAAD(webhookSecretsTable, rowID, "secret"),
		); err != nil {
			return fmt.Errorf("webhook snapshot %q secret: %w", row.ID, err)
		}
		if err := validateEnvelope(
			protector,
			row.PreviousSecret,
			FieldAAD(webhookSecretsTable, rowID, "previous_secret"),
		); err != nil {
			return fmt.Errorf(
				"webhook snapshot %q previous secret: %w",
				row.ID,
				err,
			)
		}
		if err := validateEnvelope(
			protector,
			row.AccessToken,
			FieldAAD(webhookSecretsTable, rowID, "access_token"),
		); err != nil {
			return fmt.Errorf(
				"webhook snapshot %q access token: %w",
				row.ID,
				err,
			)
		}
	}

	var pushes []models.AgentPushNotificationConfig
	if err := db.WithContext(ctx).
		Select("id", "token", "authentication").
		Where(
			"organization_id = ? AND project_id = ?",
			scope.OrganizationID,
			scope.ProjectID,
		).
		Order("id ASC").
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
		Order("id ASC").
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
	maintenanceNow := time.Now().UTC()
	if db == nil {
		return SecretRotationReport{}, errors.New("secret rotation database is required")
	}
	if protector == nil {
		return SecretRotationReport{}, ErrKeyringUnavailable
	}
	return rotateDatabaseSecretsAt(ctx, db, protector, maintenanceNow)
}

func rotateDatabaseSecretsAt(
	ctx context.Context,
	db *gorm.DB,
	protector Protector,
	maintenanceNow time.Time,
) (SecretRotationReport, error) {
	var report SecretRotationReport
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		projects, err := listSecretMaintenanceProjects(ctx, tx)
		if err != nil {
			return err
		}
		for _, project := range projects {
			if err := lockSecretMaintenanceProject(tx, project.Scope()); err != nil {
				return err
			}
			if err := scopeddb.ConfigureProjectScopeTransaction(
				tx,
				project.Scope(),
			); err != nil {
				return fmt.Errorf(
					"configure project %d secret rotation scope: %w",
					project.ID,
					err,
				)
			}
			projectReport, err := rotateProjectDatabaseSecretsAt(
				tx,
				protector,
				project.Scope(),
				maintenanceNow,
			)
			if err != nil {
				return fmt.Errorf(
					"rotate project %d database secrets: %w",
					project.ID,
					err,
				)
			}
			report.add(projectReport)
		}

		globalReport, err := rotateGlobalDatabaseSecrets(tx, protector)
		if err != nil {
			return err
		}
		report.add(globalReport)
		return nil
	})
	if err != nil {
		return SecretRotationReport{}, err
	}
	return report, nil
}

func (report *SecretRotationReport) add(delta SecretRotationReport) {
	report.Rotated += delta.Rotated
	report.Verified += delta.Verified
}

func lockSecretMaintenanceProject(
	tx *gorm.DB,
	scope models.ProjectScope,
) error {
	var project models.Project
	query := tx.Select("id", "organization_id").
		Where("id = ? AND organization_id = ?", scope.ProjectID, scope.OrganizationID)
	query = withSecretMaintenanceLock(query, "SHARE")
	if err := query.First(&project).Error; err != nil {
		return fmt.Errorf("lock project for secret rotation: %w", err)
	}
	return nil
}

func withSecretMaintenanceLock(db *gorm.DB, strength string) *gorm.DB {
	if db.Dialector.Name() != "postgres" {
		return db
	}
	return db.Clauses(clause.Locking{Strength: strength})
}

func rotateProjectDatabaseSecretsAt(
	tx *gorm.DB,
	protector Protector,
	scope models.ProjectScope,
	maintenanceNow time.Time,
) (SecretRotationReport, error) {
	var report SecretRotationReport
	var webhooks []models.WebhookConfig
	webhookQuery := tx.Unscoped().
		Select(
			"id",
			"organization_id",
			"project_id",
			"secret",
			"previous_secret",
			"access_token",
		).
		Where(
			"organization_id = ? AND project_id = ?",
			scope.OrganizationID,
			scope.ProjectID,
		).
		Order("id ASC")
	if err := withSecretMaintenanceLock(
		webhookQuery,
		"UPDATE",
	).Find(&webhooks).Error; err != nil {
		return report, fmt.Errorf("lock webhook configs: %w", err)
	}
	for _, row := range webhooks {
		delta, err := rotateWebhookConfigRow(tx, protector, row)
		if err != nil {
			return SecretRotationReport{}, err
		}
		report.add(delta)
	}

	var snapshots []models.WebhookDeliverySnapshot
	snapshotQuery := tx.
		Select(
			"id",
			"organization_id",
			"project_id",
			"config_id",
			"secret",
			"previous_secret",
			"access_token",
			"credential_expires_at",
			"credential_shredded_at",
			"credential_shred_reason",
		).
		Where(
			"organization_id = ? AND project_id = ? AND credential_shredded_at IS NULL",
			scope.OrganizationID,
			scope.ProjectID,
		).
		Order("id ASC")
	if err := withSecretMaintenanceLock(
		snapshotQuery,
		"UPDATE",
	).Find(&snapshots).Error; err != nil {
		return SecretRotationReport{}, fmt.Errorf(
			"lock webhook delivery snapshots: %w",
			err,
		)
	}
	for _, row := range snapshots {
		if row.CredentialExpiresAt.IsZero() {
			return SecretRotationReport{}, errors.New(
				"rotate webhook snapshot secrets: unshredded credential lifetime is missing",
			)
		}
		if !row.CredentialExpiresAt.After(maintenanceNow) {
			continue
		}
		delta, _, err := rewrapWebhookSnapshotRow(
			tx,
			protector,
			row,
			maintenanceNow,
		)
		if err != nil {
			return SecretRotationReport{}, err
		}
		report.add(delta)
	}

	var pushes []models.AgentPushNotificationConfig
	pushQuery := tx.Select(
		"id",
		"organization_id",
		"project_id",
		"token",
		"authentication",
	).Where(
		"organization_id = ? AND project_id = ?",
		scope.OrganizationID,
		scope.ProjectID,
	).Order("id ASC")
	if err := withSecretMaintenanceLock(
		pushQuery,
		"UPDATE",
	).Find(&pushes).Error; err != nil {
		return SecretRotationReport{}, fmt.Errorf(
			"lock A2A push configs: %w",
			err,
		)
	}
	for _, row := range pushes {
		delta, err := rotateA2APushRow(tx, protector, row)
		if err != nil {
			return SecretRotationReport{}, err
		}
		report.add(delta)
	}
	return report, nil
}

func rotateWebhookConfigRow(
	tx *gorm.DB,
	protector Protector,
	row models.WebhookConfig,
) (SecretRotationReport, error) {
	rowID := strconv.FormatUint(uint64(row.ID), 10)
	updates := map[string]any{}
	var delta SecretRotationReport
	for _, field := range []struct {
		column string
		value  string
	}{
		{column: "secret", value: row.Secret},
		{column: "previous_secret", value: row.PreviousSecret},
		{column: "access_token", value: row.AccessToken},
	} {
		value, changed, err := rotateValue(
			protector,
			field.value,
			FieldAAD(webhookSecretsTable, rowID, field.column),
		)
		if err != nil {
			return SecretRotationReport{}, fmt.Errorf(
				"rotate webhook %d %s: %w",
				row.ID,
				field.column,
				err,
			)
		}
		if changed {
			updates[field.column] = value
			delta.Rotated++
		} else if field.value != "" {
			delta.Verified++
		}
	}
	if len(updates) == 0 {
		return delta, nil
	}
	result := tx.Unscoped().Model(&models.WebhookConfig{}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? AND secret = ? AND previous_secret = ? AND access_token = ?",
			row.ID,
			row.OrganizationID,
			row.ProjectID,
			row.Secret,
			row.PreviousSecret,
			row.AccessToken,
		).
		Updates(updates)
	if result.Error != nil {
		return SecretRotationReport{}, result.Error
	}
	if result.RowsAffected != 1 {
		return SecretRotationReport{}, errors.New(
			"webhook config changed during secret rotation",
		)
	}
	return delta, nil
}

func rewrapWebhookSnapshotRow(
	tx *gorm.DB,
	protector Protector,
	row models.WebhookDeliverySnapshot,
	maintenanceNow time.Time,
) (SecretRotationReport, bool, error) {
	if row.CredentialExpiresAt.IsZero() {
		return SecretRotationReport{}, false, errors.New(
			"rotate webhook snapshot secrets: unshredded credential lifetime is missing",
		)
	}
	if row.CredentialShreddedAt != nil ||
		!row.CredentialExpiresAt.After(maintenanceNow) {
		return SecretRotationReport{}, true, nil
	}

	rowID := strconv.FormatUint(uint64(row.ConfigID), 10)
	updates := map[string]any{}
	var delta SecretRotationReport
	for _, field := range []struct {
		column string
		value  string
	}{
		{column: "secret", value: row.Secret},
		{column: "previous_secret", value: row.PreviousSecret},
		{column: "access_token", value: row.AccessToken},
	} {
		value, changed, err := rotateValue(
			protector,
			field.value,
			FieldAAD(webhookSecretsTable, rowID, field.column),
		)
		if err != nil {
			return SecretRotationReport{}, false, fmt.Errorf(
				"rotate webhook snapshot %q %s: %w",
				row.ID,
				field.column,
				err,
			)
		}
		if changed {
			updates[field.column] = value
			delta.Rotated++
		} else if field.value != "" {
			delta.Verified++
		}
	}
	if len(updates) == 0 {
		return delta, false, nil
	}

	result := tx.Table("webhook_delivery_snapshots").
		Where(
			"id = ? AND organization_id = ? AND project_id = ? AND config_id = ?",
			row.ID,
			row.OrganizationID,
			row.ProjectID,
			row.ConfigID,
		).
		Where(
			"secret = ? AND previous_secret = ? AND access_token = ?",
			row.Secret,
			row.PreviousSecret,
			row.AccessToken,
		).
		Where(
			"credential_shredded_at IS NULL AND credential_expires_at > ?",
			maintenanceNow,
		).
		Updates(updates)
	if result.Error != nil {
		return SecretRotationReport{}, false, result.Error
	}
	if result.RowsAffected == 1 {
		return delta, false, nil
	}

	var current models.WebhookDeliverySnapshot
	if err := tx.Table("webhook_delivery_snapshots").
		Select(
			"id",
			"organization_id",
			"project_id",
			"config_id",
			"secret",
			"previous_secret",
			"access_token",
			"credential_expires_at",
			"credential_shredded_at",
			"credential_shred_reason",
		).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? AND config_id = ?",
			row.ID,
			row.OrganizationID,
			row.ProjectID,
			row.ConfigID,
		).
		Take(&current).Error; err != nil {
		return SecretRotationReport{}, false, fmt.Errorf(
			"re-read webhook snapshot after rotation conflict: %w",
			err,
		)
	}
	if current.CredentialExpiresAt.IsZero() {
		return SecretRotationReport{}, false, errors.New(
			"rotate webhook snapshot secrets: unshredded credential lifetime is missing",
		)
	}
	if current.CredentialShreddedAt != nil ||
		!current.CredentialExpiresAt.After(maintenanceNow) {
		return SecretRotationReport{}, true, nil
	}
	return SecretRotationReport{}, false, errors.New(
		"live webhook snapshot changed during secret rotation",
	)
}

func rotateA2APushRow(
	tx *gorm.DB,
	protector Protector,
	row models.AgentPushNotificationConfig,
) (SecretRotationReport, error) {
	updates := map[string]any{}
	var delta SecretRotationReport
	if value, changed, err := rotateValue(
		protector,
		row.Token,
		FieldAAD(a2aPushSecretsTable, row.ID, "token"),
	); err != nil {
		return SecretRotationReport{}, fmt.Errorf(
			"rotate A2A push config %q token: %w",
			row.ID,
			err,
		)
	} else if changed {
		updates["token"] = value
		delta.Rotated++
	} else if row.Token != "" {
		delta.Verified++
	}

	storedAuthentication, err := storedJSONEnvelope(row.Authentication)
	if err != nil {
		return SecretRotationReport{}, fmt.Errorf(
			"rotate A2A push config %q authentication: %w",
			row.ID,
			err,
		)
	}
	authenticationEnvelope, authenticationChanged, err := rotateValue(
		protector,
		storedAuthentication,
		FieldAAD(a2aPushSecretsTable, row.ID, "authentication"),
	)
	if err != nil {
		return SecretRotationReport{}, fmt.Errorf(
			"rotate A2A push config %q authentication: %w",
			row.ID,
			err,
		)
	}
	if authenticationChanged {
		encoded, err := json.Marshal(authenticationEnvelope)
		if err != nil {
			return SecretRotationReport{}, err
		}
		updates["authentication"] = datatypes.JSON(encoded)
		delta.Rotated++
	} else if storedAuthentication != "" {
		delta.Verified++
	}
	if len(updates) == 0 {
		return delta, nil
	}
	result := tx.Model(&models.AgentPushNotificationConfig{}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? AND token = ? AND authentication = ?",
			row.ID,
			row.OrganizationID,
			row.ProjectID,
			row.Token,
			row.Authentication,
		).
		Updates(updates)
	if result.Error != nil {
		return SecretRotationReport{}, result.Error
	}
	if result.RowsAffected != 1 {
		return SecretRotationReport{}, errors.New(
			"A2A push config changed during secret rotation",
		)
	}
	return delta, nil
}

func rotateGlobalDatabaseSecrets(
	tx *gorm.DB,
	protector Protector,
) (SecretRotationReport, error) {
	var report SecretRotationReport
	var emails []models.EmailConfig
	emailQuery := tx.Select("id", "smtp_password").Order("id ASC")
	if err := withSecretMaintenanceLock(
		emailQuery,
		"UPDATE",
	).Find(&emails).Error; err != nil {
		return report, fmt.Errorf("lock email configs: %w", err)
	}
	for _, row := range emails {
		rowID := strconv.FormatUint(uint64(row.ID), 10)
		value, changed, err := rotateValue(
			protector,
			row.SMTPPassword,
			FieldAAD(emailSecretsTable, rowID, "smtp_password"),
		)
		if err != nil {
			return SecretRotationReport{}, fmt.Errorf(
				"rotate email config %d SMTP password: %w",
				row.ID,
				err,
			)
		}
		if !changed {
			if row.SMTPPassword != "" {
				report.Verified++
			}
			continue
		}
		result := tx.Model(&models.EmailConfig{}).
			Where("id = ? AND smtp_password = ?", row.ID, row.SMTPPassword).
			UpdateColumn("smtp_password", value)
		if result.Error != nil {
			return SecretRotationReport{}, result.Error
		}
		if result.RowsAffected != 1 {
			return SecretRotationReport{}, errors.New(
				"email config changed during secret rotation",
			)
		}
		report.Rotated++
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
