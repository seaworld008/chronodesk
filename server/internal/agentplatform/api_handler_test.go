package agentplatform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/agentauth"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type apiHandlerTestProject struct {
	organization models.Organization
	project      models.Project
	queue        models.Queue
}

func TestAgentDownloadAttachmentReturnsContentWithSafeHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dsn := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared",
		strings.ReplaceAll(t.Name(), "/", "_"),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open Agent attachment download database: %v", err)
	}
	project := ensureAPIHandlerTestProject(t, db)
	if err := db.AutoMigrate(
		&models.Ticket{},
		&models.TicketAttachment{},
	); err != nil {
		t.Fatalf("migrate Agent attachment download schemas: %v", err)
	}
	ticket := models.Ticket{
		OrganizationID: project.organization.ID,
		ProjectID:      project.project.ID,
		QueueID:        project.queue.ID,
		TicketNumber:   "AGENT-ATTACHMENT-DOWNLOAD",
		Title:          "Agent attachment download",
		Description:    "Agent attachment download",
		Type:           models.TicketTypeRequest,
		Priority:       models.TicketPriorityNormal,
		Status:         models.TicketStatusOpen,
		Source:         models.TicketSourceAgent,
		Version:        1,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatalf("seed Agent attachment download ticket: %v", err)
	}
	storage, err := services.NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatalf("create Agent attachment download storage: %v", err)
	}
	content := []byte("Agent attachment content")
	stored, err := storage.Put(
		context.Background(),
		"tickets/agent-download.txt",
		bytes.NewReader(content),
		1024,
	)
	if err != nil {
		t.Fatalf("store Agent attachment download content: %v", err)
	}
	originalName := `Agent "evidence"; final.txt`
	systemActor := models.SystemActor("agent-download-header-test")
	attachment := models.TicketAttachment{
		OrganizationID: project.organization.ID,
		ProjectID:      project.project.ID,
		TicketID:       ticket.ID,
		ActorType:      systemActor.Type,
		ActorID:        systemActor.ID,
		FileName:       "agent-download.txt",
		OriginalName:   originalName,
		FileSize:       stored.Size,
		MimeType:       "text/plain",
		FileType:       models.AttachmentTypeDocument,
		Extension:      ".txt",
		StoragePath:    stored.Key,
		StorageType:    storage.AttachmentStorageType(),
		Hash:           stored.SHA256,
		VirusScan:      models.VirusScanClean,
	}
	if err := db.Create(&attachment).Error; err != nil {
		t.Fatalf("seed clean Agent attachment: %v", err)
	}
	native := services.NewAgentNativeService(
		db,
		services.AgentNativeOptions{
			AttachmentStorage: storage,
		},
	)
	handler := NewAPIHandler(db, native, nil, 1024, nil)
	router := gin.New()
	router.GET("/attachments/:id/content", func(c *gin.Context) {
		ctx, contextErr := services.WithOperationContext(
			c.Request.Context(),
			services.OperationContext{
				Scope:  project.project.Scope(),
				Actor:  systemActor,
				Source: services.SourceProtocolWorker,
			},
		)
		if contextErr != nil {
			t.Fatalf("build Agent attachment context: %v", contextErr)
		}
		c.Request = c.Request.WithContext(ctx)
		handler.DownloadAttachment(c)
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/attachments/"+
			strconv.FormatUint(uint64(attachment.ID), 10)+
			"/content",
		nil,
	)
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf(
			"Agent download status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	if response.Body.String() != string(content) {
		t.Fatalf(
			"Agent download content = %q, want %q",
			response.Body.String(),
			content,
		)
	}
	for name, want := range map[string]string{
		"Cache-Control":           "private, no-store",
		"Pragma":                  "no-cache",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "default-src 'none'; sandbox",
		"Content-Type":            "text/plain",
	} {
		if got := response.Header().Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	if got := response.Header().Get("Accept-Ranges"); got != "" {
		t.Fatalf("Accept-Ranges = %q, want absent", got)
	}
	disposition, parameters, err := mime.ParseMediaType(
		response.Header().Get("Content-Disposition"),
	)
	if err != nil {
		t.Fatalf("parse Agent Content-Disposition: %v", err)
	}
	if disposition != "attachment" ||
		parameters["filename"] != originalName {
		t.Fatalf(
			"Agent Content-Disposition = %q params=%v",
			disposition,
			parameters,
		)
	}
}

func ensureAPIHandlerTestProject(
	t *testing.T,
	db *gorm.DB,
) apiHandlerTestProject {
	t.Helper()
	if err := db.AutoMigrate(
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.Queue{},
		&models.ProjectPrincipalGrant{},
	); err != nil {
		t.Fatalf("migrate API handler project fixture: %v", err)
	}
	var organization models.Organization
	if err := db.Where("slug = ?", "api-handler-test").
		FirstOrCreate(&organization, models.Organization{
			Slug:   "api-handler-test",
			Name:   "API Handler Test",
			Status: models.OrganizationStatusActive,
		}).Error; err != nil {
		t.Fatalf("seed API handler organization: %v", err)
	}
	var unit models.BusinessUnit
	if err := db.Where(
		"organization_id = ? AND key = ?",
		organization.ID,
		"TEST",
	).FirstOrCreate(&unit, models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "TEST",
		Name:           "Test",
		Status:         models.BusinessUnitStatusActive,
	}).Error; err != nil {
		t.Fatalf("seed API handler business unit: %v", err)
	}
	var project models.Project
	if err := db.Where(
		"organization_id = ? AND key = ?",
		organization.ID,
		models.ProjectKey("TEST"),
	).FirstOrCreate(&project, models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            "TEST",
		Name:           "Test",
		Status:         models.ProjectStatusActive,
	}).Error; err != nil {
		t.Fatalf("seed API handler project: %v", err)
	}
	var queue models.Queue
	if err := db.Where(
		"project_id = ? AND key = ?",
		project.ID,
		models.QueueKey("default"),
	).FirstOrCreate(&queue, models.Queue{
		ProjectID: project.ID,
		Key:       "default",
		Name:      "Default",
		Status:    models.QueueStatusActive,
		IsDefault: true,
	}).Error; err != nil {
		t.Fatalf("seed API handler queue: %v", err)
	}
	scope := project.Scope()
	if db.Migrator().HasTable(&models.Ticket{}) {
		if err := db.Model(&models.Ticket{}).
			Where("organization_id = 0 OR project_id = 0 OR queue_id = 0").
			Updates(map[string]any{
				"organization_id": scope.OrganizationID,
				"project_id":      scope.ProjectID,
				"queue_id":        queue.ID,
			}).Error; err != nil {
			t.Fatalf("scope API handler tickets: %v", err)
		}
	}
	for _, model := range []any{
		&models.TicketComment{},
		&models.TicketAttachment{},
		&models.TicketHistory{},
		&models.TicketLease{},
		&models.IdempotencyRecord{},
		&models.PolicyDecision{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	} {
		if !db.Migrator().HasTable(model) ||
			!db.Migrator().HasColumn(model, "organization_id") ||
			!db.Migrator().HasColumn(model, "project_id") {
			continue
		}
		if err := db.Model(model).
			Where("organization_id = 0 OR project_id = 0").
			Updates(map[string]any{
				"organization_id": scope.OrganizationID,
				"project_id":      scope.ProjectID,
			}).Error; err != nil {
			t.Fatalf("scope API handler fixture %T: %v", model, err)
		}
	}
	return apiHandlerTestProject{
		organization: organization,
		project:      project,
		queue:        queue,
	}
}

