package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

const MaxWebhookEmergencyTombstonePageSize = 100

var ErrInvalidWebhookEmergencyTombstoneQuery = errors.New(
	"Webhook emergency tombstone query is invalid",
)

// WebhookEmergencyRevokePreflight is the secret-free exact-admin projection
// used to confirm an irreversible emergency revoke.
type WebhookEmergencyRevokePreflight struct {
	ConfigID         uint                 `json:"config_id"`
	Status           models.WebhookStatus `json:"status"`
	Deleted          bool                 `json:"deleted"`
	EmergencyRevoked bool                 `json:"emergency_revoked"`
	ResourceVersion  uint64               `json:"resource_version"`
}

type WebhookEmergencyTombstonePage struct {
	Items      []WebhookEmergencyRevokePreflight `json:"items"`
	Total      int64                             `json:"total"`
	Page       int                               `json:"page"`
	PageSize   int                               `json:"page_size"`
	TotalPages int                               `json:"total_pages"`
}

func (s *AgentNativeService) GetWebhookEmergencyRevokePreflight(
	ctx context.Context,
	configID uint,
) (WebhookEmergencyRevokePreflight, error) {
	var result WebhookEmergencyRevokePreflight
	if s == nil || s.db == nil || configID == 0 {
		return result, ErrWebhookConfigNotFound
	}
	operation, userID, err := webhookEmergencyHumanOperation(ctx)
	if err != nil {
		return result, err
	}
	err = runProjectOperation(
		ctx,
		s.db,
		func(projectCtx context.Context) error {
			return transactionForContext(
				projectCtx,
				s.db,
				func(tx *gorm.DB) error {
					if err := revalidateWebhookEmergencyAdmin(
						projectCtx,
						tx,
						operation.Scope,
						userID,
					); err != nil {
						return err
					}
					config, err := lockWebhookConfigByID(
						tx.WithContext(projectCtx),
						operation.Scope,
						configID,
						"SHARE",
					)
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return ErrWebhookConfigNotFound
					}
					if err != nil {
						return err
					}
					result, err = webhookEmergencyPreflightTx(
						projectCtx,
						tx,
						operation.Scope,
						config,
					)
					return err
				},
			)
		},
	)
	return result, err
}

func (s *AgentNativeService) ListWebhookEmergencyTombstones(
	ctx context.Context,
	page int,
	pageSize int,
) (WebhookEmergencyTombstonePage, error) {
	var result WebhookEmergencyTombstonePage
	if s == nil || s.db == nil {
		return result, ErrProjectAccessDenied
	}
	if page < 1 ||
		pageSize < 1 ||
		pageSize > MaxWebhookEmergencyTombstonePageSize ||
		page > int(^uint(0)>>1)/pageSize {
		return result, ErrInvalidWebhookEmergencyTombstoneQuery
	}
	operation, userID, err := webhookEmergencyHumanOperation(ctx)
	if err != nil {
		return result, err
	}
	err = runProjectOperation(
		ctx,
		s.db,
		func(projectCtx context.Context) error {
			return transactionForContext(
				projectCtx,
				s.db,
				func(tx *gorm.DB) error {
					if err := revalidateWebhookEmergencyAdmin(
						projectCtx,
						tx,
						operation.Scope,
						userID,
					); err != nil {
						return err
					}
					base := tx.WithContext(projectCtx).
						Unscoped().
						Model(&models.WebhookConfig{}).
						Where(
							"organization_id = ? AND project_id = ? AND deleted_at IS NOT NULL",
							operation.Scope.OrganizationID,
							operation.Scope.ProjectID,
						)
					if err := base.Count(&result.Total).Error; err != nil {
						return fmt.Errorf(
							"count Webhook emergency tombstones: %w",
							err,
						)
					}
					var configs []models.WebhookConfig
					if err := base.
						Select(
							"id",
							"status",
							"deleted_at",
						).
						Order("deleted_at DESC, id DESC").
						Offset((page - 1) * pageSize).
						Limit(pageSize).
						Find(&configs).Error; err != nil {
						return fmt.Errorf(
							"list Webhook emergency tombstones: %w",
							err,
						)
					}
					result.Items = make(
						[]WebhookEmergencyRevokePreflight,
						0,
						len(configs),
					)
					for index := range configs {
						item, err := webhookEmergencyPreflightTx(
							projectCtx,
							tx,
							operation.Scope,
							&configs[index],
						)
						if err != nil {
							return err
						}
						result.Items = append(result.Items, item)
					}
					result.Page = page
					result.PageSize = pageSize
					result.TotalPages = int(result.Total / int64(pageSize))
					if result.Total%int64(pageSize) != 0 {
						result.TotalPages++
					}
					return nil
				},
			)
		},
	)
	return result, err
}

