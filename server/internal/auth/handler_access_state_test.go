package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type accessStateUserRepo struct {
	*otpTestUserRepo
	getErr error
}

func (r *accessStateUserRepo) GetByID(ctx context.Context, id uint) (*User, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	return r.otpTestUserRepo.GetByID(ctx, id)
}

type accessStateTokenRepo struct {
	TokenRepository
	active bool
	err    error
}

func (r *accessStateTokenRepo) IsSessionActive(context.Context, uint, string) (bool, error) {
	return r.active, r.err
}

func TestRequireAuthRevalidatesCurrentUserStateAndPlatformRole(t *testing.T) {
	gin.SetMode(gin.TestMode)

	passwordChangedAt := time.Now().Add(time.Minute)
	activeLock := time.Now().Add(time.Hour)
	expiredLock := time.Now().Add(-time.Hour)
	tests := []struct {
		name          string
		currentUser   *User
		repoError     error
		tokenRole     PlatformRole
		sessionActive bool
		sessionError  error
		legacyToken   string
		wantStatus    int
		wantErrorCode string
	}{
		{
			name: "active user with current role is accepted",
			currentUser: &User{
				ID:           42,
				PlatformRole: PlatformRolePlatformAdmin,
				Status:       StatusActive,
			},
			tokenRole:     PlatformRolePlatformAdmin,
			sessionActive: true,
			wantStatus:    http.StatusOK,
		},
		{
			name: "inactive user access token is rejected immediately",
			currentUser: &User{
				ID:           42,
				PlatformRole: PlatformRolePlatformAdmin,
				Status:       StatusInactive,
			},
			tokenRole:     PlatformRolePlatformAdmin,
			sessionActive: true,
			wantStatus:    http.StatusUnauthorized,
			wantErrorCode: "account_inactive",
		},
		{
			name: "suspended user access token is rejected immediately",
			currentUser: &User{
				ID:           42,
				PlatformRole: PlatformRolePlatformAdmin,
				Status:       StatusSuspended,
			},
			tokenRole:     PlatformRolePlatformAdmin,
			sessionActive: true,
			wantStatus:    http.StatusUnauthorized,
			wantErrorCode: "account_inactive",
		},
		{
			name: "deleted user status is rejected immediately",
			currentUser: &User{
				ID:           42,
				PlatformRole: PlatformRolePlatformAdmin,
				Status:       StatusDeleted,
			},
			tokenRole:     PlatformRolePlatformAdmin,
			sessionActive: true,
			wantStatus:    http.StatusUnauthorized,
			wantErrorCode: "account_inactive",
		},
		{
			name: "unknown user status fails closed",
			currentUser: &User{
				ID:           42,
				PlatformRole: PlatformRolePlatformAdmin,
				Status:       UserStatus("unknown"),
			},
			tokenRole:     PlatformRolePlatformAdmin,
			sessionActive: true,
			wantStatus:    http.StatusUnauthorized,
			wantErrorCode: "invalid_token",
		},
		{
			name: "future lock deadline invalidates access token",
			currentUser: &User{
				ID:           42,
				PlatformRole: PlatformRolePlatformAdmin,
				Status:       StatusActive,
				LockedUntil:  &activeLock,
			},
			tokenRole:     PlatformRolePlatformAdmin,
			sessionActive: true,
			wantStatus:    http.StatusUnauthorized,
			wantErrorCode: "account_locked",
		},
		{
			name: "expired lock deadline permits access token",
			currentUser: &User{
				ID:           42,
				PlatformRole: PlatformRolePlatformAdmin,
				Status:       StatusActive,
				LockedUntil:  &expiredLock,
			},
			tokenRole:     PlatformRolePlatformAdmin,
			sessionActive: true,
			wantStatus:    http.StatusOK,
		},
		{
			name:          "deleted user access token is rejected immediately",
			currentUser:   nil,
			tokenRole:     PlatformRolePlatformAdmin,
			sessionActive: true,
			wantStatus:    http.StatusUnauthorized,
			wantErrorCode: "invalid_token",
		},
		{
			name:          "repository outage fails closed without invalidating the session",
			repoError:     errors.New("database unavailable"),
			tokenRole:     PlatformRolePlatformAdmin,
			sessionActive: true,
			wantStatus:    http.StatusServiceUnavailable,
			wantErrorCode: "authentication_unavailable",
		},
		{
			name: "role downgrade invalidates old administrator token",
			currentUser: &User{
				ID:           42,
				PlatformRole: PlatformRoleMember,
				Status:       StatusActive,
			},
			tokenRole:     PlatformRolePlatformAdmin,
			sessionActive: true,
			wantStatus:    http.StatusUnauthorized,
			wantErrorCode: "stale_token",
		},
		{
			name: "migrated customer rejects historical role token",
			currentUser: &User{
				ID:           42,
				PlatformRole: PlatformRoleMember,
				Status:       StatusActive,
			},
			legacyToken:   "user",
			sessionActive: true,
			wantStatus:    http.StatusUnauthorized,
			wantErrorCode: "invalid_token",
		},
		{
			name: "migrated administrator rejects historical role token",
			currentUser: &User{
				ID:           42,
				PlatformRole: PlatformRolePlatformAdmin,
				Status:       StatusActive,
			},
			legacyToken:   "superuser",
			sessionActive: true,
			wantStatus:    http.StatusUnauthorized,
			wantErrorCode: "invalid_token",
		},
		{
			name: "unmigrated historical role fails closed",
			currentUser: &User{
				ID:           42,
				PlatformRole: PlatformRole("admin"),
				Status:       StatusActive,
			},
			legacyToken:   "admin",
			sessionActive: true,
			wantStatus:    http.StatusUnauthorized,
			wantErrorCode: "invalid_token",
		},
		{
			name: "password change invalidates old access token",
			currentUser: &User{
				ID:                42,
				PlatformRole:      PlatformRolePlatformAdmin,
				Status:            StatusActive,
				PasswordChangedAt: &passwordChangedAt,
			},
			tokenRole:     PlatformRolePlatformAdmin,
			sessionActive: true,
			wantStatus:    http.StatusUnauthorized,
			wantErrorCode: "stale_token",
		},
		{
			name: "revoked login session invalidates access token",
			currentUser: &User{
				ID:           42,
				PlatformRole: PlatformRolePlatformAdmin,
				Status:       StatusActive,
			},
			tokenRole:     PlatformRolePlatformAdmin,
			sessionActive: false,
			wantStatus:    http.StatusUnauthorized,
			wantErrorCode: "session_revoked",
		},
		{
			name: "session repository outage fails closed",
			currentUser: &User{
				ID:           42,
				PlatformRole: PlatformRolePlatformAdmin,
				Status:       StatusActive,
			},
			tokenRole:     PlatformRolePlatformAdmin,
			sessionActive: true,
			sessionError:  errors.New("session database unavailable"),
			wantStatus:    http.StatusServiceUnavailable,
			wantErrorCode: "authentication_unavailable",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			jwtManager := mustTestJWTManager(t, time.Hour, time.Hour)
			var accessToken string
			var err error
			if test.legacyToken != "" {
				accessToken, err = signedLegacyRoleToken(
					jwtManager,
					test.legacyToken,
					"access",
					jwtManager.accessSecret,
				)
			} else {
				accessToken, _, err = jwtManager.GenerateTokenPair(42, test.tokenRole, "session-test-42")
			}
			if err != nil {
				t.Fatalf("generate token: %v", err)
			}

			handler := NewAuthHandler(&AuthService{
				userRepo: &accessStateUserRepo{
					otpTestUserRepo: &otpTestUserRepo{user: test.currentUser},
					getErr:          test.repoError,
				},
				jwtManager: jwtManager,
				tokenRepo: &accessStateTokenRepo{
					active: test.sessionActive,
					err:    test.sessionError,
				},
			}, nil)

			router := gin.New()
			router.GET("/protected", func(c *gin.Context) {
				handler.RequireAuth(NewGinHTTPContext(c))
				if c.IsAborted() {
					return
				}
				platformRole, _ := c.Get("platform_role")
				_, legacyRole := c.Get("user_role")
				c.JSON(http.StatusOK, gin.H{
					"platform_role": platformRole,
					"legacy_role":   legacyRole,
				})
			})

			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			request.Header.Set("Authorization", "Bearer "+accessToken)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if test.wantErrorCode == "" {
				return
			}

			var payload ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if payload.Error != test.wantErrorCode {
				t.Fatalf("error = %q, want %q", payload.Error, test.wantErrorCode)
			}
		})
	}
}

