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
	named, namedErr := sqliteNamedProjectScopeForeignKey(
		tableState.SQL,
		definition.name,
	)
	if namedErr == nil && named.isCanonicalProjectScopeFK() {
		return nil
	}
	if namedErr != nil &&
		!errors.Is(namedErr, errSQLiteNamedConstraintMissing) {
		return fmt.Errorf(
			"SQLite Project-scope foreign key %s is malformed: %w",
			definition.name,
			namedErr,
		)
	}
	if namedErr == nil {
		return fmt.Errorf(
			"SQLite Project-scope foreign key %s has an incompatible definition",
			definition.name,
		)
	}
	open, openErr := findSQLiteTableBodyOpen(tableState.SQL)
	if openErr != nil {
		return fmt.Errorf(
			"parse SQLite table %s opening DDL: %w",
			definition.table,
			openErr,
		)
	}
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
	ChildRelationOID    uint32 `gorm:"column:child_relation_oid"`
	ExpectedChildOID    uint32 `gorm:"column:expected_child_oid"`
	ParentRelationOID   uint32 `gorm:"column:parent_relation_oid"`
	ExpectedParentOID   uint32 `gorm:"column:expected_parent_oid"`
	ChildSchema         string `gorm:"column:child_schema"`
	ChildTable          string `gorm:"column:child_table"`
	ParentSchema        string `gorm:"column:parent_schema"`
	ParentTable         string `gorm:"column:parent_table"`
	Name                string `gorm:"column:constraint_name"`
	Type                string `gorm:"column:constraint_type"`
	Ordinality          int    `gorm:"column:ordinality"`
	ChildAttnum         int    `gorm:"column:child_attnum"`
	ParentAttnum        int    `gorm:"column:parent_attnum"`
	ChildColumn         string `gorm:"column:child_column"`
	ParentColumn        string `gorm:"column:parent_column"`
	UpdateAction        string `gorm:"column:update_action"`
	DeleteAction        string `gorm:"column:delete_action"`
	MatchType           string `gorm:"column:match_type"`
	Validated           bool   `gorm:"column:validated"`
	Deferrable          bool   `gorm:"column:deferrable"`
	Deferred            bool   `gorm:"column:deferred"`
	ParentConstraintOID uint32 `gorm:"column:parent_constraint_oid"`
}