func grantAPIHandlerTestProject(
	t *testing.T,
	db *gorm.DB,
	project models.Project,
	principalID string,
	scopes []string,
) {
	t.Helper()
	if err := db.AutoMigrate(&models.ProjectPrincipalGrant{}); err != nil {
		t.Fatalf("migrate API handler project grant: %v", err)
	}
	encodedScopes, err := json.Marshal(scopes)
	if err != nil {
		t.Fatalf("encode API handler project scopes: %v", err)
	}
	var grant models.ProjectPrincipalGrant
	result := db.Where(
		"project_id = ? AND service_principal_id = ?",
		project.ID,
		principalID,
	).First(&grant)
	switch {
	case errors.Is(result.Error, gorm.ErrRecordNotFound):
		grant = models.ProjectPrincipalGrant{
			ProjectID:          project.ID,
			ServicePrincipalID: principalID,
			Role:               models.ProjectRoleAgent,
			Scopes:             datatypes.JSON(encodedScopes),
			IsActive:           true,
		}
		if err := db.Create(&grant).Error; err != nil {
			t.Fatalf("seed API handler project grant: %v", err)
		}
	case result.Error != nil:
		t.Fatalf("load API handler project grant: %v", result.Error)
	default:
		if err := db.Model(&grant).Updates(map[string]any{
			"scopes":    datatypes.JSON(encodedScopes),
			"is_active": true,
		}).Error; err != nil {
			t.Fatalf("update API handler project grant: %v", err)
		}
	}
}

