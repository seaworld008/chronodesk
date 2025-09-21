package auth

import (
	"context"
	"encoding/json"
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
			"msg":  "Invalid request format",
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

		message := "Registration failed"
		status := http.StatusInternalServerError

		switch err {
		case ErrUserExists:
			message = "User already exists"
			status = http.StatusConflict
		case ErrPasswordTooWeak:
			message = err.Error()
			status = http.StatusBadRequest
		default:
			if strings.Contains(err.Error(), "password") {
				message = err.Error()
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
		"msg":  "Registration successful",
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
			Message: "Invalid request format",
		})
		return
	}

	// 验证输入
	if req.Email == "" || req.Password == "" {
		c.JSON(http.StatusBadRequest, map[string]interface{}{
			"code": 1,
			"msg":  "Email and password are required",
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

		message := "Login failed"
		status := http.StatusUnauthorized

		switch err {
		case ErrInvalidCredentials:
			message = "Invalid email or password"
		case ErrUserNotFound:
			message = "Invalid email or password"
		case ErrAccountLocked:
			message = "Account is locked"
			status = http.StatusForbidden
		case ErrEmailNotVerified:
			message = "Email not verified"
			status = http.StatusForbidden
		case ErrInvalidOTP:
			message = "Invalid OTP code"
		default:
			if strings.Contains(err.Error(), "OTP") {
				message = "OTP code required"
				status = http.StatusBadRequest
			} else if strings.Contains(err.Error(), "too many") {
				message = "Too many failed login attempts"
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
		"msg":  "Login successful",
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
			Message: "Invalid request format",
		})
		return
	}

	if req.RefreshToken == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "validation_error",
			Message: "Refresh token is required",
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
		message := "Token refresh failed"
		status := http.StatusUnauthorized

		switch err {
		case ErrInvalidToken:
			code = "invalid_token"
			message = "Invalid refresh token"
		case ErrTokenExpired:
			code = "token_expired"
			message = "Refresh token expired"
		case ErrUserNotFound:
			code = "user_not_found"
			message = "User not found"
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
		Message: "Token refreshed successfully",
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
	if err := h.authService.Logout(ctx, refreshToken); err != nil {
		h.logger.Error("Logout failed", "error", err)
	}

	h.logger.Info("User logged out successfully")

	// 返回成功响应
	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "Logout successful",
	})
}

// LogoutAll 登出所有设备
func (h *AuthHandler) LogoutAll(c HTTPContext) {
	// 从上下文获取用户ID
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "unauthorized",
			Message: "User not authenticated",
		})
		return
	}

	userIDUint, ok := userID.(uint)
	if !ok {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "internal_error",
			Message: "Invalid user ID",
		})
		return
	}

	ctx := context.Background()
	if err := h.authService.LogoutAll(ctx, userIDUint); err != nil {
		h.logger.Error("Logout all failed", "error", err, "user_id", userIDUint)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "logout_failed",
			Message: "Failed to logout from all devices",
		})
		return
	}

	h.logger.Info("User logged out from all devices", "user_id", userIDUint)

	// 返回成功响应
	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "Logged out from all devices successfully",
	})
}

// GetProfile 获取用户资料
func (h *AuthHandler) GetProfile(c HTTPContext) {
	// 从上下文获取用户ID
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "unauthorized",
			Message: "User not authenticated",
		})
		return
	}

	userIDUint, ok := userID.(uint)
	if !ok {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "internal_error",
			Message: "Invalid user ID",
		})
		return
	}

	ctx := context.Background()
	user, err := h.authService.userRepo.GetByID(ctx, userIDUint)
	if err != nil {
		h.logger.Error("Failed to get user", "error", err, "user_id", userIDUint)
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error:   "user_not_found",
			Message: "User not found",
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
		Message: "Auth service is healthy",
	})
}

