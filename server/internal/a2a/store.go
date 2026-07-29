package a2a

import (
	"context"
	"errors"
	"time"
)

var (
	ErrTaskNotFound       = errors.New("a2a task not found")
	ErrTaskConflict       = errors.New("a2a task version conflict")
	ErrInvalidPageToken   = errors.New("invalid page token")
	ErrInvalidEventCursor = errors.New("invalid event cursor")
	ErrPushConfigNotFound = errors.New("a2a push notification config not found")
)

// Store is the persistence boundary for protocol state. Implementations must
// return detached Task values that callers may mutate safely. Deployments that
// expose tasks to multiple principals should wrap this interface with the
// object-level authorization rules carried in context.
type Store interface {
	CreateTask(ctx context.Context, task Task) error
	UpdateTask(ctx context.Context, task Task) error
	GetTask(ctx context.Context, id string) (Task, error)
	FindTaskByMessageID(ctx context.Context, messageID string) (Task, error)
	ListTasks(ctx context.Context, params ListTasksParams) (ListTasksResult, error)
	ClaimTaskExecution(
		ctx context.Context,
		taskID string,
		messageID string,
		expectedVersion uint64,
		claimID string,
		now time.Time,
		expiresAt time.Time,
	) error
	RenewTaskExecution(
		ctx context.Context,
		taskID string,
		messageID string,
		claimID string,
		now time.Time,
		expiresAt time.Time,
	) error
	ReleaseTaskExecution(ctx context.Context, taskID, messageID, claimID string) error

	AppendEvent(ctx context.Context, event StoredEvent) (StoredEvent, error)
	EventsAfter(ctx context.Context, taskID, cursor string, limit int) ([]StoredEvent, error)

	CreatePushConfig(ctx context.Context, config PushNotificationConfig) error
	GetPushConfig(ctx context.Context, taskID, id string) (PushNotificationConfig, error)
	ListPushConfigs(ctx context.Context, taskID, pageToken string, pageSize int) ([]PushNotificationConfig, string, error)
	DeletePushConfig(ctx context.Context, taskID, id string) error
}

type taskExecutionClaimContextKey struct{}

type taskExecutionClaimRef struct {
	TaskID    string
	MessageID string
	ClaimID   string
	CheckedAt time.Time
}

func withTaskExecutionClaim(
	ctx context.Context,
	taskID string,
	messageID string,
	claimID string,
) context.Context {
	return withTaskExecutionClaimAt(
		ctx,
		taskID,
		messageID,
		claimID,
		time.Now().UTC(),
	)
}

func withTaskExecutionClaimAt(
	ctx context.Context,
	taskID string,
	messageID string,
	claimID string,
	checkedAt time.Time,
) context.Context {
	return context.WithValue(ctx, taskExecutionClaimContextKey{}, taskExecutionClaimRef{
		TaskID: taskID, MessageID: messageID, ClaimID: claimID, CheckedAt: checkedAt,
	})
}

func taskExecutionClaimFromContext(ctx context.Context) (taskExecutionClaimRef, bool) {
	claim, ok := ctx.Value(taskExecutionClaimContextKey{}).(taskExecutionClaimRef)
	if !ok || claim.TaskID == "" || claim.MessageID == "" || claim.ClaimID == "" {
		return taskExecutionClaimRef{}, false
	}
	if claim.CheckedAt.IsZero() {
		claim.CheckedAt = time.Now().UTC()
	}
	return claim, true
}

func bindTaskOwner(ctx context.Context, task *Task) error {
	owner, scoped := TaskOwnerFromContext(ctx)
	if !scoped {
		return nil
	}
	if task.OwnerActorType != "" &&
		(task.OwnerActorType != owner.ActorType || task.OwnerActorID != owner.ActorID) {
		return ErrTaskConflict
	}
	task.OwnerActorType = owner.ActorType
	task.OwnerActorID = owner.ActorID
	task.OwnerCredentialID = owner.CredentialID
	return nil
}

func taskAccessible(ctx context.Context, task Task) bool {
	owner, scoped := TaskOwnerFromContext(ctx)
	if !scoped {
		return true
	}
	return task.OwnerActorType == owner.ActorType && task.OwnerActorID == owner.ActorID
}
