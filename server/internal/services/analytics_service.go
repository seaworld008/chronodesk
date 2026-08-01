package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrAnalyticsAuthorizedProjectSetRequired = errors.New(
		"analytics authorized project set is required",
	)
	ErrAnalyticsInvalidTimeRange = errors.New(
		"analytics time range is invalid",
	)
	ErrAnalyticsResultTooLarge = errors.New(
		"analytics result exceeds bounded series limit",
	)
	ErrAnalyticsExportTooLarge = errors.New(
		"analytics export exceeds bounded response limit",
	)
	ErrAnalyticsProjectLimit = errors.New(
		"analytics authorized project set exceeds bounded limit",
	)
)

const (
	AnalyticsMaxTimeRangeDays  = 90
	AnalyticsMaxCategoryValues = 1_000
	AnalyticsMaxExportBytes    = 1 << 20
	AnalyticsMaxProjects       = 500
)

// AnalyticsAuthorizedProjectSet is trusted control data resolved by a
// server-side membership/grant resolver. Transport payloads must never build
// this value directly. Adapters must resolve a fresh set for every request so a
// revoked membership disappears from the next query.
type AnalyticsAuthorizedProjectSet struct {
	OrganizationID uint
	ProjectIDs     []uint
	humanUserID    uint
}

func (authorized AnalyticsAuthorizedProjectSet) validate() error {
	if authorized.OrganizationID == 0 || authorized.humanUserID == 0 {
		return ErrAnalyticsAuthorizedProjectSetRequired
	}
	if len(authorized.ProjectIDs) > AnalyticsMaxProjects {
		return ErrAnalyticsProjectLimit
	}
	seen := make(map[uint]struct{}, len(authorized.ProjectIDs))
	for _, projectID := range authorized.ProjectIDs {
		if projectID == 0 {
			return ErrAnalyticsAuthorizedProjectSetRequired
		}
		if _, duplicate := seen[projectID]; duplicate {
			return ErrAnalyticsAuthorizedProjectSetRequired
		}
		seen[projectID] = struct{}{}
	}
	return nil
}

func (authorized AnalyticsAuthorizedProjectSet) snapshot() AnalyticsAuthorizedProjectSet {
	authorized.ProjectIDs = append([]uint(nil), authorized.ProjectIDs...)
	return authorized
}

// NewHumanAnalyticsAuthorizedProjectSet binds a server-resolved project set to
// the authenticated human whose live memberships will be checked again inside
// the analytics transaction. The subject remains private so transport-decoded
// data cannot construct a trusted authorization fact.
func NewHumanAnalyticsAuthorizedProjectSet(
	humanUserID uint,
	organizationID uint,
	projectIDs []uint,
) (AnalyticsAuthorizedProjectSet, error) {
	authorized := AnalyticsAuthorizedProjectSet{
		OrganizationID: organizationID,
		ProjectIDs:     append([]uint(nil), projectIDs...),
		humanUserID:    humanUserID,
	}
	if err := authorized.validate(); err != nil {
		return AnalyticsAuthorizedProjectSet{}, err
	}
	return authorized, nil
}

// AnalyticsService keeps platform observability separate from project-owned
// business analytics. Project business methods always enter an authorized
// project-set transaction before reading any business table.
type AnalyticsService struct {
	db *gorm.DB
}

func NewAnalyticsService(db *gorm.DB) *AnalyticsService {
	return &AnalyticsService{db: db}
}

// SystemStats contains runtime metrics that can be read truthfully from the Go
// runtime. Uptime is deliberately absent: this service has no authoritative
// lifecycle start source, so it must not fabricate one from the current time.
type SystemStats struct {
	CPUCount   int         `json:"cpu_count"`
	GoVersion  string      `json:"go_version"`
	ServerTime time.Time   `json:"server_time"`
	GoRoutines int         `json:"goroutines"`
	CGOCalls   int64       `json:"cgo_calls"`
	MemStats   MemoryStats `json:"memory_stats"`
	GCStats    GCStats     `json:"gc_stats"`
}

type MemoryStats struct {
	HeapAlloc    uint64 `json:"heap_alloc"`
	HeapSys      uint64 `json:"heap_sys"`
	HeapIdle     uint64 `json:"heap_idle"`
	HeapInuse    uint64 `json:"heap_inuse"`
	HeapReleased uint64 `json:"heap_released"`
	HeapObjects  uint64 `json:"heap_objects"`
	Sys          uint64 `json:"sys"`
	Alloc        uint64 `json:"alloc"`
	TotalAlloc   uint64 `json:"total_alloc"`
	StackInuse   uint64 `json:"stack_inuse"`
	StackSys     uint64 `json:"stack_sys"`
	Mallocs      uint64 `json:"mallocs"`
	Frees        uint64 `json:"frees"`
}

