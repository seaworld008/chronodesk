package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/observability"
)

// AuthHandler 认证处理器
type AuthHandler struct {
	authService          *AuthService
	logger               Logger
	secureAuthCookies    bool
	allowedBrowserOrigin string
	requestTimeout       time.Duration
}

const (
	trustedDeviceCookieName = "chronodesk_trusted_device"
	trustedDeviceCookiePath = "/api/auth/login"
	refreshTokenCookieName  = "chronodesk_refresh_token"
	refreshTokenCookiePath  = "/api/auth"
	humanSessionIDHeader    = "X-Chronodesk-Session-ID"
	// statusClientClosedRequest follows the established reverse-proxy convention
	// for a request whose client has already canceled its context. net/http does
	// not define this non-standard status, so keep it local to the HTTP adapter.
	statusClientClosedRequest = 499
	defaultAuthRequestTimeout = 10 * time.Second
)

// AuthHandlerOption 配置认证 HTTP 适配器。
type AuthHandlerOption func(*AuthHandler)

// WithSecureTrustedDeviceCookie 强制可信设备 Cookie 使用 Secure 属性。
// 生产环境必须启用；TLS 直连请求即使未配置也会自动启用。
func WithSecureTrustedDeviceCookie(secure bool) AuthHandlerOption {
	return WithSecureAuthCookies(secure)
}

// WithSecureAuthCookies 强制所有浏览器认证 Cookie 使用 Secure 属性。
// 生产环境必须启用；TLS 直连请求即使未配置也会自动启用。
func WithSecureAuthCookies(secure bool) AuthHandlerOption {
	return func(handler *AuthHandler) {
		handler.secureAuthCookies = secure
	}
}

// WithAllowedBrowserOrigin configures the one deployment-owned Web origin
// allowed to use refresh-cookie authenticated endpoints. Invalid or empty
// values deliberately leave the handler fail closed.
func WithAllowedBrowserOrigin(origin string) AuthHandlerOption {
	return func(handler *AuthHandler) {
		normalized, err := normalizeBrowserOrigin(origin)
		if err != nil {
			handler.allowedBrowserOrigin = ""
			return
		}
		handler.allowedBrowserOrigin = normalized
	}
}

func browserOriginFromApplicationWebURL(raw string) (string, error) {
	parsed, err := parseApplicationWebURL(raw)
	if err != nil {
		return "", err
	}
	return normalizeBrowserOrigin(parsed.Scheme + "://" + parsed.Host)
}

func normalizeBrowserOrigin(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse browser origin: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if (scheme != "http" && scheme != "https") ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.Opaque != "" ||
		parsed.Path != "" ||
		parsed.RawPath != "" ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return "", errors.New("browser origin must be an absolute HTTP(S) origin")
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" {
		return "", errors.New("browser origin host is required")
	}
	port := parsed.Port()
	if (scheme == "http" && port == "80") ||
		(scheme == "https" && port == "443") {
		port = ""
	}
	authority := hostname
	if port != "" {
		authority = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		authority = "[" + hostname + "]"
	}
	return scheme + "://" + authority, nil
}

// BrowserOriginAllowed applies the exact browser-origin policy shared by
// authentication handlers and route middleware. Both the configured origin
// and the request Origin must be one canonical absolute HTTP(S) origin.
func BrowserOriginAllowed(request *http.Request, allowedOrigin string) bool {
	if request == nil {
		return false
	}
	normalizedAllowedOrigin, err := normalizeBrowserOrigin(allowedOrigin)
	if err != nil {
		return false
	}
	origins := request.Header.Values("Origin")
	if len(origins) != 1 {
		return false
	}
	origin, err := normalizeBrowserOrigin(origins[0])
	return err == nil && origin == normalizedAllowedOrigin
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

func authLogRequestID(c HTTPContext) string {
	if c == nil {
		return "request-unavailable"
	}
	if value, exists := c.Get("request_id"); exists {
		if requestID, ok := value.(string); ok && requestID != "" {
			return observability.SafeLogValue(requestID)
		}
	}
	if requestID := c.GetHeader("X-Request-ID"); requestID != "" {
		return observability.SafeLogValue(requestID)
	}
	return "request-unavailable"
}

func authLogUserID(userID uint) string {
	return observability.SafeLogValue(strconv.FormatUint(uint64(userID), 10))
}

func backupCodeSecurityLogRequestID(HTTPContext) string {
	// Request IDs may be client supplied. This endpoint handles a password and
	// newly generated recovery credentials, so it never writes that open value
	// to operational logs. The durable security audit retains separately
	// sanitized request/correlation metadata.
	return "security-sensitive-request"
}

// authLogReason intentionally returns only a bounded, predefined category.
// Authentication service errors may include password-policy input, OTP/token
// state, or database values and must never be written verbatim.
func authLogReason(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.Canceled):
		return "request_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "request_deadline_exceeded"
	case errors.Is(err, ErrInvalidCredentials):
		return "invalid_credentials"
	case errors.Is(err, ErrUserNotFound):
		return "user_not_found"
	case errors.Is(err, ErrUserExists):
		return "user_exists"
	case errors.Is(err, ErrInvalidToken):
		return "invalid_token"
	case errors.Is(err, ErrTokenExpired):
		return "token_expired"
	case errors.Is(err, ErrInvalidOTP):
		return "invalid_otp"
	case errors.Is(err, ErrOTPExpired):
		return "otp_expired"
	case errors.Is(err, ErrInvalidPassword):
		return "invalid_password"
	case errors.Is(err, ErrOTPNotEnabled):
		return "otp_not_enabled"
	case errors.Is(err, ErrBackupCodesChanged):
		return "backup_codes_changed"
	case errors.Is(err, ErrAtomicBackupCodeRotationUnavailable):
		return "atomic_backup_code_rotation_unavailable"
	case errors.Is(err, ErrAtomicRegistrationUnavailable):
		return "atomic_registration_unavailable"
	case errors.Is(err, ErrEmailNotVerified):
		return "email_not_verified"
	case errors.Is(err, ErrEmailVerificationPolicyUnavailable):
		return "email_verification_policy_unavailable"
	case errors.Is(err, ErrEmailVerificationPolicyChanged):
		return "email_verification_policy_changed"
	case errors.Is(err, ErrAccountLocked):
		return "account_locked"
	case errors.Is(err, ErrPasswordTooWeak):
		return "password_too_weak"
	default:
		return "internal_error"
	}
}

