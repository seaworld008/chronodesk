package services

import (
	"errors"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestWebhookOutboxAttemptGateUsesInjectedClockAtDeadlineBoundary(
	t *testing.T,
) {
	deadline := time.Date(
		2026,
		time.August,
		17,
		9,
		1,
		0,
		0,
		time.UTC,
	)
	tests := []struct {
		name         string
		now          time.Time
		wantRejected bool
	}{
		{
			name: "immediately before deadline",
			now:  deadline.Add(-time.Nanosecond),
		},
		{
			name:         "equal to deadline",
			now:          deadline,
			wantRejected: true,
		},
		{
			name:         "after deadline",
			now:          deadline.Add(time.Nanosecond),
			wantRejected: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWebhookOutboxLifecycleFixture(
				t,
				deadline.Add(-models.WebhookDeliveryCredentialLifetime),
			)
			claimed, claimRef := fixture.claim(t, "clock-boundary-worker")
			notifications := &NotificationService{
				db: fixture.db,
				webhookAttemptGateClock: func() time.Time {
					return test.now
				},
			}
			_, err := notifications.validateWebhookOutboxAttemptGate(
				fixture.worker,
				webhookGateClaim(
					fixture,
					claimed,
					claimRef,
					deadline,
				),
			)
			if test.wantRejected {
				if !errors.Is(err, ErrWebhookOutboxAttemptRejected) {
					t.Fatalf(
						"gate error = %v, want attempt rejected",
						err,
					)
				}
				return
			}
			if err != nil {
				t.Fatalf("gate before deadline: %v", err)
			}
		})
	}
}

func TestWebhookOutboxAttemptGateResamplesClockBeforeDispatchStart(
	t *testing.T,
) {
	deadline := time.Date(
		2026,
		time.August,
		17,
		9,
		1,
		0,
		0,
		time.UTC,
	)
	fixture := newWebhookOutboxLifecycleFixture(
		t,
		deadline.Add(-models.WebhookDeliveryCredentialLifetime),
	)
	claimed, claimRef := fixture.claim(t, "clock-resample-worker")
	current := deadline.Add(-time.Nanosecond)
	notifications := &NotificationService{
		db: fixture.db,
		webhookAttemptGateClock: func() time.Time {
			return current
		},
	}
	claim := webhookGateClaim(
		fixture,
		claimed,
		claimRef,
		deadline,
	)
	if _, err := notifications.validateWebhookOutboxAttemptGate(
		fixture.worker,
		claim,
	); err != nil {
		t.Fatalf("first gate before deadline: %v", err)
	}

	current = deadline
	if err := notifications.beginWebhookOutboxDispatch(
		fixture.worker,
		claim,
	); !errors.Is(err, ErrWebhookOutboxAttemptRejected) {
		t.Fatalf(
			"dispatch gate after clock advance error = %v, want rejected",
			err,
		)
	}
	var delivery models.OutboxDelivery
	if err := fixture.db.First(
		&delivery,
		"id = ?",
		claimed.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if !isWebhookDispatchPrepared(
		delivery.DispatchStartedAt,
		delivery.LockedAt,
	) {
		t.Fatalf(
			"rejected dispatch changed prepared boundary: %+v",
			delivery,
		)
	}
}

func webhookGateClaim(
	fixture *webhookOutboxLifecycleFixture,
	delivery *models.OutboxDelivery,
	claim OutboxClaimRef,
	deadline time.Time,
) WebhookOutboxAttemptClaim {
	return WebhookOutboxAttemptClaim{
		DeliveryID:          delivery.ID,
		EventID:             delivery.EventID,
		Scope:               fixture.scope,
		WorkerID:            claim.WorkerID,
		LockToken:           claim.LockToken,
		LockedAt:            claim.LockedAt,
		AttemptGeneration:   claim.Attempts,
		SnapshotDestination: delivery.DestinationID,
		EffectiveDeadline:   deadline.UTC(),
		CredentialExpiresAt: delivery.ExpiresAt.UTC(),
	}
}
