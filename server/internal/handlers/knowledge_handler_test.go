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

func TestKnowledgeVersionResponseExposesCreatorClassWithoutIdentity(
	t *testing.T,
) {
	response := newKnowledgeVersionResponse(
		models.KnowledgeArticleVersion{
			CreatedByType: models.ActorTypeServicePrincipal,
			CreatedByID:   "private-principal-id",
		},
	)
	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	if !strings.Contains(
		text,
		`"created_by_type":"service_principal"`,
	) {
		t.Fatalf("creator class missing: %s", text)
	}
	if strings.Contains(text, "private-principal-id") ||
		strings.Contains(text, "created_by_id") {
		t.Fatalf("creator identity leaked: %s", text)
	}
}

func TestKnowledgeSourceResponseDoesNotSerializeRestrictedSnapshotFields(
	t *testing.T,
) {
	response := newKnowledgeSourceResponse(
		services.KnowledgeSourceView{
			Ordinal:        3,
			Kind:           services.KnowledgeSourceAttachment,
			Visibility:     services.KnowledgeSourceRestricted,
			ReferenceLabel: "受限附件来源",
		},
	)
	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, protected := range []string{
		"source_ticket_id",
		"source_attachment_id",
		"ticket_number",
		"ticket_title",
		"attachment_name",
		"attachment_hash",
	} {
		if strings.Contains(text, `"`+protected+`"`) {
			t.Fatalf(
				"restricted knowledge source leaked %q: %s",
				protected,
				text,
			)
		}
	}
	if !strings.Contains(text, `"visibility":"restricted"`) ||
		!strings.Contains(text, `"reference_label":"受限附件来源"`) {
		t.Fatalf(
			"restricted knowledge source lost safe state: %s",
			text,
		)
	}
}

