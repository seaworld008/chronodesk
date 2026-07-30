package models

import (
	"time"

	"gorm.io/datatypes"
)

// A2ATaskState is the persisted, transport-neutral state of an A2A task.
// The wire layer maps these values to the ProtoJSON TASK_STATE_* enum names.
type A2ATaskState string

const (
	A2ATaskStateSubmitted     A2ATaskState = "submitted"
	A2ATaskStateWorking       A2ATaskState = "working"
	A2ATaskStateInputRequired A2ATaskState = "input-required"
	A2ATaskStateCompleted     A2ATaskState = "completed"
	A2ATaskStateFailed        A2ATaskState = "failed"
	A2ATaskStateCanceled      A2ATaskState = "canceled"
	A2ATaskStateRejected      A2ATaskState = "rejected"
	A2ATaskStateAuthRequired  A2ATaskState = "auth-required"
)

// AgentTask persists one A2A interaction. It deliberately references a Ticket
// by ID only: A2A lifecycle transitions never mutate the linked Ticket.
type AgentTask struct {
	ID                 string                        `json:"id" gorm:"primaryKey;size:64"`
	OrganizationID     uint                          `json:"organization_id" gorm:"index"`
	ProjectID          uint                          `json:"project_id" gorm:"index"`
	ContextID          string                        `json:"context_id" gorm:"size:255;not null;index"`
	LinkedTicketID     *uint                         `json:"linked_ticket_id,omitempty" gorm:"index"`
	OwnerActorType     ActorType                     `json:"owner_actor_type,omitempty" gorm:"size:32;index:idx_agent_task_owner,priority:1"`
	OwnerActorID       string                        `json:"owner_actor_id,omitempty" gorm:"size:128;index:idx_agent_task_owner,priority:2"`
	OwnerCredentialID  string                        `json:"owner_credential_id,omitempty" gorm:"size:128;index"`
	State              A2ATaskState                  `json:"state" gorm:"size:32;not null;index"`
	StatusMessage      datatypes.JSON                `json:"status_message,omitempty" gorm:"type:json"`
	StatusTimestamp    time.Time                     `json:"status_timestamp" gorm:"not null;index"`
	Metadata           datatypes.JSON                `json:"metadata,omitempty" gorm:"type:json"`
	Version            uint64                        `json:"version" gorm:"not null;default:1"`
	ExecutionClaimID   string                        `json:"-" gorm:"size:64;not null;default:'';index"`
	ExecutionMessageID string                        `json:"-" gorm:"size:255;not null;default:'';index"`
	ExecutionExpiresAt *time.Time                    `json:"-" gorm:"index"`
	CreatedAt          time.Time                     `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt          time.Time                     `json:"updated_at" gorm:"autoUpdateTime"`
	Messages           []AgentMessage                `json:"messages,omitempty" gorm:"foreignKey:TaskID;references:ID"`
	Artifacts          []AgentArtifact               `json:"artifacts,omitempty" gorm:"foreignKey:TaskID;references:ID"`
	StatusHistory      []AgentTaskStatusHistory      `json:"status_history,omitempty" gorm:"foreignKey:TaskID;references:ID"`
	PushNotification   []AgentPushNotificationConfig `json:"push_notification,omitempty" gorm:"foreignKey:TaskID;references:ID"`
}

func (AgentTask) TableName() string {
	return "agent_tasks"
}

// AgentMessage stores the complete protocol message as JSON while retaining
// indexed routing fields for efficient task/context queries.
type AgentMessage struct {
	ID             string         `json:"id" gorm:"primaryKey;size:255"`
	OrganizationID uint           `json:"organization_id" gorm:"index"`
	ProjectID      uint           `json:"project_id" gorm:"index"`
	TaskID         string         `json:"task_id" gorm:"size:64;not null;index:idx_agent_messages_task_sequence,priority:1"`
	ContextID      string         `json:"context_id" gorm:"size:255;not null;index"`
	Role           string         `json:"role" gorm:"size:24;not null"`
	Sequence       uint64         `json:"sequence" gorm:"not null;index:idx_agent_messages_task_sequence,priority:2"`
	RequestDigest  string         `json:"-" gorm:"size:64;index"`
	Payload        datatypes.JSON `json:"payload" gorm:"type:json;not null"`
	CreatedAt      time.Time      `json:"created_at" gorm:"autoCreateTime"`
}

func (AgentMessage) TableName() string {
	return "agent_messages"
}

// AgentArtifact stores a generated A2A artifact independently from Ticket
// attachments. A backend may choose to expose a Ticket attachment as a Part.
type AgentArtifact struct {
	ID             string         `json:"id" gorm:"primaryKey;size:64"`
	OrganizationID uint           `json:"organization_id" gorm:"index"`
	ProjectID      uint           `json:"project_id" gorm:"index"`
	TaskID         string         `json:"task_id" gorm:"primaryKey;size:64;not null;index:idx_agent_artifacts_task_sequence,priority:1"`
	Sequence       uint64         `json:"sequence" gorm:"not null;index:idx_agent_artifacts_task_sequence,priority:2"`
	Payload        datatypes.JSON `json:"payload" gorm:"type:json;not null"`
	CreatedAt      time.Time      `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt      time.Time      `json:"updated_at" gorm:"autoUpdateTime"`
}

