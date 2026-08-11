package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrWebhookOutboxLifecycleInvariant = errors.New(
	"webhook outbox lifecycle invariant failed",
)

type OutboxClaimRef struct {
	DeliveryID        string
	WorkerID          string
	LockToken         string
	LockedAt          time.Time
	Attempts          int
	EffectiveDeadline time.Time
}

func OutboxClaimRefFromDelivery(
	delivery *models.OutboxDelivery,
) (OutboxClaimRef, error) {
	if delivery == nil ||
		delivery.ID == "" ||
		delivery.LockedBy == "" ||
		delivery.LockToken == nil ||
		strings.TrimSpace(*delivery.LockToken) == "" ||
		delivery.LockedAt == nil ||
		delivery.Attempts <= 0 {
		return OutboxClaimRef{}, ErrOutboxLockLost
	}
	claim := OutboxClaimRef{
		DeliveryID: delivery.ID,
		WorkerID:   delivery.LockedBy,
		LockToken:  *delivery.LockToken,
		LockedAt:   *delivery.LockedAt,
		Attempts:   delivery.Attempts,
	}
	if delivery.DestinationType == "webhook" {
		if delivery.ExpiresAt == nil {
			return OutboxClaimRef{}, ErrOutboxLockLost
		}
		claim.EffectiveDeadline = *delivery.ExpiresAt
	}
	return claim, nil
}

func newOutboxLockToken() (string, error) {
	token, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate outbox lock token: %w", err)
	}
	return token.String(), nil
}

type OutboxAttemptResultKind string

const (
	OutboxAttemptKnownSuccess OutboxAttemptResultKind = "known_success"
	OutboxAttemptKnownFailure OutboxAttemptResultKind = "known_failure_no_side_effect"
	OutboxAttemptUncertain    OutboxAttemptResultKind = "uncertain"
)

type OutboxAttemptResult struct {
	Kind              OutboxAttemptResultKind
	CompletedAt       time.Time
	Err               error
	notStarted        bool
	effectiveDeadline time.Time
}

type outboxTimeoutError interface {
	Timeout() bool
}

func OutboxKnownSuccess(completedAt time.Time) OutboxAttemptResult {
	return OutboxAttemptResult{
		Kind:        OutboxAttemptKnownSuccess,
		CompletedAt: completedAt.UTC(),
	}
}

func OutboxKnownFailure(err error) OutboxAttemptResult {
	return OutboxAttemptResult{
		Kind: OutboxAttemptKnownFailure,
		Err:  err,
	}
}

func OutboxUncertain(err error) OutboxAttemptResult {
	return OutboxAttemptResult{
		Kind: OutboxAttemptUncertain,
		Err:  err,
	}
}

func outboxLateCompletion(
	completedAt time.Time,
	err error,
) OutboxAttemptResult {
	return OutboxAttemptResult{
		Kind:        OutboxAttemptUncertain,
		CompletedAt: completedAt.UTC(),
		Err:         err,
	}
}

func (result OutboxAttemptResult) withEffectiveDeadline(
	deadline time.Time,
) OutboxAttemptResult {
	result.effectiveDeadline = deadline.UTC()
	return result
}

func outboxAttemptNotStarted(err error) OutboxAttemptResult {
	return OutboxAttemptResult{
		Kind:       OutboxAttemptKnownFailure,
		Err:        err,
		notStarted: true,
	}
}

func outboxAttemptErrorIsUncertain(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var timeout outboxTimeoutError
	return errors.As(err, &timeout) && timeout.Timeout()
}

type OutboxFinalizeResult struct {
	Status models.OutboxDeliveryStatus
}

type WebhookOutboxCleanupResult struct {
	Attempted               int
	Expired                 int
	OverlapCleared          int
	LegacySucceededShredded int
	Malformed               int
}

func outboxLockTokenValue(token *string) string {
	if token == nil {
		return ""
	}
	return *token
}

func (claim OutboxClaimRef) validate() error {
	if strings.TrimSpace(claim.DeliveryID) == "" ||
		strings.TrimSpace(claim.WorkerID) == "" ||
		strings.TrimSpace(claim.LockToken) == "" ||
		claim.LockedAt.IsZero() ||
		claim.Attempts <= 0 {
		return ErrOutboxLockLost
	}
	return nil
}

