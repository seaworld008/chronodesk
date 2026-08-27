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

type sessionTestProfileRepo struct{}

func (sessionTestProfileRepo) Create(context.Context, *UserProfile) error {
	return nil
}

func (sessionTestProfileRepo) GetByUserID(
	_ context.Context,
	userID uint,
) (*UserProfile, error) {
	return &UserProfile{UserID: userID}, nil
}

func (sessionTestProfileRepo) Patch(
	context.Context,
	uint,
	ProfilePatch,
) error {
	return nil
}

func (sessionTestProfileRepo) Delete(context.Context, uint) error {
	return nil
}

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
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("open session SQL database: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(
		&models.User{},
		&models.LoginHistory{},
		&models.OTPTrustedDevice{},
		&RefreshToken{},
	); err != nil {
		t.Fatalf("migrate refresh token session table: %v", err)
	}
	for _, user := range []models.User{
		{
			ID:            42,
			Username:      "session-admin",
			Email:         "session-admin@example.test",
			PasswordHash:  "not-used",
			PlatformRole:  models.PlatformRolePlatformAdmin,
			Status:        models.UserStatusActive,
			EmailVerified: true,
		},
		{
			ID:            84,
			Username:      "session-member",
			Email:         "session-member@example.test",
			PasswordHash:  "not-used",
			PlatformRole:  models.PlatformRoleMember,
			Status:        models.UserStatusActive,
			EmailVerified: true,
		},
	} {
		if err := db.Create(&user).Error; err != nil {
			t.Fatalf("seed session user: %v", err)
		}
	}
	repository := &GormTokenRepository{db: db}
	manager := mustTestJWTManager(t, time.Hour, 24*time.Hour)
	service := &AuthService{
		userRepo: &sessionTestUserRepo{
			users: map[uint]*User{
				42: {
					ID:           42,
					PlatformRole: PlatformRolePlatformAdmin,
					Status:       StatusActive,
				},
				84: {
					ID:           84,
					PlatformRole: PlatformRoleMember,
					Status:       StatusActive,
				},
			},
		},
		profileRepo:        sessionTestProfileRepo{},
		tokenRepo:          repository,
		loginHistoryRepo:   NewGormLoginHistoryRepository(db),
		jwtManager:         manager,
		emailConfigService: sessionTestEmailConfig{},
		config: &AuthConfig{
			AccessTokenExpire:  time.Hour,
			RefreshTokenExpire: 24 * time.Hour,
		},
	}
	return repository, manager, NewAuthHandler(
		service,
		nil,
		WithAllowedBrowserOrigin(testBrowserOrigin),
	)
}

func issueSessionTokens(
	t *testing.T,
	repository *GormTokenRepository,
	manager *SimpleJWTManager,
	userID uint,
	platformRole PlatformRole,
	sessionID string,
) (string, string) {
	t.Helper()
	accessToken, refreshToken, err := manager.GenerateTokenPair(
		userID,
		platformRole,
		sessionID,
	)
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
		PlatformRolePlatformAdmin,
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

func TestLogoutKeepsTrustedDeviceCredentialForTheNextLogin(t *testing.T) {
	repository, manager, handler := setupSessionRevocationTest(t)
	_, refreshToken := issueSessionTokens(
		t,
		repository,
		manager,
		42,
		PlatformRolePlatformAdmin,
		"session-with-trusted-device",
	)

	gin.SetMode(gin.TestMode)
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
		"session-with-trusted-device",
	)
	request.AddCookie(&http.Cookie{
		Name:  refreshTokenCookieName,
		Value: refreshToken,
		Path:  refreshTokenCookiePath,
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("logout status = %d; body=%s", response.Code, response.Body)
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == trustedDeviceCookieName {
			t.Fatalf(
				"普通退出错误清除了可信设备凭据: MaxAge=%d Expires=%s",
				cookie.MaxAge,
				cookie.Expires,
			)
		}
	}
	refreshCleared := false
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == refreshTokenCookieName && cookie.MaxAge < 0 {
			refreshCleared = true
		}
	}
	if !refreshCleared {
		t.Fatal("普通退出未清除 refresh Cookie")
	}
}

