package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrProjectNotFound           = errors.New("project not found")
	ErrProjectAccessDenied       = errors.New("project access denied")
	ErrProjectInactive           = errors.New("project is not active")
	ErrQueueNotFound             = errors.New("queue not found")
	ErrProjectMembershipNotFound = errors.New("project membership not found")
	ErrProjectMembershipUser     = errors.New("project membership user is unavailable")
	ErrProjectMembershipConflict = errors.New("project membership conflicts with required grant")
	ErrLastProjectAdministrator  = errors.New("project requires an active administrator")
	ErrProjectEventWriter        = errors.New("project event writer is unavailable")
	ErrProjectSequenceConflict   = errors.New("project ticket sequence conflict")
)

// Bootstrap configuration identifiers are stable UUIDv7 values installed by
// the configuration-platform migration. They let the one-time scope upgrade
// bind every existing Ticket to immutable versions before project-specific
// configuration is published.
const (
	defaultRequestTypeIncidentVersionID     = "00000000-0000-7000-8000-000000000101"
	defaultRequestTypeRequestVersionID      = "00000000-0000-7000-8000-000000000102"
	defaultRequestTypeProblemVersionID      = "00000000-0000-7000-8000-000000000103"
	defaultRequestTypeChangeVersionID       = "00000000-0000-7000-8000-000000000104"
	defaultRequestTypeComplaintVersionID    = "00000000-0000-7000-8000-000000000105"
	defaultRequestTypeConsultationVersionID = "00000000-0000-7000-8000-000000000106"
	defaultWorkflowVersionID                = "00000000-0000-7000-8000-000000000201"
)

func defaultRequestTypeVersionID(ticketType models.TicketType) string {
	switch ticketType {
	case models.TicketTypeIncident:
		return defaultRequestTypeIncidentVersionID
	case models.TicketTypeProblem:
		return defaultRequestTypeProblemVersionID
	case models.TicketTypeChange:
		return defaultRequestTypeChangeVersionID
	case models.TicketTypeComplaint:
		return defaultRequestTypeComplaintVersionID
	case models.TicketTypeConsultation:
		return defaultRequestTypeConsultationVersionID
	default:
		return defaultRequestTypeRequestVersionID
	}
}

type ProjectAccess struct {
	Project models.Project      `json:"project"`
	Role    models.ProjectRole  `json:"role"`
	Scope   models.ProjectScope `json:"scope"`
	Scopes  []string            `json:"scopes,omitempty"`
}

type ProjectService struct {
	db     *gorm.DB
	events projectDomainEventAppender
	now    func() time.Time
}

type projectDomainEventAppender interface {
	AppendDomainEventTx(
		context.Context,
		*gorm.DB,
		DomainEventInput,
		[]OutboxTarget,
	) (*models.DomainEvent, error)
}

func NewProjectService(
	db *gorm.DB,
	eventAppenders ...projectDomainEventAppender,
) (*ProjectService, error) {
	if db == nil {
		return nil, errors.New("project database is required")
	}
	if len(eventAppenders) > 1 {
		return nil, errors.New("only one project event writer is supported")
	}
	var events projectDomainEventAppender
	if len(eventAppenders) == 1 {
		if eventAppenders[0] == nil {
			return nil, ErrProjectEventWriter
		}
		events = eventAppenders[0]
	}
	return &ProjectService{db: db, events: events, now: time.Now}, nil
}

type ProjectMembershipView struct {
	ID        uint                `json:"id"`
	ProjectID uint                `json:"project_id"`
	UserID    uint                `json:"user_id"`
	User      *models.UserSummary `json:"user,omitempty"`
	Role      models.ProjectRole  `json:"role"`
	IsActive  bool                `json:"is_active"`
	Version   uint64              `json:"version"`
	CreatedAt time.Time           `json:"created_at"`
	UpdatedAt time.Time           `json:"updated_at"`
}

func projectMembershipView(
	membership *models.ProjectMembership,
) ProjectMembershipView {
	if membership == nil {
		return ProjectMembershipView{}
	}
	return ProjectMembershipView{
		ID:        membership.ID,
		ProjectID: membership.ProjectID,
		UserID:    membership.UserID,
		User:      membership.User.ToSummary(),
		Role:      membership.Role,
		IsActive:  membership.IsActive,
		Version:   membership.Version,
		CreatedAt: membership.CreatedAt,
		UpdatedAt: membership.UpdatedAt,
	}
}

