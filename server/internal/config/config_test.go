package config

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	buildversion "github.com/seaworld008/chronodesk/server/internal/version"
)

const testRuntimeDatabaseURL = "postgres://chronodesk_runtime:test@localhost:5432/chronodesk?sslmode=disable"

func TestLoadUsesBuildVersionAndRejectsRuntimeVersionDrift(t *testing.T) {
	originalVersion := buildversion.Version
	buildversion.Version = "0.2.0-test"
	t.Cleanup(func() {
		buildversion.Version = originalVersion
	})

	t.Run("APP_VERSION absent", func(t *testing.T) {
		t.Setenv("APP_VERSION", "")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.App.Version != buildversion.Version {
			t.Fatalf(
				"App.Version = %q, want build version %q",
				cfg.App.Version,
				buildversion.Version,
			)
		}
	})

	t.Run("matching APP_VERSION", func(t *testing.T) {
		t.Setenv("APP_VERSION", buildversion.Version)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.App.Version != buildversion.Version {
			t.Fatalf(
				"App.Version = %q, want build version %q",
				cfg.App.Version,
				buildversion.Version,
			)
		}
	})

	t.Run("mismatched APP_VERSION", func(t *testing.T) {
		t.Setenv("APP_VERSION", "0.1.0-runtime-override")
		_, err := Load()
		if err == nil ||
			!strings.Contains(err.Error(), "APP_VERSION") ||
			!strings.Contains(err.Error(), buildversion.Version) {
			t.Fatalf(
				"Load() error = %v, want stable build-version mismatch",
				err,
			)
		}
	})
}

func TestLoadRejectsInvalidBuildIdentityBeforeAPPVersionComparison(
	t *testing.T,
) {
	originalVersion := buildversion.Version
	t.Cleanup(func() {
		buildversion.Version = originalVersion
	})
	t.Setenv("AUTO_MIGRATE", "false")

	tests := []struct {
		name       string
		build      string
		appVersion string
	}{
		{
			name:  "empty build with absent APP_VERSION",
			build: "",
		},
		{
			name:       "empty build with matching APP_VERSION",
			build:      "",
			appVersion: "",
		},
		{
			name:       "leading whitespace with matching APP_VERSION",
			build:      " 0.2.0",
			appVersion: " 0.2.0",
		},
		{
			name:       "trailing whitespace with matching APP_VERSION",
			build:      "0.2.0 ",
			appVersion: "0.2.0 ",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			buildversion.Version = test.build
			t.Setenv("APP_VERSION", test.appVersion)

			_, err := Load()
			if err == nil ||
				!strings.Contains(err.Error(), "build version") ||
				!strings.Contains(err.Error(), "invalid") {
				t.Fatalf(
					"Load() error = %v, want invalid build identity before APP_VERSION comparison",
					err,
				)
			}
		})
	}
}

func TestLoadAcceptsTrimmedDevelopmentBuildIdentities(t *testing.T) {
	originalVersion := buildversion.Version
	t.Cleanup(func() {
		buildversion.Version = originalVersion
	})

	for _, build := range []string{
		"0.2.0-rc.1+build.42",
		"development",
	} {
		t.Run(build, func(t *testing.T) {
			buildversion.Version = build
			t.Setenv("APP_VERSION", build)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.App.Version != build {
				t.Fatalf("App.Version = %q, want %q", cfg.App.Version, build)
			}
		})
	}
}

func TestCORSConfigFromEnv(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://a.com,https://b.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.CORS.AllowedOrigins) != 2 {
		t.Fatalf("expected 2 origins, got %d", len(cfg.CORS.AllowedOrigins))
	}
	if cfg.CORS.AllowedOrigins[0] != "https://a.com" || cfg.CORS.AllowedOrigins[1] != "https://b.com" {
		t.Fatalf("unexpected origins: %v", cfg.CORS.AllowedOrigins)
	}
}

