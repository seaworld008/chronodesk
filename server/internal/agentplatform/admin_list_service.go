package agentplatform

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/seaworld008/chronodesk/server/internal/listcursor"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const (
	DefaultAdminListSize = 25
	MaxAdminListSize     = 100

	adminListCursorVersion   = 1
	adminEventSortVersion    = "created_at_desc_id_desc.v1"
	adminDecisionSortVersion = "created_at_desc_id_desc.v1"
)

var (
	ErrInvalidAdminListQuery  = errors.New("administrator list query is invalid")
	ErrInvalidAdminListCursor = errors.New("administrator list cursor is invalid")
	ErrAdminListCursorKey     = errors.New("administrator list cursor signing key is unavailable")
)

type adminListQueryKind uint8

const (
	adminPageListQuery adminListQueryKind = iota + 1
	adminCursorListQuery
)

type AdminPageQuery struct {
	Page      int
	PageSize  int
	SortBy    string
	SortOrder string
}

type AdminCursorQuery struct {
	Cursor string
	Limit  int
}

type parsedAdminListQuery struct {
	AdminPageQuery
	AdminCursorQuery
}

type AdminPrincipalGrantSummary struct {
	ID        uint               `json:"id"`
	ProjectID uint               `json:"project_id"`
	Role      models.ProjectRole `json:"role"`
	Scopes    []string           `json:"scopes"`
	IsActive  bool               `json:"is_active"`
	ExpiresAt *time.Time         `json:"expires_at"`
	CreatedAt time.Time          `json:"created_at"`
}

type AdminServicePrincipalListItem struct {
	ID                string                        `json:"id"`
	ClientID          string                        `json:"client_id"`
	Name              string                        `json:"name"`
	Description       string                        `json:"description"`
	Status            models.ServicePrincipalStatus `json:"status"`
	Scopes            []string                      `json:"scopes"`
	RateLimit         int                           `json:"rate_limit"`
	ConcurrencyLimit  int                           `json:"concurrency_limit"`
	LastUsedAt        *time.Time                    `json:"last_used_at"`
	ExpiresAt         *time.Time                    `json:"expires_at"`
	CreatedAt         time.Time                     `json:"created_at"`
	ReadOnly          bool                          `json:"read_only"`
	EmergencyDisabled bool                          `json:"emergency_disabled"`
	ResourceVersion   uint64                        `json:"resource_version"`
	Grant             AdminPrincipalGrantSummary    `json:"grant"`
}

type AdminPrincipalPage struct {
	Items      []AdminServicePrincipalListItem `json:"items"`
	Total      int64                           `json:"total"`
	Page       int                             `json:"page"`
	PageSize   int                             `json:"page_size"`
	TotalPages int                             `json:"total_pages"`
}

type AdminPolicyPage struct {
	Items      []AdminAgentPolicyListItem `json:"items"`
	Total      int64                      `json:"total"`
	Page       int                        `json:"page"`
	PageSize   int                        `json:"page_size"`
	TotalPages int                        `json:"total_pages"`
}

type AdminAgentPolicyListItem struct {
	ID                 string                   `json:"id"`
	CreatedAt          time.Time                `json:"created_at"`
	UpdatedAt          time.Time                `json:"updated_at"`
	ServicePrincipalID string                   `json:"service_principal_id"`
	Name               string                   `json:"name"`
	Effect             models.AgentPolicyEffect `json:"effect"`
	Scope              string                   `json:"scope"`
	Action             string                   `json:"action"`
	ResourceType       string                   `json:"resource_type"`
	ResourceID         string                   `json:"resource_id"`
	Priority           int                      `json:"priority"`
	IsActive           bool                     `json:"is_active"`
	ExpiresAt          *time.Time               `json:"expires_at"`
	ResourceVersion    uint64                   `json:"resource_version"`
}

type AdminTicketLeaseListItem struct {
	ID                string           `json:"id"`
	TicketID          uint             `json:"ticket_id"`
	TicketNumber      string           `json:"ticket_number"`
	HolderActorType   models.ActorType `json:"holder_actor_type"`
	HolderActorID     string           `json:"holder_actor_id"`
	HolderDisplayName string           `json:"holder_display_name"`
	AcquiredAt        time.Time        `json:"acquired_at"`
	ExpiresAt         time.Time        `json:"expires_at"`
	TicketVersion     uint64           `json:"ticket_version"`
	ResourceVersion   uint64           `json:"resource_version"`
}

type AdminLeasePage struct {
	Items      []AdminTicketLeaseListItem `json:"items"`
	Total      int64                      `json:"total"`
	Page       int                        `json:"page"`
	PageSize   int                        `json:"page_size"`
	TotalPages int                        `json:"total_pages"`
}

type AdminOutboxPage struct {
	Items      []AdminOutboxDeliveryListItem `json:"items"`
	Total      int64                         `json:"total"`
	Page       int                           `json:"page"`
	PageSize   int                           `json:"page_size"`
	TotalPages int                           `json:"total_pages"`
}

type AdminOutboxDeliveryListItem struct {
	ID               string                      `json:"id"`
	CreatedAt        time.Time                   `json:"created_at"`
	UpdatedAt        time.Time                   `json:"updated_at"`
	EventID          string                      `json:"event_id"`
	DestinationType  string                      `json:"destination_type"`
	DestinationLabel string                      `json:"destination_label"`
	Status           models.OutboxDeliveryStatus `json:"status"`
	Attempts         int                         `json:"attempts"`
	NextAttemptAt    time.Time                   `json:"next_attempt_at"`
	LastError        string                      `json:"last_error"`
	ResourceVersion  uint64                      `json:"resource_version"`
}

