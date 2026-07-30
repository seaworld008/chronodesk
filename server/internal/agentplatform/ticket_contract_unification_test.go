package agentplatform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/agentauth"
	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestAgentRESTContractRejectsRawAssignmentProjectionPatch(t *testing.T) {
	fixture := newMCPAdapterFixture(t)
	allowAgentTicketAction(
		t,
		fixture,
		models.ScopeTicketsAssign,
		"ticket.assign",
	)
	ticket := fixture.seedTicket(t, "UNIFIED-REST-RAW-ASSIGN", "")
	leaseID := claimMCPContractTicket(
		t,
		fixture,
		ticket,
		"claim-rest-raw-assignment",
	)

	response := patchAgentRESTTicket(t, fixture, ticket, leaseID, map[string]any{
		"assigned_to_id":                   fixture.user.ID,
		"assigned_to_actor_type":           models.ActorTypeServicePrincipal,
		"assigned_to_actor_id":             fixture.principal.ID,
		"assigned_to_service_principal_id": fixture.principal.ID,
	})
	if response.Code != http.StatusBadRequest {
		t.Errorf(
			"raw assignment projection PATCH status=%d body=%s, want 400",
			response.Code,
			response.Body.String(),
		)
	}
	assertAgentRESTTicketUnchanged(t, fixture.db, ticket)
}

func TestAgentRESTContractEscalationCannotUseOrdinaryUpdateAuthority(t *testing.T) {
	fixture := newMCPAdapterFixture(t)
	allowAgentTicketAction(
		t,
		fixture,
		models.ScopeTicketsUpdate,
		"ticket.update",
	)
	ticket := fixture.seedTicket(t, "UNIFIED-REST-RAW-ESCALATE", "")
	leaseID := claimMCPContractTicket(
		t,
		fixture,
		ticket,
		"claim-rest-raw-escalation",
	)

	response := patchAgentRESTTicket(t, fixture, ticket, leaseID, map[string]any{
		"is_escalated": true,
	})
	if response.Code != http.StatusBadRequest {
		t.Errorf(
			"is_escalated ordinary PATCH status=%d body=%s, want 400 and a dedicated risky transition command",
			response.Code,
			response.Body.String(),
		)
	}
	assertAgentRESTTicketUnchanged(t, fixture.db, ticket)
}