func TestKnowledgeHandlerContractedAuthoredPublishAndSearchFlow(
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

	authored, authoredBody := performKnowledgeHandlerRequest[knowledgeAuthoredResponse](
		t,
		managerRouter,
		http.MethodPost,
		"/api/projects/OPS/knowledge/articles",
		createKnowledgeArticleRequest{
			Key:     "service-recovery",
			Title:   "服务恢复手册",
			Summary: "生产服务恢复知识",
			Markdown: `## 现象

服务恢复前必须确认数据库健康状态。

## 解决步骤

1. 检查数据库连接和错误率。
2. 逐步恢复服务流量。

## 验证

确认请求成功率恢复并持续观察。`,
		},
		http.StatusCreated,
	)
	if strings.Contains(authoredBody, "chronodesk-managed") ||
		strings.Contains(authoredBody, `"object_provider"`) ||
		strings.Contains(authoredBody, `"object_store_id"`) ||
		strings.Contains(authoredBody, `"object_version_id"`) ||
		authored.Data.Receipt.EventID == "" {
		t.Fatalf("authored response leaked storage or missed receipt: %s", authoredBody)
	}
	article := authored.Data.Article
	version := authored.Data.Version
	document, documentBody := performKnowledgeHandlerRequest[knowledgeDocumentResponse](
		t,
		managerRouter,
		http.MethodGet,
		fmt.Sprintf(
			"/api/projects/OPS/knowledge/articles/%s/document?version_id=%s",
			article.ID,
			version.ID,
		),
		nil,
		http.StatusOK,
	)
	if document.Data.Markdown == "" || len(document.Data.Sections) != 3 ||
		strings.Contains(documentBody, `"object_provider"`) ||
		strings.Contains(documentBody, `"object_store_id"`) ||
		strings.Contains(documentBody, `"object_version_id"`) {
		t.Fatalf("authored document response = %+v body=%s", document.Data, documentBody)
	}

	performKnowledgeHandlerRequest[json.RawMessage](
		t,
		managerRouter,
		http.MethodPost,
		"/api/projects/OPS/knowledge/ingestions/not-browser-owned/parsing",
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
		indexState.Data.DocumentCount != 3 {
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

func TestKnowledgeHandlerClassifiesSearchFailuresWithoutModelPolicy(
	t *testing.T,
) {
	for _, test := range []struct {
		name        string
		query       string
		searchErr   error
		wantStatus  int
		wantMessage string
		wantCalls   int
	}{
		{
			name:        "index unavailable",
			query:       "服务恢复",
			searchErr:   services.ErrKnowledgeIndexUnavailable,
			wantStatus:  http.StatusServiceUnavailable,
			wantMessage: "知识索引服务不可用",
			wantCalls:   1,
		},
		{
			name:        "invalid index response",
			query:       "服务恢复",
			searchErr:   errors.New("malformed OpenSearch response"),
			wantStatus:  http.StatusBadGateway,
			wantMessage: "知识索引响应无效",
			wantCalls:   1,
		},
		{
			name:        "invalid human query",
			query:       " ",
			wantStatus:  http.StatusBadRequest,
			wantMessage: "知识参数或引用无效",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			environment := newKnowledgeHandlerTestEnvironment(t)
			environment.index.searchErr = test.searchErr
			router := knowledgeHandlerTestRouter(
				environment,
				environment.agent,
			)

			response, _ := performKnowledgeHandlerRequest[json.RawMessage](
				t,
				router,
				http.MethodPost,
				"/api/projects/OPS/knowledge/searches",
				knowledgeSearchRequest{
					Query: test.query,
					Limit: 5,
				},
				test.wantStatus,
			)
			if response.Msg != test.wantMessage {
				t.Fatalf(
					"search message = %q, want %q",
					response.Msg,
					test.wantMessage,
				)
			}
			if environment.index.searchCalls != test.wantCalls {
				t.Fatalf(
					"search calls = %d, want %d",
					environment.index.searchCalls,
					test.wantCalls,
				)
			}
			if test.wantCalls == 1 &&
				len(environment.index.lastSearch.QueryEmbedding) != 0 {
				t.Fatalf(
					"missing model policy did not use lexical search: %+v",
					environment.index.lastSearch,
				)
			}
		})
	}
}

func TestKnowledgeHandlerDoesNotExposeAdvancedKnowledgeMutationRoutes(
	t *testing.T,
) {
	environment := newKnowledgeHandlerTestEnvironment(t)
	router := knowledgeHandlerTestRouter(environment, environment.manager)
	for _, test := range []struct {
		method string
		path   string
	}{
		{
			method: http.MethodPost,
			path:   "/api/projects/OPS/knowledge/articles/article-id/versions",
		},
		{
			method: http.MethodPost,
			path: "/api/projects/OPS/knowledge/articles/article-id/" +
				"access-grants",
		},
		{
			method: http.MethodPost,
			path: "/api/projects/OPS/knowledge/versions/version-id/" +
				"ingestions",
		},
		{
			method: http.MethodPost,
			path: "/api/projects/OPS/knowledge/citations/citation-id/" +
				"feedback",
		},
		{
			method: http.MethodGet,
			path:   "/api/projects/OPS/knowledge/model-policy",
		},
		{
			method: http.MethodPut,
			path:   "/api/projects/OPS/knowledge/model-policy",
		},
	} {
		t.Run(test.method+"_"+test.path, func(t *testing.T) {
			performKnowledgeHandlerRequest[json.RawMessage](
				t,
				router,
				test.method,
				test.path,
				nil,
				http.StatusNotFound,
			)
		})
	}
}

func TestKnowledgeHandlerDirectoriesAreStrictSafeAndManagerOnly(t *testing.T) {
	environment := newKnowledgeHandlerTestEnvironment(t)
	managerRouter := knowledgeHandlerTestRouter(
		environment,
		environment.manager,
	)
	agentRouter := knowledgeHandlerTestRouter(
		environment,
		environment.agent,
	)
	created, _ := performKnowledgeHandlerRequest[knowledgeAuthoredResponse](
		t,
		managerRouter,
		http.MethodPost,
		"/api/projects/OPS/knowledge/articles",
		createKnowledgeArticleRequest{
			Key:      "directory-contract",
			Title:    "目录契约",
			Summary:  "安全列表",
			Markdown: "## 现象\n\n目录契约正文。",
		},
		http.StatusCreated,
	)
	type articlePage struct {
		Items      []knowledgeArticleResponse `json:"items"`
		Total      int64                      `json:"total"`
		Page       int                        `json:"page"`
		PageSize   int                        `json:"page_size"`
		TotalPages int                        `json:"total_pages"`
	}
	page, body := performKnowledgeHandlerRequest[articlePage](
		t,
		managerRouter,
		http.MethodGet,
		"/api/projects/OPS/knowledge/articles?page=1&page_size=25&sort_by=updated_at&sort_order=desc&view=manage",
		nil,
		http.StatusOK,
	)
	if page.Data.Total != 1 || len(page.Data.Items) != 1 ||
		page.Data.Page != 1 || page.Data.PageSize != 25 ||
		page.Data.TotalPages != 1 {
		t.Fatalf("article directory = %+v", page.Data)
	}
	for _, forbidden := range []string{
		"organization_id",
		"project_id",
		"created_by_id",
		"updated_by_id",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("article directory exposed %q: %s", forbidden, body)
		}
	}
	for _, path := range []string{
		"/api/projects/OPS/knowledge/articles?page=0",
		"/api/projects/OPS/knowledge/articles?page_size=101",
		"/api/projects/OPS/knowledge/articles?page_size=",
		"/api/projects/OPS/knowledge/articles?page=1&page=2",
		"/api/projects/OPS/knowledge/articles?unknown=value",
		"/api/projects/OPS/knowledge/articles?status=unknown",
		"/api/projects/OPS/knowledge/articles?view=unknown",
		"/api/projects/OPS/knowledge/articles/" + created.Data.Article.ID +
			"/versions?page_size=101",
		"/api/projects/OPS/knowledge/articles/" + created.Data.Article.ID +
			"/versions?virus_scan=unknown",
		"/api/projects/OPS/knowledge/ingestions?page_size=101",
		"/api/projects/OPS/knowledge/ingestions?status=unknown",
	} {
		performKnowledgeHandlerRequest[json.RawMessage](
			t,
			managerRouter,
			http.MethodGet,
			path,
			nil,
			http.StatusBadRequest,
		)
	}
	type readerArticlePage struct {
		Items []knowledgeArticleResponse `json:"items"`
		Total int64                      `json:"total"`
	}
	readerPage, _ := performKnowledgeHandlerRequest[readerArticlePage](
		t,
		agentRouter,
		http.MethodGet,
		"/api/projects/OPS/knowledge/articles?page=1&page_size=25",
		nil,
		http.StatusOK,
	)
	if readerPage.Data.Total != 0 || len(readerPage.Data.Items) != 0 {
		t.Fatalf("unpublished article leaked to project reader: %+v", readerPage.Data)
	}
	for _, path := range []string{
		"/api/projects/OPS/knowledge/articles/" +
			created.Data.Article.ID + "/versions",
		"/api/projects/OPS/knowledge/ingestions",
	} {
		performKnowledgeHandlerRequest[json.RawMessage](
			t,
			agentRouter,
			http.MethodGet,
			path,
			nil,
			http.StatusForbidden,
		)
	}
}

func TestKnowledgeHandlerExplicitContributorCanManageOnlyOwnDrafts(
	t *testing.T,
) {
	environment := newKnowledgeHandlerTestEnvironment(t)
	if err := environment.db.Model(&models.ProjectMembership{}).
		Where(
			"project_id = ? AND user_id = ?",
			environment.operations.ID,
			environment.agent.ID,
		).
		Updates(map[string]any{
			"knowledge_contributor": true,
			"version":               gorm.Expr("version + 1"),
		}).Error; err != nil {
		t.Fatal(err)
	}
	contributorRouter := knowledgeHandlerTestRouter(
		environment,
		environment.agent,
	)
	created, _ := performKnowledgeHandlerRequest[knowledgeAuthoredResponse](
		t,
		contributorRouter,
		http.MethodPost,
		"/api/projects/OPS/knowledge/articles",
		createKnowledgeArticleRequest{
			Key:      "agent-contribution",
			Title:    "处理人提交的排障草稿",
			Markdown: "## 现象\n\n连接失败。\n\n## 处理\n\n复核配置。",
		},
		http.StatusCreated,
	)
	if created.Data.Version.Status != models.KnowledgeVersionDraft {
		t.Fatalf("contributor version = %+v", created.Data.Version)
	}
	type articlePage struct {
		Items []knowledgeArticleResponse `json:"items"`
		Total int64                      `json:"total"`
	}
	browse, _ := performKnowledgeHandlerRequest[articlePage](
		t,
		contributorRouter,
		http.MethodGet,
		"/api/projects/OPS/knowledge/articles?page=1&page_size=25",
		nil,
		http.StatusOK,
	)
	if browse.Data.Total != 0 {
		t.Fatalf("contributor draft leaked into browse: %+v", browse.Data)
	}
	mine, _ := performKnowledgeHandlerRequest[articlePage](
		t,
		contributorRouter,
		http.MethodGet,
		"/api/projects/OPS/knowledge/articles?page=1&page_size=25&view=mine",
		nil,
		http.StatusOK,
	)
	if mine.Data.Total != 1 ||
		len(mine.Data.Items) != 1 ||
		mine.Data.Items[0].ID != created.Data.Article.ID ||
		mine.Data.Items[0].HasUnpublishedDraft == nil ||
		!*mine.Data.Items[0].HasUnpublishedDraft ||
		mine.Data.Items[0].LatestDraftAt == nil ||
		mine.Data.Items[0].LatestDraftVersion == nil ||
		*mine.Data.Items[0].LatestDraftVersion != 1 {
		t.Fatalf("contributor personal view = %+v", mine.Data)
	}
	performKnowledgeHandlerRequest[json.RawMessage](
		t,
		contributorRouter,
		http.MethodGet,
		"/api/projects/OPS/knowledge/articles?view=manage",
		nil,
		http.StatusForbidden,
	)
	performKnowledgeHandlerRequest[json.RawMessage](
		t,
		contributorRouter,
		http.MethodGet,
		"/api/projects/OPS/knowledge/articles?view=invalid",
		nil,
		http.StatusBadRequest,
	)
	revised, _ := performKnowledgeHandlerRequest[knowledgeAuthoredResponse](
		t,
		contributorRouter,
		http.MethodPost,
		"/api/projects/OPS/knowledge/articles/"+
			created.Data.Article.ID+"/drafts",
		createKnowledgeArticleDraftRequest{
			Title:    "处理人补充后的草稿",
			Markdown: "## 补充\n\n复核前补充验证步骤。",
		},
		http.StatusCreated,
	)
	if revised.Data.Version.Version != 2 {
		t.Fatalf("contributor revision = %+v", revised.Data.Version)
	}
	latestDraft, _ := performKnowledgeHandlerRequest[knowledgeDocumentResponse](
		t,
		contributorRouter,
		http.MethodGet,
		"/api/projects/OPS/knowledge/articles/"+
			created.Data.Article.ID+
			"/document?prefer_latest_draft=true",
		nil,
		http.StatusOK,
	)
	if latestDraft.Data.Version.ID != revised.Data.Version.ID ||
		!strings.Contains(
			latestDraft.Data.Markdown,
			"复核前补充验证步骤",
		) {
		t.Fatalf("contributor latest draft = %+v", latestDraft.Data)
	}
	performKnowledgeHandlerRequest[json.RawMessage](
		t,
		contributorRouter,
		http.MethodGet,
		"/api/projects/OPS/knowledge/articles/"+
			created.Data.Article.ID+
			"/document?version_id="+revised.Data.Version.ID+
			"&prefer_latest_draft=true",
		nil,
		http.StatusBadRequest,
	)
	performKnowledgeHandlerRequest[json.RawMessage](
		t,
		contributorRouter,
		http.MethodPost,
		"/api/projects/OPS/knowledge/versions/"+
			revised.Data.Version.ID+"/publication",
		nil,
		http.StatusForbidden,
	)
	performKnowledgeHandlerRequest[json.RawMessage](
		t,
		contributorRouter,
		http.MethodPost,
		"/api/projects/OPS/knowledge/articles",
		json.RawMessage(`{
			"key":"agent-public-grant",
			"title":"不允许自行指定发布范围",
			"markdown":"## body\n",
			"grant_project_access":true
		}`),
		http.StatusBadRequest,
	)
}

func TestKnowledgeHandlerRejectsScopeAndActorFields(
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
}

type knowledgeHandlerTestIndex struct {
	documents   []services.HybridIndexDocument
	lastSearch  services.HybridSearchRequest
	searchCalls int
	searchErr   error
}

func (index *knowledgeHandlerTestIndex) Search(
	_ context.Context,
	request services.HybridSearchRequest,
) ([]services.HybridSearchHit, error) {
	if err := request.Filter.Validate(); err != nil {
		return nil, err
	}
	index.searchCalls++
	index.lastSearch = request
	if index.searchErr != nil {
		return nil, index.searchErr
	}
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

func (index *knowledgeHandlerTestIndex) ReplaceProjectBatches(
	ctx context.Context,
	_ services.HybridIndexReplacement,
	source services.HybridIndexBatchSource,
) error {
	index.documents = nil
	for {
		batch, err := source(ctx)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		index.documents = append(index.documents, batch...)
	}
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
	request services.ModelEmbedRequest,
) (services.ModelEmbedResponse, error) {
	embeddings := make([][]float32, 0, len(request.Inputs))
	for range request.Inputs {
		embeddings = append(embeddings, []float32{0.1, 0.2})
	}
	return services.ModelEmbedResponse{
		Embeddings: embeddings,
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
		&models.KnowledgeObjectWriteIntent{},
		&models.KnowledgeSourceLink{},
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
	storage, err := services.NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	knowledgeService, err := services.NewKnowledgeService(
		db,
		services.KnowledgeServiceDependencies{
			SearchIndex:          index,
			ProjectAuthorization: projectService,
			Events:               nativeService,
			ModelProviders: map[string]services.ModelProvider{
				"handler-provider": knowledgeHandlerTestProvider{},
			},
			AttachmentStorage: storage,
			StorageBucket:     "chronodesk-managed",
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