// abortTerminatedReadRequest handles only errors caused by this request's own
// canceled context. A database error that merely races with cancellation, or a
// write request, must continue through the normal fail-closed 503 path.
func (h *AuthHandler) abortTerminatedReadRequest(c HTTPContext, err error) bool {
	if c == nil || err == nil {
		return false
	}
	request := c.Request()
	if request == nil {
		return false
	}
	switch request.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
	default:
		return false
	}

	contextErr := request.Context().Err()
	status := 0
	reason := ""
	switch {
	case errors.Is(contextErr, context.Canceled) && errors.Is(err, context.Canceled):
		status = statusClientClosedRequest
		reason = "request_canceled"
	case errors.Is(contextErr, context.DeadlineExceeded) && errors.Is(err, context.DeadlineExceeded):
		status = http.StatusRequestTimeout
		reason = "request_deadline_exceeded"
	default:
		return false
	}

	h.logger.Debug(
		"Authentication request ended before revalidation completed",
		"request_id", authLogRequestID(c),
		"reason", reason,
	)
	if !authResponseWritten(c) {
		// Record a meaningful terminal status without attempting a JSON body on
		// a connection whose request context is already done.
		c.Status(status)
	}
	c.Abort()
	return true
}

func authResponseWritten(c HTTPContext) bool {
	ginContext, ok := c.(*GinHTTPContext)
	return ok && ginContext.ginCtx.Writer.Written()
}

func rejectOversizedAuthenticationRequest(
	c HTTPContext,
	err error,
) bool {
	if !errors.Is(err, ErrAuthenticationRequestBodyTooLarge) {
		return false
	}
	c.JSON(http.StatusRequestEntityTooLarge, ErrorResponse{
		Error:   "request_too_large",
		Message: "认证请求体超过允许的大小",
		Code:    "request_too_large",
	})
	c.Abort()
	return true
}

func (h *AuthHandler) boundedRequestContext(
	c HTTPContext,
) (context.Context, context.CancelFunc, bool) {
	if c == nil || c.Request() == nil {
		h.logger.Error("Authentication request context is unavailable")
		if c != nil {
			c.JSON(http.StatusServiceUnavailable, ErrorResponse{
				Error:   "authentication_unavailable",
				Message: "认证服务暂时不可用",
			})
			c.Abort()
		}
		return nil, func() {}, false
	}
	timeout := h.requestTimeout
	if timeout <= 0 {
		timeout = defaultAuthRequestTimeout
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), timeout)
	return ctx, cancel, true
}

// NewAuthHandler 创建认证处理器
func NewAuthHandler(authService *AuthService, logger Logger, options ...AuthHandlerOption) *AuthHandler {
	if logger == nil {
		logger = &SimpleLogger{}
	}
	handler := &AuthHandler{
		authService:    authService,
		logger:         logger,
		requestTimeout: defaultAuthRequestTimeout,
	}
	for _, option := range options {
		if option != nil {
			option(handler)
		}
	}
	return handler
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
	SetCookie(cookie *http.Cookie)
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

func (h *AuthHandler) requireAllowedBrowserOrigin(c HTTPContext) bool {
	if !BrowserOriginAllowed(c.Request(), h.allowedBrowserOrigin) {
		h.rejectBrowserOrigin(c)
		return false
	}
	return true
}

func (h *AuthHandler) rejectBrowserOrigin(c HTTPContext) {
	h.logger.Warn(
		"Rejected browser authentication request from an untrusted origin",
		"request_id", authLogRequestID(c),
		"reason", "origin_not_allowed",
	)
	c.JSON(http.StatusForbidden, ErrorResponse{
		Error:   "origin_not_allowed",
		Message: "浏览器来源不受信任",
		Code:    "origin_not_allowed",
	})
	c.Abort()
}

// RequireAllowedBrowserOrigin is the route-middleware projection of the same
// exact Origin check used inside every browser authentication handler.
func (h *AuthHandler) RequireAllowedBrowserOrigin(c HTTPContext) {
	if !h.requireAllowedBrowserOrigin(c) {
		return
	}
	c.Next()
}

// rejectLegacyRefreshCredentials ensures the browser refresh credential has
// exactly one transport: the HttpOnly Cookie. Even an empty legacy header or
// an empty JSON object is rejected so callers cannot accidentally retain a
// bearer-copy compatibility path.
func rejectLegacyRefreshCredentials(c HTTPContext) bool {
	request := c.Request()
	if request == nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "认证请求格式无效",
		})
		return true
	}
	if len(request.Header.Values("X-Refresh-Token")) != 0 {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "刷新凭据只能通过安全 Cookie 提交",
		})
		return true
	}
	if request.URL != nil {
		if _, exists := request.URL.Query()["refresh_token"]; exists {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:   "invalid_request",
				Message: "刷新凭据只能通过安全 Cookie 提交",
			})
			return true
		}
	}
	if request.Body == nil || request.Body == http.NoBody {
		return false
	}
	if request.ContentLength > maxAuthenticationJSONBodyBytes {
		return rejectOversizedAuthenticationRequest(
			c,
			ErrAuthenticationRequestBodyTooLarge,
		)
	}
	body, err := io.ReadAll(io.LimitReader(
		request.Body,
		maxAuthenticationJSONBodyBytes+1,
	))
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "认证请求格式无效",
		})
		return true
	}
	if int64(len(body)) > maxAuthenticationJSONBodyBytes {
		return rejectOversizedAuthenticationRequest(
			c,
			ErrAuthenticationRequestBodyTooLarge,
		)
	}
	if len(body) != 0 {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "刷新凭据只能通过安全 Cookie 提交",
		})
		return true
	}
	return false
}

type refreshTokenCookieState uint8

const (
	refreshTokenCookieAbsent refreshTokenCookieState = iota
	refreshTokenCookieValid
	refreshTokenCookieInvalid
)

