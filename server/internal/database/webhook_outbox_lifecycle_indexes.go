package database

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

const (
	webhookOutboxLifecycleClaimIndex         = "idx_outbox_lifecycle_claim"
	webhookOutboxLifecycleStaleClaimIndex    = "idx_outbox_lifecycle_stale_claim"
	webhookOutboxRetryClaimIndex             = "idx_outbox_webhook_retry_claim"
	webhookOutboxStaleClaimIndex             = "idx_outbox_webhook_stale_claim"
	webhookOutboxLifecycleCleanupIndex       = "idx_outbox_webhook_lifecycle_cleanup"
	webhookOutboxLegacySucceededCleanupIndex = "idx_outbox_webhook_legacy_cleanup"
	webhookSnapshotOverlapCleanupIndex       = "idx_webhook_snapshot_overlap_cleanup"
)

var webhookOutboxLifecycleIndexDefinitions = []automationWebhookIndexDefinition{
	{
		name:  webhookOutboxLifecycleClaimIndex,
		table: "outbox_deliveries",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "next_attempt_at"},
			{name: "created_at"},
			{name: "id"},
		},
		where: "destination_type <> 'webhook' AND " +
			"status IN ('pending', 'failed')",
		postgresPredicate: "(((destination_type)::text <> " +
			"'webhook'::text) AND ((status)::text = ANY " +
			"((ARRAY['pending'::character varying, " +
			"'failed'::character varying])::text[])))",
	},
	{
		name:  webhookOutboxLifecycleStaleClaimIndex,
		table: "outbox_deliveries",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "locked_at"},
			{name: "created_at"},
			{name: "id"},
		},
		where: "destination_type <> 'webhook' AND status = 'processing'",
		postgresPredicate: "(((destination_type)::text <> " +
			"'webhook'::text) AND ((status)::text = 'processing'::text))",
	},
	{
		name:  webhookOutboxRetryClaimIndex,
		table: "outbox_deliveries",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "destination_type"},
			{name: "status"},
			{name: "next_attempt_at"},
			{name: "created_at"},
			{name: "id"},
		},
		where: "destination_type = 'webhook' AND " +
			"status IN ('pending', 'failed') AND expires_at IS NOT NULL",
		postgresPredicate: "(((destination_type)::text = " +
			"'webhook'::text) AND ((status)::text = ANY " +
			"((ARRAY['pending'::character varying, " +
			"'failed'::character varying])::text[])) " +
			"AND (expires_at IS NOT NULL))",
	},
	{
		name:  webhookOutboxStaleClaimIndex,
		table: "outbox_deliveries",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "destination_type"},
			{name: "status"},
			{name: "locked_at"},
			{name: "created_at"},
			{name: "id"},
		},
		where: "destination_type = 'webhook' AND " +
			"status = 'processing' AND expires_at IS NOT NULL",
		postgresPredicate: "(((destination_type)::text = " +
			"'webhook'::text) AND ((status)::text = 'processing'::text) " +
			"AND (expires_at IS NOT NULL))",
	},
	{
		name:  webhookOutboxLifecycleCleanupIndex,
		table: "outbox_deliveries",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "destination_type"},
			{name: "status"},
			{name: "expires_at"},
			{name: "destination_id"},
			{name: "id"},
		},
		where: "destination_type = 'webhook' AND " +
			"status IN ('dead', 'failed', 'pending', 'processing') " +
			"AND expires_at IS NOT NULL",
		postgresPredicate: "(((destination_type)::text = " +
			"'webhook'::text) AND ((status)::text = ANY " +
			"((ARRAY['dead'::character varying, " +
			"'failed'::character varying, 'pending'::character varying, " +
			"'processing'::character varying])::text[])) " +
			"AND (expires_at IS NOT NULL))",
	},
	{
		name:  webhookOutboxLegacySucceededCleanupIndex,
		table: "outbox_deliveries",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "destination_type"},
			{name: "status"},
			{name: "destination_id"},
			{name: "id"},
		},
		where: "destination_type = 'webhook' AND status = 'succeeded'",
		postgresPredicate: "(((destination_type)::text = " +
			"'webhook'::text) AND ((status)::text = 'succeeded'::text))",
	},
	{
		name:  webhookSnapshotOverlapCleanupIndex,
		table: "webhook_delivery_snapshots",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "previous_secret_expires_at"},
			{name: "id"},
			{name: "credential_shredded_at"},
		},
		where: "credential_shredded_at IS NULL AND " +
			"previous_secret_expires_at IS NOT NULL",
		postgresPredicate: "((credential_shredded_at IS NULL) " +
			"AND (previous_secret_expires_at IS NOT NULL))",
	},
}

