package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/safeconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// CategoryHandler exposes the read-only category catalogue used by ticket
// forms and reference fields. Category administration remains outside this
// compatibility API.
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

func (h *CategoryHandler) List(c *gin.Context) {
	page := parsePositiveInt(c.Query("page"), 1, 1_000_000)
	pageSize := parsePositiveInt(c.Query("page_size"), 25, 100)

	var filter categoryListFilter
	if rawFilter := strings.TrimSpace(c.Query("filter")); rawFilter != "" {
		if err := json.Unmarshal([]byte(rawFilter), &filter); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"code": 1,
				"msg":  "分类筛选条件无效",
			})
			return
		}
	}

	query := h.visibleQuery(c).Model(&models.Category{})
	if len(filter.IDs) > 0 {
		query = query.Where("id IN ?", filter.IDs)
	}

	status := strings.TrimSpace(c.Query("status"))
	if status == "" {
		status = strings.TrimSpace(filter.Status)
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

	search := strings.TrimSpace(c.Query("search"))
	if search == "" {
		search = strings.TrimSpace(filter.Search)
	}
	if search == "" {
		search = strings.TrimSpace(filter.Q)
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
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "统计分类数量失败",
		})
		return
	}

	var categories []models.Category
	if err := query.
		Order("sort_order ASC, name ASC, id ASC").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&categories).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "获取分类列表失败",
		})
		return
	}

	c.Header("X-Total-Count", strconv.FormatInt(total, 10))
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"items": categories,
			"total": total,
		},
	})
}

func (h *CategoryHandler) Get(c *gin.Context) {
	id, err := safeconv.ParsePositiveUint(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "分类 ID 无效",
		})
		return
	}

	var category models.Category
	if err := h.visibleQuery(c).First(&category, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 1,
				"msg":  "未找到分类",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "获取分类失败",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": category,
	})
}

func (h *CategoryHandler) visibleQuery(c *gin.Context) *gorm.DB {
	query := h.db.Model(&models.Category{})
	role, _ := c.Get("user_role")
	roleName, _ := role.(string)
	switch roleName {
	case string(models.RoleAdmin), string(models.RoleSuperUser):
		return query
	default:
		return query.Where("status = ? AND is_public = ?", models.CategoryStatusActive, true)
	}
}

func parsePositiveInt(value string, fallback, max int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return fallback
	}
	if parsed > max {
		return max
	}
	return parsed
}
