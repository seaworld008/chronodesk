package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/observability"
	"github.com/seaworld008/chronodesk/server/internal/safeconv"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 512

	// JSON numbers are decoded through float64. Values above 2^53-1 no longer
	// preserve an exact integer identity and must not be used as database IDs.
	maxExactJSONInteger = float64(1<<53 - 1)
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return originAllowed(r.Header.Get("Origin"), wsAllowedOrigins, wsAllowAllOrigins)
	},
}

var wsAllowedOrigins []string
var wsAllowAllOrigins bool

// ConfigureOriginCheck sets websocket origin whitelist behavior.
func ConfigureOriginCheck(allowed []string, allowAll bool) {
	wsAllowedOrigins = allowed
	wsAllowAllOrigins = allowAll
}

func originAllowed(origin string, allowed []string, allowAll bool) bool {
	if allowAll {
		return true
	}
	if origin == "" {
		return false
	}
	for _, allowedOrigin := range allowed {
		if allowedOrigin == "*" || allowedOrigin == origin {
			return true
		}
		if matchWildcard(allowedOrigin, origin) {
			return true
		}
	}
	return false
}

func matchWildcard(pattern, value string) bool {
	if pattern == "*" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return pattern == value
	}

	parts := strings.Split(pattern, "*")
	if len(parts) == 0 {
		return false
	}

	if !strings.HasPrefix(value, parts[0]) {
		return false
	}
	if !strings.HasSuffix(value, parts[len(parts)-1]) {
		return false
	}

	index := len(parts[0])
	for i := 1; i < len(parts)-1; i++ {
		part := parts[i]
		if part == "" {
			continue
		}
		next := strings.Index(value[index:], part)
		if next == -1 {
			return false
		}
		index += next + len(part)
	}

	return true
}

// Client is a middleman between the websocket connection and the hub.
type Client struct {
	// The websocket connection.
	conn *websocket.Conn

	// Buffered channel of outbound messages.
	send chan []byte

	// User ID associated with this connection
	UserID uint

	// scope is the trusted, server-resolved project boundary for the lifetime
	// of this connection. It is deliberately private so external callers
	// cannot mutate a registered client into another project.
	scope models.ProjectScope

	// Hub reference
	hub *Hub

	done      chan struct{}
	closeOnce sync.Once
}

// NewClient creates a new WebSocket client
func NewClient(
	hub *Hub,
	conn *websocket.Conn,
	userID uint,
	scope models.ProjectScope,
) (*Client, error) {
	if hub == nil {
		return nil, errors.New("WebSocket hub is required")
	}
	if conn == nil {
		return nil, errors.New("WebSocket connection is required")
	}
	if userID == 0 {
		return nil, errors.New("WebSocket user is required")
	}
	if err := scope.Validate(); err != nil {
		return nil, errors.New("trusted WebSocket project scope is required")
	}
	return &Client{
		hub:    hub,
		conn:   conn,
		send:   make(chan []byte, 256),
		UserID: userID,
		scope:  scope,
		done:   make(chan struct{}),
	}, nil
}

// ProjectScope returns the immutable project binding for this connection.
func (c *Client) ProjectScope() models.ProjectScope {
	if c == nil {
		return models.ProjectScope{}
	}
	return c.scope
}

func (c *Client) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		if c.conn != nil {
			_ = c.conn.Close()
		}
	})
}

// readPump pumps messages from the websocket connection to the hub.
//
// The application runs readPump in a per-connection goroutine. The application
// ensures that there is at most one reader on a connection by executing all
// reads from this goroutine.
func (c *Client) readPump() {
	defer func() {
		c.hub.unregisterClient(c)
		c.close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Print("WebSocket connection closed unexpectedly")
			}
			break
		}

		// Handle incoming messages from client
		c.handleMessage(message)
	}
}

