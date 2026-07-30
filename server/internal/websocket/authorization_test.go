package websocket

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	gorilla "github.com/gorilla/websocket"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestHubBroadcastFailsClosedWithoutAuthorizer(t *testing.T) {
	hub := NewHub()
	client := newWebSocketTestClient(hub, 101, websocketTestScopeA, 1)
	hub.clients[client] = true

	err := hub.BroadcastToUser(
		context.Background(),
		websocketTestScopeA,
		101,
		"notification",
		map[string]any{"sequence": 1},
	)
	if !errors.Is(err, ErrFanoutAuthorizationDenied) {
		t.Fatalf("broadcast error = %v, want authorization denial", err)
	}
	if hub.IsUserOnline(websocketTestScopeA, 101) {
		t.Fatal("client remained registered without a fan-out authorizer")
	}
	select {
	case <-client.done:
	default:
		t.Fatal("client connection was not closed after fail-closed denial")
	}
}

func TestHubBroadcastRevalidatesProjectAccessAndEvictsRevokedClients(
	t *testing.T,
) {
	tests := []struct {
		name   string
		revoke func(*gorm.DB) error
	}{
		{
			name: "membership revoked",
			revoke: func(db *gorm.DB) error {
				return db.Exec(`
					UPDATE project_memberships
					SET is_active = false
					WHERE project_id = ? AND user_id = ?
				`, websocketTestScopeA.ProjectID, 101).Error
			},
		},
		{
			name: "user deactivated",
			revoke: func(db *gorm.DB) error {
				return db.Exec(
					"UPDATE users SET status = ? WHERE id = ?",
					models.UserStatusInactive,
					101,
				).Error
			},
		},
		{
			name: "project deactivated",
			revoke: func(db *gorm.DB) error {
				return db.Exec(
					"UPDATE projects SET status = ? WHERE id = ?",
					models.ProjectStatusArchived,
					websocketTestScopeA.ProjectID,
				).Error
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openFanoutAuthorizationTestDB(t, test.name)
			hub := NewHub(NewDatabaseFanoutAuthorizer(db))
			client := newWebSocketTestClient(
				hub,
				101,
				websocketTestScopeA,
				1,
			)
			hub.clients[client] = true

			if err := hub.BroadcastToUser(
				context.Background(),
				websocketTestScopeA,
				101,
				"notification",
				map[string]any{"sequence": 1},
			); err != nil {
				t.Fatalf("authorized broadcast failed: %v", err)
			}
			select {
			case <-client.send:
			default:
				t.Fatal("authorized client did not receive the first message")
			}

			if err := test.revoke(db); err != nil {
				t.Fatalf("revoke access: %v", err)
			}
			err := hub.BroadcastToUser(
				context.Background(),
				websocketTestScopeA,
				101,
				"notification",
				map[string]any{"sequence": 2},
			)
			if !errors.Is(err, ErrFanoutAuthorizationDenied) {
				t.Fatalf(
					"revoked broadcast error = %v, want authorization denial",
					err,
				)
			}
			if hub.IsUserOnline(websocketTestScopeA, 101) {
				t.Fatal("revoked client remained registered in the hub")
			}
			select {
			case <-client.done:
			default:
				t.Fatal("revoked client connection was not closed")
			}
			select {
			case message := <-client.send:
				t.Fatalf("revoked client received message %s", message.payload)
			default:
			}
		})
	}
}

func TestWritePumpRevalidatesAfterEnqueueAndDropsFrameRevokedWhileBlocked(
	t *testing.T,
) {
	authorizer := &blockedWriteAuthorizer{
		writeAuthorizationStarted: make(chan struct{}),
		releaseWriteAuthorization: make(chan struct{}),
	}
	hub := NewHub(authorizer)
	connection := &recordingWebSocketConnection{}
	client := newWebSocketTestClient(
		hub,
		101,
		websocketTestScopeA,
		1,
	)
	client.conn = connection
	hub.clients[client] = true
	writeReturned := make(chan struct{})
	go func() {
		defer close(writeReturned)
		client.writePump()
	}()

	if err := hub.BroadcastToUser(
		context.Background(),
		websocketTestScopeA,
		101,
		"notification",
		map[string]any{"sequence": 1},
	); err != nil {
		t.Fatalf("enqueue authorized WebSocket frame: %v", err)
	}
	select {
	case <-authorizer.writeAuthorizationStarted:
	case <-time.After(time.Second):
		t.Fatal("writePump did not reach final authorization barrier")
	}

	if err := hub.RevokeProjectMembership(
		websocketTestScopeA,
		101,
	); err != nil {
		t.Fatalf("revoke project membership: %v", err)
	}
	close(authorizer.releaseWriteAuthorization)

	if hub.IsUserOnline(websocketTestScopeA, 101) {
		t.Fatal("revoked blocked writer remained registered")
	}
	if len(client.send) != 0 {
		t.Fatalf("revocation retained %d queued frame(s)", len(client.send))
	}

	select {
	case <-writeReturned:
	case <-time.After(time.Second):
		t.Fatal("revoked writePump did not return")
	}
	if connection.nextWriterCalls.Load() != 0 {
		t.Fatalf(
			"revoked writer called NextWriter %d time(s)",
			connection.nextWriterCalls.Load(),
		)
	}
	if authorizer.calls.Load() != 2 {
		t.Fatalf(
			"authorization calls = %d, want enqueue and final-write checks",
			authorizer.calls.Load(),
		)
	}
}

