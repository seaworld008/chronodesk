package services

import (
	"context"
	"crypto/ed25519"
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
	ErrConfigurationNotFound      = errors.New("project configuration not found")
	ErrConfigurationStateConflict = errors.New("project configuration state conflict")
	ErrConfigurationImmutable     = models.ErrPublishedConfigurationImmutable
	ErrConfigurationEventWriter   = errors.New(
		"project configuration event writer is unavailable",
	)
	ErrSolutionInstallationState = errors.New("solution installation state conflict")
)

type ProjectConfigurationService struct {
	db     *gorm.DB
	events projectDomainEventAppender
	now    func() time.Time
}

func NewProjectConfigurationService(
	db *gorm.DB,
	eventAppenders ...projectDomainEventAppender,
) (*ProjectConfigurationService, error) {
	if db == nil {
		return nil, errors.New("project configuration database is required")
	}
	if len(eventAppenders) > 1 {
		return nil, errors.New(
			"only one project configuration event writer is supported",
		)
	}
	var events projectDomainEventAppender
	if len(eventAppenders) == 1 {
		if eventAppenders[0] == nil {
			return nil, ErrConfigurationEventWriter
		}
		events = eventAppenders[0]
	}
	return &ProjectConfigurationService{
		db:     db,
		events: events,
		now:    time.Now,
	}, nil
}

const projectConfigurationBootstrapActorID = "project-configuration-bootstrap"

// BootstrapActiveProjects guarantees every active project has one immutable
// published configuration before any ticket intake is accepted. Project
// enumeration reads only the control plane; each configuration write executes
// in its own FORCE-RLS project transaction.
func (service *ProjectConfigurationService) BootstrapActiveProjects(
	ctx context.Context,
) error {
	if service == nil || service.db == nil {
		return errors.New("project configuration service is unavailable")
	}
	var projects []models.Project
	if err := service.db.WithContext(ctx).
		Select("id", "organization_id", "key", "status").
		Where("status = ?", models.ProjectStatusActive).
		Order("organization_id ASC, id ASC").
		Find(&projects).Error; err != nil {
		return fmt.Errorf("list projects for configuration bootstrap: %w", err)
	}
	actor := models.SystemActor(projectConfigurationBootstrapActorID)
	for _, project := range projects {
		scope := project.Scope()
		operationContext, err := EnsureSystemProjectOperationContext(
			ctx,
			scope,
			actor,
			"configuration-bootstrap:"+string(project.Key),
			"configuration-bootstrap:"+string(project.Key),
		)
		if err != nil {
			return err
		}
		if err := scopeddb.WithProjectScopeContextTransaction(
			operationContext,
			service.db,
			scope,
			func(scopedContext context.Context) error {
				_, bootstrapErr :=
					service.BootstrapProjectConfiguration(scopedContext)
				return bootstrapErr
			},
		); err != nil {
			return fmt.Errorf(
				"bootstrap project %s configuration: %w",
				project.Key,
				err,
			)
		}
	}
	return nil
}

type RequestTypeDraftInput struct {
	ID          string
	Key         string
	Name        string
	Description string
	WorkClass   models.WorkClass
	JSONSchema  json.RawMessage
	UISchema    json.RawMessage
}

