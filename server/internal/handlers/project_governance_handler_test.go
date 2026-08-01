package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPlatformProjectListStrictlyRejectsInvalidGovernanceQueries(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	for _, rawQuery := range []string{
		"page=0",
		"page=-1",
		"page_size=0",
		"page_size=-1",
		"page_size=101",
		"status=unknown",
		"business_unit_public_id=1",
		"order_by=organization_id",
		"order=sideways",
		"unknown=value",
	} {
		t.Run(rawQuery, func(t *testing.T) {
			router := gin.New()
			router.GET(
				"/api/platform/projects",
				NewProjectHandler(nil).ListPlatform,
			)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodGet,
				"/api/platform/projects?"+rawQuery,
				nil,
			)
			router.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf(
					"status = %d, want 400; body=%s",
					response.Code,
					response.Body.String(),
				)
			}
		})
	}
}

func TestPlatformProjectListDefaultsReachServiceBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET(
		"/api/platform/projects",
		NewProjectHandler(nil).ListPlatform,
	)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/platform/projects",
		nil,
	)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError ||
		!strings.Contains(response.Body.String(), "项目服务不可用") {
		t.Fatalf(
			"default query status/body = %d %s",
			response.Code,
			response.Body.String(),
		)
	}
}

func TestProjectCreationContextStrictlyRejectsInvalidRemoteSearch(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	for _, rawQuery := range []string{
		"page=0",
		"page=-1",
		"page_size=0",
		"page_size=101",
		"search=" + strings.Repeat("x", 101),
		"business_unit_page=0",
		"business_unit_page=-1",
		"business_unit_page_size=0",
		"business_unit_page_size=-1",
		"business_unit_page_size=101",
		"business_unit_search=" + strings.Repeat("x", 101),
		"status=active",
		"unknown=value",
	} {
		t.Run(rawQuery, func(t *testing.T) {
			router := gin.New()
			router.GET(
				"/api/platform/project-creation-context",
				NewProjectHandler(nil).CreationContext,
			)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodGet,
				"/api/platform/project-creation-context?"+rawQuery,
				nil,
			)
			router.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf(
					"status = %d, want 400; body=%s",
					response.Code,
					response.Body.String(),
				)
			}
		})
	}
}

func TestPlatformBusinessUnitFilterStrictlyRejectsInvalidRemoteSearch(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	for _, rawQuery := range []string{
		"page=0",
		"page=-1",
		"page_size=0",
		"page_size=101",
		"search=" + strings.Repeat("x", 101),
		"status=active",
		"unknown=value",
	} {
		t.Run(rawQuery, func(t *testing.T) {
			router := gin.New()
			router.GET(
				"/api/platform/project-business-units",
				NewProjectHandler(nil).ListPlatformBusinessUnits,
			)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodGet,
				"/api/platform/project-business-units?"+rawQuery,
				nil,
			)
			router.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf(
					"status = %d, want 400; body=%s",
					response.Code,
					response.Body.String(),
				)
			}
		})
	}
}

func TestPlatformProjectCreateAcceptsOnlyPublicScopeAndExplicitAdmins(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	valid := `{
		"business_unit_public_id":"019fb344-fa16-7e13-9c5b-08eb95478098",
		"key":"NEW",
		"name":"New",
		"description":"",
		"initial_project_admin_user_ids":[7],
		"default_queue_key":"default",
		"default_queue_name":"默认队列"
	}`
	for _, test := range []struct {
		name       string
		payload    string
		wantStatus int
	}{
		{
			name:       "public scope reaches service boundary",
			payload:    valid,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name: "numeric organization rejected",
			payload: strings.Replace(
				valid,
				`"key":"NEW"`,
				`"organization_id":1,"key":"NEW"`,
				1,
			),
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "numeric business unit rejected",
			payload: strings.Replace(
				valid,
				`"key":"NEW"`,
				`"business_unit_id":1,"key":"NEW"`,
				1,
			),
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "explicit project administrator required",
			payload: strings.Replace(
				valid,
				`"initial_project_admin_user_ids":[7]`,
				`"initial_project_admin_user_ids":[]`,
				1,
			),
			wantStatus: http.StatusBadRequest,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.POST(
				"/api/platform/projects",
				NewProjectHandler(nil).Create,
			)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/platform/projects",
				strings.NewReader(test.payload),
			)
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf(
					"status = %d, want %d; body=%s",
					response.Code,
					test.wantStatus,
					response.Body.String(),
				)
			}
		})
	}
}
