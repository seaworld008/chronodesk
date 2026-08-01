package agentplatform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/agentauth"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAgentKnowledgeDraftValidation(t *testing.T) {
	validArticle := agentKnowledgeArticleDraftRequest{
		Key:      "agent.runbook",
		Title:    "Agent runbook",
		Markdown: "# Runbook\n\nUntrusted content.",
	}
	if err := validateAgentKnowledgeArticleDraft(validArticle); err != nil {
		t.Fatalf("valid article draft rejected: %v", err)
	}
	validVersion := agentKnowledgeVersionDraftRequest{
		Title:    "Agent runbook v2",
		Markdown: "# Runbook\n\nUpdated.",
	}
	if err := validateAgentKnowledgeVersionDraft(validVersion); err != nil {
		t.Fatalf("valid version draft rejected: %v", err)
	}

	tooManySources := make(
		[]uint,
		services.MaxAuthoredSourceLinks+1,
	)
	for index := range tooManySources {
		tooManySources[index] = uint(index + 1)
	}
	tests := []struct {
		name    string
		request agentKnowledgeArticleDraftRequest
	}{
		{
			name: "non canonical key",
			request: agentKnowledgeArticleDraftRequest{
				Key:      "Agent Runbook",
				Title:    "Valid",
				Markdown: "Valid",
			},
		},
		{
			name: "blank title",
			request: agentKnowledgeArticleDraftRequest{
				Key:      "valid",
				Title:    " \t",
				Markdown: "Valid",
			},
		},
		{
			name: "invalid UTF-8 markdown",
			request: agentKnowledgeArticleDraftRequest{
				Key:      "valid",
				Title:    "Valid",
				Markdown: string([]byte{0xff}),
			},
		},
		{
			name: "attachment without ticket",
			request: agentKnowledgeArticleDraftRequest{
				Key:                 "valid",
				Title:               "Valid",
				Markdown:            "Valid",
				SourceAttachmentIDs: []uint{1},
			},
		},
		{
			name: "duplicate attachment",
			request: agentKnowledgeArticleDraftRequest{
				Key:                 "valid",
				Title:               "Valid",
				Markdown:            "Valid",
				SourceTicketID:      1,
				SourceAttachmentIDs: []uint{2, 2},
			},
		},
		{
			name: "too many attachments",
			request: agentKnowledgeArticleDraftRequest{
				Key:                 "valid",
				Title:               "Valid",
				Markdown:            "Valid",
				SourceTicketID:      1,
				SourceAttachmentIDs: tooManySources,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateAgentKnowledgeArticleDraft(
				test.request,
			); err == nil {
				t.Fatal("invalid article draft was accepted")
			}
		})
	}
}

func TestAgentCapabilitiesDiscoverKnowledgeScopes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET(
		"/capabilities",
		(&APIHandler{}).Capabilities,
	)
	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodGet,
			"/capabilities",
			nil,
		),
	)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"capabilities status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	var envelope struct {
		Data struct {
			Scopes []string `json:"scopes_supported"`
		} `json:"data"`
	}
	if err := json.Unmarshal(
		response.Body.Bytes(),
		&envelope,
	); err != nil {
		t.Fatalf("decode Agent capabilities: %v", err)
	}
	discovered := make(
		map[string]bool,
		len(envelope.Data.Scopes),
	)
	for _, scope := range envelope.Data.Scopes {
		discovered[scope] = true
	}
	for _, scope := range []string{
		models.ScopeKnowledgeRead,
		models.ScopeKnowledgeWrite,
	} {
		if !discovered[scope] {
			t.Fatalf(
				"capabilities omitted %q: %v",
				scope,
				envelope.Data.Scopes,
			)
		}
	}
}

func TestAgentKnowledgeRoutesEnforceScopesAndExposeNoPublication(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	tokens := agentauth.NewManager(
		"agent-knowledge-route-secret",
		"https://issuer.example.test",
		"https://api.example.test",
		time.Hour,
	)
	issue := func(scopes ...string) string {
		t.Helper()
		token, _, err := tokens.Issue(
			&agentauth.Principal{
				ID:           "knowledge-route-principal",
				CredentialID: "knowledge-route-credential",
				ClientID:     "knowledge-route-client",
				Name:         "Knowledge route Agent",
				Scopes:       append([]string(nil), scopes...),
				Active:       true,
			},
			"TEST",
			scopes,
		)
		if err != nil {
			t.Fatalf("issue Agent knowledge token: %v", err)
		}
		return token
	}
	readToken := issue(models.ScopeKnowledgeRead)
	writeToken := issue(models.ScopeKnowledgeWrite)

	handler := NewAPIHandler(nil, nil, tokens, 0, nil)
	router := gin.New()
	handler.RegisterRoutes(
		router.Group("/api/v2/projects/:projectKey"),
	)

	tests := []struct {
		name   string
		method string
		path   string
		token  string
		status int
		code   string
	}{
		{
			name:   "write route rejects read-only token",
			method: http.MethodPost,
			path:   "/api/v2/projects/TEST/knowledge/articles",
			token:  readToken,
			status: http.StatusForbidden,
			code:   ProblemInsufficientScope,
		},
		{
			name:   "read route rejects write-only token",
			method: http.MethodGet,
			path:   "/api/v2/projects/TEST/knowledge/articles",
			token:  writeToken,
			status: http.StatusForbidden,
			code:   ProblemInsufficientScope,
		},
		{
			name:   "project-explicit token cannot cross projects",
			method: http.MethodGet,
			path:   "/api/v2/projects/OTHER/knowledge/articles",
			token:  readToken,
			status: http.StatusForbidden,
			code:   "project_scope_mismatch",
		},
		{
			name:   "publication route does not exist",
			method: http.MethodPost,
			path: "/api/v2/projects/TEST/knowledge/articles/" +
				"018f0f77-ec00-7000-8000-000000000001/publication",
			token:  writeToken,
			status: http.StatusNotFound,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(
				test.method,
				test.path,
				strings.NewReader(`{}`),
			)
			request.Header.Set(
				"Authorization",
				"Bearer "+test.token,
			)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf(
					"status = %d, want %d, body = %s",
					response.Code,
					test.status,
					response.Body.String(),
				)
			}
			if test.code == "" {
				return
			}
			var problem struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(
				response.Body.Bytes(),
				&problem,
			); err != nil {
				t.Fatalf("decode Agent problem: %v", err)
			}
			if problem.Code != test.code {
				t.Fatalf(
					"problem code = %q, want %q",
					problem.Code,
					test.code,
				)
			}
		})
	}
}

