package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

func TestWebhookOutboxClaimClassesRemainFairAtLimitOne(t *testing.T) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	pending := fixture.delivery
	failed, _, _ := fixture.createIntent(t, "claim-class-failed")
	processing, _, _ := fixture.createIntent(
		t,
		"claim-class-processing",
	)
	staleAt := now.Add(-2 * time.Minute)
	staleToken := "018f3f7e-7b22-7cc0-8000-000000000001"
	if err := fixture.db.Model(&models.OutboxDelivery{}).
		Where("id = ?", failed.ID).
		Update("status", models.OutboxDeliveryFailed).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.OutboxDelivery{}).
		Where("id = ?", processing.ID).
		Updates(map[string]any{
			"status":              models.OutboxDeliveryProcessing,
			"attempts":            1,
			"locked_at":           staleAt,
			"locked_by":           "stale-claim-class-worker",
			"lock_token":          staleToken,
			"dispatch_started_at": staleAt,
		}).Error; err != nil {
		t.Fatal(err)
	}
	want := map[string]struct{}{
		pending.ID:    {},
		failed.ID:     {},
		processing.ID: {},
	}
	got := make(map[string]struct{}, len(want))
	for attempt := 0; attempt < len(want); attempt++ {
		claimed, err := fixture.service.ClaimPendingOutbox(
			fixture.worker,
			fmt.Sprintf("fair-claim-worker-%d", attempt),
			1,
			time.Minute,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(claimed) != 1 {
			t.Fatalf(
				"fair claim attempt %d returned %d rows",
				attempt,
				len(claimed),
			)
		}
		got[claimed[0].ID] = struct{}{}
	}
	if len(got) != len(want) {
		t.Fatalf("claim classes were starved: got=%v want=%v", got, want)
	}
	for id := range want {
		if _, exists := got[id]; !exists {
			t.Fatalf("claim class delivery %s was starved", id)
		}
	}
}

func TestWebhookOutboxClaimClassCursorIsIndependentPerProject(
	t *testing.T,
) {
	service := &AgentNativeService{}
	scopes := []models.ProjectScope{
		{OrganizationID: 1, ProjectID: 11},
		{OrganizationID: 1, ProjectID: 12},
		{OrganizationID: 1, ProjectID: 13},
	}
	for round := 0; round < 4; round++ {
		for _, scope := range scopes {
			if got := service.nextOutboxClaimClassStart(
				scope,
				4,
			); got != round {
				t.Fatalf(
					"project %+v class round %d started at %d",
					scope,
					round,
					got,
				)
			}
		}
	}
}

