package models

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const JSONSchemaDraft202012 = "https://json-schema.org/draft/2020-12/schema"

var (
	configurationKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)
	semanticVersionPattern  = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	hexDigestPattern        = regexp.MustCompile(`^[0-9a-f]{64}$`)
	typedFieldPattern       = regexp.MustCompile(`^[a-z][a-z0-9_]*(?:\.[a-z][a-z0-9_]*){0,3}$`)
	clockPattern            = regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`)
	datePattern             = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
)

var ErrPublishedConfigurationImmutable = errors.New("published configuration is immutable")

type ConfigurationVersionStatus string

const (
	ConfigurationStatusDraft     ConfigurationVersionStatus = "draft"
	ConfigurationStatusSimulated ConfigurationVersionStatus = "simulated"
	ConfigurationStatusPublished ConfigurationVersionStatus = "published"
)

func (status ConfigurationVersionStatus) IsValid() bool {
	switch status {
	case ConfigurationStatusDraft,
		ConfigurationStatusSimulated,
		ConfigurationStatusPublished:
		return true
	default:
		return false
	}
}

type WorkClass string

const (
	WorkClassIncident     WorkClass = "incident"
	WorkClassRequest      WorkClass = "request"
	WorkClassProblem      WorkClass = "problem"
	WorkClassChange       WorkClass = "change"
	WorkClassComplaint    WorkClass = "complaint"
	WorkClassConsultation WorkClass = "consultation"
)

func (workClass WorkClass) IsValid() bool {
	switch workClass {
	case WorkClassIncident,
		WorkClassRequest,
		WorkClassProblem,
		WorkClassChange,
		WorkClassComplaint,
		WorkClassConsultation:
		return true
	default:
		return false
	}
}

// LifecycleCategory is the single reporting and automation projection shared
// by every project-specific workflow.
type LifecycleCategory string

const (
	LifecycleCategoryNew       LifecycleCategory = "new"
	LifecycleCategoryActive    LifecycleCategory = "active"
	LifecycleCategoryWaiting   LifecycleCategory = "waiting"
	LifecycleCategoryResolved  LifecycleCategory = "resolved"
	LifecycleCategoryClosed    LifecycleCategory = "closed"
	LifecycleCategoryCancelled LifecycleCategory = "cancelled"
)

func (category LifecycleCategory) IsValid() bool {
	switch category {
	case LifecycleCategoryNew,
		LifecycleCategoryActive,
		LifecycleCategoryWaiting,
		LifecycleCategoryResolved,
		LifecycleCategoryClosed,
		LifecycleCategoryCancelled:
		return true
	default:
		return false
	}
}

func DefaultLifecycleCategory(status TicketStatus) (LifecycleCategory, error) {
	switch status {
	case TicketStatusOpen:
		return LifecycleCategoryNew, nil
	case TicketStatusInProgress:
		return LifecycleCategoryActive, nil
	case TicketStatusPending:
		return LifecycleCategoryWaiting, nil
	case TicketStatusResolved:
		return LifecycleCategoryResolved, nil
	case TicketStatusClosed:
		return LifecycleCategoryClosed, nil
	case TicketStatusCancelled:
		return LifecycleCategoryCancelled, nil
	default:
		return "", fmt.Errorf("ticket status %q has no lifecycle category", status)
	}
}

type RequestTypeVersion struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	OrganizationID uint                       `json:"organization_id" gorm:"not null;index;uniqueIndex:idx_request_type_project_key_version,priority:1"`
	ProjectID      uint                       `json:"project_id" gorm:"not null;index;uniqueIndex:idx_request_type_project_key_version,priority:2"`
	Key            string                     `json:"key" gorm:"size:64;not null;uniqueIndex:idx_request_type_project_key_version,priority:3"`
	Version        uint64                     `json:"version" gorm:"not null;uniqueIndex:idx_request_type_project_key_version,priority:4"`
	Status         ConfigurationVersionStatus `json:"status" gorm:"size:20;not null;default:'draft';index"`
	Name           string                     `json:"name" gorm:"size:120;not null"`
	Description    string                     `json:"description" gorm:"size:500"`
	WorkClass      WorkClass                  `json:"work_class" gorm:"size:32;not null;index"`
	JSONSchema     datatypes.JSON             `json:"json_schema" gorm:"type:jsonb;not null"`
	UISchema       datatypes.JSON             `json:"ui_schema" gorm:"type:jsonb;not null"`
	ContentHash    string                     `json:"content_hash" gorm:"size:64;not null;index"`
	CreatedByType  ActorType                  `json:"created_by_type" gorm:"size:32;not null;<-:create"`
	CreatedByID    string                     `json:"created_by_id" gorm:"size:128;not null;<-:create"`
	PublishedAt    *time.Time                 `json:"published_at,omitempty" gorm:"index"`
}

func (RequestTypeVersion) TableName() string {
	return "request_type_versions"
}

func (version *RequestTypeVersion) BeforeCreate(_ *gorm.DB) error {
	if err := ensureProjectPublicID(&version.ID); err != nil {
		return err
	}
	if version.Status == "" {
		version.Status = ConfigurationStatusDraft
	}
	return version.RefreshContentHash()
}

func (version *RequestTypeVersion) BeforeUpdate(_ *gorm.DB) error {
	if version.Status == ConfigurationStatusPublished {
		return ErrPublishedConfigurationImmutable
	}
	return version.RefreshContentHash()
}

func (version *RequestTypeVersion) RefreshContentHash() error {
	if err := version.Validate(); err != nil {
		return err
	}
	digest, err := hashCanonicalJSON(struct {
		OrganizationID uint            `json:"organization_id"`
		ProjectID      uint            `json:"project_id"`
		Key            string          `json:"key"`
		Version        uint64          `json:"version"`
		Name           string          `json:"name"`
		Description    string          `json:"description"`
		WorkClass      WorkClass       `json:"work_class"`
		JSONSchema     json.RawMessage `json:"json_schema"`
		UISchema       json.RawMessage `json:"ui_schema"`
	}{
		OrganizationID: version.OrganizationID,
		ProjectID:      version.ProjectID,
		Key:            version.Key,
		Version:        version.Version,
		Name:           version.Name,
		Description:    version.Description,
		WorkClass:      version.WorkClass,
		JSONSchema:     json.RawMessage(version.JSONSchema),
		UISchema:       json.RawMessage(version.UISchema),
	})
	if err != nil {
		return err
	}
	version.ContentHash = digest
	return nil
}

func (version RequestTypeVersion) Validate() error {
	if version.OrganizationID == 0 || version.ProjectID == 0 {
		return errors.New("request type requires organization and project scope")
	}
	if err := validateConfigurationKey(version.Key, "request type"); err != nil {
		return err
	}
	if version.Version == 0 {
		return errors.New("request type version must be positive")
	}
	if !version.Status.IsValid() {
		return fmt.Errorf("invalid request type status %q", version.Status)
	}
	if strings.TrimSpace(version.Name) == "" {
		return errors.New("request type name is required")
	}
	if !version.WorkClass.IsValid() {
		return fmt.Errorf("invalid work class %q", version.WorkClass)
	}
	if err := (ActorRef{
		Type: version.CreatedByType,
		ID:   version.CreatedByID,
	}).Validate(); err != nil {
		return fmt.Errorf("request type creator is invalid: %w", err)
	}
	if err := validateDraft202012Schema(json.RawMessage(version.JSONSchema)); err != nil {
		return fmt.Errorf("invalid request type JSON Schema: %w", err)
	}
	if err := validateJSONObject(json.RawMessage(version.UISchema), "UI schema"); err != nil {
		return err
	}
	return nil
}

type WorkflowStateDefinition struct {
	Key               string            `json:"key"`
	Name              string            `json:"name"`
	LifecycleCategory LifecycleCategory `json:"lifecycle_category"`
	IsInitial         bool              `json:"is_initial,omitempty"`
	IsTerminal        bool              `json:"is_terminal,omitempty"`
}

type WorkflowTransitionDefinition struct {
	Key   string        `json:"key"`
	Name  string        `json:"name"`
	From  string        `json:"from"`
	To    string        `json:"to"`
	Roles []ProjectRole `json:"roles,omitempty"`
}

type WorkflowVersion struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	OrganizationID uint                       `json:"organization_id" gorm:"not null;index;uniqueIndex:idx_workflow_project_key_version,priority:1"`
	ProjectID      uint                       `json:"project_id" gorm:"not null;index;uniqueIndex:idx_workflow_project_key_version,priority:2"`
	Key            string                     `json:"key" gorm:"size:64;not null;uniqueIndex:idx_workflow_project_key_version,priority:3"`
	Version        uint64                     `json:"version" gorm:"not null;uniqueIndex:idx_workflow_project_key_version,priority:4"`
	Status         ConfigurationVersionStatus `json:"status" gorm:"size:20;not null;default:'draft';index"`
	Name           string                     `json:"name" gorm:"size:120;not null"`
	Description    string                     `json:"description" gorm:"size:500"`
	States         datatypes.JSON             `json:"states" gorm:"type:jsonb;not null"`
	Transitions    datatypes.JSON             `json:"transitions" gorm:"type:jsonb;not null"`
	ContentHash    string                     `json:"content_hash" gorm:"size:64;not null;index"`
	CreatedByType  ActorType                  `json:"created_by_type" gorm:"size:32;not null;<-:create"`
	CreatedByID    string                     `json:"created_by_id" gorm:"size:128;not null;<-:create"`
	PublishedAt    *time.Time                 `json:"published_at,omitempty" gorm:"index"`
}

func (WorkflowVersion) TableName() string {
	return "workflow_versions"
}

func (version *WorkflowVersion) BeforeCreate(_ *gorm.DB) error {
	if err := ensureProjectPublicID(&version.ID); err != nil {
		return err
	}
	if version.Status == "" {
		version.Status = ConfigurationStatusDraft
	}
	return version.RefreshContentHash()
}

func (version *WorkflowVersion) BeforeUpdate(_ *gorm.DB) error {
	if version.Status == ConfigurationStatusPublished {
		return ErrPublishedConfigurationImmutable
	}
	return version.RefreshContentHash()
}

func (version WorkflowVersion) StateDefinitions() ([]WorkflowStateDefinition, error) {
	var states []WorkflowStateDefinition
	if err := decodeStrictJSON(version.States, &states); err != nil {
		return nil, fmt.Errorf("decode workflow states: %w", err)
	}
	return states, nil
}

func (version WorkflowVersion) TransitionDefinitions() ([]WorkflowTransitionDefinition, error) {
	var transitions []WorkflowTransitionDefinition
	if err := decodeStrictJSON(version.Transitions, &transitions); err != nil {
		return nil, fmt.Errorf("decode workflow transitions: %w", err)
	}
	return transitions, nil
}

func (version *WorkflowVersion) SetDefinitions(
	states []WorkflowStateDefinition,
	transitions []WorkflowTransitionDefinition,
) error {
	if err := validateWorkflowDefinitions(states, transitions); err != nil {
		return err
	}
	encodedStates, err := json.Marshal(states)
	if err != nil {
		return fmt.Errorf("encode workflow states: %w", err)
	}
	encodedTransitions, err := json.Marshal(transitions)
	if err != nil {
		return fmt.Errorf("encode workflow transitions: %w", err)
	}
	version.States = datatypes.JSON(encodedStates)
	version.Transitions = datatypes.JSON(encodedTransitions)
	return nil
}

func (version *WorkflowVersion) RefreshContentHash() error {
	if err := version.Validate(); err != nil {
		return err
	}
	digest, err := hashCanonicalJSON(struct {
		OrganizationID uint            `json:"organization_id"`
		ProjectID      uint            `json:"project_id"`
		Key            string          `json:"key"`
		Version        uint64          `json:"version"`
		Name           string          `json:"name"`
		Description    string          `json:"description"`
		States         json.RawMessage `json:"states"`
		Transitions    json.RawMessage `json:"transitions"`
	}{
		OrganizationID: version.OrganizationID,
		ProjectID:      version.ProjectID,
		Key:            version.Key,
		Version:        version.Version,
		Name:           version.Name,
		Description:    version.Description,
		States:         json.RawMessage(version.States),
		Transitions:    json.RawMessage(version.Transitions),
	})
	if err != nil {
		return err
	}
	version.ContentHash = digest
	return nil
}

func (version WorkflowVersion) Validate() error {
	if version.OrganizationID == 0 || version.ProjectID == 0 {
		return errors.New("workflow requires organization and project scope")
	}
	if err := validateConfigurationKey(version.Key, "workflow"); err != nil {
		return err
	}
	if version.Version == 0 {
		return errors.New("workflow version must be positive")
	}
	if !version.Status.IsValid() {
		return fmt.Errorf("invalid workflow status %q", version.Status)
	}
	if strings.TrimSpace(version.Name) == "" {
		return errors.New("workflow name is required")
	}
	if err := (ActorRef{
		Type: version.CreatedByType,
		ID:   version.CreatedByID,
	}).Validate(); err != nil {
		return fmt.Errorf("workflow creator is invalid: %w", err)
	}
	states, err := version.StateDefinitions()
	if err != nil {
		return err
	}
	transitions, err := version.TransitionDefinitions()
	if err != nil {
		return err
	}
	return validateWorkflowDefinitions(states, transitions)
}

type ExpressionValueType string

const (
	ExpressionValueString     ExpressionValueType = "string"
	ExpressionValueNumber     ExpressionValueType = "number"
	ExpressionValueBoolean    ExpressionValueType = "boolean"
	ExpressionValueTimestamp  ExpressionValueType = "timestamp"
	ExpressionValueStringList ExpressionValueType = "string_list"
	ExpressionValueNumberList ExpressionValueType = "number_list"
)

func (valueType ExpressionValueType) IsValid() bool {
	switch valueType {
	case ExpressionValueString,
		ExpressionValueNumber,
		ExpressionValueBoolean,
		ExpressionValueTimestamp,
		ExpressionValueStringList,
		ExpressionValueNumberList:
		return true
	default:
		return false
	}
}

type ExpressionOperator string

const (
	ExpressionOperatorEqual              ExpressionOperator = "eq"
	ExpressionOperatorNotEqual           ExpressionOperator = "neq"
	ExpressionOperatorIn                 ExpressionOperator = "in"
	ExpressionOperatorNotIn              ExpressionOperator = "not_in"
	ExpressionOperatorContains           ExpressionOperator = "contains"
	ExpressionOperatorGreaterThan        ExpressionOperator = "gt"
	ExpressionOperatorGreaterThanOrEqual ExpressionOperator = "gte"
	ExpressionOperatorLessThan           ExpressionOperator = "lt"
	ExpressionOperatorLessThanOrEqual    ExpressionOperator = "lte"
	ExpressionOperatorExists             ExpressionOperator = "exists"
)

func (operator ExpressionOperator) IsValid() bool {
	switch operator {
	case ExpressionOperatorEqual,
		ExpressionOperatorNotEqual,
		ExpressionOperatorIn,
		ExpressionOperatorNotIn,
		ExpressionOperatorContains,
		ExpressionOperatorGreaterThan,
		ExpressionOperatorGreaterThanOrEqual,
		ExpressionOperatorLessThan,
		ExpressionOperatorLessThanOrEqual,
		ExpressionOperatorExists:
		return true
	default:
		return false
	}
}

// TypedExpression is a bounded declarative predicate. It intentionally has no
// source-code, template, function-call or eval field.
type TypedExpression struct {
	Field     string              `json:"field,omitempty"`
	ValueType ExpressionValueType `json:"value_type,omitempty"`
	Operator  ExpressionOperator  `json:"operator,omitempty"`
	Value     json.RawMessage     `json:"value,omitempty"`
	All       []TypedExpression   `json:"all,omitempty"`
	Any       []TypedExpression   `json:"any,omitempty"`
	Not       *TypedExpression    `json:"not,omitempty"`
}

func (expression TypedExpression) Validate() error {
	nodes := 0
	return expression.validate(0, &nodes)
}

func (expression TypedExpression) validate(depth int, nodes *int) error {
	if depth > 8 {
		return errors.New("typed expression exceeds maximum depth")
	}
	(*nodes)++
	if *nodes > 64 {
		return errors.New("typed expression exceeds maximum node count")
	}
	groupCount := 0
	if len(expression.All) > 0 {
		groupCount++
	}
	if len(expression.Any) > 0 {
		groupCount++
	}
	if expression.Not != nil {
		groupCount++
	}
	if groupCount > 0 {
		if groupCount != 1 ||
			expression.Field != "" ||
			expression.ValueType != "" ||
			expression.Operator != "" ||
			len(expression.Value) != 0 {
			return errors.New("typed expression group must contain exactly one of all, any or not")
		}
		children := expression.All
		if len(expression.Any) > 0 {
			children = expression.Any
		}
		if expression.Not != nil {
			children = []TypedExpression{*expression.Not}
		}
		if len(children) == 0 || len(children) > 32 {
			return errors.New("typed expression group size is invalid")
		}
		for i := range children {
			if err := children[i].validate(depth+1, nodes); err != nil {
				return err
			}
		}
		return nil
	}
	if !typedFieldPattern.MatchString(expression.Field) {
		return fmt.Errorf("invalid typed expression field %q", expression.Field)
	}
	if !expression.ValueType.IsValid() {
		return fmt.Errorf("invalid typed expression value type %q", expression.ValueType)
	}
	if !expression.Operator.IsValid() {
		return fmt.Errorf("invalid typed expression operator %q", expression.Operator)
	}
	if expression.Operator == ExpressionOperatorExists {
		if len(expression.Value) != 0 && string(expression.Value) != "null" {
			return errors.New("exists expression must not carry a value")
		}
		return nil
	}
	if len(expression.Value) == 0 || !json.Valid(expression.Value) {
		return errors.New("typed expression value must be valid JSON")
	}
	return validateTypedExpressionValue(
		expression.ValueType,
		expression.Operator,
		expression.Value,
	)
}

type ConfigurationActionType string

const (
	ConfigurationActionSetField        ConfigurationActionType = "set_field"
	ConfigurationActionSetPriority     ConfigurationActionType = "set_priority"
	ConfigurationActionRouteQueue      ConfigurationActionType = "route_queue"
	ConfigurationActionRouteTeam       ConfigurationActionType = "route_team"
	ConfigurationActionTransition      ConfigurationActionType = "transition"
	ConfigurationActionAddTag          ConfigurationActionType = "add_tag"
	ConfigurationActionRemoveTag       ConfigurationActionType = "remove_tag"
	ConfigurationActionNotify          ConfigurationActionType = "notify"
	ConfigurationActionRequireApproval ConfigurationActionType = "require_approval"
)

type ConfigurationAction struct {
	Type       ConfigurationActionType `json:"type"`
	Parameters json.RawMessage         `json:"parameters"`
}

func (action ConfigurationAction) Validate() error {
	switch action.Type {
	case ConfigurationActionSetField:
		var parameters struct {
			Field     string              `json:"field"`
			ValueType ExpressionValueType `json:"value_type"`
			Value     json.RawMessage     `json:"value"`
		}
		if err := decodeStrictJSON(action.Parameters, &parameters); err != nil {
			return fmt.Errorf("decode set_field action: %w", err)
		}
		if !typedFieldPattern.MatchString(parameters.Field) ||
			!parameters.ValueType.IsValid() {
			return errors.New("set_field action has invalid field or value type")
		}
		return validateTypedExpressionValue(
			parameters.ValueType,
			ExpressionOperatorEqual,
			parameters.Value,
		)
	case ConfigurationActionSetPriority:
		var parameters struct {
			Priority TicketPriority `json:"priority"`
		}
		if err := decodeStrictJSON(action.Parameters, &parameters); err != nil {
			return fmt.Errorf("decode set_priority action: %w", err)
		}
		if !parameters.Priority.IsValid() {
			return fmt.Errorf("invalid priority %q", parameters.Priority)
		}
		return nil
	case ConfigurationActionRouteQueue:
		var parameters struct {
			QueueKey string `json:"queue_key"`
		}
		if err := decodeStrictJSON(action.Parameters, &parameters); err != nil {
			return fmt.Errorf("decode route_queue action: %w", err)
		}
		return ValidateQueueKey(parameters.QueueKey)
	case ConfigurationActionRouteTeam:
		var parameters struct {
			TeamKey string `json:"team_key"`
		}
		if err := decodeStrictJSON(action.Parameters, &parameters); err != nil {
			return fmt.Errorf("decode route_team action: %w", err)
		}
		return ValidateTeamKey(parameters.TeamKey)
	case ConfigurationActionTransition:
		var parameters struct {
			State string `json:"state"`
		}
		if err := decodeStrictJSON(action.Parameters, &parameters); err != nil {
			return fmt.Errorf("decode transition action: %w", err)
		}
		return validateConfigurationKey(parameters.State, "workflow state")
	case ConfigurationActionAddTag, ConfigurationActionRemoveTag:
		var parameters struct {
			Tag string `json:"tag"`
		}
		if err := decodeStrictJSON(action.Parameters, &parameters); err != nil {
			return fmt.Errorf("decode tag action: %w", err)
		}
		if strings.TrimSpace(parameters.Tag) == "" ||
			len(parameters.Tag) > 64 ||
			strings.ContainsAny(parameters.Tag, "\r\n") {
			return errors.New("tag action has invalid tag")
		}
		return nil
	case ConfigurationActionNotify:
		var parameters struct {
			TemplateKey string `json:"template_key"`
			Channel     string `json:"channel"`
		}
		if err := decodeStrictJSON(action.Parameters, &parameters); err != nil {
			return fmt.Errorf("decode notify action: %w", err)
		}
		if err := validateConfigurationKey(parameters.TemplateKey, "notification template"); err != nil {
			return err
		}
		switch parameters.Channel {
		case "internal", "email", "webhook":
			return nil
		default:
			return fmt.Errorf("invalid notification channel %q", parameters.Channel)
		}
	case ConfigurationActionRequireApproval:
		var parameters struct {
			PolicyKey string `json:"policy_key"`
		}
		if err := decodeStrictJSON(action.Parameters, &parameters); err != nil {
			return fmt.Errorf("decode require_approval action: %w", err)
		}
		return validateConfigurationKey(parameters.PolicyKey, "approval policy")
	default:
		return fmt.Errorf("unsupported configuration action %q", action.Type)
	}
}

type SLAPolicyDefinition struct {
	Key               string              `json:"key"`
	Name              string              `json:"name"`
	ResponseMinutes   uint                `json:"response_minutes"`
	ResolutionMinutes uint                `json:"resolution_minutes"`
	CalendarKey       string              `json:"calendar_key"`
	PauseWhen         []LifecycleCategory `json:"pause_when,omitempty"`
	Applicability     *TypedExpression    `json:"applicability,omitempty"`
}

type CalendarWindow struct {
	Weekday int    `json:"weekday"`
	Start   string `json:"start"`
	End     string `json:"end"`
}

type CalendarDefinition struct {
	Key      string           `json:"key"`
	Name     string           `json:"name"`
	Timezone string           `json:"timezone"`
	Windows  []CalendarWindow `json:"windows"`
	Holidays []string         `json:"holidays,omitempty"`
}

type RouteDefinition struct {
	Key      string          `json:"key"`
	Name     string          `json:"name"`
	Priority int             `json:"priority"`
	When     TypedExpression `json:"when"`
	QueueKey string          `json:"queue_key"`
	TeamKey  string          `json:"team_key,omitempty"`
}

type AutomationDefinition struct {
	Key     string                `json:"key"`
	Name    string                `json:"name"`
	Enabled bool                  `json:"enabled"`
	When    TypedExpression       `json:"when"`
	Actions []ConfigurationAction `json:"actions"`
}

type ApprovalPolicyDefinition struct {
	Key               string          `json:"key"`
	Name              string          `json:"name"`
	When              TypedExpression `json:"when"`
	RequiredApprovals uint            `json:"required_approvals"`
	ApproverRoles     []ProjectRole   `json:"approver_roles"`
}

type ConfigurationRiskLevel string

const (
	ConfigurationRiskLow      ConfigurationRiskLevel = "low"
	ConfigurationRiskMedium   ConfigurationRiskLevel = "medium"
	ConfigurationRiskHigh     ConfigurationRiskLevel = "high"
	ConfigurationRiskCritical ConfigurationRiskLevel = "critical"
)

func (level ConfigurationRiskLevel) IsValid() bool {
	switch level {
	case ConfigurationRiskLow,
		ConfigurationRiskMedium,
		ConfigurationRiskHigh,
		ConfigurationRiskCritical:
		return true
	default:
		return false
	}
}

type RiskPolicyDefinition struct {
	Key               string                 `json:"key"`
	Name              string                 `json:"name"`
	When              TypedExpression        `json:"when"`
	Level             ConfigurationRiskLevel `json:"level"`
	RequiresApproval  bool                   `json:"requires_approval"`
	ApprovalPolicyKey string                 `json:"approval_policy_key,omitempty"`
}

type ConfigurationSnapshot struct {
	RequestTypeVersionIDs []string                   `json:"request_type_version_ids"`
	WorkflowVersionIDs    []string                   `json:"workflow_version_ids"`
	SLAPolicies           []SLAPolicyDefinition      `json:"sla_policies,omitempty"`
	Calendars             []CalendarDefinition       `json:"calendars,omitempty"`
	Routes                []RouteDefinition          `json:"routes,omitempty"`
	Automations           []AutomationDefinition     `json:"automations,omitempty"`
	ApprovalPolicies      []ApprovalPolicyDefinition `json:"approval_policies,omitempty"`
	RiskPolicies          []RiskPolicyDefinition     `json:"risk_policies,omitempty"`
}

const MaxConfigurationSnapshotVersions = 100

func (snapshot ConfigurationSnapshot) Validate() error {
	if len(snapshot.RequestTypeVersionIDs) > MaxConfigurationSnapshotVersions ||
		len(snapshot.WorkflowVersionIDs) > MaxConfigurationSnapshotVersions {
		return errors.New(
			"configuration snapshot exceeds the maximum published version count",
		)
	}
	if err := validateUUIDReferences(snapshot.RequestTypeVersionIDs, "request type"); err != nil {
		return err
	}
	if err := validateUUIDReferences(snapshot.WorkflowVersionIDs, "workflow"); err != nil {
		return err
	}
	if len(snapshot.RequestTypeVersionIDs) == 0 ||
		len(snapshot.WorkflowVersionIDs) == 0 {
		return errors.New("configuration snapshot requires request type and workflow versions")
	}
	if err := validateConfigurationDefinitions(snapshot); err != nil {
		return err
	}
	return nil
}

type ConfigurationSimulationReport struct {
	SnapshotHash string   `json:"snapshot_hash"`
	Checks       []string `json:"checks"`
	Warnings     []string `json:"warnings,omitempty"`
}

type ConfigurationRelease struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	OrganizationID       uint                       `json:"organization_id" gorm:"not null;index;uniqueIndex:idx_configuration_release_project_version,priority:1"`
	ProjectID            uint                       `json:"project_id" gorm:"not null;index;uniqueIndex:idx_configuration_release_project_version,priority:2"`
	Version              uint64                     `json:"version" gorm:"not null;uniqueIndex:idx_configuration_release_project_version,priority:3"`
	Status               ConfigurationVersionStatus `json:"status" gorm:"size:20;not null;default:'draft';index"`
	Snapshot             datatypes.JSON             `json:"snapshot" gorm:"type:jsonb;not null"`
	SnapshotHash         string                     `json:"snapshot_hash" gorm:"size:64;not null;index"`
	SimulationReport     datatypes.JSON             `json:"simulation_report,omitempty" gorm:"type:jsonb"`
	BaseReleaseID        *string                    `json:"base_release_id,omitempty" gorm:"size:36;index"`
	RollbackOfReleaseID  *string                    `json:"rollback_of_release_id,omitempty" gorm:"size:36;index"`
	SourcePackageKey     string                     `json:"source_package_key,omitempty" gorm:"size:64;index"`
	SourcePackageVersion string                     `json:"source_package_version,omitempty" gorm:"size:64"`
	CreatedByType        ActorType                  `json:"created_by_type" gorm:"size:32;not null;<-:create"`
	CreatedByID          string                     `json:"created_by_id" gorm:"size:128;not null;<-:create"`
	ApprovedByType       ActorType                  `json:"approved_by_type,omitempty" gorm:"size:32"`
	ApprovedByID         string                     `json:"approved_by_id,omitempty" gorm:"size:128"`
	PublishedAt          *time.Time                 `json:"published_at,omitempty" gorm:"index"`
}

func (ConfigurationRelease) TableName() string {
	return "configuration_releases"
}

func (release *ConfigurationRelease) BeforeCreate(_ *gorm.DB) error {
	if err := ensureProjectPublicID(&release.ID); err != nil {
		return err
	}
	if release.Status == "" {
		release.Status = ConfigurationStatusDraft
	}
	return release.RefreshSnapshotHash()
}

func (release *ConfigurationRelease) BeforeUpdate(_ *gorm.DB) error {
	if release.Status == ConfigurationStatusPublished {
		return ErrPublishedConfigurationImmutable
	}
	return release.RefreshSnapshotHash()
}

func (release ConfigurationRelease) ConfigurationSnapshot() (ConfigurationSnapshot, error) {
	var snapshot ConfigurationSnapshot
	if err := decodeStrictJSON(release.Snapshot, &snapshot); err != nil {
		return snapshot, fmt.Errorf("decode configuration snapshot: %w", err)
	}
	return snapshot, nil
}

func (release *ConfigurationRelease) SetConfigurationSnapshot(
	snapshot ConfigurationSnapshot,
) error {
	if err := snapshot.Validate(); err != nil {
		return err
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode configuration snapshot: %w", err)
	}
	release.Snapshot = datatypes.JSON(encoded)
	return release.RefreshSnapshotHash()
}

func (release *ConfigurationRelease) SetSimulationReport(
	report ConfigurationSimulationReport,
) error {
	encoded, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("encode configuration simulation report: %w", err)
	}
	release.SimulationReport = datatypes.JSON(encoded)
	return nil
}

func (release *ConfigurationRelease) RefreshSnapshotHash() error {
	if release.OrganizationID == 0 || release.ProjectID == 0 {
		return errors.New("configuration release requires organization and project scope")
	}
	if release.Version == 0 {
		return errors.New("configuration release version must be positive")
	}
	if !release.Status.IsValid() {
		return fmt.Errorf("invalid configuration release status %q", release.Status)
	}
	if err := (ActorRef{
		Type: release.CreatedByType,
		ID:   release.CreatedByID,
	}).Validate(); err != nil {
		return fmt.Errorf("configuration release creator is invalid: %w", err)
	}
	snapshot, err := release.ConfigurationSnapshot()
	if err != nil {
		return err
	}
	if err := snapshot.Validate(); err != nil {
		return err
	}
	digest, err := hashCanonicalJSON(snapshot)
	if err != nil {
		return err
	}
	release.SnapshotHash = digest
	if release.Status == ConfigurationStatusPublished {
		if release.PublishedAt == nil {
			return errors.New("published release requires published_at")
		}
		if err := (ActorRef{
			Type: release.ApprovedByType,
			ID:   release.ApprovedByID,
		}).Validate(); err != nil {
			return fmt.Errorf("published release requires approving actor: %w", err)
		}
	}
	return nil
}

type SolutionTemplateKind string

const (
	SolutionTemplateRequestType SolutionTemplateKind = "request_type"
	SolutionTemplateWorkflow    SolutionTemplateKind = "workflow"
	SolutionTemplateSLA         SolutionTemplateKind = "sla"
	SolutionTemplateCalendar    SolutionTemplateKind = "calendar"
	SolutionTemplateRoute       SolutionTemplateKind = "route"
	SolutionTemplateAutomation  SolutionTemplateKind = "automation"
	SolutionTemplateApproval    SolutionTemplateKind = "approval"
	SolutionTemplateRisk        SolutionTemplateKind = "risk"
	SolutionTemplateExtension   SolutionTemplateKind = "extension"
)

func (kind SolutionTemplateKind) IsValid() bool {
	switch kind {
	case SolutionTemplateRequestType,
		SolutionTemplateWorkflow,
		SolutionTemplateSLA,
		SolutionTemplateCalendar,
		SolutionTemplateRoute,
		SolutionTemplateAutomation,
		SolutionTemplateApproval,
		SolutionTemplateRisk,
		SolutionTemplateExtension:
		return true
	default:
		return false
	}
}

type SolutionTemplateReference struct {
	Kind SolutionTemplateKind `json:"kind"`
	Key  string               `json:"key"`
}

type SolutionDependency struct {
	PackageKey        string `json:"package_key"`
	VersionConstraint string `json:"version_constraint"`
	ContentHash       string `json:"content_hash"`
}

type IndustrySolutionManifest struct {
	SchemaVersion      string                      `json:"schema_version"`
	PackageKey         string                      `json:"package_key"`
	Name               string                      `json:"name"`
	Industry           string                      `json:"industry"`
	Version            string                      `json:"version"`
	Terminology        map[string]string           `json:"terminology,omitempty"`
	TemplateReferences []SolutionTemplateReference `json:"template_references"`
	Dependencies       []SolutionDependency        `json:"dependencies,omitempty"`
	ContentHash        string                      `json:"content_hash"`
}

type RequestTypeTemplate struct {
	Key         string          `json:"key"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	WorkClass   WorkClass       `json:"work_class"`
	JSONSchema  json.RawMessage `json:"json_schema"`
	UISchema    json.RawMessage `json:"ui_schema"`
}

