package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

var (
	ErrSLAConfigNotFound   = errors.New("no suitable SLA config found")
	ErrInvalidWorkingHours = errors.New("invalid SLA working hours")
)

// SLAService is the authoritative domain implementation for selecting an SLA
// policy, calculating its deadlines, and evaluating a ticket against it.
// Persisted ticket SLA fields are projections of this service's result.
type SLAService struct {
	db  *gorm.DB
	now func() time.Time
}

func NewSLAService(db *gorm.DB) *SLAService {
	return newSLAServiceWithClock(db, time.Now)
}

func newSLAServiceWithClock(db *gorm.DB, now func() time.Time) *SLAService {
	if now == nil {
		now = time.Now
	}
	return &SLAService{db: db, now: now}
}

// TicketSLAStatus describes the current SLA evaluation for one ticket.
type TicketSLAStatus struct {
	TicketID                 uint              `json:"ticket_id"`
	ResponseDeadline         time.Time         `json:"response_deadline"`
	ResolutionDeadline       time.Time         `json:"resolution_deadline"`
	IsResponseOverdue        bool              `json:"is_response_overdue"`
	IsResolutionOverdue      bool              `json:"is_resolution_overdue"`
	ResponseOverdueMinutes   int64             `json:"response_overdue_minutes"`
	ResolutionOverdueMinutes int64             `json:"resolution_overdue_minutes"`
	SLAConfig                *models.SLAConfig `json:"sla_config,omitempty"`
}

type slaProjection struct {
	DueDate  *time.Time
	Breached bool
}

func (s *SLAService) GetConfigForTicket(
	ctx context.Context,
	ticket *models.Ticket,
) (*models.SLAConfig, error) {
	return s.getConfigForTicketOnDB(ctx, s.db, ticket)
}

func (s *SLAService) getConfigForTicketOnDB(
	ctx context.Context,
	db *gorm.DB,
	ticket *models.Ticket,
) (*models.SLAConfig, error) {
	if db == nil {
		return nil, errors.New("SLA database is required")
	}
	if ticket == nil {
		return nil, errors.New("ticket is required")
	}
	scope, err := slaProjectScopeForTicket(ctx, ticket)
	if err != nil {
		return nil, err
	}

	query := db.WithContext(ctx).
		Model(&models.SLAConfig{}).
		Where(
			"organization_id = ? AND project_id = ? AND is_active = ?",
			scope.OrganizationID,
			scope.ProjectID,
			true,
		)
	conditions := make([]string, 0, 4)
	params := make([]any, 0, 4)

	if ticket.Type != "" {
		conditions = append(conditions, "ticket_type = ? OR ticket_type IS NULL")
		params = append(params, ticket.Type)
	} else {
		conditions = append(conditions, "ticket_type IS NULL")
	}
	if ticket.Priority != "" {
		conditions = append(conditions, "priority = ? OR priority IS NULL")
		params = append(params, ticket.Priority)
	} else {
		conditions = append(conditions, "priority IS NULL")
	}
	if ticket.AssignedToID != nil {
		conditions = append(conditions, "assigned_user_id = ? OR assigned_user_id IS NULL")
		params = append(params, *ticket.AssignedToID)
	} else {
		conditions = append(conditions, "assigned_user_id IS NULL")
	}

	category, err := slaTicketCategoryOnDB(ctx, db, ticket)
	if err != nil {
		return nil, err
	}
	if category != nil {
		conditions = append(conditions, "category IN ? OR category IS NULL")
		params = append(params, []string{category.Slug, category.Name})
	} else {
		conditions = append(conditions, "category IS NULL")
	}

	query = query.Where("("+strings.Join(conditions, ") AND (")+")", params...)

	var config models.SLAConfig
	err = query.
		Order("(ticket_type IS NOT NULL) DESC").
		Order("(priority IS NOT NULL) DESC").
		Order("(category IS NOT NULL) DESC").
		Order("(assigned_user_id IS NOT NULL) DESC").
		Order("is_default DESC").
		Order("id ASC").
		First(&config).Error
	if err == nil {
		return &config, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("get SLA config: %w", err)
	}

	if err := db.WithContext(ctx).
		Where(
			"organization_id = ? AND project_id = ? AND is_default = ? AND is_active = ?",
			scope.OrganizationID,
			scope.ProjectID,
			true,
			true,
		).
		Order("id ASC").
		First(&config).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrSLAConfigNotFound
		}
		return nil, fmt.Errorf("get default SLA config: %w", err)
	}
	return &config, nil
}