type agentKnowledgeRouteFixture struct {
	db         *gorm.DB
	native     *services.AgentNativeService
	knowledge  *services.KnowledgeService
	handler    *APIHandler
	router     *gin.Engine
	principal  *models.ServicePrincipal
	credential *models.AgentCredential
	token      string
	project    apiHandlerTestProject
}

type agentKnowledgeSearchIndex struct {
	request services.HybridSearchRequest
}

func (index *agentKnowledgeSearchIndex) Search(
	_ context.Context,
	request services.HybridSearchRequest,
) ([]services.HybridSearchHit, error) {
	index.request = request
	return []services.HybridSearchHit{}, nil
}

func (*agentKnowledgeSearchIndex) ReplaceProject(
	context.Context,
	services.HybridIndexReplacement,
) error {
	return nil
}

type agentKnowledgeModelProvider struct{}

func (agentKnowledgeModelProvider) Descriptor() services.ModelProviderDescriptor {
	return services.ModelProviderDescriptor{
		Key:        "agent-search-test",
		IsExternal: false,
	}
}

func (agentKnowledgeModelProvider) Generate(
	context.Context,
	services.ModelGenerateRequest,
) (services.ModelGenerateResponse, error) {
	return services.ModelGenerateResponse{}, nil
}

func (agentKnowledgeModelProvider) Embed(
	_ context.Context,
	request services.ModelEmbedRequest,
) (services.ModelEmbedResponse, error) {
	if len(request.Inputs) != 1 {
		return services.ModelEmbedResponse{}, fmt.Errorf(
			"expected one search input",
		)
	}
	return services.ModelEmbedResponse{
		Embeddings: [][]float32{{0.25, 0.75}},
	}, nil
}

func (agentKnowledgeModelProvider) Rerank(
	context.Context,
	services.ModelRerankRequest,
) (services.ModelRerankResponse, error) {
	return services.ModelRerankResponse{}, fmt.Errorf(
		"rerank must not run for an empty search index",
	)
}

func newAgentKnowledgeRouteFixture(
	t *testing.T,
	scopes []string,
) agentKnowledgeRouteFixture {
	t.Helper()
	dsn := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared",
		strings.ReplaceAll(t.Name(), "/", "_"),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open Agent knowledge database: %v", err)
	}
	project := ensureAPIHandlerTestProject(t, db)
	if err := db.AutoMigrate(
		&models.ServicePrincipal{},
		&models.AgentCredential{},
		&models.AgentPolicy{},
		&models.PolicyDecision{},
		&models.IdempotencyRecord{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.KnowledgeArticle{},
		&models.KnowledgeArticleVersion{},
		&models.KnowledgeObjectWriteIntent{},
		&models.KnowledgeArticleACL{},
		&models.KnowledgeSourceLink{},
		&models.KnowledgeIngestionTask{},
		&models.KnowledgeChunk{},
		&models.KnowledgeIndexState{},
		&models.Ticket{},
		&models.TicketAttachment{},
		&models.User{},
		&models.ProjectMembership{},
	); err != nil {
		t.Fatalf("migrate Agent knowledge schemas: %v", err)
	}
	human := models.User{
		ID:           42,
		Username:     "agent-knowledge-reviewer",
		Email:        "agent-knowledge-reviewer@example.test",
		PasswordHash: "test-only",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.FirstOrCreate(&human, human.ID).Error; err != nil {
		t.Fatalf("seed Agent knowledge reviewer: %v", err)
	}
	membership := models.ProjectMembership{
		ProjectID: project.project.ID,
		UserID:    human.ID,
		Role:      models.ProjectRoleManager,
		IsActive:  true,
		Version:   1,
	}
	if err := db.Where(
		"project_id = ? AND user_id = ?",
		membership.ProjectID,
		membership.UserID,
	).FirstOrCreate(&membership).Error; err != nil {
		t.Fatalf("seed Agent knowledge reviewer membership: %v", err)
	}
	native := services.NewAgentNativeService(db)
	principal, err := native.CreateServicePrincipal(
		context.Background(),
		services.CreateServicePrincipalInput{
			Name:   "Agent knowledge fixture",
			Scopes: append([]string(nil), scopes...),
		},
	)
	if err != nil {
		t.Fatalf("create Agent knowledge principal: %v", err)
	}
	issued, err := native.IssueCredential(
		context.Background(),
		principal.ID,
		"Agent knowledge fixture",
		time.Hour,
	)
	if err != nil {
		t.Fatalf("issue Agent knowledge credential: %v", err)
	}
	grantAPIHandlerTestProject(
		t,
		db,
		project.project,
		principal.ID,
		scopes,
	)
	storage, err := services.NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatalf("create Agent knowledge storage: %v", err)
	}
	knowledge, err := services.NewKnowledgeService(
		db,
		services.KnowledgeServiceDependencies{
			Events:               native,
			AttachmentStorage:    storage,
			StorageBucket:        "agent-knowledge-test",
			IdempotencyCompleter: native,
		},
	)
	if err != nil {
		t.Fatalf("create Agent knowledge service: %v", err)
	}
	tokens := agentauth.NewManager(
		"agent-knowledge-fixture-secret",
		"https://issuer.example.test",
		"https://api.example.test",
		time.Hour,
	)
	accessToken, _, err := tokens.Issue(
		&agentauth.Principal{
			ID:           principal.ID,
			CredentialID: issued.Credential.ID,
			ClientID:     "agent-knowledge-fixture",
			Name:         principal.Name,
			Scopes:       append([]string(nil), scopes...),
			Active:       true,
		},
		string(project.project.Key),
		scopes,
	)
	if err != nil {
		t.Fatalf("issue Agent knowledge access token: %v", err)
	}
	handler := NewAPIHandler(db, native, tokens, 1<<20, nil)
	handler.SetKnowledgeService(knowledge)
	router := gin.New()
	handler.RegisterRoutes(
		router.Group("/api/v2/projects/:projectKey"),
	)
	return agentKnowledgeRouteFixture{
		db:         db,
		native:     native,
		knowledge:  knowledge,
		handler:    handler,
		router:     router,
		principal:  principal,
		credential: issued.Credential,
		token:      accessToken,
		project:    project,
	}
}

