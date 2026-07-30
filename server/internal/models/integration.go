package models

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

var (
	integrationKeyPattern        = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	ErrPublishedMappingImmutable = errors.New("published integration mapping is immutable")
)

type ConnectorDirection string

const (
	ConnectorDirectionInbound       ConnectorDirection = "inbound"
	ConnectorDirectionOutbound      ConnectorDirection = "outbound"
	ConnectorDirectionBidirectional ConnectorDirection = "bidirectional"
)

type ConnectorDefinitionStatus string

const (
	ConnectorDefinitionStatusActive   ConnectorDefinitionStatus = "active"
	ConnectorDefinitionStatusDisabled ConnectorDefinitionStatus = "disabled"
	ConnectorDefinitionStatusArchived ConnectorDefinitionStatus = "archived"
)

type ConnectionStatus string

const (
	ConnectionStatusActive   ConnectionStatus = "active"
	ConnectionStatusInactive ConnectionStatus = "inactive"
	ConnectionStatusError    ConnectionStatus = "error"
	ConnectionStatusArchived ConnectionStatus = "archived"
)

type MappingVersionStatus string

const (
	MappingVersionStatusDraft     MappingVersionStatus = "draft"
	MappingVersionStatusPublished MappingVersionStatus = "published"
	MappingVersionStatusRetired   MappingVersionStatus = "retired"
)

type InboxMessageStatus string

const (
	InboxMessageStatusProcessing InboxMessageStatus = "processing"
	InboxMessageStatusCompleted  InboxMessageStatus = "completed"
	InboxMessageStatusConflict   InboxMessageStatus = "conflict"
	InboxMessageStatusDeadLetter InboxMessageStatus = "dead_letter"
)

type InboxReceiptStatus string

const (
	InboxReceiptStatusApplied InboxReceiptStatus = "applied"
	InboxReceiptStatusNoop    InboxReceiptStatus = "noop"
)

type SyncDirection string

const (
	SyncDirectionInbound  SyncDirection = "inbound"
	SyncDirectionOutbound SyncDirection = "outbound"
)

type SyncRunStatus string

const (
	SyncRunStatusPending   SyncRunStatus = "pending"
	SyncRunStatusRunning   SyncRunStatus = "running"
	SyncRunStatusSucceeded SyncRunStatus = "succeeded"
	SyncRunStatusFailed    SyncRunStatus = "failed"
	SyncRunStatusConflict  SyncRunStatus = "conflict"
	SyncRunStatusCancelled SyncRunStatus = "cancelled"
)

type IntegrationConflictType string

const (
	IntegrationConflictMessageIdentityReuse  IntegrationConflictType = "message_identity_reuse"
	IntegrationConflictExternalLinkMismatch  IntegrationConflictType = "external_link_mismatch"
	IntegrationConflictInternalLinkCollision IntegrationConflictType = "internal_link_collision"
)

type IntegrationConflictStatus string

const (
	IntegrationConflictStatusOpen     IntegrationConflictStatus = "open"
	IntegrationConflictStatusResolved IntegrationConflictStatus = "resolved"
	IntegrationConflictStatusIgnored  IntegrationConflictStatus = "ignored"
)

type DeadLetterStatus string

const (
	DeadLetterStatusOpen     DeadLetterStatus = "open"
	DeadLetterStatusRequeued DeadLetterStatus = "requeued"
	DeadLetterStatusResolved DeadLetterStatus = "resolved"
)

