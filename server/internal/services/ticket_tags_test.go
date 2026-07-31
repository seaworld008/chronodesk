package services

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeTicketTags(t *testing.T) {
	t.Run("trims and deduplicates case insensitively", func(t *testing.T) {
		tags, err := normalizeTicketTags([]string{"  Urgent ", "urgent", "客户"})
		if err != nil {
			t.Fatalf("normalize ticket tags: %v", err)
		}
		if len(tags) != 2 || tags[0] != "Urgent" || tags[1] != "客户" {
			t.Fatalf("unexpected tags: %#v", tags)
		}
	})

	t.Run("rejects more than twenty tags", func(t *testing.T) {
		raw := make([]string, 21)
		for index := range raw {
			raw[index] = "tag-" + string(rune('a'+index))
		}
		_, err := normalizeTicketTags(raw)
		if !errors.Is(err, ErrInvalidTicketTags) {
			t.Fatalf("expected invalid tags, got %v", err)
		}
	})

	t.Run("counts unicode characters instead of bytes", func(t *testing.T) {
		if _, err := normalizeTicketTags([]string{strings.Repeat("中", 50)}); err != nil {
			t.Fatalf("50 unicode characters must be accepted: %v", err)
		}
		_, err := normalizeTicketTags([]string{strings.Repeat("中", 51)})
		if !errors.Is(err, ErrInvalidTicketTags) {
			t.Fatalf("expected invalid tags, got %v", err)
		}
	})
}
