package config

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/version"
)

// Config 应用配置结构
type Config struct {
	Server    ServerConfig    `json:"server"`
	Database  DatabaseConfig  `json:"database"`
	Redis     RedisConfig     `json:"redis"`
	JWT       JWTConfig       `json:"jwt"`
	SMTP      SMTPConfig      `json:"smtp"`
	OTP       OTPConfig       `json:"otp"`
	Security  SecurityConfig  `json:"security"`
	App       AppConfig       `json:"app"`
	CORS      CORSConfig      `json:"cors"`
	Log       LogConfig       `json:"log"`
	Upload    UploadConfig    `json:"upload"`
	RateLimit RateLimitConfig `json:"rate_limit"`
	Agent     AgentConfig     `json:"agent"`
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Port           string   `json:"port"`
	GinMode        string   `json:"gin_mode"`
	Environment    string   `json:"environment"`
	Debug          bool     `json:"debug"`
	EnableSwagger  bool     `json:"enable_swagger"`
	EnablePprof    bool     `json:"enable_pprof"`
	TrustedProxies []string `json:"trusted_proxies"`
}

// DatabaseConfig 数据库配置
type DatabaseConfig struct {
	Host            string        `json:"host"`
	Port            int           `json:"port"`
	User            string        `json:"user"`
	Password        string        `json:"password"`
	Name            string        `json:"name"`
	SSLMode         string        `json:"ssl_mode"`
	Timezone        string        `json:"timezone"`
	MaxOpenConns    int           `json:"max_open_conns"`
	MaxIdleConns    int           `json:"max_idle_conns"`
	ConnMaxLifetime time.Duration `json:"conn_max_lifetime"`
}

// RedisConfig Redis配置
type RedisConfig struct {
	Host         string        `json:"host"`
	Port         int           `json:"port"`
	Password     string        `json:"password"`
	DB           int           `json:"db"`
	PoolSize     int           `json:"pool_size"`
	MinIdleConns int           `json:"min_idle_conns"`
	PoolTimeout  time.Duration `json:"pool_timeout"`
	IdleTimeout  time.Duration `json:"idle_timeout"`
}

// JWTConfig JWT配置
type JWTConfig struct {
	Secret           string        `json:"secret"`
	RefreshSecret    string        `json:"refresh_secret"`
	ExpiresIn        time.Duration `json:"expires_in"`
	RefreshExpiresIn time.Duration `json:"refresh_expires_in"`
}

// SMTPConfig 邮件配置
type SMTPConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	FromName string `json:"from_name"`
}

// OTPConfig OTP配置
type OTPConfig struct {
	ExpiresIn time.Duration `json:"expires_in"`
	Length    int           `json:"length"`
}

// SecurityConfig 安全配置
type SecurityConfig struct {
	BcryptCost int    `json:"bcrypt_cost"`
	CSRFSecret string `json:"csrf_secret"`
}

// AppConfig 应用配置
type AppConfig struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	URL     string `json:"url"`
	WebURL  string `json:"web_url"`
}

// CORSConfig CORS配置
type CORSConfig struct {
	AllowedOrigins []string `json:"allowed_origins"`
	AllowedMethods []string `json:"allowed_methods"`
	AllowedHeaders []string `json:"allowed_headers"`
}

// LogConfig 日志配置
type LogConfig struct {
	Level  string `json:"level"`
	Format string `json:"format"`
	Output string `json:"output"`
}

// UploadConfig 文件上传配置
type UploadConfig struct {
	MaxSize      string   `json:"max_size"`
	AllowedTypes []string `json:"allowed_types"`
}

// RateLimitConfig 限流配置
type RateLimitConfig struct {
	Requests          int           `json:"requests"`
	Window            time.Duration `json:"window"`
	AnonymousRequests int           `json:"anonymous_requests"`
	AnonymousWindow   time.Duration `json:"anonymous_window"`
}

// AgentConfig controls machine identities and protocol endpoints separately
// from human browser sessions.
type AgentConfig struct {
	JWTSecret           string        `json:"-"`
	CredentialPepper    string        `json:"-"`
	Issuer              string        `json:"issuer"`
	MCPResourceURL      string        `json:"mcp_resource_url"`
	APIResourceURL      string        `json:"api_resource_url"`
	A2AResourceURL      string        `json:"a2a_resource_url"`
	TokenTTL            time.Duration `json:"token_ttl"`
	CredentialTTL       time.Duration `json:"credential_ttl"`
	CompatibilityUserID uint          `json:"compatibility_user_id"`
	AttachmentDir       string        `json:"attachment_dir"`
	MaxAttachmentBytes  int64         `json:"max_attachment_bytes"`
	LoopThreshold       int           `json:"loop_threshold"`
	LoopWindow          time.Duration `json:"loop_window"`
	GlobalReadOnly      bool          `json:"global_read_only"`
}

