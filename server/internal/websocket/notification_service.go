package websocket

import (
	"context"
	"log"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/observability"
)

// NotificationWebSocketService handles real-time notification pushing
type NotificationWebSocketService struct {
	hub *Hub
}

// NewNotificationWebSocketService creates a new notification websocket service
func NewNotificationWebSocketService(hub *Hub) *NotificationWebSocketService {
	return &NotificationWebSocketService{
		hub: hub,
	}
}

// PushNotification sends a notification to a specific user via WebSocket
func (s *NotificationWebSocketService) PushNotification(ctx context.Context, notification *models.Notification) error {
	// Check if user is online
	if !s.hub.IsUserOnline(notification.RecipientID) {
		log.Printf(
			"Notification recipient is offline: user_id=%s delivery=deferred",
			safeLogUint(notification.RecipientID),
		)
		return nil
	}

	// Convert notification to response format
	notificationData := notification.ToResponse()

	// Send the notification via WebSocket
	s.hub.BroadcastToUser(notification.RecipientID, "notification", map[string]interface{}{
		"id":             notificationData.ID,
		"type":           notificationData.Type,
		"title":          notificationData.Title,
		"content":        notificationData.Content,
		"priority":       notificationData.Priority,
		"created_at":     notificationData.CreatedAt,
		"is_read":        notificationData.IsRead,
		"action_url":     notificationData.ActionURL,
		"sender":         notificationData.Sender,
		"related_ticket": notificationData.RelatedTicket,
	})

	log.Printf(
		"Pushed WebSocket notification: notification_id=%s user_id=%s",
		safeLogUint(notification.ID),
		safeLogUint(notification.RecipientID),
	)
	return nil
}

// PushSystemNotification sends a system-wide notification to all connected users
func (s *NotificationWebSocketService) PushSystemNotification(ctx context.Context, title, content string) error {
	s.hub.BroadcastToAll("system_notification", map[string]interface{}{
		"title":     title,
		"content":   content,
		"timestamp": time.Now().Unix(),
		"priority":  "normal",
	})

	log.Print("Pushed system notification to all connected users")
	return nil
}

// PushUnreadCount sends updated unread count to a specific user
func (s *NotificationWebSocketService) PushUnreadCount(ctx context.Context, userID uint, count int64) error {
	if !s.hub.IsUserOnline(userID) {
		return nil
	}

	s.hub.BroadcastToUser(userID, "unread_count", map[string]interface{}{
		"count":     count,
		"timestamp": time.Now().Unix(),
	})

	log.Printf(
		"Pushed unread count: count=%s user_id=%s",
		safeLogInt64(count),
		safeLogUint(userID),
	)
	return nil
}

// PushTicketUpdate sends ticket update notification to relevant users
func (s *NotificationWebSocketService) PushTicketUpdate(ctx context.Context, ticket *models.Ticket, updateType string) error {
	// Create a list of users to notify
	usersToNotify := make(map[uint]bool)

	// Notify the creator
	if ticket.CreatedByID != 0 {
		usersToNotify[ticket.CreatedByID] = true
	}

	// Notify the assignee
	if ticket.AssignedToID != nil && *ticket.AssignedToID != 0 {
		usersToNotify[*ticket.AssignedToID] = true
	}

	// Prepare ticket update data
	updateData := map[string]interface{}{
		"ticket_id":   ticket.ID,
		"title":       ticket.Title,
		"status":      ticket.Status,
		"priority":    ticket.Priority,
		"update_type": updateType,
		"timestamp":   time.Now().Unix(),
	}

	// Send to all relevant users who are online
	for userID := range usersToNotify {
		if s.hub.IsUserOnline(userID) {
			s.hub.BroadcastToUser(userID, "ticket_update", updateData)
		}
	}

	log.Printf(
		"Pushed ticket update: ticket_id=%s recipient_count=%s",
		safeLogUint(ticket.ID),
		safeLogInt64(int64(len(usersToNotify))),
	)
	return nil
}

// GetOnlineUsers returns the list of currently online users
func (s *NotificationWebSocketService) GetOnlineUsers() []uint {
	return s.hub.GetConnectedUsers()
}

// GetOnlineUserCount returns the number of currently connected users
func (s *NotificationWebSocketService) GetOnlineUserCount() int {
	return s.hub.GetClientCount()
}

// IsUserOnline checks if a specific user is currently online
func (s *NotificationWebSocketService) IsUserOnline(userID uint) bool {
	return s.hub.IsUserOnline(userID)
}

// PushUserStatusUpdate notifies about user status changes (online/offline)
func (s *NotificationWebSocketService) PushUserStatusUpdate(ctx context.Context, userID uint, status string) error {
	// Send to all admin users about user status change
	statusData := map[string]interface{}{
		"user_id":   userID,
		"status":    status,
		"timestamp": time.Now().Unix(),
	}

	// For now, broadcast to all users
	// In a real implementation, you might want to only notify admins or relevant users
	s.hub.BroadcastToAll("user_status", statusData)

	log.Printf(
		"Pushed user status update: user_id=%s status=%s",
		safeLogUint(userID),
		observability.SafeLogValue(status),
	)
	return nil
}

// SendWelcomeMessage sends a welcome message to a newly connected user
func (s *NotificationWebSocketService) SendWelcomeMessage(ctx context.Context, userID uint) error {
	if !s.hub.IsUserOnline(userID) {
		return nil
	}

	s.hub.BroadcastToUser(userID, "welcome", map[string]interface{}{
		"message":   "欢迎使用实时通知系统！",
		"timestamp": time.Now().Unix(),
		"features": []string{
			"实时通知推送",
			"工单状态更新",
			"系统公告",
			"在线状态显示",
		},
	})

	log.Printf("Sent welcome message: user_id=%s", safeLogUint(userID))
	return nil
}
