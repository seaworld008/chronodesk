package observability

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSafeLogValueRemovesLineAndDisplayControlCharacters(t *testing.T) {
	t.Parallel()

	got := SafeLogValue("req-1\r\n[ERROR] forged\x00\u202eabc")
	if strings.ContainsAny(got, "\r\n\x00") {
		t.Fatalf("safe log value retained line or C0 controls: %q", got)
	}
	if strings.ContainsRune(got, '\u202e') {
		t.Fatalf("safe log value retained bidi override: %q", got)
	}
	if got != "req-1[ERROR] forgedabc" {
		t.Fatalf("safe log value = %q", got)
	}
}

func TestSafeLogValueBoundsUntrustedFieldsAtRuneBoundary(t *testing.T) {
	t.Parallel()

	got := SafeLogValue(strings.Repeat("工", maxLogValueRunes+10))
	if !utf8.ValidString(got) {
		t.Fatalf("safe log value is not valid UTF-8: %q", got)
	}
	if runeCount := utf8.RuneCountInString(got); runeCount != maxLogValueRunes+3 {
		t.Fatalf("safe log rune count = %d, want %d", runeCount, maxLogValueRunes+3)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("truncated safe log value is missing marker: %q", got)
	}
}
