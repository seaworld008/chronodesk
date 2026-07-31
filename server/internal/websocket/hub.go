package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

// Hub maintains the set of active clients and broadcasts messages to the clients.
type Hub struct {
	// Registered clients.
	clients map[*Client]bool

	// Register requests from the clients.
	register chan *Client

	// Unregister requests from clients.
	unregister chan *Client

	// Mutex for thread-safe operations
	mu sync.RWMutex

	authorizer FanoutAuthorizer

	done     chan struct{}
	stopOnce sync.Once
}

// NewHub creates a new WebSocket hub
func NewHub(authorizers ...FanoutAuthorizer) *Hub {
	var authorizer FanoutAuthorizer
	if len(authorizers) == 1 {
		authorizer = authorizers[0]
	}
	return &Hub{
		register:   make(chan *Client),
		unregister: make(chan *Client),
		clients:    make(map[*Client]bool),
		authorizer: authorizer,
		done:       make(chan struct{}),
	}
}

// Run starts the hub
func (h *Hub) Run(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	defer h.stop()
	for {
		select {
		case <-ctx.Done():
			return
		case client := <-h.register:
			if err := validateClientBinding(client); err != nil ||
				client.hub != h {
				if client != nil {
					client.close()
				}
				log.Print("Rejected invalid WebSocket client registration")
				continue
			}
			h.mu.Lock()
			h.clients[client] = true
			clientCount := len(h.clients)
			h.mu.Unlock()
			log.Printf(
				"WebSocket client connected, user: %d, project: %d, total: %d",
				client.UserID,
				client.scope.ProjectID,
				clientCount,
			)

		case client := <-h.unregister:
			h.mu.Lock()
			delete(h.clients, client)
			clientCount := len(h.clients)
			h.mu.Unlock()
			client.close()
			log.Printf(
				"WebSocket client disconnected, user: %d, project: %d, total: %d",
				client.UserID,
				client.scope.ProjectID,
				clientCount,
			)

		}
	}
}

func (h *Hub) stop() {
	h.stopOnce.Do(func() {
		close(h.done)
		h.mu.Lock()
		clients := make([]*Client, 0, len(h.clients))
		for client := range h.clients {
			delete(h.clients, client)
			clients = append(clients, client)
		}
		h.mu.Unlock()
		for _, client := range clients {
			client.close()
		}
	})
}

func (h *Hub) registerClient(client *Client) bool {
	if h == nil || validateClientBinding(client) != nil ||
		client.hub != h {
		if client != nil {
			client.close()
		}
		return false
	}
	select {
	case <-h.done:
		return false
	case h.register <- client:
		return true
	}
}

func (h *Hub) unregisterClient(client *Client) {
	if h == nil || client == nil {
		return
	}
	select {
	case <-h.done:
		client.close()
	case h.unregister <- client:
	}
}

// BroadcastToUser sends a message only to connections for the same project
// and user. Project scope is server-owned control data, never a message field.
func (h *Hub) BroadcastToUser(
	ctx context.Context,
	scope models.ProjectScope,
	userID uint,
	messageType string,
	data interface{},
) error {
	if h == nil {
		return errors.New("WebSocket hub is required")
	}
	if err := scope.Validate(); err != nil {
		return fmt.Errorf("WebSocket broadcast project scope: %w", err)
	}
	if userID == 0 {
		return errors.New("WebSocket broadcast user is required")
	}
	select {
	case <-h.done:
		return nil
	default:
	}
	if h.authorizer == nil {
		h.evictUserScope(scope, userID)
		return fmt.Errorf(
			"%w: authorizer is unavailable",
			ErrFanoutAuthorizationDenied,
		)
	}
	if err := h.authorizer.AuthorizeFanout(ctx, scope, userID); err != nil {
		h.evictUserScope(scope, userID)
		return fmt.Errorf("authorize WebSocket fan-out: %w", err)
	}
	message := map[string]interface{}{
		"type":      messageType,
		"data":      data,
		"timestamp": getTimestamp(),
	}

	messageBytes, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshal WebSocket message: %w", err)
	}

	h.mu.RLock()
	clients := make([]*Client, 0)
	for client := range h.clients {
		if client.UserID == userID && client.scope == scope {
			clients = append(clients, client)
		}
	}
	h.mu.RUnlock()
	for _, client := range clients {
		if !client.enqueue(messageBytes) {
			h.evictClient(client)
		}
	}
	return nil
}

func (h *Hub) evictUserScope(scope models.ProjectScope, userID uint) {
	h.evictMatching(func(client *Client) bool {
		return client.UserID == userID && client.scope == scope
	})
}