func TestBackupCodeRegenerationRateLimitDefaultsAndOverrides(t *testing.T) {
	t.Setenv("OTP_BACKUP_CODE_RATE_LIMIT_REQUESTS", "")
	t.Setenv("OTP_BACKUP_CODE_RATE_LIMIT_WINDOW", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RateLimit.BackupCodeRequests != 5 ||
		cfg.RateLimit.BackupCodeWindow != 15*time.Minute {
		t.Fatalf("backup-code limiter defaults = %+v", cfg.RateLimit)
	}

	t.Setenv("OTP_BACKUP_CODE_RATE_LIMIT_REQUESTS", "3")
	t.Setenv("OTP_BACKUP_CODE_RATE_LIMIT_WINDOW", "20m")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RateLimit.BackupCodeRequests != 3 ||
		cfg.RateLimit.BackupCodeWindow != 20*time.Minute {
		t.Fatalf("backup-code limiter overrides = %+v", cfg.RateLimit)
	}
}

func TestTrustedProxiesRequireExplicitConfiguration(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Server.TrustedProxies) != 0 {
		t.Fatalf("trusted proxies default = %v, want none", cfg.Server.TrustedProxies)
	}

	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8,192.0.2.10")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Server.TrustedProxies) != 2 {
		t.Fatalf("trusted proxies = %v, want 2 entries", cfg.Server.TrustedProxies)
	}
}

func TestValidate_RequiresJWTRefreshSecretInProduction(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Environment: "production",
		},
		JWT: JWTConfig{
			Secret:        "human-access-0123456789-abcdef-XYZ",
			RefreshSecret: "your-super-secret-jwt-refresh-key-change-in-production",
		},
		Database: DatabaseConfig{
			RuntimeURL: testRuntimeDatabaseURL,
			Host:       "localhost",
			User:       "user",
			Name:       "db",
		},
		Redis: RedisConfig{
			Host: "localhost",
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatalf("expected production config validation to fail for default refresh secret")
	}
}

func TestValidateProductionRequiresStrongIndependentSecrets(t *testing.T) {
	valid := Config{
		Server: ServerConfig{Environment: "production"},
		App:    AppConfig{URL: "https://desk.internal.example"},
		JWT: JWTConfig{
			Secret:           "human-access-0123456789-abcdef-XYZ",
			RefreshSecret:    "human-refresh-0123456789-abcdef-XYZ",
			ExpiresIn:        time.Hour,
			RefreshExpiresIn: 24 * time.Hour,
			Issuer:           "https://desk.internal.example",
			Audience:         "https://desk.internal.example/api",
		},
		Security: SecurityConfig{BcryptCost: 12},
		Agent: AgentConfig{
			Issuer:                        "https://desk.internal.example",
			MCPResourceURL:                "https://desk.internal.example/mcp",
			APIResourceURL:                "https://desk.internal.example/api/v2",
			A2AResourceURL:                "https://desk.internal.example/a2a/v1",
			JWTSecret:                     "agent-access-0123456789-abcdef-XYZ",
			CredentialPepper:              "agent-pepper-0123456789-abcdef-XYZ",
			TokenTTL:                      15 * time.Minute,
			CredentialTTL:                 24 * time.Hour,
			AttachmentStorageBackend:      "local",
			AttachmentDir:                 "/srv/chronodesk/attachments",
			AttachmentStagingDir:          "/srv/chronodesk/attachment-staging",
			AttachmentLocalDeploymentMode: "single",
			MaxAttachmentBytes:            1,
			LoopThreshold:                 1,
			LoopWindow:                    time.Minute,
		},
		Database: DatabaseConfig{
			RuntimeURL: testRuntimeDatabaseURL,
			Host:       "localhost",
			User:       "user",
			Name:       "db",
		},
		Redis: RedisConfig{Host: "localhost"},
		RateLimit: RateLimitConfig{
			Requests:                  100,
			Window:                    time.Hour,
			AnonymousIdentityRequests: 20,
			AnonymousIPRequests:       200,
			AnonymousWindow:           time.Minute,
			BackupCodeRequests:        5,
			BackupCodeWindow:          15 * time.Minute,
		},
		AuditExport: AuditExportConfig{
			StorageBackend:      "local",
			StorageDir:          "/srv/chronodesk/audit-exports",
			LocalDeploymentMode: "single",
			ReplicaCount:        1,
			WorkerID:            "audit-export-test-worker",
			PollInterval:        5 * time.Second,
			CleanupInterval:     15 * time.Minute,
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid production secrets rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{
			name: "short human access",
			mutate: func(cfg *Config) {
				cfg.JWT.Secret = "a"
			},
		},
		{
			name: "placeholder human refresh",
			mutate: func(cfg *Config) {
				cfg.JWT.RefreshSecret = "replace-with-a-real-refresh-secret-value"
			},
		},
		{
			name: "human access and refresh are equal",
			mutate: func(cfg *Config) {
				cfg.JWT.RefreshSecret = cfg.JWT.Secret
			},
		},
		{
			name: "Agent JWT equals human refresh",
			mutate: func(cfg *Config) {
				cfg.Agent.JWTSecret = cfg.JWT.RefreshSecret
			},
		},
		{
			name: "credential pepper equals human access",
			mutate: func(cfg *Config) {
				cfg.Agent.CredentialPepper = cfg.JWT.Secret
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected production secret validation to fail")
			}
		})
	}
}

