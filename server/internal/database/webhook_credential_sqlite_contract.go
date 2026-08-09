package database

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

type sqliteForeignKeyViolation struct {
	Table  string        `gorm:"column:table"`
	RowID  sql.NullInt64 `gorm:"column:rowid"`
	Parent string        `gorm:"column:parent"`
	FKID   int           `gorm:"column:fkid"`
}

var sqliteWebhookCredentialProtectedTables = []string{
	"projects",
	"domain_events",
	"webhook_delivery_snapshots",
	"outbox_deliveries",
	"schema_migration_checkpoints",
}

func requireSQLiteNoTempSchemaShadows(
	db *gorm.DB,
	names ...string,
) error {
	if db == nil {
		return errors.New("SQLite TEMP shadow database is required")
	}
	if len(names) == 0 {
		return nil
	}
	placeholders := make([]string, len(names))
	args := make([]any, len(names))
	for index, name := range names {
		placeholders[index] = "?"
		args[index] = name
	}
	var collision struct {
		Type string `gorm:"column:type"`
		Name string `gorm:"column:name"`
	}
	result := db.Raw(
		`SELECT type, name
		 FROM temp.sqlite_schema
		 WHERE name COLLATE NOCASE IN (`+
			strings.Join(placeholders, ", ")+`)
		 ORDER BY type, name
		 LIMIT 1`,
		args...,
	).Scan(&collision)
	if result.Error != nil {
		return fmt.Errorf(
			"inspect SQLite TEMP schema shadows: %w",
			result.Error,
		)
	}
	if result.RowsAffected != 0 {
		return fmt.Errorf(
			"SQLite TEMP schema shadow %s %s is not permitted",
			collision.Type,
			collision.Name,
		)
	}
	return nil
}

