package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

// ConfigHandler 配置管理处理器
type ConfigHandler struct {
	configService *services.ConfigService
}

type systemConfigUpdateRequest struct {
	Value       string `json:"value"`
	ValueType   string `json:"value_type" binding:"required,oneof=string int bool json"`
	Description string `json:"description" binding:"max=500"`
	Category    string `json:"category" binding:"max=50"`
	Group       string `json:"group" binding:"max=50"`
}

// NewConfigHandler 创建配置处理器
func NewConfigHandler(db *gorm.DB) *ConfigHandler {
	return &ConfigHandler{
		configService: services.NewConfigService(db),
	}
}

// GetAllConfigs 获取所有配置
// @Summary 获取所有系统配置
// @Description 获取所有系统配置列表，支持按分类筛选
// @Tags 系统配置
// @Security ApiKeyAuth
// @Param category query string false "配置分类" Enums(system,security,email,ticket,notify,ui)
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 401 {object} map[string]interface{} "未授权"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/platform/configs [get]
func (h *ConfigHandler) GetAllConfigs(c *gin.Context) {
	category := c.Query("category")

	var configs []models.SystemConfig
	var err error

	if category != "" {
		configs, err = h.configService.GetConfigsByCategory(category)
	} else {
		configs, err = h.configService.GetAllConfigs()
	}

	if err != nil {
		logHandlerFailure(c, "config.list", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取配置失败",
			"error":   "internal_error",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "获取配置成功",
		"data":    configs,
	})
}

// GetConfig 获取单个配置
// @Summary 获取指定配置
// @Description 获取指定键的配置值
// @Tags 系统配置
// @Security ApiKeyAuth
// @Param key path string true "配置键"
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 404 {object} map[string]interface{} "配置不存在"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/platform/configs/{key} [get]
func (h *ConfigHandler) GetConfig(c *gin.Context) {
	key := c.Param("key")

	value, err := h.configService.GetConfig(key)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "配置不存在",
			"error":   "config_not_found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "获取配置成功",
		"data": gin.H{
			"key":   key,
			"value": value,
		},
	})
}

// CreateConfig 创建配置
// @Summary 创建新配置
// @Description 创建新的系统配置项
// @Tags 系统配置
// @Security ApiKeyAuth
// @Param config body models.SystemConfig true "配置信息"
// @Success 201 {object} map[string]interface{} "创建成功"
// @Failure 400 {object} map[string]interface{} "请求参数错误"
// @Failure 409 {object} map[string]interface{} "配置已存在"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/platform/configs [post]
func (h *ConfigHandler) CreateConfig(c *gin.Context) {
	var req models.SystemConfig
	if err := decodeStrictJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数错误",
			"error":   "invalid_request",
		})
		return
	}

	// 验证配置值类型
	if err := h.configService.ValidateConfig(req.Key, req.Value, req.ValueType); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "配置值验证失败",
			"error":   "invalid_config_value",
		})
		return
	}

	if err := h.configService.SetConfig(req.Key, req.Value, req.ValueType, req.Description, req.Category, req.Group); err != nil {
		logHandlerFailure(c, "config.create", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "创建配置失败",
			"error":   "internal_error",
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "配置创建成功",
		"data":    req,
	})
}

// UpdateConfig 更新配置
// @Summary 更新配置
// @Description 更新指定配置的值
// @Tags 系统配置
// @Security ApiKeyAuth
// @Param key path string true "配置键"
// @Param config body models.SystemConfig true "配置信息"
// @Success 200 {object} map[string]interface{} "更新成功"
// @Failure 400 {object} map[string]interface{} "请求参数错误"
// @Failure 404 {object} map[string]interface{} "配置不存在"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/platform/configs/{key} [put]
func (h *ConfigHandler) UpdateConfig(c *gin.Context) {
	key := c.Param("key")

	var req systemConfigUpdateRequest
	if err := decodeStrictJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数错误",
			"error":   "invalid_request",
		})
		return
	}

	// 确保 key 一致
	// 验证配置值类型
	if err := h.configService.ValidateConfig(key, req.Value, req.ValueType); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "配置值验证失败",
			"error":   "invalid_config_value",
		})
		return
	}

	if err := h.configService.SetConfig(key, req.Value, req.ValueType, req.Description, req.Category, req.Group); err != nil {
		logHandlerFailure(c, "config.update", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "更新配置失败",
			"error":   "internal_error",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "配置更新成功",
		"data":    req,
	})
}

// DeleteConfig 删除配置
// @Summary 删除配置
// @Description 删除指定的配置项
// @Tags 系统配置
// @Security ApiKeyAuth
// @Param key path string true "配置键"
// @Success 200 {object} map[string]interface{} "删除成功"
// @Failure 404 {object} map[string]interface{} "配置不存在"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/platform/configs/{key} [delete]
func (h *ConfigHandler) DeleteConfig(c *gin.Context) {
	key := c.Param("key")

	if err := h.configService.DeleteConfig(key); err != nil {
		logHandlerFailure(c, "config.delete", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "删除配置失败",
			"error":   "internal_error",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "配置删除成功",
	})
}

