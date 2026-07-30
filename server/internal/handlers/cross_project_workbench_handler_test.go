package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

type crossProjectWorkbenchQueryStub struct {
	input  services.CrossProjectWorkbenchQuery
	result *services.CrossProjectWorkbenchPage
	err    error
	calls  int
}

func (stub *crossProjectWorkbenchQueryStub) ListTickets(
	_ context.Context,
	input services.CrossProjectWorkbenchQuery,
) (*services.CrossProjectWorkbenchPage, error) {
	stub.calls++
	stub.input = input
	return stub.result, stub.err
}

func TestCrossProjectWorkbenchHandlerIgnoresPlatformRoleByContract(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	stub := &crossProjectWorkbenchQueryStub{
		result: &services.CrossProjectWorkbenchPage{
			Items: []services.CrossProjectWorkbenchTicket{{
				ID:           42,
				ProjectID:    3,
				ProjectKey:   "OPS",
				ProjectName:  "Operations",
				TicketNumber: "OPS-42",
				Title:        "Example",
				Status:       models.TicketStatusOpen,
			}},
			Total:      1,
			Page:       2,
			PageSize:   10,
			TotalPages: 3,
			View:       services.CrossProjectWorkbenchAssigned,
		},
	}
	handler := NewCrossProjectWorkbenchHandler(stub)
	router := gin.New()
	router.GET("/api/workbench/tickets", func(c *gin.Context) {
		c.Set("user_id", uint(7))
		c.Set("user_role", string(models.RoleAdmin))
		c.Next()
	}, handler.ListTickets)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/workbench/tickets?view=assigned&page=2&page_size=10",
		nil,
	)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if stub.calls != 1 ||
		stub.input.UserID != 7 ||
		stub.input.View != services.CrossProjectWorkbenchAssigned ||
		stub.input.Page != 2 ||
		stub.input.PageSize != 10 {
		t.Fatalf("service input = %+v, calls = %d", stub.input, stub.calls)
	}
	var envelope middleware.StandardResponse
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != 0 {
		t.Fatalf("response = %+v", envelope)
	}
}

func TestCrossProjectWorkbenchHandlerRejectsInvalidPagination(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &crossProjectWorkbenchQueryStub{}
	handler := NewCrossProjectWorkbenchHandler(stub)
	router := gin.New()
	router.GET("/api/workbench/tickets", func(c *gin.Context) {
		c.Set("user_id", uint(7))
		c.Next()
	}, handler.ListTickets)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/workbench/tickets?page_size=unbounded",
		nil,
	)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if stub.calls != 0 {
		t.Fatalf("service called %d times for invalid query", stub.calls)
	}
}

func TestCrossProjectWorkbenchHandlerRequiresAuthenticatedHuman(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &crossProjectWorkbenchQueryStub{}
	handler := NewCrossProjectWorkbenchHandler(stub)
	router := gin.New()
	router.GET("/api/workbench/tickets", handler.ListTickets)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/workbench/tickets",
		nil,
	)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if stub.calls != 0 {
		t.Fatalf("service called %d times without identity", stub.calls)
	}
}

func TestCrossProjectWorkbenchHandlerMapsDomainErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{
			name:       "access",
			err:        services.ErrCrossProjectWorkbenchAccessDenied,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "query",
			err:        services.ErrCrossProjectWorkbenchQuery,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "project limit",
			err:        services.ErrCrossProjectWorkbenchProjectLimit,
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "internal",
			err:        errors.New("database unavailable"),
			wantStatus: http.StatusInternalServerError,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &crossProjectWorkbenchQueryStub{err: test.err}
			handler := NewCrossProjectWorkbenchHandler(stub)
			router := gin.New()
			router.GET("/api/workbench/tickets", func(c *gin.Context) {
				c.Set("user_id", uint(7))
				c.Next()
			}, handler.ListTickets)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodGet,
				"/api/workbench/tickets",
				nil,
			)
			router.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf(
					"status = %d, want %d, body = %s",
					response.Code,
					test.wantStatus,
					response.Body.String(),
				)
			}
		})
	}
}