func (AgentArtifact) TableName() string {
	return "agent_artifacts"
}

// AgentTaskStatusHistory is the append-only audit trail of A2A status changes.
type AgentTaskStatusHistory struct {
	ID             uint64         `json:"id" gorm:"primaryKey;autoIncrement"`
	OrganizationID uint           `json:"organization_id" gorm:"index"`
	ProjectID      uint           `json:"project_id" gorm:"index"`
	TaskID         string         `json:"task_id" gorm:"size:64;not null;uniqueIndex:idx_agent_status_task_sequence,priority:1"`
	Sequence       uint64         `json:"sequence" gorm:"not null;uniqueIndex:idx_agent_status_task_sequence,priority:2"`
	State          A2ATaskState   `json:"state" gorm:"size:32;not null;index"`
	Status         datatypes.JSON `json:"status" gorm:"type:json;not null"`
	CreatedAt      time.Time      `json:"created_at" gorm:"autoCreateTime"`
}

func (AgentTaskStatusHistory) TableName() string {
	return "agent_task_status_history"
}

// AgentTaskEvent is the durable replay log used by Last-Event-ID based SSE
// subscriptions. ID is the monotonically increasing event cursor source.
type AgentTaskEvent struct {
	ID              uint64         `json:"id" gorm:"primaryKey;autoIncrement"`
	OrganizationID  uint           `json:"organization_id" gorm:"index"`
	ProjectID       uint           `json:"project_id" gorm:"index"`
	TaskID          string         `json:"task_id" gorm:"size:64;not null;index:idx_agent_events_task_id,priority:1"`
	ContextID       string         `json:"context_id" gorm:"size:255;not null;index"`
	ResourceVersion uint64         `json:"resource_version" gorm:"not null;default:1"`
	Payload         datatypes.JSON `json:"payload" gorm:"type:json;not null"`
	CreatedAt       time.Time      `json:"created_at" gorm:"autoCreateTime;index:idx_agent_events_task_id,priority:2"`
}

func (AgentTaskEvent) TableName() string {
	return "agent_task_events"
}

// AgentPushNotificationConfig persists client-provided callback configuration.
// Credentials are intentionally omitted from JSON responses by the protocol
// layer after creation.
type AgentPushNotificationConfig struct {
	ID             string `json:"id" gorm:"primaryKey;size:64"`
	OrganizationID uint   `json:"organization_id" gorm:"index"`
	ProjectID      uint   `json:"project_id" gorm:"index"`
	TaskID         string `json:"task_id" gorm:"size:64;not null;index"`
	URL            string `json:"url" gorm:"size:2048;not null"`
	// Token contains an AEAD envelope. Authentication stores that envelope as
	// a JSON string, preserving the JSON column while ensuring credentials are
	// never persisted as a readable JSON object.
	Token          string         `json:"-" gorm:"size:2048"`
	Authentication datatypes.JSON `json:"-" gorm:"type:json"`
	CreatedAt      time.Time      `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt      time.Time      `json:"updated_at" gorm:"autoUpdateTime"`
}

func (AgentPushNotificationConfig) TableName() string {
	return "agent_push_notification_configs"
}
