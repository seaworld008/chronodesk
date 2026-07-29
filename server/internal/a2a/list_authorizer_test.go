package a2a

import (
	"context"
	"errors"
	"testing"
	"time"
)

type recordingTaskListAuthorizer struct {
	denied           map[string]bool
	deniedArtifactID string
	summaries        []TaskListAuthorizationSummary
}

func (a *recordingTaskListAuthorizer) AuthorizeTaskSnapshot(
	_ context.Context,
	task Task,
) (bool, error) {
	for _, artifact := range task.Artifacts {
		if artifact.ArtifactID == a.deniedArtifactID {
			return false, nil
		}
	}
	return !a.denied[task.ID], nil
}

func (a *recordingTaskListAuthorizer) PrepareTaskList(
	context.Context,
	ListTasksParams,
) (TaskListAuthorizationBatch, error) {
	return &recordingTaskListAuthorizationBatch{owner: a}, nil
}

type recordingTaskListAuthorizationBatch struct {
	owner *recordingTaskListAuthorizer
}

func (b *recordingTaskListAuthorizationBatch) Allows(task Task) (bool, error) {
	for _, artifact := range task.Artifacts {
		if artifact.ArtifactID == b.owner.deniedArtifactID {
			return false, nil
		}
	}
	return !b.owner.denied[task.ID], nil
}

func (b *recordingTaskListAuthorizationBatch) RecordSummary(
	_ context.Context,
	summary TaskListAuthorizationSummary,
) error {
	b.owner.summaries = append(b.owner.summaries, summary)
	return nil
}

func TestListTasksFiltersDeniedResourcesWithBoundedCursorProgress(t *testing.T) {
	store := NewMemoryStore()
	base := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	for index, id := range []string{"task-1", "task-2", "task-denied", "task-4"} {
		createdAt := base.Add(time.Duration(index) * time.Minute)
		task := Task{
			ID: id, ContextID: "list-policy-context",
			Status: TaskStatus{
				State: TaskStateSubmitted, Timestamp: createdAt,
			},
			StatusHistory: []TaskStatus{{
				State: TaskStateSubmitted, Timestamp: createdAt,
			}},
			CreatedAt: createdAt, LastModified: createdAt, Version: 1,
		}
		if err := store.CreateTask(context.Background(), task); err != nil {
			t.Fatal(err)
		}
	}
	authorizer := &recordingTaskListAuthorizer{
		denied: map[string]bool{"task-denied": true},
	}
	service := NewService(store, BackendFuncs{}, ServiceOptions{
		TaskListAuthorizer: authorizer,
	})

	first, err := service.ListTasks(context.Background(), ListTasksParams{
		PageSize: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Tasks) != 2 ||
		first.Tasks[0].ID != "task-4" ||
		first.Tasks[1].ID != "task-2" {
		t.Fatalf("first authorized page = %#v", first.Tasks)
	}
	if first.NextPageToken == "" {
		t.Fatal("first authorized page did not expose a forward cursor")
	}
	if first.TotalSize != 3 {
		t.Fatalf(
			"authorized total is not exact: total=%d",
			first.TotalSize,
		)
	}
	if len(authorizer.summaries) != 1 ||
		authorizer.summaries[0].CandidatesScanned != 4 ||
		authorizer.summaries[0].ItemsFiltered != 1 {
		t.Fatalf("first authorization summary = %#v", authorizer.summaries)
	}

	second, err := service.ListTasks(context.Background(), ListTasksParams{
		PageSize:  2,
		PageToken: first.NextPageToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Tasks) != 1 || second.Tasks[0].ID != "task-1" {
		t.Fatalf("second authorized page = %#v", second.Tasks)
	}
	if second.TotalSize != 3 {
		t.Fatalf("second page total=%d, want exact authorized total 3", second.TotalSize)
	}
	if second.NextPageToken != "" {
		t.Fatalf("last page cursor = %q", second.NextPageToken)
	}
	for _, page := range [][]Task{first.Tasks, second.Tasks} {
		for _, task := range page {
			if task.ID == "task-denied" {
				t.Fatal("explicitly denied Task leaked through ListTasks")
			}
		}
	}
	if len(authorizer.summaries) != 2 {
		t.Fatalf("expected one aggregate decision per page: %#v", authorizer.summaries)
	}
}

func TestExistingTaskSnapshotAuthorizationAppliesToReplayAndGetTask(t *testing.T) {
	authorizer := &recordingTaskListAuthorizer{denied: make(map[string]bool)}
	service := NewService(NewMemoryStore(), BackendFuncs{}, ServiceOptions{
		TaskListAuthorizer: authorizer,
	})
	text := "create an authorization-bound snapshot"
	params := SendMessageParams{
		Message: Message{
			MessageID: "snapshot-replay-message",
			Role:      RoleUser,
			Parts:     []Part{{Text: &text}},
		},
	}
	first, err := service.SendMessage(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}

	authorizer.denied[first.ID] = true
	if _, err := service.SendMessage(context.Background(), params); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("denied idempotent replay error = %v, want ErrTaskNotFound", err)
	}
	if _, err := service.GetTask(
		context.Background(),
		GetTaskParams{ID: first.ID},
	); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("denied GetTask error = %v, want ErrTaskNotFound", err)
	}
	if _, err := service.Replay(
		context.Background(),
		first.ID,
		"",
	); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("denied event replay error = %v, want ErrTaskNotFound", err)
	}
	if _, _, err := service.Subscribe(
		context.Background(),
		first.ID,
	); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("denied subscription error = %v, want ErrTaskNotFound", err)
	}
	listed, err := service.ListTasks(context.Background(), ListTasksParams{PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tasks) != 0 {
		t.Fatalf("denied persisted snapshot leaked through ListTasks: %#v", listed.Tasks)
	}
}

