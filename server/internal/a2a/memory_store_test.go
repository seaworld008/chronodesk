package a2a

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestMemoryStoreUsesOpaqueStableTaskCursor(t *testing.T) {
	store := NewMemoryStore()
	base := time.Date(2026, 7, 28, 14, 0, 0, 0, time.UTC)
	for i, id := range []string{"task-oldest", "task-middle", "task-newest"} {
		createdAt := base.Add(time.Duration(i) * time.Minute)
		if err := store.CreateTask(context.Background(), Task{
			ID:        id,
			ContextID: "context",
			Status: TaskStatus{
				State:     TaskStateSubmitted,
				Timestamp: createdAt,
			},
			CreatedAt:    createdAt,
			LastModified: createdAt,
			Version:      1,
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	first, err := store.ListTasks(context.Background(), ListTasksParams{PageSize: 2})
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if first.TotalSize != 3 || len(first.Tasks) != 2 || first.NextPageToken == "" {
		t.Fatalf("unexpected first page: %#v", first)
	}
	if first.Tasks[0].ID != "task-newest" || first.Tasks[1].ID != "task-middle" {
		t.Fatalf("unexpected first-page order: %#v", first.Tasks)
	}

	// A newer insertion must not shift the second page or duplicate an item.
	newTime := base.Add(10 * time.Minute)
	if err := store.CreateTask(context.Background(), Task{
		ID:        "task-added-later",
		ContextID: "context",
		Status: TaskStatus{
			State:     TaskStateSubmitted,
			Timestamp: newTime,
		},
		CreatedAt:    newTime,
		LastModified: newTime,
		Version:      1,
	}); err != nil {
		t.Fatalf("create later task: %v", err)
	}
	second, err := store.ListTasks(context.Background(), ListTasksParams{
		PageSize:  2,
		PageToken: first.NextPageToken,
	})
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if len(second.Tasks) != 1 || second.Tasks[0].ID != "task-oldest" {
		t.Fatalf("cursor pagination shifted after insertion: %#v", second.Tasks)
	}
	if second.NextPageToken != "" {
		t.Fatalf("expected final page token to be empty: %q", second.NextPageToken)
	}

	_, err = store.ListTasks(context.Background(), ListTasksParams{PageToken: "not-a-valid-token"})
	if !errors.Is(err, ErrInvalidPageToken) {
		t.Fatalf("expected invalid page token error, got %v", err)
	}
}

func TestServiceReplayReadsEveryDurableEventBatch(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, 7, 28, 15, 0, 0, 0, time.UTC)
	task := Task{
		ID:        "large-replay-task",
		ContextID: "large-replay-context",
		Status: TaskStatus{
			State:     TaskStateWorking,
			Timestamp: now,
		},
		CreatedAt:    now,
		LastModified: now,
		Version:      1,
	}
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("create replay task: %v", err)
	}
	for i := 0; i < 1005; i++ {
		eventTask := task.Clone()
		if _, err := store.AppendEvent(context.Background(), StoredEvent{
			TaskID:    task.ID,
			ContextID: task.ContextID,
			Payload:   StreamResponse{Task: &eventTask},
			CreatedAt: now.Add(time.Duration(i) * time.Millisecond),
		}); err != nil {
			t.Fatalf("append event %d: %v", i, err)
		}
	}
	service := NewService(store, BackendFuncs{}, ServiceOptions{})
	events, err := service.Replay(context.Background(), task.ID, "")
	if err != nil {
		t.Fatalf("replay events: %v", err)
	}
	if len(events) != 1005 {
		t.Fatalf("expected all replay batches, got %d events", len(events))
	}
}

func TestBrokerClosesSlowSubscriberSoClientCanReplay(t *testing.T) {
	broker := NewBroker()
	events, unsubscribe := broker.Subscribe("task")
	defer unsubscribe()
	for i := 0; i < 65; i++ {
		broker.Publish(StoredEvent{TaskID: "task", Cursor: encodeEventCursor(uint64(i + 1))})
	}
	received := 0
	for range events {
		received++
	}
	if received != 64 {
		t.Fatalf("expected buffered events before forced reconnect, got %d", received)
	}

	reconnected, cancel := broker.Subscribe("task")
	defer cancel()
	broker.Publish(StoredEvent{TaskID: "task", Cursor: encodeEventCursor(66)})
	select {
	case event := <-reconnected:
		if event.Cursor != encodeEventCursor(66) {
			t.Fatalf("unexpected reconnected cursor: %q", event.Cursor)
		}
	case <-time.After(time.Second):
		t.Fatal("reconnected subscriber did not receive events")
	}
}

func TestEventCursorCannotBeReusedAcrossTasks(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now().UTC()
	for _, id := range []string{"task-a", "task-b"} {
		if err := store.CreateTask(context.Background(), Task{
			ID:        id,
			ContextID: "context-" + id,
			Status: TaskStatus{
				State:     TaskStateSubmitted,
				Timestamp: now,
			},
			CreatedAt:    now,
			LastModified: now,
			Version:      1,
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	first, err := store.AppendEvent(context.Background(), StoredEvent{TaskID: "task-a"})
	if err != nil {
		t.Fatalf("append event: %v", err)
	}
	if _, err := store.EventsAfter(context.Background(), "task-b", first.Cursor, 10); !errors.Is(err, ErrInvalidEventCursor) {
		t.Fatalf("expected cross-task cursor rejection, got %v", err)
	}
}

func TestPushDispatcherReceivesDurableEventsAfterConfiguration(t *testing.T) {
	dispatcher := &recordingPushDispatcher{}
	store := NewMemoryStore()
	service := NewService(store, BackendFuncs{}, ServiceOptions{
		PushDispatcher: dispatcher,
	})
	text := "needs backend input"
	task, err := service.SendMessage(context.Background(), SendMessageParams{
		Message: Message{
			MessageID: "push-dispatch-message",
			Role:      RoleUser,
			Parts:     []Part{{Text: &text}},
		},
		Configuration: SendMessageConfiguration{
			TaskPushNotification: &PushNotificationConfig{
				URL:   "https://hooks.example.com/a2a",
				Token: "correlation-secret",
				Authentication: &AuthenticationInfo{
					Scheme:      "Bearer",
					Credentials: "delivery-secret",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("send message with push config: %v", err)
	}
	if task.Status.State != TaskStateInputRequired {
		t.Fatalf("expected no-op backend to request input, got %s", task.Status.State)
	}
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if len(dispatcher.events) != 3 {
		t.Fatalf("expected submitted, working, and input-required deliveries, got %d", len(dispatcher.events))
	}
	if dispatcher.configs[0].Authentication == nil ||
		dispatcher.configs[0].Authentication.Credentials != "delivery-secret" {
		t.Fatal("Outbox adapter did not receive callback authentication")
	}
	if dispatcher.events[2].Payload.StatusUpdate == nil ||
		dispatcher.events[2].Payload.StatusUpdate.Status.State != TaskStateInputRequired {
		t.Fatalf("unexpected final push event: %#v", dispatcher.events[2])
	}
}

type recordingPushDispatcher struct {
	mu      sync.Mutex
	configs []PushNotificationConfig
	events  []StoredEvent
}

func (d *recordingPushDispatcher) Enqueue(_ context.Context, config PushNotificationConfig, event StoredEvent) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.configs = append(d.configs, clonePushConfig(config))
	d.events = append(d.events, event)
	return nil
}
