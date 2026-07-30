package agentplatform

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/agentauth"
	"github.com/seaworld008/chronodesk/server/internal/mcp"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var mcpAdapterTestSequence atomic.Uint64

type mcpAdapterFixture struct {
	db                   *gorm.DB
	service              *services.AgentNativeService
	manager              *agentauth.Manager
	adapter              *MCPAdapter
	organization         models.Organization
	project              models.Project
	queue                models.Queue
	requestTypeVersionID string
	workflowVersionID    string
	user                 models.User
	principal            *models.ServicePrincipal
	credential           models.AgentCredential
	token                string
	actor                mcp.Principal
}

func newMCPAdapterFixture(t *testing.T) *mcpAdapterFixture {
	t.Helper()
	dsn := fmt.Sprintf(
		"file:mcp-adapter-%d?mode=memory&cache=shared",
		mcpAdapterTestSequence.Add(1),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.Queue{},
		&models.RequestTypeVersion{},
		&models.WorkflowVersion{},
		&models.ConfigurationRelease{},
		&models.User{},
		&models.Category{},
		&models.ServicePrincipal{},
		&models.AgentCredential{},
		&models.AgentPolicy{},
		&models.PolicyDecision{},
		&models.IdempotencyRecord{},
		&models.SLAConfig{},
		&models.Ticket{},
		&models.TicketComment{},
		&models.TicketAttachment{},
		&models.TicketHistory{},
		&models.TicketLease{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.ProjectPrincipalGrant{},
	); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	organization := models.Organization{
		Slug:   fmt.Sprintf("mcp-%d", mcpAdapterTestSequence.Load()),
		Name:   "MCP test organization",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	businessUnit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "support",
		Name:           "Support",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&businessUnit).Error; err != nil {
		t.Fatalf("seed business unit: %v", err)
	}
	project := models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: businessUnit.ID,
		Key:            "TEST",
		Name:           "MCP test project",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatalf("seed project: %v", err)
	}
	queue := models.Queue{
		ProjectID: project.ID,
		Key:       "default",
		Name:      "Default",
		Status:    models.QueueStatusActive,
		IsDefault: true,
	}
	if err := db.Create(&queue).Error; err != nil {
		t.Fatalf("seed default queue: %v", err)
	}
	requestTypeVersionID, workflowVersionID :=
		bootstrapAgentplatformTestConfiguration(t, db, project.Scope())
	user := models.User{
		Username:     fmt.Sprintf("mcp-compat-%d", mcpAdapterTestSequence.Load()),
		Email:        fmt.Sprintf("mcp-compat-%d@example.com", mcpAdapterTestSequence.Load()),
		PasswordHash: "not-a-password",
		Role:         models.RoleAgent,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed actor user: %v", err)
	}
	storage, err := services.NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatalf("create attachment storage: %v", err)
	}
	service := services.NewAgentNativeService(db, services.AgentNativeOptions{
		AttachmentStorage:  storage,
		AttachmentMaxBytes: 10 << 20,
	})
	scopes := append([]string(nil), models.SupportedAgentScopes...)
	principal, err := service.CreateServicePrincipal(context.Background(), services.CreateServicePrincipalInput{
		Name:               fmt.Sprintf("mcp-agent-%d", mcpAdapterTestSequence.Load()),
		Scopes:             scopes,
		RateLimitPerMinute: 500,
		ConcurrentLimit:    8,
	})
	if err != nil {
		t.Fatalf("create principal: %v", err)
	}
	credential := models.AgentCredential{
		ID:                 fmt.Sprintf("credential-%d", mcpAdapterTestSequence.Load()),
		ServicePrincipalID: principal.ID,
		Name:               "mcp-test",
		SecretHash:         "unused-by-access-token",
		Status:             models.AgentCredentialStatusActive,
		ExpiresAt:          time.Now().UTC().Add(time.Hour),
	}
	if err := db.Create(&credential).Error; err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	encodedScopes, err := json.Marshal(scopes)
	if err != nil {
		t.Fatalf("encode project grant scopes: %v", err)
	}
	grant := models.ProjectPrincipalGrant{
		ProjectID:          project.ID,
		ServicePrincipalID: principal.ID,
		Role:               models.ProjectRoleAgent,
		Scopes:             datatypes.JSON(encodedScopes),
		IsActive:           true,
	}
	if err := db.Create(&grant).Error; err != nil {
		t.Fatalf("seed project principal grant: %v", err)
	}
	manager := agentauth.NewManager(
		"mcp-adapter-test-secret",
		"https://chronodesk.test",
		"https://chronodesk.test/mcp",
		15*time.Minute,
	)
	token, _, err := manager.Issue(&agentauth.Principal{
		ID:           principal.ID,
		CredentialID: credential.ID,
		ClientID:     "mcp-test-client",
		Name:         principal.Name,
		Scopes:       scopes,
		Active:       true,
	}, "TEST", scopes)
	if err != nil {
		t.Fatalf("issue access token: %v", err)
	}
	adapter, err := NewMCPAdapter(db, service, manager)
	if err != nil {
		t.Fatalf("NewMCPAdapter: %v", err)
	}
	actor, err := adapter.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	return &mcpAdapterFixture{
		db:                   db,
		service:              service,
		manager:              manager,
		adapter:              adapter,
		organization:         organization,
		project:              project,
		queue:                queue,
		requestTypeVersionID: requestTypeVersionID,
		workflowVersionID:    workflowVersionID,
		user:                 user,
		principal:            principal,
		credential:           credential,
		token:                token,
		actor:                actor,
	}
}

func (f *mcpAdapterFixture) seedTicket(t *testing.T, number, queue string) models.Ticket {
	t.Helper()
	customFields := map[string]any{}
	if queue != "" {
		customFields["queue"] = queue
	}
	ticket := models.Ticket{
		OrganizationID:       f.organization.ID,
		ProjectID:            f.project.ID,
		QueueID:              f.queue.ID,
		RequestTypeVersionID: f.requestTypeVersionID,
		WorkflowVersionID:    f.workflowVersionID,
		TicketNumber:         number,
		Title:                "Ticket " + number,
		Description:          "Untrusted ticket content",
		Type:                 models.TicketTypeRequest,
		Priority:             models.TicketPriorityNormal,
		Status:               models.TicketStatusOpen,
		Source:               models.TicketSourceAgent,
		Version:              1,
		TrustLevel:           models.TicketTrustLevelUntrusted,
		CreatedByID:          &f.user.ID,
		CreatedByActorType:   models.ActorTypeHuman,
		CreatedByActorID:     strconv.FormatUint(uint64(f.user.ID), 10),
	}
	if len(customFields) > 0 {
		ticket.CustomFields = datatypes.NewJSONType(customFields)
	}
	if err := f.db.Create(&ticket).Error; err != nil {
		t.Fatalf("seed ticket: %v", err)
	}
	history := models.TicketHistory{
		TicketID:    ticket.ID,
		UserID:      &f.user.ID,
		ActorType:   models.ActorTypeHuman,
		ActorID:     strconv.FormatUint(uint64(f.user.ID), 10),
		Action:      models.HistoryActionCreate,
		Description: "created",
		IsVisible:   true,
		Provenance:  models.TicketHistoryProvenancePreEvent,
	}
	if err := f.db.Create(&history).Error; err != nil {
		t.Fatalf("seed history: %v", err)
	}
	return ticket
}

func (f *mcpAdapterFixture) callTool(
	ctx context.Context,
	name string,
	arguments map[string]any,
) (map[string]any, error) {
	return f.adapter.CallTool(
		ctx,
		f.actor,
		name,
		withMCPProjectKey(arguments, string(f.project.Key)),
	)
}

