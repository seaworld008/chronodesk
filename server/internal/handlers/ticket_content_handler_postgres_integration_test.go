package handlers

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/database"
	"github.com/seaworld008/chronodesk/server/internal/httpcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPostgresRequesterCommentAttachmentHTTPMatrix(t *testing.T) {
	fixture := openPostgresRequesterCommentAttachmentFixture(t)
	gin.SetMode(gin.TestMode)

	storage, err := services.NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatalf("create local attachment storage: %v", err)
	}
	native := services.NewAgentNativeService(
		fixture.runtimeDB,
		services.AgentNativeOptions{
			AttachmentStorage:  storage,
			AttachmentStaging:  storage,
			AttachmentMaxBytes: 1024,
		},
	)
	tickets, err := services.NewTicketService(fixture.runtimeDB, native, nil, 0)
	if err != nil {
		t.Fatalf("create PostgreSQL ticket service: %v", err)
	}
	projects, err := services.NewProjectService(fixture.runtimeDB)
	if err != nil {
		t.Fatalf("create PostgreSQL project service: %v", err)
	}
	handler := NewTicketContentHandler(fixture.runtimeDB, tickets, native, 1024)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", fixture.requester.ID)
		c.Next()
	})
	group := router.Group("/projects/:projectKey/tickets")
	group.Use(ProjectExternalScopeMiddleware(projects, fixture.runtimeDB))
	handler.RegisterExternalRoutes(group)

	tests := []struct {
		name       string
		commentID  uint
		wantStatus int
	}{
		{
			name:       "internal comment is forbidden",
			commentID:  fixture.internalComment.ID,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "other ticket public comment is forbidden",
			commentID:  fixture.otherTicketPublicComment.ID,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "deleted public comment is forbidden",
			commentID:  fixture.deletedComment.ID,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "own public comment is accepted",
			commentID:  fixture.publicComment.ID,
			wantStatus: http.StatusAccepted,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			request := postgresRequesterAttachmentRequest(
				t,
				fixture.project.Key,
				fixture.ticket.ID,
				test.commentID,
				fixture.ticket.Version,
			)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf(
					"POST attachment status=%d, want %d: %s",
					response.Code,
					test.wantStatus,
					response.Body.String(),
				)
			}
			for _, databaseError := range []string{
				"row-level security",
				"violates row level security",
				"permission denied for",
				"insufficient privilege",
			} {
				if strings.Contains(
					strings.ToLower(response.Body.String()),
					databaseError,
				) {
					t.Fatalf(
						"HTTP response exposed PostgreSQL RLS failure %q: %s",
						databaseError,
						response.Body.String(),
					)
				}
			}
			if test.wantStatus == http.StatusForbidden {
				assertPostgresRequesterAttachmentIntents(
					t,
					fixture.ownerDB,
					fixture.project.Scope(),
					fixture.ticket.ID,
					0,
				)
				return
			}
			assertPostgresRequesterAcceptedAttachment(
				t,
				fixture.ownerDB,
				fixture.project.Scope(),
				fixture.ticket.ID,
				fixture.publicComment.ID,
			)
			for _, privateValue := range []string{
				"storage_path",
				".staging/",
				"_attachment_upload_migration",
			} {
				if strings.Contains(
					response.Body.String(),
					privateValue,
				) {
					t.Fatalf(
						"accepted requester response leaked %q: %s",
						privateValue,
						response.Body.String(),
					)
				}
			}
		})
	}
}

type postgresRequesterCommentAttachmentFixture struct {
	ownerDB                  *gorm.DB
	runtimeDB                *gorm.DB
	project                  models.Project
	requester                models.User
	ticket                   models.Ticket
	publicComment            models.TicketComment
	internalComment          models.TicketComment
	deletedComment           models.TicketComment
	otherTicketPublicComment models.TicketComment
}

