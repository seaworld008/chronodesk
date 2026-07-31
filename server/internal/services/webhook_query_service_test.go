package services

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

var webhookQueryTestCursorKey = []byte(
	"chronodesk-webhook-query-test-cursor-key-20260731",
)

func TestWebhookQueryServiceStableCursorAndBindings(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.WebhookConfig{},
		&models.WebhookLog{},
	); err != nil {
		t.Fatal(err)
	}
	ctxA := webhookQueryTestContext(t, 7, 11)
	ctxB := webhookQueryTestContext(t, 7, 12)
	configA := models.WebhookConfig{
		OrganizationID: 7,
		ProjectID:      11,
		Name:           "project-a",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     "https://example.test/a",
		Status:         models.WebhookStatusActive,
		CreatedBy:      1,
	}
	configB := models.WebhookConfig{
		OrganizationID: 7,
		ProjectID:      12,
		Name:           "project-b",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     "https://example.test/b",
		Status:         models.WebhookStatusActive,
		CreatedBy:      1,
	}
	if err := db.Create(&configA).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&configB).Error; err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.July, 31, 9, 0, 0, 0, time.UTC)
	logs := make([]models.WebhookLog, 0, 151)
	for index := 0; index < 151; index++ {
		logs = append(logs, models.WebhookLog{
			CreatedAt:      at,
			OrganizationID: 7,
			ProjectID:      11,
			ConfigID:       configA.ID,
			EventType:      models.WebhookEventSystemAlert,
			Status:         "failed",
		})
	}
	if err := db.CreateInBatches(&logs, 50).Error; err != nil {
		t.Fatal(err)
	}

	service := NewWebhookQueryService(db)
	if err := service.ConfigureListCursor(webhookQueryTestCursorKey); err != nil {
		t.Fatal(err)
	}
	first, err := service.ListDeliveries(
		ctxA,
		configA.ID,
		WebhookDeliveryQuery{Limit: 100, Status: "failed"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 100 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first page = %+v", first)
	}
	if first.Items[0].ID != logs[150].ID ||
		first.Items[99].ID != logs[51].ID {
		t.Fatalf(
			"first page bounds = %d..%d, want %d..%d",
			first.Items[0].ID,
			first.Items[99].ID,
			logs[150].ID,
			logs[51].ID,
		)
	}
	second, err := service.ListDeliveries(
		ctxA,
		configA.ID,
		WebhookDeliveryQuery{
			Limit:  100,
			Status: "failed",
			Cursor: first.NextCursor,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 51 || second.HasMore ||
		second.NextCursor != "" ||
		second.Items[0].ID != logs[50].ID ||
		second.Items[50].ID != logs[0].ID {
		t.Fatalf("second page = %+v", second)
	}

	tampered := first.NextCursor[:len(first.NextCursor)-1] + "A"
	if tampered == first.NextCursor {
		tampered = first.NextCursor[:len(first.NextCursor)-1] + "B"
	}
	cases := []struct {
		name     string
		ctx      context.Context
		configID uint
		query    WebhookDeliveryQuery
	}{
		{
			name:     "tampered",
			ctx:      ctxA,
			configID: configA.ID,
			query: WebhookDeliveryQuery{
				Limit: 100, Status: "failed", Cursor: tampered,
			},
		},
		{
			name:     "filter changed",
			ctx:      ctxA,
			configID: configA.ID,
			query: WebhookDeliveryQuery{
				Limit: 100, Status: "success", Cursor: first.NextCursor,
			},
		},
		{
			name:     "cross project",
			ctx:      ctxB,
			configID: configB.ID,
			query: WebhookDeliveryQuery{
				Limit: 100, Status: "failed", Cursor: first.NextCursor,
			},
		},
		{
			name:     "limit changed",
			ctx:      ctxA,
			configID: configA.ID,
			query: WebhookDeliveryQuery{
				Limit: 25, Status: "failed", Cursor: first.NextCursor,
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.ListDeliveries(
				test.ctx,
				test.configID,
				test.query,
			); !errors.Is(err, ErrInvalidWebhookListCursor) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestWebhookQueryServiceDefinitionPageAndFailClosedCursor(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.WebhookConfig{},
		&models.WebhookLog{},
	); err != nil {
		t.Fatal(err)
	}
	ctx := webhookQueryTestContext(t, 3, 4)
	createdAt := time.Date(2026, time.July, 31, 10, 0, 0, 0, time.UTC)
	configs := []models.WebhookConfig{
		{
			CreatedAt:      createdAt,
			OrganizationID: 3,
			ProjectID:      4,
			Name:           "first",
			Provider:       models.WebhookProviderCustom,
			WebhookURL:     "https://example.test/first",
			Status:         models.WebhookStatusActive,
			CreatedBy:      1,
		},
		{
			CreatedAt:      createdAt,
			OrganizationID: 3,
			ProjectID:      4,
			Name:           "second",
			Provider:       models.WebhookProviderCustom,
			WebhookURL:     "https://example.test/second",
			Status:         models.WebhookStatusActive,
			CreatedBy:      1,
		},
	}
	if err := db.Create(&configs).Error; err != nil {
		t.Fatal(err)
	}
	service := NewWebhookQueryService(db)
	page, err := service.ListDefinitions(ctx, WebhookDefinitionQuery{
		Page:     1,
		PageSize: 25,
		Provider: models.WebhookProviderCustom,
		Status:   models.WebhookStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || page.TotalPages != 1 ||
		len(page.Items) != 2 ||
		page.Items[0].ID != configs[1].ID ||
		page.Items[1].ID != configs[0].ID {
		t.Fatalf("page = %+v", page)
	}
	if _, err := service.ListDeliveries(
		ctx,
		configs[0].ID,
		WebhookDeliveryQuery{Limit: 25},
	); !errors.Is(err, ErrWebhookListCursorKey) {
		t.Fatalf("unconfigured cursor error = %v", err)
	}
}

func webhookQueryTestContext(
	t *testing.T,
	organizationID uint,
	projectID uint,
) context.Context {
	t.Helper()
	ctx, err := WithOperationContext(context.Background(), OperationContext{
		Scope: models.ProjectScope{
			OrganizationID: organizationID,
			ProjectID:      projectID,
		},
		Actor:  models.HumanActor(1),
		Source: SourceProtocolHumanREST,
	})
	if err != nil {
		t.Fatal(fmt.Errorf("build webhook query test context: %w", err))
	}
	return ctx
}
