package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

const integrationManagementRequestBodyLimit = 2 << 20

// IntegrationHandler exposes the project-scoped Integration management API.
// It accepts only transport DTOs; scope and Actor are read exclusively from
// ProjectScopeMiddleware's trusted request context.
type IntegrationHandler struct {
	service  *services.IntegrationManagementService
	response *middleware.ResponseHelper
}

func NewIntegrationHandler(
	service *services.IntegrationManagementService,
) *IntegrationHandler {
	return &IntegrationHandler{
		service:  service,
		response: middleware.NewResponseHelper(),
	}
}

// RegisterRoutes mounts below a project group that already uses
// ProjectScopeMiddleware.
func (handler *IntegrationHandler) RegisterRoutes(projectGroup *gin.RouterGroup) {
	integrations := projectGroup.Group("/integrations")

	integrations.GET("/connector-definitions", handler.ListConnectorDefinitions)
	integrations.POST("/connector-definitions", handler.CreateConnectorDefinition)
	integrations.PUT(
		"/connector-definitions/:definitionID",
		handler.UpdateConnectorDefinition,
	)

	integrations.GET("/connections", handler.ListConnections)
	integrations.POST("/connections", handler.CreateConnection)
	integrations.PUT("/connections/:connectionID", handler.UpdateConnection)
	integrations.GET(
		"/connections/:connectionID/mappings",
		handler.ListMappings,
	)
	integrations.POST(
		"/connections/:connectionID/mappings",
		handler.CreateMappingDraft,
	)

	integrations.PUT("/mappings/:mappingID", handler.UpdateMappingDraft)
	integrations.POST("/mappings/:mappingID/dry-runs", handler.DryRunMapping)
	integrations.POST("/mappings/:mappingID/publication", handler.PublishMapping)

	integrations.GET("/overview", handler.Overview)
	integrations.GET("/conflicts", handler.ListConflicts)
	integrations.POST("/conflicts/:conflictID/resolution", handler.ResolveConflict)
	integrations.GET("/dead-letters", handler.ListDeadLetters)
	integrations.POST("/dead-letters/:deadLetterID/replays", handler.ReplayDeadLetter)
}

type connectorDefinitionRequest struct {
	Key                        string                           `json:"key"`
	Name                       string                           `json:"name"`
	Description                string                           `json:"description"`
	Kind                       string                           `json:"kind"`
	Direction                  models.ConnectorDirection        `json:"direction"`
	Status                     models.ConnectorDefinitionStatus `json:"status"`
	SignatureScheme            string                           `json:"signature_scheme"`
	DefaultReplayWindowSeconds int                              `json:"default_replay_window_seconds"`
	ConfigurationSchema        json.RawMessage                  `json:"configuration_schema"`
	MappingSchema              json.RawMessage                  `json:"mapping_schema"`
}

type connectorDefinitionUpdateRequest struct {
	Name                       string                           `json:"name"`
	Description                string                           `json:"description"`
	Status                     models.ConnectorDefinitionStatus `json:"status"`
	SignatureScheme            string                           `json:"signature_scheme"`
	DefaultReplayWindowSeconds int                              `json:"default_replay_window_seconds"`
	ConfigurationSchema        json.RawMessage                  `json:"configuration_schema"`
	MappingSchema              json.RawMessage                  `json:"mapping_schema"`
	ExpectedUpdatedAt          time.Time                        `json:"expected_updated_at"`
}

func (handler *IntegrationHandler) CreateConnectorDefinition(c *gin.Context) {
	if !handler.requireIntegrationManager(c) {
		return
	}
	var request connectorDefinitionRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	definition, err := handler.service.CreateConnectorDefinition(
		c.Request.Context(),
		services.ConnectorDefinitionInput{
			Key:                        request.Key,
			Name:                       request.Name,
			Description:                request.Description,
			Kind:                       request.Kind,
			Direction:                  request.Direction,
			Status:                     request.Status,
			SignatureScheme:            request.SignatureScheme,
			DefaultReplayWindowSeconds: request.DefaultReplayWindowSeconds,
			ConfigurationSchema:        request.ConfigurationSchema,
			MappingSchema:              request.MappingSchema,
		},
	)
	if err != nil {
		handler.writeError(c, err, nil)
		return
	}
	handler.response.Created(c, connectorDefinitionViewOf(*definition), "连接器定义创建成功")
}

