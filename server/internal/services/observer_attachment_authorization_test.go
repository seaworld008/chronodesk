package services

import (
	"context"
	"errors"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestObserverAttachmentAuthorizationRequiresPublicRead(t *testing.T) {
	scope := models.ProjectScope{OrganizationID: 7, ProjectID: 11}
	access := &ProjectAccess{
		Scope: scope,
		Role:  models.ProjectRoleObserver,
	}
	operation := OperationContext{
		Scope:  scope,
		Actor:  models.HumanActor(42),
		Source: SourceProtocolHumanREST,
	}
	ticket := models.Ticket{
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
	}

	if err := authorizeHumanAttachmentTicket(
		access,
		operation,
		ticket,
		false,
		true,
	); err != nil {
		t.Fatalf("observer public attachment read error = %v", err)
	}
	for _, testCase := range []struct {
		name     string
		write    bool
		isPublic bool
	}{
		{name: "private read", write: false, isPublic: false},
		{name: "public write", write: true, isPublic: true},
		{name: "private write", write: true, isPublic: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := authorizeHumanAttachmentTicket(
				access,
				operation,
				ticket,
				testCase.write,
				testCase.isPublic,
			)
			if !errors.Is(err, ErrProjectAccessDenied) {
				t.Fatalf("observer attachment authorization error = %v", err)
			}
		})
	}
}

func TestObserverAttachmentDownloadTargetsAreIndistinguishable(
	t *testing.T,
) {
	db := openAgentNativeTestDB(t)
	observer := seedActorUser(t, db, "observer-attachment-oracle")
	operationContext := testProjectOperationContext(
		t,
		db,
		models.HumanActor(observer.ID),
	)
	ensureTestHumanProjectRole(
		t,
		db,
		operationContext,
		observer.ID,
		models.ProjectRoleObserver,
	)
	operation, err := OperationContextFromContext(operationContext)
	if err != nil {
		t.Fatal(err)
	}
	ticket := seedNativeTicket(
		t,
		db,
		observer.ID,
		"OBSERVER-ATTACHMENT-ORACLE",
	)
	otherTicket := seedNativeTicket(
		t,
		db,
		observer.ID,
		"OBSERVER-ATTACHMENT-OTHER-TICKET",
	)
	attachments := []models.TicketAttachment{
		{
			OrganizationID: operation.Scope.OrganizationID,
			ProjectID:      operation.Scope.ProjectID,
			TicketID:       ticket.ID,
			ActorType:      models.ActorTypeHuman,
			ActorID:        models.HumanActor(observer.ID).ID,
			FileName:       "observer-private-clean.txt",
			OriginalName:   "observer-private-clean.txt",
			FileSize:       7,
			MimeType:       "text/plain",
			FileType:       models.AttachmentTypeDocument,
			StoragePath:    "observer-private-clean.txt",
			StorageType:    "local",
			Hash:           "observer-private-clean-hash",
			IsPublic:       false,
			VirusScan:      models.VirusScanClean,
		},
		{
			OrganizationID: operation.Scope.OrganizationID,
			ProjectID:      operation.Scope.ProjectID,
			TicketID:       otherTicket.ID,
			ActorType:      models.ActorTypeHuman,
			ActorID:        models.HumanActor(observer.ID).ID,
			FileName:       "observer-other-ticket-public.txt",
			OriginalName:   "observer-other-ticket-public.txt",
			FileSize:       7,
			MimeType:       "text/plain",
			FileType:       models.AttachmentTypeDocument,
			StoragePath:    "observer-other-ticket-public.txt",
			StorageType:    "local",
			Hash:           "observer-other-ticket-public-hash",
			IsPublic:       true,
			VirusScan:      models.VirusScanClean,
		},
	}
	if err := db.Create(&attachments).Error; err != nil {
		t.Fatal(err)
	}
	service := NewAgentNativeService(db)
	access := &ProjectAccess{
		Scope: operation.Scope,
		Role:  models.ProjectRoleObserver,
	}
	targets := []struct {
		name         string
		ticketID     uint
		attachmentID uint
	}{
		{
			name:         "existing private attachment on readable ticket",
			ticketID:     ticket.ID,
			attachmentID: attachments[0].ID,
		},
		{
			name:         "random nonexistent attachment",
			ticketID:     ticket.ID,
			attachmentID: attachments[len(attachments)-1].ID + 1000000,
		},
		{
			name:         "same project attachment on another ticket",
			ticketID:     ticket.ID,
			attachmentID: attachments[1].ID,
		},
	}
	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			var destination models.TicketAttachment
			err := service.loadAndAuthorizeAttachmentDownload(
				operationContext,
				access,
				operation,
				target.ticketID,
				target.attachmentID,
				&destination,
				"",
			)
			if !errors.Is(err, ErrAttachmentUnavailable) {
				t.Fatalf(
					"observer target error = %v, want attachment unavailable",
					err,
				)
			}
		})
	}
}

