package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

const testBrowserOrigin = "https://web.example.test"

func TestRefreshTokenCookieUsesHardenedAttributesAndMatchingClear(t *testing.T) {
	handler := NewAuthHandler(nil, nil, WithSecureAuthCookies(true))
	expiresAt := time.Now().Add(24 * time.Hour)

	setCookie := recordTrustedDeviceCookie(t, nil, func(c HTTPContext) {
		if !handler.setRefreshTokenCookie(
			c,
			"opaque-session-credential",
			expiresAt,
		) {
			t.Fatal("setRefreshTokenCookie rejected a valid lifetime")
		}
	})
	if setCookie.Name != refreshTokenCookieName ||
		setCookie.Path != refreshTokenCookiePath ||
		setCookie.Value == "" {
		t.Fatalf(
			"refresh Cookie identity/path invalid: name=%q path=%q",
			setCookie.Name,
			setCookie.Path,
		)
	}
	if !setCookie.HttpOnly ||
		!setCookie.Secure ||
		setCookie.SameSite != http.SameSiteStrictMode ||
		setCookie.MaxAge <= 0 ||
		!setCookie.Expires.After(time.Now()) {
		t.Fatalf(
			"refresh Cookie attributes invalid: HttpOnly=%v Secure=%v SameSite=%v MaxAge=%d",
			setCookie.HttpOnly,
			setCookie.Secure,
			setCookie.SameSite,
			setCookie.MaxAge,
		)
	}

	clearCookie := recordTrustedDeviceCookie(
		t,
		nil,
		handler.clearRefreshTokenCookie,
	)
	if clearCookie.Name != setCookie.Name ||
		clearCookie.Path != setCookie.Path ||
		clearCookie.Value != "" ||
		clearCookie.MaxAge >= 0 ||
		clearCookie.HttpOnly != setCookie.HttpOnly ||
		clearCookie.Secure != setCookie.Secure ||
		clearCookie.SameSite != setCookie.SameSite {
		t.Fatalf(
			"refresh Cookie clear attributes do not match issuance: name=%q path=%q HttpOnly=%v Secure=%v SameSite=%v MaxAge=%d",
			clearCookie.Name,
			clearCookie.Path,
			clearCookie.HttpOnly,
			clearCookie.Secure,
			clearCookie.SameSite,
			clearCookie.MaxAge,
		)
	}
}

func TestRefreshAndLogoutFailClosedWithoutConfiguredExactOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name    string
		handler *AuthHandler
		origin  string
	}{
		{
			name:    "unconfigured handler",
			handler: NewAuthHandler(nil, nil),
			origin:  testBrowserOrigin,
		},
		{
			name: "missing origin",
			handler: NewAuthHandler(
				nil,
				nil,
				WithAllowedBrowserOrigin(testBrowserOrigin),
			),
		},
		{
			name: "wrong origin despite forwarded host",
			handler: NewAuthHandler(
				nil,
				nil,
				WithAllowedBrowserOrigin(testBrowserOrigin),
			),
			origin: "https://attacker.example.test",
		},
		{
			name: "null origin",
			handler: NewAuthHandler(
				nil,
				nil,
				WithAllowedBrowserOrigin(testBrowserOrigin),
			),
			origin: "null",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.POST("/api/auth/refresh", func(c *gin.Context) {
				test.handler.RefreshToken(NewGinHTTPContext(c))
			})
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/auth/refresh",
				nil,
			)
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			request.Host = "web.example.test"
			request.Header.Set("X-Forwarded-Host", "web.example.test")
			request.Header.Set("X-Forwarded-Proto", "https")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusForbidden {
				t.Fatalf(
					"status = %d, want 403; body=%s",
					response.Code,
					response.Body.String(),
				)
			}
			var problem ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Error != "origin_not_allowed" ||
				problem.Code != "origin_not_allowed" {
				t.Fatalf("origin failure = %+v", problem)
			}
			if len(response.Header().Values("Set-Cookie")) != 0 {
				t.Fatal("origin failure wrote a Cookie")
			}
		})
	}
}