func TestLogoutAllRevokesEveryUserSessionOnly(t *testing.T) {
	repository, manager, handler := setupSessionRevocationTest(t)
	firstAccess, _ := issueSessionTokens(
		t, repository, manager, 42, PlatformRolePlatformAdmin, "session-admin-one",
	)
	secondAccess, _ := issueSessionTokens(
		t, repository, manager, 42, PlatformRolePlatformAdmin, "session-admin-two",
	)
	otherAccess, _ := issueSessionTokens(
		t, repository, manager, 84, PlatformRoleMember, "session-other-user",
	)
	for _, device := range []models.OTPTrustedDevice{
		{
			UserID:          42,
			DeviceTokenHash: hashTrustedDeviceToken("admin-device-one"),
			DeviceName:      "Admin device one",
			LastUsedAt:      time.Now(),
			ExpiresAt:       time.Now().Add(time.Hour),
		},
		{
			UserID:          42,
			DeviceTokenHash: hashTrustedDeviceToken("admin-device-two"),
			DeviceName:      "Admin device two",
			LastUsedAt:      time.Now(),
			ExpiresAt:       time.Now().Add(time.Hour),
		},
		{
			UserID:          84,
			DeviceTokenHash: hashTrustedDeviceToken("other-user-device"),
			DeviceName:      "Other user device",
			LastUsedAt:      time.Now(),
			ExpiresAt:       time.Now().Add(time.Hour),
		},
	} {
		if err := repository.db.Create(&device).Error; err != nil {
			t.Fatalf("persist trusted device: %v", err)
		}
	}

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
	var targetActive, otherActive int64
	if err := repository.db.Model(&models.OTPTrustedDevice{}).
		Where("user_id = ? AND revoked = ?", 42, false).
		Count(&targetActive).Error; err != nil {
		t.Fatal(err)
	}
	if err := repository.db.Model(&models.OTPTrustedDevice{}).
		Where("user_id = ? AND revoked = ?", 84, false).
		Count(&otherActive).Error; err != nil {
		t.Fatal(err)
	}
	if targetActive != 0 || otherActive != 1 {
		t.Fatalf(
			"active trusted devices after logout-all = target:%d other:%d, want 0/1",
			targetActive,
			otherActive,
		)
	}
}

func TestRevokeAllSessionsIssuedBeforeUsesOriginalSessionTime(t *testing.T) {
	repository, manager, handler := setupSessionRevocationTest(t)
	oldAccess, oldRefresh := issueSessionTokens(
		t,
		repository,
		manager,
		42,
		PlatformRolePlatformAdmin,
		"pre-cutover-session",
	)
	newAccess, newRefresh := issueSessionTokens(
		t,
		repository,
		manager,
		84,
		PlatformRoleMember,
		"post-cutover-session",
	)
	cutoff := time.Now().UTC().Add(-time.Hour)
	oldLogin := cutoff.Add(-time.Hour)
	if err := repository.db.Model(&models.LoginHistory{}).
		Where(
			"user_id = ? AND session_id = ?",
			42,
			"pre-cutover-session",
		).
		Updates(map[string]interface{}{
			"login_time":       oldLogin,
			"last_activity_at": oldLogin,
		}).Error; err != nil {
		t.Fatalf("backdate pre-cutover session: %v", err)
	}

	revoked, err := handler.authService.RevokeAllSessionsIssuedBefore(
		context.Background(),
		cutoff,
	)
	if err != nil {
		t.Fatalf("revoke pre-cutover sessions: %v", err)
	}
	if revoked != 1 {
		t.Fatalf("revoked sessions = %d, want 1", revoked)
	}
	if _, err := repository.GetRefreshToken(
		context.Background(),
		oldRefresh,
	); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("pre-cutover refresh remains active: %v", err)
	}
	if _, err := repository.GetRefreshToken(
		context.Background(),
		newRefresh,
	); err != nil {
		t.Fatalf("post-cutover refresh was revoked: %v", err)
	}
	if status, problem := protectedRequest(
		t,
		handler,
		oldAccess,
	); status != http.StatusUnauthorized ||
		problem.Error != "session_revoked" {
		t.Fatalf(
			"pre-cutover access status = %d, problem=%+v",
			status,
			problem,
		)
	}
	if status, problem := protectedRequest(
		t,
		handler,
		newAccess,
	); status != http.StatusOK {
		t.Fatalf(
			"post-cutover access status = %d, problem=%+v",
			status,
			problem,
		)
	}
	repeated, err := handler.authService.RevokeAllSessionsIssuedBefore(
		context.Background(),
		cutoff,
	)
	if err != nil {
		t.Fatalf("repeat cutover: %v", err)
	}
	if repeated != 0 {
		t.Fatalf("repeat cutover revoked %d sessions, want 0", repeated)
	}
	if _, err := handler.authService.RevokeAllSessionsIssuedBefore(
		context.Background(),
		time.Time{},
	); !errors.Is(err, ErrInvalidSessionCutoff) {
		t.Fatalf("zero cutover error = %v", err)
	}
}