type WorkflowTemplate struct {
	Key         string                         `json:"key"`
	Name        string                         `json:"name"`
	Description string                         `json:"description,omitempty"`
	States      []WorkflowStateDefinition      `json:"states"`
	Transitions []WorkflowTransitionDefinition `json:"transitions"`
}

// IndustrySolutionSnapshot contains configuration templates, never executable
// source. Installation materializes these templates as project-owned versions.
type IndustrySolutionSnapshot struct {
	RequestTypes     []RequestTypeTemplate      `json:"request_types"`
	Workflows        []WorkflowTemplate         `json:"workflows"`
	SLAPolicies      []SLAPolicyDefinition      `json:"sla_policies,omitempty"`
	Calendars        []CalendarDefinition       `json:"calendars,omitempty"`
	Routes           []RouteDefinition          `json:"routes,omitempty"`
	Automations      []AutomationDefinition     `json:"automations,omitempty"`
	ApprovalPolicies []ApprovalPolicyDefinition `json:"approval_policies,omitempty"`
	RiskPolicies     []RiskPolicyDefinition     `json:"risk_policies,omitempty"`
	Extensions       map[string]json.RawMessage `json:"extensions,omitempty"`
}

func (snapshot IndustrySolutionSnapshot) Validate() error {
	requestTypes := make(map[string]struct{}, len(snapshot.RequestTypes))
	for _, requestType := range snapshot.RequestTypes {
		if err := validateConfigurationKey(requestType.Key, "request type template"); err != nil {
			return err
		}
		if _, duplicate := requestTypes[requestType.Key]; duplicate {
			return fmt.Errorf("duplicate request type template %q", requestType.Key)
		}
		requestTypes[requestType.Key] = struct{}{}
		if strings.TrimSpace(requestType.Name) == "" ||
			!requestType.WorkClass.IsValid() {
			return fmt.Errorf("request type template %q is invalid", requestType.Key)
		}
		if err := validateDraft202012Schema(requestType.JSONSchema); err != nil {
			return fmt.Errorf("request type template %q: %w", requestType.Key, err)
		}
		if err := validateJSONObject(requestType.UISchema, "UI schema"); err != nil {
			return fmt.Errorf("request type template %q: %w", requestType.Key, err)
		}
	}
	if len(requestTypes) == 0 {
		return errors.New("solution snapshot requires request type templates")
	}

	workflows := make(map[string]struct{}, len(snapshot.Workflows))
	for _, workflow := range snapshot.Workflows {
		if err := validateConfigurationKey(workflow.Key, "workflow template"); err != nil {
			return err
		}
		if _, duplicate := workflows[workflow.Key]; duplicate {
			return fmt.Errorf("duplicate workflow template %q", workflow.Key)
		}
		workflows[workflow.Key] = struct{}{}
		if strings.TrimSpace(workflow.Name) == "" {
			return fmt.Errorf("workflow template %q requires a name", workflow.Key)
		}
		if err := validateWorkflowDefinitions(workflow.States, workflow.Transitions); err != nil {
			return fmt.Errorf("workflow template %q: %w", workflow.Key, err)
		}
	}
	if len(workflows) == 0 {
		return errors.New("solution snapshot requires workflow templates")
	}
	if len(snapshot.Extensions) > 32 {
		return errors.New("solution snapshot exceeds maximum extension count")
	}
	totalExtensionBytes := 0
	for key, raw := range snapshot.Extensions {
		if err := validateConfigurationKey(key, "solution extension"); err != nil {
			return err
		}
		if len(raw) == 0 || len(raw) > 256*1024 {
			return fmt.Errorf(
				"solution extension %q exceeds maximum size",
				key,
			)
		}
		totalExtensionBytes += len(raw)
		if totalExtensionBytes > 1024*1024 {
			return errors.New("solution extensions exceed maximum total size")
		}
		if err := validateJSONObject(
			raw,
			"solution extension "+key,
		); err != nil {
			return err
		}
	}
	return validateConfigurationDefinitions(ConfigurationSnapshot{
		SLAPolicies:      snapshot.SLAPolicies,
		Calendars:        snapshot.Calendars,
		Routes:           snapshot.Routes,
		Automations:      snapshot.Automations,
		ApprovalPolicies: snapshot.ApprovalPolicies,
		RiskPolicies:     snapshot.RiskPolicies,
	})
}