// RefreshRateLimitIdentity is a cryptographically verified, non-authorizing
// projection used only to keep one Human refresh session in one rate-limit
// bucket across token rotation. It must never be treated as authenticated
// request state; AuthService still performs the authoritative repository and
// session checks inside RefreshToken and Logout.
type RefreshRateLimitIdentity struct {
	UserID    uint
	SessionID string
}

func readRefreshTokenCookie(
	c HTTPContext,
) (string, refreshTokenCookieState) {
	request := c.Request()
	if request == nil {
		return "", refreshTokenCookieAbsent
	}
	var token string
	count := 0
	for _, cookie := range request.Cookies() {
		if cookie.Name != refreshTokenCookieName {
			continue
		}
		count++
		token = cookie.Value
	}
	rawCount := 0
	for _, header := range request.Header.Values("Cookie") {
		for _, pair := range strings.Split(header, ";") {
			name, _, present := strings.Cut(
				strings.TrimSpace(pair),
				"=",
			)
			if present && name == refreshTokenCookieName {
				rawCount++
			}
		}
	}
	if rawCount == 0 && count == 0 {
		return "", refreshTokenCookieAbsent
	}
	if rawCount != 1 ||
		count != 1 ||
		token == "" ||
		strings.TrimSpace(token) != token {
		return "", refreshTokenCookieInvalid
	}
	return token, refreshTokenCookieValid
}

// ProjectRefreshRateLimitIdentity verifies the one strict refresh Cookie and
// projects its signed Human/session identifiers without querying storage.
// Invalid, expired, missing, or ambiguous Cookies deliberately produce no
// identity so the anonymous limiter can use its shared unidentified bucket.
func (h *AuthHandler) ProjectRefreshRateLimitIdentity(
	c HTTPContext,
) (RefreshRateLimitIdentity, bool) {
	if h == nil ||
		h.authService == nil ||
		h.authService.jwtManager == nil {
		return RefreshRateLimitIdentity{}, false
	}
	refreshToken, cookieState := readRefreshTokenCookie(c)
	if cookieState != refreshTokenCookieValid {
		return RefreshRateLimitIdentity{}, false
	}
	claims, err := h.authService.jwtManager.VerifyRefreshToken(refreshToken)
	if err != nil ||
		claims == nil ||
		claims.Type != "refresh" ||
		claims.UserID == 0 ||
		claims.SessionID == "" ||
		claims.SessionID != strings.TrimSpace(claims.SessionID) ||
		len(claims.SessionID) > 128 {
		return RefreshRateLimitIdentity{}, false
	}
	return RefreshRateLimitIdentity{
		UserID:    claims.UserID,
		SessionID: claims.SessionID,
	}, true
}

func writeMissingRefreshCookie(c HTTPContext) {
	c.JSON(http.StatusUnauthorized, ErrorResponse{
		Error:   "invalid_token",
		Message: "缺少有效的登录会话 Cookie",
		Code:    "invalid_token",
	})
}

func writeSessionPreconditionRequired(c HTTPContext) {
	c.JSON(http.StatusBadRequest, ErrorResponse{
		Error:   "session_precondition_required",
		Message: "缺少有效的浏览器会话前置条件",
		Code:    "session_precondition_required",
	})
}

func (h *AuthHandler) requireRefreshCookieSession(
	c HTTPContext,
	refreshToken, expectedSessionID string,
) bool {
	expectedSessionID = strings.TrimSpace(expectedSessionID)
	if expectedSessionID == "" || len(expectedSessionID) > 128 {
		writeSessionPreconditionRequired(c)
		return false
	}
	if h.authService == nil || h.authService.jwtManager == nil {
		h.logger.Error(
			"Authentication session verifier is unavailable",
			"request_id", authLogRequestID(c),
		)
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:   "authentication_unavailable",
			Message: "认证服务暂时不可用",
			Code:    "authentication_unavailable",
		})
		return false
	}
	claims, err := h.authService.jwtManager.VerifyRefreshToken(refreshToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "invalid_token",
			Message: "登录会话 Cookie 无效或已过期",
			Code:    "invalid_token",
		})
		return false
	}
	if claims.SessionID != expectedSessionID {
		c.JSON(http.StatusConflict, ErrorResponse{
			Error:   "session_replaced",
			Message: "浏览器登录会话已被更新，请刷新后重试",
			Code:    "session_replaced",
		})
		return false
	}
	return true
}

// Register 用户注册
func (h *AuthHandler) Register(c HTTPContext) {
	if !h.requireAllowedBrowserOrigin(c) {
		return
	}
	var req RegisterRequest
	if err := c.Bind(&req); err != nil {
		if rejectOversizedAuthenticationRequest(c, err) {
			return
		}
		h.logger.Error(
			"Failed to bind register request",
			"request_id", authLogRequestID(c),
			"reason", "invalid_request_body",
		)
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
	ctx, cancel, ok := h.boundedRequestContext(c)
	if !ok {
		return
	}
	defer cancel()
	resp, err := h.authService.Register(ctx, &req, ipAddress, userAgent)
	if err != nil {
		h.logger.Error(
			"Registration failed",
			"request_id", authLogRequestID(c),
			"reason", authLogReason(err),
		)

		status, message := registrationFailureHTTPResponse(err)

		c.JSON(status, map[string]interface{}{
			"code": 1, // 错误码设为1
			"msg":  message,
			"data": nil,
		})
		return
	}

	h.logger.Info(
		"User registered successfully",
		"request_id", authLogRequestID(c),
		"user_id", authLogUserID(resp.User.ID),
	)
	if resp.RefreshToken != "" && !h.setRefreshTokenCookie(
		c,
		resp.RefreshToken,
		resp.RefreshTokenExpiresAt,
	) {
		h.logger.Error(
			"Registration session cookie could not be issued",
			"request_id", authLogRequestID(c),
			"reason", "invalid_refresh_cookie_lifetime",
		)
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:   "authentication_unavailable",
			Message: "认证服务暂时不可用",
		})
		return
	}

	// 返回成功响应
	c.JSON(http.StatusCreated, map[string]interface{}{
		"code": 0, // 成功码设为0
		"msg":  "注册成功",
		"data": resp,
	})
}

