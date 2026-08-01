package services

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultWorkbenchDashboardDays         = 30
	maxWorkbenchDashboardResponseProjects = 100
)

type WorkbenchDashboardQuery struct {
	UserID      uint
	ProjectKeys []models.ProjectKey
	HasFilter   bool
	Days        int
}

type WorkbenchDashboardProject struct {
	Key  models.ProjectKey `json:"key"`
	Name string            `json:"name"`
}

type WorkbenchDashboardStatusCounts struct {
	Open       int64 `json:"open"`
	InProgress int64 `json:"in_progress"`
	Pending    int64 `json:"pending"`
	Resolved   int64 `json:"resolved"`
	Closed     int64 `json:"closed"`
	Cancelled  int64 `json:"cancelled"`
}

type WorkbenchDashboardPriorityCounts struct {
	Low      int64 `json:"low"`
	Normal   int64 `json:"normal"`
	High     int64 `json:"high"`
	Urgent   int64 `json:"urgent"`
	Critical int64 `json:"critical"`
}

type WorkbenchDashboardAssignmentCounts struct {
	Assigned         int64 `json:"assigned"`
	Unassigned       int64 `json:"unassigned"`
	Human            int64 `json:"human"`
	ServicePrincipal int64 `json:"service_principal"`
}

type WorkbenchDashboardSummary struct {
	Total       int64                              `json:"total"`
	Status      WorkbenchDashboardStatusCounts     `json:"status"`
	Priority    WorkbenchDashboardPriorityCounts   `json:"priority"`
	SLABreached int64                              `json:"sla_breached"`
	Overdue     int64                              `json:"overdue"`
	Assignment  WorkbenchDashboardAssignmentCounts `json:"assignment"`
}

type WorkbenchDashboardDailyPoint struct {
	Date    string `json:"date"`
	Created int64  `json:"created"`
}

type WorkbenchDashboardProjectBreakdown struct {
	ProjectKey  models.ProjectKey `json:"project_key"`
	ProjectName string            `json:"project_name"`
	Total       int64             `json:"total"`
	SLABreached int64             `json:"sla_breached"`
	Overdue     int64             `json:"overdue"`
}

type dashboardProjectAggregateRow struct {
	ProjectID   uint
	Total       int64
	SLABreached int64
	Overdue     int64
}

type authorizedDashboardProject struct {
	ID             uint                 `gorm:"column:id"`
	OrganizationID uint                 `gorm:"column:organization_id"`
	Key            models.ProjectKey    `gorm:"column:key"`
	Name           string               `gorm:"column:name"`
	Status         models.ProjectStatus `gorm:"column:status"`
}

type WorkbenchDashboard struct {
	GeneratedAt               time.Time                            `json:"generated_at"`
	Days                      int                                  `json:"days"`
	SelectedProjectCount      int                                  `json:"selected_project_count"`
	SelectedProjects          []WorkbenchDashboardProject          `json:"selected_projects"`
	SelectedProjectsTruncated bool                                 `json:"selected_projects_truncated"`
	Summary                   WorkbenchDashboardSummary            `json:"summary"`
	DailyTrend                []WorkbenchDashboardDailyPoint       `json:"daily_trend"`
	ProjectBreakdown          []WorkbenchDashboardProjectBreakdown `json:"project_breakdown"`
	ProjectBreakdownTruncated bool                                 `json:"project_breakdown_truncated"`
}

type dashboardAggregateRow struct {
	Total                    int64
	StatusOpen               int64
	StatusInProgress         int64
	StatusPending            int64
	StatusResolved           int64
	StatusClosed             int64
	StatusCancelled          int64
	PriorityLow              int64
	PriorityNormal           int64
	PriorityHigh             int64
	PriorityUrgent           int64
	PriorityCritical         int64
	SLABreached              int64
	Overdue                  int64
	Assigned                 int64
	Unassigned               int64
	AssignedHuman            int64
	AssignedServicePrincipal int64
}

type dashboardDailyRow struct {
	Date    string
	Created int64
}

