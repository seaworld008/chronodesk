package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"github.com/seaworld008/chronodesk/server/internal/security"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/gorm"
)

// WebhookHandler Webhook处理器
type WebhookHandler struct {
	db                  *gorm.DB
	notificationService *services.NotificationService
	secretStore         security.Protector
}

// NewWebhookHandlerWithProtector injects the application data-encryption
// keyring used by both configuration writes and webhook deliveries.
func NewWebhookHandlerWithProtector(
	db *gorm.DB,
	protector security.Protector,
) *WebhookHandler {
	return &WebhookHandler{
		db:                  db,
		notificationService: services.NewNotificationServiceWithProtector(db, protector),
		secretStore:         protector,
	}
}

// CreateWebhookRequest 创建webhook请求结构
type CreateWebhookRequest struct {
	Name            string                     `json:"name" binding:"required,max=100"`
	Description     string                     `json:"description" binding:"max=500"`
	Provider        models.WebhookProvider     `json:"provider" binding:"required"`
	WebhookURL      string                     `json:"webhook_url" binding:"required,url"`
	Secret          string                     `json:"secret"`
	AccessToken     string                     `json:"access_token"`
	EnabledEvents   []models.WebhookEventType  `json:"enabled_events"`
	MessageTemplate string                     `json:"message_template"`
	MessageFormat   string                     `json:"message_format"`
	FilterRules     *models.WebhookFilterRules `json:"filter_rules"`
	RetryCount      int                        `json:"retry_count"`
	RetryInterval   int                        `json:"retry_interval"`
	TimeoutSeconds  int                        `json:"timeout_seconds"`
	IsAsync         bool                       `json:"is_async"`
	RateLimit       int                        `json:"rate_limit"`
	RateLimitWindow int                        `json:"rate_limit_window"`
}