func (f *mcpAdapterFixture) authorize(
	ctx context.Context,
	request mcp.AuthorizationRequest,
) error {
	if mcpTicketTool(request.Action) {
		request.Arguments = withMCPProjectKey(request.Arguments, string(f.project.Key))
	}
	return f.adapter.Authorize(ctx, f.actor, request)
}

func TestTicketAssignedActorRejectsIncompleteAndContradictoryProjections(t *testing.T) {
	humanID := uint(17)
	otherHumanID := uint(23)
	servicePrincipalID := "11111111-1111-4111-8111-111111111111"
	tests := []struct {
		name       string
		ticket     models.Ticket
		wantActor  *models.ActorRef
		wantReason string
	}{
		{
			name:   "unassigned",
			ticket: models.Ticket{ID: 1},
		},
		{
			name: "complete human actor",
			ticket: models.Ticket{
				ID:                           2,
				AssignedToActorType:          models.ActorTypeHuman,
				AssignedToActorID:            strconv.FormatUint(uint64(humanID), 10),
				AssignedToID:                 &humanID,
				AssignedToServicePrincipalID: nil,
			},
			wantActor: func() *models.ActorRef {
				actor := models.HumanActor(humanID)
				return &actor
			}(),
		},
		{
			name: "legacy human projection without actor",
			ticket: models.Ticket{
				ID:           3,
				AssignedToID: &humanID,
			},
			wantReason: "missing_actor",
		},
		{
			name: "actor type only",
			ticket: models.Ticket{
				ID:                  4,
				AssignedToActorType: models.ActorTypeHuman,
			},
			wantReason: "incomplete_actor",
		},
		{
			name: "actor id only",
			ticket: models.Ticket{
				ID:                5,
				AssignedToActorID: strconv.FormatUint(uint64(humanID), 10),
			},
			wantReason: "incomplete_actor",
		},
		{
			name: "human projection disagrees with actor",
			ticket: models.Ticket{
				ID:                  6,
				AssignedToActorType: models.ActorTypeHuman,
				AssignedToActorID:   strconv.FormatUint(uint64(humanID), 10),
				AssignedToID:        &otherHumanID,
			},
			wantReason: "projection_mismatch",
		},
		{
			name: "service principal carries human projection",
			ticket: models.Ticket{
				ID:                           7,
				AssignedToActorType:          models.ActorTypeServicePrincipal,
				AssignedToActorID:            servicePrincipalID,
				AssignedToID:                 &humanID,
				AssignedToServicePrincipalID: &servicePrincipalID,
			},
			wantReason: "projection_mismatch",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actor, err := ticketAssignedActor(&test.ticket)
			if test.wantReason == "" {
				if err != nil || !reflect.DeepEqual(actor, test.wantActor) {
					t.Fatalf("actor=%+v err=%v, want actor=%+v", actor, err, test.wantActor)
				}
				return
			}
			var failure *mcp.BackendError
			if !errors.As(err, &failure) ||
				failure.Code != "data_integrity_error" ||
				failure.Details["reason_code"] != test.wantReason {
				t.Fatalf("error=%#v, want data_integrity_error/%s", err, test.wantReason)
			}
		})
	}
}

func TestMCPAdapterReturnsStableIntegrityErrorForLegacyAssignmentProjection(t *testing.T) {
	fixture := newMCPAdapterFixture(t)
	ticket := fixture.seedTicket(t, "MCP-INTEGRITY-001", "triage")
	if err := fixture.db.Model(&models.Ticket{}).
		Where("id = ?", ticket.ID).
		Updates(map[string]any{
			"assigned_to_id":         fixture.user.ID,
			"assigned_to_actor_type": "",
			"assigned_to_actor_id":   "",
		}).Error; err != nil {
		t.Fatalf("corrupt assignment projection: %v", err)
	}

	for _, call := range []struct {
		name      string
		arguments map[string]any
	}{
		{name: "ticket_get", arguments: map[string]any{"ticket_id": int64(ticket.ID)}},
		{name: "ticket_list", arguments: map[string]any{"search": ticket.TicketNumber}},
	} {
		t.Run(call.name, func(t *testing.T) {
			_, err := fixture.callTool(context.Background(), call.name, call.arguments)
			var failure *mcp.BackendError
			if !errors.As(err, &failure) ||
				failure.Code != "data_integrity_error" ||
				failure.Message != "ticket assignment data is inconsistent" ||
				failure.Details["reason_code"] != "missing_actor" ||
				failure.Details["field"] != "assigned_to_actor" {
				t.Fatalf("error=%#v", err)
			}
		})
	}
}