func TestRefreshAndLogoutRejectLegacyCredentialCarriers(t *testing.T) {
	repository, manager, handler := setupSessionRevocationTest(t)
	_, refreshToken := issueSessionTokens(
		t,
		repository,
		manager,
		42,
		PlatformRolePlatformAdmin,
		"legacy-carrier-session",
	)
	for _, test := range []struct {
		name    string
		path    string
		body    string
		header  bool
		chunked bool
		query   bool
	}{
		{
			name: "refresh JSON bearer",
			path: "/api/auth/refresh",
			body: `{"refresh_token":"legacy-copy"}`,
		},
		{
			name: "refresh empty JSON body",
			path: "/api/auth/refresh",
			body: `{}`,
		},
		{
			name:    "refresh chunked JSON bearer",
			path:    "/api/auth/refresh",
			body:    `{"refresh_token":"chunked-legacy-copy"}`,
			chunked: true,
		},
		{
			name:   "refresh header bearer",
			path:   "/api/auth/refresh",
			header: true,
		},
		{
			name:  "refresh query bearer",
			path:  "/api/auth/refresh",
			query: true,
		},
		{
			name: "logout JSON bearer",
			path: "/api/auth/logout",
			body: `{"refresh_token":"legacy-copy"}`,
		},
		{
			name:   "logout header bearer",
			path:   "/api/auth/logout",
			header: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.POST(test.path, func(c *gin.Context) {
				httpContext := NewGinHTTPContext(c)
				if strings.HasSuffix(test.path, "/refresh") {
					handler.RefreshToken(httpContext)
					return
				}
				handler.Logout(httpContext)
			})
			target := test.path
			if test.query {
				target += "?refresh_token=query-legacy-copy"
			}
			request := httptest.NewRequest(
				http.MethodPost,
				target,
				strings.NewReader(test.body),
			)
			request.Header.Set("Origin", testBrowserOrigin)
			if test.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			if test.chunked {
				request.ContentLength = -1
				request.TransferEncoding = []string{"chunked"}
			}
			if test.header {
				request.Header.Set(
					"X-Refresh-Token",
					"legacy-header-copy",
				)
			}
			request.AddCookie(&http.Cookie{
				Name:  refreshTokenCookieName,
				Value: refreshToken,
				Path:  refreshTokenCookiePath,
			})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf(
					"status = %d, want 400; body=%s",
					response.Code,
					response.Body.String(),
				)
			}
			if len(response.Header().Values("Set-Cookie")) != 0 {
				t.Fatal("legacy credential rejection wrote a Cookie")
			}
		})
	}
	active, err := repository.IsSessionActive(
		context.Background(),
		42,
		"legacy-carrier-session",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("rejected legacy carrier changed the valid Cookie session")
	}
}

func TestRefreshUsesOnlyCookieRotatesWithoutJSONLeakAndReplaysDeterministically(
	t *testing.T,
) {
	repository, manager, handler := setupSessionRevocationTest(t)
	_, refreshToken := issueSessionTokens(
		t,
		repository,
		manager,
		42,
		PlatformRolePlatformAdmin,
		"cookie-refresh-session",
	)

	first := performRefreshCookieRequest(t, handler, refreshToken)
	if first.Code != http.StatusOK {
		t.Fatalf("first refresh status = %d; body=%s", first.Code, first.Body)
	}
	firstCookie := onlyRefreshCookie(t, first)
	if firstCookie.Value == "" || firstCookie.Value == refreshToken {
		t.Fatal("refresh did not rotate the browser Cookie")
	}
	if strings.Contains(first.Body.String(), firstCookie.Value) ||
		strings.Contains(first.Body.String(), "refresh_token") {
		t.Fatal("refresh response body exposed the refresh credential")
	}

	replayed := performRefreshCookieRequest(t, handler, refreshToken)
	if replayed.Code != http.StatusOK {
		t.Fatalf(
			"replayed refresh status = %d; body=%s",
			replayed.Code,
			replayed.Body,
		)
	}
	replayedCookie := onlyRefreshCookie(t, replayed)
	if replayedCookie.Value != firstCookie.Value {
		t.Fatal("rotation replay did not reproduce the same replacement Cookie")
	}
}

