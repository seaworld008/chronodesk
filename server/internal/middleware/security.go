package middleware

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
)

// SecurityConfig 安全中间件配置
type SecurityConfig struct {
	// XSSProtection XSS保护
	XSSProtection string
	// ContentTypeNosniff 内容类型嗅探保护
	ContentTypeNosniff bool
	// XFrameOptions X-Frame-Options头
	XFrameOptions string
	// HSTSMaxAge HSTS最大年龄（秒）
	HSTSMaxAge int
	// HSTSIncludeSubdomains HSTS是否包含子域名
	HSTSIncludeSubdomains bool
	// HSTSPreload HSTS是否预加载
	HSTSPreload bool
	// ContentSecurityPolicy 内容安全策略
	ContentSecurityPolicy string
	// ReferrerPolicy 引用策略
	ReferrerPolicy string
	// PermissionsPolicy 权限策略
	PermissionsPolicy string
	// CrossOriginEmbedderPolicy 跨域嵌入策略
	CrossOriginEmbedderPolicy string
	// CrossOriginOpenerPolicy 跨域开启策略
	CrossOriginOpenerPolicy string
	// CrossOriginResourcePolicy 跨域资源策略
	CrossOriginResourcePolicy string
	// RemoveServerHeader 是否移除Server头
	RemoveServerHeader bool
	// CustomHeaders 自定义安全头
	CustomHeaders map[string]string
}

// DefaultSecurityConfig 默认安全配置
func DefaultSecurityConfig() *SecurityConfig {
	return &SecurityConfig{
		XSSProtection:             "1; mode=block",
		ContentTypeNosniff:        true,
		XFrameOptions:             "DENY",
		HSTSMaxAge:                31536000, // 1年
		HSTSIncludeSubdomains:     true,
		HSTSPreload:               false,
		ContentSecurityPolicy:     "default-src 'self'",
		ReferrerPolicy:            "strict-origin-when-cross-origin",
		PermissionsPolicy:         "geolocation=(), microphone=(), camera=()",
		CrossOriginEmbedderPolicy: "require-corp",
		CrossOriginOpenerPolicy:   "same-origin",
		CrossOriginResourcePolicy: "same-origin",
		RemoveServerHeader:        true,
		CustomHeaders:             make(map[string]string),
	}
}

// DevelopmentSecurityConfig 开发环境安全配置
func DevelopmentSecurityConfig() *SecurityConfig {
	return &SecurityConfig{
		XSSProtection:             "1; mode=block",
		ContentTypeNosniff:        true,
		XFrameOptions:             "SAMEORIGIN",
		HSTSMaxAge:                0, // 开发环境不启用HSTS
		ContentSecurityPolicy:     "default-src 'self' 'unsafe-inline' 'unsafe-eval'",
		ReferrerPolicy:            "no-referrer-when-downgrade",
		CrossOriginEmbedderPolicy: "",
		CrossOriginOpenerPolicy:   "",
		CrossOriginResourcePolicy: "cross-origin",
		RemoveServerHeader:        false,
		CustomHeaders:             make(map[string]string),
	}
}

// ProductionSecurityConfig 生产环境安全配置
func ProductionSecurityConfig() *SecurityConfig {
	return &SecurityConfig{
		XSSProtection:             "1; mode=block",
		ContentTypeNosniff:        true,
		XFrameOptions:             "DENY",
		HSTSMaxAge:                63072000, // 2年
		HSTSIncludeSubdomains:     true,
		HSTSPreload:               true,
		ContentSecurityPolicy:     "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'",
		ReferrerPolicy:            "strict-origin-when-cross-origin",
		PermissionsPolicy:         "geolocation=(), microphone=(), camera=(), payment=(), usb=(), magnetometer=(), gyroscope=(), speaker=()",
		CrossOriginEmbedderPolicy: "require-corp",
		CrossOriginOpenerPolicy:   "same-origin",
		CrossOriginResourcePolicy: "same-origin",
		RemoveServerHeader:        true,
		CustomHeaders:             make(map[string]string),
	}
}

