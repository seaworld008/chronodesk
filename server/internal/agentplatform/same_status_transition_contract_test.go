package agentplatform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/a2a"
	"github.com/seaworld008/chronodesk/server/internal/httpcontract"
	"github.com/seaworld008/chronodesk/server/internal/mcp"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/gorm"
)

func TestAgentRESTRejectsSameStatusTransitionWithoutObservableMutation(
	t *testing.T,
) {
	fixture := newMCPAdapterFixture(t)
	allowAgentTicketAction(
		t,
		fixture,
		models.ScopeTicketsTransition,
		"ticket.transition",
	)
	ticket := fixture.seedTicket(t, "REST-SAME-STATUS-TRANSITION", "")
	leaseID := claimMCPContractTicket(
		t,
		fixture,
		ticket,
		"claim-rest-same-status-transition",
	)
	const idempotencyKey = "rest-same-status-transition"
	baseline := captureSameStatusTransitionBaseline(
		t,
		fixture.db,
		ticket.ID,
		fixture.principal.ID,
		"ticket.transition",
		idempotencyKey,
	)

	response := performAgentRESTJSON(
		t,
		newAgentRESTCommandRouter(fixture),
		http.MethodPost,
		fmt.Sprintf(
			"/api/v2/projects/TEST/tickets/%d/commands/transition",
			ticket.ID,
		),
		fixture.token,
		map[string]string{
			"If-Match":         httpcontract.FormatETag(ticket.Version),
			"Idempotency-Key":  idempotencyKey,
			"X-Ticket-Lease":   leaseID,
			"X-Correlation-ID": "corr-rest-same-status-transition",
		},
		map[string]any{
			"status": string(ticket.Status),
			"reason": "same canonical status must not manufacture a transition",
		},
	)
	assertAgentRESTProblem(
		t,
		response,
		http.StatusBadRequest,
		"invalid_ticket_transition",
	)
	var problem map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode same-status REST problem: %v", err)
	}
	var typedProblem Problem
	if err := json.Unmarshal(response.Body.Bytes(), &typedProblem); err != nil {
		t.Fatalf("decode typed same-status REST problem: %v", err)
	}
	if typedProblem.Retryable {
		t.Fatalf("same-status REST rejection was marked retryable: %+v", typedProblem)
	}
	if _, exists := problem["receipt"]; exists {
		t.Fatalf("same-status REST rejection returned a success receipt: %s", response.Body.String())
	}
	if _, exists := problem["data"]; exists {
		t.Fatalf("same-status REST rejection returned a Ticket snapshot: %s", response.Body.String())
	}
	assertSameStatusTransitionRejected(
		t,
		fixture.db,
		baseline,
	)
}

func TestMCPRejectsSameStatusTransitionWithoutObservableMutation(t *testing.T) {
	fixture := newMCPAdapterFixture(t)
	allowAgentTicketAction(
		t,
		fixture,
		models.ScopeTicketsTransition,
		"ticket.transition",
	)
	ticket := fixture.seedTicket(t, "MCP-SAME-STATUS-TRANSITION", "")
	leaseID := claimMCPContractTicket(
		t,
		fixture,
		ticket,
		"claim-mcp-same-status-transition",
	)
	const idempotencyKey = "mcp-same-status-transition"
	baseline := captureSameStatusTransitionBaseline(
		t,
		fixture.db,
		ticket.ID,
		fixture.principal.ID,
		"ticket.transition",
		idempotencyKey,
	)

	result, err := fixture.callTool(
		context.Background(),
		"ticket_transition",
		map[string]any{
			"ticket_id":        int64(ticket.ID),
			"expected_version": int64(ticket.Version),
			"lease_id":         leaseID,
			"status":           string(ticket.Status),
			"reason":           "same canonical status must not manufacture a transition",
			"idempotency_key":  idempotencyKey,
		},
	)
	var backendErr *mcp.BackendError
	if !errors.As(err, &backendErr) ||
		backendErr.Code != "invalid_argument" ||
		backendErr.Retryable {
		t.Fatalf("same-status MCP transition result=%v error=%v", result, err)
	}
	if result != nil {
		t.Fatalf("same-status MCP rejection returned a success result: %#v", result)
	}
	assertSameStatusTransitionRejected(
		t,
		fixture.db,
		baseline,
	)
}

