package database

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/database/webhookdispatch"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

const (
	webhookDispatchStartConstraint       = "chk_outbox_dispatch_started_at"
	webhookDispatchStartInsertTrigger    = "trg_outbox_dispatch_start_insert"
	webhookDispatchStartUpdateTrigger    = "trg_outbox_dispatch_start_update"
	webhookDispatchGenerationPostgresTrg = webhookdispatch.GenerationTriggerName
)

func MigrateWebhookDispatchStartBoundary(db *gorm.DB) error {
	if db == nil {
		return errors.New("Webhook dispatch-start database is required")
	}
	if !db.Migrator().HasTable(&models.OutboxDelivery{}) {
		return errors.New("outbox_deliveries table is required")
	}
	if db.Dialector.Name() == "sqlite" {
		if err := requireSQLiteNoTempSchemaShadows(
			db,
			"outbox_deliveries",
			webhookDispatchStartInsertTrigger,
			webhookDispatchStartUpdateTrigger,
		); err != nil {
			return err
		}
	}
	return db.Transaction(func(tx *gorm.DB) error {
		qualifiedTable := "outbox_deliveries"
		if tx.Dialector.Name() == "postgres" {
			schema, err := currentPostgresDispatchSchema(tx)
			if err != nil {
				return err
			}
			qualifiedTable = qualifiedPostgresDispatchObject(
				schema,
				"outbox_deliveries",
			)
			if err := tx.Exec(
				"LOCK TABLE " + qualifiedTable +
					" IN SHARE ROW EXCLUSIVE MODE",
			).Error; err != nil {
				return fmt.Errorf(
					"lock Webhook dispatch-start table: %w",
					err,
				)
			}
		}
		hasColumn, err := hasExactDatabaseColumn(
			tx,
			"outbox_deliveries",
			"dispatch_started_at",
		)
		if err != nil {
			return err
		}
		if !hasColumn {
			columnType := "datetime"
			if tx.Dialector.Name() == "postgres" {
				columnType = "TIMESTAMPTZ"
			}
			if err := tx.Exec(
				"ALTER TABLE " + qualifiedTable + " ADD COLUMN " +
					"dispatch_started_at " + columnType,
			).Error; err != nil {
				return fmt.Errorf(
					"add outbox_deliveries.dispatch_started_at: %w",
					err,
				)
			}
		}
		if err := validateWebhookDispatchStartData(tx); err != nil {
			return err
		}
		var installErr error
		switch tx.Dialector.Name() {
		case "postgres":
			installErr =
				installPostgresWebhookDispatchStartBoundary(tx)
		case "sqlite":
			installErr =
				installSQLiteWebhookDispatchStartBoundary(tx)
		default:
			return fmt.Errorf(
				"Webhook dispatch-start migration is unsupported for database dialect %q",
				tx.Dialector.Name(),
			)
		}
		if installErr != nil {
			return installErr
		}
		return ValidateWebhookDispatchStartBoundary(tx)
	})
}

func ValidateWebhookDispatchStartBoundary(db *gorm.DB) error {
	if db == nil {
		return errors.New("Webhook dispatch-start database is required")
	}
	if err := validateWebhookDispatchStartColumn(db); err != nil {
		return err
	}
	if err := validateWebhookDispatchStartData(db); err != nil {
		return err
	}
	switch db.Dialector.Name() {
	case "postgres":
		valid, err := postgresWebhookDispatchStartConstraintIsValid(db)
		if err != nil {
			return err
		}
		if !valid {
			return fmt.Errorf(
				"PostgreSQL Webhook dispatch-start constraint %s is missing or incompatible",
				webhookDispatchStartConstraint,
			)
		}
		generationValid, err :=
			postgresWebhookDispatchGenerationFenceIsValid(db)
		if err != nil {
			return err
		}
		if !generationValid {
			return fmt.Errorf(
				"PostgreSQL Webhook dispatch generation fence %s is missing or incompatible",
				webhookDispatchGenerationPostgresTrg,
			)
		}
		return nil
	case "sqlite":
		if err := requireSQLiteNoTempSchemaShadows(
			db,
			"outbox_deliveries",
			webhookDispatchStartInsertTrigger,
			webhookDispatchStartUpdateTrigger,
		); err != nil {
			return err
		}
		for _, definition := range sqliteWebhookDispatchStartTriggers() {
			var existing struct {
				SQL string `gorm:"column:sql"`
			}
			result := db.Raw(
				`SELECT sql
				 FROM main.sqlite_schema
				 WHERE type = 'trigger' AND name = ?`,
				definition.name,
			).Scan(&existing)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 ||
				canonicalSQLiteLifecycleFenceSQL(existing.SQL) !=
					canonicalSQLiteLifecycleFenceSQL(definition.sql) {
				return fmt.Errorf(
					"SQLite Webhook dispatch-start boundary %s is missing or incompatible",
					definition.name,
				)
			}
		}
		return nil
	default:
		return fmt.Errorf(
			"Webhook dispatch-start validation is unsupported for database dialect %q",
			db.Dialector.Name(),
		)
	}
}

