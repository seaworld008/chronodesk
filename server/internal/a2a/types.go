// Package a2a implements the A2A 1.0 JSON-RPC binding and durable task
// lifecycle primitives used by ChronoDesk.
package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	ProtocolVersion = "1.0"
	JSONRPCVersion  = "2.0"
	AgentCardPath   = "/.well-known/agent-card.json"
	RPCPath         = "/a2a/v1"

	// MetadataLinkedTicketID keeps ChronoDesk's domain linkage inside the
	// protocol-defined metadata object instead of adding a non-standard field
	// to SendMessageRequest or Task.
	MetadataLinkedTicketID = "com.chronodesk/linkedTicketId"
)

// TaskState follows the A2A 1.0 ProtoJSON enum names on the wire.
type TaskState string

const (
	TaskStateUnspecified   TaskState = "TASK_STATE_UNSPECIFIED"
	TaskStateSubmitted     TaskState = "TASK_STATE_SUBMITTED"
	TaskStateWorking       TaskState = "TASK_STATE_WORKING"
	TaskStateCompleted     TaskState = "TASK_STATE_COMPLETED"
	TaskStateFailed        TaskState = "TASK_STATE_FAILED"
	TaskStateCanceled      TaskState = "TASK_STATE_CANCELED"
	TaskStateInputRequired TaskState = "TASK_STATE_INPUT_REQUIRED"
	TaskStateRejected      TaskState = "TASK_STATE_REJECTED"
	TaskStateAuthRequired  TaskState = "TASK_STATE_AUTH_REQUIRED"
)

func (s TaskState) IsValid() bool {
	switch normalizeTaskState(s) {
	case TaskStateSubmitted, TaskStateWorking, TaskStateCompleted, TaskStateFailed,
		TaskStateCanceled, TaskStateInputRequired, TaskStateRejected, TaskStateAuthRequired:
		return true
	default:
		return false
	}
}

func (s TaskState) IsTerminal() bool {
	switch normalizeTaskState(s) {
	case TaskStateCompleted, TaskStateFailed, TaskStateCanceled, TaskStateRejected:
		return true
	default:
		return false
	}
}

func (s TaskState) IsInterrupted() bool {
	switch normalizeTaskState(s) {
	case TaskStateInputRequired, TaskStateAuthRequired:
		return true
	default:
		return false
	}
}

func normalizeTaskState(s TaskState) TaskState {
	switch s {
	case TaskStateSubmitted:
		return TaskStateSubmitted
	case TaskStateWorking:
		return TaskStateWorking
	case TaskStateCompleted:
		return TaskStateCompleted
	case TaskStateFailed:
		return TaskStateFailed
	case TaskStateCanceled:
		return TaskStateCanceled
	case TaskStateInputRequired:
		return TaskStateInputRequired
	case TaskStateRejected:
		return TaskStateRejected
	case TaskStateAuthRequired:
		return TaskStateAuthRequired
	default:
		return TaskStateUnspecified
	}
}

// Role identifies the creator of an A2A message.
type Role string

const (
	RoleUnspecified Role = "ROLE_UNSPECIFIED"
	RoleUser        Role = "ROLE_USER"
	RoleAgent       Role = "ROLE_AGENT"
)

func normalizeRole(role Role) Role {
	switch role {
	case RoleUser:
		return RoleUser
	case RoleAgent:
		return RoleAgent
	default:
		return RoleUnspecified
	}
}

// Part is a discriminated union. Exactly one of Text, Raw, URL, or Data must
// be present. URL parts are references only; this package never fetches them.
type Part struct {
	Text      *string         `json:"text,omitempty"`
	Raw       []byte          `json:"raw,omitempty"`
	URL       *string         `json:"url,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Metadata  map[string]any  `json:"metadata,omitempty"`
	Filename  string          `json:"filename,omitempty"`
	MediaType string          `json:"mediaType,omitempty"`
}

func (p Part) Validate() error {
	count := 0
	if p.Text != nil {
		count++
	}
	if p.Raw != nil {
		count++
	}
	if p.URL != nil {
		count++
	}
	if len(p.Data) > 0 {
		if !json.Valid(p.Data) {
			return errors.New("part.data must contain valid JSON")
		}
		count++
	}
	if count != 1 {
		return errors.New("part must contain exactly one of text, raw, url, or data")
	}
	return nil
}

type Message struct {
	MessageID       string         `json:"messageId"`
	ContextID       string         `json:"contextId,omitempty"`
	TaskID          string         `json:"taskId,omitempty"`
	Role            Role           `json:"role"`
	Parts           []Part         `json:"parts"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	Extensions      []string       `json:"extensions,omitempty"`
	ReferenceTaskID []string       `json:"referenceTaskIds,omitempty"`
	RequestDigest   string         `json:"-"`
}

