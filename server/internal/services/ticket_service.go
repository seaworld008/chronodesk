package services

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

var (
	ErrInvalidTicketTransition = errors.New("invalid ticket status transition")
	ErrInvalidBulkTicketUpdate = errors.New("invalid bulk ticket update")
)

// TicketServiceInterface defines the interface for ticket service
type TicketServiceInterface interface {
	GetTickets(ctx context.Context, filters TicketFilters) ([]*models.Ticket, int64, error)
	GetTicket(ctx context.Context, id uint) (*models.Ticket, error)
	CreateTicket(ctx context.Context, req *models.TicketCreateRequest, userID uint) (*models.Ticket, error)
	UpdateTicket(ctx context.Context, id uint, req *models.TicketUpdateRequest, userID uint) (*models.Ticket, error)
	DeleteTicket(ctx context.Context, id uint, userID uint, userRole string) error
	AssignTicket(ticketID uint, assigneeID uint, userID uint, comment string) (*models.Ticket, error)
	TransferTicket(ticketID uint, assigneeID uint, userID uint, comment string, transferReason string) (*models.Ticket, error)
	EscalateTicket(ticketID uint, escalateToID uint, userID uint, reason string, comment string) (*models.Ticket, error)
	UpdateTicketStatus(ticketID uint, status string, userID uint, comment string, resolutionNotes string) (*models.Ticket, error)
	GetTicketStatistics(userID uint, role string) (*TicketStatisticsResponse, error)
	GetUserTickets(userID uint, status string, priority string, limit int) ([]*models.Ticket, int64, error)
	GetUnassignedTickets(priority string, categoryID string, limit int) ([]*models.Ticket, int64, error)
	GetOverdueTickets(userID uint, role string) ([]*models.Ticket, int64, error)
	GetSLABreachedTickets(userID uint, role string) ([]*models.Ticket, int64, error)
	BulkAssignTickets(ticketIDs []uint, assigneeID uint, userID uint, comment string) (*BulkOperationResult, error)
	BulkUpdateStatus(ticketIDs []uint, status string, userID uint, comment string) (*BulkOperationResult, error)
	GetTicketStats(ctx context.Context, userID uint) (*TicketStats, error)
	BulkUpdateTickets(ctx context.Context, req *BulkUpdateRequest, userID uint) error
	GetTicketHistory(ticketID uint) ([]*models.TicketHistory, int64, error)
	CreateTicketHistory(ctx context.Context, req *models.TicketHistoryCreateRequest, userID *uint) error
}

