package security

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestKeyringEnvelopeAuthenticatesAADAndCiphertext(t *testing.T) {
	key := bytes.Repeat([]byte{0x41}, 32)
	ring, err := NewKeyring("dek-current", map[string][]byte{"dek-current": key})
	if err != nil {
		t.Fatal(err)
	}
	aad := FieldAAD("webhook_configs", "42", "secret")
	envelope, err := ring.Seal([]byte("delivery-secret"), aad)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(envelope, "cdsec:v1:dek-current:") {
		t.Fatalf("unexpected envelope: %q", envelope)
	}
	if strings.Contains(envelope, "delivery-secret") {
		t.Fatal("envelope contains plaintext")
	}
	revealed, err := ring.Open(envelope, aad)
	if err != nil {
		t.Fatal(err)
	}
	if string(revealed) != "delivery-secret" {
		t.Fatalf("revealed %q", revealed)
	}

	if _, err := ring.Open(envelope, FieldAAD("webhook_configs", "43", "secret")); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("moved ciphertext error=%v, want authentication failure", err)
	}
	keyID, encoded, err := parseEnvelope(envelope)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	payload[len(payload)-1] ^= 0x01
	tampered := envelopePrefix + keyID + ":" + base64.RawURLEncoding.EncodeToString(payload)
	if _, err := ring.Open(tampered, aad); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("tampered ciphertext error=%v, want authentication failure", err)
	}
	if _, err := ring.Open("delivery-secret", aad); !errors.Is(err, ErrPlaintextSecret) {
		t.Fatalf("plaintext error=%v, want ErrPlaintextSecret", err)
	}
}

func TestKeyringUsesRandomNonce(t *testing.T) {
	key := bytes.Repeat([]byte{0x22}, 32)
	ring, err := NewKeyring("dek-a", map[string][]byte{"dek-a": key})
	if err != nil {
		t.Fatal(err)
	}
	aad := FieldAAD("email_configs", "1", "smtp_password")
	first, err := ring.Seal([]byte("same-password"), aad)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ring.Seal([]byte("same-password"), aad)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("randomized encryption produced identical envelopes")
	}
}

