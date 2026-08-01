package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

func TestAttachmentUploadFinalPutRacingTicketDeleteLeavesNoObject(
	t *testing.T,
) {
	fixture := newAttachmentUploadDeleteRaceFixture(t)
	uploadResult, workerContext := fixture.stageCommittedUpload(t)
	finalKey := "tickets/" +
		fixture.ticketIDText() + "/" +
		uploadResult.Attachment.FileName
	fixture.storage.pauseFinalKey(finalKey)

	uploadCompleted := make(chan error, 1)
	go func() {
		uploadCompleted <- fixture.service.
			ExecuteAttachmentUploadOutbox(
				workerContext,
				uploadResult.Attachment.ID,
			)
	}()
	fixture.storage.waitForFinalPut(t)
	fixture.assertObjectExists(t, finalKey)
	fixture.assertObjectExists(
		t,
		uploadResult.Attachment.StoragePath,
	)

	ticketService := newTicketServiceWithDependenciesForTest(
		t,
		fixture.db,
		fixture.service,
		nil,
		0,
	)
	if err := ticketService.DeleteTicketExpectedVersion(
		fixture.humanContext,
		fixture.ticket.ID,
		fixture.user.ID,
		string(models.ProjectRoleAdmin),
		uploadResult.Receipt.ResourceVersion,
	); err != nil {
		t.Fatalf("delete Ticket after final Put: %v", err)
	}
	fixture.storage.releaseFinalPut()

	select {
	case err := <-uploadCompleted:
		if err != nil {
			t.Fatalf(
				"upload finalizer did not compensate deleted Ticket: %v",
				err,
			)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("upload finalizer timed out after Ticket deletion")
	}
	assertAttachmentUploadMigrationIntentIsPrivate(
		t,
		fixture.db,
		fixture.ticket.ID,
		uploadResult.Attachment.ID,
		uploadResult.Attachment.StoragePath,
		finalKey,
	)
	fixture.assertObjectMissing(
		t,
		uploadResult.Attachment.StoragePath,
	)
	fixture.assertObjectMissing(t, finalKey)
	assertAttachmentUploadDeleteHasNoLiveDatabaseReference(
		t,
		fixture.db,
		fixture.ticket.ID,
		uploadResult.Attachment.ID,
	)

	// Delivery retries after the compensation are harmless and do not need the
	// deleted TicketAttachment row to rediscover either durable object key.
	if err := fixture.service.ExecuteAttachmentUploadOutbox(
		workerContext,
		uploadResult.Attachment.ID,
	); err != nil {
		t.Fatalf("idempotent upload compensation retry: %v", err)
	}
	fixture.assertObjectMissing(
		t,
		uploadResult.Attachment.StoragePath,
	)
	fixture.assertObjectMissing(t, finalKey)
}

func TestAttachmentUploadIntentRecoversFinalObjectAfterWorkerRestart(
	t *testing.T,
) {
	fixture := newAttachmentUploadDeleteRaceFixture(t)
	uploadResult, workerContext := fixture.stageCommittedUpload(t)
	finalKey := "tickets/" +
		fixture.ticketIDText() + "/" +
		uploadResult.Attachment.FileName
	stagedReader, err := fixture.storage.OpenStaged(
		workerContext,
		uploadResult.Attachment.StoragePath,
	)
	if err != nil {
		t.Fatalf("open staged bytes before simulated crash: %v", err)
	}
	stored, putErr := fixture.storage.LocalAttachmentStorage.Put(
		workerContext,
		finalKey,
		stagedReader,
		1024,
	)
	closeErr := stagedReader.Close()
	if putErr != nil || closeErr != nil ||
		stored == nil ||
		stored.Key != finalKey {
		t.Fatalf(
			"seed final object before simulated crash: stored=%+v put=%v close=%v",
			stored,
			putErr,
			closeErr,
		)
	}
	fixture.assertObjectExists(t, finalKey)
	if _, err := fixture.service.DeleteTicket(
		fixture.humanContext,
		DeleteTicketCommand{
			TicketID:        fixture.ticket.ID,
			ExpectedVersion: uploadResult.Receipt.ResourceVersion,
			Actor:           models.HumanActor(fixture.user.ID),
			SourceProtocol:  "test",
		},
	); err != nil {
		t.Fatalf("delete Ticket before simulated worker crash: %v", err)
	}
	operation, err := OperationContextFromContext(fixture.humanContext)
	if err != nil {
		t.Fatal(err)
	}
	archive := fixture.db.Model(&models.Project{}).
		Where(
			"id = ? AND organization_id = ?",
			operation.Scope.ProjectID,
			operation.Scope.OrganizationID,
		).
		Update("status", models.ProjectStatusArchived)
	if archive.Error != nil {
		t.Fatalf("archive project before recovery: %v", archive.Error)
	}
	if archive.RowsAffected != 1 {
		t.Fatal("project was not archived before recovery")
	}

	restarted := NewAgentNativeService(
		fixture.db,
		AgentNativeOptions{
			AttachmentStorage:  fixture.storage.LocalAttachmentStorage,
			AttachmentStaging:  fixture.storage.LocalAttachmentStorage,
			AttachmentMaxBytes: 1024,
		},
	)
	if err := restarted.ExecuteAttachmentUploadOutbox(
		workerContext,
		uploadResult.Attachment.ID,
	); err != nil {
		t.Fatalf("restart upload compensation: %v", err)
	}
	fixture.assertObjectMissing(
		t,
		uploadResult.Attachment.StoragePath,
	)
	fixture.assertObjectMissing(t, finalKey)
	assertAttachmentUploadDeleteHasNoLiveDatabaseReference(
		t,
		fixture.db,
		fixture.ticket.ID,
		uploadResult.Attachment.ID,
	)
}

type attachmentUploadCancellationFailureContextKey struct{}

func TestAttachmentUploadArchivedPendingIntentCleansBothObjects(
	t *testing.T,
) {
	fixture := newAttachmentUploadDeleteRaceFixture(t)
	uploadResult, workerContext := fixture.stageCommittedUpload(t)
	finalKey := "tickets/" +
		fixture.ticketIDText() + "/" +
		uploadResult.Attachment.FileName
	stagedReader, err := fixture.storage.OpenStaged(
		workerContext,
		uploadResult.Attachment.StoragePath,
	)
	if err != nil {
		t.Fatalf("open staged bytes before archived crash: %v", err)
	}
	stored, putErr := fixture.storage.LocalAttachmentStorage.Put(
		workerContext,
		finalKey,
		stagedReader,
		1024,
	)
	closeErr := stagedReader.Close()
	if putErr != nil || closeErr != nil ||
		stored == nil ||
		stored.Key != finalKey {
		t.Fatalf(
			"seed final object before archived crash: stored=%+v put=%v close=%v",
			stored,
			putErr,
			closeErr,
		)
	}
	operation, err := OperationContextFromContext(fixture.humanContext)
	if err != nil {
		t.Fatal(err)
	}
	archive := fixture.db.Model(&models.Project{}).
		Where(
			"id = ? AND organization_id = ?",
			operation.Scope.ProjectID,
			operation.Scope.OrganizationID,
		).
		Update("status", models.ProjectStatusArchived)
	if archive.Error != nil || archive.RowsAffected != 1 {
		t.Fatalf(
			"archive project with pending upload: rows=%d err=%v",
			archive.RowsAffected,
			archive.Error,
		)
	}

	auditLedger, err := NewAuditLedgerService(fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewAgentNativeService(
		fixture.db,
		AgentNativeOptions{
			AttachmentStorage:  fixture.storage.LocalAttachmentStorage,
			AttachmentStaging:  fixture.storage.LocalAttachmentStorage,
			AttachmentMaxBytes: 1024,
			AuditLedger:        auditLedger,
		},
	)
	wrongWorkerContext, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  operation.Scope,
			Actor:  models.SystemActor("not-the-outbox-worker"),
			Source: SourceProtocolWorker,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.ExecuteAttachmentUploadOutbox(
		wrongWorkerContext,
		uploadResult.Attachment.ID,
	); !errors.Is(err, ErrInvalidActor) {
		t.Fatalf("untrusted archived cleanup error = %v", err)
	}
	fixture.assertObjectExists(
		t,
		uploadResult.Attachment.StoragePath,
	)
	fixture.assertObjectExists(t, finalKey)

	injected := errors.New("injected cancellation outbox failure")
	const callbackName = "test:attachment_upload_cancellation_outbox_failure"
	if err := fixture.db.Callback().Create().
		Before("gorm:create").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement == nil ||
				tx.Statement.Context.Value(
					attachmentUploadCancellationFailureContextKey{},
				) != true ||
				tx.Statement.Table != "outbox_deliveries" {
				return
			}
			_ = tx.AddError(injected)
		}); err != nil {
		t.Fatal(err)
	}
	callbackRegistered := true
	t.Cleanup(func() {
		if callbackRegistered {
			_ = fixture.db.Callback().Create().Remove(
				callbackName,
			)
		}
	})
	failingContext := context.WithValue(
		workerContext,
		attachmentUploadCancellationFailureContextKey{},
		true,
	)
	if err := restarted.ExecuteAttachmentUploadOutbox(
		failingContext,
		uploadResult.Attachment.ID,
	); !errors.Is(err, injected) {
		t.Fatalf(
			"cancelled upload atomic failure = %v, want %v",
			err,
			injected,
		)
	}
	if err := fixture.db.Callback().Create().Remove(
		callbackName,
	); err != nil {
		t.Fatal(err)
	}
	callbackRegistered = false
	fixture.assertObjectExists(
		t,
		uploadResult.Attachment.StoragePath,
	)
	fixture.assertObjectExists(t, finalKey)
	assertAttachmentStorageReference(
		t,
		fixture.db,
		uploadResult.Attachment.ID,
		"staging",
		uploadResult.Attachment.StoragePath,
	)
	var ticketBeforeRetry models.Ticket
	if err := fixture.db.Select("id", "version").
		Where("id = ?", fixture.ticket.ID).
		Take(&ticketBeforeRetry).Error; err != nil {
		t.Fatal(err)
	}
	if ticketBeforeRetry.Version !=
		uploadResult.Receipt.ResourceVersion {
		t.Fatalf(
			"failed cancellation changed Ticket version to %d",
			ticketBeforeRetry.Version,
		)
	}
	var rolledBackCancellationEvents int64
	if err := fixture.db.Model(&models.DomainEvent{}).
		Where(
			"type = ? AND subject = ?",
			eventcontract.TicketAttachmentUploadCancelledEventType,
			"ticket/"+fixture.ticketIDText(),
		).
		Count(&rolledBackCancellationEvents).Error; err != nil {
		t.Fatal(err)
	}
	if rolledBackCancellationEvents != 0 {
		t.Fatalf(
			"failed cancellation committed %d terminal events",
			rolledBackCancellationEvents,
		)
	}
	var rolledBackCancellationAudit int64
	if err := fixture.db.Model(&models.AuditLedgerEntry{}).
		Where(
			"event_type = ?",
			eventcontract.TicketAttachmentUploadCancelledEventType,
		).
		Count(&rolledBackCancellationAudit).Error; err != nil {
		t.Fatal(err)
	}
	if rolledBackCancellationAudit != 0 {
		t.Fatalf(
			"failed cancellation committed %d audit entries",
			rolledBackCancellationAudit,
		)
	}

	if err := restarted.ExecuteAttachmentUploadOutbox(
		workerContext,
		uploadResult.Attachment.ID,
	); err != nil {
		t.Fatalf("archived pending upload cleanup: %v", err)
	}
	fixture.assertObjectMissing(
		t,
		uploadResult.Attachment.StoragePath,
	)
	fixture.assertObjectMissing(t, finalKey)
	var tombstone models.TicketAttachment
	if err := fixture.db.Where(
		"id = ?",
		uploadResult.Attachment.ID,
	).Take(&tombstone).Error; err != nil {
		t.Fatalf("load cancelled attachment tombstone: %v", err)
	}
	if tombstone.StorageType !=
		attachmentUploadCancelledStorageType ||
		tombstone.StoragePath != "" {
		t.Fatalf(
			"cancelled attachment tombstone retained live storage: type=%q path=%q",
			tombstone.StorageType,
			tombstone.StoragePath,
		)
	}
	var liveObjectReferences int64
	if err := fixture.db.Model(&models.TicketAttachment{}).
		Where(
			"storage_path IN ?",
			[]string{
				uploadResult.Attachment.StoragePath,
				finalKey,
			},
		).
		Count(&liveObjectReferences).Error; err != nil {
		t.Fatal(err)
	}
	if liveObjectReferences != 0 {
		t.Fatalf(
			"archived cleanup retained %d live object references",
			liveObjectReferences,
		)
	}
	var ticket models.Ticket
	if err := fixture.db.Select("id", "version").
		Where("id = ?", fixture.ticket.ID).
		Take(&ticket).Error; err != nil {
		t.Fatalf("load Ticket after cancelled upload: %v", err)
	}
	wantVersion := uploadResult.Receipt.ResourceVersion + 1
	if ticket.Version != wantVersion {
		t.Fatalf(
			"cancelled upload Ticket version = %d, want %d",
			ticket.Version,
			wantVersion,
		)
	}
	var cancelledEvents []models.DomainEvent
	if err := fixture.db.Where(
		"type = ? AND subject = ?",
		eventcontract.TicketAttachmentUploadCancelledEventType,
		"ticket/"+fixture.ticketIDText(),
	).Find(&cancelledEvents).Error; err != nil {
		t.Fatalf("load attachment upload cancellation event: %v", err)
	}
	if len(cancelledEvents) != 1 {
		t.Fatalf(
			"attachment upload cancellation events = %d, want 1",
			len(cancelledEvents),
		)
	}
	cancelledEvent := cancelledEvents[0]
	if cancelledEvent.ResourceVersion != wantVersion ||
		cancelledEvent.CausationID != uploadResult.Event.ID ||
		cancelledEvent.ActorType != models.ActorTypeSystem ||
		cancelledEvent.ActorID != outboxSystemActorID {
		t.Fatalf(
			"cancelled upload event provenance is inconsistent: %+v",
			cancelledEvent,
		)
	}
	var cancellationData struct {
		TicketID     uint   `json:"ticket_id"`
		AttachmentID uint   `json:"attachment_id"`
		StorageState string `json:"storage_state"`
		Reason       string `json:"reason"`
	}
	if err := json.Unmarshal(
		cancelledEvent.Data,
		&cancellationData,
	); err != nil {
		t.Fatal(err)
	}
	if cancellationData.TicketID != fixture.ticket.ID ||
		cancellationData.AttachmentID !=
			uploadResult.Attachment.ID ||
		cancellationData.StorageState !=
			attachmentUploadCancelledStorageType ||
		cancellationData.Reason != "project_archived" {
		t.Fatalf(
			"cancelled upload event data is inconsistent: %+v",
			cancellationData,
		)
	}
	var cancellationHistory models.TicketHistory
	if err := fixture.db.Where(
		"event_id = ?",
		cancelledEvent.ID,
	).Take(&cancellationHistory).Error; err != nil {
		t.Fatalf("load cancelled upload history: %v", err)
	}
	if cancellationHistory.AttachmentID == nil ||
		*cancellationHistory.AttachmentID !=
			uploadResult.Attachment.ID ||
		cancellationHistory.ResourceVersion != wantVersion ||
		cancellationHistory.Provenance !=
			models.TicketHistoryProvenanceDomainEvent ||
		cancellationHistory.Actor() !=
			models.SystemActor(outboxSystemActorID) {
		t.Fatalf(
			"cancelled upload history link is inconsistent: %+v",
			cancellationHistory,
		)
	}
	var historyDetails map[string]any
	if err := json.Unmarshal(
		[]byte(cancellationHistory.Details),
		&historyDetails,
	); err != nil {
		t.Fatalf("parse cancelled upload history details: %v", err)
	}
	if historyDetails["storage_state"] !=
		attachmentUploadCancelledStorageType ||
		historyDetails["reason"] != "project_archived" {
		t.Fatalf(
			"cancelled upload history is not parseable: %+v",
			historyDetails,
		)
	}
	var originalHistory models.TicketHistory
	if err := fixture.db.Where(
		"event_id = ?",
		uploadResult.Event.ID,
	).Take(&originalHistory).Error; err != nil {
		t.Fatalf("load original upload history: %v", err)
	}
	if originalHistory.AttachmentID == nil ||
		*originalHistory.AttachmentID !=
			uploadResult.Attachment.ID ||
		originalHistory.ResourceVersion !=
			uploadResult.Receipt.ResourceVersion {
		t.Fatalf(
			"original upload history became dangling: %+v",
			originalHistory,
		)
	}
	var cancellationOutbox int64
	if err := fixture.db.Model(&models.OutboxDelivery{}).
		Where("event_id = ?", cancelledEvent.ID).
		Count(&cancellationOutbox).Error; err != nil {
		t.Fatal(err)
	}
	if cancellationOutbox != 1 {
		t.Fatalf(
			"cancelled upload Outbox rows = %d, want 1",
			cancellationOutbox,
		)
	}
	var cancellationAudit models.AuditLedgerEntry
	if err := fixture.db.Where(
		"domain_event_id = ?",
		cancelledEvent.ID,
	).Take(&cancellationAudit).Error; err != nil {
		t.Fatalf("load cancelled upload audit entry: %v", err)
	}
	if cancellationAudit.EventType !=
		eventcontract.TicketAttachmentUploadCancelledEventType ||
		cancellationAudit.ResourceVersion != wantVersion ||
		cancellationAudit.Actor() !=
			models.SystemActor(outboxSystemActorID) ||
		cancellationAudit.Outcome !=
			models.AuditLedgerOutcomeSucceeded {
		t.Fatalf(
			"cancelled upload audit chain is inconsistent: %+v",
			cancellationAudit,
		)
	}
	assertAttachmentCreatedEventCount(
		t,
		fixture.db,
		uploadResult.Attachment.ID,
		0,
	)
	if err := restarted.ExecuteAttachmentUploadOutbox(
		workerContext,
		uploadResult.Attachment.ID,
	); err != nil {
		t.Fatalf("retry archived pending upload cleanup: %v", err)
	}
	var retryCancellationEvents int64
	if err := fixture.db.Model(&models.DomainEvent{}).
		Where(
			"type = ? AND subject = ?",
			eventcontract.TicketAttachmentUploadCancelledEventType,
			"ticket/"+fixture.ticketIDText(),
		).
		Count(&retryCancellationEvents).Error; err != nil {
		t.Fatal(err)
	}
	if retryCancellationEvents != 1 {
		t.Fatalf(
			"retry duplicated cancellation events: %d",
			retryCancellationEvents,
		)
	}
}

