package agentplatform

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/a2a"
)

func TestA2APushRequestUsesCanonicalMediaTypeAndVersion(t *testing.T) {
	payload := json.RawMessage(`{"statusUpdate":{"taskId":"task-1"}}`)
	request, err := newA2APushRequest(
		context.Background(),
		"https://hooks.example.com/a2a",
		payload,
		"event-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := request.Header.Get("Content-Type"); got != "application/a2a+json" {
		t.Fatalf("Content-Type=%q, want application/a2a+json", got)
	}
	if got := request.Header.Get("A2A-Version"); got != a2a.ProtocolVersion {
		t.Fatalf("A2A-Version=%q, want %q", got, a2a.ProtocolVersion)
	}
	if got := request.Header.Get("X-CloudEvents-ID"); got != "event-1" {
		t.Fatalf("X-CloudEvents-ID=%q, want event-1", got)
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(payload) {
		t.Fatalf("push body=%s, want %s", body, payload)
	}
}
