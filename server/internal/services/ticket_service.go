package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"gongdan-system/internal/models"
	"gorm.io/gorm"
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
	db                  *gorm.DB
	notificationService NotificationServiceInterface
}

// NewTicketService creates a new ticket service
func NewTicketService(db *gorm.DB) TicketServiceInterface {
	return &TicketService{
		db:                  db,
		notificationService: NewNotificationService(db),
	}
}

// TicketFilters represents filters for ticket queries
type TicketFilters struct {
	Status     string
	Priority   string
	Type       string
	Tags       []string
	AssigneeID *uint
	CreatorID  *uint
	Search     string
	Page       int
	Limit      int
	SortBy     string
	SortOrder  string
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

	// Count total
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count tickets: %w", err)
	}

	// Apply pagination
	if filters.Page > 0 && filters.Limit > 0 {
		offset := (filters.Page - 1) * filters.Limit
		query = query.Offset(offset).Limit(filters.Limit)
	}

	// Apply sorting
	sortBy := "created_at"
	sortOrder := "DESC"
	if filters.SortBy != "" {
		sortBy = filters.SortBy
	}
	if filters.SortOrder != "" {
		sortOrder = filters.SortOrder
	}
	query = query.Order(fmt.Sprintf("%s %s", sortBy, sortOrder))

	// Preload associations
	query = query.Preload("CreatedBy").Preload("AssignedTo").Preload("Comments")

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
	// Convert tags to JSON string
	tagsJSON := ""
	if len(req.Tags) > 0 {
		tagsBytes, _ := json.Marshal(req.Tags)
		tagsJSON = string(tagsBytes)
	}

	// Convert custom fields to JSON string
	customFieldsJSON := ""
	if req.CustomFields != nil {
		customFieldsBytes, _ := json.Marshal(req.CustomFields)
		customFieldsJSON = string(customFieldsBytes)
	}

	// Generate unique ticket number
	ticketNumber := s.generateTicketNumber()

	status := models.TicketStatusOpen
	if req.Status != nil {
		status = models.TicketStatus(*req.Status)
	}

	now := time.Now()

	ticket := &models.Ticket{
		TicketNumber:  ticketNumber,
		Title:         req.Title,
		Description:   req.Description,
		Status:        status,
		Priority:      req.Priority,
		Type:          req.Type,
		Source:        req.Source,
		CreatedByID:   userID,
		Tags:          tagsJSON,
		CustomFields:  customFieldsJSON,
		CustomerEmail: req.CustomerEmail,
		CustomerPhone: req.CustomerPhone,
		CustomerName:  req.CustomerName,
		CreatedAt:     now,
		UpdatedAt:     now,
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
	}

	// Set category if provided
	if req.CategoryID != nil {
		ticket.CategoryID = req.CategoryID
	}

	// Set subcategory if provided
	if req.SubcategoryID != nil {
		ticket.SubcategoryID = req.SubcategoryID
	}

	// Set due date if provided
	if req.DueDate != nil {
		ticket.DueDate = req.DueDate
	}

	if err := s.db.WithContext(ctx).Create(ticket).Error; err != nil {
		return nil, fmt.Errorf("failed to create ticket: %w", err)
	}

	// Reload with associations
	return s.GetTicket(ctx, ticket.ID)
}

// UpdateTicket updates an existing ticket
func (s *TicketService) UpdateTicket(ctx context.Context, id uint, req *models.TicketUpdateRequest, userID uint) (*models.Ticket, error) {
	// 获取原工单信息用于比较
	originalTicket, err := s.GetTicket(ctx, id)
	if err != nil {
		return nil, err
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

		// 设置特殊时间戳
		now := time.Now()
		if *req.Status == "resolved" && ticket.ResolvedAt == nil {
			ticket.ResolvedAt = &now
		}
		if *req.Status == "closed" && ticket.ClosedAt == nil {
			ticket.ClosedAt = &now
		}
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
		tagsBytes, _ := json.Marshal(req.Tags)
		ticket.Tags = string(tagsBytes)
	}
	if req.CustomFields != nil {
		customFieldsBytes, _ := json.Marshal(req.CustomFields)
		ticket.CustomFields = string(customFieldsBytes)
	}

	ticket.UpdatedAt = time.Now()

	// 在事务中保存工单和历史记录
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 保存工单更新
		if err := tx.Save(&ticket).Error; err != nil {
			return fmt.Errorf("failed to update ticket: %w", err)
		}

		// 创建历史记录
		for _, historyReq := range historyRecords {
			history := &models.TicketHistory{
				TicketID:    historyReq.TicketID,
				UserID:      &userID,
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

		return nil
	})

	if err != nil {
		return nil, err
	}

	// 发送通知
	go func() {
		// 检查是否有状态变更需要发送通知
		if req.Status != nil && models.TicketStatus(*req.Status) != originalTicket.Status {
			if err := s.notificationService.NotifyTicketStatusChanged(ctx, &ticket, originalTicket.Status, userID); err != nil {
				// 记录错误但不影响主流程
				fmt.Printf("Failed to send ticket status change notification: %v\n", err)
			}
		}

		// 检查是否有分配变更需要发送通知
		if req.AssignedToID != nil {
			// 如果之前没有分配或者分配给了不同的人
			if originalTicket.AssignedToID == nil || *originalTicket.AssignedToID != *req.AssignedToID {
				if err := s.notificationService.NotifyTicketAssigned(ctx, &ticket, userID); err != nil {
					fmt.Printf("Failed to send ticket assignment notification: %v\n", err)
				}
			}
		}
	}()

	return &ticket, nil
}

