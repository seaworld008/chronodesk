package database

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

type automationWebhookIndexColumn struct {
	name       string
	descending bool
}

type automationWebhookIndexDefinition struct {
	name              string
	table             string
	unique            bool
	columns           []automationWebhookIndexColumn
	where             string
	postgresPredicate string
}

var automationWebhookPaginationIndexes = []automationWebhookIndexDefinition{
	{
		name:  "idx_automation_rules_directory",
		table: "automation_rules",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "deleted_at"},
			{name: "priority"},
			{name: "created_at", descending: true},
			{name: "id", descending: true},
		},
	},
	{
		name:  "idx_automation_logs_timeline",
		table: "automation_logs",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "executed_at", descending: true},
			{name: "id", descending: true},
		},
	},
	{
		name:  "idx_automation_logs_rule_timeline",
		table: "automation_logs",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "rule_id"},
			{name: "executed_at", descending: true},
			{name: "id", descending: true},
		},
	},
	{
		name:  "idx_automation_logs_ticket_timeline",
		table: "automation_logs",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "ticket_id"},
			{name: "executed_at", descending: true},
			{name: "id", descending: true},
		},
	},
	{
		name:  "idx_automation_logs_success_timeline",
		table: "automation_logs",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "success"},
			{name: "executed_at", descending: true},
			{name: "id", descending: true},
		},
	},
	{
		name:  "idx_webhook_configs_directory",
		table: "webhook_configs",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "deleted_at"},
			{name: "created_at", descending: true},
			{name: "id", descending: true},
		},
	},
	{
		name:  "idx_webhook_logs_timeline",
		table: "webhook_logs",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "config_id"},
			{name: "created_at", descending: true},
			{name: "id", descending: true},
		},
	},
	{
		name:  "idx_webhook_logs_status_timeline",
		table: "webhook_logs",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "config_id"},
			{name: "status"},
			{name: "created_at", descending: true},
			{name: "id", descending: true},
		},
	},
	{
		name:  "idx_webhook_logs_event_timeline",
		table: "webhook_logs",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "config_id"},
			{name: "event_type"},
			{name: "created_at", descending: true},
			{name: "id", descending: true},
		},
	},
	{
		name:  "idx_sla_configs_scope_directory",
		table: "sla_configs",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "is_default", descending: true},
			{name: "created_at", descending: true},
			{name: "id", descending: true},
		},
	},
	{
		name:  "idx_ticket_templates_scope_directory",
		table: "ticket_templates",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "created_at", descending: true},
			{name: "id", descending: true},
		},
	},
	{
		name:  "idx_quick_replies_scope_directory",
		table: "quick_replies",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "created_at", descending: true},
			{name: "id", descending: true},
		},
	},
	{
		name:  "idx_agent_runs_scope_timeline",
		table: "agent_runs",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "created_at", descending: true},
			{name: "id", descending: true},
		},
	},
	{
		name:  "idx_action_proposals_scope_timeline",
		table: "action_proposals",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "created_at", descending: true},
			{name: "id", descending: true},
		},
	},
	{
		name:  "idx_approval_tasks_scope_timeline",
		table: "approval_tasks",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "created_at", descending: true},
			{name: "id", descending: true},
		},
	},
	{
		name:  "idx_handoffs_scope_timeline",
		table: "handoffs",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "created_at", descending: true},
			{name: "id", descending: true},
		},
	},
	{
		name:  "idx_knowledge_articles_scope_directory",
		table: "knowledge_articles",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "updated_at", descending: true},
			{name: "id", descending: true},
		},
	},
	{
		name:  "idx_knowledge_versions_article_directory",
		table: "knowledge_article_versions",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "article_id"},
			{name: "version", descending: true},
			{name: "id", descending: true},
		},
	},
	{
		name:  "idx_knowledge_versions_draft_activity",
		table: "knowledge_article_versions",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "article_id"},
			{name: "status"},
			{name: "created_at", descending: true},
			{name: "id", descending: true},
		},
	},
	{
		name:  "idx_knowledge_ingestions_scope_directory",
		table: "knowledge_ingestion_tasks",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "created_at", descending: true},
			{name: "id", descending: true},
		},
	},
}

