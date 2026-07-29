package handlers

import (
	"math"
	"strconv"
	"testing"
)

func TestParseUintValueRejectsOverflowFractionAndNegative(t *testing.T) {
	overflow := "18446744073709551616"
	if strconv.IntSize == 32 {
		overflow = strconv.FormatUint(uint64(math.MaxUint32)+1, 10)
	}
	tests := []any{
		overflow,
		42.5,
		float64(-1),
		int64(-1),
	}
	for _, value := range tests {
		if got, ok := parseUintValue(value); ok {
			t.Fatalf("parseUintValue(%v) = %d, true; want rejection", value, got)
		}
	}
}
