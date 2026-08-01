package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/gorm"
)

func TestPostgresAutomationAndWebhookDirectoriesAreStableAcross151Ties(
	t *testing.T,
) {
	db := openWebhookStatsPostgresIntegrationDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.AutomationRule{},
		&models.WebhookConfig{},
	); err != nil {
		t.Fatal(err)
	}
	user := postgresListTestUser(t, db, "directory-lists")
	createdAt := time.Date(2026, time.July, 31, 11, 30, 0, 0, time.UTC)
	rules := make([]models.AutomationRule, 0, 151)
	configs := make([]models.WebhookConfig, 0, 151)
	for index := 0; index < 151; index++ {
		rules = append(rules, models.AutomationRule{
			CreatedAt:      createdAt,
			OrganizationID: 1,
			ProjectID:      10,
			Name:           fmt.Sprintf("postgres-rule-%03d", index),
			RuleType:       "assignment",
			Priority:       10,
			TriggerEvent:   "io.chronodesk.ticket.created.v1",
			Conditions:     "[]",
			Actions:        "[]",
			CreatedBy:      user.ID,
		})
		configs = append(configs, models.WebhookConfig{
			CreatedAt:      createdAt,
			OrganizationID: 1,
			ProjectID:      10,
			Name:           fmt.Sprintf("postgres-webhook-%03d", index),
			Provider:       models.WebhookProviderCustom,
			WebhookURL:     "https://example.invalid/callback",
			Status:         models.WebhookStatusActive,
			CreatedBy:      user.ID,
		})
	}
	if err := db.CreateInBatches(&rules, 50).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.CreateInBatches(&configs, 50).Error; err != nil {
		t.Fatal(err)
	}
	ctx := postgresListTestContext(t, user.ID, 1, 10)
	automation := services.NewAutomationService(db)
	firstRules, totalRules, err := automation.GetRules(
		ctx,
		"assignment",
		"",
		nil,
		"",
		1,
		100,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondRules, secondRuleTotal, err := automation.GetRules(
		ctx,
		"assignment",
		"",
		nil,
		"",
		2,
		100,
	)
	if err != nil {
		t.Fatal(err)
	}
	webhooks := services.NewWebhookQueryService(db)
	firstConfigs, err := webhooks.ListDefinitions(
		ctx,
		services.WebhookDefinitionQuery{
			Page:     1,
			PageSize: 100,
			Provider: models.WebhookProviderCustom,
			Status:   models.WebhookStatusActive,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	secondConfigs, err := webhooks.ListDefinitions(
		ctx,
		services.WebhookDefinitionQuery{
			Page:     2,
			PageSize: 100,
			Provider: models.WebhookProviderCustom,
			Status:   models.WebhookStatusActive,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if totalRules != 151 || secondRuleTotal != totalRules ||
		len(firstRules) != 100 || len(secondRules) != 51 ||
		firstRules[0].ID != rules[150].ID ||
		firstRules[99].ID != rules[51].ID ||
		secondRules[0].ID != rules[50].ID ||
		secondRules[50].ID != rules[0].ID {
		t.Fatalf(
			"unstable PostgreSQL automation directory: total=%d/%d first=%d second=%d",
			totalRules,
			secondRuleTotal,
			len(firstRules),
			len(secondRules),
		)
	}
	if firstConfigs.Total != 151 || secondConfigs.Total != 151 ||
		len(firstConfigs.Items) != 100 || len(secondConfigs.Items) != 51 ||
		firstConfigs.Items[0].ID != configs[150].ID ||
		firstConfigs.Items[99].ID != configs[51].ID ||
		secondConfigs.Items[0].ID != configs[50].ID ||
		secondConfigs.Items[50].ID != configs[0].ID {
		t.Fatalf(
			"unstable PostgreSQL Webhook directory: first=%d second=%d",
			len(firstConfigs.Items),
			len(secondConfigs.Items),
		)
	}
	for model, index := range map[any]string{
		&models.AutomationRule{}: "idx_automation_rules_directory",
		&models.WebhookConfig{}:  "idx_webhook_configs_directory",
	} {
		if !db.Migrator().HasIndex(model, index) {
			t.Fatalf("PostgreSQL directory index %q is missing", index)
		}
	}
	assertPostgresListIndexOrder(
		t,
		db,
		"idx_automation_rules_directory",
		"organization_id",
		"project_id",
		"deleted_at",
		"priority",
		"created_at desc",
		"id desc",
	)
	assertPostgresListIndexOrder(
		t,
		db,
		"idx_webhook_configs_directory",
		"organization_id",
		"project_id",
		"deleted_at",
		"created_at desc",
		"id desc",
	)
}

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
	concurrentLog := models.WebhookLog{
		CreatedAt:      createdAt,
		OrganizationID: 1,
		ProjectID:      10,
		ConfigID:       config.ID,
		EventType:      models.WebhookEventSystemAlert,
		Status:         "failed",
	}
	if err := db.Create(&concurrentLog).Error; err != nil {
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
	for _, item := range second.Items {
		if item.ID == concurrentLog.ID {
			t.Fatalf(
				"concurrent Webhook insert %d leaked into continuation",
				concurrentLog.ID,
			)
		}
	}
	for _, index := range []string{
		"idx_webhook_logs_timeline",
		"idx_webhook_logs_status_timeline",
		"idx_webhook_logs_event_timeline",
	} {
		if !db.Migrator().HasIndex(&models.WebhookLog{}, index) {
			t.Fatalf("PostgreSQL Webhook timeline index %q is missing", index)
		}
	}
	assertPostgresListIndexOrder(
		t,
		db,
		"idx_webhook_logs_status_timeline",
		"organization_id",
		"project_id",
		"config_id",
		"status",
		"created_at desc",
		"id desc",
	)
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
		Conditions:     "[]",
		Actions:        "[]",
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
			OrganizationID:  1,
			ProjectID:       10,
			RuleID:          rule.ID,
			TicketID:        ticket.ID,
			TriggerEvent:    rule.TriggerEvent,
			ExecutedAt:      executedAt,
			Success:         true,
			ActionsExecuted: "[]",
			Changes:         "{}",
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
	concurrentLog := models.AutomationLog{
		OrganizationID:  1,
		ProjectID:       10,
		RuleID:          rule.ID,
		TicketID:        ticket.ID,
		TriggerEvent:    rule.TriggerEvent,
		ExecutedAt:      executedAt,
		Success:         true,
		ActionsExecuted: "[]",
		Changes:         "{}",
	}
	if err := db.Create(&concurrentLog).Error; err != nil {
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
	for _, item := range second.Items {
		if item.ID == concurrentLog.ID {
			t.Fatalf(
				"concurrent automation insert %d leaked into continuation",
				concurrentLog.ID,
			)
		}
	}
	for _, index := range []string{
		"idx_automation_logs_timeline",
		"idx_automation_logs_rule_timeline",
		"idx_automation_logs_ticket_timeline",
		"idx_automation_logs_success_timeline",
	} {
		if !db.Migrator().HasIndex(&models.AutomationLog{}, index) {
			t.Fatalf(
				"PostgreSQL automation timeline index %q is missing",
				index,
			)
		}
	}
	assertPostgresListIndexOrder(
		t,
		db,
		"idx_automation_logs_success_timeline",
		"organization_id",
		"project_id",
		"success",
		"executed_at desc",
		"id desc",
	)
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

func assertPostgresListIndexOrder(
	t *testing.T,
	db *gorm.DB,
	indexName string,
	columns ...string,
) {
	t.Helper()
	var definition string
	if err := db.Raw(
		`SELECT indexdef
		 FROM pg_indexes
		 WHERE schemaname = current_schema()
		   AND indexname = ?`,
		indexName,
	).Scan(&definition).Error; err != nil {
		t.Fatalf("read PostgreSQL index %q: %v", indexName, err)
	}
	normalized := strings.ToLower(strings.ReplaceAll(definition, `"`, ""))
	offset := 0
	for _, column := range columns {
		position := strings.Index(normalized[offset:], column)
		if position < 0 {
			t.Fatalf(
				"PostgreSQL index %q is not ordered by %q after offset %d: %s",
				indexName,
				column,
				offset,
				definition,
			)
		}
		offset += position + len(column)
	}
}