func registrationFailureHTTPResponse(err error) (int, string) {
	switch {
	case errors.Is(err, ErrUserExists):
		return http.StatusConflict, "该用户已存在"
	case errors.Is(err, ErrPasswordTooWeak):
		return http.StatusBadRequest, "密码强度不符合要求"
	case errors.Is(err, ErrEmailVerificationPolicyUnavailable),
		errors.Is(err, ErrEmailVerificationPolicyChanged),
		errors.Is(err, ErrAtomicRegistrationUnavailable):
		return http.StatusServiceUnavailable, "注册服务暂时不可用"
	case err != nil && strings.Contains(err.Error(), "password"):
		return http.StatusBadRequest, "密码强度不符合要求"
	default:
		return http.StatusInternalServerError, "注册失败"
	}
}

// Login 用户登录
func (h *AuthHandler) Login(c HTTPContext) {
	if !h.requireAllowedBrowserOrigin(c) {
		return
	}
	var req LoginRequest
	if err := c.Bind(&req); err != nil {
		if rejectOversizedAuthenticationRequest(c, err) {
			return
		}
		h.logger.Error(
			"Failed to bind login request",
			"request_id", authLogRequestID(c),
			"reason", "invalid_request_body",
		)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "请求格式无效",
		})
		return
	}
	if request := c.Request(); request != nil {
		if cookie, err := request.Cookie(trustedDeviceCookieName); err == nil {
			req.DeviceToken = cookie.Value
		}
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
	ctx, cancel, ok := h.boundedRequestContext(c)
	if !ok {
		return
	}
	defer cancel()
	resp, err := h.authService.Login(ctx, &req, ipAddress, userAgent)
	if err != nil {
		if req.DeviceToken != "" &&
			(errors.Is(err, ErrInvalidOTP) || strings.Contains(err.Error(), "OTP")) {
			h.clearTrustedDeviceCookie(c)
		}
		h.logger.Error(
			"Login failed",
			"request_id", authLogRequestID(c),
			"reason", authLogReason(err),
		)

		status, message := loginFailureHTTPResponse(err)

		c.JSON(status, map[string]interface{}{
			"code": 1, // 错误码设为1
			"msg":  message,
			"data": nil,
		})
		return
	}

	h.logger.Info(
		"User logged in successfully",
		"request_id", authLogRequestID(c),
		"user_id", authLogUserID(resp.User.ID),
	)

	// 设置安全头
	c.SetHeader("X-Auth-Token", resp.AccessToken)
	if !h.setRefreshTokenCookie(
		c,
		resp.RefreshToken,
		resp.RefreshTokenExpiresAt,
	) {
		h.logger.Error(
			"Login session cookie could not be issued",
			"request_id", authLogRequestID(c),
			"reason", "invalid_refresh_cookie_lifetime",
		)
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:   "authentication_unavailable",
			Message: "认证服务暂时不可用",
		})
		return
	}
	if req.RememberDevice && resp.TrustedDeviceToken != "" {
		h.setTrustedDeviceCookie(
			c,
			resp.TrustedDeviceToken,
			resp.TrustedDeviceExpiresAt,
		)
	} else if !req.RememberDevice {
		h.clearTrustedDeviceCookie(c)
	}

	// 返回成功响应 - 使用ApiResponse格式与前端保持一致
	c.JSON(http.StatusOK, map[string]interface{}{
		"code": 0,
		"msg":  "登录成功",
		"data": resp,
	})
}

// RefreshToken 刷新令牌
func (h *AuthHandler) RefreshToken(c HTTPContext) {
	if !h.requireAllowedBrowserOrigin(c) {
		return
	}
	if rejectLegacyRefreshCredentials(c) {
		return
	}
	refreshToken, cookieState := readRefreshTokenCookie(c)
	if cookieState != refreshTokenCookieValid {
		writeMissingRefreshCookie(c)
		return
	}
	req := RefreshTokenRequest{RefreshToken: refreshToken}

	// 获取客户端信息
	ipAddress := c.ClientIP()
	userAgent := c.UserAgent()

	// 调用认证服务
	ctx, cancel, ok := h.boundedRequestContext(c)
	if !ok {
		return
	}
	defer cancel()
	resp, err := h.authService.RefreshToken(ctx, &req, ipAddress, userAgent)
	if err != nil {
		h.logger.Error(
			"Token refresh failed",
			"request_id", authLogRequestID(c),
			"reason", authLogReason(err),
		)

		status, code, message := refreshFailureHTTPResponse(err)

		c.JSON(status, ErrorResponse{
			Error:   code,
			Message: message,
			Code:    code,
		})
		return
	}

	h.logger.Info(
		"Token refreshed successfully",
		"request_id", authLogRequestID(c),
		"user_id", authLogUserID(resp.User.ID),
	)
	if !h.setRefreshTokenCookie(
		c,
		resp.RefreshToken,
		resp.RefreshTokenExpiresAt,
	) {
		h.logger.Error(
			"Rotated session cookie could not be issued",
			"request_id", authLogRequestID(c),
			"reason", "invalid_refresh_cookie_lifetime",
		)
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:   "refresh_failed",
			Message: "刷新登录令牌失败",
			Code:    "refresh_failed",
		})
		return
	}

	// 返回成功响应
	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "登录令牌刷新成功",
		Data:    resp,
	})
}

func loginFailureHTTPResponse(err error) (int, string) {
	switch {
	case errors.Is(err, ErrInvalidCredentials),
		errors.Is(err, ErrUserNotFound):
		return http.StatusUnauthorized, "邮箱或密码错误"
	case errors.Is(err, ErrAccountLocked):
		return http.StatusForbidden, "账号已锁定"
	case errors.Is(err, ErrEmailNotVerified):
		return http.StatusForbidden, "邮箱尚未验证"
	case errors.Is(err, ErrEmailVerificationPolicyUnavailable):
		return http.StatusServiceUnavailable, "认证策略暂时不可用"
	case errors.Is(err, ErrInvalidOTP):
		return http.StatusUnauthorized, "OTP 验证码错误"
	case err != nil && strings.Contains(err.Error(), "OTP"):
		return http.StatusBadRequest, "请输入 OTP 验证码"
	case err != nil && strings.Contains(err.Error(), "too many"):
		return http.StatusTooManyRequests, "登录失败次数过多，请稍后重试"
	default:
		return http.StatusServiceUnavailable, "登录失败"
	}
}