// Load 加载配置
func Load() (*Config, error) {
	// 加载 .env 文件
	if err := loadEnvFile(".env"); err != nil {
		// 在生产环境中，.env 文件可能不存在，这是正常的
		fmt.Printf("Warning: .env file not found: %v\n", err)
	}

	appURL := getEnv("APP_URL", "http://localhost:8081")
	config := &Config{
		Server: ServerConfig{
			Port:           getEnv("PORT", "8081"),
			GinMode:        getEnv("GIN_MODE", "debug"),
			Environment:    getEnv("ENVIRONMENT", "development"),
			Debug:          getEnvAsBool("DEBUG", true),
			EnableSwagger:  getEnvAsBool("ENABLE_SWAGGER", true),
			EnablePprof:    getEnvAsBool("ENABLE_PPROF", false),
			TrustedProxies: getEnvAsSlice("TRUSTED_PROXIES", []string{}),
		},
		Database: DatabaseConfig{
			Host:            getEnv("DB_HOST", "localhost"),
			Port:            getEnvAsInt("DB_PORT", 5432),
			User:            getEnv("DB_USER", "chronodesk"),
			Password:        getEnv("DB_PASSWORD", "chronodesk_dev_only"),
			Name:            getEnv("DB_NAME", "chronodesk"),
			SSLMode:         getEnv("DB_SSLMODE", "disable"),
			Timezone:        getEnv("DB_TIMEZONE", "Asia/Shanghai"),
			MaxOpenConns:    getEnvAsInt("DB_MAX_OPEN_CONNS", 25),
			MaxIdleConns:    getEnvAsInt("DB_MAX_IDLE_CONNS", 5),
			ConnMaxLifetime: getEnvAsDuration("DB_CONN_MAX_LIFETIME", 5*time.Minute),
		},
		Redis: RedisConfig{
			Host:         getEnv("REDIS_HOST", "localhost"),
			Port:         getEnvAsInt("REDIS_PORT", 6379),
			Password:     getEnv("REDIS_PASSWORD", ""),
			DB:           getEnvAsInt("REDIS_DB", 0),
			PoolSize:     getEnvAsInt("REDIS_POOL_SIZE", 10),
			MinIdleConns: getEnvAsInt("REDIS_MIN_IDLE_CONNS", 5),
			PoolTimeout:  getEnvAsDuration("REDIS_POOL_TIMEOUT", 4*time.Second),
			IdleTimeout:  getEnvAsDuration("REDIS_IDLE_TIMEOUT", 5*time.Minute),
		},
		JWT: JWTConfig{
			Secret:           getEnv("JWT_SECRET", "your-super-secret-jwt-key-change-in-production"),
			RefreshSecret:    getEnv("JWT_REFRESH_SECRET", "your-super-secret-jwt-refresh-key-change-in-production"),
			ExpiresIn:        getEnvAsDuration("JWT_EXPIRES_IN", 24*time.Hour),
			RefreshExpiresIn: getEnvAsDuration("JWT_REFRESH_EXPIRES_IN", 168*time.Hour),
		},
		SMTP: SMTPConfig{
			Host:     getEnv("SMTP_HOST", "smtp.gmail.com"),
			Port:     getEnvAsInt("SMTP_PORT", 587),
			Username: getEnv("SMTP_USERNAME", ""),
			Password: getEnv("SMTP_PASSWORD", ""),
			From:     getEnv("SMTP_FROM", "noreply@chronodesk.local"),
			FromName: getEnv("SMTP_FROM_NAME", "ChronoDesk"),
		},
		OTP: OTPConfig{
			ExpiresIn: getEnvAsDuration("OTP_EXPIRES_IN", 10*time.Minute),
			Length:    getEnvAsInt("OTP_LENGTH", 6),
		},
		Security: SecurityConfig{
			BcryptCost: getEnvAsInt("BCRYPT_COST", 12),
			CSRFSecret: getEnv("CSRF_SECRET", "your-csrf-secret-key"),
		},
		App: AppConfig{
			Name:    getEnv("APP_NAME", "ChronoDesk"),
			Version: getEnv("APP_VERSION", version.Version),
			URL:     appURL,
			WebURL:  getEnv("WEB_URL", "http://localhost:3000"),
		},
		CORS: CORSConfig{
			AllowedOrigins: getEnvAsSlice("CORS_ALLOWED_ORIGINS", []string{"http://localhost:3000", "http://localhost:5173"}),
			AllowedMethods: getEnvAsSlice("CORS_ALLOWED_METHODS", []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}),
			AllowedHeaders: getEnvAsSlice("CORS_ALLOWED_HEADERS", []string{
				"Origin",
				"Content-Type",
				"Accept",
				"Authorization",
				"X-Requested-With",
				"Idempotency-Key",
				"If-Match",
				"X-Ticket-Lease",
				"MCP-Protocol-Version",
				"Mcp-Method",
				"Mcp-Name",
			}),
		},
		Log: LogConfig{
			Level:  getEnv("LOG_LEVEL", "info"),
			Format: getEnv("LOG_FORMAT", "json"),
			Output: getEnv("LOG_OUTPUT", "stdout"),
		},
		Upload: UploadConfig{
			MaxSize:      getEnv("UPLOAD_MAX_SIZE", "10MB"),
			AllowedTypes: getEnvAsSlice("UPLOAD_ALLOWED_TYPES", []string{"jpg", "jpeg", "png", "gif", "pdf", "doc", "docx"}),
		},
		RateLimit: RateLimitConfig{
			Requests:          getEnvAsInt("RATE_LIMIT_REQUESTS", 600),
			Window:            getEnvAsDuration("RATE_LIMIT_WINDOW", time.Minute),
			AnonymousRequests: getEnvAsInt("AUTH_RATE_LIMIT_REQUESTS", 20),
			AnonymousWindow:   getEnvAsDuration("AUTH_RATE_LIMIT_WINDOW", time.Minute),
		},
		Agent: AgentConfig{
			JWTSecret:        getEnv("AGENT_JWT_SECRET", getEnv("JWT_SECRET", "your-super-secret-jwt-key-change-in-production")),
			CredentialPepper: getEnv("AGENT_CREDENTIAL_PEPPER", getEnv("AGENT_JWT_SECRET", getEnv("JWT_SECRET", "your-super-secret-jwt-key-change-in-production"))),
			Issuer:           getEnv("AGENT_ISSUER", appURL),
			// RFC 8707 resource identifiers are derived from the canonical public
			// origin. They are intentionally not independently configurable:
			// MCP, REST, and A2A tokens must never share an audience.
			MCPResourceURL:      appURL + "/mcp",
			APIResourceURL:      appURL + "/api/v1",
			A2AResourceURL:      appURL + "/a2a/v1",
			TokenTTL:            getEnvAsDuration("AGENT_TOKEN_TTL", 15*time.Minute),
			CredentialTTL:       getEnvAsDuration("AGENT_CREDENTIAL_TTL", 90*24*time.Hour),
			CompatibilityUserID: uint(getEnvAsInt("AGENT_COMPATIBILITY_USER_ID", 1)),
			AttachmentDir:       getEnv("AGENT_ATTACHMENT_DIR", "./data/agent-attachments"),
			MaxAttachmentBytes:  int64(getEnvAsInt("AGENT_MAX_ATTACHMENT_BYTES", 25<<20)),
			LoopThreshold:       getEnvAsInt("AGENT_LOOP_THRESHOLD", 20),
			LoopWindow:          getEnvAsDuration("AGENT_LOOP_WINDOW", time.Minute),
			GlobalReadOnly:      getEnvAsBool("AGENT_GLOBAL_READ_ONLY", false),
		},
	}

	// 验证配置
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return config, nil
}

