package services

import (
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

type webhookLegacySucceededScanRow struct {
	ID               string `gorm:"column:id"`
	DestinationID    string `gorm:"column:destination_id"`
	SnapshotShredded bool   `gorm:"column:snapshot_shredded"`
}

func buildWebhookLegacySucceededCandidateQuery(
	query *gorm.DB,
	scope models.ProjectScope,
	cursor webhookOutboxCleanupCursor,
	limit int,
) *gorm.DB {
	query = applyWebhookOutboxDeliveryCleanupCursor(query, cursor)
	return query.
		Select(
			`id,
			 destination_id,
			 (
			  SUBSTR(outbox_deliveries.destination_id, 1, ?) = ?
			  AND LENGTH(outbox_deliveries.destination_id) = ?
			  AND COALESCE((
				SELECT CASE
					WHEN lifecycle_snapshot.credential_shredded_at IS NOT NULL
					  AND lifecycle_snapshot.credential_shred_reason = ?
					  AND lifecycle_snapshot.secret = ''
					  AND lifecycle_snapshot.previous_secret = ''
					  AND lifecycle_snapshot.previous_secret_expires_at IS NULL
					  AND lifecycle_snapshot.access_token = ''
						THEN TRUE
					ELSE FALSE
				END
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
				LIMIT 1
			  ), FALSE)
			 ) AS snapshot_shredded`,
			len(models.WebhookDeliverySnapshotDestinationPrefix),
			models.WebhookDeliverySnapshotDestinationPrefix,
			len(models.WebhookDeliverySnapshotDestinationPrefix)+36,
			models.WebhookCredentialShredReasonSucceeded,
			len(models.WebhookDeliverySnapshotDestinationPrefix)+1,
		).
		Where(
			`organization_id = ? AND project_id = ?
			 AND destination_type = 'webhook'
			 AND status = 'succeeded'`,
			scope.OrganizationID,
			scope.ProjectID,
		).
		Order("destination_id ASC, id ASC").
		Limit(limit)
}

func buildWebhookExpiryCandidateQuery(
	query *gorm.DB,
	scope models.ProjectScope,
	now time.Time,
	_ time.Time,
	cursor webhookOutboxCleanupCursor,
	limit int,
) *gorm.DB {
	if !cursor.isZero() {
		query = query.Where(
			"(status, expires_at, destination_id, id) > (?, ?, ?, ?)",
			cursor.status,
			cursor.sortAt,
			cursor.destinationID,
			cursor.stableID,
		)
	}
	return query.
		Select("id", "destination_id", "status", "expires_at").
		Where(
			"organization_id = ? AND project_id = ? "+
				"AND destination_type = 'webhook' "+
				"AND status IN ('dead', 'failed', 'pending', 'processing') "+
				"AND expires_at IS NOT NULL "+
				"AND expires_at <= ?",
			scope.OrganizationID,
			scope.ProjectID,
			now,
		).
		Order(
			"status ASC, expires_at ASC, destination_id ASC, id ASC",
		).
		Limit(limit)
}

func buildWebhookExpiryEligiblePageQuery(
	query *gorm.DB,
	scope models.ProjectScope,
	now time.Time,
	lockCutoff time.Time,
	ids []string,
) *gorm.DB {
	if len(ids) == 0 {
		return query.Where("1 = 0")
	}
	return query.Where(
		"organization_id = ? AND project_id = ? "+
			"AND id IN ? "+
			"AND destination_type = 'webhook' "+
			"AND status IN ('dead', 'failed', 'pending', 'processing') "+
			"AND expires_at IS NOT NULL "+
			"AND expires_at <= ? "+
			"AND (status <> 'processing' OR ("+
			"locked_at IS NULL OR "+
			"TRIM(locked_by) = '' OR "+
			"locked_at < ?))",
		scope.OrganizationID,
		scope.ProjectID,
		ids,
		now,
		lockCutoff,
	)
}

func buildWebhookOverlapCandidateQuery(
	query *gorm.DB,
	scope models.ProjectScope,
	now time.Time,
	cursor webhookOutboxCleanupCursor,
	limit int,
) *gorm.DB {
	if !cursor.isZero() {
		query = query.Where(
			"(previous_secret_expires_at, id) > (?, ?)",
			cursor.sortAt,
			cursor.stableID,
		)
	}
	return query.
		Select("id", "previous_secret_expires_at").
		Where(
			"organization_id = ? AND project_id = ? "+
				"AND credential_shredded_at IS NULL "+
				"AND previous_secret_expires_at IS NOT NULL "+
				"AND previous_secret_expires_at <= ?",
			scope.OrganizationID,
			scope.ProjectID,
			now,
		).
		Order("previous_secret_expires_at ASC, id ASC").
		Limit(limit)
}

func applyWebhookOutboxDeliveryCleanupCursor(
	query *gorm.DB,
	cursor webhookOutboxCleanupCursor,
) *gorm.DB {
	if cursor.isZero() {
		return query
	}
	return query.Where(
		"(destination_id, id) > (?, ?)",
		cursor.destinationID,
		cursor.stableID,
	)
}