type GCStats struct {
	NumGC         uint32        `json:"num_gc"`
	NumForcedGC   uint32        `json:"num_forced_gc"`
	GCCPUFraction float64       `json:"gc_cpu_fraction"`
	LastGC        *time.Time    `json:"last_gc,omitempty"`
	NextGC        uint64        `json:"next_gc"`
	PauseTotal    time.Duration `json:"pause_total"`
	PauseNs       []uint64      `json:"pause_ns"`
}

// PlatformStats contains only process/runtime and platform-owned identity and
// maintenance metrics. It never reads Ticket, TicketComment, Category, or
// ProjectMembership.
type PlatformStats struct {
	Runtime *SystemStats         `json:"runtime"`
	Users   PlatformUserStats    `json:"users"`
	Cleanup PlatformCleanupStats `json:"cleanup"`
}

type PlatformUserStats struct {
	Total              int64 `json:"total"`
	Active             int64 `json:"active"`
	PlatformAdmins     int64 `json:"platform_admins"`
	SecurityAuditors   int64 `json:"security_auditors"`
	EmergencyOperators int64 `json:"emergency_operators"`
	Members            int64 `json:"members"`
	TodayLogins        int64 `json:"today_logins"`
	WeekLogins         int64 `json:"week_logins"`
	MonthLogins        int64 `json:"month_logins"`
}

type PlatformCleanupStats struct {
	CleanupJobs int64      `json:"cleanup_jobs"`
	LastCleanup *time.Time `json:"last_cleanup,omitempty"`
}

// BusinessStats contains only data derived from the explicitly authorized
// project set.
type BusinessStats struct {
	TicketStats     AnalyticsTicketStats   `json:"ticket_stats"`
	MembershipStats ProjectMembershipStats `json:"membership_stats"`
	ActivityStats   ActivityStats          `json:"activity_stats"`
}

type AnalyticsTicketStats struct {
	Total      int64 `json:"total"`
	Open       int64 `json:"open"`
	InProgress int64 `json:"in_progress"`
	Resolved   int64 `json:"resolved"`
	Closed     int64 `json:"closed"`

	HighPriority   int64 `json:"high_priority"`
	MediumPriority int64 `json:"medium_priority"`
	LowPriority    int64 `json:"low_priority"`

	ByCategory map[string]int64 `json:"by_category"`

	Today     int64 `json:"today"`
	ThisWeek  int64 `json:"this_week"`
	ThisMonth int64 `json:"this_month"`

	// Values are hours derived only from the durable response_time and
	// resolution_time minute fields. Nil means the metric is unavailable
	// because the authorized set has no real sample.
	AvgResponseTime   *float64 `json:"avg_response_time_hours,omitempty"`
	AvgResolutionTime *float64 `json:"avg_resolution_time_hours,omitempty"`
}

type ProjectMembershipStats struct {
	Total         int64 `json:"total"`
	ActiveUsers   int64 `json:"active_users"`
	ProjectAdmins int64 `json:"project_admins"`
	Managers      int64 `json:"managers"`
	Agents        int64 `json:"agents"`
	Requesters    int64 `json:"requesters"`
	Observers     int64 `json:"observers"`
}

type ActivityStats struct {
	TotalComments   int64 `json:"total_comments"`
	TodayComments   int64 `json:"today_comments"`
	WeekComments    int64 `json:"week_comments"`
	TotalCategories int64 `json:"total_categories"`
}

type TimeRangeStats struct {
	StartDate         time.Time    `json:"start_date"`
	EndDate           time.Time    `json:"end_date"`
	TicketTrend       []DailyCount `json:"ticket_trend"`
	UserActivityTrend []DailyCount `json:"user_activity_trend"`
	CommentTrend      []DailyCount `json:"comment_trend"`
}

type DailyCount struct {
	Date  time.Time `json:"date"`
	Count int64     `json:"count"`
}

