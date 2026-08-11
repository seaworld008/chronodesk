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
		!errors.Is(
			result.Err,
			services.ErrWebhookOutboxAttemptRejected,
		) {
		t.Fatalf("shredded result = %+v", result)
	}
	if clientCreations.Load() != 1 {
		t.Fatalf("client creations = %d, want 1", clientCreations.Load())
	}
	if httpAttempts.Load() != 0 {
		t.Fatalf("revoked snapshot performed %d HTTP attempts", httpAttempts.Load())
	}
}

func TestWebhookOutboxImmutableSnapshotSurvivesOrdinarySoftDelete(
	t *testing.T,
) {
	fixture := newHTTPSecondGateFixture(t, time.Now().Add(time.Minute))
	deleteResult := fixture.db.Delete(
		&models.WebhookConfig{},
		fixture.snapshot.ConfigID,
	)
	if deleteResult.Error != nil || deleteResult.RowsAffected != 1 {
		t.Fatalf(
			"soft delete Webhook config: rows=%d err=%v",
			deleteResult.RowsAffected,
			deleteResult.Error,
		)
	}
	var deleted models.WebhookConfig
	if err := fixture.db.Unscoped().First(
		&deleted,
		fixture.snapshot.ConfigID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if !deleted.DeletedAt.Valid {
		t.Fatal("ordinary delete did not retain a soft-deleted lock anchor")
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

	result := fixture.deliverAttempt(
		t,
		deliverer,
		time.Now().Add(time.Minute),
	)
	if result.Kind != services.OutboxAttemptKnownSuccess ||
		result.Err != nil ||
		clientCreations.Load() != 1 ||
		httpAttempts.Load() != 1 {
		t.Fatalf(
			"soft-deleted immutable delivery result=%+v clients=%d HTTP=%d",
			result,
			clientCreations.Load(),
			httpAttempts.Load(),
		)
	}
}

func TestWebhookEmergencyRevokeBlocksBothHTTPGates(
	t *testing.T,
) {
	fixture := newHTTPSecondGateFixture(t, time.Now().Add(time.Minute))
	if err := fixture.db.AutoMigrate(
		&models.ProjectMembership{},
	); err != nil {
		t.Fatal(err)
	}
	var config models.WebhookConfig
	if err := fixture.db.First(
		&config,
		fixture.snapshot.ConfigID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&models.ProjectMembership{
		ProjectID: fixture.scope.ProjectID,
		UserID:    config.CreatedBy,
		Role:      models.ProjectRoleAdmin,
		IsActive:  true,
		Version:   1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	adminContext, err := services.WithOperationContext(
		context.Background(),
		services.OperationContext{
			Scope:  fixture.scope,
			Actor:  models.HumanActor(config.CreatedBy),
			Source: services.SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	revoke, err := services.NewAgentNativeService(
		fixture.db,
	).EmergencyRevokeWebhook(
		adminContext,
		config.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if revoke.InFlightDeliveries != 0 ||
		revoke.ShreddedSnapshots != 1 ||
		revoke.ExpiredDeliveries != 1 {
		t.Fatalf("emergency revoke result = %+v", revoke)
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

	result := fixture.deliverAttempt(
		t,
		deliverer,
		time.Now().Add(time.Minute),
	)
	if result.Kind != services.OutboxAttemptKnownFailure ||
		!errors.Is(
			result.Err,
			services.ErrWebhookOutboxAttemptRejected,
		) ||
		clientCreations.Load() != 0 ||
		httpAttempts.Load() != 0 {
		t.Fatalf(
			"revoked delivery result=%+v clients=%d HTTP=%d",
			result,
			clientCreations.Load(),
			httpAttempts.Load(),
		)
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
		!errors.Is(
			attempt.Err,
			services.ErrWebhookOutboxAttemptRejected,
		) {
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
	sharedClient := &http.Client{
		Timeout: time.Minute,
		Transport: httpSecondGateRoundTripper(
			func(request *http.Request) (*http.Response, error) {
				httpAttempts.Add(1)
				var ok bool
				requestDeadline, ok = request.Context().Deadline()
				if !ok {
					return nil, errors.New("request deadline is missing")
				}
				return httpSecondGateNoContentResponse(), nil
			},
		),
	}
	deliverer := fixture.deliverer(t, func(
		_ context.Context,
		_ *url.URL,
		timeout time.Duration,
	) (*http.Client, error) {
		clientCreatedAt = time.Now()
		clientTimeout = timeout
		return sharedClient, nil
	})

	result := fixture.deliverAttempt(t, deliverer, time.Now().Add(time.Minute))
	if result.Kind != services.OutboxAttemptKnownSuccess || result.Err != nil {
		t.Fatalf("bounded result = %+v", result)
	}
	if httpAttempts.Load() != 1 {
		t.Fatalf("HTTP attempts = %d, want 1", httpAttempts.Load())
	}
	if sharedClient.Timeout != time.Minute {
		t.Fatalf(
			"factory shared client timeout mutated to %s",
			sharedClient.Timeout,
		)
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
		!errors.Is(
			result.Err,
			services.ErrWebhookOutboxAttemptRejected,
		) {
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
		!errors.Is(
			result.Err,
			services.ErrWebhookOutboxAttemptRejected,
		) {
		t.Fatalf("stale claim result = %+v", result)
	}
	if httpAttempts.Load() != 0 {
		t.Fatalf("stale claim performed %d HTTP attempts", httpAttempts.Load())
	}
}

func TestWebhookOutboxRejectsCallerForgedUnsupportedEventBeforeFinalize(
	t *testing.T,
) {
	fixture := newHTTPSecondGateFixture(t, time.Now().Add(time.Minute))
	var httpAttempts atomic.Int32
	deliverer := fixture.deliverer(t, func(
		context.Context,
		*url.URL,
		time.Duration,
	) (*http.Client, error) {
		return &http.Client{Transport: httpSecondGateRoundTripper(
			func(*http.Request) (*http.Response, error) {
				httpAttempts.Add(1)
				return httpSecondGateNoContentResponse(), nil
			},
		)}, nil
	})
	forged := fixture.event
	forged.Type = "io.chronodesk.unsupported.v1"
	deadline := time.Now().Add(time.Minute)
	ctx, cancel := context.WithDeadline(
		agentplatformTestOutboxWorkerContext(t, fixture.scope),
		deadline,
	)
	result := deliverer.DeliverAttempt(
		ctx,
		&fixture.delivery,
		forged,
	)
	cancel()
	claim, err := services.OutboxClaimRefFromDelivery(&fixture.delivery)
	if err != nil {
		t.Fatal(err)
	}
	claim.EffectiveDeadline = deadline
	finalized, finalizeErr := services.NewAgentNativeService(
		fixture.db,
	).FinalizeOutboxAttempt(
		agentplatformTestOutboxWorkerContext(t, fixture.scope),
		claim,
		result,
	)
	if finalizeErr != nil {
		t.Fatalf("finalize forged event result: %v", finalizeErr)
	}
	if result.Kind != services.OutboxAttemptKnownFailure ||
		!errors.Is(
			result.Err,
			services.ErrWebhookOutboxAttemptRejected,
		) {
		t.Fatalf("forged event result = %+v", result)
	}
	if finalized.Status == models.OutboxDeliverySucceeded {
		t.Fatalf("forged event finalized as %q", finalized.Status)
	}
	var snapshot models.WebhookDeliverySnapshot
	if err := fixture.db.Take(
		&snapshot,
		"id = ?",
		fixture.snapshot.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if snapshot.CredentialShreddedAt != nil {
		t.Fatalf("forged event shredded snapshot: %+v", snapshot)
	}
	if httpAttempts.Load() != 0 {
		t.Fatalf("forged event performed %d HTTP attempts", httpAttempts.Load())
	}
}

func TestWebhookOutboxConfiguredDeadlineRejectsLateCleanEOF(
	t *testing.T,
) {
	fixture := newHTTPSecondGateFixture(t, time.Now().Add(time.Minute))
	if err := fixture.db.Table(
		(models.WebhookDeliverySnapshot{}).TableName(),
	).Where("id = ?", fixture.snapshot.ID).
		Update("timeout_seconds", 1).Error; err != nil {
		t.Fatal(err)
	}
	var (
		httpAttempts    atomic.Int32
		requestDeadline = make(chan time.Time, 1)
	)
	deliverer := fixture.deliverer(t, func(
		context.Context,
		*url.URL,
		time.Duration,
	) (*http.Client, error) {
		return &http.Client{Transport: httpSecondGateRoundTripper(
			func(request *http.Request) (*http.Response, error) {
				httpAttempts.Add(1)
				deadline, ok := request.Context().Deadline()
				if !ok {
					return nil, errors.New("request deadline missing")
				}
				requestDeadline <- deadline
				return &http.Response{
					StatusCode: http.StatusNoContent,
					Header:     make(http.Header),
					Body: &httpSecondGateLateEOFBody{
						context: request.Context(),
					},
				}, nil
			},
		)}, nil
	})
	deadline := time.Now().Add(time.Minute)
	result := fixture.deliverAttempt(t, deliverer, deadline)
	claim, err := services.OutboxClaimRefFromDelivery(&fixture.delivery)
	if err != nil {
		t.Fatal(err)
	}
	claim.EffectiveDeadline = deadline
	finalized, finalizeErr := services.NewAgentNativeService(
		fixture.db,
	).FinalizeOutboxAttempt(
		agentplatformTestOutboxWorkerContext(t, fixture.scope),
		claim,
		result,
	)
	if finalizeErr != nil {
		t.Fatalf("finalize late EOF: %v", finalizeErr)
	}
	attemptDeadline := awaitHTTPSecondGateRequestDeadline(
		t,
		requestDeadline,
	)
	if result.Kind == services.OutboxAttemptKnownSuccess {
		t.Fatalf(
			"late EOF at %s returned known success: %+v",
			time.Now(),
			result,
		)
	}
	if result.CompletedAt.IsZero() ||
		!result.CompletedAt.After(attemptDeadline) {
		t.Fatalf(
			"late EOF completion=%s deadline=%s",
			result.CompletedAt,
			attemptDeadline,
		)
	}
	if finalized.Status == models.OutboxDeliverySucceeded {
		t.Fatalf("late EOF finalized as %q", finalized.Status)
	}
	if httpAttempts.Load() != 1 {
		t.Fatalf("late EOF HTTP attempts = %d, want 1", httpAttempts.Load())
	}
}

func TestWebhookOutboxTimelyEOFSucceedsAfterLateFinalize(
	t *testing.T,
) {
	fixture := newHTTPSecondGateFixture(t, time.Now().Add(time.Minute))
	if err := fixture.db.Table(
		(models.WebhookDeliverySnapshot{}).TableName(),
	).Where("id = ?", fixture.snapshot.ID).
		Update("timeout_seconds", 1).Error; err != nil {
		t.Fatal(err)
	}
	requestDeadline := make(chan time.Time, 1)
	deliverer := fixture.deliverer(t, func(
		context.Context,
		*url.URL,
		time.Duration,
	) (*http.Client, error) {
		return &http.Client{Transport: httpSecondGateRoundTripper(
			func(request *http.Request) (*http.Response, error) {
				deadline, ok := request.Context().Deadline()
				if !ok {
					return nil, errors.New("request deadline missing")
				}
				requestDeadline <- deadline
				return httpSecondGateNoContentResponse(), nil
			},
		)}, nil
	})
	workerDeadline := time.Now().Add(time.Minute)
	result := fixture.deliverAttempt(t, deliverer, workerDeadline)
	attemptDeadline := awaitHTTPSecondGateRequestDeadline(
		t,
		requestDeadline,
	)
	if result.Kind != services.OutboxAttemptKnownSuccess ||
		result.CompletedAt.IsZero() ||
		result.CompletedAt.After(attemptDeadline) {
		t.Fatalf("timely EOF result = %+v", result)
	}
	timer := time.NewTimer(time.Until(attemptDeadline) + 20*time.Millisecond)
	<-timer.C
	claim, err := services.OutboxClaimRefFromDelivery(&fixture.delivery)
	if err != nil {
		t.Fatal(err)
	}
	claim.EffectiveDeadline = workerDeadline
	finalized, finalizeErr := services.NewAgentNativeService(
		fixture.db,
	).FinalizeOutboxAttempt(
		agentplatformTestOutboxWorkerContext(t, fixture.scope),
		claim,
		result,
	)
	if finalizeErr != nil {
		t.Fatalf("late finalize timely EOF: %v", finalizeErr)
	}
	if finalized.Status != models.OutboxDeliverySucceeded {
		t.Fatalf("timely EOF finalized as %q", finalized.Status)
	}
}

func TestWebhookOutboxMalformedBoundariesUseFixedRejection(
	t *testing.T,
) {
	tests := []struct {
		name   string
		invoke func(
			*testing.T,
			httpSecondGateFixture,
			*NativeOutboxDeliverer,
		) services.OutboxAttemptResult
	}{
		{
			name: "destination",
			invoke: func(
				t *testing.T,
				fixture httpSecondGateFixture,
				deliverer *NativeOutboxDeliverer,
			) services.OutboxAttemptResult {
				t.Helper()
				delivery := fixture.delivery
				delivery.DestinationID = "snapshot:not-a-uuid"
				return fixture.deliverAttemptFor(
					t,
					deliverer,
					&delivery,
					fixture.event,
					time.Now().Add(time.Minute),
				)
			},
		},
		{
			name: "scope",
			invoke: func(
				t *testing.T,
				fixture httpSecondGateFixture,
				deliverer *NativeOutboxDeliverer,
			) services.OutboxAttemptResult {
				t.Helper()
				delivery := fixture.delivery
				delivery.ProjectID++
				event := fixture.event
				event.ProjectID = delivery.ProjectID
				return fixture.deliverAttemptFor(
					t,
					deliverer,
					&delivery,
					event,
					time.Now().Add(time.Minute),
				)
			},
		},
		{
			name: "event",
			invoke: func(
				t *testing.T,
				fixture httpSecondGateFixture,
				deliverer *NativeOutboxDeliverer,
			) services.OutboxAttemptResult {
				t.Helper()
				event := fixture.event
				event.ID = "forged-event"
				return fixture.deliverAttemptFor(
					t,
					deliverer,
					&fixture.delivery,
					event,
					time.Now().Add(time.Minute),
				)
			},
		},
		{
			name: "nil notification event",
			invoke: func(
				t *testing.T,
				fixture httpSecondGateFixture,
				deliverer *NativeOutboxDeliverer,
			) services.OutboxAttemptResult {
				t.Helper()
				deadline := time.Now().Add(time.Minute)
				ctx, cancel := context.WithDeadline(
					agentplatformTestOutboxWorkerContext(
						t,
						fixture.scope,
					),
					deadline,
				)
				defer cancel()
				return deliverer.notifications.
					SendWebhookSnapshotOutboxAttemptResult(
						ctx,
						fixture.claim(t, deadline),
						nil,
					)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newHTTPSecondGateFixture(
				t,
				time.Now().Add(time.Minute),
			)
			var httpAttempts atomic.Int32
			deliverer := fixture.deliverer(t, func(
				context.Context,
				*url.URL,
				time.Duration,
			) (*http.Client, error) {
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
			result := test.invoke(t, fixture, deliverer)
			if result.Kind != services.OutboxAttemptKnownFailure ||
				!errors.Is(
					result.Err,
					services.ErrWebhookOutboxAttemptRejected,
				) {
				t.Fatalf("malformed boundary result = %+v", result)
			}
			if httpAttempts.Load() != 0 {
				t.Fatalf(
					"malformed boundary performed %d HTTP attempts",
					httpAttempts.Load(),
				)
			}
		})
	}
}

func TestWebhookOutboxDirectDeliverUsesFixedMalformedScopeRejection(
	t *testing.T,
) {
	fixture := newHTTPSecondGateFixture(t, time.Now().Add(time.Minute))
	var httpAttempts atomic.Int32
	deliverer := fixture.deliverer(t, func(
		context.Context,
		*url.URL,
		time.Duration,
	) (*http.Client, error) {
		return &http.Client{Transport: httpSecondGateRoundTripper(
			func(*http.Request) (*http.Response, error) {
				httpAttempts.Add(1)
				return httpSecondGateNoContentResponse(), nil
			},
		)}, nil
	})
	delivery := fixture.delivery
	delivery.ProjectID++
	event := fixture.event
	event.ProjectID = delivery.ProjectID
	ctx, cancel := context.WithDeadline(
		agentplatformTestOutboxWorkerContext(t, fixture.scope),
		time.Now().Add(time.Minute),
	)
	err := deliverer.Deliver(ctx, &delivery, event)
	cancel()
	if !errors.Is(err, services.ErrWebhookOutboxAttemptRejected) {
		t.Fatalf("direct malformed scope error = %v", err)
	}
	if httpAttempts.Load() != 0 {
		t.Fatalf(
			"direct malformed scope performed %d HTTP attempts",
			httpAttempts.Load(),
		)
	}
}

type httpSecondGateLateEOFBody struct {
	context context.Context
	once    sync.Once
}

func (body *httpSecondGateLateEOFBody) Read([]byte) (int, error) {
	body.once.Do(func() { <-body.context.Done() })
	return 0, io.EOF
}

func (*httpSecondGateLateEOFBody) Close() error {
	return nil
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

func awaitHTTPSecondGateRequestDeadline(
	t *testing.T,
	deadlines <-chan time.Time,
) time.Time {
	t.Helper()
	select {
	case deadline := <-deadlines:
		return deadline
	case <-time.After(time.Second):
		t.Fatal("HTTP transport did not report its request deadline")
		return time.Time{}
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
			"status":              models.OutboxDeliveryProcessing,
			"attempts":            1,
			"locked_at":           lockedAt,
			"locked_by":           "http-second-gate-worker",
			"lock_token":          lockToken,
			"dispatch_started_at": lockedAt,
			"expires_at":          credentialDeadline,
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
	return fixture.deliverAttemptFor(
		t,
		deliverer,
		&fixture.delivery,
		fixture.event,
		effectiveDeadline,
	)
}

func (fixture httpSecondGateFixture) deliverAttemptFor(
	t *testing.T,
	deliverer *NativeOutboxDeliverer,
	delivery *models.OutboxDelivery,
	event services.CloudEventEnvelope,
	effectiveDeadline time.Time,
) services.OutboxAttemptResult {
	t.Helper()
	ctx, cancel := context.WithDeadline(
		agentplatformTestOutboxWorkerContext(t, fixture.scope),
		effectiveDeadline,
	)
	defer cancel()
	return deliverer.DeliverAttempt(ctx, delivery, event)
}

func (fixture httpSecondGateFixture) claim(
	t *testing.T,
	effectiveDeadline time.Time,
) services.WebhookOutboxAttemptClaim {
	t.Helper()
	claim, err := services.OutboxClaimRefFromDelivery(&fixture.delivery)
	if err != nil {
		t.Fatal(err)
	}
	return services.WebhookOutboxAttemptClaim{
		DeliveryID:          fixture.delivery.ID,
		EventID:             fixture.event.ID,
		Scope:               fixture.scope,
		WorkerID:            claim.WorkerID,
		LockToken:           claim.LockToken,
		LockedAt:            claim.LockedAt,
		AttemptGeneration:   claim.Attempts,
		SnapshotDestination: fixture.delivery.DestinationID,
		EffectiveDeadline:   effectiveDeadline,
		CredentialExpiresAt: fixture.snapshot.CredentialExpiresAt,
	}
}
