package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"

	"gorm.io/gorm"
)

var (
	ErrInvalidTicketTransition  = errors.New("invalid ticket status transition")
	ErrInvalidBulkTicketUpdate  = errors.New("invalid bulk ticket update")
	ErrTicketCreateAccessDenied = errors.New(
		"human ticket creation requires an authorized active project membership",
	)
)

// TicketServiceInterface defines the interface for ticket service
type TicketServiceInterface interface {
	GetTickets(ctx context.Context, filters TicketFilters) ([]*models.Ticket, int64, error)
	GetTicket(ctx context.Context, id uint) (*models.Ticket, error)
	CreateTicket(ctx context.Context, req *models.TicketCreateRequest, userID uint) (*models.Ticket, error)
	UpdateTicketExpectedVersion(ctx context.Context, id uint, req *models.TicketUpdateRequest, userID uint, expectedVersion uint64) (*models.Ticket, error)
	DeleteTicketExpectedVersion(ctx context.Context, id uint, userID uint, userRole string, expectedVersion uint64) error
	AssignTicketExpectedVersion(ctx context.Context, ticketID uint, assigneeID uint, userID uint, comment string, expectedVersion uint64) (*models.Ticket, error)
	TransferTicketExpectedVersion(ctx context.Context, ticketID uint, assigneeID uint, userID uint, comment string, transferReason string, expectedVersion uint64) (*models.Ticket, error)
	EscalateTicketExpectedVersion(ctx context.Context, ticketID uint, escalateToID uint, userID uint, reason string, comment string, expectedVersion uint64) (*models.Ticket, error)
	UpdateTicketStatusExpectedVersion(ctx context.Context, ticketID uint, status string, userID uint, comment string, resolutionNotes string, expectedVersion uint64) (*models.Ticket, error)
	GetTicketStatistics(ctx context.Context, userID uint, role string) (*TicketStatisticsResponse, error)
	GetUserTickets(ctx context.Context, userID uint, status string, priority string, limit int) ([]*models.Ticket, int64, error)
	GetUnassignedTickets(ctx context.Context, priority string, categoryID string, limit int) ([]*models.Ticket, int64, error)
	GetOverdueTickets(ctx context.Context, userID uint, role string) ([]*models.Ticket, int64, error)
	GetSLABreachedTickets(ctx context.Context, userID uint, role string) ([]*models.Ticket, int64, error)
	BulkUpdateTickets(ctx context.Context, req *BulkUpdateRequest, userID uint) (*BulkUpdateResult, error)
	GetTicketHistory(ctx context.Context, ticketID uint) ([]*models.TicketHistory, int64, error)
}

// TicketService implements TicketServiceInterface
type TicketService struct {
	db            *gorm.DB
	agentNative   *AgentNativeService
	projects      *ProjectService
	statsCache    StatsCache
	statsCacheTTL time.Duration
}

var ticketSortableColumns = map[string]string{
	"id":            "id",
	"ticket_number": "ticket_number",
	"title":         "title",
	"status":        "status",
	"priority":      "priority",
	"due_date":      "due_date",
	"created_at":    "created_at",
	"updated_at":    "updated_at",
}

// StatsCache defines the minimal cache interface used by ticket statistics.
type StatsCache interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error
}

// NewTicketService constructs the Human REST ticket adapter over the same
// transactional Agent-native event service used by REST v1, MCP, and A2A.
// Both dependencies are mandatory so a write path can never silently skip its
// DomainEvent and Outbox records.
func NewTicketService(
	db *gorm.DB,
	native *AgentNativeService,
	cache StatsCache,
	ttl time.Duration,
) (*TicketService, error) {
	if db == nil {
		return nil, errors.New("ticket database is required")
	}
	if native == nil {
		return nil, errors.New("agent-native event service is required")
	}
	service := &TicketService{
		db:          db,
		agentNative: native,
	}
	projects, err := NewProjectService(db)
	if err != nil {
		return nil, err
	}
	service.projects = projects
	if cache != nil && ttl > 0 {
		service.statsCache = cache
		service.statsCacheTTL = ttl
	}
	return service, nil
}

// TicketFilters represents filters for ticket queries
type TicketFilters struct {
	Status      string
	Priority    string
	Type        string
	Source      string
	Tags        []string
	AssigneeID  *uint
	CreatorID   *uint
	SLABreached *bool
	IsOverdue   *bool
	Unassigned  *bool
	Search      string
	Page        int
	Limit       int
	SortBy      string
	SortOrder   string
}

// TicketStatisticsResponse represents enhanced ticket statistics for dashboard
type TicketStatisticsResponse struct {
	Total        int64            `json:"total"`
	Open         int64            `json:"open"`
	InProgress   int64            `json:"in_progress"`
	Pending      int64            `json:"pending"`
	Resolved     int64            `json:"resolved"`
	Closed       int64            `json:"closed"`
	Overdue      int64            `json:"overdue"`
	SLABreached  int64            `json:"sla_breached"`
	MyTickets    int64            `json:"my_tickets"`
	Unassigned   int64            `json:"unassigned"`
	HighPriority int64            `json:"high_priority"`
	Escalated    int64            `json:"escalated"`
	ByPriority   map[string]int64 `json:"by_priority"`
	ByCategory   map[string]int64 `json:"by_category"`
}