// SecurityMiddleware 安全中间件
func SecurityMiddleware(config *SecurityConfig) func(HTTPContext) {
	if config == nil {
		config = DefaultSecurityConfig()
	}

	return func(c HTTPContext) {
		// 设置XSS保护
		if config.XSSProtection != "" {
			setHeader(c, "X-XSS-Protection", config.XSSProtection)
		}

		// 设置内容类型嗅探保护
		if config.ContentTypeNosniff {
			setHeader(c, "X-Content-Type-Options", "nosniff")
		}

		// 设置X-Frame-Options
		if config.XFrameOptions != "" {
			setHeader(c, "X-Frame-Options", config.XFrameOptions)
		}

		// 设置HSTS
		if config.HSTSMaxAge > 0 {
			hstsValue := fmt.Sprintf("max-age=%d", config.HSTSMaxAge)
			if config.HSTSIncludeSubdomains {
				hstsValue += "; includeSubDomains"
			}
			if config.HSTSPreload {
				hstsValue += "; preload"
			}
			setHeader(c, "Strict-Transport-Security", hstsValue)
		}

		// 设置内容安全策略
		if config.ContentSecurityPolicy != "" {
			setHeader(c, "Content-Security-Policy", config.ContentSecurityPolicy)
		}

		// 设置引用策略
		if config.ReferrerPolicy != "" {
			setHeader(c, "Referrer-Policy", config.ReferrerPolicy)
		}

		// 设置权限策略
		if config.PermissionsPolicy != "" {
			setHeader(c, "Permissions-Policy", config.PermissionsPolicy)
		}

		// 设置跨域嵌入策略
		if config.CrossOriginEmbedderPolicy != "" {
			setHeader(c, "Cross-Origin-Embedder-Policy", config.CrossOriginEmbedderPolicy)
		}

		// 设置跨域开启策略
		if config.CrossOriginOpenerPolicy != "" {
			setHeader(c, "Cross-Origin-Opener-Policy", config.CrossOriginOpenerPolicy)
		}

		// 设置跨域资源策略
		if config.CrossOriginResourcePolicy != "" {
			setHeader(c, "Cross-Origin-Resource-Policy", config.CrossOriginResourcePolicy)
		}

		// 移除Server头
		if config.RemoveServerHeader {
			setHeader(c, "Server", "")
		}

		// 设置自定义头
		for key, value := range config.CustomHeaders {
			setHeader(c, key, value)
		}

		c.Next()
	}
}

// CSRFConfig CSRF保护配置
type CSRFConfig struct {
	// TokenLength 令牌长度
	TokenLength int
	// TokenLookup 令牌查找方式
	TokenLookup string
	// ContextKey 上下文键名
	ContextKey string
	// CookieName Cookie名称
	CookieName string
	// CookieDomain Cookie域名
	CookieDomain string
	// CookiePath Cookie路径
	CookiePath string
	// CookieMaxAge Cookie最大年龄
	CookieMaxAge int
	// CookieSecure Cookie是否安全
	CookieSecure bool
	// CookieHTTPOnly Cookie是否仅HTTP
	CookieHTTPOnly bool
	// CookieSameSite Cookie SameSite属性
	CookieSameSite string
	// Skipper 跳过函数
	Skipper func(HTTPContext) bool
	// ErrorHandler 错误处理器
	ErrorHandler func(HTTPContext, error)
}

// DefaultCSRFConfig 默认CSRF配置
func DefaultCSRFConfig() *CSRFConfig {
	return &CSRFConfig{
		TokenLength:    32,
		TokenLookup:    "header:X-CSRF-Token",
		ContextKey:     "csrf",
		CookieName:     "_csrf",
		CookiePath:     "/",
		CookieMaxAge:   86400, // 24小时
		CookieSecure:   false,
		CookieHTTPOnly: true,
		CookieSameSite: "Strict",
	}
}