type attachmentUploadTransientErrorContextKey struct{}

func TestAttachmentUploadTransientFinalErrorPreservesObjectForRetry(
	t *testing.T,
) {
	fixture := newAttachmentUploadDeleteRaceFixture(t)
	uploadResult, workerContext := fixture.stageCommittedUpload(t)
	finalKey := "tickets/" +
		fixture.ticketIDText() + "/" +
		uploadResult.Attachment.FileName
	injected := errors.New("injected transient final transaction failure")
	const callbackName = "test:attachment_upload_transient_final_error"
	if err := fixture.db.Callback().Update().
		Before("gorm:update").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement == nil ||
				tx.Statement.Context.Value(
					attachmentUploadTransientErrorContextKey{},
				) != true ||
				tx.Statement.Table != "ticket_attachments" {
				return
			}
			_ = tx.AddError(injected)
		}); err != nil {
		t.Fatal(err)
	}
	callbackRegistered := true
	t.Cleanup(func() {
		if callbackRegistered {
			_ = fixture.db.Callback().Update().Remove(callbackName)
		}
	})
	failingContext := context.WithValue(
		workerContext,
		attachmentUploadTransientErrorContextKey{},
		true,
	)
	if err := fixture.service.ExecuteAttachmentUploadOutbox(
		failingContext,
		uploadResult.Attachment.ID,
	); !errors.Is(err, injected) {
		t.Fatalf(
			"transient final transaction error = %v, want %v",
			err,
			injected,
		)
	}
	if err := fixture.db.Callback().Update().Remove(
		callbackName,
	); err != nil {
		t.Fatal(err)
	}
	callbackRegistered = false

	fixture.assertObjectExists(t, finalKey)
	fixture.assertObjectExists(
		t,
		uploadResult.Attachment.StoragePath,
	)
	assertAttachmentStorageReference(
		t,
		fixture.db,
		uploadResult.Attachment.ID,
		"staging",
		uploadResult.Attachment.StoragePath,
	)

	if err := fixture.service.ExecuteAttachmentUploadOutbox(
		workerContext,
		uploadResult.Attachment.ID,
	); err != nil {
		t.Fatalf("retry transient attachment finalization: %v", err)
	}
	fixture.assertObjectExists(t, finalKey)
	fixture.assertObjectMissing(
		t,
		uploadResult.Attachment.StoragePath,
	)
	assertAttachmentStorageReference(
		t,
		fixture.db,
		uploadResult.Attachment.ID,
		"local",
		finalKey,
	)
	assertAttachmentCreatedEventCount(
		t,
		fixture.db,
		uploadResult.Attachment.ID,
		1,
	)
}

