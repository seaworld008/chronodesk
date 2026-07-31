// Package listcursor signs and verifies opaque list-continuation payloads.
//
// The package deliberately knows nothing about a domain's scope, filters, or
// sort order. Callers own a closed payload that binds those values and pass a
// unique purpose to NewCodec so cursors cannot cross protocol or list domains.
package listcursor

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
)

const (
	maxEncodedCursorBytes = 4096
	maxPayloadBytes       = 3072
)

var (
	ErrInvalidCursor = errors.New("list cursor is invalid")
	ErrInvalidKey    = errors.New("list cursor key is invalid")
)

type Codec struct {
	signingKey []byte
}

func NewCodec(rootKey []byte, purpose string) (*Codec, error) {
	purpose = strings.TrimSpace(purpose)
	if len(rootKey) == 0 || purpose == "" {
		return nil, ErrInvalidKey
	}
	deriver := hmac.New(sha256.New, rootKey)
	_, _ = deriver.Write([]byte("chronodesk.list-cursor.v1\x00"))
	_, _ = deriver.Write([]byte(purpose))
	return &Codec{signingKey: deriver.Sum(nil)}, nil
}

func (c *Codec) Encode(payload any) (string, error) {
	if c == nil || len(c.signingKey) == 0 || payload == nil {
		return "", ErrInvalidKey
	}
	raw, err := json.Marshal(payload)
	if err != nil || len(raw) == 0 || len(raw) > maxPayloadBytes {
		return "", ErrInvalidCursor
	}
	mac := hmac.New(sha256.New, c.signingKey)
	_, _ = mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (c *Codec) Decode(raw string, target any) error {
	if c == nil || len(c.signingKey) == 0 {
		return ErrInvalidKey
	}
	if target == nil ||
		raw == "" ||
		len(raw) > maxEncodedCursorBytes ||
		strings.IndexFunc(raw, unicode.IsSpace) >= 0 {
		return ErrInvalidCursor
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return ErrInvalidCursor
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(payload) == 0 || len(payload) > maxPayloadBytes {
		return ErrInvalidCursor
	}
	providedMAC, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(providedMAC) != sha256.Size {
		return ErrInvalidCursor
	}
	expectedMAC := hmac.New(sha256.New, c.signingKey)
	_, _ = expectedMAC.Write(payload)
	if !hmac.Equal(providedMAC, expectedMAC.Sum(nil)) {
		return ErrInvalidCursor
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrInvalidCursor
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalidCursor
	}
	return nil
}