// CSRFMiddleware CSRF保护中间件
func CSRFMiddleware(config *CSRFConfig) func(HTTPContext) {
	if config == nil {
		config = DefaultCSRFConfig()
	}

	return func(c HTTPContext) {
		// 检查是否跳过
		if config.Skipper != nil && config.Skipper(c) {
			c.Next()
			return
		}

		// 对于安全方法（GET, HEAD, OPTIONS），只需要生成令牌
		method := getMethod(c)
		if method == "GET" || method == "HEAD" || method == "OPTIONS" {
			token := generateCSRFToken(config.TokenLength)
			setCSRFCookie(c, token, config)
			c.Set(config.ContextKey, token)
			c.Next()
			return
		}

		// 对于不安全方法，需要验证令牌
		token := extractCSRFToken(c, config)
		if token == "" {
			handleCSRFError(c, fmt.Errorf("missing CSRF token"), config)
			return
		}

		// 从Cookie获取存储的令牌
		storedToken := getCSRFCookie(c, config)
		if storedToken == "" {
			handleCSRFError(c, fmt.Errorf("missing CSRF cookie"), config)
			return
		}

		// 验证令牌
		if !validateCSRFToken(token, storedToken) {
			handleCSRFError(c, fmt.Errorf("invalid CSRF token"), config)
			return
		}

		c.Next()
	}
}

// generateCSRFToken 生成CSRF令牌
func generateCSRFToken(length int) string {
	bytes := make([]byte, length)
	rand.Read(bytes)
	return base64.URLEncoding.EncodeToString(bytes)
}

// extractCSRFToken 提取CSRF令牌
func extractCSRFToken(c HTTPContext, config *CSRFConfig) string {
	// 解析TokenLookup
	parts := strings.Split(config.TokenLookup, ":")
	if len(parts) != 2 {
		return ""
	}

	switch parts[0] {
	case "header":
		return c.GetHeader(parts[1])
	case "form":
		// 需要根据具体框架实现表单值获取
		return ""
	case "query":
		// 需要根据具体框架实现查询参数获取
		return ""
	default:
		return ""
	}
}

// setCSRFCookie 设置CSRF Cookie
func setCSRFCookie(c HTTPContext, token string, config *CSRFConfig) {
	// 构建Cookie字符串
	cookieValue := fmt.Sprintf("%s=%s; Path=%s; Max-Age=%d",
		config.CookieName, token, config.CookiePath, config.CookieMaxAge)

	if config.CookieDomain != "" {
		cookieValue += fmt.Sprintf("; Domain=%s", config.CookieDomain)
	}

	if config.CookieSecure {
		cookieValue += "; Secure"
	}

	if config.CookieHTTPOnly {
		cookieValue += "; HttpOnly"
	}

	if config.CookieSameSite != "" {
		cookieValue += fmt.Sprintf("; SameSite=%s", config.CookieSameSite)
	}

	setHeader(c, "Set-Cookie", cookieValue)
}

// getCSRFCookie 获取CSRF Cookie
func getCSRFCookie(c HTTPContext, config *CSRFConfig) string {
	cookieHeader := c.GetHeader("Cookie")
	if cookieHeader == "" {
		return ""
	}

	// 解析Cookie
	cookies := strings.Split(cookieHeader, ";")
	for _, cookie := range cookies {
		cookie = strings.TrimSpace(cookie)
		parts := strings.SplitN(cookie, "=", 2)
		if len(parts) == 2 && parts[0] == config.CookieName {
			return parts[1]
		}
	}

	return ""
}

// validateCSRFToken 验证CSRF令牌
func validateCSRFToken(token, storedToken string) bool {
	return subtle.ConstantTimeCompare([]byte(token), []byte(storedToken)) == 1
}

// handleCSRFError 处理CSRF错误
func handleCSRFError(c HTTPContext, err error, config *CSRFConfig) {
	if config.ErrorHandler != nil {
		config.ErrorHandler(c, err)
		return
	}

	// 默认错误处理
	setStatus(c, 403)
	setHeader(c, "Content-Type", "application/json")
	errorResponse := map[string]interface{}{
		"error":   "Forbidden",
		"code":    403,
		"message": "CSRF token validation failed",
	}
	sendJSON(c, errorResponse)
}

