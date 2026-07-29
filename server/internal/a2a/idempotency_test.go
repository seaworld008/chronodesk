package a2a

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestSendMessageRetryReturnsOriginalTaskWithoutReexecution(t *testing.T) {
	var executions atomic.Int32
	service := NewService(NewMemoryStore(), BackendFuncs{
		ProcessFunc: func(context.Context, Task, Message, Reporter) error {
			executions.Add(1)
			return nil
		},
	}, ServiceOptions{})
	text := "structured command"
	params := SendMessageParams{Message: Message{
		MessageID: "idempotent-message",
		Role:      RoleUser,
		Parts:     []Part{{Text: &text}},
	}}

	first, err := service.SendMessage(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.SendMessage(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("retry created a second Task: first=%s second=%s", first.ID, second.ID)
	}
	if executions.Load() != 1 {
		t.Fatalf("backend executions=%d, want 1", executions.Load())
	}
	list, err := service.ListTasks(context.Background(), ListTasksParams{PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tasks) != 1 {
		t.Fatalf("tasks=%d, want 1", len(list.Tasks))
	}
}

func TestMessageIDReplayBindsNormalizedRequestDigest(t *testing.T) {
	var executions atomic.Int32
	service := NewService(NewMemoryStore(), BackendFuncs{
		ProcessFunc: func(context.Context, Task, Message, Reporter) error {
			executions.Add(1)
			return nil
		},
	}, ServiceOptions{})
	text := "original command"
	params := SendMessageParams{
		Message: Message{
			MessageID: "digest-bound-message",
			Role:      RoleUser,
			Parts:     []Part{{Text: &text}},
			Metadata:  map[string]any{"skill": "ticket-query"},
		},
		Metadata: map[string]any{"input": map[string]any{"ticket_id": 42}},
	}
	first, err := service.SendMessage(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}

	historyLength := 0
	responseOnlyRetry := params
	responseOnlyRetry.Configuration.HistoryLength = &historyLength
	responseOnlyRetry.Configuration.ReturnImmediately = true
	responseOnlyRetry.Message.TaskID = first.ID
	responseOnlyRetry.Message.ContextID = first.ContextID
	if _, err := service.SendMessage(context.Background(), responseOnlyRetry); err != nil {
		t.Fatalf("pure response options changed request digest: %v", err)
	}
	wrongContext := responseOnlyRetry
	wrongContext.Message.ContextID = "different-context"
	if _, err := service.SendMessage(context.Background(), wrongContext); !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("mismatched replay context error=%v, want ErrTaskConflict", err)
	}

	changed := "different command"
	mismatched := params
	mismatched.Message.Parts = []Part{{Text: &changed}}
	if _, err := service.SendMessage(context.Background(), mismatched); !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("mismatched replay error=%v, want ErrTaskConflict", err)
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("backend executions=%d, want one", got)
	}
}

func TestExecuteAsyncUsesPersistedOriginalMessage(t *testing.T) {
	received := make(chan string, 1)
	service := NewService(NewMemoryStore(), BackendFuncs{
		ProcessFunc: func(_ context.Context, _ Task, message Message, _ Reporter) error {
			received <- *message.Parts[0].Text
			return nil
		},
	}, ServiceOptions{})
	original := "persisted original"
	params := SendMessageParams{Message: Message{
		MessageID: "persisted-execution-message",
		Role:      RoleUser,
		Parts:     []Part{{Text: &original}},
	}}
	task, replayed, err := service.StartMessageOnce(context.Background(), params)
	if err != nil || replayed {
		t.Fatalf("start message replayed=%v err=%v", replayed, err)
	}

	mutated := "caller-side mutation"
	service.ExecuteAsync(context.Background(), task, Message{
		MessageID: params.Message.MessageID,
		Role:      RoleUser,
		Parts:     []Part{{Text: &mutated}},
	})
	select {
	case got := <-received:
		if got != original {
			t.Fatalf("backend received %q, want persisted %q", got, original)
		}
	case <-time.After(time.Second):
		t.Fatal("backend did not execute")
	}
}