// MigrateWebhookOutboxLifecycleIndexes installs the physical indexes consumed
// by claim and bounded cleanup. It is independent of the foundation checkpoint
// so upgrading this query contract never changes the frozen cutover checksum.
func MigrateWebhookOutboxLifecycleIndexes(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if err := requireWebhookOutboxLifecycleIndexColumns(db); err != nil {
		return err
	}
	switch db.Dialector.Name() {
	case "postgres":
		return migratePostgresWebhookOutboxLifecycleIndexes(db)
	case "sqlite":
		return migrateSQLiteWebhookOutboxLifecycleIndexes(db)
	default:
		return fmt.Errorf(
			"webhook Outbox lifecycle index migration is unsupported for database dialect %q",
			db.Dialector.Name(),
		)
	}
}

func requireWebhookOutboxLifecycleIndexColumns(db *gorm.DB) error {
	seenTables := make(map[string]struct{})
	for _, definition := range webhookOutboxLifecycleIndexDefinitions {
		if _, seen := seenTables[definition.table]; !seen {
			if !db.Migrator().HasTable(definition.table) {
				return fmt.Errorf("%s table is required", definition.table)
			}
			seenTables[definition.table] = struct{}{}
		}
		for _, column := range definition.columns {
			if !db.Migrator().HasColumn(definition.table, column.name) {
				return fmt.Errorf(
					"%s.%s is required",
					definition.table,
					column.name,
				)
			}
		}
	}
	return nil
}

func migratePostgresWebhookOutboxLifecycleIndexes(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		schema, err := currentPostgresAutomationWebhookIndexSchema(tx)
		if err != nil {
			return err
		}
		lockedTables := make(map[string]struct{})
		for _, definition := range webhookOutboxLifecycleIndexDefinitions {
			if _, locked := lockedTables[definition.table]; !locked {
				qualifiedTable :=
					quoteAutomationWebhookPostgresIdentifier(schema) +
						"." +
						quoteAutomationWebhookPostgresIdentifier(
							definition.table,
						)
				if err := tx.Exec(
					"LOCK TABLE " + qualifiedTable +
						" IN SHARE ROW EXCLUSIVE MODE",
				).Error; err != nil {
					return fmt.Errorf(
						"lock %s for webhook Outbox lifecycle index migration: %w",
						definition.table,
						err,
					)
				}
				lockedTables[definition.table] = struct{}{}
			}
			valid, err := postgresAutomationWebhookIndexIsValid(
				tx,
				definition,
			)
			if err != nil {
				return err
			}
			if valid {
				continue
			}
			qualifiedIndex :=
				quoteAutomationWebhookPostgresIdentifier(schema) +
					"." +
					quoteAutomationWebhookPostgresIdentifier(
						definition.name,
					)
			if err := tx.Exec(
				"DROP INDEX IF EXISTS " + qualifiedIndex,
			).Error; err != nil {
				return err
			}
			if err := tx.Exec(
				postgresAutomationWebhookIndexDDL(schema, definition),
			).Error; err != nil {
				return err
			}
			if err := validatePostgresAutomationWebhookIndex(
				tx,
				definition,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func migrateSQLiteWebhookOutboxLifecycleIndexes(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		for _, definition := range webhookOutboxLifecycleIndexDefinitions {
			valid, err := sqliteAutomationWebhookIndexIsValid(
				tx,
				definition,
			)
			if err != nil {
				return err
			}
			if valid {
				continue
			}
			if err := tx.Exec(
				"DROP INDEX IF EXISTS " +
					quoteAutomationWebhookSQLiteIdentifier(
						definition.name,
					),
			).Error; err != nil {
				return err
			}
			if err := tx.Exec(
				sqliteAutomationWebhookIndexDDL(definition),
			).Error; err != nil {
				return err
			}
			if err := validateSQLiteAutomationWebhookIndex(
				tx,
				definition,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

// ValidateWebhookOutboxLifecycleIndexes is the read-only runtime exact gate.
func ValidateWebhookOutboxLifecycleIndexes(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	for _, definition := range webhookOutboxLifecycleIndexDefinitions {
		var (
			valid bool
			err   error
		)
		switch db.Dialector.Name() {
		case "postgres":
			valid, err = postgresAutomationWebhookIndexIsValid(
				db,
				definition,
			)
		case "sqlite":
			valid, err = sqliteAutomationWebhookIndexIsValid(
				db,
				definition,
			)
		default:
			return fmt.Errorf(
				"webhook Outbox lifecycle index validation is unsupported for database dialect %q",
				db.Dialector.Name(),
			)
		}
		if err != nil {
			return err
		}
		if !valid {
			return fmt.Errorf(
				"webhook Outbox lifecycle index %s on %s must be a valid, ready, non-unique, non-expression btree index on (%s) with its exact lifecycle predicate; run `go run ./cmd/migrate`",
				definition.name,
				definition.table,
				automationWebhookIndexContractDescription(definition),
			)
		}
	}
	return nil
}