func TestJWTManagerRejectsWrongAudienceAndSubject(t *testing.T) {
	manager := mustTestJWTManager(t, time.Hour, time.Hour)
	now := time.Now()

	tests := []struct {
		name    string
		payload *JWTPayload
	}{
		{
			name: "wrong issuer",
			payload: &JWTPayload{
				UserID:       42,
				PlatformRole: PlatformRolePlatformAdmin,
				Type:         "access",
				SessionID:    "session-wrong-issuer",
				Iss:          "https://other-issuer.example.test",
				Sub:          "42",
				Aud:          manager.audience,
				Exp:          now.Add(time.Hour).Unix(),
				Nbf:          now.Unix(),
				Iat:          now.Unix(),
				Jti:          "wrong-issuer",
			},
		},
		{
			name: "wrong audience",
			payload: &JWTPayload{
				UserID:       42,
				PlatformRole: PlatformRolePlatformAdmin,
				Type:         "access",
				SessionID:    "session-wrong-audience",
				Iss:          manager.issuer,
				Sub:          "42",
				Aud:          "another-api",
				Exp:          now.Add(time.Hour).Unix(),
				Nbf:          now.Unix(),
				Iat:          now.Unix(),
				Jti:          "wrong-audience",
			},
		},
		{
			name: "subject does not match user",
			payload: &JWTPayload{
				UserID:       42,
				PlatformRole: PlatformRolePlatformAdmin,
				Type:         "access",
				SessionID:    "session-wrong-subject",
				Iss:          manager.issuer,
				Sub:          "7",
				Aud:          manager.audience,
				Exp:          now.Add(time.Hour).Unix(),
				Nbf:          now.Unix(),
				Iat:          now.Unix(),
				Jti:          "wrong-subject",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			token, err := manager.generateToken(test.payload, manager.accessSecret)
			if err != nil {
				t.Fatalf("generate token: %v", err)
			}
			if _, err := manager.VerifyAccessToken(token); err == nil {
				t.Fatal("expected token verification to fail")
			}
		})
	}
}

func signedLegacyRoleToken(
	manager *SimpleJWTManager,
	role string,
	tokenType string,
	secret string,
) (string, error) {
	now := time.Now()
	header, err := json.Marshal(JWTHeader{Alg: "HS256", Typ: "JWT"})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]any{
		"user_id": 42,
		"role":    role,
		"type":    tokenType,
		"sid":     "session-test-42",
		"iss":     manager.issuer,
		"sub":     "42",
		"aud":     manager.audience,
		"exp":     now.Add(time.Hour).Unix(),
		"nbf":     now.Unix(),
		"iat":     now.Unix(),
		"jti":     "historical-role-token",
	})
	if err != nil {
		return "", err
	}
	message := base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload)
	return message + "." + manager.sign(message, secret), nil
}
