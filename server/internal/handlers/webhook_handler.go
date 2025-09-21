package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gongdan-system/internal/models"
	"gongdan-system/internal/services"
)

// WebhookHandler Webhook处理器
type WebhookHandler struct {
	db                  *gorm.DB
	notificationService *services.NotificationService
}

// NewWebhookHandler 创建Webhook处理器
func NewWebhookHandler(db *gorm.DB) *WebhookHandler {
	return &WebhookHandler{
		db:                  db,
		notificationService: services.NewNotificationService(db),
	}
}

// CreateWebhookRequest 创建webhook请求结构
type CreateWebhookRequest struct {
	Name            string                        `json:"name" binding:"required,max=100"`
	Description     string                        `json:"description" binding:"max=500"`
	Provider        models.WebhookProvider        `json:"provider" binding:"required"`
	WebhookURL      string                        `json:"webhook_url" binding:"required,url"`
	Secret          string                        `json:"secret"`
	AccessToken     string                        `json:"access_token"`
	EnabledEvents   []models.WebhookEventType     `json:"enabled_events"`
	MessageTemplate string                        `json:"message_template"`
	MessageFormat   string                        `json:"message_format"`
	FilterRules     json.RawMessage               `json:"filter_rules"`
	RetryCount      int                           `json:"retry_count"`
	RetryInterval   int                           `json:"retry_interval"`
	TimeoutSeconds  int                           `json:"timeout_seconds"`
	IsAsync         bool                          `json:"is_async"`
	RateLimit       int                           `json:"rate_limit"`
	RateLimitWindow int                           `json:"rate_limit_window"`
}

// UpdateWebhookRequest 更新webhook请求结构
type UpdateWebhookRequest struct {
	Name            *string                        `json:"name" binding:"omitempty,max=100"`
	Description     *string                        `json:"description" binding:"omitempty,max=500"`
	Provider        *models.WebhookProvider        `json:"provider"`
	WebhookURL      *string                        `json:"webhook_url" binding:"omitempty,url"`
	Secret          *string                        `json:"secret"`
	AccessToken     *string                        `json:"access_token"`
	EnabledEvents   *[]models.WebhookEventType     `json:"enabled_events"`
	MessageTemplate *string                        `json:"message_template"`
	MessageFormat   *string                        `json:"message_format"`
	FilterRules     *json.RawMessage               `json:"filter_rules"`
	RetryCount      *int                           `json:"retry_count"`
	RetryInterval   *int                           `json:"retry_interval"`
	TimeoutSeconds  *int                           `json:"timeout_seconds"`
	IsAsync         *bool                          `json:"is_async"`
	RateLimit       *int                           `json:"rate_limit"`
	RateLimitWindow *int                           `json:"rate_limit_window"`
	Status          *models.WebhookStatus          `json:"status"`
}

// ListWebhooksResponse 列表响应结构
type ListWebhooksResponse struct {
	Items []models.WebhookConfig `json:"items"`
	Total int64                  `json:"total"`
	Page  int                    `json:"page"`
	Size  int                    `json:"size"`
}

