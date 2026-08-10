package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/seaworld008/chronodesk/server/internal/listcursor"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrIntegrationManagementInvalidInput = errors.New("invalid integration management input")
	ErrIntegrationManagementNotFound     = errors.New("integration management resource not found")
	ErrIntegrationManagementConflict     = errors.New("integration management state conflict")
	ErrIntegrationManagementImmutable    = models.ErrPublishedMappingImmutable
	ErrIntegrationTargetCommandDenied    = errors.New("integration target command is not allowed")
	ErrIntegrationManagementUnavailable  = errors.New("integration management dependency is unavailable")
	ErrIntegrationListCursorInvalid      = errors.New("integration list cursor is invalid")
)

const (
	integrationManagementMaximumJSONBytes = 2 << 20
	integrationManagementDefaultPageSize  = 25
	integrationManagementMaximumPageSize  = 100
	integrationManagementMaximumSearch    = 200
	integrationDomainEventCursorVersion   = 1
	integrationDomainEventSortVersion     = "created_at_desc_id_desc.v1"
)

var (
	integrationManagementKindPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	integrationKeyReferencePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,190}$`)
	allowedIntegrationTargetCommands = map[string]struct{}{
		"ticket.create":         {},
		"ticket.update":         {},
		"ticket.assign":         {},
		"ticket.transition":     {},
		"ticket.comment.create": {},
		"ticket.escalate":       {},
	}
)

// IntegrationManagementService owns the project-scoped administration rules
// for Connector definitions, Connections, Mapping versions and operational
// recovery. Organization, Project and Actor values are accepted only through a
// validated OperationContext.
type IntegrationManagementService struct {
	db                *gorm.DB
	inbox             *IntegrationInboxService
	domainEventCursor *listcursor.Codec
	now               func() time.Time
}

func NewIntegrationManagementService(
	db *gorm.DB,
	inbox *IntegrationInboxService,
) (*IntegrationManagementService, error) {
	if db == nil {
		return nil, errors.New("integration management database is required")
	}
	return &IntegrationManagementService{
		db:    db,
		inbox: inbox,
		now:   time.Now,
	}, nil
}

// ConfigureCursorSigningKey enables scope-bound, tamper-evident timeline
// cursors. The application root must pass an existing server secret; cursor
// material is derived for this list purpose and is never returned to callers.
func (service *IntegrationManagementService) ConfigureCursorSigningKey(
	rootKey []byte,
) error {
	if service == nil {
		return ErrIntegrationManagementUnavailable
	}
	codec, err := listcursor.NewCodec(
		rootKey,
		"integration-domain-events.v1",
	)
	if err != nil {
		return fmt.Errorf(
			"%w: configure integration cursor",
			ErrIntegrationManagementUnavailable,
		)
	}
	service.domainEventCursor = codec
	return nil
}

type IntegrationListOptions struct {
	Page               int
	PageSize           int
	SortBy             string
	SortOrder          string
	Search             string
	Status             string
	Type               string
	ConnectionPublicID string
}

type IntegrationPage[T any] struct {
	Items    []T   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

type IntegrationDomainEventCursorOptions struct {
	Cursor    string
	Limit     int
	EventType string
	Search    string
}

type IntegrationCursorPage[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
}

type integrationListSpec struct {
	defaultSortBy    string
	defaultSortOrder string
	sortColumns      map[string]string
	statuses         map[string]struct{}
	types            map[string]struct{}
}

type integrationDomainEventCursorPayload struct {
	Version        int       `json:"v"`
	OrganizationID uint      `json:"organization_id"`
	ProjectID      uint      `json:"project_id"`
	Limit          int       `json:"limit"`
	FilterDigest   string    `json:"filter_digest"`
	SortVersion    string    `json:"sort_version"`
	CreatedAt      time.Time `json:"created_at"`
	ID             string    `json:"id"`
}

type ConnectorDefinitionInput struct {
	Key                        string
	Name                       string
	Description                string
	Kind                       string
	Direction                  models.ConnectorDirection
	Status                     models.ConnectorDefinitionStatus
	SignatureScheme            string
	DefaultReplayWindowSeconds int
	ConfigurationSchema        json.RawMessage
	MappingSchema              json.RawMessage
}

type ConnectorDefinitionUpdateInput struct {
	Name                       string
	Description                string
	Status                     models.ConnectorDefinitionStatus
	SignatureScheme            string
	DefaultReplayWindowSeconds int
	ConfigurationSchema        json.RawMessage
	MappingSchema              json.RawMessage
	ExpectedUpdatedAt          time.Time
}

func (service *IntegrationManagementService) CreateConnectorDefinition(
	ctx context.Context,
	input ConnectorDefinitionInput,
) (*models.ConnectorDefinition, error) {
	operation, err := integrationManagementOperation(ctx)
	if err != nil {
		return nil, err
	}
	normalized, err := normalizeConnectorDefinitionInput(input)
	if err != nil {
		return nil, err
	}
	definition := &models.ConnectorDefinition{
		OrganizationID:             operation.Scope.OrganizationID,
		ProjectID:                  operation.Scope.ProjectID,
		Key:                        normalized.Key,
		Name:                       normalized.Name,
		Description:                normalized.Description,
		Kind:                       normalized.Kind,
		Direction:                  normalized.Direction,
		Status:                     normalized.Status,
		SignatureScheme:            normalized.SignatureScheme,
		DefaultReplayWindowSeconds: normalized.DefaultReplayWindowSeconds,
		ConfigurationSchema:        datatypes.JSON(normalized.ConfigurationSchema),
		MappingSchema:              datatypes.JSON(normalized.MappingSchema),
	}
	if err := service.db.WithContext(ctx).Create(definition).Error; err != nil {
		return nil, integrationManagementWriteError("create connector definition", err)
	}
	return definition, nil
}

func (service *IntegrationManagementService) UpdateConnectorDefinition(
	ctx context.Context,
	publicID string,
	input ConnectorDefinitionUpdateInput,
) (*models.ConnectorDefinition, error) {
	operation, err := integrationManagementOperation(ctx)
	if err != nil {
		return nil, err
	}
	publicID = strings.TrimSpace(publicID)
	if publicID == "" || input.ExpectedUpdatedAt.IsZero() {
		return nil, fmt.Errorf(
			"%w: connector id and expected_updated_at are required",
			ErrIntegrationManagementInvalidInput,
		)
	}
	var definition models.ConnectorDefinition
	if err := scopedIntegrationQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where("public_id = ?", publicID).First(&definition).Error; err != nil {
		return nil, integrationManagementLookupError(err)
	}
	normalized, err := normalizeConnectorDefinitionInput(ConnectorDefinitionInput{
		Key:                        definition.Key,
		Name:                       input.Name,
		Description:                input.Description,
		Kind:                       definition.Kind,
		Direction:                  definition.Direction,
		Status:                     input.Status,
		SignatureScheme:            input.SignatureScheme,
		DefaultReplayWindowSeconds: input.DefaultReplayWindowSeconds,
		ConfigurationSchema:        input.ConfigurationSchema,
		MappingSchema:              input.MappingSchema,
	})
	if err != nil {
		return nil, err
	}
	if definition.Status == models.ConnectorDefinitionStatusArchived {
		return nil, ErrIntegrationManagementImmutable
	}
	nextUpdatedAt := service.nextUpdatedAt(definition.UpdatedAt)
	updated := scopedIntegrationQuery(
		service.db.WithContext(ctx).Model(&models.ConnectorDefinition{}),
		operation.Scope,
	).Where(
		"id = ? AND updated_at = ? AND status <> ?",
		definition.ID,
		input.ExpectedUpdatedAt,
		models.ConnectorDefinitionStatusArchived,
	).UpdateColumns(map[string]any{
		"name":                          normalized.Name,
		"description":                   normalized.Description,
		"status":                        normalized.Status,
		"signature_scheme":              normalized.SignatureScheme,
		"default_replay_window_seconds": normalized.DefaultReplayWindowSeconds,
		"configuration_schema":          datatypes.JSON(normalized.ConfigurationSchema),
		"mapping_schema":                datatypes.JSON(normalized.MappingSchema),
		"updated_at":                    nextUpdatedAt,
	})
	if updated.Error != nil {
		return nil, integrationManagementWriteError("update connector definition", updated.Error)
	}
	if updated.RowsAffected != 1 {
		return nil, ErrIntegrationManagementConflict
	}
	if err := scopedIntegrationQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where("id = ?", definition.ID).First(&definition).Error; err != nil {
		return nil, integrationManagementLookupError(err)
	}
	return &definition, nil
}

func (service *IntegrationManagementService) ListConnectorDefinitions(
	ctx context.Context,
	options IntegrationListOptions,
) (IntegrationPage[models.ConnectorDefinition], error) {
	operation, err := integrationManagementOperation(ctx)
	if err != nil {
		return IntegrationPage[models.ConnectorDefinition]{}, err
	}
	options, err = normalizeIntegrationListOptions(
		options,
		integrationListSpec{
			defaultSortBy:    "created_at",
			defaultSortOrder: "desc",
			sortColumns: map[string]string{
				"created_at": "created_at",
				"updated_at": "updated_at",
				"name":       "name",
				"status":     "status",
				"id":         "id",
			},
			statuses: integrationStringSet(
				string(models.ConnectorDefinitionStatusActive),
				string(models.ConnectorDefinitionStatusDisabled),
				string(models.ConnectorDefinitionStatusArchived),
			),
		},
	)
	if err != nil {
		return IntegrationPage[models.ConnectorDefinition]{}, err
	}
	query := scopedIntegrationQuery(
		service.db.WithContext(ctx).Model(&models.ConnectorDefinition{}),
		operation.Scope,
	)
	if options.Status != "" {
		query = query.Where("status = ?", options.Status)
	}
	if options.Search != "" {
		pattern := "%" + escapeIntegrationLike(options.Search) + "%"
		query = query.Where(
			"(LOWER(key) LIKE LOWER(?) ESCAPE '\\' OR LOWER(name) LIKE LOWER(?) ESCAPE '\\' OR LOWER(kind) LIKE LOWER(?) ESCAPE '\\')",
			pattern,
			pattern,
			pattern,
		)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return IntegrationPage[models.ConnectorDefinition]{}, err
	}
	var items []models.ConnectorDefinition
	listQuery := query.Select(
		"id, public_id, created_at, updated_at, organization_id, project_id, key, name, description, kind, direction, status, signature_scheme, default_replay_window_seconds, " +
			"CASE WHEN configuration_schema IS NULL OR CAST(configuration_schema AS TEXT) IN ('', '{}', 'null') THEN NULL ELSE '{}' END AS configuration_schema, " +
			"CASE WHEN mapping_schema IS NULL OR CAST(mapping_schema AS TEXT) IN ('', '{}', 'null') THEN NULL ELSE '{}' END AS mapping_schema",
	)
	if err := applyIntegrationOrder(listQuery, options, integrationListSpec{
		sortColumns: map[string]string{
			"created_at": "created_at",
			"updated_at": "updated_at",
			"name":       "name",
			"status":     "status",
			"id":         "id",
		},
	}).
		Offset(integrationPageOffset(options)).
		Limit(options.PageSize).
		Find(&items).Error; err != nil {
		return IntegrationPage[models.ConnectorDefinition]{}, err
	}
	return IntegrationPage[models.ConnectorDefinition]{
		Items:    items,
		Total:    total,
		Page:     options.Page,
		PageSize: options.PageSize,
	}, nil
}

type ConnectionInput struct {
	ConnectorDefinitionPublicID string
	Key                         string
	Name                        string
	Description                 string
	Status                      models.ConnectionStatus
	Configuration               json.RawMessage
	VerificationKeyRef          string
	ReplayWindowSeconds         int
}

type ConnectionUpdateInput struct {
	Name                string
	Description         string
	Status              models.ConnectionStatus
	Configuration       json.RawMessage
	VerificationKeyRef  string
	ReplayWindowSeconds int
	ExpectedUpdatedAt   time.Time
}

func (service *IntegrationManagementService) CreateConnection(
	ctx context.Context,
	input ConnectionInput,
) (*models.Connection, error) {
	operation, err := integrationManagementOperation(ctx)
	if err != nil {
		return nil, err
	}
	normalized, err := normalizeConnectionInput(input)
	if err != nil {
		return nil, err
	}
	var definition models.ConnectorDefinition
	if err := scopedIntegrationQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where(
		"public_id = ? AND status <> ?",
		normalized.ConnectorDefinitionPublicID,
		models.ConnectorDefinitionStatusArchived,
	).First(&definition).Error; err != nil {
		return nil, integrationManagementLookupError(err)
	}
	connection := &models.Connection{
		OrganizationID:        operation.Scope.OrganizationID,
		ProjectID:             operation.Scope.ProjectID,
		ConnectorDefinitionID: definition.ID,
		Key:                   normalized.Key,
		Name:                  normalized.Name,
		Description:           normalized.Description,
		Status:                normalized.Status,
		Configuration:         datatypes.JSON(normalized.Configuration),
		VerificationKeyRef:    normalized.VerificationKeyRef,
		ReplayWindowSeconds:   normalized.ReplayWindowSeconds,
	}
	if err := service.db.WithContext(ctx).Create(connection).Error; err != nil {
		return nil, integrationManagementWriteError("create connection", err)
	}
	return connection, nil
}

func (service *IntegrationManagementService) UpdateConnection(
	ctx context.Context,
	publicID string,
	input ConnectionUpdateInput,
) (*models.Connection, error) {
	operation, err := integrationManagementOperation(ctx)
	if err != nil {
		return nil, err
	}
	publicID = strings.TrimSpace(publicID)
	if publicID == "" || input.ExpectedUpdatedAt.IsZero() {
		return nil, fmt.Errorf(
			"%w: connection id and expected_updated_at are required",
			ErrIntegrationManagementInvalidInput,
		)
	}
	var connection models.Connection
	if err := scopedIntegrationQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where("public_id = ?", publicID).First(&connection).Error; err != nil {
		return nil, integrationManagementLookupError(err)
	}
	normalized, err := normalizeConnectionInput(ConnectionInput{
		ConnectorDefinitionPublicID: "existing",
		Key:                         connection.Key,
		Name:                        input.Name,
		Description:                 input.Description,
		Status:                      input.Status,
		Configuration:               input.Configuration,
		VerificationKeyRef:          input.VerificationKeyRef,
		ReplayWindowSeconds:         input.ReplayWindowSeconds,
	})
	if err != nil {
		return nil, err
	}
	if connection.Status == models.ConnectionStatusArchived {
		return nil, ErrIntegrationManagementImmutable
	}
	nextUpdatedAt := service.nextUpdatedAt(connection.UpdatedAt)
	updated := scopedIntegrationQuery(
		service.db.WithContext(ctx).Model(&models.Connection{}),
		operation.Scope,
	).Where(
		"id = ? AND updated_at = ? AND status <> ?",
		connection.ID,
		input.ExpectedUpdatedAt,
		models.ConnectionStatusArchived,
	).UpdateColumns(map[string]any{
		"name":                  normalized.Name,
		"description":           normalized.Description,
		"status":                normalized.Status,
		"configuration":         datatypes.JSON(normalized.Configuration),
		"verification_key_ref":  normalized.VerificationKeyRef,
		"replay_window_seconds": normalized.ReplayWindowSeconds,
		"updated_at":            nextUpdatedAt,
	})
	if updated.Error != nil {
		return nil, integrationManagementWriteError("update connection", updated.Error)
	}
	if updated.RowsAffected != 1 {
		return nil, ErrIntegrationManagementConflict
	}
	if err := scopedIntegrationQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where("id = ?", connection.ID).First(&connection).Error; err != nil {
		return nil, integrationManagementLookupError(err)
	}
	return &connection, nil
}

func (service *IntegrationManagementService) ListConnections(
	ctx context.Context,
	options IntegrationListOptions,
) (IntegrationPage[models.Connection], error) {
	operation, err := integrationManagementOperation(ctx)
	if err != nil {
		return IntegrationPage[models.Connection]{}, err
	}
	spec := integrationListSpec{
		defaultSortBy:    "created_at",
		defaultSortOrder: "desc",
		sortColumns: map[string]string{
			"created_at": "created_at",
			"updated_at": "updated_at",
			"name":       "name",
			"status":     "status",
			"id":         "id",
		},
		statuses: integrationStringSet(
			string(models.ConnectionStatusActive),
			string(models.ConnectionStatusInactive),
			string(models.ConnectionStatusError),
			string(models.ConnectionStatusArchived),
		),
	}
	options, err = normalizeIntegrationListOptions(options, spec)
	if err != nil {
		return IntegrationPage[models.Connection]{}, err
	}
	query := scopedIntegrationQuery(
		service.db.WithContext(ctx).Model(&models.Connection{}),
		operation.Scope,
	)
	if options.Status != "" {
		query = query.Where("status = ?", options.Status)
	}
	if options.Search != "" {
		pattern := "%" + escapeIntegrationLike(options.Search) + "%"
		query = query.Where(
			"(LOWER(key) LIKE LOWER(?) ESCAPE '\\' OR LOWER(name) LIKE LOWER(?) ESCAPE '\\')",
			pattern,
			pattern,
		)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return IntegrationPage[models.Connection]{}, err
	}
	var items []models.Connection
	listQuery := query.Select(
		"id, public_id, created_at, updated_at, organization_id, project_id, connector_definition_id, key, name, description, status, replay_window_seconds, actor_type, actor_id, actor_credential_id, last_verified_at, last_error_at, last_error_code, " +
			"CASE WHEN configuration IS NULL OR CAST(configuration AS TEXT) IN ('', '{}', 'null') THEN NULL ELSE '{}' END AS configuration, " +
			"CASE WHEN TRIM(verification_key_ref) = '' THEN '' ELSE 'configured' END AS verification_key_ref",
	)
	if err := applyIntegrationOrder(listQuery, options, spec).
		Offset(integrationPageOffset(options)).
		Limit(options.PageSize).
		Find(&items).Error; err != nil {
		return IntegrationPage[models.Connection]{}, err
	}
	return IntegrationPage[models.Connection]{
		Items:    items,
		Total:    total,
		Page:     options.Page,
		PageSize: options.PageSize,
	}, nil
}

type MappingDraftInput struct {
	ConnectionPublicID string
	Key                string
	SourceSchema       json.RawMessage
	TargetCommand      string
	Definition         json.RawMessage
}

type MappingDraftUpdateInput struct {
	SourceSchema             json.RawMessage
	TargetCommand            string
	Definition               json.RawMessage
	ExpectedDefinitionDigest string
	ExpectedUpdatedAt        time.Time
}

type MappingPublishInput struct {
	ExpectedDefinitionDigest string
	ExpectedUpdatedAt        time.Time
}

func (service *IntegrationManagementService) CreateMappingDraft(
	ctx context.Context,
	input MappingDraftInput,
) (*models.MappingVersion, error) {
	operation, err := integrationManagementOperation(ctx)
	if err != nil {
		return nil, err
	}
	input.ConnectionPublicID = strings.TrimSpace(input.ConnectionPublicID)
	input.Key = strings.TrimSpace(input.Key)
	input.TargetCommand = strings.TrimSpace(input.TargetCommand)
	if input.ConnectionPublicID == "" ||
		!integrationManagementKindPattern.MatchString(input.Key) {
		return nil, fmt.Errorf(
			"%w: connection and mapping key are required",
			ErrIntegrationManagementInvalidInput,
		)
	}
	sourceSchema, err := normalizeJSONObject(input.SourceSchema, false)
	if err != nil {
		return nil, err
	}
	definition, err := normalizeJSONObject(input.Definition, true)
	if err != nil {
		return nil, err
	}
	if err := validateIntegrationTargetCommand(input.TargetCommand); err != nil {
		return nil, err
	}

	var created models.MappingVersion
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		var connection models.Connection
		if err := scopedIntegrationQuery(tx, operation.Scope).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(
				"public_id = ? AND status <> ?",
				input.ConnectionPublicID,
				models.ConnectionStatusArchived,
			).
			First(&connection).Error; err != nil {
			return integrationManagementLookupError(err)
		}
		var maximum uint
		if err := scopedIntegrationQuery(
			tx.Model(&models.MappingVersion{}),
			operation.Scope,
		).Where(
			"connection_id = ? AND key = ?",
			connection.ID,
			input.Key,
		).Select("COALESCE(MAX(version), 0)").Scan(&maximum).Error; err != nil {
			return fmt.Errorf("allocate mapping version: %w", err)
		}
		created = models.MappingVersion{
			OrganizationID: operation.Scope.OrganizationID,
			ProjectID:      operation.Scope.ProjectID,
			ConnectionID:   connection.ID,
			Key:            input.Key,
			Version:        maximum + 1,
			Status:         models.MappingVersionStatusDraft,
			SourceSchema:   datatypes.JSON(sourceSchema),
			TargetCommand:  input.TargetCommand,
			Definition:     datatypes.JSON(definition),
		}
		if err := tx.Create(&created).Error; err != nil {
			return integrationManagementWriteError("create mapping draft", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

func (service *IntegrationManagementService) UpdateMappingDraft(
	ctx context.Context,
	publicID string,
	input MappingDraftUpdateInput,
) (*models.MappingVersion, error) {
	operation, err := integrationManagementOperation(ctx)
	if err != nil {
		return nil, err
	}
	publicID = strings.TrimSpace(publicID)
	expectedDigest, err := normalizeDefinitionDigest(input.ExpectedDefinitionDigest)
	if err != nil || publicID == "" || input.ExpectedUpdatedAt.IsZero() {
		return nil, fmt.Errorf(
			"%w: mapping id, digest and expected_updated_at are required",
			ErrIntegrationManagementInvalidInput,
		)
	}
	sourceSchema, err := normalizeJSONObject(input.SourceSchema, false)
	if err != nil {
		return nil, err
	}
	definition, err := normalizeJSONObject(input.Definition, true)
	if err != nil {
		return nil, err
	}
	targetCommand := strings.TrimSpace(input.TargetCommand)
	if err := validateIntegrationTargetCommand(targetCommand); err != nil {
		return nil, err
	}
	digest := integrationJSONDigest(definition)

	var mapping models.MappingVersion
	if err := scopedIntegrationQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where("public_id = ?", publicID).First(&mapping).Error; err != nil {
		return nil, integrationManagementLookupError(err)
	}
	if mapping.Status != models.MappingVersionStatusDraft {
		return nil, ErrIntegrationManagementImmutable
	}
	nextUpdatedAt := service.nextUpdatedAt(mapping.UpdatedAt)
	updated := scopedIntegrationQuery(
		service.db.WithContext(ctx).Model(&models.MappingVersion{}),
		operation.Scope,
	).Where(
		"id = ? AND status = ? AND definition_digest = ? AND updated_at = ?",
		mapping.ID,
		models.MappingVersionStatusDraft,
		expectedDigest,
		input.ExpectedUpdatedAt,
	).UpdateColumns(map[string]any{
		"source_schema":     datatypes.JSON(sourceSchema),
		"target_command":    targetCommand,
		"definition":        datatypes.JSON(definition),
		"definition_digest": digest,
		"updated_at":        nextUpdatedAt,
	})
	if updated.Error != nil {
		return nil, integrationManagementWriteError("update mapping draft", updated.Error)
	}
	if updated.RowsAffected != 1 {
		return nil, ErrIntegrationManagementConflict
	}
	if err := scopedIntegrationQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where("id = ?", mapping.ID).First(&mapping).Error; err != nil {
		return nil, integrationManagementLookupError(err)
	}
	return &mapping, nil
}

func (service *IntegrationManagementService) PublishMapping(
	ctx context.Context,
	publicID string,
	input MappingPublishInput,
) (*models.MappingVersion, error) {
	operation, err := integrationManagementOperation(ctx)
	if err != nil {
		return nil, err
	}
	publicID = strings.TrimSpace(publicID)
	expectedDigest, err := normalizeDefinitionDigest(input.ExpectedDefinitionDigest)
	if err != nil || publicID == "" || input.ExpectedUpdatedAt.IsZero() {
		return nil, fmt.Errorf(
			"%w: mapping id, digest and expected_updated_at are required",
			ErrIntegrationManagementInvalidInput,
		)
	}
	var mapping models.MappingVersion
	if err := scopedIntegrationQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where("public_id = ?", publicID).First(&mapping).Error; err != nil {
		return nil, integrationManagementLookupError(err)
	}
	if mapping.Status != models.MappingVersionStatusDraft {
		return nil, ErrIntegrationManagementImmutable
	}
	if integrationJSONDigest(mapping.Definition) != mapping.DefinitionDigest {
		return nil, ErrIntegrationManagementConflict
	}
	if err := validateIntegrationTargetCommand(mapping.TargetCommand); err != nil {
		return nil, err
	}
	publishedAt := service.nextUpdatedAt(mapping.UpdatedAt)
	updated := scopedIntegrationQuery(
		service.db.WithContext(ctx).Model(&models.MappingVersion{}),
		operation.Scope,
	).Where(
		"id = ? AND status = ? AND definition_digest = ? AND updated_at = ?",
		mapping.ID,
		models.MappingVersionStatusDraft,
		expectedDigest,
		input.ExpectedUpdatedAt,
	).UpdateColumns(map[string]any{
		"status":            models.MappingVersionStatusPublished,
		"published_at":      publishedAt,
		"published_by_type": operation.Actor.Type,
		"published_by_id":   operation.Actor.ID,
		"updated_at":        publishedAt,
	})
	if updated.Error != nil {
		return nil, integrationManagementWriteError("publish mapping", updated.Error)
	}
	if updated.RowsAffected != 1 {
		return nil, ErrIntegrationManagementConflict
	}
	if err := scopedIntegrationQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where("id = ?", mapping.ID).First(&mapping).Error; err != nil {
		return nil, integrationManagementLookupError(err)
	}
	return &mapping, nil
}

func (service *IntegrationManagementService) ListMappings(
	ctx context.Context,
	connectionPublicID string,
	options IntegrationListOptions,
) (IntegrationPage[models.MappingVersion], error) {
	operation, err := integrationManagementOperation(ctx)
	if err != nil {
		return IntegrationPage[models.MappingVersion]{}, err
	}
	var connection models.Connection
	if err := scopedIntegrationQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where(
		"public_id = ?",
		strings.TrimSpace(connectionPublicID),
	).First(&connection).Error; err != nil {
		return IntegrationPage[models.MappingVersion]{}, integrationManagementLookupError(err)
	}
	spec := integrationListSpec{
		defaultSortBy:    "created_at",
		defaultSortOrder: "desc",
		sortColumns: map[string]string{
			"created_at": "created_at",
			"updated_at": "updated_at",
			"key":        "key",
			"version":    "version",
			"status":     "status",
			"id":         "id",
		},
		statuses: integrationStringSet(
			string(models.MappingVersionStatusDraft),
			string(models.MappingVersionStatusPublished),
			string(models.MappingVersionStatusRetired),
		),
	}
	options, err = normalizeIntegrationListOptions(options, spec)
	if err != nil {
		return IntegrationPage[models.MappingVersion]{}, err
	}
	query := scopedIntegrationQuery(
		service.db.WithContext(ctx).Model(&models.MappingVersion{}),
		operation.Scope,
	).Where("connection_id = ?", connection.ID)
	if options.Status != "" {
		query = query.Where("status = ?", options.Status)
	}
	if options.Search != "" {
		pattern := "%" + escapeIntegrationLike(options.Search) + "%"
		query = query.Where(
			"(LOWER(key) LIKE LOWER(?) ESCAPE '\\' OR LOWER(target_command) LIKE LOWER(?) ESCAPE '\\')",
			pattern,
			pattern,
		)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return IntegrationPage[models.MappingVersion]{}, err
	}
	var items []models.MappingVersion
	listQuery := query.Select(
		"id, public_id, created_at, updated_at, organization_id, project_id, connection_id, key, version, status, target_command, definition_digest, published_at, published_by_type, published_by_id",
	)
	if err := applyIntegrationOrder(listQuery, options, spec).
		Offset(integrationPageOffset(options)).
		Limit(options.PageSize).
		Find(&items).Error; err != nil {
		return IntegrationPage[models.MappingVersion]{}, err
	}
	return IntegrationPage[models.MappingVersion]{
		Items:    items,
		Total:    total,
		Page:     options.Page,
		PageSize: options.PageSize,
	}, nil
}

func (service *IntegrationManagementService) DryRunMapping(
	ctx context.Context,
	mappingPublicID string,
	payload []byte,
) (IntegrationMappingDryRunResult, error) {
	operation, err := integrationManagementOperation(ctx)
	if err != nil {
		return IntegrationMappingDryRunResult{}, err
	}
	if service.inbox == nil || service.inbox.dryRunner == nil {
		return IntegrationMappingDryRunResult{}, ErrIntegrationManagementUnavailable
	}
	if len(payload) == 0 || len(payload) > integrationManagementMaximumJSONBytes ||
		!json.Valid(payload) {
		return IntegrationMappingDryRunResult{}, fmt.Errorf(
			"%w: dry-run payload must be valid JSON",
			ErrIntegrationManagementInvalidInput,
		)
	}
	var mapping models.MappingVersion
	if err := scopedIntegrationQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where(
		"public_id = ?",
		strings.TrimSpace(mappingPublicID),
	).First(&mapping).Error; err != nil {
		return IntegrationMappingDryRunResult{}, integrationManagementLookupError(err)
	}
	if err := validateIntegrationTargetCommand(mapping.TargetCommand); err != nil {
		return IntegrationMappingDryRunResult{}, err
	}
	var connection models.Connection
	if err := scopedIntegrationQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where(
		"id = ? AND status <> ?",
		mapping.ConnectionID,
		models.ConnectionStatusArchived,
	).First(&connection).Error; err != nil {
		return IntegrationMappingDryRunResult{}, integrationManagementLookupError(err)
	}
	var connector models.ConnectorDefinition
	if err := scopedIntegrationQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where(
		"id = ? AND status <> ?",
		connection.ConnectorDefinitionID,
		models.ConnectorDefinitionStatusArchived,
	).First(&connector).Error; err != nil {
		return IntegrationMappingDryRunResult{}, integrationManagementLookupError(err)
	}
	result, err := service.inbox.dryRunner.DryRun(
		ctx,
		IntegrationMappingDryRunRequest{
			Connection: &connection,
			Connector:  &connector,
			Mapping:    &mapping,
			Payload:    append([]byte(nil), payload...),
		},
	)
	if err != nil {
		return IntegrationMappingDryRunResult{}, err
	}
	result.MappingVersionID = mapping.ID
	result.PayloadDigest = integrationPayloadDigest(payload)
	result.TargetCommand = mapping.TargetCommand
	return result, nil
}

type IntegrationConnectionHealth struct {
	PublicID      string                  `json:"public_id"`
	Key           string                  `json:"key"`
	Name          string                  `json:"name"`
	Status        models.ConnectionStatus `json:"status"`
	LastVerified  *time.Time              `json:"last_verified_at,omitempty"`
	LastErrorAt   *time.Time              `json:"last_error_at,omitempty"`
	LastErrorCode string                  `json:"last_error_code,omitempty"`
	LastRun       *models.SyncRun         `json:"last_run,omitempty"`
}

type IntegrationOverview struct {
	ConnectorDefinitions      int64                         `json:"connector_definitions"`
	Connections               int64                         `json:"connections"`
	ActiveConnections         int64                         `json:"active_connections"`
	ErrorConnections          int64                         `json:"error_connections"`
	OpenConflicts             int64                         `json:"open_conflicts"`
	OpenDeadLetters           int64                         `json:"open_dead_letters"`
	RunningSyncRuns           int64                         `json:"running_sync_runs"`
	RecentRuns                []models.SyncRun              `json:"recent_runs"`
	RecentRunsLimit           int                           `json:"recent_runs_limit"`
	RecentRunsTruncated       bool                          `json:"recent_runs_truncated"`
	ConnectionHealth          []IntegrationConnectionHealth `json:"connection_health"`
	ConnectionHealthLimit     int                           `json:"connection_health_limit"`
	ConnectionHealthTruncated bool                          `json:"connection_health_truncated"`
}

func (service *IntegrationManagementService) Overview(
	ctx context.Context,
) (*IntegrationOverview, error) {
	operation, err := integrationManagementOperation(ctx)
	if err != nil {
		return nil, err
	}
	scope := operation.Scope
	overview := &IntegrationOverview{}
	counts := []struct {
		model any
		where string
		args  []any
		value *int64
	}{
		{&models.ConnectorDefinition{}, "", nil, &overview.ConnectorDefinitions},
		{&models.Connection{}, "", nil, &overview.Connections},
		{&models.Connection{}, "status = ?", []any{models.ConnectionStatusActive}, &overview.ActiveConnections},
		{&models.Connection{}, "status = ?", []any{models.ConnectionStatusError}, &overview.ErrorConnections},
		{&models.IntegrationConflict{}, "status = ?", []any{models.IntegrationConflictStatusOpen}, &overview.OpenConflicts},
		{&models.DeadLetter{}, "status = ?", []any{models.DeadLetterStatusOpen}, &overview.OpenDeadLetters},
		{&models.SyncRun{}, "status = ?", []any{models.SyncRunStatusRunning}, &overview.RunningSyncRuns},
	}
	for _, count := range counts {
		query := scopedIntegrationQuery(
			service.db.WithContext(ctx).Model(count.model),
			scope,
		)
		if count.where != "" {
			query = query.Where(count.where, count.args...)
		}
		if err := query.Count(count.value).Error; err != nil {
			return nil, err
		}
	}
	const recentRunLimit = 20
	if err := scopedIntegrationQuery(
		service.db.WithContext(ctx),
		scope,
	).Select(
		"id, public_id, created_at, updated_at, organization_id, project_id, connection_id, run_key, direction, status, started_at, finished_at, processed_count, succeeded_count, failed_count, conflict_count, error_code",
	).Order("created_at DESC, id DESC").
		Limit(recentRunLimit + 1).
		Find(&overview.RecentRuns).Error; err != nil {
		return nil, err
	}
	overview.RecentRunsLimit = recentRunLimit
	overview.RecentRunsTruncated = len(overview.RecentRuns) > recentRunLimit
	if overview.RecentRunsTruncated {
		overview.RecentRuns = overview.RecentRuns[:recentRunLimit]
	}
	var connections []models.Connection
	if err := scopedIntegrationQuery(
		service.db.WithContext(ctx),
		scope,
	).Select(
		"id, public_id, created_at, updated_at, organization_id, project_id, key, name, status, last_verified_at, last_error_at, last_error_code",
	).Where("status <> ?", models.ConnectionStatusArchived).
		Order("name ASC, id ASC").
		Limit(integrationManagementMaximumPageSize + 1).
		Find(&connections).Error; err != nil {
		return nil, err
	}
	overview.ConnectionHealthLimit = integrationManagementMaximumPageSize
	overview.ConnectionHealthTruncated =
		len(connections) > integrationManagementMaximumPageSize
	if overview.ConnectionHealthTruncated {
		connections = connections[:integrationManagementMaximumPageSize]
	}
	connectionIDs := make([]uint, 0, len(connections))
	for index := range connections {
		connectionIDs = append(connectionIDs, connections[index].ID)
	}
	latestRuns := make(map[uint]models.SyncRun, len(connectionIDs))
	if len(connectionIDs) > 0 {
		ranked := scopedIntegrationQuery(
			service.db.WithContext(ctx).Model(&models.SyncRun{}),
			scope,
		).Select(
			"id, public_id, created_at, updated_at, organization_id, project_id, connection_id, run_key, direction, status, started_at, finished_at, processed_count, succeeded_count, failed_count, conflict_count, error_code, ROW_NUMBER() OVER (PARTITION BY connection_id ORDER BY created_at DESC, id DESC) AS integration_row_number",
		).Where("connection_id IN ?", connectionIDs)
		var runs []models.SyncRun
		if err := service.db.WithContext(ctx).
			Table("(?) AS ranked_sync_runs", ranked).
			Where("integration_row_number = 1").
			Find(&runs).Error; err != nil {
			return nil, err
		}
		for index := range runs {
			latestRuns[runs[index].ConnectionID] = runs[index]
		}
	}
	overview.ConnectionHealth = make([]IntegrationConnectionHealth, 0, len(connections))
	for _, connection := range connections {
		health := IntegrationConnectionHealth{
			PublicID:      connection.PublicID,
			Key:           connection.Key,
			Name:          connection.Name,
			Status:        connection.Status,
			LastVerified:  connection.LastVerifiedAt,
			LastErrorAt:   connection.LastErrorAt,
			LastErrorCode: connection.LastErrorCode,
		}
		if run, ok := latestRuns[connection.ID]; ok {
			health.LastRun = &run
		}
		overview.ConnectionHealth = append(overview.ConnectionHealth, health)
	}
	return overview, nil
}

func (service *IntegrationManagementService) ListConflicts(
	ctx context.Context,
	options IntegrationListOptions,
) (IntegrationPage[models.IntegrationConflict], error) {
	operation, err := integrationManagementOperation(ctx)
	if err != nil {
		return IntegrationPage[models.IntegrationConflict]{}, err
	}
	spec := integrationListSpec{
		defaultSortBy:    "created_at",
		defaultSortOrder: "desc",
		sortColumns: map[string]string{
			"created_at": "created_at",
			"updated_at": "updated_at",
			"status":     "status",
			"type":       "type",
			"id":         "id",
		},
		statuses: integrationStringSet(
			string(models.IntegrationConflictStatusOpen),
			string(models.IntegrationConflictStatusResolved),
			string(models.IntegrationConflictStatusIgnored),
		),
		types: integrationStringSet(
			string(models.IntegrationConflictMessageIdentityReuse),
			string(models.IntegrationConflictExternalLinkMismatch),
			string(models.IntegrationConflictInternalLinkCollision),
		),
	}
	options, err = normalizeIntegrationListOptions(options, spec)
	if err != nil {
		return IntegrationPage[models.IntegrationConflict]{}, err
	}
	query := scopedIntegrationQuery(
		service.db.WithContext(ctx).Model(&models.IntegrationConflict{}),
		operation.Scope,
	)
	if options.Status != "" {
		query = query.Where("status = ?", options.Status)
	}
	if options.Type != "" {
		query = query.Where("type = ?", options.Type)
	}
	if options.Search != "" {
		pattern := "%" + escapeIntegrationLike(options.Search) + "%"
		query = query.Where(
			"(LOWER(external_resource_type) LIKE LOWER(?) ESCAPE '\\' OR LOWER(external_resource_id) LIKE LOWER(?) ESCAPE '\\')",
			pattern,
			pattern,
		)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return IntegrationPage[models.IntegrationConflict]{}, err
	}
	var items []models.IntegrationConflict
	listQuery := query.Select(
		"id, public_id, created_at, updated_at, organization_id, project_id, connection_id, inbox_message_id, conflict_key, type, status, external_resource_type, external_resource_id, existing_internal_resource_type, existing_internal_resource_id, incoming_internal_resource_type, incoming_internal_resource_id, resolved_at, resolved_by_type, resolved_by_id",
	)
	if err := applyIntegrationOrder(listQuery, options, spec).
		Offset(integrationPageOffset(options)).
		Limit(options.PageSize).
		Find(&items).Error; err != nil {
		return IntegrationPage[models.IntegrationConflict]{}, err
	}
	return IntegrationPage[models.IntegrationConflict]{
		Items:    items,
		Total:    total,
		Page:     options.Page,
		PageSize: options.PageSize,
	}, nil
}

type IntegrationConflictResolution string

const (
	IntegrationConflictResolve IntegrationConflictResolution = "resolved"
	IntegrationConflictIgnore  IntegrationConflictResolution = "ignored"
)

type ResolveIntegrationConflictInput struct {
	Resolution        IntegrationConflictResolution
	ExpectedUpdatedAt time.Time
}

func (service *IntegrationManagementService) ResolveConflict(
	ctx context.Context,
	publicID string,
	input ResolveIntegrationConflictInput,
) (*models.IntegrationConflict, error) {
	operation, err := integrationManagementOperation(ctx)
	if err != nil {
		return nil, err
	}
	publicID = strings.TrimSpace(publicID)
	if publicID == "" || input.ExpectedUpdatedAt.IsZero() {
		return nil, fmt.Errorf(
			"%w: conflict id and expected_updated_at are required",
			ErrIntegrationManagementInvalidInput,
		)
	}
	var nextStatus models.IntegrationConflictStatus
	switch input.Resolution {
	case IntegrationConflictResolve:
		nextStatus = models.IntegrationConflictStatusResolved
	case IntegrationConflictIgnore:
		nextStatus = models.IntegrationConflictStatusIgnored
	default:
		return nil, fmt.Errorf(
			"%w: unsupported conflict resolution",
			ErrIntegrationManagementInvalidInput,
		)
	}
	var conflict models.IntegrationConflict
	if err := scopedIntegrationQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where("public_id = ?", publicID).First(&conflict).Error; err != nil {
		return nil, integrationManagementLookupError(err)
	}
	if conflict.Status != models.IntegrationConflictStatusOpen {
		return nil, ErrIntegrationManagementConflict
	}
	resolvedAt := service.nextUpdatedAt(conflict.UpdatedAt)
	updated := scopedIntegrationQuery(
		service.db.WithContext(ctx).Model(&models.IntegrationConflict{}),
		operation.Scope,
	).Where(
		"id = ? AND status = ? AND updated_at = ?",
		conflict.ID,
		models.IntegrationConflictStatusOpen,
		input.ExpectedUpdatedAt,
	).UpdateColumns(map[string]any{
		"status":           nextStatus,
		"resolved_at":      resolvedAt,
		"resolved_by_type": operation.Actor.Type,
		"resolved_by_id":   operation.Actor.ID,
		"updated_at":       resolvedAt,
	})
	if updated.Error != nil {
		return nil, integrationManagementWriteError("resolve integration conflict", updated.Error)
	}
	if updated.RowsAffected != 1 {
		return nil, ErrIntegrationManagementConflict
	}
	if err := scopedIntegrationQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where("id = ?", conflict.ID).First(&conflict).Error; err != nil {
		return nil, integrationManagementLookupError(err)
	}
	return &conflict, nil
}

func (service *IntegrationManagementService) ListDeadLetters(
	ctx context.Context,
	options IntegrationListOptions,
) (IntegrationPage[models.DeadLetter], error) {
	operation, err := integrationManagementOperation(ctx)
	if err != nil {
		return IntegrationPage[models.DeadLetter]{}, err
	}
	spec := integrationListSpec{
		defaultSortBy:    "created_at",
		defaultSortOrder: "desc",
		sortColumns: map[string]string{
			"created_at":    "created_at",
			"updated_at":    "updated_at",
			"status":        "status",
			"attempt_count": "attempt_count",
			"id":            "id",
		},
		statuses: integrationStringSet(
			string(models.DeadLetterStatusOpen),
			string(models.DeadLetterStatusRequeued),
			string(models.DeadLetterStatusResolved),
		),
	}
	options, err = normalizeIntegrationListOptions(options, spec)
	if err != nil {
		return IntegrationPage[models.DeadLetter]{}, err
	}
	query := scopedIntegrationQuery(
		service.db.WithContext(ctx).Model(&models.DeadLetter{}),
		operation.Scope,
	)
	if options.Status != "" {
		query = query.Where("status = ?", options.Status)
	}
	if options.Search != "" {
		pattern := "%" + escapeIntegrationLike(options.Search) + "%"
		query = query.Where(
			"LOWER(reason_code) LIKE LOWER(?) ESCAPE '\\'",
			pattern,
		)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return IntegrationPage[models.DeadLetter]{}, err
	}
	var items []models.DeadLetter
	listQuery := query.Select(
		"id, public_id, created_at, updated_at, organization_id, project_id, connection_id, inbox_message_id, status, reason_code, attempt_count, next_attempt_at, resolved_at, resolved_by_type, resolved_by_id",
	)
	if err := applyIntegrationOrder(listQuery, options, spec).
		Offset(integrationPageOffset(options)).
		Limit(options.PageSize).
		Find(&items).Error; err != nil {
		return IntegrationPage[models.DeadLetter]{}, err
	}
	return IntegrationPage[models.DeadLetter]{
		Items:    items,
		Total:    total,
		Page:     options.Page,
		PageSize: options.PageSize,
	}, nil
}

func (service *IntegrationManagementService) ListInboxMessages(
	ctx context.Context,
	options IntegrationListOptions,
) (IntegrationPage[models.InboxMessage], error) {
	operation, err := integrationManagementOperation(ctx)
	if err != nil {
		return IntegrationPage[models.InboxMessage]{}, err
	}
	spec := integrationListSpec{
		defaultSortBy:    "received_at",
		defaultSortOrder: "desc",
		sortColumns: map[string]string{
			"received_at":  "received_at",
			"processed_at": "processed_at",
			"status":       "status",
			"created_at":   "created_at",
			"id":           "id",
		},
		statuses: integrationStringSet(
			string(models.InboxMessageStatusProcessing),
			string(models.InboxMessageStatusCompleted),
			string(models.InboxMessageStatusConflict),
			string(models.InboxMessageStatusDeadLetter),
		),
	}
	options, err = normalizeIntegrationListOptions(options, spec)
	if err != nil {
		return IntegrationPage[models.InboxMessage]{}, err
	}
	query := scopedIntegrationQuery(
		service.db.WithContext(ctx).Model(&models.InboxMessage{}),
		operation.Scope,
	)
	if options.ConnectionPublicID != "" {
		connectionID, lookupErr := service.integrationConnectionID(
			ctx,
			operation.Scope,
			options.ConnectionPublicID,
		)
		if lookupErr != nil {
			return IntegrationPage[models.InboxMessage]{}, lookupErr
		}
		query = query.Where("connection_id = ?", connectionID)
	}
	if options.Status != "" {
		query = query.Where("status = ?", options.Status)
	}
	if options.Search != "" {
		pattern := "%" + escapeIntegrationLike(options.Search) + "%"
		query = query.Where(
			"(LOWER(external_message_id) LIKE LOWER(?) ESCAPE '\\' OR LOWER(external_resource_type) LIKE LOWER(?) ESCAPE '\\' OR LOWER(external_resource_id) LIKE LOWER(?) ESCAPE '\\')",
			pattern,
			pattern,
			pattern,
		)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return IntegrationPage[models.InboxMessage]{}, err
	}
	var items []models.InboxMessage
	listQuery := query.Select(
		"id, public_id, created_at, updated_at, organization_id, project_id, connection_id, mapping_version_id, external_message_id, external_resource_type, external_resource_id, signed_at, received_at, content_type, payload_digest, status, processed_at",
	)
	if err := applyIntegrationOrder(listQuery, options, spec).
		Offset(integrationPageOffset(options)).
		Limit(options.PageSize).
		Find(&items).Error; err != nil {
		return IntegrationPage[models.InboxMessage]{}, err
	}
	return IntegrationPage[models.InboxMessage]{
		Items:    items,
		Total:    total,
		Page:     options.Page,
		PageSize: options.PageSize,
	}, nil
}

func (service *IntegrationManagementService) ListInboxReceipts(
	ctx context.Context,
	inboxMessagePublicID string,
	options IntegrationListOptions,
) (IntegrationPage[models.InboxReceipt], error) {
	operation, err := integrationManagementOperation(ctx)
	if err != nil {
		return IntegrationPage[models.InboxReceipt]{}, err
	}
	inboxMessagePublicID = strings.TrimSpace(inboxMessagePublicID)
	if inboxMessagePublicID == "" || len(inboxMessagePublicID) > 64 {
		return IntegrationPage[models.InboxReceipt]{},
			ErrIntegrationManagementInvalidInput
	}
	var message models.InboxMessage
	if err := scopedIntegrationQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where("public_id = ?", inboxMessagePublicID).
		First(&message).Error; err != nil {
		return IntegrationPage[models.InboxReceipt]{},
			integrationManagementLookupError(err)
	}
	spec := integrationListSpec{
		defaultSortBy:    "created_at",
		defaultSortOrder: "desc",
		sortColumns: map[string]string{
			"created_at":   "created_at",
			"processed_at": "processed_at",
			"status":       "status",
			"id":           "id",
		},
		statuses: integrationStringSet(
			string(models.InboxReceiptStatusApplied),
			string(models.InboxReceiptStatusNoop),
		),
	}
	options, err = normalizeIntegrationListOptions(options, spec)
	if err != nil {
		return IntegrationPage[models.InboxReceipt]{}, err
	}
	query := scopedIntegrationQuery(
		service.db.WithContext(ctx).Model(&models.InboxReceipt{}),
		operation.Scope,
	).Where("inbox_message_id = ?", message.ID)
	if options.Status != "" {
		query = query.Where("status = ?", options.Status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return IntegrationPage[models.InboxReceipt]{}, err
	}
	var items []models.InboxReceipt
	listQuery := query.Select(
		"id, public_id, created_at, organization_id, project_id, connection_id, inbox_message_id, status, resource_type, resource_id, resource_version, event_id, operation_id, actor_type, actor_id, processed_at",
	)
	if err := applyIntegrationOrder(listQuery, options, spec).
		Offset(integrationPageOffset(options)).
		Limit(options.PageSize).
		Find(&items).Error; err != nil {
		return IntegrationPage[models.InboxReceipt]{}, err
	}
	return IntegrationPage[models.InboxReceipt]{
		Items:    items,
		Total:    total,
		Page:     options.Page,
		PageSize: options.PageSize,
	}, nil
}

func (service *IntegrationManagementService) ListSyncRuns(
	ctx context.Context,
	options IntegrationListOptions,
) (IntegrationPage[models.SyncRun], error) {
	operation, err := integrationManagementOperation(ctx)
	if err != nil {
		return IntegrationPage[models.SyncRun]{}, err
	}
	spec := integrationListSpec{
		defaultSortBy:    "created_at",
		defaultSortOrder: "desc",
		sortColumns: map[string]string{
			"created_at":  "created_at",
			"updated_at":  "updated_at",
			"started_at":  "started_at",
			"finished_at": "finished_at",
			"status":      "status",
			"id":          "id",
		},
		statuses: integrationStringSet(
			string(models.SyncRunStatusPending),
			string(models.SyncRunStatusRunning),
			string(models.SyncRunStatusSucceeded),
			string(models.SyncRunStatusFailed),
			string(models.SyncRunStatusConflict),
			string(models.SyncRunStatusCancelled),
		),
		types: integrationStringSet(
			string(models.SyncDirectionInbound),
			string(models.SyncDirectionOutbound),
		),
	}
	options, err = normalizeIntegrationListOptions(options, spec)
	if err != nil {
		return IntegrationPage[models.SyncRun]{}, err
	}
	query := scopedIntegrationQuery(
		service.db.WithContext(ctx).Model(&models.SyncRun{}),
		operation.Scope,
	)
	if options.ConnectionPublicID != "" {
		connectionID, lookupErr := service.integrationConnectionID(
			ctx,
			operation.Scope,
			options.ConnectionPublicID,
		)
		if lookupErr != nil {
			return IntegrationPage[models.SyncRun]{}, lookupErr
		}
		query = query.Where("connection_id = ?", connectionID)
	}
	if options.Status != "" {
		query = query.Where("status = ?", options.Status)
	}
	if options.Type != "" {
		query = query.Where("direction = ?", options.Type)
	}
	if options.Search != "" {
		pattern := "%" + escapeIntegrationLike(options.Search) + "%"
		query = query.Where(
			"(LOWER(run_key) LIKE LOWER(?) ESCAPE '\\' OR LOWER(error_code) LIKE LOWER(?) ESCAPE '\\')",
			pattern,
			pattern,
		)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return IntegrationPage[models.SyncRun]{}, err
	}
	var items []models.SyncRun
	listQuery := query.Select(
		"id, public_id, created_at, updated_at, organization_id, project_id, connection_id, run_key, direction, status, started_at, finished_at, processed_count, succeeded_count, failed_count, conflict_count, error_code",
	)
	if err := applyIntegrationOrder(listQuery, options, spec).
		Offset(integrationPageOffset(options)).
		Limit(options.PageSize).
		Find(&items).Error; err != nil {
		return IntegrationPage[models.SyncRun]{}, err
	}
	return IntegrationPage[models.SyncRun]{
		Items:    items,
		Total:    total,
		Page:     options.Page,
		PageSize: options.PageSize,
	}, nil
}

func (service *IntegrationManagementService) ListOutboxDeliveries(
	ctx context.Context,
	options IntegrationListOptions,
) (IntegrationPage[models.OutboxDelivery], error) {
	operation, err := integrationManagementOperation(ctx)
	if err != nil {
		return IntegrationPage[models.OutboxDelivery]{}, err
	}
	spec := integrationListSpec{
		defaultSortBy:    "created_at",
		defaultSortOrder: "desc",
		sortColumns: map[string]string{
			"created_at":      "created_at",
			"updated_at":      "updated_at",
			"status":          "status",
			"next_attempt_at": "next_attempt_at",
			"id":              "id",
		},
		statuses: integrationStringSet(
			string(models.OutboxDeliveryPending),
			string(models.OutboxDeliveryProcessing),
			string(models.OutboxDeliverySucceeded),
			string(models.OutboxDeliveryFailed),
			string(models.OutboxDeliveryDead),
			string(models.OutboxDeliveryExpired),
		),
	}
	options, err = normalizeIntegrationListOptions(options, spec)
	if err != nil {
		return IntegrationPage[models.OutboxDelivery]{}, err
	}
	query := scopedIntegrationQuery(
		service.db.WithContext(ctx).Model(&models.OutboxDelivery{}),
		operation.Scope,
	)
	if options.Status != "" {
		query = query.Where("status = ?", options.Status)
	}
	if options.Type != "" {
		query = query.Where("destination_type = ?", options.Type)
	}
	if options.Search != "" {
		pattern := "%" + escapeIntegrationLike(options.Search) + "%"
		query = query.Where(
			"(LOWER(event_id) LIKE LOWER(?) ESCAPE '\\' OR LOWER(destination_type) LIKE LOWER(?) ESCAPE '\\')",
			pattern,
			pattern,
		)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return IntegrationPage[models.OutboxDelivery]{}, err
	}
	var items []models.OutboxDelivery
	listQuery := query.Select(
		"id, created_at, updated_at, organization_id, project_id, event_id, destination_type, status, attempts, max_attempts, next_attempt_at, last_error, delivered_at, expires_at, expired_at",
	)
	if err := applyIntegrationOrder(listQuery, options, spec).
		Offset(integrationPageOffset(options)).
		Limit(options.PageSize).
		Find(&items).Error; err != nil {
		return IntegrationPage[models.OutboxDelivery]{}, err
	}
	return IntegrationPage[models.OutboxDelivery]{
		Items:    items,
		Total:    total,
		Page:     options.Page,
		PageSize: options.PageSize,
	}, nil
}

func (service *IntegrationManagementService) ListDomainEvents(
	ctx context.Context,
	options IntegrationDomainEventCursorOptions,
) (IntegrationCursorPage[models.DomainEvent], error) {
	operation, err := integrationManagementOperation(ctx)
	if err != nil {
		return IntegrationCursorPage[models.DomainEvent]{}, err
	}
	if service.domainEventCursor == nil {
		return IntegrationCursorPage[models.DomainEvent]{},
			ErrIntegrationManagementUnavailable
	}
	options.EventType = strings.TrimSpace(options.EventType)
	options.Search = strings.TrimSpace(options.Search)
	if options.Limit == 0 {
		options.Limit = integrationManagementDefaultPageSize
	}
	if options.Limit < 1 ||
		options.Limit > integrationManagementMaximumPageSize ||
		len(options.EventType) > 255 ||
		len(options.Search) > integrationManagementMaximumSearch ||
		containsIntegrationControl(options.EventType) ||
		containsIntegrationControl(options.Search) {
		return IntegrationCursorPage[models.DomainEvent]{},
			ErrIntegrationManagementInvalidInput
	}
	filterDigest := integrationDomainEventFilterDigest(options)
	var cursor *integrationDomainEventCursorPayload
	if options.Cursor != "" {
		decoded := &integrationDomainEventCursorPayload{}
		if err := service.domainEventCursor.Decode(options.Cursor, decoded); err != nil ||
			decoded.Version != integrationDomainEventCursorVersion ||
			decoded.OrganizationID != operation.Scope.OrganizationID ||
			decoded.ProjectID != operation.Scope.ProjectID ||
			decoded.Limit != options.Limit ||
			decoded.FilterDigest != filterDigest ||
			decoded.SortVersion != integrationDomainEventSortVersion ||
			decoded.CreatedAt.IsZero() ||
			strings.TrimSpace(decoded.ID) == "" {
			return IntegrationCursorPage[models.DomainEvent]{},
				ErrIntegrationListCursorInvalid
		}
		cursor = decoded
	}
	query := scopedIntegrationQuery(
		service.db.WithContext(ctx).Model(&models.DomainEvent{}),
		operation.Scope,
	)
	if options.EventType != "" {
		query = query.Where("type = ?", options.EventType)
	}
	if options.Search != "" {
		pattern := "%" + escapeIntegrationLike(options.Search) + "%"
		query = query.Where(
			"(LOWER(subject) LIKE LOWER(?) ESCAPE '\\' OR LOWER(actor_id) LIKE LOWER(?) ESCAPE '\\')",
			pattern,
			pattern,
		)
	}
	if cursor != nil {
		query = query.Where(
			"created_at < ? OR (created_at = ? AND id < ?)",
			cursor.CreatedAt,
			cursor.CreatedAt,
			cursor.ID,
		)
	}
	var events []models.DomainEvent
	if err := query.Select(
		"id, created_at, organization_id, project_id, type, subject, time, actor_type, actor_id, resource_version",
	).Order("created_at DESC, id DESC").
		Limit(options.Limit + 1).
		Find(&events).Error; err != nil {
		return IntegrationCursorPage[models.DomainEvent]{}, err
	}
	hasMore := len(events) > options.Limit
	if hasMore {
		events = events[:options.Limit]
	}
	nextCursor := ""
	if hasMore && len(events) > 0 {
		last := events[len(events)-1]
		nextCursor, err = service.domainEventCursor.Encode(
			integrationDomainEventCursorPayload{
				Version:        integrationDomainEventCursorVersion,
				OrganizationID: operation.Scope.OrganizationID,
				ProjectID:      operation.Scope.ProjectID,
				Limit:          options.Limit,
				FilterDigest:   filterDigest,
				SortVersion:    integrationDomainEventSortVersion,
				CreatedAt:      last.CreatedAt.UTC(),
				ID:             last.ID,
			},
		)
		if err != nil {
			return IntegrationCursorPage[models.DomainEvent]{},
				ErrIntegrationManagementUnavailable
		}
	}
	return IntegrationCursorPage[models.DomainEvent]{
		Items:      events,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

type ReplayIntegrationDeadLetterInput struct {
	ExpectedUpdatedAt time.Time
}

func (service *IntegrationManagementService) ReplayDeadLetter(
	ctx context.Context,
	publicID string,
	input ReplayIntegrationDeadLetterInput,
) (*IntegrationInboundResult, error) {
	operation, err := integrationManagementOperation(ctx)
	if err != nil {
		return nil, err
	}
	if service.inbox == nil {
		return nil, ErrIntegrationManagementUnavailable
	}
	publicID = strings.TrimSpace(publicID)
	if publicID == "" || input.ExpectedUpdatedAt.IsZero() {
		return nil, fmt.Errorf(
			"%w: dead-letter id and expected_updated_at are required",
			ErrIntegrationManagementInvalidInput,
		)
	}
	var letter models.DeadLetter
	if err := scopedIntegrationQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where("public_id = ?", publicID).First(&letter).Error; err != nil {
		return nil, integrationManagementLookupError(err)
	}
	return service.inbox.ReplayDeadLetter(ctx, IntegrationDeadLetterReplayInput{
		DeadLetterID:      letter.ID,
		ExpectedUpdatedAt: input.ExpectedUpdatedAt,
	})
}

func integrationManagementOperation(ctx context.Context) (OperationContext, error) {
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return OperationContext{}, fmt.Errorf(
			"%w: trusted operation context is required",
			ErrIntegrationManagementInvalidInput,
		)
	}
	if operation.Source != SourceProtocolHumanREST ||
		operation.Actor.Type != models.ActorTypeHuman {
		return OperationContext{}, fmt.Errorf(
			"%w: human project operation is required",
			ErrIntegrationManagementInvalidInput,
		)
	}
	return operation, nil
}

func scopedIntegrationQuery(
	db *gorm.DB,
	scope models.ProjectScope,
) *gorm.DB {
	return db.Where(
		"organization_id = ? AND project_id = ?",
		scope.OrganizationID,
		scope.ProjectID,
	)
}

func normalizeIntegrationListOptions(
	options IntegrationListOptions,
	spec integrationListSpec,
) (IntegrationListOptions, error) {
	if options.Page == 0 {
		options.Page = 1
	} else if options.Page < 1 {
		return IntegrationListOptions{}, ErrIntegrationManagementInvalidInput
	}
	if options.PageSize == 0 {
		options.PageSize = integrationManagementDefaultPageSize
	} else if options.PageSize < 1 ||
		options.PageSize > integrationManagementMaximumPageSize {
		return IntegrationListOptions{}, ErrIntegrationManagementInvalidInput
	}
	if options.Page > math.MaxInt/options.PageSize {
		return IntegrationListOptions{}, ErrIntegrationManagementInvalidInput
	}
	if options.SortBy == "" {
		options.SortBy = spec.defaultSortBy
	}
	if _, ok := spec.sortColumns[options.SortBy]; !ok {
		return IntegrationListOptions{}, ErrIntegrationManagementInvalidInput
	}
	if options.SortOrder == "" {
		options.SortOrder = spec.defaultSortOrder
	}
	if options.SortOrder != "asc" && options.SortOrder != "desc" {
		return IntegrationListOptions{}, ErrIntegrationManagementInvalidInput
	}
	for _, value := range []string{
		options.Search,
		options.Status,
		options.Type,
		options.ConnectionPublicID,
	} {
		if strings.TrimSpace(value) != value ||
			containsIntegrationControl(value) {
			return IntegrationListOptions{}, ErrIntegrationManagementInvalidInput
		}
	}
	if len(options.Search) > integrationManagementMaximumSearch ||
		len(options.Status) > 64 ||
		len(options.Type) > 128 ||
		len(options.ConnectionPublicID) > 64 {
		return IntegrationListOptions{}, ErrIntegrationManagementInvalidInput
	}
	if options.Status != "" {
		if _, ok := spec.statuses[options.Status]; !ok {
			return IntegrationListOptions{}, ErrIntegrationManagementInvalidInput
		}
	}
	if options.Type != "" && len(spec.types) > 0 {
		if _, ok := spec.types[options.Type]; !ok {
			return IntegrationListOptions{}, ErrIntegrationManagementInvalidInput
		}
	}
	return options, nil
}

func applyIntegrationOrder(
	query *gorm.DB,
	options IntegrationListOptions,
	spec integrationListSpec,
) *gorm.DB {
	column := spec.sortColumns[options.SortBy]
	direction := "ASC"
	if options.SortOrder == "desc" {
		direction = "DESC"
	}
	query = query.Order(column + " " + direction)
	if column != "id" {
		query = query.Order("id " + direction)
	}
	return query
}

func integrationPageOffset(options IntegrationListOptions) int {
	return (options.Page - 1) * options.PageSize
}

func integrationStringSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func containsIntegrationControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func escapeIntegrationLike(value string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	)
	return replacer.Replace(value)
}

func (service *IntegrationManagementService) integrationConnectionID(
	ctx context.Context,
	scope models.ProjectScope,
	publicID string,
) (uint, error) {
	var connection models.Connection
	if err := scopedIntegrationQuery(
		service.db.WithContext(ctx),
		scope,
	).Select("id").Where("public_id = ?", publicID).
		First(&connection).Error; err != nil {
		return 0, integrationManagementLookupError(err)
	}
	return connection.ID, nil
}

func integrationDomainEventFilterDigest(
	options IntegrationDomainEventCursorOptions,
) string {
	sum := sha256.Sum256([]byte(
		options.EventType + "\x00" + options.Search,
	))
	return hex.EncodeToString(sum[:])
}

func normalizeConnectorDefinitionInput(
	input ConnectorDefinitionInput,
) (ConnectorDefinitionInput, error) {
	input.Key = strings.TrimSpace(input.Key)
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.Kind = strings.TrimSpace(input.Kind)
	input.SignatureScheme = strings.TrimSpace(input.SignatureScheme)
	if !integrationManagementKindPattern.MatchString(input.Key) ||
		!integrationManagementKindPattern.MatchString(input.Kind) ||
		input.Name == "" || len(input.Name) > 120 ||
		len(input.Description) > 500 ||
		input.SignatureScheme == "" || len(input.SignatureScheme) > 64 {
		return ConnectorDefinitionInput{}, fmt.Errorf(
			"%w: connector definition fields are invalid",
			ErrIntegrationManagementInvalidInput,
		)
	}
	switch input.Direction {
	case models.ConnectorDirectionInbound,
		models.ConnectorDirectionOutbound,
		models.ConnectorDirectionBidirectional:
	default:
		return ConnectorDefinitionInput{}, fmt.Errorf(
			"%w: connector direction is invalid",
			ErrIntegrationManagementInvalidInput,
		)
	}
	if input.Status == "" {
		input.Status = models.ConnectorDefinitionStatusActive
	}
	switch input.Status {
	case models.ConnectorDefinitionStatusActive,
		models.ConnectorDefinitionStatusDisabled,
		models.ConnectorDefinitionStatusArchived:
	default:
		return ConnectorDefinitionInput{}, fmt.Errorf(
			"%w: connector status is invalid",
			ErrIntegrationManagementInvalidInput,
		)
	}
	if input.DefaultReplayWindowSeconds == 0 {
		input.DefaultReplayWindowSeconds = 300
	}
	if input.DefaultReplayWindowSeconds < 30 ||
		input.DefaultReplayWindowSeconds > 86400 {
		return ConnectorDefinitionInput{}, fmt.Errorf(
			"%w: connector replay window is invalid",
			ErrIntegrationManagementInvalidInput,
		)
	}
	var err error
	input.ConfigurationSchema, err = normalizeJSONObject(input.ConfigurationSchema, false)
	if err != nil {
		return ConnectorDefinitionInput{}, err
	}
	input.MappingSchema, err = normalizeJSONObject(input.MappingSchema, false)
	if err != nil {
		return ConnectorDefinitionInput{}, err
	}
	return input, nil
}

func normalizeConnectionInput(input ConnectionInput) (ConnectionInput, error) {
	input.ConnectorDefinitionPublicID = strings.TrimSpace(input.ConnectorDefinitionPublicID)
	input.Key = strings.TrimSpace(input.Key)
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.VerificationKeyRef = strings.TrimSpace(input.VerificationKeyRef)
	if input.ConnectorDefinitionPublicID == "" ||
		!integrationManagementKindPattern.MatchString(input.Key) ||
		input.Name == "" || len(input.Name) > 120 ||
		len(input.Description) > 500 {
		return ConnectionInput{}, fmt.Errorf(
			"%w: connection fields are invalid",
			ErrIntegrationManagementInvalidInput,
		)
	}
	if input.Status == "" {
		input.Status = models.ConnectionStatusActive
	}
	switch input.Status {
	case models.ConnectionStatusActive,
		models.ConnectionStatusInactive,
		models.ConnectionStatusError,
		models.ConnectionStatusArchived:
	default:
		return ConnectionInput{}, fmt.Errorf(
			"%w: connection status is invalid",
			ErrIntegrationManagementInvalidInput,
		)
	}
	if input.ReplayWindowSeconds == 0 {
		input.ReplayWindowSeconds = 300
	}
	if input.ReplayWindowSeconds < 30 || input.ReplayWindowSeconds > 86400 {
		return ConnectionInput{}, fmt.Errorf(
			"%w: connection replay window is invalid",
			ErrIntegrationManagementInvalidInput,
		)
	}
	if input.VerificationKeyRef != "" &&
		!integrationKeyReferencePattern.MatchString(input.VerificationKeyRef) {
		return ConnectionInput{}, fmt.Errorf(
			"%w: verification key reference is invalid",
			ErrIntegrationManagementInvalidInput,
		)
	}
	configuration, err := normalizeJSONObject(input.Configuration, false)
	if err != nil {
		return ConnectionInput{}, err
	}
	if containsInlineIntegrationSecret(configuration) {
		return ConnectionInput{}, fmt.Errorf(
			"%w: inline credentials are not allowed; use verification_key_ref",
			ErrIntegrationManagementInvalidInput,
		)
	}
	input.Configuration = configuration
	return input, nil
}

func normalizeJSONObject(
	raw json.RawMessage,
	required bool,
) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		if required {
			return nil, fmt.Errorf(
				"%w: JSON object is required",
				ErrIntegrationManagementInvalidInput,
			)
		}
		return json.RawMessage(`{}`), nil
	}
	if len(raw) > integrationManagementMaximumJSONBytes {
		return nil, fmt.Errorf(
			"%w: JSON object is too large",
			ErrIntegrationManagementInvalidInput,
		)
	}
	var object map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, fmt.Errorf(
			"%w: valid JSON object is required",
			ErrIntegrationManagementInvalidInput,
		)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf(
			"%w: only one JSON object is allowed",
			ErrIntegrationManagementInvalidInput,
		)
	}
	buffer := bytes.NewBuffer(nil)
	if err := json.Compact(buffer, raw); err != nil {
		return nil, fmt.Errorf(
			"%w: valid JSON object is required",
			ErrIntegrationManagementInvalidInput,
		)
	}
	return json.RawMessage(buffer.Bytes()), nil
}

func containsInlineIntegrationSecret(raw json.RawMessage) bool {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return true
	}
	var visit func(any) bool
	visit = func(current any) bool {
		switch typed := current.(type) {
		case map[string]any:
			for key, nested := range typed {
				normalized := strings.Map(func(character rune) rune {
					if unicode.IsLetter(character) || unicode.IsDigit(character) {
						return unicode.ToLower(character)
					}
					return -1
				}, key)
				switch normalized {
				case "secret", "clientsecret", "token", "accesstoken",
					"refreshtoken", "password", "apikey", "privatekey",
					"signingkey", "credential", "credentials":
					return true
				}
				if visit(nested) {
					return true
				}
			}
		case []any:
			for _, nested := range typed {
				if visit(nested) {
					return true
				}
			}
		}
		return false
	}
	return visit(value)
}

func validateIntegrationTargetCommand(command string) error {
	if _, allowed := allowedIntegrationTargetCommands[command]; !allowed {
		return ErrIntegrationTargetCommandDenied
	}
	return nil
}

func normalizeDefinitionDigest(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return "", fmt.Errorf(
			"%w: definition digest must be SHA-256",
			ErrIntegrationManagementInvalidInput,
		)
	}
	return value, nil
}

func integrationJSONDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func integrationManagementLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrIntegrationManagementNotFound
	}
	return err
}

func integrationManagementWriteError(operation string, err error) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(err.Error())
	if errors.Is(err, gorm.ErrDuplicatedKey) ||
		strings.Contains(lower, "unique constraint") ||
		strings.Contains(lower, "duplicate key") {
		return ErrIntegrationManagementConflict
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func (service *IntegrationManagementService) nextUpdatedAt(
	current time.Time,
) time.Time {
	next := service.now().UTC()
	if !next.After(current) {
		next = current.UTC().Add(time.Nanosecond)
	}
	return next
}
