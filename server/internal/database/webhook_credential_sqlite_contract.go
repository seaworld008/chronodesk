package database

import (
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"
)

func installSQLiteWebhookCredentialConstraints(db *gorm.DB) error {
	if db == nil || db.Dialector.Name() != "sqlite" {
		return fmt.Errorf(
			"SQLite webhook credential constraint database is required",
		)
	}
	byTable := make(map[string][]string)
	for name, definition := range webhookCredentialConstraintDefinitions {
		byTable[definition.table] = append(byTable[definition.table], name)
	}
	tables := make([]string, 0, len(byTable))
	for table := range byTable {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		sort.Strings(byTable[table])
		if err := rebuildSQLiteWebhookCredentialConstraintTable(
			db,
			table,
			byTable[table],
		); err != nil {
			return err
		}
	}
	return nil
}

func rebuildSQLiteWebhookCredentialConstraintTable(
	db *gorm.DB,
	table string,
	constraintNames []string,
) error {
	var tableState sqliteSchemaObject
	if err := db.Raw(`
		SELECT type, name, sql
		FROM sqlite_master
		WHERE type = 'table' AND name = ?
	`, table).Take(&tableState).Error; err != nil {
		return fmt.Errorf(
			"read SQLite table %s for webhook credential constraints: %w",
			table,
			err,
		)
	}
	open := strings.Index(tableState.SQL, "(")
	close, ok := matchingSQLParenthesis(tableState.SQL, open)
	if !ok {
		return fmt.Errorf("SQLite table %s has malformed DDL", table)
	}
	bodyParts, err := splitSQLiteTableBody(tableState.SQL[open+1 : close])
	if err != nil {
		return fmt.Errorf("parse SQLite table %s DDL: %w", table, err)
	}
	changed := false
	if table == "webhook_delivery_snapshots" {
		foundDeadline := false
		for index, part := range bodyParts {
			if sqliteDDLLeadingIdentifier(part) != "credential_expires_at" {
				continue
			}
			foundDeadline = true
			lowerPart := strings.ToLower(strings.Join(strings.Fields(part), " "))
			if !strings.Contains(lowerPart, " not null") {
				bodyParts[index] = strings.TrimSpace(part) + " NOT NULL"
				changed = true
			}
		}
		if !foundDeadline {
			return fmt.Errorf(
				"SQLite webhook_delivery_snapshots.credential_expires_at is missing",
			)
		}
	}
	for _, name := range constraintNames {
		definition := webhookCredentialConstraintDefinitions[name]
		actual, exists := sqliteWebhookConstraintExpression(
			tableState.SQL,
			name,
		)
		if exists {
			got, parseErr := canonicalWebhookConstraintDefinition(actual)
			if parseErr != nil {
				return fmt.Errorf(
					"parse SQLite webhook credential constraint %s: %w",
					name,
					parseErr,
				)
			}
			want, parseErr := canonicalWebhookConstraintDefinition(
				definition.expression,
			)
			if parseErr != nil {
				return fmt.Errorf(
					"parse canonical webhook credential constraint %s: %w",
					name,
					parseErr,
				)
			}
			if got != want {
				return fmt.Errorf(
					"SQLite webhook credential constraint %s has an incompatible definition",
					name,
				)
			}
			continue
		}
		if countSQLiteNamedConstraint(tableState.SQL, name) != 0 {
			return fmt.Errorf(
				"SQLite webhook credential constraint %s is malformed or duplicated",
				name,
			)
		}
		bodyParts = append(
			bodyParts,
			"CONSTRAINT "+
				quoteAutomationWebhookSQLiteIdentifier(name)+
				" CHECK ("+definition.expression+")",
		)
		changed = true
	}
	if !changed {
		return nil
	}
	tempTable := table + "__webhook_credential_contract"
	createSQL := "CREATE TABLE " +
		quoteAutomationWebhookSQLiteIdentifier(tempTable) +
		" (" + strings.Join(bodyParts, ", ") + ")" +
		tableState.SQL[close+1:]
	return rebuildSQLiteTableFromDDL(
		db,
		table,
		tempTable,
		createSQL,
		"webhook credential constraints",
	)
}

func splitSQLiteTableBody(body string) ([]string, error) {
	var (
		parts     []string
		start     int
		depth     int
		quote     byte
		bracketed bool
	)
	for index := 0; index < len(body); index++ {
		current := body[index]
		if quote != 0 {
			if current == quote {
				if index+1 < len(body) && body[index+1] == quote {
					index++
					continue
				}
				quote = 0
			}
			continue
		}
		if bracketed {
			if current == ']' {
				bracketed = false
			}
			continue
		}
		switch current {
		case '\'', '"', '`':
			quote = current
		case '[':
			bracketed = true
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("unbalanced closing parenthesis")
			}
		case ',':
			if depth == 0 {
				part := strings.TrimSpace(body[start:index])
				if part == "" {
					return nil, fmt.Errorf("empty table body item")
				}
				parts = append(parts, part)
				start = index + 1
			}
		}
	}
	if quote != 0 || bracketed || depth != 0 {
		return nil, fmt.Errorf("unbalanced quoted text or parenthesis")
	}
	last := strings.TrimSpace(body[start:])
	if last == "" {
		return nil, fmt.Errorf("empty final table body item")
	}
	return append(parts, last), nil
}

