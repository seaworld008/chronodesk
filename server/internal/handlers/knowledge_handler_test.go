package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type knowledgeHandlerTestEnvironment struct {
	db               *gorm.DB
	projectService   *services.ProjectService
	knowledgeService *services.KnowledgeService
	index            *knowledgeHandlerTestIndex
	operations       models.Project
	security         models.Project
	manager          models.User
	agent            models.User
	requester        models.User
	observer         models.User
}

type knowledgeHandlerEnvelope[T any] struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data T      `json:"data"`
}

func TestKnowledgeHandlerFullAdministrativeAndACLSearchFlow(
	t *testing.T,
) {
	environment := newKnowledgeHandlerTestEnvironment(t)
	managerRouter := knowledgeHandlerTestRouter(
		environment,
		environment.manager,
	)
	agentRouter := knowledgeHandlerTestRouter(
		environment,
		environment.agent,
	)

	policyRequest := updateKnowledgeModelPolicyRequest{
		ProviderKey:    "handler-provider",
		GenerateModel:  "generate-v1",
		EmbeddingModel: "embed-v1",
		RerankModel:    "rerank-v1",
		DataEgress:     models.ModelDataEgressRedacted,
		RedactionRules: []models.ModelRedactionRule{
			{
				Literal:     "secret-internal-pattern",
				Replacement: "[REDACTED]",
			},
		},
		ProviderAllowlist: []string{"handler-provider"},
		ModelAllowlist: []string{
			"generate-v1",
			"embed-v1",
			"rerank-v1",
		},
		MonthlyTokenBudget:      100000,
		MonthlyCostBudgetMicros: 500000,
		RequestsPerMinute:       30,
		TokensPerMinute:         10000,
	}
	policy, policyBody := performKnowledgeHandlerRequest[knowledgeModelPolicyResponse](
		t,
		managerRouter,
		http.MethodPut,
		"/api/projects/OPS/knowledge/model-policy",
		policyRequest,
		http.StatusOK,
	)
	if policy.Data.RedactionRuleCount != 1 ||
		policy.Data.ProviderAllowlistCount != 1 ||
		policy.Data.ModelAllowlistCount != 3 {
		t.Fatalf("safe policy response = %+v", policy.Data)
	}
	var policyJSON struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(policyBody), &policyJSON); err != nil {
		t.Fatal(err)
	}
	for _, sensitive := range []string{
		"redaction_rules",
		"provider_allowlist",
		"model_allowlist",
	} {
		if _, exposed := policyJSON.Data[sensitive]; exposed {
			t.Errorf("policy response exposed %q", sensitive)
		}
	}
	if strings.Contains(policyBody, "secret-internal-pattern") {
		t.Fatal("policy response exposed a redaction literal")
	}
	performKnowledgeHandlerRequest[knowledgeModelPolicyResponse](
		t,
		managerRouter,
		http.MethodGet,
		"/api/projects/OPS/knowledge/model-policy",
		nil,
		http.StatusOK,
	)
	performKnowledgeHandlerRequest[json.RawMessage](
		t,
		agentRouter,
		http.MethodGet,
		"/api/projects/OPS/knowledge/model-policy",
		nil,
		http.StatusForbidden,
	)
	performKnowledgeHandlerRequest[json.RawMessage](
		t,
		managerRouter,
		http.MethodGet,
		"/api/projects/SEC/knowledge/model-policy",
		nil,
		http.StatusNotFound,
	)

	article, _ := performKnowledgeHandlerRequest[knowledgeArticleResponse](
		t,
		managerRouter,
		http.MethodPost,
		"/api/projects/OPS/knowledge/articles",
		createKnowledgeArticleRequest{
			Key:                "service-recovery",
			Title:              "服务恢复手册",
			Summary:            "生产服务恢复知识",
			GrantProjectAccess: true,
		},
		http.StatusCreated,
	)

	_, versionBody := performKnowledgeHandlerRequest[knowledgeVersionResponse](
		t,
		managerRouter,
		http.MethodPost,
		fmt.Sprintf(
			"/api/projects/OPS/knowledge/articles/%s/versions",
			article.Data.ID,
		),
		registerKnowledgeVersionRequest{
			Title:           "服务恢复手册 v1",
			ObjectProvider:  "s3",
			ObjectBucket:    "knowledge-private",
			ObjectKey:       "projects/ops/service-recovery.pdf",
			ObjectVersionID: "object-v1",
			FileName:        "service-recovery.pdf",
			MimeType:        "application/pdf",
			SizeBytes:       4096,
			ContentHash:     strings.Repeat("a", 64),
		},
		http.StatusCreated,
	)
	if strings.Contains(versionBody, "knowledge-private") ||
		strings.Contains(versionBody, "projects/ops/service-recovery.pdf") ||
		strings.Contains(versionBody, `"object_provider"`) {
		t.Fatalf("version response exposed object location: %s", versionBody)
	}
	var versionEnvelope knowledgeHandlerEnvelope[knowledgeVersionResponse]
	if err := json.Unmarshal([]byte(versionBody), &versionEnvelope); err != nil {
		t.Fatal(err)
	}
	version := versionEnvelope.Data

	performKnowledgeHandlerRequest[json.RawMessage](
		t,
		managerRouter,
		http.MethodPost,
		fmt.Sprintf(
			"/api/projects/OPS/knowledge/articles/%s/versions",
			article.Data.ID,
		),
		registerKnowledgeVersionRequest{
			Title:          "URL 注入",
			ObjectProvider: "s3",
			ObjectBucket:   "knowledge-private",
			ObjectKey:      "https://attacker.example/file.pdf",
			FileName:       "file.pdf",
			MimeType:       "application/pdf",
			SizeBytes:      10,
			ContentHash:    strings.Repeat("b", 64),
		},
		http.StatusBadRequest,
	)
	performKnowledgeHandlerRequest[json.RawMessage](
		t,
		managerRouter,
		http.MethodPost,
		fmt.Sprintf(
			"/api/projects/SEC/knowledge/articles/%s/versions",
			article.Data.ID,
		),
		registerKnowledgeVersionRequest{
			Title:          "跨项目版本",
			ObjectProvider: "s3",
			ObjectBucket:   "knowledge-private",
			ObjectKey:      "projects/sec/forbidden.pdf",
			FileName:       "forbidden.pdf",
			MimeType:       "application/pdf",
			SizeBytes:      10,
			ContentHash:    strings.Repeat("c", 64),
		},
		http.StatusNotFound,
	)

	task, _ := performKnowledgeHandlerRequest[knowledgeIngestionResponse](
		t,
		managerRouter,
		http.MethodPost,
		fmt.Sprintf(
			"/api/projects/OPS/knowledge/versions/%s/ingestions",
			version.ID,
		),
		queueKnowledgeIngestionRequest{ParserKey: "pdf"},
		http.StatusCreated,
	)
	performKnowledgeHandlerRequest[json.RawMessage](
		t,
		managerRouter,
		http.MethodPost,
		fmt.Sprintf(
			"/api/projects/OPS/knowledge/ingestions/%s/parsing",
			task.Data.ID,
		),
		nil,
		http.StatusNotFound,
	)
	performKnowledgeHandlerRequest[json.RawMessage](
		t,
		managerRouter,
		http.MethodPost,
		fmt.Sprintf(
			"/api/projects/OPS/knowledge/versions/%s/scan-results",
			version.ID,
		),
		knowledgeScanResultRequest{
			Status: models.VirusScanClean,
			Detail: "browser must never assert scan results",
		},
		http.StatusNotFound,
	)
	workerContext, err := services.EnsureSystemProjectOperationContext(
		context.Background(),
		environment.operations.Scope(),
		models.SystemActor("knowledge-ingestion-worker"),
		"knowledge-handler-worker",
		"knowledge-handler-worker",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := environment.knowledgeService.MarkVersionVirusScan(
		workerContext,
		version.ID,
		models.VirusScanClean,
		"scanner clean",
	); err != nil {
		t.Fatalf("worker records virus scan: %v", err)
	}
	if _, err := environment.knowledgeService.StartParsing(
		workerContext,
		task.Data.ID,
	); err != nil {
		t.Fatalf("worker starts parsing: %v", err)
	}
	page := 4
	if _, err := environment.knowledgeService.StoreChunks(
		workerContext,
		task.Data.ID,
		[]services.KnowledgeChunkInput{
			{
				PageNumber: &page,
				Content:    "完整内部内容不可原样返回；服务恢复前检查数据库健康状态。",
				Snippet:    "服务恢复前检查数据库健康状态。",
				TokenCount: 18,
			},
		},
	); err != nil {
		t.Fatalf("worker stores chunks: %v", err)
	}
	if _, err := environment.knowledgeService.CompleteIngestion(
		workerContext,
		task.Data.ID,
	); err != nil {
		t.Fatalf("worker completes ingestion: %v", err)
	}
	performKnowledgeHandlerRequest[knowledgeVersionResponse](
		t,
		managerRouter,
		http.MethodPost,
		fmt.Sprintf(
			"/api/projects/OPS/knowledge/versions/%s/publication",
			version.ID,
		),
		nil,
		http.StatusOK,
	)
	indexState, _ := performKnowledgeHandlerRequest[knowledgeIndexStateResponse](
		t,
		managerRouter,
		http.MethodPost,
		"/api/projects/OPS/knowledge/index-rebuilds",
		nil,
		http.StatusAccepted,
	)
	if indexState.Data.Status != models.KnowledgeIndexRebuildRequested {
		t.Fatalf("index state = %+v", indexState.Data)
	}
	outboxWorkerContext, err := services.EnsureSystemProjectOperationContext(
		context.Background(),
		environment.operations.Scope(),
		models.SystemActor("knowledge-index-outbox-test"),
		"knowledge-index-outbox-test",
		"knowledge-index-outbox-test",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := environment.knowledgeService.ExecuteIndexRebuildOutbox(
		outboxWorkerContext,
		indexState.Data.ID,
		indexState.Data.DesiredGeneration,
	); err != nil {
		t.Fatalf("execute queued index rebuild: %v", err)
	}
	indexState, _ = performKnowledgeHandlerRequest[knowledgeIndexStateResponse](
		t,
		managerRouter,
		http.MethodGet,
		"/api/projects/OPS/knowledge/index-rebuilds/current",
		nil,
		http.StatusOK,
	)
	if indexState.Data.Status != models.KnowledgeIndexReady ||
		indexState.Data.DocumentCount != 1 {
		t.Fatalf("completed index state = %+v", indexState.Data)
	}

	searchQuery := "UNIQUE_QUERY_不得回显 如何恢复服务"
	search, searchBody := performKnowledgeHandlerRequest[knowledgeSearchResponse](
		t,
		agentRouter,
		http.MethodPost,
		"/api/projects/OPS/knowledge/searches",
		knowledgeSearchRequest{
			Query: searchQuery,
			Limit: 1,
		},
		http.StatusOK,
	)
	if strings.Contains(searchBody, searchQuery) {
		t.Fatal("search response echoed the query")
	}
	if len(search.Data.Items) != 1 {
		t.Fatalf("search response = %+v", search.Data)
	}
	citation := search.Data.Items[0]
	if citation.VersionID != version.ID ||
		citation.DocumentVersion != version.Version ||
		citation.PageNumber == nil ||
		*citation.PageNumber != page ||
		citation.ContentHash == "" ||
		citation.Snippet == "" {
		t.Fatalf("citation = %+v", citation)
	}
	if environment.index.lastSearch.Filter.OrganizationID !=
		environment.operations.OrganizationID ||
		environment.index.lastSearch.Filter.ProjectID !=
			environment.operations.ID ||
		len(environment.index.lastSearch.Filter.ACLSubjects) == 0 {
		t.Fatalf(
			"search filter was not pushed down: %+v",
			environment.index.lastSearch.Filter,
		)
	}
	performKnowledgeHandlerRequest[knowledgeFeedbackResponse](
		t,
		agentRouter,
		http.MethodPost,
		fmt.Sprintf(
			"/api/projects/OPS/knowledge/citations/%s/feedback",
			citation.ID,
		),
		knowledgeFeedbackRequest{
			Rating:  models.KnowledgeFeedbackHelpful,
			Comment: "引用准确",
		},
		http.StatusCreated,
	)
	for role, user := range map[string]models.User{
		"requester": environment.requester,
		"observer":  environment.observer,
	} {
		t.Run(role+"_can_search_project_acl", func(t *testing.T) {
			readerRouter := knowledgeHandlerTestRouter(environment, user)
			response, _ := performKnowledgeHandlerRequest[knowledgeSearchResponse](
				t,
				readerRouter,
				http.MethodPost,
				"/api/projects/OPS/knowledge/searches",
				knowledgeSearchRequest{
					Query: "恢复服务",
					Limit: 1,
				},
				http.StatusOK,
			)
			if len(response.Data.Items) != 1 {
				t.Fatalf("%s search response = %+v", role, response.Data)
			}
		})
	}

	performKnowledgeHandlerRequest[json.RawMessage](
		t,
		agentRouter,
		http.MethodPost,
		"/api/projects/OPS/knowledge/articles",
		createKnowledgeArticleRequest{
			Key:   "forbidden",
			Title: "无权创建",
		},
		http.StatusForbidden,
	)
}

func TestKnowledgeHandlerRejectsScopeActorAndURLFields(
	t *testing.T,
) {
	environment := newKnowledgeHandlerTestEnvironment(t)
	router := knowledgeHandlerTestRouter(environment, environment.manager)
	injected := json.RawMessage(`{
		"key":"injected",
		"title":"范围注入",
		"grant_project_access":true,
		"organization_id":999,
		"project_id":999,
		"actor":{"type":"human","id":"999"}
	}`)
	response, _ := performKnowledgeHandlerRequest[json.RawMessage](
		t,
		router,
		http.MethodPost,
		"/api/projects/OPS/knowledge/articles",
		injected,
		http.StatusBadRequest,
	)
	if response.Msg != "知识请求参数无效" {
		t.Fatalf("scope injection message = %q", response.Msg)
	}
	var count int64
	if err := environment.db.Model(&models.KnowledgeArticle{}).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("articles created after scope injection = %d", count)
	}

	withURL := json.RawMessage(`{
		"title":"URL 字段注入",
		"object_provider":"s3",
		"object_bucket":"knowledge",
		"object_key":"projects/ops/file.pdf",
		"file_name":"file.pdf",
		"mime_type":"application/pdf",
		"size_bytes":10,
		"content_hash":"` + strings.Repeat("a", 64) + `",
		"object_storage_url":"https://attacker.example/file.pdf"
	}`)
	performKnowledgeHandlerRequest[json.RawMessage](
		t,
		router,
		http.MethodPost,
		"/api/projects/OPS/knowledge/articles/missing/versions",
		withURL,
		http.StatusBadRequest,
	)
}