func TestMCPAdapterAuthenticationQueriesResourcesAndPolicy(t *testing.T) {
	fixture := newMCPAdapterFixture(t)
	first := fixture.seedTicket(t, "MCP-QUERY-001", "triage")
	time.Sleep(time.Millisecond)
	second := fixture.seedTicket(t, "MCP-QUERY-002", "backlog")

	if fixture.actor.ID != fixture.principal.ID ||
		fixture.actor.CredentialID != fixture.credential.ID ||
		!fixture.actor.HasScopes(models.ScopeTicketsRead, models.ScopeTicketsUpdate) {
		t.Fatalf("unexpected authenticated principal: %+v", fixture.actor)
	}
	if fixture.actor.Attributes["project_key"] != string(fixture.project.Key) ||
		fixture.actor.Attributes["organization_id"] != fixture.organization.ID ||
		fixture.actor.Attributes["project_id"] != fixture.project.ID {
		t.Fatalf("authenticated principal has no trusted project context: %+v", fixture.actor.Attributes)
	}
	if err := fixture.authorize(
		context.Background(),
		mcp.AuthorizationRequest{
			Action:         "ticket_get",
			RequiredScopes: []string{models.ScopeTicketsRead},
			Arguments:      map[string]any{"ticket_id": first.ID},
		},
	); err != nil {
		t.Fatalf("Authorize read: %v", err)
	}
	var authorizationDecision models.PolicyDecision
	if err := fixture.db.
		Where("action = ? AND resource_id = ?", "ticket.read", strconv.FormatUint(uint64(first.ID), 10)).
		Order("created_at DESC").
		First(&authorizationDecision).Error; err != nil {
		t.Fatalf("load authorization decision: %v", err)
	}
	if authorizationDecision.OrganizationID != fixture.organization.ID ||
		authorizationDecision.ProjectID != fixture.project.ID ||
		authorizationDecision.SourceProtocol != mcpSourceProtocol {
		t.Fatalf("authorization did not persist trusted MCP project context: %+v", authorizationDecision)
	}

	pageOne, err := fixture.callTool(context.Background(), "ticket_list", map[string]any{
		"limit": int64(1),
	})
	if err != nil {
		t.Fatalf("ticket_list page one: %v", err)
	}
	items := pageOne["items"].([]map[string]any)
	if len(items) != 1 || items[0]["id"] != second.ID || pageOne["next_cursor"] == "" {
		t.Fatalf("unexpected first page: %#v", pageOne)
	}
	pageTwo, err := fixture.callTool(context.Background(), "ticket_list", map[string]any{
		"limit":  int64(1),
		"cursor": pageOne["next_cursor"],
	})
	if err != nil {
		t.Fatalf("ticket_list page two: %v", err)
	}
	secondPageItems := pageTwo["items"].([]map[string]any)
	if len(secondPageItems) != 1 || secondPageItems[0]["id"] != first.ID {
		t.Fatalf("unexpected second page: %#v first_time=%s second_time=%s cursor=%v", pageTwo, first.CreatedAt, second.CreatedAt, pageOne["next_cursor"])
	}

	queueResult, err := fixture.callTool(context.Background(), "ticket_list", map[string]any{
		"queue": "triage",
	})
	if err != nil {
		t.Fatalf("queue list: %v", err)
	}
	queueItems := queueResult["items"].([]map[string]any)
	if len(queueItems) != 1 || queueItems[0]["id"] != first.ID {
		t.Fatalf("queue filter result: %#v", queueResult)
	}

	getResult, err := fixture.callTool(context.Background(), "ticket_get", map[string]any{
		"ticket_id": int64(first.ID),
	})
	if err != nil {
		t.Fatalf("ticket_get: %v", err)
	}
	ticket := getResult["ticket"].(map[string]any)
	if ticket["queue"] != "triage" || ticket["description"] != first.Description {
		t.Fatalf("ticket_get result: %#v", ticket)
	}

	historyResult, err := fixture.callTool(context.Background(), "ticket_history", map[string]any{
		"ticket_id": int64(first.ID),
	})
	if err != nil {
		t.Fatalf("ticket_history: %v", err)
	}
	historyItems := historyResult["items"].([]map[string]any)
	if len(historyItems) != 1 {
		t.Fatalf("ticket history result: %#v", historyResult)
	}
	if historyItems[0]["event_id"] != nil ||
		historyItems[0]["resource_version"] != uint64(0) ||
		historyItems[0]["provenance"] != string(models.TicketHistoryProvenancePreEvent) {
		t.Fatalf("pre-event history must not expose a synthetic event link: %#v", historyItems[0])
	}

	for _, uri := range []string{
		fmt.Sprintf("ticket://projects/TEST/tickets/%d", first.ID),
		"ticket://projects/TEST/queues/triage",
		fmt.Sprintf("ticket://projects/TEST/tickets/%d/history", first.ID),
	} {
		resource, err := fixture.adapter.ReadResource(context.Background(), fixture.actor, uri)
		if err != nil {
			t.Fatalf("ReadResource(%s): %v", uri, err)
		}
		if resource.URI != uri || resource.MIMEType != "application/json" || resource.Text == "" {
			t.Fatalf("invalid resource %s: %+v", uri, resource)
		}
	}
	if _, err := fixture.adapter.ReadResource(
		context.Background(),
		fixture.actor,
		"https://attacker.example/ticket",
	); err == nil {
		t.Fatal("external resource URI was accepted")
	}

	if _, err := fixture.service.CreateAgentPolicy(context.Background(), services.CreateAgentPolicyInput{
		ServicePrincipalID: fixture.principal.ID,
		Name:               "deny first ticket",
		Effect:             models.AgentPolicyEffectDeny,
		Scope:              models.ScopeTicketsRead,
		Action:             "ticket.read",
		ResourceType:       "ticket",
		ResourceID:         strconv.FormatUint(uint64(first.ID), 10),
		Priority:           100,
	}); err != nil {
		t.Fatalf("create deny policy: %v", err)
	}
	if _, err := fixture.callTool(context.Background(), "ticket_get", map[string]any{
		"ticket_id": int64(first.ID),
	}); err == nil || !strings.Contains(err.Error(), "action denied") {
		t.Fatalf("object policy did not deny ticket_get: %v", err)
	}
	filtered, err := fixture.callTool(context.Background(), "ticket_list", map[string]any{})
	if err != nil {
		t.Fatalf("policy-filtered list: %v", err)
	}
	for _, item := range filtered["items"].([]map[string]any) {
		if item["id"] == first.ID {
			t.Fatalf("policy-denied ticket leaked through list: %#v", filtered)
		}
	}

	if err := fixture.db.Model(&models.AgentCredential{}).
		Where("id = ?", fixture.credential.ID).
		Update("status", models.AgentCredentialStatusRevoked).Error; err != nil {
		t.Fatalf("revoke credential: %v", err)
	}
	if _, err := fixture.adapter.Authenticate(context.Background(), fixture.token); err == nil {
		t.Fatal("Authenticate accepted revoked credential")
	}
	if _, err := fixture.adapter.Revalidate(context.Background(), fixture.token); err == nil {
		t.Fatal("Revalidate accepted revoked credential")
	}
}

func TestMCPAdapterRejectsProjectConfusionAndFiltersForeignTickets(t *testing.T) {
	fixture := newMCPAdapterFixture(t)
	foreignProject := models.Project{
		OrganizationID: fixture.organization.ID,
		BusinessUnitID: fixture.project.BusinessUnitID,
		Key:            "OTHER",
		Name:           "Foreign project",
		Status:         models.ProjectStatusActive,
	}
	if err := fixture.db.Create(&foreignProject).Error; err != nil {
		t.Fatalf("seed foreign project: %v", err)
	}
	foreignQueue := models.Queue{
		ProjectID: foreignProject.ID,
		Key:       "default",
		Name:      "Default",
		Status:    models.QueueStatusActive,
		IsDefault: true,
	}
	if err := fixture.db.Create(&foreignQueue).Error; err != nil {
		t.Fatalf("seed foreign queue: %v", err)
	}
	foreignTicket := models.Ticket{
		OrganizationID:     fixture.organization.ID,
		ProjectID:          foreignProject.ID,
		QueueID:            foreignQueue.ID,
		TicketNumber:       "FOREIGN-001",
		Title:              "Foreign ticket",
		Description:        "must not cross the project boundary",
		Type:               models.TicketTypeRequest,
		Priority:           models.TicketPriorityNormal,
		Status:             models.TicketStatusOpen,
		Source:             models.TicketSourceAgent,
		Version:            1,
		TrustLevel:         models.TicketTrustLevelUntrusted,
		CreatedByID:        &fixture.user.ID,
		CreatedByActorType: models.ActorTypeHuman,
		CreatedByActorID:   strconv.FormatUint(uint64(fixture.user.ID), 10),
	}
	if err := fixture.db.Create(&foreignTicket).Error; err != nil {
		t.Fatalf("seed foreign ticket: %v", err)
	}

	_, err := fixture.adapter.CallTool(
		context.Background(),
		fixture.actor,
		"ticket_get",
		map[string]any{"ticket_id": foreignTicket.ID},
	)
	var failure *mcp.BackendError
	if !errors.As(err, &failure) ||
		failure.Code != "invalid_params" ||
		failure.Details["field"] != "project_key" {
		t.Fatalf("missing project_key error = %#v, err=%v", failure, err)
	}

	_, err = fixture.adapter.CallTool(
		context.Background(),
		fixture.actor,
		"ticket_get",
		map[string]any{
			"project_key": string(foreignProject.Key),
			"ticket_id":   foreignTicket.ID,
		},
	)
	failure = nil
	if !errors.As(err, &failure) || failure.Code != "project_scope_mismatch" {
		t.Fatalf("mismatched project_key error = %#v, err=%v", failure, err)
	}
	if err := fixture.adapter.Authorize(
		context.Background(),
		fixture.actor,
		mcp.AuthorizationRequest{
			Action:         "ticket_get",
			RequiredScopes: []string{models.ScopeTicketsRead},
			Arguments: map[string]any{
				"project_key": string(foreignProject.Key),
				"ticket_id":   foreignTicket.ID,
			},
		},
	); err == nil {
		t.Fatal("authorization accepted a project_key that differs from the token")
	}

	if _, err := fixture.callTool(
		context.Background(),
		"ticket_get",
		map[string]any{"ticket_id": foreignTicket.ID},
	); err == nil {
		t.Fatal("foreign ticket was readable through a matching token project key")
	}
	if _, err := fixture.adapter.ReadResource(
		context.Background(),
		fixture.actor,
		fmt.Sprintf("ticket://projects/OTHER/tickets/%d", foreignTicket.ID),
	); err == nil {
		t.Fatal("foreign project resource URI was accepted")
	}
	if _, err := fixture.adapter.ReadResource(
		context.Background(),
		fixture.actor,
		fmt.Sprintf("ticket://projects/TEST/tickets/%d", foreignTicket.ID),
	); err == nil {
		t.Fatal("foreign ticket was readable through a forged token-project URI")
	}
	if allowed, err := fixture.adapter.ValidateSubscription(
		context.Background(),
		fixture.actor,
		fmt.Sprintf("ticket://projects/OTHER/tickets/%d", foreignTicket.ID),
	); err != nil || allowed {
		t.Fatalf("foreign project subscription = (%v, %v), want denied", allowed, err)
	}

	if err := fixture.db.Model(&models.ProjectPrincipalGrant{}).
		Where(
			"project_id = ? AND service_principal_id = ?",
			fixture.project.ID,
			fixture.principal.ID,
		).
		Update("is_active", false).Error; err != nil {
		t.Fatalf("revoke project grant: %v", err)
	}
	if _, err := fixture.adapter.Revalidate(context.Background(), fixture.token); err == nil {
		t.Fatal("Revalidate accepted a revoked project grant")
	}
}

