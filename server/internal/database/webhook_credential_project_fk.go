package database

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

const (
	domainEventProjectScopeFK = "fk_domain_events_project_scope"
	outboxProjectScopeFK      = "fk_outbox_deliveries_project_scope"
	webhookSnapshotProjectFK  = "fk_webhook_delivery_snapshots_project_scope"
)

type webhookProjectScopeFKDefinition struct {
	name  string
	table string
	model any
}

func webhookProjectScopeFKDefinitions() []webhookProjectScopeFKDefinition {
	return []webhookProjectScopeFKDefinition{
		{
			name:  domainEventProjectScopeFK,
			table: "domain_events",
			model: &models.DomainEvent{},
		},
		{
			name:  outboxProjectScopeFK,
			table: "outbox_deliveries",
			model: &models.OutboxDelivery{},
		},
		{
			name:  webhookSnapshotProjectFK,
			table: "webhook_delivery_snapshots",
			model: &models.WebhookDeliverySnapshot{},
		},
	}
}

func installWebhookProjectScopeForeignKeys(db *gorm.DB) error {
	if db == nil {
		return errors.New(
			"webhook Project-scope foreign key database is required",
		)
	}
	if err := validateWebhookProjectDirectoryReferences(db); err != nil {
		return err
	}
	switch db.Dialector.Name() {
	case "postgres":
		for _, definition := range webhookProjectScopeFKDefinitions() {
			valid, exists, err := postgresWebhookProjectScopeFKState(
				db,
				definition,
			)
			if err != nil {
				return err
			}
			if exists && !valid {
				return fmt.Errorf(
					"PostgreSQL Project-scope foreign key %s has an incompatible definition",
					definition.name,
				)
			}
			if !exists {
				if err := db.Exec(
					"ALTER TABLE " + definition.table +
						" ADD CONSTRAINT " + definition.name +
						" FOREIGN KEY (organization_id, project_id)" +
						" REFERENCES projects(organization_id, id)" +
						" ON UPDATE RESTRICT ON DELETE RESTRICT NOT VALID",
				).Error; err != nil {
					return fmt.Errorf(
						"add PostgreSQL Project-scope foreign key %s: %w",
						definition.name,
						err,
					)
				}
			}
			if err := db.Exec(
				"ALTER TABLE " + definition.table +
					" VALIDATE CONSTRAINT " + definition.name,
			).Error; err != nil {
				return fmt.Errorf(
					"validate PostgreSQL Project-scope foreign key %s: %w",
					definition.name,
					err,
				)
			}
		}
	case "sqlite":
		enabled, err := sqliteForeignKeysEnabled(db)
		if err != nil {
			return err
		}
		_, inTransaction := db.Statement.ConnPool.(gorm.TxCommitter)
		if !enabled && !inTransaction {
			return errors.New(
				"SQLite foreign_keys must be enabled before webhook Project-scope migration",
			)
		}
		allValid := true
		for _, definition := range webhookProjectScopeFKDefinitions() {
			if err := validateSQLiteWebhookProjectScopeFK(
				db,
				definition,
			); err != nil {
				allValid = false
				break
			}
		}
		if !allValid {
			if inTransaction && enabled {
				return errors.New(
					"SQLite Project-scope foreign key rebuild requires foreign_keys disabled before its top-level cutover transaction",
				)
			}
			if !inTransaction {
				if err := db.Exec(
					"PRAGMA foreign_keys = OFF",
				).Error; err != nil {
					return fmt.Errorf(
						"disable SQLite foreign keys for canonical rebuild: %w",
						err,
					)
				}
			}
			restoreForeignKeys := func() error {
				if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
					return fmt.Errorf(
						"restore SQLite foreign keys after canonical rebuild: %w",
						err,
					)
				}
				return nil
			}
			rebuild := func(tx *gorm.DB) error {
				for _, definition := range webhookProjectScopeFKDefinitions() {
					if err := rebuildSQLiteTableWithProjectScopeFK(
						tx,
						definition,
					); err != nil {
						return err
					}
				}
				return nil
			}
			if inTransaction {
				if err := rebuild(db); err != nil {
					return err
				}
			} else {
				rebuildErr := db.Transaction(rebuild)
				if restoreErr := restoreForeignKeys(); restoreErr != nil {
					return errors.Join(rebuildErr, restoreErr)
				}
				if rebuildErr != nil {
					return rebuildErr
				}
			}
		}
		if enabled {
			var violations []struct {
				Table string `gorm:"column:table"`
				RowID int64  `gorm:"column:rowid"`
			}
			if err := db.Raw("PRAGMA foreign_key_check").
				Scan(&violations).Error; err != nil {
				return fmt.Errorf(
					"run SQLite foreign key check: %w",
					err,
				)
			}
			if len(violations) != 0 {
				return fmt.Errorf(
					"SQLite foreign key check found %d violations",
					len(violations),
				)
			}
		}
	default:
		return fmt.Errorf(
			"webhook Project-scope foreign keys are unsupported for database dialect %q",
			db.Dialector.Name(),
		)
	}
	return validateWebhookProjectScopeForeignKeyCatalog(db)
}