func (service *ProjectService) ListHumanMemberships(
	ctx context.Context,
	scope models.ProjectScope,
) ([]ProjectMembershipView, error) {
	if err := requireMatchingProjectOperation(ctx, scope); err != nil {
		return nil, err
	}
	var memberships []models.ProjectMembership
	if err := service.db.WithContext(ctx).
		Preload("User").
		Where("project_id = ?", scope.ProjectID).
		Order("is_active DESC, role ASC, user_id ASC").
		Find(&memberships).Error; err != nil {
		return nil, fmt.Errorf("list project memberships: %w", err)
	}
	result := make([]ProjectMembershipView, 0, len(memberships))
	for index := range memberships {
		result = append(result, projectMembershipView(&memberships[index]))
	}
	return result, nil
}

type UpsertProjectMembershipInput struct {
	UserID uint
	Role   models.ProjectRole
}

// UpsertHumanMembership creates or reactivates an explicit project grant for
// a human identity. The membership mutation and its CloudEvent/audit-ledger
// entry commit atomically.
func (service *ProjectService) UpsertHumanMembership(
	ctx context.Context,
	scope models.ProjectScope,
	input UpsertProjectMembershipInput,
) (*ProjectMembershipView, error) {
	return service.writeHumanMembership(ctx, scope, input, false)
}

// EnsureHumanMembership creates the requested grant exactly once. An existing
// identical active grant is a no-op; an inactive or differently privileged
// grant fails closed instead of silently changing an operator decision. This
// is used by trusted bootstrap flows that must remain idempotent and auditable.
func (service *ProjectService) EnsureHumanMembership(
	ctx context.Context,
	scope models.ProjectScope,
	input UpsertProjectMembershipInput,
) (*ProjectMembershipView, error) {
	return service.writeHumanMembership(ctx, scope, input, true)
}

func (service *ProjectService) writeHumanMembership(
	ctx context.Context,
	scope models.ProjectScope,
	input UpsertProjectMembershipInput,
	ensureOnly bool,
) (*ProjectMembershipView, error) {
	operation, err := matchingProjectOperation(ctx, scope)
	if err != nil {
		return nil, err
	}
	if input.UserID == 0 || !input.Role.IsValid() {
		return nil, ErrProjectMembershipUser
	}
	if service.events == nil {
		return nil, ErrProjectEventWriter
	}

	var membership models.ProjectMembership
	changed := false
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		var project models.Project
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).
			Where(
				"id = ? AND organization_id = ? AND status = ?",
				scope.ProjectID,
				scope.OrganizationID,
				models.ProjectStatusActive,
			).
			First(&project).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrProjectNotFound
			}
			return fmt.Errorf("load membership project: %w", err)
		}
		var user models.User
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).
			Where("id = ? AND status = ?", input.UserID, models.UserStatusActive).
			First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrProjectMembershipUser
			}
			return fmt.Errorf("load membership user: %w", err)
		}

		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("project_id = ? AND user_id = ?", scope.ProjectID, input.UserID).
			First(&membership)
		switch {
		case errors.Is(query.Error, gorm.ErrRecordNotFound):
			membership = models.ProjectMembership{
				ProjectID: scope.ProjectID,
				UserID:    input.UserID,
				User:      user,
				Role:      input.Role,
				IsActive:  true,
				Version:   1,
			}
			if err := tx.Create(&membership).Error; err != nil {
				return fmt.Errorf("create project membership: %w", err)
			}
			changed = true
		case query.Error != nil:
			return fmt.Errorf("lock project membership: %w", query.Error)
		default:
			if ensureOnly {
				if membership.Role != input.Role || !membership.IsActive {
					return fmt.Errorf(
						"%w: existing role %q active=%t",
						ErrProjectMembershipConflict,
						membership.Role,
						membership.IsActive,
					)
				}
				membership.User = user
				return nil
			}
			if membership.Role == models.ProjectRoleAdmin &&
				input.Role != models.ProjectRoleAdmin &&
				membership.IsActive {
				if err := ensureAnotherProjectAdministrator(
					tx,
					scope.ProjectID,
					membership.ID,
				); err != nil {
					return err
				}
			}
			membership.Role = input.Role
			membership.IsActive = true
			membership.Version++
			if err := tx.Model(&membership).Updates(map[string]any{
				"role":       membership.Role,
				"is_active":  true,
				"version":    membership.Version,
				"updated_at": service.now().UTC(),
			}).Error; err != nil {
				return fmt.Errorf("update project membership: %w", err)
			}
			membership.User = user
			changed = true
		}
		if !changed {
			return nil
		}
		return service.appendMembershipEventTx(
			ctx,
			tx,
			operation,
			&membership,
			"upserted",
		)
	})
	if err != nil {
		return nil, err
	}
	view := projectMembershipView(&membership)
	return &view, nil
}