// UpdateWebhookRequest 更新webhook请求结构
type UpdateWebhookRequest struct {
	Name                 *string                    `json:"name" binding:"omitempty,max=100"`
	Description          *string                    `json:"description" binding:"omitempty,max=500"`
	Provider             *models.WebhookProvider    `json:"provider"`
	WebhookURL           *string                    `json:"webhook_url" binding:"omitempty,url"`
	Secret               *string                    `json:"secret"`
	SecretOverlapSeconds *int                       `json:"secret_overlap_seconds"`
	AccessToken          *string                    `json:"access_token"`
	EnabledEvents        *[]models.WebhookEventType `json:"enabled_events"`
	MessageTemplate      *string                    `json:"message_template"`
	MessageFormat        *string                    `json:"message_format"`
	FilterRules          *models.WebhookFilterRules `json:"filter_rules"`
	RetryCount           *int                       `json:"retry_count"`
	RetryInterval        *int                       `json:"retry_interval"`
	TimeoutSeconds       *int                       `json:"timeout_seconds"`
	IsAsync              *bool                      `json:"is_async"`
	RateLimit            *int                       `json:"rate_limit"`
	RateLimitWindow      *int                       `json:"rate_limit_window"`
	Status               *models.WebhookStatus      `json:"status"`
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
	operation, ok := requireWebhookProjectAccess(c, true)
	if !ok {
		return
	}
	var req CreateWebhookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "参数验证失败",
			"data": nil,
		})
		return
	}

	// 获取当前用户ID
	userID := c.GetUint("user_id")

	// 创建webhook配置
	webhook := models.WebhookConfig{
		OrganizationID:  operation.Scope.OrganizationID,
		ProjectID:       operation.Scope.ProjectID,
		Name:            req.Name,
		Description:     req.Description,
		Provider:        req.Provider,
		WebhookURL:      req.WebhookURL,
		MessageTemplate: req.MessageTemplate,
		MessageFormat:   req.MessageFormat,
		RetryCount:      req.RetryCount,
		RetryInterval:   req.RetryInterval,
		TimeoutSeconds:  req.TimeoutSeconds,
		IsAsync:         req.IsAsync,
		RateLimit:       req.RateLimit,
		RateLimitWindow: req.RateLimitWindow,
		Status:          models.WebhookStatusActive,
		CreatedBy:       userID,
	}
	if err := webhook.SetSubscriptions(req.EnabledEvents, req.FilterRules, true); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "Webhook 订阅事件或状态筛选无效",
			"data": nil,
		})
		return
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
	if err := security.ValidateHTTPSCallbackURLString(webhook.WebhookURL); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "Webhook 地址必须是公网 HTTPS 地址，且不能包含用户凭据",
			"data": nil,
		})
		return
	}

	if err := scopeddb.TransactionForContext(
		c.Request.Context(),
		h.db,
		func(tx *gorm.DB) error {
			if err := tx.Create(&webhook).Error; err != nil {
				return err
			}
			secret, err := h.protectWebhookSecret(webhook.ID, "secret", req.Secret)
			if err != nil {
				return err
			}
			accessToken, err := h.protectWebhookSecret(webhook.ID, "access_token", req.AccessToken)
			if err != nil {
				return err
			}
			if secret == "" && accessToken == "" {
				return nil
			}
			webhook.Secret = secret
			webhook.AccessToken = accessToken
			return tx.Model(&webhook).Updates(map[string]any{
				"secret":       secret,
				"access_token": accessToken,
			}).Error
		},
	); err != nil {
		logHandlerFailure(c, "webhook.create", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "创建webhook失败，请检查加密配置或稍后重试",
			"data": nil,
		})
		return
	}

	// 加载关联数据
	if err := h.db.WithContext(c.Request.Context()).Preload("Creator").First(&webhook, webhook.ID).Error; err != nil {
		logHandlerFailure(c, "webhook.reload_after_create", err)
	}

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
	operation, ok := requireWebhookProjectAccess(c, false)
	if !ok {
		return
	}
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
	query := h.db.WithContext(c.Request.Context()).
		Model(&models.WebhookConfig{}).
		Where(
			"organization_id = ? AND project_id = ?",
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
		)

	if provider != "" {
		query = query.Where("provider = ?", provider)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}

	// 获取总数
	var total int64
	if err := query.Count(&total).Error; err != nil {
		logHandlerFailure(c, "webhook.count", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "获取webhook列表失败",
			"data": nil,
		})
		return
	}

	// 获取数据
	var webhooks []models.WebhookConfig
	if err := query.Preload("Creator").
		Order("created_at DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&webhooks).Error; err != nil {
		logHandlerFailure(c, "webhook.list", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "获取webhook列表失败",
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
	operation, ok := requireWebhookProjectAccess(c, false)
	if !ok {
		return
	}
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
	if err := h.db.WithContext(c.Request.Context()).
		Preload("Creator").
		Preload("Updater").
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			uint(id),
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
		).
		First(&webhook).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 1,
				"msg":  "webhook不存在",
				"data": nil,
			})
		} else {
			logHandlerFailure(c, "webhook.get", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 1,
				"msg":  "获取webhook失败",
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
	operation, ok := requireWebhookProjectAccess(c, true)
	if !ok {
		return
	}
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
			"msg":  "参数验证失败",
			"data": nil,
		})
		return
	}

	// 获取当前用户ID
	userID := c.GetUint("user_id")

	// 检查webhook是否存在
	var webhook models.WebhookConfig
	if err := h.db.WithContext(c.Request.Context()).Where(
		"id = ? AND organization_id = ? AND project_id = ?",
		uint(id),
		operation.Scope.OrganizationID,
		operation.Scope.ProjectID,
	).First(&webhook).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 1,
				"msg":  "webhook不存在",
				"data": nil,
			})
		} else {
			logHandlerFailure(c, "webhook.get_for_update", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 1,
				"msg":  "获取webhook失败",
				"data": nil,
			})
		}
		return
	}

	// 更新字段
	updates := map[string]interface{}{
		"updated_by": userID,
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
		if err := security.ValidateHTTPSCallbackURLString(*req.WebhookURL); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"code": 1,
				"msg":  "Webhook 地址必须是公网 HTTPS 地址，且不能包含用户凭据",
				"data": nil,
			})
			return
		}
		updates["webhook_url"] = *req.WebhookURL
	}
	if req.Secret != nil {
		if strings.TrimSpace(*req.Secret) == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"code": 1,
				"msg":  "Webhook 签名密钥不能为空",
				"data": nil,
			})
			return
		}
		overlapSeconds := 24 * 60 * 60
		if req.SecretOverlapSeconds != nil {
			overlapSeconds = *req.SecretOverlapSeconds
		}
		if overlapSeconds < 0 || overlapSeconds > 7*24*60*60 {
			c.JSON(http.StatusBadRequest, gin.H{
				"code": 1,
				"msg":  "Webhook 密钥重叠期必须在 0 到 7 天之间",
				"data": nil,
			})
			return
		}
		previousSecret := ""
		var previousExpiresAt *time.Time
		if overlapSeconds > 0 && webhook.Secret != "" {
			plaintext, revealErr := security.RevealOptional(
				h.secretStore,
				webhook.Secret,
				security.FieldAAD(
					"webhook_configs",
					strconv.FormatUint(uint64(webhook.ID), 10),
					"secret",
				),
			)
			if revealErr != nil {
				logHandlerFailure(c, "webhook.reveal_previous_secret", revealErr)
				c.JSON(http.StatusInternalServerError, gin.H{
					"code": 1,
					"msg":  "更新webhook失败，请检查加密配置",
					"data": nil,
				})
				return
			}
			previousSecret, revealErr = h.protectWebhookSecret(
				webhook.ID,
				"previous_secret",
				plaintext,
			)
			if revealErr != nil {
				logHandlerFailure(c, "webhook.protect_previous_secret", revealErr)
				c.JSON(http.StatusInternalServerError, gin.H{
					"code": 1,
					"msg":  "更新webhook失败，请检查加密配置",
					"data": nil,
				})
				return
			}
			expiresAt := time.Now().UTC().Add(
				time.Duration(overlapSeconds) * time.Second,
			)
			previousExpiresAt = &expiresAt
		}
		secret, err := h.protectWebhookSecret(webhook.ID, "secret", *req.Secret)
		if err != nil {
			logHandlerFailure(c, "webhook.protect_secret", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 1,
				"msg":  "更新webhook失败，请检查加密配置",
				"data": nil,
			})
			return
		}
		updates["secret"] = secret
		updates["previous_secret"] = previousSecret
		updates["previous_secret_expires_at"] = previousExpiresAt
	}
	if req.AccessToken != nil {
		accessToken, err := h.protectWebhookSecret(webhook.ID, "access_token", *req.AccessToken)
		if err != nil {
			logHandlerFailure(c, "webhook.protect_access_token", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 1,
				"msg":  "更新webhook失败，请检查加密配置",
				"data": nil,
			})
			return
		}
		updates["access_token"] = accessToken
	}
	if req.MessageTemplate != nil {
		updates["message_template"] = *req.MessageTemplate
	}
	if req.MessageFormat != nil {
		updates["message_format"] = *req.MessageFormat
	}
	if req.EnabledEvents != nil || req.FilterRules != nil {
		events := webhook.EnabledEventsObj
		filters := webhook.FilterRulesObj
		if req.EnabledEvents != nil {
			events = *req.EnabledEvents
		}
		if req.FilterRules != nil {
			filters = req.FilterRules
		}
		if err := webhook.SetSubscriptions(events, filters, true); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"code": 1,
				"msg":  "Webhook 订阅事件或状态筛选无效",
				"data": nil,
			})
			return
		}
		updates["enabled_events"] = webhook.EnabledEvents
		updates["filter_rules"] = webhook.FilterRules
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
	if err := h.db.WithContext(c.Request.Context()).
		Model(&models.WebhookConfig{}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			webhook.ID,
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
		).
		Updates(updates).Error; err != nil {
		logHandlerFailure(c, "webhook.update", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "更新webhook失败",
			"data": nil,
		})
		return
	}

	// 重新获取更新后的数据
	if err := h.db.WithContext(c.Request.Context()).
		Preload("Creator").
		Preload("Updater").
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			webhook.ID,
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
		).
		First(&webhook).Error; err != nil {
		logHandlerFailure(c, "webhook.reload_after_update", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "更新成功",
		"data": webhook,
	})
}

