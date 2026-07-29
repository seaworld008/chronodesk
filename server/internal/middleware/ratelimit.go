package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RateLimiter 限流器接口
type RateLimiter interface {
	Allow(key string) bool
	AllowN(key string, n int) bool
	Limit() int
	Remaining(key string) int
	Reset(key string) time.Time
}

// RateLimitConfig 限流配置
type RateLimitConfig struct {
	// Limiter 限流器实例
	Limiter RateLimiter
	// KeyFunc 生成限流键的函数
	KeyFunc func(HTTPContext) string
	// SkipFunc 跳过限流的函数
	SkipFunc func(HTTPContext) bool
	// ErrorHandler 错误处理函数
	ErrorHandler func(HTTPContext)
	// Headers 是否在响应中包含限流信息
	Headers bool
}

// DefaultKeyFunc 默认键生成函数（基于IP）
func DefaultKeyFunc(c HTTPContext) string {
	return getClientIP(c)
}

// IPKeyFunc 基于IP的键生成函数
func IPKeyFunc(c HTTPContext) string {
	// Gin's ClientIP honours the application's explicit trusted-proxy list.
	// Reading X-Forwarded-For directly would allow untrusted clients to choose
	// their own rate-limit bucket.
	return getClientIP(c)
}

// UserKeyFunc 基于用户ID的键生成函数
func UserKeyFunc(c HTTPContext) string {
	userID, exists := c.Get("user_id")
	if !exists {
		return IPKeyFunc(c) // 回退到IP限流
	}

	if id, ok := userID.(uint); ok {
		return fmt.Sprintf("user_%d", id)
	}
	return IPKeyFunc(c)
}

// RouteKeyFunc 基于路由的键生成函数
func RouteKeyFunc(c HTTPContext) string {
	return IPKeyFunc(c) + "|" + getRoutePattern(c)
}

// UserRouteKeyFunc isolates authenticated users behind the same enterprise
// proxy/NAT and prevents unrelated endpoints from exhausting one shared
// bucket. Before authentication it safely falls back to the trusted client IP.
func UserRouteKeyFunc(c HTTPContext) string {
	return UserKeyFunc(c) + "|" + getRoutePattern(c)
}

// MachineIdentityRouteKeyFunc builds a strict key for one verified machine
// identity dimension, such as a service principal or credential. Missing
// identity returns an empty key so the Redis limiter fails closed; it never
// falls back to a human user ID or remote IP bucket.
func MachineIdentityRouteKeyFunc(contextKey, dimension string) func(HTTPContext) string {
	contextKey = strings.TrimSpace(contextKey)
	dimension = strings.TrimSpace(dimension)
	return func(c HTTPContext) string {
		if contextKey == "" || dimension == "" {
			return ""
		}
		value, exists := c.Get(contextKey)
		if !exists {
			return ""
		}
		subject, ok := value.(string)
		subject = strings.TrimSpace(subject)
		if !ok || subject == "" {
			return ""
		}
		return "machine_" + dimension + "_" + subject + "|" + getRoutePattern(c)
	}
}

func getRoutePattern(c HTTPContext) string {
	if ginContext, ok := c.(*GinHTTPContext); ok {
		if route := ginContext.Context.FullPath(); route != "" {
			return route
		}
	}
	return getPath(c)
}

// RateLimit 限流中间件
func RateLimit(config *RateLimitConfig) func(HTTPContext) {
	if config == nil {
		panic("rate limit config cannot be nil")
	}

	if config.Limiter == nil {
		panic("rate limiter cannot be nil")
	}

	if config.KeyFunc == nil {
		config.KeyFunc = DefaultKeyFunc
	}

	if config.ErrorHandler == nil {
		config.ErrorHandler = func(c HTTPContext) {
			c.JSON(http.StatusTooManyRequests, map[string]interface{}{
				"error":   "rate_limit_exceeded",
				"code":    "RATE_LIMIT_EXCEEDED",
				"message": "请求过于频繁，请稍后重试",
			})
		}
	}

	return func(c HTTPContext) {
		// 检查是否跳过限流
		if config.SkipFunc != nil && config.SkipFunc(c) {
			c.Next()
			return
		}

		key := config.KeyFunc(c)
		if !config.Limiter.Allow(key) {
			// 设置限流信息头
			if config.Headers {
				setRateLimitHeaders(c, config.Limiter, key)
				retryAfter := time.Until(config.Limiter.Reset(key))
				if retryAfter < time.Second {
					retryAfter = time.Second
				}
				setHeader(c, "Retry-After", strconv.FormatInt(ceilSeconds(retryAfter), 10))
			}

			config.ErrorHandler(c)
			c.Abort()
			return
		}

		// 设置限流信息头
		if config.Headers {
			setRateLimitHeaders(c, config.Limiter, key)
		}

		c.Next()
	}
}

// setRateLimitHeaders 设置限流相关的响应头
func setRateLimitHeaders(c HTTPContext, limiter RateLimiter, key string) {
	remaining := limiter.Remaining(key)
	reset := limiter.Reset(key)
	resetAfter := time.Until(reset)
	if resetAfter < 0 {
		resetAfter = 0
	}
	resetSeconds := ceilSeconds(resetAfter)

	setHeader(c, "RateLimit-Limit", strconv.Itoa(limiter.Limit()))
	setHeader(c, "RateLimit-Remaining", strconv.Itoa(remaining))
	setHeader(c, "RateLimit-Reset", strconv.FormatInt(resetSeconds, 10))
}

func ceilSeconds(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return int64((duration + time.Second - 1) / time.Second)
}
