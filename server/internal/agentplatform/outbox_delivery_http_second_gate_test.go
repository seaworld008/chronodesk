package agentplatform

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/security"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type httpSecondGateRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip httpSecondGateRoundTripper) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return roundTrip(request)
}

type httpSecondGateFixture struct {
	db       *gorm.DB
	scope    models.ProjectScope
	event    services.CloudEventEnvelope
	delivery models.OutboxDelivery
	snapshot models.WebhookDeliverySnapshot
	store    security.Protector
}

func TestWebhookOutboxSecondGateRejectsShreddedSnapshotBeforeHTTP(
	t *testing.T,
) {
	fixture := newHTTPSecondGateFixture(t, time.Now().Add(time.Minute))
	var (
		clientCreations atomic.Int32
		httpAttempts    atomic.Int32
	)
	deliverer := fixture.deliverer(t, func(
		_ context.Context,
		_ *url.URL,
		_ time.Duration,
	) (*http.Client, error) {
		clientCreations.Add(1)
		shreddedAt := time.Now().UTC()
		reason := models.WebhookCredentialShredReasonRevoked
		result := fixture.db.Table(
			(models.WebhookDeliverySnapshot{}).TableName(),
		).Where("id = ?", fixture.snapshot.ID).Updates(map[string]any{
			"secret":                     "",
			"previous_secret":            "",
			"previous_secret_expires_at": nil,
			"access_token":               "",
			"credential_shredded_at":     shreddedAt,
			"credential_shred_reason":    reason,
		})
		if result.Error != nil || result.RowsAffected != 1 {
			t.Fatalf("shred snapshot between gates: %v", result.Error)
		}
		return &http.Client{Transport: httpSecondGateRoundTripper(
			func(*http.Request) (*http.Response, error) {
				httpAttempts.Add(1)
				return httpSecondGateNoContentResponse(), nil
			},
		)}, nil
	})

	result := fixture.deliverAttempt(t, deliverer, time.Now().Add(time.Minute))
	if result.Kind != services.OutboxAttemptKnownFailure ||
		result.Err == nil {
		t.Fatalf("shredded result = %+v", result)
	}
	if clientCreations.Load() != 1 {
		t.Fatalf("client creations = %d, want 1", clientCreations.Load())
	}
	if httpAttempts.Load() != 0 {
		t.Fatalf("revoked snapshot performed %d HTTP attempts", httpAttempts.Load())
	}
}

func TestWebhookOutboxFirstGateRejectsMismatchedPairBeforeClientCreation(
	t *testing.T,
) {
	fixture := newHTTPSecondGateFixture(t, time.Now().Add(time.Minute))
	changedDeadline := fixture.snapshot.CredentialExpiresAt.Add(-time.Second)
	result := fixture.db.Model(&models.OutboxDelivery{}).
		Where("id = ?", fixture.delivery.ID).
		UpdateColumn("expires_at", changedDeadline)
	if result.Error != nil || result.RowsAffected != 1 {
		t.Fatalf("mismatch delivery deadline: %v", result.Error)
	}
	var (
		clientCreations atomic.Int32
		httpAttempts    atomic.Int32
	)
	deliverer := fixture.deliverer(t, func(
		context.Context,
		*url.URL,
		time.Duration,
	) (*http.Client, error) {
		clientCreations.Add(1)
		return &http.Client{Transport: httpSecondGateRoundTripper(
			func(*http.Request) (*http.Response, error) {
				httpAttempts.Add(1)
				return httpSecondGateNoContentResponse(), nil
			},
		)}, nil
	})

	attempt := fixture.deliverAttempt(t, deliverer, time.Now().Add(time.Minute))
	if attempt.Kind != services.OutboxAttemptKnownFailure ||
		attempt.Err == nil {
		t.Fatalf("mismatched pair result = %+v", attempt)
	}
	if clientCreations.Load() != 0 || httpAttempts.Load() != 0 {
		t.Fatalf(
			"mismatched pair client creations=%d HTTP=%d, want zero",
			clientCreations.Load(),
			httpAttempts.Load(),
		)
	}
}