// CreateWebhook 创建webhook配置
// @Summary 创建webhook配置
// @Description 创建新的webhook通知配置
// @Tags webhook
// @Accept json
// @Produce json
// @Param webhook body CreateWebhookRequest true "Webhook配置"
// @Success 200 {object} models.WebhookConfig
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/webhooks [post]
// @Security BearerAuth
func (h *WebhookHandler) CreateWebhook(c *gin.Context) {
	var req CreateWebhookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "参数验证失败: " + err.Error(),
			"data": nil,
		})
		return
	}

	// 获取当前用户ID
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code": 1,
			"msg":  "用户未认证",
			"data": nil,
		})
		return
	}

	// 创建webhook配置
	webhook := models.WebhookConfig{
		Name:             req.Name,
		Description:      req.Description,
		Provider:         req.Provider,
		WebhookURL:       req.WebhookURL,
		Secret:           req.Secret,
		AccessToken:      req.AccessToken,
		EnabledEventsObj: req.EnabledEvents,
		MessageTemplate:  req.MessageTemplate,
		MessageFormat:    req.MessageFormat,
		FilterRulesObj:   req.FilterRules,
		RetryCount:       req.RetryCount,
		RetryInterval:    req.RetryInterval,
		TimeoutSeconds:   req.TimeoutSeconds,
		IsAsync:          req.IsAsync,
		RateLimit:        req.RateLimit,
		RateLimitWindow:  req.RateLimitWindow,
		Status:           models.WebhookStatusActive,
		CreatedBy:        userID.(uint),
	}

	// 设置默认值
	if webhook.RetryCount == 0 {
		webhook.RetryCount = 3
	}
	if webhook.RetryInterval == 0 {
		webhook.RetryInterval = 60
	}
	if webhook.TimeoutSeconds == 0 {
		webhook.TimeoutSeconds = 30
	}
	if webhook.RateLimit == 0 {
		webhook.RateLimit = 60
	}
	if webhook.RateLimitWindow == 0 {
		webhook.RateLimitWindow = 60
	}
	if webhook.MessageFormat == "" {
		webhook.MessageFormat = "markdown"
	}

	if err := h.db.Create(&webhook).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "创建webhook失败: " + err.Error(),
			"data": nil,
		})
		return
	}

	// 加载关联数据
	h.db.Preload("Creator").First(&webhook, webhook.ID)

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "创建成功",
		"data": webhook,
	})
}

// ListWebhooks 获取webhook列表
// @Summary 获取webhook列表
// @Description 分页获取webhook配置列表
// @Tags webhook
// @Accept json
// @Produce json
// @Param page query int false "页码" default(1)
// @Param page_size query int false "每页数量" default(10)
// @Param provider query string false "提供商过滤"
// @Param status query string false "状态过滤"
// @Success 200 {object} ListWebhooksResponse
// @Failure 500 {object} map[string]interface{}
// @Router /api/webhooks [get]
// @Security BearerAuth
func (h *WebhookHandler) ListWebhooks(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	provider := c.Query("provider")
	status := c.Query("status")

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 10
	}

	offset := (page - 1) * pageSize

	// 构建查询
	query := h.db.Model(&models.WebhookConfig{})
	
	if provider != "" {
		query = query.Where("provider = ?", provider)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}

	// 获取总数
	var total int64
	query.Count(&total)

	// 获取数据
	var webhooks []models.WebhookConfig
	if err := query.Preload("Creator").
		Order("created_at DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&webhooks).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "获取webhook列表失败: " + err.Error(),
			"data": nil,
		})
		return
	}

	response := ListWebhooksResponse{
		Items: webhooks,
		Total: total,
		Page:  page,
		Size:  pageSize,
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "获取成功",
		"data": response,
	})
}

// GetWebhook 获取webhook详情
// @Summary 获取webhook详情
// @Description 根据ID获取webhook配置详情
// @Tags webhook
// @Accept json
// @Produce json
// @Param id path int true "Webhook ID"
// @Success 200 {object} models.WebhookConfig
// @Failure 404 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/webhooks/{id} [get]
// @Security BearerAuth
func (h *WebhookHandler) GetWebhook(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "无效的ID",
			"data": nil,
		})
		return
	}

	var webhook models.WebhookConfig
	if err := h.db.Preload("Creator").Preload("Updater").
		First(&webhook, uint(id)).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 1,
				"msg":  "webhook不存在",
				"data": nil,
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 1,
				"msg":  "获取webhook失败: " + err.Error(),
				"data": nil,
			})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "获取成功",
		"data": webhook,
	})
}

