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
	tb.mu.RLock()
	defer tb.mu.RUnlock()

	b := tb.getBucket(key)
	tb.refillBucket(b)
	return b.tokens
}

// Reset 获取下次重置时间
func (tb *TokenBucket) Reset(key string) time.Time {
	tb.mu.RLock()
	defer tb.mu.RUnlock()

	b := tb.getBucket(key)
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
	sw.mu.RLock()
	defer sw.mu.RUnlock()

	w := sw.getWindow(key)
	sw.cleanExpiredRequests(w, time.Now())
	return w.limit - len(w.requests)
}

// Reset 获取窗口重置时间
func (sw *SlidingWindow) Reset(key string) time.Time {
	sw.mu.RLock()
	defer sw.mu.RUnlock()

	w := sw.getWindow(key)
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
	// 这里需要根据具体框架实现获取客户端IP
	// 暂时返回固定值，实际使用时需要实现
	return "default_key"
}

// IPKeyFunc 基于IP的键生成函数
func IPKeyFunc(c HTTPContext) string {
	// 获取真实IP地址
	ip := c.GetHeader("X-Forwarded-For")
	if ip == "" {
		ip = c.GetHeader("X-Real-IP")
	}
	if ip == "" {
		// 这里需要从连接中获取远程地址
		ip = "unknown"
	}
	return ip
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
	// 这里需要根据具体框架实现获取路由信息
	// 暂时返回IP，实际使用时需要实现
	return IPKeyFunc(c)
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
				"error": "Too many requests",
				"code":  "RATE_LIMIT_EXCEEDED",
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

	// 设置标准的限流响应头
	setHeader(c, "X-RateLimit-Remaining", strconv.Itoa(remaining))
	setHeader(c, "X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
	setHeader(c, "X-RateLimit-Reset-After", strconv.FormatInt(int64(time.Until(reset).Seconds()), 10))
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