type attachmentUploadAmbiguousCommitContextKey struct{}

func TestAttachmentUploadAmbiguousCommitPreservesLiveFinalObject(
	t *testing.T,
) {
	fixture := newAttachmentUploadDeleteRaceFixture(t)
	uploadResult, workerContext := fixture.stageCommittedUpload(t)
	finalKey := "tickets/" +
		fixture.ticketIDText() + "/" +
		uploadResult.Attachment.FileName
	injected := errors.New("injected ambiguous transaction commit result")
	const callbackName = "test:attachment_upload_ambiguous_commit"
	intercepted := false
	if err := fixture.db.Callback().Create().
		After("gorm:create").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement == nil ||
				tx.Statement.Context.Value(
					attachmentUploadAmbiguousCommitContextKey{},
				) != true ||
				tx.Statement.Table != "outbox_deliveries" ||
				tx.Error != nil {
				return
			}
			committer, ok := tx.Statement.ConnPool.(gorm.TxCommitter)
			if !ok {
				_ = tx.AddError(errors.New(
					"attachment ambiguous-commit test requires a transaction",
				))
				return
			}
			if err := committer.Commit(); err != nil {
				_ = tx.AddError(err)
				return
			}
			intercepted = true
			_ = tx.AddError(injected)
		}); err != nil {
		t.Fatal(err)
	}
	callbackRegistered := true
	t.Cleanup(func() {
		if callbackRegistered {
			_ = fixture.db.Callback().Create().Remove(callbackName)
		}
	})
	ambiguousContext := context.WithValue(
		workerContext,
		attachmentUploadAmbiguousCommitContextKey{},
		true,
	)
	finalizeErr := fixture.service.ExecuteAttachmentUploadOutbox(
		ambiguousContext,
		uploadResult.Attachment.ID,
	)
	if !intercepted {
		t.Fatal("ambiguous commit barrier did not intercept the final transaction")
	}
	if !errors.Is(finalizeErr, injected) {
		t.Fatalf(
			"ambiguous final transaction error = %v, want %v",
			finalizeErr,
			injected,
		)
	}
	if err := fixture.db.Callback().Create().Remove(
		callbackName,
	); err != nil {
		t.Fatal(err)
	}
	callbackRegistered = false

	fixture.assertObjectExists(t, finalKey)
	fixture.assertObjectExists(
		t,
		uploadResult.Attachment.StoragePath,
	)
	assertAttachmentStorageReference(
		t,
		fixture.db,
		uploadResult.Attachment.ID,
		"local",
		finalKey,
	)
	assertAttachmentCreatedEventCount(
		t,
		fixture.db,
		uploadResult.Attachment.ID,
		1,
	)
	intent, err := newAttachmentUploadMigrationIntent(
		*uploadResult.Attachment,
		fixture.service.attachmentStorage,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.cleanupObsoleteAttachmentUpload(
		workerContext,
		intent,
		nil,
	); !errors.Is(err, errAttachmentUploadStillReferenced) {
		t.Fatalf(
			"live-reference compensation proof error = %v, want %v",
			err,
			errAttachmentUploadStillReferenced,
		)
	}
	fixture.assertObjectExists(t, finalKey)

	if err := fixture.service.ExecuteAttachmentUploadOutbox(
		workerContext,
		uploadResult.Attachment.ID,
	); err != nil {
		t.Fatalf("retry ambiguous attachment finalization: %v", err)
	}
	fixture.assertObjectExists(t, finalKey)
	fixture.assertObjectMissing(
		t,
		uploadResult.Attachment.StoragePath,
	)
	assertAttachmentCreatedEventCount(
		t,
		fixture.db,
		uploadResult.Attachment.ID,
		1,
	)
}