func TestA2ARejectsSameStatusTransitionWithoutObservableMutation(t *testing.T) {
	fixture := newA2AAdapterFixture(t)
	ticket := seedA2AQueryTicket(t, fixture, "A2A-SAME-STATUS-TRANSITION")
	now := time.Now().UTC()
	lease := models.TicketLease{
		ID:              "a2a-same-status-transition-lease",
		OrganizationID:  fixture.organization.ID,
		ProjectID:       fixture.project.ID,
		TicketID:        ticket.ID,
		HolderActorType: models.ActorTypeServicePrincipal,
		HolderActorID:   fixture.principal.ID,
		TicketVersion:   ticket.Version,
		ExpiresAt:       now.Add(time.Minute),
		LastHeartbeatAt: now,
	}
	if err := fixture.db.Create(&lease).Error; err != nil {
		t.Fatalf("seed same-status A2A lease: %v", err)
	}
	if _, err := fixture.native.CreateAgentPolicy(
		context.Background(),
		services.CreateAgentPolicyInput{
			ServicePrincipalID: fixture.principal.ID,
			Name:               "allow A2A same-status transition",
			Effect:             models.AgentPolicyEffectAllow,
			Scope:              models.ScopeTicketsTransition,
			Action:             "ticket.transition",
			ResourceType:       "ticket",
			ResourceID:         strconvUint(ticket.ID),
			Priority:           100,
		},
	); err != nil {
		t.Fatalf("create same-status A2A transition policy: %v", err)
	}
	message := structuredA2AMessage(t, "ticket-work", map[string]any{
		"operation":        "transition",
		"ticket_id":        ticket.ID,
		"expected_version": ticket.Version,
		"lease_id":         lease.ID,
		"status":           ticket.Status,
		"reason":           "same canonical status must not manufacture a transition",
	})
	message.MessageID = "a2a-same-status-transition"
	baseline := captureSameStatusTransitionBaseline(
		t,
		fixture.db,
		ticket.ID,
		fixture.principal.ID,
		"a2a.ticket-work.transition",
		message.MessageID,
	)
	reporter := &recordingA2AReporter{}

	if err := fixture.backend.Process(
		context.Background(),
		a2a.Task{
			ID:        "task-a2a-same-status-transition",
			ContextID: "context-a2a-same-status-transition",
		},
		message,
		reporter,
	); err != nil {
		t.Fatalf("process same-status A2A transition: %v", err)
	}
	if reporter.lastState() != a2a.TaskStateInputRequired {
		t.Fatalf(
			"same-status A2A state = %s, want INPUT_REQUIRED",
			reporter.lastState(),
		)
	}
	statusMessage := reporter.lastStatusMessage()
	if statusMessage == nil || len(statusMessage.Parts) != 1 {
		t.Fatalf("same-status A2A rejection omitted structured error: %#v", statusMessage)
	}
	var payload struct {
		Code           string   `json:"code"`
		RequiredFields []string `json:"requiredFields"`
	}
	if err := json.Unmarshal(statusMessage.Parts[0].Data, &payload); err != nil {
		t.Fatalf("decode same-status A2A error: %v", err)
	}
	if payload.Code != "invalid_ticket_transition" ||
		len(payload.RequiredFields) != 1 ||
		payload.RequiredFields[0] != "valid status transition" {
		t.Fatalf("same-status A2A error payload = %+v", payload)
	}
	if len(reporter.artifacts) != 0 {
		t.Fatalf("same-status A2A rejection returned success artifacts: %#v", reporter.artifacts)
	}
	assertSameStatusTransitionRejected(
		t,
		fixture.db,
		baseline,
	)
}

type sameStatusTransitionBaseline struct {
	ticket             models.Ticket
	historyCount       int64
	domainEvents       int64
	outboxCount        int64
	notificationOutbox int64
	principalID        string
	idempotencyOp      string
	idempotencyKey     string
	idempotencyCount   int64
}

func captureSameStatusTransitionBaseline(
	t *testing.T,
	db *gorm.DB,
	ticketID uint,
	principalID string,
	idempotencyOp string,
	idempotencyKey string,
) sameStatusTransitionBaseline {
	t.Helper()
	baseline := sameStatusTransitionBaseline{
		principalID:    principalID,
		idempotencyOp:  idempotencyOp,
		idempotencyKey: idempotencyKey,
	}
	if err := db.First(&baseline.ticket, ticketID).Error; err != nil {
		t.Fatalf("load same-status transition Ticket baseline: %v", err)
	}
	if err := db.Model(&models.TicketHistory{}).
		Where("ticket_id = ?", ticketID).
		Count(&baseline.historyCount).Error; err != nil {
		t.Fatalf("count same-status Ticket history baseline: %v", err)
	}
	if err := db.Model(&models.DomainEvent{}).
		Where("subject = ?", fmt.Sprintf("ticket/%d", ticketID)).
		Count(&baseline.domainEvents).Error; err != nil {
		t.Fatalf("count same-status Ticket events baseline: %v", err)
	}
	var eventIDs []string
	if err := db.Model(&models.DomainEvent{}).
		Where("subject = ?", fmt.Sprintf("ticket/%d", ticketID)).
		Pluck("id", &eventIDs).Error; err != nil {
		t.Fatalf("load same-status Ticket event IDs baseline: %v", err)
	}
	if len(eventIDs) > 0 {
		if err := db.Model(&models.OutboxDelivery{}).
			Where("event_id IN ?", eventIDs).
			Count(&baseline.outboxCount).Error; err != nil {
			t.Fatalf("count same-status Ticket Outbox baseline: %v", err)
		}
		if err := db.Model(&models.OutboxDelivery{}).
			Where(
				"event_id IN ? AND destination_type = ?",
				eventIDs,
				services.NotificationOutboxDestination,
			).
			Count(&baseline.notificationOutbox).Error; err != nil {
			t.Fatalf("count same-status notification Outbox baseline: %v", err)
		}
	}
	if err := db.Model(&models.IdempotencyRecord{}).
		Where(
			"actor_type = ? AND actor_id = ? AND operation = ? AND key = ?",
			models.ActorTypeServicePrincipal,
			principalID,
			idempotencyOp,
			idempotencyKey,
		).
		Count(&baseline.idempotencyCount).Error; err != nil {
		t.Fatalf("count same-status idempotency baseline: %v", err)
	}
	if baseline.idempotencyCount != 0 {
		t.Fatalf(
			"same-status idempotency baseline = %d, want 0",
			baseline.idempotencyCount,
		)
	}
	return baseline
}