func (handler *IntegrationHandler) UpdateConnectorDefinition(c *gin.Context) {
	if !handler.requireIntegrationManager(c) {
		return
	}
	definitionID, ok := handler.requiredPathID(c, "definitionID")
	if !ok {
		return
	}
	var request connectorDefinitionUpdateRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	definition, err := handler.service.UpdateConnectorDefinition(
		c.Request.Context(),
		definitionID,
		services.ConnectorDefinitionUpdateInput{
			Name:                       request.Name,
			Description:                request.Description,
			Status:                     request.Status,
			SignatureScheme:            request.SignatureScheme,
			DefaultReplayWindowSeconds: request.DefaultReplayWindowSeconds,
			ConfigurationSchema:        request.ConfigurationSchema,
			MappingSchema:              request.MappingSchema,
			ExpectedUpdatedAt:          request.ExpectedUpdatedAt,
		},
	)
	if err != nil {
		handler.writeError(c, err, nil)
		return
	}
	handler.response.Success(c, connectorDefinitionViewOf(*definition), "连接器定义更新成功")
}

func (handler *IntegrationHandler) ListConnectorDefinitions(c *gin.Context) {
	if !handler.requireIntegrationReader(c) {
		return
	}
	options, ok := handler.listOptions(c)
	if !ok {
		return
	}
	page, err := handler.service.ListConnectorDefinitions(c.Request.Context(), options)
	if err != nil {
		handler.writeError(c, err, nil)
		return
	}
	items := make([]connectorDefinitionView, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, connectorDefinitionViewOf(item))
	}
	handler.response.List(
		c,
		items,
		page.Total,
		page.Page,
		page.PageSize,
		"获取连接器定义成功",
	)
}

type connectionRequest struct {
	ConnectorDefinitionID string                  `json:"connector_definition_id"`
	Key                   string                  `json:"key"`
	Name                  string                  `json:"name"`
	Description           string                  `json:"description"`
	Status                models.ConnectionStatus `json:"status"`
	Configuration         json.RawMessage         `json:"configuration"`
	VerificationKeyRef    string                  `json:"verification_key_ref"`
	ReplayWindowSeconds   int                     `json:"replay_window_seconds"`
}

type connectionUpdateRequest struct {
	Name                string                  `json:"name"`
	Description         string                  `json:"description"`
	Status              models.ConnectionStatus `json:"status"`
	Configuration       json.RawMessage         `json:"configuration"`
	VerificationKeyRef  string                  `json:"verification_key_ref"`
	ReplayWindowSeconds int                     `json:"replay_window_seconds"`
	ExpectedUpdatedAt   time.Time               `json:"expected_updated_at"`
}

func (handler *IntegrationHandler) CreateConnection(c *gin.Context) {
	if !handler.requireIntegrationManager(c) {
		return
	}
	var request connectionRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	connection, err := handler.service.CreateConnection(
		c.Request.Context(),
		services.ConnectionInput{
			ConnectorDefinitionPublicID: request.ConnectorDefinitionID,
			Key:                         request.Key,
			Name:                        request.Name,
			Description:                 request.Description,
			Status:                      request.Status,
			Configuration:               request.Configuration,
			VerificationKeyRef:          request.VerificationKeyRef,
			ReplayWindowSeconds:         request.ReplayWindowSeconds,
		},
	)
	if err != nil {
		handler.writeError(c, err, nil)
		return
	}
	handler.response.Created(c, connectionViewOf(*connection), "连接创建成功")
}

func (handler *IntegrationHandler) UpdateConnection(c *gin.Context) {
	if !handler.requireIntegrationManager(c) {
		return
	}
	connectionID, ok := handler.requiredPathID(c, "connectionID")
	if !ok {
		return
	}
	var request connectionUpdateRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	connection, err := handler.service.UpdateConnection(
		c.Request.Context(),
		connectionID,
		services.ConnectionUpdateInput{
			Name:                request.Name,
			Description:         request.Description,
			Status:              request.Status,
			Configuration:       request.Configuration,
			VerificationKeyRef:  request.VerificationKeyRef,
			ReplayWindowSeconds: request.ReplayWindowSeconds,
			ExpectedUpdatedAt:   request.ExpectedUpdatedAt,
		},
	)
	if err != nil {
		handler.writeError(c, err, nil)
		return
	}
	handler.response.Success(c, connectionViewOf(*connection), "连接更新成功")
}

