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

const (
	knowledgeRequestBodyLimit         = 8 << 20
	knowledgeAuthoredRequestBodyLimit = services.MaxAuthoredMarkdownBytes +
		(64 << 10)
)

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
	knowledge.GET("/articles", handler.ListArticles)
	knowledge.GET(
		"/articles/:articleID/versions",
		handler.ListArticleVersions,
	)
	knowledge.GET("/ingestions", handler.ListIngestions)
	// Virus scan, parsing, chunk persistence, and ingestion completion are
	// worker-only commands. Exposing them on the human project API would let a
	// browser caller forge a clean scan or inject trusted index content.
	knowledge.POST(
		"/versions/:versionID/publication",
		handler.PublishVersion,
	)
	knowledge.GET("/index-rebuilds/current", handler.GetIndexState)
}

// RegisterExternalRoutes mounts knowledge operations that cross a model or
// search-index boundary. The group must use
// ProjectExternalScopeMiddleware; each service flow performs its own
// snapshot -> external I/O -> final live revalidation sequence.
func (handler *KnowledgeHandler) RegisterExternalRoutes(
	projectGroup *gin.RouterGroup,
) {
	knowledge := projectGroup.Group("/knowledge")
	knowledge.POST("/articles", handler.CreateArticle)
	knowledge.GET(
		"/articles/:articleID/document",
		handler.GetArticleDocument,
	)
	knowledge.POST(
		"/articles/:articleID/drafts",
		handler.CreateArticleDraft,
	)
	knowledge.POST("/index-rebuilds", handler.RebuildIndex)
	knowledge.POST("/searches", handler.Search)
}

type createKnowledgeArticleRequest struct {
	Key                 string `json:"key"`
	Title               string `json:"title"`
	Summary             string `json:"summary"`
	Markdown            string `json:"markdown"`
	SourceTicketID      uint   `json:"source_ticket_id"`
	SourceAttachmentIDs []uint `json:"source_attachment_ids"`
}

func (handler *KnowledgeHandler) ListArticles(c *gin.Context) {
	if !handler.requireProjectAccess(c, false) {
		return
	}
	query, err := parseDirectoryListQuery(
		c.Request.URL.RawQuery,
		directoryListQuerySpec{
			DefaultSortBy:    "updated_at",
			DefaultSortOrder: "desc",
			SortFields: map[string]struct{}{
				"created_at": {},
				"updated_at": {},
				"key":        {},
				"title":      {},
				"status":     {},
			},
			FilterFields: map[string]struct{}{
				"status": {},
				"q":      {},
				"view":   {},
			},
		},
	)
	if err != nil {
		handler.response.BadRequest(c, "知识文章查询参数无效")
		return
	}
	status, _ := query.value("status")
	keyword, _ := query.value("q")
	view, _ := query.value("view")
	if keyword != "" && len([]rune(keyword)) > 200 {
		handler.response.BadRequest(c, "知识文章查询参数无效")
		return
	}
	access, ok := ProjectAccessFromGin(c)
	if !ok {
		handler.response.Forbidden(c, "未解析可信项目范围")
		return
	}
	manageAll := view == "manage"
	managedByActor := view == "mine"
	if view != "" && !manageAll && !managedByActor {
		handler.response.BadRequest(c, "知识文章查询参数无效")
		return
	}
	if (manageAll &&
		access.Role != models.ProjectRoleAdmin &&
		access.Role != models.ProjectRoleManager) ||
		(managedByActor && !access.CanCreateKnowledgeDrafts) {
		handler.response.Forbidden(c, "当前成员无权使用该知识视图")
		return
	}
	page, err := handler.service.ListArticles(
		c.Request.Context(),
		services.KnowledgeArticleListFilter{
			Status:         models.KnowledgeArticleStatus(status),
			Query:          keyword,
			ManageAll:      manageAll,
			ManagedByActor: managedByActor,
		},
		services.DirectoryPageRequest{
			Page:      query.Page,
			PageSize:  query.PageSize,
			SortBy:    query.SortBy,
			SortOrder: query.SortOrder,
		},
	)
	if errors.Is(err, services.ErrDirectoryListQuery) {
		handler.response.BadRequest(c, "知识文章查询参数无效")
		return
	}
	if err != nil {
		handler.writeError(c, err)
		return
	}
	items := make([]knowledgeArticleResponse, 0, len(page.Items))
	for index := range page.Items {
		items = append(
			items,
			newKnowledgeArticleDirectoryResponse(
				page.Items[index],
				manageAll || managedByActor,
			),
		)
	}
	handler.response.List(
		c,
		items,
		page.Total,
		page.Page,
		page.PageSize,
		"获取知识文章成功",
	)
}

