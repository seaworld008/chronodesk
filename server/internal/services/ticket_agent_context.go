package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

const (
	maxAgentContextGoalRunes       = 4_000
	maxAgentContextItems           = 20
	maxAgentContextItemRunes       = 1_000
	maxAgentContextResourceRunes   = 2_048
	maxAgentContextSerializedBytes = 64 * 1024
)

var ErrInvalidAgentContext = errors.New("invalid Agent context")

// validateAgentContext is the shared Human, MCP and A2A storage boundary.
// Agent facts remain untrusted text, but the persisted Ticket response must
// stay bounded regardless of which Adapter submitted the command.
func validateAgentContext(value *models.AgentContext) error {
	if value == nil {
		return nil
	}
	if utf8.RuneCountInString(value.Goal) > maxAgentContextGoalRunes {
		return fmt.Errorf(
			"%w: goal must contain at most %d characters",
			ErrInvalidAgentContext,
			maxAgentContextGoalRunes,
		)
	}
	for name, list := range map[string][]string{
		"constraints":         value.Constraints,
		"acceptance_criteria": value.AcceptanceCriteria,
		"missing_information": value.MissingInformation,
		"related_resources":   value.RelatedResources,
	} {
		if len(list) > maxAgentContextItems {
			return fmt.Errorf(
				"%w: %s must contain at most %d items",
				ErrInvalidAgentContext,
				name,
				maxAgentContextItems,
			)
		}
		itemLimit := maxAgentContextItemRunes
		if name == "related_resources" {
			itemLimit = maxAgentContextResourceRunes
		}
		for _, item := range list {
			if utf8.RuneCountInString(item) > itemLimit {
				return fmt.Errorf(
					"%w: each %s item must contain at most %d characters",
					ErrInvalidAgentContext,
					name,
					itemLimit,
				)
			}
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%w: encode context: %v", ErrInvalidAgentContext, err)
	}
	if len(encoded) > maxAgentContextSerializedBytes {
		return fmt.Errorf(
			"%w: serialized context must contain at most %d bytes",
			ErrInvalidAgentContext,
			maxAgentContextSerializedBytes,
		)
	}
	return nil
}