func (handler *IntegrationHandler) ListConnections(c *gin.Context) {
	if !handler.requireIntegrationReader(c) {
		return
	}
	options, ok := handler.listOptions(c)
	if !ok {
		return
	}
	page, err := handler.service.ListConnections(c.Request.Context(), options)
	if err != nil {
		handler.writeError(c, err, nil)
		return
	}
	items := make([]connectionView, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, connectionViewOf(item))
	}
	handler.response.List(
		c,
		items,
		page.Total,
		page.Page,
		page.PageSize,
		"获取连接成功",
	)
}

type mappingDraftRequest struct {
	Key           string          `json:"key"`
	SourceSchema  json.RawMessage `json:"source_schema"`
	TargetCommand string          `json:"target_command"`
	Definition    json.RawMessage `json:"definition"`
}

type mappingDraftUpdateRequest struct {
	SourceSchema             json.RawMessage `json:"source_schema"`
	TargetCommand            string          `json:"target_command"`
	Definition               json.RawMessage `json:"definition"`
	ExpectedDefinitionDigest string          `json:"expected_definition_digest"`
	ExpectedUpdatedAt        time.Time       `json:"expected_updated_at"`
}

type mappingPublishRequest struct {
	ExpectedDefinitionDigest string    `json:"expected_definition_digest"`
	ExpectedUpdatedAt        time.Time `json:"expected_updated_at"`
}

type mappingDryRunRequest struct {
	Payload json.RawMessage `json:"payload"`
}

func (handler *IntegrationHandler) CreateMappingDraft(c *gin.Context) {
	if !handler.requireIntegrationManager(c) {
		return
	}
	connectionID, ok := handler.requiredPathID(c, "connectionID")
	if !ok {
		return
	}
	var request mappingDraftRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	mapping, err := handler.service.CreateMappingDraft(
		c.Request.Context(),
		services.MappingDraftInput{
			ConnectionPublicID: connectionID,
			Key:                request.Key,
			SourceSchema:       request.SourceSchema,
			TargetCommand:      request.TargetCommand,
			Definition:         request.Definition,
		},
	)
	if err != nil {
		handler.writeError(c, err, nil)
		return
	}
	handler.response.Created(c, mappingViewOf(*mapping), "映射草稿创建成功")
}

func (handler *IntegrationHandler) UpdateMappingDraft(c *gin.Context) {
	if !handler.requireIntegrationManager(c) {
		return
	}
	mappingID, ok := handler.requiredPathID(c, "mappingID")
	if !ok {
		return
	}
	var request mappingDraftUpdateRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	mapping, err := handler.service.UpdateMappingDraft(
		c.Request.Context(),
		mappingID,
		services.MappingDraftUpdateInput{
			SourceSchema:             request.SourceSchema,
			TargetCommand:            request.TargetCommand,
			Definition:               request.Definition,
			ExpectedDefinitionDigest: request.ExpectedDefinitionDigest,
			ExpectedUpdatedAt:        request.ExpectedUpdatedAt,
		},
	)
	if err != nil {
		handler.writeError(c, err, nil)
		return
	}
	handler.response.Success(c, mappingViewOf(*mapping), "映射草稿更新成功")
}

func (handler *IntegrationHandler) PublishMapping(c *gin.Context) {
	if !handler.requireIntegrationManager(c) {
		return
	}
	mappingID, ok := handler.requiredPathID(c, "mappingID")
	if !ok {
		return
	}
	var request mappingPublishRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	mapping, err := handler.service.PublishMapping(
		c.Request.Context(),
		mappingID,
		services.MappingPublishInput{
			ExpectedDefinitionDigest: request.ExpectedDefinitionDigest,
			ExpectedUpdatedAt:        request.ExpectedUpdatedAt,
		},
	)
	if err != nil {
		handler.writeError(c, err, nil)
		return
	}
	handler.response.Success(c, mappingViewOf(*mapping), "映射发布成功")
}