// MigrateAutomationWebhookPaginationIndexes installs the stable pagination
// indexes independently of the resumable model scan. Existing same-named
// indexes are retained only when their complete physical definition matches;
// interrupted, partial, expression, wrong-order, or wrong-direction indexes
// are rebuilt transactionally.
func MigrateAutomationWebhookPaginationIndexes(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if err := requireAutomationWebhookIndexColumns(db); err != nil {
		return err
	}

	switch db.Dialector.Name() {
	case "postgres":
		return migratePostgresAutomationWebhookPaginationIndexes(db)
	case "sqlite":
		return migrateSQLiteAutomationWebhookPaginationIndexes(db)
	default:
		return fmt.Errorf(
			"automation and webhook pagination index migration is unsupported for database dialect %q",
			db.Dialector.Name(),
		)
	}
}

func requireAutomationWebhookIndexColumns(db *gorm.DB) error {
	seenTables := make(map[string]struct{})
	for _, definition := range automationWebhookPaginationIndexes {
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

func migratePostgresAutomationWebhookPaginationIndexes(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		schema, err := currentPostgresAutomationWebhookIndexSchema(tx)
		if err != nil {
			return err
		}

		lockedTables := make(map[string]struct{})
		for _, definition := range automationWebhookPaginationIndexes {
			if _, locked := lockedTables[definition.table]; locked {
				continue
			}
			qualifiedTable := quoteAutomationWebhookPostgresIdentifier(schema) +
				"." + quoteAutomationWebhookPostgresIdentifier(definition.table)
			if err := tx.Exec(
				"LOCK TABLE " + qualifiedTable + " IN SHARE ROW EXCLUSIVE MODE",
			).Error; err != nil {
				return fmt.Errorf(
					"lock %s for pagination index migration: %w",
					definition.table,
					err,
				)
			}
			lockedTables[definition.table] = struct{}{}
		}

		for _, definition := range automationWebhookPaginationIndexes {
			valid, err := postgresAutomationWebhookIndexIsValid(tx, definition)
			if err != nil {
				return err
			}
			if valid {
				continue
			}
			qualifiedIndex := quoteAutomationWebhookPostgresIdentifier(schema) +
				"." + quoteAutomationWebhookPostgresIdentifier(definition.name)
			if err := tx.Exec("DROP INDEX IF EXISTS " + qualifiedIndex).Error; err != nil {
				return fmt.Errorf(
					"drop incompatible pagination index %s: %w",
					definition.name,
					err,
				)
			}
			if err := tx.Exec(postgresAutomationWebhookIndexDDL(
				schema,
				definition,
			)).Error; err != nil {
				return fmt.Errorf(
					"create pagination index %s: %w",
					definition.name,
					err,
				)
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

func currentPostgresAutomationWebhookIndexSchema(db *gorm.DB) (string, error) {
	var schema string
	if err := db.Raw("SELECT CURRENT_SCHEMA()").Scan(&schema).Error; err != nil {
		return "", fmt.Errorf(
			"resolve PostgreSQL automation and webhook index schema: %w",
			err,
		)
	}
	if strings.TrimSpace(schema) == "" {
		return "", errors.New(
			"PostgreSQL automation and webhook index schema is required",
		)
	}
	return schema, nil
}

func postgresAutomationWebhookIndexDDL(
	schema string,
	definition automationWebhookIndexDefinition,
) string {
	unique := ""
	if definition.unique {
		unique = "UNIQUE "
	}
	statement := "CREATE " + unique + "INDEX " +
		quoteAutomationWebhookPostgresIdentifier(definition.name) +
		" ON " +
		quoteAutomationWebhookPostgresIdentifier(schema) + "." +
		quoteAutomationWebhookPostgresIdentifier(definition.table) +
		" (" + automationWebhookIndexColumnSQL(
		definition,
		quoteAutomationWebhookPostgresIdentifier,
	) + ")"
	if definition.where != "" {
		statement += " WHERE " + definition.where
	}
	return statement
}

func quoteAutomationWebhookPostgresIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func migrateSQLiteAutomationWebhookPaginationIndexes(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		for _, definition := range automationWebhookPaginationIndexes {
			valid, err := sqliteAutomationWebhookIndexIsValid(tx, definition)
			if err != nil {
				return err
			}
			if valid {
				continue
			}
			if err := tx.Exec(
				"DROP INDEX IF EXISTS " +
					quoteAutomationWebhookSQLiteIdentifier(definition.name),
			).Error; err != nil {
				return fmt.Errorf(
					"drop incompatible pagination index %s: %w",
					definition.name,
					err,
				)
			}
			if err := tx.Exec(sqliteAutomationWebhookIndexDDL(definition)).Error; err != nil {
				return fmt.Errorf(
					"create pagination index %s: %w",
					definition.name,
					err,
				)
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

func sqliteAutomationWebhookIndexDDL(
	definition automationWebhookIndexDefinition,
) string {
	unique := ""
	if definition.unique {
		unique = "UNIQUE "
	}
	statement := "CREATE " + unique + "INDEX " +
		quoteAutomationWebhookSQLiteIdentifier(definition.name) +
		" ON " +
		quoteAutomationWebhookSQLiteIdentifier(definition.table) +
		" (" + automationWebhookIndexColumnSQL(
		definition,
		quoteAutomationWebhookSQLiteIdentifier,
	) + ")"
	if definition.where != "" {
		statement += " WHERE " + definition.where
	}
	return statement
}

func quoteAutomationWebhookSQLiteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func automationWebhookIndexColumnSQL(
	definition automationWebhookIndexDefinition,
	quote func(string) string,
) string {
	columns := make([]string, 0, len(definition.columns))
	for _, column := range definition.columns {
		sqlColumn := quote(column.name)
		if column.descending {
			sqlColumn += " DESC"
		}
		columns = append(columns, sqlColumn)
	}
	return strings.Join(columns, ", ")
}

// ValidateAutomationWebhookPaginationIndexes is the read-only runtime gate for
// the exact physical index definitions used by stable keyset pagination.
func ValidateAutomationWebhookPaginationIndexes(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}

	for _, definition := range automationWebhookPaginationIndexes {
		var (
			valid bool
			err   error
		)
		switch db.Dialector.Name() {
		case "postgres":
			valid, err = postgresAutomationWebhookIndexIsValid(db, definition)
		case "sqlite":
			valid, err = sqliteAutomationWebhookIndexIsValid(db, definition)
		default:
			return fmt.Errorf(
				"automation and webhook pagination index validation is unsupported for database dialect %q",
				db.Dialector.Name(),
			)
		}
		if err != nil {
			return err
		}
		if !valid {
			return fmt.Errorf(
				"pagination index %s on %s must be a valid, ready, unique=%t, non-partial, non-expression btree index on (%s) in exact order; run `go run ./cmd/migrate`",
				definition.name,
				definition.table,
				definition.unique,
				automationWebhookIndexContractDescription(definition),
			)
		}
	}
	return nil
}

func automationWebhookIndexContractDescription(
	definition automationWebhookIndexDefinition,
) string {
	columns := make([]string, 0, len(definition.columns))
	for _, column := range definition.columns {
		name := column.name
		if column.descending {
			name += " DESC"
		}
		columns = append(columns, name)
	}
	return strings.Join(columns, ", ")
}

type postgresAutomationWebhookIndexColumn struct {
	ColumnName      string `gorm:"column:column_name"`
	Ordinal         int    `gorm:"column:ordinal"`
	KeyColumnCount  int    `gorm:"column:key_column_count"`
	AttributeCount  int    `gorm:"column:attribute_count"`
	AccessMethod    string `gorm:"column:access_method"`
	IsUnique        bool   `gorm:"column:is_unique"`
	IsValid         bool   `gorm:"column:is_valid"`
	IsReady         bool   `gorm:"column:is_ready"`
	IsLive          bool   `gorm:"column:is_live"`
	HasPredicate    bool   `gorm:"column:has_predicate"`
	HasExpressions  bool   `gorm:"column:has_expressions"`
	IsDescending    bool   `gorm:"column:is_descending"`
	IsNullsFirst    bool   `gorm:"column:is_nulls_first"`
	DefaultOpclass  bool   `gorm:"column:default_opclass"`
	ColumnCollation bool   `gorm:"column:column_collation"`
	Predicate       string `gorm:"column:predicate"`
}

func postgresAutomationWebhookIndexIsValid(
	db *gorm.DB,
	definition automationWebhookIndexDefinition,
) (bool, error) {
	var rows []postgresAutomationWebhookIndexColumn
	if err := db.Raw(`
		SELECT
			COALESCE(attribute.attname, '') AS column_name,
			index_key.ordinality::integer AS ordinal,
			index_row.indnkeyatts::integer AS key_column_count,
			index_row.indnatts::integer AS attribute_count,
			access_method.amname AS access_method,
			index_row.indisunique AS is_unique,
			index_row.indisvalid AS is_valid,
			index_row.indisready AS is_ready,
			index_row.indislive AS is_live,
			(index_row.indpred IS NOT NULL) AS has_predicate,
			(index_row.indexprs IS NOT NULL) AS has_expressions,
			COALESCE(
				pg_get_expr(index_row.indpred, index_row.indrelid),
				''
			) AS predicate,
			((index_key.option_bits::integer & 1) = 1) AS is_descending,
			((index_key.option_bits::integer & 2) = 2) AS is_nulls_first,
			opclass_row.opcdefault AS default_opclass,
			(
				index_key.collation_oid = attribute.attcollation
			) AS column_collation
		FROM pg_class AS table_row
		JOIN pg_namespace AS namespace_row
		  ON namespace_row.oid = table_row.relnamespace
		JOIN pg_index AS index_row
		  ON index_row.indrelid = table_row.oid
		JOIN pg_class AS index_class
		  ON index_class.oid = index_row.indexrelid
		JOIN pg_am AS access_method
		  ON access_method.oid = index_class.relam
		CROSS JOIN LATERAL unnest(
			index_row.indkey::smallint[],
			index_row.indoption::smallint[],
			index_row.indclass::oid[],
			index_row.indcollation::oid[]
		) WITH ORDINALITY AS index_key(
			attribute_number,
			option_bits,
			opclass_oid,
			collation_oid,
			ordinality
		)
		LEFT JOIN pg_attribute AS attribute
		  ON attribute.attrelid = table_row.oid
		 AND attribute.attnum = index_key.attribute_number
		LEFT JOIN pg_opclass AS opclass_row
		  ON opclass_row.oid = index_key.opclass_oid
		WHERE namespace_row.nspname = CURRENT_SCHEMA()
		  AND table_row.relname = ?
		  AND index_class.relname = ?
		ORDER BY index_key.ordinality
	`, definition.table, definition.name).Scan(&rows).Error; err != nil {
		return false, fmt.Errorf(
			"inspect PostgreSQL pagination index %s: %w",
			definition.name,
			err,
		)
	}
	if len(rows) != len(definition.columns) {
		return false, nil
	}
	for position, row := range rows {
		expected := definition.columns[position]
		if row.ColumnName != expected.name ||
			row.Ordinal != position+1 ||
			row.KeyColumnCount != len(definition.columns) ||
			row.AttributeCount != len(definition.columns) ||
			row.AccessMethod != "btree" ||
			row.IsUnique != definition.unique ||
			!row.IsValid ||
			!row.IsReady ||
			!row.IsLive ||
			row.HasPredicate != (definition.where != "") ||
			row.HasExpressions ||
			row.IsDescending != expected.descending ||
			row.IsNullsFirst != expected.descending ||
			!row.DefaultOpclass ||
			!row.ColumnCollation ||
			row.Predicate != definition.postgresPredicate {
			return false, nil
		}
	}
	return true, nil
}

func validatePostgresAutomationWebhookIndex(
	db *gorm.DB,
	definition automationWebhookIndexDefinition,
) error {
	valid, err := postgresAutomationWebhookIndexIsValid(db, definition)
	if err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf(
			"PostgreSQL pagination index %s has an incompatible definition after migration",
			definition.name,
		)
	}
	return nil
}

type sqliteAutomationWebhookIndexListRow struct {
	Name    string `gorm:"column:name"`
	Unique  int    `gorm:"column:unique"`
	Origin  string `gorm:"column:origin"`
	Partial int    `gorm:"column:partial"`
}

type sqliteAutomationWebhookIndexColumn struct {
	Sequence   int            `gorm:"column:seqno"`
	ColumnID   int            `gorm:"column:cid"`
	ColumnName sql.NullString `gorm:"column:name"`
	Descending int            `gorm:"column:desc"`
	Collation  sql.NullString `gorm:"column:coll"`
	Key        int            `gorm:"column:key"`
}

func sqliteAutomationWebhookIndexIsValid(
	db *gorm.DB,
	definition automationWebhookIndexDefinition,
) (bool, error) {
	var indexes []sqliteAutomationWebhookIndexListRow
	if err := db.Raw(
		"PRAGMA main.index_list(" +
			quoteAutomationWebhookSQLiteIdentifier(definition.table) +
			")",
	).Scan(&indexes).Error; err != nil {
		return false, fmt.Errorf(
			"inspect SQLite index list for %s: %w",
			definition.table,
			err,
		)
	}

	found := false
	for _, index := range indexes {
		if index.Name != definition.name {
			continue
		}
		found = true
		if (index.Unique != 0) != definition.unique ||
			(index.Partial != 0) != (definition.where != "") {
			return false, nil
		}
		break
	}
	if !found {
		return false, nil
	}
	if definition.where != "" {
		var storedSQL sql.NullString
		if err := db.Raw(
			`SELECT sql
			 FROM main.sqlite_schema
			 WHERE type = 'index' AND name = ?`,
			definition.name,
		).Scan(&storedSQL).Error; err != nil {
			return false, fmt.Errorf(
				"inspect SQLite pagination index SQL %s: %w",
				definition.name,
				err,
			)
		}
		if !storedSQL.Valid {
			return false, nil
		}
		upperSQL := strings.ToUpper(storedSQL.String)
		whereAt := strings.LastIndex(upperSQL, " WHERE ")
		if whereAt < 0 ||
			canonicalSQLiteLifecycleFenceSQL(
				storedSQL.String[whereAt+len(" WHERE "):],
			) != canonicalSQLiteLifecycleFenceSQL(definition.where) {
			return false, nil
		}
	}

	var indexRows []sqliteAutomationWebhookIndexColumn
	if err := db.Raw(
		"PRAGMA main.index_xinfo(" +
			quoteAutomationWebhookSQLiteIdentifier(definition.name) +
			")",
	).Scan(&indexRows).Error; err != nil {
		return false, fmt.Errorf(
			"inspect SQLite pagination index %s: %w",
			definition.name,
			err,
		)
	}
	keyColumns := make([]sqliteAutomationWebhookIndexColumn, 0, len(indexRows))
	for _, row := range indexRows {
		if row.Key == 1 {
			keyColumns = append(keyColumns, row)
		}
	}
	if len(keyColumns) != len(definition.columns) {
		return false, nil
	}
	for position, row := range keyColumns {
		expected := definition.columns[position]
		if row.Sequence != position ||
			row.ColumnID < 0 ||
			!row.ColumnName.Valid ||
			row.ColumnName.String != expected.name ||
			!row.Collation.Valid ||
			!strings.EqualFold(row.Collation.String, "BINARY") ||
			(row.Descending == 1) != expected.descending {
			return false, nil
		}
	}
	return true, nil
}

func validateSQLiteAutomationWebhookIndex(
	db *gorm.DB,
	definition automationWebhookIndexDefinition,
) error {
	valid, err := sqliteAutomationWebhookIndexIsValid(db, definition)
	if err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf(
			"SQLite pagination index %s has an incompatible definition after migration",
			definition.name,
		)
	}
	return nil
}
