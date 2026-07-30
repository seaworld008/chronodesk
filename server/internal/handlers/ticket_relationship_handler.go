package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/gorm"
)

const (
	ticketRelationshipRequestBodyLimit = 64 << 10
	ticketRelationshipMetadataLimit    = 32 << 10
)

var ticketEntityReferencePattern = regexp.MustCompile(
	`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,254}$`,
)

type ticketRelationshipOperations interface {
	AddEntityLink(
		context.Context,
		services.AddEntityLinkInput,
	) (*services.AddEntityLinkResult, error)
	AddTicketRelation(
		context.Context,
		services.AddTicketRelationInput,
	) (*services.AddTicketRelationResult, error)
	ListEntityLinks(context.Context, uint) ([]models.EntityLink, error)
	ListTicketRelations(context.Context, uint) ([]models.TicketRelation, error)
}

// TicketRelationshipHandler exposes project-scoped human APIs for immutable
// entity links and ticket relations. The project group must already use
// ProjectScopeMiddleware; request bodies never select a project or Actor.
type TicketRelationshipHandler struct {
	relationships ticketRelationshipOperations
	tickets       services.TicketServiceInterface
}

func NewTicketRelationshipHandler(
	relationships *services.TicketRelationshipService,
	tickets services.TicketServiceInterface,
) *TicketRelationshipHandler {
	return newTicketRelationshipHandler(relationships, tickets)
}

func newTicketRelationshipHandler(
	relationships ticketRelationshipOperations,
	tickets services.TicketServiceInterface,
) *TicketRelationshipHandler {
	return &TicketRelationshipHandler{
		relationships: relationships,
		tickets:       tickets,
	}
}

// RegisterRoutes mounts below /api/projects/:projectKey. There are
// deliberately no global or implicit-project aliases.
func (handler *TicketRelationshipHandler) RegisterRoutes(
	projectGroup *gin.RouterGroup,
) {
	// Gin requires sibling routes to use the same wildcard name for a shared
	// path segment. The existing human ticket adapter owns this segment as
	// ":id", so relationship routes must use the same canonical name.
	tickets := projectGroup.Group("/tickets/:id")
	tickets.GET("/entity-links", handler.ListEntityLinks)
	tickets.POST("/entity-links", handler.AddEntityLink)
	tickets.GET("/relations", handler.ListTicketRelations)
	tickets.POST("/relations", handler.AddTicketRelation)
}

type addEntityLinkRequest struct {
	ExpectedVersion uint64                 `json:"expected_version"`
	Kind            models.EntityKind      `json:"kind"`
	ReferenceID     string                 `json:"reference_id"`
	DisplayName     string                 `json:"display_name"`
	Metadata        map[string]interface{} `json:"metadata"`
}

type addTicketRelationRequest struct {
	ExpectedVersion uint64                    `json:"expected_version"`
	TargetTicketID  uint                      `json:"target_ticket_id"`
	Relation        models.TicketRelationType `json:"relation"`
	Reason          string                    `json:"reason"`
}

type entityLinkResponse struct {
	ID          string                 `json:"id"`
	CreatedAt   time.Time              `json:"created_at"`
	TicketID    uint                   `json:"ticket_id"`
	Kind        models.EntityKind      `json:"kind"`
	ReferenceID string                 `json:"reference_id"`
	DisplayName string                 `json:"display_name"`
	Metadata    map[string]interface{} `json:"metadata"`
}

type ticketRelationResponse struct {
	ID             string                    `json:"id"`
	CreatedAt      time.Time                 `json:"created_at"`
	SourceTicketID uint                      `json:"source_ticket_id"`
	TargetTicketID uint                      `json:"target_ticket_id"`
	Relation       models.TicketRelationType `json:"relation"`
	Reason         string                    `json:"reason"`
}

type addEntityLinkResponse struct {
	Link          entityLinkResponse `json:"link"`
	TicketVersion uint64             `json:"ticket_version"`
	EventID       string             `json:"event_id"`
}

type addTicketRelationResponse struct {
	Relation      ticketRelationResponse `json:"relation"`
	TicketVersion uint64                 `json:"ticket_version"`
	EventID       string                 `json:"event_id"`
}