func (handler *IntegrationHandler) DryRunMapping(c *gin.Context) {
	if !handler.requireIntegrationManager(c) {
		return
	}
	mappingID, ok := handler.requiredPathID(c, "mappingID")
	if !ok {
		return
	}
	var request mappingDryRunRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	result, err := handler.service.DryRunMapping(
		c.Request.Context(),
		mappingID,
		request.Payload,
	)
	if err != nil {
		handler.writeError(c, err, nil)
		return
	}
	handler.response.Success(c, result, "映射试运行成功")
}

func (handler *IntegrationHandler) ListMappings(c *gin.Context) {
	if !handler.requireIntegrationReader(c) {
		return
	}
	connectionID, ok := handler.requiredPathID(c, "connectionID")
	if !ok {
		return
	}
	options, ok := handler.listOptions(c)
	if !ok {
		return
	}
	page, err := handler.service.ListMappings(
		c.Request.Context(),
		connectionID,
		options,
	)
	if err != nil {
		handler.writeError(c, err, nil)
		return
	}
	items := make([]mappingView, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, mappingViewOf(item))
	}
	handler.response.List(
		c,
		items,
		page.Total,
		page.Page,
		page.PageSize,
		"获取映射版本成功",
	)
}

func (handler *IntegrationHandler) Overview(c *gin.Context) {
	if !handler.requireIntegrationReader(c) {
		return
	}
	overview, err := handler.service.Overview(c.Request.Context())
	if err != nil {
		handler.writeError(c, err, nil)
		return
	}
	handler.response.Success(c, overviewViewOf(*overview), "获取集成运行概览成功")
}

func (handler *IntegrationHandler) ListConflicts(c *gin.Context) {
	if !handler.requireIntegrationReader(c) {
		return
	}
	options, ok := handler.listOptions(c)
	if !ok {
		return
	}
	page, err := handler.service.ListConflicts(c.Request.Context(), options)
	if err != nil {
		handler.writeError(c, err, nil)
		return
	}
	items := make([]conflictView, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, conflictViewOf(item))
	}
	handler.response.List(c, items, page.Total, page.Page, page.PageSize, "获取集成冲突成功")
}

type conflictResolutionRequest struct {
	Resolution        services.IntegrationConflictResolution `json:"resolution"`
	ExpectedUpdatedAt time.Time                              `json:"expected_updated_at"`
}

func (handler *IntegrationHandler) ResolveConflict(c *gin.Context) {
	if !handler.requireIntegrationManager(c) {
		return
	}
	conflictID, ok := handler.requiredPathID(c, "conflictID")
	if !ok {
		return
	}
	var request conflictResolutionRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	conflict, err := handler.service.ResolveConflict(
		c.Request.Context(),
		conflictID,
		services.ResolveIntegrationConflictInput{
			Resolution:        request.Resolution,
			ExpectedUpdatedAt: request.ExpectedUpdatedAt,
		},
	)
	if err != nil {
		handler.writeError(c, err, nil)
		return
	}
	handler.response.Success(c, conflictViewOf(*conflict), "集成冲突处理成功")
}

func (handler *IntegrationHandler) ListDeadLetters(c *gin.Context) {
	if !handler.requireIntegrationReader(c) {
		return
	}
	options, ok := handler.listOptions(c)
	if !ok {
		return
	}
	page, err := handler.service.ListDeadLetters(c.Request.Context(), options)
	if err != nil {
		handler.writeError(c, err, nil)
		return
	}
	items := make([]deadLetterView, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, deadLetterViewOf(item))
	}
	handler.response.List(c, items, page.Total, page.Page, page.PageSize, "获取死信成功")
}

type deadLetterReplayRequest struct {
	ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
}

func (handler *IntegrationHandler) ReplayDeadLetter(c *gin.Context) {
	if !handler.requireIntegrationManager(c) {
		return
	}
	deadLetterID, ok := handler.requiredPathID(c, "deadLetterID")
	if !ok {
		return
	}
	var request deadLetterReplayRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	result, err := handler.service.ReplayDeadLetter(
		c.Request.Context(),
		deadLetterID,
		services.ReplayIntegrationDeadLetterInput{
			ExpectedUpdatedAt: request.ExpectedUpdatedAt,
		},
	)
	if err != nil {
		handler.writeError(c, err, integrationInboundResultViewOf(result))
		return
	}
	handler.response.Success(c, integrationInboundResultViewOf(result), "死信回放成功")
}

