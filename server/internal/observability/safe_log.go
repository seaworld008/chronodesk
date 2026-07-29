// Package observability provides small, protocol-neutral helpers for producing
// operational telemetry without trusting values that originated outside the
// process.
package observability

import (
	"strings"
	"unicode"
)

const maxLogValueRunes = 256

// SafeLogValue removes characters that can forge or visually reorder plaintext
// log entries and bounds the resulting field. Callers must still avoid passing
// credentials, secrets, OTPs, passwords, or tokens to this function: redaction
// is a data-minimization decision, not a substitute for sanitization.
func SafeLogValue(value string) string {
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", "")

	var builder strings.Builder
	builder.Grow(min(len(value), maxLogValueRunes))
	runeCount := 0
	truncated := false
	for _, current := range value {
		if unicode.IsControl(current) || unicode.In(current, unicode.Cf) {
			continue
		}
		if runeCount == maxLogValueRunes {
			truncated = true
			break
		}
		builder.WriteRune(current)
		runeCount++
	}
	if truncated {
		builder.WriteString("...")
	}

	// Keep these replacements at the final data-flow boundary. In addition to
	// defense in depth, they make the plaintext log-safety contract explicit to
	// static analyzers.
	safe := strings.ReplaceAll(builder.String(), "\r", "")
	return strings.ReplaceAll(safe, "\n", "")
}
