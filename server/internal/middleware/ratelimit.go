package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RateLimiter 限流器接口
type RateLimiter interface {
	Allow(key string) bool
	AllowN(key string, n int) bool
	Remaining(key string) int
	Reset(key string) time.Time
	Cleanup()
}

// TokenBucket 令牌桶限流器
type TokenBucket struct {
	mu          sync.RWMutex
	buckets     map[string]*bucket
	capacity    int           // 桶容量
	refillRate  int           // 每秒补充令牌数
	window      time.Duration // 清理窗口
	lastCleanup time.Time
}

// bucket 单个令牌桶
type bucket struct {
	tokens     int       // 当前令牌数
	lastRefill time.Time // 上次补充时间
	capacity   int       // 桶容量
	refillRate int       // 补充速率
}

// NewTokenBucket 创建令牌桶限流器
func NewTokenBucket(capacity, refillRate int, window time.Duration) *TokenBucket {
	if capacity <= 0 {
		panic("token bucket capacity must be positive")
	}
	if refillRate <= 0 {
		panic("token bucket refill rate must be positive")
	}
	if window <= 0 {
		panic("token bucket cleanup window must be positive")
	}
	return &TokenBucket{
		buckets:     make(map[string]*bucket),
		capacity:    capacity,
		refillRate:  refillRate,
		window:      window,
		lastCleanup: time.Now(),
	}
}

// Allow 检查是否允许请求（消耗1个令牌）
func (tb *TokenBucket) Allow(key string) bool {
	return tb.AllowN(key, 1)
}

// AllowN 检查是否允许请求（消耗n个令牌）
func (tb *TokenBucket) AllowN(key string, n int) bool {
	if n <= 0 {
		return false
	}
	tb.mu.Lock()
	defer tb.mu.Unlock()

	// 定期清理过期的桶
	if time.Since(tb.lastCleanup) > tb.window {
		tb.cleanup()
		tb.lastCleanup = time.Now()
	}

	b := tb.getBucket(key)
	tb.refillBucket(b)

	if b.tokens >= n {
		b.tokens -= n
		return true
	}
	return false
}

// Remaining 获取剩余令牌数
func (tb *TokenBucket) Remaining(key string) int {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	b := tb.getBucket(key)
	tb.refillBucket(b)
	return b.tokens
}

// Reset 获取下次重置时间
func (tb *TokenBucket) Reset(key string) time.Time {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	b := tb.getBucket(key)
	tb.refillBucket(b)
	// 计算下次完全补满的时间
	neededTokens := tb.capacity - b.tokens
	if neededTokens <= 0 {
		return time.Now()
	}
	seconds := float64(neededTokens) / float64(tb.refillRate)
	return time.Now().Add(time.Duration(seconds * float64(time.Second)))
}

// Cleanup 清理过期的桶
func (tb *TokenBucket) Cleanup() {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.cleanup()
}

// getBucket 获取或创建桶
func (tb *TokenBucket) getBucket(key string) *bucket {
	if b, exists := tb.buckets[key]; exists {
		return b
	}

	b := &bucket{
		tokens:     tb.capacity,
		lastRefill: time.Now(),
		capacity:   tb.capacity,
		refillRate: tb.refillRate,
	}
	tb.buckets[key] = b
	return b
}

// refillBucket 补充令牌
func (tb *TokenBucket) refillBucket(b *bucket) {
	now := time.Now()
	elapsed := now.Sub(b.lastRefill)
	tokensToAdd := int(elapsed.Seconds()) * b.refillRate

	if tokensToAdd > 0 {
		b.tokens += tokensToAdd
		if b.tokens > b.capacity {
			b.tokens = b.capacity
		}
		b.lastRefill = now
	}
}

// cleanup 清理过期的桶
func (tb *TokenBucket) cleanup() {
	now := time.Now()
	for key, b := range tb.buckets {
		if now.Sub(b.lastRefill) > tb.window {
			delete(tb.buckets, key)
		}
	}
}

// SlidingWindow 滑动窗口限流器
type SlidingWindow struct {
	mu          sync.RWMutex
	windows     map[string]*window
	limit       int           // 窗口内最大请求数
	window      time.Duration // 窗口大小
	lastCleanup time.Time
}

// window 滑动窗口
type window struct {
	requests   []time.Time   // 请求时间戳列表
	limit      int           // 限制数量
	windowSize time.Duration // 窗口大小
}

// NewSlidingWindow 创建滑动窗口限流器
func NewSlidingWindow(limit int, windowSize time.Duration) *SlidingWindow {
	if limit <= 0 {
		panic("sliding window limit must be positive")
	}
	if windowSize <= 0 {
		panic("sliding window duration must be positive")
	}
	return &SlidingWindow{
		windows:     make(map[string]*window),
		limit:       limit,
		window:      windowSize,
		lastCleanup: time.Now(),
	}
}

// Allow 检查是否允许请求
func (sw *SlidingWindow) Allow(key string) bool {
	return sw.AllowN(key, 1)
}