func postgresWebhookProjectScopeFKState(
	db *gorm.DB,
	expected webhookProjectScopeFKDefinition,
) (bool, bool, error) {
	var rows []postgresWebhookProjectScopeFKCatalogRow
	if err := db.Raw(`
		SELECT
			constraint_state.conrelid::oid AS child_relation_oid,
			to_regclass(
				format('%I.%I', CURRENT_SCHEMA(), CAST(? AS text))
			)::oid AS expected_child_oid,
			constraint_state.confrelid::oid AS parent_relation_oid,
			to_regclass(
				format('%I.%I', CURRENT_SCHEMA(), 'projects')
			)::oid AS expected_parent_oid,
			child_namespace.nspname AS child_schema,
			child_table.relname AS child_table,
			parent_namespace.nspname AS parent_schema,
			parent_table.relname AS parent_table,
			constraint_state.conname AS constraint_name,
			constraint_state.contype::text AS constraint_type,
			key.ordinality::integer AS ordinality,
			key.child_attnum::integer AS child_attnum,
			key.parent_attnum::integer AS parent_attnum,
			child_attribute.attname AS child_column,
			parent_attribute.attname AS parent_column,
			constraint_state.confupdtype::text AS update_action,
			constraint_state.confdeltype::text AS delete_action,
			constraint_state.confmatchtype::text AS match_type,
			constraint_state.convalidated AS validated,
			constraint_state.condeferrable AS deferrable,
			constraint_state.condeferred AS deferred,
			constraint_state.conparentid::oid AS parent_constraint_oid
		FROM pg_constraint AS constraint_state
		JOIN pg_class AS child_table
		  ON child_table.oid = constraint_state.conrelid
		JOIN pg_namespace AS child_namespace
		  ON child_namespace.oid = child_table.relnamespace
		JOIN pg_class AS parent_table
		  ON parent_table.oid = constraint_state.confrelid
		JOIN pg_namespace AS parent_namespace
		  ON parent_namespace.oid = parent_table.relnamespace
		JOIN LATERAL unnest(
			constraint_state.conkey,
			constraint_state.confkey
		) WITH ORDINALITY AS key(
			child_attnum,
			parent_attnum,
			ordinality
		) ON TRUE
		JOIN pg_attribute AS child_attribute
		  ON child_attribute.attrelid = constraint_state.conrelid
		 AND child_attribute.attnum = key.child_attnum
		 AND NOT child_attribute.attisdropped
		JOIN pg_attribute AS parent_attribute
		  ON parent_attribute.attrelid = constraint_state.confrelid
		 AND parent_attribute.attnum = key.parent_attnum
		 AND NOT parent_attribute.attisdropped
		WHERE child_namespace.oid =
			to_regnamespace(CURRENT_SCHEMA())::oid
		  AND child_table.relname = ?
		  AND constraint_state.conname = ?
		ORDER BY key.ordinality
	`, expected.table, expected.table, expected.name).Scan(&rows).Error; err != nil {
		return false, false, fmt.Errorf(
			"read PostgreSQL Project-scope foreign key %s: %w",
			expected.name,
			err,
		)
	}
	if len(rows) == 0 {
		return false, false, nil
	}
	if len(rows) != 2 {
		return false, true, nil
	}
	expectedChildColumns := []string{"organization_id", "project_id"}
	expectedParentColumns := []string{"organization_id", "id"}
	for index, row := range rows {
		if row.ChildRelationOID == 0 ||
			row.ParentRelationOID == 0 ||
			row.ChildRelationOID != row.ExpectedChildOID ||
			row.ParentRelationOID != row.ExpectedParentOID ||
			row.ChildRelationOID == row.ParentRelationOID ||
			row.ChildSchema != row.ParentSchema ||
			row.ChildSchema == "" ||
			row.ChildTable != expected.table ||
			row.ParentTable != "projects" ||
			row.Name != expected.name ||
			row.Type != "f" ||
			row.Ordinality != index+1 ||
			row.ChildAttnum <= 0 ||
			row.ParentAttnum <= 0 ||
			row.ChildColumn != expectedChildColumns[index] ||
			row.ParentColumn != expectedParentColumns[index] ||
			row.UpdateAction != "r" ||
			row.DeleteAction != "r" ||
			row.MatchType != "s" ||
			!row.Validated ||
			row.Deferrable ||
			row.Deferred ||
			row.ParentConstraintOID != 0 {
			return false, true, nil
		}
	}
	return true, true, nil
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
	named, err := sqliteNamedProjectScopeForeignKey(
		tableState.SQL,
		expected.name,
	)
	if err != nil {
		return fmt.Errorf(
			"SQLite Project-scope foreign key %s is missing, duplicated, or malformed on %s: %w",
			expected.name,
			expected.table,
			err,
		)
	}
	if !named.isCanonicalProjectScopeFK() {
		return fmt.Errorf(
			"SQLite Project-scope foreign key %s is not the canonical FK group on %s",
			expected.name,
			expected.table,
		)
	}
	return nil
}

func sqliteNamedProjectScopeForeignKey(
	tableSQL string,
	name string,
) (sqliteParsedTableConstraint, error) {
	constraints, err := sqliteNamedTableConstraints(tableSQL, name)
	if err != nil {
		return sqliteParsedTableConstraint{}, err
	}
	if len(constraints) == 0 {
		return sqliteParsedTableConstraint{}, fmt.Errorf(
			"%w: %s",
			errSQLiteNamedConstraintMissing,
			name,
		)
	}
	if len(constraints) != 1 {
		return sqliteParsedTableConstraint{}, fmt.Errorf(
			"SQLite named constraint %s is duplicated",
			name,
		)
	}
	if constraints[0].kind != sqliteTableConstraintForeignKey {
		return sqliteParsedTableConstraint{}, fmt.Errorf(
			"SQLite named constraint %s is not a FOREIGN KEY",
			name,
		)
	}
	return constraints[0], nil
}

func (constraint sqliteParsedTableConstraint) isCanonicalProjectScopeFK() bool {
	return constraint.kind == sqliteTableConstraintForeignKey &&
		equalFoldedIdentifiers(
			constraint.childColumns,
			[]string{"organization_id", "project_id"},
		) &&
		strings.EqualFold(constraint.parentTable, "projects") &&
		equalFoldedIdentifiers(
			constraint.parentColumns,
			[]string{"organization_id", "id"},
		) &&
		constraint.onUpdate == "RESTRICT" &&
		constraint.onDelete == "RESTRICT" &&
		!constraint.deferrable &&
		!constraint.initiallyDeferred
}

func equalFoldedIdentifiers(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !strings.EqualFold(left[index], right[index]) {
			return false
		}
	}
	return true
}
