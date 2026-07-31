package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/database"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/gorm"
)

const (
	projectAccessContextKey = "project_access"
	projectRoleContextKey   = "project_role"
)

var errProjectRequestRollback = errors.New(
	"project request returned an unsuccessful response",
)

type ProjectHandler struct {
	service  *services.ProjectService
	response *middleware.ResponseHelper
}

func NewProjectHandler(service *services.ProjectService) *ProjectHandler {
	return &ProjectHandler{
		service:  service,
		response: middleware.NewResponseHelper(),
	}
}

func (handler *ProjectHandler) List(c *gin.Context) {
	if handler.service == nil {
		handler.response.InternalServerError(c, "项目服务不可用")
		return
	}
	userID := c.GetUint("user_id")
	projects, err := handler.service.ListHumanProjects(
		c.Request.Context(),
		userID,
	)
	if err != nil {
		writeProjectError(c, handler.response, err)
		return
	}
	handler.response.Success(c, projects, "获取授权项目成功")
}

func (handler *ProjectHandler) Current(c *gin.Context) {
	access, ok := ProjectAccessFromGin(c)
	if !ok {
		handler.response.Forbidden(c, "未解析可信项目范围")
		return
	}
	handler.response.Success(c, access, "获取项目上下文成功")
}

func (handler *ProjectHandler) ListQueues(c *gin.Context) {
	access, ok := ProjectAccessFromGin(c)
	if !ok {
		handler.response.Forbidden(c, "未解析可信项目范围")
		return
	}
	queues, err := handler.service.ListQueues(c.Request.Context(), access.Scope)
	if err != nil {
		writeProjectError(c, handler.response, err)
		return
	}
	handler.response.Success(c, queues, "获取项目队列成功")
}

func (handler *ProjectHandler) ListMemberships(c *gin.Context) {
	access, ok := ProjectAccessFromGin(c)
	if !ok {
		handler.response.Forbidden(c, "未解析可信项目范围")
		return
	}
	if access.Role != models.ProjectRoleAdmin &&
		access.Role != models.ProjectRoleManager {
		handler.response.Forbidden(c, "仅项目管理员或经理可查看项目成员")
		return
	}
	memberships, err := handler.service.ListHumanMemberships(
		c.Request.Context(),
		access.Scope,
	)
	if err != nil {
		writeProjectError(c, handler.response, err)
		return
	}
	handler.response.Success(c, memberships, "获取项目成员成功")
}

type upsertProjectMembershipRequest struct {
	UserID uint               `json:"user_id" binding:"required"`
	Role   models.ProjectRole `json:"role" binding:"required"`
}

func (handler *ProjectHandler) UpsertMembership(c *gin.Context) {
	access, ok := ProjectAccessFromGin(c)
	if !ok {
		handler.response.Forbidden(c, "未解析可信项目范围")
		return
	}
	if access.Role != models.ProjectRoleAdmin {
		handler.response.Forbidden(c, "仅项目管理员可变更项目成员")
		return
	}
	var request upsertProjectMembershipRequest
	if err := decodeStrictProjectJSON(c, &request); err != nil ||
		!request.Role.IsValid() {
		handler.response.BadRequest(c, "项目成员参数无效")
		return
	}
	membership, err := handler.service.UpsertHumanMembership(
		c.Request.Context(),
		access.Scope,
		services.UpsertProjectMembershipInput{
			UserID: request.UserID,
			Role:   request.Role,
		},
	)
	if err != nil {
		writeProjectError(c, handler.response, err)
		return
	}
	handler.response.Success(c, membership, "项目成员授权成功")
}

func (handler *ProjectHandler) DeactivateMembership(c *gin.Context) {
	access, ok := ProjectAccessFromGin(c)
	if !ok {
		handler.response.Forbidden(c, "未解析可信项目范围")
		return
	}
	if access.Role != models.ProjectRoleAdmin {
		handler.response.Forbidden(c, "仅项目管理员可撤销项目成员")
		return
	}
	userID, err := strconv.ParseUint(c.Param("userID"), 10, 32)
	if err != nil || userID == 0 {
		handler.response.BadRequest(c, "用户 ID 无效")
		return
	}
	membership, err := handler.service.DeactivateHumanMembership(
		c.Request.Context(),
		access.Scope,
		uint(userID),
	)
	if err != nil {
		writeProjectError(c, handler.response, err)
		return
	}
	handler.response.Success(c, membership, "项目成员授权已撤销")
}

type createProjectRequest struct {
	OrganizationID   uint   `json:"organization_id" binding:"required"`
	BusinessUnitID   uint   `json:"business_unit_id" binding:"required"`
	Key              string `json:"key" binding:"required"`
	Name             string `json:"name" binding:"required,max=120"`
	Description      string `json:"description" binding:"max=500"`
	DefaultQueueKey  string `json:"default_queue_key"`
	DefaultQueueName string `json:"default_queue_name"`
}

// platformProjectSummary keeps the existing command-handler helper private
// while sharing the exact closed DTO used by the platform list service.
type platformProjectSummary = services.PlatformProjectSummary

