package services

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestWorkbenchDashboardDefaultAllAndExplicitFilters(t *testing.T) {
	db := crossProjectWorkbenchTestDB(t)
	seedCrossProjectWorkbench(t, db, 7)
	seedWorkbenchDashboardProject(t, db)
	if err := db.Exec(`
		INSERT INTO projects (id, organization_id, key, name, status)
		VALUES (50, 1, 'EMPTY', 'Empty project', 'active');
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		INSERT INTO project_memberships (id, project_id, user_id, role, is_active)
		VALUES (5, 50, 7, 'observer', TRUE);
	`).Error; err != nil {
		t.Fatal(err)
	}
	service, err := NewCrossProjectWorkbenchService(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	all, err := service.Dashboard(context.Background(), WorkbenchDashboardQuery{
		UserID: 7,
		Days:   7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.SelectedProjects) != 3 || all.Summary.Total != 7 {
		t.Fatalf("default-all dashboard = %+v", all)
	}
	if all.Summary.Status.Open != 6 ||
		all.Summary.Status.Resolved != 1 ||
		all.Summary.Priority.Normal != 7 ||
		all.Summary.SLABreached != 1 ||
		all.Summary.Overdue != 1 ||
		all.Summary.Assignment.Human != 4 ||
		all.Summary.Assignment.ServicePrincipal != 2 ||
		all.Summary.Assignment.Unassigned != 1 {
		t.Fatalf("unexpected dashboard summary: %+v", all.Summary)
	}
	if len(all.DailyTrend) != 7 ||
		len(all.ProjectBreakdown) != 3 ||
		all.ProjectBreakdown[2].ProjectKey != "EMPTY" ||
		all.ProjectBreakdown[2].Total != 0 {
		t.Fatalf("bounded breakdowns missing: %+v", all)
	}

	emptyProject, err := service.Dashboard(
		context.Background(),
		WorkbenchDashboardQuery{
			UserID:      7,
			ProjectKeys: []models.ProjectKey{"EMPTY"},
			HasFilter:   true,
			Days:        30,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if emptyProject.Summary.Total != 0 ||
		len(emptyProject.DailyTrend) != 30 ||
		len(emptyProject.ProjectBreakdown) != 1 ||
		emptyProject.ProjectBreakdown[0].Total != 0 {
		t.Fatalf("zero-ticket project dashboard = %+v", emptyProject)
	}

	single, err := service.Dashboard(context.Background(), WorkbenchDashboardQuery{
		UserID:      7,
		ProjectKeys: []models.ProjectKey{"FIN"},
		HasFilter:   true,
		Days:        30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if single.Summary.Total != 3 ||
		len(single.SelectedProjects) != 1 ||
		single.SelectedProjects[0].Key != "FIN" {
		t.Fatalf("single project dashboard = %+v", single)
	}

	multi, err := service.Dashboard(context.Background(), WorkbenchDashboardQuery{
		UserID:      7,
		ProjectKeys: []models.ProjectKey{"OPS", "FIN"},
		HasFilter:   true,
		Days:        90,
	})
	if err != nil {
		t.Fatal(err)
	}
	if multi.Summary.Total != all.Summary.Total ||
		len(multi.SelectedProjects) != 2 {
		t.Fatalf("multi-project dashboard = %+v", multi)
	}
}

func TestWorkbenchDashboardExcludesOldSevenDayTicketsAndHonorsNinetyDayBoundary(
	t *testing.T,
) {
	db := crossProjectWorkbenchTestDB(t)
	seedCrossProjectWorkbench(t, db, 7)
	seedWorkbenchDashboardProject(t, db)
	service, err := NewCrossProjectWorkbenchService(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	insertTicket := func(
		id uint,
		createdAt time.Time,
		status, priority string,
		slaBreached bool,
		assignedType, assignedID any,
	) {
		t.Helper()
		if err := db.Exec(`
			INSERT INTO tickets (
				id, public_id, organization_id, project_id, ticket_number, title,
				type, priority, status, created_by_actor_type,
				created_by_actor_id, assigned_to_actor_type,
				assigned_to_actor_id, due_date, sla_breached, version,
				created_at, updated_at
			) VALUES (
				?, ?, 1, 10, ?, ?, 'request', ?, ?, 'human', '7', ?, ?,
				?, ?, 1, ?, ?
			)
		`,
			id,
			fmt.Sprintf("dashboard-window-%d", id),
			fmt.Sprintf("OPS-%d", id),
			fmt.Sprintf("Window ticket %d", id),
			priority,
			status,
			assignedType,
			assignedID,
			time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
			slaBreached,
			createdAt,
			createdAt,
		).Error; err != nil {
			t.Fatalf("insert dashboard window ticket %d: %v", id, err)
		}
	}

	sevenDayStart := dashboardDay(now).AddDate(0, 0, -6)
	ninetyDayStart := dashboardDay(now).AddDate(0, 0, -89)
	insertTicket(
		601,
		sevenDayStart.Add(-time.Second),
		"pending",
		"high",
		true,
		nil,
		nil,
	)
	insertTicket(
		602,
		ninetyDayStart,
		"in_progress",
		"critical",
		true,
		"human",
		"7",
	)
	insertTicket(
		603,
		ninetyDayStart.Add(-time.Second),
		"cancelled",
		"low",
		true,
		nil,
		nil,
	)
	insertTicket(
		604,
		now.Add(time.Second),
		"open",
		"urgent",
		true,
		nil,
		nil,
	)

	sevenDays, err := service.Dashboard(
		context.Background(),
		WorkbenchDashboardQuery{UserID: 7, Days: 7},
	)
	if err != nil {
		t.Fatal(err)
	}
	if sevenDays.Summary.Total != 7 ||
		sevenDays.Summary.Status.Pending != 0 ||
		sevenDays.Summary.Status.InProgress != 0 ||
		sevenDays.Summary.Priority.High != 0 ||
		sevenDays.Summary.Priority.Critical != 0 ||
		sevenDays.Summary.Priority.Low != 0 ||
		sevenDays.Summary.Priority.Urgent != 0 ||
		sevenDays.Summary.SLABreached != 1 ||
		sevenDays.Summary.Overdue != 1 ||
		sevenDays.Summary.Assignment.Human != 4 ||
		sevenDays.Summary.Assignment.ServicePrincipal != 2 ||
		sevenDays.Summary.Assignment.Unassigned != 1 {
		t.Fatalf(
			"seven-day summary included out-of-window tickets: %+v",
			sevenDays.Summary,
		)
	}
	sevenProjectTotals := make(map[models.ProjectKey]int64)
	var sevenTrendTotal int64
	for _, row := range sevenDays.ProjectBreakdown {
		sevenProjectTotals[row.ProjectKey] = row.Total
	}
	for _, point := range sevenDays.DailyTrend {
		sevenTrendTotal += point.Created
	}
	if sevenProjectTotals["OPS"] != 4 ||
		sevenProjectTotals["FIN"] != 3 ||
		sevenTrendTotal != 7 {
		t.Fatalf("seven-day breakdowns are inconsistent: %+v", sevenDays)
	}

	ninetyDays, err := service.Dashboard(
		context.Background(),
		WorkbenchDashboardQuery{UserID: 7, Days: 90},
	)
	if err != nil {
		t.Fatal(err)
	}
	if ninetyDays.Summary.Total != 9 ||
		ninetyDays.Summary.Status.Pending != 1 ||
		ninetyDays.Summary.Status.InProgress != 1 ||
		ninetyDays.Summary.Priority.High != 1 ||
		ninetyDays.Summary.Priority.Critical != 1 ||
		ninetyDays.Summary.Priority.Low != 0 ||
		ninetyDays.Summary.Priority.Urgent != 0 ||
		ninetyDays.Summary.SLABreached != 3 ||
		ninetyDays.Summary.Overdue != 3 ||
		ninetyDays.Summary.Assignment.Human != 5 ||
		ninetyDays.Summary.Assignment.ServicePrincipal != 2 ||
		ninetyDays.Summary.Assignment.Unassigned != 2 {
		t.Fatalf(
			"ninety-day boundary summary = %+v",
			ninetyDays.Summary,
		)
	}
	ninetyProjectTotals := make(map[models.ProjectKey]int64)
	var ninetyTrendTotal int64
	for _, row := range ninetyDays.ProjectBreakdown {
		ninetyProjectTotals[row.ProjectKey] = row.Total
	}
	for _, point := range ninetyDays.DailyTrend {
		ninetyTrendTotal += point.Created
	}
	if ninetyProjectTotals["OPS"] != 6 ||
		ninetyProjectTotals["FIN"] != 3 ||
		ninetyTrendTotal != 9 {
		t.Fatalf("ninety-day breakdowns are inconsistent: %+v", ninetyDays)
	}
}

func TestWorkbenchDashboardAggregatesAllButBoundsProjectArrays(t *testing.T) {
	db := crossProjectWorkbenchTestDB(t)
	if err := db.Exec(
		"INSERT INTO users (id, username, display_name) VALUES (7, 'owner', 'Owner')",
	).Error; err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 101; index++ {
		projectID := 1_000 + index
		if err := db.Exec(
			`INSERT INTO projects (id, organization_id, key, name, status)
			 VALUES (?, 1, ?, ?, 'active')`,
			projectID,
			fmt.Sprintf("P%03d", index),
			fmt.Sprintf("Project %03d", index),
		).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(
			`INSERT INTO project_memberships
			 (id, project_id, user_id, role, is_active)
			 VALUES (?, ?, 7, 'observer', TRUE)`,
			projectID,
			projectID,
		).Error; err != nil {
			t.Fatal(err)
		}
	}
	service, err := NewCrossProjectWorkbenchService(db)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Dashboard(
		context.Background(),
		WorkbenchDashboardQuery{UserID: 7, Days: 7},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.SelectedProjectCount != 101 ||
		len(result.SelectedProjects) != 100 ||
		!result.SelectedProjectsTruncated ||
		len(result.ProjectBreakdown) != 100 ||
		!result.ProjectBreakdownTruncated {
		t.Fatalf("dashboard project arrays are not bounded: %+v", result)
	}
	if result.Summary.Total != 0 || len(result.DailyTrend) != 7 {
		t.Fatalf("bounded response changed aggregate semantics: %+v", result)
	}
}

func TestWorkbenchDashboardFailsClosedAndNeverUsesPlatformRole(t *testing.T) {
	db := crossProjectWorkbenchTestDB(t)
	seedCrossProjectWorkbench(t, db, 7)
	seedWorkbenchDashboardProject(t, db)
	service, err := NewCrossProjectWorkbenchService(db)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name  string
		query WorkbenchDashboardQuery
		want  error
	}{
		{
			name: "unauthorized injection",
			query: WorkbenchDashboardQuery{
				UserID: 7, ProjectKeys: []models.ProjectKey{"HIDDEN"},
				HasFilter: true, Days: 30,
			},
			want: ErrCrossProjectWorkbenchAccessDenied,
		},
		{
			name: "archived project",
			query: WorkbenchDashboardQuery{
				UserID: 7, ProjectKeys: []models.ProjectKey{"OLD"},
				HasFilter: true, Days: 30,
			},
			want: ErrCrossProjectWorkbenchAccessDenied,
		},
		{
			name: "duplicate query key",
			query: WorkbenchDashboardQuery{
				UserID: 7, ProjectKeys: []models.ProjectKey{"OPS", "OPS"},
				HasFilter: true, Days: 30,
			},
			want: ErrCrossProjectWorkbenchQuery,
		},
		{
			name:  "invalid days",
			query: WorkbenchDashboardQuery{UserID: 7, Days: 365},
			want:  ErrCrossProjectWorkbenchQuery,
		},
		{
			name:  "overflow-sized days",
			query: WorkbenchDashboardQuery{UserID: 7, Days: math.MaxInt},
			want:  ErrCrossProjectWorkbenchQuery,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, queryErr := service.Dashboard(context.Background(), test.query)
			if !errors.Is(queryErr, test.want) {
				t.Fatalf("error = %v, want %v", queryErr, test.want)
			}
		})
	}

	empty, err := service.Dashboard(context.Background(), WorkbenchDashboardQuery{
		// This user can carry platform_admin in authentication, but the service
		// accepts no platform role and has no membership fallback.
		UserID: 99,
		Days:   30,
	})
	if !errors.Is(err, ErrCrossProjectWorkbenchAccessDenied) {
		// Unknown/inactive humans fail before authorization. Seed an active
		// platform-only human to prove the empty membership response below.
		t.Fatalf("unknown human error = %v", err)
	}
	if empty != nil {
		t.Fatalf("unknown human dashboard = %+v", empty)
	}
	if err := db.Exec(
		"INSERT INTO users (id, username, display_name, status) VALUES (99, 'admin', 'Admin', 'active')",
	).Error; err != nil {
		t.Fatal(err)
	}
	empty, err = service.Dashboard(context.Background(), WorkbenchDashboardQuery{
		UserID: 99,
		Days:   30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Summary.Total != 0 ||
		len(empty.SelectedProjects) != 0 ||
		len(empty.DailyTrend) != 0 ||
		len(empty.ProjectBreakdown) != 0 {
		t.Fatalf("platform-only human received data: %+v", empty)
	}
}

func TestWorkbenchDashboardRechecksRevokedMembership(t *testing.T) {
	db := crossProjectWorkbenchTestDB(t)
	seedCrossProjectWorkbench(t, db, 7)
	service, err := NewCrossProjectWorkbenchService(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"UPDATE project_memberships SET is_active = FALSE WHERE user_id = ?",
		7,
	).Error; err != nil {
		t.Fatal(err)
	}
	result, err := service.Dashboard(context.Background(), WorkbenchDashboardQuery{
		UserID: 7,
		Days:   30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.Total != 0 || len(result.SelectedProjects) != 0 {
		t.Fatalf("revoked membership leaked dashboard data: %+v", result)
	}
	if err := db.Exec(
		"UPDATE project_memberships SET is_active = TRUE WHERE user_id = ?",
		7,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"UPDATE projects SET status = 'archived' WHERE key = 'OPS'",
	).Error; err != nil {
		t.Fatal(err)
	}
	_, err = service.Dashboard(context.Background(), WorkbenchDashboardQuery{
		UserID:      7,
		ProjectKeys: []models.ProjectKey{"OPS"},
		HasFilter:   true,
		Days:        30,
	})
	if !errors.Is(err, ErrCrossProjectWorkbenchAccessDenied) {
		t.Fatalf("archived project error = %v", err)
	}
}

func TestWorkbenchDashboardQueryCountDoesNotGrowWithProjectCount(t *testing.T) {
	db := crossProjectWorkbenchTestDB(t)
	seedCrossProjectWorkbench(t, db, 7)
	seedWorkbenchDashboardProject(t, db)
	service, err := NewCrossProjectWorkbenchService(db)
	if err != nil {
		t.Fatal(err)
	}
	var count atomic.Int64
	if err := db.Callback().Query().
		Before("gorm:query").
		Register("test:count-dashboard-queries", func(*gorm.DB) {
			count.Add(1)
		}); err != nil {
		t.Fatal(err)
	}

	if _, err := service.Dashboard(
		context.Background(),
		WorkbenchDashboardQuery{UserID: 7, Days: 30},
	); err != nil {
		t.Fatal(err)
	}
	allProjectsCount := count.Load()
	count.Store(0)
	if _, err := service.Dashboard(
		context.Background(),
		WorkbenchDashboardQuery{
			UserID:      7,
			ProjectKeys: []models.ProjectKey{"OPS"},
			HasFilter:   true,
			Days:        30,
		},
	); err != nil {
		t.Fatal(err)
	}
	singleProjectCount := count.Load()
	if allProjectsCount != singleProjectCount || allProjectsCount > 8 {
		t.Fatalf(
			"query count all=%d single=%d; dashboard must remain fixed and bounded",
			allProjectsCount,
			singleProjectCount,
		)
	}
}

func TestWorkbenchDashboardUsesProjectUserMembershipLockOrder(t *testing.T) {
	db := crossProjectWorkbenchTestDB(t)
	seedCrossProjectWorkbench(t, db, 7)
	service, err := NewCrossProjectWorkbenchService(db)
	if err != nil {
		t.Fatal(err)
	}
	type observedLock struct {
		table    string
		strength string
	}
	locks := make([]observedLock, 0, 3)
	if err := db.Callback().Query().
		After("gorm:query").
		Register("test:dashboard-lock-order", func(query *gorm.DB) {
			lockClause, exists := query.Statement.Clauses["FOR"]
			if !exists {
				return
			}
			locking, ok := lockClause.Expression.(clause.Locking)
			if !ok {
				return
			}
			locks = append(locks, observedLock{
				table:    query.Statement.Table,
				strength: locking.Strength,
			})
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Dashboard(
		context.Background(),
		WorkbenchDashboardQuery{UserID: 7, Days: 30},
	); err != nil {
		t.Fatal(err)
	}
	want := []observedLock{
		{table: "projects", strength: "SHARE"},
		{table: "users", strength: "SHARE"},
		{table: "project_memberships", strength: "SHARE"},
	}
	if len(locks) != len(want) {
		t.Fatalf("dashboard locks = %+v, want %+v", locks, want)
	}
	for index := range want {
		if locks[index] != want[index] {
			t.Fatalf(
				"dashboard lock %d = %+v, want %+v",
				index,
				locks[index],
				want[index],
			)
		}
	}
}

func seedWorkbenchDashboardProject(t *testing.T, db *gorm.DB) {
	t.Helper()
	statements := []string{
		`INSERT INTO projects (id, organization_id, key, name, status)
		 VALUES (40, 1, 'FIN2', 'Finance duplicate', 'active')`,
		`UPDATE projects SET key = 'FIN', name = 'Finance'
		 WHERE id = 20`,
		`INSERT INTO project_memberships
		 (id, project_id, user_id, role, is_active)
		 VALUES (4, 20, 7, 'observer', TRUE)`,
		`INSERT INTO tickets (
			id, public_id, organization_id, project_id, ticket_number, title,
			type, priority, status, created_by_actor_type, created_by_actor_id,
			assigned_to_actor_type, assigned_to_actor_id, due_date,
			sla_breached, version, created_at, updated_at
		) VALUES
		(401, '401', 1, 20, 'FIN-1', 'Finance overdue', 'incident', 'normal',
		 'open', 'human', '7', 'service_principal', 'sp-1',
		 '2026-07-30T00:00:00Z', TRUE, 1,
		 '2026-07-29T00:00:00Z', '2026-07-29T00:00:00Z'),
		(402, '402', 1, 20, 'FIN-2', 'Finance unassigned', 'request', 'normal',
		 'open', 'human', '7', NULL, NULL, NULL, FALSE, 1,
		 '2026-07-31T00:00:00Z', '2026-07-31T00:00:00Z')`,
		`INSERT INTO tickets (
			id, public_id, organization_id, project_id, ticket_number, title,
			type, priority, status, created_by_actor_type, created_by_actor_id,
			assigned_to_actor_type, assigned_to_actor_id, sla_breached, version,
			created_at, updated_at
		) VALUES
		(501, '501', 1, 40, 'FIN2-1', 'Not a member', 'request', 'normal',
		 'open', 'human', '7', NULL, NULL, FALSE, 1,
		 '2026-07-31T00:00:00Z', '2026-07-31T00:00:00Z')`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("seed dashboard: %v", err)
		}
	}
}
