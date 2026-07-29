package safeconv

import (
	"errors"
	"math"
	"strconv"
	"testing"
)

func TestParseUintUsesNativeWidth(t *testing.T) {
	maximum := strconv.FormatUint(uint64(math.MaxUint), 10)
	got, err := ParseUint(maximum)
	if err != nil {
		t.Fatalf("ParseUint(%q) error = %v", maximum, err)
	}
	if got != math.MaxUint {
		t.Fatalf("ParseUint(%q) = %d, want %d", maximum, got, uint(math.MaxUint))
	}
	if got, err := Uint(uint64(math.MaxUint)); err != nil || got != math.MaxUint {
		t.Fatalf("Uint(MaxUint) = (%d, %v), want (%d, nil)", got, err, uint(math.MaxUint))
	}

	if _, err := ParseUint("18446744073709551616"); err == nil {
		t.Fatal("ParseUint() accepted a value larger than uint64")
	}
	if _, err := ParseUint("-1"); err == nil {
		t.Fatal("ParseUint() accepted a negative value")
	}
}

func TestUintRejectsValueThatWouldNarrowOn32Bit(t *testing.T) {
	// This always exceeds a 32-bit uint and remains representable on 64-bit
	// hosts. The branch lets the suite exercise the actual target width rather
	// than assuming the architecture used by CI.
	if strconv.IntSize != 32 {
		t.Skip("native uint is 64-bit")
	}
	value := uint64(math.MaxUint32) + 1
	if _, err := Uint(value); !errors.Is(err, ErrIntegerOutOfRange) {
		t.Fatalf("Uint(%d) error = %v, want ErrIntegerOutOfRange", value, err)
	}
	if _, err := ParseUint(strconv.FormatUint(value, 10)); !errors.Is(err, ErrIntegerOutOfRange) {
		t.Fatalf("ParseUint(%d) error = %v, want ErrIntegerOutOfRange", value, err)
	}
}

func TestPositiveUintRejectsZeroAndOverflow(t *testing.T) {
	if _, err := ParsePositiveUint("0"); !errors.Is(err, ErrIntegerOutOfRange) {
		t.Fatalf("ParsePositiveUint(0) error = %v, want ErrIntegerOutOfRange", err)
	}
	if _, err := PositiveUint(0); !errors.Is(err, ErrIntegerOutOfRange) {
		t.Fatalf("PositiveUint(0) error = %v, want ErrIntegerOutOfRange", err)
	}
	if strconv.IntSize == 32 {
		if _, err := PositiveUint(uint64(math.MaxUint32) + 1); !errors.Is(err, ErrIntegerOutOfRange) {
			t.Fatalf("PositiveUint(overflow) error = %v, want ErrIntegerOutOfRange", err)
		}
	}
}

func TestIntUsesNativeWidth(t *testing.T) {
	got, err := Int(int64(math.MaxInt))
	if err != nil {
		t.Fatalf("Int(MaxInt) error = %v", err)
	}
	if got != math.MaxInt {
		t.Fatalf("Int(MaxInt) = %d, want %d", got, int(math.MaxInt))
	}
	if strconv.IntSize == 32 {
		if _, err := Int(int64(math.MaxInt32) + 1); !errors.Is(err, ErrIntegerOutOfRange) {
			t.Fatalf("Int(overflow) error = %v, want ErrIntegerOutOfRange", err)
		}
	}
}