func TestMCPAdapterRevalidateDoesNotTouchUsageTimestamps(t *testing.T) {
	fixture := newMCPAdapterFixture(t)
	known := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	if err := fixture.db.Model(&models.ServicePrincipal{}).
		Where("id = ?", fixture.principal.ID).
		UpdateColumn("last_used_at", known).Error; err != nil {
		t.Fatalf("set principal last_used_at: %v", err)
	}
	if err := fixture.db.Model(&models.AgentCredential{}).
		Where("id = ?", fixture.credential.ID).
		UpdateColumn("last_used_at", known).Error; err != nil {
		t.Fatalf("set credential last_used_at: %v", err)
	}

	if _, err := fixture.adapter.Revalidate(context.Background(), fixture.token); err != nil {
		t.Fatalf("Revalidate: %v", err)
	}

	var principal models.ServicePrincipal
	if err := fixture.db.First(&principal, "id = ?", fixture.principal.ID).Error; err != nil {
		t.Fatalf("reload principal: %v", err)
	}
	var credential models.AgentCredential
	if err := fixture.db.First(&credential, "id = ?", fixture.credential.ID).Error; err != nil {
		t.Fatalf("reload credential: %v", err)
	}
	if principal.LastUsedAt == nil || !principal.LastUsedAt.Equal(known) {
		t.Fatalf("principal last_used_at changed during revalidation: %v", principal.LastUsedAt)
	}
	if credential.LastUsedAt == nil || !credential.LastUsedAt.Equal(known) {
		t.Fatalf("credential last_used_at changed during revalidation: %v", credential.LastUsedAt)
	}
}

func TestMCPAdapterAuthorizationUsesConcreteTicketContext(t *testing.T) {
	fixture := newMCPAdapterFixture(t)
	denied := fixture.seedTicket(t, "MCP-POLICY-DENY", "triage")
	allowed := fixture.seedTicket(t, "MCP-POLICY-ALLOW", "triage")
	deniedID := strconv.FormatUint(uint64(denied.ID), 10)
	allowedID := strconv.FormatUint(uint64(allowed.ID), 10)

	if _, err := fixture.service.CreateAgentPolicy(context.Background(), services.CreateAgentPolicyInput{
		ServicePrincipalID: fixture.principal.ID,
		Name:               "deny one ticket subscription",
		Effect:             models.AgentPolicyEffectDeny,
		Scope:              models.ScopeEventsSubscribe,
		Action:             "ticket.subscribe",
		ResourceType:       "ticket",
		ResourceID:         deniedID,
		Priority:           100,
	}); err != nil {
		t.Fatalf("create subscription deny policy: %v", err)
	}
	subscription := func(ticketID string) mcp.AuthorizationRequest {
		return mcp.AuthorizationRequest{
			Action:         "resource:subscribe",
			RequiredScopes: []string{models.ScopeTicketsRead, models.ScopeEventsSubscribe},
			ResourceURI:    "ticket://projects/TEST/tickets/" + ticketID,
		}
	}
	var policyErr *mcp.PolicyError
	if err := fixture.authorize(context.Background(), subscription(deniedID)); !errors.As(err, &policyErr) {
		t.Fatalf("expected concrete subscription policy denial, got %v", err)
	}
	if err := fixture.authorize(context.Background(), subscription(allowedID)); err != nil {
		t.Fatalf("unrelated ticket subscription denied: %v", err)
	}

	if _, err := fixture.service.CreateAgentPolicy(context.Background(), services.CreateAgentPolicyInput{
		ServicePrincipalID: fixture.principal.ID,
		Name:               "allow one risky transition",
		Effect:             models.AgentPolicyEffectAllow,
		Scope:              models.ScopeTicketsTransition,
		Action:             "ticket.transition",
		ResourceType:       "ticket",
		ResourceID:         allowedID,
		Priority:           100,
	}); err != nil {
		t.Fatalf("create transition allow policy: %v", err)
	}
	transition := func(ticketID uint) mcp.AuthorizationRequest {
		return mcp.AuthorizationRequest{
			Action:         "ticket_transition",
			RequiredScopes: []string{models.ScopeTicketsTransition},
			Arguments: map[string]any{
				"ticket_id": ticketID,
				"status":    string(models.TicketStatusResolved),
			},
		}
	}
	if err := fixture.authorize(context.Background(), transition(allowed.ID)); err != nil {
		t.Fatalf("ticket-specific risky allow was not honored: %v", err)
	}
	policyErr = nil
	if err := fixture.authorize(context.Background(), transition(denied.ID)); !errors.As(err, &policyErr) {
		t.Fatalf("expected risky transition without exact allow to be denied, got %v", err)
	}
}

func TestMCPAdapterSubscriptionRevalidationIsReadOnlyAndQuotaFree(t *testing.T) {
	fixture := newMCPAdapterFixture(t)
	ticket := fixture.seedTicket(t, "MCP-SUBSCRIPTION-READONLY", "triage")
	if err := fixture.db.Model(&models.ServicePrincipal{}).
		Where("id = ?", fixture.principal.ID).
		UpdateColumn("rate_limit_per_minute", 1).Error; err != nil {
		t.Fatalf("set principal rate limit: %v", err)
	}
	var decisionsBefore int64
	if err := fixture.db.Model(&models.PolicyDecision{}).Count(&decisionsBefore).Error; err != nil {
		t.Fatalf("count decisions before: %v", err)
	}

	uri := fmt.Sprintf("ticket://projects/TEST/tickets/%d", ticket.ID)
	for i := 0; i < 3; i++ {
		allowed, err := fixture.adapter.ValidateSubscription(context.Background(), fixture.actor, uri)
		if err != nil {
			t.Fatalf("ValidateSubscription #%d: %v", i+1, err)
		}
		if !allowed {
			t.Fatalf("ValidateSubscription #%d unexpectedly denied", i+1)
		}
	}

	var decisionsAfter int64
	if err := fixture.db.Model(&models.PolicyDecision{}).Count(&decisionsAfter).Error; err != nil {
		t.Fatalf("count decisions after: %v", err)
	}
	if decisionsAfter != decisionsBefore {
		t.Fatalf("subscription revalidation persisted %d policy decisions", decisionsAfter-decisionsBefore)
	}
	release, err := fixture.service.AcquireAgentExecution(context.Background(), fixture.principal.ID)
	if err != nil {
		t.Fatalf("subscription revalidation consumed execution quota: %v", err)
	}
	release()
}

