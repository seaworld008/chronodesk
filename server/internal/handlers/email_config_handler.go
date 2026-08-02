package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

var emailConfigValidator = validator.New()

const invalidSMTPHostMessage = "SMTP 主机必须是合法的主机名、IPv4 或未加方括号的 IPv6 地址，且不能包含协议、端口或路径"

// EmailConfigHandler 邮箱配置处理器
type EmailConfigHandler struct {
	emailConfigService services.EmailConfigServiceInterface
	response           *middleware.ResponseHelper
}

// NewEmailConfigHandler 创建邮箱配置处理器
func NewEmailConfigHandler(emailConfigService services.EmailConfigServiceInterface) *EmailConfigHandler {
	return &EmailConfigHandler{
		emailConfigService: emailConfigService,
		response:           middleware.NewResponseHelper(),
	}
}

// GetEmailConfig 获取邮箱配置
// @Summary 获取邮箱配置
// @Description 获取当前的邮箱配置信息
// @Tags 邮箱配置
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} SuccessResponse{data=models.EmailConfigResponse}
// @Failure 401 {object} ErrorResponse
// @Failure 403 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/platform/email-config [get]
func (h *EmailConfigHandler) GetEmailConfig(c *gin.Context) {
	ctx := c.Request.Context()

	// 获取邮箱配置
	config, err := h.emailConfigService.GetEmailConfig(ctx)
	if err != nil {
		logHandlerFailure(c, "email_config.get", err)
		h.response.Error(c, http.StatusInternalServerError, "get_email_config_failed", "获取邮箱配置失败")
		return
	}

	h.response.Success(c, config.ToResponse(), "邮箱配置获取成功")
}

// UpdateEmailConfig 更新邮箱配置
// @Summary 更新邮箱配置
// @Description 更新邮箱配置信息，包括SMTP设置和邮箱验证开关
// @Tags 邮箱配置
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param request body models.EmailConfigUpdateRequest true "邮箱配置更新请求"
// @Success 200 {object} SuccessResponse{data=models.EmailConfigResponse}
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 403 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/platform/email-config [put]
func (h *EmailConfigHandler) UpdateEmailConfig(c *gin.Context) {
	ctx := c.Request.Context()

	// 获取用户ID
	userID, exists := c.Get("user_id")
	if !exists {
		h.response.Error(c, http.StatusUnauthorized, "unauthorized", "用户未认证")
		return
	}

	// 解析请求体
	var req models.EmailConfigUpdateRequest
	if err := decodeStrictJSON(c, &req); err != nil {
		h.response.Error(c, http.StatusBadRequest, "invalid_request", "请求参数无效")
		return
	}
	if req.SMTPHost != nil && *req.SMTPHost != "" {
		if err := emailConfigValidator.Var(*req.SMTPHost, "hostname_rfc1123|ip"); err != nil {
			h.response.Error(
				c,
				http.StatusBadRequest,
				"invalid_smtp_host",
				invalidSMTPHostMessage,
			)
			return
		}
	}
	if req.FromEmail != nil && *req.FromEmail != "" {
		if err := emailConfigValidator.Var(*req.FromEmail, "email"); err != nil {
			h.response.Error(
				c,
				http.StatusBadRequest,
				"invalid_from_email",
				"发件人邮箱格式无效",
			)
			return
		}
	}

	// 更新邮箱配置
	config, err := h.emailConfigService.UpdateEmailConfig(ctx, &req, userID.(uint))
	if err != nil {
		logHandlerFailure(c, "email_config.update", err)
		h.response.Error(c, http.StatusInternalServerError, "update_email_config_failed", "更新邮箱配置失败")
		return
	}

	h.response.Success(c, config.ToResponse(), "邮箱配置更新成功")
}

// TestEmailConnection 测试邮件连接
// @Summary 测试邮件连接
// @Description 测试SMTP连接并发送测试邮件
// @Tags 邮箱配置
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param request body models.EmailTestRequest true "邮件测试请求"
// @Success 200 {object} SuccessResponse
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 403 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/platform/email-config/test [post]
func (h *EmailConfigHandler) TestEmailConnection(c *gin.Context) {
	ctx := c.Request.Context()

	// 解析请求体
	var req models.EmailTestRequest
	if err := decodeStrictJSON(c, &req); err != nil {
		h.response.Error(c, http.StatusBadRequest, "invalid_request", "请求参数无效")
		return
	}

	// 测试邮件连接
	err := h.emailConfigService.TestEmailConnection(ctx, &req)
	if err != nil {
		logHandlerFailure(c, "email_config.test_connection", err)
		h.response.Error(c, http.StatusInternalServerError, "test_email_failed", "邮件连接测试失败")
		return
	}

	h.response.Success(c, nil, "邮件测试成功")
}
