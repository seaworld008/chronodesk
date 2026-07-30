package models

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/seaworld008/chronodesk/server/internal/agentcontract"
)

// ActorType identifies the security principal that performed an operation.
type ActorType string

const (
	ActorTypeHuman            ActorType = "human"
	ActorTypeServicePrincipal ActorType = "service_principal"
	ActorTypeSystem           ActorType = "system"
)

// ActorRef is the protocol-neutral identity used by audit, event and policy code.
// Persistent models store Type and ID as separate indexed columns.
type ActorRef struct {
	Type ActorType `json:"type"`
	ID   string    `json:"id"`
}

func (a ActorRef) Validate() error {
	switch a.Type {
	case ActorTypeHuman, ActorTypeServicePrincipal, ActorTypeSystem:
	default:
		return fmt.Errorf("unsupported actor type %q", a.Type)
	}
	if strings.TrimSpace(a.ID) == "" {
		return fmt.Errorf("actor id is required")
	}
	return nil
}

func HumanActor(userID uint) ActorRef {
	return ActorRef{Type: ActorTypeHuman, ID: strconv.FormatUint(uint64(userID), 10)}
}

func ServicePrincipalActor(principalID string) ActorRef {
	return ActorRef{Type: ActorTypeServicePrincipal, ID: principalID}
}

func SystemActor(component string) ActorRef {
	if strings.TrimSpace(component) == "" {
		component = "chronodesk"
	}
	return ActorRef{Type: ActorTypeSystem, ID: component}
}

// Agent scopes deliberately describe small, composable capabilities.
const (
	ScopeTicketsRead       = agentcontract.ScopeTicketsRead
	ScopeTicketsCreate     = agentcontract.ScopeTicketsCreate
	ScopeTicketsUpdate     = agentcontract.ScopeTicketsUpdate
	ScopeTicketsAssign     = agentcontract.ScopeTicketsAssign
	ScopeTicketsTransition = agentcontract.ScopeTicketsTransition
	ScopeCommentsWrite     = agentcontract.ScopeCommentsWrite
	ScopeAttachmentsRead   = agentcontract.ScopeAttachmentsRead
	ScopeAttachmentsWrite  = agentcontract.ScopeAttachmentsWrite
	ScopeEventsSubscribe   = agentcontract.ScopeEventsSubscribe
	ScopeTasksManage       = agentcontract.ScopeTasksManage
)

var SupportedAgentScopes = agentcontract.SupportedScopes()

type ServicePrincipalStatus string

const (
	ServicePrincipalStatusActive   ServicePrincipalStatus = "active"
	ServicePrincipalStatusInactive ServicePrincipalStatus = "inactive"
	ServicePrincipalStatusRevoked  ServicePrincipalStatus = "revoked"
)

// ServicePrincipal is an AI agent or automation identity. It never reuses a
// human login and its credentials are held in AgentCredential.
type ServicePrincipal struct {
	ID        string     `json:"id" gorm:"primaryKey;size:36"`
	CreatedAt time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" gorm:"index"`

	Name        string                 `json:"name" gorm:"size:100;not null;uniqueIndex"`
	Description string                 `json:"description" gorm:"size:500"`
	Status      ServicePrincipalStatus `json:"status" gorm:"size:20;not null;default:'active';index"`
	Scopes      datatypes.JSON         `json:"scopes" gorm:"type:jsonb;not null"`

	RateLimitPerMinute int        `json:"rate_limit_per_minute" gorm:"not null;default:60"`
	ConcurrentLimit    int        `json:"concurrent_limit" gorm:"not null;default:4"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty" gorm:"index"`
	ReadOnly           bool       `json:"read_only" gorm:"not null;default:false"`
	EmergencyDisabled  bool       `json:"emergency_disabled" gorm:"not null;default:false;index"`
	LastUsedAt         *time.Time `json:"last_used_at,omitempty"`

	CreatedByID *uint `json:"created_by_id,omitempty" gorm:"index"`

	Credentials []AgentCredential `json:"credentials,omitempty" gorm:"foreignKey:ServicePrincipalID"`
	Policies    []AgentPolicy     `json:"policies,omitempty" gorm:"foreignKey:ServicePrincipalID"`
}

func (ServicePrincipal) TableName() string {
	return "service_principals"
}

func (p *ServicePrincipal) ScopeList() []string {
	var scopes []string
	if len(p.Scopes) == 0 {
		return scopes
	}
	_ = json.Unmarshal(p.Scopes, &scopes)
	return scopes
}

func (p *ServicePrincipal) HasScope(scope string) bool {
	for _, candidate := range p.ScopeList() {
		if candidate == scope {
			return true
		}
	}
	return false
}

