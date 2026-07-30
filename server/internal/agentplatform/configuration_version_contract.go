package agentplatform

import (
	"strings"

	"github.com/google/uuid"
)

// normalizeMachineConfigurationVersionID accepts only the canonical UUID
// representation used by project configuration resources. Project ownership,
// publication state, and release membership remain authoritative domain checks.
func normalizeMachineConfigurationVersionID(value string) (string, bool) {
	value = strings.TrimSpace(value)
	parsed, err := uuid.Parse(value)
	if err != nil || len(value) != len(parsed.String()) ||
		!strings.EqualFold(value, parsed.String()) {
		return "", false
	}
	return parsed.String(), true
}
