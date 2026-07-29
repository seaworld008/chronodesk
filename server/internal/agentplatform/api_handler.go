package agentplatform

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/agentauth"
	"github.com/seaworld008/chronodesk/server/internal/mcp"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/observability"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type ResourcePublisher interface {
	PublishTicket(ticketID uint)
}

type APIHandler struct {
	db                  *gorm.DB
	native              *services.AgentNativeService
	tokens              *agentauth.Manager
	compatibilityUserID uint
	maxAttachmentBytes  int64
	publisher           ResourcePublisher
}

func NewAPIHandler(
	db *gorm.DB,
	native *services.AgentNativeService,
	tokens *agentauth.Manager,
	compatibilityUserID uint,
	maxAttachmentBytes int64,
	publisher ResourcePublisher,
) *APIHandler {
	if maxAttachmentBytes <= 0 {
		maxAttachmentBytes = 10 << 20
	}
	return &APIHandler{
		db:                  db,
		native:              native,
		tokens:              tokens,
		compatibilityUserID: compatibilityUserID,
		maxAttachmentBytes:  maxAttachmentBytes,
		publisher:           publisher,
	}
}

func (h *APIHandler) RegisterRoutes(api *gin.RouterGroup) {
	api.GET("/capabilities", h.Capabilities)
	api.GET("/tickets", h.tokens.Middleware(models.ScopeTicketsRead), h.executionLimit(), h.ListTickets)
	api.POST("/tickets", h.tokens.Middleware(models.ScopeTicketsCreate), h.executionLimit(), h.CreateTicket)
	api.GET("/tickets/:id", h.tokens.Middleware(models.ScopeTicketsRead), h.executionLimit(), h.GetTicket)
	// Ticket patches select their scope from the command shape. A transition,
	// assignment and ordinary field update are intentionally separate commands
	// so a broad merge patch cannot smuggle a risky action through tickets:update.
	api.PATCH("/tickets/:id", h.tokens.Middleware(), h.executionLimit(), h.UpdateTicket)
	api.GET("/tickets/:id/history", h.tokens.Middleware(models.ScopeTicketsRead), h.executionLimit(), h.GetHistory)
	api.GET("/tickets/:id/comments", h.tokens.Middleware(models.ScopeTicketsRead), h.executionLimit(), h.ListComments)
	api.POST("/tickets/:id/comments", h.tokens.Middleware(models.ScopeCommentsWrite), h.executionLimit(), h.CreateComment)
	api.GET("/tickets/:id/attachments", h.tokens.Middleware(models.ScopeAttachmentsRead), h.executionLimit(), h.ListAttachments)
	api.POST("/tickets/:id/attachments", h.tokens.Middleware(models.ScopeAttachmentsWrite), h.executionLimit(), h.StoreAttachment)
	api.GET("/attachments/:id/content", h.tokens.Middleware(models.ScopeAttachmentsRead), h.executionLimit(), h.DownloadAttachment)
	api.POST("/tickets/:id/claim", h.tokens.Middleware(models.ScopeTasksManage), h.executionLimit(), h.ClaimTicket)
	api.POST("/leases/:id/heartbeat", h.tokens.Middleware(models.ScopeTasksManage), h.executionLimit(), h.HeartbeatLease)
	api.DELETE("/leases/:id", h.tokens.Middleware(models.ScopeTasksManage), h.executionLimit(), h.ReleaseLease)
	api.GET("/events", h.tokens.Middleware(models.ScopeEventsSubscribe), h.executionLimit(), h.ListEvents)
}