// TicketVersionPrecondition binds a bulk command to the exact list snapshot
// from which a human initiated it.
type TicketVersionPrecondition struct {
	ID      uint   `json:"id" binding:"required,gt=0"`
	Version uint64 `json:"version" binding:"required,gt=0"`
}

type TicketVersionReceipt struct {
	ID      uint   `json:"id"`
	Version uint64 `json:"version"`
}

type BulkUpdateResult struct {
	Tickets []TicketVersionReceipt `json:"tickets"`
}

// BulkUpdateRequest represents bulk update request
type BulkUpdateRequest struct {
	Tickets      []TicketVersionPrecondition `json:"tickets"`
	Status       *string                     `json:"status,omitempty"`
	Priority     *string                     `json:"priority,omitempty"`
	AssignedToID *uint                       `json:"assigned_to_id,omitempty"`
	Tags         []string                    `json:"tags,omitempty"`
	CustomFields map[string]interface{}      `json:"custom_fields,omitempty"`
}

// GetTickets retrieves tickets with filters
func (s *TicketService) GetTickets(ctx context.Context, filters TicketFilters) ([]*models.Ticket, int64, error) {
	var tickets []*models.Ticket
	var total int64

	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return nil, 0, err
	}
	query := scopedTicketQuery(s.db.WithContext(ctx).Model(&models.Ticket{}), scope)

	// Apply filters
	if filters.Status != "" {
		statuses := splitCommaSeparated(filters.Status)
		if len(statuses) == 1 {
			query = query.Where("status = ?", statuses[0])
		} else if len(statuses) > 1 {
			query = query.Where("status IN ?", statuses)
		}
	}
	if filters.Priority != "" {
		priorities := splitCommaSeparated(filters.Priority)
		if len(priorities) == 1 {
			query = query.Where("priority = ?", priorities[0])
		} else if len(priorities) > 1 {
			query = query.Where("priority IN ?", priorities)
		}
	}
	if filters.Type != "" {
		query = query.Where("type = ?", filters.Type)
	}
	if filters.Source != "" {
		query = query.Where("source = ?", filters.Source)
	}
	if filters.AssigneeID != nil {
		query = query.Where("assigned_to_id = ?", *filters.AssigneeID)
	}
	if filters.CreatorID != nil {
		query = query.Where("created_by_id = ?", *filters.CreatorID)
	}
	if filters.Search != "" {
		query = query.Where("title ILIKE ? OR description ILIKE ?", "%"+filters.Search+"%", "%"+filters.Search+"%")
	}
	if len(filters.Tags) > 0 {
		for _, tag := range filters.Tags {
			trimmed := strings.TrimSpace(tag)
			if trimmed == "" {
				continue
			}
			query = query.Where("tags::jsonb ? ?", trimmed)
		}
	}
	if filters.SLABreached != nil {
		if *filters.SLABreached {
			query = query.Where(
				"sla_breached = ? AND status IN ?",
				true,
				slaActiveTicketStatuses(),
			)
		} else {
			query = query.Where(
				"sla_breached = ? OR status NOT IN ?",
				false,
				slaActiveTicketStatuses(),
			)
		}
	}
	if filters.IsOverdue != nil {
		now := time.Now()
		if *filters.IsOverdue {
			query = query.Where("due_date < ? AND status NOT IN (?, ?)", now, models.TicketStatusResolved, models.TicketStatusClosed)
		} else {
			query = query.Where("(due_date IS NULL OR due_date >= ?) OR status IN (?, ?)", now, models.TicketStatusResolved, models.TicketStatusClosed)
		}
	}
	if filters.Unassigned != nil {
		if *filters.Unassigned {
			query = query.Where("assigned_to_id IS NULL")
		} else {
			query = query.Where("assigned_to_id IS NOT NULL")
		}
	}

	// Count total
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count tickets: %w", err)
	}

	// Apply pagination
	if filters.Page > 0 && filters.Limit > 0 {
		offset := (filters.Page - 1) * filters.Limit
		query = query.Offset(offset).Limit(filters.Limit)
	}

	// Apply sorting (whitelist)
	sortBy, sortOrder := sanitizeTicketSort(filters.SortBy, filters.SortOrder)
	query = query.Order(fmt.Sprintf("%s %s", sortBy, sortOrder))

	// Preload associations
	query = query.Preload("CreatedBy").Preload("AssignedTo")

	if err := query.Find(&tickets).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to get tickets: %w", err)
	}

	return tickets, total, nil
}

func splitCommaSeparated(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func sanitizeTicketSort(sortBy, sortOrder string) (string, string) {
	column := "created_at"
	if requested := strings.ToLower(strings.TrimSpace(sortBy)); requested != "" {
		if whitelisted, ok := ticketSortableColumns[requested]; ok {
			column = whitelisted
		}
	}

	direction := "DESC"
	switch strings.ToLower(strings.TrimSpace(sortOrder)) {
	case "asc":
		direction = "ASC"
	case "desc":
		direction = "DESC"
	}

	return column, direction
}

// GetTicket retrieves a single ticket by ID
func (s *TicketService) GetTicket(ctx context.Context, id uint) (*models.Ticket, error) {
	var ticket models.Ticket
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return nil, err
	}

	err = s.db.WithContext(ctx).
		Preload("CreatedBy").
		Preload("AssignedTo").
		Preload("Comments").
		Preload("Comments.User").
		Where(
			"tickets.id = ? AND tickets.organization_id = ? AND tickets.project_id = ?",
			id,
			scope.OrganizationID,
			scope.ProjectID,
		).
		First(&ticket).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("ticket not found")
		}
		return nil, fmt.Errorf("failed to get ticket: %w", err)
	}

	return &ticket, nil
}