func (handler *IntegrationHandler) requireIntegrationManager(c *gin.Context) bool {
	if !handler.validateTrustedContext(c) {
		return false
	}
	access, _ := ProjectAccessFromGin(c)
	switch access.Role {
	case models.ProjectRoleAdmin, models.ProjectRoleManager:
		return true
	default:
		handler.response.Forbidden(c, "仅项目管理员或经理可管理集成")
		return false
	}
}

func (handler *IntegrationHandler) requireIntegrationReader(c *gin.Context) bool {
	if !handler.validateTrustedContext(c) {
		return false
	}
	access, _ := ProjectAccessFromGin(c)
	switch access.Role {
	case models.ProjectRoleAdmin,
		models.ProjectRoleManager,
		models.ProjectRoleObserver:
		return true
	default:
		handler.response.Forbidden(c, "无权查看集成管理信息")
		return false
	}
}

func (handler *IntegrationHandler) validateTrustedContext(c *gin.Context) bool {
	if handler == nil || handler.service == nil {
		middleware.NewResponseHelper().InternalServerError(c, "集成管理服务不可用")
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
		handler.response.Forbidden(c, "项目操作上下文无效")
		return false
	}
	return true
}

func (handler *IntegrationHandler) bindJSON(c *gin.Context, destination any) bool {
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		integrationManagementRequestBodyLimit,
	)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		handler.response.BadRequest(c, "集成管理请求参数无效")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		handler.response.BadRequest(c, "集成管理请求只能包含一个 JSON 对象")
		return false
	}
	return true
}

func (handler *IntegrationHandler) requiredPathID(
	c *gin.Context,
	name string,
) (string, bool) {
	value := strings.TrimSpace(c.Param(name))
	if value == "" || len(value) > 64 {
		handler.response.BadRequest(c, "集成资源标识无效")
		return "", false
	}
	return value, true
}

func (handler *IntegrationHandler) listOptions(
	c *gin.Context,
) (services.IntegrationListOptions, bool) {
	options := services.IntegrationListOptions{Page: 1, PageSize: 20}
	allowed := map[string]struct{}{"page": {}, "pageSize": {}}
	for name, values := range c.Request.URL.Query() {
		if _, ok := allowed[name]; !ok || len(values) != 1 {
			handler.response.BadRequest(c, "分页参数无效")
			return services.IntegrationListOptions{}, false
		}
	}
	for name, destination := range map[string]*int{
		"page":     &options.Page,
		"pageSize": &options.PageSize,
	} {
		raw := strings.TrimSpace(c.Query(name))
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			handler.response.BadRequest(c, "分页参数无效")
			return services.IntegrationListOptions{}, false
		}
		*destination = value
	}
	return options, true
}

func (handler *IntegrationHandler) writeError(
	c *gin.Context,
	err error,
	data any,
) {
	switch {
	case errors.Is(err, services.ErrIntegrationManagementInvalidInput),
		errors.Is(err, services.ErrIntegrationInvalidInput),
		errors.Is(err, services.ErrIntegrationTargetCommandDenied):
		handler.response.BadRequest(c, "集成管理参数无效")
	case errors.Is(err, services.ErrIntegrationManagementNotFound),
		errors.Is(err, services.ErrIntegrationDeadLetterNotFound),
		errors.Is(err, services.ErrIntegrationProjectNotFound),
		errors.Is(err, services.ErrIntegrationConnectionNotFound),
		errors.Is(err, services.ErrIntegrationMappingNotFound):
		handler.response.NotFound(c, "集成资源不存在")
	case errors.Is(err, services.ErrIntegrationManagementConflict),
		errors.Is(err, services.ErrIntegrationManagementImmutable),
		errors.Is(err, services.ErrIntegrationDeadLetterState),
		errors.Is(err, services.ErrIntegrationMessageInProgress),
		errors.Is(err, services.ErrIntegrationProjectInactive),
		errors.Is(err, services.ErrIntegrationConnectionInactive),
		errors.Is(err, services.ErrIntegrationConnectorInactive),
		errors.Is(err, services.ErrIntegrationMappingNotPublished),
		errors.Is(err, services.ErrIntegrationConflict):
		handler.response.Error(c, http.StatusConflict, "集成资源状态冲突", data)
	case errors.Is(err, services.ErrIntegrationManagementUnavailable),
		errors.Is(err, services.ErrIntegrationDryRunUnavailable):
		handler.response.Error(c, http.StatusServiceUnavailable, "集成管理依赖暂不可用")
	case errors.Is(err, services.ErrIntegrationCommandFailed):
		handler.response.Error(c, http.StatusBadGateway, "死信回放领域命令失败", data)
	default:
		handler.response.InternalServerError(c, "集成管理操作失败")
	}
}