func (manifest IndustrySolutionManifest) Validate(
	snapshot IndustrySolutionSnapshot,
) error {
	if manifest.SchemaVersion != "1.0" {
		return fmt.Errorf("unsupported solution manifest schema %q", manifest.SchemaVersion)
	}
	if err := validateConfigurationKey(manifest.PackageKey, "solution package"); err != nil {
		return err
	}
	if strings.TrimSpace(manifest.Name) == "" ||
		strings.TrimSpace(manifest.Industry) == "" {
		return errors.New("solution package name and industry are required")
	}
	if !semanticVersionPattern.MatchString(manifest.Version) {
		return fmt.Errorf("invalid solution package version %q", manifest.Version)
	}
	if !hexDigestPattern.MatchString(manifest.ContentHash) {
		return errors.New("solution package content hash must be a SHA-256 digest")
	}
	if len(manifest.Terminology) > 128 {
		return errors.New("solution terminology exceeds maximum entries")
	}
	for key, value := range manifest.Terminology {
		if err := validateConfigurationKey(key, "terminology"); err != nil {
			return err
		}
		if strings.TrimSpace(value) == "" ||
			len(value) > 120 ||
			strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("terminology %q has invalid value", key)
		}
	}
	available := solutionSnapshotTemplateKeys(snapshot)
	references := make(map[string]struct{}, len(manifest.TemplateReferences))
	for _, reference := range manifest.TemplateReferences {
		if !reference.Kind.IsValid() {
			return fmt.Errorf("invalid solution template kind %q", reference.Kind)
		}
		if err := validateConfigurationKey(reference.Key, "solution template"); err != nil {
			return err
		}
		identity := string(reference.Kind) + ":" + reference.Key
		if _, duplicate := references[identity]; duplicate {
			return fmt.Errorf("duplicate solution template reference %q", identity)
		}
		references[identity] = struct{}{}
		if _, exists := available[identity]; !exists {
			return fmt.Errorf("solution template reference %q is missing", identity)
		}
	}
	if len(references) != len(available) {
		return errors.New("solution manifest must reference every snapshot template")
	}
	dependencies := make(map[string]struct{}, len(manifest.Dependencies))
	for _, dependency := range manifest.Dependencies {
		if err := validateConfigurationKey(dependency.PackageKey, "dependency package"); err != nil {
			return err
		}
		if strings.TrimSpace(dependency.VersionConstraint) == "" ||
			len(dependency.VersionConstraint) > 100 {
			return fmt.Errorf("dependency %q has invalid version constraint", dependency.PackageKey)
		}
		if !hexDigestPattern.MatchString(dependency.ContentHash) {
			return fmt.Errorf("dependency %q has invalid content hash", dependency.PackageKey)
		}
		if _, duplicate := dependencies[dependency.PackageKey]; duplicate {
			return fmt.Errorf("duplicate dependency %q", dependency.PackageKey)
		}
		dependencies[dependency.PackageKey] = struct{}{}
	}
	return nil
}

