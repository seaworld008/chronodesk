package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestValidateTicketRequestFormUsesPublishedSchemaAndRejectsReservedFields(
	t *testing.T,
) {
	t.Parallel()

	requestType := models.RequestTypeVersion{
		JSONSchema: datatypes.JSON(`{
			"$schema":"https://json-schema.org/draft/2020-12/schema",
			"type":"object",
			"properties":{
				"summary":{"type":"string","minLength":1},
				"description":{"type":"string","minLength":1},
				"priority":{"type":"string","enum":["low","normal","high","urgent","critical"]},
				"risk_level":{"type":"string","enum":["low","medium","high"]}
			},
			"required":["summary","description","priority","risk_level"],
			"additionalProperties":false
		}`),
	}
	baseRequest := func() *models.TicketCreateRequest {
		customFields := models.JSONMap{"risk_level": "medium"}
		return &models.TicketCreateRequest{
			Title:        "数据库连接异常",
			Description:  "生产环境连接池持续耗尽。",
			Type:         models.TicketTypeIncident,
			Priority:     models.TicketPriorityNormal,
			Source:       models.TicketSourceWeb,
			CustomFields: &customFields,
		}
	}

	tests := []struct {
		name    string
		mutate  func(*models.TicketCreateRequest)
		wantErr bool
	}{
		{name: "valid derived core and custom fields"},
		{
			name: "missing required custom field",
			mutate: func(request *models.TicketCreateRequest) {
				empty := models.JSONMap{}
				request.CustomFields = &empty
			},
			wantErr: true,
		},
		{
			name: "reserved project field cannot hide in custom fields",
			mutate: func(request *models.TicketCreateRequest) {
				fields := models.JSONMap{
					"risk_level": "medium",
					"project_id": 999,
				}
				request.CustomFields = &fields
			},
			wantErr: true,
		},
		{
			name: "schema enum remains authoritative",
			mutate: func(request *models.TicketCreateRequest) {
				fields := models.JSONMap{"risk_level": "unbounded"}
				request.CustomFields = &fields
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := baseRequest()
			if test.mutate != nil {
				test.mutate(request)
			}
			err := validateTicketRequestForm(requestType, request)
			if test.wantErr && !errors.Is(err, ErrTicketFormValidation) {
				t.Fatalf("validateTicketRequestForm() error = %v, want form validation", err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("validateTicketRequestForm() unexpected error: %v", err)
			}
		})
	}
}

func TestTicketTransitionUsesStoredWorkflowVersionInsteadOfHardcodedLifecycle(
	t *testing.T,
) {
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "workflow-runtime")
	native := NewAgentNativeService(db)
	ticketService, err := NewTicketService(
		db,
		native,
		nil,
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := testProjectOperationContext(t, db, models.HumanActor(user.ID))
	grantHumanTicketCreateMembership(
		t,
		db,
		ctx,
		user.ID,
		models.ProjectRoleAgent,
	)
	ticket, err := ticketService.CreateTicket(
		ctx,
		&models.TicketCreateRequest{
			Title:       "验证版本化工作流",
			Description: "状态流转必须服从工单绑定的不可变工作流版本。",
			Type:        models.TicketTypeRequest,
			Priority:    models.TicketPriorityNormal,
			Source:      models.TicketSourceWeb,
		},
		user.ID,
	)
	if err != nil {
		t.Fatal(err)
	}

	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var originalRelease models.ConfigurationRelease
	if err := db.Where(
		"organization_id = ? AND project_id = ? AND status = ?",
		operation.Scope.OrganizationID,
		operation.Scope.ProjectID,
		models.ConfigurationStatusPublished,
	).Order("version ASC").First(&originalRelease).Error; err != nil {
		t.Fatalf("load original V1 release: %v", err)
	}
	now := time.Now().UTC()
	replacement := models.WorkflowVersion{
		OrganizationID: operation.Scope.OrganizationID,
		ProjectID:      operation.Scope.ProjectID,
		Key:            "default",
		Version:        2,
		Status:         models.ConfigurationStatusPublished,
		Name:           "Replacement workflow",
		CreatedByType:  models.ActorTypeHuman,
		CreatedByID:    models.HumanActor(user.ID).ID,
		PublishedAt:    &now,
	}
	if err := replacement.SetDefinitions(
		[]models.WorkflowStateDefinition{
			{
				Key: "new", Name: "New",
				LifecycleCategory: models.LifecycleCategoryNew,
				IsInitial:         true,
			},
			{
				Key: "shadow_new", Name: "Shadow new",
				LifecycleCategory: models.LifecycleCategoryNew,
			},
			{
				Key: "done", Name: "Done",
				LifecycleCategory: models.LifecycleCategoryResolved,
				IsTerminal:        true,
			},
		},
		[]models.WorkflowTransitionDefinition{
			{
				Key: "resolve_directly", Name: "Resolve directly",
				From: "new", To: "done",
			},
			{
				Key: "shadow_resolve", Name: "Shadow resolve",
				From: "shadow_new", To: "done",
			},
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&replacement).Error; err != nil {
		t.Fatal(err)
	}
	var requestType models.RequestTypeVersion
	if err := db.First(
		&requestType,
		"id = ?",
		ticket.RequestTypeVersionID,
	).Error; err != nil {
		t.Fatal(err)
	}
	release := models.ConfigurationRelease{
		OrganizationID: operation.Scope.OrganizationID,
		ProjectID:      operation.Scope.ProjectID,
		Version:        2,
		Status:         models.ConfigurationStatusPublished,
		CreatedByType:  models.ActorTypeHuman,
		CreatedByID:    models.HumanActor(user.ID).ID,
		ApprovedByType: models.ActorTypeHuman,
		ApprovedByID:   models.HumanActor(user.ID).ID,
		PublishedAt:    &now,
	}
	if err := release.SetConfigurationSnapshot(models.ConfigurationSnapshot{
		RequestTypeVersionIDs: []string{requestType.ID},
		WorkflowVersionIDs:    []string{replacement.ID},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&release).Error; err != nil {
		t.Fatal(err)
	}
	allowed, err := ticketService.AllowedTicketTransitions(
		ctx,
		ticket.ID,
		user.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(allowed) != 2 ||
		allowed[0] != models.TicketStatusInProgress ||
		allowed[1] != models.TicketStatusCancelled {
		t.Fatalf(
			"bound V1 transitions after V2 publication = %v",
			allowed,
		)
	}
	configurationService, err := NewProjectConfigurationService(db, native)
	if err != nil {
		t.Fatal(err)
	}
	rollback, err := configurationService.RollbackConfigurationRelease(
		ctx,
		originalRelease.ID,
	)
	if err != nil {
		t.Fatalf("rollback current configuration to V1: %v", err)
	}
	current, err := configurationService.CurrentConfigurationRelease(ctx)
	if err != nil {
		t.Fatalf("load current rollback release: %v", err)
	}
	if current.ID != rollback.ID ||
		rollback.RollbackOfReleaseID == nil ||
		*rollback.RollbackOfReleaseID != originalRelease.ID {
		t.Fatalf("current rollback release = %+v, rollback = %+v", current, rollback)
	}
	allowed, err = ticketService.AllowedTicketTransitions(
		ctx,
		ticket.ID,
		user.ID,
	)
	if err != nil {
		t.Fatalf("bound V1 transitions after rollback: %v", err)
	}
	if len(allowed) != 2 ||
		allowed[0] != models.TicketStatusInProgress ||
		allowed[1] != models.TicketStatusCancelled {
		t.Fatalf("bound V1 transitions after rollback = %v", allowed)
	}

	// The former hardcoded lifecycle allowed open -> resolved, while the
	// published bootstrap workflow intentionally requires start -> resolve.
	if _, err := ticketService.UpdateTicketStatusExpectedVersion(
		ctx,
		ticket.ID,
		string(models.TicketStatusResolved),
		user.ID,
		"",
		"",
		ticket.Version,
	); !errors.Is(err, ErrInvalidTicketTransition) {
		t.Fatalf("open -> resolved error = %v, want workflow rejection", err)
	}

	started, err := ticketService.UpdateTicketStatusExpectedVersion(
		ctx,
		ticket.ID,
		string(models.TicketStatusInProgress),
		user.ID,
		"",
		"",
		ticket.Version,
	)
	if err != nil {
		t.Fatalf("published workflow start transition failed: %v", err)
	}
	if started.Status != models.TicketStatusInProgress ||
		started.WorkflowVersionID != ticket.WorkflowVersionID {
		t.Fatalf("unexpected workflow transition result: %+v", started)
	}
}

func TestTicketWorkflowRuntimeProjectsRepeatedCategoriesAsCanonicalEdgeUnion(
	t *testing.T,
) {
	db, ticketService, ctx, user, ticket := newWorkflowRuntimeTicket(
		t,
		models.ProjectRoleAgent,
	)
	forceHistoricalWorkflowDefinitions(
		t,
		db,
		ticket.WorkflowVersionID,
		[]models.WorkflowStateDefinition{
			{
				Key: "primary_new", Name: "Primary new",
				LifecycleCategory: models.LifecycleCategoryNew,
				IsInitial:         true,
			},
			{
				Key: "shadow_new", Name: "Shadow new",
				LifecycleCategory: models.LifecycleCategoryNew,
			},
			{
				Key: "primary_active", Name: "Primary active",
				LifecycleCategory: models.LifecycleCategoryActive,
			},
			{
				Key: "shadow_active", Name: "Shadow active",
				LifecycleCategory: models.LifecycleCategoryActive,
			},
			{
				Key: "customer_wait", Name: "Waiting for customer",
				LifecycleCategory: models.LifecycleCategoryWaiting,
			},
			{
				Key: "vendor_wait", Name: "Waiting for vendor",
				LifecycleCategory: models.LifecycleCategoryWaiting,
			},
			{
				Key: "resolved", Name: "Resolved",
				LifecycleCategory: models.LifecycleCategoryResolved,
				IsTerminal:        true,
			},
			{
				Key: "closed", Name: "Closed",
				LifecycleCategory: models.LifecycleCategoryClosed,
				IsTerminal:        true,
			},
		},
		[]models.WorkflowTransitionDefinition{
			{
				Key: "primary_start", Name: "Primary start",
				From: "primary_new", To: "primary_active",
			},
			{
				Key: "shadow_start", Name: "Shadow start",
				From: "shadow_new", To: "shadow_active",
			},
			{
				Key: "shadow_resolve", Name: "Shadow resolve",
				From: "shadow_new", To: "resolved",
			},
			{
				Key: "customer_wait", Name: "Wait for customer",
				From: "primary_active", To: "customer_wait",
			},
			{
				Key: "vendor_wait", Name: "Wait for vendor",
				From: "shadow_active", To: "vendor_wait",
			},
			{
				Key: "resolve", Name: "Resolve",
				From: "shadow_active", To: "resolved",
			},
		},
	)

	allowed, err := ticketService.AllowedTicketTransitions(
		ctx,
		ticket.ID,
		user.ID,
	)
	if err != nil {
		t.Fatalf("AllowedTicketTransitions() for repeated new states: %v", err)
	}
	if len(allowed) != 2 ||
		allowed[0] != models.TicketStatusInProgress ||
		allowed[1] != models.TicketStatusResolved {
		t.Fatalf(
			"canonical open edge union = %v, want [in_progress resolved]",
			allowed,
		)
	}

	started, err := ticketService.UpdateTicketStatusExpectedVersion(
		ctx,
		ticket.ID,
		string(models.TicketStatusInProgress),
		user.ID,
		"",
		"",
		ticket.Version,
	)
	if err != nil {
		t.Fatalf("canonical open -> in_progress transition: %v", err)
	}
	if started.WorkflowVersionID != ticket.WorkflowVersionID {
		t.Fatalf(
			"transition changed bound workflow version %q -> %q",
			ticket.WorkflowVersionID,
			started.WorkflowVersionID,
		)
	}

	// Exact workflow state keys are not persisted today. Once the Ticket is in
	// the canonical active category, all active-state edges therefore project
	// into one de-duplicated set of canonical TicketStatus values.
	allowed, err = ticketService.AllowedTicketTransitions(
		ctx,
		ticket.ID,
		user.ID,
	)
	if err != nil {
		t.Fatalf("AllowedTicketTransitions() for repeated active/waiting states: %v", err)
	}
	if len(allowed) != 2 ||
		allowed[0] != models.TicketStatusPending ||
		allowed[1] != models.TicketStatusResolved {
		t.Fatalf(
			"canonical active edge union = %v, want [pending resolved]",
			allowed,
		)
	}
	if _, err := ticketService.UpdateTicketStatusExpectedVersion(
		ctx,
		ticket.ID,
		string(models.TicketStatusClosed),
		user.ID,
		"",
		"",
		started.Version,
	); !errors.Is(err, ErrInvalidTicketTransition) {
		t.Fatalf("illegal canonical active -> closed edge error = %v", err)
	}
}

func TestTicketWorkflowRuntimeUnionsRolesAcrossRepeatedCategoryEdges(
	t *testing.T,
) {
	for _, test := range []struct {
		name    string
		role    models.ProjectRole
		allowed bool
	}{
		{name: "agent edge", role: models.ProjectRoleAgent, allowed: true},
		{name: "requester edge", role: models.ProjectRoleRequester, allowed: true},
		{name: "unmatched manager role", role: models.ProjectRoleManager},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, ticketService, ctx, user, ticket := newWorkflowRuntimeTicket(
				t,
				test.role,
			)
			forceHistoricalWorkflowDefinitions(
				t,
				db,
				ticket.WorkflowVersionID,
				[]models.WorkflowStateDefinition{
					{
						Key: "primary_new", Name: "Primary new",
						LifecycleCategory: models.LifecycleCategoryNew,
						IsInitial:         true,
					},
					{
						Key: "shadow_new", Name: "Shadow new",
						LifecycleCategory: models.LifecycleCategoryNew,
					},
					{
						Key: "primary_active", Name: "Primary active",
						LifecycleCategory: models.LifecycleCategoryActive,
					},
					{
						Key: "shadow_active", Name: "Shadow active",
						LifecycleCategory: models.LifecycleCategoryActive,
					},
				},
				[]models.WorkflowTransitionDefinition{
					{
						Key: "agent_start", Name: "Agent start",
						From:  "primary_new",
						To:    "primary_active",
						Roles: []models.ProjectRole{models.ProjectRoleAgent},
					},
					{
						Key: "requester_shadow_start", Name: "Requester shadow start",
						From:  "shadow_new",
						To:    "shadow_active",
						Roles: []models.ProjectRole{models.ProjectRoleRequester},
					},
				},
			)

			updated, err := ticketService.UpdateTicketStatusExpectedVersion(
				ctx,
				ticket.ID,
				string(models.TicketStatusInProgress),
				user.ID,
				"",
				"",
				ticket.Version,
			)
			if test.allowed {
				if err != nil {
					t.Fatalf("role %q canonical transition rejected: %v", test.role, err)
				}
				if updated.Status != models.TicketStatusInProgress {
					t.Fatalf("role %q transition result = %+v", test.role, updated)
				}
				return
			}
			if !errors.Is(err, ErrInvalidTicketTransition) {
				t.Fatalf("role %q transition error = %v", test.role, err)
			}
		})
	}
}

func TestTicketWorkflowRuntimeRejectsSameCanonicalStatusWithoutStateKey(
	t *testing.T,
) {
	db, _, ctx, user, ticket := newWorkflowRuntimeTicket(
		t,
		models.ProjectRoleAgent,
	)
	err := transactionForContext(ctx, db, func(tx *gorm.DB) error {
		var persisted models.Ticket
		if err := tx.First(&persisted, ticket.ID).Error; err != nil {
			return err
		}
		return validateTicketWorkflowTransitionTx(
			ctx,
			tx,
			models.ProjectScope{
				OrganizationID: persisted.OrganizationID,
				ProjectID:      persisted.ProjectID,
			},
			&persisted,
			persisted.Status,
			models.HumanActor(user.ID),
		)
	})
	if !errors.Is(err, ErrInvalidTicketTransition) {
		t.Fatalf(
			"same canonical status workflow validation error = %v, want ErrInvalidTicketTransition",
			err,
		)
	}
}

func TestHumanTicketServiceSameStatusRemainsNoOp(t *testing.T) {
	db, ticketService, ctx, user, ticket := newWorkflowRuntimeTicket(
		t,
		models.ProjectRoleAgent,
	)
	before := *ticket
	var beforeHistory, beforeEvents, beforeOutbox int64
	if err := db.Model(&models.TicketHistory{}).
		Where("ticket_id = ?", ticket.ID).
		Count(&beforeHistory).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.DomainEvent{}).
		Where(
			"type = ? AND subject = ?",
			eventcontract.TicketTransitionedEventType,
			fmt.Sprintf("ticket/%d", ticket.ID),
		).
		Count(&beforeEvents).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.OutboxDelivery{}).
		Count(&beforeOutbox).Error; err != nil {
		t.Fatal(err)
	}

	unchanged, err := ticketService.UpdateTicketStatusExpectedVersion(
		ctx,
		ticket.ID,
		string(ticket.Status),
		user.ID,
		"",
		"",
		ticket.Version,
	)
	if err != nil {
		t.Fatalf("Human same-status no-op: %v", err)
	}
	if unchanged.Version != before.Version ||
		unchanged.Status != before.Status ||
		!unchanged.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("Human same-status no-op changed Ticket: before=%+v after=%+v", before, unchanged)
	}

	var afterHistory, afterEvents, afterOutbox int64
	if err := db.Model(&models.TicketHistory{}).
		Where("ticket_id = ?", ticket.ID).
		Count(&afterHistory).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.DomainEvent{}).
		Where(
			"type = ? AND subject = ?",
			eventcontract.TicketTransitionedEventType,
			fmt.Sprintf("ticket/%d", ticket.ID),
		).
		Count(&afterEvents).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.OutboxDelivery{}).
		Count(&afterOutbox).Error; err != nil {
		t.Fatal(err)
	}
	if afterHistory != beforeHistory ||
		afterEvents != beforeEvents ||
		afterOutbox != beforeOutbox {
		t.Fatalf(
			"Human same-status no-op side effects history/events/outbox = %d/%d/%d, want %d/%d/%d",
			afterHistory,
			afterEvents,
			afterOutbox,
			beforeHistory,
			beforeEvents,
			beforeOutbox,
		)
	}
}

