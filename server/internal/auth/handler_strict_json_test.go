package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAuthFailureMappingsPublishContractedRuntimeStatuses(t *testing.T) {
	for _, test := range []struct {
		name       string
		err        error
		wantStatus int
	}{
		{
			name:       "locked login is forbidden",
			err:        ErrAccountLocked,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "unverified login is forbidden",
			err:        ErrEmailNotVerified,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "login attempt limit",
			err:        errors.New("too many login attempts"),
			wantStatus: http.StatusTooManyRequests,
		},
		{
			name:       "login dependency failure",
			err:        errors.New("database unavailable"),
			wantStatus: http.StatusServiceUnavailable,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, message := loginFailureHTTPResponse(test.err)
			if status != test.wantStatus || message == "" {
				t.Fatalf(
					"login failure = (%d, %q), want status %d",
					status,
					message,
					test.wantStatus,
				)
			}
		})
	}

	for _, test := range []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "refresh deadline",
			err:        context.DeadlineExceeded,
			wantStatus: http.StatusRequestTimeout,
			wantCode:   "request_timeout",
		},
		{
			name:       "refresh dependency failure",
			err:        errors.New("token store unavailable"),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "refresh_failed",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, code, message := refreshFailureHTTPResponse(test.err)
			if status != test.wantStatus ||
				code != test.wantCode ||
				message == "" {
				t.Fatalf(
					"refresh failure = (%d, %q, %q), want (%d, %q)",
					status,
					code,
					message,
					test.wantStatus,
					test.wantCode,
				)
			}
			payload, err := json.Marshal(ErrorResponse{
				Error:   code,
				Message: message,
				Code:    code,
			})
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(payload, &fields); err != nil {
				t.Fatal(err)
			}
			for _, required := range []string{"error", "message", "code"} {
				if _, ok := fields[required]; !ok {
					t.Errorf("refresh error is missing %q: %s", required, payload)
				}
			}
			if len(fields) != 3 {
				t.Errorf("refresh error has unexpected fields: %s", payload)
			}
		})
	}
}

func TestLogoutHandlersPublishClosedSuccessEnvelopesAndClearCookie(t *testing.T) {
	_, _, handler := setupSessionRevocationTest(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/logout", func(c *gin.Context) {
		handler.Logout(NewGinHTTPContext(c))
	})
	router.POST("/logout-all", func(c *gin.Context) {
		c.Set("user_id", uint(42))
		handler.LogoutAll(NewGinHTTPContext(c))
	})

	for _, test := range []struct {
		path        string
		wantMessage string
	}{
		{path: "/logout", wantMessage: "退出登录成功"},
		{path: "/logout-all", wantMessage: "已从所有设备退出登录"},
	} {
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 2 ||
				body["success"] != true ||
				body["message"] != test.wantMessage {
				t.Fatalf("logout response = %v", body)
			}
			cookie := response.Header().Get("Set-Cookie")
			if !strings.Contains(cookie, trustedDeviceCookieName+"=") ||
				!strings.Contains(cookie, "Path="+trustedDeviceCookiePath) ||
				!strings.Contains(cookie, "HttpOnly") ||
				!strings.Contains(cookie, "SameSite=Strict") {
				t.Fatalf("trusted-device clearing cookie = %q", cookie)
			}
		})
	}
}

