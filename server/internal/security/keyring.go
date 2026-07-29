package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"golang.org/x/crypto/hkdf"
)

const (
	envelopePrefix = "cdsec:v1:"

	PrimaryKeyIDEnv          = "CHRONODESK_DATA_ENCRYPTION_PRIMARY_KEY_ID"
	KeysEnv                  = "CHRONODESK_DATA_ENCRYPTION_KEYS"
	AgentCredentialPepperEnv = "AGENT_CREDENTIAL_PEPPER"
	AgentJWTSecretEnv        = "AGENT_JWT_SECRET"
	HumanJWTSecretEnv        = "JWT_SECRET"

	DerivedDatabaseKeyID            = "agent-pepper-hkdf-v1"
	DatabaseSecretsDerivationDomain = "database-secrets/v1"
)

var (
	ErrKeyringUnavailable = errors.New("data-encryption keyring is unavailable")
	ErrInvalidEnvelope    = errors.New("invalid encrypted-secret envelope")
	ErrPlaintextSecret    = errors.New("plaintext secret is not accepted")
	ErrUnknownKey         = errors.New("encrypted-secret key is unavailable")
	ErrAuthentication     = errors.New("encrypted-secret authentication failed")

	keyIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
)

const derivedKeySalt = "chronodesk:data-encryption:hkdf-sha256:v1"

// Protector is the narrow dependency used by persistence adapters. The
// implementation emits a versioned envelope that includes the key ID; callers
// must supply stable, record-specific AAD so ciphertext cannot be moved between
// rows or fields.
type Protector interface {
	Seal(plaintext []byte, aad []byte) (string, error)
	Open(envelope string, aad []byte) ([]byte, error)
	PrimaryKeyID() string
}

// Keyring supports online key rotation: the primary key encrypts all new
// values, while retained historical keys only decrypt existing envelopes.
type Keyring struct {
	primaryID string
	aeads     map[string]cipher.AEAD
	random    io.Reader
}

// NewKeyring builds an AES-256-GCM keyring. Every key must be exactly 32 bytes.
func NewKeyring(primaryID string, keys map[string][]byte) (*Keyring, error) {
	return newKeyring(primaryID, keys, rand.Reader)
}

// NewDerivedKeyring derives an independent AES-256 key from an existing
// high-entropy root secret using HKDF-SHA256 domain separation. This is the
// deployable path when a dedicated DEK keyring has not been provisioned yet;
// callers must pass an explicit, stable key ID and domain.
func NewDerivedKeyring(
	rootSecret []byte,
	keyID string,
	domain string,
) (*Keyring, error) {
	key, err := DeriveAES256Key(rootSecret, domain)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	return NewKeyring(keyID, map[string][]byte{keyID: key})
}

// DeriveAES256Key exposes deterministic derivation for composing a rotation
// keyring from multiple historical roots. The returned key must be cleared by
// the caller after NewKeyring has initialized its AEAD.
func DeriveAES256Key(rootSecret []byte, domain string) ([]byte, error) {
	domain = strings.TrimSpace(domain)
	if len(rootSecret) < 32 {
		return nil, fmt.Errorf("%w: root secret must contain at least 32 bytes", ErrKeyringUnavailable)
	}
	if domain == "" || len(domain) > 255 {
		return nil, fmt.Errorf("%w: derivation domain is invalid", ErrKeyringUnavailable)
	}
	reader := hkdf.New(
		sha256.New,
		rootSecret,
		[]byte(derivedKeySalt),
		[]byte("chronodesk:data-encryption:"+domain),
	)
	key := make([]byte, 32)
	if _, err := io.ReadFull(reader, key); err != nil {
		clear(key)
		return nil, fmt.Errorf("%w: derive data-encryption key", ErrKeyringUnavailable)
	}
	return key, nil
}