type connectorDefinitionView struct {
	PublicID            string                           `json:"id"`
	Key                 string                           `json:"key"`
	Name                string                           `json:"name"`
	Description         string                           `json:"description"`
	Kind                string                           `json:"kind"`
	Direction           models.ConnectorDirection        `json:"direction"`
	Status              models.ConnectorDefinitionStatus `json:"status"`
	SignatureScheme     string                           `json:"signature_scheme"`
	DefaultReplayWindow int                              `json:"default_replay_window_seconds"`
	ConfigurationSchema json.RawMessage                  `json:"configuration_schema"`
	MappingSchema       json.RawMessage                  `json:"mapping_schema"`
	CreatedAt           time.Time                        `json:"created_at"`
	UpdatedAt           time.Time                        `json:"updated_at"`
}

func connectorDefinitionViewOf(model models.ConnectorDefinition) connectorDefinitionView {
	return connectorDefinitionView{
		PublicID:            model.PublicID,
		Key:                 model.Key,
		Name:                model.Name,
		Description:         model.Description,
		Kind:                model.Kind,
		Direction:           model.Direction,
		Status:              model.Status,
		SignatureScheme:     model.SignatureScheme,
		DefaultReplayWindow: model.DefaultReplayWindowSeconds,
		ConfigurationSchema: cloneRawJSON(model.ConfigurationSchema),
		MappingSchema:       cloneRawJSON(model.MappingSchema),
		CreatedAt:           model.CreatedAt,
		UpdatedAt:           model.UpdatedAt,
	}
}

type connectionView struct {
	PublicID            string                  `json:"id"`
	Key                 string                  `json:"key"`
	Name                string                  `json:"name"`
	Description         string                  `json:"description"`
	Status              models.ConnectionStatus `json:"status"`
	ReplayWindowSeconds int                     `json:"replay_window_seconds"`
	HasConfiguration    bool                    `json:"has_configuration"`
	HasVerificationKey  bool                    `json:"has_verification_key"`
	LastVerifiedAt      *time.Time              `json:"last_verified_at,omitempty"`
	LastErrorAt         *time.Time              `json:"last_error_at,omitempty"`
	LastErrorCode       string                  `json:"last_error_code,omitempty"`
	CreatedAt           time.Time               `json:"created_at"`
	UpdatedAt           time.Time               `json:"updated_at"`
}

func connectionViewOf(model models.Connection) connectionView {
	configuration := strings.TrimSpace(string(model.Configuration))
	return connectionView{
		PublicID:            model.PublicID,
		Key:                 model.Key,
		Name:                model.Name,
		Description:         model.Description,
		Status:              model.Status,
		ReplayWindowSeconds: model.ReplayWindowSeconds,
		HasConfiguration:    configuration != "" && configuration != "{}" && configuration != "null",
		HasVerificationKey:  strings.TrimSpace(model.VerificationKeyRef) != "",
		LastVerifiedAt:      model.LastVerifiedAt,
		LastErrorAt:         model.LastErrorAt,
		LastErrorCode:       model.LastErrorCode,
		CreatedAt:           model.CreatedAt,
		UpdatedAt:           model.UpdatedAt,
	}
}

type mappingView struct {
	PublicID      string                      `json:"id"`
	ConnectionID  uint                        `json:"connection_id"`
	Key           string                      `json:"key"`
	Version       uint                        `json:"version"`
	Status        models.MappingVersionStatus `json:"status"`
	SourceSchema  json.RawMessage             `json:"source_schema"`
	TargetCommand string                      `json:"target_command"`
	Definition    json.RawMessage             `json:"definition"`
	Digest        string                      `json:"definition_digest"`
	PublishedAt   *time.Time                  `json:"published_at,omitempty"`
	PublishedBy   string                      `json:"published_by,omitempty"`
	CreatedAt     time.Time                   `json:"created_at"`
	UpdatedAt     time.Time                   `json:"updated_at"`
}

