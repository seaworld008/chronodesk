package handlers

import (
	"context"
	"net/url"
	"strconv"
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
	query, parseErr := url.ParseQuery(c.Request.URL.RawQuery)
	if parseErr != nil {
		handler.response.BadRequest(c, "运营大屏查询参数无效")
		return
	}
	for key := range query {
		if key != "project_keys" && key != "days" {
			handler.response.BadRequest(c, "运营大屏查询参数无效")
			return
		}
	}
	days := 30
	if rawDays, present := query["days"]; present {
		if len(rawDays) != 1 ||
			rawDays[0] == "" ||
			rawDays[0] != strings.TrimSpace(rawDays[0]) {
			handler.response.BadRequest(c, "运营大屏查询参数无效")
			return
		}
		parsedDays, parseErr := strconv.Atoi(rawDays[0])
		if parseErr != nil ||
			strconv.Itoa(parsedDays) != rawDays[0] ||
			(parsedDays != 7 && parsedDays != 30 && parsedDays != 90) {
			handler.response.BadRequest(c, "运营大屏查询参数无效")
			return
		}
		days = parsedDays
	}
	rawKeys, hasFilter := query["project_keys"]
	if len(rawKeys) > maxWorkbenchDashboardProjectKeys {
		handler.response.BadRequest(c, "运营大屏查询参数无效")
		return
	}
	projectKeys := make([]models.ProjectKey, 0, len(rawKeys))
	seenKeys := make(map[models.ProjectKey]struct{}, len(rawKeys))
	for _, rawKey := range rawKeys {
		if rawKey == "" || rawKey != strings.TrimSpace(rawKey) {
			handler.response.BadRequest(c, "运营大屏查询参数无效")
			return
		}
		key := models.ProjectKey(rawKey)
		if err := key.Validate(); err != nil {
			handler.response.BadRequest(c, "运营大屏查询参数无效")
			return
		}
		if _, duplicate := seenKeys[key]; duplicate {
			handler.response.BadRequest(c, "运营大屏查询参数无效")
			return
		}
		seenKeys[key] = struct{}{}
		projectKeys = append(
			projectKeys,
			key,
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

const maxWorkbenchDashboardProjectKeys = 500
