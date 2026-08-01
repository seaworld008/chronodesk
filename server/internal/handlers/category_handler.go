package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/safeconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// CategoryHandler exposes the read-only category catalogue used by ticket
// forms and reference fields. Category administration remains outside this
// human-facing API.
type CategoryHandler struct {
	db *gorm.DB
}

func NewCategoryHandler(db *gorm.DB) *CategoryHandler {
	return &CategoryHandler{db: db}
}

type categoryListFilter struct {
	IDs      []uint `json:"ids"`
	Status   string `json:"status"`
	ParentID *uint  `json:"parent_id"`
	Q        string `json:"q"`
	Search   string `json:"search"`
}

type categoryResponse struct {
	ID          uint                  `json:"id"`
	Name        string                `json:"name"`
	Slug        string                `json:"slug"`
	Description string                `json:"description"`
	Icon        string                `json:"icon"`
	Color       string                `json:"color"`
	Type        models.CategoryType   `json:"type"`
	Status      models.CategoryStatus `json:"status"`
	SortOrder   int                   `json:"sort_order"`
	ParentID    *uint                 `json:"parent_id"`
	Level       int                   `json:"level"`
	Path        string                `json:"path"`
	IsDefault   bool                  `json:"is_default"`
	IsPublic    bool                  `json:"is_public"`
}

var categoryListQuerySpec = directoryListQuerySpec{
	DefaultSortBy:    "sort_order",
	DefaultSortOrder: "asc",
	SortFields: map[string]struct{}{
		"id":         {},
		"name":       {},
		"slug":       {},
		"sort_order": {},
		"status":     {},
		"type":       {},
	},
	FilterFields: map[string]struct{}{
		"filter": {},
		"search": {},
		"sort":   {},
		"status": {},
	},
}

func (h *CategoryHandler) List(c *gin.Context) {
	query, ok := h.visibleQuery(c)
	if !ok {
		return
	}
	listQuery, err := parseDirectoryListQuery(
		c.Request.URL.RawQuery,
		categoryListQuerySpec,
	)
	if err != nil {
		writeInvalidCategoryListQuery(c)
		return
	}
	sortBy, sortOrder, err := categoryListSort(listQuery)
	if err != nil {
		writeInvalidCategoryListQuery(c)
		return
	}

	var filter categoryListFilter
	if rawFilter, ok := listQuery.value("filter"); ok {
		if err := decodeCategoryListFilter(rawFilter, &filter); err != nil {
			writeInvalidCategoryListQuery(c)
			return
		}
	}
	if err := validateCategoryListFilter(filter); err != nil {
		writeInvalidCategoryListQuery(c)
		return
	}

	query = query.Model(&models.Category{})
	if len(filter.IDs) > 0 {
		query = query.Where("categories.id IN ?", filter.IDs)
	}

	status, directStatus := listQuery.value("status")
	if directStatus && filter.Status != "" {
		writeInvalidCategoryListQuery(c)
		return
	}
	if !directStatus {
		status = filter.Status
	}
	if status != "" {
		switch models.CategoryStatus(status) {
		case models.CategoryStatusActive, models.CategoryStatusInactive, models.CategoryStatusArchived:
			query = query.Where("status = ?", status)
		default:
			c.JSON(http.StatusBadRequest, gin.H{
				"code": 1,
				"msg":  "分类状态无效",
			})
			return
		}
	}

	if filter.ParentID != nil {
		query = query.Where("parent_id = ?", *filter.ParentID)
	}

	search, directSearch := listQuery.value("search")
	filterSearch, err := categoryFilterSearch(filter)
	if err != nil || directSearch && filterSearch != "" {
		writeInvalidCategoryListQuery(c)
		return
	}
	if !directSearch {
		search = filterSearch
	}
	if err := validateDirectorySearch(search); err != nil {
		writeInvalidCategoryListQuery(c)
		return
	}
	if search != "" {
		escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(search)
		query = query.Where(
			"(LOWER(name) LIKE LOWER(?) ESCAPE '\\' OR LOWER(slug) LIKE LOWER(?) ESCAPE '\\')",
			"%"+escaped+"%",
			"%"+escaped+"%",
		)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		logHandlerFailure(c, "category.count", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "统计分类数量失败",
		})
		return
	}

	var categories []models.Category
	if err := query.
		Order(categoryOrderClause(sortBy, sortOrder)).
		Limit(listQuery.PageSize).
		Offset((listQuery.Page - 1) * listQuery.PageSize).
		Find(&categories).Error; err != nil {
		logHandlerFailure(c, "category.list", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "获取分类列表失败",
		})
		return
	}

	items := make([]categoryResponse, 0, len(categories))
	for i := range categories {
		items = append(items, categoryDTO(&categories[i]))
	}
	c.Header("X-Total-Count", strconv.FormatInt(total, 10))
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"items":       items,
			"total":       total,
			"page":        listQuery.Page,
			"page_size":   listQuery.PageSize,
			"total_pages": directoryTotalPages(total, listQuery.PageSize),
		},
	})
}

func (h *CategoryHandler) Get(c *gin.Context) {
	query, ok := h.visibleQuery(c)
	if !ok {
		return
	}
	id, err := safeconv.ParsePositiveUint(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "分类 ID 无效",
		})
		return
	}

	var category models.Category
	if err := query.Where("categories.id = ?", id).
		First(&category).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 1,
				"msg":  "未找到分类",
			})
			return
		}
		logHandlerFailure(c, "category.get", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "获取分类失败",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": categoryDTO(&category),
	})
}

