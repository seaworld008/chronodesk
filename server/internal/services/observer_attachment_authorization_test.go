package services

import (
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
