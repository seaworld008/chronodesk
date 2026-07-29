package agentplatform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gongdan-system/internal/agentauth"
	"gongdan-system/internal/models"
	"gongdan-system/internal/services"

	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestListTicketsFiltersEachResourcePolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.ServicePrincipal{},
		&models.AgentCredential{},
		&models.AgentPolicy{},
		&models.PolicyDecision{},
		&models.Ticket{},
	); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username: "visibility-user", Email: "visibility@example.com", PasswordHash: "hash",
		Role: models.RoleAgent, Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	native := services.NewAgentNativeService(db)
	principal, err := native.CreateServicePrincipal(context.Background(), services.CreateServicePrincipalInput{
		Name:                "visibility-agent",
		Scopes:              []string{models.ScopeTicketsRead},
		CompatibilityUserID: &user.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := native.IssueCredential(context.Background(), principal.ID, "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tickets := []models.Ticket{
		{
			TicketNumber: "VISIBLE-1", Title: "visible", Description: "visible",
			Type: models.TicketTypeRequest, Priority: models.TicketPriorityNormal,
			Status: models.TicketStatusOpen, Source: models.TicketSourceAgent,
			CreatedByID: user.ID, Version: 1,
		},
		{
			TicketNumber: "HIDDEN-2", Title: "hidden", Description: "hidden",
			Type: models.TicketTypeRequest, Priority: models.TicketPriorityNormal,
			Status: models.TicketStatusOpen, Source: models.TicketSourceAgent,
			CreatedByID: user.ID, Version: 1,
		},
	}
	if err := db.Create(&tickets).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := native.CreateAgentPolicy(context.Background(), services.CreateAgentPolicyInput{
		ServicePrincipalID: principal.ID,
		Name:               "hide ticket",
		Effect:             models.AgentPolicyEffectDeny,
		Scope:              models.ScopeTicketsRead,
		Action:             "ticket.read",
		ResourceType:       "ticket",
		ResourceID:         fmt.Sprint(tickets[1].ID),
	}); err != nil {
		t.Fatal(err)
	}

	handler := &APIHandler{db: db, native: native}
	router := gin.New()
	router.GET("/tickets", func(c *gin.Context) {
		c.Set("agent_principal_id", principal.ID)
		c.Set("agent_credential_id", credential.Credential.ID)
		handler.ListTickets(c)
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/tickets?limit=10", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data []models.TicketResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data) != 1 || envelope.Data[0].TicketNumber != "VISIBLE-1" {
		t.Fatalf("resource policy was not applied to list items: %s", recorder.Body.String())
	}
}

func TestListTicketsUsesBoundedPolicyBatchAndAdvancingCandidateCursor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.ServicePrincipal{},
		&models.AgentCredential{},
		&models.AgentPolicy{},
		&models.PolicyDecision{},
		&models.Ticket{},
	); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username: "bounded-user", Email: "bounded@example.com", PasswordHash: "hash",
		Role: models.RoleAgent, Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	native := services.NewAgentNativeService(db)
	principal, err := native.CreateServicePrincipal(context.Background(), services.CreateServicePrincipalInput{
		Name:                "bounded-ticket-agent",
		Scopes:              []string{models.ScopeTicketsRead},
		CompatibilityUserID: &user.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := native.IssueCredential(context.Background(), principal.ID, "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := native.CreateAgentPolicy(context.Background(), services.CreateAgentPolicyInput{
		ServicePrincipalID: principal.ID,
		Name:               "hide every ticket",
		Effect:             models.AgentPolicyEffectDeny,
		Scope:              models.ScopeTicketsRead,
		Action:             "ticket.read",
		ResourceType:       "ticket",
		ResourceID:         "*",
	}); err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, time.July, 28, 10, 0, 0, 0, time.UTC)
	tickets := make([]models.Ticket, 12)
	for i := range tickets {
		tickets[i] = models.Ticket{
			CreatedAt:    base.Add(time.Duration(i) * time.Minute),
			TicketNumber: fmt.Sprintf("BOUNDED-%02d", i),
			Title:        fmt.Sprintf("ticket %d", i),
			Description:  "policy filtered",
			Type:         models.TicketTypeRequest,
			Priority:     models.TicketPriorityNormal,
			Status:       models.TicketStatusOpen,
			Source:       models.TicketSourceAgent,
			CreatedByID:  user.ID,
			Version:      1,
		}
	}
	if err := db.Create(&tickets).Error; err != nil {
		t.Fatal(err)
	}

	var policyQueries atomic.Int64
	callbackName := "count_bounded_ticket_policy_queries"
	if err := db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == (models.AgentPolicy{}).TableName() {
			policyQueries.Add(1)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Query().Remove(callbackName)
	})

	handler := &APIHandler{db: db, native: native}
	router := gin.New()
	router.GET("/tickets", func(c *gin.Context) {
		c.Set("agent_principal_id", principal.ID)
		c.Set("agent_credential_id", credential.Credential.ID)
		handler.ListTickets(c)
	})
	requestPage := func(cursor string) struct {
		Data []models.TicketResponse `json:"data"`
		Meta Meta                    `json:"meta"`
	} {
		t.Helper()
		target := "/tickets?limit=1"
		if cursor != "" {
			target += "&cursor=" + cursor
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		var envelope struct {
			Data []models.TicketResponse `json:"data"`
			Meta Meta                    `json:"meta"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope
	}

	first := requestPage("")
	if len(first.Data) != 0 || !first.Meta.HasMore || first.Meta.NextCursor == "" {
		t.Fatalf("unexpected first deny-heavy page: %#v", first)
	}
	firstCursor, err := DecodeCursor(first.Meta.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if firstCursor.ID != fmt.Sprint(tickets[7].ID) {
		t.Fatalf(
			"cursor stopped at candidate %s, want fifth bounded candidate %d",
			firstCursor.ID,
			tickets[7].ID,
		)
	}
	if policyQueries.Load() != 1 {
		t.Fatalf("policy queries=%d, want one batch load", policyQueries.Load())
	}
	var decisions int64
	if err := db.Model(&models.PolicyDecision{}).Count(&decisions).Error; err != nil {
		t.Fatal(err)
	}
	if decisions != 1 {
		t.Fatalf("policy decisions=%d, want one list summary", decisions)
	}

	second := requestPage(first.Meta.NextCursor)
	if len(second.Data) != 0 || !second.Meta.HasMore || second.Meta.NextCursor == "" {
		t.Fatalf("unexpected second deny-heavy page: %#v", second)
	}
	secondCursor, err := DecodeCursor(second.Meta.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if secondCursor.ID != fmt.Sprint(tickets[2].ID) {
		t.Fatalf(
			"second cursor stopped at candidate %s, want %d",
			secondCursor.ID,
			tickets[2].ID,
		)
	}
	if policyQueries.Load() != 2 {
		t.Fatalf("policy queries after two requests=%d, want two", policyQueries.Load())
	}
	if err := db.Model(&models.PolicyDecision{}).Count(&decisions).Error; err != nil {
		t.Fatal(err)
	}
	if decisions != 2 {
		t.Fatalf("policy decisions after two requests=%d, want two summaries", decisions)
	}
}

func TestListEventsUsesBoundedPolicyBatchWithoutDecisionAmplification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.ServicePrincipal{},
		&models.AgentCredential{},
		&models.AgentPolicy{},
		&models.PolicyDecision{},
		&models.DomainEvent{},
	); err != nil {
		t.Fatal(err)
	}
	native := services.NewAgentNativeService(db)
	principal, err := native.CreateServicePrincipal(context.Background(), services.CreateServicePrincipalInput{
		Name:   "bounded-event-agent",
		Scopes: []string{models.ScopeEventsSubscribe},
	})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := native.IssueCredential(context.Background(), principal.ID, "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := native.CreateAgentPolicy(context.Background(), services.CreateAgentPolicyInput{
		ServicePrincipalID: principal.ID,
		Name:               "hide every event",
		Effect:             models.AgentPolicyEffectDeny,
		Scope:              models.ScopeEventsSubscribe,
		Action:             "event.read",
		ResourceType:       "event",
		ResourceID:         "*",
	}); err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, time.July, 28, 11, 0, 0, 0, time.UTC)
	events := make([]models.DomainEvent, 12)
	for i := range events {
		eventTime := base.Add(time.Duration(i) * time.Minute)
		events[i] = models.DomainEvent{
			ID:              fmt.Sprintf("event-%02d", i),
			CreatedAt:       eventTime,
			SpecVersion:     "1.0",
			Source:          "urn:chronodesk:test",
			Type:            "com.chronodesk.test",
			Subject:         "queue/test",
			Time:            eventTime,
			DataContentType: "application/json",
			Data:            datatypes.JSON([]byte(`{"safe":true}`)),
			ActorType:       models.ActorTypeSystem,
			ActorID:         "test",
			ResourceVersion: 1,
		}
	}
	if err := db.Create(&events).Error; err != nil {
		t.Fatal(err)
	}

	var policyQueries atomic.Int64
	callbackName := "count_bounded_event_policy_queries"
	if err := db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == (models.AgentPolicy{}).TableName() {
			policyQueries.Add(1)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Query().Remove(callbackName)
	})

	handler := &APIHandler{db: db, native: native}
	router := gin.New()
	router.GET("/events", func(c *gin.Context) {
		c.Set("agent_principal_id", principal.ID)
		c.Set("agent_credential_id", credential.Credential.ID)
		handler.ListEvents(c)
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/events?limit=1", nil),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data []services.CloudEventEnvelope `json:"data"`
		Meta Meta                          `json:"meta"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data) != 0 || !envelope.Meta.HasMore || envelope.Meta.NextCursor == "" {
		t.Fatalf("unexpected deny-heavy event page: %#v", envelope)
	}
	cursor, err := DecodeCursor(envelope.Meta.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if cursor.ID != events[7].ID {
		t.Fatalf("event cursor stopped at %s, want fifth candidate %s", cursor.ID, events[7].ID)
	}
	if policyQueries.Load() != 1 {
		t.Fatalf("event policy queries=%d, want one batch load", policyQueries.Load())
	}
	var decisions int64
	if err := db.Model(&models.PolicyDecision{}).Count(&decisions).Error; err != nil {
		t.Fatal(err)
	}
	if decisions != 1 {
		t.Fatalf("event policy decisions=%d, want one list summary", decisions)
	}
}

func TestListEventsRequiresEventAndLinkedTicketAuthorization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.ServicePrincipal{},
		&models.AgentCredential{},
		&models.AgentPolicy{},
		&models.PolicyDecision{},
		&models.DomainEvent{},
	); err != nil {
		t.Fatal(err)
	}
	native := services.NewAgentNativeService(db)
	eventsOnly, err := native.CreateServicePrincipal(context.Background(), services.CreateServicePrincipalInput{
		Name:   "events-only-agent",
		Scopes: []string{models.ScopeEventsSubscribe},
	})
	if err != nil {
		t.Fatal(err)
	}
	eventsOnlyCredential, err := native.IssueCredential(
		context.Background(),
		eventsOnly.ID,
		"events-only",
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	fullAccess, err := native.CreateServicePrincipal(context.Background(), services.CreateServicePrincipalInput{
		Name:   "event-and-ticket-agent",
		Scopes: []string{models.ScopeEventsSubscribe, models.ScopeTicketsRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	fullCredential, err := native.IssueCredential(
		context.Background(),
		fullAccess.ID,
		"event-and-ticket",
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	ticketEvent := models.DomainEvent{
		ID: "ticket-event-denied", CreatedAt: base.Add(time.Minute),
		SpecVersion: "1.0", Source: "urn:chronodesk:test",
		Type: "io.chronodesk.ticket.updated.v1", Subject: "ticket/42", Time: base.Add(time.Minute),
		DataContentType: "application/json", Data: datatypes.JSON([]byte(`{"ticket_id":42,"secret":"hidden"}`)),
		ActorType: models.ActorTypeSystem, ActorID: "test", ResourceVersion: 2,
	}
	queueEvent := models.DomainEvent{
		ID: "queue-event-visible", CreatedAt: base,
		SpecVersion: "1.0", Source: "urn:chronodesk:test",
		Type: "io.chronodesk.queue.updated.v1", Subject: "queue/support", Time: base,
		DataContentType: "application/json", Data: datatypes.JSON([]byte(`{"queue":"support"}`)),
		ActorType: models.ActorTypeSystem, ActorID: "test", ResourceVersion: 1,
	}
	if err := db.Create(&[]models.DomainEvent{ticketEvent, queueEvent}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := native.CreateAgentPolicy(context.Background(), services.CreateAgentPolicyInput{
		ServicePrincipalID: fullAccess.ID,
		Name:               "deny one event object",
		Effect:             models.AgentPolicyEffectDeny,
		Scope:              models.ScopeEventsSubscribe,
		Action:             "event.read",
		ResourceType:       "event",
		ResourceID:         ticketEvent.ID,
	}); err != nil {
		t.Fatal(err)
	}

	handler := &APIHandler{db: db, native: native}
	requestEvents := func(
		principalID string,
		credentialID string,
		scopes []string,
	) []services.CloudEventEnvelope {
		t.Helper()
		router := gin.New()
		router.GET("/events", func(c *gin.Context) {
			c.Set(agentauth.ContextPrincipalID, principalID)
			c.Set(agentauth.ContextCredentialID, credentialID)
			c.Set(agentauth.ContextScopes, scopes)
			handler.ListEvents(c)
		})
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/events?limit=10", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		var envelope struct {
			Data []services.CloudEventEnvelope `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data
	}

	eventsOnlyResult := requestEvents(
		eventsOnly.ID,
		eventsOnlyCredential.Credential.ID,
		[]string{models.ScopeEventsSubscribe},
	)
	if len(eventsOnlyResult) != 1 || eventsOnlyResult[0].ID != queueEvent.ID {
		t.Fatalf("events-only token received linked ticket data: %#v", eventsOnlyResult)
	}
	fullResult := requestEvents(
		fullAccess.ID,
		fullCredential.Credential.ID,
		[]string{models.ScopeEventsSubscribe, models.ScopeTicketsRead},
	)
	if len(fullResult) != 1 || fullResult[0].ID != queueEvent.ID {
		t.Fatalf("event-level deny did not hide linked ticket event: %#v", fullResult)
	}
}

func TestClassifyTicketPatchUsesLeastPrivilegeScope(t *testing.T) {
	tests := []struct {
		name       string
		changes    map[string]any
		wantScope  string
		wantAction string
		wantRisky  bool
		wantErr    bool
	}{
		{
			name:       "ordinary update",
			changes:    map[string]any{"title": "updated"},
			wantScope:  models.ScopeTicketsUpdate,
			wantAction: "ticket.update",
		},
		{
			name:       "transition",
			changes:    map[string]any{"status": models.TicketStatusInProgress},
			wantScope:  models.ScopeTicketsTransition,
			wantAction: "ticket.transition",
			wantRisky:  true,
		},
		{
			name:       "assignment",
			changes:    map[string]any{"assigned_to_service_principal_id": "sp-1"},
			wantScope:  models.ScopeTicketsAssign,
			wantAction: "ticket.assign",
			wantRisky:  true,
		},
		{
			name:    "mixed command",
			changes: map[string]any{"title": "updated", "status": models.TicketStatusPending},
			wantErr: true,
		},
		{
			name:    "server controlled trust",
			changes: map[string]any{"trust_level": models.TicketTrustLevelTrusted},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scope, action, risky, err := classifyTicketPatch(tt.changes)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("classifyTicketPatch() error = %v", err)
			}
			if scope != tt.wantScope || action != tt.wantAction || risky != tt.wantRisky {
				t.Fatalf(
					"classifyTicketPatch() = (%q, %q, %v), want (%q, %q, %v)",
					scope,
					action,
					risky,
					tt.wantScope,
					tt.wantAction,
					tt.wantRisky,
				)
			}
		})
	}
}

func TestAgentCommentAndAttachmentEndpointsRequireTicketLeaseHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &APIHandler{}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(agentauth.ContextPrincipalID, "service-principal-test")
		c.Set(agentauth.ContextCredentialID, "credential-test")
		c.Next()
	})
	router.POST("/tickets/:id/comments", handler.CreateComment)
	router.POST("/tickets/:id/attachments", handler.StoreAttachment)

	tests := []struct {
		name string
		path string
		body string
	}{
		{
			name: "comment",
			path: "/tickets/42/comments",
			body: `{"content":"must not be parsed without a lease"}`,
		},
		{
			name: "attachment",
			path: "/tickets/42/attachments",
			body: "not-even-a-multipart-body",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("If-Match", `"v1"`)
			request.Header.Set("X-Ticket-Lease", " \t")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusConflict {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var problem Problem
			if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
				t.Fatalf("decode problem: %v", err)
			}
			if problem.Code != ProblemLeaseConflict ||
				!strings.Contains(problem.Detail, "X-Ticket-Lease") {
				t.Fatalf("unexpected missing-lease problem: %+v", problem)
			}
		})
	}
}

func TestAPIHandlerRegistersAndServesLeaseCommandRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.Category{},
		&models.ServicePrincipal{},
		&models.AgentCredential{},
		&models.AgentPolicy{},
		&models.PolicyDecision{},
		&models.IdempotencyRecord{},
		&models.Ticket{},
		&models.TicketHistory{},
		&models.TicketLease{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:     "lease-route-user",
		Email:        "lease-route@example.com",
		PasswordHash: "hash",
		Role:         models.RoleAgent,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	native := services.NewAgentNativeService(db)
	principal, err := native.CreateServicePrincipal(context.Background(), services.CreateServicePrincipalInput{
		Name:                "lease-route-agent",
		Scopes:              []string{models.ScopeTasksManage},
		CompatibilityUserID: &user.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := native.IssueCredential(context.Background(), principal.ID, "route-test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		TicketNumber: "LEASE-ROUTE-1",
		Title:        "Lease route contract",
		Description:  "Exercise registered claim and heartbeat paths.",
		Type:         models.TicketTypeRequest,
		Priority:     models.TicketPriorityNormal,
		Status:       models.TicketStatusOpen,
		Source:       models.TicketSourceAgent,
		CreatedByID:  user.ID,
		Version:      1,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}

	tokens := agentauth.NewManager("lease-route-secret", "https://issuer.example.test", "https://api.example.test", time.Hour)
	accessToken, _, err := tokens.Issue(&agentauth.Principal{
		ID:           principal.ID,
		CredentialID: credential.Credential.ID,
		ClientID:     "lease-route-client",
		Name:         principal.Name,
		Scopes:       []string{models.ScopeTasksManage},
		Active:       true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAPIHandler(db, native, tokens, user.ID, 1<<20, nil)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/v1"))

	doRequest := func(path, idempotencyKey, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+accessToken)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("If-Match", `"v1"`)
		request.Header.Set("Idempotency-Key", idempotencyKey)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}

	claimBody := `{"ttl_seconds":60}`
	claimPath := fmt.Sprintf("/api/v1/tickets/%d/claim", ticket.ID)
	claimResponse := doRequest(claimPath, "claim-route-key", claimBody)
	if claimResponse.Code != http.StatusOK {
		t.Fatalf("claim status=%d body=%s", claimResponse.Code, claimResponse.Body.String())
	}
	var claimEnvelope struct {
		Data struct {
			LeaseID string `json:"lease_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(claimResponse.Body.Bytes(), &claimEnvelope); err != nil {
		t.Fatal(err)
	}
	if claimEnvelope.Data.LeaseID == "" {
		t.Fatalf("claim response omitted lease_id: %s", claimResponse.Body.String())
	}
	var claimRecord models.IdempotencyRecord
	if err := db.Where(
		"actor_id = ? AND operation = ? AND key = ?",
		principal.ID,
		"ticket.claim",
		"claim-route-key",
	).First(&claimRecord).Error; err != nil {
		t.Fatal(err)
	}
	wantClaimHash := digestBytes(commandFingerprint(
		http.MethodPost,
		claimPath,
		1,
		"",
		[]byte(claimBody),
	))
	if claimRecord.RequestHash != wantClaimHash {
		t.Fatalf("claim fingerprint did not use canonical route: got=%s want=%s", claimRecord.RequestHash, wantClaimHash)
	}

	heartbeatBody := `{"ttl_seconds":90}`
	heartbeatPath := "/api/v1/leases/" + claimEnvelope.Data.LeaseID + "/heartbeat"
	heartbeatResponse := doRequest(heartbeatPath, "heartbeat-route-key", heartbeatBody)
	if heartbeatResponse.Code != http.StatusOK {
		t.Fatalf("heartbeat status=%d body=%s", heartbeatResponse.Code, heartbeatResponse.Body.String())
	}
	var heartbeatRecord models.IdempotencyRecord
	if err := db.Where(
		"actor_id = ? AND operation = ? AND key = ?",
		principal.ID,
		"ticket.lease.heartbeat",
		"heartbeat-route-key",
	).First(&heartbeatRecord).Error; err != nil {
		t.Fatal(err)
	}
	heartbeatFingerprintBody, _ := json.Marshal(gin.H{
		"lease_id":         claimEnvelope.Data.LeaseID,
		"expected_version": uint64(1),
		"ttl_seconds":      90,
	})
	wantHeartbeatHash := digestBytes(commandFingerprint(
		http.MethodPost,
		heartbeatPath,
		1,
		claimEnvelope.Data.LeaseID,
		heartbeatFingerprintBody,
	))
	if heartbeatRecord.RequestHash != wantHeartbeatHash {
		t.Fatalf(
			"heartbeat fingerprint did not use canonical route: got=%s want=%s",
			heartbeatRecord.RequestHash,
			wantHeartbeatHash,
		)
	}
}

func TestIdempotentCommentReplayMatchesInitialEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.ServicePrincipal{},
		&models.Ticket{},
		&models.TicketComment{},
	); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username: "replay-user", Email: "replay@example.com", PasswordHash: "hash",
		Role: models.RoleAgent, Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		TicketNumber: "REPLAY-1", Title: "ticket", Description: "description",
		Type: models.TicketTypeRequest, Priority: models.TicketPriorityNormal,
		Status: models.TicketStatusOpen, Source: models.TicketSourceAgent,
		CreatedByID: user.ID, Version: 2,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	comment := models.TicketComment{
		TicketID: ticket.ID, UserID: user.ID, ActorType: models.ActorTypeHuman,
		ActorID: models.HumanActor(user.ID).ID, Content: "result", ContentType: "text",
		Type: models.CommentTypeInternal,
	}
	if err := db.Create(&comment).Error; err != nil {
		t.Fatal(err)
	}
	receipt := Receipt{
		OperationID: "operation", ResourceID: fmt.Sprint(comment.ID),
		ResourceVersion: 2, EventID: "event", ChangedFields: []string{"comments"},
	}
	responseBody, _ := json.Marshal(receipt)
	record := &models.IdempotencyRecord{
		ResourceID: fmt.Sprint(comment.ID), ResponseCode: http.StatusCreated,
		ResponseBody: datatypes.JSON(responseBody),
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	(&APIHandler{db: db}).writeReplayedComment(context, record)

	if recorder.Code != http.StatusCreated || recorder.Header().Get("ETag") != `"v2"` {
		t.Fatalf("status=%d etag=%q body=%s", recorder.Code, recorder.Header().Get("ETag"), recorder.Body.String())
	}
	var envelope struct {
		Data    models.TicketCommentResponse `json:"data"`
		Receipt Receipt                      `json:"receipt"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Content != "result" || envelope.Receipt.EventID != "event" {
		t.Fatalf("unexpected replay envelope: %#v", envelope)
	}
}

func TestIdempotentTicketReplayUsesOriginalSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	snapshot := models.TicketResponse{
		ID: 7, TicketNumber: "SNAPSHOT-7", Title: "original result", Version: 1,
	}
	snapshotBody, _ := json.Marshal(snapshot)
	receipt := Receipt{
		OperationID: "operation", ResourceID: "7", ResourceVersion: 1,
		EventID: "event", ChangedFields: []string{"ticket"},
	}
	receiptBody, _ := json.Marshal(receipt)
	record := &models.IdempotencyRecord{
		ResourceID: "7", ResponseCode: http.StatusCreated,
		ResponseBody: datatypes.JSON(receiptBody), ResourceSnapshot: datatypes.JSON(snapshotBody),
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	(&APIHandler{}).writeReplayedTicket(context, record, http.StatusCreated)

	var envelope struct {
		Data models.TicketResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Title != "original result" ||
		envelope.Data.Version != 1 ||
		recorder.Header().Get("ETag") != `"v1"` {
		t.Fatalf("replay did not preserve original snapshot: body=%s etag=%q", recorder.Body.String(), recorder.Header().Get("ETag"))
	}
}
