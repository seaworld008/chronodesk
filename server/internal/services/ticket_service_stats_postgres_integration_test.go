package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestTicketStatisticsUnderNonOwnerPostgresRLSAndCacheRevocation(
	t *testing.T,
) {
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip(
			"set CHRONODESK_POSTGRES_INTEGRATION=1 for PostgreSQL ticket statistics RLS evidence",
		)
	}
	rawDSN := strings.TrimSpace(
		os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"),
	)
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
			t.Fatal(
				"PostgreSQL ticket statistics integration test requires a loopback target",
			)
		}
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	schemaName := "chronodesk_ticket_stats_" + suffix
	roleName := "chronodesk_ticket_stats_runtime_" + suffix
	rolePassword := "ChronodeskTicketStats" + suffix
	quotedSchema := quoteTicketStatsPostgresIdentifier(schemaName)
	quotedRole := quoteTicketStatsPostgresIdentifier(roleName)

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
		_ = admin.Exec(
			"DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE",
		).Error
		if roleCreated {
			_ = admin.Exec("DROP ROLE IF EXISTS " + quotedRole).Error
		}
		_ = adminSQL.Close()
	})

	if err := admin.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create ticket statistics schema: %v", err)
	}
	adminScopedURL := *parsed
	adminQuery := adminScopedURL.Query()
	adminQuery.Set("search_path", schemaName)
	adminScopedURL.RawQuery = adminQuery.Encode()
	adminScoped, err := gorm.Open(
		postgres.Open(adminScopedURL.String()),
		&gorm.Config{
			TranslateError: true,
			Logger:         logger.Default.LogMode(logger.Silent),
		},
	)
	if err != nil {
		t.Fatalf("open schema-scoped PostgreSQL admin: %v", err)
	}
	adminScopedSQL, err := adminScoped.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adminScopedSQL.Close() })

	createTicketStatsPostgresSchema(t, adminScoped)
	seedTicketStatsPostgresFixtures(t, adminScoped)
	if err := adminScoped.Exec(
		"ALTER TABLE tickets ENABLE ROW LEVEL SECURITY",
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := adminScoped.Exec(
		"ALTER TABLE tickets FORCE ROW LEVEL SECURITY",
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := adminScoped.Exec(`
		CREATE POLICY chronodesk_ticket_statistics_project_scope
		ON tickets
		FOR SELECT
		TO PUBLIC
		USING (
			organization_id = NULLIF(
				current_setting('chronodesk.organization_id', true),
				''
			)::bigint
			AND project_id = NULLIF(
				current_setting('chronodesk.project_id', true),
				''
			)::bigint
		)
	`).Error; err != nil {
		t.Fatal(err)
	}

	if err := admin.Exec(
		"CREATE ROLE " + quotedRole +
			" LOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS PASSWORD " +
			quoteTicketStatsPostgresLiteral(rolePassword),
	).Error; err != nil {
		t.Fatalf("create ticket statistics runtime role: %v", err)
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
	// SELECT ... FOR SHARE requires UPDATE privilege on the authorization
	// relations. Business facts remain read-only for the runtime role.
	if err := adminScoped.Exec(
		"GRANT UPDATE ON " +
			quoteTicketStatsPostgresIdentifier("projects") + ", " +
			quoteTicketStatsPostgresIdentifier("users") + ", " +
			quoteTicketStatsPostgresIdentifier("project_memberships") +
			" TO " + quotedRole,
	).Error; err != nil {
		t.Fatal(err)
	}

	runtimeURL := adminScopedURL
	runtimeURL.User = url.UserPassword(roleName, rolePassword)
	runtimeDB, err := gorm.Open(
		postgres.Open(runtimeURL.String()),
		&gorm.Config{
			TranslateError: true,
			Logger:         logger.Default.LogMode(logger.Silent),
		},
	)
	if err != nil {
		t.Fatalf("open non-owner ticket statistics runtime: %v", err)
	}
	runtimeSQL, err := runtimeDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	// A single physical connection makes the post-operation assertions below
	// prove SET LOCAL was cleared instead of accidentally checking a different
	// clean session from the pool.
	runtimeSQL.SetMaxOpenConns(1)
	runtimeSQL.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = runtimeSQL.Close() })

	assertTicketStatsPostgresRuntimeRole(t, runtimeDB, roleName)
	var unscopedTickets int64
	if err := runtimeDB.Table("tickets").Count(&unscopedTickets).Error; err != nil {
		t.Fatal(err)
	}
	if unscopedTickets != 0 {
		t.Fatalf(
			"FORCE RLS exposed %d tickets without transaction-local scope",
			unscopedTickets,
		)
	}

	cache := newTicketStatsPostgresBarrierCache()
	service, err := NewTicketService(
		runtimeDB,
		NewAgentNativeService(runtimeDB),
		cache,
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	projectScope := models.ProjectScope{
		OrganizationID: 10,
		ProjectID:      101,
	}
	projectContext := ticketStatsPostgresHumanContext(
		t,
		projectScope,
		1,
	)

	stats, err := service.GetTicketStatistics(
		projectContext,
		1,
		string(models.ProjectRoleAdmin),
	)
	if err != nil {
		t.Fatalf("get PostgreSQL ticket statistics: %v", err)
	}
	if stats.Total != 2 ||
		stats.Open != 1 ||
		stats.Resolved != 1 ||
		stats.HighPriority != 1 ||
		len(stats.ByPriority) != 2 ||
		stats.ByPriority[string(models.TicketPriorityHigh)] != 1 ||
		stats.ByPriority[string(models.TicketPriorityLow)] != 1 ||
		stats.ByPriority[string(models.TicketPriorityUrgent)] != 0 {
		t.Fatalf(
			"project 101 statistics leaked another project: %+v",
			stats,
		)
	}
	assertTicketStatsPostgresScopeCleared(t, runtimeDB)

	cacheKey, cachedPayload, ok := cache.onlyEntry()
	if !ok {
		t.Fatal("ticket statistics response was not cached")
	}
	const expectedCacheKey = "ticket_stats:v2:10:101:human:1:" +
		"project_admin:membership:1001:1"
	if cacheKey != expectedCacheKey {
		t.Fatalf(
			"ticket statistics cache key = %q, want %q",
			cacheKey,
			expectedCacheKey,
		)
	}
	var envelope ticketStatisticsCacheEnvelope
	if err := json.Unmarshal([]byte(cachedPayload), &envelope); err != nil {
		t.Fatalf("decode ticket statistics cache envelope: %v", err)
	}
	if envelope.OrganizationID != 10 ||
		envelope.ProjectID != 101 ||
		envelope.UserID != 1 ||
		envelope.ProjectRole != models.ProjectRoleAdmin ||
		envelope.MembershipID != 1001 ||
		envelope.MembershipVersion != 1 ||
		envelope.Statistics.Total != 2 {
		t.Fatalf(
			"ticket statistics cache envelope is not bound to live project authorization: %+v",
			envelope,
		)
	}

	unauthorizedContext := ticketStatsPostgresHumanContext(
		t,
		models.ProjectScope{
			OrganizationID: 10,
			ProjectID:      102,
		},
		1,
	)
	if unauthorizedStats, unauthorizedErr := service.GetTicketStatistics(
		unauthorizedContext,
		1,
		string(models.ProjectRoleAdmin),
	); !errors.Is(unauthorizedErr, ErrProjectAccessDenied) ||
		unauthorizedStats != nil {
		t.Fatalf(
			"unauthorized project statistics = (%+v, %v), want nil access denied",
			unauthorizedStats,
			unauthorizedErr,
		)
	}

	cacheHit := make(chan struct{})
	resumeAfterRevocation := make(chan struct{})
	resumeClosed := false
	defer func() {
		if !resumeClosed {
			close(resumeAfterRevocation)
		}
	}()
	cache.blockNextHit(cacheHit, resumeAfterRevocation)
	type statisticsResult struct {
		stats *TicketStatisticsResponse
		err   error
	}
	result := make(chan statisticsResult, 1)
	go func() {
		response, responseErr := service.GetTicketStatistics(
			projectContext,
			1,
			string(models.ProjectRoleAdmin),
		)
		result <- statisticsResult{stats: response, err: responseErr}
	}()

	select {
	case <-cacheHit:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a real ticket statistics cache hit")
	}
	revokeContext, cancelRevoke := context.WithTimeout(
		context.Background(),
		3*time.Second,
	)
	defer cancelRevoke()
	if err := adminScoped.WithContext(revokeContext).Transaction(
		func(tx *gorm.DB) error {
			if lockErr := tx.Exec(
				"SET LOCAL lock_timeout = '1500ms'",
			).Error; lockErr != nil {
				return lockErr
			}
			return tx.Exec(`
				UPDATE project_memberships
				SET
					is_active = FALSE,
					version = version + 1,
					updated_at = NOW()
				WHERE id = 1001
			`).Error
		},
	); err != nil {
		t.Fatalf("revoke membership after cache hit: %v", err)
	}
	close(resumeAfterRevocation)
	resumeClosed = true

	select {
	case response := <-result:
		if !errors.Is(response.err, ErrProjectAccessDenied) {
			t.Fatalf(
				"cache hit after PostgreSQL membership revocation error = %v",
				response.err,
			)
		}
		if response.stats != nil {
			t.Fatalf(
				"cache hit returned stale statistics after revocation: %+v",
				response.stats,
			)
		}
	case <-time.After(5 * time.Second):
		t.Fatal(
			"ticket statistics request did not complete after membership revocation",
		)
	}
}

func createTicketStatsPostgresSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	statements := []string{
		`CREATE TABLE projects (
			id BIGINT PRIMARY KEY,
			public_id TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL,
			organization_id BIGINT NOT NULL,
			business_unit_id BIGINT NOT NULL,
			key TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT NOT NULL,
			status TEXT NOT NULL,
			ticket_sequence BIGINT NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE users (
			id BIGINT PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL,
			deleted_at TIMESTAMPTZ,
			username TEXT NOT NULL,
			email TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			platform_role TEXT NOT NULL,
			status TEXT NOT NULL
		)`,
		`CREATE TABLE project_memberships (
			id BIGINT PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL,
			version BIGINT NOT NULL,
			project_id BIGINT NOT NULL,
			user_id BIGINT NOT NULL,
			role TEXT NOT NULL,
			is_active BOOLEAN NOT NULL
		)`,
		`CREATE TABLE tickets (
			id BIGINT PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL,
			deleted_at TIMESTAMPTZ,
			organization_id BIGINT NOT NULL,
			project_id BIGINT NOT NULL,
			status TEXT NOT NULL,
			priority TEXT NOT NULL,
			due_date TIMESTAMPTZ,
			created_by_id BIGINT,
			assigned_to_id BIGINT,
			sla_breached BOOLEAN NOT NULL DEFAULT FALSE,
			is_escalated BOOLEAN NOT NULL DEFAULT FALSE
		)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create PostgreSQL ticket statistics table: %v", err)
		}
	}
}

func seedTicketStatsPostgresFixtures(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	statements := []struct {
		query string
		args  []any
	}{
		{
			query: `INSERT INTO projects (
				id, public_id, created_at, updated_at, organization_id,
				business_unit_id, key, name, description, status
			) VALUES
				(101, '00000000-0000-7000-8000-000000000101', ?, ?, 10, 1,
					'ALPHA', 'Alpha', '', 'active'),
				(102, '00000000-0000-7000-8000-000000000102', ?, ?, 10, 1,
					'BETA', 'Beta', '', 'active')`,
			args: []any{now, now, now, now},
		},
		{
			query: `INSERT INTO users (
				id, created_at, updated_at, username, email, password_hash,
				platform_role, status
			) VALUES
				(1, ?, ?, 'stats-user', 'stats-user@example.test', 'hash',
					'member', 'active')`,
			args: []any{now, now},
		},
		{
			query: `INSERT INTO project_memberships (
				id, created_at, updated_at, version, project_id, user_id, role,
				is_active
			) VALUES
				(1001, ?, ?, 1, 101, 1, 'project_admin', TRUE)`,
			args: []any{now, now},
		},
		{
			query: `INSERT INTO tickets (
				id, created_at, updated_at, organization_id, project_id, status,
				priority, due_date, created_by_id, assigned_to_id,
				sla_breached, is_escalated
			) VALUES
				(2001, ?, ?, 10, 101, 'open', 'high', ?, 1, NULL, TRUE, FALSE),
				(2002, ?, ?, 10, 101, 'resolved', 'low', NULL, 1, 1, FALSE, TRUE),
				(2101, ?, ?, 10, 102, 'pending', 'urgent', ?, 1, NULL, TRUE, TRUE),
				(2102, ?, ?, 10, 102, 'open', 'urgent', ?, 1, NULL, TRUE, TRUE),
				(2103, ?, ?, 10, 102, 'closed', 'urgent', NULL, 1, 1, FALSE, TRUE)`,
			args: []any{
				now,
				now,
				now.Add(-time.Hour),
				now,
				now,
				now,
				now,
				now.Add(-time.Hour),
				now,
				now,
				now.Add(-time.Hour),
				now,
				now,
			},
		},
	}
	for _, statement := range statements {
		if err := db.Exec(statement.query, statement.args...).Error; err != nil {
			t.Fatalf("seed PostgreSQL ticket statistics fixtures: %v", err)
		}
	}
}

func assertTicketStatsPostgresRuntimeRole(
	t *testing.T,
	db *gorm.DB,
	roleName string,
) {
	t.Helper()
	roleState := struct {
		CurrentUser string `gorm:"column:current_user"`
		Superuser   bool
		BypassRLS   bool `gorm:"column:bypass_rls"`
		Inherit     bool
		TableOwner  string
		RLSEnabled  bool `gorm:"column:rls_enabled"`
		RLSForced   bool `gorm:"column:rls_forced"`
		TicketWrite bool `gorm:"column:ticket_write"`
	}{}
	if err := db.Raw(`
		SELECT
			current_user,
			role.rolsuper AS superuser,
			role.rolbypassrls AS bypass_rls,
			role.rolinherit AS inherit,
			owner.rolname AS table_owner,
			table_state.relrowsecurity AS rls_enabled,
			table_state.relforcerowsecurity AS rls_forced,
			has_table_privilege(
				current_user,
				'tickets',
				'INSERT, UPDATE, DELETE'
			) AS ticket_write
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
		roleState.Inherit ||
		roleState.TableOwner == roleName ||
		!roleState.RLSEnabled ||
		!roleState.RLSForced ||
		roleState.TicketWrite {
		t.Fatalf(
			"ticket statistics runtime role is not least privilege: %+v",
			roleState,
		)
	}
}

func assertTicketStatsPostgresScopeCleared(
	t *testing.T,
	db *gorm.DB,
) {
	t.Helper()
	settings := struct {
		OrganizationID string `gorm:"column:organization_id"`
		ProjectID      string `gorm:"column:project_id"`
		ProjectIDs     string `gorm:"column:project_ids"`
	}{}
	if err := db.Raw(`
		SELECT
			COALESCE(
				current_setting('chronodesk.organization_id', true),
				''
			) AS organization_id,
			COALESCE(
				current_setting('chronodesk.project_id', true),
				''
			) AS project_id,
			COALESCE(
				current_setting('chronodesk.project_ids', true),
				''
			) AS project_ids
	`).Scan(&settings).Error; err != nil {
		t.Fatal(err)
	}
	if settings.OrganizationID != "" ||
		settings.ProjectID != "" ||
		settings.ProjectIDs != "" {
		t.Fatalf(
			"transaction-local project scope leaked into the session: %+v",
			settings,
		)
	}
	var unscopedTickets int64
	if err := db.Table("tickets").Count(&unscopedTickets).Error; err != nil {
		t.Fatal(err)
	}
	if unscopedTickets != 0 {
		t.Fatalf(
			"FORCE RLS exposed %d tickets after the scoped transaction committed",
			unscopedTickets,
		)
	}
}

func ticketStatsPostgresHumanContext(
	t *testing.T,
	scope models.ProjectScope,
	userID uint,
) context.Context {
	t.Helper()
	ctx, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  scope,
			Actor:  models.HumanActor(userID),
			Source: SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

type ticketStatsPostgresBarrierCache struct {
	mu                sync.Mutex
	values            map[string]string
	nextHitReached    chan<- struct{}
	nextHitResume     <-chan struct{}
	nextHitHasBlocked bool
}

func newTicketStatsPostgresBarrierCache() *ticketStatsPostgresBarrierCache {
	return &ticketStatsPostgresBarrierCache{
		values: make(map[string]string),
	}
}

func (cache *ticketStatsPostgresBarrierCache) Get(
	_ context.Context,
	key string,
) (string, error) {
	cache.mu.Lock()
	value, found := cache.values[key]
	reached := cache.nextHitReached
	resume := cache.nextHitResume
	shouldBlock := found && value != "" && reached != nil &&
		resume != nil && !cache.nextHitHasBlocked
	if shouldBlock {
		cache.nextHitHasBlocked = true
	}
	cache.mu.Unlock()

	if shouldBlock {
		close(reached)
		<-resume
	}
	if !found {
		return "", gorm.ErrRecordNotFound
	}
	return value, nil
}

func (cache *ticketStatsPostgresBarrierCache) Set(
	_ context.Context,
	key string,
	value interface{},
	_ time.Duration,
) error {
	encoded, ok := value.(string)
	if !ok {
		return errors.New("ticket statistics cache requires a string value")
	}
	cache.mu.Lock()
	cache.values[key] = encoded
	cache.mu.Unlock()
	return nil
}

func (cache *ticketStatsPostgresBarrierCache) blockNextHit(
	reached chan<- struct{},
	resume <-chan struct{},
) {
	cache.mu.Lock()
	cache.nextHitReached = reached
	cache.nextHitResume = resume
	cache.nextHitHasBlocked = false
	cache.mu.Unlock()
}

func (cache *ticketStatsPostgresBarrierCache) onlyEntry() (
	string,
	string,
	bool,
) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.values) != 1 {
		return "", "", false
	}
	for key, value := range cache.values {
		return key, value, true
	}
	return "", "", false
}

func quoteTicketStatsPostgresIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quoteTicketStatsPostgresLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}