func TestObserverAttachmentAuthorizationPrecedesScanAndStorageState(
	t *testing.T,
) {
	db := openAgentNativeTestDB(t)
	observer := seedActorUser(t, db, "observer-attachment-state")
	operationContext := testProjectOperationContext(
		t,
		db,
		models.HumanActor(observer.ID),
	)
	ensureTestHumanProjectRole(
		t,
		db,
		operationContext,
		observer.ID,
		models.ProjectRoleObserver,
	)
	operation, err := OperationContextFromContext(operationContext)
	if err != nil {
		t.Fatal(err)
	}
	ticket := seedNativeTicket(
		t,
		db,
		observer.ID,
		"OBSERVER-ATTACHMENT-STATE",
	)
	attachments := []models.TicketAttachment{
		{
			FileName:     "private-pending.txt",
			OriginalName: "private-pending.txt",
			StoragePath:  "private-pending.txt",
			StorageType:  "local",
			VirusScan:    models.VirusScanPending,
		},
		{
			FileName:     "private-infected.txt",
			OriginalName: "private-infected.txt",
			StoragePath:  "private-infected.txt",
			StorageType:  "local",
			VirusScan:    models.VirusScanInfected,
		},
		{
			FileName:     "private-error.txt",
			OriginalName: "private-error.txt",
			StoragePath:  "private-error.txt",
			StorageType:  "local",
			VirusScan:    models.VirusScanError,
		},
		{
			FileName:     "private-staging.txt",
			OriginalName: "private-staging.txt",
			StoragePath:  "private-staging.txt",
			StorageType:  "staging",
			VirusScan:    models.VirusScanClean,
		},
	}
	for index := range attachments {
		attachments[index].OrganizationID = operation.Scope.OrganizationID
		attachments[index].ProjectID = operation.Scope.ProjectID
		attachments[index].TicketID = ticket.ID
		attachments[index].ActorType = models.ActorTypeHuman
		attachments[index].ActorID = models.HumanActor(observer.ID).ID
		attachments[index].FileSize = 7
		attachments[index].MimeType = "text/plain"
		attachments[index].FileType = models.AttachmentTypeDocument
		attachments[index].Hash = "private-state-hash"
		attachments[index].IsPublic = false
	}
	if err := db.Create(&attachments).Error; err != nil {
		t.Fatal(err)
	}
	service := NewAgentNativeService(db)
	access := &ProjectAccess{
		Scope: operation.Scope,
		Role:  models.ProjectRoleObserver,
	}
	for _, attachment := range attachments {
		t.Run(
			string(attachment.VirusScan)+"/"+attachment.StorageType,
			func(t *testing.T) {
				var destination models.TicketAttachment
				err := service.loadAndAuthorizeAttachmentDownload(
					operationContext,
					access,
					operation,
					ticket.ID,
					attachment.ID,
					&destination,
					"",
				)
				if !errors.Is(err, ErrAttachmentUnavailable) {
					t.Fatalf(
						"observer private state error = %v, want attachment unavailable",
						err,
					)
				}
				if errors.Is(err, ErrAttachmentNotClean) {
					t.Fatalf(
						"observer private state leaked attachment scan/storage state: %v",
						err,
					)
				}
			},
		)
	}
	for _, role := range []models.ProjectRole{
		models.ProjectRoleAdmin,
		models.ProjectRoleManager,
		models.ProjectRoleAgent,
	} {
		t.Run("privileged/"+string(role), func(t *testing.T) {
			var destination models.TicketAttachment
			err := service.loadAndAuthorizeAttachmentDownload(
				operationContext,
				&ProjectAccess{
					Scope: operation.Scope,
					Role:  role,
				},
				operation,
				ticket.ID,
				attachments[0].ID,
				&destination,
				"",
			)
			if !errors.Is(err, ErrAttachmentNotClean) {
				t.Fatalf(
					"%s pending private attachment error = %v, want attachment not clean",
					role,
					err,
				)
			}
		})
	}
}

