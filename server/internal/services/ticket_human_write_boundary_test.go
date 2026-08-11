package services

import (
	"errors"
	"strconv"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"

	"gorm.io/gorm"
)

func TestHumanTicketCreateRejectsProtectedFieldsInSharedService(t *testing.T) {
	open := models.TicketStatusOpen
	for _, test := range []struct {
		name    string
		mutate  func(*models.TicketCreateRequest)
		wantErr error
	}{
		{
			name: "generic status",
			mutate: func(request *models.TicketCreateRequest) {
				request.Status = &open
			},
			wantErr: ErrHumanTicketStatusRequiresWorkflow,
		},
		{
			name: "trusted Agent source",
			mutate: func(request *models.TicketCreateRequest) {
				request.Source = models.TicketSourceAgent
			},
			wantErr: ErrTrustedTicketSourceNotHumanWritable,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := openAgentNativeTestDB(t)
			actor := seedActorUser(t, db, "human-create-boundary-"+test.name)
			ctx := testProjectOperationContext(
				t,
				db,
				models.HumanActor(actor.ID),
			)
			grantHumanTicketCreateMembership(
				t,
				db,
				ctx,
				actor.ID,
				models.ProjectRoleAdmin,
			)
			service := newTicketServiceForTest(t, db)
			request := humanTicketCreateAuthorizationRequest()
			test.mutate(request)

			if _, err := service.CreateTicket(
				ctx,
				request,
				actor.ID,
			); !errors.Is(err, test.wantErr) {
				t.Fatalf("CreateTicket() error = %v, want %v", err, test.wantErr)
			}
			assertNoHumanBoundaryTicketWrites(t, db)
		})
	}
}

func TestHumanNativeTicketCreateRejectsProtectedFields(t *testing.T) {
	open := models.TicketStatusOpen
	for _, test := range []struct {
		name    string
		mutate  func(*models.TicketCreateRequest)
		wantErr error
	}{
		{
			name: "generic status",
			mutate: func(request *models.TicketCreateRequest) {
				request.Status = &open
			},
			wantErr: ErrHumanTicketStatusRequiresWorkflow,
		},
		{
			name: "trusted Agent source",
			mutate: func(request *models.TicketCreateRequest) {
				request.Source = models.TicketSourceAgent
			},
			wantErr: ErrTrustedTicketSourceNotHumanWritable,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := openAgentNativeTestDB(t)
			actor := seedActorUser(t, db, "human-native-create-"+test.name)
			ctx := testProjectOperationContext(
				t,
				db,
				models.HumanActor(actor.ID),
			)
			request := humanTicketCreateAuthorizationRequest()
			test.mutate(request)

			if _, err := NewAgentNativeService(db).CreateNativeTicket(
				ctx,
				NativeTicketCreateInput{
					Request:        *request,
					Actor:          models.HumanActor(actor.ID),
					SourceProtocol: string(SourceProtocolHumanREST),
					TrustLevel:     models.TicketTrustLevelUntrusted,
				},
			); !errors.Is(err, test.wantErr) {
				t.Fatalf(
					"CreateNativeTicket() error = %v, want %v",
					err,
					test.wantErr,
				)
			}
			assertNoHumanBoundaryTicketWrites(t, db)
		})
	}
}

func TestHumanGenericTicketUpdateRejectsProtectedFields(t *testing.T) {
	inProgress := models.TicketStatusInProgress
	agentSource := models.TicketSourceAgent
	webSource := models.TicketSourceWeb
	for _, test := range []struct {
		name          string
		currentSource models.TicketSource
		request       models.TicketUpdateRequest
		wantErr       error
	}{
		{
			name:          "generic status",
			currentSource: models.TicketSourceWeb,
			request: models.TicketUpdateRequest{
				Status: &inProgress,
			},
			wantErr: ErrHumanTicketStatusRequiresWorkflow,
		},
		{
			name:          "declare trusted Agent source",
			currentSource: models.TicketSourceWeb,
			request: models.TicketUpdateRequest{
				Source: &agentSource,
			},
			wantErr: ErrTrustedTicketSourceNotHumanWritable,
		},
		{
			name:          "rewrite trusted Agent source",
			currentSource: models.TicketSourceAgent,
			request: models.TicketUpdateRequest{
				Source: &webSource,
			},
			wantErr: ErrTrustedTicketSourceNotHumanWritable,
		},
		{
			name:          "repeat trusted Agent source",
			currentSource: models.TicketSourceAgent,
			request: models.TicketUpdateRequest{
				Source: &agentSource,
			},
			wantErr: ErrTrustedTicketSourceNotHumanWritable,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDurableNotificationFixture(t, false)
			if err := fixture.db.Model(&models.Ticket{}).
				Where("id = ?", fixture.ticket.ID).
				Update("source", test.currentSource).Error; err != nil {
				t.Fatalf("seed Ticket source: %v", err)
			}

			if _, err := fixture.service.UpdateTicketExpectedVersion(
				fixture.ctx,
				fixture.ticket.ID,
				&test.request,
				fixture.actor.ID,
				fixture.ticket.Version,
			); !errors.Is(err, test.wantErr) {
				t.Fatalf(
					"UpdateTicketExpectedVersion() error = %v, want %v",
					err,
					test.wantErr,
				)
			}
			assertHumanBoundaryTicketUnchanged(
				t,
				fixture,
				test.currentSource,
			)
		})
	}
}

