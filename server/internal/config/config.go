package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/seaworld008/chronodesk/server/internal/version"
	"golang.org/x/net/publicsuffix"
)

// Config 应用配置结构
type Config struct {
	Server        ServerConfig        `json:"server"`
	Database      DatabaseConfig      `json:"database"`
	Redis         RedisConfig         `json:"redis"`
	JWT           JWTConfig           `json:"jwt"`
	Security      SecurityConfig      `json:"security"`
	App           AppConfig           `json:"app"`
	CORS          CORSConfig          `json:"cors"`
	RateLimit     RateLimitConfig     `json:"rate_limit"`
	Agent         AgentConfig         `json:"agent"`
	Knowledge     KnowledgeConfig     `json:"knowledge"`
	Observability ObservabilityConfig `json:"observability"`
	Integration   IntegrationConfig   `json:"integration"`
	AuditExport   AuditExportConfig   `json:"audit_export"`
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
	RuntimeURL      string        `json:"-"`
	MigrationURL    string        `json:"-"`
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
	BackupCodeRequests        int           `json:"backup_code_requests"`
	BackupCodeWindow          time.Duration `json:"backup_code_window"`
}

// AgentConfig controls machine identities and protocol endpoints separately
// from human browser sessions.
type AgentConfig struct {
	JWTSecret                     string                    `json:"-"`
	CredentialPepper              string                    `json:"-"`
	Issuer                        string                    `json:"issuer"`
	MCPResourceURL                string                    `json:"mcp_resource_url"`
	APIResourceURL                string                    `json:"api_resource_url"`
	A2AResourceURL                string                    `json:"a2a_resource_url"`
	TokenTTL                      time.Duration             `json:"token_ttl"`
	CredentialTTL                 time.Duration             `json:"credential_ttl"`
	AttachmentStorageBackend      string                    `json:"attachment_storage_backend"`
	AttachmentLocalStoreID        string                    `json:"attachment_local_store_id"`
	AttachmentDir                 string                    `json:"attachment_dir"`
	AttachmentStagingDir          string                    `json:"attachment_staging_dir"`
	AttachmentLocalDeploymentMode string                    `json:"attachment_local_deployment_mode"`
	AttachmentS3Endpoint          string                    `json:"attachment_s3_endpoint"`
	AttachmentS3StoreID           string                    `json:"attachment_s3_store_id"`
	AttachmentS3Region            string                    `json:"attachment_s3_region"`
	AttachmentS3Bucket            string                    `json:"attachment_s3_bucket"`
	AttachmentS3Prefix            string                    `json:"attachment_s3_prefix"`
	AttachmentS3UsePathStyle      bool                      `json:"attachment_s3_use_path_style"`
	AttachmentS3AllowInsecure     bool                      `json:"attachment_s3_allow_insecure"`
	AttachmentS3AccessKeyID       string                    `json:"-"`
	AttachmentS3SecretAccessKey   string                    `json:"-"`
	AttachmentS3SessionToken      string                    `json:"-"`
	AttachmentS3SSE               string                    `json:"attachment_s3_sse"`
	AttachmentS3KMSKeyID          string                    `json:"-"`
	AttachmentS3VersioningMode    string                    `json:"attachment_s3_versioning_mode"`
	AttachmentS3HistoricalStores  []AttachmentS3StoreConfig `json:"-"`
	MaxAttachmentBytes            int64                     `json:"max_attachment_bytes"`
	LoopThreshold                 int                       `json:"loop_threshold"`
	LoopWindow                    time.Duration             `json:"loop_window"`
	GlobalReadOnly                bool                      `json:"global_read_only"`
}

// AttachmentS3StoreConfig is one deployment-owned historical object-store
// generation. It is never serialized because it may contain static
// credentials. At most a small bounded registry is loaded at startup.
type AttachmentS3StoreConfig struct {
	StoreID               string `json:"store_id"`
	Endpoint              string `json:"endpoint,omitempty"`
	Region                string `json:"region"`
	Bucket                string `json:"bucket"`
	Prefix                string `json:"prefix"`
	UsePathStyle          bool   `json:"use_path_style,omitempty"`
	AllowInsecureEndpoint bool   `json:"allow_insecure_endpoint,omitempty"`
	AccessKeyID           string `json:"access_key_id,omitempty"`
	SecretAccessKey       string `json:"secret_access_key,omitempty"`
	SessionToken          string `json:"session_token,omitempty"`
	ServerSideEncryption  string `json:"server_side_encryption,omitempty"`
	KMSKeyID              string `json:"kms_key_id,omitempty"`
	VersioningMode        string `json:"versioning_mode,omitempty"`
}

// KnowledgeConfig controls only deployment-owned search infrastructure.
// Project model/provider allowlists, data-egress policy, budgets, and limits
// remain versioned project data in PostgreSQL.
type KnowledgeConfig struct {
	OpenSearchURL           string `json:"opensearch_url"`
	OpenSearchIndexPrefix   string `json:"opensearch_index_prefix"`
	OpenSearchPipeline      string `json:"opensearch_pipeline"`
	OpenSearchVectorSize    int    `json:"opensearch_vector_size"`
	OpenSearchAllowInsecure bool   `json:"opensearch_allow_insecure"`
	ModelGatewayURL         string `json:"model_gateway_url"`
	ModelGatewayProviderKey string `json:"model_gateway_provider_key"`
	ModelGatewayExternal    bool   `json:"model_gateway_external"`
}