func newPlatformProjectSummary(
	project models.Project,
) platformProjectSummary {
	return platformProjectSummary{
		PublicID:    project.PublicID,
		Key:         project.Key,
		Name:        project.Name,
		Description: project.Description,
		Status:      project.Status,
	}
}

func (handler *ProjectHandler) ListPlatform(c *gin.Context) {
	if handler.service == nil {
		handler.response.InternalServerError(c, "项目服务不可用")
		return
	}
	if len(c.Request.URL.Query()) != 0 {
		handler.response.BadRequest(c, "平台项目查询参数无效")
		return
	}
	projects, err := handler.service.ListPlatformProjects(
		c.Request.Context(),
		c.GetUint("user_id"),
	)
	if err != nil {
		writeProjectError(c, handler.response, err)
		return
	}
	handler.response.Success(c, projects, "获取平台项目成功")
}

func (handler *ProjectHandler) Create(c *gin.Context) {
	var request createProjectRequest
	if err := decodeStrictProjectJSON(c, &request); err != nil ||
		models.ValidateProjectKey(request.Key) != nil {
		handler.response.BadRequest(c, "项目参数无效")
		return
	}
	project, err := handler.service.CreateProject(
		c.Request.Context(),
		services.CreateProjectInput{
			OrganizationID:   request.OrganizationID,
			BusinessUnitID:   request.BusinessUnitID,
			Key:              request.Key,
			Name:             request.Name,
			Description:      request.Description,
			AdministratorID:  c.GetUint("user_id"),
			DefaultQueueKey:  strings.TrimSpace(request.DefaultQueueKey),
			DefaultQueueName: request.DefaultQueueName,
		},
	)
	if err != nil {
		writeProjectError(c, handler.response, err)
		return
	}
	handler.response.Created(
		c,
		newPlatformProjectSummary(project.Project),
		"项目创建成功",
	)
}

func (handler *ProjectHandler) Archive(c *gin.Context) {
	projectPublicID := c.Param("projectPublicID")
	parsedPublicID, err := uuid.Parse(projectPublicID)
	if err != nil ||
		parsedPublicID.Version() != 7 ||
		parsedPublicID.Variant() != uuid.RFC4122 ||
		parsedPublicID.String() != projectPublicID {
		handler.response.BadRequest(c, "项目公共 ID 无效")
		return
	}
	if handler.service == nil {
		handler.response.InternalServerError(c, "项目服务不可用")
		return
	}
	project, err := handler.service.ArchiveProject(
		c.Request.Context(),
		projectPublicID,
		models.HumanActor(c.GetUint("user_id")),
	)
	if err != nil {
		writeProjectArchiveError(c, handler.response, err)
		return
	}
	if project == nil {
		handler.response.InternalServerError(c, "项目归档失败")
		return
	}
	handler.response.Success(
		c,
		newPlatformProjectSummary(*project),
		"项目已归档",
	)
}

func decodeStrictProjectJSON(c *gin.Context, target interface{}) error {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return errors.New("JSON request body is required")
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON request body must contain exactly one value")
		}
		return err
	}
	return binding.Validator.ValidateStruct(target)
}

func ProjectScopeMiddleware(
	service *services.ProjectService,
	db *gorm.DB,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		if service == nil || db == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"code": "project_service_unavailable",
				"msg":  "项目服务不可用",
			})
			return
		}
		userID := c.GetUint("user_id")
		access, err := service.ResolveHumanProject(
			c.Request.Context(),
			c.Param("projectKey"),
			userID,
		)
		if err != nil {
			response := middleware.NewResponseHelper()
			writeProjectScopeResolutionError(c, response, err)
			c.Abort()
			return
		}
		operation := services.OperationContext{
			Scope:         access.Scope,
			Actor:         models.HumanActor(userID),
			Source:        services.SourceProtocolHumanREST,
			TraceID:       middleware.TraceID(c),
			CorrelationID: middleware.CorrelationID(c),
		}
		requestContext, err := services.WithOperationContext(
			c.Request.Context(),
			operation,
		)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code": "invalid_project_context",
				"msg":  "项目上下文无效",
			})
			return
		}
		originalWriter := c.Writer
		defer func() {
			c.Writer = originalWriter
		}()
		bufferedWriter, err :=
			middleware.NewTransactionalResponseBuffer(originalWriter)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"code": "project_response_unavailable",
				"msg":  "项目响应事务不可用",
			})
			return
		}
		defer func() {
			if closeErr := bufferedWriter.Close(); closeErr != nil {
				_ = c.Error(closeErr)
			}
		}()
		scopedErr := database.WithProjectScopeContextTransaction(
			requestContext,
			db,
			access.Scope,
			func(scopedContext context.Context) error {
				currentAccess, revalidateErr :=
					service.RevalidateHumanProjectAccess(
						scopedContext,
						access.Scope,
						userID,
					)
				if revalidateErr != nil {
					return revalidateErr
				}
				c.Set(projectAccessContextKey, *currentAccess)
				c.Set(
					projectRoleContextKey,
					string(currentAccess.Role),
				)
				c.Request = c.Request.WithContext(scopedContext)
				c.Writer = bufferedWriter
				c.Next()
				c.Writer = originalWriter
				if err := bufferedWriter.Err(); err != nil {
					return err
				}
				if c.IsAborted() ||
					bufferedWriter.Status() >= http.StatusBadRequest {
					return errProjectRequestRollback
				}
				return nil
			},
		)
		c.Writer = originalWriter
		// Never leave a completed transaction in the context observed by
		// middleware that resumes after c.Next.
		c.Request = c.Request.WithContext(requestContext)
		if errors.Is(scopedErr, errProjectRequestRollback) {
			if err := bufferedWriter.Commit(); err != nil {
				_ = c.Error(err)
				if !c.Writer.Written() {
					c.AbortWithStatusJSON(
						http.StatusInternalServerError,
						gin.H{
							"code": "project_response_failed",
							"msg":  "项目响应失败",
						},
					)
				}
			}
			return
		}
		if errors.Is(scopedErr, services.ErrProjectAccessDenied) ||
			errors.Is(scopedErr, services.ErrProjectInactive) {
			writeProjectScopeResolutionError(
				c,
				middleware.NewResponseHelper(),
				scopedErr,
			)
			return
		}
		if scopedErr != nil {
			_ = c.Error(scopedErr)
			if !c.Writer.Written() {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"code": "project_transaction_failed",
					"msg":  "项目操作事务失败",
				})
			} else {
				c.Abort()
			}
			return
		}
		if err := bufferedWriter.Commit(); err != nil {
			_ = c.Error(err)
			if !c.Writer.Written() {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"code": "project_response_failed",
					"msg":  "项目响应失败",
				})
			} else {
				c.Abort()
			}
		}
	}
}

