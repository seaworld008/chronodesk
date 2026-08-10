package models

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const (
	ProjectKeyMaxLength = 32
	TeamKeyMaxLength    = 64
	QueueKeyMaxLength   = 64
)

var (
	projectKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_-]{0,31}$`)
	teamKeyPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	queueKeyPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

// ProjectKey is the stable, organization-local identifier used in human and
// machine references. It is intentionally stricter than a display name so it
// can be embedded safely in resource paths and audit subjects.
type ProjectKey string

func (key ProjectKey) IsValid() bool {
	return projectKeyPattern.MatchString(string(key))
}

func (key ProjectKey) Validate() error {
	if key.IsValid() {
		return nil
	}
	return fmt.Errorf(
		"project key must match %s and contain at most %d characters",
		projectKeyPattern.String(),
		ProjectKeyMaxLength,
	)
}

func ValidateProjectKey(key string) error {
	return ProjectKey(key).Validate()
}

// TeamKey is the stable, project-local identifier for a group of human
// workers. It shares the path-safe lowercase alphabet used by queue routing.
type TeamKey string

func (key TeamKey) IsValid() bool {
	return teamKeyPattern.MatchString(string(key))
}

func (key TeamKey) Validate() error {
	if key.IsValid() {
		return nil
	}
	return fmt.Errorf(
		"team key must match %s and contain at most %d characters",
		teamKeyPattern.String(),
		TeamKeyMaxLength,
	)
}

func ValidateTeamKey(key string) error {
	return TeamKey(key).Validate()
}

// QueueKey is the stable, project-local routing identifier. Lowercase keys
// avoid case-folding differences between PostgreSQL, clients and resource URIs.
type QueueKey string

func (key QueueKey) IsValid() bool {
	return queueKeyPattern.MatchString(string(key))
}

func (key QueueKey) Validate() error {
	if key.IsValid() {
		return nil
	}
	return fmt.Errorf(
		"queue key must match %s and contain at most %d characters",
		queueKeyPattern.String(),
		QueueKeyMaxLength,
	)
}

func ValidateQueueKey(key string) error {
	return QueueKey(key).Validate()
}

type OrganizationStatus string

const (
	OrganizationStatusActive   OrganizationStatus = "active"
	OrganizationStatusArchived OrganizationStatus = "archived"
)

type BusinessUnitStatus string

const (
	BusinessUnitStatusActive   BusinessUnitStatus = "active"
	BusinessUnitStatusArchived BusinessUnitStatus = "archived"
)

type ProjectStatus string

const (
	ProjectStatusActive   ProjectStatus = "active"
	ProjectStatusArchived ProjectStatus = "archived"
)

var projectStatusValues = []ProjectStatus{
	ProjectStatusActive,
	ProjectStatusArchived,
}

func ProjectStatusValues() []ProjectStatus {
	return append([]ProjectStatus(nil), projectStatusValues...)
}

func (status ProjectStatus) IsValid() bool {
	for _, candidate := range projectStatusValues {
		if status == candidate {
			return true
		}
	}
	return false
}

type TeamStatus string

const (
	TeamStatusActive   TeamStatus = "active"
	TeamStatusArchived TeamStatus = "archived"
)

type QueueStatus string

const (
	QueueStatusActive   QueueStatus = "active"
	QueueStatusArchived QueueStatus = "archived"
)

type ProjectRole string

const (
	ProjectRoleAdmin     ProjectRole = "project_admin"
	ProjectRoleManager   ProjectRole = "manager"
	ProjectRoleAgent     ProjectRole = "agent"
	ProjectRoleRequester ProjectRole = "requester"
	ProjectRoleObserver  ProjectRole = "observer"
)

func (role ProjectRole) IsValid() bool {
	switch role {
	case ProjectRoleAdmin,
		ProjectRoleManager,
		ProjectRoleAgent,
		ProjectRoleRequester,
		ProjectRoleObserver:
		return true
	default:
		return false
	}
}

type TeamRole string

const (
	TeamRoleLead   TeamRole = "lead"
	TeamRoleMember TeamRole = "member"
)

func (role TeamRole) IsValid() bool {
	return role == TeamRoleLead || role == TeamRoleMember
}

// ProjectScope is the trusted internal scope carried between Adapters and the
// domain layer. Public IDs remain API identifiers; numeric IDs are used only
// after a repository has resolved and authorized them.
type ProjectScope struct {
	OrganizationID uint `json:"organization_id"`
	ProjectID      uint `json:"project_id"`
}

func (scope ProjectScope) Validate() error {
	if scope.OrganizationID == 0 {
		return fmt.Errorf("organization id is required")
	}
	if scope.ProjectID == 0 {
		return fmt.Errorf("project id is required")
	}
	return nil
}

func (scope ProjectScope) IsZero() bool {
	return scope.OrganizationID == 0 && scope.ProjectID == 0
}

// Organization is the top-level ownership boundary. The project-scope upgrade
// creates one explicit organization for a previously unscoped installation.
type Organization struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	PublicID  string    `json:"public_id" gorm:"size:36;not null;uniqueIndex;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	Slug        string             `json:"slug" gorm:"size:64;not null;uniqueIndex"`
	Name        string             `json:"name" gorm:"size:120;not null"`
	Description string             `json:"description" gorm:"size:500"`
	Status      OrganizationStatus `json:"status" gorm:"size:20;not null;default:'active';index;check:chk_organizations_status,status IN ('active','archived')"`
}

