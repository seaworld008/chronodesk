package services

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/listcursor"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

const (
	DefaultWebhookListSize = 25
	MaxWebhookListSize     = 100

	webhookDeliveryCursorVersion = 1
	webhookDeliverySortVersion   = "created_at_desc_id_desc.v1"
)

var (
	ErrInvalidWebhookListQuery  = errors.New("webhook list query is invalid")
	ErrInvalidWebhookListCursor = errors.New("webhook list cursor is invalid")
	ErrWebhookListCursorKey     = errors.New("webhook list cursor signing key is unavailable")
	ErrWebhookConfigNotFound    = errors.New("webhook configuration not found")
)

type WebhookDefinitionQuery struct {
	Page     int
	PageSize int
	Provider models.WebhookProvider
	Status   models.WebhookStatus
}

type WebhookDefinitionPage struct {
	Items      []models.WebhookConfig
	Total      int64
	Page       int
	PageSize   int
	TotalPages int
}

type WebhookDeliveryQuery struct {
	Cursor    string
	Limit     int
	Status    string
	EventType models.WebhookEventType
}

type WebhookDeliveryPage struct {
	Items      []models.WebhookLog
	NextCursor string
	HasMore    bool
}

type webhookDeliveryCursor struct {
	Version      int    `json:"v"`
	Organization uint   `json:"organization_id"`
	Project      uint   `json:"project_id"`
	ConfigID     uint   `json:"config_id"`
	Limit        int    `json:"limit"`
	FilterHash   string `json:"filter_hash"`
	SortVersion  string `json:"sort_version"`
	CreatedAt    string `json:"created_at"`
	ID           uint   `json:"id"`
}

type WebhookQueryService struct {
	db                  *gorm.DB
	deliveryCursorCodec *listcursor.Codec
}

func NewWebhookQueryService(db *gorm.DB) *WebhookQueryService {
	return &WebhookQueryService{db: db}
}

func (s *WebhookQueryService) ConfigureListCursor(root []byte) error {
	if s == nil || s.db == nil || len(root) == 0 {
		return ErrWebhookListCursorKey
	}
	codec, err := listcursor.NewCodec(root, "webhook-delivery-attempts.v1")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrWebhookListCursorKey, err)
	}
	s.deliveryCursorCodec = codec
	return nil
}

func (s *WebhookQueryService) ListDefinitions(
	ctx context.Context,
	query WebhookDefinitionQuery,
) (*WebhookDefinitionPage, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("webhook query database is required")
	}
	scope, err := webhookQueryScope(ctx)
	if err != nil {
		return nil, err
	}
	if query.Page < 1 || query.PageSize < 1 ||
		query.PageSize > MaxWebhookListSize ||
		(query.Provider != "" && !validWebhookQueryProvider(query.Provider)) ||
		(query.Status != "" && !validWebhookQueryStatus(query.Status)) {
		return nil, ErrInvalidWebhookListQuery
	}

	definitions := s.db.WithContext(ctx).
		Model(&models.WebhookConfig{}).
		Where(
			"organization_id = ? AND project_id = ?",
			scope.OrganizationID,
			scope.ProjectID,
		)
	if query.Provider != "" {
		definitions = definitions.Where("provider = ?", query.Provider)
	}
	if query.Status != "" {
		definitions = definitions.Where("status = ?", query.Status)
	}

	var total int64
	if err := definitions.Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count webhook definitions: %w", err)
	}
	var items []models.WebhookConfig
	if err := definitions.
		Order("created_at DESC").
		Order("id DESC").
		Offset((query.Page - 1) * query.PageSize).
		Limit(query.PageSize).
		Find(&items).Error; err != nil {
		return nil, fmt.Errorf("list webhook definitions: %w", err)
	}
	if items == nil {
		items = []models.WebhookConfig{}
	}
	return &WebhookDefinitionPage{
		Items:      items,
		Total:      total,
		Page:       query.Page,
		PageSize:   query.PageSize,
		TotalPages: webhookTotalPages(total, query.PageSize),
	}, nil
}

