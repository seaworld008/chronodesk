package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

// ProjectCommandScopeMiddleware resolves a public project key and installs a
// trusted Human OperationContext without opening a database transaction.
//
// Commands whose service owns a stable exclusive lock order must use this
// boundary. The preflight ProjectAccess is suitable only for early UI/API
// rejection; the command service must revalidate the actor and all mutable
// authorization rows inside the same short transaction as the domain write.
// This prevents a generic middleware's SHARE locks from being upgraded later
// in a different order and deadlocking cross-administrator commands.
func ProjectCommandScopeMiddleware(
	service *services.ProjectService,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		if service == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"code": "project_command_scope_unavailable",
				"msg":  "项目命令上下文不可用",
			})
			return
		}
		userID := c.GetUint("user_id")
		if userID == 0 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code": "authentication_required",
				"msg":  "需要登录",
			})
			return
		}
		access, err := service.ResolveHumanProject(
			c.Request.Context(),
			c.Param("projectKey"),
			userID,
		)
		if err != nil {
			writeProjectScopeResolutionError(
				c,
				middleware.NewResponseHelper(),
				err,
			)
			c.Abort()
			return
		}
		requestContext, err := services.WithOperationContext(
			c.Request.Context(),
			services.OperationContext{
				Scope:         access.Scope,
				Actor:         models.HumanActor(userID),
				Source:        services.SourceProtocolHumanREST,
				TraceID:       middleware.TraceID(c),
				CorrelationID: middleware.CorrelationID(c),
			},
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
		c.Request = c.Request.WithContext(requestContext)
		c.Next()
	}
}
