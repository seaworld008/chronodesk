package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/seaworld008/chronodesk/server/internal/httpcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// TicketContentHandler completes the human-facing comments and attachments
// API while delegating writes to the same transactional service used by Agent
// protocols.
type TicketContentHandler struct {
	db                 *gorm.DB
	ticketService      services.TicketServiceInterface
	native             *services.AgentNativeService
	maxAttachmentBytes int64
}

// customerCommentResponse intentionally contains only public conversation
// fields. Human account security data, service-principal controls, actor IDs
// and internal worklog/notification metadata are excluded.
type customerCommentResponse struct {
	ID          uint               `json:"id"`
	CreatedAt   time.Time          `json:"created_at"`
	TicketID    uint               `json:"ticket_id"`
	Content     string             `json:"content"`
	ContentType string             `json:"content_type"`
	Type        models.CommentType `json:"type"`
	ParentID    *uint              `json:"parent_id,omitempty"`
	ReplyCount  int                `json:"reply_count"`
	IsEdited    bool               `json:"is_edited"`
	EditedAt    *time.Time         `json:"edited_at,omitempty"`
}

// customerAttachmentResponse exposes only metadata customers need to display
// and download a public attachment. Storage identifiers, actor identities,
// hashes, scan diagnostics and internal counters remain privileged.
type customerAttachmentResponse struct {
	ID           uint                   `json:"id"`
	CreatedAt    time.Time              `json:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at"`
	TicketID     uint                   `json:"ticket_id"`
	CommentID    *uint                  `json:"comment_id,omitempty"`
	OriginalName string                 `json:"original_name"`
	FileSize     int64                  `json:"file_size"`
	MimeType     string                 `json:"mime_type"`
	FileType     models.AttachmentType  `json:"file_type"`
	Extension    string                 `json:"extension"`
	IsPublic     bool                   `json:"is_public"`
	VirusScan    models.VirusScanStatus `json:"virus_scan"`
	ScannedAt    *time.Time             `json:"scanned_at,omitempty"`
	Description  string                 `json:"description,omitempty"`
	Width        int                    `json:"width,omitempty"`
	Height       int                    `json:"height,omitempty"`
	PageCount    int                    `json:"page_count,omitempty"`
}

func customerAttachmentFromModel(attachment *models.TicketAttachment) *customerAttachmentResponse {
	if attachment == nil {
		return nil
	}
	return &customerAttachmentResponse{
		ID:           attachment.ID,
		CreatedAt:    attachment.CreatedAt,
		UpdatedAt:    attachment.UpdatedAt,
		TicketID:     attachment.TicketID,
		CommentID:    attachment.CommentID,
		OriginalName: attachment.OriginalName,
		FileSize:     attachment.FileSize,
		MimeType:     attachment.MimeType,
		FileType:     attachment.FileType,
		Extension:    attachment.Extension,
		IsPublic:     attachment.IsPublic,
		VirusScan:    attachment.VirusScan,
		ScannedAt:    attachment.ScannedAt,
		Description:  attachment.Description,
		Width:        attachment.Width,
		Height:       attachment.Height,
		PageCount:    attachment.PageCount,
	}
}

const (
	maxHumanCommentContentRunes = 10000
	maxHumanCommentRequestBytes = 64 << 10
)

type humanCommentCreateRequest struct {
	Content      string             `json:"content"`
	ContentType  string             `json:"content_type"`
	Type         models.CommentType `json:"type"`
	ParentID     *uint              `json:"parent_id"`
	TimeSpent    *int               `json:"time_spent"`
	BillableTime *int               `json:"billable_time"`
	WorkType     string             `json:"work_type"`
}

func NewTicketContentHandler(
	db *gorm.DB,
	ticketService services.TicketServiceInterface,
	native *services.AgentNativeService,
	maxAttachmentBytes int64,
) *TicketContentHandler {
	if maxAttachmentBytes <= 0 {
		maxAttachmentBytes = 10 << 20
	}
	return &TicketContentHandler{
		db:                 db,
		ticketService:      ticketService,
		native:             native,
		maxAttachmentBytes: maxAttachmentBytes,
	}
}

