package middleware

import (
	"os"
	"strings"
	"time"
)

// MiddlewareConfig 中间件配置
type MiddlewareConfig struct {
	// 环境配置
	Environment string // "development", "production", "test"

	// JWT配置
	JWT *JWTConfig

	// CORS配置
	CORS *CORSConfig

	// 限流配置
	RateLimit *RateLimitConfig

	// 日志配置
	Logger *LoggerConfig

	// 恢复配置
	Recovery *RecoveryConfig

	// 安全配置
	Security *SecurityConfig

	// CSRF配置
	CSRF *CSRFConfig

	// 自定义中间件
	CustomMiddlewares []func(HTTPContext)
}

// JWTConfig JWT配置结构
type JWTConfig struct {
	SecretKey        string
	AccessTokenTTL   time.Duration
	RefreshTokenTTL  time.Duration
	Issuer           string
	SkipPaths        []string
	BlacklistManager BlacklistManager
}

// DefaultMiddlewareConfig 默认中间件配置
func DefaultMiddlewareConfig() *MiddlewareConfig {
	return &MiddlewareConfig{
		Environment: "development",
		JWT: &JWTConfig{
			SecretKey:       "your-secret-key",
			AccessTokenTTL:  time.Hour * 24,
			RefreshTokenTTL: time.Hour * 24 * 7,
			Issuer:          "ticket-system",
			SkipPaths:       []string{"/api/auth/login", "/api/auth/register", "/health"},
		},
		CORS: DefaultCORSConfig(),
		RateLimit: &RateLimitConfig{
			Limiter: NewTokenBucket(100, 10, time.Minute),
			KeyFunc: func(c HTTPContext) string { return getClientIP(c) },
			Headers: true,
		},
		Logger:   DefaultLoggerConfig(),
		Recovery: DefaultRecoveryConfig(),
		Security: DefaultSecurityConfig(),
		CSRF:     DefaultCSRFConfig(),
	}
}

// DevelopmentMiddlewareConfig 开发环境中间件配置
func DevelopmentMiddlewareConfig() *MiddlewareConfig {
	config := DefaultMiddlewareConfig()
	config.Environment = "development"
	config.CORS = DevelopmentCORSConfig()
	config.Security = DevelopmentSecurityConfig()
	config.Logger.Logger = NewSimpleLogger(os.Stdout, LogLevelDebug)
	return config
}

