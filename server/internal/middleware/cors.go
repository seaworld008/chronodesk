package middleware

import (
	"net/http"
	"strconv"
	"strings"
)

// CORSConfig CORS 配置结构
type CORSConfig struct {
	// AllowOrigins 允许的源列表
	AllowOrigins []string
	// AllowMethods 允许的 HTTP 方法
	AllowMethods []string
	// AllowHeaders 允许的请求头
	AllowHeaders []string
	// ExposeHeaders 暴露给客户端的响应头
	ExposeHeaders []string
	// AllowCredentials 是否允许发送 Cookie
	AllowCredentials bool
	// MaxAge 预检请求的缓存时间（秒）
	MaxAge int
	// AllowAllOrigins 是否允许所有源（开发模式）
	AllowAllOrigins bool
	// AllowOriginFunc 自定义源验证函数
	AllowOriginFunc func(origin string) bool
}

// DefaultCORSConfig 默认 CORS 配置
func DefaultCORSConfig() *CORSConfig {
	return &CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{
			http.MethodGet,
			http.MethodPost,
			http.MethodPut,
			http.MethodPatch,
			http.MethodDelete,
			http.MethodHead,
			http.MethodOptions,
		},
		AllowHeaders: []string{
			"Origin",
			"Content-Length",
			"Content-Type",
			"Authorization",
			"Accept",
			"Accept-Encoding",
			"Accept-Language",
			"X-Requested-With",
			"X-CSRF-Token",
			"X-Request-ID",
		},
		ExposeHeaders: []string{
			"Content-Length",
			"X-Request-ID",
			"X-Response-Time",
		},
		AllowCredentials: true,
		MaxAge:           86400, // 24 hours
		AllowAllOrigins:  false,
	}
}

// DevelopmentCORSConfig 开发环境 CORS 配置
func DevelopmentCORSConfig() *CORSConfig {
	config := DefaultCORSConfig()
	config.AllowAllOrigins = true
	config.AllowOrigins = []string{"*"}
	return config
}

// ProductionCORSConfig 生产环境 CORS 配置
func ProductionCORSConfig(allowedOrigins []string) *CORSConfig {
	config := DefaultCORSConfig()
	config.AllowAllOrigins = false
	config.AllowOrigins = allowedOrigins
	config.AllowCredentials = true
	return config
}

// CORS CORS 中间件
func CORS(config *CORSConfig) func(HTTPContext) {
	if config == nil {
		config = DefaultCORSConfig()
	}

	return func(c HTTPContext) {
		origin := c.GetHeader("Origin")
		requestMethod := c.GetHeader("Access-Control-Request-Method")
		requestHeaders := c.GetHeader("Access-Control-Request-Headers")

		// 检查是否允许该源
		allowOrigin := ""
		if config.AllowAllOrigins {
			allowOrigin = "*"
		} else if config.AllowOriginFunc != nil {
			if config.AllowOriginFunc(origin) {
				allowOrigin = origin
			}
		} else {
			for _, allowedOrigin := range config.AllowOrigins {
				if allowedOrigin == "*" || allowedOrigin == origin {
					allowOrigin = allowedOrigin
					break
				}
				// 支持通配符匹配
				if strings.Contains(allowedOrigin, "*") {
					if matchWildcard(allowedOrigin, origin) {
						allowOrigin = origin
						break
					}
				}
			}
		}

		// 设置 CORS 响应头
		if allowOrigin != "" {
			setHeader(c, "Access-Control-Allow-Origin", allowOrigin)
		}

		// 设置允许的方法
		if len(config.AllowMethods) > 0 {
			setHeader(c, "Access-Control-Allow-Methods", strings.Join(config.AllowMethods, ", "))
		}

		// 设置允许的请求头
		if len(config.AllowHeaders) > 0 {
			setHeader(c, "Access-Control-Allow-Headers", strings.Join(config.AllowHeaders, ", "))
		}

		// 设置暴露的响应头
		if len(config.ExposeHeaders) > 0 {
			setHeader(c, "Access-Control-Expose-Headers", strings.Join(config.ExposeHeaders, ", "))
		}

		// 设置是否允许凭据
		if config.AllowCredentials {
			setHeader(c, "Access-Control-Allow-Credentials", "true")
		}

		// 设置预检请求缓存时间
		if config.MaxAge > 0 {
			setHeader(c, "Access-Control-Max-Age", strconv.Itoa(config.MaxAge))
		}

		// 处理预检请求
		if getMethod(c) == http.MethodOptions {
			// 验证预检请求
			if requestMethod != "" {
				// 检查请求方法是否被允许
				methodAllowed := false
				for _, method := range config.AllowMethods {
					if method == requestMethod {
						methodAllowed = true
						break
					}
				}
				if !methodAllowed {
					setStatus(c, http.StatusMethodNotAllowed)
					c.Abort()
					return
				}
			}

			if requestHeaders != "" {
				// 检查请求头是否被允许
				headers := strings.Split(requestHeaders, ",")
				for _, header := range headers {
					header = strings.TrimSpace(header)
					headerAllowed := false
					for _, allowedHeader := range config.AllowHeaders {
						if strings.EqualFold(allowedHeader, header) {
							headerAllowed = true
							break
						}
					}
					if !headerAllowed {
						setStatus(c, http.StatusForbidden)
						c.Abort()
						return
					}
				}
			}

			// 预检请求成功
			setStatus(c, http.StatusNoContent)
			c.Abort()
			return
		}

		c.Next()
	}
}

