package agentplatform

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/agentauth"
	"github.com/seaworld008/chronodesk/server/internal/httpcontract"
	"github.com/seaworld008/chronodesk/server/internal/mcp"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/observability"
	"github.com/seaworld008/chronodesk/server/internal/safeconv"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type ResourcePublisher interface {
	PublishTicket(projectKey string, ticketID uint)
}

type apiPublicationBuffer struct {
	projectKey string
	ticketIDs  []uint
	seen       map[uint]struct{}
}

type apiPublicationBufferContextKey struct{}

type APIHandler struct {
	db                 *gorm.DB
	native             *services.AgentNativeService
	knowledge          *services.KnowledgeService
	projects           *services.ProjectService
	tokens             *agentauth.Manager
	maxAttachmentBytes int64
	publisher          ResourcePublisher
	ticketContentLists *agentTicketContentListCursors
}

func NewAPIHandler(
	db *gorm.DB,
	native *services.AgentNativeService,
	tokens *agentauth.Manager,
	maxAttachmentBytes int64,
	publisher ResourcePublisher,
) *APIHandler {
	if maxAttachmentBytes <= 0 {
		maxAttachmentBytes = 10 << 20
	}
	projects, _ := services.NewProjectService(db)
	return &APIHandler{
		db:                 db,
		native:             native,
		projects:           projects,
		tokens:             tokens,
		maxAttachmentBytes: maxAttachmentBytes,
		publisher:          publisher,
	}
}

func (h *APIHandler) SetKnowledgeService(
	knowledge *services.KnowledgeService,
) {
	if h != nil {
		h.knowledge = knowledge
	}
}

func (h *APIHandler) RegisterRoutes(api *gin.RouterGroup) {
	api.GET("/capabilities", h.tokens.Middleware(models.ScopeTicketsRead), h.bindProjectContext(), h.Capabilities)
	api.GET("/knowledge/articles", h.tokens.Middleware(models.ScopeKnowledgeRead), h.executionLimit(), h.bindExternalProjectContext(), h.ListKnowledgeArticles)
	api.POST("/knowledge/articles", h.tokens.Middleware(models.ScopeKnowledgeWrite), h.executionLimit(), h.bindExternalProjectContext(), h.CreateKnowledgeArticleDraft)
	api.GET("/knowledge/articles/:knowledgeArticleID/document", h.tokens.Middleware(models.ScopeKnowledgeRead), h.executionLimit(), h.bindExternalProjectContext(), h.GetKnowledgeArticleDocument)
	api.POST("/knowledge/articles/:knowledgeArticleID/drafts", h.tokens.Middleware(models.ScopeKnowledgeWrite), h.executionLimit(), h.bindExternalProjectContext(), h.CreateKnowledgeVersionDraft)
	api.POST("/knowledge/searches", h.tokens.Middleware(models.ScopeKnowledgeRead), h.executionLimit(), h.bindExternalProjectContext(), h.SearchKnowledge)
	api.GET("/tickets", h.tokens.Middleware(models.ScopeTicketsRead), h.executionLimit(), h.bindProjectContext(), h.ListTickets)
	api.POST("/tickets", h.tokens.Middleware(models.ScopeTicketsCreate), h.executionLimit(), h.bindMachineWriteProjectContext(), h.CreateTicket)
	api.GET("/tickets/:id", h.tokens.Middleware(models.ScopeTicketsRead), h.executionLimit(), h.bindProjectContext(), h.GetTicket)
	api.PATCH("/tickets/:id", h.tokens.Middleware(models.ScopeTicketsUpdate), h.executionLimit(), h.bindMachineWriteProjectContext(), h.UpdateTicket)
	api.POST("/tickets/:id/commands/assign", h.tokens.Middleware(models.ScopeTicketsAssign), h.executionLimit(), h.bindMachineWriteProjectContext(), h.AssignTicket)
	api.POST("/tickets/:id/commands/transition", h.tokens.Middleware(models.ScopeTicketsTransition), h.executionLimit(), h.bindMachineWriteProjectContext(), h.TransitionTicket)
	api.POST("/tickets/:id/commands/escalate", h.tokens.Middleware(models.ScopeTicketsTransition), h.executionLimit(), h.bindMachineWriteProjectContext(), h.EscalateTicket)
	api.GET("/tickets/:id/history", h.tokens.Middleware(models.ScopeTicketsRead), h.executionLimit(), h.bindProjectContext(), h.GetHistory)
	api.GET("/tickets/:id/comments", h.tokens.Middleware(models.ScopeTicketsRead), h.executionLimit(), h.bindProjectContext(), h.ListComments)
	api.POST("/tickets/:id/comments", h.tokens.Middleware(models.ScopeCommentsWrite), h.executionLimit(), h.bindMachineWriteProjectContext(), h.CreateComment)
	api.GET("/tickets/:id/attachments", h.tokens.Middleware(models.ScopeAttachmentsRead), h.executionLimit(), h.bindProjectContext(), h.ListAttachments)
	api.POST("/tickets/:id/attachments", h.tokens.Middleware(models.ScopeAttachmentsWrite), h.executionLimit(), h.bindExternalProjectContext(), h.StoreAttachment)
	api.GET("/attachments/:id/content", h.tokens.Middleware(models.ScopeAttachmentsRead), h.executionLimit(), h.bindExternalProjectContext(), h.DownloadAttachment)
	api.POST("/tickets/:id/claim", h.tokens.Middleware(models.ScopeTasksManage), h.executionLimit(), h.bindMachineWriteProjectContext(), h.ClaimTicket)
	api.POST("/leases/:id/heartbeat", h.tokens.Middleware(models.ScopeTasksManage), h.executionLimit(), h.bindMachineWriteProjectContext(), h.HeartbeatLease)
	api.DELETE("/leases/:id", h.tokens.Middleware(models.ScopeTasksManage), h.executionLimit(), h.bindMachineWriteProjectContext(), h.ReleaseLease)
	api.GET("/events", h.tokens.Middleware(models.ScopeEventsSubscribe), h.executionLimit(), h.bindProjectContext(), h.ListEvents)
}

func (h *APIHandler) bindProjectContext() gin.HandlerFunc {
	return h.bindProjectContextWithTransaction(true)
}

func (h *APIHandler) bindMachineWriteProjectContext() gin.HandlerFunc {
	return h.bindProjectContextWithTransaction(false)
}

func (h *APIHandler) bindProjectContextWithTransaction(
	transactional bool,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.projects == nil {
			WriteProblem(
				c,
				http.StatusServiceUnavailable,
				ProblemInternal,
				"Project authorization is unavailable",
				true,
			)
			c.Abort()
			return
		}
		principalID := strings.TrimSpace(
			c.GetString(agentauth.ContextPrincipalID),
		)
		credentialID := strings.TrimSpace(
			c.GetString(agentauth.ContextCredentialID),
		)
		projectKey := strings.TrimSpace(c.Param("projectKey"))
		tokenScopes, _ := c.Get(agentauth.ContextScopes)
		verifiedTokenScopes, ok := tokenScopes.([]string)
		if !ok {
			WriteProblem(
				c,
				http.StatusUnauthorized,
				ProblemUnauthorized,
				"Verified Agent scopes are unavailable",
				false,
			)
			c.Abort()
			return
		}
		verifiedTokenScopes = append(
			[]string(nil),
			verifiedTokenScopes...,
		)
		access, err := h.projects.ResolvePrincipalProject(
			c.Request.Context(),
			projectKey,
			principalID,
		)
		if err != nil {
			WriteProblem(
				c,
				http.StatusForbidden,
				ProblemPolicyDenied,
				"Project access is denied",
				false,
			)
			c.Abort()
			return
		}
		operationContext, err := services.WithOperationContext(
			c.Request.Context(),
			services.OperationContext{
				Scope:        access.Scope,
				Actor:        models.ServicePrincipalActor(principalID),
				Source:       services.SourceProtocolAgentREST,
				CredentialID: credentialID,
				TraceID: observability.TraceIDFromContext(
					c.Request.Context(),
				),
				CorrelationID: observability.CorrelationIDFromContext(
					c.Request.Context(),
				),
			},
		)
		if err != nil {
			WriteProblem(
				c,
				http.StatusForbidden,
				ProblemPolicyDenied,
				"Project operation context is invalid",
				false,
			)
			c.Abort()
			return
		}
		publications := &apiPublicationBuffer{
			projectKey: projectKey,
			seen:       make(map[uint]struct{}),
		}
		operationContext = context.WithValue(
			operationContext,
			apiPublicationBufferContextKey{},
			publications,
		)
		originalRequest := c.Request
		if !transactional {
			c.Set(
				agentauth.ContextScopes,
				intersectScopes(
					verifiedTokenScopes,
					access.Scopes,
				),
			)
			c.Request = originalRequest.WithContext(operationContext)
			defer func() {
				c.Request = originalRequest.WithContext(operationContext)
			}()
			c.Next()
			for _, ticketID := range publications.ticketIDs {
				if h.publisher != nil {
					h.publisher.PublishTicket(projectKey, ticketID)
				}
			}
			return
		}

		originalWriter := c.Writer
		defer func() {
			c.Writer = originalWriter
			c.Request = originalRequest.WithContext(operationContext)
		}()
		responseBuffer, err :=
			middleware.NewTransactionalResponseBuffer(originalWriter)
		if err != nil {
			WriteProblem(
				c,
				http.StatusInternalServerError,
				ProblemInternal,
				"Project response transaction is unavailable",
				true,
			)
			c.Abort()
			return
		}
		defer func() {
			if closeErr := responseBuffer.Close(); closeErr != nil {
				_ = c.Error(closeErr)
			}
		}()

		transactionErr := scopeddb.WithProjectScopeContextTransaction(
			operationContext,
			h.db,
			access.Scope,
			func(scopedContext context.Context) error {
				currentAccess, revalidateErr :=
					h.native.RevalidatePrincipalProjectOperation(
						scopedContext,
						verifiedTokenScopes...,
					)
				if revalidateErr != nil {
					return revalidateErr
				}
				if currentAccess.Project.Key !=
					models.ProjectKey(projectKey) {
					return services.ErrProjectAccessDenied
				}
				c.Set(
					agentauth.ContextScopes,
					intersectScopes(
						verifiedTokenScopes,
						currentAccess.Scopes,
					),
				)
				c.Request = originalRequest.WithContext(scopedContext)
				c.Writer = responseBuffer
				c.Next()
				c.Writer = originalWriter
				if responseErr := responseBuffer.Err(); responseErr != nil {
					return responseErr
				}
				// HTTP problem responses can carry durable denied
				// PolicyDecision and failed idempotency state. Domain
				// transactions preserve their own rollback via savepoints.
				return nil
			},
		)
		c.Writer = originalWriter
		c.Request = originalRequest.WithContext(operationContext)
		if transactionErr != nil {
			_ = c.Error(transactionErr)
			if errors.Is(
				transactionErr,
				services.ErrProjectAccessDenied,
			) {
				WriteProblem(
					c,
					http.StatusForbidden,
					ProblemPolicyDenied,
					"Project access is denied",
					false,
				)
				c.Abort()
				return
			}
			if errors.Is(transactionErr, services.ErrInvalidCredential) ||
				errors.Is(transactionErr, services.ErrCredentialExpired) ||
				errors.Is(transactionErr, services.ErrPrincipalNotFound) ||
				errors.Is(transactionErr, services.ErrPrincipalDisabled) ||
				errors.Is(transactionErr, services.ErrPrincipalExpired) {
				WriteProblem(
					c,
					http.StatusUnauthorized,
					ProblemUnauthorized,
					"Agent credential is no longer active",
					false,
				)
				c.Abort()
				return
			}
			WriteProblem(
				c,
				http.StatusInternalServerError,
				ProblemInternal,
				"Project transaction failed",
				true,
			)
			c.Abort()
			return
		}
		for _, ticketID := range publications.ticketIDs {
			if h.publisher != nil {
				h.publisher.PublishTicket(projectKey, ticketID)
			}
		}
		if err := responseBuffer.Commit(); err != nil {
			_ = c.Error(err)
			if !c.Writer.Written() {
				WriteProblem(
					c,
					http.StatusInternalServerError,
					ProblemInternal,
					"Project response failed",
					true,
				)
			}
			c.Abort()
		}
	}
}

func agentRESTProjectPath(c *gin.Context, suffix string) string {
	projectKey := ""
	if c != nil {
		projectKey = strings.TrimSpace(c.Param("projectKey"))
	}
	return "/api/v2/projects/" + projectKey + "/" +
		strings.TrimPrefix(suffix, "/")
}

func (h *APIHandler) Capabilities(c *gin.Context) {
	WriteData(c, http.StatusOK, gin.H{
		"api_version":       "v2",
		"openapi":           "/openapi.yaml",
		"asyncapi":          "/asyncapi.yaml",
		"mcp_endpoint":      "/mcp",
		"mcp_version":       mcp.ProtocolVersion,
		"mcp_transport":     "streamable-http",
		"mcp_stateless":     true,
		"mcp_subscriptions": "subscriptions/listen",
		"a2a_endpoint":      "/a2a/v1",
		"a2a_version":       "1.0",
		"agent_card":        "/.well-known/agent-card.json",
		"oauth_metadata": gin.H{
			"api": "/.well-known/oauth-protected-resource/api/v2",
			"mcp": "/.well-known/oauth-protected-resource/mcp",
			"a2a": "/.well-known/oauth-protected-resource/a2a/v1",
		},
		"scopes_supported": models.SupportedAgentScopes,
		"concurrency": gin.H{
			"optimistic_version": true,
			"ticket_leases":      true,
			"idempotency_keys":   true,
		},
	}, Meta{})
}

func (h *APIHandler) ListKnowledgeArticles(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	decision, ok := h.checkAgentKnowledgeAction(
		c,
		principal,
		models.ScopeKnowledgeRead,
		"knowledge.article.list",
		"knowledge_article",
		"*",
		false,
		"",
	)
	if !ok {
		return
	}
	page, pageSize, ok := parseAgentKnowledgePage(c)
	if !ok {
		return
	}
	result, err := h.knowledge.ListArticles(
		c.Request.Context(),
		services.KnowledgeArticleListFilter{
			PolicyDecisionID: decision.ID,
		},
		services.DirectoryPageRequest{
			Page:      page,
			PageSize:  pageSize,
			SortBy:    "updated_at",
			SortOrder: "desc",
		},
	)
	if errors.Is(err, services.ErrDirectoryListQuery) {
		WriteProblem(
			c,
			http.StatusBadRequest,
			ProblemInvalidRequest,
			"Knowledge article pagination is invalid",
			false,
		)
		return
	}
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	items := make(
		[]agentKnowledgeArticleResponse,
		0,
		len(result.Items),
	)
	for _, article := range result.Items {
		items = append(items, newAgentKnowledgeArticleResponse(article))
	}
	WriteData(c, http.StatusOK, agentKnowledgeArticlePage{
		Items:      items,
		Total:      result.Total,
		Page:       result.Page,
		PageSize:   result.PageSize,
		TotalPages: result.TotalPages,
	}, Meta{HasMore: result.Page < result.TotalPages})
}

