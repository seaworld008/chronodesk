package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

type cleanupServiceStub struct {
	executeErr    error
	executeCalled bool
	executeAll    bool
}

func (s *cleanupServiceStub) GetCleanupConfig(context.Context) (*models.CleanupConfig, error) {
	return models.GetDefaultCleanupConfig(), nil
}

func (s *cleanupServiceStub) SetCleanupConfig(
	context.Context,
	*models.CleanupConfig,
	uint,
) error {
	return nil
}

func (s *cleanupServiceStub) ExecuteCleanup(
	_ context.Context,
	_, _ string,
	_ *uint,
) error {
	s.executeCalled = true
	return s.executeErr
}

func (s *cleanupServiceStub) ExecuteAllCleanupTasks(
	_ context.Context,
	_ string,
	_ *uint,
) error {
	s.executeAll = true
	return s.executeErr
}

func (s *cleanupServiceStub) ListCleanupLogPage(
	context.Context,
	string,
	services.DirectoryPageRequest,
) (*services.DirectoryPage[*models.CleanupLogResponse], error) {
	return &services.DirectoryPage[*models.CleanupLogResponse]{
		Items: make([]*models.CleanupLogResponse, 0),
		Page:  1,
	}, nil
}

func (s *cleanupServiceStub) GetCleanupStats(
	context.Context,
) (*services.CleanupStatsResponse, error) {
	return &services.CleanupStatsResponse{}, nil
}

func TestSystemHandlerInvalidRequestMessageIsChinese(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &SystemHandler{}
	router := gin.New()
	router.PUT("/cleanup/config", handler.UpdateCleanupConfig)

	request := httptest.NewRequest(http.MethodPut, "/cleanup/config", bytes.NewBufferString(`{"enabled":`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Error != "invalid_request" {
		t.Fatalf("error code = %q", payload.Error)
	}
	if payload.Message != "请求体格式无效" {
		t.Fatalf("message = %q", payload.Message)
	}
}

func TestSystemCleanupLogsRejectInvalidDirectoryQueries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &SystemHandler{cleanupSvc: &cleanupServiceStub{}}
	router := gin.New()
	router.GET("/cleanup/logs", handler.GetCleanupLogs)
	for _, query := range []string{
		"limit=20",
		"page=0",
		"page_size=101",
		"sort_by=error_message",
		"sort_order=ASC",
		"task_type=",
		"task_type=login_history&task_type=other",
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(
			response,
			httptest.NewRequest(
				http.MethodGet,
				"/cleanup/logs?"+query,
				nil,
			),
		)
		if response.Code != http.StatusBadRequest {
			t.Fatalf(
				"query %q status = %d, body=%s",
				query,
				response.Code,
				response.Body.String(),
			)
		}
	}
}

func TestSystemHandlerRegistersCleanupRoutesOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := &SystemHandler{}
	handler.RegisterRoutes(router.Group("/api/platform"))

	paths := make([]string, 0)
	for _, route := range router.Routes() {
		paths = append(paths, route.Method+" "+route.Path)
	}
	for _, expected := range []string{
		"GET /api/platform/system/cleanup/config",
		"PUT /api/platform/system/cleanup/config",
		"POST /api/platform/system/cleanup/execute",
		"POST /api/platform/system/cleanup/execute-all",
		"GET /api/platform/system/cleanup/logs",
		"GET /api/platform/system/cleanup/stats",
	} {
		if !slices.Contains(paths, expected) {
			t.Errorf("missing route %q in %v", expected, paths)
		}
	}
	for _, route := range paths {
		if route == "GET /api/platform/system/configs" ||
			route == "POST /api/platform/system/configs" ||
			route == "GET /api/platform/system/configs/:key" ||
			route == "PUT /api/platform/system/configs/:key" ||
			route == "DELETE /api/platform/system/configs/:key" {
			t.Errorf("duplicate config route remains registered: %s", route)
		}
	}
}

func TestSystemHandlerExecutesCleanupBeforeReportingCompletion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name        string
		path        string
		body        string
		executeAll  bool
		serviceErr  error
		wantStatus  int
		wantMessage string
	}{
		{
			name:        "single task completes",
			path:        "/cleanup/execute",
			body:        `{"task_type":"login_history"}`,
			wantStatus:  http.StatusOK,
			wantMessage: "清理任务已完成",
		},
		{
			name:        "all tasks complete",
			path:        "/cleanup/execute-all",
			body:        `{}`,
			executeAll:  true,
			wantStatus:  http.StatusOK,
			wantMessage: "全部清理任务已完成",
		},
		{
			name:        "failure is not reported as started",
			path:        "/cleanup/execute",
			body:        `{"task_type":"login_history"}`,
			serviceErr:  errors.New("database unavailable"),
			wantStatus:  http.StatusInternalServerError,
			wantMessage: "执行清理任务失败",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			stub := &cleanupServiceStub{executeErr: test.serviceErr}
			handler := &SystemHandler{cleanupSvc: stub}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set("user_id", uint(42))
			})
			router.POST("/cleanup/execute", handler.ExecuteCleanup)
			router.POST("/cleanup/execute-all", handler.ExecuteAllCleanup)

			request := httptest.NewRequest(
				http.MethodPost,
				test.path,
				bytes.NewBufferString(test.body),
			)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), test.wantMessage) {
				t.Fatalf("response does not contain %q: %s", test.wantMessage, response.Body.String())
			}
			if test.executeAll {
				if !stub.executeAll {
					t.Fatal("all cleanup operation returned before service execution")
				}
			} else if !stub.executeCalled {
				t.Fatal("cleanup operation returned before service execution")
			}
		})
	}
}
