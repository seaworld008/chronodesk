package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gongdan-system/internal/models"
	"gongdan-system/internal/services"
)

// SystemHandler 系统配置处理器
type SystemHandler struct {
	db         *gorm.DB
	cleanupSvc *services.CleanupService
}

// NewSystemHandler 创建系统配置处理器
func NewSystemHandler(db *gorm.DB) *SystemHandler {
	return &SystemHandler{
		db:         db,
		cleanupSvc: services.NewCleanupService(db),
	}
}

// RegisterRoutes 注册路由
func (h *SystemHandler) RegisterRoutes(router *gin.RouterGroup) {
	// 系统配置相关路由 - 仅管理员可访问
	system := router.Group("/system")
	{
		// 配置管理
		system.GET("/configs", h.GetConfigs)
		system.POST("/configs", h.CreateConfig)
		system.PUT("/configs/:key", h.UpdateConfig)
		system.DELETE("/configs/:key", h.DeleteConfig)
		system.GET("/configs/:key", h.GetConfig)

		// 清理配置专门接口
		system.GET("/cleanup/config", h.GetCleanupConfig)
		system.PUT("/cleanup/config", h.UpdateCleanupConfig)
		
		// 清理操作
		system.POST("/cleanup/execute", h.ExecuteCleanup)
		system.POST("/cleanup/execute-all", h.ExecuteAllCleanup)
		system.GET("/cleanup/logs", h.GetCleanupLogs)
		system.GET("/cleanup/stats", h.GetCleanupStats)
	}
}

// GetConfigs 获取所有配置
func (h *SystemHandler) GetConfigs(c *gin.Context) {
	category := c.Query("category")
	group := c.Query("group")
	isActive := c.Query("is_active")

	query := h.db.Model(&models.SystemConfig{})
	
	if category != "" {
		query = query.Where("category = ?", category)
	}
	if group != "" {
		query = query.Where("\"group\" = ?", group)
	}
	if isActive != "" {
		query = query.Where("is_active = ?", isActive == "true")
	}

	var configs []models.SystemConfig
	if err := query.Order("category, \"group\", key").Find(&configs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed_to_get_configs",
			"message": "Failed to retrieve system configurations",
		})
		return
	}

	responses := make([]*models.SystemConfigResponse, len(configs))
	for i, config := range configs {
		responses[i] = config.ToResponse()
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    responses,
		"total":   len(responses),
	})
}

// CreateConfig 创建新配置
func (h *SystemHandler) CreateConfig(c *gin.Context) {
	var req models.SystemConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
			"details": err.Error(),
		})
		return
	}

	// 获取当前用户ID
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":   "unauthorized",
			"message": "User not authenticated",
		})
		return
	}

	// 检查配置是否已存在
	var existingConfig models.SystemConfig
	if err := h.db.Where("key = ?", req.Key).First(&existingConfig).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{
			"error":   "config_exists",
			"message": "Configuration with this key already exists",
		})
		return
	}

	// 创建新配置
	uid := userID.(uint)
	config := models.SystemConfig{
		Key:         req.Key,
		Description: req.Description,
		Category:    req.Category,
		Group:       req.Group,
		IsRequired:  req.IsRequired != nil && *req.IsRequired,
		IsActive:    req.IsActive == nil || *req.IsActive,
		UpdatedBy:   &uid,
	}

	if err := config.SetValue(req.Value); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_value",
			"message": "Invalid configuration value",
			"details": err.Error(),
		})
		return
	}

	if err := h.db.Create(&config).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed_to_create_config",
			"message": "Failed to create configuration",
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    config.ToResponse(),
		"message": "Configuration created successfully",
	})
}

// UpdateConfig 更新配置
func (h *SystemHandler) UpdateConfig(c *gin.Context) {
	key := c.Param("key")
	
	var req models.SystemConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
			"details": err.Error(),
		})
		return
	}

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":   "unauthorized",
			"message": "User not authenticated",
		})
		return
	}

	var config models.SystemConfig
	if err := h.db.Where("key = ?", key).First(&config).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "config_not_found",
				"message": "Configuration not found",
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "database_error",
				"message": "Failed to retrieve configuration",
			})
		}
		return
	}

	// 更新配置值
	if err := config.SetValue(req.Value); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_value",
			"message": "Invalid configuration value",
			"details": err.Error(),
		})
		return
	}

	// 更新其他字段
	if req.Description != "" {
		config.Description = req.Description
	}
	if req.IsActive != nil {
		config.IsActive = *req.IsActive
	}
	uid := userID.(uint)
	config.UpdatedBy = &uid
	config.Version++

	if err := h.db.Save(&config).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed_to_update_config",
			"message": "Failed to update configuration",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    config.ToResponse(),
		"message": "Configuration updated successfully",
	})
}

