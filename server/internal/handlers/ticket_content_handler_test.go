package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/httpcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func intPtr(value int) *int {
	return &value
}

func TestTicketContentPaginationIsBoundedStableAndSeparatesReplies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.ServicePrincipal{},
		&models.Ticket{},
		&models.TicketComment{},
		&models.TicketAttachment{},
	); err != nil {
		t.Fatalf("migrate paginated content schemas: %v", err)
	}
	admin := models.User{
		Username:     "content-pagination-admin",
		Email:        "content-pagination@example.com",
		PasswordHash: "hashed",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		TicketNumber: "CONTENT-PAGE",
		Title:        "content pagination",
		Description:  "content pagination",
		Type:         models.TicketTypeRequest,
		Priority:     models.TicketPriorityNormal,
		Status:       models.TicketStatusOpen,
		Source:       models.TicketSourceWeb,
		CreatedByID:  &admin.ID,
		Version:      1,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	sameCreatedAt := time.Now().UTC().Truncate(time.Second)
	comments := make([]models.TicketComment, 150)
	for index := range comments {
		comments[index] = models.TicketComment{
			CreatedAt:   sameCreatedAt,
			TicketID:    ticket.ID,
			UserID:      &admin.ID,
			ActorType:   models.ActorTypeHuman,
			ActorID:     strconv.FormatUint(uint64(admin.ID), 10),
			Content:     "top-level-" + strconv.Itoa(index+1),
			ContentType: "text",
			Type:        models.CommentTypePublic,
		}
	}
	if err := db.Create(&comments).Error; err != nil {
		t.Fatalf("seed 150 top-level comments: %v", err)
	}
	replies := make([]models.TicketComment, 3)
	for index := range replies {
		replies[index] = models.TicketComment{
			CreatedAt:   sameCreatedAt,
			TicketID:    ticket.ID,
			UserID:      &admin.ID,
			ActorType:   models.ActorTypeHuman,
			ActorID:     strconv.FormatUint(uint64(admin.ID), 10),
			Content:     "reply-" + strconv.Itoa(index+1),
			ContentType: "text",
			Type:        models.CommentTypePublic,
			ParentID:    &comments[0].ID,
		}
	}
	if err := db.Create(&replies).Error; err != nil {
		t.Fatalf("seed replies: %v", err)
	}

	ticketService := newHandlerTicketService(t, db)
	scope := ensureHandlerTestProject(t, db)
	foreignComment := models.TicketComment{
		OrganizationID: scope.OrganizationID + 100,
		ProjectID:      scope.ProjectID + 100,
		TicketID:       ticket.ID,
		UserID:         &admin.ID,
		ActorType:      models.ActorTypeHuman,
		ActorID:        strconv.FormatUint(uint64(admin.ID), 10),
		Content:        "foreign-project-comment",
		ContentType:    "text",
		Type:           models.CommentTypePublic,
	}
	if err := db.Session(&gorm.Session{SkipHooks: true}).Create(&foreignComment).Error; err != nil {
		t.Fatalf("seed foreign-project comment: %v", err)
	}
	foreignReply := foreignComment
	foreignReply.ID = 0
	foreignReply.Content = "foreign-project-reply"
	foreignReply.ParentID = &comments[0].ID
	if err := db.Session(&gorm.Session{SkipHooks: true}).Create(&foreignReply).Error; err != nil {
		t.Fatalf("seed foreign-project reply: %v", err)
	}

	handler := NewTicketContentHandler(db, ticketService, nil, 0)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", admin.ID)
		c.Set(projectRoleContextKey, string(models.ProjectRoleAdmin))
		c.Next()
	})
	router.Use(handlerTestProjectMiddleware(t, db))
	router.GET("/tickets/:id/comments", handler.ListComments)
	router.GET("/tickets/:id/comments/:comment_id/replies", handler.ListCommentReplies)
	router.GET("/tickets/:id/attachments", handler.ListAttachments)

	path := "/tickets/" + strconv.FormatUint(uint64(ticket.ID), 10)
	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, path+"/comments?page=2&page_size=25", nil),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("comments page status=%d body=%s", response.Code, response.Body.String())
	}
	var page struct {
		Data       []models.TicketCommentResponse `json:"data"`
		Total      int64                          `json:"total"`
		Page       int                            `json:"page"`
		PageSize   int                            `json:"page_size"`
		TotalPages int64                          `json:"total_pages"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 25 || page.Total != 150 || page.Page != 2 ||
		page.PageSize != 25 || page.TotalPages != 6 {
		t.Fatalf("unexpected comments page: %+v", page)
	}
	for index := range page.Data {
		if page.Data[index].ID != comments[index+25].ID ||
			page.Data[index].ParentID != nil {
			t.Fatalf("unstable or nested comment at %d: %+v", index, page.Data[index])
		}
	}

	firstPageResponse := httptest.NewRecorder()
	router.ServeHTTP(
		firstPageResponse,
		httptest.NewRequest(http.MethodGet, path+"/comments?page=1&page_size=25", nil),
	)
	if firstPageResponse.Code != http.StatusOK {
		t.Fatalf("first comments page status=%d body=%s", firstPageResponse.Code, firstPageResponse.Body.String())
	}
	if err := json.Unmarshal(firstPageResponse.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Data[0].ReplyCount != 3 || len(page.Data[0].Replies) != 0 {
		t.Fatalf("top-level reply projection=%+v", page.Data[0])
	}

	replyResponse := httptest.NewRecorder()
	router.ServeHTTP(
		replyResponse,
		httptest.NewRequest(
			http.MethodGet,
			path+"/comments/"+strconv.FormatUint(uint64(comments[0].ID), 10)+"/replies?page=2&page_size=2",
			nil,
		),
	)
	if replyResponse.Code != http.StatusOK {
		t.Fatalf("reply page status=%d body=%s", replyResponse.Code, replyResponse.Body.String())
	}
	if err := json.Unmarshal(replyResponse.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 || page.Total != 3 || page.TotalPages != 2 ||
		page.Data[0].ID != replies[2].ID {
		t.Fatalf("unexpected reply page: %+v", page)
	}

	for _, suffix := range []string{
		"/comments?page=0",
		"/comments?page=-1",
		"/comments?page_size=0",
		"/comments?page_size=-1",
		"/comments?page_size=101",
		"/comments?page_size=abc",
		"/attachments?page_size=101",
	} {
		invalidResponse := httptest.NewRecorder()
		router.ServeHTTP(
			invalidResponse,
			httptest.NewRequest(http.MethodGet, path+suffix, nil),
		)
		if invalidResponse.Code != http.StatusBadRequest ||
			!strings.Contains(invalidResponse.Body.String(), `"code":"invalid_pagination"`) {
			t.Fatalf(
				"%s status=%d body=%s",
				suffix,
				invalidResponse.Code,
				invalidResponse.Body.String(),
			)
		}
	}
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
		PasswordHash: "hashed", PlatformRole: models.PlatformRoleMember, Status: models.UserStatusActive,
	}
	other := models.User{
		Username: "content-other", Email: "content-other@example.com",
		PasswordHash: "hashed", PlatformRole: models.PlatformRoleMember, Status: models.UserStatusActive,
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
		Phone: "18899990000", PasswordHash: "hashed", PlatformRole: models.PlatformRoleMember,
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
		CreatedByID: &owner.ID, Version: 1,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	comments := []models.TicketComment{
		{
			TicketID: ticket.ID, UserID: &owner.ID, ActorType: models.ActorTypeHuman,
			ActorID: strconv.FormatUint(uint64(owner.ID), 10),
			Content: "public", ContentType: "text", Type: models.CommentTypePublic,
		},
		{
			TicketID: ticket.ID, UserID: &owner.ID, ActorType: models.ActorTypeHuman,
			ActorID: strconv.FormatUint(uint64(owner.ID), 10),
			Content: "internal secret", ContentType: "text", Type: models.CommentTypeInternal,
		},
		{
			TicketID: ticket.ID, UserID: &agent.ID, ActorType: models.ActorTypeHuman,
			ActorID: "PRIVATE-HUMAN-ACTOR-ID",
			Content: "support public", ContentType: "text", Type: models.CommentTypePublic,
			TimeSpent: intPtr(42), BillableTime: intPtr(21), WorkType: "PRIVATE-WORK-TYPE",
			NotificationSent: true, Metadata: `{"secret":"PRIVATE-COMMENT-METADATA"}`,
		},
		{
			TicketID: ticket.ID, ActorType: models.ActorTypeServicePrincipal,
			ActorID: "PRIVATE-SERVICE-ACTOR-ID", ServicePrincipalID: &principal.ID,
			Content: "service public", ContentType: "markdown", Type: models.CommentTypePublic,
			TimeSpent: intPtr(84), BillableTime: intPtr(63), WorkType: "PRIVATE-AGENT-WORK-TYPE",
			NotificationSent: true,
		},
	}
	if err := db.Create(&comments).Error; err != nil {
		t.Fatal(err)
	}
	scannedAt := time.Now().Add(-time.Minute)
	attachments := []models.TicketAttachment{
		{
			TicketID: ticket.ID, UploadedBy: &agent.ID,
			ActorType: models.ActorTypeServicePrincipal, ActorID: "PRIVATE-ATTACHMENT-ACTOR-ID",
			ServicePrincipalID: &principal.ID,
			FileName:           "PRIVATE-STORAGE-FILE-NAME.txt", OriginalName: "customer-visible.txt",
			FileSize: 12, MimeType: "text/plain", FileType: models.AttachmentTypeDocument,
			Extension: ".txt", StoragePath: "PRIVATE-STORAGE-PATH",
			IsPublic: true, DownloadCount: 99, Hash: "PRIVATE-ATTACHMENT-HASH",
			VirusScan: models.VirusScanClean, ScanDetails: "PRIVATE-SCAN-DETAILS",
			ScannedAt: &scannedAt, Description: "customer-visible-description",
		},
		{
			TicketID: ticket.ID, UploadedBy: &agent.ID,
			ActorType: models.ActorTypeHuman, ActorID: "PRIVATE-INTERNAL-ATTACHMENT-ACTOR",
			FileName: "private.txt", OriginalName: "private.txt", FileSize: 7,
			MimeType: "text/plain", StoragePath: "private/path", IsPublic: false,
			VirusScan: models.VirusScanPending,
		},
	}
	if err := db.Create(&attachments).Error; err != nil {
		t.Fatal(err)
	}

	handler := NewTicketContentHandler(db, newHandlerTicketService(t, db), nil, 0)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		userID, _ := strconv.ParseUint(c.GetHeader("X-Test-User"), 10, 32)
		c.Set("user_id", uint(userID))
		role := c.GetHeader("X-Test-Role")
		if role == "" {
			role = string(models.ProjectRoleRequester)
		}
		c.Set(projectRoleContextKey, role)
		c.Next()
	})
	router.Use(handlerTestProjectMiddleware(t, db))
	router.GET("/tickets/:id/comments", handler.ListComments)
	router.GET("/tickets/:id/attachments", handler.ListAttachments)

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
		Data  []models.TicketCommentResponse `json:"data"`
		Total int64                          `json:"total"`
	}
	if err := json.Unmarshal(ownerResponse.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 3 || len(body.Data) != 3 ||
		body.Data[0].Content != "public" ||
		body.Data[1].Content != "support public" ||
		body.Data[2].Content != "service public" {
		t.Fatalf("customer saw non-public comments: %#v", body.Data)
	}
	for _, role := range []models.ProjectRole{
		models.ProjectRoleAgent,
		models.ProjectRoleObserver,
	} {
		roleRequest := httptest.NewRequest(
			http.MethodGet,
			"/tickets/"+strconv.FormatUint(uint64(ticket.ID), 10)+"/comments",
			nil,
		)
		roleRequest.Header.Set("X-Test-User", strconv.FormatUint(uint64(owner.ID), 10))
		roleRequest.Header.Set("X-Test-Role", string(role))
		roleResponse := httptest.NewRecorder()
		router.ServeHTTP(roleResponse, roleRequest)
		if roleResponse.Code != http.StatusOK {
			t.Fatalf("%s comments status=%d body=%s", role, roleResponse.Code, roleResponse.Body.String())
		}
		var roleBody struct {
			Data  []models.TicketCommentResponse `json:"data"`
			Total int64                          `json:"total"`
		}
		if err := json.Unmarshal(roleResponse.Body.Bytes(), &roleBody); err != nil {
			t.Fatal(err)
		}
		if roleBody.Total != 4 || len(roleBody.Data) != 4 {
			t.Fatalf("%s comment page=%+v", role, roleBody)
		}
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

	attachmentRequest := httptest.NewRequest(
		http.MethodGet,
		"/tickets/"+strconv.FormatUint(uint64(ticket.ID), 10)+"/attachments",
		nil,
	)
	attachmentRequest.Header.Set("X-Test-User", strconv.FormatUint(uint64(owner.ID), 10))
	attachmentResponse := httptest.NewRecorder()
	router.ServeHTTP(attachmentResponse, attachmentRequest)
	if attachmentResponse.Code != http.StatusOK {
		t.Fatalf(
			"customer attachment response = %d: %s",
			attachmentResponse.Code,
			attachmentResponse.Body.String(),
		)
	}
	var attachmentBody struct {
		Data  []customerAttachmentResponse `json:"data"`
		Total int64                        `json:"total"`
	}
	if err := json.Unmarshal(attachmentResponse.Body.Bytes(), &attachmentBody); err != nil {
		t.Fatal(err)
	}
	if attachmentBody.Total != 1 || len(attachmentBody.Data) != 1 ||
		attachmentBody.Data[0].OriginalName != "customer-visible.txt" ||
		attachmentBody.Data[0].VirusScan != models.VirusScanClean {
		t.Fatalf("customer attachment data=%+v", attachmentBody.Data)
	}
	for _, role := range []models.ProjectRole{
		models.ProjectRoleAgent,
		models.ProjectRoleObserver,
	} {
		roleRequest := httptest.NewRequest(
			http.MethodGet,
			"/tickets/"+strconv.FormatUint(uint64(ticket.ID), 10)+"/attachments",
			nil,
		)
		roleRequest.Header.Set("X-Test-User", strconv.FormatUint(uint64(owner.ID), 10))
		roleRequest.Header.Set("X-Test-Role", string(role))
		roleResponse := httptest.NewRecorder()
		router.ServeHTTP(roleResponse, roleRequest)
		if roleResponse.Code != http.StatusOK {
			t.Fatalf("%s attachments status=%d body=%s", role, roleResponse.Code, roleResponse.Body.String())
		}
		var roleBody struct {
			Data  []models.TicketAttachmentResponse `json:"data"`
			Total int64                             `json:"total"`
		}
		if err := json.Unmarshal(roleResponse.Body.Bytes(), &roleBody); err != nil {
			t.Fatal(err)
		}
		if roleBody.Total != 2 || len(roleBody.Data) != 2 {
			t.Fatalf("%s attachment page=%+v", role, roleBody)
		}
	}
	rawAttachmentBody := attachmentResponse.Body.String()
	for _, forbidden := range []string{
		`"uploaded_by"`, `"actor_type"`, `"actor_id"`, `"service_principal_id"`,
		`"file_name"`, `"download_count"`, `"hash"`, `"scan_details"`,
		"PRIVATE-ATTACHMENT-ACTOR-ID", "PRIVATE-STORAGE-FILE-NAME",
		"PRIVATE-STORAGE-PATH", "PRIVATE-ATTACHMENT-HASH", "PRIVATE-SCAN-DETAILS",
		"private.txt",
	} {
		if strings.Contains(rawAttachmentBody, forbidden) {
			t.Fatalf(
				"customer attachment response leaked %q: %s",
				forbidden,
				rawAttachmentBody,
			)
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
		&models.ServicePrincipal{},
		&models.Ticket{},
		&models.TicketComment{},
		&models.TicketAttachment{},
		&models.TicketHistory{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.ProjectMembership{},
	); err != nil {
		t.Fatalf("migrate content schemas: %v", err)
	}
	customer := models.User{
		Username: "reference-owner", Email: "reference-owner@example.com",
		PasswordHash: "hashed", PlatformRole: models.PlatformRoleMember, Status: models.UserStatusActive,
	}
	if err := db.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}
	tickets := []models.Ticket{
		{
			TicketNumber: "REFERENCE-1", Title: "one", Description: "one",
			Type: models.TicketTypeRequest, Priority: models.TicketPriorityNormal,
			Status: models.TicketStatusOpen, Source: models.TicketSourceWeb,
			CreatedByID: &customer.ID, Version: 1,
		},
		{
			TicketNumber: "REFERENCE-2", Title: "two", Description: "two",
			Type: models.TicketTypeRequest, Priority: models.TicketPriorityNormal,
			Status: models.TicketStatusOpen, Source: models.TicketSourceWeb,
			CreatedByID: &customer.ID, Version: 1,
		},
	}
	if err := db.Create(&tickets).Error; err != nil {
		t.Fatal(err)
	}
	comments := []models.TicketComment{
		{TicketID: tickets[0].ID, UserID: &customer.ID, ActorType: models.ActorTypeHuman, ActorID: strconv.FormatUint(uint64(customer.ID), 10), Content: "internal", Type: models.CommentTypeInternal},
		{TicketID: tickets[0].ID, UserID: &customer.ID, ActorType: models.ActorTypeHuman, ActorID: strconv.FormatUint(uint64(customer.ID), 10), Content: "deleted", Type: models.CommentTypePublic, IsDeleted: true},
		{TicketID: tickets[1].ID, UserID: &customer.ID, ActorType: models.ActorTypeHuman, ActorID: strconv.FormatUint(uint64(customer.ID), 10), Content: "other ticket", Type: models.CommentTypePublic},
	}
	if err := db.Create(&comments).Error; err != nil {
		t.Fatal(err)
	}

	ticketService := newHandlerTicketService(t, db)
	scope := ensureHandlerTestProject(t, db)
	if err := db.Create(&models.ProjectMembership{
		ProjectID: scope.ProjectID,
		UserID:    customer.ID,
		Role:      models.ProjectRoleRequester,
		IsActive:  true,
	}).Error; err != nil {
		t.Fatalf("seed requester project membership: %v", err)
	}
	storage, err := services.NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	native := services.NewAgentNativeService(db, services.AgentNativeOptions{
		AttachmentStorage:  storage,
		AttachmentStaging:  storage,
		AttachmentMaxBytes: 1024,
	})
	handler := NewTicketContentHandler(db, ticketService, native, 1024)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", customer.ID)
		c.Set(projectRoleContextKey, string(models.ProjectRoleRequester))
		c.Next()
	})
	router.Use(handlerTestProjectMiddleware(t, db))
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
			request.Header.Set("If-Match", httpcontract.FormatETag(tickets[0].Version))
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
			request.Header.Set("If-Match", httpcontract.FormatETag(tickets[0].Version))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("attachment reference status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	publicComment := models.TicketComment{
		TicketID:  tickets[0].ID,
		UserID:    &customer.ID,
		ActorType: models.ActorTypeHuman,
		ActorID: strconv.FormatUint(
			uint64(customer.ID),
			10,
		),
		Content: "public",
		Type:    models.CommentTypePublic,
	}
	if err := db.Create(&publicComment).Error; err != nil {
		t.Fatal(err)
	}
	nestedParent := models.TicketComment{
		TicketID: tickets[0].ID, UserID: &customer.ID,
		ActorType: models.ActorTypeHuman,
		ActorID:   strconv.FormatUint(uint64(customer.ID), 10),
		Content:   "existing reply", ContentType: "text",
		Type: models.CommentTypePublic, ParentID: &publicComment.ID,
	}
	if err := db.Create(&nestedParent).Error; err != nil {
		t.Fatal(err)
	}
	nestedRequest := httptest.NewRequest(
		http.MethodPost,
		"/tickets/"+jsonNumber(tickets[0].ID)+"/comments",
		bytes.NewBufferString(
			`{"content":"nested reply","parent_id":`+
				jsonNumber(nestedParent.ID)+`}`,
		),
	)
	nestedRequest.Header.Set("Content-Type", "application/json")
	nestedRequest.Header.Set("If-Match", httpcontract.FormatETag(tickets[0].Version))
	nestedResponse := httptest.NewRecorder()
	router.ServeHTTP(nestedResponse, nestedRequest)
	if nestedResponse.Code != http.StatusBadRequest ||
		!strings.Contains(nestedResponse.Body.String(), `"code":"nested_comment_reply"`) {
		t.Fatalf(
			"nested requester reply status=%d body=%s",
			nestedResponse.Code,
			nestedResponse.Body.String(),
		)
	}
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	part, err := writer.CreateFormFile("file", "public-proof.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("proof")); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("comment_id", jsonNumber(publicComment.ID)); err != nil {
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
	request.Header.Set("If-Match", httpcontract.FormatETag(tickets[0].Version))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf(
			"public comment attachment status=%d body=%s",
			response.Code,
			response.Body.String(),
		)
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
		PasswordHash: "hashed", PlatformRole: models.PlatformRoleMember, Status: models.UserStatusActive,
	}
	if err := db.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		TicketNumber: "WORKLOG-1", Title: "one", Description: "one",
		Type: models.TicketTypeRequest, Priority: models.TicketPriorityNormal,
		Status: models.TicketStatusOpen, Source: models.TicketSourceWeb,
		CreatedByID: &customer.ID, Version: 1,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	handler := NewTicketContentHandler(db, newHandlerTicketService(t, db), nil, 0)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", customer.ID)
		c.Set(projectRoleContextKey, string(models.ProjectRoleRequester))
		c.Next()
	})
	router.Use(handlerTestProjectMiddleware(t, db))
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
		request.Header.Set("If-Match", httpcontract.FormatETag(ticket.Version))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("worklog payload %s status=%d body=%s", payload, response.Code, response.Body.String())
		}
	}
}

func TestStoreAttachmentRejectsInvalidMultipartAsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.Ticket{},
		&models.TicketComment{},
	); err != nil {
		t.Fatal(err)
	}
	admin := models.User{
		Username: "attachment-contract-admin", Email: "attachment-contract@example.com",
		PasswordHash: "hashed", PlatformRole: models.PlatformRolePlatformAdmin, Status: models.UserStatusActive,
	}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		TicketNumber: "ATTACHMENT-CONTRACT", Title: "attachment", Description: "attachment",
		Type: models.TicketTypeRequest, Priority: models.TicketPriorityNormal,
		Status: models.TicketStatusOpen, Source: models.TicketSourceWeb,
		CreatedByID: &admin.ID, Version: 1,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}

	handler := NewTicketContentHandler(db, newHandlerTicketService(t, db), nil, 1024)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", admin.ID)
		c.Set(projectRoleContextKey, string(models.ProjectRoleAdmin))
		c.Next()
	})
	router.Use(handlerTestProjectMiddleware(t, db))
	router.POST("/tickets/:id/attachments", handler.StoreAttachment)

	request := httptest.NewRequest(
		http.MethodPost,
		"/tickets/"+jsonNumber(ticket.ID)+"/attachments",
		strings.NewReader("visibility=internal"),
	)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("If-Match", httpcontract.FormatETag(ticket.Version))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf(
			"invalid multipart status=%d body=%s",
			response.Code,
			response.Body.String(),
		)
	}
}

func TestTicketContentWritesEnforceIfMatch(t *testing.T) {
	operations := []struct {
		name       string
		pathSuffix string
		register   func(*gin.Engine, *TicketContentHandler)
		request    func(*testing.T, string) *http.Request
	}{
		{
			name:       "comment",
			pathSuffix: "/comments",
			register: func(router *gin.Engine, handler *TicketContentHandler) {
				router.POST("/tickets/:id/comments", handler.CreateComment)
			},
			request: func(t *testing.T, path string) *http.Request {
				t.Helper()
				request := httptest.NewRequest(
					http.MethodPost,
					path,
					bytes.NewBufferString(`{"content":"并发版本回归","type":"public"}`),
				)
				request.Header.Set("Content-Type", "application/json")
				return request
			},
		},
		{
			name:       "attachment",
			pathSuffix: "/attachments",
			register: func(router *gin.Engine, handler *TicketContentHandler) {
				router.POST("/tickets/:id/attachments", handler.StoreAttachment)
			},
			request: func(t *testing.T, path string) *http.Request {
				t.Helper()
				var payload bytes.Buffer
				writer := multipart.NewWriter(&payload)
				part, err := writer.CreateFormFile("file", "customer-proof.txt")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := part.Write([]byte("proof")); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				request := httptest.NewRequest(http.MethodPost, path, &payload)
				request.Header.Set("Content-Type", writer.FormDataContentType())
				return request
			},
		},
	}

	for _, operation := range operations {
		operation := operation
		t.Run(operation.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			db := openHandlerTestDB(t)
			if err := db.AutoMigrate(
				&models.User{},
				&models.ServicePrincipal{},
				&models.Ticket{},
				&models.TicketComment{},
				&models.TicketAttachment{},
				&models.DomainEvent{},
				&models.OutboxDelivery{},
				&models.TicketHistory{},
				&models.ProjectMembership{},
			); err != nil {
				t.Fatalf("migrate content concurrency schemas: %v", err)
			}
			customer := models.User{
				Username:     "content-version-" + operation.name,
				Email:        "content-version-" + operation.name + "@example.com",
				PasswordHash: "hashed",
				PlatformRole: models.PlatformRoleMember,
				Status:       models.UserStatusActive,
			}
			if err := db.Create(&customer).Error; err != nil {
				t.Fatal(err)
			}
			ticket := models.Ticket{
				TicketNumber: "CONTENT-VERSION-" + strings.ToUpper(operation.name),
				Title:        "content version",
				Description:  "content version",
				Type:         models.TicketTypeRequest,
				Priority:     models.TicketPriorityNormal,
				Status:       models.TicketStatusOpen,
				Source:       models.TicketSourceWeb,
				CreatedByID:  &customer.ID,
				Version:      2,
			}
			if err := db.Create(&ticket).Error; err != nil {
				t.Fatal(err)
			}
			storage, err := services.NewLocalAttachmentStorage(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			native := services.NewAgentNativeService(db, services.AgentNativeOptions{
				AttachmentStorage:  storage,
				AttachmentStaging:  storage,
				AttachmentMaxBytes: 1024,
			})
			ticketService := newHandlerTicketService(t, db)
			scope := ensureHandlerTestProject(t, db)
			if err := db.Create(&models.ProjectMembership{
				ProjectID: scope.ProjectID,
				UserID:    customer.ID,
				Role:      models.ProjectRoleRequester,
				IsActive:  true,
			}).Error; err != nil {
				t.Fatalf("seed requester project membership: %v", err)
			}
			handler := NewTicketContentHandler(
				db,
				ticketService,
				native,
				1024,
			)
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set("user_id", customer.ID)
				c.Set(projectRoleContextKey, string(models.ProjectRoleRequester))
				c.Next()
			})
			router.Use(handlerTestProjectMiddleware(t, db))
			operation.register(router, handler)
			path := "/tickets/" + jsonNumber(ticket.ID) + operation.pathSuffix

			currentStatus := http.StatusCreated
			if operation.name == "attachment" {
				currentStatus = http.StatusAccepted
			}
			tests := []struct {
				name        string
				ifMatch     string
				wantStatus  int
				wantCode    string
				wantRecords int64
				wantVersion uint64
			}{
				{
					name:        "missing",
					wantStatus:  http.StatusPreconditionRequired,
					wantCode:    "precondition_required",
					wantVersion: 2,
				},
				{
					name:        "stale",
					ifMatch:     httpcontract.FormatETag(1),
					wantStatus:  http.StatusConflict,
					wantCode:    "version_conflict",
					wantVersion: 2,
				},
				{
					name:        "current",
					ifMatch:     httpcontract.FormatETag(2),
					wantStatus:  currentStatus,
					wantRecords: 1,
					wantVersion: 3,
				},
			}
			for _, test := range tests {
				test := test
				t.Run(test.name, func(t *testing.T) {
					request := operation.request(t, path)
					if test.ifMatch != "" {
						request.Header.Set("If-Match", test.ifMatch)
					}
					response := httptest.NewRecorder()
					router.ServeHTTP(response, request)
					if response.Code != test.wantStatus {
						t.Fatalf(
							"status=%d, want %d: %s",
							response.Code,
							test.wantStatus,
							response.Body.String(),
						)
					}
					if test.wantCode != "" {
						var problem humanTicketProblem
						if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
							t.Fatalf("decode problem response: %v", err)
						}
						if problem.Code != test.wantCode || problem.Status != test.wantStatus {
							t.Fatalf("problem=%+v", problem)
						}
						if got := response.Header().Get("Content-Type"); !strings.HasPrefix(
							got,
							"application/problem+json",
						) {
							t.Fatalf("Content-Type=%q", got)
						}
					} else if got, want := response.Header().Get("ETag"), httpcontract.FormatETag(3); got != want {
						t.Fatalf("ETag=%q, want %q", got, want)
					}

					var records int64
					query := db
					if operation.name == "comment" {
						query = query.Model(&models.TicketComment{})
					} else {
						query = query.Model(&models.TicketAttachment{})
					}
					if err := query.Where("ticket_id = ?", ticket.ID).Count(&records).Error; err != nil {
						t.Fatal(err)
					}
					if records != test.wantRecords {
						t.Fatalf("persisted records=%d, want %d", records, test.wantRecords)
					}
					var current models.Ticket
					if err := db.Select("version").First(&current, ticket.ID).Error; err != nil {
						t.Fatal(err)
					}
					if current.Version != test.wantVersion {
						t.Fatalf("ticket version=%d, want %d", current.Version, test.wantVersion)
					}
					if operation.name == "attachment" && test.wantRecords == 1 {
						for _, forbidden := range []string{
							`"uploaded_by"`, `"actor_type"`, `"actor_id"`,
							`"service_principal_id"`, `"file_name"`,
							`"download_count"`, `"hash"`, `"scan_details"`,
						} {
							if strings.Contains(response.Body.String(), forbidden) {
								t.Fatalf(
									"customer attachment upload leaked %q: %s",
									forbidden,
									response.Body.String(),
								)
							}
						}
						if !strings.Contains(response.Body.String(), `"virus_scan":"pending"`) {
							t.Fatalf("customer response lost public scan status: %s", response.Body.String())
						}
					}
				})
			}
		})
	}
}

func TestCreateCommentRejectsInvalidInputWithChineseContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.ServicePrincipal{},
		&models.Ticket{},
		&models.TicketComment{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.TicketHistory{},
	); err != nil {
		t.Fatalf("migrate comment contract schemas: %v", err)
	}
	admin := models.User{
		Username: "comment-contract-admin", Email: "comment-contract@example.com",
		PasswordHash: "hashed", PlatformRole: models.PlatformRolePlatformAdmin, Status: models.UserStatusActive,
	}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		TicketNumber: "COMMENT-CONTRACT", Title: "comment contract", Description: "comment contract",
		Type: models.TicketTypeRequest, Priority: models.TicketPriorityNormal,
		Status: models.TicketStatusOpen, Source: models.TicketSourceWeb,
		CreatedByID:        &admin.ID,
		CreatedByActorType: models.ActorTypeHuman,
		CreatedByActorID:   strconv.FormatUint(uint64(admin.ID), 10),
		Version:            1,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	handler := NewTicketContentHandler(
		db,
		newHandlerTicketService(t, db),
		services.NewAgentNativeService(db),
		0,
	)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", admin.ID)
		c.Set(projectRoleContextKey, string(models.ProjectRoleAdmin))
		c.Next()
	})
	router.Use(handlerTestProjectMiddleware(t, db))
	router.POST("/tickets/:id/comments", handler.CreateComment)

	longPayload, err := json.Marshal(map[string]any{
		"content": strings.Repeat("评", maxHumanCommentContentRunes+1),
		"type":    "public",
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		body        string
		code        string
		message     string
		contentType string
	}{
		{
			name:    "empty body",
			code:    "invalid_request",
			message: "请求正文不能为空",
		},
		{
			name:    "malformed JSON",
			body:    `{`,
			code:    "invalid_request",
			message: "请求正文必须是有效的 JSON 对象",
		},
		{
			name:    "unknown field",
			body:    `{"content":"有效评论","type":"public","unknown":true}`,
			code:    "invalid_request",
			message: "请求正文必须是有效的 JSON 对象",
		},
		{
			name:    "empty content",
			body:    `{"content":"","type":"public"}`,
			code:    "validation_error",
			message: "评论内容不能为空",
		},
		{
			name:    "blank content",
			body:    `{"content":" \t\n ","type":"public"}`,
			code:    "validation_error",
			message: "评论内容不能为空",
		},
		{
			name:    "content too long",
			body:    string(longPayload),
			code:    "validation_error",
			message: "评论内容不能超过 10000 个字符",
		},
		{
			name:    "invalid content type",
			body:    `{"content":"有效评论","content_type":"text/html","type":"public"}`,
			code:    "validation_error",
			message: "评论内容格式无效，仅支持纯文本或 Markdown",
		},
		{
			name:    "invalid comment type",
			body:    `{"content":"有效评论","type":"private"}`,
			code:    "validation_error",
			message: "评论类型无效，仅支持公开或内部评论",
		},
		{
			name:    "zero parent",
			body:    `{"content":"有效评论","type":"public","parent_id":0}`,
			code:    "validation_error",
			message: "父评论 ID 必须大于 0",
		},
		{
			name:    "missing parent",
			body:    `{"content":"有效评论","type":"public","parent_id":999999}`,
			code:    "validation_error",
			message: "评论请求不符合要求",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPost,
				"/tickets/"+jsonNumber(ticket.ID)+"/comments",
				bytes.NewBufferString(test.body),
			)
			contentType := test.contentType
			if contentType == "" {
				contentType = "application/json"
			}
			request.Header.Set("Content-Type", contentType)
			request.Header.Set("If-Match", httpcontract.FormatETag(ticket.Version))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var payload struct {
				Success bool   `json:"success"`
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if payload.Success || payload.Code != test.code || payload.Message != test.message {
				t.Fatalf("unexpected error contract: %+v", payload)
			}
			publicText := strings.ToLower(response.Body.String())
			for _, forbidden := range []string{
				"key: '",
				"field validation",
				"failed on the",
				"comment content must",
				"native comments support",
				"invalid comment type",
				"invalid comment",
			} {
				if strings.Contains(publicText, forbidden) {
					t.Fatalf("response leaked internal detail %q: %s", forbidden, response.Body.String())
				}
			}

			var commentCount int64
			if err := db.Model(&models.TicketComment{}).
				Where("ticket_id = ?", ticket.ID).
				Count(&commentCount).Error; err != nil {
				t.Fatal(err)
			}
			if commentCount != 0 {
				t.Fatalf("invalid request persisted %d comments", commentCount)
			}
			var current models.Ticket
			if err := db.Select("version").First(&current, ticket.ID).Error; err != nil {
				t.Fatal(err)
			}
			if current.Version != 1 {
				t.Fatalf("invalid request changed ticket version to %d", current.Version)
			}
		})
	}

	validRequest := httptest.NewRequest(
		http.MethodPost,
		"/tickets/"+jsonNumber(ticket.ID)+"/comments",
		bytes.NewBufferString(`{"content":"  已完成核查  ","content_type":"markdown","type":"public"}`),
	)
	validRequest.Header.Set("Content-Type", "application/json")
	validRequest.Header.Set("If-Match", httpcontract.FormatETag(ticket.Version))
	validResponse := httptest.NewRecorder()
	router.ServeHTTP(validResponse, validRequest)
	if validResponse.Code != http.StatusCreated {
		t.Fatalf(
			"valid comment status=%d body=%s",
			validResponse.Code,
			validResponse.Body.String(),
		)
	}
	var persisted models.TicketComment
	if err := db.Where("ticket_id = ?", ticket.ID).First(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Content != "已完成核查" ||
		persisted.ContentType != "markdown" ||
		persisted.Type != models.CommentTypePublic {
		t.Fatalf("valid comment was not normalized correctly: %+v", persisted)
	}
}

func TestCreateCommentKeepsHumanVisibilityDenial(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.Ticket{}, &models.TicketComment{}); err != nil {
		t.Fatal(err)
	}
	admin := models.User{
		Username: "comment-system-admin", Email: "comment-system-admin@example.com",
		PasswordHash: "hashed", PlatformRole: models.PlatformRolePlatformAdmin, Status: models.UserStatusActive,
	}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		TicketNumber: "COMMENT-SYSTEM", Title: "system comment", Description: "system comment",
		Type: models.TicketTypeRequest, Priority: models.TicketPriorityNormal,
		Status: models.TicketStatusOpen, Source: models.TicketSourceWeb,
		CreatedByID: &admin.ID, Version: 1,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	handler := NewTicketContentHandler(db, newHandlerTicketService(t, db), nil, 0)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", admin.ID)
		c.Set(projectRoleContextKey, string(models.ProjectRoleAdmin))
		c.Next()
	})
	router.Use(handlerTestProjectMiddleware(t, db))
	router.POST("/tickets/:id/comments", handler.CreateComment)

	request := httptest.NewRequest(
		http.MethodPost,
		"/tickets/"+jsonNumber(ticket.ID)+"/comments",
		bytes.NewBufferString(`{"content":"系统评论","type":"system"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", httpcontract.FormatETag(ticket.Version))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden ||
		!strings.Contains(response.Body.String(), `"code":"comment_visibility_denied"`) {
		t.Fatalf("system comment denial changed: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCreateCommentKeepsNotFoundConflictAndInternalErrorsSafe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.Ticket{}, &models.TicketComment{}); err != nil {
		t.Fatal(err)
	}
	admin := models.User{
		Username: "comment-error-admin", Email: "comment-error-admin@example.com",
		PasswordHash: "hashed", PlatformRole: models.PlatformRolePlatformAdmin, Status: models.UserStatusActive,
	}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	handler := NewTicketContentHandler(db, newHandlerTicketService(t, db), nil, 0)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", admin.ID)
		c.Set(projectRoleContextKey, string(models.ProjectRoleAdmin))
		c.Next()
	})
	router.Use(handlerTestProjectMiddleware(t, db))
	router.POST("/tickets/:id/comments", handler.CreateComment)

	notFoundRequest := httptest.NewRequest(
		http.MethodPost,
		"/tickets/999999/comments",
		bytes.NewBufferString(`{"content":"有效评论","type":"public"}`),
	)
	notFoundRequest.Header.Set("Content-Type", "application/json")
	notFoundResponse := httptest.NewRecorder()
	router.ServeHTTP(notFoundResponse, notFoundRequest)
	if notFoundResponse.Code != http.StatusNotFound ||
		!strings.Contains(notFoundResponse.Body.String(), `"message":"资源不存在"`) {
		t.Fatalf(
			"not-found contract changed: status=%d body=%s",
			notFoundResponse.Code,
			notFoundResponse.Body.String(),
		)
	}

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		forbidden  string
	}{
		{
			name:       "version conflict",
			err:        services.ErrVersionConflict,
			wantStatus: http.StatusConflict,
			wantCode:   "version_conflict",
		},
		{
			name:       "internal error",
			err:        errors.New("database password=PRIVATE must not leak"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "internal_error",
			forbidden:  "PRIVATE",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(response)
			context.Request = httptest.NewRequest(http.MethodPost, "/tickets/1/comments", nil)
			handler.writeCommentError(context, test.err)
			if response.Code != test.wantStatus ||
				!strings.Contains(response.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Fatalf(
					"status=%d body=%s",
					response.Code,
					response.Body.String(),
				)
			}
			if test.forbidden != "" && strings.Contains(response.Body.String(), test.forbidden) {
				t.Fatalf("response leaked internal error: %s", response.Body.String())
			}
		})
	}
}