func TestValidateAuditExportLocalDeploymentTopology(t *testing.T) {
	base := Config{
		Server: ServerConfig{Environment: "development"},
		App:    AppConfig{URL: "https://desk.internal.example"},
		JWT: JWTConfig{
			Secret:           "human-access-0123456789-abcdef-XYZ",
			RefreshSecret:    "human-refresh-0123456789-abcdef-XYZ",
			ExpiresIn:        time.Hour,
			RefreshExpiresIn: 24 * time.Hour,
			Issuer:           "https://desk.internal.example",
			Audience:         "https://desk.internal.example/api",
		},
		Security: SecurityConfig{BcryptCost: 12},
		Agent: AgentConfig{
			Issuer:                        "https://desk.internal.example",
			MCPResourceURL:                "https://desk.internal.example/mcp",
			APIResourceURL:                "https://desk.internal.example/api/v2",
			A2AResourceURL:                "https://desk.internal.example/a2a/v1",
			JWTSecret:                     "agent-access-0123456789-abcdef-XYZ",
			CredentialPepper:              "agent-pepper-0123456789-abcdef-XYZ",
			TokenTTL:                      15 * time.Minute,
			CredentialTTL:                 24 * time.Hour,
			AttachmentStorageBackend:      "local",
			AttachmentDir:                 "/srv/chronodesk/attachments",
			AttachmentStagingDir:          "/srv/chronodesk/attachment-staging",
			AttachmentLocalDeploymentMode: "shared-rwx",
			MaxAttachmentBytes:            1,
			LoopThreshold:                 1,
			LoopWindow:                    time.Minute,
		},
		Database: DatabaseConfig{
			RuntimeURL: testRuntimeDatabaseURL,
			Host:       "localhost",
			User:       "user",
			Name:       "db",
		},
		Redis: RedisConfig{Host: "localhost"},
		RateLimit: RateLimitConfig{
			Requests:                  100,
			Window:                    time.Hour,
			AnonymousIdentityRequests: 20,
			AnonymousIPRequests:       200,
			AnonymousWindow:           time.Minute,
			BackupCodeRequests:        5,
			BackupCodeWindow:          15 * time.Minute,
		},
		AuditExport: AuditExportConfig{
			StorageBackend:      "local",
			StorageDir:          "/srv/chronodesk/audit-exports",
			LocalDeploymentMode: "single",
			ReplicaCount:        2,
			WorkerID:            "audit-export-test-worker",
			PollInterval:        5 * time.Second,
			CleanupInterval:     15 * time.Minute,
		},
	}
	if err := base.Validate(); err == nil ||
		!strings.Contains(err.Error(), "shared-rwx") {
		t.Fatalf("unsafe multi-replica local mode error = %v", err)
	}
	base.AuditExport.LocalDeploymentMode = "shared-rwx"
	if err := base.Validate(); err != nil {
		t.Fatalf("shared RWX audit export config rejected: %v", err)
	}
	base.Server.Environment = "production"
	base.AuditExport.LocalDeploymentMode = ""
	if err := base.Validate(); err == nil ||
		!strings.Contains(err.Error(), "single or shared-rwx") {
		t.Fatalf("implicit production audit export mode error = %v", err)
	}
}

func TestLoadConfig_UsesJWTRefreshSecretEnv(t *testing.T) {
	t.Setenv("JWT_SECRET", "human-access-secret-0123456789-abcdef")
	t.Setenv("JWT_REFRESH_SECRET", "human-refresh-secret-0123456789-abcdef")
	t.Setenv("ENVIRONMENT", "development")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.JWT.RefreshSecret != "human-refresh-secret-0123456789-abcdef" {
		t.Fatalf("expected refresh secret from env, got: %q", cfg.JWT.RefreshSecret)
	}
}