// ForgotPassword 忘记密码
func (h *AuthHandler) ForgotPassword(c HTTPContext) {
	var req ForgotPasswordRequest
	if err := c.Bind(&req); err != nil {
		h.logger.Error("Failed to bind forgot password request", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "Invalid request format",
		})
		return
	}

	ctx := context.Background()
	err := h.authService.ForgotPassword(ctx, req.Email)
	if err != nil {
		h.logger.Error("Failed to process forgot password", "error", err, "email", req.Email)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "forgot_password_failed",
			Message: "Failed to process password reset request",
		})
		return
	}

	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "Password reset email sent",
	})
}

// ResetPassword 重置密码
func (h *AuthHandler) ResetPassword(c HTTPContext) {
	var req ResetPasswordRequest
	if err := c.Bind(&req); err != nil {
		h.logger.Error("Failed to bind reset password request", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "Invalid request format",
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
				Message: "Invalid or expired reset token",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "reset_password_failed",
			Message: "Failed to reset password",
		})
		return
	}

	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "Password reset successfully",
	})
}

// VerifyEmail 验证邮箱
func (h *AuthHandler) VerifyEmail(c HTTPContext) {
	token := c.GetQuery("token")
	if token == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "missing_token",
			Message: "Verification token is required",
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
				Message: "Invalid or expired verification token",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "verification_failed",
			Message: "Failed to verify email",
		})
		return
	}

	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "Email verified successfully",
	})
}

// ResendVerification 重发验证邮件
func (h *AuthHandler) ResendVerification(c HTTPContext) {
	var req ResendVerificationRequest
	if err := c.Bind(&req); err != nil {
		h.logger.Error("Failed to bind resend verification request", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "Invalid request format",
		})
		return
	}

	ctx := context.Background()
	err := h.authService.ResendVerification(ctx, req.Email)
	if err != nil {
		h.logger.Error("Failed to resend verification", "error", err)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "resend_failed",
			Message: "Failed to resend verification email",
		})
		return
	}

	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "Verification email sent",
	})
}

// UpdateProfile 更新用户资料
func (h *AuthHandler) UpdateProfile(c HTTPContext) {
	userInfo, err := GetUserFromContext(c)
	if err != nil {
		h.logger.Error("Failed to get user from context", "error", err)
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "unauthorized",
			Message: "Authentication required",
		})
		return
	}

	var req UpdateProfileRequest
	if err := c.Bind(&req); err != nil {
		h.logger.Error("Failed to bind update profile request", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "Invalid request format",
		})
		return
	}

	ctx := context.Background()
	err = h.authService.UpdateProfile(ctx, userInfo.ID, &req)
	if err != nil {
		h.logger.Error("Failed to update profile", "error", err, "userID", userInfo.ID)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "update_failed",
			Message: "Failed to update profile",
		})
		return
	}

	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "Profile updated successfully",
	})
}

// ChangePassword 修改密码
func (h *AuthHandler) ChangePassword(c HTTPContext) {
	userInfo, err := GetUserFromContext(c)
	if err != nil {
		h.logger.Error("Failed to get user from context", "error", err)
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "unauthorized",
			Message: "Authentication required",
		})
		return
	}

	var req ChangePasswordRequest
	if err := c.Bind(&req); err != nil {
		h.logger.Error("Failed to bind change password request", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "Invalid request format",
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
				Message: "Current password is incorrect",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "change_password_failed",
			Message: "Failed to change password",
		})
		return
	}

	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "Password changed successfully",
	})
}

// EnableOTP 启用OTP
func (h *AuthHandler) EnableOTP(c HTTPContext) {
	userInfo, err := GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "unauthorized",
			Message: "User not authenticated",
		})
		return
	}

	var req EnableOTPRequest
	if err := c.Bind(&req); err != nil {
		h.logger.Error("Failed to bind enable OTP request", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "Invalid request body",
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
		message := "Failed to enable OTP"

		if errors.Is(err, ErrInvalidCredentials) {
			status = http.StatusUnauthorized
			message = "Invalid password"
		} else if errors.Is(err, ErrUserNotFound) {
			status = http.StatusNotFound
			message = "User not found"
		} else if err.Error() == "OTP already enabled" {
			status = http.StatusBadRequest
			message = "OTP already enabled"
		}

		c.JSON(status, ErrorResponse{
			Error:   "enable_otp_failed",
			Message: message,
		})
		return
	}

	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "OTP enabled successfully",
		Data:    otpSetup,
	})
}