type AdminAttachmentPage struct {
	Items      []AdminAttachmentScanListItem `json:"items"`
	Total      int64                         `json:"total"`
	Page       int                           `json:"page"`
	PageSize   int                           `json:"page_size"`
	TotalPages int                           `json:"total_pages"`
}

type AdminAttachmentScanListItem struct {
	ID              uint                   `json:"id"`
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
	TicketID        uint                   `json:"ticket_id"`
	OriginalName    string                 `json:"original_name"`
	MimeType        string                 `json:"mime_type"`
	FileSize        int64                  `json:"file_size"`
	VirusScan       models.VirusScanStatus `json:"virus_scan"`
	ScannedAt       *time.Time             `json:"scanned_at"`
	ResourceVersion uint64                 `json:"resource_version"`
}

type AdminDomainEventPage struct {
	Items      []AdminDomainEventListItem `json:"items"`
	NextCursor string                     `json:"next_cursor"`
	HasMore    bool                       `json:"has_more"`
}

type AdminDomainEventListItem struct {
	ID              string           `json:"id"`
	CreatedAt       time.Time        `json:"created_at"`
	Type            string           `json:"type"`
	Subject         string           `json:"subject"`
	ActorType       models.ActorType `json:"actor_type"`
	ActorID         string           `json:"actor_id"`
	ResourceVersion uint64           `json:"resource_version"`
	Time            time.Time        `json:"time"`
}

type AdminPolicyDecisionPage struct {
	Items      []AdminPolicyDecisionListItem `json:"items"`
	NextCursor string                        `json:"next_cursor"`
	HasMore    bool                          `json:"has_more"`
}

type AdminPolicyDecisionListItem struct {
	ID              string           `json:"id"`
	CreatedAt       time.Time        `json:"created_at"`
	ActorType       models.ActorType `json:"actor_type"`
	ActorID         string           `json:"actor_id"`
	CredentialID    string           `json:"credential_id"`
	Scope           string           `json:"scope"`
	Action          string           `json:"action"`
	ResourceType    string           `json:"resource_type"`
	ResourceID      string           `json:"resource_id"`
	Allowed         bool             `json:"allowed"`
	ReasonCode      string           `json:"reason_code"`
	MatchedPolicyID string           `json:"matched_policy_id"`
	SourceProtocol  string           `json:"source_protocol"`
}

type AdminOverviewMetrics struct {
	GlobalReadOnly             bool  `json:"global_read_only"`
	EmergencyStop              bool  `json:"emergency_stop"`
	PrincipalCount             int64 `json:"principal_count"`
	ActivePrincipalCount       int64 `json:"active_principal_count"`
	ActiveLeaseCount           int64 `json:"active_lease_count"`
	FailedOutboxCount          int64 `json:"failed_outbox_count"`
	RecentEventCount           int64 `json:"recent_event_count"`
	PendingAttachmentScanCount int64 `json:"pending_attachment_scan_count"`
}

type adminPrincipalScanRow struct {
	ID                string
	Name              string
	Description       string
	Status            models.ServicePrincipalStatus
	Scopes            datatypes.JSON
	RateLimit         int
	ConcurrencyLimit  int
	LastUsedAt        *time.Time
	ExpiresAt         *time.Time
	CreatedAt         time.Time
	ReadOnly          bool
	EmergencyDisabled bool
	GrantID           uint
	GrantProjectID    uint
	GrantRole         models.ProjectRole
	GrantScopes       datatypes.JSON
	GrantIsActive     bool
	GrantExpiresAt    *time.Time
	GrantCreatedAt    time.Time
}

type adminListCursor struct {
	Version      int    `json:"v"`
	Kind         string `json:"kind"`
	Organization uint   `json:"organization_id"`
	Project      uint   `json:"project_id"`
	Limit        int    `json:"limit"`
	FilterHash   string `json:"filter_hash"`
	SortVersion  string `json:"sort_version"`
	CreatedAt    string `json:"created_at"`
	ID           string `json:"id"`
}

type AdminListService struct {
	db                  *gorm.DB
	eventCursorCodec    *listcursor.Codec
	decisionCursorCodec *listcursor.Codec
	now                 func() time.Time
}

func NewAdminListService(
	db *gorm.DB,
	rootCursorKey []byte,
) (*AdminListService, error) {
	if db == nil {
		return nil, errors.New("administrator list database is required")
	}
	if len(rootCursorKey) == 0 {
		return nil, ErrAdminListCursorKey
	}
	service := &AdminListService{db: db, now: time.Now}
	eventCodec, err := listcursor.NewCodec(
		rootCursorKey,
		"agent-admin-domain-events.v1",
	)
	if err != nil {
		return nil, err
	}
	decisionCodec, err := listcursor.NewCodec(
		rootCursorKey,
		"agent-admin-policy-decisions.v1",
	)
	if err != nil {
		return nil, err
	}
	service.eventCursorCodec = eventCodec
	service.decisionCursorCodec = decisionCodec
	return service, nil
}