func apiHandlerTestOperationContext(
	t *testing.T,
	db *gorm.DB,
	principalID,
	credentialID string,
) context.Context {
	t.Helper()
	project := ensureAPIHandlerTestProject(t, db)
	ctx, err := services.WithOperationContext(
		context.Background(),
		services.OperationContext{
			Scope:        project.project.Scope(),
			Actor:        models.ServicePrincipalActor(principalID),
			Source:       services.SourceProtocolAgentREST,
			CredentialID: credentialID,
		},
	)
	if err != nil {
		t.Fatalf("build API handler operation context: %v", err)
	}
	return ctx
}

func TestWriteNativeProblemLogsOnlySafeCorrelationMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var output bytes.Buffer
	originalWriter := log.Writer()
	originalFlags := log.Flags()
	originalPrefix := log.Prefix()
	log.SetOutput(&output)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(originalWriter)
		log.SetFlags(originalFlags)
		log.SetPrefix(originalPrefix)
	})

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set("request_id", "req-1\r\n[ERROR] forged\u202e")
	writeNativeProblem(
		context,
		fmt.Errorf("database failure contained token=%s password=%s", "secret-token", "secret-password"),
	)

	logged := output.String()
	for _, forbidden := range []string{
		"secret-token",
		"secret-password",
		"\r",
		"\n[ERROR]",
		"\u202e",
	} {
		if strings.Contains(logged, forbidden) {
			t.Fatalf("agent-native error log contains unsafe value %q: %q", forbidden, logged)
		}
	}
	if !strings.Contains(logged, "request_id=req-1[ERROR] forged") ||
		!strings.Contains(logged, "code=internal_error") {
		t.Fatalf("agent-native error log lost safe correlation metadata: %q", logged)
	}
}

func TestWriteNativeProblemRejectsEmptyAttachmentWithStableContract(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set("request_id", "attachment-empty")

	writeNativeProblem(context, services.ErrInvalidAttachment)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	var problem Problem
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem response: %v", err)
	}
	if problem.Code != ProblemAttachmentRejected ||
		problem.Status != http.StatusBadRequest ||
		problem.Retryable {
		t.Fatalf("unexpected empty attachment problem: %+v", problem)
	}
}

func TestInvalidTicketTagsUseStableRESTAndMCPContracts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	err := fmt.Errorf("%w: each tag must contain at most 50 characters", services.ErrInvalidTicketTags)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPatch, "/agent/tickets/1", nil)
	writeNativeProblem(context, err)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	var problem Problem
	if decodeErr := json.Unmarshal(recorder.Body.Bytes(), &problem); decodeErr != nil {
		t.Fatalf("decode problem response: %v", decodeErr)
	}
	if problem.Code != "invalid_request" || problem.Retryable {
		t.Fatalf("unexpected REST problem: %+v", problem)
	}

	mcpProblem := backendError(err)
	if mcpProblem.Code != "invalid_argument" || mcpProblem.Retryable {
		t.Fatalf("unexpected MCP problem: %+v", mcpProblem)
	}
}

