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