func webhookDispatchStartExpression(columnPrefix string) string {
	return columnPrefix + "dispatch_started_at IS NULL OR (" +
		columnPrefix + "destination_type = 'webhook' AND (" +
		columnPrefix + "status = 'processing' OR " +
		columnPrefix + "status = 'succeeded' OR " +
		columnPrefix + "status = 'expired') AND (" +
		columnPrefix + "status <> 'processing' OR (" +
		columnPrefix + "locked_at IS NOT NULL AND " +
		"(" + columnPrefix + "dispatch_started_at = " +
		columnPrefix + "locked_at OR " +
		columnPrefix + "dispatch_started_at > " +
		columnPrefix + "locked_at))))"
}

func validateWebhookDispatchStartData(db *gorm.DB) error {
	table := "outbox_deliveries"
	if db.Dialector.Name() == "sqlite" {
		table = "main.outbox_deliveries"
	}
	var invalid bool
	if err := db.Raw(
		"SELECT EXISTS (SELECT 1 FROM " + table + " WHERE NOT (" +
			webhookDispatchStartExpression("") +
			") LIMIT 1) AS invalid",
	).Scan(&invalid).Error; err != nil {
		return fmt.Errorf("validate Webhook dispatch-start data: %w", err)
	}
	if invalid {
		return errors.New(
			"Outbox delivery has an invalid Webhook dispatch-start state",
		)
	}
	return nil
}

func validateWebhookDispatchStartColumn(db *gorm.DB) error {
	switch db.Dialector.Name() {
	case "postgres":
		var columns []struct {
			DataType      string         `gorm:"column:data_type"`
			IsNullable    string         `gorm:"column:is_nullable"`
			ColumnDefault sql.NullString `gorm:"column:column_default"`
			Identity      string         `gorm:"column:is_identity"`
			Generated     string         `gorm:"column:is_generated"`
		}
		if err := db.Raw(`
			SELECT
				data_type,
				is_nullable,
				column_default,
				is_identity,
				is_generated
			FROM information_schema.columns
			WHERE table_schema = CURRENT_SCHEMA()
			  AND table_name = 'outbox_deliveries'
			  AND column_name = 'dispatch_started_at'
		`).Scan(&columns).Error; err != nil {
			return err
		}
		if len(columns) != 1 ||
			columns[0].DataType != "timestamp with time zone" ||
			columns[0].IsNullable != "YES" ||
			columns[0].ColumnDefault.Valid ||
			columns[0].Identity != "NO" ||
			columns[0].Generated != "NEVER" {
			return errors.New(
				"PostgreSQL outbox_deliveries.dispatch_started_at must be one nullable TIMESTAMPTZ plain column",
			)
		}
	case "sqlite":
		var columns []struct {
			Name         string         `gorm:"column:name"`
			Type         string         `gorm:"column:type"`
			NotNull      int            `gorm:"column:notnull"`
			DefaultValue sql.NullString `gorm:"column:dflt_value"`
			PK           int            `gorm:"column:pk"`
		}
		if err := db.Raw(
			`PRAGMA main.table_info("outbox_deliveries")`,
		).Scan(&columns).Error; err != nil {
			return err
		}
		matches := 0
		for _, column := range columns {
			if column.Name != "dispatch_started_at" {
				continue
			}
			matches++
			if !strings.EqualFold(column.Type, "datetime") ||
				column.NotNull != 0 ||
				column.DefaultValue.Valid ||
				column.PK != 0 {
				return errors.New(
					"SQLite outbox_deliveries.dispatch_started_at must be one nullable datetime column",
				)
			}
		}
		if matches != 1 {
			return errors.New(
				"SQLite outbox_deliveries.dispatch_started_at must be one nullable datetime column",
			)
		}
	default:
		return fmt.Errorf(
			"unsupported Webhook dispatch-start column dialect %q",
			db.Dialector.Name(),
		)
	}
	return nil
}

