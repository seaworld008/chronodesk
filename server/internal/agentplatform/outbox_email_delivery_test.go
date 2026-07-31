package agentplatform

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/auth"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/security"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type recordingAuthOutboxEmailSender struct {
	mu             sync.Mutex
	failures       int
	passwordTokens []string
}

type recordingNotificationOutboxEmailSender struct {
	mu       sync.Mutex
	db       *gorm.DB
	failures int
	calls    int
}

func (s *recordingNotificationOutboxEmailSender) SendEmailNotificationOutboxAttempt(
	ctx context.Context,
	scope models.ProjectScope,
	notification *models.Notification,
) error {
	var persisted models.Notification
	if err := s.db.WithContext(ctx).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			notification.ID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		First(&persisted).Error; err != nil {
		return err
	}
	if persisted.IsSent {
		return nil
	}
	s.mu.Lock()
	s.calls++
	if s.failures > 0 {
		s.failures--
		s.mu.Unlock()
		return errors.New("injected notification SMTP failure")
	}
	s.mu.Unlock()
	persisted.MarkAsSent()
	persisted.MarkAsDelivered()
	persisted.DeliveryStatus = "delivered"
	return s.db.WithContext(ctx).Save(&persisted).Error
}

func (s *recordingNotificationOutboxEmailSender) GetEmailTemplate(
	models.NotificationType,
) (*services.EmailTemplate, error) {
	return nil, nil
}

func (s *recordingNotificationOutboxEmailSender) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *recordingAuthOutboxEmailSender) SendVerificationEmail(
	context.Context,
	string,
	string,
) error {
	return nil
}

func (s *recordingAuthOutboxEmailSender) SendPasswordResetEmail(
	_ context.Context,
	_ string,
	token string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.passwordTokens = append(s.passwordTokens, token)
	if s.failures > 0 {
		s.failures--
		return errors.New("injected SMTP failure")
	}
	return nil
}

func (s *recordingAuthOutboxEmailSender) SendWelcomeEmail(
	context.Context,
	string,
	string,
) error {
	return nil
}

func (s *recordingAuthOutboxEmailSender) resetAttempts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.passwordTokens...)
}

type authEmailOutboxFixture struct {
	db         *gorm.DB
	native     *services.AgentNativeService
	deliverer  *NativeOutboxDeliverer
	sender     *recordingAuthOutboxEmailSender
	plaintext  string
	deliveryID string
	workerCtx  context.Context
}