func TestAttachmentUploadConcurrentFinalizeIsIdempotent(
	t *testing.T,
) {
	fixture := newAttachmentUploadDeleteRaceFixture(t)
	uploadResult, workerContext := fixture.stageCommittedUpload(t)
	finalKey := "tickets/" +
		fixture.ticketIDText() + "/" +
		uploadResult.Attachment.FileName
	storage := &attachmentUploadConcurrentFinalizeStorage{
		LocalAttachmentStorage: fixture.storage.LocalAttachmentStorage,
		finalKey:               finalKey,
		arrivals:               make(chan struct{}, 2),
		release:                make(chan struct{}),
	}
	t.Cleanup(storage.releasePuts)
	service := NewAgentNativeService(
		fixture.db,
		AgentNativeOptions{
			AttachmentStorage:  storage,
			AttachmentStaging:  storage,
			AttachmentMaxBytes: 1024,
		},
	)
	results := make(chan error, 2)
	for range 2 {
		go func() {
			results <- service.ExecuteAttachmentUploadOutbox(
				workerContext,
				uploadResult.Attachment.ID,
			)
		}()
	}
	storage.waitForPuts(t, 2)
	storage.releasePuts()
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("concurrent attachment finalizer: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent attachment finalizer timed out")
		}
	}

	fixture.assertObjectExists(t, finalKey)
	fixture.assertObjectMissing(
		t,
		uploadResult.Attachment.StoragePath,
	)
	assertAttachmentStorageReference(
		t,
		fixture.db,
		uploadResult.Attachment.ID,
		"local",
		finalKey,
	)
	assertAttachmentCreatedEventCount(
		t,
		fixture.db,
		uploadResult.Attachment.ID,
		1,
	)
}