func installPostgresWebhookDispatchStartBoundary(db *gorm.DB) error {
	schema, err := currentPostgresDispatchSchema(db)
	if err != nil {
		return err
	}
	qualifiedTable := qualifiedPostgresDispatchObject(
		schema,
		"outbox_deliveries",
	)
	valid, err := postgresWebhookDispatchStartConstraintIsValid(db)
	if err != nil {
		return err
	}
	if !valid {
		if err := db.Exec(
			"ALTER TABLE " + qualifiedTable +
				" DROP CONSTRAINT IF EXISTS " +
				quotePostgresDispatchIdentifier(
					webhookDispatchStartConstraint,
				),
		).Error; err != nil {
			return fmt.Errorf(
				"drop drifted Webhook dispatch-start constraint: %w",
				err,
			)
		}
		if err := db.Exec(
			"ALTER TABLE " + qualifiedTable + " ADD CONSTRAINT " +
				quotePostgresDispatchIdentifier(
					webhookDispatchStartConstraint,
				) + " CHECK (" +
				webhookDispatchStartExpression("") + ") NOT VALID",
		).Error; err != nil {
			return fmt.Errorf(
				"install Webhook dispatch-start constraint: %w",
				err,
			)
		}
		if err := db.Exec(
			"ALTER TABLE " + qualifiedTable +
				" VALIDATE CONSTRAINT " +
				quotePostgresDispatchIdentifier(
					webhookDispatchStartConstraint,
				),
		).Error; err != nil {
			return fmt.Errorf(
				"validate Webhook dispatch-start constraint: %w",
				err,
			)
		}
	}
	generationValid, err :=
		postgresWebhookDispatchGenerationFenceIsValid(db)
	if err != nil {
		return err
	}
	if generationValid {
		return nil
	}
	return installPostgresWebhookDispatchGenerationFence(db)
}

func installPostgresWebhookDispatchGenerationFence(
	db *gorm.DB,
) error {
	return webhookdispatch.MigratePostgresGenerationFence(db)
}

func currentPostgresDispatchSchema(db *gorm.DB) (string, error) {
	var schema string
	if err := db.Raw("SELECT CURRENT_SCHEMA()").Scan(&schema).Error; err != nil {
		return "", fmt.Errorf(
			"resolve PostgreSQL Webhook dispatch schema: %w",
			err,
		)
	}
	if strings.TrimSpace(schema) == "" {
		return "", errors.New(
			"PostgreSQL Webhook dispatch current schema is required",
		)
	}
	return schema, nil
}

func quotePostgresDispatchIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func qualifiedPostgresDispatchObject(schema string, name string) string {
	return quotePostgresDispatchIdentifier(schema) + "." +
		quotePostgresDispatchIdentifier(name)
}

func postgresWebhookDispatchStartConstraintIsValid(
	db *gorm.DB,
) (bool, error) {
	var states []struct {
		Type       string `gorm:"column:constraint_type"`
		Expression string `gorm:"column:expression"`
		Validated  bool   `gorm:"column:validated"`
		NoInherit  bool   `gorm:"column:no_inherit"`
	}
	if err := db.Raw(`
		SELECT
			constraint_state.contype::text AS constraint_type,
			pg_get_expr(
				constraint_state.conbin,
				constraint_state.conrelid,
				false
			) AS expression,
			constraint_state.convalidated AS validated,
			constraint_state.connoinherit AS no_inherit
		FROM pg_constraint AS constraint_state
		JOIN pg_class AS table_state
		  ON table_state.oid = constraint_state.conrelid
		JOIN pg_namespace AS namespace
		  ON namespace.oid = table_state.relnamespace
		WHERE namespace.nspname = CURRENT_SCHEMA()
		  AND table_state.relname = 'outbox_deliveries'
		  AND constraint_state.conname = ?
	`, webhookDispatchStartConstraint).Scan(&states).Error; err != nil {
		return false, err
	}
	if len(states) != 1 ||
		states[0].Type != "c" ||
		!states[0].Validated ||
		states[0].NoInherit {
		return false, nil
	}
	got, err := canonicalWebhookConstraintDefinition(
		states[0].Expression,
	)
	if err != nil {
		return false, err
	}
	want, err := canonicalWebhookConstraintDefinition(
		webhookDispatchStartExpression(""),
	)
	return err == nil && got == want, err
}

func postgresWebhookDispatchGenerationFenceIsValid(
	db *gorm.DB,
) (bool, error) {
	return webhookdispatch.PostgresGenerationFenceIsValid(db)
}

