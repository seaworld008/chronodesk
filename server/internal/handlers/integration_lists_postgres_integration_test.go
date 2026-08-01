package handlers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/datatypes"
)

func TestPostgresIntegrationDirectoriesAreStableAcross151Ties(t *testing.T) {
	db := openWebhookStatsPostgresIntegrationDB(t)
	if err := db.AutoMigrate(
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.ConnectorDefinition{},
		&models.Connection{},
		&models.MappingVersion{},
		&models.InboxMessage{},
		&models.InboxReceipt{},
	); err != nil {
		t.Fatal(err)
	}
	organization := models.Organization{
		Slug:   "integration-lists",
		Name:   "Integration Lists",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "integration",
		Name:           "Integration",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	project := models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            "PGI",
		Name:           "PostgreSQL Integration",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, time.August, 1, 6, 0, 0, 0, time.UTC)
	definitions := make([]models.ConnectorDefinition, 0, 151)
	for index := 0; index < 151; index++ {
		definitions = append(definitions, models.ConnectorDefinition{
			CreatedAt:                  createdAt,
			UpdatedAt:                  createdAt,
			OrganizationID:             organization.ID,
			ProjectID:                  project.ID,
			Key:                        fmt.Sprintf("connector-%03d", index),
			Name:                       fmt.Sprintf("Connector %03d", index),
			Kind:                       "webhook",
			Direction:                  models.ConnectorDirectionInbound,
			Status:                     models.ConnectorDefinitionStatusActive,
			SignatureScheme:            "hmac-sha256",
			DefaultReplayWindowSeconds: 300,
			ConfigurationSchema:        datatypes.JSON([]byte(`{"type":"object"}`)),
			MappingSchema:              datatypes.JSON([]byte(`{"type":"object"}`)),
		})
	}
	if err := db.CreateInBatches(definitions, 50).Error; err != nil {
		t.Fatal(err)
	}
	connection := models.Connection{
		OrganizationID:        organization.ID,
		ProjectID:             project.ID,
		ConnectorDefinitionID: definitions[0].ID,
		Key:                   "primary",
		Name:                  "Primary",
		Status:                models.ConnectionStatusActive,
		ReplayWindowSeconds:   300,
		ActorType:             models.ActorTypeSystem,
		ActorID:               "connector:postgres",
	}
	if err := db.Create(&connection).Error; err != nil {
		t.Fatal(err)
	}
	mapping := models.MappingVersion{
		OrganizationID: organization.ID,
		ProjectID:      project.ID,
		ConnectionID:   connection.ID,
		Key:            "tickets",
		Version:        1,
		Status:         models.MappingVersionStatusDraft,
		TargetCommand:  "ticket.create",
		Definition:     datatypes.JSON([]byte(`{"title":"$.title"}`)),
	}
	if err := db.Create(&mapping).Error; err != nil {
		t.Fatal(err)
	}
	messages := make([]models.InboxMessage, 0, 151)
	for index := 0; index < 151; index++ {
		messages = append(messages, models.InboxMessage{
			CreatedAt:            createdAt,
			UpdatedAt:            createdAt,
			OrganizationID:       organization.ID,
			ProjectID:            project.ID,
			ConnectionID:         connection.ID,
			MappingVersionID:     mapping.ID,
			ExternalMessageID:    fmt.Sprintf("message-%03d", index),
			ExternalResourceType: "ticket",
			ExternalResourceID:   fmt.Sprintf("EXT-%03d", index),
			SignedAt:             createdAt,
			ReceivedAt:           createdAt,
			ContentType:          "application/json",
			Payload:              []byte(`{"bounded":true}`),
			PayloadDigest:        fmt.Sprintf("%064d", index),
			SignatureDigest:      fmt.Sprintf("%064d", index),
			Status:               models.InboxMessageStatusCompleted,
			ProcessedAt:          &createdAt,
		})
	}
	if err := db.CreateInBatches(messages, 50).Error; err != nil {
		t.Fatal(err)
	}
	service, err := services.NewIntegrationManagementService(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := services.WithOperationContext(
		context.Background(),
		services.OperationContext{
			Scope:  project.Scope(),
			Actor:  models.HumanActor(91),
			Source: services.SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	firstDefinitions, err := service.ListConnectorDefinitions(
		ctx,
		services.IntegrationListOptions{
			Page:      1,
			PageSize:  100,
			SortBy:    "created_at",
			SortOrder: "desc",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	secondDefinitions, err := service.ListConnectorDefinitions(
		ctx,
		services.IntegrationListOptions{
			Page:      2,
			PageSize:  100,
			SortBy:    "created_at",
			SortOrder: "desc",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	firstMessages, err := service.ListInboxMessages(
		ctx,
		services.IntegrationListOptions{
			Page:      1,
			PageSize:  100,
			SortBy:    "received_at",
			SortOrder: "desc",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	secondMessages, err := service.ListInboxMessages(
		ctx,
		services.IntegrationListOptions{
			Page:      2,
			PageSize:  100,
			SortBy:    "received_at",
			SortOrder: "desc",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstDefinitions.Items) != 100 ||
		len(secondDefinitions.Items) != 51 ||
		firstDefinitions.Items[0].ID != definitions[150].ID ||
		secondDefinitions.Items[50].ID != definitions[0].ID {
		t.Fatalf(
			"connector pages first=%d second=%d",
			len(firstDefinitions.Items),
			len(secondDefinitions.Items),
		)
	}
	if len(firstMessages.Items) != 100 ||
		len(secondMessages.Items) != 51 ||
		firstMessages.Items[0].ID != messages[150].ID ||
		secondMessages.Items[50].ID != messages[0].ID {
		t.Fatalf(
			"inbox pages first=%d second=%d",
			len(firstMessages.Items),
			len(secondMessages.Items),
		)
	}
}
