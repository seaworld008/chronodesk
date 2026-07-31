package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/gorm"
)

func TestPostgresWebhookDeliveryCursorIsStableAcross151Ties(t *testing.T) {
	db := openWebhookStatsPostgresIntegrationDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.WebhookConfig{},
		&models.WebhookLog{},
	); err != nil {
		t.Fatal(err)
	}
	user := postgresListTestUser(t, db, "webhook-list")
	config := models.WebhookConfig{
		OrganizationID: 1,
		ProjectID:      10,
		Name:           "PostgreSQL cursor webhook",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     "https://example.invalid/cursor",
		Status:         models.WebhookStatusActive,
		CreatedBy:      user.ID,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	logs := make([]models.WebhookLog, 0, 151)
	for index := 0; index < 151; index++ {
		logs = append(logs, models.WebhookLog{
			CreatedAt:      createdAt,
			OrganizationID: 1,
			ProjectID:      10,
			ConfigID:       config.ID,
			EventType:      models.WebhookEventSystemAlert,
			Status:         "failed",
		})
	}
	if err := db.CreateInBatches(&logs, 50).Error; err != nil {
		t.Fatal(err)
	}
	service := services.NewWebhookQueryService(db)
	if err := service.ConfigureListCursor(
		[]byte("postgres-webhook-list-cursor-key-20260731"),
	); err != nil {
		t.Fatal(err)
	}
	ctx := postgresListTestContext(t, user.ID, 1, 10)
	first, err := service.ListDeliveries(
		ctx,
		config.ID,
		services.WebhookDeliveryQuery{Limit: 100, Status: "failed"},
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ListDeliveries(
		ctx,
		config.ID,
		services.WebhookDeliveryQuery{
			Limit: 100, Status: "failed", Cursor: first.NextCursor,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 100 || len(second.Items) != 51 ||
		first.Items[0].ID != logs[150].ID ||
		first.Items[99].ID != logs[51].ID ||
		second.Items[0].ID != logs[50].ID ||
		second.Items[50].ID != logs[0].ID {
		t.Fatalf("unstable PostgreSQL pages: first=%d second=%d", len(first.Items), len(second.Items))
	}
	tampered := first.NextCursor[:len(first.NextCursor)-1] + "A"
	if tampered == first.NextCursor {
		tampered = first.NextCursor[:len(first.NextCursor)-1] + "B"
	}
	if _, err := service.ListDeliveries(
		ctx,
		config.ID,
		services.WebhookDeliveryQuery{
			Limit: 100, Status: "failed", Cursor: tampered,
		},
	); !errors.Is(err, services.ErrInvalidWebhookListCursor) {
		t.Fatalf("tampered cursor error = %v", err)
	}
}

func TestPostgresAutomationLogCursorIsStableAcross150Ties(t *testing.T) {
	db := openWebhookStatsPostgresIntegrationDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.Ticket{},
		&models.AutomationRule{},
		&models.AutomationLog{},
	); err != nil {
		t.Fatal(err)
	}
	user := postgresListTestUser(t, db, "automation-list")
	rule := models.AutomationRule{
		OrganizationID: 1,
		ProjectID:      10,
		Name:           "PostgreSQL cursor rule",
		RuleType:       "assignment",
		TriggerEvent:   "io.chronodesk.ticket.created.v1",
		CreatedBy:      user.ID,
	}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		OrganizationID:       1,
		ProjectID:            10,
		QueueID:              1,
		RequestTypeVersionID: "request-type-test",
		WorkflowVersionID:    "workflow-test",
		TicketNumber:         "PG-LIST-1",
		Title:                "PostgreSQL list cursor",
		Description:          "cursor fixture",
		Type:                 models.TicketTypeRequest,
		Priority:             models.TicketPriorityNormal,
		Status:               models.TicketStatusOpen,
		Source:               models.TicketSourceWeb,
		CreatedByID:          &user.ID,
		CreatedByActorType:   models.ActorTypeHuman,
		CreatedByActorID:     "1",
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	executedAt := time.Date(2026, time.July, 31, 13, 0, 0, 0, time.UTC)
	logs := make([]models.AutomationLog, 0, 150)
	for index := 0; index < 150; index++ {
		logs = append(logs, models.AutomationLog{
			OrganizationID: 1,
			ProjectID:      10,
			RuleID:         rule.ID,
			TicketID:       ticket.ID,
			TriggerEvent:   rule.TriggerEvent,
			ExecutedAt:     executedAt,
			Success:        true,
		})
	}
	if err := db.CreateInBatches(&logs, 50).Error; err != nil {
		t.Fatal(err)
	}
	service := services.NewAutomationService(db)
	if err := service.ConfigureListCursor(
		[]byte("postgres-automation-list-cursor-key-20260731"),
	); err != nil {
		t.Fatal(err)
	}
	ctx := postgresListTestContext(t, user.ID, 1, 10)
	success := true
	first, err := service.ListExecutionLogs(
		ctx,
		services.AutomationExecutionLogQuery{Limit: 100, Success: &success},
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ListExecutionLogs(
		ctx,
		services.AutomationExecutionLogQuery{
			Limit: 100, Success: &success, Cursor: first.NextCursor,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 100 || len(second.Items) != 50 ||
		first.Items[0].ID != logs[149].ID ||
		first.Items[99].ID != logs[50].ID ||
		second.Items[0].ID != logs[49].ID ||
		second.Items[49].ID != logs[0].ID {
		t.Fatalf("unstable PostgreSQL pages: first=%d second=%d", len(first.Items), len(second.Items))
	}
	tampered := first.NextCursor[:len(first.NextCursor)-1] + "A"
	if tampered == first.NextCursor {
		tampered = first.NextCursor[:len(first.NextCursor)-1] + "B"
	}
	if _, err := service.ListExecutionLogs(
		ctx,
		services.AutomationExecutionLogQuery{
			Limit: 100, Success: &success, Cursor: tampered,
		},
	); !errors.Is(err, services.ErrInvalidAutomationListCursor) {
		t.Fatalf("tampered cursor error = %v", err)
	}
}

func postgresListTestUser(
	t *testing.T,
	db *gorm.DB,
	suffix string,
) models.User {
	t.Helper()
	user := models.User{
		Username:     "postgres-" + suffix,
		Email:        "postgres-" + suffix + "@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	return user
}

func postgresListTestContext(
	t *testing.T,
	userID uint,
	organizationID uint,
	projectID uint,
) context.Context {
	t.Helper()
	ctx, err := services.WithOperationContext(
		context.Background(),
		services.OperationContext{
			Scope: models.ProjectScope{
				OrganizationID: organizationID,
				ProjectID:      projectID,
			},
			Actor:  models.HumanActor(userID),
			Source: services.SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}
