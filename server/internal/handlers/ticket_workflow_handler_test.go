package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"gongdan-system/internal/models"
	"gongdan-system/internal/services"
)

func setupWorkflowHandlerTest(t *testing.T) (*TicketWorkflowHandler, models.User, models.User, models.Ticket) {
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
		CreatedByID:  agent.ID,
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
		CreatedByID:  otherAgent.ID,
		AssignedToID: &otherAgent.ID,
	}
	if err := db.Create(&otherTicket).Error; err != nil {
		t.Fatalf("failed to create other ticket: %v", err)
	}

	return NewTicketWorkflowHandler(services.NewTicketService(db)), agent, otherAgent, assignedTicket
}

func TestTicketStatsUsesAuthenticatedUserRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, agent, _, _ := setupWorkflowHandlerTest(t)

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
	handler, agent, _, ticket := setupWorkflowHandlerTest(t)

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

func jsonNumber(value uint) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