// DeactivateHumanMembership revokes project access without deleting its audit
// identity. The final active project administrator cannot be revoked.
func (service *ProjectService) DeactivateHumanMembership(
	ctx context.Context,
	scope models.ProjectScope,
	userID uint,
) (*ProjectMembershipView, error) {
	operation, err := matchingProjectOperation(ctx, scope)
	if err != nil {
		return nil, err
	}
	if userID == 0 {
		return nil, ErrProjectMembershipNotFound
	}
	if service.events == nil {
		return nil, ErrProjectEventWriter
	}
	var membership models.ProjectMembership
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("User").
			Where("project_id = ? AND user_id = ?", scope.ProjectID, userID).
			First(&membership).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrProjectMembershipNotFound
			}
			return fmt.Errorf("lock project membership: %w", err)
		}
		if !membership.IsActive {
			return nil
		}
		if membership.Role == models.ProjectRoleAdmin {
			if err := ensureAnotherProjectAdministrator(
				tx,
				scope.ProjectID,
				membership.ID,
			); err != nil {
				return err
			}
		}
		membership.IsActive = false
		membership.Version++
		if err := tx.Model(&membership).Updates(map[string]any{
			"is_active":  false,
			"version":    membership.Version,
			"updated_at": service.now().UTC(),
		}).Error; err != nil {
			return fmt.Errorf("deactivate project membership: %w", err)
		}
		return service.appendMembershipEventTx(
			ctx,
			tx,
			operation,
			&membership,
			"deactivated",
		)
	})
	if err != nil {
		return nil, err
	}
	view := projectMembershipView(&membership)
	return &view, nil
}

func requireMatchingProjectOperation(
	ctx context.Context,
	scope models.ProjectScope,
) error {
	_, err := matchingProjectOperation(ctx, scope)
	return err
}

