package middleware

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func loggedCanceledRequest(
	t *testing.T,
	requestContext context.Context,
	handler gin.HandlerFunc,
) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var output bytes.Buffer
	router := gin.New()
	router.Use(WrapGinMiddleware(LoggingMiddleware(&LoggerConfig{
		Logger:       NewSimpleLogger(&output, LogLevelDebug),
		LogLatency:   true,
		LogRequestID: true,
	})))
	router.GET("/read", handler)
	request := httptest.NewRequest(http.MethodGet, "/read", nil).
		WithContext(requestContext)
	router.ServeHTTP(httptest.NewRecorder(), request)
	return output.String()
}

func TestLoggingMiddlewareClassifiesRequestContextTermination(t *testing.T) {
	tests := []struct {
		name            string
		requestContext  func() (context.Context, context.CancelFunc)
		wantStatus      string
		wantTermination string
	}{
		{
			name: "client cancellation",
			requestContext: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
			wantStatus:      "status=499",
			wantTermination: "termination=client_canceled",
		},
		{
			name: "request deadline",
			requestContext: func() (context.Context, context.CancelFunc) {
				return context.WithDeadline(context.Background(), testExpiredDeadline())
			},
			wantStatus:      "status=408",
			wantTermination: "termination=deadline_exceeded",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestContext, cancel := test.requestContext()
			defer cancel()
			output := loggedCanceledRequest(
				t,
				requestContext,
				func(c *gin.Context) { c.Abort() },
			)
			for _, expected := range []string{
				"INFO Request canceled",
				test.wantStatus,
				test.wantTermination,
			} {
				if !strings.Contains(output, expected) {
					t.Fatalf("log missing %q:\n%s", expected, output)
				}
			}
			if strings.Contains(output, "status=200") ||
				strings.Contains(output, "ERROR Request") {
				t.Fatalf("canceled request used success/error severity:\n%s", output)
			}
		})
	}
}

func TestLoggingMiddlewarePreservesCommittedServerErrorAfterCancellation(t *testing.T) {
	requestContext, cancel := context.WithCancel(context.Background())
	cancel()
	output := loggedCanceledRequest(
		t,
		requestContext,
		func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "database unavailable"})
		},
	)
	if !strings.Contains(output, "ERROR Request completed") ||
		!strings.Contains(output, "status=503") {
		t.Fatalf("committed server error was not preserved:\n%s", output)
	}
	if strings.Contains(output, "Request canceled") {
		t.Fatalf("committed server error was hidden as cancellation:\n%s", output)
	}
}

func TestLoggingMiddlewareDoesNotSuppressUnrelatedHandlerError(t *testing.T) {
	requestContext, cancel := context.WithCancel(context.Background())
	cancel()
	output := loggedCanceledRequest(
		t,
		requestContext,
		func(c *gin.Context) {
			_ = c.Error(errors.New("database unavailable"))
			c.Abort()
		},
	)
	if !strings.Contains(output, "ERROR Request failed") {
		t.Fatalf("unrelated handler error was suppressed:\n%s", output)
	}
	if strings.Contains(output, "Request canceled") {
		t.Fatalf("unrelated handler error was hidden as cancellation:\n%s", output)
	}
}

func testExpiredDeadline() (deadline time.Time) {
	return time.Unix(1, 0)
}
