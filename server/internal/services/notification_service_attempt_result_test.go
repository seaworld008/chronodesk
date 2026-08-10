package services

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type webhookAttemptRoundTripper func(
	*http.Request,
) (*http.Response, error)

func (transport webhookAttemptRoundTripper) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return transport(request)
}

type webhookAttemptErrorBody struct{}

func (webhookAttemptErrorBody) Read([]byte) (int, error) {
	return 0, errors.New("response body read failed")
}

func (webhookAttemptErrorBody) Close() error {
	return nil
}

type webhookAttemptEOFBody struct {
	reader    io.Reader
	completed chan<- time.Time
	once      sync.Once
}

func (body *webhookAttemptEOFBody) Read(buffer []byte) (int, error) {
	count, err := body.reader.Read(buffer)
	if err == io.EOF {
		body.once.Do(func() {
			body.completed <- time.Now().UTC()
		})
	}
	return count, err
}

func (*webhookAttemptEOFBody) Close() error {
	return nil
}

type notificationRichAttemptDeliverer struct {
	service *NotificationService
	config  *models.WebhookConfig
	event   *NotificationEvent
}

func (deliverer notificationRichAttemptDeliverer) Deliver(
	ctx context.Context,
	_ *models.OutboxDelivery,
	_ CloudEventEnvelope,
) error {
	return deliverer.service.sendWebhookAttempt(
		ctx,
		deliverer.config,
		deliverer.event,
	)
}

func (deliverer notificationRichAttemptDeliverer) DeliverAttempt(
	ctx context.Context,
	_ *models.OutboxDelivery,
	_ CloudEventEnvelope,
) OutboxAttemptResult {
	return deliverer.service.sendWebhookAttemptResult(
		ctx,
		deliverer.config,
		deliverer.event,
	)
}

func TestWebhookHTTPAttemptResultClassifiesKnownAndUncertainOutcomes(
	t *testing.T,
) {
	for _, test := range []struct {
		name string
		run  webhookAttemptRoundTripper
		want OutboxAttemptResultKind
	}{
		{
			name: "bounded_non_2xx_is_known_failure",
			run: func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusBadGateway,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("rejected")),
				}, nil
			},
			want: OutboxAttemptKnownFailure,
		},
		{
			name: "transport_error_after_do_is_uncertain",
			run: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("transport reset")
			},
			want: OutboxAttemptUncertain,
		},
		{
			name: "response_body_error_is_uncertain",
			run: func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusNoContent,
					Header:     make(http.Header),
					Body:       webhookAttemptErrorBody{},
				}, nil
			},
			want: OutboxAttemptUncertain,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, config, event := newWebhookAttemptResultFixture(
				t,
				test.run,
			)
			result := service.sendWebhookAttemptResult(
				context.Background(),
				&config,
				event,
			)
			if result.Kind != test.want {
				t.Fatalf(
					"attempt result kind = %s, want %s; err=%v",
					result.Kind,
					test.want,
					result.Err,
				)
			}
			if test.want == OutboxAttemptKnownFailure ||
				test.want == OutboxAttemptUncertain {
				if result.Err == nil || !result.CompletedAt.IsZero() {
					t.Fatalf("failure result is incomplete: %+v", result)
				}
			}
		})
	}
}