func (h *APIHandler) GetKnowledgeArticleDocument(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	articleID, ok := parseKnowledgeArticleID(c, "knowledgeArticleID")
	if !ok {
		return
	}
	versionID, ok := parseOptionalKnowledgeVersionID(c)
	if !ok {
		return
	}
	decision, ok := h.checkAgentKnowledgeAction(
		c,
		principal,
		models.ScopeKnowledgeRead,
		"knowledge.article.read",
		"knowledge_article",
		articleID,
		false,
		"",
	)
	if !ok {
		return
	}
	documentInput := services.GetArticleDocumentInput{
		VersionID:        versionID,
		PolicyDecisionID: decision.ID,
		Authorization: services.
			KnowledgeDocumentRead,
	}
	document, err := h.knowledge.GetArticleDocument(
		c.Request.Context(),
		articleID,
		documentInput,
	)
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	document, ok = h.expandAgentKnowledgeDocumentSources(
		c,
		principal,
		articleID,
		documentInput,
		document,
	)
	if !ok {
		return
	}
	WriteData(
		c,
		http.StatusOK,
		newAgentKnowledgeDocumentResponse(document),
		Meta{},
	)
}

func (h *APIHandler) CreateKnowledgeArticleDraft(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	key, ok := RequireIdempotencyKey(c)
	if !ok {
		return
	}
	body, ok := readJSONBody(c, maxAgentKnowledgeRequestBytes)
	if !ok {
		return
	}
	var request agentKnowledgeArticleDraftRequest
	if err := decodeStrictJSON(body, &request); err != nil {
		WriteProblem(
			c,
			http.StatusBadRequest,
			ProblemInvalidRequest,
			"Knowledge article draft request is invalid",
			false,
		)
		return
	}
	if err := validateAgentKnowledgeArticleDraft(request); err != nil {
		WriteProblem(
			c,
			http.StatusBadRequest,
			ProblemInvalidRequest,
			err.Error(),
			false,
		)
		return
	}
	fingerprint := commandFingerprint(
		http.MethodPost,
		agentRESTProjectPath(c, "/knowledge/articles"),
		0,
		"",
		body,
	)
	requestDigest := digestBytes(fingerprint)
	decision, ok := h.checkAgentKnowledgeAction(
		c,
		principal,
		models.ScopeKnowledgeWrite,
		"knowledge.article.draft.create",
		"knowledge_article",
		"*",
		true,
		requestDigest,
	)
	if !ok {
		return
	}
	sourceAuthorization, ok := h.authorizeAgentKnowledgeSources(
		c,
		principal,
		request.SourceTicketID,
		request.SourceAttachmentIDs,
		requestDigest,
	)
	if !ok {
		return
	}
	reservation, err := h.native.ReserveIdempotency(
		c.Request.Context(),
		models.ServicePrincipalActor(principal.ID),
		"knowledge.article.create",
		key,
		fingerprint,
		24*time.Hour,
	)
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	if reservation.Replayed {
		h.writeReplayedKnowledgeDocument(
			c,
			reservation.Record,
			"",
			decision.ID,
			services.KnowledgeDocumentAuthoredArticleReplay,
		)
		return
	}
	result, err := h.knowledge.CreateAuthoredArticle(
		c.Request.Context(),
		services.CreateAuthoredArticleInput{
			Key:                              request.Key,
			Title:                            request.Title,
			Summary:                          request.Summary,
			Markdown:                         request.Markdown,
			SourceTicketID:                   request.SourceTicketID,
			SourceAttachmentIDs:              append([]uint(nil), request.SourceAttachmentIDs...),
			PolicyDecisionID:                 decision.ID,
			SourceTicketPolicyDecisionID:     sourceAuthorization.TicketPolicyDecisionID,
			SourceAttachmentPolicyDecisionID: sourceAuthorization.AttachmentPolicyDecisionID,
			IdempotencyRecordID:              reservation.Record.ID,
			IdempotencyCompletionTTL:         24 * time.Hour,
		},
	)
	if err != nil {
		_ = h.native.FailIdempotency(
			c.Request.Context(),
			reservation.Record.ID,
			services.AgentNativeErrorCode(err),
		)
		h.writeNativeError(c, err)
		return
	}
	h.writeAuthoredKnowledgeResult(c, result)
}

func (h *APIHandler) CreateKnowledgeVersionDraft(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	articleID, ok := parseKnowledgeArticleID(c, "knowledgeArticleID")
	if !ok {
		return
	}
	key, ok := RequireIdempotencyKey(c)
	if !ok {
		return
	}
	body, ok := readJSONBody(c, maxAgentKnowledgeRequestBytes)
	if !ok {
		return
	}
	var request agentKnowledgeVersionDraftRequest
	if err := decodeStrictJSON(body, &request); err != nil {
		WriteProblem(
			c,
			http.StatusBadRequest,
			ProblemInvalidRequest,
			"Knowledge version draft request is invalid",
			false,
		)
		return
	}
	if err := validateAgentKnowledgeVersionDraft(request); err != nil {
		WriteProblem(
			c,
			http.StatusBadRequest,
			ProblemInvalidRequest,
			err.Error(),
			false,
		)
		return
	}
	fingerprint := commandFingerprint(
		http.MethodPost,
		agentRESTProjectPath(
			c,
			"/knowledge/articles/"+articleID+"/drafts",
		),
		0,
		"",
		body,
	)
	requestDigest := digestBytes(fingerprint)
	decision, ok := h.checkAgentKnowledgeAction(
		c,
		principal,
		models.ScopeKnowledgeWrite,
		"knowledge.article.draft.create",
		"knowledge_article",
		articleID,
		true,
		requestDigest,
	)
	if !ok {
		return
	}
	sourceAuthorization, ok := h.authorizeAgentKnowledgeSources(
		c,
		principal,
		request.SourceTicketID,
		request.SourceAttachmentIDs,
		requestDigest,
	)
	if !ok {
		return
	}
	reservation, err := h.native.ReserveIdempotency(
		c.Request.Context(),
		models.ServicePrincipalActor(principal.ID),
		"knowledge.article.draft.create",
		key,
		fingerprint,
		24*time.Hour,
	)
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	if reservation.Replayed {
		h.writeReplayedKnowledgeDocument(
			c,
			reservation.Record,
			articleID,
			decision.ID,
			services.KnowledgeDocumentAuthoredVersionReplay,
		)
		return
	}
	result, err := h.knowledge.CreateAuthoredVersion(
		c.Request.Context(),
		articleID,
		services.CreateAuthoredVersionInput{
			Title:                            request.Title,
			Markdown:                         request.Markdown,
			SourceTicketID:                   request.SourceTicketID,
			SourceAttachmentIDs:              append([]uint(nil), request.SourceAttachmentIDs...),
			PolicyDecisionID:                 decision.ID,
			SourceTicketPolicyDecisionID:     sourceAuthorization.TicketPolicyDecisionID,
			SourceAttachmentPolicyDecisionID: sourceAuthorization.AttachmentPolicyDecisionID,
			IdempotencyRecordID:              reservation.Record.ID,
			IdempotencyCompletionTTL:         24 * time.Hour,
		},
	)
	if err != nil {
		_ = h.native.FailIdempotency(
			c.Request.Context(),
			reservation.Record.ID,
			services.AgentNativeErrorCode(err),
		)
		h.writeNativeError(c, err)
		return
	}
	h.writeAuthoredKnowledgeResult(c, result)
}

type agentKnowledgeCitationResponse struct {
	ID              string  `json:"id"`
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

type agentKnowledgeSearchResponse struct {
	SearchID string                           `json:"search_id"`
	Items    []agentKnowledgeCitationResponse `json:"items"`
}

func (h *APIHandler) SearchKnowledge(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	body, ok := readJSONBody(c, 16<<10)
	if !ok {
		return
	}
	var request agentKnowledgeSearchRequest
	if err := decodeStrictJSON(body, &request); err != nil {
		WriteProblem(
			c,
			http.StatusBadRequest,
			ProblemInvalidRequest,
			"Knowledge search request is invalid",
			false,
		)
		return
	}
	request.Query = strings.TrimSpace(request.Query)
	if request.Query == "" || utf8.RuneCountInString(request.Query) > 2000 {
		WriteProblem(
			c,
			http.StatusBadRequest,
			ProblemInvalidRequest,
			"Knowledge search query must contain between 1 and 2000 characters",
			false,
		)
		return
	}
	if request.Limit == 0 {
		request.Limit = 10
	}
	if request.Limit < 1 || request.Limit > 50 {
		WriteProblem(
			c,
			http.StatusBadRequest,
			ProblemInvalidRequest,
			"Knowledge search limit must be between 1 and 50",
			false,
		)
		return
	}
	decision, ok := h.checkAgentKnowledgeAction(
		c,
		principal,
		models.ScopeKnowledgeRead,
		"knowledge.search",
		"knowledge",
		"*",
		false,
		digestBytes(body),
	)
	if !ok {
		return
	}
	result, err := h.knowledge.Search(
		c.Request.Context(),
		services.KnowledgeSearchInput{
			Query:            request.Query,
			Limit:            request.Limit,
			PolicyDecisionID: decision.ID,
		},
	)
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	items := make(
		[]agentKnowledgeCitationResponse,
		0,
		len(result.Items),
	)
	for _, citation := range result.Items {
		items = append(items, agentKnowledgeCitationResponse{
			ID:              citation.ID,
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
		})
	}
	WriteData(c, http.StatusOK, agentKnowledgeSearchResponse{
		SearchID: result.SearchID,
		Items:    items,
	}, Meta{})
}

func (h *APIHandler) ListTickets(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	limit, err := ParseLimit(c, 20, 100)
	if err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}
	cursor, err := DecodeCursor(c.Query("cursor"))
	if err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}

	policyBatch, err := h.native.PrepareReadPolicyBatch(
		c.Request.Context(),
		services.PolicyCheckInput{
			ServicePrincipalID: principal.ID,
			CredentialID:       principal.CredentialID,
			Scope:              models.ScopeTicketsRead,
			Action:             "ticket.list",
			ResourceType:       "ticket",
			ResourceID:         "*",
			SourceProtocol:     "rest",
		},
	)
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	projectScope, err := services.RequireProjectScope(c.Request.Context())
	if err != nil {
		h.writeNativeError(c, err)
		return
	}

	query := h.db.WithContext(c.Request.Context()).Model(&models.Ticket{}).
		Where(
			"tickets.organization_id = ? AND tickets.project_id = ?",
			projectScope.OrganizationID,
			projectScope.ProjectID,
		).
		Preload("CreatedBy").
		Preload("AssignedTo")
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		query = query.Where("status = ?", status)
	}
	if priority := strings.TrimSpace(c.Query("priority")); priority != "" {
		query = query.Where("priority = ?", priority)
	}
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		query = query.Where("(title ILIKE ? OR description ILIKE ?)", "%"+search+"%", "%"+search+"%")
	}
	if !cursor.CreatedAt.IsZero() {
		cursorID, parseErr := strconv.ParseUint(cursor.ID, 10, 64)
		if parseErr != nil {
			WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, "Invalid cursor", false)
			return
		}
		query = query.Where(
			"(created_at < ?) OR (created_at = ? AND id < ?)",
			cursor.CreatedAt,
			cursor.CreatedAt,
			cursorID,
		)
	}

	candidateBudget := boundedListCandidateBudget(limit)
	var candidates []models.Ticket
	if err := query.
		Order("created_at DESC, id DESC").
		Limit(candidateBudget + 1).
		Find(&candidates).Error; err != nil {
		h.writeNativeError(c, err)
		return
	}

	tickets := make([]models.Ticket, 0, limit)
	scanned := 0
	for scanned < len(candidates) && scanned < candidateBudget && len(tickets) < limit {
		ticket := &candidates[scanned]
		scanned++
		allowed, checkErr := policyBatch.Allows(services.PolicyCheckInput{
			ServicePrincipalID: principal.ID,
			CredentialID:       principal.CredentialID,
			Scope:              models.ScopeTicketsRead,
			Action:             "ticket.read",
			ResourceType:       "ticket",
			ResourceID:         strconv.FormatUint(uint64(ticket.ID), 10),
			SourceProtocol:     "rest",
		})
		if checkErr != nil {
			h.writeNativeError(c, checkErr)
			return
		}
		if allowed {
			tickets = append(tickets, *ticket)
		}
	}

	// A cursor advances past the final raw candidate examined, including a
	// denied candidate. This prevents deny-heavy pages from repeatedly scanning
	// the same rows. has_more describes the raw candidate stream; a following
	// page may therefore contain zero visible items.
	hasMore := scanned < len(candidates)
	items := make([]*models.TicketResponse, 0, len(tickets))
	for i := range tickets {
		items = append(items, tickets[i].ToResponse())
	}
	meta := Meta{HasMore: hasMore}
	if hasMore && scanned > 0 {
		last := candidates[scanned-1]
		meta.NextCursor = EncodeCursor(Cursor{
			CreatedAt: last.CreatedAt,
			ID:        strconv.FormatUint(uint64(last.ID), 10),
		})
	}
	if _, err := policyBatch.RecordSummary(c.Request.Context(), map[string]any{
		"candidate_budget":   candidateBudget,
		"candidates_scanned": scanned,
		"items_returned":     len(items),
		"items_filtered":     scanned - len(items),
		"has_more":           hasMore,
		"cursor_semantics":   "last_examined_candidate",
	}); err != nil {
		h.writeNativeError(c, err)
		return
	}
	WriteData(c, http.StatusOK, items, meta)
}

const maxListCandidateBudget = 500

func boundedListCandidateBudget(limit int) int {
	budget := limit * 5
	if budget < limit {
		budget = limit
	}
	if budget > maxListCandidateBudget {
		budget = maxListCandidateBudget
	}
	return budget
}

func (h *APIHandler) GetTicket(c *gin.Context) {
	ticket, ok := h.loadAuthorizedTicket(c)
	if !ok {
		return
	}
	c.Header("ETag", httpcontract.FormatETag(ticket.Version))
	WriteData(c, http.StatusOK, ticket.ToResponse(), Meta{})
}

