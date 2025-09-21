package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"gongdan-system/internal/middleware"
	"gongdan-system/internal/models"
	"gongdan-system/internal/services"
)

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
// @Router /api/admin/email-config [get]
func (h *EmailConfigHandler) GetEmailConfig(c *gin.Context) {
	ctx := context.Background()

	// 获取邮箱配置
	config, err := h.emailConfigService.GetEmailConfig(ctx)
	if err != nil {
		h.response.Error(c, http.StatusInternalServerError, "get_email_config_failed", err.Error())
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
// @Router /api/admin/email-config [put]
func (h *EmailConfigHandler) UpdateEmailConfig(c *gin.Context) {
	ctx := context.Background()

	// 获取用户ID
	userID, exists := c.Get("user_id")
	if !exists {
		h.response.Error(c, http.StatusUnauthorized, "unauthorized", "用户未认证")
		return
	}

	// 解析请求体
	var req models.EmailConfigUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.response.Error(c, http.StatusBadRequest, "invalid_request", "请求参数无效: "+err.Error())
		return
	}

	// 更新邮箱配置
	config, err := h.emailConfigService.UpdateEmailConfig(ctx, &req, userID.(uint))
	if err != nil {
		h.response.Error(c, http.StatusInternalServerError, "update_email_config_failed", err.Error())
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
// @Router /api/admin/email-config/test [post]
func (h *EmailConfigHandler) TestEmailConnection(c *gin.Context) {
	ctx := context.Background()

	// 解析请求体
	var req models.EmailTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.response.Error(c, http.StatusBadRequest, "invalid_request", "请求参数无效: "+err.Error())
		return
	}

	// 测试邮件连接
	err := h.emailConfigService.TestEmailConnection(ctx, &req)
	if err != nil {
		h.response.Error(c, http.StatusInternalServerError, "test_email_failed", err.Error())
		return
	}

	h.response.Success(c, nil, "邮件测试成功")
}

// GetEmailStatus 获取邮箱验证状态
// @Summary 获取邮箱验证状态
// @Description 获取当前邮箱验证是否启用的状态
// @Tags 邮箱配置
// @Accept json
// @Produce json
// @Success 200 {object} SuccessResponse{data=map[string]bool}
// @Failure 500 {object} ErrorResponse
// @Router /api/email-status [get]
func (h *EmailConfigHandler) GetEmailStatus(c *gin.Context) {
	ctx := context.Background()

	// 检查邮箱验证是否启用
	enabled, err := h.emailConfigService.IsEmailVerificationEnabled(ctx)
	if err != nil {
		h.response.Error(c, http.StatusInternalServerError, "get_email_status_failed", err.Error())
		return
	}

	h.response.Success(c, map[string]bool{
		"email_verification_enabled": enabled,
	}, "邮箱状态获取成功")
}