func (fixture agentKnowledgeRouteFixture) publishVersion(
	t *testing.T,
	versionID string,
) {
	t.Helper()
	ctx, err := services.WithOperationContext(
		context.Background(),
		services.OperationContext{
			Scope:  fixture.project.project.Scope(),
			Actor:  models.HumanActor(42),
			Source: services.SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatalf("build knowledge publication context: %v", err)
	}
	if _, err := fixture.knowledge.PublishVersion(ctx, versionID); err != nil {
		t.Fatalf("publish knowledge version: %v", err)
	}
}

func (fixture agentKnowledgeRouteFixture) seedPublishedKnowledgeSource(
	t *testing.T,
) (
	models.KnowledgeArticle,
	models.KnowledgeArticleVersion,
	models.Ticket,
	models.TicketAttachment,
) {
	t.Helper()
	creatorID := uint(42)
	ticket := models.Ticket{
		OrganizationID:       fixture.project.project.OrganizationID,
		ProjectID:            fixture.project.project.ID,
		QueueID:              fixture.project.project.ID,
		RequestTypeVersionID: "knowledge-source-request-type",
		WorkflowVersionID:    "knowledge-source-workflow",
		TicketNumber:         "TEST-KB-1",
		Title:                "Private source ticket",
		Description:          "untrusted source",
		Type:                 models.TicketTypeRequest,
		Priority:             models.TicketPriorityNormal,
		Status:               models.TicketStatusOpen,
		Source:               models.TicketSourceWeb,
		Version:              1,
		CreatedByID:          &creatorID,
		CreatedByActorType:   models.ActorTypeHuman,
		CreatedByActorID:     "42",
	}
	if err := fixture.db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	attachment := models.TicketAttachment{
		TicketID:     ticket.ID,
		ActorType:    models.ActorTypeHuman,
		ActorID:      "42",
		FileName:     "private-source.txt",
		OriginalName: "private-source.txt",
		FileSize:     7,
		MimeType:     "text/plain",
		StoragePath:  "private/source.txt",
		Hash:         strings.Repeat("a", 64),
		VirusScan:    models.VirusScanClean,
		IsPublic:     false,
	}
	if err := fixture.db.Create(&attachment).Error; err != nil {
		t.Fatal(err)
	}
	humanContext, err := services.WithOperationContext(
		context.Background(),
		services.OperationContext{
			Scope:  fixture.project.project.Scope(),
			Actor:  models.HumanActor(42),
			Source: services.SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.knowledge.CreateAuthoredArticle(
		humanContext,
		services.CreateAuthoredArticleInput{
			Key:                 "source.authorization",
			Title:               "Source authorization",
			Markdown:            "# Safe article\n",
			GrantProjectAccess:  true,
			SourceTicketID:      ticket.ID,
			SourceAttachmentIDs: []uint{attachment.ID},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.knowledge.PublishVersion(
		humanContext,
		result.Version.ID,
	); err != nil {
		t.Fatal(err)
	}
	return result.Article, result.Version, ticket, attachment
}

func (fixture agentKnowledgeRouteFixture) request(
	t *testing.T,
	method string,
	path string,
	body string,
	idempotencyKey string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(
		method,
		path,
		strings.NewReader(body),
	)
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response := httptest.NewRecorder()
	fixture.router.ServeHTTP(response, request)
	return response
}

func TestAgentKnowledgeWriteOnlyDraftIsAtomicallyIdempotent(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	fixture := newAgentKnowledgeRouteFixture(
		t,
		[]string{models.ScopeKnowledgeWrite},
	)
	path := "/api/v2/projects/TEST/knowledge/articles"
	body := `{"key":"agent.runbook","title":"Agent runbook","markdown":"# Runbook\n\nUse safely."}`
	first := fixture.request(
		t,
		http.MethodPost,
		path,
		body,
		"agent-knowledge-create-1",
	)
	if first.Code != http.StatusCreated {
		t.Fatalf(
			"first create status = %d, body = %s",
			first.Code,
			first.Body.String(),
		)
	}
	var firstEnvelope struct {
		Data    agentKnowledgeDocumentResponse `json:"data"`
		Receipt Receipt                        `json:"receipt"`
	}
	if err := json.Unmarshal(
		first.Body.Bytes(),
		&firstEnvelope,
	); err != nil {
		t.Fatalf("decode first knowledge draft: %v", err)
	}
	if firstEnvelope.Data.Article.Key != "agent.runbook" ||
		firstEnvelope.Data.Version.Status !=
			models.KnowledgeVersionDraft ||
		firstEnvelope.Data.Markdown !=
			"# Runbook\n\nUse safely." ||
		firstEnvelope.Receipt.ResourceID !=
			firstEnvelope.Data.Article.ID {
		t.Fatalf(
			"unexpected first knowledge draft: %+v",
			firstEnvelope,
		)
	}
	for _, forbidden := range []string{
		"object_provider",
		"object_bucket",
		"object_key",
		"scan_detail",
	} {
		if strings.Contains(first.Body.String(), forbidden) {
			t.Fatalf(
				"Agent response leaked %q: %s",
				forbidden,
				first.Body.String(),
			)
		}
	}

	replayed := fixture.request(
		t,
		http.MethodPost,
		path,
		body,
		"agent-knowledge-create-1",
	)
	if replayed.Code != http.StatusCreated {
		t.Fatalf(
			"replay status = %d, body = %s",
			replayed.Code,
			replayed.Body.String(),
		)
	}
	var replayEnvelope struct {
		Data    agentKnowledgeDocumentResponse `json:"data"`
		Receipt Receipt                        `json:"receipt"`
	}
	if err := json.Unmarshal(
		replayed.Body.Bytes(),
		&replayEnvelope,
	); err != nil {
		t.Fatalf("decode replayed knowledge draft: %v", err)
	}
	if replayEnvelope.Data.Article.ID !=
		firstEnvelope.Data.Article.ID ||
		replayEnvelope.Data.Version.ID !=
			firstEnvelope.Data.Version.ID ||
		replayEnvelope.Receipt.OperationID !=
			firstEnvelope.Receipt.OperationID ||
		replayEnvelope.Receipt.EventID !=
			firstEnvelope.Receipt.EventID {
		t.Fatalf(
			"replay was not stable: first=%+v replay=%+v",
			firstEnvelope,
			replayEnvelope,
		)
	}

	conflict := fixture.request(
		t,
		http.MethodPost,
		path,
		`{"key":"agent.runbook.changed","title":"Changed","markdown":"Changed"}`,
		"agent-knowledge-create-1",
	)
	if conflict.Code != http.StatusConflict {
		t.Fatalf(
			"changed replay status = %d, body = %s",
			conflict.Code,
			conflict.Body.String(),
		)
	}
	versionPath := path + "/" +
		firstEnvelope.Data.Article.ID +
		"/drafts"
	versionBody := `{"title":"Agent runbook v2","markdown":"# Runbook\n\nUpdated safely."}`
	versionFirst := fixture.request(
		t,
		http.MethodPost,
		versionPath,
		versionBody,
		"agent-knowledge-version-2",
	)
	if versionFirst.Code != http.StatusCreated {
		t.Fatalf(
			"version create status = %d, body = %s",
			versionFirst.Code,
			versionFirst.Body.String(),
		)
	}
	var versionFirstEnvelope struct {
		Data    agentKnowledgeDocumentResponse `json:"data"`
		Receipt Receipt                        `json:"receipt"`
	}
	if err := json.Unmarshal(
		versionFirst.Body.Bytes(),
		&versionFirstEnvelope,
	); err != nil {
		t.Fatalf("decode version draft: %v", err)
	}
	if versionFirstEnvelope.Data.Article.ID !=
		firstEnvelope.Data.Article.ID ||
		versionFirstEnvelope.Data.Version.Version != 2 ||
		versionFirstEnvelope.Receipt.ResourceID !=
			versionFirstEnvelope.Data.Version.ID {
		t.Fatalf(
			"unexpected version draft: %+v",
			versionFirstEnvelope,
		)
	}
	versionReplay := fixture.request(
		t,
		http.MethodPost,
		versionPath,
		versionBody,
		"agent-knowledge-version-2",
	)
	if versionReplay.Code != http.StatusCreated {
		t.Fatalf(
			"version replay status = %d, body = %s",
			versionReplay.Code,
			versionReplay.Body.String(),
		)
	}
	var versionReplayEnvelope struct {
		Data    agentKnowledgeDocumentResponse `json:"data"`
		Receipt Receipt                        `json:"receipt"`
	}
	if err := json.Unmarshal(
		versionReplay.Body.Bytes(),
		&versionReplayEnvelope,
	); err != nil {
		t.Fatalf("decode replayed version draft: %v", err)
	}
	if versionReplayEnvelope.Data.Version.ID !=
		versionFirstEnvelope.Data.Version.ID ||
		versionReplayEnvelope.Receipt.OperationID !=
			versionFirstEnvelope.Receipt.OperationID {
		t.Fatalf(
			"version replay was not stable: first=%+v replay=%+v",
			versionFirstEnvelope,
			versionReplayEnvelope,
		)
	}
	var articleCount int64
	var versionCount int64
	var eventCount int64
	if err := fixture.db.Model(&models.KnowledgeArticle{}).
		Count(&articleCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.KnowledgeArticleVersion{}).
		Count(&versionCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.DomainEvent{}).
		Where(
			"type = ?",
			"io.chronodesk.knowledge.draft.created.v1",
		).
		Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if articleCount != 1 || versionCount != 2 || eventCount != 2 {
		t.Fatalf(
			"durable counts article=%d version=%d event=%d",
			articleCount,
			versionCount,
			eventCount,
		)
	}
}

func TestAgentKnowledgeDraftCannotGrantProjectAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := newAgentKnowledgeRouteFixture(
		t,
		[]string{models.ScopeKnowledgeWrite},
	)
	response := fixture.request(
		t,
		http.MethodPost,
		"/api/v2/projects/TEST/knowledge/articles",
		`{"key":"agent.acl","title":"Agent ACL","markdown":"# Draft","grant_project_access":true}`,
		"agent-knowledge-acl-denied",
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf(
			"Agent ACL request status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	var count int64
	if err := fixture.db.Model(&models.KnowledgeArticle{}).
		Where("key = ?", "agent.acl").
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("Agent ACL request persisted %d articles", count)
	}
}

func TestAgentKnowledgeArticleCreateReplayPinsOriginalVersionAfterNewDraft(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	fixture := newAgentKnowledgeRouteFixture(
		t,
		[]string{models.ScopeKnowledgeWrite},
	)
	path := "/api/v2/projects/TEST/knowledge/articles"
	articleBody := `{"key":"agent.pinned-v1","title":"Pinned v1","markdown":"# Version 1\n\nOriginal."}`
	first := fixture.request(
		t,
		http.MethodPost,
		path,
		articleBody,
		"agent-knowledge-pinned-v1",
	)
	if first.Code != http.StatusCreated {
		t.Fatalf(
			"create v1 status = %d, body = %s",
			first.Code,
			first.Body.String(),
		)
	}
	var v1 struct {
		Data agentKnowledgeDocumentResponse `json:"data"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &v1); err != nil {
		t.Fatalf("decode v1: %v", err)
	}

	versionPath := path + "/" + v1.Data.Article.ID + "/drafts"
	second := fixture.request(
		t,
		http.MethodPost,
		versionPath,
		`{"title":"Pinned v2","markdown":"# Version 2\n\nLater draft."}`,
		"agent-knowledge-pinned-v2",
	)
	if second.Code != http.StatusCreated {
		t.Fatalf(
			"create v2 status = %d, body = %s",
			second.Code,
			second.Body.String(),
		)
	}

	replay := fixture.request(
		t,
		http.MethodPost,
		path,
		articleBody,
		"agent-knowledge-pinned-v1",
	)
	if replay.Code != http.StatusCreated {
		t.Fatalf(
			"replay v1 status = %d, body = %s",
			replay.Code,
			replay.Body.String(),
		)
	}
	var replayed struct {
		Data agentKnowledgeDocumentResponse `json:"data"`
	}
	if err := json.Unmarshal(replay.Body.Bytes(), &replayed); err != nil {
		t.Fatalf("decode replayed v1: %v", err)
	}
	if replayed.Data.Article.ID != v1.Data.Article.ID ||
		replayed.Data.Version.ID != v1.Data.Version.ID ||
		replayed.Data.Version.ContentHash != v1.Data.Version.ContentHash ||
		replayed.Data.Markdown != v1.Data.Markdown {
		t.Fatalf(
			"article create replay drifted from v1: first=%+v replay=%+v",
			v1.Data,
			replayed.Data,
		)
	}
}

func TestAgentKnowledgeArticleCreateReplayPinsPublishedOriginalVersion(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	fixture := newAgentKnowledgeRouteFixture(
		t,
		[]string{models.ScopeKnowledgeWrite},
	)
	path := "/api/v2/projects/TEST/knowledge/articles"
	body := `{"key":"agent.published-v1","title":"Published v1","markdown":"# Published\n\nReviewed."}`
	first := fixture.request(
		t,
		http.MethodPost,
		path,
		body,
		"agent-knowledge-published-v1",
	)
	if first.Code != http.StatusCreated {
		t.Fatalf(
			"create published v1 status = %d, body = %s",
			first.Code,
			first.Body.String(),
		)
	}
	var v1 struct {
		Data agentKnowledgeDocumentResponse `json:"data"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &v1); err != nil {
		t.Fatalf("decode published v1: %v", err)
	}
	fixture.publishVersion(t, v1.Data.Version.ID)

	replay := fixture.request(
		t,
		http.MethodPost,
		path,
		body,
		"agent-knowledge-published-v1",
	)
	if replay.Code != http.StatusCreated {
		t.Fatalf(
			"replay published v1 status = %d, body = %s",
			replay.Code,
			replay.Body.String(),
		)
	}
	var replayed struct {
		Data agentKnowledgeDocumentResponse `json:"data"`
	}
	if err := json.Unmarshal(replay.Body.Bytes(), &replayed); err != nil {
		t.Fatalf("decode replayed published v1: %v", err)
	}
	if replayed.Data.Article.ID != v1.Data.Article.ID ||
		replayed.Data.Version.ID != v1.Data.Version.ID ||
		replayed.Data.Version.ContentHash != v1.Data.Version.ContentHash ||
		replayed.Data.Version.Status != models.KnowledgeVersionPublished ||
		replayed.Data.Markdown != v1.Data.Markdown {
		t.Fatalf(
			"published article replay drifted from v1: first=%+v replay=%+v",
			v1.Data,
			replayed.Data,
		)
	}
	if err := fixture.db.Model(&models.ProjectPrincipalGrant{}).
		Where(
			"project_id = ? AND service_principal_id = ?",
			fixture.project.project.ID,
			fixture.principal.ID,
		).
		Update("is_active", false).Error; err != nil {
		t.Fatalf("revoke Agent knowledge grant: %v", err)
	}
	denied := fixture.request(
		t,
		http.MethodPost,
		path,
		body,
		"agent-knowledge-published-v1",
	)
	if denied.Code != http.StatusForbidden {
		t.Fatalf(
			"replay after live grant revocation status = %d, body = %s",
			denied.Code,
			denied.Body.String(),
		)
	}
}

func TestAgentKnowledgeReadRoutesReturnClosedAuthorizedDocuments(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	fixture := newAgentKnowledgeRouteFixture(
		t,
		[]string{
			models.ScopeKnowledgeRead,
			models.ScopeKnowledgeWrite,
		},
	)
	create := fixture.request(
		t,
		http.MethodPost,
		"/api/v2/projects/TEST/knowledge/articles",
		`{"key":"readable.runbook","title":"Readable runbook","markdown":"# Readable\n\nAuthorized."}`,
		"agent-knowledge-readable-1",
	)
	if create.Code != http.StatusCreated {
		t.Fatalf(
			"create readable draft status = %d, body = %s",
			create.Code,
			create.Body.String(),
		)
	}
	var created struct {
		Data agentKnowledgeDocumentResponse `json:"data"`
	}
	if err := json.Unmarshal(
		create.Body.Bytes(),
		&created,
	); err != nil {
		t.Fatalf("decode readable draft: %v", err)
	}
	var persistedArticle models.KnowledgeArticle
	if err := fixture.db.Where(
		"id = ?",
		created.Data.Article.ID,
	).First(&persistedArticle).Error; err != nil {
		t.Fatalf("load persisted readable article: %v", err)
	}
	if persistedArticle.ProjectID != fixture.project.project.ID {
		t.Fatalf(
			"persisted article project = %d, want %d",
			persistedArticle.ProjectID,
			fixture.project.project.ID,
		)
	}

	list := fixture.request(
		t,
		http.MethodGet,
		"/api/v2/projects/TEST/knowledge/articles?page=1&page_size=10",
		"",
		"",
	)
	if list.Code != http.StatusOK {
		t.Fatalf(
			"list knowledge status = %d, body = %s",
			list.Code,
			list.Body.String(),
		)
	}
	var listEnvelope struct {
		Data agentKnowledgeArticlePage `json:"data"`
	}
	if err := json.Unmarshal(
		list.Body.Bytes(),
		&listEnvelope,
	); err != nil {
		t.Fatalf("decode knowledge list: %v", err)
	}
	if len(listEnvelope.Data.Items) != 0 ||
		listEnvelope.Data.Total != 0 {
		t.Fatalf(
			"draft leaked into published knowledge list: %+v",
			listEnvelope.Data,
		)
	}

	documentPath := "/api/v2/projects/TEST/knowledge/articles/" +
		created.Data.Article.ID +
		"/document?version_id=" +
		created.Data.Version.ID
	document := fixture.request(
		t,
		http.MethodGet,
		documentPath,
		"",
		"",
	)
	if document.Code != http.StatusOK {
		t.Fatalf(
			"get knowledge document status = %d, body = %s",
			document.Code,
			document.Body.String(),
		)
	}
	var documentEnvelope struct {
		Data agentKnowledgeDocumentResponse `json:"data"`
	}
	if err := json.Unmarshal(
		document.Body.Bytes(),
		&documentEnvelope,
	); err != nil {
		t.Fatalf("decode knowledge document: %v", err)
	}
	if documentEnvelope.Data.Article.ID !=
		created.Data.Article.ID ||
		documentEnvelope.Data.Version.ID !=
			created.Data.Version.ID ||
		documentEnvelope.Data.Markdown !=
			"# Readable\n\nAuthorized." {
		t.Fatalf(
			"unexpected knowledge document: %+v",
			documentEnvelope.Data,
		)
	}
	if strings.Contains(document.Body.String(), "object_key") {
		t.Fatalf(
			"knowledge document leaked object key: %s",
			document.Body.String(),
		)
	}
}

func TestAgentKnowledgeSourceProjectionRequiresLiveSourceAuthority(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	for _, testCase := range []struct {
		name       string
		scopes     []string
		visibility services.KnowledgeSourceVisibility
	}{
		{
			name: "knowledge read alone",
			scopes: []string{
				models.ScopeKnowledgeRead,
			},
			visibility: services.KnowledgeSourceRestricted,
		},
		{
			name: "ticket read without attachment read",
			scopes: []string{
				models.ScopeKnowledgeRead,
				models.ScopeTicketsRead,
			},
			visibility: services.KnowledgeSourceRestricted,
		},
		{
			name: "ticket and attachment read",
			scopes: []string{
				models.ScopeKnowledgeRead,
				models.ScopeTicketsRead,
				models.ScopeAttachmentsRead,
			},
			visibility: services.KnowledgeSourceFull,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newAgentKnowledgeRouteFixture(
				t,
				testCase.scopes,
			)
			article, version, ticket, attachment :=
				fixture.seedPublishedKnowledgeSource(t)
			response := fixture.request(
				t,
				http.MethodGet,
				"/api/v2/projects/TEST/knowledge/articles/"+
					article.ID+
					"/document?version_id="+version.ID,
				"",
				"",
			)
			if response.Code != http.StatusOK {
				t.Fatalf(
					"get source document status = %d, body = %s",
					response.Code,
					response.Body.String(),
				)
			}
			var envelope struct {
				Data agentKnowledgeDocumentResponse `json:"data"`
			}
			if err := json.Unmarshal(
				response.Body.Bytes(),
				&envelope,
			); err != nil {
				t.Fatal(err)
			}
			if len(envelope.Data.Sources) != 1 ||
				envelope.Data.Sources[0].Visibility !=
					testCase.visibility {
				t.Fatalf(
					"source projection = %+v, want %q",
					envelope.Data.Sources,
					testCase.visibility,
				)
			}
			source := envelope.Data.Sources[0]
			if testCase.visibility ==
				services.KnowledgeSourceFull {
				if source.SourceTicketID == nil ||
					*source.SourceTicketID != ticket.ID ||
					source.SourceAttachmentID == nil ||
					*source.SourceAttachmentID != attachment.ID ||
					source.TicketTitle != ticket.Title ||
					source.AttachmentHash != attachment.Hash {
					t.Fatalf(
						"full source projection is incomplete: %+v",
						source,
					)
				}
				if err := fixture.db.Create(&models.AgentPolicy{
					ID: uuid.Must(uuid.NewV7()).String(),
					ServicePrincipalID: fixture.
						principal.ID,
					Name:         "revoke attachment source detail",
					Effect:       models.AgentPolicyEffectDeny,
					Scope:        models.ScopeAttachmentsRead,
					Action:       "ticket.attachment.read",
					ResourceType: "ticket",
					ResourceID: strconv.FormatUint(
						uint64(ticket.ID),
						10,
					),
					Priority: 100,
					IsActive: true,
				}).Error; err != nil {
					t.Fatal(err)
				}
				revoked := fixture.request(
					t,
					http.MethodGet,
					"/api/v2/projects/TEST/knowledge/articles/"+
						article.ID+
						"/document?version_id="+version.ID,
					"",
					"",
				)
				if revoked.Code != http.StatusOK ||
					strings.Contains(
						revoked.Body.String(),
						`"visibility":"full"`,
					) ||
					!strings.Contains(
						revoked.Body.String(),
						`"visibility":"restricted"`,
					) {
					t.Fatalf(
						"revoked source policy response = %d %s",
						revoked.Code,
						revoked.Body.String(),
					)
				}

				return
			}
			for _, protected := range []string{
				"source_ticket_id",
				"source_attachment_id",
				"ticket_number",
				"ticket_title",
				"attachment_name",
				"attachment_hash",
			} {
				if strings.Contains(
					response.Body.String(),
					`"`+protected+`"`,
				) {
					t.Fatalf(
						"restricted source leaked %q: %s",
						protected,
						response.Body.String(),
					)
				}
			}
		})
	}
}

func TestAgentKnowledgeSearchUsesProjectAndPrincipalACLFilters(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	fixture := newAgentKnowledgeRouteFixture(
		t,
		[]string{models.ScopeKnowledgeRead},
	)
	if err := fixture.db.AutoMigrate(
		&models.ProjectModelPolicy{},
	); err != nil {
		t.Fatalf("migrate Agent knowledge model policy: %v", err)
	}
	index := &agentKnowledgeSearchIndex{}
	knowledge, err := services.NewKnowledgeService(
		fixture.db,
		services.KnowledgeServiceDependencies{
			SearchIndex: index,
			ModelProviders: map[string]services.ModelProvider{
				"agent-search-test": agentKnowledgeModelProvider{},
			},
			Events: fixture.native,
		},
	)
	if err != nil {
		t.Fatalf("create searchable Agent knowledge service: %v", err)
	}
	fixture.handler.SetKnowledgeService(knowledge)
	systemActor := models.SystemActor("agent-search-test")
	if err := fixture.db.Create(&models.ProjectModelPolicy{
		OrganizationID: fixture.project.organization.ID,
		ProjectID:      fixture.project.project.ID,
		PolicyKey:      "knowledge",
		IsActive:       true,
		ProviderKey:    "agent-search-test",
		GenerateModel:  "generate-test",
		EmbeddingModel: "embedding-test",
		RerankModel:    "rerank-test",
		DataEgress:     models.ModelDataEgressDenied,
		RedactionRules: datatypes.JSON(
			[]byte(`[]`),
		),
		ProviderAllowlist: datatypes.JSON(
			[]byte(`["agent-search-test"]`),
		),
		ModelAllowlist: datatypes.JSON(
			[]byte(
				`["generate-test","embedding-test","rerank-test"]`,
			),
		),
		CreatedByType: systemActor.Type,
		CreatedByID:   systemActor.ID,
	}).Error; err != nil {
		t.Fatalf("seed Agent knowledge model policy: %v", err)
	}
	response := fixture.request(
		t,
		http.MethodPost,
		"/api/v2/projects/TEST/knowledge/searches",
		`{"query":"safe runbook","limit":5}`,
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"Agent search status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	var envelope struct {
		Data agentKnowledgeSearchResponse `json:"data"`
	}
	if err := json.Unmarshal(
		response.Body.Bytes(),
		&envelope,
	); err != nil {
		t.Fatalf("decode Agent knowledge search: %v", err)
	}
	if envelope.Data.SearchID == "" ||
		len(envelope.Data.Items) != 0 {
		t.Fatalf(
			"unexpected empty-index Agent search: %+v",
			envelope.Data,
		)
	}
	if index.request.Filter.OrganizationID !=
		fixture.project.organization.ID ||
		index.request.Filter.ProjectID !=
			fixture.project.project.ID ||
		!index.request.Filter.PublishedOnly ||
		index.request.Filter.VirusScan !=
			models.VirusScanClean {
		t.Fatalf(
			"search index filter lost hard boundaries: %+v",
			index.request.Filter,
		)
	}
	foundPrincipal := false
	for _, subject := range index.request.Filter.ACLSubjects {
		if subject.Type ==
			models.KnowledgeACLServicePrincipal &&
			subject.ID == fixture.principal.ID {
			foundPrincipal = true
		}
	}
	if !foundPrincipal {
		t.Fatalf(
			"search ACL omitted principal subject: %+v",
			index.request.Filter.ACLSubjects,
		)
	}
}

func TestAgentKnowledgeRejectsUnknownAndOversizedDraftBodies(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	fixture := newAgentKnowledgeRouteFixture(
		t,
		[]string{models.ScopeKnowledgeWrite},
	)
	path := "/api/v2/projects/TEST/knowledge/articles"
	unknown := fixture.request(
		t,
		http.MethodPost,
		path,
		`{"key":"strict","title":"Strict","markdown":"Strict","publish":true}`,
		"agent-knowledge-strict-1",
	)
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf(
			"unknown field status = %d, body = %s",
			unknown.Code,
			unknown.Body.String(),
		)
	}
	oversized := fixture.request(
		t,
		http.MethodPost,
		path,
		`{"key":"large","title":"Large","markdown":"`+
			strings.Repeat(
				"x",
				int(maxAgentKnowledgeRequestBytes),
			)+
			`"}`,
		"agent-knowledge-large-1",
	)
	if oversized.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"oversized body status = %d, body = %s",
			oversized.Code,
			oversized.Body.String(),
		)
	}
	var idempotencyCount int64
	if err := fixture.db.Model(&models.IdempotencyRecord{}).
		Count(&idempotencyCount).Error; err != nil {
		t.Fatal(err)
	}
	if idempotencyCount != 0 {
		t.Fatalf(
			"invalid bodies reserved %d idempotency records",
			idempotencyCount,
		)
	}
}

func TestAgentKnowledgeSourceScopesFailClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name   string
		scopes []string
		body   string
	}{
		{
			name: "ticket source requires tickets read",
			scopes: []string{
				models.ScopeKnowledgeWrite,
			},
			body: `{"key":"ticket-source","title":"Ticket source","markdown":"Ticket source","source_ticket_id":1}`,
		},
		{
			name: "attachment source requires attachments read",
			scopes: []string{
				models.ScopeKnowledgeWrite,
				models.ScopeTicketsRead,
			},
			body: `{"key":"attachment-source","title":"Attachment source","markdown":"Attachment source","source_ticket_id":1,"source_attachment_ids":[2]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAgentKnowledgeRouteFixture(
				t,
				test.scopes,
			)
			response := fixture.request(
				t,
				http.MethodPost,
				"/api/v2/projects/TEST/knowledge/articles",
				test.body,
				"agent-knowledge-source-scope",
			)
			if response.Code != http.StatusForbidden {
				t.Fatalf(
					"source scope status = %d, body = %s",
					response.Code,
					response.Body.String(),
				)
			}
			var problem Problem
			if err := json.Unmarshal(
				response.Body.Bytes(),
				&problem,
			); err != nil {
				t.Fatalf("decode source scope problem: %v", err)
			}
			if problem.Code != ProblemInsufficientScope {
				t.Fatalf(
					"source scope code = %q, want %q",
					problem.Code,
					ProblemInsufficientScope,
				)
			}
			var idempotencyCount int64
			if err := fixture.db.Model(
				&models.IdempotencyRecord{},
			).Count(&idempotencyCount).Error; err != nil {
				t.Fatal(err)
			}
			if idempotencyCount != 0 {
				t.Fatalf(
					"source scope denial reserved %d records",
					idempotencyCount,
				)
			}
		})
	}
}

func TestAgentKnowledgeWritePolicyDenialCreatesNoDraft(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	fixture := newAgentKnowledgeRouteFixture(
		t,
		[]string{models.ScopeKnowledgeWrite},
	)
	if _, err := fixture.native.CreateAgentPolicy(
		context.Background(),
		services.CreateAgentPolicyInput{
			ServicePrincipalID: fixture.principal.ID,
			Name:               "deny Agent knowledge drafts",
			Effect:             models.AgentPolicyEffectDeny,
			Scope:              models.ScopeKnowledgeWrite,
			Action:             "knowledge.article.draft.create",
			ResourceType:       "knowledge_article",
			ResourceID:         "*",
		},
	); err != nil {
		t.Fatalf("create Agent knowledge deny policy: %v", err)
	}
	response := fixture.request(
		t,
		http.MethodPost,
		"/api/v2/projects/TEST/knowledge/articles",
		`{"key":"denied","title":"Denied","markdown":"Denied"}`,
		"agent-knowledge-denied-1",
	)
	if response.Code != http.StatusForbidden {
		t.Fatalf(
			"denied create status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	var articleCount int64
	var idempotencyCount int64
	if err := fixture.db.Model(&models.KnowledgeArticle{}).
		Count(&articleCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.IdempotencyRecord{}).
		Count(&idempotencyCount).Error; err != nil {
		t.Fatal(err)
	}
	if articleCount != 0 || idempotencyCount != 0 {
		t.Fatalf(
			"denied write persisted article=%d idempotency=%d",
			articleCount,
			idempotencyCount,
		)
	}
}