type sqliteSchemaObject struct {
	Type string `gorm:"column:type"`
	Name string `gorm:"column:name"`
	SQL  string `gorm:"column:sql"`
}

func rebuildSQLiteTableWithProjectScopeFK(
	db *gorm.DB,
	definition webhookProjectScopeFKDefinition,
) error {
	var tableState sqliteSchemaObject
	if err := db.Raw(`
		SELECT type, name, sql
		FROM sqlite_master
		WHERE type = 'table' AND name = ?
	`, definition.table).Take(&tableState).Error; err != nil {
		return fmt.Errorf(
			"read SQLite table %s for Project-scope rebuild: %w",
			definition.table,
			err,
		)
	}
	if countSQLiteNamedConstraint(
		tableState.SQL,
		definition.name,
	) == 1 {
		return nil
	}
	if countSQLiteNamedConstraint(
		tableState.SQL,
		definition.name,
	) != 0 {
		return fmt.Errorf(
			"SQLite Project-scope foreign key %s is duplicated",
			definition.name,
		)
	}
	open := strings.Index(tableState.SQL, "(")
	close, ok := matchingSQLParenthesis(tableState.SQL, open)
	if !ok {
		return fmt.Errorf(
			"SQLite table %s has malformed DDL",
			definition.table,
		)
	}
	tempTable := definition.table + "__project_scope_fk"
	body := tableState.SQL[open+1 : close]
	suffix := tableState.SQL[close+1:]
	createSQL := "CREATE TABLE " +
		quoteAutomationWebhookSQLiteIdentifier(tempTable) +
		" (" + body + ", CONSTRAINT " +
		quoteAutomationWebhookSQLiteIdentifier(definition.name) +
		" FOREIGN KEY (" +
		quoteAutomationWebhookSQLiteIdentifier("organization_id") + ", " +
		quoteAutomationWebhookSQLiteIdentifier("project_id") +
		") REFERENCES " +
		quoteAutomationWebhookSQLiteIdentifier("projects") + "(" +
		quoteAutomationWebhookSQLiteIdentifier("organization_id") + ", " +
		quoteAutomationWebhookSQLiteIdentifier("id") +
		") ON UPDATE RESTRICT ON DELETE RESTRICT)" + suffix

	return rebuildSQLiteTableFromDDL(
		db,
		definition.table,
		tempTable,
		createSQL,
		"Project-scope foreign key",
	)
}

