package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

type trustedLoginUserRepository struct {
	UserRepository
	user *User
}

func (repository *trustedLoginUserRepository) GetByEmail(
	_ context.Context,
	email string,
) (*User, error) {
	if repository.user == nil || repository.user.Email != email {
		return nil, ErrUserNotFound
	}
	copied := *repository.user
	return &copied, nil
}

func (*trustedLoginUserRepository) ResetFailedLogin(context.Context, uint) error {
	return nil
}

func (repository *trustedLoginUserRepository) UpdateLastLogin(
	_ context.Context,
	_ uint,
	loginTime time.Time,
) error {
	repository.user.LastLoginAt = &loginTime
	return nil
}

func (*trustedLoginUserRepository) ConsumeBackupCode(
	context.Context,
	uint,
	string,
) (bool, error) {
	return false, nil
}

type trustedLoginAttemptRepository struct {
	LoginAttemptRepository
}

func (*trustedLoginAttemptRepository) GetRecentFailedAttempts(
	context.Context,
	string,
	time.Time,
) (int, error) {
	return 0, nil
}

func (*trustedLoginAttemptRepository) Create(
	context.Context,
	*LoginAttempt,
) error {
	return nil
}

type trustedLoginProfileRepository struct {
	ProfileRepository
}

func (*trustedLoginProfileRepository) GetByUserID(
	context.Context,
	uint,
) (*UserProfile, error) {
	return nil, ErrUserNotFound
}

type trustedLoginTokenRepository struct {
	TokenRepository
	refreshTokens     []*RefreshToken
	deviceRepository  *trustedLoginDeviceRepository
	historyRepository *trustedLoginHistoryRepository
}

func (repository *trustedLoginTokenRepository) CreateRefreshToken(
	_ context.Context,
	token *RefreshToken,
) error {
	copied := *token
	repository.refreshTokens = append(repository.refreshTokens, &copied)
	return nil
}

func (*trustedLoginTokenRepository) RevokeSession(
	context.Context,
	uint,
	string,
) error {
	return nil
}

func (repository *trustedLoginTokenRepository) CommitLoginSession(
	ctx context.Context,
	command *LoginSessionCommit,
) error {
	if command.TrustedDeviceTokenHash != "" {
		device, err := repository.deviceRepository.GetByTokenHash(
			ctx,
			command.TrustedDeviceTokenHash,
		)
		if err != nil ||
			device.UserID != command.UserID ||
			device.Revoked ||
			!device.ExpiresAt.After(command.CommittedAt) {
			return ErrTrustedDeviceInvalid
		}
		device.LastUsedAt = command.CommittedAt
		device.LastIP = command.TrustedDeviceIP
		device.UserAgent = command.TrustedDeviceUserAgent
		if command.TrustedDeviceName != "" {
			device.DeviceName = command.TrustedDeviceName
		}
		if command.TrustedDeviceExpiresAt != nil {
			device.ExpiresAt = *command.TrustedDeviceExpiresAt
		}
		if err := repository.deviceRepository.Update(ctx, device); err != nil {
			return err
		}
	} else if command.NewTrustedDevice != nil {
		if err := repository.deviceRepository.Create(
			ctx,
			command.NewTrustedDevice,
		); err != nil {
			return err
		}
	}
	if err := repository.CreateRefreshToken(ctx, command.RefreshToken); err != nil {
		return err
	}
	return repository.historyRepository.Create(ctx, command.LoginHistory)
}

type trustedLoginHistoryRepository struct {
	LoginHistoryRepository
	history []*models.LoginHistory
}

func (repository *trustedLoginHistoryRepository) Create(
	_ context.Context,
	history *models.LoginHistory,
) error {
	copied := *history
	repository.history = append(repository.history, &copied)
	return nil
}

type trustedLoginDeviceRepository struct {
	TrustedDeviceRepository
	device         *models.OTPTrustedDevice
	createdDevices []*models.OTPTrustedDevice
}

func (repository *trustedLoginDeviceRepository) GetByTokenHash(
	_ context.Context,
	tokenHash string,
) (*models.OTPTrustedDevice, error) {
	if repository.device == nil ||
		repository.device.DeviceTokenHash != tokenHash {
		return nil, errors.New("trusted device not found")
	}
	copied := *repository.device
	return &copied, nil
}

func (repository *trustedLoginDeviceRepository) Create(
	_ context.Context,
	device *models.OTPTrustedDevice,
) error {
	copied := *device
	repository.createdDevices = append(repository.createdDevices, &copied)
	return nil
}

func (repository *trustedLoginDeviceRepository) Update(
	_ context.Context,
	device *models.OTPTrustedDevice,
) error {
	copied := *device
	repository.device = &copied
	return nil
}

