package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gongdan-system/internal/models"
	"gongdan-system/internal/services"
)

// AdminUserHandler 管理员用户管理处理器
type AdminUserHandler struct {
	adminUserService *services.AdminUserService
}

// NewAdminUserHandler 创建管理员用户管理处理器
func NewAdminUserHandler(adminUserService *services.AdminUserService) *AdminUserHandler {
	return &AdminUserHandler{
		adminUserService: adminUserService,
	}
}

// GetUserList 获取用户列表
// @Summary 获取用户列表
// @Description 管理员获取系统中所有用户的列表，支持分页、搜索和过滤
// @Tags 管理员-用户管理
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param page query int false "页码" default(1)
// @Param page_size query int false "每页数量" default(20)
// @Param role query string false "用户角色过滤" Enums(admin, agent, customer, supervisor)
// @Param status query string false "用户状态过滤" Enums(active, inactive, suspended, deleted)
// @Param search query string false "搜索关键词（用户名、邮箱、姓名）"
// @Param order_by query string false "排序字段" Enums(id, username, email, created_at, updated_at, last_login_at) default(created_at)
// @Param order query string false "排序方向" Enums(asc, desc) default(desc)
// @Success 200 {object} ApiResponse{data=services.UserListResponse}
// @Failure 400 {object} ApiResponse
// @Failure 401 {object} ApiResponse
// @Failure 403 {object} ApiResponse
// @Failure 500 {object} ApiResponse
// @Router /api/admin/users [get]
func (h *AdminUserHandler) GetUserList(c *gin.Context) {
	var req services.UserListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "查询参数错误: " + err.Error(),
			Data: nil,
		})
		return
	}

	response, err := h.adminUserService.GetUserList(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "获取用户列表失败: " + err.Error(),
			Data: nil,
		})
		return
	}

	c.JSON(http.StatusOK, ApiResponse{
		Code: 0,
		Msg:  "获取用户列表成功",
		Data: response,
	})
}

// GetUser 获取用户详细信息
// @Summary 获取用户详细信息
// @Description 管理员获取指定用户的详细信息
// @Tags 管理员-用户管理
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param id path int true "用户ID"
// @Success 200 {object} ApiResponse{data=models.UserResponse}
// @Failure 400 {object} ApiResponse
// @Failure 401 {object} ApiResponse
// @Failure 403 {object} ApiResponse
// @Failure 404 {object} ApiResponse
// @Failure 500 {object} ApiResponse
// @Router /api/admin/users/{id} [get]
func (h *AdminUserHandler) GetUser(c *gin.Context) {
	userIDStr := c.Param("id")
	userID, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "无效的用户ID",
			Data: nil,
		})
		return
	}

	user, err := h.adminUserService.GetUserByID(c.Request.Context(), uint(userID))
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, ApiResponse{
				Code: 1,
				Msg:  "用户不存在",
				Data: nil,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "获取用户信息失败: " + err.Error(),
			Data: nil,
		})
		return
	}

	c.JSON(http.StatusOK, ApiResponse{
		Code: 0,
		Msg:  "获取用户信息成功",
		Data: user.ToResponse(),
	})
}

// CreateUser 创建新用户
// @Summary 创建新用户
// @Description 管理员创建新的用户账号
// @Tags 管理员-用户管理
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param request body models.UserCreateRequest true "创建用户请求"
// @Success 201 {object} ApiResponse{data=models.UserResponse}
// @Failure 400 {object} ApiResponse
// @Failure 401 {object} ApiResponse
// @Failure 403 {object} ApiResponse
// @Failure 409 {object} ApiResponse
// @Failure 500 {object} ApiResponse
// @Router /api/admin/users [post]
func (h *AdminUserHandler) CreateUser(c *gin.Context) {
	var req models.UserCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "请求参数错误: " + err.Error(),
			Data: nil,
		})
		return
	}

	// 基本验证
	if len(req.Username) < 3 {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "用户名长度至少3位",
			Data: nil,
		})
		return
	}

	if len(req.Password) < 8 {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "密码长度至少8位",
			Data: nil,
		})
		return
	}

	user, err := h.adminUserService.CreateUser(c.Request.Context(), &req)
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			c.JSON(http.StatusConflict, ApiResponse{
				Code: 1,
				Msg:  err.Error(),
				Data: nil,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "创建用户失败: " + err.Error(),
			Data: nil,
		})
		return
	}

	c.JSON(http.StatusCreated, ApiResponse{
		Code: 0,
		Msg:  "用户创建成功",
		Data: user.ToResponse(),
	})
}

