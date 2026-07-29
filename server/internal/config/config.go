package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/seaworld008/chronodesk/server/internal/version"
)

// Config 应用配置结构
type Config struct {
	Server    ServerConfig    `json:"server"`
	Database  DatabaseConfig  `json:"database"`
	Redis     RedisConfig     `json:"redis"`
	JWT       JWTConfig       `json:"jwt"`
	Security  SecurityConfig  `json:"security"`
	App       AppConfig       `json:"app"`
	CORS      CORSConfig      `json:"cors"`
	RateLimit RateLimitConfig `json:"rate_limit"`
	Agent     AgentConfig     `json:"agent"`
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Port           string   `json:"port"`
	GinMode        string   `json:"gin_mode"`
	Environment    string   `json:"environment"`
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
	Issuer           string        `json:"issuer"`
	Audience         string        `json:"audience"`
}

// SecurityConfig 安全配置
type SecurityConfig struct {
	BcryptCost int `json:"bcrypt_cost"`
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

// RateLimitConfig 限流配置
type RateLimitConfig struct {
	Requests                  int           `json:"requests"`
	Window                    time.Duration `json:"window"`
	AnonymousIdentityRequests int           `json:"anonymous_identity_requests"`
	AnonymousIPRequests       int           `json:"anonymous_ip_requests"`
	AnonymousWindow           time.Duration `json:"anonymous_window"`
}

// AgentConfig controls machine identities and protocol endpoints separately
// from human browser sessions.
type AgentConfig struct {
	JWTSecret          string        `json:"-"`
	CredentialPepper   string        `json:"-"`
	Issuer             string        `json:"issuer"`
	MCPResourceURL     string        `json:"mcp_resource_url"`
	APIResourceURL     string        `json:"api_resource_url"`
	A2AResourceURL     string        `json:"a2a_resource_url"`
	TokenTTL           time.Duration `json:"token_ttl"`
	CredentialTTL      time.Duration `json:"credential_ttl"`
	AttachmentDir      string        `json:"attachment_dir"`
	MaxAttachmentBytes int64         `json:"max_attachment_bytes"`
	LoopThreshold      int           `json:"loop_threshold"`
	LoopWindow         time.Duration `json:"loop_window"`
	GlobalReadOnly     bool          `json:"global_read_only"`
}

// Load 加载配置
func Load() (*Config, error) {
	// 使用与迁移和维护命令一致的 dotenv 解析器，正确处理引号、转义和
	// `export KEY=value`。部署环境通常没有 .env；缺失文件不是错误。
	if err := godotenv.Load(".env"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load .env: %w", err)
	}

	appURL := getEnv("APP_URL", "http://localhost:8081")
	bcryptCost, err := getEnvAsStrictInt("BCRYPT_COST", 12)
	if err != nil {
		return nil, err
	}
	config := &Config{
		Server: ServerConfig{
			Port:           getEnv("PORT", "8081"),
			GinMode:        getEnv("GIN_MODE", "debug"),
			Environment:    getEnv("ENVIRONMENT", "development"),
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
			Issuer:           appURL,
			Audience:         appURL + "/api",
		},
		Security: SecurityConfig{
			BcryptCost: bcryptCost,
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
		RateLimit: RateLimitConfig{
			Requests:                  getEnvAsInt("RATE_LIMIT_REQUESTS", 600),
			Window:                    getEnvAsDuration("RATE_LIMIT_WINDOW", time.Minute),
			AnonymousIdentityRequests: getEnvAsInt("AUTH_IDENTITY_RATE_LIMIT_REQUESTS", 20),
			AnonymousIPRequests:       getEnvAsInt("AUTH_IP_RATE_LIMIT_REQUESTS", 200),
			AnonymousWindow:           getEnvAsDuration("AUTH_RATE_LIMIT_WINDOW", time.Minute),
		},
		Agent: AgentConfig{
			JWTSecret:        getEnv("AGENT_JWT_SECRET", getEnv("JWT_SECRET", "your-super-secret-jwt-key-change-in-production")),
			CredentialPepper: getEnv("AGENT_CREDENTIAL_PEPPER", getEnv("AGENT_JWT_SECRET", getEnv("JWT_SECRET", "your-super-secret-jwt-key-change-in-production"))),
			Issuer:           getEnv("AGENT_ISSUER", appURL),
			// RFC 8707 resource identifiers are derived from the canonical public
			// origin. They are intentionally not independently configurable:
			// MCP, REST, and A2A tokens must never share an audience.
			MCPResourceURL:     appURL + "/mcp",
			APIResourceURL:     appURL + "/api/v1",
			A2AResourceURL:     appURL + "/a2a/v1",
			TokenTTL:           getEnvAsDuration("AGENT_TOKEN_TTL", 15*time.Minute),
			CredentialTTL:      getEnvAsDuration("AGENT_CREDENTIAL_TTL", 90*24*time.Hour),
			AttachmentDir:      getEnv("AGENT_ATTACHMENT_DIR", "./data/agent-attachments"),
			MaxAttachmentBytes: int64(getEnvAsInt("AGENT_MAX_ATTACHMENT_BYTES", 25<<20)),
			LoopThreshold:      getEnvAsInt("AGENT_LOOP_THRESHOLD", 20),
			LoopWindow:         getEnvAsDuration("AGENT_LOOP_WINDOW", time.Minute),
			GlobalReadOnly:     getEnvAsBool("AGENT_GLOBAL_READ_ONLY", false),
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
	if insecureHumanJWTSecret(c.JWT.Secret) {
		return fmt.Errorf("human JWT access secret must be at least 32 characters without surrounding whitespace")
	}
	if insecureHumanJWTSecret(c.JWT.RefreshSecret) {
		return fmt.Errorf("human JWT refresh secret must be at least 32 characters without surrounding whitespace")
	}
	if c.JWT.Secret == c.JWT.RefreshSecret {
		return fmt.Errorf("human JWT access and refresh secrets must be different")
	}
	if c.JWT.ExpiresIn <= 0 || c.JWT.RefreshExpiresIn <= 0 {
		return fmt.Errorf("human JWT access and refresh expiration must be positive")
	}
	if c.Security.BcryptCost < 10 || c.Security.BcryptCost > 16 {
		return fmt.Errorf("bcrypt cost must be between 10 and 16")
	}
	if c.Server.Environment == "production" {
		secrets := []struct {
			name  string
			value string
		}{
			{name: "human JWT access secret", value: c.JWT.Secret},
			{name: "human JWT refresh secret", value: c.JWT.RefreshSecret},
			{name: "Agent JWT secret", value: c.Agent.JWTSecret},
			{name: "Agent credential pepper", value: c.Agent.CredentialPepper},
		}
		for _, secret := range secrets {
			if insecureProductionSecret(secret.value) {
				return fmt.Errorf("%s must be at least 32 non-placeholder characters", secret.name)
			}
		}
		for left := 0; left < len(secrets); left++ {
			for right := left + 1; right < len(secrets); right++ {
				if secrets[left].value == secrets[right].value {
					return fmt.Errorf(
						"%s and %s must use separate production secrets",
						secrets[left].name,
						secrets[right].name,
					)
				}
			}
		}
	}
	if err := validateHumanJWTEndpointContract(
		c.App.URL,
		c.JWT.Issuer,
		c.JWT.Audience,
	); err != nil {
		return err
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
		c.RateLimit.AnonymousIdentityRequests <= 0 ||
		c.RateLimit.AnonymousIPRequests <= 0 ||
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

func validateHumanJWTEndpointContract(appURL, issuer, audience string) error {
	if err := validateCanonicalAppURL(appURL); err != nil {
		return err
	}
	if issuer != appURL {
		return fmt.Errorf("human JWT issuer must exactly match APP URL")
	}
	expectedAudience := appURL + "/api"
	if audience != expectedAudience {
		return fmt.Errorf("human JWT audience must exactly match %q", expectedAudience)
	}
	return nil
}

func validateAgentEndpointContract(appURL, issuer, mcpResourceURL, apiResourceURL, a2aResourceURL string) error {
	if err := validateCanonicalAppURL(appURL); err != nil {
		return err
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

func validateCanonicalAppURL(appURL string) error {
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
	trimmed := strings.TrimSpace(value)
	normalized := strings.ToLower(trimmed)
	return value != trimmed ||
		len(trimmed) < 32 ||
		strings.Contains(normalized, "replace-with") ||
		strings.Contains(normalized, "change-in-production") ||
		strings.Contains(normalized, "example")
}

func insecureHumanJWTSecret(value string) bool {
	trimmed := strings.TrimSpace(value)
	return value != trimmed || len(trimmed) < 32
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

func getEnvAsStrictInt(key string, defaultValue int) (int, error) {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue, nil
	}
	intValue, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return intValue, nil
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
