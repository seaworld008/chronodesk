package observability

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var metricNamespacePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// HTTPMetricsConfig controls an isolated HTTP collector registry. No process,
// Go runtime, or default-global collectors are registered implicitly.
type HTTPMetricsConfig struct {
	Namespace       string
	DurationBuckets []float64
}

// HTTPMetrics contains bounded-cardinality server request metrics registered
// in a private Prometheus registry.
type HTTPMetrics struct {
	registry RegistererGatherer
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inFlight *prometheus.GaugeVec
}

// RegistererGatherer is the subset implemented by prometheus.Registry. It is
// exported so the composition root can register additional bounded collectors
// in the same private registry without using Prometheus globals.
type RegistererGatherer interface {
	prometheus.Registerer
	prometheus.Gatherer
}

// NewHTTPMetrics constructs and registers all HTTP metrics atomically in a new
// private registry. Invalid metric configuration returns an error.
func NewHTTPMetrics(config HTTPMetricsConfig) (*HTTPMetrics, error) {
	namespace := strings.TrimSpace(config.Namespace)
	if namespace == "" {
		namespace = defaultTracingServiceName
	}
	if !metricNamespacePattern.MatchString(namespace) {
		return nil, errors.New("observability: invalid Prometheus namespace")
	}
	buckets := config.DurationBuckets
	if len(buckets) == 0 {
		buckets = prometheus.DefBuckets
	}
	if err := validateHistogramBuckets(buckets); err != nil {
		return nil, err
	}

	registry := prometheus.NewRegistry()
	metrics := &HTTPMetrics{
		registry: registry,
		requests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "http",
				Name:      "server_requests_total",
				Help:      "Total HTTP server requests by method, matched route template, and status class.",
			},
			[]string{"method", "route", "status_class"},
		),
		duration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: "http",
				Name:      "server_request_duration_seconds",
				Help:      "HTTP server request duration by method, matched route template, and status class.",
				Buckets:   append([]float64(nil), buckets...),
			},
			[]string{"method", "route", "status_class"},
		),
		inFlight: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "http",
				Name:      "server_requests_in_flight",
				Help:      "Current HTTP server requests by bounded HTTP method.",
			},
			[]string{"method"},
		),
	}
	for _, collector := range []prometheus.Collector{
		metrics.requests,
		metrics.duration,
		metrics.inFlight,
	} {
		if err := registry.Register(collector); err != nil {
			return nil, fmt.Errorf("observability: register Prometheus collector: %w", err)
		}
	}
	return metrics, nil
}

// Registry returns the private registry through its register-and-gather
// contract. It never returns prometheus.DefaultRegisterer.
func (metrics *HTTPMetrics) Registry() RegistererGatherer {
	if metrics == nil {
		return nil
	}
	return metrics.registry
}

// Handler exposes only this HTTPMetrics instance's private registry.
func (metrics *HTTPMetrics) Handler() http.Handler {
	if metrics == nil || metrics.registry == nil {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			http.Error(writer, "metrics unavailable", http.StatusServiceUnavailable)
		})
	}
	return promhttp.HandlerFor(metrics.registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
		ErrorHandling:     promhttp.HTTPErrorOnError,
	})
}

// BeginRequest increments the bounded in-flight gauge and returns an idempotent
// completion function. The caller must pass a matched route template or one of
// the constant fallback labels; raw request URLs are rejected into "other".
func (metrics *HTTPMetrics) BeginRequest(method string) func(route string, statusCode int) {
	if metrics == nil {
		return func(string, int) {}
	}
	method = normalizeHTTPMethod(method)
	startedAt := time.Now()
	metrics.inFlight.WithLabelValues(method).Inc()
	var once sync.Once
	return func(route string, statusCode int) {
		once.Do(func() {
			metrics.inFlight.WithLabelValues(method).Dec()
			route = normalizeRouteTemplate(route)
			statusClass := normalizeStatusClass(statusCode)
			metrics.requests.WithLabelValues(method, route, statusClass).Inc()
			metrics.duration.WithLabelValues(method, route, statusClass).
				Observe(time.Since(startedAt).Seconds())
		})
	}
}

func validateHistogramBuckets(buckets []float64) error {
	var previous float64
	for index, bucket := range buckets {
		if bucket <= 0 || math.IsNaN(bucket) || math.IsInf(bucket, 0) {
			return errors.New("observability: histogram buckets must be finite and positive")
		}
		if index > 0 && bucket <= previous {
			return errors.New("observability: histogram buckets must be strictly increasing")
		}
		previous = bucket
	}
	return nil
}

func normalizeHTTPMethod(method string) string {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodConnect:
		return http.MethodConnect
	case http.MethodDelete:
		return http.MethodDelete
	case http.MethodGet:
		return http.MethodGet
	case http.MethodHead:
		return http.MethodHead
	case http.MethodOptions:
		return http.MethodOptions
	case http.MethodPatch:
		return http.MethodPatch
	case http.MethodPost:
		return http.MethodPost
	case http.MethodPut:
		return http.MethodPut
	case http.MethodTrace:
		return http.MethodTrace
	default:
		return "OTHER"
	}
}

func normalizeRouteTemplate(route string) string {
	route = strings.TrimSpace(route)
	switch route {
	case "", "unmatched":
		return "unmatched"
	}
	if len(route) > 256 || !strings.HasPrefix(route, "/") ||
		strings.ContainsAny(route, "\r\n?#") {
		return "other"
	}
	return route
}

func normalizeStatusClass(statusCode int) string {
	if statusCode < 100 || statusCode > 599 {
		return "unknown"
	}
	return strconv.Itoa(statusCode/100) + "xx"
}
