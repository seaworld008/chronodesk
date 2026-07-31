package handlers

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/gorm"
)

// UserHandler 用户处理器
type UserHandler struct {
	userService          *services.UserService
	trustedDeviceService *services.TrustedDeviceService
}

type TrustedDeviceResponse struct {
	ID         uint      `json:"id"`
	DeviceName string    `json:"device_name"`
	LastUsedAt time.Time `json:"last_used_at"`
	LastIP     string    `json:"last_ip"`
	UserAgent  string    `json:"user_agent"`
	ExpiresAt  time.Time `json:"expires_at"`
	Revoked    bool      `json:"revoked"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// NewUserHandler 创建用户处理器
func NewUserHandler(userService *services.UserService, trustedDeviceService *services.TrustedDeviceService) *UserHandler {
	return &UserHandler{
		userService:          userService,
		trustedDeviceService: trustedDeviceService,
	}
}

// GetTrustedDevices 获取用户的可信设备列表
func (h *UserHandler) GetTrustedDevices(c *gin.Context) {
	userID := getUserIDFromContext(c)
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, ApiResponse{
			Code: 1,
			Msg:  "用户未认证",
			Data: nil,
		})
		return
	}

	if h.trustedDeviceService == nil {
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "可信设备服务未初始化",
			Data: nil,
		})
		return
	}

	query, err := parseDirectoryListQuery(
		c.Request.URL.Query(),
		directoryListQuerySpec{
			DefaultSortBy:    "revoked",
			DefaultSortOrder: "asc",
			SortFields: map[string]struct{}{
				"created_at":   {},
				"updated_at":   {},
				"last_used_at": {},
				"expires_at":   {},
				"revoked":      {},
				"device_name":  {},
			},
		},
	)
	if err != nil {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "可信设备查询参数无效",
			Data: nil,
		})
		return
	}

	devices, err := h.trustedDeviceService.ListTrustedDevicePage(
		c.Request.Context(),
		userID,
		services.DirectoryPageRequest{
			Page:      query.Page,
			PageSize:  query.PageSize,
			SortBy:    query.SortBy,
			SortOrder: query.SortOrder,
		},
	)
	if err != nil {
		logHandlerFailure(c, "user.list_trusted_devices", err)
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "获取可信设备失败",
			Data: nil,
		})
		return
	}

	responses := make([]TrustedDeviceResponse, len(devices.Items))
	for i, device := range devices.Items {
		responses[i] = TrustedDeviceResponse{
			ID:         device.ID,
			DeviceName: device.DeviceName,
			LastUsedAt: device.LastUsedAt,
			LastIP:     device.LastIP,
			UserAgent:  device.UserAgent,
			ExpiresAt:  device.ExpiresAt,
			Revoked:    device.Revoked,
			CreatedAt:  device.CreatedAt,
			UpdatedAt:  device.UpdatedAt,
		}
	}

	c.JSON(http.StatusOK, ApiResponse{
		Code: 0,
		Msg:  "获取可信设备成功",
		Data: services.DirectoryPage[TrustedDeviceResponse]{
			Items:      responses,
			Total:      devices.Total,
			Page:       devices.Page,
			PageSize:   devices.PageSize,
			TotalPages: devices.TotalPages,
		},
	})
}

// RevokeTrustedDevice 撤销指定可信设备
func (h *UserHandler) RevokeTrustedDevice(c *gin.Context) {
	userID := getUserIDFromContext(c)
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, ApiResponse{
			Code: 1,
			Msg:  "用户未认证",
			Data: nil,
		})
		return
	}

	if h.trustedDeviceService == nil {
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "可信设备服务未初始化",
			Data: nil,
		})
		return
	}

	deviceIDStr := c.Param("id")
	deviceIDValue, err := strconv.ParseUint(deviceIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "无效的设备ID",
			Data: nil,
		})
		return
	}

	deviceID := uint(deviceIDValue)
	if err := h.trustedDeviceService.RevokeTrustedDevice(c.Request.Context(), userID, deviceID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, ApiResponse{
				Code: 1,
				Msg:  "设备未找到或已撤销",
				Data: nil,
			})
			return
		}
		logHandlerFailure(c, "user.revoke_trusted_device", err)
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "撤销可信设备失败",
			Data: nil,
		})
		return
	}

	c.JSON(http.StatusOK, ApiResponse{
		Code: 0,
		Msg:  "已撤销可信设备",
		Data: nil,
	})
}

// GetLoginHistory 获取登录历史
// @Summary 获取登录历史记录
// @Description 获取当前用户的登录历史记录
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param page query int false "页码" default(1)
// @Param page_size query int false "每页数量" default(20)
// @Param status query string false "登录状态" Enums(success, failed, blocked, suspended)
// @Param start_date query string false "开始日期" format(date-time)
// @Param end_date query string false "结束日期" format(date-time)
// @Param ip_address query string false "登录IP"
// @Param device_type query string false "设备类型"
// @Param login_method query string false "登录方式"
// @Param session_id query string false "会话ID"
// @Param is_active query bool false "是否活跃会话"
// @Success 200 {object} ApiResponse{data=PaginatedResponse{items=[]models.LoginHistoryResponse}}
// @Failure 400 {object} ApiResponse
// @Failure 401 {object} ApiResponse
// @Failure 500 {object} ApiResponse
// @Router /api/user/login-history [get]
func (h *UserHandler) GetLoginHistory(c *gin.Context) {
	userID := getUserIDFromContext(c)
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, ApiResponse{
			Code: 1,
			Msg:  "用户未认证",
			Data: nil,
		})
		return
	}

	// 解析查询参数
	var req models.LoginHistoryRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "查询参数错误",
			Data: nil,
		})
		return
	}

	// 设置默认值
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 {
		req.PageSize = 20
	}
	if req.PageSize > 100 {
		req.PageSize = 100
	}

	histories, total, err := h.userService.GetLoginHistory(c.Request.Context(), userID, &req)
	if err != nil {
		logHandlerFailure(c, "user.get_login_history", err)
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "获取登录历史失败",
			Data: nil,
		})
		return
	}

	response := PaginatedResponse{
		Items:    histories,
		Total:    total,
		Page:     int64(req.Page),
		PageSize: int64(req.PageSize),
	}

	c.JSON(http.StatusOK, ApiResponse{
		Code: 0,
		Msg:  "获取登录历史成功",
		Data: response,
	})
}

// GetStats 获取用户统计信息
// @Summary 获取用户统计信息
// @Description 获取当前用户的个人统计信息，包括工单、评论、登录等数据
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ApiResponse{data=models.UserProfileStats}
// @Failure 401 {object} ApiResponse
// @Failure 500 {object} ApiResponse
// @Router /api/user/stats [get]
func (h *UserHandler) GetStats(c *gin.Context) {
	userID := getUserIDFromContext(c)
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, ApiResponse{
			Code: 1,
			Msg:  "用户未认证",
			Data: nil,
		})
		return
	}

	stats, err := h.userService.GetUserStats(c.Request.Context(), userID)
	if err != nil {
		logHandlerFailure(c, "user.get_statistics", err)
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "获取用户统计信息失败",
			Data: nil,
		})
		return
	}

	c.JSON(http.StatusOK, ApiResponse{
		Code: 0,
		Msg:  "获取用户统计信息成功",
		Data: stats,
	})
}

// UploadAvatar 上传头像
// @Summary 上传用户头像
// @Description 上传并更新用户头像
// @Tags 用户管理
// @Accept multipart/form-data
// @Produce json
// @Security ApiKeyAuth
// @Param avatar formData file true "头像文件"
// @Success 200 {object} ApiResponse{data=UploadAvatarResponse}
// @Failure 400 {object} ApiResponse
// @Failure 401 {object} ApiResponse
// @Failure 413 {object} ApiResponse
// @Failure 500 {object} ApiResponse
// @Router /api/user/avatar [post]
func (h *UserHandler) UploadAvatar(c *gin.Context) {
	userID := getUserIDFromContext(c)
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, ApiResponse{
			Code: 1,
			Msg:  "用户未认证",
			Data: nil,
		})
		return
	}

	file, header, err := c.Request.FormFile("avatar")
	if err != nil {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "文件上传错误",
			Data: nil,
		})
		return
	}
	defer file.Close()

	avatarURL, err := h.userService.UploadAvatar(c.Request.Context(), userID, file, header)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrAttachmentTooLarge):
			c.JSON(http.StatusRequestEntityTooLarge, ApiResponse{
				Code: 1,
				Msg:  "文件过大，最大支持2MB",
				Data: nil,
			})
			return
		case errors.Is(err, services.ErrInvalidAvatar):
			c.JSON(http.StatusBadRequest, ApiResponse{
				Code: 1,
				Msg:  "头像格式无效，请上传有效的 JPEG 或 PNG 图片",
				Data: nil,
			})
			return
		case errors.Is(err, services.ErrAvatarStorageMissing):
			c.JSON(http.StatusServiceUnavailable, ApiResponse{
				Code: 1,
				Msg:  "头像存储服务暂不可用",
				Data: nil,
			})
			return
		}
		logHandlerFailure(c, "user.upload_avatar", err)
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "头像上传失败，请稍后重试",
			Data: nil,
		})
		return
	}

	response := UploadAvatarResponse{
		AvatarURL: avatarURL,
	}

	c.JSON(http.StatusOK, ApiResponse{
		Code: 0,
		Msg:  "头像上传成功",
		Data: response,
	})
}

// GetAvatar serves immutable, sanitized avatar objects. Avatar URLs are opaque
// and public because browser image elements cannot attach the API Bearer token.
func (h *UserHandler) GetAvatar(c *gin.Context) {
	userID64, err := strconv.ParseUint(c.Param("userID"), 10, 32)
	if err != nil || userID64 == 0 {
		c.Status(http.StatusNotFound)
		return
	}
	reader, contentType, err := h.userService.OpenAvatar(
		c.Request.Context(),
		uint(userID64),
		c.Param("filename"),
	)
	if err != nil {
		if errors.Is(err, services.ErrAvatarStorageMissing) {
			c.Status(http.StatusServiceUnavailable)
			return
		}
		c.Status(http.StatusNotFound)
		return
	}
	defer reader.Close()

	payload, err := io.ReadAll(io.LimitReader(reader, 2*1024*1024+1))
	if err != nil || len(payload) > 2*1024*1024 {
		if err != nil {
			logHandlerFailure(c, "user.read_avatar", err)
		}
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, contentType, payload)
}

// DeleteLoginSession 删除登录会话
// @Summary 删除指定的登录会话
// @Description 删除指定的登录会话（踢出特定设备）
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param id path int true "登录历史ID"
// @Success 200 {object} ApiResponse
// @Failure 400 {object} ApiResponse
// @Failure 401 {object} ApiResponse
// @Failure 404 {object} ApiResponse
// @Failure 500 {object} ApiResponse
// @Router /api/user/login-history/{id} [delete]
func (h *UserHandler) DeleteLoginSession(c *gin.Context) {
	userID := getUserIDFromContext(c)
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, ApiResponse{
			Code: 1,
			Msg:  "用户未认证",
			Data: nil,
		})
		return
	}

	historyIDStr := c.Param("id")
	historyIDValue, err := strconv.ParseUint(historyIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "无效的会话ID",
			Data: nil,
		})
		return
	}

	historyID := uint(historyIDValue)
	if err := h.userService.DeleteLoginSession(c.Request.Context(), userID, historyID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, ApiResponse{
				Code: 1,
				Msg:  "会话不存在",
				Data: nil,
			})
			return
		}
		logHandlerFailure(c, "user.delete_login_session", err)
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "删除会话失败",
			Data: nil,
		})
		return
	}

	c.JSON(http.StatusOK, ApiResponse{
		Code: 0,
		Msg:  "会话删除成功",
		Data: nil,
	})
}

// 辅助函数

// getUserIDFromContext 从上下文中获取用户ID
func getUserIDFromContext(c *gin.Context) uint {
	userID, exists := c.Get("user_id")
	if !exists {
		return 0
	}

	userIDUint, ok := userID.(uint)
	if !ok {
		return 0
	}

	return userIDUint
}

// UploadAvatarResponse 上传头像响应
type UploadAvatarResponse struct {
	AvatarURL string `json:"avatar_url" example:"/uploads/avatars/avatar_1_1640000000.jpg"`
}

// PaginatedResponse 分页响应
type PaginatedResponse struct {
	Items    interface{} `json:"items"`
	Total    int64       `json:"total"`
	Page     int64       `json:"page"`
	PageSize int64       `json:"page_size"`
}

// ApiResponse 统一API响应格式
type ApiResponse struct {
	Code int         `json:"code" example:"0"`
	Msg  string      `json:"msg" example:"success"`
	Data interface{} `json:"data"`
}