func (h *APIHandler) Capabilities(c *gin.Context) {
	WriteData(c, http.StatusOK, gin.H{
		"api_version":       "v1",
		"openapi":           "/openapi.yaml",
		"mcp_endpoint":      "/mcp",
		"mcp_version":       mcp.ProtocolVersion,
		"mcp_transport":     "streamable-http",
		"mcp_stateless":     true,
		"mcp_subscriptions": "subscriptions/listen",
		"a2a_endpoint":      "/a2a/v1",
		"a2a_version":       "1.0",
		"agent_card":        "/.well-known/agent-card.json",
		"oauth_metadata": gin.H{
			"api": "/.well-known/oauth-protected-resource/api/v1",
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

	query := h.db.WithContext(c.Request.Context()).Model(&models.Ticket{}).
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
	c.Header("ETag", FormatETag(ticket.Version))
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
		Title        string                `json:"title"`
		Description  string                `json:"description"`
		Type         models.TicketType     `json:"type"`
		Priority     models.TicketPriority `json:"priority"`
		Tags         []string              `json:"tags"`
		AgentContext *models.AgentContext  `json:"agent_context"`
		CategoryID   *uint                 `json:"category_id"`
		DueDate      *time.Time            `json:"due_date"`
	}
	if err := decodeStrictJSON(body, &request); err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}
	fingerprint := commandFingerprint(http.MethodPost, "/api/v1/tickets", 0, "", body)
	actor := models.ServicePrincipalActor(principal.ID)
	reservation, err := h.native.ReserveIdempotency(
		c.Request.Context(), actor, "ticket.create", key, fingerprint, 24*time.Hour,
	)
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	if reservation.Replayed {
		if !h.authorizeReplay(c, principal, models.ScopeTicketsCreate, "ticket.create", "", true, false) {
			return
		}
		h.writeReplayedTicket(c, reservation.Record, http.StatusCreated)
		return
	}

	result, err := h.native.CreateNativeTicket(c.Request.Context(), services.NativeTicketCreateInput{
		Request: models.TicketCreateRequest{
			Title:        request.Title,
			Description:  request.Description,
			Type:         request.Type,
			Priority:     request.Priority,
			Source:       models.TicketSourceAgent,
			Tags:         models.StringList(request.Tags),
			AgentContext: request.AgentContext,
			CategoryID:   request.CategoryID,
			DueDate:      request.DueDate,
		},
		Actor:               actor,
		CompatibilityUserID: h.compatibilityUserID,
		CredentialID:        principal.CredentialID,
		SourceProtocol:      "rest",
		RequestDigest:       digestBytes(fingerprint),
		TrustLevel:          models.TicketTrustLevelUntrusted,
		TraceID:             RequestID(c),
		CorrelationID:       c.GetHeader("X-Correlation-ID"),
		IdempotencyRecordID: reservation.Record.ID,
	})
	if err != nil {
		_ = h.native.FailIdempotency(c.Request.Context(), reservation.Record.ID, services.AgentNativeErrorCode(err))
		h.writeNativeError(c, err)
		return
	}
	h.publishTicket(result.Ticket.ID)
	c.Header("ETag", FormatETag(result.Ticket.Version))
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
	expectedVersion, err := ParseIfMatch(c.GetHeader("If-Match"))
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
	var changes map[string]any
	if err := decodeStrictJSON(body, &changes); err != nil || len(changes) == 0 {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, "A non-empty JSON merge patch is required", false)
		return
	}
	requiredScope, action, risky, err := classifyTicketPatch(changes)
	if err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}
	if !agentauth.HasScopes(c, requiredScope) {
		WriteProblem(
			c,
			http.StatusForbidden,
			ProblemInsufficientScope,
			fmt.Sprintf("The access token does not grant %s", requiredScope),
			false,
		)
		return
	}
	leaseID := strings.TrimSpace(c.GetHeader("X-Ticket-Lease"))
	fingerprint := commandFingerprint(
		http.MethodPatch,
		fmt.Sprintf("/api/v1/tickets/%d", ticketID),
		expectedVersion,
		leaseID,
		body,
	)
	actor := models.ServicePrincipalActor(principal.ID)
	reservation, err := h.native.ReserveIdempotency(
		c.Request.Context(), actor, action, key, fingerprint, 24*time.Hour,
	)
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	if reservation.Replayed {
		if !h.authorizeReplay(
			c,
			principal,
			requiredScope,
			action,
			strconv.FormatUint(uint64(ticketID), 10),
			true,
			risky,
		) {
			return
		}
		h.writeReplayedTicket(c, reservation.Record, http.StatusOK)
		return
	}
	result, err := h.native.UpdateTicketVersion(c.Request.Context(), services.VersionedTicketUpdateInput{
		TicketID:            ticketID,
		ExpectedVersion:     expectedVersion,
		LeaseID:             leaseID,
		Actor:               actor,
		CompatibilityUserID: h.compatibilityUserID,
		CredentialID:        principal.CredentialID,
		SourceProtocol:      "rest",
		RequestDigest:       digestBytes(fingerprint),
		Changes:             changes,
		RequiredScope:       requiredScope,
		Action:              action,
		IsRisky:             risky,
		TraceID:             RequestID(c),
		CorrelationID:       c.GetHeader("X-Correlation-ID"),
		IdempotencyRecordID: reservation.Record.ID,
	})
	if err != nil {
		_ = h.native.FailIdempotency(c.Request.Context(), reservation.Record.ID, services.AgentNativeErrorCode(err))
		h.writeNativeError(c, err)
		return
	}
	h.publishTicket(ticketID)
	c.Header("ETag", FormatETag(result.Ticket.Version))
	WriteReceipt(c, http.StatusOK, result.Ticket.ToResponse(), receiptFromService(result.Receipt))
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
	expectedVersion, err := ParseIfMatch(c.GetHeader("If-Match"))
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
		fmt.Sprintf("/api/v1/tickets/%d/claim", ticketID),
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
	if reservation.Replayed {
		if !h.authorizeReplay(
			c,
			principal,
			models.ScopeTasksManage,
			"ticket.claim",
			strconv.FormatUint(uint64(ticketID), 10),
			true,
			false,
		) {
			return
		}
		h.writeReplayedLease(c, reservation.Record)
		return
	}
	result, err := h.native.ClaimTicketLeaseCommand(
		c.Request.Context(),
		services.ClaimTicketLeaseCommandInput{
			TicketID:            ticketID,
			Actor:               actor,
			ExpectedVersion:     expectedVersion,
			TTL:                 time.Duration(request.TTLSeconds) * time.Second,
			CredentialID:        principal.CredentialID,
			SourceProtocol:      "rest",
			RequestDigest:       digestBytes(fingerprint),
			IdempotencyRecordID: reservation.Record.ID,
			TraceID:             RequestID(c),
			CorrelationID:       c.GetHeader("X-Correlation-ID"),
		},
	)
	if err != nil {
		_ = h.native.FailIdempotency(c.Request.Context(), reservation.Record.ID, services.AgentNativeErrorCode(err))
		h.writeNativeError(c, err)
		return
	}
	h.publishTicket(ticketID)
	WriteReceipt(c, http.StatusOK, leaseResponseData(result.Lease), receiptFromService(result.Receipt))
}