// ConnectorDefinition describes one project-owned integration protocol. Schema
// and capability JSON are declarative data and are never executed as prompts.
type ConnectorDefinition struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	PublicID  string    `json:"public_id" gorm:"size:36;not null;uniqueIndex;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	OrganizationID             uint                      `json:"organization_id" gorm:"not null;index"`
	ProjectID                  uint                      `json:"project_id" gorm:"not null;index;uniqueIndex:idx_connector_definitions_project_key,priority:1"`
	Project                    Project                   `json:"project,omitempty" gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	Key                        string                    `json:"key" gorm:"size:64;not null;uniqueIndex:idx_connector_definitions_project_key,priority:2"`
	Name                       string                    `json:"name" gorm:"size:120;not null"`
	Description                string                    `json:"description" gorm:"size:500"`
	Kind                       string                    `json:"kind" gorm:"size:64;not null;index"`
	Direction                  ConnectorDirection        `json:"direction" gorm:"size:20;not null;index;check:chk_connector_definitions_direction,direction IN ('inbound','outbound','bidirectional')"`
	Status                     ConnectorDefinitionStatus `json:"status" gorm:"size:20;not null;default:'active';index;check:chk_connector_definitions_status,status IN ('active','disabled','archived')"`
	SignatureScheme            string                    `json:"signature_scheme" gorm:"size:64;not null"`
	DefaultReplayWindowSeconds int                       `json:"default_replay_window_seconds" gorm:"not null;default:300"`
	ConfigurationSchema        datatypes.JSON            `json:"configuration_schema,omitempty" gorm:"type:jsonb"`
	MappingSchema              datatypes.JSON            `json:"mapping_schema,omitempty" gorm:"type:jsonb"`
}

func (ConnectorDefinition) TableName() string {
	return "connector_definitions"
}

func (definition *ConnectorDefinition) BeforeCreate(_ *gorm.DB) error {
	if err := validateIntegrationKey(definition.Key); err != nil {
		return fmt.Errorf("invalid connector definition key: %w", err)
	}
	if definition.ProjectID == 0 {
		return errors.New("connector definition project id is required")
	}
	if definition.DefaultReplayWindowSeconds == 0 {
		definition.DefaultReplayWindowSeconds = 300
	}
	if definition.DefaultReplayWindowSeconds < 30 ||
		definition.DefaultReplayWindowSeconds > 86400 {
		return errors.New("connector replay window must be between 30 and 86400 seconds")
	}
	return ensureProjectPublicID(&definition.PublicID)
}