// UpdateUser 更新用户信息
// @Summary 更新用户信息
// @Description 管理员更新指定用户的信息
// @Tags 管理员-用户管理
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param id path int true "用户ID"
// @Param request body models.UserUpdateRequest true "更新用户请求"
// @Success 200 {object} ApiResponse{data=models.UserResponse}
// @Failure 400 {object} ApiResponse
// @Failure 401 {object} ApiResponse
// @Failure 403 {object} ApiResponse
// @Failure 404 {object} ApiResponse
// @Failure 409 {object} ApiResponse
// @Failure 500 {object} ApiResponse
// @Router /api/admin/users/{id} [put]
func (h *AdminUserHandler) UpdateUser(c *gin.Context) {
	userIDStr := c.Param("id")
	userID, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "无效的用户ID",
			Data: nil,
		})
		return
	}

	var req models.UserUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "请求参数错误: " + err.Error(),
			Data: nil,
		})
		return
	}

	user, err := h.adminUserService.UpdateUser(c.Request.Context(), uint(userID), &req)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, ApiResponse{
				Code: 1,
				Msg:  "用户不存在",
				Data: nil,
			})
			return
		}
		if strings.Contains(err.Error(), "already exists") {
			c.JSON(http.StatusConflict, ApiResponse{
				Code: 1,
				Msg:  err.Error(),
				Data: nil,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "更新用户信息失败: " + err.Error(),
			Data: nil,
		})
		return
	}

	c.JSON(http.StatusOK, ApiResponse{
		Code: 0,
		Msg:  "用户信息更新成功",
		Data: user.ToResponse(),
	})
}

// ResetUserPassword 重置用户密码
// @Summary 重置用户密码
// @Description 管理员重置指定用户的密码
// @Tags 管理员-用户管理
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param id path int true "用户ID"
// @Param request body ResetPasswordRequest true "重置密码请求"
// @Success 200 {object} ApiResponse
// @Failure 400 {object} ApiResponse
// @Failure 401 {object} ApiResponse
// @Failure 403 {object} ApiResponse
// @Failure 404 {object} ApiResponse
// @Failure 500 {object} ApiResponse
// @Router /api/admin/users/{id}/reset-password [post]
func (h *AdminUserHandler) ResetUserPassword(c *gin.Context) {
	userIDStr := c.Param("id")
	userID, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "无效的用户ID",
			Data: nil,
		})
		return
	}

	var req ResetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "请求参数错误: " + err.Error(),
			Data: nil,
		})
		return
	}

	if len(req.NewPassword) < 8 {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "密码长度至少8位",
			Data: nil,
		})
		return
	}

	err = h.adminUserService.ResetUserPassword(c.Request.Context(), uint(userID), req.NewPassword)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, ApiResponse{
				Code: 1,
				Msg:  "用户不存在",
				Data: nil,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "重置密码失败: " + err.Error(),
			Data: nil,
		})
		return
	}

	c.JSON(http.StatusOK, ApiResponse{
		Code: 0,
		Msg:  "密码重置成功",
		Data: nil,
	})
}

// DeleteUser 删除用户
// @Summary 删除用户
// @Description 管理员删除指定用户（软删除）
// @Tags 管理员-用户管理
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param id path int true "用户ID"
// @Success 200 {object} ApiResponse
// @Failure 400 {object} ApiResponse
// @Failure 401 {object} ApiResponse
// @Failure 403 {object} ApiResponse
// @Failure 404 {object} ApiResponse
// @Failure 409 {object} ApiResponse
// @Failure 500 {object} ApiResponse
// @Router /api/admin/users/{id} [delete]
func (h *AdminUserHandler) DeleteUser(c *gin.Context) {
	userIDStr := c.Param("id")
	userID, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "无效的用户ID",
			Data: nil,
		})
		return
	}

	err = h.adminUserService.DeleteUser(c.Request.Context(), uint(userID))
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, ApiResponse{
				Code: 1,
				Msg:  "用户不存在",
				Data: nil,
			})
			return
		}
		if strings.Contains(err.Error(), "cannot delete") {
			c.JSON(http.StatusConflict, ApiResponse{
				Code: 1,
				Msg:  err.Error(),
				Data: nil,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "删除用户失败: " + err.Error(),
			Data: nil,
		})
		return
	}

	c.JSON(http.StatusOK, ApiResponse{
		Code: 0,
		Msg:  "用户删除成功",
		Data: nil,
	})
}

