package services

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

type lifecycleRichDeliverer struct {
	result      OutboxAttemptResult
	mutate      func(*models.OutboxDelivery)
	richCalls   atomic.Int32
	legacyCalls atomic.Int32
}

func (deliverer *lifecycleRichDeliverer) Deliver(
	context.Context,
	*models.OutboxDelivery,
	CloudEventEnvelope,
) error {
	deliverer.legacyCalls.Add(1)
	return errors.New("legacy lifecycle result path must not run")
}

func (deliverer *lifecycleRichDeliverer) DeliverAttempt(
	_ context.Context,
	delivery *models.OutboxDelivery,
	_ CloudEventEnvelope,
) OutboxAttemptResult {
	deliverer.richCalls.Add(1)
	if deliverer.mutate != nil {
		deliverer.mutate(delivery)
	}
	return deliverer.result
}

type lifecycleDeadlineHandoffDeliverer struct {
	completedAt time.Time
}

func (deliverer lifecycleDeadlineHandoffDeliverer) Deliver(
	context.Context,
	*models.OutboxDelivery,
	CloudEventEnvelope,
) error {
	return nil
}

type lifecycleDeadlineKnownFailureDeliverer struct{}

func (lifecycleDeadlineKnownFailureDeliverer) Deliver(
	context.Context,
	*models.OutboxDelivery,
	CloudEventEnvelope,
) error {
	return nil
}

func (lifecycleDeadlineKnownFailureDeliverer) DeliverAttempt(
	ctx context.Context,
	_ *models.OutboxDelivery,
	_ CloudEventEnvelope,
) OutboxAttemptResult {
	<-ctx.Done()
	return OutboxKnownFailure(
		errors.New("receiver rejected before applying a side effect"),
	)
}

func (deliverer lifecycleDeadlineHandoffDeliverer) DeliverAttempt(
	ctx context.Context,
	_ *models.OutboxDelivery,
	_ CloudEventEnvelope,
) OutboxAttemptResult {
	<-ctx.Done()
	return OutboxKnownSuccess(deliverer.completedAt)
}

func seedLifecycleWorkerProject(
	t *testing.T,
	fixture *webhookOutboxLifecycleFixture,
	serial int,
) models.Project {
	t.Helper()
	var project models.Project
	if err := fixture.db.First(
		&project,
		"id = ? AND organization_id = ?",
		fixture.scope.ProjectID,
		fixture.scope.OrganizationID,
	).Error; err != nil {
		t.Fatal(err)
	}
	project.ID = 0
	project.PublicID = ""
	project.CreatedAt = time.Time{}
	project.UpdatedAt = time.Time{}
	project.Key = models.ProjectKey(fmt.Sprintf("LCP%03d", serial))
	project.Name = fmt.Sprintf("lifecycle worker project %d", serial)
	if err := fixture.db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	return project
}

