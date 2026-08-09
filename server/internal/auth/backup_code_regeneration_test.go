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
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

const (
	backupCodeTestPassword = "CurrentPassword123!"
	backupCodeTestHash     = "current-password-hash"
)

type backupCodeTestPasswordService struct{}

func (backupCodeTestPasswordService) HashPassword(password string) (string, error) {
	return "hash:" + password, nil
}

func (backupCodeTestPasswordService) VerifyPassword(hash, password string) error {
	if hash != backupCodeTestHash || password != backupCodeTestPassword {
		return errors.New("password verification failed")
	}
	return nil
}

func (backupCodeTestPasswordService) ValidatePassword(string) error {
	return nil
}

func (backupCodeTestPasswordService) GenerateRandomPassword(int) (string, error) {
	return "", nil
}

type backupCodeTestOTPService struct {
	mu    sync.Mutex
	calls int
	codes []string
	err   error
}

func (*backupCodeTestOTPService) GenerateSecret() (string, error) {
	return "", nil
}

func (*backupCodeTestOTPService) GenerateQRCode(string, string) (string, error) {
	return "", nil
}

func (*backupCodeTestOTPService) GenerateCode(string) (string, error) {
	return "", nil
}

func (*backupCodeTestOTPService) VerifyCode(string, string) bool {
	return false
}

func (service *backupCodeTestOTPService) GenerateBackupCodes() ([]string, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.calls++
	if service.err != nil {
		return nil, service.err
	}
	if service.codes != nil {
		return append([]string(nil), service.codes...), nil
	}
	codes := make([]string, 10)
	for index := range codes {
		codes[index] = fmt.Sprintf("SET%02d-CODE%02d", service.calls, index+1)
	}
	return codes, nil
}

func (service *backupCodeTestOTPService) callCount() int {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.calls
}

type backupCodeMemoryRepository struct {
	UserRepository
	mu          sync.Mutex
	user        User
	audits      []AuthenticationSecurityAuditEvent
	rotationErr error
	getArrived  *sync.WaitGroup
	getRelease  <-chan struct{}
}

func (repository *backupCodeMemoryRepository) GetByID(
	_ context.Context,
	userID uint,
) (*User, error) {
	repository.mu.Lock()
	if repository.user.ID != userID {
		repository.mu.Unlock()
		return nil, ErrUserNotFound
	}
	copied := repository.user
	getArrived := repository.getArrived
	getRelease := repository.getRelease
	repository.mu.Unlock()
	if getArrived != nil {
		getArrived.Done()
	}
	if getRelease != nil {
		<-getRelease
	}
	return &copied, nil
}

func (repository *backupCodeMemoryRepository) RotateBackupCodesWithAudit(
	_ context.Context,
	expected BackupCodeRotationSnapshot,
	replacementHashes string,
	audit AuthenticationSecurityAuditEvent,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.rotationErr != nil {
		return repository.rotationErr
	}
	if repository.user.ID != expected.UserID ||
		repository.user.PasswordHash != expected.PasswordHash ||
		repository.user.OTPEnabled != expected.OTPEnabled ||
		repository.user.BackupCodes != expected.BackupCodes {
		return ErrBackupCodesChanged
	}
	repository.user.BackupCodes = replacementHashes
	repository.audits = append(repository.audits, audit)
	return nil
}

func (repository *backupCodeMemoryRepository) snapshot() (User, []AuthenticationSecurityAuditEvent) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	user := repository.user
	audits := append([]AuthenticationSecurityAuditEvent(nil), repository.audits...)
	return user, audits
}

type backupCodeTestLogger struct {
	mu     sync.Mutex
	fields []interface{}
}

func (*backupCodeTestLogger) Info(string, ...interface{})  {}
func (*backupCodeTestLogger) Warn(string, ...interface{})  {}
func (*backupCodeTestLogger) Debug(string, ...interface{}) {}

func (logger *backupCodeTestLogger) Error(_ string, fields ...interface{}) {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	logger.fields = append(logger.fields, fields...)
}