func openPostgresRequesterCommentAttachmentFixture(
	t *testing.T,
) postgresRequesterCommentAttachmentFixture {
	t.Helper()
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip(
			"set CHRONODESK_POSTGRES_INTEGRATION=1 for PostgreSQL requester attachment HTTP evidence",
		)
	}
	rawDSN := strings.TrimSpace(
		os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"),
	)
	if rawDSN == "" {
		t.Fatal("CHRONODESK_POSTGRES_INTEGRATION_DSN is required")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatalf("parse PostgreSQL integration DSN: %v", err)
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal(
				"requester attachment integration test requires a loopback PostgreSQL target",
			)
		}
	}

	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	schemaName := "chronodesk_content_http_" + suffix
	ownerRole := "chronodesk_content_owner_" + suffix
	runtimeRole := "chronodesk_content_runtime_" + suffix
	ownerPassword := "ChronoDeskContentOwner" + suffix + "!"
	runtimePassword := "ChronoDeskContentRuntime" + suffix + "!"
	quotedSchema := quotePostgresRequesterAttachmentIdentifier(schemaName)
	quotedOwnerRole := quotePostgresRequesterAttachmentIdentifier(ownerRole)
	quotedRuntimeRole := quotePostgresRequesterAttachmentIdentifier(runtimeRole)

	silentConfig := &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	}
	adminDB, err := gorm.Open(postgres.Open(rawDSN), silentConfig)
	if err != nil {
		t.Fatalf("open PostgreSQL integration administrator: %v", err)
	}
	adminSQL, err := adminDB.DB()
	if err != nil {
		t.Fatalf("open PostgreSQL integration administrator pool: %v", err)
	}
	schemaCreated := false
	ownerCreated := false
	runtimeCreated := false
	t.Cleanup(func() {
		if schemaCreated {
			if cleanupErr := adminDB.Exec(
				"DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE",
			).Error; cleanupErr != nil {
				t.Errorf("drop PostgreSQL requester attachment schema: %v", cleanupErr)
			}
		}
		if runtimeCreated {
			if cleanupErr := adminDB.Exec(
				"DROP ROLE IF EXISTS " + quotedRuntimeRole,
			).Error; cleanupErr != nil {
				t.Errorf("drop PostgreSQL requester attachment runtime role: %v", cleanupErr)
			}
		}
		if ownerCreated {
			if cleanupErr := adminDB.Exec(
				"DROP ROLE IF EXISTS " + quotedOwnerRole,
			).Error; cleanupErr != nil {
				t.Errorf("drop PostgreSQL requester attachment owner role: %v", cleanupErr)
			}
		}
		if closeErr := adminSQL.Close(); closeErr != nil {
			t.Errorf("close PostgreSQL integration administrator pool: %v", closeErr)
		}
	})

	if err := adminDB.Exec(
		"CREATE ROLE " + quotedOwnerRole +
			" LOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS PASSWORD " +
			quotePostgresRequesterAttachmentLiteral(ownerPassword),
	).Error; err != nil {
		t.Fatalf("create PostgreSQL requester attachment owner role: %v", err)
	}
	ownerCreated = true
	if err := adminDB.Exec(
		"CREATE SCHEMA " + quotedSchema + " AUTHORIZATION " + quotedOwnerRole,
	).Error; err != nil {
		t.Fatalf("create isolated PostgreSQL requester attachment schema: %v", err)
	}
	schemaCreated = true

	ownerURL := *parsed
	ownerURL.User = url.UserPassword(ownerRole, ownerPassword)
	ownerQuery := ownerURL.Query()
	ownerQuery.Set("search_path", schemaName)
	ownerURL.RawQuery = ownerQuery.Encode()
	ownerDB, err := gorm.Open(postgres.Open(ownerURL.String()), silentConfig)
	if err != nil {
		t.Fatalf("open PostgreSQL requester attachment owner: %v", err)
	}
	ownerSQL, err := ownerDB.DB()
	if err != nil {
		t.Fatalf("open PostgreSQL requester attachment owner pool: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := ownerSQL.Close(); closeErr != nil {
			t.Errorf("close PostgreSQL requester attachment owner pool: %v", closeErr)
		}
	})

	if err := database.RunMigrations(
		ownerDB,
		services.EnsureProjectScopeMigrationMembership,
	); err != nil {
		t.Fatalf("run PostgreSQL requester attachment migrations: %v", err)
	}
	configuration, err := services.NewProjectConfigurationService(ownerDB)
	if err != nil {
		t.Fatalf("create PostgreSQL requester attachment configuration service: %v", err)
	}
	if err := configuration.BootstrapActiveProjects(context.Background()); err != nil {
		t.Fatalf("bootstrap PostgreSQL requester attachment configuration: %v", err)
	}

	var project models.Project
	if err := ownerDB.Where("key = ?", database.DefaultProjectKey).
		Take(&project).Error; err != nil {
		t.Fatalf("load PostgreSQL requester attachment project: %v", err)
	}
	requester := models.User{
		Username:     "requester-" + suffix,
		Email:        "requester-" + suffix + "@example.test",
		PasswordHash: "test-only-password-hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := ownerDB.Create(&requester).Error; err != nil {
		t.Fatalf("create PostgreSQL requester: %v", err)
	}
	if err := ownerDB.Create(&models.ProjectMembership{
		ProjectID: project.ID,
		UserID:    requester.ID,
		Role:      models.ProjectRoleRequester,
		IsActive:  true,
		Version:   1,
	}).Error; err != nil {
		t.Fatalf("create active PostgreSQL requester membership: %v", err)
	}

	var queue models.Queue
	if err := ownerDB.Where(
		"project_id = ? AND is_default = ?",
		project.ID,
		true,
	).Take(&queue).Error; err != nil {
		t.Fatalf("load PostgreSQL requester attachment default queue: %v", err)
	}
	var requestType models.RequestTypeVersion
	if err := ownerDB.Where(
		"project_id = ? AND key = ? AND status = ?",
		project.ID,
		"request",
		models.ConfigurationStatusPublished,
	).Take(&requestType).Error; err != nil {
		t.Fatalf("load PostgreSQL requester attachment request type: %v", err)
	}
	var workflow models.WorkflowVersion
	if err := ownerDB.Where(
		"project_id = ? AND key = ? AND status = ?",
		project.ID,
		"default",
		models.ConfigurationStatusPublished,
	).Take(&workflow).Error; err != nil {
		t.Fatalf("load PostgreSQL requester attachment workflow: %v", err)
	}
	createTicket := func(number string) models.Ticket {
		ticket := models.Ticket{
			OrganizationID:       project.OrganizationID,
			ProjectID:            project.ID,
			QueueID:              queue.ID,
			RequestTypeVersionID: requestType.ID,
			WorkflowVersionID:    workflow.ID,
			TicketNumber:         number,
			Title:                "Requester attachment fixture",
			Description:          "Requester attachment fixture",
			Type:                 models.TicketTypeRequest,
			Priority:             models.TicketPriorityNormal,
			Status:               models.TicketStatusOpen,
			Source:               models.TicketSourceWeb,
			Version:              1,
			TrustLevel:           models.TicketTrustLevelUntrusted,
			CreatedByID:          &requester.ID,
			CreatedByActorType:   models.ActorTypeHuman,
			CreatedByActorID:     models.HumanActor(requester.ID).ID,
		}
		if err := ownerDB.Create(&ticket).Error; err != nil {
			t.Fatalf("create PostgreSQL requester attachment ticket: %v", err)
		}
		return ticket
	}
	ticket := createTicket("CONTENT-" + suffix + "-1")
	otherTicket := createTicket("CONTENT-" + suffix + "-2")
	newComment := func(ticketID uint, content string, kind models.CommentType, deleted bool) models.TicketComment {
		comment := models.TicketComment{
			OrganizationID: project.OrganizationID,
			ProjectID:      project.ID,
			TicketID:       ticketID,
			UserID:         &requester.ID,
			ActorType:      models.ActorTypeHuman,
			ActorID:        models.HumanActor(requester.ID).ID,
			Content:        content,
			ContentType:    "text",
			Type:           kind,
			IsDeleted:      deleted,
		}
		if err := ownerDB.Create(&comment).Error; err != nil {
			t.Fatalf("create PostgreSQL requester attachment comment: %v", err)
		}
		return comment
	}
	publicComment := newComment(ticket.ID, "public", models.CommentTypePublic, false)
	internalComment := newComment(ticket.ID, "internal", models.CommentTypeInternal, false)
	deletedComment := newComment(ticket.ID, "deleted", models.CommentTypePublic, true)
	otherTicketPublicComment := newComment(
		otherTicket.ID,
		"other ticket public",
		models.CommentTypePublic,
		false,
	)

	if err := database.EnableProjectRLS(ownerDB); err != nil {
		t.Fatalf("enable PostgreSQL requester attachment FORCE RLS: %v", err)
	}
	if err := adminDB.Exec(
		"CREATE ROLE " + quotedRuntimeRole +
			" LOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS PASSWORD " +
			quotePostgresRequesterAttachmentLiteral(runtimePassword),
	).Error; err != nil {
		t.Fatalf("create PostgreSQL requester attachment runtime role: %v", err)
	}
	runtimeCreated = true
	for _, statement := range []string{
		"GRANT USAGE ON SCHEMA " + quotedSchema + " TO " + quotedRuntimeRole,
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA " +
			quotedSchema + " TO " + quotedRuntimeRole,
		"GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA " +
			quotedSchema + " TO " + quotedRuntimeRole,
	} {
		if err := ownerDB.Exec(statement).Error; err != nil {
			t.Fatalf("grant PostgreSQL requester attachment runtime privilege: %v", err)
		}
	}

	runtimeURL := *parsed
	runtimeURL.User = url.UserPassword(runtimeRole, runtimePassword)
	runtimeQuery := runtimeURL.Query()
	runtimeQuery.Set("search_path", schemaName)
	runtimeURL.RawQuery = runtimeQuery.Encode()
	runtimeDB, err := gorm.Open(postgres.Open(runtimeURL.String()), silentConfig)
	if err != nil {
		t.Fatalf("open PostgreSQL requester attachment runtime: %v", err)
	}
	runtimeSQL, err := runtimeDB.DB()
	if err != nil {
		t.Fatalf("open PostgreSQL requester attachment runtime pool: %v", err)
	}
	runtimeSQL.SetMaxOpenConns(4)
	runtimeSQL.SetMaxIdleConns(4)
	t.Cleanup(func() {
		if closeErr := runtimeSQL.Close(); closeErr != nil {
			t.Errorf("close PostgreSQL requester attachment runtime pool: %v", closeErr)
		}
	})
	if err := database.ValidateProjectRLSRuntime(runtimeDB); err != nil {
		t.Fatalf("validate PostgreSQL requester attachment FORCE RLS runtime: %v", err)
	}
	if err := database.ValidateProjectRuntimeRole(runtimeDB); err != nil {
		t.Fatalf("validate PostgreSQL requester attachment non-owner runtime role: %v", err)
	}
	if err := database.InstallProjectScopeTransactionRouting(runtimeDB); err != nil {
		t.Fatalf("install PostgreSQL requester attachment scope routing: %v", err)
	}

	return postgresRequesterCommentAttachmentFixture{
		ownerDB:                  ownerDB,
		runtimeDB:                runtimeDB,
		project:                  project,
		requester:                requester,
		ticket:                   ticket,
		publicComment:            publicComment,
		internalComment:          internalComment,
		deletedComment:           deletedComment,
		otherTicketPublicComment: otherTicketPublicComment,
	}
}

