package handlers

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

type captureAdminAuditExporter struct {
	filter   *services.AdminAuditFilter
	userID   uint
	role     models.PlatformRole
	anchorID uint
	create   *services.AdminAuditExportView
	status   *services.AdminAuditExportView
	download *services.AdminAuditExportDownload
	err      error
	calls    int
}

func (exporter *captureAdminAuditExporter) Create(
	_ context.Context,
	userID uint,
	role models.PlatformRole,
	filter *services.AdminAuditFilter,
	anchorID uint,
) (*services.AdminAuditExportView, error) {
	exporter.calls++
	exporter.userID = userID
	exporter.role = role
	exporter.filter = filter
	exporter.anchorID = anchorID
	return exporter.create, exporter.err
}

func (exporter *captureAdminAuditExporter) Get(
	_ context.Context,
	userID uint,
	_ string,
) (*services.AdminAuditExportView, error) {
	exporter.calls++
	exporter.userID = userID
	return exporter.status, exporter.err
}

func (exporter *captureAdminAuditExporter) Open(
	_ context.Context,
	userID uint,
	_ string,
) (*services.AdminAuditExportDownload, error) {
	exporter.calls++
	exporter.userID = userID
	return exporter.download, exporter.err
}

type captureAuditExportMiddleware struct {
	record    services.AdminAuditRecord
	finalized services.AdminAuditRecord
}

func (capture *captureAuditExportMiddleware) Record(
	_ context.Context,
	record *services.AdminAuditRecord,
) error {
	record.ID = 73
	capture.record = *record
	return nil
}

func (capture *captureAuditExportMiddleware) Finalize(
	_ context.Context,
	record *services.AdminAuditRecord,
) error {
	capture.finalized = *record
	return nil
}

func (*captureAuditExportMiddleware) List(
	context.Context,
	*services.AdminAuditFilter,
) ([]*models.AdminAuditLog, int64, error) {
	return nil, 0, nil
}

func adminAuditExportTestRouter(
	exporter *captureAdminAuditExporter,
	audit *captureAuditExportMiddleware,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewAdminAuditHandler(&captureAdminAuditService{})
	handler.SetExportService(exporter)
	router := gin.New()
	group := router.Group("/api/platform")
	group.Use(func(c *gin.Context) {
		c.Set("user_id", uint(41))
		c.Set("platform_role", models.PlatformRoleSecurityAuditor)
		c.Next()
	})
	group.Use(middleware.LogAdminOperation(audit))
	group.POST("/audit-exports", handler.CreateAuditExport)
	group.GET("/audit-exports/:publicID", handler.GetAuditExport)
	group.GET(
		"/audit-exports/:publicID/download",
		handler.DownloadAuditExport,
	)
	return router
}

func TestAdminAuditExportHandlerUsesDurableMiddlewareAnchorAndStrictRange(
	t *testing.T,
) {
	start := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(30 * 24 * time.Hour)
	exporter := &captureAdminAuditExporter{
		create: &services.AdminAuditExportView{
			PublicID: "0198a342-7386-7dc2-9de3-8d91b47509c2",
			State:    models.AdminAuditExportQueued,
		},
	}
	audit := &captureAuditExportMiddleware{}
	router := adminAuditExportTestRouter(exporter, audit)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/platform/audit-exports?start_time="+
			url.QueryEscape(start.Format(time.RFC3339))+
			"&end_time="+url.QueryEscape(end.Format(time.RFC3339))+
			"&method=GET&path_prefix=%2Fapi%2Fplatform",
		nil,
	)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if exporter.calls != 1 ||
		exporter.userID != 41 ||
		exporter.role != models.PlatformRoleSecurityAuditor ||
		exporter.anchorID != 73 ||
		exporter.filter == nil ||
		exporter.filter.Method != http.MethodGet ||
		exporter.filter.Path != "/api/platform" {
		t.Fatalf("captured export request = %+v", exporter)
	}
	if audit.record.ActionCode != "platform.audit_export.create" ||
		audit.record.ResourceType != "audit_export" ||
		audit.finalized.Result != "success" {
		t.Fatalf(
			"creation audit record=%+v finalized=%+v",
			audit.record,
			audit.finalized,
		)
	}
}

