package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

var (
	ErrTicketConfigurationUnavailable = errors.New(
		"published ticket configuration is unavailable",
	)
	ErrTicketRequestTypeAmbiguous = errors.New(
		"request type version must be selected explicitly",
	)
	ErrTicketFormValidation = errors.New("ticket form validation failed")
)

type resolvedTicketConfiguration struct {
	Release     models.ConfigurationRelease
	RequestType models.RequestTypeVersion
	Workflow    models.WorkflowVersion
}

func (configuration resolvedTicketConfiguration) InitialStatus() (
	models.TicketStatus,
	error,
) {
	states, err := configuration.Workflow.StateDefinitions()
	if err != nil {
		return "", err
	}
	for _, state := range states {
		if !state.IsInitial {
			continue
		}
		switch state.LifecycleCategory {
		case models.LifecycleCategoryNew:
			return models.TicketStatusOpen, nil
		case models.LifecycleCategoryActive:
			return models.TicketStatusInProgress, nil
		case models.LifecycleCategoryWaiting:
			return models.TicketStatusPending, nil
		case models.LifecycleCategoryResolved:
			return models.TicketStatusResolved, nil
		case models.LifecycleCategoryClosed:
			return models.TicketStatusClosed, nil
		case models.LifecycleCategoryCancelled:
			return models.TicketStatusCancelled, nil
		default:
			return "", errors.New("workflow initial lifecycle category is invalid")
		}
	}
	return "", errors.New("workflow has no initial state")
}

func resolveTicketConfigurationTx(
	ctx context.Context,
	tx *gorm.DB,
	scope models.ProjectScope,
	request *models.TicketCreateRequest,
	requestTypeVersionID string,
	workflowVersionID string,
) (*resolvedTicketConfiguration, error) {
	if tx == nil || request == nil {
		return nil, errors.New("ticket configuration input is required")
	}
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	requestTypeVersionID, err := matchingConfigurationVersionID(
		requestTypeVersionID,
		request.RequestTypeVersionID,
		"request type",
	)
	if err != nil {
		return nil, err
	}
	workflowVersionID, err = matchingConfigurationVersionID(
		workflowVersionID,
		request.WorkflowVersionID,
		"workflow",
	)
	if err != nil {
		return nil, err
	}

	var release models.ConfigurationRelease
	if err := scopedConfigurationQuery(tx.WithContext(ctx), scope).
		Where("status = ?", models.ConfigurationStatusPublished).
		Order("version DESC, published_at DESC, id DESC").
		First(&release).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTicketConfigurationUnavailable
		}
		return nil, fmt.Errorf("load published ticket configuration: %w", err)
	}
	snapshot, err := release.ConfigurationSnapshot()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTicketConfigurationUnavailable, err)
	}

	var requestTypes []models.RequestTypeVersion
	if err := scopedConfigurationQuery(tx.WithContext(ctx), scope).
		Where(
			"id IN ? AND status = ?",
			snapshot.RequestTypeVersionIDs,
			models.ConfigurationStatusPublished,
		).
		Find(&requestTypes).Error; err != nil {
		return nil, fmt.Errorf("load published request types: %w", err)
	}
	if len(requestTypes) != len(snapshot.RequestTypeVersionIDs) {
		return nil, ErrTicketConfigurationUnavailable
	}
	requestType, err := selectTicketRequestType(
		requestTypes,
		requestTypeVersionID,
		models.WorkClass(request.Type),
	)
	if err != nil {
		return nil, err
	}

	var workflows []models.WorkflowVersion
	if err := scopedConfigurationQuery(tx.WithContext(ctx), scope).
		Where(
			"id IN ? AND status = ?",
			snapshot.WorkflowVersionIDs,
			models.ConfigurationStatusPublished,
		).
		Find(&workflows).Error; err != nil {
		return nil, fmt.Errorf("load published workflows: %w", err)
	}
	if len(workflows) != len(snapshot.WorkflowVersionIDs) {
		return nil, ErrTicketConfigurationUnavailable
	}
	workflow, err := selectTicketWorkflow(workflows, workflowVersionID)
	if err != nil {
		return nil, err
	}
	if err := validateTicketRequestForm(*requestType, request); err != nil {
		return nil, err
	}
	return &resolvedTicketConfiguration{
		Release:     release,
		RequestType: *requestType,
		Workflow:    *workflow,
	}, nil
}

