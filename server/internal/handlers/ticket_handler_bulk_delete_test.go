package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
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

func newHandlerTicketService(
	t *testing.T,
	db *gorm.DB,
) *services.TicketService {
	t.Helper()
	ensureHandlerTestProject(t, db)
	service, err := services.NewTicketService(
		db,
		services.NewAgentNativeService(db),
		nil,
		0,
	)
	if err != nil {
		t.Fatalf("NewTicketService() error = %v", err)
	}
	return service
}

func ensureHandlerTestProject(t *testing.T, db *gorm.DB) models.ProjectScope {
	t.Helper()
	if err := db.AutoMigrate(
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.Queue{},
	); err != nil {
		t.Fatalf("migrate handler project fixture: %v", err)
	}
	var project models.Project
	err := db.Where("key = ?", models.ProjectKey("TEST")).First(&project).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		organization := models.Organization{
			Slug:   "handler-test",
			Name:   "Handler Test",
			Status: models.OrganizationStatusActive,
		}
		if err := db.Create(&organization).Error; err != nil {
			t.Fatalf("seed handler organization: %v", err)
		}
		unit := models.BusinessUnit{
			OrganizationID: organization.ID,
			Key:            "TEST",
			Name:           "Test",
			Status:         models.BusinessUnitStatusActive,
		}
		if err := db.Create(&unit).Error; err != nil {
			t.Fatalf("seed handler business unit: %v", err)
		}
		project = models.Project{
			OrganizationID: organization.ID,
			BusinessUnitID: unit.ID,
			Key:            "TEST",
			Name:           "Test",
			Status:         models.ProjectStatusActive,
		}
		if err := db.Create(&project).Error; err != nil {
			t.Fatalf("seed handler project: %v", err)
		}
	} else if err != nil {
		t.Fatalf("load handler project: %v", err)
	}
	var queue models.Queue
	err = db.Where("project_id = ? AND key = ?", project.ID, models.QueueKey("default")).
		First(&queue).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		queue = models.Queue{
			ProjectID: project.ID,
			Key:       "default",
			Name:      "Default",
			Status:    models.QueueStatusActive,
			IsDefault: true,
		}
		if err := db.Create(&queue).Error; err != nil {
			t.Fatalf("seed handler queue: %v", err)
		}
	} else if err != nil {
		t.Fatalf("load handler queue: %v", err)
	}
	scope := project.Scope()
	if db.Migrator().HasTable(&models.Ticket{}) {
		if err := db.Model(&models.Ticket{}).
			Where("organization_id = 0 OR project_id = 0").
			Updates(map[string]any{
				"organization_id": scope.OrganizationID,
				"project_id":      scope.ProjectID,
				"queue_id":        queue.ID,
			}).Error; err != nil {
			t.Fatalf("scope handler tickets: %v", err)
		}
	}
	for _, model := range []any{
		&models.TicketComment{},
		&models.TicketAttachment{},
		&models.TicketHistory{},
		&models.TicketLease{},
		&models.IdempotencyRecord{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	} {
		if !db.Migrator().HasTable(model) {
			continue
		}
		if err := db.Model(model).
			Where("organization_id = 0 OR project_id = 0").
			Updates(map[string]any{
				"organization_id": scope.OrganizationID,
				"project_id":      scope.ProjectID,
			}).Error; err != nil {
			t.Fatalf("scope handler fixture %T: %v", model, err)
		}
	}
	return scope
}

func handlerTestProjectMiddleware(
	t *testing.T,
	db *gorm.DB,
) gin.HandlerFunc {
	t.Helper()
	scope := ensureHandlerTestProject(t, db)
	return func(c *gin.Context) {
		ctx, err := services.WithOperationContext(
			c.Request.Context(),
			services.OperationContext{
				Scope:  scope,
				Actor:  models.HumanActor(c.GetUint("user_id")),
				Source: services.SourceProtocolHumanREST,
			},
		)
		if err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func handlerTestRequestContext(
	t *testing.T,
	db *gorm.DB,
	userID uint,
) context.Context {
	t.Helper()
	ctx, err := services.WithOperationContext(
		context.Background(),
		services.OperationContext{
			Scope:  ensureHandlerTestProject(t, db),
			Actor:  models.HumanActor(userID),
			Source: services.SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatalf("build handler operation context: %v", err)
	}
	return ctx
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
		&models.DomainEvent{},
		&models.OutboxDelivery{},
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
		CreatedByID:  &admin.ID,
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
		CreatedByID:  &admin.ID,
	}
	if err := db.Create(&ticket2).Error; err != nil {
		t.Fatalf("failed to create ticket2: %v", err)
	}

	handler := NewTicketHandler(newHandlerTicketService(t, db))
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", admin.ID)
		c.Set(projectRoleContextKey, string(models.ProjectRoleAdmin))
		c.Next()
	})
	router.Use(handlerTestProjectMiddleware(t, db))
	router.DELETE("/tickets/bulk-delete", handler.BulkDeleteTickets)

	body, err := json.Marshal(map[string]interface{}{
		"tickets": []map[string]any{
			{"id": ticket1.ID, "version": ticket1.Version},
			{"id": ticket2.ID, "version": ticket2.Version},
		},
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
