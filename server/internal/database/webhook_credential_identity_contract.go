package database

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"
)

type webhookCredentialIdentityColumnContract struct {
	table            string
	column           string
	postgresDataType string
	postgresUDT      string
	sqliteType       string
	characterLength  *int64
	primaryKey       bool
	textComparison   bool
}

func webhookCredentialIdentityColumnContracts() []webhookCredentialIdentityColumnContract {
	varchar36 := int64(36)
	varchar64 := int64(64)
	varchar50 := int64(50)
	varchar128 := int64(128)
	return []webhookCredentialIdentityColumnContract{
		{
			table:            "domain_events",
			column:           "organization_id",
			postgresDataType: "bigint",
			postgresUDT:      "int8",
			sqliteType:       "INTEGER",
		},
		{
			table:            "domain_events",
			column:           "project_id",
			postgresDataType: "bigint",
			postgresUDT:      "int8",
			sqliteType:       "INTEGER",
		},
		{
			table:            "domain_events",
			column:           "id",
			postgresDataType: "character varying",
			postgresUDT:      "varchar",
			sqliteType:       "TEXT",
			characterLength:  &varchar36,
			primaryKey:       true,
			textComparison:   true,
		},
		{
			table:            "webhook_delivery_snapshots",
			column:           "organization_id",
			postgresDataType: "bigint",
			postgresUDT:      "int8",
			sqliteType:       "INTEGER",
		},
		{
			table:            "webhook_delivery_snapshots",
			column:           "project_id",
			postgresDataType: "bigint",
			postgresUDT:      "int8",
			sqliteType:       "INTEGER",
		},
		{
			table:            "webhook_delivery_snapshots",
			column:           "id",
			postgresDataType: "character varying",
			postgresUDT:      "varchar",
			sqliteType:       "TEXT",
			characterLength:  &varchar36,
			primaryKey:       true,
			textComparison:   true,
		},
		{
			table:            "webhook_delivery_snapshots",
			column:           "event_id",
			postgresDataType: "character varying",
			postgresUDT:      "varchar",
			sqliteType:       "TEXT",
			characterLength:  &varchar64,
			textComparison:   true,
		},
		{
			table:            "outbox_deliveries",
			column:           "organization_id",
			postgresDataType: "bigint",
			postgresUDT:      "int8",
			sqliteType:       "INTEGER",
		},
		{
			table:            "outbox_deliveries",
			column:           "project_id",
			postgresDataType: "bigint",
			postgresUDT:      "int8",
			sqliteType:       "INTEGER",
		},
		{
			table:            "outbox_deliveries",
			column:           "id",
			postgresDataType: "character varying",
			postgresUDT:      "varchar",
			sqliteType:       "TEXT",
			characterLength:  &varchar36,
			primaryKey:       true,
			textComparison:   true,
		},
		{
			table:            "outbox_deliveries",
			column:           "event_id",
			postgresDataType: "character varying",
			postgresUDT:      "varchar",
			sqliteType:       "TEXT",
			characterLength:  &varchar36,
			textComparison:   true,
		},
		{
			table:            "outbox_deliveries",
			column:           "destination_type",
			postgresDataType: "character varying",
			postgresUDT:      "varchar",
			sqliteType:       "TEXT",
			characterLength:  &varchar50,
			textComparison:   true,
		},
		{
			table:            "outbox_deliveries",
			column:           "destination_id",
			postgresDataType: "character varying",
			postgresUDT:      "varchar",
			sqliteType:       "TEXT",
			characterLength:  &varchar128,
			textComparison:   true,
		},
	}
}

func validateWebhookCredentialIdentityColumnContract(db *gorm.DB) error {
	return validateWebhookCredentialIdentityColumnContractState(db, false)
}

