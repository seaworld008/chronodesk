package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

const (
	// TraceIDHeader and CorrelationIDHeader are the stable HTTP compatibility
	// headers used by ChronoDesk protocol adapters.
	TraceIDHeader       = "X-Trace-ID"
	CorrelationIDHeader = "X-Correlation-ID"

	maxCorrelationIDLength = 128
)

type telemetryContextKey uint8

const correlationIDContextKey telemetryContextKey = iota

var correlationIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// ContextWithCorrelationID stores a server-validated correlation identifier in
// a standard context. Invalid values are deliberately ignored so untrusted
// header data cannot become trusted control metadata by calling this helper.
func ContextWithCorrelationID(ctx context.Context, correlationID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	correlationID = strings.TrimSpace(correlationID)
	if !IsValidCorrelationID(correlationID) {
		return ctx
	}
	return context.WithValue(ctx, correlationIDContextKey, correlationID)
}

// CorrelationIDFromContext returns only identifiers that passed the bounded
// control-value validation performed by ContextWithCorrelationID.
func CorrelationIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(correlationIDContextKey).(string)
	if !IsValidCorrelationID(value) {
		return ""
	}
	return value
}

// TraceIDFromContext exposes the OpenTelemetry trace identifier without
// introducing a second tracing identity.
func TraceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.TraceID().String()
}

// IsValidCorrelationID accepts a deliberately small, printable identifier
// alphabet. Correlation IDs are control metadata, never arbitrary user text.
func IsValidCorrelationID(value string) bool {
	if value == "" || len(value) > maxCorrelationIDLength {
		return false
	}
	return correlationIDPattern.MatchString(value)
}

// NewCorrelationID returns a cryptographically random, non-semantic
// correlation identifier. Entropy failure is returned to the caller so request
// middleware can fail closed instead of silently issuing a predictable value.
func NewCorrelationID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
