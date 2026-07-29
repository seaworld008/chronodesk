package handlers

import (
	"context"
	"log/slog"
	"regexp"

	"github.com/gin-gonic/gin"
)

const (
	unknownHandlerMethod    = "unknown"
	unknownHandlerOperation = "handler.unknown"
	unmatchedHandlerRoute   = "unmatched"
	matchedHandlerRoute     = "registered"
	handlerFailureCategory  = "internal_failure"
	maxHandlerOperationSize = 128
)

// Handler operations are implementation-owned identifiers. Keeping this
// grammar narrow protects the log boundary if a future caller accidentally
// supplies a value derived from a request.
var handlerOperationPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// logHandlerFailure keeps implementation details in structured server logs.
// Client responses must use a stable public code and Chinese message instead of
// reusing the error text passed here.
func logHandlerFailure(c *gin.Context, operation string, err error) {
	if err == nil {
		return
	}

	route := unmatchedHandlerRoute
	method := unknownHandlerMethod
	logContext := context.Background()
	if c != nil {
		// FullPath tells us whether Gin matched a registered route. Do not log its
		// value (or fall back to URL path/query): all request-location values are
		// treated as untrusted at this boundary. The implementation-owned
		// operation identifier retains the useful routing dimension.
		if fullPath := c.FullPath(); fullPath != "" {
			route = matchedHandlerRoute
		}
		if c.Request != nil {
			method = safeHandlerMethod(c.Request.Method)
			logContext = c.Request.Context()
		}
	}

	slog.ErrorContext(
		logContext,
		"REST 操作失败",
		slog.String("operation", safeHandlerOperation(operation)),
		slog.String("method", method),
		slog.String("route", route),
		// Error text can contain query values, user input, credentials returned
		// by an upstream service, or SQL details. Keep the log event useful for
		// aggregation without persisting any of that untrusted data.
		slog.String("failure_category", handlerFailureCategory),
	)
}

func safeHandlerMethod(method string) string {
	switch method {
	case "GET":
		return "GET"
	case "HEAD":
		return "HEAD"
	case "OPTIONS":
		return "OPTIONS"
	case "POST":
		return "POST"
	case "PUT":
		return "PUT"
	case "PATCH":
		return "PATCH"
	case "DELETE":
		return "DELETE"
	case "CONNECT":
		return "CONNECT"
	case "TRACE":
		return "TRACE"
	default:
		return unknownHandlerMethod
	}
}

func safeHandlerOperation(operation string) string {
	if len(operation) <= maxHandlerOperationSize && handlerOperationPattern.MatchString(operation) {
		return operation
	}
	return unknownHandlerOperation
}
