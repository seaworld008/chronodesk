package models

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type AgentRunStatus string

const (
	AgentRunStatusQueued          AgentRunStatus = "queued"
	AgentRunStatusRunning         AgentRunStatus = "running"
	AgentRunStatusWaitingApproval AgentRunStatus = "waiting_approval"
	AgentRunStatusSucceeded       AgentRunStatus = "succeeded"
	AgentRunStatusFailed          AgentRunStatus = "failed"
	AgentRunStatusCancelled       AgentRunStatus = "cancelled"
	AgentRunStatusTakenOver       AgentRunStatus = "taken_over"
)

func (status AgentRunStatus) IsTerminal() bool {
	switch status {
	case AgentRunStatusSucceeded,
		AgentRunStatusFailed,
		AgentRunStatusCancelled,
		AgentRunStatusTakenOver:
		return true
	default:
		return false
	}
}

type ActionRiskLevel string

const (
	ActionRiskLow      ActionRiskLevel = "low"
	ActionRiskMedium   ActionRiskLevel = "medium"
	ActionRiskHigh     ActionRiskLevel = "high"
	ActionRiskCritical ActionRiskLevel = "critical"
)

func (risk ActionRiskLevel) IsValid() bool {
	switch risk {
	case ActionRiskLow, ActionRiskMedium, ActionRiskHigh, ActionRiskCritical:
		return true
	default:
		return false
	}
}

type ActionProposalStatus string

const (
	ActionProposalPending     ActionProposalStatus = "pending"
	ActionProposalApproved    ActionProposalStatus = "approved"
	ActionProposalRejected    ActionProposalStatus = "rejected"
	ActionProposalExecuted    ActionProposalStatus = "executed"
	ActionProposalInvalidated ActionProposalStatus = "invalidated"
	ActionProposalExpired     ActionProposalStatus = "expired"
)

type ApprovalTaskStatus string

const (
	ApprovalTaskPending     ApprovalTaskStatus = "pending"
	ApprovalTaskApproved    ApprovalTaskStatus = "approved"
	ApprovalTaskRejected    ApprovalTaskStatus = "rejected"
	ApprovalTaskInvalidated ApprovalTaskStatus = "invalidated"
	ApprovalTaskExpired     ApprovalTaskStatus = "expired"
)

type ApprovalDecisionValue string

const (
	ApprovalDecisionApprove ApprovalDecisionValue = "approve"
	ApprovalDecisionReject  ApprovalDecisionValue = "reject"
)

type HandoffDirection string

const (
	HandoffHumanToAgent HandoffDirection = "human_to_agent"
	HandoffAgentToHuman HandoffDirection = "agent_to_human"
	HandoffQueueToTeam  HandoffDirection = "queue_to_team"
)

type EvidenceKind string

const (
	EvidenceKnowledgeVersion EvidenceKind = "knowledge_version"
	EvidenceExternalObject   EvidenceKind = "external_object"
	EvidenceArtifact         EvidenceKind = "artifact"
)

