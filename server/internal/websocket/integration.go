package websocket

import (
	"context"
	"fmt"
	"log"

	"gongdan-system/internal/models"
)

// Global WebSocket notification service instance
var GlobalNotificationService *NotificationWebSocketService

// NotificationReadHandler handles notification read request and returns unread count.
type NotificationReadHandler func(ctx context.Context, userID uint, notificationID uint) (int64, error)

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

// NotificationCreatedHook is called when a new notification is created
func NotificationCreatedHook(ctx context.Context, notification *models.Notification) {
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
func MarkNotificationAsReadHook(ctx context.Context, userID uint, notificationID uint) error {
	if GlobalNotificationReadHandler == nil {
		return fmt.Errorf("notification read handler not initialized")
	}

	unreadCount, err := GlobalNotificationReadHandler(ctx, userID, notificationID)
	if err != nil {
		return err
	}

	NotificationMarkedAsReadHook(ctx, userID, unreadCount)
	return nil
}

// NotificationMarkedAsReadHook pushes unread count to user after notification is marked as read.
func NotificationMarkedAsReadHook(ctx context.Context, userID uint, unreadCount int64) {
	if GlobalNotificationService == nil {
		return
	}

	if err := GlobalNotificationService.PushUnreadCount(ctx, userID, unreadCount); err != nil {
		log.Printf("Failed to push unread count for user %d: %v", userID, err)
	}
}

// NotificationAllMarkedAsReadHook is called when all notifications are marked as read
func NotificationAllMarkedAsReadHook(ctx context.Context, userID uint) {
	if GlobalNotificationService == nil {
		return
	}

	// Push unread count as 0
	GlobalNotificationService.PushUnreadCount(ctx, userID, 0)
}

// TicketUpdatedHook is called when a ticket is updated
func TicketUpdatedHook(ctx context.Context, ticket *models.Ticket, updateType string) {
	if GlobalNotificationService == nil {
		return
	}

	err := GlobalNotificationService.PushTicketUpdate(ctx, ticket, updateType)
	if err != nil {
		log.Printf("Failed to push ticket update via WebSocket: %v", err)
	}
}