func TestConcurrentRefreshResponsesCannotClearOrDivergeRotatedCookie(t *testing.T) {
	repository, manager, handler := setupSessionRevocationTest(t)
	_, refreshToken := issueSessionTokens(
		t,
		repository,
		manager,
		42,
		PlatformRolePlatformAdmin,
		"concurrent-cookie-refresh-session",
	)

	const callers = 2
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, callers)
	var group sync.WaitGroup
	group.Add(callers)
	for range callers {
		go func() {
			defer group.Done()
			<-start
			results <- performRefreshCookieRequest(t, handler, refreshToken)
		}()
	}
	close(start)
	group.Wait()
	close(results)

	var replacement string
	for response := range results {
		if response.Code != http.StatusOK {
			t.Fatalf(
				"concurrent refresh status = %d; body=%s",
				response.Code,
				response.Body.String(),
			)
		}
		cookie := onlyRefreshCookie(t, response)
		if cookie.MaxAge <= 0 || cookie.Value == "" {
			t.Fatal("concurrent refresh emitted a clearing Cookie")
		}
		if replacement == "" {
			replacement = cookie.Value
		} else if cookie.Value != replacement {
			t.Fatal("concurrent refresh responses diverged")
		}
	}
}

func TestRefreshFailureNeverClearsAConcurrentSuccessCookie(t *testing.T) {
	_, _, handler := setupSessionRevocationTest(t)
	response := performRefreshCookieRequest(
		t,
		handler,
		"not-a-valid-browser-session",
	)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf(
			"invalid refresh status = %d; body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	if len(response.Header().Values("Set-Cookie")) != 0 {
		t.Fatal("failed refresh emitted Set-Cookie and could clear a newer session")
	}
}

