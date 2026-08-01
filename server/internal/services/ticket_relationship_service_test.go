package services

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
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

	links, err := service.ListEntityLinks(
		ctx,
		source.ID,
		DirectoryPageRequest{
			Page: 1, PageSize: 25, SortBy: "created_at", SortOrder: "desc",
		},
	)
	if err != nil || len(links.Items) != 1 ||
		links.Items[0].ProjectID != operation.Scope.ProjectID {
		t.Fatalf("scoped entity links = %+v, %v", links, err)
	}
	relations, err := service.ListTicketRelations(
		ctx,
		source.ID,
		DirectoryPageRequest{
			Page: 1, PageSize: 25, SortBy: "created_at", SortOrder: "desc",
		},
	)
	if err != nil || len(relations.Items) != 1 ||
		relations.Items[0].Relation.ProjectID != operation.Scope.ProjectID ||
		relations.Items[0].Direction != TicketRelationDirectionOutgoing ||
		relations.Items[0].RelatedTicketID != target.ID ||
		relations.Items[0].RelatedTicketNumber != target.TicketNumber ||
		relations.Items[0].RelatedTicketTitle != target.Title {
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
	if _, err := service.ListEntityLinks(
		context.Background(),
		1,
		DirectoryPageRequest{
			Page: 1, PageSize: 25, SortBy: "created_at", SortOrder: "desc",
		},
	); err == nil {
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

func TestEntityLinkDirectoryIsBoundedAndStableAcrossPages(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.Ticket{},
		&models.EntityLink{},
	); err != nil {
		t.Fatal(err)
	}
	ctx := testProjectOperationContext(t, db, models.HumanActor(17))
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
	ticket := relationshipTestTicket(
		operation.Scope,
		queue.ID,
		"REL-PAGE-1",
	)
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	links := make([]models.EntityLink, 0, 150)
	for index := 0; index < 150; index++ {
		links = append(links, models.EntityLink{
			CreatedAt:      now,
			OrganizationID: operation.Scope.OrganizationID,
			ProjectID:      operation.Scope.ProjectID,
			TicketID:       ticket.ID,
			Kind:           models.EntityKindDevice,
			ReferenceID:    fmt.Sprintf("device-%03d", index),
			DisplayName:    fmt.Sprintf("Device %03d", index),
			Metadata:       datatypes.JSON([]byte(`{}`)),
			CreatedByType:  models.ActorTypeHuman,
			CreatedByID:    "17",
		})
	}
	if err := db.Create(&links).Error; err != nil {
		t.Fatal(err)
	}
	service, err := NewTicketRelationshipService(
		db,
		NewAgentNativeService(db),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := DirectoryPageRequest{
		Page: 1, PageSize: 100, SortBy: "created_at", SortOrder: "desc",
	}
	first, err := service.ListEntityLinks(ctx, ticket.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	request.Page = 2
	second, err := service.ListEntityLinks(ctx, ticket.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 150 || second.Total != 150 ||
		first.TotalPages != 2 || second.TotalPages != 2 ||
		len(first.Items) != 100 || len(second.Items) != 50 {
		t.Fatalf("entity link pages = %+v / %+v", first, second)
	}
	seen := make(map[string]struct{}, 150)
	var previous string
	for _, link := range append(first.Items, second.Items...) {
		if _, duplicate := seen[link.ID]; duplicate {
			t.Fatalf("duplicate entity link %s", link.ID)
		}
		seen[link.ID] = struct{}{}
		if previous != "" && previous <= link.ID {
			t.Fatalf(
				"entity link ID order is not descending: %s then %s",
				previous,
				link.ID,
			)
		}
		previous = link.ID
	}
	for _, invalid := range []DirectoryPageRequest{
		{Page: 0, PageSize: 25, SortBy: "created_at", SortOrder: "desc"},
		{Page: 1, PageSize: 101, SortBy: "created_at", SortOrder: "desc"},
		{
			Page: math.MaxInt, PageSize: 100,
			SortBy: "created_at", SortOrder: "desc",
		},
		{Page: 1, PageSize: 25, SortBy: "ticket_id", SortOrder: "desc"},
	} {
		if _, err := service.ListEntityLinks(
			ctx,
			ticket.ID,
			invalid,
		); !errors.Is(err, ErrDirectoryListQuery) {
			t.Fatalf("invalid relationship page %+v error = %v", invalid, err)
		}
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
