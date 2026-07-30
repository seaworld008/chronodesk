package models

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAuditLedgerCanonicalHashIsDeterministicAndSensitiveToFields(
	t *testing.T,
) {
	entry := validAuditLedgerEntry()
	entry.ID = uuid.Must(uuid.NewV7()).String()
	first, err := entry.ComputeEntryHash()
	if err != nil {
		t.Fatal(err)
	}
	second, err := entry.ComputeEntryHash()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("canonical hashes differ: %q != %q", first, second)
	}
	changed := entry
	changed.PolicyVersion = "policy-v2"
	changedHash, err := changed.ComputeEntryHash()
	if err != nil {
		t.Fatal(err)
	}
	if changedHash == first {
		t.Fatal("policy version mutation did not change entry hash")
	}
}

func TestAuditLedgerEntryContainsNoRawPayloadOrSecretFields(t *testing.T) {
	entryType := reflect.TypeOf(AuditLedgerEntry{})
	for _, forbidden := range []string{
		"RequestBody",
		"Payload",
		"Data",
		"Metadata",
		"Prompt",
		"ChainOfThought",
		"Reasoning",
		"Secret",
		"Credential",
		"Token",
	} {
		if _, exists := entryType.FieldByName(forbidden); exists {
			t.Errorf("audit ledger entry exposes forbidden field %q", forbidden)
		}
	}
	if _, exists := entryType.FieldByName("PayloadDigest"); !exists {
		t.Fatal("audit ledger entry lacks PayloadDigest")
	}
}

func TestAuditLedgerEntryHooksRejectUpdateDeleteAndUseUUIDv7(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&AuditLedgerEntry{}, &AuditChainHead{}); err != nil {
		t.Fatal(err)
	}
	entry := validAuditLedgerEntry()
	if err := db.Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	parsed, err := uuid.Parse(entry.ID)
	if err != nil || parsed.Version() != 7 {
		t.Fatalf(
			"audit entry id = %q, UUID version = %v, err = %v",
			entry.ID,
			parsed.Version(),
			err,
		)
	}
	if entry.OccurredAt.Location() != time.UTC {
		t.Fatalf("occurred_at location = %v", entry.OccurredAt.Location())
	}
	entry.ResourceVersion = 2
	if err := db.Save(&entry).Error; !errors.Is(
		err,
		ErrAuditLedgerAppendOnly,
	) {
		t.Fatalf("audit entry update error = %v", err)
	}
	if err := db.Delete(&entry).Error; !errors.Is(
		err,
		ErrAuditLedgerAppendOnly,
	) {
		t.Fatalf("audit entry delete error = %v", err)
	}
	head := AuditChainHead{
		OrganizationID: 1,
		ProjectID:      2,
		LastHash:       AuditLedgerGenesisHash,
	}
	if err := db.Create(&head).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&head).Error; !errors.Is(
		err,
		ErrAuditLedgerAppendOnly,
	) {
		t.Fatalf("audit head delete error = %v", err)
	}
}

func validAuditLedgerEntry() AuditLedgerEntry {
	return AuditLedgerEntry{
		OccurredAt:           time.Date(2026, 7, 30, 12, 0, 0, 123, time.UTC),
		OrganizationID:       1,
		ProjectID:            2,
		Sequence:             1,
		PreviousHash:         AuditLedgerGenesisHash,
		PayloadDigest:        strings.Repeat("a", 64),
		EventType:            "ticket.updated",
		ResourceType:         "ticket",
		ResourceID:           "42",
		ResourceVersion:      1,
		Outcome:              AuditLedgerOutcomeSucceeded,
		ActorType:            ActorTypeHuman,
		ActorID:              "7",
		DomainEventID:        uuid.Must(uuid.NewV7()).String(),
		ConfigurationVersion: "configuration-v1",
		PolicyVersion:        "policy-v1",
		TraceID:              "trace-1",
		CorrelationID:        "correlation-1",
	}
}