func (h *Hub) evictClient(client *Client) {
	if h == nil || client == nil {
		return
	}
	h.mu.Lock()
	delete(h.clients, client)
	h.mu.Unlock()
	client.close()
}

func (h *Hub) evictMatching(matches func(*Client) bool) {
	if h == nil || matches == nil {
		return
	}
	h.mu.Lock()
	clients := make([]*Client, 0)
	for client := range h.clients {
		if matches(client) {
			delete(h.clients, client)
			clients = append(clients, client)
		}
	}
	h.mu.Unlock()
	for _, client := range clients {
		client.close()
	}
}

func (h *Hub) authorizeDelivery(
	client *Client,
	authorizationEpoch uint64,
) error {
	if h == nil {
		return fmt.Errorf(
			"%w: hub is unavailable",
			ErrFanoutAuthorizationDenied,
		)
	}
	if h.authorizer == nil {
		return fmt.Errorf(
			"%w: authorizer is unavailable",
			ErrFanoutAuthorizationDenied,
		)
	}
	if err := h.validateDeliveryState(client, authorizationEpoch); err != nil {
		return err
	}
	if err := h.authorizer.AuthorizeFanout(
		client.ctx,
		client.scope,
		client.UserID,
	); err != nil {
		return fmt.Errorf("authorize final WebSocket delivery: %w", err)
	}
	return h.validateDeliveryState(client, authorizationEpoch)
}

// validateDeliveryState performs only process-local checks. writePump repeats
// it while holding Client.deliveryMu; Client.close publishes the revocation,
// interrupts the socket, then waits on that mutex before returning.
func (h *Hub) validateDeliveryState(
	client *Client,
	authorizationEpoch uint64,
) error {
	if h == nil || client == nil || client.hub != h {
		return fmt.Errorf(
			"%w: invalid client binding",
			ErrFanoutAuthorizationDenied,
		)
	}
	if client.closing.Load() {
		return fmt.Errorf(
			"%w: client connection is closing",
			ErrFanoutAuthorizationDenied,
		)
	}
	if client.authorizationEpoch.Load() != authorizationEpoch {
		return fmt.Errorf(
			"%w: stale authorization epoch",
			ErrFanoutAuthorizationDenied,
		)
	}
	if client.ctx == nil {
		return fmt.Errorf(
			"%w: connection context is unavailable",
			ErrFanoutAuthorizationDenied,
		)
	}
	if err := client.ctx.Err(); err != nil {
		return fmt.Errorf(
			"%w: connection context is closed: %v",
			ErrFanoutAuthorizationDenied,
			err,
		)
	}
	select {
	case <-client.done:
		return fmt.Errorf(
			"%w: client connection is closed",
			ErrFanoutAuthorizationDenied,
		)
	default:
		return nil
	}
}

// RevokeProjectMembership actively evicts every idle connection for one
// committed membership revocation. Queued frames are invalidated and drained.
func (h *Hub) RevokeProjectMembership(
	scope models.ProjectScope,
	userID uint,
) error {
	return ProjectMembershipRevokedHook(h, scope, userID)
}

// RevokeUser actively evicts a deactivated or deleted user's connections in
// every project.
func (h *Hub) RevokeUser(userID uint) error {
	return UserAccessRevokedHook(h, userID)
}

// RevokeProject actively evicts every connection for a deactivated or deleted
// project.
func (h *Hub) RevokeProject(scope models.ProjectScope) error {
	return ProjectAccessRevokedHook(h, scope)
}

// IsUserOnline checks whether the user has a connection in this project.
func (h *Hub) IsUserOnline(
	scope models.ProjectScope,
	userID uint,
) bool {
	if h == nil || userID == 0 || scope.Validate() != nil {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()

	for client := range h.clients {
		if client.UserID == userID && client.scope == scope {
			return true
		}
	}

	return false
}

func validateClientBinding(client *Client) error {
	if client == nil {
		return errors.New("WebSocket client is required")
	}
	if client.hub == nil {
		return errors.New("WebSocket client hub is required")
	}
	if client.UserID == 0 {
		return errors.New("WebSocket client user is required")
	}
	if err := client.scope.Validate(); err != nil {
		return fmt.Errorf("WebSocket client project scope: %w", err)
	}
	if client.send == nil || client.receive == nil || client.done == nil ||
		client.ctx == nil || client.cancel == nil {
		return errors.New("WebSocket client channels are required")
	}
	return nil
}

func getTimestamp() int64 {
	return time.Now().Unix()
}
