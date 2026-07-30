package services

import (
	"bytes"
	"context"
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
			return restarted.
				ExecuteAttachmentStagingCleanupOutbox(
					deliveryContext,
					attachment.ID,
				)
		}),
	)
	if err != nil {
		t.Fatalf(
			"process archived cleanup delivery after restart: %v",
			err,
		)
	}
	if batch.Claimed != 1 ||
		batch.Delivered != 1 ||
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
	var persisted models.TicketAttachment
	if err := db.First(
		&persisted,
		"id = ?",
		attachment.ID,
	).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("staging intent survived cleanup: %v", err)
	}
	if err := restarted.ExecuteAttachmentStagingCleanupOutbox(
		workerCtx,
		attachment.ID,
	); err != nil {
		t.Fatalf("idempotent restart cleanup: %v", err)
	}
}