// Connection is a project-local installation of a ConnectorDefinition.
// VerificationKeyRef names protected key material held by the composition
// layer; plaintext credentials never belong in this record.
type Connection struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	PublicID  string    `json:"public_id" gorm:"size:36;not null;uniqueIndex;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	OrganizationID        uint                `json:"organization_id" gorm:"not null;index"`
	ProjectID             uint                `json:"project_id" gorm:"not null;index;uniqueIndex:idx_connections_project_key,priority:1"`
	Project               Project             `json:"project,omitempty" gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	ConnectorDefinitionID uint                `json:"connector_definition_id" gorm:"not null;index"`
	ConnectorDefinition   ConnectorDefinition `json:"connector_definition,omitempty" gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
	Key                   string              `json:"key" gorm:"size:64;not null;uniqueIndex:idx_connections_project_key,priority:2"`
	Name                  string              `json:"name" gorm:"size:120;not null"`
	Description           string              `json:"description" gorm:"size:500"`
	Status                ConnectionStatus    `json:"status" gorm:"size:20;not null;default:'active';index;check:chk_connections_status,status IN ('active','inactive','error','archived')"`
	Configuration         datatypes.JSON      `json:"configuration,omitempty" gorm:"type:jsonb"`
	VerificationKeyRef    string              `json:"verification_key_ref,omitempty" gorm:"size:191"`
	ReplayWindowSeconds   int                 `json:"replay_window_seconds" gorm:"not null;default:300"`
	ActorType             ActorType           `json:"actor_type" gorm:"size:32;not null;default:'system'"`
	ActorID               string              `json:"actor_id" gorm:"size:128;not null"`
	ActorCredentialID     string              `json:"actor_credential_id,omitempty" gorm:"size:36"`
	LastVerifiedAt        *time.Time          `json:"last_verified_at,omitempty" gorm:"index"`
	LastErrorAt           *time.Time          `json:"last_error_at,omitempty" gorm:"index"`
	LastErrorCode         string              `json:"last_error_code,omitempty" gorm:"size:100;index"`
}

func (Connection) TableName() string {
	return "connections"
}

func (connection *Connection) BeforeCreate(_ *gorm.DB) error {
	if err := validateIntegrationKey(connection.Key); err != nil {
		return fmt.Errorf("invalid connection key: %w", err)
	}
	if connection.ProjectID == 0 || connection.ConnectorDefinitionID == 0 {
		return errors.New("connection project and connector definition are required")
	}
	if connection.ReplayWindowSeconds == 0 {
		connection.ReplayWindowSeconds = 300
	}
	if connection.ReplayWindowSeconds < 30 || connection.ReplayWindowSeconds > 86400 {
		return errors.New("connection replay window must be between 30 and 86400 seconds")
	}
	if err := ensureProjectPublicID(&connection.PublicID); err != nil {
		return err
	}
	if connection.ActorType == "" {
		connection.ActorType = ActorTypeSystem
	}
	if strings.TrimSpace(connection.ActorID) == "" {
		connection.ActorID = "connector:" + connection.PublicID
	}
	if err := (ActorRef{Type: connection.ActorType, ID: connection.ActorID}).Validate(); err != nil {
		return err
	}
	if connection.ActorType == ActorTypeServicePrincipal &&
		strings.TrimSpace(connection.ActorCredentialID) == "" {
		return errors.New("service-principal connection requires an actor credential id")
	}
	if connection.ActorType != ActorTypeServicePrincipal &&
		strings.TrimSpace(connection.ActorCredentialID) != "" {
		return errors.New("only service-principal connections may carry an actor credential id")
	}
	return nil
}

func (connection Connection) Actor() ActorRef {
	return ActorRef{Type: connection.ActorType, ID: connection.ActorID}
}

// MappingVersion is append-only once published. Drafts may be dry-run and
// edited; publication fixes the mapping bytes and their digest permanently.
type MappingVersion struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	PublicID  string    `json:"public_id" gorm:"size:36;not null;uniqueIndex;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	OrganizationID   uint                 `json:"organization_id" gorm:"not null;index"`
	ProjectID        uint                 `json:"project_id" gorm:"not null;index;uniqueIndex:idx_mapping_versions_project_connection_key_version,priority:1"`
	Project          Project              `json:"project,omitempty" gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	ConnectionID     uint                 `json:"connection_id" gorm:"not null;index;uniqueIndex:idx_mapping_versions_project_connection_key_version,priority:2"`
	Connection       Connection           `json:"connection,omitempty" gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	Key              string               `json:"key" gorm:"size:64;not null;uniqueIndex:idx_mapping_versions_project_connection_key_version,priority:3"`
	Version          uint                 `json:"version" gorm:"not null;uniqueIndex:idx_mapping_versions_project_connection_key_version,priority:4"`
	Status           MappingVersionStatus `json:"status" gorm:"size:20;not null;default:'draft';index;check:chk_mapping_versions_status,status IN ('draft','published','retired')"`
	SourceSchema     datatypes.JSON       `json:"source_schema,omitempty" gorm:"type:jsonb"`
	TargetCommand    string               `json:"target_command" gorm:"size:128;not null;index"`
	Definition       datatypes.JSON       `json:"definition" gorm:"type:jsonb;not null"`
	DefinitionDigest string               `json:"definition_digest" gorm:"size:64;not null"`
	PublishedAt      *time.Time           `json:"published_at,omitempty" gorm:"index"`
	PublishedByType  ActorType            `json:"published_by_type,omitempty" gorm:"size:32"`
	PublishedByID    string               `json:"published_by_id,omitempty" gorm:"size:128"`
}

func (MappingVersion) TableName() string {
	return "mapping_versions"
}

