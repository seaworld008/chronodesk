package auth

import (
	"testing"
	"time"
)

const (
	testHumanJWTIssuer   = "https://chronodesk-auth.example.test"
	testHumanJWTAudience = testHumanJWTIssuer + "/api"
	testJWTAccessSecret  = "test-human-access-secret-0123456789-abcdef"
	testJWTRefreshSecret = "test-human-refresh-secret-0123456789-abcdef"
)

func mustTestJWTManager(
	t *testing.T,
	accessExpire, refreshExpire time.Duration,
) *SimpleJWTManager {
	t.Helper()
	manager, err := NewSimpleJWTManager(JWTManagerConfig{
		AccessSecret:  testJWTAccessSecret,
		RefreshSecret: testJWTRefreshSecret,
		AccessExpire:  accessExpire,
		RefreshExpire: refreshExpire,
		Issuer:        testHumanJWTIssuer,
		Audience:      testHumanJWTAudience,
	})
	if err != nil {
		t.Fatalf("create test JWT manager: %v", err)
	}
	return manager
}

func mustTestPasswordService(t *testing.T) *SimplePasswordService {
	t.Helper()
	service, err := NewSimplePasswordService(PasswordServiceConfig{
		MinLength:  8,
		BcryptCost: BcryptCostMin,
	})
	if err != nil {
		t.Fatalf("create test password service: %v", err)
	}
	return service
}
