package websocket

import (
	"context"
	"log"

	"gongdan-system/internal/models"
)

// Global WebSocket notification service instance
var GlobalNotificationService *NotificationWebSocketService

// SetGlobalNotificationService sets the global WebSocket notification service
func SetGlobalNotificationService(service *NotificationWebSocketService) {
	GlobalNotificationService = service
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

// NotificationMarkedAsReadHook is called when a notification is marked as read
func NotificationMarkedAsReadHook(ctx context.Context, userID uint, notificationID uint) {
	if GlobalNotificationService == nil {
		return
	}

	// Update unread count for the user
	// TODO: Get actual unread count from service
	GlobalNotificationService.PushUnreadCount(ctx, userID, 0)
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