// ServicePrincipalSummary is safe to embed in business records. Control-plane
// state such as scopes, limits, expiry, emergency switches, and credentials is
// exposed only by administrator endpoints.
type ServicePrincipalSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (p *ServicePrincipal) ToSummary() *ServicePrincipalSummary {
	if p == nil {
		return nil
	}
	return &ServicePrincipalSummary{ID: p.ID, Name: p.Name}
}

type AgentCredentialStatus string

const (
	AgentCredentialStatusActive  AgentCredentialStatus = "active"
	AgentCredentialStatusRevoked AgentCredentialStatus = "revoked"
)

// AgentCredential stores only a keyed hash of the generated secret.
type AgentCredential struct {
	ID                 string                `json:"id" gorm:"primaryKey;size:36"`
	CreatedAt          time.Time             `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt          time.Time             `json:"updated_at" gorm:"autoUpdateTime"`
	ServicePrincipalID string                `json:"service_principal_id" gorm:"size:36;not null;index"`
	Name               string                `json:"name" gorm:"size:100;not null"`
	SecretHash         string                `json:"-" gorm:"size:64;not null"`
	Status             AgentCredentialStatus `json:"status" gorm:"size:20;not null;default:'active';index"`
	ExpiresAt          time.Time             `json:"expires_at" gorm:"not null;index"`
	LastUsedAt         *time.Time            `json:"last_used_at,omitempty"`
	RevokedAt          *time.Time            `json:"revoked_at,omitempty"`
	RevokedByActorType ActorType             `json:"revoked_by_actor_type,omitempty" gorm:"size:32"`
	RevokedByActorID   string                `json:"revoked_by_actor_id,omitempty" gorm:"size:128"`
}

func (AgentCredential) TableName() string {
	return "agent_credentials"
}

type AgentPolicyEffect string

const (
	AgentPolicyEffectAllow AgentPolicyEffect = "allow"
	AgentPolicyEffectDeny  AgentPolicyEffect = "deny"
)

// AgentPolicy refines a principal's scopes. Deny rules always win. Risky
// actions require an explicit matching allow rule.
type AgentPolicy struct {
	ID                 string            `json:"id" gorm:"primaryKey;size:36"`
	CreatedAt          time.Time         `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt          time.Time         `json:"updated_at" gorm:"autoUpdateTime"`
	ServicePrincipalID string            `json:"service_principal_id" gorm:"size:36;not null;index"`
	Name               string            `json:"name" gorm:"size:100;not null"`
	Effect             AgentPolicyEffect `json:"effect" gorm:"size:10;not null;index"`
	Scope              string            `json:"scope" gorm:"size:100;not null;index"`
	Action             string            `json:"action" gorm:"size:100;index"`
	ResourceType       string            `json:"resource_type" gorm:"size:100;index"`
	ResourceID         string            `json:"resource_id" gorm:"size:128;index"`
	Conditions         datatypes.JSON    `json:"conditions,omitempty" gorm:"type:jsonb"`
	Priority           int               `json:"priority" gorm:"not null;default:0;index"`
	IsActive           bool              `json:"is_active" gorm:"not null;default:true;index"`
	ExpiresAt          *time.Time        `json:"expires_at,omitempty" gorm:"index"`
}

func (AgentPolicy) TableName() string {
	return "agent_policies"
}

// PolicyDecision is immutable evidence of an authorization decision.
type PolicyDecision struct {
	ID                 string         `json:"id" gorm:"primaryKey;size:36"`
	CreatedAt          time.Time      `json:"created_at" gorm:"autoCreateTime;index"`
	OrganizationID     uint           `json:"organization_id" gorm:"not null;index"`
	ProjectID          uint           `json:"project_id" gorm:"not null;index"`
	ServicePrincipalID string         `json:"service_principal_id" gorm:"size:36;index"`
	CredentialID       string         `json:"credential_id,omitempty" gorm:"size:36;index"`
	ActorType          ActorType      `json:"actor_type" gorm:"size:32;not null;index"`
	ActorID            string         `json:"actor_id" gorm:"size:128;not null;index"`
	Scope              string         `json:"scope" gorm:"size:100;not null;index"`
	Action             string         `json:"action" gorm:"size:100;index"`
	ResourceType       string         `json:"resource_type" gorm:"size:100;index"`
	ResourceID         string         `json:"resource_id" gorm:"size:128;index"`
	IsWrite            bool           `json:"is_write" gorm:"not null;default:false"`
	IsRisky            bool           `json:"is_risky" gorm:"not null;default:false"`
	Allowed            bool           `json:"allowed" gorm:"not null;index"`
	ReasonCode         string         `json:"reason_code" gorm:"size:100;not null;index"`
	MatchedPolicyID    string         `json:"matched_policy_id,omitempty" gorm:"size:36;index"`
	RequestDigest      string         `json:"request_digest,omitempty" gorm:"size:64"`
	SourceProtocol     string         `json:"source_protocol,omitempty" gorm:"size:32"`
	Context            datatypes.JSON `json:"context,omitempty" gorm:"type:jsonb"`
}