func TestHumanNativeTicketUpdateRejectsProtectedFields(t *testing.T) {
	for _, test := range []struct {
		name          string
		currentSource models.TicketSource
		changes       map[string]any
		wantErr       error
	}{
		{
			name:          "generic status",
			currentSource: models.TicketSourceWeb,
			changes: map[string]any{
				"status": models.TicketStatusInProgress,
			},
			wantErr: ErrHumanTicketStatusRequiresWorkflow,
		},
		{
			name:          "declare trusted Agent source",
			currentSource: models.TicketSourceWeb,
			changes: map[string]any{
				"source": models.TicketSourceAgent,
			},
			wantErr: ErrTrustedTicketSourceNotHumanWritable,
		},
		{
			name:          "rewrite trusted Agent source",
			currentSource: models.TicketSourceAgent,
			changes: map[string]any{
				"source": models.TicketSourceWeb,
			},
			wantErr: ErrTrustedTicketSourceNotHumanWritable,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDurableNotificationFixture(t, false)
			if err := fixture.db.Model(&models.Ticket{}).
				Where("id = ?", fixture.ticket.ID).
				Update("source", test.currentSource).Error; err != nil {
				t.Fatalf("seed Ticket source: %v", err)
			}

			if _, err := NewAgentNativeService(fixture.db).UpdateTicketVersion(
				fixture.ctx,
				VersionedTicketUpdateInput{
					TicketID:        fixture.ticket.ID,
					ExpectedVersion: fixture.ticket.Version,
					Actor:           models.HumanActor(fixture.actor.ID),
					SourceProtocol:  string(SourceProtocolHumanREST),
					Changes:         test.changes,
				},
			); !errors.Is(err, test.wantErr) {
				t.Fatalf(
					"UpdateTicketVersion() error = %v, want %v",
					err,
					test.wantErr,
				)
			}
			assertHumanBoundaryTicketUnchanged(
				t,
				fixture,
				test.currentSource,
			)
		})
	}
}

func TestHumanMayUpdateNonProvenanceFieldsOnAgentSourceTicket(t *testing.T) {
	fixture := newDurableNotificationFixture(t, false)
	if err := fixture.db.Model(&models.Ticket{}).
		Where("id = ?", fixture.ticket.ID).
		Update("source", models.TicketSourceAgent).Error; err != nil {
		t.Fatalf("seed Agent Ticket source: %v", err)
	}
	title := "Human-reviewed Agent ticket"

	updated, err := fixture.service.UpdateTicketExpectedVersion(
		fixture.ctx,
		fixture.ticket.ID,
		&models.TicketUpdateRequest{Title: &title},
		fixture.actor.ID,
		fixture.ticket.Version,
	)
	if err != nil {
		t.Fatalf("update non-provenance field: %v", err)
	}
	if updated.Title != title ||
		updated.Source != models.TicketSourceAgent ||
		updated.Version != fixture.ticket.Version+1 {
		t.Fatalf("updated Agent-source Ticket = %+v", updated)
	}
}

func TestHumanMayChangeBetweenOrdinaryTicketSources(t *testing.T) {
	fixture := newDurableNotificationFixture(t, false)
	phone := models.TicketSourcePhone

	updated, err := fixture.service.UpdateTicketExpectedVersion(
		fixture.ctx,
		fixture.ticket.ID,
		&models.TicketUpdateRequest{Source: &phone},
		fixture.actor.ID,
		fixture.ticket.Version,
	)
	if err != nil {
		t.Fatalf("update ordinary Ticket source: %v", err)
	}
	if updated.Source != phone ||
		updated.Version != fixture.ticket.Version+1 {
		t.Fatalf("updated ordinary-source Ticket = %+v", updated)
	}
}

func TestSystemConnectorTicketSourceRemainsAllowed(t *testing.T) {
	db := openAgentNativeTestDB(t)
	actor := models.SystemActor("connector-source-boundary")
	ctx := testProjectOperationContext(t, db, actor)
	request := humanTicketCreateAuthorizationRequest()
	request.Source = models.TicketSourceAPI

	created, err := NewAgentNativeService(db).CreateNativeTicket(
		ctx,
		NativeTicketCreateInput{
			Request:        *request,
			Actor:          actor,
			SourceProtocol: string(SourceProtocolConnector),
			TrustLevel:     models.TicketTrustLevelUntrusted,
		},
	)
	if err != nil {
		t.Fatalf("create Connector Ticket: %v", err)
	}
	if created.Ticket.Source != models.TicketSourceAPI ||
		created.Ticket.CreatedByActorType != models.ActorTypeSystem ||
		created.Ticket.CreatedByActorID != actor.ID {
		t.Fatalf("Connector Ticket provenance = %+v", created.Ticket)
	}
}