func (h *APIHandler) HeartbeatLease(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	expectedVersion, err := ParseIfMatch(c.GetHeader("If-Match"))
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
		"/api/v1/leases/"+c.Param("id")+"/heartbeat",
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
	if reservation.Replayed {
		if !h.authorizeReplay(
			c,
			principal,
			models.ScopeTasksManage,
			"ticket.lease.heartbeat",
			reservation.Record.ResourceID,
			true,
			false,
		) {
			return
		}
		h.writeReplayedLease(c, reservation.Record)
		return
	}
	result, err := h.native.HeartbeatTicketLeaseCommand(
		c.Request.Context(),
		services.HeartbeatTicketLeaseCommandInput{
			LeaseID:             c.Param("id"),
			Actor:               actor,
			ExpectedVersion:     expectedVersion,
			TTL:                 time.Duration(request.TTLSeconds) * time.Second,
			CredentialID:        principal.CredentialID,
			SourceProtocol:      "rest",
			RequestDigest:       digestBytes(fingerprint),
			IdempotencyRecordID: reservation.Record.ID,
			TraceID:             RequestID(c),
			CorrelationID:       c.GetHeader("X-Correlation-ID"),
		},
	)
	if err != nil {
		_ = h.native.FailIdempotency(c.Request.Context(), reservation.Record.ID, services.AgentNativeErrorCode(err))
		h.writeNativeError(c, err)
		return
	}
	h.publishTicket(result.Lease.TicketID)
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
		"/api/v1/leases/"+c.Param("id"),
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
	if reservation.Replayed {
		if !h.authorizeReplay(
			c,
			principal,
			models.ScopeTasksManage,
			"ticket.lease.release",
			reservation.Record.ResourceID,
			true,
			false,
		) {
			return
		}
		h.writeReplayedLease(c, reservation.Record)
		return
	}
	result, err := h.native.ReleaseTicketLeaseCommand(
		c.Request.Context(),
		services.ReleaseTicketLeaseCommandInput{
			LeaseID:             c.Param("id"),
			Actor:               actor,
			Reason:              "released by REST client",
			CredentialID:        principal.CredentialID,
			SourceProtocol:      "rest",
			RequestDigest:       digestBytes(fingerprint),
			IdempotencyRecordID: reservation.Record.ID,
			TraceID:             RequestID(c),
			CorrelationID:       c.GetHeader("X-Correlation-ID"),
		},
	)
	if err != nil {
		_ = h.native.FailIdempotency(c.Request.Context(), reservation.Record.ID, services.AgentNativeErrorCode(err))
		h.writeNativeError(c, err)
		return
	}
	h.publishTicket(result.Lease.TicketID)
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
	expectedVersion, err := ParseIfMatch(c.GetHeader("If-Match"))
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
		fmt.Sprintf("/api/v1/tickets/%d/comments", ticketID),
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
	if reservation.Replayed {
		if !h.authorizeReplay(
			c,
			principal,
			models.ScopeCommentsWrite,
			"ticket.comment.create",
			strconv.FormatUint(uint64(ticketID), 10),
			true,
			false,
		) {
			return
		}
		h.writeReplayedComment(c, reservation.Record)
		return
	}
	result, err := h.native.CreateComment(c.Request.Context(), services.NativeCommentInput{
		TicketID:            ticketID,
		ExpectedVersion:     expectedVersion,
		LeaseID:             leaseID,
		Actor:               actor,
		CompatibilityUserID: h.compatibilityUserID,
		CredentialID:        principal.CredentialID,
		SourceProtocol:      "rest",
		RequestDigest:       digestBytes(fingerprint),
		Content:             request.Content,
		ContentType:         request.ContentType,
		Type:                request.Type,
		Reason:              request.RationaleSummary,
		EvidenceRefs:        request.Evidence,
		InputSources:        request.InputSources,
		TraceID:             RequestID(c),
		IdempotencyRecordID: reservation.Record.ID,
	})
	if err != nil {
		_ = h.native.FailIdempotency(c.Request.Context(), reservation.Record.ID, services.AgentNativeErrorCode(err))
		h.writeNativeError(c, err)
		return
	}
	h.publishTicket(ticketID)
	c.Header("ETag", FormatETag(result.Receipt.ResourceVersion))
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
	expectedVersion, err := ParseIfMatch(c.GetHeader("If-Match"))
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
		fmt.Sprintf("/api/v1/tickets/%d/attachments", ticketID),
		expectedVersion,
		leaseID,
		idempotencyBody,
	)
	actor := models.ServicePrincipalActor(principal.ID)
	reservation, err := h.native.ReserveIdempotency(
		c.Request.Context(), actor, "ticket.attachment.create", key, fingerprint, 24*time.Hour,
	)
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	if reservation.Replayed {
		if !h.authorizeReplay(
			c,
			principal,
			models.ScopeAttachmentsWrite,
			"ticket.attachment.create",
			strconv.FormatUint(uint64(ticketID), 10),
			true,
			false,
		) {
			return
		}
		h.writeReplayedAttachment(c, reservation.Record)
		return
	}
	result, err := h.native.StoreAttachment(c.Request.Context(), services.NativeAttachmentInput{
		TicketID:            ticketID,
		ExpectedVersion:     expectedVersion,
		LeaseID:             leaseID,
		Actor:               actor,
		CompatibilityUserID: h.compatibilityUserID,
		CredentialID:        principal.CredentialID,
		SourceProtocol:      "rest",
		RequestDigest:       digestBytes(fingerprint),
		OriginalName:        header.Filename,
		ContentType:         header.Header.Get("Content-Type"),
		Description:         c.PostForm("description"),
		IsPublic:            c.PostForm("visibility") == "public",
		Reader:              bytes.NewReader(content),
		TraceID:             RequestID(c),
		IdempotencyRecordID: reservation.Record.ID,
	})
	if err != nil {
		_ = h.native.FailIdempotency(c.Request.Context(), reservation.Record.ID, services.AgentNativeErrorCode(err))
		h.writeNativeError(c, err)
		return
	}
	h.publishTicket(ticketID)
	c.Header("ETag", FormatETag(result.Receipt.ResourceVersion))
	WriteReceipt(c, http.StatusCreated, result.Attachment.ToResponse(), receiptFromService(result.Receipt))
}