func TestLogoutWithRotatedCookieRevokesTheReplacementSession(t *testing.T) {
	repository, manager, handler := setupSessionRevocationTest(t)
	accessToken, refreshToken := issueSessionTokens(
		t,
		repository,
		manager,
		42,
		PlatformRolePlatformAdmin,
		"refresh-logout-race-session",
	)
	refreshed := performRefreshCookieRequest(t, handler, refreshToken)
	if refreshed.Code != http.StatusOK {
		t.Fatalf(
			"refresh status = %d; body=%s",
			refreshed.Code,
			refreshed.Body.String(),
		)
	}
	replacement := onlyRefreshCookie(t, refreshed).Value

	// Force the exact old token outside the short replay lookup window. Logout
	// must still use its verified immutable sid to revoke the replacement.
	staleRotation := time.Now().UTC().Add(
		-refreshRotationReplayWindow - time.Second,
	)
	if err := repository.db.Model(&RefreshToken{}).
		Where("token = ?", bearerTokenDigest("refresh-token", refreshToken)).
		Update("rotated_at", staleRotation).Error; err != nil {
		t.Fatalf("age rotated Cookie record: %v", err)
	}

	router := gin.New()
	router.POST("/api/auth/logout", func(c *gin.Context) {
		handler.Logout(NewGinHTTPContext(c))
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/logout",
		nil,
	)
	request.Header.Set("Origin", testBrowserOrigin)
	request.Header.Set(
		humanSessionIDHeader,
		"refresh-logout-race-session",
	)
	request.AddCookie(&http.Cookie{
		Name:  refreshTokenCookieName,
		Value: refreshToken,
		Path:  refreshTokenCookiePath,
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf(
			"logout status = %d; body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	cleared := onlyRefreshCookie(t, response)
	if cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Fatal("logout did not clear the rotated browser Cookie")
	}
	if _, err := repository.GetRefreshToken(
		context.Background(),
		replacement,
	); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("replacement session remains active: %v", err)
	}
	if status, problem := protectedRequest(
		t,
		handler,
		accessToken,
	); status != http.StatusUnauthorized ||
		problem.Error != "session_revoked" {
		t.Fatalf(
			"rotated session access status = %d, problem=%+v",
			status,
			problem,
		)
	}
}

func TestLogoutRejectsCookieFromAReplacementBrowserSession(t *testing.T) {
	repository, manager, handler := setupSessionRevocationTest(t)
	_, _ = issueSessionTokens(
		t,
		repository,
		manager,
		42,
		PlatformRolePlatformAdmin,
		"stale-logout-session-a",
	)
	_, replacementRefresh := issueSessionTokens(
		t,
		repository,
		manager,
		84,
		PlatformRoleMember,
		"replacement-login-session-b",
	)

	router := gin.New()
	router.POST("/api/auth/logout", func(c *gin.Context) {
		handler.Logout(NewGinHTTPContext(c))
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/logout",
		nil,
	)
	request.Header.Set("Origin", testBrowserOrigin)
	request.Header.Set(
		humanSessionIDHeader,
		"stale-logout-session-a",
	)
	request.AddCookie(&http.Cookie{
		Name:  refreshTokenCookieName,
		Value: replacementRefresh,
		Path:  refreshTokenCookiePath,
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf(
			"stale logout status = %d, want 409; body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	if len(response.Header().Values("Set-Cookie")) != 0 {
		t.Fatal("stale logout cleared the replacement Cookie")
	}
	for _, session := range []struct {
		userID    uint
		sessionID string
	}{
		{userID: 42, sessionID: "stale-logout-session-a"},
		{userID: 84, sessionID: "replacement-login-session-b"},
	} {
		active, err := repository.IsSessionActive(
			context.Background(),
			session.userID,
			session.sessionID,
		)
		if err != nil {
			t.Fatal(err)
		}
		if !active {
			t.Fatalf("stale logout revoked session %q", session.sessionID)
		}
	}
}

func TestLogoutAllRejectsCookieFromAReplacementBrowserSession(t *testing.T) {
	repository, manager, handler := setupSessionRevocationTest(t)
	_, _ = issueSessionTokens(
		t,
		repository,
		manager,
		42,
		PlatformRolePlatformAdmin,
		"stale-logout-all-session-a",
	)
	_, replacementRefresh := issueSessionTokens(
		t,
		repository,
		manager,
		84,
		PlatformRoleMember,
		"replacement-logout-all-session-b",
	)

	router := gin.New()
	router.POST("/api/auth/logout-all", func(c *gin.Context) {
		c.Set("user_id", uint(42))
		c.Set("session_id", "stale-logout-all-session-a")
		handler.LogoutAll(NewGinHTTPContext(c))
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/logout-all",
		nil,
	)
	request.AddCookie(&http.Cookie{
		Name:  refreshTokenCookieName,
		Value: replacementRefresh,
		Path:  refreshTokenCookiePath,
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf(
			"stale logout-all status = %d, want 409; body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	if len(response.Header().Values("Set-Cookie")) != 0 {
		t.Fatal("stale logout-all cleared the replacement Cookie")
	}
	for _, session := range []struct {
		userID    uint
		sessionID string
	}{
		{userID: 42, sessionID: "stale-logout-all-session-a"},
		{userID: 84, sessionID: "replacement-logout-all-session-b"},
	} {
		active, err := repository.IsSessionActive(
			context.Background(),
			session.userID,
			session.sessionID,
		)
		if err != nil {
			t.Fatal(err)
		}
		if !active {
			t.Fatalf("stale logout-all revoked session %q", session.sessionID)
		}
	}
}

func TestRefreshAndLogoutRejectMissingCookie(t *testing.T) {
	_, _, handler := setupSessionRevocationTest(t)
	for _, path := range []string{"/api/auth/refresh", "/api/auth/logout"} {
		t.Run(path, func(t *testing.T) {
			router := gin.New()
			router.POST(path, func(c *gin.Context) {
				httpContext := NewGinHTTPContext(c)
				if strings.HasSuffix(path, "/refresh") {
					handler.RefreshToken(httpContext)
					return
				}
				handler.Logout(httpContext)
			})
			request := httptest.NewRequest(http.MethodPost, path, nil)
			request.Header.Set("Origin", testBrowserOrigin)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf(
					"status = %d, want 401; body=%s",
					response.Code,
					response.Body.String(),
				)
			}
			if len(response.Header().Values("Set-Cookie")) != 0 {
				t.Fatal("missing Cookie failure emitted Set-Cookie")
			}
		})
	}
}

func TestRefreshRejectsAmbiguousDuplicateCookies(t *testing.T) {
	repository, manager, handler := setupSessionRevocationTest(t)
	_, refreshToken := issueSessionTokens(
		t,
		repository,
		manager,
		42,
		PlatformRolePlatformAdmin,
		"duplicate-cookie-session",
	)
	router := gin.New()
	router.POST("/api/auth/refresh", func(c *gin.Context) {
		handler.RefreshToken(NewGinHTTPContext(c))
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/refresh",
		nil,
	)
	request.Header.Set("Origin", testBrowserOrigin)
	request.AddCookie(&http.Cookie{
		Name:  refreshTokenCookieName,
		Value: refreshToken,
		Path:  refreshTokenCookiePath,
	})
	request.AddCookie(&http.Cookie{
		Name:  refreshTokenCookieName,
		Value: "ambiguous-session-sentinel",
		Path:  refreshTokenCookiePath,
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf(
			"duplicate Cookie status = %d, want 401; body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	if len(response.Header().Values("Set-Cookie")) != 0 {
		t.Fatal("duplicate Cookie rejection emitted Set-Cookie")
	}
	active, err := repository.IsSessionActive(
		context.Background(),
		42,
		"duplicate-cookie-session",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("duplicate Cookie rejection revoked the valid session")
	}
}

type failingLogoutAllTokenRepository struct {
	TokenRepository
}

func (failingLogoutAllTokenRepository) RevokeAllUserTokens(
	context.Context,
	uint,
) error {
	return errors.New("session store unavailable")
}

func TestLogoutAllClearsCookiesOnlyAfterSuccessfulRevocation(t *testing.T) {
	handler := NewAuthHandler(
		&AuthService{
			tokenRepo: failingLogoutAllTokenRepository{},
		},
		nil,
	)
	router := gin.New()
	router.POST("/api/auth/logout-all", func(c *gin.Context) {
		c.Set("user_id", uint(42))
		handler.LogoutAll(NewGinHTTPContext(c))
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodPost,
			"/api/auth/logout-all",
			nil,
		),
	)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf(
			"status = %d, want 500; body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	if len(response.Header().Values("Set-Cookie")) != 0 {
		t.Fatal("failed logout-all cleared browser Cookies")
	}
}

func performRefreshCookieRequest(
	t *testing.T,
	handler *AuthHandler,
	refreshToken string,
) *httptest.ResponseRecorder {
	t.Helper()
	router := gin.New()
	router.POST("/api/auth/refresh", func(c *gin.Context) {
		handler.RefreshToken(NewGinHTTPContext(c))
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/refresh",
		nil,
	)
	request.Header.Set("Origin", testBrowserOrigin)
	request.AddCookie(&http.Cookie{
		Name:  refreshTokenCookieName,
		Value: refreshToken,
		Path:  refreshTokenCookiePath,
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func onlyRefreshCookie(
	t *testing.T,
	response *httptest.ResponseRecorder,
) *http.Cookie {
	t.Helper()
	var found *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name != refreshTokenCookieName {
			continue
		}
		if found != nil {
			t.Fatal("response emitted multiple refresh Cookies")
		}
		found = cookie
	}
	if found == nil {
		t.Fatal("response omitted refresh Cookie")
	}
	if found.Path != refreshTokenCookiePath ||
		!found.HttpOnly ||
		found.SameSite != http.SameSiteStrictMode {
		t.Fatalf(
			"refresh Cookie attributes invalid: path=%q HttpOnly=%v SameSite=%v",
			found.Path,
			found.HttpOnly,
			found.SameSite,
		)
	}
	return found
}
