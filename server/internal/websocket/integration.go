package websocket

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

// Global WebSocket notification service instance
var GlobalNotificationService *NotificationWebSocketService

// NotificationReadHandler handles notification read request and returns unread count.
type NotificationReadHandler func(
	ctx context.Context,
	scope models.ProjectScope,
	userID uint,
	notificationID uint,
) (int64, error)

// Global notification read handler instance.
var GlobalNotificationReadHandler NotificationReadHandler

// SetGlobalNotificationService sets the global WebSocket notification service
func SetGlobalNotificationService(service *NotificationWebSocketService) {
	GlobalNotificationService = service
}

// SetNotificationReadHandler sets the global notification read handler.
func SetNotificationReadHandler(handler NotificationReadHandler) {
	GlobalNotificationReadHandler = handler
}

// ProjectMembershipRevokedHook must be called after a membership revocation
// commits. It actively closes idle sockets and invalidates every queued frame
// for that user-project binding.
func ProjectMembershipRevokedHook(
	hub *Hub,
	scope models.ProjectScope,
	userID uint,
) error {
	if hub == nil {
		return errors.New("WebSocket hub is required")
	}
	if err := scope.Validate(); err != nil {
		return fmt.Errorf("WebSocket membership revocation scope: %w", err)
	}
	if userID == 0 {
		return errors.New("WebSocket membership revocation user is required")
	}
	hub.evictUserScope(scope, userID)
	return nil
}

// UserAccessRevokedHook must be called after a user is deactivated or deleted.
func UserAccessRevokedHook(hub *Hub, userID uint) error {
	if hub == nil {
		return errors.New("WebSocket hub is required")
	}
	if userID == 0 {
		return errors.New("WebSocket user revocation requires a user")
	}
	hub.evictMatching(func(client *Client) bool {
		return client.UserID == userID
	})
	return nil
}

// ProjectAccessRevokedHook must be called after a project is archived,
// deactivated, or deleted.
func ProjectAccessRevokedHook(
	hub *Hub,
	scope models.ProjectScope,
) error {
	if hub == nil {
		return errors.New("WebSocket hub is required")
	}
	if err := scope.Validate(); err != nil {
		return fmt.Errorf("WebSocket project revocation scope: %w", err)
	}
	hub.evictMatching(func(client *Client) bool {
		return client.scope == scope
	})
	return nil
}

// NotificationCreatedHook is called when a new notification is created
func NotificationCreatedHook(ctx context.Context, notification *models.Notification) {
	if notification == nil {
		log.Print("Rejected nil notification WebSocket hook")
		return
	}
	if GlobalNotificationService == nil {
		log.Printf("WebSocket service not initialized, skipping real-time push for notification %d", notification.ID)
		return
	}

	// Push the notification via WebSocket if the channel supports it
	if notification.Channel == models.NotificationChannelWebSocket || notification.Channel == models.NotificationChannelInApp {
		err := GlobalNotificationService.PushNotification(ctx, notification)
		if err != nil {
			log.Printf("Failed to push notification %d via WebSocket: %v", notification.ID, err)
		}
	}
}

// MarkNotificationAsReadHook is called when a notification is marked as read via WebSocket.
func MarkNotificationAsReadHook(
	ctx context.Context,
	scope models.ProjectScope,
	userID uint,
	notificationID uint,
) error {
	if err := scope.Validate(); err != nil {
		return fmt.Errorf("notification read project scope: %w", err)
	}
	if userID == 0 || notificationID == 0 {
		return fmt.Errorf("notification read user and notification are required")
	}
	if GlobalNotificationReadHandler == nil {
		return fmt.Errorf("notification read handler not initialized")
	}

	unreadCount, err := GlobalNotificationReadHandler(
		ctx,
		scope,
		userID,
		notificationID,
	)
	if err != nil {
		return err
	}

	return NotificationMarkedAsReadHook(ctx, scope, userID, unreadCount)
}

// NotificationMarkedAsReadHook pushes unread count to user after notification is marked as read.
func NotificationMarkedAsReadHook(
	ctx context.Context,
	scope models.ProjectScope,
	userID uint,
	unreadCount int64,
) error {
	if err := scope.Validate(); err != nil {
		return fmt.Errorf("notification unread-count project scope: %w", err)
	}
	if GlobalNotificationService == nil {
		return nil
	}

	if err := GlobalNotificationService.PushUnreadCount(
		ctx,
		scope,
		userID,
		unreadCount,
	); err != nil {
		log.Printf("Failed to push unread count for user %d: %v", userID, err)
		return err
	}
	return nil
}

// NotificationAllMarkedAsReadHook is called when all notifications are marked as read
func NotificationAllMarkedAsReadHook(
	ctx context.Context,
	scope models.ProjectScope,
	userID uint,
) error {
	if err := scope.Validate(); err != nil {
		return fmt.Errorf("notification unread-count project scope: %w", err)
	}
	if GlobalNotificationService == nil {
		return nil
	}

	// Push unread count as 0
	return GlobalNotificationService.PushUnreadCount(ctx, scope, userID, 0)
}