type IndustrySolutionPackage struct {
	Manifest           IndustrySolutionManifest `json:"manifest"`
	Snapshot           IndustrySolutionSnapshot `json:"snapshot"`
	SignatureAlgorithm string                   `json:"signature_algorithm"`
	SignerKeyID        string                   `json:"signer_key_id"`
	Signature          []byte                   `json:"signature"`
}

var ErrIndustrySolutionSignatureInvalid = errors.New("industry solution signature is invalid")

func SignIndustrySolutionPackage(
	manifest IndustrySolutionManifest,
	snapshot IndustrySolutionSnapshot,
	signerKeyID string,
	privateKey ed25519.PrivateKey,
) (*IndustrySolutionPackage, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("Ed25519 private key is invalid")
	}
	if strings.TrimSpace(signerKeyID) == "" || len(signerKeyID) > 128 {
		return nil, errors.New("signer key id is invalid")
	}
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	contentHash, err := hashCanonicalJSON(snapshot)
	if err != nil {
		return nil, err
	}
	manifest.ContentHash = contentHash
	if err := manifest.Validate(snapshot); err != nil {
		return nil, err
	}
	result := &IndustrySolutionPackage{
		Manifest:           manifest,
		Snapshot:           snapshot,
		SignatureAlgorithm: "ed25519",
		SignerKeyID:        signerKeyID,
	}
	payload, err := result.signaturePayload()
	if err != nil {
		return nil, err
	}
	result.Signature = ed25519.Sign(privateKey, payload)
	return result, nil
}

