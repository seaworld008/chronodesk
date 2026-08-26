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

type profileValidationRepository struct{}

func (profileValidationRepository) Create(context.Context, *UserProfile) error {
	return nil
}

func (profileValidationRepository) GetByUserID(
	_ context.Context,
	userID uint,
) (*UserProfile, error) {
	return &UserProfile{UserID: userID}, nil
}

func (profileValidationRepository) Patch(
	_ context.Context,
	_ uint,
	patch ProfilePatch,
) error {
	if patch.Avatar != nil && *patch.Avatar != "" {
		return ErrInvalidProfileAvatar
	}
	return nil
}

func (profileValidationRepository) Delete(context.Context, uint) error {
	return nil
}

func TestUpdateProfileRequestPreservesOmittedAndExplicitEmptyFields(t *testing.T) {
	var omitted UpdateProfileRequest
	if err := json.Unmarshal([]byte(`{}`), &omitted); err != nil {
		t.Fatal(err)
	}
	if omitted.FirstName != nil ||
		omitted.LastName != nil ||
		omitted.PhoneNumber != nil ||
		omitted.Avatar != nil ||
		omitted.Timezone != nil ||
		omitted.Language != nil {
		t.Fatalf("omitted profile fields became present: %+v", omitted)
	}

	var explicit UpdateProfileRequest
	if err := json.Unmarshal([]byte(`{
		"first_name":"",
		"last_name":"",
		"phone_number":"",
		"avatar":"",
		"timezone":"",
		"language":""
	}`), &explicit); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]*string{
		"first_name":   explicit.FirstName,
		"last_name":    explicit.LastName,
		"phone_number": explicit.PhoneNumber,
		"avatar":       explicit.Avatar,
		"timezone":     explicit.Timezone,
		"language":     explicit.Language,
	} {
		if value == nil || *value != "" {
			t.Errorf("%s explicit empty value = %v", name, value)
		}
	}
}

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
	}{
		{
			name:       "invalid MFA step-up preserves the authenticated session",
			err:        ErrInvalidOTP,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "expired MFA step-up preserves the authenticated session",
			err:        ErrOTPExpired,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "MFA dependency failure",
			err:        errors.New("OTP store unavailable"),
			wantStatus: http.StatusInternalServerError,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, message := verifyOTPFailureHTTPResponse(test.err)
			if status != test.wantStatus || message == "" {
				t.Fatalf(
					"verify OTP failure = (%d, %q), want status %d",
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

func TestLogoutHandlersPublishClosedSuccessEnvelopesAndPreserveTrustSemantics(
	t *testing.T,
) {
	repository, manager, handler := setupSessionRevocationTest(t)
	_, refreshToken := issueSessionTokens(
		t,
		repository,
		manager,
		42,
		PlatformRolePlatformAdmin,
		"logout-envelope-session",
	)
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
		clearsTrust bool
	}{
		{
			path:        "/logout",
			wantMessage: "退出登录成功",
			clearsTrust: false,
		},
		{
			path:        "/logout-all",
			wantMessage: "已从所有设备退出登录",
			clearsTrust: true,
		},
	} {
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, nil)
			request.AddCookie(&http.Cookie{
				Name:  refreshTokenCookieName,
				Value: refreshToken,
				Path:  refreshTokenCookiePath,
			})
			if test.path == "/logout" {
				request.Header.Set("Origin", testBrowserOrigin)
			}
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
			refreshCleared := false
			trustCleared := false
			for _, cookie := range response.Result().Cookies() {
				if cookie.MaxAge >= 0 ||
					!cookie.HttpOnly ||
					cookie.SameSite != http.SameSiteStrictMode {
					continue
				}
				switch cookie.Name {
				case refreshTokenCookieName:
					refreshCleared =
						cookie.Path == refreshTokenCookiePath
				case trustedDeviceCookieName:
					trustCleared =
						cookie.Path == trustedDeviceCookiePath
				}
			}
			if !refreshCleared {
				t.Fatalf(
					"refresh clearing cookie missing: %q",
					response.Header().Values("Set-Cookie"),
				)
			}
			if trustCleared != test.clearsTrust {
				t.Fatalf(
					"trusted-device clearing cookies = %q",
					response.Header().Values("Set-Cookie"),
				)
			}
		})
	}
}

func TestHumanAuthHandlersRejectUnknownAndTrailingJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewAuthHandler(
		nil,
		nil,
		WithAllowedBrowserOrigin(testBrowserOrigin),
	)
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
			name:   "login rejects email beyond persistence limit",
			handle: handler.Login,
			payload: `{"email":"` +
				strings.Repeat("a", 88) +
				`@example.test","password":"secret"}`,
		},
		{
			name:   "login rejects device name beyond persistence limit",
			handle: handler.Login,
			payload: `{"email":"user@example.test","password":"secret",` +
				`"device_name":"` + strings.Repeat("设", 101) + `"}`,
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
			name:    "verify email rejects query-shaped compatibility fields",
			handle:  handler.VerifyEmail,
			payload: `{"token":"token","token_query":"legacy"}`,
		},
		{
			name:    "verify email rejects trailing JSON",
			handle:  handler.VerifyEmail,
			payload: `{"token":"token"} {}`,
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
			request.Header.Set("Origin", testBrowserOrigin)
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

func TestVerifyEmailRejectsLegacyHTTPQueryToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewAuthHandler(nil, nil)
	router := gin.New()
	router.POST("/verify-email", func(c *gin.Context) {
		handler.VerifyEmail(NewGinHTTPContext(c))
	})

	request := httptest.NewRequest(
		http.MethodPost,
		"/verify-email?token=must-not-enter-http-query",
		nil,
	)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf(
			"query-only verification status = %d; body=%s",
			response.Code,
			response.Body,
		)
	}
	assertClosedRuntimeAuthError(
		t,
		response.Body.Bytes(),
		"invalid_request",
	)
}

func TestAuthenticationJSONBodyLimitRejectsKnownChunkedAndUnderstatedBodies(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	payload := `{"token":"` +
		strings.Repeat("x", int(maxAuthenticationJSONBodyBytes)) +
		`"}`
	for _, testCase := range []struct {
		name             string
		configureRequest func(*http.Request)
	}{
		{
			name: "known content length",
		},
		{
			name: "chunked body",
			configureRequest: func(request *http.Request) {
				request.ContentLength = -1
				request.TransferEncoding = []string{"chunked"}
			},
		},
		{
			name: "understated content length",
			configureRequest: func(request *http.Request) {
				request.ContentLength = 16
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			handler := NewAuthHandler(nil, nil)
			router := gin.New()
			router.POST("/", func(c *gin.Context) {
				handler.VerifyEmail(NewGinHTTPContext(c))
			})
			request := httptest.NewRequest(
				http.MethodPost,
				"/",
				strings.NewReader(payload),
			)
			request.Header.Set("Content-Type", "application/json")
			if testCase.configureRequest != nil {
				testCase.configureRequest(request)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf(
					"status = %d, want 413; body=%s",
					response.Code,
					response.Body.String(),
				)
			}
			var body ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code != "request_too_large" ||
				body.Error != "request_too_large" {
				t.Fatalf("oversized body response = %+v", body)
			}
		})
	}
}

func TestUpdateProfileReturnsStableChineseValidationErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewAuthHandler(&AuthService{
		profileRepo: profileValidationRepository{},
	}, nil)
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
			payload:  `{"language":"fr"}`,
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
