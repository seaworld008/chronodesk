package services

import (
	"context"
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"
	"gongdan-system/internal/models"
)

// EnhancedTicketService 增强的工单服务，集成通知功能
type EnhancedTicketService struct {
	*TicketService              // 嵌入原有服务
	notificationService *NotificationService
}

// NewEnhancedTicketService 创建增强工单服务
func NewEnhancedTicketService(db *gorm.DB) *EnhancedTicketService {
	return &EnhancedTicketService{
		TicketService:       NewTicketService(db).(*TicketService),
		notificationService: NewNotificationService(db),
	}
}

// CreateTicketWithNotification 创建工单并发送通知
func (s *EnhancedTicketService) CreateTicketWithNotification(ctx context.Context, req *models.TicketCreateRequest, userID uint) (*models.Ticket, error) {
	// 创建工单
	ticket, err := s.CreateTicket(ctx, req, userID)
	if err != nil {
		return nil, err
	}

	// 加载关联数据用于通知
	s.db.Preload("CreatedBy").Preload("Category").First(ticket, ticket.ID)

	// 异步发送通知
	go func() {
		event := &NotificationEvent{
			Type:        models.WebhookEventTicketCreated,
			ResourceID:  ticket.ID,
			ResourceType: "ticket",
			Title:       fmt.Sprintf("新工单: %s", ticket.Title),
			Description: fmt.Sprintf("用户 %s 创建了新工单", ticket.CreatedBy.Username),
			Data: map[string]interface{}{
				"ticket_number": ticket.TicketNumber,
				"title":         ticket.Title,
				"description":   ticket.Description,
				"priority":      ticket.Priority,
				"type":          ticket.Type,
				"status":        ticket.Status,
				"creator":       ticket.CreatedBy.Username,
				"creator_email": ticket.CreatedBy.Email,
			},
			Metadata: map[string]string{
				"category": func() string {
					if ticket.Category != nil {
						return ticket.Category.Name
					}
					return "未分类"
				}(),
			},
			Timestamp: ticket.CreatedAt,
			UserID:    &userID,
		}

		if err := s.notificationService.SendNotification(context.Background(), event); err != nil {
			log.Printf("发送工单创建通知失败: %v", err)
		}
	}()

	return ticket, nil
}

// AssignTicketWithNotification 分配工单并发送通知
func (s *EnhancedTicketService) AssignTicketWithNotification(ctx context.Context, id uint, assigneeID uint, userID uint) (*models.Ticket, error) {
	// 获取原工单信息
	var oldTicket models.Ticket
	if err := s.db.Preload("AssignedTo").First(&oldTicket, id).Error; err != nil {
		return nil, err
	}

	// 执行分配
	ticket, err := s.AssignTicket(id, assigneeID, userID, "")
	if err != nil {
		return nil, err
	}

	// 加载关联数据
	s.db.Preload("CreatedBy").Preload("AssignedTo").Preload("Category").First(ticket, ticket.ID)

	// 异步发送通知
	go func() {
		var assigneeName string
		var assigneeEmail string
		if ticket.AssignedTo != nil {
			assigneeName = ticket.AssignedTo.Username
			assigneeEmail = ticket.AssignedTo.Email
		}

		event := &NotificationEvent{
			Type:        models.WebhookEventTicketAssigned,
			ResourceID:  ticket.ID,
			ResourceType: "ticket",
			Title:       fmt.Sprintf("工单分配: %s", ticket.Title),
			Description: fmt.Sprintf("工单已分配给 %s", assigneeName),
			Data: map[string]interface{}{
				"ticket_number":   ticket.TicketNumber,
				"title":           ticket.Title,
				"assignee":        assigneeName,
				"assignee_email":  assigneeEmail,
				"previous_assignee": func() string {
					if oldTicket.AssignedTo != nil {
						return oldTicket.AssignedTo.Username
					}
					return "无"
				}(),
			},
			Metadata: map[string]string{
				"action": "assign",
			},
			Timestamp: time.Now(),
			UserID:    &userID,
		}

		if err := s.notificationService.SendNotification(context.Background(), event); err != nil {
			log.Printf("发送工单分配通知失败: %v", err)
		}
	}()

	return ticket, nil
}