// TicketService implements TicketServiceInterface
type TicketService struct {
	db                *gorm.DB
	automationService *AutomationService
	agentNative       *AgentNativeService
	statsCache        StatsCache
	statsCacheTTL     time.Duration
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

// NewTicketService creates a new ticket service
func NewTicketService(db *gorm.DB) TicketServiceInterface {
	return &TicketService{
		db:                db,
		automationService: NewAutomationService(db),
	}
}

// NewTicketServiceWithCache creates a new ticket service with stats cache enabled.
func NewTicketServiceWithCache(db *gorm.DB, cache StatsCache, ttl time.Duration) TicketServiceInterface {
	service := &TicketService{
		db:                db,
		automationService: NewAutomationService(db),
	}
	if cache != nil && ttl > 0 {
		service.statsCache = cache
		service.statsCacheTTL = ttl
	}
	return service
}

// NewTicketServiceWithAgentNative keeps the compatibility API while making
// human ticket lifecycle writes emit the same transactional CloudEvents and
// Outbox records as REST v1, MCP and A2A.
func NewTicketServiceWithAgentNative(
	db *gorm.DB,
	cache StatsCache,
	ttl time.Duration,
	native *AgentNativeService,
) TicketServiceInterface {
	service := &TicketService{
		db:          db,
		agentNative: native,
	}
	if cache != nil && ttl > 0 {
		service.statsCache = cache
		service.statsCacheTTL = ttl
	}
	return service
}

// TicketFilters represents filters for ticket queries
type TicketFilters struct {
	Status      string
	Priority    string
	Type        string
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

// TicketStats represents ticket statistics
type TicketStats struct {
	Total      int64 `json:"total"`
	Open       int64 `json:"open"`
	InProgress int64 `json:"in_progress"`
	Resolved   int64 `json:"resolved"`
	Closed     int64 `json:"closed"`
	Overdue    int64 `json:"overdue"`
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

// BulkOperationResult represents the result of a bulk operation
type BulkOperationResult struct {
	AssignedCount   int    `json:"assigned_count,omitempty"`
	UpdatedCount    int    `json:"updated_count,omitempty"`
	FailedCount     int    `json:"failed_count"`
	AssignedTickets []uint `json:"assigned_tickets,omitempty"`
	UpdatedTickets  []uint `json:"updated_tickets,omitempty"`
	FailedTickets   []uint `json:"failed_tickets"`
}

// BulkUpdateRequest represents bulk update request
type BulkUpdateRequest struct {
	TicketIDs    []uint                 `json:"ticket_ids"`
	Status       *string                `json:"status,omitempty"`
	Priority     *string                `json:"priority,omitempty"`
	AssignedToID *uint                  `json:"assigned_to_id,omitempty"`
	Tags         []string               `json:"tags,omitempty"`
	CustomFields map[string]interface{} `json:"custom_fields,omitempty"`
}

// GetTickets retrieves tickets with filters
func (s *TicketService) GetTickets(ctx context.Context, filters TicketFilters) ([]*models.Ticket, int64, error) {
	var tickets []*models.Ticket
	var total int64

	query := s.db.WithContext(ctx).Model(&models.Ticket{})

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
		query = query.Where("sla_breached = ?", *filters.SLABreached)
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

	err := s.db.WithContext(ctx).
		Preload("CreatedBy").
		Preload("AssignedTo").
		Preload("Comments").
		Preload("Comments.User").
		First(&ticket, id).Error

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
	// Generate unique ticket number
	ticketNumber := s.generateTicketNumber()

	status := models.TicketStatusOpen
	if req.Status != nil {
		status = models.TicketStatus(*req.Status)
	}

	now := time.Now()

	ticket := &models.Ticket{
		TicketNumber:       ticketNumber,
		Title:              req.Title,
		Description:        req.Description,
		Status:             status,
		Priority:           req.Priority,
		Type:               req.Type,
		Source:             req.Source,
		CreatedByID:        userID,
		CreatedByActorType: models.ActorTypeHuman,
		CreatedByActorID:   strconv.FormatUint(uint64(userID), 10),
		Version:            1,
		TrustLevel:         models.TicketTrustLevelUntrusted,
		Tags:               datatypes.JSONSlice[string](req.Tags), // 转换类型，GORM自动处理JSONB序列化
		CustomerEmail:      req.CustomerEmail,
		CustomerPhone:      req.CustomerPhone,
		CustomerName:       req.CustomerName,
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	// 设置 CustomFields
	if req.CustomFields != nil {
		ticket.CustomFields = datatypes.NewJSONType(req.CustomFields.ToMap())
	}
	if req.AgentContext != nil {
		ticket.AgentContext = datatypes.NewJSONType(*req.AgentContext)
	}

	if status == models.TicketStatusResolved && ticket.ResolvedAt == nil {
		ticket.ResolvedAt = &now
	}
	if status == models.TicketStatusClosed && ticket.ClosedAt == nil {
		ticket.ClosedAt = &now
	}

	// Set assignee if provided
	if req.AssignedToID != nil {
		ticket.AssignedToID = req.AssignedToID
		ticket.AssignedToActorType = models.ActorTypeHuman
		ticket.AssignedToActorID = strconv.FormatUint(uint64(*req.AssignedToID), 10)
	}

	// Set category if provided
	if req.CategoryID != nil {
		ticket.CategoryID = req.CategoryID
		var category models.Category
		if err := s.db.WithContext(ctx).Select("id", "sla_hours").First(&category, *req.CategoryID).Error; err != nil {
			return nil, fmt.Errorf("failed to load ticket category: %w", err)
		}
		if category.SLAHours != nil && *category.SLAHours > 0 {
			slaDueDate := now.Add(time.Duration(*category.SLAHours) * time.Hour)
			ticket.SLADueDate = &slaDueDate
		}
	}

	// Set subcategory if provided
	if req.SubcategoryID != nil {
		ticket.SubcategoryID = req.SubcategoryID
	}

	// Set due date if provided
	if req.DueDate != nil {
		ticket.DueDate = req.DueDate
	}

	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(ticket).Error; err != nil {
			return fmt.Errorf("failed to create ticket: %w", err)
		}
		return s.appendHumanTicketEventTx(
			ctx,
			tx,
			"io.chronodesk.ticket.created.v1",
			ticket,
			userID,
			map[string]any{
				"ticket_id":         ticket.ID,
				"source":            ticket.Source,
				"content_untrusted": true,
			},
		)
	}); err != nil {
		return nil, err
	}

	// Reload with associations and run event-driven rules. Automation errors do
	// not roll back the already committed user operation; execution logs retain
	// the failure for operators.
	created, err := s.GetTicket(ctx, ticket.ID)
	if err != nil {
		return nil, err
	}
	s.executeAutomation(ctx, "ticket.created", created)
	return s.GetTicket(ctx, ticket.ID)
}

// UpdateTicket updates an existing ticket
func (s *TicketService) UpdateTicket(ctx context.Context, id uint, req *models.TicketUpdateRequest, userID uint) (*models.Ticket, error) {
	return s.updateTicket(ctx, id, req, userID, 0)
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
	return s.updateTicket(ctx, id, req, userID, expectedVersion)
}

func (s *TicketService) updateTicket(
	ctx context.Context,
	id uint,
	req *models.TicketUpdateRequest,
	userID uint,
	authorizedVersion uint64,
) (*models.Ticket, error) {
	// 获取原工单信息用于比较
	originalTicket, err := s.GetTicket(ctx, id)
	if err != nil {
		return nil, err
	}
	if authorizedVersion != 0 && originalTicket.Version != authorizedVersion {
		return nil, ErrVersionConflict
	}

	// 创建副本用于更新
	ticket := *originalTicket
	var historyRecords []*models.TicketHistoryCreateRequest

	// Update fields and track changes
	if req.Title != nil && *req.Title != ticket.Title {
		historyRecords = append(historyRecords, &models.TicketHistoryCreateRequest{
			TicketID:    id,
			Action:      models.HistoryActionUpdate,
			Description: "标题已更新",
			FieldName:   "title",
			OldValue:    ticket.Title,
			NewValue:    *req.Title,
		})
		ticket.Title = *req.Title
	}

	if req.Description != nil && *req.Description != ticket.Description {
		historyRecords = append(historyRecords, &models.TicketHistoryCreateRequest{
			TicketID:    id,
			Action:      models.HistoryActionUpdate,
			Description: "描述已更新",
			FieldName:   "description",
			OldValue:    truncateString(ticket.Description, 50),
			NewValue:    truncateString(*req.Description, 50),
		})
		ticket.Description = *req.Description
	}

	if req.Status != nil && models.TicketStatus(*req.Status) != ticket.Status {
		if !ticket.Status.CanTransitionTo(*req.Status) {
			return nil, fmt.Errorf("%w: %s to %s", ErrInvalidTicketTransition, ticket.Status, *req.Status)
		}
		oldStatus := string(ticket.Status)
		newStatus := string(*req.Status)
		historyRecords = append(historyRecords, &models.TicketHistoryCreateRequest{
			TicketID:    id,
			Action:      models.HistoryActionStatusChange,
			Description: fmt.Sprintf("状态从「%s」变更为「%s」", getStatusLabel(oldStatus), getStatusLabel(newStatus)),
			FieldName:   "status",
			OldValue:    oldStatus,
			NewValue:    newStatus,
			IsImportant: getBoolPtr(true),
		})
		ticket.Status = models.TicketStatus(*req.Status)

		applyTicketStatusTimestamps(&ticket, *req.Status, time.Now())
	}

	if req.Priority != nil && models.TicketPriority(*req.Priority) != ticket.Priority {
		oldPriority := string(ticket.Priority)
		newPriority := string(*req.Priority)
		historyRecords = append(historyRecords, &models.TicketHistoryCreateRequest{
			TicketID:    id,
			Action:      models.HistoryActionPriorityChange,
			Description: fmt.Sprintf("优先级从「%s」变更为「%s」", getPriorityLabel(oldPriority), getPriorityLabel(newPriority)),
			FieldName:   "priority",
			OldValue:    oldPriority,
			NewValue:    newPriority,
			IsImportant: getBoolPtr(true),
		})
		ticket.Priority = models.TicketPriority(*req.Priority)
	}

	if req.Type != nil && models.TicketType(*req.Type) != ticket.Type {
		oldType := string(ticket.Type)
		newType := string(*req.Type)
		historyRecords = append(historyRecords, &models.TicketHistoryCreateRequest{
			TicketID:    id,
			Action:      models.HistoryActionUpdate,
			Description: fmt.Sprintf("类型从「%s」变更为「%s」", oldType, newType),
			FieldName:   "type",
			OldValue:    oldType,
			NewValue:    newType,
		})
		ticket.Type = models.TicketType(*req.Type)
	}

	if req.Source != nil && models.TicketSource(*req.Source) != ticket.Source {
		oldSource := string(ticket.Source)
		newSource := string(*req.Source)
		historyRecords = append(historyRecords, &models.TicketHistoryCreateRequest{
			TicketID:    id,
			Action:      models.HistoryActionUpdate,
			Description: fmt.Sprintf("来源从「%s」变更为「%s」", getSourceLabel(oldSource), getSourceLabel(newSource)),
			FieldName:   "source",
			OldValue:    oldSource,
			NewValue:    newSource,
		})
		ticket.Source = models.TicketSource(*req.Source)
	}

	// 处理分配变更
	if req.AssignedToID != nil {
		oldAssigneeID := ticket.AssignedToID
		newAssigneeID := req.AssignedToID

		// 分配逻辑
		if oldAssigneeID == nil && newAssigneeID != nil {
			// 新分配
			historyRecords = append(historyRecords, &models.TicketHistoryCreateRequest{
				TicketID:    id,
				Action:      models.HistoryActionAssign,
				Description: fmt.Sprintf("工单已分配给用户 ID: %d", *newAssigneeID),
				FieldName:   "assigned_to_id",
				OldValue:    "未分配",
				NewValue:    fmt.Sprintf("%d", *newAssigneeID),
				IsImportant: getBoolPtr(true),
			})
		} else if oldAssigneeID != nil && newAssigneeID != nil && *oldAssigneeID != *newAssigneeID {
			// 重新分配
			historyRecords = append(historyRecords, &models.TicketHistoryCreateRequest{
				TicketID:    id,
				Action:      models.HistoryActionAssign,
				Description: fmt.Sprintf("工单从用户 ID: %d 重新分配给用户 ID: %d", *oldAssigneeID, *newAssigneeID),
				FieldName:   "assigned_to_id",
				OldValue:    fmt.Sprintf("%d", *oldAssigneeID),
				NewValue:    fmt.Sprintf("%d", *newAssigneeID),
				IsImportant: getBoolPtr(true),
			})
		} else if oldAssigneeID != nil && newAssigneeID == nil {
			// 取消分配
			historyRecords = append(historyRecords, &models.TicketHistoryCreateRequest{
				TicketID:    id,
				Action:      models.HistoryActionUnassign,
				Description: fmt.Sprintf("取消分配给用户 ID: %d", *oldAssigneeID),
				FieldName:   "assigned_to_id",
				OldValue:    fmt.Sprintf("%d", *oldAssigneeID),
				NewValue:    "未分配",
				IsImportant: getBoolPtr(true),
			})
		}
		ticket.AssignedToID = req.AssignedToID
	}

	if req.DueDate != nil {
		ticket.DueDate = req.DueDate
	}
	if req.Tags != nil {
		ticket.Tags = datatypes.JSONSlice[string](req.Tags) // 转换类型
	}
	if req.CustomFields != nil {
		ticket.CustomFields = datatypes.NewJSONType(req.CustomFields.ToMap())
	}
	if req.AgentContext != nil {
		ticket.AgentContext = datatypes.NewJSONType(*req.AgentContext)
	}

	ticket.UpdatedAt = time.Now()
	ticket.Version++

	statusChanged := req.Status != nil &&
		models.TicketStatus(*req.Status) != originalTicket.Status
	assignmentChanged := req.AssignedToID != nil &&
		(originalTicket.AssignedToID == nil ||
			*originalTicket.AssignedToID != *req.AssignedToID)
	notificationTargets := make([]OutboxTarget, 0, 3)
	if statusChanged {
		notificationTargets = append(
			notificationTargets,
			TicketStatusNotificationOutboxTargets(&ticket, userID)...,
		)
	}
	if assignmentChanged {
		notificationTargets = append(
			notificationTargets,
			TicketAssignedNotificationOutboxTargets(&ticket, userID)...,
		)
	}

	// 在事务中保存工单和历史记录
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 保存工单更新
		if err := saveTicketCAS(tx, &ticket, originalTicket.Version); err != nil {
			return fmt.Errorf("failed to update ticket: %w", err)
		}

		// 创建历史记录
		for _, historyReq := range historyRecords {
			history := &models.TicketHistory{
				TicketID:    historyReq.TicketID,
				UserID:      &userID,
				ActorType:   models.ActorTypeHuman,
				ActorID:     strconv.FormatUint(uint64(userID), 10),
				Action:      historyReq.Action,
				Description: historyReq.Description,
				FieldName:   historyReq.FieldName,
				OldValue:    historyReq.OldValue,
				NewValue:    historyReq.NewValue,
				IsVisible:   true,
				IsSystem:    false,
				IsAutomated: false,
				IsImportant: historyReq.IsImportant != nil && *historyReq.IsImportant,
			}

			if err := tx.Create(history).Error; err != nil {
				return fmt.Errorf("failed to create history record: %w", err)
			}
		}

		changedFields := make([]string, 0, len(historyRecords))
		for _, historyReq := range historyRecords {
			if historyReq.FieldName != "" {
				changedFields = append(changedFields, historyReq.FieldName)
			}
		}
		eventData := map[string]any{
			"ticket_id":      ticket.ID,
			"changed_fields": changedFields,
		}
		if len(notificationTargets) > 0 {
			addTicketNotificationEventSnapshot(eventData, &ticket)
			if statusChanged {
				eventData["old_status"] = originalTicket.Status
				eventData["new_status"] = ticket.Status
			}
			if assignmentChanged && ticket.AssignedToID != nil {
				eventData["assigned_to_id"] = *ticket.AssignedToID
			}
		}
		return s.appendHumanTicketEventTx(
			ctx,
			tx,
			"io.chronodesk.ticket.updated.v1",
			&ticket,
			userID,
			eventData,
			notificationTargets...,
		)
	})

	if err != nil {
		return nil, err
	}
	s.executeAutomation(ctx, "ticket.updated", &ticket)

	return &ticket, nil
}

