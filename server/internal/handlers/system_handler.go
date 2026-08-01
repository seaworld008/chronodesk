package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/gorm"
)

// SystemHandler 系统配置处理器
type SystemHandler struct {
	cleanupSvc cleanupService
}

type cleanupService interface {
	GetCleanupConfig(context.Context) (*models.CleanupConfig, error)
	SetCleanupConfig(context.Context, *models.CleanupConfig, uint) error
	ExecuteCleanup(context.Context, string, string, *uint) error
	ExecuteAllCleanupTasks(context.Context, string, *uint) error
	ListCleanupLogPage(
		context.Context,
		string,
		services.DirectoryPageRequest,
	) (*services.DirectoryPage[*models.CleanupLogResponse], error)
	GetCleanupStats(context.Context) (*services.CleanupStatsResponse, error)
}

// NewSystemHandler 创建系统配置处理器
func NewSystemHandler(db *gorm.DB) *SystemHandler {
	return &SystemHandler{
		cleanupSvc: services.NewCleanupService(db),
	}
}

// RegisterRoutes 注册路由
func (h *SystemHandler) RegisterRoutes(router *gin.RouterGroup) {
	// 系统清理路由 - 仅管理员可访问
	system := router.Group("/system")
	{
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

// GetCleanupConfig 获取清理配置
func (h *SystemHandler) GetCleanupConfig(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	config, err := h.cleanupSvc.GetCleanupConfig(ctx)
	if err != nil {
		logHandlerFailure(c, "system_cleanup.get_config", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed_to_get_cleanup_config",
			"message": "获取清理配置失败",
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
	if err := decodeStrictJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "请求体格式无效",
		})
		return
	}

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":   "unauthorized",
			"message": "用户未登录",
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	uid := userID.(uint)
	if err := h.cleanupSvc.SetCleanupConfig(ctx, &req, uid); err != nil {
		logHandlerFailure(c, "system_cleanup.update_config", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "failed_to_update_cleanup_config",
			"message": "更新清理配置失败，请检查配置内容",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "清理配置更新成功",
		"data":    req,
	})
}

// ExecuteCleanup 执行清理任务
func (h *SystemHandler) ExecuteCleanup(c *gin.Context) {
	var req struct {
		TaskType string `json:"task_type" validate:"required"`
	}

	if err := decodeStrictJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"message": "请求体格式无效",
		})
		return
	}

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":   "unauthorized",
			"message": "用户未登录",
		})
		return
	}

	uid, ok := userID.(uint)
	if !ok || uid == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":   "unauthorized",
			"message": "登录身份无效",
		})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 300*time.Second)
	defer cancel()
	if err := h.cleanupSvc.ExecuteCleanup(ctx, req.TaskType, "manual", &uid); err != nil {
		logHandlerFailure(c, "system_cleanup.execute", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "cleanup_failed",
			"message": "执行清理任务失败",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "清理任务已完成",
		"data": gin.H{
			"task_type":    req.TaskType,
			"trigger_type": "manual",
			"status":       "completed",
		},
	})
}

// ExecuteAllCleanup 执行所有清理任务
func (h *SystemHandler) ExecuteAllCleanup(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":   "unauthorized",
			"message": "用户未登录",
		})
		return
	}

	uid, ok := userID.(uint)
	if !ok || uid == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":   "unauthorized",
			"message": "登录身份无效",
		})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 600*time.Second)
	defer cancel()
	if err := h.cleanupSvc.ExecuteAllCleanupTasks(ctx, "manual", &uid); err != nil {
		logHandlerFailure(c, "system_cleanup.execute_all", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "cleanup_failed",
			"message": "执行全部清理任务失败",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "全部清理任务已完成",
		"data": gin.H{
			"trigger_type": "manual",
			"status":       "completed",
		},
	})
}

// GetCleanupLogs 获取清理日志
func (h *SystemHandler) GetCleanupLogs(c *gin.Context) {
	query, err := parseDirectoryListQuery(
		c.Request.URL.RawQuery,
		directoryListQuerySpec{
			DefaultSortBy:    "created_at",
			DefaultSortOrder: "desc",
			SortFields: map[string]struct{}{
				"created_at":      {},
				"start_time":      {},
				"end_time":        {},
				"status":          {},
				"task_type":       {},
				"records_deleted": {},
			},
			FilterFields: map[string]struct{}{"task_type": {}},
		},
	)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_query",
			"message": "清理日志查询参数无效",
		})
		return
	}
	taskType, _ := query.value("task_type")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	logs, err := h.cleanupSvc.ListCleanupLogPage(
		ctx,
		taskType,
		services.DirectoryPageRequest{
			Page:      query.Page,
			PageSize:  query.PageSize,
			SortBy:    query.SortBy,
			SortOrder: query.SortOrder,
		},
	)
	if errors.Is(err, services.ErrDirectoryListQuery) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_query",
			"message": "清理日志查询参数无效",
		})
		return
	}
	if err != nil {
		logHandlerFailure(c, "system_cleanup.get_logs", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed_to_get_cleanup_logs",
			"message": "获取清理日志失败",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    logs,
	})
}

// GetCleanupStats 获取清理统计信息
func (h *SystemHandler) GetCleanupStats(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	stats, err := h.cleanupSvc.GetCleanupStats(ctx)
	if err != nil {
		logHandlerFailure(c, "system_cleanup.get_stats", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed_to_get_cleanup_stats",
			"message": "获取清理统计信息失败",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    stats,
	})
}