// Validate 验证配置
func (c *Config) Validate() error {
	// 验证必需的配置项
	if c.JWT.Secret == "your-super-secret-jwt-key-change-in-production" && c.Server.Environment == "production" {
		return fmt.Errorf("JWT secret must be changed in production environment")
	}
	if c.JWT.RefreshSecret == "your-super-secret-jwt-refresh-key-change-in-production" && c.Server.Environment == "production" {
		return fmt.Errorf("JWT refresh secret must be changed in production environment")
	}
	if c.Agent.JWTSecret == "your-super-secret-jwt-key-change-in-production" && c.Server.Environment == "production" {
		return fmt.Errorf("agent JWT secret must be changed in production environment")
	}
	if c.Server.Environment == "production" {
		if insecureProductionSecret(c.Agent.JWTSecret) {
			return fmt.Errorf("agent JWT secret must be at least 32 non-placeholder characters")
		}
		if insecureProductionSecret(c.Agent.CredentialPepper) {
			return fmt.Errorf("agent credential pepper must be at least 32 non-placeholder characters")
		}
		if c.Agent.JWTSecret == c.JWT.Secret || c.Agent.CredentialPepper == c.Agent.JWTSecret {
			return fmt.Errorf("human JWT, Agent JWT, and Agent credential pepper must use separate production secrets")
		}
	}
	if err := validateAgentEndpointContract(
		c.App.URL,
		c.Agent.Issuer,
		c.Agent.MCPResourceURL,
		c.Agent.APIResourceURL,
		c.Agent.A2AResourceURL,
	); err != nil {
		return err
	}
	if c.Agent.TokenTTL <= 0 || c.Agent.TokenTTL > time.Hour {
		return fmt.Errorf("agent token TTL must be between 1 second and 1 hour")
	}
	if c.Agent.CredentialTTL < time.Hour || c.Agent.CredentialTTL > 365*24*time.Hour {
		return fmt.Errorf("agent credential TTL must be between 1 hour and 365 days")
	}
	if c.Agent.MaxAttachmentBytes <= 0 {
		return fmt.Errorf("agent maximum attachment size must be positive")
	}
	if c.Agent.LoopThreshold <= 0 || c.Agent.LoopWindow <= 0 {
		return fmt.Errorf("agent loop threshold and window must be positive")
	}
	if c.RateLimit.Requests <= 0 ||
		c.RateLimit.Window <= 0 ||
		c.RateLimit.AnonymousRequests <= 0 ||
		c.RateLimit.AnonymousWindow <= 0 {
		return fmt.Errorf("authenticated and anonymous rate limit requests and windows must be positive")
	}

	if c.Database.Host == "" {
		return fmt.Errorf("database host is required")
	}

	if c.Database.User == "" {
		return fmt.Errorf("database user is required")
	}

	if c.Database.Name == "" {
		return fmt.Errorf("database name is required")
	}

	if c.Redis.Host == "" {
		return fmt.Errorf("redis host is required")
	}

	return nil
}