func TestKeyringRotationAndRestart(t *testing.T) {
	oldKey := bytes.Repeat([]byte{0x11}, 32)
	newKey := bytes.Repeat([]byte{0x22}, 32)
	aad := FieldAAD("agent_push_notification_configs", "push-1", "token")

	oldRing, err := NewKeyring("dek-old", map[string][]byte{"dek-old": oldKey})
	if err != nil {
		t.Fatal(err)
	}
	oldEnvelope, err := oldRing.Seal([]byte("opaque-token"), aad)
	if err != nil {
		t.Fatal(err)
	}

	rotatingRing, err := NewKeyring("dek-new", map[string][]byte{
		"dek-old": oldKey,
		"dek-new": newKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	rewrapped, changed, err := rotatingRing.Rewrap(oldEnvelope, aad)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || !strings.HasPrefix(rewrapped, "cdsec:v1:dek-new:") {
		t.Fatalf("unexpected rewrapped envelope %q changed=%v", rewrapped, changed)
	}

	// A new process constructed from the same persisted key material can read
	// the database envelope, while a process with a different key cannot.
	restarted, err := NewKeyring("dek-new", map[string][]byte{"dek-new": append([]byte(nil), newKey...)})
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := restarted.Open(rewrapped, aad)
	if err != nil || string(plaintext) != "opaque-token" {
		t.Fatalf("restart decrypt=%q err=%v", plaintext, err)
	}
	wrong, err := NewKeyring("dek-new", map[string][]byte{"dek-new": bytes.Repeat([]byte{0x33}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrong.Open(rewrapped, aad); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("wrong key error=%v, want ErrAuthentication", err)
	}
	if _, err := oldRing.Open(rewrapped, aad); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("retired keyring error=%v, want ErrUnknownKey", err)
	}
}

func TestLoadKeyringFromEnvironmentFailsClosed(t *testing.T) {
	t.Setenv(PrimaryKeyIDEnv, "")
	t.Setenv(KeysEnv, "")
	if _, err := LoadKeyringFromEnvironment(); !errors.Is(err, ErrKeyringUnavailable) {
		t.Fatalf("missing environment error=%v", err)
	}

	key := bytes.Repeat([]byte{0x72}, 32)
	t.Setenv(PrimaryKeyIDEnv, "dek-env")
	t.Setenv(KeysEnv, `{"dek-env":"`+base64.StdEncoding.EncodeToString(key)+`"}`)
	ring, err := LoadKeyringFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if ring.PrimaryKeyID() != "dek-env" {
		t.Fatalf("primary key ID=%q", ring.PrimaryKeyID())
	}
}

func TestDerivedKeyringUsesHKDFDomainSeparation(t *testing.T) {
	root := bytes.Repeat([]byte{0x5d}, 48)
	aad := FieldAAD("email_configs", "9", "smtp_password")
	first, err := NewDerivedKeyring(root, "derived-v1", "database-secrets/v1")
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := first.Seal([]byte("smtp-secret"), aad)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewDerivedKeyring(
		append([]byte(nil), root...),
		"derived-v1",
		"database-secrets/v1",
	)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := restarted.Open(envelope, aad)
	if err != nil || string(plaintext) != "smtp-secret" {
		t.Fatalf("derived restart decrypt=%q err=%v", plaintext, err)
	}
	otherDomain, err := NewDerivedKeyring(root, "derived-v1", "agent-credentials/v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := otherDomain.Open(envelope, aad); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("cross-domain decrypt error=%v", err)
	}
	if _, err := NewDerivedKeyring(
		[]byte("short-root"),
		"derived-v1",
		"database-secrets/v1",
	); !errors.Is(err, ErrKeyringUnavailable) {
		t.Fatalf("short root error=%v", err)
	}
}

func TestLoadDeploymentKeyringPriorityAndFailClosedFallback(t *testing.T) {
	root := bytes.Repeat([]byte{0x6a}, 48)
	t.Setenv(PrimaryKeyIDEnv, "")
	t.Setenv(KeysEnv, "")
	derived, err := LoadDeploymentKeyring(root)
	if err != nil {
		t.Fatal(err)
	}
	if derived.PrimaryKeyID() != DerivedDatabaseKeyID {
		t.Fatalf("derived primary=%q", derived.PrimaryKeyID())
	}

	dedicatedKey := bytes.Repeat([]byte{0x6b}, 32)
	t.Setenv(PrimaryKeyIDEnv, "dedicated-v2")
	t.Setenv(
		KeysEnv,
		`{"dedicated-v2":"`+base64.StdEncoding.EncodeToString(dedicatedKey)+`"}`,
	)
	dedicated, err := LoadDeploymentKeyring(root)
	if err != nil {
		t.Fatal(err)
	}
	if dedicated.PrimaryKeyID() != "dedicated-v2" {
		t.Fatalf("dedicated primary=%q", dedicated.PrimaryKeyID())
	}

	t.Setenv(KeysEnv, "{invalid-json")
	if _, err := LoadDeploymentKeyring(root); !errors.Is(err, ErrKeyringUnavailable) {
		t.Fatalf("invalid dedicated keyring unexpectedly fell back: %v", err)
	}
}

func TestLoadDeploymentKeyringFromEnvironmentDerivesPepper(t *testing.T) {
	t.Setenv(PrimaryKeyIDEnv, "")
	t.Setenv(KeysEnv, "")
	t.Setenv(AgentJWTSecretEnv, "")
	t.Setenv(HumanJWTSecretEnv, "")
	t.Setenv(AgentCredentialPepperEnv, strings.Repeat("high-entropy-pepper-", 3))
	ring, err := LoadDeploymentKeyringFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if ring.PrimaryKeyID() != DerivedDatabaseKeyID {
		t.Fatalf("derived primary=%q", ring.PrimaryKeyID())
	}

	t.Setenv(AgentCredentialPepperEnv, "")
	t.Setenv(HumanJWTSecretEnv, strings.Repeat("stable-human-jwt-root-", 3))
	fallback, err := LoadDeploymentKeyringFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if fallback.PrimaryKeyID() != DerivedDatabaseKeyID {
		t.Fatalf("fallback primary=%q", fallback.PrimaryKeyID())
	}
}
