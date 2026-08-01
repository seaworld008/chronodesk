package app

import (
	"regexp"
	"strings"
	"testing"
)

func TestKnowledgeObjectCleanupWorkerIDIsBoundedAndPortable(
	t *testing.T,
) {
	workerID := knowledgeObjectCleanupWorkerID(
		strings.Repeat("主机 / unsafe@", 20),
		42,
	)
	if len(workerID) > 96 ||
		!regexp.MustCompile(
			`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,95}$`,
		).MatchString(workerID) {
		t.Fatalf("knowledge cleanup worker ID = %q", workerID)
	}
}