func (service *ProjectConfigurationService) CreateRequestTypeDraft(
	ctx context.Context,
	input RequestTypeDraftInput,
) (*models.RequestTypeVersion, error) {
	operation, err := projectConfigurationOperation(ctx)
	if err != nil {
		return nil, err
	}
	var created models.RequestTypeVersion
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		version, err := nextRequestTypeVersion(
			tx,
			operation.Scope,
			input.Key,
		)
		if err != nil {
			return err
		}
		created = models.RequestTypeVersion{
			ID:             input.ID,
			OrganizationID: operation.Scope.OrganizationID,
			ProjectID:      operation.Scope.ProjectID,
			Key:            input.Key,
			Version:        version,
			Status:         models.ConfigurationStatusDraft,
			Name:           strings.TrimSpace(input.Name),
			Description:    strings.TrimSpace(input.Description),
			WorkClass:      input.WorkClass,
			JSONSchema:     datatypes.JSON(append([]byte(nil), input.JSONSchema...)),
			UISchema:       datatypes.JSON(append([]byte(nil), input.UISchema...)),
			CreatedByType:  operation.Actor.Type,
			CreatedByID:    operation.Actor.ID,
		}
		if err := tx.WithContext(ctx).Create(&created).Error; err != nil {
			return fmt.Errorf("create request type draft: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

func (service *ProjectConfigurationService) UpdateRequestTypeDraft(
	ctx context.Context,
	id string,
	input RequestTypeDraftInput,
) (*models.RequestTypeVersion, error) {
	operation, err := projectConfigurationOperation(ctx)
	if err != nil {
		return nil, err
	}
	var version models.RequestTypeVersion
	if err := scopedConfigurationQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where("id = ?", id).First(&version).Error; err != nil {
		return nil, configurationLookupError(err)
	}
	if version.Status != models.ConfigurationStatusDraft {
		return nil, ErrConfigurationImmutable
	}
	if input.Key != "" && input.Key != version.Key {
		return nil, errors.New("request type key is immutable across a version")
	}
	version.Name = strings.TrimSpace(input.Name)
	version.Description = strings.TrimSpace(input.Description)
	version.WorkClass = input.WorkClass
	version.JSONSchema = datatypes.JSON(append([]byte(nil), input.JSONSchema...))
	version.UISchema = datatypes.JSON(append([]byte(nil), input.UISchema...))
	if err := service.db.WithContext(ctx).Save(&version).Error; err != nil {
		return nil, fmt.Errorf("update request type draft: %w", err)
	}
	return &version, nil
}

type WorkflowDraftInput struct {
	ID          string
	Key         string
	Name        string
	Description string
	States      []models.WorkflowStateDefinition
	Transitions []models.WorkflowTransitionDefinition
}

func (service *ProjectConfigurationService) CreateWorkflowDraft(
	ctx context.Context,
	input WorkflowDraftInput,
) (*models.WorkflowVersion, error) {
	operation, err := projectConfigurationOperation(ctx)
	if err != nil {
		return nil, err
	}
	var created models.WorkflowVersion
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		version, err := nextWorkflowVersion(tx, operation.Scope, input.Key)
		if err != nil {
			return err
		}
		created = models.WorkflowVersion{
			ID:             input.ID,
			OrganizationID: operation.Scope.OrganizationID,
			ProjectID:      operation.Scope.ProjectID,
			Key:            input.Key,
			Version:        version,
			Status:         models.ConfigurationStatusDraft,
			Name:           strings.TrimSpace(input.Name),
			Description:    strings.TrimSpace(input.Description),
			CreatedByType:  operation.Actor.Type,
			CreatedByID:    operation.Actor.ID,
		}
		if err := created.SetDefinitions(input.States, input.Transitions); err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Create(&created).Error; err != nil {
			return fmt.Errorf("create workflow draft: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

func (service *ProjectConfigurationService) UpdateWorkflowDraft(
	ctx context.Context,
	id string,
	input WorkflowDraftInput,
) (*models.WorkflowVersion, error) {
	operation, err := projectConfigurationOperation(ctx)
	if err != nil {
		return nil, err
	}
	var version models.WorkflowVersion
	if err := scopedConfigurationQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where("id = ?", id).First(&version).Error; err != nil {
		return nil, configurationLookupError(err)
	}
	if version.Status != models.ConfigurationStatusDraft {
		return nil, ErrConfigurationImmutable
	}
	if input.Key != "" && input.Key != version.Key {
		return nil, errors.New("workflow key is immutable across a version")
	}
	version.Name = strings.TrimSpace(input.Name)
	version.Description = strings.TrimSpace(input.Description)
	if err := version.SetDefinitions(input.States, input.Transitions); err != nil {
		return nil, err
	}
	if err := service.db.WithContext(ctx).Save(&version).Error; err != nil {
		return nil, fmt.Errorf("update workflow draft: %w", err)
	}
	return &version, nil
}

type ConfigurationReleaseDraftInput struct {
	Snapshot             models.ConfigurationSnapshot
	BaseReleaseID        *string
	SourcePackageKey     string
	SourcePackageVersion string
}

func (service *ProjectConfigurationService) CreateConfigurationReleaseDraft(
	ctx context.Context,
	input ConfigurationReleaseDraftInput,
) (*models.ConfigurationRelease, error) {
	operation, err := projectConfigurationOperation(ctx)
	if err != nil {
		return nil, err
	}
	var release models.ConfigurationRelease
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		if err := service.validateSnapshotReferencesTx(
			ctx,
			tx,
			operation.Scope,
			input.Snapshot,
			false,
		); err != nil {
			return err
		}
		version, err := nextConfigurationReleaseVersion(tx, operation.Scope)
		if err != nil {
			return err
		}
		release = models.ConfigurationRelease{
			OrganizationID:       operation.Scope.OrganizationID,
			ProjectID:            operation.Scope.ProjectID,
			Version:              version,
			Status:               models.ConfigurationStatusDraft,
			BaseReleaseID:        input.BaseReleaseID,
			SourcePackageKey:     input.SourcePackageKey,
			SourcePackageVersion: input.SourcePackageVersion,
			CreatedByType:        operation.Actor.Type,
			CreatedByID:          operation.Actor.ID,
		}
		if err := release.SetConfigurationSnapshot(input.Snapshot); err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Create(&release).Error; err != nil {
			return fmt.Errorf("create configuration release draft: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &release, nil
}

func (service *ProjectConfigurationService) UpdateConfigurationReleaseDraft(
	ctx context.Context,
	id string,
	snapshot models.ConfigurationSnapshot,
) (*models.ConfigurationRelease, error) {
	operation, err := projectConfigurationOperation(ctx)
	if err != nil {
		return nil, err
	}
	var release models.ConfigurationRelease
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		if err := scopedConfigurationQuery(tx.WithContext(ctx), operation.Scope).
			Where("id = ?", id).
			First(&release).Error; err != nil {
			return configurationLookupError(err)
		}
		if release.Status != models.ConfigurationStatusDraft {
			return ErrConfigurationImmutable
		}
		if err := service.validateSnapshotReferencesTx(
			ctx,
			tx,
			operation.Scope,
			snapshot,
			false,
		); err != nil {
			return err
		}
		if err := release.SetConfigurationSnapshot(snapshot); err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Save(&release).Error; err != nil {
			return fmt.Errorf("update configuration release draft: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &release, nil
}

func (service *ProjectConfigurationService) SimulateConfigurationRelease(
	ctx context.Context,
	id string,
) (*models.ConfigurationSimulationReport, error) {
	operation, err := projectConfigurationOperation(ctx)
	if err != nil {
		return nil, err
	}
	var report models.ConfigurationSimulationReport
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		var release models.ConfigurationRelease
		if err := scopedConfigurationQuery(tx.WithContext(ctx), operation.Scope).
			Where("id = ?", id).
			First(&release).Error; err != nil {
			return configurationLookupError(err)
		}
		if release.Status == models.ConfigurationStatusPublished {
			return ErrConfigurationImmutable
		}
		if release.Status == models.ConfigurationStatusSimulated {
			if err := json.Unmarshal(release.SimulationReport, &report); err != nil {
				return fmt.Errorf("decode existing simulation report: %w", err)
			}
			return nil
		}
		snapshot, err := release.ConfigurationSnapshot()
		if err != nil {
			return err
		}
		if err := service.validateSnapshotReferencesTx(
			ctx,
			tx,
			operation.Scope,
			snapshot,
			false,
		); err != nil {
			return err
		}
		if err := service.simulateReferencedVersionsTx(
			ctx,
			tx,
			operation.Scope,
			snapshot,
		); err != nil {
			return err
		}
		report = models.ConfigurationSimulationReport{
			SnapshotHash: release.SnapshotHash,
			Checks: []string{
				"json_schema_2020_12",
				"workflow_lifecycle_mapping",
				"typed_expressions",
				"closed_action_schema",
				"project_references",
			},
		}
		if err := release.SetSimulationReport(report); err != nil {
			return err
		}
		release.Status = models.ConfigurationStatusSimulated
		if err := tx.WithContext(ctx).Save(&release).Error; err != nil {
			return fmt.Errorf("persist configuration simulation: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &report, nil
}

func (service *ProjectConfigurationService) ApproveConfigurationRelease(
	ctx context.Context,
	id string,
) (*models.ConfigurationRelease, error) {
	operation, err := projectConfigurationOperation(ctx)
	if err != nil {
		return nil, err
	}
	var release models.ConfigurationRelease
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		published, err := service.publishConfigurationReleaseTx(
			ctx,
			tx,
			operation,
			id,
		)
		if err != nil {
			return err
		}
		release = *published
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &release, nil
}

func (service *ProjectConfigurationService) CurrentConfigurationRelease(
	ctx context.Context,
) (*models.ConfigurationRelease, error) {
	operation, err := projectConfigurationOperation(ctx)
	if err != nil {
		return nil, err
	}
	var release models.ConfigurationRelease
	if err := scopedConfigurationQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where("status = ?", models.ConfigurationStatusPublished).
		Order("version DESC").
		First(&release).Error; err != nil {
		return nil, configurationLookupError(err)
	}
	return &release, nil
}

// ProjectIntakeConfiguration is the immutable, published configuration surface
// required to render and validate project-scoped ticket intake. Child versions
// retain the order declared by the release snapshot.
type ProjectIntakeConfiguration struct {
	ReleaseID      string                      `json:"release_id"`
	ReleaseVersion uint64                      `json:"release_version"`
	RequestTypes   []models.RequestTypeVersion `json:"request_types"`
	Workflows      []models.WorkflowVersion    `json:"workflows"`
}

// CurrentIntakeConfiguration resolves only the latest published release in the
// trusted OperationContext scope. Missing, cross-project, draft or internally
// inconsistent configuration fails closed.
func (service *ProjectConfigurationService) CurrentIntakeConfiguration(
	ctx context.Context,
) (*ProjectIntakeConfiguration, error) {
	operation, err := projectConfigurationOperation(ctx)
	if err != nil {
		return nil, err
	}

	var result ProjectIntakeConfiguration
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		var release models.ConfigurationRelease
		if err := scopedConfigurationQuery(
			tx.WithContext(ctx),
			operation.Scope,
		).Where("status = ?", models.ConfigurationStatusPublished).
			Order("version DESC").
			First(&release).Error; err != nil {
			return configurationLookupError(err)
		}
		snapshot, err := release.ConfigurationSnapshot()
		if err != nil {
			return fmt.Errorf("decode published intake configuration: %w", err)
		}
		if err := snapshot.Validate(); err != nil {
			return fmt.Errorf(
				"%w: invalid published intake snapshot: %v",
				ErrConfigurationStateConflict,
				err,
			)
		}

		requestTypes, err := loadPublishedRequestTypesInSnapshotOrder(
			ctx,
			tx,
			operation.Scope,
			snapshot.RequestTypeVersionIDs,
		)
		if err != nil {
			return err
		}
		workflows, err := loadPublishedWorkflowsInSnapshotOrder(
			ctx,
			tx,
			operation.Scope,
			snapshot.WorkflowVersionIDs,
		)
		if err != nil {
			return err
		}
		result = ProjectIntakeConfiguration{
			ReleaseID:      release.ID,
			ReleaseVersion: release.Version,
			RequestTypes:   requestTypes,
			Workflows:      workflows,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func loadPublishedRequestTypesInSnapshotOrder(
	ctx context.Context,
	tx *gorm.DB,
	scope models.ProjectScope,
	ids []string,
) ([]models.RequestTypeVersion, error) {
	var rows []models.RequestTypeVersion
	if err := scopedConfigurationQuery(tx.WithContext(ctx), scope).
		Where(
			"id IN ? AND status = ?",
			ids,
			models.ConfigurationStatusPublished,
		).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load published request types: %w", err)
	}
	byID := make(map[string]models.RequestTypeVersion, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	ordered := make([]models.RequestTypeVersion, 0, len(ids))
	for _, id := range ids {
		row, exists := byID[id]
		if !exists {
			return nil, fmt.Errorf(
				"%w: published request type %q is unavailable",
				ErrConfigurationStateConflict,
				id,
			)
		}
		ordered = append(ordered, row)
	}
	return ordered, nil
}

func loadPublishedWorkflowsInSnapshotOrder(
	ctx context.Context,
	tx *gorm.DB,
	scope models.ProjectScope,
	ids []string,
) ([]models.WorkflowVersion, error) {
	var rows []models.WorkflowVersion
	if err := scopedConfigurationQuery(tx.WithContext(ctx), scope).
		Where(
			"id IN ? AND status = ?",
			ids,
			models.ConfigurationStatusPublished,
		).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load published workflows: %w", err)
	}
	byID := make(map[string]models.WorkflowVersion, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	ordered := make([]models.WorkflowVersion, 0, len(ids))
	for _, id := range ids {
		row, exists := byID[id]
		if !exists {
			return nil, fmt.Errorf(
				"%w: published workflow %q is unavailable",
				ErrConfigurationStateConflict,
				id,
			)
		}
		ordered = append(ordered, row)
	}
	return ordered, nil
}

func (service *ProjectConfigurationService) RollbackConfigurationRelease(
	ctx context.Context,
	targetReleaseID string,
) (*models.ConfigurationRelease, error) {
	operation, err := projectConfigurationOperation(ctx)
	if err != nil {
		return nil, err
	}
	if service.events == nil {
		return nil, ErrConfigurationEventWriter
	}
	var rollback models.ConfigurationRelease
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		var target models.ConfigurationRelease
		if err := scopedConfigurationQuery(tx.WithContext(ctx), operation.Scope).
			Where(
				"id = ? AND status = ?",
				targetReleaseID,
				models.ConfigurationStatusPublished,
			).
			First(&target).Error; err != nil {
			return configurationLookupError(err)
		}
		var current models.ConfigurationRelease
		if err := scopedConfigurationQuery(tx.WithContext(ctx), operation.Scope).
			Where("status = ?", models.ConfigurationStatusPublished).
			Order("version DESC").
			First(&current).Error; err != nil {
			return configurationLookupError(err)
		}
		version, err := nextConfigurationReleaseVersion(tx, operation.Scope)
		if err != nil {
			return err
		}
		now := service.now().UTC()
		rollback = models.ConfigurationRelease{
			OrganizationID:       operation.Scope.OrganizationID,
			ProjectID:            operation.Scope.ProjectID,
			Version:              version,
			Status:               models.ConfigurationStatusPublished,
			Snapshot:             append(datatypes.JSON(nil), target.Snapshot...),
			SnapshotHash:         target.SnapshotHash,
			SimulationReport:     append(datatypes.JSON(nil), target.SimulationReport...),
			BaseReleaseID:        &current.ID,
			RollbackOfReleaseID:  &target.ID,
			SourcePackageKey:     target.SourcePackageKey,
			SourcePackageVersion: target.SourcePackageVersion,
			CreatedByType:        operation.Actor.Type,
			CreatedByID:          operation.Actor.ID,
			ApprovedByType:       operation.Actor.Type,
			ApprovedByID:         operation.Actor.ID,
			PublishedAt:          &now,
		}
		if err := tx.WithContext(ctx).Create(&rollback).Error; err != nil {
			return fmt.Errorf("create rollback release: %w", err)
		}
		return service.appendConfigurationPublishedEventTx(
			ctx,
			tx,
			operation,
			&rollback,
		)
	})
	if err != nil {
		return nil, err
	}
	return &rollback, nil
}

type SolutionUpgradePreview struct {
	PackageKey         string                   `json:"package_key"`
	PackageVersion     string                   `json:"package_version"`
	BaseInstallationID *string                  `json:"base_installation_id,omitempty"`
	Diff               models.ConfigurationDiff `json:"diff"`
}

func (service *ProjectConfigurationService) PreviewSolutionUpgrade(
	ctx context.Context,
	solution models.IndustrySolutionPackage,
	publicKey ed25519.PublicKey,
) (*SolutionUpgradePreview, error) {
	operation, err := projectConfigurationOperation(ctx)
	if err != nil {
		return nil, err
	}
	if err := solution.Verify(publicKey); err != nil {
		return nil, err
	}
	if err := service.validateSolutionDependencies(
		ctx,
		operation.Scope,
		solution.Manifest.Dependencies,
	); err != nil {
		return nil, err
	}
	preview := &SolutionUpgradePreview{
		PackageKey:     solution.Manifest.PackageKey,
		PackageVersion: solution.Manifest.Version,
	}
	var current models.ProjectSolutionInstallation
	err = scopedConfigurationQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where(
		"package_key = ? AND status = ?",
		solution.Manifest.PackageKey,
		models.SolutionInstallationActive,
	).
		Order("approved_at DESC, id DESC").
		First(&current).Error
	var previous models.IndustrySolutionSnapshot
	var previousVersion string
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
	case err != nil:
		return nil, fmt.Errorf("load active solution installation: %w", err)
	default:
		if err := strictUnmarshal(current.PackageSnapshot, &previous); err != nil {
			return nil, fmt.Errorf("decode active solution snapshot: %w", err)
		}
		preview.BaseInstallationID = &current.ID
		previousVersion = current.PackageVersion
	}
	diff, err := models.DiffIndustrySolutionSnapshots(
		previousVersion,
		previous,
		solution.Manifest.Version,
		solution.Snapshot,
	)
	if err != nil {
		return nil, err
	}
	preview.Diff = diff
	return preview, nil
}

func (service *ProjectConfigurationService) PrepareSolutionInstallation(
	ctx context.Context,
	solution models.IndustrySolutionPackage,
	publicKey ed25519.PublicKey,
) (*models.ProjectSolutionInstallation, error) {
	operation, err := projectConfigurationOperation(ctx)
	if err != nil {
		return nil, err
	}
	preview, err := service.PreviewSolutionUpgrade(ctx, solution, publicKey)
	if err != nil {
		return nil, err
	}
	var installation models.ProjectSolutionInstallation
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		workflowIDs := make([]string, 0, len(solution.Snapshot.Workflows))
		for _, template := range solution.Snapshot.Workflows {
			version, err := nextWorkflowVersion(tx, operation.Scope, template.Key)
			if err != nil {
				return err
			}
			workflow := models.WorkflowVersion{
				OrganizationID: operation.Scope.OrganizationID,
				ProjectID:      operation.Scope.ProjectID,
				Key:            template.Key,
				Version:        version,
				Status:         models.ConfigurationStatusDraft,
				Name:           template.Name,
				Description:    template.Description,
				CreatedByType:  operation.Actor.Type,
				CreatedByID:    operation.Actor.ID,
			}
			if err := workflow.SetDefinitions(
				template.States,
				template.Transitions,
			); err != nil {
				return err
			}
			if err := tx.WithContext(ctx).Create(&workflow).Error; err != nil {
				return fmt.Errorf("install workflow template %q: %w", template.Key, err)
			}
			workflowIDs = append(workflowIDs, workflow.ID)
		}

		requestTypeIDs := make([]string, 0, len(solution.Snapshot.RequestTypes))
		for _, template := range solution.Snapshot.RequestTypes {
			version, err := nextRequestTypeVersion(
				tx,
				operation.Scope,
				template.Key,
			)
			if err != nil {
				return err
			}
			requestType := models.RequestTypeVersion{
				OrganizationID: operation.Scope.OrganizationID,
				ProjectID:      operation.Scope.ProjectID,
				Key:            template.Key,
				Version:        version,
				Status:         models.ConfigurationStatusDraft,
				Name:           template.Name,
				Description:    template.Description,
				WorkClass:      template.WorkClass,
				JSONSchema:     datatypes.JSON(append([]byte(nil), template.JSONSchema...)),
				UISchema:       datatypes.JSON(append([]byte(nil), template.UISchema...)),
				CreatedByType:  operation.Actor.Type,
				CreatedByID:    operation.Actor.ID,
			}
			if err := tx.WithContext(ctx).Create(&requestType).Error; err != nil {
				return fmt.Errorf("install request type template %q: %w", template.Key, err)
			}
			requestTypeIDs = append(requestTypeIDs, requestType.ID)
		}

		releaseVersion, err := nextConfigurationReleaseVersion(tx, operation.Scope)
		if err != nil {
			return err
		}
		release := models.ConfigurationRelease{
			OrganizationID:       operation.Scope.OrganizationID,
			ProjectID:            operation.Scope.ProjectID,
			Version:              releaseVersion,
			Status:               models.ConfigurationStatusDraft,
			SourcePackageKey:     solution.Manifest.PackageKey,
			SourcePackageVersion: solution.Manifest.Version,
			CreatedByType:        operation.Actor.Type,
			CreatedByID:          operation.Actor.ID,
		}
		if preview.BaseInstallationID != nil {
			var base models.ProjectSolutionInstallation
			if err := scopedConfigurationQuery(tx.WithContext(ctx), operation.Scope).
				Where("id = ?", *preview.BaseInstallationID).
				First(&base).Error; err != nil {
				return configurationLookupError(err)
			}
			release.BaseReleaseID = &base.ReleaseID
		}
		if err := release.SetConfigurationSnapshot(models.ConfigurationSnapshot{
			RequestTypeVersionIDs: requestTypeIDs,
			WorkflowVersionIDs:    workflowIDs,
			SLAPolicies:           solution.Snapshot.SLAPolicies,
			Calendars:             solution.Snapshot.Calendars,
			Routes:                solution.Snapshot.Routes,
			Automations:           solution.Snapshot.Automations,
			ApprovalPolicies:      solution.Snapshot.ApprovalPolicies,
			RiskPolicies:          solution.Snapshot.RiskPolicies,
		}); err != nil {
			return err
		}
		if err := service.validateSnapshotReferencesTx(
			ctx,
			tx,
			operation.Scope,
			mustConfigurationSnapshot(release),
			false,
		); err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Create(&release).Error; err != nil {
			return fmt.Errorf("create solution configuration release: %w", err)
		}

		manifestJSON, err := json.Marshal(solution.Manifest)
		if err != nil {
			return err
		}
		snapshotJSON, err := json.Marshal(solution.Snapshot)
		if err != nil {
			return err
		}
		diffJSON, err := json.Marshal(preview.Diff)
		if err != nil {
			return err
		}
		installation = models.ProjectSolutionInstallation{
			OrganizationID:     operation.Scope.OrganizationID,
			ProjectID:          operation.Scope.ProjectID,
			PackageKey:         solution.Manifest.PackageKey,
			PackageVersion:     solution.Manifest.Version,
			Status:             models.SolutionInstallationPending,
			ReleaseID:          release.ID,
			BaseInstallationID: preview.BaseInstallationID,
			Manifest:           datatypes.JSON(manifestJSON),
			PackageSnapshot:    datatypes.JSON(snapshotJSON),
			UpgradeDiff:        datatypes.JSON(diffJSON),
			ContentHash:        solution.Manifest.ContentHash,
			SignerKeyID:        solution.SignerKeyID,
			Signature:          append([]byte(nil), solution.Signature...),
			CreatedByType:      operation.Actor.Type,
			CreatedByID:        operation.Actor.ID,
		}
		if err := tx.WithContext(ctx).Create(&installation).Error; err != nil {
			return fmt.Errorf("create solution installation snapshot: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &installation, nil
}

func (service *ProjectConfigurationService) SimulateSolutionInstallation(
	ctx context.Context,
	installationID string,
) (*models.ConfigurationSimulationReport, error) {
	operation, err := projectConfigurationOperation(ctx)
	if err != nil {
		return nil, err
	}
	var installation models.ProjectSolutionInstallation
	if err := scopedConfigurationQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where("id = ?", installationID).First(&installation).Error; err != nil {
		return nil, configurationLookupError(err)
	}
	if installation.Status != models.SolutionInstallationPending {
		return nil, ErrSolutionInstallationState
	}
	return service.SimulateConfigurationRelease(ctx, installation.ReleaseID)
}

func (service *ProjectConfigurationService) ApproveSolutionInstallation(
	ctx context.Context,
	installationID string,
) (*models.ProjectSolutionInstallation, error) {
	operation, err := projectConfigurationOperation(ctx)
	if err != nil {
		return nil, err
	}
	var installation models.ProjectSolutionInstallation
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		if err := scopedConfigurationQuery(tx.WithContext(ctx), operation.Scope).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", installationID).
			First(&installation).Error; err != nil {
			return configurationLookupError(err)
		}
		if installation.Status != models.SolutionInstallationPending {
			return ErrSolutionInstallationState
		}
		if _, err := service.publishConfigurationReleaseTx(
			ctx,
			tx,
			operation,
			installation.ReleaseID,
		); err != nil {
			return err
		}
		if err := scopedConfigurationQuery(tx.WithContext(ctx), operation.Scope).
			Model(&models.ProjectSolutionInstallation{}).
			Where(
				"package_key = ? AND status = ? AND id <> ?",
				installation.PackageKey,
				models.SolutionInstallationActive,
				installation.ID,
			).
			UpdateColumn("status", models.SolutionInstallationSuperseded).Error; err != nil {
			return fmt.Errorf("supersede previous solution installation: %w", err)
		}
		now := service.now().UTC()
		result := scopedConfigurationQuery(tx.WithContext(ctx), operation.Scope).
			Model(&models.ProjectSolutionInstallation{}).
			Where(
				"id = ? AND status = ?",
				installation.ID,
				models.SolutionInstallationPending,
			).
			Updates(map[string]any{
				"status":           models.SolutionInstallationActive,
				"approved_by_type": operation.Actor.Type,
				"approved_by_id":   operation.Actor.ID,
				"approved_at":      now,
			})
		if result.Error != nil {
			return fmt.Errorf("approve solution installation: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return ErrSolutionInstallationState
		}
		return scopedConfigurationQuery(tx.WithContext(ctx), operation.Scope).
			Where("id = ?", installation.ID).
			First(&installation).Error
	})
	if err != nil {
		return nil, err
	}
	return &installation, nil
}

func (service *ProjectConfigurationService) BootstrapProjectConfiguration(
	ctx context.Context,
) (*models.ConfigurationRelease, error) {
	operation, err := projectConfigurationOperation(ctx)
	if err != nil {
		return nil, err
	}
	var release models.ConfigurationRelease
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		var bootstrapErr error
		release, bootstrapErr = bootstrapProjectConfigurationTx(
			ctx,
			tx,
			operation,
			service.now().UTC(),
		)
		return bootstrapErr
	})
	if err != nil {
		return nil, err
	}
	return &release, nil
}

func bootstrapProjectConfigurationTx(
	ctx context.Context,
	tx *gorm.DB,
	operation OperationContext,
	now time.Time,
) (models.ConfigurationRelease, error) {
	var release models.ConfigurationRelease
	err := scopedConfigurationQuery(tx.WithContext(ctx), operation.Scope).
		Where(
			"source_package_key = ? AND status = ?",
			"chronodesk.bootstrap",
			models.ConfigurationStatusPublished,
		).
		First(&release).Error
	if err == nil {
		return release, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return release, err
	}

	stableIDs, err := useStableBootstrapIDs(tx, operation.Scope)
	if err != nil {
		return release, err
	}
	workflow := models.WorkflowVersion{
		OrganizationID: operation.Scope.OrganizationID,
		ProjectID:      operation.Scope.ProjectID,
		Key:            "default",
		Version:        1,
		Status:         models.ConfigurationStatusPublished,
		Name:           "默认工作流",
		Description:    "ChronoDesk 内置统一生命周期工作流",
		CreatedByType:  operation.Actor.Type,
		CreatedByID:    operation.Actor.ID,
		PublishedAt:    &now,
	}
	if stableIDs {
		workflow.ID = defaultWorkflowVersionID
	}
	if err := workflow.SetDefinitions(
		defaultWorkflowStates(),
		defaultWorkflowTransitions(),
	); err != nil {
		return release, err
	}
	if err := tx.WithContext(ctx).Create(&workflow).Error; err != nil {
		return release, fmt.Errorf("create bootstrap workflow: %w", err)
	}

	requestTypes := bootstrapRequestTypes(
		operation.Scope,
		operation.Actor,
		now,
		stableIDs,
	)
	for i := range requestTypes {
		if err := tx.WithContext(ctx).Create(&requestTypes[i]).Error; err != nil {
			return release, fmt.Errorf(
				"create bootstrap request type %q: %w",
				requestTypes[i].Key,
				err,
			)
		}
	}
	requestTypeIDs := make([]string, 0, len(requestTypes))
	for _, requestType := range requestTypes {
		requestTypeIDs = append(requestTypeIDs, requestType.ID)
	}
	version, err := nextConfigurationReleaseVersion(tx, operation.Scope)
	if err != nil {
		return release, err
	}
	release = models.ConfigurationRelease{
		OrganizationID:       operation.Scope.OrganizationID,
		ProjectID:            operation.Scope.ProjectID,
		Version:              version,
		Status:               models.ConfigurationStatusPublished,
		SourcePackageKey:     "chronodesk.bootstrap",
		SourcePackageVersion: "1.0.0",
		CreatedByType:        operation.Actor.Type,
		CreatedByID:          operation.Actor.ID,
		ApprovedByType:       operation.Actor.Type,
		ApprovedByID:         operation.Actor.ID,
		PublishedAt:          &now,
	}
	if err := release.SetConfigurationSnapshot(models.ConfigurationSnapshot{
		RequestTypeVersionIDs: requestTypeIDs,
		WorkflowVersionIDs:    []string{workflow.ID},
	}); err != nil {
		return release, err
	}
	if err := tx.WithContext(ctx).Create(&release).Error; err != nil {
		return release, fmt.Errorf("create bootstrap configuration release: %w", err)
	}
	return release, nil
}

func useStableBootstrapIDs(
	tx *gorm.DB,
	scope models.ProjectScope,
) (bool, error) {
	var project models.Project
	if err := tx.Select("id", "organization_id", "key").
		Where(
			"id = ? AND organization_id = ?",
			scope.ProjectID,
			scope.OrganizationID,
		).
		First(&project).Error; err != nil {
		return false, fmt.Errorf("load bootstrap project identity: %w", err)
	}
	return project.Key == models.ProjectKey("DEFAULT"), nil
}

func projectConfigurationOperation(
	ctx context.Context,
) (OperationContext, error) {
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return OperationContext{}, err
	}
	if err := operation.Scope.Validate(); err != nil {
		return OperationContext{}, err
	}
	if err := operation.Actor.Validate(); err != nil {
		return OperationContext{}, err
	}
	return operation, nil
}

func scopedConfigurationQuery(
	db *gorm.DB,
	scope models.ProjectScope,
) *gorm.DB {
	return db.Where(
		"organization_id = ? AND project_id = ?",
		scope.OrganizationID,
		scope.ProjectID,
	)
}

func configurationLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrConfigurationNotFound
	}
	return err
}

func nextRequestTypeVersion(
	tx *gorm.DB,
	scope models.ProjectScope,
	key string,
) (uint64, error) {
	var maximum uint64
	if err := scopedConfigurationQuery(tx, scope).
		Model(&models.RequestTypeVersion{}).
		Where("key = ?", key).
		Select("COALESCE(MAX(version), 0)").
		Scan(&maximum).Error; err != nil {
		return 0, fmt.Errorf("allocate request type version: %w", err)
	}
	return maximum + 1, nil
}

func nextWorkflowVersion(
	tx *gorm.DB,
	scope models.ProjectScope,
	key string,
) (uint64, error) {
	var maximum uint64
	if err := scopedConfigurationQuery(tx, scope).
		Model(&models.WorkflowVersion{}).
		Where("key = ?", key).
		Select("COALESCE(MAX(version), 0)").
		Scan(&maximum).Error; err != nil {
		return 0, fmt.Errorf("allocate workflow version: %w", err)
	}
	return maximum + 1, nil
}

func nextConfigurationReleaseVersion(
	tx *gorm.DB,
	scope models.ProjectScope,
) (uint64, error) {
	var maximum uint64
	if err := scopedConfigurationQuery(tx, scope).
		Model(&models.ConfigurationRelease{}).
		Select("COALESCE(MAX(version), 0)").
		Scan(&maximum).Error; err != nil {
		return 0, fmt.Errorf("allocate configuration release version: %w", err)
	}
	return maximum + 1, nil
}

func (service *ProjectConfigurationService) validateSnapshotReferencesTx(
	ctx context.Context,
	tx *gorm.DB,
	scope models.ProjectScope,
	snapshot models.ConfigurationSnapshot,
	requirePublished bool,
) error {
	if err := snapshot.Validate(); err != nil {
		return err
	}
	var requestTypes []models.RequestTypeVersion
	if err := scopedConfigurationQuery(tx.WithContext(ctx), scope).
		Where("id IN ?", snapshot.RequestTypeVersionIDs).
		Find(&requestTypes).Error; err != nil {
		return fmt.Errorf("load configuration request types: %w", err)
	}
	if len(requestTypes) != len(snapshot.RequestTypeVersionIDs) {
		return ErrConfigurationNotFound
	}
	for _, version := range requestTypes {
		if requirePublished &&
			version.Status != models.ConfigurationStatusPublished {
			return ErrConfigurationStateConflict
		}
		if !version.Status.IsValid() {
			return ErrConfigurationStateConflict
		}
	}

	var workflows []models.WorkflowVersion
	if err := scopedConfigurationQuery(tx.WithContext(ctx), scope).
		Where("id IN ?", snapshot.WorkflowVersionIDs).
		Find(&workflows).Error; err != nil {
		return fmt.Errorf("load configuration workflows: %w", err)
	}
	if len(workflows) != len(snapshot.WorkflowVersionIDs) {
		return ErrConfigurationNotFound
	}
	for _, version := range workflows {
		if requirePublished &&
			version.Status != models.ConfigurationStatusPublished {
			return ErrConfigurationStateConflict
		}
		if !version.Status.IsValid() {
			return ErrConfigurationStateConflict
		}
	}

	for _, route := range snapshot.Routes {
		var queueCount int64
		if err := tx.WithContext(ctx).Model(&models.Queue{}).
			Where(
				"project_id = ? AND key = ? AND status = ?",
				scope.ProjectID,
				route.QueueKey,
				models.QueueStatusActive,
			).
			Count(&queueCount).Error; err != nil {
			return err
		}
		if queueCount != 1 {
			return fmt.Errorf("route %q references unavailable queue", route.Key)
		}
		if route.TeamKey != "" {
			var teamCount int64
			if err := tx.WithContext(ctx).Model(&models.Team{}).
				Where(
					"project_id = ? AND key = ? AND status = ?",
					scope.ProjectID,
					route.TeamKey,
					models.TeamStatusActive,
				).
				Count(&teamCount).Error; err != nil {
				return err
			}
			if teamCount != 1 {
				return fmt.Errorf("route %q references unavailable team", route.Key)
			}
		}
	}
	return nil
}

func (service *ProjectConfigurationService) simulateReferencedVersionsTx(
	ctx context.Context,
	tx *gorm.DB,
	scope models.ProjectScope,
	snapshot models.ConfigurationSnapshot,
) error {
	for _, id := range snapshot.RequestTypeVersionIDs {
		var version models.RequestTypeVersion
		if err := scopedConfigurationQuery(tx.WithContext(ctx), scope).
			Where("id = ?", id).
			First(&version).Error; err != nil {
			return configurationLookupError(err)
		}
		switch version.Status {
		case models.ConfigurationStatusDraft:
			version.Status = models.ConfigurationStatusSimulated
			if err := tx.WithContext(ctx).Save(&version).Error; err != nil {
				return fmt.Errorf("simulate request type %q: %w", id, err)
			}
		case models.ConfigurationStatusSimulated,
			models.ConfigurationStatusPublished:
		default:
			return ErrConfigurationStateConflict
		}
	}
	for _, id := range snapshot.WorkflowVersionIDs {
		var version models.WorkflowVersion
		if err := scopedConfigurationQuery(tx.WithContext(ctx), scope).
			Where("id = ?", id).
			First(&version).Error; err != nil {
			return configurationLookupError(err)
		}
		switch version.Status {
		case models.ConfigurationStatusDraft:
			version.Status = models.ConfigurationStatusSimulated
			if err := tx.WithContext(ctx).Save(&version).Error; err != nil {
				return fmt.Errorf("simulate workflow %q: %w", id, err)
			}
		case models.ConfigurationStatusSimulated,
			models.ConfigurationStatusPublished:
		default:
			return ErrConfigurationStateConflict
		}
	}
	return nil
}

func (service *ProjectConfigurationService) publishConfigurationReleaseTx(
	ctx context.Context,
	tx *gorm.DB,
	operation OperationContext,
	id string,
) (*models.ConfigurationRelease, error) {
	if service.events == nil {
		return nil, ErrConfigurationEventWriter
	}
	var release models.ConfigurationRelease
	if err := scopedConfigurationQuery(tx.WithContext(ctx), operation.Scope).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", id).
		First(&release).Error; err != nil {
		return nil, configurationLookupError(err)
	}
	if release.Status != models.ConfigurationStatusSimulated {
		if release.Status == models.ConfigurationStatusPublished {
			return nil, ErrConfigurationImmutable
		}
		return nil, ErrConfigurationStateConflict
	}
	snapshot, err := release.ConfigurationSnapshot()
	if err != nil {
		return nil, err
	}
	if err := service.validateSnapshotReferencesTx(
		ctx,
		tx,
		operation.Scope,
		snapshot,
		false,
	); err != nil {
		return nil, err
	}
	now := service.now().UTC()
	for _, id := range snapshot.RequestTypeVersionIDs {
		var version models.RequestTypeVersion
		if err := scopedConfigurationQuery(tx.WithContext(ctx), operation.Scope).
			Where("id = ?", id).
			First(&version).Error; err != nil {
			return nil, configurationLookupError(err)
		}
		switch version.Status {
		case models.ConfigurationStatusPublished:
		case models.ConfigurationStatusSimulated:
			result := scopedConfigurationQuery(tx.WithContext(ctx), operation.Scope).
				Model(&models.RequestTypeVersion{}).
				Where("id = ? AND status = ?", id, models.ConfigurationStatusSimulated).
				UpdateColumns(map[string]any{
					"status":       models.ConfigurationStatusPublished,
					"published_at": now,
				})
			if result.Error != nil || result.RowsAffected != 1 {
				return nil, ErrConfigurationStateConflict
			}
		default:
			return nil, ErrConfigurationStateConflict
		}
	}
	for _, id := range snapshot.WorkflowVersionIDs {
		var version models.WorkflowVersion
		if err := scopedConfigurationQuery(tx.WithContext(ctx), operation.Scope).
			Where("id = ?", id).
			First(&version).Error; err != nil {
			return nil, configurationLookupError(err)
		}
		switch version.Status {
		case models.ConfigurationStatusPublished:
		case models.ConfigurationStatusSimulated:
			result := scopedConfigurationQuery(tx.WithContext(ctx), operation.Scope).
				Model(&models.WorkflowVersion{}).
				Where("id = ? AND status = ?", id, models.ConfigurationStatusSimulated).
				UpdateColumns(map[string]any{
					"status":       models.ConfigurationStatusPublished,
					"published_at": now,
				})
			if result.Error != nil || result.RowsAffected != 1 {
				return nil, ErrConfigurationStateConflict
			}
		default:
			return nil, ErrConfigurationStateConflict
		}
	}
	result := scopedConfigurationQuery(tx.WithContext(ctx), operation.Scope).
		Model(&models.ConfigurationRelease{}).
		Where("id = ? AND status = ?", id, models.ConfigurationStatusSimulated).
		UpdateColumns(map[string]any{
			"status":           models.ConfigurationStatusPublished,
			"approved_by_type": operation.Actor.Type,
			"approved_by_id":   operation.Actor.ID,
			"published_at":     now,
		})
	if result.Error != nil {
		return nil, fmt.Errorf("publish configuration release: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return nil, ErrConfigurationStateConflict
	}
	if err := scopedConfigurationQuery(tx.WithContext(ctx), operation.Scope).
		Where("id = ?", id).
		First(&release).Error; err != nil {
		return nil, err
	}
	if err := service.appendConfigurationPublishedEventTx(
		ctx,
		tx,
		operation,
		&release,
	); err != nil {
		return nil, err
	}
	return &release, nil
}

func (service *ProjectConfigurationService) appendConfigurationPublishedEventTx(
	ctx context.Context,
	tx *gorm.DB,
	operation OperationContext,
	release *models.ConfigurationRelease,
) error {
	if service.events == nil {
		return ErrConfigurationEventWriter
	}
	if release == nil || release.ID == "" ||
		release.Status != models.ConfigurationStatusPublished {
		return ErrConfigurationStateConflict
	}
	eventTime := service.now().UTC()
	if release.PublishedAt != nil {
		eventTime = release.PublishedAt.UTC()
	}
	_, err := service.events.AppendDomainEventTx(
		ctx,
		tx,
		DomainEventInput{
			Type: eventcontract.ConfigurationPublishedEventType,
			Subject: fmt.Sprintf(
				"project/%d/configuration-releases/%s",
				release.ProjectID,
				release.ID,
			),
			Time: eventTime,
			Data: map[string]any{
				"organization_id":               release.OrganizationID,
				"project_id":                    release.ProjectID,
				"configuration_release_id":      release.ID,
				"configuration_release_version": release.Version,
				"snapshot_hash":                 release.SnapshotHash,
				"source_package_key":            release.SourcePackageKey,
				"source_package_version":        release.SourcePackageVersion,
			},
			Scope:                operation.Scope,
			TraceID:              operation.TraceID,
			CorrelationID:        operation.CorrelationID,
			Actor:                operation.Actor,
			ResourceVersion:      release.Version,
			ConfigurationVersion: release.ID,
		},
		nil,
	)
	if err != nil {
		return fmt.Errorf("append configuration published event: %w", err)
	}
	return nil
}

func (service *ProjectConfigurationService) validateSolutionDependencies(
	ctx context.Context,
	scope models.ProjectScope,
	dependencies []models.SolutionDependency,
) error {
	for _, dependency := range dependencies {
		var installation models.ProjectSolutionInstallation
		if err := scopedConfigurationQuery(service.db.WithContext(ctx), scope).
			Where(
				"package_key = ? AND status = ?",
				dependency.PackageKey,
				models.SolutionInstallationActive,
			).
			Order("approved_at DESC, id DESC").
			First(&installation).Error; err != nil {
			return fmt.Errorf(
				"solution dependency %q is not installed: %w",
				dependency.PackageKey,
				configurationLookupError(err),
			)
		}
		if installation.ContentHash != dependency.ContentHash {
			return fmt.Errorf(
				"solution dependency %q content hash mismatch",
				dependency.PackageKey,
			)
		}
	}
	return nil
}

func strictUnmarshal(raw []byte, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	return decoder.Decode(destination)
}

func mustConfigurationSnapshot(
	release models.ConfigurationRelease,
) models.ConfigurationSnapshot {
	snapshot, _ := release.ConfigurationSnapshot()
	return snapshot
}

func defaultWorkflowStates() []models.WorkflowStateDefinition {
	return []models.WorkflowStateDefinition{
		{
			Key: "open", Name: "待处理",
			LifecycleCategory: models.LifecycleCategoryNew,
			IsInitial:         true,
		},
		{
			Key: "in_progress", Name: "处理中",
			LifecycleCategory: models.LifecycleCategoryActive,
		},
		{
			Key: "pending", Name: "等待中",
			LifecycleCategory: models.LifecycleCategoryWaiting,
		},
		{
			Key: "resolved", Name: "已解决",
			LifecycleCategory: models.LifecycleCategoryResolved,
			IsTerminal:        true,
		},
		{
			Key: "closed", Name: "已关闭",
			LifecycleCategory: models.LifecycleCategoryClosed,
			IsTerminal:        true,
		},
		{
			Key: "cancelled", Name: "已取消",
			LifecycleCategory: models.LifecycleCategoryCancelled,
			IsTerminal:        true,
		},
	}
}

func defaultWorkflowTransitions() []models.WorkflowTransitionDefinition {
	return []models.WorkflowTransitionDefinition{
		{Key: "start", Name: "开始处理", From: "open", To: "in_progress"},
		{Key: "wait", Name: "等待信息", From: "in_progress", To: "pending"},
		{Key: "resume", Name: "继续处理", From: "pending", To: "in_progress"},
		{Key: "resolve", Name: "解决", From: "in_progress", To: "resolved"},
		{Key: "reopen", Name: "重新打开", From: "resolved", To: "in_progress"},
		{Key: "close", Name: "关闭", From: "resolved", To: "closed"},
		{Key: "cancel", Name: "取消", From: "open", To: "cancelled"},
	}
}

func bootstrapRequestTypes(
	scope models.ProjectScope,
	actor models.ActorRef,
	publishedAt time.Time,
	stableIDs bool,
) []models.RequestTypeVersion {
	schema := datatypes.JSON(
		`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"title":{"type":"string"},"description":{"type":"string"}},"required":["title","description"],"additionalProperties":false}`,
	)
	uiSchema := datatypes.JSON(`{}`)
	requestTypes := []models.RequestTypeVersion{
		bootstrapRequestType(
			scope,
			"",
			"incident",
			"事件",
			models.WorkClassIncident,
			schema,
			uiSchema,
			actor,
			publishedAt,
		),
		bootstrapRequestType(
			scope,
			"",
			"request",
			"服务请求",
			models.WorkClassRequest,
			schema,
			uiSchema,
			actor,
			publishedAt,
		),
		bootstrapRequestType(
			scope,
			"",
			"problem",
			"问题",
			models.WorkClassProblem,
			schema,
			uiSchema,
			actor,
			publishedAt,
		),
		bootstrapRequestType(
			scope,
			"",
			"change",
			"变更",
			models.WorkClassChange,
			schema,
			uiSchema,
			actor,
			publishedAt,
		),
		bootstrapRequestType(
			scope,
			"",
			"complaint",
			"投诉",
			models.WorkClassComplaint,
			schema,
			uiSchema,
			actor,
			publishedAt,
		),
		bootstrapRequestType(
			scope,
			"",
			"consultation",
			"咨询",
			models.WorkClassConsultation,
			schema,
			uiSchema,
			actor,
			publishedAt,
		),
	}
	if stableIDs {
		stable := []string{
			defaultRequestTypeIncidentVersionID,
			defaultRequestTypeRequestVersionID,
			defaultRequestTypeProblemVersionID,
			defaultRequestTypeChangeVersionID,
			defaultRequestTypeComplaintVersionID,
			defaultRequestTypeConsultationVersionID,
		}
		for index := range requestTypes {
			requestTypes[index].ID = stable[index]
		}
	}
	return requestTypes
}

func bootstrapRequestType(
	scope models.ProjectScope,
	id string,
	key string,
	name string,
	workClass models.WorkClass,
	schema datatypes.JSON,
	uiSchema datatypes.JSON,
	actor models.ActorRef,
	publishedAt time.Time,
) models.RequestTypeVersion {
	return models.RequestTypeVersion{
		ID:             id,
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		Key:            key,
		Version:        1,
		Status:         models.ConfigurationStatusPublished,
		Name:           name,
		WorkClass:      workClass,
		JSONSchema:     append(datatypes.JSON(nil), schema...),
		UISchema:       append(datatypes.JSON(nil), uiSchema...),
		CreatedByType:  actor.Type,
		CreatedByID:    actor.ID,
		PublishedAt:    &publishedAt,
	}
}
