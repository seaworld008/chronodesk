package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLogHandlerFailureUsesOnlySafeStructuredMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var output bytes.Buffer
	originalLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(originalLogger) })

	router := gin.New()
	router.GET("/tickets/:id", func(c *gin.Context) {
		logHandlerFailure(c, "ticket.get", errors.New("password=never-log-this"))
		c.Status(http.StatusInternalServerError)
	})
	router.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/tickets/42?access_token=also-never-log", nil),
	)

	entry := parseSingleStructuredLogEntry(t, output.String())
	assertLogField(t, entry, "operation", "ticket.get")
	assertLogField(t, entry, "method", http.MethodGet)
	assertLogField(t, entry, "route", matchedHandlerRoute)
	assertLogField(t, entry, "failure_category", handlerFailureCategory)
	if _, containsRawError := entry["error"]; containsRawError {
		t.Fatalf("日志不得包含原始 error 字段: %v", entry)
	}
	for _, secret := range []string{"never-log-this", "also-never-log"} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("日志泄漏了不可信内容 %q: %s", secret, output.String())
		}
	}
}

func TestLogHandlerFailureRejectsUntrustedRouteMethodOperationAndError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var output bytes.Buffer
	originalLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(originalLogger) })

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = &http.Request{
		Method: "POST\r\nforged-method",
		URL: &url.URL{
			Path:     "/unmatched\r\nforged-route",
			RawQuery: "token=private-query-value",
		},
	}
	longUserValue := "private-error-value\r\nforged-log\x00" + strings.Repeat("长", 2048)
	logHandlerFailure(context, "ticket.get\r\nforged-operation", errors.New(longUserValue))

	rawEntry := output.String()
	if strings.Count(rawEntry, "\n") != 1 {
		t.Fatalf("日志必须只生成一个物理行: %q", rawEntry)
	}
	entry := parseSingleStructuredLogEntry(t, rawEntry)
	assertLogField(t, entry, "operation", unknownHandlerOperation)
	assertLogField(t, entry, "method", unknownHandlerMethod)
	assertLogField(t, entry, "route", unmatchedHandlerRoute)
	assertLogField(t, entry, "failure_category", handlerFailureCategory)
	for _, forbidden := range []string{
		"forged-method",
		"forged-route",
		"private-query-value",
		"private-error-value",
		"forged-log",
		strings.Repeat("长", 32),
	} {
		if strings.Contains(rawEntry, forbidden) {
			t.Fatalf("日志包含了不可信内容 %q: %q", forbidden, rawEntry)
		}
	}
}

func TestSafeHandlerOperationAndMethodUseWhitelists(t *testing.T) {
	for _, testCase := range []struct {
		name string
		got  string
		want string
	}{
		{name: "valid method", got: http.MethodPatch, want: http.MethodPatch},
		{name: "control character", got: "GET\x00", want: unknownHandlerMethod},
		{name: "extension method", got: "PROPFIND", want: unknownHandlerMethod},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := safeHandlerMethod(testCase.got); got != testCase.want {
				t.Fatalf("safeHandlerMethod(%q) = %q, want %q", testCase.got, got, testCase.want)
			}
		})
	}

	for _, testCase := range []struct {
		name string
		got  string
		want string
	}{
		{name: "valid operation", got: "ticket_content.create", want: "ticket_content.create"},
		{name: "single segment", got: "ticket", want: unknownHandlerOperation},
		{name: "control character", got: "ticket.get\nforged", want: unknownHandlerOperation},
		{name: "oversized input", got: strings.Repeat("a", 1024) + ".get", want: unknownHandlerOperation},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := safeHandlerOperation(testCase.got); got != testCase.want {
				t.Fatalf("safeHandlerOperation(%q) = %q, want %q", testCase.got, got, testCase.want)
			}
		})
	}
}

func parseSingleStructuredLogEntry(t *testing.T, raw string) map[string]any {
	t.Helper()
	if strings.Count(raw, "\n") != 1 {
		t.Fatalf("日志必须是单行 JSON: %q", raw)
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		t.Fatalf("日志不是有效 JSON: %v; %q", err, raw)
	}
	return entry
}

func assertLogField(t *testing.T, entry map[string]any, field, want string) {
	t.Helper()
	if got, ok := entry[field].(string); !ok || got != want {
		t.Fatalf("日志字段 %s = %#v, want %q; entry=%v", field, entry[field], want, entry)
	}
}
