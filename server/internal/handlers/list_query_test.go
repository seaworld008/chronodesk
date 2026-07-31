package handlers

import (
	"net/url"
	"testing"
)

func TestParseDirectoryListQueryDefaultsAndClosedSorting(t *testing.T) {
	spec := directoryListQuerySpec{
		DefaultSortBy:    "created_at",
		DefaultSortOrder: "desc",
		SortFields: map[string]struct{}{
			"created_at": {},
			"name":       {},
		},
		FilterFields: map[string]struct{}{"status": {}},
	}

	got, err := parseDirectoryListQuery(url.Values{}, spec)
	if err != nil {
		t.Fatalf("parse defaults: %v", err)
	}
	if got.Page != 1 || got.PageSize != 25 ||
		got.SortBy != "created_at" || got.SortOrder != "desc" {
		t.Fatalf("defaults = %+v", got)
	}

	got, err = parseDirectoryListQuery(url.Values{
		"page":       {"2"},
		"page_size":  {"100"},
		"sort_by":    {"name"},
		"sort_order": {"asc"},
		"status":     {"active"},
	}, spec)
	if err != nil {
		t.Fatalf("parse explicit query: %v", err)
	}
	if got.Page != 2 || got.PageSize != 100 ||
		got.SortBy != "name" || got.SortOrder != "asc" {
		t.Fatalf("explicit query = %+v", got)
	}
}

func TestParseDirectoryListQueryRejectsInvalidInput(t *testing.T) {
	spec := directoryListQuerySpec{
		DefaultSortBy:    "created_at",
		DefaultSortOrder: "desc",
		SortFields: map[string]struct{}{
			"created_at": {},
			"name":       {},
		},
		FilterFields: map[string]struct{}{"status": {}},
	}
	tests := []url.Values{
		{"unknown": {"value"}},
		{"page": {"1", "2"}},
		{"page_size": {"25", "50"}},
		{"sort_by": {"name", "created_at"}},
		{"sort_order": {"asc", "desc"}},
		{"status": {"active", "inactive"}},
		{"page": {""}},
		{"page_size": {""}},
		{"sort_by": {""}},
		{"sort_order": {""}},
		{"status": {""}},
		{"page": {"0"}},
		{"page": {"-1"}},
		{"page": {"+1"}},
		{"page": {"1.0"}},
		{"page": {" 1"}},
		{"page_size": {"0"}},
		{"page_size": {"-1"}},
		{"page_size": {"101"}},
		{"page_size": {"not-a-number"}},
		{"sort_by": {"unsafe_column"}},
		{"sort_order": {"ASC"}},
		{"sort_order": {"sideways"}},
	}
	for _, values := range tests {
		t.Run(values.Encode(), func(t *testing.T) {
			if _, err := parseDirectoryListQuery(values, spec); err == nil {
				t.Fatalf("query %q unexpectedly accepted", values.Encode())
			}
		})
	}
}
