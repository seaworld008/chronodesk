package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/seaworld008/chronodesk/server/internal/httpcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

type authorizationRaceTicketService struct {
	services.TicketServiceInterface
	versioned services.TicketServiceInterface
	once      sync.Once
	before    func()
}

func (s *authorizationRaceTicketService) race() {
	s.once.Do(s.before)
}

func (s *authorizationRaceTicketService) UpdateTicketExpectedVersion(
	ctx context.Context,
	id uint,
	req *models.TicketUpdateRequest,
	userID uint,
	version uint64,
) (*models.Ticket, error) {
	s.race()
	return s.versioned.UpdateTicketExpectedVersion(ctx, id, req, userID, version)
}

func (s *authorizationRaceTicketService) AssignTicketExpectedVersion(
	ctx context.Context,
	ticketID, assigneeID, userID uint,
	comment string,
	version uint64,
) (*models.Ticket, error) {
	s.race()
	return s.versioned.AssignTicketExpectedVersion(ctx, ticketID, assigneeID, userID, comment, version)
}

func (s *authorizationRaceTicketService) TransferTicketExpectedVersion(
	ctx context.Context,
	ticketID, assigneeID, userID uint,
	comment, reason string,
	version uint64,
) (*models.Ticket, error) {
	s.race()
	return s.versioned.TransferTicketExpectedVersion(ctx, ticketID, assigneeID, userID, comment, reason, version)
}

func (s *authorizationRaceTicketService) EscalateTicketExpectedVersion(
	ctx context.Context,
	ticketID, escalateToID, userID uint,
	reason, comment string,
	version uint64,
) (*models.Ticket, error) {
	s.race()
	return s.versioned.EscalateTicketExpectedVersion(ctx, ticketID, escalateToID, userID, reason, comment, version)
}

func (s *authorizationRaceTicketService) UpdateTicketStatusExpectedVersion(
	ctx context.Context,
	ticketID uint,
	status string,
	userID uint,
	comment, resolutionNotes string,
	version uint64,
) (*models.Ticket, error) {
	s.race()
	return s.versioned.UpdateTicketStatusExpectedVersion(ctx, ticketID, status, userID, comment, resolutionNotes, version)
}

func setupWorkflowHandlerTest(
	t *testing.T,
) (*TicketWorkflowHandler, *gorm.DB, models.User, models.User, models.Ticket, models.Ticket) {
	t.Helper()

	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.Ticket{},
		&models.TicketComment{},
		&models.TicketHistory{},
		&models.Notification{},
	); err != nil {
		t.Fatalf("failed to migrate schemas: %v", err)
	}

	agent := models.User{
		Username:     "workflow-agent",
		Email:        "workflow-agent@example.com",
		PasswordHash: "hashed",
		Role:         models.RoleAgent,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&agent).Error; err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	otherAgent := models.User{
		Username:     "workflow-other-agent",
		Email:        "workflow-other-agent@example.com",
		PasswordHash: "hashed",
		Role:         models.RoleAgent,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&otherAgent).Error; err != nil {
		t.Fatalf("failed to create other agent: %v", err)
	}

	assignedTicket := models.Ticket{
		TicketNumber: "WF-001",
		Title:        "Assigned ticket",
		Description:  "desc",
		Priority:     models.TicketPriorityNormal,
		Status:       models.TicketStatusOpen,
		Type:         models.TicketTypeRequest,
		Source:       models.TicketSourceWeb,
		CreatedByID:  &agent.ID,
		AssignedToID: &agent.ID,
	}
	if err := db.Create(&assignedTicket).Error; err != nil {
		t.Fatalf("failed to create assigned ticket: %v", err)
	}

	otherTicket := models.Ticket{
		TicketNumber: "WF-002",
		Title:        "Other ticket",
		Description:  "desc",
		Priority:     models.TicketPriorityHigh,
		Status:       models.TicketStatusOpen,
		Type:         models.TicketTypeIncident,
		Source:       models.TicketSourceWeb,
		CreatedByID:  &otherAgent.ID,
		AssignedToID: &otherAgent.ID,
	}
	if err := db.Create(&otherTicket).Error; err != nil {
		t.Fatalf("failed to create other ticket: %v", err)
	}

	return NewTicketWorkflowHandler(newHandlerTicketService(t, db)), db, agent, otherAgent, assignedTicket, otherTicket
}

