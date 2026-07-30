package observability

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const (
	defaultTracingServiceName = "chronodesk"
	defaultExportTimeout      = 10 * time.Second
)

// TracingConfig configures the process-local OpenTelemetry trace runtime.
// An empty OTLPHTTPEndpoint explicitly selects a safe no-op provider while
// retaining W3C Trace Context propagation.
type TracingConfig struct {
	ServiceName        string
	ServiceVersion     string
	Environment        string
	OTLPHTTPEndpoint   string
	OTLPHeaders        map[string]string
	AllowInsecureHTTP  bool
	ExportTimeout      time.Duration
	TraceSamplingRatio *float64
}

// TracingRuntime owns a provider and propagator without requiring global state.
// The composition root may install them globally when third-party libraries
// need the OpenTelemetry global API.
type TracingRuntime struct {
	provider   trace.TracerProvider
	propagator propagation.TextMapPropagator
	enabled    bool
	shutdown   func(context.Context) error

	shutdownOnce sync.Once
	shutdownErr  error
}

// NewTracingRuntime creates an OTLP/HTTP-backed tracing runtime. Invalid or
// unsafe exporter configuration fails closed. No endpoint performs no network
// activity and returns an operational no-op runtime.
func NewTracingRuntime(ctx context.Context, config TracingConfig) (*TracingRuntime, error) {
	propagator := propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
	)
	headers, err := validateOTLPHeaders(config.OTLPHeaders)
	if err != nil {
		return nil, err
	}
	timeout := config.ExportTimeout
	if timeout == 0 {
		timeout = defaultExportTimeout
	}
	if timeout < 0 {
		return nil, errors.New("observability: export timeout must be positive")
	}
	sampler, err := tracingSampler(config.TraceSamplingRatio)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimSpace(config.OTLPHTTPEndpoint)
	if endpoint == "" {
		return &TracingRuntime{
			provider:   noop.NewTracerProvider(),
			propagator: propagator,
			shutdown:   func(context.Context) error { return nil },
		}, nil
	}

	parsedEndpoint, err := validateOTLPEndpoint(endpoint, config.AllowInsecureHTTP)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	exporterOptions := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(parsedEndpoint.String()),
		otlptracehttp.WithTimeout(timeout),
		// Always override OTEL_EXPORTER_OTLP_HEADERS. Credentials must enter
		// through the validated ChronoDesk configuration boundary.
		otlptracehttp.WithHeaders(headers),
	}
	if parsedEndpoint.Scheme == "http" {
		exporterOptions = append(exporterOptions, otlptracehttp.WithInsecure())
	}
	exporter, err := otlptracehttp.New(ctx, exporterOptions...)
	if err != nil {
		return nil, fmt.Errorf("observability: initialize OTLP trace exporter: %w", err)
	}

	serviceName := strings.TrimSpace(config.ServiceName)
	if serviceName == "" {
		serviceName = defaultTracingServiceName
	}
	attributes := []attribute.KeyValue{
		attribute.String("service.name", serviceName),
	}
	if serviceVersion := strings.TrimSpace(config.ServiceVersion); serviceVersion != "" {
		attributes = append(attributes, attribute.String("service.version", serviceVersion))
	}
	if environment := strings.TrimSpace(config.Environment); environment != "" {
		attributes = append(
			attributes,
			attribute.String("deployment.environment.name", environment),
		)
	}
	runtimeResource, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(attributes...),
	)
	if err != nil {
		_ = exporter.Shutdown(ctx)
		return nil, fmt.Errorf("observability: construct trace resource: %w", err)
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(runtimeResource),
		sdktrace.WithSampler(sampler),
	)

	return &TracingRuntime{
		provider:   provider,
		propagator: propagator,
		enabled:    true,
		shutdown:   provider.Shutdown,
	}, nil
}

// TracerProvider returns the runtime-owned provider for explicit middleware
// wiring.
func (runtime *TracingRuntime) TracerProvider() trace.TracerProvider {
	if runtime == nil || runtime.provider == nil {
		return noop.NewTracerProvider()
	}
	return runtime.provider
}

// Propagator returns the W3C Trace Context propagator. Arbitrary inbound
// Baggage is intentionally not promoted into trusted request context.
func (runtime *TracingRuntime) Propagator() propagation.TextMapPropagator {
	if runtime == nil || runtime.propagator == nil {
		return propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
		)
	}
	return runtime.propagator
}

// Enabled reports whether spans are configured for OTLP export.
func (runtime *TracingRuntime) Enabled() bool {
	return runtime != nil && runtime.enabled
}

// InstallGlobals installs this runtime for dependencies that use the
// OpenTelemetry global API. The returned function restores the prior global
// provider and propagator; application middleware can avoid global state by
// using TracerProvider and Propagator directly.
func (runtime *TracingRuntime) InstallGlobals() func() {
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(runtime.TracerProvider())
	otel.SetTextMapPropagator(runtime.Propagator())
	return func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	}
}

// Shutdown flushes and closes the exporter at most once.
func (runtime *TracingRuntime) Shutdown(ctx context.Context) error {
	if runtime == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runtime.shutdownOnce.Do(func() {
		if runtime.shutdown != nil {
			runtime.shutdownErr = runtime.shutdown(ctx)
		}
	})
	return runtime.shutdownErr
}

func validateOTLPEndpoint(endpoint string, allowInsecureHTTP bool) (*url.URL, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("observability: invalid OTLP endpoint: %w", err)
	}
	if parsed.Host == "" {
		return nil, errors.New("observability: OTLP endpoint requires an absolute host")
	}
	if parsed.User != nil {
		return nil, errors.New("observability: OTLP endpoint must not contain credentials")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("observability: OTLP endpoint must not contain query or fragment")
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		if !allowInsecureHTTP {
			return nil, errors.New("observability: insecure OTLP HTTP requires explicit opt-in")
		}
	default:
		return nil, errors.New("observability: OTLP endpoint scheme must be https or http")
	}
	return parsed, nil
}

func validateOTLPHeaders(input map[string]string) (map[string]string, error) {
	if len(input) == 0 {
		return nil, nil
	}
	output := make(map[string]string, len(input))
	for rawKey, rawValue := range input {
		key := strings.TrimSpace(rawKey)
		if !isHTTPHeaderToken(key) {
			return nil, errors.New("observability: invalid OTLP header name")
		}
		if !isSafeHTTPHeaderValue(rawValue) {
			return nil, errors.New("observability: invalid OTLP header value")
		}
		output[key] = rawValue
	}
	return output, nil
}

func isHTTPHeaderToken(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		current := value[index]
		if current >= 'a' && current <= 'z' ||
			current >= 'A' && current <= 'Z' ||
			current >= '0' && current <= '9' {
			continue
		}
		switch current {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		default:
			return false
		}
	}
	return true
}

func isSafeHTTPHeaderValue(value string) bool {
	for index := 0; index < len(value); index++ {
		current := value[index]
		// Reject every ASCII control character, including horizontal tab,
		// rather than relying only on CR/LF checks at this credential-bearing
		// boundary.
		if current < 0x20 || current == 0x7f {
			return false
		}
	}
	return true
}

func tracingSampler(ratio *float64) (sdktrace.Sampler, error) {
	if ratio == nil {
		return sdktrace.ParentBased(sdktrace.AlwaysSample()), nil
	}
	if *ratio < 0 || *ratio > 1 {
		return nil, errors.New("observability: trace sampling ratio must be between zero and one")
	}
	return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(*ratio)), nil
}