// CreateTicket creates a new ticket
func (s *TicketService) CreateTicket(ctx context.Context, req *models.TicketCreateRequest, userID uint) (*models.Ticket, error) {
	if req == nil || userID == 0 {
		return nil, fmt.Errorf("human ticket create request and actor are required")
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if operation.Actor != models.HumanActor(userID) {
		return nil, fmt.Errorf(
			"human ticket actor does not match operation context",
		)
	}
	role, err := s.projects.activeHumanMembershipRole(
		ctx,
		operation.Scope,
		userID,
	)
	if err != nil {
		if errors.Is(err, ErrProjectAccessDenied) {
			return nil, ErrTicketCreateAccessDenied
		}
		return nil, err
	}
	if !humanProjectRoleCanCreateTicket(role) {
		return nil, ErrTicketCreateAccessDenied
	}
	if role == models.ProjectRoleRequester &&
		(req.Status != nil || req.AssignedToID != nil) {
		return nil, ErrTicketCreateAccessDenied
	}
	request := *req
	if request.Source == "" {
		request.Source = models.TicketSourceWeb
	}
	var assignedActor *models.ActorRef
	if request.AssignedToID != nil {
		actor := models.HumanActor(*request.AssignedToID)
		assignedActor = &actor
	}
	result, err := s.agentNative.CreateNativeTicket(ctx, NativeTicketCreateInput{
		Request:        request,
		Actor:          models.HumanActor(userID),
		AssignedActor:  assignedActor,
		SourceProtocol: "rest-human",
		TrustLevel:     models.TicketTrustLevelUntrusted,
	})
	if err != nil {
		return nil, err
	}
	return s.GetTicket(ctx, result.Ticket.ID)
}

func humanProjectRoleCanCreateTicket(role models.ProjectRole) bool {
	switch role {
	case models.ProjectRoleAdmin,
		models.ProjectRoleManager,
		models.ProjectRoleAgent,
		models.ProjectRoleRequester:
		return true
	default:
		return false
	}
}

// UpdateTicketExpectedVersion binds a prior authorization decision to the
// exact ticket version that is updated.
func (s *TicketService) UpdateTicketExpectedVersion(
	ctx context.Context,
	id uint,
	req *models.TicketUpdateRequest,
	userID uint,
	expectedVersion uint64,
) (*models.Ticket, error) {
	if expectedVersion == 0 {
		return nil, ErrVersionConflict
	}
	if req == nil || userID == 0 {
		return nil, fmt.Errorf("human ticket update request and actor are required")
	}
	current, err := s.GetTicket(ctx, id)
	if err != nil {
		return nil, err
	}
	if current.Version != expectedVersion {
		return nil, ErrVersionConflict
	}
	changes, histories, assignmentResolved, err := s.agentNative.buildHumanTicketUpdate(
		ctx,
		current,
		req,
	)
	if err != nil {
		return nil, err
	}
	if len(changes) == 0 {
		return current, nil
	}
	result, err := s.agentNative.UpdateTicketVersion(ctx, VersionedTicketUpdateInput{
		TicketID:           id,
		ExpectedVersion:    expectedVersion,
		Actor:              models.HumanActor(userID),
		SourceProtocol:     "rest-human",
		Changes:            changes,
		EventType:          eventcontract.TicketUpdatedEventType,
		EventData:          map[string]any{"ticket_id": id},
		assignmentResolved: assignmentResolved,
		historyRecords:     histories,
	})
	if err != nil {
		return nil, err
	}
	return s.GetTicket(ctx, result.Ticket.ID)
}

func (s *TicketService) AssignTicketExpectedVersion(
	ctx context.Context,
	ticketID uint,
	assigneeID uint,
	userID uint,
	comment string,
	expectedVersion uint64,
) (*models.Ticket, error) {
	if expectedVersion == 0 {
		return nil, ErrVersionConflict
	}
	ticket, err := s.GetTicket(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if ticket.Version != expectedVersion {
		return nil, ErrVersionConflict
	}
	description := fmt.Sprintf("工单分配给用户 ID: %d", assigneeID)
	if comment != "" {
		description += fmt.Sprintf(" - %s", comment)
	}
	assignee := models.HumanActor(assigneeID)
	result, err := s.agentNative.AssignTicket(ctx, AssignTicketCommand{
		TicketID:        ticketID,
		ExpectedVersion: expectedVersion,
		Actor:           models.HumanActor(userID),
		Assignee:        &assignee,
		SourceProtocol:  "rest-human",
		Reason:          comment,
		historyRecords: []ticketHistorySpec{{
			Action:      models.HistoryActionAssign,
			Description: description,
			FieldName:   "assigned_to_id",
			OldValue:    getAssigneeValue(ticket.AssignedToID),
			NewValue:    strconv.FormatUint(uint64(assigneeID), 10),
			IsVisible:   true,
			IsImportant: true,
		}},
	})
	if err != nil {
		return nil, err
	}
	return s.GetTicket(ctx, result.Ticket.ID)
}

func (s *TicketService) TransferTicketExpectedVersion(
	ctx context.Context,
	ticketID uint,
	assigneeID uint,
	userID uint,
	comment string,
	transferReason string,
	expectedVersion uint64,
) (*models.Ticket, error) {
	if expectedVersion == 0 {
		return nil, ErrVersionConflict
	}
	ticket, err := s.GetTicket(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if ticket.Version != expectedVersion {
		return nil, ErrVersionConflict
	}
	description := fmt.Sprintf(
		"工单从用户 ID: %s 转移给用户 ID: %d",
		getAssigneeValue(ticket.AssignedToID),
		assigneeID,
	)
	if transferReason != "" {
		description += fmt.Sprintf(" (原因: %s)", transferReason)
	}
	if comment != "" {
		description += fmt.Sprintf(" - %s", comment)
	}
	assignee := models.HumanActor(assigneeID)
	result, err := s.agentNative.AssignTicket(ctx, AssignTicketCommand{
		TicketID:        ticketID,
		ExpectedVersion: expectedVersion,
		Actor:           models.HumanActor(userID),
		Assignee:        &assignee,
		SourceProtocol:  "rest-human",
		Reason:          transferReason,
		historyRecords: []ticketHistorySpec{{
			Action:      models.HistoryActionTransfer,
			Description: description,
			FieldName:   "assigned_to_id",
			OldValue:    getAssigneeValue(ticket.AssignedToID),
			NewValue:    strconv.FormatUint(uint64(assigneeID), 10),
			IsVisible:   true,
			IsImportant: true,
		}},
	})
	if err != nil {
		return nil, err
	}
	return s.GetTicket(ctx, result.Ticket.ID)
}

func (s *TicketService) EscalateTicketExpectedVersion(
	ctx context.Context,
	ticketID uint,
	escalateToID uint,
	userID uint,
	reason string,
	comment string,
	expectedVersion uint64,
) (*models.Ticket, error) {
	if expectedVersion == 0 {
		return nil, ErrVersionConflict
	}
	ticket, err := s.GetTicket(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if ticket.Version != expectedVersion {
		return nil, ErrVersionConflict
	}
	nextPriority := escalatedPriority(ticket.Priority)
	description := fmt.Sprintf("工单升级到用户 ID: %d", escalateToID)
	if reason != "" {
		description += fmt.Sprintf(" (原因: %s)", reason)
	}
	if comment != "" {
		description += fmt.Sprintf(" - %s", comment)
	}
	assignee := models.HumanActor(escalateToID)
	result, err := s.agentNative.EscalateTicket(ctx, EscalateTicketCommand{
		TicketID:        ticketID,
		ExpectedVersion: expectedVersion,
		Actor:           models.HumanActor(userID),
		Priority:        &nextPriority,
		Assignee:        &assignee,
		SourceProtocol:  "rest-human",
		Reason:          reason,
		Comment:         comment,
		historyRecords: []ticketHistorySpec{{
			Action:      models.HistoryActionEscalate,
			Description: description,
			FieldName:   "escalation",
			OldValue: fmt.Sprintf(
				"assigned_to: %s, priority: %s",
				getAssigneeValue(ticket.AssignedToID),
				ticket.Priority,
			),
			NewValue: fmt.Sprintf(
				"assigned_to: %d, priority: %s",
				escalateToID,
				nextPriority,
			),
			IsVisible:   true,
			IsImportant: true,
		}},
	})
	if err != nil {
		return nil, err
	}
	return s.GetTicket(ctx, result.Ticket.ID)
}

func (s *TicketService) UpdateTicketStatusExpectedVersion(
	ctx context.Context,
	ticketID uint,
	status string,
	userID uint,
	comment string,
	resolutionNotes string,
	expectedVersion uint64,
) (*models.Ticket, error) {
	if expectedVersion == 0 {
		return nil, ErrVersionConflict
	}
	ticket, err := s.GetTicket(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if ticket.Version != expectedVersion {
		return nil, ErrVersionConflict
	}
	nextStatus := models.TicketStatus(status)
	if !nextStatus.IsValid() {
		return nil, fmt.Errorf("%w: %s to %s", ErrInvalidTicketTransition, ticket.Status, nextStatus)
	}
	if ticket.Status == nextStatus {
		return ticket, nil
	}
	oldStatus := ticket.Status
	description := fmt.Sprintf(
		"状态从「%s」变更为「%s」",
		getStatusLabel(string(oldStatus)),
		getStatusLabel(status),
	)
	if comment != "" {
		description += fmt.Sprintf(" - %s", comment)
	}
	if resolutionNotes != "" && nextStatus == models.TicketStatusResolved {
		description += fmt.Sprintf(" (解决方案: %s)", resolutionNotes)
	}
	result, err := s.agentNative.TransitionTicket(ctx, TransitionTicketCommand{
		TicketID:        ticketID,
		ExpectedVersion: expectedVersion,
		Actor:           models.HumanActor(userID),
		Status:          nextStatus,
		SourceProtocol:  "rest-human",
		Reason:          comment,
		Comment:         comment,
		ResolutionNotes: resolutionNotes,
		historyRecords: []ticketHistorySpec{{
			Action:      models.HistoryActionStatusChange,
			Description: description,
			FieldName:   "status",
			OldValue:    string(oldStatus),
			NewValue:    string(nextStatus),
			IsVisible:   true,
			IsImportant: true,
		}},
	})
	if err != nil {
		return nil, err
	}
	return s.GetTicket(ctx, result.Ticket.ID)
}

// AllowedTicketTransitions projects the immutable workflow bound to a Ticket
// into the shared lifecycle statuses visible to Human REST. It is intentionally
// not a global status matrix.
func (s *TicketService) AllowedTicketTransitions(
	ctx context.Context,
	ticketID uint,
	userID uint,
) ([]models.TicketStatus, error) {
	if ticketID == 0 || userID == 0 {
		return nil, ErrInvalidTicketTransition
	}
	operation, err := commandOperationContext(
		ctx,
		models.HumanActor(userID),
	)
	if err != nil {
		return nil, err
	}
	var allowed []models.TicketStatus
	err = transactionForContext(ctx, s.db, func(tx *gorm.DB) error {
		var ticket models.Ticket
		if err := tx.WithContext(ctx).Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			ticketID,
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
		).First(&ticket).Error; err != nil {
			return err
		}
		for _, candidate := range []models.TicketStatus{
			models.TicketStatusOpen,
			models.TicketStatusInProgress,
			models.TicketStatusPending,
			models.TicketStatusResolved,
			models.TicketStatusClosed,
			models.TicketStatusCancelled,
		} {
			if candidate == ticket.Status {
				continue
			}
			if err := validateTicketWorkflowTransitionTx(
				ctx,
				tx,
				operation.Scope,
				&ticket,
				candidate,
				operation.Actor,
			); err == nil {
				allowed = append(allowed, candidate)
			}
		}
		return nil
	})
	return allowed, err
}

// GetTicketStatistics returns enhanced statistics for one authorized project.
func (s *TicketService) GetTicketStatistics(
	ctx context.Context,
	userID uint,
	role string,
) (*TicketStatisticsResponse, error) {
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return nil, err
	}
	cacheKey := ""
	if s.statsCache != nil && s.statsCacheTTL > 0 {
		cacheKey = fmt.Sprintf(
			"ticket_stats:%d:%s:%d",
			scope.ProjectID,
			role,
			userID,
		)
		if cached, err := s.statsCache.Get(ctx, cacheKey); err == nil && cached != "" {
			var cachedStats TicketStatisticsResponse
			if err := json.Unmarshal([]byte(cached), &cachedStats); err == nil {
				if cachedStats.ByPriority == nil {
					cachedStats.ByPriority = make(map[string]int64)
				}
				if cachedStats.ByCategory == nil {
					cachedStats.ByCategory = make(map[string]int64)
				}
				return &cachedStats, nil
			}
		}
	}

	stats := &TicketStatisticsResponse{
		ByPriority: make(map[string]int64),
		ByCategory: make(map[string]int64),
	}

	now := time.Now()
	query := scopedTicketQuery(
		s.db.WithContext(ctx).Model(&models.Ticket{}),
		scope,
	)
	query = scopeHumanTicketQuery(query, userID, role)

	var aggregated struct {
		Total        int64
		Open         int64
		InProgress   int64
		Pending      int64
		Resolved     int64
		Closed       int64
		Overdue      int64
		Unassigned   int64
		HighPriority int64
		SLABreached  int64
		Escalated    int64
	}

	if err := query.Select(`
		COUNT(*) AS total,
		SUM(CASE WHEN status = 'open' THEN 1 ELSE 0 END) AS open,
		SUM(CASE WHEN status = 'in_progress' THEN 1 ELSE 0 END) AS in_progress,
		SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END) AS pending,
		SUM(CASE WHEN status = 'resolved' THEN 1 ELSE 0 END) AS resolved,
		SUM(CASE WHEN status = 'closed' THEN 1 ELSE 0 END) AS closed,
		SUM(CASE WHEN due_date < ? AND status NOT IN ('resolved','closed') THEN 1 ELSE 0 END) AS overdue,
		SUM(CASE WHEN assigned_to_id IS NULL THEN 1 ELSE 0 END) AS unassigned,
		SUM(CASE WHEN priority IN ('high','urgent') THEN 1 ELSE 0 END) AS high_priority,
			SUM(CASE WHEN sla_breached = true AND status IN ('open','in_progress','pending') THEN 1 ELSE 0 END) AS sla_breached,
		SUM(CASE WHEN is_escalated = true THEN 1 ELSE 0 END) AS escalated
	`, now).Scan(&aggregated).Error; err != nil {
		return nil, fmt.Errorf("failed to aggregate ticket statistics: %w", err)
	}

	stats.Total = aggregated.Total
	stats.Open = aggregated.Open
	stats.InProgress = aggregated.InProgress
	stats.Pending = aggregated.Pending
	stats.Resolved = aggregated.Resolved
	stats.Closed = aggregated.Closed
	stats.Overdue = aggregated.Overdue
	stats.Unassigned = aggregated.Unassigned
	stats.HighPriority = aggregated.HighPriority
	stats.SLABreached = aggregated.SLABreached
	stats.Escalated = aggregated.Escalated
	switch strings.ToLower(strings.TrimSpace(role)) {
	case string(models.ProjectRoleAgent),
		string(models.ProjectRoleRequester):
		stats.MyTickets = stats.Total
	}

	priorityCounts := []struct {
		Priority string
		Count    int64
	}{}

	priorityQuery := scopedTicketQuery(
		s.db.WithContext(ctx).Model(&models.Ticket{}),
		scope,
	)
	priorityQuery = scopeHumanTicketQuery(priorityQuery, userID, role)

	if err := priorityQuery.
		Select("priority, count(*) as count").
		Group("priority").
		Find(&priorityCounts).Error; err == nil {
		for _, pc := range priorityCounts {
			stats.ByPriority[pc.Priority] = pc.Count
		}
	}

	if cacheKey != "" {
		if payload, err := json.Marshal(stats); err == nil {
			_ = s.statsCache.Set(ctx, cacheKey, string(payload), s.statsCacheTTL)
		}
	}

	return stats, nil
}

// GetUserTickets gets tickets assigned to a specific user in one project.
func (s *TicketService) GetUserTickets(
	ctx context.Context,
	userID uint,
	status string,
	priority string,
	limit int,
) ([]*models.Ticket, int64, error) {
	var tickets []*models.Ticket
	var total int64
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return nil, 0, err
	}

	query := scopedTicketQuery(
		s.db.WithContext(ctx).Model(&models.Ticket{}),
		scope,
	).Where("assigned_to_id = ?", userID)

	if status != "" {
		statuses := parseCommaSeparated(status)
		query = query.Where("status IN ?", statuses)
	}

	if priority != "" {
		priorities := parseCommaSeparated(priority)
		query = query.Where("priority IN ?", priorities)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count user tickets: %w", err)
	}

	query = query.Preload("CreatedBy").Preload("AssignedTo").Preload("Category")
	if limit > 0 {
		query = query.Limit(limit)
	}
	query = query.Order("created_at DESC")

	if err := query.Find(&tickets).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to get user tickets: %w", err)
	}

	return tickets, total, nil
}

// GetUnassignedTickets gets unassigned tickets in one project.
func (s *TicketService) GetUnassignedTickets(
	ctx context.Context,
	priority string,
	categoryID string,
	limit int,
) ([]*models.Ticket, int64, error) {
	var tickets []*models.Ticket
	var total int64
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return nil, 0, err
	}

	query := scopedTicketQuery(
		s.db.WithContext(ctx).Model(&models.Ticket{}),
		scope,
	).Where("assigned_to_id IS NULL")

	if priority != "" {
		priorities := parseCommaSeparated(priority)
		query = query.Where("priority IN ?", priorities)
	}

	if categoryID != "" {
		if catID, err := strconv.ParseUint(categoryID, 10, 32); err == nil {
			query = query.Where("category_id = ?", catID)
		}
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count unassigned tickets: %w", err)
	}

	query = query.Preload("CreatedBy").Preload("Category")
	if limit > 0 {
		query = query.Limit(limit)
	}
	query = query.Order("created_at DESC")

	if err := query.Find(&tickets).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to get unassigned tickets: %w", err)
	}

	return tickets, total, nil
}

