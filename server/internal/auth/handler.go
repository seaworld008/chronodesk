package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// AuthHandler 认证处理器
type AuthHandler struct {
	authService *AuthService
	logger      Logger
}

// Logger 日志接口
type Logger interface {
	Info(msg string, fields ...interface{})
	Error(msg string, fields ...interface{})
	Warn(msg string, fields ...interface{})
	Debug(msg string, fields ...interface{})
}

// SimpleLogger 简单日志实现
type SimpleLogger struct{}

func (l *SimpleLogger) Info(msg string, fields ...interface{}) {
	fmt.Printf("[INFO] %s %v\n", msg, fields)
}

func (l *SimpleLogger) Error(msg string, fields ...interface{}) {
	fmt.Printf("[ERROR] %s %v\n", msg, fields)
}

func (l *SimpleLogger) Warn(msg string, fields ...interface{}) {
	fmt.Printf("[WARN] %s %v\n", msg, fields)
}

func (l *SimpleLogger) Debug(msg string, fields ...interface{}) {
	fmt.Printf("[DEBUG] %s %v\n", msg, fields)
}

// NewAuthHandler 创建认证处理器
func NewAuthHandler(authService *AuthService, logger Logger) *AuthHandler {
	if logger == nil {
		logger = &SimpleLogger{}
	}
	return &AuthHandler{
		authService: authService,
		logger:      logger,
	}
}

// HTTPContext HTTP上下文接口
type HTTPContext interface {
	GetHeader(key string) string
	SetHeader(key, value string)
	GetQuery(key string) string
	GetParam(key string) string
	Bind(obj interface{}) error
	JSON(code int, obj interface{})
	String(code int, format string, values ...interface{})
	Status(code int)
	Abort()
	Get(key string) (interface{}, bool)
	Set(key string, value interface{})
	ClientIP() string
	UserAgent() string
	Request() *http.Request
	Next()
}

// ErrorResponse 错误响应
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
	Code    string `json:"code,omitempty"`
}

// SuccessResponse 成功响应
type SuccessResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// Register 用户注册
func (h *AuthHandler) Register(c HTTPContext) {
	var req RegisterRequest
	if err := c.Bind(&req); err != nil {
		h.logger.Error("Failed to bind register request", "error", err)
		c.JSON(http.StatusBadRequest, map[string]interface{}{
			"code": 1,
			"msg":  "请求格式无效",
			"data": nil,
		})
		return
	}

	// 验证输入
	if err := h.validateRegisterRequest(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "validation_error",
			Message: err.Error(),
		})
		return
	}

	// 获取客户端信息
	ipAddress := c.ClientIP()
	userAgent := c.UserAgent()

	// 调用认证服务
	ctx := context.Background()
	resp, err := h.authService.Register(ctx, &req, ipAddress, userAgent)
	if err != nil {
		h.logger.Error("Registration failed", "error", err, "email", req.Email)

		message := "注册失败"
		status := http.StatusInternalServerError

		switch err {
		case ErrUserExists:
			message = "该用户已存在"
			status = http.StatusConflict
		case ErrPasswordTooWeak:
			message = "密码强度不符合要求"
			status = http.StatusBadRequest
		default:
			if strings.Contains(err.Error(), "password") {
				message = "密码强度不符合要求"
				status = http.StatusBadRequest
			}
		}

		c.JSON(status, map[string]interface{}{
			"code": 1, // 错误码设为1
			"msg":  message,
			"data": nil,
		})
		return
	}

	h.logger.Info("User registered successfully", "user_id", resp.User.ID, "email", req.Email)

	// 返回成功响应
	c.JSON(http.StatusCreated, map[string]interface{}{
		"code": 0, // 成功码设为0
		"msg":  "注册成功",
		"data": resp,
	})
}