// ToggleUserStatus 切换用户状态
// @Summary 切换用户状态
// @Description 管理员切换用户状态（启用/禁用）
// @Tags 管理员-用户管理
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param id path int true "用户ID"
// @Success 200 {object} ApiResponse{data=models.UserResponse}
// @Failure 400 {object} ApiResponse
// @Failure 401 {object} ApiResponse
// @Failure 403 {object} ApiResponse
// @Failure 404 {object} ApiResponse
// @Failure 409 {object} ApiResponse
// @Failure 500 {object} ApiResponse
// @Router /api/admin/users/{id}/toggle-status [post]
func (h *AdminUserHandler) ToggleUserStatus(c *gin.Context) {
	userIDStr := c.Param("id")
	userID, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "无效的用户ID",
			Data: nil,
		})
		return
	}

	user, err := h.adminUserService.ToggleUserStatus(c.Request.Context(), uint(userID))
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, ApiResponse{
				Code: 1,
				Msg:  "用户不存在",
				Data: nil,
			})
			return
		}
		if strings.Contains(err.Error(), "cannot") {
			c.JSON(http.StatusConflict, ApiResponse{
				Code: 1,
				Msg:  err.Error(),
				Data: nil,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "切换用户状态失败: " + err.Error(),
			Data: nil,
		})
		return
	}

	c.JSON(http.StatusOK, ApiResponse{
		Code: 0,
		Msg:  "用户状态切换成功",
		Data: user.ToResponse(),
	})
}

// BatchDeleteUsers 批量删除用户
// @Summary 批量删除用户
// @Description 管理员批量删除多个用户
// @Tags 管理员-用户管理
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param request body BatchDeleteUsersRequest true "批量删除用户请求"
// @Success 200 {object} ApiResponse
// @Failure 400 {object} ApiResponse
// @Failure 401 {object} ApiResponse
// @Failure 403 {object} ApiResponse
// @Failure 409 {object} ApiResponse
// @Failure 500 {object} ApiResponse
// @Router /api/admin/users/batch-delete [post]
func (h *AdminUserHandler) BatchDeleteUsers(c *gin.Context) {
	var req BatchDeleteUsersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "请求参数错误: " + err.Error(),
			Data: nil,
		})
		return
	}

	if len(req.UserIDs) == 0 {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "用户ID列表不能为空",
			Data: nil,
		})
		return
	}

	err := h.adminUserService.BatchDeleteUsers(c.Request.Context(), req.UserIDs)
	if err != nil {
		if strings.Contains(err.Error(), "cannot delete") {
			c.JSON(http.StatusConflict, ApiResponse{
				Code: 1,
				Msg:  err.Error(),
				Data: nil,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "批量删除用户失败: " + err.Error(),
			Data: nil,
		})
		return
	}

	c.JSON(http.StatusOK, ApiResponse{
		Code: 0,
		Msg:  "批量删除用户成功",
		Data: nil,
	})
}

// GetUserStats 获取用户统计信息
// @Summary 获取用户统计信息
// @Description 管理员获取系统用户统计信息
// @Tags 管理员-用户管理
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} ApiResponse{data=services.UserStatsResponse}
// @Failure 401 {object} ApiResponse
// @Failure 403 {object} ApiResponse
// @Failure 500 {object} ApiResponse
// @Router /api/admin/users/stats [get]
func (h *AdminUserHandler) GetUserStats(c *gin.Context) {
	stats, err := h.adminUserService.GetUserStats(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "获取用户统计信息失败: " + err.Error(),
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

// 请求和响应结构体

// ResetPasswordRequest 重置密码请求
type ResetPasswordRequest struct {
	NewPassword string `json:"new_password" binding:"required,min=8" example:"newpassword123"`
}

// BatchDeleteUsersRequest 批量删除用户请求
type BatchDeleteUsersRequest struct {
	UserIDs []uint `json:"user_ids" binding:"required" example:"[1,2,3]"`
}