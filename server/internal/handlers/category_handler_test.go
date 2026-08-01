package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"github.com/gin-gonic/gin"
)

var categoryHandlerTestScope = models.ProjectScope{
	OrganizationID: 1,
	ProjectID:      101,
}

func installCategoryHandlerTestAccess(
	c *gin.Context,
	role models.ProjectRole,
) {
	c.Set(projectRoleContextKey, string(role))
	c.Set(projectAccessContextKey, services.ProjectAccess{
		Scope: categoryHandlerTestScope,
		Role:  role,
	})
}

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
		PlatformRole: models.PlatformRolePlatformAdmin,
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
	if err := db.Model(&models.Category{}).
		Where("1 = 1").
		Updates(map[string]any{
			"organization_id": categoryHandlerTestScope.OrganizationID,
			"project_id":      categoryHandlerTestScope.ProjectID,
		}).Error; err != nil {
		t.Fatalf("scope categories: %v", err)
	}
	if err := db.Model(&categories[1]).Update("is_public", false).Error; err != nil {
		t.Fatalf("make category private: %v", err)
	}
	foreignCategory := models.Category{
		OrganizationID: categoryHandlerTestScope.OrganizationID,
		ProjectID:      categoryHandlerTestScope.ProjectID + 1,
		Name:           "Foreign Project",
		Slug:           "foreign-project",
		Type:           models.CategoryTypeGeneral,
		Status:         models.CategoryStatusActive,
		IsPublic:       true,
		CreatedBy:      admin.ID,
	}
	if err := db.Create(&foreignCategory).Error; err != nil {
		t.Fatalf("create foreign-project category: %v", err)
	}

	handler := NewCategoryHandler(db)
	router := gin.New()
	router.GET("/categories", func(c *gin.Context) {
		installCategoryHandlerTestAccess(
			c,
			models.ProjectRole(c.GetHeader("X-Test-Role")),
		)
		handler.List(c)
	})
	router.GET("/categories/:id", func(c *gin.Context) {
		installCategoryHandlerTestAccess(
			c,
			models.ProjectRole(c.GetHeader("X-Test-Role")),
		)
		handler.Get(c)
	})

	customerList := performCategoryRequest(t, router, http.MethodGet, "/categories", string(models.ProjectRoleRequester))
	if customerList.Code != http.StatusOK {
		t.Fatalf("customer list status = %d, body=%s", customerList.Code, customerList.Body.String())
	}
	var customerPayload struct {
		Data struct {
			Items      []categoryResponse `json:"items"`
			Total      int64              `json:"total"`
			Page       int                `json:"page"`
			PageSize   int                `json:"page_size"`
			TotalPages int64              `json:"total_pages"`
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
	if customerPayload.Data.Page != 1 ||
		customerPayload.Data.PageSize != 25 ||
		customerPayload.Data.TotalPages != 1 {
		t.Fatalf("default category page metadata = %+v", customerPayload.Data)
	}
	var closedCustomerPayload struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(
		customerList.Body.Bytes(),
		&closedCustomerPayload,
	); err != nil {
		t.Fatalf("decode closed category response: %v", err)
	}
	for _, unpublished := range []string{
		"organization_id",
		"project_id",
		"created_at",
		"updated_at",
		"deleted_at",
		"created_by",
		"updated_by",
		"creator",
		"updater",
		"parent",
		"children",
		"tickets",
		"metadata",
		"template",
		"allowed_roles",
		"restricted_roles",
		"auto_assign_user_id",
		"auto_assign_user",
	} {
		if _, exposed := closedCustomerPayload.Data.Items[0][unpublished]; exposed {
			t.Fatalf("category directory exposed unpublished field %q", unpublished)
		}
	}

	filter := url.QueryEscape(fmt.Sprintf(`{"ids":[%d]}`, categories[1].ID))
	adminList := performCategoryRequest(
		t,
		router,
		http.MethodGet,
		"/categories?filter="+filter,
		string(models.ProjectRoleAdmin),
	)
	if adminList.Code != http.StatusOK {
		t.Fatalf("admin filtered list status = %d, body=%s", adminList.Code, adminList.Body.String())
	}
	var adminPayload struct {
		Data struct {
			Items []categoryResponse `json:"items"`
			Total int64              `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(adminList.Body.Bytes(), &adminPayload); err != nil {
		t.Fatalf("decode admin list: %v", err)
	}
	if adminPayload.Data.Total != 1 || len(adminPayload.Data.Items) != 1 || adminPayload.Data.Items[0].ID != categories[1].ID {
		t.Fatalf("admin id filter returned %+v", adminPayload.Data.Items)
	}
	foreignFilter, err := json.Marshal(map[string]any{
		"ids": []uint{foreignCategory.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	foreignList := performCategoryRequest(
		t,
		router,
		http.MethodGet,
		"/categories?filter="+url.QueryEscape(string(foreignFilter)),
		string(models.ProjectRoleAdmin),
	)
	if foreignList.Code != http.StatusOK {
		t.Fatalf(
			"foreign category filter status=%d body=%s",
			foreignList.Code,
			foreignList.Body.String(),
		)
	}
	var foreignPayload struct {
		Data struct {
			Items []categoryResponse `json:"items"`
			Total int64              `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(
		foreignList.Body.Bytes(),
		&foreignPayload,
	); err != nil {
		t.Fatalf("decode foreign category response: %v", err)
	}
	if foreignPayload.Data.Total != 0 ||
		len(foreignPayload.Data.Items) != 0 {
		t.Fatalf(
			"foreign project category leaked: %+v",
			foreignPayload.Data,
		)
	}

	hiddenGet := performCategoryRequest(
		t,
		router,
		http.MethodGet,
		fmt.Sprintf("/categories/%d", categories[1].ID),
		string(models.ProjectRoleRequester),
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
		string(models.ProjectRoleAdmin),
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

func TestCategoryHandlerStrictPaginationAndStablePages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.Category{}); err != nil {
		t.Fatalf("migrate category schemas: %v", err)
	}
	creator := models.User{
		Username:     "category-page-admin",
		Email:        "category-page-admin@example.com",
		PasswordHash: "hashed",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&creator).Error; err != nil {
		t.Fatalf("create category page admin: %v", err)
	}
	categories := make([]models.Category, 0, 151)
	for index := 0; index < 151; index++ {
		categories = append(categories, models.Category{
			Name:      fmt.Sprintf("Stable Category %03d", index),
			Slug:      fmt.Sprintf("stable-category-%03d", index),
			Type:      models.CategoryTypeGeneral,
			Status:    models.CategoryStatusActive,
			IsPublic:  true,
			SortOrder: 1,
			CreatedBy: creator.ID,
		})
	}
	if err := db.CreateInBatches(&categories, 50).Error; err != nil {
		t.Fatalf("create category pages: %v", err)
	}
	if err := db.Model(&models.Category{}).
		Where("1 = 1").
		Updates(map[string]any{
			"organization_id": categoryHandlerTestScope.OrganizationID,
			"project_id":      categoryHandlerTestScope.ProjectID,
		}).Error; err != nil {
		t.Fatalf("scope category pages: %v", err)
	}

	handler := NewCategoryHandler(db)
	router := gin.New()
	router.GET("/categories", func(c *gin.Context) {
		installCategoryHandlerTestAccess(c, models.ProjectRoleAdmin)
		handler.List(c)
	})

	type categoryPagePayload struct {
		Data struct {
			Items      []categoryResponse `json:"items"`
			Total      int64              `json:"total"`
			Page       int                `json:"page"`
			PageSize   int                `json:"page_size"`
			TotalPages int64              `json:"total_pages"`
		} `json:"data"`
	}
	readPage := func(page int) categoryPagePayload {
		t.Helper()
		response := performCategoryRequest(
			t,
			router,
			http.MethodGet,
			"/categories?page="+strconv.Itoa(page)+
				"&page_size=100&sort_by=status&sort_order=asc",
			string(models.ProjectRoleAdmin),
		)
		if response.Code != http.StatusOK {
			t.Fatalf(
				"category page %d status=%d body=%s",
				page,
				response.Code,
				response.Body.String(),
			)
		}
		var payload categoryPagePayload
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode category page %d: %v", page, err)
		}
		return payload
	}
	first := readPage(1)
	second := readPage(2)
	if first.Data.Total != 151 ||
		first.Data.Page != 1 ||
		first.Data.PageSize != 100 ||
		first.Data.TotalPages != 2 ||
		len(first.Data.Items) != 100 ||
		second.Data.Total != 151 ||
		second.Data.Page != 2 ||
		second.Data.PageSize != 100 ||
		second.Data.TotalPages != 2 ||
		len(second.Data.Items) != 51 {
		t.Fatalf(
			"category page metadata first=%+v second=%+v",
			first.Data,
			second.Data,
		)
	}
	seen := make(map[uint]struct{}, 151)
	var previous uint
	for _, item := range append(first.Data.Items, second.Data.Items...) {
		if _, duplicate := seen[item.ID]; duplicate {
			t.Fatalf("category %d appeared on multiple pages", item.ID)
		}
		if previous != 0 && item.ID <= previous {
			t.Fatalf(
				"category ID tie-break is not ascending: %d then %d",
				previous,
				item.ID,
			)
		}
		seen[item.ID] = struct{}{}
		previous = item.ID
	}
	if len(seen) != 151 {
		t.Fatalf("unique categories=%d, want 151", len(seen))
	}
}

func TestCategoryHandlerRejectsInvalidListQueries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(&models.Category{}); err != nil {
		t.Fatalf("migrate categories: %v", err)
	}
	handler := NewCategoryHandler(db)
	router := gin.New()
	router.GET("/categories", func(c *gin.Context) {
		installCategoryHandlerTestAccess(c, models.ProjectRoleAdmin)
		handler.List(c)
	})

	ids := make([]uint, 101)
	for index := range ids {
		ids[index] = uint(index + 1)
	}
	tooManyIDs, err := json.Marshal(map[string]any{"ids": ids})
	if err != nil {
		t.Fatal(err)
	}
	controlSearch, err := json.Marshal(map[string]any{"search": "bad\nsearch"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		rawQuery string
	}{
		{name: "zero page", rawQuery: "page=0"},
		{name: "negative page", rawQuery: "page=-1"},
		{name: "empty page", rawQuery: "page="},
		{name: "duplicate page", rawQuery: "page=1&page=2"},
		{name: "zero page size", rawQuery: "page_size=0"},
		{name: "negative page size", rawQuery: "page_size=-1"},
		{name: "page size above maximum", rawQuery: "page_size=101"},
		{name: "offset overflow", rawQuery: "page=9223372036854775807&page_size=100"},
		{name: "unknown parameter", rawQuery: "unknown=true"},
		{name: "malformed encoding", rawQuery: "search=%zz"},
		{name: "empty search", rawQuery: "search="},
		{name: "padded search", rawQuery: "search=%20term"},
		{
			name:     "long search",
			rawQuery: "search=" + url.QueryEscape(strings.Repeat("界", 201)),
		},
		{name: "null filter", rawQuery: "filter=null"},
		{name: "array filter", rawQuery: "filter=%5B%5D"},
		{name: "trailing filter", rawQuery: "filter=%7B%7D%7B%7D"},
		{
			name:     "unknown filter field",
			rawQuery: "filter=" + url.QueryEscape(`{"secret":true}`),
		},
		{
			name:     "too many filter ids",
			rawQuery: "filter=" + url.QueryEscape(string(tooManyIDs)),
		},
		{
			name:     "zero filter id",
			rawQuery: "filter=" + url.QueryEscape(`{"ids":[0]}`),
		},
		{
			name:     "duplicate filter id",
			rawQuery: "filter=" + url.QueryEscape(`{"ids":[1,1]}`),
		},
		{
			name:     "zero parent id",
			rawQuery: "filter=" + url.QueryEscape(`{"parent_id":0}`),
		},
		{
			name:     "ambiguous filter search",
			rawQuery: "filter=" + url.QueryEscape(`{"q":"one","search":"two"}`),
		},
		{
			name:     "control filter search",
			rawQuery: "filter=" + url.QueryEscape(string(controlSearch)),
		},
		{
			name:     "invalid filter status",
			rawQuery: "filter=" + url.QueryEscape(`{"status":"pending"}`),
		},
		{
			name: "duplicate status sources",
			rawQuery: "status=active&filter=" +
				url.QueryEscape(`{"status":"active"}`),
		},
		{
			name: "duplicate search sources",
			rawQuery: "search=one&filter=" +
				url.QueryEscape(`{"search":"two"}`),
		},
		{name: "invalid sort field", rawQuery: "sort_by=created_by"},
		{name: "invalid sort order", rawQuery: "sort_order=sideways"},
		{
			name:     "invalid legacy sort",
			rawQuery: "sort=" + url.QueryEscape(`["name","SIDEWAYS"]`),
		},
		{
			name: "conflicting sort contracts",
			rawQuery: "sort=" + url.QueryEscape(`["name","ASC"]`) +
				"&sort_by=name",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/categories", nil)
			request.URL.RawQuery = test.rawQuery
			request.RequestURI = "/categories?" + test.rawQuery
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf(
					"status=%d, want 400, body=%s",
					response.Code,
					response.Body.String(),
				)
			}
		})
	}
}

func TestCategoryHandlerFailsClosedWithoutTrustedProjectAccess(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(&models.Category{}); err != nil {
		t.Fatalf("migrate scoped categories: %v", err)
	}
	handler := NewCategoryHandler(db)
	router := gin.New()
	router.GET("/categories", handler.List)
	router.GET("/categories/:id", handler.Get)

	for _, path := range []string{"/categories", "/categories/1"} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		router.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf(
				"unscoped category request %s status=%d body=%s",
				path,
				response.Code,
				response.Body.String(),
			)
		}
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
