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

func TestAssigneeHandlerReturnsOnlyActiveAssignableUsers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.ProjectMembership{},
	); err != nil {
		t.Fatalf("migrate users: %v", err)
	}

	users := []models.User{
		{
			Username: "active-agent", Email: "active-agent@example.com",
			PasswordHash: "hashed", PlatformRole: models.PlatformRoleMember, Status: models.UserStatusActive,
		},
		{
			Username: "active-customer", Email: "active-customer@example.com",
			PasswordHash: "hashed", PlatformRole: models.PlatformRoleMember, Status: models.UserStatusActive,
		},
		{
			Username: "inactive-agent", Email: "inactive-agent@example.com",
			PasswordHash: "hashed", PlatformRole: models.PlatformRoleMember, Status: models.UserStatusInactive,
		},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("create users: %v", err)
	}
	organization := models.Organization{
		Slug: "assignee-test", Name: "Assignee Test",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "OPS", Name: "Operations",
		Status: models.BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	project := models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            "ASSIGNEE", Name: "Assignee",
		Status: models.ProjectStatusActive,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	memberships := []models.ProjectMembership{
		{
			ProjectID: project.ID, UserID: users[0].ID,
			Role: models.ProjectRoleAgent, IsActive: true,
		},
		{
			ProjectID: project.ID, UserID: users[1].ID,
			Role: models.ProjectRoleRequester, IsActive: true,
		},
		{
			ProjectID: project.ID, UserID: users[2].ID,
			Role: models.ProjectRoleAgent, IsActive: true,
		},
	}
	if err := db.Create(&memberships).Error; err != nil {
		t.Fatal(err)
	}

	handler := NewAssigneeHandler(db)
	router := gin.New()
	bindProject := func(c *gin.Context) {
		role := models.ProjectRole(c.GetHeader("X-Test-Role"))
		c.Set(projectRoleContextKey, string(role))
		c.Set(projectAccessContextKey, services.ProjectAccess{
			Project: project,
			Role:    role,
			Scope:   project.Scope(),
		})
	}
	router.GET("/assignees", func(c *gin.Context) {
		bindProject(c)
		handler.List(c)
	})
	router.GET("/assignees/:id", func(c *gin.Context) {
		bindProject(c)
		handler.Get(c)
	})

	adminRequest := httptest.NewRequest(http.MethodGet, "/assignees", nil)
	adminRequest.Header.Set(
		"X-Test-Role",
		string(models.ProjectRoleAdmin),
	)
	adminResponse := httptest.NewRecorder()
	router.ServeHTTP(adminResponse, adminRequest)
	if adminResponse.Code != http.StatusOK {
		t.Fatalf("admin status = %d, body=%s", adminResponse.Code, adminResponse.Body.String())
	}
	var payload struct {
		Data struct {
			Items      []map[string]any `json:"items"`
			Total      int64            `json:"total"`
			Page       int              `json:"page"`
			PageSize   int              `json:"page_size"`
			TotalPages int64            `json:"total_pages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(adminResponse.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode admin response: %v", err)
	}
	if payload.Data.Total != 1 || len(payload.Data.Items) != 1 {
		t.Fatalf("assignable users = total %d items %d", payload.Data.Total, len(payload.Data.Items))
	}
	if payload.Data.Items[0]["username"] != "active-agent" {
		t.Fatalf("assignable user = %v", payload.Data.Items[0]["username"])
	}
	if _, exposed := payload.Data.Items[0]["email"]; exposed {
		t.Fatal("assignee directory must not expose email")
	}
	for _, unpublished := range []string{
		"password_hash",
		"platform_role",
		"status",
		"phone",
		"department",
		"job_title",
		"metadata",
		"project_id",
		"organization_id",
	} {
		if _, exposed := payload.Data.Items[0][unpublished]; exposed {
			t.Fatalf("assignee directory exposed field %q", unpublished)
		}
	}
	if payload.Data.Page != 1 ||
		payload.Data.PageSize != 25 ||
		payload.Data.TotalPages != 1 {
		t.Fatalf("assignee page metadata = %+v", payload.Data)
	}

	customerRequest := httptest.NewRequest(http.MethodGet, "/assignees", nil)
	customerRequest.Header.Set(
		"X-Test-Role",
		string(models.ProjectRoleRequester),
	)
	customerResponse := httptest.NewRecorder()
	router.ServeHTTP(customerResponse, customerRequest)
	if customerResponse.Code != http.StatusOK {
		t.Fatalf("customer status = %d", customerResponse.Code)
	}
	var customerPayload struct {
		Data struct {
			Items      []assigneeResponse `json:"items"`
			Total      int64              `json:"total"`
			Page       int                `json:"page"`
			PageSize   int                `json:"page_size"`
			TotalPages int64              `json:"total_pages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(customerResponse.Body.Bytes(), &customerPayload); err != nil {
		t.Fatalf("decode customer response: %v", err)
	}
	if customerPayload.Data.Total != 0 || len(customerPayload.Data.Items) != 0 {
		t.Fatalf("customer assignees = total %d items %d", customerPayload.Data.Total, len(customerPayload.Data.Items))
	}
	if customerPayload.Data.Page != 1 ||
		customerPayload.Data.PageSize != 25 ||
		customerPayload.Data.TotalPages != 0 {
		t.Fatalf("customer page metadata = %+v", customerPayload.Data)
	}

	invalidFilterRequest := httptest.NewRequest(http.MethodGet, "/assignees?filter=%7B", nil)
	invalidFilterRequest.Header.Set(
		"X-Test-Role",
		string(models.ProjectRoleAdmin),
	)
	invalidFilterResponse := httptest.NewRecorder()
	router.ServeHTTP(invalidFilterResponse, invalidFilterRequest)
	if invalidFilterResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid assignee filter status = %d, body=%s", invalidFilterResponse.Code, invalidFilterResponse.Body.String())
	}
	var invalidFilterPayload struct {
		Msg string `json:"msg"`
	}
	if err := json.Unmarshal(invalidFilterResponse.Body.Bytes(), &invalidFilterPayload); err != nil {
		t.Fatalf("decode invalid assignee filter response: %v", err)
	}
	if invalidFilterPayload.Msg != "处理人筛选条件无效" {
		t.Fatalf("invalid assignee filter message = %q", invalidFilterPayload.Msg)
	}

	invalidIDRequest := httptest.NewRequest(http.MethodGet, "/assignees/not-a-number", nil)
	invalidIDRequest.Header.Set(
		"X-Test-Role",
		string(models.ProjectRoleAdmin),
	)
	invalidIDResponse := httptest.NewRecorder()
	router.ServeHTTP(invalidIDResponse, invalidIDRequest)
	if invalidIDResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid assignee id status = %d, body=%s", invalidIDResponse.Code, invalidIDResponse.Body.String())
	}
	var invalidIDPayload struct {
		Msg string `json:"msg"`
	}
	if err := json.Unmarshal(invalidIDResponse.Body.Bytes(), &invalidIDPayload); err != nil {
		t.Fatalf("decode invalid assignee id response: %v", err)
	}
	if invalidIDPayload.Msg != "处理人 ID 无效" {
		t.Fatalf("invalid assignee id message = %q", invalidIDPayload.Msg)
	}
}

