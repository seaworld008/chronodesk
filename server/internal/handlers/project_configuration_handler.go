package handlers

import (
	"crypto/ed25519"
	"encoding/base64"
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

const projectConfigurationRequestBodyLimit = 4 << 20

type intakeRequestTypeResponse struct {
	ID          string                            `json:"id"`
	Version     uint64                            `json:"version"`
	Status      models.ConfigurationVersionStatus `json:"status"`
	Key         string                            `json:"key"`
	Name        string                            `json:"name"`
	Description string                            `json:"description"`
	WorkClass   models.WorkClass                  `json:"work_class"`
	JSONSchema  json.RawMessage                   `json:"json_schema"`
	UISchema    json.RawMessage                   `json:"ui_schema"`
	PublishedAt *time.Time                        `json:"published_at,omitempty"`
}

type intakeWorkflowResponse struct {
	ID          string                            `json:"id"`
	Version     uint64                            `json:"version"`
	Status      models.ConfigurationVersionStatus `json:"status"`
	Key         string                            `json:"key"`
	Name        string                            `json:"name"`
	Description string                            `json:"description"`
	States      json.RawMessage                   `json:"states"`
	Transitions json.RawMessage                   `json:"transitions"`
	PublishedAt *time.Time                        `json:"published_at,omitempty"`
}

type projectIntakeConfigurationResponse struct {
	ReleaseID      string                      `json:"release_id"`
	ReleaseVersion uint64                      `json:"release_version"`
	RequestTypes   []intakeRequestTypeResponse `json:"request_types"`
	Workflows      []intakeWorkflowResponse    `json:"workflows"`
}

// ProjectConfigurationHandler adapts project-scoped human REST requests to the
// same ProjectConfigurationService used by other protocol adapters. The
// trusted scope and Actor always come from ProjectScopeMiddleware.
type ProjectConfigurationHandler struct {
	service  *services.ProjectConfigurationService
	response *middleware.ResponseHelper
}

func NewProjectConfigurationHandler(
	service *services.ProjectConfigurationService,
) *ProjectConfigurationHandler {
	return &ProjectConfigurationHandler{
		service:  service,
		response: middleware.NewResponseHelper(),
	}
}

// RegisterRoutes mounts configuration resources below a project route group.
// The supplied group must already use ProjectScopeMiddleware.
func (handler *ProjectConfigurationHandler) RegisterRoutes(
	projectGroup *gin.RouterGroup,
) {
	configuration := projectGroup.Group("/configuration")
	configuration.POST(
		"/request-type-versions",
		handler.CreateRequestTypeDraft,
	)
	configuration.PATCH(
		"/request-type-versions/:versionID",
		handler.UpdateRequestTypeDraft,
	)
	configuration.POST(
		"/workflow-versions",
		handler.CreateWorkflowDraft,
	)
	configuration.PATCH(
		"/workflow-versions/:versionID",
		handler.UpdateWorkflowDraft,
	)
	configuration.POST(
		"/releases",
		handler.CreateConfigurationReleaseDraft,
	)
	configuration.PATCH(
		"/releases/:releaseID",
		handler.UpdateConfigurationReleaseDraft,
	)
	configuration.POST(
		"/releases/:releaseID/simulations",
		handler.SimulateConfigurationRelease,
	)
	configuration.POST(
		"/releases/:releaseID/publication",
		handler.PublishConfigurationRelease,
	)
	configuration.GET(
		"/releases/current",
		handler.CurrentConfigurationRelease,
	)
	configuration.GET(
		"/intake",
		handler.CurrentIntakeConfiguration,
	)
	configuration.POST(
		"/releases/:releaseID/rollbacks",
		handler.RollbackConfigurationRelease,
	)
	configuration.POST(
		"/solution-upgrade-previews",
		handler.PreviewSolutionUpgrade,
	)
	configuration.POST(
		"/solution-installations",
		handler.PrepareSolutionInstallation,
	)
	configuration.POST(
		"/solution-installations/:installationID/simulations",
		handler.SimulateSolutionInstallation,
	)
	configuration.POST(
		"/solution-installations/:installationID/publication",
		handler.PublishSolutionInstallation,
	)
}

type requestTypeDraftRequest struct {
	ID          string           `json:"id"`
	Key         string           `json:"key"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	WorkClass   models.WorkClass `json:"work_class"`
	JSONSchema  json.RawMessage  `json:"json_schema"`
	UISchema    json.RawMessage  `json:"ui_schema"`
}

func (handler *ProjectConfigurationHandler) CreateRequestTypeDraft(
	c *gin.Context,
) {
	if !handler.requireConfigurationManager(c) {
		return
	}
	var request requestTypeDraftRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	version, err := handler.service.CreateRequestTypeDraft(
		c.Request.Context(),
		services.RequestTypeDraftInput{
			ID:          strings.TrimSpace(request.ID),
			Key:         strings.TrimSpace(request.Key),
			Name:        strings.TrimSpace(request.Name),
			Description: strings.TrimSpace(request.Description),
			WorkClass:   request.WorkClass,
			JSONSchema:  request.JSONSchema,
			UISchema:    request.UISchema,
		},
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Created(c, version, "请求类型草稿创建成功")
}

func (handler *ProjectConfigurationHandler) UpdateRequestTypeDraft(
	c *gin.Context,
) {
	if !handler.requireConfigurationManager(c) {
		return
	}
	versionID, ok := handler.requiredPathID(c, "versionID", "请求类型版本标识无效")
	if !ok {
		return
	}
	var request requestTypeDraftRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	version, err := handler.service.UpdateRequestTypeDraft(
		c.Request.Context(),
		versionID,
		services.RequestTypeDraftInput{
			Key:         strings.TrimSpace(request.Key),
			Name:        strings.TrimSpace(request.Name),
			Description: strings.TrimSpace(request.Description),
			WorkClass:   request.WorkClass,
			JSONSchema:  request.JSONSchema,
			UISchema:    request.UISchema,
		},
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(c, version, "请求类型草稿更新成功")
}

type workflowDraftRequest struct {
	ID          string                                `json:"id"`
	Key         string                                `json:"key"`
	Name        string                                `json:"name"`
	Description string                                `json:"description"`
	States      []models.WorkflowStateDefinition      `json:"states"`
	Transitions []models.WorkflowTransitionDefinition `json:"transitions"`
}

func (handler *ProjectConfigurationHandler) CreateWorkflowDraft(
	c *gin.Context,
) {
	if !handler.requireConfigurationManager(c) {
		return
	}
	var request workflowDraftRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	version, err := handler.service.CreateWorkflowDraft(
		c.Request.Context(),
		services.WorkflowDraftInput{
			ID:          strings.TrimSpace(request.ID),
			Key:         strings.TrimSpace(request.Key),
			Name:        strings.TrimSpace(request.Name),
			Description: strings.TrimSpace(request.Description),
			States:      request.States,
			Transitions: request.Transitions,
		},
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Created(c, version, "工作流草稿创建成功")
}

func (handler *ProjectConfigurationHandler) UpdateWorkflowDraft(
	c *gin.Context,
) {
	if !handler.requireConfigurationManager(c) {
		return
	}
	versionID, ok := handler.requiredPathID(c, "versionID", "工作流版本标识无效")
	if !ok {
		return
	}
	var request workflowDraftRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	version, err := handler.service.UpdateWorkflowDraft(
		c.Request.Context(),
		versionID,
		services.WorkflowDraftInput{
			Key:         strings.TrimSpace(request.Key),
			Name:        strings.TrimSpace(request.Name),
			Description: strings.TrimSpace(request.Description),
			States:      request.States,
			Transitions: request.Transitions,
		},
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(c, version, "工作流草稿更新成功")
}

type configurationReleaseDraftRequest struct {
	Snapshot             models.ConfigurationSnapshot `json:"snapshot"`
	BaseReleaseID        *string                      `json:"base_release_id"`
	SourcePackageKey     string                       `json:"source_package_key"`
	SourcePackageVersion string                       `json:"source_package_version"`
}

func (handler *ProjectConfigurationHandler) CreateConfigurationReleaseDraft(
	c *gin.Context,
) {
	if !handler.requireConfigurationManager(c) {
		return
	}
	var request configurationReleaseDraftRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	release, err := handler.service.CreateConfigurationReleaseDraft(
		c.Request.Context(),
		services.ConfigurationReleaseDraftInput{
			Snapshot:             request.Snapshot,
			BaseReleaseID:        trimOptionalString(request.BaseReleaseID),
			SourcePackageKey:     strings.TrimSpace(request.SourcePackageKey),
			SourcePackageVersion: strings.TrimSpace(request.SourcePackageVersion),
		},
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Created(c, release, "配置发布草稿创建成功")
}

type configurationReleaseUpdateRequest struct {
	Snapshot models.ConfigurationSnapshot `json:"snapshot"`
}

func (handler *ProjectConfigurationHandler) UpdateConfigurationReleaseDraft(
	c *gin.Context,
) {
	if !handler.requireConfigurationManager(c) {
		return
	}
	releaseID, ok := handler.requiredPathID(c, "releaseID", "配置发布标识无效")
	if !ok {
		return
	}
	var request configurationReleaseUpdateRequest
	if !handler.bindJSON(c, &request) {
		return
	}
	release, err := handler.service.UpdateConfigurationReleaseDraft(
		c.Request.Context(),
		releaseID,
		request.Snapshot,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(c, release, "配置发布草稿更新成功")
}

func (handler *ProjectConfigurationHandler) SimulateConfigurationRelease(
	c *gin.Context,
) {
	if !handler.requireConfigurationManager(c) {
		return
	}
	releaseID, ok := handler.requiredPathID(c, "releaseID", "配置发布标识无效")
	if !ok {
		return
	}
	report, err := handler.service.SimulateConfigurationRelease(
		c.Request.Context(),
		releaseID,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(c, report, "配置发布模拟成功")
}

func (handler *ProjectConfigurationHandler) PublishConfigurationRelease(
	c *gin.Context,
) {
	if !handler.requireConfigurationManager(c) {
		return
	}
	releaseID, ok := handler.requiredPathID(c, "releaseID", "配置发布标识无效")
	if !ok {
		return
	}
	release, err := handler.service.ApproveConfigurationRelease(
		c.Request.Context(),
		releaseID,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(c, release, "配置发布审批成功")
}

func (handler *ProjectConfigurationHandler) CurrentConfigurationRelease(
	c *gin.Context,
) {
	if !handler.requireConfigurationManager(c) {
		return
	}
	release, err := handler.service.CurrentConfigurationRelease(
		c.Request.Context(),
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(c, release, "获取当前配置发布成功")
}

func (handler *ProjectConfigurationHandler) CurrentIntakeConfiguration(
	c *gin.Context,
) {
	if !handler.requireConfigurationReader(c) {
		return
	}
	configuration, err := handler.service.CurrentIntakeConfiguration(
		c.Request.Context(),
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(
		c,
		projectIntakeConfigurationDTO(configuration),
		"获取当前建单配置成功",
	)
}

func projectIntakeConfigurationDTO(
	configuration *services.ProjectIntakeConfiguration,
) projectIntakeConfigurationResponse {
	if configuration == nil {
		return projectIntakeConfigurationResponse{
			RequestTypes: []intakeRequestTypeResponse{},
			Workflows:    []intakeWorkflowResponse{},
		}
	}
	requestTypes := make(
		[]intakeRequestTypeResponse,
		0,
		len(configuration.RequestTypes),
	)
	for _, version := range configuration.RequestTypes {
		requestTypes = append(requestTypes, intakeRequestTypeResponse{
			ID:          version.ID,
			Version:     version.Version,
			Status:      version.Status,
			Key:         version.Key,
			Name:        version.Name,
			Description: version.Description,
			WorkClass:   version.WorkClass,
			JSONSchema:  json.RawMessage(version.JSONSchema),
			UISchema:    json.RawMessage(version.UISchema),
			PublishedAt: version.PublishedAt,
		})
	}
	workflows := make(
		[]intakeWorkflowResponse,
		0,
		len(configuration.Workflows),
	)
	for _, version := range configuration.Workflows {
		workflows = append(workflows, intakeWorkflowResponse{
			ID:          version.ID,
			Version:     version.Version,
			Status:      version.Status,
			Key:         version.Key,
			Name:        version.Name,
			Description: version.Description,
			States:      json.RawMessage(version.States),
			Transitions: json.RawMessage(version.Transitions),
			PublishedAt: version.PublishedAt,
		})
	}
	return projectIntakeConfigurationResponse{
		ReleaseID:      configuration.ReleaseID,
		ReleaseVersion: configuration.ReleaseVersion,
		RequestTypes:   requestTypes,
		Workflows:      workflows,
	}
}

func (handler *ProjectConfigurationHandler) RollbackConfigurationRelease(
	c *gin.Context,
) {
	if !handler.requireConfigurationManager(c) {
		return
	}
	releaseID, ok := handler.requiredPathID(c, "releaseID", "配置发布标识无效")
	if !ok {
		return
	}
	release, err := handler.service.RollbackConfigurationRelease(
		c.Request.Context(),
		releaseID,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Created(c, release, "配置回滚发布成功")
}

type solutionPackageRequest struct {
	Package   models.IndustrySolutionPackage `json:"package"`
	PublicKey string                         `json:"public_key"`
}

func (handler *ProjectConfigurationHandler) PreviewSolutionUpgrade(
	c *gin.Context,
) {
	if !handler.requireConfigurationManager(c) {
		return
	}
	request, publicKey, ok := handler.bindSolutionPackage(c)
	if !ok {
		return
	}
	preview, err := handler.service.PreviewSolutionUpgrade(
		c.Request.Context(),
		request.Package,
		publicKey,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(c, preview, "方案升级差异预览成功")
}

func (handler *ProjectConfigurationHandler) PrepareSolutionInstallation(
	c *gin.Context,
) {
	if !handler.requireConfigurationManager(c) {
		return
	}
	request, publicKey, ok := handler.bindSolutionPackage(c)
	if !ok {
		return
	}
	installation, err := handler.service.PrepareSolutionInstallation(
		c.Request.Context(),
		request.Package,
		publicKey,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Created(c, installation, "方案安装草稿创建成功")
}

func (handler *ProjectConfigurationHandler) SimulateSolutionInstallation(
	c *gin.Context,
) {
	if !handler.requireConfigurationManager(c) {
		return
	}
	installationID, ok := handler.requiredPathID(
		c,
		"installationID",
		"方案安装标识无效",
	)
	if !ok {
		return
	}
	report, err := handler.service.SimulateSolutionInstallation(
		c.Request.Context(),
		installationID,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(c, report, "方案安装模拟成功")
}

func (handler *ProjectConfigurationHandler) PublishSolutionInstallation(
	c *gin.Context,
) {
	if !handler.requireConfigurationManager(c) {
		return
	}
	installationID, ok := handler.requiredPathID(
		c,
		"installationID",
		"方案安装标识无效",
	)
	if !ok {
		return
	}
	installation, err := handler.service.ApproveSolutionInstallation(
		c.Request.Context(),
		installationID,
	)
	if err != nil {
		handler.writeError(c, err)
		return
	}
	handler.response.Success(c, installation, "方案安装审批成功")
}

func (handler *ProjectConfigurationHandler) requireConfigurationManager(
	c *gin.Context,
) bool {
	access, ok := handler.trustedConfigurationAccess(c)
	if !ok {
		return false
	}
	switch access.Role {
	case models.ProjectRoleAdmin, models.ProjectRoleManager:
		return true
	default:
		handler.response.Forbidden(c, "仅项目管理员或经理可管理项目配置")
		return false
	}
}

func (handler *ProjectConfigurationHandler) requireConfigurationReader(
	c *gin.Context,
) bool {
	access, ok := handler.trustedConfigurationAccess(c)
	return ok && access.Role.IsValid()
}

func (handler *ProjectConfigurationHandler) trustedConfigurationAccess(
	c *gin.Context,
) (services.ProjectAccess, bool) {
	if handler == nil || handler.service == nil {
		middleware.NewResponseHelper().InternalServerError(c, "项目配置服务不可用")
		return services.ProjectAccess{}, false
	}
	access, ok := ProjectAccessFromGin(c)
	if !ok {
		handler.response.Forbidden(c, "未解析可信项目范围")
		return services.ProjectAccess{}, false
	}
	operation, err := services.OperationContextFromContext(c.Request.Context())
	if err != nil ||
		operation.Scope != access.Scope ||
		operation.Source != services.SourceProtocolHumanREST ||
		operation.Actor != models.HumanActor(c.GetUint("user_id")) {
		handler.response.Forbidden(c, "项目操作上下文无效")
		return services.ProjectAccess{}, false
	}
	if !access.Role.IsValid() {
		handler.response.Forbidden(c, "项目成员角色无效")
		return services.ProjectAccess{}, false
	}
	return access, true
}

func (handler *ProjectConfigurationHandler) bindJSON(
	c *gin.Context,
	destination any,
) bool {
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		projectConfigurationRequestBodyLimit,
	)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		handler.response.BadRequest(c, "项目配置请求参数无效")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		handler.response.BadRequest(c, "项目配置请求只能包含一个 JSON 对象")
		return false
	}
	return true
}

func (handler *ProjectConfigurationHandler) bindSolutionPackage(
	c *gin.Context,
) (solutionPackageRequest, ed25519.PublicKey, bool) {
	var request solutionPackageRequest
	if !handler.bindJSON(c, &request) {
		return solutionPackageRequest{}, nil, false
	}
	publicKey, err := decodeEd25519PublicKey(request.PublicKey)
	if err != nil {
		handler.response.BadRequest(c, "方案签名公钥无效")
		return solutionPackageRequest{}, nil, false
	}
	return request, publicKey, true
}

func decodeEd25519PublicKey(encoded string) (ed25519.PublicKey, error) {
	encoded = strings.TrimSpace(encoded)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, errors.New("invalid ed25519 public key")
	}
	return ed25519.PublicKey(decoded), nil
}

func (handler *ProjectConfigurationHandler) requiredPathID(
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

func (handler *ProjectConfigurationHandler) writeError(
	c *gin.Context,
	err error,
) {
	switch {
	case errors.Is(err, services.ErrConfigurationNotFound):
		handler.response.NotFound(c, "项目配置资源不存在")
	case errors.Is(err, services.ErrConfigurationImmutable):
		handler.response.Error(c, http.StatusConflict, "已发布配置不可修改")
	case errors.Is(err, services.ErrConfigurationStateConflict):
		handler.response.Error(c, http.StatusConflict, "项目配置状态冲突")
	case errors.Is(err, services.ErrSolutionInstallationState):
		handler.response.Error(c, http.StatusConflict, "方案安装状态冲突")
	case errors.Is(err, models.ErrIndustrySolutionSignatureInvalid):
		handler.response.BadRequest(c, "方案包签名无效")
	default:
		handler.response.BadRequest(c, "项目配置参数或引用无效")
	}
}

func trimOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