func validateWebhookProjectDirectoryReferences(db *gorm.DB) error {
	var violation struct {
		Source   string `gorm:"column:source"`
		ObjectID string `gorm:"column:object_id"`
	}
	if err := db.Raw(`
		SELECT source, object_id
		FROM (
			SELECT
				'domain_events' AS source,
				CAST(child.id AS TEXT) AS object_id
			FROM domain_events AS child
			LEFT JOIN projects AS project
			  ON project.organization_id = child.organization_id
			 AND project.id = child.project_id
			WHERE project.id IS NULL
			UNION ALL
			SELECT
				'outbox_deliveries' AS source,
				CAST(child.id AS TEXT) AS object_id
			FROM outbox_deliveries AS child
			LEFT JOIN projects AS project
			  ON project.organization_id = child.organization_id
			 AND project.id = child.project_id
			WHERE project.id IS NULL
			UNION ALL
			SELECT
				'webhook_delivery_snapshots' AS source,
				CAST(child.id AS TEXT) AS object_id
			FROM webhook_delivery_snapshots AS child
			LEFT JOIN projects AS project
			  ON project.organization_id = child.organization_id
			 AND project.id = child.project_id
			WHERE project.id IS NULL
		) AS violations
		ORDER BY source, object_id
		LIMIT 1
	`).Scan(&violation).Error; err != nil {
		return fmt.Errorf(
			"audit webhook Project directory references: %w",
			err,
		)
	}
	if violation.Source != "" {
		return fmt.Errorf(
			"%s row %s references a missing Project directory scope",
			violation.Source,
			violation.ObjectID,
		)
	}
	return nil
}

type postgresWebhookProjectScopeFKCatalogRow struct {
	Table      string `gorm:"column:table_name"`
	Name       string `gorm:"column:constraint_name"`
	Type       string `gorm:"column:constraint_type"`
	Validated  bool   `gorm:"column:validated"`
	Deferrable bool   `gorm:"column:deferrable"`
	Deferred   bool   `gorm:"column:deferred"`
	Definition string `gorm:"column:definition"`
}

func postgresWebhookProjectScopeFKState(
	db *gorm.DB,
	expected webhookProjectScopeFKDefinition,
) (bool, bool, error) {
	var rows []postgresWebhookProjectScopeFKCatalogRow
	if err := db.Raw(`
		SELECT
			table_state.relname AS table_name,
			constraint_state.conname AS constraint_name,
			constraint_state.contype::text AS constraint_type,
			constraint_state.convalidated AS validated,
			constraint_state.condeferrable AS deferrable,
			constraint_state.condeferred AS deferred,
			pg_get_constraintdef(constraint_state.oid, true) AS definition
		FROM pg_constraint AS constraint_state
		JOIN pg_class AS table_state
		  ON table_state.oid = constraint_state.conrelid
		JOIN pg_namespace AS namespace
		  ON namespace.oid = table_state.relnamespace
		WHERE namespace.nspname = CURRENT_SCHEMA()
		  AND table_state.relname = ?
		  AND constraint_state.conname = ?
	`, expected.table, expected.name).Scan(&rows).Error; err != nil {
		return false, false, fmt.Errorf(
			"read PostgreSQL Project-scope foreign key %s: %w",
			expected.name,
			err,
		)
	}
	if len(rows) == 0 {
		return false, false, nil
	}
	if len(rows) != 1 {
		return false, true, nil
	}
	row := rows[0]
	want := "foreign key (organization_id, project_id) " +
		"references projects(organization_id, id) " +
		"on update restrict on delete restrict"
	return row.Table == expected.table &&
			row.Name == expected.name &&
			row.Type == "f" &&
			row.Validated &&
			!row.Deferrable &&
			!row.Deferred &&
			canonicalForeignKeyDefinition(row.Definition) ==
				canonicalForeignKeyDefinition(want),
		true,
		nil
}

func canonicalForeignKeyDefinition(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, `"`, "")
	return strings.Join(strings.Fields(value), " ")
}

