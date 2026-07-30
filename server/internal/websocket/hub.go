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

	done     chan struct{}
	stopOnce sync.Once
}

// NewHub creates a new WebSocket hub
func NewHub() *Hub {
	return &Hub{
		register:   make(chan *Client),
		unregister: make(chan *Client),
		clients:    make(map[*Client]bool),
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
		defer h.mu.Unlock()
		for client := range h.clients {
			delete(h.clients, client)
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
	message := map[string]interface{}{
		"type":      messageType,
		"data":      data,
		"timestamp": getTimestamp(),
	}

	messageBytes, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshal WebSocket message: %w", err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	for client := range h.clients {
		if client.UserID == userID && client.scope == scope {
			select {
			case client.send <- messageBytes:
			default:
				delete(h.clients, client)
				client.close()
			}
		}
	}
	return nil
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
	if client.send == nil || client.done == nil {
		return errors.New("WebSocket client channels are required")
	}
	return nil
}

func getTimestamp() int64 {
	return time.Now().Unix()
}