func TestRealWebSocketBlockedWriterRevocationDoesNotDeliver(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if errors.Is(err, syscall.EPERM) ||
			errors.Is(err, syscall.EACCES) {
			t.Skipf("loopback listener is unavailable in this sandbox: %v", err)
		}
		t.Fatalf("listen for real WebSocket test: %v", err)
	}

	authorizer := &blockedWriteAuthorizer{
		writeAuthorizationStarted: make(chan struct{}),
		releaseWriteAuthorization: make(chan struct{}),
	}
	hub := NewHub(authorizer)
	connected := make(chan *Client, 1)
	writeReturned := make(chan struct{})
	testUpgrader := gorilla.Upgrader{
		CheckOrigin: func(*http.Request) bool { return true },
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			conn, upgradeErr := testUpgrader.Upgrade(
				writer,
				request,
				nil,
			)
			if upgradeErr != nil {
				t.Errorf("upgrade real WebSocket: %v", upgradeErr)
				return
			}
			client, clientErr := NewClient(
				context.WithoutCancel(request.Context()),
				hub,
				conn,
				101,
				websocketTestScopeA,
			)
			if clientErr != nil {
				t.Errorf("create real WebSocket client: %v", clientErr)
				_ = conn.Close()
				return
			}
			hub.mu.Lock()
			hub.clients[client] = true
			hub.mu.Unlock()
			connected <- client
			client.writePump()
			close(writeReturned)
		},
	))
	server.Listener = listener
	server.Start()
	defer server.Close()

	socketURL := "ws" + strings.TrimPrefix(server.URL, "http")
	peer, _, err := gorilla.DefaultDialer.Dial(socketURL, nil)
	if err != nil {
		t.Fatalf("dial real WebSocket: %v", err)
	}
	defer peer.Close()

	select {
	case <-connected:
	case <-time.After(time.Second):
		t.Fatal("real WebSocket client did not connect")
	}
	if err := hub.BroadcastToUser(
		context.Background(),
		websocketTestScopeA,
		101,
		"notification",
		map[string]any{"sequence": 1},
	); err != nil {
		t.Fatalf("enqueue real WebSocket frame: %v", err)
	}
	select {
	case <-authorizer.writeAuthorizationStarted:
	case <-time.After(time.Second):
		t.Fatal("real WebSocket writer did not reach authorization barrier")
	}
	if err := hub.RevokeProjectMembership(
		websocketTestScopeA,
		101,
	); err != nil {
		t.Fatalf("revoke real WebSocket membership: %v", err)
	}
	close(authorizer.releaseWriteAuthorization)

	if err := peer.SetReadDeadline(
		time.Now().Add(500 * time.Millisecond),
	); err != nil {
		t.Fatal(err)
	}
	messageType, payload, readErr := peer.ReadMessage()
	if readErr == nil && messageType == gorilla.TextMessage {
		t.Fatalf("revoked real WebSocket frame reached peer: %s", payload)
	}
	select {
	case <-writeReturned:
	case <-time.After(time.Second):
		t.Fatal("real WebSocket writer did not stop after revocation")
	}
}