func sqliteDDLLeadingIdentifier(part string) string {
	part = strings.TrimSpace(part)
	if part == "" {
		return ""
	}
	switch part[0] {
	case '"', '`':
		close := strings.IndexByte(part[1:], part[0])
		if close < 0 {
			return ""
		}
		return strings.ToLower(part[1 : close+1])
	case '[':
		close := strings.IndexByte(part[1:], ']')
		if close < 0 {
			return ""
		}
		return strings.ToLower(part[1 : close+1])
	default:
		end := strings.IndexAny(part, " \t\r\n")
		if end < 0 {
			end = len(part)
		}
		return strings.ToLower(part[:end])
	}
}

func rebuildSQLiteTableFromDDL(
	db *gorm.DB,
	table string,
	tempTable string,
	createSQL string,
	purpose string,
) error {
	type columnState struct {
		Name   string `gorm:"column:name"`
		Hidden int    `gorm:"column:hidden"`
	}
	var columnRows []columnState
	if err := db.Raw(
		"PRAGMA table_xinfo(" +
			quoteAutomationWebhookSQLiteIdentifier(table) + ")",
	).Scan(&columnRows).Error; err != nil {
		return fmt.Errorf("read SQLite %s columns: %w", table, err)
	}
	columns := make([]string, 0, len(columnRows))
	for _, column := range columnRows {
		if column.Hidden == 0 {
			columns = append(
				columns,
				quoteAutomationWebhookSQLiteIdentifier(column.Name),
			)
		}
	}
	if len(columns) == 0 {
		return fmt.Errorf("SQLite table %s has no copyable columns", table)
	}
	var schemaObjects []sqliteSchemaObject
	if err := db.Raw(`
		SELECT type, name, sql
		FROM sqlite_master
		WHERE tbl_name = ?
		  AND type IN ('index', 'trigger')
		  AND sql IS NOT NULL
		ORDER BY type, name
	`, table).Scan(&schemaObjects).Error; err != nil {
		return fmt.Errorf("read SQLite %s dependent schema: %w", table, err)
	}
	var externalTriggers []sqliteSchemaObject
	if err := db.Raw(`
		SELECT type, name, sql
		FROM sqlite_master
		WHERE type = 'trigger'
		  AND tbl_name <> ?
		  AND sql IS NOT NULL
		  AND instr(lower(sql), lower(?)) > 0
		ORDER BY name
	`, table, table).Scan(&externalTriggers).Error; err != nil {
		return fmt.Errorf(
			"read SQLite triggers that reference %s: %w",
			table,
			err,
		)
	}
	for _, trigger := range externalTriggers {
		if err := db.Exec(
			"DROP TRIGGER " +
				quoteAutomationWebhookSQLiteIdentifier(trigger.Name),
		).Error; err != nil {
			return fmt.Errorf(
				"temporarily drop SQLite trigger %s for %s rebuild: %w",
				trigger.Name,
				table,
				err,
			)
		}
	}
	statements := []string{
		"DROP TABLE IF EXISTS " +
			quoteAutomationWebhookSQLiteIdentifier(tempTable),
		createSQL,
		"INSERT INTO " +
			quoteAutomationWebhookSQLiteIdentifier(tempTable) +
			" (" + strings.Join(columns, ", ") + ") SELECT " +
			strings.Join(columns, ", ") + " FROM " +
			quoteAutomationWebhookSQLiteIdentifier(table),
		"DROP TABLE " + quoteAutomationWebhookSQLiteIdentifier(table),
		"ALTER TABLE " +
			quoteAutomationWebhookSQLiteIdentifier(tempTable) +
			" RENAME TO " +
			quoteAutomationWebhookSQLiteIdentifier(table),
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("rebuild SQLite %s for %s: %w", table, purpose, err)
		}
	}
	for _, object := range schemaObjects {
		if err := db.Exec(object.SQL).Error; err != nil {
			return fmt.Errorf(
				"restore SQLite %s %s after %s rebuild: %w",
				object.Type,
				object.Name,
				purpose,
				err,
			)
		}
	}
	for _, trigger := range externalTriggers {
		if err := db.Exec(trigger.SQL).Error; err != nil {
			return fmt.Errorf(
				"restore SQLite trigger %s after %s rebuild: %w",
				trigger.Name,
				purpose,
				err,
			)
		}
	}
	return nil
}