func TestWebhookHTTPSuccessCompletionIsBodyCompletionNotAuditCompletion(
	t *testing.T,
) {
	bodyCompleted := make(chan time.Time, 1)
	service, config, event := newWebhookAttemptResultFixture(
		t,
		func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusNoContent,
				Header:     make(http.Header),
				Body: &webhookAttemptEOFBody{
					reader:    strings.NewReader("bounded response"),
					completed: bodyCompleted,
				},
			}, nil
		},
	)
	const callbackName = "test:webhook_attempt_slow_audit"
	if err := service.db.Callback().Create().Before("gorm:create").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Table == "webhook_logs" {
				time.Sleep(75 * time.Millisecond)
			}
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		service.waitForWebhookAttemptAudits()
		_ = service.db.Callback().Create().Remove(callbackName)
	})
	deadline := time.Now().UTC().Add(50 * time.Millisecond)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	result := service.sendWebhookAttemptResult(ctx, &config, event)
	receiverDone := <-bodyCompleted
	returnedAt := time.Now().UTC()
	if result.Kind != OutboxAttemptKnownSuccess {
		t.Fatalf("HTTP 2xx result = %+v, want known success", result)
	}
	if result.CompletedAt.Before(receiverDone) ||
		result.CompletedAt.After(deadline) ||
		returnedAt.After(deadline) {
		t.Fatalf(
			"completion=%s receiver=%s deadline=%s return=%s",
			result.CompletedAt,
			receiverDone,
			deadline,
			returnedAt,
		)
	}
	service.waitForWebhookAttemptAudits()
	var audit models.WebhookLog
	if err := service.db.Order("id DESC").First(&audit).Error; err != nil {
		t.Fatal(err)
	}
	if !audit.CreatedAt.Equal(result.CompletedAt) {
		t.Fatalf(
			"audit created_at=%s, want completed_at=%s",
			audit.CreatedAt,
			result.CompletedAt,
		)
	}
}