// SecureHeaders 安全头中间件（简化版）
func SecureHeaders() func(HTTPContext) {
	return SecurityMiddleware(DefaultSecurityConfig())
}

// StrictSecureHeaders 严格安全头中间件
func StrictSecureHeaders() func(HTTPContext) {
	return SecurityMiddleware(ProductionSecurityConfig())
}

// DevSecureHeaders 开发环境安全头中间件
func DevSecureHeaders() func(HTTPContext) {
	return SecurityMiddleware(DevelopmentSecurityConfig())
}

// NoSniff 内容类型嗅探保护中间件
func NoSniff() func(HTTPContext) {
	return func(c HTTPContext) {
		setHeader(c, "X-Content-Type-Options", "nosniff")
		c.Next()
	}
}

// XSSProtection XSS保护中间件
func XSSProtection() func(HTTPContext) {
	return func(c HTTPContext) {
		setHeader(c, "X-XSS-Protection", "1; mode=block")
		c.Next()
	}
}

// FrameDeny 框架拒绝中间件
func FrameDeny() func(HTTPContext) {
	return func(c HTTPContext) {
		setHeader(c, "X-Frame-Options", "DENY")
		c.Next()
	}
}

// HSTS HTTP严格传输安全中间件
func HSTS(maxAge int, includeSubdomains, preload bool) func(HTTPContext) {
	hstsValue := fmt.Sprintf("max-age=%d", maxAge)
	if includeSubdomains {
		hstsValue += "; includeSubDomains"
	}
	if preload {
		hstsValue += "; preload"
	}

	return func(c HTTPContext) {
		setHeader(c, "Strict-Transport-Security", hstsValue)
		c.Next()
	}
}

// CSP 内容安全策略中间件
func CSP(policy string) func(HTTPContext) {
	return func(c HTTPContext) {
		setHeader(c, "Content-Security-Policy", policy)
		c.Next()
	}
}

// SecurityHeaders 安全响应结构
type SecurityHeaders struct {
	XSSProtection             string `json:"x_xss_protection,omitempty"`
	ContentTypeOptions        string `json:"x_content_type_options,omitempty"`
	XFrameOptions             string `json:"x_frame_options,omitempty"`
	StrictTransportSecurity   string `json:"strict_transport_security,omitempty"`
	ContentSecurityPolicy     string `json:"content_security_policy,omitempty"`
	ReferrerPolicy            string `json:"referrer_policy,omitempty"`
	PermissionsPolicy         string `json:"permissions_policy,omitempty"`
	CrossOriginEmbedderPolicy string `json:"cross_origin_embedder_policy,omitempty"`
	CrossOriginOpenerPolicy   string `json:"cross_origin_opener_policy,omitempty"`
	CrossOriginResourcePolicy string `json:"cross_origin_resource_policy,omitempty"`
}

// GetSecurityHeaders 获取当前安全头信息
func GetSecurityHeaders(c HTTPContext) *SecurityHeaders {
	return &SecurityHeaders{
		XSSProtection:             c.GetHeader("X-XSS-Protection"),
		ContentTypeOptions:        c.GetHeader("X-Content-Type-Options"),
		XFrameOptions:             c.GetHeader("X-Frame-Options"),
		StrictTransportSecurity:   c.GetHeader("Strict-Transport-Security"),
		ContentSecurityPolicy:     c.GetHeader("Content-Security-Policy"),
		ReferrerPolicy:            c.GetHeader("Referrer-Policy"),
		PermissionsPolicy:         c.GetHeader("Permissions-Policy"),
		CrossOriginEmbedderPolicy: c.GetHeader("Cross-Origin-Embedder-Policy"),
		CrossOriginOpenerPolicy:   c.GetHeader("Cross-Origin-Opener-Policy"),
		CrossOriginResourcePolicy: c.GetHeader("Cross-Origin-Resource-Policy"),
	}
}