func (s *AdminListService) Overview(
	ctx context.Context,
	scope models.ProjectScope,
	control *RuntimeControl,
) (*AdminOverviewMetrics, error) {
	if err := validateAdminListScope(ctx, scope); err != nil {
		return nil, err
	}
	now := s.now().UTC()
	metrics := &AdminOverviewMetrics{
		GlobalReadOnly: control != nil && control.ReadOnly(),
		EmergencyStop:  control != nil && control.EmergencyStop(),
	}
	principals := s.scopedPrincipalGrantQuery(ctx, scope)
	if err := principals.
		Distinct("service_principals.id").
		Count(&metrics.PrincipalCount).Error; err != nil {
		return nil, err
	}
	if err := principals.
		Where(
			"project_principal_grants.is_active = ? AND (project_principal_grants.expires_at IS NULL OR project_principal_grants.expires_at > ?) AND service_principals.status = ? AND service_principals.emergency_disabled = ? AND (service_principals.expires_at IS NULL OR service_principals.expires_at > ?)",
			true,
			now,
			models.ServicePrincipalStatusActive,
			false,
			now,
		).
		Distinct("service_principals.id").
		Count(&metrics.ActivePrincipalCount).Error; err != nil {
		return nil, err
	}
	if err := s.db.WithContext(ctx).
		Model(&models.TicketLease{}).
		Where(
			"organization_id = ? AND project_id = ? AND released_at IS NULL AND expires_at > ?",
			scope.OrganizationID,
			scope.ProjectID,
			now,
		).
		Count(&metrics.ActiveLeaseCount).Error; err != nil {
		return nil, err
	}
	if err := s.db.WithContext(ctx).
		Model(&models.OutboxDelivery{}).
		Where(
			"organization_id = ? AND project_id = ? AND status IN ?",
			scope.OrganizationID,
			scope.ProjectID,
			[]models.OutboxDeliveryStatus{
				models.OutboxDeliveryFailed,
				models.OutboxDeliveryDead,
			},
		).
		Count(&metrics.FailedOutboxCount).Error; err != nil {
		return nil, err
	}
	if err := s.db.WithContext(ctx).
		Model(&models.DomainEvent{}).
		Where(
			"organization_id = ? AND project_id = ? AND created_at >= ?",
			scope.OrganizationID,
			scope.ProjectID,
			now.Add(-24*time.Hour),
		).
		Count(&metrics.RecentEventCount).Error; err != nil {
		return nil, err
	}
	if err := s.db.WithContext(ctx).
		Model(&models.TicketAttachment{}).
		Where(
			"organization_id = ? AND project_id = ? AND virus_scan IN ?",
			scope.OrganizationID,
			scope.ProjectID,
			[]models.VirusScanStatus{
				models.VirusScanPending,
				models.VirusScanError,
			},
		).
		Count(&metrics.PendingAttachmentScanCount).Error; err != nil {
		return nil, err
	}
	return metrics, nil
}

func (s *AdminListService) ListPrincipals(
	ctx context.Context,
	scope models.ProjectScope,
	page AdminPageQuery,
) (*AdminPrincipalPage, error) {
	if err := validateAdminPage(ctx, scope, page); err != nil {
		return nil, err
	}
	query := s.scopedPrincipalGrantQuery(ctx, scope)
	var total int64
	if err := query.
		Distinct("service_principals.id").
		Count(&total).Error; err != nil {
		return nil, err
	}
	var rows []adminPrincipalScanRow
	if err := query.
		Select(strings.Join([]string{
			"service_principals.id AS id",
			"service_principals.name AS name",
			"service_principals.description AS description",
			"service_principals.status AS status",
			"service_principals.scopes AS scopes",
			"service_principals.rate_limit_per_minute AS rate_limit",
			"service_principals.concurrent_limit AS concurrency_limit",
			"service_principals.last_used_at AS last_used_at",
			"service_principals.expires_at AS expires_at",
			"service_principals.created_at AS created_at",
			"service_principals.read_only AS read_only",
			"service_principals.emergency_disabled AS emergency_disabled",
			"project_principal_grants.id AS grant_id",
			"project_principal_grants.project_id AS grant_project_id",
			"project_principal_grants.role AS grant_role",
			"project_principal_grants.scopes AS grant_scopes",
			"project_principal_grants.is_active AS grant_is_active",
			"project_principal_grants.expires_at AS grant_expires_at",
			"project_principal_grants.created_at AS grant_created_at",
		}, ", ")).
		Order("service_principals.created_at DESC").
		Order("service_principals.id DESC").
		Offset(adminPageOffset(page)).
		Limit(page.PageSize).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]AdminServicePrincipalListItem, 0, len(rows))
	for i := range rows {
		principalScopes, err := decodeAdminListScopes(rows[i].Scopes)
		if err != nil {
			return nil, err
		}
		grantScopes, err := decodeAdminListScopes(rows[i].GrantScopes)
		if err != nil {
			return nil, err
		}
		items = append(items, AdminServicePrincipalListItem{
			ID:                rows[i].ID,
			ClientID:          rows[i].ID,
			Name:              rows[i].Name,
			Description:       rows[i].Description,
			Status:            rows[i].Status,
			Scopes:            intersectAgentScopes(principalScopes, grantScopes),
			RateLimit:         rows[i].RateLimit,
			ConcurrencyLimit:  rows[i].ConcurrencyLimit,
			LastUsedAt:        rows[i].LastUsedAt,
			ExpiresAt:         rows[i].ExpiresAt,
			CreatedAt:         rows[i].CreatedAt,
			ReadOnly:          rows[i].ReadOnly,
			EmergencyDisabled: rows[i].EmergencyDisabled,
			Grant: AdminPrincipalGrantSummary{
				ID:        rows[i].GrantID,
				ProjectID: rows[i].GrantProjectID,
				Role:      rows[i].GrantRole,
				Scopes:    grantScopes,
				IsActive:  rows[i].GrantIsActive,
				ExpiresAt: rows[i].GrantExpiresAt,
				CreatedAt: rows[i].GrantCreatedAt,
			},
		})
	}
	subjects := make([]string, 0, len(items))
	for i := range items {
		subjects = append(subjects, "service-principal/"+items[i].ID)
	}
	versions, err := s.resourceVersions(ctx, scope, subjects)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].ResourceVersion = versions["service-principal/"+items[i].ID]
	}
	return &AdminPrincipalPage{
		Items:      items,
		Total:      total,
		Page:       page.Page,
		PageSize:   page.PageSize,
		TotalPages: adminTotalPages(total, page.PageSize),
	}, nil
}

