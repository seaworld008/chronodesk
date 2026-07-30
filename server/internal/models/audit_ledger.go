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

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	AuditLedgerSchemaVersion = "chronodesk.audit-ledger.v1"
	AuditLedgerGenesisHash   = "0000000000000000000000000000000000000000000000000000000000000000"
)

var (
	ErrAuditLedgerAppendOnly = errors.New("audit ledger entries are append-only")
	auditLedgerHashPattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	auditLedgerTypePattern   = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
)

type AuditLedgerOutcome string

const (
	AuditLedgerOutcomeSucceeded AuditLedgerOutcome = "succeeded"
	AuditLedgerOutcomeDenied    AuditLedgerOutcome = "denied"
	AuditLedgerOutcomeFailed    AuditLedgerOutcome = "failed"
)

func (outcome AuditLedgerOutcome) IsValid() bool {
	switch outcome {
	case AuditLedgerOutcomeSucceeded,
		AuditLedgerOutcomeDenied,
		AuditLedgerOutcomeFailed:
		return true
	default:
		return false
	}
}

// AuditLedgerEntry is an immutable, project-scoped link in the tamper-evident
// audit chain. It stores only a digest of the audited payload.
type AuditLedgerEntry struct {
	ID         string    `json:"id" gorm:"primaryKey;size:36;<-:create"`
	OccurredAt time.Time `json:"occurred_at" gorm:"not null;index;<-:create"`

	OrganizationID uint   `json:"organization_id" gorm:"not null;index;uniqueIndex:idx_audit_ledger_project_sequence,priority:1;uniqueIndex:idx_audit_ledger_project_event,priority:1;<-:create"`
	ProjectID      uint   `json:"project_id" gorm:"not null;index;uniqueIndex:idx_audit_ledger_project_sequence,priority:2;uniqueIndex:idx_audit_ledger_project_event,priority:2;<-:create"`
	Sequence       uint64 `json:"sequence" gorm:"not null;uniqueIndex:idx_audit_ledger_project_sequence,priority:3;<-:create"`
	PreviousHash   string `json:"previous_hash" gorm:"size:64;not null;index;<-:create"`
	EntryHash      string `json:"entry_hash" gorm:"size:64;not null;uniqueIndex;<-:create"`
	PayloadDigest  string `json:"payload_digest" gorm:"size:64;not null;index;<-:create"`

	EventType       string             `json:"event_type" gorm:"size:128;not null;index;<-:create"`
	ResourceType    string             `json:"resource_type" gorm:"size:128;not null;index;<-:create"`
	ResourceID      string             `json:"resource_id" gorm:"size:128;not null;index;<-:create"`
	ResourceVersion uint64             `json:"resource_version" gorm:"not null;<-:create"`
	Outcome         AuditLedgerOutcome `json:"outcome" gorm:"size:20;not null;index;<-:create"`

	ActorType ActorType `json:"actor_type" gorm:"size:32;not null;index;<-:create"`
	ActorID   string    `json:"actor_id" gorm:"size:128;not null;index;<-:create"`

	DomainEventID        string `json:"domain_event_id" gorm:"size:128;not null;index;uniqueIndex:idx_audit_ledger_project_event,priority:3;<-:create"`
	ConfigurationVersion string `json:"configuration_version,omitempty" gorm:"size:128;index;<-:create"`
	PolicyVersion        string `json:"policy_version,omitempty" gorm:"size:128;index;<-:create"`
	TraceID              string `json:"trace_id,omitempty" gorm:"size:128;index;<-:create"`
	CorrelationID        string `json:"correlation_id,omitempty" gorm:"size:255;index;<-:create"`
}

func (AuditLedgerEntry) TableName() string {
	return "audit_ledger_entries"
}

func (entry *AuditLedgerEntry) BeforeCreate(_ *gorm.DB) error {
	if err := ensureAuditLedgerUUID(&entry.ID); err != nil {
		return err
	}
	if entry.OccurredAt.IsZero() {
		return errors.New("audit ledger occurrence time is required")
	}
	entry.OccurredAt = entry.OccurredAt.UTC().Round(0)
	if entry.EntryHash == "" {
		computed, err := entry.ComputeEntryHash()
		if err != nil {
			return err
		}
		entry.EntryHash = computed
	}
	return entry.Validate()
}