func TestWebhookOutboxFirstGateRejectsExactPairMutation(
	t *testing.T,
) {
	tests := []struct {
		name   string
		mutate func(*testing.T, httpSecondGateFixture)
	}{
		{
			name: "event",
			mutate: func(t *testing.T, fixture httpSecondGateFixture) {
				t.Helper()
				result := fixture.db.Model(&models.OutboxDelivery{}).
					Where("id = ?", fixture.delivery.ID).
					UpdateColumn("event_id", "mutated-event")
				if result.Error != nil || result.RowsAffected != 1 {
					t.Fatalf("mutate delivery event: %v", result.Error)
				}
			},
		},
		{
			name: "destination",
			mutate: func(t *testing.T, fixture httpSecondGateFixture) {
				t.Helper()
				otherSnapshot := uuid.Must(uuid.NewV7()).String()
				result := fixture.db.Model(&models.OutboxDelivery{}).
					Where("id = ?", fixture.delivery.ID).
					UpdateColumn(
						"destination_id",
						models.WebhookDeliverySnapshotDestinationPrefix+
							otherSnapshot,
					)
				if result.Error != nil || result.RowsAffected != 1 {
					t.Fatalf(
						"mutate delivery destination: %v",
						result.Error,
					)
				}
			},
		},
		{
			name: "scope",
			mutate: func(t *testing.T, fixture httpSecondGateFixture) {
				t.Helper()
				result := fixture.db.Model(&models.OutboxDelivery{}).
					Where("id = ?", fixture.delivery.ID).
					UpdateColumn(
						"project_id",
						fixture.scope.ProjectID+1000,
					)
				if result.Error != nil || result.RowsAffected != 1 {
					t.Fatalf("mutate delivery scope: %v", result.Error)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newHTTPSecondGateFixture(
				t,
				time.Now().Add(time.Minute),
			)
			test.mutate(t, fixture)
			var (
				clientCreations atomic.Int32
				httpAttempts    atomic.Int32
			)
			deliverer := fixture.deliverer(t, func(
				context.Context,
				*url.URL,
				time.Duration,
			) (*http.Client, error) {
				clientCreations.Add(1)
				return &http.Client{
					Transport: httpSecondGateRoundTripper(
						func(*http.Request) (*http.Response, error) {
							httpAttempts.Add(1)
							return httpSecondGateNoContentResponse(),
								nil
						},
					),
				}, nil
			})

			attempt := fixture.deliverAttempt(
				t,
				deliverer,
				time.Now().Add(time.Minute),
			)
			if attempt.Kind != services.OutboxAttemptKnownFailure ||
				!errors.Is(
					attempt.Err,
					services.ErrWebhookOutboxAttemptRejected,
				) {
				t.Fatalf("mutated pair result = %+v", attempt)
			}
			if clientCreations.Load() != 0 ||
				httpAttempts.Load() != 0 {
				t.Fatalf(
					"mutated pair client creations=%d HTTP=%d, want zero",
					clientCreations.Load(),
					httpAttempts.Load(),
				)
			}
		})
	}
}

func TestWebhookOutboxRequestAndClientAreBoundedByCredentialDeadline(
	t *testing.T,
) {
	credentialDeadline := time.Now().Add(300 * time.Millisecond)
	fixture := newHTTPSecondGateFixture(t, credentialDeadline)
	var (
		clientTimeout   time.Duration
		clientCreatedAt time.Time
		requestDeadline time.Time
		httpAttempts    atomic.Int32
	)
	deliverer := fixture.deliverer(t, func(
		_ context.Context,
		_ *url.URL,
		timeout time.Duration,
	) (*http.Client, error) {
		clientCreatedAt = time.Now()
		clientTimeout = timeout
		return &http.Client{Transport: httpSecondGateRoundTripper(
			func(request *http.Request) (*http.Response, error) {
				httpAttempts.Add(1)
				var ok bool
				requestDeadline, ok = request.Context().Deadline()
				if !ok {
					return nil, errors.New("request deadline is missing")
				}
				return httpSecondGateNoContentResponse(), nil
			},
		)}, nil
	})

	result := fixture.deliverAttempt(t, deliverer, time.Now().Add(time.Minute))
	if result.Kind != services.OutboxAttemptKnownSuccess || result.Err != nil {
		t.Fatalf("bounded result = %+v", result)
	}
	if httpAttempts.Load() != 1 {
		t.Fatalf("HTTP attempts = %d, want 1", httpAttempts.Load())
	}
	if requestDeadline.After(fixture.snapshot.CredentialExpiresAt) {
		t.Fatalf(
			"request deadline %s exceeds credential deadline %s",
			requestDeadline,
			fixture.snapshot.CredentialExpiresAt,
		)
	}
	remainingAtFactory := fixture.snapshot.CredentialExpiresAt.Sub(
		clientCreatedAt,
	)
	if clientTimeout <= 0 ||
		clientTimeout > remainingAtFactory+5*time.Millisecond {
		t.Fatalf(
			"client timeout %s exceeds credential remaining window %s",
			clientTimeout,
			remainingAtFactory,
		)
	}
}

func TestWebhookOutboxDeadlineElapsedAfterClientCreationSkipsHTTP(
	t *testing.T,
) {
	credentialDeadline := time.Now().Add(time.Minute)
	fixture := newHTTPSecondGateFixture(t, credentialDeadline)
	var (
		clientCreations atomic.Int32
		httpAttempts    atomic.Int32
	)
	deliverer := fixture.deliverer(t, func(
		ctx context.Context,
		_ *url.URL,
		_ time.Duration,
	) (*http.Client, error) {
		clientCreations.Add(1)
		<-ctx.Done()
		return &http.Client{Transport: httpSecondGateRoundTripper(
			func(*http.Request) (*http.Response, error) {
				httpAttempts.Add(1)
				return httpSecondGateNoContentResponse(), nil
			},
		)}, nil
	})

	result := fixture.deliverAttempt(
		t,
		deliverer,
		time.Now().Add(50*time.Millisecond),
	)
	if result.Kind != services.OutboxAttemptKnownFailure ||
		result.Err == nil {
		t.Fatalf("elapsed deadline result = %+v", result)
	}
	if clientCreations.Load() != 1 {
		t.Fatalf("client creations = %d, want 1", clientCreations.Load())
	}
	if httpAttempts.Load() != 0 {
		t.Fatalf("expired attempt performed %d HTTP requests", httpAttempts.Load())
	}
}

func TestWebhookOutboxSecondGateRejectsNewClaimGeneration(
	t *testing.T,
) {
	fixture := newHTTPSecondGateFixture(t, time.Now().Add(time.Minute))
	var httpAttempts atomic.Int32
	deliverer := fixture.deliverer(t, func(
		context.Context,
		*url.URL,
		time.Duration,
	) (*http.Client, error) {
		newToken := uuid.Must(uuid.NewV7()).String()
		result := fixture.db.Model(&models.OutboxDelivery{}).
			Where("id = ?", fixture.delivery.ID).
			Updates(map[string]any{
				"attempts":   fixture.delivery.Attempts + 1,
				"lock_token": newToken,
			})
		if result.Error != nil || result.RowsAffected != 1 {
			t.Fatalf("replace claim generation: %v", result.Error)
		}
		return &http.Client{Transport: httpSecondGateRoundTripper(
			func(*http.Request) (*http.Response, error) {
				httpAttempts.Add(1)
				return httpSecondGateNoContentResponse(), nil
			},
		)}, nil
	})

	result := fixture.deliverAttempt(t, deliverer, time.Now().Add(time.Minute))
	if result.Kind != services.OutboxAttemptKnownFailure ||
		result.Err == nil {
		t.Fatalf("stale claim result = %+v", result)
	}
	if httpAttempts.Load() != 0 {
		t.Fatalf("stale claim performed %d HTTP attempts", httpAttempts.Load())
	}
}

func TestWebhookOutboxValidClaimCompletesAtBodyEOFWithoutAuditWait(
	t *testing.T,
) {
	fixture := newHTTPSecondGateFixture(t, time.Now().Add(time.Minute))
	var (
		httpAttempts atomic.Int32
		bodyEOF      = make(chan struct{})
		releaseAudit = make(chan struct{})
		auditLock    = make(chan *gorm.DB, 1)
	)
	deliverer := fixture.deliverer(t, func(
		context.Context,
		*url.URL,
		time.Duration,
	) (*http.Client, error) {
		return &http.Client{Transport: httpSecondGateRoundTripper(
			func(*http.Request) (*http.Response, error) {
				httpAttempts.Add(1)
				return &http.Response{
					StatusCode: http.StatusNoContent,
					Header:     make(http.Header),
					Body: &httpSecondGateEOFBody{
						reader: bytes.NewReader([]byte("complete")),
						eof:    bodyEOF,
						onEOF: func() {
							tx := fixture.db.Begin()
							if tx.Error != nil {
								t.Errorf("begin audit lock: %v", tx.Error)
								return
							}
							var count int64
							if err := tx.Model(&models.WebhookLog{}).
								Count(&count).Error; err != nil {
								t.Errorf("hold audit lock: %v", err)
								_ = tx.Rollback().Error
								return
							}
							auditLock <- tx
						},
					},
				}, nil
			},
		)}, nil
	})

	resultCh := make(chan services.OutboxAttemptResult, 1)
	go func() {
		resultCh <- fixture.deliverAttempt(
			t,
			deliverer,
			time.Now().Add(time.Minute),
		)
	}()
	select {
	case <-bodyEOF:
	case <-time.After(time.Second):
		close(releaseAudit)
		t.Fatal("response body did not reach EOF")
	}
	var tx *gorm.DB
	select {
	case tx = <-auditLock:
	case <-time.After(time.Second):
		close(releaseAudit)
		t.Fatal("audit persistence was not blocked after body EOF")
	}
	rollbackDone := make(chan struct{})
	go func() {
		<-releaseAudit
		_ = tx.Rollback().Error
		close(rollbackDone)
	}()
	var releaseOnce sync.Once
	releaseBlockedAudit := func() {
		releaseOnce.Do(func() { close(releaseAudit) })
		<-rollbackDone
	}
	select {
	case result := <-resultCh:
		if result.Kind != services.OutboxAttemptKnownSuccess ||
			result.Err != nil ||
			result.CompletedAt.IsZero() {
			releaseBlockedAudit()
			t.Fatalf("valid result = %+v", result)
		}
	case <-time.After(200 * time.Millisecond):
		releaseBlockedAudit()
		t.Fatal("attempt waited for post-response audit persistence")
	}
	releaseBlockedAudit()
	if httpAttempts.Load() != 1 {
		t.Fatalf("HTTP attempts = %d, want 1", httpAttempts.Load())
	}
}

type httpSecondGateEOFBody struct {
	reader io.Reader
	eof    chan<- struct{}
	once   sync.Once
	onEOF  func()
}

func (body *httpSecondGateEOFBody) Read(buffer []byte) (int, error) {
	count, err := body.reader.Read(buffer)
	if errors.Is(err, io.EOF) {
		body.once.Do(func() {
			if body.onEOF != nil {
				body.onEOF()
			}
			close(body.eof)
		})
	}
	return count, err
}

func (*httpSecondGateEOFBody) Close() error {
	return nil
}

func httpSecondGateNoContentResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusNoContent,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

func newHTTPSecondGateFixture(
	t *testing.T,
	credentialDeadline time.Time,
) httpSecondGateFixture {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") +
		"?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(
		&models.User{},
		&models.WebhookConfig{},
		&models.WebhookDeliverySnapshot{},
		&models.WebhookLog{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatalf("migrate HTTP second-gate fixture: %v", err)
	}
	projectFixture := ensureAPIHandlerTestProject(t, db)
	scope := projectFixture.project.Scope()
	owner := models.User{
		Username:     "http-second-gate-" + strings.ReplaceAll(t.Name(), "/", "-"),
		Email:        "http-second-gate-" + strings.ReplaceAll(t.Name(), "/", "-") + "@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	config := models.WebhookConfig{
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		Name:           "HTTP second gate",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     "https://webhook.example.test/callback",
		Status:         models.WebhookStatusActive,
		EnabledEventsObj: []models.WebhookEventType{
			models.WebhookEventTicketCreated,
		},
		TimeoutSeconds: 30,
		CreatedBy:      owner.ID,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	store := newAgentplatformWebhookTestProtector(t)
	envelope, err := security.ProtectOptional(
		store,
		agentplatformCustomWebhookTestSecret,
		security.FieldAAD(
			"webhook_configs",
			strconv.FormatUint(uint64(config.ID), 10),
			"secret",
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.WebhookConfig{}).
		Where("id = ?", config.ID).
		UpdateColumn("secret", envelope).Error; err != nil {
		t.Fatal(err)
	}
	eventModel, err := appendTestDomainEvent(
		context.Background(),
		services.NewAgentNativeService(db),
		services.DomainEventInput{
			Type:            "io.chronodesk.ticket.created.v1",
			Subject:         "ticket/42",
			Actor:           models.SystemActor("http-second-gate-test"),
			ResourceVersion: 1,
			Scope:           scope,
			Data:            map[string]any{"ticket_id": 42},
		},
		[]services.OutboxTarget{{
			Type:        "webhook",
			ID:          "configured",
			MaxAttempts: 3,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	var delivery models.OutboxDelivery
	if err := db.Where(
		"event_id = ? AND destination_type = ?",
		eventModel.ID,
		"webhook",
	).Take(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	snapshotID, err := models.ParseWebhookDeliverySnapshotDestinationID(
		delivery.DestinationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot models.WebhookDeliverySnapshot
	if err := db.Take(&snapshot, "id = ?", snapshotID).Error; err != nil {
		t.Fatal(err)
	}
	credentialDeadline = credentialDeadline.UTC()
	if err := db.Table(
		(models.WebhookDeliverySnapshot{}).TableName(),
	).Where("id = ?", snapshot.ID).Update(
		"credential_expires_at",
		credentialDeadline,
	).Error; err != nil {
		t.Fatal(err)
	}
	lockToken := uuid.Must(uuid.NewV7()).String()
	lockedAt := time.Now().UTC()
	if err := db.Model(&models.OutboxDelivery{}).
		Where("id = ?", delivery.ID).
		Updates(map[string]any{
			"status":     models.OutboxDeliveryProcessing,
			"attempts":   1,
			"locked_at":  lockedAt,
			"locked_by":  "http-second-gate-worker",
			"lock_token": lockToken,
			"expires_at": credentialDeadline,
		}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Take(&delivery, "id = ?", delivery.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Take(&snapshot, "id = ?", snapshot.ID).Error; err != nil {
		t.Fatal(err)
	}
	return httpSecondGateFixture{
		db:       db,
		scope:    scope,
		event:    services.CloudEventFromModel(eventModel),
		delivery: delivery,
		snapshot: snapshot,
		store:    store,
	}
}

func (fixture httpSecondGateFixture) deliverer(
	t *testing.T,
	factory services.WebhookClientFactoryFunc,
) *NativeOutboxDeliverer {
	t.Helper()
	notifications := services.NewNotificationServiceWithClientFactory(
		fixture.db,
		fixture.store,
		factory,
	)
	t.Cleanup(notifications.CloseWebhookAttemptAuditsAndWait)
	deliverer, err := NewNativeOutboxDeliverer(
		NativeOutboxDelivererOptions{
			DB:            fixture.db,
			Notifications: notifications,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return deliverer
}

func (fixture httpSecondGateFixture) deliverAttempt(
	t *testing.T,
	deliverer *NativeOutboxDeliverer,
	effectiveDeadline time.Time,
) services.OutboxAttemptResult {
	t.Helper()
	ctx, cancel := context.WithDeadline(
		agentplatformTestOutboxWorkerContext(t, fixture.scope),
		effectiveDeadline,
	)
	defer cancel()
	return deliverer.DeliverAttempt(ctx, &fixture.delivery, fixture.event)
}
