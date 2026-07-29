package handlers

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"gongdan-system/internal/models"
	"gongdan-system/internal/services"

	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
)

func intPtr(value int) *int {
	return &value
}

func TestTicketContentCustomerVisibilityAndObjectAuthorization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.Ticket{},
		&models.TicketComment{},
		&models.TicketAttachment{},
		&models.ServicePrincipal{},
	); err != nil {
		t.Fatalf("migrate content schemas: %v", err)
	}
	owner := models.User{
		Username: "content-owner", Email: "content-owner@example.com",
		PasswordHash: "hashed", Role: models.RoleCustomer, Status: models.UserStatusActive,
	}
	other := models.User{
		Username: "content-other", Email: "content-other@example.com",
		PasswordHash: "hashed", Role: models.RoleCustomer, Status: models.UserStatusActive,
	}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	lastLogin := time.Now().Add(-time.Hour)
	agent := models.User{
		Username: "private-support-user", Email: "support-private@example.com",
		Phone: "18899990000", PasswordHash: "hashed", Role: models.RoleAgent,
		Status: models.UserStatusActive, TwoFactorEnabled: true, LastLoginAt: &lastLogin,
	}
	if err := db.Create(&agent).Error; err != nil {
		t.Fatal(err)
	}
	lastUsed := time.Now().Add(-time.Minute)
	principal := models.ServicePrincipal{
		ID: "PRIVATE-SERVICE-PRINCIPAL-ID", Name: "private-comment-agent",
		Status:             models.ServicePrincipalStatusActive,
		Scopes:             datatypes.JSON([]byte(`["tickets:read","comments:write"]`)),
		RateLimitPerMinute: 987, ConcurrentLimit: 654,
		EmergencyDisabled: true, LastUsedAt: &lastUsed,
	}
	if err := db.Create(&principal).Error; err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		TicketNumber: "CONTENT-1",
		Title:        "content", Description: "content",
		Type: models.TicketTypeRequest, Priority: models.TicketPriorityNormal,
		Status: models.TicketStatusOpen, Source: models.TicketSourceWeb,
		CreatedByID: owner.ID, Version: 1,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	comments := []models.TicketComment{
		{
			TicketID: ticket.ID, UserID: owner.ID, ActorType: models.ActorTypeHuman,
			ActorID: strconv.FormatUint(uint64(owner.ID), 10),
			Content: "public", ContentType: "text", Type: models.CommentTypePublic,
		},
		{
			TicketID: ticket.ID, UserID: owner.ID, ActorType: models.ActorTypeHuman,
			ActorID: strconv.FormatUint(uint64(owner.ID), 10),
			Content: "internal secret", ContentType: "text", Type: models.CommentTypeInternal,
		},
		{
			TicketID: ticket.ID, UserID: agent.ID, ActorType: models.ActorTypeHuman,
			ActorID: "PRIVATE-HUMAN-ACTOR-ID",
			Content: "support public", ContentType: "text", Type: models.CommentTypePublic,
			TimeSpent: intPtr(42), BillableTime: intPtr(21), WorkType: "PRIVATE-WORK-TYPE",
			NotificationSent: true, Metadata: `{"secret":"PRIVATE-COMMENT-METADATA"}`,
		},
		{
			TicketID: ticket.ID, UserID: agent.ID, ActorType: models.ActorTypeServicePrincipal,
			ActorID: "PRIVATE-SERVICE-ACTOR-ID", ServicePrincipalID: &principal.ID,
			Content: "service public", ContentType: "markdown", Type: models.CommentTypePublic,
			TimeSpent: intPtr(84), BillableTime: intPtr(63), WorkType: "PRIVATE-AGENT-WORK-TYPE",
			NotificationSent: true,
		},
	}
	if err := db.Create(&comments).Error; err != nil {
		t.Fatal(err)
	}

	handler := NewTicketContentHandler(db, services.NewTicketService(db), nil, 0)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		userID, _ := strconv.ParseUint(c.GetHeader("X-Test-User"), 10, 32)
		c.Set("user_id", uint(userID))
		c.Set("user_role", "customer")
		c.Next()
	})
	router.GET("/tickets/:id/comments", handler.ListComments)

	ownerRequest := httptest.NewRequest(
		http.MethodGet,
		"/tickets/"+strconv.FormatUint(uint64(ticket.ID), 10)+"/comments",
		nil,
	)
	ownerRequest.Header.Set("X-Test-User", strconv.FormatUint(uint64(owner.ID), 10))
	ownerResponse := httptest.NewRecorder()
	router.ServeHTTP(ownerResponse, ownerRequest)
	if ownerResponse.Code != http.StatusOK {
		t.Fatalf("owner response = %d: %s", ownerResponse.Code, ownerResponse.Body.String())
	}
	var body struct {
		Data []models.TicketCommentResponse `json:"data"`
	}
	if err := json.Unmarshal(ownerResponse.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 3 ||
		body.Data[0].Content != "public" ||
		body.Data[1].Content != "support public" ||
		body.Data[2].Content != "service public" {
		t.Fatalf("customer saw non-public comments: %#v", body.Data)
	}
	rawBody := ownerResponse.Body.String()
	for _, forbidden := range []string{
		`"user"`, `"service_principal"`, `"actor"`,
		`"time_spent"`, `"billable_time"`, `"work_type"`, `"notification_sent"`,
		`"metadata"`, `"attachments"`,
		`"email"`, `"phone"`, `"two_factor_enabled"`, `"last_login_at"`,
		`"scopes"`, `"rate_limit_per_minute"`, `"concurrent_limit"`, `"emergency_disabled"`,
		"support-private@example.com", "18899990000",
		"PRIVATE-HUMAN-ACTOR-ID", "PRIVATE-SERVICE-ACTOR-ID", "PRIVATE-SERVICE-PRINCIPAL-ID",
		"PRIVATE-WORK-TYPE", "PRIVATE-AGENT-WORK-TYPE", "PRIVATE-COMMENT-METADATA",
	} {
		if strings.Contains(rawBody, forbidden) {
			t.Fatalf("customer comment response leaked %q: %s", forbidden, rawBody)
		}
	}

	otherRequest := httptest.NewRequest(
		http.MethodGet,
		"/tickets/"+strconv.FormatUint(uint64(ticket.ID), 10)+"/comments",
		nil,
	)
	otherRequest.Header.Set("X-Test-User", strconv.FormatUint(uint64(other.ID), 10))
	otherResponse := httptest.NewRecorder()
	router.ServeHTTP(otherResponse, otherRequest)
	if otherResponse.Code != http.StatusForbidden {
		t.Fatalf("other customer response = %d: %s", otherResponse.Code, otherResponse.Body.String())
	}
}

