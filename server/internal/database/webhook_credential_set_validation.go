package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
)

const webhookRuntimeProjectPageSize = 256

type webhookCredentialViolation struct {
	Code      string `gorm:"column:violation_code"`
	ObjectID  string `gorm:"column:object_id"`
	RelatedID string `gorm:"column:related_id"`
}

func validateWebhookCredentialRuntimeSnapshot(
	ctx context.Context,
	db *gorm.DB,
) error {
	if ctx == nil {
		return errors.New(
			"webhook credential runtime validation context is required",
		)
	}
	if db == nil {
		return errors.New("webhook credential lifetime database is required")
	}
	if db.Dialector.Name() == "sqlite" {
		return withPinnedGORMConnection(
			ctx,
			db,
			func(pinned *gorm.DB) error {
				return validateWebhookCredentialRuntimeSnapshotPinned(
					ctx,
					pinned,
				)
			},
		)
	}
	return validateWebhookCredentialRuntimeSnapshotPinned(ctx, db)
}

func validateWebhookCredentialRuntimeSnapshotPinned(
	ctx context.Context,
	db *gorm.DB,
) error {
	run := func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "sqlite" {
			enabled, err := sqliteForeignKeysEnabled(tx)
			if err != nil {
				return err
			}
			if !enabled {
				return errors.New(
					"SQLite foreign_keys runtime contract is disabled",
				)
			}
		}
		var lastOrganizationID uint
		var lastProjectID uint
		for {
			var projects []models.Project
			if err := tx.WithContext(ctx).
				Select("id", "organization_id", "status").
				Where(
					"organization_id > ? OR "+
						"(organization_id = ? AND id > ?)",
					lastOrganizationID,
					lastOrganizationID,
					lastProjectID,
				).
				Order("organization_id ASC, id ASC").
				Limit(webhookRuntimeProjectPageSize).
				Find(&projects).Error; err != nil {
				return fmt.Errorf(
					"list trusted projects for webhook credential validation: %w",
					err,
				)
			}
			if len(projects) == 0 {
				return nil
			}
			for index := range projects {
				project := projects[index]
				if !project.Status.IsValid() {
					return fmt.Errorf(
						"Project %d has unsupported status %q",
						project.ID,
						project.Status,
					)
				}
				scope := project.Scope()
				if err := scope.Validate(); err != nil {
					return fmt.Errorf(
						"invalid trusted project for webhook credential validation: %w",
						err,
					)
				}
				if err := scopeddb.ConfigureProjectScopeTransaction(
					tx,
					scope,
				); err != nil {
					return fmt.Errorf(
						"configure webhook credential validation scope %d: %w",
						scope.ProjectID,
						err,
					)
				}
				if err := validateWebhookCredentialScopeSet(
					tx.WithContext(ctx),
					scope,
					true,
				); err != nil {
					return fmt.Errorf(
						"validate webhook credentials for project %d: %w",
						scope.ProjectID,
						err,
					)
				}
			}
			last := projects[len(projects)-1]
			lastOrganizationID = last.OrganizationID
			lastProjectID = last.ID
		}
	}
	if db.Dialector.Name() == "postgres" {
		return db.WithContext(ctx).Transaction(
			run,
			&sql.TxOptions{
				Isolation: sql.LevelRepeatableRead,
				ReadOnly:  true,
			},
		)
	}
	return db.WithContext(ctx).Transaction(run)
}

func validateWebhookCredentialScopeSet(
	db *gorm.DB,
	scope models.ProjectScope,
	requireDeadlines bool,
) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	return validateWebhookCredentialSet(
		db,
		&scope,
		requireDeadlines,
		false,
	)
}

func validateWebhookCredentialOwnerSet(
	db *gorm.DB,
	requireDeadlines bool,
) error {
	return validateWebhookCredentialSet(
		db,
		nil,
		requireDeadlines,
		false,
	)
}