func slaProjectScopeForTicket(
	ctx context.Context,
	ticket *models.Ticket,
) (models.ProjectScope, error) {
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return models.ProjectScope{}, fmt.Errorf(
			"trusted SLA project scope is required: %w",
			err,
		)
	}
	if ticket == nil ||
		ticket.OrganizationID != scope.OrganizationID ||
		ticket.ProjectID != scope.ProjectID {
		return models.ProjectScope{}, errors.New(
			"ticket does not match trusted SLA project scope",
		)
	}
	return scope, nil
}

func slaTicketCategoryOnDB(
	ctx context.Context,
	db *gorm.DB,
	ticket *models.Ticket,
) (*models.Category, error) {
	if ticket.CategoryID == nil {
		return nil, nil
	}
	if ticket.Category != nil && ticket.Category.ID == *ticket.CategoryID {
		return ticket.Category, nil
	}
	var category models.Category
	if err := db.WithContext(ctx).
		Select("id", "name", "slug").
		First(&category, *ticket.CategoryID).Error; err != nil {
		return nil, fmt.Errorf("get ticket category for SLA: %w", err)
	}
	return &category, nil
}

func (s *SLAService) CalculateDeadlines(
	ctx context.Context,
	ticket *models.Ticket,
	config *models.SLAConfig,
) (responseDeadline, resolutionDeadline time.Time, err error) {
	if ticket == nil {
		return time.Time{}, time.Time{}, errors.New("ticket is required")
	}
	if config == nil {
		return time.Time{}, time.Time{}, errors.New("SLA config is required")
	}
	scope, err := slaProjectScopeForTicket(ctx, ticket)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if config.OrganizationID != scope.OrganizationID ||
		config.ProjectID != scope.ProjectID {
		return time.Time{}, time.Time{}, errors.New(
			"SLA config does not match trusted project scope",
		)
	}
	if ticket.CreatedAt.IsZero() {
		return time.Time{}, time.Time{}, errors.New("ticket creation time is required")
	}

	workingHours, err := config.GetWorkingHours()
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("get SLA working hours: %w", err)
	}
	responseDeadline, err = s.addWorkingTime(
		ticket.CreatedAt,
		time.Duration(config.ResponseTime)*time.Minute,
		workingHours,
		config.ExcludeWeekends,
		config.ExcludeHolidays,
	)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	resolutionDeadline, err = s.addWorkingTime(
		ticket.CreatedAt,
		time.Duration(config.ResolutionTime)*time.Minute,
		workingHours,
		config.ExcludeWeekends,
		config.ExcludeHolidays,
	)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return responseDeadline, resolutionDeadline, nil
}

func (s *SLAService) addWorkingTime(
	startTime time.Time,
	duration time.Duration,
	workingHours *models.WorkingHours,
	excludeWeekends, excludeHolidays bool,
) (time.Time, error) {
	if duration < 0 {
		return time.Time{}, fmt.Errorf("%w: duration must not be negative", ErrInvalidWorkingHours)
	}
	if duration == 0 {
		return startTime, nil
	}

	schedule, err := prepareWorkingSchedule(
		workingHours,
		excludeWeekends,
		excludeHolidays,
		startTime.Location(),
	)
	if err != nil {
		return time.Time{}, err
	}

	current := startTime.In(schedule.location)
	remaining := duration
	const maximumCalendarDays = 366 * 100
	for dayCount := 0; dayCount < maximumCalendarDays; dayCount++ {
		dayStart := time.Date(
			current.Year(),
			current.Month(),
			current.Day(),
			0,
			0,
			0,
			0,
			schedule.location,
		)
		if schedule.isExcluded(dayStart) {
			current = dayStart.AddDate(0, 0, 1)
			continue
		}

		interval, ok := schedule.intervals[current.Weekday()]
		if !ok {
			current = dayStart.AddDate(0, 0, 1)
			continue
		}
		windowStart := dayStart.Add(interval.start)
		windowEnd := dayStart.Add(interval.end)
		if current.Before(windowStart) {
			current = windowStart
		}
		if !current.Before(windowEnd) {
			current = dayStart.AddDate(0, 0, 1)
			continue
		}

		available := windowEnd.Sub(current)
		if remaining <= available {
			return current.Add(remaining).In(startTime.Location()), nil
		}
		remaining -= available
		current = dayStart.AddDate(0, 0, 1)
	}

	return time.Time{}, fmt.Errorf(
		"%w: deadline exceeds supported calendar range",
		ErrInvalidWorkingHours,
	)
}

