package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type sessionTestUserRepo struct {
	UserRepository
	users map[uint]*User
}

type sessionTestEmailConfig struct{}

func (sessionTestEmailConfig) IsEmailVerificationEnabled(context.Context) (bool, error) {
	return false, nil
}

func (sessionTestEmailConfig) CanSendEmail(context.Context) (bool, error) {
	return false, nil
}

func (r *sessionTestUserRepo) GetByID(_ context.Context, id uint) (*User, error) {
	user := r.users[id]
	if user == nil {
		return nil, ErrUserNotFound
	}
	copy := *user
	return &copy, nil
}

func setupSessionRevocationTest(t *testing.T) (*GormTokenRepository, *SimpleJWTManager, *AuthHandler) {
	t.Helper()
	dsn := fmt.Sprintf("file:auth-session-%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open session database: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.LoginHistory{}, &RefreshToken{}); err != nil {
		t.Fatalf("migrate refresh token session table: %v", err)
	}
	repository := &GormTokenRepository{db: db}
	manager := NewSimpleJWTManager(
		"session-test-access-secret",
		"session-test-refresh-secret",
		time.Hour,
		24*time.Hour,
	)
	service := &AuthService{
		userRepo: &sessionTestUserRepo{
			users: map[uint]*User{
				42: {
					ID:     42,
					Role:   RoleAdmin,
					Status: StatusActive,
				},
				84: {
					ID:     84,
					Role:   RoleUser,
					Status: StatusActive,
				},
			},
		},
		tokenRepo:          repository,
		jwtManager:         manager,
		emailConfigService: sessionTestEmailConfig{},
		config: &AuthConfig{
			AccessTokenExpire:  time.Hour,
			RefreshTokenExpire: 24 * time.Hour,
		},
	}
	return repository, manager, NewAuthHandler(service, nil)
}

func issueSessionTokens(
	t *testing.T,
	repository *GormTokenRepository,
	manager *SimpleJWTManager,
	userID uint,
	role UserRole,
	sessionID string,
) (string, string) {
	t.Helper()
	accessToken, refreshToken, err := manager.GenerateTokenPair(userID, role, sessionID)
	if err != nil {
		t.Fatalf("generate session tokens: %v", err)
	}
	if err := repository.CreateRefreshToken(context.Background(), &RefreshToken{
		UserID:    userID,
		Token:     refreshToken,
		SessionID: sessionID,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("persist refresh token: %v", err)
	}
	now := time.Now()
	if err := repository.db.Create(&models.LoginHistory{
		UserID:         userID,
		Username:       fmt.Sprintf("user-%d", userID),
		Email:          fmt.Sprintf("user-%d@example.com", userID),
		IPAddress:      "127.0.0.1",
		LoginTime:      now,
		LastActivityAt: &now,
		SessionID:      sessionID,
		LoginStatus:    models.LoginStatusSuccess,
		IsActive:       true,
	}).Error; err != nil {
		t.Fatalf("persist login session: %v", err)
	}
	return accessToken, refreshToken
}

func protectedRequest(t *testing.T, handler *AuthHandler, accessToken string) (int, ErrorResponse) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/protected", func(c *gin.Context) {
		handler.RequireAuth(NewGinHTTPContext(c))
		if c.IsAborted() {
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	var problem ErrorResponse
	if response.Code != http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
			t.Fatalf("decode authentication error: %v; body=%s", err, response.Body.String())
		}
	}
	return response.Code, problem
}

func TestLogoutRevokesAccessTokenSessionImmediately(t *testing.T) {
	repository, manager, handler := setupSessionRevocationTest(t)
	accessToken, refreshToken := issueSessionTokens(
		t,
		repository,
		manager,
		42,
		RoleAdmin,
		"session-single-device",
	)

	if status, problem := protectedRequest(t, handler, accessToken); status != http.StatusOK {
		t.Fatalf("active session status = %d, problem=%+v", status, problem)
	}
	if err := handler.authService.Logout(context.Background(), refreshToken); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if status, problem := protectedRequest(t, handler, accessToken); status != http.StatusUnauthorized ||
		problem.Error != "session_revoked" {
		t.Fatalf("revoked session status = %d, problem=%+v", status, problem)
	}
	active, err := repository.IsSessionActive(context.Background(), 42, "session-single-device")
	if err != nil {
		t.Fatalf("check session: %v", err)
	}
	if active {
		t.Fatal("logged-out session remains active in database")
	}
}