func (s *AnalyticsService) GetSystemStats() (*SystemStats, error) {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)

	var lastGC *time.Time
	if memory.LastGC != 0 {
		value := time.Unix(0, int64(memory.LastGC))
		lastGC = &value
	}
	pauseCount := int(memory.NumGC)
	if pauseCount > len(memory.PauseNs) {
		pauseCount = len(memory.PauseNs)
	}
	pauses := make([]uint64, pauseCount)
	copy(pauses, memory.PauseNs[:pauseCount])

	return &SystemStats{
		CPUCount:   runtime.NumCPU(),
		GoVersion:  runtime.Version(),
		ServerTime: time.Now(),
		GoRoutines: runtime.NumGoroutine(),
		CGOCalls:   runtime.NumCgoCall(),
		MemStats: MemoryStats{
			HeapAlloc:    memory.HeapAlloc,
			HeapSys:      memory.HeapSys,
			HeapIdle:     memory.HeapIdle,
			HeapInuse:    memory.HeapInuse,
			HeapReleased: memory.HeapReleased,
			HeapObjects:  memory.HeapObjects,
			Sys:          memory.Sys,
			Alloc:        memory.Alloc,
			TotalAlloc:   memory.TotalAlloc,
			StackInuse:   memory.StackInuse,
			StackSys:     memory.StackSys,
			Mallocs:      memory.Mallocs,
			Frees:        memory.Frees,
		},
		GCStats: GCStats{
			NumGC:         memory.NumGC,
			NumForcedGC:   memory.NumForcedGC,
			GCCPUFraction: memory.GCCPUFraction,
			LastGC:        lastGC,
			NextGC:        memory.NextGC,
			PauseTotal:    time.Duration(memory.PauseTotalNs),
			PauseNs:       pauses,
		},
	}, nil
}

func (s *AnalyticsService) GetPlatformStats(
	ctx context.Context,
) (*PlatformStats, error) {
	runtimeStats, err := s.GetSystemStats()
	if err != nil {
		return nil, fmt.Errorf("get platform runtime stats: %w", err)
	}
	userStats, err := s.getPlatformUserStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("get platform user stats: %w", err)
	}
	cleanupStats, err := s.getPlatformCleanupStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("get platform cleanup stats: %w", err)
	}
	return &PlatformStats{
		Runtime: runtimeStats,
		Users:   *userStats,
		Cleanup: *cleanupStats,
	}, nil
}