type ObservabilityConfig struct {
	OTLPHTTPEndpoint   string            `json:"otlp_http_endpoint"`
	OTLPHeaders        map[string]string `json:"-"`
	AllowInsecureHTTP  bool              `json:"allow_insecure_http"`
	TraceSamplingRatio float64           `json:"trace_sampling_ratio"`
	MetricsEnabled     bool              `json:"metrics_enabled"`
	MetricsBearerToken string            `json:"-"`
}

type IntegrationConfig struct {
	HMACKeys map[string]IntegrationHMACKeyConfig `json:"-"`
}

type IntegrationHMACKeyConfig struct {
	Current  []byte
	Previous []byte
}

// AuditExportConfig describes the durable object-store topology used by the
// asynchronous platform audit exporter. The current adapter is local
// filesystem storage; multi-replica deployments must explicitly declare a
// shared RWX/PVC root.
type AuditExportConfig struct {
	StorageBackend      string        `json:"storage_backend"`
	StorageDir          string        `json:"storage_dir"`
	LocalDeploymentMode string        `json:"local_deployment_mode"`
	ReplicaCount        int           `json:"replica_count"`
	WorkerID            string        `json:"worker_id"`
	PollInterval        time.Duration `json:"poll_interval"`
	CleanupInterval     time.Duration `json:"cleanup_interval"`
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
	traceSamplingRatio, err := getEnvAsStrictFloat64(
		"OTEL_TRACE_SAMPLING_RATIO",
		0.1,
	)
	if err != nil {
		return nil, err
	}
	otlpHeaders, err := getEnvAsStrictStringMap(
		"CHRONODESK_OTLP_HEADERS_JSON",
	)
	if err != nil {
		return nil, err
	}
	integrationHMACKeys, err := getEnvAsIntegrationHMACKeys(
		"CHRONODESK_INTEGRATION_HMAC_KEYS_JSON",
	)
	if err != nil {
		return nil, err
	}
	historicalAttachmentStores, err :=
		getEnvAsAttachmentS3Stores(
			"AGENT_ATTACHMENT_S3_HISTORICAL_STORES_JSON",
		)
	if err != nil {
		return nil, err
	}
	environment := getEnv("ENVIRONMENT", "development")
	auditExportModeDefault := "single"
	attachmentLocalModeDefault := "single"
	if environment == "production" {
		// Production must make the topology assertion explicitly.
		auditExportModeDefault = ""
		attachmentLocalModeDefault = ""
	}
	auditExportReplicaCount, err := getEnvAsStrictInt(
		"CHRONODESK_REPLICA_COUNT",
		1,
	)
	if err != nil {
		return nil, err
	}
	appVersion := version.Version
	if appVersion == "" || strings.TrimSpace(appVersion) != appVersion {
		return nil, errors.New("build version is invalid")
	}
	if configuredVersion := os.Getenv("APP_VERSION"); configuredVersion != "" &&
		configuredVersion != appVersion {
		return nil, fmt.Errorf(
			"APP_VERSION %q does not match build version %q",
			configuredVersion,
			appVersion,
		)
	}
	config := &Config{
		Server: ServerConfig{
			Port:           getEnv("PORT", "8081"),
			GinMode:        getEnv("GIN_MODE", "debug"),
			Environment:    environment,
			TrustedProxies: getEnvAsSlice("TRUSTED_PROXIES", []string{}),
		},
		Database: DatabaseConfig{
			RuntimeURL: getEnv(
				"DATABASE_RUNTIME_URL",
				"postgres://chronodesk_runtime:chronodesk_runtime_dev_only@localhost:5432/chronodesk?sslmode=disable",
			),
			MigrationURL:    getEnv("DATABASE_MIGRATION_URL", ""),
			Host:            getEnv("DB_HOST", "localhost"),
			Port:            getEnvAsInt("DB_PORT", 5432),
			User:            getEnv("DB_USER", "chronodesk_runtime"),
			Password:        getEnv("DB_PASSWORD", "chronodesk_runtime_dev_only"),
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
			Version: appVersion,
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
				"traceparent",
				"tracestate",
				"baggage",
				"X-Correlation-ID",
			}),
		},
		RateLimit: RateLimitConfig{
			Requests:                  getEnvAsInt("RATE_LIMIT_REQUESTS", 600),
			Window:                    getEnvAsDuration("RATE_LIMIT_WINDOW", time.Minute),
			AnonymousIdentityRequests: getEnvAsInt("AUTH_IDENTITY_RATE_LIMIT_REQUESTS", 20),
			AnonymousIPRequests:       getEnvAsInt("AUTH_IP_RATE_LIMIT_REQUESTS", 200),
			AnonymousWindow:           getEnvAsDuration("AUTH_RATE_LIMIT_WINDOW", time.Minute),
			BackupCodeRequests:        getEnvAsInt("OTP_BACKUP_CODE_RATE_LIMIT_REQUESTS", 5),
			BackupCodeWindow:          getEnvAsDuration("OTP_BACKUP_CODE_RATE_LIMIT_WINDOW", 15*time.Minute),
		},
		Agent: AgentConfig{
			JWTSecret:        getEnv("AGENT_JWT_SECRET", getEnv("JWT_SECRET", "your-super-secret-jwt-key-change-in-production")),
			CredentialPepper: getEnv("AGENT_CREDENTIAL_PEPPER", getEnv("AGENT_JWT_SECRET", getEnv("JWT_SECRET", "your-super-secret-jwt-key-change-in-production"))),
			Issuer:           getEnv("AGENT_ISSUER", appURL),
			// RFC 8707 resource identifiers are derived from the canonical public
			// origin. They are intentionally not independently configurable:
			// MCP, REST, and A2A tokens must never share an audience.
			MCPResourceURL: appURL + "/mcp",
			APIResourceURL: appURL + "/api/v2",
			A2AResourceURL: appURL + "/a2a/v1",
			TokenTTL:       getEnvAsDuration("AGENT_TOKEN_TTL", 15*time.Minute),
			CredentialTTL:  getEnvAsDuration("AGENT_CREDENTIAL_TTL", 90*24*time.Hour),
			AttachmentStorageBackend: getEnv(
				"AGENT_ATTACHMENT_STORAGE_BACKEND",
				"local",
			),
			AttachmentLocalStoreID: getEnv(
				"AGENT_ATTACHMENT_LOCAL_STORE_ID",
				"local-default",
			),
			AttachmentDir: getEnv(
				"AGENT_ATTACHMENT_DIR",
				"./data/agent-attachments",
			),
			AttachmentStagingDir: getEnv(
				"AGENT_ATTACHMENT_STAGING_DIR",
				"./data/agent-attachment-staging",
			),
			AttachmentLocalDeploymentMode: getEnv(
				"AGENT_ATTACHMENT_LOCAL_DEPLOYMENT_MODE",
				attachmentLocalModeDefault,
			),
			AttachmentS3Endpoint: getEnv(
				"AGENT_ATTACHMENT_S3_ENDPOINT",
				"",
			),
			AttachmentS3StoreID: getEnv(
				"AGENT_ATTACHMENT_S3_STORE_ID",
				"s3-default",
			),
			AttachmentS3Region: getEnv(
				"AGENT_ATTACHMENT_S3_REGION",
				"us-east-1",
			),
			AttachmentS3Bucket: getEnv(
				"AGENT_ATTACHMENT_S3_BUCKET",
				"",
			),
			AttachmentS3Prefix: getEnv(
				"AGENT_ATTACHMENT_S3_PREFIX",
				"chronodesk/attachments",
			),
			AttachmentS3UsePathStyle: getEnvAsBool(
				"AGENT_ATTACHMENT_S3_USE_PATH_STYLE",
				false,
			),
			AttachmentS3AllowInsecure: getEnvAsBool(
				"AGENT_ATTACHMENT_S3_ALLOW_INSECURE",
				false,
			),
			AttachmentS3AccessKeyID: getEnv(
				"AGENT_ATTACHMENT_S3_ACCESS_KEY_ID",
				"",
			),
			AttachmentS3SecretAccessKey: getEnv(
				"AGENT_ATTACHMENT_S3_SECRET_ACCESS_KEY",
				"",
			),
			AttachmentS3SessionToken: getEnv(
				"AGENT_ATTACHMENT_S3_SESSION_TOKEN",
				"",
			),
			AttachmentS3SSE: getEnv(
				"AGENT_ATTACHMENT_S3_SSE",
				"bucket-default",
			),
			AttachmentS3KMSKeyID: getEnv(
				"AGENT_ATTACHMENT_S3_KMS_KEY_ID",
				"",
			),
			AttachmentS3VersioningMode: getEnv(
				"AGENT_ATTACHMENT_S3_VERSIONING_MODE",
				"auto",
			),
			AttachmentS3HistoricalStores: historicalAttachmentStores,
			MaxAttachmentBytes: int64(
				getEnvAsInt(
					"AGENT_MAX_ATTACHMENT_BYTES",
					25<<20,
				),
			),
			LoopThreshold:  getEnvAsInt("AGENT_LOOP_THRESHOLD", 20),
			LoopWindow:     getEnvAsDuration("AGENT_LOOP_WINDOW", time.Minute),
			GlobalReadOnly: getEnvAsBool("AGENT_GLOBAL_READ_ONLY", false),
		},
		Knowledge: KnowledgeConfig{
			OpenSearchURL:           getEnv("OPENSEARCH_URL", ""),
			OpenSearchIndexPrefix:   getEnv("OPENSEARCH_INDEX_PREFIX", "chronodesk-knowledge"),
			OpenSearchPipeline:      getEnv("OPENSEARCH_SEARCH_PIPELINE", "chronodesk-knowledge-hybrid"),
			OpenSearchVectorSize:    getEnvAsInt("OPENSEARCH_VECTOR_DIMENSION", 384),
			OpenSearchAllowInsecure: getEnvAsBool("OPENSEARCH_ALLOW_INSECURE", false),
			ModelGatewayURL:         getEnv("MODEL_GATEWAY_URL", ""),
			ModelGatewayProviderKey: getEnv("MODEL_GATEWAY_PROVIDER_KEY", "default"),
			ModelGatewayExternal:    getEnvAsBool("MODEL_GATEWAY_EXTERNAL", true),
		},
		Observability: ObservabilityConfig{
			OTLPHTTPEndpoint: getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
			OTLPHeaders:      otlpHeaders,
			AllowInsecureHTTP: getEnvAsBool(
				"OTEL_EXPORTER_OTLP_ALLOW_INSECURE",
				false,
			),
			TraceSamplingRatio: traceSamplingRatio,
			MetricsEnabled:     getEnvAsBool("METRICS_ENABLED", true),
			MetricsBearerToken: getEnv(
				"CHRONODESK_METRICS_BEARER_TOKEN",
				"",
			),
		},
		Integration: IntegrationConfig{HMACKeys: integrationHMACKeys},
		AuditExport: AuditExportConfig{
			StorageBackend: getEnv(
				"AUDIT_EXPORT_STORAGE_BACKEND",
				"local",
			),
			StorageDir: getEnv(
				"AUDIT_EXPORT_STORAGE_DIR",
				"./data/audit-exports",
			),
			LocalDeploymentMode: getEnv(
				"AUDIT_EXPORT_LOCAL_DEPLOYMENT_MODE",
				auditExportModeDefault,
			),
			ReplicaCount: auditExportReplicaCount,
			WorkerID: getEnv(
				"AUDIT_EXPORT_WORKER_ID",
				getEnv("HOSTNAME", "chronodesk-audit-export-1"),
			),
			PollInterval: getEnvAsDuration(
				"AUDIT_EXPORT_POLL_INTERVAL",
				5*time.Second,
			),
			CleanupInterval: getEnvAsDuration(
				"AUDIT_EXPORT_CLEANUP_INTERVAL",
				15*time.Minute,
			),
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
	if c.App.WebURL != "" {
		if err := validateBrowserSessionSiteContract(
			c.App.URL,
			c.App.WebURL,
		); err != nil {
			return err
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
	if c.Agent.MaxAttachmentBytes <= 0 ||
		c.Agent.MaxAttachmentBytes > 100<<20 {
		return fmt.Errorf(
			"agent maximum attachment size must be between 1 byte and 100 MiB",
		)
	}
	if err := c.validateAttachmentStorageConfig(); err != nil {
		return err
	}
	if c.Agent.LoopThreshold <= 0 || c.Agent.LoopWindow <= 0 {
		return fmt.Errorf("agent loop threshold and window must be positive")
	}
	if strings.TrimSpace(c.Knowledge.OpenSearchURL) != "" &&
		(c.Knowledge.OpenSearchVectorSize < 1 ||
			c.Knowledge.OpenSearchVectorSize > 65535) {
		return fmt.Errorf("OpenSearch vector dimension must be between 1 and 65535")
	}
	if strings.TrimSpace(c.Knowledge.OpenSearchURL) == "" &&
		strings.TrimSpace(c.Knowledge.ModelGatewayURL) != "" {
		return fmt.Errorf("model gateway requires an OpenSearch endpoint")
	}
	if strings.TrimSpace(c.Knowledge.ModelGatewayURL) != "" &&
		strings.TrimSpace(c.Knowledge.ModelGatewayProviderKey) == "" {
		return fmt.Errorf("model gateway provider key is required")
	}
	if c.Observability.TraceSamplingRatio < 0 ||
		c.Observability.TraceSamplingRatio > 1 {
		return fmt.Errorf(
			"OpenTelemetry trace sampling ratio must be between zero and one",
		)
	}
	if err := validateOperationalBearerToken(
		c.Observability.MetricsBearerToken,
	); err != nil {
		return err
	}
	if c.Server.Environment == "production" &&
		c.Observability.MetricsEnabled &&
		c.Observability.MetricsBearerToken == "" {
		return fmt.Errorf(
			"production metrics require CHRONODESK_METRICS_BEARER_TOKEN",
		)
	}
	if c.RateLimit.Requests <= 0 ||
		c.RateLimit.Window <= 0 ||
		c.RateLimit.AnonymousIdentityRequests <= 0 ||
		c.RateLimit.AnonymousIPRequests <= 0 ||
		c.RateLimit.AnonymousWindow <= 0 ||
		c.RateLimit.BackupCodeRequests <= 0 ||
		c.RateLimit.BackupCodeWindow <= 0 {
		return fmt.Errorf("authenticated and anonymous rate limit requests and windows must be positive")
	}
	if c.AuditExport.StorageBackend == "" {
		// Keep directly-constructed development test configs compatible while
		// treating the loaded runtime default as the canonical local adapter.
		c.AuditExport.StorageBackend = "local"
	}
	if c.AuditExport.LocalDeploymentMode == "" &&
		c.Server.Environment != "production" {
		c.AuditExport.LocalDeploymentMode = "single"
	}
	if c.AuditExport.ReplicaCount == 0 {
		c.AuditExport.ReplicaCount = 1
	}
	if c.AuditExport.PollInterval == 0 {
		c.AuditExport.PollInterval = 5 * time.Second
	}
	if c.AuditExport.CleanupInterval == 0 {
		c.AuditExport.CleanupInterval = 15 * time.Minute
	}
	if c.AuditExport.StorageBackend != "local" {
		return fmt.Errorf(
			"audit export storage backend %q is not supported",
			c.AuditExport.StorageBackend,
		)
	}
	if strings.TrimSpace(c.AuditExport.StorageDir) == "" {
		if c.Server.Environment == "production" {
			return fmt.Errorf("production audit export storage directory is required")
		}
		c.AuditExport.StorageDir = "./data/audit-exports"
	}
	if c.AuditExport.LocalDeploymentMode != "single" &&
		c.AuditExport.LocalDeploymentMode != "shared-rwx" {
		return fmt.Errorf(
			"audit export local deployment mode must be single or shared-rwx",
		)
	}
	if c.AuditExport.ReplicaCount < 1 {
		return fmt.Errorf("audit export replica count must be positive")
	}
	if c.AuditExport.ReplicaCount > 1 &&
		c.AuditExport.LocalDeploymentMode != "shared-rwx" {
		return fmt.Errorf(
			"multi-replica local audit export storage requires shared-rwx mode",
		)
	}
	if strings.TrimSpace(c.AuditExport.WorkerID) == "" {
		if c.Server.Environment == "production" {
			return fmt.Errorf("production audit export worker id is required")
		}
		c.AuditExport.WorkerID = "chronodesk-audit-export-1"
	}
	if c.AuditExport.PollInterval < time.Second ||
		c.AuditExport.PollInterval > time.Minute ||
		c.AuditExport.CleanupInterval < time.Minute ||
		c.AuditExport.CleanupInterval > 24*time.Hour {
		return fmt.Errorf("audit export worker intervals are invalid")
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
	if err := validatePostgresDatabaseURL(
		"DATABASE_RUNTIME_URL",
		c.Database.RuntimeURL,
		true,
	); err != nil {
		return err
	}
	if err := validatePostgresDatabaseURL(
		"DATABASE_MIGRATION_URL",
		c.Database.MigrationURL,
		false,
	); err != nil {
		return err
	}
	if samePostgresDatabaseIdentity(
		c.Database.RuntimeURL,
		c.Database.MigrationURL,
	) {
		return fmt.Errorf(
			"DATABASE_RUNTIME_URL and DATABASE_MIGRATION_URL must use different PostgreSQL roles",
		)
	}

	if c.Redis.Host == "" {
		return fmt.Errorf("redis host is required")
	}

	return nil
}

func (c *Config) validateAttachmentStorageConfig() error {
	c.Agent.AttachmentStorageBackend = strings.ToLower(
		strings.TrimSpace(c.Agent.AttachmentStorageBackend),
	)
	if c.Agent.AttachmentStorageBackend == "" {
		c.Agent.AttachmentStorageBackend = "local"
	}
	if c.Agent.AttachmentStorageBackend != "local" &&
		c.Agent.AttachmentStorageBackend != "s3" {
		return fmt.Errorf(
			"attachment storage backend must be local or s3",
		)
	}
	c.Agent.AttachmentLocalStoreID = strings.ToLower(
		strings.TrimSpace(c.Agent.AttachmentLocalStoreID),
	)
	if c.Agent.AttachmentLocalStoreID == "" {
		c.Agent.AttachmentLocalStoreID = "local-default"
	}
	c.Agent.AttachmentS3StoreID = strings.ToLower(
		strings.TrimSpace(c.Agent.AttachmentS3StoreID),
	)
	if c.Agent.AttachmentS3StoreID == "" {
		c.Agent.AttachmentS3StoreID = "s3-default"
	}
	if !validAttachmentStoreID(c.Agent.AttachmentLocalStoreID) ||
		!validAttachmentStoreID(c.Agent.AttachmentS3StoreID) ||
		c.Agent.AttachmentLocalStoreID ==
			c.Agent.AttachmentS3StoreID {
		return fmt.Errorf(
			"attachment store IDs must be distinct lowercase non-sensitive identifiers",
		)
	}
	c.Agent.AttachmentS3VersioningMode = strings.ToLower(
		strings.TrimSpace(c.Agent.AttachmentS3VersioningMode),
	)
	if c.Agent.AttachmentS3VersioningMode == "" {
		c.Agent.AttachmentS3VersioningMode = "auto"
	}
	if !validAttachmentS3VersioningMode(
		c.Agent.AttachmentS3VersioningMode,
	) {
		return fmt.Errorf(
			"attachment S3 versioning mode must be auto, required, or disabled",
		)
	}
	registeredStoreIDs := map[string]struct{}{
		c.Agent.AttachmentLocalStoreID: {},
		c.Agent.AttachmentS3StoreID:    {},
	}
	if len(c.Agent.AttachmentS3HistoricalStores) > 8 {
		return fmt.Errorf(
			"attachment S3 historical store registry supports at most 8 stores",
		)
	}
	for index := range c.Agent.AttachmentS3HistoricalStores {
		store := &c.Agent.AttachmentS3HistoricalStores[index]
		if err := validateHistoricalAttachmentS3Store(
			store,
			c.Server.Environment,
		); err != nil {
			return fmt.Errorf(
				"attachment historical S3 store %d: %w",
				index,
				err,
			)
		}
		if _, exists := registeredStoreIDs[store.StoreID]; exists {
			return fmt.Errorf(
				"attachment store_id %q is duplicated",
				store.StoreID,
			)
		}
		registeredStoreIDs[store.StoreID] = struct{}{}
	}
	if strings.TrimSpace(c.Agent.AttachmentDir) == "" {
		if c.Server.Environment == "production" {
			return fmt.Errorf(
				"production attachment fallback directory is required",
			)
		}
		c.Agent.AttachmentDir = "./data/agent-attachments"
	}
	if strings.TrimSpace(c.Agent.AttachmentStagingDir) == "" {
		if c.Server.Environment == "production" {
			return fmt.Errorf(
				"production attachment staging directory is required",
			)
		}
		c.Agent.AttachmentStagingDir =
			"./data/agent-attachment-staging"
	}
	c.Agent.AttachmentLocalDeploymentMode = strings.ToLower(
		strings.TrimSpace(
			c.Agent.AttachmentLocalDeploymentMode,
		),
	)
	if c.Agent.AttachmentLocalDeploymentMode == "" {
		if c.Server.Environment == "production" {
			return fmt.Errorf(
				"production attachment local deployment mode must be explicitly single or shared-rwx",
			)
		}
		c.Agent.AttachmentLocalDeploymentMode = "single"
	}
	if c.Agent.AttachmentLocalDeploymentMode != "single" &&
		c.Agent.AttachmentLocalDeploymentMode != "shared-rwx" {
		return fmt.Errorf(
			"attachment local deployment mode must be single or shared-rwx",
		)
	}
	replicaCount := c.AuditExport.ReplicaCount
	if replicaCount == 0 {
		replicaCount = 1
	}
	// Inbound bytes are always staged outside the database transaction. A
	// multi-replica deployment therefore needs a shared filesystem even when
	// the final immutable object is stored in S3.
	if replicaCount > 1 &&
		c.Agent.AttachmentLocalDeploymentMode != "shared-rwx" {
		return fmt.Errorf(
			"multi-replica attachment staging requires shared-rwx mode",
		)
	}

	s3Requested := c.Agent.AttachmentStorageBackend == "s3" ||
		strings.TrimSpace(c.Agent.AttachmentS3Endpoint) != "" ||
		strings.TrimSpace(c.Agent.AttachmentS3Bucket) != "" ||
		strings.TrimSpace(c.Agent.AttachmentS3AccessKeyID) != "" ||
		strings.TrimSpace(c.Agent.AttachmentS3SecretAccessKey) != "" ||
		strings.TrimSpace(c.Agent.AttachmentS3SessionToken) != "" ||
		strings.TrimSpace(c.Agent.AttachmentS3KMSKeyID) != ""
	if !s3Requested {
		return nil
	}
	if strings.TrimSpace(c.Agent.AttachmentS3Bucket) == "" ||
		strings.ContainsAny(
			c.Agent.AttachmentS3Bucket,
			"/\\\r\n\t ",
		) {
		return fmt.Errorf(
			"attachment S3 bucket must be a non-empty bucket name",
		)
	}
	if strings.TrimSpace(c.Agent.AttachmentS3Region) == "" {
		return fmt.Errorf("attachment S3 region is required")
	}
	if !validAttachmentObjectPrefix(
		c.Agent.AttachmentS3Prefix,
	) {
		return fmt.Errorf(
			"attachment S3 prefix must contain safe relative path segments",
		)
	}
	accessKeyConfigured :=
		strings.TrimSpace(c.Agent.AttachmentS3AccessKeyID) != ""
	secretKeyConfigured :=
		strings.TrimSpace(
			c.Agent.AttachmentS3SecretAccessKey,
		) != ""
	if accessKeyConfigured != secretKeyConfigured {
		return fmt.Errorf(
			"attachment S3 access key id and secret access key must be configured together",
		)
	}
	if strings.TrimSpace(c.Agent.AttachmentS3SessionToken) != "" &&
		!accessKeyConfigured {
		return fmt.Errorf(
			"attachment S3 session token requires static access credentials",
		)
	}

	c.Agent.AttachmentS3SSE = strings.ToLower(
		strings.TrimSpace(c.Agent.AttachmentS3SSE),
	)
	if c.Agent.AttachmentS3SSE == "" {
		c.Agent.AttachmentS3SSE = "bucket-default"
	}
	switch c.Agent.AttachmentS3SSE {
	case "bucket-default", "aes256", "aws:kms":
	default:
		return fmt.Errorf(
			"attachment S3 server-side encryption must be bucket-default, AES256, or aws:kms",
		)
	}
	if strings.TrimSpace(c.Agent.AttachmentS3KMSKeyID) != "" &&
		c.Agent.AttachmentS3SSE != "aws:kms" {
		return fmt.Errorf(
			"attachment S3 KMS key requires aws:kms encryption",
		)
	}

	endpoint := strings.TrimSpace(c.Agent.AttachmentS3Endpoint)
	if endpoint == "" {
		if c.Agent.AttachmentS3AllowInsecure {
			return fmt.Errorf(
				"attachment S3 insecure mode requires an explicit development endpoint",
			)
		}
		return nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") ||
		parsed.Host == "" ||
		parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return fmt.Errorf(
			"attachment S3 endpoint must be an absolute HTTP(S) origin without credentials, path, query, or fragment",
		)
	}
	if parsed.Scheme == "http" {
		if c.Server.Environment == "production" {
			return fmt.Errorf(
				"production attachment S3 endpoint must use HTTPS",
			)
		}
		if !c.Agent.AttachmentS3AllowInsecure {
			return fmt.Errorf(
				"HTTP attachment S3 endpoint requires AGENT_ATTACHMENT_S3_ALLOW_INSECURE=true",
			)
		}
	} else if c.Agent.AttachmentS3AllowInsecure {
		return fmt.Errorf(
			"attachment S3 insecure mode is invalid for an HTTPS endpoint",
		)
	}
	return nil
}

func validAttachmentObjectPrefix(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" ||
		strings.HasPrefix(value, "/") ||
		strings.HasSuffix(value, "/") ||
		strings.ContainsAny(value, "\\\x00\r\n") {
		return false
	}
	parts := strings.Split(value, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func validAttachmentStoreID(value string) bool {
	if len(value) == 0 || len(value) > 63 ||
		value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') ||
			character == '-' {
			continue
		}
		return false
	}
	return true
}

func validAttachmentS3VersioningMode(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "auto", "required", "disabled":
		return true
	default:
		return false
	}
}

func validateHistoricalAttachmentS3Store(
	store *AttachmentS3StoreConfig,
	environment string,
) error {
	if store == nil {
		return fmt.Errorf("store is required")
	}
	store.StoreID = strings.ToLower(strings.TrimSpace(store.StoreID))
	store.Region = strings.TrimSpace(store.Region)
	store.Bucket = strings.TrimSpace(store.Bucket)
	store.Prefix = strings.TrimSpace(store.Prefix)
	store.Endpoint = strings.TrimSpace(store.Endpoint)
	store.AccessKeyID = strings.TrimSpace(store.AccessKeyID)
	store.ServerSideEncryption = strings.ToLower(
		strings.TrimSpace(store.ServerSideEncryption),
	)
	store.VersioningMode = strings.ToLower(
		strings.TrimSpace(store.VersioningMode),
	)
	if !validAttachmentStoreID(store.StoreID) {
		return fmt.Errorf("store_id is invalid")
	}
	if store.Region == "" {
		return fmt.Errorf("region is required")
	}
	if store.Bucket == "" ||
		strings.ContainsAny(store.Bucket, "/\\\r\n\t ") {
		return fmt.Errorf("bucket is invalid")
	}
	if !validAttachmentObjectPrefix(store.Prefix) {
		return fmt.Errorf("prefix must contain safe relative path segments")
	}
	hasAccessKey := store.AccessKeyID != ""
	hasSecretKey := strings.TrimSpace(store.SecretAccessKey) != ""
	if hasAccessKey != hasSecretKey {
		return fmt.Errorf(
			"access key id and secret access key must be configured together",
		)
	}
	if strings.TrimSpace(store.SessionToken) != "" && !hasAccessKey {
		return fmt.Errorf(
			"session token requires static access credentials",
		)
	}
	if store.ServerSideEncryption == "" {
		store.ServerSideEncryption = "bucket-default"
	}
	switch store.ServerSideEncryption {
	case "bucket-default", "aes256":
		if strings.TrimSpace(store.KMSKeyID) != "" {
			return fmt.Errorf("KMS key requires aws:kms encryption")
		}
	case "aws:kms":
	default:
		return fmt.Errorf("server-side encryption is invalid")
	}
	if store.VersioningMode == "" {
		store.VersioningMode = "auto"
	}
	if !validAttachmentS3VersioningMode(store.VersioningMode) {
		return fmt.Errorf("versioning mode is invalid")
	}
	if store.Endpoint == "" {
		if store.AllowInsecureEndpoint {
			return fmt.Errorf(
				"insecure mode requires an explicit development endpoint",
			)
		}
		return nil
	}
	parsed, err := url.Parse(store.Endpoint)
	if err != nil ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") ||
		parsed.Host == "" ||
		parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return fmt.Errorf("endpoint must be an absolute HTTP(S) origin")
	}
	if parsed.Scheme == "http" {
		if environment == "production" {
			return fmt.Errorf("production endpoint must use HTTPS")
		}
		if !store.AllowInsecureEndpoint {
			return fmt.Errorf("HTTP endpoint requires insecure opt-in")
		}
	} else if store.AllowInsecureEndpoint {
		return fmt.Errorf("insecure mode is invalid for HTTPS")
	}
	return nil
}

func validateOperationalBearerToken(value string) error {
	if strings.TrimSpace(value) != value {
		return fmt.Errorf(
			"CHRONODESK_METRICS_BEARER_TOKEN must not contain surrounding whitespace",
		)
	}
	if value != "" && len(value) < 32 {
		return fmt.Errorf(
			"CHRONODESK_METRICS_BEARER_TOKEN must contain at least 32 bytes",
		)
	}
	return nil
}

func validatePostgresDatabaseURL(
	name string,
	value string,
	required bool,
) error {
	if value == "" {
		if required {
			return fmt.Errorf("%s is required", name)
		}
		return nil
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must not contain surrounding whitespace", name)
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" ||
		(parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") ||
		parsed.User == nil || parsed.User.Username() == "" ||
		strings.Trim(parsed.Path, "/") == "" ||
		parsed.Fragment != "" {
		return fmt.Errorf(
			"%s must be a PostgreSQL URL with an explicit role, host, and database",
			name,
		)
	}
	return nil
}

func samePostgresDatabaseIdentity(runtimeURL, migrationURL string) bool {
	if runtimeURL == "" || migrationURL == "" {
		return false
	}
	runtime, runtimeErr := url.Parse(runtimeURL)
	migration, migrationErr := url.Parse(migrationURL)
	if runtimeErr != nil || migrationErr != nil ||
		runtime.User == nil || migration.User == nil {
		return false
	}
	return strings.EqualFold(runtime.Scheme, migration.Scheme) &&
		strings.EqualFold(runtime.Host, migration.Host) &&
		runtime.Path == migration.Path &&
		runtime.User.Username() == migration.User.Username()
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
		"API": {got: apiResourceURL, path: "/api/v2"},
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

func validateBrowserSessionSiteContract(appURL, webURL string) error {
	appSite, err := schemefulSite("APP_URL", appURL)
	if err != nil {
		return err
	}
	webSite, err := schemefulSite("WEB_URL", webURL)
	if err != nil {
		return err
	}
	if appSite != webSite {
		return fmt.Errorf(
			"APP_URL and WEB_URL must use the same schemeful site for SameSite=Strict browser sessions",
		)
	}
	return nil
}

func schemefulSite(name, raw string) (string, error) {
	if raw != strings.TrimSpace(raw) {
		return "", fmt.Errorf("%s must not contain surrounding whitespace", name)
	}
	parsed, err := url.Parse(raw)
	if err != nil ||
		!parsed.IsAbs() ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return "", fmt.Errorf(
			"%s must be an absolute HTTP(S) URL without credentials, query, or fragment",
			name,
		)
	}
	hostname := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if hostname == "" {
		return "", fmt.Errorf("%s must include a hostname", name)
	}
	siteHost := hostname
	if address := net.ParseIP(hostname); address == nil &&
		hostname != "localhost" {
		registrable, err := publicsuffix.EffectiveTLDPlusOne(hostname)
		if err == nil {
			siteHost = strings.ToLower(registrable)
		}
	}
	return strings.ToLower(parsed.Scheme) + "://" + siteHost, nil
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

func getEnvAsStrictFloat64(
	key string,
	defaultValue float64,
) (float64, error) {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue, nil
	}
	floatValue, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number: %w", key, err)
	}
	return floatValue, nil
}

func getEnvAsStrictStringMap(key string) (map[string]string, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil, nil
	}
	result := map[string]string{}
	if err := json.Unmarshal([]byte(value), &result); err != nil {
		return nil, fmt.Errorf("%s must be a JSON string map: %w", key, err)
	}
	return result, nil
}

func getEnvAsIntegrationHMACKeys(
	key string,
) (map[string]IntegrationHMACKeyConfig, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return map[string]IntegrationHMACKeyConfig{}, nil
	}
	var encoded map[string]struct {
		Current  string `json:"current"`
		Previous string `json:"previous,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(value)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&encoded); err != nil {
		return nil, fmt.Errorf("%s must be a strict key map: %w", key, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return nil, fmt.Errorf("%s must contain one JSON value", key)
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s contains invalid trailing data: %w", key, err)
	}
	result := make(
		map[string]IntegrationHMACKeyConfig,
		len(encoded),
	)
	for reference, pair := range encoded {
		if !validIntegrationKeyReference(reference) {
			return nil, fmt.Errorf("%s contains an invalid key reference", key)
		}
		current, err := base64.StdEncoding.DecodeString(pair.Current)
		if err != nil || len(current) < 32 || len(current) > 4096 {
			return nil, fmt.Errorf(
				"%s current key %q must be Base64 for 32-4096 bytes",
				key,
				reference,
			)
		}
		var previous []byte
		if pair.Previous != "" {
			previous, err = base64.StdEncoding.DecodeString(pair.Previous)
			if err != nil || len(previous) < 32 || len(previous) > 4096 {
				clear(current)
				return nil, fmt.Errorf(
					"%s previous key %q must be Base64 for 32-4096 bytes",
					key,
					reference,
				)
			}
		}
		result[reference] = IntegrationHMACKeyConfig{
			Current:  current,
			Previous: previous,
		}
	}
	return result, nil
}

func getEnvAsAttachmentS3Stores(
	key string,
) ([]AttachmentS3StoreConfig, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil, nil
	}
	if len(value) > 64<<10 {
		return nil, fmt.Errorf("%s exceeds 64 KiB", key)
	}
	var stores []AttachmentS3StoreConfig
	decoder := json.NewDecoder(bytes.NewReader([]byte(value)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stores); err != nil {
		return nil, fmt.Errorf(
			"%s must be a strict JSON array: %w",
			key,
			err,
		)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return nil, fmt.Errorf("%s must contain one JSON value", key)
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf(
			"%s contains invalid trailing data: %w",
			key,
			err,
		)
	}
	if len(stores) > 8 {
		return nil, fmt.Errorf("%s supports at most 8 stores", key)
	}
	return stores, nil
}

func validIntegrationKeyReference(value string) bool {
	if value == "" || len(value) > 191 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' {
			continue
		}
		switch character {
		case '.', '_', ':', '/', '-':
			continue
		default:
			return false
		}
	}
	return true
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