// GetOverdueTickets gets overdue tickets in one project.
func (s *TicketService) GetOverdueTickets(
	ctx context.Context,
	userID uint,
	role string,
) ([]*models.Ticket, int64, error) {
	var tickets []*models.Ticket
	var total int64
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return nil, 0, err
	}

	now := time.Now()
	query := scopedTicketQuery(
		s.db.WithContext(ctx).Model(&models.Ticket{}),
		scope,
	).
		Where("due_date < ? AND status NOT IN (?, ?)", now, models.TicketStatusResolved, models.TicketStatusClosed)
	query = scopeHumanTicketQuery(query, userID, role)

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count overdue tickets: %w", err)
	}

	query = query.Preload("CreatedBy").Preload("AssignedTo").Preload("Category").
		Order("due_date ASC")

	if err := query.Find(&tickets).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to get overdue tickets: %w", err)
	}

	return tickets, total, nil
}

// GetSLABreachedTickets gets SLA breached tickets in one project.
func (s *TicketService) GetSLABreachedTickets(
	ctx context.Context,
	userID uint,
	role string,
) ([]*models.Ticket, int64, error) {
	var tickets []*models.Ticket
	var total int64
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return nil, 0, err
	}

	query := scopedTicketQuery(
		s.db.WithContext(ctx).Model(&models.Ticket{}),
		scope,
	).
		Where("tickets.sla_breached = ? AND tickets.status IN ?", true, slaActiveTicketStatuses())
	query = scopeHumanTicketQuery(query, userID, role)

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count SLA breached tickets: %w", err)
	}

	query = query.Preload("CreatedBy").Preload("AssignedTo").Preload("Category").
		Order("tickets.created_at ASC")

	if err := query.Find(&tickets).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to get SLA breached tickets: %w", err)
	}

	return tickets, total, nil
}

