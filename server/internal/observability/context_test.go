package observability

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestCorrelationIDContextAcceptsOnlyBoundedControlValues(t *testing.T) {
	t.Parallel()

	valid := "incident-42:attempt_3"
	ctx := ContextWithCorrelationID(context.Background(), valid)
	if got := CorrelationIDFromContext(ctx); got != valid {
		t.Fatalf("correlation ID = %q, want %q", got, valid)
	}

	for _, invalid := range []string{
		"",
		"contains spaces",
		"line\r\nforged",
		strings.Repeat("a", maxCorrelationIDLength+1),
	} {
		ctx := ContextWithCorrelationID(context.Background(), invalid)
		if got := CorrelationIDFromContext(ctx); got != "" {
			t.Fatalf("invalid correlation ID %q survived as %q", invalid, got)
		}
	}
}

func TestTraceIDFromContextUsesOpenTelemetryIdentity(t *testing.T) {
	t.Parallel()

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	ctx := trace.ContextWithSpanContext(
		context.Background(),
		trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: traceID,
			SpanID:  spanID,
		}),
	)
	if got := TraceIDFromContext(ctx); got != traceID.String() {
		t.Fatalf("trace ID = %q, want %q", got, traceID.String())
	}
}

func TestNewCorrelationIDIsValidAndDistinct(t *testing.T) {
	t.Parallel()

	first, err := NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	if !IsValidCorrelationID(first) || !IsValidCorrelationID(second) {
		t.Fatalf("invalid generated IDs: %q, %q", first, second)
	}
	if first == second {
		t.Fatalf("generated duplicate correlation ID %q", first)
	}
}
