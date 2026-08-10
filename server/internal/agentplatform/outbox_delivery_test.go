package agentplatform

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"github.com/seaworld008/chronodesk/server/internal/security"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var _ services.OutboxAttemptDeliverer = (*NativeOutboxDeliverer)(nil)

const agentplatformCustomWebhookTestSecret = "agentplatform-custom-webhook-test-secret"

type recordingSLAEscalationConsumer struct {
	calls int
	event services.CloudEventEnvelope
}

type deadlineThenNilAttachmentUploadConsumer struct{}

func (deadlineThenNilAttachmentUploadConsumer) ExecuteAttachmentUploadOutbox(
	ctx context.Context,
	_ uint,
) error {
	<-ctx.Done()
	return nil
}

func (deadlineThenNilAttachmentUploadConsumer) ExecuteAttachmentStagingCleanupOutbox(
	context.Context,
	uint,
) error {
	return nil
}

type recordingWebSocketAccessRevoker struct {
	membershipScope models.ProjectScope
	membershipUser  uint
	user            uint
	projectScope    models.ProjectScope
}

type recordingAttachmentOutboxConsumer struct {
	uploaded uint
	cleaned  uint
}

func (consumer *recordingAttachmentOutboxConsumer) ExecuteAttachmentUploadOutbox(
	_ context.Context,
	attachmentID uint,
) error {
	consumer.uploaded = attachmentID
	return nil
}

func (consumer *recordingAttachmentOutboxConsumer) ExecuteAttachmentStagingCleanupOutbox(
	_ context.Context,
	attachmentID uint,
) error {
	consumer.cleaned = attachmentID
	return nil
}

func (revoker *recordingWebSocketAccessRevoker) RevokeProjectMembership(
	scope models.ProjectScope,
	userID uint,
) error {
	revoker.membershipScope = scope
	revoker.membershipUser = userID
	return nil
}

func (revoker *recordingWebSocketAccessRevoker) RevokeUser(
	userID uint,
) error {
	revoker.user = userID
	return nil
}

func (revoker *recordingWebSocketAccessRevoker) RevokeProject(
	scope models.ProjectScope,
) error {
	revoker.projectScope = scope
	return nil
}

