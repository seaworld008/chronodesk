package services

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/security"
)

func TestWebhookDispatchStartCommitsBeforeTransportBegins(t *testing.T) {
	now := time.Now().UTC().Add(-time.Second).Truncate(time.Millisecond)
	fixture := newWebhookOutboxLifecycleFixture(t, now)
	if err := fixture.db.AutoMigrate(&models.WebhookLog{}); err != nil {
		t.Fatal(err)
	}
	claimed, claimRef := fixture.claim(t, "dispatch-start-worker")
	protector, err := security.NewKeyring(
		"dispatch-start-test",
		map[string][]byte{
			"dispatch-start-test": bytes.Repeat([]byte{0x51}, 32),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := protector.Seal(
		[]byte(testCustomWebhookSecret),
		security.FieldAAD(
			"webhook_configs",
			strconv.FormatUint(uint64(fixture.config.ID), 10),
			"secret",
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Exec(
		`UPDATE webhook_delivery_snapshots
		 SET secret = ?,
		     previous_secret = '',
		     previous_secret_expires_at = NULL,
		     access_token = ''
		 WHERE id = ?`,
		secret,
		fixture.snapshot.ID,
	).Error; err != nil {
		t.Fatal(err)
	}

	var transportCalls int
	notifications := NewNotificationServiceWithClientFactory(
		fixture.db,
		protector,
		WebhookClientFactoryFunc(func(
			_ context.Context,
			_ *url.URL,
			_ time.Duration,
		) (*http.Client, error) {
			return &http.Client{
				Transport: webhookAttemptRoundTripper(func(
					*http.Request,
				) (*http.Response, error) {
					transportCalls++
					var delivery models.OutboxDelivery
					if err := fixture.db.Where(
						"id = ?",
						claimed.ID,
					).Take(&delivery).Error; err != nil {
						t.Fatal(err)
					}
					if !isWebhookDispatchStarted(
						delivery.DispatchStartedAt,
						delivery.LockedAt,
					) {
						t.Fatal(
							"HTTP transport began before durable dispatch start committed",
						)
					}
					return &http.Response{
						StatusCode: http.StatusNoContent,
						Header:     make(http.Header),
						Body:       io.NopCloser(bytes.NewReader(nil)),
					}, nil
				}),
			}, nil
		}),
	)
	t.Cleanup(notifications.waitForWebhookAttemptAudits)
	var dispatchBoundaryCalls int
	notifications.beforeWebhookDispatchStart = func(
		context.Context,
		WebhookOutboxAttemptClaim,
	) {
		dispatchBoundaryCalls++
	}
	var committedBoundaryCalls int
	notifications.afterWebhookDispatchStart = func(
		context.Context,
		WebhookOutboxAttemptClaim,
	) {
		committedBoundaryCalls++
	}
	caller := CloudEventFromModel(&fixture.event)
	result := notifications.SendWebhookSnapshotOutboxAttemptResult(
		fixture.worker,
		WebhookOutboxAttemptClaim{
			DeliveryID:          claimed.ID,
			EventID:             claimed.EventID,
			Scope:               fixture.scope,
			WorkerID:            claimRef.WorkerID,
			LockToken:           claimRef.LockToken,
			LockedAt:            claimRef.LockedAt,
			AttemptGeneration:   claimRef.Attempts,
			SnapshotDestination: claimed.DestinationID,
			EffectiveDeadline:   claimed.ExpiresAt.UTC(),
			CredentialExpiresAt: claimed.ExpiresAt.UTC(),
		},
		&caller,
	)
	if result.Kind != OutboxAttemptKnownSuccess ||
		result.Err != nil ||
		transportCalls != 1 ||
		dispatchBoundaryCalls != 1 ||
		committedBoundaryCalls != 1 {
		t.Fatalf(
			"dispatch result=%+v transport calls=%d boundary=%d/%d",
			result,
			transportCalls,
			dispatchBoundaryCalls,
			committedBoundaryCalls,
		)
	}
}