func matchingConfigurationVersionID(
	commandValue string,
	requestValue string,
	name string,
) (string, error) {
	commandValue = strings.TrimSpace(commandValue)
	requestValue = strings.TrimSpace(requestValue)
	if commandValue != "" && requestValue != "" &&
		commandValue != requestValue {
		return "", fmt.Errorf("%s version selection conflicts with request", name)
	}
	if commandValue != "" {
		return commandValue, nil
	}
	return requestValue, nil
}

func selectTicketRequestType(
	versions []models.RequestTypeVersion,
	versionID string,
	workClass models.WorkClass,
) (*models.RequestTypeVersion, error) {
	if !workClass.IsValid() {
		return nil, fmt.Errorf("invalid ticket work class %q", workClass)
	}
	versionID = strings.TrimSpace(versionID)
	var matches []models.RequestTypeVersion
	for _, version := range versions {
		if versionID != "" {
			if version.ID == versionID {
				matches = append(matches, version)
			}
			continue
		}
		if version.WorkClass == workClass {
			matches = append(matches, version)
		}
	}
	if len(matches) == 0 {
		return nil, ErrTicketConfigurationUnavailable
	}
	if len(matches) != 1 {
		return nil, ErrTicketRequestTypeAmbiguous
	}
	if matches[0].WorkClass != workClass {
		return nil, errors.New(
			"selected request type does not match ticket work class",
		)
	}
	return &matches[0], nil
}

func selectTicketWorkflow(
	workflows []models.WorkflowVersion,
	versionID string,
) (*models.WorkflowVersion, error) {
	versionID = strings.TrimSpace(versionID)
	var matches []models.WorkflowVersion
	for _, workflow := range workflows {
		if versionID == "" || workflow.ID == versionID {
			matches = append(matches, workflow)
		}
	}
	if len(matches) == 0 {
		return nil, ErrTicketConfigurationUnavailable
	}
	if len(matches) != 1 {
		return nil, errors.New("workflow version must be selected explicitly")
	}
	return &matches[0], nil
}

var ticketFormReservedFields = map[string]struct{}{
	"organization_id":         {},
	"project_id":              {},
	"queue_id":                {},
	"ticket_number":           {},
	"request_type_version_id": {},
	"workflow_version_id":     {},
	"external_id":             {},
	"external_ids":            {},
	"actor":                   {},
	"actor_id":                {},
	"credential_id":           {},
	"assigned_to_id":          {},
	"assigned_to_actor_id":    {},
	"service_principal_id":    {},
	"created_by_id":           {},
	"created_by_actor_id":     {},
	"title":                   {},
	"summary":                 {},
	"description":             {},
	"priority":                {},
	"work_class":              {},
	"type":                    {},
	"source":                  {},
}

func validateTicketRequestForm(
	requestType models.RequestTypeVersion,
	request *models.TicketCreateRequest,
) error {
	if request == nil {
		return ErrTicketFormValidation
	}
	var schemaProperties struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(requestType.JSONSchema, &schemaProperties); err != nil {
		return fmt.Errorf("%w: invalid published schema", ErrTicketFormValidation)
	}
	instance := make(map[string]any)
	if request.CustomFields != nil {
		for key, value := range request.CustomFields.ToMap() {
			key = strings.TrimSpace(key)
			if _, reserved := ticketFormReservedFields[key]; reserved {
				return fmt.Errorf(
					"%w: custom field %q is reserved",
					ErrTicketFormValidation,
					key,
				)
			}
			instance[key] = value
		}
	}
	derived := map[string]any{
		"title":       request.Title,
		"summary":     request.Title,
		"description": request.Description,
		"priority":    string(request.Priority),
		"work_class":  string(request.Type),
		"type":        string(request.Type),
		"source":      string(request.Source),
	}
	for key, value := range derived {
		if _, declared := schemaProperties.Properties[key]; declared {
			instance[key] = value
		}
	}

	var schema jsonschema.Schema
	if err := json.Unmarshal(requestType.JSONSchema, &schema); err != nil {
		return fmt.Errorf("%w: decode published schema", ErrTicketFormValidation)
	}
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{
		Loader: func(uri *url.URL) (*jsonschema.Schema, error) {
			return nil, fmt.Errorf(
				"external JSON Schema reference %q is forbidden",
				uri.String(),
			)
		},
	})
	if err != nil {
		return fmt.Errorf("%w: resolve published schema: %v", ErrTicketFormValidation, err)
	}
	if err := resolved.Validate(instance); err != nil {
		return fmt.Errorf("%w: %v", ErrTicketFormValidation, err)
	}
	return nil
}