// Login 用户登录
func (h *AuthHandler) Login(c HTTPContext) {
	var req LoginRequest
	if err := c.Bind(&req); err != nil {
		h.logger.Error("Failed to bind login request", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "请求格式无效",
		})
		return
	}

	// 验证输入
	if req.Email == "" || req.Password == "" {
		c.JSON(http.StatusBadRequest, map[string]interface{}{
			"code": 1,
			"msg":  "请输入邮箱和密码",
			"data": nil,
		})
		return
	}

	// 获取客户端信息
	ipAddress := c.ClientIP()
	userAgent := c.UserAgent()

	// 调用认证服务
	ctx := context.Background()
	resp, err := h.authService.Login(ctx, &req, ipAddress, userAgent)
	if err != nil {
		h.logger.Error("Login failed", "error", err, "email", req.Email)

		message := "登录失败"
		status := http.StatusUnauthorized

		switch err {
		case ErrInvalidCredentials:
			message = "邮箱或密码错误"
		case ErrUserNotFound:
			message = "邮箱或密码错误"
		case ErrAccountLocked:
			message = "账号已锁定"
			status = http.StatusForbidden
		case ErrEmailNotVerified:
			message = "邮箱尚未验证"
			status = http.StatusForbidden
		case ErrInvalidOTP:
			message = "OTP 验证码错误"
		default:
			if strings.Contains(err.Error(), "OTP") {
				message = "请输入 OTP 验证码"
				status = http.StatusBadRequest
			} else if strings.Contains(err.Error(), "too many") {
				message = "登录失败次数过多，请稍后重试"
				status = http.StatusTooManyRequests
			}
		}

		c.JSON(status, map[string]interface{}{
			"code": 1, // 错误码设为1
			"msg":  message,
			"data": nil,
		})
		return
	}

	h.logger.Info("User logged in successfully", "user_id", resp.User.ID, "email", req.Email)

	// 设置安全头
	c.SetHeader("X-Auth-Token", resp.AccessToken)

	// 返回成功响应 - 使用ApiResponse格式与前端保持一致
	c.JSON(http.StatusOK, map[string]interface{}{
		"code": 0,
		"msg":  "登录成功",
		"data": resp,
	})
}

// RefreshToken 刷新令牌
func (h *AuthHandler) RefreshToken(c HTTPContext) {
	var req RefreshTokenRequest
	if err := c.Bind(&req); err != nil {
		h.logger.Error("Failed to bind refresh token request", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "请求格式无效",
		})
		return
	}

	if req.RefreshToken == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "validation_error",
			Message: "缺少刷新令牌",
		})
		return
	}

	// 获取客户端信息
	ipAddress := c.ClientIP()
	userAgent := c.UserAgent()

	// 调用认证服务
	ctx := context.Background()
	resp, err := h.authService.RefreshToken(ctx, &req, ipAddress, userAgent)
	if err != nil {
		h.logger.Error("Token refresh failed", "error", err)

		code := "refresh_failed"
		message := "刷新登录令牌失败"
		status := http.StatusUnauthorized

		switch err {
		case ErrInvalidToken:
			code = "invalid_token"
			message = "刷新令牌无效"
		case ErrTokenExpired:
			code = "token_expired"
			message = "刷新令牌已过期"
		case ErrUserNotFound:
			code = "user_not_found"
			message = "未找到用户"
		}

		c.JSON(status, ErrorResponse{
			Error:   code,
			Message: message,
			Code:    code,
		})
		return
	}

	h.logger.Info("Token refreshed successfully", "user_id", resp.User.ID)

	// 返回成功响应
	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "登录令牌刷新成功",
		Data:    resp,
	})
}