func TestTicketStatsUsesAuthenticatedUserRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, _, agent, _, _, _ := setupWorkflowHandlerTest(t)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", agent.ID)
		c.Set("user_role", "agent")
		c.Next()
	})
	router.GET("/tickets/stats", handler.GetTicketStats)

	req := httptest.NewRequest(http.MethodGet, "/tickets/stats", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", resp.Code, resp.Body.String())
	}

	var body struct {
		Data struct {
			Total     int64 `json:"total"`
			MyTickets int64 `json:"my_tickets"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body.Data.Total != 1 || body.Data.MyTickets != 1 {
		t.Fatalf("agent stats must be scoped to assigned tickets, got total=%d my_tickets=%d", body.Data.Total, body.Data.MyTickets)
	}
}

func TestUpdateTicketStatusRejectsUnknownStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, _, agent, _, ticket, _ := setupWorkflowHandlerTest(t)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", agent.ID)
		c.Set("user_role", "agent")
		c.Next()
	})
	router.POST("/tickets/:id/status", handler.UpdateTicketStatus)

	body := bytes.NewBufferString(`{"status":"reopened","comment":"invalid state"}`)
	req := httptest.NewRequest(http.MethodPost, "/tickets/"+jsonNumber(ticket.ID)+"/status", body)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d, body=%s", resp.Code, resp.Body.String())
	}
}

func TestAgentCannotWorkflowTicketAssignedToAnotherAgent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, _, agent, _, _, otherTicket := setupWorkflowHandlerTest(t)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", agent.ID)
		c.Set("user_role", "agent")
		c.Next()
	})
	router.POST("/tickets/:id/status", handler.UpdateTicketStatus)

	body := bytes.NewBufferString(`{"status":"in_progress"}`)
	req := httptest.NewRequest(http.MethodPost, "/tickets/"+jsonNumber(otherTicket.ID)+"/status", body)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("agent updated another assignee's ticket: status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestCustomerQueueAndAggregateQueriesAreObjectScoped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, db, _, otherAgent, _, _ := setupWorkflowHandlerTest(t)
	customer := models.User{
		Username:     "workflow-customer",
		Email:        "workflow-customer@example.com",
		PasswordHash: "hashed",
		Role:         models.RoleCustomer,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&customer).Error; err != nil {
		t.Fatalf("create customer: %v", err)
	}
	yesterday := time.Now().Add(-24 * time.Hour)
	own := models.Ticket{
		TicketNumber: "WF-CUSTOMER",
		Title:        "Customer ticket",
		Description:  "desc",
		Priority:     models.TicketPriorityHigh,
		Status:       models.TicketStatusOpen,
		Type:         models.TicketTypeIncident,
		Source:       models.TicketSourceWeb,
		CreatedByID:  &customer.ID,
		DueDate:      &yesterday,
		SLABreached:  true,
	}
	other := models.Ticket{
		TicketNumber: "WF-PRIVATE",
		Title:        "Another customer's ticket",
		Description:  "desc",
		Priority:     models.TicketPriorityUrgent,
		Status:       models.TicketStatusOpen,
		Type:         models.TicketTypeIncident,
		Source:       models.TicketSourceWeb,
		CreatedByID:  &otherAgent.ID,
		DueDate:      &yesterday,
		SLABreached:  true,
	}
	if err := db.Create(&own).Error; err != nil {
		t.Fatalf("create customer ticket: %v", err)
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatalf("create private ticket: %v", err)
	}

	stats, err := handler.ticketService.GetTicketStatistics(customer.ID, "customer")
	if err != nil {
		t.Fatalf("customer stats: %v", err)
	}
	if stats.Total != 1 || stats.MyTickets != 1 {
		t.Fatalf(
			"customer aggregate leaked other tickets: total=%d my_tickets=%d",
			stats.Total,
			stats.MyTickets,
		)
	}
	overdue, overdueTotal, err := handler.ticketService.GetOverdueTickets(customer.ID, "customer")
	if err != nil {
		t.Fatalf("customer overdue tickets: %v", err)
	}
	if overdueTotal != 1 || len(overdue) != 1 || overdue[0].ID != own.ID {
		t.Fatalf("customer overdue query was not object scoped: total=%d tickets=%+v", overdueTotal, overdue)
	}
	breached, breachedTotal, err := handler.ticketService.GetSLABreachedTickets(customer.ID, "customer")
	if err != nil {
		t.Fatalf("customer SLA tickets: %v", err)
	}
	if breachedTotal != 1 || len(breached) != 1 || breached[0].ID != own.ID {
		t.Fatalf("customer SLA query was not object scoped: total=%d tickets=%+v", breachedTotal, breached)
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", customer.ID)
		c.Set("user_role", "customer")
		c.Next()
	})
	router.GET("/tickets/unassigned", handler.GetUnassignedTickets)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/tickets/unassigned", nil))
	if resp.Code != http.StatusForbidden {
		t.Fatalf("customer accessed unassigned queue: status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestCustomerSpecialListsUseSafeTicketDTO(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, db, _, _, _, _ := setupWorkflowHandlerTest(t)
	customer := models.User{
		Username: "safe-customer", Email: "private-user-email@example.com",
		Phone: "18800001111", PasswordHash: "hashed",
		DisplayName: "Safe Customer", Role: models.RoleCustomer, Status: models.UserStatusActive,
		LastLoginIP: "10.10.10.10",
	}
	if err := db.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().Add(-24 * time.Hour)
	ticket := models.Ticket{
		TicketNumber: "SAFE-CUSTOMER", Title: "safe list", Description: "description",
		Priority: models.TicketPriorityHigh, Status: models.TicketStatusOpen,
		Type: models.TicketTypeIncident, Source: models.TicketSourceWeb,
		CreatedByID: &customer.ID, AssignedToID: &customer.ID,
		DueDate: &yesterday, SLABreached: true, Version: 1,
		InternalNotes: "INTERNAL-NOTES-MUST-NOT-LEAK",
		AgentContext: datatypes.NewJSONType(models.AgentContext{
			Goal: "AGENT-CONTEXT-MUST-NOT-LEAK",
		}),
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.TicketComment{
		TicketID: ticket.ID, UserID: &customer.ID, ActorType: models.ActorTypeHuman,
		ActorID: strconv.FormatUint(uint64(customer.ID), 10), Content: "COMMENT-MUST-NOT-LEAK",
		Type: models.CommentTypeInternal,
	}).Error; err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", customer.ID)
		c.Set("user_role", "customer")
		c.Next()
	})
	router.GET("/tickets/my", handler.GetMyTickets)
	router.GET("/tickets/overdue", handler.GetOverdueTickets)
	router.GET("/tickets/sla-breach", handler.GetSLABreachedTickets)

	for _, path := range []string{"/tickets/my", "/tickets/overdue", "/tickets/sla-breach"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
		body := response.Body.String()
		for _, forbidden := range []string{
			`"internal_notes"`,
			`"comments"`,
			`"email":`,
			`"phone":`,
			`"role":`,
			`"last_login`,
			"INTERNAL-NOTES-MUST-NOT-LEAK",
			"AGENT-CONTEXT-MUST-NOT-LEAK",
			"COMMENT-MUST-NOT-LEAK",
			"private-user-email@example.com",
		} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s leaked %q: %s", path, forbidden, body)
			}
		}
	}
}

