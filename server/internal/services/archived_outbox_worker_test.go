package services

import (
	"context"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestArchivedProjectWorkerClaimsAttachmentUploadForTerminalCancellation(
	t *testing.T,
) {
	fixture := newAttachmentUploadDeleteRaceFixture(t)
	upload, _ := fixture.stageCommittedUpload(t)
	operation, err := OperationContextFromContext(fixture.humanContext)
	if err != nil {
		t.Fatal(err)
	}
	update := fixture.db.Model(&models.Project{}).
		Where(
			"id = ? AND organization_id = ?",
			operation.Scope.ProjectID,
			operation.Scope.OrganizationID,
		).
		Update("status", models.ProjectStatusArchived)
	if update.Error != nil || update.RowsAffected != 1 {
		t.Fatalf(
			"archive pending upload project: rows=%d err=%v",
			update.RowsAffected,
			update.Error,
		)
	}

	worker := NewAgentNativeService(
		fixture.db,
		AgentNativeOptions{
			AttachmentStorage:  fixture.storage.LocalAttachmentStorage,
			AttachmentStaging:  fixture.storage.LocalAttachmentStorage,
			AttachmentMaxBytes: 1024,
		},
	)
	batch, err := worker.ProcessOutboxBatch(
		context.Background(),
		"archived-upload-cancellation-worker",
		10,
		OutboxDeliverFunc(func(
			ctx context.Context,
			delivery *models.OutboxDelivery,
			_ CloudEventEnvelope,
		) error {
			if delivery.DestinationType !=
				AttachmentUploadOutboxDestination {
				t.Fatalf(
					"archived worker exposed destination %q",
					delivery.DestinationType,
				)
			}
			return worker.ExecuteAttachmentUploadOutbox(
				ctx,
				upload.Attachment.ID,
			)
		}),
	)
	if err != nil {
		t.Fatalf("process archived attachment upload: %v", err)
	}
	if batch.Claimed != 1 ||
		batch.Delivered != 1 ||
		batch.Failed != 0 {
		t.Fatalf("archived attachment upload batch = %+v", batch)
	}
	var tombstone models.TicketAttachment
	if err := fixture.db.Where(
		"id = ?",
		upload.Attachment.ID,
	).Take(&tombstone).Error; err != nil {
		t.Fatalf("load archived upload tombstone: %v", err)
	}
	if tombstone.StorageType != attachmentUploadCancelledStorageType ||
		tombstone.StoragePath != "" {
		t.Fatalf(
			"archived worker did not reach terminal cancellation: %+v",
			tombstone,
		)
	}
}

func TestArchivedProjectWorkerClaimsOnlyAccessRevocationEventStreams(
	t *testing.T,
) {
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "archived-outbox-revocation")
	if err := db.Model(&models.User{}).
		Where("id = ?", user.ID).
		Update(
			"platform_role",
			models.PlatformRolePlatformAdmin,
		).Error; err != nil {
		t.Fatal(err)
	}
	service := NewAgentNativeService(
		db,
		AgentNativeOptions{
			DefaultOutboxTargets: []OutboxTarget{{
				Type:        "event_stream",
				ID:          "default",
				MaxAttempts: 3,
			}},
		},
	)
	ctx := testProjectOperationContext(
		t,
		db,
		models.HumanActor(user.ID),
	)
	generic, err := service.createDomainEvent(
		t,
		ctx,
		DomainEventInput{
			Type:    "io.chronodesk.ticket.generic.v1",
			Subject: "ticket/1",
			Actor:   models.HumanActor(user.ID),
			Data:    map[string]any{"ticket_id": 1},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var project models.Project
	if err := db.Where(
		"id = ? AND organization_id = ?",
		operation.Scope.ProjectID,
		operation.Scope.OrganizationID,
	).Take(&project).Error; err != nil {
		t.Fatal(err)
	}
	projectService, err := NewProjectService(db, service)
	if err != nil {
		t.Fatal(err)
	}
	archived, err := projectService.ArchiveProject(
		context.Background(),
		project.PublicID,
		models.HumanActor(user.ID),
	)
	if err != nil {
		t.Fatalf("archive project through production command: %v", err)
	}
	if archived.Status != models.ProjectStatusArchived {
		t.Fatalf("archive command returned %+v", archived)
	}
	var revocation models.DomainEvent
	if err := db.Where(
		"type = ? AND project_id = ?",
		ProjectAccessRevokedEventType,
		project.ID,
	).Take(&revocation).Error; err != nil {
		t.Fatal(err)
	}
	var archivedProject models.Project
	if err := db.
		Where(
			"id = ? AND organization_id = ?",
			operation.Scope.ProjectID,
			operation.Scope.OrganizationID,
		).Take(&archivedProject).Error; err != nil {
		t.Fatal(err)
	}
	if archivedProject.Status != models.ProjectStatusArchived {
		t.Fatalf("project status = %q after archive", archivedProject.Status)
	}

	var delivered []string
	batch, err := service.ProcessOutboxBatch(
		context.Background(),
		"archived-access-revocation-worker",
		10,
		OutboxDeliverFunc(func(
			_ context.Context,
			_ *models.OutboxDelivery,
			event CloudEventEnvelope,
		) error {
			delivered = append(delivered, event.Type)
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("process archived access revocation: %v", err)
	}
	if batch.Claimed != 1 ||
		batch.Delivered != 1 ||
		len(delivered) != 1 ||
		delivered[0] != ProjectAccessRevokedEventType {
		t.Fatalf(
			"archived revocation batch=%+v delivered=%v",
			batch,
			delivered,
		)
	}
	var genericDelivery models.OutboxDelivery
	if err := db.Where(
		"event_id = ?",
		generic.ID,
	).Take(&genericDelivery).Error; err != nil {
		t.Fatal(err)
	}
	if genericDelivery.Status != models.OutboxDeliveryPending {
		t.Fatalf(
			"archived worker claimed generic event stream: %+v",
			genericDelivery,
		)
	}
	var revocationDelivery models.OutboxDelivery
	if err := db.Where(
		"event_id = ?",
		revocation.ID,
	).Take(&revocationDelivery).Error; err != nil {
		t.Fatal(err)
	}
	if revocationDelivery.Status != models.OutboxDeliverySucceeded {
		t.Fatalf(
			"archived worker did not deliver revocation: %+v",
			revocationDelivery,
		)
	}
}

func TestOutboxClaimRevalidatesArchiveAfterActiveProjectEnumeration(
	t *testing.T,
) {
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "outbox-archive-claim-barrier")
	if err := db.Model(&models.User{}).
		Where("id = ?", user.ID).
		Update(
			"platform_role",
			models.PlatformRolePlatformAdmin,
		).Error; err != nil {
		t.Fatal(err)
	}
	service := NewAgentNativeService(
		db,
		AgentNativeOptions{
			DefaultOutboxTargets: []OutboxTarget{{
				Type:        "event_stream",
				ID:          "default",
				MaxAttempts: 3,
			}},
		},
	)
	ctx := testProjectOperationContext(
		t,
		db,
		models.HumanActor(user.ID),
	)
	generic, err := service.createDomainEvent(
		t,
		ctx,
		DomainEventInput{
			Type:    "io.chronodesk.ticket.pre-archive.v1",
			Subject: "ticket/1",
			Actor:   models.HumanActor(user.ID),
			Data:    map[string]any{"ticket_id": 1},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	actor := models.SystemActor(outboxSystemActorID)
	projects, err := outboxWorkerProjects(
		context.Background(),
		db,
		actor,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("active project enumeration = %d, want 1", len(projects))
	}
	staleEnumeration := projects[0]

	var project models.Project
	if err := db.Where(
		"id = ? AND organization_id = ?",
		staleEnumeration.Scope.ProjectID,
		staleEnumeration.Scope.OrganizationID,
	).Take(&project).Error; err != nil {
		t.Fatal(err)
	}
	projectService, err := NewProjectService(db, service)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projectService.ArchiveProject(
		context.Background(),
		project.PublicID,
		models.HumanActor(user.ID),
	); err != nil {
		t.Fatalf("archive after active enumeration: %v", err)
	}

	traceID := "outbox-archive-claim-barrier"
	claimCtx, err := EnsureSystemProjectOperationContext(
		context.Background(),
		staleEnumeration.Scope,
		actor,
		traceID,
		traceID,
	)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.ClaimPendingOutbox(
		claimCtx,
		"outbox-archive-claim-barrier-worker",
		10,
		service.outboxLockTTL,
	)
	if err != nil {
		t.Fatalf("claim after archive commit: %v", err)
	}
	if len(claimed) != 1 ||
		claimed[0].Event == nil ||
		claimed[0].Event.Type != ProjectAccessRevokedEventType {
		t.Fatalf(
			"claim after active enumeration leaked archived work: %+v",
			claimed,
		)
	}
	var genericDelivery models.OutboxDelivery
	if err := db.Where(
		"event_id = ?",
		generic.ID,
	).Take(&genericDelivery).Error; err != nil {
		t.Fatal(err)
	}
	if genericDelivery.Status != models.OutboxDeliveryPending ||
		genericDelivery.Attempts != 0 ||
		genericDelivery.LockedBy != "" {
		t.Fatalf(
			"archived generic delivery was claimed after stale enumeration: %+v",
			genericDelivery,
		)
	}
}