func (h *TicketContentHandler) RegisterRoutes(tickets *gin.RouterGroup) {
	tickets.GET("/:id/comments", h.ListComments)
	tickets.GET("/:id/comments/:comment_id/replies", h.ListCommentReplies)
	tickets.POST("/:id/comments", h.CreateComment)
	tickets.GET("/:id/attachments", h.ListAttachments)
}

// RegisterExternalRoutes mounts attachment object-storage and streaming
// operations on a group that uses ProjectExternalScopeMiddleware.
func (h *TicketContentHandler) RegisterExternalRoutes(
	tickets *gin.RouterGroup,
) {
	tickets.POST("/:id/attachments", h.StoreAttachment)
	tickets.GET("/:id/attachments/:attachment_id/content", h.DownloadAttachment)
}

func (h *TicketContentHandler) ListComments(c *gin.Context) {
	ticket, ok := h.authorizedTicket(c, ticketAccessRead)
	if !ok {
		return
	}
	page, pageSize, ok := parseStrictPagePagination(c, 25, 100)
	if !ok {
		return
	}
	customer := isRequesterRole(normalizedProjectRole(c))
	query := h.db.WithContext(c.Request.Context()).
		Model(&models.TicketComment{}).
		Where(
			"ticket_comments.ticket_id = ? AND ticket_comments.organization_id = ? AND ticket_comments.project_id = ? AND ticket_comments.is_deleted = ? AND ticket_comments.parent_id IS NULL",
			ticket.ID,
			ticket.OrganizationID,
			ticket.ProjectID,
			false,
		)
	if customer {
		query = query.Where("ticket_comments.type = ?", models.CommentTypePublic)
	} else {
		query = query.Preload("User").Preload("ServicePrincipal")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		h.writeError(c, err)
		return
	}
	replyVisibility := ""
	replyArguments := []any{
		ticket.ID,
		ticket.OrganizationID,
		ticket.ProjectID,
		false,
	}
	if customer {
		replyVisibility = " AND replies.type = ?"
		replyArguments = append(replyArguments, models.CommentTypePublic)
	}
	replyCountSQL := `(SELECT COUNT(*) FROM ticket_comments AS replies
		WHERE replies.parent_id = ticket_comments.id
		  AND replies.ticket_id = ?
		  AND replies.organization_id = ?
		  AND replies.project_id = ?
		  AND replies.is_deleted = ?` + replyVisibility + `) AS reply_count`
	var comments []models.TicketComment
	if err := query.
		Select("ticket_comments.*, "+replyCountSQL, replyArguments...).
		Order("ticket_comments.created_at ASC, ticket_comments.id ASC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&comments).Error; err != nil {
		h.writeError(c, err)
		return
	}
	if customer {
		writePageEnvelope(c, customerCommentResponses(comments), total, page, pageSize)
		return
	}
	result := make([]*models.TicketCommentResponse, 0, len(comments))
	for i := range comments {
		result = append(result, comments[i].ToResponse())
	}
	writePageEnvelope(c, result, total, page, pageSize)
}

func (h *TicketContentHandler) ListCommentReplies(c *gin.Context) {
	ticket, ok := h.authorizedTicket(c, ticketAccessRead)
	if !ok {
		return
	}
	commentID, err := strconv.ParseUint(c.Param("comment_id"), 10, 32)
	if err != nil || commentID == 0 {
		writeHumanCommentRequestError(c, "invalid_request", "父评论 ID 无效")
		return
	}
	page, pageSize, ok := parseStrictPagePagination(c, 25, 100)
	if !ok {
		return
	}
	customer := isRequesterRole(normalizedProjectRole(c))
	parentQuery := h.db.WithContext(c.Request.Context()).
		Model(&models.TicketComment{}).
		Where(
			"id = ? AND ticket_id = ? AND organization_id = ? AND project_id = ? AND parent_id IS NULL AND is_deleted = ?",
			uint(commentID),
			ticket.ID,
			ticket.OrganizationID,
			ticket.ProjectID,
			false,
		)
	if customer {
		parentQuery = parentQuery.Where("type = ?", models.CommentTypePublic)
	}
	var parentCount int64
	if err := parentQuery.Count(&parentCount).Error; err != nil {
		h.writeError(c, err)
		return
	}
	if parentCount != 1 {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"code":    "not_found",
			"message": "父评论不存在",
		})
		return
	}

	query := h.db.WithContext(c.Request.Context()).
		Model(&models.TicketComment{}).
		Where(
			"ticket_id = ? AND organization_id = ? AND project_id = ? AND parent_id = ? AND is_deleted = ?",
			ticket.ID,
			ticket.OrganizationID,
			ticket.ProjectID,
			uint(commentID),
			false,
		)
	if customer {
		query = query.Where("type = ?", models.CommentTypePublic)
	} else {
		query = query.Preload("User").Preload("ServicePrincipal")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		h.writeError(c, err)
		return
	}
	var replies []models.TicketComment
	if err := query.
		Order("created_at ASC, id ASC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&replies).Error; err != nil {
		h.writeError(c, err)
		return
	}
	if customer {
		writePageEnvelope(c, customerCommentResponses(replies), total, page, pageSize)
		return
	}
	result := make([]*models.TicketCommentResponse, 0, len(replies))
	for i := range replies {
		result = append(result, replies[i].ToResponse())
	}
	writePageEnvelope(c, result, total, page, pageSize)
}