func (mapping *MappingVersion) BeforeCreate(_ *gorm.DB) error {
	if mapping.ProjectID == 0 || mapping.ConnectionID == 0 {
		return errors.New("mapping project and connection are required")
	}
	if err := validateIntegrationKey(mapping.Key); err != nil {
		return fmt.Errorf("invalid mapping key: %w", err)
	}
	if mapping.Version == 0 {
		return errors.New("mapping version must be positive")
	}
	if err := mapping.refreshDefinitionDigest(); err != nil {
		return err
	}
	if mapping.Status == "" {
		mapping.Status = MappingVersionStatusDraft
	}
	if mapping.Status == MappingVersionStatusPublished ||
		mapping.Status == MappingVersionStatusRetired {
		if err := mapping.validatePublication(); err != nil {
			return err
		}
	}
	return ensureProjectPublicID(&mapping.PublicID)
}

func (mapping *MappingVersion) BeforeUpdate(tx *gorm.DB) error {
	if mapping.ID == 0 {
		return errors.New("bulk mapping updates are not allowed")
	}
	var existing MappingVersion
	if err := tx.Session(&gorm.Session{NewDB: true, SkipHooks: true}).
		Select("id", "status").
		First(&existing, mapping.ID).Error; err != nil {
		return err
	}
	if existing.Status == MappingVersionStatusPublished ||
		existing.Status == MappingVersionStatusRetired {
		return ErrPublishedMappingImmutable
	}
	if mapping.Status == MappingVersionStatusRetired {
		return errors.New("draft mapping cannot be retired")
	}
	if err := mapping.refreshDefinitionDigest(); err != nil {
		return err
	}
	if mapping.Status == MappingVersionStatusPublished {
		if err := mapping.validatePublication(); err != nil {
			return err
		}
	}
	return nil
}

func (mapping *MappingVersion) BeforeDelete(tx *gorm.DB) error {
	if mapping.ID == 0 {
		return errors.New("bulk mapping deletes are not allowed")
	}
	var existing MappingVersion
	if err := tx.Session(&gorm.Session{NewDB: true, SkipHooks: true}).
		Select("id", "status").
		First(&existing, mapping.ID).Error; err != nil {
		return err
	}
	if existing.Status == MappingVersionStatusPublished ||
		existing.Status == MappingVersionStatusRetired {
		return ErrPublishedMappingImmutable
	}
	return nil
}

func (mapping *MappingVersion) Publish(actor ActorRef, publishedAt time.Time) error {
	if mapping.Status != "" && mapping.Status != MappingVersionStatusDraft {
		return errors.New("only draft mappings may be published")
	}
	if err := actor.Validate(); err != nil {
		return fmt.Errorf("invalid mapping publisher: %w", err)
	}
	if publishedAt.IsZero() {
		return errors.New("mapping publication time is required")
	}
	publishedAt = publishedAt.UTC()
	mapping.Status = MappingVersionStatusPublished
	mapping.PublishedAt = &publishedAt
	mapping.PublishedByType = actor.Type
	mapping.PublishedByID = actor.ID
	return nil
}

func (mapping MappingVersion) validatePublication() error {
	if mapping.PublishedAt == nil || mapping.PublishedAt.IsZero() {
		return errors.New("published mapping requires published_at")
	}
	return (ActorRef{
		Type: mapping.PublishedByType,
		ID:   mapping.PublishedByID,
	}).Validate()
}

func (mapping *MappingVersion) refreshDefinitionDigest() error {
	if len(mapping.Definition) == 0 || !json.Valid(mapping.Definition) {
		return errors.New("mapping definition must be valid JSON")
	}
	digest := sha256.Sum256(mapping.Definition)
	mapping.DefinitionDigest = hex.EncodeToString(digest[:])
	return nil
}