func installSQLiteWebhookDispatchStartBoundary(db *gorm.DB) error {
	for _, definition := range sqliteWebhookDispatchStartTriggers() {
		var existing struct {
			SQL string `gorm:"column:sql"`
		}
		result := db.Raw(
			`SELECT sql
			 FROM main.sqlite_schema
			 WHERE type = 'trigger' AND name = ?`,
			definition.name,
		).Scan(&existing)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 &&
			canonicalSQLiteLifecycleFenceSQL(existing.SQL) ==
				canonicalSQLiteLifecycleFenceSQL(definition.sql) {
			continue
		}
		if err := db.Exec(
			"DROP TRIGGER IF EXISTS " +
				quoteAutomationWebhookSQLiteIdentifier(definition.name),
		).Error; err != nil {
			return err
		}
		if err := db.Exec(definition.sql).Error; err != nil {
			return err
		}
	}
	return nil
}

func sqliteWebhookDispatchStartTriggers() []sqliteLifecycleFenceTrigger {
	expression := webhookDispatchStartExpression("NEW.")
	generationViolation := sqliteWebhookDispatchGenerationViolation()
	return []sqliteLifecycleFenceTrigger{
		{
			name: webhookDispatchStartInsertTrigger,
			sql: `CREATE TRIGGER ` +
				quoteAutomationWebhookSQLiteIdentifier(
					webhookDispatchStartInsertTrigger,
				) +
				` AFTER INSERT ON "outbox_deliveries" FOR EACH ROW ` +
				`WHEN NOT (` + expression + `) BEGIN ` +
				`SELECT RAISE(ABORT, 'outbox dispatch-start invariant'); ` +
				`END`,
		},
		{
			name: webhookDispatchStartUpdateTrigger,
			sql: `CREATE TRIGGER ` +
				quoteAutomationWebhookSQLiteIdentifier(
					webhookDispatchStartUpdateTrigger,
				) +
				` AFTER UPDATE ON "outbox_deliveries" FOR EACH ROW ` +
				`WHEN NOT (` + expression + `) OR (` +
				generationViolation + `) BEGIN ` +
				`SELECT RAISE(ABORT, 'outbox dispatch-start invariant'); ` +
				`END`,
		},
	}
}

func sqliteWebhookDispatchGenerationViolation() string {
	generationSame := `OLD.attempts IS NEW.attempts ` +
		`AND OLD.locked_at IS NEW.locked_at ` +
		`AND OLD.locked_by IS NEW.locked_by ` +
		`AND OLD.lock_token IS NEW.lock_token`
	oldPrepared := `OLD.dispatch_started_at IS NOT NULL ` +
		`AND OLD.locked_at IS NOT NULL ` +
		`AND OLD.dispatch_started_at = OLD.locked_at`
	newPrepared := `NEW.dispatch_started_at IS NOT NULL ` +
		`AND NEW.locked_at IS NOT NULL ` +
		`AND NEW.dispatch_started_at = NEW.locked_at`
	reboundPrepared := `(` + oldPrepared + `) AND (` +
		newPrepared + `) AND ` +
		`NEW.dispatch_started_at IS NOT OLD.dispatch_started_at ` +
		`AND NEW.attempts = OLD.attempts + 1 ` +
		`AND NEW.locked_at > OLD.locked_at ` +
		`AND TRIM(NEW.locked_by) <> '' ` +
		`AND NEW.lock_token IS NOT NULL ` +
		`AND NEW.lock_token IS NOT OLD.lock_token ` +
		`AND NEW.lock_token GLOB '` +
		sqliteWebhookOutboxLifecycleUUIDv7Glob() + `'`
	sameGenerationMarkerTransition := `(` +
		`OLD.dispatch_started_at IS NULL ` +
		`AND NEW.dispatch_started_at IS NULL` +
		`) OR ((` + oldPrepared + `) AND (` +
		`NEW.dispatch_started_at = OLD.dispatch_started_at ` +
		`OR NEW.dispatch_started_at > NEW.locked_at` +
		`)) OR (` +
		`OLD.dispatch_started_at IS NOT NULL ` +
		`AND OLD.locked_at IS NOT NULL ` +
		`AND OLD.dispatch_started_at > OLD.locked_at ` +
		`AND NEW.dispatch_started_at IS OLD.dispatch_started_at` +
		`)`
	allowed := `NEW.destination_type = 'webhook' AND ((NOT (` +
		generationSame + `) AND (` + reboundPrepared + `)) OR ((` +
		generationSame + `) AND (` + sameGenerationMarkerTransition + `)))`
	return `OLD.destination_type = 'webhook' ` +
		`AND OLD.status = 'processing' ` +
		`AND NEW.status = 'processing' ` +
		`AND NOT (` + allowed + `)`
}