func TestMCPAdapterAttachmentPolicyUsesFileNameContextAtBothChecks(t *testing.T) {
	fixture := newMCPAdapterFixture(t)
	ticket := fixture.seedTicket(t, "MCP-ATTACHMENT-CONTEXT", "triage")
	if _, err := fixture.service.CreateAgentPolicy(context.Background(), services.CreateAgentPolicyInput{
		ServicePrincipalID: fixture.principal.ID,
		Name:               "deny blocked attachment filename",
		Effect:             models.AgentPolicyEffectDeny,
		Scope:              models.ScopeAttachmentsWrite,
		Action:             "ticket.attachment.create",
		ResourceType:       "ticket",
		ResourceID:         strconv.FormatUint(uint64(ticket.ID), 10),
		Conditions:         map[string]any{"file_name": "blocked.txt"},
		Priority:           100,
	}); err != nil {
		t.Fatalf("create attachment policy: %v", err)
	}
	arguments := map[string]any{
		"ticket_id":        int64(ticket.ID),
		"expected_version": int64(1),
		"lease_id":         "unused-because-policy-denies",
		"file_name":        "blocked.txt",
		"content_type":     "text/plain",
		"content_base64":   base64.StdEncoding.EncodeToString([]byte("hello")),
		"sha256":           "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
		"visibility":       "internal",
		"idempotency_key":  "blocked-attachment-0001",
	}
	var policyErr *mcp.PolicyError
	if err := fixture.authorize(
		context.Background(),
		mcp.AuthorizationRequest{
			Action:         "ticket_attach_file",
			RequiredScopes: []string{models.ScopeAttachmentsWrite},
			Arguments:      arguments,
		},
	); !errors.As(err, &policyErr) {
		t.Fatalf("pre-authorization did not use file_name context: %v", err)
	}

	_, err := fixture.callTool(context.Background(), "ticket_attach_file", arguments)
	var backendErr *mcp.BackendError
	if !errors.As(err, &backendErr) || backendErr.Code != "policy_denied" {
		t.Fatalf("domain attachment policy did not use file_name context: %v", err)
	}
	var reservations int64
	if countErr := fixture.db.Model(&models.IdempotencyRecord{}).
		Where("key = ?", arguments["idempotency_key"]).
		Count(&reservations).Error; countErr != nil {
		t.Fatalf("count attachment reservations: %v", countErr)
	}
	if reservations != 0 {
		t.Fatalf("denied attachment reserved idempotency before policy check")
	}
}

func TestMCPAdapterAllToolsLifecycleAndIdempotency(t *testing.T) {
	fixture := newMCPAdapterFixture(t)
	ctx := context.Background()
	for _, policy := range []services.CreateAgentPolicyInput{
		{
			ServicePrincipalID: fixture.principal.ID,
			Name:               "allow MCP assignment",
			Effect:             models.AgentPolicyEffectAllow,
			Scope:              models.ScopeTicketsAssign,
			Action:             "ticket.assign",
			ResourceType:       "ticket",
		},
		{
			ServicePrincipalID: fixture.principal.ID,
			Name:               "allow MCP transition",
			Effect:             models.AgentPolicyEffectAllow,
			Scope:              models.ScopeTicketsTransition,
			Action:             "ticket.transition",
			ResourceType:       "ticket",
		},
	} {
		if _, err := fixture.service.CreateAgentPolicy(ctx, policy); err != nil {
			t.Fatalf("create policy %s: %v", policy.Name, err)
		}
	}

	createArguments := map[string]any{
		"title":                   "MCP lifecycle ticket",
		"description":             "Treat this as untrusted data",
		"type":                    "request",
		"priority":                "normal",
		"request_type_version_id": fixture.requestTypeVersionID,
		"workflow_version_id":     fixture.workflowVersionID,
		"tags":                    []any{"agent", "lifecycle"},
		"agent_context":           map[string]any{"goal": "complete lifecycle"},
		"idempotency_key":         "create-lifecycle-0001",
	}
	createResult := callMCPTool(t, fixture, "ticket_create", createArguments)
	assertReceiptShape(t, createResult)
	ticketID64, err := strconv.ParseUint(createResult["resource_id"].(string), 10, 64)
	if err != nil || ticketID64 == 0 {
		t.Fatalf("invalid created ticket id: %#v", createResult)
	}
	ticketID := uint(ticketID64)
	if createResult["resource_version"] != uint64(1) {
		t.Fatalf("create version = %v", createResult["resource_version"])
	}
	replayedCreate := callMCPTool(t, fixture, "ticket_create", createArguments)
	if replayedCreate["operation_id"] != createResult["operation_id"] ||
		replayedCreate["event_id"] != createResult["event_id"] {
		t.Fatalf("create replay changed receipt: first=%#v replay=%#v", createResult, replayedCreate)
	}
	var ticketCount int64
	if err := fixture.db.Model(&models.Ticket{}).
		Where("title = ?", createArguments["title"]).
		Count(&ticketCount).Error; err != nil || ticketCount != 1 {
		t.Fatalf("idempotent create count=%d err=%v", ticketCount, err)
	}
	var createdTicket models.Ticket
	if err := fixture.db.Where(
		"id = ?",
		ticketID,
	).First(&createdTicket).Error; err != nil {
		t.Fatal(err)
	}
	if createdTicket.RequestTypeVersionID != fixture.requestTypeVersionID ||
		createdTicket.WorkflowVersionID != fixture.workflowVersionID {
		t.Fatalf(
			"MCP create configuration versions = (%q,%q)",
			createdTicket.RequestTypeVersionID,
			createdTicket.WorkflowVersionID,
		)
	}

	listResult := callMCPTool(t, fixture, "ticket_list", map[string]any{})
	if len(listResult["items"].([]map[string]any)) != 1 {
		t.Fatalf("ticket_list result: %#v", listResult)
	}
	getResult := callMCPTool(t, fixture, "ticket_get", map[string]any{"ticket_id": int64(ticketID)})
	if getResult["ticket"].(map[string]any)["id"] != ticketID {
		t.Fatalf("ticket_get result: %#v", getResult)
	}

	claimResult := callMCPTool(t, fixture, "ticket_claim", map[string]any{
		"ticket_id":        int64(ticketID),
		"expected_version": int64(1),
		"lease_seconds":    int64(60),
		"idempotency_key":  "claim-lifecycle-0001",
	})
	assertLeaseShape(t, claimResult)
	leaseID := claimResult["lease_id"].(string)

	heartbeatResult := callMCPTool(t, fixture, "ticket_heartbeat", map[string]any{
		"ticket_id":       int64(ticketID),
		"lease_id":        leaseID,
		"lease_seconds":   int64(60),
		"idempotency_key": "heartbeat-lifecycle-0001",
	})
	assertLeaseShape(t, heartbeatResult)

	updateResult := callMCPTool(t, fixture, "ticket_update", map[string]any{
		"ticket_id":        int64(ticketID),
		"expected_version": int64(1),
		"lease_id":         leaseID,
		"patch": map[string]any{
			"title": "Updated over MCP",
			"queue": "operations",
			"tags":  []any{"updated"},
		},
		"reason":          "normalise intake",
		"idempotency_key": "update-lifecycle-0001",
	})
	assertReceiptShape(t, updateResult)
	if updateResult["resource_version"] != uint64(2) {
		t.Fatalf("update version = %v", updateResult["resource_version"])
	}

	assignResult := callMCPTool(t, fixture, "ticket_assign", map[string]any{
		"ticket_id":        int64(ticketID),
		"expected_version": int64(2),
		"lease_id":         leaseID,
		"assignee": map[string]any{
			"type": string(models.ActorTypeHuman),
			"id":   strconv.FormatUint(uint64(fixture.user.ID), 10),
		},
		"reason":          "assign human operator",
		"idempotency_key": "assign-lifecycle-0001",
	})
	assertReceiptShape(t, assignResult)
	if assignResult["resource_version"] != uint64(3) {
		t.Fatalf("assign version = %v", assignResult["resource_version"])
	}

	transitionResult := callMCPTool(t, fixture, "ticket_transition", map[string]any{
		"ticket_id":        int64(ticketID),
		"expected_version": int64(3),
		"lease_id":         leaseID,
		"status":           "in_progress",
		"reason":           "agent started work",
		"idempotency_key":  "transition-lifecycle-0001",
	})
	assertReceiptShape(t, transitionResult)
	if transitionResult["resource_version"] != uint64(4) {
		t.Fatalf("transition version = %v", transitionResult["resource_version"])
	}

	commentResult := callMCPTool(t, fixture, "ticket_add_comment", map[string]any{
		"ticket_id":        int64(ticketID),
		"expected_version": int64(4),
		"lease_id":         leaseID,
		"visibility":       "internal",
		"content":          "Investigating the issue.",
		"content_type":     "markdown",
		"reason":           "record progress",
		"idempotency_key":  "comment-lifecycle-0001",
	})
	assertReceiptShape(t, commentResult["receipt"].(map[string]any))
	comment := commentResult["comment"].(map[string]any)
	if comment["visibility"] != "internal" || comment["ticket_id"] != ticketID {
		t.Fatalf("comment result: %#v", commentResult)
	}

	content := []byte("hello")
	attachmentResult := callMCPTool(t, fixture, "ticket_attach_file", map[string]any{
		"ticket_id":        int64(ticketID),
		"expected_version": int64(5),
		"lease_id":         leaseID,
		"file_name":        "evidence.txt",
		"content_type":     "text/plain",
		"content_base64":   base64.StdEncoding.EncodeToString(content),
		"sha256":           "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
		"visibility":       "internal",
		"idempotency_key":  "attach-lifecycle-0001",
	})
	assertReceiptShape(t, attachmentResult["receipt"].(map[string]any))
	attachment := attachmentResult["attachment"].(map[string]any)
	if attachment["sha256"] != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" ||
		attachment["virus_scan"] != "pending" {
		t.Fatalf("attachment result: %#v", attachmentResult)
	}

	historyResult := callMCPTool(t, fixture, "ticket_history", map[string]any{
		"ticket_id": int64(ticketID),
		"limit":     int64(100),
	})
	historyItems := historyResult["items"].([]map[string]any)
	if len(historyItems) < 5 {
		t.Fatalf("history result: %#v", historyResult)
	}
	for _, item := range historyItems {
		eventID, ok := item["event_id"].(string)
		if !ok || eventID == "" ||
			item["provenance"] != string(models.TicketHistoryProvenanceDomainEvent) ||
			item["resource_version"] == uint64(0) {
			t.Fatalf("native history must expose its persisted event link: %#v", item)
		}
	}
	actionResult := callMCPTool(t, fixture, "action_check", map[string]any{
		"action":    "ticket_transition",
		"ticket_id": int64(ticketID),
	})
	if actionResult["allowed"] != true ||
		actionResult["decision_id"] == "" ||
		len(actionResult["required_scopes"].([]string)) != 1 {
		t.Fatalf("action_check result: %#v", actionResult)
	}

	releaseResult := callMCPTool(t, fixture, "ticket_release", map[string]any{
		"ticket_id":       int64(ticketID),
		"lease_id":        leaseID,
		"idempotency_key": "release-lifecycle-0001",
	})
	assertReceiptShape(t, releaseResult)
	if releaseResult["resource_version"] != uint64(6) {
		t.Fatalf("release version = %v", releaseResult["resource_version"])
	}

	var persisted models.Ticket
	if err := fixture.db.First(&persisted, ticketID).Error; err != nil {
		t.Fatalf("load final ticket: %v", err)
	}
	if persisted.Version != 6 ||
		persisted.Status != models.TicketStatusInProgress ||
		persisted.Title != "Updated over MCP" ||
		ticketQueue(&persisted) != "operations" {
		t.Fatalf("final ticket: %+v", persisted)
	}

	select {
	case event := <-fixture.adapter.Events():
		if !strings.HasPrefix(event.URI, "ticket://") {
			t.Fatalf("invalid resource event: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("no MCP resource event was published")
	}
}

func TestMCPAdapterTicketCreateRequiresCanonicalConfigurationVersions(
	t *testing.T,
) {
	fixture := newMCPAdapterFixture(t)
	base := map[string]any{
		"title":           "Rejected MCP ticket",
		"description":     "Configuration versions are mandatory.",
		"type":            "request",
		"priority":        "normal",
		"idempotency_key": "mcp-version-contract-0001",
	}
	for _, test := range []struct {
		name      string
		arguments map[string]any
	}{
		{name: "missing", arguments: base},
		{
			name: "invalid UUID",
			arguments: func() map[string]any {
				result := cloneStringAnyMap(base)
				result["request_type_version_id"] = "not-a-uuid"
				result["workflow_version_id"] = "also-not-a-uuid"
				return result
			}(),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := fixture.callTool(
				context.Background(),
				"ticket_create",
				test.arguments,
			)
			var backendErr *mcp.BackendError
			if !errors.As(err, &backendErr) ||
				backendErr.Code != "invalid_argument" {
				t.Fatalf("error=%#v, want invalid_argument", err)
			}
			var count int64
			if err := fixture.db.Model(&models.Ticket{}).
				Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("invalid MCP intake created %d ticket(s)", count)
			}
		})
	}
}

