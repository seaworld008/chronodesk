package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrProjectNotFound            = errors.New("project not found")
	ErrProjectAccessDenied        = errors.New("project access denied")
	ErrProjectInactive            = errors.New("project is not active")
	ErrQueueNotFound              = errors.New("queue not found")
	ErrProjectMembershipNotFound  = errors.New("project membership not found")
	ErrProjectMembershipUser      = errors.New("project membership user is unavailable")
	ErrProjectMembershipConflict  = errors.New("project membership conflicts with required grant")
	ErrLastProjectAdministrator   = errors.New("project requires an active administrator")
	ErrProjectEventWriter         = errors.New("project event writer is unavailable")
	ErrProjectPublicID            = errors.New("invalid project public id")
	ErrDefaultProjectArchive      = errors.New("default project cannot be archived")
	ErrProjectSequenceConflict    = errors.New("project ticket sequence conflict")
	ErrProjectGovernanceQuery     = errors.New("invalid project governance query")
	ErrDirectoryListQuery         = errors.New("invalid directory list query")
	ErrProjectOrganizationContext = errors.New(
		"authenticated organization context is unavailable",
	)
	ErrBusinessUnitPublicID        = errors.New("invalid business unit public id")
	ErrInitialProjectAdministrator = errors.New(
		"at least one active initial project administrator is required",
	)
)

const projectAccessRevocationOutboxDestinationID = "access-revocation"

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
	Project               models.Project        `json:"project"`
	Role                  models.ProjectRole    `json:"project_role"`
	Scope                 models.ProjectScope   `json:"scope"`
	Scopes                []string              `json:"scopes,omitempty"`
	AuthorizationSnapshot AuthorizationSnapshot `json:"-"`
}

// AuthorizationSnapshot is an internal, immutable comparison value produced
// by live project authorization. It deliberately carries only persisted
// authorization record identities and versions/timestamps; callers still have
// to perform a fresh revalidation after external I/O before comparing the two
// snapshots. Every field is excluded from JSON so authorization control state
// cannot become a browser or machine-protocol contract by accident.
type AuthorizationSnapshot struct {
	Scope               models.ProjectScope `json:"-"`
	ActorType           models.ActorType    `json:"-"`
	ProjectUpdatedAt    time.Time           `json:"-"`
	UserID              uint                `json:"-"`
	UserUpdatedAt       time.Time           `json:"-"`
	MembershipID        uint                `json:"-"`
	MembershipVersion   uint64              `json:"-"`
	MembershipUpdatedAt time.Time           `json:"-"`
	MembershipRole      models.ProjectRole  `json:"-"`
	PrincipalID         string              `json:"-"`
	PrincipalUpdatedAt  time.Time           `json:"-"`
	GrantID             uint                `json:"-"`
	GrantUpdatedAt      time.Time           `json:"-"`
	GrantRole           models.ProjectRole  `json:"-"`
	GrantScopes         []string            `json:"-"`
	CredentialID        string              `json:"-"`
	CredentialUpdatedAt time.Time           `json:"-"`
}

// Matches performs an exact comparison between two successful live
// revalidations. Two empty snapshots never match, and time.Time.Equal avoids
// false mismatches caused only by location or monotonic-clock metadata.
func (snapshot AuthorizationSnapshot) Matches(
	current AuthorizationSnapshot,
) bool {
	if snapshot.Scope.IsZero() ||
		snapshot.ActorType == "" ||
		snapshot.Scope != current.Scope ||
		snapshot.ActorType != current.ActorType {
		return false
	}
	return snapshot.ProjectUpdatedAt.Equal(current.ProjectUpdatedAt) &&
		snapshot.UserID == current.UserID &&
		snapshot.UserUpdatedAt.Equal(current.UserUpdatedAt) &&
		snapshot.MembershipID == current.MembershipID &&
		snapshot.MembershipVersion == current.MembershipVersion &&
		snapshot.MembershipUpdatedAt.Equal(current.MembershipUpdatedAt) &&
		snapshot.MembershipRole == current.MembershipRole &&
		snapshot.PrincipalID == current.PrincipalID &&
		snapshot.PrincipalUpdatedAt.Equal(current.PrincipalUpdatedAt) &&
		snapshot.GrantID == current.GrantID &&
		snapshot.GrantUpdatedAt.Equal(current.GrantUpdatedAt) &&
		snapshot.GrantRole == current.GrantRole &&
		slices.Equal(snapshot.GrantScopes, current.GrantScopes) &&
		snapshot.CredentialID == current.CredentialID &&
		snapshot.CredentialUpdatedAt.Equal(current.CredentialUpdatedAt)
}

func newHumanAuthorizationSnapshot(
	scope models.ProjectScope,
	project models.Project,
	user models.User,
	membership models.ProjectMembership,
) AuthorizationSnapshot {
	return AuthorizationSnapshot{
		Scope:               scope,
		ActorType:           models.ActorTypeHuman,
		ProjectUpdatedAt:    project.UpdatedAt,
		UserID:              user.ID,
		UserUpdatedAt:       user.UpdatedAt,
		MembershipID:        membership.ID,
		MembershipVersion:   membership.Version,
		MembershipUpdatedAt: membership.UpdatedAt,
		MembershipRole:      membership.Role,
	}
}

func newPrincipalAuthorizationSnapshot(
	scope models.ProjectScope,
	project models.Project,
	principal models.ServicePrincipal,
	grant models.ProjectPrincipalGrant,
	grantScopes []string,
	credential *models.AgentCredential,
) AuthorizationSnapshot {
	snapshot := AuthorizationSnapshot{
		Scope:              scope,
		ActorType:          models.ActorTypeServicePrincipal,
		ProjectUpdatedAt:   project.UpdatedAt,
		PrincipalID:        principal.ID,
		PrincipalUpdatedAt: principal.UpdatedAt,
		GrantID:            grant.ID,
		GrantUpdatedAt:     grant.UpdatedAt,
		GrantRole:          grant.Role,
		GrantScopes:        slices.Clone(grantScopes),
	}
	if credential != nil {
		snapshot.CredentialID = credential.ID
		snapshot.CredentialUpdatedAt = credential.UpdatedAt
	}
	return snapshot
}

// AuthorizedProject is the closed Human Web projection. Relational objects
// are not preloaded by project resolution and internal sequence state is not a
// browser contract, so neither may leak through models.Project serialization.
type AuthorizedProject struct {
	ID             uint                 `json:"id"`
	PublicID       string               `json:"public_id"`
	CreatedAt      time.Time            `json:"created_at"`
	UpdatedAt      time.Time            `json:"updated_at"`
	OrganizationID uint                 `json:"organization_id"`
	BusinessUnitID uint                 `json:"business_unit_id"`
	Key            models.ProjectKey    `json:"key"`
	Name           string               `json:"name"`
	Description    string               `json:"description"`
	Status         models.ProjectStatus `json:"status"`
}

// PlatformProjectSummary is the closed platform-control-plane projection.
// Platform governance must be able to discover every Project independently of
// Membership, but that does not mint a ProjectRole or a trusted numeric scope.
type PlatformProjectSummary struct {
	PublicID     string                      `json:"public_id"`
	CreatedAt    time.Time                   `json:"created_at"`
	UpdatedAt    time.Time                   `json:"updated_at"`
	Key          models.ProjectKey           `json:"key"`
	Name         string                      `json:"name"`
	Description  string                      `json:"description"`
	Status       models.ProjectStatus        `json:"status"`
	BusinessUnit PlatformBusinessUnitSummary `json:"business_unit"`
}

// PlatformOrganizationSummary and PlatformBusinessUnitSummary expose public
// governance identities only. Numeric trusted scope never crosses the Human
// Web project-creation boundary.
type PlatformOrganizationSummary struct {
	PublicID string `json:"public_id"`
	Name     string `json:"name"`
}

