package database

import (
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/config"
)

func TestValidateRedisTCPURLRequiresTLSForRemoteEndpoints(t *testing.T) {
	tests := []struct {
		name          string
		rawURL        string
		allowInsecure bool
		wantErr       bool
	}{
		{name: "remote TLS", rawURL: "rediss://example.com:6379", wantErr: false},
		{name: "remote plaintext", rawURL: "redis://example.com:6379", wantErr: true},
		{name: "explicit development override", rawURL: "redis://redis:6379", allowInsecure: true},
		{name: "loopback plaintext", rawURL: "redis://127.0.0.1:6379"},
		{name: "loopback hostname", rawURL: "redis://localhost:6379"},
		{name: "HTTP scheme", rawURL: "https://example.com", wantErr: true},
		{name: "relative", rawURL: "example.com:6379", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateRedisTCPURL(test.rawURL, test.allowInsecure)
			if (err != nil) != test.wantErr {
				t.Fatalf(
					"validateRedisTCPURL(%q, %v) error = %v, wantErr %v",
					test.rawURL,
					test.allowInsecure,
					err,
					test.wantErr,
				)
			}
		})
	}
}

func TestExplicitRedisRESTConfigurationNeverFallsBack(t *testing.T) {
	t.Setenv("KV_REST_API_URL", "not-an-absolute-url")
	t.Setenv("KV_REST_API_TOKEN", "configured-token")
	t.Setenv("REDIS_URL", "redis://localhost:6379")

	_, err := connectRedis(&config.Config{})
	if err == nil ||
		!strings.Contains(err.Error(), "invalid explicit Redis REST configuration") {
		t.Fatalf("connectRedis() error = %v, want explicit REST configuration failure", err)
	}
}