func (h *WebhookHandler) protectWebhookSecret(
	configID uint,
	field string,
	plaintext string,
) (string, error) {
	return security.ProtectOptional(
		h.secretStore,
		plaintext,
		security.FieldAAD(
			"webhook_configs",
			strconv.FormatUint(uint64(configID), 10),
			field,
		),
	)
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
	operation, ok := requireWebhookProjectAccess(c, true)
	if !ok {
		return
	}
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
	if err := h.db.WithContext(c.Request.Context()).Where(
		"id = ? AND organization_id = ? AND project_id = ?",
		uint(id),
		operation.Scope.OrganizationID,
		operation.Scope.ProjectID,
	).First(&webhook).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 1,
				"msg":  "webhook不存在",
				"data": nil,
			})
		} else {
			logHandlerFailure(c, "webhook.get_for_delete", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 1,
				"msg":  "获取webhook失败",
				"data": nil,
			})
		}
		return
	}

	// 软删除
	if err := h.db.WithContext(c.Request.Context()).Where(
		"id = ? AND organization_id = ? AND project_id = ?",
		webhook.ID,
		operation.Scope.OrganizationID,
		operation.Scope.ProjectID,
	).Delete(&models.WebhookConfig{}).Error; err != nil {
		logHandlerFailure(c, "webhook.delete", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "删除webhook失败",
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
	operation, ok := requireWebhookProjectAccess(c, true)
	if !ok {
		return
	}
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
	if err := h.notificationService.TestWebhook(
		ctx,
		operation.Scope,
		uint(id),
	); err != nil {
		logHandlerFailure(c, "webhook.test", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "Webhook 测试失败，请检查配置和目标服务状态",
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
	operation, ok := requireWebhookProjectAccess(c, false)
	if !ok {
		return
	}
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
	query := h.db.WithContext(c.Request.Context()).
		Model(&models.WebhookLog{}).
		Where(
			"organization_id = ? AND project_id = ? AND config_id = ?",
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
			uint(id),
		)

	if status != "" {
		query = query.Where("status = ?", status)
	}

	// 获取总数
	var total int64
	if err := query.Count(&total).Error; err != nil {
		logHandlerFailure(c, "webhook.count_logs", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "获取日志失败",
			"data": nil,
		})
		return
	}

	// 获取数据
	var logs []models.WebhookLog
	if err := query.Order("created_at DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&logs).Error; err != nil {
		logHandlerFailure(c, "webhook.list_logs", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "获取日志失败",
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
	operation, ok := requireWebhookProjectAccess(c, false)
	if !ok {
		return
	}
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
	if err := h.db.WithContext(c.Request.Context()).
		Select("total_sent, total_success, total_failed").
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			uint(id),
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
		).
		First(&webhook).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 1,
				"msg":  "webhook不存在",
				"data": nil,
			})
		} else {
			logHandlerFailure(c, "webhook.get_stats_config", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 1,
				"msg":  "获取统计数据失败",
				"data": nil,
			})
		}
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

	rows, err := h.db.WithContext(c.Request.Context()).Raw(`
		SELECT 
			DATE(created_at) as date,
			COUNT(*) as sent,
			COUNT(CASE WHEN status = 'success' THEN 1 END) as success,
			COUNT(CASE WHEN status = 'failed' THEN 1 END) as failed
		FROM webhook_logs 
		WHERE organization_id = ? AND project_id = ?
		  AND config_id = ? AND created_at >= ?
		GROUP BY DATE(created_at)
		ORDER BY date
	`,
		operation.Scope.OrganizationID,
		operation.Scope.ProjectID,
		uint(id),
		startTime,
	).Rows()

	if err != nil {
		logHandlerFailure(c, "webhook.query_stats", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "获取统计数据失败",
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
		if err := h.db.ScanRows(rows, &stat); err != nil {
			logHandlerFailure(c, "webhook.scan_stats", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 1,
				"msg":  "获取统计数据失败",
				"data": nil,
			})
			return
		}
		dailyStats = append(dailyStats, stat)
	}
	if err := rows.Err(); err != nil {
		logHandlerFailure(c, "webhook.iterate_stats", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "获取统计数据失败",
			"data": nil,
		})
		return
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

func requireWebhookProjectAccess(
	c *gin.Context,
	manage bool,
) (services.OperationContext, bool) {
	if c == nil || c.Request == nil {
		return services.OperationContext{}, false
	}
	operation, err := services.OperationContextFromContext(c.Request.Context())
	if err != nil || operation.Source != services.SourceProtocolHumanREST ||
		operation.Actor.Type != models.ActorTypeHuman {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"code": 1,
			"msg":  "无权访问项目 Webhook",
			"data": nil,
		})
		return services.OperationContext{}, false
	}
	access, exists := ProjectAccessFromGin(c)
	if !exists || access.Scope != operation.Scope {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"code": 1,
			"msg":  "无权访问项目 Webhook",
			"data": nil,
		})
		return services.OperationContext{}, false
	}
	if manage &&
		access.Role != models.ProjectRoleAdmin &&
		access.Role != models.ProjectRoleManager {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"code": 1,
			"msg":  "仅项目管理员或经理可管理 Webhook",
			"data": nil,
		})
		return services.OperationContext{}, false
	}
	return operation, true
}
