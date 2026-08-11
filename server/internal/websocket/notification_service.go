package websocket

import (
	"context"
	"errors"
	"fmt"
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
	if s == nil || s.hub == nil {
		return errors.New("WebSocket notification service is unavailable")
	}
	if notification == nil {
		return errors.New("notification is required")
	}
	scope := models.ProjectScope{
		OrganizationID: notification.OrganizationID,
		ProjectID:      notification.ProjectID,
	}
	if err := scope.Validate(); err != nil {
		return fmt.Errorf("notification project scope: %w", err)
	}
	if notification.RecipientID == 0 {
		return errors.New("notification recipient is required")
	}
	if ctx != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	// Check if user is online
	if !s.hub.IsUserOnline(scope, notification.RecipientID) {
		log.Printf(
			"Notification recipient is offline: user_id=%s delivery=deferred",
			safeLogUint(notification.RecipientID),
		)
		return nil
	}

	// Convert notification to response format
	notificationData := notification.ToResponse()

	// Send the notification via WebSocket
	if err := s.hub.BroadcastToUser(ctx, scope, notification.RecipientID, "notification", map[string]interface{}{
		"id":         notificationData.ID,
		"type":       notificationData.Type,
		"title":      notificationData.Title,
		"content":    notificationData.Content,
		"priority":   notificationData.Priority,
		"created_at": notificationData.CreatedAt,
		"is_read":    notificationData.IsRead,
		"action_url": notificationData.ActionURL,
		"sender":     notificationData.Sender,
	}); err != nil {
		return err
	}

	log.Printf(
		"Pushed WebSocket notification: notification_id=%s user_id=%s",
		safeLogUint(notification.ID),
		safeLogUint(notification.RecipientID),
	)
	return nil
}

// PushUnreadCount sends updated unread count to a specific user
func (s *NotificationWebSocketService) PushUnreadCount(
	ctx context.Context,
	scope models.ProjectScope,
	userID uint,
	count int64,
) error {
	if s == nil || s.hub == nil {
		return errors.New("WebSocket notification service is unavailable")
	}
	if err := scope.Validate(); err != nil {
		return fmt.Errorf("unread-count project scope: %w", err)
	}
	if userID == 0 {
		return errors.New("unread-count user is required")
	}
	if count < 0 {
		return errors.New("unread count cannot be negative")
	}
	if ctx != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	if !s.hub.IsUserOnline(scope, userID) {
		return nil
	}

	if err := s.hub.BroadcastToUser(ctx, scope, userID, "unread_count", map[string]interface{}{
		"count":     count,
		"timestamp": time.Now().Unix(),
	}); err != nil {
		return err
	}

	log.Printf(
		"Pushed unread count: count=%s user_id=%s",
		safeLogInt64(count),
		safeLogUint(userID),
	)
	return nil
}