func (h *APIHandler) ListComments(c *gin.Context) {
	if _, ok := h.loadAuthorizedTicket(c); !ok {
		return
	}
	ticketID, _ := parsePathUint(c, "id")
	var comments []models.TicketComment
	if err := h.db.WithContext(c.Request.Context()).
		Preload("User").
		Preload("ServicePrincipal").
		Where("ticket_id = ? AND is_deleted = ?", ticketID, false).
		Order("created_at ASC").
		Find(&comments).Error; err != nil {
		h.writeNativeError(c, err)
		return
	}
	result := make([]*models.TicketCommentResponse, 0, len(comments))
	for i := range comments {
		result = append(result, comments[i].ToResponse())
	}
	WriteData(c, http.StatusOK, result, Meta{})
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
	var attachments []models.TicketAttachment
	if err := h.db.WithContext(c.Request.Context()).
		Where("ticket_id = ?", ticketID).
		Order("created_at DESC").
		Find(&attachments).Error; err != nil {
		h.writeNativeError(c, err)
		return
	}
	result := make([]*models.TicketAttachmentResponse, 0, len(attachments))
	for i := range attachments {
		result = append(result, attachments[i].ToResponse())
	}
	WriteData(c, http.StatusOK, result, Meta{})
}

func (h *APIHandler) DownloadAttachment(c *gin.Context) {
	attachmentID, ok := parsePathUint(c, "id")
	if !ok {
		return
	}
	var metadata models.TicketAttachment
	if err := h.db.WithContext(c.Request.Context()).First(&metadata, attachmentID).Error; err != nil {
		h.writeNativeError(c, err)
		return
	}
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	if _, err := h.native.CheckAction(c.Request.Context(), services.PolicyCheckInput{
		ServicePrincipalID: principal.ID,
		CredentialID:       principal.CredentialID,
		Scope:              models.ScopeAttachmentsRead,
		Action:             "ticket.attachment.read",
		ResourceType:       "ticket",
		ResourceID:         strconv.FormatUint(uint64(metadata.TicketID), 10),
		SourceProtocol:     "rest",
	}); err != nil {
		h.writeNativeError(c, err)
		return
	}
	attachment, reader, err := h.native.OpenAttachment(c.Request.Context(), attachmentID)
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	defer reader.Close()
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, attachment.OriginalName))
	c.DataFromReader(http.StatusOK, attachment.FileSize, attachment.MimeType, reader, nil)
}

