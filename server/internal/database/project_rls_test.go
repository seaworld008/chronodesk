package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/config"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestProjectRuntimeConnectionsRequireExplicitRoleURLs(t *testing.T) {
	if _, _, err := OpenProjectMigrationDatabase(nil); err == nil {
		t.Fatal("nil migration configuration was accepted")
	}
	if _, err := NewProjectRuntime(nil); err == nil {
		t.Fatal("nil runtime configuration was accepted")
	}
	cfg := &config.Config{}
	if _, _, err := OpenProjectMigrationDatabase(cfg); err == nil ||
		!strings.Contains(err.Error(), "DATABASE_MIGRATION_URL") {
		t.Fatalf("missing migration URL error = %v", err)
	}
	if _, err := NewProjectRuntime(cfg); err == nil ||
		!strings.Contains(err.Error(), "DATABASE_RUNTIME_URL") {
		t.Fatalf("missing runtime URL error = %v", err)
	}
}

func TestProjectRLSTableInventoriesAreExplicitAndDefensive(t *testing.T) {
	protected := ProjectRLSProtectedTables()
	required := RequiredProjectOwnedTables()
	if len(protected) != len(required) || len(protected) < 20 {
		t.Fatalf(
			"protected inventory is not the complete project-owned inventory: protected=%d required=%d",
			len(protected),
			len(required),
		)
	}
	for index := range required {
		if protected[index] != required[index] {
			t.Fatalf(
				"protected inventory differs at %d: %q != %q",
				index,
				protected[index],
				required[index],
			)
		}
	}
	protected[0] = "users"
	if got := ProjectRLSProtectedTables(); got[0] != "categories" {
		t.Fatalf("caller mutated protected table inventory: %v", got)
	}

	for _, expected := range []string{
		"categories",
		"ticket_comments",
		"policy_decisions",
		"domain_events",
		"agent_tasks",
		"agent_runs",
		"approval_decisions",
		"knowledge_source_links",
		"knowledge_object_write_intents",
		"connector_definitions",
		"integration_conflicts",
		"notifications",
		"webhook_configs",
		"webhook_logs",
		"automation_rules",
		"automation_logs",
		"sla_configs",
		"ticket_templates",
		"quick_replies",
	} {
		if !containsProjectRLSTestString(required, expected) {
			t.Errorf("required project-owned table inventory omits %q", expected)
		}
	}
	required[0] = "users"
	if RequiredProjectOwnedTables()[0] != "categories" {
		t.Fatal("caller mutated required project-owned table inventory")
	}
}

