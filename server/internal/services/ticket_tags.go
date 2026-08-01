package services

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

const (
	maxTicketTags     = 20
	maxTicketTagRunes = 50
)

var ErrInvalidTicketTags = errors.New("invalid ticket tags")

// normalizeTicketTags is the protocol-neutral Ticket tag boundary. Adapters
// may improve input ergonomics, but every human and machine command receives
// the same trimming, case-insensitive uniqueness, and size rules here.
func normalizeTicketTags(tags []string) (models.StringList, error) {
	normalized := make(models.StringList, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, raw := range tags {
		tag := strings.TrimSpace(raw)
		if tag == "" {
			continue
		}
		if utf8.RuneCountInString(tag) > maxTicketTagRunes {
			return nil, fmt.Errorf(
				"%w: each tag must contain at most %d characters",
				ErrInvalidTicketTags,
				maxTicketTagRunes,
			)
		}
		key := strings.ToLower(tag)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, tag)
		if len(normalized) > maxTicketTags {
			return nil, fmt.Errorf(
				"%w: at most %d tags are allowed",
				ErrInvalidTicketTags,
				maxTicketTags,
			)
		}
	}
	return normalized, nil
}