func refreshFailureHTTPResponse(err error) (int, string, string) {
	switch {
	case errors.Is(err, ErrInvalidToken):
		return http.StatusUnauthorized, "invalid_token", "刷新令牌无效"
	case errors.Is(err, ErrTokenExpired):
		return http.StatusUnauthorized, "token_expired", "刷新令牌已过期"
	case errors.Is(err, ErrUserNotFound):
		return http.StatusUnauthorized, "user_not_found", "未找到用户"
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusRequestTimeout, "request_timeout", "认证请求超时，请重试"
	default:
		return http.StatusServiceUnavailable, "refresh_failed", "刷新登录令牌失败"
	}
}

// Logout 用户登出
func (h *AuthHandler) Logout(c HTTPContext) {
	// 普通退出只结束当前登录会话。用户显式选择的可信设备凭据继续保留，
	// 直到其在设备管理中撤销、过期，或执行全设备退出。
	if !h.requireAllowedBrowserOrigin(c) {
		return
	}
	if rejectLegacyRefreshCredentials(c) {
		return
	}
	refreshToken, cookieState := readRefreshTokenCookie(c)
	if cookieState != refreshTokenCookieValid {
		writeMissingRefreshCookie(c)
		return
	}
	request := c.Request()
	if request == nil {
		writeSessionPreconditionRequired(c)
		return
	}
	sessionIDs := request.Header.Values(humanSessionIDHeader)
	if len(sessionIDs) != 1 {
		writeSessionPreconditionRequired(c)
		return
	}
	if !h.requireRefreshCookieSession(
		c,
		refreshToken,
		sessionIDs[0],
	) {
		return
	}

	ctx, cancel, ok := h.boundedRequestContext(c)
	if !ok {
		return
	}
	defer cancel()
	if err := h.authService.Logout(ctx, refreshToken); err != nil {
		h.logger.Error(
			"Logout failed",
			"request_id", authLogRequestID(c),
			"reason", authLogReason(err),
		)
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{
			Error:   "logout_failed",
			Message: "退出登录失败，请稍后重试",
		})
		return
	}

	h.logger.Info("User logged out successfully", "request_id", authLogRequestID(c))
	h.clearRefreshTokenCookie(c)

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
	refreshToken, cookieState := readRefreshTokenCookie(c)
	if cookieState == refreshTokenCookieInvalid {
		writeMissingRefreshCookie(c)
		return
	}
	if cookieState == refreshTokenCookieValid {
		sessionID, exists := c.Get("session_id")
		expectedSessionID, validSessionID := sessionID.(string)
		if !exists ||
			!validSessionID ||
			!h.requireRefreshCookieSession(
				c,
				refreshToken,
				expectedSessionID,
			) {
			return
		}
	}

	ctx, cancel, ok := h.boundedRequestContext(c)
	if !ok {
		return
	}
	defer cancel()
	if err := h.authService.LogoutAll(ctx, userIDUint); err != nil {
		h.logger.Error(
			"Logout all failed",
			"request_id", authLogRequestID(c),
			"user_id", authLogUserID(userIDUint),
			"reason", authLogReason(err),
		)
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "logout_failed",
			Message: "无法从所有设备退出登录",
		})
		return
	}

	h.logger.Info(
		"User logged out from all devices",
		"request_id", authLogRequestID(c),
		"user_id", authLogUserID(userIDUint),
	)
	h.clearRefreshTokenCookie(c)
	h.clearTrustedDeviceCookie(c)

	// 返回成功响应
	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "已从所有设备退出登录",
	})
}

func (h *AuthHandler) setTrustedDeviceCookie(c HTTPContext, token string, expiresAt time.Time) {
	if token == "" {
		return
	}
	now := time.Now()
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge < 1 {
		maxAge = int(defaultTrustedDeviceTTL.Seconds())
		expiresAt = now.Add(defaultTrustedDeviceTTL)
	}
	c.SetCookie(&http.Cookie{
		Name:     trustedDeviceCookieName,
		Value:    token,
		Path:     trustedDeviceCookiePath,
		MaxAge:   maxAge,
		Expires:  expiresAt.UTC(),
		HttpOnly: true,
		Secure:   h.authCookieIsSecure(c),
		SameSite: http.SameSiteStrictMode,
	})
}

func (h *AuthHandler) clearTrustedDeviceCookie(c HTTPContext) {
	c.SetCookie(&http.Cookie{
		Name:     trustedDeviceCookieName,
		Value:    "",
		Path:     trustedDeviceCookiePath,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0).UTC(),
		HttpOnly: true,
		Secure:   h.authCookieIsSecure(c),
		SameSite: http.SameSiteStrictMode,
	})
}

func (h *AuthHandler) setRefreshTokenCookie(
	c HTTPContext,
	token string,
	expiresAt time.Time,
) bool {
	now := time.Now()
	if token == "" || expiresAt.IsZero() || !expiresAt.After(now) {
		return false
	}
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge < 1 {
		return false
	}
	c.SetCookie(&http.Cookie{
		Name:     refreshTokenCookieName,
		Value:    token,
		Path:     refreshTokenCookiePath,
		MaxAge:   maxAge,
		Expires:  expiresAt.UTC(),
		HttpOnly: true,
		Secure:   h.authCookieIsSecure(c),
		SameSite: http.SameSiteStrictMode,
	})
	return true
}

func (h *AuthHandler) clearRefreshTokenCookie(c HTTPContext) {
	c.SetCookie(&http.Cookie{
		Name:     refreshTokenCookieName,
		Value:    "",
		Path:     refreshTokenCookiePath,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0).UTC(),
		HttpOnly: true,
		Secure:   h.authCookieIsSecure(c),
		SameSite: http.SameSiteStrictMode,
	})
}