func TestCustomerHistoryUsesVisibleNarrowDTO(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, db, _, _, _, _ := setupWorkflowHandlerTest(t)
	customer := models.User{
		Username: "history-customer", Email: "history-private@example.com",
		PasswordHash: "hashed", Role: models.RoleCustomer, Status: models.UserStatusActive,
	}
	if err := db.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		TicketNumber: "HISTORY-SAFE", Title: "history", Description: "history",
		Priority: models.TicketPriorityNormal, Status: models.TicketStatusOpen,
		Type: models.TicketTypeRequest, Source: models.TicketSourceWeb,
		CreatedByID: &customer.ID, Version: 1,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	visible := models.TicketHistory{
		TicketID: ticket.ID, UserID: &customer.ID,
		Action: models.HistoryActionUpdate, Description: "visible history",
		IsVisible: true, SourceIP: "192.0.2.10", UserAgent: "SECRET-USER-AGENT",
		Details: `{"secret":"RAW-DETAIL"}`, Metadata: `{"secret":"RAW-METADATA"}`,
	}
	hidden := models.TicketHistory{
		TicketID: ticket.ID, UserID: &customer.ID,
		Action: models.HistoryActionSystem, Description: "HIDDEN-HISTORY-MUST-NOT-LEAK",
		IsVisible: false,
	}
	if err := db.Create(&[]models.TicketHistory{visible, hidden}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.TicketHistory{}).
		Where("ticket_id = ? AND description = ?", ticket.ID, hidden.Description).
		UpdateColumn("is_visible", false).Error; err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", customer.ID)
		c.Set("user_role", "customer")
		c.Next()
	})
	router.GET("/tickets/:id/history", handler.GetTicketHistory)
	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/tickets/"+jsonNumber(ticket.ID)+"/history", nil),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("history status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "visible history") {
		t.Fatalf("visible history missing: %s", body)
	}
	for _, forbidden := range []string{
		`"source_ip"`, `"user_agent"`, `"details"`, `"metadata"`, `"user"`,
		"192.0.2.10", "SECRET-USER-AGENT", "RAW-DETAIL", "RAW-METADATA",
		"HIDDEN-HISTORY-MUST-NOT-LEAK", "history-private@example.com",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("history leaked %q: %s", forbidden, body)
		}
	}
}