func newKeyring(primaryID string, keys map[string][]byte, random io.Reader) (*Keyring, error) {
	primaryID = strings.TrimSpace(primaryID)
	if !keyIDPattern.MatchString(primaryID) {
		return nil, fmt.Errorf("%w: invalid primary key ID", ErrKeyringUnavailable)
	}
	if len(keys) == 0 || random == nil {
		return nil, ErrKeyringUnavailable
	}

	aeads := make(map[string]cipher.AEAD, len(keys))
	for rawID, rawKey := range keys {
		keyID := strings.TrimSpace(rawID)
		if !keyIDPattern.MatchString(keyID) {
			return nil, fmt.Errorf("%w: invalid key ID", ErrKeyringUnavailable)
		}
		if len(rawKey) != 32 {
			return nil, fmt.Errorf("%w: key %q must contain 32 bytes", ErrKeyringUnavailable, keyID)
		}
		block, err := aes.NewCipher(rawKey)
		if err != nil {
			return nil, fmt.Errorf("%w: initialize key %q: %v", ErrKeyringUnavailable, keyID, err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("%w: initialize AEAD for key %q: %v", ErrKeyringUnavailable, keyID, err)
		}
		aeads[keyID] = aead
	}
	if _, ok := aeads[primaryID]; !ok {
		return nil, fmt.Errorf("%w: primary key %q is absent", ErrKeyringUnavailable, primaryID)
	}
	return &Keyring{primaryID: primaryID, aeads: aeads, random: random}, nil
}

func (k *Keyring) PrimaryKeyID() string {
	if k == nil {
		return ""
	}
	return k.primaryID
}

func (k *Keyring) Seal(plaintext []byte, aad []byte) (string, error) {
	if k == nil {
		return "", ErrKeyringUnavailable
	}
	aead, ok := k.aeads[k.primaryID]
	if !ok {
		return "", ErrKeyringUnavailable
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(k.random, nonce); err != nil {
		return "", fmt.Errorf("%w: generate nonce", ErrKeyringUnavailable)
	}
	sealed := aead.Seal(nil, nonce, plaintext, aad)
	payload := make([]byte, 0, len(nonce)+len(sealed))
	payload = append(payload, nonce...)
	payload = append(payload, sealed...)
	return envelopePrefix + k.primaryID + ":" +
		base64.RawURLEncoding.EncodeToString(payload), nil
}

func (k *Keyring) Open(envelope string, aad []byte) ([]byte, error) {
	if k == nil {
		return nil, ErrKeyringUnavailable
	}
	keyID, encoded, err := parseEnvelope(envelope)
	if err != nil {
		return nil, err
	}
	aead, ok := k.aeads[keyID]
	if !ok {
		return nil, fmt.Errorf("%w: key ID %q", ErrUnknownKey, keyID)
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(payload) < aead.NonceSize()+aead.Overhead() {
		return nil, ErrInvalidEnvelope
	}
	nonce := payload[:aead.NonceSize()]
	ciphertext := payload[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrAuthentication
	}
	return plaintext, nil
}

// Rewrap re-encrypts an envelope with the current primary key. It returns
// changed=false when the envelope already uses that key. Authentication is
// always verified before returning.
func (k *Keyring) Rewrap(envelope string, aad []byte) (rewrapped string, changed bool, err error) {
	keyID, _, err := parseEnvelope(envelope)
	if err != nil {
		return "", false, err
	}
	plaintext, err := k.Open(envelope, aad)
	if err != nil {
		return "", false, err
	}
	defer clear(plaintext)
	if keyID == k.primaryID {
		return envelope, false, nil
	}
	rewrapped, err = k.Seal(plaintext, aad)
	return rewrapped, err == nil, err
}

func IsEnvelope(value string) bool {
	_, _, err := parseEnvelope(value)
	return err == nil
}

func EnvelopeKeyID(value string) (string, error) {
	keyID, _, err := parseEnvelope(value)
	return keyID, err
}

func parseEnvelope(value string) (keyID string, payload string, err error) {
	if !strings.HasPrefix(value, envelopePrefix) {
		if strings.TrimSpace(value) != "" {
			return "", "", ErrPlaintextSecret
		}
		return "", "", ErrInvalidEnvelope
	}
	remaining := strings.TrimPrefix(value, envelopePrefix)
	keyID, payload, ok := strings.Cut(remaining, ":")
	if !ok || !keyIDPattern.MatchString(keyID) || payload == "" {
		return "", "", ErrInvalidEnvelope
	}
	return keyID, payload, nil
}

// LoadKeyringFromEnvironment parses a JSON object whose values are base64
// encoded 32-byte keys. No development or plaintext fallback is provided.
//
// CHRONODESK_DATA_ENCRYPTION_PRIMARY_KEY_ID=dek-2026-07
// CHRONODESK_DATA_ENCRYPTION_KEYS={"dek-2026-07":"<base64>","dek-2026-04":"<base64>"}
func LoadKeyringFromEnvironment() (*Keyring, error) {
	primaryID := strings.TrimSpace(os.Getenv(PrimaryKeyIDEnv))
	encodedKeys := strings.TrimSpace(os.Getenv(KeysEnv))
	if primaryID == "" || encodedKeys == "" {
		return nil, ErrKeyringUnavailable
	}
	var configured map[string]string
	if err := json.Unmarshal([]byte(encodedKeys), &configured); err != nil {
		return nil, fmt.Errorf("%w: %s must be a JSON object", ErrKeyringUnavailable, KeysEnv)
	}
	keys := make(map[string][]byte, len(configured))
	for keyID, encoded := range configured {
		decoded, err := decodeKey(encoded)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid base64 key %q", ErrKeyringUnavailable, keyID)
		}
		keys[keyID] = decoded
	}
	return NewKeyring(primaryID, keys)
}

// LoadDeploymentKeyring prioritizes a dedicated environment keyring, which
// supports independent online rotation. When neither DEK variable is present,
// it explicitly derives a domain-separated key from the supplied stable Agent
// credential pepper. A partially configured or invalid environment keyring
// never falls back.
func LoadDeploymentKeyring(rootSecret []byte) (*Keyring, error) {
	if strings.TrimSpace(os.Getenv(PrimaryKeyIDEnv)) != "" ||
		strings.TrimSpace(os.Getenv(KeysEnv)) != "" {
		return LoadKeyringFromEnvironment()
	}
	return NewDerivedKeyring(
		rootSecret,
		DerivedDatabaseKeyID,
		DatabaseSecretsDerivationDomain,
	)
}

func LoadDeploymentKeyringFromEnvironment() (*Keyring, error) {
	rootSecret := strings.TrimSpace(os.Getenv(AgentCredentialPepperEnv))
	if rootSecret == "" {
		rootSecret = strings.TrimSpace(os.Getenv(AgentJWTSecretEnv))
	}
	if rootSecret == "" {
		rootSecret = strings.TrimSpace(os.Getenv(HumanJWTSecretEnv))
	}
	return LoadDeploymentKeyring([]byte(rootSecret))
}

func decodeKey(encoded string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(encoded)
		if err == nil {
			return decoded, nil
		}
	}
	return nil, errors.New("invalid base64")
}

// FieldAAD binds an encrypted value to one database table, row, and column.
func FieldAAD(table, rowID, field string) []byte {
	return []byte("chronodesk:v1:" + table + ":" + rowID + ":" + field)
}

func ProtectOptional(protector Protector, plaintext string, aad []byte) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if protector == nil {
		return "", ErrKeyringUnavailable
	}
	return protector.Seal([]byte(plaintext), aad)
}

func RevealOptional(protector Protector, envelope string, aad []byte) (string, error) {
	if envelope == "" {
		return "", nil
	}
	if protector == nil {
		return "", ErrKeyringUnavailable
	}
	plaintext, err := protector.Open(envelope, aad)
	if err != nil {
		return "", err
	}
	defer clear(plaintext)
	return string(plaintext), nil
}