func TestAssigneeHandlerStrictPaginationAndStablePages(t *testing.T) {
	router, users := newAssigneeDirectoryTestRouter(t, 151)

	type assigneePagePayload struct {
		Data struct {
			Items      []assigneeResponse `json:"items"`
			Total      int64              `json:"total"`
			Page       int                `json:"page"`
			PageSize   int                `json:"page_size"`
			TotalPages int64              `json:"total_pages"`
		} `json:"data"`
	}
	readPage := func(page int) assigneePagePayload {
		t.Helper()
		request := httptest.NewRequest(
			http.MethodGet,
			"/assignees?page="+strconv.Itoa(page)+
				"&page_size=100&sort_by=role&sort_order=asc",
			nil,
		)
		request.Header.Set("X-Test-Role", string(models.ProjectRoleAdmin))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf(
				"assignee page %d status=%d body=%s",
				page,
				response.Code,
				response.Body.String(),
			)
		}
		var payload assigneePagePayload
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode assignee page %d: %v", page, err)
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
			"assignee page metadata first=%+v second=%+v",
			first.Data,
			second.Data,
		)
	}
	seen := make(map[uint]struct{}, len(users))
	var previous uint
	for _, item := range append(first.Data.Items, second.Data.Items...) {
		if _, duplicate := seen[item.ID]; duplicate {
			t.Fatalf("assignee %d appeared on multiple pages", item.ID)
		}
		if previous != 0 && item.ID <= previous {
			t.Fatalf(
				"assignee ID tie-break is not ascending: %d then %d",
				previous,
				item.ID,
			)
		}
		seen[item.ID] = struct{}{}
		previous = item.ID
	}
	if len(seen) != len(users) {
		t.Fatalf("unique assignees=%d, want %d", len(seen), len(users))
	}
}