func (s *AnalyticsService) GetBusinessStats(
	ctx context.Context,
	authorized AnalyticsAuthorizedProjectSet,
) (*BusinessStats, error) {
	if err := authorized.validate(); err != nil {
		return nil, err
	}
	authorized = authorized.snapshot()
	stats := emptyBusinessStats()
	err := s.withAuthorizedHumanProjectSetTransaction(
		ctx,
		authorized,
		func(scopedCtx context.Context) error {
			ticketStats, queryErr := s.getTicketStats(scopedCtx, authorized)
			if queryErr != nil {
				return fmt.Errorf("get ticket stats: %w", queryErr)
			}
			membershipStats, queryErr := s.getProjectMembershipStats(
				scopedCtx,
				authorized,
			)
			if queryErr != nil {
				return fmt.Errorf("get project membership stats: %w", queryErr)
			}
			activityStats, queryErr := s.getActivityStats(
				scopedCtx,
				authorized,
			)
			if queryErr != nil {
				return fmt.Errorf("get activity stats: %w", queryErr)
			}
			stats.TicketStats = *ticketStats
			stats.MembershipStats = *membershipStats
			stats.ActivityStats = *activityStats
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("query authorized business stats: %w", err)
	}
	return stats, nil
}

func (s *AnalyticsService) withAuthorizedHumanProjectSetTransaction(
	ctx context.Context,
	authorized AnalyticsAuthorizedProjectSet,
	fn func(context.Context) error,
) error {
	return scopeddb.WithAuthorizedProjectScopeTransaction(
		ctx,
		s.db,
		authorized.OrganizationID,
		authorized.ProjectIDs,
		func(scopedCtx context.Context) error {
			if err := s.revalidateHumanAnalyticsProjectSet(
				scopedCtx,
				authorized,
			); err != nil {
				return err
			}
			if len(authorized.ProjectIDs) == 0 {
				return nil
			}
			return fn(scopedCtx)
		},
	)
}

// revalidateHumanAnalyticsProjectSet is deliberately the first callback work
// after SET LOCAL installs the authorized project array. Its lock order matches
// the single-project authorization path: Project rows, Human row, Membership
// rows. No Ticket, Comment, category, or trend query runs before it succeeds.
func (s *AnalyticsService) revalidateHumanAnalyticsProjectSet(
	ctx context.Context,
	authorized AnalyticsAuthorizedProjectSet,
) error {
	db := s.db.WithContext(ctx)

	var projects []models.Project
	if err := db.Clauses(clause.Locking{Strength: "SHARE"}).
		Where(
			"organization_id = ? AND id IN ? AND status = ?",
			authorized.OrganizationID,
			authorized.ProjectIDs,
			models.ProjectStatusActive,
		).
		Order("id ASC").
		Find(&projects).Error; err != nil {
		return fmt.Errorf("lock analytics projects: %w", err)
	}
	if len(projects) != len(authorized.ProjectIDs) {
		return ErrProjectAccessDenied
	}

	var user models.User
	err := db.Clauses(clause.Locking{Strength: "SHARE"}).
		Select("id", "status").
		Where(
			"id = ? AND status = ?",
			authorized.humanUserID,
			models.UserStatusActive,
		).
		Take(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrProjectAccessDenied
	}
	if err != nil {
		return fmt.Errorf("lock analytics human identity: %w", err)
	}

	if len(authorized.ProjectIDs) == 0 {
		return nil
	}
	var memberships []models.ProjectMembership
	if err := db.Clauses(clause.Locking{Strength: "SHARE"}).
		Select("id", "project_id", "user_id", "role", "is_active").
		Where(
			"project_id IN ? AND user_id = ? AND is_active = ?",
			authorized.ProjectIDs,
			authorized.humanUserID,
			true,
		).
		Order("project_id ASC").
		Find(&memberships).Error; err != nil {
		return fmt.Errorf("lock analytics project memberships: %w", err)
	}
	if len(memberships) != len(authorized.ProjectIDs) {
		return ErrProjectAccessDenied
	}
	expected := make(map[uint]struct{}, len(authorized.ProjectIDs))
	for _, projectID := range authorized.ProjectIDs {
		expected[projectID] = struct{}{}
	}
	for _, membership := range memberships {
		if membership.UserID != authorized.humanUserID ||
			!membership.IsActive ||
			!membership.Role.IsValid() {
			return ErrProjectAccessDenied
		}
		if _, exists := expected[membership.ProjectID]; !exists {
			return ErrProjectAccessDenied
		}
		delete(expected, membership.ProjectID)
	}
	if len(expected) != 0 {
		return ErrProjectAccessDenied
	}
	return nil
}

func emptyBusinessStats() *BusinessStats {
	return &BusinessStats{
		TicketStats: AnalyticsTicketStats{
			ByCategory: make(map[string]int64),
		},
	}
}

func (s *AnalyticsService) getTicketStats(
	ctx context.Context,
	authorized AnalyticsAuthorizedProjectSet,
) (*AnalyticsTicketStats, error) {
	stats := AnalyticsTicketStats{
		ByCategory: make(map[string]int64),
	}
	tickets := s.authorizedTickets(ctx, authorized)
	if err := tickets.Count(&stats.Total).Error; err != nil {
		return nil, err
	}

	var statusCounts []struct {
		Status models.TicketStatus
		Count  int64
	}
	if err := s.authorizedTickets(ctx, authorized).
		Select("status, count(*) AS count").
		Group("status").
		Scan(&statusCounts).Error; err != nil {
		return nil, err
	}
	for _, value := range statusCounts {
		switch value.Status {
		case models.TicketStatusOpen:
			stats.Open = value.Count
		case models.TicketStatusInProgress:
			stats.InProgress = value.Count
		case models.TicketStatusResolved:
			stats.Resolved = value.Count
		case models.TicketStatusClosed:
			stats.Closed = value.Count
		}
	}

	var priorityCounts []struct {
		Priority models.TicketPriority
		Count    int64
	}
	if err := s.authorizedTickets(ctx, authorized).
		Select("priority, count(*) AS count").
		Group("priority").
		Scan(&priorityCounts).Error; err != nil {
		return nil, err
	}
	for _, value := range priorityCounts {
		switch value.Priority {
		case models.TicketPriorityHigh:
			stats.HighPriority = value.Count
		case models.TicketPriorityNormal:
			stats.MediumPriority = value.Count
		case models.TicketPriorityLow:
			stats.LowPriority = value.Count
		}
	}

	var categoryCounts []struct {
		CategoryName string `gorm:"column:category_name"`
		Count        int64
	}
	if err := s.db.WithContext(ctx).
		Table("tickets AS tickets").
		Select("categories.name AS category_name, count(*) AS count").
		Joins(`
			JOIN categories
			  ON categories.id = tickets.category_id
			 AND categories.organization_id = tickets.organization_id
			 AND categories.project_id = tickets.project_id
		`).
		Where("tickets.deleted_at IS NULL").
		Where("tickets.organization_id = ?", authorized.OrganizationID).
		Where("tickets.project_id IN ?", authorized.ProjectIDs).
		Group("categories.name").
		Order("categories.name ASC").
		Limit(AnalyticsMaxCategoryValues + 1).
		Scan(&categoryCounts).Error; err != nil {
		return nil, err
	}
	if len(categoryCounts) > AnalyticsMaxCategoryValues {
		return nil, ErrAnalyticsResultTooLarge
	}
	for _, value := range categoryCounts {
		if value.CategoryName != "" {
			stats.ByCategory[value.CategoryName] = value.Count
		}
	}

	now := time.Now()
	today := dayStart(now)
	weekStart := today.AddDate(0, 0, -int(today.Weekday()))
	monthStart := time.Date(
		now.Year(),
		now.Month(),
		1,
		0,
		0,
		0,
		0,
		now.Location(),
	)
	if err := s.authorizedTickets(ctx, authorized).
		Where("created_at >= ?", today).
		Count(&stats.Today).Error; err != nil {
		return nil, err
	}
	if err := s.authorizedTickets(ctx, authorized).
		Where("created_at >= ?", weekStart).
		Count(&stats.ThisWeek).Error; err != nil {
		return nil, err
	}
	if err := s.authorizedTickets(ctx, authorized).
		Where("created_at >= ?", monthStart).
		Count(&stats.ThisMonth).Error; err != nil {
		return nil, err
	}

	responseHours, err := s.averagePersistedTicketMinutes(
		ctx,
		authorized,
		"response_time",
		nil,
	)
	if err != nil {
		return nil, err
	}
	resolutionHours, err := s.averagePersistedTicketMinutes(
		ctx,
		authorized,
		"resolution_time",
		[]models.TicketStatus{
			models.TicketStatusResolved,
			models.TicketStatusClosed,
		},
	)
	if err != nil {
		return nil, err
	}
	stats.AvgResponseTime = responseHours
	stats.AvgResolutionTime = resolutionHours
	return &stats, nil
}

func (s *AnalyticsService) averagePersistedTicketMinutes(
	ctx context.Context,
	authorized AnalyticsAuthorizedProjectSet,
	column string,
	statuses []models.TicketStatus,
) (*float64, error) {
	var average sql.NullFloat64
	query := s.authorizedTickets(ctx, authorized).
		Select("AVG(" + column + ")").
		Where(column + " IS NOT NULL AND " + column + " >= 0")
	if len(statuses) > 0 {
		query = query.Where("status IN ?", statuses)
	}
	if err := query.Scan(&average).Error; err != nil {
		return nil, err
	}
	if !average.Valid {
		return nil, nil
	}
	hours := average.Float64 / 60
	return &hours, nil
}

func (s *AnalyticsService) authorizedTickets(
	ctx context.Context,
	authorized AnalyticsAuthorizedProjectSet,
) *gorm.DB {
	return s.db.WithContext(ctx).
		Model(&models.Ticket{}).
		Where("organization_id = ?", authorized.OrganizationID).
		Where("project_id IN ?", authorized.ProjectIDs)
}

func (s *AnalyticsService) getProjectMembershipStats(
	ctx context.Context,
	authorized AnalyticsAuthorizedProjectSet,
) (*ProjectMembershipStats, error) {
	stats := ProjectMembershipStats{}
	base := func() *gorm.DB {
		return s.db.WithContext(ctx).
			Table("project_memberships AS memberships").
			Joins("JOIN projects ON projects.id = memberships.project_id").
			Joins("JOIN users ON users.id = memberships.user_id").
			Where("projects.organization_id = ?", authorized.OrganizationID).
			Where("projects.id IN ?", authorized.ProjectIDs).
			Where("projects.status = ?", models.ProjectStatusActive).
			Where("memberships.project_id IN ?", authorized.ProjectIDs).
			Where("memberships.is_active = ?", true).
			Where("users.deleted_at IS NULL").
			Where("users.status = ?", models.UserStatusActive)
	}
	if err := base().Count(&stats.Total).Error; err != nil {
		return nil, err
	}
	if err := base().
		Select("COUNT(DISTINCT memberships.user_id)").
		Scan(&stats.ActiveUsers).Error; err != nil {
		return nil, err
	}
	var roleCounts []struct {
		Role  models.ProjectRole
		Count int64
	}
	if err := base().
		Select("memberships.role, count(*) AS count").
		Group("memberships.role").
		Scan(&roleCounts).Error; err != nil {
		return nil, err
	}
	for _, value := range roleCounts {
		switch value.Role {
		case models.ProjectRoleAdmin:
			stats.ProjectAdmins = value.Count
		case models.ProjectRoleManager:
			stats.Managers = value.Count
		case models.ProjectRoleAgent:
			stats.Agents = value.Count
		case models.ProjectRoleRequester:
			stats.Requesters = value.Count
		case models.ProjectRoleObserver:
			stats.Observers = value.Count
		}
	}
	return &stats, nil
}

func (s *AnalyticsService) getActivityStats(
	ctx context.Context,
	authorized AnalyticsAuthorizedProjectSet,
) (*ActivityStats, error) {
	stats := ActivityStats{}
	comments := func() *gorm.DB {
		return s.db.WithContext(ctx).
			Model(&models.TicketComment{}).
			Where("organization_id = ?", authorized.OrganizationID).
			Where("project_id IN ?", authorized.ProjectIDs).
			Where("deleted_at IS NULL")
	}
	if err := comments().Count(&stats.TotalComments).Error; err != nil {
		return nil, err
	}
	now := time.Now()
	today := dayStart(now)
	weekStart := today.AddDate(0, 0, -int(today.Weekday()))
	if err := comments().
		Where("created_at >= ?", today).
		Count(&stats.TodayComments).Error; err != nil {
		return nil, err
	}
	if err := comments().
		Where("created_at >= ?", weekStart).
		Count(&stats.WeekComments).Error; err != nil {
		return nil, err
	}
	if err := s.authorizedTickets(ctx, authorized).
		Where("category_id IS NOT NULL").
		Select("COUNT(DISTINCT category_id)").
		Scan(&stats.TotalCategories).Error; err != nil {
		return nil, err
	}
	return &stats, nil
}

func (s *AnalyticsService) getPlatformUserStats(
	ctx context.Context,
) (*PlatformUserStats, error) {
	stats := PlatformUserStats{}
	if err := s.db.WithContext(ctx).
		Model(&models.User{}).
		Count(&stats.Total).Error; err != nil {
		return nil, err
	}
	var roleCounts []struct {
		PlatformRole models.PlatformRole
		Count        int64
	}
	if err := s.db.WithContext(ctx).
		Model(&models.User{}).
		Select("platform_role, count(*) AS count").
		Group("platform_role").
		Scan(&roleCounts).Error; err != nil {
		return nil, err
	}
	for _, value := range roleCounts {
		switch value.PlatformRole {
		case models.PlatformRolePlatformAdmin:
			stats.PlatformAdmins = value.Count
		case models.PlatformRoleSecurityAuditor:
			stats.SecurityAuditors = value.Count
		case models.PlatformRoleEmergencyOperator:
			stats.EmergencyOperators = value.Count
		case models.PlatformRoleMember:
			stats.Members = value.Count
		}
	}

	now := time.Now()
	today := dayStart(now)
	weekStart := today.AddDate(0, 0, -int(today.Weekday()))
	monthStart := time.Date(
		now.Year(),
		now.Month(),
		1,
		0,
		0,
		0,
		0,
		now.Location(),
	)
	if err := s.db.WithContext(ctx).
		Model(&models.LoginHistory{}).
		Select("COUNT(DISTINCT user_id)").
		Where("login_time >= ?", now.AddDate(0, 0, -30)).
		Scan(&stats.Active).Error; err != nil {
		return nil, err
	}
	if err := s.db.WithContext(ctx).
		Model(&models.LoginHistory{}).
		Where("login_time >= ?", today).
		Count(&stats.TodayLogins).Error; err != nil {
		return nil, err
	}
	if err := s.db.WithContext(ctx).
		Model(&models.LoginHistory{}).
		Where("login_time >= ?", weekStart).
		Count(&stats.WeekLogins).Error; err != nil {
		return nil, err
	}
	if err := s.db.WithContext(ctx).
		Model(&models.LoginHistory{}).
		Where("login_time >= ?", monthStart).
		Count(&stats.MonthLogins).Error; err != nil {
		return nil, err
	}
	return &stats, nil
}

func (s *AnalyticsService) getPlatformCleanupStats(
	ctx context.Context,
) (*PlatformCleanupStats, error) {
	stats := PlatformCleanupStats{}
	if err := s.db.WithContext(ctx).
		Model(&models.CleanupLog{}).
		Count(&stats.CleanupJobs).Error; err != nil {
		return nil, err
	}
	var latest models.CleanupLog
	err := s.db.WithContext(ctx).
		Model(&models.CleanupLog{}).
		Order("start_time DESC").
		First(&latest).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return &stats, nil
	case err != nil:
		return nil, err
	default:
		value := latest.StartTime
		stats.LastCleanup = &value
		return &stats, nil
	}
}