func (s *AgentNativeService) FinalizeOutboxAttempt(
	ctx context.Context,
	claim OutboxClaimRef,
	attempt OutboxAttemptResult,
) (OutboxFinalizeResult, error) {
	result := OutboxFinalizeResult{}
	if err := claim.validate(); err != nil {
		return result, err
	}
	if err := attempt.validate(); err != nil {
		return result, err
	}
	operation, err := requireOutboxWorkerOperation(ctx)
	if err != nil {
		return result, err
	}
	err = runSystemProjectOperation(
		ctx,
		s.db,
		operation.Scope,
		operation.Actor,
		operation.TraceID,
		operation.CorrelationID,
		func(projectCtx context.Context) error {
			return transactionForContext(
				projectCtx,
				s.db,
				func(tx *gorm.DB) error {
					if err := lockWebhookLifecycleProject(
						tx,
						operation.Scope,
					); err != nil {
						return err
					}
					anchor, err := loadOutboxDeliveryLockAnchor(
						tx,
						operation.Scope,
						claim.DeliveryID,
					)
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return ErrOutboxLockLost
					}
					if err != nil {
						return err
					}
					if anchor.DestinationType == "webhook" {
						if _, err := lockWebhookConfigForDestination(
							tx,
							operation.Scope,
							anchor.DestinationID,
						); err != nil {
							return err
						}
					}
					delivery, err := lockClaimedOutboxDelivery(
						tx,
						operation.Scope,
						claim,
					)
					if err != nil {
						return err
					}
					now := s.now().UTC()
					if attempt.notStarted &&
						(delivery.ExpiresAt == nil ||
							now.Before(delivery.ExpiresAt.UTC())) {
						status, err :=
							releaseUnstartedOutboxClaim(
								tx,
								operation.Scope,
								delivery,
								claim,
								now,
							)
						result.Status = status
						return err
					}
					if delivery.DestinationType != "webhook" {
						status, err := finalizeNonWebhookOutboxAttempt(
							tx,
							operation.Scope,
							delivery,
							claim,
							attempt,
							now,
						)
						result.Status = status
						return err
					}
					snapshot, err := lockWebhookSnapshotForDelivery(
						tx,
						delivery,
					)
					if err != nil {
						return err
					}
					status, err := finalizeWebhookOutboxAttempt(
						tx,
						operation.Scope,
						delivery,
						snapshot,
						claim,
						attempt,
						now,
					)
					result.Status = status
					return err
				},
			)
		},
	)
	if err != nil {
		return OutboxFinalizeResult{}, err
	}
	return result, nil
}

func (attempt OutboxAttemptResult) validate() error {
	if attempt.notStarted &&
		(attempt.Kind != OutboxAttemptKnownFailure ||
			attempt.Err == nil ||
			!attempt.CompletedAt.IsZero()) {
		return ErrWebhookOutboxLifecycleInvariant
	}
	switch attempt.Kind {
	case OutboxAttemptKnownSuccess:
		if attempt.Err != nil {
			return ErrWebhookOutboxLifecycleInvariant
		}
	case OutboxAttemptKnownFailure, OutboxAttemptUncertain:
	default:
		return ErrWebhookOutboxLifecycleInvariant
	}
	return nil
}

func releaseUnstartedOutboxClaim(
	tx *gorm.DB,
	scope models.ProjectScope,
	delivery *models.OutboxDelivery,
	claim OutboxClaimRef,
	now time.Time,
) (models.OutboxDeliveryStatus, error) {
	if delivery == nil {
		return "", ErrWebhookOutboxLifecycleInvariant
	}
	if err := updateClaimedOutboxDelivery(
		tx,
		scope,
		claim,
		map[string]any{
			"status":          models.OutboxDeliveryFailed,
			"attempts":        gorm.Expr("attempts - 1"),
			"next_attempt_at": now,
			"locked_at":       nil,
			"locked_by":       "",
			"lock_token":      nil,
			"last_error":      "outbox delivery attempt did not start",
			"updated_at":      now,
		},
	); err != nil {
		return "", err
	}
	return models.OutboxDeliveryFailed, nil
}

func lockWebhookLifecycleProject(
	tx *gorm.DB,
	scope models.ProjectScope,
) error {
	var project models.Project
	err := tx.
		Select("id", "organization_id", "status").
		Clauses(clause.Locking{Strength: "SHARE"}).
		Where(
			"id = ? AND organization_id = ?",
			scope.ProjectID,
			scope.OrganizationID,
		).
		Take(&project).Error
	if err != nil {
		return fmt.Errorf("lock webhook lifecycle project: %w", err)
	}
	if project.Status != models.ProjectStatusActive &&
		project.Status != models.ProjectStatusArchived {
		return ErrProjectInactive
	}
	return nil
}