// matchWildcard 通配符匹配
func matchWildcard(pattern, str string) bool {
	if pattern == "*" {
		return true
	}

	// 简单的通配符匹配实现
	if strings.HasPrefix(pattern, "*.") {
		// 匹配子域名，如 *.example.com
		suffix := pattern[1:] // 移除 *
		return strings.HasSuffix(str, suffix)
	}

	if strings.HasSuffix(pattern, "*") {
		// 匹配前缀，如 https://example.*
		prefix := pattern[:len(pattern)-1] // 移除 *
		return strings.HasPrefix(str, prefix)
	}

	return pattern == str
}

// CORSWithOrigins 快速创建指定源的 CORS 中间件
func CORSWithOrigins(origins ...string) func(HTTPContext) {
	config := DefaultCORSConfig()
	config.AllowOrigins = origins
	config.AllowAllOrigins = false
	return CORS(config)
}

// CORSAllowAll 允许所有源的 CORS 中间件（仅用于开发）
func CORSAllowAll() func(HTTPContext) {
	return CORS(DevelopmentCORSConfig())
}

// CORSSecure 安全的 CORS 中间件（用于生产环境）
func CORSSecure(allowedOrigins []string) func(HTTPContext) {
	config := ProductionCORSConfig(allowedOrigins)
	return CORS(config)
}

// ValidateOrigin 验证源是否被允许
func ValidateOrigin(allowedOrigins []string, origin string) bool {
	for _, allowedOrigin := range allowedOrigins {
		if allowedOrigin == "*" || allowedOrigin == origin {
			return true
		}
		if strings.Contains(allowedOrigin, "*") {
			if matchWildcard(allowedOrigin, origin) {
				return true
			}
		}
	}
	return false
}

// CORSResponse CORS 预检响应结构
type CORSResponse struct {
	AllowOrigin      string `json:"allow_origin"`
	AllowMethods     string `json:"allow_methods"`
	AllowHeaders     string `json:"allow_headers"`
	ExposeHeaders    string `json:"expose_headers"`
	AllowCredentials bool   `json:"allow_credentials"`
	MaxAge           int    `json:"max_age"`
}

// GetCORSInfo 获取 CORS 配置信息
func GetCORSInfo(config *CORSConfig, origin string) *CORSResponse {
	if config == nil {
		config = DefaultCORSConfig()
	}

	allowOrigin := ""
	if config.AllowAllOrigins {
		allowOrigin = "*"
	} else if ValidateOrigin(config.AllowOrigins, origin) {
		allowOrigin = origin
	}

	return &CORSResponse{
		AllowOrigin:      allowOrigin,
		AllowMethods:     strings.Join(config.AllowMethods, ", "),
		AllowHeaders:     strings.Join(config.AllowHeaders, ", "),
		ExposeHeaders:    strings.Join(config.ExposeHeaders, ", "),
		AllowCredentials: config.AllowCredentials,
		MaxAge:           config.MaxAge,
	}
}
