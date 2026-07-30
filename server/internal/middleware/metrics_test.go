package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/observability"
)

func TestHTTPMetricsMiddlewareUsesOnlyMatchedRouteTemplates(t *testing.T) {
	gin.SetMode(gin.TestMode)

	metrics, err := observability.NewHTTPMetrics(observability.HTTPMetricsConfig{
		Namespace: "chronodesk_test",
	})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(HTTPMetricsMiddleware(metrics))
	router.GET(
		"/api/v2/projects/:projectKey/tickets/:ticketID",
		func(c *gin.Context) {
			c.Status(http.StatusOK)
		},
	)

	for _, path := range []string{
		"/api/v2/projects/project-alpha/tickets/1",
		"/api/v2/projects/project-beta/tickets/999999",
		"/does-not-exist/secret-one",
		"/does-not-exist/secret-two",
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(
			recorder,
			httptest.NewRequest(http.MethodGet, path, nil),
		)
	}

	families, err := metrics.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	routes := make(map[string]struct{})
	for _, forbidden := range []string{
		"project-alpha",
		"project-beta",
		"999999",
		"secret-one",
		"secret-two",
	} {
		for _, family := range families {
			for _, metric := range family.Metric {
				for _, label := range metric.Label {
					if strings.Contains(label.GetValue(), forbidden) {
						t.Fatalf(
							"metric %q label %q leaked %q in %q",
							family.GetName(),
							label.GetName(),
							forbidden,
							label.GetValue(),
						)
					}
				}
			}
		}
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if label.GetName() == "route" {
					routes[label.GetValue()] = struct{}{}
				}
			}
		}
	}
	wantRoutes := []string{
		"/api/v2/projects/:projectKey/tickets/:ticketID",
		"unmatched",
	}
	if len(routes) != len(wantRoutes) {
		t.Fatalf("route labels = %#v, want only matched template and unmatched", routes)
	}
	for _, route := range wantRoutes {
		if _, exists := routes[route]; !exists {
			t.Fatalf("route labels = %#v, missing %q", routes, route)
		}
	}
}

func TestPrometheusHandlerFailsClosedWithoutRegistry(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	context := testGinContext(
		recorder,
		httptest.NewRequest(http.MethodGet, "/metrics", nil),
	)
	PrometheusHandler(nil)(context)
	if recorder.Code != http.StatusServiceUnavailable {
		body, _ := io.ReadAll(recorder.Result().Body)
		t.Fatalf("status = %d, body = %s", recorder.Code, body)
	}
}

func TestHTTPMetricsMiddlewareRecordsPanicAsFiveHundredClass(t *testing.T) {
	gin.SetMode(gin.TestMode)

	metrics, err := observability.NewHTTPMetrics(observability.HTTPMetricsConfig{
		Namespace: "chronodesk_panic_test",
	})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(HTTPMetricsMiddleware(metrics))
	router.GET("/panic", func(*gin.Context) {
		panic("untrusted-panic")
	})
	router.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/panic", nil),
	)

	recorder := httptest.NewRecorder()
	PrometheusHandler(metrics)(
		testGinContext(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil)),
	)
	body := recorder.Body.String()
	if !strings.Contains(body, `route="/panic",status_class="5xx"} 1`) {
		t.Fatalf("panic was not recorded as 5xx:\n%s", body)
	}
	if strings.Contains(body, "untrusted-panic") {
		t.Fatalf("metrics leaked panic value:\n%s", body)
	}
}

func testGinContext(
	recorder *httptest.ResponseRecorder,
	request *http.Request,
) *gin.Context {
	context, _ := gin.CreateTestContext(recorder)
	context.Request = request
	return context
}