func lockClaimedOutboxDelivery(
	tx *gorm.DB,
	scope models.ProjectScope,
	claim OutboxClaimRef,
) (*models.OutboxDelivery, error) {
	var delivery models.OutboxDelivery
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? "+
				"AND status = ? AND locked_by = ? "+
				"AND lock_token = ? AND locked_at = ? AND attempts = ?",
			claim.DeliveryID,
			scope.OrganizationID,
			scope.ProjectID,
			models.OutboxDeliveryProcessing,
			claim.WorkerID,
			claim.LockToken,
			claim.LockedAt,
			claim.Attempts,
		).
		Take(&delivery).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrOutboxLockLost
	}
	if err != nil {
		return nil, fmt.Errorf("lock claimed outbox delivery: %w", err)
	}
	return &delivery, nil
}

func lockWebhookSnapshotForDelivery(
	tx *gorm.DB,
	delivery *models.OutboxDelivery,
) (*models.WebhookDeliverySnapshot, error) {
	if delivery == nil ||
		delivery.DestinationType != "webhook" ||
		delivery.ExpiresAt == nil {
		return nil, ErrWebhookOutboxLifecycleInvariant
	}
	snapshotID, err := models.ParseWebhookDeliverySnapshotDestinationID(
		delivery.DestinationID,
	)
	if err != nil {
		return nil, ErrWebhookOutboxLifecycleInvariant
	}
	var snapshot models.WebhookDeliverySnapshot
	err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			snapshotID,
			delivery.OrganizationID,
			delivery.ProjectID,
		).
		Take(&snapshot).Error
	if err != nil {
		return nil, ErrWebhookOutboxLifecycleInvariant
	}
	if snapshot.EventID != delivery.EventID ||
		!snapshot.CredentialExpiresAt.Equal(delivery.ExpiresAt.UTC()) {
		return nil, ErrWebhookOutboxLifecycleInvariant
	}
	var eventCount int64
	if err := tx.Model(&models.DomainEvent{}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			delivery.EventID,
			delivery.OrganizationID,
			delivery.ProjectID,
		).
		Count(&eventCount).Error; err != nil {
		return nil, fmt.Errorf(
			"validate webhook lifecycle event scope: %w",
			err,
		)
	}
	if eventCount != 1 {
		return nil, ErrWebhookOutboxLifecycleInvariant
	}
	if err := snapshot.ValidateCredentialState(); err != nil {
		return nil, ErrWebhookOutboxLifecycleInvariant
	}
	return &snapshot, nil
}