func TestAuthorizedAgentWorkflowVersionIsBoundToCAS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, db, agent, otherAgent, ticket, _ := setupWorkflowHandlerTest(t)
	base := newHandlerTicketService(t, db)
	racing := &authorizationRaceTicketService{
		TicketServiceInterface: base,
		versioned:              base,
		before: func() {
			if err := db.Model(&models.Ticket{}).
				Where("id = ?", ticket.ID).
				Updates(map[string]any{
					"assigned_to_id": otherAgent.ID,
					"version":        gorm.Expr("version + 1"),
				}).Error; err != nil {
				t.Fatalf("inject concurrent ownership change: %v", err)
			}
		},
	}
	handler := NewTicketWorkflowHandler(racing)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", agent.ID)
		c.Set("user_role", "agent")
		c.Next()
	})
	router.POST("/tickets/:id/status", handler.UpdateTicketStatus)
	request := httptest.NewRequest(
		http.MethodPost,
		"/tickets/"+jsonNumber(ticket.ID)+"/status",
		bytes.NewBufferString(`{"status":"in_progress"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", httpcontract.FormatETag(ticket.Version))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("workflow status=%d body=%s", response.Code, response.Body.String())
	}
	var persisted models.Ticket
	if err := db.First(&persisted, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Status != models.TicketStatusOpen || persisted.AssignedToID == nil ||
		*persisted.AssignedToID != otherAgent.ID {
		t.Fatalf("stale authorization modified ticket: %+v", persisted)
	}
}

func TestAuthorizedAgentUpdateVersionIsBoundToCAS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, db, agent, otherAgent, ticket, _ := setupWorkflowHandlerTest(t)
	base := newHandlerTicketService(t, db)
	racing := &authorizationRaceTicketService{
		TicketServiceInterface: base,
		versioned:              base,
		before: func() {
			if err := db.Model(&models.Ticket{}).
				Where("id = ?", ticket.ID).
				Updates(map[string]any{
					"assigned_to_id": otherAgent.ID,
					"version":        gorm.Expr("version + 1"),
				}).Error; err != nil {
				t.Fatalf("inject concurrent ownership change: %v", err)
			}
		},
	}
	handler := NewTicketHandler(racing)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", agent.ID)
		c.Set("user_role", "agent")
		c.Next()
	})
	router.PATCH("/tickets/:id", handler.UpdateTicket)
	request := httptest.NewRequest(
		http.MethodPatch,
		"/tickets/"+jsonNumber(ticket.ID),
		bytes.NewBufferString(`{"title":"stale overwrite"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", httpcontract.FormatETag(ticket.Version))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("update status=%d body=%s", response.Code, response.Body.String())
	}
	var persisted models.Ticket
	if err := db.First(&persisted, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Title != ticket.Title || persisted.AssignedToID == nil ||
		*persisted.AssignedToID != otherAgent.ID {
		t.Fatalf("stale authorization modified ticket: %+v", persisted)
	}
}

func jsonNumber(value uint) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
