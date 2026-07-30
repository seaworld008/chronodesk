package a2a

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const a2aTestProjectKey = "TEST"

var a2aDefaultTestScope = models.ProjectScope{
	OrganizationID: 101,
	ProjectID:      201,
}

func a2aTestContext(t *testing.T) context.Context {
	t.Helper()
	return a2aTestContextForScope(t, a2aTestProjectKey, a2aDefaultTestScope)
}

func a2aTestContextForScope(
	t *testing.T,
	projectKey string,
	scope models.ProjectScope,
) context.Context {
	t.Helper()
	actor := models.ServicePrincipalActor("a2a-test-principal")
	ctx, err := services.WithOperationContext(
		context.Background(),
		services.OperationContext{
			Scope:        scope,
			Actor:        actor,
			Source:       services.SourceProtocolA2A,
			CredentialID: "a2a-test-credential",
		},
	)
	if err != nil {
		t.Fatalf("bind A2A test operation context: %v", err)
	}
	ctx, err = WithProjectBinding(ctx, ProjectBinding{
		ProjectKey: projectKey,
		Scope:      scope,
	})
	if err != nil {
		t.Fatalf("bind A2A test project: %v", err)
	}
	return ctx
}

func a2aTestRequestMetadata() map[string]any {
	return map[string]any{MetadataProjectKey: a2aTestProjectKey}
}

