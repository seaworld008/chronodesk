package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

var e164PhonePattern = regexp.MustCompile(`^\+[1-9][0-9]{1,14}$`)

type optionalCreatePhone struct {
	present bool
	value   string
}

func (phone *optionalCreatePhone) UnmarshalJSON(data []byte) error {
	phone.present = true
	if err := json.Unmarshal(data, &phone.value); err != nil {
		return errors.New("phone must be an E.164 string")
	}
	return nil
}

type adminUserCreateRequest struct {
	Username     string              `json:"username" binding:"required,min=3,max=50"`
	Email        string              `json:"email" binding:"required,email"`
	Phone        optionalCreatePhone `json:"phone"`
	Password     string              `json:"password" binding:"required,min=8,max=128"`
	FirstName    string              `json:"first_name" binding:"omitempty,max=50"`
	LastName     string              `json:"last_name" binding:"omitempty,max=50"`
	DisplayName  string              `json:"display_name" binding:"omitempty,max=100"`
	PlatformRole models.PlatformRole `json:"platform_role" binding:"required,oneof=platform_admin security_auditor emergency_operator member"`
	Department   string              `json:"department" binding:"omitempty,max=100"`
	JobTitle     string              `json:"job_title" binding:"omitempty,max=100"`
	ManagerID    *uint               `json:"manager_id" binding:"omitempty,gt=0"`
}

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
// @Param platform_role query string false "平台角色过滤" Enums(platform_admin, security_auditor, emergency_operator, member)
// @Param status query string false "用户状态过滤" Enums(active, inactive, suspended, deleted)
// @Param search query string false "搜索关键词（用户名、邮箱、姓名）"
// @Param order_by query string false "排序字段" Enums(id, username, email, created_at, updated_at, last_login_at) default(created_at)
// @Param order query string false "排序方向" Enums(asc, desc) default(desc)
// @Success 200 {object} ApiResponse{data=services.UserListResponse}
// @Failure 400 {object} ApiResponse
// @Failure 401 {object} ApiResponse
// @Failure 403 {object} ApiResponse
// @Failure 500 {object} ApiResponse
// @Router /api/platform/users [get]
func (h *AdminUserHandler) GetUserList(c *gin.Context) {
	var req services.UserListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "查询参数错误",
			Data: nil,
		})
		return
	}

	response, err := h.adminUserService.GetUserList(c.Request.Context(), &req)
	if err != nil {
		logHandlerFailure(c, "admin_user.list", err)
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "获取用户列表失败",
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
// @Router /api/platform/users/{id} [get]
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
		logHandlerFailure(c, "admin_user.get", err)
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "获取用户信息失败",
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
// @Router /api/platform/users [post]
func (h *AdminUserHandler) CreateUser(c *gin.Context) {
	var request adminUserCreateRequest
	if err := decodeStrictAdminUserJSON(c, &request); err != nil {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "请求参数错误",
			Data: nil,
		})
		return
	}

	// 基本验证
	if len(request.Username) < 3 {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "用户名长度至少3位",
			Data: nil,
		})
		return
	}

	if len(request.Password) < 8 {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "密码长度至少8位",
			Data: nil,
		})
		return
	}
	phone := ""
	if request.Phone.present {
		phone = strings.TrimSpace(request.Phone.value)
	}
	if request.Phone.present &&
		(phone == "" || !e164PhonePattern.MatchString(phone)) {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "电话号码必须是 E.164 格式，例如 +8613800138000",
			Data: nil,
		})
		return
	}

	req := models.UserCreateRequest{
		Username:     request.Username,
		Email:        request.Email,
		Phone:        phone,
		Password:     request.Password,
		FirstName:    request.FirstName,
		LastName:     request.LastName,
		DisplayName:  request.DisplayName,
		PlatformRole: request.PlatformRole,
		Department:   request.Department,
		JobTitle:     request.JobTitle,
		ManagerID:    request.ManagerID,
	}
	user, err := h.adminUserService.CreateUser(c.Request.Context(), &req)
	if err != nil {
		if errors.Is(err, services.ErrAdminUserIdentityConflict) {
			c.JSON(http.StatusConflict, ApiResponse{
				Code: 1,
				Msg:  "用户名或邮箱已被使用（包括已删除账号）",
				Data: nil,
			})
			return
		}
		logHandlerFailure(c, "admin_user.create", err)
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "创建用户失败",
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
// @Router /api/platform/users/{id} [put]
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
	if err := decodeStrictAdminUserJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "请求参数错误",
			Data: nil,
		})
		return
	}
	if req.Phone != nil {
		phone := strings.TrimSpace(*req.Phone)
		if phone != "" && !e164PhonePattern.MatchString(phone) {
			c.JSON(http.StatusBadRequest, ApiResponse{
				Code: 1,
				Msg:  "电话号码必须是 E.164 格式，例如 +8613800138000",
				Data: nil,
			})
			return
		}
		req.Phone = &phone
	}

	user, err := h.adminUserService.UpdateUser(
		c.Request.Context(),
		models.HumanActor(c.GetUint("user_id")),
		uint(userID),
		&req,
	)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, ApiResponse{
				Code: 1,
				Msg:  "用户不存在",
				Data: nil,
			})
			return
		}
		if errors.Is(err, services.ErrAdminUserIdentityConflict) {
			c.JSON(http.StatusConflict, ApiResponse{
				Code: 1,
				Msg:  "用户名或邮箱已被使用（包括已删除账号）",
				Data: nil,
			})
			return
		}
		if errors.Is(err, services.ErrLastActivePlatformAdministrator) {
			c.JSON(http.StatusConflict, ApiResponse{
				Code: 1,
				Msg:  "不能停用或降级最后一个活跃平台管理员",
				Data: nil,
			})
			return
		}
		logHandlerFailure(c, "admin_user.update", err)
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "更新用户信息失败",
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
// @Router /api/platform/users/{id}/reset-password [post]
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
	if err := decodeStrictAdminUserJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, ApiResponse{
			Code: 1,
			Msg:  "请求参数错误",
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
		logHandlerFailure(c, "admin_user.reset_password", err)
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "重置密码失败",
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

func decodeStrictAdminUserJSON(c *gin.Context, target any) error {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return errors.New("request body is required")
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return err
	}
	return binding.Validator.ValidateStruct(target)
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
// @Router /api/platform/users/{id} [delete]
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

	err = h.adminUserService.DeleteUser(
		c.Request.Context(),
		models.HumanActor(c.GetUint("user_id")),
		uint(userID),
	)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, ApiResponse{
				Code: 1,
				Msg:  "用户不存在",
				Data: nil,
			})
			return
		}
		if errors.Is(err, services.ErrLastActivePlatformAdministrator) {
			c.JSON(http.StatusConflict, ApiResponse{
				Code: 1,
				Msg:  "不能删除最后一个活跃平台管理员",
				Data: nil,
			})
			return
		}
		logHandlerFailure(c, "admin_user.delete", err)
		c.JSON(http.StatusInternalServerError, ApiResponse{
			Code: 1,
			Msg:  "删除用户失败",
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
// @Router /api/platform/users/stats [get]
func (h *AdminUserHandler) GetUserStats(c *gin.Context) {
	stats, err := h.adminUserService.GetUserStats(c.Request.Context())
	if err != nil {
		logHandlerFailure(c, "admin_user.get_statistics", err)
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

// 请求和响应结构体

// ResetPasswordRequest 重置密码请求
type ResetPasswordRequest struct {
	NewPassword string `json:"new_password" binding:"required,min=8" example:"newpassword123"`
}