func patchAgentRESTTicket(
	t *testing.T,
	fixture *mcpAdapterFixture,
	ticket models.Ticket,
	leaseID string,
	patch map[string]any,
) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(patch)
	if err != nil {
		t.Fatalf("encode Agent REST patch: %v", err)
	}
	gin.SetMode(gin.TestMode)
	handler := &APIHandler{db: fixture.db, native: fixture.service}
	router := gin.New()
	router.PATCH("/tickets/:id", func(c *gin.Context) {
		c.Set(agentauth.ContextPrincipalID, fixture.principal.ID)
		c.Set(agentauth.ContextCredentialID, fixture.credential.ID)
		c.Set(agentauth.ContextScopes, append([]string(nil), models.SupportedAgentScopes...))
		handler.UpdateTicket(c)
	})
	request := httptest.NewRequest(
		http.MethodPatch,
		fmt.Sprintf("/tickets/%d", ticket.ID),
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", fmt.Sprintf(`"v%d"`, ticket.Version))
	request.Header.Set("X-Ticket-Lease", leaseID)
	request.Header.Set(
		"Idempotency-Key",
		fmt.Sprintf("rest-unified-%d-%s", ticket.ID, ticket.TicketNumber),
	)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func assertAgentRESTTicketUnchanged(
	t *testing.T,
	db *gorm.DB,
	before models.Ticket,
) {
	t.Helper()
	var after models.Ticket
	if err := db.First(&after, before.ID).Error; err != nil {
		t.Fatalf("reload Agent REST ticket: %v", err)
	}
	if after.Version != before.Version ||
		after.AssignedToID != nil ||
		after.AssignedToActorType != "" ||
		after.AssignedToActorID != "" ||
		after.AssignedToServicePrincipalID != nil ||
		after.IsEscalated != before.IsEscalated {
		t.Errorf(
			"rejected Agent REST patch changed durable ticket: before(version=%d escalated=%v) after(version=%d assigned_to=%v actor=(%q,%q) service_principal=%v escalated=%v)",
			before.Version,
			before.IsEscalated,
			after.Version,
			after.AssignedToID,
			after.AssignedToActorType,
			after.AssignedToActorID,
			after.AssignedToServicePrincipalID,
			after.IsEscalated,
		)
	}
}

func TestMCPAssignmentAndTransitionCommitNotificationOutbox(t *testing.T) {
	t.Run("assignment", func(t *testing.T) {
		fixture := newMCPAdapterFixture(t)
		allowAgentTicketAction(
			t,
			fixture,
			models.ScopeTicketsAssign,
			"ticket.assign",
		)
		ticket := fixture.seedTicket(t, "UNIFIED-MCP-ASSIGN", "")
		leaseID := claimMCPContractTicket(t, fixture, ticket, "claim-unified-assign")

		callMCPTool(t, fixture, "ticket_assign", map[string]any{
			"ticket_id":        int64(ticket.ID),
			"expected_version": int64(ticket.Version),
			"lease_id":         leaseID,
			"assignee": map[string]any{
				"type": string(models.ActorTypeHuman),
				"id":   strconv.FormatUint(uint64(fixture.user.ID), 10),
			},
			"reason":          "domain notification contract",
			"idempotency_key": "assign-unified-contract",
		})

		assertAgentNotificationOutbox(
			t,
			fixture.db,
			eventcontract.TicketAssignedEventType,
			ticket.ID,
			[]string{fmt.Sprintf(
				"%s:%d",
				models.NotificationTypeTicketAssigned,
				fixture.user.ID,
			)},
		)
	})

	t.Run("status transition", func(t *testing.T) {
		fixture := newMCPAdapterFixture(t)
		allowAgentTicketAction(
			t,
			fixture,
			models.ScopeTicketsTransition,
			"ticket.transition",
		)
		ticket := fixture.seedTicket(t, "UNIFIED-MCP-TRANSITION", "")
		if err := fixture.db.Model(&models.Ticket{}).
			Where("id = ?", ticket.ID).
			Updates(map[string]any{
				"assigned_to_id":         fixture.user.ID,
				"assigned_to_actor_type": models.ActorTypeHuman,
				"assigned_to_actor_id":   models.HumanActor(fixture.user.ID).ID,
			}).Error; err != nil {
			t.Fatalf("seed canonical assignment: %v", err)
		}
		leaseID := claimMCPContractTicket(t, fixture, ticket, "claim-unified-transition")

		callMCPTool(t, fixture, "ticket_transition", map[string]any{
			"ticket_id":        int64(ticket.ID),
			"expected_version": int64(ticket.Version),
			"lease_id":         leaseID,
			"status":           string(models.TicketStatusInProgress),
			"reason":           "domain notification contract",
			"idempotency_key":  "transition-unified-contract",
		})

		assertAgentNotificationOutbox(
			t,
			fixture.db,
			eventcontract.TicketTransitionedEventType,
			ticket.ID,
			[]string{fmt.Sprintf(
				"%s:%d",
				models.NotificationTypeTicketStatusChanged,
				fixture.user.ID,
			)},
		)
	})
}

func TestAgentCreateUsesAuthoritativeSLAConfigProjection(t *testing.T) {
	fixture := newMCPAdapterFixture(t)
	allowAgentTicketAction(
		t,
		fixture,
		models.ScopeTicketsCreate,
		"ticket.create",
	)
	slaHours := 1
	category := models.Category{
		Name:      "Agent SLA contract",
		Slug:      "agent-sla-contract",
		Type:      models.CategoryTypeSupport,
		Status:    models.CategoryStatusActive,
		SLAHours:  &slaHours,
		CreatedBy: fixture.user.ID,
	}
	if err := fixture.db.Create(&category).Error; err != nil {
		t.Fatalf("create SLA category: %v", err)
	}
	config := models.SLAConfig{
		OrganizationID:  fixture.organization.ID,
		ProjectID:       fixture.project.ID,
		Name:            "Agent SLA contract",
		IsActive:        true,
		IsDefault:       true,
		ResponseTime:    60,
		ResolutionTime:  240,
		ExcludeWeekends: true,
	}
	if err := fixture.db.Create(&config).Error; err != nil {
		t.Fatalf("create authoritative SLA config: %v", err)
	}

	operationContext, err := services.WithOperationContext(
		context.Background(),
		services.OperationContext{
			Scope:        fixture.project.Scope(),
			Actor:        models.ServicePrincipalActor(fixture.principal.ID),
			Source:       services.SourceProtocolMCP,
			CredentialID: fixture.credential.ID,
		},
	)
	if err != nil {
		t.Fatalf("create Agent SLA operation context: %v", err)
	}
	result, err := fixture.service.CreateNativeTicket(
		operationContext,
		services.NativeTicketCreateInput{
			Request: models.TicketCreateRequest{
				Title:                "Agent category SLA",
				Description:          "Agent intake must share human SLA initialization",
				Type:                 models.TicketTypeRequest,
				Priority:             models.TicketPriorityNormal,
				Source:               models.TicketSourceAgent,
				CategoryID:           &category.ID,
				RequestTypeVersionID: fixture.requestTypeVersionID,
				WorkflowVersionID:    fixture.workflowVersionID,
			},
			Actor:          models.ServicePrincipalActor(fixture.principal.ID),
			CredentialID:   fixture.credential.ID,
			SourceProtocol: "rest",
			RequestDigest:  "agent-create-category-sla-contract",
			TrustLevel:     models.TicketTrustLevelUntrusted,
		},
	)
	if err != nil {
		t.Fatalf("Agent create failed before SLA assertion: %v", err)
	}
	if result.Ticket.SLADueDate == nil {
		t.Fatal(
			"Agent create persisted category_id without the authoritative SLA projection",
		)
	}
	_, want, err := services.NewSLAService(fixture.db).
		CalculateDeadlines(operationContext, result.Ticket, &config)
	if err != nil {
		t.Fatalf("calculate expected SLA projection: %v", err)
	}
	if !result.Ticket.SLADueDate.Equal(want) {
		t.Fatalf("Agent SLA deadline = %v, want %v", result.Ticket.SLADueDate, want)
	}
}

func allowAgentTicketAction(
	t *testing.T,
	fixture *mcpAdapterFixture,
	scope string,
	action string,
) {
	t.Helper()
	if _, err := fixture.service.CreateAgentPolicy(
		context.Background(),
		services.CreateAgentPolicyInput{
			ServicePrincipalID: fixture.principal.ID,
			Name:               "allow " + action,
			Effect:             models.AgentPolicyEffectAllow,
			Scope:              scope,
			Action:             action,
			ResourceType:       "ticket",
		},
	); err != nil {
		t.Fatalf("create %s policy: %v", action, err)
	}
}

func claimMCPContractTicket(
	t *testing.T,
	fixture *mcpAdapterFixture,
	ticket models.Ticket,
	idempotencyKey string,
) string {
	t.Helper()
	result := callMCPTool(t, fixture, "ticket_claim", map[string]any{
		"ticket_id":        int64(ticket.ID),
		"expected_version": int64(ticket.Version),
		"lease_seconds":    int64(60),
		"idempotency_key":  idempotencyKey,
	})
	leaseID, ok := result["lease_id"].(string)
	if !ok || leaseID == "" {
		t.Fatalf("ticket_claim returned invalid lease: %#v", result)
	}
	return leaseID
}

func assertAgentNotificationOutbox(
	t *testing.T,
	db *gorm.DB,
	eventType string,
	ticketID uint,
	wantDestinations []string,
) {
	t.Helper()
	var event models.DomainEvent
	if err := db.
		Where("type = ? AND subject = ?", eventType, fmt.Sprintf("ticket/%d", ticketID)).
		Order("created_at DESC").
		First(&event).Error; err != nil {
		t.Fatalf("load %s event for ticket %d: %v", eventType, ticketID, err)
	}
	var deliveries []models.OutboxDelivery
	if err := db.
		Where(
			"event_id = ? AND destination_type = ?",
			event.ID,
			services.NotificationOutboxDestination,
		).
		Order("destination_id ASC").
		Find(&deliveries).Error; err != nil {
		t.Fatalf("load Agent notification Outbox for event %s: %v", event.ID, err)
	}
	gotDestinations := make([]string, 0, len(deliveries))
	for _, delivery := range deliveries {
		if delivery.Status != models.OutboxDeliveryPending {
			t.Fatalf(
				"Agent notification Outbox %s state = %q, want pending",
				delivery.ID,
				delivery.Status,
			)
		}
		gotDestinations = append(gotDestinations, delivery.DestinationID)
	}
	sort.Strings(gotDestinations)
	sort.Strings(wantDestinations)
	if fmt.Sprint(gotDestinations) != fmt.Sprint(wantDestinations) {
		t.Fatalf(
			"ticket %d Agent notification Outbox destinations = %v, want %v",
			ticketID,
			gotDestinations,
			wantDestinations,
		)
	}
}
