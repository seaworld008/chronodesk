// Package safeconv centralizes checked conversions at untrusted protocol and
// infrastructure boundaries. Go's uint and int widths are platform-dependent,
// so parsing into a fixed 64-bit intermediate and casting directly is unsafe.
package safeconv

import (
	"errors"
	"math"
	"strconv"
)

// ErrIntegerOutOfRange identifies an integer that cannot be represented by the
// native-width destination type.
var ErrIntegerOutOfRange = errors.New("integer is outside the native-width range")

// ParseUint parses a base-10 value directly at the platform uint width.
func ParseUint(value string) (uint, error) {
	parsed, err := strconv.ParseUint(value, 10, strconv.IntSize)
	if err != nil {
		return 0, err
	}
	// Keep the explicit constant bound even though ParseUint already received
	// strconv.IntSize. It documents the cast invariant and is provable by
	// security analyzers that do not evaluate platform-width arguments.
	if parsed > uint64(math.MaxUint) {
		return 0, ErrIntegerOutOfRange
	}
	return uint(parsed), nil
}

// ParsePositiveUint parses a non-zero base-10 value at the platform uint width.
func ParsePositiveUint(value string) (uint, error) {
	parsed, err := ParseUint(value)
	if err != nil {
		return 0, err
	}
	if parsed == 0 {
		return 0, ErrIntegerOutOfRange
	}
	return parsed, nil
}

// Uint converts a uint64 only after proving that it fits the platform uint.
func Uint(value uint64) (uint, error) {
	if value > uint64(math.MaxUint) {
		return 0, ErrIntegerOutOfRange
	}
	return uint(value), nil
}

// PositiveUint converts a non-zero uint64 only after proving that it fits the
// platform uint.
func PositiveUint(value uint64) (uint, error) {
	if value == 0 {
		return 0, ErrIntegerOutOfRange
	}
	return Uint(value)
}

// Int converts an int64 only after proving that it fits the platform int.
func Int(value int64) (int, error) {
	if value < int64(math.MinInt) || value > int64(math.MaxInt) {
		return 0, ErrIntegerOutOfRange
	}
	return int(value), nil
}