func matchingProjectOperation(
	ctx context.Context,
	scope models.ProjectScope,
) (OperationContext, error) {
	if err := scope.Validate(); err != nil {
		return OperationContext{}, err
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil || operation.Scope != scope {
		return OperationContext{}, ErrProjectAccessDenied
	}
	return operation, nil
}

func ensureAnotherProjectAdministrator(
	tx *gorm.DB,
	projectID uint,
	excludedMembershipID uint,
) error {
	var count int64
	if err := tx.Model(&models.ProjectMembership{}).
		Where(
			"project_id = ? AND id != ? AND role = ? AND is_active = ?",
			projectID,
			excludedMembershipID,
			models.ProjectRoleAdmin,
			true,
		).
		Count(&count).Error; err != nil {
		return fmt.Errorf("count project administrators: %w", err)
	}
	if count == 0 {
		return ErrLastProjectAdministrator
	}
	return nil
}

func (service *ProjectService) appendMembershipEventTx(
	ctx context.Context,
	tx *gorm.DB,
	operation OperationContext,
	membership *models.ProjectMembership,
	action string,
) error {
	if membership == nil {
		return ErrProjectMembershipNotFound
	}
	_, err := service.events.AppendDomainEventTx(
		ctx,
		tx,
		DomainEventInput{
			Type: fmt.Sprintf(
				"io.chronodesk.project.membership.%s.v1",
				action,
			),
			Subject: fmt.Sprintf(
				"project/%d/memberships/%d",
				membership.ProjectID,
				membership.UserID,
			),
			Data: map[string]any{
				"membership_id": membership.ID,
				"user_id":       membership.UserID,
				"role":          membership.Role,
				"is_active":     membership.IsActive,
			},
			Scope:           operation.Scope,
			TraceID:         operation.TraceID,
			CorrelationID:   operation.CorrelationID,
			Actor:           operation.Actor,
			ResourceVersion: membership.Version,
		},
		nil,
	)
	if err != nil {
		return fmt.Errorf("append project membership event: %w", err)
	}
	return nil
}

// ListHumanProjects returns only explicitly authorized projects, except for a
// platform administrator entering the authorized management overview.
func (service *ProjectService) ListHumanProjects(
	ctx context.Context,
	userID uint,
	platformAdministrator bool,
) ([]ProjectAccess, error) {
	if userID == 0 {
		return nil, ErrProjectAccessDenied
	}
	var rows []struct {
		models.Project
		MembershipRole models.ProjectRole `gorm:"column:membership_role"`
	}
	query := service.db.WithContext(ctx).
		Table("projects").
		Select("projects.*, project_memberships.role AS membership_role").
		Joins(
			"LEFT JOIN project_memberships ON project_memberships.project_id = projects.id AND project_memberships.user_id = ? AND project_memberships.is_active = ?",
			userID,
			true,
		).
		Where("projects.status = ?", models.ProjectStatusActive)
	if !platformAdministrator {
		query = query.Where("project_memberships.id IS NOT NULL")
	}
	if err := query.Order("projects.name ASC, projects.id ASC").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list authorized projects: %w", err)
	}
	result := make([]ProjectAccess, 0, len(rows))
	for _, row := range rows {
		role := row.MembershipRole
		if platformAdministrator {
			role = models.ProjectRoleAdmin
		}
		result = append(result, ProjectAccess{
			Project: row.Project,
			Role:    role,
			Scope:   row.Project.Scope(),
		})
	}
	return result, nil
}

func (service *ProjectService) ResolveHumanProject(
	ctx context.Context,
	projectKey string,
	userID uint,
	platformAdministrator bool,
) (*ProjectAccess, error) {
	if userID == 0 || models.ValidateProjectKey(projectKey) != nil {
		return nil, ErrProjectAccessDenied
	}
	var candidates []struct {
		models.Project
		MembershipRole models.ProjectRole `gorm:"column:membership_role"`
	}
	query := service.db.WithContext(ctx).
		Table("projects").
		Select("projects.*, project_memberships.role AS membership_role").
		Joins(
			"LEFT JOIN project_memberships ON project_memberships.project_id = projects.id AND project_memberships.user_id = ? AND project_memberships.is_active = ?",
			userID,
			true,
		).
		Where("projects.key = ?", projectKey)
	if !platformAdministrator {
		query = query.Where("project_memberships.id IS NOT NULL")
	}
	if err := query.Limit(2).Scan(&candidates).Error; err != nil {
		return nil, fmt.Errorf("resolve authorized project: %w", err)
	}
	if len(candidates) == 0 {
		return nil, ErrProjectAccessDenied
	}
	// Project keys are organization-local. The first private-cloud release is
	// single-organization, but resolution still fails closed if inconsistent
	// data would make an organization implicit.
	if len(candidates) != 1 {
		return nil, ErrProjectAccessDenied
	}
	project := candidates[0].Project
	if project.Status != models.ProjectStatusActive {
		return nil, ErrProjectInactive
	}
	role := candidates[0].MembershipRole
	if platformAdministrator {
		role = models.ProjectRoleAdmin
	}
	if !role.IsValid() {
		return nil, ErrProjectAccessDenied
	}
	return &ProjectAccess{
		Project: project,
		Role:    role,
		Scope:   project.Scope(),
	}, nil
}

func (service *ProjectService) activeHumanMembershipRole(
	ctx context.Context,
	scope models.ProjectScope,
	userID uint,
) (models.ProjectRole, error) {
	if service == nil || service.db == nil || userID == 0 {
		return "", ErrProjectAccessDenied
	}
	if err := scope.Validate(); err != nil {
		return "", ErrProjectAccessDenied
	}
	var membership struct {
		Role models.ProjectRole
	}
	if err := service.db.WithContext(ctx).
		Table("project_memberships").
		Select("project_memberships.role").
		Joins(
			"JOIN projects ON projects.id = project_memberships.project_id",
		).
		Where(
			"project_memberships.project_id = ? AND project_memberships.user_id = ? AND project_memberships.is_active = ?",
			scope.ProjectID,
			userID,
			true,
		).
		Where(
			"projects.organization_id = ? AND projects.status = ?",
			scope.OrganizationID,
			models.ProjectStatusActive,
		).
		Take(&membership).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrProjectAccessDenied
		}
		return "", fmt.Errorf("resolve active project membership: %w", err)
	}
	if !membership.Role.IsValid() {
		return "", ErrProjectAccessDenied
	}
	return membership.Role, nil
}