// AssignTicket assigns a ticket to a user with workflow support
func (s *TicketService) AssignTicket(ticketID uint, assigneeID uint, userID uint, comment string) (*models.Ticket, error) {
	ticket, err := s.GetTicket(context.Background(), ticketID)
	if err != nil {
		return nil, err
	}

	oldAssigneeID := ticket.AssignedToID
	ticket.AssignedToID = &assigneeID
	ticket.UpdatedAt = time.Now()

	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(ticket).Error; err != nil {
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

		return tx.Create(history).Error
	})

	if err != nil {
		return nil, err
	}

	go func() {
		if err := s.notificationService.NotifyTicketAssigned(context.Background(), ticket, userID); err != nil {
			fmt.Printf("Failed to send assignment notification: %v\n", err)
		}
	}()

	return ticket, nil
}

// TransferTicket transfers a ticket to another user
func (s *TicketService) TransferTicket(ticketID uint, assigneeID uint, userID uint, comment string, transferReason string) (*models.Ticket, error) {
	ticket, err := s.GetTicket(context.Background(), ticketID)
	if err != nil {
		return nil, err
	}

	oldAssigneeID := ticket.AssignedToID
	ticket.AssignedToID = &assigneeID
	ticket.UpdatedAt = time.Now()

	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(ticket).Error; err != nil {
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

		return tx.Create(history).Error
	})

	if err != nil {
		return nil, err
	}

	return ticket, nil
}

// EscalateTicket escalates a ticket to a higher level
func (s *TicketService) EscalateTicket(ticketID uint, escalateToID uint, userID uint, reason string, comment string) (*models.Ticket, error) {
	ticket, err := s.GetTicket(context.Background(), ticketID)
	if err != nil {
		return nil, err
	}

	oldAssigneeID := ticket.AssignedToID
	oldPriority := ticket.Priority

	ticket.AssignedToID = &escalateToID
	if ticket.Priority == models.TicketPriorityLow || ticket.Priority == models.TicketPriorityNormal {
		ticket.Priority = models.TicketPriorityHigh
	} else if ticket.Priority == models.TicketPriorityHigh {
		ticket.Priority = models.TicketPriorityUrgent
	}
	ticket.UpdatedAt = time.Now()

	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(ticket).Error; err != nil {
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

		return tx.Create(history).Error
	})

	if err != nil {
		return nil, err
	}

	return ticket, nil
}

// UpdateTicketStatus updates ticket status with workflow support
func (s *TicketService) UpdateTicketStatus(ticketID uint, status string, userID uint, comment string, resolutionNotes string) (*models.Ticket, error) {
	ticket, err := s.GetTicket(context.Background(), ticketID)
	if err != nil {
		return nil, err
	}

	oldStatus := ticket.Status
	ticket.Status = models.TicketStatus(status)
	ticket.UpdatedAt = time.Now()

	now := time.Now()
	if status == "resolved" && ticket.ResolvedAt == nil {
		ticket.ResolvedAt = &now
	}
	if status == "closed" && ticket.ClosedAt == nil {
		ticket.ClosedAt = &now
	}

	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(ticket).Error; err != nil {
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

		return tx.Create(history).Error
	})

	if err != nil {
		return nil, err
	}

	go func() {
		if err := s.notificationService.NotifyTicketStatusChanged(context.Background(), ticket, oldStatus, userID); err != nil {
			fmt.Printf("Failed to send status change notification: %v\n", err)
		}
	}()

	return ticket, nil
}

