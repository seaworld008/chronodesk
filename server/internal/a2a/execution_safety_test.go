package a2a

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type denyAllTaskAuthorizer struct{}

func (denyAllTaskAuthorizer) AuthorizeTaskSnapshot(context.Context, Task) (bool, error) {
	return false, nil
}

func (denyAllTaskAuthorizer) PrepareTaskList(
	context.Context,
	ListTasksParams,
) (TaskListAuthorizationBatch, error) {
	return denyAllTaskAuthorizationBatch{}, nil
}

type denyAllTaskAuthorizationBatch struct{}

func (denyAllTaskAuthorizationBatch) Allows(Task) (bool, error) {
	return false, nil
}

func (denyAllTaskAuthorizationBatch) RecordSummary(
	context.Context,
	TaskListAuthorizationSummary,
) error {
	return nil
}

func TestSendMessageAuthorizesBeforeAnyBackendSideEffect(t *testing.T) {
	for _, returnImmediately := range []bool{false, true} {
		t.Run(map[bool]string{false: "synchronous", true: "return immediately"}[returnImmediately], func(t *testing.T) {
			var executions atomic.Int32
			service := NewService(NewMemoryStore(), BackendFuncs{
				ProcessFunc: func(context.Context, Task, Message, Reporter) error {
					executions.Add(1)
					return nil
				},
			}, ServiceOptions{TaskListAuthorizer: denyAllTaskAuthorizer{}})
			text := "must not execute"
			_, err := service.SendMessage(context.Background(), SendMessageParams{
				Message: Message{
					MessageID: "denied-before-execution",
					Role:      RoleUser,
					Parts:     []Part{{Text: &text}},
				},
				Configuration: SendMessageConfiguration{
					ReturnImmediately: returnImmediately,
				},
			})
			if !errors.Is(err, ErrTaskNotFound) {
				t.Fatalf("SendMessage() error = %v, want ErrTaskNotFound", err)
			}
			time.Sleep(20 * time.Millisecond)
			if got := executions.Load(); got != 0 {
				t.Fatalf("authorization denial still executed backend %d times", got)
			}
		})
	}
}

func TestBackendPanicIsConvertedToFailedTaskAndReleasesClaim(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store, BackendFuncs{
		ProcessFunc: func(context.Context, Task, Message, Reporter) error {
			panic("customer-secret-must-not-escape")
		},
	}, ServiceOptions{})
	text := "trigger isolated backend"
	task, err := service.SendMessage(context.Background(), SendMessageParams{
		Message: Message{
			MessageID: "panic-isolation",
			Role:      RoleUser,
			Parts:     []Part{{Text: &text}},
		},
	})
	if err != nil {
		t.Fatalf("SendMessage() returned protocol error: %v", err)
	}
	if task.Status.State != TaskStateFailed {
		t.Fatalf("task state = %q, want failed", task.Status.State)
	}
	if task.ExecutionClaimID != "" || task.ExecutionExpiresAt != nil {
		t.Fatalf("backend panic left execution claim: %+v", task)
	}
	for _, status := range task.StatusHistory {
		if status.Message == nil {
			continue
		}
		for _, part := range status.Message.Parts {
			if part.Text != nil && *part.Text == "customer-secret-must-not-escape" {
				t.Fatal("backend panic value leaked into task history")
			}
		}
	}

	secondText := "server remains alive"
	second, err := service.SendMessage(context.Background(), SendMessageParams{
		Message: Message{
			MessageID: "after-panic",
			Role:      RoleUser,
			Parts:     []Part{{Text: &secondText}},
		},
	})
	if err != nil || second.Status.State != TaskStateFailed {
		t.Fatalf("service did not survive backend panic: state=%q error=%v", second.Status.State, err)
	}
}
