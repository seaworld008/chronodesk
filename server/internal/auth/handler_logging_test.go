package auth

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAuthLogRequestIDRemovesLogForgingCharacters(t *testing.T) {
	gin.SetMode(gin.TestMode)

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest("GET", "/", nil)
	context.Set("request_id", "req-1\r\n[ERROR] forged\u202e")

	got := authLogRequestID(NewGinHTTPContext(context))
	if strings.ContainsAny(got, "\r\n") || strings.ContainsRune(got, '\u202e') {
		t.Fatalf("request ID was not safe for plaintext logs: %q", got)
	}
	if got != "req-1[ERROR] forged" {
		t.Fatalf("safe request ID = %q", got)
	}
}

func TestAuthLogReasonNeverReturnsSensitiveErrorText(t *testing.T) {
	t.Parallel()

	secret := "password=correct-horse-battery-staple otp=123456 token=secret"
	if got := authLogReason(errors.New(secret)); got != "internal_error" {
		t.Fatalf("unknown authentication error reason = %q", got)
	}
	for _, err := range []error{
		ErrInvalidCredentials,
		ErrUserNotFound,
		ErrUserExists,
		ErrInvalidToken,
		ErrTokenExpired,
		ErrInvalidOTP,
		ErrOTPExpired,
		ErrEmailNotVerified,
		ErrAccountLocked,
		ErrPasswordTooWeak,
	} {
		if got := authLogReason(err); got == "" || strings.Contains(got, secret) {
			t.Fatalf("authentication reason for %v is unsafe: %q", err, got)
		}
	}
}