func TestLoadConfig_DefaultsUseCanonicalAgentResourceURLs(t *testing.T) {
	for _, key := range []string{"PORT", "APP_URL", "AGENT_ISSUER"} {
		t.Setenv(key, "")
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.Port != "8081" {
		t.Errorf("default listen port = %q, want 8081", cfg.Server.Port)
	}
	if cfg.App.URL != "http://localhost:8081" {
		t.Errorf("default APP URL = %q", cfg.App.URL)
	}
	if cfg.Agent.Issuer != cfg.App.URL {
		t.Errorf("default Agent issuer = %q, want APP URL %q", cfg.Agent.Issuer, cfg.App.URL)
	}
	if cfg.JWT.Issuer != cfg.App.URL {
		t.Errorf("default human JWT issuer = %q, want APP URL %q", cfg.JWT.Issuer, cfg.App.URL)
	}
	if cfg.JWT.Audience != cfg.App.URL+"/api" {
		t.Errorf("default human JWT audience = %q, want %q", cfg.JWT.Audience, cfg.App.URL+"/api")
	}
	for name, resource := range map[string]struct {
		got  string
		path string
	}{
		"MCP": {got: cfg.Agent.MCPResourceURL, path: "/mcp"},
		"API": {got: cfg.Agent.APIResourceURL, path: "/api/v2"},
		"A2A": {got: cfg.Agent.A2AResourceURL, path: "/a2a/v1"},
	} {
		if resource.got != cfg.App.URL+resource.path {
			t.Errorf("default %s resource = %q, want %q", name, resource.got, cfg.App.URL+resource.path)
		}
	}
}

func TestLoadConfigAttachmentStorageDefaultsToLocal(t *testing.T) {
	for _, key := range []string{
		"AGENT_ATTACHMENT_STORAGE_BACKEND",
		"AGENT_ATTACHMENT_DIR",
		"AGENT_ATTACHMENT_STAGING_DIR",
		"AGENT_ATTACHMENT_LOCAL_DEPLOYMENT_MODE",
		"AGENT_ATTACHMENT_S3_ENDPOINT",
		"AGENT_ATTACHMENT_S3_BUCKET",
	} {
		t.Setenv(key, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if cfg.Agent.AttachmentStorageBackend != "local" ||
		cfg.Agent.AttachmentLocalStoreID != "local-default" ||
		cfg.Agent.AttachmentS3StoreID != "s3-default" ||
		cfg.Agent.AttachmentS3VersioningMode != "auto" ||
		cfg.Agent.AttachmentDir != "./data/agent-attachments" ||
		cfg.Agent.AttachmentStagingDir !=
			"./data/agent-attachment-staging" ||
		cfg.Agent.AttachmentLocalDeploymentMode != "single" {
		t.Fatalf(
			"unexpected attachment defaults: %+v",
			cfg.Agent,
		)
	}
}

func TestLoadAttachmentHistoricalS3RegistryIsStrictBoundedAndUnique(
	t *testing.T,
) {
	t.Setenv(
		"AGENT_ATTACHMENT_S3_HISTORICAL_STORES_JSON",
		`[{"store_id":"s3-2025","endpoint":"https://objects.example.test","region":"us-east-1","bucket":"chronodesk-old","prefix":"attachments","versioning_mode":"required"}]`,
	)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if len(cfg.Agent.AttachmentS3HistoricalStores) != 1 ||
		cfg.Agent.AttachmentS3HistoricalStores[0].StoreID !=
			"s3-2025" ||
		cfg.Agent.AttachmentS3HistoricalStores[0].VersioningMode !=
			"required" {
		t.Fatalf(
			"unexpected historical stores: %+v",
			cfg.Agent.AttachmentS3HistoricalStores,
		)
	}

	t.Setenv(
		"AGENT_ATTACHMENT_S3_HISTORICAL_STORES_JSON",
		`[{"store_id":"s3-2025","endpoint":"https://objects.example.test","region":"us-east-1","bucket":"chronodesk-old","prefix":"attachments","unexpected":true}]`,
	)
	if _, err := Load(); err == nil ||
		!strings.Contains(err.Error(), "strict JSON array") {
		t.Fatalf("unknown historical field error = %v", err)
	}
}

func TestValidateAttachmentStoreIDsRejectGenerationCollision(
	t *testing.T,
) {
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Agent.AttachmentS3HistoricalStores = []AttachmentS3StoreConfig{{
		StoreID:              "s3-default",
		Endpoint:             "https://objects.example.test",
		Region:               "us-east-1",
		Bucket:               "chronodesk-old",
		Prefix:               "attachments",
		ServerSideEncryption: "bucket-default",
		VersioningMode:       "auto",
	}}
	if err := cfg.Validate(); err == nil ||
		!strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("duplicate store_id error = %v", err)
	}
}

func TestValidateAttachmentS3Configuration(t *testing.T) {
	base, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	base.Agent.AttachmentStorageBackend = "s3"
	base.Agent.AttachmentS3Endpoint = "http://minio:9000"
	base.Agent.AttachmentS3AllowInsecure = true
	base.Agent.AttachmentS3Region = "us-east-1"
	base.Agent.AttachmentS3Bucket = "chronodesk-private"
	base.Agent.AttachmentS3Prefix = "tenant/attachments"
	base.Agent.AttachmentS3UsePathStyle = true
	base.Agent.AttachmentS3AccessKeyID = "minio-access"
	base.Agent.AttachmentS3SecretAccessKey = "minio-secret"
	base.Agent.AttachmentS3SSE = "bucket-default"
	if err := base.Validate(); err != nil {
		t.Fatalf("valid MinIO-compatible config rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Config)
		match  string
	}{
		{
			name: "plaintext endpoint needs explicit development opt in",
			mutate: func(cfg *Config) {
				cfg.Agent.AttachmentS3AllowInsecure = false
			},
			match: "ALLOW_INSECURE",
		},
		{
			name: "credential pair is atomic",
			mutate: func(cfg *Config) {
				cfg.Agent.AttachmentS3SecretAccessKey = ""
			},
			match: "configured together",
		},
		{
			name: "object prefix cannot traverse",
			mutate: func(cfg *Config) {
				cfg.Agent.AttachmentS3Prefix = "tenant/../private"
			},
			match: "safe relative path",
		},
		{
			name: "kms key needs kms mode",
			mutate: func(cfg *Config) {
				cfg.Agent.AttachmentS3KMSKeyID = "alias/desk"
			},
			match: "requires aws:kms",
		},
		{
			name: "multi replica staging needs shared storage",
			mutate: func(cfg *Config) {
				cfg.AuditExport.ReplicaCount = 2
				cfg.Agent.AttachmentLocalDeploymentMode = "single"
			},
			match: "shared-rwx",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := *base
			test.mutate(&cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), test.match) {
				t.Fatalf(
					"Validate() error = %v, want %q",
					err,
					test.match,
				)
			}
		})
	}
}

func TestLoadConfigKnowledgeInfrastructureIsDeploymentOwned(t *testing.T) {
	t.Setenv("OPENSEARCH_URL", "https://search.internal.example")
	t.Setenv("OPENSEARCH_INDEX_PREFIX", "desk-knowledge")
	t.Setenv("OPENSEARCH_SEARCH_PIPELINE", "desk-hybrid")
	t.Setenv("OPENSEARCH_VECTOR_DIMENSION", "768")
	t.Setenv("MODEL_GATEWAY_URL", "https://models.internal.example")
	t.Setenv("MODEL_GATEWAY_PROVIDER_KEY", "private-gateway")
	t.Setenv("MODEL_GATEWAY_EXTERNAL", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if cfg.Knowledge.OpenSearchURL != "https://search.internal.example" ||
		cfg.Knowledge.OpenSearchIndexPrefix != "desk-knowledge" ||
		cfg.Knowledge.OpenSearchPipeline != "desk-hybrid" ||
		cfg.Knowledge.OpenSearchVectorSize != 768 ||
		cfg.Knowledge.ModelGatewayURL != "https://models.internal.example" ||
		cfg.Knowledge.ModelGatewayProviderKey != "private-gateway" ||
		cfg.Knowledge.ModelGatewayExternal {
		t.Fatalf("unexpected knowledge infrastructure config: %+v", cfg.Knowledge)
	}
}

func TestValidateKnowledgeGatewayRequiresSearchAndVectorDimension(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	cfg.Knowledge.OpenSearchURL = ""
	cfg.Knowledge.ModelGatewayURL = "https://models.internal.example"
	if err := cfg.Validate(); err == nil ||
		!strings.Contains(err.Error(), "requires an OpenSearch endpoint") {
		t.Fatalf("model gateway without search error = %v", err)
	}
	cfg.Knowledge.OpenSearchURL = "https://search.internal.example"
	cfg.Knowledge.OpenSearchVectorSize = 0
	if err := cfg.Validate(); err == nil ||
		!strings.Contains(err.Error(), "vector dimension") {
		t.Fatalf("invalid vector dimension error = %v", err)
	}
}

func TestLoadObservabilityDeploymentControls(t *testing.T) {
	t.Setenv(
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"https://otel.internal.example/v1/traces",
	)
	t.Setenv("OTEL_TRACE_SAMPLING_RATIO", "0.25")
	t.Setenv(
		"CHRONODESK_OTLP_HEADERS_JSON",
		`{"Authorization":"Bearer deployment-secret"}`,
	)
	t.Setenv("METRICS_ENABLED", "false")
	t.Setenv(
		"CHRONODESK_METRICS_BEARER_TOKEN",
		"metrics-bearer-token-0123456789-abcdef",
	)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Observability.OTLPHTTPEndpoint !=
		"https://otel.internal.example/v1/traces" ||
		cfg.Observability.TraceSamplingRatio != 0.25 ||
		cfg.Observability.MetricsEnabled ||
		cfg.Observability.MetricsBearerToken !=
			"metrics-bearer-token-0123456789-abcdef" ||
		cfg.Observability.OTLPHeaders["Authorization"] !=
			"Bearer deployment-secret" {
		t.Fatalf("unexpected observability config: %+v", cfg.Observability)
	}
}

func TestProductionMetricsRequireStrongBearerToken(t *testing.T) {
	t.Setenv("ENVIRONMENT", "production")
	t.Setenv("APP_URL", "https://desk.internal.example")
	t.Setenv("JWT_SECRET", "human-access-0123456789-abcdef-XYZ")
	t.Setenv("JWT_REFRESH_SECRET", "human-refresh-0123456789-abcdef-XYZ")
	t.Setenv("AGENT_JWT_SECRET", "agent-access-0123456789-abcdef-XYZ")
	t.Setenv(
		"AGENT_CREDENTIAL_PEPPER",
		"agent-pepper-0123456789-abcdef-XYZ",
	)
	t.Setenv("METRICS_ENABLED", "false")
	t.Setenv("AUDIT_EXPORT_LOCAL_DEPLOYMENT_MODE", "single")
	t.Setenv("AGENT_ATTACHMENT_LOCAL_DEPLOYMENT_MODE", "single")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Observability.MetricsEnabled = true
	cfg.Observability.MetricsBearerToken = ""
	if err := cfg.Validate(); err == nil ||
		!strings.Contains(err.Error(), "production metrics require") {
		t.Fatalf("missing production metrics token error = %v", err)
	}
	cfg.Observability.MetricsBearerToken = strings.Repeat("m", 31)
	if err := cfg.Validate(); err == nil ||
		!strings.Contains(err.Error(), "at least 32 bytes") {
		t.Fatalf("weak production metrics token error = %v", err)
	}
	cfg.Observability.MetricsBearerToken = strings.Repeat("m", 32)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("strong production metrics token rejected: %v", err)
	}
}

func TestDatabaseRuntimeAndMigrationRolesAreExplicitAndDistinct(t *testing.T) {
	t.Setenv(
		"DATABASE_RUNTIME_URL",
		"postgres://chronodesk_runtime:runtime@db.internal/desk?sslmode=require",
	)
	t.Setenv(
		"DATABASE_MIGRATION_URL",
		"postgres://chronodesk_migration:migration@db.internal/desk?sslmode=require",
	)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.RuntimeURL == "" || cfg.Database.MigrationURL == "" {
		t.Fatalf("database role URLs were not loaded")
	}
	cfg.Observability.MetricsBearerToken =
		"metrics-bearer-token-0123456789-abcdef"
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{
		"chronodesk_runtime:runtime",
		"chronodesk_migration:migration",
		cfg.Observability.MetricsBearerToken,
	} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("serialized configuration exposed deployment secret")
		}
	}

	cfg.Database.MigrationURL =
		"postgres://chronodesk_runtime:other@db.internal/desk?sslmode=require"
	if err := cfg.Validate(); err == nil ||
		!strings.Contains(err.Error(), "different PostgreSQL roles") {
		t.Fatalf("same runtime/migration role error = %v", err)
	}
	cfg.Database.MigrationURL = "not-a-postgres-url"
	if err := cfg.Validate(); err == nil ||
		!strings.Contains(err.Error(), "DATABASE_MIGRATION_URL") {
		t.Fatalf("invalid migration URL error = %v", err)
	}
}

