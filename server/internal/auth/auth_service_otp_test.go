package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"gongdan-system/internal/models"
)

type otpTestUserRepo struct {
	user          *User
	updateCalled  bool
	updateErr     error
	lastUpdated   *User
	consumeCalled bool
	consumeResult bool
	consumeErr    error
}

func (r *otpTestUserRepo) Create(ctx context.Context, user *User) error { return nil }
func (r *otpTestUserRepo) GetByID(ctx context.Context, id uint) (*User, error) {
	if r.user == nil || r.user.ID != id {
		return nil, ErrUserNotFound
	}
	return r.user, nil
}
func (r *otpTestUserRepo) GetByEmail(ctx context.Context, email string) (*User, error) {
	return nil, ErrUserNotFound
}
func (r *otpTestUserRepo) GetByUsername(ctx context.Context, username string) (*User, error) {
	return nil, ErrUserNotFound
}
func (r *otpTestUserRepo) Update(ctx context.Context, user *User) error {
	r.updateCalled = true
	copied := *user
	r.lastUpdated = &copied
	return r.updateErr
}
func (r *otpTestUserRepo) Delete(ctx context.Context, id uint) error { return nil }
func (r *otpTestUserRepo) List(ctx context.Context, offset, limit int) ([]*User, int64, error) {
	return nil, 0, nil
}
func (r *otpTestUserRepo) UpdateLastLogin(ctx context.Context, userID uint, loginTime time.Time) error {
	return nil
}
func (r *otpTestUserRepo) IncrementFailedLogin(ctx context.Context, userID uint) error { return nil }
func (r *otpTestUserRepo) ResetFailedLogin(ctx context.Context, userID uint) error     { return nil }
func (r *otpTestUserRepo) LockUser(ctx context.Context, userID uint, until time.Time) error {
	return nil
}
func (r *otpTestUserRepo) UnlockUser(ctx context.Context, userID uint) error { return nil }
func (r *otpTestUserRepo) ConfigureOTP(
	context.Context,
	uint,
	string,
	string,
	bool,
) error {
	return nil
}
func (r *otpTestUserRepo) ReplaceBackupCodes(context.Context, uint, string) error {
	return nil
}
func (r *otpTestUserRepo) ConsumeBackupCode(context.Context, uint, string) (bool, error) {
	r.consumeCalled = true
	return r.consumeResult, r.consumeErr
}

type noopOTPService struct{}

func (s *noopOTPService) GenerateSecret() (string, error)                     { return "", nil }
func (s *noopOTPService) GenerateQRCode(secret, email string) (string, error) { return "", nil }
func (s *noopOTPService) GenerateCode(secret string) (string, error)          { return "", nil }
func (s *noopOTPService) VerifyCode(secret, code string) bool                 { return false }
func (s *noopOTPService) GenerateBackupCodes() ([]string, error)              { return nil, nil }

func TestVerifyOTP_BackupCodePersistsRemoval(t *testing.T) {
	repo := &otpTestUserRepo{
		consumeResult: true,
		user: &User{
			ID:          42,
			OTPEnabled:  true,
			OTPSecret:   "secret",
			BackupCodes: "ABCDEF,ZXCVBN",
		},
	}
	svc := &AuthService{
		userRepo:   repo,
		otpService: &noopOTPService{},
	}

	err := svc.VerifyOTP(context.Background(), 42, "ABCDEF")
	if err != nil {
		t.Fatalf("expected backup code to validate, got error: %v", err)
	}

	if !repo.consumeCalled {
		t.Fatalf("expected repository CAS consumption")
	}
}

func TestVerifyOTP_BackupCodePersistFailureReturnsError(t *testing.T) {
	repo := &otpTestUserRepo{
		consumeResult: true,
		consumeErr:    errors.New("db write failed"),
		user: &User{
			ID:          42,
			OTPEnabled:  true,
			OTPSecret:   "secret",
			BackupCodes: "ABCDEF,ZXCVBN",
		},
	}
	svc := &AuthService{
		userRepo:   repo,
		otpService: &noopOTPService{},
	}

	err := svc.VerifyOTP(context.Background(), 42, "ABCDEF")
	if err == nil {
		t.Fatalf("expected persist failure to bubble up")
	}
}

var _ LoginHistoryRepository = (*noopLoginHistoryRepo)(nil)

type noopLoginHistoryRepo struct{}

func (n *noopLoginHistoryRepo) Create(ctx context.Context, history *models.LoginHistory) error {
	return nil
}
func (n *noopLoginHistoryRepo) RefreshSession(ctx context.Context, userID uint, sessionID, ipAddress, userAgent string, at time.Time) error {
	return nil
}
func (n *noopLoginHistoryRepo) EndSession(ctx context.Context, userID uint, sessionID string, status models.LoginStatus, reason string, at time.Time) error {
	return nil
}
func (n *noopLoginHistoryRepo) EndAllSessions(ctx context.Context, userID uint, status models.LoginStatus, reason string, at time.Time) error {
	return nil
}
