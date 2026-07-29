package auth

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashPassword_UsesBcryptPrefix(t *testing.T) {
	svc := mustTestPasswordService(t)

	hash, err := svc.HashPassword("StrongPass1!")
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}

	if !strings.HasPrefix(hash, "$2") {
		t.Fatalf("expected bcrypt hash prefix '$2', got: %s", hash)
	}
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		t.Fatalf("read bcrypt cost: %v", err)
	}
	if cost != BcryptCostMin {
		t.Fatalf("bcrypt cost = %d, want configured cost %d", cost, BcryptCostMin)
	}
}

func TestVerifyPassword_RejectsLegacySHA256(t *testing.T) {
	svc := mustTestPasswordService(t)
	password := "StrongPass1!"

	legacyHash := "31430f91ca9e5474a389af707821646a78604d62f9f4dc0296e083b520616491"
	err := svc.VerifyPassword(legacyHash, password)
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("legacy hash error = %v, want unsupported password hash", err)
	}
}

func TestNewSimplePasswordServiceRejectsUnsafeOrAmbiguousConfig(t *testing.T) {
	tests := []PasswordServiceConfig{
		{MinLength: 7, BcryptCost: BcryptCostMin},
		{MinLength: 129, BcryptCost: BcryptCostMin},
		{MinLength: 8, BcryptCost: BcryptCostMin - 1},
		{MinLength: 8, BcryptCost: BcryptCostMax + 1},
	}
	for _, config := range tests {
		if _, err := NewSimplePasswordService(config); err == nil {
			t.Errorf("NewSimplePasswordService(%+v) succeeded", config)
		}
	}
}
