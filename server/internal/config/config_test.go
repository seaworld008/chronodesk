package config

import "testing"

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