func validateAgentEndpointContract(appURL, issuer, mcpResourceURL, apiResourceURL, a2aResourceURL string) error {
	if appURL != strings.TrimSpace(appURL) {
		return fmt.Errorf("APP URL must not contain surrounding whitespace")
	}
	parsed, err := url.Parse(appURL)
	if err != nil ||
		!parsed.IsAbs() ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		parsed.Path != "" ||
		parsed.RawPath != "" {
		return fmt.Errorf("APP URL must be an absolute canonical origin without path, query, fragment, or user info")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHostname(parsed.Hostname())) {
		return fmt.Errorf("APP URL must use HTTPS except for loopback development")
	}
	if issuer != appURL {
		return fmt.Errorf("agent issuer must exactly match APP URL")
	}
	expectedResources := map[string]struct {
		got  string
		path string
	}{
		"MCP": {got: mcpResourceURL, path: "/mcp"},
		"API": {got: apiResourceURL, path: "/api/v1"},
		"A2A": {got: a2aResourceURL, path: "/a2a/v1"},
	}
	for name, resource := range expectedResources {
		expected := appURL + resource.path
		if resource.got != expected {
			return fmt.Errorf("agent %s resource URL must exactly match %q", name, expected)
		}
	}
	return nil
}

func isLoopbackHostname(hostname string) bool {
	switch strings.ToLower(hostname) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func insecureProductionSecret(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	return len(value) < 32 ||
		strings.Contains(normalized, "replace-with") ||
		strings.Contains(normalized, "change-in-production") ||
		strings.Contains(normalized, "example")
}

// GetDSN 获取数据库连接字符串
func (c *Config) GetDSN() string {
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s TimeZone=%s",
		c.Database.Host,
		c.Database.Port,
		c.Database.User,
		c.Database.Password,
		c.Database.Name,
		c.Database.SSLMode,
		c.Database.Timezone,
	)
}

// GetRedisAddr 获取Redis地址
func (c *Config) GetRedisAddr() string {
	return fmt.Sprintf("%s:%d", c.Redis.Host, c.Redis.Port)
}

// IsDevelopment 是否为开发环境
func (c *Config) IsDevelopment() bool {
	return c.Server.Environment == "development"
}

// IsProduction 是否为生产环境
func (c *Config) IsProduction() bool {
	return c.Server.Environment == "production"
}

// 辅助函数
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvAsBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
		}
	}
	return defaultValue
}

func getEnvAsDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}

func getEnvAsSlice(key string, defaultValue []string) []string {
	if value := os.Getenv(key); value != "" {
		return strings.Split(value, ",")
	}
	return defaultValue
}

// loadEnvFile 简单的.env文件加载器
func loadEnvFile(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// 跳过空行和注释
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// 解析键值对
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			// 只有当环境变量不存在时才设置
			if os.Getenv(key) == "" {
				os.Setenv(key, value)
			}
		}
	}

	return scanner.Err()
}