func (h *CategoryHandler) visibleQuery(
	c *gin.Context,
) (*gorm.DB, bool) {
	access, ok := ProjectAccessFromGin(c)
	if !ok || access.Scope.Validate() != nil {
		c.JSON(http.StatusForbidden, gin.H{
			"code": "project_access_denied",
			"msg":  "无权访问该项目",
		})
		return nil, false
	}
	query := h.db.WithContext(c.Request.Context()).
		Model(&models.Category{}).
		Where(
			"categories.organization_id = ? AND categories.project_id = ?",
			access.Scope.OrganizationID,
			access.Scope.ProjectID,
		)
	switch access.Role {
	case models.ProjectRoleAdmin,
		models.ProjectRoleManager:
		return query, true
	default:
		return query.Where(
			"categories.status = ? AND categories.is_public = ?",
			models.CategoryStatusActive,
			true,
		), true
	}
}

func writeInvalidCategoryListQuery(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{
		"code": 1,
		"msg":  "分类筛选条件无效",
	})
}

func decodeCategoryListFilter(
	raw string,
	filter *categoryListFilter,
) error {
	if len(raw) > 8_192 || raw == "" || raw[0] != '{' {
		return errInvalidDirectoryListQuery
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(filter); err != nil {
		return errInvalidDirectoryListQuery
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errInvalidDirectoryListQuery
	}
	return nil
}

func validateCategoryListFilter(filter categoryListFilter) error {
	if len(filter.IDs) > 100 {
		return errInvalidDirectoryListQuery
	}
	seen := make(map[uint]struct{}, len(filter.IDs))
	for _, id := range filter.IDs {
		if id == 0 {
			return errInvalidDirectoryListQuery
		}
		if _, exists := seen[id]; exists {
			return errInvalidDirectoryListQuery
		}
		seen[id] = struct{}{}
	}
	if filter.ParentID != nil && *filter.ParentID == 0 {
		return errInvalidDirectoryListQuery
	}
	if filter.Status != "" {
		switch models.CategoryStatus(filter.Status) {
		case models.CategoryStatusActive,
			models.CategoryStatusInactive,
			models.CategoryStatusArchived:
		default:
			return errInvalidDirectoryListQuery
		}
	}
	_, err := categoryFilterSearch(filter)
	return err
}

func categoryFilterSearch(filter categoryListFilter) (string, error) {
	if filter.Q != "" && filter.Search != "" {
		return "", errInvalidDirectoryListQuery
	}
	search := filter.Search
	if search == "" {
		search = filter.Q
	}
	if err := validateDirectorySearch(search); err != nil {
		return "", err
	}
	return search, nil
}

func validateDirectorySearch(search string) error {
	if search == "" {
		return nil
	}
	if strings.TrimSpace(search) != search ||
		!utf8.ValidString(search) ||
		utf8.RuneCountInString(search) > 200 ||
		containsDirectoryQueryControl(search) {
		return errInvalidDirectoryListQuery
	}
	return nil
}

func categoryListSort(
	query directoryListQuery,
) (string, string, error) {
	legacySort, legacy := query.value("sort")
	_, hasSortBy := query.value("sort_by")
	_, hasSortOrder := query.value("sort_order")
	if !legacy {
		return query.SortBy, query.SortOrder, nil
	}
	if hasSortBy || hasSortOrder {
		return "", "", errInvalidDirectoryListQuery
	}
	var values []string
	decoder := json.NewDecoder(bytes.NewBufferString(legacySort))
	if err := decoder.Decode(&values); err != nil || len(values) != 2 {
		return "", "", errInvalidDirectoryListQuery
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "", "", errInvalidDirectoryListQuery
	}
	if _, ok := categoryListQuerySpec.SortFields[values[0]]; !ok {
		return "", "", errInvalidDirectoryListQuery
	}
	order := strings.ToLower(values[1])
	if order != "asc" && order != "desc" {
		return "", "", errInvalidDirectoryListQuery
	}
	return values[0], order, nil
}

func categoryOrderClause(sortBy, sortOrder string) string {
	columns := map[string]string{
		"id":         "categories.id",
		"name":       "categories.name",
		"slug":       "categories.slug",
		"sort_order": "categories.sort_order",
		"status":     "categories.status",
		"type":       "categories.type",
	}
	order := "ASC"
	if sortOrder == "desc" {
		order = "DESC"
	}
	if sortBy == "id" {
		return columns[sortBy] + " " + order
	}
	if sortBy == "sort_order" {
		return columns[sortBy] + " " + order +
			", categories.name " + order +
			", categories.id " + order
	}
	return columns[sortBy] + " " + order +
		", categories.id " + order
}

func categoryDTO(category *models.Category) categoryResponse {
	return categoryResponse{
		ID:          category.ID,
		Name:        category.Name,
		Slug:        category.Slug,
		Description: category.Description,
		Icon:        category.Icon,
		Color:       category.Color,
		Type:        category.Type,
		Status:      category.Status,
		SortOrder:   category.SortOrder,
		ParentID:    category.ParentID,
		Level:       category.Level,
		Path:        category.Path,
		IsDefault:   category.IsDefault,
		IsPublic:    category.IsPublic,
	}
}

func directoryTotalPages(total int64, pageSize int) int64 {
	if total == 0 {
		return 0
	}
	return (total-1)/int64(pageSize) + 1
}