func (service *ProjectService) ResolvePrincipalProject(
	ctx context.Context,
	projectKey string,
	principalID string,
	requiredScopes ...string,
) (*ProjectAccess, error) {
	if models.ValidateProjectKey(projectKey) != nil ||
		strings.TrimSpace(principalID) == "" {
		return nil, ErrProjectAccessDenied
	}
	var candidates []struct {
		models.Project
		GrantRole      models.ProjectRole `gorm:"column:grant_role"`
		GrantScopes    []byte             `gorm:"column:grant_scopes"`
		GrantExpiresAt *time.Time         `gorm:"column:grant_expires_at"`
	}
	if err := service.db.WithContext(ctx).
		Table("projects").
		Select(
			"projects.*, project_principal_grants.role AS grant_role, project_principal_grants.scopes AS grant_scopes, project_principal_grants.expires_at AS grant_expires_at",
		).
		Joins(
			"JOIN project_principal_grants ON project_principal_grants.project_id = projects.id AND project_principal_grants.service_principal_id = ? AND project_principal_grants.is_active = ?",
			principalID,
			true,
		).
		Where("projects.key = ?", projectKey).
		Limit(2).
		Scan(&candidates).Error; err != nil {
		return nil, fmt.Errorf("resolve authorized principal project: %w", err)
	}
	if len(candidates) != 1 {
		return nil, ErrProjectAccessDenied
	}
	project := candidates[0].Project
	if project.Status != models.ProjectStatusActive {
		return nil, ErrProjectInactive
	}
	if candidates[0].GrantExpiresAt != nil &&
		!candidates[0].GrantExpiresAt.After(service.now().UTC()) {
		return nil, ErrProjectAccessDenied
	}
	if !candidates[0].GrantRole.IsValid() {
		return nil, ErrProjectAccessDenied
	}
	grant := models.ProjectPrincipalGrant{
		Role:      candidates[0].GrantRole,
		Scopes:    candidates[0].GrantScopes,
		ExpiresAt: candidates[0].GrantExpiresAt,
	}
	for _, required := range requiredScopes {
		if !grant.HasScope(required) {
			return nil, ErrProjectAccessDenied
		}
	}
	grantScopes, err := grant.ScopeList()
	if err != nil {
		return nil, ErrProjectAccessDenied
	}
	return &ProjectAccess{
		Project: project,
		Role:    candidates[0].GrantRole,
		Scope:   project.Scope(),
		Scopes:  grantScopes,
	}, nil
}

// GrantPrincipalProject creates the explicit project authorization required
// before a service principal can obtain a project-bound OAuth token. Project
// keys remain organization-local, so the single-organization release fails
// closed if inconsistent data makes a key ambiguous.
func (service *ProjectService) GrantPrincipalProject(
	ctx context.Context,
	projectKey string,
	principalID string,
	role models.ProjectRole,
	scopes []string,
	expiresAt *time.Time,
) (*ProjectAccess, error) {
	if ctx == nil ||
		models.ValidateProjectKey(projectKey) != nil ||
		strings.TrimSpace(principalID) == "" ||
		!role.IsValid() {
		return nil, ErrProjectAccessDenied
	}
	normalizedScopes, err := normalizeAgentScopes(scopes)
	if err != nil {
		return nil, err
	}
	encodedScopes, err := json.Marshal(normalizedScopes)
	if err != nil {
		return nil, fmt.Errorf("encode project principal scopes: %w", err)
	}

	var projects []models.Project
	if err := service.db.WithContext(ctx).
		Where("key = ?", projectKey).
		Limit(2).
		Find(&projects).Error; err != nil {
		return nil, fmt.Errorf("resolve project for principal grant: %w", err)
	}
	if len(projects) == 0 {
		return nil, ErrProjectNotFound
	}
	if len(projects) != 1 {
		return nil, ErrProjectAccessDenied
	}
	project := projects[0]
	if project.Status != models.ProjectStatusActive {
		return nil, ErrProjectInactive
	}

	grant := models.ProjectPrincipalGrant{
		ProjectID:          project.ID,
		ServicePrincipalID: strings.TrimSpace(principalID),
		Role:               role,
		Scopes:             datatypes.JSON(encodedScopes),
		IsActive:           true,
		ExpiresAt:          expiresAt,
	}
	if err := service.db.WithContext(ctx).Create(&grant).Error; err != nil {
		return nil, fmt.Errorf("create project principal grant: %w", err)
	}
	return &ProjectAccess{
		Project: project,
		Role:    role,
		Scope:   project.Scope(),
		Scopes:  normalizedScopes,
	}, nil
}

