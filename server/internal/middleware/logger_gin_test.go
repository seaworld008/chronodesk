package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestGinRequestQueryRedactsCredentialsBeforeLogging(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/auth/verify-email?view=compact&token=first&to%6ben=second&authorization_code=third&signature=fourth",
		nil,
	)

	got := getQuery(NewGinHTTPContext(context))
	for _, secret := range []string{"first", "second", "third", "fourth"} {
		if strings.Contains(got, secret) {
			t.Fatalf("query leaked %q: %q", secret, got)
		}
	}
	for _, want := range []string{
		"authorization_code=%5BREDACTED%5D",
		"signature=%5BREDACTED%5D",
		"token=%5BREDACTED%5D",
		"view=compact",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("query = %q, want field %q", got, want)
		}
	}
	if strings.Count(got, "token=") != 1 {
		t.Fatalf("repeated credential keys were not collapsed safely: %q", got)
	}
}

func TestSanitizeQueryForLogsFailsClosedForMalformedAndOversizedInput(t *testing.T) {
	if got := sanitizeQueryForLogs("%zz=secret"); got != "" {
		t.Fatalf("malformed query = %q, want empty", got)
	}
	if got := sanitizeQueryForLogs("view=" + strings.Repeat("a", maxLoggedQueryBytes+1)); got != "[TRUNCATED]" {
		t.Fatalf("oversized query = %q, want [TRUNCATED]", got)
	}
}