func TestAttachmentUploadFinalizationCommitsEventAndAudit(
	t *testing.T,
) {
	fixture := newAttachmentUploadDeleteRaceFixture(t)
	uploadResult, workerContext := fixture.stageCommittedUpload(t)
	auditLedger, err := NewAuditLedgerService(fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	service := NewAgentNativeService(
		fixture.db,
		AgentNativeOptions{
			AttachmentStorage: fixture.storage.
				LocalAttachmentStorage,
			AttachmentStaging: fixture.storage.
				LocalAttachmentStorage,
			AttachmentMaxBytes: 1024,
			AuditLedger:        auditLedger,
		},
	)
	if err := service.ExecuteAttachmentUploadOutbox(
		workerContext,
		uploadResult.Attachment.ID,
	); err != nil {
		t.Fatalf("finalize audited attachment upload: %v", err)
	}
	var event models.DomainEvent
	if err := fixture.db.Where(
		"type = ? AND subject = ?",
		eventcontract.TicketAttachmentCreatedEventType,
		"ticket/"+fixture.ticketIDText(),
	).Take(&event).Error; err != nil {
		t.Fatalf("load audited attachment-created event: %v", err)
	}
	if event.ActorType != models.ActorTypeSystem ||
		event.ActorID != outboxSystemActorID ||
		event.ResourceVersion !=
			uploadResult.Receipt.ResourceVersion {
		t.Fatalf(
			"attachment-created event provenance is inconsistent: %+v",
			event,
		)
	}
	var audit models.AuditLedgerEntry
	if err := fixture.db.Where(
		"domain_event_id = ?",
		event.ID,
	).Take(&audit).Error; err != nil {
		t.Fatalf("load attachment-created audit entry: %v", err)
	}
	if audit.EventType !=
		eventcontract.TicketAttachmentCreatedEventType ||
		audit.ResourceVersion != event.ResourceVersion ||
		audit.Actor() != models.SystemActor(outboxSystemActorID) ||
		audit.Outcome != models.AuditLedgerOutcomeSucceeded {
		t.Fatalf(
			"attachment-created audit chain is inconsistent: %+v",
			audit,
		)
	}
}

type attachmentUploadDeleteRaceFixture struct {
	db           *gorm.DB
	service      *AgentNativeService
	storage      *attachmentUploadDeleteBarrierStorage
	user         models.User
	ticket       models.Ticket
	humanContext context.Context
}

func newAttachmentUploadDeleteRaceFixture(
	t *testing.T,
) attachmentUploadDeleteRaceFixture {
	t.Helper()
	db := openAgentNativeTestDB(t)
	if err := db.AutoMigrate(&models.Notification{}); err != nil {
		t.Fatalf("migrate attachment delete dependencies: %v", err)
	}
	user := seedActorUser(t, db, "attachment-upload-delete")
	local, err := NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	storage := &attachmentUploadDeleteBarrierStorage{
		LocalAttachmentStorage: local,
		finalPutCompleted:      make(chan struct{}),
		releaseFinal:           make(chan struct{}),
	}
	t.Cleanup(storage.releaseFinalPut)
	service := NewAgentNativeService(
		db,
		AgentNativeOptions{
			AttachmentStorage:  storage,
			AttachmentStaging:  storage,
			AttachmentMaxBytes: 1024,
		},
	)
	humanContext := testProjectOperationContext(
		t,
		db,
		models.HumanActor(user.ID),
	)
	ensureAttachmentTestAuthorization(
		t,
		db,
		humanContext,
		models.HumanActor(user.ID),
	)
	ticket := seedNativeTicket(
		t,
		db,
		user.ID,
		"ATTACH-UPLOAD-DELETE-001",
	)
	return attachmentUploadDeleteRaceFixture{
		db:           db,
		service:      service,
		storage:      storage,
		user:         user,
		ticket:       ticket,
		humanContext: humanContext,
	}
}

func (fixture attachmentUploadDeleteRaceFixture) stageCommittedUpload(
	t *testing.T,
) (*NativeAttachmentResult, context.Context) {
	t.Helper()
	result, err := fixture.service.StoreAttachment(
		fixture.humanContext,
		NativeAttachmentInput{
			TicketID:        fixture.ticket.ID,
			ExpectedVersion: fixture.ticket.Version,
			Actor:           models.HumanActor(fixture.user.ID),
			OriginalName:    "delete-race.txt",
			ContentType:     "text/plain",
			Reader: bytes.NewBufferString(
				"durable upload bytes",
			),
		},
	)
	if err != nil {
		t.Fatalf("stage committed attachment upload: %v", err)
	}
	operation, err := OperationContextFromContext(
		fixture.humanContext,
	)
	if err != nil {
		t.Fatal(err)
	}
	workerContext, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  operation.Scope,
			Actor:  models.SystemActor(outboxSystemActorID),
			Source: SourceProtocolWorker,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return result, workerContext
}

func (fixture attachmentUploadDeleteRaceFixture) ticketIDText() string {
	return strconv.FormatUint(uint64(fixture.ticket.ID), 10)
}

type attachmentUploadDeleteBarrierStorage struct {
	*LocalAttachmentStorage
	finalKey          string
	finalPutCompleted chan struct{}
	finalPutOnce      sync.Once
	releaseFinal      chan struct{}
	releaseOnce       sync.Once
}

func (storage *attachmentUploadDeleteBarrierStorage) pauseFinalKey(
	key string,
) {
	storage.finalKey = key
}

func (storage *attachmentUploadDeleteBarrierStorage) Put(
	ctx context.Context,
	key string,
	reader io.Reader,
	maxBytes int64,
) (*StoredAttachmentObject, error) {
	stored, err := storage.LocalAttachmentStorage.Put(
		ctx,
		key,
		reader,
		maxBytes,
	)
	if err != nil || key != storage.finalKey {
		return stored, err
	}
	storage.finalPutOnce.Do(func() {
		close(storage.finalPutCompleted)
	})
	select {
	case <-storage.releaseFinal:
		return stored, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (storage *attachmentUploadDeleteBarrierStorage) waitForFinalPut(
	t *testing.T,
) {
	t.Helper()
	select {
	case <-storage.finalPutCompleted:
	case <-time.After(5 * time.Second):
		t.Fatal("attachment final Put did not reach the delete barrier")
	}
}

func (storage *attachmentUploadDeleteBarrierStorage) releaseFinalPut() {
	storage.releaseOnce.Do(func() {
		close(storage.releaseFinal)
	})
}

type attachmentUploadConcurrentFinalizeStorage struct {
	*LocalAttachmentStorage
	finalKey    string
	arrivals    chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
	writeMu     sync.Mutex
}

func (storage *attachmentUploadConcurrentFinalizeStorage) Put(
	ctx context.Context,
	key string,
	reader io.Reader,
	maxBytes int64,
) (*StoredAttachmentObject, error) {
	if key != storage.finalKey {
		return storage.LocalAttachmentStorage.Put(
			ctx,
			key,
			reader,
			maxBytes,
		)
	}
	select {
	case storage.arrivals <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-storage.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	storage.writeMu.Lock()
	defer storage.writeMu.Unlock()
	return storage.LocalAttachmentStorage.Put(
		ctx,
		key,
		reader,
		maxBytes,
	)
}

func (storage *attachmentUploadConcurrentFinalizeStorage) waitForPuts(
	t *testing.T,
	count int,
) {
	t.Helper()
	for range count {
		select {
		case <-storage.arrivals:
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent attachment Put did not reach the barrier")
		}
	}
}

func (storage *attachmentUploadConcurrentFinalizeStorage) releasePuts() {
	storage.releaseOnce.Do(func() {
		close(storage.release)
	})
}

func (fixture attachmentUploadDeleteRaceFixture) assertObjectExists(
	t *testing.T,
	key string,
) {
	t.Helper()
	reader, err := fixture.storage.LocalAttachmentStorage.Open(
		context.Background(),
		key,
	)
	if err != nil {
		t.Fatalf("attachment object %q is missing: %v", key, err)
	}
	_ = reader.Close()
}

func (fixture attachmentUploadDeleteRaceFixture) assertObjectMissing(
	t *testing.T,
	key string,
) {
	t.Helper()
	reader, err := fixture.storage.LocalAttachmentStorage.Open(
		context.Background(),
		key,
	)
	if err == nil {
		_ = reader.Close()
		t.Fatalf("orphan attachment object %q still exists", key)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(
			"inspect missing attachment object %q: %v",
			key,
			err,
		)
	}
}

func assertAttachmentStorageReference(
	t *testing.T,
	db *gorm.DB,
	attachmentID uint,
	storageType string,
	storagePath string,
) {
	t.Helper()
	var attachment models.TicketAttachment
	if err := db.Where("id = ?", attachmentID).
		Take(&attachment).Error; err != nil {
		t.Fatalf("load live attachment reference: %v", err)
	}
	if attachment.StorageType != storageType ||
		attachment.StoragePath != storagePath {
		t.Fatalf(
			"attachment storage reference = (%q, %q), want (%q, %q)",
			attachment.StorageType,
			attachment.StoragePath,
			storageType,
			storagePath,
		)
	}
}

func assertAttachmentCreatedEventCount(
	t *testing.T,
	db *gorm.DB,
	attachmentID uint,
	want int64,
) {
	t.Helper()
	var count int64
	attachmentFragment := "%\"attachment_id\":" +
		strconv.FormatUint(uint64(attachmentID), 10) + "%"
	if err := db.Model(&models.DomainEvent{}).
		Where(
			"type = ? AND data LIKE ?",
			eventcontract.TicketAttachmentCreatedEventType,
			attachmentFragment,
		).
		Count(&count).Error; err != nil {
		t.Fatalf("count attachment-created events: %v", err)
	}
	if count != want {
		t.Fatalf(
			"attachment-created event count = %d, want %d",
			count,
			want,
		)
	}
}

func assertAttachmentUploadMigrationIntentIsPrivate(
	t *testing.T,
	db *gorm.DB,
	ticketID uint,
	attachmentID uint,
	stagingKey string,
	finalKey string,
) {
	t.Helper()
	var event models.DomainEvent
	if err := db.Where(
		"type = ? AND subject = ?",
		"io.chronodesk.ticket.attachment.upload-requested.v1",
		"ticket/"+strconv.FormatUint(uint64(ticketID), 10),
	).Order("created_at DESC").Take(&event).Error; err != nil {
		t.Fatalf("load durable attachment upload intent event: %v", err)
	}
	envelope := CloudEventFromModel(&event)
	wire, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal public upload event: %v", err)
	}
	for _, secret := range []string{
		AttachmentUploadMigrationDataField,
		stagingKey,
		finalKey,
	} {
		if strings.Contains(string(envelope.Data), secret) ||
			strings.Contains(string(wire), secret) {
			t.Fatalf(
				"public upload event leaked private migration value %q: %s",
				secret,
				wire,
			)
		}
	}
	if !strings.Contains(
		string(envelope.InternalData),
		AttachmentUploadMigrationDataField,
	) ||
		!strings.Contains(string(envelope.InternalData), finalKey) ||
		!strings.Contains(
			string(envelope.InternalData),
			strconv.FormatUint(uint64(attachmentID), 10),
		) {
		t.Fatalf(
			"durable upload migration intent is incomplete: %s",
			envelope.InternalData,
		)
	}
	var public map[string]any
	if err := json.Unmarshal(envelope.Data, &public); err != nil {
		t.Fatal(err)
	}
	if public["ticket_id"] != float64(ticketID) {
		t.Fatalf("public upload event lost Ticket identity: %+v", public)
	}
}

func assertAttachmentUploadDeleteHasNoLiveDatabaseReference(
	t *testing.T,
	db *gorm.DB,
	ticketID uint,
	attachmentID uint,
) {
	t.Helper()
	var attachments int64
	if err := db.Unscoped().
		Model(&models.TicketAttachment{}).
		Where(
			"id = ? OR ticket_id = ?",
			attachmentID,
			ticketID,
		).
		Count(&attachments).Error; err != nil {
		t.Fatal(err)
	}
	if attachments != 0 {
		t.Fatalf(
			"deleted Ticket retained %d attachment database rows",
			attachments,
		)
	}
	var histories int64
	if err := db.Model(&models.TicketHistory{}).
		Where("attachment_id = ?", attachmentID).
		Count(&histories).Error; err != nil {
		t.Fatal(err)
	}
	if histories != 0 {
		t.Fatalf(
			"deleted Ticket retained %d attachment history references",
			histories,
		)
	}
}