func TestEventStreamOutboxDispatchesCommittedWebSocketAccessRevocations(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open("file:websocket-access-revocation-outbox?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	scope := models.ProjectScope{OrganizationID: 7, ProjectID: 70}
	tests := []struct {
		name      string
		eventType string
		data      string
		assert    func(*testing.T, *recordingWebSocketAccessRevoker)
	}{
		{
			name:      "membership",
			eventType: services.ProjectMembershipDeactivatedEventType,
			data:      `{"user_id":101}`,
			assert: func(
				t *testing.T,
				revoker *recordingWebSocketAccessRevoker,
			) {
				t.Helper()
				if revoker.membershipScope != scope ||
					revoker.membershipUser != 101 {
					t.Fatalf(
						"membership revocation = scope %+v user %d",
						revoker.membershipScope,
						revoker.membershipUser,
					)
				}
			},
		},
		{
			name:      "user",
			eventType: services.UserAccessRevokedEventType,
			data:      `{"user_id":202}`,
			assert: func(
				t *testing.T,
				revoker *recordingWebSocketAccessRevoker,
			) {
				t.Helper()
				if revoker.user != 202 {
					t.Fatalf("user revocation = %d, want 202", revoker.user)
				}
			},
		},
		{
			name:      "project",
			eventType: services.ProjectAccessRevokedEventType,
			data:      `{}`,
			assert: func(
				t *testing.T,
				revoker *recordingWebSocketAccessRevoker,
			) {
				t.Helper()
				if revoker.projectScope != scope {
					t.Fatalf(
						"project revocation = %+v, want %+v",
						revoker.projectScope,
						scope,
					)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			revoker := &recordingWebSocketAccessRevoker{}
			deliverer, err := NewNativeOutboxDeliverer(
				NativeOutboxDelivererOptions{
					DB:                db,
					AccessRevocations: revoker,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			delivery := &models.OutboxDelivery{
				ID:              "access-revocation-" + test.name,
				OrganizationID:  scope.OrganizationID,
				ProjectID:       scope.ProjectID,
				EventID:         "access-event-" + test.name,
				DestinationType: "event_stream",
				DestinationID:   "access-revocation",
			}
			event := services.CloudEventEnvelope{
				ID:             delivery.EventID,
				Type:           test.eventType,
				OrganizationID: scope.OrganizationID,
				ProjectID:      scope.ProjectID,
				Data:           []byte(test.data),
			}
			if err := deliverer.Deliver(
				agentplatformTestOutboxWorkerContext(t, scope),
				delivery,
				event,
			); err != nil {
				t.Fatalf("deliver committed revocation: %v", err)
			}
			test.assert(t, revoker)
		})
	}

	deliverer, err := NewNativeOutboxDeliverer(
		NativeOutboxDelivererOptions{DB: db},
	)
	if err != nil {
		t.Fatal(err)
	}
	delivery := &models.OutboxDelivery{
		ID:              "missing-access-revoker",
		OrganizationID:  scope.OrganizationID,
		ProjectID:       scope.ProjectID,
		EventID:         "missing-access-revoker-event",
		DestinationType: "event_stream",
		DestinationID:   "access-revocation",
	}
	event := services.CloudEventEnvelope{
		ID:             delivery.EventID,
		Type:           services.UserAccessRevokedEventType,
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		Data:           []byte(`{"user_id":303}`),
	}
	if err := deliverer.Deliver(
		agentplatformTestOutboxWorkerContext(t, scope),
		delivery,
		event,
	); err == nil || !strings.Contains(err.Error(), "consumer is unavailable") {
		t.Fatalf("missing access-revocation consumer error = %v", err)
	}
}

func TestProjectArchiveOutboxWorkerRevokesWebSocketProjectAccess(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open("file:project-archive-websocket-outbox?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	closeAgentplatformTestDB(t, db)
	if err := db.AutoMigrate(
		&models.User{},
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.WebhookDeliverySnapshot{},
		&models.AuditChainHead{},
		&models.AuditLedgerEntry{},
	); err != nil {
		t.Fatal(err)
	}
	organization := models.Organization{
		Slug:   "archive-ws",
		Name:   "Archive WebSocket",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "ARCHIVE",
		Name:           "Archive",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	project := models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            "ARCHIVE",
		Name:           "Archive",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:     "archive-ws-admin",
		Email:        "archive-ws-admin@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	ledger, err := services.NewAuditLedgerService(db)
	if err != nil {
		t.Fatal(err)
	}
	native := services.NewAgentNativeService(
		db,
		services.AgentNativeOptions{AuditLedger: ledger},
	)
	projectService, err := services.NewProjectService(db, native)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projectService.ArchiveProject(
		context.Background(),
		project.PublicID,
		models.HumanActor(user.ID),
	); err != nil {
		t.Fatalf("archive project: %v", err)
	}
	revoker := &recordingWebSocketAccessRevoker{}
	deliverer, err := NewNativeOutboxDeliverer(
		NativeOutboxDelivererOptions{
			DB:                db,
			AccessRevocations: revoker,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := native.ProcessOutboxBatch(
		context.Background(),
		"project-archive-websocket-worker",
		10,
		deliverer,
	)
	if err != nil {
		t.Fatalf("process project archive Outbox: %v", err)
	}
	if batch.Claimed != 1 ||
		batch.Delivered != 1 ||
		batch.Failed != 0 {
		t.Fatalf("project archive Outbox batch = %+v", batch)
	}
	if revoker.projectScope != project.Scope() {
		t.Fatalf(
			"project revocation scope = %+v, want %+v",
			revoker.projectScope,
			project.Scope(),
		)
	}
}

func TestAttachmentStagingCleanupOutboxUsesDedicatedConsumerMethod(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open("file:attachment-staging-cleanup-routing?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	consumer := &recordingAttachmentOutboxConsumer{}
	deliverer, err := NewNativeOutboxDeliverer(
		NativeOutboxDelivererOptions{
			DB:                db,
			AttachmentUploads: consumer,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	scope := models.ProjectScope{OrganizationID: 7, ProjectID: 70}
	delivery := &models.OutboxDelivery{
		ID:              "attachment-staging-cleanup",
		OrganizationID:  scope.OrganizationID,
		ProjectID:       scope.ProjectID,
		EventID:         "attachment-staging-cleanup-event",
		DestinationType: services.AttachmentStagingCleanupOutboxDestination,
		DestinationID:   "42",
	}
	event := services.CloudEventEnvelope{
		ID:             delivery.EventID,
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
	}
	if err := deliverer.Deliver(
		agentplatformTestOutboxWorkerContext(t, scope),
		delivery,
		event,
	); err != nil {
		t.Fatalf("deliver attachment staging cleanup: %v", err)
	}
	if consumer.cleaned != 42 || consumer.uploaded != 0 {
		t.Fatalf(
			"attachment consumer calls = cleanup %d upload %d",
			consumer.cleaned,
			consumer.uploaded,
		)
	}

	delivery.DestinationID = "0"
	if err := deliverer.Deliver(
		agentplatformTestOutboxWorkerContext(t, scope),
		delivery,
		event,
	); err == nil || !strings.Contains(err.Error(), "destination is invalid") {
		t.Fatalf("invalid staging cleanup destination error = %v", err)
	}
}

func TestNativeOutboxDelivererRequiresTrustedMatchingWorkerBoundary(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open("file:outbox-trusted-boundary?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal("get trusted boundary SQLite pool")
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	deliverer, err := NewNativeOutboxDeliverer(
		NativeOutboxDelivererOptions{DB: db},
	)
	if err != nil {
		t.Fatal(err)
	}
	scope := models.ProjectScope{OrganizationID: 1, ProjectID: 2}
	delivery := &models.OutboxDelivery{
		ID:              "trusted-boundary-delivery",
		OrganizationID:  scope.OrganizationID,
		ProjectID:       scope.ProjectID,
		EventID:         "trusted-boundary-event",
		DestinationType: "event_stream",
		DestinationID:   "default",
	}
	event := services.CloudEventEnvelope{
		ID:             delivery.EventID,
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
	}
	if err := deliverer.Deliver(
		context.Background(),
		delivery,
		event,
	); err == nil || !strings.Contains(err.Error(), "trusted worker context") {
		t.Fatalf("missing worker context error = %v", err)
	}
	if err := deliverer.Deliver(
		agentplatformTestOutboxWorkerContext(
			t,
			models.ProjectScope{OrganizationID: 1, ProjectID: 3},
		),
		delivery,
		event,
	); err == nil || !strings.Contains(err.Error(), "project scope mismatch") {
		t.Fatalf("mismatched worker scope error = %v", err)
	}
	mismatchedEvent := event
	mismatchedEvent.ID = "different-event"
	if err := deliverer.Deliver(
		agentplatformTestOutboxWorkerContext(t, scope),
		delivery,
		mismatchedEvent,
	); err == nil || !strings.Contains(err.Error(), "event reference mismatch") {
		t.Fatalf("mismatched event reference error = %v", err)
	}
	workerCtx := agentplatformTestOutboxWorkerContext(t, scope)
	err = scopeddb.WithProjectScopeContextTransaction(
		workerCtx,
		db,
		scope,
		func(transactionContext context.Context) error {
			return deliverer.Deliver(transactionContext, delivery, event)
		},
	)
	if err == nil ||
		!strings.Contains(err.Error(), "cannot run inside a database transaction") {
		t.Fatalf("transactional side-effect error = %v", err)
	}
}

type failOnceAttachmentStorage struct {
	services.AttachmentStorage
	attempts atomic.Int32
}

func (s *failOnceAttachmentStorage) Delete(ctx context.Context, key string) error {
	if s.attempts.Add(1) == 1 {
		return errors.New("temporary attachment storage failure")
	}
	return s.AttachmentStorage.Delete(ctx, key)
}

func (c *recordingSLAEscalationConsumer) ExecuteDomainEvent(
	_ context.Context,
	event services.CloudEventEnvelope,
) error {
	c.calls++
	c.event = event
	return nil
}

func TestSLAEscalationOutboxDeliveryUsesInjectedConsumer(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:sla_outbox_routing?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	deliverer, err := NewNativeOutboxDeliverer(NativeOutboxDelivererOptions{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	delivery := &models.OutboxDelivery{
		ID:              "sla-delivery",
		OrganizationID:  1,
		ProjectID:       1,
		EventID:         "sla-event",
		DestinationType: services.SLAEscalationOutboxDestination,
		DestinationID:   "breach",
	}
	event := services.CloudEventEnvelope{
		ID:             "sla-event",
		Type:           services.SLABreachEventType,
		OrganizationID: 1,
		ProjectID:      1,
	}
	workerCtx := agentplatformTestOutboxWorkerContext(
		t,
		models.ProjectScope{OrganizationID: 1, ProjectID: 1},
	)
	if err := deliverer.Deliver(workerCtx, delivery, event); err == nil {
		t.Fatal("unconfigured SLA continuation was acknowledged")
	}
	consumer := &recordingSLAEscalationConsumer{}
	deliverer, err = NewNativeOutboxDeliverer(NativeOutboxDelivererOptions{
		DB:            db,
		SLAEscalation: consumer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := deliverer.Deliver(workerCtx, delivery, event); err != nil {
		t.Fatalf("deliver SLA continuation: %v", err)
	}
	if consumer.calls != 1 || consumer.event.ID != event.ID {
		t.Fatalf("SLA continuation was not routed intact: %+v", consumer)
	}
}

func TestNativeOutboxDelivererDoesNotAcknowledgeLateNilNonWebhookResult(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open("file:late_nil_non_webhook_attempt?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal("get late nil non-webhook SQLite pool")
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	deliverer, err := NewNativeOutboxDeliverer(
		NativeOutboxDelivererOptions{
			DB:                db,
			AttachmentUploads: deadlineThenNilAttachmentUploadConsumer{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	scope := models.ProjectScope{OrganizationID: 1, ProjectID: 1}
	delivery := &models.OutboxDelivery{
		ID:              "late-nil-non-webhook",
		OrganizationID:  scope.OrganizationID,
		ProjectID:       scope.ProjectID,
		EventID:         "late-nil-event",
		DestinationType: services.AttachmentUploadOutboxDestination,
		DestinationID:   "1",
	}
	event := services.CloudEventEnvelope{
		ID:             delivery.EventID,
		Type:           "io.chronodesk.attachment.upload.requested.v1",
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
	}
	workerCtx, cancel := context.WithTimeout(
		agentplatformTestOutboxWorkerContext(t, scope),
		10*time.Millisecond,
	)
	defer cancel()

	result := deliverer.DeliverAttempt(workerCtx, delivery, event)
	if result.Kind != services.OutboxAttemptUncertain {
		t.Fatalf(
			"late nil result kind = %q error = %v, want uncertain",
			result.Kind,
			result.Err,
		)
	}
}

func TestAttachmentCleanupOutboxRetriesAfterCommitAndIsIdempotent(t *testing.T) {
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(
		&models.User{},
		&models.Ticket{},
		&models.Notification{},
		&models.TicketHistory{},
		&models.TicketComment{},
		&models.TicketAttachment{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.WebhookDeliverySnapshot{},
	); err != nil {
		t.Fatalf("migrate attachment cleanup schema: %v", err)
	}
	user := models.User{
		Username: "cleanup-owner", Email: "cleanup-owner@example.com",
		PasswordHash: "hash", PlatformRole: models.PlatformRolePlatformAdmin, Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	projectFixture := ensureAPIHandlerTestProject(t, db)
	ticket := models.Ticket{
		OrganizationID: projectFixture.organization.ID,
		ProjectID:      projectFixture.project.ID,
		QueueID:        projectFixture.queue.ID,
		TicketNumber:   "CLEANUP-1", Title: "Cleanup",
		Description: "attachment", Priority: models.TicketPriorityNormal,
		Status: models.TicketStatusOpen, Type: models.TicketTypeRequest,
		Source: models.TicketSourceWeb, CreatedByID: &user.ID, Version: 1,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	local, err := services.NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stored, err := local.Put(
		context.Background(),
		"tickets/cleanup/retry.txt",
		strings.NewReader("retryable cleanup"),
		1024,
	)
	if err != nil {
		t.Fatal(err)
	}
	attachment := models.TicketAttachment{
		TicketID: ticket.ID, UploadedBy: &user.ID,
		FileName: "retry.txt", OriginalName: "retry.txt",
		FileSize: stored.Size, StoragePath: stored.Key, Hash: stored.SHA256,
	}
	if err := db.Create(&attachment).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 29, 6, 0, 0, 0, time.UTC)
	native := services.NewAgentNativeService(db, services.AgentNativeOptions{
		Now:               func() time.Time { return now },
		AttachmentStorage: local,
		AttachmentStaging: local,
		DefaultOutboxTargets: []services.OutboxTarget{{
			Type: "event_stream", ID: "default", MaxAttempts: 8,
		}},
	})
	ticketService, err := services.NewTicketService(db, native, nil, 0)
	if err != nil {
		t.Fatalf("NewTicketService() error = %v", err)
	}
	operationContext, err := services.WithOperationContext(
		context.Background(),
		services.OperationContext{
			Scope:  projectFixture.project.Scope(),
			Actor:  models.HumanActor(user.ID),
			Source: services.SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatalf("create cleanup operation context: %v", err)
	}
	if err := ticketService.DeleteTicketExpectedVersion(
		operationContext,
		ticket.ID,
		user.ID,
		"admin",
		ticket.Version,
	); err != nil {
		t.Fatalf("delete ticket transaction: %v", err)
	}
	// The database transaction must not call external storage.
	reader, err := local.Open(context.Background(), stored.Key)
	if err != nil {
		t.Fatalf("file was removed before Outbox delivery: %v", err)
	}
	_ = reader.Close()
	var event models.DomainEvent
	if err := db.First(
		&event,
		"type = ? AND subject = ?",
		"io.chronodesk.ticket.deleted.v1",
		fmt.Sprintf("ticket/%d", ticket.ID),
	).Error; err != nil {
		t.Fatalf("load committed ticket deletion event: %v", err)
	}
	flaky := &failOnceAttachmentStorage{AttachmentStorage: local}
	deliverer, err := NewNativeOutboxDeliverer(NativeOutboxDelivererOptions{
		DB:                db,
		AttachmentStorage: flaky,
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := native.ProcessOutboxBatch(
		context.Background(),
		"attachment-cleanup-first",
		10,
		deliverer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Claimed != 2 || first.Failed != 1 || first.Delivered != 1 {
		t.Fatalf("first cleanup attempt was not retained for retry: %+v", first)
	}
	reader, err = local.Open(context.Background(), stored.Key)
	if err != nil {
		t.Fatalf("failed cleanup attempt removed the object: %v", err)
	}
	_ = reader.Close()

	now = now.Add(3 * time.Second)
	second, err := native.ProcessOutboxBatch(
		context.Background(),
		"attachment-cleanup-retry",
		10,
		deliverer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.Delivered != 1 || flaky.attempts.Load() != 2 {
		t.Fatalf("cleanup retry did not succeed: result=%+v attempts=%d", second, flaky.attempts.Load())
	}
	if reader, err := local.Open(context.Background(), stored.Key); err == nil {
		_ = reader.Close()
		t.Fatal("attachment object still exists after successful Outbox delivery")
	}

	var delivery models.OutboxDelivery
	if err := db.First(
		&delivery,
		"event_id = ? AND destination_type = ?",
		event.ID,
		services.AttachmentCleanupOutboxDestination,
	).Error; err != nil {
		t.Fatal(err)
	}
	if delivery.Status != models.OutboxDeliverySucceeded || delivery.Attempts != 2 {
		t.Fatalf("cleanup delivery state = %+v", delivery)
	}
	// A crash after object deletion but before acknowledgement may invoke the
	// same delivery again. AttachmentStorage.Delete must make that safe.
	if err := deliverer.Deliver(
		agentplatformTestOutboxWorkerContext(
			t,
			models.ProjectScope{
				OrganizationID: delivery.OrganizationID,
				ProjectID:      delivery.ProjectID,
			},
		),
		&delivery,
		services.CloudEventFromModel(&event),
	); err != nil {
		t.Fatalf("duplicate cleanup delivery is not idempotent: %v", err)
	}
}

func TestAttachmentCleanupOutboxRejectsPathTraversal(t *testing.T) {
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(
		&models.User{},
		&models.Ticket{},
		&models.TicketAttachment{},
	); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username: "traversal-owner", Email: "traversal-owner@example.com",
		PasswordHash: "hash", PlatformRole: models.PlatformRolePlatformAdmin, Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	scope := installAgentplatformTestProjectScope(t, db)
	ticket := models.Ticket{
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		TicketNumber:   "CLEANUP-TRAVERSAL", Title: "Traversal",
		Description: "must reject", Priority: models.TicketPriorityNormal,
		Status: models.TicketStatusOpen, Type: models.TicketTypeRequest,
		Source: models.TicketSourceWeb, CreatedByID: &user.ID, Version: 1,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	attachment := models.TicketAttachment{
		TicketID: ticket.ID, UploadedBy: &user.ID,
		FileName: "escape.txt", OriginalName: "escape.txt", FileSize: 1,
		StoragePath: "../escape.txt", StorageUrl: "http://127.0.0.1/private",
	}
	if err := db.Create(&attachment).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&attachment).Error; err != nil {
		t.Fatal(err)
	}
	target, err := services.NewAttachmentCleanupOutboxTarget(
		attachment.ID,
		attachment.StoragePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(target.ID, attachment.StoragePath) ||
		strings.Contains(target.ID, attachment.StorageUrl) {
		t.Fatal("cleanup destination exposed an internal path or provider URL")
	}
	local, err := services.NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	deliverer, err := NewNativeOutboxDeliverer(NativeOutboxDelivererOptions{
		DB:                db,
		AttachmentStorage: local,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{
		"ticket_id": ticket.ID,
		"deleted":   true,
		services.AttachmentCleanupObjectsDataField: []services.AttachmentCleanupObject{{
			AttachmentID: attachment.ID,
			TicketID:     ticket.ID,
			StoragePath:  attachment.StoragePath,
		}},
	})
	eventID := "attachment-path-traversal-event"
	delivery := &models.OutboxDelivery{
		OrganizationID:  scope.OrganizationID,
		ProjectID:       scope.ProjectID,
		EventID:         eventID,
		DestinationType: services.AttachmentCleanupOutboxDestination,
		DestinationID:   target.ID,
	}
	event := services.CloudEventEnvelope{
		ID:             eventID,
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		Type:           "io.chronodesk.ticket.deleted.v1",
		Subject:        fmt.Sprintf("ticket/%d", ticket.ID),
		Data:           data,
	}
	err = deliverer.Deliver(
		agentplatformTestOutboxWorkerContext(t, scope),
		delivery,
		event,
	)
	if !errors.Is(err, services.ErrInvalidAttachmentName) {
		t.Fatalf("path traversal cleanup error = %v", err)
	}
}

func TestIsPublicCallbackIPRejectsSSRFNetworks(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{ip: "8.8.8.8", want: true},
		{ip: "2606:4700:4700::1111", want: true},
		{ip: "127.0.0.1", want: false},
		{ip: "10.0.0.1", want: false},
		{ip: "100.64.0.1", want: false},
		{ip: "169.254.169.254", want: false},
		{ip: "192.0.2.1", want: false},
		{ip: "::1", want: false},
		{ip: "fd00::1", want: false},
		{ip: "2001:db8::1", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			if got := security.IsPublicCallbackIP(net.ParseIP(tt.ip)); got != tt.want {
				t.Fatalf("security.IsPublicCallbackIP(%q) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestWebhookNotificationKeepsExactCloudEventIdentity(t *testing.T) {
	data, err := json.Marshal(map[string]any{"ticket_id": 42})
	if err != nil {
		t.Fatal(err)
	}
	event := services.CloudEventEnvelope{
		ID:      "event-1",
		Type:    "io.chronodesk.ticket.comment.created.v1",
		Subject: "ticket/42",
		Data:    data,
	}
	if got := ticketIDFromCloudEvent(event); got != 42 {
		t.Fatalf("ticketIDFromCloudEvent() = %d, want 42", got)
	}
	notification := notificationEventFromCloudEvent(event)
	if notification.Type != models.WebhookEventTicketComment {
		t.Fatalf("comment type = %q", notification.Type)
	}

	event.Type = "io.chronodesk.ticket.transitioned.v1"
	event.Data = json.RawMessage(`{"ticket_id":42,"new_status":"resolved"}`)
	notification = notificationEventFromCloudEvent(event)
	if notification.Type != models.WebhookEventTicketTransitioned ||
		notification.TransitionStatus != models.TicketStatusResolved {
		t.Fatalf("resolved transition notification = %+v", notification)
	}
	event.Data = json.RawMessage(`{"ticket_id":42,"new_status":"closed"}`)
	notification = notificationEventFromCloudEvent(event)
	if notification.Type != models.WebhookEventTicketTransitioned ||
		notification.TransitionStatus != models.TicketStatusClosed {
		t.Fatalf("closed transition notification = %+v", notification)
	}

	event.Type = "io.chronodesk.ticket.sla.breached.v1"
	notification = notificationEventFromCloudEvent(event)
	if notification.Type != models.WebhookEventTicketSLABreached {
		t.Fatalf("SLA type was downgraded to %q", notification.Type)
	}

	event.Type = "io.chronodesk.automation.notification.requested.v1"
	event.Data = json.RawMessage(`{
		"ticket_id": 42,
		"notification": {
			"title": "Escalation required",
			"content": "Ticket 42 needs an owner."
		}
	}`)
	notification = notificationEventFromCloudEvent(event)
	if notification.Type != models.WebhookEventAutomationNotification {
		t.Fatalf("automation notification type = %q", notification.Type)
	}
	if notification.Title != "Escalation required" ||
		notification.Description != "Ticket 42 needs an owner." {
		t.Fatalf("automation notification payload was ignored: %#v", notification)
	}
	if notification.Type != models.WebhookEventType(event.Type) {
		t.Fatalf("CloudEvent identity was rewritten: %q != %q", notification.Type, event.Type)
	}
	if !eventcontract.IsWebhookDeliveryEventType(string(notification.Type)) {
		t.Fatalf("current event type is not subscribable: %q", notification.Type)
	}
}

func TestCloudEventQueueRoutingIncludesOldAndNewQueue(t *testing.T) {
	data, _ := json.Marshal(map[string]any{
		"old_queue": "payments",
		"new_queue": "platform",
	})
	queues := queueNamesFromCloudEvent(services.CloudEventEnvelope{Data: data})
	if len(queues) != 2 || queues[0] != "payments" || queues[1] != "platform" {
		t.Fatalf("queue routes = %#v", queues)
	}
}

func TestOutboxWebhookDeliveryUsesOneBoundedAttemptWithoutLocalRetry(t *testing.T) {
	var attempts atomic.Int32
	requests := make(chan struct {
		CloudEventID  string
		IdempotencyID string
		ContentType   string
		Timestamp     string
		Signature     string
		Body          []byte
	}, 1)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		attempts.Add(1)
		body, _ := io.ReadAll(request.Body)
		requests <- struct {
			CloudEventID  string
			IdempotencyID string
			ContentType   string
			Timestamp     string
			Signature     string
			Body          []byte
		}{
			CloudEventID:  request.Header.Get("X-CloudEvents-ID"),
			IdempotencyID: request.Header.Get("Idempotency-Key"),
			ContentType:   request.Header.Get("Content-Type"),
			Timestamp:     request.Header.Get("X-ChronoDesk-Timestamp"),
			Signature:     request.Header.Get("X-ChronoDesk-Signature"),
			Body:          body,
		}
		http.Error(w, "temporary failure", http.StatusBadGateway)
	}))
	defer endpoint.Close()

	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlite handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(
		&models.User{},
		&models.WebhookConfig{},
		&models.WebhookDeliverySnapshot{},
		&models.WebhookLog{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatalf("migrate webhook schema: %v", err)
	}
	user := models.User{
		Username:     "outbox-owner",
		Email:        "outbox-owner@example.com",
		PasswordHash: "not-a-real-password",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create webhook owner: %v", err)
	}
	projectFixture := ensureAPIHandlerTestProject(t, db)
	projectScope := projectFixture.project.Scope()
	config := models.WebhookConfig{
		OrganizationID:   projectScope.OrganizationID,
		ProjectID:        projectScope.ProjectID,
		Name:             "outbox-single-attempt",
		Provider:         models.WebhookProviderCustom,
		WebhookURL:       endpoint.URL,
		Status:           models.WebhookStatusActive,
		EnabledEventsObj: []models.WebhookEventType{models.WebhookEventTicketCreated},
		RetryCount:       1,
		RetryInterval:    1,
		TimeoutSeconds:   30,
		CreatedBy:        user.ID,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatalf("create webhook config: %v", err)
	}
	notifications := newWebhookTestNotificationService(t, db)
	actor := models.SystemActor("webhook-outbox-test")
	eventModel, err := appendTestDomainEvent(
		context.Background(),
		services.NewAgentNativeService(db),
		services.DomainEventInput{
			Type:            "io.chronodesk.ticket.created.v1",
			Subject:         "ticket/42",
			Actor:           actor,
			ResourceVersion: 1,
			Scope:           projectScope,
			Data:            map[string]any{"ticket_id": 42},
		},
		[]services.OutboxTarget{{
			Type:        "webhook",
			ID:          "configured",
			MaxAttempts: 8,
		}},
	)
	if err != nil {
		t.Fatalf("commit webhook event and snapshot: %v", err)
	}
	deliverer, err := NewNativeOutboxDeliverer(NativeOutboxDelivererOptions{
		DB:            db,
		Notifications: notifications,
	})
	if err != nil {
		t.Fatalf("create outbox deliverer: %v", err)
	}
	event := services.CloudEventFromModel(eventModel)
	var delivery models.OutboxDelivery
	if err := db.Where(
		"event_id = ? AND destination_type = ?",
		event.ID,
		"webhook",
	).First(&delivery).Error; err != nil {
		t.Fatalf("load snapshot delivery: %v", err)
	}
	if !strings.HasPrefix(delivery.DestinationID, webhookSnapshotPrefix) {
		t.Fatalf("webhook delivery is not snapshot-bound: %+v", delivery)
	}
	claimWebhookDeliveryForAdapterTest(t, db, &delivery)

	started := time.Now()
	attemptContext, cancelAttempt := context.WithDeadline(
		agentplatformTestOutboxWorkerContext(t, projectScope),
		delivery.ExpiresAt.UTC(),
	)
	err = deliverer.Deliver(
		attemptContext,
		&delivery,
		event,
	)
	cancelAttempt()
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("non-2xx webhook attempt must fail for Outbox backoff")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("Outbox delivery made %d HTTP attempts, want exactly 1", got)
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("Outbox delivery slept inside worker for %s", elapsed)
	}
	request := <-requests
	if request.CloudEventID != event.ID || request.IdempotencyID != delivery.ID {
		t.Fatalf(
			"stable delivery headers cloud_event=%q idempotency=%q",
			request.CloudEventID,
			request.IdempotencyID,
		)
	}
	if request.ContentType != "application/cloudevents+json" {
		t.Fatalf("custom Webhook Content-Type = %q", request.ContentType)
	}
	timestamp, err := strconv.ParseInt(request.Timestamp, 10, 64)
	if err != nil {
		t.Fatalf("custom Webhook timestamp = %q: %v", request.Timestamp, err)
	}
	if timestamp < started.Add(-time.Second).Unix() ||
		timestamp > time.Now().Add(time.Second).Unix() {
		t.Fatalf("custom Webhook timestamp %d is outside the replay window", timestamp)
	}
	mac := hmac.New(sha256.New, []byte(agentplatformCustomWebhookTestSecret))
	_, _ = mac.Write([]byte(request.Timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(request.Body)
	wantSignature := "v1=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(request.Signature), []byte(wantSignature)) {
		t.Fatalf("custom Webhook signature = %q, want %q", request.Signature, wantSignature)
	}
	wantBody, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(request.Body, wantBody) {
		t.Fatalf("custom Webhook body = %s, want exact CloudEvent %s", request.Body, wantBody)
	}
	var requestBody services.CloudEventEnvelope
	if err := json.Unmarshal(request.Body, &requestBody); err != nil {
		t.Fatal(err)
	}
	if requestBody.ID != event.ID ||
		requestBody.Type != event.Type ||
		requestBody.SpecVersion != "1.0" ||
		requestBody.OrganizationID != projectScope.OrganizationID ||
		requestBody.ProjectID != projectScope.ProjectID {
		t.Fatalf("structured CloudEvent identity missing from webhook body: %#v", requestBody)
	}
	var requestData map[string]any
	if err := json.Unmarshal(requestBody.Data, &requestData); err != nil {
		t.Fatal(err)
	}
	if requestData["ticket_id"] != float64(42) {
		t.Fatalf("structured CloudEvent data missing ticket identity: %#v", requestData)
	}
	notifications.WaitForWebhookAttemptAudits()
	var logs []models.WebhookLog
	if err := db.Order("id ASC").Find(&logs).Error; err != nil {
		t.Fatalf("load webhook logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("Outbox delivery wrote %d attempt logs, want 1", len(logs))
	}
	if logs[0].Status != "failed" || logs[0].NextRetryAt != nil {
		t.Fatalf("Outbox attempt scheduled a competing legacy retry: %+v", logs[0])
	}
	if logs[0].OrganizationID != projectScope.OrganizationID ||
		logs[0].ProjectID != projectScope.ProjectID {
		t.Fatalf("Outbox attempt log lost project scope: %+v", logs[0])
	}
}

func TestCommittedWebhookDeliveriesUseImmutableSnapshots(t *testing.T) {
	type capturedRequest struct {
		Timestamp string
		Signature string
		Body      []byte
	}
	var firstAttempts atomic.Int32
	firstRequests := make(chan capturedRequest, 1)
	firstEndpoint := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, request *http.Request) {
			firstAttempts.Add(1)
			body, _ := io.ReadAll(request.Body)
			firstRequests <- capturedRequest{
				Timestamp: request.Header.Get("X-ChronoDesk-Timestamp"),
				Signature: request.Header.Get("X-ChronoDesk-Signature"),
				Body:      body,
			}
			w.WriteHeader(http.StatusNoContent)
		},
	))
	defer firstEndpoint.Close()
	var secondAttempts atomic.Int32
	secondEndpoint := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			secondAttempts.Add(1)
			w.WriteHeader(http.StatusNoContent)
		},
	))
	defer secondEndpoint.Close()
	var changedAttempts atomic.Int32
	changedEndpoint := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			changedAttempts.Add(1)
			w.WriteHeader(http.StatusNoContent)
		},
	))
	defer changedEndpoint.Close()
	var newAttempts atomic.Int32
	newEndpoint := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			newAttempts.Add(1)
			w.WriteHeader(http.StatusNoContent)
		},
	))
	defer newEndpoint.Close()

	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") +
		"?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(
		&models.User{},
		&models.WebhookConfig{},
		&models.WebhookDeliverySnapshot{},
		&models.WebhookLog{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:     "snapshot-owner",
		Email:        "snapshot-owner@example.test",
		PasswordHash: "not-a-real-password",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	projectFixture := ensureAPIHandlerTestProject(t, db)
	scope := projectFixture.project.Scope()
	configs := []models.WebhookConfig{
		{
			OrganizationID: scope.OrganizationID,
			ProjectID:      scope.ProjectID,
			Name:           "first-frozen",
			Provider:       models.WebhookProviderCustom,
			WebhookURL:     firstEndpoint.URL,
			Status:         models.WebhookStatusActive,
			EnabledEventsObj: []models.WebhookEventType{
				models.WebhookEventTicketCreated,
			},
			RetryCount: 1,
			CreatedBy:  user.ID,
		},
		{
			OrganizationID: scope.OrganizationID,
			ProjectID:      scope.ProjectID,
			Name:           "second-frozen",
			Provider:       models.WebhookProviderCustom,
			WebhookURL:     secondEndpoint.URL,
			Status:         models.WebhookStatusActive,
			EnabledEventsObj: []models.WebhookEventType{
				models.WebhookEventTicketCreated,
			},
			RetryCount: 4,
			CreatedBy:  user.ID,
		},
	}
	for index := range configs {
		if err := db.Create(&configs[index]).Error; err != nil {
			t.Fatal(err)
		}
	}
	notifications := newWebhookTestNotificationService(t, db)
	now := time.Now().UTC()
	native := services.NewAgentNativeService(
		db,
		services.AgentNativeOptions{Now: func() time.Time { return now }},
	)
	event, err := appendTestDomainEvent(
		context.Background(),
		native,
		services.DomainEventInput{
			Type:            "io.chronodesk.ticket.created.v1",
			Subject:         "ticket/42",
			Actor:           models.SystemActor("snapshot-test"),
			ResourceVersion: 1,
			Scope:           scope,
			Data:            map[string]any{"ticket_id": 42},
		},
		[]services.OutboxTarget{{
			Type:        "webhook",
			ID:          "configured",
			MaxAttempts: 8,
		}},
	)
	if err != nil {
		t.Fatalf("commit domain event: %v", err)
	}
	var snapshots []models.WebhookDeliverySnapshot
	if err := db.Where("event_id = ?", event.ID).
		Order("config_id ASC").
		Find(&snapshots).Error; err != nil {
		t.Fatal(err)
	}
	var deliveries []models.OutboxDelivery
	if err := db.Where(
		"event_id = ? AND destination_type = ?",
		event.ID,
		"webhook",
	).Order("destination_id ASC").Find(&deliveries).Error; err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 2 || len(deliveries) != 2 {
		t.Fatalf(
			"committed snapshots=%d deliveries=%d, want 2 each",
			len(snapshots),
			len(deliveries),
		)
	}
	snapshotByID := make(map[string]models.WebhookDeliverySnapshot, 2)
	for _, snapshot := range snapshots {
		snapshotByID[snapshot.ID] = snapshot
	}
	for _, delivery := range deliveries {
		snapshotID, err := parseWebhookSnapshotDestinationID(
			delivery.DestinationID,
		)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, exists := snapshotByID[snapshotID]
		if !exists ||
			delivery.MaxAttempts != snapshot.RetryCount+1 ||
			delivery.Status != models.OutboxDeliveryPending ||
			delivery.ExpiresAt == nil ||
			!delivery.ExpiresAt.Equal(snapshot.CredentialExpiresAt) ||
			!snapshot.CredentialExpiresAt.Equal(
				now.Add(models.WebhookDeliveryCredentialLifetime),
			) {
			t.Fatalf("invalid snapshot delivery: %+v", delivery)
		}
	}
	originalDeadlines := make(map[string]time.Time, len(snapshots))
	for _, snapshot := range snapshots {
		originalDeadlines[snapshot.ID] = snapshot.CredentialExpiresAt
	}

	protector := newAgentplatformWebhookTestProtector(t)
	rotatedSecret := "rotated-webhook-secret"
	rotatedEnvelope, err := security.ProtectOptional(
		protector,
		rotatedSecret,
		security.FieldAAD(
			"webhook_configs",
			strconv.FormatUint(uint64(configs[0].ID), 10),
			"secret",
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.WebhookConfig{}).
		Where("id = ?", configs[0].ID).
		UpdateColumns(map[string]any{
			"webhook_url":    changedEndpoint.URL,
			"secret":         rotatedEnvelope,
			"enabled_events": `["io.chronodesk.ticket.transitioned.v1"]`,
			"filter_rules":   `{"transition_statuses":["closed"]}`,
			"status":         models.WebhookStatusDisabled,
		}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&configs[1]).Error; err != nil {
		t.Fatal(err)
	}
	newConfig := models.WebhookConfig{
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		Name:           "created-after-event",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     newEndpoint.URL,
		Status:         models.WebhookStatusActive,
		EnabledEventsObj: []models.WebhookEventType{
			models.WebhookEventTicketCreated,
		},
		CreatedBy: user.ID,
	}
	if err := db.Create(&newConfig).Error; err != nil {
		t.Fatal(err)
	}

	deliverer, err := NewNativeOutboxDeliverer(
		NativeOutboxDelivererOptions{
			DB:            db,
			Notifications: notifications,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := native.ProcessOutboxBatch(
		context.Background(),
		"webhook-snapshot-worker",
		10,
		deliverer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Delivered != 2 || result.Failed != 0 ||
		firstAttempts.Load() != 1 ||
		secondAttempts.Load() != 1 ||
		changedAttempts.Load() != 0 ||
		newAttempts.Load() != 0 {
		t.Fatalf(
			"snapshot delivery result=%+v attempts=(old:%d,%d changed:%d new:%d)",
			result,
			firstAttempts.Load(),
			secondAttempts.Load(),
			changedAttempts.Load(),
			newAttempts.Load(),
		)
	}
	firstRequest := <-firstRequests
	mac := hmac.New(
		sha256.New,
		[]byte(agentplatformCustomWebhookTestSecret),
	)
	_, _ = mac.Write([]byte(firstRequest.Timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(firstRequest.Body)
	oldSignature := "v1=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal(
		[]byte(firstRequest.Signature),
		[]byte(oldSignature),
	) {
		t.Fatalf(
			"committed delivery did not use frozen secret: got %q want %q",
			firstRequest.Signature,
			oldSignature,
		)
	}
	var snapshotCount, newConfigSnapshots int64
	if err := db.Model(&models.WebhookDeliverySnapshot{}).
		Where("event_id = ?", event.ID).
		Count(&snapshotCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.WebhookDeliverySnapshot{}).
		Where("event_id = ? AND config_id = ?", event.ID, newConfig.ID).
		Count(&newConfigSnapshots).Error; err != nil {
		t.Fatal(err)
	}
	if snapshotCount != 2 || newConfigSnapshots != 0 {
		t.Fatalf(
			"historical event snapshots=%d new subscription snapshots=%d",
			snapshotCount,
			newConfigSnapshots,
		)
	}
	var retained models.WebhookDeliverySnapshot
	if err := db.First(
		&retained,
		"event_id = ? AND config_id = ?",
		event.ID,
		configs[0].ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if retained.WebhookURL != firstEndpoint.URL ||
		retained.EnabledEvents !=
			`["io.chronodesk.ticket.created.v1"]` ||
		!retained.CredentialExpiresAt.Equal(
			originalDeadlines[retained.ID],
		) {
		t.Fatalf("committed snapshot changed after config edit: %+v", retained)
	}
	var deletedConfigSnapshot models.WebhookDeliverySnapshot
	if err := db.First(
		&deletedConfigSnapshot,
		"event_id = ? AND config_id = ?",
		event.ID,
		configs[1].ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if !deletedConfigSnapshot.CredentialExpiresAt.Equal(
		originalDeadlines[deletedConfigSnapshot.ID],
	) {
		t.Fatalf(
			"committed snapshot deadline changed after config delete: %+v",
			deletedConfigSnapshot,
		)
	}
	otherProject := models.Project{
		OrganizationID: scope.OrganizationID,
		BusinessUnitID: projectFixture.project.BusinessUnitID,
		Key:            "OTHER",
		Name:           "Other",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&otherProject).Error; err != nil {
		t.Fatal(err)
	}
	foreignScope := otherProject.Scope()
	foreignDelivery := deliveries[0]
	foreignDelivery.ID = "foreign-project-snapshot-delivery"
	foreignDelivery.OrganizationID = foreignScope.OrganizationID
	foreignDelivery.ProjectID = foreignScope.ProjectID
	foreignEvent := services.CloudEventFromModel(event)
	foreignEvent.OrganizationID = foreignScope.OrganizationID
	foreignEvent.ProjectID = foreignScope.ProjectID
	if err := deliverer.Deliver(
		agentplatformTestOutboxWorkerContext(t, foreignScope),
		&foreignDelivery,
		foreignEvent,
	); err == nil ||
		!errors.Is(err, services.ErrWebhookOutboxAttemptRejected) {
		t.Fatalf("cross-project snapshot error = %v", err)
	}
	if firstAttempts.Load() != 1 ||
		secondAttempts.Load() != 1 ||
		changedAttempts.Load() != 0 ||
		newAttempts.Load() != 0 {
		t.Fatal("cross-project snapshot lookup performed an HTTP request")
	}
	var persistedEvent models.DomainEvent
	if err := db.First(&persistedEvent, "id = ?", event.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persistedEvent.PublishedAt == nil {
		t.Fatal("event was not published after snapshot deliveries completed")
	}
}

func newWebhookTestNotificationService(
	t testing.TB,
	db *gorm.DB,
) *services.NotificationService {
	t.Helper()
	protector := newAgentplatformWebhookTestProtector(t)
	var configs []models.WebhookConfig
	if err := db.Where("provider = ?", models.WebhookProviderCustom).
		Find(&configs).Error; err != nil {
		t.Fatal(err)
	}
	for index := range configs {
		config := &configs[index]
		if security.IsEnvelope(config.Secret) {
			continue
		}
		envelope, err := security.ProtectOptional(
			protector,
			agentplatformCustomWebhookTestSecret,
			security.FieldAAD(
				"webhook_configs",
				strconv.FormatUint(uint64(config.ID), 10),
				"secret",
			),
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&models.WebhookConfig{}).
			Where("id = ?", config.ID).
			UpdateColumn("secret", envelope).Error; err != nil {
			t.Fatal(err)
		}
	}
	return services.NewNotificationServiceWithClientFactory(
		db,
		protector,
		services.WebhookClientFactoryFunc(func(
			context.Context,
			*url.URL,
			time.Duration,
		) (*http.Client, error) {
			return http.DefaultClient, nil
		}),
	)
}

func claimWebhookDeliveryForAdapterTest(
	t testing.TB,
	db *gorm.DB,
	delivery *models.OutboxDelivery,
) {
	t.Helper()
	if db == nil || delivery == nil || delivery.ExpiresAt == nil {
		t.Fatal("webhook delivery claim fixture is incomplete")
	}
	lockedAt := time.Now().UTC()
	lockToken := "019feb4d-0000-7000-8000-000000000001"
	result := db.Model(&models.OutboxDelivery{}).
		Where("id = ?", delivery.ID).
		Updates(map[string]any{
			"status":     models.OutboxDeliveryProcessing,
			"attempts":   1,
			"locked_at":  lockedAt,
			"locked_by":  "adapter-webhook-test-worker",
			"lock_token": lockToken,
		})
	if result.Error != nil || result.RowsAffected != 1 {
		t.Fatalf("claim adapter webhook delivery: %v", result.Error)
	}
	if err := db.Take(delivery, "id = ?", delivery.ID).Error; err != nil {
		t.Fatalf("reload adapter webhook delivery claim: %v", err)
	}
}

func newAgentplatformWebhookTestProtector(
	t testing.TB,
) security.Protector {
	t.Helper()
	protector, err := security.NewKeyring(
		"agentplatform-webhook-test",
		map[string][]byte{
			"agentplatform-webhook-test": bytes.Repeat([]byte{0x59}, 32),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return protector
}
