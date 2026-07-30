package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type auditLedgerBusinessRecord struct {
	ID        uint `gorm:"primaryKey"`
	ProjectID uint
	Value     string
}

func TestAuditLedgerAppendTxSharesBusinessCommitAndTrustedContext(
	t *testing.T,
) {
	db := newAuditLedgerTestDB(t, false)
	service := newAuditLedgerTestService(t, db)
	scope := models.ProjectScope{OrganizationID: 1, ProjectID: 10}
	ctx := auditLedgerTestContext(t, scope, 42)
	input := auditLedgerTestInput("event-1", "payload-1", 1)

	if _, err := service.AppendTx(
		ctx,
		db,
		input,
	); !errors.Is(err, ErrAuditLedgerTransactionRequired) {
		t.Fatalf("append without transaction error = %v", err)
	}

	var first *models.AuditLedgerEntry
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&auditLedgerBusinessRecord{
			ProjectID: scope.ProjectID,
			Value:     "committed",
		}).Error; err != nil {
			return err
		}
		entry, err := service.AppendTx(ctx, tx, input)
		if err != nil {
			return err
		}
		first = entry
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if first.Sequence != 1 ||
		first.PreviousHash != models.AuditLedgerGenesisHash ||
		first.Actor() != models.HumanActor(42) ||
		first.OrganizationID != scope.OrganizationID ||
		first.ProjectID != scope.ProjectID ||
		first.TraceID != "trace-42" ||
		first.CorrelationID != "correlation-42" {
		t.Fatalf("first audit entry = %+v", first)
	}

	var second *models.AuditLedgerEntry
	if err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		second, err = service.AppendTx(
			ctx,
			tx,
			auditLedgerTestInput("event-2", "payload-2", 2),
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if second.Sequence != 2 || second.PreviousHash != first.EntryHash {
		t.Fatalf("second audit entry = %+v", second)
	}
	verification, err := service.Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !verification.Valid ||
		verification.CheckedEntries != 2 ||
		verification.HeadSequence != 2 ||
		verification.HeadHash != second.EntryHash {
		t.Fatalf("verification = %+v", verification)
	}

	otherScope := models.ProjectScope{OrganizationID: 1, ProjectID: 20}
	otherContext := auditLedgerTestContext(t, otherScope, 42)
	var other *models.AuditLedgerEntry
	if err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		other, err = service.AppendTx(
			otherContext,
			tx,
			auditLedgerTestInput("other-event-1", "other-payload", 1),
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if other.Sequence != 1 ||
		other.PreviousHash != models.AuditLedgerGenesisHash {
		t.Fatalf("other project entry = %+v", other)
	}
}

func TestAuditLedgerAppendRollsBackWithCallerBusinessTransaction(
	t *testing.T,
) {
	db := newAuditLedgerTestDB(t, false)
	service := newAuditLedgerTestService(t, db)
	scope := models.ProjectScope{OrganizationID: 1, ProjectID: 10}
	ctx := auditLedgerTestContext(t, scope, 7)
	rollback := errors.New("rollback business transaction")
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&auditLedgerBusinessRecord{
			ProjectID: scope.ProjectID,
			Value:     "must rollback",
		}).Error; err != nil {
			return err
		}
		if _, err := service.AppendTx(
			ctx,
			tx,
			auditLedgerTestInput("rollback-event", "rollback-payload", 1),
		); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("transaction error = %v", err)
	}
	var businessCount int64
	if err := db.Model(&auditLedgerBusinessRecord{}).
		Count(&businessCount).Error; err != nil {
		t.Fatal(err)
	}
	var entryCount int64
	if err := db.Model(&models.AuditLedgerEntry{}).
		Count(&entryCount).Error; err != nil {
		t.Fatal(err)
	}
	var headCount int64
	if err := db.Model(&models.AuditChainHead{}).
		Count(&headCount).Error; err != nil {
		t.Fatal(err)
	}
	if businessCount != 0 || entryCount != 0 || headCount != 0 {
		t.Fatalf(
			"rollback counts: business=%d entries=%d heads=%d",
			businessCount,
			entryCount,
			headCount,
		)
	}
}

