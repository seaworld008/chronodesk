package services

import (
	"context"
	"errors"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

func TestBootstrapProjectAdministratorGrantIsAtomicAuditedAndIdempotent(
	t *testing.T,
) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.ProjectMembership{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.WebhookConfig{},
		&models.WebhookDeliverySnapshot{},
		&models.AuditChainHead{},
		&models.AuditLedgerEntry{},
	); err != nil {
		t.Fatal(err)
	}
	_, _, project, administrator := seedProjectAccessFixture(t, db)
	administrator.PlatformRole = models.PlatformRolePlatformAdmin
	if err := db.Model(&administrator).Update(
		"platform_role",
		models.PlatformRolePlatformAdmin,
	).Error; err != nil {
		t.Fatal(err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		err := db.Transaction(func(tx *gorm.DB) error {
			return EnsureBootstrapProjectAdministratorMembership(
				context.Background(),
				tx,
				administrator,
				project.Scope(),
			)
		})
		if err != nil {
			t.Fatalf("bootstrap attempt %d: %v", attempt, err)
		}
	}

	var membership models.ProjectMembership
	if err := db.Where(
		"project_id = ? AND user_id = ?",
		project.ID,
		administrator.ID,
	).First(&membership).Error; err != nil {
		t.Fatal(err)
	}
	if membership.Role != models.ProjectRoleAdmin ||
		!membership.IsActive ||
		membership.Version != 1 {
		t.Fatalf("unexpected bootstrap membership: %+v", membership)
	}

	var events []models.DomainEvent
	if err := db.Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	var deliveries []models.OutboxDelivery
	if err := db.Find(&deliveries).Error; err != nil {
		t.Fatal(err)
	}
	var auditEntries []models.AuditLedgerEntry
	if err := db.Find(&auditEntries).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 ||
		events[0].Type != "io.chronodesk.project.membership.upserted.v1" ||
		events[0].ActorType != models.ActorTypeSystem ||
		events[0].ActorID != bootstrapProjectAdministratorActor ||
		events[0].OrganizationID != project.OrganizationID ||
		events[0].ProjectID != project.ID {
		t.Fatalf("unexpected bootstrap events: %+v", events)
	}
	if len(deliveries) != 1 ||
		deliveries[0].EventID != events[0].ID ||
		deliveries[0].DestinationType != "event_stream" ||
		deliveries[0].Status != models.OutboxDeliveryPending {
		t.Fatalf("unexpected bootstrap deliveries: %+v", deliveries)
	}
	if len(auditEntries) != 1 ||
		auditEntries[0].DomainEventID != events[0].ID ||
		auditEntries[0].Actor() != models.SystemActor(bootstrapProjectAdministratorActor) ||
		auditEntries[0].Outcome != models.AuditLedgerOutcomeSucceeded {
		t.Fatalf("unexpected bootstrap audit entries: %+v", auditEntries)
	}
}

func TestBootstrapProjectAdministratorGrantRejectsExistingDifferentRole(
	t *testing.T,
) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.ProjectMembership{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.WebhookConfig{},
		&models.WebhookDeliverySnapshot{},
		&models.AuditChainHead{},
		&models.AuditLedgerEntry{},
	); err != nil {
		t.Fatal(err)
	}
	_, _, project, administrator := seedProjectAccessFixture(t, db)
	administrator.PlatformRole = models.PlatformRolePlatformAdmin
	if err := db.Model(&administrator).Update(
		"platform_role",
		models.PlatformRolePlatformAdmin,
	).Error; err != nil {
		t.Fatal(err)
	}
	membership := models.ProjectMembership{
		ProjectID: project.ID,
		UserID:    administrator.ID,
		Role:      models.ProjectRoleManager,
		IsActive:  true,
		Version:   1,
	}
	if err := db.Create(&membership).Error; err != nil {
		t.Fatal(err)
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		return EnsureBootstrapProjectAdministratorMembership(
			context.Background(),
			tx,
			administrator,
			project.Scope(),
		)
	})
	if !errors.Is(err, ErrProjectMembershipConflict) {
		t.Fatalf("bootstrap conflict error = %v, want membership conflict", err)
	}

	var persisted models.ProjectMembership
	if err := db.First(&persisted, membership.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Role != models.ProjectRoleManager ||
		!persisted.IsActive ||
		persisted.Version != 1 {
		t.Fatalf("conflicting membership changed: %+v", persisted)
	}
	for _, assertion := range []struct {
		name  string
		model any
	}{
		{name: "domain events", model: &models.DomainEvent{}},
		{name: "outbox deliveries", model: &models.OutboxDelivery{}},
		{name: "audit ledger entries", model: &models.AuditLedgerEntry{}},
	} {
		var count int64
		if err := db.Model(assertion.model).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s persisted after conflict: %d", assertion.name, count)
		}
	}
}
