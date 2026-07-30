package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/database"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/gorm"
)

// ProjectExternalScopeMiddleware establishes a trusted OperationContext and a
// live authorization snapshot without wrapping the downstream handler in a
// database transaction. It is only for operation-specific two-phase flows:
// the handler/service performs external I/O outside a transaction, then opens
// a second short scoped transaction to revalidate the same authorization and
// ACL/version snapshot before returning or finalizing domain state.
//
// Pure database operations continue to use ProjectScopeMiddleware so their
// authorization and domain changes commit atomically in one short transaction.
func ProjectExternalScopeMiddleware(
	service *services.ProjectService,
	db *gorm.DB,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		if service == nil || db == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"code": "project_scope_unavailable",
				"msg":  "项目上下文不可用",
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
		operationContext, err := services.WithOperationContext(
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

		var currentAccess *services.ProjectAccess
		err = database.WithProjectScopeContextTransaction(
			operationContext,
			db,
			access.Scope,
			func(scopedContext context.Context) error {
				var revalidateErr error
				currentAccess, revalidateErr =
					service.RevalidateHumanProjectAccess(
						scopedContext,
						access.Scope,
						userID,
					)
				return revalidateErr
			},
		)
		if err != nil || currentAccess == nil {
			response := middleware.NewResponseHelper()
			if err == nil {
				err = services.ErrProjectAccessDenied
			}
			writeProjectError(c, response, err)
			c.Abort()
			return
		}

		c.Set(projectAccessContextKey, *currentAccess)
		c.Set(projectRoleContextKey, string(currentAccess.Role))
		c.Request = c.Request.WithContext(operationContext)
		c.Next()
	}
}