// writePump pumps messages from the hub to the websocket connection.
//
// A goroutine running writePump is started for each connection. The
// application ensures that there is at most one writer to a connection by
// executing all writes from this goroutine.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.close()
	}()

	for {
		select {
		case <-c.done:
			return
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel.
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Add queued messages to the current message.
			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write([]byte{'\n'})
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleMessage processes incoming messages from the client
func (c *Client) handleMessage(message []byte) {
	var msg map[string]interface{}
	if err := json.Unmarshal(message, &msg); err != nil {
		log.Print("Rejected malformed WebSocket client message")
		return
	}

	msgType, ok := msg["type"].(string)
	if !ok {
		log.Printf("Invalid message type from client user_id=%s", safeLogUint(c.UserID))
		return
	}

	switch msgType {
	case "ping":
		// Respond to client ping
		c.sendPong()
	case "mark_read":
		// Handle mark notification as read
		c.handleMarkRead(msg)
	default:
		log.Printf(
			"Unknown WebSocket message type=%s user_id=%s",
			observability.SafeLogValue(msgType),
			safeLogUint(c.UserID),
		)
	}
}

// sendPong sends a pong response to the client
func (c *Client) sendPong() {
	response := map[string]interface{}{
		"type":      "pong",
		"timestamp": time.Now().Unix(),
	}

	responseBytes, _ := json.Marshal(response)
	select {
	case <-c.done:
		return
	case c.send <- responseBytes:
	default:
		c.close()
	}
}

// handleMarkRead handles marking notifications as read via WebSocket
func (c *Client) handleMarkRead(msg map[string]interface{}) {
	// Extract notification ID from message
	if idFloat, ok := msg["notification_id"].(float64); ok {
		if idFloat <= 0 ||
			math.Trunc(idFloat) != idFloat ||
			idFloat > maxExactJSONInteger ||
			idFloat >= math.Ldexp(1, strconv.IntSize) {
			log.Printf(
				"Rejected invalid WebSocket notification ID: user_id=%s",
				safeLogUint(c.UserID),
			)
			return
		}
		notificationID, err := safeconv.PositiveUint(uint64(idFloat))
		if err != nil {
			log.Printf(
				"Rejected out-of-range WebSocket notification ID: user_id=%s",
				safeLogUint(c.UserID),
			)
			return
		}
		log.Printf(
			"WebSocket mark-read requested: user_id=%s notification_id=%s",
			safeLogUint(c.UserID),
			safeLogUint(notificationID),
		)
		if err := MarkNotificationAsReadHook(
			context.Background(),
			c.scope,
			c.UserID,
			notificationID,
		); err != nil {
			log.Printf(
				"WebSocket mark-read failed: user_id=%s notification_id=%s reason=persistence_error",
				safeLogUint(c.UserID),
				safeLogUint(notificationID),
			)
		}
	}
}

func safeLogUint(value uint) string {
	return observability.SafeLogValue(strconv.FormatUint(uint64(value), 10))
}

func safeLogInt64(value int64) string {
	return observability.SafeLogValue(strconv.FormatInt(value, 10))
}

// ServeWS handles websocket requests from the peer.
func ServeWS(
	hub *Hub,
	c *gin.Context,
	scope models.ProjectScope,
) {
	if hub == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "websocket_unavailable",
			"message": "实时通知服务不可用",
		})
		return
	}
	if err := scope.Validate(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "invalid_project_context",
			"message": "项目上下文无效",
		})
		return
	}

	// Get user ID from JWT token in context
	userIDInterface, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":   "unauthorized",
			"message": "用户未认证",
		})
		return
	}

	userID, ok := userIDInterface.(uint)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "invalid_user_context",
			"message": "用户身份上下文无效",
		})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Print("WebSocket upgrade failed")
		return
	}

	client, err := NewClient(hub, conn, userID, scope)
	if err != nil {
		_ = conn.Close()
		log.Print("Rejected invalid WebSocket client binding")
		return
	}
	if !client.hub.registerClient(client) {
		client.close()
		return
	}

	// Start goroutines for reading and writing
	go client.writePump()
	go client.readPump()
}