// AssignTicket assigns a ticket to a user with workflow support
func (s *TicketService) AssignTicket(ticketID uint, assigneeID uint, userID uint, comment string) (*models.Ticket, error) {
	return s.assignTicket(ticketID, assigneeID, userID, comment, 0)
}

func (s *TicketService) AssignTicketExpectedVersion(
	ticketID uint,
	assigneeID uint,
	userID uint,
	comment string,
	expectedVersion uint64,
) (*models.Ticket, error) {
	if expectedVersion == 0 {
		return nil, ErrVersionConflict
	}
	return s.assignTicket(ticketID, assigneeID, userID, comment, expectedVersion)
}

func (s *TicketService) assignTicket(
	ticketID uint,
	assigneeID uint,
	userID uint,
	comment string,
	authorizedVersion uint64,
) (*models.Ticket, error) {
	ticket, err := s.GetTicket(context.Background(), ticketID)
	if err != nil {
		return nil, err
	}
	if authorizedVersion != 0 && ticket.Version != authorizedVersion {
		return nil, ErrVersionConflict
	}

	oldAssigneeID := ticket.AssignedToID
	expectedVersion := ticket.Version
	ticket.AssignedToID = &assigneeID
	ticket.AssignedToActorType = models.ActorTypeHuman
	ticket.AssignedToActorID = strconv.FormatUint(uint64(assigneeID), 10)
	ticket.UpdatedAt = time.Now()
	ticket.Version++
	notificationTargets := TicketAssignedNotificationOutboxTargets(ticket, userID)

	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := saveTicketCAS(tx, ticket, expectedVersion); err != nil {
			return fmt.Errorf("failed to assign ticket: %w", err)
		}

		historyReq := &models.TicketHistoryCreateRequest{
			TicketID:    ticketID,
			Action:      models.HistoryActionAssign,
			Description: fmt.Sprintf("工单分配给用户 ID: %d", assigneeID),
			FieldName:   "assigned_to_id",
			OldValue:    getAssigneeValue(oldAssigneeID),
			NewValue:    fmt.Sprintf("%d", assigneeID),
			IsImportant: getBoolPtr(true),
		}

		if comment != "" {
			historyReq.Description += fmt.Sprintf(" - %s", comment)
		}

		history := &models.TicketHistory{
			TicketID:    historyReq.TicketID,
			UserID:      &userID,
			ActorType:   models.ActorTypeHuman,
			ActorID:     strconv.FormatUint(uint64(userID), 10),
			Action:      historyReq.Action,
			Description: historyReq.Description,
			FieldName:   historyReq.FieldName,
			OldValue:    historyReq.OldValue,
			NewValue:    historyReq.NewValue,
			IsVisible:   true,
			IsSystem:    false,
			IsAutomated: false,
			IsImportant: true,
		}

		if err := tx.Create(history).Error; err != nil {
			return err
		}
		eventData := map[string]any{
			"ticket_id":      ticket.ID,
			"assigned_to_id": assigneeID,
		}
		if len(notificationTargets) > 0 {
			addTicketNotificationEventSnapshot(eventData, ticket)
		}
		return s.appendHumanTicketEventTx(
			context.Background(),
			tx,
			"io.chronodesk.ticket.assigned.v1",
			ticket,
			userID,
			eventData,
			notificationTargets...,
		)
	})

	if err != nil {
		return nil, err
	}

	s.executeAutomation(context.Background(), "ticket.updated", ticket)

	return ticket, nil
}