func (handler *TicketRelationshipHandler) ListEntityLinks(c *gin.Context) {
	ticket, ok := handler.authorizedTicket(c, ticketAccessRead)
	if !ok {
		return
	}
	links, err := handler.relationships.ListEntityLinks(
		c.Request.Context(),
		ticket.ID,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	items := make([]entityLinkResponse, 0, len(links))
	for index := range links {
		item, err := entityLinkView(links[index])
		if err != nil {
			handler.writeError(c, err)
			return
		}
		items = append(items, item)
	}
	setTicketETag(c, ticket.Version)
	c.JSON(http.StatusOK, gin.H{
		"success":        true,
		"data":           items,
		"total":          len(items),
		"ticket_version": ticket.Version,
	})
}

func (handler *TicketRelationshipHandler) AddEntityLink(c *gin.Context) {
	ticket, ok := handler.authorizedTicket(c, ticketAccessUpdate)
	if !ok {
		return
	}
	var request addEntityLinkRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	if request.ExpectedVersion == 0 {
		handler.writeProblem(
			c,
			http.StatusUnprocessableEntity,
			"expected_version_required",
			"必须提供大于零的 expected_version",
			false,
		)
		return
	}
	request.ReferenceID = strings.TrimSpace(request.ReferenceID)
	request.DisplayName = strings.TrimSpace(request.DisplayName)
	if !request.Kind.IsValid() ||
		!ticketEntityReferencePattern.MatchString(request.ReferenceID) ||
		request.DisplayName == "" ||
		utf8.RuneCountInString(request.DisplayName) > 255 {
		handler.writeProblem(
			c,
			http.StatusUnprocessableEntity,
			"invalid_entity_link",
			"实体关联参数无效",
			false,
		)
		return
	}
	if request.Metadata == nil {
		request.Metadata = map[string]interface{}{}
	}
	encodedMetadata, err := json.Marshal(request.Metadata)
	if err != nil || len(encodedMetadata) > ticketRelationshipMetadataLimit {
		handler.writeProblem(
			c,
			http.StatusUnprocessableEntity,
			"invalid_entity_link",
			"实体关联元数据无效或超过大小限制",
			false,
		)
		return
	}
	result, err := handler.relationships.AddEntityLink(
		c.Request.Context(),
		services.AddEntityLinkInput{
			TicketID:        ticket.ID,
			ExpectedVersion: request.ExpectedVersion,
			Kind:            request.Kind,
			ReferenceID:     request.ReferenceID,
			DisplayName:     request.DisplayName,
			Metadata:        request.Metadata,
		},
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	if result == nil || result.Link == nil {
		handler.writeError(c, errors.New("empty entity link result"))
		return
	}
	link, err := entityLinkView(*result.Link)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	setTicketETag(c, result.TicketVersion)
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data": addEntityLinkResponse{
			Link:          link,
			TicketVersion: result.TicketVersion,
			EventID:       result.EventID,
		},
	})
}

