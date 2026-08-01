package services

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
)

func TestIntegrationManagementListsAreBoundedStableAndStrict(t *testing.T) {
	fixture := newIntegrationInboxFixture(t)
	service, err := NewIntegrationManagementService(fixture.db, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := integrationManagementTestContext(t, fixture.scope, 81)
	sameTime := time.Date(2026, time.August, 1, 3, 0, 0, 0, time.UTC)
	definitions := make([]models.ConnectorDefinition, 0, 150)
	for index := 0; index < 150; index++ {
		definitions = append(definitions, models.ConnectorDefinition{
			CreatedAt:                  sameTime,
			UpdatedAt:                  sameTime,
			OrganizationID:             fixture.scope.OrganizationID,
			ProjectID:                  fixture.scope.ProjectID,
			Key:                        fmt.Sprintf("bulk-%03d", index),
			Name:                       fmt.Sprintf("批量连接器 %03d", index),
			Kind:                       "webhook",
			Direction:                  models.ConnectorDirectionInbound,
			Status:                     models.ConnectorDefinitionStatusActive,
			SignatureScheme:            "hmac-sha256",
			DefaultReplayWindowSeconds: 300,
			ConfigurationSchema:        datatypes.JSON([]byte(`{"type":"object"}`)),
			MappingSchema:              datatypes.JSON([]byte(`{"type":"object"}`)),
		})
	}
	if err := fixture.db.CreateInBatches(definitions, 50).Error; err != nil {
		t.Fatal(err)
	}

	first, err := service.ListConnectorDefinitions(
		ctx,
		IntegrationListOptions{
			Page:      1,
			PageSize:  100,
			SortBy:    "created_at",
			SortOrder: "desc",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ListConnectorDefinitions(
		ctx,
		IntegrationListOptions{
			Page:      2,
			PageSize:  100,
			SortBy:    "created_at",
			SortOrder: "desc",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 151 || len(first.Items) != 100 ||
		len(second.Items) != 51 {
		t.Fatalf(
			"unexpected pages first=%d second=%d total=%d",
			len(first.Items),
			len(second.Items),
			first.Total,
		)
	}
	seen := make(map[uint]struct{}, 151)
	for _, page := range [][]models.ConnectorDefinition{
		first.Items,
		second.Items,
	} {
		for _, item := range page {
			if _, exists := seen[item.ID]; exists {
				t.Fatalf("duplicate connector id %d across pages", item.ID)
			}
			seen[item.ID] = struct{}{}
		}
	}
	if len(seen) != 151 {
		t.Fatalf("listed %d unique connectors, want 151", len(seen))
	}

	for _, invalid := range []IntegrationListOptions{
		{Page: -1},
		{PageSize: -1},
		{PageSize: 101},
		{SortBy: "configuration_schema"},
		{SortOrder: "sideways"},
		{Search: " leading"},
	} {
		if _, err := service.ListConnectorDefinitions(ctx, invalid); !errors.Is(err, ErrIntegrationManagementInvalidInput) {
			t.Fatalf("invalid options %+v error=%v", invalid, err)
		}
	}
}

func TestIntegrationDomainEventCursorBindsScopeFilterAndLimit(t *testing.T) {
	fixture := newIntegrationInboxFixture(t)
	if err := fixture.db.AutoMigrate(
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatal(err)
	}
	service, err := NewIntegrationManagementService(fixture.db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigureCursorSigningKey(
		[]byte("integration-pagination-test-secret"),
	); err != nil {
		t.Fatal(err)
	}
	ctx := integrationManagementTestContext(t, fixture.scope, 82)
	sameTime := time.Date(2026, time.August, 1, 4, 0, 0, 0, time.UTC)
	events := make([]models.DomainEvent, 0, 151)
	for index := 0; index < 151; index++ {
		events = append(events, models.DomainEvent{
			ID:              uuid.NewString(),
			CreatedAt:       sameTime,
			OrganizationID:  fixture.scope.OrganizationID,
			ProjectID:       fixture.scope.ProjectID,
			SpecVersion:     "1.0",
			Source:          "urn:chronodesk:test",
			Type:            "io.chronodesk.integration.test.v1",
			Subject:         fmt.Sprintf("integration/%03d", index),
			Time:            sameTime,
			DataContentType: "application/json",
			Data:            datatypes.JSON([]byte(`{"secret":"not-listed"}`)),
			ActorType:       models.ActorTypeSystem,
			ActorID:         "integration-test",
			ResourceVersion: 1,
		})
	}
	if err := fixture.db.CreateInBatches(events, 50).Error; err != nil {
		t.Fatal(err)
	}

	first, err := service.ListDomainEvents(
		ctx,
		IntegrationDomainEventCursorOptions{Limit: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 100 || !first.HasMore ||
		first.NextCursor == "" {
		t.Fatalf("first cursor page=%+v", first)
	}
	second, err := service.ListDomainEvents(
		ctx,
		IntegrationDomainEventCursorOptions{
			Cursor: first.NextCursor,
			Limit:  100,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 51 || second.HasMore {
		t.Fatalf("second cursor page=%+v", second)
	}
	seen := make(map[string]struct{}, 151)
	for _, page := range [][]models.DomainEvent{first.Items, second.Items} {
		for _, item := range page {
			if _, exists := seen[item.ID]; exists {
				t.Fatalf("duplicate event %s", item.ID)
			}
			seen[item.ID] = struct{}{}
		}
	}

	for name, options := range map[string]IntegrationDomainEventCursorOptions{
		"limit": {
			Cursor: first.NextCursor,
			Limit:  50,
		},
		"filter": {
			Cursor: first.NextCursor,
			Limit:  100,
			Search: "changed",
		},
		"tamper": {
			Cursor: func() string {
				replacement := "A"
				if first.NextCursor[0] == 'A' {
					replacement = "B"
				}
				return replacement + first.NextCursor[1:]
			}(),
			Limit: 100,
		},
	} {
		if _, err := service.ListDomainEvents(ctx, options); !errors.Is(err, ErrIntegrationListCursorInvalid) {
			t.Fatalf("%s cursor error=%v", name, err)
		}
	}

	otherProject := models.Project{
		OrganizationID: fixture.scope.OrganizationID,
		BusinessUnitID: fixture.project.BusinessUnitID,
		Key:            "IN2",
		Name:           "Integration Other",
		Status:         models.ProjectStatusActive,
	}
	if err := fixture.db.Create(&otherProject).Error; err != nil {
		t.Fatal(err)
	}
	otherContext := integrationManagementTestContext(
		t,
		otherProject.Scope(),
		82,
	)
	if _, err := service.ListDomainEvents(
		otherContext,
		IntegrationDomainEventCursorOptions{
			Cursor: first.NextCursor,
			Limit:  100,
		},
	); !errors.Is(err, ErrIntegrationListCursorInvalid) {
		t.Fatalf("cross-project cursor error=%v", err)
	}
}
