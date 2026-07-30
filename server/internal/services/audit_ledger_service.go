package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrAuditLedgerTransactionRequired = errors.New(
		"audit ledger append requires caller transaction",
	)
	ErrAuditLedgerConcurrentAppend = errors.New(
		"audit ledger chain head changed concurrently",
	)
	ErrAuditLedgerVerificationFailed = errors.New(
		"audit ledger verification failed",
	)
	ErrAuditLedgerExportReceiptInvalid = errors.New(
		"audit ledger WORM receipt is invalid",
	)
)

type AuditLedgerService struct {
	db  *gorm.DB
	now func() time.Time
}

func NewAuditLedgerService(db *gorm.DB) (*AuditLedgerService, error) {
	if db == nil {
		return nil, errors.New("audit ledger database is required")
	}
	return &AuditLedgerService{
		db:  db,
		now: time.Now,
	}, nil
}

// AuditLedgerAppendInput deliberately accepts no scope, Actor, request body,
// prompt, metadata map or secret-bearing value. Callers provide only a digest
// of their canonical business payload.
type AuditLedgerAppendInput struct {
	PayloadDigest        string
	EventType            string
	ResourceType         string
	ResourceID           string
	ResourceVersion      uint64
	Outcome              models.AuditLedgerOutcome
	DomainEventID        string
	ConfigurationVersion string
	PolicyVersion        string
	OccurredAt           time.Time
}

// AppendTx appends to the same transaction supplied by the business caller. It
// never begins, commits or rolls back a transaction itself.
func (service *AuditLedgerService) AppendTx(
	ctx context.Context,
	tx *gorm.DB,
	input AuditLedgerAppendInput,
) (*models.AuditLedgerEntry, error) {
	if service == nil || service.db == nil {
		return nil, errors.New("audit ledger service is unavailable")
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if tx == nil || tx.Statement == nil || tx.Statement.ConnPool == nil {
		return nil, ErrAuditLedgerTransactionRequired
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrAuditLedgerTransactionRequired
	}
	tx = tx.WithContext(ctx)
	genesis := models.AuditChainHead{
		OrganizationID: operation.Scope.OrganizationID,
		ProjectID:      operation.Scope.ProjectID,
		LastSequence:   0,
		LastHash:       models.AuditLedgerGenesisHash,
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "organization_id"},
			{Name: "project_id"},
		},
		DoNothing: true,
	}).Create(&genesis).Error; err != nil {
		return nil, fmt.Errorf("ensure audit chain head: %w", err)
	}

	var head models.AuditChainHead
	if err := auditLedgerScopedQuery(tx, operation.Scope).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("organization_id = ? AND project_id = ?",
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
		).
		First(&head).Error; err != nil {
		return nil, fmt.Errorf("lock audit chain head: %w", err)
	}
	if err := head.Validate(); err != nil {
		return nil, fmt.Errorf("%w: invalid chain head: %v",
			ErrAuditLedgerVerificationFailed,
			err,
		)
	}
	occurredAt := input.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = service.now()
	}
	occurredAt = occurredAt.UTC().Round(0)
	entry := models.AuditLedgerEntry{
		OrganizationID:       operation.Scope.OrganizationID,
		ProjectID:            operation.Scope.ProjectID,
		Sequence:             head.LastSequence + 1,
		PreviousHash:         head.LastHash,
		PayloadDigest:        strings.ToLower(strings.TrimSpace(input.PayloadDigest)),
		EventType:            strings.TrimSpace(input.EventType),
		ResourceType:         strings.TrimSpace(input.ResourceType),
		ResourceID:           strings.TrimSpace(input.ResourceID),
		ResourceVersion:      input.ResourceVersion,
		Outcome:              input.Outcome,
		ActorType:            operation.Actor.Type,
		ActorID:              operation.Actor.ID,
		DomainEventID:        strings.TrimSpace(input.DomainEventID),
		ConfigurationVersion: strings.TrimSpace(input.ConfigurationVersion),
		PolicyVersion:        strings.TrimSpace(input.PolicyVersion),
		TraceID:              strings.TrimSpace(operation.TraceID),
		CorrelationID:        strings.TrimSpace(operation.CorrelationID),
		OccurredAt:           occurredAt,
	}
	if err := tx.Create(&entry).Error; err != nil {
		return nil, fmt.Errorf("append audit ledger entry: %w", err)
	}
	now := service.now().UTC().Round(0)
	result := auditLedgerScopedQuery(
		tx.Model(&models.AuditChainHead{}),
		operation.Scope,
	).Where(
		"id = ? AND last_sequence = ? AND last_hash = ?",
		head.ID,
		head.LastSequence,
		head.LastHash,
	).UpdateColumns(map[string]any{
		"last_sequence": entry.Sequence,
		"last_hash":     entry.EntryHash,
		"last_entry_id": entry.ID,
		"updated_at":    now,
	})
	if result.Error != nil {
		return nil, fmt.Errorf("advance audit chain head: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return nil, ErrAuditLedgerConcurrentAppend
	}
	return &entry, nil
}

type AuditLedgerViolationReason string

