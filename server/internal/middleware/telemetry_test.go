package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/observability"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestTracingMiddlewareContinuesW3CTraceAndExposesTrustedIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	defer provider.Shutdown(context.Background())

	router := gin.New()
	router.Use(TracingMiddleware(TelemetryConfig{
		TracerProvider: provider,
		Propagator:     propagation.TraceContext{},
	}))
	router.GET(
		"/api/v2/projects/:projectKey/tickets/:ticketID",
		func(c *gin.Context) {
			if got := TraceID(c); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
				t.Errorf("handler trace ID = %q", got)
			}
			correlationID := CorrelationID(c)
			if correlationID == "" ||
				!observability.IsValidCorrelationID(correlationID) {
				t.Errorf("handler correlation ID = %q", correlationID)
			}
			if got := c.GetHeader(observability.CorrelationIDHeader); got != correlationID {
				t.Errorf("trusted request correlation header = %q, want %q", got, correlationID)
			}
			if got := observability.CorrelationIDFromContext(c.Request.Context()); got != correlationID {
				t.Errorf("standard context correlation ID = %q, want %q", got, correlationID)
			}
			c.Status(http.StatusNoContent)
		},
	)

	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/projects/secret-project/tickets/9001",
		nil,
	)
	request.Header.Set(
		"traceparent",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	)
	request.Header.Set(
		observability.CorrelationIDHeader,
		"invalid correlation with spaces",
	)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get(observability.TraceIDHeader); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("response trace ID = %q", got)
	}
	if got := recorder.Header().Get(observability.CorrelationIDHeader); !observability.IsValidCorrelationID(got) ||
		got == "invalid correlation with spaces" {
		t.Fatalf("response correlation ID = %q", got)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}
	span := spans[0]
	if span.Name != "GET /api/v2/projects/:projectKey/tickets/:ticketID" {
		t.Fatalf("span name = %q", span.Name)
	}
	if span.SpanKind != trace.SpanKindServer {
		t.Fatalf("span kind = %v", span.SpanKind)
	}
	if !span.Parent.IsRemote() ||
		span.Parent.SpanID().String() != "00f067aa0ba902b7" {
		t.Fatalf("span parent = %+v", span.Parent)
	}
	attributes := attributesByName(span.Attributes)
	if attributes["http.route"] != "/api/v2/projects/:projectKey/tickets/:ticketID" {
		t.Fatalf("route attribute = %q", attributes["http.route"])
	}
	for _, sensitive := range []string{"secret-project", "9001"} {
		for key, value := range attributes {
			if value == sensitive {
				t.Fatalf("attribute %q leaked high-cardinality value %q", key, value)
			}
		}
	}
}

func TestTracingMiddlewareNoopPreservesInboundTraceContext(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(TracingMiddleware(TelemetryConfig{}))
	router.GET("/health", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set(
		"traceparent",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := recorder.Header().Get(observability.TraceIDHeader); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace ID = %q", got)
	}
}

func TestTracingMiddlewareRecordsPanicWithoutLeakingPanicValue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	defer provider.Shutdown(context.Background())

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(TracingMiddleware(TelemetryConfig{
		TracerProvider: provider,
		Propagator:     propagation.TraceContext{},
	}))
	router.GET("/panic", func(*gin.Context) {
		panic("secret-panic-payload")
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/panic", nil),
	)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", recorder.Code)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}
	if spans[0].Status.Code != codes.Error {
		t.Fatalf("span status = %+v", spans[0].Status)
	}
	attributes := attributesByName(spans[0].Attributes)
	if attributes["http.response.status_code"] != "500" {
		t.Fatalf("status attribute = %q", attributes["http.response.status_code"])
	}
	for _, event := range spans[0].Events {
		for _, current := range event.Attributes {
			if strings.Contains(current.Value.Emit(), "secret-panic-payload") {
				t.Fatalf("span event leaked panic value: %+v", event)
			}
		}
	}
}

func attributesByName(attributes []attribute.KeyValue) map[string]string {
	result := make(map[string]string, len(attributes))
	for _, current := range attributes {
		result[string(current.Key)] = current.Value.Emit()
	}
	return result
}