func TestAuditLedgerConcurrentAppendProducesOneMonotonicChain(
	t *testing.T,
) {
	db := newAuditLedgerTestDB(t, true)
	service := newAuditLedgerTestService(t, db)
	scope := models.ProjectScope{OrganizationID: 3, ProjectID: 30}
	ctx := auditLedgerTestContext(t, scope, 99)
	const appendCount = 24
	var wait sync.WaitGroup
	errorsChannel := make(chan error, appendCount)
	for index := 0; index < appendCount; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := service.AppendTx(
					ctx,
					tx,
					auditLedgerTestInput(
						fmt.Sprintf("concurrent-event-%d", index),
						fmt.Sprintf("concurrent-payload-%d", index),
						uint64(index+1),
					),
				)
				return err
			})
			errorsChannel <- err
		}()
	}
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("concurrent append error = %v", err)
		}
	}
	verification, err := service.Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !verification.Valid ||
		verification.CheckedEntries != appendCount ||
		verification.HeadSequence != appendCount {
		t.Fatalf("concurrent verification = %+v", verification)
	}
	var entries []models.AuditLedgerEntry
	if err := db.Where(
		"organization_id = ? AND project_id = ?",
		scope.OrganizationID,
		scope.ProjectID,
	).Order("sequence ASC").Find(&entries).Error; err != nil {
		t.Fatal(err)
	}
	seenHashes := make(map[string]struct{}, len(entries))
	for index, entry := range entries {
		wantSequence := uint64(index + 1)
		if entry.Sequence != wantSequence {
			t.Fatalf("entry %d sequence = %d", index, entry.Sequence)
		}
		if _, duplicate := seenHashes[entry.EntryHash]; duplicate {
			t.Fatalf("duplicate entry hash %q", entry.EntryHash)
		}
		seenHashes[entry.EntryHash] = struct{}{}
		if index > 0 && entry.PreviousHash != entries[index-1].EntryHash {
			t.Fatalf(
				"entry %d previous hash = %q, want %q",
				index,
				entry.PreviousHash,
				entries[index-1].EntryHash,
			)
		}
	}
}

