package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"gongdan-system/internal/models"

	"github.com/gin-gonic/gin"
)

func TestCategoryHandlerVisibilityAndReferenceFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.Category{}, &models.Ticket{}); err != nil {
		t.Fatalf("migrate category schemas: %v", err)
	}

	admin := models.User{
		Username:     "category-admin",
		Email:        "category-admin@example.com",
		PasswordHash: "hashed",
		Role:         models.RoleAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatalf("create admin: %v", err)
	}

	categories := []models.Category{
		{
			Name: "Public Active", Slug: "public-active",
			Type: models.CategoryTypeGeneral, Status: models.CategoryStatusActive,
			IsPublic: true, CreatedBy: admin.ID,
		},
		{
			Name: "Private Active", Slug: "private-active",
			Type: models.CategoryTypeGeneral, Status: models.CategoryStatusActive,
			IsPublic: false, CreatedBy: admin.ID,
		},
		{
			Name: "Public Inactive", Slug: "public-inactive",
			Type: models.CategoryTypeGeneral, Status: models.CategoryStatusInactive,
			IsPublic: true, CreatedBy: admin.ID,
		},
	}
	if err := db.Create(&categories).Error; err != nil {
		t.Fatalf("create categories: %v", err)
	}
	if err := db.Model(&categories[1]).Update("is_public", false).Error; err != nil {
		t.Fatalf("make category private: %v", err)
	}

	handler := NewCategoryHandler(db)
	router := gin.New()
	router.GET("/categories", func(c *gin.Context) {
		c.Set("user_role", c.GetHeader("X-Test-Role"))
		handler.List(c)
	})
	router.GET("/categories/:id", func(c *gin.Context) {
		c.Set("user_role", c.GetHeader("X-Test-Role"))
		handler.Get(c)
	})

	customerList := performCategoryRequest(t, router, http.MethodGet, "/categories", string(models.RoleCustomer))
	if customerList.Code != http.StatusOK {
		t.Fatalf("customer list status = %d, body=%s", customerList.Code, customerList.Body.String())
	}
	var customerPayload struct {
		Data struct {
			Items []models.Category `json:"items"`
			Total int64             `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(customerList.Body.Bytes(), &customerPayload); err != nil {
		t.Fatalf("decode customer list: %v", err)
	}
	if customerPayload.Data.Total != 1 || len(customerPayload.Data.Items) != 1 {
		t.Fatalf("customer category visibility = total %d items %d", customerPayload.Data.Total, len(customerPayload.Data.Items))
	}
	if customerPayload.Data.Items[0].ID != categories[0].ID {
		t.Fatalf("customer saw category %d, want %d", customerPayload.Data.Items[0].ID, categories[0].ID)
	}

	filter := url.QueryEscape(fmt.Sprintf(`{"ids":[%d]}`, categories[1].ID))
	adminList := performCategoryRequest(
		t,
		router,
		http.MethodGet,
		"/categories?filter="+filter,
		string(models.RoleAdmin),
	)
	if adminList.Code != http.StatusOK {
		t.Fatalf("admin filtered list status = %d, body=%s", adminList.Code, adminList.Body.String())
	}
	var adminPayload struct {
		Data struct {
			Items []models.Category `json:"items"`
			Total int64             `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(adminList.Body.Bytes(), &adminPayload); err != nil {
		t.Fatalf("decode admin list: %v", err)
	}
	if adminPayload.Data.Total != 1 || len(adminPayload.Data.Items) != 1 || adminPayload.Data.Items[0].ID != categories[1].ID {
		t.Fatalf("admin id filter returned %+v", adminPayload.Data.Items)
	}

	hiddenGet := performCategoryRequest(
		t,
		router,
		http.MethodGet,
		fmt.Sprintf("/categories/%d", categories[1].ID),
		string(models.RoleCustomer),
	)
	if hiddenGet.Code != http.StatusNotFound {
		t.Fatalf("hidden category status = %d, want %d", hiddenGet.Code, http.StatusNotFound)
	}
	var hiddenPayload struct {
		Msg string `json:"msg"`
	}
	if err := json.Unmarshal(hiddenGet.Body.Bytes(), &hiddenPayload); err != nil {
		t.Fatalf("decode hidden category response: %v", err)
	}
	if hiddenPayload.Msg != "未找到分类" {
		t.Fatalf("hidden category message = %q", hiddenPayload.Msg)
	}

	invalidFilter := performCategoryRequest(
		t,
		router,
		http.MethodGet,
		"/categories?filter=%7B",
		string(models.RoleAdmin),
	)
	if invalidFilter.Code != http.StatusBadRequest {
		t.Fatalf("invalid category filter status = %d, body=%s", invalidFilter.Code, invalidFilter.Body.String())
	}
	var invalidFilterPayload struct {
		Msg string `json:"msg"`
	}
	if err := json.Unmarshal(invalidFilter.Body.Bytes(), &invalidFilterPayload); err != nil {
		t.Fatalf("decode invalid category filter response: %v", err)
	}
	if invalidFilterPayload.Msg != "分类筛选条件无效" {
		t.Fatalf("invalid category filter message = %q", invalidFilterPayload.Msg)
	}
}

func performCategoryRequest(
	t *testing.T,
	router http.Handler,
	method string,
	target string,
	role string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, nil)
	request.Header.Set("X-Test-Role", role)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}