// Logout 用户登出
func (h *AuthHandler) Logout(c HTTPContext) {
	// 从头部获取刷新令牌
	refreshToken := c.GetHeader("X-Refresh-Token")
	if refreshToken == "" {
		// 尝试从请求体获取
		var req struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := c.Bind(&req); err == nil {
			refreshToken = req.RefreshToken
		}
	}

	ctx := context.Background()
	if request := c.Request(); request != nil {
		ctx = request.Context()
	}
	if err := h.authService.Logout(ctx, refreshToken); err != nil {
		h.logger.Error("Logout failed", "error", err)
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:   "logout_failed",
			Message: "退出登录失败，请稍后重试",
		})
		return
	}

	h.logger.Info("User logged out successfully")

	// 返回成功响应
	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "退出登录成功",
	})
}

// LogoutAll 登出所有设备
func (h *AuthHandler) LogoutAll(c HTTPContext) {
	// 从上下文获取用户ID
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "unauthorized",
			Message: "用户未登录",
		})
		return
	}

	userIDUint, ok := userID.(uint)
	if !ok {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "internal_error",
			Message: "用户 ID 无效",
		})
		return
	}

	ctx := context.Background()
	if request := c.Request(); request != nil {
		ctx = request.Context()
	}
	if err := h.authService.LogoutAll(ctx, userIDUint); err != nil {
		h.logger.Error("Logout all failed", "error", err, "user_id", userIDUint)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "logout_failed",
			Message: "无法从所有设备退出登录",
		})
		return
	}

	h.logger.Info("User logged out from all devices", "user_id", userIDUint)

	// 返回成功响应
	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "已从所有设备退出登录",
	})
}

// GetProfile 获取用户资料
func (h *AuthHandler) GetProfile(c HTTPContext) {
	// 从上下文获取用户ID
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "unauthorized",
			Message: "用户未登录",
		})
		return
	}

	userIDUint, ok := userID.(uint)
	if !ok {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "internal_error",
			Message: "用户 ID 无效",
		})
		return
	}

	ctx := context.Background()
	user, err := h.authService.userRepo.GetByID(ctx, userIDUint)
	if err != nil {
		h.logger.Error("Failed to get user", "error", err, "user_id", userIDUint)
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error:   "user_not_found",
			Message: "未找到用户",
		})
		return
	}

	profile, _ := h.authService.profileRepo.GetByUserID(ctx, userIDUint)
	userInfo := h.authService.buildUserInfo(user, profile)

	// 返回成功响应
	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Data:    userInfo,
	})
}

// Health 健康检查
func (h *AuthHandler) Health(c HTTPContext) {
	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "认证服务运行正常",
	})
}

// ForgotPassword 忘记密码
func (h *AuthHandler) ForgotPassword(c HTTPContext) {
	var req ForgotPasswordRequest
	if err := c.Bind(&req); err != nil {
		h.logger.Error("Failed to bind forgot password request", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "请求格式无效",
		})
		return
	}

	ctx := context.Background()
	err := h.authService.ForgotPassword(ctx, req.Email)
	if err != nil {
		h.logger.Error("Failed to process forgot password", "error", err, "email", req.Email)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "forgot_password_failed",
			Message: "处理密码重置请求失败",
		})
		return
	}

	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "密码重置邮件已发送",
	})
}

// ResetPassword 重置密码
func (h *AuthHandler) ResetPassword(c HTTPContext) {
	var req ResetPasswordRequest
	if err := c.Bind(&req); err != nil {
		h.logger.Error("Failed to bind reset password request", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "请求格式无效",
		})
		return
	}

	ctx := context.Background()
	err := h.authService.ResetPassword(ctx, req.Token, req.NewPassword)
	if err != nil {
		h.logger.Error("Failed to reset password", "error", err)
		if err == ErrInvalidToken {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:   "invalid_token",
				Message: "密码重置令牌无效或已过期",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "reset_password_failed",
			Message: "重置密码失败",
		})
		return
	}

	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "密码重置成功",
	})
}