func firstSQLiteForeignKeyViolation(
	db *gorm.DB,
) (sqliteForeignKeyViolation, bool, error) {
	var violation sqliteForeignKeyViolation
	result := db.Raw(`
		SELECT "table", rowid, parent, fkid
		FROM pragma_foreign_key_check
		LIMIT 1
	`).Scan(&violation)
	if result.Error != nil {
		return sqliteForeignKeyViolation{}, false, result.Error
	}
	return violation, result.RowsAffected != 0, nil
}

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
		FROM main.sqlite_schema
		WHERE type = 'table' AND name = ?
	`, table).Take(&tableState).Error; err != nil {
		return fmt.Errorf(
			"read SQLite table %s for webhook credential constraints: %w",
			table,
			err,
		)
	}
	open, openErr := findSQLiteTableBodyOpen(tableState.SQL)
	if openErr != nil {
		return fmt.Errorf(
			"parse SQLite table %s opening DDL: %w",
			table,
			openErr,
		)
	}
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
				bodyParts[index] = appendSQLiteColumnConstraint(
					part,
					"NOT NULL",
				)
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
		actual, parseErr := sqliteNamedCheckConstraintExpression(
			tableState.SQL,
			name,
		)
		if parseErr == nil {
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
				if isLegacyWebhookCredentialConstraint(name, got) {
					replaced := false
					for index, part := range bodyParts {
						constraint, named, parseErr :=
							parseSQLiteTableConstraint(part)
						if parseErr != nil {
							return parseErr
						}
						if !named ||
							!strings.EqualFold(constraint.name, name) {
							continue
						}
						bodyParts[index] = "CONSTRAINT " +
							quoteAutomationWebhookSQLiteIdentifier(name) +
							" CHECK (" + definition.expression + ")"
						replaced = true
						changed = true
						break
					}
					if !replaced {
						return fmt.Errorf(
							"SQLite webhook credential constraint %s could not be upgraded",
							name,
						)
					}
					continue
				}
				return fmt.Errorf(
					"SQLite webhook credential constraint %s has an incompatible definition",
					name,
				)
			}
			continue
		}
		if !errors.Is(parseErr, errSQLiteNamedConstraintMissing) {
			return fmt.Errorf(
				"SQLite webhook credential constraint %s is malformed: %w",
				name,
				parseErr,
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
		" (" + joinSQLiteTableBody(bodyParts) + ")" +
		tableState.SQL[close+1:]
	return rebuildSQLiteTableFromDDL(
		db,
		table,
		tempTable,
		createSQL,
		"webhook credential constraints",
	)
}

func isLegacyWebhookSnapshotScopeConstraint(canonical string) bool {
	legacy, err := canonicalWebhookConstraintDefinition(
		"organization_id > 0 AND project_id > 0 AND event_id <> ''",
	)
	return err == nil && canonical == legacy
}

func isLegacyWebhookCredentialConstraint(
	name string,
	canonical string,
) bool {
	switch name {
	case "chk_webhook_snapshot_scope":
		return isLegacyWebhookSnapshotScopeConstraint(canonical)
	case "chk_projects_status":
		legacy, err := canonicalWebhookConstraintDefinition(
			closedVocabularyINConstraintExpression(
				"status",
				models.ProjectStatusValues(),
			),
		)
		return err == nil && canonical == legacy
	default:
		return false
	}
}

var errSQLiteNamedConstraintMissing = errors.New(
	"SQLite named constraint is missing",
)

type sqliteTableConstraintKind uint8

const (
	sqliteTableConstraintUnknown sqliteTableConstraintKind = iota
	sqliteTableConstraintCheck
	sqliteTableConstraintForeignKey
)

type sqliteParsedTableConstraint struct {
	name              string
	kind              sqliteTableConstraintKind
	checkExpression   string
	childColumns      []string
	parentTable       string
	parentColumns     []string
	onUpdate          string
	onDelete          string
	deferrable        bool
	deferrabilitySet  bool
	initiallyDeferred bool
}

func sqliteNamedCheckConstraintExpression(
	tableSQL string,
	name string,
) (string, error) {
	constraints, err := sqliteNamedTableConstraints(tableSQL, name)
	if err != nil {
		return "", err
	}
	if len(constraints) == 0 {
		return "", fmt.Errorf(
			"%w: %s",
			errSQLiteNamedConstraintMissing,
			name,
		)
	}
	if len(constraints) != 1 {
		return "", fmt.Errorf(
			"SQLite named constraint %s is duplicated",
			name,
		)
	}
	if constraints[0].kind != sqliteTableConstraintCheck {
		return "", fmt.Errorf(
			"SQLite named constraint %s is not a CHECK constraint",
			name,
		)
	}
	return constraints[0].checkExpression, nil
}

func sqliteNamedTableConstraints(
	tableSQL string,
	name string,
) ([]sqliteParsedTableConstraint, error) {
	open, err := findSQLiteTableBodyOpen(tableSQL)
	if err != nil {
		return nil, err
	}
	close, ok := matchingSQLParenthesis(tableSQL, open)
	if !ok {
		return nil, fmt.Errorf("SQLite table DDL is malformed")
	}
	parts, err := splitSQLiteTableBody(tableSQL[open+1 : close])
	if err != nil {
		return nil, err
	}
	matches := make([]sqliteParsedTableConstraint, 0, 1)
	for _, part := range parts {
		constraint, named, parseErr := parseSQLiteTableConstraint(part)
		if parseErr != nil {
			return nil, parseErr
		}
		if named && strings.EqualFold(constraint.name, name) {
			matches = append(matches, constraint)
		}
	}
	return matches, nil
}

func parseSQLiteTableConstraint(
	part string,
) (sqliteParsedTableConstraint, bool, error) {
	var result sqliteParsedTableConstraint
	index := 0
	keyword, _, next, ok := scanSQLiteDDLIdentifier(part, index)
	if !ok || !strings.EqualFold(keyword, "constraint") {
		return result, false, nil
	}
	index = next
	name, quoted, next, ok := scanSQLiteDDLIdentifier(part, index)
	if !ok {
		return result, false, fmt.Errorf(
			"SQLite table CONSTRAINT is missing its name",
		)
	}
	_ = quoted
	result.name = name
	index = next
	kind, _, next, ok := scanSQLiteDDLIdentifier(part, index)
	if !ok {
		return result, true, fmt.Errorf(
			"SQLite named constraint %s is missing its type",
			name,
		)
	}
	index = next
	switch {
	case strings.EqualFold(kind, "check"):
		result.kind = sqliteTableConstraintCheck
		var triviaOK bool
		index, triviaOK = skipSQLiteDDLTrivia(part, index)
		if !triviaOK {
			return result, true, fmt.Errorf(
				"SQLite CHECK constraint %s has unterminated comment",
				name,
			)
		}
		if index >= len(part) || part[index] != '(' {
			return result, true, fmt.Errorf(
				"SQLite CHECK constraint %s is missing its expression",
				name,
			)
		}
		close, balanced := matchingSQLParenthesis(part, index)
		trailing, trailingOK := skipSQLiteDDLTrivia(part, close+1)
		if !balanced || !trailingOK || trailing != len(part) {
			return result, true, fmt.Errorf(
				"SQLite CHECK constraint %s has malformed trailing SQL",
				name,
			)
		}
		result.checkExpression = part[index+1 : close]
	case strings.EqualFold(kind, "foreign"):
		result.kind = sqliteTableConstraintForeignKey
		key, _, next, ok := scanSQLiteDDLIdentifier(part, index)
		if !ok || !strings.EqualFold(key, "key") {
			return result, true, fmt.Errorf(
				"SQLite FOREIGN KEY constraint %s is malformed",
				name,
			)
		}
		index = next
		var parseErr error
		result.childColumns, index, parseErr =
			parseSQLiteDDLIdentifierList(part, index)
		if parseErr != nil {
			return result, true, fmt.Errorf(
				"parse SQLite FOREIGN KEY %s child columns: %w",
				name,
				parseErr,
			)
		}
		references, _, next, ok :=
			scanSQLiteDDLIdentifier(part, index)
		if !ok || !strings.EqualFold(references, "references") {
			return result, true, fmt.Errorf(
				"SQLite FOREIGN KEY constraint %s is missing REFERENCES",
				name,
			)
		}
		index = next
		result.parentTable, _, index, ok =
			scanSQLiteDDLIdentifier(part, index)
		if !ok {
			return result, true, fmt.Errorf(
				"SQLite FOREIGN KEY constraint %s is missing its parent table",
				name,
			)
		}
		result.parentColumns, index, parseErr =
			parseSQLiteDDLIdentifierList(part, index)
		if parseErr != nil {
			return result, true, fmt.Errorf(
				"parse SQLite FOREIGN KEY %s parent columns: %w",
				name,
				parseErr,
			)
		}
		for {
			nextKeyword, _, afterKeyword, present :=
				scanSQLiteDDLIdentifier(part, index)
			if !present {
				if strings.TrimSpace(part[index:]) != "" {
					return result, true, fmt.Errorf(
						"SQLite FOREIGN KEY constraint %s has malformed trailing SQL",
						name,
					)
				}
				break
			}
			switch {
			case strings.EqualFold(nextKeyword, "on"):
				actionKind, _, afterKind, actionPresent :=
					scanSQLiteDDLIdentifier(part, afterKeyword)
				if !actionPresent ||
					(!strings.EqualFold(actionKind, "update") &&
						!strings.EqualFold(actionKind, "delete")) {
					return result, true, fmt.Errorf(
						"SQLite FOREIGN KEY constraint %s has malformed ON action",
						name,
					)
				}
				action, afterAction, actionErr :=
					parseSQLiteDDLForeignKeyAction(part, afterKind)
				if actionErr != nil {
					return result, true, fmt.Errorf(
						"SQLite FOREIGN KEY constraint %s: %w",
						name,
						actionErr,
					)
				}
				if strings.EqualFold(actionKind, "update") {
					if result.onUpdate != "" {
						return result, true, fmt.Errorf(
							"SQLite FOREIGN KEY constraint %s duplicates ON UPDATE",
							name,
						)
					}
					result.onUpdate = action
				} else {
					if result.onDelete != "" {
						return result, true, fmt.Errorf(
							"SQLite FOREIGN KEY constraint %s duplicates ON DELETE",
							name,
						)
					}
					result.onDelete = action
				}
				index = afterAction
			case strings.EqualFold(nextKeyword, "deferrable") ||
				strings.EqualFold(nextKeyword, "not"):
				if result.deferrabilitySet {
					return result, true, fmt.Errorf(
						"SQLite FOREIGN KEY constraint %s duplicates deferrability",
						name,
					)
				}
				result.deferrabilitySet = true
				if strings.EqualFold(nextKeyword, "not") {
					deferrable, _, afterDeferrable, deferrableOK :=
						scanSQLiteDDLIdentifier(part, afterKeyword)
					if !deferrableOK ||
						!strings.EqualFold(deferrable, "deferrable") {
						return result, true, fmt.Errorf(
							"SQLite FOREIGN KEY constraint %s has malformed NOT DEFERRABLE",
							name,
						)
					}
					result.deferrable = false
					index = afterDeferrable
				} else {
					result.deferrable = true
					index = afterKeyword
				}
				initially, _, afterInitially, initiallyOK :=
					scanSQLiteDDLIdentifier(part, index)
				if initiallyOK && strings.EqualFold(initially, "initially") {
					mode, _, afterMode, modeOK :=
						scanSQLiteDDLIdentifier(part, afterInitially)
					if !modeOK ||
						(!strings.EqualFold(mode, "immediate") &&
							!strings.EqualFold(mode, "deferred")) {
						return result, true, fmt.Errorf(
							"SQLite FOREIGN KEY constraint %s has malformed INITIALLY mode",
							name,
						)
					}
					result.initiallyDeferred =
						strings.EqualFold(mode, "deferred")
					index = afterMode
				}
			default:
				return result, true, fmt.Errorf(
					"SQLite FOREIGN KEY constraint %s has unsupported clause %q",
					name,
					nextKeyword,
				)
			}
		}
	default:
		result.kind = sqliteTableConstraintUnknown
	}
	return result, true, nil
}

func parseSQLiteDDLIdentifierList(
	value string,
	index int,
) ([]string, int, error) {
	var triviaOK bool
	index, triviaOK = skipSQLiteDDLTrivia(value, index)
	if !triviaOK {
		return nil, index, errors.New("identifier list has unterminated comment")
	}
	if index >= len(value) || value[index] != '(' {
		return nil, index, errors.New("identifier list is missing")
	}
	index++
	identifiers := make([]string, 0, 2)
	for {
		identifier, _, next, ok := scanSQLiteDDLIdentifier(value, index)
		if !ok {
			return nil, index, errors.New("identifier is missing")
		}
		identifiers = append(identifiers, identifier)
		index, triviaOK = skipSQLiteDDLTrivia(value, next)
		if !triviaOK {
			return nil, index, errors.New(
				"identifier list has unterminated comment",
			)
		}
		if index >= len(value) {
			return nil, index, errors.New("identifier list is unterminated")
		}
		if value[index] == ')' {
			return identifiers, index + 1, nil
		}
		if value[index] != ',' {
			return nil, index, errors.New(
				"identifier list separator is malformed",
			)
		}
		index++
	}
}

func parseSQLiteDDLForeignKeyAction(
	value string,
	index int,
) (string, int, error) {
	first, _, next, ok := scanSQLiteDDLIdentifier(value, index)
	if !ok {
		return "", index, errors.New("foreign key action is missing")
	}
	switch strings.ToUpper(first) {
	case "RESTRICT", "CASCADE":
		return strings.ToUpper(first), next, nil
	case "NO", "SET":
		second, _, afterSecond, secondOK :=
			scanSQLiteDDLIdentifier(value, next)
		if !secondOK {
			return "", index, errors.New("foreign key action is incomplete")
		}
		action := strings.ToUpper(first + " " + second)
		if action != "NO ACTION" &&
			action != "SET NULL" &&
			action != "SET DEFAULT" {
			return "", index, fmt.Errorf(
				"unsupported foreign key action %q",
				action,
			)
		}
		return action, afterSecond, nil
	default:
		return "", index, fmt.Errorf(
			"unsupported foreign key action %q",
			first,
		)
	}
}

func scanSQLiteDDLIdentifier(
	value string,
	start int,
) (string, bool, int, bool) {
	var triviaOK bool
	start, triviaOK = skipSQLiteDDLTrivia(value, start)
	if !triviaOK {
		return "", false, start, false
	}
	if start >= len(value) {
		return "", false, start, false
	}
	switch value[start] {
	case '"', '`':
		quote := value[start]
		var builder strings.Builder
		for index := start + 1; index < len(value); index++ {
			if value[index] != quote {
				builder.WriteByte(value[index])
				continue
			}
			if index+1 < len(value) && value[index+1] == quote {
				builder.WriteByte(quote)
				index++
				continue
			}
			return builder.String(), true, index + 1, true
		}
		return "", true, start, false
	case '[':
		close := strings.IndexByte(value[start+1:], ']')
		if close < 0 {
			return "", true, start, false
		}
		close += start + 1
		return value[start+1 : close], true, close + 1, true
	case '\'':
		return "", false, start, false
	default:
		end := start
		for end < len(value) &&
			(value[end] == '_' ||
				value[end] >= 'a' && value[end] <= 'z' ||
				value[end] >= 'A' && value[end] <= 'Z' ||
				value[end] >= '0' && value[end] <= '9') {
			end++
		}
		if end == start {
			return "", false, start, false
		}
		return value[start:end], false, end, true
	}
}