func (h *AuthHandler) authCookieIsSecure(c HTTPContext) bool {
	if h.secureAuthCookies {
		return true
	}
	request := c.Request()
	return request != nil && request.TLS != nil
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

	ctx, cancel, ok := h.boundedRequestContext(c)
	if !ok {
		return
	}
	defer cancel()
	user, err := h.authService.userRepo.GetByID(ctx, userIDUint)
	if err != nil {
		h.logger.Error(
			"Failed to get user",
			"request_id", authLogRequestID(c),
			"user_id", authLogUserID(userIDUint),
			"reason", authLogReason(err),
		)
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

// ForgotPassword 忘记密码
func (h *AuthHandler) ForgotPassword(c HTTPContext) {
	var req ForgotPasswordRequest
	if err := c.Bind(&req); err != nil {
		if rejectOversizedAuthenticationRequest(c, err) {
			return
		}
		h.logger.Error(
			"Failed to bind forgot password request",
			"request_id", authLogRequestID(c),
			"reason", "invalid_request_body",
		)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "请求格式无效",
		})
		return
	}

	ctx, cancel, ok := h.boundedRequestContext(c)
	if !ok {
		return
	}
	defer cancel()
	err := h.authService.ForgotPassword(ctx, req.Email)
	if err != nil {
		if h.abortTerminatedPublicEmailRequest(c, err) {
			return
		}
		h.logger.Error(
			"Failed to process forgot password",
			"request_id", authLogRequestID(c),
			"reason", authLogReason(err),
		)
	}

	// Never reveal account existence or dependency health on a public recovery
	// endpoint. Operators receive the bounded internal log above.
	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "密码重置邮件已发送",
	})
}

// ResetPassword 重置密码
func (h *AuthHandler) ResetPassword(c HTTPContext) {
	var req ResetPasswordRequest
	if err := c.Bind(&req); err != nil {
		if rejectOversizedAuthenticationRequest(c, err) {
			return
		}
		h.logger.Error(
			"Failed to bind reset password request",
			"request_id", authLogRequestID(c),
			"reason", "invalid_request_body",
		)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "请求格式无效",
		})
		return
	}

	ctx, cancel, ok := h.boundedRequestContext(c)
	if !ok {
		return
	}
	defer cancel()
	err := h.authService.ResetPassword(ctx, req.Token, req.NewPassword)
	if err != nil {
		h.logger.Error(
			"Failed to reset password",
			"request_id", authLogRequestID(c),
			"reason", authLogReason(err),
		)
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
	var req VerifyEmailRequest
	if err := c.Bind(&req); err != nil {
		if rejectOversizedAuthenticationRequest(c, err) {
			return
		}
		h.logger.Error(
			"Failed to bind verify email request",
			"request_id", authLogRequestID(c),
			"reason", "invalid_request_body",
		)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "请求格式无效",
		})
		return
	}

	ctx, cancel, ok := h.boundedRequestContext(c)
	if !ok {
		return
	}
	defer cancel()
	err := h.authService.VerifyEmail(ctx, req.Token)
	if err != nil {
		h.logger.Error(
			"Failed to verify email",
			"request_id", authLogRequestID(c),
			"reason", authLogReason(err),
		)
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
		if rejectOversizedAuthenticationRequest(c, err) {
			return
		}
		h.logger.Error(
			"Failed to bind resend verification request",
			"request_id", authLogRequestID(c),
			"reason", "invalid_request_body",
		)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "请求格式无效",
		})
		return
	}

	ctx, cancel, ok := h.boundedRequestContext(c)
	if !ok {
		return
	}
	defer cancel()
	err := h.authService.ResendVerification(ctx, req.Email)
	if err != nil {
		if h.abortTerminatedPublicEmailRequest(c, err) {
			return
		}
		h.logger.Error(
			"Failed to resend verification",
			"request_id", authLogRequestID(c),
			"reason", authLogReason(err),
		)
	}

	// Keep the public response indistinguishable for unknown, already verified,
	// accepted, and internally failed requests.
	c.JSON(http.StatusOK, SuccessResponse{
		Success: true,
		Message: "验证邮件已发送",
	})
}

// abortTerminatedPublicEmailRequest avoids writing the enumeration-safe success
// envelope after the original client request has already ended. The service
// error must match the parent request's termination cause: a timeout derived by
// boundedRequestContext, or an unrelated dependency failure that races with
// parent cancellation, must still publish the same opaque 200 response.
func (h *AuthHandler) abortTerminatedPublicEmailRequest(
	c HTTPContext,
	err error,
) bool {
	if c == nil || err == nil || c.Request() == nil {
		return false
	}

	parentErr := c.Request().Context().Err()
	status := 0
	reason := ""
	switch {
	case errors.Is(parentErr, context.Canceled) &&
		errors.Is(err, context.Canceled):
		status = statusClientClosedRequest
		reason = "request_canceled"
	case errors.Is(parentErr, context.DeadlineExceeded) &&
		errors.Is(err, context.DeadlineExceeded):
		status = http.StatusRequestTimeout
		reason = "request_deadline_exceeded"
	default:
		return false
	}

	h.logger.Debug(
		"Public authentication email request ended before processing completed",
		"request_id", authLogRequestID(c),
		"reason", reason,
	)
	if !authResponseWritten(c) {
		c.Status(status)
	}
	c.Abort()
	return true
}

// UpdateProfile 更新用户资料
func (h *AuthHandler) UpdateProfile(c HTTPContext) {
	userInfo, err := GetUserFromContext(c)
	if err != nil {
		h.logger.Error(
			"Failed to get user from context",
			"request_id", authLogRequestID(c),
			"reason", "missing_authentication_context",
		)
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "unauthorized",
			Message: "请先登录",
		})
		return
	}

	var req UpdateProfileRequest
	if err := c.Bind(&req); err != nil {
		if rejectOversizedAuthenticationRequest(c, err) {
			return
		}
		h.logger.Error(
			"Failed to bind update profile request",
			"request_id", authLogRequestID(c),
			"user_id", authLogUserID(userInfo.ID),
			"reason", "invalid_request_body",
		)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "请求格式无效",
		})
		return
	}

	ctx, cancel, ok := h.boundedRequestContext(c)
	if !ok {
		return
	}
	defer cancel()
	err = h.authService.UpdateProfile(ctx, userInfo.ID, &req)
	if err != nil {
		h.logger.Error(
			"Failed to update profile",
			"request_id", authLogRequestID(c),
			"user_id", authLogUserID(userInfo.ID),
			"reason", authLogReason(err),
		)
		if code, message, ok := profileValidationHTTPError(err); ok {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:   code,
				Code:    code,
				Message: message,
			})
			return
		}
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