func (h *APIHandler) GetHistory(c *gin.Context) {
	if _, ok := h.loadAuthorizedTicket(c); !ok {
		return
	}
	ticketID, _ := parsePathUint(c, "id")
	limit, err := ParseLimit(c, 50, 100)
	if err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}
	var history []models.TicketHistory
	if err := h.db.WithContext(c.Request.Context()).
		Preload("User").
		Preload("ServicePrincipal").
		Where("ticket_id = ?", ticketID).
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
	query := h.db.WithContext(c.Request.Context()).Model(&models.DomainEvent{})
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

func (h *APIHandler) executionLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		principalID := c.GetString(agentauth.ContextPrincipalID)
		release, err := h.native.AcquireAgentExecution(c.Request.Context(), principalID)
		if err != nil {
			h.writeNativeError(c, err)
			c.Abort()
			return
		}
		defer release()
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

func (h *APIHandler) authorizeReplay(
	c *gin.Context,
	principal authenticatedPrincipal,
	scope string,
	action string,
	resourceID string,
	write bool,
	risky bool,
) bool {
	_, err := h.native.CheckAction(c.Request.Context(), services.PolicyCheckInput{
		ServicePrincipalID: principal.ID,
		CredentialID:       principal.CredentialID,
		Scope:              scope,
		Action:             action,
		ResourceType:       "ticket",
		ResourceID:         resourceID,
		IsWrite:            write,
		IsRisky:            risky,
		SourceProtocol:     "rest",
		Context:            map[string]any{"idempotent_replay": true},
	})
	if err != nil {
		h.writeNativeError(c, err)
		return false
	}
	return true
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
	if err := h.db.WithContext(c.Request.Context()).
		Preload("CreatedBy").
		Preload("AssignedTo").
		First(&ticket, ticketID).Error; err != nil {
		h.writeNativeError(c, err)
		return nil, false
	}
	return &ticket, true
}

func classifyTicketPatch(changes map[string]any) (scope, action string, risky bool, err error) {
	const (
		updateKind = "update"
		assignKind = "assign"
		statusKind = "transition"
	)
	kind := ""
	for rawField := range changes {
		field := strings.ToLower(strings.TrimSpace(rawField))
		if field == "source" || field == "trust_level" {
			return "", "", false, fmt.Errorf("%s is server-controlled and cannot be changed by an Agent", field)
		}
		fieldKind := updateKind
		switch field {
		case "status":
			fieldKind = statusKind
		case "assigned_to_id",
			"assigned_to_actor_type",
			"assigned_to_actor_id",
			"assigned_to_service_principal_id":
			fieldKind = assignKind
		}
		if kind == "" {
			kind = fieldKind
			continue
		}
		if kind != fieldKind {
			return "", "", false, errors.New(
				"ordinary updates, assignments and transitions must be sent as separate commands",
			)
		}
	}
	switch kind {
	case statusKind:
		return models.ScopeTicketsTransition, "ticket.transition", true, nil
	case assignKind:
		return models.ScopeTicketsAssign, "ticket.assign", true, nil
	default:
		return models.ScopeTicketsUpdate, "ticket.update", false, nil
	}
}

