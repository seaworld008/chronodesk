package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTrustedDeviceSecurityDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(
			"file:"+strings.ReplaceAll(t.Name(), "/", "-")+
				"?mode=memory&cache=shared",
		),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.LoginHistory{},
		&models.OTPTrustedDevice{},
		&models.EmailConfig{},
		&RefreshToken{},
		&LoginAttempt{},
	); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	return db
}

func seedTrustedDeviceSecurityUser(
	t *testing.T,
	db *gorm.DB,
	userID uint,
) {
	t.Helper()
	if err := db.Create(&models.User{
		ID:            userID,
		Username:      fmt.Sprintf("trusted-security-%d", userID),
		Email:         fmt.Sprintf("trusted-security-%d@example.test", userID),
		PasswordHash:  "not-used",
		PlatformRole:  models.PlatformRoleMember,
		Status:        models.UserStatusActive,
		EmailVerified: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestTrustedDeviceQuotaRevokesExpiredBeforeCountingValidDevices(t *testing.T) {
	db := newTrustedDeviceSecurityDB(t)
	repository := NewGormTrustedDeviceRepository(db)
	service := &AuthService{trustedDeviceRepo: repository}
	const (
		targetUserID = uint(42)
		otherUserID  = uint(84)
	)
	now := time.Now()
	devices := []models.OTPTrustedDevice{
		{
			UserID:          targetUserID,
			DeviceTokenHash: hashTrustedDeviceToken("expired-most-recent"),
			DeviceName:      "Expired but recently used",
			LastUsedAt:      now,
			ExpiresAt:       now.Add(-time.Minute),
		},
		{
			UserID:          targetUserID,
			DeviceTokenHash: hashTrustedDeviceToken("valid-older"),
			DeviceName:      "Valid older",
			LastUsedAt:      now.Add(-2 * time.Hour),
			ExpiresAt:       now.Add(time.Hour),
		},
		{
			UserID:          targetUserID,
			DeviceTokenHash: hashTrustedDeviceToken("valid-newer"),
			DeviceName:      "Valid newer",
			LastUsedAt:      now.Add(-time.Hour),
			ExpiresAt:       now.Add(2 * time.Hour),
		},
		{
			UserID:          otherUserID,
			DeviceTokenHash: hashTrustedDeviceToken("other-expired"),
			DeviceName:      "Other expired",
			LastUsedAt:      now,
			ExpiresAt:       now.Add(-time.Minute),
		},
	}
	if err := db.Create(&devices).Error; err != nil {
		t.Fatal(err)
	}

	service.enforceTrustedDeviceQuota(
		context.Background(),
		targetUserID,
		2,
		now,
	)

	var stored []models.OTPTrustedDevice
	if err := db.Order("id ASC").Find(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if len(stored) != 4 {
		t.Fatalf("trusted device rows = %d, want 4", len(stored))
	}
	if !stored[0].Revoked {
		t.Fatal("expired target device was not cleaned before quota enforcement")
	}
	if stored[1].Revoked || stored[2].Revoked {
		t.Fatal("expired device consumed quota and evicted a valid device")
	}
	if stored[3].Revoked {
		t.Fatal("quota cleanup escaped the target user")
	}
}

func TestTrustedDeviceUpdateCannotResurrectARevokedRow(t *testing.T) {
	db := newTrustedDeviceSecurityDB(t)
	repository := NewGormTrustedDeviceRepository(db)
	now := time.Now()
	device := models.OTPTrustedDevice{
		UserID:          42,
		DeviceTokenHash: hashTrustedDeviceToken("stale-device-update"),
		DeviceName:      "Stale device",
		LastUsedAt:      now,
		LastIP:          "192.0.2.1",
		ExpiresAt:       now.Add(time.Hour),
	}
	if err := db.Create(&device).Error; err != nil {
		t.Fatal(err)
	}
	stale := device
	if err := db.Model(&models.OTPTrustedDevice{}).
		Where("id = ?", device.ID).
		Update("revoked", true).Error; err != nil {
		t.Fatal(err)
	}

	stale.LastIP = "198.51.100.9"
	stale.Revoked = false
	if err := repository.Update(
		context.Background(),
		&stale,
	); !errors.Is(err, ErrTrustedDeviceInvalid) {
		t.Fatalf("stale trusted-device update error = %v", err)
	}
	var stored models.OTPTrustedDevice
	if err := db.First(&stored, device.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !stored.Revoked || stored.LastIP != device.LastIP {
		t.Fatalf("stale update resurrected or changed revoked row: %+v", stored)
	}
}

func TestGormAtomicLoginCommitRevalidatesTrustedDeviceBeforeSession(t *testing.T) {
	db := newTrustedDeviceSecurityDB(t)
	const userID = uint(42)
	seedTrustedDeviceSecurityUser(t, db, userID)
	repository := NewGormTokenRepository(db).(*GormTokenRepository)
	now := time.Now()
	deviceToken := "gorm-atomic-trusted-device"
	deviceHash := hashTrustedDeviceToken(deviceToken)
	if err := db.Create(&models.OTPTrustedDevice{
		UserID:          userID,
		DeviceTokenHash: deviceHash,
		DeviceName:      "Atomic trusted device",
		LastUsedAt:      now,
		ExpiresAt:       now.Add(time.Hour),
	}).Error; err != nil {
		t.Fatal(err)
	}

	first := atomicTrustedLoginCommitForTest(
		userID,
		"gorm-atomic-session-first",
		"gorm-atomic-refresh-first",
		deviceHash,
		now,
	)
	if err := repository.CommitLoginSession(
		context.Background(),
		first,
	); err != nil {
		t.Fatal(err)
	}
	active, err := repository.IsSessionActive(
		context.Background(),
		userID,
		first.RefreshToken.SessionID,
	)
	if err != nil || !active {
		t.Fatalf("committed atomic session active/error = %v/%v", active, err)
	}
	if err := repository.RevokeAllUserTokens(
		context.Background(),
		userID,
	); err != nil {
		t.Fatal(err)
	}

	second := atomicTrustedLoginCommitForTest(
		userID,
		"gorm-atomic-session-second",
		"gorm-atomic-refresh-second",
		deviceHash,
		now.Add(time.Minute),
	)
	if err := repository.CommitLoginSession(
		context.Background(),
		second,
	); !errors.Is(err, ErrTrustedDeviceInvalid) {
		t.Fatalf("revoked trusted-device commit error = %v", err)
	}
	var refreshCount, historyCount, attemptCount int64
	if err := db.Model(&RefreshToken{}).Count(&refreshCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.LoginHistory{}).
		Count(&historyCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&LoginAttempt{}).
		Count(&attemptCount).Error; err != nil {
		t.Fatal(err)
	}
	if refreshCount != 1 || historyCount != 1 || attemptCount != 1 {
		t.Fatalf(
			"failed revalidation left refresh/history/attempt = %d/%d/%d, want 1/1/1",
			refreshCount,
			historyCount,
			attemptCount,
		)
	}
	var storedUser models.User
	if err := db.Select("last_login_at").First(&storedUser, userID).Error; err != nil {
		t.Fatal(err)
	}
	if storedUser.LastLoginAt == nil ||
		!storedUser.LastLoginAt.Equal(first.CommittedAt) {
		t.Fatalf(
			"failed second commit changed last_login_at = %v, want %v",
			storedUser.LastLoginAt,
			first.CommittedAt,
		)
	}
}

func atomicTrustedLoginCommitForTest(
	userID uint,
	sessionID,
	refreshToken,
	deviceHash string,
	at time.Time,
) *LoginSessionCommit {
	return &LoginSessionCommit{
		UserID:      userID,
		CommittedAt: at,
		ExpectedPrincipal: &LoginPrincipalSnapshot{
			Email:         "trusted-security-42@example.test",
			PasswordHash:  "not-used",
			PlatformRole:  PlatformRoleMember,
			Status:        StatusActive,
			EmailVerified: true,
		},
		ExpectedEmailPolicy: &EmailVerificationPolicySnapshot{
			Enabled: false,
		},
		RefreshToken: &RefreshToken{
			UserID:    userID,
			Token:     refreshToken,
			SessionID: sessionID,
			ExpiresAt: at.Add(time.Hour),
			CreatedAt: at,
		},
		LoginHistory: &models.LoginHistory{
			UserID:         userID,
			Username:       "gorm-atomic-login",
			Email:          "trusted-security-42@example.test",
			LoginTime:      at,
			LastActivityAt: &at,
			SessionID:      sessionID,
			LoginStatus:    models.LoginStatusSuccess,
			LoginMethod:    models.LoginMethodPasswordTrusted,
			IsActive:       true,
		},
		SuccessfulAttempt: &LoginAttempt{
			UserID:    &userID,
			Email:     "trusted-security-42@example.test",
			IPAddress: "127.0.0.1",
			UserAgent: "Gorm atomic login test",
			Success:   true,
			CreatedAt: at,
		},
		TrustedDeviceTokenHash: deviceHash,
		TrustedDeviceIP:        "127.0.0.1",
		TrustedDeviceUserAgent: "Gorm atomic login test",
	}
}

func TestLogoutAllRollsBackSessionsWhenTrustedDeviceRevocationFails(t *testing.T) {
	db := newTrustedDeviceSecurityDB(t)
	repository := NewGormTokenRepository(db)
	const userID = uint(42)
	seedTrustedDeviceSecurityUser(t, db, userID)
	if err := repository.CreateRefreshToken(
		context.Background(),
		&RefreshToken{
			UserID:    userID,
			Token:     "logout-all-rollback-refresh",
			SessionID: "logout-all-rollback-session",
			ExpiresAt: time.Now().Add(time.Hour),
		},
	); err != nil {
		t.Fatal(err)
	}
	loginAt := time.Now()
	if err := db.Create(&models.LoginHistory{
		UserID:         userID,
		Username:       "logout-all-rollback",
		Email:          "logout-all-rollback@example.test",
		LoginTime:      loginAt,
		LastActivityAt: &loginAt,
		SessionID:      "logout-all-rollback-session",
		LoginStatus:    models.LoginStatusSuccess,
		IsActive:       true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.OTPTrustedDevice{
		UserID:          userID,
		DeviceTokenHash: hashTrustedDeviceToken("logout-all-rollback-device"),
		DeviceName:      "Rollback device",
		LastUsedAt:      loginAt,
		ExpiresAt:       loginAt.Add(time.Hour),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		CREATE TRIGGER reject_logout_all_trusted_device
		BEFORE UPDATE ON otp_trusted_devices
		BEGIN
			SELECT RAISE(FAIL, 'injected trusted-device failure');
		END
	`).Error; err != nil {
		t.Fatal(err)
	}

	if err := repository.RevokeAllUserTokens(
		context.Background(),
		userID,
	); err == nil {
		t.Fatal("logout-all unexpectedly committed after trusted-device failure")
	}
	var activeRefresh, activeHistory, activeDevice int64
	if err := db.Model(&RefreshToken{}).
		Where("user_id = ? AND revoked = ?", userID, false).
		Count(&activeRefresh).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.LoginHistory{}).
		Where("user_id = ? AND is_active = ?", userID, true).
		Count(&activeHistory).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.OTPTrustedDevice{}).
		Where("user_id = ? AND revoked = ?", userID, false).
		Count(&activeDevice).Error; err != nil {
		t.Fatal(err)
	}
	if activeRefresh != 1 || activeHistory != 1 || activeDevice != 1 {
		t.Fatalf(
			"rollback active refresh/history/device = %d/%d/%d, want 1/1/1",
			activeRefresh,
			activeHistory,
			activeDevice,
		)
	}
}

func TestLogoutAllMakesAnOldTrustedDeviceCookieRequireOTP(t *testing.T) {
	db := newTrustedDeviceSecurityDB(t)
	const (
		userID           = uint(42)
		otherUserID      = uint(84)
		email            = "logout-all-trusted@example.test"
		password         = "CorrectPassword123!"
		oldDeviceToken   = "logout-all-old-device-cookie"
		secondToken      = "logout-all-second-device-cookie"
		otherDeviceToken = "logout-all-other-user-cookie"
	)
	seedTrustedDeviceSecurityUser(t, db, userID)
	seedTrustedDeviceSecurityUser(t, db, otherUserID)
	now := time.Now()
	for _, device := range []models.OTPTrustedDevice{
		{
			UserID:          userID,
			DeviceTokenHash: hashTrustedDeviceToken(oldDeviceToken),
			DeviceName:      "Old browser",
			LastUsedAt:      now,
			ExpiresAt:       now.Add(time.Hour),
		},
		{
			UserID:          userID,
			DeviceTokenHash: hashTrustedDeviceToken(secondToken),
			DeviceName:      "Second browser",
			LastUsedAt:      now,
			ExpiresAt:       now.Add(time.Hour),
		},
		{
			UserID:          otherUserID,
			DeviceTokenHash: hashTrustedDeviceToken(otherDeviceToken),
			DeviceName:      "Other user browser",
			LastUsedAt:      now,
			ExpiresAt:       now.Add(time.Hour),
		},
	} {
		if err := db.Create(&device).Error; err != nil {
			t.Fatal(err)
		}
	}

	userRepository := &trustedLoginUserRepository{
		user: &User{
			ID:            userID,
			Username:      "logout-all-trusted",
			Email:         email,
			PasswordHash:  password,
			PlatformRole:  PlatformRoleMember,
			Status:        StatusActive,
			EmailVerified: true,
			OTPEnabled:    true,
			OTPSecret:     "otp-secret",
		},
	}
	service := NewAuthService(
		userRepository,
		&trustedLoginProfileRepository{},
		NewGormTokenRepository(db),
		&trustedLoginAttemptRepository{},
		NewGormLoginHistoryRepository(db),
		NewGormTrustedDeviceRepository(db),
		nil,
		otpAuditEmailConfig{},
		trustedLoginOTPService{},
		trustedLoginPasswordService{},
		mustTestJWTManager(t, time.Hour, 24*time.Hour),
		&AuthConfig{
			AccessTokenExpire:        time.Hour,
			RefreshTokenExpire:       24 * time.Hour,
			MaxFailedLogins:          5,
			RequireEmailVerification: false,
		},
	)
	if err := service.LogoutAll(context.Background(), userID); err != nil {
		t.Fatalf("logout all: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewAuthHandler(
		service,
		nil,
		WithAllowedBrowserOrigin(testBrowserOrigin),
	)
	router.POST("/api/auth/login", func(c *gin.Context) {
		handler.Login(NewGinHTTPContext(c))
	})
	body, err := json.Marshal(LoginRequest{Email: email, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/login",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", testBrowserOrigin)
	request.AddCookie(&http.Cookie{
		Name:  trustedDeviceCookieName,
		Value: oldDeviceToken,
		Path:  trustedDeviceCookiePath,
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), "OTP") {
		t.Fatalf(
			"old trusted cookie login status/body = %d/%s, want OTP requirement",
			response.Code,
			response.Body,
		)
	}
	var targetActive, otherActive int64
	if err := db.Model(&models.OTPTrustedDevice{}).
		Where("user_id = ? AND revoked = ?", userID, false).
		Count(&targetActive).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.OTPTrustedDevice{}).
		Where("user_id = ? AND revoked = ?", otherUserID, false).
		Count(&otherActive).Error; err != nil {
		t.Fatal(err)
	}
	if targetActive != 0 || otherActive != 1 {
		t.Fatalf(
			"trusted device scope = target:%d other:%d, want 0/1",
			targetActive,
			otherActive,
		)
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == trustedDeviceCookieName && cookie.MaxAge < 0 {
			return
		}
	}
	t.Fatalf(
		"rejected old trusted cookie was not cleared: %v",
		response.Header().Values("Set-Cookie"),
	)
}
