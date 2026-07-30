package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type noopTestPushDispatcher struct{}

func (noopTestPushDispatcher) Enqueue(
	context.Context,
	PushNotificationConfig,
	StoredEvent,
) error {
	return nil
}

func (noopTestPushDispatcher) EnqueueTx(
	context.Context,
	*gorm.DB,
	PushNotificationConfig,
	StoredEvent,
) error {
	return nil
}

func TestTaskOwnerIsolationAcrossStores(t *testing.T) {
	factories := map[string]func(*testing.T) Store{
		"memory": func(*testing.T) Store {
			return NewMemoryStore()
		},
		"gorm": func(t *testing.T) Store {
			t.Helper()
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
			return store
		},
	}

	for name, factory := range factories {
		t.Run(name, func(t *testing.T) {
			store := factory(t)
			service := NewService(store, BackendFuncs{}, ServiceOptions{
				PushDispatcher: noopTestPushDispatcher{},
			})
			ownerA := WithTaskOwner(a2aTestContext(t), TaskOwner{
				ActorType:    "service_principal",
				ActorID:      "principal-a",
				CredentialID: "credential-a",
			})
			rotatedA := WithTaskOwner(a2aTestContext(t), TaskOwner{
				ActorType:    "service_principal",
				ActorID:      "principal-a",
				CredentialID: "credential-a-rotated",
			})
			ownerB := WithTaskOwner(a2aTestContext(t), TaskOwner{
				ActorType:    "service_principal",
				ActorID:      "principal-b",
				CredentialID: "credential-b",
			})

			now := time.Date(2026, 7, 28, 17, 0, 0, 0, time.UTC)
			task := Task{
				ID:        "owned-task",
				ContextID: "owned-context",
				Status: TaskStatus{
					State:     TaskStateSubmitted,
					Timestamp: now,
				},
				StatusHistory: []TaskStatus{{
					State:     TaskStateSubmitted,
					Timestamp: now,
				}},
				CreatedAt:    now,
				LastModified: now,
				Version:      1,
			}
			if err := store.CreateTask(ownerA, task); err != nil {
				t.Fatalf("create owned task: %v", err)
			}
			created, err := store.GetTask(ownerA, task.ID)
			if err != nil {
				t.Fatalf("get owned task: %v", err)
			}
			if created.OwnerActorType != "service_principal" ||
				created.OwnerActorID != "principal-a" ||
				created.OwnerCredentialID != "credential-a" {
				t.Fatalf("owner snapshot was not persisted: %+v", created)
			}
			wire, err := json.Marshal(created)
			if err != nil {
				t.Fatalf("marshal task: %v", err)
			}
			if string(wire) == "" ||
				strings.Contains(string(wire), "principal-a") ||
				strings.Contains(string(wire), "credential-a") ||
				strings.Contains(string(wire), "ownerActor") {
				t.Fatalf("internal owner fields leaked on the A2A wire: %s", wire)
			}

			event, err := store.AppendEvent(ownerA, StoredEvent{
				TaskID:    task.ID,
				ContextID: task.ContextID,
				Payload:   StreamResponse{Task: taskPointer(created)},
				CreatedAt: now,
			})
			if err != nil {
				t.Fatalf("append owned event: %v", err)
			}
			push := PushNotificationConfig{
				ID:        "owned-push",
				TaskID:    task.ID,
				URL:       "https://hooks.example.com/a2a",
				CreatedAt: now,
			}
			if err := store.CreatePushConfig(ownerA, push); err != nil {
				t.Fatalf("create owned push config: %v", err)
			}

			assertTaskNotFound(t, func() error {
				_, err := store.GetTask(ownerB, task.ID)
				return err
			})
			listed, err := store.ListTasks(ownerB, ListTasksParams{})
			if err != nil {
				t.Fatalf("list other principal tasks: %v", err)
			}
			if listed.TotalSize != 0 || len(listed.Tasks) != 0 {
				t.Fatalf("other principal listed owned task: %#v", listed)
			}
			assertTaskNotFound(t, func() error {
				foreign := created.Clone()
				foreign.LastModified = now.Add(time.Minute)
				return store.UpdateTask(ownerB, foreign)
			})
			assertTaskNotFound(t, func() error {
				_, err := store.AppendEvent(ownerB, StoredEvent{TaskID: task.ID})
				return err
			})
			assertTaskNotFound(t, func() error {
				_, err := store.EventsAfter(ownerB, task.ID, "", 10)
				return err
			})
			assertTaskNotFound(t, func() error {
				return store.CreatePushConfig(ownerB, PushNotificationConfig{
					ID:     "foreign-push",
					TaskID: task.ID,
					URL:    "https://hooks.example.com/foreign",
				})
			})
			assertTaskNotFound(t, func() error {
				_, err := store.GetPushConfig(ownerB, task.ID, push.ID)
				return err
			})
			assertTaskNotFound(t, func() error {
				_, _, err := store.ListPushConfigs(ownerB, task.ID, "", 10)
				return err
			})
			assertTaskNotFound(t, func() error {
				return store.DeletePushConfig(ownerB, task.ID, push.ID)
			})
			assertTaskNotFound(t, func() error {
				_, err := service.Replay(ownerB, task.ID, "")
				return err
			})
			assertTaskNotFound(t, func() error {
				_, _, err := service.Subscribe(ownerB, task.ID)
				return err
			})
			assertTaskNotFound(t, func() error {
				_, err := service.CancelTask(ownerB, task.ID)
				return err
			})
			assertTaskNotFound(t, func() error {
				text := "continue another principal's task"
				_, err := service.StartMessage(ownerB, SendMessageParams{Message: Message{
					MessageID: "foreign-continuation",
					TaskID:    task.ID,
					Role:      RoleUser,
					Parts:     []Part{{Text: &text}},
				}})
				return err
			})

			rotated, err := store.GetTask(rotatedA, task.ID)
			if err != nil {
				t.Fatalf("rotated credential cannot read task: %v", err)
			}
			rotated.LastModified = now.Add(time.Minute)
			if err := store.UpdateTask(rotatedA, rotated); err != nil {
				t.Fatalf("rotated credential cannot update task: %v", err)
			}
			rotated, err = store.GetTask(rotatedA, task.ID)
			if err != nil {
				t.Fatalf("reload after rotated update: %v", err)
			}
			if rotated.OwnerCredentialID != "credential-a" {
				t.Fatalf("credential rotation rewrote immutable owner snapshot: %+v", rotated)
			}
			replayed, err := service.Replay(rotatedA, task.ID, "")
			if err != nil {
				t.Fatalf("rotated credential cannot replay: %v", err)
			}
			if len(replayed) != 1 || replayed[0].Cursor != event.Cursor {
				t.Fatalf("unexpected replay for owner: %#v", replayed)
			}
			live, unsubscribe, err := service.Subscribe(rotatedA, task.ID)
			if err != nil || live == nil || unsubscribe == nil {
				t.Fatalf("rotated credential cannot subscribe: %v", err)
			}
			unsubscribe()
			if _, err := store.GetPushConfig(rotatedA, task.ID, push.ID); err != nil {
				t.Fatalf("rotated credential cannot read push config: %v", err)
			}
			if _, err := service.CancelTask(rotatedA, task.ID); err != nil {
				t.Fatalf("rotated credential cannot cancel task: %v", err)
			}
		})
	}
}

func TestScopedCreateCannotForgeTaskOwner(t *testing.T) {
	ctx := WithTaskOwner(a2aTestContext(t), TaskOwner{
		ActorType:    "service_principal",
		ActorID:      "principal-a",
		CredentialID: "credential-a",
	})
	task := Task{
		ID:             "forged-owner-task",
		ContextID:      "forged-owner-context",
		OwnerActorType: "service_principal",
		OwnerActorID:   "principal-b",
		Status:         TaskStatus{State: TaskStateSubmitted, Timestamp: time.Now().UTC()},
		CreatedAt:      time.Now().UTC(),
		LastModified:   time.Now().UTC(),
		Version:        1,
	}
	if err := NewMemoryStore().CreateTask(ctx, task); !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("expected forged owner rejection, got %v", err)
	}
}

func assertTaskNotFound(t *testing.T, operation func() error) {
	t.Helper()
	if err := operation(); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("expected opaque task-not-found result, got %v", err)
	}
}
