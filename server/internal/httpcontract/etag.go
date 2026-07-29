// Package httpcontract contains protocol-neutral HTTP value contracts shared
// by ChronoDesk's Human and machine adapters.
package httpcontract

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	ErrIfMatchRequired = errors.New("If-Match is required")
	ErrInvalidIfMatch  = errors.New(`If-Match must use the format "v<number>"`)
)

// FormatETag returns the strong validator for one positive resource version.
func FormatETag(version uint64) string {
	return fmt.Sprintf(`"v%d"`, version)
}

// ParseIfMatch accepts exactly one strong ChronoDesk resource validator.
//
// Weak validators, wildcards, lists, zero, and unquoted values are rejected:
// mutating commands must identify the exact resource version they observed.
func ParseIfMatch(value string) (uint64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, ErrIfMatchRequired
	}
	if len(value) < 4 ||
		value[0] != '"' ||
		value[len(value)-1] != '"' ||
		value[1] != 'v' {
		return 0, ErrInvalidIfMatch
	}
	version, err := strconv.ParseUint(value[2:len(value)-1], 10, 64)
	if err != nil || version == 0 {
		return 0, ErrInvalidIfMatch
	}
	return version, nil
}