func TestHumanTicketCreationDerivesNonOpenWorkflowInitialStatus(t *testing.T) {
	db := openAgentNativeTestDB(t)
	actor := seedActorUser(t, db, "non-open-initial")
	ctx := testProjectOperationContext(t, db, models.HumanActor(actor.ID))
	grantHumanTicketCreateMembership(
		t,
		db,
		ctx,
		actor.ID,
		models.ProjectRoleAdmin,
	)
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		t.Fatalf("load operation context: %v", err)
	}
	var workflow models.WorkflowVersion
	if err := db.Where(
		"organization_id = ? AND project_id = ? AND status = ?",
		operation.Scope.OrganizationID,
		operation.Scope.ProjectID,
		models.ConfigurationStatusPublished,
	).First(&workflow).Error; err != nil {
		t.Fatalf("load published Workflow: %v", err)
	}
	forceHistoricalWorkflowDefinitions(
		t,
		db,
		workflow.ID,
		[]models.WorkflowStateDefinition{
			{
				Key:               "active",
				Name:              "处理中",
				LifecycleCategory: models.LifecycleCategoryActive,
				IsInitial:         true,
			},
			{
				Key:               "resolved",
				Name:              "已解决",
				LifecycleCategory: models.LifecycleCategoryResolved,
				IsTerminal:        true,
			},
		},
		[]models.WorkflowTransitionDefinition{{
			Key:  "resolve",
			Name: "解决",
			From: "active",
			To:   "resolved",
		}},
	)

	created, err := newTicketServiceForTest(t, db).CreateTicket(
		ctx,
		humanTicketCreateAuthorizationRequest(),
		actor.ID,
	)
	if err != nil {
		t.Fatalf("create Ticket from non-open Workflow: %v", err)
	}
	if created.Status != models.TicketStatusInProgress {
		t.Fatalf(
			"created Ticket status = %q, want %q",
			created.Status,
			models.TicketStatusInProgress,
		)
	}
}

func TestTicketUpdateBindsActorToTrustedOperationContext(t *testing.T) {
	fixture := newDurableNotificationFixture(t, false)

	_, err := NewAgentNativeService(fixture.db).UpdateTicketVersion(
		fixture.ctx,
		VersionedTicketUpdateInput{
			TicketID:        fixture.ticket.ID,
			ExpectedVersion: fixture.ticket.Version,
			Actor:           models.HumanActor(fixture.assignee.ID),
			SourceProtocol:  string(SourceProtocolHumanREST),
			Changes:         map[string]any{"title": "forged actor"},
		},
	)
	if !errors.Is(err, ErrInvalidActor) {
		t.Fatalf("UpdateTicketVersion() error = %v, want invalid actor", err)
	}
	assertHumanBoundaryTicketUnchanged(
		t,
		fixture,
		fixture.ticket.Source,
	)
}

func assertNoHumanBoundaryTicketWrites(t *testing.T, db *gorm.DB) {
	t.Helper()
	for name, model := range map[string]any{
		"Tickets":           &models.Ticket{},
		"Ticket histories":  &models.TicketHistory{},
		"Domain Events":     &models.DomainEvent{},
		"Outbox Deliveries": &models.OutboxDelivery{},
	} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if count != 0 {
			t.Fatalf(
				"protected Human create persisted %d %s",
				count,
				name,
			)
		}
	}
}

func assertHumanBoundaryTicketUnchanged(
	t *testing.T,
	fixture durableNotificationFixture,
	wantSource models.TicketSource,
) {
	t.Helper()
	var persisted models.Ticket
	if err := fixture.db.First(&persisted, fixture.ticket.ID).Error; err != nil {
		t.Fatalf("reload Ticket: %v", err)
	}
	if persisted.Version != fixture.ticket.Version ||
		persisted.Status != fixture.ticket.Status ||
		persisted.Source != wantSource {
		t.Fatalf(
			"protected Human update mutated Ticket: version=%d status=%q source=%q",
			persisted.Version,
			persisted.Status,
			persisted.Source,
		)
	}

	var historyCount int64
	if err := fixture.db.Model(&models.TicketHistory{}).
		Where("ticket_id = ?", fixture.ticket.ID).
		Count(&historyCount).Error; err != nil {
		t.Fatalf("count Ticket history: %v", err)
	}
	var eventCount int64
	if err := fixture.db.Model(&models.DomainEvent{}).
		Where("subject = ?", "ticket/"+strconv.FormatUint(
			uint64(fixture.ticket.ID),
			10,
		)).
		Count(&eventCount).Error; err != nil {
		t.Fatalf("count Ticket events: %v", err)
	}
	if historyCount != 0 || eventCount != 0 {
		t.Fatalf(
			"protected Human update emitted history=%d events=%d",
			historyCount,
			eventCount,
		)
	}
	var outboxCount int64
	if err := fixture.db.Model(&models.OutboxDelivery{}).
		Count(&outboxCount).Error; err != nil {
		t.Fatalf("count Outbox Deliveries: %v", err)
	}
	if outboxCount != 0 {
		t.Fatalf(
			"protected Human update emitted %d Outbox Deliveries",
			outboxCount,
		)
	}
}