func (PolicyDecision) TableName() string {
	return "policy_decisions"
}

// DomainEvent is both the durable event log and the persisted CloudEvents 1.0
// envelope. Data contains JSON and is never interpreted as instructions.
type DomainEvent struct {
	ID                   string         `json:"id" gorm:"primaryKey;size:36"`
	CreatedAt            time.Time      `json:"created_at" gorm:"autoCreateTime;index"`
	OrganizationID       uint           `json:"organizationid" gorm:"not null;index"`
	ProjectID            uint           `json:"projectid" gorm:"not null;index"`
	ConfigurationVersion string         `json:"configurationversion,omitempty" gorm:"size:100;index"`
	PolicyDecisionID     string         `json:"policydecisionid,omitempty" gorm:"size:36;index"`
	SpecVersion          string         `json:"specversion" gorm:"size:10;not null;default:'1.0'"`
	Source               string         `json:"source" gorm:"size:255;not null;index"`
	Type                 string         `json:"type" gorm:"size:255;not null;index"`
	Subject              string         `json:"subject" gorm:"size:255;index"`
	Time                 time.Time      `json:"time" gorm:"not null;index"`
	DataContentType      string         `json:"datacontenttype" gorm:"size:100;not null;default:'application/json'"`
	DataSchema           string         `json:"dataschema,omitempty" gorm:"size:500"`
	Data                 datatypes.JSON `json:"data" gorm:"type:jsonb;not null"`

	TraceID         string     `json:"trace_id,omitempty" gorm:"size:128;index"`
	CorrelationID   string     `json:"correlation_id,omitempty" gorm:"size:255;index"`
	CausationID     string     `json:"causation_id,omitempty" gorm:"size:255;index"`
	ActorType       ActorType  `json:"actor_type" gorm:"size:32;not null;index"`
	ActorID         string     `json:"actor_id" gorm:"size:128;not null;index"`
	ResourceVersion uint64     `json:"resource_version" gorm:"not null;default:1"`
	PublishedAt     *time.Time `json:"published_at,omitempty" gorm:"index"`

	Deliveries []OutboxDelivery `json:"deliveries,omitempty" gorm:"foreignKey:EventID"`
}

func (DomainEvent) TableName() string {
	return "domain_events"
}

type OutboxDeliveryStatus string

const (
	OutboxDeliveryPending    OutboxDeliveryStatus = "pending"
	OutboxDeliveryProcessing OutboxDeliveryStatus = "processing"
	OutboxDeliverySucceeded  OutboxDeliveryStatus = "succeeded"
	OutboxDeliveryFailed     OutboxDeliveryStatus = "failed"
	OutboxDeliveryDead       OutboxDeliveryStatus = "dead"
)

// OutboxDelivery tracks independent delivery state for each event target.
type OutboxDelivery struct {
	ID              string               `json:"id" gorm:"primaryKey;size:36"`
	CreatedAt       time.Time            `json:"created_at" gorm:"autoCreateTime;index"`
	UpdatedAt       time.Time            `json:"updated_at" gorm:"autoUpdateTime"`
	OrganizationID  uint                 `json:"organization_id" gorm:"not null;index"`
	ProjectID       uint                 `json:"project_id" gorm:"not null;index"`
	EventID         string               `json:"event_id" gorm:"size:36;not null;uniqueIndex:idx_event_destination,priority:1"`
	Event           *DomainEvent         `json:"event,omitempty" gorm:"foreignKey:EventID"`
	DestinationType string               `json:"destination_type" gorm:"size:50;not null;uniqueIndex:idx_event_destination,priority:2;index"`
	DestinationID   string               `json:"destination_id" gorm:"size:128;not null;uniqueIndex:idx_event_destination,priority:3;index"`
	Status          OutboxDeliveryStatus `json:"status" gorm:"size:20;not null;default:'pending';index"`
	Attempts        int                  `json:"attempts" gorm:"not null;default:0"`
	MaxAttempts     int                  `json:"max_attempts" gorm:"not null;default:8"`
	NextAttemptAt   time.Time            `json:"next_attempt_at" gorm:"not null;index"`
	LockedAt        *time.Time           `json:"locked_at,omitempty" gorm:"index"`
	LockedBy        string               `json:"locked_by,omitempty" gorm:"size:100;index"`
	LastError       string               `json:"last_error,omitempty" gorm:"type:text"`
	DeliveredAt     *time.Time           `json:"delivered_at,omitempty" gorm:"index"`
}

