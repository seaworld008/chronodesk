package services

import (
	"errors"
	"strings"
	"testing"

	"gorm.io/datatypes"
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

func TestSanitizeTicketChangesUsesCanonicalTagBoundary(t *testing.T) {
	changes, fields, err := sanitizeTicketChanges(map[string]any{
		"tags": []any{"  Urgent ", "urgent", "客户"},
	})
	if err != nil {
		t.Fatalf("sanitize tags: %v", err)
	}
	if len(fields) != 1 || fields[0] != "tags" {
		t.Fatalf("unexpected fields: %#v", fields)
	}
	tags, ok := changes["tags"].(datatypes.JSONSlice[string])
	if !ok {
		t.Fatalf("unexpected sanitized tags type: %T", changes["tags"])
	}
	if len(tags) != 2 || tags[0] != "Urgent" || tags[1] != "客户" {
		t.Fatalf("unexpected sanitized tags: %#v", tags)
	}

	_, _, err = sanitizeTicketChanges(map[string]any{
		"tags": []string{strings.Repeat("x", 51)},
	})
	if !errors.Is(err, ErrInvalidTicketTags) {
		t.Fatalf("expected shared invalid tags error, got %v", err)
	}
	if code := AgentNativeErrorCode(err); code != "invalid_request" {
		t.Fatalf("unexpected protocol error code: %q", code)
	}
}

func TestBulkTicketChangesCannotBypassCanonicalTagBoundary(t *testing.T) {
	changes, _, err := bulkTicketChanges(&BulkUpdateRequest{
		Tags: []string{"  Bulk ", "bulk", "客户"},
	})
	if err != nil {
		t.Fatalf("bulk ticket changes: %v", err)
	}
	tags := changes["tags"].(datatypes.JSONSlice[string])
	if len(tags) != 2 || tags[0] != "Bulk" || tags[1] != "客户" {
		t.Fatalf("unexpected normalized bulk tags: %#v", tags)
	}

	_, _, err = bulkTicketChanges(&BulkUpdateRequest{
		Tags: []string{strings.Repeat("x", 51)},
	})
	if !errors.Is(err, ErrInvalidBulkTicketUpdate) ||
		!errors.Is(err, ErrInvalidTicketTags) {
		t.Fatalf("bulk error lost stable sentinels: %v", err)
	}
}