func profileValidationHTTPError(err error) (string, string, bool) {
	switch {
	case errors.Is(err, ErrInvalidProfileName):
		return "invalid_profile_name", "名字和姓氏最多 50 个字符", true
	case errors.Is(err, ErrInvalidProfileZone):
		return "invalid_profile_timezone", "时区必须是有效的 IANA 时区名称", true
	case errors.Is(err, ErrInvalidProfileLocale):
		return "unsupported_profile_language", "当前仅支持简体中文（zh-CN）或英文（en）", true
	case errors.Is(err, ErrInvalidProfilePhone):
		return "invalid_profile_phone", "手机号码必须为空或使用 E.164 格式", true
	case errors.Is(err, ErrInvalidProfileAvatar):
		return "invalid_profile_avatar", "头像字段只能省略、清空或保持当前值；更换头像请使用上传接口", true
	default:
		return "", "", false
	}
}

// ChangePassword 修改密码
func (h *AuthHandler) ChangePassword(c HTTPContext) {
	userInfo, err := GetUserFromContext(c)
	if err != nil {
		h.logger.Error(
			"Failed to get user from context",
			"request_id", authLogRequestID(c),
			"reason", "missing_authentication_context",
		)
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "unauthorized",
			Message: "请先登录",
		})
		return
	}

	var req ChangePasswordRequest
	if err := c.Bind(&req); err != nil {
		if rejectOversizedAuthenticationRequest(c, err) {
			return
		}
		h.logger.Error(
			"Failed to bind change password request",
			"request_id", authLogRequestID(c),
			"user_id", authLogUserID(userInfo.ID),
			"reason", "invalid_request_body",
		)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "请求格式无效",
		})
		return
	}

	ctx, cancel, ok := h.boundedRequestContext(c)
	if !ok {
		return
	}
	defer cancel()
	err = h.authService.ChangePassword(ctx, userInfo.ID, req.CurrentPassword, req.NewPassword)
	if err != nil {
		h.logger.Error(
			"Failed to change password",
			"request_id", authLogRequestID(c),
			"user_id", authLogUserID(userInfo.ID),
			"reason", authLogReason(err),
		)
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
		Message: "密码修改成功，所有会话（包括当前会话）均已失效",
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
		if rejectOversizedAuthenticationRequest(c, err) {
			return
		}
		h.logger.Error(
			"Failed to bind enable OTP request",
			"request_id", authLogRequestID(c),
			"user_id", authLogUserID(userInfo.ID),
			"reason", "invalid_request_body",
		)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "请求体格式无效",
		})
		return
	}

	ctx, cancel, ok := h.boundedRequestContext(c)
	if !ok {
		return
	}
	defer cancel()

	otpSetup, err := h.authService.EnableOTP(ctx, userInfo.ID, req.Password)
	if err != nil {
		h.logger.Error(
			"Failed to enable OTP",
			"request_id", authLogRequestID(c),
			"user_id", authLogUserID(userInfo.ID),
			"reason", authLogReason(err),
		)
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
		if rejectOversizedAuthenticationRequest(c, err) {
			return
		}
		h.logger.Error(
			"Failed to bind disable OTP request",
			"request_id", authLogRequestID(c),
			"user_id", authLogUserID(userInfo.ID),
			"reason", "invalid_request_body",
		)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "请求体格式无效",
		})
		return
	}

	ctx, cancel, ok := h.boundedRequestContext(c)
	if !ok {
		return
	}
	defer cancel()

	if err := h.authService.DisableOTP(ctx, userInfo.ID, req.Password); err != nil {
		h.logger.Error(
			"Failed to disable OTP",
			"request_id", authLogRequestID(c),
			"user_id", authLogUserID(userInfo.ID),
			"reason", authLogReason(err),
		)
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
		if rejectOversizedAuthenticationRequest(c, err) {
			return
		}
		h.logger.Error(
			"Failed to bind verify OTP request",
			"request_id", authLogRequestID(c),
			"user_id", authLogUserID(userInfo.ID),
			"reason", "invalid_request_body",
		)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "请求体格式无效",
		})
		return
	}

	ctx, cancel, ok := h.boundedRequestContext(c)
	if !ok {
		return
	}
	defer cancel()

	if err := h.authService.VerifyOTP(ctx, userInfo.ID, req.Code); err != nil {
		h.logger.Error(
			"Failed to verify OTP",
			"request_id", authLogRequestID(c),
			"user_id", authLogUserID(userInfo.ID),
			"reason", authLogReason(err),
		)
		status, message := verifyOTPFailureHTTPResponse(err)

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

func verifyOTPFailureHTTPResponse(err error) (int, string) {
	switch {
	case errors.Is(err, ErrInvalidOTP):
		return http.StatusBadRequest, "OTP 验证码错误"
	case errors.Is(err, ErrOTPExpired):
		return http.StatusBadRequest, "OTP 验证码已过期"
	default:
		return http.StatusInternalServerError, "验证 OTP 失败"
	}
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

	var req GenerateBackupCodesRequest
	if err := c.Bind(&req); err != nil {
		if rejectOversizedAuthenticationRequest(c, err) {
			return
		}
		h.logger.Warn(
			"Invalid backup-code regeneration request",
			"request_id", backupCodeSecurityLogRequestID(c),
			"user_id", authLogUserID(userInfo.ID),
			"reason", "invalid_request_body",
		)
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "请求体格式无效",
		})
		return
	}

	ctx, cancel, ok := h.boundedRequestContext(c)
	if !ok {
		return
	}
	defer cancel()

	codes, err := h.authService.GenerateBackupCodes(
		ctx,
		userInfo.ID,
		req.CurrentPassword,
		AuthenticationSecurityAuditContext{
			RequestID:     authLogRequestID(c),
			TraceID:       observability.TraceIDFromContext(ctx),
			CorrelationID: observability.CorrelationIDFromContext(ctx),
		},
	)
	if err != nil {
		h.logger.Error(
			"Failed to generate backup codes",
			"request_id", backupCodeSecurityLogRequestID(c),
			"user_id", authLogUserID(userInfo.ID),
			"reason", authLogReason(err),
		)
		status := http.StatusServiceUnavailable
		errorCode := "backup_code_regeneration_unavailable"
		message := "备用验证码服务暂时不可用"
		switch {
		case errors.Is(err, ErrInvalidPassword):
			status = http.StatusUnauthorized
			errorCode = "invalid_password"
			message = "当前密码错误"
		case errors.Is(err, ErrOTPNotEnabled):
			status = http.StatusConflict
			errorCode = "otp_not_enabled"
			message = "尚未启用 OTP"
		case errors.Is(err, ErrBackupCodesChanged):
			status = http.StatusConflict
			errorCode = "backup_codes_changed"
			message = "认证状态已变更，请重试"
		}
		c.JSON(status, ErrorResponse{
			Error:   errorCode,
			Message: message,
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

	roleValue, exists := c.Get("platform_role")
	if !exists {
		return nil, fmt.Errorf("platform role not found")
	}

	platformRole, ok := roleValue.(PlatformRole)
	if !ok || !platformRole.IsValid() {
		return nil, fmt.Errorf("invalid platform role")
	}

	return &UserInfo{
		ID:           userIDUint,
		PlatformRole: platformRole,
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
		h.logger.Error(
			"Token verification failed",
			"request_id", authLogRequestID(c),
			"reason", authLogReason(err),
		)
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

	requestContext, cancel, ok := h.boundedRequestContext(c)
	if !ok {
		return
	}
	defer cancel()
	currentUser, err := h.authService.userRepo.GetByID(requestContext, claims.UserID)
	if err != nil {
		if h.abortTerminatedReadRequest(c, err) {
			return
		}
		if errors.Is(err, ErrUserNotFound) {
			h.logger.Warn(
				"Access token principal is no longer available",
				"request_id", authLogRequestID(c),
				"reason", "principal_not_found",
			)
			c.JSON(http.StatusUnauthorized, ErrorResponse{
				Error:   "invalid_token",
				Message: "访问令牌对应的用户已失效",
			})
		} else {
			h.logger.Error(
				"Failed to revalidate access token principal",
				"request_id", authLogRequestID(c),
				"reason", authLogReason(err),
			)
			c.JSON(http.StatusServiceUnavailable, ErrorResponse{
				Error:   "authentication_unavailable",
				Message: "认证服务暂时不可用",
			})
		}
		c.Abort()
		return
	}
	if stateErr := validateUserAccessState(currentUser, time.Now()); stateErr != nil {
		h.logger.Warn(
			"Access token principal is not active",
			"request_id", authLogRequestID(c),
			"user_id", authLogUserID(currentUser.ID),
			"status", observability.SafeLogValue(string(currentUser.Status)),
		)
		errorCode := "account_inactive"
		message := "账号当前不可用，请重新登录或联系管理员"
		if errors.Is(stateErr, ErrAccountLocked) {
			errorCode = "account_locked"
			message = "账号已锁定，请稍后重试"
		} else if errors.Is(stateErr, ErrInvalidAccountState) {
			errorCode = "invalid_token"
			message = "访问令牌对应的账号状态无效"
		}
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   errorCode,
			Message: message,
		})
		c.Abort()
		return
	}
	if !currentUser.PlatformRole.IsValid() {
		h.logger.Warn(
			"Access token principal has an invalid platform role",
			"request_id", authLogRequestID(c),
			"user_id", authLogUserID(currentUser.ID),
			"reason", "invalid_platform_role",
		)
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "invalid_token",
			Message: "访问令牌对应的账号角色无效",
		})
		c.Abort()
		return
	}
	if currentUser.PlatformRole != claims.PlatformRole {
		h.logger.Warn(
			"Access token role is stale",
			"request_id", authLogRequestID(c),
			"user_id", authLogUserID(currentUser.ID),
			"reason", "role_changed",
		)
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "stale_token",
			Message: "账号权限已变更，请重新登录",
		})
		c.Abort()
		return
	}
	if currentUser.PasswordChangedAt != nil && claims.Iat < currentUser.PasswordChangedAt.Unix() {
		h.logger.Warn(
			"Access token predates password change",
			"request_id", authLogRequestID(c),
			"user_id", authLogUserID(currentUser.ID),
		)
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
		if h.abortTerminatedReadRequest(c, err) {
			return
		}
		h.logger.Error(
			"Failed to revalidate access token session",
			"request_id", authLogRequestID(c),
			"user_id", authLogUserID(currentUser.ID),
			"reason", authLogReason(err),
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
			"request_id", authLogRequestID(c),
			"user_id", authLogUserID(currentUser.ID),
		)
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "session_revoked",
			Message: "登录会话已结束，请重新登录",
		})
		c.Abort()
		return
	}

	// 平台职责使用单一类型化 context 值。项目角色由后续项目中间件
	// 每次从数据库实时解析，绝不写入 human JWT 或平台 context。
	c.Set("user_id", currentUser.ID)
	c.Set("platform_role", currentUser.PlatformRole)
	c.Set("token_jti", claims.Jti)
	c.Set("session_id", claims.SessionID)

	// 继续处理
	c.Next()
}

// RequirePlatformRoles authorizes an exact, closed allowlist of platform
// duties. Platform roles are deliberately unordered and have no inheritance.
func (h *AuthHandler) RequirePlatformRoles(
	allowedRoles ...PlatformRole,
) func(HTTPContext) {
	allowlist := make(map[PlatformRole]struct{}, len(allowedRoles))
	for _, role := range allowedRoles {
		if role.IsValid() {
			allowlist[role] = struct{}{}
		}
	}
	return func(c HTTPContext) {
		roleValue, exists := c.Get("platform_role")
		if !exists {
			c.JSON(http.StatusForbidden, ErrorResponse{
				Error:   "access_denied",
				Message: "无权访问",
			})
			c.Abort()
			return
		}

		platformRole, ok := roleValue.(PlatformRole)
		if !ok || !platformRole.IsValid() {
			c.JSON(http.StatusForbidden, ErrorResponse{
				Error:   "access_denied",
				Message: "无权访问",
			})
			c.Abort()
			return
		}

		if _, allowed := allowlist[platformRole]; !allowed {
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
