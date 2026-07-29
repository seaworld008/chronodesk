package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gongdan-system/internal/models"
	"gongdan-system/internal/services"

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
	IsEdited    bool               `json:"is_edited"`
	EditedAt    *time.Time         `json:"edited_at,omitempty"`
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
	tickets.POST("/:id/comments", h.CreateComment)
	tickets.GET("/:id/attachments", h.ListAttachments)
	tickets.POST("/:id/attachments", h.StoreAttachment)
	tickets.GET("/:id/attachments/:attachment_id/content", h.DownloadAttachment)
}

func (h *TicketContentHandler) ListComments(c *gin.Context) {
	ticket, ok := h.authorizedTicket(c, ticketAccessRead)
	if !ok {
		return
	}
	customer := isCustomerRole(normalizedUserRole(c))
	query := h.db.WithContext(c.Request.Context()).
		Where("ticket_id = ? AND is_deleted = ?", ticket.ID, false)
	if customer {
		query = query.Where("type = ?", models.CommentTypePublic)
	} else {
		query = query.Preload("User").Preload("ServicePrincipal")
	}
	var comments []models.TicketComment
	if err := query.Order("created_at ASC, id ASC").Find(&comments).Error; err != nil {
		h.writeError(c, err)
		return
	}
	if customer {
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
				IsEdited:    comments[i].IsEdited,
				EditedAt:    comments[i].EditedAt,
			})
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": result, "total": len(result)})
		return
	}
	result := make([]*models.TicketCommentResponse, 0, len(comments))
	for i := range comments {
		result = append(result, comments[i].ToResponse())
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result, "total": len(result)})
}