func (h *TicketContentHandler) CreateComment(c *gin.Context) {
	ticket, ok := h.authorizedTicket(c, ticketAccessUpdate)
	if !ok {
		return
	}
	expectedVersion, ok := requireTicketIfMatch(c)
	if !ok {
		return
	}
	request, ok := decodeHumanCommentRequest(c)
	if !ok {
		return
	}
	if !validateHumanCommentRequest(c, request) {
		return
	}
	if request.Type == "" {
		request.Type = models.CommentTypePublic
	}
	if request.Type == models.CommentTypeSystem ||
		(isRequesterRole(normalizedProjectRole(c)) && request.Type != models.CommentTypePublic) {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"code":    "comment_visibility_denied",
			"message": "当前身份不能创建该可见性级别的评论",
		})
		return
	}
	if isRequesterRole(normalizedProjectRole(c)) {
		if request.TimeSpent != nil || request.BillableTime != nil || strings.TrimSpace(request.WorkType) != "" {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"code":    "comment_worklog_denied",
				"message": "客户不能提交工时、计费时间或工作类型",
			})
			return
		}
		if request.ParentID != nil && !h.customerCanReferenceComment(c, ticket.ID, *request.ParentID) {
			return
		}
	}
	userID := c.GetUint("user_id")
	result, err := h.native.CreateComment(c.Request.Context(), services.NativeCommentInput{
		TicketID:        ticket.ID,
		ExpectedVersion: expectedVersion,
		Actor:           models.HumanActor(userID),
		SourceProtocol:  "rest-human",
		Content:         request.Content,
		ContentType:     request.ContentType,
		Type:            request.Type,
		ParentID:        request.ParentID,
		TimeSpent:       request.TimeSpent,
		BillableTime:    request.BillableTime,
		WorkType:        request.WorkType,
		TraceID:         requestID(c),
		CorrelationID:   c.GetHeader("X-Correlation-ID"),
	})
	if err != nil {
		h.writeCommentError(c, err)
		return
	}
	c.Header("ETag", httpcontract.FormatETag(result.Receipt.ResourceVersion))
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    result.Comment.ToResponse(),
		"receipt": result.Receipt,
	})
}

func decodeHumanCommentRequest(c *gin.Context) (*humanCommentCreateRequest, bool) {
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		maxHumanCommentRequestBytes,
	)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()

	var request humanCommentCreateRequest
	if err := decoder.Decode(&request); err != nil {
		var tooLarge *http.MaxBytesError
		switch {
		case errors.Is(err, io.EOF):
			writeHumanCommentRequestError(c, "invalid_request", "请求正文不能为空")
		case errors.As(err, &tooLarge):
			writeHumanCommentRequestError(c, "invalid_request", "请求正文超过大小限制")
		default:
			writeHumanCommentRequestError(c, "invalid_request", "请求正文必须是有效的 JSON 对象")
		}
		return nil, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeHumanCommentRequestError(c, "invalid_request", "请求正文只能包含一个 JSON 对象")
		return nil, false
	}
	return &request, true
}

