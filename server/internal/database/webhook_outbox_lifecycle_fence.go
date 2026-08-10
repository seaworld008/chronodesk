package database

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

const (
	webhookOutboxLifecycleFenceConstraint    = "chk_outbox_lifecycle_lock_token"
	webhookOutboxLifecycleFenceInsertTrigger = "trg_outbox_lifecycle_fence_insert"
	webhookOutboxLifecycleFenceUpdateTrigger = "trg_outbox_lifecycle_fence_update"
)

func MigrateWebhookOutboxLifecycleFence(db *gorm.DB) error {
	if db == nil {
		return errors.New("webhook Outbox lifecycle fence database is required")
	}
	if db.Dialector.Name() == "sqlite" {
		if err := requireSQLiteNoTempSchemaShadows(
			db,
			"outbox_deliveries",
			webhookOutboxLifecycleFenceInsertTrigger,
			webhookOutboxLifecycleFenceUpdateTrigger,
		); err != nil {
			return err
		}
	}
	if !db.Migrator().HasTable(&models.OutboxDelivery{}) {
		return errors.New("outbox_deliveries table is required")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			if err := tx.Exec(
				"LOCK TABLE outbox_deliveries IN SHARE ROW EXCLUSIVE MODE",
			).Error; err != nil {
				return fmt.Errorf(
					"lock Outbox lifecycle fence table: %w",
					err,
				)
			}
		}
		hasColumn, err := hasExactDatabaseColumn(
			tx,
			"outbox_deliveries",
			"lock_token",
		)
		if err != nil {
			return err
		}
		if !hasColumn {
			columnType := "TEXT"
			if tx.Dialector.Name() == "postgres" {
				columnType = "VARCHAR(36)"
			}
			if err := tx.Exec(
				"ALTER TABLE outbox_deliveries ADD COLUMN lock_token " +
					columnType,
			).Error; err != nil {
				return fmt.Errorf(
					"add outbox_deliveries.lock_token: %w",
					err,
				)
			}
		}
		if err := backfillWebhookOutboxLifecycleFence(tx); err != nil {
			return err
		}
		switch tx.Dialector.Name() {
		case "postgres":
			if err := installPostgresWebhookOutboxLifecycleFence(
				tx,
			); err != nil {
				return err
			}
		case "sqlite":
			if err := installSQLiteWebhookOutboxLifecycleFence(
				tx,
			); err != nil {
				return err
			}
		default:
			return fmt.Errorf(
				"webhook Outbox lifecycle fence migration is unsupported for database dialect %q",
				tx.Dialector.Name(),
			)
		}
		return ValidateWebhookOutboxLifecycleFence(tx)
	})
}

func backfillWebhookOutboxLifecycleFence(db *gorm.DB) error {
	table := "outbox_deliveries"
	if db.Dialector.Name() == "sqlite" {
		table = "main.outbox_deliveries"
	}
	if err := db.Exec(
		`UPDATE `+table+`
		 SET lock_token = NULL
		 WHERE status <> ? AND lock_token IS NOT NULL`,
		models.OutboxDeliveryProcessing,
	).Error; err != nil {
		return fmt.Errorf(
			"clear legacy non-processing Outbox lock tokens: %w",
			err,
		)
	}
	var processing []struct {
		ID        string         `gorm:"column:id"`
		LockToken sql.NullString `gorm:"column:lock_token"`
	}
	if err := db.Raw(
		`SELECT id, lock_token
		 FROM `+table+`
		 WHERE status = ?
		 ORDER BY id`,
		models.OutboxDeliveryProcessing,
	).Scan(&processing).Error; err != nil {
		return fmt.Errorf(
			"read legacy processing Outbox lock tokens: %w",
			err,
		)
	}
	for _, delivery := range processing {
		if delivery.LockToken.Valid &&
			webhookOutboxLifecycleTokenIsUUIDv7(
				delivery.LockToken.String,
			) {
			continue
		}
		token, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf(
				"generate legacy Outbox lifecycle fence: %w",
				err,
			)
		}
		result := db.Exec(
			`UPDATE `+table+`
			 SET lock_token = ?
			 WHERE id = ? AND status = ?`,
			token.String(),
			delivery.ID,
			models.OutboxDeliveryProcessing,
		)
		if result.Error != nil {
			return fmt.Errorf(
				"backfill legacy Outbox lifecycle fence: %w",
				result.Error,
			)
		}
		if result.RowsAffected != 1 {
			return errors.New(
				"legacy Outbox lifecycle fence candidate changed",
			)
		}
	}
	return nil
}