func (s *TicketService) executeAutomation(ctx context.Context, event string, ticket *models.Ticket) {
	if s.automationService == nil || ticket == nil {
		return
	}
	if err := s.automationService.ExecuteRules(ctx, event, ticket); err != nil {
		log.Printf("工单自动化事件执行失败：event=%s ticket_id=%d error=%v", event, ticket.ID, err)
	}
}

func addTicketNotificationEventSnapshot(data map[string]any, ticket *models.Ticket) {
	if data == nil || ticket == nil {
		return
	}
	data["ticket_number"] = ticket.TicketNumber
	data["ticket_title"] = ticket.Title
	data["ticket_priority"] = ticket.Priority
}

func (s *TicketService) appendHumanTicketEventTx(
	ctx context.Context,
	tx *gorm.DB,
	eventType string,
	ticket *models.Ticket,
	userID uint,
	data map[string]any,
	additionalTargets ...OutboxTarget,
) error {
	if s.agentNative == nil {
		return nil
	}
	if data == nil {
		data = make(map[string]any)
	}
	if _, exists := data["ticket_id"]; !exists {
		data["ticket_id"] = ticket.ID
	}
	_, err := s.agentNative.AppendDomainEventWithAdditionalTargetsTx(
		ctx,
		tx,
		DomainEventInput{
			Type:            eventType,
			Subject:         fmt.Sprintf("ticket/%d", ticket.ID),
			Actor:           models.HumanActor(userID),
			ResourceVersion: ticket.Version,
			Data:            data,
		},
		additionalTargets,
	)
	return err
}

// saveTicketCAS prevents compatibility human routes from overwriting a
// concurrent Agent or human change. It deliberately updates zero-valued
// fields while preserving immutable row identity and creation metadata.
func saveTicketCAS(tx *gorm.DB, ticket *models.Ticket, expectedVersion uint64) error {
	if tx == nil || ticket == nil || ticket.ID == 0 || expectedVersion == 0 {
		return ErrVersionConflict
	}
	result := tx.Model(&models.Ticket{}).
		Where("id = ? AND version = ?", ticket.ID, expectedVersion).
		Select("*").
		Omit("id", "created_at", "deleted_at").
		Updates(ticket)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrVersionConflict
	}
	return nil
}

// TransferTicket transfers a ticket to another user
func (s *TicketService) TransferTicket(ticketID uint, assigneeID uint, userID uint, comment string, transferReason string) (*models.Ticket, error) {
	return s.transferTicket(ticketID, assigneeID, userID, comment, transferReason, 0)
}

func (s *TicketService) TransferTicketExpectedVersion(
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
	return s.transferTicket(ticketID, assigneeID, userID, comment, transferReason, expectedVersion)
}

func (s *TicketService) transferTicket(
	ticketID uint,
	assigneeID uint,
	userID uint,
	comment string,
	transferReason string,
	authorizedVersion uint64,
) (*models.Ticket, error) {
	ticket, err := s.GetTicket(context.Background(), ticketID)
	if err != nil {
		return nil, err
	}
	if authorizedVersion != 0 && ticket.Version != authorizedVersion {
		return nil, ErrVersionConflict
	}

	oldAssigneeID := ticket.AssignedToID
	expectedVersion := ticket.Version
	ticket.AssignedToID = &assigneeID
	ticket.AssignedToActorType = models.ActorTypeHuman
	ticket.AssignedToActorID = strconv.FormatUint(uint64(assigneeID), 10)
	ticket.UpdatedAt = time.Now()
	ticket.Version++

	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := saveTicketCAS(tx, ticket, expectedVersion); err != nil {
			return fmt.Errorf("failed to transfer ticket: %w", err)
		}

		description := fmt.Sprintf("工单从用户 ID: %s 转移给用户 ID: %d", getAssigneeValue(oldAssigneeID), assigneeID)
		if transferReason != "" {
			description += fmt.Sprintf(" (原因: %s)", transferReason)
		}
		if comment != "" {
			description += fmt.Sprintf(" - %s", comment)
		}

		history := &models.TicketHistory{
			TicketID:    ticketID,
			UserID:      &userID,
			ActorType:   models.ActorTypeHuman,
			ActorID:     strconv.FormatUint(uint64(userID), 10),
			Action:      models.HistoryActionTransfer,
			Description: description,
			FieldName:   "assigned_to_id",
			OldValue:    getAssigneeValue(oldAssigneeID),
			NewValue:    fmt.Sprintf("%d", assigneeID),
			IsVisible:   true,
			IsSystem:    false,
			IsAutomated: false,
			IsImportant: true,
		}

		if err := tx.Create(history).Error; err != nil {
			return err
		}
		return s.appendHumanTicketEventTx(
			context.Background(),
			tx,
			"io.chronodesk.ticket.assigned.v1",
			ticket,
			userID,
			map[string]any{
				"ticket_id":      ticket.ID,
				"assigned_to_id": assigneeID,
				"transfer":       true,
			},
		)
	})

	if err != nil {
		return nil, err
	}

	s.executeAutomation(context.Background(), "ticket.updated", ticket)
	return ticket, nil
}