func (OutboxDelivery) TableName() string {
	return "outbox_deliveries"
}

type IdempotencyState string

const (
	IdempotencyStateProcessing IdempotencyState = "processing"
	IdempotencyStateCompleted  IdempotencyState = "completed"
	IdempotencyStateFailed     IdempotencyState = "failed"
)

// IdempotencyRecord is unique for an actor, operation and caller-supplied key.
type IdempotencyRecord struct {
	ID               string           `json:"id" gorm:"primaryKey;size:36"`
	CreatedAt        time.Time        `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt        time.Time        `json:"updated_at" gorm:"autoUpdateTime"`
	OrganizationID   uint             `json:"organization_id" gorm:"not null;index;uniqueIndex:idx_idempotency_actor_operation_key,priority:1"`
	ProjectID        uint             `json:"project_id" gorm:"not null;index;uniqueIndex:idx_idempotency_actor_operation_key,priority:2"`
	ActorType        ActorType        `json:"actor_type" gorm:"size:32;not null;uniqueIndex:idx_idempotency_actor_operation_key,priority:3"`
	ActorID          string           `json:"actor_id" gorm:"size:128;not null;uniqueIndex:idx_idempotency_actor_operation_key,priority:4"`
	Operation        string           `json:"operation" gorm:"size:128;not null;uniqueIndex:idx_idempotency_actor_operation_key,priority:5"`
	Key              string           `json:"key" gorm:"size:255;not null;uniqueIndex:idx_idempotency_actor_operation_key,priority:6"`
	RequestHash      string           `json:"request_hash" gorm:"size:64;not null"`
	State            IdempotencyState `json:"state" gorm:"size:20;not null;default:'processing';index"`
	ResponseCode     int              `json:"response_code,omitempty"`
	ResponseBody     datatypes.JSON   `json:"response_body,omitempty" gorm:"type:jsonb"`
	ResourceSnapshot datatypes.JSON   `json:"resource_snapshot,omitempty" gorm:"type:jsonb"`
	ResourceID       string           `json:"resource_id,omitempty" gorm:"size:128;index"`
	EventID          string           `json:"event_id,omitempty" gorm:"size:36;index"`
	LastErrorCode    string           `json:"last_error_code,omitempty" gorm:"size:100"`
	ExpiresAt        time.Time        `json:"expires_at" gorm:"not null;index"`
	// CompletionTTLNanoseconds preserves the caller-requested replay retention
	// separately from ExpiresAt, which is a short lease while processing.
	CompletionTTLNanoseconds int64      `json:"-" gorm:"not null;default:0"`
	CompletedAt              *time.Time `json:"completed_at,omitempty"`
}

func (IdempotencyRecord) TableName() string {
	return "idempotency_records"
}

// TicketLease coordinates multiple agents working the same ticket.
type TicketLease struct {
	ID              string     `json:"lease_id" gorm:"primaryKey;size:36"`
	CreatedAt       time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt       time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
	OrganizationID  uint       `json:"organization_id" gorm:"not null;index"`
	ProjectID       uint       `json:"project_id" gorm:"not null;index"`
	TicketID        uint       `json:"ticket_id" gorm:"not null;uniqueIndex"`
	HolderActorType ActorType  `json:"holder_actor_type" gorm:"size:32;not null;index"`
	HolderActorID   string     `json:"holder_actor_id" gorm:"size:128;not null;index"`
	TicketVersion   uint64     `json:"ticket_version" gorm:"not null"`
	ExpiresAt       time.Time  `json:"expires_at" gorm:"not null;index"`
	LastHeartbeatAt time.Time  `json:"last_heartbeat_at" gorm:"not null"`
	ReleasedAt      *time.Time `json:"released_at,omitempty" gorm:"index"`
	ReleaseReason   string     `json:"release_reason,omitempty" gorm:"size:255"`
}

func (TicketLease) TableName() string {
	return "ticket_leases"
}

func (lease *TicketLease) BeforeCreate(tx *gorm.DB) error {
	return inheritTicketProjectScope(
		tx,
		lease.TicketID,
		&lease.OrganizationID,
		&lease.ProjectID,
	)
}

func (l *TicketLease) IsActive(now time.Time) bool {
	return l.ReleasedAt == nil && l.ExpiresAt.After(now)
}
