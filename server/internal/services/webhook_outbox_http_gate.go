package services

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

var ErrWebhookOutboxAttemptRejected = errors.New(
	"webhook outbox attempt rejected",
)

// WebhookOutboxAttemptClaim is the immutable worker claim carried across the
// adapter/service boundary and both short database gates. AttemptGeneration is
// the claimed delivery attempt count; together with LockedAt and LockToken it
// prevents the same worker identity from reusing a stale generation.
type WebhookOutboxAttemptClaim struct {
	DeliveryID          string
	EventID             string
	Scope               models.ProjectScope
	WorkerID            string
	LockToken           string
	LockedAt            time.Time
	AttemptGeneration   int
	SnapshotDestination string
	EffectiveDeadline   time.Time
	CredentialExpiresAt time.Time
}

type webhookOutboxGateState struct {
	snapshot models.WebhookDeliverySnapshot
	event    models.DomainEvent
}

func (claim WebhookOutboxAttemptClaim) validate() error {
	if strings.TrimSpace(claim.DeliveryID) == "" ||
		strings.TrimSpace(claim.EventID) == "" ||
		strings.TrimSpace(claim.WorkerID) == "" ||
		strings.TrimSpace(claim.LockToken) == "" ||
		claim.LockedAt.IsZero() ||
		claim.AttemptGeneration <= 0 ||
		claim.EffectiveDeadline.IsZero() ||
		claim.CredentialExpiresAt.IsZero() ||
		!claim.LockedAt.Before(claim.EffectiveDeadline) ||
		!claim.LockedAt.Before(claim.CredentialExpiresAt) ||
		claim.EffectiveDeadline.After(claim.CredentialExpiresAt) {
		return ErrWebhookOutboxAttemptRejected
	}
	if err := claim.Scope.Validate(); err != nil {
		return ErrWebhookOutboxAttemptRejected
	}
	if _, err := models.ParseWebhookDeliverySnapshotDestinationID(
		claim.SnapshotDestination,
	); err != nil {
		return ErrWebhookOutboxAttemptRejected
	}
	return nil
}

func (claim WebhookOutboxAttemptClaim) outboxClaimRef() OutboxClaimRef {
	return OutboxClaimRef{
		DeliveryID:        claim.DeliveryID,
		WorkerID:          claim.WorkerID,
		LockToken:         claim.LockToken,
		LockedAt:          claim.LockedAt,
		Attempts:          claim.AttemptGeneration,
		EffectiveDeadline: claim.EffectiveDeadline,
	}
}

func (ns *NotificationService) validateWebhookOutboxAttemptGate(
	ctx context.Context,
	claim WebhookOutboxAttemptClaim,
) (webhookOutboxGateState, error) {
	if ns == nil || ns.db == nil || ctx == nil {
		return webhookOutboxGateState{},
			ErrWebhookOutboxAttemptRejected
	}
	if err := claim.validate(); err != nil || ctx.Err() != nil {
		return webhookOutboxGateState{},
			ErrWebhookOutboxAttemptRejected
	}
	now := time.Now().UTC()
	if !now.Before(claim.EffectiveDeadline.UTC()) ||
		!now.Before(claim.CredentialExpiresAt.UTC()) {
		return webhookOutboxGateState{},
			ErrWebhookOutboxAttemptRejected
	}
	state, err := withNotificationProjectOperation(
		ns,
		ctx,
		claim.Scope,
		func(scopedContext context.Context) (
			webhookOutboxGateState,
			error,
		) {
			var validated webhookOutboxGateState
			err := transactionForContext(
				scopedContext,
				ns.db,
				func(tx *gorm.DB) error {
					if err := lockWebhookLifecycleProject(
						tx,
						claim.Scope,
					); err != nil {
						return err
					}
					if _, err := lockWebhookConfigForDestination(
						tx,
						claim.Scope,
						claim.SnapshotDestination,
					); err != nil {
						return err
					}
					delivery, err := lockClaimedOutboxDelivery(
						tx,
						claim.Scope,
						claim.outboxClaimRef(),
					)
					if err != nil {
						return err
					}
					if delivery.EventID != claim.EventID ||
						delivery.DestinationType != "webhook" ||
						delivery.DestinationID !=
							claim.SnapshotDestination ||
						delivery.ExpiresAt == nil ||
						!delivery.ExpiresAt.UTC().Equal(
							claim.CredentialExpiresAt.UTC(),
						) {
						return ErrWebhookOutboxLifecycleInvariant
					}
					if err := tx.
						Where(
							"id = ? AND organization_id = ? AND project_id = ?",
							claim.EventID,
							claim.Scope.OrganizationID,
							claim.Scope.ProjectID,
						).
						Take(&validated.event).Error; err != nil {
						return ErrWebhookOutboxLifecycleInvariant
					}
					snapshot, err := lockWebhookSnapshotForDelivery(
						tx,
						delivery,
					)
					if err != nil {
						return err
					}
					now := time.Now().UTC()
					if snapshot.CredentialShreddedAt != nil ||
						!snapshot.CredentialExpiresAt.UTC().Equal(
							claim.CredentialExpiresAt.UTC(),
						) ||
						!now.Before(claim.EffectiveDeadline.UTC()) ||
						!now.Before(
							snapshot.CredentialExpiresAt.UTC(),
						) {
						return ErrWebhookOutboxLifecycleInvariant
					}
					validated.snapshot = *snapshot
					return nil
				},
			)
			return validated, err
		},
	)
	if err != nil {
		return webhookOutboxGateState{},
			ErrWebhookOutboxAttemptRejected
	}
	return state, nil
}
