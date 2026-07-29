package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"gongdan-system/internal/models"

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

type assigneeFilter struct {
	IDs    []uint `json:"ids"`
	Q      string `json:"q"`
	Search string `json:"search"`
}

func NewAssigneeHandler(db *gorm.DB) *AssigneeHandler {
	return &AssigneeHandler{db: db}
}

func (h *AssigneeHandler) List(c *gin.Context) {
	if !canAssignTickets(c.GetString("user_role")) {
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{"items": []assigneeResponse{}, "total": 0},
		})
		return
	}

	var filter assigneeFilter
	if rawFilter := strings.TrimSpace(c.Query("filter")); rawFilter != "" {
		if err := json.Unmarshal([]byte(rawFilter), &filter); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 1, "msg": "处理人筛选条件无效"})
			return
		}
	}

	query := h.assignableQuery(c)
	if len(filter.IDs) > 0 {
		query = query.Where("id IN ?", filter.IDs)
	}
	search := strings.TrimSpace(c.Query("search"))
	if search == "" {
		search = strings.TrimSpace(filter.Search)
	}
	if search == "" {
		search = strings.TrimSpace(filter.Q)
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
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "msg": "统计处理人数量失败"})
		return
	}

	page := parsePositiveInt(c.Query("page"), 1, 1_000_000)
	pageSize := parsePositiveInt(c.Query("page_size"), 25, 100)
	var users []models.User
	if err := query.
		Order("username ASC, id ASC").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&users).Error; err != nil {
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
		"data": gin.H{"items": items, "total": total},
	})
}

func (h *AssigneeHandler) Get(c *gin.Context) {
	if !canAssignTickets(c.GetString("user_role")) {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "msg": "未找到可分配的处理人"})
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "msg": "处理人 ID 无效"})
		return
	}

	var user models.User
	if err := h.assignableQuery(c).First(&user, uint(id)).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"code": 1, "msg": "未找到可分配的处理人"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "msg": "获取处理人失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": assigneeDTO(&user)})
}

func (h *AssigneeHandler) assignableQuery(c *gin.Context) *gorm.DB {
	return h.db.WithContext(c.Request.Context()).
		Model(&models.User{}).
		Where("status = ?", models.UserStatusActive).
		Where("role IN ?", []models.UserRole{
			models.RoleAgent,
			models.RoleSupervisor,
			models.RoleAdmin,
			models.RoleSuperUser,
		})
}

func assigneeDTO(user *models.User) assigneeResponse {
	return assigneeResponse{
		ID:          user.ID,
		Username:    user.Username,
		FirstName:   user.FirstName,
		LastName:    user.LastName,
		DisplayName: user.DisplayName,
		Role:        string(user.Role),
	}
}

func canAssignTickets(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case string(models.RoleAgent),
		string(models.RoleSupervisor),
		string(models.RoleAdmin),
		string(models.RoleSuperUser):
		return true
	default:
		return false
	}
}
