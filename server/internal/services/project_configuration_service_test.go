package services

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestProjectConfigurationReleaseLifecycleIsImmutableAndScoped(t *testing.T) {
	db, project, otherProject := newProjectConfigurationTestDB(t)
	service, err := NewProjectConfigurationService(db)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time {
		return time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	}
	ctx := projectConfigurationTestContext(t, project)
	requestType, workflow := createConfigurationDraftVersions(
		t,
		service,
		ctx,
	)
	release, err := service.CreateConfigurationReleaseDraft(
		ctx,
		ConfigurationReleaseDraftInput{
			Snapshot: models.ConfigurationSnapshot{
				RequestTypeVersionIDs: []string{requestType.ID},
				WorkflowVersionIDs:    []string{workflow.ID},
			},
		},
	)
	if err != nil {
		t.Fatalf("create release draft: %v", err)
	}
	report, err := service.SimulateConfigurationRelease(ctx, release.ID)
	if err != nil {
		t.Fatalf("simulate release: %v", err)
	}
	if report.SnapshotHash != release.SnapshotHash {
		t.Fatalf(
			"simulation hash = %q, want %q",
			report.SnapshotHash,
			release.SnapshotHash,
		)
	}
	assertConfigurationVersionStatus(
		t,
		db,
		&models.RequestTypeVersion{},
		requestType.ID,
		models.ConfigurationStatusSimulated,
	)
	assertConfigurationVersionStatus(
		t,
		db,
		&models.WorkflowVersion{},
		workflow.ID,
		models.ConfigurationStatusSimulated,
	)

	published, err := service.ApproveConfigurationRelease(ctx, release.ID)
	if err != nil {
		t.Fatalf("publish release: %v", err)
	}
	if published.Status != models.ConfigurationStatusPublished ||
		published.ApprovedByType != models.ActorTypeHuman ||
		published.PublishedAt == nil {
		t.Fatalf("published release = %+v", published)
	}
	assertConfigurationVersionStatus(
		t,
		db,
		&models.RequestTypeVersion{},
		requestType.ID,
		models.ConfigurationStatusPublished,
	)
	assertConfigurationVersionStatus(
		t,
		db,
		&models.WorkflowVersion{},
		workflow.ID,
		models.ConfigurationStatusPublished,
	)

	_, err = service.UpdateRequestTypeDraft(ctx, requestType.ID, RequestTypeDraftInput{
		Name:       "Mutated",
		WorkClass:  models.WorkClassIncident,
		JSONSchema: configurationServiceJSONSchema(false),
		UISchema:   json.RawMessage(`{}`),
	})
	if !errors.Is(err, ErrConfigurationImmutable) {
		t.Fatalf("published request type update error = %v", err)
	}
	_, err = service.UpdateConfigurationReleaseDraft(
		ctx,
		release.ID,
		models.ConfigurationSnapshot{
			RequestTypeVersionIDs: []string{requestType.ID},
			WorkflowVersionIDs:    []string{workflow.ID},
		},
	)
	if !errors.Is(err, ErrConfigurationImmutable) {
		t.Fatalf("published release update error = %v", err)
	}
	if _, err := service.ApproveConfigurationRelease(
		ctx,
		release.ID,
	); !errors.Is(err, ErrConfigurationImmutable) {
		t.Fatalf("republish error = %v", err)
	}

	otherContext := projectConfigurationTestContext(t, otherProject)
	if _, err := service.SimulateConfigurationRelease(
		otherContext,
		release.ID,
	); !errors.Is(err, ErrConfigurationNotFound) {
		t.Fatalf("cross-project release lookup error = %v", err)
	}
	wrongOrganizationContext := projectConfigurationTestContextWithScope(
		t,
		models.ProjectScope{
			OrganizationID: project.OrganizationID + 1000,
			ProjectID:      project.ID,
		},
	)
	if _, err := service.CurrentConfigurationRelease(
		wrongOrganizationContext,
	); !errors.Is(err, ErrConfigurationNotFound) {
		t.Fatalf("wrong-organization release lookup error = %v", err)
	}
}