func finalizeWebhookOutboxAttempt(
	tx *gorm.DB,
	scope models.ProjectScope,
	delivery *models.OutboxDelivery,
	snapshot *models.WebhookDeliverySnapshot,
	claim OutboxClaimRef,
	attempt OutboxAttemptResult,
	now time.Time,
) (models.OutboxDeliveryStatus, error) {
	expiresAt := delivery.ExpiresAt.UTC()
	effectiveDeadline := claim.EffectiveDeadline.UTC()
	if !attempt.effectiveDeadline.IsZero() &&
		(effectiveDeadline.IsZero() ||
			attempt.effectiveDeadline.Before(effectiveDeadline)) {
		effectiveDeadline = attempt.effectiveDeadline.UTC()
	}
	if attempt.Kind == OutboxAttemptKnownSuccess &&
		!attempt.CompletedAt.IsZero() &&
		!attempt.CompletedAt.UTC().Before(claim.LockedAt.UTC()) &&
		!effectiveDeadline.IsZero() &&
		!attempt.CompletedAt.UTC().After(
			effectiveDeadline,
		) &&
		!attempt.CompletedAt.UTC().After(expiresAt) &&
		snapshot.CredentialShreddedAt == nil {
		if err := updateClaimedOutboxDelivery(
			tx,
			scope,
			claim,
			map[string]any{
				"status":       models.OutboxDeliverySucceeded,
				"delivered_at": attempt.CompletedAt.UTC(),
				"expired_at":   nil,
				"locked_at":    nil,
				"locked_by":    "",
				"lock_token":   nil,
				"last_error":   "",
				"updated_at":   now,
			},
		); err != nil {
			return "", err
		}
		if err := shredWebhookSnapshot(
			tx,
			snapshot,
			models.WebhookCredentialShredReasonSucceeded,
			now,
		); err != nil {
			return "", err
		}
		if err := publishOutboxEventIfComplete(
			tx,
			scope,
			delivery.EventID,
			now,
		); err != nil {
			return "", err
		}
		return models.OutboxDeliverySucceeded, nil
	}

	shouldExpire := attempt.Kind == OutboxAttemptUncertain ||
		attempt.Kind == OutboxAttemptKnownSuccess ||
		!now.Before(expiresAt) ||
		snapshot.CredentialShreddedAt != nil
	if !shouldExpire && delivery.Attempts >= delivery.MaxAttempts {
		if err := updateClaimedOutboxDelivery(
			tx,
			scope,
			claim,
			map[string]any{
				"status":     models.OutboxDeliveryDead,
				"locked_at":  nil,
				"locked_by":  "",
				"lock_token": nil,
				"last_error": scrubOutboxFailure(attempt.Err),
				"updated_at": now,
			},
		); err != nil {
			return "", err
		}
		return models.OutboxDeliveryDead, nil
	}
	if !shouldExpire {
		backoff := outboxRetryBackoff(delivery.Attempts)
		if !now.Add(backoff).Before(expiresAt) {
			shouldExpire = true
		} else {
			if err := updateClaimedOutboxDelivery(
				tx,
				scope,
				claim,
				map[string]any{
					"status":          models.OutboxDeliveryFailed,
					"next_attempt_at": now.Add(backoff),
					"locked_at":       nil,
					"locked_by":       "",
					"lock_token":      nil,
					"last_error":      scrubOutboxFailure(attempt.Err),
					"updated_at":      now,
				},
			); err != nil {
				return "", err
			}
			return models.OutboxDeliveryFailed, nil
		}
	}

	lastError := "webhook delivery result is uncertain"
	if attempt.Kind == OutboxAttemptKnownFailure {
		lastError = scrubOutboxFailure(attempt.Err)
	}
	if attempt.Kind == OutboxAttemptKnownSuccess {
		lastError = "webhook delivery completion was not timely"
	}
	if err := updateClaimedOutboxDelivery(
		tx,
		scope,
		claim,
		map[string]any{
			"status":       models.OutboxDeliveryExpired,
			"expired_at":   now,
			"delivered_at": nil,
			"locked_at":    nil,
			"locked_by":    "",
			"lock_token":   nil,
			"last_error":   lastError,
			"updated_at":   now,
		},
	); err != nil {
		return "", err
	}
	if snapshot.CredentialShreddedAt == nil {
		if err := shredWebhookSnapshot(
			tx,
			snapshot,
			models.WebhookCredentialShredReasonExpired,
			now,
		); err != nil {
			return "", err
		}
	}
	if err := preserveOutboxEventPublication(
		tx,
		scope,
		delivery.EventID,
	); err != nil {
		return "", err
	}
	return models.OutboxDeliveryExpired, nil
}

func finalizeNonWebhookOutboxAttempt(
	tx *gorm.DB,
	scope models.ProjectScope,
	delivery *models.OutboxDelivery,
	claim OutboxClaimRef,
	attempt OutboxAttemptResult,
	now time.Time,
) (models.OutboxDeliveryStatus, error) {
	if attempt.Kind == OutboxAttemptKnownSuccess {
		completedAt := attempt.CompletedAt.UTC()
		if completedAt.IsZero() {
			completedAt = now
		}
		if err := updateClaimedOutboxDelivery(
			tx,
			scope,
			claim,
			map[string]any{
				"status":       models.OutboxDeliverySucceeded,
				"delivered_at": completedAt,
				"locked_at":    nil,
				"locked_by":    "",
				"lock_token":   nil,
				"last_error":   "",
				"updated_at":   now,
			},
		); err != nil {
			return "", err
		}
		if err := publishOutboxEventIfComplete(
			tx,
			scope,
			delivery.EventID,
			now,
		); err != nil {
			return "", err
		}
		return models.OutboxDeliverySucceeded, nil
	}
	status := models.OutboxDeliveryFailed
	if delivery.Attempts >= delivery.MaxAttempts {
		status = models.OutboxDeliveryDead
	}
	if err := updateClaimedOutboxDelivery(
		tx,
		scope,
		claim,
		map[string]any{
			"status":          status,
			"next_attempt_at": now.Add(outboxRetryBackoff(delivery.Attempts)),
			"locked_at":       nil,
			"locked_by":       "",
			"lock_token":      nil,
			"last_error":      scrubOutboxFailure(attempt.Err),
			"updated_at":      now,
		},
	); err != nil {
		return "", err
	}
	return status, nil
}