type knowledgeHandlerTestIndex struct {
	documents  []services.HybridIndexDocument
	lastSearch services.HybridSearchRequest
}

func (index *knowledgeHandlerTestIndex) Search(
	_ context.Context,
	request services.HybridSearchRequest,
) ([]services.HybridSearchHit, error) {
	if err := request.Filter.Validate(); err != nil {
		return nil, err
	}
	index.lastSearch = request
	hits := make([]services.HybridSearchHit, 0)
	for _, document := range index.documents {
		if document.OrganizationID != request.Filter.OrganizationID ||
			document.ProjectID != request.Filter.ProjectID ||
			!knowledgeHandlerACLIntersects(
				document.ACLSubjects,
				request.Filter.ACLSubjects,
			) {
			continue
		}
		hits = append(hits, services.HybridSearchHit{
			OrganizationID:  document.OrganizationID,
			ProjectID:       document.ProjectID,
			ArticleID:       document.ArticleID,
			VersionID:       document.VersionID,
			DocumentVersion: document.DocumentVersion,
			ChunkID:         document.ChunkID,
			PageNumber:      document.PageNumber,
			Snippet:         document.Snippet,
			ContentHash:     document.ContentHash,
			Score:           0.8,
			TokenCount:      document.TokenCount,
		})
	}
	if len(hits) > request.Limit {
		hits = hits[:request.Limit]
	}
	return hits, nil
}