func TestCurrentIntakeConfigurationPreservesPublishedSnapshotOrder(
	t *testing.T,
) {
	db, project, otherProject := newProjectConfigurationTestDB(t)
	service, err := NewProjectConfigurationService(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := projectConfigurationTestContext(t, project)
	firstRequestType, firstWorkflow := createConfigurationDraftVersions(
		t,
		service,
		ctx,
	)
	secondRequestType, secondWorkflow := createConfigurationDraftVersions(
		t,
		service,
		ctx,
	)
	release, err := service.CreateConfigurationReleaseDraft(
		ctx,
		ConfigurationReleaseDraftInput{
			Snapshot: models.ConfigurationSnapshot{
				RequestTypeVersionIDs: []string{
					secondRequestType.ID,
					firstRequestType.ID,
				},
				WorkflowVersionIDs: []string{
					secondWorkflow.ID,
					firstWorkflow.ID,
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("create release draft: %v", err)
	}
	if _, err := service.SimulateConfigurationRelease(ctx, release.ID); err != nil {
		t.Fatalf("simulate release: %v", err)
	}
	published, err := service.ApproveConfigurationRelease(ctx, release.ID)
	if err != nil {
		t.Fatalf("publish release: %v", err)
	}

	intake, err := service.CurrentIntakeConfiguration(ctx)
	if err != nil {
		t.Fatalf("current intake configuration: %v", err)
	}
	if intake.ReleaseID != published.ID ||
		intake.ReleaseVersion != published.Version {
		t.Fatalf("intake release = %+v, want id=%q version=%d", intake, published.ID, published.Version)
	}
	if len(intake.RequestTypes) != 2 ||
		intake.RequestTypes[0].ID != secondRequestType.ID ||
		intake.RequestTypes[1].ID != firstRequestType.ID {
		t.Fatalf(
			"request type order = %+v, want [%q, %q]",
			intake.RequestTypes,
			secondRequestType.ID,
			firstRequestType.ID,
		)
	}
	if len(intake.Workflows) != 2 ||
		intake.Workflows[0].ID != secondWorkflow.ID ||
		intake.Workflows[1].ID != firstWorkflow.ID {
		t.Fatalf(
			"workflow order = %+v, want [%q, %q]",
			intake.Workflows,
			secondWorkflow.ID,
			firstWorkflow.ID,
		)
	}
	for _, requestType := range intake.RequestTypes {
		if requestType.Status != models.ConfigurationStatusPublished ||
			requestType.ProjectID != project.ID {
			t.Fatalf("unpublished or cross-project request type returned: %+v", requestType)
		}
	}
	for _, workflow := range intake.Workflows {
		if workflow.Status != models.ConfigurationStatusPublished ||
			workflow.ProjectID != project.ID {
			t.Fatalf("unpublished or cross-project workflow returned: %+v", workflow)
		}
	}

	otherContext := projectConfigurationTestContext(t, otherProject)
	if _, err := service.CurrentIntakeConfiguration(
		otherContext,
	); !errors.Is(err, ErrConfigurationNotFound) {
		t.Fatalf("cross-project intake configuration error = %v", err)
	}
	if _, err := service.CurrentIntakeConfiguration(
		context.Background(),
	); err == nil {
		t.Fatal("unscoped intake configuration unexpectedly succeeded")
	}
}

func TestProjectConfigurationRejectsInvalidWorkflowMapping(t *testing.T) {
	db := newProjectConfigurationStandaloneDB(t, "invalid-workflow")
	service, err := NewProjectConfigurationService(db)
	if err != nil {
		t.Fatal(err)
	}
	var project models.Project
	if err := db.Order("id ASC").First(&project).Error; err != nil {
		t.Fatal(err)
	}
	ctx := projectConfigurationTestContext(t, project)
	states := configurationServiceWorkflowStates()
	states[1].LifecycleCategory = "processing"
	if _, err := service.CreateWorkflowDraft(ctx, WorkflowDraftInput{
		Key:         "invalid",
		Name:        "Invalid",
		States:      states,
		Transitions: configurationServiceWorkflowTransitions(),
	}); err == nil {
		t.Fatal("workflow with unsupported lifecycle category was created")
	}
}

func TestIndustrySolutionInstallUpgradeDiffApprovalAndRollback(t *testing.T) {
	db, project, otherProject := newProjectConfigurationTestDB(t)
	service, err := NewProjectConfigurationService(db)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time {
		return time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC)
	}
	ctx := projectConfigurationTestContext(t, project)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	versionOne := signedConfigurationServiceSolution(
		t,
		"1.0.0",
		false,
		privateKey,
	)
	installationOne, err := service.PrepareSolutionInstallation(
		ctx,
		*versionOne,
		publicKey,
	)
	if err != nil {
		t.Fatalf("prepare initial solution: %v", err)
	}
	if installationOne.Status != models.SolutionInstallationPending {
		t.Fatalf("initial installation status = %q", installationOne.Status)
	}
	if _, err := service.SimulateSolutionInstallation(
		ctx,
		installationOne.ID,
	); err != nil {
		t.Fatalf("simulate initial solution: %v", err)
	}
	installationOne, err = service.ApproveSolutionInstallation(
		ctx,
		installationOne.ID,
	)
	if err != nil {
		t.Fatalf("approve initial solution: %v", err)
	}
	if installationOne.Status != models.SolutionInstallationActive {
		t.Fatalf("approved initial installation = %+v", installationOne)
	}
	var firstRelease models.ConfigurationRelease
	if err := db.Where("id = ?", installationOne.ReleaseID).
		First(&firstRelease).Error; err != nil {
		t.Fatal(err)
	}

	versionTwo := signedConfigurationServiceSolution(
		t,
		"1.1.0",
		true,
		privateKey,
	)
	preview, err := service.PreviewSolutionUpgrade(
		ctx,
		*versionTwo,
		publicKey,
	)
	if err != nil {
		t.Fatalf("preview upgrade: %v", err)
	}
	if preview.BaseInstallationID == nil ||
		*preview.BaseInstallationID != installationOne.ID ||
		preview.Diff.Compatible ||
		len(preview.Diff.BreakingChanges) == 0 {
		t.Fatalf("upgrade preview = %+v", preview)
	}
	installationTwo, err := service.PrepareSolutionInstallation(
		ctx,
		*versionTwo,
		publicKey,
	)
	if err != nil {
		t.Fatalf("prepare upgrade: %v", err)
	}
	if _, err := service.SimulateSolutionInstallation(
		ctx,
		installationTwo.ID,
	); err != nil {
		t.Fatalf("simulate upgrade: %v", err)
	}
	installationTwo, err = service.ApproveSolutionInstallation(
		ctx,
		installationTwo.ID,
	)
	if err != nil {
		t.Fatalf("approve upgrade: %v", err)
	}
	if installationTwo.Status != models.SolutionInstallationActive {
		t.Fatalf("approved upgrade = %+v", installationTwo)
	}
	if err := db.First(&installationOne, "id = ?", installationOne.ID).Error; err != nil {
		t.Fatal(err)
	}
	if installationOne.Status != models.SolutionInstallationSuperseded {
		t.Fatalf("old installation status = %q", installationOne.Status)
	}

	rollback, err := service.RollbackConfigurationRelease(
		ctx,
		firstRelease.ID,
	)
	if err != nil {
		t.Fatalf("rollback release: %v", err)
	}
	if rollback.RollbackOfReleaseID == nil ||
		*rollback.RollbackOfReleaseID != firstRelease.ID ||
		rollback.Status != models.ConfigurationStatusPublished ||
		rollback.Version <= firstRelease.Version {
		t.Fatalf("rollback release = %+v", rollback)
	}
	if rollback.SnapshotHash != firstRelease.SnapshotHash {
		t.Fatalf(
			"rollback hash = %q, want %q",
			rollback.SnapshotHash,
			firstRelease.SnapshotHash,
		)
	}
	var stillPublished models.ConfigurationRelease
	if err := db.First(&stillPublished, "id = ?", firstRelease.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stillPublished.Status != models.ConfigurationStatusPublished ||
		stillPublished.SnapshotHash != firstRelease.SnapshotHash {
		t.Fatalf("rollback mutated target release: %+v", stillPublished)
	}

	otherContext := projectConfigurationTestContext(t, otherProject)
	if _, err := service.ApproveSolutionInstallation(
		otherContext,
		installationTwo.ID,
	); !errors.Is(err, ErrConfigurationNotFound) {
		t.Fatalf("cross-project installation error = %v", err)
	}
	if _, err := service.RollbackConfigurationRelease(
		otherContext,
		firstRelease.ID,
	); !errors.Is(err, ErrConfigurationNotFound) {
		t.Fatalf("cross-project rollback error = %v", err)
	}
}

func TestBootstrapProjectConfigurationUsesProjectUniqueUUIDv7Versions(t *testing.T) {
	db, project, otherProject := newProjectConfigurationTestDB(t)
	service, err := NewProjectConfigurationService(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := projectConfigurationTestContext(t, project)
	first, err := service.BootstrapProjectConfiguration(ctx)
	if err != nil {
		t.Fatalf("bootstrap configuration: %v", err)
	}
	second, err := service.BootstrapProjectConfiguration(ctx)
	if err != nil {
		t.Fatalf("repeat bootstrap configuration: %v", err)
	}
	if first.ID != second.ID ||
		first.Status != models.ConfigurationStatusPublished {
		t.Fatalf("bootstrap is not idempotent: first=%+v second=%+v", first, second)
	}
	var requestTypes []models.RequestTypeVersion
	if err := db.Where(
		"organization_id = ? AND project_id = ?",
		project.OrganizationID,
		project.ID,
	).Order("id ASC").Find(&requestTypes).Error; err != nil {
		t.Fatal(err)
	}
	if len(requestTypes) != 6 {
		t.Fatalf("bootstrap request type count = %d, want 6", len(requestTypes))
	}
	for _, requestType := range requestTypes {
		parsed, parseErr := uuid.Parse(requestType.ID)
		if parseErr != nil || parsed.Version() != 7 {
			t.Errorf("bootstrap request type id is not UUIDv7: %q", requestType.ID)
		}
		if requestType.Status != models.ConfigurationStatusPublished {
			t.Errorf("bootstrap request type %q is not published", requestType.ID)
		}
	}
	var workflow models.WorkflowVersion
	if err := db.Where(
		"organization_id = ? AND project_id = ? AND key = ?",
		project.OrganizationID,
		project.ID,
		"default",
	).First(&workflow).Error; err != nil {
		t.Fatal(err)
	}
	parsedWorkflow, err := uuid.Parse(workflow.ID)
	if err != nil || parsedWorkflow.Version() != 7 {
		t.Fatalf("bootstrap workflow id is not UUIDv7: %q", workflow.ID)
	}
	states, err := workflow.StateDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	wantCategories := map[string]models.LifecycleCategory{
		"open":        models.LifecycleCategoryNew,
		"in_progress": models.LifecycleCategoryActive,
		"pending":     models.LifecycleCategoryWaiting,
		"resolved":    models.LifecycleCategoryResolved,
		"closed":      models.LifecycleCategoryClosed,
		"cancelled":   models.LifecycleCategoryCancelled,
	}
	for _, state := range states {
		if state.LifecycleCategory != wantCategories[state.Key] {
			t.Errorf(
				"bootstrap state %q category = %q, want %q",
				state.Key,
				state.LifecycleCategory,
				wantCategories[state.Key],
			)
		}
	}
	otherContext := projectConfigurationTestContext(t, otherProject)
	otherRelease, err := service.BootstrapProjectConfiguration(otherContext)
	if err != nil {
		t.Fatal(err)
	}
	otherSnapshot, err := otherRelease.ConfigurationSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	firstSnapshot, err := first.ConfigurationSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	firstIDs := make(map[string]struct{}, len(firstSnapshot.RequestTypeVersionIDs)+1)
	for _, id := range firstSnapshot.RequestTypeVersionIDs {
		firstIDs[id] = struct{}{}
	}
	firstIDs[firstSnapshot.WorkflowVersionIDs[0]] = struct{}{}
	for _, id := range append(
		otherSnapshot.RequestTypeVersionIDs,
		otherSnapshot.WorkflowVersionIDs...,
	) {
		if _, collision := firstIDs[id]; collision {
			t.Fatalf("project bootstrap version ID collision: %s", id)
		}
	}
}

func newProjectConfigurationTestDB(
	t *testing.T,
) (*gorm.DB, models.Project, models.Project) {
	t.Helper()
	db := newProjectConfigurationStandaloneDB(t, t.Name())
	var projects []models.Project
	if err := db.Order("id ASC").Find(&projects).Error; err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 {
		t.Fatalf("project fixture count = %d, want 2", len(projects))
	}
	return db, projects[0], projects[1]
}

func newProjectConfigurationStandaloneDB(
	t *testing.T,
	suffix string,
) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+suffix+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.Team{},
		&models.Queue{},
		&models.RequestTypeVersion{},
		&models.WorkflowVersion{},
		&models.ConfigurationRelease{},
		&models.ProjectSolutionInstallation{},
	); err != nil {
		t.Fatal(err)
	}
	organization := models.Organization{
		Slug:   "configuration-" + suffix,
		Name:   "Configuration",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "OPS",
		Name:           "Operations",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	projects := []models.Project{
		{
			OrganizationID: organization.ID,
			BusinessUnitID: unit.ID,
			Key:            "OPS",
			Name:           "Operations",
			Status:         models.ProjectStatusActive,
		},
		{
			OrganizationID: organization.ID,
			BusinessUnitID: unit.ID,
			Key:            "SEC",
			Name:           "Security",
			Status:         models.ProjectStatusActive,
		},
	}
	if err := db.Create(&projects).Error; err != nil {
		t.Fatal(err)
	}
	for _, project := range projects {
		queue := models.Queue{
			ProjectID: project.ID,
			Key:       "default",
			Name:      "Default",
			Status:    models.QueueStatusActive,
			IsDefault: true,
		}
		if err := db.Create(&queue).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func projectConfigurationTestContext(
	t *testing.T,
	project models.Project,
) context.Context {
	t.Helper()
	return projectConfigurationTestContextWithScope(t, project.Scope())
}

func projectConfigurationTestContextWithScope(
	t *testing.T,
	scope models.ProjectScope,
) context.Context {
	t.Helper()
	ctx, err := WithOperationContext(context.Background(), OperationContext{
		Scope:  scope,
		Actor:  models.HumanActor(1),
		Source: SourceProtocolHumanREST,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func createConfigurationDraftVersions(
	t *testing.T,
	service *ProjectConfigurationService,
	ctx context.Context,
) (*models.RequestTypeVersion, *models.WorkflowVersion) {
	t.Helper()
	requestType, err := service.CreateRequestTypeDraft(ctx, RequestTypeDraftInput{
		Key:        "incident",
		Name:       "Incident",
		WorkClass:  models.WorkClassIncident,
		JSONSchema: configurationServiceJSONSchema(false),
		UISchema:   json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create request type draft: %v", err)
	}
	workflow, err := service.CreateWorkflowDraft(ctx, WorkflowDraftInput{
		Key:         "default",
		Name:        "Default",
		States:      configurationServiceWorkflowStates(),
		Transitions: configurationServiceWorkflowTransitions(),
	})
	if err != nil {
		t.Fatalf("create workflow draft: %v", err)
	}
	return requestType, workflow
}

func configurationServiceWorkflowStates() []models.WorkflowStateDefinition {
	return []models.WorkflowStateDefinition{
		{
			Key: "open", Name: "Open",
			LifecycleCategory: models.LifecycleCategoryNew,
			IsInitial:         true,
		},
		{
			Key: "in_progress", Name: "In progress",
			LifecycleCategory: models.LifecycleCategoryActive,
		},
		{
			Key: "resolved", Name: "Resolved",
			LifecycleCategory: models.LifecycleCategoryResolved,
			IsTerminal:        true,
		},
	}
}

func configurationServiceWorkflowTransitions() []models.WorkflowTransitionDefinition {
	return []models.WorkflowTransitionDefinition{
		{Key: "start", Name: "Start", From: "open", To: "in_progress"},
		{Key: "resolve", Name: "Resolve", From: "in_progress", To: "resolved"},
	}
}

func configurationServiceJSONSchema(addRequiredImpact bool) json.RawMessage {
	required := `["title"]`
	properties := `"title":{"type":"string"}`
	if addRequiredImpact {
		required = `["title","impact"]`
		properties += `,"impact":{"type":"string"}`
	}
	return json.RawMessage(
		`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{` +
			properties +
			`},"required":` +
			required +
			`,"additionalProperties":false}`,
	)
}

func signedConfigurationServiceSolution(
	t *testing.T,
	version string,
	addRequiredImpact bool,
	privateKey ed25519.PrivateKey,
) *models.IndustrySolutionPackage {
	t.Helper()
	snapshot := models.IndustrySolutionSnapshot{
		RequestTypes: []models.RequestTypeTemplate{
			{
				Key:        "incident",
				Name:       "Incident",
				WorkClass:  models.WorkClassIncident,
				JSONSchema: configurationServiceJSONSchema(addRequiredImpact),
				UISchema:   json.RawMessage(`{}`),
			},
		},
		Workflows: []models.WorkflowTemplate{
			{
				Key:         "default",
				Name:        "Default",
				States:      configurationServiceWorkflowStates(),
				Transitions: configurationServiceWorkflowTransitions(),
			},
		},
	}
	manifest := models.IndustrySolutionManifest{
		SchemaVersion: "1.0",
		PackageKey:    "it-operations",
		Name:          "IT Operations",
		Industry:      "technology",
		Version:       version,
		Terminology:   map[string]string{"ticket": "工单"},
		TemplateReferences: []models.SolutionTemplateReference{
			{Kind: models.SolutionTemplateRequestType, Key: "incident"},
			{Kind: models.SolutionTemplateWorkflow, Key: "default"},
		},
	}
	solution, err := models.SignIndustrySolutionPackage(
		manifest,
		snapshot,
		"test-signer",
		privateKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	return solution
}

func assertConfigurationVersionStatus(
	t *testing.T,
	db *gorm.DB,
	model any,
	id string,
	want models.ConfigurationVersionStatus,
) {
	t.Helper()
	var row struct {
		Status models.ConfigurationVersionStatus
	}
	if err := db.Model(model).Select("status").
		Where("id = ?", id).
		Scan(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != want {
		t.Fatalf("%T %q status = %q, want %q", model, id, row.Status, want)
	}
}