func (h *APIHandler) writeReplayedTicket(c *gin.Context, record *models.IdempotencyRecord, status int) {
	var receipt Receipt
	_ = json.Unmarshal(record.ResponseBody, &receipt)
	if len(record.ResourceSnapshot) > 0 {
		var snapshot models.TicketResponse
		if json.Unmarshal(record.ResourceSnapshot, &snapshot) == nil && snapshot.ID > 0 {
			c.Header("ETag", FormatETag(receipt.ResourceVersion))
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
	if err := h.db.WithContext(c.Request.Context()).First(&ticket, uint(resourceID)).Error; err != nil {
		h.writeIdempotentBody(c, record)
		return
	}
	c.Header("ETag", FormatETag(ticket.Version))
	WriteReceipt(c, status, ticket.ToResponse(), receipt)
}

func (h *APIHandler) writeReplayedLease(c *gin.Context, record *models.IdempotencyRecord) {
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
	var receipt Receipt
	_ = json.Unmarshal(record.ResponseBody, &receipt)
	if len(record.ResourceSnapshot) > 0 {
		var snapshot models.TicketComment
		if json.Unmarshal(record.ResourceSnapshot, &snapshot) == nil && snapshot.ID > 0 {
			c.Header("ETag", FormatETag(receipt.ResourceVersion))
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
		First(&comment, uint(resourceID)).Error; err != nil {
		h.writeIdempotentBody(c, record)
		return
	}
	c.Header("ETag", FormatETag(receipt.ResourceVersion))
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

func (h *APIHandler) writeReplayedAttachment(c *gin.Context, record *models.IdempotencyRecord) {
	var receipt Receipt
	_ = json.Unmarshal(record.ResponseBody, &receipt)
	if len(record.ResourceSnapshot) > 0 {
		var snapshot models.TicketAttachment
		if json.Unmarshal(record.ResourceSnapshot, &snapshot) == nil && snapshot.ID > 0 {
			c.Header("ETag", FormatETag(receipt.ResourceVersion))
			WriteReceipt(c, record.ResponseCode, snapshot.ToResponse(), receipt)
			return
		}
	}
	resourceID, err := strconv.ParseUint(record.ResourceID, 10, 32)
	if err != nil {
		h.writeIdempotentBody(c, record)
		return
	}
	var attachment models.TicketAttachment
	if err := h.db.WithContext(c.Request.Context()).First(&attachment, uint(resourceID)).Error; err != nil {
		h.writeIdempotentBody(c, record)
		return
	}
	c.Header("ETag", FormatETag(receipt.ResourceVersion))
	WriteReceipt(c, record.ResponseCode, attachment.ToResponse(), receipt)
}

func (h *APIHandler) writeIdempotentBody(c *gin.Context, record *models.IdempotencyRecord) {
	var response any
	if len(record.ResponseBody) > 0 {
		_ = json.Unmarshal(record.ResponseBody, &response)
	}
	WriteData(c, record.ResponseCode, response, Meta{})
}

func (h *APIHandler) publishTicket(ticketID uint) {
	if h.publisher != nil {
		h.publisher.PublishTicket(ticketID)
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
	case errors.Is(err, gorm.ErrRecordNotFound):
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
	case errors.Is(err, services.ErrPolicyDenied):
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
	case errors.Is(err, services.ErrVersionConflict):
		status, code = http.StatusConflict, ProblemVersionConflict
	case errors.Is(err, services.ErrLeaseConflict), errors.Is(err, services.ErrLeaseExpired), errors.Is(err, services.ErrLeaseNotOwned):
		status, code = http.StatusConflict, ProblemLeaseConflict
	case errors.Is(err, services.ErrIdempotencyConflict), errors.Is(err, services.ErrIdempotencyInProgress):
		status, code = http.StatusConflict, ProblemIdempotencyConflict
	case errors.Is(err, services.ErrOutboxReplayConflict):
		status, code = http.StatusConflict, ProblemOutboxConflict
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