func webhookEmergencyHumanOperation(
	ctx context.Context,
) (OperationContext, uint, error) {
	operation, err := OperationContextFromContext(ctx)
	if err != nil ||
		operation.Source != SourceProtocolHumanREST ||
		operation.Actor.Type != models.ActorTypeHuman {
		return OperationContext{}, 0, ErrProjectAccessDenied
	}
	userID, err := humanActorUserID(operation.Actor)
	if err != nil {
		return OperationContext{}, 0, ErrProjectAccessDenied
	}
	return operation, userID, nil
}

func revalidateWebhookEmergencyAdmin(
	ctx context.Context,
	tx *gorm.DB,
	scope models.ProjectScope,
	userID uint,
) error {
	projects, err := NewProjectService(tx)
	if err != nil {
		return err
	}
	access, err := projects.RevalidateHumanProjectAccess(
		ctx,
		scope,
		userID,
	)
	if err != nil {
		return err
	}
	if access == nil || access.Role != models.ProjectRoleAdmin {
		return ErrProjectAccessDenied
	}
	return nil
}

func webhookEmergencyPreflightTx(
	ctx context.Context,
	tx *gorm.DB,
	scope models.ProjectScope,
	config *models.WebhookConfig,
) (WebhookEmergencyRevokePreflight, error) {
	if config == nil || config.ID == 0 {
		return WebhookEmergencyRevokePreflight{},
			ErrWebhookConfigNotFound
	}
	version, err := CurrentWebhookAdminResourceVersionTx(
		ctx,
		tx,
		scope,
		config.ID,
	)
	if err != nil {
		return WebhookEmergencyRevokePreflight{}, err
	}
	var emergencyEvents int64
	if err := tx.WithContext(ctx).
		Model(&models.DomainEvent{}).
		Where(
			"organization_id = ? AND project_id = ? AND subject = ? AND type = ?",
			scope.OrganizationID,
			scope.ProjectID,
			WebhookAdminSubject(config.ID),
			WebhookEmergencyRevokedAdminEventType,
		).
		Count(&emergencyEvents).Error; err != nil {
		return WebhookEmergencyRevokePreflight{}, err
	}
	return WebhookEmergencyRevokePreflight{
		ConfigID:         config.ID,
		Status:           config.Status,
		Deleted:          config.DeletedAt.Valid,
		EmergencyRevoked: emergencyEvents > 0,
		ResourceVersion:  version,
	}, nil
}

// EnsureWebhookConfigMutableTx rejects ordinary configuration edits after the
// durable emergency-revoked administrator event has committed. The event is a
// shared database fact, so the terminal guard survives service/router restarts
// and is not specific to the Human adapter process.
func EnsureWebhookConfigMutableTx(
	ctx context.Context,
	tx *gorm.DB,
	scope models.ProjectScope,
	configID uint,
) error {
	if ctx == nil || tx == nil {
		return fmt.Errorf(
			"%w: Webhook terminal guard requires a transaction context",
			ErrInvalidScope,
		)
	}
	if err := scope.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidScope, err)
	}
	if configID == 0 {
		return ErrWebhookConfigNotFound
	}
	var emergencyEvents int64
	if err := tx.WithContext(ctx).
		Model(&models.DomainEvent{}).
		Where(
			"organization_id = ? AND project_id = ? AND subject = ? AND type = ?",
			scope.OrganizationID,
			scope.ProjectID,
			WebhookAdminSubject(configID),
			WebhookEmergencyRevokedAdminEventType,
		).
		Count(&emergencyEvents).Error; err != nil {
		return err
	}
	if emergencyEvents > 0 {
		return ErrWebhookEmergencyRevokedTerminal
	}
	return nil
}