// VerifyEmail 验证邮箱
func (h *AuthHandler) VerifyEmail(c HTTPContext) {
	token := c.GetQuery("token")
	if token == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "missing_token",
			Message: "缺少邮箱验证令牌",
		})
		return
	}

	ctx := context.Background()
	err := h.authService.VerifyEmail(ctx, token)
	if err != nil {
		h.logger.Error("Failed to verify email", "error", err)
		if err == ErrInvalidToken {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:   "invalid_token",
				Message: "邮箱验证令牌无效或已过期",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "verification_failed",
			Message: "验证邮箱失败",
		})
		return
	}

	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "邮箱验证成功",
	})
}

// ResendVerification 重发验证邮件
func (h *AuthHandler) ResendVerification(c HTTPContext) {
	var req ResendVerificationRequest
	if err := c.Bind(&req); err != nil {
		h.logger.Error("Failed to bind resend verification request", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "请求格式无效",
		})
		return
	}

	ctx := context.Background()
	err := h.authService.ResendVerification(ctx, req.Email)
	if err != nil {
		h.logger.Error("Failed to resend verification", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "resend_failed",
			Message: "重新发送验证邮件失败",
		})
		return
	}

	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "验证邮件已发送",
	})
}

// UpdateProfile 更新用户资料
func (h *AuthHandler) UpdateProfile(c HTTPContext) {
	userInfo, err := GetUserFromContext(c)
	if err != nil {
		h.logger.Error("Failed to get user from context", "error", err)
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "unauthorized",
			Message: "请先登录",
		})
		return
	}

	var req UpdateProfileRequest
	if err := c.Bind(&req); err != nil {
		h.logger.Error("Failed to bind update profile request", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "请求格式无效",
		})
		return
	}

	ctx := context.Background()
	err = h.authService.UpdateProfile(ctx, userInfo.ID, &req)
	if err != nil {
		h.logger.Error("Failed to update profile", "error", err, "userID", userInfo.ID)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "update_failed",
			Message: "更新个人资料失败",
		})
		return
	}

	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "个人资料更新成功",
	})
}

// ChangePassword 修改密码
func (h *AuthHandler) ChangePassword(c HTTPContext) {
	userInfo, err := GetUserFromContext(c)
	if err != nil {
		h.logger.Error("Failed to get user from context", "error", err)
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "unauthorized",
			Message: "请先登录",
		})
		return
	}

	var req ChangePasswordRequest
	if err := c.Bind(&req); err != nil {
		h.logger.Error("Failed to bind change password request", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "请求格式无效",
		})
		return
	}

	ctx := context.Background()
	err = h.authService.ChangePassword(ctx, userInfo.ID, req.CurrentPassword, req.NewPassword)
	if err != nil {
		h.logger.Error("Failed to change password", "error", err, "userID", userInfo.ID)
		if err == ErrInvalidCredentials {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:   "invalid_password",
				Message: "当前密码错误",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "change_password_failed",
			Message: "修改密码失败",
		})
		return
	}

	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "密码修改成功",
	})
}

// EnableOTP 启用OTP
func (h *AuthHandler) EnableOTP(c HTTPContext) {
	userInfo, err := GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "unauthorized",
			Message: "用户未登录",
		})
		return
	}

	var req EnableOTPRequest
	if err := c.Bind(&req); err != nil {
		h.logger.Error("Failed to bind enable OTP request", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "请求体格式无效",
		})
		return
	}

	ctx := c.Request().Context()
	if ctx == nil {
		ctx = context.Background()
	}

	otpSetup, err := h.authService.EnableOTP(ctx, userInfo.ID, req.Password)
	if err != nil {
		h.logger.Error("Failed to enable OTP", "error", err, "user_id", userInfo.ID)
		status := http.StatusInternalServerError
		message := "启用 OTP 失败"

		if errors.Is(err, ErrInvalidCredentials) {
			status = http.StatusUnauthorized
			message = "密码错误"
		} else if errors.Is(err, ErrUserNotFound) {
			status = http.StatusNotFound
			message = "未找到用户"
		} else if err.Error() == "OTP already enabled" {
			status = http.StatusBadRequest
			message = "OTP 已启用"
		}

		c.JSON(status, ErrorResponse{
			Error:   "enable_otp_failed",
			Message: message,
		})
		return
	}

	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "OTP 启用成功",
		Data:    otpSetup,
	})
}