func (service *ProjectService) ListQueues(
	ctx context.Context,
	scope models.ProjectScope,
) ([]models.Queue, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	var queues []models.Queue
	if err := service.db.WithContext(ctx).
		Where(
			"project_id = ? AND status = ?",
			scope.ProjectID,
			models.QueueStatusActive,
		).
		Order("is_default DESC, name ASC, id ASC").
		Find(&queues).Error; err != nil {
		return nil, fmt.Errorf("list project queues: %w", err)
	}
	return queues, nil
}

func (service *ProjectService) ResolveQueue(
	ctx context.Context,
	scope models.ProjectScope,
	queueKey string,
) (*models.Queue, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if models.ValidateQueueKey(queueKey) != nil {
		return nil, ErrQueueNotFound
	}
	var queue models.Queue
	if err := service.db.WithContext(ctx).
		Where(
			"project_id = ? AND key = ? AND status = ?",
			scope.ProjectID,
			queueKey,
			models.QueueStatusActive,
		).
		First(&queue).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrQueueNotFound
		}
		return nil, fmt.Errorf("resolve project queue: %w", err)
	}
	return &queue, nil
}

// AllocateTicketIdentityTx reserves the next project-local sequence while the
// caller's Ticket and DomainEvent are committed in the same transaction.
func (service *ProjectService) AllocateTicketIdentityTx(
	ctx context.Context,
	tx *gorm.DB,
	scope models.ProjectScope,
) (string, error) {
	if tx == nil {
		return "", errors.New("transaction is required")
	}
	if err := scope.Validate(); err != nil {
		return "", err
	}
	var project models.Project
	if err := tx.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"id = ? AND organization_id = ? AND status = ?",
			scope.ProjectID,
			scope.OrganizationID,
			models.ProjectStatusActive,
		).
		First(&project).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrProjectNotFound
		}
		return "", err
	}
	next := project.TicketSequence + 1
	result := tx.WithContext(ctx).Model(&models.Project{}).
		Where(
			"id = ? AND organization_id = ? AND ticket_sequence = ?",
			project.ID,
			project.OrganizationID,
			project.TicketSequence,
		).
		UpdateColumn("ticket_sequence", next)
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected != 1 {
		return "", ErrProjectSequenceConflict
	}
	return fmt.Sprintf("%s-%d", project.Key, next), nil
}

type CreateProjectInput struct {
	OrganizationID   uint
	BusinessUnitID   uint
	Key              string
	Name             string
	Description      string
	AdministratorID  uint
	DefaultQueueKey  string
	DefaultQueueName string
}