const (
	maxA2AMessageIDLength = 255
	maxA2AContextIDLength = 255
	maxA2ATaskIDLength    = 64
)

func (m *Message) ValidateInbound() error {
	if m == nil {
		return errors.New("message is required")
	}
	if strings.TrimSpace(m.MessageID) == "" {
		return errors.New("message.messageId is required")
	}
	if utf8.RuneCountInString(m.MessageID) > maxA2AMessageIDLength {
		return fmt.Errorf(
			"message.messageId must not exceed %d characters",
			maxA2AMessageIDLength,
		)
	}
	if utf8.RuneCountInString(m.ContextID) > maxA2AContextIDLength {
		return fmt.Errorf(
			"message.contextId must not exceed %d characters",
			maxA2AContextIDLength,
		)
	}
	if utf8.RuneCountInString(m.TaskID) > maxA2ATaskIDLength {
		return fmt.Errorf(
			"message.taskId must not exceed %d characters",
			maxA2ATaskIDLength,
		)
	}
	m.Role = normalizeRole(m.Role)
	if m.Role != RoleUser {
		return errors.New("inbound message.role must be ROLE_USER")
	}
	if len(m.Parts) == 0 {
		return errors.New("message.parts must contain at least one part")
	}
	for i := range m.Parts {
		if err := m.Parts[i].Validate(); err != nil {
			return fmt.Errorf("message.parts[%d]: %w", i, err)
		}
	}
	return nil
}

func (m *Message) normalizeAgent(taskID, contextID string, now time.Time, id func() string) error {
	if m == nil {
		return nil
	}
	m.Role = normalizeRole(m.Role)
	if m.Role == RoleUnspecified {
		m.Role = RoleAgent
	}
	if m.Role != RoleAgent {
		return errors.New("backend message.role must be ROLE_AGENT")
	}
	if m.MessageID == "" {
		m.MessageID = id()
	}
	m.TaskID = taskID
	m.ContextID = contextID
	if len(m.Parts) == 0 {
		return errors.New("backend message.parts must contain at least one part")
	}
	for i := range m.Parts {
		if err := m.Parts[i].Validate(); err != nil {
			return fmt.Errorf("backend message.parts[%d]: %w", i, err)
		}
	}
	if m.Metadata == nil {
		m.Metadata = map[string]any{}
	}
	m.Metadata["createdAt"] = now.UTC().Format(time.RFC3339Nano)
	return nil
}