// BatchUpdateConfigs 批量更新配置
// @Summary 批量更新配置
// @Description 批量更新多个配置项
// @Tags 系统配置
// @Security ApiKeyAuth
// @Param configs body []models.SystemConfig true "配置列表"
// @Success 200 {object} map[string]interface{} "更新成功"
// @Failure 400 {object} map[string]interface{} "请求参数错误"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/platform/configs/batch [put]
func (h *ConfigHandler) BatchUpdateConfigs(c *gin.Context) {
	var configs []models.SystemConfig
	if err := decodeStrictJSON(c, &configs); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请求参数错误",
			"error":   "invalid_request",
		})
		return
	}

	// 验证所有配置
	for _, config := range configs {
		if err := h.configService.ValidateConfig(config.Key, config.Value, config.ValueType); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": "配置验证失败",
				"error":   "invalid_config_value",
			})
			return
		}
	}

	if err := h.configService.BatchUpdateConfigs(configs); err != nil {
		logHandlerFailure(c, "config.batch_update", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "批量更新失败",
			"error":   "internal_error",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "批量更新成功",
		"data": gin.H{
			"updated_count": len(configs),
		},
	})
}

// GetSecurityPolicy 获取安全策略配置
// @Summary 获取安全策略
// @Description 获取系统安全策略配置
// @Tags 系统配置
// @Security ApiKeyAuth
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/platform/configs/security-policy [get]
func (h *ConfigHandler) GetSecurityPolicy(c *gin.Context) {
	policy, err := h.configService.GetSecurityPolicy()
	if err != nil {
		logHandlerFailure(c, "config.get_security_policy", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "获取安全策略失败",
			"error":   "internal_error",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "获取安全策略成功",
		"data":    policy,
	})
}

// ExportConfigs 导出配置
// @Summary 导出配置
// @Description 导出系统配置为JSON格式
// @Tags 系统配置
// @Security ApiKeyAuth
// @Param category query string false "配置分类"
// @Param format query string false "导出格式" Enums(json) default(json)
// @Success 200 {object} map[string]interface{} "导出成功"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/platform/configs/export [get]
func (h *ConfigHandler) ExportConfigs(c *gin.Context) {
	category := c.Query("category")
	format := c.DefaultQuery("format", "json")

	if format != "json" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "不支持的导出格式",
		})
		return
	}

	data, err := h.configService.ExportConfigs(category)
	if err != nil {
		logHandlerFailure(c, "config.export", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "导出配置失败",
			"error":   "internal_error",
		})
		return
	}

	// 设置响应头
	c.Header("Content-Type", "application/json")
	c.Header("Content-Disposition", "attachment; filename=system_configs.json")

	c.Data(http.StatusOK, "application/json", data)
}

// ImportConfigs 导入配置
// @Summary 导入配置
// @Description 从JSON文件导入配置
// @Tags 系统配置
// @Security ApiKeyAuth
// @Param file formData file true "配置文件"
// @Success 200 {object} map[string]interface{} "导入成功"
// @Failure 400 {object} map[string]interface{} "请求参数错误"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/platform/configs/import [post]
func (h *ConfigHandler) ImportConfigs(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请选择要导入的文件",
			"error":   "invalid_file",
		})
		return
	}

	// 打开文件
	src, err := file.Open()
	if err != nil {
		logHandlerFailure(c, "config.import_open_file", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "文件打开失败",
			"error":   "internal_error",
		})
		return
	}
	defer src.Close()

	// 读取文件内容
	data := make([]byte, file.Size)
	if _, err := src.Read(data); err != nil {
		logHandlerFailure(c, "config.import_read_file", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "文件读取失败",
			"error":   "internal_error",
		})
		return
	}

	// 导入配置
	if err := h.configService.ImportConfigs(data); err != nil {
		logHandlerFailure(c, "config.import", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "配置导入失败",
			"error":   "internal_error",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "配置导入成功",
		"data": gin.H{
			"filename": file.Filename,
			"size":     file.Size,
		},
	})
}

// InitDefaultConfigs 初始化默认配置
// @Summary 初始化默认配置
// @Description 初始化系统默认配置
// @Tags 系统配置
// @Security ApiKeyAuth
// @Success 200 {object} map[string]interface{} "成功"
// @Failure 500 {object} map[string]interface{} "服务器错误"
// @Router /api/platform/configs/init [post]
func (h *ConfigHandler) InitDefaultConfigs(c *gin.Context) {
	if err := h.configService.InitDefaultConfigs(); err != nil {
		logHandlerFailure(c, "config.initialize_defaults", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "初始化默认配置失败",
			"error":   "internal_error",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "默认配置初始化成功",
	})
}