func TestAllowedTicketTransitionsPropagatesWorkflowLookupErrors(t *testing.T) {
	db, ticketService, ctx, user, ticket := newWorkflowRuntimeTicket(
		t,
		models.ProjectRoleAgent,
	)
	if err := db.Migrator().DropTable(&models.WorkflowVersion{}); err != nil {
		t.Fatal(err)
	}

	allowed, err := ticketService.AllowedTicketTransitions(
		ctx,
		ticket.ID,
		user.ID,
	)
	if err == nil ||
		!errors.Is(err, ErrInvalidTicketTransition) ||
		!strings.Contains(err.Error(), "load ticket workflow") {
		t.Fatalf("AllowedTicketTransitions() error = %v, want workflow lookup error", err)
	}
	if allowed != nil {
		t.Fatalf("AllowedTicketTransitions() = %v, want nil on lookup error", allowed)
	}
}

func TestAllowedTicketTransitionsPropagatesAuthorizationQueryErrors(
	t *testing.T,
) {
	db, ticketService, ctx, user, ticket := newWorkflowRuntimeTicket(
		t,
		models.ProjectRoleAgent,
	)
	forceHistoricalWorkflowDefinitions(
		t,
		db,
		ticket.WorkflowVersionID,
		[]models.WorkflowStateDefinition{
			{
				Key: "open", Name: "Open",
				LifecycleCategory: models.LifecycleCategoryNew,
				IsInitial:         true,
			},
			{
				Key: "active", Name: "Active",
				LifecycleCategory: models.LifecycleCategoryActive,
			},
		},
		[]models.WorkflowTransitionDefinition{
			{
				Key: "start", Name: "Start",
				From:  "open",
				To:    "active",
				Roles: []models.ProjectRole{models.ProjectRoleAgent},
			},
		},
	)
	if err := db.Migrator().DropTable(&models.ProjectMembership{}); err != nil {
		t.Fatal(err)
	}

	allowed, err := ticketService.AllowedTicketTransitions(
		ctx,
		ticket.ID,
		user.ID,
	)
	if err == nil ||
		!errors.Is(err, ErrInvalidTicketTransition) ||
		!strings.Contains(
			err.Error(),
			"workflow actor has no active project membership",
		) {
		t.Fatalf("AllowedTicketTransitions() error = %v, want authorization query error", err)
	}
	if allowed != nil {
		t.Fatalf("AllowedTicketTransitions() = %v, want nil on authorization error", allowed)
	}
}

