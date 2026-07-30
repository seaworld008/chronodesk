package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/observability"
)

// HTTPMetricsMiddleware records only bounded HTTP method, matched route
// template, and status class labels. It never falls back to Request.URL.Path.
func HTTPMetricsMiddleware(metrics *observability.HTTPMetrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		if metrics == nil || c.Request == nil {
			c.Next()
			return
		}
		finish := metrics.BeginRequest(boundedHTTPMethod(c.Request.Method))
		defer func() {
			recovered := recover()
			statusCode := c.Writer.Status()
			if recovered != nil && statusCode < http.StatusInternalServerError {
				statusCode = http.StatusInternalServerError
			}
			finish(matchedRouteTemplate(c), statusCode)
			if recovered != nil {
				panic(recovered)
			}
		}()
		c.Next()
	}
}

// PrometheusHandler adapts the runtime's private registry for an explicit
// /metrics Gin route. A missing runtime returns 503 instead of exposing the
// process-global Prometheus registry.
func PrometheusHandler(metrics *observability.HTTPMetrics) gin.HandlerFunc {
	if metrics == nil {
		return func(c *gin.Context) {
			c.String(http.StatusServiceUnavailable, "metrics unavailable")
		}
	}
	return gin.WrapH(metrics.Handler())
}

func matchedRouteTemplate(c *gin.Context) string {
	if c == nil {
		return "unmatched"
	}
	route := strings.TrimSpace(c.FullPath())
	if route == "" {
		return "unmatched"
	}
	if len(route) > 256 || !strings.HasPrefix(route, "/") ||
		strings.ContainsAny(route, "\r\n?#") {
		return "other"
	}
	return route
}

func boundedHTTPMethod(method string) string {
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
