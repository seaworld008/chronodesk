package services

import (
	"context"
	"strconv"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

func notificationTestOperationContext(
	t *testing.T,
	scope models.ProjectScope,
	actor models.ActorRef,
) context.Context {
	t.Helper()
	ctx, err := WithOperationContext(context.Background(), OperationContext{
		Scope:  scope,
		Actor:  actor,
		Source: SourceProtocolHumanREST,
	})
	if actor.Type == models.ActorTypeSystem {
		ctx, err = WithOperationContext(context.Background(), OperationContext{
			Scope:  scope,
			Actor:  actor,
			Source: SourceProtocolWorker,
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func seedNotificationProjectMembership(
	t *testing.T,
	db *gorm.DB,
	userID uint,
) models.ProjectScope {
	t.Helper()
	if err := db.AutoMigrate(
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.ProjectMembership{},
	); err != nil {
		t.Fatal(err)
	}
	organization := models.Organization{
		Slug:   "notification-" + strconv.FormatUint(uint64(userID), 10),
		Name:   "Notification Test",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "NOTIFY",
		Name:           "Notification",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	project := models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            "NOTIFY",
		Name:           "Notification",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	membership := models.ProjectMembership{
		ProjectID: project.ID,
		UserID:    userID,
		Role:      models.ProjectRoleRequester,
		IsActive:  true,
		Version:   1,
	}
	if err := db.Create(&membership).Error; err != nil {
		t.Fatal(err)
	}
	return project.Scope()
}
