package services

import (
	"context"
	"errors"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

// testProjectOperationContext explicitly installs the project boundary required
// by service tests that predate project scoping. It also backfills rows seeded
// by the test before the scope was created; production uses the one-time
// project-scope migration instead.
func testProjectOperationContext(
	t *testing.T,
	db *gorm.DB,
	actor models.ActorRef,
) context.Context {
	t.Helper()
	if err := db.AutoMigrate(
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.ProjectMembership{},
		&models.ProjectPrincipalGrant{},
		&models.Team{},
		&models.Queue{},
		&models.RequestTypeVersion{},
		&models.WorkflowVersion{},
		&models.ConfigurationRelease{},
	); err != nil {
		t.Fatalf("migrate test project scope: %v", err)
	}
	organization := models.Organization{}
	if err := db.Where("slug = ?", "test").
		FirstOrCreate(&organization, models.Organization{
			Slug:   "test",
			Name:   "Test",
			Status: models.OrganizationStatusActive,
		}).Error; err != nil {
		t.Fatalf("create test organization: %v", err)
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
		t.Fatalf("create test business unit: %v", err)
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
		t.Fatalf("create test project: %v", err)
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
		t.Fatalf("create test queue: %v", err)
	}
	for _, scopedModel := range []any{
		&models.Category{},
		&models.TicketComment{},
		&models.TicketAttachment{},
		&models.TicketHistory{},
		&models.TicketLease{},
		&models.PolicyDecision{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.IdempotencyRecord{},
		&models.Notification{},
		&models.WebhookConfig{},
		&models.WebhookLog{},
		&models.AutomationRule{},
		&models.AutomationLog{},
		&models.SLAConfig{},
		&models.TicketTemplate{},
		&models.QuickReply{},
	} {
		if !db.Migrator().HasTable(scopedModel) ||
			!db.Migrator().HasColumn(scopedModel, "organization_id") ||
			!db.Migrator().HasColumn(scopedModel, "project_id") {
			continue
		}
		if err := db.Model(scopedModel).
			Where("organization_id = 0 OR project_id = 0").
			Updates(map[string]any{
				"organization_id": organization.ID,
				"project_id":      project.ID,
			}).Error; err != nil {
			t.Fatalf("backfill test project-owned row %T: %v", scopedModel, err)
		}
	}
	credentialID := ""
	if actor.Type == models.ActorTypeServicePrincipal {
		credentialID = "test-credential"
	}
	ctx, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:        project.Scope(),
			Actor:        actor,
			Source:       sourceProtocolForTestActor(actor),
			CredentialID: credentialID,
		},
	)
	if err != nil {
		t.Fatalf("install test operation context: %v", err)
	}
	configurationService, err := NewProjectConfigurationService(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configurationService.BootstrapProjectConfiguration(ctx); err != nil {
		t.Fatalf("bootstrap test project configuration: %v", err)
	}
	if db.Migrator().HasTable(&models.Ticket{}) {
		var requestType models.RequestTypeVersion
		if err := db.Where(
			"project_id = ? AND key = ? AND status = ?",
			project.ID,
			"request",
			models.ConfigurationStatusPublished,
		).First(&requestType).Error; err != nil {
			t.Fatal(err)
		}
		var workflow models.WorkflowVersion
		if err := db.Where(
			"project_id = ? AND key = ? AND status = ?",
			project.ID,
			"default",
			models.ConfigurationStatusPublished,
		).First(&workflow).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&models.Ticket{}).
			Where("organization_id = 0 OR project_id = 0 OR queue_id = 0").
			Updates(map[string]any{
				"organization_id":         organization.ID,
				"project_id":              project.ID,
				"queue_id":                queue.ID,
				"request_type_version_id": requestType.ID,
				"workflow_version_id":     workflow.ID,
			}).Error; err != nil {
			t.Fatalf("backfill test tickets into project scope: %v", err)
		}
	}
	return ctx
}

func sourceProtocolForTestActor(actor models.ActorRef) SourceProtocol {
	if actor.Type == models.ActorTypeServicePrincipal {
		return SourceProtocolAgentREST
	}
	if actor.Type == models.ActorTypeSystem {
		return SourceProtocolWorker
	}
	return SourceProtocolHumanREST
}

func ensureTestHumanProjectRole(
	t *testing.T,
	db *gorm.DB,
	ctx context.Context,
	userID uint,
	role models.ProjectRole,
) {
	t.Helper()
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var membership models.ProjectMembership
	result := db.Where(
		"project_id = ? AND user_id = ?",
		operation.Scope.ProjectID,
		userID,
	).First(&membership)
	switch {
	case result.Error == nil:
		if err := db.Model(&membership).Updates(map[string]any{
			"role":      role,
			"is_active": true,
		}).Error; err != nil {
			t.Fatalf("update test project membership: %v", err)
		}
	case errors.Is(result.Error, gorm.ErrRecordNotFound):
		if err := db.Create(&models.ProjectMembership{
			ProjectID: operation.Scope.ProjectID,
			UserID:    userID,
			Role:      role,
			IsActive:  true,
			Version:   1,
		}).Error; err != nil {
			t.Fatalf("create test project membership: %v", err)
		}
	default:
		t.Fatalf("load test project membership: %v", result.Error)
	}
}