func TestMCPAdapterRejectsAttachmentHashAndConstructorMisconfiguration(t *testing.T) {
	fixture := newMCPAdapterFixture(t)
	ticket := fixture.seedTicket(t, "MCP-HASH-001", "default")
	_, err := fixture.callTool(context.Background(), "ticket_attach_file", map[string]any{
		"ticket_id":        int64(ticket.ID),
		"expected_version": int64(1),
		"lease_id":         "lease-not-used-before-hash-rejection",
		"file_name":        "evidence.txt",
		"content_type":     "text/plain",
		"content_base64":   base64.StdEncoding.EncodeToString([]byte("hello")),
		"sha256":           strings.Repeat("0", 64),
		"visibility":       "internal",
		"idempotency_key":  "attach-invalid-0001",
	})
	var failure *mcp.BackendError
	if !errors.As(err, &failure) || failure.Code != "invalid_argument" {
		t.Fatalf("hash mismatch error = %v", err)
	}
	var attachmentCount int64
	if err := fixture.db.Model(&models.TicketAttachment{}).Count(&attachmentCount).Error; err != nil || attachmentCount != 0 {
		t.Fatalf("invalid attachment count=%d err=%v", attachmentCount, err)
	}

	if _, err := NewMCPAdapter(nil, fixture.service, fixture.manager); err == nil {
		t.Fatal("NewMCPAdapter accepted nil DB")
	}
	if _, err := NewMCPAdapter(fixture.db, nil, fixture.manager); err == nil {
		t.Fatal("NewMCPAdapter accepted nil service")
	}
	if _, err := NewMCPAdapter(fixture.db, fixture.service, nil); err == nil {
		t.Fatal("NewMCPAdapter accepted nil token manager")
	}
}

