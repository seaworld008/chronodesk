package config

import (
	"strings"
	"testing"
	"time"
)

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
			Secret:        "changed-secret",
			RefreshSecret: "your-super-secret-jwt-refresh-key-change-in-production",
		},
		Database: DatabaseConfig{
			Host: "localhost",
			User: "user",
			Name: "db",
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

func TestLoadConfig_UsesJWTRefreshSecretEnv(t *testing.T) {
	t.Setenv("JWT_SECRET", "secret-1")
	t.Setenv("JWT_REFRESH_SECRET", "refresh-secret-1")
	t.Setenv("ENVIRONMENT", "development")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.JWT.RefreshSecret != "refresh-secret-1" {
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
	for name, resource := range map[string]struct {
		got  string
		path string
	}{
		"MCP": {got: cfg.Agent.MCPResourceURL, path: "/mcp"},
		"API": {got: cfg.Agent.APIResourceURL, path: "/api/v1"},
		"A2A": {got: cfg.Agent.A2AResourceURL, path: "/a2a/v1"},
	} {
		if resource.got != cfg.App.URL+resource.path {
			t.Errorf("default %s resource = %q, want %q", name, resource.got, cfg.App.URL+resource.path)
		}
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
}

func TestValidate_AgentEndpointContract(t *testing.T) {
	valid := Config{
		Server: ServerConfig{Environment: "development"},
		App:    AppConfig{URL: "https://desk.internal.example"},
		Agent: AgentConfig{
			Issuer:             "https://desk.internal.example",
			MCPResourceURL:     "https://desk.internal.example/mcp",
			APIResourceURL:     "https://desk.internal.example/api/v1",
			A2AResourceURL:     "https://desk.internal.example/a2a/v1",
			TokenTTL:           15 * time.Minute,
			CredentialTTL:      24 * time.Hour,
			MaxAttachmentBytes: 1,
			LoopThreshold:      1,
			LoopWindow:         time.Minute,
		},
		Database: DatabaseConfig{Host: "localhost", User: "user", Name: "db"},
		Redis:    RedisConfig{Host: "localhost"},
		RateLimit: RateLimitConfig{
			Requests:          100,
			Window:            time.Hour,
			AnonymousRequests: 20,
			AnonymousWindow:   time.Minute,
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
				cfg.Agent.APIResourceURL = "https://desk.internal.example/api/v1/"
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
				cfg.Agent.APIResourceURL = cfg.App.URL + "/api/v1"
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
				cfg.Agent.APIResourceURL = cfg.App.URL + "/api/v1"
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
			name: "anonymous rate limit count is not positive",
			mutate: func(cfg *Config) {
				cfg.RateLimit.AnonymousRequests = 0
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
