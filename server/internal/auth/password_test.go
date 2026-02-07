package auth

import (
	"crypto/sha256"
	"encoding/hex"
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

func TestVerifyPassword_LegacySHA256Compatible(t *testing.T) {
	svc := NewSimplePasswordService(8, "ticket-system-salt")
	password := "StrongPass1!"

	legacyHash := legacySHA256Hash(password, "ticket-system-salt")
	if err := svc.VerifyPassword(legacyHash, password); err != nil {
		t.Fatalf("expected legacy hash verification success, got error: %v", err)
	}
}

func legacySHA256Hash(password, salt string) string {
	hasher := sha256.New()
	hasher.Write([]byte(salt))
	hasher.Write([]byte(password))
	hasher.Write([]byte(salt))
	return hex.EncodeToString(hasher.Sum(nil))
}