func validateWebhookCredentialIdentityColumnContractState(
	db *gorm.DB,
	allowNullable bool,
) error {
	if db == nil {
		return errors.New(
			"webhook credential identity column contract database is required",
		)
	}
	switch db.Dialector.Name() {
	case "postgres":
		return validatePostgresWebhookCredentialIdentityColumnContract(
			db,
			allowNullable,
		)
	case "sqlite":
		return validateSQLiteWebhookCredentialIdentityColumnContract(
			db,
			allowNullable,
		)
	default:
		return fmt.Errorf(
			"webhook credential identity column contract is unsupported for database dialect %q",
			db.Dialector.Name(),
		)
	}
}

func validatePostgresWebhookCredentialIdentityColumnContract(
	db *gorm.DB,
	allowNullable bool,
) error {
	type columnState struct {
		TableName       string  `gorm:"column:table_name"`
		ColumnName      string  `gorm:"column:column_name"`
		DataType        string  `gorm:"column:data_type"`
		UDTName         string  `gorm:"column:udt_name"`
		IsNullable      string  `gorm:"column:is_nullable"`
		ColumnDefault   *string `gorm:"column:column_default"`
		CharacterLength *int64  `gorm:"column:character_maximum_length"`
		CollationName   *string `gorm:"column:collation_name"`
		IsGenerated     string  `gorm:"column:is_generated"`
		IsIdentity      string  `gorm:"column:is_identity"`
	}
	var columns []columnState
	if err := db.Raw(`
		SELECT
			table_name,
			column_name,
			data_type,
			udt_name,
			is_nullable,
			column_default,
			character_maximum_length,
			collation_name,
			is_generated,
			is_identity
		FROM information_schema.columns
		WHERE table_schema = CURRENT_SCHEMA()
		  AND (
			(table_name = 'domain_events' AND column_name IN (
				'id', 'organization_id', 'project_id'
			))
			OR
			(table_name = 'webhook_delivery_snapshots' AND column_name IN (
				'id', 'organization_id', 'project_id', 'event_id'
			))
			OR
			(table_name = 'outbox_deliveries' AND column_name IN (
				'id', 'organization_id', 'project_id', 'event_id',
				'destination_type', 'destination_id'
			))
		  )
		ORDER BY table_name, column_name
	`).Scan(&columns).Error; err != nil {
		return fmt.Errorf(
			"read PostgreSQL webhook credential identity columns: %w",
			err,
		)
	}
	byKey := make(map[string]columnState, len(columns))
	for _, column := range columns {
		byKey[column.TableName+"."+column.ColumnName] = column
	}
	for _, contract := range webhookCredentialIdentityColumnContracts() {
		key := contract.table + "." + contract.column
		column, exists := byKey[key]
		if !exists {
			return fmt.Errorf("%s is missing", key)
		}
		if column.DataType != contract.postgresDataType ||
			column.UDTName != contract.postgresUDT ||
			(!allowNullable && column.IsNullable != "NO") ||
			column.ColumnDefault != nil ||
			!equalOptionalInt64(
				column.CharacterLength,
				contract.characterLength,
			) ||
			(contract.textComparison && column.CollationName != nil) ||
			column.IsGenerated != "NEVER" ||
			column.IsIdentity != "NO" {
			return fmt.Errorf(
				"%s has incompatible PostgreSQL identity type/null/default/length/collation contract",
				key,
			)
		}
	}
	return validatePostgresWebhookCredentialPrimaryKeys(db)
}

