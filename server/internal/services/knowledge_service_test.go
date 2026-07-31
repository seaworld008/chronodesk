package services

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestKnowledgeIngestionBlocksUnscannedFilesAndRebuildsScopedIndex(
	t *testing.T,
) {
	db := newKnowledgeServiceTestDB(t)
	scope := models.ProjectScope{OrganizationID: 1, ProjectID: 10}
	otherScope := models.ProjectScope{OrganizationID: 1, ProjectID: 20}
	seedKnowledgeSearchAuthorization(t, db, scope)
	ctx := knowledgeServiceTestContext(t, scope)
	workerCtx := knowledgeServiceTestWorkerContext(t, scope)
	index := &knowledgeServiceTestIndex{}
	provider := &knowledgeServiceTestProvider{
		descriptor: ModelProviderDescriptor{
			Key:        "approved-external",
			IsExternal: false,
		},
	}
	service := newKnowledgeServiceForTest(
		t,
		db,
		index,
		nil,
		map[string]ModelProvider{
			"approved-external": provider,
		},
	)
	setKnowledgeServiceTestPolicy(
		t,
		service,
		ctx,
		models.ModelDataEgressDenied,
	)

	article, err := service.CreateArticle(ctx, CreateKnowledgeArticleInput{
		Key:                "database-recovery",
		Title:              "数据库恢复手册",
		GrantProjectAccess: true,
	})
	if err != nil {
		t.Fatalf("create article: %v", err)
	}
	version, err := service.CreateVersion(
		ctx,
		article.ID,
		CreateKnowledgeVersionInput{
			Title: "数据库恢复手册 v1",
			Source: models.KnowledgeObjectReference{
				Provider:    "s3",
				Bucket:      "knowledge",
				Key:         "projects/10/database-recovery.pdf",
				VersionID:   "object-v1",
				FileName:    "database-recovery.pdf",
				MimeType:    "application/pdf",
				SizeBytes:   4096,
				ContentHash: strings.Repeat("a", 64),
			},
		},
	)
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	if version.ObjectKey == "" || version.ObjectReference().Key == "" {
		t.Fatalf("version did not retain object reference: %+v", version)
	}
	task, err := service.QueueIngestion(ctx, version.ID, "pdf")
	if err != nil {
		t.Fatalf("queue ingestion: %v", err)
	}
	if _, err := service.MarkVersionVirusScan(
		ctx,
		version.ID,
		models.VirusScanClean,
		"forged browser result",
	); !errors.Is(err, ErrKnowledgeWorkerRequired) {
		t.Fatalf("human forged virus scan error = %v", err)
	}
	if _, err := service.StartParsing(
		workerCtx,
		task.ID,
	); !errors.Is(err, ErrKnowledgeVirusScanRequired) {
		t.Fatalf("unscanned parsing error = %v", err)
	}
	var chunkCount int64
	if err := db.Model(&models.KnowledgeChunk{}).Count(&chunkCount).Error; err != nil {
		t.Fatal(err)
	}
	if chunkCount != 0 {
		t.Fatalf("chunks created before clean scan = %d", chunkCount)
	}

	if _, err := service.MarkVersionVirusScan(
		workerCtx,
		version.ID,
		models.VirusScanClean,
		"scanner clean",
	); err != nil {
		t.Fatalf("mark clean: %v", err)
	}
	if _, err := service.StartParsing(workerCtx, task.ID); err != nil {
		t.Fatalf("start parsing: %v", err)
	}
	page := 3
	chunks, err := service.StoreChunks(
		workerCtx,
		task.ID,
		[]KnowledgeChunkInput{
			{
				PageNumber:  &page,
				SectionPath: "恢复/验证",
				Content:     "恢复服务前必须检查数据库健康状态。",
				Snippet:     "恢复服务前必须检查数据库健康状态。",
				TokenCount:  16,
			},
		},
	)
	if err != nil {
		t.Fatalf("store chunks: %v", err)
	}
	if _, err := service.CompleteIngestion(workerCtx, task.ID); err != nil {
		t.Fatalf("complete ingestion: %v", err)
	}
	published, err := service.PublishVersion(ctx, version.ID)
	if err != nil {
		t.Fatalf("publish version: %v", err)
	}
	if published.Status != models.KnowledgeVersionPublished {
		t.Fatalf("published status = %q", published.Status)
	}

	var state models.KnowledgeIndexState
	if err := db.Where(
		"organization_id = ? AND project_id = ? AND index_name = ?",
		scope.OrganizationID,
		scope.ProjectID,
		"knowledge",
	).Take(&state).Error; err != nil {
		t.Fatalf("load automatically queued index state: %v", err)
	}
	if state.Status != models.KnowledgeIndexRebuildRequested ||
		state.Generation != 0 ||
		state.DesiredGeneration != 1 {
		t.Fatalf("automatically queued index state = %+v", state)
	}
	var automaticDelivery models.OutboxDelivery
	if err := db.Where(
		"organization_id = ? AND project_id = ? AND destination_type = ?",
		scope.OrganizationID,
		scope.ProjectID,
		KnowledgeIndexRebuildOutboxDestination,
	).Take(&automaticDelivery).Error; err != nil {
		t.Fatalf("load automatic rebuild outbox delivery: %v", err)
	}
	if automaticDelivery.DestinationID != fmt.Sprintf("%s:1", state.ID) {
		t.Fatalf(
			"automatic rebuild destination = %q",
			automaticDelivery.DestinationID,
		)
	}
	if err := service.ExecuteIndexRebuildOutbox(
		workerCtx,
		state.ID,
		state.DesiredGeneration,
	); err != nil {
		t.Fatalf("execute automatically queued index rebuild: %v", err)
	}

	queuedState, err := service.RebuildIndex(ctx)
	if err != nil {
		t.Fatalf("rebuild index: %v", err)
	}
	if queuedState.Status != models.KnowledgeIndexRebuildRequested ||
		queuedState.Generation != 1 ||
		queuedState.DesiredGeneration != 2 {
		t.Fatalf("queued index state = %+v", queuedState)
	}
	if err := service.ExecuteIndexRebuildOutbox(
		workerCtx,
		queuedState.ID,
		queuedState.DesiredGeneration,
	); err != nil {
		t.Fatalf("execute queued index rebuild: %v", err)
	}
	if err := db.First(&state, "id = ?", queuedState.ID).Error; err != nil {
		t.Fatal(err)
	}
	if state.Status != models.KnowledgeIndexReady ||
		state.Generation != 2 ||
		state.DocumentCount != 1 {
		t.Fatalf("completed index state = %+v", state)
	}
	if index.replacement.OrganizationID != scope.OrganizationID ||
		index.replacement.ProjectID != scope.ProjectID ||
		len(index.replacement.Documents) != 1 {
		t.Fatalf("index replacement = %+v", index.replacement)
	}
	document := index.replacement.Documents[0]
	if document.VersionID != version.ID ||
		document.DocumentVersion != version.Version ||
		document.ChunkID != chunks[0].ID ||
		document.PageNumber == nil ||
		*document.PageNumber != page ||
		document.ContentHash != chunks[0].ContentHash ||
		len(document.Embedding) != 3 ||
		!knowledgeTestHasSubject(
			document.ACLSubjects,
			models.KnowledgeACLAllProject,
			"*",
		) {
		t.Fatalf("rebuilt document = %+v", document)
	}

	if _, err := service.CreateVersion(
		knowledgeServiceTestContext(t, otherScope),
		article.ID,
		CreateKnowledgeVersionInput{
			Title: "跨项目版本",
			Source: models.KnowledgeObjectReference{
				Provider:    "s3",
				Bucket:      "knowledge",
				Key:         "projects/20/forbidden.pdf",
				FileName:    "forbidden.pdf",
				MimeType:    "application/pdf",
				SizeBytes:   100,
				ContentHash: strings.Repeat("b", 64),
			},
		},
	); !errors.Is(err, ErrKnowledgeNotFound) {
		t.Fatalf("cross-project version error = %v", err)
	}
}

