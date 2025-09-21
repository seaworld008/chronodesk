package websocket

import (
	"encoding/json"
	"log"
	"sync"
	"time"
)

// Hub maintains the set of active clients and broadcasts messages to the clients.
type Hub struct {
	// Registered clients.
	clients map[*Client]bool

	// Inbound messages from the clients.
	broadcast chan []byte

	// Register requests from the clients.
	register chan *Client

	// Unregister requests from clients.
	unregister chan *Client

	// Mutex for thread-safe operations
	mu sync.RWMutex
}

// NewHub creates a new WebSocket hub
func NewHub() *Hub {
	return &Hub{
		broadcast:  make(chan []byte),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		clients:    make(map[*Client]bool),
	}
}

// Run starts the hub
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			log.Printf("WebSocket client connected, user: %d, total: %d", client.UserID, len(h.clients))

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
				log.Printf("WebSocket client disconnected, user: %d, total: %d", client.UserID, len(h.clients))
			}
			h.mu.Unlock()

		case message := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// BroadcastToUser sends a message to a specific user
func (h *Hub) BroadcastToUser(userID uint, messageType string, data interface{}) {
	message := map[string]interface{}{
		"type": messageType,
		"data": data,
		"timestamp": getTimestamp(),
	}

	messageBytes, err := json.Marshal(message)
	if err != nil {
		log.Printf("Error marshaling message: %v", err)
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	
	for client := range h.clients {
		if client.UserID == userID {
			select {
			case client.send <- messageBytes:
			default:
				close(client.send)
				delete(h.clients, client)
			}
		}
	}
}

// BroadcastToAll sends a message to all connected clients
func (h *Hub) BroadcastToAll(messageType string, data interface{}) {
	message := map[string]interface{}{
		"type": messageType,
		"data": data,
		"timestamp": getTimestamp(),
	}

	messageBytes, err := json.Marshal(message)
	if err != nil {
		log.Printf("Error marshaling message: %v", err)
		return
	}

	h.broadcast <- messageBytes
}

// GetConnectedUsers returns the list of connected user IDs
func (h *Hub) GetConnectedUsers() []uint {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	userSet := make(map[uint]bool)
	for client := range h.clients {
		userSet[client.UserID] = true
	}
	
	users := make([]uint, 0, len(userSet))
	for userID := range userSet {
		users = append(users, userID)
	}
	
	return users
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

// GetClientCount returns the total number of connected clients
func (h *Hub) GetClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func getTimestamp() int64 {
	return time.Now().Unix()
}