func ProjectAccessFromGin(c *gin.Context) (services.ProjectAccess, bool) {
	if c == nil {
		return services.ProjectAccess{}, false
	}
	access, ok := c.Get(projectAccessContextKey)
	if !ok {
		return services.ProjectAccess{}, false
	}
	resolved, ok := access.(services.ProjectAccess)
	return resolved, ok
}

// RequireProjectRoles authorizes an exact allowlist using the membership that
// ProjectScopeMiddleware resolved for this request. Project roles are not a
// hierarchy and platform duties never participate in this decision.
func RequireProjectRoles(allowedRoles ...models.ProjectRole) gin.HandlerFunc {
	allowlist := make(map[models.ProjectRole]struct{}, len(allowedRoles))
	for _, role := range allowedRoles {
		if role.IsValid() {
			allowlist[role] = struct{}{}
		}
	}
	return func(c *gin.Context) {
		access, ok := ProjectAccessFromGin(c)
		if !ok || !access.Role.IsValid() {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code": "project_access_denied",
				"msg":  "无权访问该项目",
			})
			return
		}
		if _, allowed := allowlist[access.Role]; !allowed {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code": "project_role_denied",
				"msg":  "项目权限不足",
			})
			return
		}
		c.Next()
	}
}

func writeProjectArchiveError(
	c *gin.Context,
	response *middleware.ResponseHelper,
	err error,
) {
	switch {
	case errors.Is(err, services.ErrProjectPublicID):
		response.BadRequest(c, "项目公共 ID 无效")
	case errors.Is(err, services.ErrProjectAccessDenied):
		response.Forbidden(c, "无权归档该项目")
	case errors.Is(err, services.ErrProjectNotFound):
		response.NotFound(c, "项目不存在")
	case errors.Is(err, services.ErrProjectInactive):
		response.Error(c, http.StatusConflict, "项目当前状态不允许归档")
	case errors.Is(err, services.ErrDefaultProjectArchive):
		response.Error(c, http.StatusConflict, "系统默认项目不能归档")
	default:
		response.InternalServerError(c, "项目归档失败")
	}
}

func writeProjectError(
	c *gin.Context,
	response *middleware.ResponseHelper,
	err error,
) {
	switch {
	case errors.Is(err, services.ErrProjectAccessDenied):
		response.Forbidden(c, "无权访问该项目")
	case errors.Is(err, services.ErrProjectNotFound),
		errors.Is(err, services.ErrQueueNotFound),
		errors.Is(err, services.ErrProjectMembershipNotFound),
		errors.Is(err, services.ErrProjectMembershipUser):
		response.NotFound(c, "项目、队列或成员不存在")
	case errors.Is(err, services.ErrProjectInactive):
		response.Forbidden(c, "项目已停用")
	case errors.Is(err, services.ErrLastProjectAdministrator):
		response.Error(c, http.StatusConflict, "项目必须保留至少一名有效管理员")
	default:
		response.InternalServerError(c, "项目操作失败")
	}
}

func writeProjectScopeResolutionError(
	c *gin.Context,
	response *middleware.ResponseHelper,
	err error,
) {
	switch {
	case errors.Is(err, services.ErrProjectAccessDenied),
		errors.Is(err, services.ErrProjectInactive):
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"code": "project_access_revoked",
			"msg":  "当前项目访问权限已失效",
		})
	default:
		writeProjectError(c, response, err)
	}
}