// EscalateTicket escalates a ticket to a higher level
func (s *TicketService) EscalateTicket(ticketID uint, escalateToID uint, userID uint, reason string, comment string) (*models.Ticket, error) {
	return s.escalateTicket(ticketID, escalateToID, userID, reason, comment, 0)
}

func (s *TicketService) EscalateTicketExpectedVersion(
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
	return s.escalateTicket(ticketID, escalateToID, userID, reason, comment, expectedVersion)
}

func (s *TicketService) escalateTicket(
	ticketID uint,
	escalateToID uint,
	userID uint,
	reason string,
	comment string,
	authorizedVersion uint64,
) (*models.Ticket, error) {
	ticket, err := s.GetTicket(context.Background(), ticketID)
	if err != nil {
		return nil, err
	}
	if authorizedVersion != 0 && ticket.Version != authorizedVersion {
		return nil, ErrVersionConflict
	}

	oldAssigneeID := ticket.AssignedToID
	oldPriority := ticket.Priority
	expectedVersion := ticket.Version

	ticket.AssignedToID = &escalateToID
	ticket.AssignedToActorType = models.ActorTypeHuman
	ticket.AssignedToActorID = strconv.FormatUint(uint64(escalateToID), 10)
	ticket.IsEscalated = true
	switch ticket.Priority {
	case models.TicketPriorityLow:
		ticket.Priority = models.TicketPriorityNormal
	case models.TicketPriorityNormal:
		ticket.Priority = models.TicketPriorityHigh
	case models.TicketPriorityHigh:
		ticket.Priority = models.TicketPriorityUrgent
	case models.TicketPriorityUrgent:
		ticket.Priority = models.TicketPriorityCritical
	}
	ticket.UpdatedAt = time.Now()
	ticket.Version++

	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := saveTicketCAS(tx, ticket, expectedVersion); err != nil {
			return fmt.Errorf("failed to escalate ticket: %w", err)
		}

		description := fmt.Sprintf("工单升级到用户 ID: %d", escalateToID)
		if reason != "" {
			description += fmt.Sprintf(" (原因: %s)", reason)
		}
		if comment != "" {
			description += fmt.Sprintf(" - %s", comment)
		}

		history := &models.TicketHistory{
			TicketID:    ticketID,
			UserID:      &userID,
			ActorType:   models.ActorTypeHuman,
			ActorID:     strconv.FormatUint(uint64(userID), 10),
			Action:      models.HistoryActionEscalate,
			Description: description,
			FieldName:   "escalation",
			OldValue:    fmt.Sprintf("assigned_to: %s, priority: %s", getAssigneeValue(oldAssigneeID), string(oldPriority)),
			NewValue:    fmt.Sprintf("assigned_to: %d, priority: %s", escalateToID, string(ticket.Priority)),
			IsVisible:   true,
			IsSystem:    false,
			IsAutomated: false,
			IsImportant: true,
		}

		if err := tx.Create(history).Error; err != nil {
			return err
		}
		return s.appendHumanTicketEventTx(
			context.Background(),
			tx,
			"io.chronodesk.ticket.escalated.v1",
			ticket,
			userID,
			map[string]any{
				"ticket_id":      ticket.ID,
				"assigned_to_id": escalateToID,
				"priority":       ticket.Priority,
			},
		)
	})

	if err != nil {
		return nil, err
	}

	s.executeAutomation(context.Background(), "ticket.updated", ticket)
	return ticket, nil
}

// UpdateTicketStatus updates ticket status with workflow support
func (s *TicketService) UpdateTicketStatus(ticketID uint, status string, userID uint, comment string, resolutionNotes string) (*models.Ticket, error) {
	return s.updateTicketStatus(ticketID, status, userID, comment, resolutionNotes, 0)
}

func (s *TicketService) UpdateTicketStatusExpectedVersion(
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
	return s.updateTicketStatus(ticketID, status, userID, comment, resolutionNotes, expectedVersion)
}

func (s *TicketService) updateTicketStatus(
	ticketID uint,
	status string,
	userID uint,
	comment string,
	resolutionNotes string,
	authorizedVersion uint64,
) (*models.Ticket, error) {
	ticket, err := s.GetTicket(context.Background(), ticketID)
	if err != nil {
		return nil, err
	}
	if authorizedVersion != 0 && ticket.Version != authorizedVersion {
		return nil, ErrVersionConflict
	}

	nextStatus := models.TicketStatus(status)
	if !nextStatus.IsValid() || !ticket.Status.CanTransitionTo(nextStatus) {
		return nil, fmt.Errorf("%w: %s to %s", ErrInvalidTicketTransition, ticket.Status, nextStatus)
	}
	if ticket.Status == nextStatus {
		return ticket, nil
	}

	oldStatus := ticket.Status
	expectedVersion := ticket.Version
	ticket.Status = nextStatus
	ticket.UpdatedAt = time.Now()
	ticket.Version++

	applyTicketStatusTimestamps(ticket, nextStatus, time.Now())
	notificationTargets := TicketStatusNotificationOutboxTargets(ticket, userID)

	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := saveTicketCAS(tx, ticket, expectedVersion); err != nil {
			return fmt.Errorf("failed to update ticket status: %w", err)
		}

		description := fmt.Sprintf("状态从「%s」变更为「%s」", getStatusLabel(string(oldStatus)), getStatusLabel(status))
		if comment != "" {
			description += fmt.Sprintf(" - %s", comment)
		}
		if resolutionNotes != "" && status == "resolved" {
			description += fmt.Sprintf(" (解决方案: %s)", resolutionNotes)
		}

		history := &models.TicketHistory{
			TicketID:    ticketID,
			UserID:      &userID,
			ActorType:   models.ActorTypeHuman,
			ActorID:     strconv.FormatUint(uint64(userID), 10),
			Action:      models.HistoryActionStatusChange,
			Description: description,
			FieldName:   "status",
			OldValue:    string(oldStatus),
			NewValue:    status,
			IsVisible:   true,
			IsSystem:    false,
			IsAutomated: false,
			IsImportant: true,
		}

		if err := tx.Create(history).Error; err != nil {
			return err
		}
		eventData := map[string]any{
			"ticket_id":  ticket.ID,
			"old_status": oldStatus,
			"new_status": nextStatus,
		}
		if len(notificationTargets) > 0 {
			addTicketNotificationEventSnapshot(eventData, ticket)
		}
		return s.appendHumanTicketEventTx(
			context.Background(),
			tx,
			"io.chronodesk.ticket.transitioned.v1",
			ticket,
			userID,
			eventData,
			notificationTargets...,
		)
	})

	if err != nil {
		return nil, err
	}

	s.executeAutomation(context.Background(), "ticket.updated", ticket)

	return ticket, nil
}

