package auth

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestTrustedDeviceCredentialIsNotPartOfTheJSONContract(t *testing.T) {
	var request LoginRequest
	if err := json.Unmarshal([]byte(`{
		"email":"user@example.com",
		"password":"password",
		"device_token":"attacker-controlled"
	}`), &request); err != nil {
		t.Fatalf("decode login request: %v", err)
	}
	if request.DeviceToken != "" {
		t.Fatal("login request accepted a trusted-device credential from JSON")
	}

	encoded, err := json.Marshal(AuthResponse{
		AccessToken:            "access-token",
		RefreshToken:           "refresh-token",
		TrustedDeviceToken:     "trusted-device-secret",
		TrustedDeviceExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("encode auth response: %v", err)
	}
	if strings.Contains(string(encoded), "trusted-device-secret") ||
		strings.Contains(string(encoded), "trusted_device") ||
		strings.Contains(string(encoded), "refresh-token") ||
		strings.Contains(string(encoded), "refresh_token") {
		t.Fatalf("auth response exposed a server-managed credential: %s", encoded)
	}
}

func TestTrustedDeviceCookieUsesHardenedProductionAttributes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewAuthHandler(nil, nil, WithSecureTrustedDeviceCookie(true))
	expiresAt := time.Now().Add(24 * time.Hour)

	cookie := recordTrustedDeviceCookie(t, nil, func(c HTTPContext) {
		handler.setTrustedDeviceCookie(c, "opaque-token", expiresAt)
	})

	if cookie.Name != trustedDeviceCookieName || cookie.Value != "opaque-token" {
		t.Fatalf("cookie identity = %s/%s", cookie.Name, cookie.Value)
	}
	if cookie.Path != trustedDeviceCookiePath {
		t.Fatalf("cookie path = %q, want %q", cookie.Path, trustedDeviceCookiePath)
	}
	if !cookie.HttpOnly {
		t.Fatal("trusted-device cookie must be HttpOnly")
	}
	if !cookie.Secure {
		t.Fatal("production trusted-device cookie must be Secure")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("SameSite = %v, want Strict", cookie.SameSite)
	}
	if cookie.MaxAge <= 0 || cookie.Expires.Before(time.Now()) {
		t.Fatalf("cookie lifetime is invalid: MaxAge=%d Expires=%s", cookie.MaxAge, cookie.Expires)
	}
}

func TestTrustedDeviceCookieAutomaticallyUsesSecureOnTLS(t *testing.T) {
	handler := NewAuthHandler(nil, nil)
	cookie := recordTrustedDeviceCookie(t, &tls.ConnectionState{}, func(c HTTPContext) {
		handler.setTrustedDeviceCookie(c, "opaque-token", time.Now().Add(time.Hour))
	})
	if !cookie.Secure {
		t.Fatal("TLS trusted-device cookie must be Secure")
	}
}

func TestClearTrustedDeviceCookiePreservesSecurityAttributes(t *testing.T) {
	handler := NewAuthHandler(nil, nil, WithSecureTrustedDeviceCookie(true))
	cookie := recordTrustedDeviceCookie(t, nil, handler.clearTrustedDeviceCookie)

	if cookie.Value != "" || cookie.MaxAge >= 0 {
		t.Fatalf("clear cookie did not expire credential: value=%q MaxAge=%d", cookie.Value, cookie.MaxAge)
	}
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf(
			"clear cookie weakened attributes: HttpOnly=%v Secure=%v SameSite=%v",
			cookie.HttpOnly,
			cookie.Secure,
			cookie.SameSite,
		)
	}
}

func recordTrustedDeviceCookie(
	t *testing.T,
	tlsState *tls.ConnectionState,
	writeCookie func(HTTPContext),
) *http.Cookie {
	t.Helper()

	router := gin.New()
	router.GET("/cookie", func(c *gin.Context) {
		writeCookie(NewGinHTTPContext(c))
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "/cookie", nil)
	request.TLS = tlsState
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Set-Cookie count = %d, want 1; header=%q", len(cookies), response.Header().Values("Set-Cookie"))
	}
	return cookies[0]
}
