package a2a

import (
	"encoding/json"
	"math"
	"strconv"
	"testing"
)

func TestLinkedTicketIDRejectsNativeUintOverflowAndFraction(t *testing.T) {
	overflow := "18446744073709551616"
	if strconv.IntSize == 32 {
		overflow = strconv.FormatUint(uint64(math.MaxUint32)+1, 10)
	}
	tests := []struct {
		name  string
		value any
	}{
		{name: "JSON number overflow", value: json.Number(overflow)},
		{name: "uint64 overflow", value: uint64(math.MaxUint)},
		{name: "fraction", value: 42.5},
	}
	if strconv.IntSize == 64 {
		tests[1].value = uint64(0)
	} else {
		tests[1].value = uint64(math.MaxUint32) + 1
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := linkedTicketIDFromMetadata(map[string]any{
				MetadataLinkedTicketID: test.value,
			}); err == nil {
				t.Fatal("linkedTicketIDFromMetadata() accepted invalid native uint")
			}
		})
	}
}