func TestCustomerCannotReferenceInternalDeletedOrCrossTicketComment(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.Ticket{},
		&models.TicketComment{},
		&models.TicketAttachment{},
	); err != nil {
		t.Fatalf("migrate content schemas: %v", err)
	}
	customer := models.User{
		Username: "reference-owner", Email: "reference-owner@example.com",
		PasswordHash: "hashed", Role: models.RoleCustomer, Status: models.UserStatusActive,
	}
	if err := db.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}
	tickets := []models.Ticket{
		{
			TicketNumber: "REFERENCE-1", Title: "one", Description: "one",
			Type: models.TicketTypeRequest, Priority: models.TicketPriorityNormal,
			Status: models.TicketStatusOpen, Source: models.TicketSourceWeb,
			CreatedByID: customer.ID, Version: 1,
		},
		{
			TicketNumber: "REFERENCE-2", Title: "two", Description: "two",
			Type: models.TicketTypeRequest, Priority: models.TicketPriorityNormal,
			Status: models.TicketStatusOpen, Source: models.TicketSourceWeb,
			CreatedByID: customer.ID, Version: 1,
		},
	}
	if err := db.Create(&tickets).Error; err != nil {
		t.Fatal(err)
	}
	comments := []models.TicketComment{
		{TicketID: tickets[0].ID, UserID: customer.ID, Content: "internal", Type: models.CommentTypeInternal},
		{TicketID: tickets[0].ID, UserID: customer.ID, Content: "deleted", Type: models.CommentTypePublic, IsDeleted: true},
		{TicketID: tickets[1].ID, UserID: customer.ID, Content: "other ticket", Type: models.CommentTypePublic},
	}
	if err := db.Create(&comments).Error; err != nil {
		t.Fatal(err)
	}

	handler := NewTicketContentHandler(db, services.NewTicketService(db), nil, 1024)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", customer.ID)
		c.Set("user_role", "customer")
		c.Next()
	})
	router.POST("/tickets/:id/comments", handler.CreateComment)
	router.POST("/tickets/:id/attachments", handler.StoreAttachment)

	for _, comment := range comments {
		t.Run(comment.Content+"_parent", func(t *testing.T) {
			payload := bytes.NewBufferString(`{"content":"reply","parent_id":` + jsonNumber(comment.ID) + `}`)
			request := httptest.NewRequest(
				http.MethodPost,
				"/tickets/"+jsonNumber(tickets[0].ID)+"/comments",
				payload,
			)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("parent reference status=%d body=%s", response.Code, response.Body.String())
			}
		})

		t.Run(comment.Content+"_attachment", func(t *testing.T) {
			var payload bytes.Buffer
			writer := multipart.NewWriter(&payload)
			part, err := writer.CreateFormFile("file", "proof.txt")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write([]byte("proof")); err != nil {
				t.Fatal(err)
			}
			if err := writer.WriteField("comment_id", jsonNumber(comment.ID)); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(
				http.MethodPost,
				"/tickets/"+jsonNumber(tickets[0].ID)+"/attachments",
				&payload,
			)
			request.Header.Set("Content-Type", writer.FormDataContentType())
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("attachment reference status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestCustomerCannotCreateCommentWorklog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.Ticket{}, &models.TicketComment{}); err != nil {
		t.Fatal(err)
	}
	customer := models.User{
		Username: "worklog-owner", Email: "worklog-owner@example.com",
		PasswordHash: "hashed", Role: models.RoleCustomer, Status: models.UserStatusActive,
	}
	if err := db.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		TicketNumber: "WORKLOG-1", Title: "one", Description: "one",
		Type: models.TicketTypeRequest, Priority: models.TicketPriorityNormal,
		Status: models.TicketStatusOpen, Source: models.TicketSourceWeb,
		CreatedByID: customer.ID, Version: 1,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	handler := NewTicketContentHandler(db, services.NewTicketService(db), nil, 0)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", customer.ID)
		c.Set("user_role", "customer")
		c.Next()
	})
	router.POST("/tickets/:id/comments", handler.CreateComment)

	for _, payload := range []string{
		`{"content":"x","time_spent":0}`,
		`{"content":"x","billable_time":0}`,
		`{"content":"x","work_type":"support"}`,
	} {
		request := httptest.NewRequest(
			http.MethodPost,
			"/tickets/"+jsonNumber(ticket.ID)+"/comments",
			bytes.NewBufferString(payload),
		)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("worklog payload %s status=%d body=%s", payload, response.Code, response.Body.String())
		}
	}
}
