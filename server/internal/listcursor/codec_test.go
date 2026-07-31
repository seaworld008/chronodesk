package listcursor

import (
	"errors"
	"testing"
)

type testPayload struct {
	Version int    `json:"v"`
	Scope   uint   `json:"scope"`
	ID      string `json:"id"`
}

func TestCodecRoundTripTamperPurposeAndStrictPayload(t *testing.T) {
	codec, err := NewCodec(
		[]byte("chronodesk-shared-list-cursor-test-root-key"),
		"agent-events.v1",
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := codec.Encode(testPayload{
		Version: 1,
		Scope:   42,
		ID:      "event-151",
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded testPayload
	if err := codec.Decode(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != (testPayload{Version: 1, Scope: 42, ID: "event-151"}) {
		t.Fatalf("decoded=%+v", decoded)
	}

	tampered := encoded[:len(encoded)-1]
	if encoded[len(encoded)-1] != 'A' {
		tampered += "A"
	} else {
		tampered += "B"
	}
	if err := codec.Decode(tampered, &decoded); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("tampered cursor error=%v", err)
	}

	other, err := NewCodec(
		[]byte("chronodesk-shared-list-cursor-test-root-key"),
		"webhook-deliveries.v1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Decode(encoded, &decoded); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("cross-purpose cursor error=%v", err)
	}

	var closed struct {
		Version int `json:"v"`
	}
	if err := codec.Decode(encoded, &closed); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("unknown payload field error=%v", err)
	}
}

func TestCodecRejectsMissingKeyOrPurpose(t *testing.T) {
	if _, err := NewCodec(nil, "agent-events.v1"); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("missing key error=%v", err)
	}
	if _, err := NewCodec([]byte("key"), " "); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("missing purpose error=%v", err)
	}
}