func TestAuditLedgerVerifyAndExportLocateFirstTamperedEntry(
	t *testing.T,
) {
	db := newAuditLedgerTestDB(t, false)
	service := newAuditLedgerTestService(t, db)
	scope := models.ProjectScope{OrganizationID: 5, ProjectID: 50}
	ctx := auditLedgerTestContext(t, scope, 11)
	for index := 1; index <= 3; index++ {
		if err := db.Transaction(func(tx *gorm.DB) error {
			_, err := service.AppendTx(
				ctx,
				tx,
				auditLedgerTestInput(
					fmt.Sprintf("tamper-event-%d", index),
					fmt.Sprintf("payload-%d", index),
					uint64(index),
				),
			)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(
		"UPDATE audit_ledger_entries SET payload_digest = ? "+
			"WHERE organization_id = ? AND project_id = ? AND sequence = ?",
		strings.Repeat("f", 64),
		scope.OrganizationID,
		scope.ProjectID,
		2,
	).Error; err != nil {
		t.Fatal(err)
	}
	verification, err := service.Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if verification.Valid ||
		verification.FirstBrokenSequence == nil ||
		*verification.FirstBrokenSequence != 2 ||
		verification.Reason != AuditLedgerViolationEntryHash ||
		verification.CheckedEntries != 1 {
		t.Fatalf("tamper verification = %+v", verification)
	}
	exporter := &auditLedgerTestWORMExporter{}
	result, err := service.Export(ctx, exporter)
	if !errors.Is(err, ErrAuditLedgerVerificationFailed) {
		t.Fatalf("tampered export error = %v", err)
	}
	if result == nil ||
		result.Verification.FirstBrokenSequence == nil ||
		*result.Verification.FirstBrokenSequence != 2 ||
		exporter.calls != 0 {
		t.Fatalf("tampered export result = %+v, exporter calls = %d", result, exporter.calls)
	}
}

func TestAuditLedgerExportWritesDeterministicDigestToWORMTarget(
	t *testing.T,
) {
	db := newAuditLedgerTestDB(t, false)
	service := newAuditLedgerTestService(t, db)
	scope := models.ProjectScope{OrganizationID: 8, ProjectID: 80}
	ctx := auditLedgerTestContext(t, scope, 15)
	for index := 1; index <= 2; index++ {
		if err := db.Transaction(func(tx *gorm.DB) error {
			_, err := service.AppendTx(
				ctx,
				tx,
				auditLedgerTestInput(
					fmt.Sprintf("export-event-%d", index),
					fmt.Sprintf("payload-%d", index),
					uint64(index),
				),
			)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	exporter := &auditLedgerTestWORMExporter{}
	result, err := service.Export(ctx, exporter)
	if err != nil {
		t.Fatal(err)
	}
	if result.Manifest == nil ||
		result.Receipt == nil ||
		!result.Verification.Valid ||
		result.Manifest.EntryCount != 2 ||
		result.Manifest.FirstSequence != 1 ||
		result.Manifest.LastSequence != 2 ||
		result.Receipt.ContentDigest != result.Manifest.ContentDigest {
		t.Fatalf("export result = %+v", result)
	}
	digest := sha256.Sum256(exporter.records)
	if hex.EncodeToString(digest[:]) != result.Manifest.ContentDigest {
		t.Fatalf(
			"export digest = %q, want %q",
			hex.EncodeToString(digest[:]),
			result.Manifest.ContentDigest,
		)
	}
	if strings.Contains(string(exporter.records), "request_body") ||
		strings.Contains(string(exporter.records), "chain_of_thought") ||
		strings.Contains(string(exporter.records), "secret") {
		t.Fatalf("export contains prohibited content: %s", exporter.records)
	}
	if lines := strings.Count(string(exporter.records), "\n"); lines != 2 {
		t.Fatalf("export JSONL line count = %d", lines)
	}
}

func TestAuditLedgerAppendInputCannotOverrideScopeActorOrCarryPayload(
	t *testing.T,
) {
	inputType := reflect.TypeOf(AuditLedgerAppendInput{})
	for _, forbidden := range []string{
		"OrganizationID",
		"ProjectID",
		"Scope",
		"Actor",
		"ActorType",
		"ActorID",
		"Payload",
		"RequestBody",
		"Prompt",
		"Secret",
		"Metadata",
	} {
		if _, exists := inputType.FieldByName(forbidden); exists {
			t.Errorf("append input exposes forbidden field %q", forbidden)
		}
	}
}

type auditLedgerTestWORMExporter struct {
	calls    int
	manifest AuditLedgerExportManifest
	records  []byte
}

func (exporter *auditLedgerTestWORMExporter) WriteOnce(
	_ context.Context,
	manifest AuditLedgerExportManifest,
	records io.Reader,
) (AuditLedgerWORMReceipt, error) {
	exporter.calls++
	exporter.manifest = manifest
	raw, err := io.ReadAll(records)
	if err != nil {
		return AuditLedgerWORMReceipt{}, err
	}
	exporter.records = raw
	return AuditLedgerWORMReceipt{
		Reference:     "worm://audit/export-1",
		VersionID:     "immutable-version-1",
		ContentDigest: manifest.ContentDigest,
	}, nil
}

func newAuditLedgerTestDB(t *testing.T, serializeConnections bool) *gorm.DB {
	t.Helper()
	dsn := "file:" + t.Name() + "?mode=memory&cache=shared&_busy_timeout=10000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if serializeConnections {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(
		&auditLedgerBusinessRecord{},
		&models.AuditLedgerEntry{},
		&models.AuditChainHead{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

func newAuditLedgerTestService(
	t *testing.T,
	db *gorm.DB,
) *AuditLedgerService {
	t.Helper()
	service, err := NewAuditLedgerService(db)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time {
		return time.Date(2026, 7, 30, 14, 0, 0, 123, time.UTC)
	}
	return service
}

func auditLedgerTestContext(
	t *testing.T,
	scope models.ProjectScope,
	userID uint,
) context.Context {
	t.Helper()
	ctx, err := WithOperationContext(context.Background(), OperationContext{
		Scope:         scope,
		Actor:         models.HumanActor(userID),
		Source:        SourceProtocolHumanREST,
		TraceID:       fmt.Sprintf("trace-%d", userID),
		CorrelationID: fmt.Sprintf("correlation-%d", userID),
	})
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func auditLedgerTestInput(
	eventID string,
	payload string,
	resourceVersion uint64,
) AuditLedgerAppendInput {
	digest := sha256.Sum256([]byte(payload))
	return AuditLedgerAppendInput{
		PayloadDigest:        hex.EncodeToString(digest[:]),
		EventType:            "ticket.updated",
		ResourceType:         "ticket",
		ResourceID:           fmt.Sprintf("ticket-%d", resourceVersion),
		ResourceVersion:      resourceVersion,
		Outcome:              models.AuditLedgerOutcomeSucceeded,
		DomainEventID:        eventID + "-" + uuid.Must(uuid.NewV7()).String(),
		ConfigurationVersion: "configuration-v1",
		PolicyVersion:        "policy-v1",
	}
}