func (handler *TicketRelationshipHandler) ListTicketRelations(
	c *gin.Context,
) {
	ticket, ok := handler.authorizedTicket(c, ticketAccessRead)
	if !ok {
		return
	}
	relations, err := handler.relationships.ListTicketRelations(
		c.Request.Context(),
		ticket.ID,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	items := make([]ticketRelationResponse, 0, len(relations))
	for index := range relations {
		items = append(items, ticketRelationView(relations[index]))
	}
	setTicketETag(c, ticket.Version)
	c.JSON(http.StatusOK, gin.H{
		"success":        true,
		"data":           items,
		"total":          len(items),
		"ticket_version": ticket.Version,
	})
}

func (handler *TicketRelationshipHandler) AddTicketRelation(
	c *gin.Context,
) {
	source, ok := handler.authorizedTicket(c, ticketAccessUpdate)
	if !ok {
		return
	}
	var request addTicketRelationRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	if request.ExpectedVersion == 0 {
		handler.writeProblem(
			c,
			http.StatusUnprocessableEntity,
			"expected_version_required",
			"必须提供大于零的 expected_version",
			false,
		)
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.TargetTicketID == 0 ||
		request.TargetTicketID == source.ID ||
		!request.Relation.IsValid() ||
		utf8.RuneCountInString(request.Reason) > 1000 {
		handler.writeProblem(
			c,
			http.StatusUnprocessableEntity,
			"invalid_ticket_relation",
			"工单关系参数无效",
			false,
		)
		return
	}
	if _, err := authorizeTicket(
		c.Request.Context(),
		c,
		handler.tickets,
		request.TargetTicketID,
		ticketAccessRead,
	); err != nil {
		handler.writeAuthorizationError(c, err)
		return
	}
	result, err := handler.relationships.AddTicketRelation(
		c.Request.Context(),
		services.AddTicketRelationInput{
			SourceTicketID:  source.ID,
			TargetTicketID:  request.TargetTicketID,
			ExpectedVersion: request.ExpectedVersion,
			Relation:        request.Relation,
			Reason:          request.Reason,
		},
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	if result == nil || result.Relation == nil {
		handler.writeError(c, errors.New("empty ticket relation result"))
		return
	}
	setTicketETag(c, result.TicketVersion)
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data": addTicketRelationResponse{
			Relation:      ticketRelationView(*result.Relation),
			TicketVersion: result.TicketVersion,
			EventID:       result.EventID,
		},
	})
}

func (handler *TicketRelationshipHandler) authorizedTicket(
	c *gin.Context,
	mode ticketAccessMode,
) (*models.Ticket, bool) {
	if !handler.requireTrustedContext(c) {
		return nil, false
	}
	rawTicketID := strings.TrimSpace(c.Param("id"))
	ticketID, err := strconv.ParseUint(rawTicketID, 10, 32)
	if err != nil || ticketID == 0 {
		handler.writeProblem(
			c,
			http.StatusBadRequest,
			"invalid_ticket_id",
			"工单 ID 无效",
			false,
		)
		return nil, false
	}
	ticket, err := authorizeTicket(
		c.Request.Context(),
		c,
		handler.tickets,
		uint(ticketID),
		mode,
	)
	if err != nil {
		handler.writeAuthorizationError(c, err)
		return nil, false
	}
	return ticket, true
}

func (handler *TicketRelationshipHandler) requireTrustedContext(
	c *gin.Context,
) bool {
	if handler == nil ||
		handler.relationships == nil ||
		handler.tickets == nil {
		handler.writeProblem(
			c,
			http.StatusServiceUnavailable,
			"ticket_relationship_service_unavailable",
			"工单关联服务不可用",
			true,
		)
		return false
	}
	access, ok := ProjectAccessFromGin(c)
	if !ok {
		handler.writeProblem(
			c,
			http.StatusForbidden,
			"ticket_relationship_access_denied",
			"未解析可信项目范围",
			false,
		)
		return false
	}
	userID := c.GetUint("user_id")
	operation, err := services.OperationContextFromContext(
		c.Request.Context(),
	)
	if err != nil ||
		userID == 0 ||
		operation.Scope != access.Scope ||
		operation.Actor != models.HumanActor(userID) ||
		operation.Source != services.SourceProtocolHumanREST ||
		!access.Role.IsValid() ||
		normalizedProjectRole(c) != string(access.Role) ||
		access.Project.Scope() != access.Scope ||
		string(access.Project.Key) != c.Param("projectKey") {
		handler.writeProblem(
			c,
			http.StatusForbidden,
			"ticket_relationship_access_denied",
			"工单关联操作上下文无效",
			false,
		)
		return false
	}
	return true
}

func (handler *TicketRelationshipHandler) bindJSON(
	c *gin.Context,
	destination interface{},
) bool {
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		ticketRelationshipRequestBodyLimit,
	)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			handler.writeProblem(
				c,
				http.StatusRequestEntityTooLarge,
				"ticket_relationship_request_too_large",
				"工单关联请求超过大小限制",
				false,
			)
			return false
		}
		handler.writeProblem(
			c,
			http.StatusBadRequest,
			"invalid_ticket_relationship_request",
			"工单关联请求正文必须是有效的 JSON 对象",
			false,
		)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		handler.writeProblem(
			c,
			http.StatusBadRequest,
			"invalid_ticket_relationship_request",
			"工单关联请求只能包含一个 JSON 对象",
			false,
		)
		return false
	}
	return true
}