func (logger *backupCodeTestLogger) serialized() string {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	return fmt.Sprint(logger.fields...)
}

func newBackupCodeHandlerTest(
	t *testing.T,
	user User,
) (*gin.Engine, *backupCodeMemoryRepository, *backupCodeTestOTPService, *backupCodeTestLogger) {
	t.Helper()
	repository := &backupCodeMemoryRepository{user: user}
	otpService := &backupCodeTestOTPService{}
	service := &AuthService{
		userRepo:        repository,
		otpService:      otpService,
		passwordService: backupCodeTestPasswordService{},
	}
	logger := &backupCodeTestLogger{}
	handler := NewAuthHandler(service, logger)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/auth/otp/backup-codes", func(c *gin.Context) {
		c.Set("user_id", user.ID)
		c.Set("platform_role", PlatformRoleMember)
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = "request-backup-code-test"
		}
		c.Set("request_id", requestID)
		handler.GenerateBackupCodes(NewGinHTTPContext(c))
	})
	return router, repository, otpService, logger
}

func performBackupCodeRequest(
	router http.Handler,
	body string,
) *httptest.ResponseRecorder {
	return performBackupCodeRequestWithID(router, body, "")
}

func performBackupCodeRequestWithID(
	router http.Handler,
	body string,
	requestID string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/otp/backup-codes",
		strings.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	if requestID != "" {
		request.Header.Set("X-Request-ID", requestID)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestGenerateBackupCodesRequiresStrictCurrentPasswordJSON(t *testing.T) {
	oldHashes, err := hashBackupCodes([]string{"OLD-CODE-01"})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "bearer only empty body", body: ""},
		{name: "missing password", body: `{}`},
		{name: "empty password", body: `{"current_password":""}`},
		{
			name: "unknown field",
			body: `{"current_password":"CurrentPassword123!","unexpected":true}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			router, repository, otpService, _ := newBackupCodeHandlerTest(
				t,
				User{
					ID:           42,
					PasswordHash: backupCodeTestHash,
					OTPEnabled:   true,
					BackupCodes:  oldHashes,
				},
			)
			response := performBackupCodeRequest(router, test.body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", response.Code, response.Body.String())
			}
			var payload ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Error != "invalid_request" {
				t.Fatalf("error = %q, want invalid_request", payload.Error)
			}
			stored, audits := repository.snapshot()
			if stored.BackupCodes != oldHashes || len(audits) != 0 {
				t.Fatal("invalid request changed backup codes or created success audit")
			}
			if otpService.callCount() != 0 {
				t.Fatal("invalid request generated plaintext backup codes")
			}
		})
	}
}

func TestGenerateBackupCodesMapsStepUpAndMFAFailures(t *testing.T) {
	oldHashes, err := hashBackupCodes([]string{"OLD-CODE-01"})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		user       User
		password   string
		wantStatus int
		wantError  string
	}{
		{
			name: "wrong password",
			user: User{
				ID:           42,
				PasswordHash: backupCodeTestHash,
				OTPEnabled:   true,
				BackupCodes:  oldHashes,
			},
			password:   "WrongPassword123!",
			wantStatus: http.StatusUnauthorized,
			wantError:  "invalid_password",
		},
		{
			name: "OTP disabled",
			user: User{
				ID:           42,
				PasswordHash: backupCodeTestHash,
				OTPEnabled:   false,
				BackupCodes:  "",
			},
			password:   backupCodeTestPassword,
			wantStatus: http.StatusConflict,
			wantError:  "otp_not_enabled",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			router, repository, otpService, logger := newBackupCodeHandlerTest(t, test.user)
			response := performBackupCodeRequest(
				router,
				fmt.Sprintf(`{"current_password":%q}`, test.password),
			)
			if response.Code != test.wantStatus {
				t.Fatalf(
					"status = %d, want %d; body=%s",
					response.Code,
					test.wantStatus,
					response.Body.String(),
				)
			}
			var payload ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Error != test.wantError {
				t.Fatalf("error = %q, want %q", payload.Error, test.wantError)
			}
			stored, audits := repository.snapshot()
			if stored.BackupCodes != test.user.BackupCodes || len(audits) != 0 {
				t.Fatal("rejected step-up changed backup codes or created success audit")
			}
			if otpService.callCount() != 0 {
				t.Fatal("rejected step-up generated plaintext backup codes")
			}
			logged := logger.serialized()
			if strings.Contains(logged, test.password) || strings.Contains(logged, oldHashes) {
				t.Fatalf("security log contains secret material: %q", logged)
			}
		})
	}
}

func TestGenerateBackupCodesFailsClosedWithoutAtomicRepository(t *testing.T) {
	otpService := &backupCodeTestOTPService{}
	service := &AuthService{
		userRepo: struct{ UserRepository }{
			UserRepository: &backupCodeMemoryRepository{
				user: User{
					ID:           42,
					PasswordHash: backupCodeTestHash,
					OTPEnabled:   true,
				},
			},
		},
		otpService:      otpService,
		passwordService: backupCodeTestPasswordService{},
	}
	_, err := service.GenerateBackupCodes(
		context.Background(),
		42,
		backupCodeTestPassword,
		AuthenticationSecurityAuditContext{},
	)
	if !errors.Is(err, ErrAtomicBackupCodeRotationUnavailable) {
		t.Fatalf("error = %v, want atomic repository unavailable", err)
	}
	if otpService.callCount() != 0 {
		t.Fatal("service generated codes before proving atomic repository capability")
	}
}

func TestGenerateBackupCodesRejectsNonTenCodeGeneratorResult(t *testing.T) {
	repository := &backupCodeMemoryRepository{
		user: User{
			ID:           42,
			PasswordHash: backupCodeTestHash,
			OTPEnabled:   true,
		},
	}
	otpService := &backupCodeTestOTPService{
		codes: []string{"ONLY-ONE-CODE"},
	}
	service := &AuthService{
		userRepo:        repository,
		otpService:      otpService,
		passwordService: backupCodeTestPasswordService{},
	}
	codes, err := service.GenerateBackupCodes(
		context.Background(),
		42,
		backupCodeTestPassword,
		AuthenticationSecurityAuditContext{RequestID: "invalid-generator-count"},
	)
	if !errors.Is(err, ErrInvalidBackupCodeStorage) || len(codes) != 0 {
		t.Fatalf("result = %#v, %v; want fail-closed invalid storage", codes, err)
	}
	stored, audits := repository.snapshot()
	if stored.BackupCodes != "" || len(audits) != 0 {
		t.Fatal("invalid generator result reached atomic storage")
	}
}

func TestGenerateBackupCodesSuccessReturnsTenCodesAndSafeAudit(t *testing.T) {
	oldHashes, err := hashBackupCodes([]string{"OLD-CODE-01"})
	if err != nil {
		t.Fatal(err)
	}
	router, repository, otpService, logger := newBackupCodeHandlerTest(
		t,
		User{
			ID:           42,
			PasswordHash: backupCodeTestHash,
			OTPEnabled:   true,
			BackupCodes:  oldHashes,
		},
	)
	response := performBackupCodeRequest(
		router,
		`{"current_password":"CurrentPassword123!"}`,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Data struct {
			BackupCodes []string `json:"backup_codes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data.BackupCodes) != 10 || otpService.callCount() != 1 {
		t.Fatalf("returned %d codes after %d generations", len(payload.Data.BackupCodes), otpService.callCount())
	}

	stored, audits := repository.snapshot()
	if stored.BackupCodes == oldHashes {
		t.Fatal("winning rotation did not replace hashes")
	}
	if len(audits) != 1 ||
		audits[0].EventType != AuthenticationSecurityEventBackupCodesRegenerated ||
		audits[0].Source != AuthenticationSecurityAuditSourceHumanREST ||
		audits[0].UserID != 42 ||
		audits[0].RequestID != "request-backup-code-test" ||
		audits[0].CreatedAt.IsZero() {
		t.Fatalf("unexpected success audit: %+v", audits)
	}
	auditJSON, err := json.Marshal(audits[0])
	if err != nil {
		t.Fatal(err)
	}
	secrets := append([]string{
		backupCodeTestPassword,
		oldHashes,
		stored.BackupCodes,
	}, payload.Data.BackupCodes...)
	for _, secret := range secrets {
		if strings.Contains(string(auditJSON), secret) ||
			strings.Contains(logger.serialized(), secret) {
			t.Fatalf("audit or log contains secret material")
		}
	}
}

func TestBackupCodeRegenerationAuditMetadataRedactsSecretMaterial(t *testing.T) {
	oldHashes, err := hashBackupCodes([]string{"OLD-CODE-01"})
	if err != nil {
		t.Fatal(err)
	}
	for _, requestID := range []string{
		backupCodeTestPassword,
		"SET01-CODE01",
		oldHashes,
	} {
		t.Run(requestID[:min(12, len(requestID))], func(t *testing.T) {
			router, repository, _, logger := newBackupCodeHandlerTest(
				t,
				User{
					ID:           42,
					PasswordHash: backupCodeTestHash,
					OTPEnabled:   true,
					BackupCodes:  oldHashes,
					OTPSecret:    "JBSWY3DPEHPK3PXP",
				},
			)
			response := performBackupCodeRequestWithID(
				router,
				`{"current_password":"CurrentPassword123!"}`,
				requestID,
			)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
			}
			_, audits := repository.snapshot()
			if len(audits) != 1 {
				t.Fatalf("audit count = %d", len(audits))
			}
			auditJSON, err := json.Marshal(audits[0])
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(auditJSON, []byte(requestID)) ||
				strings.Contains(logger.serialized(), requestID) {
				t.Fatalf("request metadata retained secret material: %s", auditJSON)
			}
		})
	}
}