func (s *AnalyticsService) GetTimeRangeStats(
	ctx context.Context,
	authorized AnalyticsAuthorizedProjectSet,
	startDate time.Time,
	endDate time.Time,
) (*TimeRangeStats, error) {
	if err := authorized.validate(); err != nil {
		return nil, err
	}
	authorized = authorized.snapshot()
	if err := ValidateAnalyticsTimeRange(startDate, endDate); err != nil {
		return nil, err
	}
	stats := &TimeRangeStats{
		StartDate:         startDate,
		EndDate:           endDate,
		TicketTrend:       make([]DailyCount, 0),
		UserActivityTrend: make([]DailyCount, 0),
		CommentTrend:      make([]DailyCount, 0),
	}
	err := s.withAuthorizedHumanProjectSetTransaction(
		ctx,
		authorized,
		func(scopedCtx context.Context) error {
			var queryErr error
			stats.TicketTrend, queryErr = s.getDailyTicketTrend(
				scopedCtx,
				authorized,
				startDate,
				endDate,
			)
			if queryErr != nil {
				return fmt.Errorf("get ticket trend: %w", queryErr)
			}
			stats.UserActivityTrend, queryErr = s.getDailyUserActivityTrend(
				scopedCtx,
				authorized,
				startDate,
				endDate,
			)
			if queryErr != nil {
				return fmt.Errorf("get user activity trend: %w", queryErr)
			}
			stats.CommentTrend, queryErr = s.getDailyCommentTrend(
				scopedCtx,
				authorized,
				startDate,
				endDate,
			)
			if queryErr != nil {
				return fmt.Errorf("get comment trend: %w", queryErr)
			}
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("query authorized time-range stats: %w", err)
	}
	return stats, nil
}

func (s *AnalyticsService) getDailyTicketTrend(
	ctx context.Context,
	authorized AnalyticsAuthorizedProjectSet,
	startDate time.Time,
	endDate time.Time,
) ([]DailyCount, error) {
	return s.dailyTrend(
		s.authorizedTickets(ctx, authorized).
			Where("created_at >= ? AND created_at <= ?", startDate, endDate),
		"created_at",
	)
}

func (s *AnalyticsService) getDailyUserActivityTrend(
	ctx context.Context,
	authorized AnalyticsAuthorizedProjectSet,
	startDate time.Time,
	endDate time.Time,
) ([]DailyCount, error) {
	query := s.db.WithContext(ctx).
		Table("login_histories AS login_histories").
		Joins("JOIN users ON users.id = login_histories.user_id").
		Where("users.deleted_at IS NULL").
		Where("users.status = ?", models.UserStatusActive).
		Where("login_histories.login_time >= ?", startDate).
		Where("login_histories.login_time <= ?", endDate).
		Where(
			`EXISTS (
				SELECT 1
				FROM project_memberships AS memberships
				JOIN projects ON projects.id = memberships.project_id
				WHERE memberships.user_id = login_histories.user_id
					AND memberships.is_active = ?
					AND memberships.project_id IN ?
					AND projects.organization_id = ?
					AND projects.id IN ?
					AND projects.status = ?
			)`,
			true,
			authorized.ProjectIDs,
			authorized.OrganizationID,
			authorized.ProjectIDs,
			models.ProjectStatusActive,
		)
	return s.dailyDistinctUserTrend(query, "login_histories.login_time")
}

func (s *AnalyticsService) getDailyCommentTrend(
	ctx context.Context,
	authorized AnalyticsAuthorizedProjectSet,
	startDate time.Time,
	endDate time.Time,
) ([]DailyCount, error) {
	query := s.db.WithContext(ctx).
		Model(&models.TicketComment{}).
		Where("organization_id = ?", authorized.OrganizationID).
		Where("project_id IN ?", authorized.ProjectIDs).
		Where("deleted_at IS NULL").
		Where("created_at >= ? AND created_at <= ?", startDate, endDate)
	return s.dailyTrend(query, "created_at")
}

func (s *AnalyticsService) dailyTrend(
	query *gorm.DB,
	timeColumn string,
) ([]DailyCount, error) {
	var rows []struct {
		Date  string `gorm:"column:date"`
		Count int64
	}
	if err := query.
		Select("DATE(" + timeColumn + ") AS date, COUNT(*) AS count").
		Group("DATE(" + timeColumn + ")").
		Order("date").
		Limit(AnalyticsMaxTimeRangeDays + 1).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	return parseDailyCounts(rows)
}

func (s *AnalyticsService) dailyDistinctUserTrend(
	query *gorm.DB,
	timeColumn string,
) ([]DailyCount, error) {
	var rows []struct {
		Date  string `gorm:"column:date"`
		Count int64
	}
	if err := query.
		Select(
			"DATE(" + timeColumn + ") AS date, " +
				"COUNT(DISTINCT login_histories.user_id) AS count",
		).
		Group("DATE(" + timeColumn + ")").
		Order("date").
		Limit(AnalyticsMaxTimeRangeDays + 1).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	return parseDailyCounts(rows)
}

func parseDailyCounts(
	rows []struct {
		Date  string `gorm:"column:date"`
		Count int64
	},
) ([]DailyCount, error) {
	if len(rows) > AnalyticsMaxTimeRangeDays {
		return nil, ErrAnalyticsResultTooLarge
	}
	values := make([]DailyCount, 0, len(rows))
	for _, row := range rows {
		date, err := time.Parse("2006-01-02", row.Date)
		if err != nil {
			return nil, fmt.Errorf("parse analytics date %q: %w", row.Date, err)
		}
		values = append(values, DailyCount{Date: date, Count: row.Count})
	}
	return values, nil
}

func (s *AnalyticsService) ExportStats(
	ctx context.Context,
	authorized AnalyticsAuthorizedProjectSet,
	format string,
	startDate *time.Time,
	endDate *time.Time,
) ([]byte, error) {
	if format != "json" {
		return nil, fmt.Errorf("unsupported export format: %s", format)
	}
	if err := authorized.validate(); err != nil {
		return nil, err
	}
	if (startDate == nil) != (endDate == nil) {
		return nil, ErrAnalyticsInvalidTimeRange
	}
	if startDate != nil {
		if err := ValidateAnalyticsTimeRange(*startDate, *endDate); err != nil {
			return nil, err
		}
	}
	platformStats, err := s.GetPlatformStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("get platform stats: %w", err)
	}
	businessStats, err := s.GetBusinessStats(ctx, authorized)
	if err != nil {
		return nil, fmt.Errorf("get business stats: %w", err)
	}
	exportData := map[string]any{
		"export_time":    time.Now(),
		"platform_stats": platformStats,
		"business_stats": businessStats,
	}
	if startDate != nil {
		timeRangeStats, rangeErr := s.GetTimeRangeStats(
			ctx,
			authorized,
			*startDate,
			*endDate,
		)
		if rangeErr != nil {
			return nil, fmt.Errorf("get time range stats: %w", rangeErr)
		}
		exportData["time_range_stats"] = timeRangeStats
	}
	return marshalAnalyticsExport(exportData)
}