func TestMCPAdapterCommentAndAttachmentRequireLeaseBeforeDomainWork(t *testing.T) {
	fixture := newMCPAdapterFixture(t)
	ticket := fixture.seedTicket(t, "MCP-LEASE-REQUIRED-001", "default")

	var decisionsBefore, idempotencyBefore int64
	if err := fixture.db.Model(&models.PolicyDecision{}).Count(&decisionsBefore).Error; err != nil {
		t.Fatalf("count decisions before: %v", err)
	}
	if err := fixture.db.Model(&models.IdempotencyRecord{}).Count(&idempotencyBefore).Error; err != nil {
		t.Fatalf("count idempotency before: %v", err)
	}

	// A missing lease must be rejected before even the shared execution gate
	// enters the domain service.
	fixture.service.SetGlobalEmergencyStop(true)
	t.Cleanup(func() {
		fixture.service.SetGlobalEmergencyStop(false)
	})
	cases := []struct {
		name      string
		arguments map[string]any
	}{
		{
			name: "ticket_add_comment",
			arguments: map[string]any{
				"ticket_id":        int64(ticket.ID),
				"expected_version": int64(1),
				"lease_id":         "   ",
				"visibility":       "internal",
				"content":          "must not be persisted",
				"content_type":     "text",
				"reason":           "validate lease",
				"idempotency_key":  "idem-comment-1",
			},
		},
		{
			name: "ticket_attach_file",
			arguments: map[string]any{
				"ticket_id":        int64(ticket.ID),
				"expected_version": int64(1),
				"file_name":        "evidence.txt",
				"content_type":     "text/plain",
				"content_base64":   base64.StdEncoding.EncodeToString([]byte("hello")),
				"sha256":           "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
				"visibility":       "internal",
				"idempotency_key":  "idem-attach-1",
			},
		},
	}
	for _, testCase := range cases {
		_, err := fixture.callTool(
			context.Background(),
			testCase.name,
			testCase.arguments,
		)
		var failure *mcp.BackendError
		if !errors.As(err, &failure) ||
			failure.Code != "invalid_params" ||
			failure.Details["field"] != "lease_id" {
			t.Fatalf("%s missing lease error = %#v, err=%v", testCase.name, failure, err)
		}
	}

	for modelName, model := range map[string]any{
		"comment":    &models.TicketComment{},
		"attachment": &models.TicketAttachment{},
	} {
		var count int64
		if err := fixture.db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v", modelName, count, err)
		}
	}
	var decisionsAfter, idempotencyAfter int64
	if err := fixture.db.Model(&models.PolicyDecision{}).Count(&decisionsAfter).Error; err != nil {
		t.Fatalf("count decisions after: %v", err)
	}
	if err := fixture.db.Model(&models.IdempotencyRecord{}).Count(&idempotencyAfter).Error; err != nil {
		t.Fatalf("count idempotency after: %v", err)
	}
	if decisionsAfter != decisionsBefore || idempotencyAfter != idempotencyBefore {
		t.Fatalf(
			"missing lease reached policy/idempotency work: decisions %d->%d idempotency %d->%d",
			decisionsBefore,
			decisionsAfter,
			idempotencyBefore,
			idempotencyAfter,
		)
	}
}

func TestMCPAdapterListTicketsUsesBoundedPolicyBatchAndRawCursor(t *testing.T) {
	fixture := newMCPAdapterFixture(t)
	baseTime := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	tickets := make([]models.Ticket, 601)
	for i := range tickets {
		createdAt := baseTime.Add(time.Duration(i) * time.Microsecond)
		tickets[i] = models.Ticket{
			CreatedAt:          createdAt,
			UpdatedAt:          createdAt,
			OrganizationID:     fixture.organization.ID,
			ProjectID:          fixture.project.ID,
			QueueID:            fixture.queue.ID,
			TicketNumber:       fmt.Sprintf("MCP-BOUNDED-%03d", i+1),
			Title:              fmt.Sprintf("Bounded ticket %03d", i+1),
			Description:        "Untrusted content",
			Type:               models.TicketTypeRequest,
			Priority:           models.TicketPriorityNormal,
			Status:             models.TicketStatusOpen,
			Source:             models.TicketSourceAgent,
			Version:            1,
			TrustLevel:         models.TicketTrustLevelUntrusted,
			CreatedByID:        &fixture.user.ID,
			CreatedByActorType: models.ActorTypeHuman,
			CreatedByActorID:   strconv.FormatUint(uint64(fixture.user.ID), 10),
		}
	}
	if err := fixture.db.CreateInBatches(&tickets, 25).Error; err != nil {
		t.Fatalf("seed bounded tickets: %v", err)
	}
	if _, err := fixture.service.CreateAgentPolicy(context.Background(), services.CreateAgentPolicyInput{
		ServicePrincipalID: fixture.principal.ID,
		Name:               "deny all ticket objects",
		Effect:             models.AgentPolicyEffectDeny,
		Scope:              models.ScopeTicketsRead,
		Action:             "ticket.read",
		ResourceType:       "ticket",
		ResourceID:         "*",
		Priority:           100,
	}); err != nil {
		t.Fatalf("create ticket read deny policy: %v", err)
	}

	if err := fixture.authorize(
		context.Background(),
		mcp.AuthorizationRequest{
			Action:         "ticket_list",
			RequiredScopes: []string{models.ScopeTicketsRead},
		},
	); err != nil {
		t.Fatalf("authorize ticket_list: %v", err)
	}
	var preflightDecisions int64
	if err := fixture.db.Model(&models.PolicyDecision{}).
		Where("action = ? AND source_protocol = ?", "ticket.list", mcpSourceProtocol).
		Count(&preflightDecisions).Error; err != nil {
		t.Fatalf("count preflight list decisions: %v", err)
	}
	if preflightDecisions != 0 {
		t.Fatalf("ticket_list preflight persisted %d decisions", preflightDecisions)
	}

	var queryCount atomic.Int64
	callbackName := fmt.Sprintf("mcp-bounded-query-count-%d", mcpAdapterTestSequence.Load())
	if err := fixture.db.Callback().Query().Before("gorm:query").Register(
		callbackName,
		func(_ *gorm.DB) {
			queryCount.Add(1)
		},
	); err != nil {
		t.Fatalf("register query counter: %v", err)
	}
	firstPage, err := fixture.callTool(
		context.Background(),
		"ticket_list",
		map[string]any{"limit": int64(100)},
	)
	if err != nil {
		t.Fatalf("first bounded ticket_list: %v", err)
	}
	if queries := queryCount.Load(); queries > 8 {
		t.Fatalf("first bounded ticket_list used %d queries; possible N+1 authorization", queries)
	}
	if items := firstPage["items"].([]map[string]any); len(items) != 0 {
		t.Fatalf("denied page returned items: %#v", items)
	}
	firstCursor, ok := firstPage["next_cursor"].(string)
	if !ok || firstCursor == "" {
		t.Fatalf("deny-heavy page did not advance cursor: %#v", firstPage)
	}
	decodedCursor, err := DecodeCursor(firstCursor)
	if err != nil {
		t.Fatalf("decode first cursor: %v", err)
	}
	// The 500th raw candidate in descending order is tickets[101].
	if decodedCursor.ID != strconv.FormatUint(uint64(tickets[101].ID), 10) ||
		!decodedCursor.CreatedAt.Equal(tickets[101].CreatedAt) {
		t.Fatalf(
			"cursor=%+v, want last examined raw candidate id=%d created_at=%s",
			decodedCursor,
			tickets[101].ID,
			tickets[101].CreatedAt,
		)
	}

	assertMCPListPolicySummary(t, fixture, 1, 500, 500, 0, true)
	var perCandidateDecisions int64
	if err := fixture.db.Model(&models.PolicyDecision{}).
		Where("action = ? AND source_protocol = ?", "ticket.read", mcpSourceProtocol).
		Count(&perCandidateDecisions).Error; err != nil {
		t.Fatalf("count per-candidate decisions: %v", err)
	}
	if perCandidateDecisions != 0 {
		t.Fatalf("ticket_list persisted %d per-candidate decisions", perCandidateDecisions)
	}

	queryCount.Store(0)
	secondPage, err := fixture.callTool(
		context.Background(),
		"ticket_list",
		map[string]any{"limit": int64(100), "cursor": firstCursor},
	)
	if err != nil {
		t.Fatalf("second bounded ticket_list: %v", err)
	}
	if queries := queryCount.Load(); queries > 8 {
		t.Fatalf("second bounded ticket_list used %d queries; possible N+1 authorization", queries)
	}
	if items := secondPage["items"].([]map[string]any); len(items) != 0 {
		t.Fatalf("second denied page returned items: %#v", items)
	}
	if cursor, exists := secondPage["next_cursor"]; exists {
		t.Fatalf("exhausted raw stream returned cursor %v", cursor)
	}
	assertMCPListPolicySummary(t, fixture, 2, 500, 101, 0, false)
}