func TestHumanAuthHandlersRejectUnknownAndTrailingJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewAuthHandler(nil, nil)
	tests := []struct {
		name    string
		handle  func(HTTPContext)
		setup   func(*gin.Context)
		payload string
	}{
		{
			name:    "login rejects historical role",
			handle:  handler.Login,
			payload: `{"email":"user@example.test","password":"secret","role":"admin"}`,
		},
		{
			name:    "login rejects trailing JSON",
			handle:  handler.Login,
			payload: `{"email":"user@example.test","password":"secret"} {}`,
		},
		{
			name:    "refresh rejects permissions",
			handle:  handler.RefreshToken,
			payload: `{"refresh_token":"token","permissions":["admin"]}`,
		},
		{
			name:    "refresh rejects trailing JSON",
			handle:  handler.RefreshToken,
			payload: `{"refresh_token":"token"} {}`,
		},
		{
			name:   "profile rejects historical role",
			handle: handler.UpdateProfile,
			setup:  setStrictJSONAuthenticatedHuman,
			payload: `{
				"first_name":"Chrono",
				"role":"admin"
			}`,
		},
		{
			name:   "profile rejects permissions",
			handle: handler.UpdateProfile,
			setup:  setStrictJSONAuthenticatedHuman,
			payload: `{
				"first_name":"Chrono",
				"permissions":["admin"]
			}`,
		},
		{
			name:    "profile rejects trailing JSON",
			handle:  handler.UpdateProfile,
			setup:   setStrictJSONAuthenticatedHuman,
			payload: `{"first_name":"Chrono"} {}`,
		},
		{
			name:    "logout rejects unknown fields",
			handle:  handler.Logout,
			payload: `{"permissions":["admin"]}`,
		},
		{
			name:    "logout rejects trailing JSON",
			handle:  handler.Logout,
			payload: `{"refresh_token":"token"} {}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.POST("/", func(c *gin.Context) {
				if test.setup != nil {
					test.setup(c)
				}
				test.handle(NewGinHTTPContext(c))
			})
			request := httptest.NewRequest(
				http.MethodPost,
				"/",
				strings.NewReader(test.payload),
			)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf(
					"status = %d, want %d; body=%s",
					response.Code,
					http.StatusBadRequest,
					response.Body.String(),
				)
			}
			assertClosedRuntimeAuthError(t, response.Body.Bytes(), "invalid_request")
		})
	}
}

func TestUpdateProfileReturnsStableChineseValidationErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewAuthHandler(&AuthService{}, nil)
	tests := []struct {
		name     string
		payload  string
		wantCode string
	}{
		{
			name:     "name",
			payload:  `{"first_name":"` + strings.Repeat("名", 51) + `"}`,
			wantCode: "invalid_profile_name",
		},
		{
			name:     "timezone",
			payload:  `{"timezone":"Mars/Olympus"}`,
			wantCode: "invalid_profile_timezone",
		},
		{
			name:     "language",
			payload:  `{"language":"en"}`,
			wantCode: "unsupported_profile_language",
		},
		{
			name:     "phone",
			payload:  `{"phone_number":"13800138000"}`,
			wantCode: "invalid_profile_phone",
		},
		{
			name:     "avatar",
			payload:  `{"avatar":"https://example.test/avatar.png"}`,
			wantCode: "invalid_profile_avatar",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.PUT("/", func(c *gin.Context) {
				setStrictJSONAuthenticatedHuman(c)
				handler.UpdateProfile(NewGinHTTPContext(c))
			})
			request := httptest.NewRequest(
				http.MethodPut,
				"/",
				strings.NewReader(test.payload),
			)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
			}
			var body ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error != test.wantCode ||
				body.Code != test.wantCode ||
				!strings.ContainsAny(body.Message, "的一是个效区名机文码持当仅最") {
				t.Fatalf("validation response = %+v", body)
			}
		})
	}
}

func setStrictJSONAuthenticatedHuman(c *gin.Context) {
	c.Set("user_id", uint(42))
	c.Set("platform_role", PlatformRoleMember)
}

func assertClosedRuntimeAuthError(
	t *testing.T,
	payload []byte,
	wantError string,
) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("decode auth error: %v; body=%s", err, payload)
	}
	if len(fields) != 2 {
		t.Fatalf("auth error fields = %v, want error/message", fields)
	}
	var gotError string
	if err := json.Unmarshal(fields["error"], &gotError); err != nil {
		t.Fatalf("decode error code: %v", err)
	}
	if gotError != wantError {
		t.Fatalf("error = %q, want %q", gotError, wantError)
	}
	if _, ok := fields["message"]; !ok {
		t.Fatal("auth error message is missing")
	}
}
