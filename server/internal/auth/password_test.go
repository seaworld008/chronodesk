package auth

import (
	"strings"
	"testing"
)

func TestHashPassword_UsesBcryptPrefix(t *testing.T) {
	svc := NewSimplePasswordService(8, "ignored")

	hash, err := svc.HashPassword("StrongPass1!")
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}

	if !strings.HasPrefix(hash, "$2") {
		t.Fatalf("expected bcrypt hash prefix '$2', got: %s", hash)
	}
}

func TestVerifyPassword_RejectsLegacySHA256(t *testing.T) {
	svc := NewSimplePasswordService(8, "ticket-system-salt")
	password := "StrongPass1!"

	legacyHash := "31430f91ca9e5474a389af707821646a78604d62f9f4dc0296e083b520616491"
	err := svc.VerifyPassword(legacyHash, password)
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("legacy hash error = %v, want unsupported password hash", err)
	}
}