func TestConcurrentBackupCodeHTTPRequestsReturnOne200AndOne409(t *testing.T) {
	oldHashes, err := hashBackupCodes([]string{"OLD-CODE-01"})
	if err != nil {
		t.Fatal(err)
	}
	router, repository, _, _ := newBackupCodeHandlerTest(
		t,
		User{
			ID:           42,
			PasswordHash: backupCodeTestHash,
			OTPEnabled:   true,
			BackupCodes:  oldHashes,
		},
	)
	var arrived sync.WaitGroup
	arrived.Add(2)
	release := make(chan struct{})
	repository.mu.Lock()
	repository.getArrived = &arrived
	repository.getRelease = release
	repository.mu.Unlock()

	responses := make(chan *httptest.ResponseRecorder, 2)
	for index := 0; index < 2; index++ {
		go func() {
			responses <- performBackupCodeRequest(
				router,
				`{"current_password":"CurrentPassword123!"}`,
			)
		}()
	}
	arrived.Wait()
	close(release)

	statusCounts := map[int]int{}
	var winningCodes []string
	for index := 0; index < 2; index++ {
		response := <-responses
		statusCounts[response.Code]++
		if response.Code == http.StatusOK {
			var payload struct {
				Data struct {
					BackupCodes []string `json:"backup_codes"`
				} `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			winningCodes = payload.Data.BackupCodes
		} else {
			var payload map[string]interface{}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload["error"] != "backup_codes_changed" ||
				payload["data"] != nil {
				t.Fatalf("losing response contract = %v", payload)
			}
		}
	}
	if statusCounts[http.StatusOK] != 1 ||
		statusCounts[http.StatusConflict] != 1 ||
		len(winningCodes) != 10 {
		t.Fatalf("statuses=%v winning codes=%d", statusCounts, len(winningCodes))
	}
	stored, audits := repository.snapshot()
	hashes, err := parseBackupCodeHashes(stored.BackupCodes)
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || matchBackupCode(hashes, winningCodes[0]) < 0 {
		t.Fatal("only the 200 response set must be valid with one success audit")
	}
	losingSetFirstCode := "SET01-CODE01"
	if winningCodes[0] == losingSetFirstCode {
		losingSetFirstCode = "SET02-CODE01"
	}
	if matchBackupCode(hashes, losingSetFirstCode) >= 0 {
		t.Fatal("losing response set became valid")
	}
}

type barrierBackupCodeRepository struct {
	*GormUserRepository
	arrived sync.WaitGroup
	release chan struct{}
}

func newBarrierBackupCodeRepository(
	repository *GormUserRepository,
	callers int,
) *barrierBackupCodeRepository {
	result := &barrierBackupCodeRepository{
		GormUserRepository: repository,
		release:            make(chan struct{}),
	}
	result.arrived.Add(callers)
	return result
}

func (repository *barrierBackupCodeRepository) GetByID(
	ctx context.Context,
	userID uint,
) (*User, error) {
	user, err := repository.GormUserRepository.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	repository.arrived.Done()
	<-repository.release
	return user, nil
}

func setupGormBackupCodeRotationTest(
	t *testing.T,
) (*gorm.DB, *GormUserRepository, *User, string) {
	t.Helper()
	db, repository, _, user := newOTPSecretStorageTest(t)
	if err := db.AutoMigrate(&AuthenticationSecurityAuditEvent{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.User{}).
		Where("id = ?", user.ID).
		Update("password_hash", backupCodeTestHash).Error; err != nil {
		t.Fatal(err)
	}
	oldHashes, err := hashBackupCodes([]string{"OLD-CODE-01", "OLD-CODE-02"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ConfigureOTP(
		context.Background(),
		user.ID,
		"JBSWY3DPEHPK3PXP",
		oldHashes,
		true,
	); err != nil {
		t.Fatal(err)
	}
	return db, repository, user, oldHashes
}

func TestGormBackupCodeRegenerationCASAllowsExactlyOneWinner(t *testing.T) {
	db, baseRepository, user, _ := setupGormBackupCodeRotationTest(t)
	repository := newBarrierBackupCodeRepository(baseRepository, 2)
	otpService := &backupCodeTestOTPService{}
	service := &AuthService{
		userRepo:        repository,
		otpService:      otpService,
		passwordService: backupCodeTestPasswordService{},
	}

	type result struct {
		codes []string
		err   error
	}
	results := make(chan result, 2)
	for index := 0; index < 2; index++ {
		go func() {
			codes, err := service.GenerateBackupCodes(
				context.Background(),
				user.ID,
				backupCodeTestPassword,
				AuthenticationSecurityAuditContext{
					RequestID:     "concurrent-request",
					TraceID:       "4bf92f3577b34da6a3ce929d0e0e4736",
					CorrelationID: "correlation-concurrent",
				},
			)
			results <- result{codes: codes, err: err}
		}()
	}
	repository.arrived.Wait()
	close(repository.release)

	var winner, loser result
	for index := 0; index < 2; index++ {
		current := <-results
		if current.err == nil {
			winner = current
		} else {
			loser = current
		}
	}
	if len(winner.codes) != 10 {
		t.Fatalf("winner returned %d codes", len(winner.codes))
	}
	if !errors.Is(loser.err, ErrBackupCodesChanged) || len(loser.codes) != 0 {
		t.Fatalf("loser = %+v, want backup_codes_changed with no codes", loser)
	}
	var auditCount int64
	if err := db.Model(&AuthenticationSecurityAuditEvent{}).
		Where(
			"user_id = ? AND event_type = ?",
			user.ID,
			AuthenticationSecurityEventBackupCodesRegenerated,
		).
		Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("success audit count = %d, want 1", auditCount)
	}
	consumed, err := baseRepository.ConsumeBackupCode(
		context.Background(),
		user.ID,
		"OLD-CODE-01",
	)
	if err != nil || consumed {
		t.Fatalf("old code remained valid: consumed=%v err=%v", consumed, err)
	}
	var stored models.User
	if err := db.Select("backup_codes").First(&stored, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	for _, plaintext := range append(
		append([]string{}, winner.codes...),
		"OLD-CODE-01",
		"OLD-CODE-02",
	) {
		if strings.Contains(stored.BackupCodes, plaintext) {
			t.Fatal("database contains a plaintext backup code")
		}
	}
	consumed, err = baseRepository.ConsumeBackupCode(
		context.Background(),
		user.ID,
		winner.codes[0],
	)
	if err != nil || !consumed {
		t.Fatalf("winner code was not valid: consumed=%v err=%v", consumed, err)
	}
	consumed, err = baseRepository.ConsumeBackupCode(
		context.Background(),
		user.ID,
		winner.codes[0],
	)
	if err != nil || consumed {
		t.Fatalf("winner code was not single-use: consumed=%v err=%v", consumed, err)
	}
}

func TestGormBackupCodeRegenerationRejectsStalePasswordAndMFAState(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*gorm.DB, uint) error
	}{
		{
			name: "password changed",
			mutate: func(db *gorm.DB, userID uint) error {
				return db.Model(&models.User{}).
					Where("id = ?", userID).
					Update("password_hash", "new-password-hash").Error
			},
		},
		{
			name: "OTP disabled",
			mutate: func(db *gorm.DB, userID uint) error {
				return db.Model(&models.User{}).
					Where("id = ?", userID).
					Updates(map[string]interface{}{
						"two_factor_enabled": false,
						"backup_codes":       "",
					}).Error
			},
		},
		{
			name: "backup codes consumed",
			mutate: func(db *gorm.DB, userID uint) error {
				return db.Model(&models.User{}).
					Where("id = ?", userID).
					Update("backup_codes", "").Error
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, repository, user, oldHashes := setupGormBackupCodeRotationTest(t)
			expected := BackupCodeRotationSnapshot{
				UserID:       user.ID,
				OTPEnabled:   true,
				PasswordHash: backupCodeTestHash,
				BackupCodes:  oldHashes,
			}
			if err := test.mutate(db, user.ID); err != nil {
				t.Fatal(err)
			}
			replacement, err := hashBackupCodes([]string{"NEW-CODE-01"})
			if err != nil {
				t.Fatal(err)
			}
			err = repository.RotateBackupCodesWithAudit(
				context.Background(),
				expected,
				replacement,
				AuthenticationSecurityAuditEvent{
					UserID:    user.ID,
					EventType: AuthenticationSecurityEventBackupCodesRegenerated,
					Source:    AuthenticationSecurityAuditSourceHumanREST,
					CreatedAt: time.Now(),
				},
			)
			if !errors.Is(err, ErrBackupCodesChanged) {
				t.Fatalf("error = %v, want backup_codes_changed", err)
			}
			var auditCount int64
			if err := db.Model(&AuthenticationSecurityAuditEvent{}).
				Count(&auditCount).Error; err != nil {
				t.Fatal(err)
			}
			if auditCount != 0 {
				t.Fatalf("stale rotation wrote %d audit events", auditCount)
			}
		})
	}
}

func TestGormBackupCodeRegenerationRollsBackWhenAuditInsertFails(t *testing.T) {
	db, repository, user, oldHashes := setupGormBackupCodeRotationTest(t)
	if err := db.Exec(`
		CREATE TRIGGER reject_backup_code_audit
		BEFORE INSERT ON authentication_security_audit_events
		BEGIN
			SELECT RAISE(FAIL, 'injected audit failure');
		END
	`).Error; err != nil {
		t.Fatal(err)
	}
	replacement, err := hashBackupCodes([]string{"NEW-CODE-01"})
	if err != nil {
		t.Fatal(err)
	}
	err = repository.RotateBackupCodesWithAudit(
		context.Background(),
		BackupCodeRotationSnapshot{
			UserID:       user.ID,
			OTPEnabled:   true,
			PasswordHash: backupCodeTestHash,
			BackupCodes:  oldHashes,
		},
		replacement,
		AuthenticationSecurityAuditEvent{
			UserID:    user.ID,
			EventType: AuthenticationSecurityEventBackupCodesRegenerated,
			Source:    AuthenticationSecurityAuditSourceHumanREST,
			RequestID: "audit-failure-request",
			CreatedAt: time.Now(),
		},
	)
	if err == nil {
		t.Fatal("audit insert failure unexpectedly committed")
	}
	var stored models.User
	if err := db.Select("backup_codes").First(&stored, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.BackupCodes != oldHashes {
		t.Fatal("backup-code hashes escaped audit transaction rollback")
	}
	var auditCount int64
	if err := db.Model(&AuthenticationSecurityAuditEvent{}).
		Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != 0 {
		t.Fatalf("failed transaction persisted %d audit events", auditCount)
	}
}

func TestAuthenticationSecurityAuditRejectsOpenVocabulary(t *testing.T) {
	db, _, user, _ := setupGormBackupCodeRotationTest(t)
	for _, audit := range []AuthenticationSecurityAuditEvent{
		{
			UserID:    user.ID,
			EventType: "password=" + backupCodeTestPassword,
			Source:    AuthenticationSecurityAuditSourceHumanREST,
			RequestID: "closed-vocabulary-test",
			CreatedAt: time.Now(),
		},
		{
			UserID:    user.ID,
			EventType: AuthenticationSecurityEventBackupCodesRegenerated,
			Source:    "backup-code-hash",
			RequestID: "closed-vocabulary-test",
			CreatedAt: time.Now(),
		},
	} {
		if err := db.Create(&audit).Error; err == nil {
			t.Fatalf("open-vocabulary audit unexpectedly persisted: %+v", audit)
		}
	}
}

func TestBackupCodeRegenerationResponseDoesNotEchoSecretsOnCASLoss(t *testing.T) {
	oldHashes, err := hashBackupCodes([]string{"OLD-CODE-01"})
	if err != nil {
		t.Fatal(err)
	}
	router, repository, _, logger := newBackupCodeHandlerTest(
		t,
		User{
			ID:           42,
			PasswordHash: backupCodeTestHash,
			OTPEnabled:   true,
			BackupCodes:  oldHashes,
		},
	)
	repository.rotationErr = fmt.Errorf(
		"%w: %s %s",
		ErrBackupCodesChanged,
		backupCodeTestPassword,
		oldHashes,
	)
	response := performBackupCodeRequestWithID(
		router,
		`{"current_password":"CurrentPassword123!"}`,
		backupCodeTestPassword,
	)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", response.Code, response.Body.String())
	}
	if bytes.Contains(response.Body.Bytes(), []byte(backupCodeTestPassword)) ||
		bytes.Contains(response.Body.Bytes(), []byte(oldHashes)) ||
		strings.Contains(logger.serialized(), backupCodeTestPassword) ||
		strings.Contains(logger.serialized(), oldHashes) {
		t.Fatal("CAS-loss response or log exposed secret material")
	}
}

func TestBackupCodeRegenerationStorageFailureReturnsNoCodes(t *testing.T) {
	oldHashes, err := hashBackupCodes([]string{"OLD-CODE-01"})
	if err != nil {
		t.Fatal(err)
	}
	router, repository, otpService, logger := newBackupCodeHandlerTest(
		t,
		User{
			ID:           42,
			PasswordHash: backupCodeTestHash,
			OTPEnabled:   true,
			BackupCodes:  oldHashes,
		},
	)
	repository.rotationErr = errors.New("storage unavailable SET01-CODE01")
	response := performBackupCodeRequest(
		router,
		`{"current_password":"CurrentPassword123!"}`,
	)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", response.Code, response.Body.String())
	}
	if bytes.Contains(response.Body.Bytes(), []byte("SET01-CODE01")) ||
		strings.Contains(logger.serialized(), "SET01-CODE01") {
		t.Fatal("storage failure exposed generated plaintext")
	}
	stored, audits := repository.snapshot()
	if stored.BackupCodes != oldHashes || len(audits) != 0 ||
		otpService.callCount() != 1 {
		t.Fatal("storage failure changed hashes or wrote success audit")
	}
}
