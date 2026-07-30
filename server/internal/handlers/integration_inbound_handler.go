package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/observability"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

const (
	IntegrationInboundTimestampHeader            = "X-ChronoDesk-Timestamp"
	IntegrationInboundSignatureHeader            = "X-ChronoDesk-Signature"
	IntegrationInboundMessageIDHeader            = "Idempotency-Key"
	IntegrationInboundExternalResourceTypeHeader = "X-ChronoDesk-External-Resource-Type"
	IntegrationInboundExternalResourceIDHeader   = "X-ChronoDesk-External-Resource-ID"

	integrationInboundBodyLimit = int64(2 << 20)
)

var (
	integrationInboundTimestampPattern = regexp.MustCompile(`^[1-9][0-9]{9,18}$`)
	integrationInboundResourcePattern  = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)
)

// IntegrationInboundReceiver is the narrow unauthenticated transport seam.
// The concrete Inbox service resolves public IDs and authenticates the
// Connection using HMAC before any durable write.
type IntegrationInboundReceiver interface {
	services.IntegrationInboundTargetResolver
	Receive(
		context.Context,
		services.IntegrationInboundInput,
	) (*services.IntegrationInboundResult, error)
}

// IntegrationInboundHandler accepts signed project-explicit inbound messages.
// It intentionally does not use browser auth or trust any scope/Actor/trace
// field supplied in the body.
type IntegrationInboundHandler struct {
	receiver IntegrationInboundReceiver
	response *middleware.ResponseHelper
}

func NewIntegrationInboundHandler(
	receiver IntegrationInboundReceiver,
) *IntegrationInboundHandler {
	return &IntegrationInboundHandler{
		receiver: receiver,
		response: middleware.NewResponseHelper(),
	}
}

// RegisterRoutes mounts the HMAC-authenticated public endpoint. The caller
// should pass the root engine/group; this route must not inherit human session
// or Agent bearer-token middleware.
func (handler *IntegrationInboundHandler) RegisterRoutes(router gin.IRoutes) {
	router.POST(
		"/api/v2/projects/:projectKey/integrations/inbound/"+
			":connectionID/mappings/:mappingID/messages",
		handler.Receive,
	)
}