func (Organization) TableName() string {
	return "organizations"
}

func (organization *Organization) BeforeCreate(_ *gorm.DB) error {
	return ensureProjectPublicID(&organization.PublicID)
}

type BusinessUnit struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	PublicID  string    `json:"public_id" gorm:"size:36;not null;uniqueIndex;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	OrganizationID uint               `json:"organization_id" gorm:"not null;index;uniqueIndex:idx_business_units_organization_key,priority:1"`
	Organization   Organization       `json:"organization,omitempty" gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
	Key            string             `json:"key" gorm:"size:32;not null;uniqueIndex:idx_business_units_organization_key,priority:2"`
	Name           string             `json:"name" gorm:"size:120;not null"`
	Description    string             `json:"description" gorm:"size:500"`
	Status         BusinessUnitStatus `json:"status" gorm:"size:20;not null;default:'active';index;check:chk_business_units_status,status IN ('active','archived')"`
}

func (BusinessUnit) TableName() string {
	return "business_units"
}

func (unit *BusinessUnit) BeforeCreate(_ *gorm.DB) error {
	return ensureProjectPublicID(&unit.PublicID)
}

type Project struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement;uniqueIndex:idx_projects_scope_id,priority:2"`
	PublicID  string    `json:"public_id" gorm:"size:36;not null;uniqueIndex;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	OrganizationID uint          `json:"organization_id" gorm:"not null;index;uniqueIndex:idx_projects_organization_key,priority:1;uniqueIndex:idx_projects_scope_id,priority:1"`
	Organization   Organization  `json:"organization,omitempty" gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
	BusinessUnitID uint          `json:"business_unit_id" gorm:"not null;index"`
	BusinessUnit   BusinessUnit  `json:"business_unit,omitempty" gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
	Key            ProjectKey    `json:"key" gorm:"size:32;not null;uniqueIndex:idx_projects_organization_key,priority:2;<-:create"`
	Name           string        `json:"name" gorm:"size:120;not null"`
	Description    string        `json:"description" gorm:"size:500"`
	Status         ProjectStatus `json:"status" gorm:"size:20;not null;default:'active';index;check:chk_projects_status,status IS NOT NULL AND (status = 'active' OR status = 'archived')"`
	TicketSequence uint64        `json:"ticket_sequence" gorm:"not null;default:0"`
}

func (Project) TableName() string {
	return "projects"
}

func (project *Project) BeforeCreate(_ *gorm.DB) error {
	if err := project.Key.Validate(); err != nil {
		return err
	}
	return ensureProjectPublicID(&project.PublicID)
}

func (project *Project) BeforeUpdate(tx *gorm.DB) error {
	changedKey, attempted, err := attemptedProjectKeyUpdateValue(tx)
	if err != nil {
		return err
	}
	if !attempted {
		return nil
	}
	return ProjectKey(changedKey).Validate()
}

func (project Project) Scope() ProjectScope {
	return ProjectScope{
		OrganizationID: project.OrganizationID,
		ProjectID:      project.ID,
	}
}

type ProjectMembership struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
	Version   uint64    `json:"version" gorm:"not null;default:1"`

	ProjectID            uint        `json:"project_id" gorm:"not null;index;uniqueIndex:idx_project_memberships_project_user,priority:1"`
	Project              Project     `json:"project,omitempty" gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	UserID               uint        `json:"user_id" gorm:"not null;index;uniqueIndex:idx_project_memberships_project_user,priority:2"`
	User                 User        `json:"user,omitempty" gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	Role                 ProjectRole `json:"role" gorm:"size:20;not null;default:'observer';index;check:chk_project_memberships_role,role IN ('project_admin','manager','agent','requester','observer')"`
	IsActive             bool        `json:"is_active" gorm:"not null;default:true;index"`
	KnowledgeContributor bool        `json:"knowledge_contributor" gorm:"not null;default:false"`
}

func (ProjectMembership) TableName() string {
	return "project_memberships"
}

type Team struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	PublicID  string    `json:"public_id" gorm:"size:36;not null;uniqueIndex;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	ProjectID   uint       `json:"project_id" gorm:"not null;index;uniqueIndex:idx_teams_project_key,priority:1"`
	Project     Project    `json:"project,omitempty" gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	Key         TeamKey    `json:"key" gorm:"size:64;not null;uniqueIndex:idx_teams_project_key,priority:2"`
	Name        string     `json:"name" gorm:"size:120;not null"`
	Description string     `json:"description" gorm:"size:500"`
	Status      TeamStatus `json:"status" gorm:"size:20;not null;default:'active';index;check:chk_teams_status,status IN ('active','archived')"`
}

func (Team) TableName() string {
	return "teams"
}

func (team *Team) BeforeCreate(_ *gorm.DB) error {
	if err := team.Key.Validate(); err != nil {
		return err
	}
	return ensureProjectPublicID(&team.PublicID)
}

func (team *Team) BeforeUpdate(tx *gorm.DB) error {
	changedKey, changed, err := stableKeyUpdateValue(tx)
	if err != nil {
		return err
	}
	if changed {
		return TeamKey(changedKey).Validate()
	}
	if team.Key == "" {
		return nil
	}
	return team.Key.Validate()
}

type TeamMembership struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	TeamID   uint     `json:"team_id" gorm:"not null;index;uniqueIndex:idx_team_memberships_team_user,priority:1"`
	Team     Team     `json:"team,omitempty" gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	UserID   uint     `json:"user_id" gorm:"not null;index;uniqueIndex:idx_team_memberships_team_user,priority:2"`
	User     User     `json:"user,omitempty" gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	Role     TeamRole `json:"role" gorm:"size:20;not null;default:'member';index;check:chk_team_memberships_role,role IN ('lead','member')"`
	IsActive bool     `json:"is_active" gorm:"not null;default:true;index"`
}

func (TeamMembership) TableName() string {
	return "team_memberships"
}

type Queue struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	PublicID  string    `json:"public_id" gorm:"size:36;not null;uniqueIndex;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	ProjectID   uint        `json:"project_id" gorm:"not null;index;uniqueIndex:idx_queues_project_key,priority:1"`
	Project     Project     `json:"project,omitempty" gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	TeamID      *uint       `json:"team_id,omitempty" gorm:"index"`
	Team        *Team       `json:"team,omitempty" gorm:"constraint:OnUpdate:CASCADE,OnDelete:SET NULL"`
	Key         QueueKey    `json:"key" gorm:"size:64;not null;uniqueIndex:idx_queues_project_key,priority:2"`
	Name        string      `json:"name" gorm:"size:120;not null"`
	Description string      `json:"description" gorm:"size:500"`
	Status      QueueStatus `json:"status" gorm:"size:20;not null;default:'active';index;check:chk_queues_status,status IN ('active','archived')"`
	IsDefault   bool        `json:"is_default" gorm:"not null;default:false;index"`
}

func (Queue) TableName() string {
	return "queues"
}

func (queue *Queue) BeforeCreate(_ *gorm.DB) error {
	if err := queue.Key.Validate(); err != nil {
		return err
	}
	return ensureProjectPublicID(&queue.PublicID)
}

func (queue *Queue) BeforeUpdate(tx *gorm.DB) error {
	changedKey, changed, err := stableKeyUpdateValue(tx)
	if err != nil {
		return err
	}
	if changed {
		return QueueKey(changedKey).Validate()
	}
	if queue.Key == "" {
		return nil
	}
	return queue.Key.Validate()
}

type ProjectPrincipalGrant struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	ProjectID          uint             `json:"project_id" gorm:"not null;index;uniqueIndex:idx_project_principal_grants_project_principal,priority:1"`
	Project            Project          `json:"project,omitempty" gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	ServicePrincipalID string           `json:"service_principal_id" gorm:"size:36;not null;index;uniqueIndex:idx_project_principal_grants_project_principal,priority:2"`
	ServicePrincipal   ServicePrincipal `json:"service_principal,omitempty" gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	Role               ProjectRole      `json:"role" gorm:"size:20;not null;default:'observer';index;check:chk_project_principal_grants_role,role IN ('project_admin','manager','agent','requester','observer')"`
	Scopes             datatypes.JSON   `json:"scopes" gorm:"type:jsonb;not null"`
	IsActive           bool             `json:"is_active" gorm:"not null;default:true;index"`
	ExpiresAt          *time.Time       `json:"expires_at,omitempty" gorm:"index"`
}

func (ProjectPrincipalGrant) TableName() string {
	return "project_principal_grants"
}

func (grant ProjectPrincipalGrant) ScopeList() ([]string, error) {
	if len(grant.Scopes) == 0 {
		return []string{}, nil
	}
	var scopes []string
	if err := json.Unmarshal(grant.Scopes, &scopes); err != nil {
		return nil, fmt.Errorf("decode project principal grant scopes: %w", err)
	}
	if scopes == nil {
		return []string{}, nil
	}
	return scopes, nil
}

func (grant ProjectPrincipalGrant) HasScope(scope string) bool {
	scopes, err := grant.ScopeList()
	if err != nil {
		return false
	}
	for _, candidate := range scopes {
		if candidate == scope {
			return true
		}
	}
	return false
}

func ensureProjectPublicID(publicID *string) error {
	if publicID == nil {
		return fmt.Errorf("public id destination is required")
	}
	if strings.TrimSpace(*publicID) == "" {
		generated, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate UUIDv7 public id: %w", err)
		}
		*publicID = generated.String()
		return nil
	}
	parsed, err := uuid.Parse(*publicID)
	if err != nil {
		return fmt.Errorf("public id must be a UUID: %w", err)
	}
	*publicID = parsed.String()
	return nil
}

func stableKeyUpdateValue(tx *gorm.DB) (string, bool, error) {
	if tx == nil || tx.Statement == nil || !tx.Statement.Changed("Key") {
		return "", false, nil
	}
	value := reflect.ValueOf(tx.Statement.Dest)
	for value.IsValid() &&
		(value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return "", true, fmt.Errorf("updated key must be a string")
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return "", true, fmt.Errorf("updated key must be a string")
	}

	switch value.Kind() {
	case reflect.Map:
		for _, candidate := range []string{"key", "Key"} {
			entry := value.MapIndex(reflect.ValueOf(candidate))
			if entry.IsValid() {
				return stableKeyString(entry)
			}
		}
	case reflect.Struct:
		field := value.FieldByName("Key")
		if field.IsValid() {
			return stableKeyString(field)
		}
	}
	return "", true, fmt.Errorf("updated key must be a string")
}

func attemptedProjectKeyUpdateValue(
	tx *gorm.DB,
) (string, bool, error) {
	if tx == nil || tx.Statement == nil {
		return "", false, nil
	}
	value := reflect.ValueOf(tx.Statement.Dest)
	for value.IsValid() &&
		(value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return "", false, nil
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return "", false, nil
	}

	switch value.Kind() {
	case reflect.Map:
		for _, candidate := range []string{"key", "Key"} {
			entry := value.MapIndex(reflect.ValueOf(candidate))
			if entry.IsValid() {
				return stableKeyString(entry)
			}
		}
	case reflect.Struct:
		field := value.FieldByName("Key")
		if field.IsValid() {
			key, attempted, err := stableKeyString(field)
			if err != nil || !attempted || key == "" {
				return "", false, err
			}
			return key, true, nil
		}
	}
	return "", false, nil
}

func stableKeyString(value reflect.Value) (string, bool, error) {
	for value.IsValid() &&
		(value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return "", true, fmt.Errorf("updated key must be a string")
		}
		value = value.Elem()
	}
	if value.IsValid() && value.Kind() == reflect.String {
		return value.String(), true, nil
	}
	return "", true, fmt.Errorf("updated key must be a string")
}