func TestAssigneeHandlerRejectsInvalidListQueries(t *testing.T) {
	router, _ := newAssigneeDirectoryTestRouter(t, 1)
	ids := make([]uint, 101)
	for index := range ids {
		ids[index] = uint(index + 1)
	}
	tooManyIDs, err := json.Marshal(map[string]any{"ids": ids})
	if err != nil {
		t.Fatal(err)
	}
	controlSearch, err := json.Marshal(map[string]any{"search": "bad\tsearch"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		rawQuery string
		role     models.ProjectRole
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
		{name: "padded search", rawQuery: "search=term%20"},
		{
			name:     "long search",
			rawQuery: "search=" + url.QueryEscape(strings.Repeat("界", 201)),
		},
		{name: "null filter", rawQuery: "filter=null"},
		{name: "array filter", rawQuery: "filter=%5B%5D"},
		{name: "trailing filter", rawQuery: "filter=%7B%7D%7B%7D"},
		{
			name:     "unknown filter field",
			rawQuery: "filter=" + url.QueryEscape(`{"email":"hidden"}`),
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
			name:     "ambiguous filter search",
			rawQuery: "filter=" + url.QueryEscape(`{"q":"one","search":"two"}`),
		},
		{
			name:     "control filter search",
			rawQuery: "filter=" + url.QueryEscape(string(controlSearch)),
		},
		{
			name: "duplicate search sources",
			rawQuery: "search=one&filter=" +
				url.QueryEscape(`{"search":"two"}`),
		},
		{name: "invalid sort field", rawQuery: "sort_by=email"},
		{name: "invalid sort order", rawQuery: "sort_order=sideways"},
		{
			name:     "invalid legacy sort",
			rawQuery: "sort=" + url.QueryEscape(`["username","SIDEWAYS"]`),
		},
		{
			name: "conflicting sort contracts",
			rawQuery: "sort=" + url.QueryEscape(`["username","ASC"]`) +
				"&sort_by=username",
		},
		{
			name:     "unauthorized role still validates query",
			rawQuery: "page=0",
			role:     models.ProjectRoleRequester,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/assignees", nil)
			request.URL.RawQuery = test.rawQuery
			request.RequestURI = "/assignees?" + test.rawQuery
			role := test.role
			if role == "" {
				role = models.ProjectRoleAdmin
			}
			request.Header.Set("X-Test-Role", string(role))
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

func newAssigneeDirectoryTestRouter(
	t *testing.T,
	count int,
) (*gin.Engine, []models.User) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.ProjectMembership{},
	); err != nil {
		t.Fatalf("migrate assignee directory schemas: %v", err)
	}
	organization := models.Organization{
		Slug:   fmt.Sprintf("assignee-page-%d", count),
		Name:   "Assignee Page",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatalf("create assignee organization: %v", err)
	}
	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "ASSIGNEE-PAGE",
		Name:           "Assignee Page",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatalf("create assignee business unit: %v", err)
	}
	project := models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            "ASSIGNEE-PAGE",
		Name:           "Assignee Page",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatalf("create assignee project: %v", err)
	}
	users := make([]models.User, 0, count)
	for index := 0; index < count; index++ {
		users = append(users, models.User{
			Username: fmt.Sprintf("stable-assignee-%03d", index),
			Email: fmt.Sprintf(
				"stable-assignee-%03d@example.test",
				index,
			),
			PasswordHash: "hashed",
			PlatformRole: models.PlatformRoleMember,
			Status:       models.UserStatusActive,
		})
	}
	if len(users) > 0 {
		if err := db.CreateInBatches(&users, 50).Error; err != nil {
			t.Fatalf("create assignee page users: %v", err)
		}
		memberships := make([]models.ProjectMembership, 0, len(users))
		for _, user := range users {
			memberships = append(memberships, models.ProjectMembership{
				ProjectID: project.ID,
				UserID:    user.ID,
				Role:      models.ProjectRoleAgent,
				IsActive:  true,
			})
		}
		if err := db.CreateInBatches(&memberships, 50).Error; err != nil {
			t.Fatalf("create assignee memberships: %v", err)
		}
	}

	handler := NewAssigneeHandler(db)
	router := gin.New()
	bindProject := func(c *gin.Context) {
		role := models.ProjectRole(c.GetHeader("X-Test-Role"))
		c.Set(projectRoleContextKey, string(role))
		c.Set(projectAccessContextKey, services.ProjectAccess{
			Project: project,
			Role:    role,
			Scope:   project.Scope(),
		})
	}
	router.GET("/assignees", func(c *gin.Context) {
		bindProject(c)
		handler.List(c)
	})
	return router, users
}