func validatePostgresWebhookCredentialPrimaryKeys(db *gorm.DB) error {
	type primaryKeyRow struct {
		TableName      string `gorm:"column:table_name"`
		ConstraintOID  uint32 `gorm:"column:constraint_oid"`
		ColumnName     string `gorm:"column:column_name"`
		Ordinality     int    `gorm:"column:ordinality"`
		ConstraintType string `gorm:"column:constraint_type"`
	}
	var rows []primaryKeyRow
	if err := db.Raw(`
		SELECT
			table_state.relname AS table_name,
			constraint_state.oid::oid AS constraint_oid,
			attribute.attname AS column_name,
			key.ordinality::integer AS ordinality,
			constraint_state.contype::text AS constraint_type
		FROM pg_constraint AS constraint_state
		JOIN pg_class AS table_state
		  ON table_state.oid = constraint_state.conrelid
		JOIN pg_namespace AS namespace
		  ON namespace.oid = table_state.relnamespace
		JOIN LATERAL unnest(constraint_state.conkey)
		  WITH ORDINALITY AS key(attnum, ordinality) ON TRUE
		JOIN pg_attribute AS attribute
		  ON attribute.attrelid = constraint_state.conrelid
		 AND attribute.attnum = key.attnum
		 AND NOT attribute.attisdropped
		WHERE namespace.nspname = CURRENT_SCHEMA()
		  AND table_state.relname IN (
			'domain_events',
			'webhook_delivery_snapshots',
			'outbox_deliveries'
		  )
		  AND constraint_state.contype = 'p'
		ORDER BY table_state.relname, constraint_state.oid, key.ordinality
	`).Scan(&rows).Error; err != nil {
		return fmt.Errorf(
			"read PostgreSQL webhook credential primary keys: %w",
			err,
		)
	}
	byTable := make(map[string][]primaryKeyRow, 3)
	for _, row := range rows {
		byTable[row.TableName] = append(byTable[row.TableName], row)
	}
	for _, table := range []string{
		"domain_events",
		"webhook_delivery_snapshots",
		"outbox_deliveries",
	} {
		keys := byTable[table]
		if len(keys) != 1 ||
			keys[0].ConstraintOID == 0 ||
			keys[0].ConstraintType != "p" ||
			keys[0].ColumnName != "id" ||
			keys[0].Ordinality != 1 {
			return fmt.Errorf(
				"%s has incompatible PostgreSQL primary key semantics",
				table,
			)
		}
	}
	return nil
}

func validateSQLiteWebhookCredentialIdentityColumnContract(
	db *gorm.DB,
	allowNullable bool,
) error {
	type columnState struct {
		Name       string  `gorm:"column:name"`
		Type       string  `gorm:"column:type"`
		NotNull    int     `gorm:"column:notnull"`
		Default    *string `gorm:"column:dflt_value"`
		PrimaryKey int     `gorm:"column:pk"`
		HiddenFlag int     `gorm:"column:hidden"`
	}
	byTable := make(map[string]map[string]columnState, 3)
	for _, table := range []string{
		"domain_events",
		"webhook_delivery_snapshots",
		"outbox_deliveries",
	} {
		var rows []columnState
		if err := db.Raw(
			"PRAGMA table_xinfo(" +
				quoteAutomationWebhookSQLiteIdentifier(table) + ")",
		).Scan(&rows).Error; err != nil {
			return fmt.Errorf(
				"read SQLite %s identity columns: %w",
				table,
				err,
			)
		}
		byTable[table] = make(map[string]columnState, len(rows))
		primaryKeyColumns := make([]columnState, 0, 1)
		for _, row := range rows {
			byTable[table][row.Name] = row
			if row.PrimaryKey > 0 {
				primaryKeyColumns = append(primaryKeyColumns, row)
			}
		}
		if len(primaryKeyColumns) != 1 ||
			primaryKeyColumns[0].Name != "id" ||
			primaryKeyColumns[0].PrimaryKey != 1 {
			return fmt.Errorf(
				"%s has incompatible SQLite primary key semantics",
				table,
			)
		}
	}
	for _, contract := range webhookCredentialIdentityColumnContracts() {
		key := contract.table + "." + contract.column
		column, exists := byTable[contract.table][contract.column]
		if !exists {
			return fmt.Errorf("%s is missing", key)
		}
		wantPrimaryKey := 0
		if contract.primaryKey {
			wantPrimaryKey = 1
		}
		if strings.ToUpper(strings.TrimSpace(column.Type)) !=
			contract.sqliteType ||
			(!allowNullable && column.NotNull != 1) ||
			column.Default != nil ||
			column.PrimaryKey != wantPrimaryKey ||
			column.HiddenFlag != 0 {
			return fmt.Errorf(
				"%s has incompatible SQLite identity type/null/default/primary-key contract",
				key,
			)
		}
		if contract.textComparison {
			binary, err := sqliteColumnUsesDefaultBinaryCollation(
				db,
				contract.table,
				contract.column,
			)
			if err != nil {
				return err
			}
			if !binary {
				return fmt.Errorf(
					"%s has incompatible SQLite collation contract",
					key,
				)
			}
		}
	}
	return nil
}

