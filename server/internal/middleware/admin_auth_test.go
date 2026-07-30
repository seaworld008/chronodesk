package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"github.com/gin-gonic/gin"
)

type recordingAdminAuditService struct {
	records       []*services.AdminAuditRecord
	recordErr     error
	finalizeErr   error
	finalizeCalls int
}

func (s *recordingAdminAuditService) Record(_ context.Context, record *services.AdminAuditRecord) error {
	if s.recordErr != nil {
		return s.recordErr
	}
	record.ID = uint(len(s.records) + 1)
	s.records = append(s.records, record)
	return nil
}

func (s *recordingAdminAuditService) Finalize(
	_ context.Context,
	_ *services.AdminAuditRecord,
) error {
	s.finalizeCalls++
	return s.finalizeErr
}

func (*recordingAdminAuditService) List(
	context.Context,
	*services.AdminAuditFilter,
) ([]*models.AdminAuditLog, int64, error) {
	return nil, 0, nil
}

func TestImportantAdminOperationCoversPlatformAndProjectAgentControlPlanes(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{name: "legacy write", method: http.MethodPost, path: "/api/admin/users", want: true},
		{name: "legacy nested write", method: http.MethodDelete, path: "/api/admin/webhooks/42", want: true},
		{name: "agent principal write", method: http.MethodPost, path: "/api/projects/OPS/admin/agents/service-principals", want: true},
		{name: "agent credential write", method: http.MethodPost, path: "/api/projects/OPS/admin/agents/service-principals/p1/credentials/rotate", want: true},
		{name: "agent read", method: http.MethodGet, path: "/api/projects/OPS/admin/agents/agent-control/overview", want: false},
		{name: "head is read", method: http.MethodHead, path: "/api/admin/users", want: false},
		{name: "options is read", method: http.MethodOptions, path: "/api/projects/OPS/admin/agents/service-principals", want: false},
		{name: "project admin prefix boundary", method: http.MethodPost, path: "/api/projects/OPS/admin/agent/service-principals", want: false},
		{name: "legacy prefix boundary", method: http.MethodPost, path: "/api/administrator/users", want: false},
		{name: "admin prefix boundary", method: http.MethodPost, path: "/api/administrator/service-principals", want: false},
		{name: "non admin write", method: http.MethodPost, path: "/api/v2/projects/OPS/tickets", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isImportantAdminOperation(tt.method, tt.path); got != tt.want {
				t.Fatalf("isImportantAdminOperation(%q, %q) = %v, want %v", tt.method, tt.path, got, tt.want)
			}
		})
	}
}

func TestLogAdminOperationRecordsProjectAgentManagementWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	audit := &recordingAdminAuditService{}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", uint(7))
		c.Set("user_role", "admin")
		c.Next()
	})
	router.Use(LogAdminOperation(audit))
	router.POST("/api/projects/:projectKey/admin/agents/service-principals", func(c *gin.Context) {
		c.Status(http.StatusCreated)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/OPS/admin/agents/service-principals?client_secret=must-not-leak&view=compact",
		nil,
	)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusCreated)
	}
	if len(audit.records) != 1 {
		t.Fatalf("audit records = %d, want 1", len(audit.records))
	}
	if audit.finalizeCalls != 1 {
		t.Fatalf("audit finalize calls = %d, want 1", audit.finalizeCalls)
	}
	record := audit.records[0]
	if record.Path != "/api/projects/OPS/admin/agents/service-principals" ||
		record.Method != http.MethodPost ||
		record.StatusCode != http.StatusCreated ||
		record.UserID == nil ||
		*record.UserID != 7 {
		t.Fatalf("unexpected audit record: %+v", record)
	}
	if record.Query != "client_secret=%5BREDACTED%5D&view=compact" {
		t.Fatalf("audit query was not safely redacted: %q", record.Query)
	}
}

func TestAdminWriteFailsClosedBeforeHandlerWhenAuditAnchorCannotPersist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	audit := &recordingAdminAuditService{recordErr: errors.New("database unavailable")}
	handlerCalled := false
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", uint(7))
		c.Set("user_role", "admin")
		c.Next()
	})
	router.Use(LogAdminOperation(audit))
	router.POST("/api/admin/users", func(c *gin.Context) {
		handlerCalled = true
		c.Status(http.StatusCreated)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/users", nil)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if handlerCalled {
		t.Fatal("admin handler ran without a durable audit anchor")
	}
	if len(audit.records) != 0 || audit.finalizeCalls != 0 {
		t.Fatalf(
			"audit lifecycle = records:%d finalizations:%d, want 0/0",
			len(audit.records),
			audit.finalizeCalls,
		)
	}
}

func TestAdminWriteRetainsPendingAnchorWhenFinalizationFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	audit := &recordingAdminAuditService{finalizeErr: errors.New("database unavailable")}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", uint(7))
		c.Set("user_role", "admin")
		c.Next()
	})
	router.Use(LogAdminOperation(audit))
	router.POST("/api/admin/users", func(c *gin.Context) {
		c.Status(http.StatusCreated)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/users", nil)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusCreated)
	}
	if len(audit.records) != 1 || audit.records[0].ID == 0 {
		t.Fatalf("durable audit anchor missing: %+v", audit.records)
	}
	if audit.finalizeCalls != 1 {
		t.Fatalf("audit finalize calls = %d, want 1", audit.finalizeCalls)
	}
}

func TestAdminWritePanicFinalizesDurableAuditAsError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	audit := &recordingAdminAuditService{}
	router := gin.New()
	router.Use(WrapGinMiddleware(RecoveryMiddleware(&RecoveryConfig{
		Logger:            NewSimpleLogger(nil, LogLevelError),
		EnableStackTrace:  false,
		DisablePrintStack: true,
	})))
	router.Use(func(c *gin.Context) {
		c.Set("user_id", uint(7))
		c.Set("user_role", "admin")
		c.Next()
	})
	router.Use(LogAdminOperation(audit))
	router.POST("/api/admin/users", func(*gin.Context) {
		panic("handler panic")
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/users", nil)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	if len(audit.records) != 1 || audit.finalizeCalls != 1 {
		t.Fatalf(
			"audit lifecycle = records:%d finalizations:%d, want 1/1",
			len(audit.records),
			audit.finalizeCalls,
		)
	}
	record := audit.records[0]
	if record.Result != "error" ||
		record.StatusCode != http.StatusInternalServerError ||
		record.Notes != "管理员写操作异常终止" {
		t.Fatalf("panic audit was not finalized as an error: %+v", record)
	}
}