func TestLoadObservabilityRejectsMalformedControls(t *testing.T) {
	t.Setenv("OTEL_TRACE_SAMPLING_RATIO", "not-a-number")
	if _, err := Load(); err == nil ||
		!strings.Contains(err.Error(), "OTEL_TRACE_SAMPLING_RATIO") {
		t.Fatalf("invalid sampling ratio error = %v", err)
	}
	t.Setenv("OTEL_TRACE_SAMPLING_RATIO", "0.1")
	t.Setenv("CHRONODESK_OTLP_HEADERS_JSON", `{"Authorization":42}`)
	if _, err := Load(); err == nil ||
		!strings.Contains(err.Error(), "CHRONODESK_OTLP_HEADERS_JSON") {
		t.Fatalf("invalid OTLP headers error = %v", err)
	}
}

func TestLoadIntegrationHMACKeyRotationIsDeploymentOwned(t *testing.T) {
	current := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("c", 32)))
	previous := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", 32)))
	t.Setenv(
		"CHRONODESK_INTEGRATION_HMAC_KEYS_JSON",
		`{"erp-primary":{"current":"`+current+
			`","previous":"`+previous+`"}}`,
	)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	keys, exists := cfg.Integration.HMACKeys["erp-primary"]
	if !exists ||
		string(keys.Current) != strings.Repeat("c", 32) ||
		string(keys.Previous) != strings.Repeat("p", 32) {
		t.Fatalf("unexpected integration HMAC key configuration")
	}
}