func scopeHumanTicketQuery(query *gorm.DB, userID uint, role string) *gorm.DB {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case string(models.ProjectRoleRequester):
		return query.Where("tickets.created_by_id = ?", userID)
	case string(models.ProjectRoleAgent):
		return query.Where("tickets.assigned_to_id = ?", userID)
	case string(models.ProjectRoleManager),
		string(models.ProjectRoleAdmin),
		string(models.ProjectRoleObserver):
		return query
	default:
		// Authentication currently rejects unknown roles. Keep the data layer
		// fail-closed as defense in depth for direct service callers.
		return query.Where("1 = 0")
	}
}

// GetTicketHistory gets the history for a project-scoped ticket.
func (s *TicketService) GetTicketHistory(
	ctx context.Context,
	ticketID uint,
) ([]*models.TicketHistory, int64, error) {
	var histories []*models.TicketHistory
	var total int64
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return nil, 0, err
	}
	var ticket models.Ticket
	if err := scopedTicketQuery(
		s.db.WithContext(ctx).Model(&models.Ticket{}),
		scope,
	).Select("tickets.id").Where("tickets.id = ?", ticketID).
		First(&ticket).Error; err != nil {
		return nil, 0, err
	}

	query := s.db.WithContext(ctx).
		Model(&models.TicketHistory{}).
		Where(
			"ticket_id = ? AND organization_id = ? AND project_id = ?",
			ticketID,
			scope.OrganizationID,
			scope.ProjectID,
		)

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count ticket history: %w", err)
	}

	query = query.Preload("User").Order("created_at DESC")

	if err := query.Find(&histories).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to get ticket history: %w", err)
	}

	return histories, total, nil
}

