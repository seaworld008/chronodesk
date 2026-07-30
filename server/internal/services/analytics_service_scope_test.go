package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestAnalyticsBusinessStatsFailClosedWithoutAuthorizedProjects(t *testing.T) {
	db := openAnalyticsScopeTestDB(t)
	seedAnalyticsScopeFixtures(t, db)
	service := NewAnalyticsService(db)

	_, err := service.GetBusinessStats(
		context.Background(),
		AnalyticsAuthorizedProjectSet{},
	)
	if !errors.Is(err, ErrAnalyticsAuthorizedProjectSetRequired) {
		t.Fatalf("missing scope error = %v", err)
	}

	stats, err := service.GetBusinessStats(
		context.Background(),
		mustHumanAnalyticsAuthorizedProjectSet(t, 1, 10, nil),
	)
	if err != nil {
		t.Fatalf("empty authorized set: %v", err)
	}
	if stats.TicketStats.Total != 0 ||
		stats.MembershipStats.Total != 0 ||
		stats.ActivityStats.TotalComments != 0 {
		t.Fatalf("empty authorized set exposed business rows: %+v", stats)
	}
	if stats.TicketStats.AvgResponseTime != nil ||
		stats.TicketStats.AvgResolutionTime != nil {
		t.Fatalf("empty authorized set fabricated duration metrics: %+v", stats.TicketStats)
	}
	if stats.TicketStats.ByCategory == nil {
		t.Fatal("empty authorized set must return a stable empty category map")
	}
}