func installPostgresWebhookOutboxLifecycleFence(db *gorm.DB) error {
	valid, err := postgresWebhookOutboxLifecycleFenceIsValid(db)
	if err != nil {
		return err
	}
	if valid {
		return nil
	}
	if err := db.Exec(
		"ALTER TABLE outbox_deliveries DROP CONSTRAINT IF EXISTS " +
			webhookOutboxLifecycleFenceConstraint,
	).Error; err != nil {
		return fmt.Errorf("drop drifted Outbox lifecycle fence: %w", err)
	}
	if err := db.Exec(
		"ALTER TABLE outbox_deliveries ADD CONSTRAINT " +
			webhookOutboxLifecycleFenceConstraint +
			" CHECK (" + postgresWebhookOutboxLifecycleFenceExpression() +
			") NOT VALID",
	).Error; err != nil {
		return fmt.Errorf("install Outbox lifecycle fence: %w", err)
	}
	if err := db.Exec(
		"ALTER TABLE outbox_deliveries VALIDATE CONSTRAINT " +
			webhookOutboxLifecycleFenceConstraint,
	).Error; err != nil {
		return fmt.Errorf("validate Outbox lifecycle fence: %w", err)
	}
	return nil
}

func installSQLiteWebhookOutboxLifecycleFence(db *gorm.DB) error {
	if err := requireSQLiteNoTempSchemaShadows(
		db,
		"outbox_deliveries",
		webhookOutboxLifecycleFenceInsertTrigger,
		webhookOutboxLifecycleFenceUpdateTrigger,
	); err != nil {
		return err
	}
	for _, definition := range sqliteWebhookOutboxLifecycleFenceTriggers() {
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
			return fmt.Errorf(
				"inspect SQLite Outbox lifecycle fence %s: %w",
				definition.name,
				result.Error,
			)
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
			return fmt.Errorf(
				"drop drifted SQLite Outbox lifecycle fence %s: %w",
				definition.name,
				err,
			)
		}
		if err := db.Exec(definition.sql).Error; err != nil {
			return fmt.Errorf(
				"install SQLite Outbox lifecycle fence %s: %w",
				definition.name,
				err,
			)
		}
	}
	return nil
}

func ValidateWebhookOutboxLifecycleFence(db *gorm.DB) error {
	if db == nil {
		return errors.New("webhook Outbox lifecycle fence database is required")
	}
	if db.Dialector.Name() == "sqlite" {
		if err := requireSQLiteNoTempSchemaShadows(
			db,
			"outbox_deliveries",
			webhookOutboxLifecycleFenceInsertTrigger,
			webhookOutboxLifecycleFenceUpdateTrigger,
		); err != nil {
			return err
		}
	}
	if err := validateWebhookOutboxLifecycleFenceColumn(db); err != nil {
		return err
	}
	switch db.Dialector.Name() {
	case "postgres":
		valid, err := postgresWebhookOutboxLifecycleFenceIsValid(db)
		if err != nil {
			return err
		}
		if !valid {
			return fmt.Errorf(
				"PostgreSQL Outbox lifecycle fence %s is missing or incompatible",
				webhookOutboxLifecycleFenceConstraint,
			)
		}
		return nil
	case "sqlite":
		for _, definition := range sqliteWebhookOutboxLifecycleFenceTriggers() {
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
					"SQLite Outbox lifecycle fence %s is missing or incompatible",
					definition.name,
				)
			}
		}
		return validateWebhookOutboxLifecycleFenceData(db)
	default:
		return fmt.Errorf(
			"webhook Outbox lifecycle fence validation is unsupported for database dialect %q",
			db.Dialector.Name(),
		)
	}
}

func validateWebhookOutboxLifecycleFenceColumn(db *gorm.DB) error {
	switch db.Dialector.Name() {
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
		var lockTokenColumns []struct {
			Name         string
			Type         string
			NotNull      int
			DefaultValue sql.NullString
			PK           int
		}
		for _, column := range columns {
			if strings.EqualFold(column.Name, "lock_token") {
				lockTokenColumns = append(lockTokenColumns, struct {
					Name         string
					Type         string
					NotNull      int
					DefaultValue sql.NullString
					PK           int
				}{
					Name:         column.Name,
					Type:         column.Type,
					NotNull:      column.NotNull,
					DefaultValue: column.DefaultValue,
					PK:           column.PK,
				})
			}
		}
		if len(lockTokenColumns) != 1 ||
			lockTokenColumns[0].Name != "lock_token" ||
			!strings.EqualFold(lockTokenColumns[0].Type, "TEXT") ||
			lockTokenColumns[0].NotNull != 0 ||
			lockTokenColumns[0].DefaultValue.Valid ||
			lockTokenColumns[0].PK != 0 {
			return errors.New(
				"SQLite outbox_deliveries.lock_token must be one nullable TEXT column",
			)
		}
	case "postgres":
		var columns []struct {
			DataType      string         `gorm:"column:data_type"`
			MaximumLength sql.NullInt64  `gorm:"column:maximum_length"`
			IsNullable    string         `gorm:"column:is_nullable"`
			ColumnDefault sql.NullString `gorm:"column:column_default"`
			Identity      string         `gorm:"column:is_identity"`
			Generated     string         `gorm:"column:is_generated"`
		}
		if err := db.Raw(`
			SELECT
				data_type,
				character_maximum_length AS maximum_length,
				is_nullable,
				column_default,
				is_identity,
				is_generated
			FROM information_schema.columns
			WHERE table_schema = CURRENT_SCHEMA()
			  AND table_name = 'outbox_deliveries'
			  AND column_name = 'lock_token'
		`).Scan(&columns).Error; err != nil {
			return err
		}
		if len(columns) != 1 ||
			columns[0].DataType != "character varying" ||
			!columns[0].MaximumLength.Valid ||
			columns[0].MaximumLength.Int64 != 36 ||
			columns[0].IsNullable != "YES" ||
			columns[0].ColumnDefault.Valid ||
			columns[0].Identity != "NO" ||
			columns[0].Generated != "NEVER" {
			return errors.New(
				"PostgreSQL outbox_deliveries.lock_token must be one nullable VARCHAR(36) plain column",
			)
		}
	default:
		return fmt.Errorf(
			"unsupported lifecycle fence column dialect %q",
			db.Dialector.Name(),
		)
	}
	return nil
}