// UpdateTicketWithNotification 更新工单并发送通知
func (s *EnhancedTicketService) UpdateTicketWithNotification(ctx context.Context, id uint, req *models.TicketUpdateRequest, userID uint) (*models.Ticket, error) {
	// 获取更新前的工单状态
	var oldTicket models.Ticket
	if err := s.db.Preload("CreatedBy").First(&oldTicket, id).Error; err != nil {
		return nil, err
	}

	// 执行更新
	ticket, err := s.UpdateTicket(ctx, id, req, userID)
	if err != nil {
		return nil, err
	}

	// 加载关联数据
	s.db.Preload("CreatedBy").Preload("AssignedTo").Preload("Category").First(ticket, ticket.ID)

	// 检查是否有重要字段变化
	var eventType models.WebhookEventType
	var title string
	var description string

	if oldTicket.Status != ticket.Status {
		switch ticket.Status {
		case models.TicketStatusResolved:
			eventType = models.WebhookEventTicketResolved
			title = fmt.Sprintf("工单解决: %s", ticket.Title)
			description = fmt.Sprintf("工单状态已更新为: 已解决")
		case models.TicketStatusClosed:
			eventType = models.WebhookEventTicketClosed
			title = fmt.Sprintf("工单关闭: %s", ticket.Title)
			description = fmt.Sprintf("工单状态已更新为: 已关闭")
		default:
			eventType = models.WebhookEventTicketUpdated
			title = fmt.Sprintf("工单更新: %s", ticket.Title)
			description = fmt.Sprintf("工单状态从 %s 更新为 %s", oldTicket.Status, ticket.Status)
		}
	} else if oldTicket.Priority != ticket.Priority {
		eventType = models.WebhookEventTicketUpdated
		title = fmt.Sprintf("工单更新: %s", ticket.Title)
		description = fmt.Sprintf("工单优先级从 %s 更新为 %s", oldTicket.Priority, ticket.Priority)
	} else {
		eventType = models.WebhookEventTicketUpdated
		title = fmt.Sprintf("工单更新: %s", ticket.Title)
		description = "工单信息已更新"
	}

	// 异步发送通知
	go func() {
		event := &NotificationEvent{
			Type:        eventType,
			ResourceID:  ticket.ID,
			ResourceType: "ticket",
			Title:       title,
			Description: description,
			Data: map[string]interface{}{
				"ticket_number": ticket.TicketNumber,
				"title":         ticket.Title,
				"old_status":    oldTicket.Status,
				"new_status":    ticket.Status,
				"old_priority":  oldTicket.Priority,
				"new_priority":  ticket.Priority,
				"updater":       ticket.CreatedBy.Username,
			},
			Metadata: map[string]string{
				"action": "update",
			},
			Timestamp: time.Now(),
			UserID:    &userID,
		}

		if err := s.notificationService.SendNotification(context.Background(), event); err != nil {
			log.Printf("发送工单更新通知失败: %v", err)
		}
	}()

	return ticket, nil
}

// SendCommentNotification 发送评论通知
func (s *EnhancedTicketService) SendCommentNotification(ctx context.Context, comment *models.TicketComment) error {
	// 加载关联数据
	var ticket models.Ticket
	if err := s.db.Preload("CreatedBy").Preload("AssignedTo").First(&ticket, comment.TicketID).Error; err != nil {
		return fmt.Errorf("获取工单信息失败: %w", err)
	}

	var user models.User
	if err := s.db.First(&user, comment.UserID).Error; err != nil {
		return fmt.Errorf("获取评论用户信息失败: %w", err)
	}

	// 异步发送通知
	go func() {
		event := &NotificationEvent{
			Type:        models.WebhookEventTicketComment,
			ResourceID:  ticket.ID,
			ResourceType: "ticket",
			Title:       fmt.Sprintf("新评论: %s", ticket.Title),
			Description: fmt.Sprintf("%s 在工单上添加了评论", user.Username),
			Data: map[string]interface{}{
				"ticket_number":  ticket.TicketNumber,
				"title":          ticket.Title,
				"comment_author": user.Username,
				"comment_content": func() string {
					if len(comment.Content) > 100 {
						return comment.Content[:100] + "..."
					}
					return comment.Content
				}(),
			},
			Metadata: map[string]string{
				"comment_id": fmt.Sprintf("%d", comment.ID),
			},
			Timestamp: comment.CreatedAt,
			UserID:    &comment.UserID,
		}

		if err := s.notificationService.SendNotification(context.Background(), event); err != nil {
			log.Printf("发送评论通知失败: %v", err)
		}
	}()

	return nil
}

