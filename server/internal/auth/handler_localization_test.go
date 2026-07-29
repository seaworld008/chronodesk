package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAuthHandlerLoginValidationMessagesAreChinese(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewAuthHandler(nil, nil)
	router := gin.New()
	router.POST("/login", func(c *gin.Context) {
		handler.Login(NewGinHTTPContext(c))
	})

	tests := []struct {
		name    string
		body    string
		message string
		field   string
	}{
		{
			name:    "invalid request body",
			body:    `{"email":`,
			message: "请求格式无效",
			field:   "message",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/login", bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
			}
			var payload map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if payload[test.field] != test.message {
				t.Fatalf("%s = %q, want %q", test.field, payload[test.field], test.message)
			}
		})
	}
}

func TestAuthHandlerRegisterValidationMessagesAreChinese(t *testing.T) {
	handler := &AuthHandler{}
	valid := RegisterRequest{
		Username:        "valid-user",
		Email:           "valid@example.com",
		Password:        "valid-pass-123",
		ConfirmPassword: "valid-pass-123",
	}

	tests := []struct {
		name    string
		mutate  func(*RegisterRequest)
		message string
	}{
		{
			name:    "missing username",
			mutate:  func(req *RegisterRequest) { req.Username = "" },
			message: "请输入用户名",
		},
		{
			name:    "invalid email",
			mutate:  func(req *RegisterRequest) { req.Email = "invalid" },
			message: "邮箱格式无效",
		},
		{
			name:    "short password",
			mutate:  func(req *RegisterRequest) { req.Password = "short" },
			message: "密码至少需要 8 个字符",
		},
		{
			name:    "password mismatch",
			mutate:  func(req *RegisterRequest) { req.ConfirmPassword = "different-pass" },
			message: "两次输入的密码不一致",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := valid
			test.mutate(&req)
			err := handler.validateRegisterRequest(&req)
			if err == nil || err.Error() != test.message {
				t.Fatalf("validation error = %v, want %q", err, test.message)
			}
		})
	}
}