func (handler *IntegrationInboundHandler) Receive(c *gin.Context) {
	if handler == nil || handler.receiver == nil {
		middleware.NewResponseHelper().InternalServerError(c, "集成入站服务不可用")
		return
	}
	projectKey := c.Param("projectKey")
	connectionID := c.Param("connectionID")
	mappingID := c.Param("mappingID")
	if models.ValidateProjectKey(projectKey) != nil ||
		!canonicalInboundUUID(connectionID) ||
		!canonicalInboundUUID(mappingID) {
		handler.response.BadRequest(c, "集成入站路径无效")
		return
	}
	contentType, ok := handler.contentType(c)
	if !ok {
		return
	}
	messageID, ok := handler.requiredHeader(
		c,
		IntegrationInboundMessageIDHeader,
		191,
	)
	if !ok {
		return
	}
	resourceType, ok := handler.requiredHeader(
		c,
		IntegrationInboundExternalResourceTypeHeader,
		64,
	)
	if !ok {
		return
	}
	if !integrationInboundResourcePattern.MatchString(resourceType) {
		handler.response.BadRequest(c, "外部资源类型无效")
		return
	}
	resourceID, ok := handler.requiredHeader(
		c,
		IntegrationInboundExternalResourceIDHeader,
		191,
	)
	if !ok {
		return
	}
	timestampText, ok := handler.requiredHeader(
		c,
		IntegrationInboundTimestampHeader,
		19,
	)
	if !ok {
		return
	}
	if !integrationInboundTimestampPattern.MatchString(timestampText) {
		handler.response.BadRequest(c, "签名时间戳无效")
		return
	}
	timestamp, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil {
		handler.response.BadRequest(c, "签名时间戳无效")
		return
	}
	signatureValues := c.Request.Header.Values(
		IntegrationInboundSignatureHeader,
	)
	if len(signatureValues) != 1 ||
		len(signatureValues[0]) != len("v1=")+64 ||
		strings.TrimSpace(signatureValues[0]) != signatureValues[0] ||
		strings.ContainsFunc(signatureValues[0], unicode.IsControl) {
		handler.response.Unauthorized(c, "集成入站连接验证失败")
		return
	}
	signature := signatureValues[0]
	body, ok := handler.readBody(c)
	if !ok {
		return
	}
	if !json.Valid(body) {
		handler.response.BadRequest(c, "集成入站消息必须是有效 JSON")
		return
	}
	target, err := handler.receiver.ResolvePublicInboundTarget(
		c.Request.Context(),
		projectKey,
		connectionID,
		mappingID,
	)
	if err != nil {
		// Public callers cannot distinguish an unknown project, a cross-project
		// UUID, an inactive Connection or a missing Mapping.
		handler.response.Unauthorized(c, "集成入站连接验证失败")
		return
	}
	traceID := middleware.TraceID(c)
	correlationID := middleware.CorrelationID(c)
	if correlationID == "" {
		correlationID, err = observability.NewCorrelationID()
		if err != nil {
			handler.response.InternalServerError(c, "请求标识生成失败")
			return
		}
		c.Header(observability.CorrelationIDHeader, correlationID)
	}
	result, receiveErr := handler.receiver.Receive(
		c.Request.Context(),
		services.IntegrationInboundInput{
			Scope:                target.Scope,
			ConnectionID:         target.ConnectionID,
			MappingVersionID:     target.MappingVersionID,
			ExternalMessageID:    messageID,
			ExternalResourceType: resourceType,
			ExternalResourceID:   resourceID,
			SignedAt:             time.Unix(timestamp, 0).UTC(),
			Signature:            signature,
			ContentType:          contentType,
			Body:                 body,
			TrustedTraceID:       traceID,
			TrustedCorrelationID: correlationID,
		},
	)
	view := integrationInboundPublicViewOf(result)
	switch {
	case receiveErr == nil:
		if result != nil && result.Replayed {
			handler.response.Success(c, view, "集成消息已处理")
			return
		}
		handler.response.Created(c, view, "集成消息已处理")
	case errors.Is(receiveErr, services.ErrIntegrationConflict):
		handler.response.Error(c, http.StatusConflict, "集成消息存在待处理冲突", view)
	case errors.Is(receiveErr, services.ErrIntegrationMessageInProgress):
		handler.response.Error(c, http.StatusConflict, "集成消息正在处理", view)
	case errors.Is(receiveErr, services.ErrIntegrationCommandFailed),
		errors.Is(receiveErr, services.ErrIntegrationMessageDeadLettered):
		handler.response.Error(
			c,
			http.StatusUnprocessableEntity,
			"集成消息已进入死信队列",
			view,
		)
	case errors.Is(receiveErr, services.ErrIntegrationSignatureRejected),
		errors.Is(receiveErr, services.ErrIntegrationVerificationKeyUnavailable),
		errors.Is(receiveErr, services.ErrIntegrationReplayWindow),
		errors.Is(receiveErr, services.ErrIntegrationProjectNotFound),
		errors.Is(receiveErr, services.ErrIntegrationConnectionNotFound),
		errors.Is(receiveErr, services.ErrIntegrationConnectionInactive),
		errors.Is(receiveErr, services.ErrIntegrationConnectorInactive),
		errors.Is(receiveErr, services.ErrIntegrationMappingNotFound),
		errors.Is(receiveErr, services.ErrIntegrationMappingNotPublished):
		handler.response.Unauthorized(c, "集成入站连接验证失败")
	case errors.Is(receiveErr, services.ErrIntegrationInvalidInput):
		handler.response.BadRequest(c, "集成入站请求无效")
	default:
		// Never reflect command, payload, secret-store or database errors.
		handler.response.Error(c, http.StatusServiceUnavailable, "集成入站处理暂不可用")
	}
}

func (handler *IntegrationInboundHandler) contentType(c *gin.Context) (string, bool) {
	raw := c.GetHeader("Content-Type")
	if raw == "" || len(raw) > 128 {
		handler.response.Error(c, http.StatusUnsupportedMediaType, "不支持的集成消息类型")
		return "", false
	}
	mediaType, parameters, err := mime.ParseMediaType(raw)
	if err != nil ||
		(mediaType != "application/json" &&
			mediaType != "application/cloudevents+json") {
		handler.response.Error(c, http.StatusUnsupportedMediaType, "不支持的集成消息类型")
		return "", false
	}
	for key, value := range parameters {
		if !strings.EqualFold(key, "charset") ||
			!strings.EqualFold(strings.TrimSpace(value), "utf-8") {
			handler.response.Error(c, http.StatusUnsupportedMediaType, "不支持的集成消息类型")
			return "", false
		}
	}
	return mediaType, true
}

