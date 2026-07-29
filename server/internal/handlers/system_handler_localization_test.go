package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSystemHandlerInvalidRequestMessageIsChinese(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &SystemHandler{}
	router := gin.New()
	router.POST("/configs", handler.CreateConfig)

	request := httptest.NewRequest(http.MethodPost, "/configs", bytes.NewBufferString(`{"key":`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Error != "invalid_request" {
		t.Fatalf("error code = %q", payload.Error)
	}
	if payload.Message != "请求体格式无效" {
		t.Fatalf("message = %q", payload.Message)
	}
}