// DeleteConfig 删除配置
func (h *SystemHandler) DeleteConfig(c *gin.Context) {
	key := c.Param("key")

	var config models.SystemConfig
	if err := h.db.Where("key = ?", key).First(&config).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "config_not_found",
				"message": "Configuration not found",
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "database_error",
				"message": "Failed to retrieve configuration",
			})
		}
		return
	}

	// 检查是否为必需配置
	if config.IsRequired {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "cannot_delete_required_config",
			"message": "Cannot delete required configuration",
		})
		return
	}

	if err := h.db.Delete(&config).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed_to_delete_config",
			"message": "Failed to delete configuration",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Configuration deleted successfully",
	})
}

// GetConfig 获取单个配置
func (h *SystemHandler) GetConfig(c *gin.Context) {
	key := c.Param("key")

	var config models.SystemConfig
	if err := h.db.Where("key = ?", key).First(&config).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "config_not_found",
				"message": "Configuration not found",
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "database_error",
				"message": "Failed to retrieve configuration",
			})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    config.ToResponse(),
	})
}

// GetCleanupConfig 获取清理配置
func (h *SystemHandler) GetCleanupConfig(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	config, err := h.cleanupSvc.GetCleanupConfig(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed_to_get_cleanup_config",
			"message": "Failed to retrieve cleanup configuration",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    config,
	})
}

// UpdateCleanupConfig 更新清理配置
func (h *SystemHandler) UpdateCleanupConfig(c *gin.Context) {
	var req models.CleanupConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
			"details": err.Error(),
		})
		return
	}

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":   "unauthorized",
			"message": "User not authenticated",
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	uid := userID.(uint)
	if err := h.cleanupSvc.SetCleanupConfig(ctx, &req, uid); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "failed_to_update_cleanup_config",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Cleanup configuration updated successfully",
		"data":    req,
	})
}

// ExecuteCleanup 执行清理任务
func (h *SystemHandler) ExecuteCleanup(c *gin.Context) {
	var req struct {
		TaskType string `json:"task_type" validate:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "Invalid request body",
			"details": err.Error(),
		})
		return
	}

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":   "unauthorized",
			"message": "User not authenticated",
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second) // 5分钟超时
	defer cancel()

	// 异步执行清理任务
	go func() {
		uid := userID.(uint)
		if err := h.cleanupSvc.ExecuteCleanup(ctx, req.TaskType, "manual", &uid); err != nil {
			// 记录错误日志，但不影响响应
			// log.Printf("Cleanup task %s failed: %v", req.TaskType, err)
		}
	}()

	c.JSON(http.StatusAccepted, gin.H{
		"success": true,
		"message": "Cleanup task started successfully",
		"data": gin.H{
			"task_type":    req.TaskType,
			"trigger_type": "manual",
			"status":       "started",
		},
	})
}

// ExecuteAllCleanup 执行所有清理任务
func (h *SystemHandler) ExecuteAllCleanup(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":   "unauthorized",
			"message": "User not authenticated",
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second) // 10分钟超时
	defer cancel()

	// 异步执行所有清理任务
	go func() {
		uid := userID.(uint)
		if err := h.cleanupSvc.ExecuteAllCleanupTasks(ctx, "manual", &uid); err != nil {
			// 记录错误日志，但不影响响应
			// log.Printf("All cleanup tasks failed: %v", err)
		}
	}()

	c.JSON(http.StatusAccepted, gin.H{
		"success": true,
		"message": "All cleanup tasks started successfully",
		"data": gin.H{
			"trigger_type": "manual",
			"status":       "started",
		},
	})
}

// GetCleanupLogs 获取清理日志
func (h *SystemHandler) GetCleanupLogs(c *gin.Context) {
	taskType := c.Query("task_type")
	limitStr := c.Query("limit")
	
	limit := 20
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	logs, err := h.cleanupSvc.GetCleanupLogs(ctx, taskType, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed_to_get_cleanup_logs",
			"message": "Failed to retrieve cleanup logs",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    logs,
		"total":   len(logs),
	})
}

// GetCleanupStats 获取清理统计信息
func (h *SystemHandler) GetCleanupStats(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stats, err := h.cleanupSvc.GetCleanupStats(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed_to_get_cleanup_stats",
			"message": "Failed to retrieve cleanup statistics",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    stats,
	})
}