func TestLogoutAllRevokesEveryUserSessionOnly(t *testing.T) {
	repository, manager, handler := setupSessionRevocationTest(t)
	firstAccess, _ := issueSessionTokens(t, repository, manager, 42, RoleAdmin, "session-admin-one")
	secondAccess, _ := issueSessionTokens(t, repository, manager, 42, RoleAdmin, "session-admin-two")
	otherAccess, _ := issueSessionTokens(t, repository, manager, 84, RoleUser, "session-other-user")

	if err := handler.authService.LogoutAll(context.Background(), 42); err != nil {
		t.Fatalf("logout all: %v", err)
	}
	for name, accessToken := range map[string]string{
		"first":  firstAccess,
		"second": secondAccess,
	} {
		t.Run(name, func(t *testing.T) {
			if status, problem := protectedRequest(t, handler, accessToken); status != http.StatusUnauthorized ||
				problem.Error != "session_revoked" {
				t.Fatalf("status = %d, problem=%+v", status, problem)
			}
		})
	}
	if status, problem := protectedRequest(t, handler, otherAccess); status != http.StatusOK {
		t.Fatalf("other user's session status = %d, problem=%+v", status, problem)
	}
}

func TestRevokedSessionCannotBeResurrectedByLateRefreshWrite(t *testing.T) {
	repository, manager, handler := setupSessionRevocationTest(t)
	accessToken, refreshToken := issueSessionTokens(
		t,
		repository,
		manager,
		42,
		RoleAdmin,
		"session-refresh-race",
	)
	if err := handler.authService.Logout(context.Background(), refreshToken); err != nil {
		t.Fatalf("logout: %v", err)
	}

	// Simulate a refresh request that passed its initial check immediately before
	// logout and committed a new refresh token afterwards. The immutable sid is
	// still revoked by login_histories, so this row must not reactivate access.
	_, lateRefreshToken, err := manager.GenerateTokenPair(42, RoleAdmin, "session-refresh-race")
	if err != nil {
		t.Fatalf("generate late refresh token: %v", err)
	}
	if err := repository.CreateRefreshToken(context.Background(), &RefreshToken{
		UserID:    42,
		Token:     lateRefreshToken,
		SessionID: "session-refresh-race",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("persist late refresh token: %v", err)
	}

	active, err := repository.IsSessionActive(context.Background(), 42, "session-refresh-race")
	if err != nil {
		t.Fatalf("check session: %v", err)
	}
	if active {
		t.Fatal("late refresh write resurrected a revoked session")
	}
	if status, problem := protectedRequest(t, handler, accessToken); status != http.StatusUnauthorized ||
		problem.Error != "session_revoked" {
		t.Fatalf("status = %d, problem=%+v", status, problem)
	}
}

func TestJWTRequiresPersistentSessionIdentifier(t *testing.T) {
	manager := NewSimpleJWTManager(
		"session-test-access-secret",
		"session-test-refresh-secret",
		time.Hour,
		24*time.Hour,
	)
	if _, _, err := manager.GenerateTokenPair(42, RoleAdmin, ""); err == nil {
		t.Fatal("token pair without session id succeeded")
	}

	now := time.Now()
	token, err := manager.generateToken(&JWTPayload{
		UserID: 42,
		Role:   RoleAdmin,
		Type:   "access",
		Iss:    manager.issuer,
		Sub:    "42",
		Aud:    "ticket-system-api",
		Exp:    now.Add(time.Hour).Unix(),
		Nbf:    now.Unix(),
		Iat:    now.Unix(),
		Jti:    "legacy-token-without-session",
	}, manager.accessSecret)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	if _, err := manager.VerifyAccessToken(token); err == nil {
		t.Fatal("access token without session id was accepted")
	}
}

func TestRefreshTokenIssuedBeforePasswordChangeIsRejected(t *testing.T) {
	repository, manager, handler := setupSessionRevocationTest(t)
	_, refreshToken := issueSessionTokens(
		t,
		repository,
		manager,
		42,
		RoleAdmin,
		"session-before-password-change",
	)

	record, err := repository.GetRefreshToken(context.Background(), refreshToken)
	if err != nil {
		t.Fatalf("load refresh token: %v", err)
	}
	passwordChangedAt := record.CreatedAt.Add(time.Millisecond)
	userRepo := handler.authService.userRepo.(*sessionTestUserRepo)
	userRepo.users[42].PasswordChangedAt = &passwordChangedAt

	if _, err := handler.authService.RefreshToken(
		context.Background(),
		&RefreshTokenRequest{RefreshToken: refreshToken},
		"127.0.0.1",
		"session-test",
	); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("refresh after password change error = %v, want invalid token", err)
	}

	active, err := repository.IsSessionActive(context.Background(), 42, "session-before-password-change")
	if err != nil {
		t.Fatalf("check revoked session: %v", err)
	}
	if active {
		t.Fatal("password change left the old refresh session active")
	}
}

func TestRefreshTokenCanOnlyBeConsumedOnce(t *testing.T) {
	repository, manager, _ := setupSessionRevocationTest(t)
	_, refreshToken := issueSessionTokens(
		t,
		repository,
		manager,
		42,
		RoleAdmin,
		"session-refresh-replay",
	)

	if err := repository.RevokeRefreshToken(context.Background(), refreshToken); err != nil {
		t.Fatalf("first refresh token consumption failed: %v", err)
	}
	if err := repository.RevokeRefreshToken(context.Background(), refreshToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("second refresh token consumption error = %v, want invalid token", err)
	}
}