func (solution IndustrySolutionPackage) Verify(publicKey ed25519.PublicKey) error {
	if len(publicKey) != ed25519.PublicKeySize ||
		solution.SignatureAlgorithm != "ed25519" ||
		strings.TrimSpace(solution.SignerKeyID) == "" ||
		len(solution.Signature) != ed25519.SignatureSize {
		return ErrIndustrySolutionSignatureInvalid
	}
	if err := solution.Snapshot.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrIndustrySolutionSignatureInvalid, err)
	}
	contentHash, err := hashCanonicalJSON(solution.Snapshot)
	if err != nil {
		return err
	}
	if contentHash != solution.Manifest.ContentHash {
		return fmt.Errorf("%w: content hash mismatch", ErrIndustrySolutionSignatureInvalid)
	}
	if err := solution.Manifest.Validate(solution.Snapshot); err != nil {
		return fmt.Errorf("%w: %v", ErrIndustrySolutionSignatureInvalid, err)
	}
	payload, err := solution.signaturePayload()
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, payload, solution.Signature) {
		return ErrIndustrySolutionSignatureInvalid
	}
	return nil
}

func (solution IndustrySolutionPackage) Export() ([]byte, error) {
	if len(solution.Signature) != ed25519.SignatureSize {
		return nil, ErrIndustrySolutionSignatureInvalid
	}
	encoded, err := json.Marshal(solution)
	if err != nil {
		return nil, fmt.Errorf("export industry solution package: %w", err)
	}
	return encoded, nil
}

func ParseIndustrySolutionPackage(raw []byte) (*IndustrySolutionPackage, error) {
	var solution IndustrySolutionPackage
	if err := decodeStrictJSON(raw, &solution); err != nil {
		return nil, fmt.Errorf("parse industry solution package: %w", err)
	}
	return &solution, nil
}

func (solution IndustrySolutionPackage) signaturePayload() ([]byte, error) {
	payload := struct {
		Manifest           IndustrySolutionManifest `json:"manifest"`
		Snapshot           IndustrySolutionSnapshot `json:"snapshot"`
		SignatureAlgorithm string                   `json:"signature_algorithm"`
		SignerKeyID        string                   `json:"signer_key_id"`
	}{
		Manifest:           solution.Manifest,
		Snapshot:           solution.Snapshot,
		SignatureAlgorithm: solution.SignatureAlgorithm,
		SignerKeyID:        solution.SignerKeyID,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode industry solution signature payload: %w", err)
	}
	return encoded, nil
}