type InboxMessage struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	PublicID  string    `json:"public_id" gorm:"size:36;not null;uniqueIndex;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	OrganizationID       uint               `json:"organization_id" gorm:"not null;index"`
	ProjectID            uint               `json:"project_id" gorm:"not null;index;uniqueIndex:idx_inbox_messages_project_connection_external_id,priority:1"`
	ConnectionID         uint               `json:"connection_id" gorm:"not null;index;uniqueIndex:idx_inbox_messages_project_connection_external_id,priority:2"`
	MappingVersionID     uint               `json:"mapping_version_id" gorm:"not null;index"`
	ExternalMessageID    string             `json:"external_message_id" gorm:"size:191;not null;uniqueIndex:idx_inbox_messages_project_connection_external_id,priority:3"`
	ExternalResourceType string             `json:"external_resource_type" gorm:"size:64;not null;index"`
	ExternalResourceID   string             `json:"external_resource_id" gorm:"size:191;not null;index"`
	SignedAt             time.Time          `json:"signed_at" gorm:"not null;index"`
	ReceivedAt           time.Time          `json:"received_at" gorm:"not null;index"`
	ContentType          string             `json:"content_type" gorm:"size:128;not null"`
	Payload              []byte             `json:"-" gorm:"type:bytea;not null"`
	PayloadDigest        string             `json:"payload_digest" gorm:"size:64;not null;index"`
	SignatureDigest      string             `json:"signature_digest" gorm:"size:64;not null"`
	Status               InboxMessageStatus `json:"status" gorm:"size:20;not null;default:'processing';index;check:chk_inbox_messages_status,status IN ('processing','completed','conflict','dead_letter')"`
	ProcessedAt          *time.Time         `json:"processed_at,omitempty" gorm:"index"`
}

func (InboxMessage) TableName() string {
	return "inbox_messages"
}

func (message *InboxMessage) BeforeCreate(_ *gorm.DB) error {
	if message.ProjectID == 0 ||
		message.ConnectionID == 0 ||
		message.MappingVersionID == 0 {
		return errors.New("inbox message project, connection and mapping are required")
	}
	if strings.TrimSpace(message.ExternalMessageID) == "" {
		return errors.New("external message id is required")
	}
	if len(message.Payload) == 0 {
		return errors.New("inbox payload is required")
	}
	return ensureProjectPublicID(&message.PublicID)
}

type InboxReceipt struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	PublicID  string    `json:"public_id" gorm:"size:36;not null;uniqueIndex;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`

	OrganizationID  uint               `json:"organization_id" gorm:"not null;index"`
	ProjectID       uint               `json:"project_id" gorm:"not null;index"`
	ConnectionID    uint               `json:"connection_id" gorm:"not null;index"`
	InboxMessageID  uint               `json:"inbox_message_id" gorm:"not null;uniqueIndex"`
	Status          InboxReceiptStatus `json:"status" gorm:"size:20;not null;check:chk_inbox_receipts_status,status IN ('applied','noop')"`
	ResourceType    string             `json:"resource_type" gorm:"size:64;not null;index"`
	ResourceID      string             `json:"resource_id" gorm:"size:128;not null;index"`
	ResourceVersion uint64             `json:"resource_version" gorm:"not null"`
	EventID         string             `json:"event_id,omitempty" gorm:"size:64;index"`
	OperationID     string             `json:"operation_id,omitempty" gorm:"size:64;index"`
	Result          datatypes.JSON     `json:"result" gorm:"type:jsonb;not null"`
	ActorType       ActorType          `json:"actor_type" gorm:"size:32;not null"`
	ActorID         string             `json:"actor_id" gorm:"size:128;not null"`
	ProcessedAt     time.Time          `json:"processed_at" gorm:"not null;index"`
}

func (InboxReceipt) TableName() string {
	return "inbox_receipts"
}

func (receipt *InboxReceipt) BeforeCreate(_ *gorm.DB) error {
	if receipt.ProjectID == 0 ||
		receipt.ConnectionID == 0 ||
		receipt.InboxMessageID == 0 {
		return errors.New("inbox receipt scope and message are required")
	}
	if strings.TrimSpace(receipt.ResourceType) == "" ||
		strings.TrimSpace(receipt.ResourceID) == "" {
		return errors.New("inbox receipt resource is required")
	}
	if err := (ActorRef{Type: receipt.ActorType, ID: receipt.ActorID}).Validate(); err != nil {
		return err
	}
	if len(receipt.Result) == 0 {
		receipt.Result = datatypes.JSON([]byte(`{}`))
	}
	return ensureProjectPublicID(&receipt.PublicID)
}