func TestListAuthorizationUsesFullSnapshotBeforeResponseProjection(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now().UTC()
	task := Task{
		ID:        "task-projected-authorization",
		ContextID: "context-projected-authorization",
		Status: TaskStatus{
			State:     TaskStateCompleted,
			Timestamp: now,
		},
		Artifacts: []Artifact{{
			ArtifactID: "denied-ticket-snapshot",
			Parts: []Part{{
				Data:      []byte(`{"ticket":{"id":99}}`),
				MediaType: "application/json",
			}},
		}},
		CreatedAt:    now,
		LastModified: now,
		Version:      1,
	}
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	authorizer := &recordingTaskListAuthorizer{
		denied:           make(map[string]bool),
		deniedArtifactID: "denied-ticket-snapshot",
	}
	service := NewService(store, BackendFuncs{}, ServiceOptions{
		TaskListAuthorizer: authorizer,
	})
	listed, err := service.ListTasks(context.Background(), ListTasksParams{
		PageSize:         10,
		IncludeArtifacts: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if listed.TotalSize != 0 || len(listed.Tasks) != 0 {
		t.Fatalf("projected response bypassed snapshot authorization: %#v", listed)
	}
}

func TestReplayAuthorizationIncludesHistoricalArtifactSnapshots(t *testing.T) {
	store := NewMemoryStore()
	task := Task{
		ID:        "task-historical-artifact",
		ContextID: "context-historical-artifact",
		Status: TaskStatus{
			State:     TaskStateCompleted,
			Timestamp: time.Now().UTC(),
		},
		CreatedAt:    time.Now().UTC(),
		LastModified: time.Now().UTC(),
		Version:      1,
	}
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	secretArtifact := Artifact{
		ArtifactID: "historical-ticket-snapshot",
		Parts: []Part{{
			Data:      []byte(`{"result":{"ticket":{"id":81}}}`),
			MediaType: "application/json",
		}},
	}
	if _, err := store.AppendEvent(context.Background(), StoredEvent{
		TaskID:    task.ID,
		ContextID: task.ContextID,
		Payload: StreamResponse{ArtifactUpdate: &TaskArtifactUpdateEvent{
			TaskID:    task.ID,
			ContextID: task.ContextID,
			Artifact:  secretArtifact,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	authorizer := &recordingTaskListAuthorizer{
		denied:           make(map[string]bool),
		deniedArtifactID: secretArtifact.ArtifactID,
	}
	service := NewService(store, BackendFuncs{}, ServiceOptions{
		TaskListAuthorizer: authorizer,
	})
	if _, err := service.Replay(
		context.Background(),
		task.ID,
		"",
	); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("historical Artifact replay error = %v, want ErrTaskNotFound", err)
	}
}
