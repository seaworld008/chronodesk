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
}

func (stub *stubWorkbenchDashboardQuery) Dashboard(
	_ context.Context,
	input services.WorkbenchDashboardQuery,
) (*services.WorkbenchDashboard, error) {
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
		})
	}
}
