package websocket

import (
	"strings"
	"testing"
)

func TestWebSocketLogIdentifiersAreSingleLine(t *testing.T) {
	t.Parallel()

	for name, value := range map[string]string{
		"user_id":            safeLogUint(^uint(0)),
		"notification_id":    safeLogUint(42),
		"notification_count": safeLogInt64(99),
	} {
		if value == "" {
			t.Fatalf("%s log value is empty", name)
		}
		if strings.ContainsAny(value, "\r\n") {
			t.Fatalf("%s log value contains a line boundary: %q", name, value)
		}
	}
}