// ProductionMiddlewareConfig 生产环境中间件配置
func ProductionMiddlewareConfig() *MiddlewareConfig {
	config := DefaultMiddlewareConfig()
	config.Environment = "production"
	config.CORS = &CORSConfig{
		AllowOrigins:     []string{"https://yourdomain.com"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           43200, // 12 hours in seconds
	}
	config.Security = ProductionSecurityConfig()
	config.Logger.Logger = NewSimpleLogger(os.Stdout, LogLevelInfo)
	config.Recovery.DisablePrintStack = true
	return config
}

// SetupMiddlewares 设置中间件链
func SetupMiddlewares(config *MiddlewareConfig) []func(HTTPContext) {
	if config == nil {
		config = DefaultMiddlewareConfig()
	}

	middlewares := make([]func(HTTPContext), 0)

	// 1. 请求ID中间件（最先执行）
	middlewares = append(middlewares, RequestIDMiddleware())

	// 2. 恢复中间件（捕获panic）
	if config.Recovery != nil {
		middlewares = append(middlewares, RecoveryMiddleware(config.Recovery))
	}

	// 3. 日志中间件
	if config.Logger != nil {
		middlewares = append(middlewares, LoggingMiddleware(config.Logger))
	}

	// 4. 安全头中间件
	if config.Security != nil {
		middlewares = append(middlewares, SecurityMiddleware(config.Security))
	}

	// 5. CORS中间件
	if config.CORS != nil {
		middlewares = append(middlewares, CORS(config.CORS))
	}

	// 6. 限流中间件
	if config.RateLimit != nil {
		middlewares = append(middlewares, RateLimit(config.RateLimit))
	}

	// 7. CSRF保护中间件
	if config.CSRF != nil {
		middlewares = append(middlewares, CSRFMiddleware(config.CSRF))
	}

	// 8. 自定义中间件
	if config.CustomMiddlewares != nil {
		middlewares = append(middlewares, config.CustomMiddlewares...)
	}

	return middlewares
}

// SetupAuthMiddlewares 设置认证中间件
func SetupAuthMiddlewares(config *MiddlewareConfig) []func(HTTPContext) {
	middlewares := make([]func(HTTPContext), 0)

	// JWT认证中间件
	if config.JWT != nil {
		jwtManager := NewJWTManager(
			config.JWT.SecretKey,
			config.JWT.AccessTokenTTL,
			config.JWT.RefreshTokenTTL,
			config.JWT.Issuer,
		)

		if config.JWT.BlacklistManager != nil {
			middlewares = append(middlewares, JWTWithBlacklist(jwtManager, config.JWT.BlacklistManager))
		} else {
			middlewares = append(middlewares, JWTAuth(jwtManager))
		}
	}

	return middlewares
}

// SetupOptionalAuthMiddlewares 设置可选认证中间件
func SetupOptionalAuthMiddlewares(config *MiddlewareConfig) []func(HTTPContext) {
	middlewares := make([]func(HTTPContext), 0)

	// 可选JWT认证中间件
	if config.JWT != nil {
		jwtManager := NewJWTManager(
			config.JWT.SecretKey,
			config.JWT.AccessTokenTTL,
			config.JWT.RefreshTokenTTL,
			config.JWT.Issuer,
		)
		middlewares = append(middlewares, OptionalJWTAuth(jwtManager))
	}

	return middlewares
}

// EnvironmentConfig 环境配置
type EnvironmentConfig struct {
	IsDevelopment bool
	IsProduction  bool
	IsTest        bool
	LogLevel      LogLevel
	DebugMode     bool
}

// GetEnvironmentConfig 获取环境配置
func GetEnvironmentConfig() *EnvironmentConfig {
	env := os.Getenv("APP_ENV")
	if env == "" {
		env = "development"
	}

	config := &EnvironmentConfig{
		IsDevelopment: env == "development",
		IsProduction:  env == "production",
		IsTest:        env == "test",
		DebugMode:     env == "development",
	}

	// 设置日志级别
	switch env {
	case "production":
		config.LogLevel = LogLevelInfo
	case "test":
		config.LogLevel = LogLevelWarn
	default:
		config.LogLevel = LogLevelDebug
	}

	return config
}

// LoadConfigFromEnv 从环境变量加载配置
func LoadConfigFromEnv() *MiddlewareConfig {
	envConfig := GetEnvironmentConfig()

	var config *MiddlewareConfig
	if envConfig.IsProduction {
		config = ProductionMiddlewareConfig()
	} else {
		config = DevelopmentMiddlewareConfig()
	}

	// 从环境变量覆盖JWT配置
	if secretKey := os.Getenv("JWT_SECRET_KEY"); secretKey != "" {
		config.JWT.SecretKey = secretKey
	}

	if issuer := os.Getenv("JWT_ISSUER"); issuer != "" {
		config.JWT.Issuer = issuer
	}

	if ttlStr := os.Getenv("JWT_ACCESS_TOKEN_TTL"); ttlStr != "" {
		if ttl, err := time.ParseDuration(ttlStr); err == nil {
			config.JWT.AccessTokenTTL = ttl
		}
	}

	if ttlStr := os.Getenv("JWT_REFRESH_TOKEN_TTL"); ttlStr != "" {
		if ttl, err := time.ParseDuration(ttlStr); err == nil {
			config.JWT.RefreshTokenTTL = ttl
		}
	}

	// 从环境变量覆盖CORS配置
	if origins := os.Getenv("CORS_ALLOWED_ORIGINS"); origins != "" {
		config.CORS.AllowOrigins = strings.Split(origins, ",")
	}

	if methods := os.Getenv("CORS_ALLOWED_METHODS"); methods != "" {
		config.CORS.AllowMethods = strings.Split(methods, ",")
	}

	if headers := os.Getenv("CORS_ALLOWED_HEADERS"); headers != "" {
		config.CORS.AllowHeaders = strings.Split(headers, ",")
	}

	// 从环境变量覆盖限流配置
	// 限流配置暂时跳过，因为结构体字段不匹配
	_ = os.Getenv("RATE_LIMIT_REQUESTS")
	_ = os.Getenv("RATE_LIMIT_WINDOW")

	return config
}

// MiddlewareChain 中间件链
type MiddlewareChain struct {
	middlewares []func(HTTPContext)
	index       int
}

// NewMiddlewareChain 创建中间件链
func NewMiddlewareChain(middlewares ...func(HTTPContext)) *MiddlewareChain {
	return &MiddlewareChain{
		middlewares: middlewares,
		index:       0,
	}
}

// Execute 执行中间件链
func (mc *MiddlewareChain) Execute(c HTTPContext) {
	if mc.index < len(mc.middlewares) {
		middleware := mc.middlewares[mc.index]
		mc.index++
		middleware(c)
	}
}

// Reset 重置中间件链
func (mc *MiddlewareChain) Reset() {
	mc.index = 0
}

// Add 添加中间件
func (mc *MiddlewareChain) Add(middleware func(HTTPContext)) {
	mc.middlewares = append(mc.middlewares, middleware)
}

// MiddlewareGroup 中间件组
type MiddlewareGroup struct {
	name        string
	middlewares []func(HTTPContext)
}

// NewMiddlewareGroup 创建中间件组
func NewMiddlewareGroup(name string) *MiddlewareGroup {
	return &MiddlewareGroup{
		name:        name,
		middlewares: make([]func(HTTPContext), 0),
	}
}

// Add 添加中间件到组
func (mg *MiddlewareGroup) Add(middleware func(HTTPContext)) *MiddlewareGroup {
	mg.middlewares = append(mg.middlewares, middleware)
	return mg
}

// GetMiddlewares 获取组内所有中间件
func (mg *MiddlewareGroup) GetMiddlewares() []func(HTTPContext) {
	return mg.middlewares
}

// GetName 获取组名
func (mg *MiddlewareGroup) GetName() string {
	return mg.name
}

// MiddlewareManager 中间件管理器
type MiddlewareManager struct {
	groups map[string]*MiddlewareGroup
	global []func(HTTPContext)
}

// NewMiddlewareManager 创建中间件管理器
func NewMiddlewareManager() *MiddlewareManager {
	return &MiddlewareManager{
		groups: make(map[string]*MiddlewareGroup),
		global: make([]func(HTTPContext), 0),
	}
}

// AddGlobal 添加全局中间件
func (mm *MiddlewareManager) AddGlobal(middleware func(HTTPContext)) {
	mm.global = append(mm.global, middleware)
}

// AddGroup 添加中间件组
func (mm *MiddlewareManager) AddGroup(group *MiddlewareGroup) {
	mm.groups[group.GetName()] = group
}

// GetGroup 获取中间件组
func (mm *MiddlewareManager) GetGroup(name string) *MiddlewareGroup {
	return mm.groups[name]
}

// GetGlobal 获取全局中间件
func (mm *MiddlewareManager) GetGlobal() []func(HTTPContext) {
	return mm.global
}

// GetAll 获取所有中间件（全局 + 指定组）
func (mm *MiddlewareManager) GetAll(groupNames ...string) []func(HTTPContext) {
	middlewares := make([]func(HTTPContext), 0)

	// 添加全局中间件
	middlewares = append(middlewares, mm.global...)

	// 添加指定组的中间件
	for _, groupName := range groupNames {
		if group, exists := mm.groups[groupName]; exists {
			middlewares = append(middlewares, group.GetMiddlewares()...)
		}
	}

	return middlewares
}

// SetupDefaultMiddlewareManager 设置默认中间件管理器
func SetupDefaultMiddlewareManager(config *MiddlewareConfig) *MiddlewareManager {
	manager := NewMiddlewareManager()

	// 全局中间件
	globalMiddlewares := SetupMiddlewares(config)
	for _, middleware := range globalMiddlewares {
		manager.AddGlobal(middleware)
	}

	// 认证中间件组
	authGroup := NewMiddlewareGroup("auth")
	authMiddlewares := SetupAuthMiddlewares(config)
	for _, middleware := range authMiddlewares {
		authGroup.Add(middleware)
	}
	manager.AddGroup(authGroup)

	// 可选认证中间件组
	optionalAuthGroup := NewMiddlewareGroup("optional-auth")
	optionalAuthMiddlewares := SetupOptionalAuthMiddlewares(config)
	for _, middleware := range optionalAuthMiddlewares {
		optionalAuthGroup.Add(middleware)
	}
	manager.AddGroup(optionalAuthGroup)

	// 管理员权限中间件组
	adminGroup := NewMiddlewareGroup("admin")
	if config.JWT != nil {
		jwtManager := NewJWTManager(
			config.JWT.SecretKey,
			config.JWT.AccessTokenTTL,
			config.JWT.RefreshTokenTTL,
			config.JWT.Issuer,
		)
		adminGroup.Add(JWTAuth(jwtManager))
		adminGroup.Add(RequireRole("admin"))
	}
	manager.AddGroup(adminGroup)

	return manager
}

// HealthCheck 健康检查响应
type HealthCheck struct {
	Status    string            `json:"status"`
	Timestamp int64             `json:"timestamp"`
	Version   string            `json:"version,omitempty"`
	Uptime    string            `json:"uptime,omitempty"`
	Checks    map[string]string `json:"checks,omitempty"`
}

// HealthCheckMiddleware 健康检查中间件
func HealthCheckMiddleware(path string) func(HTTPContext) {
	if path == "" {
		path = "/health"
	}

	startTime := time.Now()

	return func(c HTTPContext) {
		if getPath(c) == path {
			uptime := time.Since(startTime)
			health := &HealthCheck{
				Status:    "ok",
				Timestamp: time.Now().Unix(),
				Uptime:    uptime.String(),
				Checks: map[string]string{
					"server": "ok",
				},
			}
			c.JSON(200, health)
			c.Abort()
			return
		}
		c.Next()
	}
}

// MetricsMiddleware 指标中间件
func MetricsMiddleware() func(HTTPContext) {
	return func(c HTTPContext) {
		start := time.Now()
		c.Next()
		duration := time.Since(start)

		// 这里可以记录指标到监控系统
		// 例如：Prometheus, StatsD 等
		_ = duration // 避免未使用变量警告
	}
}
