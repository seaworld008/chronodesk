package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestNewTracingRuntimeWithoutEndpointIsSafeNoop(t *testing.T) {
	t.Parallel()

	runtime, err := NewTracingRuntime(context.Background(), TracingConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Enabled() {
		t.Fatal("runtime unexpectedly enabled OTLP export")
	}
	if runtime.TracerProvider() == nil || runtime.Propagator() == nil {
		t.Fatal("no-op runtime did not provide complete wiring interfaces")
	}

	headers := http.Header{}
	headers.Set(
		"traceparent",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	)
	parent := runtime.Propagator().Extract(
		context.Background(),
		propagation.HeaderCarrier(headers),
	)
	_, span := runtime.TracerProvider().Tracer("test").Start(parent, "noop")
	defer span.End()
	spanContext := span.SpanContext()
	if !spanContext.IsValid() {
		t.Fatal("no-op runtime discarded valid inbound W3C trace context")
	}
	if got := spanContext.TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace ID = %q", got)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("no-op shutdown: %v", err)
	}
}

func TestNewTracingRuntimeFailsClosedForUnsafeConfiguration(t *testing.T) {
	t.Parallel()

	ratioAboveOne := 1.1
	ratioBelowZero := -0.1
	tests := []struct {
		name   string
		config TracingConfig
	}{
		{
			name: "relative endpoint",
			config: TracingConfig{
				OTLPHTTPEndpoint: "/v1/traces",
			},
		},
		{
			name: "insecure endpoint without opt in",
			config: TracingConfig{
				OTLPHTTPEndpoint: "http://collector.internal:4318/v1/traces",
			},
		},
		{
			name: "credentials in endpoint",
			config: TracingConfig{
				OTLPHTTPEndpoint: "https://token@collector.example/v1/traces",
			},
		},
		{
			name: "query in endpoint",
			config: TracingConfig{
				OTLPHTTPEndpoint: "https://collector.example/v1/traces?token=secret",
			},
		},
		{
			name: "header injection",
			config: TracingConfig{
				OTLPHeaders: map[string]string{
					"Authorization": "value\r\nX-Forged: yes",
				},
			},
		},
		{
			name: "invalid header name",
			config: TracingConfig{
				OTLPHeaders: map[string]string{
					"Invalid Header": "value",
				},
			},
		},
		{
			name: "negative timeout",
			config: TracingConfig{
				ExportTimeout: -time.Second,
			},
		},
		{
			name: "sampling ratio above one",
			config: TracingConfig{
				TraceSamplingRatio: &ratioAboveOne,
			},
		},
		{
			name: "sampling ratio below zero",
			config: TracingConfig{
				TraceSamplingRatio: &ratioBelowZero,
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runtime, err := NewTracingRuntime(context.Background(), test.config)
			if err == nil {
				_ = runtime.Shutdown(context.Background())
				t.Fatal("unsafe tracing configuration was accepted")
			}
		})
	}
}

func TestNewTracingRuntimeAcceptsExplicitExporterConfiguration(t *testing.T) {
	t.Parallel()

	neverSample := 0.0
	runtime, err := NewTracingRuntime(context.Background(), TracingConfig{
		ServiceName:        "chronodesk-test",
		ServiceVersion:     "test",
		Environment:        "unit",
		OTLPHTTPEndpoint:   "http://127.0.0.1:4318/v1/traces",
		AllowInsecureHTTP:  true,
		TraceSamplingRatio: &neverSample,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !runtime.Enabled() {
		t.Fatal("configured runtime is disabled")
	}
	_, span := runtime.TracerProvider().Tracer("test").Start(
		context.Background(),
		"not-exported",
	)
	if span.SpanContext().TraceFlags().IsSampled() {
		t.Fatal("zero sampling ratio produced sampled span")
	}
	span.End()
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestNewTracingRuntimeExportsToStableTracePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		endpointPath string
		wantPath     string
	}{
		{
			name:     "base endpoint",
			wantPath: defaultOTLPTracePath,
		},
		{
			name:         "base endpoint with trailing slash",
			endpointPath: "/",
			wantPath:     defaultOTLPTracePath,
		},
		{
			name:         "explicit custom path",
			endpointPath: "/collector/custom-traces",
			wantPath:     "/collector/custom-traces",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			type exportRequest struct {
				method string
				path   string
			}
			requests := make(chan exportRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(
				func(response http.ResponseWriter, request *http.Request) {
					requests <- exportRequest{
						method: request.Method,
						path:   request.URL.Path,
					}
					response.Header().Set("Content-Type", "application/x-protobuf")
					response.WriteHeader(http.StatusOK)
				},
			))
			defer server.Close()

			alwaysSample := 1.0
			runtime, err := NewTracingRuntime(context.Background(), TracingConfig{
				ServiceName:        "chronodesk-test",
				OTLPHTTPEndpoint:   server.URL + test.endpointPath,
				AllowInsecureHTTP:  true,
				TraceSamplingRatio: &alwaysSample,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, span := runtime.TracerProvider().Tracer("test").Start(
				context.Background(),
				"exported",
			)
			span.End()

			shutdownContext, cancel := context.WithTimeout(
				context.Background(),
				5*time.Second,
			)
			defer cancel()
			if err := runtime.Shutdown(shutdownContext); err != nil {
				t.Fatalf("shutdown: %v", err)
			}

			select {
			case request := <-requests:
				if request.method != http.MethodPost {
					t.Fatalf("export method = %q", request.method)
				}
				if request.path != test.wantPath {
					t.Fatalf("export path = %q, want %q", request.path, test.wantPath)
				}
			default:
				t.Fatal("trace exporter did not send a request")
			}
		})
	}
}

func TestInstallGlobalsCanBeRestored(t *testing.T) {
	// Global OpenTelemetry state is process-wide; intentionally do not run this
	// test in parallel.
	runtime, err := NewTracingRuntime(context.Background(), TracingConfig{})
	if err != nil {
		t.Fatal(err)
	}
	restore := runtime.InstallGlobals()
	defer restore()

	headers := http.Header{}
	headers.Set(
		"traceparent",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	)
	ctx := runtime.Propagator().Extract(
		context.Background(),
		propagation.HeaderCarrier(headers),
	)
	if got := trace.SpanContextFromContext(ctx).TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("global propagator trace ID = %q", got)
	}
}