func validateWebhookCredentialSet(
	db *gorm.DB,
	scope *models.ProjectScope,
	requireDeadlines bool,
	includeAllOutboxShapes bool,
) error {
	if db == nil {
		return errors.New("webhook credential validation database is required")
	}
	snapshotScope := "1 = 1"
	deliveryScope := "1 = 1"
	eventScope := "1 = 1"
	args := make([]any, 0, 2)
	if scope != nil {
		snapshotScope = "organization_id = @organization_id " +
			"AND project_id = @project_id"
		deliveryScope = snapshotScope
		eventScope = snapshotScope
		args = append(
			args,
			sql.Named("organization_id", scope.OrganizationID),
			sql.Named("project_id", scope.ProjectID),
		)
	}
	snapshotIDValid := webhookCredentialUUIDShapeSQL(
		db.Dialector.Name(),
		"id",
		true,
	)
	deliveryIDValid := webhookCredentialUUIDShapeSQL(
		db.Dialector.Name(),
		"id",
		false,
	)
	eventIDValid := webhookCredentialUUIDShapeSQL(
		db.Dialector.Name(),
		"event_id",
		false,
	)
	statusValues := closedVocabularySQLList(
		models.OutboxDeliveryStatusValues(),
	)
	reasonValues := closedVocabularySQLList(
		models.WebhookCredentialShredReasonValues(),
	)
	requireSnapshotDeadline := "FALSE"
	requireDeliveryDeadline := "FALSE"
	if requireDeadlines {
		requireSnapshotDeadline = "credential_expires_at IS NULL"
		requireDeliveryDeadline = "expires_at IS NULL"
	}
	deliveryShapeSource := "scoped_webhooks"
	if includeAllOutboxShapes {
		deliveryShapeSource = "scoped_deliveries"
	}
	query := fmt.Sprintf(`
		WITH
		scoped_snapshots AS (
			SELECT *
			FROM webhook_delivery_snapshots
			WHERE %s
		),
		scoped_deliveries AS (
			SELECT *
			FROM outbox_deliveries
			WHERE %s
		),
		scoped_webhooks AS (
			SELECT *
			FROM scoped_deliveries
			WHERE destination_type = 'webhook'
		),
		scoped_events AS (
			SELECT id, organization_id, project_id
			FROM domain_events
			WHERE %s
		),
		violations AS (
			SELECT
				10 AS priority,
				'snapshot_shape' AS violation_code,
				CAST(id AS TEXT) AS object_id,
				CAST(event_id AS TEXT) AS related_id
			FROM scoped_snapshots
			WHERE NOT (%s)
			   OR id IS NULL
			   OR organization_id = 0
			   OR organization_id IS NULL
			   OR project_id = 0
			   OR project_id IS NULL
			   OR event_id IS NULL
			   OR event_id = ''
			   OR NOT (%s)
			   OR %s
			   OR (
					(credential_shredded_at IS NULL) <>
					(credential_shred_reason IS NULL)
			   )
			   OR (
					credential_shred_reason IS NOT NULL
					AND credential_shred_reason NOT IN (%s)
			   )
			   OR (
					credential_shredded_at IS NOT NULL
					AND (
						secret IS NULL OR secret <> ''
						OR previous_secret IS NULL
						OR previous_secret <> ''
						OR access_token IS NULL
						OR access_token <> ''
					)
			   )
			UNION ALL
			SELECT
				20,
				'delivery_shape',
				CAST(id AS TEXT),
				CAST(event_id AS TEXT)
			FROM %s
			WHERE NOT (%s)
			   OR id IS NULL
			   OR organization_id = 0
			   OR organization_id IS NULL
			   OR project_id = 0
			   OR project_id IS NULL
			   OR event_id IS NULL
			   OR event_id = ''
			   OR NOT (%s)
			   OR destination_type IS NULL
			   OR destination_id IS NULL
			   OR status IS NULL
			   OR status NOT IN (%s)
			   OR ((status = 'expired') <> (expired_at IS NOT NULL))
			   OR (
					status = 'expired'
					AND destination_type <> 'webhook'
			   )
			   OR (
					destination_type = 'webhook'
					AND %s
			   )
			UNION ALL
			SELECT
				30,
				'snapshot_event_missing_or_mismatched',
				CAST(snapshot.id AS TEXT),
				CAST(snapshot.event_id AS TEXT)
			FROM scoped_snapshots AS snapshot
			LEFT JOIN scoped_events AS event
			  ON event.id = snapshot.event_id
			 AND event.organization_id = snapshot.organization_id
			 AND event.project_id = snapshot.project_id
			WHERE event.id IS NULL
			UNION ALL
			SELECT
				40,
				'delivery_event_missing_or_mismatched',
				CAST(delivery.id AS TEXT),
				CAST(delivery.event_id AS TEXT)
			FROM scoped_webhooks AS delivery
			LEFT JOIN scoped_events AS event
			  ON event.id = delivery.event_id
			 AND event.organization_id = delivery.organization_id
			 AND event.project_id = delivery.project_id
			WHERE event.id IS NULL
			UNION ALL
			SELECT
				50,
				'delivery_snapshot_missing_or_mismatched',
				CAST(delivery.id AS TEXT),
				CAST(delivery.destination_id AS TEXT)
			FROM scoped_webhooks AS delivery
			LEFT JOIN scoped_snapshots AS snapshot
			  ON delivery.destination_id = 'snapshot:' || snapshot.id
			 AND delivery.organization_id = snapshot.organization_id
			 AND delivery.project_id = snapshot.project_id
			WHERE snapshot.id IS NULL
			   OR delivery.event_id <> snapshot.event_id
			   OR (
					%s
					AND (
						delivery.expires_at IS NULL
						OR snapshot.credential_expires_at IS NULL
						OR delivery.expires_at <>
							snapshot.credential_expires_at
					)
			   )
			UNION ALL
			SELECT
				60,
				'snapshot_delivery_count',
				CAST(snapshot.id AS TEXT),
				CAST(COUNT(delivery.id) AS TEXT)
			FROM scoped_snapshots AS snapshot
			LEFT JOIN scoped_webhooks AS delivery
			  ON delivery.destination_id = 'snapshot:' || snapshot.id
			 AND delivery.organization_id = snapshot.organization_id
			 AND delivery.project_id = snapshot.project_id
			GROUP BY snapshot.id
			HAVING COUNT(delivery.id) <> 1
		)
		SELECT violation_code, object_id, related_id
		FROM violations
		ORDER BY priority, object_id, related_id
		LIMIT 1
	`,
		snapshotScope,
		deliveryScope,
		eventScope,
		snapshotIDValid,
		eventIDValid,
		requireSnapshotDeadline,
		reasonValues,
		deliveryShapeSource,
		deliveryIDValid,
		eventIDValid,
		statusValues,
		requireDeliveryDeadline,
		sqlBooleanLiteral(requireDeadlines),
	)
	var violation webhookCredentialViolation
	if err := db.Raw(query, args...).Scan(&violation).Error; err != nil {
		return fmt.Errorf(
			"run set-based webhook credential validation: %w",
			err,
		)
	}
	if violation.Code != "" {
		return webhookCredentialViolationError(violation)
	}
	return nil
}