func assertSameStatusTransitionRejected(
	t *testing.T,
	db *gorm.DB,
	baseline sameStatusTransitionBaseline,
) {
	t.Helper()
	var afterTicket models.Ticket
	if err := db.First(&afterTicket, baseline.ticket.ID).Error; err != nil {
		t.Fatalf("reload same-status rejected Ticket: %v", err)
	}
	if afterTicket.Version != baseline.ticket.Version ||
		afterTicket.Status != baseline.ticket.Status ||
		!afterTicket.UpdatedAt.Equal(baseline.ticket.UpdatedAt) {
		t.Fatalf(
			"same-status rejection changed Ticket: before=%+v after=%+v",
			baseline.ticket,
			afterTicket,
		)
	}
	after := captureSameStatusTransitionSideEffects(t, db, baseline.ticket.ID)
	if after.historyCount != baseline.historyCount ||
		after.domainEvents != baseline.domainEvents ||
		after.outboxCount != baseline.outboxCount ||
		after.notificationOutbox != baseline.notificationOutbox {
		t.Fatalf(
			"same-status rejection side effects history/events/outbox/notifications = %d/%d/%d/%d, want %d/%d/%d/%d",
			after.historyCount,
			after.domainEvents,
			after.outboxCount,
			after.notificationOutbox,
			baseline.historyCount,
			baseline.domainEvents,
			baseline.outboxCount,
			baseline.notificationOutbox,
		)
	}
	var recordCount int64
	if err := db.Model(&models.IdempotencyRecord{}).
		Where(
			"actor_type = ? AND actor_id = ? AND operation = ? AND key = ?",
			models.ActorTypeServicePrincipal,
			baseline.principalID,
			baseline.idempotencyOp,
			baseline.idempotencyKey,
		).
		Count(&recordCount).Error; err != nil {
		t.Fatalf("count same-status failed idempotency record: %v", err)
	}
	if recordCount != baseline.idempotencyCount+1 {
		t.Fatalf(
			"same-status idempotency count = %d, want %d",
			recordCount,
			baseline.idempotencyCount+1,
		)
	}
	var record models.IdempotencyRecord
	if err := db.Where(
		"actor_type = ? AND actor_id = ? AND operation = ? AND key = ?",
		models.ActorTypeServicePrincipal,
		baseline.principalID,
		baseline.idempotencyOp,
		baseline.idempotencyKey,
	).First(&record).Error; err != nil {
		t.Fatalf("load same-status failed idempotency record: %v", err)
	}
	if record.State != models.IdempotencyStateFailed ||
		record.LastErrorCode != "invalid_ticket_transition" ||
		record.ResponseCode != 0 ||
		len(record.ResponseBody) != 0 ||
		len(record.ResourceSnapshot) != 0 ||
		record.ResourceID != "" ||
		record.EventID != "" ||
		record.CompletedAt == nil {
		t.Fatalf(
			"same-status rejection persisted a successful receipt/snapshot: %+v",
			record,
		)
	}
}

func captureSameStatusTransitionSideEffects(
	t *testing.T,
	db *gorm.DB,
	ticketID uint,
) sameStatusTransitionBaseline {
	t.Helper()
	var result sameStatusTransitionBaseline
	if err := db.Model(&models.TicketHistory{}).
		Where("ticket_id = ?", ticketID).
		Count(&result.historyCount).Error; err != nil {
		t.Fatalf("count same-status Ticket history: %v", err)
	}
	var eventIDs []string
	if err := db.Model(&models.DomainEvent{}).
		Where("subject = ?", fmt.Sprintf("ticket/%d", ticketID)).
		Pluck("id", &eventIDs).Error; err != nil {
		t.Fatalf("load same-status Ticket event IDs: %v", err)
	}
	result.domainEvents = int64(len(eventIDs))
	if len(eventIDs) == 0 {
		return result
	}
	if err := db.Model(&models.OutboxDelivery{}).
		Where("event_id IN ?", eventIDs).
		Count(&result.outboxCount).Error; err != nil {
		t.Fatalf("count same-status Ticket Outbox: %v", err)
	}
	if err := db.Model(&models.OutboxDelivery{}).
		Where(
			"event_id IN ? AND destination_type = ?",
			eventIDs,
			services.NotificationOutboxDestination,
		).
		Count(&result.notificationOutbox).Error; err != nil {
		t.Fatalf("count same-status notification Outbox: %v", err)
	}
	return result
}
