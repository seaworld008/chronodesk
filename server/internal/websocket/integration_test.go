package websocket

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"testing"
	"time"
)

func parseUnreadCountMessage(t *testing.T, raw []byte) int64 {
	t.Helper()

	var payload map[string]interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("failed to unmarshal websocket payload: %v", err)
	}

	msgType, _ := payload["type"].(string)
	if msgType != "unread_count" {
		t.Fatalf("expected unread_count message, got %q", msgType)
	}

	data, ok := payload["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected payload.data object, got %#v", payload["data"])
	}

	countFloat, ok := data["count"].(float64)
	if !ok {
		t.Fatalf("expected numeric count, got %#v", data["count"])
	}

	return int64(countFloat)
}

func TestNotificationMarkedAsReadHook_PushesProvidedUnreadCount(t *testing.T) {
	hub := NewHub()
	client := &Client{
		hub:    hub,
		send:   make(chan []byte, 1),
		UserID: 101,
	}
	hub.clients[client] = true

	GlobalNotificationService = NewNotificationWebSocketService(hub)
	t.Cleanup(func() {
		GlobalNotificationService = nil
	})

	NotificationMarkedAsReadHook(context.Background(), 101, 5)

	select {
	case msg := <-client.send:
		if got := parseUnreadCountMessage(t, msg); got != 5 {
			t.Fatalf("expected unread count 5, got %d", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected unread_count message to be pushed")
	}
}

func TestHubRunClosesClientsAndReturnsOnContextCancellation(t *testing.T) {
	hub := NewHub()
	client := &Client{
		hub:  hub,
		send: make(chan []byte, 1),
		done: make(chan struct{}),
	}
	hub.clients[client] = true

	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan struct{})
	go func() {
		hub.Run(ctx)
		close(returned)
	}()
	cancel()

	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("WebSocket hub did not stop after context cancellation")
	}
	select {
	case <-client.done:
	default:
		t.Fatal("WebSocket hub shutdown did not close connected client")
	}
	if len(hub.clients) != 0 {
		t.Fatalf("WebSocket hub retained %d clients after shutdown", len(hub.clients))
	}
}

func TestClientHandleMarkRead_InvokesReadHandlerAndPushesUnreadCount(t *testing.T) {
	hub := NewHub()
	client := &Client{
		hub:    hub,
		send:   make(chan []byte, 2),
		UserID: 202,
	}
	hub.clients[client] = true

	GlobalNotificationService = NewNotificationWebSocketService(hub)
	t.Cleanup(func() {
		GlobalNotificationService = nil
		SetNotificationReadHandler(nil)
	})

	var called bool
	SetNotificationReadHandler(func(ctx context.Context, userID uint, notificationID uint) (int64, error) {
		called = true
		if userID != 202 {
			t.Fatalf("expected userID 202, got %d", userID)
		}
		if notificationID != 88 {
			t.Fatalf("expected notificationID 88, got %d", notificationID)
		}
		return 4, nil
	})

	client.handleMarkRead(map[string]interface{}{
		"notification_id": float64(88),
	})

	if !called {
		t.Fatalf("expected notification read handler to be called")
	}

	select {
	case msg := <-client.send:
		if got := parseUnreadCountMessage(t, msg); got != 4 {
			t.Fatalf("expected unread count 4, got %d", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected unread_count message to be pushed")
	}
}

func TestClientHandleMarkReadRejectsInvalidNumericIDs(t *testing.T) {
	client := &Client{UserID: 303}
	t.Cleanup(func() {
		SetNotificationReadHandler(nil)
	})

	calls := 0
	SetNotificationReadHandler(func(context.Context, uint, uint) (int64, error) {
		calls++
		return 0, nil
	})

	values := []float64{
		0,
		-1,
		42.5,
		math.NaN(),
		math.Inf(1),
		maxExactJSONInteger + 1,
		math.Ldexp(1, strconv.IntSize),
	}
	for _, value := range values {
		client.handleMarkRead(map[string]interface{}{"notification_id": value})
	}
	if calls != 0 {
		t.Fatalf("invalid notification IDs invoked the persistence hook %d time(s)", calls)
	}
}
