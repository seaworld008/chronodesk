package database

import (
	"errors"
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