func TestRevokedSessionCannotBeResurrectedByLateRefreshWrite(t *testing.T) {
	repository, manager, handler := setupSessionRevocationTest(t)
	accessToken, refreshToken := issueSessionTokens(
		t,
		repository,
		manager,
		42,
		PlatformRolePlatformAdmin,
		"session-refresh-race",
	)
	if err := handler.authService.Logout(context.Background(), refreshToken); err != nil {
		t.Fatalf("logout: %v", err)
	}

	// Simulate a refresh request that passed its initial check immediately before
	// logout and committed a new refresh token afterwards. The immutable sid is
	// still revoked by login_histories, so this row must not reactivate access.
	_, lateRefreshToken, err := manager.GenerateTokenPair(
		42,
		PlatformRolePlatformAdmin,
		"session-refresh-race",
	)
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
	manager := mustTestJWTManager(t, time.Hour, 24*time.Hour)
	if _, _, err := manager.GenerateTokenPair(
		42,
		PlatformRolePlatformAdmin,
		"",
	); err == nil {
		t.Fatal("token pair without session id succeeded")
	}
	for _, historicalRole := range []PlatformRole{
		PlatformRole("admin"),
		PlatformRole("supervisor"),
		PlatformRole("agent"),
		PlatformRole("customer"),
	} {
		if _, _, err := manager.GenerateTokenPair(
			42,
			historicalRole,
			"session-invalid-human-role",
		); err == nil {
			t.Errorf("token pair with historical role %q succeeded", historicalRole)
		}
	}

	now := time.Now()
	token, err := manager.generateToken(&JWTPayload{
		UserID:       42,
		PlatformRole: PlatformRolePlatformAdmin,
		Type:         "access",
		Iss:          manager.issuer,
		Sub:          "42",
		Aud:          manager.audience,
		Exp:          now.Add(time.Hour).Unix(),
		Nbf:          now.Unix(),
		Iat:          now.Unix(),
		Jti:          "legacy-token-without-session",
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
		PlatformRolePlatformAdmin,
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

func TestRefreshTokenPlatformRoleMismatchIsRejectedAndRevokesSession(t *testing.T) {
	repository, manager, handler := setupSessionRevocationTest(t)
	_, refreshToken := issueSessionTokens(
		t,
		repository,
		manager,
		42,
		PlatformRolePlatformAdmin,
		"session-before-role-change",
	)

	userRepo := handler.authService.userRepo.(*sessionTestUserRepo)
	userRepo.users[42].PlatformRole = PlatformRoleMember

	if _, err := handler.authService.RefreshToken(
		context.Background(),
		&RefreshTokenRequest{RefreshToken: refreshToken},
		"127.0.0.1",
		"session-test",
	); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("refresh after role change error = %v, want invalid token", err)
	}

	active, err := repository.IsSessionActive(context.Background(), 42, "session-before-role-change")
	if err != nil {
		t.Fatalf("check revoked session: %v", err)
	}
	if active {
		t.Fatal("role change left the old refresh session active")
	}
}

func TestRefreshTokenCanOnlyBeConsumedOnce(t *testing.T) {
	repository, manager, _ := setupSessionRevocationTest(t)
	_, refreshToken := issueSessionTokens(
		t,
		repository,
		manager,
		42,
		PlatformRolePlatformAdmin,
		"session-refresh-replay",
	)

	if err := repository.RevokeRefreshToken(context.Background(), refreshToken); err != nil {
		t.Fatalf("first refresh token consumption failed: %v", err)
	}
	if err := repository.RevokeRefreshToken(context.Background(), refreshToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("second refresh token consumption error = %v, want invalid token", err)
	}
}
