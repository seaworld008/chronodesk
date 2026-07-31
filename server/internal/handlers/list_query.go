package handlers

import (
	"errors"
	"math"
	"net/url"
	"strconv"
	"strings"
)

const (
	defaultDirectoryPageSize = 25
	maxDirectoryPageSize     = 100
)

var errInvalidDirectoryListQuery = errors.New("目录查询参数无效")

type directoryListQuerySpec struct {
	DefaultSortBy    string
	DefaultSortOrder string
	SortFields       map[string]struct{}
	FilterFields     map[string]struct{}
}

type directoryListQuery struct {
	Page      int
	PageSize  int
	SortBy    string
	SortOrder string
}

func parseDirectoryListQuery(
	values url.Values,
	spec directoryListQuerySpec,
) (directoryListQuery, error) {
	if spec.DefaultSortBy == "" ||
		(spec.DefaultSortOrder != "asc" &&
			spec.DefaultSortOrder != "desc") {
		return directoryListQuery{}, errInvalidDirectoryListQuery
	}
	if _, ok := spec.SortFields[spec.DefaultSortBy]; !ok {
		return directoryListQuery{}, errInvalidDirectoryListQuery
	}

	allowed := map[string]struct{}{
		"page":       {},
		"page_size":  {},
		"sort_by":    {},
		"sort_order": {},
	}
	for field := range spec.FilterFields {
		allowed[field] = struct{}{}
	}
	for key, entries := range values {
		if _, ok := allowed[key]; !ok || len(entries) != 1 ||
			strings.TrimSpace(entries[0]) == "" {
			return directoryListQuery{}, errInvalidDirectoryListQuery
		}
	}

	page, err := parseDirectoryPositiveInt(
		values,
		"page",
		1,
		math.MaxInt/defaultDirectoryPageSize,
	)
	if err != nil {
		return directoryListQuery{}, err
	}
	pageSize, err := parseDirectoryPositiveInt(
		values,
		"page_size",
		defaultDirectoryPageSize,
		maxDirectoryPageSize,
	)
	if err != nil {
		return directoryListQuery{}, err
	}
	if page > math.MaxInt/pageSize {
		return directoryListQuery{}, errInvalidDirectoryListQuery
	}

	sortBy := spec.DefaultSortBy
	if raw, ok := values["sort_by"]; ok {
		sortBy = raw[0]
	}
	if _, ok := spec.SortFields[sortBy]; !ok {
		return directoryListQuery{}, errInvalidDirectoryListQuery
	}
	sortOrder := spec.DefaultSortOrder
	if raw, ok := values["sort_order"]; ok {
		sortOrder = raw[0]
	}
	if sortOrder != "asc" && sortOrder != "desc" {
		return directoryListQuery{}, errInvalidDirectoryListQuery
	}

	return directoryListQuery{
		Page:      page,
		PageSize:  pageSize,
		SortBy:    sortBy,
		SortOrder: sortOrder,
	}, nil
}

func parseDirectoryPositiveInt(
	values url.Values,
	key string,
	defaultValue int,
	maximum int,
) (int, error) {
	raw, exists := values[key]
	if !exists {
		return defaultValue, nil
	}
	value := raw[0]
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, errInvalidDirectoryListQuery
		}
	}
	parsed, err := strconv.ParseUint(value, 10, 0)
	if err != nil || parsed == 0 || parsed > uint64(maximum) {
		return 0, errInvalidDirectoryListQuery
	}
	return int(parsed), nil
}

func directoryQueryValue(
	values url.Values,
	key string,
) (string, bool) {
	entry, ok := values[key]
	if !ok {
		return "", false
	}
	return entry[0], true
}