type ConfigurationDiff struct {
	FromVersion     string   `json:"from_version,omitempty"`
	ToVersion       string   `json:"to_version"`
	Added           []string `json:"added,omitempty"`
	Removed         []string `json:"removed,omitempty"`
	Changed         []string `json:"changed,omitempty"`
	BreakingChanges []string `json:"breaking_changes,omitempty"`
	Compatible      bool     `json:"compatible"`
}

func DiffIndustrySolutionSnapshots(
	fromVersion string,
	from IndustrySolutionSnapshot,
	toVersion string,
	to IndustrySolutionSnapshot,
) (ConfigurationDiff, error) {
	if err := to.Validate(); err != nil {
		return ConfigurationDiff{}, err
	}
	if fromVersion != "" {
		if err := from.Validate(); err != nil {
			return ConfigurationDiff{}, err
		}
	}
	diff := ConfigurationDiff{
		FromVersion: fromVersion,
		ToVersion:   toVersion,
		Compatible:  true,
	}
	fromItems, err := solutionSnapshotFingerprints(from)
	if err != nil {
		return diff, err
	}
	toItems, err := solutionSnapshotFingerprints(to)
	if err != nil {
		return diff, err
	}
	for identity, toHash := range toItems {
		fromHash, exists := fromItems[identity]
		switch {
		case !exists:
			diff.Added = append(diff.Added, identity)
		case fromHash != toHash:
			diff.Changed = append(diff.Changed, identity)
		}
	}
	for identity := range fromItems {
		if _, exists := toItems[identity]; !exists {
			diff.Removed = append(diff.Removed, identity)
			if strings.HasPrefix(identity, "request_type:") ||
				strings.HasPrefix(identity, "workflow:") {
				diff.BreakingChanges = append(
					diff.BreakingChanges,
					"removed "+identity,
				)
			}
		}
	}
	diff.BreakingChanges = append(
		diff.BreakingChanges,
		requestTypeCompatibilityBreaks(from.RequestTypes, to.RequestTypes)...,
	)
	diff.BreakingChanges = append(
		diff.BreakingChanges,
		workflowCompatibilityBreaks(from.Workflows, to.Workflows)...,
	)
	diff.Compatible = len(diff.BreakingChanges) == 0
	sort.Strings(diff.Added)
	sort.Strings(diff.Removed)
	sort.Strings(diff.Changed)
	sort.Strings(diff.BreakingChanges)
	return diff, nil
}

type SolutionInstallationStatus string

const (
	SolutionInstallationPending    SolutionInstallationStatus = "pending_review"
	SolutionInstallationActive     SolutionInstallationStatus = "active"
	SolutionInstallationSuperseded SolutionInstallationStatus = "superseded"
)

func (status SolutionInstallationStatus) IsValid() bool {
	switch status {
	case SolutionInstallationPending,
		SolutionInstallationActive,
		SolutionInstallationSuperseded:
		return true
	default:
		return false
	}
}

type ProjectSolutionInstallation struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	OrganizationID     uint                       `json:"organization_id" gorm:"not null;index;uniqueIndex:idx_solution_install_project_package_version,priority:1"`
	ProjectID          uint                       `json:"project_id" gorm:"not null;index;uniqueIndex:idx_solution_install_project_package_version,priority:2"`
	PackageKey         string                     `json:"package_key" gorm:"size:64;not null;index;uniqueIndex:idx_solution_install_project_package_version,priority:3"`
	PackageVersion     string                     `json:"package_version" gorm:"size:64;not null;uniqueIndex:idx_solution_install_project_package_version,priority:4"`
	Status             SolutionInstallationStatus `json:"status" gorm:"size:24;not null;default:'pending_review';index"`
	ReleaseID          string                     `json:"release_id" gorm:"size:36;not null;index"`
	BaseInstallationID *string                    `json:"base_installation_id,omitempty" gorm:"size:36;index"`
	Manifest           datatypes.JSON             `json:"manifest" gorm:"type:jsonb;not null"`
	PackageSnapshot    datatypes.JSON             `json:"package_snapshot" gorm:"type:jsonb;not null"`
	UpgradeDiff        datatypes.JSON             `json:"upgrade_diff" gorm:"type:jsonb;not null"`
	ContentHash        string                     `json:"content_hash" gorm:"size:64;not null;index"`
	SignerKeyID        string                     `json:"signer_key_id" gorm:"size:128;not null"`
	Signature          []byte                     `json:"signature" gorm:"type:bytea;not null"`
	CreatedByType      ActorType                  `json:"created_by_type" gorm:"size:32;not null;<-:create"`
	CreatedByID        string                     `json:"created_by_id" gorm:"size:128;not null;<-:create"`
	ApprovedByType     ActorType                  `json:"approved_by_type,omitempty" gorm:"size:32"`
	ApprovedByID       string                     `json:"approved_by_id,omitempty" gorm:"size:128"`
	ApprovedAt         *time.Time                 `json:"approved_at,omitempty" gorm:"index"`
}

func (ProjectSolutionInstallation) TableName() string {
	return "project_solution_installations"
}

func (installation *ProjectSolutionInstallation) BeforeCreate(_ *gorm.DB) error {
	if err := ensureProjectPublicID(&installation.ID); err != nil {
		return err
	}
	if installation.Status == "" {
		installation.Status = SolutionInstallationPending
	}
	if installation.OrganizationID == 0 || installation.ProjectID == 0 {
		return errors.New("solution installation requires organization and project scope")
	}
	if err := validateConfigurationKey(installation.PackageKey, "solution package"); err != nil {
		return err
	}
	if !semanticVersionPattern.MatchString(installation.PackageVersion) {
		return errors.New("solution installation package version is invalid")
	}
	if !installation.Status.IsValid() {
		return fmt.Errorf("invalid solution installation status %q", installation.Status)
	}
	if _, err := uuid.Parse(installation.ReleaseID); err != nil {
		return errors.New("solution installation release id is invalid")
	}
	if !hexDigestPattern.MatchString(installation.ContentHash) {
		return errors.New("solution installation content hash is invalid")
	}
	if len(installation.Signature) != ed25519.SignatureSize {
		return errors.New("solution installation signature is invalid")
	}
	if err := (ActorRef{
		Type: installation.CreatedByType,
		ID:   installation.CreatedByID,
	}).Validate(); err != nil {
		return fmt.Errorf("solution installation creator is invalid: %w", err)
	}
	if len(installation.Manifest) == 0 ||
		len(installation.PackageSnapshot) == 0 ||
		len(installation.UpgradeDiff) == 0 {
		return errors.New("solution installation snapshot evidence is required")
	}
	return nil
}

func solutionSnapshotTemplateKeys(
	snapshot IndustrySolutionSnapshot,
) map[string]struct{} {
	result := make(map[string]struct{})
	for _, template := range snapshot.RequestTypes {
		result[string(SolutionTemplateRequestType)+":"+template.Key] = struct{}{}
	}
	for _, template := range snapshot.Workflows {
		result[string(SolutionTemplateWorkflow)+":"+template.Key] = struct{}{}
	}
	for _, template := range snapshot.SLAPolicies {
		result[string(SolutionTemplateSLA)+":"+template.Key] = struct{}{}
	}
	for _, template := range snapshot.Calendars {
		result[string(SolutionTemplateCalendar)+":"+template.Key] = struct{}{}
	}
	for _, template := range snapshot.Routes {
		result[string(SolutionTemplateRoute)+":"+template.Key] = struct{}{}
	}
	for _, template := range snapshot.Automations {
		result[string(SolutionTemplateAutomation)+":"+template.Key] = struct{}{}
	}
	for _, template := range snapshot.ApprovalPolicies {
		result[string(SolutionTemplateApproval)+":"+template.Key] = struct{}{}
	}
	for _, template := range snapshot.RiskPolicies {
		result[string(SolutionTemplateRisk)+":"+template.Key] = struct{}{}
	}
	for key := range snapshot.Extensions {
		result[string(SolutionTemplateExtension)+":"+key] = struct{}{}
	}
	return result
}