// DisableOTP 禁用OTP
func (h *AuthHandler) DisableOTP(c HTTPContext) {
	userInfo, err := GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "unauthorized",
			Message: "用户未登录",
		})
		return
	}

	var req struct {
		Password string `json:"password" binding:"required"`
	}
	if err := c.Bind(&req); err != nil {
		h.logger.Error("Failed to bind disable OTP request", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "请求体格式无效",
		})
		return
	}

	ctx := c.Request().Context()
	if ctx == nil {
		ctx = context.Background()
	}

	if err := h.authService.DisableOTP(ctx, userInfo.ID, req.Password); err != nil {
		h.logger.Error("Failed to disable OTP", "error", err, "user_id", userInfo.ID)
		status := http.StatusInternalServerError
		message := "停用 OTP 失败"

		if errors.Is(err, ErrInvalidCredentials) {
			status = http.StatusUnauthorized
			message = "密码错误"
		}

		c.JSON(status, ErrorResponse{
			Error:   "disable_otp_failed",
			Message: message,
		})
		return
	}

	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "OTP 停用成功",
	})
}

// VerifyOTP 验证OTP
func (h *AuthHandler) VerifyOTP(c HTTPContext) {
	userInfo, err := GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "unauthorized",
			Message: "用户未登录",
		})
		return
	}

	var req VerifyOTPRequest
	if err := c.Bind(&req); err != nil {
		h.logger.Error("Failed to bind verify OTP request", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "请求体格式无效",
		})
		return
	}

	ctx := c.Request().Context()
	if ctx == nil {
		ctx = context.Background()
	}

	if err := h.authService.VerifyOTP(ctx, userInfo.ID, req.Code); err != nil {
		h.logger.Error("Failed to verify OTP", "error", err, "user_id", userInfo.ID)
		status := http.StatusUnauthorized
		message := "OTP 验证码错误"

		if errors.Is(err, ErrOTPExpired) {
			status = http.StatusBadRequest
			message = "OTP 验证码已过期"
		} else if !errors.Is(err, ErrInvalidOTP) {
			status = http.StatusInternalServerError
			message = "验证 OTP 失败"
		}

		c.JSON(status, ErrorResponse{
			Error:   "invalid_otp",
			Message: message,
		})
		return
	}

	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "OTP 验证成功",
	})
}

// GenerateBackupCodes 生成备用代码
func (h *AuthHandler) GenerateBackupCodes(c HTTPContext) {
	userInfo, err := GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "unauthorized",
			Message: "用户未登录",
		})
		return
	}

	ctx := c.Request().Context()
	if ctx == nil {
		ctx = context.Background()
	}

	codes, err := h.authService.GenerateBackupCodes(ctx, userInfo.ID)
	if err != nil {
		h.logger.Error("Failed to generate backup codes", "error", err, "user_id", userInfo.ID)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "generate_backup_codes_failed",
			Message: "生成备用验证码失败",
		})
		return
	}

	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "备用验证码生成成功",
		Data: map[string]interface{}{
			"backup_codes": codes,
		},
	})
}

// 验证方法