func TestKnowledgeInfectedDocumentIsQuarantinedBeforeParsing(
	t *testing.T,
) {
	db := newKnowledgeServiceTestDB(t)
	scope := models.ProjectScope{OrganizationID: 1, ProjectID: 10}
	ctx := knowledgeServiceTestContext(t, scope)
	workerCtx := knowledgeServiceTestWorkerContext(t, scope)
	service := newKnowledgeServiceForTest(
		t,
		db,
		&knowledgeServiceTestIndex{},
		nil,
		nil,
	)
	article, err := service.CreateArticle(ctx, CreateKnowledgeArticleInput{
		Key:   "infected",
		Title: "不安全文档",
	})
	if err != nil {
		t.Fatal(err)
	}
	version, err := service.CreateVersion(
		ctx,
		article.ID,
		CreateKnowledgeVersionInput{
			Title: "不安全文档 v1",
			Source: models.KnowledgeObjectReference{
				Provider:    "s3",
				Bucket:      "knowledge",
				Key:         "projects/10/infected.pdf",
				FileName:    "infected.pdf",
				MimeType:    "application/pdf",
				SizeBytes:   100,
				ContentHash: strings.Repeat("c", 64),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.QueueIngestion(ctx, version.ID, "pdf")
	if err != nil {
		t.Fatal(err)
	}
	version, err = service.MarkVersionVirusScan(
		workerCtx,
		version.ID,
		models.VirusScanInfected,
		"malware detected",
	)
	if err != nil {
		t.Fatal(err)
	}
	if version.Status != models.KnowledgeVersionQuarantined {
		t.Fatalf("infected version status = %q", version.Status)
	}
	if _, err := service.StartParsing(
		workerCtx,
		task.ID,
	); !errors.Is(err, ErrKnowledgeIngestionState) {
		t.Fatalf("quarantined parsing error = %v", err)
	}
	if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != models.KnowledgeIngestionQuarantined {
		t.Fatalf("infected task status = %q", task.Status)
	}
}

func TestKnowledgeSearchPushesScopeAndACLBeforeRerankAndReturnsCitations(
	t *testing.T,
) {
	db := newKnowledgeServiceTestDB(t)
	scope := models.ProjectScope{OrganizationID: 7, ProjectID: 70}
	seedKnowledgeSearchAuthorization(t, db, scope)
	ctx := knowledgeServiceTestContext(t, scope)
	callOrder := make([]string, 0, 3)
	page := 8
	hit := HybridSearchHit{
		OrganizationID:  scope.OrganizationID,
		ProjectID:       scope.ProjectID,
		ArticleID:       uuid.Must(uuid.NewV7()).String(),
		VersionID:       uuid.Must(uuid.NewV7()).String(),
		DocumentVersion: 3,
		ChunkID:         uuid.Must(uuid.NewV7()).String(),
		PageNumber:      &page,
		Snippet:         "联系 secret@example.test 执行数据库恢复。",
		ContentHash:     strings.Repeat("d", 64),
		Score:           0.72,
		TokenCount:      15,
	}
	seedKnowledgeSearchHit(t, db, scope, hit)
	index := &knowledgeServiceTestIndex{
		calls: &callOrder,
		hits:  []HybridSearchHit{hit},
	}
	provider := &knowledgeServiceTestProvider{
		descriptor: ModelProviderDescriptor{
			Key:        "approved-external",
			IsExternal: true,
		},
		calls: &callOrder,
	}
	resolver := &knowledgeServiceTestAccessResolver{
		subjects: []models.KnowledgeACLSubject{
			{
				Type: models.KnowledgeACLProjectRole,
				ID:   string(models.ProjectRoleManager),
			},
		},
	}
	service := newKnowledgeServiceForTest(
		t,
		db,
		index,
		resolver,
		map[string]ModelProvider{
			"approved-external": provider,
		},
	)
	setKnowledgeServiceTestPolicy(
		t,
		service,
		ctx,
		models.ModelDataEgressRedacted,
	)

	result, err := service.Search(ctx, KnowledgeSearchInput{
		Query: "请联系 secret@example.test 查找恢复步骤",
		Limit: 1,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !reflect.DeepEqual(callOrder, []string{"embed", "search", "rerank"}) {
		t.Fatalf("call order = %#v", callOrder)
	}
	filter := index.searchRequest.Filter
	if filter.OrganizationID != scope.OrganizationID ||
		filter.ProjectID != scope.ProjectID ||
		!filter.PublishedOnly ||
		filter.VirusScan != models.VirusScanClean {
		t.Fatalf("backend filter = %+v", filter)
	}
	for _, expected := range []models.KnowledgeACLSubject{
		{Type: models.KnowledgeACLAllProject, ID: "*"},
		{Type: models.KnowledgeACLHuman, ID: "42"},
		{
			Type: models.KnowledgeACLProjectRole,
			ID:   string(models.ProjectRoleManager),
		},
	} {
		if !knowledgeTestHasSubject(
			filter.ACLSubjects,
			expected.Type,
			expected.ID,
		) {
			t.Errorf("backend ACL filter lacks %+v: %+v", expected, filter.ACLSubjects)
		}
	}
	if strings.Contains(provider.embedRequest.Inputs[0], "secret@example.test") ||
		strings.Contains(
			provider.rerankRequest.Candidates[0].Content,
			"secret@example.test",
		) {
		t.Fatal("external provider received unredacted content")
	}
	if provider.embedRequest.Limits.RequestsPerMinute != 30 ||
		provider.rerankRequest.Limits.TokensPerMinute != 10000 {
		t.Fatalf(
			"model limits were not propagated: embed=%+v rerank=%+v",
			provider.embedRequest.Limits,
			provider.rerankRequest.Limits,
		)
	}
	if len(result.Items) != 1 {
		t.Fatalf("search result = %+v", result)
	}
	citation := result.Items[0]
	if citation.VersionID != hit.VersionID ||
		citation.DocumentVersion != hit.DocumentVersion ||
		citation.PageNumber == nil ||
		*citation.PageNumber != page ||
		citation.Snippet != hit.Snippet ||
		citation.ContentHash != hit.ContentHash {
		t.Fatalf("citation = %+v", citation)
	}
	feedback, err := service.RecordFeedback(
		ctx,
		citation.ID,
		models.KnowledgeFeedbackHelpful,
		"引用准确",
	)
	if err != nil {
		t.Fatalf("record feedback: %v", err)
	}
	if feedback.OrganizationID != scope.OrganizationID ||
		feedback.ProjectID != scope.ProjectID ||
		feedback.CitationID != citation.ID {
		t.Fatalf("feedback = %+v", feedback)
	}
	otherContext := knowledgeServiceTestContext(
		t,
		models.ProjectScope{
			OrganizationID: scope.OrganizationID,
			ProjectID:      scope.ProjectID + 1,
		},
	)
	if _, err := service.RecordFeedback(
		otherContext,
		citation.ID,
		models.KnowledgeFeedbackHelpful,
		"",
	); !errors.Is(err, ErrKnowledgeNotFound) {
		t.Fatalf("cross-project feedback error = %v", err)
	}
}

func TestKnowledgeSearchFailsClosedOnIndexBoundaryViolationBeforeRerank(
	t *testing.T,
) {
	db := newKnowledgeServiceTestDB(t)
	scope := models.ProjectScope{OrganizationID: 7, ProjectID: 70}
	seedKnowledgeSearchAuthorization(t, db, scope)
	ctx := knowledgeServiceTestContext(t, scope)
	index := &knowledgeServiceTestIndex{
		hits: []HybridSearchHit{
			{
				OrganizationID:  scope.OrganizationID,
				ProjectID:       scope.ProjectID + 1,
				ArticleID:       uuid.Must(uuid.NewV7()).String(),
				VersionID:       uuid.Must(uuid.NewV7()).String(),
				DocumentVersion: 1,
				ChunkID:         uuid.Must(uuid.NewV7()).String(),
				Snippet:         "越界命中",
				ContentHash:     strings.Repeat("e", 64),
				TokenCount:      2,
			},
		},
	}
	provider := &knowledgeServiceTestProvider{
		descriptor: ModelProviderDescriptor{
			Key:        "approved-external",
			IsExternal: true,
		},
	}
	service := newKnowledgeServiceForTest(
		t,
		db,
		index,
		nil,
		map[string]ModelProvider{
			"approved-external": provider,
		},
	)
	setKnowledgeServiceTestPolicy(
		t,
		service,
		ctx,
		models.ModelDataEgressAllowed,
	)

	_, err := service.Search(ctx, KnowledgeSearchInput{
		Query: "越界测试",
		Limit: 1,
	})
	if !errors.Is(err, ErrKnowledgeIndexBoundaryViolation) {
		t.Fatalf("boundary violation error = %v", err)
	}
	if provider.rerankCalls != 0 {
		t.Fatalf("rerank called after boundary violation = %d", provider.rerankCalls)
	}
	if index.searchCalls != 1 ||
		index.searchRequest.Filter.OrganizationID != scope.OrganizationID ||
		index.searchRequest.Filter.ProjectID != scope.ProjectID ||
		len(index.searchRequest.Filter.ACLSubjects) == 0 {
		t.Fatalf("backend search did not receive mandatory filters: %+v", index.searchRequest)
	}
}

func TestKnowledgeSearchDiscardsExternalResultAfterMembershipRevocation(
	t *testing.T,
) {
	db := newKnowledgeServiceTestDB(t)
	scope := models.ProjectScope{OrganizationID: 7, ProjectID: 70}
	seedKnowledgeSearchAuthorization(t, db, scope)
	ctx := knowledgeServiceTestContext(t, scope)
	index := &knowledgeServiceTestIndex{
		afterSearch: func(externalContext context.Context) {
			if scopeddb.HasTransaction(externalContext) {
				t.Fatal("knowledge search index ran inside a project transaction")
			}
			if err := db.Model(&models.ProjectMembership{}).
				Where("project_id = ? AND user_id = ?", scope.ProjectID, 42).
				Updates(map[string]any{
					"is_active": false,
					"version":   gorm.Expr("version + 1"),
				}).Error; err != nil {
				t.Fatalf("revoke membership after external search: %v", err)
			}
		},
	}
	provider := &knowledgeServiceTestProvider{
		descriptor: ModelProviderDescriptor{
			Key:        "approved-external",
			IsExternal: true,
		},
	}
	service := newKnowledgeServiceForTest(
		t,
		db,
		index,
		nil,
		map[string]ModelProvider{
			"approved-external": provider,
		},
	)
	setKnowledgeServiceTestPolicy(
		t,
		service,
		ctx,
		models.ModelDataEgressRedacted,
	)

	_, err := service.Search(ctx, KnowledgeSearchInput{
		Query: "撤权后不能返回",
		Limit: 1,
	})
	if !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf("post-search revocation error = %v", err)
	}
	var citations int64
	if err := db.Model(&models.KnowledgeCitation{}).
		Count(&citations).Error; err != nil {
		t.Fatal(err)
	}
	if citations != 0 {
		t.Fatalf("revoked knowledge search persisted %d citations", citations)
	}
}

func TestKnowledgeSearchDeniesExternalProviderWhenDataEgressIsDisabled(
	t *testing.T,
) {
	db := newKnowledgeServiceTestDB(t)
	scope := models.ProjectScope{OrganizationID: 7, ProjectID: 70}
	seedKnowledgeSearchAuthorization(t, db, scope)
	ctx := knowledgeServiceTestContext(t, scope)
	index := &knowledgeServiceTestIndex{}
	provider := &knowledgeServiceTestProvider{
		descriptor: ModelProviderDescriptor{
			Key:        "approved-external",
			IsExternal: true,
		},
	}
	service := newKnowledgeServiceForTest(
		t,
		db,
		index,
		nil,
		map[string]ModelProvider{
			"approved-external": provider,
		},
	)
	setKnowledgeServiceTestPolicy(
		t,
		service,
		ctx,
		models.ModelDataEgressDenied,
	)
	if _, err := service.Search(ctx, KnowledgeSearchInput{
		Query: "不得外发",
		Limit: 1,
	}); !errors.Is(err, ErrKnowledgeModelPolicyDenied) {
		t.Fatalf("egress denial error = %v", err)
	}
	if provider.embedCalls != 0 || index.searchCalls != 0 {
		t.Fatalf(
			"provider/index called after egress denial: embed=%d search=%d",
			provider.embedCalls,
			index.searchCalls,
		)
	}
}

func TestSetProjectModelPolicyRequiresRegisteredProviderAndScopesUpdates(
	t *testing.T,
) {
	db := newKnowledgeServiceTestDB(t)
	scope := models.ProjectScope{OrganizationID: 8, ProjectID: 80}
	otherScope := models.ProjectScope{OrganizationID: 8, ProjectID: 81}
	provider := &knowledgeServiceTestProvider{
		descriptor: ModelProviderDescriptor{
			Key:        "approved-local",
			IsExternal: false,
		},
	}
	service := newKnowledgeServiceForTest(
		t,
		db,
		nil,
		nil,
		map[string]ModelProvider{"approved-local": provider},
	)
	input := ProjectModelPolicyInput{
		PolicyKey:      "knowledge",
		ProviderKey:    "unregistered",
		GenerateModel:  "generate-v1",
		EmbeddingModel: "embed-v1",
		RerankModel:    "rerank-v1",
		DataEgress:     models.ModelDataEgressDenied,
		ProviderAllowlist: []string{
			"unregistered",
		},
		ModelAllowlist: []string{
			"generate-v1",
			"embed-v1",
			"rerank-v1",
		},
	}
	if _, err := service.SetProjectModelPolicy(
		knowledgeServiceTestContext(t, scope),
		input,
	); !errors.Is(err, ErrKnowledgeModelPolicyDenied) {
		t.Fatalf("unregistered provider error = %v", err)
	}
	var count int64
	if err := db.Model(&models.ProjectModelPolicy{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("unregistered provider persisted %d policies", count)
	}

	input.ProviderKey = "approved-local"
	input.ProviderAllowlist = []string{"approved-local"}
	first, err := service.SetProjectModelPolicy(
		knowledgeServiceTestContext(t, scope),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.SetProjectModelPolicy(
		knowledgeServiceTestContext(t, otherScope),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	input.MonthlyTokenBudget = 12345
	updated, err := service.SetProjectModelPolicy(
		knowledgeServiceTestContext(t, scope),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != first.ID || updated.MonthlyTokenBudget != 12345 {
		t.Fatalf("unexpected scoped policy update: %+v", updated)
	}
	var untouched models.ProjectModelPolicy
	if err := db.Where("id = ?", other.ID).First(&untouched).Error; err != nil {
		t.Fatal(err)
	}
	if untouched.MonthlyTokenBudget != 0 {
		t.Fatalf("other project policy was modified: %+v", untouched)
	}
}

type knowledgeServiceTestIndex struct {
	calls         *[]string
	hits          []HybridSearchHit
	afterSearch   func(context.Context)
	searchRequest HybridSearchRequest
	searchCalls   int
	replacement   HybridIndexReplacement
	replaceErr    error
}

func (index *knowledgeServiceTestIndex) Search(
	ctx context.Context,
	request HybridSearchRequest,
) ([]HybridSearchHit, error) {
	index.searchCalls++
	index.searchRequest = request
	if index.calls != nil {
		*index.calls = append(*index.calls, "search")
	}
	if err := request.Filter.Validate(); err != nil {
		return nil, err
	}
	if index.afterSearch != nil {
		index.afterSearch(ctx)
	}
	return append([]HybridSearchHit(nil), index.hits...), nil
}

func (index *knowledgeServiceTestIndex) ReplaceProject(
	_ context.Context,
	replacement HybridIndexReplacement,
) error {
	index.replacement = replacement
	return index.replaceErr
}

type knowledgeServiceTestProvider struct {
	descriptor    ModelProviderDescriptor
	calls         *[]string
	embedRequest  ModelEmbedRequest
	rerankRequest ModelRerankRequest
	embedCalls    int
	rerankCalls   int
}

func (provider *knowledgeServiceTestProvider) Descriptor() ModelProviderDescriptor {
	return provider.descriptor
}

func (provider *knowledgeServiceTestProvider) Generate(
	_ context.Context,
	_ ModelGenerateRequest,
) (ModelGenerateResponse, error) {
	return ModelGenerateResponse{Text: "unused"}, nil
}

func (provider *knowledgeServiceTestProvider) Embed(
	_ context.Context,
	request ModelEmbedRequest,
) (ModelEmbedResponse, error) {
	provider.embedCalls++
	provider.embedRequest = request
	if provider.calls != nil {
		*provider.calls = append(*provider.calls, "embed")
	}
	return ModelEmbedResponse{
		Embeddings: [][]float32{{0.1, 0.2, 0.3}},
		Usage:      ModelUsage{InputTokens: 10},
	}, nil
}

func (provider *knowledgeServiceTestProvider) Rerank(
	_ context.Context,
	request ModelRerankRequest,
) (ModelRerankResponse, error) {
	provider.rerankCalls++
	provider.rerankRequest = request
	if provider.calls != nil {
		*provider.calls = append(*provider.calls, "rerank")
	}
	items := make([]ModelRerankItem, 0, request.Limit)
	for index, candidate := range request.Candidates {
		if len(items) >= request.Limit {
			break
		}
		items = append(items, ModelRerankItem{
			ID:    candidate.ID,
			Score: 1 - float64(index)*0.1,
		})
	}
	return ModelRerankResponse{
		Items: items,
		Usage: ModelUsage{InputTokens: 20},
	}, nil
}

type knowledgeServiceTestAccessResolver struct {
	subjects []models.KnowledgeACLSubject
}

func (resolver *knowledgeServiceTestAccessResolver) ResolveKnowledgeSubjects(
	_ context.Context,
	_ models.ProjectScope,
	_ models.ActorRef,
) ([]models.KnowledgeACLSubject, error) {
	return append([]models.KnowledgeACLSubject(nil), resolver.subjects...), nil
}

func newKnowledgeServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.User{},
		&models.ProjectMembership{},
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
	return db
}

func seedKnowledgeSearchAuthorization(
	t *testing.T,
	db *gorm.DB,
	scope models.ProjectScope,
) {
	t.Helper()
	organization := models.Organization{
		ID:     scope.OrganizationID,
		Slug:   fmt.Sprintf("knowledge-org-%d", scope.OrganizationID),
		Name:   "Knowledge Search Organization",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	businessUnit := models.BusinessUnit{
		ID:             scope.ProjectID + 1000,
		OrganizationID: scope.OrganizationID,
		Key:            fmt.Sprintf("knowledge-%d", scope.ProjectID),
		Name:           "Knowledge Search",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&businessUnit).Error; err != nil {
		t.Fatal(err)
	}
	project := models.Project{
		ID:             scope.ProjectID,
		OrganizationID: scope.OrganizationID,
		BusinessUnitID: businessUnit.ID,
		Key:            models.ProjectKey(fmt.Sprintf("K%d", scope.ProjectID)),
		Name:           "Knowledge Search Project",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	user := models.User{
		ID:           42,
		Username:     fmt.Sprintf("knowledge-search-%d", scope.ProjectID),
		Email:        fmt.Sprintf("knowledge-search-%d@example.test", scope.ProjectID),
		PasswordHash: "test-only",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ProjectMembership{
		ProjectID: scope.ProjectID,
		UserID:    user.ID,
		Role:      models.ProjectRoleManager,
		IsActive:  true,
		Version:   1,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func seedKnowledgeSearchHit(
	t *testing.T,
	db *gorm.DB,
	scope models.ProjectScope,
	hit HybridSearchHit,
) {
	t.Helper()
	actor := models.HumanActor(42)
	now := time.Now().UTC()
	article := models.KnowledgeArticle{
		ID:             hit.ArticleID,
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		Key:            "search-hit",
		Title:          "Search hit",
		Status:         models.KnowledgeArticleActive,
		Revision:       1,
		CreatedByType:  actor.Type,
		CreatedByID:    actor.ID,
		UpdatedByType:  actor.Type,
		UpdatedByID:    actor.ID,
	}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	version := models.KnowledgeArticleVersion{
		ID:               hit.VersionID,
		OrganizationID:   scope.OrganizationID,
		ProjectID:        scope.ProjectID,
		ArticleID:        hit.ArticleID,
		Version:          hit.DocumentVersion,
		Status:           models.KnowledgeVersionPublished,
		Title:            "Search hit",
		ObjectProvider:   "test",
		ObjectBucket:     "knowledge",
		ObjectKey:        "search-hit.txt",
		OriginalFileName: "search-hit.txt",
		MimeType:         "text/plain",
		SizeBytes:        int64(len(hit.Snippet)),
		ContentHash:      hit.ContentHash,
		VirusScan:        models.VirusScanClean,
		ScannedAt:        &now,
		CreatedByType:    actor.Type,
		CreatedByID:      actor.ID,
		PublishedAt:      &now,
	}
	if err := db.Create(&version).Error; err != nil {
		t.Fatal(err)
	}
	task := models.KnowledgeIngestionTask{
		ID:             uuid.Must(uuid.NewV7()).String(),
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		ArticleID:      hit.ArticleID,
		VersionID:      hit.VersionID,
		Attempt:        1,
		Status:         models.KnowledgeIngestionCompleted,
		ParserKey:      "test",
		CreatedByType:  actor.Type,
		CreatedByID:    actor.ID,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	chunk := models.KnowledgeChunk{
		ID:              hit.ChunkID,
		OrganizationID:  scope.OrganizationID,
		ProjectID:       scope.ProjectID,
		ArticleID:       hit.ArticleID,
		VersionID:       hit.VersionID,
		IngestionTaskID: task.ID,
		Ordinal:         1,
		PageNumber:      hit.PageNumber,
		Content:         hit.Snippet,
		Snippet:         hit.Snippet,
		ContentHash:     hit.ContentHash,
		TokenCount:      hit.TokenCount,
	}
	if err := db.Create(&chunk).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.KnowledgeArticleACL{
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		ArticleID:      hit.ArticleID,
		SubjectType:    models.KnowledgeACLAllProject,
		SubjectID:      "*",
		Permission:     models.KnowledgeACLRead,
		GrantedByType:  actor.Type,
		GrantedByID:    actor.ID,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func newKnowledgeServiceForTest(
	t *testing.T,
	db *gorm.DB,
	index HybridSearchIndex,
	resolver KnowledgeAccessResolver,
	providers map[string]ModelProvider,
) *KnowledgeService {
	t.Helper()
	service, err := NewKnowledgeService(db, KnowledgeServiceDependencies{
		SearchIndex:    index,
		AccessResolver: resolver,
		ModelProviders: providers,
		Events:         NewAgentNativeService(db),
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func knowledgeServiceTestContext(
	t *testing.T,
	scope models.ProjectScope,
) context.Context {
	t.Helper()
	ctx, err := WithOperationContext(context.Background(), OperationContext{
		Scope:  scope,
		Actor:  models.HumanActor(42),
		Source: SourceProtocolHumanREST,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func knowledgeServiceTestWorkerContext(
	t *testing.T,
	scope models.ProjectScope,
) context.Context {
	t.Helper()
	ctx, err := EnsureSystemProjectOperationContext(
		context.Background(),
		scope,
		models.SystemActor("knowledge-test-worker"),
		"knowledge-test",
		"knowledge-test",
	)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func setKnowledgeServiceTestPolicy(
	t *testing.T,
	service *KnowledgeService,
	ctx context.Context,
	egress models.ModelDataEgressMode,
) {
	t.Helper()
	rules := []models.ModelRedactionRule{}
	if egress == models.ModelDataEgressRedacted {
		rules = []models.ModelRedactionRule{
			{
				Literal:     "secret@example.test",
				Replacement: "[REDACTED]",
			},
		}
	}
	if _, err := service.SetProjectModelPolicy(
		ctx,
		ProjectModelPolicyInput{
			PolicyKey:      "knowledge",
			ProviderKey:    "approved-external",
			GenerateModel:  "generate-v1",
			EmbeddingModel: "embed-v1",
			RerankModel:    "rerank-v1",
			DataEgress:     egress,
			RedactionRules: rules,
			ProviderAllowlist: []string{
				"approved-external",
			},
			ModelAllowlist: []string{
				"generate-v1",
				"embed-v1",
				"rerank-v1",
			},
			MonthlyTokenBudget:      100000,
			MonthlyCostBudgetMicros: 500000,
			RequestsPerMinute:       30,
			TokensPerMinute:         10000,
		},
	); err != nil {
		t.Fatal(err)
	}
}

func knowledgeTestHasSubject(
	subjects []models.KnowledgeACLSubject,
	subjectType models.KnowledgeACLSubjectType,
	id string,
) bool {
	for _, subject := range subjects {
		if subject.Type == subjectType && subject.ID == id {
			return true
		}
	}
	return false
}
