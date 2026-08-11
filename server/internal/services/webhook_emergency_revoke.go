package services

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// WebhookEmergencyRevokeResult is the secret-free durable outcome projected
// by the Human administrator API. In-flight requests are reported because an
// HTTP request that already left the process cannot be recalled.
type WebhookEmergencyRevokeResult struct {
	ConfigID              uint                 `json:"config_id"`
	Status                models.WebhookStatus `json:"status"`
	ExpiredDeliveries     int                  `json:"expired_deliveries"`
	InFlightDeliveries    int                  `json:"in_flight_deliveries"`
	ShreddedSnapshots     int                  `json:"shredded_snapshots"`
	CredentialShredReason string               `json:"credential_shred_reason"`
}

// EmergencyRevokeWebhook disables one mutable Webhook configuration and
// terminalizes all work that has not already started. It is deliberately a
// domain command rather than an HTTP-handler rule so every Human adapter uses
// the same authorization, scope, locking and lifecycle semantics.
func (s *AgentNativeService) EmergencyRevokeWebhook(
	ctx context.Context,
	configID uint,
) (WebhookEmergencyRevokeResult, error) {
	result := WebhookEmergencyRevokeResult{}
	if s == nil || s.db == nil || configID == 0 {
		return result, ErrWebhookConfigNotFound
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil ||
		operation.Source != SourceProtocolHumanREST ||
		operation.Actor.Type != models.ActorTypeHuman {
		return result, ErrProjectAccessDenied
	}
	userID, err := humanActorUserID(operation.Actor)
	if err != nil {
		return result, ErrProjectAccessDenied
	}

	err = runProjectOperation(
		ctx,
		s.db,
		func(projectCtx context.Context) error {
			return transactionForContext(
				projectCtx,
				s.db,
				func(tx *gorm.DB) error {
					projects, projectErr := NewProjectService(tx)
					if projectErr != nil {
						return projectErr
					}
					access, accessErr :=
						projects.RevalidateHumanProjectAccess(
							projectCtx,
							operation.Scope,
							userID,
						)
					if accessErr != nil {
						return accessErr
					}
					if access == nil ||
						access.Role != models.ProjectRoleAdmin {
						return ErrProjectAccessDenied
					}

					config, lockErr := lockWebhookConfigByID(
						tx.WithContext(projectCtx),
						operation.Scope,
						configID,
						"UPDATE",
					)
					if errors.Is(lockErr, gorm.ErrRecordNotFound) {
						return ErrWebhookConfigNotFound
					}
					if lockErr != nil {
						return lockErr
					}

					snapshots, snapshotErr :=
						listWebhookSnapshotsForConfig(
							tx.WithContext(projectCtx),
							operation.Scope,
							config.ID,
						)
					if snapshotErr != nil {
						return snapshotErr
					}
					deliveries, deliveryErr :=
						lockWebhookDeliveriesForSnapshots(
							tx.WithContext(projectCtx),
							operation.Scope,
							snapshots,
						)
					if deliveryErr != nil {
						return deliveryErr
					}

					// The shared order is config -> delivery -> snapshot. The
					// first snapshot query above is discovery-only; acquire
					// delivery row locks before taking snapshot row locks.
					snapshots, snapshotErr =
						lockWebhookSnapshotsForConfig(
							tx.WithContext(projectCtx),
							operation.Scope,
							config.ID,
						)
					if snapshotErr != nil {
						return snapshotErr
					}

					now := s.now().UTC()
					configUpdate := tx.WithContext(projectCtx).
						Unscoped().
						Model(&models.WebhookConfig{}).
						Where(
							"id = ? AND organization_id = ? AND project_id = ?",
							config.ID,
							operation.Scope.OrganizationID,
							operation.Scope.ProjectID,
						).
						Updates(map[string]any{
							"status":     models.WebhookStatusDisabled,
							"updated_at": now,
							"updated_by": userID,
						})
					if configUpdate.Error != nil {
						return fmt.Errorf(
							"disable revoked webhook configuration: %w",
							configUpdate.Error,
						)
					}
					if configUpdate.RowsAffected != 1 {
						return ErrWebhookConfigNotFound
					}

					for index := range deliveries {
						delivery := &deliveries[index]
						switch delivery.Status {
						case models.OutboxDeliveryPending,
							models.OutboxDeliveryFailed,
							models.OutboxDeliveryDead:
							update := tx.WithContext(projectCtx).
								Model(&models.OutboxDelivery{}).
								Where(
									"id = ? AND organization_id = ? AND project_id = ? AND status = ?",
									delivery.ID,
									operation.Scope.OrganizationID,
									operation.Scope.ProjectID,
									delivery.Status,
								).
								Updates(map[string]any{
									"status":       models.OutboxDeliveryExpired,
									"expired_at":   now,
									"delivered_at": nil,
									"locked_at":    nil,
									"locked_by":    "",
									"lock_token":   nil,
									"last_error": "webhook delivery " +
										"credentials revoked",
									"updated_at": now,
								})
							if update.Error != nil {
								return fmt.Errorf(
									"expire revoked webhook delivery: %w",
									update.Error,
								)
							}
							if update.RowsAffected != 1 {
								return ErrWebhookOutboxLifecycleInvariant
							}
							result.ExpiredDeliveries++
						case models.OutboxDeliveryProcessing:
							result.InFlightDeliveries++
						case models.OutboxDeliverySucceeded,
							models.OutboxDeliveryExpired:
							// Terminal delivery history is immutable.
						default:
							return ErrWebhookOutboxLifecycleInvariant
						}
					}

					for index := range snapshots {
						if snapshots[index].CredentialShreddedAt != nil {
							continue
						}
						if err := shredWebhookSnapshot(
							tx.WithContext(projectCtx),
							&snapshots[index],
							models.WebhookCredentialShredReasonRevoked,
							now,
						); err != nil {
							return err
						}
						result.ShreddedSnapshots++
					}

					result.ConfigID = config.ID
					result.Status = models.WebhookStatusDisabled
					result.CredentialShredReason = string(
						models.WebhookCredentialShredReasonRevoked,
					)
					return nil
				},
			)
		},
	)
	if err != nil {
		return WebhookEmergencyRevokeResult{}, err
	}
	return result, nil
}

func lockWebhookConfigByID(
	tx *gorm.DB,
	scope models.ProjectScope,
	configID uint,
	strength string,
) (*models.WebhookConfig, error) {
	if tx == nil || configID == 0 {
		return nil, ErrWebhookOutboxLifecycleInvariant
	}
	var config models.WebhookConfig
	err := tx.Unscoped().
		Clauses(clause.Locking{Strength: strength}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			configID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		Take(&config).Error
	if err != nil {
		return nil, err
	}
	return &config, nil
}

func webhookConfigIDForSnapshot(
	tx *gorm.DB,
	scope models.ProjectScope,
	snapshotID string,
) (uint, error) {
	if tx == nil {
		return 0, ErrWebhookOutboxLifecycleInvariant
	}
	snapshotID, err := models.ParseWebhookDeliverySnapshotID(snapshotID)
	if err != nil {
		return 0, ErrWebhookOutboxLifecycleInvariant
	}
	var snapshot struct {
		ConfigID uint
	}
	err = tx.Table((models.WebhookDeliverySnapshot{}).TableName()).
		Select("config_id").
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			snapshotID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		Take(&snapshot).Error
	if err != nil || snapshot.ConfigID == 0 {
		return 0, ErrWebhookOutboxLifecycleInvariant
	}
	return snapshot.ConfigID, nil
}

func lockWebhookConfigForSnapshot(
	tx *gorm.DB,
	scope models.ProjectScope,
	snapshotID string,
) (*models.WebhookConfig, error) {
	configID, err := webhookConfigIDForSnapshot(
		tx,
		scope,
		snapshotID,
	)
	if err != nil {
		return nil, err
	}
	config, err := lockWebhookConfigByID(
		tx,
		scope,
		configID,
		"SHARE",
	)
	if err != nil {
		return nil, ErrWebhookOutboxLifecycleInvariant
	}
	return config, nil
}

func lockWebhookConfigForDestination(
	tx *gorm.DB,
	scope models.ProjectScope,
	destinationID string,
) (*models.WebhookConfig, error) {
	snapshotID, err := models.ParseWebhookDeliverySnapshotDestinationID(
		destinationID,
	)
	if err != nil {
		return nil, ErrWebhookOutboxLifecycleInvariant
	}
	return lockWebhookConfigForSnapshot(tx, scope, snapshotID)
}

func loadOutboxDeliveryLockAnchor(
	tx *gorm.DB,
	scope models.ProjectScope,
	deliveryID string,
) (*models.OutboxDelivery, error) {
	if tx == nil || deliveryID == "" {
		return nil, ErrWebhookOutboxLifecycleInvariant
	}
	var delivery models.OutboxDelivery
	err := tx.Where(
		"id = ? AND organization_id = ? AND project_id = ?",
		deliveryID,
		scope.OrganizationID,
		scope.ProjectID,
	).
		Take(&delivery).Error
	if err != nil {
		return nil, err
	}
	return &delivery, nil
}

func lockWebhookConfigsForDeliveryCandidates(
	tx *gorm.DB,
	scope models.ProjectScope,
	deliveries []models.OutboxDelivery,
) error {
	configSet := make(map[uint]struct{}, len(deliveries))
	for index := range deliveries {
		if deliveries[index].DestinationType != "webhook" {
			continue
		}
		snapshotID, err :=
			models.ParseWebhookDeliverySnapshotDestinationID(
				deliveries[index].DestinationID,
			)
		if err != nil {
			return ErrWebhookOutboxLifecycleInvariant
		}
		configID, err := webhookConfigIDForSnapshot(
			tx,
			scope,
			snapshotID,
		)
		if err != nil {
			return err
		}
		configSet[configID] = struct{}{}
	}
	configIDs := make([]uint, 0, len(configSet))
	for configID := range configSet {
		configIDs = append(configIDs, configID)
	}
	sort.Slice(configIDs, func(left, right int) bool {
		return configIDs[left] < configIDs[right]
	})
	for _, configID := range configIDs {
		if _, err := lockWebhookConfigByID(
			tx,
			scope,
			configID,
			"SHARE",
		); err != nil {
			return ErrWebhookOutboxLifecycleInvariant
		}
	}
	return nil
}

func lockWebhookDeliveryCandidateRows(
	tx *gorm.DB,
	scope models.ProjectScope,
	deliveries []models.OutboxDelivery,
) error {
	ids := make([]string, 0, len(deliveries))
	for index := range deliveries {
		if deliveries[index].DestinationType == "webhook" {
			ids = append(ids, deliveries[index].ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	var locked []struct {
		ID string
	}
	err := tx.Model(&models.OutboxDelivery{}).
		Select("id").
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"organization_id = ? AND project_id = ? AND id IN ?",
			scope.OrganizationID,
			scope.ProjectID,
			ids,
		).
		Order("id ASC").
		Find(&locked).Error
	if err != nil {
		return fmt.Errorf(
			"lock webhook delivery candidates: %w",
			err,
		)
	}
	return nil
}

func lockWebhookSnapshotCandidateRows(
	tx *gorm.DB,
	scope models.ProjectScope,
	deliveries []models.OutboxDelivery,
) error {
	snapshotSet := make(map[string]struct{}, len(deliveries))
	for index := range deliveries {
		if deliveries[index].DestinationType != "webhook" {
			continue
		}
		snapshotID, err :=
			models.ParseWebhookDeliverySnapshotDestinationID(
				deliveries[index].DestinationID,
			)
		if err != nil {
			return ErrWebhookOutboxLifecycleInvariant
		}
		snapshotSet[snapshotID] = struct{}{}
	}
	snapshotIDs := make([]string, 0, len(snapshotSet))
	for snapshotID := range snapshotSet {
		snapshotIDs = append(snapshotIDs, snapshotID)
	}
	if len(snapshotIDs) == 0 {
		return nil
	}
	sort.Strings(snapshotIDs)
	var locked []struct {
		ID string
	}
	err := tx.Model(&models.WebhookDeliverySnapshot{}).
		Select("id").
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"organization_id = ? AND project_id = ? AND id IN ?",
			scope.OrganizationID,
			scope.ProjectID,
			snapshotIDs,
		).
		Order("id ASC").
		Find(&locked).Error
	if err != nil {
		return fmt.Errorf(
			"lock webhook snapshot candidates: %w",
			err,
		)
	}
	if len(locked) != len(snapshotIDs) {
		return ErrWebhookOutboxLifecycleInvariant
	}
	return nil
}

func lockWebhookSnapshotsForConfig(
	tx *gorm.DB,
	scope models.ProjectScope,
	configID uint,
) ([]models.WebhookDeliverySnapshot, error) {
	if tx == nil || configID == 0 {
		return nil, ErrWebhookOutboxLifecycleInvariant
	}
	var snapshots []models.WebhookDeliverySnapshot
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"organization_id = ? AND project_id = ? AND config_id = ?",
			scope.OrganizationID,
			scope.ProjectID,
			configID,
		).
		Order("id ASC").
		Find(&snapshots).Error
	if err != nil {
		return nil, fmt.Errorf(
			"lock revoked webhook snapshots: %w",
			err,
		)
	}
	return snapshots, nil
}

func listWebhookSnapshotsForConfig(
	tx *gorm.DB,
	scope models.ProjectScope,
	configID uint,
) ([]models.WebhookDeliverySnapshot, error) {
	if tx == nil || configID == 0 {
		return nil, ErrWebhookOutboxLifecycleInvariant
	}
	var snapshots []models.WebhookDeliverySnapshot
	err := tx.Where(
		"organization_id = ? AND project_id = ? AND config_id = ?",
		scope.OrganizationID,
		scope.ProjectID,
		configID,
	).
		Order("id ASC").
		Find(&snapshots).Error
	if err != nil {
		return nil, fmt.Errorf(
			"discover revoked webhook snapshots: %w",
			err,
		)
	}
	return snapshots, nil
}

func lockWebhookDeliveriesForSnapshots(
	tx *gorm.DB,
	scope models.ProjectScope,
	snapshots []models.WebhookDeliverySnapshot,
) ([]models.OutboxDelivery, error) {
	if tx == nil || len(snapshots) == 0 {
		return nil, nil
	}
	destinations := make([]string, 0, len(snapshots))
	for index := range snapshots {
		destinationID, err :=
			models.WebhookDeliverySnapshotDestinationID(
				snapshots[index].ID,
			)
		if err != nil {
			return nil, ErrWebhookOutboxLifecycleInvariant
		}
		destinations = append(destinations, destinationID)
	}
	sort.Strings(destinations)
	var deliveries []models.OutboxDelivery
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"organization_id = ? AND project_id = ? AND destination_type = ? AND destination_id IN ?",
			scope.OrganizationID,
			scope.ProjectID,
			"webhook",
			destinations,
		).
		Order("id ASC").
		Find(&deliveries).Error
	if err != nil {
		return nil, fmt.Errorf(
			"lock revoked webhook deliveries: %w",
			err,
		)
	}
	return deliveries, nil
}

func WebhookAdminSubject(configID uint) string {
	return "webhook/" + strconv.FormatUint(uint64(configID), 10)
}