func TestA2AGormStorePersistsAndFiltersEveryProjectOwnedTable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlite handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	store := NewGormStoreWithProtector(db, nil)
	if err := store.AutoMigrate(); err != nil {
		t.Fatalf("migrate A2A models: %v", err)
	}

	scopeA := models.ProjectScope{OrganizationID: 101, ProjectID: 201}
	scopeB := models.ProjectScope{OrganizationID: 102, ProjectID: 202}
	ctxA := a2aTestContextForScope(t, "PROJECT_A", scopeA)
	ctxB := a2aTestContextForScope(t, "PROJECT_B", scopeB)
	now := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	text := "project A message"
	artifactText := "project A artifact"
	status := TaskStatus{State: TaskStateSubmitted, Timestamp: now}
	taskA := Task{
		ID:        "project-a-task",
		ContextID: "project-a-context",
		Status:    status,
		History: []Message{{
			MessageID:     "project-a-message",
			Role:          RoleUser,
			Parts:         []Part{{Text: &text}},
			RequestDigest: "project-a-digest",
		}},
		Artifacts: []Artifact{{
			ArtifactID: "project-a-artifact",
			Parts:      []Part{{Text: &artifactText}},
		}},
		StatusHistory: []TaskStatus{status},
		CreatedAt:     now,
		LastModified:  now,
		Version:       1,
	}
	if err := store.CreateTask(ctxA, taskA); err != nil {
		t.Fatalf("create project A task: %v", err)
	}
	eventA, err := store.AppendEvent(ctxA, StoredEvent{
		TaskID:          taskA.ID,
		ContextID:       taskA.ContextID,
		ResourceVersion: taskA.Version,
		Payload:         StreamResponse{Task: &taskA},
		CreatedAt:       now,
	})
	if err != nil {
		t.Fatalf("append project A event: %v", err)
	}
	if err := store.CreatePushConfig(ctxA, PushNotificationConfig{
		ID:        "project-a-push",
		TaskID:    taskA.ID,
		URL:       "https://hooks.example.test/a2a",
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("create project A push config: %v", err)
	}

	for _, table := range []struct {
		name  string
		model any
	}{
		{name: "agent_tasks", model: &models.AgentTask{}},
		{name: "agent_messages", model: &models.AgentMessage{}},
		{name: "agent_artifacts", model: &models.AgentArtifact{}},
		{name: "agent_task_status_history", model: &models.AgentTaskStatusHistory{}},
		{name: "agent_task_events", model: &models.AgentTaskEvent{}},
		{name: "agent_push_notification_configs", model: &models.AgentPushNotificationConfig{}},
	} {
		t.Run(table.name+" carries project scope", func(t *testing.T) {
			var total int64
			if err := db.Model(table.model).Count(&total).Error; err != nil {
				t.Fatal(err)
			}
			var scoped int64
			if err := db.Model(table.model).
				Where(
					"organization_id = ? AND project_id = ?",
					scopeA.OrganizationID,
					scopeA.ProjectID,
				).
				Count(&scoped).Error; err != nil {
				t.Fatal(err)
			}
			if total != 1 || scoped != total {
				t.Fatalf("total=%d project-scoped=%d, want all rows in project A", total, scoped)
			}
		})
	}

	if _, err := store.GetTask(ctxB, taskA.ID); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("cross-project GetTask error=%v, want ErrTaskNotFound", err)
	}
	if _, err := store.FindTaskByMessageID(ctxB, "project-a-message"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("cross-project FindTaskByMessageID error=%v, want ErrTaskNotFound", err)
	}
	listB, err := store.ListTasks(ctxB, ListTasksParams{PageSize: 20})
	if err != nil {
		t.Fatalf("cross-project ListTasks: %v", err)
	}
	if listB.TotalSize != 0 || len(listB.Tasks) != 0 {
		t.Fatalf("cross-project ListTasks leaked project A: %+v", listB)
	}
	if _, err := store.EventsAfter(ctxB, taskA.ID, "", 20); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("cross-project EventsAfter error=%v, want ErrTaskNotFound", err)
	}
	if _, err := store.GetPushConfig(ctxB, taskA.ID, "project-a-push"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("cross-project GetPushConfig error=%v, want ErrTaskNotFound", err)
	}
	if _, _, err := store.ListPushConfigs(ctxB, taskA.ID, "", 20); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("cross-project ListPushConfigs error=%v, want ErrTaskNotFound", err)
	}
	if err := store.DeletePushConfig(ctxB, taskA.ID, "project-a-push"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("cross-project DeletePushConfig error=%v, want ErrTaskNotFound", err)
	}
	if err := store.UpdateTask(ctxB, taskA); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("cross-project UpdateTask error=%v, want ErrTaskNotFound", err)
	}
	if err := store.ClaimTaskExecution(
		ctxB,
		taskA.ID,
		"project-a-message",
		taskA.Version,
		"cross-project-claim",
		now,
		now.Add(time.Minute),
	); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("cross-project ClaimTaskExecution error=%v, want ErrTaskNotFound", err)
	}
	if _, err := store.AppendEvent(ctxB, StoredEvent{
		TaskID:    taskA.ID,
		ContextID: taskA.ContextID,
		Payload:   StreamResponse{Task: &taskA},
		CreatedAt: now,
	}); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("cross-project AppendEvent error=%v, want ErrTaskNotFound", err)
	}
	if _, err := store.GetPushConfig(ctxA, taskA.ID, "project-a-push"); err != nil {
		t.Fatalf("cross-project delete changed project A push config: %v", err)
	}

	collidingText := "project B must not reuse project A message"
	collidingTask := Task{
		ID:        "project-b-colliding-task",
		ContextID: "project-b-colliding-context",
		Status:    status,
		History: []Message{{
			MessageID:     "project-a-message",
			Role:          RoleUser,
			Parts:         []Part{{Text: &collidingText}},
			RequestDigest: "project-b-digest",
		}},
		StatusHistory: []TaskStatus{status},
		CreatedAt:     now,
		LastModified:  now,
		Version:       1,
	}
	if err := store.CreateTask(ctxB, collidingTask); !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("cross-project message-id collision error=%v, want ErrTaskConflict", err)
	}
	if _, err := store.GetTask(ctxB, collidingTask.ID); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("message collision left a partial project B task: %v", err)
	}

	taskB := Task{
		ID:            "project-b-task",
		ContextID:     "project-b-context",
		Status:        status,
		StatusHistory: []TaskStatus{status},
		CreatedAt:     now,
		LastModified:  now,
		Version:       1,
	}
	if err := store.CreateTask(ctxB, taskB); err != nil {
		t.Fatalf("create project B task: %v", err)
	}
	if _, err := store.AppendEvent(ctxB, StoredEvent{
		TaskID:          taskB.ID,
		ContextID:       taskB.ContextID,
		ResourceVersion: taskB.Version,
		Payload:         StreamResponse{Task: &taskB},
		CreatedAt:       now,
	}); err != nil {
		t.Fatalf("append project B event: %v", err)
	}
	if _, err := store.EventsAfter(ctxB, taskB.ID, eventA.Cursor, 20); !errors.Is(err, ErrInvalidEventCursor) {
		t.Fatalf("project A cursor used in project B error=%v, want ErrInvalidEventCursor", err)
	}
}