func (service *CrossProjectWorkbenchService) Dashboard(
	ctx context.Context,
	input WorkbenchDashboardQuery,
) (*WorkbenchDashboard, error) {
	if ctx == nil || input.UserID == 0 {
		return nil, ErrCrossProjectWorkbenchAccessDenied
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("workbench dashboard context: %w", err)
	}
	if input.Days == 0 {
		input.Days = defaultWorkbenchDashboardDays
	}
	if input.Days != 7 && input.Days != 30 && input.Days != 90 {
		return nil, ErrCrossProjectWorkbenchQuery
	}
	if len(input.ProjectKeys) > maxCrossProjectWorkbenchProjects {
		return nil, ErrCrossProjectWorkbenchProjectLimit
	}
	if !input.HasFilter && len(input.ProjectKeys) != 0 {
		return nil, ErrCrossProjectWorkbenchQuery
	}
	if input.HasFilter && len(input.ProjectKeys) == 0 {
		return nil, ErrCrossProjectWorkbenchQuery
	}
	seenKeys := make(map[models.ProjectKey]struct{}, len(input.ProjectKeys))
	for _, key := range input.ProjectKeys {
		if err := key.Validate(); err != nil {
			return nil, ErrCrossProjectWorkbenchQuery
		}
		if _, exists := seenKeys[key]; exists {
			return nil, ErrCrossProjectWorkbenchQuery
		}
		seenKeys[key] = struct{}{}
	}

	generatedAt := service.dashboardNow().UTC()
	result := emptyWorkbenchDashboard(generatedAt, input.Days)
	err := service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Discovery is deliberately lock-free. The authoritative snapshot is
		// rebuilt below after acquiring Project -> User -> Membership locks,
		// matching project archive and membership administration commands.
		candidateQuery := tx.Table("projects AS projects").
			Select("projects.id, projects.organization_id, projects.key, projects.name").
			Joins("JOIN project_memberships AS memberships ON memberships.project_id = projects.id").
			Where("memberships.user_id = ?", input.UserID).
			Where("memberships.is_active = ?", true).
			Where("projects.status = ?", models.ProjectStatusActive).
			Order("projects.id ASC").
			Limit(maxCrossProjectWorkbenchProjects + 1)
		candidates := make([]authorizedDashboardProject, 0)
		if err := candidateQuery.Scan(&candidates).Error; err != nil {
			return fmt.Errorf("discover workbench dashboard memberships: %w", err)
		}
		if len(candidates) > maxCrossProjectWorkbenchProjects {
			return ErrCrossProjectWorkbenchProjectLimit
		}

		candidateProjectIDs := make([]uint, 0, len(candidates))
		for _, candidate := range candidates {
			if candidate.ID == 0 {
				return ErrCrossProjectWorkbenchAccessDenied
			}
			candidateProjectIDs = append(candidateProjectIDs, candidate.ID)
		}
		sort.Slice(candidateProjectIDs, func(left, right int) bool {
			return candidateProjectIDs[left] < candidateProjectIDs[right]
		})

		lockedProjects := make([]authorizedDashboardProject, 0, len(candidates))
		if len(candidateProjectIDs) > 0 {
			if err := tx.Table("projects").
				Select("id, organization_id, key, name, status").
				Clauses(clause.Locking{Strength: "SHARE"}).
				Where("id IN ?", candidateProjectIDs).
				Order("id ASC").
				Find(&lockedProjects).Error; err != nil {
				return fmt.Errorf("lock workbench dashboard projects: %w", err)
			}
			if len(lockedProjects) != len(candidateProjectIDs) {
				return ErrCrossProjectWorkbenchAccessDenied
			}
		}

		var lockedUser models.User
		if err := tx.
			Unscoped().
			Select("id", "status", "deleted_at").
			Clauses(clause.Locking{Strength: "SHARE"}).
			Where("id = ?", input.UserID).
			Take(&lockedUser).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrCrossProjectWorkbenchAccessDenied
			}
			return fmt.Errorf("lock workbench dashboard human: %w", err)
		}
		if lockedUser.DeletedAt.Valid ||
			lockedUser.Status != models.UserStatusActive {
			return ErrCrossProjectWorkbenchAccessDenied
		}

		lockedMemberships := make([]models.ProjectMembership, 0, len(candidates))
		if len(candidateProjectIDs) > 0 {
			if err := tx.
				Select("id", "project_id", "user_id", "is_active").
				Clauses(clause.Locking{Strength: "SHARE"}).
				Where(
					"user_id = ? AND project_id IN ?",
					input.UserID,
					candidateProjectIDs,
				).
				Order("id ASC").
				Find(&lockedMemberships).Error; err != nil {
				return fmt.Errorf(
					"lock workbench dashboard memberships: %w",
					err,
				)
			}
			if len(lockedMemberships) != len(candidateProjectIDs) {
				return ErrCrossProjectWorkbenchAccessDenied
			}
		}

		activeMembershipProjectIDs := make(
			map[uint]struct{},
			len(lockedMemberships),
		)
		for _, membership := range lockedMemberships {
			if !membership.IsActive ||
				membership.UserID != input.UserID {
				return ErrCrossProjectWorkbenchAccessDenied
			}
			activeMembershipProjectIDs[membership.ProjectID] = struct{}{}
		}
		if len(activeMembershipProjectIDs) != len(candidateProjectIDs) {
			return ErrCrossProjectWorkbenchAccessDenied
		}

		if len(lockedProjects) == 0 {
			if input.HasFilter {
				return ErrCrossProjectWorkbenchAccessDenied
			}
			return nil
		}
		organizationID := lockedProjects[0].OrganizationID
		authorizedByKey := make(
			map[models.ProjectKey]authorizedDashboardProject,
			len(lockedProjects),
		)
		for _, project := range lockedProjects {
			if project.ID == 0 ||
				project.OrganizationID == 0 ||
				project.OrganizationID != organizationID ||
				project.Status != models.ProjectStatusActive {
				return ErrCrossProjectWorkbenchAccessDenied
			}
			if _, active := activeMembershipProjectIDs[project.ID]; !active {
				return ErrCrossProjectWorkbenchAccessDenied
			}
			authorizedByKey[project.Key] = project
		}
		projects := lockedProjects
		if input.HasFilter {
			projects = make([]authorizedDashboardProject, 0, len(input.ProjectKeys))
			for _, key := range input.ProjectKeys {
				project, authorized := authorizedByKey[key]
				if !authorized {
					return ErrCrossProjectWorkbenchAccessDenied
				}
				projects = append(projects, project)
			}
		}
		projectIDs := make([]uint, 0, len(projects))
		result.SelectedProjectCount = len(projects)
		result.SelectedProjectsTruncated =
			len(projects) > maxWorkbenchDashboardResponseProjects
		for index, project := range projects {
			projectIDs = append(projectIDs, project.ID)
			if index < maxWorkbenchDashboardResponseProjects {
				result.SelectedProjects = append(
					result.SelectedProjects,
					WorkbenchDashboardProject{
						Key: project.Key, Name: project.Name,
					},
				)
			}
		}
		if err := scopeddb.ConfigureAuthorizedProjectScopeTransaction(
			tx,
			organizationID,
			projectIDs,
		); err != nil {
			return fmt.Errorf("configure workbench dashboard scope: %w", err)
		}

		windowStart := dashboardDay(generatedAt).
			AddDate(0, 0, -(input.Days - 1))
		windowPredicate :=
			"tickets.created_at >= ? AND tickets.created_at <= ?"
		if tx.Dialector.Name() == "sqlite" {
			// SQLite fixtures can contain both RFC3339 and driver-formatted
			// DATETIME values. Normalize them before comparison so tests enforce
			// the same inclusive UTC window used by PostgreSQL.
			windowPredicate =
				"datetime(tickets.created_at) >= datetime(?) AND " +
					"datetime(tickets.created_at) <= datetime(?)"
		}
		base := func() *gorm.DB {
			return tx.Table("tickets AS tickets").
				Where("tickets.organization_id = ?", organizationID).
				Where("tickets.project_id IN ?", projectIDs).
				Where("tickets.deleted_at IS NULL").
				Where(windowPredicate, windowStart, generatedAt)
		}
		var aggregate dashboardAggregateRow
		if err := base().Select(`
			COUNT(*) AS total,
			COALESCE(SUM(CASE WHEN tickets.status = 'open' THEN 1 ELSE 0 END), 0) AS status_open,
			COALESCE(SUM(CASE WHEN tickets.status = 'in_progress' THEN 1 ELSE 0 END), 0) AS status_in_progress,
			COALESCE(SUM(CASE WHEN tickets.status = 'pending' THEN 1 ELSE 0 END), 0) AS status_pending,
			COALESCE(SUM(CASE WHEN tickets.status = 'resolved' THEN 1 ELSE 0 END), 0) AS status_resolved,
			COALESCE(SUM(CASE WHEN tickets.status = 'closed' THEN 1 ELSE 0 END), 0) AS status_closed,
			COALESCE(SUM(CASE WHEN tickets.status = 'cancelled' THEN 1 ELSE 0 END), 0) AS status_cancelled,
			COALESCE(SUM(CASE WHEN tickets.priority = 'low' THEN 1 ELSE 0 END), 0) AS priority_low,
			COALESCE(SUM(CASE WHEN tickets.priority = 'normal' THEN 1 ELSE 0 END), 0) AS priority_normal,
			COALESCE(SUM(CASE WHEN tickets.priority = 'high' THEN 1 ELSE 0 END), 0) AS priority_high,
			COALESCE(SUM(CASE WHEN tickets.priority = 'urgent' THEN 1 ELSE 0 END), 0) AS priority_urgent,
			COALESCE(SUM(CASE WHEN tickets.priority = 'critical' THEN 1 ELSE 0 END), 0) AS priority_critical,
			COALESCE(SUM(CASE WHEN tickets.sla_breached = TRUE THEN 1 ELSE 0 END), 0) AS sla_breached,
			COALESCE(SUM(CASE WHEN tickets.status NOT IN ('resolved', 'closed', 'cancelled')
				AND tickets.due_date < ? THEN 1 ELSE 0 END), 0) AS overdue,
			COALESCE(SUM(CASE WHEN tickets.assigned_to_actor_type IN ('human', 'service_principal') THEN 1 ELSE 0 END), 0) AS assigned,
			COALESCE(SUM(CASE WHEN tickets.assigned_to_actor_type IS NULL OR tickets.assigned_to_actor_type = '' THEN 1 ELSE 0 END), 0) AS unassigned,
			COALESCE(SUM(CASE WHEN tickets.assigned_to_actor_type = 'human' THEN 1 ELSE 0 END), 0) AS assigned_human,
			COALESCE(SUM(CASE WHEN tickets.assigned_to_actor_type = 'service_principal' THEN 1 ELSE 0 END), 0) AS assigned_service_principal
		`, generatedAt).Scan(&aggregate).Error; err != nil {
			return fmt.Errorf("aggregate workbench dashboard summary: %w", err)
		}
		result.Summary = workbenchDashboardSummary(aggregate)

		dailyRows := make([]dashboardDailyRow, 0)
		dateExpression := "DATE(tickets.created_at)"
		if tx.Dialector.Name() == "postgres" {
			dateExpression = "TO_CHAR(tickets.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD')"
		}
		if err := base().
			Select(dateExpression + " AS date, COUNT(*) AS created").
			Group(dateExpression).
			Order(dateExpression + " ASC").
			Scan(&dailyRows).Error; err != nil {
			return fmt.Errorf("aggregate workbench dashboard trend: %w", err)
		}
		dailyByDate := make(map[string]int64, len(dailyRows))
		for _, row := range dailyRows {
			dailyByDate[row.Date] = row.Created
		}
		for day := 0; day < input.Days; day++ {
			date := windowStart.AddDate(0, 0, day).Format("2006-01-02")
			result.DailyTrend = append(result.DailyTrend, WorkbenchDashboardDailyPoint{
				Date: date, Created: dailyByDate[date],
			})
		}

		projectRows := make([]dashboardProjectAggregateRow, 0, len(projects))
		if err := base().
			Select(`
				tickets.project_id AS project_id,
				COUNT(*) AS total,
				COALESCE(SUM(CASE WHEN tickets.sla_breached = TRUE THEN 1 ELSE 0 END), 0) AS sla_breached,
				COALESCE(SUM(CASE WHEN tickets.status NOT IN ('resolved', 'closed', 'cancelled')
					AND tickets.due_date < ? THEN 1 ELSE 0 END), 0) AS overdue
			`, generatedAt).
			Group("tickets.project_id").
			Scan(&projectRows).Error; err != nil {
			return fmt.Errorf("aggregate workbench dashboard projects: %w", err)
		}
		aggregateByProject := make(
			map[uint]dashboardProjectAggregateRow,
			len(projectRows),
		)
		for _, row := range projectRows {
			aggregateByProject[row.ProjectID] = row
		}
		for _, project := range projects {
			row := aggregateByProject[project.ID]
			result.ProjectBreakdown = append(
				result.ProjectBreakdown,
				WorkbenchDashboardProjectBreakdown{
					ProjectKey:  project.Key,
					ProjectName: project.Name,
					Total:       row.Total,
					SLABreached: row.SLABreached,
					Overdue:     row.Overdue,
				},
			)
		}
		sort.Slice(result.ProjectBreakdown, func(left, right int) bool {
			if result.ProjectBreakdown[left].Total !=
				result.ProjectBreakdown[right].Total {
				return result.ProjectBreakdown[left].Total >
					result.ProjectBreakdown[right].Total
			}
			return result.ProjectBreakdown[left].ProjectKey <
				result.ProjectBreakdown[right].ProjectKey
		})
		if len(result.ProjectBreakdown) >
			maxWorkbenchDashboardResponseProjects {
			result.ProjectBreakdown =
				result.ProjectBreakdown[:maxWorkbenchDashboardResponseProjects]
			result.ProjectBreakdownTruncated = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (service *CrossProjectWorkbenchService) dashboardNow() time.Time {
	if service.now != nil {
		return service.now()
	}
	return time.Now()
}

func emptyWorkbenchDashboard(generatedAt time.Time, days int) *WorkbenchDashboard {
	return &WorkbenchDashboard{
		GeneratedAt:      generatedAt,
		Days:             days,
		SelectedProjects: []WorkbenchDashboardProject{},
		Summary:          WorkbenchDashboardSummary{},
		DailyTrend:       []WorkbenchDashboardDailyPoint{},
		ProjectBreakdown: []WorkbenchDashboardProjectBreakdown{},
	}
}

func dashboardDay(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func workbenchDashboardSummary(row dashboardAggregateRow) WorkbenchDashboardSummary {
	return WorkbenchDashboardSummary{
		Total: row.Total,
		Status: WorkbenchDashboardStatusCounts{
			Open: row.StatusOpen, InProgress: row.StatusInProgress,
			Pending: row.StatusPending, Resolved: row.StatusResolved,
			Closed: row.StatusClosed, Cancelled: row.StatusCancelled,
		},
		Priority: WorkbenchDashboardPriorityCounts{
			Low: row.PriorityLow, Normal: row.PriorityNormal,
			High: row.PriorityHigh, Urgent: row.PriorityUrgent,
			Critical: row.PriorityCritical,
		},
		SLABreached: row.SLABreached,
		Overdue:     row.Overdue,
		Assignment: WorkbenchDashboardAssignmentCounts{
			Assigned: row.Assigned, Unassigned: row.Unassigned,
			Human:            row.AssignedHuman,
			ServicePrincipal: row.AssignedServicePrincipal,
		},
	}
}