func validateWebhookProjectScopeForeignKeyCatalog(db *gorm.DB) error {
	switch db.Dialector.Name() {
	case "postgres":
		for _, definition := range webhookProjectScopeFKDefinitions() {
			valid, exists, err := postgresWebhookProjectScopeFKState(
				db,
				definition,
			)
			if err != nil {
				return err
			}
			if !exists || !valid {
				return fmt.Errorf(
					"PostgreSQL Project-scope foreign key %s is missing, unvalidated, or incompatible",
					definition.name,
				)
			}
		}
		return nil
	case "sqlite":
		for _, definition := range webhookProjectScopeFKDefinitions() {
			if err := validateSQLiteWebhookProjectScopeFK(
				db,
				definition,
			); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf(
			"webhook Project-scope foreign key validation is unsupported for database dialect %q",
			db.Dialector.Name(),
		)
	}
}

func sqliteForeignKeysEnabled(db *gorm.DB) (bool, error) {
	var enabled int
	if err := db.Raw("PRAGMA foreign_keys").Scan(&enabled).Error; err != nil {
		return false, fmt.Errorf(
			"read SQLite foreign_keys state: %w",
			err,
		)
	}
	return enabled == 1, nil
}

type sqliteWebhookForeignKeyRow struct {
	ID       int    `gorm:"column:id"`
	Sequence int    `gorm:"column:seq"`
	Table    string `gorm:"column:table"`
	From     string `gorm:"column:from"`
	To       string `gorm:"column:to"`
	OnUpdate string `gorm:"column:on_update"`
	OnDelete string `gorm:"column:on_delete"`
	Match    string `gorm:"column:match"`
}

func validateSQLiteWebhookProjectScopeFK(
	db *gorm.DB,
	expected webhookProjectScopeFKDefinition,
) error {
	var rows []sqliteWebhookForeignKeyRow
	if err := db.Raw(
		"PRAGMA foreign_key_list(`" + expected.table + "`)",
	).Scan(&rows).Error; err != nil {
		return fmt.Errorf(
			"read SQLite Project-scope foreign keys for %s: %w",
			expected.table,
			err,
		)
	}
	byID := make(map[int][]sqliteWebhookForeignKeyRow)
	for _, row := range rows {
		if row.Table == "projects" {
			byID[row.ID] = append(byID[row.ID], row)
		}
	}
	matches := 0
	for _, group := range byID {
		sort.Slice(group, func(left, right int) bool {
			return group[left].Sequence < group[right].Sequence
		})
		if len(group) != 2 {
			continue
		}
		if group[0].From == "organization_id" &&
			group[0].To == "organization_id" &&
			group[1].From == "project_id" &&
			group[1].To == "id" &&
			group[0].OnUpdate == "RESTRICT" &&
			group[0].OnDelete == "RESTRICT" &&
			group[1].OnUpdate == "RESTRICT" &&
			group[1].OnDelete == "RESTRICT" &&
			group[0].Match == "NONE" &&
			group[1].Match == "NONE" {
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf(
			"SQLite table %s has %d canonical Project-scope foreign keys, want 1",
			expected.table,
			matches,
		)
	}
	var tableState struct {
		SQL string `gorm:"column:sql"`
	}
	if err := db.Raw(`
		SELECT sql
		FROM sqlite_master
		WHERE type = 'table' AND name = ?
	`, expected.table).Scan(&tableState).Error; err != nil {
		return fmt.Errorf(
			"read SQLite table SQL for %s: %w",
			expected.table,
			err,
		)
	}
	if countSQLiteNamedConstraint(
		tableState.SQL,
		expected.name,
	) != 1 {
		return fmt.Errorf(
			"SQLite Project-scope foreign key %s is missing or duplicated on %s",
			expected.name,
			expected.table,
		)
	}
	return nil
}

func countSQLiteNamedConstraint(tableSQL, name string) int {
	lower := strings.ToLower(tableSQL)
	needle := strings.ToLower(name)
	count := 0
	for offset := 0; offset < len(lower); {
		index := strings.Index(lower[offset:], needle)
		if index < 0 {
			break
		}
		index += offset
		before := strings.LastIndex(lower[:index], "constraint")
		if before >= 0 &&
			strings.Trim(
				lower[before+len("constraint"):index],
				" \t\n\r`\"",
			) == "" {
			count++
		}
		offset = index + len(needle)
	}
	return count
}
