package auth

import (
	"encoding/base64"
	"encoding/json"
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
				config.Audience = config.Issuer + "/api/v2"
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
	accessToken, _, err := manager.GenerateTokenPair(
		42,
		PlatformRolePlatformAdmin,
		"canonical-session",
	)
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
	segments := strings.Split(accessToken, ".")
	if len(segments) != 3 {
		t.Fatalf("JWT segments = %d, want 3", len(segments))
	}
	rawPayload, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		t.Fatalf("decode JWT payload: %v", err)
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(rawPayload, &claims); err != nil {
		t.Fatalf("decode JWT claims: %v", err)
	}
	if _, exists := claims["platform_role"]; !exists {
		t.Fatal("human JWT is missing platform_role")
	}
	if _, exists := claims["role"]; exists {
		t.Fatal("human JWT retained destructive legacy role claim")
	}
}

func TestJWTManagerRejectsLegacyRoleClaimsForBothTokenTypes(t *testing.T) {
	manager := mustTestJWTManager(t, time.Hour, 24*time.Hour)
	tests := []struct {
		name      string
		tokenType string
		secret    string
		verify    func(string) (*Claims, error)
	}{
		{
			name:      "access",
			tokenType: "access",
			secret:    manager.accessSecret,
			verify:    manager.VerifyAccessToken,
		},
		{
			name:      "refresh",
			tokenType: "refresh",
			secret:    manager.refreshSecret,
			verify:    manager.VerifyRefreshToken,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			token, err := signedLegacyRoleToken(
				manager,
				"admin",
				test.tokenType,
				test.secret,
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := test.verify(token); err == nil {
				t.Fatalf("legacy %s token was accepted", test.tokenType)
			}
		})
	}
}