func (*AuditLedgerEntry) BeforeUpdate(_ *gorm.DB) error {
	return ErrAuditLedgerAppendOnly
}

func (*AuditLedgerEntry) BeforeDelete(_ *gorm.DB) error {
	return ErrAuditLedgerAppendOnly
}

func (entry AuditLedgerEntry) Actor() ActorRef {
	return ActorRef{Type: entry.ActorType, ID: entry.ActorID}
}

func (entry AuditLedgerEntry) ComputeEntryHash() (string, error) {
	if err := entry.validateCanonicalFields(); err != nil {
		return "", err
	}
	canonical := auditLedgerCanonicalEntry{
		SchemaVersion:        AuditLedgerSchemaVersion,
		ID:                   entry.ID,
		OrganizationID:       entry.OrganizationID,
		ProjectID:            entry.ProjectID,
		Sequence:             entry.Sequence,
		PreviousHash:         entry.PreviousHash,
		PayloadDigest:        entry.PayloadDigest,
		EventType:            entry.EventType,
		ResourceType:         entry.ResourceType,
		ResourceID:           entry.ResourceID,
		ResourceVersion:      entry.ResourceVersion,
		Outcome:              entry.Outcome,
		ActorType:            entry.ActorType,
		ActorID:              entry.ActorID,
		DomainEventID:        entry.DomainEventID,
		ConfigurationVersion: entry.ConfigurationVersion,
		PolicyVersion:        entry.PolicyVersion,
		TraceID:              entry.TraceID,
		CorrelationID:        entry.CorrelationID,
		OccurredAt:           entry.OccurredAt.UTC().Format(time.RFC3339Nano),
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("encode canonical audit ledger entry: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (entry AuditLedgerEntry) Validate() error {
	if err := entry.validateCanonicalFields(); err != nil {
		return err
	}
	if !auditLedgerHashPattern.MatchString(entry.EntryHash) {
		return errors.New("audit ledger entry hash must be SHA-256")
	}
	computed, err := entry.ComputeEntryHash()
	if err != nil {
		return err
	}
	if computed != entry.EntryHash {
		return errors.New("audit ledger entry hash does not match canonical fields")
	}
	return nil
}

func (entry AuditLedgerEntry) validateCanonicalFields() error {
	if err := (ProjectScope{
		OrganizationID: entry.OrganizationID,
		ProjectID:      entry.ProjectID,
	}).Validate(); err != nil {
		return fmt.Errorf("invalid audit ledger scope: %w", err)
	}
	if strings.TrimSpace(entry.ID) == "" {
		return errors.New("audit ledger entry id is required")
	}
	if entry.Sequence == 0 {
		return errors.New("audit ledger sequence must be positive")
	}
	if !auditLedgerHashPattern.MatchString(entry.PreviousHash) {
		return errors.New("audit ledger previous hash must be SHA-256")
	}
	if !auditLedgerHashPattern.MatchString(entry.PayloadDigest) {
		return errors.New("audit ledger payload digest must be SHA-256")
	}
	if !auditLedgerTypePattern.MatchString(entry.EventType) {
		return fmt.Errorf("audit ledger event type %q is invalid", entry.EventType)
	}
	if !auditLedgerTypePattern.MatchString(entry.ResourceType) {
		return fmt.Errorf("audit ledger resource type %q is invalid", entry.ResourceType)
	}
	if strings.TrimSpace(entry.ResourceID) == "" ||
		len(entry.ResourceID) > 128 {
		return errors.New("audit ledger resource id is invalid")
	}
	if entry.ResourceVersion == 0 {
		return errors.New("audit ledger resource version must be positive")
	}
	if !entry.Outcome.IsValid() {
		return fmt.Errorf("audit ledger outcome %q is invalid", entry.Outcome)
	}
	if err := entry.Actor().Validate(); err != nil {
		return fmt.Errorf("audit ledger actor is invalid: %w", err)
	}
	if strings.TrimSpace(entry.DomainEventID) == "" ||
		len(entry.DomainEventID) > 128 {
		return errors.New("audit ledger domain event id is invalid")
	}
	for name, value := range map[string]struct {
		value string
		limit int
	}{
		"configuration version": {
			value: entry.ConfigurationVersion,
			limit: 128,
		},
		"policy version": {
			value: entry.PolicyVersion,
			limit: 128,
		},
		"trace id": {
			value: entry.TraceID,
			limit: 128,
		},
		"correlation id": {
			value: entry.CorrelationID,
			limit: 255,
		},
	} {
		if len(value.value) > value.limit {
			return fmt.Errorf("audit ledger %s exceeds maximum length", name)
		}
	}
	_, utcOffset := entry.OccurredAt.Zone()
	if entry.OccurredAt.IsZero() || utcOffset != 0 {
		return errors.New("audit ledger occurrence time must be UTC")
	}
	return nil
}

type auditLedgerCanonicalEntry struct {
	SchemaVersion        string             `json:"schema_version"`
	ID                   string             `json:"id"`
	OrganizationID       uint               `json:"organization_id"`
	ProjectID            uint               `json:"project_id"`
	Sequence             uint64             `json:"sequence"`
	PreviousHash         string             `json:"previous_hash"`
	PayloadDigest        string             `json:"payload_digest"`
	EventType            string             `json:"event_type"`
	ResourceType         string             `json:"resource_type"`
	ResourceID           string             `json:"resource_id"`
	ResourceVersion      uint64             `json:"resource_version"`
	Outcome              AuditLedgerOutcome `json:"outcome"`
	ActorType            ActorType          `json:"actor_type"`
	ActorID              string             `json:"actor_id"`
	DomainEventID        string             `json:"domain_event_id"`
	ConfigurationVersion string             `json:"configuration_version"`
	PolicyVersion        string             `json:"policy_version"`
	TraceID              string             `json:"trace_id"`
	CorrelationID        string             `json:"correlation_id"`
	OccurredAt           string             `json:"occurred_at"`
}

// AuditChainHead is the sole mutable coordination row for a project chain.
// Ledger entries themselves never change.
type AuditChainHead struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	OrganizationID uint   `json:"organization_id" gorm:"not null;index;uniqueIndex:idx_audit_chain_project,priority:1;<-:create"`
	ProjectID      uint   `json:"project_id" gorm:"not null;index;uniqueIndex:idx_audit_chain_project,priority:2;<-:create"`
	LastSequence   uint64 `json:"last_sequence" gorm:"not null;default:0"`
	LastHash       string `json:"last_hash" gorm:"size:64;not null"`
	LastEntryID    string `json:"last_entry_id,omitempty" gorm:"size:36;index"`
}

func (AuditChainHead) TableName() string {
	return "audit_chain_heads"
}

func (head *AuditChainHead) BeforeCreate(_ *gorm.DB) error {
	if err := ensureAuditLedgerUUID(&head.ID); err != nil {
		return err
	}
	if head.LastHash == "" {
		head.LastHash = AuditLedgerGenesisHash
	}
	return head.Validate()
}

func (head *AuditChainHead) BeforeUpdate(_ *gorm.DB) error {
	return head.Validate()
}

func (*AuditChainHead) BeforeDelete(_ *gorm.DB) error {
	return ErrAuditLedgerAppendOnly
}

func (head AuditChainHead) Validate() error {
	if err := (ProjectScope{
		OrganizationID: head.OrganizationID,
		ProjectID:      head.ProjectID,
	}).Validate(); err != nil {
		return fmt.Errorf("invalid audit chain head scope: %w", err)
	}
	if !auditLedgerHashPattern.MatchString(head.LastHash) {
		return errors.New("audit chain head hash must be SHA-256")
	}
	if head.LastSequence == 0 {
		if head.LastHash != AuditLedgerGenesisHash ||
			strings.TrimSpace(head.LastEntryID) != "" {
			return errors.New("empty audit chain head must use genesis state")
		}
		return nil
	}
	if strings.TrimSpace(head.LastEntryID) == "" {
		return errors.New("non-empty audit chain head requires last entry id")
	}
	return nil
}

func ensureAuditLedgerUUID(value *string) error {
	if value == nil {
		return errors.New("audit ledger id destination is required")
	}
	if strings.TrimSpace(*value) == "" {
		generated, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate audit ledger UUIDv7: %w", err)
		}
		*value = generated.String()
		return nil
	}
	parsed, err := uuid.Parse(*value)
	if err != nil || parsed.String() != strings.ToLower(*value) {
		return errors.New("audit ledger id must be a canonical UUID")
	}
	return nil
}
