package database

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

const idempotencyScopeIndexName = "idx_idempotency_actor_operation_key"

var idempotencyScopeIndexColumns = []string{
	"organization_id",
	"project_id",
	"actor_type",
	"actor_id",
	"operation",
	"key",
}

// MigrateIdempotencyScopeIndex replaces the pre-project four-column uniqueness
// rule with the project-scoped contract used by AgentNativeService. GORM keeps
// an existing same-named index even when its model definition gains columns,
// so an explicit migration is required before the six-column ON CONFLICT
// target can be used.
//
// The table write lock and both DDL statements live in one transaction. A
// failed rebuild therefore restores the previous index and never exposes a
// window in which concurrent writes can bypass idempotency uniqueness.
func MigrateIdempotencyScopeIndex(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if !db.Migrator().HasTable(&models.IdempotencyRecord{}) {
		return errors.New("idempotency_records table is required")
	}
	for _, column := range idempotencyScopeIndexColumns {
		if !db.Migrator().HasColumn(&models.IdempotencyRecord{}, column) {
			return fmt.Errorf("idempotency_records.%s is required", column)
		}
	}

	switch db.Dialector.Name() {
	case "postgres":
		return migratePostgresIdempotencyScopeIndex(db)
	case "sqlite":
		return migrateSQLiteIdempotencyScopeIndex(db)
	default:
		return fmt.Errorf(
			"idempotency scope index migration is unsupported for database dialect %q",
			db.Dialector.Name(),
		)
	}
}

func migratePostgresIdempotencyScopeIndex(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		schema, err := currentPostgresIdempotencySchema(tx)
		if err != nil {
			return err
		}
		qualifiedTable := quotePostgresIdempotencyIdentifier(schema) +
			"." + quotePostgresIdempotencyIdentifier("idempotency_records")
		qualifiedIndex := quotePostgresIdempotencyIdentifier(schema) +
			"." + quotePostgresIdempotencyIdentifier(idempotencyScopeIndexName)

		// SHARE ROW EXCLUSIVE permits ordinary reads while blocking INSERT,
		// UPDATE and DELETE until the atomic DROP/CREATE transaction commits.
		if err := tx.Exec(
			"LOCK TABLE " + qualifiedTable + " IN SHARE ROW EXCLUSIVE MODE",
		).Error; err != nil {
			return fmt.Errorf("lock idempotency records for index migration: %w", err)
		}

		valid, err := postgresIdempotencyScopeIndexIsValid(tx)
		if err != nil {
			return err
		}
		if valid {
			return nil
		}
		if err := tx.Exec(
			"DROP INDEX IF EXISTS " + qualifiedIndex,
		).Error; err != nil {
			return fmt.Errorf("drop legacy idempotency uniqueness index: %w", err)
		}
		if err := tx.Exec(
			`CREATE UNIQUE INDEX ` +
				quotePostgresIdempotencyIdentifier(idempotencyScopeIndexName) +
				` ON ` + qualifiedTable + ` (
				organization_id,
				project_id,
				actor_type,
				actor_id,
				operation,
				key
			)`,
		).Error; err != nil {
			return fmt.Errorf("create project-scoped idempotency uniqueness index: %w", err)
		}
		return validatePostgresIdempotencyScopeIndex(tx)
	})
}

func currentPostgresIdempotencySchema(db *gorm.DB) (string, error) {
	var schema string
	if err := db.Raw("SELECT CURRENT_SCHEMA()").Scan(&schema).Error; err != nil {
		return "", fmt.Errorf("resolve PostgreSQL idempotency schema: %w", err)
	}
	if strings.TrimSpace(schema) == "" {
		return "", errors.New("PostgreSQL current schema is required")
	}
	return schema, nil
}

func quotePostgresIdempotencyIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func migrateSQLiteIdempotencyScopeIndex(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		valid, err := sqliteIdempotencyScopeIndexIsValid(tx)
		if err != nil {
			return err
		}
		if valid {
			return nil
		}
		if err := tx.Exec(
			"DROP INDEX IF EXISTS " + idempotencyScopeIndexName,
		).Error; err != nil {
			return fmt.Errorf("drop legacy idempotency uniqueness index: %w", err)
		}
		if err := tx.Exec(`
			CREATE UNIQUE INDEX idx_idempotency_actor_operation_key
			ON idempotency_records (
				organization_id,
				project_id,
				actor_type,
				actor_id,
				operation,
				key
			)
		`).Error; err != nil {
			return fmt.Errorf("create project-scoped idempotency uniqueness index: %w", err)
		}
		return validateSQLiteIdempotencyScopeIndex(tx)
	})
}

// ValidateIdempotencyScopeIndex is a runtime fail-fast gate for the exact
// unique-index contract required by the scoped ON CONFLICT write.
func ValidateIdempotencyScopeIndex(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	var err error
	switch db.Dialector.Name() {
	case "postgres":
		err = validatePostgresIdempotencyScopeIndex(db)
	case "sqlite":
		err = validateSQLiteIdempotencyScopeIndex(db)
	default:
		return fmt.Errorf(
			"idempotency scope index validation is unsupported for database dialect %q",
			db.Dialector.Name(),
		)
	}
	if err != nil {
		return fmt.Errorf(
			"idempotency uniqueness index must be UNIQUE (%s) in exact order; run `go run ./cmd/migrate`: %w",
			strings.Join(idempotencyScopeIndexColumns, ", "),
			err,
		)
	}
	return nil
}

