package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/observability"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
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

func TestTracingMiddlewareSensitiveRouteRejectsExternalPropagationWithoutLosingRequestState(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	defer provider.Shutdown(context.Background())
	propagator := propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)

	const (
		externalTraceID     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		externalCorrelation = "OLD-CODE-01"
		externalTraceState  = "vendor=TOTPSECRETBASE32"
		externalBaggage     = "otp_storage_hash=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	var (
		handlerTraceID     string
		handlerCorrelation string
		handlerTraceState  string
		handlerBaggage     string
		handlerContextErr  error
		handlerDeadline    time.Time
		hasDeadline        bool
		requestHeaders     http.Header
	)
	router := gin.New()
	router.Use(TracingMiddleware(TelemetryConfig{
		TracerProvider: provider,
		Propagator:     propagator,
	}))
	router.POST("/api/auth/otp/backup-codes", func(c *gin.Context) {
		handlerTraceID = TraceID(c)
		handlerCorrelation = CorrelationID(c)
		handlerTraceState = trace.SpanContextFromContext(
			c.Request.Context(),
		).TraceState().String()
		handlerBaggage = baggage.FromContext(c.Request.Context()).String()
		handlerContextErr = c.Request.Context().Err()
		handlerDeadline, hasDeadline = c.Request.Context().Deadline()
		requestHeaders = c.Request.Header.Clone()
		c.Status(http.StatusNoContent)
	})

	expectedDeadline := time.Now().Add(time.Minute)
	requestContext, cancel := context.WithDeadline(
		context.Background(),
		expectedDeadline,
	)
	cancel()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/otp/backup-codes",
		nil,
	).WithContext(requestContext)
	request.Header.Set(
		"traceparent",
		"00-"+externalTraceID+"-00f067aa0ba902b7-01",
	)
	request.Header.Set("tracestate", externalTraceState)
	request.Header.Set("baggage", externalBaggage)
	request.Header.Set(
		observability.CorrelationIDHeader,
		externalCorrelation,
	)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	if handlerContextErr != context.Canceled ||
		!hasDeadline ||
		!handlerDeadline.Equal(expectedDeadline) {
		t.Fatalf(
			"request state lost: err=%v deadline=%v has=%v",
			handlerContextErr,
			handlerDeadline,
			hasDeadline,
		)
	}
	if handlerTraceID == "" ||
		handlerTraceID == externalTraceID ||
		handlerCorrelation == "" ||
		handlerCorrelation == externalCorrelation {
		t.Fatalf(
			"server telemetry IDs = trace %q correlation %q",
			handlerTraceID,
			handlerCorrelation,
		)
	}
	if handlerTraceState != "" || handlerBaggage != "" {
		t.Fatalf(
			"external propagation reached context: tracestate=%q baggage=%q",
			handlerTraceState,
			handlerBaggage,
		)
	}
	for _, header := range []string{
		"traceparent",
		"tracestate",
		"baggage",
	} {
		if got := requestHeaders.Get(header); got != "" {
			t.Fatalf("sensitive request retained %s=%q", header, got)
		}
	}
	for _, secret := range []string{
		externalTraceID,
		externalCorrelation,
		"TOTPSECRETBASE32",
		strings.TrimPrefix(externalBaggage, "otp_storage_hash="),
	} {
		for name, value := range recorder.Header() {
			if strings.Contains(strings.Join(value, ","), secret) {
				t.Fatalf("response header %s exposed %q", name, secret)
			}
		}
	}
}

func TestTracingMiddlewareOrdinaryRoutePreservesExternalPropagation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	defer provider.Shutdown(context.Background())

	const (
		externalTraceID     = "4bf92f3577b34da6a3ce929d0e0e4736"
		externalCorrelation = "ordinary-correlation"
		externalTraceState  = "vendor=ordinary"
		externalBaggage     = "tenant=ordinary"
	)
	router := gin.New()
	router.Use(TracingMiddleware(TelemetryConfig{
		TracerProvider: provider,
		Propagator: propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	}))
	router.GET("/ordinary", func(c *gin.Context) {
		if got := TraceID(c); got != externalTraceID {
			t.Errorf("trace ID = %q", got)
		}
		if got := CorrelationID(c); got != externalCorrelation {
			t.Errorf("correlation ID = %q", got)
		}
		if got := trace.SpanContextFromContext(
			c.Request.Context(),
		).TraceState().String(); got != externalTraceState {
			t.Errorf("tracestate = %q", got)
		}
		if got := baggage.FromContext(
			c.Request.Context(),
		).String(); got != externalBaggage {
			t.Errorf("baggage = %q", got)
		}
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/ordinary", nil)
	request.Header.Set(
		"traceparent",
		"00-"+externalTraceID+"-00f067aa0ba902b7-01",
	)
	request.Header.Set("tracestate", externalTraceState)
	request.Header.Set("baggage", externalBaggage)
	request.Header.Set(
		observability.CorrelationIDHeader,
		externalCorrelation,
	)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d", recorder.Code)
	}
	if recorder.Header().Get(observability.TraceIDHeader) != externalTraceID ||
		recorder.Header().Get(observability.CorrelationIDHeader) !=
			externalCorrelation {
		t.Fatalf("ordinary propagation response headers = %v", recorder.Header())
	}
}

func TestTracingMiddlewareSensitiveRouteNoopCreatesServerTelemetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const externalTraceID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	router := gin.New()
	router.Use(TracingMiddleware(TelemetryConfig{}))
	router.POST("/api/auth/otp/backup-codes", func(c *gin.Context) {
		if got := TraceID(c); got == "" || got == externalTraceID {
			t.Errorf("server trace ID = %q", got)
		}
		if got := CorrelationID(c); got == "" || got == externalTraceID {
			t.Errorf("server correlation ID = %q", got)
		}
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/otp/backup-codes",
		nil,
	)
	request.Header.Set(
		"traceparent",
		"00-"+externalTraceID+"-00f067aa0ba902b7-01",
	)
	request.Header.Set(
		observability.CorrelationIDHeader,
		externalTraceID,
	)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d", recorder.Code)
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