func TestLoadIntegrationHMACKeysRejectsUnknownOrWeakValues(t *testing.T) {
	t.Setenv(
		"CHRONODESK_INTEGRATION_HMAC_KEYS_JSON",
		`{"erp":{"current":"d2Vhaw==","endpoint":"https://evil.test"}}`,
	)
	if _, err := Load(); err == nil ||
		!strings.Contains(err.Error(), "CHRONODESK_INTEGRATION_HMAC_KEYS_JSON") {
		t.Fatalf("invalid integration HMAC key map error = %v", err)
	}
}

func TestLoadConfig_AcceptsConfigurablePublicOrigin(t *testing.T) {
	t.Setenv("PORT", "8081")
	t.Setenv("APP_URL", "https://desk.internal.example")
	t.Setenv("AGENT_ISSUER", "https://desk.internal.example")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.Port != "8081" || cfg.App.URL != "https://desk.internal.example" {
		t.Fatalf("unexpected listen/public configuration: %#v %#v", cfg.Server, cfg.App)
	}
	if cfg.JWT.Issuer != cfg.App.URL || cfg.JWT.Audience != cfg.App.URL+"/api" {
		t.Fatalf("unexpected human JWT resource contract: %#v", cfg.JWT)
	}
}

func TestLoadConfigRejectsInvalidBcryptCost(t *testing.T) {
	for _, value := range []string{"not-a-number", "9", "17"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("BCRYPT_COST", value)
			if _, err := Load(); err == nil {
				t.Fatalf("Load() accepted BCRYPT_COST=%q", value)
			}
		})
	}
}