// GetTicketStatistics returns enhanced statistics for dashboard
func (s *TicketService) GetTicketStatistics(userID uint, role string) (*TicketStatisticsResponse, error) {
	cacheKey := ""
	if s.statsCache != nil && s.statsCacheTTL > 0 {
		cacheKey = fmt.Sprintf("ticket_stats:%s:%d", role, userID)
		if cached, err := s.statsCache.Get(context.Background(), cacheKey); err == nil && cached != "" {
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
	query := scopeHumanTicketQuery(s.db.Model(&models.Ticket{}), userID, role)

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
		SUM(CASE WHEN sla_breached = true THEN 1 ELSE 0 END) AS sla_breached,
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
	case string(models.RoleAgent), string(models.RoleCustomer), string(models.RoleUser):
		stats.MyTickets = stats.Total
	}

	priorityCounts := []struct {
		Priority string
		Count    int64
	}{}

	priorityQuery := scopeHumanTicketQuery(s.db.Model(&models.Ticket{}), userID, role)

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
			_ = s.statsCache.Set(context.Background(), cacheKey, string(payload), s.statsCacheTTL)
		}
	}

	return stats, nil
}

// GetUserTickets gets tickets assigned to a specific user
func (s *TicketService) GetUserTickets(userID uint, status string, priority string, limit int) ([]*models.Ticket, int64, error) {
	var tickets []*models.Ticket
	var total int64

	query := s.db.Model(&models.Ticket{}).Where("assigned_to_id = ?", userID)

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

// GetUnassignedTickets gets unassigned tickets
func (s *TicketService) GetUnassignedTickets(priority string, categoryID string, limit int) ([]*models.Ticket, int64, error) {
	var tickets []*models.Ticket
	var total int64

	query := s.db.Model(&models.Ticket{}).Where("assigned_to_id IS NULL")

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

// GetOverdueTickets gets overdue tickets
func (s *TicketService) GetOverdueTickets(userID uint, role string) ([]*models.Ticket, int64, error) {
	var tickets []*models.Ticket
	var total int64

	now := time.Now()
	query := s.db.Model(&models.Ticket{}).
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

// GetSLABreachedTickets gets SLA breached tickets
func (s *TicketService) GetSLABreachedTickets(userID uint, role string) ([]*models.Ticket, int64, error) {
	var tickets []*models.Ticket
	var total int64

	now := time.Now()
	query := s.db.Model(&models.Ticket{}).
		Where(
			"(tickets.sla_breached = ? OR (tickets.sla_due_date IS NOT NULL AND tickets.sla_due_date < ?)) AND tickets.status NOT IN (?, ?)",
			true,
			now,
			models.TicketStatusResolved,
			models.TicketStatusClosed,
		)
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
	case string(models.RoleCustomer), string(models.RoleUser):
		return query.Where("tickets.created_by_id = ?", userID)
	case string(models.RoleAgent):
		return query.Where("tickets.assigned_to_id = ?", userID)
	case string(models.RoleSupervisor), string(models.RoleAdmin), string(models.RoleSuperUser):
		return query
	default:
		// Authentication currently rejects unknown roles. Keep the data layer
		// fail-closed as defense in depth for direct service callers.
		return query.Where("1 = 0")
	}
}

// BulkAssignTickets assigns multiple tickets to a user
func (s *TicketService) BulkAssignTickets(ticketIDs []uint, assigneeID uint, userID uint, comment string) (*BulkOperationResult, error) {
	result := &BulkOperationResult{
		AssignedTickets: []uint{},
		FailedTickets:   []uint{},
	}

	for _, ticketID := range ticketIDs {
		if _, err := s.AssignTicket(ticketID, assigneeID, userID, comment); err != nil {
			result.FailedTickets = append(result.FailedTickets, ticketID)
			result.FailedCount++
		} else {
			result.AssignedTickets = append(result.AssignedTickets, ticketID)
			result.AssignedCount++
		}
	}

	return result, nil
}

// BulkUpdateStatus updates status for multiple tickets
func (s *TicketService) BulkUpdateStatus(ticketIDs []uint, status string, userID uint, comment string) (*BulkOperationResult, error) {
	result := &BulkOperationResult{
		UpdatedTickets: []uint{},
		FailedTickets:  []uint{},
	}

	for _, ticketID := range ticketIDs {
		if _, err := s.UpdateTicketStatus(ticketID, status, userID, comment, ""); err != nil {
			result.FailedTickets = append(result.FailedTickets, ticketID)
			result.FailedCount++
		} else {
			result.UpdatedTickets = append(result.UpdatedTickets, ticketID)
			result.UpdatedCount++
		}
	}

	return result, nil
}

// GetTicketHistory gets the history for a specific ticket
func (s *TicketService) GetTicketHistory(ticketID uint) ([]*models.TicketHistory, int64, error) {
	var histories []*models.TicketHistory
	var total int64

	query := s.db.Model(&models.TicketHistory{}).Where("ticket_id = ?", ticketID)

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

// DeleteTicket deletes a ticket
func (s *TicketService) DeleteTicket(ctx context.Context, id uint, userID uint, userRole string) error {
	ticket, err := s.GetTicket(ctx, id)
	if err != nil {
		return err
	}

	// Check if user has permission to delete (creator or admin)
	if ticket.CreatedByID != userID {
		if !isElevatedRole(userRole) {
			return fmt.Errorf("permission denied")
		}
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var attachments []models.TicketAttachment
		if err := tx.
			Select("id", "ticket_id", "storage_path").
			Where("ticket_id = ?", id).
			Order("id ASC").
			Find(&attachments).Error; err != nil {
			return fmt.Errorf("failed to load ticket attachment cleanup manifest: %w", err)
		}
		cleanupTargets := make([]OutboxTarget, 0, len(attachments))
		cleanupObjects := make([]AttachmentCleanupObject, 0, len(attachments))
		for i := range attachments {
			target, err := NewAttachmentCleanupOutboxTarget(
				attachments[i].ID,
				attachments[i].StoragePath,
			)
			if err != nil {
				return fmt.Errorf(
					"prepare attachment %d cleanup: %w",
					attachments[i].ID,
					err,
				)
			}
			cleanupTargets = append(cleanupTargets, target)
			cleanupObjects = append(cleanupObjects, AttachmentCleanupObject{
				AttachmentID: attachments[i].ID,
				TicketID:     ticket.ID,
				StoragePath:  attachments[i].StoragePath,
			})
		}
		if err := tx.Where("related_ticket_id = ?", id).Delete(&models.Notification{}).Error; err != nil {
			return fmt.Errorf("failed to delete ticket notifications: %w", err)
		}
		if err := tx.Where("ticket_id = ?", id).Delete(&models.TicketHistory{}).Error; err != nil {
			return fmt.Errorf("failed to delete ticket histories: %w", err)
		}
		if err := tx.Where("ticket_id = ?", id).Delete(&models.TicketAttachment{}).Error; err != nil {
			return fmt.Errorf("failed to delete ticket attachments: %w", err)
		}
		if err := tx.Where("ticket_id = ?", id).Delete(&models.TicketComment{}).Error; err != nil {
			return fmt.Errorf("failed to delete ticket comments: %w", err)
		}
		if err := tx.Delete(ticket).Error; err != nil {
			return fmt.Errorf("failed to delete ticket: %w", err)
		}
		eventData := map[string]any{
			"ticket_id":                ticket.ID,
			"deleted":                  true,
			"attachment_cleanup_count": len(cleanupTargets),
		}
		if len(cleanupObjects) > 0 {
			eventData[AttachmentCleanupObjectsDataField] = cleanupObjects
		}
		return s.appendHumanTicketEventTx(
			ctx,
			tx,
			"io.chronodesk.ticket.deleted.v1",
			ticket,
			userID,
			eventData,
			cleanupTargets...,
		)
	})
}

func isElevatedRole(role string) bool {
	switch strings.ToLower(role) {
	case "admin", "superuser", "super_admin", "super-user":
		return true
	default:
		return false
	}
}

// GetTicketStats returns ticket statistics
func (s *TicketService) GetTicketStats(ctx context.Context, userID uint) (*TicketStats, error) {
	stats := &TicketStats{}

	// Count total tickets
	if err := s.db.WithContext(ctx).Model(&models.Ticket{}).Count(&stats.Total).Error; err != nil {
		return nil, fmt.Errorf("failed to count total tickets: %w", err)
	}

	// Count by status
	if err := s.db.WithContext(ctx).Model(&models.Ticket{}).Where("status = ?", models.TicketStatusOpen).Count(&stats.Open).Error; err != nil {
		return nil, fmt.Errorf("failed to count open tickets: %w", err)
	}

	if err := s.db.WithContext(ctx).Model(&models.Ticket{}).Where("status = ?", models.TicketStatusInProgress).Count(&stats.InProgress).Error; err != nil {
		return nil, fmt.Errorf("failed to count in progress tickets: %w", err)
	}

	if err := s.db.WithContext(ctx).Model(&models.Ticket{}).Where("status = ?", models.TicketStatusResolved).Count(&stats.Resolved).Error; err != nil {
		return nil, fmt.Errorf("failed to count resolved tickets: %w", err)
	}

	if err := s.db.WithContext(ctx).Model(&models.Ticket{}).Where("status = ?", models.TicketStatusClosed).Count(&stats.Closed).Error; err != nil {
		return nil, fmt.Errorf("failed to count closed tickets: %w", err)
	}

	// Count overdue tickets
	now := time.Now()
	if err := s.db.WithContext(ctx).Model(&models.Ticket{}).
		Where("due_date < ? AND status NOT IN (?, ?)", now, models.TicketStatusResolved, models.TicketStatusClosed).
		Count(&stats.Overdue).Error; err != nil {
		return nil, fmt.Errorf("failed to count overdue tickets: %w", err)
	}

	return stats, nil
}

// BulkUpdateTickets updates multiple tickets
func (s *TicketService) BulkUpdateTickets(ctx context.Context, req *BulkUpdateRequest, userID uint) error {
	if req == nil || len(req.TicketIDs) == 0 {
		return fmt.Errorf("%w: no ticket IDs provided", ErrInvalidBulkTicketUpdate)
	}
	if userID == 0 {
		return fmt.Errorf("%w: human actor is required", ErrInvalidBulkTicketUpdate)
	}
	if s.agentNative == nil {
		return errors.New("agent-native ticket service is unavailable")
	}

	ticketIDs, err := normalizedBulkTicketIDs(req.TicketIDs)
	if err != nil {
		return err
	}
	changes, changedFields, err := bulkTicketChanges(req)
	if err != nil {
		return err
	}

	// Compatibility semantics are deliberately all-or-nothing. Every ticket is
	// reloaded and CAS-updated in the same transaction; one stale version,
	// invalid transition, missing resource, history failure or Outbox failure
	// rolls back the complete batch.
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if req.AssignedToID != nil {
			var assigneeCount int64
			if err := tx.Model(&models.User{}).
				Where("id = ?", *req.AssignedToID).
				Count(&assigneeCount).Error; err != nil {
				return fmt.Errorf("validate bulk assignee: %w", err)
			}
			if assigneeCount != 1 {
				return fmt.Errorf(
					"%w: assignee %d not found",
					ErrInvalidBulkTicketUpdate,
					*req.AssignedToID,
				)
			}
		}

		for _, ticketID := range ticketIDs {
			var ticket models.Ticket
			if err := tx.First(&ticket, ticketID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return fmt.Errorf(
						"%w: ticket %d not found",
						ErrInvalidBulkTicketUpdate,
						ticketID,
					)
				}
				return fmt.Errorf("load bulk ticket %d: %w", ticketID, err)
			}
			if ticket.Version == 0 {
				return fmt.Errorf(
					"%w: ticket %d has no resource version",
					ErrVersionConflict,
					ticketID,
				)
			}
			if err := validateTicketChangeSemantics(&ticket, changes); err != nil {
				return fmt.Errorf("ticket %d: %w", ticketID, err)
			}

			expectedVersion := ticket.Version
			changeSet := make(map[string]any, len(changedFields))
			for _, field := range changedFields {
				changeSet[field] = map[string]any{
					"old": ticketFieldValue(&ticket, field),
					"new": changes[field],
				}
			}

			writeChanges := cloneBulkTicketChanges(changes)
			now := time.Now()
			if rawStatus, ok := writeChanges["status"]; ok {
				nextStatus := models.TicketStatus(fmt.Sprint(rawStatus))
				applyTicketStatusTimestamps(&ticket, nextStatus, now)
				writeChanges["resolved_at"] = ticket.ResolvedAt
				writeChanges["closed_at"] = ticket.ClosedAt
			}
			writeChanges["version"] = expectedVersion + 1
			writeChanges["updated_at"] = now

			update := tx.Model(&models.Ticket{}).
				Where("id = ? AND version = ?", ticketID, expectedVersion).
				Updates(writeChanges)
			if update.Error != nil {
				return fmt.Errorf("bulk update ticket %d: %w", ticketID, update.Error)
			}
			if update.RowsAffected != 1 {
				return fmt.Errorf(
					"%w: ticket %d expected version %d",
					ErrVersionConflict,
					ticketID,
					expectedVersion,
				)
			}
			if err := tx.First(&ticket, ticketID).Error; err != nil {
				return fmt.Errorf("reload bulk ticket %d: %w", ticketID, err)
			}

			details, err := json.Marshal(map[string]any{
				"bulk":           true,
				"changed_fields": changedFields,
				"changes":        changeSet,
				"old_version":    expectedVersion,
				"new_version":    ticket.Version,
			})
			if err != nil {
				return fmt.Errorf("encode bulk ticket %d audit: %w", ticketID, err)
			}
			metadata, err := json.Marshal(map[string]any{
				"bulk":            true,
				"source_protocol": "rest-human",
			})
			if err != nil {
				return fmt.Errorf("encode bulk ticket %d metadata: %w", ticketID, err)
			}
			actorUserID := userID
			history := &models.TicketHistory{
				TicketID:    ticketID,
				UserID:      &actorUserID,
				ActorType:   models.ActorTypeHuman,
				ActorID:     models.HumanActor(userID).ID,
				Action:      historyActionForChanges(changedFields),
				Description: "批量更新工单",
				Details:     string(details),
				FieldName:   bulkHistoryFieldName(changedFields),
				IsVisible:   true,
				IsSystem:    false,
				IsAutomated: false,
				IsImportant: true,
				Metadata:    string(metadata),
			}
			if len(changedFields) == 1 {
				history.OldValue = bulkAuditValue(ticketFieldValueBeforeChange(changeSet, changedFields[0], "old"))
				history.NewValue = bulkAuditValue(ticketFieldValueBeforeChange(changeSet, changedFields[0], "new"))
			}
			if err := tx.Create(history).Error; err != nil {
				return fmt.Errorf("create bulk ticket %d history: %w", ticketID, err)
			}

			if err := s.appendHumanTicketEventTx(
				ctx,
				tx,
				bulkTicketEventType(changedFields),
				&ticket,
				userID,
				map[string]any{
					"ticket_id":      ticket.ID,
					"changed_fields": changedFields,
					"changes":        changeSet,
					"old_version":    expectedVersion,
					"new_version":    ticket.Version,
					"bulk":           true,
					"status":         ticket.Status,
					"new_status":     ticket.Status,
				},
			); err != nil {
				return fmt.Errorf("append bulk ticket %d event: %w", ticketID, err)
			}
		}
		return nil
	})
}

