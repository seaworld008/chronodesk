package auth

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

type originReadCounter struct {
	reader io.Reader
	reads  int
}

func (counter *originReadCounter) Read(buffer []byte) (int, error) {
	counter.reads++
	return counter.reader.Read(buffer)
}

func TestRegisterAndLoginRejectUntrustedOriginBeforeReadingCredentials(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	handler := NewAuthHandler(
		nil,
		nil,
		WithAllowedBrowserOrigin(testBrowserOrigin),
	)
	endpoints := []struct {
		name    string
		path    string
		payload string
		handle  func(HTTPContext)
	}{
		{
			name: "register",
			path: "/register",
			payload: `{"username":"origin-user","email":"origin-user@example.test",` +
				`"password":"StrongPassword123!","confirm_password":"StrongPassword123!"}`,
			handle: handler.Register,
		},
		{
			name:    "login",
			path:    "/login",
			payload: `{"email":"origin-user@example.test","password":"StrongPassword123!"}`,
			handle:  handler.Login,
		},
	}
	origins := []struct {
		name   string
		values []string
	}{
		{name: "missing"},
		{name: "null", values: []string{"null"}},
		{name: "wrong", values: []string{"https://attacker.example.test"}},
		{
			name:   "duplicate",
			values: []string{testBrowserOrigin, testBrowserOrigin},
		},
	}

	for _, endpoint := range endpoints {
		for _, origin := range origins {
			t.Run(endpoint.name+"/"+origin.name, func(t *testing.T) {
				router := gin.New()
				router.POST(endpoint.path, func(c *gin.Context) {
					endpoint.handle(NewGinHTTPContext(c))
				})
				body := &originReadCounter{
					reader: bytes.NewBufferString(endpoint.payload),
				}
				request := httptest.NewRequest(
					http.MethodPost,
					endpoint.path,
					body,
				)
				request.Header.Set("Content-Type", "text/plain")
				for _, value := range origin.values {
					request.Header.Add("Origin", value)
				}
				request.AddCookie(&http.Cookie{
					Name:  trustedDeviceCookieName,
					Value: "must-not-be-consumed",
					Path:  trustedDeviceCookiePath,
				})
				response := httptest.NewRecorder()

				router.ServeHTTP(response, request)

				if response.Code != http.StatusForbidden {
					t.Fatalf(
						"status = %d, want %d; body=%s",
						response.Code,
						http.StatusForbidden,
						response.Body,
					)
				}
				if body.reads != 0 {
					t.Fatalf(
						"credential body reads = %d, want 0 before Origin acceptance",
						body.reads,
					)
				}
				if cookies := response.Header().Values("Set-Cookie"); len(cookies) != 0 {
					t.Fatalf("untrusted Origin issued cookies: %q", cookies)
				}
			})
		}
	}
}

func TestRegisterAndLoginAcceptExactOriginBeforeBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewAuthHandler(
		nil,
		nil,
		WithAllowedBrowserOrigin(testBrowserOrigin),
	)
	for _, endpoint := range []struct {
		name   string
		handle func(HTTPContext)
	}{
		{name: "register", handle: handler.Register},
		{name: "login", handle: handler.Login},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			router := gin.New()
			router.POST("/", func(c *gin.Context) {
				endpoint.handle(NewGinHTTPContext(c))
			})
			body := &originReadCounter{
				reader: bytes.NewBufferString(`{"invalid":`),
			}
			request := httptest.NewRequest(http.MethodPost, "/", body)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", testBrowserOrigin)
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf(
					"status = %d, want %d; body=%s",
					response.Code,
					http.StatusBadRequest,
					response.Body,
				)
			}
			if body.reads == 0 {
				t.Fatal("exact allowed Origin did not reach credential binding")
			}
			if cookies := response.Header().Values("Set-Cookie"); len(cookies) != 0 {
				t.Fatalf("failed binding issued cookies: %q", cookies)
			}
		})
	}
}

func TestBrowserOriginAllowedUsesCanonicalExactOrigin(t *testing.T) {
	for _, test := range []struct {
		name    string
		allowed string
		values  []string
		want    bool
	}{
		{
			name:    "exact",
			allowed: "HTTPS://WEB.EXAMPLE.TEST:443",
			values:  []string{testBrowserOrigin},
			want:    true,
		},
		{
			name:    "path is rejected",
			allowed: testBrowserOrigin,
			values:  []string{testBrowserOrigin + "/login"},
		},
		{
			name:    "multiple origins are rejected",
			allowed: testBrowserOrigin,
			values:  []string{testBrowserOrigin, testBrowserOrigin},
		},
		{
			name:    "invalid configuration fails closed",
			allowed: "",
			values:  []string{testBrowserOrigin},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", nil)
			for _, value := range test.values {
				request.Header.Add("Origin", value)
			}
			if got := BrowserOriginAllowed(request, test.allowed); got != test.want {
				t.Fatalf("BrowserOriginAllowed() = %v, want %v", got, test.want)
			}
		})
	}
}