func newWorkflowRuntimeTicket(
	t *testing.T,
	role models.ProjectRole,
) (
	*gorm.DB,
	*TicketService,
	context.Context,
	models.User,
	*models.Ticket,
) {
	t.Helper()
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, strings.ReplaceAll(t.Name(), "/", "-"))
	native := NewAgentNativeService(db)
	ticketService, err := NewTicketService(
		db,
		native,
		nil,
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := testProjectOperationContext(t, db, models.HumanActor(user.ID))
	grantHumanTicketCreateMembership(t, db, ctx, user.ID, role)
	ticket, err := ticketService.CreateTicket(
		ctx,
		&models.TicketCreateRequest{
			Title:       "验证历史工作流",
			Description: "历史发布配置中的重复生命周期类别必须继续执行。",
			Type:        models.TicketTypeRequest,
			Priority:    models.TicketPriorityNormal,
			Source:      models.TicketSourceWeb,
		},
		user.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	return db, ticketService, ctx, user, ticket
}

func forceHistoricalWorkflowDefinitions(
	t *testing.T,
	db *gorm.DB,
	workflowID string,
	states []models.WorkflowStateDefinition,
	transitions []models.WorkflowTransitionDefinition,
) {
	t.Helper()
	encodedStates, err := json.Marshal(states)
	if err != nil {
		t.Fatal(err)
	}
	encodedTransitions, err := json.Marshal(transitions)
	if err != nil {
		t.Fatal(err)
	}
	result := db.Exec(
		"UPDATE workflow_versions SET states = ?, transitions = ? WHERE id = ?",
		string(encodedStates),
		string(encodedTransitions),
		workflowID,
	)
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	if result.RowsAffected != 1 {
		t.Fatalf("updated historical workflows = %d, want 1", result.RowsAffected)
	}
}

func TestValidateTicketRequestFormForbidsExternalSchemaReferences(t *testing.T) {
	t.Parallel()
	requestType := models.RequestTypeVersion{
		JSONSchema: datatypes.JSON(`{
			"$schema":"https://json-schema.org/draft/2020-12/schema",
			"$ref":"https://attacker.invalid/schema.json"
		}`),
	}
	request := &models.TicketCreateRequest{
		Title:       "外部引用",
		Description: "不得从网络加载。",
		Type:        models.TicketTypeRequest,
		Priority:    models.TicketPriorityNormal,
		Source:      models.TicketSourceWeb,
	}
	if err := validateTicketRequestForm(requestType, request); !errors.Is(
		err,
		ErrTicketFormValidation,
	) {
		t.Fatalf("validateTicketRequestForm() error = %v, want form validation", err)
	}
}
