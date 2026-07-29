package agentplatform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/agentauth"
	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/httpcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"

	"github.com/gin-gonic/gin"
)

func TestAgentRESTTicketCommandsCommitTypedAuditableChanges(t *testing.T) {
	fixture := newMCPAdapterFixture(t)
	for _, permission := range []struct {
		scope  string
		action string
	}{
		{models.ScopeTicketsAssign, "ticket.assign"},
		{models.ScopeTicketsTransition, "ticket.transition"},
		{models.ScopeTicketsTransition, "ticket.escalate"},
	} {
		allowAgentTicketAction(t, fixture, permission.scope, permission.action)
	}

	ticket := fixture.seedTicket(t, "REST-COMMANDS-HAPPY", "")
	leaseID := claimMCPContractTicket(
		t,
		fixture,
		ticket,
		"claim-rest-command-happy",
	)
	router := newAgentRESTCommandRouter(fixture)

	updateResponse := performAgentRESTJSON(
		t,
		router,
		http.MethodPatch,
		fmt.Sprintf("/api/v1/tickets/%d", ticket.ID),
		fixture.token,
		map[string]string{
			"If-Match":         httpcontract.FormatETag(1),
			"Idempotency-Key":  "rest-update-happy-0001",
			"X-Ticket-Lease":   leaseID,
			"X-Correlation-ID": "corr-rest-update",
		},
		map[string]any{
			"title":         "REST typed command lifecycle",
			"customer_name": "测试客户",
		},
	)
	updateEnvelope := assertAgentRESTTicketCommandSuccess(
		t,
		updateResponse,
		2,
		[]string{"customer_name", "title"},
	)
	if updateEnvelope.Data.Title != "REST typed command lifecycle" ||
		updateEnvelope.Data.CustomerName != "测试客户" {
		t.Fatalf("ordinary update response lost fields: %+v", updateEnvelope.Data)
	}

	assignBody := map[string]any{
		"assignee": map[string]any{
			"type": string(models.ActorTypeHuman),
			"id":   strconv.FormatUint(uint64(fixture.user.ID), 10),
		},
		"reason": "按技能与当前负载分配",
	}
	assignHeaders := map[string]string{
		"If-Match":         httpcontract.FormatETag(2),
		"Idempotency-Key":  "rest-assign-happy-0001",
		"X-Ticket-Lease":   leaseID,
		"X-Correlation-ID": "corr-rest-assign",
	}
	assignResponse := performAgentRESTJSON(
		t,
		router,
		http.MethodPost,
		fmt.Sprintf("/api/v1/tickets/%d/commands/assign", ticket.ID),
		fixture.token,
		assignHeaders,
		assignBody,
	)
	assignEnvelope := assertAgentRESTTicketCommandSuccess(
		t,
		assignResponse,
		3,
		[]string{
			"assigned_to_actor_id",
			"assigned_to_actor_type",
			"assigned_to_id",
			"assigned_to_service_principal_id",
		},
	)
	if assignEnvelope.Data.AssignedToActor == nil ||
		assignEnvelope.Data.AssignedToActor.Type != models.ActorTypeHuman ||
		assignEnvelope.Data.AssignedToActor.ID != strconv.FormatUint(uint64(fixture.user.ID), 10) {
		t.Fatalf("assignment response did not expose canonical ActorRef: %+v", assignEnvelope.Data)
	}

	replayResponse := performAgentRESTJSON(
		t,
		router,
		http.MethodPost,
		fmt.Sprintf("/api/v1/tickets/%d/commands/assign", ticket.ID),
		fixture.token,
		assignHeaders,
		assignBody,
	)
	replayEnvelope := assertAgentRESTTicketCommandSuccess(
		t,
		replayResponse,
		3,
		[]string{
			"assigned_to_actor_id",
			"assigned_to_actor_type",
			"assigned_to_id",
			"assigned_to_service_principal_id",
		},
	)
	if replayEnvelope.Receipt.OperationID != assignEnvelope.Receipt.OperationID ||
		replayEnvelope.Receipt.EventID != assignEnvelope.Receipt.EventID {
		t.Fatalf(
			"idempotent replay returned a new operation: first=%+v replay=%+v",
			assignEnvelope.Receipt,
			replayEnvelope.Receipt,
		)
	}

	transitionResponse := performAgentRESTJSON(
		t,
		router,
		http.MethodPost,
		fmt.Sprintf("/api/v1/tickets/%d/commands/transition", ticket.ID),
		fixture.token,
		map[string]string{
			"If-Match":         httpcontract.FormatETag(3),
			"Idempotency-Key":  "rest-transition-happy-0001",
			"X-Ticket-Lease":   leaseID,
			"X-Correlation-ID": "corr-rest-transition",
		},
		map[string]any{
			"status": string(models.TicketStatusInProgress),
			"reason": "已确认信息完整并开始处理",
		},
	)
	transitionEnvelope := assertAgentRESTTicketCommandSuccess(
		t,
		transitionResponse,
		4,
		[]string{"status"},
	)
	if transitionEnvelope.Data.Status != models.TicketStatusInProgress {
		t.Fatalf("transition status = %q, want in_progress", transitionEnvelope.Data.Status)
	}

	escalateResponse := performAgentRESTJSON(
		t,
		router,
		http.MethodPost,
		fmt.Sprintf("/api/v1/tickets/%d/commands/escalate", ticket.ID),
		fixture.token,
		map[string]string{
			"If-Match":         httpcontract.FormatETag(4),
			"Idempotency-Key":  "rest-escalate-happy-0001",
			"X-Ticket-Lease":   leaseID,
			"X-Correlation-ID": "corr-rest-escalate",
		},
		map[string]any{
			"reason":   "已达到明确升级阈值",
			"priority": string(models.TicketPriorityUrgent),
			"assignee": map[string]any{
				"type": string(models.ActorTypeServicePrincipal),
				"id":   fixture.principal.ID,
			},
		},
	)
	escalateEnvelope := assertAgentRESTTicketCommandSuccess(
		t,
		escalateResponse,
		5,
		[]string{
			"assigned_to_actor_id",
			"assigned_to_actor_type",
			"assigned_to_id",
			"assigned_to_service_principal_id",
			"is_escalated",
			"priority",
		},
	)
	if !escalateEnvelope.Data.IsEscalated ||
		escalateEnvelope.Data.Priority != models.TicketPriorityUrgent ||
		escalateEnvelope.Data.AssignedToActor == nil ||
		escalateEnvelope.Data.AssignedToActor.Type != models.ActorTypeServicePrincipal ||
		escalateEnvelope.Data.AssignedToActor.ID != fixture.principal.ID {
		t.Fatalf("escalation response lost atomic changes: %+v", escalateEnvelope.Data)
	}

	releaseResponse := performAgentRESTJSON(
		t,
		router,
		http.MethodPost,
		fmt.Sprintf("/api/v1/tickets/%d/commands/assign", ticket.ID),
		fixture.token,
		map[string]string{
			"If-Match":         httpcontract.FormatETag(5),
			"Idempotency-Key":  "rest-release-happy-0001",
			"X-Ticket-Lease":   leaseID,
			"X-Correlation-ID": "corr-rest-release",
		},
		map[string]any{
			"assignee": nil,
			"reason":   "当前处理者轮值结束，释放回队列",
		},
	)
	releaseEnvelope := assertAgentRESTTicketCommandSuccess(
		t,
		releaseResponse,
		6,
		[]string{
			"assigned_to_actor_id",
			"assigned_to_actor_type",
			"assigned_to_id",
			"assigned_to_service_principal_id",
		},
	)
	if releaseEnvelope.Data.AssignedToActor != nil ||
		releaseEnvelope.Data.AssignedToID != nil {
		t.Fatalf("release retained assignment projection: %+v", releaseEnvelope.Data)
	}

	var persisted models.Ticket
	if err := fixture.db.First(&persisted, ticket.ID).Error; err != nil {
		t.Fatalf("reload REST command ticket: %v", err)
	}
	if persisted.Version != 6 ||
		persisted.Title != "REST typed command lifecycle" ||
		persisted.Status != models.TicketStatusInProgress ||
		persisted.Priority != models.TicketPriorityUrgent ||
		!persisted.IsEscalated ||
		persisted.AssignedToID != nil ||
		persisted.AssignedToActorType != "" ||
		persisted.AssignedToActorID != "" ||
		persisted.AssignedToServicePrincipalID != nil {
		t.Fatalf("durable ticket does not match command sequence: %+v", persisted)
	}

	assertAgentRESTCommandEvent(
		t,
		fixture,
		eventcontract.TicketAssignedEventType,
		3,
		"corr-rest-assign",
		"按技能与当前负载分配",
		1,
	)
	assertAgentRESTCommandEvent(
		t,
		fixture,
		eventcontract.TicketTransitionedEventType,
		4,
		"corr-rest-transition",
		"已确认信息完整并开始处理",
		1,
	)
	assertAgentRESTCommandEvent(
		t,
		fixture,
		eventcontract.TicketEscalatedEventType,
		5,
		"corr-rest-escalate",
		"已达到明确升级阈值",
		1,
	)
	assertAgentRESTCommandEvent(
		t,
		fixture,
		eventcontract.TicketAssignedEventType,
		6,
		"corr-rest-release",
		"当前处理者轮值结束，释放回队列",
		1,
	)

	var histories []models.TicketHistory
	if err := fixture.db.
		Where(
			"ticket_id = ? AND actor_type = ? AND actor_id = ?",
			ticket.ID,
			models.ActorTypeServicePrincipal,
			fixture.principal.ID,
		).
		Order("id ASC").
		Find(&histories).Error; err != nil {
		t.Fatalf("load service-principal ticket histories: %v", err)
	}
	if len(histories) != 5 {
		t.Fatalf("service-principal histories = %d, want 5: %+v", len(histories), histories)
	}
	for _, history := range histories {
		if history.EventID == nil ||
			*history.EventID == "" ||
			history.ResourceVersion == 0 ||
			history.ServicePrincipalID == nil ||
			*history.ServicePrincipalID != fixture.principal.ID {
			t.Fatalf("history is missing immutable Agent audit linkage: %+v", history)
		}
	}
}