func validateHumanCommentRequest(c *gin.Context, request *humanCommentCreateRequest) bool {
	request.Content = strings.TrimSpace(request.Content)
	switch {
	case request.Content == "":
		writeHumanCommentRequestError(c, "validation_error", "评论内容不能为空")
		return false
	case utf8.RuneCountInString(request.Content) > maxHumanCommentContentRunes:
		writeHumanCommentRequestError(c, "validation_error", "评论内容不能超过 10000 个字符")
		return false
	}

	request.ContentType = strings.TrimSpace(request.ContentType)
	if request.ContentType == "" {
		request.ContentType = "text"
	}
	if request.ContentType != "text" && request.ContentType != "markdown" {
		writeHumanCommentRequestError(c, "validation_error", "评论内容格式无效，仅支持纯文本或 Markdown")
		return false
	}

	request.Type = models.CommentType(strings.TrimSpace(string(request.Type)))
	if request.Type == "" {
		request.Type = models.CommentTypePublic
	}
	if request.Type != models.CommentTypePublic &&
		request.Type != models.CommentTypeInternal &&
		request.Type != models.CommentTypeSystem {
		writeHumanCommentRequestError(c, "validation_error", "评论类型无效，仅支持公开或内部评论")
		return false
	}
	if request.ParentID != nil && *request.ParentID == 0 {
		writeHumanCommentRequestError(c, "validation_error", "父评论 ID 必须大于 0")
		return false
	}
	if request.TimeSpent != nil && *request.TimeSpent < 0 {
		writeHumanCommentRequestError(c, "validation_error", "工时不能为负数")
		return false
	}
	if request.BillableTime != nil && *request.BillableTime < 0 {
		writeHumanCommentRequestError(c, "validation_error", "计费时间不能为负数")
		return false
	}
	request.WorkType = strings.TrimSpace(request.WorkType)
	return true
}

func writeHumanCommentRequestError(c *gin.Context, code, message string) {
	c.JSON(http.StatusBadRequest, gin.H{
		"success": false,
		"code":    code,
		"message": message,
	})
}

func (h *TicketContentHandler) writeCommentError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, services.ErrNestedCommentReply):
		writeHumanCommentRequestError(c, "nested_comment_reply", "仅支持单层回复，不能回复已有父评论的回复")
	case errors.Is(err, services.ErrInvalidComment):
		writeHumanCommentRequestError(c, "validation_error", "评论请求不符合要求")
	case errors.Is(err, services.ErrInvalidActor):
		writeHumanCommentRequestError(c, "invalid_request", "当前用户身份无效")
	case errors.Is(err, gorm.ErrRecordNotFound), errors.Is(err, services.ErrVersionConflict):
		h.writeError(c, err)
	default:
		logHandlerFailure(c, "ticket_comment.create", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"code":    "internal_error",
			"message": "评论提交失败，请稍后重试",
		})
	}
}

func (h *TicketContentHandler) ListAttachments(c *gin.Context) {
	ticket, ok := h.authorizedTicket(c, ticketAccessRead)
	if !ok {
		return
	}
	page, pageSize, ok := parseStrictPagePagination(c, 25, 100)
	if !ok {
		return
	}
	query := h.db.WithContext(c.Request.Context()).
		Model(&models.TicketAttachment{}).
		Where(
			"ticket_id = ? AND organization_id = ? AND project_id = ?",
			ticket.ID,
			ticket.OrganizationID,
			ticket.ProjectID,
		)
	if isRequesterRole(normalizedProjectRole(c)) {
		query = query.Where("is_public = ?", true)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		h.writeError(c, err)
		return
	}
	var attachments []models.TicketAttachment
	if err := query.
		Order("created_at DESC, id DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&attachments).Error; err != nil {
		h.writeError(c, err)
		return
	}
	if isRequesterRole(normalizedProjectRole(c)) {
		result := make([]*customerAttachmentResponse, 0, len(attachments))
		for i := range attachments {
			result = append(result, customerAttachmentFromModel(&attachments[i]))
		}
		writePageEnvelope(c, result, total, page, pageSize)
		return
	}
	result := make([]*models.TicketAttachmentResponse, 0, len(attachments))
	for i := range attachments {
		result = append(result, attachments[i].ToResponse())
	}
	writePageEnvelope(c, result, total, page, pageSize)
}