type postgresIndexColumn struct {
	ColumnName     string `gorm:"column:column_name"`
	Ordinal        int    `gorm:"column:ordinal"`
	KeyColumnCount int    `gorm:"column:key_column_count"`
	IsUnique       bool   `gorm:"column:is_unique"`
	IsValid        bool   `gorm:"column:is_valid"`
	IsReady        bool   `gorm:"column:is_ready"`
	HasPredicate   bool   `gorm:"column:has_predicate"`
	HasExpressions bool   `gorm:"column:has_expressions"`
}

func postgresIdempotencyScopeIndexIsValid(db *gorm.DB) (bool, error) {
	var rows []postgresIndexColumn
	if err := db.Raw(`
		SELECT
			COALESCE(attribute.attname, '') AS column_name,
			index_key.ordinality::integer AS ordinal,
			index_row.indnkeyatts::integer AS key_column_count,
			index_row.indisunique AS is_unique,
			index_row.indisvalid AS is_valid,
			index_row.indisready AS is_ready,
			(index_row.indpred IS NOT NULL) AS has_predicate,
			(index_row.indexprs IS NOT NULL) AS has_expressions
		FROM pg_class AS table_row
		JOIN pg_namespace AS namespace_row
		  ON namespace_row.oid = table_row.relnamespace
		JOIN pg_index AS index_row
		  ON index_row.indrelid = table_row.oid
		JOIN pg_class AS index_class
		  ON index_class.oid = index_row.indexrelid
		CROSS JOIN LATERAL unnest(index_row.indkey)
		  WITH ORDINALITY AS index_key(attribute_number, ordinality)
		LEFT JOIN pg_attribute AS attribute
		  ON attribute.attrelid = table_row.oid
		 AND attribute.attnum = index_key.attribute_number
		WHERE namespace_row.nspname = CURRENT_SCHEMA()
		  AND table_row.relname = 'idempotency_records'
		  AND index_class.relname = ?
		ORDER BY index_key.ordinality
	`, idempotencyScopeIndexName).Scan(&rows).Error; err != nil {
		return false, fmt.Errorf("inspect PostgreSQL idempotency uniqueness index: %w", err)
	}
	if len(rows) != len(idempotencyScopeIndexColumns) {
		return false, nil
	}
	for position, row := range rows {
		if row.Ordinal != position+1 ||
			row.KeyColumnCount != len(idempotencyScopeIndexColumns) ||
			!row.IsUnique ||
			!row.IsValid ||
			!row.IsReady ||
			row.HasPredicate ||
			row.HasExpressions ||
			row.ColumnName != idempotencyScopeIndexColumns[position] {
			return false, nil
		}
	}
	return true, nil
}

func validatePostgresIdempotencyScopeIndex(db *gorm.DB) error {
	valid, err := postgresIdempotencyScopeIndexIsValid(db)
	if err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("%s is missing or has a different definition", idempotencyScopeIndexName)
	}
	return nil
}

type sqliteIndexListRow struct {
	Name    string `gorm:"column:name"`
	Unique  int    `gorm:"column:unique"`
	Partial int    `gorm:"column:partial"`
}

type sqliteIndexColumn struct {
	Sequence int            `gorm:"column:seqno"`
	Name     sql.NullString `gorm:"column:name"`
}

func sqliteIdempotencyScopeIndexIsValid(db *gorm.DB) (bool, error) {
	var indexes []sqliteIndexListRow
	if err := db.Raw("PRAGMA index_list('idempotency_records')").
		Scan(&indexes).Error; err != nil {
		return false, fmt.Errorf("inspect SQLite idempotency index list: %w", err)
	}
	unique := false
	found := false
	for _, index := range indexes {
		if index.Name == idempotencyScopeIndexName {
			found = true
			unique = index.Unique == 1 && index.Partial == 0
			break
		}
	}
	if !found || !unique {
		return false, nil
	}

	var columns []sqliteIndexColumn
	if err := db.Raw(
		"PRAGMA index_info('" + idempotencyScopeIndexName + "')",
	).Scan(&columns).Error; err != nil {
		return false, fmt.Errorf("inspect SQLite idempotency index columns: %w", err)
	}
	if len(columns) != len(idempotencyScopeIndexColumns) {
		return false, nil
	}
	for position, column := range columns {
		if column.Sequence != position ||
			!column.Name.Valid ||
			column.Name.String != idempotencyScopeIndexColumns[position] {
			return false, nil
		}
	}
	return true, nil
}

func validateSQLiteIdempotencyScopeIndex(db *gorm.DB) error {
	valid, err := sqliteIdempotencyScopeIndexIsValid(db)
	if err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("%s is missing or has a different definition", idempotencyScopeIndexName)
	}
	return nil
}