const (
	AuditLedgerViolationNone            AuditLedgerViolationReason = ""
	AuditLedgerViolationMissingSequence AuditLedgerViolationReason = "missing_sequence"
	AuditLedgerViolationPreviousHash    AuditLedgerViolationReason = "previous_hash_mismatch"
	AuditLedgerViolationEntryHash       AuditLedgerViolationReason = "entry_hash_mismatch"
	AuditLedgerViolationHeadMissing     AuditLedgerViolationReason = "chain_head_missing"
	AuditLedgerViolationHeadSequence    AuditLedgerViolationReason = "chain_head_sequence_mismatch"
	AuditLedgerViolationHeadHash        AuditLedgerViolationReason = "chain_head_hash_mismatch"
	AuditLedgerViolationHeadEntry       AuditLedgerViolationReason = "chain_head_entry_mismatch"
)

type AuditLedgerVerification struct {
	Valid               bool                       `json:"valid"`
	CheckedEntries      uint64                     `json:"checked_entries"`
	FirstBrokenSequence *uint64                    `json:"first_broken_sequence,omitempty"`
	Reason              AuditLedgerViolationReason `json:"reason,omitempty"`
	ExpectedHash        string                     `json:"expected_hash,omitempty"`
	ActualHash          string                     `json:"actual_hash,omitempty"`
	HeadSequence        uint64                     `json:"head_sequence"`
	HeadHash            string                     `json:"head_hash,omitempty"`
}