// DisableOTP 禁用OTP
func (h *AuthHandler) DisableOTP(c HTTPContext) {
	userInfo, err := GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "unauthorized",
			Message: "User not authenticated",
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
			Message: "Invalid request body",
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
		message := "Failed to disable OTP"

		if errors.Is(err, ErrInvalidCredentials) {
			status = http.StatusUnauthorized
			message = "Invalid password"
		}

		c.JSON(status, ErrorResponse{
			Error:   "disable_otp_failed",
			Message: message,
		})
		return
	}

	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "OTP disabled successfully",
	})
}

// VerifyOTP 验证OTP
func (h *AuthHandler) VerifyOTP(c HTTPContext) {
	userInfo, err := GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "unauthorized",
			Message: "User not authenticated",
		})
		return
	}

	var req VerifyOTPRequest
	if err := c.Bind(&req); err != nil {
		h.logger.Error("Failed to bind verify OTP request", "error", err)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "Invalid request body",
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
		message := "Invalid OTP code"

		if errors.Is(err, ErrOTPExpired) {
			status = http.StatusBadRequest
			message = "OTP code expired"
		} else if !errors.Is(err, ErrInvalidOTP) {
			status = http.StatusInternalServerError
			message = "Failed to verify OTP"
		}

		c.JSON(status, ErrorResponse{
			Error:   "invalid_otp",
			Message: message,
		})
		return
	}

	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "OTP verified successfully",
	})
}

// GenerateBackupCodes 生成备用代码
func (h *AuthHandler) GenerateBackupCodes(c HTTPContext) {
	userInfo, err := GetUserFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "unauthorized",
			Message: "User not authenticated",
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
			Message: "Failed to generate backup codes",
		})
		return
	}

	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "Backup codes generated successfully",
		Data: map[string]interface{}{
			"backup_codes": codes,
		},
	})
}

// 验证方法

func (h *AuthHandler) validateRegisterRequest(req *RegisterRequest) error {
	if req.Username == "" {
		return fmt.Errorf("username is required")
	}
	if len(req.Username) < 3 || len(req.Username) > 50 {
		return fmt.Errorf("username must be between 3 and 50 characters")
	}
	if !IsValidUsername(req.Username) {
		return fmt.Errorf("username contains invalid characters")
	}

	if req.Email == "" {
		return fmt.Errorf("email is required")
	}
	if !IsValidEmail(req.Email) {
		return fmt.Errorf("invalid email format")
	}

	if req.Password == "" {
		return fmt.Errorf("password is required")
	}
	if len(req.Password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}

	if req.ConfirmPassword == "" {
		return fmt.Errorf("password confirmation is required")
	}
	if req.Password != req.ConfirmPassword {
		return fmt.Errorf("passwords do not match")
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
			Message: "Authorization token is required",
		})
		c.Abort()
		return
	}

	// 解析Bearer令牌
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "invalid_token_format",
			Message: "Invalid authorization header format",
		})
		c.Abort()
		return
	}

	token := parts[1]

	// 验证令牌
	claims, err := h.authService.jwtManager.VerifyAccessToken(token)
	if err != nil {
		h.logger.Error("Token verification failed", "error", err)
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "invalid_token",
			Message: "Invalid or expired token",
		})
		c.Abort()
		return
	}

	// 设置用户信息到上下文
	c.Set("user_id", claims.UserID)
	c.Set("user_role", string(claims.Role))
	c.Set("user_role_enum", claims.Role)
	c.Set("token_jti", claims.Jti)

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
				Message: "Access denied",
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
				Message: "Access denied",
			})
			c.Abort()
			return
		}

		// 检查权限
		user := &User{Role: userRole}
		if !user.HasPermission(requiredRole) {
			c.JSON(http.StatusForbidden, ErrorResponse{
				Error:   "insufficient_permissions",
				Message: "Insufficient permissions",
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

// ListUsers 获取用户列表（管理员功能）
func (h *AuthHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	// 解析查询参数
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}

	// 这里应该调用用户服务获取用户列表
	// users, total, err := h.userService.ListUsers(r.Context(), page, limit)
	// 暂时返回空列表
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"users":   []interface{}{},
		"total":   0,
		"page":    page,
		"limit":   limit,
	})
}

