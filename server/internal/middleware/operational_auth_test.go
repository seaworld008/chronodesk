package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestOperationalEndpointAuthRequiresStrongConfiguration(t *testing.T) {
	for _, test := range []struct {
		name      string
		token     string
		loopback  bool
		wantError bool
	}{
		{name: "missing", wantError: true},
		{name: "weak", token: strings.Repeat("x", 31), wantError: true},
		{
			name:      "surrounding whitespace",
			token:     " " + strings.Repeat("x", 32),
			wantError: true,
		},
		{name: "loopback only", loopback: true},
		{name: "strong bearer", token: strings.Repeat("x", 32)},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewOperationalEndpointAuth(test.token, test.loopback)
			if (err != nil) != test.wantError {
				t.Fatalf("configuration error = %v, wantError=%v", err, test.wantError)
			}
		})
	}
}

func TestOperationalEndpointAuthBearerAndLoopbackPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	token := "chronodesk-operational-test-token-2026"
	for _, test := range []struct {
		name       string
		token      string
		loopback   bool
		remoteAddr string
		header     string
		wantStatus int
	}{
		{
			name:       "valid bearer",
			token:      token,
			remoteAddr: "192.0.2.10:9000",
			header:     "Bearer " + token,
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "invalid bearer",
			token:      token,
			remoteAddr: "192.0.2.10:9000",
			header:     "Bearer wrong",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "query token ignored",
			token:      token,
			remoteAddr: "192.0.2.10:9000",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "loopback allowed",
			loopback:   true,
			remoteAddr: "127.0.0.1:9000",
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "non-loopback denied",
			loopback:   true,
			remoteAddr: "192.0.2.10:9000",
			wantStatus: http.StatusUnauthorized,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			auth, err := NewOperationalEndpointAuth(test.token, test.loopback)
			if err != nil {
				t.Fatal(err)
			}
			router := gin.New()
			if err := router.SetTrustedProxies(nil); err != nil {
				t.Fatal(err)
			}
			router.GET("/metrics", auth.Middleware(), func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})
			request := httptest.NewRequest(
				http.MethodGet,
				"/metrics?access_token="+token,
				nil,
			)
			request.RemoteAddr = test.remoteAddr
			if test.header != "" {
				request.Header.Set("Authorization", test.header)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf(
					"status = %d, want %d, body=%s",
					response.Code,
					test.wantStatus,
					response.Body.String(),
				)
			}
			if response.Code == http.StatusUnauthorized {
				if !strings.Contains(
					response.Header().Get("WWW-Authenticate"),
					"Bearer",
				) {
					t.Fatal("unauthorized response omitted Bearer challenge")
				}
				if strings.Contains(response.Body.String(), token) {
					t.Fatal("unauthorized response disclosed bearer token")
				}
			}
		})
	}
}