func (s *WebhookQueryService) ListDeliveries(
	ctx context.Context,
	configID uint,
	query WebhookDeliveryQuery,
) (*WebhookDeliveryPage, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("webhook query database is required")
	}
	scope, err := webhookQueryScope(ctx)
	if err != nil {
		return nil, err
	}
	if s.deliveryCursorCodec == nil {
		return nil, ErrWebhookListCursorKey
	}
	if configID == 0 || query.Limit < 1 ||
		query.Limit > MaxWebhookListSize ||
		(query.Status != "" &&
			query.Status != "pending" &&
			query.Status != "success" &&
			query.Status != "failed") ||
		(query.EventType != "" && models.ValidateWebhookSubscriptions(
			[]models.WebhookEventType{query.EventType},
			nil,
			true,
		) != nil) {
		return nil, ErrInvalidWebhookListQuery
	}
	var config models.WebhookConfig
	if err := s.db.WithContext(ctx).
		Select("id").
		Where(
			"organization_id = ? AND project_id = ? AND id = ?",
			scope.OrganizationID,
			scope.ProjectID,
			configID,
		).
		First(&config).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWebhookConfigNotFound
		}
		return nil, fmt.Errorf("resolve webhook configuration: %w", err)
	}

	filterHash := webhookDeliveryFilterHash(query)
	cursor, err := s.decodeDeliveryCursor(
		query.Cursor,
		scope,
		configID,
		query.Limit,
		filterHash,
	)
	if err != nil {
		return nil, err
	}
	deliveries := s.db.WithContext(ctx).
		Model(&models.WebhookLog{}).
		Where(
			"organization_id = ? AND project_id = ? AND config_id = ?",
			scope.OrganizationID,
			scope.ProjectID,
			configID,
		)
	if query.Status != "" {
		deliveries = deliveries.Where("status = ?", query.Status)
	}
	if query.EventType != "" {
		deliveries = deliveries.Where("event_type = ?", query.EventType)
	}
	if cursor != nil {
		deliveries = deliveries.Where(
			"created_at < ? OR (created_at = ? AND id < ?)",
			cursor.CreatedAt,
			cursor.CreatedAt,
			cursor.ID,
		)
	}

	var items []models.WebhookLog
	if err := deliveries.
		Order("created_at DESC").
		Order("id DESC").
		Limit(query.Limit + 1).
		Find(&items).Error; err != nil {
		return nil, fmt.Errorf("list webhook deliveries: %w", err)
	}
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	nextCursor := ""
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		nextCursor, err = s.deliveryCursorCodec.Encode(webhookDeliveryCursor{
			Version:      webhookDeliveryCursorVersion,
			Organization: scope.OrganizationID,
			Project:      scope.ProjectID,
			ConfigID:     configID,
			Limit:        query.Limit,
			FilterHash:   filterHash,
			SortVersion:  webhookDeliverySortVersion,
			CreatedAt:    last.CreatedAt.UTC().Format(time.RFC3339Nano),
			ID:           last.ID,
		})
		if err != nil {
			return nil, ErrWebhookListCursorKey
		}
	}
	if items == nil {
		items = []models.WebhookLog{}
	}
	return &WebhookDeliveryPage{
		Items:      items,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

func webhookQueryScope(ctx context.Context) (models.ProjectScope, error) {
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return models.ProjectScope{}, fmt.Errorf(
			"trusted webhook project scope is required: %w",
			err,
		)
	}
	return scope, nil
}

func webhookDeliveryFilterHash(query WebhookDeliveryQuery) string {
	raw, _ := json.Marshal(struct {
		Status    string                  `json:"status"`
		EventType models.WebhookEventType `json:"event_type"`
	}{
		Status:    query.Status,
		EventType: query.EventType,
	})
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum[:])
}

func (s *WebhookQueryService) decodeDeliveryCursor(
	raw string,
	scope models.ProjectScope,
	configID uint,
	limit int,
	filterHash string,
) (*struct {
	CreatedAt time.Time
	ID        uint
}, error) {
	if raw == "" {
		return nil, nil
	}
	var cursor webhookDeliveryCursor
	if err := s.deliveryCursorCodec.Decode(raw, &cursor); err != nil {
		return nil, ErrInvalidWebhookListCursor
	}
	createdAt, err := time.Parse(time.RFC3339Nano, cursor.CreatedAt)
	if err != nil || createdAt.IsZero() || cursor.ID == 0 ||
		cursor.Version != webhookDeliveryCursorVersion ||
		cursor.Organization != scope.OrganizationID ||
		cursor.Project != scope.ProjectID ||
		cursor.ConfigID != configID ||
		cursor.Limit != limit ||
		cursor.FilterHash != filterHash ||
		cursor.SortVersion != webhookDeliverySortVersion ||
		strings.TrimSpace(cursor.CreatedAt) != cursor.CreatedAt {
		return nil, ErrInvalidWebhookListCursor
	}
	return &struct {
		CreatedAt time.Time
		ID        uint
	}{
		CreatedAt: createdAt.UTC(),
		ID:        cursor.ID,
	}, nil
}

func webhookTotalPages(total int64, size int) int {
	if total <= 0 || size <= 0 {
		return 0
	}
	return int((total + int64(size) - 1) / int64(size))
}

func validWebhookQueryProvider(provider models.WebhookProvider) bool {
	switch provider {
	case models.WebhookProviderWeChat,
		models.WebhookProviderDingTalk,
		models.WebhookProviderLark,
		models.WebhookProviderSlack,
		models.WebhookProviderTeams,
		models.WebhookProviderCustom:
		return true
	default:
		return false
	}
}

func validWebhookQueryStatus(status models.WebhookStatus) bool {
	switch status {
	case models.WebhookStatusActive,
		models.WebhookStatusInactive,
		models.WebhookStatusDisabled,
		models.WebhookStatusError:
		return true
	default:
		return false
	}
}
