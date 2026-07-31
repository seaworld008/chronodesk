package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type otpAuditUserRepository struct {
	UserRepository
	user *User
}

func (repository *otpAuditUserRepository) GetByEmail(
	_ context.Context,
	email string,
) (*User, error) {
	if repository.user == nil || repository.user.Email != email {
		return nil, ErrUserNotFound
	}
	copied := *repository.user
	return &copied, nil
}

func (*otpAuditUserRepository) IncrementFailedLogin(context.Context, uint) error {
	return nil
}

type otpAuditAttemptRepository struct {
	LoginAttemptRepository
	attempts []*LoginAttempt
}

func (repository *otpAuditAttemptRepository) Create(
	_ context.Context,
	attempt *LoginAttempt,
) error {
	copied := *attempt
	repository.attempts = append(repository.attempts, &copied)
	return nil
}

func (*otpAuditAttemptRepository) GetRecentFailedAttempts(
	context.Context,
	string,
	time.Time,
) (int, error) {
	return 0, nil
}

type otpAuditEmailConfig struct{}

func (otpAuditEmailConfig) IsEmailVerificationEnabled(context.Context) (bool, error) {
	return false, nil
}

func (otpAuditEmailConfig) CanSendEmail(context.Context) (bool, error) {
	return false, nil
}

func TestOTPRequiredLoginPersistsAuditWithoutChangingResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf(
		"file:otp-required-login-audit-%d?mode=memory&cache=shared&_foreign_keys=on",
		time.Now().UnixNano(),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open audit database: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.LoginHistory{}); err != nil {
		t.Fatalf("migrate audit database: %v", err)
	}

	const (
		userID   = uint(42)
		email    = "otp-audit@example.test"
		password = "CorrectPassword123!"
	)
	passwordService := mustTestPasswordService(t)
	passwordHash, err := passwordService.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := db.Create(&models.User{
		ID:            userID,
		Username:      "otp-audit",
		Email:         email,
		PasswordHash:  passwordHash,
		PlatformRole:  models.PlatformRoleMember,
		Status:        models.UserStatusActive,
		EmailVerified: true,
	}).Error; err != nil {
		t.Fatalf("seed audit user: %v", err)
	}

	authUser := &User{
		ID:            userID,
		Username:      "otp-audit",
		Email:         email,
		PasswordHash:  passwordHash,
		PlatformRole:  PlatformRoleMember,
		Status:        StatusActive,
		EmailVerified: true,
		OTPEnabled:    true,
		OTPSecret:     "encrypted-test-secret",
	}
	attempts := &otpAuditAttemptRepository{}
	service := &AuthService{
		userRepo:           &otpAuditUserRepository{user: authUser},
		loginAttemptRepo:   attempts,
		loginHistoryRepo:   NewGormLoginHistoryRepository(db),
		emailConfigService: otpAuditEmailConfig{},
		passwordService:    passwordService,
		config: &AuthConfig{
			MaxFailedLogins:          5,
			RequireEmailVerification: false,
		},
	}
	handler := NewAuthHandler(service, nil)
	router := gin.New()
	router.POST("/api/auth/login", func(c *gin.Context) {
		handler.Login(NewGinHTTPContext(c))
	})

	requestBody, err := json.Marshal(LoginRequest{
		Email:    email,
		Password: password,
	})
	if err != nil {
		t.Fatalf("encode login request: %v", err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/login",
		bytes.NewReader(requestBody),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "ChronoDesk-OTP-Audit-Test")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf(
			"OTP-required response status = %d, want %d; body=%s",
			response.Code,
			http.StatusBadRequest,
			response.Body.String(),
		)
	}
	var payload struct {
		Code int         `json:"code"`
		Msg  string      `json:"msg"`
		Data interface{} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode OTP-required response: %v", err)
	}
	if payload.Code != 1 ||
		payload.Msg != "请输入 OTP 验证码" ||
		payload.Data != nil {
		t.Fatalf("OTP-required response contract changed: %+v", payload)
	}

	var history models.LoginHistory
	if err := db.Where("user_id = ?", userID).First(&history).Error; err != nil {
		t.Fatalf("read OTP-required login audit: %v", err)
	}
	if history.LoginMethod != models.LoginMethodOTPRequired ||
		history.LoginStatus != models.LoginStatusFailed ||
		history.FailureReason != "otp required" ||
		history.IsActive {
		t.Fatalf("unexpected OTP-required login audit: %+v", history)
	}
	if len(attempts.attempts) != 1 ||
		attempts.attempts[0].Success ||
		attempts.attempts[0].FailReason != "otp required" {
		t.Fatalf("unexpected login attempt audit: %+v", attempts.attempts)
	}
}