func (h *APIHandler) CreateTicket(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	key, ok := RequireIdempotencyKey(c)
	if !ok {
		return
	}
	body, ok := readJSONBody(c, 1<<20)
	if !ok {
		return
	}
	var request struct {
		Title                string                `json:"title"`
		Description          string                `json:"description"`
		Type                 models.TicketType     `json:"type"`
		Priority             models.TicketPriority `json:"priority"`
		RequestTypeVersionID string                `json:"request_type_version_id"`
		WorkflowVersionID    string                `json:"workflow_version_id"`
		Tags                 []string              `json:"tags"`
		AgentContext         *models.AgentContext  `json:"agent_context"`
		CategoryID           *uint                 `json:"category_id"`
		DueDate              *time.Time            `json:"due_date"`
	}
	if err := decodeStrictJSON(body, &request); err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}
	request.RequestTypeVersionID, ok =
		normalizeMachineConfigurationVersionID(request.RequestTypeVersionID)
	if !ok {
		WriteProblem(
			c,
			http.StatusBadRequest,
			ProblemInvalidRequest,
			"request_type_version_id must be a canonical UUID",
			false,
		)
		return
	}
	request.WorkflowVersionID, ok =
		normalizeMachineConfigurationVersionID(request.WorkflowVersionID)
	if !ok {
		WriteProblem(
			c,
			http.StatusBadRequest,
			ProblemInvalidRequest,
			"workflow_version_id must be a canonical UUID",
			false,
		)
		return
	}
	fingerprint := commandFingerprint(
		http.MethodPost,
		agentRESTProjectPath(c, "/tickets"),
		0,
		"",
		body,
	)
	actor := models.ServicePrincipalActor(principal.ID)
	reservation, err := h.native.ReserveIdempotency(
		c.Request.Context(), actor, "ticket.create", key, fingerprint, 24*time.Hour,
	)
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	tokenScopesValue, _ := c.Get(agentauth.ContextScopes)
	tokenScopes, ok := tokenScopesValue.([]string)
	if !ok {
		h.writeNativeError(c, services.ErrInvalidScope)
		return
	}
	authorization := services.NativeCommandAuthorizationInput{
		Kind:           services.NativeCommandTicketCreate,
		Actor:          actor,
		CredentialID:   principal.CredentialID,
		TokenScopes:    append([]string(nil), tokenScopes...),
		RequestDigest:  digestBytes(fingerprint),
		SourceProtocol: string(services.SourceProtocolAgentREST),
	}
	if reservation.Replayed {
		if err := h.native.
			AuthorizeNativeCommandReplayInShortProjectTransactions(
				c.Request.Context(),
				authorization,
			); err != nil {
			h.writeNativeError(c, err)
			return
		}
		h.writeReplayedTicket(c, reservation.Record, http.StatusCreated)
		return
	}

	authorizedContext, err :=
		h.native.AuthorizeNativeCommandInShortProjectTransactions(
			c.Request.Context(),
			authorization,
		)
	if err != nil {
		_ = h.native.FailIdempotency(
			c.Request.Context(),
			reservation.Record.ID,
			services.AgentNativeErrorCode(err),
		)
		h.writeNativeError(c, err)
		return
	}
	result, err := runMachineTicketCreateDatabaseCommand(
		authorizedContext,
		h.db,
		h.native,
		services.NativeTicketCreateInput{
			Request: models.TicketCreateRequest{
				Title:                request.Title,
				Description:          request.Description,
				Type:                 request.Type,
				Priority:             request.Priority,
				Source:               models.TicketSourceAgent,
				RequestTypeVersionID: request.RequestTypeVersionID,
				WorkflowVersionID:    request.WorkflowVersionID,
				Tags:                 models.StringList(request.Tags),
				AgentContext:         request.AgentContext,
				CategoryID:           request.CategoryID,
				DueDate:              request.DueDate,
			},
			Actor:               actor,
			CredentialID:        principal.CredentialID,
			SourceProtocol:      string(services.SourceProtocolAgentREST),
			RequestDigest:       digestBytes(fingerprint),
			TrustLevel:          models.TicketTrustLevelUntrusted,
			TraceID:             RequestID(c),
			CorrelationID:       c.GetHeader("X-Correlation-ID"),
			IdempotencyRecordID: reservation.Record.ID,
		},
	)
	if err != nil {
		_ = h.native.FailIdempotency(c.Request.Context(), reservation.Record.ID, services.AgentNativeErrorCode(err))
		h.writeNativeError(c, err)
		return
	}
	h.publishTicket(c, result.Ticket.ID)
	c.Header("ETag", httpcontract.FormatETag(result.Ticket.Version))
	WriteReceipt(c, http.StatusCreated, result.Ticket.ToResponse(), receiptFromService(result.Receipt))
}

func (h *APIHandler) UpdateTicket(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	ticketID, ok := parsePathUint(c, "id")
	if !ok {
		return
	}
	expectedVersion, err := httpcontract.ParseIfMatch(c.GetHeader("If-Match"))
	if err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}
	key, ok := RequireIdempotencyKey(c)
	if !ok {
		return
	}
	body, ok := readJSONBody(c, 1<<20)
	if !ok {
		return
	}
	changes, err := decodeOrdinaryTicketPatch(body)
	if err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}
	leaseID, ok := requireTicketLeaseHeader(c)
	if !ok {
		return
	}
	fingerprint := commandFingerprint(
		http.MethodPatch,
		agentRESTProjectPath(c, fmt.Sprintf("/tickets/%d", ticketID)),
		expectedVersion,
		leaseID,
		body,
	)
	actor := models.ServicePrincipalActor(principal.ID)
	reservation, err := h.native.ReserveIdempotency(
		c.Request.Context(), actor, "ticket.update", key, fingerprint, 24*time.Hour,
	)
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	authorization := services.NativeCommandAuthorizationInput{
		Kind:          services.NativeCommandTicketUpdate,
		TicketID:      ticketID,
		RequestDigest: digestBytes(fingerprint),
	}
	if reservation.Replayed {
		if _, _, err := h.authorizeAgentRESTNativeCommand(
			c,
			principal,
			authorization,
			true,
		); err != nil {
			h.writeNativeError(c, err)
			return
		}
		h.writeReplayedTicket(c, reservation.Record, http.StatusOK)
		return
	}
	authorizedContext, tokenScopes, err :=
		h.authorizeAgentRESTNativeCommand(
			c,
			principal,
			authorization,
			false,
		)
	if err != nil {
		_ = h.native.FailIdempotency(
			c.Request.Context(),
			reservation.Record.ID,
			services.AgentNativeErrorCode(err),
		)
		h.writeNativeError(c, err)
		return
	}
	result, err := runMachineProjectCommand(
		authorizedContext,
		h.native,
		tokenScopes,
		models.ProjectKey(c.Param("projectKey")),
		func(
			scopedContext context.Context,
		) (*services.VersionedTicketUpdateResult, error) {
			return h.native.UpdateTicketVersion(
				scopedContext,
				services.VersionedTicketUpdateInput{
					TicketID:        ticketID,
					ExpectedVersion: expectedVersion,
					LeaseID:         leaseID,
					Actor:           actor,
					CredentialID:    principal.CredentialID,
					SourceProtocol: string(
						services.SourceProtocolAgentREST,
					),
					RequestDigest:       digestBytes(fingerprint),
					Changes:             changes,
					RequiredScope:       models.ScopeTicketsUpdate,
					Action:              "ticket.update",
					IsRisky:             false,
					TraceID:             RequestID(c),
					CorrelationID:       c.GetHeader("X-Correlation-ID"),
					IdempotencyRecordID: reservation.Record.ID,
				},
			)
		},
	)
	if err != nil {
		_ = h.native.FailIdempotency(c.Request.Context(), reservation.Record.ID, services.AgentNativeErrorCode(err))
		h.writeNativeError(c, err)
		return
	}
	h.publishTicket(c, ticketID)
	c.Header("ETag", httpcontract.FormatETag(result.Ticket.Version))
	WriteReceipt(c, http.StatusOK, result.Ticket.ToResponse(), receiptFromService(result.Receipt))
}

type ticketCommandRequestContext struct {
	principal       authenticatedPrincipal
	ticketID        uint
	expectedVersion uint64
	idempotencyKey  string
	leaseID         string
	correlationID   string
	body            []byte
}

func (h *APIHandler) prepareTicketCommand(
	c *gin.Context,
) (ticketCommandRequestContext, bool) {
	principal, ok := h.principal(c)
	if !ok {
		return ticketCommandRequestContext{}, false
	}
	ticketID, ok := parsePathUint(c, "id")
	if !ok {
		return ticketCommandRequestContext{}, false
	}
	expectedVersion, err := httpcontract.ParseIfMatch(c.GetHeader("If-Match"))
	if err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return ticketCommandRequestContext{}, false
	}
	idempotencyKey, ok := RequireIdempotencyKey(c)
	if !ok {
		return ticketCommandRequestContext{}, false
	}
	leaseID, ok := requireTicketLeaseHeader(c)
	if !ok {
		return ticketCommandRequestContext{}, false
	}
	correlationID, ok := requireCommandCorrelationID(c)
	if !ok {
		return ticketCommandRequestContext{}, false
	}
	body, ok := readJSONBody(c, 64<<10)
	if !ok {
		return ticketCommandRequestContext{}, false
	}
	return ticketCommandRequestContext{
		principal:       principal,
		ticketID:        ticketID,
		expectedVersion: expectedVersion,
		idempotencyKey:  idempotencyKey,
		leaseID:         leaseID,
		correlationID:   correlationID,
		body:            body,
	}, true
}

func (h *APIHandler) AssignTicket(c *gin.Context) {
	commandRequest, ok := h.prepareTicketCommand(c)
	if !ok {
		return
	}
	request, err := decodeTicketAssignmentCommand(commandRequest.body)
	if err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}

	path := fmt.Sprintf(
		agentRESTProjectPath(c, "/tickets/%d/commands/assign"),
		commandRequest.ticketID,
	)
	fingerprint := commandFingerprint(
		http.MethodPost,
		path,
		commandRequest.expectedVersion,
		commandRequest.leaseID,
		commandRequest.body,
	)
	actor := models.ServicePrincipalActor(commandRequest.principal.ID)
	reservation, err := h.native.ReserveIdempotency(
		c.Request.Context(),
		actor,
		"ticket.assign",
		commandRequest.idempotencyKey,
		fingerprint,
		24*time.Hour,
	)
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	authorization := services.NativeCommandAuthorizationInput{
		Kind:          services.NativeCommandTicketAssign,
		TicketID:      commandRequest.ticketID,
		Assignee:      request.Assignee,
		RequestDigest: digestBytes(fingerprint),
	}
	if reservation.Replayed {
		if _, _, err := h.authorizeAgentRESTNativeCommand(
			c,
			commandRequest.principal,
			authorization,
			true,
		); err != nil {
			h.writeNativeError(c, err)
			return
		}
		h.writeReplayedTicket(c, reservation.Record, http.StatusOK)
		return
	}

	authorizedContext, tokenScopes, err :=
		h.authorizeAgentRESTNativeCommand(
			c,
			commandRequest.principal,
			authorization,
			false,
		)
	if err != nil {
		_ = h.native.FailIdempotency(
			c.Request.Context(),
			reservation.Record.ID,
			services.AgentNativeErrorCode(err),
		)
		h.writeNativeError(c, err)
		return
	}
	result, err := runMachineProjectCommand(
		authorizedContext,
		h.native,
		tokenScopes,
		models.ProjectKey(c.Param("projectKey")),
		func(
			scopedContext context.Context,
		) (*services.VersionedTicketUpdateResult, error) {
			return h.native.AssignTicket(
				scopedContext,
				services.AssignTicketCommand{
					TicketID:        commandRequest.ticketID,
					ExpectedVersion: commandRequest.expectedVersion,
					LeaseID:         commandRequest.leaseID,
					Actor:           actor,
					Assignee:        request.Assignee,
					CredentialID:    commandRequest.principal.CredentialID,
					SourceProtocol: string(
						services.SourceProtocolAgentREST,
					),
					RequestDigest:       digestBytes(fingerprint),
					Reason:              request.Reason,
					TraceID:             RequestID(c),
					CorrelationID:       commandRequest.correlationID,
					IdempotencyRecordID: reservation.Record.ID,
				},
			)
		},
	)
	if err != nil {
		_ = h.native.FailIdempotency(
			c.Request.Context(),
			reservation.Record.ID,
			services.AgentNativeErrorCode(err),
		)
		h.writeNativeError(c, err)
		return
	}
	h.writeTicketCommandResult(c, commandRequest.ticketID, result)
}

func (h *APIHandler) TransitionTicket(c *gin.Context) {
	commandRequest, ok := h.prepareTicketCommand(c)
	if !ok {
		return
	}
	request, err := decodeTicketTransitionCommand(commandRequest.body)
	if err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}

	path := fmt.Sprintf(
		agentRESTProjectPath(c, "/tickets/%d/commands/transition"),
		commandRequest.ticketID,
	)
	fingerprint := commandFingerprint(
		http.MethodPost,
		path,
		commandRequest.expectedVersion,
		commandRequest.leaseID,
		commandRequest.body,
	)
	actor := models.ServicePrincipalActor(commandRequest.principal.ID)
	reservation, err := h.native.ReserveIdempotency(
		c.Request.Context(),
		actor,
		"ticket.transition",
		commandRequest.idempotencyKey,
		fingerprint,
		24*time.Hour,
	)
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	authorization := services.NativeCommandAuthorizationInput{
		Kind:          services.NativeCommandTicketTransit,
		TicketID:      commandRequest.ticketID,
		RequestDigest: digestBytes(fingerprint),
	}
	if reservation.Replayed {
		if _, _, err := h.authorizeAgentRESTNativeCommand(
			c,
			commandRequest.principal,
			authorization,
			true,
		); err != nil {
			h.writeNativeError(c, err)
			return
		}
		h.writeReplayedTicket(c, reservation.Record, http.StatusOK)
		return
	}

	authorizedContext, tokenScopes, err :=
		h.authorizeAgentRESTNativeCommand(
			c,
			commandRequest.principal,
			authorization,
			false,
		)
	if err != nil {
		_ = h.native.FailIdempotency(
			c.Request.Context(),
			reservation.Record.ID,
			services.AgentNativeErrorCode(err),
		)
		h.writeNativeError(c, err)
		return
	}
	result, err := runMachineProjectCommand(
		authorizedContext,
		h.native,
		tokenScopes,
		models.ProjectKey(c.Param("projectKey")),
		func(
			scopedContext context.Context,
		) (*services.VersionedTicketUpdateResult, error) {
			return h.native.TransitionTicket(
				scopedContext,
				services.TransitionTicketCommand{
					TicketID:        commandRequest.ticketID,
					ExpectedVersion: commandRequest.expectedVersion,
					LeaseID:         commandRequest.leaseID,
					Actor:           actor,
					Status:          request.Status,
					CredentialID:    commandRequest.principal.CredentialID,
					SourceProtocol: string(
						services.SourceProtocolAgentREST,
					),
					RequestDigest:       digestBytes(fingerprint),
					Reason:              request.Reason,
					TraceID:             RequestID(c),
					CorrelationID:       commandRequest.correlationID,
					IdempotencyRecordID: reservation.Record.ID,
				},
			)
		},
	)
	if err != nil {
		_ = h.native.FailIdempotency(
			c.Request.Context(),
			reservation.Record.ID,
			services.AgentNativeErrorCode(err),
		)
		h.writeNativeError(c, err)
		return
	}
	h.writeTicketCommandResult(c, commandRequest.ticketID, result)
}