func seedLifecycleNonWebhookDelivery(
	t *testing.T,
	fixture *webhookOutboxLifecycleFixture,
	project models.Project,
	serial int,
) models.OutboxDelivery {
	t.Helper()
	event := models.DomainEvent{
		ID: fmt.Sprintf(
			"72000000-0000-7000-8000-%012d",
			serial,
		),
		OrganizationID:  fixture.scope.OrganizationID,
		ProjectID:       project.ID,
		SpecVersion:     "1.0",
		Source:          "urn:chronodesk:test:lifecycle-worker",
		Type:            "io.chronodesk.test.lifecycle-worker.v1",
		Subject:         fmt.Sprintf("lifecycle-worker/%d", serial),
		Time:            fixture.clock.UTC(),
		DataContentType: "application/json",
		Data:            []byte(`{}`),
		ActorType:       models.ActorTypeSystem,
		ActorID:         outboxSystemActorID,
		ResourceVersion: 1,
	}
	if err := fixture.db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	delivery := models.OutboxDelivery{
		ID: fmt.Sprintf(
			"73000000-0000-7000-8000-%012d",
			serial,
		),
		OrganizationID:  fixture.scope.OrganizationID,
		ProjectID:       project.ID,
		EventID:         event.ID,
		DestinationType: "event_stream",
		DestinationID:   fmt.Sprintf("lifecycle-worker-%d", serial),
		Status:          models.OutboxDeliveryPending,
		MaxAttempts:     8,
		NextAttemptAt:   fixture.clock.Add(-time.Minute),
	}
	if err := fixture.db.Create(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	return delivery
}

func seedLifecycleWebhookConfig(
	t *testing.T,
	fixture *webhookOutboxLifecycleFixture,
	project models.Project,
	serial int,
) models.WebhookConfig {
	t.Helper()
	config := fixture.config
	config.ID = 0
	config.CreatedAt = time.Time{}
	config.UpdatedAt = time.Time{}
	config.DeletedAt = gorm.DeletedAt{}
	config.ProjectID = project.ID
	config.Name = fmt.Sprintf("lifecycle worker webhook %d", serial)
	config.LastTriggeredAt = nil
	config.LastSuccessAt = nil
	config.LastErrorAt = nil
	config.LastError = ""
	config.TotalSent = 0
	config.TotalSuccess = 0
	config.TotalFailed = 0
	if err := fixture.db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	return config
}

func seedLifecycleWebhookDelivery(
	t *testing.T,
	fixture *webhookOutboxLifecycleFixture,
	project models.Project,
	serial int,
) (models.OutboxDelivery, models.WebhookDeliverySnapshot) {
	t.Helper()
	seedLifecycleWebhookConfig(t, fixture, project, serial)
	scope := models.ProjectScope{
		OrganizationID: fixture.scope.OrganizationID,
		ProjectID:      project.ID,
	}
	actor := models.SystemActor("webhook-lifecycle-producer")
	event, err := fixture.service.createDomainEvent(
		t,
		contextWithProjectScope(t, scope, actor),
		DomainEventInput{
			Type:            "io.chronodesk.ticket.created.v1",
			Subject:         fmt.Sprintf("ticket/lifecycle-worker-%d", serial),
			Actor:           actor,
			ResourceVersion: 1,
			Data:            map[string]any{"serial": serial},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	var delivery models.OutboxDelivery
	if err := fixture.db.Where(
		"event_id = ? AND destination_type = ?",
		event.ID,
		"webhook",
	).Take(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	var snapshot models.WebhookDeliverySnapshot
	if err := fixture.db.Where(
		"event_id = ? AND project_id = ?",
		event.ID,
		project.ID,
	).Take(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	return delivery, snapshot
}

type lifecycleLegacySucceededPair struct {
	delivery models.OutboxDelivery
	snapshot models.WebhookDeliverySnapshot
}

func seedLifecycleLegacySucceededPairs(
	t *testing.T,
	fixture *webhookOutboxLifecycleFixture,
	project models.Project,
	serialStart int,
	count int,
) []lifecycleLegacySucceededPair {
	t.Helper()
	config := seedLifecycleWebhookConfig(
		t,
		fixture,
		project,
		serialStart,
	)
	pairs := make([]lifecycleLegacySucceededPair, 0, count)
	for offset := 0; offset < count; offset++ {
		serial := serialStart + offset
		event := models.DomainEvent{
			ID: fmt.Sprintf(
				"74000000-0000-7000-8000-%012d",
				serial,
			),
			OrganizationID:  fixture.scope.OrganizationID,
			ProjectID:       project.ID,
			SpecVersion:     "1.0",
			Source:          "urn:chronodesk:test:legacy-cleanup-budget",
			Type:            "io.chronodesk.test.legacy-cleanup-budget.v1",
			Subject:         fmt.Sprintf("legacy-cleanup-budget/%d", serial),
			Time:            fixture.clock.UTC(),
			DataContentType: "application/json",
			Data:            []byte(`{}`),
			ActorType:       models.ActorTypeSystem,
			ActorID:         outboxSystemActorID,
			ResourceVersion: 1,
		}
		if err := fixture.db.Create(&event).Error; err != nil {
			t.Fatal(err)
		}
		snapshot, err := models.NewWebhookDeliverySnapshot(
			config,
			event.ID,
			fixture.clock.Add(24*time.Hour),
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := fixture.db.Create(snapshot).Error; err != nil {
			t.Fatal(err)
		}
		destinationID, err :=
			models.WebhookDeliverySnapshotDestinationID(snapshot.ID)
		if err != nil {
			t.Fatal(err)
		}
		deliveredAt := fixture.clock.Add(-time.Minute)
		expiresAt := snapshot.CredentialExpiresAt
		delivery := models.OutboxDelivery{
			ID: fmt.Sprintf(
				"75000000-0000-7000-8000-%012d",
				serial,
			),
			OrganizationID:  fixture.scope.OrganizationID,
			ProjectID:       project.ID,
			EventID:         event.ID,
			DestinationType: "webhook",
			DestinationID:   destinationID,
			Status:          models.OutboxDeliverySucceeded,
			Attempts:        1,
			MaxAttempts:     8,
			NextAttemptAt:   fixture.clock.Add(-time.Minute),
			DeliveredAt:     &deliveredAt,
			ExpiresAt:       &expiresAt,
		}
		if err := fixture.db.Create(&delivery).Error; err != nil {
			t.Fatal(err)
		}
		pairs = append(pairs, lifecycleLegacySucceededPair{
			delivery: delivery,
			snapshot: *snapshot,
		})
	}
	return pairs
}

func shredLifecycleLegacySnapshotSucceeded(
	t *testing.T,
	fixture *webhookOutboxLifecycleFixture,
	snapshotID string,
) {
	t.Helper()
	shreddedAt := fixture.clock.Add(-time.Second)
	reason := models.WebhookCredentialShredReasonSucceeded
	result := fixture.db.Session(&gorm.Session{SkipHooks: true}).
		Table((models.WebhookDeliverySnapshot{}).TableName()).
		Where(
			"id = ? AND organization_id = ?",
			snapshotID,
			fixture.scope.OrganizationID,
		).
		Updates(map[string]any{
			"secret":                     "",
			"previous_secret":            "",
			"previous_secret_expires_at": nil,
			"access_token":               "",
			"credential_shredded_at":     shreddedAt,
			"credential_shred_reason":    reason,
		})
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	if result.RowsAffected != 1 {
		t.Fatalf(
			"shred legacy snapshot %s rows = %d",
			snapshotID,
			result.RowsAffected,
		)
	}
}

func registerLifecycleProjectClaimError(
	t *testing.T,
	fixture *webhookOutboxLifecycleFixture,
	projectID uint,
	injected error,
	beforeError func(),
) {
	t.Helper()
	callbackName := fmt.Sprintf(
		"test:lifecycle_project_claim_error:%d",
		projectID,
	)
	if err := fixture.db.Callback().Query().
		Before("gorm:query").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement == nil ||
				tx.Statement.Table !=
					(models.OutboxDelivery{}).TableName() {
				return
			}
			operation, operationErr := OperationContextFromContext(
				tx.Statement.Context,
			)
			if operationErr != nil ||
				operation.Scope.ProjectID != projectID {
				return
			}
			if beforeError != nil {
				beforeError()
			}
			_ = tx.AddError(injected)
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = fixture.db.Callback().Query().Remove(callbackName)
	})
}