func TestAgentRESTTicketCommandsRejectPrivilegeAndContractBypass(t *testing.T) {
	fixture := newMCPAdapterFixture(t)
	for _, permission := range []struct {
		scope  string
		action string
	}{
		{models.ScopeTicketsAssign, "ticket.assign"},
		{models.ScopeTicketsTransition, "ticket.transition"},
		{models.ScopeTicketsTransition, "ticket.escalate"},
	} {
		allowAgentTicketAction(t, fixture, permission.scope, permission.action)
	}
	ticket := fixture.seedTicket(t, "REST-COMMANDS-REJECT", "")
	leaseID := claimMCPContractTicket(
		t,
		fixture,
		ticket,
		"claim-rest-command-reject",
	)
	router := newAgentRESTCommandRouter(fixture)
	transitionOnlyToken := issueAgentRESTTestToken(
		t,
		fixture,
		[]string{models.ScopeTicketsTransition},
	)
	updateOnlyToken := issueAgentRESTTestToken(
		t,
		fixture,
		[]string{models.ScopeTicketsUpdate},
	)

	baseHeaders := func(key string) map[string]string {
		return map[string]string{
			"If-Match":         httpcontract.FormatETag(1),
			"Idempotency-Key":  key,
			"X-Ticket-Lease":   leaseID,
			"X-Correlation-ID": "corr-" + key,
		}
	}
	commandPath := func(command string) string {
		return fmt.Sprintf(
			"/api/v1/tickets/%d/commands/%s",
			ticket.ID,
			command,
		)
	}

	tests := []struct {
		name       string
		method     string
		path       string
		token      string
		headers    map[string]string
		body       any
		wantStatus int
		wantCode   string
	}{
		{
			name:       "missing bearer token",
			method:     http.MethodPost,
			path:       commandPath("assign"),
			headers:    baseHeaders("reject-missing-auth"),
			body:       map[string]any{"assignee": nil, "reason": "释放"},
			wantStatus: http.StatusUnauthorized,
			wantCode:   ProblemUnauthorized,
		},
		{
			name:       "scope middleware blocks assignment",
			method:     http.MethodPost,
			path:       commandPath("assign"),
			token:      updateOnlyToken,
			headers:    baseHeaders("reject-assign-scope"),
			body:       map[string]any{"assignee": nil, "reason": "释放"},
			wantStatus: http.StatusForbidden,
			wantCode:   ProblemInsufficientScope,
		},
		{
			name:   "escalation assignment requires second scope",
			method: http.MethodPost,
			path:   commandPath("escalate"),
			token:  transitionOnlyToken,
			headers: baseHeaders(
				"reject-escalate-assign-scope",
			),
			body: map[string]any{
				"reason": "升级并分配",
				"assignee": map[string]any{
					"type": string(models.ActorTypeHuman),
					"id":   strconv.FormatUint(uint64(fixture.user.ID), 10),
				},
			},
			wantStatus: http.StatusForbidden,
			wantCode:   ProblemInsufficientScope,
		},
		{
			name:    "assign rejects raw projection",
			method:  http.MethodPost,
			path:    commandPath("assign"),
			token:   fixture.token,
			headers: baseHeaders("reject-raw-assignment"),
			body: map[string]any{
				"assignee":       nil,
				"reason":         "释放",
				"assigned_to_id": fixture.user.ID,
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   ProblemInvalidRequest,
		},
		{
			name:       "release requires reason",
			method:     http.MethodPost,
			path:       commandPath("assign"),
			token:      fixture.token,
			headers:    baseHeaders("reject-release-no-reason"),
			body:       map[string]any{"assignee": nil},
			wantStatus: http.StatusBadRequest,
			wantCode:   ProblemInvalidRequest,
		},
		{
			name:   "system cannot be an assignee",
			method: http.MethodPost,
			path:   commandPath("assign"),
			token:  fixture.token,
			headers: baseHeaders(
				"reject-system-assignee",
			),
			body: map[string]any{
				"assignee": map[string]any{
					"type": string(models.ActorTypeSystem),
					"id":   "scheduler",
				},
				"reason": "系统主体不得接单",
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   ProblemInvalidRequest,
		},
		{
			name:    "transition rejects ordinary field",
			method:  http.MethodPost,
			path:    commandPath("transition"),
			token:   fixture.token,
			headers: baseHeaders("reject-transition-priority"),
			body: map[string]any{
				"status":   string(models.TicketStatusInProgress),
				"reason":   "开始处理",
				"priority": string(models.TicketPriorityHigh),
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   ProblemInvalidRequest,
		},
		{
			name:    "escalation rejects null assignee",
			method:  http.MethodPost,
			path:    commandPath("escalate"),
			token:   fixture.token,
			headers: baseHeaders("reject-escalate-null"),
			body: map[string]any{
				"reason":   "升级",
				"assignee": nil,
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   ProblemInvalidRequest,
		},
		{
			name:   "command requires correlation id",
			method: http.MethodPost,
			path:   commandPath("transition"),
			token:  fixture.token,
			headers: map[string]string{
				"If-Match":        httpcontract.FormatETag(1),
				"Idempotency-Key": "reject-no-correlation",
				"X-Ticket-Lease":  leaseID,
			},
			body: map[string]any{
				"status": string(models.TicketStatusInProgress),
				"reason": "开始处理",
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   ProblemInvalidRequest,
		},
		{
			name:   "command requires idempotency key",
			method: http.MethodPost,
			path:   commandPath("transition"),
			token:  fixture.token,
			headers: map[string]string{
				"If-Match":         httpcontract.FormatETag(1),
				"X-Ticket-Lease":   leaseID,
				"X-Correlation-ID": "corr-reject-no-idempotency",
			},
			body: map[string]any{
				"status": string(models.TicketStatusInProgress),
				"reason": "开始处理",
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   ProblemInvalidRequest,
		},
		{
			name:   "command requires lease",
			method: http.MethodPost,
			path:   commandPath("transition"),
			token:  fixture.token,
			headers: map[string]string{
				"If-Match":         httpcontract.FormatETag(1),
				"Idempotency-Key":  "reject-no-lease",
				"X-Correlation-ID": "corr-reject-no-lease",
			},
			body: map[string]any{
				"status": string(models.TicketStatusInProgress),
				"reason": "开始处理",
			},
			wantStatus: http.StatusConflict,
			wantCode:   ProblemLeaseConflict,
		},
		{
			name:   "stale command version",
			method: http.MethodPost,
			path:   commandPath("transition"),
			token:  fixture.token,
			headers: map[string]string{
				"If-Match":         httpcontract.FormatETag(2),
				"Idempotency-Key":  "reject-stale-version",
				"X-Ticket-Lease":   leaseID,
				"X-Correlation-ID": "corr-reject-stale-version",
			},
			body: map[string]any{
				"status": string(models.TicketStatusInProgress),
				"reason": "开始处理",
			},
			wantStatus: http.StatusConflict,
			wantCode:   ProblemVersionConflict,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := performAgentRESTJSON(
				t,
				router,
				test.method,
				test.path,
				test.token,
				test.headers,
				test.body,
			)
			assertAgentRESTProblem(
				t,
				response,
				test.wantStatus,
				test.wantCode,
			)
			assertRejectedAgentRESTTicketUnchanged(t, fixture, ticket)
		})
	}

	forbiddenPatchFields := map[string]any{
		"status":                           string(models.TicketStatusInProgress),
		"is_escalated":                     true,
		"source":                           string(models.TicketSourceAgent),
		"trust_level":                      string(models.TicketTrustLevelTrusted),
		"sla_breached":                     true,
		"sla_due_date":                     "2026-07-30T12:00:00Z",
		"assigned_to_id":                   fixture.user.ID,
		"assigned_to_actor_type":           string(models.ActorTypeHuman),
		"assigned_to_actor_id":             strconv.FormatUint(uint64(fixture.user.ID), 10),
		"assigned_to_service_principal_id": fixture.principal.ID,
	}
	for field, value := range forbiddenPatchFields {
		t.Run("ordinary patch rejects "+field, func(t *testing.T) {
			response := performAgentRESTJSON(
				t,
				router,
				http.MethodPatch,
				fmt.Sprintf("/api/v1/tickets/%d", ticket.ID),
				fixture.token,
				baseHeaders("reject-patch-"+field),
				map[string]any{field: value},
			)
			assertAgentRESTProblem(
				t,
				response,
				http.StatusBadRequest,
				ProblemInvalidRequest,
			)
			assertRejectedAgentRESTTicketUnchanged(t, fixture, ticket)
		})
	}
}

type agentRESTTicketCommandEnvelope struct {
	Data    models.TicketResponse `json:"data"`
	Receipt Receipt               `json:"receipt"`
}

func newAgentRESTCommandRouter(fixture *mcpAdapterFixture) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewAPIHandler(
		fixture.db,
		fixture.service,
		fixture.manager,
		10<<20,
		nil,
	)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/v1"))
	return router
}

func issueAgentRESTTestToken(
	t *testing.T,
	fixture *mcpAdapterFixture,
	scopes []string,
) string {
	t.Helper()
	token, _, err := fixture.manager.Issue(
		&agentauth.Principal{
			ID:           fixture.principal.ID,
			CredentialID: fixture.credential.ID,
			ClientID:     "rest-command-test-client",
			Name:         fixture.principal.Name,
			Scopes:       append([]string(nil), models.SupportedAgentScopes...),
			Active:       true,
		},
		scopes,
	)
	if err != nil {
		t.Fatalf("issue REST command test token: %v", err)
	}
	return token
}

func performAgentRESTJSON(
	t *testing.T,
	router http.Handler,
	method string,
	path string,
	token string,
	headers map[string]string,
	body any,
) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("encode REST command body: %v", err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func assertAgentRESTTicketCommandSuccess(
	t *testing.T,
	response *httptest.ResponseRecorder,
	version uint64,
	changedFields []string,
) agentRESTTicketCommandEnvelope {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf(
			"REST command status=%d body=%s, want 200",
			response.Code,
			response.Body.String(),
		)
	}
	if got := response.Header().Get("ETag"); got != httpcontract.FormatETag(version) {
		t.Fatalf("REST command ETag=%q, want %q", got, httpcontract.FormatETag(version))
	}
	var envelope agentRESTTicketCommandEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode REST command response: %v", err)
	}
	if envelope.Data.Version != version ||
		envelope.Receipt.ResourceVersion != version ||
		envelope.Receipt.OperationID == "" ||
		envelope.Receipt.ResourceID == "" ||
		envelope.Receipt.EventID == "" ||
		envelope.Receipt.PolicyDecisionID == "" {
		t.Fatalf("REST command returned incomplete receipt: %+v", envelope)
	}
	if fmt.Sprint(envelope.Receipt.ChangedFields) != fmt.Sprint(changedFields) {
		t.Fatalf(
			"changed_fields=%v, want %v",
			envelope.Receipt.ChangedFields,
			changedFields,
		)
	}
	return envelope
}

func assertAgentRESTProblem(
	t *testing.T,
	response *httptest.ResponseRecorder,
	status int,
	code string,
) {
	t.Helper()
	if response.Code != status {
		t.Fatalf(
			"problem status=%d body=%s, want %d",
			response.Code,
			response.Body.String(),
			status,
		)
	}
	var problem Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode REST problem: %v", err)
	}
	if problem.Code != code || problem.Status != status {
		t.Fatalf("problem=%+v, want status=%d code=%q", problem, status, code)
	}
}

func assertRejectedAgentRESTTicketUnchanged(
	t *testing.T,
	fixture *mcpAdapterFixture,
	before models.Ticket,
) {
	t.Helper()
	var after models.Ticket
	if err := fixture.db.First(&after, before.ID).Error; err != nil {
		t.Fatalf("reload rejected REST command ticket: %v", err)
	}
	if after.Version != before.Version ||
		after.Status != before.Status ||
		after.Priority != before.Priority ||
		after.IsEscalated != before.IsEscalated ||
		after.AssignedToID != nil ||
		after.AssignedToActorType != "" ||
		after.AssignedToActorID != "" ||
		after.AssignedToServicePrincipalID != nil {
		t.Fatalf(
			"rejected REST command mutated durable ticket: before=%+v after=%+v",
			before,
			after,
		)
	}
}

func assertAgentRESTCommandEvent(
	t *testing.T,
	fixture *mcpAdapterFixture,
	eventType string,
	resourceVersion uint64,
	correlationID string,
	reason string,
	wantCount int64,
) {
	t.Helper()
	var count int64
	if err := fixture.db.Model(&models.DomainEvent{}).
		Where(
			"type = ? AND subject = ? AND resource_version = ?",
			eventType,
			fmt.Sprintf("ticket/%d", fixtureTicketIDFromEventTest(t, fixture, eventType, resourceVersion)),
			resourceVersion,
		).
		Count(&count).Error; err != nil {
		t.Fatalf("count REST command event: %v", err)
	}
	if count != wantCount {
		t.Fatalf(
			"event %s/v%d count=%d, want %d",
			eventType,
			resourceVersion,
			count,
			wantCount,
		)
	}

	var event models.DomainEvent
	if err := fixture.db.
		Where("type = ? AND resource_version = ?", eventType, resourceVersion).
		First(&event).Error; err != nil {
		t.Fatalf("load REST command event: %v", err)
	}
	if event.CorrelationID != correlationID ||
		event.ActorType != models.ActorTypeServicePrincipal ||
		event.ActorID != fixture.principal.ID {
		t.Fatalf("REST command event lost provenance: %+v", event)
	}
	var data map[string]any
	if err := json.Unmarshal(event.Data, &data); err != nil {
		t.Fatalf("decode REST command event data: %v", err)
	}
	if data["reason"] != reason {
		t.Fatalf("event reason=%#v, want %q", data["reason"], reason)
	}
}

func fixtureTicketIDFromEventTest(
	t *testing.T,
	fixture *mcpAdapterFixture,
	eventType string,
	resourceVersion uint64,
) uint {
	t.Helper()
	var event models.DomainEvent
	if err := fixture.db.
		Where("type = ? AND resource_version = ?", eventType, resourceVersion).
		First(&event).Error; err != nil {
		t.Fatalf("locate REST command event: %v", err)
	}
	var ticketID uint64
	if _, err := fmt.Sscanf(event.Subject, "ticket/%d", &ticketID); err != nil ||
		ticketID == 0 {
		t.Fatalf("invalid ticket event subject %q", event.Subject)
	}
	return uint(ticketID)
}

func TestAgentRESTCommandReasonsHaveClosedLengthBounds(t *testing.T) {
	tooLong := make([]rune, 1001)
	for index := range tooLong {
		tooLong[index] = '理'
	}
	tests := []struct {
		name string
		body []byte
		call func([]byte) error
	}{
		{
			name: "assign release",
			body: []byte(`{"assignee":null,"reason":"` + string(tooLong) + `"}`),
			call: func(body []byte) error {
				_, err := decodeTicketAssignmentCommand(body)
				return err
			},
		},
		{
			name: "transition",
			body: []byte(`{"status":"in_progress","reason":"` + string(tooLong) + `"}`),
			call: func(body []byte) error {
				_, err := decodeTicketTransitionCommand(body)
				return err
			},
		},
		{
			name: "escalation",
			body: []byte(`{"reason":"` + string(tooLong) + `"}`),
			call: func(body []byte) error {
				_, err := decodeTicketEscalationCommand(body)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(test.body); err == nil {
				t.Fatal("1001-character reason was accepted")
			}
		})
	}
}

func TestAgentRESTCommandCorrelationIDTrimsButNeverSynthesizes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name      string
		value     string
		want      string
		wantValid bool
	}{
		{name: "missing", wantValid: false},
		{name: "whitespace", value: " \t ", wantValid: false},
		{name: "trimmed", value: " corr-42 ", want: "corr-42", wantValid: true},
		{name: "too long", value: string(bytes.Repeat([]byte("x"), 256)), wantValid: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			if test.value != "" {
				context.Request.Header.Set("X-Correlation-ID", test.value)
			}
			got, valid := requireCommandCorrelationID(context)
			if valid != test.wantValid || got != test.want {
				t.Fatalf(
					"correlation id=(%q,%v), want (%q,%v)",
					got,
					valid,
					test.want,
					test.wantValid,
				)
			}
		})
	}
}
