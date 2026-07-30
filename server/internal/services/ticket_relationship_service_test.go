package services

import (
	"context"
	"errors"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

func TestTicketRelationshipsAreVersionedAuditedAndProjectScoped(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.Ticket{},
		&models.EntityLink{},
		&models.TicketRelation{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatal(err)
	}
	ctx := testProjectOperationContext(t, db, models.HumanActor(7))
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var queue models.Queue
	if err := db.Where(
		"project_id = ? AND is_default = ?",
		operation.Scope.ProjectID,
		true,
	).First(&queue).Error; err != nil {
		t.Fatal(err)
	}
	source := relationshipTestTicket(
		operation.Scope,
		queue.ID,
		"TEST-101",
	)
	target := relationshipTestTicket(
		operation.Scope,
		queue.ID,
		"TEST-102",
	)
	if err := db.Create(&source).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	service, err := NewTicketRelationshipService(
		db,
		NewAgentNativeService(db),
	)
	if err != nil {
		t.Fatal(err)
	}

	entity, err := service.AddEntityLink(ctx, AddEntityLinkInput{
		TicketID:        source.ID,
		ExpectedVersion: 1,
		Kind:            models.EntityKindDevice,
		ReferenceID:     "cmdb/device-42",
		DisplayName:     "数据库主机 42",
		Metadata:        map[string]any{"serial": "SN-42"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if entity.TicketVersion != 2 || entity.EventID == "" {
		t.Fatalf("unexpected entity link result: %+v", entity)
	}
	if _, err := service.AddEntityLink(ctx, AddEntityLinkInput{
		TicketID:        source.ID,
		ExpectedVersion: 1,
		Kind:            models.EntityKindDevice,
		ReferenceID:     "cmdb/device-stale",
		DisplayName:     "stale",
	}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale version error = %v", err)
	}

	created, err := service.AddTicketRelation(ctx, AddTicketRelationInput{
		SourceTicketID:  source.ID,
		TargetTicketID:  target.ID,
		ExpectedVersion: 2,
		Relation:        models.TicketRelationBlocks,
		Reason:          "等待变更",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.TicketVersion != 3 || created.EventID == "" {
		t.Fatalf("unexpected relation result: %+v", created)
	}
	var event models.DomainEvent
	if err := db.First(&event, "id = ?", created.EventID).Error; err != nil {
		t.Fatal(err)
	}
	if event.OrganizationID != operation.Scope.OrganizationID ||
		event.ProjectID != operation.Scope.ProjectID ||
		event.ResourceVersion != 3 {
		t.Fatalf("event scope/version = %+v", event)
	}

	otherProject, otherQueue := createRelationshipTestProject(
		t,
		db,
		operation.Scope.OrganizationID,
	)
	foreign := relationshipTestTicket(
		otherProject.Scope(),
		otherQueue.ID,
		"OTHER-1",
	)
	if err := db.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.AddTicketRelation(ctx, AddTicketRelationInput{
		SourceTicketID:  source.ID,
		TargetTicketID:  foreign.ID,
		ExpectedVersion: 3,
		Relation:        models.TicketRelationCollaboratesWith,
	}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-project relation error = %v", err)
	}
	var unchanged models.Ticket
	if err := db.First(&unchanged, source.ID).Error; err != nil {
		t.Fatal(err)
	}
	if unchanged.Version != 3 {
		t.Fatalf("failed cross-project relation changed version to %d", unchanged.Version)
	}

	links, err := service.ListEntityLinks(ctx, source.ID)
	if err != nil || len(links) != 1 || links[0].ProjectID != operation.Scope.ProjectID {
		t.Fatalf("scoped entity links = %+v, %v", links, err)
	}
	relations, err := service.ListTicketRelations(ctx, source.ID)
	if err != nil || len(relations) != 1 ||
		relations[0].ProjectID != operation.Scope.ProjectID {
		t.Fatalf("scoped ticket relations = %+v, %v", relations, err)
	}
}

func TestTicketRelationshipServiceRejectsMissingTrustedScope(t *testing.T) {
	db := openTestDB(t)
	service, err := NewTicketRelationshipService(
		db,
		NewAgentNativeService(db),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListEntityLinks(context.Background(), 1); err == nil {
		t.Fatal("unscoped entity link query was accepted")
	}
	if _, err := service.AddTicketRelation(
		context.Background(),
		AddTicketRelationInput{
			SourceTicketID:  1,
			TargetTicketID:  2,
			ExpectedVersion: 1,
			Relation:        models.TicketRelationBlocks,
		},
	); err == nil {
		t.Fatal("unscoped relation command was accepted")
	}
}

func relationshipTestTicket(
	scope models.ProjectScope,
	queueID uint,
	number string,
) models.Ticket {
	return models.Ticket{
		OrganizationID:       scope.OrganizationID,
		ProjectID:            scope.ProjectID,
		QueueID:              queueID,
		RequestTypeVersionID: defaultRequestTypeRequestVersionID,
		WorkflowVersionID:    defaultWorkflowVersionID,
		TicketNumber:         number,
		Title:                number,
		Description:          "relationship test",
		Type:                 models.TicketTypeRequest,
		Priority:             models.TicketPriorityNormal,
		Status:               models.TicketStatusOpen,
		Source:               models.TicketSourceWeb,
		Version:              1,
		CreatedByActorType:   models.ActorTypeHuman,
		CreatedByActorID:     "7",
	}
}

func createRelationshipTestProject(
	t *testing.T,
	db *gorm.DB,
	organizationID uint,
) (models.Project, models.Queue) {
	t.Helper()
	var unit models.BusinessUnit
	if err := db.Where("organization_id = ?", organizationID).
		First(&unit).Error; err != nil {
		t.Fatal(err)
	}
	project := models.Project{
		OrganizationID: organizationID,
		BusinessUnitID: unit.ID,
		Key:            models.ProjectKey("OTHER"),
		Name:           "Other",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	queue := models.Queue{
		ProjectID: project.ID,
		Key:       models.QueueKey("default"),
		Name:      "Default",
		Status:    models.QueueStatusActive,
		IsDefault: true,
	}
	if err := db.Create(&queue).Error; err != nil {
		t.Fatal(err)
	}
	return project, queue
}