func TestA2AGormStoreRequiresMatchingTrustedOperationScope(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := NewGormStoreWithProtector(db, nil)

	for _, call := range a2aGormStorePublicCalls(store) {
		t.Run("unscoped "+call.name, func(t *testing.T) {
			if err := call.run(context.Background()); !errors.Is(
				err,
				ErrProjectBindingRequired,
			) {
				t.Fatalf(
					"unscoped store error=%v, want ErrProjectBindingRequired",
					err,
				)
			}
		})
	}

	scopeA := models.ProjectScope{OrganizationID: 101, ProjectID: 201}
	scopeB := models.ProjectScope{OrganizationID: 102, ProjectID: 202}
	ctx, err := WithProjectBinding(context.Background(), ProjectBinding{
		ProjectKey: "PROJECT_A",
		Scope:      scopeA,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = services.WithOperationContext(ctx, services.OperationContext{
		Scope:        scopeB,
		Actor:        models.ServicePrincipalActor("a2a-test-principal"),
		Source:       services.SourceProtocolA2A,
		CredentialID: "a2a-test-credential",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range a2aGormStorePublicCalls(store) {
		t.Run("mismatched "+call.name, func(t *testing.T) {
			if err := call.run(ctx); !errors.Is(err, ErrProjectScopeMismatch) {
				t.Fatalf(
					"mismatched store scope error=%v, want ErrProjectScopeMismatch",
					err,
				)
			}
		})
	}
}

type a2aGormStoreCall struct {
	name string
	run  func(context.Context) error
}

func a2aGormStorePublicCalls(store *GormStore) []a2aGormStoreCall {
	now := time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)
	task := Task{
		ID:           "scope-required-task",
		ContextID:    "scope-required-context",
		Status:       TaskStatus{State: TaskStateSubmitted, Timestamp: now},
		CreatedAt:    now,
		LastModified: now,
		Version:      1,
	}
	event := StoredEvent{
		TaskID:    task.ID,
		ContextID: task.ContextID,
		Payload:   StreamResponse{Task: &task},
		CreatedAt: now,
	}
	push := PushNotificationConfig{
		ID:        "scope-required-push",
		TaskID:    task.ID,
		URL:       "https://hooks.example.test/a2a",
		CreatedAt: now,
	}
	return []a2aGormStoreCall{
		{name: "CreateTask", run: func(ctx context.Context) error {
			return store.CreateTask(ctx, task)
		}},
		{name: "FindTaskByMessageID", run: func(ctx context.Context) error {
			_, err := store.FindTaskByMessageID(ctx, "scope-required-message")
			return err
		}},
		{name: "UpdateTask", run: func(ctx context.Context) error {
			return store.UpdateTask(ctx, task)
		}},
		{name: "ClaimTaskExecution", run: func(ctx context.Context) error {
			return store.ClaimTaskExecution(
				ctx,
				task.ID,
				"scope-required-message",
				task.Version,
				"scope-required-claim",
				now,
				now.Add(time.Minute),
			)
		}},
		{name: "RenewTaskExecution", run: func(ctx context.Context) error {
			return store.RenewTaskExecution(
				ctx,
				task.ID,
				"scope-required-message",
				"scope-required-claim",
				now,
				now.Add(time.Minute),
			)
		}},
		{name: "ReleaseTaskExecution", run: func(ctx context.Context) error {
			return store.ReleaseTaskExecution(
				ctx,
				task.ID,
				"scope-required-message",
				"scope-required-claim",
			)
		}},
		{name: "GetTask", run: func(ctx context.Context) error {
			_, err := store.GetTask(ctx, task.ID)
			return err
		}},
		{name: "ListTasks", run: func(ctx context.Context) error {
			_, err := store.ListTasks(ctx, ListTasksParams{})
			return err
		}},
		{name: "AppendEvent", run: func(ctx context.Context) error {
			_, err := store.AppendEvent(ctx, event)
			return err
		}},
		{name: "CreateTaskWithEvent", run: func(ctx context.Context) error {
			_, err := store.CreateTaskWithEvent(
				ctx,
				task,
				event,
				nil,
				nil,
			)
			return err
		}},
		{name: "UpdateTaskWithEvent", run: func(ctx context.Context) error {
			_, err := store.UpdateTaskWithEvent(
				ctx,
				task,
				event,
				nil,
				nil,
			)
			return err
		}},
		{name: "AppendEventWithPush", run: func(ctx context.Context) error {
			_, err := store.AppendEventWithPush(ctx, event, nil)
			return err
		}},
		{name: "EventsAfter", run: func(ctx context.Context) error {
			_, err := store.EventsAfter(ctx, task.ID, "", 20)
			return err
		}},
		{name: "CreatePushConfig", run: func(ctx context.Context) error {
			return store.CreatePushConfig(ctx, push)
		}},
		{name: "GetPushConfig", run: func(ctx context.Context) error {
			_, err := store.GetPushConfig(ctx, task.ID, push.ID)
			return err
		}},
		{name: "ListPushConfigs", run: func(ctx context.Context) error {
			_, _, err := store.ListPushConfigs(ctx, task.ID, "", 20)
			return err
		}},
		{name: "DeletePushConfig", run: func(ctx context.Context) error {
			return store.DeletePushConfig(ctx, task.ID, push.ID)
		}},
	}
}

func TestA2AGormStoreDoesNotOpenDirectTransactions(t *testing.T) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(
		fileSet,
		"gorm_store.go",
		nil,
		parser.SkipObjectResolution,
	)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Transaction" {
			return true
		}
		position := fileSet.Position(call.Pos())
		t.Errorf(
			"direct GORM Transaction at %s; use scopeddb.TransactionForContext",
			position,
		)
		return true
	})
}