func postgresRequesterAttachmentRequest(
	t *testing.T,
	projectKey models.ProjectKey,
	ticketID uint,
	commentID uint,
	version uint64,
) *http.Request {
	t.Helper()
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	part, err := writer.CreateFormFile("file", "requester-proof.txt")
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write([]byte("requester attachment evidence")); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.WriteField("comment_id", strconv.FormatUint(uint64(commentID), 10)); err != nil {
		t.Fatalf("write multipart comment id: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart payload: %v", err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/projects/%s/tickets/%d/attachments", projectKey, ticketID),
		&payload,
	)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("If-Match", httpcontract.FormatETag(version))
	return request
}

func assertPostgresRequesterAttachmentIntents(
	t *testing.T,
	db *gorm.DB,
	scope models.ProjectScope,
	ticketID uint,
	want int64,
) {
	t.Helper()
	var intents int64
	err := database.WithProjectScopeTransaction(
		context.Background(),
		db,
		scope,
		func(scoped *gorm.DB) error {
			return scoped.Model(&models.TicketAttachment{}).
				Where("ticket_id = ?", ticketID).
				Count(&intents).Error
		},
	)
	if err != nil {
		t.Fatalf("count rejected PostgreSQL attachment intents: %v", err)
	}
	if intents != want {
		t.Fatalf("attachment staging intents=%d, want %d", intents, want)
	}
}

func assertPostgresRequesterAcceptedAttachment(
	t *testing.T,
	db *gorm.DB,
	scope models.ProjectScope,
	ticketID uint,
	commentID uint,
) {
	t.Helper()
	var attachments int64
	err := database.WithProjectScopeTransaction(
		context.Background(),
		db,
		scope,
		func(scoped *gorm.DB) error {
			return scoped.Model(&models.TicketAttachment{}).
				Where(
					"ticket_id = ? AND comment_id = ? AND storage_type = ? AND is_public = ?",
					ticketID,
					commentID,
					"staging",
					true,
				).
				Count(&attachments).Error
		},
	)
	if err != nil {
		t.Fatalf(
			"count accepted PostgreSQL attachment: %v",
			err,
		)
	}
	if attachments != 1 {
		t.Fatalf(
			"accepted requester attachment rows=%d, want 1",
			attachments,
		)
	}
}

func quotePostgresRequesterAttachmentIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quotePostgresRequesterAttachmentLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}
