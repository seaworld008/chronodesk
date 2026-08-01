package database

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

const (
	adminAuditActorTypeConstraint   = "chk_admin_audit_logs_actor_type"
	adminAuditActorRoleConstraint   = "chk_admin_audit_logs_actor_role"
	adminAuditExportStateConstraint = "chk_admin_audit_export_jobs_state"
	adminAuditExportCountConstraint = "chk_admin_audit_export_jobs_counts"
	adminAuditExportIDConstraint    = "chk_admin_audit_export_jobs_public_id"
	adminAuditExportUUIDv7Pattern   = "^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"
)

// PrepareAdminAuditActorColumns runs before the canonical model migration.
// Existing non-empty audit tables cannot accept new NOT NULL ActorRef columns
// directly, so the migration first adds nullable columns and deterministically
// backfills every historical row as a human actor. It preserves the historical
// platform_role value and never invents a role for a system actor.
func PrepareAdminAuditActorColumns(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if !db.Migrator().HasTable(&models.AdminAuditLog{}) {
		return nil
	}
	for _, column := range []struct {
		name       string
		definition string
	}{
		{"actor_type", "VARCHAR(32)"},
		{"actor_id", "VARCHAR(128)"},
	} {
		exists, err := hasExactDatabaseColumn(
			db,
			"admin_audit_logs",
			column.name,
		)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if err := db.Exec(
			"ALTER TABLE admin_audit_logs ADD COLUMN " +
				column.name + " " + column.definition,
		).Error; err != nil {
			return fmt.Errorf(
				"add admin audit %s: %w",
				column.name,
				err,
			)
		}
	}
	actorIDExpression := "CAST(id AS TEXT)"
	if db.Dialector.Name() == "postgres" {
		actorIDExpression = "id::text"
	}
	if err := db.Exec(`
		UPDATE admin_audit_logs
		SET
			actor_type = COALESCE(NULLIF(actor_type, ''), 'human'),
			actor_id = COALESCE(
				NULLIF(actor_id, ''),
				CASE
					WHEN user_id IS NOT NULL THEN CAST(user_id AS TEXT)
					ELSE 'legacy-audit:' || ` + actorIDExpression + `
				END
			)
		WHERE actor_type IS NULL OR actor_type = ''
		   OR actor_id IS NULL OR actor_id = ''
	`).Error; err != nil {
		return fmt.Errorf("backfill admin audit actors: %w", err)
	}
	return nil
}

// MigrateAdminAuditExportContract installs the ActorRef contract and durable
// export-job storage after the canonical model batch has run.
func MigrateAdminAuditExportContract(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if err := db.AutoMigrate(&models.AdminAuditExportJob{}); err != nil {
		return fmt.Errorf("migrate admin audit export jobs: %w", err)
	}
	if err := PrepareAdminAuditActorColumns(db); err != nil {
		return err
	}
	// The platform-role cutover deliberately remains the final durable write in
	// the outer migration transaction. A legacy audit table can therefore still
	// carry role while AutoMigrate has already added nullable platform_role.
	// Populate that authoritative human projection before installing the
	// ActorRef constraint; migrateLegacyAdminAuditRoles will later re-verify the
	// same mapping and remove role atomically with the cutover checkpoint.
	if err := backfillLegacyAdminAuditPlatformRoles(db); err != nil {
		return fmt.Errorf("backfill legacy admin audit platform roles: %w", err)
	}
	if db.Dialector.Name() == "postgres" {
		if err := installPostgresAdminAuditActorContract(db); err != nil {
			return err
		}
		if err := installPostgresAdminAuditExportConstraints(db); err != nil {
			return err
		}
	}
	for _, statement := range []string{
		"CREATE INDEX IF NOT EXISTS idx_admin_audit_actor_created_id ON admin_audit_logs(actor_type, actor_id, created_at DESC, id DESC)",
		"CREATE INDEX IF NOT EXISTS idx_admin_audit_exports_claim ON admin_audit_export_jobs(state, requested_at ASC, id ASC)",
		"CREATE INDEX IF NOT EXISTS idx_admin_audit_exports_lease ON admin_audit_export_jobs(state, lease_expires_at ASC, id ASC)",
		"CREATE INDEX IF NOT EXISTS idx_admin_audit_exports_owner ON admin_audit_export_jobs(requester_user_id, created_at DESC, id DESC)",
		"CREATE INDEX IF NOT EXISTS idx_admin_audit_exports_expiry ON admin_audit_export_jobs(expires_at ASC, id ASC)",
	} {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("create admin audit export index: %w", err)
		}
	}
	return nil
}

