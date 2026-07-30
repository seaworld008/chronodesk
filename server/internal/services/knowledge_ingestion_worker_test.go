package services

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
)

func TestKnowledgeIngestionWorkerCompletesOnlyRequestedProject(t *testing.T) {
	db := newKnowledgeServiceTestDB(t)
	service := newKnowledgeServiceForTest(t, db, nil, nil, nil)
	scope := models.ProjectScope{OrganizationID: 12, ProjectID: 120}
	otherScope := models.ProjectScope{OrganizationID: 12, ProjectID: 121}
	task := seedKnowledgeWorkerTask(t, service, scope, "worker-pdf")
	otherTask := seedKnowledgeWorkerTask(t, service, otherScope, "worker-pdf")
	scans := 0
	parses := 0
	worker, err := NewKnowledgeIngestionWorker(
		KnowledgeIngestionWorkerOptions{
			DB:      db,
			Service: service,
			Scanner: KnowledgeVirusScannerFunc(func(
				ctx context.Context,
				reference models.KnowledgeObjectReference,
			) (KnowledgeVirusScanResult, error) {
				assertKnowledgeWorkerExternalContext(
					t,
					ctx,
					scope,
					"knowledge-worker-test",
				)
				scans++
				if reference.VersionID == "" {
					t.Fatal("scanner did not receive immutable object version")
				}
				return KnowledgeVirusScanResult{
					Status: models.VirusScanClean,
					Detail: "clean",
				}, nil
			}),
			Parsers: map[string]KnowledgeDocumentParser{
				"worker-pdf": KnowledgeDocumentParserFunc(func(
					ctx context.Context,
					_ models.KnowledgeObjectReference,
				) ([]KnowledgeChunkInput, error) {
					assertKnowledgeWorkerExternalContext(
						t,
						ctx,
						scope,
						"knowledge-worker-test",
					)
					parses++
					page := 1
					return []KnowledgeChunkInput{{
						PageNumber:  &page,
						SectionPath: "恢复步骤",
						Content:     "先验证备份，再执行恢复。",
						Snippet:     "先验证备份",
						TokenCount:  12,
					}}, nil
				}),
			},
			WorkerID: "knowledge-worker-test",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := worker.ProcessProject(
		context.Background(),
		scope,
		10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Selected != 1 || result.Completed != 1 ||
		result.Quarantined != 0 || result.Failed != 0 ||
		scans != 1 || parses != 1 {
		t.Fatalf("unexpected worker result: %+v scans=%d parses=%d", result, scans, parses)
	}
	var completed models.KnowledgeIngestionTask
	if err := db.First(&completed, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if completed.Status != models.KnowledgeIngestionCompleted {
		t.Fatalf("task status = %s", completed.Status)
	}
	var untouched models.KnowledgeIngestionTask
	if err := db.First(&untouched, "id = ?", otherTask.ID).Error; err != nil {
		t.Fatal(err)
	}
	if untouched.Status != models.KnowledgeIngestionQueued {
		t.Fatalf("other project task status = %s", untouched.Status)
	}
	var chunks []models.KnowledgeChunk
	if err := db.Where(
		"organization_id = ? AND project_id = ? AND ingestion_task_id = ?",
		scope.OrganizationID,
		scope.ProjectID,
		task.ID,
	).Find(&chunks).Error; err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || chunks[0].ContentHash == "" {
		t.Fatalf("unexpected persisted chunks: %+v", chunks)
	}
}

func TestKnowledgeIngestionWorkerProcessesEveryActiveProjectWithScopedShortTransactions(
	t *testing.T,
) {
	db := newKnowledgeServiceTestDB(t)
	if err := db.AutoMigrate(
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
	); err != nil {
		t.Fatal(err)
	}
	service := newKnowledgeServiceForTest(t, db, nil, nil, nil)
	organization := models.Organization{
		Slug:   "knowledge-worker",
		Name:   "Knowledge Worker",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "KNOWLEDGE",
		Name:           "Knowledge",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	projectA := createKnowledgeWorkerProject(
		t,
		db,
		organization.ID,
		unit.ID,
		"KNOW-A",
		models.ProjectStatusActive,
	)
	projectB := createKnowledgeWorkerProject(
		t,
		db,
		organization.ID,
		unit.ID,
		"KNOW-B",
		models.ProjectStatusActive,
	)
	archivedProject := createKnowledgeWorkerProject(
		t,
		db,
		organization.ID,
		unit.ID,
		"KNOW-ARCHIVED",
		models.ProjectStatusArchived,
	)
	taskA := seedKnowledgeWorkerTask(t, service, projectA.Scope(), "worker-pdf")
	taskB := seedKnowledgeWorkerTask(t, service, projectB.Scope(), "worker-pdf")
	archivedTask := seedKnowledgeWorkerTask(
		t,
		service,
		archivedProject.Scope(),
		"worker-pdf",
	)

	const workerID = "knowledge-worker-active-projects"
	activeScopes := map[models.ProjectScope]bool{
		projectA.Scope(): true,
		projectB.Scope(): true,
	}
	scannedScopes := map[models.ProjectScope]int{}
	parsedScopes := map[models.ProjectScope]int{}
	scopedDatabaseCalls := assertKnowledgeWorkerDatabaseScope(
		t,
		db,
		workerID,
	)
	worker, err := NewKnowledgeIngestionWorker(
		KnowledgeIngestionWorkerOptions{
			DB:      db,
			Service: service,
			Scanner: KnowledgeVirusScannerFunc(func(
				ctx context.Context,
				_ models.KnowledgeObjectReference,
			) (KnowledgeVirusScanResult, error) {
				operation := assertKnowledgeWorkerExternalOperation(
					t,
					ctx,
					workerID,
				)
				if !activeScopes[operation.Scope] {
					t.Fatalf(
						"scanner received inactive project scope: %+v",
						operation.Scope,
					)
				}
				scannedScopes[operation.Scope]++
				return KnowledgeVirusScanResult{
					Status: models.VirusScanClean,
					Detail: "clean",
				}, nil
			}),
			Parsers: map[string]KnowledgeDocumentParser{
				"worker-pdf": KnowledgeDocumentParserFunc(func(
					ctx context.Context,
					_ models.KnowledgeObjectReference,
				) ([]KnowledgeChunkInput, error) {
					operation := assertKnowledgeWorkerExternalOperation(
						t,
						ctx,
						workerID,
					)
					if !activeScopes[operation.Scope] {
						t.Fatalf(
							"parser received inactive project scope: %+v",
							operation.Scope,
						)
					}
					parsedScopes[operation.Scope]++
					page := 1
					return []KnowledgeChunkInput{{
						PageNumber:  &page,
						SectionPath: "active project",
						Content:     "project-scoped parsed content",
						Snippet:     "project-scoped",
						TokenCount:  4,
					}}, nil
				}),
			},
			WorkerID: workerID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := worker.ProcessActiveProjects(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.Selected != 2 || result.Completed != 2 ||
		result.Quarantined != 0 || result.Failed != 0 ||
		result.Skipped != 0 {
		t.Fatalf("active-project result = %+v", result)
	}
	for scope := range activeScopes {
		if scannedScopes[scope] != 1 || parsedScopes[scope] != 1 {
			t.Fatalf(
				"scope %+v external calls: scans=%d parses=%d",
				scope,
				scannedScopes[scope],
				parsedScopes[scope],
			)
		}
	}
	if *scopedDatabaseCalls == 0 {
		t.Fatal("worker did not execute project-scoped database operations")
	}

	for _, task := range []*models.KnowledgeIngestionTask{taskA, taskB} {
		var persisted models.KnowledgeIngestionTask
		if err := db.First(&persisted, "id = ?", task.ID).Error; err != nil {
			t.Fatal(err)
		}
		if persisted.Status != models.KnowledgeIngestionCompleted {
			t.Fatalf("active task %q status = %s", task.ID, persisted.Status)
		}
	}
	var persistedArchived models.KnowledgeIngestionTask
	if err := db.First(
		&persistedArchived,
		"id = ?",
		archivedTask.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if persistedArchived.Status != models.KnowledgeIngestionQueued {
		t.Fatalf(
			"archived project task status = %s",
			persistedArchived.Status,
		)
	}
}

func TestKnowledgeIngestionWorkerQuarantinesBeforeParser(t *testing.T) {
	db := newKnowledgeServiceTestDB(t)
	service := newKnowledgeServiceForTest(t, db, nil, nil, nil)
	scope := models.ProjectScope{OrganizationID: 13, ProjectID: 130}
	task := seedKnowledgeWorkerTask(t, service, scope, "worker-pdf")
	parserCalled := false
	worker, err := NewKnowledgeIngestionWorker(
		KnowledgeIngestionWorkerOptions{
			DB:      db,
			Service: service,
			Scanner: KnowledgeVirusScannerFunc(func(
				context.Context,
				models.KnowledgeObjectReference,
			) (KnowledgeVirusScanResult, error) {
				return KnowledgeVirusScanResult{
					Status: models.VirusScanInfected,
					Detail: strings.Repeat("x", 1200),
				}, nil
			}),
			Parsers: map[string]KnowledgeDocumentParser{
				"worker-pdf": KnowledgeDocumentParserFunc(func(
					context.Context,
					models.KnowledgeObjectReference,
				) ([]KnowledgeChunkInput, error) {
					parserCalled = true
					return nil, errors.New("must not run")
				}),
			},
			WorkerID: "knowledge-worker-test",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := worker.ProcessProject(context.Background(), scope, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.Quarantined != 1 || parserCalled {
		t.Fatalf("unexpected quarantine result: %+v parser=%t", result, parserCalled)
	}
	var persisted models.KnowledgeIngestionTask
	if err := db.First(&persisted, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Status != models.KnowledgeIngestionQuarantined ||
		len(persisted.FailureDetail) > 1000 {
		t.Fatalf("unexpected quarantined task: %+v", persisted)
	}
}

func TestKnowledgeIngestionWorkerRejectsOuterDatabaseTransaction(t *testing.T) {
	db := newKnowledgeServiceTestDB(t)
	service := newKnowledgeServiceForTest(t, db, nil, nil, nil)
	scope := models.ProjectScope{OrganizationID: 15, ProjectID: 150}
	scannerCalled := false
	worker, err := NewKnowledgeIngestionWorker(
		KnowledgeIngestionWorkerOptions{
			DB:      db,
			Service: service,
			Scanner: KnowledgeVirusScannerFunc(func(
				context.Context,
				models.KnowledgeObjectReference,
			) (KnowledgeVirusScanResult, error) {
				scannerCalled = true
				return KnowledgeVirusScanResult{
					Status: models.VirusScanClean,
				}, nil
			}),
			WorkerID: "knowledge-worker-outer-transaction",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	var processErr error
	if err := scopeddb.WithProjectScopeContextTransaction(
		context.Background(),
		db,
		scope,
		func(scopedContext context.Context) error {
			_, processErr = worker.ProcessProject(
				scopedContext,
				scope,
				1,
			)
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if processErr == nil ||
		!strings.Contains(processErr.Error(), "outside a database transaction") {
		t.Fatalf("ProcessProject() error = %v", processErr)
	}
	if scannerCalled {
		t.Fatal("scanner ran inside an outer database transaction")
	}
}

func TestKnowledgeIngestionFailureTransitionIsWorkerOnly(t *testing.T) {
	db := newKnowledgeServiceTestDB(t)
	service := newKnowledgeServiceForTest(t, db, nil, nil, nil)
	scope := models.ProjectScope{OrganizationID: 14, ProjectID: 140}
	task := seedKnowledgeWorkerTask(t, service, scope, "missing-parser")
	if err := service.FailIngestion(
		knowledgeServiceTestContext(t, scope),
		task.ID,
		"forged_failure",
		"浏览器伪造",
	); !errors.Is(err, ErrKnowledgeWorkerRequired) {
		t.Fatalf("human failure transition error = %v", err)
	}
}

func createKnowledgeWorkerProject(
	t *testing.T,
	db *gorm.DB,
	organizationID uint,
	businessUnitID uint,
	key models.ProjectKey,
	status models.ProjectStatus,
) models.Project {
	t.Helper()
	project := models.Project{
		OrganizationID: organizationID,
		BusinessUnitID: businessUnitID,
		Key:            key,
		Name:           string(key),
		Status:         status,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	return project
}

func assertKnowledgeWorkerExternalContext(
	t *testing.T,
	ctx context.Context,
	scope models.ProjectScope,
	workerID string,
) {
	t.Helper()
	operation := assertKnowledgeWorkerExternalOperation(t, ctx, workerID)
	if operation.Scope != scope {
		t.Fatalf(
			"external operation scope = %+v, want %+v",
			operation.Scope,
			scope,
		)
	}
}

func assertKnowledgeWorkerExternalOperation(
	t *testing.T,
	ctx context.Context,
	workerID string,
) OperationContext {
	t.Helper()
	if scopeddb.HasTransaction(ctx) {
		t.Fatal("external knowledge I/O inherited a database transaction")
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Actor != models.SystemActor(workerID) ||
		operation.Source != SourceProtocolWorker {
		t.Fatalf(
			"external knowledge operation provenance = %+v/%s",
			operation.Actor,
			operation.Source,
		)
	}
	return operation
}

func assertKnowledgeWorkerDatabaseScope(
	t *testing.T,
	db *gorm.DB,
	workerID string,
) *int {
	t.Helper()
	calls := 0
	callbackName := "test:knowledge_worker_project_scope"
	assertScope := func(tx *gorm.DB) {
		if tx == nil || tx.Statement == nil ||
			tx.Statement.Context == nil {
			return
		}
		operation, err := OperationContextFromContext(tx.Statement.Context)
		if err != nil ||
			operation.Actor != models.SystemActor(workerID) {
			return
		}
		calls++
		if operation.Source != SourceProtocolWorker {
			t.Errorf("worker database source = %s", operation.Source)
		}
		if !scopeddb.HasTransaction(tx.Statement.Context) {
			t.Error("worker database operation has no scoped transaction binding")
		}
		if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
			t.Error("worker database operation did not use a database transaction")
		}
	}
	for _, callback := range []struct {
		register func(string, func(*gorm.DB)) error
		remove   func(string) error
	}{
		{
			register: db.Callback().Query().After("gorm:query").Register,
			remove:   db.Callback().Query().Remove,
		},
		{
			register: db.Callback().Create().After("gorm:create").Register,
			remove:   db.Callback().Create().Remove,
		},
		{
			register: db.Callback().Update().After("gorm:update").Register,
			remove:   db.Callback().Update().Remove,
		},
	} {
		if err := callback.register(callbackName, assertScope); err != nil {
			t.Fatal(err)
		}
		remove := callback.remove
		t.Cleanup(func() {
			_ = remove(callbackName)
		})
	}
	return &calls
}

func seedKnowledgeWorkerTask(
	t *testing.T,
	service *KnowledgeService,
	scope models.ProjectScope,
	parserKey string,
) *models.KnowledgeIngestionTask {
	t.Helper()
	ctx := knowledgeServiceTestContext(t, scope)
	article, err := service.CreateArticle(ctx, CreateKnowledgeArticleInput{
		Key:                "worker-" + parserKey,
		Title:              "隔离解析测试",
		GrantProjectAccess: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	version, err := service.CreateVersion(
		ctx,
		article.ID,
		CreateKnowledgeVersionInput{
			Title: "隔离解析测试 v1",
			Source: models.KnowledgeObjectReference{
				Provider:    "s3",
				Bucket:      "knowledge",
				Key:         "projects/worker/source.pdf",
				VersionID:   "immutable-v1",
				FileName:    "source.pdf",
				MimeType:    "application/pdf",
				SizeBytes:   2048,
				ContentHash: strings.Repeat("a", 64),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.QueueIngestion(ctx, version.ID, parserKey)
	if err != nil {
		t.Fatal(err)
	}
	return task
}
