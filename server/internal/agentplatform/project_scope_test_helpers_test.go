package agentplatform

import (
	"context"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/gorm"
)

const agentplatformTestOutboxWorkerActorID = "outbox-delivery-worker"

func installAgentplatformTestProjectScope(
	t *testing.T,
	db *gorm.DB,
) models.ProjectScope {
	t.Helper()
	if err := db.AutoMigrate(
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.Queue{},
		&models.RequestTypeVersion{},
		&models.WorkflowVersion{},
		&models.ConfigurationRelease{},
	); err != nil {
		t.Fatalf("migrate agentplatform test project scope: %v", err)
	}
	organization := models.Organization{}
	if err := db.Where("slug = ?", "agentplatform-test").
		FirstOrCreate(&organization, models.Organization{
			Slug:   "agentplatform-test",
			Name:   "Agent Platform Test",
			Status: models.OrganizationStatusActive,
		}).Error; err != nil {
		t.Fatal(err)
	}
	unit := models.BusinessUnit{}
	if err := db.Where(
		"organization_id = ? AND key = ?",
		organization.ID,
		"TEST",
	).FirstOrCreate(&unit, models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "TEST",
		Name:           "Test",
		Status:         models.BusinessUnitStatusActive,
	}).Error; err != nil {
		t.Fatal(err)
	}
	project := models.Project{}
	if err := db.Where(
		"organization_id = ? AND key = ?",
		organization.ID,
		models.ProjectKey("TEST"),
	).FirstOrCreate(&project, models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            models.ProjectKey("TEST"),
		Name:           "Test",
		Status:         models.ProjectStatusActive,
	}).Error; err != nil {
		t.Fatal(err)
	}
	queue := models.Queue{}
	if err := db.Where(
		"project_id = ? AND key = ?",
		project.ID,
		models.QueueKey("default"),
	).FirstOrCreate(&queue, models.Queue{
		ProjectID: project.ID,
		Key:       models.QueueKey("default"),
		Name:      "Default",
		Status:    models.QueueStatusActive,
		IsDefault: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	requestTypeID, workflowID := bootstrapAgentplatformTestConfiguration(
		t,
		db,
		project.Scope(),
	)
	backfillAgentplatformTestProjectRows(
		t,
		db,
		project.Scope(),
		queue.ID,
		requestTypeID,
		workflowID,
	)
	return project.Scope()
}

func bootstrapAgentplatformTestConfiguration(
	t *testing.T,
	db *gorm.DB,
	scope models.ProjectScope,
) (string, string) {
	t.Helper()
	ctx, err := services.WithOperationContext(
		context.Background(),
		services.OperationContext{
			Scope:  scope,
			Actor:  models.SystemActor("agentplatform-test-bootstrap"),
			Source: services.SourceProtocolWorker,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	configurationService, err := services.NewProjectConfigurationService(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configurationService.BootstrapProjectConfiguration(ctx); err != nil {
		t.Fatalf("bootstrap agentplatform test configuration: %v", err)
	}
	var requestType models.RequestTypeVersion
	if err := db.Where(
		"project_id = ? AND key = ? AND status = ?",
		scope.ProjectID,
		"request",
		models.ConfigurationStatusPublished,
	).First(&requestType).Error; err != nil {
		t.Fatal(err)
	}
	var workflow models.WorkflowVersion
	if err := db.Where(
		"project_id = ? AND key = ? AND status = ?",
		scope.ProjectID,
		"default",
		models.ConfigurationStatusPublished,
	).First(&workflow).Error; err != nil {
		t.Fatal(err)
	}
	return requestType.ID, workflow.ID
}

func backfillAgentplatformTestProjectRows(
	t *testing.T,
	db *gorm.DB,
	scope models.ProjectScope,
	queueID uint,
	requestTypeVersionID string,
	workflowVersionID string,
) {
	t.Helper()
	for _, model := range []any{
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.Notification{},
	} {
		if !db.Migrator().HasTable(model) ||
			!db.Migrator().HasColumn(model, "organization_id") ||
			!db.Migrator().HasColumn(model, "project_id") {
			continue
		}
		if err := db.Model(model).
			Where("organization_id = 0 OR project_id = 0").
			Updates(map[string]any{
				"organization_id": scope.OrganizationID,
				"project_id":      scope.ProjectID,
			}).Error; err != nil {
			t.Fatalf("backfill agentplatform test row %T: %v", model, err)
		}
	}
	if db.Migrator().HasTable(&models.Ticket{}) {
		if err := db.Model(&models.Ticket{}).
			Where(
				"organization_id = 0 OR project_id = 0 OR queue_id = 0",
			).
			Updates(map[string]any{
				"organization_id":         scope.OrganizationID,
				"project_id":              scope.ProjectID,
				"queue_id":                queueID,
				"request_type_version_id": requestTypeVersionID,
				"workflow_version_id":     workflowVersionID,
			}).Error; err != nil {
			t.Fatalf("backfill agentplatform test ticket: %v", err)
		}
	}
}

func agentplatformTestOperationContext(
	t *testing.T,
	scope models.ProjectScope,
	actor models.ActorRef,
) context.Context {
	t.Helper()
	source := services.SourceProtocolWorker
	if actor.Type == models.ActorTypeHuman {
		source = services.SourceProtocolHumanREST
	}
	ctx, err := services.WithOperationContext(
		context.Background(),
		services.OperationContext{
			Scope:  scope,
			Actor:  actor,
			Source: source,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func agentplatformTestOutboxWorkerContext(
	t *testing.T,
	scope models.ProjectScope,
) context.Context {
	t.Helper()
	return agentplatformTestOperationContext(
		t,
		scope,
		models.SystemActor(agentplatformTestOutboxWorkerActorID),
	)
}