func (index *knowledgeHandlerTestIndex) ReplaceProject(
	_ context.Context,
	replacement services.HybridIndexReplacement,
) error {
	index.documents = append(
		[]services.HybridIndexDocument(nil),
		replacement.Documents...,
	)
	return nil
}

type knowledgeHandlerTestProvider struct{}

func (knowledgeHandlerTestProvider) Descriptor() services.ModelProviderDescriptor {
	return services.ModelProviderDescriptor{
		Key:        "handler-provider",
		IsExternal: true,
	}
}

func (knowledgeHandlerTestProvider) Generate(
	_ context.Context,
	_ services.ModelGenerateRequest,
) (services.ModelGenerateResponse, error) {
	return services.ModelGenerateResponse{Text: "unused"}, nil
}

func (knowledgeHandlerTestProvider) Embed(
	_ context.Context,
	_ services.ModelEmbedRequest,
) (services.ModelEmbedResponse, error) {
	return services.ModelEmbedResponse{
		Embeddings: [][]float32{{0.1, 0.2}},
	}, nil
}

func (knowledgeHandlerTestProvider) Rerank(
	_ context.Context,
	request services.ModelRerankRequest,
) (services.ModelRerankResponse, error) {
	items := make([]services.ModelRerankItem, 0, request.Limit)
	for _, candidate := range request.Candidates {
		if len(items) == request.Limit {
			break
		}
		items = append(items, services.ModelRerankItem{
			ID:    candidate.ID,
			Score: 0.95,
		})
	}
	return services.ModelRerankResponse{Items: items}, nil
}