type PlatformBusinessUnitSummary struct {
	PublicID    string `json:"public_id"`
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type PlatformProjectListRequest struct {
	Page                 int
	PageSize             int
	Search               string
	Status               *models.ProjectStatus
	BusinessUnitPublicID string
	OrderBy              string
	Order                string
}

type PlatformProjectPage struct {
	Items      []PlatformProjectSummary `json:"items"`
	Total      int64                    `json:"total"`
	Page       int                      `json:"page"`
	PageSize   int                      `json:"page_size"`
	TotalPages int                      `json:"total_pages"`
}

// DirectoryPageRequest is the shared, bounded contract for mutable
// administrative directories. Every caller must supply a transport-validated
// page and a closed sort field; each service still validates its own sort
// allowlist before constructing SQL.
type DirectoryPageRequest struct {
	Page      int
	PageSize  int
	SortBy    string
	SortOrder string
}

type DirectoryPage[T any] struct {
	Items      []T   `json:"items"`
	Total      int64 `json:"total"`
	Page       int   `json:"page"`
	PageSize   int   `json:"page_size"`
	TotalPages int   `json:"total_pages"`
}

func validateDirectoryPageRequest(
	request DirectoryPageRequest,
	sortFields map[string]struct{},
) error {
	if request.Page < 1 || request.PageSize < 1 || request.PageSize > 100 ||
		(request.SortOrder != "asc" && request.SortOrder != "desc") {
		return ErrDirectoryListQuery
	}
	if _, ok := sortFields[request.SortBy]; !ok {
		return ErrDirectoryListQuery
	}
	if request.Page > math.MaxInt/request.PageSize {
		return ErrDirectoryListQuery
	}
	return nil
}

func directoryPageOffset(request DirectoryPageRequest) int {
	return (request.Page - 1) * request.PageSize
}

func directoryTotalPages(total int64, pageSize int) int {
	if total == 0 {
		return 0
	}
	return int((total + int64(pageSize) - 1) / int64(pageSize))
}

type ProjectUserSearchRequest struct {
	Page     int
	PageSize int
	Search   string
}

type ProjectUserOption struct {
	ID          uint   `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Avatar      string `json:"avatar"`
}

type ProjectUserOptionPage struct {
	Items      []ProjectUserOption `json:"items"`
	Total      int64               `json:"total"`
	Page       int                 `json:"page"`
	PageSize   int                 `json:"page_size"`
	TotalPages int                 `json:"total_pages"`
}

type PlatformBusinessUnitSearchRequest struct {
	Page     int
	PageSize int
	Search   string
}

type PlatformBusinessUnitPage struct {
	Items      []PlatformBusinessUnitSummary `json:"items"`
	Total      int64                         `json:"total"`
	Page       int                           `json:"page"`
	PageSize   int                           `json:"page_size"`
	TotalPages int                           `json:"total_pages"`
}

type ProjectCreationContextRequest struct {
	Users         ProjectUserSearchRequest
	BusinessUnits PlatformBusinessUnitSearchRequest
}

type ProjectCreationContext struct {
	Organization  PlatformOrganizationSummary `json:"organization"`
	BusinessUnits PlatformBusinessUnitPage    `json:"business_units"`
	Creator       ProjectUserOption           `json:"creator"`
	Users         ProjectUserOptionPage       `json:"users"`
}

func (access ProjectAccess) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Project AuthorizedProject   `json:"project"`
		Role    models.ProjectRole  `json:"project_role"`
		Scope   models.ProjectScope `json:"scope"`
		Scopes  []string            `json:"scopes,omitempty"`
	}{
		Project: AuthorizedProject{
			ID:             access.Project.ID,
			PublicID:       access.Project.PublicID,
			CreatedAt:      access.Project.CreatedAt,
			UpdatedAt:      access.Project.UpdatedAt,
			OrganizationID: access.Project.OrganizationID,
			BusinessUnitID: access.Project.BusinessUnitID,
			Key:            access.Project.Key,
			Name:           access.Project.Name,
			Description:    access.Project.Description,
			Status:         access.Project.Status,
		},
		Role:   access.Role,
		Scope:  access.Scope,
		Scopes: access.Scopes,
	})
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

func (service *ProjectService) ListHumanMembershipPage(
	ctx context.Context,
	scope models.ProjectScope,
	request DirectoryPageRequest,
) (*DirectoryPage[ProjectMembershipView], error) {
	if err := requireMatchingProjectOperation(ctx, scope); err != nil {
		return nil, err
	}
	sortFields := map[string]struct{}{
		"created_at": {},
		"updated_at": {},
		"role":       {},
		"is_active":  {},
		"user_id":    {},
	}
	if err := validateDirectoryPageRequest(request, sortFields); err != nil {
		return nil, err
	}
	query := service.db.WithContext(ctx).
		Model(&models.ProjectMembership{}).
		Where("project_id = ?", scope.ProjectID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count project memberships: %w", err)
	}
	memberships := make(
		[]models.ProjectMembership,
		0,
		request.PageSize,
	)
	if err := query.
		Preload("User").
		Order(projectMembershipDirectoryOrder(request)).
		Offset(directoryPageOffset(request)).
		Limit(request.PageSize).
		Find(&memberships).Error; err != nil {
		return nil, fmt.Errorf("list project memberships: %w", err)
	}
	result := make([]ProjectMembershipView, 0, len(memberships))
	for index := range memberships {
		result = append(result, projectMembershipView(&memberships[index]))
	}
	return &DirectoryPage[ProjectMembershipView]{
		Items:      result,
		Total:      total,
		Page:       request.Page,
		PageSize:   request.PageSize,
		TotalPages: directoryTotalPages(total, request.PageSize),
	}, nil
}

func projectMembershipDirectoryOrder(request DirectoryPageRequest) string {
	if request.SortBy == "is_active" && request.SortOrder == "desc" {
		return "is_active DESC, role ASC, user_id ASC, id ASC"
	}
	direction := "ASC"
	if request.SortOrder == "desc" {
		direction = "DESC"
	}
	column := map[string]string{
		"created_at": "created_at",
		"updated_at": "updated_at",
		"role":       "role",
		"is_active":  "is_active",
		"user_id":    "user_id",
	}[request.SortBy]
	return column + " " + direction + ", id " + direction
}

type UpsertProjectMembershipInput struct {
	UserID uint
	Role   models.ProjectRole
}

// UpsertHumanMembership is the complete authorization-and-command boundary
// for creating, changing or reactivating a Human project grant. Like
// DeactivateHumanMembership, it must be entered before any scoped transaction
// has locked only the requester.
func (service *ProjectService) UpsertHumanMembership(
	ctx context.Context,
	scope models.ProjectScope,
	input UpsertProjectMembershipInput,
) (*ProjectMembershipView, error) {
	if scopeddb.HasTransaction(ctx) {
		return nil, fmt.Errorf(
			"%w: membership administration must own the authorization transaction",
			ErrProjectAccessDenied,
		)
	}
	operation, err := matchingProjectOperation(ctx, scope)
	if err != nil {
		return nil, err
	}
	requesterID, err := humanActorUserID(operation.Actor)
	if err != nil {
		return nil, err
	}
	if input.UserID == 0 || !input.Role.IsValid() {
		return nil, ErrProjectMembershipUser
	}
	if service.events == nil {
		return nil, ErrProjectEventWriter
	}

	var membership *ProjectMembershipView
	err = runProjectOperation(
		ctx,
		service.db,
		func(scopedContext context.Context) error {
			if _, lockErr := service.lockHumanMembershipAdministration(
				service.db.WithContext(scopedContext),
				scope,
				requesterID,
				input.UserID,
			); lockErr != nil {
				return lockErr
			}
			var writeErr error
			membership, writeErr = service.writeHumanMembership(
				scopedContext,
				scope,
				input,
				false,
			)
			return writeErr
		},
	)
	if err != nil {
		return nil, err
	}
	return membership, nil
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
		// Query explicit model columns so a migration connection can call
		// this writer both before and after dropping the legacy users.role
		// column without reusing a SELECT * prepared plan whose result type
		// changed.
		if err := tx.Session(&gorm.Session{QueryFields: true}).
			Clauses(clause.Locking{Strength: "SHARE"}).
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

// DeactivateHumanMembership is the complete authorization-and-command
// boundary for revoking Human project access. Adapters may resolve the public
// Project key before calling it, but must not enter a scoped authorization
// transaction first: this method has to acquire Project, all subject Users and
// all subject Memberships in the stable order below. The final active project
// administrator cannot be revoked.
func (service *ProjectService) DeactivateHumanMembership(
	ctx context.Context,
	scope models.ProjectScope,
	userID uint,
) (*ProjectMembershipView, error) {
	if scopeddb.HasTransaction(ctx) {
		return nil, fmt.Errorf(
			"%w: membership administration must own the authorization transaction",
			ErrProjectAccessDenied,
		)
	}
	operation, err := matchingProjectOperation(ctx, scope)
	if err != nil {
		return nil, err
	}
	requesterID, err := humanActorUserID(operation.Actor)
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
	err = runProjectOperation(ctx, service.db, func(scopedContext context.Context) error {
		locked, lockErr := service.lockHumanMembershipAdministration(
			service.db.WithContext(scopedContext),
			scope,
			requesterID,
			userID,
		)
		if lockErr != nil {
			return lockErr
		}
		return transactionForContext(
			scopedContext,
			service.db,
			func(tx *gorm.DB) error {
				var targetExists bool
				membership, targetExists =
					locked.membershipsByUserID[userID]
				if !targetExists {
					return ErrProjectMembershipNotFound
				}
				membership.User = locked.usersByID[userID]
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
					return fmt.Errorf(
						"deactivate project membership: %w",
						err,
					)
				}
				return service.appendMembershipEventTx(
					scopedContext,
					tx,
					operation,
					&membership,
					"deactivated",
				)
			},
		)
	})
	if err != nil {
		return nil, err
	}
	view := projectMembershipView(&membership)
	return &view, nil
}

type humanMembershipAdministrationLocks struct {
	usersByID           map[uint]models.User
	membershipsByUserID map[uint]models.ProjectMembership
}

func (service *ProjectService) lockHumanMembershipAdministration(
	db *gorm.DB,
	scope models.ProjectScope,
	requesterID uint,
	targetID uint,
) (humanMembershipAdministrationLocks, error) {
	locked := humanMembershipAdministrationLocks{}
	if db == nil || requesterID == 0 || targetID == 0 {
		return locked, ErrProjectAccessDenied
	}
	// Membership administration owns the first authorization lock. Project
	// UPDATE serializes both the last-administrator invariant and a missing
	// target Membership insertion before subject locks are acquired.
	var project models.Project
	if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"id = ? AND organization_id = ? AND status = ?",
			scope.ProjectID,
			scope.OrganizationID,
			models.ProjectStatusActive,
		).
		Take(&project).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return locked, ErrProjectAccessDenied
		}
		return locked, fmt.Errorf(
			"lock membership administration project: %w",
			err,
		)
	}

	userIDs := []uint{requesterID, targetID}
	slices.Sort(userIDs)
	userIDs = slices.Compact(userIDs)
	var users []models.User
	if err := db.
		Unscoped().
		Session(&gorm.Session{QueryFields: true}).
		Clauses(clause.Locking{Strength: "SHARE"}).
		Where("id IN ?", userIDs).
		Order("id ASC").
		Find(&users).Error; err != nil {
		return locked, fmt.Errorf(
			"lock membership administration users: %w",
			err,
		)
	}
	if len(users) != len(userIDs) {
		return locked, ErrProjectMembershipUser
	}
	locked.usersByID = make(map[uint]models.User, len(users))
	for _, user := range users {
		locked.usersByID[user.ID] = user
	}
	requester := locked.usersByID[requesterID]
	if requester.ID == 0 ||
		requester.DeletedAt.Valid ||
		requester.Status != models.UserStatusActive {
		return locked, ErrProjectAccessDenied
	}

	var memberships []models.ProjectMembership
	if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"project_id = ? AND user_id IN ?",
			scope.ProjectID,
			userIDs,
		).
		Order("id ASC").
		Find(&memberships).Error; err != nil {
		return locked, fmt.Errorf(
			"lock membership administration grants: %w",
			err,
		)
	}
	locked.membershipsByUserID = make(
		map[uint]models.ProjectMembership,
		len(memberships),
	)
	for _, membership := range memberships {
		locked.membershipsByUserID[membership.UserID] = membership
	}
	requesterMembership, ok := locked.membershipsByUserID[requesterID]
	if !ok ||
		!requesterMembership.IsActive ||
		requesterMembership.Role != models.ProjectRoleAdmin {
		return locked, ErrProjectAccessDenied
	}
	return locked, nil
}

func humanActorUserID(actor models.ActorRef) (uint, error) {
	if actor.Type != models.ActorTypeHuman {
		return 0, ErrProjectAccessDenied
	}
	parsed, err := strconv.ParseUint(actor.ID, 10, strconv.IntSize)
	if err != nil || parsed == 0 {
		return 0, ErrProjectAccessDenied
	}
	return uint(parsed), nil
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

// ListHumanProjects returns only projects backed by the human's current active
// membership. Platform duties are intentionally absent from this interface.
func (service *ProjectService) ListHumanProjects(
	ctx context.Context,
	userID uint,
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
			"JOIN project_memberships ON project_memberships.project_id = projects.id AND project_memberships.user_id = ? AND project_memberships.is_active = ?",
			userID,
			true,
		).
		Joins(
			"JOIN users ON users.id = project_memberships.user_id AND users.status = ? AND users.deleted_at IS NULL",
			models.UserStatusActive,
		).
		Where("projects.status = ?", models.ProjectStatusActive)
	if err := query.Order("projects.name ASC, projects.id ASC").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list authorized projects: %w", err)
	}
	result := make([]ProjectAccess, 0, len(rows))
	for _, row := range rows {
		if !row.MembershipRole.IsValid() {
			return nil, ErrProjectAccessDenied
		}
		result = append(result, ProjectAccess{
			Project: row.Project,
			Role:    row.MembershipRole,
			Scope:   row.Project.Scope(),
		})
	}
	return result, nil
}

// ListPlatformProjects retains the small service seam used by non-HTTP
// platform authorization tests. Browser governance uses
// ListPlatformProjectPage so every request is bounded.
func (service *ProjectService) ListPlatformProjects(
	ctx context.Context,
	userID uint,
) ([]PlatformProjectSummary, error) {
	page, err := service.ListPlatformProjectPage(
		ctx,
		userID,
		PlatformProjectListRequest{
			Page:     1,
			PageSize: 100,
			OrderBy:  "status",
			Order:    "asc",
		},
	)
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

// ListPlatformProjectPage returns the bounded platform governance inventory
// without resolving ProjectScope or consulting ProjectMembership. The current
// account and the single authenticated Organization context are revalidated
// inside the same read transaction.
func (service *ProjectService) ListPlatformProjectPage(
	ctx context.Context,
	userID uint,
	request PlatformProjectListRequest,
) (*PlatformProjectPage, error) {
	if service == nil ||
		service.db == nil ||
		ctx == nil ||
		userID == 0 ||
		scopeddb.HasTransaction(ctx) {
		return nil, ErrProjectAccessDenied
	}
	normalized, err := normalizePlatformProjectListRequest(request)
	if err != nil {
		return nil, err
	}

	result := &PlatformProjectPage{
		Items:    make([]PlatformProjectSummary, 0),
		Page:     normalized.Page,
		PageSize: normalized.PageSize,
	}
	err = service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, lockErr := lockPlatformProjectAdministrator(
			tx,
			userID,
			"SHARE",
		); lockErr != nil {
			return lockErr
		}
		organization, organizationErr := resolveAuthenticatedOrganizationTx(tx)
		if organizationErr != nil {
			return organizationErr
		}

		query := tx.
			Table("projects").
			Joins(
				"JOIN business_units ON business_units.id = projects.business_unit_id AND business_units.organization_id = projects.organization_id",
			).
			Where("projects.organization_id = ?", organization.ID)
		if normalized.Search != "" {
			pattern := "%" + escapeSQLLike(
				strings.ToLower(normalized.Search),
			) + "%"
			query = query.Where(
				"(LOWER(CAST(projects.key AS TEXT)) LIKE ? ESCAPE '\\' OR LOWER(projects.name) LIKE ? ESCAPE '\\' OR LOWER(projects.description) LIKE ? ESCAPE '\\')",
				pattern,
				pattern,
				pattern,
			)
		}
		if normalized.Status != nil {
			query = query.Where("projects.status = ?", *normalized.Status)
		}
		if normalized.BusinessUnitPublicID != "" {
			query = query.Where(
				"business_units.public_id = ?",
				normalized.BusinessUnitPublicID,
			)
		}
		if err := query.Count(&result.Total).Error; err != nil {
			return fmt.Errorf("count platform projects: %w", err)
		}

		orderColumn := clause.Column{Table: "projects", Name: normalized.OrderBy}
		if normalized.OrderBy == "business_unit" {
			orderColumn = clause.Column{Table: "business_units", Name: "name"}
		}
		descending := normalized.Order == "desc"
		query = query.
			Select(
				"projects.public_id, projects.created_at, projects.updated_at, projects.key, projects.name, projects.description, projects.status, business_units.public_id AS business_unit_public_id, business_units.key AS business_unit_key, business_units.name AS business_unit_name, business_units.description AS business_unit_description",
			).
			Clauses(clause.OrderBy{
				Columns: []clause.OrderByColumn{
					{Column: orderColumn, Desc: descending},
					{
						Column: clause.Column{
							Table: "projects",
							Name:  "id",
						},
						Desc: descending,
					},
				},
			}).
			Offset((normalized.Page - 1) * normalized.PageSize).
			Limit(normalized.PageSize)
		var rows []struct {
			PublicID                string
			CreatedAt               time.Time
			UpdatedAt               time.Time
			Key                     models.ProjectKey
			Name                    string
			Description             string
			Status                  models.ProjectStatus
			BusinessUnitPublicID    string
			BusinessUnitKey         string
			BusinessUnitName        string
			BusinessUnitDescription string
		}
		if err := query.Scan(&rows).Error; err != nil {
			return fmt.Errorf("list platform projects: %w", err)
		}
		for _, row := range rows {
			item := PlatformProjectSummary{
				PublicID:    row.PublicID,
				CreatedAt:   row.CreatedAt,
				UpdatedAt:   row.UpdatedAt,
				Key:         row.Key,
				Name:        row.Name,
				Description: row.Description,
				Status:      row.Status,
				BusinessUnit: PlatformBusinessUnitSummary{
					PublicID:    row.BusinessUnitPublicID,
					Key:         row.BusinessUnitKey,
					Name:        row.BusinessUnitName,
					Description: row.BusinessUnitDescription,
				},
			}
			if err := validatePlatformProjectSummary(item); err != nil {
				return err
			}
			result.Items = append(result.Items, item)
		}
		result.TotalPages = pageCount(result.Total, result.PageSize)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func normalizePlatformProjectListRequest(
	request PlatformProjectListRequest,
) (PlatformProjectListRequest, error) {
	if request.Page < 1 ||
		request.PageSize < 1 ||
		request.PageSize > 100 {
		return PlatformProjectListRequest{}, ErrProjectGovernanceQuery
	}
	request.Search = strings.TrimSpace(request.Search)
	if utf8.RuneCountInString(request.Search) > 100 {
		return PlatformProjectListRequest{}, ErrProjectGovernanceQuery
	}
	if request.Status != nil &&
		*request.Status != models.ProjectStatusActive &&
		*request.Status != models.ProjectStatusArchived {
		return PlatformProjectListRequest{}, ErrProjectGovernanceQuery
	}
	request.BusinessUnitPublicID =
		strings.TrimSpace(request.BusinessUnitPublicID)
	if request.BusinessUnitPublicID != "" &&
		!isCanonicalUUIDv7(request.BusinessUnitPublicID) {
		return PlatformProjectListRequest{}, ErrProjectGovernanceQuery
	}
	if request.OrderBy == "" {
		request.OrderBy = "name"
	}
	switch request.OrderBy {
	case "name", "key", "status", "business_unit", "created_at", "updated_at":
	default:
		return PlatformProjectListRequest{}, ErrProjectGovernanceQuery
	}
	if request.Order == "" {
		request.Order = "asc"
	}
	if request.Order != "asc" && request.Order != "desc" {
		return PlatformProjectListRequest{}, ErrProjectGovernanceQuery
	}
	return request, nil
}

func validatePlatformProjectSummary(project PlatformProjectSummary) error {
	if !isCanonicalUUIDv7(project.PublicID) ||
		!isCanonicalUUIDv7(project.BusinessUnit.PublicID) ||
		!project.Key.IsValid() ||
		(project.Status != models.ProjectStatusActive &&
			project.Status != models.ProjectStatusArchived) {
		return errors.New(
			"platform project inventory contains invalid project identity",
		)
	}
	return nil
}

func (service *ProjectService) GetProjectCreationContext(
	ctx context.Context,
	userID uint,
	request ProjectCreationContextRequest,
) (*ProjectCreationContext, error) {
	if service == nil ||
		service.db == nil ||
		ctx == nil ||
		userID == 0 ||
		scopeddb.HasTransaction(ctx) {
		return nil, ErrProjectAccessDenied
	}
	usersRequest, err := normalizeProjectUserSearchRequest(request.Users)
	if err != nil {
		return nil, err
	}
	businessUnitsRequest, err := normalizeBusinessUnitSearchRequest(
		request.BusinessUnits,
	)
	if err != nil {
		return nil, err
	}
	var result ProjectCreationContext
	err = service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		creator, lockErr := lockPlatformProjectAdministrator(
			tx,
			userID,
			"SHARE",
		)
		if lockErr != nil {
			return lockErr
		}
		organization, organizationErr := resolveAuthenticatedOrganizationTx(tx)
		if organizationErr != nil {
			return organizationErr
		}
		result.Organization = PlatformOrganizationSummary{
			PublicID: organization.PublicID,
			Name:     organization.Name,
		}
		result.Creator = projectUserOption(creator)

		businessUnits, businessUnitErr := searchBusinessUnitsTx(
			tx,
			organization.ID,
			businessUnitsRequest,
			true,
		)
		if businessUnitErr != nil {
			return businessUnitErr
		}
		result.BusinessUnits = *businessUnits
		users, userErr := searchProjectUsersTx(tx, usersRequest)
		if userErr != nil {
			return userErr
		}
		result.Users = *users
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// ListPlatformProjectBusinessUnits returns every current and historical
// Business Unit as a bounded governance filter dimension. Project creation
// deliberately uses GetProjectCreationContext instead, whose options remain
// active-only.
func (service *ProjectService) ListPlatformProjectBusinessUnits(
	ctx context.Context,
	userID uint,
	request PlatformBusinessUnitSearchRequest,
) (*PlatformBusinessUnitPage, error) {
	if service == nil ||
		service.db == nil ||
		ctx == nil ||
		userID == 0 ||
		scopeddb.HasTransaction(ctx) {
		return nil, ErrProjectAccessDenied
	}
	normalized, err := normalizeBusinessUnitSearchRequest(request)
	if err != nil {
		return nil, err
	}
	var result *PlatformBusinessUnitPage
	err = service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, lockErr := lockPlatformProjectAdministrator(
			tx,
			userID,
			"SHARE",
		); lockErr != nil {
			return lockErr
		}
		organization, organizationErr := resolveAuthenticatedOrganizationTx(tx)
		if organizationErr != nil {
			return organizationErr
		}
		var searchErr error
		result, searchErr = searchBusinessUnitsTx(
			tx,
			organization.ID,
			normalized,
			false,
		)
		return searchErr
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func normalizeBusinessUnitSearchRequest(
	request PlatformBusinessUnitSearchRequest,
) (PlatformBusinessUnitSearchRequest, error) {
	if request.Page < 1 ||
		request.PageSize < 1 ||
		request.PageSize > 100 {
		return PlatformBusinessUnitSearchRequest{}, ErrProjectGovernanceQuery
	}
	request.Search = strings.TrimSpace(request.Search)
	if utf8.RuneCountInString(request.Search) > 100 {
		return PlatformBusinessUnitSearchRequest{}, ErrProjectGovernanceQuery
	}
	return request, nil
}

func searchBusinessUnitsTx(
	tx *gorm.DB,
	organizationID uint,
	request PlatformBusinessUnitSearchRequest,
	activeOnly bool,
) (*PlatformBusinessUnitPage, error) {
	query := tx.
		Model(&models.BusinessUnit{}).
		Where("organization_id = ?", organizationID)
	if activeOnly {
		query = query.Where("status = ?", models.BusinessUnitStatusActive)
	}
	if request.Search != "" {
		pattern := "%" + escapeSQLLike(
			strings.ToLower(request.Search),
		) + "%"
		query = query.Where(
			"(LOWER(key) LIKE ? ESCAPE '\\' OR LOWER(name) LIKE ? ESCAPE '\\' OR LOWER(description) LIKE ? ESCAPE '\\')",
			pattern,
			pattern,
			pattern,
		)
	}
	result := &PlatformBusinessUnitPage{
		Items:    make([]PlatformBusinessUnitSummary, 0),
		Page:     request.Page,
		PageSize: request.PageSize,
	}
	if err := query.Count(&result.Total).Error; err != nil {
		return nil, fmt.Errorf("count project creation business units: %w", err)
	}
	var units []models.BusinessUnit
	if err := query.
		Order("LOWER(name) ASC").
		Order("id ASC").
		Offset((request.Page - 1) * request.PageSize).
		Limit(request.PageSize).
		Find(&units).Error; err != nil {
		return nil, fmt.Errorf("list project creation business units: %w", err)
	}
	for _, unit := range units {
		if !isCanonicalUUIDv7(unit.PublicID) {
			return nil, ErrProjectOrganizationContext
		}
		result.Items = append(
			result.Items,
			platformBusinessUnitSummary(unit),
		)
	}
	result.TotalPages = pageCount(result.Total, result.PageSize)
	return result, nil
}

func (service *ProjectService) SearchHumanMembershipCandidates(
	ctx context.Context,
	scope models.ProjectScope,
	request ProjectUserSearchRequest,
) (*ProjectUserOptionPage, error) {
	if err := requireMatchingProjectOperation(ctx, scope); err != nil {
		return nil, err
	}
	normalized, err := normalizeProjectUserSearchRequest(request)
	if err != nil {
		return nil, err
	}
	return searchProjectUsersTx(
		service.db.WithContext(ctx),
		normalized,
	)
}

func normalizeProjectUserSearchRequest(
	request ProjectUserSearchRequest,
) (ProjectUserSearchRequest, error) {
	if request.Page < 1 ||
		request.PageSize < 1 ||
		request.PageSize > 100 {
		return ProjectUserSearchRequest{}, ErrProjectGovernanceQuery
	}
	request.Search = strings.TrimSpace(request.Search)
	if utf8.RuneCountInString(request.Search) > 100 {
		return ProjectUserSearchRequest{}, ErrProjectGovernanceQuery
	}
	return request, nil
}

func searchProjectUsersTx(
	tx *gorm.DB,
	request ProjectUserSearchRequest,
) (*ProjectUserOptionPage, error) {
	query := tx.
		Unscoped().
		Model(&models.User{}).
		Where(
			"deleted_at IS NULL AND status = ?",
			models.UserStatusActive,
		)
	if request.Search != "" {
		pattern := "%" + escapeSQLLike(
			strings.ToLower(request.Search),
		) + "%"
		query = query.Where(
			"(LOWER(username) LIKE ? ESCAPE '\\' OR LOWER(email) LIKE ? ESCAPE '\\' OR LOWER(first_name) LIKE ? ESCAPE '\\' OR LOWER(last_name) LIKE ? ESCAPE '\\' OR LOWER(display_name) LIKE ? ESCAPE '\\')",
			pattern,
			pattern,
			pattern,
			pattern,
			pattern,
		)
	}
	result := &ProjectUserOptionPage{
		Items:    make([]ProjectUserOption, 0),
		Page:     request.Page,
		PageSize: request.PageSize,
	}
	if err := query.Count(&result.Total).Error; err != nil {
		return nil, fmt.Errorf("count project user options: %w", err)
	}
	var users []models.User
	if err := query.
		Order("LOWER(display_name) ASC").
		Order("LOWER(username) ASC").
		Order("id ASC").
		Offset((request.Page - 1) * request.PageSize).
		Limit(request.PageSize).
		Find(&users).Error; err != nil {
		return nil, fmt.Errorf("list project user options: %w", err)
	}
	for _, user := range users {
		result.Items = append(result.Items, projectUserOption(user))
	}
	result.TotalPages = pageCount(result.Total, result.PageSize)
	return result, nil
}

func lockPlatformProjectAdministrator(
	tx *gorm.DB,
	userID uint,
	lockStrength string,
) (models.User, error) {
	if tx == nil || userID == 0 {
		return models.User{}, ErrProjectAccessDenied
	}
	var administrator models.User
	query := tx.
		Unscoped().
		Where(
			"id = ? AND deleted_at IS NULL AND status = ? AND platform_role = ?",
			userID,
			models.UserStatusActive,
			models.PlatformRolePlatformAdmin,
		)
	if lockStrength != "" {
		query = query.Clauses(clause.Locking{Strength: lockStrength})
	}
	if err := query.Take(&administrator).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.User{}, ErrProjectAccessDenied
		}
		return models.User{}, fmt.Errorf(
			"revalidate platform project administrator: %w",
			err,
		)
	}
	return administrator, nil
}

func resolveAuthenticatedOrganizationTx(
	tx *gorm.DB,
) (models.Organization, error) {
	var organizations []models.Organization
	if err := tx.
		Where("status = ?", models.OrganizationStatusActive).
		Order("id ASC").
		Limit(2).
		Find(&organizations).Error; err != nil {
		return models.Organization{}, fmt.Errorf(
			"resolve authenticated organization: %w",
			err,
		)
	}
	if len(organizations) != 1 ||
		!isCanonicalUUIDv7(organizations[0].PublicID) {
		return models.Organization{}, ErrProjectOrganizationContext
	}
	return organizations[0], nil
}

func platformBusinessUnitSummary(
	unit models.BusinessUnit,
) PlatformBusinessUnitSummary {
	return PlatformBusinessUnitSummary{
		PublicID:    unit.PublicID,
		Key:         unit.Key,
		Name:        unit.Name,
		Description: unit.Description,
	}
}

func projectUserOption(user models.User) ProjectUserOption {
	return ProjectUserOption{
		ID:          user.ID,
		Username:    user.Username,
		DisplayName: user.GetFullName(),
		Avatar:      user.Avatar,
	}
}

func pageCount(total int64, pageSize int) int {
	if total <= 0 || pageSize <= 0 {
		return 0
	}
	return int((total + int64(pageSize) - 1) / int64(pageSize))
}

func escapeSQLLike(value string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"%", "\\%",
		"_", "\\_",
	)
	return replacer.Replace(value)
}

func isCanonicalUUIDv7(value string) bool {
	if strings.TrimSpace(value) != value {
		return false
	}
	parsed, err := uuid.Parse(value)
	return err == nil &&
		parsed.Version() == 7 &&
		parsed.Variant() == uuid.RFC4122 &&
		parsed.String() == value
}

func (service *ProjectService) ResolveHumanProject(
	ctx context.Context,
	projectKey string,
	userID uint,
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
			"JOIN project_memberships ON project_memberships.project_id = projects.id AND project_memberships.user_id = ? AND project_memberships.is_active = ?",
			userID,
			true,
		).
		Joins(
			"JOIN users ON users.id = project_memberships.user_id AND users.status = ? AND users.deleted_at IS NULL",
			models.UserStatusActive,
		).
		Where("projects.key = ?", projectKey)
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
	if !role.IsValid() {
		return nil, ErrProjectAccessDenied
	}
	return &ProjectAccess{
		Project: project,
		Role:    role,
		Scope:   project.Scope(),
	}, nil
}

// TicketCreateDatabaseCommand is invoked only after the Project and the
// protocol actor's complete live authorization have been locked in one
// service-owned transaction. It is a services-layer pure database command:
// protocol handlers and external I/O adapters must never be passed as this
// callback.
type TicketCreateDatabaseCommand func(
	context.Context,
	*gorm.DB,
	*ProjectAccess,
) error

// RunTicketCreateDatabaseCommand owns the complete authorization and
// transaction boundary for Human, Agent REST, MCP and A2A ticket intake.
// Ticket creation increments Project sequence state, so Project UPDATE is
// always the first lock. Human then uses User→Membership; machine actors reuse
// Principal→Grant→Credential live revalidation. The callback commits Ticket,
// Event, Outbox, Audit and receipt state in this same short transaction.
func RunTicketCreateDatabaseCommand(
	ctx context.Context,
	db *gorm.DB,
	native *AgentNativeService,
	command TicketCreateDatabaseCommand,
) (*ProjectAccess, error) {
	if db == nil || command == nil {
		return nil, ErrTicketCreateAccessDenied
	}
	if scopeddb.HasTransaction(ctx) {
		return nil, fmt.Errorf(
			"%w: ticket creation must own the authorization transaction",
			ErrTicketCreateAccessDenied,
		)
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return nil, ErrTicketCreateAccessDenied
	}

	var access *ProjectAccess
	err = runProjectOperation(
		ctx,
		db,
		func(scopedContext context.Context) error {
			projectDB := db.WithContext(scopedContext)
			project, lockErr := lockActiveAuthorizationProjectWithStrength(
				projectDB,
				operation.Scope,
				"UPDATE",
			)
			if lockErr != nil {
				return lockErr
			}
			switch operation.Actor.Type {
			case models.ActorTypeHuman:
				userID, actorErr := humanActorUserID(operation.Actor)
				if actorErr != nil {
					return ErrTicketCreateAccessDenied
				}
				access, actorErr =
					lockHumanProjectAuthorizationAfterProject(
						projectDB,
						operation.Scope,
						project,
						userID,
					)
				if actorErr != nil {
					if errors.Is(actorErr, ErrProjectAccessDenied) {
						return ErrTicketCreateAccessDenied
					}
					return actorErr
				}
				if !humanProjectRoleCanCreateTicket(access.Role) {
					return ErrTicketCreateAccessDenied
				}
			case models.ActorTypeServicePrincipal:
				if native == nil {
					return errors.New(
						"Agent ticket authorization is unavailable",
					)
				}
				var actorErr error
				access, actorErr =
					native.revalidatePrincipalProjectOperationAfterProject(
						scopedContext,
						project,
						models.ScopeTicketsCreate,
					)
				if actorErr != nil {
					return actorErr
				}
			default:
				return ErrInvalidActor
			}
			return transactionForContext(
				scopedContext,
				db,
				func(tx *gorm.DB) error {
					return command(scopedContext, tx, access)
				},
			)
		},
	)
	if err != nil {
		return nil, err
	}
	return access, nil
}

// RunHumanTicketCreateDatabaseCommand is the Human adapter used by
// TicketService. The actor ID must exactly match the trusted OperationContext;
// machine adapters call RunTicketCreateDatabaseCommand directly.
func (service *ProjectService) RunHumanTicketCreateDatabaseCommand(
	ctx context.Context,
	scope models.ProjectScope,
	userID uint,
	command TicketCreateDatabaseCommand,
) (*ProjectAccess, error) {
	if service == nil || service.db == nil || userID == 0 || command == nil {
		return nil, ErrTicketCreateAccessDenied
	}
	if scopeddb.HasTransaction(ctx) {
		return nil, fmt.Errorf(
			"%w: ticket creation must own the authorization transaction",
			ErrTicketCreateAccessDenied,
		)
	}
	operation, err := matchingProjectOperation(ctx, scope)
	if err != nil || operation.Actor != models.HumanActor(userID) {
		return nil, ErrTicketCreateAccessDenied
	}
	return RunTicketCreateDatabaseCommand(
		ctx,
		service.db,
		nil,
		command,
	)
}

// RevalidateHumanProjectAccess reloads the complete human authorization
// inside the exact single-project transaction that will execute the request.
// A successful pre-resolution is only allowed to choose the trusted RLS
// scope; it never remains an authorization fact after the transaction opens.
func (service *ProjectService) RevalidateHumanProjectAccess(
	ctx context.Context,
	scope models.ProjectScope,
	userID uint,
) (*ProjectAccess, error) {
	if service == nil || service.db == nil || userID == 0 {
		return nil, ErrProjectAccessDenied
	}
	if err := requireExactProjectAuthorizationTransaction(ctx, scope); err != nil {
		return nil, err
	}
	return lockHumanProjectAuthorization(
		service.db.WithContext(ctx),
		scope,
		userID,
		"SHARE",
	)
}

func lockHumanProjectAuthorization(
	db *gorm.DB,
	scope models.ProjectScope,
	userID uint,
	projectLockStrength string,
) (*ProjectAccess, error) {
	project, err := lockActiveAuthorizationProjectWithStrength(
		db,
		scope,
		projectLockStrength,
	)
	if err != nil {
		return nil, err
	}
	return lockHumanProjectAuthorizationAfterProject(
		db,
		scope,
		project,
		userID,
	)
}

func lockHumanProjectAuthorizationAfterProject(
	db *gorm.DB,
	scope models.ProjectScope,
	project models.Project,
	userID uint,
) (*ProjectAccess, error) {
	var user models.User
	err := db.Clauses(clause.Locking{Strength: "SHARE"}).
		Where(
			"id = ? AND status = ?",
			userID,
			models.UserStatusActive,
		).
		Take(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProjectAccessDenied
	}
	if err != nil {
		return nil, fmt.Errorf("lock human project identity: %w", err)
	}
	var membership models.ProjectMembership
	err = db.Clauses(clause.Locking{Strength: "SHARE"}).
		Where(
			"project_id = ? AND user_id = ? AND is_active = ?",
			scope.ProjectID,
			userID,
			true,
		).
		Take(&membership).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProjectAccessDenied
	}
	if err != nil {
		return nil, fmt.Errorf("lock human project membership: %w", err)
	}
	if !membership.Role.IsValid() {
		return nil, ErrProjectAccessDenied
	}
	return &ProjectAccess{
		Project: project,
		Role:    membership.Role,
		Scope:   scope,
		AuthorizationSnapshot: newHumanAuthorizationSnapshot(
			scope,
			project,
			user,
			membership,
		),
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
		Joins(
			"JOIN service_principals ON service_principals.id = project_principal_grants.service_principal_id AND service_principals.status = ? AND service_principals.emergency_disabled = ? AND service_principals.deleted_at IS NULL",
			models.ServicePrincipalStatusActive,
			false,
		).
		Where(
			"service_principals.expires_at IS NULL OR service_principals.expires_at > ?",
			service.now().UTC(),
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

// RevalidatePrincipalProjectAccess is the service-principal counterpart to
// RevalidateHumanProjectAccess. Principal state, project state, the live
// Grant, its expiry and all required project scopes are read from the same
// transaction that will execute the protocol callback.
func (service *ProjectService) RevalidatePrincipalProjectAccess(
	ctx context.Context,
	scope models.ProjectScope,
	principalID string,
	requiredScopes ...string,
) (*ProjectAccess, error) {
	if service == nil || service.db == nil ||
		strings.TrimSpace(principalID) == "" {
		return nil, ErrProjectAccessDenied
	}
	if err := requireExactProjectAuthorizationTransaction(ctx, scope); err != nil {
		return nil, err
	}
	db := service.db.WithContext(ctx)
	project, err := lockActiveAuthorizationProject(db, scope)
	if err != nil {
		return nil, err
	}
	var principal models.ServicePrincipal
	err = db.Clauses(clause.Locking{Strength: "SHARE"}).
		Where("id = ?", strings.TrimSpace(principalID)).
		Take(&principal).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProjectAccessDenied
	}
	if err != nil {
		return nil, fmt.Errorf("lock project principal: %w", err)
	}
	var grant models.ProjectPrincipalGrant
	err = db.Clauses(clause.Locking{Strength: "SHARE"}).
		Where(
			"project_id = ? AND service_principal_id = ? AND is_active = ?",
			scope.ProjectID,
			strings.TrimSpace(principalID),
			true,
		).
		Take(&grant).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProjectAccessDenied
	}
	if err != nil {
		return nil, fmt.Errorf("lock project principal grant: %w", err)
	}
	now := service.now().UTC()
	if principal.DeletedAt != nil ||
		principal.Status != models.ServicePrincipalStatusActive ||
		principal.EmergencyDisabled ||
		(principal.ExpiresAt != nil &&
			!principal.ExpiresAt.After(now)) {
		return nil, ErrProjectAccessDenied
	}
	if !grant.Role.IsValid() ||
		(grant.ExpiresAt != nil && !grant.ExpiresAt.After(now)) {
		return nil, ErrProjectAccessDenied
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
		Role:    grant.Role,
		Scope:   scope,
		Scopes:  grantScopes,
		AuthorizationSnapshot: newPrincipalAuthorizationSnapshot(
			scope,
			project,
			principal,
			grant,
			grantScopes,
			nil,
		),
	}, nil
}

func lockActiveAuthorizationProject(
	db *gorm.DB,
	scope models.ProjectScope,
) (models.Project, error) {
	return lockActiveAuthorizationProjectWithStrength(db, scope, "SHARE")
}

func lockActiveAuthorizationProjectWithStrength(
	db *gorm.DB,
	scope models.ProjectScope,
	strength string,
) (models.Project, error) {
	var project models.Project
	err := db.Clauses(clause.Locking{Strength: strength}).
		Where(
			"id = ? AND organization_id = ? AND status = ?",
			scope.ProjectID,
			scope.OrganizationID,
			models.ProjectStatusActive,
		).
		Take(&project).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.Project{}, ErrProjectAccessDenied
	}
	if err != nil {
		return models.Project{}, fmt.Errorf(
			"lock active authorization project: %w",
			err,
		)
	}
	if project.Scope() != scope {
		return models.Project{}, ErrProjectAccessDenied
	}
	return project, nil
}

func requireExactProjectAuthorizationTransaction(
	ctx context.Context,
	scope models.ProjectScope,
) error {
	reusable, err := scopeddb.CanReuseProjectScopeTransaction(ctx, scope)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrProjectAccessDenied, err)
	}
	if !reusable {
		return fmt.Errorf(
			"%w: live authorization requires the project transaction",
			ErrProjectAccessDenied,
		)
	}
	return nil
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

func (service *ProjectService) ListQueuePage(
	ctx context.Context,
	scope models.ProjectScope,
	request DirectoryPageRequest,
) (*DirectoryPage[models.Queue], error) {
	if err := requireMatchingProjectOperation(ctx, scope); err != nil {
		return nil, err
	}
	sortFields := map[string]struct{}{
		"created_at": {},
		"updated_at": {},
		"name":       {},
		"key":        {},
		"is_default": {},
	}
	if err := validateDirectoryPageRequest(request, sortFields); err != nil {
		return nil, err
	}
	query := service.db.WithContext(ctx).
		Model(&models.Queue{}).
		Where(
			"project_id = ? AND status = ?",
			scope.ProjectID,
			models.QueueStatusActive,
		)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count project queues: %w", err)
	}
	queues := make([]models.Queue, 0, request.PageSize)
	if err := query.
		Order(projectQueueDirectoryOrder(request)).
		Offset(directoryPageOffset(request)).
		Limit(request.PageSize).
		Find(&queues).Error; err != nil {
		return nil, fmt.Errorf("list project queues: %w", err)
	}
	return &DirectoryPage[models.Queue]{
		Items:      queues,
		Total:      total,
		Page:       request.Page,
		PageSize:   request.PageSize,
		TotalPages: directoryTotalPages(total, request.PageSize),
	}, nil
}

func projectQueueDirectoryOrder(request DirectoryPageRequest) string {
	if request.SortBy == "is_default" && request.SortOrder == "desc" {
		return "is_default DESC, name ASC, id ASC"
	}
	direction := "ASC"
	if request.SortOrder == "desc" {
		direction = "DESC"
	}
	column := map[string]string{
		"created_at": "created_at",
		"updated_at": "updated_at",
		"name":       "name",
		"key":        "\"key\"",
		"is_default": "is_default",
	}[request.SortBy]
	return column + " " + direction + ", id " + direction
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
	ActorUserID             uint
	BusinessUnitPublicID    string
	Key                     string
	Name                    string
	Description             string
	InitialAdministratorIDs []uint
	DefaultQueueKey         string
	DefaultQueueName        string
}

func (service *ProjectService) CreateProject(
	ctx context.Context,
	input CreateProjectInput,
) (*models.Project, error) {
	input.BusinessUnitPublicID = strings.TrimSpace(
		input.BusinessUnitPublicID,
	)
	if input.ActorUserID == 0 ||
		!isCanonicalUUIDv7(input.BusinessUnitPublicID) ||
		models.ValidateProjectKey(input.Key) != nil ||
		strings.TrimSpace(input.Name) == "" {
		return nil, errors.New(
			"complete public project identity and creator are required",
		)
	}
	administratorIDs, err := normalizeInitialProjectAdministrators(
		input.InitialAdministratorIDs,
	)
	if err != nil {
		return nil, err
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
	var project models.Project
	var queue models.Queue
	var bootstrapRelease models.ConfigurationRelease
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		creator, creatorErr := lockPlatformProjectAdministrator(
			tx.WithContext(ctx),
			input.ActorUserID,
			"SHARE",
		)
		if creatorErr != nil {
			return creatorErr
		}
		organization, organizationErr := resolveAuthenticatedOrganizationTx(
			tx.WithContext(ctx),
		)
		if organizationErr != nil {
			return organizationErr
		}
		var unit models.BusinessUnit
		if err := tx.WithContext(ctx).
			Where(
				"public_id = ? AND organization_id = ? AND status = ?",
				input.BusinessUnitPublicID,
				organization.ID,
				models.BusinessUnitStatusActive,
			).
			First(&unit).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrBusinessUnitPublicID
			}
			return fmt.Errorf("resolve project business unit: %w", err)
		}
		if err := lockInitialProjectAdministrators(
			tx.WithContext(ctx),
			administratorIDs,
		); err != nil {
			return err
		}
		project = models.Project{
			OrganizationID: organization.ID,
			BusinessUnitID: unit.ID,
			BusinessUnit:   unit,
			Key:            models.ProjectKey(input.Key),
			Name:           strings.TrimSpace(input.Name),
			Description:    strings.TrimSpace(input.Description),
			Status:         models.ProjectStatusActive,
		}
		if err := tx.WithContext(ctx).Create(&project).Error; err != nil {
			return fmt.Errorf("create project: %w", err)
		}
		memberships := make(
			[]models.ProjectMembership,
			0,
			len(administratorIDs),
		)
		for _, administratorID := range administratorIDs {
			memberships = append(memberships, models.ProjectMembership{
				ProjectID: project.ID,
				UserID:    administratorID,
				Role:      models.ProjectRoleAdmin,
				IsActive:  true,
			})
		}
		if err := tx.WithContext(ctx).
			Create(&memberships).Error; err != nil {
			return fmt.Errorf(
				"create initial project administrator memberships: %w",
				err,
			)
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
			Actor:  models.HumanActor(creator.ID),
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
			administratorIDs,
		)
	})
	if err != nil {
		return nil, err
	}
	return &project, nil
}

func normalizeInitialProjectAdministrators(
	userIDs []uint,
) ([]uint, error) {
	if len(userIDs) == 0 || len(userIDs) > 100 {
		return nil, ErrInitialProjectAdministrator
	}
	unique := make(map[uint]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID == 0 {
			return nil, ErrInitialProjectAdministrator
		}
		unique[userID] = struct{}{}
	}
	result := make([]uint, 0, len(unique))
	for userID := range unique {
		result = append(result, userID)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left] < result[right]
	})
	return result, nil
}

func lockInitialProjectAdministrators(
	tx *gorm.DB,
	userIDs []uint,
) error {
	var administrators []struct {
		ID uint
	}
	if err := tx.
		Unscoped().
		Model(&models.User{}).
		Clauses(clause.Locking{Strength: "SHARE"}).
		Where(
			"id IN ? AND deleted_at IS NULL AND status = ?",
			userIDs,
			models.UserStatusActive,
		).
		Order("id ASC").
		Find(&administrators).Error; err != nil {
		return fmt.Errorf("lock initial project administrators: %w", err)
	}
	if len(administrators) != len(userIDs) {
		return ErrInitialProjectAdministrator
	}
	for index, administrator := range administrators {
		if administrator.ID != userIDs[index] {
			return ErrInitialProjectAdministrator
		}
	}
	return nil
}

// ArchiveProject is the platform control-plane command that revokes every
// current project access path. It owns the Project row lock and commits the
// archived state, immutable revocation event, Outbox delivery and audit entry
// atomically. The event_stream target is deliberately narrow: archived
// projects must not resume generic business-event fanout.
func (service *ProjectService) ArchiveProject(
	ctx context.Context,
	projectPublicID string,
	actor models.ActorRef,
) (*models.Project, error) {
	publicID := strings.TrimSpace(projectPublicID)
	parsed, err := uuid.Parse(publicID)
	if publicID != projectPublicID ||
		err != nil ||
		parsed.Version() != 7 ||
		parsed.Variant() != uuid.RFC4122 ||
		parsed.String() != publicID {
		return nil, ErrProjectPublicID
	}
	if actor.Type != models.ActorTypeHuman || actor.Validate() != nil {
		return nil, ErrProjectAccessDenied
	}
	actorIDValue, err := strconv.ParseUint(actor.ID, 10, strconv.IntSize)
	if err != nil || actorIDValue == 0 {
		return nil, ErrProjectAccessDenied
	}
	actorID := uint(actorIDValue)
	if service.events == nil {
		return nil, ErrProjectEventWriter
	}

	var project models.Project
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("public_id = ?", publicID).
			Take(&project).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrProjectNotFound
			}
			return fmt.Errorf("lock project for archive: %w", err)
		}
		if _, err := lockPlatformProjectAdministrator(
			tx.WithContext(ctx),
			actorID,
			"SHARE",
		); err != nil {
			return err
		}
		organization, err := resolveAuthenticatedOrganizationTx(
			tx.WithContext(ctx),
		)
		if err != nil {
			return err
		}
		if project.OrganizationID != organization.ID {
			return ErrProjectNotFound
		}
		var unit models.BusinessUnit
		if err := tx.WithContext(ctx).
			Where(
				"id = ? AND organization_id = ?",
				project.BusinessUnitID,
				project.OrganizationID,
			).
			Take(&unit).Error; err != nil {
			return fmt.Errorf("load archived project business unit: %w", err)
		}
		project.BusinessUnit = unit
		if project.Key == models.ProjectKey("DEFAULT") {
			return ErrDefaultProjectArchive
		}
		if project.Status == models.ProjectStatusArchived {
			return nil
		}
		if project.Status != models.ProjectStatusActive {
			return ErrProjectInactive
		}

		scope := project.Scope()
		if err := scopeddb.ConfigureProjectScopeTransaction(tx, scope); err != nil {
			return fmt.Errorf("configure project archive scope: %w", err)
		}
		operation := OperationContext{
			Scope:  scope,
			Actor:  actor,
			Source: SourceProtocolHumanREST,
		}
		projectContext, err := WithOperationContext(ctx, operation)
		if err != nil {
			return err
		}
		now := service.now().UTC()
		update := tx.WithContext(projectContext).
			Model(&models.Project{}).
			Where(
				"id = ? AND organization_id = ? AND status = ?",
				project.ID,
				project.OrganizationID,
				models.ProjectStatusActive,
			).
			Updates(map[string]any{
				"status":     models.ProjectStatusArchived,
				"updated_at": now,
			})
		if update.Error != nil {
			return fmt.Errorf("archive project: %w", update.Error)
		}
		if update.RowsAffected != 1 {
			return ErrProjectInactive
		}
		project.Status = models.ProjectStatusArchived
		project.UpdatedAt = now
		_, err = service.events.AppendDomainEventTx(
			projectContext,
			tx,
			DomainEventInput{
				Type:    ProjectAccessRevokedEventType,
				Subject: fmt.Sprintf("project/%d", project.ID),
				Time:    now,
				Data: map[string]any{
					"organization_id":   project.OrganizationID,
					"project_id":        project.ID,
					"project_public_id": project.PublicID,
					"project_key":       project.Key,
					"status":            project.Status,
					"reason":            "project_archived",
				},
				Scope:           scope,
				Actor:           actor,
				ResourceVersion: 2,
			},
			[]OutboxTarget{{
				Type:        "event_stream",
				ID:          projectAccessRevocationOutboxDestinationID,
				MaxAttempts: 8,
			}},
		)
		if err != nil {
			return fmt.Errorf(
				"append project access-revoked event: %w",
				err,
			)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &project, nil
}

func (service *ProjectService) appendProjectCreatedEventTx(
	ctx context.Context,
	tx *gorm.DB,
	operation OperationContext,
	project *models.Project,
	queue *models.Queue,
	release *models.ConfigurationRelease,
	initialAdministratorIDs []uint,
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
				"organization_id":                project.OrganizationID,
				"project_id":                     project.ID,
				"project_public_id":              project.PublicID,
				"project_key":                    project.Key,
				"business_unit_id":               project.BusinessUnitID,
				"creator_user_id":                operation.Actor.ID,
				"initial_project_admin_user_ids": initialAdministratorIDs,
				"default_queue_id":               queue.ID,
				"default_queue_key":              queue.Key,
				"configuration_release_id":       release.ID,
				"configuration_release_version":  release.Version,
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
