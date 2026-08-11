package services

import (
	"context"
	"errors"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

func setHumanTicketCreateMembership(
	t *testing.T,
	db *gorm.DB,
	ctx context.Context,
	userID uint,
	role models.ProjectRole,
	active bool,
) {
	t.Helper()
	if err := db.AutoMigrate(&models.ProjectMembership{}); err != nil {
		t.Fatalf("migrate project membership: %v", err)
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var membership models.ProjectMembership
	result := db.Where(
		"project_id = ? AND user_id = ?",
		operation.Scope.ProjectID,
		userID,
	).Assign(models.ProjectMembership{
		Role:     role,
		IsActive: active,
	}).FirstOrCreate(&membership, models.ProjectMembership{
		ProjectID: operation.Scope.ProjectID,
		UserID:    userID,
		Role:      role,
		IsActive:  active,
	})
	if result.Error != nil {
		t.Fatalf("seed project membership: %v", result.Error)
	}
	if err := db.Model(&membership).UpdateColumns(map[string]any{
		"role":      role,
		"is_active": active,
	}).Error; err != nil {
		t.Fatalf("persist project membership state: %v", err)
	}
}

func grantHumanTicketCreateMembership(
	t *testing.T,
	db *gorm.DB,
	ctx context.Context,
	userID uint,
	role models.ProjectRole,
) {
	t.Helper()
	setHumanTicketCreateMembership(t, db, ctx, userID, role, true)
}

func TestHumanProjectRoleCanCreateTicket(t *testing.T) {
	for _, test := range []struct {
		role models.ProjectRole
		want bool
	}{
		{role: models.ProjectRoleAdmin, want: true},
		{role: models.ProjectRoleManager, want: true},
		{role: models.ProjectRoleAgent, want: true},
		{role: models.ProjectRoleRequester, want: true},
		{role: models.ProjectRoleObserver, want: false},
		{role: models.ProjectRole("unknown"), want: false},
	} {
		if got := humanProjectRoleCanCreateTicket(test.role); got != test.want {
			t.Errorf(
				"humanProjectRoleCanCreateTicket(%q) = %t, want %t",
				test.role,
				got,
				test.want,
			)
		}
	}
}

func TestCreateTicketRequiresActiveAuthorizedProjectMembership(t *testing.T) {
	for _, test := range []struct {
		name       string
		membership *models.ProjectMembership
	}{
		{name: "missing"},
		{
			name: "inactive agent",
			membership: &models.ProjectMembership{
				Role:     models.ProjectRoleAgent,
				IsActive: false,
			},
		},
		{
			name: "observer",
			membership: &models.ProjectMembership{
				Role:     models.ProjectRoleObserver,
				IsActive: true,
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			db := openAgentNativeTestDB(t)
			user := seedActorUser(t, db, "create-membership-"+test.name)
			ctx := testProjectOperationContext(
				t,
				db,
				models.HumanActor(user.ID),
			)
			if test.membership == nil {
				if err := db.AutoMigrate(&models.ProjectMembership{}); err != nil {
					t.Fatal(err)
				}
			} else {
				setHumanTicketCreateMembership(
					t,
					db,
					ctx,
					user.ID,
					test.membership.Role,
					test.membership.IsActive,
				)
			}
			service := newTicketServiceForTest(t, db)
			_, err := service.CreateTicket(
				ctx,
				humanTicketCreateAuthorizationRequest(),
				user.ID,
			)
			if !errors.Is(err, ErrTicketCreateAccessDenied) {
				t.Fatalf(
					"CreateTicket() error = %v, want ticket create access denied",
					err,
				)
			}
			var count int64
			if err := db.Model(&models.Ticket{}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("denied create persisted %d tickets", count)
			}
		})
	}
}

func TestRequesterCreateTicketPrivilegeIsEnforcedByDomain(t *testing.T) {
	db := openAgentNativeTestDB(t)
	requester := seedActorUser(t, db, "create-requester")
	ctx := testProjectOperationContext(
		t,
		db,
		models.HumanActor(requester.ID),
	)
	grantHumanTicketCreateMembership(
		t,
		db,
		ctx,
		requester.ID,
		models.ProjectRoleRequester,
	)
	service := newTicketServiceForTest(t, db)
	open := models.TicketStatusOpen
	assignedToID := requester.ID
	for _, test := range []struct {
		name    string
		mutate  func(*models.TicketCreateRequest)
		wantErr error
	}{
		{
			name: "status",
			mutate: func(request *models.TicketCreateRequest) {
				request.Status = &open
			},
			wantErr: ErrHumanTicketStatusRequiresWorkflow,
		},
		{
			name: "assignee",
			mutate: func(request *models.TicketCreateRequest) {
				request.AssignedToID = &assignedToID
			},
			wantErr: ErrTicketCreateAccessDenied,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := humanTicketCreateAuthorizationRequest()
			test.mutate(request)
			if _, err := service.CreateTicket(
				ctx,
				request,
				requester.ID,
			); !errors.Is(err, test.wantErr) {
				t.Fatalf(
					"requester privileged CreateTicket() error = %v, want %v",
					err,
					test.wantErr,
				)
			}
		})
	}

	ticket, err := service.CreateTicket(
		ctx,
		humanTicketCreateAuthorizationRequest(),
		requester.ID,
	)
	if err != nil {
		t.Fatalf("requester unprivileged CreateTicket(): %v", err)
	}
	if ticket.CreatedByID == nil || *ticket.CreatedByID != requester.ID {
		t.Fatalf("requester ticket creator = %+v", ticket.CreatedByID)
	}
}

func humanTicketCreateAuthorizationRequest() *models.TicketCreateRequest {
	return &models.TicketCreateRequest{
		Title:       "Membership-gated ticket",
		Description: "Creation must remain project-scoped.",
		Type:        models.TicketTypeRequest,
		Priority:    models.TicketPriorityNormal,
		Source:      models.TicketSourceWeb,
	}
}
