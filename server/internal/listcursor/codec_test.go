package listcursor

import (
	"errors"
	"strings"
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

func TestCodecRejectsNonCanonicalBase64TrailingBits(t *testing.T) {
	codec, err := NewCodec(
		[]byte("chronodesk-list-cursor-canonical-base64-test-root"),
		"canonical-base64.v1",
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := codec.Encode(testPayload{Version: 1, Scope: 7, ID: "row-25"})
	if err != nil {
		t.Fatal(err)
	}
	replacements := map[byte]byte{
		'A': 'B',
		'E': 'F',
		'I': 'J',
		'M': 'N',
		'Q': 'R',
		'U': 'V',
		'Y': 'Z',
		'c': 'd',
		'g': 'h',
		'k': 'l',
		'o': 'p',
		's': 't',
		'w': 'x',
		'0': '1',
		'4': '5',
		'8': '9',
	}
	last := encoded[len(encoded)-1]
	replacement, ok := replacements[last]
	if !ok {
		t.Fatalf("unexpected canonical base64 suffix %q in %q", last, encoded)
	}
	nonCanonical := encoded[:len(encoded)-1] + string(replacement)
	var decoded testPayload
	if err := codec.Decode(
		nonCanonical,
		&decoded,
	); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("non-canonical cursor error=%v", err)
	}
}

func TestCodecRejectsPaddedStandardAndWhitespaceBase64URL(t *testing.T) {
	codec, err := NewCodec(
		[]byte("chronodesk-list-cursor-strict-alphabet-test-root"),
		"strict-base64url.v1",
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := codec.Encode(testPayload{Version: 1, Scope: 8, ID: "row-26"})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(encoded, ".")
	if len(parts) != 2 {
		t.Fatalf("cursor parts=%d", len(parts))
	}
	cases := map[string]string{
		"padded payload": parts[0] + "=." + parts[1],
		"padded mac":     parts[0] + "." + parts[1] + "=",
		"standard plus":  "+" + parts[0][1:] + "." + parts[1],
		"standard slash": "/" + parts[0][1:] + "." + parts[1],
		"leading space":  " " + encoded,
		"trailing line":  encoded + "\n",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			var decoded testPayload
			if err := codec.Decode(
				raw,
				&decoded,
			); !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("strict cursor error=%v", err)
			}
		})
	}
}