func TestWebhookOutboxWorkerKeepsTimelySuccessAcrossSlowPostResponseAudit(
	t *testing.T,
) {
	now := time.Now().UTC()
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	service, config, event := newWebhookAttemptResultFixture(
		t,
		func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusNoContent,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		},
	)
	const callbackName = "test:webhook_attempt_worker_slow_audit"
	if err := service.db.Callback().Create().Before("gorm:create").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Table == "webhook_logs" {
				time.Sleep(time.Second)
			}
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		service.waitForWebhookAttemptAudits()
		_ = service.db.Callback().Create().Remove(callbackName)
	})

	deadline := now.Add(750 * time.Millisecond)
	if err := fixture.db.Exec(
		"UPDATE outbox_deliveries SET expires_at = ? WHERE id = ?",
		deadline,
		fixture.delivery.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Exec(
		"UPDATE webhook_delivery_snapshots "+
			"SET credential_expires_at = ? WHERE id = ?",
		deadline,
		fixture.snapshot.ID,
	).Error; err != nil {
		t.Fatal(err)
	}

	batch, err := fixture.service.ProcessOutboxBatch(
		context.Background(),
		"timely-success-slow-audit-worker",
		1,
		notificationRichAttemptDeliverer{
			service: service,
			config:  &config,
			event:   event,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var delivery models.OutboxDelivery
	if err := fixture.db.First(
		&delivery,
		"id = ?",
		fixture.delivery.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if batch.Delivered != 1 ||
		batch.Expired != 0 ||
		delivery.Status != models.OutboxDeliverySucceeded ||
		delivery.DeliveredAt == nil ||
		delivery.DeliveredAt.After(deadline) {
		t.Fatalf(
			"slow audit changed timely result: batch=%+v delivery=%+v deadline=%s",
			batch,
			delivery,
			deadline,
		)
	}
}

func TestWebhookOutboxBatchPersistsEveryAuditBeyondWriterConcurrency(
	t *testing.T,
) {
	now := time.Now().UTC()
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	service, config, event := newWebhookAttemptResultFixture(
		t,
		func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusNoContent,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		},
	)
	const attempts = webhookAttemptAuditConcurrency + 4
	for index := 1; index < attempts; index++ {
		fixture.createIntent(
			t,
			"complete-audit-"+strconv.Itoa(index),
		)
	}
	fixture.service.outboxDeliverySlots = make(chan struct{}, 4)
	result, err := fixture.service.ProcessOutboxBatch(
		context.Background(),
		"complete-audit-worker",
		attempts,
		notificationRichAttemptDeliverer{
			service: service,
			config:  &config,
			event:   event,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Claimed != attempts ||
		result.Delivered != attempts ||
		result.consumed != attempts {
		t.Fatalf("multi-wave batch = %+v, want %d delivered", result, attempts)
	}
	service.WaitForWebhookAttemptAudits()
	var logs int64
	if err := service.db.Model(&models.WebhookLog{}).
		Where("config_id = ?", config.ID).
		Count(&logs).Error; err != nil {
		t.Fatal(err)
	}
	var stored models.WebhookConfig
	if err := service.db.First(&stored, config.ID).Error; err != nil {
		t.Fatal(err)
	}
	if logs != attempts ||
		stored.TotalSent != attempts ||
		stored.TotalSuccess != attempts ||
		stored.TotalFailed != 0 ||
		service.webhookAttemptAuditDrops.Load() != 0 {
		t.Fatalf(
			"multi-wave audits logs=%d stats=%d/%d/%d drops=%d",
			logs,
			stored.TotalSent,
			stored.TotalSuccess,
			stored.TotalFailed,
			service.webhookAttemptAuditDrops.Load(),
		)
	}
}

func TestWebhookAttemptStatsRejectOlderReversedCompletion(
	t *testing.T,
) {
	service, config, _ := newWebhookAttemptResultFixture(
		t,
		func(*http.Request) (*http.Response, error) {
			return nil, errors.New("unused transport")
		},
	)
	newer := time.Date(
		2026,
		time.August,
		10,
		5,
		0,
		2,
		0,
		time.UTC,
	)
	older := newer.Add(-time.Second)
	service.updateConfigStats(
		context.Background(),
		&config,
		true,
		nil,
		newer,
	)
	service.updateConfigStats(
		context.Background(),
		&config,
		false,
		errors.New("older rejected attempt"),
		older,
	)
	var stored models.WebhookConfig
	if err := service.db.First(&stored, config.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.TotalSent != 2 ||
		stored.TotalSuccess != 1 ||
		stored.TotalFailed != 1 ||
		stored.LastTriggeredAt == nil ||
		!stored.LastTriggeredAt.Equal(newer) ||
		stored.LastSuccessAt == nil ||
		!stored.LastSuccessAt.Equal(newer) ||
		stored.LastErrorAt == nil ||
		!stored.LastErrorAt.Equal(older) ||
		stored.LastError != "" {
		t.Fatalf("reversed completion corrupted webhook stats: %+v", stored)
	}
}

func TestWebhookAttemptAuditBatchCapacityMatchesHardOutboxBound(
	t *testing.T,
) {
	batch := newOutboxAttemptAuditBatch(500)
	work := webhookAttemptAuditWork{
		source:  context.Background(),
		persist: func(context.Context) {},
	}
	for index := 0; index < webhookAttemptAuditBatchLimit; index++ {
		if !batch.enqueue(work) {
			t.Fatalf("bounded batch rejected item %d", index)
		}
	}
	if batch.enqueue(work) {
		t.Fatal("bounded batch accepted item beyond hard Outbox limit")
	}
}

func TestWebhookAttemptAuditCloseWaitsAdmittedAndRejectsLateSubmit(
	t *testing.T,
) {
	service, config, _ := newWebhookAttemptResultFixture(
		t,
		func(*http.Request) (*http.Response, error) {
			return nil, errors.New("unused transport")
		},
	)
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAudit := func() {
		releaseOnce.Do(func() { close(release) })
	}
	t.Cleanup(releaseAudit)
	const callbackName = "test:webhook_attempt_audit_close"
	if err := service.db.Callback().Create().Before("gorm:create").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Table == "webhook_logs" {
				select {
				case <-entered:
				default:
					close(entered)
				}
				<-release
			}
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = service.db.Callback().Create().Remove(callbackName)
	})

	service.finishWebhookAttempt(
		context.Background(),
		&config,
		&models.WebhookLog{
			OrganizationID: config.OrganizationID,
			ProjectID:      config.ProjectID,
			ConfigID:       config.ID,
			Status:         "success",
		},
		OutboxKnownSuccess(time.Now().UTC()),
		false,
		true,
		nil,
		false,
	)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("admitted audit did not start")
	}
	closed := make(chan struct{})
	go func() {
		service.CloseWebhookAttemptAuditsAndWait()
		close(closed)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		service.webhookAttemptAuditMu.Lock()
		admissionClosed := service.webhookAttemptAuditClosed
		service.webhookAttemptAuditMu.Unlock()
		if admissionClosed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("audit admission did not close")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-closed:
		t.Fatal("audit close returned before admitted audit completed")
	default:
	}

	before := service.webhookAttemptAuditDrops.Load()
	service.finishWebhookAttempt(
		context.Background(),
		&config,
		&models.WebhookLog{
			OrganizationID: config.OrganizationID,
			ProjectID:      config.ProjectID,
			ConfigID:       config.ID,
			Status:         "success",
		},
		OutboxKnownSuccess(time.Now().UTC()),
		false,
		true,
		nil,
		false,
	)
	if service.webhookAttemptAuditDrops.Load() != before+1 {
		t.Fatal("late audit submit was not rejected")
	}
	releaseAudit()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("audit close did not join admitted work")
	}
	var count int64
	if err := service.db.Model(&models.WebhookLog{}).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("persisted webhook audits = %d, want 1", count)
	}
}

