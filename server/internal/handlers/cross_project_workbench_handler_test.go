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
		c.Set("platform_role", models.PlatformRolePlatformAdmin)
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

func TestCrossProjectWorkbenchObserverCannotBypassTicketIdentityProjection(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	for _, statement := range []string{
		`CREATE TABLE projects (
			id INTEGER PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			key TEXT NOT NULL,
			name TEXT NOT NULL,
			status TEXT NOT NULL
		)`,
		`CREATE TABLE project_memberships (
			id INTEGER PRIMARY KEY,
			project_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			role TEXT NOT NULL,
			is_active BOOLEAN NOT NULL
		)`,
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY,
			username TEXT NOT NULL,
			display_name TEXT,
			status TEXT NOT NULL DEFAULT 'active',
			deleted_at DATETIME
		)`,
		`CREATE TABLE tickets (
			id INTEGER PRIMARY KEY,
			public_id TEXT NOT NULL,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			ticket_number TEXT NOT NULL,
			title TEXT NOT NULL,
			type TEXT NOT NULL,
			priority TEXT NOT NULL,
			status TEXT NOT NULL,
			created_by_id INTEGER,
			assigned_to_id INTEGER,
			created_by_actor_type TEXT NOT NULL,
			created_by_actor_id TEXT NOT NULL,
			assigned_to_actor_type TEXT,
			assigned_to_actor_id TEXT,
			due_date DATETIME,
			sla_due_date DATETIME,
			sla_breached BOOLEAN NOT NULL DEFAULT FALSE,
			version INTEGER NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
		`INSERT INTO projects (id, organization_id, key, name, status)
		 VALUES (10, 1, 'OBS', 'Observer Project', 'active')`,
		`INSERT INTO project_memberships
		 (id, project_id, user_id, role, is_active)
		 VALUES (1, 10, 7, 'observer', TRUE)`,
		`INSERT INTO users (id, username, display_name)
		 VALUES (7, 'observer', 'Observer'), (8, 'assignee-sentinel', 'Assignee Sentinel')`,
		`INSERT INTO tickets (
			id, public_id, organization_id, project_id, ticket_number, title,
			type, priority, status, created_by_id, assigned_to_id,
			created_by_actor_type, created_by_actor_id,
			assigned_to_actor_type, assigned_to_actor_id,
			sla_breached, version, created_at, updated_at
		) VALUES (
			91, '00000000-0000-7000-8000-000000000091', 1, 10,
			'OBS-91', 'observer projection', 'request', 'normal', 'open',
			7, 8, 'human', '7', 'human', '8', FALSE, 1,
			'2026-08-02T08:00:00Z', '2026-08-02T08:00:00Z'
		)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("prepare observer workbench fixture: %v", err)
		}
	}
	service, err := services.NewCrossProjectWorkbenchService(db)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewCrossProjectWorkbenchHandler(service)
	router := gin.New()
	router.GET("/api/workbench/tickets", func(c *gin.Context) {
		c.Set("user_id", uint(7))
		handler.ListTickets(c)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodGet,
			"/api/workbench/tickets?view=created",
			nil,
		),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data struct {
			Items []map[string]json.RawMessage `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Items) != 1 {
		t.Fatalf("items=%d body=%s", len(envelope.Data.Items), response.Body.String())
	}
	for _, forbidden := range []string{
		"created_by_id",
		"assigned_to_id",
		"assigned_to_name",
	} {
		if _, leaked := envelope.Data.Items[0][forbidden]; leaked {
			t.Errorf("observer workbench leaked %q: %s", forbidden, response.Body.String())
		}
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