func webhookCredentialViolationError(
	violation webhookCredentialViolation,
) error {
	description := strings.ReplaceAll(violation.Code, "_", " ")
	switch violation.Code {
	case "snapshot_shape":
		description = "snapshot shape, shred, or deadline"
	case "delivery_shape":
		description = "delivery malformed status or deadline"
	case "snapshot_event_missing_or_mismatched":
		description = "snapshot scope or event missing or mismatched"
	case "delivery_event_missing_or_mismatched":
		description = "delivery scope or event missing or mismatched"
	case "delivery_snapshot_missing_or_mismatched":
		description = "delivery missing snapshot, malformed destination, event, or deadline mismatch"
	case "snapshot_delivery_count":
		if violation.RelatedID == "0" {
			description = "snapshot missing delivery"
		} else {
			description = "snapshot duplicate delivery"
		}
	}
	return fmt.Errorf(
		"webhook credential %s violation on %s related to %s",
		description,
		violation.ObjectID,
		violation.RelatedID,
	)
}

func webhookCredentialUUIDShapeSQL(
	dialect string,
	column string,
	requireV7 bool,
) string {
	if dialect == "postgres" {
		version := "[0-9a-f]"
		if requireV7 {
			version = "7"
		}
		return column + " ~ '^[0-9a-f]{8}-[0-9a-f]{4}-" +
			version +
			"[0-9a-f]{3}-[0-9a-f]{4}-[0-9a-f]{12}$'"
	}
	version := "substr(" + column + ", 15, 1) BETWEEN '0' AND 'f'"
	if requireV7 {
		version = "substr(" + column + ", 15, 1) = '7'"
	}
	return "length(" + column + ") = 36" +
		" AND lower(" + column + ") = " + column +
		" AND substr(" + column + ", 1, 8) NOT GLOB '*[^0-9a-f]*'" +
		" AND substr(" + column + ", 9, 1) = '-'" +
		" AND substr(" + column + ", 10, 4) NOT GLOB '*[^0-9a-f]*'" +
		" AND substr(" + column + ", 14, 1) = '-'" +
		" AND substr(" + column + ", 15, 4) NOT GLOB '*[^0-9a-f]*'" +
		" AND substr(" + column + ", 19, 1) = '-'" +
		" AND substr(" + column + ", 20, 4) NOT GLOB '*[^0-9a-f]*'" +
		" AND substr(" + column + ", 24, 1) = '-'" +
		" AND substr(" + column + ", 25, 12) NOT GLOB '*[^0-9a-f]*'" +
		" AND " + version
}

func closedVocabularySQLList[T ~string](values []T) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(
			quoted,
			quoteClosedVocabularyValue(string(value)),
		)
	}
	return strings.Join(quoted, ", ")
}

func sqlBooleanLiteral(value bool) string {
	if value {
		return "TRUE"
	}
	return "FALSE"
}