type ExternalLink struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	PublicID  string    `json:"public_id" gorm:"size:36;not null;uniqueIndex;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	OrganizationID       uint   `json:"organization_id" gorm:"not null;index"`
	ProjectID            uint   `json:"project_id" gorm:"not null;index;uniqueIndex:idx_external_links_external_identity,priority:1;uniqueIndex:idx_external_links_internal_identity,priority:1"`
	ConnectionID         uint   `json:"connection_id" gorm:"not null;index;uniqueIndex:idx_external_links_external_identity,priority:2;uniqueIndex:idx_external_links_internal_identity,priority:2"`
	ExternalResourceType string `json:"external_resource_type" gorm:"size:64;not null;uniqueIndex:idx_external_links_external_identity,priority:3"`
	ExternalResourceID   string `json:"external_resource_id" gorm:"size:191;not null;uniqueIndex:idx_external_links_external_identity,priority:4"`
	InternalResourceType string `json:"internal_resource_type" gorm:"size:64;not null;uniqueIndex:idx_external_links_internal_identity,priority:3"`
	InternalResourceID   string `json:"internal_resource_id" gorm:"size:128;not null;uniqueIndex:idx_external_links_internal_identity,priority:4"`
	MappingVersionID     uint   `json:"mapping_version_id" gorm:"not null;index"`
	ExternalVersion      string `json:"external_version,omitempty" gorm:"size:128"`
	InternalVersion      uint64 `json:"internal_version" gorm:"not null"`
	LastInboxMessageID   uint   `json:"last_inbox_message_id" gorm:"not null;index"`
}

func (ExternalLink) TableName() string {
	return "external_links"
}

func (link *ExternalLink) BeforeCreate(_ *gorm.DB) error {
	if link.ProjectID == 0 ||
		link.ConnectionID == 0 ||
		link.MappingVersionID == 0 ||
		link.LastInboxMessageID == 0 {
		return errors.New("external link scope, mapping and inbox message are required")
	}
	if strings.TrimSpace(link.ExternalResourceType) == "" ||
		strings.TrimSpace(link.ExternalResourceID) == "" ||
		strings.TrimSpace(link.InternalResourceType) == "" ||
		strings.TrimSpace(link.InternalResourceID) == "" {
		return errors.New("external and internal link identities are required")
	}
	return ensureProjectPublicID(&link.PublicID)
}

type SyncCursor struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	OrganizationID uint          `json:"organization_id" gorm:"not null;index"`
	ProjectID      uint          `json:"project_id" gorm:"not null;index;uniqueIndex:idx_sync_cursors_project_connection_direction_stream,priority:1"`
	ConnectionID   uint          `json:"connection_id" gorm:"not null;index;uniqueIndex:idx_sync_cursors_project_connection_direction_stream,priority:2"`
	Stream         string        `json:"stream" gorm:"size:128;not null;uniqueIndex:idx_sync_cursors_project_connection_direction_stream,priority:4"`
	Direction      SyncDirection `json:"direction" gorm:"size:20;not null;uniqueIndex:idx_sync_cursors_project_connection_direction_stream,priority:3;check:chk_sync_cursors_direction,direction IN ('inbound','outbound')"`
	Cursor         string        `json:"cursor" gorm:"type:text;not null"`
	Version        uint64        `json:"version" gorm:"not null;default:1"`
	LastRunID      *uint         `json:"last_run_id,omitempty" gorm:"index"`
}

func (SyncCursor) TableName() string {
	return "sync_cursors"
}

func (cursor *SyncCursor) BeforeCreate(_ *gorm.DB) error {
	if cursor.ProjectID == 0 || cursor.ConnectionID == 0 {
		return errors.New("sync cursor project and connection are required")
	}
	if strings.TrimSpace(cursor.Stream) == "" {
		return errors.New("sync cursor stream is required")
	}
	if cursor.Version == 0 {
		cursor.Version = 1
	}
	return nil
}

type SyncRun struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	PublicID  string    `json:"public_id" gorm:"size:36;not null;uniqueIndex;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	OrganizationID uint          `json:"organization_id" gorm:"not null;index"`
	ProjectID      uint          `json:"project_id" gorm:"not null;index;uniqueIndex:idx_sync_runs_project_connection_run_key,priority:1"`
	ConnectionID   uint          `json:"connection_id" gorm:"not null;index;uniqueIndex:idx_sync_runs_project_connection_run_key,priority:2"`
	RunKey         string        `json:"run_key" gorm:"size:191;not null;uniqueIndex:idx_sync_runs_project_connection_run_key,priority:3"`
	Direction      SyncDirection `json:"direction" gorm:"size:20;not null;index;check:chk_sync_runs_direction,direction IN ('inbound','outbound')"`
	Status         SyncRunStatus `json:"status" gorm:"size:20;not null;default:'pending';index;check:chk_sync_runs_status,status IN ('pending','running','succeeded','failed','conflict','cancelled')"`
	StartedAt      *time.Time    `json:"started_at,omitempty" gorm:"index"`
	FinishedAt     *time.Time    `json:"finished_at,omitempty" gorm:"index"`
	CursorBefore   string        `json:"cursor_before,omitempty" gorm:"type:text"`
	CursorAfter    string        `json:"cursor_after,omitempty" gorm:"type:text"`
	ProcessedCount int64         `json:"processed_count" gorm:"not null;default:0"`
	SucceededCount int64         `json:"succeeded_count" gorm:"not null;default:0"`
	FailedCount    int64         `json:"failed_count" gorm:"not null;default:0"`
	ConflictCount  int64         `json:"conflict_count" gorm:"not null;default:0"`
	ErrorCode      string        `json:"error_code,omitempty" gorm:"size:100;index"`
	ErrorSummary   string        `json:"error_summary,omitempty" gorm:"size:500"`
}

