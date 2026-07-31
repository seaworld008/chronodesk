package handlers

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

type workbenchDashboardQuery interface {
	Dashboard(
		context.Context,
		services.WorkbenchDashboardQuery,
	) (*services.WorkbenchDashboard, error)
}

type WorkbenchDashboardHandler struct {
	service  workbenchDashboardQuery
	response *middleware.ResponseHelper
}

func NewWorkbenchDashboardHandler(
	service workbenchDashboardQuery,
) *WorkbenchDashboardHandler {
	return &WorkbenchDashboardHandler{
		service:  service,
		response: middleware.NewResponseHelper(),
	}
}

// Get exposes membership-scoped operations analytics. Platform role data is
// intentionally neither read nor forwarded.
func (handler *WorkbenchDashboardHandler) Get(c *gin.Context) {
	if handler.service == nil {
		handler.response.InternalServerError(c, "运营大屏服务不可用")
		return
	}
	userID := c.GetUint("user_id")
	if userID == 0 {
		handler.response.Unauthorized(c, "登录状态无效")
		return
	}
	days, err := parseOptionalPositiveInt(c.Query("days"), 30)
	if err != nil {
		handler.response.BadRequest(c, "运营大屏查询参数无效")
		return
	}
	rawKeys, hasFilter := c.Request.URL.Query()["project_keys"]
	projectKeys := make([]models.ProjectKey, 0, len(rawKeys))
	for _, rawKey := range rawKeys {
		if rawKey == "" || rawKey != strings.TrimSpace(rawKey) {
			handler.response.BadRequest(c, "运营大屏查询参数无效")
			return
		}
		projectKeys = append(
			projectKeys,
			models.ProjectKey(rawKey),
		)
	}
	result, err := handler.service.Dashboard(
		c.Request.Context(),
		services.WorkbenchDashboardQuery{
			UserID:      userID,
			ProjectKeys: projectKeys,
			HasFilter:   hasFilter,
			Days:        days,
		},
	)
	if err != nil {
		writeCrossProjectWorkbenchError(c, handler.response, err)
		return
	}
	handler.response.Success(c, result, "获取跨项目运营大屏成功")
}
