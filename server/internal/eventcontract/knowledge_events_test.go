package eventcontract

import (
	"regexp"
	"testing"
)

func TestKnowledgeEventTypesAreStableVersionedCloudEvents(t *testing.T) {
	pattern := regexp.MustCompile(
		`^io\.chronodesk\.knowledge\.[a-z0-9.-]+\.v1$`,
	)
	seen := map[string]struct{}{}
	for _, eventType := range []string{
		KnowledgeDraftCreatedEventType,
		KnowledgeVersionPublishedEventType,
		KnowledgeIndexRebuildRequestedEventType,
	} {
		if !pattern.MatchString(eventType) {
			t.Errorf("knowledge event type is not versioned: %q", eventType)
		}
		if _, duplicate := seen[eventType]; duplicate {
			t.Errorf("duplicate knowledge event type: %q", eventType)
		}
		seen[eventType] = struct{}{}
	}
}