func (SyncRun) TableName() string {
	return "sync_runs"
}

func (run *SyncRun) BeforeCreate(_ *gorm.DB) error {
	if run.ProjectID == 0 || run.ConnectionID == 0 {
		return errors.New("sync run project and connection are required")
	}
	if strings.TrimSpace(run.RunKey) == "" {
		return errors.New("sync run key is required")
	}
	return ensureProjectPublicID(&run.PublicID)
}

type IntegrationConflict struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	PublicID  string    `json:"public_id" gorm:"size:36;not null;uniqueIndex;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	OrganizationID               uint                      `json:"organization_id" gorm:"not null;index"`
	ProjectID                    uint                      `json:"project_id" gorm:"not null;index;uniqueIndex:idx_integration_conflicts_project_connection_key,priority:1"`
	ConnectionID                 uint                      `json:"connection_id" gorm:"not null;index;uniqueIndex:idx_integration_conflicts_project_connection_key,priority:2"`
	InboxMessageID               uint                      `json:"inbox_message_id" gorm:"not null;index"`
	ConflictKey                  string                    `json:"conflict_key" gorm:"size:64;not null;uniqueIndex:idx_integration_conflicts_project_connection_key,priority:3"`
	Type                         IntegrationConflictType   `json:"type" gorm:"size:64;not null;index"`
	Status                       IntegrationConflictStatus `json:"status" gorm:"size:20;not null;default:'open';index;check:chk_integration_conflicts_status,status IN ('open','resolved','ignored')"`
	ExternalResourceType         string                    `json:"external_resource_type" gorm:"size:64;not null;index"`
	ExternalResourceID           string                    `json:"external_resource_id" gorm:"size:191;not null;index"`
	ExistingPayloadDigest        string                    `json:"existing_payload_digest,omitempty" gorm:"size:64"`
	IncomingPayloadDigest        string                    `json:"incoming_payload_digest,omitempty" gorm:"size:64"`
	ExistingInternalResourceType string                    `json:"existing_internal_resource_type,omitempty" gorm:"size:64"`
	ExistingInternalResourceID   string                    `json:"existing_internal_resource_id,omitempty" gorm:"size:128"`
	IncomingInternalResourceType string                    `json:"incoming_internal_resource_type,omitempty" gorm:"size:64"`
	IncomingInternalResourceID   string                    `json:"incoming_internal_resource_id,omitempty" gorm:"size:128"`
	Details                      datatypes.JSON            `json:"details" gorm:"type:jsonb;not null"`
	ResolvedAt                   *time.Time                `json:"resolved_at,omitempty" gorm:"index"`
	ResolvedByType               ActorType                 `json:"resolved_by_type,omitempty" gorm:"size:32"`
	ResolvedByID                 string                    `json:"resolved_by_id,omitempty" gorm:"size:128"`
}

func (IntegrationConflict) TableName() string {
	return "integration_conflicts"
}

func (conflict *IntegrationConflict) BeforeCreate(_ *gorm.DB) error {
	if conflict.ProjectID == 0 ||
		conflict.ConnectionID == 0 ||
		conflict.InboxMessageID == 0 ||
		strings.TrimSpace(conflict.ConflictKey) == "" {
		return errors.New("integration conflict scope, message and key are required")
	}
	if len(conflict.Details) == 0 {
		conflict.Details = datatypes.JSON([]byte(`{}`))
	}
	return ensureProjectPublicID(&conflict.PublicID)
}

type DeadLetter struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	PublicID  string    `json:"public_id" gorm:"size:36;not null;uniqueIndex;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	OrganizationID uint             `json:"organization_id" gorm:"not null;index"`
	ProjectID      uint             `json:"project_id" gorm:"not null;index"`
	ConnectionID   uint             `json:"connection_id" gorm:"not null;index"`
	InboxMessageID uint             `json:"inbox_message_id" gorm:"not null;uniqueIndex"`
	Status         DeadLetterStatus `json:"status" gorm:"size:20;not null;default:'open';index;check:chk_dead_letters_status,status IN ('open','requeued','resolved')"`
	ReasonCode     string           `json:"reason_code" gorm:"size:100;not null;index"`
	ErrorSummary   string           `json:"error_summary" gorm:"size:500;not null"`
	PayloadDigest  string           `json:"payload_digest" gorm:"size:64;not null;index"`
	AttemptCount   int              `json:"attempt_count" gorm:"not null;default:1"`
	NextAttemptAt  *time.Time       `json:"next_attempt_at,omitempty" gorm:"index"`
	ResolvedAt     *time.Time       `json:"resolved_at,omitempty" gorm:"index"`
	ResolvedByType ActorType        `json:"resolved_by_type,omitempty" gorm:"size:32"`
	ResolvedByID   string           `json:"resolved_by_id,omitempty" gorm:"size:128"`
}

func (DeadLetter) TableName() string {
	return "dead_letters"
}

func (letter *DeadLetter) BeforeCreate(_ *gorm.DB) error {
	if letter.ProjectID == 0 ||
		letter.ConnectionID == 0 ||
		letter.InboxMessageID == 0 {
		return errors.New("dead letter scope and inbox message are required")
	}
	if strings.TrimSpace(letter.ReasonCode) == "" {
		return errors.New("dead letter reason code is required")
	}
	if letter.AttemptCount <= 0 {
		letter.AttemptCount = 1
	}
	return ensureProjectPublicID(&letter.PublicID)
}

func validateIntegrationKey(value string) error {
	if !integrationKeyPattern.MatchString(value) {
		return fmt.Errorf("key must match %s", integrationKeyPattern.String())
	}
	return nil
}