func validateWebhookCredentialIdentityNullSet(db *gorm.DB) error {
	if db == nil {
		return errors.New(
			"webhook credential identity NULL audit database is required",
		)
	}
	type violation struct {
		TableName  string `gorm:"column:table_name"`
		ColumnName string `gorm:"column:column_name"`
		ObjectID   string `gorm:"column:object_id"`
	}
	var found violation
	if err := db.Raw(`
		SELECT table_name, column_name, object_id
		FROM (
			SELECT 'domain_events' AS table_name,
				'id' AS column_name,
				'<NULL>' AS object_id
			FROM domain_events WHERE id IS NULL
			UNION ALL
			SELECT 'domain_events', 'organization_id',
				COALESCE(CAST(id AS TEXT), '<NULL>')
			FROM domain_events WHERE organization_id IS NULL
			UNION ALL
			SELECT 'domain_events', 'project_id',
				COALESCE(CAST(id AS TEXT), '<NULL>')
			FROM domain_events WHERE project_id IS NULL
			UNION ALL
			SELECT 'webhook_delivery_snapshots', 'id', '<NULL>'
			FROM webhook_delivery_snapshots WHERE id IS NULL
			UNION ALL
			SELECT 'webhook_delivery_snapshots', 'organization_id',
				COALESCE(CAST(id AS TEXT), '<NULL>')
			FROM webhook_delivery_snapshots WHERE organization_id IS NULL
			UNION ALL
			SELECT 'webhook_delivery_snapshots', 'project_id',
				COALESCE(CAST(id AS TEXT), '<NULL>')
			FROM webhook_delivery_snapshots WHERE project_id IS NULL
			UNION ALL
			SELECT 'webhook_delivery_snapshots', 'event_id',
				COALESCE(CAST(id AS TEXT), '<NULL>')
			FROM webhook_delivery_snapshots WHERE event_id IS NULL
			UNION ALL
			SELECT 'outbox_deliveries', 'id', '<NULL>'
			FROM outbox_deliveries WHERE id IS NULL
			UNION ALL
			SELECT 'outbox_deliveries', 'organization_id',
				COALESCE(CAST(id AS TEXT), '<NULL>')
			FROM outbox_deliveries WHERE organization_id IS NULL
			UNION ALL
			SELECT 'outbox_deliveries', 'project_id',
				COALESCE(CAST(id AS TEXT), '<NULL>')
			FROM outbox_deliveries WHERE project_id IS NULL
			UNION ALL
			SELECT 'outbox_deliveries', 'event_id',
				COALESCE(CAST(id AS TEXT), '<NULL>')
			FROM outbox_deliveries WHERE event_id IS NULL
			UNION ALL
			SELECT 'outbox_deliveries', 'destination_type',
				COALESCE(CAST(id AS TEXT), '<NULL>')
			FROM outbox_deliveries WHERE destination_type IS NULL
			UNION ALL
			SELECT 'outbox_deliveries', 'destination_id',
				COALESCE(CAST(id AS TEXT), '<NULL>')
			FROM outbox_deliveries WHERE destination_id IS NULL
		) AS identity_nulls
		ORDER BY table_name, column_name, object_id
		LIMIT 1
	`).Scan(&found).Error; err != nil {
		return fmt.Errorf(
			"audit webhook credential identity NULL values: %w",
			err,
		)
	}
	if found.TableName != "" {
		return fmt.Errorf(
			"%s.%s contains NULL identity for row %s",
			found.TableName,
			found.ColumnName,
			found.ObjectID,
		)
	}
	return nil
}

