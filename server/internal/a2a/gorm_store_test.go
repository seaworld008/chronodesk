package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGormStorePersistsTaskGraphAndReplayLog(t *testing.T) {
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
	clock := time.Date(2026, 7, 28, 13, 0, 0, 0, time.UTC)
	sequence := 0
	service := NewService(store, BackendFuncs{
		ProcessFunc: func(ctx context.Context, _ Task, _ Message, reporter Reporter) error {
			data := json.RawMessage(`{"ticket":"CD-42","result":"ok"}`)
			return reporter.AddArtifact(ctx, Artifact{
				ArtifactID: "receipt",
				Parts:      []Part{{Data: data}},
			}, false, true, nil)
		},
	}, ServiceOptions{
		NewID: func() string {
			sequence++
			return "id-" + strconv.Itoa(sequence)
		},
		Now: func() time.Time {
			clock = clock.Add(time.Millisecond)
			return clock
		},
	})

	ticketID := uint(42)
	text := "work ticket"
	task, err := service.SendMessage(context.Background(), SendMessageParams{
		Message: Message{
			MessageID: "message-gorm",
			Role:      RoleUser,
			Parts:     []Part{{Text: &text}},
		},
		Metadata: map[string]any{
			MetadataLinkedTicketID: ticketID,
		},
	})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	if task.Status.State != TaskStateCompleted {
		t.Fatalf("expected completed task, got %s", task.Status.State)
	}

	reloadedStore := NewGormStoreWithProtector(db, nil)
	reloaded, err := reloadedStore.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if reloaded.LinkedTicketID == nil || *reloaded.LinkedTicketID != ticketID {
		t.Fatalf("linked ticket not persisted: %#v", reloaded.LinkedTicketID)
	}
	if len(reloaded.History) != 2 {
		t.Fatalf("message graph not persisted: %#v", reloaded.History)
	}
	if len(reloaded.Artifacts) != 1 || reloaded.Artifacts[0].ArtifactID != "receipt" {
		t.Fatalf("artifact graph not persisted: %#v", reloaded.Artifacts)
	}
	assertStates(t, reloaded.StatusHistory,
		TaskStateSubmitted,
		TaskStateWorking,
		TaskStateCompleted,
	)

	events, err := reloadedStore.EventsAfter(context.Background(), task.ID, "", 100)
	if err != nil {
		t.Fatalf("load replay events: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("expected task/working/artifact/completed events, got %d", len(events))
	}
	for i := range events {
		if events[i].ResourceVersion != uint64(i+1) {
			t.Fatalf(
				"event %d resource version=%d, want %d",
				i,
				events[i].ResourceVersion,
				i+1,
			)
		}
	}
	replayed, err := reloadedStore.EventsAfter(context.Background(), task.ID, events[1].Cursor, 100)
	if err != nil {
		t.Fatalf("resume replay: %v", err)
	}
	if len(replayed) != 2 || replayed[0].Cursor == events[1].Cursor {
		t.Fatalf("unexpected replay slice: %#v", replayed)
	}
}

func TestGormExecutionClaimIsExclusiveAcrossServicesAndRenews(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open(filepath.Join(t.TempDir(), "dual-service-claim.db")),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	storeOne := NewGormStoreWithProtector(db, nil)
	if err := storeOne.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	storeTwo := NewGormStoreWithProtector(db, nil)

	var executions atomic.Int32
	var cancellations atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	backend := BackendFuncs{
		ProcessFunc: func(ctx context.Context, _ Task, _ Message, _ Reporter) error {
			if executions.Add(1) == 1 {
				close(started)
			}
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				cancellations.Add(1)
				return ctx.Err()
			}
		},
	}
	options := ServiceOptions{
		ExecutionClaimTTL:      80 * time.Millisecond,
		ExecutionRenewInterval: 15 * time.Millisecond,
	}
	serviceOne := NewService(storeOne, backend, options)
	serviceTwo := NewService(storeTwo, backend, options)
	text := "shared durable execution"
	params := SendMessageParams{
		Message: Message{
			MessageID: "dual-service-message",
			Role:      RoleUser,
			Parts:     []Part{{Text: &text}},
		},
		Configuration: SendMessageConfiguration{ReturnImmediately: true},
	}
	first, err := serviceOne.SendMessage(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first service did not start backend")
	}
	working, err := storeOne.GetTask(context.Background(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	workingVersion := working.Version
	workingModified := working.LastModified

	// Retry beyond the initial TTL. Successful renewals must keep the second
	// service from acquiring and restarting the same durable execution.
	for i := 0; i < 4; i++ {
		time.Sleep(35 * time.Millisecond)
		if _, err := serviceTwo.SendMessage(context.Background(), params); err != nil {
			t.Fatalf("service two retry %d: %v", i, err)
		}
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("cross-service retries executed backend %d times", got)
	}
	if got := cancellations.Load(); got != 0 {
		t.Fatalf("healthy renewed execution canceled %d times", got)
	}
	stillWorking, err := storeOne.GetTask(context.Background(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stillWorking.Version != workingVersion ||
		!stillWorking.LastModified.Equal(workingModified) {
		t.Fatalf(
			"lease heartbeat mutated Task resource: version=%d/%d modified=%s/%s",
			stillWorking.Version,
			workingVersion,
			stillWorking.LastModified,
			workingModified,
		)
	}

	close(release)
	deadline := time.Now().Add(time.Second)
	for {
		task, getErr := storeOne.GetTask(context.Background(), first.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if task.Status.State == TaskStateCompleted && task.ExecutionClaimID == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"Task did not complete and release claim: state=%s claim=%q",
				task.Status.State,
				task.ExecutionClaimID,
			)
		}
		time.Sleep(5 * time.Millisecond)
	}
	changedText := "mutated cross-service command"
	mismatched := params
	mismatched.Message.Parts = []Part{{Text: &changedText}}
	if _, err := serviceTwo.SendMessage(context.Background(), mismatched); !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("cross-service payload mismatch error=%v, want ErrTaskConflict", err)
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("payload mismatch re-executed backend %d times", got)
	}
}

func TestGormExpiredExecutionClaimCanRecoverAfterCrash(t *testing.T) {
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
	var executions atomic.Int32
	service := NewService(store, BackendFuncs{
		ProcessFunc: func(context.Context, Task, Message, Reporter) error {
			executions.Add(1)
			return nil
		},
	}, ServiceOptions{})
	text := "recover expired claim"
	params := SendMessageParams{Message: Message{
		MessageID: "expired-claim-message",
		Role:      RoleUser,
		Parts:     []Part{{Text: &text}},
	}}
	task, replayed, err := service.StartMessageOnce(context.Background(), params)
	if err != nil || replayed {
		t.Fatalf("start replayed=%v err=%v", replayed, err)
	}
	now := time.Now().UTC()
	if err := store.ClaimTaskExecution(
		context.Background(),
		task.ID,
		params.Message.MessageID,
		task.Version,
		"crashed-process-claim",
		now,
		now.Add(25*time.Millisecond),
	); err != nil {
		t.Fatal(err)
	}
	working, err := store.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	working.Status = TaskStatus{State: TaskStateWorking, Timestamp: now.Add(time.Millisecond)}
	working.StatusHistory = append(working.StatusHistory, working.Status)
	working.LastModified = working.Status.Timestamp
	if err := store.UpdateTask(
		withTaskExecutionClaim(
			context.Background(),
			task.ID,
			params.Message.MessageID,
			"crashed-process-claim",
		),
		working,
	); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	recovered, err := service.SendMessage(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status.State != TaskStateCompleted || executions.Load() != 1 {
		t.Fatalf(
			"expired claim recovery state=%s executions=%d",
			recovered.Status.State,
			executions.Load(),
		)
	}
}

func TestExpiredExecutionClaimCannotFenceTaskMutation(t *testing.T) {
	tests := map[string]struct {
		factory       func(*testing.T) Store
		databaseClock bool
	}{
		"memory": {
			factory: func(*testing.T) Store {
				return NewMemoryStore()
			},
		},
		"gorm": {
			databaseClock: true,
			factory: func(t *testing.T) Store {
				t.Helper()
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
				return store
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			store := test.factory(t)
			now := time.Now().UTC()
			task := Task{
				ID: "expired-fence-" + name, ContextID: "expired-fence-context",
				Status:        TaskStatus{State: TaskStateSubmitted, Timestamp: now},
				StatusHistory: []TaskStatus{{State: TaskStateSubmitted, Timestamp: now}},
				CreatedAt:     now, LastModified: now, Version: 1,
			}
			if err := store.CreateTask(context.Background(), task); err != nil {
				t.Fatal(err)
			}
			if err := store.ClaimTaskExecution(
				context.Background(),
				task.ID,
				"expired-fence-message",
				task.Version,
				"expired-fence-claim",
				now,
				now.Add(50*time.Millisecond),
			); err != nil {
				t.Fatal(err)
			}
			if test.databaseClock {
				time.Sleep(100 * time.Millisecond)
			}
			claimed, err := store.GetTask(context.Background(), task.ID)
			if err != nil {
				t.Fatal(err)
			}
			claimed.Status = TaskStatus{
				State: TaskStateWorking, Timestamp: now.Add(20 * time.Millisecond),
			}
			claimed.StatusHistory = append(claimed.StatusHistory, claimed.Status)
			claimed.LastModified = claimed.Status.Timestamp
			expiredContext := withTaskExecutionClaimAt(
				context.Background(),
				task.ID,
				"expired-fence-message",
				"expired-fence-claim",
				now.Add(100*time.Millisecond),
			)
			if err := store.UpdateTask(expiredContext, claimed); !errors.Is(err, ErrTaskBusy) {
				t.Fatalf("expired claim update error=%v, want ErrTaskBusy", err)
			}
			reloaded, err := store.GetTask(context.Background(), task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if reloaded.Status.State != TaskStateSubmitted || reloaded.Version != 1 {
				t.Fatalf(
					"expired claim mutated Task: state=%s version=%d",
					reloaded.Status.State,
					reloaded.Version,
				)
			}
		})
	}
}

func TestGormExecutionClaimUsesDatabaseClockForFencing(t *testing.T) {
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

	// This service clock is intentionally far behind the database clock. The
	// duration is trusted, but the persisted lease origin must come from SQL.
	serviceNow := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	task := Task{
		ID: "database-clock-fence", ContextID: "database-clock-context",
		Status: TaskStatus{
			State: TaskStateSubmitted, Timestamp: serviceNow,
		},
		StatusHistory: []TaskStatus{{
			State: TaskStateSubmitted, Timestamp: serviceNow,
		}},
		CreatedAt: serviceNow, LastModified: serviceNow, Version: 1,
	}
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if err := store.ClaimTaskExecution(
		context.Background(),
		task.ID,
		"database-clock-message",
		task.Version,
		"database-clock-claim",
		serviceNow,
		serviceNow.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	claimed.Status = TaskStatus{
		State: TaskStateWorking, Timestamp: serviceNow.Add(time.Millisecond),
	}
	claimed.StatusHistory = append(claimed.StatusHistory, claimed.Status)
	claimed.LastModified = claimed.Status.Timestamp
	// Gorm must ignore the injected checked-at value and fence against the
	// database's current time.
	ctx := withTaskExecutionClaimAt(
		context.Background(),
		task.ID,
		"database-clock-message",
		"database-clock-claim",
		serviceNow.Add(24*time.Hour),
	)
	if err := store.UpdateTask(ctx, claimed); err != nil {
		t.Fatalf("active database-time claim was rejected: %v", err)
	}
}

func TestGormCancellationStopsRemoteExecutionRenewal(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open(filepath.Join(t.TempDir(), "remote-cancel-claim.db")),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	storeOne := NewGormStoreWithProtector(db, nil)
	if err := storeOne.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	storeTwo := NewGormStoreWithProtector(db, nil)
	started := make(chan struct{})
	canceled := make(chan struct{})
	backend := BackendFuncs{
		ProcessFunc: func(ctx context.Context, _ Task, _ Message, _ Reporter) error {
			close(started)
			<-ctx.Done()
			close(canceled)
			return ctx.Err()
		},
	}
	options := ServiceOptions{
		ExecutionClaimTTL:      100 * time.Millisecond,
		ExecutionRenewInterval: 15 * time.Millisecond,
	}
	serviceOne := NewService(storeOne, backend, options)
	serviceTwo := NewService(storeTwo, backend, options)
	text := "cancel remote durable execution"
	task, err := serviceOne.SendMessage(context.Background(), SendMessageParams{
		Message: Message{
			MessageID: "remote-cancel-message",
			Role:      RoleUser,
			Parts:     []Part{{Text: &text}},
		},
		Configuration: SendMessageConfiguration{ReturnImmediately: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("backend did not start")
	}
	canceledTask, err := serviceTwo.CancelTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if canceledTask.Status.State != TaskStateCanceled {
		t.Fatalf("cancel state=%s", canceledTask.Status.State)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("terminal state did not make remote renewal cancel backend")
	}
	deadline := time.Now().Add(time.Second)
	for {
		reloaded, getErr := storeOne.GetTask(context.Background(), task.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if reloaded.ExecutionClaimID == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("canceled Task retained execution claim %q", reloaded.ExecutionClaimID)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type recordingTransactionalPushDispatcher struct {
	mu     sync.Mutex
	events []StoredEvent
}

func (d *recordingTransactionalPushDispatcher) Enqueue(
	context.Context,
	PushNotificationConfig,
	StoredEvent,
) error {
	return errors.New("non-transactional enqueue must not be used")
}

func (d *recordingTransactionalPushDispatcher) EnqueueTx(
	_ context.Context,
	_ *gorm.DB,
	_ PushNotificationConfig,
	event StoredEvent,
) error {
	d.mu.Lock()
	d.events = append(d.events, event)
	d.mu.Unlock()
	return nil
}

func (d *recordingTransactionalPushDispatcher) versions() []uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := make([]uint64, len(d.events))
	for i := range d.events {
		result[i] = d.events[i].ResourceVersion
	}
	return result
}

func TestGormPushEventsCarryCommittedTaskResourceVersion(t *testing.T) {
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
	dispatcher := &recordingTransactionalPushDispatcher{}
	service := NewService(store, BackendFuncs{
		ProcessFunc: func(ctx context.Context, _ Task, _ Message, reporter Reporter) error {
			return reporter.AddArtifact(ctx, Artifact{
				ArtifactID: "versioned-artifact",
				Parts:      []Part{{Data: json.RawMessage(`{"ok":true}`)}},
			}, false, true, nil)
		},
	}, ServiceOptions{PushDispatcher: dispatcher})
	text := "version every event"
	task, err := service.SendMessage(context.Background(), SendMessageParams{
		Message: Message{
			MessageID: "resource-version-message",
			Role:      RoleUser,
			Parts:     []Part{{Text: &text}},
		},
		Configuration: SendMessageConfiguration{
			TaskPushNotification: &PushNotificationConfig{
				URL: "https://hooks.example.com/a2a",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.Version != 4 {
		t.Fatalf("completed Task version=%d, want 4", task.Version)
	}
	got := dispatcher.versions()
	want := []uint64{1, 2, 3, 4}
	if len(got) != len(want) {
		t.Fatalf("push event versions=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("push event versions=%v, want %v", got, want)
		}
	}
}

func TestPushConfigurationRequiresDispatcher(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store, BackendFuncs{}, ServiceOptions{})
	text := "push requires dispatcher"
	_, _, err := service.StartMessageOnce(context.Background(), SendMessageParams{
		Message: Message{
			MessageID: "missing-push-dispatcher",
			Role:      RoleUser,
			Parts:     []Part{{Text: &text}},
		},
		Configuration: SendMessageConfiguration{
			TaskPushNotification: &PushNotificationConfig{
				URL: "https://hooks.example.com/a2a",
			},
		},
	})
	if !errors.Is(err, ErrPushUnavailable) {
		t.Fatalf("push without dispatcher error=%v", err)
	}
	list, listErr := service.ListTasks(context.Background(), ListTasksParams{PageSize: 10})
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(list.Tasks) != 0 {
		t.Fatalf("push misconfiguration persisted Task: %#v", list.Tasks)
	}

	now := time.Now().UTC()
	task := Task{
		ID: "preconfigured-push-task", ContextID: "preconfigured-push-context",
		Status:        TaskStatus{State: TaskStateSubmitted, Timestamp: now},
		StatusHistory: []TaskStatus{{State: TaskStateSubmitted, Timestamp: now}},
		CreatedAt:     now, LastModified: now, Version: 1,
	}
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreatePushConfig(context.Background(), PushNotificationConfig{
		TaskID: task.ID,
		URL:    "https://hooks.example.com/a2a",
	}); !errors.Is(err, ErrPushUnavailable) {
		t.Fatalf("CreatePushConfig without dispatcher error=%v", err)
	}
	if err := store.CreatePushConfig(context.Background(), PushNotificationConfig{
		ID: "preconfigured-push", TaskID: task.ID,
		URL: "https://hooks.example.com/a2a", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CancelTask(context.Background(), task.ID); !errors.Is(err, ErrPushUnavailable) {
		t.Fatalf("mutation with stored push and no dispatcher error=%v", err)
	}
	reloaded, err := store.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status.State != TaskStateSubmitted {
		t.Fatalf("push misconfiguration partially mutated Task to %s", reloaded.Status.State)
	}
}

type failingTransactionalPushDispatcher struct{}

func (failingTransactionalPushDispatcher) Enqueue(
	context.Context,
	PushNotificationConfig,
	StoredEvent,
) error {
	return errors.New("push enqueue failed")
}

func (failingTransactionalPushDispatcher) EnqueueTx(
	context.Context,
	*gorm.DB,
	PushNotificationConfig,
	StoredEvent,
) error {
	return errors.New("push enqueue failed")
}

func TestGormTaskMutationRollsBackWithEventAndPushFailure(t *testing.T) {
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

	service := NewService(store, BackendFuncs{}, ServiceOptions{
		PushDispatcher: failingTransactionalPushDispatcher{},
	})
	text := "atomic task creation"
	_, _, err = service.StartMessageOnce(context.Background(), SendMessageParams{
		Message: Message{
			MessageID: "atomic-create-message",
			Role:      RoleUser,
			Parts:     []Part{{Text: &text}},
		},
		Configuration: SendMessageConfiguration{
			TaskPushNotification: &PushNotificationConfig{
				URL: "https://hooks.example.com/a2a",
			},
		},
	})
	if err == nil {
		t.Fatal("expected transactional push failure")
	}
	list, err := store.ListTasks(context.Background(), ListTasksParams{PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tasks) != 0 {
		t.Fatalf("Task creation committed without its event/outbox: %#v", list.Tasks)
	}
	var eventCount, pushCount int64
	if err := db.Table("agent_task_events").Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("agent_push_notification_configs").Count(&pushCount).Error; err != nil {
		t.Fatal(err)
	}
	if eventCount != 0 || pushCount != 0 {
		t.Fatalf("partial create persisted events=%d push_configs=%d", eventCount, pushCount)
	}

	now := time.Now().UTC()
	task := Task{
		ID: "atomic-update-task", ContextID: "atomic-update-context",
		Status:    TaskStatus{State: TaskStateSubmitted, Timestamp: now},
		CreatedAt: now, LastModified: now, Version: 1,
	}
	task.StatusHistory = []TaskStatus{task.Status}
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	config := PushNotificationConfig{
		ID: "atomic-update-push", TaskID: task.ID,
		URL: "https://hooks.example.com/a2a", CreatedAt: now,
	}
	if err := store.CreatePushConfig(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	task.Status = TaskStatus{State: TaskStateWorking, Timestamp: now.Add(time.Second)}
	task.StatusHistory = append(task.StatusHistory, task.Status)
	task.LastModified = now.Add(time.Second)
	_, err = store.UpdateTaskWithEvent(
		context.Background(),
		task,
		StoredEvent{
			TaskID: task.ID, ContextID: task.ContextID,
			Payload: StreamResponse{StatusUpdate: &TaskStatusUpdateEvent{
				TaskID: task.ID, ContextID: task.ContextID, Status: task.Status,
			}},
			CreatedAt: task.LastModified,
		},
		nil,
		failingTransactionalPushDispatcher{},
	)
	if err == nil {
		t.Fatal("expected transactional update push failure")
	}
	reloaded, err := store.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status.State != TaskStateSubmitted || reloaded.Version != 1 {
		t.Fatalf("Task mutation escaped rollback: state=%s version=%d", reloaded.Status.State, reloaded.Version)
	}
	events, err := store.EventsAfter(context.Background(), task.ID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("event escaped rollback: %#v", events)
	}
}