func (h *AuthHandler) validateRegisterRequest(req *RegisterRequest) error {
	if req.Username == "" {
		return fmt.Errorf("请输入用户名")
	}
	if len(req.Username) < 3 || len(req.Username) > 50 {
		return fmt.Errorf("用户名长度必须为 3 到 50 个字符")
	}
	if !IsValidUsername(req.Username) {
		return fmt.Errorf("用户名包含无效字符")
	}

	if req.Email == "" {
		return fmt.Errorf("请输入邮箱")
	}
	if !IsValidEmail(req.Email) {
		return fmt.Errorf("邮箱格式无效")
	}

	if req.Password == "" {
		return fmt.Errorf("请输入密码")
	}
	if len(req.Password) < 8 {
		return fmt.Errorf("密码至少需要 8 个字符")
	}

	if req.ConfirmPassword == "" {
		return fmt.Errorf("请再次输入密码")
	}
	if req.Password != req.ConfirmPassword {
		return fmt.Errorf("两次输入的密码不一致")
	}

	// 清理输入
	req.Username = SanitizeInput(req.Username)
	req.Email = SanitizeInput(req.Email)
	req.FirstName = SanitizeInput(req.FirstName)
	req.LastName = SanitizeInput(req.LastName)
	req.Department = SanitizeInput(req.Department)
	req.Position = SanitizeInput(req.Position)

	return nil
}

// 辅助方法

// GetUserFromContext 从上下文获取用户信息
func GetUserFromContext(c HTTPContext) (*UserInfo, error) {
	userID, exists := c.Get("user_id")
	if !exists {
		return nil, fmt.Errorf("user not authenticated")
	}

	userIDUint, ok := userID.(uint)
	if !ok {
		return nil, fmt.Errorf("invalid user ID")
	}

	roleValue, exists := c.Get("user_role_enum")
	if !exists {
		roleValue, exists = c.Get("user_role")
	}
	if !exists {
		return nil, fmt.Errorf("user role not found")
	}

	var userRole UserRole
	switch v := roleValue.(type) {
	case UserRole:
		userRole = v
	case string:
		userRole = UserRole(v)
	default:
		return nil, fmt.Errorf("invalid user role")
	}

	return &UserInfo{
		ID:   userIDUint,
		Role: userRole,
	}, nil
}

