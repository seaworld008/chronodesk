package observability

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"
)

func TestHTTPMetricsUsesPrivateRegistryAndIdempotentCompletion(t *testing.T) {
	t.Parallel()

	metrics, err := NewHTTPMetrics(HTTPMetricsConfig{
		Namespace:       "chronodesk_test",
		DurationBuckets: []float64{0.01, 0.1, 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	finish := metrics.BeginRequest(http.MethodGet)
	finish("/api/v2/projects/:projectKey/tickets/:ticketID", http.StatusOK)
	finish("/must/not/count/twice", http.StatusInternalServerError)

	families, err := metrics.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	requests := metricFamilyByName(
		families,
		"chronodesk_test_http_server_requests_total",
	)
	if requests == nil || len(requests.Metric) != 1 {
		t.Fatalf("request metric family = %+v", requests)
	}
	if got := requests.Metric[0].Counter.GetValue(); got != 1 {
		t.Fatalf("request count = %v, want 1", got)
	}
	labels := labelsByName(requests.Metric[0])
	if labels["route"] != "/api/v2/projects/:projectKey/tickets/:ticketID" ||
		labels["method"] != http.MethodGet ||
		labels["status_class"] != "2xx" {
		t.Fatalf("unexpected labels: %+v", labels)
	}
	if metricFamilyByName(families, "go_gc_duration_seconds") != nil {
		t.Fatal("private registry unexpectedly contains global Go collectors")
	}

	inFlight := metricFamilyByName(
		families,
		"chronodesk_test_http_server_requests_in_flight",
	)
	if inFlight == nil || inFlight.Metric[0].Gauge.GetValue() != 0 {
		t.Fatalf("in-flight metric did not return to zero: %+v", inFlight)
	}
}

func TestHTTPMetricsHandlerExposesOnlyPrivateRegistry(t *testing.T) {
	t.Parallel()

	metrics, err := NewHTTPMetrics(HTTPMetricsConfig{Namespace: "chronodesk"})
	if err != nil {
		t.Fatal(err)
	}
	metrics.BeginRequest(http.MethodPost)("/tickets", http.StatusCreated)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metrics.Handler().ServeHTTP(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.StatusCode, body)
	}
	text := string(body)
	if !strings.Contains(text, "chronodesk_http_server_requests_total") {
		t.Fatalf("missing HTTP metric:\n%s", text)
	}
	if strings.Contains(text, "go_gc_duration_seconds") {
		t.Fatalf("handler exposed global collector:\n%s", text)
	}
}

func TestHTTPMetricsRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	tests := []HTTPMetricsConfig{
		{Namespace: "invalid-name"},
		{Namespace: "valid", DurationBuckets: []float64{-1}},
		{Namespace: "valid", DurationBuckets: []float64{1, 0.5}},
		{Namespace: "valid", DurationBuckets: []float64{1, 1}},
	}
	for _, config := range tests {
		if metrics, err := NewHTTPMetrics(config); err == nil {
			t.Fatalf("invalid config accepted: %+v (%+v)", config, metrics)
		}
	}
}

func metricFamilyByName(
	families []*dto.MetricFamily,
	name string,
) *dto.MetricFamily {
	for _, family := range families {
		if family.GetName() == name {
			return family
		}
	}
	return nil
}

func labelsByName(metric *dto.Metric) map[string]string {
	labels := make(map[string]string, len(metric.Label))
	for _, label := range metric.Label {
		labels[label.GetName()] = label.GetValue()
	}
	return labels
}
