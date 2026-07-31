package services

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAnalyticsAuthorizedProjectSetUnderNonOwnerPostgresRLS(t *testing.T) {
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set CHRONODESK_POSTGRES_INTEGRATION=1 for PostgreSQL analytics RLS evidence")
	}
	rawDSN := strings.TrimSpace(os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"))
	if rawDSN == "" {
		t.Fatal("CHRONODESK_POSTGRES_INTEGRATION_DSN is required")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatalf("parse PostgreSQL integration DSN: %v", err)
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal("PostgreSQL analytics integration test requires a loopback target")
		}
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	schemaName := "chronodesk_analytics_" + suffix
	roleName := "chronodesk_analytics_runtime_" + suffix
	rolePassword := "ChronodeskAnalytics" + suffix
	quotedSchema := quoteAnalyticsPostgresIdentifier(schemaName)
	quotedRole := quoteAnalyticsPostgresIdentifier(roleName)

	admin, err := gorm.Open(postgres.Open(rawDSN), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open PostgreSQL integration admin: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	roleCreated := false
	t.Cleanup(func() {
		_ = admin.Exec("DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE").Error
		if roleCreated {
			_ = admin.Exec("DROP ROLE IF EXISTS " + quotedRole).Error
		}
		_ = adminSQL.Close()
	})

	if err := admin.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create analytics schema: %v", err)
	}
	adminScopedURL := *parsed
	adminQuery := adminScopedURL.Query()
	adminQuery.Set("search_path", schemaName)
	adminScopedURL.RawQuery = adminQuery.Encode()
	adminScoped, err := gorm.Open(postgres.Open(adminScopedURL.String()), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open schema-scoped PostgreSQL admin: %v", err)
	}
	adminScopedSQL, err := adminScoped.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adminScopedSQL.Close() })

	createAnalyticsPostgresSchema(t, adminScoped)
	seedAnalyticsPostgresFixtures(t, adminScoped)
	for _, tableName := range []string{"tickets", "ticket_comments"} {
		quotedTable := quoteAnalyticsPostgresIdentifier(tableName)
		if err := adminScoped.Exec(
			"ALTER TABLE " + quotedTable +
				" ENABLE ROW LEVEL SECURITY",
		).Error; err != nil {
			t.Fatal(err)
		}
		if err := adminScoped.Exec(
			"ALTER TABLE " + quotedTable +
				" FORCE ROW LEVEL SECURITY",
		).Error; err != nil {
			t.Fatal(err)
		}
		if err := adminScoped.Exec(
			"CREATE POLICY chronodesk_project_scope ON " + quotedTable +
				" FOR SELECT TO PUBLIC USING (" +
				analyticsPostgresProjectPredicate + ")",
		).Error; err != nil {
			t.Fatal(err)
		}
	}

	if err := admin.Exec(
		"CREATE ROLE " + quotedRole +
			" LOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS PASSWORD " +
			quoteAnalyticsPostgresLiteral(rolePassword),
	).Error; err != nil {
		t.Fatalf("create analytics runtime role: %v", err)
	}
	roleCreated = true
	if err := adminScoped.Exec(
		"GRANT USAGE ON SCHEMA " + quotedSchema + " TO " + quotedRole,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := adminScoped.Exec(
		"GRANT SELECT ON ALL TABLES IN SCHEMA " +
			quotedSchema + " TO " + quotedRole,
	).Error; err != nil {
		t.Fatal(err)
	}
	// PostgreSQL row-locking SELECTs require UPDATE privilege on the locked
	// authorization relations. Keep business facts read-only while granting
	// only the minimum needed to linearize Project -> User -> Membership
	// revalidation against concurrent revocation.
	if err := adminScoped.Exec(
		"GRANT UPDATE ON " +
			quoteAnalyticsPostgresIdentifier("projects") + ", " +
			quoteAnalyticsPostgresIdentifier("users") + ", " +
			quoteAnalyticsPostgresIdentifier("project_memberships") +
			" TO " + quotedRole,
	).Error; err != nil {
		t.Fatal(err)
	}

	runtimeURL := adminScopedURL
	runtimeURL.User = url.UserPassword(roleName, rolePassword)
	runtimeDB, err := gorm.Open(postgres.Open(runtimeURL.String()), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open non-owner analytics runtime: %v", err)
	}
	runtimeSQL, err := runtimeDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeSQL.Close() })

	roleState := struct {
		CurrentUser string `gorm:"column:current_user"`
		Superuser   bool
		BypassRLS   bool `gorm:"column:bypass_rls"`
		TableOwner  string
	}{}
	if err := runtimeDB.Raw(`
		SELECT
			current_user,
			role.rolsuper AS superuser,
			role.rolbypassrls AS bypass_rls,
			owner.rolname AS table_owner
		FROM pg_roles AS role
		JOIN pg_class AS table_state ON table_state.relname = 'tickets'
		JOIN pg_namespace AS namespace
			ON namespace.oid = table_state.relnamespace
			AND namespace.nspname = current_schema()
		JOIN pg_roles AS owner ON owner.oid = table_state.relowner
		WHERE role.rolname = current_user
	`).Scan(&roleState).Error; err != nil {
		t.Fatal(err)
	}
	if roleState.CurrentUser != roleName ||
		roleState.Superuser ||
		roleState.BypassRLS ||
		roleState.TableOwner == roleName {
		t.Fatalf("runtime role is not least privilege: %+v", roleState)
	}

	var unscopedTickets int64
	if err := runtimeDB.Table("tickets").Count(&unscopedTickets).Error; err != nil {
		t.Fatal(err)
	}
	if unscopedTickets != 0 {
		t.Fatalf("FORCE RLS exposed %d unscoped tickets", unscopedTickets)
	}

	service := NewAnalyticsService(runtimeDB)
	stats, err := service.GetBusinessStats(
		context.Background(),
		mustHumanAnalyticsAuthorizedProjectSet(t, 1, 10, []uint{1, 2}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TicketStats.Total != 2 || stats.ActivityStats.TotalComments != 2 {
		t.Fatalf("authorized PostgreSQL stats = %+v", stats)
	}
	workbench, err := NewCrossProjectWorkbenchService(runtimeDB)
	if err != nil {
		t.Fatal(err)
	}
	workbench.now = analyticsFixtureNow
	dashboard, err := workbench.Dashboard(
		context.Background(),
		WorkbenchDashboardQuery{UserID: 1, Days: 7},
	)
	if err != nil {
		t.Fatal(err)
	}
	if dashboard.Summary.Total != 2 ||
		len(dashboard.SelectedProjects) != 2 ||
		len(dashboard.ProjectBreakdown) != 2 {
		t.Fatalf("membership-scoped PostgreSQL dashboard = %+v", dashboard)
	}

	if err := adminScoped.Exec(`
		UPDATE project_memberships
		SET is_active = FALSE
		WHERE project_id = 2 AND user_id = 1
	`).Error; err != nil {
		t.Fatal(err)
	}
	_, err = service.GetBusinessStats(
		context.Background(),
		mustHumanAnalyticsAuthorizedProjectSet(t, 1, 10, []uint{1, 2}),
	)
	if !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf("PostgreSQL membership revocation error = %v", err)
	}

	stats, err = service.GetBusinessStats(
		context.Background(),
		mustHumanAnalyticsAuthorizedProjectSet(t, 1, 10, []uint{1}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TicketStats.Total != 1 || stats.ActivityStats.TotalComments != 1 {
		t.Fatalf("PostgreSQL revocation was not immediate: %+v", stats)
	}
	dashboard, err = workbench.Dashboard(
		context.Background(),
		WorkbenchDashboardQuery{UserID: 1, Days: 7},
	)
	if err != nil {
		t.Fatal(err)
	}
	if dashboard.Summary.Total != 1 ||
		len(dashboard.SelectedProjects) != 1 ||
		dashboard.SelectedProjects[0].Key != "OPS" {
		t.Fatalf("dashboard revocation was not immediate: %+v", dashboard)
	}
}

const analyticsPostgresProjectPredicate = `
organization_id = NULLIF(
	current_setting('chronodesk.organization_id', true),
	''
)::bigint
AND project_id = ANY(
	COALESCE(
		string_to_array(
			NULLIF(
				current_setting('chronodesk.project_ids', true),
				''
			),
			','
		)::bigint[],
		ARRAY[]::bigint[]
	)
)`

func createAnalyticsPostgresSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	statements := []string{
		`CREATE TABLE projects (
			id BIGINT PRIMARY KEY,
			organization_id BIGINT NOT NULL,
			key TEXT NOT NULL,
			name TEXT NOT NULL,
			status TEXT NOT NULL
		)`,
		`CREATE TABLE tickets (
			id BIGINT PRIMARY KEY,
			organization_id BIGINT NOT NULL,
			project_id BIGINT NOT NULL,
			category_id BIGINT,
			status TEXT NOT NULL,
			priority TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL,
			deleted_at TIMESTAMPTZ,
			due_date TIMESTAMPTZ,
			sla_breached BOOLEAN NOT NULL DEFAULT FALSE,
			assigned_to_actor_type TEXT,
			assigned_to_actor_id TEXT,
			response_time BIGINT,
			resolution_time BIGINT
		)`,
		`CREATE TABLE ticket_comments (
			id BIGINT PRIMARY KEY,
			organization_id BIGINT NOT NULL,
			project_id BIGINT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL,
			deleted_at TIMESTAMPTZ
		)`,
		`CREATE TABLE categories (
			id BIGINT PRIMARY KEY,
			name TEXT NOT NULL
		)`,
		`CREATE TABLE users (
			id BIGINT PRIMARY KEY,
			status TEXT NOT NULL,
			platform_role TEXT NOT NULL,
			deleted_at TIMESTAMPTZ
		)`,
		`CREATE TABLE project_memberships (
			id BIGINT PRIMARY KEY,
			project_id BIGINT NOT NULL,
			user_id BIGINT NOT NULL,
			role TEXT NOT NULL,
			is_active BOOLEAN NOT NULL
		)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create PostgreSQL analytics table: %v", err)
		}
	}
}

func seedAnalyticsPostgresFixtures(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := analyticsFixtureNow()
	statements := []struct {
		query string
		args  []any
	}{
		{
			query: `INSERT INTO projects (id, organization_id, key, name, status) VALUES
				(1, 10, 'OPS', 'Operations', 'active'),
				(2, 10, 'FIN', 'Finance', 'active'),
				(3, 20, 'OTHER', 'Other organization', 'active')`,
		},
		{
			query: `INSERT INTO categories (id, name) VALUES
				(1, 'Incident'), (2, 'Request'), (3, 'Foreign')`,
		},
		{
			query: `INSERT INTO users (id, status, platform_role) VALUES
				(1, 'active', 'member'),
				(2, 'active', 'member'),
				(3, 'active', 'platform_admin')`,
		},
		{
			query: `INSERT INTO project_memberships (
				id, project_id, user_id, role, is_active
			) VALUES
				(1, 1, 1, 'project_admin', TRUE),
				(2, 2, 1, 'agent', TRUE),
				(3, 3, 3, 'project_admin', TRUE)`,
		},
		{
			query: `INSERT INTO tickets (
				id, organization_id, project_id, category_id, status, priority,
				created_at, updated_at, response_time, resolution_time
			) VALUES
				(1, 10, 1, 1, 'open', 'high', ?, ?, 60, NULL),
				(2, 10, 2, 2, 'resolved', 'low', ?, ?, NULL, 240),
				(3, 20, 3, 3, 'closed', 'normal', ?, ?, 9999, 9999)`,
			args: []any{
				now.Add(-time.Hour),
				now,
				now.Add(-time.Hour),
				now,
				now.Add(-time.Hour),
				now,
			},
		},
		{
			query: `INSERT INTO ticket_comments (
				id, organization_id, project_id, created_at
			) VALUES (1, 10, 1, ?), (2, 10, 2, ?), (3, 20, 3, ?)`,
			args: []any{now, now, now},
		},
	}
	for _, statement := range statements {
		if err := db.Exec(statement.query, statement.args...).Error; err != nil {
			t.Fatalf("seed PostgreSQL analytics fixtures: %v", err)
		}
	}
}

func quoteAnalyticsPostgresIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quoteAnalyticsPostgresLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}