func (s *AdminListService) ListPolicies(
	ctx context.Context,
	scope models.ProjectScope,
	principalID string,
	page AdminPageQuery,
) (*AdminPolicyPage, error) {
	if err := validateAdminPage(ctx, scope, page); err != nil {
		return nil, err
	}
	principalID = strings.TrimSpace(principalID)
	if principalID == "" {
		return nil, ErrInvalidAdminListQuery
	}
	var principalCount int64
	if err := s.scopedPrincipalGrantQuery(ctx, scope).
		Where("service_principals.id = ?", principalID).
		Distinct("service_principals.id").
		Count(&principalCount).Error; err != nil {
		return nil, err
	}
	if principalCount != 1 {
		return nil, services.ErrPrincipalNotFound
	}
	query := s.db.WithContext(ctx).
		Model(&models.AgentPolicy{}).
		Joins(
			"JOIN project_principal_grants ON project_principal_grants.service_principal_id = agent_policies.service_principal_id",
		).
		Joins("JOIN projects ON projects.id = project_principal_grants.project_id").
		Where(
			"agent_policies.service_principal_id = ? AND project_principal_grants.project_id = ? AND projects.organization_id = ?",
			principalID,
			scope.ProjectID,
			scope.OrganizationID,
		)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	var policies []models.AgentPolicy
	if err := query.
		Order("agent_policies.priority DESC").
		Order("agent_policies.created_at DESC").
		Order("agent_policies.id DESC").
		Offset(adminPageOffset(page)).
		Limit(page.PageSize).
		Find(&policies).Error; err != nil {
		return nil, err
	}
	subjects := make([]string, 0, len(policies))
	for i := range policies {
		subjects = append(
			subjects,
			"service-principal/"+policies[i].ServicePrincipalID+
				"/policy/"+policies[i].ID,
		)
	}
	versions, err := s.resourceVersions(ctx, scope, subjects)
	if err != nil {
		return nil, err
	}
	items := make([]AdminAgentPolicyListItem, 0, len(policies))
	for i := range policies {
		policy := &policies[i]
		subject := "service-principal/" + policy.ServicePrincipalID +
			"/policy/" + policy.ID
		items = append(items, AdminAgentPolicyListItem{
			ID:                 policy.ID,
			CreatedAt:          policy.CreatedAt,
			UpdatedAt:          policy.UpdatedAt,
			ServicePrincipalID: policy.ServicePrincipalID,
			Name:               policy.Name,
			Effect:             policy.Effect,
			Scope:              policy.Scope,
			Action:             policy.Action,
			ResourceType:       policy.ResourceType,
			ResourceID:         policy.ResourceID,
			Priority:           policy.Priority,
			IsActive:           policy.IsActive,
			ExpiresAt:          policy.ExpiresAt,
			ResourceVersion:    versions[subject],
		})
	}
	return &AdminPolicyPage{
		Items:      items,
		Total:      total,
		Page:       page.Page,
		PageSize:   page.PageSize,
		TotalPages: adminTotalPages(total, page.PageSize),
	}, nil
}

func (s *AdminListService) ListLeases(
	ctx context.Context,
	scope models.ProjectScope,
	page AdminPageQuery,
) (*AdminLeasePage, error) {
	if err := validateAdminPage(ctx, scope, page); err != nil {
		return nil, err
	}
	now := s.now().UTC()
	query := s.db.WithContext(ctx).
		Model(&models.TicketLease{}).
		Joins(
			"JOIN tickets ON tickets.id = ticket_leases.ticket_id AND tickets.organization_id = ticket_leases.organization_id AND tickets.project_id = ticket_leases.project_id",
		).
		Where(
			"ticket_leases.organization_id = ? AND ticket_leases.project_id = ? AND ticket_leases.released_at IS NULL AND ticket_leases.expires_at > ?",
			scope.OrganizationID,
			scope.ProjectID,
			now,
		).
		Where(
			"tickets.organization_id = ? AND tickets.project_id = ?",
			scope.OrganizationID,
			scope.ProjectID,
		)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	var items []AdminTicketLeaseListItem
	if err := query.
		Select(strings.Join([]string{
			"ticket_leases.id AS id",
			"ticket_leases.ticket_id AS ticket_id",
			"tickets.ticket_number AS ticket_number",
			"ticket_leases.holder_actor_type AS holder_actor_type",
			"ticket_leases.holder_actor_id AS holder_actor_id",
			"CASE WHEN ticket_leases.holder_actor_type = 'service_principal' THEN COALESCE(service_principals.name, ticket_leases.holder_actor_id) WHEN ticket_leases.holder_actor_type = 'human' THEN COALESCE(NULLIF(users.display_name, ''), users.username, ticket_leases.holder_actor_id) ELSE ticket_leases.holder_actor_id END AS holder_display_name",
			"ticket_leases.created_at AS acquired_at",
			"ticket_leases.expires_at AS expires_at",
			"ticket_leases.ticket_version AS ticket_version",
		}, ", ")).
		Joins(
			"LEFT JOIN service_principals ON service_principals.id = ticket_leases.holder_actor_id AND ticket_leases.holder_actor_type = ? AND service_principals.deleted_at IS NULL",
			models.ActorTypeServicePrincipal,
		).
		Joins(
			"LEFT JOIN users ON CAST(users.id AS TEXT) = ticket_leases.holder_actor_id AND ticket_leases.holder_actor_type = ? AND users.deleted_at IS NULL",
			models.ActorTypeHuman,
		).
		Order("ticket_leases.expires_at ASC").
		Order("ticket_leases.id ASC").
		Offset(adminPageOffset(page)).
		Limit(page.PageSize).
		Scan(&items).Error; err != nil {
		return nil, err
	}
	if items == nil {
		items = []AdminTicketLeaseListItem{}
	}
	subjects := make([]string, 0, len(items))
	for i := range items {
		subjects = append(subjects, "lease/"+items[i].ID)
	}
	versions, err := s.resourceVersions(ctx, scope, subjects)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].ResourceVersion = versions["lease/"+items[i].ID]
	}
	return &AdminLeasePage{
		Items:      items,
		Total:      total,
		Page:       page.Page,
		PageSize:   page.PageSize,
		TotalPages: adminTotalPages(total, page.PageSize),
	}, nil
}