// Helper functions for workflow operations
func getAssigneeValue(assigneeID *uint) string {
	if assigneeID == nil {
		return "未分配"
	}
	return fmt.Sprintf("%d", *assigneeID)
}

func parseCommaSeparated(value string) []string {
	if value == "" {
		return []string{}
	}
	parts := make([]string, 0)
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return parts
}

// DeleteTicketExpectedVersion deletes a ticket only when the caller's list or
// detail snapshot still matches the current resource version.
func (s *TicketService) DeleteTicketExpectedVersion(
	ctx context.Context,
	id uint,
	userID uint,
	userRole string,
	expectedVersion uint64,
) error {
	if expectedVersion == 0 {
		return ErrVersionConflict
	}
	ticket, err := s.GetTicket(ctx, id)
	if err != nil {
		return err
	}
	if ticket.Version != expectedVersion {
		return ErrVersionConflict
	}

	// Creators and queue administrators may delete. Object authorization in the
	// handler applies the same supervisor/admin visibility rule.
	if ticket.CreatedByID == nil || *ticket.CreatedByID != userID {
		if !isElevatedRole(userRole) {
			return fmt.Errorf("permission denied")
		}
	}

	_, err = s.agentNative.DeleteTicket(ctx, DeleteTicketCommand{
		TicketID:        id,
		ExpectedVersion: expectedVersion,
		Actor:           models.HumanActor(userID),
		SourceProtocol:  "rest-human",
	})
	return err
}