// GetTicketStatistics returns enhanced statistics for dashboard
func (s *TicketService) GetTicketStatistics(userID uint, role string) (*TicketStatisticsResponse, error) {
	stats := &TicketStatisticsResponse{
		ByPriority: make(map[string]int64),
		ByCategory: make(map[string]int64),
	}

	query := s.db.Model(&models.Ticket{})

	if role == "agent" {
		query = query.Where("assigned_to_id = ?", userID)
	}

	if err := query.Count(&stats.Total).Error; err != nil {
		return nil, fmt.Errorf("failed to count total tickets: %w", err)
	}

	statusCounts := []struct {
		Status string
		Count  int64
	}{}

	if err := s.db.Model(&models.Ticket{}).
		Select("status, count(*) as count").
		Group("status").
		Find(&statusCounts).Error; err != nil {
		return nil, fmt.Errorf("failed to get status statistics: %w", err)
	}

	for _, sc := range statusCounts {
		switch sc.Status {
		case "open":
			stats.Open = sc.Count
		case "in_progress":
			stats.InProgress = sc.Count
		case "pending":
			stats.Pending = sc.Count
		case "resolved":
			stats.Resolved = sc.Count
		case "closed":
			stats.Closed = sc.Count
		}
	}

	now := time.Now()
	if err := s.db.Model(&models.Ticket{}).
		Where("due_date < ? AND status NOT IN (?, ?)", now, models.TicketStatusResolved, models.TicketStatusClosed).
		Count(&stats.Overdue).Error; err != nil {
		return nil, fmt.Errorf("failed to count overdue tickets: %w", err)
	}

	if err := s.db.Model(&models.Ticket{}).
		Where("assigned_to_id IS NULL").
		Count(&stats.Unassigned).Error; err != nil {
		return nil, fmt.Errorf("failed to count unassigned tickets: %w", err)
	}

	if err := s.db.Model(&models.Ticket{}).
		Where("priority IN (?, ?)", models.TicketPriorityHigh, models.TicketPriorityUrgent).
		Count(&stats.HighPriority).Error; err != nil {
		return nil, fmt.Errorf("failed to count high priority tickets: %w", err)
	}

	if role == "agent" {
		if err := s.db.Model(&models.Ticket{}).
			Where("assigned_to_id = ?", userID).
			Count(&stats.MyTickets).Error; err != nil {
			return nil, fmt.Errorf("failed to count my tickets: %w", err)
		}
	}

	priorityCounts := []struct {
		Priority string
		Count    int64
	}{}
	if err := s.db.Model(&models.Ticket{}).
		Select("priority, count(*) as count").
		Group("priority").
		Find(&priorityCounts).Error; err == nil {
		for _, pc := range priorityCounts {
			stats.ByPriority[pc.Priority] = pc.Count
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

	if role == "agent" {
		query = query.Where("assigned_to_id = ?", userID)
	}

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

	query := s.db.Model(&models.Ticket{}).
		Joins("JOIN categories c ON tickets.category_id = c.id").
		Where("tickets.created_at + INTERVAL c.sla_hours HOUR < NOW() AND tickets.status NOT IN (?, ?)",
			models.TicketStatusResolved, models.TicketStatusClosed)

	if role == "agent" {
		query = query.Where("tickets.assigned_to_id = ?", userID)
	}

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

	if err := s.db.WithContext(ctx).Delete(ticket).Error; err != nil {
		return fmt.Errorf("failed to delete ticket: %w", err)
	}

	return nil
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
	if len(req.TicketIDs) == 0 {
		return fmt.Errorf("no ticket IDs provided")
	}

	updates := make(map[string]interface{})

	if req.Status != nil {
		updates["status"] = *req.Status
	}
	if req.Priority != nil {
		updates["priority"] = *req.Priority
	}
	if req.AssignedToID != nil {
		updates["assigned_to_id"] = *req.AssignedToID
	}
	if req.Tags != nil {
		updates["tags"] = req.Tags
	}
	if req.CustomFields != nil {
		customFieldsBytes, _ := json.Marshal(req.CustomFields)
		updates["custom_fields"] = string(customFieldsBytes)
	}

	updates["updated_at"] = time.Now()

	if err := s.db.WithContext(ctx).Model(&models.Ticket{}).
		Where("id IN ?", req.TicketIDs).
		Updates(updates).Error; err != nil {
		return fmt.Errorf("failed to bulk update tickets: %w", err)
	}

	return nil
}

// CreateTicketHistory creates a new ticket history record
func (s *TicketService) CreateTicketHistory(ctx context.Context, req *models.TicketHistoryCreateRequest, userID *uint) error {
	history := &models.TicketHistory{
		TicketID:     req.TicketID,
		UserID:       userID,
		Action:       req.Action,
		Description:  req.Description,
		FieldName:    req.FieldName,
		OldValue:     req.OldValue,
		NewValue:     req.NewValue,
		CommentID:    req.CommentID,
		AttachmentID: req.AttachmentID,
		IsVisible:    true,
		IsSystem:     false,
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
	}
	if label, exists := labels[source]; exists {
		return label
	}
	return source
}

// generateTicketNumber generates a unique ticket number
func (s *TicketService) generateTicketNumber() string {
	now := time.Now()
	// Format: TK-YYYYMMDD-HHMMSS-RRR (RRR is random 3-digit number)
	randomNum := rand.Intn(1000)
	return fmt.Sprintf("TK-%s-%03d", now.Format("20060102-150405"), randomNum)
}
