package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestProjectRefreshRateLimitIdentityUsesVerifiedStableSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := mustTestJWTManager(t, time.Hour, 24*time.Hour)
	issuedAt := time.Now().UTC().Add(-time.Minute)
	_, initial, err := manager.GenerateTokenPairAt(
		42,
		PlatformRoleMember,
		"stable-session",
		issuedAt,
	)
	if err != nil {
		t.Fatalf("generate initial refresh token: %v", err)
	}
	_, replacement, err := manager.GenerateRefreshTokenPair(
		42,
		PlatformRoleMember,
		"stable-session",
		initial,
		issuedAt.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("generate replacement refresh token: %v", err)
	}
	_, otherSession, err := manager.GenerateTokenPairAt(
		42,
		PlatformRoleMember,
		"other-session",
		issuedAt,
	)
	if err != nil {
		t.Fatalf("generate other-session refresh token: %v", err)
	}
	_, otherUser, err := manager.GenerateTokenPairAt(
		84,
		PlatformRoleMember,
		"stable-session",
		issuedAt,
	)
	if err != nil {
		t.Fatalf("generate other-user refresh token: %v", err)
	}

	handler := NewAuthHandler(
		&AuthService{jwtManager: manager},
		nil,
	)
	project := func(token string) (RefreshRateLimitIdentity, bool) {
		t.Helper()
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(
			http.MethodPost,
			"/api/auth/refresh",
			nil,
		)
		context.Request.AddCookie(&http.Cookie{
			Name:  refreshTokenCookieName,
			Value: token,
			Path:  refreshTokenCookiePath,
		})
		return handler.ProjectRefreshRateLimitIdentity(
			NewGinHTTPContext(context),
		)
	}

	initialIdentity, ok := project(initial)
	if !ok {
		t.Fatal("initial refresh token was not projected")
	}
	replacementIdentity, ok := project(replacement)
	if !ok {
		t.Fatal("replacement refresh token was not projected")
	}
	if initialIdentity != replacementIdentity {
		t.Fatalf(
			"rotated session identities differ: initial=%+v replacement=%+v",
			initialIdentity,
			replacementIdentity,
		)
	}
	if initialIdentity.UserID != 42 ||
		initialIdentity.SessionID != "stable-session" {
		t.Fatalf("stable identity = %+v", initialIdentity)
	}
	differentSessionIdentity, ok := project(otherSession)
	if !ok || differentSessionIdentity == initialIdentity {
		t.Fatalf(
			"different session identity = %+v, ok=%v",
			differentSessionIdentity,
			ok,
		)
	}
	differentUserIdentity, ok := project(otherUser)
	if !ok || differentUserIdentity == initialIdentity {
		t.Fatalf(
			"different user identity = %+v, ok=%v",
			differentUserIdentity,
			ok,
		)
	}
}

func TestProjectRefreshRateLimitIdentityRejectsUntrustedCookieClaims(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	manager := mustTestJWTManager(t, time.Hour, time.Hour)
	issuedAt := time.Now().UTC()
	access, validRefresh, err := manager.GenerateTokenPairAt(
		42,
		PlatformRoleMember,
		"stable-session",
		issuedAt,
	)
	if err != nil {
		t.Fatalf("generate current token pair: %v", err)
	}
	_, expiredRefresh, err := manager.GenerateTokenPairAt(
		42,
		PlatformRoleMember,
		"stable-session",
		issuedAt.Add(-2*time.Hour),
	)
	if err != nil {
		t.Fatalf("generate expired refresh token: %v", err)
	}
	handler := NewAuthHandler(
		&AuthService{jwtManager: manager},
		nil,
	)

	tests := []struct {
		name    string
		cookies []string
	}{
		{name: "missing"},
		{name: "malformed", cookies: []string{"not-a-jwt"}},
		{name: "expired", cookies: []string{expiredRefresh}},
		{name: "access token", cookies: []string{access}},
		{name: "duplicate", cookies: []string{validRefresh, validRefresh}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(
				http.MethodPost,
				"/api/auth/refresh",
				nil,
			)
			for _, token := range test.cookies {
				context.Request.AddCookie(&http.Cookie{
					Name:  refreshTokenCookieName,
					Value: token,
					Path:  refreshTokenCookiePath,
				})
			}

			identity, ok := handler.ProjectRefreshRateLimitIdentity(
				NewGinHTTPContext(context),
			)
			if ok || identity != (RefreshRateLimitIdentity{}) {
				t.Fatalf(
					"untrusted Cookie projected identity = %+v, ok=%v",
					identity,
					ok,
				)
			}
		})
	}
}