func newAuthEmailOutboxFixture(
	t *testing.T,
	failures int,
) *authEmailOutboxFixture {
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
		&auth.PasswordReset{},
		&auth.EmailVerification{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatal(err)
	}
	scope := installAgentplatformTestProjectScope(t, db)
	protector, err := security.NewKeyring(
		"test-outbox-email",
		map[string][]byte{
			"test-outbox-email": bytes.Repeat([]byte{0x63}, 32),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := auth.NewGormAuthEmailOutboxRepository(
		db,
		protector,
		scope,
		"urn:test:auth-email",
	)
	if err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:     "outbox-reset-user",
		Email:        "outbox-reset@example.test",
		PasswordHash: "test-password-hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	plaintext := "reset-token-visible-only-to-recipient"
	if err := repository.QueuePasswordReset(
		context.Background(),
		&auth.PasswordReset{
			UserID:    user.ID,
			Email:     user.Email,
			Token:     plaintext,
			ExpiresAt: time.Now().Add(time.Hour),
		},
	); err != nil {
		t.Fatal(err)
	}
	workerCtx := agentplatformTestOutboxWorkerContext(t, scope)
	sender := &recordingAuthOutboxEmailSender{failures: failures}
	consumer, err := auth.NewAuthEmailOutboxConsumer(db, protector, sender)
	if err != nil {
		t.Fatal(err)
	}
	deliverer, err := NewNativeOutboxDeliverer(
		NativeOutboxDelivererOptions{
			DB:         db,
			AuthEmails: consumer,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	native := services.NewAgentNativeService(db, services.AgentNativeOptions{
		EventSource: "urn:test:events",
	})
	var delivery models.OutboxDelivery
	if err := db.First(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	return &authEmailOutboxFixture{
		db:         db,
		native:     native,
		deliverer:  deliverer,
		sender:     sender,
		plaintext:  plaintext,
		deliveryID: delivery.ID,
		workerCtx:  workerCtx,
	}
}

func TestAuthenticationEmailOutboxRetriesAndRecoversIdempotently(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T, *authEmailOutboxFixture)
	}{
		{
			name: "SMTP failure is retried by Outbox",
			run: func(t *testing.T, fixture *authEmailOutboxFixture) {
				first, err := fixture.native.ProcessOutboxBatch(
					context.Background(),
					"email-worker-failure",
					10,
					fixture.deliverer,
				)
				if err != nil {
					t.Fatal(err)
				}
				if first.Failed != 1 || first.Delivered != 0 {
					t.Fatalf("first batch = %+v", first)
				}
				var reset auth.PasswordReset
				if err := fixture.db.First(&reset).Error; err != nil {
					t.Fatal(err)
				}
				if reset.EmailDeliveredAt != nil {
					t.Fatal("failed SMTP attempt persisted a success receipt")
				}
				if reset.DeliverySecret == "" {
					t.Fatal("failed SMTP attempt discarded the retry credential")
				}
				if err := fixture.db.Model(&models.OutboxDelivery{}).
					Where("id = ?", fixture.deliveryID).
					Update("next_attempt_at", time.Now().Add(-time.Minute)).
					Error; err != nil {
					t.Fatal(err)
				}
				second, err := fixture.native.ProcessOutboxBatch(
					context.Background(),
					"email-worker-retry",
					10,
					fixture.deliverer,
				)
				if err != nil {
					t.Fatal(err)
				}
				if second.Delivered != 1 || second.Failed != 0 {
					t.Fatalf("second batch = %+v", second)
				}
				if err := fixture.db.First(&reset).Error; err != nil {
					t.Fatal(err)
				}
				if reset.EmailDeliveredAt == nil || reset.DeliverySecret != "" {
					t.Fatal("successful retry did not persist and minimize its receipt")
				}
				attempts := fixture.sender.resetAttempts()
				if len(attempts) != 2 ||
					attempts[0] != fixture.plaintext ||
					attempts[1] != fixture.plaintext {
					t.Fatalf("SMTP attempts = %#v", attempts)
				}
			},
		},
		{
			name: "process crash after SMTP before acknowledgement",
			run: func(t *testing.T, fixture *authEmailOutboxFixture) {
				claimed, err := fixture.native.ClaimPendingOutbox(
					fixture.workerCtx,
					"email-worker-crashed",
					10,
					2*time.Minute,
				)
				if err != nil {
					t.Fatal(err)
				}
				if len(claimed) != 1 || claimed[0].Event == nil {
					t.Fatalf("claimed deliveries = %+v", claimed)
				}
				if err := fixture.deliverer.Deliver(
					fixture.workerCtx,
					claimed[0],
					services.CloudEventFromModel(claimed[0].Event),
				); err != nil {
					t.Fatal(err)
				}
				// Deliberately omit MarkOutboxDelivered, reproducing termination
				// after the side effect and its durable receipt.
				if err := fixture.db.Model(&models.OutboxDelivery{}).
					Where("id = ?", fixture.deliveryID).
					Update("locked_at", time.Now().Add(-3*time.Minute)).
					Error; err != nil {
					t.Fatal(err)
				}
				resumed, err := fixture.native.ProcessOutboxBatch(
					context.Background(),
					"email-worker-resumed",
					10,
					fixture.deliverer,
				)
				if err != nil {
					t.Fatal(err)
				}
				if resumed.Delivered != 1 {
					t.Fatalf("resumed batch = %+v", resumed)
				}
				if attempts := fixture.sender.resetAttempts(); len(attempts) != 1 {
					t.Fatalf(
						"replayed delivery performed %d SMTP attempts, want 1",
						len(attempts),
					)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failures := 0
			if strings.Contains(test.name, "failure") {
				failures = 1
			}
			fixture := newAuthEmailOutboxFixture(t, failures)
			test.run(t, fixture)
			var delivery models.OutboxDelivery
			if err := fixture.db.First(
				&delivery,
				"id = ?",
				fixture.deliveryID,
			).Error; err != nil {
				t.Fatal(err)
			}
			if delivery.Status != models.OutboxDeliverySucceeded ||
				delivery.Attempts != 2 {
				t.Fatalf("final delivery state = %+v", delivery)
			}
		})
	}
}

func TestNotificationEmailOutboxRetriesFailedAttemptAndSkipsReplay(t *testing.T) {
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
		&models.Notification{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatal(err)
	}
	scope := installAgentplatformTestProjectScope(t, db)
	user := models.User{
		Username:     "notification-outbox-user",
		Email:        "notification-outbox@example.test",
		PasswordHash: "test-password-hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.ProjectMembership{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ProjectMembership{
		ProjectID: scope.ProjectID,
		UserID:    user.ID,
		Role:      models.ProjectRoleRequester,
		IsActive:  true,
		Version:   1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	emailAttempt := &recordingNotificationOutboxEmailSender{
		db:       db,
		failures: 1,
	}
	notifications := services.NewNotificationServiceWithProtector(db, nil)
	notifications.SetEmailNotificationService(emailAttempt)
	notification, err := notifications.CreateNotification(
		agentplatformTestOperationContext(
			t,
			scope,
			models.SystemActor("notification-service"),
		),
		&models.NotificationCreateRequest{
			Type:        models.NotificationTypeSystemAlert,
			Title:       "Outbox 通知",
			Content:     "失败由共享 worker 重试",
			Channel:     models.NotificationChannelEmail,
			RecipientID: user.ID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	installAgentplatformTestProjectScope(t, db)
	deliverer, err := NewNativeOutboxDeliverer(
		NativeOutboxDelivererOptions{
			DB:            db,
			Notifications: notifications,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	native := services.NewAgentNativeService(db, services.AgentNativeOptions{
		EventSource: "urn:test:events",
	})
	first, err := native.ProcessOutboxBatch(
		context.Background(),
		"notification-email-failure",
		10,
		deliverer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Failed != 1 || emailAttempt.callCount() != 1 {
		t.Fatalf("first batch=%+v attempts=%d", first, emailAttempt.callCount())
	}
	if err := db.Model(&models.OutboxDelivery{}).
		Where("destination_type = ?", services.EmailOutboxDestination).
		Update("next_attempt_at", time.Now().Add(-time.Minute)).
		Error; err != nil {
		t.Fatal(err)
	}
	second, err := native.ProcessOutboxBatch(
		context.Background(),
		"notification-email-retry",
		10,
		deliverer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.Delivered != 1 || emailAttempt.callCount() != 2 {
		t.Fatalf("second batch=%+v attempts=%d", second, emailAttempt.callCount())
	}
	var stored models.Notification
	if err := db.First(&stored, notification.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !stored.IsSent || !stored.IsDelivered {
		t.Fatalf("notification success receipt missing: %+v", stored)
	}

	var delivery models.OutboxDelivery
	if err := db.Preload("Event").
		Where("destination_type = ?", services.EmailOutboxDestination).
		First(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	if err := deliverer.Deliver(
		agentplatformTestOutboxWorkerContext(t, scope),
		&delivery,
		services.CloudEventFromModel(delivery.Event),
	); err != nil {
		t.Fatal(err)
	}
	if emailAttempt.callCount() != 2 {
		t.Fatal("successful notification replay performed duplicate SMTP")
	}
}
