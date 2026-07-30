package websocket

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
)

var (
	websocketTestScopeA = models.ProjectScope{
		OrganizationID: 7,
		ProjectID:      70,
	}
	websocketTestScopeB = models.ProjectScope{
		OrganizationID: 7,
		ProjectID:      71,
	}
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
	hub := newAuthorizedWebSocketTestHub()
	client := newWebSocketTestClient(hub, 101, websocketTestScopeA, 1)
	hub.clients[client] = true

	GlobalNotificationService = NewNotificationWebSocketService(hub)
	t.Cleanup(func() {
		GlobalNotificationService = nil
	})

	if err := NotificationMarkedAsReadHook(
		context.Background(),
		websocketTestScopeA,
		101,
		5,
	); err != nil {
		t.Fatal(err)
	}

	select {
	case msg := <-client.send:
		if got := parseUnreadCountMessage(t, msg.payload); got != 5 {
			t.Fatalf("expected unread count 5, got %d", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected unread_count message to be pushed")
	}
}

func TestNotificationPushesOnlyToSameProjectAndUser(t *testing.T) {
	hub := newAuthorizedWebSocketTestHub()
	projectAClient := newWebSocketTestClient(
		hub,
		101,
		websocketTestScopeA,
		2,
	)
	projectBClient := newWebSocketTestClient(
		hub,
		101,
		websocketTestScopeB,
		2,
	)
	otherUserClient := newWebSocketTestClient(
		hub,
		102,
		websocketTestScopeA,
		2,
	)
	hub.clients[projectAClient] = true
	hub.clients[projectBClient] = true
	hub.clients[otherUserClient] = true
	service := NewNotificationWebSocketService(hub)

	if err := service.PushNotification(
		context.Background(),
		&models.Notification{
			ID:             55,
			OrganizationID: websocketTestScopeA.OrganizationID,
			ProjectID:      websocketTestScopeA.ProjectID,
			RecipientID:    101,
			Type:           models.NotificationTypeTicketAssigned,
			Title:          "Project A",
			Content:        "Scoped notification",
			Priority:       models.NotificationPriorityNormal,
			Channel:        models.NotificationChannelWebSocket,
		},
	); err != nil {
		t.Fatal(err)
	}
	select {
	case <-projectAClient.send:
	default:
		t.Fatal("same-project user did not receive notification")
	}
	select {
	case <-projectBClient.send:
		t.Fatal("notification leaked to same user in another project")
	default:
	}
	select {
	case <-otherUserClient.send:
		t.Fatal("notification leaked to another user in the same project")
	default:
	}

	if err := service.PushUnreadCount(
		context.Background(),
		websocketTestScopeB,
		101,
		3,
	); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-projectBClient.send:
		if got := parseUnreadCountMessage(t, message.payload); got != 3 {
			t.Fatalf("project B unread count = %d", got)
		}
	default:
		t.Fatal("same-project user did not receive unread count")
	}
	select {
	case <-projectAClient.send:
		t.Fatal("unread count leaked to another project")
	default:
	}
}

func TestHubRunClosesClientsAndReturnsOnContextCancellation(t *testing.T) {
	hub := newAuthorizedWebSocketTestHub()
	client := newWebSocketTestClient(hub, 100, websocketTestScopeA, 1)
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
	hub := newAuthorizedWebSocketTestHub()
	client := newWebSocketTestClient(hub, 202, websocketTestScopeA, 2)
	hub.clients[client] = true

	GlobalNotificationService = NewNotificationWebSocketService(hub)
	t.Cleanup(func() {
		GlobalNotificationService = nil
		SetNotificationReadHandler(nil)
	})

	var called bool
	SetNotificationReadHandler(func(
		ctx context.Context,
		scope models.ProjectScope,
		userID uint,
		notificationID uint,
	) (int64, error) {
		called = true
		if scope != websocketTestScopeA {
			t.Fatalf(
				"expected scope %+v, got %+v",
				websocketTestScopeA,
				scope,
			)
		}
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
		if got := parseUnreadCountMessage(t, msg.payload); got != 4 {
			t.Fatalf("expected unread count 4, got %d", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected unread_count message to be pushed")
	}
}

func TestClientHandleMarkReadRejectsInvalidNumericIDs(t *testing.T) {
	client := newWebSocketTestClient(
		NewHub(),
		303,
		websocketTestScopeA,
		1,
	)
	t.Cleanup(func() {
		SetNotificationReadHandler(nil)
	})

	calls := 0
	SetNotificationReadHandler(func(
		context.Context,
		models.ProjectScope,
		uint,
		uint,
	) (int64, error) {
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

func TestClientHandleMarkReadRejectsMissingProjectScope(t *testing.T) {
	hub := NewHub()
	client := newWebSocketTestClient(
		hub,
		303,
		models.ProjectScope{},
		1,
	)
	calls := 0
	SetNotificationReadHandler(func(
		context.Context,
		models.ProjectScope,
		uint,
		uint,
	) (int64, error) {
		calls++
		return 0, nil
	})
	t.Cleanup(func() {
		SetNotificationReadHandler(nil)
	})

	client.handleMarkRead(map[string]interface{}{
		"notification_id": float64(88),
	})
	if calls != 0 {
		t.Fatal("unscoped mark-read reached the persistence handler")
	}
	if hub.registerClient(client) {
		t.Fatal("hub registered a client without project scope")
	}
}

func TestServeWSRejectsMissingTrustedProjectScopeBeforeUpgrade(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(
		http.MethodGet,
		"/api/v2/projects/TEST/ws",
		nil,
	)
	ginContext.Set("user_id", uint(404))

	ServeWS(NewHub(), ginContext, models.ProjectScope{})

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf(
			"ServeWS missing-scope status = %d, want %d",
			recorder.Code,
			http.StatusInternalServerError,
		)
	}
	var response map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["error"] != "invalid_project_context" {
		t.Fatalf("ServeWS missing-scope response = %#v", response)
	}
}

func newWebSocketTestClient(
	hub *Hub,
	userID uint,
	scope models.ProjectScope,
	sendCapacity int,
) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		hub:     hub,
		send:    make(chan outboundMessage, sendCapacity),
		receive: make(chan []byte, 1),
		UserID:  userID,
		scope:   scope,
		ctx:     ctx,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
}

type allowFanoutAuthorizer struct{}

func (allowFanoutAuthorizer) AuthorizeFanout(
	context.Context,
	models.ProjectScope,
	uint,
) error {
	return nil
}

func newAuthorizedWebSocketTestHub() *Hub {
	return NewHub(allowFanoutAuthorizer{})
}