func TestValidateProjectOwnedTableScopesReportsEveryMissingScopeColumn(t *testing.T) {
	db := openProjectRLSTestDB(t)
	for _, statement := range []string{
		`CREATE TABLE tickets (
			id INTEGER PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL
		)`,
		`CREATE TABLE ticket_comments (
			id INTEGER PRIMARY KEY,
			ticket_id INTEGER NOT NULL
		)`,
		`CREATE TABLE connections (
			id TEXT PRIMARY KEY,
			project_id INTEGER NOT NULL
		)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create scope fixture: %v", err)
		}
	}

	err := validateProjectOwnedTableScopes(
		db,
		[]string{"tickets", "ticket_comments", "connections", "agent_runs"},
	)
	if err == nil {
		t.Fatal("scope validation unexpectedly accepted incomplete project-owned tables")
	}
	message := err.Error()
	for _, expected := range []string{
		"ticket_comments.organization_id",
		"ticket_comments.project_id",
		"connections.organization_id",
		"agent_runs",
	} {
		if !strings.Contains(message, expected) {
			t.Errorf("scope validation error %q omits %q", message, expected)
		}
	}
	for _, unexpected := range []string{
		"tickets.organization_id",
		"tickets.project_id",
		"connections.project_id",
	} {
		if strings.Contains(message, unexpected) {
			t.Errorf("scope validation error %q falsely reports %q", message, unexpected)
		}
	}
}

func TestRequiredProjectOwnedTableScopeAuditAcceptsScopeReadyModels(t *testing.T) {
	db := openProjectRLSTestDB(t)
	if err := db.AutoMigrate(
		&models.ApprovalDecision{},
		&models.WebhookConfig{},
		&models.WebhookLog{},
	); err != nil {
		t.Fatalf("migrate current project-owned gap models: %v", err)
	}

	if err := validateProjectOwnedTableScopes(
		db,
		[]string{"approval_decisions", "webhook_configs", "webhook_logs"},
	); err != nil {
		t.Fatalf("scope-ready project-owned models failed audit: %v", err)
	}
}

func TestCompleteMigrationInventoryIsReadyForProjectRLSCutover(t *testing.T) {
	db := openProjectRLSTestDB(t)
	if err := db.AutoMigrate(schemaMigrationModels()...); err != nil {
		t.Fatalf("migrate complete project-owned inventory: %v", err)
	}
	if err := ValidateRequiredProjectOwnedTableScopes(db); err != nil {
		t.Fatalf("complete project-owned inventory is not scope ready: %v", err)
	}
}

func TestMigrateProjectRLSFailsClosedWhenProtectedTableLacksScope(t *testing.T) {
	db := openProjectRLSTestDB(t)
	if err := db.Exec(`
		CREATE TABLE tickets (
			id INTEGER PRIMARY KEY,
			organization_id INTEGER NOT NULL
		)
	`).Error; err != nil {
		t.Fatalf("create incomplete tickets table: %v", err)
	}

	err := MigrateProjectRLS(db)
	if err == nil {
		t.Fatal("RLS migration unexpectedly accepted tickets without project_id")
	}
	if !strings.Contains(err.Error(), "tickets.project_id") {
		t.Fatalf("RLS migration did not identify the missing column: %v", err)
	}
}

func TestMigrateProjectRLSValidatesButDoesNotEmulateRLSOnSQLite(t *testing.T) {
	db := openProjectRLSTestDB(t)
	createProjectRLSScopeTables(t, db, ProjectRLSProtectedTables())
	if err := MigrateProjectRLS(db); err != nil {
		t.Fatalf("validate SQLite project scope schema: %v", err)
	}
	if err := EnableProjectRLS(db); err == nil ||
		!strings.Contains(err.Error(), "only be enabled on PostgreSQL") {
		t.Fatalf("SQLite unexpectedly claimed to enable project RLS: %v", err)
	}

	var triggerCount int64
	if err := db.Raw(`
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'trigger' AND tbl_name = 'tickets'
	`).Scan(&triggerCount).Error; err != nil {
		t.Fatalf("inspect SQLite triggers: %v", err)
	}
	if triggerCount != 0 {
		t.Fatalf("SQLite RLS emulation unexpectedly installed %d triggers", triggerCount)
	}
}

func TestProjectRLSIdentifierAllowlistRejectsUntrustedSQL(t *testing.T) {
	for _, valid := range []string{"tickets", "ticket_comments", "connector_definitions"} {
		quoted, err := quoteProjectRLSIdentifier(valid)
		if err != nil {
			t.Fatalf("allowlisted identifier %q: %v", valid, err)
		}
		if quoted != `"`+valid+`"` {
			t.Fatalf("identifier %q quoted as %q", valid, quoted)
		}
	}

	for _, invalid := range []string{
		"users",
		"Tickets",
		"tickets; DROP TABLE users",
		`tickets"`,
		"public.tickets",
		"",
	} {
		if _, err := quoteProjectRLSIdentifier(invalid); err == nil {
			t.Errorf("unsafe or non-allowlisted identifier %q was accepted", invalid)
		}
	}

	db := openProjectRLSTestDB(t)
	err := validateProjectOwnedTableScopes(
		db,
		[]string{"tickets; DROP TABLE users"},
	)
	if err == nil || !strings.Contains(err.Error(), "unsafe project RLS SQL identifier") {
		t.Fatalf("schema validator did not reject unsafe identifier: %v", err)
	}
}

func TestWithProjectScopeTransactionRejectsInvalidInputsBeforeCallback(t *testing.T) {
	db := openProjectRLSTestDB(t)
	called := false
	callback := func(*gorm.DB) error {
		called = true
		return nil
	}

	for _, test := range []struct {
		name  string
		ctx   context.Context
		db    *gorm.DB
		scope models.ProjectScope
		fn    func(*gorm.DB) error
	}{
		{
			name:  "nil context",
			db:    db,
			scope: models.ProjectScope{OrganizationID: 1, ProjectID: 2},
			fn:    callback,
		},
		{
			name:  "nil database",
			ctx:   context.Background(),
			scope: models.ProjectScope{OrganizationID: 1, ProjectID: 2},
			fn:    callback,
		},
		{
			name:  "missing organization",
			ctx:   context.Background(),
			db:    db,
			scope: models.ProjectScope{ProjectID: 2},
			fn:    callback,
		},
		{
			name:  "missing project",
			ctx:   context.Background(),
			db:    db,
			scope: models.ProjectScope{OrganizationID: 1},
			fn:    callback,
		},
		{
			name:  "nil callback",
			ctx:   context.Background(),
			db:    db,
			scope: models.ProjectScope{OrganizationID: 1, ProjectID: 2},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := WithProjectScopeTransaction(
				test.ctx,
				test.db,
				test.scope,
				test.fn,
			)
			if err == nil {
				t.Fatal("invalid project scope transaction unexpectedly succeeded")
			}
		})
	}
	if called {
		t.Fatal("callback ran for invalid project scope transaction input")
	}
}