func TestStaleWorkingReplayCannotRestartAfterInputRequired(t *testing.T) {
	var executions atomic.Int32
	started := make(chan struct{})
	requireInput := make(chan struct{})
	service := NewService(NewMemoryStore(), BackendFuncs{
		ProcessFunc: func(ctx context.Context, _ Task, _ Message, reporter Reporter) error {
			if executions.Add(1) == 1 {
				close(started)
			}
			<-requireInput
			return reporter.SetStatus(
				ctx,
				TaskStateInputRequired,
				textMessage("more input required"),
				nil,
			)
		},
	}, ServiceOptions{})
	text := "needs follow-up"
	params := SendMessageParams{
		Message: Message{
			MessageID: "stale-working-message",
			Role:      RoleUser,
			Parts:     []Part{{Text: &text}},
		},
		Configuration: SendMessageConfiguration{ReturnImmediately: true},
	}
	first, err := service.SendMessage(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("backend did not start")
	}
	stale, replayed, err := service.StartMessageOnce(context.Background(), params)
	if err != nil || !replayed || stale.Status.State != TaskStateWorking {
		t.Fatalf("stale replay state=%s replayed=%v err=%v", stale.Status.State, replayed, err)
	}
	close(requireInput)
	deadline := time.Now().Add(time.Second)
	for {
		current, getErr := service.GetTask(context.Background(), GetTaskParams{ID: first.ID})
		if getErr != nil {
			t.Fatal(getErr)
		}
		if current.Status.State == TaskStateInputRequired && current.ExecutionClaimID == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"Task did not settle at input-required: state=%s claim=%q",
				current.Status.State,
				current.ExecutionClaimID,
			)
		}
		time.Sleep(5 * time.Millisecond)
	}

	service.ExecuteAsync(context.Background(), stale, params.Message)
	time.Sleep(30 * time.Millisecond)
	if got := executions.Load(); got != 1 {
		t.Fatalf("stale working replay restarted backend %d times", got)
	}
	current, err := service.GetTask(context.Background(), GetTaskParams{ID: first.ID})
	if err != nil {
		t.Fatal(err)
	}
	if current.Status.State != TaskStateInputRequired {
		t.Fatalf("stale replay changed state to %s", current.Status.State)
	}
}

func TestActiveMessageRetryAttachesWithoutCancelingOrRestartingExecution(t *testing.T) {
	var executions atomic.Int32
	var cancellations atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	service := NewService(NewMemoryStore(), BackendFuncs{
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
	}, ServiceOptions{})
	text := "long-running structured command"
	params := SendMessageParams{
		Message: Message{
			MessageID: "active-idempotent-message",
			Role:      RoleUser,
			Parts:     []Part{{Text: &text}},
		},
		Configuration: SendMessageConfiguration{ReturnImmediately: true},
	}

	first, err := service.SendMessage(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("backend execution did not start")
	}
	second, err := service.SendMessage(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("retry changed Task: first=%s second=%s", first.ID, second.ID)
	}
	time.Sleep(25 * time.Millisecond)
	if got := executions.Load(); got != 1 {
		t.Fatalf("active retry started %d backend executions, want 1", got)
	}
	if got := cancellations.Load(); got != 0 {
		t.Fatalf("active retry canceled the original execution %d times", got)
	}

	close(release)
	deadline := time.Now().Add(time.Second)
	for {
		task, getErr := service.GetTask(context.Background(), GetTaskParams{ID: first.ID})
		if getErr != nil {
			t.Fatal(getErr)
		}
		if task.Status.State == TaskStateCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Task did not complete after original execution was released: %s", task.Status.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("backend executions=%d after completion, want 1", got)
	}
	if got := cancellations.Load(); got != 0 {
		t.Fatalf("original execution cancellations=%d, want 0", got)
	}
}