// ValidateAnalyticsTimeRange applies the same inclusive UTC date-bucket
// contract to HTTP and direct service callers. A valid range can therefore
// produce no more than AnalyticsMaxTimeRangeDays daily values per series.
func ValidateAnalyticsTimeRange(startDate, endDate time.Time) error {
	if startDate.IsZero() || endDate.IsZero() || endDate.Before(startDate) {
		return ErrAnalyticsInvalidTimeRange
	}
	startYear, startMonth, startDay := startDate.UTC().Date()
	endYear, endMonth, endDay := endDate.UTC().Date()
	startBucket := time.Date(
		startYear,
		startMonth,
		startDay,
		0,
		0,
		0,
		0,
		time.UTC,
	)
	endBucket := time.Date(
		endYear,
		endMonth,
		endDay,
		0,
		0,
		0,
		0,
		time.UTC,
	)
	inclusiveDays := int(endBucket.Sub(startBucket)/(24*time.Hour)) + 1
	if inclusiveDays < 1 || inclusiveDays > AnalyticsMaxTimeRangeDays {
		return ErrAnalyticsInvalidTimeRange
	}
	return nil
}

func marshalAnalyticsExport(exportData map[string]any) ([]byte, error) {
	data, err := json.MarshalIndent(exportData, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(data) > AnalyticsMaxExportBytes {
		return nil, ErrAnalyticsExportTooLarge
	}
	return data, nil
}

func dayStart(value time.Time) time.Time {
	return time.Date(
		value.Year(),
		value.Month(),
		value.Day(),
		0,
		0,
		0,
		0,
		value.Location(),
	)
}