func (handler *KnowledgeHandler) CreateArticle(c *gin.Context) {
	if !handler.requireProjectDraftCreationAccess(c) {
		return
	}
	var request createKnowledgeArticleRequest
	if !handler.bindAuthoredJSON(c, &request) ||
		!handler.validateAuthoredInput(
			c,
			request.Markdown,
			request.SourceTicketID,
			request.SourceAttachmentIDs,
		) {
		return
	}
	result, err := handler.service.CreateAuthoredArticle(
		c.Request.Context(),
		services.CreateAuthoredArticleInput{
			Key:            strings.TrimSpace(request.Key),
			Title:          strings.TrimSpace(request.Title),
			Summary:        strings.TrimSpace(request.Summary),
			Markdown:       request.Markdown,
			SourceTicketID: request.SourceTicketID,
			SourceAttachmentIDs: append(
				[]uint(nil),
				request.SourceAttachmentIDs...,
			),
		},
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Created(
		c,
		newKnowledgeAuthoredResponse(*result),
		"知识文章草稿创建成功",
	)
}

func (handler *KnowledgeHandler) requireProjectDraftCreationAccess(
	c *gin.Context,
) bool {
	if !handler.requireProjectAccess(c, false) {
		return false
	}
	access, ok := ProjectAccessFromGin(c)
	if !ok ||
		(!access.CanCreateKnowledgeDrafts &&
			access.Role != models.ProjectRoleAdmin &&
			access.Role != models.ProjectRoleManager) {
		handler.response.Forbidden(
			c,
			"当前成员未获知识草稿贡献授权",
		)
		return false
	}
	return true
}

type createKnowledgeArticleDraftRequest struct {
	Title               string `json:"title"`
	Markdown            string `json:"markdown"`
	SourceTicketID      uint   `json:"source_ticket_id"`
	SourceAttachmentIDs []uint `json:"source_attachment_ids"`
}

func (handler *KnowledgeHandler) CreateArticleDraft(c *gin.Context) {
	if !handler.requireProjectDraftCreationAccess(c) {
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
	var request createKnowledgeArticleDraftRequest
	if !handler.bindAuthoredJSON(c, &request) ||
		!handler.validateAuthoredInput(
			c,
			request.Markdown,
			request.SourceTicketID,
			request.SourceAttachmentIDs,
		) {
		return
	}
	result, err := handler.service.CreateAuthoredVersion(
		c.Request.Context(),
		articleID,
		services.CreateAuthoredVersionInput{
			Title:          strings.TrimSpace(request.Title),
			Markdown:       request.Markdown,
			SourceTicketID: request.SourceTicketID,
			SourceAttachmentIDs: append(
				[]uint(nil),
				request.SourceAttachmentIDs...,
			),
		},
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Created(
		c,
		newKnowledgeAuthoredResponse(*result),
		"知识文章新草稿创建成功",
	)
}

func (handler *KnowledgeHandler) GetArticleDocument(c *gin.Context) {
	if !handler.requireProjectAccess(c, false) {
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
	query := c.Request.URL.Query()
	for key, values := range query {
		if (key != "version_id" && key != "prefer_latest_draft") ||
			len(values) != 1 {
			handler.response.BadRequest(c, "知识正文查询参数无效")
			return
		}
	}
	versionID := strings.TrimSpace(query.Get("version_id"))
	if _, present := query["version_id"]; present && versionID == "" {
		handler.response.BadRequest(c, "知识正文查询参数无效")
		return
	}
	preferLatestDraft := false
	if values, present := query["prefer_latest_draft"]; present {
		switch strings.TrimSpace(values[0]) {
		case "true":
			preferLatestDraft = true
		case "false":
		default:
			handler.response.BadRequest(c, "知识正文查询参数无效")
			return
		}
	}
	if versionID != "" && preferLatestDraft {
		handler.response.BadRequest(c, "知识正文查询参数无效")
		return
	}
	document, err := handler.service.GetArticleDocument(
		c.Request.Context(),
		articleID,
		services.GetArticleDocumentInput{
			VersionID:         versionID,
			PreferLatestDraft: preferLatestDraft,
		},
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(
		c,
		newKnowledgeDocumentResponse(*document),
		"获取知识正文成功",
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

func (handler *KnowledgeHandler) ListArticleVersions(c *gin.Context) {
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
	query, err := parseDirectoryListQuery(
		c.Request.URL.RawQuery,
		directoryListQuerySpec{
			DefaultSortBy:    "version",
			DefaultSortOrder: "desc",
			SortFields: map[string]struct{}{
				"created_at": {},
				"updated_at": {},
				"version":    {},
				"status":     {},
			},
			FilterFields: map[string]struct{}{
				"status":     {},
				"virus_scan": {},
			},
		},
	)
	if err != nil {
		handler.response.BadRequest(c, "知识版本查询参数无效")
		return
	}
	status, _ := query.value("status")
	virusScan, _ := query.value("virus_scan")
	page, err := handler.service.ListArticleVersions(
		c.Request.Context(),
		articleID,
		services.KnowledgeVersionListFilter{
			Status:    models.KnowledgeVersionStatus(status),
			VirusScan: models.VirusScanStatus(virusScan),
		},
		services.DirectoryPageRequest{
			Page:      query.Page,
			PageSize:  query.PageSize,
			SortBy:    query.SortBy,
			SortOrder: query.SortOrder,
		},
	)
	if errors.Is(err, services.ErrDirectoryListQuery) {
		handler.response.BadRequest(c, "知识版本查询参数无效")
		return
	}
	if err != nil {
		handler.writeError(c, err)
		return
	}
	items := make([]knowledgeVersionResponse, 0, len(page.Items))
	for index := range page.Items {
		items = append(items, newKnowledgeVersionResponse(page.Items[index]))
	}
	handler.response.List(
		c,
		items,
		page.Total,
		page.Page,
		page.PageSize,
		"获取知识版本成功",
	)
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

func (handler *KnowledgeHandler) ListIngestions(c *gin.Context) {
	if !handler.requireProjectAccess(c, true) {
		return
	}
	query, err := parseDirectoryListQuery(
		c.Request.URL.RawQuery,
		directoryListQuerySpec{
			DefaultSortBy:    "created_at",
			DefaultSortOrder: "desc",
			SortFields: map[string]struct{}{
				"created_at": {},
				"updated_at": {},
				"attempt":    {},
				"status":     {},
			},
			FilterFields: map[string]struct{}{
				"status":     {},
				"version_id": {},
			},
		},
	)
	if err != nil {
		handler.response.BadRequest(c, "知识摄取查询参数无效")
		return
	}
	status, _ := query.value("status")
	versionID, _ := query.value("version_id")
	page, err := handler.service.ListIngestions(
		c.Request.Context(),
		services.KnowledgeIngestionListFilter{
			Status:    models.KnowledgeIngestionStatus(status),
			VersionID: versionID,
		},
		services.DirectoryPageRequest{
			Page:      query.Page,
			PageSize:  query.PageSize,
			SortBy:    query.SortBy,
			SortOrder: query.SortOrder,
		},
	)
	if errors.Is(err, services.ErrDirectoryListQuery) {
		handler.response.BadRequest(c, "知识摄取查询参数无效")
		return
	}
	if err != nil {
		handler.writeError(c, err)
		return
	}
	items := make([]knowledgeIngestionResponse, 0, len(page.Items))
	for index := range page.Items {
		items = append(items, newKnowledgeIngestionResponse(page.Items[index]))
	}
	handler.response.List(
		c,
		items,
		page.Total,
		page.Page,
		page.PageSize,
		"获取知识摄取任务成功",
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
	c.JSON(http.StatusAccepted, gin.H{
		"code": 0,
		"msg":  "知识索引重建已进入持久队列",
		"data": newKnowledgeIndexStateResponse(*state),
	})
}

func (handler *KnowledgeHandler) GetIndexState(c *gin.Context) {
	if !handler.requireProjectAccess(c, false) {
		return
	}
	state, err := handler.service.GetIndexState(c.Request.Context())
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(
		c,
		newKnowledgeIndexStateResponse(*state),
		"获取知识索引状态成功",
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
	return handler.bindJSONWithLimit(
		c,
		destination,
		knowledgeRequestBodyLimit,
	)
}

func (handler *KnowledgeHandler) bindAuthoredJSON(
	c *gin.Context,
	destination any,
) bool {
	return handler.bindJSONWithLimit(
		c,
		destination,
		knowledgeAuthoredRequestBodyLimit,
	)
}

func (handler *KnowledgeHandler) bindJSONWithLimit(
	c *gin.Context,
	destination any,
	limit int64,
) bool {
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		limit,
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

func (handler *KnowledgeHandler) validateAuthoredInput(
	c *gin.Context,
	markdown string,
	sourceTicketID uint,
	sourceAttachmentIDs []uint,
) bool {
	markdownBytes := len([]byte(markdown))
	if strings.TrimSpace(markdown) == "" ||
		markdownBytes > services.MaxAuthoredMarkdownBytes {
		handler.response.BadRequest(c, "知识正文必须为 1 到 128 KiB 的 Markdown")
		return false
	}
	if len(sourceAttachmentIDs) > services.MaxAuthoredSourceLinks {
		handler.response.BadRequest(c, "知识来源附件最多 20 个")
		return false
	}
	if len(sourceAttachmentIDs) > 0 && sourceTicketID == 0 {
		handler.response.BadRequest(c, "关联附件时必须同时指定来源工单")
		return false
	}
	seen := make(map[uint]struct{}, len(sourceAttachmentIDs))
	for _, attachmentID := range sourceAttachmentIDs {
		if attachmentID == 0 {
			handler.response.BadRequest(c, "知识来源附件标识无效")
			return false
		}
		if _, duplicate := seen[attachmentID]; duplicate {
			handler.response.BadRequest(c, "知识来源附件不能重复")
			return false
		}
		seen[attachmentID] = struct{}{}
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
	case errors.Is(err, services.ErrProjectKnowledgeAccessDenied),
		errors.Is(err, services.ErrProjectAccessDenied),
		errors.Is(err, services.ErrPolicyDenied):
		handler.response.Forbidden(c, "无权访问或管理该知识资源")
	case errors.Is(err, services.ErrKnowledgeVirusScanRequired):
		handler.response.Error(c, http.StatusConflict, "文档未通过病毒扫描，不能解析")
	case errors.Is(err, services.ErrKnowledgeIngestionState),
		errors.Is(err, models.ErrPublishedKnowledgeVersionImmutable),
		errors.Is(err, services.ErrAttachmentNotClean),
		errors.Is(err, services.ErrIdempotencyConflict):
		handler.response.Error(c, http.StatusConflict, "知识资源状态冲突")
	case errors.Is(err, services.ErrKnowledgeIndexUnavailable):
		handler.response.Error(c, http.StatusServiceUnavailable, "知识索引服务不可用")
	case errors.Is(err, services.ErrAttachmentStorageMissing):
		handler.response.Error(c, http.StatusServiceUnavailable, "知识对象存储不可用")
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
	ID                  string                        `json:"id"`
	Key                 string                        `json:"key"`
	Title               string                        `json:"title"`
	Summary             string                        `json:"summary"`
	Status              models.KnowledgeArticleStatus `json:"status"`
	CurrentVersionID    *string                       `json:"current_version_id,omitempty"`
	Revision            uint64                        `json:"revision"`
	CreatedAt           time.Time                     `json:"created_at"`
	UpdatedAt           time.Time                     `json:"updated_at"`
	HasUnpublishedDraft *bool                         `json:"has_unpublished_draft,omitempty"`
	LatestDraftAt       *time.Time                    `json:"latest_draft_at,omitempty"`
	LatestDraftVersion  *uint64                       `json:"latest_draft_version,omitempty"`
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

func newKnowledgeArticleDirectoryResponse(
	article models.KnowledgeArticle,
	includeDraftActivity bool,
) knowledgeArticleResponse {
	response := newKnowledgeArticleResponse(article)
	if !includeDraftActivity {
		return response
	}
	hasDraft := article.HasUnpublishedDraft
	response.HasUnpublishedDraft = &hasDraft
	response.LatestDraftAt = article.LatestDraftAt
	response.LatestDraftVersion = article.LatestDraftVersion
	return response
}

type knowledgeVersionResponse struct {
	ID               string                        `json:"id"`
	ArticleID        string                        `json:"article_id"`
	Version          uint64                        `json:"version"`
	Status           models.KnowledgeVersionStatus `json:"status"`
	CreatedByType    models.ActorType              `json:"created_by_type"`
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
		CreatedByType:    version.CreatedByType,
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

type knowledgeSourceResponse struct {
	Ordinal            uint                               `json:"ordinal"`
	Kind               services.KnowledgeSourceKind       `json:"kind"`
	Visibility         services.KnowledgeSourceVisibility `json:"visibility"`
	ReferenceLabel     string                             `json:"reference_label"`
	SourceTicketID     *uint                              `json:"source_ticket_id,omitempty"`
	TicketNumber       string                             `json:"ticket_number,omitempty"`
	TicketTitle        string                             `json:"ticket_title,omitempty"`
	SourceAttachmentID *uint                              `json:"source_attachment_id,omitempty"`
	AttachmentName     string                             `json:"attachment_name,omitempty"`
	AttachmentHash     string                             `json:"attachment_hash,omitempty"`
}

func newKnowledgeSourceResponse(
	source services.KnowledgeSourceView,
) knowledgeSourceResponse {
	return knowledgeSourceResponse{
		Ordinal:            source.Ordinal,
		Kind:               source.Kind,
		Visibility:         source.Visibility,
		ReferenceLabel:     source.ReferenceLabel,
		SourceTicketID:     source.SourceTicketID,
		SourceAttachmentID: source.SourceAttachmentID,
		TicketNumber:       source.TicketNumber,
		TicketTitle:        source.TicketTitle,
		AttachmentName:     source.AttachmentName,
		AttachmentHash:     source.AttachmentHash,
	}
}

func newKnowledgeSourceResponses(
	sources []services.KnowledgeSourceView,
) []knowledgeSourceResponse {
	result := make([]knowledgeSourceResponse, 0, len(sources))
	for _, source := range sources {
		result = append(result, newKnowledgeSourceResponse(source))
	}
	return result
}

type knowledgeAuthoredResponse struct {
	Article knowledgeArticleResponse  `json:"article"`
	Version knowledgeVersionResponse  `json:"version"`
	Sources []knowledgeSourceResponse `json:"sources"`
	Receipt services.OperationReceipt `json:"receipt"`
}

func newKnowledgeAuthoredResponse(
	result services.AuthoredKnowledgeResult,
) knowledgeAuthoredResponse {
	return knowledgeAuthoredResponse{
		Article: newKnowledgeArticleResponse(result.Article),
		Version: newKnowledgeVersionResponse(result.Version),
		Sources: newKnowledgeSourceResponses(result.Sources),
		Receipt: result.Receipt,
	}
}

type knowledgeDocumentSectionResponse struct {
	Ordinal     uint   `json:"ordinal"`
	Heading     string `json:"heading"`
	Level       int    `json:"level"`
	SectionPath string `json:"section_path,omitempty"`
	Markdown    string `json:"markdown"`
	ContentHash string `json:"content_hash"`
}

type knowledgeDocumentResponse struct {
	Article  knowledgeArticleResponse           `json:"article"`
	Version  knowledgeVersionResponse           `json:"version"`
	Markdown string                             `json:"markdown"`
	Sections []knowledgeDocumentSectionResponse `json:"sections"`
	Sources  []knowledgeSourceResponse          `json:"sources"`
}

func newKnowledgeDocumentResponse(
	document services.KnowledgeArticleDocument,
) knowledgeDocumentResponse {
	sections := make(
		[]knowledgeDocumentSectionResponse,
		0,
		len(document.Sections),
	)
	for _, section := range document.Sections {
		sections = append(sections, knowledgeDocumentSectionResponse{
			Ordinal:     section.Ordinal,
			Heading:     section.Heading,
			Level:       section.HeadingLevel,
			SectionPath: section.SectionPath,
			Markdown:    section.Markdown,
			ContentHash: section.ContentHash,
		})
	}
	return knowledgeDocumentResponse{
		Article:  newKnowledgeArticleResponse(document.Article),
		Version:  newKnowledgeVersionResponse(document.Version),
		Markdown: document.Markdown,
		Sections: sections,
		Sources:  newKnowledgeSourceResponses(document.Sources),
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
	ArticleKey      string  `json:"article_key"`
	ArticleTitle    string  `json:"article_title"`
	VersionID       string  `json:"version_id"`
	DocumentVersion uint64  `json:"document_version"`
	ChunkID         string  `json:"chunk_id"`
	PageNumber      *int    `json:"page_number,omitempty"`
	SectionPath     string  `json:"section_path"`
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
		ArticleKey:      citation.ArticleKey,
		ArticleTitle:    citation.ArticleTitle,
		VersionID:       citation.VersionID,
		DocumentVersion: citation.DocumentVersion,
		ChunkID:         citation.ChunkID,
		PageNumber:      citation.PageNumber,
		SectionPath:     citation.SectionPath,
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