func skipSQLiteDDLTrivia(value string, index int) (int, bool) {
	for index < len(value) {
		switch {
		case value[index] == ' ' ||
			value[index] == '\t' ||
			value[index] == '\r' ||
			value[index] == '\n':
			index++
		case value[index] == '-' &&
			index+1 < len(value) &&
			value[index+1] == '-':
			index += 2
			for index < len(value) && value[index] != '\n' {
				index++
			}
		case value[index] == '/' &&
			index+1 < len(value) &&
			value[index+1] == '*':
			close := strings.Index(value[index+2:], "*/")
			if close < 0 {
				return index, false
			}
			index += close + 4
		default:
			return index, true
		}
	}
	return index, true
}

func findSQLiteTableBodyOpen(value string) (int, error) {
	quote := byte(0)
	bracketed := false
	for index := 0; index < len(value); index++ {
		current := value[index]
		if quote != 0 {
			if current == quote {
				if index+1 < len(value) && value[index+1] == quote {
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
		if current == '-' && index+1 < len(value) &&
			value[index+1] == '-' {
			index += 2
			for index < len(value) && value[index] != '\n' {
				index++
			}
			continue
		}
		if current == '/' && index+1 < len(value) &&
			value[index+1] == '*' {
			close := strings.Index(value[index+2:], "*/")
			if close < 0 {
				return 0, errors.New("unterminated SQLite DDL comment")
			}
			index += close + 3
			continue
		}
		switch current {
		case '\'', '"', '`':
			quote = current
		case '[':
			bracketed = true
		case '(':
			return index, nil
		}
	}
	return 0, errors.New("SQLite table DDL body is missing")
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
		if current == '-' && index+1 < len(body) &&
			body[index+1] == '-' {
			index += 2
			for index < len(body) && body[index] != '\n' {
				index++
			}
			continue
		}
		if current == '/' && index+1 < len(body) &&
			body[index+1] == '*' {
			close := strings.Index(body[index+2:], "*/")
			if close < 0 {
				return nil, fmt.Errorf("unterminated block comment")
			}
			index += close + 3
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
	identifier, _, _, ok := scanSQLiteDDLIdentifier(part, 0)
	if !ok {
		return ""
	}
	return strings.ToLower(identifier)
}

func appendSQLiteColumnConstraint(part string, constraint string) string {
	return strings.TrimRight(part, " \t\r\n") + "\n" + constraint
}

func joinSQLiteTableBody(parts []string) string {
	return strings.Join(parts, "\n,\n")
}

func validateSQLiteProtectedColumnConstraintSemantics(
	db *gorm.DB,
	table string,
	protectedColumns []string,
	validatePrimaryKey bool,
) error {
	var tableSQL string
	result := db.Raw(`
		SELECT sql
		FROM main.sqlite_schema
		WHERE type = 'table' AND name = ?
	`, table).Scan(&tableSQL)
	if result.Error != nil {
		return fmt.Errorf(
			"read SQLite %s DDL for protected constraints: %w",
			table,
			result.Error,
		)
	}
	if result.RowsAffected != 1 || strings.TrimSpace(tableSQL) == "" {
		return fmt.Errorf("SQLite table %s is missing", table)
	}
	open, err := findSQLiteTableBodyOpen(tableSQL)
	if err != nil {
		return err
	}
	close, ok := matchingSQLParenthesis(tableSQL, open)
	if !ok {
		return fmt.Errorf("SQLite table %s has malformed DDL", table)
	}
	parts, err := splitSQLiteTableBody(tableSQL[open+1 : close])
	if err != nil {
		return err
	}
	protected := make(map[string]bool, len(protectedColumns))
	for _, column := range protectedColumns {
		protected[strings.ToLower(column)] = false
	}
	tablePrimaryKeys := 0
	for _, part := range parts {
		leading := sqliteDDLLeadingIdentifier(part)
		if _, wanted := protected[leading]; wanted {
			protected[leading] = true
			if err := validateSQLiteDefaultConflictAlgorithms(part); err != nil {
				return fmt.Errorf(
					"SQLite %s.%s has incompatible constraint semantics: %w",
					table,
					leading,
					err,
				)
			}
		}
		tokens, err := sqliteDDLTopLevelKeywords(part)
		if err != nil {
			return fmt.Errorf(
				"parse SQLite %s table constraints: %w",
				table,
				err,
			)
		}
		if containsSQLiteKeywordSequence(tokens, "primary", "key") &&
			leading != "id" {
			tablePrimaryKeys++
			if err := validateSQLiteDefaultConflictAlgorithms(
				part,
			); err != nil {
				return fmt.Errorf(
					"SQLite %s primary key has incompatible constraint semantics: %w",
					table,
					err,
				)
			}
		}
	}
	for column, found := range protected {
		if !found {
			return fmt.Errorf(
				"SQLite protected column %s.%s is missing",
				table,
				column,
			)
		}
	}
	if validatePrimaryKey {
		if tablePrimaryKeys > 1 {
			return fmt.Errorf(
				"SQLite table %s has duplicated table primary keys",
				table,
			)
		}
		if err := validateSQLitePrimaryKeyBackingIndex(db, table); err != nil {
			return err
		}
	}
	return nil
}

func validateSQLiteDefaultConflictAlgorithms(value string) error {
	tokens, err := sqliteDDLTopLevelKeywords(value)
	if err != nil {
		return err
	}
	for index := 0; index < len(tokens); index++ {
		if tokens[index] != "on" ||
			index+1 >= len(tokens) ||
			tokens[index+1] != "conflict" {
			continue
		}
		if index+2 >= len(tokens) {
			return errors.New("ON CONFLICT is missing its algorithm")
		}
		if tokens[index+2] != "abort" {
			return fmt.Errorf(
				"ON CONFLICT %s is not canonical ABORT behavior",
				strings.ToUpper(tokens[index+2]),
			)
		}
		index += 2
	}
	return nil
}

func sqliteDDLTopLevelKeywords(value string) ([]string, error) {
	keywords := make([]string, 0, 12)
	depth := 0
	for index := 0; index < len(value); {
		next, ok := skipSQLiteDDLTrivia(value, index)
		if !ok {
			return nil, errors.New("unterminated SQLite DDL comment")
		}
		index = next
		if index >= len(value) {
			break
		}
		switch value[index] {
		case '\'':
			after, ok := skipSQLiteDDLStringLiteral(value, index)
			if !ok {
				return nil, errors.New(
					"unterminated SQLite DDL string literal",
				)
			}
			index = after
		case '(':
			depth++
			index++
		case ')':
			depth--
			if depth < 0 {
				return nil, errors.New(
					"unbalanced SQLite DDL parenthesis",
				)
			}
			index++
		default:
			identifier, quoted, after, present :=
				scanSQLiteDDLIdentifier(value, index)
			if present {
				if depth == 0 && !quoted {
					keywords = append(
						keywords,
						strings.ToLower(identifier),
					)
				}
				index = after
				continue
			}
			index++
		}
	}
	if depth != 0 {
		return nil, errors.New("unbalanced SQLite DDL parenthesis")
	}
	return keywords, nil
}

func skipSQLiteDDLStringLiteral(value string, index int) (int, bool) {
	if index >= len(value) || value[index] != '\'' {
		return index, false
	}
	for index++; index < len(value); index++ {
		if value[index] != '\'' {
			continue
		}
		if index+1 < len(value) && value[index+1] == '\'' {
			index++
			continue
		}
		return index + 1, true
	}
	return index, false
}

func containsSQLiteKeywordSequence(tokens []string, expected ...string) bool {
	if len(expected) == 0 || len(tokens) < len(expected) {
		return false
	}
	for start := 0; start <= len(tokens)-len(expected); start++ {
		matches := true
		for index := range expected {
			if tokens[start+index] != expected[index] {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func validateSQLitePrimaryKeyBackingIndex(
	db *gorm.DB,
	table string,
) error {
	var indexes []sqliteAutomationWebhookIndexListRow
	if err := db.Raw(
		"PRAGMA main.index_list(" +
			quoteAutomationWebhookSQLiteIdentifier(table) + ")",
	).Scan(&indexes).Error; err != nil {
		return fmt.Errorf(
			"read SQLite %s primary-key indexes: %w",
			table,
			err,
		)
	}
	primaryIndexes := make([]sqliteAutomationWebhookIndexListRow, 0, 1)
	for _, index := range indexes {
		if strings.EqualFold(index.Origin, "pk") {
			primaryIndexes = append(primaryIndexes, index)
		}
	}
	if len(primaryIndexes) != 1 {
		return fmt.Errorf(
			"SQLite table %s has %d primary-key backing indexes, want 1",
			table,
			len(primaryIndexes),
		)
	}
	index := primaryIndexes[0]
	if index.Unique != 1 ||
		index.Partial != 0 ||
		!strings.HasPrefix(
			strings.ToLower(index.Name),
			"sqlite_autoindex_"+strings.ToLower(table)+"_",
		) {
		return fmt.Errorf(
			"SQLite table %s has incompatible primary-key backing index",
			table,
		)
	}
	var rows []sqliteAutomationWebhookIndexColumn
	if err := db.Raw(
		"PRAGMA main.index_xinfo(" +
			quoteAutomationWebhookSQLiteIdentifier(index.Name) + ")",
	).Scan(&rows).Error; err != nil {
		return fmt.Errorf(
			"read SQLite %s primary-key index columns: %w",
			table,
			err,
		)
	}
	keys := make([]sqliteAutomationWebhookIndexColumn, 0, 1)
	for _, row := range rows {
		if row.Key == 1 {
			keys = append(keys, row)
		}
	}
	if len(keys) != 1 ||
		keys[0].Sequence != 0 ||
		keys[0].ColumnID < 0 ||
		!keys[0].ColumnName.Valid ||
		keys[0].ColumnName.String != "id" ||
		keys[0].Descending != 0 ||
		!keys[0].Collation.Valid ||
		!strings.EqualFold(keys[0].Collation.String, "BINARY") {
		return fmt.Errorf(
			"SQLite table %s has incompatible primary-key key semantics",
			table,
		)
	}
	return nil
}

func sqliteColumnUsesDefaultBinaryCollation(
	db *gorm.DB,
	table string,
	column string,
) (bool, error) {
	var tableSQL string
	if err := db.Raw(`
		SELECT sql
		FROM main.sqlite_schema
		WHERE type = 'table' AND name = ?
	`, table).Scan(&tableSQL).Error; err != nil {
		return false, fmt.Errorf(
			"read SQLite %s DDL for collation contract: %w",
			table,
			err,
		)
	}
	if strings.TrimSpace(tableSQL) == "" {
		return false, fmt.Errorf("SQLite table %s is missing", table)
	}
	open, err := findSQLiteTableBodyOpen(tableSQL)
	if err != nil {
		return false, err
	}
	close, ok := matchingSQLParenthesis(tableSQL, open)
	if !ok {
		return false, fmt.Errorf("SQLite table %s has malformed DDL", table)
	}
	parts, err := splitSQLiteTableBody(tableSQL[open+1 : close])
	if err != nil {
		return false, err
	}
	for _, part := range parts {
		if sqliteDDLLeadingIdentifier(part) != strings.ToLower(column) {
			continue
		}
		collations, err := sqliteDDLDeclaredCollations(part)
		if err != nil {
			return false, fmt.Errorf(
				"parse SQLite %s.%s collation: %w",
				table,
				column,
				err,
			)
		}
		return len(collations) == 0 ||
			len(collations) == 1 &&
				strings.EqualFold(collations[0], "BINARY"), nil
	}
	return false, fmt.Errorf("SQLite %s.%s is missing", table, column)
}

func sqliteDDLDeclaredCollations(value string) ([]string, error) {
	collations := make([]string, 0, 1)
	for index := 0; index < len(value); {
		next, ok := skipSQLiteDDLTrivia(value, index)
		if !ok {
			return nil, errors.New("unterminated SQLite DDL comment")
		}
		index = next
		if index >= len(value) {
			return collations, nil
		}
		if value[index] == '\'' {
			index++
			for index < len(value) {
				if value[index] != '\'' {
					index++
					continue
				}
				if index+1 < len(value) && value[index+1] == '\'' {
					index += 2
					continue
				}
				index++
				break
			}
			if index > len(value) ||
				index == len(value) && value[len(value)-1] != '\'' {
				return nil, errors.New(
					"unterminated SQLite DDL string literal",
				)
			}
			continue
		}
		identifier, quoted, after, present :=
			scanSQLiteDDLIdentifier(value, index)
		if present {
			if !quoted && strings.EqualFold(identifier, "collate") {
				collation, _, afterCollation, ok :=
					scanSQLiteDDLIdentifier(value, after)
				if !ok {
					return nil, errors.New(
						"SQLite COLLATE clause is missing its name",
					)
				}
				collations = append(collations, collation)
				index = afterCollation
				continue
			}
			index = after
			continue
		}
		index++
	}
	return collations, nil
}

func rebuildSQLiteTableFromDDL(
	db *gorm.DB,
	table string,
	tempTable string,
	createSQL string,
	purpose string,
) error {
	if db == nil {
		return errors.New("SQLite rebuild database is required")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		return rebuildSQLiteTableFromDDLInTransaction(
			tx,
			table,
			tempTable,
			createSQL,
			purpose,
		)
	})
}

func rebuildSQLiteTableFromDDLInTransaction(
	db *gorm.DB,
	table string,
	tempTable string,
	createSQL string,
	purpose string,
) error {
	if err := requireSQLiteNoTempSchemaShadows(db, table); err != nil {
		return err
	}
	if err := requireSQLiteRebuildNameAvailable(db, tempTable); err != nil {
		return err
	}
	type columnState struct {
		Name   string `gorm:"column:name"`
		Hidden int    `gorm:"column:hidden"`
	}
	var columnRows []columnState
	if err := db.Raw(
		"PRAGMA main.table_xinfo(" +
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
		FROM main.sqlite_schema
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
		FROM main.sqlite_schema
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
	if err := db.Exec(createSQL).Error; err != nil {
		return fmt.Errorf(
			"create SQLite rebuild table %s for %s: %w",
			tempTable,
			purpose,
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

func requireSQLiteRebuildNameAvailable(
	db *gorm.DB,
	name string,
) error {
	var collision struct {
		SchemaName string `gorm:"column:schema_name"`
		Type       string `gorm:"column:type"`
		Name       string `gorm:"column:name"`
	}
	result := db.Raw(`
		SELECT schema_name, type, name
		FROM (
			SELECT
				'main' AS schema_name,
				type,
				name
			FROM main.sqlite_schema
			WHERE name = ? COLLATE NOCASE
			UNION ALL
			SELECT
				'temp' AS schema_name,
				type,
				name
			FROM temp.sqlite_schema
			WHERE name = ? COLLATE NOCASE
		) AS collisions
		ORDER BY schema_name, type, name
		LIMIT 1
	`, name, name).Scan(&collision)
	if result.Error != nil {
		return fmt.Errorf(
			"inspect SQLite reserved rebuild name %s: %w",
			name,
			result.Error,
		)
	}
	if result.RowsAffected != 0 {
		return fmt.Errorf(
			"SQLite reserved rebuild name %s collides with %s %s.%s",
			name,
			collision.Type,
			collision.SchemaName,
			collision.Name,
		)
	}
	return nil
}
