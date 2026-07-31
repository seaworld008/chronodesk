package services

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
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
	if allProjectsCount != singleProjectCount || allProjectsCount > 6 {
		t.Fatalf(
			"query count all=%d single=%d; dashboard must remain fixed and bounded",
			allProjectsCount,
			singleProjectCount,
		)
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
