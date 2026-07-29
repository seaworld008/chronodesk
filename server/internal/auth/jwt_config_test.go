package auth

import (
	"strings"
	"testing"
	"time"
)

func validJWTManagerConfig() JWTManagerConfig {
	return JWTManagerConfig{
		AccessSecret:  testJWTAccessSecret,
		RefreshSecret: testJWTRefreshSecret,
		AccessExpire:  time.Hour,
		RefreshExpire: 24 * time.Hour,
		Issuer:        testHumanJWTIssuer,
		Audience:      testHumanJWTAudience,
	}
}

func TestNewSimpleJWTManagerFailsClosedOnInvalidTrustConfig(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*JWTManagerConfig)
		wantErr string
	}{
		{
			name: "empty access secret",
			mutate: func(config *JWTManagerConfig) {
				config.AccessSecret = ""
			},
			wantErr: "access secret",
		},
		{
			name: "empty refresh secret",
			mutate: func(config *JWTManagerConfig) {
				config.RefreshSecret = ""
			},
			wantErr: "refresh secret",
		},
		{
			name: "short access secret",
			mutate: func(config *JWTManagerConfig) {
				config.AccessSecret = "too-short"
			},
			wantErr: "access secret",
		},
		{
			name: "same access and refresh secret",
			mutate: func(config *JWTManagerConfig) {
				config.RefreshSecret = config.AccessSecret
			},
			wantErr: "must be different",
		},
		{
			name: "zero access expiration",
			mutate: func(config *JWTManagerConfig) {
				config.AccessExpire = 0
			},
			wantErr: "expiration must be positive",
		},
		{
			name: "noncanonical issuer",
			mutate: func(config *JWTManagerConfig) {
				config.Issuer += "/auth"
			},
			wantErr: "canonical origin",
		},
		{
			name: "wrong audience",
			mutate: func(config *JWTManagerConfig) {
				config.Audience = config.Issuer + "/api/v1"
			},
			wantErr: "audience must exactly match",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validJWTManagerConfig()
			test.mutate(&config)
			_, err := NewSimpleJWTManager(config)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("NewSimpleJWTManager() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestJWTManagerIssuesCanonicalHumanRESTClaims(t *testing.T) {
	manager := mustTestJWTManager(t, time.Hour, 24*time.Hour)
	accessToken, _, err := manager.GenerateTokenPair(42, RoleAdmin, "canonical-session")
	if err != nil {
		t.Fatalf("generate token pair: %v", err)
	}
	payload, err := manager.verifyToken(accessToken, manager.accessSecret)
	if err != nil {
		t.Fatalf("verify access token: %v", err)
	}
	if payload.Iss != testHumanJWTIssuer {
		t.Fatalf("issuer = %q, want %q", payload.Iss, testHumanJWTIssuer)
	}
	if payload.Aud != testHumanJWTAudience {
		t.Fatalf("audience = %q, want %q", payload.Aud, testHumanJWTAudience)
	}
}
