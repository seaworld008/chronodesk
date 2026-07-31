package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

type stubWorkbenchDashboardQuery struct {
	input  services.WorkbenchDashboardQuery
	result *services.WorkbenchDashboard
	err    error
	calls  int
}

func (stub *stubWorkbenchDashboardQuery) Dashboard(
	_ context.Context,
	input services.WorkbenchDashboardQuery,
) (*services.WorkbenchDashboard, error) {
	stub.calls++
	stub.input = input
	return stub.result, stub.err
}

func TestWorkbenchDashboardHandlerPreservesRepeatedProjectKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &stubWorkbenchDashboardQuery{result: &services.WorkbenchDashboard{}}
	handler := NewWorkbenchDashboardHandler(stub)
	router := gin.New()
	router.GET("/api/workbench/dashboard", func(c *gin.Context) {
		c.Set("user_id", uint(7))
		handler.Get(c)
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/workbench/dashboard?project_keys=OPS&project_keys=FIN&days=7",
		nil,
	)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if !stub.input.HasFilter ||
		stub.input.Days != 7 ||
		len(stub.input.ProjectKeys) != 2 ||
		stub.input.ProjectKeys[0] != "OPS" ||
		stub.input.ProjectKeys[1] != "FIN" {
		t.Fatalf("input = %+v", stub.input)
	}
}

func TestWorkbenchDashboardHandlerDefaultsToAllProjectsAndThirtyDays(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	stub := &stubWorkbenchDashboardQuery{
		result: &services.WorkbenchDashboard{},
	}
	handler := NewWorkbenchDashboardHandler(stub)
	router := gin.New()
	router.GET("/api/workbench/dashboard", func(c *gin.Context) {
		c.Set("user_id", uint(7))
		handler.Get(c)
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodGet,
			"/api/workbench/dashboard",
			nil,
		),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if stub.calls != 1 ||
		stub.input.Days != 30 ||
		stub.input.HasFilter ||
		len(stub.input.ProjectKeys) != 0 {
		t.Fatalf("default dashboard input = %+v calls=%d", stub.input, stub.calls)
	}
}

func TestWorkbenchDashboardHandlerMapsInvalidAndUnauthorizedQueries(t *testing.T) {
	for _, test := range []struct {
		name       string
		url        string
		serviceErr error
		wantStatus int
	}{
		{
			name:       "empty filter",
			url:        "/api/workbench/dashboard?project_keys=",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "whitespace is not canonicalized",
			url:        "/api/workbench/dashboard?project_keys=%20OPS",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "duplicate project key",
			url:        "/api/workbench/dashboard?project_keys=OPS&project_keys=OPS",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid project key",
			url:        "/api/workbench/dashboard?project_keys=ops",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unknown query parameter",
			url:        "/api/workbench/dashboard?unknown=OPS",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "malformed query encoding",
			url:        "/api/workbench/dashboard?days=30;unknown=1",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "duplicate days",
			url:        "/api/workbench/dashboard?days=7&days=30",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "noncanonical days",
			url:        "/api/workbench/dashboard?days=030",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unsupported 101 days",
			url:        "/api/workbench/dashboard?days=101",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unauthorized injection",
			url:        "/api/workbench/dashboard?project_keys=OTHER",
			serviceErr: services.ErrCrossProjectWorkbenchAccessDenied,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "invalid days",
			url:        "/api/workbench/dashboard?days=all",
			wantStatus: http.StatusBadRequest,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			stub := &stubWorkbenchDashboardQuery{
				result: &services.WorkbenchDashboard{},
				err:    test.serviceErr,
			}
			handler := NewWorkbenchDashboardHandler(stub)
			router := gin.New()
			router.GET("/api/workbench/dashboard", func(c *gin.Context) {
				c.Set("user_id", uint(7))
				handler.Get(c)
			})
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, test.url, nil)
			router.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf(
					"status = %d want %d body=%s error=%v",
					response.Code, test.wantStatus, response.Body.String(),
					errors.Unwrap(test.serviceErr),
				)
			}
			if test.wantStatus == http.StatusBadRequest && stub.calls != 0 {
				t.Fatalf("invalid query reached service %d times", stub.calls)
			}
		})
	}
}