func newKnowledgeHandlerTestEnvironment(
	t *testing.T,
) knowledgeHandlerTestEnvironment {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.ProjectMembership{},
		&models.Queue{},
		&models.KnowledgeArticle{},
		&models.KnowledgeArticleVersion{},
		&models.KnowledgeArticleACL{},
		&models.KnowledgeIngestionTask{},
		&models.KnowledgeChunk{},
		&models.KnowledgeCitation{},
		&models.KnowledgeFeedback{},
		&models.KnowledgeIndexState{},
		&models.ProjectModelPolicy{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.AuditLedgerEntry{},
	); err != nil {
		t.Fatal(err)
	}
	organization := models.Organization{
		Slug:   "knowledge-handler",
		Name:   "Knowledge Handler",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "OPS",
		Name:           "Operations",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	projects := []models.Project{
		{
			OrganizationID: organization.ID,
			BusinessUnitID: unit.ID,
			Key:            "OPS",
			Name:           "Operations",
			Status:         models.ProjectStatusActive,
		},
		{
			OrganizationID: organization.ID,
			BusinessUnitID: unit.ID,
			Key:            "SEC",
			Name:           "Security",
			Status:         models.ProjectStatusActive,
		},
	}
	if err := db.Create(&projects).Error; err != nil {
		t.Fatal(err)
	}
	for _, project := range projects {
		if err := db.Create(&models.Queue{
			ProjectID: project.ID,
			Key:       "default",
			Name:      "默认队列",
			Status:    models.QueueStatusActive,
			IsDefault: true,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	manager := models.User{
		Username:     "knowledge-manager",
		Email:        "knowledge-manager@example.test",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	agent := models.User{
		Username:     "knowledge-agent",
		Email:        "knowledge-agent@example.test",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	requester := models.User{
		Username:     "knowledge-requester",
		Email:        "knowledge-requester@example.test",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	observer := models.User{
		Username:     "knowledge-observer",
		Email:        "knowledge-observer@example.test",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&manager).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&agent).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&requester).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&observer).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]models.ProjectMembership{
		{
			ProjectID: projects[0].ID,
			UserID:    manager.ID,
			Role:      models.ProjectRoleManager,
			IsActive:  true,
		},
		{
			ProjectID: projects[1].ID,
			UserID:    manager.ID,
			Role:      models.ProjectRoleManager,
			IsActive:  true,
		},
		{
			ProjectID: projects[0].ID,
			UserID:    agent.ID,
			Role:      models.ProjectRoleAgent,
			IsActive:  true,
		},
		{
			ProjectID: projects[0].ID,
			UserID:    requester.ID,
			Role:      models.ProjectRoleRequester,
			IsActive:  true,
		},
		{
			ProjectID: projects[0].ID,
			UserID:    observer.ID,
			Role:      models.ProjectRoleObserver,
			IsActive:  true,
		},
	}).Error; err != nil {
		t.Fatal(err)
	}
	nativeService := services.NewAgentNativeService(db)
	projectService, err := services.NewProjectService(db, nativeService)
	if err != nil {
		t.Fatal(err)
	}
	index := &knowledgeHandlerTestIndex{}
	knowledgeService, err := services.NewKnowledgeService(
		db,
		services.KnowledgeServiceDependencies{
			SearchIndex:          index,
			ProjectAuthorization: projectService,
			Events:               nativeService,
			ModelProviders: map[string]services.ModelProvider{
				"handler-provider": knowledgeHandlerTestProvider{},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return knowledgeHandlerTestEnvironment{
		db:               db,
		projectService:   projectService,
		knowledgeService: knowledgeService,
		index:            index,
		operations:       projects[0],
		security:         projects[1],
		manager:          manager,
		agent:            agent,
		requester:        requester,
		observer:         observer,
	}
}

func knowledgeHandlerTestRouter(
	environment knowledgeHandlerTestEnvironment,
	user models.User,
) http.Handler {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	authenticatedUser := func(c *gin.Context) {
		c.Set("user_id", user.ID)
		c.Set("platform_role", user.PlatformRole)
		c.Next()
	}
	handler := NewKnowledgeHandler(environment.knowledgeService)
	projectGroup := router.Group("/api/projects/:projectKey")
	projectGroup.Use(authenticatedUser)
	projectGroup.Use(ProjectScopeMiddleware(
		environment.projectService,
		environment.db,
	))
	handler.RegisterRoutes(projectGroup)
	externalGroup := router.Group("/api/projects/:projectKey")
	externalGroup.Use(authenticatedUser)
	externalGroup.Use(ProjectExternalScopeMiddleware(
		environment.projectService,
		environment.db,
	))
	handler.RegisterExternalRoutes(externalGroup)
	return router
}

func performKnowledgeHandlerRequest[T any](
	t *testing.T,
	router http.Handler,
	method string,
	path string,
	body any,
	wantStatus int,
) (knowledgeHandlerEnvelope[T], string) {
	t.Helper()
	var encoded []byte
	var err error
	switch value := body.(type) {
	case nil:
	case json.RawMessage:
		encoded = append([]byte(nil), value...)
	default:
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != wantStatus {
		t.Fatalf(
			"%s %s status = %d, want %d, body = %s",
			method,
			path,
			recorder.Code,
			wantStatus,
			recorder.Body.String(),
		)
	}
	var response knowledgeHandlerEnvelope[T]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		if wantStatus == http.StatusNotFound {
			return response, recorder.Body.String()
		}
		t.Fatalf("decode response %s: %v", recorder.Body.String(), err)
	}
	return response, recorder.Body.String()
}

func knowledgeHandlerACLIntersects(
	document []models.KnowledgeACLSubject,
	filter []models.KnowledgeACLSubject,
) bool {
	for _, left := range document {
		for _, right := range filter {
			if left == right {
				return true
			}
		}
	}
	return false
}