// GetUser 获取单个用户信息（管理员功能）
func (h *AuthHandler) GetUser(w http.ResponseWriter, r *http.Request) {
	userIDStr := r.URL.Query().Get("id")
	if userIDStr == "" {
		http.Error(w, "User ID required", http.StatusBadRequest)
		return
	}

	_, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	// 这里应该调用用户服务获取用户信息
	// user, err := h.userService.GetUser(r.Context(), uint(userID))
	// 暂时返回空用户
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"user":    nil,
	})
}

// UpdateUser 更新用户信息（管理员功能）
func (h *AuthHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID       uint   `json:"id"`
		Username string `json:"username"`
		Email    string `json:"email"`
		Role     string `json:"role"`
		Status   string `json:"status"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Error("Failed to decode update user request", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// 这里应该调用用户服务更新用户
	// err := h.userService.UpdateUser(r.Context(), &req)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "User updated successfully",
	})
}

// DeleteUser 删除用户（管理员功能）
func (h *AuthHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	userIDStr := r.URL.Query().Get("id")
	if userIDStr == "" {
		http.Error(w, "User ID required", http.StatusBadRequest)
		return
	}

	_, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	// 这里应该调用用户服务删除用户
	// err := h.userService.DeleteUser(r.Context(), uint(userID))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "User deleted successfully",
	})
}

// DisableUser 禁用用户（管理员功能）
func (h *AuthHandler) DisableUser(w http.ResponseWriter, r *http.Request) {
	userIDStr := r.URL.Query().Get("id")
	if userIDStr == "" {
		http.Error(w, "User ID required", http.StatusBadRequest)
		return
	}

	_, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	// 这里应该调用用户服务禁用用户
	// err := h.userService.DisableUser(r.Context(), uint(userID))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "User disabled successfully",
	})
}

// EnableUser 启用用户（管理员功能）
func (h *AuthHandler) EnableUser(w http.ResponseWriter, r *http.Request) {
	userIDStr := r.URL.Query().Get("id")
	if userIDStr == "" {
		http.Error(w, "User ID required", http.StatusBadRequest)
		return
	}

	_, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	// 这里应该调用用户服务启用用户
	// err := h.userService.EnableUser(r.Context(), uint(userID))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "User enabled successfully",
	})
}

// GetLoginAttempts 获取登录尝试记录（管理员功能）
func (h *AuthHandler) GetLoginAttempts(w http.ResponseWriter, r *http.Request) {
	// 解析查询参数
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}

	// 这里应该调用认证服务获取登录尝试记录
	// attempts, total, err := h.authService.GetLoginAttempts(r.Context(), page, limit)
	// 暂时返回空列表
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"attempts": []interface{}{},
		"total":    0,
		"page":     page,
		"limit":    limit,
	})
}

// LoggingMiddleware 日志中间件
func (h *AuthHandler) LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := context.Background()
		h.logger.Info("Request started",
			"method", r.Method,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
			"user_agent", r.UserAgent(),
		)
		next.ServeHTTP(w, r)
		h.logger.Info("Request completed",
			"method", r.Method,
			"path", r.URL.Path,
			"start", start,
		)
	})
}

// RecoveryMiddleware 恢复中间件
func (h *AuthHandler) RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				h.logger.Error("Panic recovered",
					"error", err,
					"method", r.Method,
					"path", r.URL.Path,
				)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// CORSMiddleware CORS中间件
func (h *AuthHandler) CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// SecurityMiddleware 安全中间件
func (h *AuthHandler) SecurityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 设置安全头
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		w.Header().Set("Content-Security-Policy", "default-src 'self'")

		next.ServeHTTP(w, r)
	})
}
