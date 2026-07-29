package auth

import (
	"context"
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

func TestRequireAuthRevalidatesCurrentUserStateAndRole(t *testing.T) {
	gin.SetMode(gin.TestMode)

	passwordChangedAt := time.Now().Add(time.Minute)
	tests := []struct {
		name          string
		currentUser   *User
		repoError     error
		tokenRole     UserRole
		sessionActive bool
		sessionError  error
		wantStatus    int
		wantErrorCode string
	}{
		{
			name: "active user with current role is accepted",
			currentUser: &User{
				ID:     42,
				Role:   RoleAdmin,
				Status: StatusActive,
			},
			tokenRole:     RoleAdmin,
			sessionActive: true,
			wantStatus:    http.StatusOK,
		},
		{
			name: "inactive user access token is rejected immediately",
			currentUser: &User{
				ID:     42,
				Role:   RoleAdmin,
				Status: StatusInactive,
			},
			tokenRole:     RoleAdmin,
			sessionActive: true,
			wantStatus:    http.StatusUnauthorized,
			wantErrorCode: "account_inactive",
		},
		{
			name:          "deleted user access token is rejected immediately",
			currentUser:   nil,
			tokenRole:     RoleAdmin,
			sessionActive: true,
			wantStatus:    http.StatusUnauthorized,
			wantErrorCode: "invalid_token",
		},
		{
			name:          "repository outage fails closed without invalidating the session",
			repoError:     errors.New("database unavailable"),
			tokenRole:     RoleAdmin,
			sessionActive: true,
			wantStatus:    http.StatusServiceUnavailable,
			wantErrorCode: "authentication_unavailable",
		},
		{
			name: "role downgrade invalidates old administrator token",
			currentUser: &User{
				ID:     42,
				Role:   RoleAgent,
				Status: StatusActive,
			},
			tokenRole:     RoleAdmin,
			sessionActive: true,
			wantStatus:    http.StatusUnauthorized,
			wantErrorCode: "stale_token",
		},
		{
			name: "password change invalidates old access token",
			currentUser: &User{
				ID:                42,
				Role:              RoleAdmin,
				Status:            StatusActive,
				PasswordChangedAt: &passwordChangedAt,
			},
			tokenRole:     RoleAdmin,
			sessionActive: true,
			wantStatus:    http.StatusUnauthorized,
			wantErrorCode: "stale_token",
		},
		{
			name: "revoked login session invalidates access token",
			currentUser: &User{
				ID:     42,
				Role:   RoleAdmin,
				Status: StatusActive,
			},
			tokenRole:     RoleAdmin,
			sessionActive: false,
			wantStatus:    http.StatusUnauthorized,
			wantErrorCode: "session_revoked",
		},
		{
			name: "session repository outage fails closed",
			currentUser: &User{
				ID:     42,
				Role:   RoleAdmin,
				Status: StatusActive,
			},
			tokenRole:     RoleAdmin,
			sessionActive: true,
			sessionError:  errors.New("session database unavailable"),
			wantStatus:    http.StatusServiceUnavailable,
			wantErrorCode: "authentication_unavailable",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			jwtManager := NewSimpleJWTManager("test-access-secret", "test-refresh-secret", time.Hour, time.Hour)
			accessToken, _, err := jwtManager.GenerateTokenPair(42, test.tokenRole, "session-test-42")
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
				role, _ := c.Get("user_role")
				c.JSON(http.StatusOK, gin.H{"role": role})
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
	manager := NewSimpleJWTManager("test-access-secret", "test-refresh-secret", time.Hour, time.Hour)
	now := time.Now()

	tests := []struct {
		name    string
		payload *JWTPayload
	}{
		{
			name: "wrong audience",
			payload: &JWTPayload{
				UserID:    42,
				Role:      RoleAdmin,
				Type:      "access",
				SessionID: "session-wrong-audience",
				Iss:       manager.issuer,
				Sub:       "42",
				Aud:       "another-api",
				Exp:       now.Add(time.Hour).Unix(),
				Nbf:       now.Unix(),
				Iat:       now.Unix(),
				Jti:       "wrong-audience",
			},
		},
		{
			name: "subject does not match user",
			payload: &JWTPayload{
				UserID:    42,
				Role:      RoleAdmin,
				Type:      "access",
				SessionID: "session-wrong-subject",
				Iss:       manager.issuer,
				Sub:       "7",
				Aud:       "ticket-system-api",
				Exp:       now.Add(time.Hour).Unix(),
				Nbf:       now.Unix(),
				Iat:       now.Unix(),
				Jti:       "wrong-subject",
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
