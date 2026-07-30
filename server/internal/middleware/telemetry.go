package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/observability"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const (
	TraceIDContextKey       = "trace_id"
	CorrelationIDContextKey = "correlation_id"
	defaultTracerName       = "github.com/seaworld008/chronodesk/server/internal/middleware"
)

// TelemetryConfig is the explicit Gin tracing seam. Supplying neither provider
// nor propagator is safe: the middleware becomes a no-op tracer while still
// honoring valid W3C Trace Context input.
type TelemetryConfig struct {
	TracerProvider trace.TracerProvider
	Propagator     propagation.TextMapPropagator
	TracerName     string
}

// TracingMiddleware extracts W3C Trace Context, starts one server span, and
// exposes trusted trace/correlation IDs through both response headers and Gin
// plus standard request contexts.
func TracingMiddleware(config TelemetryConfig) gin.HandlerFunc {
	provider := config.TracerProvider
	if provider == nil {
		provider = noop.NewTracerProvider()
	}
	propagator := config.Propagator
	if propagator == nil {
		propagator = propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
		)
	}
	tracerName := strings.TrimSpace(config.TracerName)
	if tracerName == "" {
		tracerName = defaultTracerName
	}
	tracer := provider.Tracer(tracerName)

	return func(c *gin.Context) {
		if c.Request == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"code": http.StatusServiceUnavailable,
				"msg":  "遥测上下文不可用",
			})
			return
		}

		method := boundedHTTPMethod(c.Request.Method)
		initialRoute := matchedRouteTemplate(c)
		parentContext := propagator.Extract(
			c.Request.Context(),
			propagation.HeaderCarrier(c.Request.Header),
		)
		requestContext, span := tracer.Start(
			parentContext,
			method+" "+initialRoute,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(attribute.String("http.request.method", method)),
		)

		traceID := observability.TraceIDFromContext(requestContext)
		correlationID, err := trustedCorrelationID(c, traceID)
		if err != nil {
			span.SetStatus(codes.Error, "telemetry identity unavailable")
			span.End()
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"code": http.StatusServiceUnavailable,
				"msg":  "请求标识生成失败",
			})
			return
		}
		requestContext = observability.ContextWithCorrelationID(
			requestContext,
			correlationID,
		)
		c.Request = c.Request.WithContext(requestContext)
		// Replace untrusted inbound correlation metadata so existing adapters
		// reading the request header see only the server-validated value.
		c.Request.Header.Set(observability.CorrelationIDHeader, correlationID)
		c.Set(CorrelationIDContextKey, correlationID)
		c.Header(observability.CorrelationIDHeader, correlationID)
		if traceID != "" {
			c.Set(TraceIDContextKey, traceID)
			c.Header(observability.TraceIDHeader, traceID)
		}

		defer func() {
			recovered := recover()
			statusCode := c.Writer.Status()
			if recovered != nil && statusCode < http.StatusInternalServerError {
				statusCode = http.StatusInternalServerError
			}
			route := matchedRouteTemplate(c)
			span.SetName(method + " " + route)
			span.SetAttributes(
				attribute.String("http.route", route),
				attribute.Int("http.response.status_code", statusCode),
			)
			if recovered != nil ||
				statusCode >= http.StatusInternalServerError ||
				len(c.Errors) != 0 {
				// Error and panic values may include untrusted ticket or
				// connector content. Record a stable event without copying
				// their raw strings.
				span.RecordError(errors.New("HTTP request failed"))
				span.SetStatus(codes.Error, "HTTP server error")
			}
			span.End()
			if recovered != nil {
				panic(recovered)
			}
		}()
		c.Next()
	}
}

// TraceID returns the OpenTelemetry trace ID exposed by TracingMiddleware.
func TraceID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if value := c.GetString(TraceIDContextKey); value != "" {
		return value
	}
	if c.Request != nil {
		return observability.TraceIDFromContext(c.Request.Context())
	}
	return ""
}

// CorrelationID returns only a validated server correlation identifier.
func CorrelationID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if value := c.GetString(CorrelationIDContextKey); observability.IsValidCorrelationID(value) {
		return value
	}
	if c.Request != nil {
		return observability.CorrelationIDFromContext(c.Request.Context())
	}
	return ""
}

func trustedCorrelationID(c *gin.Context, traceID string) (string, error) {
	if candidate := strings.TrimSpace(c.GetHeader(observability.CorrelationIDHeader)); observability.IsValidCorrelationID(candidate) {
		return candidate, nil
	}
	if observability.IsValidCorrelationID(traceID) {
		return traceID, nil
	}
	if candidate := strings.TrimSpace(c.GetString("request_id")); observability.IsValidCorrelationID(candidate) {
		return candidate, nil
	}
	return observability.NewCorrelationID()
}