func TestInvalidAgentContextUsesStableRESTAndMCPContracts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	err := fmt.Errorf(
		"%w: constraints must contain at most 20 items",
		services.ErrInvalidAgentContext,
	)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPatch, "/agent/tickets/1", nil)
	writeNativeProblem(context, err)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	var problem Problem
	if decodeErr := json.Unmarshal(recorder.Body.Bytes(), &problem); decodeErr != nil {
		t.Fatalf("decode problem response: %v", decodeErr)
	}
	if problem.Code != ProblemInvalidRequest || problem.Retryable {
		t.Fatalf("unexpected REST problem: %+v", problem)
	}

	mcpProblem := backendError(err)
	if mcpProblem.Code != "invalid_argument" ||
		mcpProblem.Message != "request is invalid" ||
		mcpProblem.Retryable {
		t.Fatalf("unexpected MCP problem: %+v", mcpProblem)
	}
}

func TestInvalidTicketCategoryUsesStableRESTAndMCPContracts(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	err := fmt.Errorf(
		"%w: category is outside the authorized project",
		services.ErrTicketCategoryScope,
	)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPatch,
		"/agent/tickets/1",
		nil,
	)
	writeNativeProblem(context, err)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	var problem Problem
	if decodeErr := json.Unmarshal(
		recorder.Body.Bytes(),
		&problem,
	); decodeErr != nil {
		t.Fatalf("decode category problem response: %v", decodeErr)
	}
	if problem.Code != ProblemInvalidRequest || problem.Retryable {
		t.Fatalf("unexpected category REST problem: %+v", problem)
	}

	mcpProblem := backendError(err)
	if mcpProblem.Code != "invalid_argument" ||
		mcpProblem.Message != "request is invalid" ||
		mcpProblem.Retryable {
		t.Fatalf("unexpected category MCP problem: %+v", mcpProblem)
	}
}

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
		PlatformRole: models.PlatformRoleMember, Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	native := services.NewAgentNativeService(db)
	principal, err := native.CreateServicePrincipal(context.Background(), services.CreateServicePrincipalInput{
		Name:   "visibility-agent",
		Scopes: []string{models.ScopeTicketsRead},
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
			CreatedByID: &user.ID, Version: 1,
		},
		{
			TicketNumber: "HIDDEN-2", Title: "hidden", Description: "hidden",
			Type: models.TicketTypeRequest, Priority: models.TicketPriorityNormal,
			Status: models.TicketStatusOpen, Source: models.TicketSourceAgent,
			CreatedByID: &user.ID, Version: 1,
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
	operationContext := apiHandlerTestOperationContext(
		t,
		db,
		principal.ID,
		credential.Credential.ID,
	)
	router := gin.New()
	router.GET("/tickets", func(c *gin.Context) {
		c.Set("agent_principal_id", principal.ID)
		c.Set("agent_credential_id", credential.Credential.ID)
		c.Request = c.Request.WithContext(operationContext)
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
		PlatformRole: models.PlatformRoleMember, Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	native := services.NewAgentNativeService(db)
	principal, err := native.CreateServicePrincipal(context.Background(), services.CreateServicePrincipalInput{
		Name:   "bounded-ticket-agent",
		Scopes: []string{models.ScopeTicketsRead},
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
			CreatedByID:  &user.ID,
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
	operationContext := apiHandlerTestOperationContext(
		t,
		db,
		principal.ID,
		credential.Credential.ID,
	)
	router := gin.New()
	router.GET("/tickets", func(c *gin.Context) {
		c.Set("agent_principal_id", principal.ID)
		c.Set("agent_credential_id", credential.Credential.ID)
		c.Request = c.Request.WithContext(operationContext)
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
	operationContext := apiHandlerTestOperationContext(
		t,
		db,
		principal.ID,
		credential.Credential.ID,
	)
	router := gin.New()
	router.GET("/events", func(c *gin.Context) {
		c.Set("agent_principal_id", principal.ID)
		c.Set("agent_credential_id", credential.Credential.ID)
		c.Request = c.Request.WithContext(operationContext)
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
			c.Request = c.Request.WithContext(apiHandlerTestOperationContext(
				t,
				db,
				principalID,
				credentialID,
			))
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

func TestDecodeOrdinaryTicketPatchRejectsCommandAndControlFields(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantKeys  []string
		wantError string
	}{
		{
			name:     "ordinary update",
			body:     `{"title":"updated","priority":"high","category_id":null}`,
			wantKeys: []string{"title", "priority", "category_id"},
		},
		{
			name:      "status requires transition command",
			body:      `{"status":"in_progress"}`,
			wantError: "explicit command",
		},
		{
			name:      "escalation requires escalation command",
			body:      `{"is_escalated":true}`,
			wantError: "explicit command",
		},
		{
			name:      "source is server controlled",
			body:      `{"source":"agent"}`,
			wantError: "explicit command",
		},
		{
			name:      "trust is server controlled",
			body:      `{"trust_level":"trusted"}`,
			wantError: "explicit command",
		},
		{
			name:      "SLA state is server controlled",
			body:      `{"sla_breached":true}`,
			wantError: "explicit command",
		},
		{
			name:      "assignment projection is forbidden",
			body:      `{"assigned_to_actor_type":"human"}`,
			wantError: "explicit command",
		},
		{
			name:      "unknown field is rejected",
			body:      `{"future_field":true}`,
			wantError: "explicit command",
		},
		{
			name:      "invalid enum is rejected",
			body:      `{"priority":"impossible"}`,
			wantError: "supported ticket priority",
		},
		{
			name:      "empty patch is rejected",
			body:      `{}`,
			wantError: "non-empty",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changes, err := decodeOrdinaryTicketPatch([]byte(test.body))
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error = %v, want containing %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("decode ordinary ticket patch: %v", err)
			}
			if len(changes) != len(test.wantKeys) {
				t.Fatalf("changes = %#v, want keys %v", changes, test.wantKeys)
			}
			for _, key := range test.wantKeys {
				if _, exists := changes[key]; !exists {
					t.Errorf("ordinary update omitted %s: %#v", key, changes)
				}
			}
		})
	}
}

func TestDecodeTicketCommandsUseClosedAuditableBodies(t *testing.T) {
	t.Run("assign actor", func(t *testing.T) {
		request, err := decodeTicketAssignmentCommand([]byte(
			`{"assignee":{"type":"human","id":"42"},"reason":"队列技能匹配"}`,
		))
		if err != nil {
			t.Fatal(err)
		}
		if request.Assignee == nil ||
			request.Assignee.Type != models.ActorTypeHuman ||
			request.Assignee.ID != "42" ||
			request.Reason != "队列技能匹配" {
			t.Fatalf("unexpected assignment command: %#v", request)
		}
	})
	t.Run("release assignment", func(t *testing.T) {
		request, err := decodeTicketAssignmentCommand([]byte(
			`{"assignee":null,"reason":"当前处理者结束轮值"}`,
		))
		if err != nil {
			t.Fatal(err)
		}
		if request.Assignee != nil || request.Reason == "" {
			t.Fatalf("unexpected release command: %#v", request)
		}
	})
	t.Run("assign requires reason", func(t *testing.T) {
		if _, err := decodeTicketAssignmentCommand([]byte(
			`{"assignee":{"type":"human","id":"42"}}`,
		)); err == nil || !strings.Contains(err.Error(), "reason") {
			t.Fatalf("error = %v, want required reason", err)
		}
	})
	t.Run("system cannot be assigned", func(t *testing.T) {
		if _, err := decodeTicketAssignmentCommand([]byte(
			`{"assignee":{"type":"system","id":"scheduler"},"reason":"invalid"}`,
		)); err == nil || !strings.Contains(err.Error(), "human or service_principal") {
			t.Fatalf("error = %v, want public ActorRef rejection", err)
		}
	})
	t.Run("assignment projection cannot be mixed", func(t *testing.T) {
		if _, err := decodeTicketAssignmentCommand([]byte(
			`{"assignee":null,"reason":"release","assigned_to_id":42}`,
		)); err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("error = %v, want unknown field rejection", err)
		}
	})
	t.Run("transition", func(t *testing.T) {
		request, err := decodeTicketTransitionCommand([]byte(
			`{"status":"in_progress","reason":"已确认告警"}`,
		))
		if err != nil {
			t.Fatal(err)
		}
		if request.Status != models.TicketStatusInProgress ||
			request.Reason != "已确认告警" {
			t.Fatalf("unexpected transition command: %#v", request)
		}
	})
	t.Run("transition rejects ordinary update", func(t *testing.T) {
		if _, err := decodeTicketTransitionCommand([]byte(
			`{"status":"in_progress","reason":"start","priority":"high"}`,
		)); err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("error = %v, want unknown field rejection", err)
		}
	})
	t.Run("escalation", func(t *testing.T) {
		request, err := decodeTicketEscalationCommand([]byte(
			`{"reason":"超过升级阈值","priority":"urgent","assignee":{"type":"service_principal","id":"sp-1"}}`,
		))
		if err != nil {
			t.Fatal(err)
		}
		if request.Priority == nil ||
			*request.Priority != models.TicketPriorityUrgent ||
			request.Assignee == nil ||
			request.Assignee.Type != models.ActorTypeServicePrincipal {
			t.Fatalf("unexpected escalation command: %#v", request)
		}
	})
	t.Run("escalation rejects null assignee", func(t *testing.T) {
		if _, err := decodeTicketEscalationCommand([]byte(
			`{"reason":"超过升级阈值","assignee":null}`,
		)); err == nil || !strings.Contains(err.Error(), "cannot be null") {
			t.Fatalf("error = %v, want null rejection", err)
		}
	})
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
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	native := services.NewAgentNativeService(db)
	principal, err := native.CreateServicePrincipal(context.Background(), services.CreateServicePrincipalInput{
		Name:   "lease-route-agent",
		Scopes: []string{models.ScopeTasksManage},
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
		CreatedByID:  &user.ID,
		Version:      1,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	projectFixture := ensureAPIHandlerTestProject(t, db)
	grantAPIHandlerTestProject(
		t,
		db,
		projectFixture.project,
		principal.ID,
		[]string{models.ScopeTasksManage},
	)

	tokens := agentauth.NewManager("lease-route-secret", "https://issuer.example.test", "https://api.example.test", time.Hour)
	accessToken, _, err := tokens.Issue(&agentauth.Principal{
		ID:           principal.ID,
		CredentialID: credential.Credential.ID,
		ClientID:     "lease-route-client",
		Name:         principal.Name,
		Scopes:       []string{models.ScopeTasksManage},
		Active:       true,
	}, "TEST", []string{models.ScopeTasksManage})
	if err != nil {
		t.Fatal(err)
	}
	verifiedAccess, err := tokens.Verify(accessToken)
	if err != nil || verifiedAccess.ProjectKey != "TEST" {
		t.Fatalf("issued lease-route token project = %#v, err=%v", verifiedAccess, err)
	}
	handler := NewAPIHandler(db, native, tokens, 1<<20, nil)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/v2/projects/:projectKey"))

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
	doDeleteRequest := func(
		path string,
		idempotencyKey string,
	) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodDelete, path, nil)
		request.Header.Set("Authorization", "Bearer "+accessToken)
		request.Header.Set("Idempotency-Key", idempotencyKey)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}

	claimBody := `{"ttl_seconds":60}`
	claimPath := fmt.Sprintf("/api/v2/projects/TEST/tickets/%d/claim", ticket.ID)
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
	if claimRecord.OrganizationID != projectFixture.organization.ID ||
		claimRecord.ProjectID != projectFixture.project.ID {
		t.Fatalf("claim idempotency record lost project binding: %+v", claimRecord)
	}

	heartbeatBody := `{"ttl_seconds":90}`
	heartbeatPath := "/api/v2/projects/TEST/leases/" + claimEnvelope.Data.LeaseID + "/heartbeat"
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
	if heartbeatRecord.OrganizationID != projectFixture.organization.ID ||
		heartbeatRecord.ProjectID != projectFixture.project.ID {
		t.Fatalf("heartbeat idempotency record lost project binding: %+v", heartbeatRecord)
	}
	t.Run("heartbeat replay", func(t *testing.T) {
		assertLeaseReplayResourceIDValidation(
			t,
			db,
			&heartbeatRecord,
			heartbeatResponse,
			func() *httptest.ResponseRecorder {
				return doRequest(
					heartbeatPath,
					"heartbeat-route-key",
					heartbeatBody,
				)
			},
		)
	})

	releasePath := "/api/v2/projects/TEST/leases/" +
		claimEnvelope.Data.LeaseID
	releaseResponse := doDeleteRequest(
		releasePath,
		"release-route-key",
	)
	if releaseResponse.Code != http.StatusOK {
		t.Fatalf(
			"release status=%d body=%s",
			releaseResponse.Code,
			releaseResponse.Body.String(),
		)
	}
	var releaseRecord models.IdempotencyRecord
	if err := db.Where(
		"actor_id = ? AND operation = ? AND key = ?",
		principal.ID,
		"ticket.lease.release",
		"release-route-key",
	).First(&releaseRecord).Error; err != nil {
		t.Fatal(err)
	}
	if releaseRecord.OrganizationID != projectFixture.organization.ID ||
		releaseRecord.ProjectID != projectFixture.project.ID ||
		releaseRecord.ResourceID != fmt.Sprint(ticket.ID) {
		t.Fatalf(
			"release idempotency record lost project or ticket binding: %+v",
			releaseRecord,
		)
	}
	t.Run("release replay", func(t *testing.T) {
		assertLeaseReplayResourceIDValidation(
			t,
			db,
			&releaseRecord,
			releaseResponse,
			func() *httptest.ResponseRecorder {
				return doDeleteRequest(
					releasePath,
					"release-route-key",
				)
			},
		)
	})

	mismatchPath := fmt.Sprintf(
		"/api/v2/projects/OTHER/tickets/%d/claim",
		ticket.ID,
	)
	mismatchResponse := doRequest(
		mismatchPath,
		"claim-project-mismatch",
		claimBody,
	)
	if mismatchResponse.Code != http.StatusForbidden ||
		!strings.Contains(
			mismatchResponse.Body.String(),
			`"code":"project_scope_mismatch"`,
		) {
		t.Fatalf(
			"token/path project mismatch status=%d body=%s",
			mismatchResponse.Code,
			mismatchResponse.Body.String(),
		)
	}
	var mismatchRecords int64
	if err := db.Model(&models.IdempotencyRecord{}).
		Where("key = ?", "claim-project-mismatch").
		Count(&mismatchRecords).Error; err != nil {
		t.Fatal(err)
	}
	if mismatchRecords != 0 {
		t.Fatalf("project mismatch reached domain work: records=%d", mismatchRecords)
	}
}

func assertLeaseReplayResourceIDValidation(
	t *testing.T,
	db *gorm.DB,
	record *models.IdempotencyRecord,
	initial *httptest.ResponseRecorder,
	replay func() *httptest.ResponseRecorder,
) {
	t.Helper()
	if db == nil || record == nil || record.ID == "" ||
		initial == nil || replay == nil {
		t.Fatal("complete lease replay fixture is required")
	}
	validReplay := replay()
	if validReplay.Code != initial.Code ||
		validReplay.Body.String() != initial.Body.String() {
		t.Fatalf(
			"valid replay differs: initial status=%d body=%s; replay status=%d body=%s",
			initial.Code,
			initial.Body.String(),
			validReplay.Code,
			validReplay.Body.String(),
		)
	}

	originalResourceID := record.ResourceID
	for _, test := range []struct {
		name       string
		resourceID string
	}{
		{name: "zero", resourceID: "0"},
		{name: "parse error", resourceID: "not-a-ticket-id"},
		{
			name:       "native uint overflow",
			resourceID: leaseReplayNativeUintOverflow(),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := db.Model(&models.IdempotencyRecord{}).
				Where("id = ?", record.ID).
				Update("resource_id", test.resourceID).Error; err != nil {
				t.Fatal(err)
			}
			response := replay()
			if response.Code != http.StatusConflict {
				t.Fatalf(
					"status=%d, want 409; body=%s",
					response.Code,
					response.Body.String(),
				)
			}
			var problem Problem
			if err := json.Unmarshal(
				response.Body.Bytes(),
				&problem,
			); err != nil {
				t.Fatal(err)
			}
			if problem.Code != ProblemIdempotencyConflict {
				t.Fatalf(
					"problem=%+v, want code %q",
					problem,
					ProblemIdempotencyConflict,
				)
			}
		})
	}
	if err := db.Model(&models.IdempotencyRecord{}).
		Where("id = ?", record.ID).
		Update("resource_id", originalResourceID).Error; err != nil {
		t.Fatal(err)
	}
}

func leaseReplayNativeUintOverflow() string {
	if strconv.IntSize == 32 {
		return "4294967296"
	}
	return "18446744073709551616"
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
		PlatformRole: models.PlatformRoleMember, Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		TicketNumber: "REPLAY-1", Title: "ticket", Description: "description",
		Type: models.TicketTypeRequest, Priority: models.TicketPriorityNormal,
		Status: models.TicketStatusOpen, Source: models.TicketSourceAgent,
		CreatedByID: &user.ID, Version: 2,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	comment := models.TicketComment{
		TicketID: ticket.ID, UserID: &user.ID, ActorType: models.ActorTypeHuman,
		ActorID: models.HumanActor(user.ID).ID, Content: "result", ContentType: "text",
		Type: models.CommentTypeInternal,
	}
	if err := db.Create(&comment).Error; err != nil {
		t.Fatal(err)
	}
	projectFixture := ensureAPIHandlerTestProject(t, db)
	receipt := Receipt{
		OperationID: "operation", ResourceID: fmt.Sprint(comment.ID),
		ResourceVersion: 2, EventID: "event", ChangedFields: []string{"comments"},
	}
	responseBody, _ := json.Marshal(receipt)
	record := &models.IdempotencyRecord{
		OrganizationID: projectFixture.organization.ID,
		ProjectID:      projectFixture.project.ID,
		ResourceID:     fmt.Sprint(comment.ID),
		ResponseCode:   http.StatusCreated,
		ResponseBody:   datatypes.JSON(responseBody),
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	context.Request = request.WithContext(apiHandlerTestOperationContext(
		t,
		db,
		"replay-principal",
		"replay-credential",
	))
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
		OrganizationID:   1,
		ProjectID:        1,
		ResourceID:       "7",
		ResponseCode:     http.StatusCreated,
		ResponseBody:     datatypes.JSON(receiptBody),
		ResourceSnapshot: datatypes.JSON(snapshotBody),
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	operationContext, err := services.WithOperationContext(
		request.Context(),
		services.OperationContext{
			Scope: models.ProjectScope{
				OrganizationID: 1,
				ProjectID:      1,
			},
			Actor:        models.ServicePrincipalActor("replay-principal"),
			Source:       services.SourceProtocolAgentREST,
			CredentialID: "replay-credential",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	context.Request = request.WithContext(operationContext)
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