func TestAnalyticsBusinessStatsUsesOnlyAuthorizedSubsetAndReflectsRevocation(t *testing.T) {
	db := openAnalyticsScopeTestDB(t)
	seedAnalyticsScopeFixtures(t, db)
	service := NewAnalyticsService(db)
	ctx := context.Background()

	stats, err := service.GetBusinessStats(
		ctx,
		mustHumanAnalyticsAuthorizedProjectSet(t, 1, 10, []uint{1, 2}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TicketStats.Total != 2 ||
		stats.ActivityStats.TotalComments != 2 ||
		stats.MembershipStats.Total != 2 {
		t.Fatalf("authorized subset stats = %+v", stats)
	}
	if stats.TicketStats.AvgResponseTime == nil ||
		*stats.TicketStats.AvgResponseTime != 1 {
		t.Fatalf("real persisted response duration = %v, want 1 hour", stats.TicketStats.AvgResponseTime)
	}
	if stats.TicketStats.AvgResolutionTime == nil ||
		*stats.TicketStats.AvgResolutionTime != 4 {
		t.Fatalf("real persisted resolution duration = %v, want 4 hours", stats.TicketStats.AvgResolutionTime)
	}

	// The Adapter must resolve memberships for every request. Passing the
	// freshly reduced server-side set proves the next query cannot retain a
	// previously authorized project in service state.
	stats, err = service.GetBusinessStats(
		ctx,
		mustHumanAnalyticsAuthorizedProjectSet(t, 1, 10, []uint{1}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TicketStats.Total != 1 ||
		stats.ActivityStats.TotalComments != 1 ||
		stats.MembershipStats.Total != 1 {
		t.Fatalf("revoked project remained visible: %+v", stats)
	}
	if stats.TicketStats.AvgResolutionTime != nil {
		t.Fatalf("missing resolution sample must be unavailable, got %v", *stats.TicketStats.AvgResolutionTime)
	}
}

func TestAnalyticsRejectsCrossOrganizationAuthorizedProjectSet(t *testing.T) {
	db := openAnalyticsScopeTestDB(t)
	seedAnalyticsScopeFixtures(t, db)

	_, err := NewAnalyticsService(db).GetBusinessStats(
		context.Background(),
		mustHumanAnalyticsAuthorizedProjectSet(t, 1, 10, []uint{1, 3}),
	)
	if err == nil || !strings.Contains(err.Error(), "cross-organization") {
		t.Fatalf("cross-organization set error = %v", err)
	}
}

func TestAnalyticsTimeRangeStatsUsesAuthorizedProjectTransaction(t *testing.T) {
	db := openAnalyticsScopeTestDB(t)
	seedAnalyticsScopeFixtures(t, db)
	now := analyticsFixtureNow()

	stats, err := NewAnalyticsService(db).GetTimeRangeStats(
		context.Background(),
		mustHumanAnalyticsAuthorizedProjectSet(t, 1, 10, []uint{1}),
		now.Add(-48*time.Hour),
		now.Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	if countDailyValues(stats.TicketTrend) != 1 ||
		countDailyValues(stats.CommentTrend) != 1 ||
		countDailyValues(stats.UserActivityTrend) != 1 {
		t.Fatalf("project trend scope leaked or omitted rows: %+v", stats)
	}
}

func TestAnalyticsRevalidatesHumanAccessBeforeEveryBusinessQuery(t *testing.T) {
	tests := []struct {
		name   string
		revoke string
	}{
		{
			name: "membership revoked",
			revoke: `UPDATE project_memberships
				SET is_active = FALSE
				WHERE project_id = 2 AND user_id = 1`,
		},
		{
			name:   "human suspended",
			revoke: `UPDATE users SET status = 'suspended' WHERE id = 1`,
		},
		{
			name:   "project archived",
			revoke: `UPDATE projects SET status = 'archived' WHERE id = 2`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openAnalyticsScopeTestDB(t)
			seedAnalyticsScopeFixtures(t, db)
			if err := db.Exec(test.revoke).Error; err != nil {
				t.Fatal(err)
			}

			businessQueries := 0
			callbackName := "test:analytics-business-query-order"
			if err := db.Callback().Query().
				Before("gorm:query").
				Register(callbackName, func(tx *gorm.DB) {
					switch tx.Statement.Table {
					case "tickets", "ticket_comments", "login_histories":
						businessQueries++
					}
				}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = db.Callback().Query().Remove(callbackName)
			})

			service := NewAnalyticsService(db)
			authorized := mustHumanAnalyticsAuthorizedProjectSet(
				t,
				1,
				10,
				[]uint{1, 2},
			)
			_, err := service.GetBusinessStats(
				context.Background(),
				authorized,
			)
			if !errors.Is(err, ErrProjectAccessDenied) {
				t.Fatalf("business revocation error = %v", err)
			}
			now := analyticsFixtureNow()
			_, err = service.GetTimeRangeStats(
				context.Background(),
				authorized,
				now.Add(-24*time.Hour),
				now.Add(time.Hour),
			)
			if !errors.Is(err, ErrProjectAccessDenied) {
				t.Fatalf("trend revocation error = %v", err)
			}
			if businessQueries != 0 {
				t.Fatalf(
					"authorization revocation allowed %d business query callbacks",
					businessQueries,
				)
			}
		})
	}
}

func TestAnalyticsSystemStatsOmitsUnavailableUptime(t *testing.T) {
	stats, err := NewAnalyticsService(openTestDB(t)).GetSystemStats()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(stats)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"uptime"`) {
		t.Fatalf("runtime without a lifecycle metric source fabricated uptime: %s", encoded)
	}
}

func countDailyValues(values []DailyCount) int64 {
	var total int64
	for _, value := range values {
		total += value.Count
	}
	return total
}

func mustHumanAnalyticsAuthorizedProjectSet(
	t *testing.T,
	userID uint,
	organizationID uint,
	projectIDs []uint,
) AnalyticsAuthorizedProjectSet {
	t.Helper()
	authorized, err := NewHumanAnalyticsAuthorizedProjectSet(
		userID,
		organizationID,
		projectIDs,
	)
	if err != nil {
		t.Fatalf("create human analytics authorization: %v", err)
	}
	return authorized
}

func openAnalyticsScopeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := openTestDB(t)
	statements := []string{
		`CREATE TABLE projects (
			id INTEGER PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			status TEXT NOT NULL
		)`,
		`CREATE TABLE tickets (
			id INTEGER PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			category_id INTEGER,
			status TEXT NOT NULL,
			priority TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			response_time INTEGER,
			resolution_time INTEGER
		)`,
		`CREATE TABLE ticket_comments (
			id INTEGER PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			created_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE categories (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL
		)`,
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY,
			status TEXT NOT NULL,
			platform_role TEXT NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE project_memberships (
			id INTEGER PRIMARY KEY,
			project_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			role TEXT NOT NULL,
			is_active BOOLEAN NOT NULL
		)`,
		`CREATE TABLE login_histories (
			id INTEGER PRIMARY KEY,
			user_id INTEGER NOT NULL,
			login_time DATETIME NOT NULL
		)`,
		`CREATE TABLE cleanup_logs (
			id INTEGER PRIMARY KEY,
			start_time DATETIME NOT NULL
		)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create analytics fixture schema: %v", err)
		}
	}
	return db
}

func seedAnalyticsScopeFixtures(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := analyticsFixtureNow()
	commands := []struct {
		query string
		args  []any
	}{
		{
			query: `INSERT INTO projects (id, organization_id, status) VALUES
				(1, 10, 'active'), (2, 10, 'active'), (3, 20, 'active')`,
		},
		{
			query: `INSERT INTO categories (id, name) VALUES
				(1, 'Incident'), (2, 'Request'), (3, 'Foreign')`,
		},
		{
			query: `INSERT INTO tickets (
				id, organization_id, project_id, category_id, status, priority,
				created_at, updated_at, response_time, resolution_time
			) VALUES
				(1, 10, 1, 1, 'open', 'high', ?, ?, 60, NULL),
				(2, 10, 2, 2, 'resolved', 'low', ?, ?, NULL, 240),
				(3, 20, 3, 3, 'closed', 'medium', ?, ?, 9999, 9999)`,
			args: []any{
				now.Add(-2 * time.Hour), now,
				now.Add(-26 * time.Hour), now,
				now.Add(-3 * time.Hour), now,
			},
		},
		{
			query: `INSERT INTO ticket_comments (
				id, organization_id, project_id, created_at
			) VALUES (1, 10, 1, ?), (2, 10, 2, ?), (3, 20, 3, ?)`,
			args: []any{now, now.Add(-24 * time.Hour), now},
		},
		{
			query: `INSERT INTO users (id, status, platform_role) VALUES
				(1, 'active', 'platform_admin'),
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
			query: `INSERT INTO login_histories (id, user_id, login_time) VALUES
				(1, 1, ?), (2, 2, ?), (3, 3, ?)`,
			args: []any{now, now, now},
		},
	}
	for _, command := range commands {
		if err := db.Exec(command.query, command.args...).Error; err != nil {
			t.Fatalf("seed analytics fixtures: %v", err)
		}
	}
}

func analyticsFixtureNow() time.Time {
	return time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
}

func TestAnalyticsPlatformStatsRemainSeparateFromProjectBusinessStats(t *testing.T) {
	db := openAnalyticsScopeTestDB(t)
	seedAnalyticsScopeFixtures(t, db)

	stats, err := NewAnalyticsService(db).GetPlatformStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Users.Total != 3 ||
		stats.Users.PlatformAdmins != 2 ||
		stats.Users.Members != 1 {
		t.Fatalf("platform identity stats = %+v", stats.Users)
	}
}
