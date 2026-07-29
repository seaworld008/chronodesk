package httpcontract

import (
	"errors"
	"math"
	"testing"
)

func TestFormatETag(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		version uint64
		want    string
	}{
		{version: 1, want: `"v1"`},
		{version: 17, want: `"v17"`},
		{version: math.MaxUint64, want: `"v18446744073709551615"`},
	} {
		if got := FormatETag(test.version); got != test.want {
			t.Errorf("FormatETag(%d) = %q, want %q", test.version, got, test.want)
		}
	}
}

func TestParseIfMatch(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		value   string
		want    uint64
		wantErr error
	}{
		{name: "minimum", value: `"v1"`, want: 1},
		{name: "trim outer whitespace", value: " \t\"v42\"\n", want: 42},
		{
			name:  "maximum",
			value: `"v18446744073709551615"`,
			want:  math.MaxUint64,
		},
		{name: "missing", value: "", wantErr: ErrIfMatchRequired},
		{name: "whitespace", value: " \t ", wantErr: ErrIfMatchRequired},
		{name: "zero", value: `"v0"`, wantErr: ErrInvalidIfMatch},
		{name: "unquoted", value: "v1", wantErr: ErrInvalidIfMatch},
		{name: "wrong prefix", value: `"1"`, wantErr: ErrInvalidIfMatch},
		{name: "weak", value: `W/"v1"`, wantErr: ErrInvalidIfMatch},
		{name: "wildcard", value: "*", wantErr: ErrInvalidIfMatch},
		{name: "list", value: `"v1", "v2"`, wantErr: ErrInvalidIfMatch},
		{name: "negative", value: `"v-1"`, wantErr: ErrInvalidIfMatch},
		{name: "overflow", value: `"v18446744073709551616"`, wantErr: ErrInvalidIfMatch},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseIfMatch(test.value)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ParseIfMatch(%q) error = %v, want %v", test.value, err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("ParseIfMatch(%q) = %d, want %d", test.value, got, test.want)
			}
		})
	}
}
