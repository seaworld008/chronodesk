package websocket

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"
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
			h.mu.Lock()
			h.clients[client] = true
			clientCount := len(h.clients)
			h.mu.Unlock()
			log.Printf("WebSocket client connected, user: %d, total: %d", client.UserID, clientCount)

		case client := <-h.unregister:
			h.mu.Lock()
			delete(h.clients, client)
			clientCount := len(h.clients)
			h.mu.Unlock()
			client.close()
			log.Printf("WebSocket client disconnected, user: %d, total: %d", client.UserID, clientCount)

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
	select {
	case <-h.done:
		return false
	case h.register <- client:
		return true
	}
}

func (h *Hub) unregisterClient(client *Client) {
	select {
	case <-h.done:
		client.close()
	case h.unregister <- client:
	}
}

// BroadcastToUser sends a message to a specific user
func (h *Hub) BroadcastToUser(userID uint, messageType string, data interface{}) {
	select {
	case <-h.done:
		return
	default:
	}
	message := map[string]interface{}{
		"type":      messageType,
		"data":      data,
		"timestamp": getTimestamp(),
	}

	messageBytes, err := json.Marshal(message)
	if err != nil {
		log.Printf("Error marshaling message: %v", err)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	for client := range h.clients {
		if client.UserID == userID {
			select {
			case client.send <- messageBytes:
			default:
				delete(h.clients, client)
				client.close()
			}
		}
	}
}

// IsUserOnline checks if a user is currently connected
func (h *Hub) IsUserOnline(userID uint) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for client := range h.clients {
		if client.UserID == userID {
			return true
		}
	}

	return false
}
func getTimestamp() int64 {
	return time.Now().Unix()
}