// SendEscalationNotification 发送升级通知
func (s *EnhancedTicketService) SendEscalationNotification(ctx context.Context, ticketID uint, reason string, userID uint) error {
	var ticket models.Ticket
	if err := s.db.Preload("CreatedBy").Preload("AssignedTo").Preload("Category").First(&ticket, ticketID).Error; err != nil {
		return fmt.Errorf("获取工单信息失败: %w", err)
	}

	// 异步发送通知
	go func() {
		event := &NotificationEvent{
			Type:        models.WebhookEventTicketEscalated,
			ResourceID:  ticket.ID,
			ResourceType: "ticket",
			Title:       fmt.Sprintf("工单升级: %s", ticket.Title),
			Description: fmt.Sprintf("工单因为 %s 被升级处理", reason),
			Data: map[string]interface{}{
				"ticket_number": ticket.TicketNumber,
				"title":         ticket.Title,
				"reason":        reason,
				"priority":      ticket.Priority,
				"assignee": func() string {
					if ticket.AssignedTo != nil {
						return ticket.AssignedTo.Username
					}
					return "未分配"
				}(),
			},
			Metadata: map[string]string{
				"escalation_reason": reason,
				"urgency":           "high",
			},
			Timestamp: time.Now(),
			UserID:    &userID,
		}

		if err := s.notificationService.SendNotification(context.Background(), event); err != nil {
			log.Printf("发送升级通知失败: %v", err)
		}
	}()

	return nil
}

// SendSystemAlert 发送系统告警通知
func (s *EnhancedTicketService) SendSystemAlert(ctx context.Context, alertType, title, message string) error {
	event := &NotificationEvent{
		Type:        models.WebhookEventSystemAlert,
		ResourceID:  0, // 系统级别事件
		ResourceType: "system",
		Title:       fmt.Sprintf("系统告警: %s", title),
		Description: message,
		Data: map[string]interface{}{
			"alert_type": alertType,
			"severity":   "warning",
			"system":     "ticket-system",
		},
		Metadata: map[string]string{
			"alert_category": alertType,
		},
		Timestamp: time.Now(),
	}

	return s.notificationService.SendNotification(ctx, event)
}

// NotificationIntegration 通知集成辅助方法
type NotificationIntegration struct {
	service *EnhancedTicketService
}

// NewNotificationIntegration 创建通知集成实例
func NewNotificationIntegration(service *EnhancedTicketService) *NotificationIntegration {
	return &NotificationIntegration{service: service}
}

// OnTicketCreated 工单创建事件处理
func (ni *NotificationIntegration) OnTicketCreated(ticket *models.Ticket, userID uint) {
	go func() {
		// 加载关联数据
		ni.service.db.Preload("CreatedBy").Preload("Category").First(ticket, ticket.ID)

		event := &NotificationEvent{
			Type:        models.WebhookEventTicketCreated,
			ResourceID:  ticket.ID,
			ResourceType: "ticket",
			Title:       fmt.Sprintf("新工单: %s", ticket.Title),
			Description: fmt.Sprintf("用户 %s 创建了新工单", ticket.CreatedBy.Username),
			Data: map[string]interface{}{
				"ticket_number": ticket.TicketNumber,
				"title":         ticket.Title,
				"priority":      ticket.Priority,
				"type":          ticket.Type,
				"creator":       ticket.CreatedBy.Username,
			},
			Timestamp: ticket.CreatedAt,
			UserID:    &userID,
		}

		if err := ni.service.notificationService.SendNotification(context.Background(), event); err != nil {
			log.Printf("发送工单创建通知失败: %v", err)
		}
	}()
}

// OnTicketStatusChanged 工单状态变更事件处理
func (ni *NotificationIntegration) OnTicketStatusChanged(ticket *models.Ticket, oldStatus, newStatus models.TicketStatus, userID uint) {
	go func() {
		var eventType models.WebhookEventType
		var description string

		switch newStatus {
		case models.TicketStatusResolved:
			eventType = models.WebhookEventTicketResolved
			description = "工单已解决"
		case models.TicketStatusClosed:
			eventType = models.WebhookEventTicketClosed
			description = "工单已关闭"
		default:
			eventType = models.WebhookEventTicketUpdated
			description = fmt.Sprintf("工单状态从 %s 更新为 %s", oldStatus, newStatus)
		}

		event := &NotificationEvent{
			Type:        eventType,
			ResourceID:  ticket.ID,
			ResourceType: "ticket",
			Title:       fmt.Sprintf("工单状态更新: %s", ticket.Title),
			Description: description,
			Data: map[string]interface{}{
				"ticket_number": ticket.TicketNumber,
				"title":         ticket.Title,
				"old_status":    oldStatus,
				"new_status":    newStatus,
			},
			Timestamp: time.Now(),
			UserID:    &userID,
		}

		if err := ni.service.notificationService.SendNotification(context.Background(), event); err != nil {
			log.Printf("发送状态变更通知失败: %v", err)
		}
	}()
}