func (handler *IntegrationInboundHandler) requiredHeader(
	c *gin.Context,
	name string,
	maximum int,
) (string, bool) {
	values := c.Request.Header.Values(name)
	if len(values) != 1 {
		handler.response.BadRequest(c, "集成入站请求头无效")
		return "", false
	}
	value := values[0]
	if value == "" ||
		len(value) > maximum ||
		strings.TrimSpace(value) != value ||
		strings.ContainsFunc(value, unicode.IsControl) {
		handler.response.BadRequest(c, "集成入站请求头无效")
		return "", false
	}
	return value, true
}

func (handler *IntegrationInboundHandler) readBody(
	c *gin.Context,
) ([]byte, bool) {
	if c.Request.ContentLength > integrationInboundBodyLimit {
		handler.response.Error(c, http.StatusRequestEntityTooLarge, "集成消息超过大小限制")
		return nil, false
	}
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		integrationInboundBodyLimit,
	)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			handler.response.Error(c, http.StatusRequestEntityTooLarge, "集成消息超过大小限制")
		} else {
			handler.response.BadRequest(c, "集成消息读取失败")
		}
		return nil, false
	}
	if len(body) == 0 {
		handler.response.BadRequest(c, "集成消息不能为空")
		return nil, false
	}
	return body, true
}

type integrationInboundPublicView struct {
	State      string                                `json:"state"`
	Replayed   bool                                  `json:"replayed"`
	Message    *integrationInboundMessagePublicView  `json:"message,omitempty"`
	Receipt    *integrationInboundReceiptPublicView  `json:"receipt,omitempty"`
	Conflict   *integrationInboundConflictPublicView `json:"conflict,omitempty"`
	DeadLetter *integrationInboundDeadLetterView     `json:"dead_letter,omitempty"`
}

type integrationInboundMessagePublicView struct {
	ID     string                    `json:"id"`
	Status models.InboxMessageStatus `json:"status"`
}

type integrationInboundReceiptPublicView struct {
	ID              string                    `json:"id"`
	Status          models.InboxReceiptStatus `json:"status"`
	ResourceType    string                    `json:"resource_type"`
	ResourceID      string                    `json:"resource_id"`
	ResourceVersion uint64                    `json:"resource_version"`
	OperationID     string                    `json:"operation_id"`
	EventID         string                    `json:"event_id,omitempty"`
}

type integrationInboundConflictPublicView struct {
	ID     string                           `json:"id"`
	Type   models.IntegrationConflictType   `json:"type"`
	Status models.IntegrationConflictStatus `json:"status"`
}

type integrationInboundDeadLetterView struct {
	ID         string                  `json:"id"`
	Status     models.DeadLetterStatus `json:"status"`
	ReasonCode string                  `json:"reason_code"`
}

func integrationInboundPublicViewOf(
	result *services.IntegrationInboundResult,
) integrationInboundPublicView {
	view := integrationInboundPublicView{State: "rejected"}
	if result == nil {
		return view
	}
	view.Replayed = result.Replayed
	if result.Message != nil {
		view.Message = &integrationInboundMessagePublicView{
			ID:     result.Message.PublicID,
			Status: result.Message.Status,
		}
		view.State = string(result.Message.Status)
	}
	if result.Receipt != nil {
		view.Receipt = &integrationInboundReceiptPublicView{
			ID:              result.Receipt.PublicID,
			Status:          result.Receipt.Status,
			ResourceType:    result.Receipt.ResourceType,
			ResourceID:      result.Receipt.ResourceID,
			ResourceVersion: result.Receipt.ResourceVersion,
			OperationID:     result.Receipt.OperationID,
			EventID:         result.Receipt.EventID,
		}
		view.State = string(result.Receipt.Status)
	}
	if result.Conflict != nil {
		view.Conflict = &integrationInboundConflictPublicView{
			ID:     result.Conflict.PublicID,
			Type:   result.Conflict.Type,
			Status: result.Conflict.Status,
		}
		view.State = "conflict"
	}
	if result.DeadLetter != nil {
		view.DeadLetter = &integrationInboundDeadLetterView{
			ID:         result.DeadLetter.PublicID,
			Status:     result.DeadLetter.Status,
			ReasonCode: result.DeadLetter.ReasonCode,
		}
		view.State = "dead_letter"
	}
	return view
}

func canonicalInboundUUID(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}