func customerCommentResponses(comments []models.TicketComment) []customerCommentResponse {
	result := make([]customerCommentResponse, 0, len(comments))
	for i := range comments {
		result = append(result, customerCommentResponse{
			ID:          comments[i].ID,
			CreatedAt:   comments[i].CreatedAt,
			TicketID:    comments[i].TicketID,
			Content:     comments[i].Content,
			ContentType: comments[i].ContentType,
			Type:        comments[i].Type,
			ParentID:    comments[i].ParentID,
			ReplyCount:  comments[i].ReplyCount,
			IsEdited:    comments[i].IsEdited,
			EditedAt:    comments[i].EditedAt,
		})
	}
	return result
}

func parseStrictPagePagination(
	c *gin.Context,
	defaultPageSize int,
	maxPageSize int,
) (int, int, bool) {
	page := 1
	pageSize := defaultPageSize
	for name, destination := range map[string]*int{
		"page":      &page,
		"page_size": &pageSize,
	} {
		raw := strings.TrimSpace(c.Query(name))
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 ||
			(name == "page" && value > 1_000_000) ||
			(name == "page_size" && value > maxPageSize) {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"code":    "invalid_pagination",
				"message": "分页参数无效，page 必须为正整数，page_size 必须在 1 到 100 之间",
			})
			return 0, 0, false
		}
		*destination = value
	}
	return page, pageSize, true
}

func writePageEnvelope(c *gin.Context, data any, total int64, page, pageSize int) {
	totalPages := int64(0)
	if total > 0 {
		totalPages = (total + int64(pageSize) - 1) / int64(pageSize)
	}
	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"data":        data,
		"total":       total,
		"page":        page,
		"page_size":   pageSize,
		"total_pages": totalPages,
	})
}

func (h *TicketContentHandler) StoreAttachment(c *gin.Context) {
	ticketID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || ticketID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"code":    "invalid_request",
			"message": "工单 ID 无效",
		})
		return
	}
	expectedVersion, ok := requireTicketIfMatch(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.maxAttachmentBytes+(1<<20))
	if err := c.Request.ParseMultipartForm(h.maxAttachmentBytes); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) ||
			errors.Is(err, multipart.ErrMessageTooLarge) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{
				"success": false,
				"code":    "attachment_rejected",
				"message": "附件超过大小限制",
			})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"code":    "invalid_request",
			"message": "附件请求格式无效",
		})
		return
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "invalid_request", "message": "缺少 file 字段"})
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, h.maxAttachmentBytes+1))
	if err != nil || int64(len(content)) > h.maxAttachmentBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"success": false,
			"code":    "attachment_rejected",
			"message": "附件超过大小限制",
		})
		return
	}
	isPublic := strings.EqualFold(c.PostForm("visibility"), "public") ||
		strings.EqualFold(c.PostForm("is_public"), "true")
	if isRequesterRole(normalizedProjectRole(c)) {
		isPublic = true
	}
	var commentID *uint
	if raw := strings.TrimSpace(c.PostForm("comment_id")); raw != "" {
		value, parseErr := strconv.ParseUint(raw, 10, 32)
		if parseErr != nil || value == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "invalid_request", "message": "comment_id 无效"})
			return
		}
		parsed := uint(value)
		commentID = &parsed
	}
	userID := c.GetUint("user_id")
	result, err := h.native.StoreAttachment(c.Request.Context(), services.NativeAttachmentInput{
		TicketID:        uint(ticketID),
		CommentID:       commentID,
		ExpectedVersion: expectedVersion,
		Actor:           models.HumanActor(userID),
		SourceProtocol:  "rest-human",
		OriginalName:    header.Filename,
		ContentType:     header.Header.Get("Content-Type"),
		Description:     c.PostForm("description"),
		IsPublic:        isPublic,
		Reader:          bytes.NewReader(content),
		TraceID:         requestID(c),
		CorrelationID:   c.GetHeader("X-Correlation-ID"),
	})
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.Header("ETag", httpcontract.FormatETag(result.Receipt.ResourceVersion))
	responseData := any(result.Attachment.ToResponse())
	if isRequesterRole(normalizedProjectRole(c)) {
		responseData = customerAttachmentFromModel(result.Attachment)
	}
	c.JSON(http.StatusAccepted, gin.H{
		"success": true,
		"data":    responseData,
		"receipt": result.Receipt,
	})
}