func TestWebhookAttemptAuditCloseCancelsPermanentlyBlockedPersistence(
	t *testing.T,
) {
	service, config, _ := newWebhookAttemptResultFixture(
		t,
		func(*http.Request) (*http.Response, error) {
			return nil, errors.New("unused transport")
		},
	)
	entered := make(chan struct{})
	safetyRelease := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(safetyRelease) })
	}
	t.Cleanup(release)
	const callbackName = "test:webhook_attempt_audit_permanent_block"
	if err := service.db.Callback().Create().Before("gorm:create").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Table != "webhook_logs" {
				return
			}
			select {
			case <-entered:
			default:
				close(entered)
			}
			select {
			case <-tx.Statement.Context.Done():
			case <-safetyRelease:
			}
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = service.db.Callback().Create().Remove(callbackName)
	})
	service.finishWebhookAttempt(
		context.Background(),
		&config,
		&models.WebhookLog{
			OrganizationID: config.OrganizationID,
			ProjectID:      config.ProjectID,
			ConfigID:       config.ID,
			Status:         "success",
		},
		OutboxKnownSuccess(time.Now().UTC()),
		false,
		true,
		nil,
		false,
	)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("permanently blocked audit did not start")
	}
	closed := make(chan struct{})
	go func() {
		service.CloseWebhookAttemptAuditsAndWait()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(750 * time.Millisecond):
		release()
		<-closed
		t.Fatal("audit close exceeded bounded shutdown budget")
	}
	if len(service.webhookAttemptAuditWriters) != 0 {
		t.Fatalf(
			"audit close retained %d writer slots",
			len(service.webhookAttemptAuditWriters),
		)
	}
}

func newWebhookAttemptResultFixture(
	t *testing.T,
	transport webhookAttemptRoundTripper,
) (*NotificationService, models.WebhookConfig, *NotificationEvent) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(t.TempDir()+"/webhook-attempt.sqlite"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal("open isolated webhook attempt SQLite database")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(
		&models.User{},
		&models.WebhookConfig{},
		&models.WebhookLog{},
	); err != nil {
		t.Fatal(err)
	}
	owner := models.User{
		Username:     "rich-http-" + strings.ReplaceAll(t.Name(), "/", "-"),
		Email:        "rich-http-" + strings.ReplaceAll(t.Name(), "/", "-") + "@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	config := models.WebhookConfig{
		OrganizationID: 1,
		ProjectID:      1,
		Name:           "rich HTTP attempt",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     "https://loopback.invalid.test/hook",
		Status:         models.WebhookStatusActive,
		CreatedBy:      owner.ID,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	config.Secret = testCustomWebhookSecret
	service := NewNotificationServiceWithProtector(db, nil)
	t.Cleanup(service.waitForWebhookAttemptAudits)
	service.webhookClients = WebhookClientFactoryFunc(func(
		context.Context,
		*url.URL,
		time.Duration,
	) (*http.Client, error) {
		return &http.Client{Transport: transport}, nil
	})
	event, _ := newTestCustomWebhookEvent(
		t,
		"rich-http-attempt",
		models.WebhookEventTicketCreated,
	)
	return service, config, event
}
