package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

const knowledgeRequestBodyLimit = 8 << 20

type KnowledgeHandler struct {
	service  *services.KnowledgeService
	response *middleware.ResponseHelper
}

func NewKnowledgeHandler(service *services.KnowledgeService) *KnowledgeHandler {
	return &KnowledgeHandler{
		service:  service,
		response: middleware.NewResponseHelper(),
	}
}

// RegisterRoutes mounts project-scoped human knowledge APIs. The supplied
// group must already use ProjectScopeMiddleware.
func (handler *KnowledgeHandler) RegisterRoutes(projectGroup *gin.RouterGroup) {
	knowledge := projectGroup.Group("/knowledge")
	knowledge.POST("/articles", handler.CreateArticle)
	knowledge.POST(
		"/articles/:articleID/versions",
		handler.RegisterVersion,
	)
	knowledge.POST(
		"/articles/:articleID/access-grants",
		handler.GrantArticleAccess,
	)
	knowledge.POST(
		"/versions/:versionID/ingestions",
		handler.QueueIngestion,
	)
	// Virus scan, parsing, chunk persistence, and ingestion completion are
	// worker-only commands. Exposing them on the human project API would let a
	// browser caller forge a clean scan or inject trusted index content.
	knowledge.POST(
		"/versions/:versionID/publication",
		handler.PublishVersion,
	)
	knowledge.POST("/index-rebuilds", handler.RebuildIndex)
	knowledge.POST("/searches", handler.Search)
	knowledge.POST(
		"/citations/:citationID/feedback",
		handler.RecordFeedback,
	)
	knowledge.GET("/model-policy", handler.GetModelPolicy)
	knowledge.PUT("/model-policy", handler.UpdateModelPolicy)
}

type createKnowledgeArticleRequest struct {
	Key                string `json:"key"`
	Title              string `json:"title"`
	Summary            string `json:"summary"`
	GrantProjectAccess bool   `json:"grant_project_access"`
}