func TestA2AGormStoreReusesOuterProjectScopeTransaction(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	store := NewGormStoreWithProtector(db, nil)
	if err := store.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	ctx := a2aTestContext(t)
	now := time.Date(2026, 7, 30, 9, 30, 0, 0, time.UTC)
	task := Task{
		ID:            "outer-scope-task",
		ContextID:     "outer-scope-context",
		Status:        TaskStatus{State: TaskStateSubmitted, Timestamp: now},
		StatusHistory: []TaskStatus{{State: TaskStateSubmitted, Timestamp: now}},
		CreatedAt:     now,
		LastModified:  now,
		Version:       1,
	}
	if err := store.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	err = scopeddb.WithProjectScopeContextTransaction(
		ctx,
		db,
		a2aDefaultTestScope,
		func(scopedContext context.Context) error {
			if _, err := store.GetTask(scopedContext, task.ID); err != nil {
				return err
			}
			_, err := store.AppendEventWithPush(
				scopedContext,
				StoredEvent{
					TaskID:          task.ID,
					ContextID:       task.ContextID,
					ResourceVersion: task.Version,
					Payload:         StreamResponse{Task: &task},
					CreatedAt:       now,
				},
				&recordingTransactionalPushDispatcher{},
			)
			return err
		},
	)
	if err != nil {
		t.Fatalf("reuse outer project scope transaction: %v", err)
	}
	events, err := store.EventsAfter(ctx, task.ID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("persisted events=%d, want 1", len(events))
	}
}