func bulkTicketEventType(changedFields []string) string {
	for _, field := range changedFields {
		if field == "status" {
			return "io.chronodesk.ticket.transitioned.v1"
		}
	}
	for _, field := range changedFields {
		if field == "assigned_to_id" {
			return "io.chronodesk.ticket.assigned.v1"
		}
	}
	return "io.chronodesk.ticket.updated.v1"
}

func normalizedBulkTicketIDs(input []uint) ([]uint, error) {
	ids := append([]uint(nil), input...)
	seen := make(map[uint]struct{}, len(ids))
	for _, ticketID := range ids {
		if ticketID == 0 {
			return nil, fmt.Errorf(
				"%w: ticket IDs must be positive",
				ErrInvalidBulkTicketUpdate,
			)
		}
		if _, exists := seen[ticketID]; exists {
			return nil, fmt.Errorf(
				"%w: duplicate ticket ID %d",
				ErrInvalidBulkTicketUpdate,
				ticketID,
			)
		}
		seen[ticketID] = struct{}{}
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	return ids, nil
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

// CreateTicketHistory creates a new ticket history record
func (s *TicketService) CreateTicketHistory(ctx context.Context, req *models.TicketHistoryCreateRequest, userID *uint) error {
	actorType := models.ActorTypeSystem
	actorID := "chronodesk"
	if userID != nil {
		actorType = models.ActorTypeHuman
		actorID = strconv.FormatUint(uint64(*userID), 10)
	}
	history := &models.TicketHistory{
		TicketID:     req.TicketID,
		UserID:       userID,
		ActorType:    actorType,
		ActorID:      actorID,
		Action:       req.Action,
		Description:  req.Description,
		FieldName:    req.FieldName,
		OldValue:     req.OldValue,
		NewValue:     req.NewValue,
		CommentID:    req.CommentID,
		AttachmentID: req.AttachmentID,
		IsVisible:    true,
		IsSystem:     userID == nil,
		IsAutomated:  false,
		IsImportant:  false,
	}

	// 设置可选字段
	if req.IsVisible != nil {
		history.IsVisible = *req.IsVisible
	}
	if req.IsImportant != nil {
		history.IsImportant = *req.IsImportant
	}

	// 处理JSON字段
	if req.Details != nil {
		detailsJSON, _ := json.Marshal(req.Details)
		history.Details = string(detailsJSON)
	}
	if req.Metadata != nil {
		metadataJSON, _ := json.Marshal(req.Metadata)
		history.Metadata = string(metadataJSON)
	}

	// 如果没有用户ID，设置为系统操作
	if userID == nil {
		history.IsSystem = true
	}

	// 保存历史记录
	if err := s.db.WithContext(ctx).Create(history).Error; err != nil {
		return fmt.Errorf("failed to create ticket history: %w", err)
	}

	return nil
}

// Helper functions

// getBoolPtr returns a pointer to a boolean value
func getBoolPtr(b bool) *bool {
	return &b
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
	case models.TicketStatusOpen:
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

// generateTicketNumber generates a unique ticket number
func (s *TicketService) generateTicketNumber() string {
	now := time.Now()
	randomSuffix := make([]byte, 6)
	if _, err := cryptorand.Read(randomSuffix); err != nil {
		return fmt.Sprintf("TK-%s-%d", now.Format("20060102-150405"), now.UnixNano())
	}
	return fmt.Sprintf("TK-%s-%x", now.Format("20060102-150405"), randomSuffix)
}