func TestAttachmentDownloadPreservesPublicAndPrivilegedRoleSemantics(
	t *testing.T,
) {
	scope := models.ProjectScope{OrganizationID: 7, ProjectID: 11}
	operation := OperationContext{
		Scope:  scope,
		Actor:  models.HumanActor(42),
		Source: SourceProtocolHumanREST,
	}
	assignedToID := uint(99)
	ticket := models.Ticket{
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		AssignedToID:   &assignedToID,
	}
	for _, role := range []models.ProjectRole{
		models.ProjectRoleAdmin,
		models.ProjectRoleManager,
		models.ProjectRoleAgent,
	} {
		t.Run(string(role), func(t *testing.T) {
			access := &ProjectAccess{Scope: scope, Role: role}
			if err := authorizeHumanAttachmentTicket(
				access,
				operation,
				ticket,
				false,
				false,
			); err != nil {
				t.Fatalf(
					"%s private attachment read error = %v",
					role,
					err,
				)
			}
		})
	}
	observerAccess := &ProjectAccess{
		Scope: scope,
		Role:  models.ProjectRoleObserver,
	}
	if err := authorizeHumanAttachmentTicket(
		observerAccess,
		operation,
		ticket,
		false,
		true,
	); err != nil {
		t.Fatalf("observer public attachment read error = %v", err)
	}
}

func TestObserverAttachmentCrossProjectScopeRemainsNotFound(
	t *testing.T,
) {
	db := openAgentNativeTestDB(t)
	observer := seedActorUser(t, db, "observer-attachment-cross-project")
	operationContext := testProjectOperationContext(
		t,
		db,
		models.HumanActor(observer.ID),
	)
	ensureTestHumanProjectRole(
		t,
		db,
		operationContext,
		observer.ID,
		models.ProjectRoleObserver,
	)
	operation, err := OperationContextFromContext(operationContext)
	if err != nil {
		t.Fatal(err)
	}
	crossProjectTicket := seedNativeTicket(
		t,
		db,
		observer.ID,
		"OBSERVER-ATTACHMENT-CROSS-PROJECT",
	)
	if err := db.Model(&crossProjectTicket).Updates(map[string]any{
		"organization_id": operation.Scope.OrganizationID + 1,
		"project_id":      operation.Scope.ProjectID + 1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	crossProjectAttachment := models.TicketAttachment{
		OrganizationID: operation.Scope.OrganizationID + 1,
		ProjectID:      operation.Scope.ProjectID + 1,
		TicketID:       crossProjectTicket.ID,
		ActorType:      models.ActorTypeHuman,
		ActorID:        models.HumanActor(observer.ID).ID,
		FileName:       "cross-project.txt",
		OriginalName:   "cross-project.txt",
		FileSize:       7,
		MimeType:       "text/plain",
		FileType:       models.AttachmentTypeDocument,
		StoragePath:    "cross-project.txt",
		StorageType:    "local",
		Hash:           "cross-project-hash",
		IsPublic:       true,
		VirusScan:      models.VirusScanClean,
	}
	if err := db.Create(&crossProjectAttachment).Error; err != nil {
		t.Fatal(err)
	}
	service := NewAgentNativeService(db)
	access := &ProjectAccess{
		Scope: operation.Scope,
		Role:  models.ProjectRoleObserver,
	}
	var destination models.TicketAttachment
	err = service.loadAndAuthorizeAttachmentDownload(
		context.Background(),
		access,
		operation,
		crossProjectTicket.ID,
		crossProjectAttachment.ID,
		&destination,
		"",
	)
	if !errors.Is(err, ErrAttachmentUnavailable) {
		t.Fatalf(
			"cross-project attachment error = %v, want attachment unavailable",
			err,
		)
	}
}
