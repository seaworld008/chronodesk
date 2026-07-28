package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"gongdan-system/internal/models"
	"gongdan-system/internal/services"
)

func openHandlerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}
	return db
}

func TestBulkDeleteTicketsHandler_RemovesRequestedTickets(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.Ticket{},
		&models.Notification{},
		&models.TicketHistory{},
		&models.TicketComment{},
		&models.TicketAttachment{},
	); err != nil {
		t.Fatalf("failed to migrate schemas: %v", err)
	}

	admin := models.User{
		Username:     "admin-handler",
		Email:        "admin-handler@example.com",
		PasswordHash: "hashed",
		Role:         models.RoleAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatalf("failed to create admin user: %v", err)
	}

	ticket1 := models.Ticket{
		TicketNumber: "H-DELETE-001",
		Title:        "ticket-1",
		Description:  "desc",
		Priority:     models.TicketPriorityNormal,
		Status:       models.TicketStatusOpen,
		Type:         models.TicketTypeRequest,
		Source:       models.TicketSourceWeb,
		CreatedByID:  admin.ID,
	}
	if err := db.Create(&ticket1).Error; err != nil {
		t.Fatalf("failed to create ticket1: %v", err)
	}

	ticket2 := models.Ticket{
		TicketNumber: "H-DELETE-002",
		Title:        "ticket-2",
		Description:  "desc",
		Priority:     models.TicketPriorityNormal,
		Status:       models.TicketStatusOpen,
		Type:         models.TicketTypeRequest,
		Source:       models.TicketSourceWeb,
		CreatedByID:  admin.ID,
	}
	if err := db.Create(&ticket2).Error; err != nil {
		t.Fatalf("failed to create ticket2: %v", err)
	}

	handler := NewTicketHandler(services.NewTicketService(db))
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", admin.ID)
		c.Set("user_role", "admin")
		c.Next()
	})
	router.DELETE("/tickets/bulk-delete", handler.BulkDeleteTickets)

	body, err := json.Marshal(map[string]interface{}{
		"ids": []uint{ticket1.ID, ticket2.ID},
	})
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/tickets/bulk-delete", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", resp.Code, resp.Body.String())
	}

	var count int64
	if err := db.Model(&models.Ticket{}).Where("id IN ?", []uint{ticket1.ID, ticket2.ID}).Count(&count).Error; err != nil {
		t.Fatalf("failed to count tickets: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected tickets to be deleted, remaining: %d", count)
	}
}