func mappingViewOf(model models.MappingVersion) mappingView {
	return mappingView{
		PublicID:      model.PublicID,
		ConnectionID:  model.ConnectionID,
		Key:           model.Key,
		Version:       model.Version,
		Status:        model.Status,
		SourceSchema:  cloneRawJSON(model.SourceSchema),
		TargetCommand: model.TargetCommand,
		Definition:    cloneRawJSON(model.Definition),
		Digest:        model.DefinitionDigest,
		PublishedAt:   model.PublishedAt,
		PublishedBy:   model.PublishedByID,
		CreatedAt:     model.CreatedAt,
		UpdatedAt:     model.UpdatedAt,
	}
}

type syncRunView struct {
	PublicID     string               `json:"id"`
	ConnectionID uint                 `json:"connection_id"`
	RunKey       string               `json:"run_key"`
	Direction    models.SyncDirection `json:"direction"`
	Status       models.SyncRunStatus `json:"status"`
	StartedAt    *time.Time           `json:"started_at,omitempty"`
	FinishedAt   *time.Time           `json:"finished_at,omitempty"`
	Processed    int64                `json:"processed_count"`
	Succeeded    int64                `json:"succeeded_count"`
	Failed       int64                `json:"failed_count"`
	Conflicts    int64                `json:"conflict_count"`
	ErrorCode    string               `json:"error_code,omitempty"`
}

type connectionHealthView struct {
	PublicID      string                  `json:"id"`
	Key           string                  `json:"key"`
	Name          string                  `json:"name"`
	Status        models.ConnectionStatus `json:"status"`
	LastVerified  *time.Time              `json:"last_verified_at,omitempty"`
	LastErrorAt   *time.Time              `json:"last_error_at,omitempty"`
	LastErrorCode string                  `json:"last_error_code,omitempty"`
	LastRun       *syncRunView            `json:"last_run,omitempty"`
}

type overviewView struct {
	ConnectorDefinitions int64                  `json:"connector_definitions"`
	Connections          int64                  `json:"connections"`
	ActiveConnections    int64                  `json:"active_connections"`
	ErrorConnections     int64                  `json:"error_connections"`
	OpenConflicts        int64                  `json:"open_conflicts"`
	OpenDeadLetters      int64                  `json:"open_dead_letters"`
	RunningSyncRuns      int64                  `json:"running_sync_runs"`
	RecentRuns           []syncRunView          `json:"recent_runs"`
	ConnectionHealth     []connectionHealthView `json:"connection_health"`
}

func overviewViewOf(model services.IntegrationOverview) overviewView {
	view := overviewView{
		ConnectorDefinitions: model.ConnectorDefinitions,
		Connections:          model.Connections,
		ActiveConnections:    model.ActiveConnections,
		ErrorConnections:     model.ErrorConnections,
		OpenConflicts:        model.OpenConflicts,
		OpenDeadLetters:      model.OpenDeadLetters,
		RunningSyncRuns:      model.RunningSyncRuns,
		RecentRuns:           make([]syncRunView, 0, len(model.RecentRuns)),
		ConnectionHealth:     make([]connectionHealthView, 0, len(model.ConnectionHealth)),
	}
	for _, run := range model.RecentRuns {
		view.RecentRuns = append(view.RecentRuns, syncRunViewOf(run))
	}
	for _, health := range model.ConnectionHealth {
		item := connectionHealthView{
			PublicID:      health.PublicID,
			Key:           health.Key,
			Name:          health.Name,
			Status:        health.Status,
			LastVerified:  health.LastVerified,
			LastErrorAt:   health.LastErrorAt,
			LastErrorCode: health.LastErrorCode,
		}
		if health.LastRun != nil {
			run := syncRunViewOf(*health.LastRun)
			item.LastRun = &run
		}
		view.ConnectionHealth = append(view.ConnectionHealth, item)
	}
	return view
}

func syncRunViewOf(model models.SyncRun) syncRunView {
	return syncRunView{
		PublicID:     model.PublicID,
		ConnectionID: model.ConnectionID,
		RunKey:       model.RunKey,
		Direction:    model.Direction,
		Status:       model.Status,
		StartedAt:    model.StartedAt,
		FinishedAt:   model.FinishedAt,
		Processed:    model.ProcessedCount,
		Succeeded:    model.SucceededCount,
		Failed:       model.FailedCount,
		Conflicts:    model.ConflictCount,
		ErrorCode:    model.ErrorCode,
	}
}

