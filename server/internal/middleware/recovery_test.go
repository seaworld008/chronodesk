package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRecoveryMiddlewareReturnsStableChineseErrorWithoutPanicDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(WrapGinMiddleware(RequestIDMiddleware()))
	router.Use(WrapGinMiddleware(RecoveryMiddleware(&RecoveryConfig{
		Logger:            NewSimpleLogger(nil, LogLevelError),
		EnableStackTrace:  false,
		DisablePrintStack: true,
	})))
	router.GET("/panic", func(*gin.Context) {
		panic("secret-internal-panic-detail")
	})

	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "secret-internal-panic-detail") {
		t.Fatalf("panic details leaked to the response: %s", response.Body.String())
	}
	var body struct {
		Success bool `json:"success"`
		Error   struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Success ||
		body.Error.Code != "internal_error" ||
		body.Error.Message != "服务器内部错误，请稍后重试" ||
		body.Error.RequestID == "" {
		t.Fatalf("unexpected recovery contract: %+v", body)
	}
	if response.Header().Get("X-Request-ID") != body.Error.RequestID {
		t.Fatalf(
			"response request ID = %q, body request ID = %q",
			response.Header().Get("X-Request-ID"),
			body.Error.RequestID,
		)
	}
}

func TestRecoveryMiddlewareDoesNotAppendJSONAfterResponseStarted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(WrapGinMiddleware(RecoveryMiddleware(&RecoveryConfig{
		Logger:            NewSimpleLogger(nil, LogLevelError),
		EnableStackTrace:  false,
		DisablePrintStack: true,
	})))
	router.GET("/partial", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "已提交"})
		panic("after-commit")
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/partial", nil),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("committed status changed to %d", response.Code)
	}
	if response.Body.String() != `{"message":"已提交"}` {
		t.Fatalf("recovery corrupted committed body: %q", response.Body.String())
	}
}