func TestWithProjectScopeTransactionCommitsAndRollsBackOnSQLite(t *testing.T) {
	db := openProjectRLSTestDB(t)
	if err := db.Exec(`
		CREATE TABLE project_rls_ledger (
			id INTEGER PRIMARY KEY,
			value TEXT NOT NULL
		)
	`).Error; err != nil {
		t.Fatalf("create transaction fixture: %v", err)
	}
	scope := models.ProjectScope{OrganizationID: 11, ProjectID: 22}

	if err := WithProjectScopeTransaction(
		context.Background(),
		db,
		scope,
		func(tx *gorm.DB) error {
			return tx.Exec(
				"INSERT INTO project_rls_ledger (id, value) VALUES (?, ?)",
				1,
				"committed",
			).Error
		},
	); err != nil {
		t.Fatalf("commit scoped SQLite transaction: %v", err)
	}

	rollback := errors.New("force rollback")
	err := WithProjectScopeTransaction(
		context.Background(),
		db,
		scope,
		func(tx *gorm.DB) error {
			if err := tx.Exec(
				"INSERT INTO project_rls_ledger (id, value) VALUES (?, ?)",
				2,
				"rolled back",
			).Error; err != nil {
				return err
			}
			return rollback
		},
	)
	if !errors.Is(err, rollback) {
		t.Fatalf("rollback error = %v, want sentinel", err)
	}

	var ids []int
	if err := db.Table("project_rls_ledger").Order("id").Pluck("id", &ids).Error; err != nil {
		t.Fatalf("read transaction fixture: %v", err)
	}
	if len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("transaction rows = %v, want only committed row 1", ids)
	}
}

func TestWithProjectScopeTransactionRejectsNestedTransaction(t *testing.T) {
	db := openProjectRLSTestDB(t)
	called := false

	err := db.Transaction(func(outer *gorm.DB) error {
		nestedErr := WithProjectScopeTransaction(
			context.Background(),
			outer,
			models.ProjectScope{OrganizationID: 11, ProjectID: 22},
			func(*gorm.DB) error {
				called = true
				return nil
			},
		)
		if nestedErr == nil ||
			!strings.Contains(nestedErr.Error(), "top-level database handle") {
			return fmt.Errorf("nested scoped transaction error = %v", nestedErr)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("outer transaction: %v", err)
	}
	if called {
		t.Fatal("callback ran inside a nested project scope transaction")
	}
}

func TestWithProjectScopeTransactionHonorsCancelledContext(t *testing.T) {
	db := openProjectRLSTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false

	err := WithProjectScopeTransaction(
		ctx,
		db,
		models.ProjectScope{OrganizationID: 1, ProjectID: 2},
		func(*gorm.DB) error {
			called = true
			return nil
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled transaction error = %v, want context.Canceled", err)
	}
	if called {
		t.Fatal("callback ran after context cancellation")
	}
}

func TestWithProjectScopeTransactionRejectsIDsOutsidePostgresBigInt(t *testing.T) {
	if ^uint(0) <= uint(^uint32(0)) {
		t.Skip("uint cannot represent values outside PostgreSQL BIGINT on this architecture")
	}
	db := openProjectRLSTestDB(t)
	tooLarge64 := postgresProjectScopeMaxID + 1
	tooLarge := uint(tooLarge64)
	called := false

	err := WithProjectScopeTransaction(
		context.Background(),
		db,
		models.ProjectScope{OrganizationID: tooLarge, ProjectID: 1},
		func(*gorm.DB) error {
			called = true
			return nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "fit PostgreSQL BIGINT") {
		t.Fatalf("out-of-range scope error = %v", err)
	}
	if called {
		t.Fatal("callback ran for an out-of-range project scope")
	}
}

func openProjectRLSTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	name := strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("open project RLS test database: %v", err)
	}
	return db
}

func containsProjectRLSTestString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func createProjectRLSScopeTables(
	t *testing.T,
	db *gorm.DB,
	tableNames []string,
) {
	t.Helper()
	for _, tableName := range tableNames {
		statement := fmt.Sprintf(
			"CREATE TABLE %s (id INTEGER PRIMARY KEY, organization_id INTEGER NOT NULL, project_id INTEGER NOT NULL)",
			quoteStaticProjectRLSIdentifier(tableName),
		)
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create project RLS scope table %s: %v", tableName, err)
		}
	}
}