func (h *APIHandler) EscalateTicket(c *gin.Context) {
	commandRequest, ok := h.prepareTicketCommand(c)
	if !ok {
		return
	}
	request, err := decodeTicketEscalationCommand(commandRequest.body)
	if err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}
	if request.Assignee != nil &&
		!agentauth.HasScopes(c, models.ScopeTicketsAssign) {
		WriteProblem(
			c,
			http.StatusForbidden,
			ProblemInsufficientScope,
			"The access token does not grant tickets:assign",
			false,
		)
		return
	}

	path := fmt.Sprintf(
		agentRESTProjectPath(c, "/tickets/%d/commands/escalate"),
		commandRequest.ticketID,
	)
	fingerprint := commandFingerprint(
		http.MethodPost,
		path,
		commandRequest.expectedVersion,
		commandRequest.leaseID,
		commandRequest.body,
	)
	actor := models.ServicePrincipalActor(commandRequest.principal.ID)
	reservation, err := h.native.ReserveIdempotency(
		c.Request.Context(),
		actor,
		"ticket.escalate",
		commandRequest.idempotencyKey,
		fingerprint,
		24*time.Hour,
	)
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	authorization := services.NativeCommandAuthorizationInput{
		Kind:          services.NativeCommandTicketEscalate,
		TicketID:      commandRequest.ticketID,
		Assignee:      request.Assignee,
		RequestDigest: digestBytes(fingerprint),
	}
	if reservation.Replayed {
		if _, _, err := h.authorizeAgentRESTNativeCommand(
			c,
			commandRequest.principal,
			authorization,
			true,
		); err != nil {
			h.writeNativeError(c, err)
			return
		}
		h.writeReplayedTicket(c, reservation.Record, http.StatusOK)
		return
	}

	authorizedContext, tokenScopes, err :=
		h.authorizeAgentRESTNativeCommand(
			c,
			commandRequest.principal,
			authorization,
			false,
		)
	if err != nil {
		_ = h.native.FailIdempotency(
			c.Request.Context(),
			reservation.Record.ID,
			services.AgentNativeErrorCode(err),
		)
		h.writeNativeError(c, err)
		return
	}
	result, err := runMachineProjectCommand(
		authorizedContext,
		h.native,
		tokenScopes,
		models.ProjectKey(c.Param("projectKey")),
		func(
			scopedContext context.Context,
		) (*services.VersionedTicketUpdateResult, error) {
			return h.native.EscalateTicket(
				scopedContext,
				services.EscalateTicketCommand{
					TicketID:        commandRequest.ticketID,
					ExpectedVersion: commandRequest.expectedVersion,
					LeaseID:         commandRequest.leaseID,
					Actor:           actor,
					Priority:        request.Priority,
					Assignee:        request.Assignee,
					CredentialID:    commandRequest.principal.CredentialID,
					SourceProtocol: string(
						services.SourceProtocolAgentREST,
					),
					RequestDigest:              digestBytes(fingerprint),
					Reason:                     request.Reason,
					TraceID:                    RequestID(c),
					CorrelationID:              commandRequest.correlationID,
					IdempotencyRecordID:        reservation.Record.ID,
					IdempotencyCompletionTTL:   24 * time.Hour,
					TransitionPolicyDecisionID: "",
					AssignmentPolicyDecisionID: "",
				},
			)
		},
	)
	if err != nil {
		_ = h.native.FailIdempotency(
			c.Request.Context(),
			reservation.Record.ID,
			services.AgentNativeErrorCode(err),
		)
		h.writeNativeError(c, err)
		return
	}
	h.writeTicketCommandResult(c, commandRequest.ticketID, result)
}

func (h *APIHandler) writeTicketCommandResult(
	c *gin.Context,
	ticketID uint,
	result *services.VersionedTicketUpdateResult,
) {
	h.publishTicket(c, ticketID)
	c.Header("ETag", httpcontract.FormatETag(result.Ticket.Version))
	WriteReceipt(
		c,
		http.StatusOK,
		result.Ticket.ToResponse(),
		receiptFromService(result.Receipt),
	)
}

func (h *APIHandler) ClaimTicket(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	ticketID, ok := parsePathUint(c, "id")
	if !ok {
		return
	}
	expectedVersion, err := httpcontract.ParseIfMatch(c.GetHeader("If-Match"))
	if err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}
	key, ok := RequireIdempotencyKey(c)
	if !ok {
		return
	}
	body, ok := readJSONBody(c, 16<<10)
	if !ok {
		return
	}
	var request struct {
		TTLSeconds int `json:"ttl_seconds"`
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := decodeStrictJSON(body, &request); err != nil {
			WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
			return
		}
	}
	fingerprint := commandFingerprint(
		http.MethodPost,
		agentRESTProjectPath(c, fmt.Sprintf("/tickets/%d/claim", ticketID)),
		expectedVersion,
		"",
		body,
	)
	actor := models.ServicePrincipalActor(principal.ID)
	reservation, err := h.native.ReserveIdempotency(
		c.Request.Context(), actor, "ticket.claim", key, fingerprint, 24*time.Hour,
	)
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	authorization := services.NativeCommandAuthorizationInput{
		Kind:          services.NativeCommandTicketClaim,
		TicketID:      ticketID,
		RequestDigest: digestBytes(fingerprint),
	}
	if reservation.Replayed {
		if _, _, err := h.authorizeAgentRESTNativeCommand(
			c,
			principal,
			authorization,
			true,
		); err != nil {
			h.writeNativeError(c, err)
			return
		}
		h.writeReplayedLease(c, reservation.Record)
		return
	}
	authorizedContext, tokenScopes, err :=
		h.authorizeAgentRESTNativeCommand(
			c,
			principal,
			authorization,
			false,
		)
	if err != nil {
		_ = h.native.FailIdempotency(
			c.Request.Context(),
			reservation.Record.ID,
			services.AgentNativeErrorCode(err),
		)
		h.writeNativeError(c, err)
		return
	}
	result, err := runMachineProjectCommand(
		authorizedContext,
		h.native,
		tokenScopes,
		models.ProjectKey(c.Param("projectKey")),
		func(
			scopedContext context.Context,
		) (*services.TicketLeaseCommandResult, error) {
			return h.native.ClaimTicketLeaseCommand(
				scopedContext,
				services.ClaimTicketLeaseCommandInput{
					TicketID:        ticketID,
					Actor:           actor,
					ExpectedVersion: expectedVersion,
					TTL:             time.Duration(request.TTLSeconds) * time.Second,
					CredentialID:    principal.CredentialID,
					SourceProtocol: string(
						services.SourceProtocolAgentREST,
					),
					RequestDigest:       digestBytes(fingerprint),
					IdempotencyRecordID: reservation.Record.ID,
					TraceID:             RequestID(c),
					CorrelationID:       c.GetHeader("X-Correlation-ID"),
				},
			)
		},
	)
	if err != nil {
		_ = h.native.FailIdempotency(c.Request.Context(), reservation.Record.ID, services.AgentNativeErrorCode(err))
		h.writeNativeError(c, err)
		return
	}
	h.publishTicket(c, ticketID)
	WriteReceipt(c, http.StatusOK, leaseResponseData(result.Lease), receiptFromService(result.Receipt))
}

func (h *APIHandler) HeartbeatLease(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	expectedVersion, err := httpcontract.ParseIfMatch(c.GetHeader("If-Match"))
	if err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}
	key, ok := RequireIdempotencyKey(c)
	if !ok {
		return
	}
	requestBody, ok := readJSONBody(c, 16<<10)
	if !ok {
		return
	}
	var request struct {
		TTLSeconds int `json:"ttl_seconds"`
	}
	if len(bytes.TrimSpace(requestBody)) > 0 {
		if err := decodeStrictJSON(requestBody, &request); err != nil {
			WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
			return
		}
	}
	body, _ := json.Marshal(gin.H{
		"lease_id":         c.Param("id"),
		"expected_version": expectedVersion,
		"ttl_seconds":      request.TTLSeconds,
	})
	fingerprint := commandFingerprint(
		http.MethodPost,
		agentRESTProjectPath(c, "/leases/"+c.Param("id")+"/heartbeat"),
		expectedVersion,
		c.Param("id"),
		body,
	)
	actor := models.ServicePrincipalActor(principal.ID)
	reservation, err := h.native.ReserveIdempotency(
		c.Request.Context(), actor, "ticket.lease.heartbeat", key, fingerprint, 24*time.Hour,
	)
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	authorization := services.NativeCommandAuthorizationInput{
		Kind:          services.NativeCommandLeaseHeartbeat,
		LeaseID:       c.Param("id"),
		RequestDigest: digestBytes(fingerprint),
	}
	if reservation.Replayed {
		replayedTicketID, parseErr := safeconv.ParsePositiveUint(
			reservation.Record.ResourceID,
		)
		if parseErr != nil {
			h.writeNativeError(c, services.ErrIdempotencyConflict)
			return
		}
		authorization.TicketID = replayedTicketID
		if _, _, err := h.authorizeAgentRESTNativeCommand(
			c,
			principal,
			authorization,
			true,
		); err != nil {
			h.writeNativeError(c, err)
			return
		}
		h.writeReplayedLease(c, reservation.Record)
		return
	}
	authorizedContext, tokenScopes, err :=
		h.authorizeAgentRESTNativeCommand(
			c,
			principal,
			authorization,
			false,
		)
	if err != nil {
		_ = h.native.FailIdempotency(
			c.Request.Context(),
			reservation.Record.ID,
			services.AgentNativeErrorCode(err),
		)
		h.writeNativeError(c, err)
		return
	}
	result, err := runMachineProjectCommand(
		authorizedContext,
		h.native,
		tokenScopes,
		models.ProjectKey(c.Param("projectKey")),
		func(
			scopedContext context.Context,
		) (*services.TicketLeaseCommandResult, error) {
			return h.native.HeartbeatTicketLeaseCommand(
				scopedContext,
				services.HeartbeatTicketLeaseCommandInput{
					LeaseID:         c.Param("id"),
					Actor:           actor,
					ExpectedVersion: expectedVersion,
					TTL:             time.Duration(request.TTLSeconds) * time.Second,
					CredentialID:    principal.CredentialID,
					SourceProtocol: string(
						services.SourceProtocolAgentREST,
					),
					RequestDigest:       digestBytes(fingerprint),
					IdempotencyRecordID: reservation.Record.ID,
					TraceID:             RequestID(c),
					CorrelationID:       c.GetHeader("X-Correlation-ID"),
				},
			)
		},
	)
	if err != nil {
		_ = h.native.FailIdempotency(c.Request.Context(), reservation.Record.ID, services.AgentNativeErrorCode(err))
		h.writeNativeError(c, err)
		return
	}
	h.publishTicket(c, result.Lease.TicketID)
	WriteReceipt(c, http.StatusOK, leaseResponseData(result.Lease), receiptFromService(result.Receipt))
}

func (h *APIHandler) ReleaseLease(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	key, ok := RequireIdempotencyKey(c)
	if !ok {
		return
	}
	fingerprint := commandFingerprint(
		http.MethodDelete,
		agentRESTProjectPath(c, "/leases/"+c.Param("id")),
		0,
		c.Param("id"),
		nil,
	)
	actor := models.ServicePrincipalActor(principal.ID)
	reservation, err := h.native.ReserveIdempotency(
		c.Request.Context(), actor, "ticket.lease.release", key, fingerprint, 24*time.Hour,
	)
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	authorization := services.NativeCommandAuthorizationInput{
		Kind:          services.NativeCommandLeaseRelease,
		LeaseID:       c.Param("id"),
		RequestDigest: digestBytes(fingerprint),
	}
	if reservation.Replayed {
		replayedTicketID, parseErr := safeconv.ParsePositiveUint(
			reservation.Record.ResourceID,
		)
		if parseErr != nil {
			h.writeNativeError(c, services.ErrIdempotencyConflict)
			return
		}
		authorization.TicketID = replayedTicketID
		if _, _, err := h.authorizeAgentRESTNativeCommand(
			c,
			principal,
			authorization,
			true,
		); err != nil {
			h.writeNativeError(c, err)
			return
		}
		h.writeReplayedLease(c, reservation.Record)
		return
	}
	authorizedContext, tokenScopes, err :=
		h.authorizeAgentRESTNativeCommand(
			c,
			principal,
			authorization,
			false,
		)
	if err != nil {
		_ = h.native.FailIdempotency(
			c.Request.Context(),
			reservation.Record.ID,
			services.AgentNativeErrorCode(err),
		)
		h.writeNativeError(c, err)
		return
	}
	result, err := runMachineProjectCommand(
		authorizedContext,
		h.native,
		tokenScopes,
		models.ProjectKey(c.Param("projectKey")),
		func(
			scopedContext context.Context,
		) (*services.TicketLeaseCommandResult, error) {
			return h.native.ReleaseTicketLeaseCommand(
				scopedContext,
				services.ReleaseTicketLeaseCommandInput{
					LeaseID:      c.Param("id"),
					Actor:        actor,
					Reason:       "released by REST client",
					CredentialID: principal.CredentialID,
					SourceProtocol: string(
						services.SourceProtocolAgentREST,
					),
					RequestDigest:       digestBytes(fingerprint),
					IdempotencyRecordID: reservation.Record.ID,
					TraceID:             RequestID(c),
					CorrelationID:       c.GetHeader("X-Correlation-ID"),
				},
			)
		},
	)
	if err != nil {
		_ = h.native.FailIdempotency(c.Request.Context(), reservation.Record.ID, services.AgentNativeErrorCode(err))
		h.writeNativeError(c, err)
		return
	}
	h.publishTicket(c, result.Lease.TicketID)
	WriteReceipt(c, http.StatusOK, leaseResponseData(result.Lease), receiptFromService(result.Receipt))
}

func (h *APIHandler) CreateComment(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	ticketID, ok := parsePathUint(c, "id")
	if !ok {
		return
	}
	expectedVersion, err := httpcontract.ParseIfMatch(c.GetHeader("If-Match"))
	if err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}
	leaseID, ok := requireTicketLeaseHeader(c)
	if !ok {
		return
	}
	key, ok := RequireIdempotencyKey(c)
	if !ok {
		return
	}
	body, ok := readJSONBody(c, 1<<20)
	if !ok {
		return
	}
	var request struct {
		Content          string             `json:"content"`
		ContentType      string             `json:"content_type"`
		Type             models.CommentType `json:"type"`
		RationaleSummary string             `json:"rationale_summary"`
		Evidence         []string           `json:"evidence"`
		InputSources     []string           `json:"input_sources"`
	}
	if err := decodeStrictJSON(body, &request); err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}
	fingerprint := commandFingerprint(
		http.MethodPost,
		agentRESTProjectPath(c, fmt.Sprintf("/tickets/%d/comments", ticketID)),
		expectedVersion,
		leaseID,
		body,
	)
	actor := models.ServicePrincipalActor(principal.ID)
	reservation, err := h.native.ReserveIdempotency(
		c.Request.Context(), actor, "ticket.comment.create", key, fingerprint, 24*time.Hour,
	)
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	authorization := services.NativeCommandAuthorizationInput{
		Kind:          services.NativeCommandCommentCreate,
		TicketID:      ticketID,
		RequestDigest: digestBytes(fingerprint),
	}
	if reservation.Replayed {
		if _, _, err := h.authorizeAgentRESTNativeCommand(
			c,
			principal,
			authorization,
			true,
		); err != nil {
			h.writeNativeError(c, err)
			return
		}
		h.writeReplayedComment(c, reservation.Record)
		return
	}
	authorizedContext, tokenScopes, err :=
		h.authorizeAgentRESTNativeCommand(
			c,
			principal,
			authorization,
			false,
		)
	if err != nil {
		_ = h.native.FailIdempotency(
			c.Request.Context(),
			reservation.Record.ID,
			services.AgentNativeErrorCode(err),
		)
		h.writeNativeError(c, err)
		return
	}
	result, err := runMachineProjectCommand(
		authorizedContext,
		h.native,
		tokenScopes,
		models.ProjectKey(c.Param("projectKey")),
		func(
			scopedContext context.Context,
		) (*services.NativeCommentResult, error) {
			return h.native.CreateComment(
				scopedContext,
				services.NativeCommentInput{
					TicketID:        ticketID,
					ExpectedVersion: expectedVersion,
					LeaseID:         leaseID,
					Actor:           actor,
					CredentialID:    principal.CredentialID,
					SourceProtocol: string(
						services.SourceProtocolAgentREST,
					),
					RequestDigest:       digestBytes(fingerprint),
					Content:             request.Content,
					ContentType:         request.ContentType,
					Type:                request.Type,
					Reason:              request.RationaleSummary,
					EvidenceRefs:        request.Evidence,
					InputSources:        request.InputSources,
					TraceID:             RequestID(c),
					IdempotencyRecordID: reservation.Record.ID,
				},
			)
		},
	)
	if err != nil {
		_ = h.native.FailIdempotency(c.Request.Context(), reservation.Record.ID, services.AgentNativeErrorCode(err))
		h.writeNativeError(c, err)
		return
	}
	h.publishTicket(c, ticketID)
	c.Header("ETag", httpcontract.FormatETag(result.Receipt.ResourceVersion))
	WriteReceipt(c, http.StatusCreated, result.Comment.ToResponse(), receiptFromService(result.Receipt))
}