func installPostgresAdminAuditActorContract(db *gorm.DB) error {
	statements := []string{
		"ALTER TABLE admin_audit_logs ALTER COLUMN actor_type SET NOT NULL",
		"ALTER TABLE admin_audit_logs ALTER COLUMN actor_id SET NOT NULL",
		"ALTER TABLE admin_audit_logs ALTER COLUMN platform_role DROP DEFAULT",
		"ALTER TABLE admin_audit_logs ALTER COLUMN platform_role DROP NOT NULL",
		"ALTER TABLE admin_audit_logs DROP CONSTRAINT IF EXISTS chk_admin_audit_logs_platform_role",
		"ALTER TABLE admin_audit_logs ADD CONSTRAINT chk_admin_audit_logs_platform_role CHECK (platform_role IS NULL OR platform_role IN ('platform_admin','security_auditor','emergency_operator','member'))",
		"ALTER TABLE admin_audit_logs DROP CONSTRAINT IF EXISTS " +
			adminAuditActorTypeConstraint,
		"ALTER TABLE admin_audit_logs ADD CONSTRAINT " +
			adminAuditActorTypeConstraint +
			" CHECK (actor_type IN ('human','service_principal','system') AND btrim(actor_id) <> '')",
		"ALTER TABLE admin_audit_logs DROP CONSTRAINT IF EXISTS " +
			adminAuditActorRoleConstraint,
		"ALTER TABLE admin_audit_logs ADD CONSTRAINT " +
			adminAuditActorRoleConstraint +
			" CHECK ((actor_type = 'human' AND platform_role IS NOT NULL) OR (actor_type IN ('service_principal','system') AND platform_role IS NULL AND user_id IS NULL))",
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("install admin audit actor contract: %w", err)
		}
	}
	return nil
}

func installPostgresAdminAuditExportConstraints(db *gorm.DB) error {
	statements := []string{
		"ALTER TABLE admin_audit_export_jobs DROP CONSTRAINT IF EXISTS " +
			adminAuditExportStateConstraint,
		"ALTER TABLE admin_audit_export_jobs ADD CONSTRAINT " +
			adminAuditExportStateConstraint +
			" CHECK (state IN ('queued','processing','completed','failed','expired'))",
		"ALTER TABLE admin_audit_export_jobs DROP CONSTRAINT IF EXISTS " +
			adminAuditExportCountConstraint,
		"ALTER TABLE admin_audit_export_jobs ADD CONSTRAINT " +
			adminAuditExportCountConstraint +
			" CHECK (row_count BETWEEN 0 AND 100000 AND size_bytes >= 0 AND attempt <= 1000000)",
		"ALTER TABLE admin_audit_export_jobs DROP CONSTRAINT IF EXISTS " +
			adminAuditExportIDConstraint,
		"ALTER TABLE admin_audit_export_jobs ADD CONSTRAINT " +
			adminAuditExportIDConstraint +
			" CHECK (public_id ~ '" +
			adminAuditExportUUIDv7Pattern +
			"')",
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf(
				"install admin audit export constraint %q: %w",
				strings.Fields(statement)[0],
				err,
			)
		}
	}
	return nil
}

// ValidateAdminAuditExportContract prevents a process from starting against a
// database where public export identifiers can drift from the Human OpenAPI
// UUIDv7 contract. The model hook protects ORM writes; this gate also protects
// direct SQL and old deployments with a missing or unvalidated constraint.
func ValidateAdminAuditExportContract(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	var contract struct {
		Definition string `gorm:"column:definition"`
		Validated  bool   `gorm:"column:validated"`
	}
	if err := db.Raw(`
		SELECT
			pg_get_constraintdef(constraint_row.oid) AS definition,
			constraint_row.convalidated AS validated
		FROM pg_constraint AS constraint_row
		JOIN pg_class AS table_row
		  ON table_row.oid = constraint_row.conrelid
		JOIN pg_namespace AS namespace_row
		  ON namespace_row.oid = table_row.relnamespace
		WHERE namespace_row.nspname = CURRENT_SCHEMA()
		  AND table_row.relname = 'admin_audit_export_jobs'
		  AND constraint_row.conname = ?
		  AND constraint_row.contype = 'c'
	`, adminAuditExportIDConstraint).Scan(&contract).Error; err != nil {
		return fmt.Errorf("read admin audit export public id contract: %w", err)
	}
	if !contract.Validated ||
		!isExactAdminAuditExportUUIDv7Check(contract.Definition) {
		return fmt.Errorf(
			"admin audit export public IDs must be protected by the canonical UUIDv7 check; run `go run ./cmd/migrate`",
		)
	}
	return nil
}

func isExactAdminAuditExportUUIDv7Check(definition string) bool {
	withoutCasts := postgresTextCastPattern.ReplaceAllString(
		definition,
		"",
	)
	canonical := strings.ToLower(withoutCasts)
	canonical = strings.Map(func(character rune) rune {
		switch character {
		case ' ', '\t', '\r', '\n', '(', ')', '"':
			return -1
		default:
			return character
		}
	}, canonical)
	literalPattern := regexp.MustCompile(`'([^']*)'`)
	matches := literalPattern.FindAllStringSubmatch(canonical, -1)
	if len(matches) != 1 || matches[0][1] != adminAuditExportUUIDv7Pattern {
		return false
	}
	return literalPattern.ReplaceAllString(canonical, "?") ==
		"checkpublic_id~?"
}