func isElevatedRole(role string) bool {
	switch models.ProjectRole(strings.ToLower(strings.TrimSpace(role))) {
	case models.ProjectRoleAdmin, models.ProjectRoleManager:
		return true
	default:
		return false
	}
}

// BulkUpdateTickets updates multiple version-bound tickets atomically.
func (s *TicketService) BulkUpdateTickets(
	ctx context.Context,
	req *BulkUpdateRequest,
	userID uint,
) (*BulkUpdateResult, error) {
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, ErrInvalidBulkTicketUpdate
	}
	preconditions, err := normalizedBulkTicketPreconditions(req.Tickets)
	if err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(preconditions))
	for _, precondition := range preconditions {
		ids = append(ids, precondition.ID)
	}
	var scopedCount int64
	if err := scopedTicketQuery(
		s.db.WithContext(ctx).Model(&models.Ticket{}),
		scope,
	).Where("tickets.id IN ?", ids).Count(&scopedCount).Error; err != nil {
		return nil, err
	}
	if scopedCount != int64(len(ids)) {
		return nil, ErrProjectAccessDenied
	}
	return s.agentNative.BulkUpdateHumanTickets(ctx, req, userID)
}

func scopedTicketQuery(
	query *gorm.DB,
	scope models.ProjectScope,
) *gorm.DB {
	if query == nil {
		return nil
	}
	if scope.Validate() != nil {
		return query.Where("1 = 0")
	}
	return query.Where(
		"tickets.organization_id = ? AND tickets.project_id = ?",
		scope.OrganizationID,
		scope.ProjectID,
	)
}

func bulkTicketEventType(changedFields []string) string {
	for _, field := range changedFields {
		if field == "status" {
			return eventcontract.TicketTransitionedEventType
		}
	}
	for _, field := range changedFields {
		if field == "assigned_to_id" {
			return eventcontract.TicketAssignedEventType
		}
	}
	return eventcontract.TicketUpdatedEventType
}

