package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

type captureAdminAuditService struct {
	filter services.AdminAuditFilter
	detail *services.AdminAuditDetail
}

func (service *captureAdminAuditService) Explore(
	_ context.Context,
	filter *services.AdminAuditFilter,
) (*services.AdminAuditPage, error) {
	if filter != nil {
		service.filter = *filter
	}
	return &services.AdminAuditPage{
		Items: []*services.AdminAuditListItem{},
		Page:  filter.Page,
		Limit: filter.Limit,
	}, nil
}

func (service *captureAdminAuditService) GetDetail(
	_ context.Context,
	_ uint,
) (*services.AdminAuditDetail, error) {
	return service.detail, nil
}

func TestAdminAuditHandlerBindsAllPublishedQueryParameters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &captureAdminAuditService{}
	handler := NewAdminAuditHandler(service)
	router := gin.New()
	router.GET("/audit-logs", handler.GetAuditLogs)

	request := httptest.NewRequest(
		http.MethodGet,
		"/audit-logs?user_id=42"+
			"&platform_role=security_auditor"+
			"&actor=audit-admin"+
			"&action=platform.user.update"+
			"&method=post"+
			"&path_prefix=%2Fapi%2Fplatform"+
			"&status=403"+
			"&result=error"+
			"&keyword=denied"+
			"&start_time=2026-07-01"+
			"&end_time=2026-07-30T12%3A30%3A00Z"+
			"&page=2"+
			"&limit=50",
		nil,
	)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}

	filter := service.filter
	if filter.UserID == nil || *filter.UserID != 42 {
		t.Errorf("user_id = %v", filter.UserID)
	}
	if filter.PlatformRole != models.PlatformRoleSecurityAuditor {
		t.Errorf("platform_role = %q", filter.PlatformRole)
	}
	if filter.Actor != "audit-admin" ||
		filter.Action != "platform.user.update" ||
		filter.Result != "error" {
		t.Errorf("structured filters = %+v", filter)
	}
	if filter.Method != "POST" ||
		filter.Path != "/api/platform" ||
		filter.Keyword != "denied" {
		t.Errorf("text filters = %+v", filter)
	}
	if filter.Status == nil || *filter.Status != http.StatusForbidden {
		t.Errorf("status = %v", filter.Status)
	}
	if filter.StartTime == nil ||
		filter.StartTime.Format("2006-01-02") != "2026-07-01" {
		t.Errorf("start_time = %v", filter.StartTime)
	}
	wantEnd := time.Date(2026, 7, 30, 12, 30, 0, 0, time.UTC)
	if filter.EndTime == nil || !filter.EndTime.Equal(wantEnd) {
		t.Errorf("end_time = %v", filter.EndTime)
	}
	if filter.Page != 2 || filter.Limit != 50 {
		t.Errorf("pagination = page %d limit %d", filter.Page, filter.Limit)
	}
}

func TestAdminAuditHandlerUsesStrictPaginationDefaultsAndValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name  string
		query string
		want  int
	}{
		{name: "defaults", query: "", want: http.StatusOK},
		{name: "zero page", query: "?page=0", want: http.StatusBadRequest},
		{name: "oversized limit", query: "?limit=101", want: http.StatusBadRequest},
		{name: "bad status", query: "?status=ok", want: http.StatusBadRequest},
		{name: "bad date", query: "?start_time=yesterday", want: http.StatusBadRequest},
		{name: "unknown filter", query: "?role=admin", want: http.StatusBadRequest},
		{name: "page cursor conflict", query: "?page=1&cursor=opaque", want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &captureAdminAuditService{}
			router := gin.New()
			router.GET(
				"/audit-logs",
				NewAdminAuditHandler(service).GetAuditLogs,
			)
			response := httptest.NewRecorder()
			router.ServeHTTP(
				response,
				httptest.NewRequest(
					http.MethodGet,
					"/audit-logs"+test.query,
					nil,
				),
			)
			if response.Code != test.want {
				t.Fatalf(
					"status = %d, want %d; body=%s",
					response.Code,
					test.want,
					response.Body.String(),
				)
			}
			if test.name == "defaults" &&
				(service.filter.Page != 1 ||
					service.filter.Limit != services.DefaultAdminAuditLimit) {
				t.Fatalf("defaults = %+v", service.filter)
			}
		})
	}
}

func TestAdminAuditHandlerRejectsUnknownPlatformRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &captureAdminAuditService{}
	handler := NewAdminAuditHandler(service)
	router := gin.New()
	router.GET("/audit-logs", handler.GetAuditLogs)

	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodGet,
			"/audit-logs?platform_role=administrator",
			nil,
		),
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf(
			"status = %d, want %d; body=%s",
			response.Code,
			http.StatusBadRequest,
			response.Body.String(),
		)
	}
	if service.filter.PlatformRole != "" {
		t.Fatalf("invalid platform role reached service: %q", service.filter.PlatformRole)
	}
}