type Artifact struct {
	ArtifactID  string         `json:"artifactId"`
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Parts       []Part         `json:"parts"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Extensions  []string       `json:"extensions,omitempty"`
}

func (a *Artifact) Validate() error {
	if a == nil {
		return errors.New("artifact is required")
	}
	if strings.TrimSpace(a.ArtifactID) == "" {
		return errors.New("artifact.artifactId is required")
	}
	if len(a.Parts) == 0 {
		return errors.New("artifact.parts must contain at least one part")
	}
	for i := range a.Parts {
		if err := a.Parts[i].Validate(); err != nil {
			return fmt.Errorf("artifact.parts[%d]: %w", i, err)
		}
	}
	return nil
}

type TaskStatus struct {
	State     TaskState `json:"state"`
	Message   *Message  `json:"message,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

// MarshalJSON implements the A2A 1.0 ProtoJSON timestamp contract. Database
// drivers may hydrate time.Time values in the deployment's local timezone;
// the wire representation must nevertheless always use UTC with a Z suffix.
func (s TaskStatus) MarshalJSON() ([]byte, error) {
	var timestamp *time.Time
	if !s.Timestamp.IsZero() {
		utc := s.Timestamp.UTC()
		timestamp = &utc
	}
	return json.Marshal(struct {
		State     TaskState  `json:"state"`
		Message   *Message   `json:"message,omitempty"`
		Timestamp *time.Time `json:"timestamp,omitempty"`
	}{
		State:     s.State,
		Message:   s.Message,
		Timestamp: timestamp,
	})
}

type Task struct {
	ID                 string         `json:"id"`
	ContextID          string         `json:"contextId,omitempty"`
	Status             TaskStatus     `json:"status"`
	Artifacts          []Artifact     `json:"artifacts,omitempty"`
	History            []Message      `json:"history,omitempty"`
	Metadata           map[string]any `json:"metadata,omitempty"`
	CreatedAt          time.Time      `json:"-"`
	LastModified       time.Time      `json:"-"`
	LinkedTicketID     *uint          `json:"-"`
	StatusHistory      []TaskStatus   `json:"-"`
	Version            uint64         `json:"-"`
	OwnerActorType     string         `json:"-"`
	OwnerActorID       string         `json:"-"`
	OwnerCredentialID  string         `json:"-"`
	ExecutionClaimID   string         `json:"-"`
	ExecutionMessageID string         `json:"-"`
	ExecutionExpiresAt *time.Time     `json:"-"`
}

func (t Task) Clone() Task {
	var cloned Task
	data, _ := json.Marshal(t)
	_ = json.Unmarshal(data, &cloned)
	cloned.CreatedAt = t.CreatedAt
	cloned.LastModified = t.LastModified
	if t.LinkedTicketID != nil {
		linkedTicketID := *t.LinkedTicketID
		cloned.LinkedTicketID = &linkedTicketID
	}
	if len(t.StatusHistory) > 0 {
		statuses, _ := json.Marshal(t.StatusHistory)
		_ = json.Unmarshal(statuses, &cloned.StatusHistory)
	}
	cloned.Version = t.Version
	cloned.OwnerActorType = t.OwnerActorType
	cloned.OwnerActorID = t.OwnerActorID
	cloned.OwnerCredentialID = t.OwnerCredentialID
	cloned.ExecutionClaimID = t.ExecutionClaimID
	cloned.ExecutionMessageID = t.ExecutionMessageID
	if t.ExecutionExpiresAt != nil {
		expiresAt := *t.ExecutionExpiresAt
		cloned.ExecutionExpiresAt = &expiresAt
	}
	for i := range cloned.History {
		if i < len(t.History) {
			cloned.History[i].RequestDigest = t.History[i].RequestDigest
		}
	}
	return cloned
}

// TaskOwner is the authenticated owner snapshot used for object-level access
// control. Actor identity is authoritative; CredentialID is audit-only so
// credential rotation does not orphan tasks.
type TaskOwner struct {
	ActorType    string
	ActorID      string
	CredentialID string
}

type taskOwnerContextKey struct{}

func WithTaskOwner(ctx context.Context, owner TaskOwner) context.Context {
	return context.WithValue(ctx, taskOwnerContextKey{}, owner)
}

func TaskOwnerFromContext(ctx context.Context) (TaskOwner, bool) {
	owner, ok := ctx.Value(taskOwnerContextKey{}).(TaskOwner)
	if !ok || strings.TrimSpace(owner.ActorType) == "" || strings.TrimSpace(owner.ActorID) == "" {
		return TaskOwner{}, false
	}
	return owner, true
}

type TaskStatusUpdateEvent struct {
	TaskID    string         `json:"taskId"`
	ContextID string         `json:"contextId"`
	Status    TaskStatus     `json:"status"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type TaskArtifactUpdateEvent struct {
	TaskID    string         `json:"taskId"`
	ContextID string         `json:"contextId"`
	Artifact  Artifact       `json:"artifact"`
	Append    bool           `json:"append,omitempty"`
	LastChunk bool           `json:"lastChunk,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// StreamResponse is the A2A 1.0 oneof wrapper. Exactly one field is populated.
type StreamResponse struct {
	Task           *Task                    `json:"task,omitempty"`
	Message        *Message                 `json:"message,omitempty"`
	StatusUpdate   *TaskStatusUpdateEvent   `json:"statusUpdate,omitempty"`
	ArtifactUpdate *TaskArtifactUpdateEvent `json:"artifactUpdate,omitempty"`
}

func (r StreamResponse) Terminal() bool {
	if r.Task != nil {
		return r.Task.Status.State.IsTerminal()
	}
	if r.StatusUpdate != nil {
		return r.StatusUpdate.Status.State.IsTerminal()
	}
	return false
}

func (r StreamResponse) Interrupted() bool {
	if r.Task != nil {
		return r.Task.Status.State.IsInterrupted()
	}
	if r.StatusUpdate != nil {
		return r.StatusUpdate.Status.State.IsInterrupted()
	}
	return false
}

type SendMessageConfiguration struct {
	AcceptedOutputModes  []string                `json:"acceptedOutputModes,omitempty"`
	TaskPushNotification *PushNotificationConfig `json:"taskPushNotificationConfig,omitempty"`
	HistoryLength        *int                    `json:"historyLength,omitempty"`
	ReturnImmediately    bool                    `json:"returnImmediately,omitempty"`
}

type SendMessageParams struct {
	Tenant        string                   `json:"tenant,omitempty"`
	Message       Message                  `json:"message"`
	Configuration SendMessageConfiguration `json:"configuration,omitempty"`
	Metadata      map[string]any           `json:"metadata,omitempty"`
}

type SendMessageResult struct {
	Task    *Task    `json:"task,omitempty"`
	Message *Message `json:"message,omitempty"`
}

type GetTaskParams struct {
	Tenant        string `json:"tenant,omitempty"`
	ID            string `json:"id"`
	HistoryLength *int   `json:"historyLength,omitempty"`
}

type ListTasksParams struct {
	Tenant               string     `json:"tenant,omitempty"`
	ContextID            string     `json:"contextId,omitempty"`
	Status               TaskState  `json:"status,omitempty"`
	PageSize             int        `json:"pageSize,omitempty"`
	PageToken            string     `json:"pageToken,omitempty"`
	HistoryLength        *int       `json:"historyLength,omitempty"`
	StatusTimestampAfter *time.Time `json:"statusTimestampAfter,omitempty"`
	IncludeArtifacts     bool       `json:"includeArtifacts,omitempty"`
}

type ListTasksResult struct {
	Tasks         []Task `json:"tasks"`
	NextPageToken string `json:"nextPageToken"`
	PageSize      int    `json:"pageSize"`
	TotalSize     int64  `json:"totalSize"`
}

type TaskIDParams struct {
	Tenant   string         `json:"tenant,omitempty"`
	ID       string         `json:"id"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type AuthenticationInfo struct {
	Scheme      string `json:"scheme"`
	Credentials string `json:"credentials,omitempty"`
}

type PushNotificationConfig struct {
	Tenant         string              `json:"tenant,omitempty"`
	ID             string              `json:"id,omitempty"`
	TaskID         string              `json:"taskId,omitempty"`
	URL            string              `json:"url"`
	Token          string              `json:"token,omitempty"`
	Authentication *AuthenticationInfo `json:"authentication,omitempty"`
	CreatedAt      time.Time           `json:"-"`
}

// Redacted removes callback secrets before a configuration is read back.
func (c PushNotificationConfig) Redacted() PushNotificationConfig {
	c.Token = ""
	if c.Authentication != nil {
		c.Authentication = &AuthenticationInfo{Scheme: c.Authentication.Scheme}
	}
	return c
}

type GetPushConfigParams struct {
	Tenant string `json:"tenant,omitempty"`
	TaskID string `json:"taskId"`
	ID     string `json:"id"`
}

type ListPushConfigsParams struct {
	Tenant    string `json:"tenant,omitempty"`
	TaskID    string `json:"taskId"`
	PageSize  int    `json:"pageSize,omitempty"`
	PageToken string `json:"pageToken,omitempty"`
}

type ListPushConfigsResult struct {
	Configs       []PushNotificationConfig `json:"configs"`
	NextPageToken string                   `json:"nextPageToken,omitempty"`
}

type GetExtendedAgentCardParams struct {
	Tenant string `json:"tenant,omitempty"`
}

// StoredEvent is a durable, replayable task event. Cursor is opaque to clients.
type StoredEvent struct {
	Cursor          string         `json:"cursor"`
	TaskID          string         `json:"taskId"`
	ContextID       string         `json:"contextId"`
	ResourceVersion uint64         `json:"resourceVersion"`
	Payload         StreamResponse `json:"payload"`
	CreatedAt       time.Time      `json:"createdAt"`
}

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func (r JSONRPCRequest) Validate() error {
	if r.JSONRPC != JSONRPCVersion {
		return errors.New("jsonrpc must be 2.0")
	}
	if len(bytes.TrimSpace(r.ID)) == 0 || bytes.Equal(bytes.TrimSpace(r.ID), []byte("null")) {
		return errors.New("id is required")
	}
	if strings.TrimSpace(r.Method) == "" {
		return errors.New("method is required")
	}
	return nil
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    []any  `json:"data,omitempty"`
}

func errorDetail(reason string, metadata map[string]string) []any {
	detail := map[string]any{
		"@type":  "type.googleapis.com/google.rpc.ErrorInfo",
		"reason": reason,
		"domain": "a2a-protocol.org",
	}
	if len(metadata) > 0 {
		detail["metadata"] = metadata
	}
	return []any{detail}
}
