package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

func TestAttachmentStagingIntentSurvivesCrashAndRestartCleanup(
	t *testing.T,
) {
	db := openAgentNativeTestDB(t)
	if err := db.AutoMigrate(&models.Notification{}); err != nil {
		t.Fatalf("migrate ticket deletion dependencies: %v", err)
	}
	user := seedActorUser(t, db, "attachment-staging-crash")
	root := t.TempDir()
	storage, err := NewLocalAttachmentStorage(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 31, 2, 0, 0, 0, time.UTC)
	options := AgentNativeOptions{
		AttachmentStorage:  storage,
		AttachmentStaging:  storage,
		AttachmentMaxBytes: 1024,
		Now: func() time.Time {
			return now
		},
	}
	service := NewAgentNativeService(db, options)
	actor := models.HumanActor(user.ID)
	ctx := testProjectOperationContext(t, db, actor)
	ensureAttachmentTestAuthorization(t, db, ctx, actor)
	ticket := seedNativeTicket(
		t,
		db,
		user.ID,
		"ATTACH-STAGING-CRASH-001",
	)
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	input := NativeAttachmentInput{
		TicketID:        ticket.ID,
		ExpectedVersion: 1,
		Actor:           actor,
		OriginalName:    "restart-proof.txt",
		Reader:          bytes.NewBufferString("durable staged bytes"),
	}
	if err := service.PrepareAttachmentUploadAuthorization(
		ctx,
		&input,
	); err != nil {
		t.Fatal(err)
	}
	initialAccess, err := service.captureAttachmentAuthorization(
		ctx,
		models.ScopeAttachmentsWrite,
	)
	if err != nil {
		t.Fatal(err)
	}
	safeName, err := SafeAttachmentName(input.OriginalName)
	if err != nil {
		t.Fatal(err)
	}
	extension := safeAttachmentExtension(safeName)
	fileName := newNativeID() + extension
	stagingKey := attachmentStagingKey(fileName)
	attachment := &models.TicketAttachment{
		TicketID:     input.TicketID,
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		FileName:     fileName,
		OriginalName: safeName,
		Extension:    extension,
		StoragePath:  stagingKey,
		StorageType:  attachmentStagingIntentStorageType,
		VirusScan:    models.VirusScanPending,
	}
	if err := service.registerAttachmentStagingIntent(
		ctx,
		operation,
		input,
		initialAccess,
		attachmentUploadPolicyCheck(
			operation,
			input,
			safeName,
		),
		attachment,
	); err != nil {
		t.Fatalf("register staging intent: %v", err)
	}
	var cleanupDelivery models.OutboxDelivery
	if err := db.Where(
		"destination_type = ? AND destination_id = ?",
		AttachmentStagingCleanupOutboxDestination,
		strconv.FormatUint(uint64(attachment.ID), 10),
	).Take(&cleanupDelivery).Error; err != nil {
		t.Fatal(err)
	}
	wantDeadline := now.Add(attachmentStagingCleanupDelay)
	if !cleanupDelivery.NextAttemptAt.Equal(wantDeadline) ||
		cleanupDelivery.Status != models.OutboxDeliveryPending {
		t.Fatalf(
			"cleanup delivery = %+v, want pending at %s",
			cleanupDelivery,
			wantDeadline,
		)
	}

	cleanupStateInput := NativeAttachmentInput{
		TicketID:        ticket.ID,
		ExpectedVersion: input.ExpectedVersion,
		Actor:           actor,
		OriginalName:    "cleanup-state.txt",
		Reader:          bytes.NewBufferString("cleanup-state staged bytes"),
	}
	if err := service.PrepareAttachmentUploadAuthorization(
		ctx,
		&cleanupStateInput,
	); err != nil {
		t.Fatal(err)
	}
	cleanupStateAccess, err := service.captureAttachmentAuthorization(
		ctx,
		models.ScopeAttachmentsWrite,
	)
	if err != nil {
		t.Fatal(err)
	}
	cleanupStateSafeName, err := SafeAttachmentName(
		cleanupStateInput.OriginalName,
	)
	if err != nil {
		t.Fatal(err)
	}
	cleanupStateExtension := safeAttachmentExtension(cleanupStateSafeName)
	cleanupStateFileName := newNativeID() + cleanupStateExtension
	cleanupStateKey := attachmentStagingKey(cleanupStateFileName)
	cleanupStateAttachment := &models.TicketAttachment{
		TicketID:     cleanupStateInput.TicketID,
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		FileName:     cleanupStateFileName,
		OriginalName: cleanupStateSafeName,
		Extension:    cleanupStateExtension,
		StoragePath:  cleanupStateKey,
		StorageType:  attachmentStagingIntentStorageType,
		VirusScan:    models.VirusScanPending,
	}
	if err := service.registerAttachmentStagingIntent(
		ctx,
		operation,
		cleanupStateInput,
		cleanupStateAccess,
		attachmentUploadPolicyCheck(
			operation,
			cleanupStateInput,
			cleanupStateSafeName,
		),
		cleanupStateAttachment,
	); err != nil {
		t.Fatalf("register cleanup-state intent: %v", err)
	}
	updateCleanupState := db.Model(&models.TicketAttachment{}).
		Where(
			"id = ? AND storage_type = ? AND storage_path = ?",
			cleanupStateAttachment.ID,
			attachmentStagingIntentStorageType,
			cleanupStateKey,
		).
		Update(
			"storage_type",
			attachmentStagingCleanupStorageType,
		)
	if updateCleanupState.Error != nil ||
		updateCleanupState.RowsAffected != 1 {
		t.Fatalf(
			"simulate cleanup fence: rows=%d err=%v",
			updateCleanupState.RowsAffected,
			updateCleanupState.Error,
		)
	}
	if _, err := storage.Stage(
		ctx,
		stagingKey,
		input.Reader,
		1024,
	); err != nil {
		t.Fatalf("stage upload before simulated crash: %v", err)
	}
	stagedPath := filepath.Join(
		root,
		filepath.FromSlash(stagingKey),
	)
	if _, err := os.Stat(stagedPath); err != nil {
		t.Fatalf("staged object missing before restart: %v", err)
	}
	partialPath := stagedPath + ".partial"
	if err := os.WriteFile(
		partialPath,
		[]byte("bytes left by a hard crash before rename"),
		0o600,
	); err != nil {
		t.Fatalf("seed hard-crash partial: %v", err)
	}
	if _, err := storage.Stage(
		ctx,
		cleanupStateKey,
		cleanupStateInput.Reader,
		1024,
	); err != nil {
		t.Fatalf("stage cleanup-state upload: %v", err)
	}
	cleanupStatePath := filepath.Join(
		root,
		filepath.FromSlash(cleanupStateKey),
	)
	cleanupStatePartialPath := cleanupStatePath + ".partial"
	if err := os.WriteFile(
		cleanupStatePartialPath,
		[]byte("partial bytes left after the cleanup fence"),
		0o600,
	); err != nil {
		t.Fatalf("seed cleanup-state partial: %v", err)
	}
	if err := db.Select("id", "version").
		First(&ticket, ticket.ID).Error; err != nil {
		t.Fatalf("load ticket version before deletion: %v", err)
	}
	deleteReceipt, err := service.DeleteTicket(
		ctx,
		DeleteTicketCommand{
			TicketID:        ticket.ID,
			ExpectedVersion: ticket.Version,
			Actor:           actor,
			SourceProtocol:  "test",
		},
	)
	if err != nil {
		t.Fatalf("delete ticket while staging intent is durable: %v", err)
	}
	var deleteEvent models.DomainEvent
	if err := db.Where("id = ?", deleteReceipt.EventID).
		Take(&deleteEvent).Error; err != nil {
		t.Fatalf("load ticket deletion event: %v", err)
	}
	var deleteData struct {
		CleanupCount int `json:"attachment_cleanup_count"`
	}
	if err := json.Unmarshal(deleteEvent.Data, &deleteData); err != nil {
		t.Fatalf("decode ticket deletion event: %v", err)
	}
	if deleteData.CleanupCount != 0 {
		t.Fatalf(
			"staging intent produced %d generic cleanup targets",
			deleteData.CleanupCount,
		)
	}
	var genericCleanupCount int64
	if err := db.Model(&models.OutboxDelivery{}).
		Where(
			"event_id = ? AND destination_type = ?",
			deleteReceipt.EventID,
			AttachmentCleanupOutboxDestination,
		).
		Count(&genericCleanupCount).Error; err != nil {
		t.Fatalf("count generic cleanup deliveries: %v", err)
	}
	if genericCleanupCount != 0 {
		t.Fatalf(
			"ticket deletion queued %d generic staging cleanups",
			genericCleanupCount,
		)
	}
	var preservedIntent models.TicketAttachment
	if err := db.First(&preservedIntent, attachment.ID).Error; err != nil {
		t.Fatalf("load staging intent preserved for cleanup: %v", err)
	}
	if preservedIntent.DeletedAt != nil ||
		preservedIntent.StorageType !=
			attachmentStagingIntentStorageType ||
		preservedIntent.StoragePath != stagingKey {
		t.Fatalf(
			"unexpected staging intent preserved for cleanup: %+v",
			preservedIntent,
		)
	}
	var preservedCleanupState models.TicketAttachment
	if err := db.First(
		&preservedCleanupState,
		cleanupStateAttachment.ID,
	).Error; err != nil {
		t.Fatalf("load staging cleanup fence after deletion: %v", err)
	}
	if preservedCleanupState.DeletedAt != nil ||
		preservedCleanupState.StorageType !=
			attachmentStagingCleanupStorageType ||
		preservedCleanupState.StoragePath != cleanupStateKey {
		t.Fatalf(
			"unexpected staging cleanup fence after deletion: %+v",
			preservedCleanupState,
		)
	}
	if err := db.Model(&models.Project{}).
		Where(
			"id = ? AND organization_id = ?",
			operation.Scope.ProjectID,
			operation.Scope.OrganizationID,
		).
		Update(
			"status",
			models.ProjectStatusArchived,
		).Error; err != nil {
		t.Fatalf("archive project before cleanup: %v", err)
	}

	now = wantDeadline.Add(time.Second)
	restarted := NewAgentNativeService(db, options)
	workerCtx, err := WithOperationContext(
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
	batch, err := restarted.ProcessOutboxBatch(
		context.Background(),
		"attachment-orphan-sweeper",
		100,
		OutboxDeliverFunc(func(
			deliveryContext context.Context,
			delivery *models.OutboxDelivery,
			_ CloudEventEnvelope,
		) error {
			if delivery.DestinationType !=
				AttachmentStagingCleanupOutboxDestination {
				return errors.New(
					"archived project exposed a non-cleanup delivery",
				)
			}
			destinationID, parseErr := strconv.ParseUint(
				delivery.DestinationID,
				10,
				64,
			)
			if parseErr != nil || destinationID == 0 {
				return errors.New(
					"staging cleanup destination is invalid",
				)
			}
			return restarted.
				ExecuteAttachmentStagingCleanupOutbox(
					deliveryContext,
					uint(destinationID),
				)
		}),
	)
	if err != nil {
		t.Fatalf(
			"process archived cleanup delivery after restart: %v",
			err,
		)
	}
	if batch.Claimed != 2 ||
		batch.Delivered != 2 ||
		batch.Failed != 0 {
		t.Fatalf("archived cleanup batch = %+v", batch)
	}
	if _, err := os.Stat(stagedPath); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatalf("staged object survived cleanup: %v", err)
	}
	if _, err := os.Stat(partialPath); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatalf("hard-crash partial survived cleanup: %v", err)
	}
	if _, err := os.Stat(cleanupStatePath); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatalf("cleanup-state staged object survived cleanup: %v", err)
	}
	if _, err := os.Stat(cleanupStatePartialPath); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatalf("cleanup-state partial survived cleanup: %v", err)
	}
	var persisted models.TicketAttachment
	if err := db.First(
		&persisted,
		"id = ?",
		attachment.ID,
	).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("staging intent survived cleanup: %v", err)
	}
	if err := db.Unscoped().First(
		&persisted,
		"id = ?",
		attachment.ID,
	).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("staging intent tombstone survived cleanup: %v", err)
	}
	if err := db.Unscoped().First(
		&persisted,
		"id = ?",
		cleanupStateAttachment.ID,
	).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("staging cleanup fence survived cleanup: %v", err)
	}
	if err := restarted.ExecuteAttachmentStagingCleanupOutbox(
		workerCtx,
		attachment.ID,
	); err != nil {
		t.Fatalf("idempotent restart cleanup: %v", err)
	}
	if err := restarted.ExecuteAttachmentStagingCleanupOutbox(
		workerCtx,
		cleanupStateAttachment.ID,
	); err != nil {
		t.Fatalf("idempotent cleanup-state restart cleanup: %v", err)
	}
}