// UpdateWebhook 更新webhook配置
// @Summary 更新webhook配置
// @Description 更新webhook配置信息
// @Tags webhook
// @Accept json
// @Produce json
// @Param id path int true "Webhook ID"
// @Param webhook body UpdateWebhookRequest true "更新数据"
// @Success 200 {object} models.WebhookConfig
// @Failure 400 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/webhooks/{id} [put]
// @Security BearerAuth
func (h *WebhookHandler) UpdateWebhook(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "无效的ID",
			"data": nil,
		})
		return
	}

	var req UpdateWebhookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "参数验证失败: " + err.Error(),
			"data": nil,
		})
		return
	}

	// 获取当前用户ID
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code": 1,
			"msg":  "用户未认证",
			"data": nil,
		})
		return
	}

	// 检查webhook是否存在
	var webhook models.WebhookConfig
	if err := h.db.First(&webhook, uint(id)).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 1,
				"msg":  "webhook不存在",
				"data": nil,
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 1,
				"msg":  "获取webhook失败: " + err.Error(),
				"data": nil,
			})
		}
		return
	}

	// 更新字段
	updates := map[string]interface{}{
		"updated_by": userID.(uint),
	}

	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.Provider != nil {
		updates["provider"] = *req.Provider
	}
	if req.WebhookURL != nil {
		updates["webhook_url"] = *req.WebhookURL
	}
	if req.Secret != nil {
		updates["secret"] = *req.Secret
	}
	if req.AccessToken != nil {
		updates["access_token"] = *req.AccessToken
	}
	if req.EnabledEvents != nil {
		webhook.EnabledEventsObj = *req.EnabledEvents
	}
	if req.MessageTemplate != nil {
		updates["message_template"] = *req.MessageTemplate
	}
	if req.MessageFormat != nil {
		updates["message_format"] = *req.MessageFormat
	}
	if req.FilterRules != nil {
		webhook.FilterRulesObj = *req.FilterRules
	}
	if req.RetryCount != nil {
		updates["retry_count"] = *req.RetryCount
	}
	if req.RetryInterval != nil {
		updates["retry_interval"] = *req.RetryInterval
	}
	if req.TimeoutSeconds != nil {
		updates["timeout_seconds"] = *req.TimeoutSeconds
	}
	if req.IsAsync != nil {
		updates["is_async"] = *req.IsAsync
	}
	if req.RateLimit != nil {
		updates["rate_limit"] = *req.RateLimit
	}
	if req.RateLimitWindow != nil {
		updates["rate_limit_window"] = *req.RateLimitWindow
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}

	// 执行更新
	if err := h.db.Model(&webhook).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "更新webhook失败: " + err.Error(),
			"data": nil,
		})
		return
	}

	// 重新获取更新后的数据
	h.db.Preload("Creator").Preload("Updater").First(&webhook, webhook.ID)

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "更新成功",
		"data": webhook,
	})
}

// DeleteWebhook 删除webhook配置
// @Summary 删除webhook配置
// @Description 软删除webhook配置
// @Tags webhook
// @Accept json
// @Produce json
// @Param id path int true "Webhook ID"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/webhooks/{id} [delete]
// @Security BearerAuth
func (h *WebhookHandler) DeleteWebhook(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "无效的ID",
			"data": nil,
		})
		return
	}

	// 检查webhook是否存在
	var webhook models.WebhookConfig
	if err := h.db.First(&webhook, uint(id)).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 1,
				"msg":  "webhook不存在",
				"data": nil,
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 1,
				"msg":  "获取webhook失败: " + err.Error(),
				"data": nil,
			})
		}
		return
	}

	// 软删除
	if err := h.db.Delete(&webhook).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "删除webhook失败: " + err.Error(),
			"data": nil,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "删除成功",
		"data": nil,
	})
}

// TestWebhook 测试webhook配置
// @Summary 测试webhook配置
// @Description 发送测试消息验证webhook配置
// @Tags webhook
// @Accept json
// @Produce json
// @Param id path int true "Webhook ID"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/webhooks/{id}/test [post]
// @Security BearerAuth
func (h *WebhookHandler) TestWebhook(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "无效的ID",
			"data": nil,
		})
		return
	}

	// 测试webhook
	ctx := c.Request.Context()
	if err := h.notificationService.TestWebhook(ctx, uint(id)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "测试失败: " + err.Error(),
			"data": nil,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "测试消息发送成功",
		"data": nil,
	})
}