func (service *ProjectService) CreateProject(
	ctx context.Context,
	input CreateProjectInput,
) (*ProjectAccess, error) {
	if input.OrganizationID == 0 ||
		input.BusinessUnitID == 0 ||
		input.AdministratorID == 0 ||
		models.ValidateProjectKey(input.Key) != nil ||
		strings.TrimSpace(input.Name) == "" {
		return nil, errors.New("complete project identity and administrator are required")
	}
	if input.DefaultQueueKey == "" {
		input.DefaultQueueKey = "default"
	}
	if input.DefaultQueueName == "" {
		input.DefaultQueueName = "默认队列"
	}
	if models.ValidateQueueKey(input.DefaultQueueKey) != nil {
		return nil, errors.New("invalid default queue key")
	}
	if service.events == nil {
		return nil, ErrProjectEventWriter
	}
	project := models.Project{
		OrganizationID: input.OrganizationID,
		BusinessUnitID: input.BusinessUnitID,
		Key:            models.ProjectKey(input.Key),
		Name:           strings.TrimSpace(input.Name),
		Description:    strings.TrimSpace(input.Description),
		Status:         models.ProjectStatusActive,
	}
	var queue models.Queue
	var bootstrapRelease models.ConfigurationRelease
	err := transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		var unit models.BusinessUnit
		if err := tx.WithContext(ctx).
			Where(
				"id = ? AND organization_id = ? AND status = ?",
				input.BusinessUnitID,
				input.OrganizationID,
				models.BusinessUnitStatusActive,
			).
			First(&unit).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Create(&project).Error; err != nil {
			return fmt.Errorf("create project: %w", err)
		}
		membership := models.ProjectMembership{
			ProjectID: project.ID,
			UserID:    input.AdministratorID,
			Role:      models.ProjectRoleAdmin,
			IsActive:  true,
		}
		if err := tx.WithContext(ctx).Create(&membership).Error; err != nil {
			return fmt.Errorf("create project administrator membership: %w", err)
		}
		queue = models.Queue{
			ProjectID: project.ID,
			Key:       models.QueueKey(input.DefaultQueueKey),
			Name:      strings.TrimSpace(input.DefaultQueueName),
			Status:    models.QueueStatusActive,
			IsDefault: true,
		}
		if err := tx.WithContext(ctx).Create(&queue).Error; err != nil {
			return fmt.Errorf("create project default queue: %w", err)
		}
		scope := project.Scope()
		if err := scopeddb.ConfigureProjectScopeTransaction(tx, scope); err != nil {
			return fmt.Errorf("configure new project transaction scope: %w", err)
		}
		operation := OperationContext{
			Scope:  scope,
			Actor:  models.HumanActor(input.AdministratorID),
			Source: SourceProtocolHumanREST,
		}
		projectContext, err := WithOperationContext(ctx, operation)
		if err != nil {
			return err
		}
		bootstrapRelease, err = bootstrapProjectConfigurationTx(
			projectContext,
			tx,
			operation,
			service.now().UTC(),
		)
		if err != nil {
			return fmt.Errorf("bootstrap project configuration: %w", err)
		}
		return service.appendProjectCreatedEventTx(
			projectContext,
			tx,
			operation,
			&project,
			&queue,
			&bootstrapRelease,
		)
	})
	if err != nil {
		return nil, err
	}
	return &ProjectAccess{
		Project: project,
		Role:    models.ProjectRoleAdmin,
		Scope:   project.Scope(),
	}, nil
}

func (service *ProjectService) appendProjectCreatedEventTx(
	ctx context.Context,
	tx *gorm.DB,
	operation OperationContext,
	project *models.Project,
	queue *models.Queue,
	release *models.ConfigurationRelease,
) error {
	if service.events == nil {
		return ErrProjectEventWriter
	}
	if project == nil || queue == nil || release == nil {
		return errors.New("project creation event resources are required")
	}
	eventTime := service.now().UTC()
	if release.PublishedAt != nil {
		eventTime = release.PublishedAt.UTC()
	}
	_, err := service.events.AppendDomainEventTx(
		ctx,
		tx,
		DomainEventInput{
			Type:    eventcontract.ProjectCreatedEventType,
			Subject: fmt.Sprintf("project/%d", project.ID),
			Time:    eventTime,
			Data: map[string]any{
				"organization_id":               project.OrganizationID,
				"project_id":                    project.ID,
				"project_public_id":             project.PublicID,
				"project_key":                   project.Key,
				"business_unit_id":              project.BusinessUnitID,
				"administrator_id":              operation.Actor.ID,
				"default_queue_id":              queue.ID,
				"default_queue_key":             queue.Key,
				"configuration_release_id":      release.ID,
				"configuration_release_version": release.Version,
			},
			Scope:                operation.Scope,
			TraceID:              operation.TraceID,
			CorrelationID:        operation.CorrelationID,
			Actor:                operation.Actor,
			ResourceVersion:      1,
			ConfigurationVersion: release.ID,
		},
		nil,
	)
	if err != nil {
		return fmt.Errorf("append project created event: %w", err)
	}
	return nil
}