func (h *TicketContentHandler) customerCanReferenceComment(c *gin.Context, ticketID, commentID uint) bool {
	scope, err := services.RequireProjectScope(c.Request.Context())
	if err != nil {
		h.writeError(c, err)
		return false
	}
	var parent models.TicketComment
	err = h.db.WithContext(c.Request.Context()).
		Select("id", "parent_id").
		Where(
			"id = ? AND ticket_id = ? AND organization_id = ? AND project_id = ? AND type = ? AND is_deleted = ?",
			commentID,
			ticketID,
			scope.OrganizationID,
			scope.ProjectID,
			models.CommentTypePublic,
			false,
		).
		First(&parent).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"code":    "comment_reference_denied",
			"message": "客户只能关联当前工单中未删除的公开评论",
		})
		return false
	}
	if err != nil {
		h.writeError(c, err)
		return false
	}
	if parent.ParentID != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"code":    "nested_comment_reply",
			"message": "仅支持单层回复，不能回复已有父评论的回复",
		})
		return false
	}
	return true
}

func (h *TicketContentHandler) DownloadAttachment(c *gin.Context) {
	ticketID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || ticketID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "invalid_request", "message": "工单 ID 无效"})
		return
	}
	attachmentID, err := strconv.ParseUint(c.Param("attachment_id"), 10, 32)
	if err != nil || attachmentID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "invalid_request", "message": "附件 ID 无效"})
		return
	}
	attachment, reader, err := h.native.OpenTicketAttachment(
		c.Request.Context(),
		uint(ticketID),
		uint(attachmentID),
	)
	if err != nil {
		h.writeError(c, err)
		return
	}
	defer reader.Close()
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, attachment.OriginalName))
	c.DataFromReader(http.StatusOK, attachment.FileSize, attachment.MimeType, reader, nil)
}

func (h *TicketContentHandler) authorizedTicket(
	c *gin.Context,
	mode ticketAccessMode,
) (*models.Ticket, bool) {
	ticketID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || ticketID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "invalid_request", "message": "工单 ID 无效"})
		return nil, false
	}
	ticket, err := authorizeTicket(c.Request.Context(), c, h.ticketService, uint(ticketID), mode)
	if err != nil {
		if !writeTicketAuthorizationError(c, err) {
			h.writeError(c, err)
		}
		return nil, false
	}
	return ticket, true
}

func (h *TicketContentHandler) writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound), err.Error() == "ticket not found":
		c.JSON(http.StatusNotFound, gin.H{"success": false, "code": "not_found", "message": "资源不存在"})
	case errors.Is(err, services.ErrVersionConflict):
		writeTicketVersionConflict(c)
	case errors.Is(err, services.ErrAttachmentTooLarge):
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"success": false, "code": "attachment_rejected", "message": "附件超过大小限制"})
	case errors.Is(err, services.ErrAttachmentNotClean):
		c.JSON(http.StatusConflict, gin.H{"success": false, "code": "attachment_not_clean", "message": "附件尚未通过安全扫描"})
	case errors.Is(err, services.ErrInvalidAttachmentName):
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "attachment_rejected", "message": "附件名称无效"})
	case errors.Is(err, services.ErrInvalidAttachment):
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "attachment_rejected", "message": "附件内容不能为空"})
	case errors.Is(err, services.ErrProjectAccessDenied):
		c.JSON(http.StatusForbidden, gin.H{"success": false, "code": "ticket_access_denied", "message": "无权访问或修改该工单"})
	case err.Error() == "attachment comment not found":
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "invalid_request", "message": "关联评论不存在或不属于当前工单"})
	default:
		logHandlerFailure(c, "ticket_content.operation", err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "code": "internal_error", "message": "操作失败，请稍后重试"})
	}
}

func requestID(c *gin.Context) string {
	if value := c.GetString("request_id"); value != "" {
		return value
	}
	return c.GetHeader("X-Request-ID")
}