func TestAdminAuditExportHandlerRejectsUnknownDuplicateEmptyAndLongRange(
	t *testing.T,
) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(30*24*time.Hour + time.Nanosecond)
	tests := []string{
		"?start_time=" + url.QueryEscape(start.Format(time.RFC3339)) +
			"&end_time=" + url.QueryEscape(start.Add(time.Hour).Format(time.RFC3339)) +
			"&ghost=1",
		"?start_time=" + url.QueryEscape(start.Format(time.RFC3339)) +
			"&start_time=" + url.QueryEscape(start.Format(time.RFC3339)) +
			"&end_time=" + url.QueryEscape(start.Add(time.Hour).Format(time.RFC3339)),
		"?start_time=&end_time=" +
			url.QueryEscape(start.Add(time.Hour).Format(time.RFC3339)),
		"?start_time=" + url.QueryEscape(start.Format(time.RFC3339)) +
			"&end_time=" + url.QueryEscape(end.Format(time.RFC3339Nano)),
	}
	for _, query := range tests {
		exporter := &captureAdminAuditExporter{}
		router := adminAuditExportTestRouter(
			exporter,
			&captureAuditExportMiddleware{},
		)
		response := httptest.NewRecorder()
		router.ServeHTTP(
			response,
			httptest.NewRequest(
				http.MethodPost,
				"/api/platform/audit-exports"+query,
				nil,
			),
		)
		if response.Code != http.StatusBadRequest || exporter.calls != 0 {
			t.Fatalf(
				"query=%q status=%d calls=%d body=%s",
				query,
				response.Code,
				exporter.calls,
				response.Body,
			)
		}
	}
}

func TestAdminAuditExportDownloadStreamsOwnedBytesAndAuditsCompletion(
	t *testing.T,
) {
	payload := []byte("time,actor\n2026-07-31T10:00:00Z,审计员\n")
	exporter := &captureAdminAuditExporter{
		download: &services.AdminAuditExportDownload{
			Reader: io.NopCloser(bytes.NewReader(payload)),
			Filename: "chronodesk-audit-" +
				"0198a342-7386-7dc2-9de3-8d91b47509c2.csv",
			Size:   int64(len(payload)),
			SHA256: strings.Repeat("a", 64),
		},
	}
	audit := &captureAuditExportMiddleware{}
	router := adminAuditExportTestRouter(exporter, audit)
	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodGet,
			"/api/platform/audit-exports/"+
				"0198a342-7386-7dc2-9de3-8d91b47509c2/download",
			nil,
		),
	)
	if response.Code != http.StatusOK ||
		!bytes.Equal(response.Body.Bytes(), payload) ||
		response.Header().Get("Cache-Control") != "no-store" ||
		exporter.userID != 41 {
		t.Fatalf(
			"download status=%d headers=%v body=%q exporter=%+v",
			response.Code,
			response.Header(),
			response.Body,
			exporter,
		)
	}
	if audit.record.ActionCode != "platform.audit_export.download" ||
		audit.finalized.Result != "success" {
		t.Fatalf(
			"download audit record=%+v finalized=%+v",
			audit.record,
			audit.finalized,
		)
	}
}

func TestAdminAuditExportDownloadMapsPendingAndExpiredStates(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{services.ErrAdminAuditExportPending, http.StatusConflict},
		{services.ErrAdminAuditExportFailed, http.StatusConflict},
		{services.ErrAdminAuditExportExpired, http.StatusGone},
		{services.ErrAdminAuditExportNotFound, http.StatusNotFound},
	}
	for _, test := range tests {
		exporter := &captureAdminAuditExporter{err: test.err}
		router := adminAuditExportTestRouter(
			exporter,
			&captureAuditExportMiddleware{},
		)
		response := httptest.NewRecorder()
		router.ServeHTTP(
			response,
			httptest.NewRequest(
				http.MethodGet,
				"/api/platform/audit-exports/"+
					"0198a342-7386-7dc2-9de3-8d91b47509c2/download",
				nil,
			),
		)
		if response.Code != test.want {
			t.Fatalf(
				"error=%v status=%d body=%s",
				test.err,
				response.Code,
				response.Body,
			)
		}
	}
}