func TestAccessRevocationHooksCloseAndDrainIdleConnections(t *testing.T) {
	tests := []struct {
		name       string
		revoke     func(*Hub) error
		targets    func(*Client, *Client, *Client) []*Client
		unaffected func(*Client, *Client, *Client) []*Client
	}{
		{
			name: "membership",
			revoke: func(hub *Hub) error {
				return ProjectMembershipRevokedHook(
					hub,
					websocketTestScopeA,
					101,
				)
			},
			targets: func(
				projectAUser101 *Client,
				_ *Client,
				_ *Client,
			) []*Client {
				return []*Client{projectAUser101}
			},
			unaffected: func(
				_ *Client,
				projectBUser101 *Client,
				projectAUser102 *Client,
			) []*Client {
				return []*Client{projectBUser101, projectAUser102}
			},
		},
		{
			name: "user",
			revoke: func(hub *Hub) error {
				return UserAccessRevokedHook(hub, 101)
			},
			targets: func(
				projectAUser101 *Client,
				projectBUser101 *Client,
				_ *Client,
			) []*Client {
				return []*Client{projectAUser101, projectBUser101}
			},
			unaffected: func(
				_ *Client,
				_ *Client,
				projectAUser102 *Client,
			) []*Client {
				return []*Client{projectAUser102}
			},
		},
		{
			name: "project",
			revoke: func(hub *Hub) error {
				return ProjectAccessRevokedHook(
					hub,
					websocketTestScopeA,
				)
			},
			targets: func(
				projectAUser101 *Client,
				_ *Client,
				projectAUser102 *Client,
			) []*Client {
				return []*Client{projectAUser101, projectAUser102}
			},
			unaffected: func(
				_ *Client,
				projectBUser101 *Client,
				_ *Client,
			) []*Client {
				return []*Client{projectBUser101}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hub := newAuthorizedWebSocketTestHub()
			projectAUser101 := newWebSocketTestClient(
				hub,
				101,
				websocketTestScopeA,
				2,
			)
			projectBUser101 := newWebSocketTestClient(
				hub,
				101,
				websocketTestScopeB,
				2,
			)
			projectAUser102 := newWebSocketTestClient(
				hub,
				102,
				websocketTestScopeA,
				2,
			)
			for _, client := range []*Client{
				projectAUser101,
				projectBUser101,
				projectAUser102,
			} {
				hub.clients[client] = true
				if !client.enqueue([]byte(`{"type":"queued"}`)) {
					t.Fatal("queue idle client frame")
				}
			}
			SetGlobalNotificationService(
				NewNotificationWebSocketService(hub),
			)
			t.Cleanup(func() {
				SetGlobalNotificationService(nil)
				hub.stop()
			})

			if err := test.revoke(hub); err != nil {
				t.Fatalf("invoke %s revocation hook: %v", test.name, err)
			}
			for _, client := range test.targets(
				projectAUser101,
				projectBUser101,
				projectAUser102,
			) {
				select {
				case <-client.done:
				default:
					t.Fatal("target idle connection was not closed")
				}
				if len(client.send) != 0 {
					t.Fatalf(
						"target idle connection retained %d frame(s)",
						len(client.send),
					)
				}
			}
			for _, client := range test.unaffected(
				projectAUser101,
				projectBUser101,
				projectAUser102,
			) {
				select {
				case <-client.done:
					t.Fatal("unaffected connection was closed")
				default:
				}
				if len(client.send) != 1 {
					t.Fatalf(
						"unaffected connection queue length = %d, want 1",
						len(client.send),
					)
				}
			}
		})
	}
}

type blockedWriteAuthorizer struct {
	calls                     atomic.Int32
	writeAuthorizationStarted chan struct{}
	releaseWriteAuthorization chan struct{}
}

func (authorizer *blockedWriteAuthorizer) AuthorizeFanout(
	context.Context,
	models.ProjectScope,
	uint,
) error {
	if authorizer.calls.Add(1) == 2 {
		close(authorizer.writeAuthorizationStarted)
		<-authorizer.releaseWriteAuthorization
	}
	return nil
}

type recordingWebSocketConnection struct {
	nextWriterCalls atomic.Int32
	closed          atomic.Bool
}

func (connection *recordingWebSocketConnection) Close() error {
	connection.closed.Store(true)
	return nil
}

func (connection *recordingWebSocketConnection) NextWriter(
	int,
) (io.WriteCloser, error) {
	connection.nextWriterCalls.Add(1)
	return discardWriteCloser{}, nil
}

func (*recordingWebSocketConnection) ReadMessage() (int, []byte, error) {
	return 0, nil, errors.New("test connection has no inbound messages")
}

func (*recordingWebSocketConnection) SetPongHandler(func(string) error) {}

func (*recordingWebSocketConnection) SetReadDeadline(time.Time) error {
	return nil
}

func (*recordingWebSocketConnection) SetReadLimit(int64) {}

func (*recordingWebSocketConnection) SetWriteDeadline(time.Time) error {
	return nil
}

func (*recordingWebSocketConnection) WriteMessage(int, []byte) error {
	return nil
}

type discardWriteCloser struct{}

func (discardWriteCloser) Write(payload []byte) (int, error) {
	return len(payload), nil
}

func (discardWriteCloser) Close() error {
	return nil
}

func openFanoutAuthorizationTestDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf(
			"file:websocket-fanout-%s?mode=memory&cache=shared",
			name,
		)),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY,
			status TEXT NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE projects (
			id INTEGER PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			status TEXT NOT NULL
		)`,
		`CREATE TABLE project_memberships (
			project_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			role TEXT NOT NULL,
			is_active BOOLEAN NOT NULL
		)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create authorization fixture schema: %v", err)
		}
	}
	if err := db.Exec(
		"INSERT INTO users (id, status) VALUES (?, ?)",
		101,
		models.UserStatusActive,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"INSERT INTO projects (id, organization_id, status) VALUES (?, ?, ?)",
		websocketTestScopeA.ProjectID,
		websocketTestScopeA.OrganizationID,
		models.ProjectStatusActive,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		INSERT INTO project_memberships (project_id, user_id, role, is_active)
		VALUES (?, ?, ?, ?)
	`,
		websocketTestScopeA.ProjectID,
		101,
		models.ProjectRoleAgent,
		true,
	).Error; err != nil {
		t.Fatal(err)
	}
	return db
}