func (handler *TicketRelationshipHandler) writeAuthorizationError(
	c *gin.Context,
	err error,
) {
	switch {
	case errors.Is(err, errTicketAccessDenied):
		handler.writeProblem(
			c,
			http.StatusForbidden,
			"ticket_relationship_access_denied",
			"无权访问或修改该工单的关联信息",
			false,
		)
	case isTicketRelationshipNotFound(err):
		handler.writeProblem(
			c,
			http.StatusNotFound,
			"ticket_not_found",
			"工单不存在",
			false,
		)
	default:
		handler.writeError(c, err)
	}
}

func (handler *TicketRelationshipHandler) writeError(
	c *gin.Context,
	err error,
) {
	switch {
	case errors.Is(err, services.ErrVersionConflict):
		writeTicketVersionConflict(c)
	case isTicketRelationshipNotFound(err):
		handler.writeProblem(
			c,
			http.StatusNotFound,
			"ticket_relationship_not_found",
			"工单关联资源不存在",
			false,
		)
	case isTicketRelationshipConflict(err):
		handler.writeProblem(
			c,
			http.StatusConflict,
			"ticket_relationship_conflict",
			"相同的工单关联已存在",
			false,
		)
	case isTicketRelationshipValidationError(err):
		handler.writeProblem(
			c,
			http.StatusUnprocessableEntity,
			"invalid_ticket_relationship",
			"工单关联参数无效",
			false,
		)
	default:
		logHandlerFailure(c, "ticket_relationship.operation", err)
		handler.writeProblem(
			c,
			http.StatusInternalServerError,
			"ticket_relationship_internal_error",
			"工单关联操作失败，请稍后重试",
			true,
		)
	}
}

func (handler *TicketRelationshipHandler) writeProblem(
	c *gin.Context,
	status int,
	code string,
	detail string,
	retryable bool,
) {
	writeHumanTicketProblem(
		c,
		status,
		code,
		"工单关联操作失败",
		detail,
		retryable,
	)
}

func entityLinkView(link models.EntityLink) (entityLinkResponse, error) {
	metadata := make(map[string]interface{})
	if len(link.Metadata) > 0 {
		if err := json.Unmarshal(link.Metadata, &metadata); err != nil {
			return entityLinkResponse{}, errors.New(
				"entity link metadata is not valid JSON",
			)
		}
	}
	return entityLinkResponse{
		ID:          link.ID,
		CreatedAt:   link.CreatedAt,
		TicketID:    link.TicketID,
		Kind:        link.Kind,
		ReferenceID: link.ReferenceID,
		DisplayName: link.DisplayName,
		Metadata:    metadata,
	}, nil
}

func ticketRelationView(
	relation models.TicketRelation,
) ticketRelationResponse {
	return ticketRelationResponse{
		ID:             relation.ID,
		CreatedAt:      relation.CreatedAt,
		SourceTicketID: relation.SourceTicketID,
		TargetTicketID: relation.TargetTicketID,
		Relation:       relation.Relation,
		Reason:         relation.Reason,
	}
}

func isTicketRelationshipNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, gorm.ErrRecordNotFound) ||
		err.Error() == "ticket not found"
}

func isTicketRelationshipConflict(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	return strings.Contains(err.Error(), "UNIQUE constraint") ||
		strings.Contains(err.Error(), "duplicate key")
}

func isTicketRelationshipValidationError(err error) bool {
	if err == nil {
		return false
	}
	for _, marker := range []string{
		"complete entity link input",
		"entity link metadata is invalid",
		"invalid entity kind",
		"entity reference and display name",
		"complete ticket relation input",
		"ticket cannot relate to itself",
		"invalid ticket relation",
	} {
		if strings.Contains(err.Error(), marker) {
			return true
		}
	}
	return false
}
