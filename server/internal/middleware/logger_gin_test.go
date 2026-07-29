package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGinRequestMetadataHelpers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/mcp?cursor=opaque", nil)
	context.Request.RemoteAddr = "192.0.2.1:1234"
	context.Status(http.StatusAccepted)
	_, _ = context.Writer.Write([]byte("accepted"))

	httpContext := NewGinHTTPContext(context)
	if got := getMethod(httpContext); got != http.MethodPost {
		t.Fatalf("method = %q, want POST", got)
	}
	if got := getPath(httpContext); got != "/mcp" {
		t.Fatalf("path = %q, want /mcp", got)
	}
	if got := getQuery(httpContext); got != "cursor=opaque" {
		t.Fatalf("query = %q, want cursor=opaque", got)
	}
	if got := getClientIP(httpContext); got != "192.0.2.1" {
		t.Fatalf("client IP = %q, want 192.0.2.1", got)
	}
	if got := getStatusCode(httpContext); got != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", got, http.StatusAccepted)
	}
	if got := getResponseSize(httpContext); got != len("accepted") {
		t.Fatalf("response size = %d, want %d", got, len("accepted"))
	}
}