type conflictView struct {
	PublicID                   string                           `json:"id"`
	ConnectionID               uint                             `json:"connection_id"`
	Type                       models.IntegrationConflictType   `json:"type"`
	Status                     models.IntegrationConflictStatus `json:"status"`
	ExternalResourceType       string                           `json:"external_resource_type"`
	ExternalResourceID         string                           `json:"external_resource_id"`
	ExistingInternalResourceID string                           `json:"existing_internal_resource_id,omitempty"`
	IncomingInternalResourceID string                           `json:"incoming_internal_resource_id,omitempty"`
	ResolvedAt                 *time.Time                       `json:"resolved_at,omitempty"`
	CreatedAt                  time.Time                        `json:"created_at"`
	UpdatedAt                  time.Time                        `json:"updated_at"`
}

func conflictViewOf(model models.IntegrationConflict) conflictView {
	return conflictView{
		PublicID:                   model.PublicID,
		ConnectionID:               model.ConnectionID,
		Type:                       model.Type,
		Status:                     model.Status,
		ExternalResourceType:       model.ExternalResourceType,
		ExternalResourceID:         model.ExternalResourceID,
		ExistingInternalResourceID: model.ExistingInternalResourceID,
		IncomingInternalResourceID: model.IncomingInternalResourceID,
		ResolvedAt:                 model.ResolvedAt,
		CreatedAt:                  model.CreatedAt,
		UpdatedAt:                  model.UpdatedAt,
	}
}

type deadLetterView struct {
	PublicID     string                  `json:"id"`
	ConnectionID uint                    `json:"connection_id"`
	Status       models.DeadLetterStatus `json:"status"`
	ReasonCode   string                  `json:"reason_code"`
	AttemptCount int                     `json:"attempt_count"`
	NextAttempt  *time.Time              `json:"next_attempt_at,omitempty"`
	ResolvedAt   *time.Time              `json:"resolved_at,omitempty"`
	CreatedAt    time.Time               `json:"created_at"`
	UpdatedAt    time.Time               `json:"updated_at"`
}

func deadLetterViewOf(model models.DeadLetter) deadLetterView {
	return deadLetterView{
		PublicID:     model.PublicID,
		ConnectionID: model.ConnectionID,
		Status:       model.Status,
		ReasonCode:   model.ReasonCode,
		AttemptCount: model.AttemptCount,
		NextAttempt:  model.NextAttemptAt,
		ResolvedAt:   model.ResolvedAt,
		CreatedAt:    model.CreatedAt,
		UpdatedAt:    model.UpdatedAt,
	}
}

type integrationInboundResultView struct {
	MessageID  string          `json:"message_id,omitempty"`
	Status     string          `json:"status,omitempty"`
	Receipt    any             `json:"receipt,omitempty"`
	Conflict   *conflictView   `json:"conflict,omitempty"`
	DeadLetter *deadLetterView `json:"dead_letter,omitempty"`
	Replayed   bool            `json:"replayed"`
}

func integrationInboundResultViewOf(
	result *services.IntegrationInboundResult,
) *integrationInboundResultView {
	if result == nil {
		return nil
	}
	view := &integrationInboundResultView{Replayed: result.Replayed}
	if result.Message != nil {
		view.MessageID = result.Message.PublicID
		view.Status = string(result.Message.Status)
	}
	if result.Receipt != nil {
		view.Receipt = map[string]any{
			"id":               result.Receipt.PublicID,
			"status":           result.Receipt.Status,
			"resource_type":    result.Receipt.ResourceType,
			"resource_id":      result.Receipt.ResourceID,
			"resource_version": result.Receipt.ResourceVersion,
			"event_id":         result.Receipt.EventID,
			"operation_id":     result.Receipt.OperationID,
		}
	}
	if result.Conflict != nil {
		conflict := conflictViewOf(*result.Conflict)
		view.Conflict = &conflict
	}
	if result.DeadLetter != nil {
		letter := deadLetterViewOf(*result.DeadLetter)
		view.DeadLetter = &letter
	}
	return view
}

func cloneRawJSON(value []byte) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(append([]byte(nil), value...))
}
