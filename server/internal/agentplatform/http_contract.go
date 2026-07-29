package agentplatform

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	ProblemInvalidRequest      = "invalid_request"
	ProblemUnauthorized        = "unauthorized"
	ProblemInsufficientScope   = "insufficient_scope"
	ProblemPolicyDenied        = "policy_denied"
	ProblemNotFound            = "not_found"
	ProblemVersionConflict     = "version_conflict"
	ProblemLeaseConflict       = "lease_conflict"
	ProblemIdempotencyConflict = "idempotency_conflict"
	ProblemRateLimited         = "rate_limited"
	ProblemServiceUnavailable  = "service_unavailable"
	ProblemAutomationLoop      = "automation_loop"
	ProblemOutboxConflict      = "outbox_replay_conflict"
	ProblemAttachmentRejected  = "attachment_rejected"
	ProblemInternal            = "internal_error"
)

type Meta struct {
	RequestID  string `json:"request_id"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more,omitempty"`
}

type Envelope struct {
	Data    any      `json:"data"`
	Meta    Meta     `json:"meta"`
	Receipt *Receipt `json:"receipt,omitempty"`
}

type Receipt struct {
	OperationID      string   `json:"operation_id"`
	ResourceID       string   `json:"resource_id"`
	ResourceVersion  uint64   `json:"resource_version"`
	EventID          string   `json:"event_id"`
	ChangedFields    []string `json:"changed_fields"`
	PolicyDecisionID string   `json:"policy_decision_id"`
}

type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail,omitempty"`
	Code      string `json:"code"`
	RequestID string `json:"request_id"`
	Retryable bool   `json:"retryable"`
}

type Cursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

func WriteData(c *gin.Context, status int, data any, meta Meta) {
	if meta.RequestID == "" {
		meta.RequestID = RequestID(c)
	}
	c.JSON(status, Envelope{Data: data, Meta: meta})
}

func WriteReceipt(c *gin.Context, status int, data any, receipt Receipt) {
	if receipt.ChangedFields == nil {
		receipt.ChangedFields = []string{}
	}
	c.JSON(status, Envelope{
		Data:    data,
		Meta:    Meta{RequestID: RequestID(c)},
		Receipt: &receipt,
	})
}

func WriteProblem(c *gin.Context, status int, code, detail string, retryable bool) {
	c.Header("Content-Type", "application/problem+json")
	c.JSON(status, Problem{
		Type:      "https://chronodesk.local/problems/" + code,
		Title:     strings.ReplaceAll(code, "_", " "),
		Status:    status,
		Detail:    detail,
		Code:      code,
		RequestID: RequestID(c),
		Retryable: retryable,
	})
}

func RequestID(c *gin.Context) string {
	if value := c.GetString("request_id"); value != "" {
		return value
	}
	if value := c.GetHeader("X-Request-ID"); value != "" {
		return value
	}
	return "request-unavailable"
}

func EncodeCursor(cursor Cursor) string {
	payload, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func DecodeCursor(value string) (Cursor, error) {
	if strings.TrimSpace(value) == "" {
		return Cursor{}, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return Cursor{}, errors.New("invalid cursor")
	}
	var cursor Cursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.ID == "" || cursor.CreatedAt.IsZero() {
		return Cursor{}, errors.New("invalid cursor")
	}
	return cursor, nil
}

func ParseLimit(c *gin.Context, defaultValue, maximum int) (int, error) {
	if defaultValue <= 0 {
		defaultValue = 20
	}
	if maximum <= 0 {
		maximum = 100
	}
	raw := strings.TrimSpace(c.Query("limit"))
	if raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 || value > maximum {
		return 0, fmt.Errorf("limit must be between 1 and %d", maximum)
	}
	return value, nil
}

func FormatETag(version uint64) string {
	return fmt.Sprintf(`"v%d"`, version)
}

func ParseIfMatch(value string) (uint64, error) {
	value = strings.TrimSpace(value)
	if len(value) < 4 || value[0] != '"' || value[len(value)-1] != '"' || value[1] != 'v' {
		return 0, errors.New(`If-Match must use the format "v<number>"`)
	}
	version, err := strconv.ParseUint(value[2:len(value)-1], 10, 64)
	if err != nil || version == 0 {
		return 0, errors.New(`If-Match must use the format "v<number>"`)
	}
	return version, nil
}

func RequireIdempotencyKey(c *gin.Context) (string, bool) {
	value := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if len(value) < 8 || len(value) > 128 {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, "Idempotency-Key must contain 8 to 128 characters", false)
		return "", false
	}
	return value, true
}
