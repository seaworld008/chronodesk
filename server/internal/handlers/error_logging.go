package handlers

import (
	"context"
	"log/slog"

	"github.com/gin-gonic/gin"
)

// logHandlerFailure keeps implementation details in structured server logs.
// Client responses must use a stable public code and Chinese message instead of
// reusing the error text passed here.
func logHandlerFailure(c *gin.Context, operation string, err error) {
	if err == nil {
		return
	}

	route := c.FullPath()
	method := ""
	logContext := context.Background()
	if c.Request != nil {
		method = c.Request.Method
		logContext = c.Request.Context()
		if route == "" && c.Request.URL != nil {
			route = c.Request.URL.EscapedPath()
		}
	}

	slog.ErrorContext(
		logContext,
		"REST 操作失败",
		slog.String("operation", operation),
		slog.String("method", method),
		slog.String("route", route),
		slog.Any("error", err),
	)
}
