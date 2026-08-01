package handlers

import (
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

	got, err := parseDirectoryListQuery("", spec)
	if err != nil {
		t.Fatalf("parse defaults: %v", err)
	}
	if got.Page != 1 || got.PageSize != 25 ||
		got.SortBy != "created_at" || got.SortOrder != "desc" {
		t.Fatalf("defaults = %+v", got)
	}

	got, err = parseDirectoryListQuery(
		"page=2&page_size=100&sort_by=name&sort_order=asc&status=active",
		spec,
	)
	if err != nil {
		t.Fatalf("parse explicit query: %v", err)
	}
	if got.Page != 2 || got.PageSize != 100 ||
		got.SortBy != "name" || got.SortOrder != "asc" {
		t.Fatalf("explicit query = %+v", got)
	}
	if status, ok := got.value("status"); !ok || status != "active" {
		t.Fatalf("status filter = %q, present=%t", status, ok)
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
	tests := []string{
		"unknown=value",
		"page=1&page=2",
		"page_size=25&page_size=50",
		"sort_by=name&sort_by=created_at",
		"sort_order=asc&sort_order=desc",
		"status=active&status=inactive",
		"page=",
		"page_size=",
		"sort_by=",
		"sort_order=",
		"status=",
		"page=0",
		"page=-1",
		"page=%2B1",
		"page=1.0",
		"page=%201",
		"page_size=0",
		"page_size=-1",
		"page_size=101",
		"page_size=not-a-number",
		"sort_by=unsafe_column",
		"sort_order=ASC",
		"sort_order=sideways",
		"page=%",
		"page=%ZZ",
		"unknown=%ZZ",
		"page=1;sort_by=name",
		"status=%FF",
		"%FF=value",
		"status=active%00",
		"status=active%0Ainactive",
	}
	for _, rawQuery := range tests {
		t.Run(rawQuery, func(t *testing.T) {
			if _, err := parseDirectoryListQuery(rawQuery, spec); err == nil {
				t.Fatalf("query %q unexpectedly accepted", rawQuery)
			}
		})
	}
}
