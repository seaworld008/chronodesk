package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

type crossProjectWorkbenchTicketQuery interface {
	ListTickets(
		context.Context,
		services.CrossProjectWorkbenchQuery,
	) (*services.CrossProjectWorkbenchPage, error)
}

type CrossProjectWorkbenchHandler struct {
	service  crossProjectWorkbenchTicketQuery
	response *middleware.ResponseHelper
}

func NewCrossProjectWorkbenchHandler(
	service crossProjectWorkbenchTicketQuery,
) *CrossProjectWorkbenchHandler {
	return &CrossProjectWorkbenchHandler{
		service:  service,
		response: middleware.NewResponseHelper(),
	}
}

func projectWorkbenchPageForHuman(
	page *services.CrossProjectWorkbenchPage,
) *services.CrossProjectWorkbenchPage {
	if page == nil {
		return nil
	}
	response := *page
	response.Items = make(
		[]services.CrossProjectWorkbenchTicket,
		len(page.Items),
	)
	copy(response.Items, page.Items)
	for index := range response.Items {
		if response.Items[index].ProjectRole != models.ProjectRoleObserver {
			continue
		}
		response.Items[index].CreatedByID = nil
		response.Items[index].AssignedToID = nil
		response.Items[index].AssignedToName = ""
	}
	return &response
}

// ListTickets exposes the ordinary human workbench. It intentionally does not
// inspect or forward platform role information: authorization comes only from
// active ProjectMembership rows resolved by the domain service.
func (handler *CrossProjectWorkbenchHandler) ListTickets(c *gin.Context) {
	if handler.service == nil {
		handler.response.InternalServerError(c, "跨项目工作台服务不可用")
		return
	}
	userID := c.GetUint("user_id")
	if userID == 0 {
		handler.response.Unauthorized(c, "登录状态无效")
		return
	}

	page, err := parseOptionalPositiveInt(c.Query("page"), 1)
	if err != nil {
		handler.response.BadRequest(c, "分页参数无效")
		return
	}
	pageSize, err := parseOptionalPositiveInt(c.Query("page_size"), 20)
	if err != nil {
		handler.response.BadRequest(c, "分页参数无效")
		return
	}
	result, err := handler.service.ListTickets(
		c.Request.Context(),
		services.CrossProjectWorkbenchQuery{
			UserID: userID,
			View: services.CrossProjectWorkbenchView(
				strings.TrimSpace(c.Query("view")),
			),
			Page:     page,
			PageSize: pageSize,
		},
	)
	if err != nil {
		writeCrossProjectWorkbenchError(c, handler.response, err)
		return
	}
	handler.response.Success(
		c,
		projectWorkbenchPageForHuman(result),
		"获取我的跨项目工作台成功",
	)
}

func parseOptionalPositiveInt(value string, fallback int) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil || parsed <= 0 {
		return 0, services.ErrCrossProjectWorkbenchQuery
	}
	return int(parsed), nil
}

func writeCrossProjectWorkbenchError(
	c *gin.Context,
	response *middleware.ResponseHelper,
	err error,
) {
	switch {
	case errors.Is(err, services.ErrCrossProjectWorkbenchAccessDenied):
		response.Forbidden(c, "无权访问跨项目工作台")
	case errors.Is(err, services.ErrCrossProjectWorkbenchQuery):
		response.BadRequest(c, "工作台查询参数无效")
	case errors.Is(err, services.ErrCrossProjectWorkbenchProjectLimit):
		response.Error(
			c,
			http.StatusUnprocessableEntity,
			"授权项目数量超出工作台安全上限",
		)
	default:
		response.InternalServerError(c, "获取跨项目工作台失败")
	}
}
