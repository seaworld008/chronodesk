package handlers

import (
	"fmt"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

func TestPostgresAutomationConfigurationDirectoriesAreStableAcross151Ties(
	t *testing.T,
) {
	db := openWebhookStatsPostgresIntegrationDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.SLAConfig{},
		&models.TicketTemplate{},
		&models.QuickReply{},
	); err != nil {
		t.Fatal(err)
	}
	user := postgresListTestUser(t, db, "automation-config-directories")
	createdAt := time.Date(2026, time.August, 1, 8, 0, 0, 0, time.UTC)
	slas := make([]models.SLAConfig, 151)
	templates := make([]models.TicketTemplate, 151)
	replies := make([]models.QuickReply, 151)
	for index := 0; index < 151; index++ {
		slas[index] = models.SLAConfig{
			CreatedAt:       createdAt,
			OrganizationID:  1,
			ProjectID:       10,
			Name:            fmt.Sprintf("postgres-sla-%03d", index),
			IsActive:        true,
			IsDefault:       index == 150,
			ResponseTime:    30,
			ResolutionTime:  240,
			WorkingHours:    `{}`,
			EscalationRules: `[]`,
		}
		templates[index] = models.TicketTemplate{
			CreatedAt:      createdAt,
			OrganizationID: 1,
			ProjectID:      10,
			Name:           fmt.Sprintf("postgres-template-%03d", index),
			Category:       "incident",
			IsActive:       true,
			CreatedBy:      user.ID,
			CustomFields:   `[]`,
		}
		replies[index] = models.QuickReply{
			CreatedAt:      createdAt,
			OrganizationID: 1,
			ProjectID:      10,
			Name:           fmt.Sprintf("postgres-reply-%03d", index),
			Category:       "incident",
			Content:        "literal %_ search",
			IsPublic:       true,
			CreatedBy:      user.ID,
		}
	}
	if err := db.CreateInBatches(&slas, 50).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.CreateInBatches(&templates, 50).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.CreateInBatches(&replies, 50).Error; err != nil {
		t.Fatal(err)
	}
	ctx := postgresListTestContext(t, user.ID, 1, 10)
	service := services.NewAutomationService(db)

	firstSLA, totalSLA, err := service.GetSLAConfigs(ctx, nil, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	secondSLA, secondSLATotal, err := service.GetSLAConfigs(
		ctx,
		nil,
		2,
		100,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstTemplates, totalTemplates, err := service.GetTemplates(
		ctx,
		"incident",
		nil,
		1,
		100,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondTemplates, secondTemplateTotal, err := service.GetTemplates(
		ctx,
		"incident",
		nil,
		2,
		100,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstReplies, totalReplies, err := service.GetQuickReplies(
		ctx,
		"incident",
		"%_",
		nil,
		user.ID,
		1,
		100,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondReplies, secondReplyTotal, err := service.GetQuickReplies(
		ctx,
		"incident",
		"%_",
		nil,
		user.ID,
		2,
		100,
	)
	if err != nil {
		t.Fatal(err)
	}

	if totalSLA != 151 || secondSLATotal != totalSLA ||
		len(firstSLA) != 100 || len(secondSLA) != 51 ||
		firstSLA[0].ID != slas[150].ID ||
		firstSLA[1].ID != slas[149].ID ||
		firstSLA[99].ID != slas[51].ID ||
		secondSLA[0].ID != slas[50].ID ||
		secondSLA[50].ID != slas[0].ID {
		t.Fatalf(
			"unstable PostgreSQL SLA pages: total=%d/%d first=%d second=%d",
			totalSLA,
			secondSLATotal,
			len(firstSLA),
			len(secondSLA),
		)
	}
	if totalTemplates != 151 ||
		secondTemplateTotal != totalTemplates ||
		len(firstTemplates) != 100 ||
		len(secondTemplates) != 51 ||
		firstTemplates[0].ID != templates[150].ID ||
		firstTemplates[99].ID != templates[51].ID ||
		secondTemplates[0].ID != templates[50].ID ||
		secondTemplates[50].ID != templates[0].ID {
		t.Fatalf(
			"unstable PostgreSQL template pages: total=%d/%d first=%d second=%d",
			totalTemplates,
			secondTemplateTotal,
			len(firstTemplates),
			len(secondTemplates),
		)
	}
	if totalReplies != 151 || secondReplyTotal != totalReplies ||
		len(firstReplies) != 100 || len(secondReplies) != 51 ||
		firstReplies[0].ID != replies[150].ID ||
		firstReplies[99].ID != replies[51].ID ||
		secondReplies[0].ID != replies[50].ID ||
		secondReplies[50].ID != replies[0].ID {
		t.Fatalf(
			"unstable PostgreSQL quick-reply pages: total=%d/%d first=%d second=%d",
			totalReplies,
			secondReplyTotal,
			len(firstReplies),
			len(secondReplies),
		)
	}
}