func (h *APIHandler) StoreAttachment(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	ticketID, ok := parsePathUint(c, "id")
	if !ok {
		return
	}
	expectedVersion, err := httpcontract.ParseIfMatch(c.GetHeader("If-Match"))
	if err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}
	leaseID, ok := requireTicketLeaseHeader(c)
	if !ok {
		return
	}
	key, ok := RequireIdempotencyKey(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		h.maxAttachmentBytes+(1<<20),
	)
	if err := c.Request.ParseMultipartForm(h.maxAttachmentBytes); err != nil {
		WriteProblem(c, http.StatusRequestEntityTooLarge, ProblemAttachmentRejected, "Invalid or oversized multipart body", false)
		return
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, "Multipart field file is required", false)
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, h.maxAttachmentBytes+1))
	if err != nil || int64(len(content)) > h.maxAttachmentBytes {
		WriteProblem(c, http.StatusRequestEntityTooLarge, ProblemAttachmentRejected, "Attachment exceeds the configured limit", false)
		return
	}
	idempotencyBody, _ := json.Marshal(gin.H{
		"ticket_id":    ticketID,
		"file_name":    header.Filename,
		"size":         len(content),
		"sha256":       fmt.Sprintf("%x", sha256.Sum256(content)),
		"content_type": header.Header.Get("Content-Type"),
		"description":  c.PostForm("description"),
		"visibility":   c.PostForm("visibility"),
	})
	fingerprint := commandFingerprint(
		http.MethodPost,
		agentRESTProjectPath(c, fmt.Sprintf("/tickets/%d/attachments", ticketID)),
		expectedVersion,
		leaseID,
		idempotencyBody,
	)
	actor := models.ServicePrincipalActor(principal.ID)
	attachmentInput := services.NativeAttachmentInput{
		TicketID:        ticketID,
		ExpectedVersion: expectedVersion,
		LeaseID:         leaseID,
		Actor:           actor,
		CredentialID:    principal.CredentialID,
		SourceProtocol: string(
			services.SourceProtocolAgentREST,
		),
		RequestDigest: digestBytes(fingerprint),
		OriginalName:  header.Filename,
		ContentType: header.Header.Get(
			"Content-Type",
		),
		Description: c.PostForm("description"),
		IsPublic: c.PostForm("visibility") ==
			"public",
		TraceID:       RequestID(c),
		CorrelationID: c.GetHeader("X-Correlation-ID"),
	}
	reservation, err := h.native.ReserveIdempotency(
		c.Request.Context(), actor, "ticket.attachment.create", key, fingerprint, 24*time.Hour,
	)
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	if reservation.Replayed {
		tokenScopesValue, _ := c.Get(agentauth.ContextScopes)
		tokenScopes, ok := tokenScopesValue.([]string)
		if !ok {
			h.writeNativeError(c, services.ErrInvalidScope)
			return
		}
		replayAuthorization, replayErr :=
			h.native.PrepareAttachmentReplayAuthorization(
				c.Request.Context(),
				attachmentInput,
				append([]string(nil), tokenScopes...),
			)
		if replayErr != nil {
			h.writeNativeError(c, replayErr)
			return
		}
		replay, replayErr :=
			h.native.
				FinalizeAttachmentReplayInShortProjectTransaction(
					c.Request.Context(),
					services.AttachmentReplayFinalizationInput{
						TicketID:      ticketID,
						Record:        reservation.Record,
						Authorization: *replayAuthorization,
					},
				)
		if replayErr != nil {
			h.writeNativeError(c, replayErr)
			return
		}
		h.writeReplayedAttachment(
			c,
			reservation.Record,
			replay.Attachment,
		)
		return
	}
	if err := h.native.PrepareAttachmentUploadAuthorization(
		c.Request.Context(),
		&attachmentInput,
	); err != nil {
		_ = h.native.FailIdempotency(
			c.Request.Context(),
			reservation.Record.ID,
			services.AgentNativeErrorCode(err),
		)
		h.writeNativeError(c, err)
		return
	}
	attachmentInput.Reader = bytes.NewReader(content)
	attachmentInput.IdempotencyRecordID =
		reservation.Record.ID
	result, err := h.native.StoreAttachment(
		c.Request.Context(),
		attachmentInput,
	)
	if err != nil {
		_ = h.native.FailIdempotency(c.Request.Context(), reservation.Record.ID, services.AgentNativeErrorCode(err))
		h.writeNativeError(c, err)
		return
	}
	h.publishTicket(c, ticketID)
	c.Header("ETag", httpcontract.FormatETag(result.Receipt.ResourceVersion))
	WriteReceipt(
		c,
		http.StatusAccepted,
		result.Attachment.ToResponse(),
		receiptFromService(result.Receipt),
	)
}