func TestValidateHumanJWTTrustContractFailsClosed(t *testing.T) {
	base, err := Load()
	if err != nil {
		t.Fatalf("load baseline config: %v", err)
	}
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "empty access secret",
			mutate: func(config *Config) {
				config.JWT.Secret = ""
			},
			wantErr: "access secret",
		},
		{
			name: "empty refresh secret",
			mutate: func(config *Config) {
				config.JWT.RefreshSecret = ""
			},
			wantErr: "refresh secret",
		},
		{
			name: "same secrets",
			mutate: func(config *Config) {
				config.JWT.RefreshSecret = config.JWT.Secret
			},
			wantErr: "must be different",
		},
		{
			name: "issuer differs from APP URL",
			mutate: func(config *Config) {
				config.JWT.Issuer = "https://auth.example.test"
			},
			wantErr: "issuer must exactly match APP URL",
		},
		{
			name: "audience differs from human REST resource",
			mutate: func(config *Config) {
				config.JWT.Audience = config.App.URL + "/api/v2"
			},
			wantErr: "audience must exactly match",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := *base
			test.mutate(&config)
			err := config.Validate()
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestValidate_AgentEndpointContract(t *testing.T) {
	valid := Config{
		Server: ServerConfig{Environment: "development"},
		App:    AppConfig{URL: "https://desk.internal.example"},
		JWT: JWTConfig{
			Secret:           "human-access-0123456789-abcdef-XYZ",
			RefreshSecret:    "human-refresh-0123456789-abcdef-XYZ",
			ExpiresIn:        time.Hour,
			RefreshExpiresIn: 24 * time.Hour,
			Issuer:           "https://desk.internal.example",
			Audience:         "https://desk.internal.example/api",
		},
		Security: SecurityConfig{BcryptCost: 12},
		Agent: AgentConfig{
			Issuer:                        "https://desk.internal.example",
			MCPResourceURL:                "https://desk.internal.example/mcp",
			APIResourceURL:                "https://desk.internal.example/api/v2",
			A2AResourceURL:                "https://desk.internal.example/a2a/v1",
			TokenTTL:                      15 * time.Minute,
			CredentialTTL:                 24 * time.Hour,
			AttachmentStorageBackend:      "local",
			AttachmentDir:                 "/srv/chronodesk/attachments",
			AttachmentStagingDir:          "/srv/chronodesk/attachment-staging",
			AttachmentLocalDeploymentMode: "single",
			MaxAttachmentBytes:            1,
			LoopThreshold:                 1,
			LoopWindow:                    time.Minute,
		},
		Database: DatabaseConfig{
			RuntimeURL: testRuntimeDatabaseURL,
			Host:       "localhost",
			User:       "user",
			Name:       "db",
		},
		Redis: RedisConfig{Host: "localhost"},
		RateLimit: RateLimitConfig{
			Requests:                  100,
			Window:                    time.Hour,
			AnonymousIdentityRequests: 20,
			AnonymousIPRequests:       200,
			AnonymousWindow:           time.Minute,
			BackupCodeRequests:        5,
			BackupCodeWindow:          15 * time.Minute,
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid Agent endpoint contract rejected: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "issuer differs from public APP URL",
			mutate: func(cfg *Config) {
				cfg.Agent.Issuer = "https://auth.internal.example"
			},
			wantErr: "issuer must exactly match APP URL",
		},
		{
			name: "MCP resource uses another path",
			mutate: func(cfg *Config) {
				cfg.Agent.MCPResourceURL = "https://desk.internal.example/api"
			},
			wantErr: "MCP resource URL",
		},
		{
			name: "API resource has trailing slash",
			mutate: func(cfg *Config) {
				cfg.Agent.APIResourceURL = "https://desk.internal.example/api/v2/"
			},
			wantErr: "API resource URL",
		},
		{
			name: "A2A resource uses another path",
			mutate: func(cfg *Config) {
				cfg.Agent.A2AResourceURL = "https://desk.internal.example/a2a"
			},
			wantErr: "A2A resource URL",
		},
		{
			name: "APP URL has a path",
			mutate: func(cfg *Config) {
				cfg.App.URL = "https://desk.internal.example/base"
				cfg.Agent.Issuer = cfg.App.URL
				cfg.Agent.MCPResourceURL = cfg.App.URL + "/mcp"
				cfg.Agent.APIResourceURL = cfg.App.URL + "/api/v2"
				cfg.Agent.A2AResourceURL = cfg.App.URL + "/a2a/v1"
			},
			wantErr: "canonical origin",
		},
		{
			name: "non-loopback HTTP is insecure",
			mutate: func(cfg *Config) {
				cfg.App.URL = "http://desk.internal.example"
				cfg.Agent.Issuer = cfg.App.URL
				cfg.Agent.MCPResourceURL = cfg.App.URL + "/mcp"
				cfg.Agent.APIResourceURL = cfg.App.URL + "/api/v2"
				cfg.Agent.A2AResourceURL = cfg.App.URL + "/a2a/v1"
			},
			wantErr: "HTTPS",
		},
		{
			name: "rate limit count is not positive",
			mutate: func(cfg *Config) {
				cfg.RateLimit.Requests = 0
			},
			wantErr: "rate limit requests and windows must be positive",
		},
		{
			name: "rate limit window is not positive",
			mutate: func(cfg *Config) {
				cfg.RateLimit.Window = 0
			},
			wantErr: "rate limit requests and windows must be positive",
		},
		{
			name: "anonymous identity rate limit count is not positive",
			mutate: func(cfg *Config) {
				cfg.RateLimit.AnonymousIdentityRequests = 0
			},
			wantErr: "rate limit requests and windows must be positive",
		},
		{
			name: "anonymous IP rate limit count is not positive",
			mutate: func(cfg *Config) {
				cfg.RateLimit.AnonymousIPRequests = 0
			},
			wantErr: "rate limit requests and windows must be positive",
		},
		{
			name: "anonymous rate limit window is not positive",
			mutate: func(cfg *Config) {
				cfg.RateLimit.AnonymousWindow = 0
			},
			wantErr: "rate limit requests and windows must be positive",
		},
		{
			name: "backup-code rate limit count is not positive",
			mutate: func(cfg *Config) {
				cfg.RateLimit.BackupCodeRequests = 0
			},
			wantErr: "rate limit requests and windows must be positive",
		},
		{
			name: "backup-code rate limit window is not positive",
			mutate: func(cfg *Config) {
				cfg.RateLimit.BackupCodeWindow = 0
			},
			wantErr: "rate limit requests and windows must be positive",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			test.mutate(&cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}