// RequireAuth 认证中间件
func (h *AuthHandler) RequireAuth(c HTTPContext) {
	// 获取Authorization头
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "missing_token",
			Message: "缺少访问令牌",
		})
		c.Abort()
		return
	}

	// 解析Bearer令牌
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "invalid_token_format",
			Message: "认证请求头格式无效",
		})
		c.Abort()
		return
	}

	token := parts[1]

	if h.authService == nil || h.authService.jwtManager == nil {
		h.logger.Error("Authentication token verifier is unavailable")
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:   "authentication_unavailable",
			Message: "认证服务暂时不可用",
		})
		c.Abort()
		return
	}

	// 验证令牌
	claims, err := h.authService.jwtManager.VerifyAccessToken(token)
	if err != nil {
		h.logger.Error("Token verification failed", "error", err)
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "invalid_token",
			Message: "访问令牌无效或已过期",
		})
		c.Abort()
		return
	}

	// JWT 中的角色只是签发时快照。每次请求都必须重新读取当前用户，
	// 以便停用、删除、锁定、降权和修改密码能够立即使旧访问令牌失效。
	// 这对管理员及 Agent 控制面尤其重要，不能依赖最长可存活数小时的角色声明。
	if h.authService.userRepo == nil {
		h.logger.Error("Authentication principal repository is unavailable")
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:   "authentication_unavailable",
			Message: "认证服务暂时不可用",
		})
		c.Abort()
		return
	}

	requestContext := context.Background()
	if request := c.Request(); request != nil {
		requestContext = request.Context()
	}
	currentUser, err := h.authService.userRepo.GetByID(requestContext, claims.UserID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			h.logger.Warn("Access token principal is no longer available", "user_id", claims.UserID)
			c.JSON(http.StatusUnauthorized, ErrorResponse{
				Error:   "invalid_token",
				Message: "访问令牌对应的用户已失效",
			})
		} else {
			h.logger.Error("Failed to revalidate access token principal", "error", err, "user_id", claims.UserID)
			c.JSON(http.StatusServiceUnavailable, ErrorResponse{
				Error:   "authentication_unavailable",
				Message: "认证服务暂时不可用",
			})
		}
		c.Abort()
		return
	}
	if currentUser.Status != StatusActive {
		h.logger.Warn("Access token principal is not active", "user_id", claims.UserID, "status", currentUser.Status)
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "account_inactive",
			Message: "账号当前不可用，请重新登录或联系管理员",
		})
		c.Abort()
		return
	}
	if currentUser.Role != claims.Role {
		h.logger.Warn(
			"Access token role is stale",
			"user_id", claims.UserID,
			"token_role", claims.Role,
			"current_role", currentUser.Role,
		)
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "stale_token",
			Message: "账号权限已变更，请重新登录",
		})
		c.Abort()
		return
	}
	if currentUser.PasswordChangedAt != nil && claims.Iat < currentUser.PasswordChangedAt.Unix() {
		h.logger.Warn("Access token predates password change", "user_id", claims.UserID)
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "stale_token",
			Message: "密码已变更，请重新登录",
		})
		c.Abort()
		return
	}

	// JWT 签名只证明令牌曾由本服务签发。会话是否仍有效必须以数据库为
	// 权威，这样单设备退出、全部退出和管理员踢下线都能立即撤销 access token。
	if h.authService.tokenRepo == nil {
		h.logger.Error("Authentication session repository is unavailable")
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:   "authentication_unavailable",
			Message: "认证服务暂时不可用",
		})
		c.Abort()
		return
	}
	sessionActive, err := h.authService.tokenRepo.IsSessionActive(
		requestContext,
		claims.UserID,
		claims.SessionID,
	)
	if err != nil {
		h.logger.Error(
			"Failed to revalidate access token session",
			"error", err,
			"user_id", claims.UserID,
			"session_id", claims.SessionID,
		)
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:   "authentication_unavailable",
			Message: "认证服务暂时不可用",
		})
		c.Abort()
		return
	}
	if !sessionActive {
		h.logger.Warn(
			"Access token session is no longer active",
			"user_id", claims.UserID,
			"session_id", claims.SessionID,
		)
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "session_revoked",
			Message: "登录会话已结束，请重新登录",
		})
		c.Abort()
		return
	}

	// 设置用户信息到上下文
	c.Set("user_id", currentUser.ID)
	c.Set("user_role", string(currentUser.Role))
	c.Set("user_role_enum", currentUser.Role)
	c.Set("token_jti", claims.Jti)
	c.Set("session_id", claims.SessionID)

	// 继续处理
	c.Next()
}

// RequireRole 角色权限中间件
func (h *AuthHandler) RequireRole(requiredRole UserRole) func(HTTPContext) {
	return func(c HTTPContext) {
		roleValue, exists := c.Get("user_role_enum")
		if !exists {
			roleValue, exists = c.Get("user_role")
		}
		if !exists {
			c.JSON(http.StatusForbidden, ErrorResponse{
				Error:   "access_denied",
				Message: "无权访问",
			})
			c.Abort()
			return
		}

		var userRole UserRole
		switch v := roleValue.(type) {
		case UserRole:
			userRole = v
		case string:
			userRole = UserRole(v)
		default:
			c.JSON(http.StatusForbidden, ErrorResponse{
				Error:   "access_denied",
				Message: "无权访问",
			})
			c.Abort()
			return
		}

		// 检查权限
		user := &User{Role: userRole}
		if !user.HasPermission(requiredRole) {
			c.JSON(http.StatusForbidden, ErrorResponse{
				Error:   "insufficient_permissions",
				Message: "权限不足",
			})
			c.Abort()
			return
		}

		// 继续处理
		c.Next()
	}
}

// ParseUserID 解析用户ID参数
func ParseUserID(c HTTPContext) (uint, error) {
	userIDStr := c.GetParam("id")
	if userIDStr == "" {
		userIDStr = c.GetQuery("user_id")
	}

	if userIDStr == "" {
		return 0, fmt.Errorf("user ID is required")
	}

	userID, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid user ID format")
	}

	return uint(userID), nil
}