// AllowN 检查是否允许n个请求
func (sw *SlidingWindow) AllowN(key string, n int) bool {
	if n <= 0 {
		return false
	}
	sw.mu.Lock()
	defer sw.mu.Unlock()

	// 定期清理
	if time.Since(sw.lastCleanup) > sw.window {
		sw.cleanup()
		sw.lastCleanup = time.Now()
	}

	w := sw.getWindow(key)
	now := time.Now()

	// 清理过期请求
	sw.cleanExpiredRequests(w, now)

	// 检查是否超过限制
	if len(w.requests)+n > w.limit {
		return false
	}

	// 添加新请求
	for i := 0; i < n; i++ {
		w.requests = append(w.requests, now)
	}

	return true
}

// Remaining 获取剩余请求数
func (sw *SlidingWindow) Remaining(key string) int {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	w := sw.getWindow(key)
	sw.cleanExpiredRequests(w, time.Now())
	return w.limit - len(w.requests)
}

// Reset 获取窗口重置时间
func (sw *SlidingWindow) Reset(key string) time.Time {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	w := sw.getWindow(key)
	sw.cleanExpiredRequests(w, time.Now())
	if len(w.requests) == 0 {
		return time.Now()
	}

	// 返回最早请求的过期时间
	return w.requests[0].Add(w.windowSize)
}

// Cleanup 清理过期数据
func (sw *SlidingWindow) Cleanup() {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	sw.cleanup()
}

// getWindow 获取或创建窗口
func (sw *SlidingWindow) getWindow(key string) *window {
	if w, exists := sw.windows[key]; exists {
		return w
	}

	w := &window{
		requests:   make([]time.Time, 0),
		limit:      sw.limit,
		windowSize: sw.window,
	}
	sw.windows[key] = w
	return w
}

// cleanExpiredRequests 清理过期请求
func (sw *SlidingWindow) cleanExpiredRequests(w *window, now time.Time) {
	cutoff := now.Add(-w.windowSize)
	validRequests := make([]time.Time, 0, len(w.requests))

	for _, req := range w.requests {
		if req.After(cutoff) {
			validRequests = append(validRequests, req)
		}
	}

	w.requests = validRequests
}

// cleanup 清理空窗口
func (sw *SlidingWindow) cleanup() {
	now := time.Now()
	for key, w := range sw.windows {
		sw.cleanExpiredRequests(w, now)
		if len(w.requests) == 0 {
			delete(sw.windows, key)
		}
	}
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

	// RFC 9333 fields plus the legacy X-RateLimit fields used by existing
	// clients during the non-protocol API transition.
	setHeader(c, "RateLimit-Remaining", strconv.Itoa(remaining))
	setHeader(c, "RateLimit-Reset", strconv.FormatInt(resetSeconds, 10))
	setHeader(c, "X-RateLimit-Remaining", strconv.Itoa(remaining))
	setHeader(c, "X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
	setHeader(c, "X-RateLimit-Reset-After", strconv.FormatInt(resetSeconds, 10))
}

func ceilSeconds(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return int64((duration + time.Second - 1) / time.Second)
}

// TokenBucketRateLimit 令牌桶限流中间件
func TokenBucketRateLimit(capacity, refillRate int, window time.Duration, keyFunc func(HTTPContext) string) func(HTTPContext) {
	limiter := NewTokenBucket(capacity, refillRate, window)
	config := &RateLimitConfig{
		Limiter: limiter,
		KeyFunc: keyFunc,
		Headers: true,
	}
	return RateLimit(config)
}

// SlidingWindowRateLimit 滑动窗口限流中间件
func SlidingWindowRateLimit(limit int, window time.Duration, keyFunc func(HTTPContext) string) func(HTTPContext) {
	limiter := NewSlidingWindow(limit, window)
	config := &RateLimitConfig{
		Limiter: limiter,
		KeyFunc: keyFunc,
		Headers: true,
	}
	return RateLimit(config)
}

// IPRateLimit 基于IP的限流中间件
func IPRateLimit(limit int, window time.Duration) func(HTTPContext) {
	return SlidingWindowRateLimit(limit, window, IPKeyFunc)
}

// UserRateLimit 基于用户的限流中间件
func UserRateLimit(limit int, window time.Duration) func(HTTPContext) {
	return SlidingWindowRateLimit(limit, window, UserKeyFunc)
}

// GlobalRateLimit 全局限流中间件
func GlobalRateLimit(limit int, window time.Duration) func(HTTPContext) {
	return SlidingWindowRateLimit(limit, window, func(c HTTPContext) string {
		return "global"
	})
}

// RateLimitInfo 限流信息
type RateLimitInfo struct {
	Remaining  int       `json:"remaining"`
	Reset      time.Time `json:"reset"`
	ResetAfter int64     `json:"reset_after"`
}

// GetRateLimitInfo 获取限流信息
func GetRateLimitInfo(limiter RateLimiter, key string) *RateLimitInfo {
	remaining := limiter.Remaining(key)
	reset := limiter.Reset(key)
	resetAfter := int64(time.Until(reset).Seconds())

	if resetAfter < 0 {
		resetAfter = 0
	}

	return &RateLimitInfo{
		Remaining:  remaining,
		Reset:      reset,
		ResetAfter: resetAfter,
	}
}