func TestWebhookOutboxProjectCursorReachesEveryProjectPastHotBacklog(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	if err := fixture.db.Exec("DELETE FROM outbox_deliveries").Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Exec(
		"DELETE FROM webhook_delivery_snapshots",
	).Error; err != nil {
		t.Fatal(err)
	}
	var template models.Project
	if err := fixture.db.First(
		&template,
		"id = ? AND organization_id = ?",
		fixture.scope.ProjectID,
		fixture.scope.OrganizationID,
	).Error; err != nil {
		t.Fatal(err)
	}
	projects := make([]models.Project, 0, 16)
	projects = append(projects, template)
	for index := 1; index < 16; index++ {
		project := template
		project.ID = 0
		project.PublicID = ""
		project.CreatedAt = time.Time{}
		project.UpdatedAt = time.Time{}
		project.Key = models.ProjectKey(fmt.Sprintf("FAIR%d", index))
		project.Name = fmt.Sprintf("fair outbox project %d", index)
		if err := fixture.db.Create(&project).Error; err != nil {
			t.Fatal(err)
		}
		projects = append(projects, project)
	}
	total := 0
	for projectIndex := range projects {
		count := 1
		if projectIndex == 0 {
			count = 16
		}
		for deliveryIndex := 0; deliveryIndex < count; deliveryIndex++ {
			total++
			event := models.DomainEvent{
				ID: fmt.Sprintf(
					"70000000-0000-7000-8000-%012d",
					total,
				),
				OrganizationID:  fixture.scope.OrganizationID,
				ProjectID:       projects[projectIndex].ID,
				SpecVersion:     "1.0",
				Source:          "urn:chronodesk:test:project-fairness",
				Type:            "io.chronodesk.test.project-fairness.v1",
				Subject:         fmt.Sprintf("fairness/%d", total),
				Time:            now,
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
					"71000000-0000-7000-8000-%012d",
					total,
				),
				OrganizationID:  fixture.scope.OrganizationID,
				ProjectID:       projects[projectIndex].ID,
				EventID:         event.ID,
				DestinationType: "event_stream",
				DestinationID:   fmt.Sprintf("fairness-%d", total),
				Status:          models.OutboxDeliveryPending,
				MaxAttempts:     8,
				NextAttemptAt:   now.Add(-time.Minute),
			}
			if err := fixture.db.Create(&delivery).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	fixture.service.outboxDeliverySlots = make(chan struct{}, 8)
	var adapterCalls atomic.Int32
	deliverer := OutboxDeliverFunc(func(
		context.Context,
		*models.OutboxDelivery,
		CloudEventEnvelope,
	) error {
		adapterCalls.Add(1)
		return nil
	})
	aggregate := OutboxBatchResult{}
	for batchIndex := 0; batchIndex < len(projects)+1 &&
		aggregate.Delivered < total; batchIndex++ {
		batch, err := fixture.service.ProcessOutboxBatch(
			context.Background(),
			fmt.Sprintf("project-fairness-worker-%d", batchIndex),
			total,
			deliverer,
		)
		if err != nil {
			t.Fatal(err)
		}
		aggregate.Claimed += batch.Claimed
		aggregate.Delivered += batch.Delivered
		aggregate.Failed += batch.Failed
		aggregate.Dead += batch.Dead
		aggregate.Expired += batch.Expired
		aggregate.consumed += batch.consumed
	}
	if aggregate.Claimed != total ||
		aggregate.Delivered != total ||
		adapterCalls.Load() != int32(total) {
		t.Fatalf(
			"project fairness batch=%+v calls=%d total=%d",
			aggregate,
			adapterCalls.Load(),
			total,
		)
	}
	for projectIndex := 1; projectIndex < len(projects); projectIndex++ {
		var succeeded int64
		if err := fixture.db.Model(&models.OutboxDelivery{}).
			Where(
				"organization_id = ? AND project_id = ? AND status = ?",
				fixture.scope.OrganizationID,
				projects[projectIndex].ID,
				models.OutboxDeliverySucceeded,
			).Count(&succeeded).Error; err != nil {
			t.Fatal(err)
		}
		if succeeded != 1 {
			t.Fatalf(
				"project %d delivery count = %d, want 1",
				projects[projectIndex].ID,
				succeeded,
			)
		}
	}
}

func TestProcessOutboxBatchPreservesCommittedPartialClaimsAfterProjectError(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	firstDelivery := fixture.delivery
	staleDelivery, _, _ := fixture.createIntent(t, "stale-max")
	if err := fixture.db.Exec(
		`UPDATE outbox_deliveries
		 SET status = ?, attempts = max_attempts, locked_at = ?,
		     locked_by = ?, lock_token = ?, dispatch_started_at = ?
		 WHERE id = ?`,
		models.OutboxDeliveryProcessing,
		now.Add(-2*time.Minute),
		"stale-partial-worker",
		"0198a5d0-0000-7000-8000-000000000001",
		now.Add(-2*time.Minute),
		staleDelivery.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	errorProject := seedLifecycleWorkerProject(t, fixture, 1)
	laterProject := seedLifecycleWorkerProject(t, fixture, 2)
	laterDelivery := seedLifecycleNonWebhookDelivery(
		t,
		fixture,
		laterProject,
		1,
	)
	injected := errors.New("injected later project claim failure")
	registerLifecycleProjectClaimError(
		t,
		fixture,
		errorProject.ID,
		injected,
		nil,
	)
	fixture.service.outboxDeliverySlots = make(chan struct{}, 3)
	var adapterCalls atomic.Int32
	batch, err := fixture.service.ProcessOutboxBatch(
		context.Background(),
		"partial-claim-worker",
		3,
		OutboxDeliverFunc(func(
			context.Context,
			*models.OutboxDelivery,
			CloudEventEnvelope,
		) error {
			adapterCalls.Add(1)
			return nil
		}),
	)
	if !errors.Is(err, injected) {
		t.Fatalf("ProcessOutboxBatch() error = %v, want injected error", err)
	}
	if batch.Claimed != 2 ||
		batch.Delivered != 2 ||
		batch.Failed != 1 ||
		batch.Dead != 1 ||
		batch.Expired != 0 ||
		batch.consumed != 3 {
		t.Fatalf("partial claim batch = %+v, want 2/2/1/1/0 consumed=3", batch)
	}
	if adapterCalls.Load() != 2 {
		t.Fatalf("partial claim adapter calls = %d, want 2", adapterCalls.Load())
	}
	for _, expected := range []struct {
		id     string
		status models.OutboxDeliveryStatus
	}{
		{id: firstDelivery.ID, status: models.OutboxDeliverySucceeded},
		{id: staleDelivery.ID, status: models.OutboxDeliveryDead},
		{id: laterDelivery.ID, status: models.OutboxDeliverySucceeded},
	} {
		var delivery models.OutboxDelivery
		if err := fixture.db.First(&delivery, "id = ?", expected.id).Error; err != nil {
			t.Fatal(err)
		}
		if delivery.Status != expected.status {
			t.Fatalf(
				"delivery %s status = %s, want %s",
				expected.id,
				delivery.Status,
				expected.status,
			)
		}
	}
}

func TestProcessOutboxBatchContinuesAfterFirstProjectClaimError(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	laterProject := seedLifecycleWorkerProject(t, fixture, 3)
	laterDelivery := seedLifecycleNonWebhookDelivery(
		t,
		fixture,
		laterProject,
		2,
	)
	injected := errors.New("injected first project claim failure")
	registerLifecycleProjectClaimError(
		t,
		fixture,
		fixture.scope.ProjectID,
		injected,
		nil,
	)
	var adapterCalls atomic.Int32
	batch, err := fixture.service.ProcessOutboxBatch(
		context.Background(),
		"continue-after-claim-error-worker",
		2,
		OutboxDeliverFunc(func(
			context.Context,
			*models.OutboxDelivery,
			CloudEventEnvelope,
		) error {
			adapterCalls.Add(1)
			return nil
		}),
	)
	if !errors.Is(err, injected) {
		t.Fatalf("ProcessOutboxBatch() error = %v, want injected error", err)
	}
	if batch.Claimed != 1 ||
		batch.Delivered != 1 ||
		batch.Failed != 0 ||
		batch.Dead != 0 ||
		batch.Expired != 0 ||
		batch.consumed != 1 {
		t.Fatalf("continued claim batch = %+v, want 1/1/0/0/0 consumed=1", batch)
	}
	if adapterCalls.Load() != 1 {
		t.Fatalf("continued claim adapter calls = %d, want 1", adapterCalls.Load())
	}
	var delivery models.OutboxDelivery
	if err := fixture.db.First(
		&delivery,
		"id = ?",
		laterDelivery.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if delivery.Status != models.OutboxDeliverySucceeded {
		t.Fatalf(
			"later delivery status = %s, want succeeded",
			delivery.Status,
		)
	}
}

func TestProcessOutboxBatchStopsProjectClaimsOnContextCancellation(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	laterProject := seedLifecycleWorkerProject(t, fixture, 4)
	seedLifecycleNonWebhookDelivery(
		t,
		fixture,
		laterProject,
		3,
	)
	ctx, cancel := context.WithCancel(context.Background())
	injected := errors.New("injected cancellation during project claim")
	registerLifecycleProjectClaimError(
		t,
		fixture,
		fixture.scope.ProjectID,
		injected,
		cancel,
	)
	var (
		adapterCalls        atomic.Int32
		laterProjectQueries atomic.Int32
	)
	const queryCounterCallback = "test:count_later_project_after_cancel"
	if err := fixture.db.Callback().Query().
		Before("gorm:query").
		Register(queryCounterCallback, func(tx *gorm.DB) {
			if tx.Statement == nil ||
				tx.Statement.Table !=
					(models.OutboxDelivery{}).TableName() {
				return
			}
			operation, operationErr := OperationContextFromContext(
				tx.Statement.Context,
			)
			if operationErr == nil &&
				operation.Scope.ProjectID == laterProject.ID {
				laterProjectQueries.Add(1)
			}
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = fixture.db.Callback().Query().Remove(queryCounterCallback)
	})
	batch, err := fixture.service.ProcessOutboxBatch(
		ctx,
		"cancel-project-claim-worker",
		2,
		OutboxDeliverFunc(func(
			context.Context,
			*models.OutboxDelivery,
			CloudEventEnvelope,
		) error {
			adapterCalls.Add(1)
			return nil
		}),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ProcessOutboxBatch() error = %v, want context canceled", err)
	}
	if !errors.Is(err, injected) {
		t.Fatalf("ProcessOutboxBatch() error = %v, want injected error", err)
	}
	if batch.Claimed != 0 ||
		batch.Delivered != 0 ||
		batch.Failed != 0 ||
		batch.Dead != 0 ||
		batch.Expired != 0 ||
		batch.consumed != 0 {
		t.Fatalf("canceled claim batch mutated work: %+v", batch)
	}
	if adapterCalls.Load() != 0 {
		t.Fatalf("canceled claim adapter calls = %d, want 0", adapterCalls.Load())
	}
	if laterProjectQueries.Load() != 0 {
		t.Fatalf(
			"canceled claim queried later project %d times",
			laterProjectQueries.Load(),
		)
	}
}

func TestProcessOutboxBatchBoundsRawCandidatesAcrossProjects(
	t *testing.T,
) {
	t.Run("exhausted only", func(t *testing.T) {
		now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
		fixture := newWebhookOutboxLifecycleFixture(t, now)
		secondProject := seedLifecycleWorkerProject(t, fixture, 5)
		thirdProject := seedLifecycleWorkerProject(t, fixture, 6)
		secondDelivery, _ := seedLifecycleWebhookDelivery(
			t,
			fixture,
			secondProject,
			5,
		)
		thirdDelivery, _ := seedLifecycleWebhookDelivery(
			t,
			fixture,
			thirdProject,
			6,
		)
		deliveries := []models.OutboxDelivery{
			fixture.delivery,
			secondDelivery,
			thirdDelivery,
		}
		for index := range deliveries {
			if err := fixture.db.Model(&models.OutboxDelivery{}).
				Where("id = ?", deliveries[index].ID).
				Updates(map[string]any{
					"status":   models.OutboxDeliveryProcessing,
					"attempts": deliveries[index].MaxAttempts,
					"locked_at": now.Add(
						-2 * time.Minute,
					),
					"dispatch_started_at": now.Add(
						-2 * time.Minute,
					),
					"locked_by": fmt.Sprintf(
						"raw-budget-stale-%d",
						index,
					),
					"lock_token": fmt.Sprintf(
						"0198a5d0-0000-7000-8000-%012d",
						index+10,
					),
				}).Error; err != nil {
				t.Fatal(err)
			}
		}
		var adapterCalls atomic.Int32
		deliverer := OutboxDeliverFunc(func(
			context.Context,
			*models.OutboxDelivery,
			CloudEventEnvelope,
		) error {
			adapterCalls.Add(1)
			return nil
		})
		first, err := fixture.service.ProcessOutboxBatch(
			context.Background(),
			"raw-budget-exhausted-first",
			2,
			deliverer,
		)
		if err != nil {
			t.Fatal(err)
		}
		if first.consumed != 2 ||
			first.Claimed != 0 ||
			first.Dead != 2 ||
			first.Failed != 2 {
			t.Fatalf("first exhausted raw budget batch = %+v", first)
		}
		var deadAfterFirst int64
		if err := fixture.db.Model(&models.OutboxDelivery{}).
			Where(
				"id IN ? AND status = ?",
				[]string{
					deliveries[0].ID,
					deliveries[1].ID,
					deliveries[2].ID,
				},
				models.OutboxDeliveryDead,
			).
			Count(&deadAfterFirst).Error; err != nil {
			t.Fatal(err)
		}
		if deadAfterFirst != 2 {
			t.Fatalf(
				"first exhausted raw budget transitioned %d rows, want 2",
				deadAfterFirst,
			)
		}
		second, err := fixture.service.ProcessOutboxBatch(
			context.Background(),
			"raw-budget-exhausted-second",
			1,
			deliverer,
		)
		if err != nil {
			t.Fatal(err)
		}
		if second.consumed != 1 ||
			second.Claimed != 0 ||
			second.Dead != 1 ||
			second.Failed != 1 ||
			adapterCalls.Load() != 0 {
			t.Fatalf(
				"second exhausted raw budget batch=%+v calls=%d",
				second,
				adapterCalls.Load(),
			)
		}
	})

	t.Run("invalid then valid", func(t *testing.T) {
		now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
		fixture := newWebhookOutboxLifecycleFixture(t, now)
		if err := fixture.db.Model(&models.OutboxDelivery{}).
			Where("id = ?", fixture.delivery.ID).
			Update(
				"destination_id",
				"bad-shot:"+fixture.snapshot.ID,
			).Error; err != nil {
			t.Fatal(err)
		}
		laterProject := seedLifecycleWorkerProject(t, fixture, 7)
		laterDelivery := seedLifecycleNonWebhookDelivery(
			t,
			fixture,
			laterProject,
			7,
		)
		var adapterCalls atomic.Int32
		batch, err := fixture.service.ProcessOutboxBatch(
			context.Background(),
			"raw-budget-invalid-valid",
			2,
			OutboxDeliverFunc(func(
				context.Context,
				*models.OutboxDelivery,
				CloudEventEnvelope,
			) error {
				adapterCalls.Add(1)
				return nil
			}),
		)
		if err != nil {
			t.Fatal(err)
		}
		if batch.consumed != 2 ||
			batch.Claimed != 1 ||
			batch.Delivered != 1 ||
			batch.Dead != 0 ||
			adapterCalls.Load() != 1 {
			t.Fatalf(
				"invalid+valid raw budget batch=%+v calls=%d",
				batch,
				adapterCalls.Load(),
			)
		}
		var later models.OutboxDelivery
		if err := fixture.db.First(
			&later,
			"id = ?",
			laterDelivery.ID,
		).Error; err != nil {
			t.Fatal(err)
		}
		if later.Status != models.OutboxDeliverySucceeded {
			t.Fatalf("later valid status = %s", later.Status)
		}
	})

	t.Run("mixed dead and claimed", func(t *testing.T) {
		now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
		fixture := newWebhookOutboxLifecycleFixture(t, now)
		stale, _, _ := fixture.createIntent(t, "raw-budget-mixed-stale")
		if err := fixture.db.Model(&models.OutboxDelivery{}).
			Where("id = ?", stale.ID).
			Updates(map[string]any{
				"status":              models.OutboxDeliveryProcessing,
				"attempts":            stale.MaxAttempts,
				"locked_at":           now.Add(-2 * time.Minute),
				"locked_by":           "raw-budget-mixed-stale",
				"lock_token":          "0198a5d0-0000-7000-8000-000000000099",
				"dispatch_started_at": now.Add(-2 * time.Minute),
			}).Error; err != nil {
			t.Fatal(err)
		}
		laterProject := seedLifecycleWorkerProject(t, fixture, 8)
		seedLifecycleNonWebhookDelivery(t, fixture, laterProject, 8)
		var adapterCalls atomic.Int32
		batch, err := fixture.service.ProcessOutboxBatch(
			context.Background(),
			"raw-budget-mixed",
			3,
			OutboxDeliverFunc(func(
				context.Context,
				*models.OutboxDelivery,
				CloudEventEnvelope,
			) error {
				adapterCalls.Add(1)
				return nil
			}),
		)
		if err != nil {
			t.Fatal(err)
		}
		if batch.consumed != 3 ||
			batch.Claimed != 2 ||
			batch.Delivered != 2 ||
			batch.Dead != 1 ||
			batch.Failed != 1 ||
			adapterCalls.Load() != 2 {
			t.Fatalf(
				"mixed raw budget batch=%+v calls=%d",
				batch,
				adapterCalls.Load(),
			)
		}
	})
}

func TestProcessOutboxBatchDefersRowsCreatedAtOrAfterBatchCutoff(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	laterProject := seedLifecycleWorkerProject(t, fixture, 9)
	batchCutoff := outboxBatchPersistenceCutoff(fixture.db).Add(time.Second)
	originalNowFunc := fixture.db.Config.NowFunc
	fixture.db.Config.NowFunc = func() time.Time {
		return batchCutoff
	}
	t.Cleanup(func() {
		fixture.db.Config.NowFunc = originalNowFunc
	})

	createGenerated := func(
		project models.Project,
		serial int,
		createdAt time.Time,
	) error {
		event := models.DomainEvent{
			ID: fmt.Sprintf(
				"76000000-0000-7000-8000-%012d",
				serial,
			),
			CreatedAt:       createdAt,
			OrganizationID:  fixture.scope.OrganizationID,
			ProjectID:       project.ID,
			SpecVersion:     "1.0",
			Source:          "urn:chronodesk:test:batch-cutoff",
			Type:            "io.chronodesk.test.batch-cutoff.v1",
			Subject:         fmt.Sprintf("batch-cutoff/%d", serial),
			Time:            now,
			DataContentType: "application/json",
			Data:            []byte(`{}`),
			ActorType:       models.ActorTypeSystem,
			ActorID:         outboxSystemActorID,
			ResourceVersion: 1,
		}
		if err := fixture.db.Create(&event).Error; err != nil {
			return err
		}
		delivery := models.OutboxDelivery{
			ID: fmt.Sprintf(
				"77000000-0000-7000-8000-%012d",
				serial,
			),
			CreatedAt:       createdAt,
			UpdatedAt:       createdAt,
			OrganizationID:  fixture.scope.OrganizationID,
			ProjectID:       project.ID,
			EventID:         event.ID,
			DestinationType: "event_stream",
			DestinationID:   fmt.Sprintf("batch-cutoff-%d", serial),
			Status:          models.OutboxDeliveryPending,
			MaxAttempts:     8,
			NextAttemptAt:   now.Add(-time.Minute),
		}
		return fixture.db.Create(&delivery).Error
	}

	var (
		generateOnce sync.Once
		generateErr  error
		adapterCalls atomic.Int32
	)
	deliverer := OutboxDeliverFunc(func(
		context.Context,
		*models.OutboxDelivery,
		CloudEventEnvelope,
	) error {
		adapterCalls.Add(1)
		generateOnce.Do(func() {
			generateErr = errors.Join(
				createGenerated(
					models.Project{
						ID:             fixture.scope.ProjectID,
						OrganizationID: fixture.scope.OrganizationID,
					},
					1,
					batchCutoff,
				),
				createGenerated(
					laterProject,
					2,
					batchCutoff.Add(time.Nanosecond),
				),
			)
		})
		return generateErr
	})
	first, err := fixture.service.ProcessOutboxBatch(
		context.Background(),
		"batch-cutoff-first",
		3,
		deliverer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Claimed != 1 ||
		first.Delivered != 1 ||
		first.consumed != 1 ||
		adapterCalls.Load() != 1 {
		t.Fatalf(
			"first cutoff batch=%+v calls=%d, want only entry row",
			first,
			adapterCalls.Load(),
		)
	}
	var pending int64
	if err := fixture.db.Model(&models.OutboxDelivery{}).
		Where(
			"id IN ? AND status = ?",
			[]string{
				"77000000-0000-7000-8000-000000000001",
				"77000000-0000-7000-8000-000000000002",
			},
			models.OutboxDeliveryPending,
		).
		Count(&pending).Error; err != nil {
		t.Fatal(err)
	}
	if pending != 2 {
		t.Fatalf("new cutoff rows pending = %d, want 2", pending)
	}

	fixture.db.Config.NowFunc = func() time.Time {
		return batchCutoff.Add(2 * time.Nanosecond)
	}
	second, err := fixture.service.ProcessOutboxBatch(
		context.Background(),
		"batch-cutoff-second",
		3,
		deliverer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.Claimed != 2 ||
		second.Delivered != 2 ||
		second.consumed != 2 ||
		adapterCalls.Load() != 3 {
		t.Fatalf(
			"second cutoff batch=%+v calls=%d, want deferred rows",
			second,
			adapterCalls.Load(),
		)
	}
}

func TestProcessOutboxBatchRepeatsStrictBatchCutoffInCandidateAndCAS(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	var (
		mu                   sync.Mutex
		rawHasCutoff         bool
		snapshotLockQueries  int
		snapshotLockMissing  int
		claimUpdateHasCutoff bool
	)
	const (
		queryCallback  = "test:batch_cutoff_candidate_queries"
		updateCallback = "test:batch_cutoff_claim_cas"
	)
	if err := fixture.db.Callback().Query().After("gorm:query").
		Register(queryCallback, func(tx *gorm.DB) {
			sql := strings.ToLower(tx.Statement.SQL.String())
			mu.Lock()
			defer mu.Unlock()
			if strings.Contains(sql, "order by next_attempt_at asc") {
				rawHasCutoff = strings.Contains(sql, "created_at < ?")
			}
			if strings.Contains(sql, "id in (") &&
				strings.Contains(sql, "webhook_delivery_snapshots") {
				snapshotLockQueries++
				if !strings.Contains(sql, "created_at < ?") {
					snapshotLockMissing++
				}
			}
		}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Callback().Update().After("gorm:update").
		Register(updateCallback, func(tx *gorm.DB) {
			sql := strings.ToLower(tx.Statement.SQL.String())
			if tx.Statement.Table !=
				(models.OutboxDelivery{}).TableName() ||
				!strings.Contains(sql, "status") {
				return
			}
			mu.Lock()
			claimUpdateHasCutoff = claimUpdateHasCutoff ||
				strings.Contains(sql, "created_at < ?")
			mu.Unlock()
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = fixture.db.Callback().Query().Remove(queryCallback)
		_ = fixture.db.Callback().Update().Remove(updateCallback)
	})
	if _, err := fixture.service.ProcessOutboxBatch(
		context.Background(),
		"batch-cutoff-contract",
		1,
		OutboxDeliverFunc(func(
			context.Context,
			*models.OutboxDelivery,
			CloudEventEnvelope,
		) error {
			return nil
		}),
	); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !rawHasCutoff ||
		snapshotLockQueries == 0 ||
		snapshotLockMissing != 0 ||
		!claimUpdateHasCutoff {
		t.Fatalf(
			"strict cutoff raw=%t snapshot_locks=%d missing=%d CAS=%t",
			rawHasCutoff,
			snapshotLockQueries,
			snapshotLockMissing,
			claimUpdateHasCutoff,
		)
	}
}

func TestWebhookOutboxMalformedDestinationNeverReachesAdapter(
	t *testing.T,
) {
	for _, test := range []struct {
		name        string
		destination func(string) string
	}{
		{
			name: "wrong_prefix",
			destination: func(id string) string {
				return "bad-shot:" + id
			},
		},
		{
			name: "uppercase_prefix",
			destination: func(id string) string {
				return "SNAPSHOT:" + id
			},
		},
		{
			name: "compact_uuid",
			destination: func(id string) string {
				return "snapshot:" + strings.ReplaceAll(id, "-", "")
			},
		},
		{
			name: "uuid_urn",
			destination: func(id string) string {
				return "snapshot:urn:uuid:" + id
			},
		},
		{
			name: "uppercase_uuid",
			destination: func(id string) string {
				return "snapshot:" + strings.ToUpper(id)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(
				2026,
				time.August,
				10,
				9,
				0,
				0,
				0,
				time.UTC,
			)
			fixture := newWebhookOutboxLifecycleFixture(t, now)
			if err := fixture.db.Model(&models.OutboxDelivery{}).
				Where("id = ?", fixture.delivery.ID).
				Update(
					"destination_id",
					test.destination(fixture.snapshot.ID),
				).Error; err != nil {
				t.Fatal(err)
			}
			deliverer := &lifecycleRichDeliverer{
				result: OutboxKnownSuccess(now),
			}
			batch, err := fixture.service.ProcessOutboxBatch(
				context.Background(),
				"malformed-destination-worker",
				1,
				deliverer,
			)
			if err != nil {
				t.Fatal(err)
			}
			if batch.Claimed != 0 ||
				deliverer.richCalls.Load() != 0 ||
				deliverer.legacyCalls.Load() != 0 {
				t.Fatalf(
					"malformed destination reached adapter: batch=%+v rich=%d legacy=%d",
					batch,
					deliverer.richCalls.Load(),
					deliverer.legacyCalls.Load(),
				)
			}
			var delivery models.OutboxDelivery
			if err := fixture.db.First(
				&delivery,
				"id = ?",
				fixture.delivery.ID,
			).Error; err != nil {
				t.Fatal(err)
			}
			if delivery.Status != models.OutboxDeliveryPending ||
				delivery.LockedAt != nil ||
				delivery.LockToken != nil {
				t.Fatalf(
					"malformed destination was claimed: %+v",
					delivery,
				)
			}
		})
	}
}

func TestWebhookOutboxClaimCursorAdvancesAcrossIneligibleRawPages(
	t *testing.T,
) {
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	malformed := fixture.delivery
	expired, expiredSnapshot, _ := fixture.createIntent(
		t,
		"raw-page-expired",
	)
	mismatched, _, _ := fixture.createIntent(
		t,
		"raw-page-mismatch",
	)
	valid, _, _ := fixture.createIntent(t, "raw-page-valid")

	if err := fixture.db.Model(&models.OutboxDelivery{}).
		Where("id = ?", malformed.ID).
		Updates(map[string]any{
			"destination_id":  "bad-shot:" + fixture.snapshot.ID,
			"next_attempt_at": now.Add(-4 * time.Minute),
		}).Error; err != nil {
		t.Fatal(err)
	}
	expiredAt := now.Add(-time.Minute)
	if err := fixture.db.Model(&models.OutboxDelivery{}).
		Where("id = ?", expired.ID).
		Updates(map[string]any{
			"expires_at":      expiredAt,
			"next_attempt_at": now.Add(-3 * time.Minute),
		}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Exec(
		"UPDATE webhook_delivery_snapshots "+
			"SET credential_expires_at = ? WHERE id = ?",
		expiredAt,
		expiredSnapshot.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.OutboxDelivery{}).
		Where("id = ?", mismatched.ID).
		Updates(map[string]any{
			"event_id":        fixture.event.ID,
			"next_attempt_at": now.Add(-2 * time.Minute),
		}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.OutboxDelivery{}).
		Where("id = ?", valid.ID).
		Update("next_attempt_at", now.Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 3; attempt++ {
		claimed, err := fixture.service.ClaimPendingOutbox(
			fixture.worker,
			fmt.Sprintf("ineligible-page-%d", attempt),
			1,
			time.Minute,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(claimed) != 0 {
			t.Fatalf(
				"ineligible raw page %d returned candidate %+v",
				attempt,
				claimed,
			)
		}
	}
	claimed, err := fixture.service.ClaimPendingOutbox(
		fixture.worker,
		"valid-after-ineligible-pages",
		1,
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].ID != valid.ID {
		t.Fatalf(
			"valid candidate did not become reachable: %+v",
			claimed,
		)
	}
}