func solutionSnapshotFingerprints(
	snapshot IndustrySolutionSnapshot,
) (map[string]string, error) {
	result := make(map[string]string)
	add := func(kind SolutionTemplateKind, key string, value any) error {
		digest, err := hashCanonicalJSON(value)
		if err != nil {
			return err
		}
		result[string(kind)+":"+key] = digest
		return nil
	}
	for _, value := range snapshot.RequestTypes {
		if err := add(SolutionTemplateRequestType, value.Key, value); err != nil {
			return nil, err
		}
	}
	for _, value := range snapshot.Workflows {
		if err := add(SolutionTemplateWorkflow, value.Key, value); err != nil {
			return nil, err
		}
	}
	for _, value := range snapshot.SLAPolicies {
		if err := add(SolutionTemplateSLA, value.Key, value); err != nil {
			return nil, err
		}
	}
	for _, value := range snapshot.Calendars {
		if err := add(SolutionTemplateCalendar, value.Key, value); err != nil {
			return nil, err
		}
	}
	for _, value := range snapshot.Routes {
		if err := add(SolutionTemplateRoute, value.Key, value); err != nil {
			return nil, err
		}
	}
	for _, value := range snapshot.Automations {
		if err := add(SolutionTemplateAutomation, value.Key, value); err != nil {
			return nil, err
		}
	}
	for _, value := range snapshot.ApprovalPolicies {
		if err := add(SolutionTemplateApproval, value.Key, value); err != nil {
			return nil, err
		}
	}
	for _, value := range snapshot.RiskPolicies {
		if err := add(SolutionTemplateRisk, value.Key, value); err != nil {
			return nil, err
		}
	}
	for key, value := range snapshot.Extensions {
		if err := add(SolutionTemplateExtension, key, value); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func requestTypeCompatibilityBreaks(
	from []RequestTypeTemplate,
	to []RequestTypeTemplate,
) []string {
	current := make(map[string]RequestTypeTemplate, len(to))
	for _, template := range to {
		current[template.Key] = template
	}
	var breaks []string
	for _, previous := range from {
		next, exists := current[previous.Key]
		if !exists {
			continue
		}
		if previous.WorkClass != next.WorkClass {
			breaks = append(
				breaks,
				fmt.Sprintf(
					"request_type:%s changed work_class from %s to %s",
					previous.Key,
					previous.WorkClass,
					next.WorkClass,
				),
			)
		}
		previousRequired := schemaRequiredFields(previous.JSONSchema)
		nextRequired := schemaRequiredFields(next.JSONSchema)
		for field := range nextRequired {
			if _, previouslyRequired := previousRequired[field]; !previouslyRequired {
				breaks = append(
					breaks,
					fmt.Sprintf(
						"request_type:%s added required field %s",
						previous.Key,
						field,
					),
				)
			}
		}
	}
	return breaks
}

func schemaRequiredFields(raw json.RawMessage) map[string]struct{} {
	var schema struct {
		Required []string `json:"required"`
	}
	_ = json.Unmarshal(raw, &schema)
	result := make(map[string]struct{}, len(schema.Required))
	for _, field := range schema.Required {
		result[field] = struct{}{}
	}
	return result
}

func workflowCompatibilityBreaks(
	from []WorkflowTemplate,
	to []WorkflowTemplate,
) []string {
	current := make(map[string]WorkflowTemplate, len(to))
	for _, workflow := range to {
		current[workflow.Key] = workflow
	}
	var breaks []string
	for _, previous := range from {
		next, exists := current[previous.Key]
		if !exists {
			continue
		}
		nextStates := make(map[string]WorkflowStateDefinition, len(next.States))
		for _, state := range next.States {
			nextStates[state.Key] = state
		}
		for _, state := range previous.States {
			nextState, exists := nextStates[state.Key]
			if !exists {
				breaks = append(
					breaks,
					fmt.Sprintf(
						"workflow:%s removed state %s",
						previous.Key,
						state.Key,
					),
				)
				continue
			}
			if state.LifecycleCategory != nextState.LifecycleCategory {
				breaks = append(
					breaks,
					fmt.Sprintf(
						"workflow:%s state %s changed lifecycle category",
						previous.Key,
						state.Key,
					),
				)
			}
		}
	}
	return breaks
}

func validateConfigurationKey(key string, kind string) error {
	if !configurationKeyPattern.MatchString(key) {
		return fmt.Errorf("%s key %q is invalid", kind, key)
	}
	return nil
}

func validateDraft202012Schema(raw json.RawMessage) error {
	if len(raw) == 0 {
		return errors.New("schema is required")
	}
	var schema map[string]json.RawMessage
	if err := decodeStrictJSON(raw, &schema); err != nil {
		return err
	}
	var dialect string
	if err := json.Unmarshal(schema["$schema"], &dialect); err != nil {
		return errors.New("schema must declare the JSON Schema dialect")
	}
	if strings.TrimSuffix(dialect, "#") != JSONSchemaDraft202012 {
		return fmt.Errorf("schema dialect must be %s", JSONSchemaDraft202012)
	}
	var rootType string
	if err := json.Unmarshal(schema["type"], &rootType); err != nil ||
		rootType != "object" {
		return errors.New("request type schema root type must be object")
	}
	return nil
}

func validateJSONObject(raw json.RawMessage, name string) error {
	if len(raw) == 0 {
		return fmt.Errorf("%s is required", name)
	}
	var object map[string]json.RawMessage
	if err := decodeStrictJSON(raw, &object); err != nil {
		return fmt.Errorf("%s must be a JSON object: %w", name, err)
	}
	return nil
}

func validateWorkflowDefinitions(
	states []WorkflowStateDefinition,
	transitions []WorkflowTransitionDefinition,
) error {
	if len(states) == 0 || len(states) > 64 {
		return errors.New("workflow must contain between 1 and 64 states")
	}
	stateKeys := make(map[string]WorkflowStateDefinition, len(states))
	initialCount := 0
	for _, state := range states {
		if err := validateConfigurationKey(state.Key, "workflow state"); err != nil {
			return err
		}
		if strings.TrimSpace(state.Name) == "" {
			return fmt.Errorf("workflow state %q requires a name", state.Key)
		}
		if !state.LifecycleCategory.IsValid() {
			return fmt.Errorf(
				"workflow state %q has invalid lifecycle category %q",
				state.Key,
				state.LifecycleCategory,
			)
		}
		if _, exists := stateKeys[state.Key]; exists {
			return fmt.Errorf("duplicate workflow state %q", state.Key)
		}
		stateKeys[state.Key] = state
		if state.IsInitial {
			initialCount++
		}
		if state.IsTerminal {
			switch state.LifecycleCategory {
			case LifecycleCategoryResolved,
				LifecycleCategoryClosed,
				LifecycleCategoryCancelled:
			default:
				return fmt.Errorf(
					"terminal workflow state %q has nonterminal lifecycle category %q",
					state.Key,
					state.LifecycleCategory,
				)
			}
		}
	}
	if initialCount != 1 {
		return fmt.Errorf("workflow requires exactly one initial state, got %d", initialCount)
	}
	if len(transitions) > 256 {
		return errors.New("workflow exceeds maximum transition count")
	}
	transitionKeys := make(map[string]struct{}, len(transitions))
	for _, transition := range transitions {
		if err := validateConfigurationKey(transition.Key, "workflow transition"); err != nil {
			return err
		}
		if strings.TrimSpace(transition.Name) == "" {
			return fmt.Errorf("workflow transition %q requires a name", transition.Key)
		}
		if _, exists := transitionKeys[transition.Key]; exists {
			return fmt.Errorf("duplicate workflow transition %q", transition.Key)
		}
		transitionKeys[transition.Key] = struct{}{}
		source, exists := stateKeys[transition.From]
		if !exists {
			return fmt.Errorf(
				"workflow transition %q references unknown source state %q",
				transition.Key,
				transition.From,
			)
		}
		target, exists := stateKeys[transition.To]
		if !exists {
			return fmt.Errorf(
				"workflow transition %q references unknown target state %q",
				transition.Key,
				transition.To,
			)
		}
		if source.LifecycleCategory == target.LifecycleCategory {
			return fmt.Errorf(
				"workflow transition %q connects lifecycle category %q to itself",
				transition.Key,
				source.LifecycleCategory,
			)
		}
		roles := make(map[ProjectRole]struct{}, len(transition.Roles))
		for _, role := range transition.Roles {
			if !role.IsValid() {
				return fmt.Errorf(
					"workflow transition %q has invalid role %q",
					transition.Key,
					role,
				)
			}
			if _, duplicate := roles[role]; duplicate {
				return fmt.Errorf(
					"workflow transition %q repeats role %q",
					transition.Key,
					role,
				)
			}
			roles[role] = struct{}{}
		}
	}
	return nil
}

func validateTypedExpressionValue(
	valueType ExpressionValueType,
	operator ExpressionOperator,
	raw json.RawMessage,
) error {
	switch operator {
	case ExpressionOperatorIn, ExpressionOperatorNotIn:
		if valueType != ExpressionValueStringList &&
			valueType != ExpressionValueNumberList {
			return errors.New("in/not_in operators require a list value type")
		}
	case ExpressionOperatorContains:
		if valueType != ExpressionValueString &&
			valueType != ExpressionValueStringList {
			return errors.New("contains operator requires string or string_list")
		}
	case ExpressionOperatorGreaterThan,
		ExpressionOperatorGreaterThanOrEqual,
		ExpressionOperatorLessThan,
		ExpressionOperatorLessThanOrEqual:
		if valueType != ExpressionValueNumber &&
			valueType != ExpressionValueTimestamp {
			return errors.New("ordered comparison requires number or timestamp")
		}
	}

	switch valueType {
	case ExpressionValueString:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return errors.New("expression value must be a string")
		}
	case ExpressionValueNumber:
		var value json.Number
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return errors.New("expression value must be a number")
		}
	case ExpressionValueBoolean:
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			return errors.New("expression value must be a boolean")
		}
	case ExpressionValueTimestamp:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return errors.New("expression timestamp must be a string")
		}
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return errors.New("expression timestamp must be RFC3339")
		}
	case ExpressionValueStringList:
		var values []string
		if err := json.Unmarshal(raw, &values); err != nil || len(values) == 0 {
			return errors.New("expression value must be a non-empty string list")
		}
	case ExpressionValueNumberList:
		var values []json.Number
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&values); err != nil || len(values) == 0 {
			return errors.New("expression value must be a non-empty number list")
		}
	default:
		return fmt.Errorf("invalid expression value type %q", valueType)
	}
	return nil
}

