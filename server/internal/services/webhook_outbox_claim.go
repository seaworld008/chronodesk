package services

import (
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

type outboxClaimScanCursor struct {
	sortAt    time.Time
	createdAt time.Time
	stableID  string
}

type outboxClaimScanCursorMutation struct {
	class    string
	expected outboxClaimScanCursor
	next     outboxClaimScanCursor
}

func (cursor outboxClaimScanCursor) isZero() bool {
	return cursor.sortAt.IsZero() || cursor.stableID == ""
}

func applyOutboxClaimScanCursor(
	query *gorm.DB,
	cursor outboxClaimScanCursor,
	sortColumn string,
) *gorm.DB {
	if cursor.isZero() {
		return query
	}
	switch sortColumn {
	case "next_attempt_at", "locked_at":
	default:
		return query.Where("1 = 0")
	}
	return query.Where(
		"("+sortColumn+", created_at, id) > (?, ?, ?)",
		cursor.sortAt,
		cursor.createdAt,
		cursor.stableID,
	)
}

func applyOutboxBatchCreatedBefore(
	query *gorm.DB,
	batchCreatedBefore time.Time,
) *gorm.DB {
	if batchCreatedBefore.IsZero() {
		return query.Where("1 = 0")
	}
	return query.Where("created_at < ?", batchCreatedBefore.UTC())
}

func applyOutboxClaimEligibility(
	query *gorm.DB,
	now time.Time,
	lockCutoff time.Time,
) *gorm.DB {
	query = query.
		Where(
			"((status IN ? AND next_attempt_at <= ?) OR "+
				"(status = ? AND locked_at IS NOT NULL "+
				"AND TRIM(locked_by) <> '' "+
				"AND TRIM(lock_token) <> '' AND locked_at < ? "+
				"AND (destination_type <> 'webhook' "+
				"OR (attempts < max_attempts "+
				"AND dispatch_started_at = locked_at))))",
			[]models.OutboxDeliveryStatus{
				models.OutboxDeliveryPending,
				models.OutboxDeliveryFailed,
			},
			now,
			models.OutboxDeliveryProcessing,
			lockCutoff,
		)
	return applyOutboxClaimDestinationEligibility(query, now)
}

func applyOutboxClaimDestinationEligibility(
	query *gorm.DB,
	now time.Time,
) *gorm.DB {
	return query.Where(
		`(
				destination_type <> ?
				OR (
					destination_type = ?
					AND expires_at IS NOT NULL
					AND expires_at > ?
					AND SUBSTR(destination_id, 1, ?) = ?
					AND LENGTH(destination_id) = ?
					AND EXISTS (
						SELECT 1
						FROM webhook_delivery_snapshots AS lifecycle_snapshot
						WHERE lifecycle_snapshot.id =
						      SUBSTR(outbox_deliveries.destination_id, ?)
						  AND lifecycle_snapshot.organization_id =
						      outbox_deliveries.organization_id
						  AND lifecycle_snapshot.project_id =
						      outbox_deliveries.project_id
						  AND lifecycle_snapshot.event_id =
						      outbox_deliveries.event_id
						  AND lifecycle_snapshot.credential_expires_at =
						      outbox_deliveries.expires_at
						  AND lifecycle_snapshot.credential_shredded_at IS NULL
						LIMIT 1 OFFSET 0
					)
				)
			)`,
		"webhook",
		"webhook",
		now,
		len(models.WebhookDeliverySnapshotDestinationPrefix),
		models.WebhookDeliverySnapshotDestinationPrefix,
		len(models.WebhookDeliverySnapshotDestinationPrefix)+36,
		len(models.WebhookDeliverySnapshotDestinationPrefix)+1,
	)
}

func applyWebhookOutboxClaimDestinationEligibility(
	query *gorm.DB,
	now time.Time,
) *gorm.DB {
	return query.Where(
		`destination_type = 'webhook'
		 AND expires_at IS NOT NULL
		 AND expires_at > ?
		 AND SUBSTR(destination_id, 1, ?) = ?
		 AND LENGTH(destination_id) = ?
		 AND EXISTS (
			SELECT 1
			FROM webhook_delivery_snapshots AS lifecycle_snapshot
			WHERE lifecycle_snapshot.id =
			      SUBSTR(outbox_deliveries.destination_id, ?)
			  AND lifecycle_snapshot.organization_id =
			      outbox_deliveries.organization_id
			  AND lifecycle_snapshot.project_id =
			      outbox_deliveries.project_id
			  AND lifecycle_snapshot.event_id =
			      outbox_deliveries.event_id
			  AND lifecycle_snapshot.credential_expires_at =
			      outbox_deliveries.expires_at
			  AND lifecycle_snapshot.credential_shredded_at IS NULL
			LIMIT 1 OFFSET 0
		 )`,
		now,
		len(models.WebhookDeliverySnapshotDestinationPrefix),
		models.WebhookDeliverySnapshotDestinationPrefix,
		len(models.WebhookDeliverySnapshotDestinationPrefix)+36,
		len(models.WebhookDeliverySnapshotDestinationPrefix)+1,
	)
}

func buildOutboxWebhookRetryClaimCandidateQuery(
	query *gorm.DB,
	scope models.ProjectScope,
	status models.OutboxDeliveryStatus,
	now time.Time,
	batchCreatedBefore time.Time,
	cursor outboxClaimScanCursor,
	limit int,
) *gorm.DB {
	query = applyOutboxBatchCreatedBefore(query, batchCreatedBefore)
	statusClause := "1 = 0"
	switch status {
	case models.OutboxDeliveryPending:
		statusClause = "status = 'pending'"
	case models.OutboxDeliveryFailed:
		statusClause = "status = 'failed'"
	}
	query = query.Where(
		"organization_id = ? AND project_id = ? "+
			"AND destination_type = 'webhook' "+
			"AND "+statusClause+" "+
			"AND expires_at IS NOT NULL "+
			"AND next_attempt_at <= ?",
		scope.OrganizationID,
		scope.ProjectID,
		now,
	)
	query = applyOutboxClaimScanCursor(
		query,
		cursor,
		"next_attempt_at",
	)
	return query.
		Order("next_attempt_at ASC, created_at ASC, id ASC").
		Limit(limit)
}

func buildOutboxWebhookStaleClaimCandidateQuery(
	query *gorm.DB,
	scope models.ProjectScope,
	lockCutoff time.Time,
	batchCreatedBefore time.Time,
	cursor outboxClaimScanCursor,
	limit int,
) *gorm.DB {
	query = applyOutboxBatchCreatedBefore(query, batchCreatedBefore)
	query = query.Where(
		"organization_id = ? AND project_id = ? "+
			"AND destination_type = 'webhook' "+
			"AND status = 'processing' AND locked_at IS NOT NULL "+
			"AND dispatch_started_at = locked_at "+
			"AND expires_at IS NOT NULL "+
			"AND locked_at < ?",
		scope.OrganizationID,
		scope.ProjectID,
		lockCutoff,
	)
	query = applyOutboxClaimScanCursor(
		query,
		cursor,
		"locked_at",
	)
	return query.
		Order("locked_at ASC, created_at ASC, id ASC").
		Limit(limit)
}

func buildOutboxNonWebhookRetryClaimCandidateQuery(
	query *gorm.DB,
	scope models.ProjectScope,
	now time.Time,
	batchCreatedBefore time.Time,
	cursor outboxClaimScanCursor,
	limit int,
) *gorm.DB {
	query = applyOutboxBatchCreatedBefore(query, batchCreatedBefore)
	query = query.Where(
		"organization_id = ? AND project_id = ? "+
			"AND destination_type <> 'webhook' "+
			"AND status IN ('pending', 'failed') "+
			"AND next_attempt_at <= ?",
		scope.OrganizationID,
		scope.ProjectID,
		now,
	)
	query = applyOutboxClaimScanCursor(
		query,
		cursor,
		"next_attempt_at",
	)
	return query.
		Order("next_attempt_at ASC, created_at ASC, id ASC").
		Limit(limit)
}

func buildOutboxNonWebhookStaleClaimCandidateQuery(
	query *gorm.DB,
	scope models.ProjectScope,
	lockCutoff time.Time,
	batchCreatedBefore time.Time,
	cursor outboxClaimScanCursor,
	limit int,
) *gorm.DB {
	query = applyOutboxBatchCreatedBefore(query, batchCreatedBefore)
	query = query.Where(
		"organization_id = ? AND project_id = ? "+
			"AND destination_type <> 'webhook' "+
			"AND status = 'processing' "+
			"AND locked_at IS NOT NULL "+
			"AND locked_at < ?",
		scope.OrganizationID,
		scope.ProjectID,
		lockCutoff,
	)
	query = applyOutboxClaimScanCursor(
		query,
		cursor,
		"locked_at",
	)
	return query.
		Order("locked_at ASC, created_at ASC, id ASC").
		Limit(limit)
}

func buildOutboxWebhookRetryEligiblePageQuery(
	query *gorm.DB,
	scope models.ProjectScope,
	status models.OutboxDeliveryStatus,
	now time.Time,
	batchCreatedBefore time.Time,
	ids []string,
) *gorm.DB {
	query = applyOutboxBatchCreatedBefore(query, batchCreatedBefore)
	if len(ids) == 0 {
		return query.Where("1 = 0")
	}
	statusClause := "1 = 0"
	switch status {
	case models.OutboxDeliveryPending:
		statusClause = "status = 'pending'"
	case models.OutboxDeliveryFailed:
		statusClause = "status = 'failed'"
	}
	query = query.Where(
		"organization_id = ? AND project_id = ? "+
			"AND id IN ? "+
			"AND destination_type = 'webhook' "+
			"AND "+statusClause+" "+
			"AND expires_at IS NOT NULL "+
			"AND next_attempt_at <= ?",
		scope.OrganizationID,
		scope.ProjectID,
		ids,
		now,
	)
	return applyWebhookOutboxClaimDestinationEligibility(query, now)
}

func buildOutboxWebhookStaleEligiblePageQuery(
	query *gorm.DB,
	scope models.ProjectScope,
	now time.Time,
	lockCutoff time.Time,
	batchCreatedBefore time.Time,
	ids []string,
) *gorm.DB {
	query = applyOutboxBatchCreatedBefore(query, batchCreatedBefore)
	if len(ids) == 0 {
		return query.Where("1 = 0")
	}
	query = query.Where(
		"organization_id = ? AND project_id = ? "+
			"AND id IN ? "+
			"AND destination_type = 'webhook' "+
			"AND status = 'processing' "+
			"AND expires_at IS NOT NULL "+
			"AND locked_at IS NOT NULL "+
			"AND dispatch_started_at = locked_at "+
			"AND TRIM(locked_by) <> '' "+
			"AND TRIM(lock_token) <> '' "+
			"AND locked_at < ?",
		scope.OrganizationID,
		scope.ProjectID,
		ids,
		lockCutoff,
	)
	return applyWebhookOutboxClaimDestinationEligibility(query, now)
}

func buildOutboxNonWebhookRetryEligiblePageQuery(
	query *gorm.DB,
	scope models.ProjectScope,
	now time.Time,
	batchCreatedBefore time.Time,
	ids []string,
) *gorm.DB {
	query = applyOutboxBatchCreatedBefore(query, batchCreatedBefore)
	if len(ids) == 0 {
		return query.Where("1 = 0")
	}
	return query.Where(
		"organization_id = ? AND project_id = ? "+
			"AND id IN ? "+
			"AND destination_type <> 'webhook' "+
			"AND status IN ('pending', 'failed') "+
			"AND next_attempt_at <= ?",
		scope.OrganizationID,
		scope.ProjectID,
		ids,
		now,
	)
}

func buildOutboxNonWebhookStaleEligiblePageQuery(
	query *gorm.DB,
	scope models.ProjectScope,
	lockCutoff time.Time,
	batchCreatedBefore time.Time,
	ids []string,
) *gorm.DB {
	query = applyOutboxBatchCreatedBefore(query, batchCreatedBefore)
	if len(ids) == 0 {
		return query.Where("1 = 0")
	}
	return query.Where(
		"organization_id = ? AND project_id = ? "+
			"AND id IN ? "+
			"AND destination_type <> 'webhook' "+
			"AND status = 'processing' "+
			"AND locked_at IS NOT NULL "+
			"AND TRIM(locked_by) <> '' "+
			"AND TRIM(lock_token) <> '' "+
			"AND locked_at < ?",
		scope.OrganizationID,
		scope.ProjectID,
		ids,
		lockCutoff,
	)
}

func applyExactOutboxCandidate(
	query *gorm.DB,
	candidate *models.OutboxDelivery,
) *gorm.DB {
	if candidate == nil {
		return query.Where("1 = 0")
	}
	query = query.Where(
		"destination_type = ? AND destination_id = ? "+
			"AND status = ? AND attempts = ? "+
			"AND next_attempt_at = ? AND locked_by = ? "+
			"AND COALESCE(lock_token, '') = ?",
		candidate.DestinationType,
		candidate.DestinationID,
		candidate.Status,
		candidate.Attempts,
		candidate.NextAttemptAt,
		candidate.LockedBy,
		outboxLockTokenValue(candidate.LockToken),
	)
	if candidate.LockedAt == nil {
		query = query.Where("locked_at IS NULL")
	} else {
		query = query.Where("locked_at = ?", *candidate.LockedAt)
	}
	if candidate.DispatchStartedAt == nil {
		query = query.Where("dispatch_started_at IS NULL")
	} else {
		query = query.Where(
			"dispatch_started_at = ?",
			candidate.DispatchStartedAt.UTC(),
		)
	}
	if candidate.ExpiresAt == nil {
		return query.Where("expires_at IS NULL")
	}
	return query.Where("expires_at = ?", *candidate.ExpiresAt)
}

func transitionExhaustedStaleWebhookCandidate(
	tx *gorm.DB,
	scope models.ProjectScope,
	candidate *models.OutboxDelivery,
	now time.Time,
	lockCutoff time.Time,
	batchCreatedBefore time.Time,
) (bool, error) {
	if candidate == nil ||
		candidate.DestinationType != "webhook" ||
		candidate.Status != models.OutboxDeliveryProcessing ||
		!isWebhookDispatchPrepared(
			candidate.DispatchStartedAt,
			candidate.LockedAt,
		) ||
		candidate.MaxAttempts <= 0 ||
		candidate.Attempts < candidate.MaxAttempts ||
		candidate.ExpiresAt == nil ||
		!now.Before(candidate.ExpiresAt.UTC()) {
		return false, nil
	}
	query := tx.Model(&models.OutboxDelivery{}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			candidate.ID,
			scope.OrganizationID,
			scope.ProjectID,
		)
	query = applyExactOutboxCandidate(query, candidate)
	query = applyOutboxBatchCreatedBefore(query, batchCreatedBefore)
	result := query.
		Where(
			"destination_type = 'webhook' "+
				"AND status = 'processing' "+
				"AND attempts >= max_attempts "+
				"AND expires_at > ? "+
				"AND locked_at IS NOT NULL "+
				"AND TRIM(locked_by) <> '' "+
				"AND TRIM(lock_token) <> '' "+
				"AND dispatch_started_at = locked_at "+
				"AND locked_at < ?",
			now,
			lockCutoff,
		).
		Updates(map[string]any{
			"status":              models.OutboxDeliveryDead,
			"delivered_at":        nil,
			"locked_at":           nil,
			"locked_by":           "",
			"lock_token":          nil,
			"dispatch_started_at": nil,
			"last_error":          "webhook delivery attempts exhausted",
			"updated_at":          now,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}
