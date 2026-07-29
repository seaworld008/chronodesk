package websocket

import (
	"context"
	"log"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
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