func installWebhookCredentialIdentityNotNull(db *gorm.DB) error {
	if err := validateWebhookCredentialIdentityNullSet(db); err != nil {
		return err
	}
	if err := validateWebhookCredentialIdentityColumnContractState(
		db,
		true,
	); err != nil {
		return err
	}
	switch db.Dialector.Name() {
	case "postgres":
		for _, contract := range webhookCredentialIdentityColumnContracts() {
			if err := db.Exec(
				"ALTER TABLE " + contract.table +
					" ALTER COLUMN " + contract.column + " SET NOT NULL",
			).Error; err != nil {
				return fmt.Errorf(
					"set PostgreSQL %s.%s identity NOT NULL: %w",
					contract.table,
					contract.column,
					err,
				)
			}
		}
	case "sqlite":
		if err := rebuildSQLiteWebhookCredentialIdentityTables(db); err != nil {
			return err
		}
	default:
		return fmt.Errorf(
			"webhook credential identity NOT NULL installation is unsupported for database dialect %q",
			db.Dialector.Name(),
		)
	}
	return validateWebhookCredentialIdentityColumnContract(db)
}

func rebuildSQLiteWebhookCredentialIdentityTables(db *gorm.DB) error {
	tables := []string{
		"domain_events",
		"webhook_delivery_snapshots",
		"outbox_deliveries",
	}
	byTable := make(map[string][]webhookCredentialIdentityColumnContract, 3)
	for _, contract := range webhookCredentialIdentityColumnContracts() {
		byTable[contract.table] = append(byTable[contract.table], contract)
	}
	for _, table := range tables {
		var tableState sqliteSchemaObject
		if err := db.Raw(`
			SELECT type, name, sql
			FROM sqlite_master
			WHERE type = 'table' AND name = ?
		`, table).Take(&tableState).Error; err != nil {
			return fmt.Errorf(
				"read SQLite %s for identity rebuild: %w",
				table,
				err,
			)
		}
		open, err := findSQLiteTableBodyOpen(tableState.SQL)
		if err != nil {
			return err
		}
		close, ok := matchingSQLParenthesis(tableState.SQL, open)
		if !ok {
			return fmt.Errorf("SQLite table %s has malformed DDL", table)
		}
		parts, err := splitSQLiteTableBody(tableState.SQL[open+1 : close])
		if err != nil {
			return err
		}
		var columnStates []struct {
			Name    string `gorm:"column:name"`
			NotNull int    `gorm:"column:notnull"`
		}
		if err := db.Raw(
			"PRAGMA table_xinfo(" +
				quoteAutomationWebhookSQLiteIdentifier(table) + ")",
		).Scan(&columnStates).Error; err != nil {
			return err
		}
		notNullByColumn := make(map[string]bool, len(columnStates))
		for _, state := range columnStates {
			notNullByColumn[state.Name] = state.NotNull == 1
		}
		required := make(map[string]struct{}, len(byTable[table]))
		for _, contract := range byTable[table] {
			required[contract.column] = struct{}{}
		}
		changed := false
		for index, part := range parts {
			column := sqliteDDLLeadingIdentifier(part)
			if _, requiredColumn := required[column]; !requiredColumn {
				continue
			}
			delete(required, column)
			if !notNullByColumn[column] {
				parts[index] = strings.TrimSpace(part) + " NOT NULL"
				changed = true
			}
		}
		if len(required) != 0 {
			missing := make([]string, 0, len(required))
			for column := range required {
				missing = append(missing, column)
			}
			sort.Strings(missing)
			return fmt.Errorf(
				"SQLite %s identity columns are missing: %s",
				table,
				strings.Join(missing, ", "),
			)
		}
		if !changed {
			continue
		}
		tempTable := table + "__identity_contract"
		createSQL := "CREATE TABLE " +
			quoteAutomationWebhookSQLiteIdentifier(tempTable) +
			" (" + strings.Join(parts, ", ") + ")" +
			tableState.SQL[close+1:]
		if err := rebuildSQLiteTableFromDDL(
			db,
			table,
			tempTable,
			createSQL,
			"webhook credential identity NOT NULL",
		); err != nil {
			return err
		}
	}
	return nil
}