func (s *AdminListService) ListOutbox(
	ctx context.Context,
	scope models.ProjectScope,
	page AdminPageQuery,
) (*AdminOutboxPage, error) {
	if err := validateAdminPage(ctx, scope, page); err != nil {
		return nil, err
	}
	query := s.db.WithContext(ctx).
		Model(&models.OutboxDelivery{}).
		Where(
			"organization_id = ? AND project_id = ?",
			scope.OrganizationID,
			scope.ProjectID,
		)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	var deliveries []models.OutboxDelivery
	if err := query.
		Order("created_at DESC").
		Order("id DESC").
		Offset(adminPageOffset(page)).
		Limit(page.PageSize).
		Find(&deliveries).Error; err != nil {
		return nil, err
	}
	subjects := make([]string, 0, len(deliveries))
	for i := range deliveries {
		subjects = append(subjects, "outbox/"+deliveries[i].ID)
	}
	versions, err := s.resourceVersions(ctx, scope, subjects)
	if err != nil {
		return nil, err
	}
	items := make([]AdminOutboxDeliveryListItem, 0, len(deliveries))
	for i := range deliveries {
		delivery := &deliveries[i]
		items = append(items, AdminOutboxDeliveryListItem{
			ID:        delivery.ID,
			CreatedAt: delivery.CreatedAt,
			UpdatedAt: delivery.UpdatedAt,
			EventID:   delivery.EventID,
			DestinationType: adminOutboxDestinationType(
				delivery.DestinationType,
			),
			DestinationLabel: adminOutboxDestinationLabel(
				delivery.DestinationType,
			),
			Status:        delivery.Status,
			Attempts:      delivery.Attempts,
			NextAttemptAt: delivery.NextAttemptAt,
			LastError: truncateAdminListText(
				services.ScrubOutboxFailureText(delivery.LastError),
				500,
			),
			ResourceVersion: versions["outbox/"+delivery.ID],
		})
	}
	return &AdminOutboxPage{
		Items:      items,
		Total:      total,
		Page:       page.Page,
		PageSize:   page.PageSize,
		TotalPages: adminTotalPages(total, page.PageSize),
	}, nil
}

func (s *AdminListService) ListAttachments(
	ctx context.Context,
	scope models.ProjectScope,
	page AdminPageQuery,
) (*AdminAttachmentPage, error) {
	if err := validateAdminPage(ctx, scope, page); err != nil {
		return nil, err
	}
	query := s.db.WithContext(ctx).
		Model(&models.TicketAttachment{}).
		Where(
			"organization_id = ? AND project_id = ?",
			scope.OrganizationID,
			scope.ProjectID,
		)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	var attachments []models.TicketAttachment
	if err := query.
		Order("created_at DESC").
		Order("id DESC").
		Offset(adminPageOffset(page)).
		Limit(page.PageSize).
		Find(&attachments).Error; err != nil {
		return nil, err
	}
	subjects := make([]string, 0, len(attachments))
	for i := range attachments {
		subjects = append(
			subjects,
			"attachment/"+strconv.FormatUint(uint64(attachments[i].ID), 10),
		)
	}
	versions, err := s.resourceVersions(ctx, scope, subjects)
	if err != nil {
		return nil, err
	}
	items := make([]AdminAttachmentScanListItem, 0, len(attachments))
	for i := range attachments {
		attachment := &attachments[i]
		subject := "attachment/" +
			strconv.FormatUint(uint64(attachment.ID), 10)
		items = append(items, AdminAttachmentScanListItem{
			ID:              attachment.ID,
			CreatedAt:       attachment.CreatedAt,
			UpdatedAt:       attachment.UpdatedAt,
			TicketID:        attachment.TicketID,
			OriginalName:    attachment.OriginalName,
			MimeType:        attachment.MimeType,
			FileSize:        attachment.FileSize,
			VirusScan:       attachment.VirusScan,
			ScannedAt:       attachment.ScannedAt,
			ResourceVersion: versions[subject],
		})
	}
	return &AdminAttachmentPage{
		Items:      items,
		Total:      total,
		Page:       page.Page,
		PageSize:   page.PageSize,
		TotalPages: adminTotalPages(total, page.PageSize),
	}, nil
}

