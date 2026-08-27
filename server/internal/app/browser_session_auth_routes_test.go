package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/auth"
)

const appTestBrowserOrigin = "https://web.example.test"

type silentAuthLogger struct{}

func (silentAuthLogger) Info(string, ...interface{})  {}
func (silentAuthLogger) Error(string, ...interface{}) {}
func (silentAuthLogger) Warn(string, ...interface{})  {}
func (silentAuthLogger) Debug(string, ...interface{}) {}

func newBrowserSessionAuthRouteTestRouter(
	steps *[]string,
) *gin.Engine {
	router := gin.New()
	handler := auth.NewAuthHandler(
		nil,
		silentAuthLogger{},
		auth.WithAllowedBrowserOrigin(appTestBrowserOrigin),
	)
	recordMiddleware := func(name string) gin.HandlerFunc {
		return func(context *gin.Context) {
			*steps = append(*steps, name)
			context.Next()
		}
	}
	recordHandler := func(name string) gin.HandlerFunc {
		return func(context *gin.Context) {
			*steps = append(*steps, name)
			context.Status(http.StatusNoContent)
		}
	}
	originGate := func(context *gin.Context) {
		*steps = append(*steps, "origin")
		ginAdapter(handler.RequireAllowedBrowserOrigin)(context)
	}

	registerBrowserSessionAuthRoutes(
		router.Group("/api/auth"),
		browserSessionAuthMiddleware{
			originGate:                 originGate,
			anonymousIPRateLimit:       recordMiddleware("anonymous-ip-limit"),
			refreshIdentityProjection:  recordMiddleware("refresh-identity"),
			anonymousIdentityRateLimit: recordMiddleware("anonymous-identity-limit"),
			requireAuth:                recordMiddleware("require-auth"),
			authenticatedRateLimit:     recordMiddleware("authenticated-limit"),
		},
		browserSessionAuthHandlers{
			register:  recordHandler("register"),
			login:     recordHandler("login"),
			logout:    recordHandler("logout"),
			refresh:   recordHandler("refresh"),
			logoutAll: recordHandler("logout-all"),
		},
	)
	return router
}

func TestBrowserSessionAuthRoutesGateOriginBeforeRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		path string
		want []string
	}{
		{
			path: "/api/auth/register",
			want: []string{
				"origin",
				"anonymous-ip-limit",
				"anonymous-identity-limit",
				"register",
			},
		},
		{
			path: "/api/auth/login",
			want: []string{
				"origin",
				"anonymous-ip-limit",
				"anonymous-identity-limit",
				"login",
			},
		},
		{
			path: "/api/auth/refresh",
			want: []string{
				"origin",
				"anonymous-ip-limit",
				"refresh-identity",
				"anonymous-identity-limit",
				"refresh",
			},
		},
		{
			path: "/api/auth/logout",
			want: []string{
				"origin",
				"anonymous-ip-limit",
				"refresh-identity",
				"anonymous-identity-limit",
				"logout",
			},
		},
		{
			path: "/api/auth/logout-all",
			want: []string{
				"origin",
				"require-auth",
				"authenticated-limit",
				"logout-all",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			steps := make([]string, 0, len(test.want))
			router := newBrowserSessionAuthRouteTestRouter(&steps)
			request := httptest.NewRequest(http.MethodPost, test.path, nil)
			request.Header.Set("Origin", appTestBrowserOrigin)
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			if response.Code != http.StatusNoContent {
				t.Fatalf(
					"status = %d, want %d; body=%s",
					response.Code,
					http.StatusNoContent,
					response.Body.String(),
				)
			}
			if !reflect.DeepEqual(steps, test.want) {
				t.Fatalf("middleware order = %v, want %v", steps, test.want)
			}
		})
	}
}

func TestBrowserSessionAuthRoutesStopBeforeRefreshIdentityWhenIPLimitRejects(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)

	for _, path := range []string{
		"/api/auth/refresh",
		"/api/auth/logout",
	} {
		t.Run(path, func(t *testing.T) {
			steps := make([]string, 0, 3)
			router := gin.New()
			record := func(name string) gin.HandlerFunc {
				return func(context *gin.Context) {
					steps = append(steps, name)
					context.Next()
				}
			}
			rejectIPLimit := func(context *gin.Context) {
				steps = append(steps, "anonymous-ip-limit")
				context.AbortWithStatus(http.StatusTooManyRequests)
			}
			unexpectedHandler := func(context *gin.Context) {
				steps = append(steps, "handler")
				context.Status(http.StatusNoContent)
			}
			registerBrowserSessionAuthRoutes(
				router.Group("/api/auth"),
				browserSessionAuthMiddleware{
					originGate:                 record("origin"),
					anonymousIPRateLimit:       rejectIPLimit,
					refreshIdentityProjection:  record("refresh-identity"),
					anonymousIdentityRateLimit: record("anonymous-identity-limit"),
					requireAuth:                record("require-auth"),
					authenticatedRateLimit:     record("authenticated-limit"),
				},
				browserSessionAuthHandlers{
					register:  unexpectedHandler,
					login:     unexpectedHandler,
					logout:    unexpectedHandler,
					refresh:   unexpectedHandler,
					logoutAll: unexpectedHandler,
				},
			)
			request := httptest.NewRequest(http.MethodPost, path, nil)
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			if response.Code != http.StatusTooManyRequests {
				t.Fatalf(
					"status = %d, want %d",
					response.Code,
					http.StatusTooManyRequests,
				)
			}
			want := []string{"origin", "anonymous-ip-limit"}
			if !reflect.DeepEqual(steps, want) {
				t.Fatalf("middleware steps = %v, want %v", steps, want)
			}
		})
	}
}

func TestBrowserSessionAuthRoutesRejectUntrustedOriginBeforeRateLimit(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)

	originCases := []struct {
		name    string
		origins []string
	}{
		{name: "missing"},
		{name: "null", origins: []string{"null"}},
		{
			name:    "wrong",
			origins: []string{"https://attacker.example.test"},
		},
		{
			name: "multiple",
			origins: []string{
				appTestBrowserOrigin,
				"https://attacker.example.test",
			},
		},
	}
	paths := []string{
		"/api/auth/register",
		"/api/auth/login",
		"/api/auth/refresh",
		"/api/auth/logout",
		"/api/auth/logout-all",
	}

	for _, originCase := range originCases {
		for _, path := range paths {
			t.Run(originCase.name+" "+path, func(t *testing.T) {
				steps := make([]string, 0, 1)
				router := newBrowserSessionAuthRouteTestRouter(&steps)
				request := httptest.NewRequest(http.MethodPost, path, nil)
				for _, origin := range originCase.origins {
					request.Header.Add("Origin", origin)
				}
				response := httptest.NewRecorder()

				router.ServeHTTP(response, request)

				if response.Code != http.StatusForbidden {
					t.Fatalf(
						"status = %d, want %d; body=%s",
						response.Code,
						http.StatusForbidden,
						response.Body.String(),
					)
				}
				if !reflect.DeepEqual(steps, []string{"origin"}) {
					t.Fatalf(
						"middleware steps = %v, want Origin gate only",
						steps,
					)
				}
				var problem auth.ErrorResponse
				if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
					t.Fatalf("decode Origin rejection: %v", err)
				}
				if problem.Error != "origin_not_allowed" ||
					problem.Code != "origin_not_allowed" {
					t.Fatalf("Origin rejection = %+v", problem)
				}
			})
		}
	}
}