func (repository *trustedLoginDeviceRepository) ListActiveDevices(
	context.Context,
	uint,
) ([]*models.OTPTrustedDevice, error) {
	if repository.device == nil || repository.device.Revoked {
		return nil, nil
	}
	copied := *repository.device
	return []*models.OTPTrustedDevice{&copied}, nil
}

type trustedLoginPasswordService struct{}

func (trustedLoginPasswordService) HashPassword(password string) (string, error) {
	return password, nil
}

func (trustedLoginPasswordService) VerifyPassword(
	hashedPassword,
	password string,
) error {
	if hashedPassword != password {
		return ErrInvalidCredentials
	}
	return nil
}

func (trustedLoginPasswordService) ValidatePassword(string) error {
	return nil
}

func (trustedLoginPasswordService) GenerateRandomPassword(int) (string, error) {
	return "", nil
}

type trustedLoginOTPService struct{}

func (trustedLoginOTPService) GenerateSecret() (string, error) {
	return "", nil
}

func (trustedLoginOTPService) GenerateQRCode(string, string) (string, error) {
	return "", nil
}

func (trustedLoginOTPService) GenerateCode(string) (string, error) {
	return "", nil
}

func (trustedLoginOTPService) VerifyCode(_ string, code string) bool {
	return code == "123456"
}

func (trustedLoginOTPService) GenerateBackupCodes() ([]string, error) {
	return nil, nil
}

func TestTrustedDeviceCanBeReusedUntilItIsRevoked(t *testing.T) {
	const (
		userID      = uint(42)
		email       = "trusted-device@example.test"
		password    = "CorrectPassword123!"
		deviceToken = "trusted-device-one-time-credential"
	)
	now := time.Now()
	userRepository := &trustedLoginUserRepository{
		user: &User{
			ID:            userID,
			Username:      "trusted-device",
			Email:         email,
			PasswordHash:  password,
			PlatformRole:  PlatformRoleMember,
			Status:        StatusActive,
			EmailVerified: true,
			OTPEnabled:    true,
			OTPSecret:     "otp-secret",
		},
	}
	deviceRepository := &trustedLoginDeviceRepository{
		device: &models.OTPTrustedDevice{
			ID:              7,
			UserID:          userID,
			DeviceTokenHash: hashTrustedDeviceToken(deviceToken),
			DeviceName:      "Test Mac",
			LastUsedAt:      now.Add(-time.Hour),
			ExpiresAt:       now.Add(time.Hour),
		},
	}
	historyRepository := &trustedLoginHistoryRepository{}
	tokenRepository := &trustedLoginTokenRepository{
		deviceRepository:  deviceRepository,
		historyRepository: historyRepository,
	}
	service := NewAuthService(
		userRepository,
		&trustedLoginProfileRepository{},
		tokenRepository,
		&trustedLoginAttemptRepository{},
		historyRepository,
		deviceRepository,
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

	response, err := service.Login(
		context.Background(),
		&LoginRequest{
			Email:       email,
			Password:    password,
			DeviceToken: deviceToken,
		},
		"127.0.0.1",
		"ChronoDesk Trusted Device Test",
	)
	if err != nil {
		t.Fatalf("可信设备复用登录失败: %v", err)
	}
	if response.AccessToken == "" ||
		len(historyRepository.history) != 1 ||
		historyRepository.history[0].LoginMethod !=
			models.LoginMethodPasswordTrusted {
		t.Fatalf("可信设备登录记录不完整: %+v", historyRepository.history)
	}

	deviceRepository.device.Revoked = true
	deviceRepository.device.ExpiresAt = time.Now()
	if _, err := service.Login(
		context.Background(),
		&LoginRequest{
			Email:       email,
			Password:    password,
			DeviceToken: deviceToken,
		},
		"127.0.0.1",
		"ChronoDesk Trusted Device Test",
	); err == nil || err.Error() != "OTP code required" {
		t.Fatalf("已撤销可信设备复用错误 = %v，期望重新要求 OTP", err)
	}

	response, err = service.Login(
		context.Background(),
		&LoginRequest{
			Email:          email,
			Password:       password,
			OTPCode:        "123456",
			DeviceToken:    deviceToken,
			RememberDevice: true,
			DeviceName:     "Test Mac re-authorized",
		},
		"127.0.0.1",
		"ChronoDesk Trusted Device Test",
	)
	if err != nil {
		t.Fatalf("撤销后重新验证登录失败: %v", err)
	}
	if response.TrustedDeviceToken == "" ||
		response.TrustedDeviceToken == deviceToken ||
		len(deviceRepository.createdDevices) != 1 ||
		deviceRepository.createdDevices[0].DeviceTokenHash ==
			hashTrustedDeviceToken(deviceToken) {
		t.Fatal("撤销后的设备没有生成独立的新凭据")
	}
}