func (s *AdminListService) ListDomainEvents(
	ctx context.Context,
	scope models.ProjectScope,
	query AdminCursorQuery,
) (*AdminDomainEventPage, error) {
	if err := validateAdminCursorQuery(ctx, scope, query); err != nil {
		return nil, err
	}
	const kind = "domain_events"
	filterHash := adminListFilterHash(kind)
	cursor, err := s.decodeCursor(
		query.Cursor,
		kind,
		scope,
		query.Limit,
		filterHash,
		adminEventSortVersion,
	)
	if err != nil {
		return nil, err
	}
	dbQuery := s.db.WithContext(ctx).
		Model(&models.DomainEvent{}).
		Where(
			"organization_id = ? AND project_id = ?",
			scope.OrganizationID,
			scope.ProjectID,
		)
	if cursor != nil {
		dbQuery = dbQuery.Where(
			"created_at < ? OR (created_at = ? AND id < ?)",
			cursor.CreatedAt,
			cursor.CreatedAt,
			cursor.ID,
		)
	}
	var events []models.DomainEvent
	if err := dbQuery.
		Order("created_at DESC").
		Order("id DESC").
		Limit(query.Limit + 1).
		Find(&events).Error; err != nil {
		return nil, err
	}
	hasMore := len(events) > query.Limit
	if hasMore {
		events = events[:query.Limit]
	}
	nextCursor := ""
	if hasMore && len(events) > 0 {
		last := events[len(events)-1]
		nextCursor, err = s.encodeCursor(
			kind,
			scope,
			query.Limit,
			filterHash,
			adminEventSortVersion,
			last.CreatedAt,
			last.ID,
		)
		if err != nil {
			return nil, err
		}
	}
	items := make([]AdminDomainEventListItem, 0, len(events))
	for i := range events {
		event := &events[i]
		items = append(items, AdminDomainEventListItem{
			ID:              event.ID,
			CreatedAt:       event.CreatedAt,
			Type:            event.Type,
			Subject:         event.Subject,
			ActorType:       event.ActorType,
			ActorID:         event.ActorID,
			ResourceVersion: event.ResourceVersion,
			Time:            event.Time,
		})
	}
	return &AdminDomainEventPage{
		Items:      items,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

func (s *AdminListService) ListPolicyDecisions(
	ctx context.Context,
	scope models.ProjectScope,
	query AdminCursorQuery,
) (*AdminPolicyDecisionPage, error) {
	if err := validateAdminCursorQuery(ctx, scope, query); err != nil {
		return nil, err
	}
	const kind = "policy_decisions"
	filterHash := adminListFilterHash(kind)
	cursor, err := s.decodeCursor(
		query.Cursor,
		kind,
		scope,
		query.Limit,
		filterHash,
		adminDecisionSortVersion,
	)
	if err != nil {
		return nil, err
	}
	dbQuery := s.db.WithContext(ctx).
		Model(&models.PolicyDecision{}).
		Where(
			"organization_id = ? AND project_id = ?",
			scope.OrganizationID,
			scope.ProjectID,
		)
	if cursor != nil {
		dbQuery = dbQuery.Where(
			"created_at < ? OR (created_at = ? AND id < ?)",
			cursor.CreatedAt,
			cursor.CreatedAt,
			cursor.ID,
		)
	}
	var decisions []models.PolicyDecision
	if err := dbQuery.
		Order("created_at DESC").
		Order("id DESC").
		Limit(query.Limit + 1).
		Find(&decisions).Error; err != nil {
		return nil, err
	}
	hasMore := len(decisions) > query.Limit
	if hasMore {
		decisions = decisions[:query.Limit]
	}
	nextCursor := ""
	if hasMore && len(decisions) > 0 {
		last := decisions[len(decisions)-1]
		nextCursor, err = s.encodeCursor(
			kind,
			scope,
			query.Limit,
			filterHash,
			adminDecisionSortVersion,
			last.CreatedAt,
			last.ID,
		)
		if err != nil {
			return nil, err
		}
	}
	items := make([]AdminPolicyDecisionListItem, 0, len(decisions))
	for i := range decisions {
		decision := &decisions[i]
		items = append(items, AdminPolicyDecisionListItem{
			ID:              decision.ID,
			CreatedAt:       decision.CreatedAt,
			ActorType:       decision.ActorType,
			ActorID:         decision.ActorID,
			CredentialID:    decision.CredentialID,
			Scope:           decision.Scope,
			Action:          decision.Action,
			ResourceType:    decision.ResourceType,
			ResourceID:      decision.ResourceID,
			Allowed:         decision.Allowed,
			ReasonCode:      decision.ReasonCode,
			MatchedPolicyID: decision.MatchedPolicyID,
			SourceProtocol:  decision.SourceProtocol,
		})
	}
	return &AdminPolicyDecisionPage{
		Items:      items,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

func (s *AdminListService) scopedPrincipalGrantQuery(
	ctx context.Context,
	scope models.ProjectScope,
) *gorm.DB {
	return s.db.WithContext(ctx).
		Model(&models.ServicePrincipal{}).
		Joins(
			"JOIN project_principal_grants ON project_principal_grants.service_principal_id = service_principals.id",
		).
		Joins("JOIN projects ON projects.id = project_principal_grants.project_id").
		Where(
			"project_principal_grants.project_id = ? AND projects.organization_id = ?",
			scope.ProjectID,
			scope.OrganizationID,
		)
}

func (s *AdminListService) encodeCursor(
	kind string,
	scope models.ProjectScope,
	limit int,
	filterHash string,
	sortVersion string,
	createdAt time.Time,
	id string,
) (string, error) {
	codec := s.cursorCodec(kind)
	if codec == nil {
		return "", ErrAdminListCursorKey
	}
	cursor := adminListCursor{
		Version:      adminListCursorVersion,
		Kind:         kind,
		Organization: scope.OrganizationID,
		Project:      scope.ProjectID,
		Limit:        limit,
		FilterHash:   filterHash,
		SortVersion:  sortVersion,
		CreatedAt:    createdAt.UTC().Format(time.RFC3339Nano),
		ID:           id,
	}
	return codec.Encode(cursor)
}

func (s *AdminListService) decodeCursor(
	raw string,
	kind string,
	scope models.ProjectScope,
	limit int,
	filterHash string,
	sortVersion string,
) (*struct {
	CreatedAt time.Time
	ID        string
}, error) {
	codec := s.cursorCodec(kind)
	if raw == "" {
		if codec == nil {
			return nil, ErrAdminListCursorKey
		}
		return nil, nil
	}
	if codec == nil ||
		len(raw) > 2048 ||
		strings.IndexFunc(raw, unicode.IsSpace) >= 0 {
		return nil, ErrInvalidAdminListCursor
	}
	var cursor adminListCursor
	if err := codec.Decode(raw, &cursor); err != nil {
		return nil, ErrInvalidAdminListCursor
	}
	if cursor.Version != adminListCursorVersion ||
		cursor.Kind != kind ||
		cursor.Organization != scope.OrganizationID ||
		cursor.Project != scope.ProjectID ||
		cursor.Limit != limit ||
		cursor.FilterHash != filterHash ||
		cursor.SortVersion != sortVersion ||
		cursor.ID == "" {
		return nil, ErrInvalidAdminListCursor
	}
	createdAt, err := time.Parse(time.RFC3339Nano, cursor.CreatedAt)
	if err != nil {
		return nil, ErrInvalidAdminListCursor
	}
	return &struct {
		CreatedAt time.Time
		ID        string
	}{CreatedAt: createdAt, ID: cursor.ID}, nil
}

func (s *AdminListService) cursorCodec(kind string) *listcursor.Codec {
	if s == nil {
		return nil
	}
	switch kind {
	case "domain_events":
		return s.eventCursorCodec
	case "policy_decisions":
		return s.decisionCursorCodec
	default:
		return nil
	}
}

func (s *AdminListService) resourceVersions(
	ctx context.Context,
	scope models.ProjectScope,
	subjects []string,
) (map[string]uint64, error) {
	versions := make(map[string]uint64, len(subjects))
	subjectByKey := make(map[string]string, len(subjects))
	keys := make([]string, 0, len(subjects))
	uniqueSubjects := make([]string, 0, len(subjects))
	for _, subject := range subjects {
		subject = strings.TrimSpace(subject)
		if subject == "" {
			continue
		}
		if _, exists := versions[subject]; exists {
			continue
		}
		versions[subject] = 1
		key := adminResourceVersionKey(scope, subject)
		subjectByKey[key] = subject
		keys = append(keys, key)
		uniqueSubjects = append(uniqueSubjects, subject)
	}
	if len(keys) == 0 {
		return versions, nil
	}
	var rows []models.SystemConfig
	if err := s.db.WithContext(ctx).
		Select("key", "version").
		Where("key IN ?", keys).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	found := make(map[string]struct{}, len(rows))
	for i := range rows {
		subject := subjectByKey[rows[i].Key]
		if subject == "" {
			continue
		}
		found[subject] = struct{}{}
		if rows[i].Version > 0 {
			versions[subject] = uint64(rows[i].Version)
		}
	}
	missing := make([]string, 0, len(uniqueSubjects)-len(found))
	for _, subject := range uniqueSubjects {
		if _, ok := found[subject]; !ok {
			missing = append(missing, subject)
		}
	}
	if len(missing) == 0 {
		return versions, nil
	}
	var eventRows []struct {
		Subject string
		Version uint64
	}
	if err := s.db.WithContext(ctx).
		Model(&models.DomainEvent{}).
		Select("subject, MAX(resource_version) AS version").
		Where(
			"organization_id = ? AND project_id = ? AND subject IN ? AND type LIKE ?",
			scope.OrganizationID,
			scope.ProjectID,
			missing,
			"io.chronodesk.admin.%",
		).
		Group("subject").
		Scan(&eventRows).Error; err != nil {
		return nil, err
	}
	for i := range eventRows {
		if eventRows[i].Version > 0 {
			versions[eventRows[i].Subject] = eventRows[i].Version
		}
	}
	return versions, nil
}

func parseAdminListQuery(
	rawQuery string,
	kind adminListQueryKind,
) (parsedAdminListQuery, error) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return parsedAdminListQuery{}, ErrInvalidAdminListQuery
	}
	allowed := map[string]struct{}{}
	result := parsedAdminListQuery{}
	switch kind {
	case adminPageListQuery:
		allowed["page"] = struct{}{}
		allowed["page_size"] = struct{}{}
		allowed["sort_by"] = struct{}{}
		allowed["sort_order"] = struct{}{}
		result.Page = 1
		result.PageSize = DefaultAdminListSize
	case adminCursorListQuery:
		allowed["cursor"] = struct{}{}
		allowed["limit"] = struct{}{}
		result.Limit = DefaultAdminListSize
	default:
		return parsedAdminListQuery{}, ErrInvalidAdminListQuery
	}
	for key, candidates := range values {
		if _, ok := allowed[key]; !ok || len(candidates) != 1 {
			return parsedAdminListQuery{}, ErrInvalidAdminListQuery
		}
		if candidates[0] == "" {
			return parsedAdminListQuery{}, ErrInvalidAdminListQuery
		}
	}
	if kind == adminPageListQuery {
		if raw, ok := singleAdminQueryValue(values, "page"); ok {
			result.Page, err = parsePositiveAdminInteger(raw, math.MaxInt)
			if err != nil {
				return parsedAdminListQuery{}, err
			}
		}
		if raw, ok := singleAdminQueryValue(values, "page_size"); ok {
			result.PageSize, err = parsePositiveAdminInteger(
				raw,
				MaxAdminListSize,
			)
			if err != nil {
				return parsedAdminListQuery{}, err
			}
		}
		if result.Page > math.MaxInt/result.PageSize {
			return parsedAdminListQuery{}, ErrInvalidAdminListQuery
		}
		if raw, ok := singleAdminQueryValue(values, "sort_by"); ok {
			result.SortBy = raw
		}
		if raw, ok := singleAdminQueryValue(values, "sort_order"); ok {
			if raw != "asc" && raw != "desc" {
				return parsedAdminListQuery{}, ErrInvalidAdminListQuery
			}
			result.SortOrder = raw
		}
		return result, nil
	}
	if raw, ok := singleAdminQueryValue(values, "limit"); ok {
		result.Limit, err = parsePositiveAdminInteger(raw, MaxAdminListSize)
		if err != nil {
			return parsedAdminListQuery{}, err
		}
	}
	if raw, ok := singleAdminQueryValue(values, "cursor"); ok {
		if len(raw) > 2048 || strings.IndexFunc(raw, unicode.IsSpace) >= 0 {
			return parsedAdminListQuery{}, ErrInvalidAdminListQuery
		}
		result.Cursor = raw
	}
	return result, nil
}

func singleAdminQueryValue(
	values url.Values,
	name string,
) (string, bool) {
	candidates, ok := values[name]
	if !ok || len(candidates) != 1 {
		return "", false
	}
	return candidates[0], true
}

func parsePositiveAdminInteger(raw string, maximum int) (int, error) {
	if strings.TrimSpace(raw) != raw {
		return 0, ErrInvalidAdminListQuery
	}
	value, err := strconv.ParseUint(raw, 10, 63)
	if err != nil || value == 0 || value > uint64(maximum) {
		return 0, ErrInvalidAdminListQuery
	}
	return int(value), nil
}

func validateAdminListScope(
	ctx context.Context,
	scope models.ProjectScope,
) error {
	trusted, err := services.RequireProjectScope(ctx)
	if err != nil {
		return err
	}
	if trusted != scope {
		return errors.New("administrator list scope does not match trusted operation context")
	}
	return nil
}

func validateAdminPage(
	ctx context.Context,
	scope models.ProjectScope,
	page AdminPageQuery,
) error {
	if err := validateAdminListScope(ctx, scope); err != nil {
		return err
	}
	if page.Page <= 0 ||
		page.PageSize <= 0 ||
		page.PageSize > MaxAdminListSize ||
		page.Page > math.MaxInt/page.PageSize {
		return ErrInvalidAdminListQuery
	}
	return nil
}

func validateAdminCursorQuery(
	ctx context.Context,
	scope models.ProjectScope,
	query AdminCursorQuery,
) error {
	if err := validateAdminListScope(ctx, scope); err != nil {
		return err
	}
	if query.Limit <= 0 || query.Limit > MaxAdminListSize {
		return ErrInvalidAdminListQuery
	}
	return nil
}

func adminPageOffset(page AdminPageQuery) int {
	return (page.Page - 1) * page.PageSize
}

func adminTotalPages(total int64, pageSize int) int {
	if total == 0 {
		return 0
	}
	return int((total + int64(pageSize) - 1) / int64(pageSize))
}

func decodeAdminListScopes(raw datatypes.JSON) ([]string, error) {
	if len(raw) == 0 {
		return []string{}, nil
	}
	var scopes []string
	if err := json.Unmarshal(raw, &scopes); err != nil {
		return nil, fmt.Errorf("decode administrator list scopes: %w", err)
	}
	if scopes == nil {
		return []string{}, nil
	}
	return scopes, nil
}

func adminListFilterHash(kind string) string {
	payload, _ := json.Marshal(struct {
		Kind    string            `json:"kind"`
		Filters map[string]string `json:"filters"`
	}{
		Kind:    kind,
		Filters: map[string]string{},
	})
	sum := sha256.Sum256(payload)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func truncateAdminListText(value string, maximumRunes int) string {
	if maximumRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maximumRunes {
		return value
	}
	return string(runes[:maximumRunes])
}

func adminOutboxDestinationType(value string) string {
	switch strings.TrimSpace(value) {
	case "webhook",
		"event_stream",
		"automation",
		"notification",
		"sla",
		"sla_escalation",
		"attachment_upload",
		"attachment_cleanup",
		"attachment_staging_cleanup",
		"a2a_push",
		"email":
		return strings.TrimSpace(value)
	default:
		return "other"
	}
}

func adminOutboxDestinationLabel(value string) string {
	switch adminOutboxDestinationType(value) {
	case "webhook":
		return "Webhook 回调"
	case "event_stream":
		return "事件流"
	case "automation":
		return "自动化规则"
	case "notification":
		return "系统通知"
	case "sla", "sla_escalation":
		return "SLA 升级"
	case "attachment_upload":
		return "附件入库"
	case "attachment_cleanup":
		return "附件清理"
	case "attachment_staging_cleanup":
		return "附件暂存清理"
	case "a2a_push":
		return "A2A 推送"
	case "email":
		return "邮件投递"
	default:
		return "其他投递目标"
	}
}