// AgentRun records observable execution facts without storing hidden
// chain-of-thought. Prompt, tool and policy identifiers point to immutable
// trusted-control versions.
type AgentRun struct {
	ID             string         `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt      time.Time      `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt      time.Time      `json:"updated_at" gorm:"autoUpdateTime"`
	OrganizationID uint           `json:"organization_id" gorm:"not null;index"`
	ProjectID      uint           `json:"project_id" gorm:"not null;index"`
	TicketID       uint           `json:"ticket_id" gorm:"not null;index"`
	PrincipalID    string         `json:"principal_id" gorm:"size:36;not null;index"`
	AgentTaskID    string         `json:"agent_task_id,omitempty" gorm:"size:64;index"`
	Status         AgentRunStatus `json:"status" gorm:"size:32;not null;index"`

	ModelProvider     string         `json:"model_provider" gorm:"size:100;not null"`
	ModelName         string         `json:"model_name" gorm:"size:200;not null"`
	PromptVersion     string         `json:"prompt_version" gorm:"size:100;not null"`
	ToolsetVersion    string         `json:"toolset_version" gorm:"size:100;not null"`
	PolicyVersion     string         `json:"policy_version" gorm:"size:100;not null"`
	PolicySnapshot    datatypes.JSON `json:"policy_snapshot" gorm:"type:jsonb;not null"`
	InputSummary      string         `json:"input_summary" gorm:"type:text"`
	OutputSummary     string         `json:"output_summary" gorm:"type:text"`
	PromptTokens      int64          `json:"prompt_tokens" gorm:"not null;default:0"`
	CompletionTokens  int64          `json:"completion_tokens" gorm:"not null;default:0"`
	CostMicros        int64          `json:"cost_micros" gorm:"not null;default:0"`
	StartedAt         *time.Time     `json:"started_at,omitempty"`
	FinishedAt        *time.Time     `json:"finished_at,omitempty"`
	TerminationReason string         `json:"termination_reason,omitempty" gorm:"size:500"`
}

func (AgentRun) TableName() string {
	return "agent_runs"
}

func (run *AgentRun) BeforeCreate(_ *gorm.DB) error {
	return ensureCollaborationID(&run.ID)
}

// ActionProposal is immutable trusted control data. ProposalDigest binds the
// exact action, preview, evidence, risk, policy and target Ticket version.
type ActionProposal struct {
	ID             string               `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt      time.Time            `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt      time.Time            `json:"updated_at" gorm:"autoUpdateTime"`
	OrganizationID uint                 `json:"organization_id" gorm:"not null;index"`
	ProjectID      uint                 `json:"project_id" gorm:"not null;index"`
	TicketID       uint                 `json:"ticket_id" gorm:"not null;index"`
	AgentRunID     string               `json:"agent_run_id" gorm:"size:36;not null;index"`
	ProposedByType ActorType            `json:"proposed_by_type" gorm:"size:32;not null;index"`
	ProposedByID   string               `json:"proposed_by_id" gorm:"size:128;not null;index"`
	ActionType     string               `json:"action_type" gorm:"size:100;not null;index"`
	ActionPayload  datatypes.JSON       `json:"action_payload" gorm:"type:jsonb;not null"`
	ChangePreview  datatypes.JSON       `json:"change_preview" gorm:"type:jsonb;not null"`
	EvidenceDigest string               `json:"evidence_digest" gorm:"size:64;not null"`
	RiskLevel      ActionRiskLevel      `json:"risk_level" gorm:"size:20;not null;index"`
	TargetVersion  uint64               `json:"target_ticket_version" gorm:"not null"`
	PolicyVersion  string               `json:"policy_version" gorm:"size:100;not null"`
	ProposalDigest string               `json:"proposal_digest" gorm:"size:64;not null;uniqueIndex;<-:create"`
	Status         ActionProposalStatus `json:"status" gorm:"size:24;not null;index"`
	ExpiresAt      time.Time            `json:"expires_at" gorm:"not null;index"`
	ExecutedAt     *time.Time           `json:"executed_at,omitempty"`
}

func (ActionProposal) TableName() string {
	return "action_proposals"
}

func (proposal *ActionProposal) BeforeCreate(_ *gorm.DB) error {
	if err := ensureCollaborationID(&proposal.ID); err != nil {
		return err
	}
	if !proposal.RiskLevel.IsValid() {
		return fmt.Errorf("invalid action risk level %q", proposal.RiskLevel)
	}
	digest, err := proposal.CalculateDigest()
	if err != nil {
		return err
	}
	if proposal.ProposalDigest != "" && proposal.ProposalDigest != digest {
		return fmt.Errorf("proposal digest does not match immutable content")
	}
	proposal.ProposalDigest = digest
	return nil
}