func (s *SLAService) CheckTicket(
	ctx context.Context,
	ticket *models.Ticket,
) (*TicketSLAStatus, error) {
	return s.checkTicketOnDB(ctx, s.db, ticket, s.now())
}

func (s *SLAService) checkTicketOnDB(
	ctx context.Context,
	db *gorm.DB,
	ticket *models.Ticket,
	now time.Time,
) (*TicketSLAStatus, error) {
	config, err := s.getConfigForTicketOnDB(ctx, db, ticket)
	if err != nil {
		return nil, err
	}
	responseDeadline, resolutionDeadline, err := s.CalculateDeadlines(ctx, ticket, config)
	if err != nil {
		return nil, fmt.Errorf("calculate SLA deadlines: %w", err)
	}

	status := &TicketSLAStatus{
		TicketID:           ticket.ID,
		ResponseDeadline:   responseDeadline,
		ResolutionDeadline: resolutionDeadline,
		SLAConfig:          config,
	}
	if isSLATerminalStatus(ticket.Status) {
		return status, nil
	}

	hasFirstResponse, err := hasTicketFirstResponseOnDB(ctx, db, ticket)
	if err != nil {
		return nil, err
	}
	if now.After(responseDeadline) && !hasFirstResponse {
		status.IsResponseOverdue = true
		status.ResponseOverdueMinutes = int64(now.Sub(responseDeadline).Minutes())
	}
	if now.After(resolutionDeadline) {
		status.IsResolutionOverdue = true
		status.ResolutionOverdueMinutes = int64(now.Sub(resolutionDeadline).Minutes())
	}
	return status, nil
}

func hasTicketFirstResponseOnDB(
	ctx context.Context,
	db *gorm.DB,
	ticket *models.Ticket,
) (bool, error) {
	if ticket == nil || ticket.ID == 0 {
		return false, nil
	}
	scope, err := slaProjectScopeForTicket(ctx, ticket)
	if err != nil {
		return false, err
	}
	var count int64
	if err := db.WithContext(ctx).
		Model(&models.TicketComment{}).
		Where(
			"organization_id = ? AND project_id = ? AND ticket_id = ? AND type != ? AND is_deleted = ?",
			scope.OrganizationID,
			scope.ProjectID,
			ticket.ID,
			models.CommentTypeSystem,
			false,
		).
		Count(&count).Error; err != nil {
		return false, fmt.Errorf("check ticket first response: %w", err)
	}
	return count > 0, nil
}

func isSLATerminalStatus(status models.TicketStatus) bool {
	switch status {
	case models.TicketStatusResolved, models.TicketStatusClosed, models.TicketStatusCancelled:
		return true
	default:
		return false
	}
}

func slaActiveTicketStatuses() []models.TicketStatus {
	return []models.TicketStatus{
		models.TicketStatusOpen,
		models.TicketStatusInProgress,
		models.TicketStatusPending,
	}
}

func (s *SLAService) projectionForTicketOnDB(
	ctx context.Context,
	db *gorm.DB,
	ticket *models.Ticket,
	now time.Time,
) (slaProjection, *TicketSLAStatus, error) {
	status, err := s.checkTicketOnDB(ctx, db, ticket, now)
	if errors.Is(err, ErrSLAConfigNotFound) {
		return slaProjection{}, nil, nil
	}
	if err != nil {
		return slaProjection{}, nil, err
	}
	dueDate := status.ResolutionDeadline
	return slaProjection{
		DueDate:  &dueDate,
		Breached: status.IsResponseOverdue || status.IsResolutionOverdue,
	}, status, nil
}

func slaProjectionChanges(
	ticket *models.Ticket,
	projection slaProjection,
) map[string]any {
	changes := make(map[string]any, 2)
	if !equalOptionalTime(ticket.SLADueDate, projection.DueDate) {
		changes["sla_due_date"] = projection.DueDate
	}
	if ticket.SLABreached != projection.Breached {
		changes["sla_breached"] = projection.Breached
	}
	return changes
}

func slaProjectionFieldNames(changes map[string]any) []string {
	fields := make([]string, 0, 2)
	if _, ok := changes["sla_breached"]; ok {
		fields = append(fields, "sla_breached")
	}
	if _, ok := changes["sla_due_date"]; ok {
		fields = append(fields, "sla_due_date")
	}
	return fields
}

func applySLAProjection(ticket *models.Ticket, projection slaProjection) {
	ticket.SLADueDate = projection.DueDate
	ticket.SLABreached = projection.Breached
}

func equalOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
