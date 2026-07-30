package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
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
		isPlatformAdministrator(c),
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
	if err := c.ShouldBindJSON(&request); err != nil ||
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

func (handler *ProjectHandler) Create(c *gin.Context) {
	if !isPlatformAdministrator(c) {
		handler.response.Forbidden(c, "仅平台管理员可创建项目")
		return
	}
	var request createProjectRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		handler.response.BadRequest(c, "项目参数无效")
		return
	}
	project, err := handler.service.CreateProject(
		c.Request.Context(),
		services.CreateProjectInput{
			OrganizationID:   request.OrganizationID,
			BusinessUnitID:   request.BusinessUnitID,
			Key:              strings.TrimSpace(request.Key),
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
	handler.response.Created(c, project, "项目创建成功")
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
			isPlatformAdministrator(c),
		)
		if err != nil {
			response := middleware.NewResponseHelper()
			writeProjectError(c, response, err)
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
		c.Set(projectAccessContextKey, *access)
		c.Set(projectRoleContextKey, string(access.Role))
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

func isPlatformAdministrator(c *gin.Context) bool {
	return strings.EqualFold(
		strings.TrimSpace(c.GetString("user_role")),
		string(models.RoleAdmin),
	)
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