// GetWebhookLogs 获取webhook日志
// @Summary 获取webhook日志
// @Description 分页获取webhook执行日志
// @Tags webhook
// @Accept json
// @Produce json
// @Param id path int true "Webhook ID"
// @Param page query int false "页码" default(1)
// @Param page_size query int false "每页数量" default(20)
// @Param status query string false "状态过滤"
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/webhooks/{id}/logs [get]
// @Security BearerAuth
func (h *WebhookHandler) GetWebhookLogs(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "无效的ID",
			"data": nil,
		})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	status := c.Query("status")

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	offset := (page - 1) * pageSize

	// 构建查询
	query := h.db.Model(&models.WebhookLog{}).Where("config_id = ?", uint(id))
	
	if status != "" {
		query = query.Where("status = ?", status)
	}

	// 获取总数
	var total int64
	query.Count(&total)

	// 获取数据
	var logs []models.WebhookLog
	if err := query.Order("created_at DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&logs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "获取日志失败: " + err.Error(),
			"data": nil,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "获取成功",
		"data": gin.H{
			"items": logs,
			"total": total,
			"page":  page,
			"size":  pageSize,
		},
	})
}

// GetWebhookStats 获取webhook统计
// @Summary 获取webhook统计
// @Description 获取webhook执行统计信息
// @Tags webhook
// @Accept json
// @Produce json
// @Param id path int true "Webhook ID"
// @Param days query int false "统计天数" default(7)
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/webhooks/{id}/stats [get]
// @Security BearerAuth
func (h *WebhookHandler) GetWebhookStats(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "无效的ID",
			"data": nil,
		})
		return
	}

	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	if days < 1 || days > 365 {
		days = 7
	}

	startTime := time.Now().AddDate(0, 0, -days)

	// 获取基础统计
	var stats struct {
		TotalSent    int64 `json:"total_sent"`
		TotalSuccess int64 `json:"total_success"`
		TotalFailed  int64 `json:"total_failed"`
	}

	var webhook models.WebhookConfig
	if err := h.db.Select("total_sent, total_success, total_failed").
		First(&webhook, uint(id)).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"code": 1,
			"msg":  "webhook不存在",
			"data": nil,
		})
		return
	}

	stats.TotalSent = webhook.TotalSent
	stats.TotalSuccess = webhook.TotalSuccess
	stats.TotalFailed = webhook.TotalFailed

	// 获取近期趋势数据
	var dailyStats []struct {
		Date    string `json:"date"`
		Sent    int64  `json:"sent"`
		Success int64  `json:"success"`
		Failed  int64  `json:"failed"`
	}

	rows, err := h.db.Raw(`
		SELECT 
			DATE(created_at) as date,
			COUNT(*) as sent,
			COUNT(CASE WHEN status = 'success' THEN 1 END) as success,
			COUNT(CASE WHEN status = 'failed' THEN 1 END) as failed
		FROM webhook_logs 
		WHERE config_id = ? AND created_at >= ?
		GROUP BY DATE(created_at)
		ORDER BY date
	`, uint(id), startTime).Rows()

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "获取统计数据失败: " + err.Error(),
			"data": nil,
		})
		return
	}
	defer rows.Close()

	for rows.Next() {
		var stat struct {
			Date    string `json:"date"`
			Sent    int64  `json:"sent"`
			Success int64  `json:"success"`
			Failed  int64  `json:"failed"`
		}
		h.db.ScanRows(rows, &stat)
		dailyStats = append(dailyStats, stat)
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "获取成功",
		"data": gin.H{
			"summary":     stats,
			"daily_stats": dailyStats,
			"period":      fmt.Sprintf("最近%d天", days),
		},
	})
}