func (proposal ActionProposal) CalculateDigest() (string, error) {
	control := struct {
		OrganizationID uint            `json:"organization_id"`
		ProjectID      uint            `json:"project_id"`
		TicketID       uint            `json:"ticket_id"`
		AgentRunID     string          `json:"agent_run_id"`
		ProposedByType ActorType       `json:"proposed_by_type"`
		ProposedByID   string          `json:"proposed_by_id"`
		ActionType     string          `json:"action_type"`
		ActionPayload  json.RawMessage `json:"action_payload"`
		ChangePreview  json.RawMessage `json:"change_preview"`
		EvidenceDigest string          `json:"evidence_digest"`
		RiskLevel      ActionRiskLevel `json:"risk_level"`
		TargetVersion  uint64          `json:"target_ticket_version"`
		PolicyVersion  string          `json:"policy_version"`
		ExpiresAt      time.Time       `json:"expires_at"`
	}{
		OrganizationID: proposal.OrganizationID,
		ProjectID:      proposal.ProjectID,
		TicketID:       proposal.TicketID,
		AgentRunID:     proposal.AgentRunID,
		ProposedByType: proposal.ProposedByType,
		ProposedByID:   proposal.ProposedByID,
		ActionType:     proposal.ActionType,
		ActionPayload:  json.RawMessage(proposal.ActionPayload),
		ChangePreview:  json.RawMessage(proposal.ChangePreview),
		EvidenceDigest: proposal.EvidenceDigest,
		RiskLevel:      proposal.RiskLevel,
		TargetVersion:  proposal.TargetVersion,
		PolicyVersion:  proposal.PolicyVersion,
		ExpiresAt:      proposal.ExpiresAt.UTC(),
	}
	encoded, err := json.Marshal(control)
	if err != nil {
		return "", fmt.Errorf("encode action proposal digest: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

type ApprovalTask struct {
	ID                string             `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt         time.Time          `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt         time.Time          `json:"updated_at" gorm:"autoUpdateTime"`
	OrganizationID    uint               `json:"organization_id" gorm:"not null;index"`
	ProjectID         uint               `json:"project_id" gorm:"not null;index"`
	TicketID          uint               `json:"ticket_id" gorm:"not null;index"`
	ProposalID        string             `json:"proposal_id" gorm:"size:36;not null;uniqueIndex"`
	ProposalDigest    string             `json:"proposal_digest" gorm:"size:64;not null"`
	TargetVersion     uint64             `json:"target_ticket_version" gorm:"not null"`
	PolicyVersion     string             `json:"policy_version" gorm:"size:100;not null"`
	RequiredApprovals int                `json:"required_approvals" gorm:"not null;default:1"`
	Status            ApprovalTaskStatus `json:"status" gorm:"size:24;not null;index"`
	ExpiresAt         time.Time          `json:"expires_at" gorm:"not null;index"`
	CompletedAt       *time.Time         `json:"completed_at,omitempty"`
	Decisions         []ApprovalDecision `json:"decisions,omitempty" gorm:"foreignKey:ApprovalTaskID"`
}

func (ApprovalTask) TableName() string {
	return "approval_tasks"
}

func (task *ApprovalTask) BeforeCreate(_ *gorm.DB) error {
	if task.RequiredApprovals < 1 || task.RequiredApprovals > 2 {
		return fmt.Errorf("required approvals must be 1 or 2")
	}
	return ensureCollaborationID(&task.ID)
}

type ApprovalDecision struct {
	ID             string                `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt      time.Time             `json:"created_at" gorm:"autoCreateTime"`
	OrganizationID uint                  `json:"organization_id" gorm:"not null;index"`
	ProjectID      uint                  `json:"project_id" gorm:"not null;index"`
	ApprovalTaskID string                `json:"approval_task_id" gorm:"size:36;not null;index;uniqueIndex:idx_approval_decision_actor,priority:1"`
	ActorType      ActorType             `json:"actor_type" gorm:"size:32;not null;uniqueIndex:idx_approval_decision_actor,priority:2"`
	ActorID        string                `json:"actor_id" gorm:"size:128;not null;uniqueIndex:idx_approval_decision_actor,priority:3"`
	Decision       ApprovalDecisionValue `json:"decision" gorm:"size:16;not null;index"`
	Comment        string                `json:"comment" gorm:"size:1000"`
	ProposalDigest string                `json:"proposal_digest" gorm:"size:64;not null"`
}

func (ApprovalDecision) TableName() string {
	return "approval_decisions"
}

func (decision *ApprovalDecision) BeforeCreate(_ *gorm.DB) error {
	if decision.OrganizationID == 0 || decision.ProjectID == 0 {
		return fmt.Errorf("approval decision requires organization and project scope")
	}
	if decision.Decision != ApprovalDecisionApprove &&
		decision.Decision != ApprovalDecisionReject {
		return fmt.Errorf("invalid approval decision %q", decision.Decision)
	}
	return ensureCollaborationID(&decision.ID)
}

type Handoff struct {
	ID               string           `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt        time.Time        `json:"created_at" gorm:"autoCreateTime"`
	OrganizationID   uint             `json:"organization_id" gorm:"not null;index"`
	ProjectID        uint             `json:"project_id" gorm:"not null;index"`
	TicketID         uint             `json:"ticket_id" gorm:"not null;index"`
	AgentRunID       string           `json:"agent_run_id,omitempty" gorm:"size:36;index"`
	Direction        HandoffDirection `json:"direction" gorm:"size:32;not null;index"`
	FromActorType    ActorType        `json:"from_actor_type" gorm:"size:32;not null"`
	FromActorID      string           `json:"from_actor_id" gorm:"size:128;not null"`
	ToActorType      ActorType        `json:"to_actor_type" gorm:"size:32;not null"`
	ToActorID        string           `json:"to_actor_id" gorm:"size:128;not null"`
	Reason           string           `json:"reason" gorm:"size:1000;not null"`
	CompletedSummary string           `json:"completed_summary" gorm:"type:text"`
	MissingInfo      datatypes.JSON   `json:"missing_information" gorm:"type:jsonb;not null"`
	EvidenceDigest   string           `json:"evidence_digest" gorm:"size:64;not null"`
}

func (Handoff) TableName() string {
	return "handoffs"
}

func (handoff *Handoff) BeforeCreate(_ *gorm.DB) error {
	return ensureCollaborationID(&handoff.ID)
}

type EvidenceReference struct {
	ID             string       `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt      time.Time    `json:"created_at" gorm:"autoCreateTime"`
	OrganizationID uint         `json:"organization_id" gorm:"not null;index"`
	ProjectID      uint         `json:"project_id" gorm:"not null;index"`
	TicketID       uint         `json:"ticket_id" gorm:"not null;index"`
	AgentRunID     string       `json:"agent_run_id,omitempty" gorm:"size:36;index"`
	ProposalID     string       `json:"proposal_id,omitempty" gorm:"size:36;index"`
	Kind           EvidenceKind `json:"kind" gorm:"size:32;not null;index"`
	ReferenceID    string       `json:"reference_id" gorm:"size:255;not null"`
	VersionID      string       `json:"version_id" gorm:"size:255;not null"`
	Locator        string       `json:"locator" gorm:"size:500"`
	ContentHash    string       `json:"content_hash" gorm:"size:64;not null"`
}

func (EvidenceReference) TableName() string {
	return "evidence_references"
}

func (evidence *EvidenceReference) BeforeCreate(_ *gorm.DB) error {
	return ensureCollaborationID(&evidence.ID)
}

func ensureCollaborationID(destination *string) error {
	if destination == nil {
		return fmt.Errorf("id destination is required")
	}
	if strings.TrimSpace(*destination) == "" {
		generated, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate collaboration UUIDv7: %w", err)
		}
		*destination = generated.String()
		return nil
	}
	parsed, err := uuid.Parse(*destination)
	if err != nil {
		return fmt.Errorf("collaboration id must be a UUID: %w", err)
	}
	*destination = parsed.String()
	return nil
}