func (service *AuditLedgerService) Verify(
	ctx context.Context,
) (*AuditLedgerVerification, error) {
	if service == nil || service.db == nil {
		return nil, errors.New("audit ledger service is unavailable")
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return service.verifyScope(ctx, operation.Scope)
}

func (service *AuditLedgerService) verifyScope(
	ctx context.Context,
	scope models.ProjectScope,
) (*AuditLedgerVerification, error) {
	var entries []models.AuditLedgerEntry
	if err := auditLedgerScopedQuery(
		service.db.WithContext(ctx),
		scope,
	).Order("sequence ASC").Find(&entries).Error; err != nil {
		return nil, fmt.Errorf("load audit ledger entries: %w", err)
	}
	var head models.AuditChainHead
	headError := auditLedgerScopedQuery(
		service.db.WithContext(ctx),
		scope,
	).Where(
		"organization_id = ? AND project_id = ?",
		scope.OrganizationID,
		scope.ProjectID,
	).First(&head).Error
	if headError != nil && !errors.Is(headError, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("load audit chain head: %w", headError)
	}
	verification := &AuditLedgerVerification{
		Valid:        true,
		HeadSequence: head.LastSequence,
		HeadHash:     head.LastHash,
	}
	expectedSequence := uint64(1)
	expectedPreviousHash := models.AuditLedgerGenesisHash
	var lastEntryID string
	for _, entry := range entries {
		if entry.Sequence != expectedSequence {
			return brokenAuditLedgerVerification(
				verification,
				expectedSequence,
				AuditLedgerViolationMissingSequence,
				"",
				"",
			), nil
		}
		if entry.PreviousHash != expectedPreviousHash {
			return brokenAuditLedgerVerification(
				verification,
				entry.Sequence,
				AuditLedgerViolationPreviousHash,
				expectedPreviousHash,
				entry.PreviousHash,
			), nil
		}
		computed, computeErr := entry.ComputeEntryHash()
		if computeErr != nil || entry.EntryHash != computed {
			return brokenAuditLedgerVerification(
				verification,
				entry.Sequence,
				AuditLedgerViolationEntryHash,
				computed,
				entry.EntryHash,
			), nil
		}
		verification.CheckedEntries++
		expectedSequence++
		expectedPreviousHash = entry.EntryHash
		lastEntryID = entry.ID
	}
	if errors.Is(headError, gorm.ErrRecordNotFound) {
		if len(entries) == 0 {
			verification.HeadHash = models.AuditLedgerGenesisHash
			return verification, nil
		}
		return brokenAuditLedgerVerification(
			verification,
			uint64(len(entries)),
			AuditLedgerViolationHeadMissing,
			expectedPreviousHash,
			"",
		), nil
	}
	if head.LastSequence != uint64(len(entries)) {
		return brokenAuditLedgerVerification(
			verification,
			firstAuditHeadBreakSequence(head, entries),
			AuditLedgerViolationHeadSequence,
			fmt.Sprintf("%d", len(entries)),
			fmt.Sprintf("%d", head.LastSequence),
		), nil
	}
	if head.LastHash != expectedPreviousHash {
		return brokenAuditLedgerVerification(
			verification,
			firstAuditHeadBreakSequence(head, entries),
			AuditLedgerViolationHeadHash,
			expectedPreviousHash,
			head.LastHash,
		), nil
	}
	if len(entries) == 0 {
		if head.LastEntryID != "" {
			return brokenAuditLedgerVerification(
				verification,
				1,
				AuditLedgerViolationHeadEntry,
				"",
				head.LastEntryID,
			), nil
		}
		return verification, nil
	}
	if head.LastEntryID != lastEntryID {
		return brokenAuditLedgerVerification(
			verification,
			head.LastSequence,
			AuditLedgerViolationHeadEntry,
			lastEntryID,
			head.LastEntryID,
		), nil
	}
	return verification, nil
}

func brokenAuditLedgerVerification(
	verification *AuditLedgerVerification,
	sequence uint64,
	reason AuditLedgerViolationReason,
	expected string,
	actual string,
) *AuditLedgerVerification {
	verification.Valid = false
	verification.FirstBrokenSequence = &sequence
	verification.Reason = reason
	verification.ExpectedHash = expected
	verification.ActualHash = actual
	return verification
}

func firstAuditHeadBreakSequence(
	head models.AuditChainHead,
	entries []models.AuditLedgerEntry,
) uint64 {
	if head.LastSequence > 0 {
		return head.LastSequence
	}
	if len(entries) > 0 {
		return entries[len(entries)-1].Sequence
	}
	return 1
}

type AuditLedgerExportManifest struct {
	SchemaVersion  string    `json:"schema_version"`
	OrganizationID uint      `json:"organization_id"`
	ProjectID      uint      `json:"project_id"`
	EntryCount     uint64    `json:"entry_count"`
	FirstSequence  uint64    `json:"first_sequence,omitempty"`
	LastSequence   uint64    `json:"last_sequence,omitempty"`
	LastEntryHash  string    `json:"last_entry_hash"`
	ContentDigest  string    `json:"content_digest"`
	GeneratedAt    time.Time `json:"generated_at"`
}

type AuditLedgerWORMReceipt struct {
	Reference     string     `json:"reference"`
	VersionID     string     `json:"version_id,omitempty"`
	ContentDigest string     `json:"content_digest"`
	RetainUntil   *time.Time `json:"retain_until,omitempty"`
}

// AuditLedgerWORMExporter is the protocol-neutral seam for future immutable
// object-lock, retention-vault or regulatory archive adapters.
type AuditLedgerWORMExporter interface {
	WriteOnce(
		ctx context.Context,
		manifest AuditLedgerExportManifest,
		records io.Reader,
	) (AuditLedgerWORMReceipt, error)
}

type AuditLedgerExportResult struct {
	Verification AuditLedgerVerification    `json:"verification"`
	Manifest     *AuditLedgerExportManifest `json:"manifest,omitempty"`
	Receipt      *AuditLedgerWORMReceipt    `json:"receipt,omitempty"`
}

func (service *AuditLedgerService) Export(
	ctx context.Context,
	exporter AuditLedgerWORMExporter,
) (*AuditLedgerExportResult, error) {
	if exporter == nil {
		return nil, errors.New("audit ledger WORM exporter is required")
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return nil, err
	}
	verification, err := service.verifyScope(ctx, operation.Scope)
	if err != nil {
		return nil, err
	}
	result := &AuditLedgerExportResult{
		Verification: *verification,
	}
	if !verification.Valid {
		return result, ErrAuditLedgerVerificationFailed
	}
	var entries []models.AuditLedgerEntry
	if err := auditLedgerScopedQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Order("sequence ASC").Find(&entries).Error; err != nil {
		return nil, fmt.Errorf("load audit ledger export entries: %w", err)
	}
	var records bytes.Buffer
	encoder := json.NewEncoder(&records)
	encoder.SetEscapeHTML(false)
	for _, entry := range entries {
		if err := encoder.Encode(entry); err != nil {
			return nil, fmt.Errorf("encode audit ledger export: %w", err)
		}
	}
	digest := sha256.Sum256(records.Bytes())
	manifest := AuditLedgerExportManifest{
		SchemaVersion:  models.AuditLedgerSchemaVersion,
		OrganizationID: operation.Scope.OrganizationID,
		ProjectID:      operation.Scope.ProjectID,
		EntryCount:     uint64(len(entries)),
		LastEntryHash:  models.AuditLedgerGenesisHash,
		ContentDigest:  hex.EncodeToString(digest[:]),
		GeneratedAt:    service.now().UTC().Round(0),
	}
	if len(entries) > 0 {
		manifest.FirstSequence = entries[0].Sequence
		manifest.LastSequence = entries[len(entries)-1].Sequence
		manifest.LastEntryHash = entries[len(entries)-1].EntryHash
	}
	result.Manifest = &manifest
	receipt, err := exporter.WriteOnce(
		ctx,
		manifest,
		bytes.NewReader(records.Bytes()),
	)
	if err != nil {
		return result, fmt.Errorf("write audit ledger WORM export: %w", err)
	}
	if strings.TrimSpace(receipt.Reference) == "" ||
		receipt.ContentDigest != manifest.ContentDigest {
		return result, ErrAuditLedgerExportReceiptInvalid
	}
	result.Receipt = &receipt
	return result, nil
}

func auditLedgerScopedQuery(
	db *gorm.DB,
	scope models.ProjectScope,
) *gorm.DB {
	return db.Where(
		"organization_id = ? AND project_id = ?",
		scope.OrganizationID,
		scope.ProjectID,
	)
}