func (h *APIHandler) ListComments(c *gin.Context) {
	if _, ok := h.loadAuthorizedTicket(c); !ok {
		return
	}
	ticketID, _ := parsePathUint(c, "id")
	projectScope, err := services.RequireProjectScope(c.Request.Context())
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	query, ok := h.requireTicketContentListQuery(
		c,
		agentTicketCommentsList,
		projectScope,
		ticketID,
	)
	if !ok {
		return
	}
	dbQuery := h.db.WithContext(c.Request.Context()).
		Where(
			"organization_id = ? AND project_id = ? AND ticket_id = ? AND is_deleted = ?",
			projectScope.OrganizationID,
			projectScope.ProjectID,
			ticketID,
			false,
		)
	if query.After != nil {
		dbQuery = dbQuery.Where(
			"created_at < ? OR (created_at = ? AND id < ?)",
			query.After.CreatedAt,
			query.After.CreatedAt,
			query.After.ID,
		)
	}
	var comments []models.TicketComment
	if err := dbQuery.
		Preload("User").
		Preload("ServicePrincipal").
		Order("created_at DESC").
		Order("id DESC").
		Limit(query.Limit + 1).
		Find(&comments).Error; err != nil {
		h.writeNativeError(c, err)
		return
	}
	hasMore := len(comments) > query.Limit
	if hasMore {
		comments = comments[:query.Limit]
	}
	result := make([]*models.TicketCommentResponse, 0, len(comments))
	for i := range comments {
		result = append(result, comments[i].ToResponse())
	}
	nextCursor, ok := nextTicketContentListCursor(
		h,
		c,
		agentTicketCommentsList,
		projectScope,
		ticketID,
		query.Limit,
		hasMore,
		comments,
	)
	if !ok {
		return
	}
	WriteData(c, http.StatusOK, agentTicketCommentPage{
		Items:      result,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, Meta{})
}

func (h *APIHandler) ListAttachments(c *gin.Context) {
	if _, ok := h.loadAuthorizedTicketFor(
		c,
		models.ScopeAttachmentsRead,
		"ticket.attachment.list",
	); !ok {
		return
	}
	ticketID, _ := parsePathUint(c, "id")
	projectScope, err := services.RequireProjectScope(c.Request.Context())
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	query, ok := h.requireTicketContentListQuery(
		c,
		agentTicketAttachmentsList,
		projectScope,
		ticketID,
	)
	if !ok {
		return
	}
	dbQuery := h.db.WithContext(c.Request.Context()).
		Where(
			"organization_id = ? AND project_id = ? AND ticket_id = ?",
			projectScope.OrganizationID,
			projectScope.ProjectID,
			ticketID,
		)
	if query.After != nil {
		dbQuery = dbQuery.Where(
			"created_at < ? OR (created_at = ? AND id < ?)",
			query.After.CreatedAt,
			query.After.CreatedAt,
			query.After.ID,
		)
	}
	var attachments []models.TicketAttachment
	if err := dbQuery.
		Order("created_at DESC").
		Order("id DESC").
		Limit(query.Limit + 1).
		Find(&attachments).Error; err != nil {
		h.writeNativeError(c, err)
		return
	}
	hasMore := len(attachments) > query.Limit
	if hasMore {
		attachments = attachments[:query.Limit]
	}
	result := make([]*models.TicketAttachmentResponse, 0, len(attachments))
	for i := range attachments {
		result = append(result, attachments[i].ToResponse())
	}
	nextCursor, ok := nextTicketContentListCursor(
		h,
		c,
		agentTicketAttachmentsList,
		projectScope,
		ticketID,
		query.Limit,
		hasMore,
		attachments,
	)
	if !ok {
		return
	}
	WriteData(c, http.StatusOK, agentTicketAttachmentPage{
		Items:      result,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, Meta{})
}

func (h *APIHandler) DownloadAttachment(c *gin.Context) {
	attachmentID, ok := parsePathUint(c, "id")
	if !ok {
		return
	}
	attachment, reader, err := h.native.OpenAttachment(c.Request.Context(), attachmentID)
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	defer reader.Close()
	setAgentAttachmentDownloadHeaders(c, attachment.OriginalName)
	c.DataFromReader(http.StatusOK, attachment.FileSize, attachment.MimeType, reader, nil)
}

func setAgentAttachmentDownloadHeaders(
	c *gin.Context,
	originalName string,
) {
	disposition := mime.FormatMediaType(
		"attachment",
		map[string]string{"filename": originalName},
	)
	if disposition == "" {
		disposition = "attachment"
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("Pragma", "no-cache")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header(
		"Content-Security-Policy",
		"default-src 'none'; sandbox",
	)
	c.Header("Content-Disposition", disposition)
}

func (h *APIHandler) GetHistory(c *gin.Context) {
	if _, ok := h.loadAuthorizedTicket(c); !ok {
		return
	}
	ticketID, _ := parsePathUint(c, "id")
	projectScope, err := services.RequireProjectScope(c.Request.Context())
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	limit, err := ParseLimit(c, 50, 100)
	if err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}
	var history []models.TicketHistory
	if err := h.db.WithContext(c.Request.Context()).
		Preload("User").
		Preload("ServicePrincipal").
		Where(
			"organization_id = ? AND project_id = ? AND ticket_id = ?",
			projectScope.OrganizationID,
			projectScope.ProjectID,
			ticketID,
		).
		Order("created_at DESC, id DESC").
		Limit(limit).
		Find(&history).Error; err != nil {
		h.writeNativeError(c, err)
		return
	}
	result := make([]*models.TicketHistoryResponse, 0, len(history))
	for i := range history {
		result = append(result, history[i].ToResponse())
	}
	WriteData(c, http.StatusOK, result, Meta{})
}

func (h *APIHandler) ListEvents(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	limit, err := ParseLimit(c, 50, 100)
	if err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}
	cursor, err := DecodeCursor(c.Query("cursor"))
	if err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}
	policyBatch, err := h.native.PrepareReadPolicyBatch(
		c.Request.Context(),
		services.PolicyCheckInput{
			ServicePrincipalID: principal.ID,
			CredentialID:       principal.CredentialID,
			Scope:              models.ScopeEventsSubscribe,
			Action:             "event.list",
			ResourceType:       "event",
			ResourceID:         "*",
			SourceProtocol:     "rest",
		},
	)
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	projectScope, err := services.RequireProjectScope(c.Request.Context())
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	query := h.db.WithContext(c.Request.Context()).
		Model(&models.DomainEvent{}).
		Where(
			"organization_id = ? AND project_id = ?",
			projectScope.OrganizationID,
			projectScope.ProjectID,
		)
	if eventType := strings.TrimSpace(c.Query("type")); eventType != "" {
		query = query.Where("type = ?", eventType)
	}
	if !cursor.CreatedAt.IsZero() {
		query = query.Where(
			"(created_at < ?) OR (created_at = ? AND id < ?)",
			cursor.CreatedAt, cursor.CreatedAt, cursor.ID,
		)
	}

	candidateBudget := boundedListCandidateBudget(limit)
	var candidates []models.DomainEvent
	if err := query.
		Order("created_at DESC, id DESC").
		Limit(candidateBudget + 1).
		Find(&candidates).Error; err != nil {
		h.writeNativeError(c, err)
		return
	}

	events := make([]models.DomainEvent, 0, limit)
	scanned := 0
	for scanned < len(candidates) && scanned < candidateBudget && len(events) < limit {
		event := &candidates[scanned]
		scanned++
		allowed, checkErr := policyBatch.Allows(services.PolicyCheckInput{
			ServicePrincipalID: principal.ID,
			CredentialID:       principal.CredentialID,
			Scope:              models.ScopeEventsSubscribe,
			Action:             "event.read",
			ResourceType:       "event",
			ResourceID:         event.ID,
			SourceProtocol:     "rest",
		})
		if checkErr != nil {
			h.writeNativeError(c, checkErr)
			return
		}
		if !allowed {
			continue
		}
		if ticketID, isTicketEvent := ticketResourceIDFromEvent(event); isTicketEvent {
			if !agentauth.HasScopes(c, models.ScopeTicketsRead) {
				continue
			}
			allowed, checkErr = policyBatch.Allows(services.PolicyCheckInput{
				ServicePrincipalID: principal.ID,
				CredentialID:       principal.CredentialID,
				Scope:              models.ScopeTicketsRead,
				Action:             "ticket.read",
				ResourceType:       "ticket",
				ResourceID:         ticketID,
				SourceProtocol:     "rest",
			})
			if checkErr != nil {
				h.writeNativeError(c, checkErr)
				return
			}
			if !allowed {
				continue
			}
		}
		events = append(events, *event)
	}

	hasMore := scanned < len(candidates)
	envelopes := make([]services.CloudEventEnvelope, 0, len(events))
	for i := range events {
		envelopes = append(envelopes, services.CloudEventFromModel(&events[i]))
	}
	meta := Meta{HasMore: hasMore}
	if hasMore && scanned > 0 {
		last := candidates[scanned-1]
		meta.NextCursor = EncodeCursor(Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	if _, err := policyBatch.RecordSummary(c.Request.Context(), map[string]any{
		"candidate_budget":   candidateBudget,
		"candidates_scanned": scanned,
		"items_returned":     len(envelopes),
		"items_filtered":     scanned - len(envelopes),
		"has_more":           hasMore,
		"cursor_semantics":   "last_examined_candidate",
	}); err != nil {
		h.writeNativeError(c, err)
		return
	}
	WriteData(c, http.StatusOK, envelopes, meta)
}

func ticketResourceIDFromEvent(event *models.DomainEvent) (string, bool) {
	if event != nil && strings.HasPrefix(event.Subject, "ticket/") {
		ticketID := strings.TrimSpace(strings.TrimPrefix(event.Subject, "ticket/"))
		if _, err := strconv.ParseUint(ticketID, 10, 64); err == nil {
			return ticketID, true
		}
	}
	return "", false
}

type authenticatedPrincipal struct {
	ID           string
	CredentialID string
}

const maxAgentKnowledgeRequestBytes int64 = 128 << 10

type agentKnowledgeArticleDraftRequest struct {
	Key                 string `json:"key"`
	Title               string `json:"title"`
	Summary             string `json:"summary"`
	Markdown            string `json:"markdown"`
	SourceTicketID      uint   `json:"source_ticket_id"`
	SourceAttachmentIDs []uint `json:"source_attachment_ids"`
}

type agentKnowledgeVersionDraftRequest struct {
	Title               string `json:"title"`
	Markdown            string `json:"markdown"`
	SourceTicketID      uint   `json:"source_ticket_id"`
	SourceAttachmentIDs []uint `json:"source_attachment_ids"`
}

type agentKnowledgeSearchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type agentKnowledgeArticleResponse struct {
	ID               string                        `json:"id"`
	CreatedAt        time.Time                     `json:"created_at"`
	UpdatedAt        time.Time                     `json:"updated_at"`
	Key              string                        `json:"key"`
	Title            string                        `json:"title"`
	Summary          string                        `json:"summary"`
	Status           models.KnowledgeArticleStatus `json:"status"`
	CurrentVersionID *string                       `json:"current_version_id,omitempty"`
	Revision         uint64                        `json:"revision"`
}

func newAgentKnowledgeArticleResponse(
	article models.KnowledgeArticle,
) agentKnowledgeArticleResponse {
	return agentKnowledgeArticleResponse{
		ID:               article.ID,
		CreatedAt:        article.CreatedAt,
		UpdatedAt:        article.UpdatedAt,
		Key:              article.Key,
		Title:            article.Title,
		Summary:          article.Summary,
		Status:           article.Status,
		CurrentVersionID: article.CurrentVersion,
		Revision:         article.Revision,
	}
}

type agentKnowledgeVersionResponse struct {
	ID          string                        `json:"id"`
	CreatedAt   time.Time                     `json:"created_at"`
	UpdatedAt   time.Time                     `json:"updated_at"`
	ArticleID   string                        `json:"article_id"`
	Version     uint64                        `json:"version"`
	Status      models.KnowledgeVersionStatus `json:"status"`
	Title       string                        `json:"title"`
	MimeType    string                        `json:"mime_type"`
	SizeBytes   int64                         `json:"size_bytes"`
	ContentHash string                        `json:"content_hash"`
	VirusScan   models.VirusScanStatus        `json:"virus_scan"`
	PageCount   int                           `json:"page_count"`
	PublishedAt *time.Time                    `json:"published_at,omitempty"`
}

func newAgentKnowledgeVersionResponse(
	version models.KnowledgeArticleVersion,
) agentKnowledgeVersionResponse {
	return agentKnowledgeVersionResponse{
		ID:          version.ID,
		CreatedAt:   version.CreatedAt,
		UpdatedAt:   version.UpdatedAt,
		ArticleID:   version.ArticleID,
		Version:     version.Version,
		Status:      version.Status,
		Title:       version.Title,
		MimeType:    version.MimeType,
		SizeBytes:   version.SizeBytes,
		ContentHash: version.ContentHash,
		VirusScan:   version.VirusScan,
		PageCount:   version.PageCount,
		PublishedAt: version.PublishedAt,
	}
}

type agentKnowledgeSourceResponse struct {
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

func newAgentKnowledgeSourceResponse(
	source services.KnowledgeSourceView,
) agentKnowledgeSourceResponse {
	return agentKnowledgeSourceResponse{
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

type agentKnowledgeDocumentSectionResponse struct {
	Ordinal      uint   `json:"ordinal"`
	Heading      string `json:"heading,omitempty"`
	HeadingLevel int    `json:"heading_level"`
	SectionPath  string `json:"section_path,omitempty"`
	Markdown     string `json:"markdown"`
	ContentHash  string `json:"content_hash"`
}

type agentKnowledgeDocumentResponse struct {
	Article  agentKnowledgeArticleResponse           `json:"article"`
	Version  agentKnowledgeVersionResponse           `json:"version"`
	Markdown string                                  `json:"markdown"`
	Sections []agentKnowledgeDocumentSectionResponse `json:"sections"`
	Sources  []agentKnowledgeSourceResponse          `json:"sources"`
}

func newAgentKnowledgeDocumentResponse(
	document *services.KnowledgeArticleDocument,
) agentKnowledgeDocumentResponse {
	response := agentKnowledgeDocumentResponse{
		Sections: make(
			[]agentKnowledgeDocumentSectionResponse,
			0,
		),
		Sources: make([]agentKnowledgeSourceResponse, 0),
	}
	if document == nil {
		return response
	}
	response.Article = newAgentKnowledgeArticleResponse(
		document.Article,
	)
	response.Version = newAgentKnowledgeVersionResponse(
		document.Version,
	)
	response.Markdown = document.Markdown
	response.Sections = make(
		[]agentKnowledgeDocumentSectionResponse,
		0,
		len(document.Sections),
	)
	for _, section := range document.Sections {
		response.Sections = append(
			response.Sections,
			agentKnowledgeDocumentSectionResponse{
				Ordinal:      section.Ordinal,
				Heading:      section.Heading,
				HeadingLevel: section.HeadingLevel,
				SectionPath:  section.SectionPath,
				Markdown:     section.Markdown,
				ContentHash:  section.ContentHash,
			},
		)
	}
	response.Sources = make(
		[]agentKnowledgeSourceResponse,
		0,
		len(document.Sources),
	)
	for _, source := range document.Sources {
		response.Sources = append(
			response.Sources,
			newAgentKnowledgeSourceResponse(source),
		)
	}
	return response
}

type agentKnowledgeArticlePage struct {
	Items      []agentKnowledgeArticleResponse `json:"items"`
	Total      int64                           `json:"total"`
	Page       int                             `json:"page"`
	PageSize   int                             `json:"page_size"`
	TotalPages int                             `json:"total_pages"`
}

func (h *APIHandler) checkAgentKnowledgeAction(
	c *gin.Context,
	principal authenticatedPrincipal,
	scope string,
	action string,
	resourceType string,
	resourceID string,
	isWrite bool,
	requestDigest string,
) (*models.PolicyDecision, bool) {
	if h == nil || h.native == nil || h.knowledge == nil {
		WriteProblem(
			c,
			http.StatusServiceUnavailable,
			ProblemServiceUnavailable,
			"Knowledge service is unavailable",
			true,
		)
		return nil, false
	}
	decision, err := h.native.CheckActionInShortProjectTransactions(
		c.Request.Context(),
		services.PolicyCheckInput{
			ServicePrincipalID: principal.ID,
			CredentialID:       principal.CredentialID,
			Scope:              scope,
			Action:             action,
			ResourceType:       resourceType,
			ResourceID:         resourceID,
			IsWrite:            isWrite,
			RequestDigest:      requestDigest,
			SourceProtocol: string(
				services.SourceProtocolAgentREST,
			),
		},
	)
	if err != nil {
		h.writeNativeError(c, err)
		return decision, false
	}
	if decision == nil || strings.TrimSpace(decision.ID) == "" {
		WriteProblem(
			c,
			http.StatusInternalServerError,
			ProblemInternal,
			"Knowledge policy decision is unavailable",
			true,
		)
		return nil, false
	}
	return decision, true
}

type agentKnowledgeSourceAuthorization struct {
	TicketPolicyDecisionID     string
	AttachmentPolicyDecisionID string
}

func (h *APIHandler) expandAgentKnowledgeDocumentSources(
	c *gin.Context,
	principal authenticatedPrincipal,
	articleID string,
	input services.GetArticleDocumentInput,
	document *services.KnowledgeArticleDocument,
) (*services.KnowledgeArticleDocument, bool) {
	if document == nil || h == nil || h.native == nil ||
		h.knowledge == nil {
		return document, true
	}
	targets := document.SourceAuthorizationTargets()
	if len(targets) == 0 ||
		!agentauth.HasScopes(c, models.ScopeTicketsRead) {
		return document, true
	}
	authorizations := make(
		[]services.KnowledgeSourceAuthorization,
		0,
		len(targets),
	)
	for _, target := range targets {
		resourceID := strconv.FormatUint(
			uint64(target.SourceTicketID),
			10,
		)
		ticketDecision, err :=
			h.native.CheckActionInShortProjectTransactions(
				c.Request.Context(),
				services.PolicyCheckInput{
					ServicePrincipalID: principal.ID,
					CredentialID:       principal.CredentialID,
					Scope:              models.ScopeTicketsRead,
					Action:             "ticket.read",
					ResourceType:       "ticket",
					ResourceID:         resourceID,
					SourceProtocol: string(
						services.SourceProtocolAgentREST,
					),
				},
			)
		if err != nil || ticketDecision == nil ||
			strings.TrimSpace(ticketDecision.ID) == "" {
			continue
		}
		authorization := services.KnowledgeSourceAuthorization{
			SourceTicketID:         target.SourceTicketID,
			TicketPolicyDecisionID: ticketDecision.ID,
		}
		if target.HasAttachment &&
			agentauth.HasScopes(
				c,
				models.ScopeAttachmentsRead,
			) {
			attachmentDecision, decisionErr :=
				h.native.CheckActionInShortProjectTransactions(
					c.Request.Context(),
					services.PolicyCheckInput{
						ServicePrincipalID: principal.ID,
						CredentialID:       principal.CredentialID,
						Scope:              models.ScopeAttachmentsRead,
						Action:             "ticket.attachment.read",
						ResourceType:       "ticket",
						ResourceID:         resourceID,
						SourceProtocol: string(
							services.SourceProtocolAgentREST,
						),
					},
				)
			if decisionErr == nil &&
				attachmentDecision != nil {
				authorization.AttachmentPolicyDecisionID =
					attachmentDecision.ID
			}
		}
		authorizations = append(authorizations, authorization)
	}
	if len(authorizations) == 0 {
		return document, true
	}
	input.SourceAuthorizations = authorizations
	expanded, err := h.knowledge.GetArticleDocument(
		c.Request.Context(),
		articleID,
		input,
	)
	if err != nil {
		h.writeNativeError(c, err)
		return nil, false
	}
	return expanded, true
}

func (h *APIHandler) authorizeAgentKnowledgeSources(
	c *gin.Context,
	principal authenticatedPrincipal,
	sourceTicketID uint,
	sourceAttachmentIDs []uint,
	requestDigest string,
) (agentKnowledgeSourceAuthorization, bool) {
	result := agentKnowledgeSourceAuthorization{}
	if len(sourceAttachmentIDs) > 0 && sourceTicketID == 0 {
		WriteProblem(
			c,
			http.StatusBadRequest,
			ProblemInvalidRequest,
			"source_ticket_id is required when source_attachment_ids are present",
			false,
		)
		return result, false
	}
	if sourceTicketID == 0 {
		return result, true
	}
	resourceID := strconv.FormatUint(uint64(sourceTicketID), 10)
	if !agentauth.HasScopes(c, models.ScopeTicketsRead) {
		h.writeNativeError(c, services.ErrInvalidScope)
		return result, false
	}
	ticketDecision, ok := h.checkAgentKnowledgeAction(
		c,
		principal,
		models.ScopeTicketsRead,
		"ticket.read",
		"ticket",
		resourceID,
		false,
		requestDigest,
	)
	if !ok {
		return result, false
	}
	result.TicketPolicyDecisionID = ticketDecision.ID
	if len(sourceAttachmentIDs) == 0 {
		return result, true
	}
	if !agentauth.HasScopes(c, models.ScopeAttachmentsRead) {
		h.writeNativeError(c, services.ErrInvalidScope)
		return result, false
	}
	attachmentDecision, ok := h.checkAgentKnowledgeAction(
		c,
		principal,
		models.ScopeAttachmentsRead,
		"ticket.attachment.read",
		"ticket",
		resourceID,
		false,
		requestDigest,
	)
	if !ok {
		return result, false
	}
	result.AttachmentPolicyDecisionID = attachmentDecision.ID
	return result, true
}

func (h *APIHandler) writeAuthoredKnowledgeResult(
	c *gin.Context,
	result *services.AuthoredKnowledgeResult,
) {
	if result == nil ||
		strings.TrimSpace(result.Article.ID) == "" ||
		strings.TrimSpace(result.Version.ID) == "" ||
		result.Document == nil {
		WriteProblem(
			c,
			http.StatusInternalServerError,
			ProblemInternal,
			"Knowledge draft result is unavailable",
			true,
		)
		return
	}
	WriteReceipt(
		c,
		http.StatusCreated,
		newAgentKnowledgeDocumentResponse(result.Document),
		receiptFromService(result.Receipt),
	)
}

func (h *APIHandler) writeReplayedKnowledgeDocument(
	c *gin.Context,
	record *models.IdempotencyRecord,
	requestedArticleID string,
	policyDecisionID string,
	authorization services.KnowledgeDocumentAuthorizationMode,
) {
	if !idempotencyRecordMatchesProject(c, record) {
		WriteProblem(
			c,
			http.StatusConflict,
			ProblemIdempotencyConflict,
			"Idempotency record belongs to another project",
			false,
		)
		return
	}
	resourceID := strings.TrimSpace(record.ResourceID)
	parsedResourceID, err := uuid.Parse(resourceID)
	if err != nil || parsedResourceID.String() != resourceID {
		WriteProblem(
			c,
			http.StatusInternalServerError,
			ProblemInternal,
			"Completed knowledge operation has no valid resource",
			true,
		)
		return
	}
	var storedCompletion services.AuthoredKnowledgeIdempotencyReceipt
	if err := json.Unmarshal(
		record.ResponseBody,
		&storedCompletion,
	); err != nil ||
		strings.TrimSpace(storedCompletion.OperationID) == "" ||
		storedCompletion.ResourceID != resourceID {
		WriteProblem(
			c,
			http.StatusInternalServerError,
			ProblemInternal,
			"Completed knowledge operation receipt is invalid",
			true,
		)
		return
	}
	storedReceipt := storedCompletion.OperationReceipt
	articleID := requestedArticleID
	versionID := resourceID
	hasBoundReference := storedCompletion.ArticleID != "" ||
		storedCompletion.VersionID != "" ||
		storedCompletion.ContentHash != ""
	if hasBoundReference {
		parsedArticleID, articleErr := uuid.Parse(
			storedCompletion.ArticleID,
		)
		parsedVersionID, versionErr := uuid.Parse(
			storedCompletion.VersionID,
		)
		contentHashBytes, hashErr := hex.DecodeString(
			storedCompletion.ContentHash,
		)
		expectedResourceID := storedCompletion.VersionID
		if requestedArticleID == "" {
			expectedResourceID = storedCompletion.ArticleID
		}
		if articleErr != nil ||
			parsedArticleID.String() != storedCompletion.ArticleID ||
			versionErr != nil ||
			parsedVersionID.String() != storedCompletion.VersionID ||
			hashErr != nil ||
			len(contentHashBytes) != sha256.Size ||
			strings.ToLower(storedCompletion.ContentHash) !=
				storedCompletion.ContentHash ||
			expectedResourceID != resourceID ||
			(requestedArticleID != "" &&
				storedCompletion.ArticleID != requestedArticleID) {
			WriteProblem(
				c,
				http.StatusInternalServerError,
				ProblemInternal,
				"Completed knowledge operation reference is invalid",
				true,
			)
			return
		}
		articleID = storedCompletion.ArticleID
		versionID = storedCompletion.VersionID
	} else if articleID == "" {
		// Compatibility for records completed before exact authored document
		// references were persisted.
		articleID = resourceID
		versionID = ""
	}
	document, err := h.knowledge.GetArticleDocument(
		c.Request.Context(),
		articleID,
		services.GetArticleDocumentInput{
			VersionID:        versionID,
			PolicyDecisionID: policyDecisionID,
			Authorization:    authorization,
		},
	)
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	if hasBoundReference &&
		(document.Article.ID != storedCompletion.ArticleID ||
			document.Version.ID != storedCompletion.VersionID ||
			document.Version.ContentHash != storedCompletion.ContentHash) {
		WriteProblem(
			c,
			http.StatusInternalServerError,
			ProblemInternal,
			"Completed knowledge operation document changed",
			true,
		)
		return
	}
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	document, ok = h.expandAgentKnowledgeDocumentSources(
		c,
		principal,
		articleID,
		services.GetArticleDocumentInput{
			VersionID:        versionID,
			PolicyDecisionID: policyDecisionID,
			Authorization:    authorization,
		},
		document,
	)
	if !ok {
		return
	}
	WriteReceipt(
		c,
		record.ResponseCode,
		newAgentKnowledgeDocumentResponse(document),
		receiptFromService(storedReceipt),
	)
}

func validateAgentKnowledgeArticleDraft(
	request agentKnowledgeArticleDraftRequest,
) error {
	if !validAgentKnowledgeKey(request.Key) {
		return errors.New(
			"key must match ^[a-z][a-z0-9._-]{0,63}$",
		)
	}
	if err := validateAgentKnowledgeTitle(request.Title); err != nil {
		return err
	}
	if utf8.RuneCountInString(request.Summary) > 1000 {
		return errors.New(
			"summary cannot exceed 1000 characters",
		)
	}
	if err := validateAgentKnowledgeMarkdown(request.Markdown); err != nil {
		return err
	}
	return validateAgentKnowledgeSources(
		request.SourceTicketID,
		request.SourceAttachmentIDs,
	)
}

func validateAgentKnowledgeVersionDraft(
	request agentKnowledgeVersionDraftRequest,
) error {
	if err := validateAgentKnowledgeTitle(request.Title); err != nil {
		return err
	}
	if err := validateAgentKnowledgeMarkdown(request.Markdown); err != nil {
		return err
	}
	return validateAgentKnowledgeSources(
		request.SourceTicketID,
		request.SourceAttachmentIDs,
	)
}

func validAgentKnowledgeKey(value string) bool {
	if len(value) < 1 || len(value) > 64 ||
		value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') ||
			character == '.' ||
			character == '_' ||
			character == '-' {
			continue
		}
		return false
	}
	return true
}

func validateAgentKnowledgeTitle(value string) error {
	length := utf8.RuneCountInString(strings.TrimSpace(value))
	if length < 1 || length > 240 {
		return errors.New(
			"title must contain between 1 and 240 characters",
		)
	}
	return nil
}

func validateAgentKnowledgeMarkdown(value string) error {
	if !utf8.ValidString(value) ||
		len(value) > services.MaxAuthoredMarkdownBytes ||
		strings.TrimSpace(value) == "" {
		return errors.New(
			"markdown must be non-blank UTF-8 within 128 KiB",
		)
	}
	return nil
}

func validateAgentKnowledgeSources(
	sourceTicketID uint,
	sourceAttachmentIDs []uint,
) error {
	if len(sourceAttachmentIDs) > services.MaxAuthoredSourceLinks {
		return fmt.Errorf(
			"source_attachment_ids cannot contain more than %d items",
			services.MaxAuthoredSourceLinks,
		)
	}
	if len(sourceAttachmentIDs) > 0 && sourceTicketID == 0 {
		return errors.New(
			"source_ticket_id is required when source_attachment_ids are present",
		)
	}
	seen := make(map[uint]struct{}, len(sourceAttachmentIDs))
	for _, attachmentID := range sourceAttachmentIDs {
		if attachmentID == 0 {
			return errors.New(
				"source_attachment_ids must contain positive IDs",
			)
		}
		if _, duplicate := seen[attachmentID]; duplicate {
			return errors.New(
				"source_attachment_ids cannot contain duplicate IDs",
			)
		}
		seen[attachmentID] = struct{}{}
	}
	return nil
}

func (h *APIHandler) executionLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		principalID := c.GetString(agentauth.ContextPrincipalID)
		leaseContext, release, err :=
			h.native.AcquireAgentExecutionContext(
				c.Request.Context(),
				principalID,
			)
		if err != nil {
			h.writeNativeError(c, err)
			c.Abort()
			return
		}
		defer release()
		c.Request = c.Request.WithContext(leaseContext)
		c.Next()
	}
}

func (h *APIHandler) principal(c *gin.Context) (authenticatedPrincipal, bool) {
	id := c.GetString(agentauth.ContextPrincipalID)
	credentialID := c.GetString(agentauth.ContextCredentialID)
	if id == "" || credentialID == "" {
		WriteProblem(c, http.StatusUnauthorized, ProblemUnauthorized, "Agent principal context is missing", false)
		return authenticatedPrincipal{}, false
	}
	return authenticatedPrincipal{ID: id, CredentialID: credentialID}, true
}

func (h *APIHandler) authorizeAgentRESTNativeCommand(
	c *gin.Context,
	principal authenticatedPrincipal,
	command services.NativeCommandAuthorizationInput,
	replay bool,
) (context.Context, []string, error) {
	tokenScopesValue, _ := c.Get(agentauth.ContextScopes)
	tokenScopes, ok := tokenScopesValue.([]string)
	if !ok {
		return nil, nil, services.ErrInvalidScope
	}
	tokenScopes = append([]string(nil), tokenScopes...)
	command.Actor = models.ServicePrincipalActor(principal.ID)
	command.CredentialID = principal.CredentialID
	command.TokenScopes = tokenScopes
	command.SourceProtocol = string(services.SourceProtocolAgentREST)
	if replay {
		if err := h.native.
			AuthorizeNativeCommandReplayInShortProjectTransactions(
				c.Request.Context(),
				command,
			); err != nil {
			return nil, nil, err
		}
		return c.Request.Context(), tokenScopes, nil
	}
	authorizedContext, err :=
		h.native.AuthorizeNativeCommandInShortProjectTransactions(
			c.Request.Context(),
			command,
		)
	if err != nil {
		return nil, nil, err
	}
	return authorizedContext, tokenScopes, nil
}

func (h *APIHandler) loadAuthorizedTicket(c *gin.Context) (*models.Ticket, bool) {
	return h.loadAuthorizedTicketFor(c, models.ScopeTicketsRead, "ticket.read")
}

func (h *APIHandler) loadAuthorizedTicketFor(
	c *gin.Context,
	scope string,
	action string,
) (*models.Ticket, bool) {
	principal, ok := h.principal(c)
	if !ok {
		return nil, false
	}
	ticketID, ok := parsePathUint(c, "id")
	if !ok {
		return nil, false
	}
	if _, err := h.native.CheckAction(c.Request.Context(), services.PolicyCheckInput{
		ServicePrincipalID: principal.ID,
		CredentialID:       principal.CredentialID,
		Scope:              scope,
		Action:             action,
		ResourceType:       "ticket",
		ResourceID:         strconv.FormatUint(uint64(ticketID), 10),
		SourceProtocol:     "rest",
	}); err != nil {
		h.writeNativeError(c, err)
		return nil, false
	}
	var ticket models.Ticket
	projectScope, err := services.RequireProjectScope(c.Request.Context())
	if err != nil {
		h.writeNativeError(c, err)
		return nil, false
	}
	if err := h.db.WithContext(c.Request.Context()).
		Preload("CreatedBy").
		Preload("AssignedTo").
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			ticketID,
			projectScope.OrganizationID,
			projectScope.ProjectID,
		).
		First(&ticket).Error; err != nil {
		h.writeNativeError(c, err)
		return nil, false
	}
	return &ticket, true
}

func decodeOrdinaryTicketPatch(body []byte) (map[string]any, error) {
	var fields map[string]json.RawMessage
	if err := decodeStrictJSON(body, &fields); err != nil {
		return nil, fmt.Errorf("invalid ticket update: %w", err)
	}
	if len(fields) == 0 {
		return nil, errors.New("a non-empty ordinary ticket update is required")
	}

	changes := make(map[string]any, len(fields))
	for field, rawValue := range fields {
		var (
			value any
			err   error
		)
		switch field {
		case "title":
			value, err = decodeBoundedString(rawValue, field, 1, 255)
		case "description":
			value, err = decodeBoundedString(rawValue, field, 1, 10000)
		case "type":
			var ticketType models.TicketType
			ticketType, err = decodeNonNullJSON[models.TicketType](rawValue, field)
			if err == nil && !ticketType.IsValid() {
				err = fmt.Errorf("%s is not a supported ticket type", field)
			}
			value = ticketType
		case "priority":
			var priority models.TicketPriority
			priority, err = decodeNonNullJSON[models.TicketPriority](rawValue, field)
			if err == nil && !priority.IsValid() {
				err = fmt.Errorf("%s is not a supported ticket priority", field)
			}
			value = priority
		case "category_id", "subcategory_id":
			value, err = decodeNullablePositiveUint(rawValue, field)
		case "tags":
			var tags []string
			tags, err = decodeNonNullJSON[[]string](rawValue, field)
			if err == nil {
				for _, tag := range tags {
					if utf8.RuneCountInString(tag) == 0 ||
						utf8.RuneCountInString(tag) > 64 {
						err = fmt.Errorf("%s entries must contain 1 to 64 characters", field)
						break
					}
				}
			}
			value = tags
		case "due_date":
			var dueDate *time.Time
			dueDate, err = decodeNullableJSON[time.Time](rawValue, field)
			if dueDate == nil {
				value = nil
			} else {
				value = *dueDate
			}
		case "customer_email":
			value, err = decodeBoundedString(rawValue, field, 0, 100)
		case "customer_phone":
			value, err = decodeBoundedString(rawValue, field, 0, 20)
		case "customer_name":
			value, err = decodeBoundedString(rawValue, field, 0, 100)
		case "internal_notes":
			value, err = decodeBoundedString(rawValue, field, 0, 10000)
		case "custom_fields":
			value, err = decodeNonNullJSON[map[string]any](rawValue, field)
		case "agent_context":
			value, err = decodeNonNullJSON[models.AgentContext](rawValue, field)
		default:
			return nil, fmt.Errorf(
				"%s is not allowed in an ordinary ticket update; use an explicit command",
				field,
			)
		}
		if err != nil {
			return nil, err
		}
		changes[field] = value
	}
	return changes, nil
}

func decodeNonNullJSON[T any](raw json.RawMessage, field string) (T, error) {
	var value T
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return value, fmt.Errorf("%s cannot be null", field)
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return value, fmt.Errorf("invalid %s: %w", field, err)
	}
	return value, nil
}