func validateWebhookOutboxLifecycleFenceData(db *gorm.DB) error {
	if db.Dialector.Name() != "sqlite" {
		return nil
	}
	var invalid bool
	if err := db.Raw(
		`SELECT EXISTS (
			SELECT 1
			FROM main.outbox_deliveries
			WHERE NOT (
				(status = ? AND lock_token IS NOT NULL
				 AND lock_token GLOB ?)
				OR (status <> ? AND lock_token IS NULL)
			)
			LIMIT 1
		 ) AS invalid`,
		models.OutboxDeliveryProcessing,
		sqliteWebhookOutboxLifecycleUUIDv7Glob(),
		models.OutboxDeliveryProcessing,
	).Scan(&invalid).Error; err != nil {
		return err
	}
	if invalid {
		return errors.New("Outbox delivery has an invalid lifecycle fence")
	}
	return nil
}

func postgresWebhookOutboxLifecycleFenceIsValid(
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
	`, webhookOutboxLifecycleFenceConstraint).Scan(&states).Error; err != nil {
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
		postgresWebhookOutboxLifecycleFenceExpression(),
	)
	return err == nil && got == want, err
}

func postgresWebhookOutboxLifecycleFenceExpression() string {
	return `(status = 'processing' AND lock_token IS NOT NULL AND ` +
		`lock_token ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-` +
		`[89ab][0-9a-f]{3}-[0-9a-f]{12}$') OR ` +
		`(status <> 'processing' AND lock_token IS NULL)`
}

type sqliteLifecycleFenceTrigger struct {
	name string
	sql  string
}

func sqliteWebhookOutboxLifecycleFenceTriggers() []sqliteLifecycleFenceTrigger {
	expression := `(NEW.status = 'processing' ` +
		`AND NEW.lock_token IS NOT NULL ` +
		`AND NEW.lock_token GLOB '` +
		sqliteWebhookOutboxLifecycleUUIDv7Glob() + `') ` +
		`OR (NEW.status <> 'processing' AND NEW.lock_token IS NULL)`
	return []sqliteLifecycleFenceTrigger{
		{
			name: webhookOutboxLifecycleFenceInsertTrigger,
			sql: `CREATE TRIGGER ` +
				quoteAutomationWebhookSQLiteIdentifier(
					webhookOutboxLifecycleFenceInsertTrigger,
				) +
				` BEFORE INSERT ON "outbox_deliveries" FOR EACH ROW ` +
				`WHEN NOT (` + expression + `) BEGIN ` +
				`SELECT RAISE(ABORT, 'outbox lifecycle fence invariant'); ` +
				`END`,
		},
		{
			name: webhookOutboxLifecycleFenceUpdateTrigger,
			sql: `CREATE TRIGGER ` +
				quoteAutomationWebhookSQLiteIdentifier(
					webhookOutboxLifecycleFenceUpdateTrigger,
				) +
				` BEFORE UPDATE OF status, lock_token ` +
				`ON "outbox_deliveries" FOR EACH ROW ` +
				`WHEN NOT (` + expression + `) BEGIN ` +
				`SELECT RAISE(ABORT, 'outbox lifecycle fence invariant'); ` +
				`END`,
		},
	}
}

func sqliteWebhookOutboxLifecycleUUIDv7Glob() string {
	hex := "[0-9a-f]"
	return strings.Repeat(hex, 8) + "-" +
		strings.Repeat(hex, 4) + "-7" +
		strings.Repeat(hex, 3) + "-[89ab]" +
		strings.Repeat(hex, 3) + "-" +
		strings.Repeat(hex, 12)
}

func canonicalSQLiteLifecycleFenceSQL(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func webhookOutboxLifecycleTokenIsUUIDv7(value string) bool {
	if value != strings.ToLower(value) {
		return false
	}
	token, err := uuid.Parse(value)
	return err == nil &&
		token.String() == value &&
		token.Version() == 7 &&
		(token.Variant() == uuid.RFC4122)
}