func (h *TicketContentHandler) CreateComment(c *gin.Context) {
	ticket, ok := h.authorizedTicket(c, ticketAccessUpdate)
	if !ok {
		return
	}
	var request struct {
		Content      string             `json:"content" binding:"required,max=10000"`
		ContentType  string             `json:"content_type"`
		Type         models.CommentType `json:"type"`
		ParentID     *uint              `json:"parent_id"`
		TimeSpent    *int               `json:"time_spent"`
		BillableTime *int               `json:"billable_time"`
		WorkType     string             `json:"work_type"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "invalid_request", "message": err.Error()})
		return
	}
	if request.Type == "" {
		request.Type = models.CommentTypePublic
	}
	if request.Type == models.CommentTypeSystem ||
		(isCustomerRole(normalizedUserRole(c)) && request.Type != models.CommentTypePublic) {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"code":    "comment_visibility_denied",
			"message": "当前身份不能创建该可见性级别的评论",
		})
		return
	}
	if isCustomerRole(normalizedUserRole(c)) {
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
		TicketID:            ticket.ID,
		ExpectedVersion:     ticket.Version,
		Actor:               models.HumanActor(userID),
		CompatibilityUserID: userID,
		SourceProtocol:      "rest-human",
		Content:             request.Content,
		ContentType:         request.ContentType,
		Type:                request.Type,
		ParentID:            request.ParentID,
		TimeSpent:           request.TimeSpent,
		BillableTime:        request.BillableTime,
		WorkType:            request.WorkType,
		TraceID:             requestID(c),
		CorrelationID:       c.GetHeader("X-Correlation-ID"),
	})
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.Header("ETag", fmt.Sprintf(`"v%d"`, result.Receipt.ResourceVersion))
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    result.Comment.ToResponse(),
		"receipt": result.Receipt,
	})
}

func (h *TicketContentHandler) ListAttachments(c *gin.Context) {
	ticket, ok := h.authorizedTicket(c, ticketAccessRead)
	if !ok {
		return
	}
	query := h.db.WithContext(c.Request.Context()).
		Where("ticket_id = ?", ticket.ID)
	if isCustomerRole(normalizedUserRole(c)) {
		query = query.Where("is_public = ?", true)
	}
	var attachments []models.TicketAttachment
	if err := query.Order("created_at DESC, id DESC").Find(&attachments).Error; err != nil {
		h.writeError(c, err)
		return
	}
	result := make([]*models.TicketAttachmentResponse, 0, len(attachments))
	for i := range attachments {
		result = append(result, attachments[i].ToResponse())
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result, "total": len(result)})
}

func (h *TicketContentHandler) StoreAttachment(c *gin.Context) {
	ticket, ok := h.authorizedTicket(c, ticketAccessUpdate)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.maxAttachmentBytes+(1<<20))
	if err := c.Request.ParseMultipartForm(h.maxAttachmentBytes); err != nil {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"success": false,
			"code":    "attachment_rejected",
			"message": "附件请求无效或超过大小限制",
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
	if isCustomerRole(normalizedUserRole(c)) {
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
	if isCustomerRole(normalizedUserRole(c)) && commentID != nil &&
		!h.customerCanReferenceComment(c, ticket.ID, *commentID) {
		return
	}
	userID := c.GetUint("user_id")
	result, err := h.native.StoreAttachment(c.Request.Context(), services.NativeAttachmentInput{
		TicketID:            ticket.ID,
		CommentID:           commentID,
		ExpectedVersion:     ticket.Version,
		Actor:               models.HumanActor(userID),
		CompatibilityUserID: userID,
		SourceProtocol:      "rest-human",
		OriginalName:        header.Filename,
		ContentType:         header.Header.Get("Content-Type"),
		Description:         c.PostForm("description"),
		IsPublic:            isPublic,
		Reader:              bytes.NewReader(content),
		TraceID:             requestID(c),
		CorrelationID:       c.GetHeader("X-Correlation-ID"),
	})
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.Header("ETag", fmt.Sprintf(`"v%d"`, result.Receipt.ResourceVersion))
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    result.Attachment.ToResponse(),
		"receipt": result.Receipt,
	})
}

func (h *TicketContentHandler) customerCanReferenceComment(c *gin.Context, ticketID, commentID uint) bool {
	var count int64
	err := h.db.WithContext(c.Request.Context()).
		Model(&models.TicketComment{}).
		Where(
			"id = ? AND ticket_id = ? AND type = ? AND is_deleted = ?",
			commentID,
			ticketID,
			models.CommentTypePublic,
			false,
		).
		Count(&count).Error
	if err != nil {
		h.writeError(c, err)
		return false
	}
	if count != 1 {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"code":    "comment_reference_denied",
			"message": "客户只能关联当前工单中未删除的公开评论",
		})
		return false
	}
	return true
}

func (h *TicketContentHandler) DownloadAttachment(c *gin.Context) {
	attachmentID, err := strconv.ParseUint(c.Param("attachment_id"), 10, 32)
	if err != nil || attachmentID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "invalid_request", "message": "附件 ID 无效"})
		return
	}
	var metadata models.TicketAttachment
	if err := h.db.WithContext(c.Request.Context()).
		Where("id = ? AND ticket_id = ?", uint(attachmentID), c.Param("id")).
		First(&metadata).Error; err != nil {
		h.writeError(c, err)
		return
	}
	if _, ok := h.authorizedTicket(c, ticketAccessRead); !ok {
		return
	}
	if isCustomerRole(normalizedUserRole(c)) && !metadata.IsPublic {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "code": "ticket_access_denied", "message": "无权下载该附件"})
		return
	}
	attachment, reader, err := h.native.OpenAttachment(c.Request.Context(), uint(attachmentID))
	if err != nil {
		h.writeError(c, err)
		return
	}
	defer reader.Close()
	_ = h.db.WithContext(c.Request.Context()).Model(&models.TicketAttachment{}).
		Where("id = ?", attachment.ID).
		UpdateColumn("download_count", gorm.Expr("download_count + 1")).Error
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
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"success": false, "code": "not_found", "message": "资源不存在"})
	case errors.Is(err, services.ErrVersionConflict):
		c.JSON(http.StatusConflict, gin.H{"success": false, "code": "version_conflict", "message": "工单已被其他操作更新，请刷新后重试"})
	case errors.Is(err, services.ErrAttachmentTooLarge):
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"success": false, "code": "attachment_rejected", "message": "附件超过大小限制"})
	case errors.Is(err, services.ErrAttachmentNotClean):
		c.JSON(http.StatusConflict, gin.H{"success": false, "code": "attachment_not_clean", "message": "附件尚未通过安全扫描"})
	case errors.Is(err, services.ErrInvalidAttachmentName):
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "attachment_rejected", "message": "附件名称无效"})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "invalid_request", "message": err.Error()})
	}
}

func requestID(c *gin.Context) string {
	if value := c.GetString("request_id"); value != "" {
		return value
	}
	return c.GetHeader("X-Request-ID")
}
