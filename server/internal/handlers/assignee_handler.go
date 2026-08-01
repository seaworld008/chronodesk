package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/safeconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type AssigneeHandler struct {
	db *gorm.DB
}

type assigneeResponse struct {
	ID          uint   `json:"id"`
	Username    string `json:"username"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

type assignableUser struct {
	models.User
	ProjectRole models.ProjectRole `gorm:"column:project_role"`
}

type assigneeFilter struct {
	IDs    []uint `json:"ids"`
	Q      string `json:"q"`
	Search string `json:"search"`
}

var assigneeListQuerySpec = directoryListQuerySpec{
	DefaultSortBy:    "username",
	DefaultSortOrder: "asc",
	SortFields: map[string]struct{}{
		"id":           {},
		"username":     {},
		"first_name":   {},
		"last_name":    {},
		"display_name": {},
		"role":         {},
	},
	FilterFields: map[string]struct{}{
		"filter": {},
		"search": {},
		"sort":   {},
	},
}

func NewAssigneeHandler(db *gorm.DB) *AssigneeHandler {
	return &AssigneeHandler{db: db}
}

func (h *AssigneeHandler) List(c *gin.Context) {
	listQuery, err := parseDirectoryListQuery(
		c.Request.URL.RawQuery,
		assigneeListQuerySpec,
	)
	if err != nil {
		writeInvalidAssigneeListQuery(c)
		return
	}
	sortBy, sortOrder, err := assigneeListSort(listQuery)
	if err != nil {
		writeInvalidAssigneeListQuery(c)
		return
	}

	if !canAssignTickets(normalizedProjectRole(c)) {
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{
				"items":       []assigneeResponse{},
				"total":       0,
				"page":        listQuery.Page,
				"page_size":   listQuery.PageSize,
				"total_pages": 0,
			},
		})
		return
	}

	var filter assigneeFilter
	if rawFilter, ok := listQuery.value("filter"); ok {
		if err := decodeAssigneeListFilter(rawFilter, &filter); err != nil {
			writeInvalidAssigneeListQuery(c)
			return
		}
	}
	if err := validateAssigneeListFilter(filter); err != nil {
		writeInvalidAssigneeListQuery(c)
		return
	}

	query, ok := h.assignableQuery(c)
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{
			"code": 1,
			"msg":  "缺少可信项目范围",
		})
		return
	}
	if len(filter.IDs) > 0 {
		query = query.Where("users.id IN ?", filter.IDs)
	}
	search, directSearch := listQuery.value("search")
	filterSearch, err := assigneeFilterSearch(filter)
	if err != nil || directSearch && filterSearch != "" {
		writeInvalidAssigneeListQuery(c)
		return
	}
	if !directSearch {
		search = filterSearch
	}
	if err := validateDirectorySearch(search); err != nil {
		writeInvalidAssigneeListQuery(c)
		return
	}
	if search != "" {
		escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(search)
		pattern := "%" + escaped + "%"
		query = query.Where(
			"(LOWER(username) LIKE LOWER(?) ESCAPE '\\' OR LOWER(first_name) LIKE LOWER(?) ESCAPE '\\' OR LOWER(last_name) LIKE LOWER(?) ESCAPE '\\' OR LOWER(display_name) LIKE LOWER(?) ESCAPE '\\')",
			pattern,
			pattern,
			pattern,
			pattern,
		)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		logHandlerFailure(c, "assignee.count", err)
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "msg": "统计处理人数量失败"})
		return
	}

	var users []assignableUser
	if err := query.
		Order(assigneeOrderClause(sortBy, sortOrder)).
		Limit(listQuery.PageSize).
		Offset((listQuery.Page - 1) * listQuery.PageSize).
		Find(&users).Error; err != nil {
		logHandlerFailure(c, "assignee.list", err)
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "msg": "获取处理人列表失败"})
		return
	}

	items := make([]assigneeResponse, 0, len(users))
	for i := range users {
		items = append(items, assigneeDTO(&users[i]))
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

func (h *AssigneeHandler) Get(c *gin.Context) {
	if !canAssignTickets(normalizedProjectRole(c)) {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "msg": "未找到可分配的处理人"})
		return
	}
	id, err := safeconv.ParsePositiveUint(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "msg": "处理人 ID 无效"})
		return
	}

	query, ok := h.assignableQuery(c)
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "msg": "缺少可信项目范围"})
		return
	}
	var user assignableUser
	if err := query.Where("users.id = ?", id).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"code": 1, "msg": "未找到可分配的处理人"})
			return
		}
		logHandlerFailure(c, "assignee.get", err)
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "msg": "获取处理人失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": assigneeDTO(&user)})
}

func (h *AssigneeHandler) assignableQuery(
	c *gin.Context,
) (*gorm.DB, bool) {
	access, ok := ProjectAccessFromGin(c)
	if !ok {
		return nil, false
	}
	return h.db.WithContext(c.Request.Context()).
		Model(&models.User{}).
		Select("users.*, project_memberships.role AS project_role").
		Joins(
			"JOIN project_memberships ON project_memberships.user_id = users.id AND project_memberships.project_id = ? AND project_memberships.is_active = ?",
			access.Scope.ProjectID,
			true,
		).
		Where("users.status = ?", models.UserStatusActive).
		Where("project_memberships.role IN ?", []models.ProjectRole{
			models.ProjectRoleAgent,
			models.ProjectRoleManager,
			models.ProjectRoleAdmin,
		}), true
}

func assigneeDTO(user *assignableUser) assigneeResponse {
	return assigneeResponse{
		ID:          user.ID,
		Username:    user.Username,
		FirstName:   user.FirstName,
		LastName:    user.LastName,
		DisplayName: user.DisplayName,
		Role:        string(user.ProjectRole),
	}
}

func canAssignTickets(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case string(models.ProjectRoleAgent),
		string(models.ProjectRoleManager),
		string(models.ProjectRoleAdmin):
		return true
	default:
		return false
	}
}

func writeInvalidAssigneeListQuery(c *gin.Context) {
	c.JSON(
		http.StatusBadRequest,
		gin.H{"code": 1, "msg": "处理人筛选条件无效"},
	)
}

func decodeAssigneeListFilter(
	raw string,
	filter *assigneeFilter,
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

func validateAssigneeListFilter(filter assigneeFilter) error {
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
	_, err := assigneeFilterSearch(filter)
	return err
}

func assigneeFilterSearch(filter assigneeFilter) (string, error) {
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

func assigneeListSort(
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
	if _, ok := assigneeListQuerySpec.SortFields[values[0]]; !ok {
		return "", "", errInvalidDirectoryListQuery
	}
	order := strings.ToLower(values[1])
	if order != "asc" && order != "desc" {
		return "", "", errInvalidDirectoryListQuery
	}
	return values[0], order, nil
}

func assigneeOrderClause(sortBy, sortOrder string) string {
	columns := map[string]string{
		"id":           "users.id",
		"username":     "users.username",
		"first_name":   "users.first_name",
		"last_name":    "users.last_name",
		"display_name": "users.display_name",
		"role":         "project_memberships.role",
	}
	order := "ASC"
	if sortOrder == "desc" {
		order = "DESC"
	}
	if sortBy == "id" {
		return columns[sortBy] + " " + order
	}
	return columns[sortBy] + " " + order + ", users.id " + order
}