func (handler *KnowledgeHandler) CreateArticle(c *gin.Context) {
	if !handler.requireProjectAccess(c, true) {
		return
	}
	var request createKnowledgeArticleRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	article, err := handler.service.CreateArticle(
		c.Request.Context(),
		services.CreateKnowledgeArticleInput{
			Key:                strings.TrimSpace(request.Key),
			Title:              strings.TrimSpace(request.Title),
			Summary:            strings.TrimSpace(request.Summary),
			GrantProjectAccess: request.GrantProjectAccess,
		},
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Created(
		c,
		newKnowledgeArticleResponse(*article),
		"知识文章创建成功",
	)
}

type registerKnowledgeVersionRequest struct {
	Title           string `json:"title"`
	ObjectProvider  string `json:"object_provider"`
	ObjectBucket    string `json:"object_bucket"`
	ObjectKey       string `json:"object_key"`
	ObjectVersionID string `json:"object_version_id"`
	FileName        string `json:"file_name"`
	MimeType        string `json:"mime_type"`
	SizeBytes       int64  `json:"size_bytes"`
	ContentHash     string `json:"content_hash"`
}

func (handler *KnowledgeHandler) RegisterVersion(c *gin.Context) {
	if !handler.requireProjectAccess(c, true) {
		return
	}
	articleID, ok := handler.requiredPathID(
		c,
		"articleID",
		"知识文章标识无效",
	)
	if !ok {
		return
	}
	var request registerKnowledgeVersionRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	if containsObjectStorageURL(request.ObjectProvider) ||
		containsObjectStorageURL(request.ObjectBucket) ||
		containsObjectStorageURL(request.ObjectKey) {
		handler.response.BadRequest(c, "对象存储元数据不得包含 URL")
		return
	}
	version, err := handler.service.CreateVersion(
		c.Request.Context(),
		articleID,
		services.CreateKnowledgeVersionInput{
			Title: strings.TrimSpace(request.Title),
			Source: models.KnowledgeObjectReference{
				Provider:    strings.TrimSpace(request.ObjectProvider),
				Bucket:      strings.TrimSpace(request.ObjectBucket),
				Key:         strings.TrimSpace(request.ObjectKey),
				VersionID:   strings.TrimSpace(request.ObjectVersionID),
				FileName:    strings.TrimSpace(request.FileName),
				MimeType:    strings.TrimSpace(request.MimeType),
				SizeBytes:   request.SizeBytes,
				ContentHash: strings.ToLower(strings.TrimSpace(request.ContentHash)),
			},
		},
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Created(
		c,
		newKnowledgeVersionResponse(*version),
		"知识文件元数据登记成功",
	)
}

type grantKnowledgeAccessRequest struct {
	SubjectType models.KnowledgeACLSubjectType `json:"subject_type"`
	SubjectID   string                         `json:"subject_id"`
	Permission  models.KnowledgeACLPermission  `json:"permission"`
}

func (handler *KnowledgeHandler) GrantArticleAccess(c *gin.Context) {
	if !handler.requireProjectAccess(c, true) {
		return
	}
	articleID, ok := handler.requiredPathID(
		c,
		"articleID",
		"知识文章标识无效",
	)
	if !ok {
		return
	}
	var request grantKnowledgeAccessRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	grant, err := handler.service.GrantArticleAccess(
		c.Request.Context(),
		articleID,
		models.KnowledgeACLSubject{
			Type: request.SubjectType,
			ID:   strings.TrimSpace(request.SubjectID),
		},
		request.Permission,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Created(c, knowledgeACLResponse{
		ID:          grant.ID,
		ArticleID:   grant.ArticleID,
		SubjectType: grant.SubjectType,
		SubjectID:   grant.SubjectID,
		Permission:  grant.Permission,
		CreatedAt:   grant.CreatedAt,
	}, "知识访问授权成功")
}

type queueKnowledgeIngestionRequest struct {
	ParserKey string `json:"parser_key"`
}

func (handler *KnowledgeHandler) QueueIngestion(c *gin.Context) {
	if !handler.requireProjectAccess(c, true) {
		return
	}
	versionID, ok := handler.requiredPathID(
		c,
		"versionID",
		"知识版本标识无效",
	)
	if !ok {
		return
	}
	var request queueKnowledgeIngestionRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	task, err := handler.service.QueueIngestion(
		c.Request.Context(),
		versionID,
		strings.TrimSpace(request.ParserKey),
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Created(
		c,
		newKnowledgeIngestionResponse(*task),
		"知识摄取任务创建成功",
	)
}

type knowledgeScanResultRequest struct {
	Status models.VirusScanStatus `json:"status"`
	Detail string                 `json:"detail"`
}

func (handler *KnowledgeHandler) RecordScanResult(c *gin.Context) {
	if !handler.requireProjectAccess(c, true) {
		return
	}
	versionID, ok := handler.requiredPathID(
		c,
		"versionID",
		"知识版本标识无效",
	)
	if !ok {
		return
	}
	var request knowledgeScanResultRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	version, err := handler.service.MarkVersionVirusScan(
		c.Request.Context(),
		versionID,
		request.Status,
		strings.TrimSpace(request.Detail),
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(
		c,
		newKnowledgeVersionResponse(*version),
		"病毒扫描结果登记成功",
	)
}

func (handler *KnowledgeHandler) StartParsing(c *gin.Context) {
	if !handler.requireProjectAccess(c, true) {
		return
	}
	taskID, ok := handler.requiredPathID(
		c,
		"taskID",
		"知识摄取任务标识无效",
	)
	if !ok {
		return
	}
	task, err := handler.service.StartParsing(c.Request.Context(), taskID)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(
		c,
		newKnowledgeIngestionResponse(*task),
		"知识解析已开始",
	)
}

type storeKnowledgeChunksRequest struct {
	Chunks []knowledgeChunkRequest `json:"chunks"`
}

type knowledgeChunkRequest struct {
	PageNumber  *int   `json:"page_number"`
	SectionPath string `json:"section_path"`
	Content     string `json:"content"`
	Snippet     string `json:"snippet"`
	TokenCount  int    `json:"token_count"`
}

func (handler *KnowledgeHandler) StoreChunks(c *gin.Context) {
	if !handler.requireProjectAccess(c, true) {
		return
	}
	taskID, ok := handler.requiredPathID(
		c,
		"taskID",
		"知识摄取任务标识无效",
	)
	if !ok {
		return
	}
	var request storeKnowledgeChunksRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	inputs := make([]services.KnowledgeChunkInput, 0, len(request.Chunks))
	for _, chunk := range request.Chunks {
		if len([]rune(chunk.Content)) > 50000 ||
			len([]rune(chunk.Snippet)) > 1000 {
			handler.response.BadRequest(c, "知识分块内容超过长度限制")
			return
		}
		inputs = append(inputs, services.KnowledgeChunkInput{
			PageNumber:  chunk.PageNumber,
			SectionPath: strings.TrimSpace(chunk.SectionPath),
			Content:     strings.TrimSpace(chunk.Content),
			Snippet:     strings.TrimSpace(chunk.Snippet),
			TokenCount:  chunk.TokenCount,
		})
	}
	chunks, err := handler.service.StoreChunks(
		c.Request.Context(),
		taskID,
		inputs,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	responses := make([]knowledgeChunkResponse, 0, len(chunks))
	for _, chunk := range chunks {
		responses = append(responses, newKnowledgeChunkResponse(chunk))
	}
	handler.response.Created(c, responses, "知识分块登记成功")
}

func (handler *KnowledgeHandler) CompleteIngestion(c *gin.Context) {
	if !handler.requireProjectAccess(c, true) {
		return
	}
	taskID, ok := handler.requiredPathID(
		c,
		"taskID",
		"知识摄取任务标识无效",
	)
	if !ok {
		return
	}
	task, err := handler.service.CompleteIngestion(
		c.Request.Context(),
		taskID,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(
		c,
		newKnowledgeIngestionResponse(*task),
		"知识摄取完成",
	)
}

func (handler *KnowledgeHandler) PublishVersion(c *gin.Context) {
	if !handler.requireProjectAccess(c, true) {
		return
	}
	versionID, ok := handler.requiredPathID(
		c,
		"versionID",
		"知识版本标识无效",
	)
	if !ok {
		return
	}
	version, err := handler.service.PublishVersion(
		c.Request.Context(),
		versionID,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(
		c,
		newKnowledgeVersionResponse(*version),
		"知识版本发布成功",
	)
}

func (handler *KnowledgeHandler) RebuildIndex(c *gin.Context) {
	if !handler.requireProjectAccess(c, true) {
		return
	}
	state, err := handler.service.RebuildIndex(c.Request.Context())
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(
		c,
		newKnowledgeIndexStateResponse(*state),
		"知识索引重建成功",
	)
}

type knowledgeSearchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

func (handler *KnowledgeHandler) Search(c *gin.Context) {
	if !handler.requireProjectAccess(c, false) {
		return
	}
	var request knowledgeSearchRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	result, err := handler.service.Search(
		c.Request.Context(),
		services.KnowledgeSearchInput{
			Query: strings.TrimSpace(request.Query),
			Limit: request.Limit,
		},
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	items := make([]knowledgeCitationResponse, 0, len(result.Items))
	for _, citation := range result.Items {
		items = append(items, newKnowledgeCitationResponse(citation))
	}
	handler.response.Success(c, knowledgeSearchResponse{
		SearchID: result.SearchID,
		Items:    items,
	}, "知识检索成功")
}

type knowledgeFeedbackRequest struct {
	Rating  models.KnowledgeFeedbackRating `json:"rating"`
	Comment string                         `json:"comment"`
}

func (handler *KnowledgeHandler) RecordFeedback(c *gin.Context) {
	if !handler.requireProjectAccess(c, false) {
		return
	}
	citationID, ok := handler.requiredPathID(
		c,
		"citationID",
		"知识引用标识无效",
	)
	if !ok {
		return
	}
	var request knowledgeFeedbackRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	feedback, err := handler.service.RecordFeedback(
		c.Request.Context(),
		citationID,
		request.Rating,
		strings.TrimSpace(request.Comment),
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Created(c, knowledgeFeedbackResponse{
		ID:         feedback.ID,
		CitationID: feedback.CitationID,
		Rating:     feedback.Rating,
		Comment:    feedback.Comment,
		CreatedAt:  feedback.CreatedAt,
	}, "知识引用反馈提交成功")
}

func (handler *KnowledgeHandler) GetModelPolicy(c *gin.Context) {
	if !handler.requireProjectAccess(c, true) {
		return
	}
	policy, err := handler.service.GetProjectModelPolicy(
		c.Request.Context(),
		"knowledge",
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	response, err := newKnowledgeModelPolicyResponse(*policy)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(c, response, "获取知识模型策略成功")
}

type updateKnowledgeModelPolicyRequest struct {
	ProviderKey             string                      `json:"provider_key"`
	GenerateModel           string                      `json:"generate_model"`
	EmbeddingModel          string                      `json:"embedding_model"`
	RerankModel             string                      `json:"rerank_model"`
	DataEgress              models.ModelDataEgressMode  `json:"data_egress"`
	RedactionRules          []models.ModelRedactionRule `json:"redaction_rules"`
	ProviderAllowlist       []string                    `json:"provider_allowlist"`
	ModelAllowlist          []string                    `json:"model_allowlist"`
	MonthlyTokenBudget      int64                       `json:"monthly_token_budget"`
	MonthlyCostBudgetMicros int64                       `json:"monthly_cost_budget_micros"`
	RequestsPerMinute       int                         `json:"requests_per_minute"`
	TokensPerMinute         int                         `json:"tokens_per_minute"`
}

func (handler *KnowledgeHandler) UpdateModelPolicy(c *gin.Context) {
	if !handler.requireProjectAccess(c, true) {
		return
	}
	var request updateKnowledgeModelPolicyRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	policy, err := handler.service.SetProjectModelPolicy(
		c.Request.Context(),
		services.ProjectModelPolicyInput{
			PolicyKey:               "knowledge",
			ProviderKey:             strings.TrimSpace(request.ProviderKey),
			GenerateModel:           strings.TrimSpace(request.GenerateModel),
			EmbeddingModel:          strings.TrimSpace(request.EmbeddingModel),
			RerankModel:             strings.TrimSpace(request.RerankModel),
			DataEgress:              request.DataEgress,
			RedactionRules:          request.RedactionRules,
			ProviderAllowlist:       trimKnowledgeStrings(request.ProviderAllowlist),
			ModelAllowlist:          trimKnowledgeStrings(request.ModelAllowlist),
			MonthlyTokenBudget:      request.MonthlyTokenBudget,
			MonthlyCostBudgetMicros: request.MonthlyCostBudgetMicros,
			RequestsPerMinute:       request.RequestsPerMinute,
			TokensPerMinute:         request.TokensPerMinute,
		},
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	response, err := newKnowledgeModelPolicyResponse(*policy)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(c, response, "知识模型策略更新成功")
}

func (handler *KnowledgeHandler) requireProjectAccess(
	c *gin.Context,
	requireManager bool,
) bool {
	if handler == nil || handler.service == nil {
		middleware.NewResponseHelper().InternalServerError(c, "知识服务不可用")
		return false
	}
	access, ok := ProjectAccessFromGin(c)
	if !ok {
		handler.response.Forbidden(c, "未解析可信项目范围")
		return false
	}
	operation, err := services.OperationContextFromContext(c.Request.Context())
	if err != nil ||
		operation.Scope != access.Scope ||
		operation.Source != services.SourceProtocolHumanREST ||
		operation.Actor != models.HumanActor(c.GetUint("user_id")) {
		handler.response.Forbidden(c, "知识操作上下文无效")
		return false
	}
	if !access.Role.IsValid() {
		handler.response.Forbidden(c, "项目角色无效")
		return false
	}
	if requireManager &&
		access.Role != models.ProjectRoleAdmin &&
		access.Role != models.ProjectRoleManager {
		handler.response.Forbidden(c, "仅项目管理员或经理可管理知识内容")
		return false
	}
	return true
}

func (handler *KnowledgeHandler) bindJSON(
	c *gin.Context,
	destination any,
) bool {
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		knowledgeRequestBodyLimit,
	)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		handler.response.BadRequest(c, "知识请求参数无效")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		handler.response.BadRequest(c, "知识请求只能包含一个 JSON 对象")
		return false
	}
	return true
}

func (handler *KnowledgeHandler) requiredPathID(
	c *gin.Context,
	name string,
	message string,
) (string, bool) {
	value := strings.TrimSpace(c.Param(name))
	if value == "" {
		handler.response.BadRequest(c, message)
		return "", false
	}
	return value, true
}

func (handler *KnowledgeHandler) writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, services.ErrKnowledgeNotFound):
		handler.response.NotFound(c, "知识资源不存在")
	case errors.Is(err, services.ErrKnowledgeVirusScanRequired):
		handler.response.Error(c, http.StatusConflict, "文档未通过病毒扫描，不能解析")
	case errors.Is(err, services.ErrKnowledgeIngestionState),
		errors.Is(err, models.ErrPublishedKnowledgeVersionImmutable):
		handler.response.Error(c, http.StatusConflict, "知识资源状态冲突")
	case errors.Is(err, services.ErrKnowledgeIndexUnavailable):
		handler.response.Error(c, http.StatusServiceUnavailable, "知识索引服务不可用")
	case errors.Is(err, services.ErrKnowledgeIndexBoundaryViolation):
		handler.response.Error(c, http.StatusBadGateway, "知识索引边界校验失败")
	case errors.Is(err, services.ErrKnowledgeModelPolicyDenied):
		handler.response.Forbidden(c, "知识模型策略拒绝本次操作")
	case errors.Is(err, services.ErrKnowledgeModelResponseInvalid):
		handler.response.Error(c, http.StatusBadGateway, "知识模型响应无效")
	default:
		handler.response.BadRequest(c, "知识参数或引用无效")
	}
}

func containsObjectStorageURL(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(value, "://") ||
		strings.HasPrefix(value, "http:") ||
		strings.HasPrefix(value, "https:")
}

func trimKnowledgeStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, strings.TrimSpace(value))
	}
	return result
}

type knowledgeArticleResponse struct {
	ID               string                        `json:"id"`
	Key              string                        `json:"key"`
	Title            string                        `json:"title"`
	Summary          string                        `json:"summary"`
	Status           models.KnowledgeArticleStatus `json:"status"`
	CurrentVersionID *string                       `json:"current_version_id,omitempty"`
	Revision         uint64                        `json:"revision"`
	CreatedAt        time.Time                     `json:"created_at"`
	UpdatedAt        time.Time                     `json:"updated_at"`
}

func newKnowledgeArticleResponse(
	article models.KnowledgeArticle,
) knowledgeArticleResponse {
	return knowledgeArticleResponse{
		ID:               article.ID,
		Key:              article.Key,
		Title:            article.Title,
		Summary:          article.Summary,
		Status:           article.Status,
		CurrentVersionID: article.CurrentVersion,
		Revision:         article.Revision,
		CreatedAt:        article.CreatedAt,
		UpdatedAt:        article.UpdatedAt,
	}
}

type knowledgeVersionResponse struct {
	ID               string                        `json:"id"`
	ArticleID        string                        `json:"article_id"`
	Version          uint64                        `json:"version"`
	Status           models.KnowledgeVersionStatus `json:"status"`
	Title            string                        `json:"title"`
	OriginalFileName string                        `json:"original_file_name"`
	MimeType         string                        `json:"mime_type"`
	SizeBytes        int64                         `json:"size_bytes"`
	ContentHash      string                        `json:"content_hash"`
	VirusScan        models.VirusScanStatus        `json:"virus_scan"`
	ScannedAt        *time.Time                    `json:"scanned_at,omitempty"`
	PageCount        int                           `json:"page_count"`
	PublishedAt      *time.Time                    `json:"published_at,omitempty"`
	CreatedAt        time.Time                     `json:"created_at"`
	UpdatedAt        time.Time                     `json:"updated_at"`
}

func newKnowledgeVersionResponse(
	version models.KnowledgeArticleVersion,
) knowledgeVersionResponse {
	return knowledgeVersionResponse{
		ID:               version.ID,
		ArticleID:        version.ArticleID,
		Version:          version.Version,
		Status:           version.Status,
		Title:            version.Title,
		OriginalFileName: version.OriginalFileName,
		MimeType:         version.MimeType,
		SizeBytes:        version.SizeBytes,
		ContentHash:      version.ContentHash,
		VirusScan:        version.VirusScan,
		ScannedAt:        version.ScannedAt,
		PageCount:        version.PageCount,
		PublishedAt:      version.PublishedAt,
		CreatedAt:        version.CreatedAt,
		UpdatedAt:        version.UpdatedAt,
	}
}

type knowledgeACLResponse struct {
	ID          string                         `json:"id"`
	ArticleID   string                         `json:"article_id"`
	SubjectType models.KnowledgeACLSubjectType `json:"subject_type"`
	SubjectID   string                         `json:"subject_id"`
	Permission  models.KnowledgeACLPermission  `json:"permission"`
	CreatedAt   time.Time                      `json:"created_at"`
}

type knowledgeIngestionResponse struct {
	ID          string                          `json:"id"`
	ArticleID   string                          `json:"article_id"`
	VersionID   string                          `json:"version_id"`
	Attempt     uint                            `json:"attempt"`
	Status      models.KnowledgeIngestionStatus `json:"status"`
	ParserKey   string                          `json:"parser_key"`
	StartedAt   *time.Time                      `json:"started_at,omitempty"`
	CompletedAt *time.Time                      `json:"completed_at,omitempty"`
	CreatedAt   time.Time                       `json:"created_at"`
	UpdatedAt   time.Time                       `json:"updated_at"`
}

func newKnowledgeIngestionResponse(
	task models.KnowledgeIngestionTask,
) knowledgeIngestionResponse {
	return knowledgeIngestionResponse{
		ID:          task.ID,
		ArticleID:   task.ArticleID,
		VersionID:   task.VersionID,
		Attempt:     task.Attempt,
		Status:      task.Status,
		ParserKey:   task.ParserKey,
		StartedAt:   task.StartedAt,
		CompletedAt: task.CompletedAt,
		CreatedAt:   task.CreatedAt,
		UpdatedAt:   task.UpdatedAt,
	}
}

type knowledgeChunkResponse struct {
	ID          string `json:"id"`
	ArticleID   string `json:"article_id"`
	VersionID   string `json:"version_id"`
	Ordinal     uint   `json:"ordinal"`
	PageNumber  *int   `json:"page_number,omitempty"`
	SectionPath string `json:"section_path,omitempty"`
	Snippet     string `json:"snippet"`
	ContentHash string `json:"content_hash"`
	TokenCount  int    `json:"token_count"`
}

func newKnowledgeChunkResponse(
	chunk models.KnowledgeChunk,
) knowledgeChunkResponse {
	return knowledgeChunkResponse{
		ID:          chunk.ID,
		ArticleID:   chunk.ArticleID,
		VersionID:   chunk.VersionID,
		Ordinal:     chunk.Ordinal,
		PageNumber:  chunk.PageNumber,
		SectionPath: chunk.SectionPath,
		Snippet:     chunk.Snippet,
		ContentHash: chunk.ContentHash,
		TokenCount:  chunk.TokenCount,
	}
}

type knowledgeIndexStateResponse struct {
	ID                string                      `json:"id"`
	IndexName         string                      `json:"index_name"`
	Generation        uint64                      `json:"generation"`
	DesiredGeneration uint64                      `json:"desired_generation"`
	Status            models.KnowledgeIndexStatus `json:"status"`
	SourceDigest      string                      `json:"source_digest,omitempty"`
	DocumentCount     int                         `json:"document_count"`
	StartedAt         *time.Time                  `json:"started_at,omitempty"`
	CompletedAt       *time.Time                  `json:"completed_at,omitempty"`
	UpdatedAt         time.Time                   `json:"updated_at"`
}

func newKnowledgeIndexStateResponse(
	state models.KnowledgeIndexState,
) knowledgeIndexStateResponse {
	return knowledgeIndexStateResponse{
		ID:                state.ID,
		IndexName:         state.IndexName,
		Generation:        state.Generation,
		DesiredGeneration: state.DesiredGeneration,
		Status:            state.Status,
		SourceDigest:      state.SourceDigest,
		DocumentCount:     state.DocumentCount,
		StartedAt:         state.StartedAt,
		CompletedAt:       state.CompletedAt,
		UpdatedAt:         state.UpdatedAt,
	}
}

type knowledgeCitationResponse struct {
	ID              string  `json:"id"`
	SearchID        string  `json:"search_id"`
	ArticleID       string  `json:"article_id"`
	VersionID       string  `json:"version_id"`
	DocumentVersion uint64  `json:"document_version"`
	ChunkID         string  `json:"chunk_id"`
	PageNumber      *int    `json:"page_number,omitempty"`
	Snippet         string  `json:"snippet"`
	ContentHash     string  `json:"content_hash"`
	Rank            int     `json:"rank"`
	Score           float64 `json:"score"`
}

func newKnowledgeCitationResponse(
	citation models.KnowledgeCitation,
) knowledgeCitationResponse {
	return knowledgeCitationResponse{
		ID:              citation.ID,
		SearchID:        citation.SearchID,
		ArticleID:       citation.ArticleID,
		VersionID:       citation.VersionID,
		DocumentVersion: citation.DocumentVersion,
		ChunkID:         citation.ChunkID,
		PageNumber:      citation.PageNumber,
		Snippet:         citation.Snippet,
		ContentHash:     citation.ContentHash,
		Rank:            citation.Rank,
		Score:           citation.Score,
	}
}

type knowledgeSearchResponse struct {
	SearchID string                      `json:"search_id"`
	Items    []knowledgeCitationResponse `json:"items"`
}

type knowledgeFeedbackResponse struct {
	ID         string                         `json:"id"`
	CitationID string                         `json:"citation_id"`
	Rating     models.KnowledgeFeedbackRating `json:"rating"`
	Comment    string                         `json:"comment,omitempty"`
	CreatedAt  time.Time                      `json:"created_at"`
}

type knowledgeModelPolicyResponse struct {
	ID                      string                     `json:"id"`
	PolicyKey               string                     `json:"policy_key"`
	IsActive                bool                       `json:"is_active"`
	ProviderKey             string                     `json:"provider_key"`
	GenerateModel           string                     `json:"generate_model"`
	EmbeddingModel          string                     `json:"embedding_model"`
	RerankModel             string                     `json:"rerank_model"`
	DataEgress              models.ModelDataEgressMode `json:"data_egress"`
	RedactionRuleCount      int                        `json:"redaction_rule_count"`
	ProviderAllowlistCount  int                        `json:"provider_allowlist_count"`
	ModelAllowlistCount     int                        `json:"model_allowlist_count"`
	MonthlyTokenBudget      int64                      `json:"monthly_token_budget"`
	MonthlyCostBudgetMicros int64                      `json:"monthly_cost_budget_micros"`
	RequestsPerMinute       int                        `json:"requests_per_minute"`
	TokensPerMinute         int                        `json:"tokens_per_minute"`
	CreatedAt               time.Time                  `json:"created_at"`
	UpdatedAt               time.Time                  `json:"updated_at"`
}

func newKnowledgeModelPolicyResponse(
	policy models.ProjectModelPolicy,
) (knowledgeModelPolicyResponse, error) {
	redactions, err := policy.Redactions()
	if err != nil {
		return knowledgeModelPolicyResponse{}, err
	}
	providers, err := policy.AllowedProviders()
	if err != nil {
		return knowledgeModelPolicyResponse{}, err
	}
	allowedModels, err := policy.AllowedModels()
	if err != nil {
		return knowledgeModelPolicyResponse{}, err
	}
	return knowledgeModelPolicyResponse{
		ID:                      policy.ID,
		PolicyKey:               policy.PolicyKey,
		IsActive:                policy.IsActive,
		ProviderKey:             policy.ProviderKey,
		GenerateModel:           policy.GenerateModel,
		EmbeddingModel:          policy.EmbeddingModel,
		RerankModel:             policy.RerankModel,
		DataEgress:              policy.DataEgress,
		RedactionRuleCount:      len(redactions),
		ProviderAllowlistCount:  len(providers),
		ModelAllowlistCount:     len(allowedModels),
		MonthlyTokenBudget:      policy.MonthlyTokenBudget,
		MonthlyCostBudgetMicros: policy.MonthlyCostBudgetMicros,
		RequestsPerMinute:       policy.RequestsPerMinute,
		TokensPerMinute:         policy.TokensPerMinute,
		CreatedAt:               policy.CreatedAt,
		UpdatedAt:               policy.UpdatedAt,
	}, nil
}