func assertMCPListPolicySummary(
	t *testing.T,
	fixture *mcpAdapterFixture,
	wantCount int,
	wantBudget int,
	wantScanned int,
	wantReturned int,
	wantHasMore bool,
) {
	t.Helper()
	var decisions []models.PolicyDecision
	if err := fixture.db.
		Where("action = ? AND source_protocol = ?", "ticket.list", mcpSourceProtocol).
		Order("created_at ASC").
		Find(&decisions).Error; err != nil {
		t.Fatalf("load list policy summaries: %v", err)
	}
	if len(decisions) != wantCount {
		t.Fatalf("ticket_list decisions=%d, want=%d", len(decisions), wantCount)
	}
	var summary map[string]any
	if err := json.Unmarshal(decisions[len(decisions)-1].Context, &summary); err != nil {
		t.Fatalf("decode list policy summary: %v", err)
	}
	if summary["candidate_budget"] != float64(wantBudget) ||
		summary["candidates_scanned"] != float64(wantScanned) ||
		summary["items_returned"] != float64(wantReturned) ||
		summary["items_filtered"] != float64(wantScanned-wantReturned) ||
		summary["has_more"] != wantHasMore ||
		summary["cursor_semantics"] != "last_examined_candidate" {
		t.Fatalf("list policy summary=%#v", summary)
	}
}

func TestMCPAdapterResultsSatisfyAdvertisedMCPOutputSchemas(t *testing.T) {
	fixture := newMCPAdapterFixture(t)
	gin.SetMode(gin.TestMode)
	protocolServer, err := mcp.NewServer(
		fixture.adapter,
		fixture.adapter,
		mcp.WithAuthorizer(fixture.adapter),
	)
	if err != nil {
		t.Fatalf("create MCP server: %v", err)
	}
	defer protocolServer.Close()
	router := gin.New()
	router.Any("/mcp", protocolServer.Handler())
	httpServer := httptest.NewServer(router)
	defer httpServer.Close()

	post := func(payload map[string]any) (*http.Response, map[string]any) {
		t.Helper()
		method, _ := payload["method"].(string)
		params, _ := payload["params"].(map[string]any)
		if params == nil {
			params = map[string]any{}
			payload["params"] = params
		}
		params["_meta"] = map[string]any{
			"io.modelcontextprotocol/protocolVersion": mcp.ProtocolVersion,
			"io.modelcontextprotocol/clientCapabilities": map[string]any{
				"extensions": map[string]any{},
			},
			"io.modelcontextprotocol/clientInfo": map[string]any{
				"name":    "adapter-contract-test",
				"version": "1",
			},
		}
		body, _ := json.Marshal(payload)
		request, requestErr := http.NewRequest(http.MethodPost, httpServer.URL+"/mcp", bytes.NewReader(body))
		if requestErr != nil {
			t.Fatalf("create request: %v", requestErr)
		}
		request.Header.Set("Authorization", "Bearer "+fixture.token)
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(mcp.HeaderProtocolVersion, mcp.ProtocolVersion)
		request.Header.Set(mcp.HeaderMethod, method)
		if method == "tools/call" {
			if name, _ := params["name"].(string); name != "" {
				request.Header.Set(mcp.HeaderName, name)
			}
		}
		response, requestErr := httpServer.Client().Do(request)
		if requestErr != nil {
			t.Fatalf("MCP POST: %v", requestErr)
		}
		if response.StatusCode == http.StatusAccepted || response.StatusCode == http.StatusNoContent {
			return response, nil
		}
		var decoded map[string]any
		if decodeErr := json.NewDecoder(response.Body).Decode(&decoded); decodeErr != nil {
			raw, _ := io.ReadAll(response.Body)
			t.Fatalf("decode MCP response: %v body=%s", decodeErr, raw)
		}
		return response, decoded
	}

	call := func(id int, name string, arguments map[string]any) map[string]any {
		t.Helper()
		arguments = withMCPProjectKey(arguments, string(fixture.project.Key))
		response, payload := post(map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  "tools/call",
			"params":  map[string]any{"name": name, "arguments": arguments},
		})
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK || payload["error"] != nil {
			t.Fatalf("%s protocol error: status=%d payload=%#v", name, response.StatusCode, payload)
		}
		result := payload["result"].(map[string]any)
		if result["isError"] == true {
			t.Fatalf("%s execution error: %#v", name, result)
		}
		envelope := result["structuredContent"].(map[string]any)
		if envelope["ok"] != true {
			t.Fatalf("%s invalid envelope: %#v", name, envelope)
		}
		return envelope["data"].(map[string]any)
	}

	create := call(2, "ticket_create", map[string]any{
		"title":                   "Schema contract ticket",
		"description":             "Untrusted",
		"type":                    "request",
		"priority":                "normal",
		"request_type_version_id": fixture.requestTypeVersionID,
		"workflow_version_id":     fixture.workflowVersionID,
		"idempotency_key":         "schema-create-0001",
	})
	ticketID, _ := strconv.ParseUint(create["resource_id"].(string), 10, 64)
	claim := call(3, "ticket_claim", map[string]any{
		"ticket_id":        ticketID,
		"expected_version": 1,
		"idempotency_key":  "schema-claim-0001",
	})
	leaseID := claim["lease_id"].(string)
	call(4, "ticket_add_comment", map[string]any{
		"ticket_id":        ticketID,
		"expected_version": 1,
		"lease_id":         leaseID,
		"visibility":       "internal",
		"content":          "Schema-safe comment",
		"content_type":     "text",
		"reason":           "contract test",
		"idempotency_key":  "schema-comment-0001",
	})
	call(5, "ticket_attach_file", map[string]any{
		"ticket_id":        ticketID,
		"expected_version": 2,
		"lease_id":         leaseID,
		"file_name":        "schema.txt",
		"content_type":     "text/plain",
		"content_base64":   "aGVsbG8=",
		"sha256":           "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
		"visibility":       "internal",
		"idempotency_key":  "schema-attach-0001",
	})
	call(6, "ticket_get", map[string]any{"ticket_id": ticketID})
	call(7, "ticket_list", map[string]any{"limit": 10})
	call(8, "ticket_history", map[string]any{"ticket_id": ticketID, "limit": 10})
	call(9, "action_check", map[string]any{"action": "ticket_update", "ticket_id": ticketID})
}

func callMCPTool(
	t *testing.T,
	fixture *mcpAdapterFixture,
	name string,
	arguments map[string]any,
) map[string]any {
	t.Helper()
	result, err := fixture.callTool(context.Background(), name, arguments)
	if err != nil {
		t.Fatalf("%s failed: %v", name, err)
	}
	return result
}

func withMCPProjectKey(arguments map[string]any, projectKey string) map[string]any {
	result := make(map[string]any, len(arguments)+1)
	for key, value := range arguments {
		result[key] = value
	}
	result["project_key"] = projectKey
	return result
}

func assertReceiptShape(t *testing.T, receipt map[string]any) {
	t.Helper()
	for _, field := range []string{
		"operation_id",
		"resource_id",
		"resource_version",
		"event_id",
		"changed_fields",
		"policy_decision_id",
	} {
		if _, ok := receipt[field]; !ok {
			t.Fatalf("receipt missing %s: %#v", field, receipt)
		}
	}
	if receipt["operation_id"] == "" ||
		receipt["resource_id"] == "" ||
		receipt["event_id"] == "" ||
		receipt["policy_decision_id"] == "" {
		t.Fatalf("receipt contains empty identifiers: %#v", receipt)
	}
}

func assertLeaseShape(t *testing.T, result map[string]any) {
	t.Helper()
	assertReceiptShape(t, result["receipt"].(map[string]any))
	if result["lease_id"] == "" || result["expires_at"] == "" || result["ticket_version"] == nil {
		t.Fatalf("invalid lease result: %#v", result)
	}
}
