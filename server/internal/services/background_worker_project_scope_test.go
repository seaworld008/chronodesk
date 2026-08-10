package services

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
)

func TestProcessOutboxBatchUsesTrustedProjectScopeOutsideDBTransaction(
	t *testing.T,
) {
	db := openAgentNativeTestDB(t)
	service := NewAgentNativeService(db)
	producer := models.SystemActor("worker-scope-test-producer")
	projectACtx := testProjectOperationContext(t, db, producer)
	projectAOperation, err := OperationContextFromContext(projectACtx)
	if err != nil {
		t.Fatal(err)
	}
	projectB := createAdditionalWorkerTestProject(
		t,
		db,
		projectAOperation.Scope.OrganizationID,
		models.ProjectKey("WORKER-B"),
	)
	projectBCtx, err := EnsureSystemProjectOperationContext(
		context.Background(),
		projectB.Scope(),
		producer,
		"producer-b",
		"producer-b",
	)
	if err != nil {
		t.Fatal(err)
	}
	createWorkerScopeOutboxEvent(t, service, projectACtx, producer, "a")
	createWorkerScopeOutboxEvent(t, service, projectBCtx, producer, "b")

	projectAWorkerCtx, err := EnsureSystemProjectOperationContext(
		context.Background(),
		projectAOperation.Scope,
		models.SystemActor(outboxSystemActorID),
		"worker-a",
		"worker-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	assertDeliveryContext := func(
		expected models.ProjectScope,
	) OutboxDeliverer {
		return OutboxDeliverFunc(func(
			ctx context.Context,
			delivery *models.OutboxDelivery,
			_ CloudEventEnvelope,
		) error {
			if scopeddb.HasTransaction(ctx) {
				return errors.New(
					"outbox network delivery inherited a database transaction",
				)
			}
			operation, operationErr := OperationContextFromContext(ctx)
			if operationErr != nil {
				return operationErr
			}
			if operation.Scope != expected {
				return fmt.Errorf(
					"delivery scope = %+v, want %+v",
					operation.Scope,
					expected,
				)
			}
			if operation.Actor != models.SystemActor(outboxSystemActorID) ||
				operation.Source != SourceProtocolWorker {
				return fmt.Errorf(
					"delivery provenance = %+v/%s",
					operation.Actor,
					operation.Source,
				)
			}
			if delivery.OrganizationID != expected.OrganizationID ||
				delivery.ProjectID != expected.ProjectID {
				return errors.New("delivery row escaped the trusted project")
			}
			return nil
		})
	}

	projectAResult, err := service.ProcessOutboxBatch(
		projectAWorkerCtx,
		"project-a-worker",
		10,
		assertDeliveryContext(projectAOperation.Scope),
	)
	if err != nil {
		t.Fatal(err)
	}
	if projectAResult.Claimed != 1 || projectAResult.Delivered != 1 {
		t.Fatalf("project A batch = %+v, want one delivery", projectAResult)
	}
	var projectBPending int64
	if err := db.Model(&models.OutboxDelivery{}).
		Where(
			"organization_id = ? AND project_id = ? AND status = ?",
			projectB.OrganizationID,
			projectB.ID,
			models.OutboxDeliveryPending,
		).
		Count(&projectBPending).Error; err != nil {
		t.Fatal(err)
	}
	if projectBPending != 1 {
		t.Fatalf(
			"project B pending deliveries = %d, want 1 after project A run",
			projectBPending,
		)
	}

	projectBResult, err := service.ProcessOutboxBatch(
		context.Background(),
		"global-worker",
		10,
		assertDeliveryContext(projectB.Scope()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if projectBResult.Claimed != 1 || projectBResult.Delivered != 1 {
		t.Fatalf("global follow-up batch = %+v, want project B delivery", projectBResult)
	}
}

func TestMarkOutboxFailedLocksAndUpdatesInOneAtomicTransaction(t *testing.T) {
	db := openAgentNativeTestDB(t)
	service := NewAgentNativeService(db)
	producer := models.SystemActor("outbox-atomic-test-producer")
	producerCtx := testProjectOperationContext(t, db, producer)
	createWorkerScopeOutboxEvent(
		t,
		service,
		producerCtx,
		producer,
		"atomic",
	)
	producerOperation, err := OperationContextFromContext(producerCtx)
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, err := EnsureSystemProjectOperationContext(
		context.Background(),
		producerOperation.Scope,
		models.SystemActor(outboxSystemActorID),
		"atomic-worker",
		"atomic-worker",
	)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.ClaimPendingOutbox(
		workerCtx,
		"atomic-worker",
		1,
		time.Minute,
	)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim delivery: count=%d err=%v", len(claimed), err)
	}
	claim, err := OutboxClaimRefFromDelivery(claimed[0])
	if err != nil {
		t.Fatal(err)
	}

	type atomicMarker struct{}
	markedCtx := context.WithValue(workerCtx, atomicMarker{}, true)
	injected := errors.New("injected outbox finalize failure")
	const (
		queryCallback  = "test:outbox_atomic_query"
		updateCallback = "test:outbox_atomic_update"
	)
	var (
		mu              sync.Mutex
		queryPool       string
		updatePool      string
		queryUsesTx     bool
		updateUsesTx    bool
		updateIntercept bool
	)
	if err := db.Callback().Query().Before("gorm:query").Register(
		queryCallback,
		func(tx *gorm.DB) {
			if tx.Statement.Context.Value(atomicMarker{}) != true ||
				tx.Statement.Table != "outbox_deliveries" {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			queryPool = fmt.Sprintf("%p", tx.Statement.ConnPool)
			_, queryUsesTx = tx.Statement.ConnPool.(gorm.TxCommitter)
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Update().Before("gorm:update").Register(
		updateCallback,
		func(tx *gorm.DB) {
			if tx.Statement.Context.Value(atomicMarker{}) != true ||
				tx.Statement.Table != "outbox_deliveries" {
				return
			}
			mu.Lock()
			updatePool = fmt.Sprintf("%p", tx.Statement.ConnPool)
			_, updateUsesTx = tx.Statement.ConnPool.(gorm.TxCommitter)
			updateIntercept = true
			mu.Unlock()
			_ = tx.AddError(injected)
		},
	); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Query().Remove(queryCallback)
		_ = db.Callback().Update().Remove(updateCallback)
	})

	err = service.MarkOutboxFailed(
		markedCtx,
		claim,
		errors.New("destination failed"),
	)
	if !errors.Is(err, injected) {
		t.Fatalf("MarkOutboxFailed() error = %v, want injected failure", err)
	}
	mu.Lock()
	recordedQueryPool := queryPool
	recordedUpdatePool := updatePool
	recordedQueryUsesTx := queryUsesTx
	recordedUpdateUsesTx := updateUsesTx
	recordedUpdateIntercept := updateIntercept
	mu.Unlock()
	if !recordedUpdateIntercept ||
		!recordedQueryUsesTx ||
		!recordedUpdateUsesTx ||
		recordedQueryPool == "" ||
		recordedQueryPool != recordedUpdatePool {
		t.Fatalf(
			"failed finalize did not use one transaction: query=%q/%v update=%q/%v intercepted=%v",
			recordedQueryPool,
			recordedQueryUsesTx,
			recordedUpdatePool,
			recordedUpdateUsesTx,
			recordedUpdateIntercept,
		)
	}

	var persisted models.OutboxDelivery
	if err := db.First(&persisted, "id = ?", claimed[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Status != models.OutboxDeliveryProcessing ||
		persisted.LockedBy != "atomic-worker" ||
		persisted.LockedAt == nil {
		t.Fatalf("failed transaction partially finalized delivery: %+v", persisted)
	}
}

func TestAutomationSchedulerEmitsProjectScopedChecksForEveryActiveProject(
	t *testing.T,
) {
	native, automation, user := setupNativeAutomationTest(t)
	projectACtx := testProjectOperationContext(
		t,
		native.db,
		models.HumanActor(user.ID),
	)
	projectAOperation, err := OperationContextFromContext(projectACtx)
	if err != nil {
		t.Fatal(err)
	}
	projectB := createAdditionalWorkerTestProject(
		t,
		native.db,
		projectAOperation.Scope.OrganizationID,
		models.ProjectKey("AUTOMATION-B"),
	)
	projectBCtx, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  projectB.Scope(),
			Actor:  models.HumanActor(user.ID),
			Source: SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	projectARule := createAutomationRule(
		t,
		automation,
		eventcontract.AutomationScheduledCheckEventType,
		models.RuleAction{
			Type:   "add_comment",
			Params: map[string]interface{}{"content": "scheduled"},
		},
	)
	projectBRule := projectARule
	projectBRule.ID = 0
	projectBRule.OrganizationID = projectB.OrganizationID
	projectBRule.ProjectID = projectB.ID
	projectBRule.Name += " project B"
	if err := native.db.Create(&projectBRule).Error; err != nil {
		t.Fatalf("create project B automation rule: %v", err)
	}
	createWorkerScopeTicket(t, native, projectACtx, user.ID, "automation-a")
	createWorkerScopeTicket(t, native, projectBCtx, user.ID, "automation-b")

	scheduler := &SchedulerService{
		db:                native.db,
		automationService: automation,
	}
	if err := scheduler.automationRulesHandler(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertProjectEventSet(
		t,
		native.db,
		eventcontract.AutomationScheduledCheckEventType,
		projectAOperation.Scope.ProjectID,
		projectB.ID,
	)
}

func TestSLASchedulerScansEveryActiveProject(t *testing.T) {
	db := openAgentNativeTestDB(t)
	if err := db.AutoMigrate(&models.SLAConfig{}); err != nil {
		t.Fatal(err)
	}
	user := seedActorUser(t, db, "sla-project-worker")
	projectACtx := testProjectOperationContext(
		t,
		db,
		models.HumanActor(user.ID),
	)
	projectAOperation, err := OperationContextFromContext(projectACtx)
	if err != nil {
		t.Fatal(err)
	}
	projectB := createAdditionalWorkerTestProject(
		t,
		db,
		projectAOperation.Scope.OrganizationID,
		models.ProjectKey("SLA-B"),
	)
	projectBCtx, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  projectB.Scope(),
			Actor:  models.HumanActor(user.ID),
			Source: SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]models.SLAConfig{
		{
			OrganizationID: projectAOperation.Scope.OrganizationID,
			ProjectID:      projectAOperation.Scope.ProjectID,
			Name:           "worker project A SLA",
			IsActive:       true,
			IsDefault:      true,
			ResponseTime:   1,
			ResolutionTime: 1,
		},
		{
			OrganizationID: projectB.OrganizationID,
			ProjectID:      projectB.ID,
			Name:           "worker project B SLA",
			IsActive:       true,
			IsDefault:      true,
			ResponseTime:   1,
			ResolutionTime: 1,
		},
	}).Error; err != nil {
		t.Fatal(err)
	}
	native := NewAgentNativeService(db)
	ticketA := createWorkerScopeTicket(
		t,
		native,
		projectACtx,
		user.ID,
		"sla-a",
	)
	ticketB := createWorkerScopeTicket(
		t,
		native,
		projectBCtx,
		user.ID,
		"sla-b",
	)
	if err := db.Model(&models.Ticket{}).
		Where("id IN ?", []uint{ticketA.ID, ticketB.ID}).
		Updates(map[string]any{
			// Keep this fixture overdue on every weekday and weekend. The
			// default SLA excludes weekends, so a 48-hour offset is not
			// sufficient when the test runs on Sunday.
			"created_at":   time.Now().Add(-14 * 24 * time.Hour),
			"sla_due_date": nil,
			"sla_breached": false,
		}).Error; err != nil {
		t.Fatal(err)
	}
	escalation := NewEscalationService(db)
	escalation.SetAgentNativeService(native)
	if err := escalation.CheckSLAViolations(context.Background()); err != nil {
		t.Fatal(err)
	}
	var breached int64
	if err := db.Model(&models.Ticket{}).
		Where(
			"id IN ? AND sla_breached = ?",
			[]uint{ticketA.ID, ticketB.ID},
			true,
		).
		Count(&breached).Error; err != nil {
		t.Fatal(err)
	}
	if breached != 2 {
		t.Fatalf("SLA breached tickets = %d, want 2", breached)
	}
	assertProjectEventSet(
		t,
		db,
		SLABreachEventType,
		projectAOperation.Scope.ProjectID,
		projectB.ID,
	)
}

func TestStatisticsWorkerUpdatesSLAConfigsWithinEachProjectScope(t *testing.T) {
	db := openAgentNativeTestDB(t)
	if err := db.AutoMigrate(&models.SLAConfig{}); err != nil {
		t.Fatal(err)
	}
	user := seedActorUser(t, db, "sla-statistics-worker")
	projectAContext := testProjectOperationContext(
		t,
		db,
		models.HumanActor(user.ID),
	)
	projectAOperation, err := OperationContextFromContext(projectAContext)
	if err != nil {
		t.Fatal(err)
	}
	projectB := createAdditionalWorkerTestProject(
		t,
		db,
		projectAOperation.Scope.OrganizationID,
		models.ProjectKey("SLA-STATS-B"),
	)
	configs := []models.SLAConfig{
		{
			OrganizationID: projectAOperation.Scope.OrganizationID,
			ProjectID:      projectAOperation.Scope.ProjectID,
			Name:           "project A statistics",
			IsActive:       true,
			ResponseTime:   60,
			ResolutionTime: 120,
			AppliedCount:   10,
			ViolationCount: 2,
		},
		{
			OrganizationID: projectB.OrganizationID,
			ProjectID:      projectB.ID,
			Name:           "project B statistics",
			IsActive:       true,
			ResponseTime:   60,
			ResolutionTime: 120,
			AppliedCount:   5,
			ViolationCount: 1,
		},
	}
	if err := db.Create(&configs).Error; err != nil {
		t.Fatal(err)
	}
	scheduler := &SchedulerService{db: db}
	projectAWorkerContext, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  projectAOperation.Scope,
			Actor:  models.SystemActor(schedulerSystemActorID),
			Source: SourceProtocolWorker,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.updateStatisticsHandler(projectAWorkerContext); err != nil {
		t.Fatal(err)
	}
	var scoped []models.SLAConfig
	if err := db.Order("id ASC").Find(&scoped).Error; err != nil {
		t.Fatal(err)
	}
	if scoped[0].ComplianceRate != 80 || scoped[1].ComplianceRate != 0 {
		t.Fatalf("scoped statistics update crossed projects: %+v", scoped)
	}
	if err := scheduler.updateStatisticsHandler(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := db.Order("id ASC").Find(&scoped).Error; err != nil {
		t.Fatal(err)
	}
	if scoped[0].ComplianceRate != 80 || scoped[1].ComplianceRate != 80 {
		t.Fatalf("global worker did not enumerate project scopes: %+v", scoped)
	}
}

func createAdditionalWorkerTestProject(
	t *testing.T,
	db *gorm.DB,
	organizationID uint,
	key models.ProjectKey,
) models.Project {
	t.Helper()
	var unit models.BusinessUnit
	if err := db.Where("organization_id = ?", organizationID).
		First(&unit).Error; err != nil {
		t.Fatal(err)
	}
	project := models.Project{
		OrganizationID: organizationID,
		BusinessUnitID: unit.ID,
		Key:            key,
		Name:           string(key),
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Queue{
		ProjectID: project.ID,
		Key:       models.QueueKey("default"),
		Name:      "Default",
		Status:    models.QueueStatusActive,
		IsDefault: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	bootstrapContext, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  project.Scope(),
			Actor:  models.SystemActor("worker-test-configuration-bootstrap"),
			Source: SourceProtocolWorker,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	configurationService, err := NewProjectConfigurationService(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configurationService.BootstrapProjectConfiguration(
		bootstrapContext,
	); err != nil {
		t.Fatal(err)
	}
	return project
}

func createWorkerScopeOutboxEvent(
	t *testing.T,
	service *AgentNativeService,
	ctx context.Context,
	actor models.ActorRef,
	suffix string,
) {
	t.Helper()
	if _, err := service.createDomainEvent(
		t,
		ctx,
		DomainEventInput{
			Type:            "io.chronodesk.worker.scope.test.v1",
			Subject:         "worker-scope/" + suffix,
			Actor:           actor,
			ResourceVersion: 1,
			Data:            map[string]any{"suffix": suffix},
		},
		[]OutboxTarget{{
			Type:        "test",
			ID:          "destination-" + suffix,
			MaxAttempts: 3,
		}},
	); err != nil {
		t.Fatal(err)
	}
}

func createWorkerScopeTicket(
	t *testing.T,
	native *AgentNativeService,
	ctx context.Context,
	userID uint,
	suffix string,
) *models.Ticket {
	t.Helper()
	result, err := native.CreateNativeTicket(ctx, NativeTicketCreateInput{
		Request: models.TicketCreateRequest{
			Title:       "Worker project " + suffix,
			Description: "project-scoped background worker test",
			Type:        models.TicketTypeIncident,
			Priority:    models.TicketPriorityHigh,
			Source:      models.TicketSourceWeb,
		},
		Actor:      models.HumanActor(userID),
		TrustLevel: models.TicketTrustLevelVerified,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result.Ticket
}

func assertProjectEventSet(
	t *testing.T,
	db *gorm.DB,
	eventType string,
	projectIDs ...uint,
) {
	t.Helper()
	var events []models.DomainEvent
	if err := db.Where("type = ?", eventType).
		Order("project_id ASC").
		Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != len(projectIDs) {
		t.Fatalf(
			"%s events = %d, want %d: %+v",
			eventType,
			len(events),
			len(projectIDs),
			events,
		)
	}
	expected := make(map[uint]struct{}, len(projectIDs))
	for _, projectID := range projectIDs {
		expected[projectID] = struct{}{}
	}
	for index := range events {
		if events[index].OrganizationID == 0 {
			t.Fatalf("event has no organization scope: %+v", events[index])
		}
		if _, exists := expected[events[index].ProjectID]; !exists {
			t.Fatalf("event escaped expected projects: %+v", events[index])
		}
		delete(expected, events[index].ProjectID)
	}
	if len(expected) != 0 {
		t.Fatalf("projects missing scoped event: %+v", expected)
	}
}