func decodeNullableJSON[T any](raw json.RawMessage, field string) (*T, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	value, err := decodeNonNullJSON[T](raw, field)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func decodeNullablePositiveUint(raw json.RawMessage, field string) (any, error) {
	value, err := decodeNullableJSON[uint](raw, field)
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, nil
	}
	if *value == 0 {
		return nil, fmt.Errorf("%s must be a positive integer or null", field)
	}
	return *value, nil
}

func decodeBoundedString(
	raw json.RawMessage,
	field string,
	minimum int,
	maximum int,
) (string, error) {
	value, err := decodeNonNullJSON[string](raw, field)
	if err != nil {
		return "", err
	}
	length := utf8.RuneCountInString(value)
	if length < minimum || length > maximum {
		return "", fmt.Errorf(
			"%s must contain between %d and %d characters",
			field,
			minimum,
			maximum,
		)
	}
	return value, nil
}

type ticketTransitionCommandRequest struct {
	Status models.TicketStatus `json:"status"`
	Reason string              `json:"reason"`
}

type ticketAssignmentCommandRequest struct {
	Assignee *models.ActorRef
	Reason   string
}

type ticketEscalationCommandRequest struct {
	Reason   string
	Priority *models.TicketPriority
	Assignee *models.ActorRef
}

func decodeTicketAssignmentCommand(
	body []byte,
) (ticketAssignmentCommandRequest, error) {
	var request struct {
		Assignee json.RawMessage `json:"assignee"`
		Reason   string          `json:"reason"`
	}
	if err := decodeStrictJSON(body, &request); err != nil {
		return ticketAssignmentCommandRequest{}, fmt.Errorf(
			"invalid assignment command: %w",
			err,
		)
	}
	if len(request.Assignee) == 0 {
		return ticketAssignmentCommandRequest{}, errors.New(
			"assignee is required and may be an ActorRef or null",
		)
	}
	reason, err := validateCommandReason(request.Reason)
	if err != nil {
		return ticketAssignmentCommandRequest{}, err
	}
	if bytes.Equal(bytes.TrimSpace(request.Assignee), []byte("null")) {
		return ticketAssignmentCommandRequest{Reason: reason}, nil
	}
	assignee, err := decodeAssignableActorRef(request.Assignee)
	if err != nil {
		return ticketAssignmentCommandRequest{}, err
	}
	return ticketAssignmentCommandRequest{
		Assignee: &assignee,
		Reason:   reason,
	}, nil
}

func decodeTicketTransitionCommand(
	body []byte,
) (ticketTransitionCommandRequest, error) {
	var request ticketTransitionCommandRequest
	if err := decodeStrictJSON(body, &request); err != nil {
		return request, fmt.Errorf("invalid transition command: %w", err)
	}
	if !request.Status.IsValid() {
		return request, fmt.Errorf("status is not a supported ticket status")
	}
	reason, err := validateCommandReason(request.Reason)
	if err != nil {
		return request, err
	}
	request.Reason = reason
	return request, nil
}

func decodeTicketEscalationCommand(
	body []byte,
) (ticketEscalationCommandRequest, error) {
	var raw struct {
		Reason   string          `json:"reason"`
		Priority json.RawMessage `json:"priority"`
		Assignee json.RawMessage `json:"assignee"`
	}
	if err := decodeStrictJSON(body, &raw); err != nil {
		return ticketEscalationCommandRequest{}, fmt.Errorf(
			"invalid escalation command: %w",
			err,
		)
	}
	reason, err := validateCommandReason(raw.Reason)
	if err != nil {
		return ticketEscalationCommandRequest{}, err
	}
	request := ticketEscalationCommandRequest{Reason: reason}
	if len(raw.Priority) > 0 {
		priority, decodeErr := decodeNonNullJSON[models.TicketPriority](
			raw.Priority,
			"priority",
		)
		if decodeErr != nil {
			return ticketEscalationCommandRequest{}, decodeErr
		}
		if !priority.IsValid() {
			return ticketEscalationCommandRequest{}, errors.New(
				"priority is not a supported ticket priority",
			)
		}
		request.Priority = &priority
	}
	if len(raw.Assignee) > 0 {
		assignee, decodeErr := decodeAssignableActorRef(raw.Assignee)
		if decodeErr != nil {
			return ticketEscalationCommandRequest{}, decodeErr
		}
		request.Assignee = &assignee
	}
	return request, nil
}

func decodeAssignableActorRef(raw json.RawMessage) (models.ActorRef, error) {
	var assignee models.ActorRef
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return assignee, errors.New("assignee cannot be null in this command")
	}
	if err := decodeStrictJSON(raw, &assignee); err != nil {
		return assignee, fmt.Errorf("invalid assignee: %w", err)
	}
	if err := assignee.Validate(); err != nil {
		return assignee, fmt.Errorf("invalid assignee: %w", err)
	}
	if assignee.Type != models.ActorTypeHuman &&
		assignee.Type != models.ActorTypeServicePrincipal {
		return assignee, errors.New(
			"assignee type must be human or service_principal",
		)
	}
	if assignee.ID != strings.TrimSpace(assignee.ID) {
		return assignee, errors.New("assignee id cannot contain surrounding whitespace")
	}
	return assignee, nil
}

func validateCommandReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	length := utf8.RuneCountInString(reason)
	if length < 1 || length > 1000 {
		return "", errors.New("reason must contain between 1 and 1000 characters")
	}
	return reason, nil
}

func (h *APIHandler) writeReplayedTicket(c *gin.Context, record *models.IdempotencyRecord, status int) {
	if !idempotencyRecordMatchesProject(c, record) {
		WriteProblem(c, http.StatusConflict, ProblemIdempotencyConflict, "Idempotency record belongs to another project", false)
		return
	}
	var receipt Receipt
	_ = json.Unmarshal(record.ResponseBody, &receipt)
	if len(record.ResourceSnapshot) > 0 {
		var snapshot models.TicketResponse
		if json.Unmarshal(record.ResourceSnapshot, &snapshot) == nil && snapshot.ID > 0 {
			c.Header("ETag", httpcontract.FormatETag(receipt.ResourceVersion))
			WriteReceipt(c, status, &snapshot, receipt)
			return
		}
	}
	resourceID, err := strconv.ParseUint(record.ResourceID, 10, 32)
	if err != nil {
		h.writeIdempotentBody(c, record)
		return
	}
	var ticket models.Ticket
	if err := h.db.WithContext(c.Request.Context()).Where(
		"id = ? AND organization_id = ? AND project_id = ?",
		uint(resourceID),
		record.OrganizationID,
		record.ProjectID,
	).First(&ticket).Error; err != nil {
		h.writeIdempotentBody(c, record)
		return
	}
	c.Header("ETag", httpcontract.FormatETag(ticket.Version))
	WriteReceipt(c, status, ticket.ToResponse(), receipt)
}

func (h *APIHandler) writeReplayedLease(c *gin.Context, record *models.IdempotencyRecord) {
	if !idempotencyRecordMatchesProject(c, record) {
		WriteProblem(c, http.StatusConflict, ProblemIdempotencyConflict, "Idempotency record belongs to another project", false)
		return
	}
	var receipt Receipt
	if json.Unmarshal(record.ResponseBody, &receipt) != nil ||
		receipt.OperationID == "" ||
		len(record.ResourceSnapshot) == 0 {
		h.writeIdempotentBody(c, record)
		return
	}
	var snapshot map[string]any
	if json.Unmarshal(record.ResourceSnapshot, &snapshot) != nil || snapshot["lease_id"] == nil {
		h.writeIdempotentBody(c, record)
		return
	}
	WriteReceipt(c, record.ResponseCode, snapshot, receipt)
}

func (h *APIHandler) writeReplayedComment(c *gin.Context, record *models.IdempotencyRecord) {
	if !idempotencyRecordMatchesProject(c, record) {
		WriteProblem(c, http.StatusConflict, ProblemIdempotencyConflict, "Idempotency record belongs to another project", false)
		return
	}
	var receipt Receipt
	_ = json.Unmarshal(record.ResponseBody, &receipt)
	if len(record.ResourceSnapshot) > 0 {
		var snapshot models.TicketComment
		if json.Unmarshal(record.ResourceSnapshot, &snapshot) == nil && snapshot.ID > 0 {
			c.Header("ETag", httpcontract.FormatETag(receipt.ResourceVersion))
			WriteReceipt(c, record.ResponseCode, snapshot.ToResponse(), receipt)
			return
		}
	}
	resourceID, err := strconv.ParseUint(record.ResourceID, 10, 32)
	if err != nil {
		h.writeIdempotentBody(c, record)
		return
	}
	var comment models.TicketComment
	if err := h.db.WithContext(c.Request.Context()).
		Preload("User").
		Preload("ServicePrincipal").
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			uint(resourceID),
			record.OrganizationID,
			record.ProjectID,
		).
		First(&comment).Error; err != nil {
		h.writeIdempotentBody(c, record)
		return
	}
	c.Header("ETag", httpcontract.FormatETag(receipt.ResourceVersion))
	WriteReceipt(c, record.ResponseCode, comment.ToResponse(), receipt)
}

func leaseResponseData(lease *models.TicketLease) gin.H {
	return gin.H{
		"lease_id":       lease.ID,
		"ticket_id":      lease.TicketID,
		"expires_at":     lease.ExpiresAt,
		"ticket_version": lease.TicketVersion,
		"released_at":    lease.ReleasedAt,
	}
}

func (h *APIHandler) writeReplayedAttachment(
	c *gin.Context,
	record *models.IdempotencyRecord,
	fallback *models.TicketAttachment,
) {
	if !idempotencyRecordMatchesProject(c, record) {
		WriteProblem(c, http.StatusConflict, ProblemIdempotencyConflict, "Idempotency record belongs to another project", false)
		return
	}
	var receipt Receipt
	_ = json.Unmarshal(record.ResponseBody, &receipt)
	if len(record.ResourceSnapshot) > 0 {
		var snapshot models.TicketAttachment
		if json.Unmarshal(record.ResourceSnapshot, &snapshot) == nil && snapshot.ID > 0 {
			c.Header("ETag", httpcontract.FormatETag(receipt.ResourceVersion))
			WriteReceipt(c, record.ResponseCode, snapshot.ToResponse(), receipt)
			return
		}
	}
	if fallback == nil ||
		fallback.OrganizationID != record.OrganizationID ||
		fallback.ProjectID != record.ProjectID ||
		strconv.FormatUint(uint64(fallback.ID), 10) !=
			strings.TrimSpace(record.ResourceID) {
		h.writeIdempotentBody(c, record)
		return
	}
	c.Header("ETag", httpcontract.FormatETag(receipt.ResourceVersion))
	WriteReceipt(c, record.ResponseCode, fallback.ToResponse(), receipt)
}

func idempotencyRecordMatchesProject(
	c *gin.Context,
	record *models.IdempotencyRecord,
) bool {
	if c == nil || record == nil {
		return false
	}
	scope, err := services.RequireProjectScope(c.Request.Context())
	return err == nil &&
		record.OrganizationID == scope.OrganizationID &&
		record.ProjectID == scope.ProjectID
}

func (h *APIHandler) writeIdempotentBody(c *gin.Context, record *models.IdempotencyRecord) {
	var response any
	if len(record.ResponseBody) > 0 {
		_ = json.Unmarshal(record.ResponseBody, &response)
	}
	WriteData(c, record.ResponseCode, response, Meta{})
}

func (h *APIHandler) publishTicket(c *gin.Context, ticketID uint) {
	if h.publisher == nil || c == nil || ticketID == 0 {
		return
	}
	if buffer, ok := c.Request.Context().Value(
		apiPublicationBufferContextKey{},
	).(*apiPublicationBuffer); ok && buffer != nil {
		if _, duplicate := buffer.seen[ticketID]; duplicate {
			return
		}
		buffer.seen[ticketID] = struct{}{}
		buffer.ticketIDs = append(buffer.ticketIDs, ticketID)
		return
	}
	projectKey := strings.TrimSpace(c.Param("projectKey"))
	if models.ValidateProjectKey(projectKey) == nil {
		h.publisher.PublishTicket(projectKey, ticketID)
	}
}

func (h *APIHandler) writeNativeError(c *gin.Context, err error) {
	writeNativeProblem(c, err)
}

func writeNativeProblem(c *gin.Context, err error) {
	code := services.AgentNativeErrorCode(err)
	status := http.StatusBadRequest
	retryable := false
	detail := err.Error()
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound),
		errors.Is(err, services.ErrKnowledgeNotFound):
		status, code = http.StatusNotFound, ProblemNotFound
	case errors.Is(err, services.ErrInvalidCredential),
		errors.Is(err, services.ErrCredentialExpired),
		errors.Is(err, services.ErrPrincipalNotFound):
		status = http.StatusUnauthorized
	case errors.Is(err, services.ErrPrincipalDisabled),
		errors.Is(err, services.ErrPrincipalExpired),
		errors.Is(err, services.ErrReadOnlyMode),
		errors.Is(err, services.ErrGlobalEmergencyStop):
		status = http.StatusForbidden
	case errors.Is(err, services.ErrPolicyDenied),
		errors.Is(err, services.ErrProjectAccessDenied),
		errors.Is(err, services.ErrProjectKnowledgeAccessDenied),
		errors.Is(err, services.ErrKnowledgeModelPolicyDenied):
		status, code = http.StatusForbidden, ProblemPolicyDenied
	case errors.Is(err, services.ErrInvalidScope):
		status, code = http.StatusForbidden, ProblemInsufficientScope
	case errors.Is(err, services.ErrRateLimited),
		errors.Is(err, services.ErrConcurrencyLimit):
		status, code, retryable = http.StatusTooManyRequests, ProblemRateLimited, true
	case errors.Is(err, services.ErrAutomationLoop):
		status, code, retryable = http.StatusTooManyRequests, ProblemAutomationLoop, true
	case errors.Is(err, services.ErrExecutionGuardUnavailable):
		status, code, retryable = http.StatusServiceUnavailable, ProblemServiceUnavailable, true
		detail = "Agent execution protection is temporarily unavailable"
	case errors.Is(err, services.ErrKnowledgeIndexUnavailable):
		status, code, retryable = http.StatusServiceUnavailable, ProblemServiceUnavailable, true
		detail = "Knowledge search is temporarily unavailable"
	case errors.Is(err, services.ErrVersionConflict):
		status, code = http.StatusConflict, ProblemVersionConflict
	case errors.Is(err, services.ErrLeaseConflict), errors.Is(err, services.ErrLeaseExpired), errors.Is(err, services.ErrLeaseNotOwned):
		status, code = http.StatusConflict, ProblemLeaseConflict
	case errors.Is(err, services.ErrIdempotencyConflict), errors.Is(err, services.ErrIdempotencyInProgress):
		status, code = http.StatusConflict, ProblemIdempotencyConflict
	case errors.Is(err, services.ErrOutboxReplayConflict):
		status, code = http.StatusConflict, ProblemOutboxConflict
	case errors.Is(err, services.ErrOutboxReplayExpired):
		status, code = http.StatusConflict, ProblemOutboxExpired
	case errors.Is(err, services.ErrTicketConfigurationUnavailable):
		status, code = http.StatusConflict, "ticket_configuration_unavailable"
	case errors.Is(err, services.ErrTicketRequestTypeAmbiguous):
		status, code = http.StatusBadRequest, "request_type_version_required"
	case errors.Is(err, services.ErrTicketFormValidation):
		status, code = http.StatusUnprocessableEntity, "ticket_form_validation_failed"
	case errors.Is(err, services.ErrInvalidAttachment):
		status, code = http.StatusBadRequest, ProblemAttachmentRejected
	case errors.Is(err, services.ErrAttachmentTooLarge), errors.Is(err, services.ErrAttachmentNotClean), errors.Is(err, services.ErrInvalidAttachmentName):
		status, code = http.StatusUnprocessableEntity, ProblemAttachmentRejected
	default:
		if code == "internal_error" {
			status, retryable = http.StatusInternalServerError, true
			log.Printf(
				"Agent-native request failed: request_id=%s code=internal_error",
				observability.SafeLogValue(RequestID(c)),
			)
			detail = "Internal server error"
		}
	}
	WriteProblem(c, status, code, detail, retryable)
}

func receiptFromService(receipt services.OperationReceipt) Receipt {
	return Receipt{
		OperationID:      receipt.OperationID,
		ResourceID:       receipt.ResourceID,
		ResourceVersion:  receipt.ResourceVersion,
		EventID:          receipt.EventID,
		ChangedFields:    receipt.ChangedFields,
		PolicyDecisionID: receipt.PolicyDecisionID,
	}
}

func digestBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("%x", sum)
}

func commandFingerprint(method, resource string, expectedVersion uint64, leaseID string, body []byte) []byte {
	bodyHash := sha256.Sum256(body)
	encoded, _ := json.Marshal(struct {
		Method          string `json:"method"`
		Resource        string `json:"resource"`
		ExpectedVersion uint64 `json:"expected_version,omitempty"`
		LeaseID         string `json:"lease_id,omitempty"`
		BodySHA256      string `json:"body_sha256"`
	}{
		Method:          strings.ToUpper(strings.TrimSpace(method)),
		Resource:        strings.TrimSpace(resource),
		ExpectedVersion: expectedVersion,
		LeaseID:         strings.TrimSpace(leaseID),
		BodySHA256:      fmt.Sprintf("%x", bodyHash),
	})
	return encoded
}

func readJSONBody(c *gin.Context, maximum int64) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maximum+1))
	if err != nil || int64(len(body)) > maximum {
		WriteProblem(c, http.StatusRequestEntityTooLarge, ProblemInvalidRequest, "Request body is too large", false)
		return nil, false
	}
	if len(bytes.TrimSpace(body)) == 0 {
		body = []byte("{}")
	}
	return body, true
}

func decodeStrictJSON(body []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func parsePathUint(c *gin.Context, name string) (uint, bool) {
	value, err := strconv.ParseUint(c.Param(name), 10, 32)
	if err != nil || value == 0 {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, "Invalid numeric resource ID", false)
		return 0, false
	}
	return uint(value), true
}

func parseKnowledgeArticleID(
	c *gin.Context,
	name string,
) (string, bool) {
	value := strings.TrimSpace(c.Param(name))
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.String() != value {
		WriteProblem(
			c,
			http.StatusBadRequest,
			ProblemInvalidRequest,
			"Invalid knowledge article ID",
			false,
		)
		return "", false
	}
	return value, true
}

func parseOptionalKnowledgeVersionID(
	c *gin.Context,
) (string, bool) {
	value := strings.TrimSpace(c.Query("version_id"))
	if value == "" {
		return "", true
	}
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.String() != value {
		WriteProblem(
			c,
			http.StatusBadRequest,
			ProblemInvalidRequest,
			"Invalid knowledge version ID",
			false,
		)
		return "", false
	}
	return value, true
}

func parseAgentKnowledgePage(c *gin.Context) (int, int, bool) {
	page, ok := parsePositiveQueryInteger(c, "page", 1, 0)
	if !ok {
		return 0, 0, false
	}
	pageSize, ok := parsePositiveQueryInteger(c, "page_size", 20, 100)
	if !ok {
		return 0, 0, false
	}
	return page, pageSize, true
}

func parsePositiveQueryInteger(
	c *gin.Context,
	name string,
	fallback int,
	maximum int,
) (int, bool) {
	value := strings.TrimSpace(c.Query(name))
	if value == "" {
		return fallback, true
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || (maximum > 0 && parsed > maximum) {
		WriteProblem(
			c,
			http.StatusBadRequest,
			ProblemInvalidRequest,
			name+" must be a positive integer within its allowed range",
			false,
		)
		return 0, false
	}
	return parsed, true
}

func requireTicketLeaseHeader(c *gin.Context) (string, bool) {
	leaseID := strings.TrimSpace(c.GetHeader("X-Ticket-Lease"))
	if leaseID == "" {
		WriteProblem(
			c,
			http.StatusConflict,
			ProblemLeaseConflict,
			"X-Ticket-Lease is required for Agent ticket writes",
			false,
		)
		return "", false
	}
	return leaseID, true
}

func requireCommandCorrelationID(c *gin.Context) (string, bool) {
	correlationID := strings.TrimSpace(c.GetHeader("X-Correlation-ID"))
	if correlationID == "" || utf8.RuneCountInString(correlationID) > 255 {
		WriteProblem(
			c,
			http.StatusBadRequest,
			ProblemInvalidRequest,
			"X-Correlation-ID must contain between 1 and 255 characters",
			false,
		)
		return "", false
	}
	return correlationID, true
}