func updateClaimedOutboxDelivery(
	tx *gorm.DB,
	scope models.ProjectScope,
	claim OutboxClaimRef,
	updates map[string]any,
) error {
	result := tx.Model(&models.OutboxDelivery{}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? "+
				"AND status = ? AND locked_by = ? "+
				"AND lock_token = ? AND locked_at = ? AND attempts = ?",
			claim.DeliveryID,
			scope.OrganizationID,
			scope.ProjectID,
			models.OutboxDeliveryProcessing,
			claim.WorkerID,
			claim.LockToken,
			claim.LockedAt,
			claim.Attempts,
		).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("finalize claimed outbox delivery: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrOutboxLockLost
	}
	return nil
}

func shredWebhookSnapshot(
	tx *gorm.DB,
	snapshot *models.WebhookDeliverySnapshot,
	reason models.WebhookCredentialShredReason,
	now time.Time,
) error {
	if snapshot == nil ||
		!reason.IsValid() ||
		snapshot.CredentialShreddedAt != nil {
		return ErrWebhookOutboxLifecycleInvariant
	}
	result := tx.Table((models.WebhookDeliverySnapshot{}).TableName()).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? "+
				"AND event_id = ? AND credential_expires_at = ? "+
				"AND credential_shredded_at IS NULL "+
				"AND credential_shred_reason IS NULL",
			snapshot.ID,
			snapshot.OrganizationID,
			snapshot.ProjectID,
			snapshot.EventID,
			snapshot.CredentialExpiresAt.UTC(),
		).
		Updates(map[string]any{
			"secret":                     "",
			"previous_secret":            "",
			"previous_secret_expires_at": nil,
			"access_token":               "",
			"credential_shredded_at":     now,
			"credential_shred_reason":    reason,
		})
	if result.Error != nil {
		return fmt.Errorf("shred webhook delivery credentials: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrWebhookOutboxLifecycleInvariant
	}
	return nil
}

func publishOutboxEventIfComplete(
	tx *gorm.DB,
	scope models.ProjectScope,
	eventID string,
	now time.Time,
) error {
	if err := lockOutboxLifecycleEvent(tx, scope, eventID); err != nil {
		return err
	}
	var remaining int64
	if err := tx.Model(&models.OutboxDelivery{}).
		Where(
			"event_id = ? AND organization_id = ? AND project_id = ? "+
				"AND status <> ?",
			eventID,
			scope.OrganizationID,
			scope.ProjectID,
			models.OutboxDeliverySucceeded,
		).
		Count(&remaining).Error; err != nil {
		return fmt.Errorf("count incomplete outbox deliveries: %w", err)
	}
	if remaining != 0 {
		return nil
	}
	result := tx.Model(&models.DomainEvent{}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			eventID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		Update("published_at", now)
	if result.Error != nil {
		return fmt.Errorf("publish completed outbox event: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrWebhookOutboxLifecycleInvariant
	}
	return nil
}

func clearOutboxEventPublication(
	tx *gorm.DB,
	scope models.ProjectScope,
	eventID string,
) error {
	if err := lockOutboxLifecycleEvent(tx, scope, eventID); err != nil {
		return err
	}
	result := tx.Model(&models.DomainEvent{}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			eventID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		Update("published_at", nil)
	if result.Error != nil {
		return fmt.Errorf("clear expired outbox event publication: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrWebhookOutboxLifecycleInvariant
	}
	return nil
}

// preserveOutboxEventPublication serializes an expiration transition with
// publication without rewriting immutable event history. An expiration never
// publishes an event, and a destination that published it earlier remains a
// durable historical fact.
func preserveOutboxEventPublication(
	tx *gorm.DB,
	scope models.ProjectScope,
	eventID string,
) error {
	return lockOutboxLifecycleEvent(tx, scope, eventID)
}

func lockOutboxLifecycleEvent(
	tx *gorm.DB,
	scope models.ProjectScope,
	eventID string,
) error {
	var event models.DomainEvent
	err := tx.
		Select("id").
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			eventID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		Take(&event).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrWebhookOutboxLifecycleInvariant
	}
	if err != nil {
		return fmt.Errorf("lock outbox lifecycle event: %w", err)
	}
	return nil
}

func outboxRetryBackoff(attempts int) time.Duration {
	backoff := time.Second * time.Duration(1<<minInt(attempts, 10))
	if backoff > time.Hour {
		return time.Hour
	}
	return backoff
}
