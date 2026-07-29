package agentplatform

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/httpcontract"
)

func TestCursorRoundTripAndTamperRejection(t *testing.T) {
	input := Cursor{CreatedAt: time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC), ID: "event-42"}
	encoded := EncodeCursor(input)
	decoded, err := DecodeCursor(encoded)
	if err != nil {
		t.Fatalf("DecodeCursor() error = %v", err)
	}
	if decoded.ID != input.ID || !decoded.CreatedAt.Equal(input.CreatedAt) {
		t.Fatalf("DecodeCursor() = %#v, want %#v", decoded, input)
	}
	if _, err := DecodeCursor(encoded + "!"); err == nil {
		t.Fatal("DecodeCursor() accepted malformed cursor")
	}
}

func TestETagRoundTrip(t *testing.T) {
	if got := httpcontract.FormatETag(17); got != `"v17"` {
		t.Fatalf("httpcontract.FormatETag() = %q", got)
	}
	version, err := httpcontract.ParseIfMatch(`"v17"`)
	if err != nil || version != 17 {
		t.Fatalf("httpcontract.ParseIfMatch() = %d, %v", version, err)
	}
	for _, invalid := range []string{"", "v17", `"17"`, `"v0"`, "*"} {
		if _, err := httpcontract.ParseIfMatch(invalid); err == nil {
			t.Fatalf("httpcontract.ParseIfMatch(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestCommandFingerprintBindsResourceVersionAndLease(t *testing.T) {
	body := []byte(`{"priority":"high"}`)
	baseline := commandFingerprint(http.MethodPatch, "/api/v1/tickets/1", 7, "lease-1", body)
	if !bytes.Equal(
		baseline,
		commandFingerprint(http.MethodPatch, "/api/v1/tickets/1", 7, "lease-1", body),
	) {
		t.Fatal("equivalent commands must have a stable fingerprint")
	}
	for name, candidate := range map[string][]byte{
		"resource": commandFingerprint(http.MethodPatch, "/api/v1/tickets/2", 7, "lease-1", body),
		"version":  commandFingerprint(http.MethodPatch, "/api/v1/tickets/1", 8, "lease-1", body),
		"lease":    commandFingerprint(http.MethodPatch, "/api/v1/tickets/1", 7, "lease-2", body),
		"method":   commandFingerprint(http.MethodPost, "/api/v1/tickets/1", 7, "lease-1", body),
		"body":     commandFingerprint(http.MethodPatch, "/api/v1/tickets/1", 7, "lease-1", []byte(`{"priority":"urgent"}`)),
	} {
		if bytes.Equal(baseline, candidate) {
			t.Fatalf("%s was not bound into the command fingerprint", name)
		}
	}
}