func validateUUIDReferences(ids []string, kind string) error {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != strings.ToLower(id) {
			return fmt.Errorf("%s version id %q is not a canonical UUID", kind, id)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("duplicate %s version id %q", kind, id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func validateConfigurationDefinitions(snapshot ConfigurationSnapshot) error {
	calendars := make(map[string]struct{}, len(snapshot.Calendars))
	for _, calendar := range snapshot.Calendars {
		if err := validateConfigurationKey(calendar.Key, "calendar"); err != nil {
			return err
		}
		if _, duplicate := calendars[calendar.Key]; duplicate {
			return fmt.Errorf("duplicate calendar %q", calendar.Key)
		}
		calendars[calendar.Key] = struct{}{}
		if strings.TrimSpace(calendar.Name) == "" ||
			strings.TrimSpace(calendar.Timezone) == "" {
			return fmt.Errorf("calendar %q requires name and timezone", calendar.Key)
		}
		if _, err := time.LoadLocation(calendar.Timezone); err != nil {
			return fmt.Errorf("calendar %q has invalid timezone", calendar.Key)
		}
		for _, window := range calendar.Windows {
			if window.Weekday < 0 || window.Weekday > 6 ||
				!clockPattern.MatchString(window.Start) ||
				!clockPattern.MatchString(window.End) ||
				window.Start >= window.End {
				return fmt.Errorf("calendar %q has invalid time window", calendar.Key)
			}
		}
		for _, holiday := range calendar.Holidays {
			if !datePattern.MatchString(holiday) {
				return fmt.Errorf("calendar %q has invalid holiday %q", calendar.Key, holiday)
			}
			if _, err := time.Parse("2006-01-02", holiday); err != nil {
				return fmt.Errorf("calendar %q has invalid holiday %q", calendar.Key, holiday)
			}
		}
	}

	slas := make(map[string]struct{}, len(snapshot.SLAPolicies))
	for _, policy := range snapshot.SLAPolicies {
		if err := validateConfigurationKey(policy.Key, "SLA policy"); err != nil {
			return err
		}
		if _, duplicate := slas[policy.Key]; duplicate {
			return fmt.Errorf("duplicate SLA policy %q", policy.Key)
		}
		slas[policy.Key] = struct{}{}
		if strings.TrimSpace(policy.Name) == "" ||
			policy.ResponseMinutes == 0 ||
			policy.ResolutionMinutes == 0 ||
			policy.ResponseMinutes > policy.ResolutionMinutes {
			return fmt.Errorf("SLA policy %q has invalid durations", policy.Key)
		}
		if _, exists := calendars[policy.CalendarKey]; !exists {
			return fmt.Errorf(
				"SLA policy %q references unknown calendar %q",
				policy.Key,
				policy.CalendarKey,
			)
		}
		for _, category := range policy.PauseWhen {
			if !category.IsValid() {
				return fmt.Errorf(
					"SLA policy %q has invalid pause category %q",
					policy.Key,
					category,
				)
			}
		}
		if policy.Applicability != nil {
			if err := policy.Applicability.Validate(); err != nil {
				return fmt.Errorf("SLA policy %q: %w", policy.Key, err)
			}
		}
	}

	if err := validateRoutes(snapshot.Routes); err != nil {
		return err
	}
	if err := validateAutomations(snapshot.Automations); err != nil {
		return err
	}
	approvals, err := validateApprovals(snapshot.ApprovalPolicies)
	if err != nil {
		return err
	}
	return validateRiskPolicies(snapshot.RiskPolicies, approvals)
}

func validateRoutes(routes []RouteDefinition) error {
	seen := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		if err := validateConfigurationKey(route.Key, "route"); err != nil {
			return err
		}
		if _, duplicate := seen[route.Key]; duplicate {
			return fmt.Errorf("duplicate route %q", route.Key)
		}
		seen[route.Key] = struct{}{}
		if strings.TrimSpace(route.Name) == "" {
			return fmt.Errorf("route %q requires a name", route.Key)
		}
		if err := route.When.Validate(); err != nil {
			return fmt.Errorf("route %q: %w", route.Key, err)
		}
		if err := ValidateQueueKey(route.QueueKey); err != nil {
			return fmt.Errorf("route %q: %w", route.Key, err)
		}
		if route.TeamKey != "" {
			if err := ValidateTeamKey(route.TeamKey); err != nil {
				return fmt.Errorf("route %q: %w", route.Key, err)
			}
		}
	}
	return nil
}

func validateAutomations(automations []AutomationDefinition) error {
	seen := make(map[string]struct{}, len(automations))
	for _, automation := range automations {
		if err := validateConfigurationKey(automation.Key, "automation"); err != nil {
			return err
		}
		if _, duplicate := seen[automation.Key]; duplicate {
			return fmt.Errorf("duplicate automation %q", automation.Key)
		}
		seen[automation.Key] = struct{}{}
		if strings.TrimSpace(automation.Name) == "" {
			return fmt.Errorf("automation %q requires a name", automation.Key)
		}
		if err := automation.When.Validate(); err != nil {
			return fmt.Errorf("automation %q: %w", automation.Key, err)
		}
		if len(automation.Actions) == 0 || len(automation.Actions) > 16 {
			return fmt.Errorf("automation %q has invalid action count", automation.Key)
		}
		for _, action := range automation.Actions {
			if err := action.Validate(); err != nil {
				return fmt.Errorf("automation %q: %w", automation.Key, err)
			}
		}
	}
	return nil
}

func validateApprovals(
	policies []ApprovalPolicyDefinition,
) (map[string]struct{}, error) {
	seen := make(map[string]struct{}, len(policies))
	for _, policy := range policies {
		if err := validateConfigurationKey(policy.Key, "approval policy"); err != nil {
			return nil, err
		}
		if _, duplicate := seen[policy.Key]; duplicate {
			return nil, fmt.Errorf("duplicate approval policy %q", policy.Key)
		}
		seen[policy.Key] = struct{}{}
		if strings.TrimSpace(policy.Name) == "" ||
			policy.RequiredApprovals < 1 ||
			policy.RequiredApprovals > 2 {
			return nil, fmt.Errorf("approval policy %q is invalid", policy.Key)
		}
		if err := policy.When.Validate(); err != nil {
			return nil, fmt.Errorf("approval policy %q: %w", policy.Key, err)
		}
		if len(policy.ApproverRoles) == 0 {
			return nil, fmt.Errorf("approval policy %q requires approver roles", policy.Key)
		}
		for _, role := range policy.ApproverRoles {
			if !role.IsValid() {
				return nil, fmt.Errorf(
					"approval policy %q has invalid role %q",
					policy.Key,
					role,
				)
			}
		}
	}
	return seen, nil
}

func validateRiskPolicies(
	policies []RiskPolicyDefinition,
	approvals map[string]struct{},
) error {
	seen := make(map[string]struct{}, len(policies))
	for _, policy := range policies {
		if err := validateConfigurationKey(policy.Key, "risk policy"); err != nil {
			return err
		}
		if _, duplicate := seen[policy.Key]; duplicate {
			return fmt.Errorf("duplicate risk policy %q", policy.Key)
		}
		seen[policy.Key] = struct{}{}
		if strings.TrimSpace(policy.Name) == "" || !policy.Level.IsValid() {
			return fmt.Errorf("risk policy %q is invalid", policy.Key)
		}
		if err := policy.When.Validate(); err != nil {
			return fmt.Errorf("risk policy %q: %w", policy.Key, err)
		}
		if policy.RequiresApproval {
			if _, exists := approvals[policy.ApprovalPolicyKey]; !exists {
				return fmt.Errorf(
					"risk policy %q references unknown approval policy %q",
					policy.Key,
					policy.ApprovalPolicyKey,
				)
			}
		} else if policy.ApprovalPolicyKey != "" {
			return fmt.Errorf(
				"risk policy %q has an unused approval policy",
				policy.Key,
			)
		}
	}
	return nil
}

func decodeStrictJSON(raw []byte, destination any) error {
	if len(raw) == 0 {
		return errors.New("JSON value is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("multiple JSON values are not allowed")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func hashCanonicalJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode canonical JSON: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