func normalizedBulkTicketPreconditions(
	input []TicketVersionPrecondition,
) ([]TicketVersionPrecondition, error) {
	preconditions := append([]TicketVersionPrecondition(nil), input...)
	seen := make(map[uint]struct{}, len(preconditions))
	for _, precondition := range preconditions {
		if precondition.ID == 0 {
			return nil, fmt.Errorf(
				"%w: ticket IDs must be positive",
				ErrInvalidBulkTicketUpdate,
			)
		}
		if precondition.Version == 0 {
			return nil, fmt.Errorf(
				"%w: ticket %d version must be positive",
				ErrInvalidBulkTicketUpdate,
				precondition.ID,
			)
		}
		if _, exists := seen[precondition.ID]; exists {
			return nil, fmt.Errorf(
				"%w: duplicate ticket ID %d",
				ErrInvalidBulkTicketUpdate,
				precondition.ID,
			)
		}
		seen[precondition.ID] = struct{}{}
	}
	sort.Slice(preconditions, func(left, right int) bool {
		return preconditions[left].ID < preconditions[right].ID
	})
	return preconditions, nil
}

func bulkTicketChanges(req *BulkUpdateRequest) (map[string]any, []string, error) {
	raw := make(map[string]any)
	if req.Status != nil {
		status := models.TicketStatus(strings.TrimSpace(*req.Status))
		if !status.IsValid() {
			return nil, nil, fmt.Errorf(
				"%w: invalid ticket status %q",
				ErrInvalidBulkTicketUpdate,
				*req.Status,
			)
		}
		raw["status"] = status
	}
	if req.Priority != nil {
		priority := models.TicketPriority(strings.TrimSpace(*req.Priority))
		if !priority.IsValid() {
			return nil, nil, fmt.Errorf(
				"%w: invalid ticket priority %q",
				ErrInvalidBulkTicketUpdate,
				*req.Priority,
			)
		}
		raw["priority"] = priority
	}
	if req.AssignedToID != nil {
		if *req.AssignedToID == 0 {
			return nil, nil, fmt.Errorf(
				"%w: assigned_to_id must be positive",
				ErrInvalidBulkTicketUpdate,
			)
		}
		raw["assigned_to_id"] = *req.AssignedToID
		raw["assigned_to_actor_type"] = models.ActorTypeHuman
		raw["assigned_to_actor_id"] = models.HumanActor(*req.AssignedToID).ID
		raw["assigned_to_service_principal_id"] = nil
	}
	if req.Tags != nil {
		raw["tags"] = append([]string(nil), req.Tags...)
	}
	if req.CustomFields != nil {
		raw["custom_fields"] = req.CustomFields
	}
	if len(raw) == 0 {
		return nil, nil, fmt.Errorf(
			"%w: no ticket changes provided",
			ErrInvalidBulkTicketUpdate,
		)
	}
	changes, fields, err := sanitizeTicketChanges(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidBulkTicketUpdate, err)
	}
	return changes, fields, nil
}

func cloneBulkTicketChanges(input map[string]any) map[string]any {
	result := make(map[string]any, len(input)+4)
	for field, value := range input {
		result[field] = value
	}
	return result
}

func bulkHistoryFieldName(fields []string) string {
	if len(fields) == 1 {
		return fields[0]
	}
	return "bulk_update"
}

func ticketFieldValueBeforeChange(changeSet map[string]any, field string, side string) any {
	change, ok := changeSet[field].(map[string]any)
	if !ok {
		return nil
	}
	return change[side]
}

func bulkAuditValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

// truncateString truncates a string to the specified length
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func applyTicketStatusTimestamps(ticket *models.Ticket, status models.TicketStatus, now time.Time) {
	switch status {
	case models.TicketStatusResolved:
		if ticket.ResolvedAt == nil {
			ticket.ResolvedAt = &now
		}
	case models.TicketStatusClosed:
		if ticket.ClosedAt == nil {
			ticket.ClosedAt = &now
		}
	case models.TicketStatusOpen,
		models.TicketStatusInProgress,
		models.TicketStatusPending:
		ticket.ResolvedAt = nil
		ticket.ClosedAt = nil
	}
}

// getStatusLabel returns Chinese label for status
func getStatusLabel(status string) string {
	labels := map[string]string{
		"open":        "待处理",
		"in_progress": "处理中",
		"pending":     "等待中",
		"resolved":    "已解决",
		"closed":      "已关闭",
		"cancelled":   "已取消",
	}
	if label, exists := labels[status]; exists {
		return label
	}
	return status
}

// getPriorityLabel returns Chinese label for priority
func getPriorityLabel(priority string) string {
	labels := map[string]string{
		"low":      "低",
		"normal":   "普通",
		"medium":   "中等",
		"high":     "高",
		"urgent":   "紧急",
		"critical": "严重",
	}
	if label, exists := labels[priority]; exists {
		return label
	}
	return priority
}

// getSourceLabel returns Chinese label for source
func getSourceLabel(source string) string {
	labels := map[string]string{
		"web":    "网页",
		"email":  "邮件",
		"phone":  "电话",
		"chat":   "聊天",
		"api":    "API",
		"mobile": "移动端",
		"agent":  "AI Agent",
	}
	if label, exists := labels[source]; exists {
		return label
	}
	return source
}
