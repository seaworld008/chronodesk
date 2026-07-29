package a2a

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"mime"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

var (
	ErrTaskNotCancelable              = errors.New("a2a task is not cancelable")
	ErrUnsupported                    = errors.New("a2a operation is not supported for the task")
	ErrInvalidTransition              = errors.New("invalid a2a task state transition")
	ErrTaskBusy                       = errors.New("a2a task is already being processed")
	ErrPushUnavailable                = errors.New("a2a push dispatcher is unavailable")
	ErrExecutionDeferred              = errors.New("a2a execution is deferred")
	ErrExtendedAgentCardNotConfigured = errors.New("a2a extended agent card is not configured")
	ErrContentTypeNotSupported        = errors.New("a2a content type is not supported")
)

const DefaultExecutionClaimTTL = 30 * time.Second

// Backend is the only domain execution dependency of the protocol server.
// Implementations bridge skills to ChronoDesk services and report structured
// updates through Reporter. Returning nil means the current turn is complete.
type Backend interface {
	Process(ctx context.Context, task Task, message Message, reporter Reporter) error
	Cancel(ctx context.Context, task Task) error
}

// Reporter is scoped to one Task and cannot mutate a linked Ticket directly.
type Reporter interface {
	SetStatus(ctx context.Context, state TaskState, message *Message, metadata map[string]any) error
	AddArtifact(ctx context.Context, artifact Artifact, appendParts, lastChunk bool, metadata map[string]any) error
}

// BackendFuncs is a convenience adapter for application integration and tests.
type BackendFuncs struct {
	ProcessFunc func(context.Context, Task, Message, Reporter) error
	CancelFunc  func(context.Context, Task) error
}

func (b BackendFuncs) Process(ctx context.Context, task Task, message Message, reporter Reporter) error {
	if b.ProcessFunc == nil {
		return reporter.SetStatus(ctx, TaskStateInputRequired, textMessage("No A2A execution backend is configured."), nil)
	}
	return b.ProcessFunc(ctx, task, message, reporter)
}

func (b BackendFuncs) Cancel(ctx context.Context, task Task) error {
	if b.CancelFunc == nil {
		return nil
	}
	return b.CancelFunc(ctx, task)
}

// PushDispatcher should enqueue delivery into the application's durable
// Outbox. Implementations must re-resolve callback hosts and enforce egress
// policy at delivery time to prevent DNS-rebinding SSRF.
type PushDispatcher interface {
	Enqueue(ctx context.Context, config PushNotificationConfig, event StoredEvent) error
}

// TransactionalPushDispatcher lets a durable Store append its protocol event,
// CloudEvent and every push Outbox delivery in one database transaction.
type TransactionalPushDispatcher interface {
	EnqueueTx(ctx context.Context, tx *gorm.DB, config PushNotificationConfig, event StoredEvent) error
}

type AtomicEventStore interface {
	AppendEventWithPush(
		ctx context.Context,
		event StoredEvent,
		dispatcher TransactionalPushDispatcher,
	) (StoredEvent, error)
}

// AtomicTaskEventStore persists a Task mutation, its protocol event, and every
// durable push delivery in one transaction. Durable stores must implement this
// boundary so a process crash cannot expose a Task state without its replay
// event or leave an event referring to a rolled-back Task state.
type AtomicTaskEventStore interface {
	CreateTaskWithEvent(
		ctx context.Context,
		task Task,
		event StoredEvent,
		pushConfig *PushNotificationConfig,
		dispatcher TransactionalPushDispatcher,
	) (StoredEvent, error)
	UpdateTaskWithEvent(
		ctx context.Context,
		task Task,
		event StoredEvent,
		pushConfig *PushNotificationConfig,
		dispatcher TransactionalPushDispatcher,
	) (StoredEvent, error)
}

type TaskListAuthorizationSummary struct {
	CandidateBudget   int
	CandidatesScanned int
	ItemsReturned     int
	ItemsFiltered     int
	HasMore           bool
	CursorSemantics   string
}

// TaskListAuthorizationBatch evaluates candidates from one immutable policy
// snapshot. Implementations must not query policies or persist a decision once
// per Task.
type TaskListAuthorizationBatch interface {
	Allows(Task) (bool, error)
	RecordSummary(context.Context, TaskListAuthorizationSummary) error
}

type TaskListAuthorizer interface {
	AuthorizeTaskSnapshot(
		context.Context,
		Task,
	) (bool, error)
	PrepareTaskList(
		context.Context,
		ListTasksParams,
	) (TaskListAuthorizationBatch, error)
}

type ServiceOptions struct {
	Broker                 *Broker
	PushDispatcher         PushDispatcher
	TaskListAuthorizer     TaskListAuthorizer
	Now                    func() time.Time
	NewID                  func() string
	BackgroundContext      context.Context
	ExecutionClaimTTL      time.Duration
	ExecutionRenewInterval time.Duration
	acceptedInputModes     []string
	acceptedOutputModes    []string
}

type Service struct {
	store                  Store
	backend                Backend
	broker                 *Broker
	pushDispatcher         PushDispatcher
	taskListAuthorizer     TaskListAuthorizer
	now                    func() time.Time
	newID                  func() string
	background             context.Context
	executionClaimTTL      time.Duration
	executionRenewInterval time.Duration
	acceptedInputModes     map[string]struct{}
	acceptedOutputModes    map[string]struct{}
	taskLocks              sync.Map
	executionsMu           sync.Mutex
	executions             map[string]*taskExecution
}

type taskExecution struct {
	cancel context.CancelFunc
}

func NewService(store Store, backend Backend, opts ServiceOptions) *Service {
	if store == nil {
		store = NewMemoryStore()
	}
	if backend == nil {
		backend = BackendFuncs{}
	}
	if opts.Broker == nil {
		opts.Broker = NewBroker()
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.NewID == nil {
		opts.NewID = randomID
	}
	if opts.BackgroundContext == nil {
		opts.BackgroundContext = context.Background()
	}
	if opts.ExecutionClaimTTL <= 0 {
		opts.ExecutionClaimTTL = DefaultExecutionClaimTTL
	}
	if opts.ExecutionRenewInterval <= 0 ||
		opts.ExecutionRenewInterval >= opts.ExecutionClaimTTL {
		opts.ExecutionRenewInterval = opts.ExecutionClaimTTL / 3
	}
	if opts.ExecutionRenewInterval <= 0 {
		opts.ExecutionRenewInterval = time.Second
	}
	return &Service{
		store:                  store,
		backend:                backend,
		broker:                 opts.Broker,
		pushDispatcher:         opts.PushDispatcher,
		taskListAuthorizer:     opts.TaskListAuthorizer,
		now:                    opts.Now,
		newID:                  opts.NewID,
		background:             opts.BackgroundContext,
		executionClaimTTL:      opts.ExecutionClaimTTL,
		executionRenewInterval: opts.ExecutionRenewInterval,
		acceptedInputModes:     normalizeMediaTypes(opts.acceptedInputModes),
		acceptedOutputModes:    normalizeMediaTypes(opts.acceptedOutputModes),
		executions:             make(map[string]*taskExecution),
	}
}

func (s *Service) StartMessage(ctx context.Context, params SendMessageParams) (Task, error) {
	task, _, err := s.StartMessageOnce(ctx, params)
	return task, err
}

// StartMessageOnce makes messageId the durable A2A idempotency key. A retry
// from the same task owner returns the original Task and must not execute the
// backend a second time.
func (s *Service) StartMessageOnce(ctx context.Context, params SendMessageParams) (Task, bool, error) {
	params.Message.MessageID = strings.TrimSpace(params.Message.MessageID)
	params.Message.TaskID = strings.TrimSpace(params.Message.TaskID)
	params.Message.ContextID = strings.TrimSpace(params.Message.ContextID)
	if err := params.Message.ValidateInbound(); err != nil {
		return Task{}, false, fmt.Errorf("invalid message: %w", err)
	}
	if err := s.validateMediaTypes(params); err != nil {
		return Task{}, false, err
	}
	if params.Configuration.HistoryLength != nil && *params.Configuration.HistoryLength < 0 {
		return Task{}, false, errors.New("historyLength must be non-negative")
	}
	if params.Configuration.TaskPushNotification != nil {
		if s.pushDispatcher == nil {
			return Task{}, false, ErrPushUnavailable
		}
		if err := validatePushConfig(*params.Configuration.TaskPushNotification); err != nil {
			return Task{}, false, err
		}
	}
	requestDigest, err := sendMessageRequestDigest(params)
	if err != nil {
		return Task{}, false, fmt.Errorf("digest A2A message request: %w", err)
	}

	message := params.Message
	message.RequestDigest = requestDigest
	existing, findErr := s.store.FindTaskByMessageID(ctx, message.MessageID)
	if findErr == nil {
		if err := validateMessageReplay(existing, message, requestDigest); err != nil {
			return Task{}, false, err
		}
		if err := s.authorizeTaskSnapshot(ctx, existing); err != nil {
			return Task{}, false, err
		}
		return limitTaskHistory(existing, params.Configuration.HistoryLength), true, nil
	}
	if !errors.Is(findErr, ErrTaskNotFound) {
		return Task{}, false, findErr
	}
	var task Task
	err = nil
	if message.TaskID == "" {
		task, err = s.createTask(ctx, params, message)
	} else {
		task, err = s.continueTask(ctx, params, message)
	}
	if err != nil {
		if errors.Is(err, ErrTaskConflict) {
			if existing, findErr := s.store.FindTaskByMessageID(ctx, message.MessageID); findErr == nil {
				if replayErr := validateMessageReplay(existing, message, requestDigest); replayErr != nil {
					return Task{}, false, replayErr
				}
				if authorizeErr := s.authorizeTaskSnapshot(ctx, existing); authorizeErr != nil {
					return Task{}, false, authorizeErr
				}
				return limitTaskHistory(existing, params.Configuration.HistoryLength), true, nil
			}
		}
		return Task{}, false, err
	}
	return task, false, nil
}

type contentTypeNotSupportedError struct {
	mediaType string
}

func (e *contentTypeNotSupportedError) Error() string {
	if e == nil || e.mediaType == "" {
		return ErrContentTypeNotSupported.Error()
	}
	return fmt.Sprintf("%s: %s", ErrContentTypeNotSupported, e.mediaType)
}

func (e *contentTypeNotSupportedError) Unwrap() error {
	return ErrContentTypeNotSupported
}

func (s *Service) validateMediaTypes(params SendMessageParams) error {
	if len(s.acceptedInputModes) > 0 {
		for _, part := range params.Message.Parts {
			mediaType, err := partMediaType(part)
			if err != nil {
				return err
			}
			if _, supported := s.acceptedInputModes[mediaType]; !supported {
				return &contentTypeNotSupportedError{mediaType: mediaType}
			}
		}
	}
	if len(params.Configuration.AcceptedOutputModes) == 0 ||
		len(s.acceptedOutputModes) == 0 {
		return nil
	}
	for _, requested := range params.Configuration.AcceptedOutputModes {
		mediaType, err := canonicalMediaType(requested)
		if err != nil {
			return err
		}
		if _, supported := s.acceptedOutputModes[mediaType]; supported {
			return nil
		}
	}
	return &contentTypeNotSupportedError{
		mediaType: strings.Join(params.Configuration.AcceptedOutputModes, ","),
	}
}

func partMediaType(part Part) (string, error) {
	if strings.TrimSpace(part.MediaType) != "" {
		return canonicalMediaType(part.MediaType)
	}
	switch {
	case part.Text != nil:
		return "text/plain", nil
	case len(part.Data) > 0:
		return "application/json", nil
	default:
		return "application/octet-stream", nil
	}
}

func normalizeMediaTypes(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		mediaType, err := canonicalMediaType(value)
		if err == nil {
			result[mediaType] = struct{}{}
		}
	}
	return result
}

func canonicalMediaType(value string) (string, error) {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil || mediaType == "" {
		return "", fmt.Errorf("invalid media type %q", value)
	}
	return strings.ToLower(mediaType), nil
}

func validateMessageReplay(task Task, message Message, requestDigest string) error {
	if message.TaskID != "" && message.TaskID != task.ID {
		return ErrTaskConflict
	}
	if message.ContextID != "" && message.ContextID != task.ContextID {
		return ErrTaskConflict
	}
	for i := range task.History {
		persisted := &task.History[i]
		if persisted.MessageID != message.MessageID {
			continue
		}
		if persisted.RequestDigest == "" || persisted.RequestDigest != requestDigest {
			return ErrTaskConflict
		}
		return nil
	}
	return ErrTaskConflict
}

func (s *Service) SendMessage(ctx context.Context, params SendMessageParams) (Task, error) {
	task, replayed, err := s.StartMessageOnce(ctx, params)
	if err != nil {
		return Task{}, err
	}
	if replayed && !taskNeedsRecovery(task) {
		return task, nil
	}
	executionContext := context.WithoutCancel(ctx)
	messageID := strings.TrimSpace(params.Message.MessageID)
	if params.Configuration.ReturnImmediately {
		go s.execute(executionContext, task.ID, messageID, task.Version)
		if err := s.authorizeTaskSnapshot(ctx, task); err != nil {
			return Task{}, err
		}
		return limitTaskHistory(task, params.Configuration.HistoryLength), nil
	}
	s.execute(executionContext, task.ID, messageID, task.Version)
	task, err = s.store.GetTask(executionContext, task.ID)
	if err != nil {
		return Task{}, err
	}
	if err := s.authorizeTaskSnapshot(ctx, task); err != nil {
		return Task{}, err
	}
	return limitTaskHistory(task, params.Configuration.HistoryLength), nil
}

func taskNeedsRecovery(task Task) bool {
	return task.Status.State == TaskStateSubmitted || task.Status.State == TaskStateWorking
}

func (s *Service) ExecuteAsync(ctx context.Context, task Task, message Message) {
	go s.execute(
		context.WithoutCancel(ctx),
		task.ID,
		strings.TrimSpace(message.MessageID),
		task.Version,
	)
}

func (s *Service) GetTask(ctx context.Context, params GetTaskParams) (Task, error) {
	if strings.TrimSpace(params.ID) == "" {
		return Task{}, errors.New("id is required")
	}
	if params.HistoryLength != nil && *params.HistoryLength < 0 {
		return Task{}, errors.New("historyLength must be non-negative")
	}
	task, err := s.store.GetTask(ctx, params.ID)
	if err != nil {
		return Task{}, err
	}
	if err := s.authorizeTaskSnapshot(ctx, task); err != nil {
		return Task{}, err
	}
	return limitTaskHistory(task, params.HistoryLength), nil
}

func (s *Service) authorizeTaskSnapshot(ctx context.Context, task Task) error {
	if s.taskListAuthorizer == nil {
		return nil
	}
	allowed, err := s.taskListAuthorizer.AuthorizeTaskSnapshot(ctx, task)
	if err != nil {
		return err
	}
	if !allowed {
		// Preserve the protocol's existing non-enumerating contract: a Task
		// whose persisted domain snapshot is no longer readable is
		// indistinguishable from an absent or owner-inaccessible Task.
		return ErrTaskNotFound
	}
	return nil
}

// ResolveA2ATaskID resolves an idempotent message replay within the current
// owner scope so protocol policy can authorize the actual Task resource before
// the replay snapshot is returned.
func (s *Service) ResolveA2ATaskID(
	ctx context.Context,
	messageID string,
) (string, error) {
	task, err := s.store.FindTaskByMessageID(ctx, strings.TrimSpace(messageID))
	if err != nil {
		return "", err
	}
	return task.ID, nil
}

func (s *Service) ListTasks(ctx context.Context, params ListTasksParams) (ListTasksResult, error) {
	if params.HistoryLength != nil && *params.HistoryLength < 0 {
		return ListTasksResult{}, errors.New("historyLength must be non-negative")
	}
	if params.Status != "" {
		params.Status = normalizeTaskState(params.Status)
		if params.Status == TaskStateUnspecified {
			return ListTasksResult{}, errors.New("invalid task status")
		}
	}
	if s.taskListAuthorizer != nil {
		return s.listAuthorizedTasks(ctx, params)
	}
	return s.store.ListTasks(ctx, params)
}

func (s *Service) listAuthorizedTasks(
	ctx context.Context,
	params ListTasksParams,
) (ListTasksResult, error) {
	batch, err := s.taskListAuthorizer.PrepareTaskList(ctx, params)
	if err != nil {
		return ListTasksResult{}, err
	}
	pageSize := normalizePageSize(params.PageSize)
	cursor, err := decodeTaskPageToken(params.PageToken)
	if err != nil {
		return ListTasksResult{}, err
	}

	// A2A 1.0 requires totalSize to be the exact number of authorized matching
	// Tasks before pagination. Scan the owner-scoped candidate set once under
	// one immutable authorization snapshot, then paginate the authorized view.
	// This deliberately favors protocol correctness and non-enumeration over a
	// non-standard "total is approximate" response extension.
	visibleAfterCursor := make([]Task, 0, pageSize+1)
	totalVisible := int64(0)
	scanned := 0
	filtered := 0
	nextRawToken := ""
	for {
		scanParams := params
		scanParams.PageSize = 100
		scanParams.PageToken = nextRawToken
		// Authorization must evaluate the complete persisted snapshot even when
		// the caller asks the response projection to omit history or artifacts.
		scanParams.HistoryLength = nil
		scanParams.IncludeArtifacts = true
		candidates, listErr := s.store.ListTasks(ctx, scanParams)
		if listErr != nil {
			return ListTasksResult{}, listErr
		}
		if len(candidates.Tasks) == 0 {
			break
		}
		for _, task := range candidates.Tasks {
			scanned++
			allowed, allowErr := batch.Allows(task)
			if allowErr != nil {
				return ListTasksResult{}, allowErr
			}
			if !allowed {
				filtered++
				continue
			}
			totalVisible++
			if taskIsAfterCursor(task, cursor) && len(visibleAfterCursor) < pageSize+1 {
				visibleAfterCursor = append(
					visibleAfterCursor,
					projectTask(task, params.HistoryLength, params.IncludeArtifacts),
				)
			}
		}
		if candidates.NextPageToken == "" {
			break
		}
		if candidates.NextPageToken == nextRawToken {
			return ListTasksResult{}, errors.New("task store did not advance list cursor")
		}
		nextRawToken = candidates.NextPageToken
	}

	hasMore := len(visibleAfterCursor) > pageSize
	if hasMore {
		visibleAfterCursor = visibleAfterCursor[:pageSize]
	}
	nextPageToken := ""
	if hasMore && len(visibleAfterCursor) > 0 {
		nextPageToken = encodeTaskPageToken(visibleAfterCursor[len(visibleAfterCursor)-1])
	}
	result := ListTasksResult{
		Tasks:         visibleAfterCursor,
		NextPageToken: nextPageToken,
		PageSize:      pageSize,
		TotalSize:     totalVisible,
	}
	if err := batch.RecordSummary(ctx, TaskListAuthorizationSummary{
		CandidateBudget:   scanned,
		CandidatesScanned: scanned,
		ItemsReturned:     len(visibleAfterCursor),
		ItemsFiltered:     filtered,
		HasMore:           hasMore,
		CursorSemantics:   "last_authorized_candidate",
	}); err != nil {
		return ListTasksResult{}, err
	}
	return result, nil
}

func taskIsAfterCursor(task Task, cursor *taskPageCursor) bool {
	if cursor == nil {
		return true
	}
	return task.Status.Timestamp.Before(cursor.StatusTimestamp) ||
		(task.Status.Timestamp.Equal(cursor.StatusTimestamp) && task.ID > cursor.ID)
}

func (s *Service) CancelTask(ctx context.Context, id string) (Task, error) {
	task, err := s.store.GetTask(ctx, id)
	if err != nil {
		return Task{}, err
	}
	if task.Status.State.IsTerminal() {
		return Task{}, ErrTaskNotCancelable
	}
	if err := s.backend.Cancel(ctx, task.Clone()); err != nil {
		return Task{}, err
	}
	s.executionsMu.Lock()
	if execution := s.executions[id]; execution != nil {
		execution.cancel()
	}
	s.executionsMu.Unlock()
	reporter := &taskReporter{service: s, taskID: id}
	if err := reporter.SetStatus(ctx, TaskStateCanceled, textMessage("Task canceled."), nil); err != nil {
		return Task{}, err
	}
	return s.store.GetTask(ctx, id)
}

func (s *Service) CreatePushConfig(ctx context.Context, config PushNotificationConfig) (PushNotificationConfig, error) {
	if s.pushDispatcher == nil {
		return PushNotificationConfig{}, ErrPushUnavailable
	}
	if strings.TrimSpace(config.TaskID) == "" {
		return PushNotificationConfig{}, errors.New("taskId is required")
	}
	if _, err := s.store.GetTask(ctx, config.TaskID); err != nil {
		return PushNotificationConfig{}, err
	}
	if err := validatePushConfig(config); err != nil {
		return PushNotificationConfig{}, err
	}
	if config.ID == "" {
		config.ID = s.newID()
	}
	config.CreatedAt = s.now()
	if err := s.store.CreatePushConfig(ctx, config); err != nil {
		return PushNotificationConfig{}, err
	}
	return config.Redacted(), nil
}

func (s *Service) GetPushConfig(ctx context.Context, params GetPushConfigParams) (PushNotificationConfig, error) {
	if params.TaskID == "" || params.ID == "" {
		return PushNotificationConfig{}, errors.New("taskId and id are required")
	}
	config, err := s.store.GetPushConfig(ctx, params.TaskID, params.ID)
	if err != nil {
		return PushNotificationConfig{}, err
	}
	return config.Redacted(), nil
}

func (s *Service) ListPushConfigs(ctx context.Context, params ListPushConfigsParams) (ListPushConfigsResult, error) {
	if params.TaskID == "" {
		return ListPushConfigsResult{}, errors.New("taskId is required")
	}
	configs, next, err := s.store.ListPushConfigs(ctx, params.TaskID, params.PageToken, params.PageSize)
	if err != nil {
		return ListPushConfigsResult{}, err
	}
	for i := range configs {
		configs[i] = configs[i].Redacted()
	}
	return ListPushConfigsResult{Configs: configs, NextPageToken: next}, nil
}

func (s *Service) DeletePushConfig(ctx context.Context, params GetPushConfigParams) error {
	if params.TaskID == "" || params.ID == "" {
		return errors.New("taskId and id are required")
	}
	return s.store.DeletePushConfig(ctx, params.TaskID, params.ID)
}

func (s *Service) Replay(ctx context.Context, taskID, cursor string) ([]StoredEvent, error) {
	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	const batchSize = 1000
	var result []StoredEvent
	nextCursor := cursor
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := s.store.EventsAfter(ctx, taskID, nextCursor, batchSize)
		if err != nil {
			return nil, err
		}
		result = append(result, batch...)
		if len(batch) < batchSize {
			break
		}
		advanced := batch[len(batch)-1].Cursor
		if advanced == "" || advanced == nextCursor {
			return nil, errors.New("event store did not advance replay cursor")
		}
		nextCursor = advanced
	}
	for _, event := range result {
		task = taskWithStreamSnapshot(task, event.Payload)
	}
	if err := s.authorizeTaskSnapshot(ctx, task); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) Subscribe(ctx context.Context, taskID string) (<-chan StoredEvent, func(), error) {
	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return nil, nil, err
	}
	if err := s.authorizeTaskSnapshot(ctx, task); err != nil {
		return nil, nil, err
	}
	if task.Status.State.IsTerminal() {
		return nil, nil, ErrUnsupported
	}
	events, unsubscribe := s.broker.Subscribe(taskID)
	return events, unsubscribe, nil
}

func taskWithStreamSnapshot(task Task, response StreamResponse) Task {
	snapshot := task.Clone()
	if response.Task != nil {
		streamTask := response.Task
		if streamTask.LinkedTicketID != nil {
			snapshot.LinkedTicketID = streamTask.LinkedTicketID
		}
		snapshot.Artifacts = append(snapshot.Artifacts, streamTask.Artifacts...)
	}
	if response.ArtifactUpdate != nil {
		snapshot.Artifacts = append(snapshot.Artifacts, response.ArtifactUpdate.Artifact)
	}
	return snapshot
}

func (s *Service) createTask(ctx context.Context, params SendMessageParams, message Message) (Task, error) {
	now := s.now()
	taskID := s.newID()
	linkedTicketID, err := linkedTicketIDFromMetadata(params.Metadata)
	if err != nil {
		return Task{}, err
	}
	contextID := strings.TrimSpace(message.ContextID)
	if contextID == "" {
		contextID = s.newID()
	}
	message.TaskID = taskID
	message.ContextID = contextID
	task := Task{
		ID:             taskID,
		ContextID:      contextID,
		LinkedTicketID: linkedTicketID,
		Status: TaskStatus{
			State:     TaskStateSubmitted,
			Timestamp: now,
		},
		History:      []Message{message},
		Metadata:     cloneMap(params.Metadata),
		CreatedAt:    now,
		LastModified: now,
		Version:      1,
	}
	if owner, ok := TaskOwnerFromContext(ctx); ok {
		task.OwnerActorType = owner.ActorType
		task.OwnerActorID = owner.ActorID
		task.OwnerCredentialID = owner.CredentialID
	}
	task.StatusHistory = []TaskStatus{task.Status}
	pushConfig := s.preparePushConfig(task.ID, params.Configuration.TaskPushNotification)
	if err := s.persistTaskEvent(
		ctx,
		task,
		true,
		StreamResponse{Task: taskPointer(task)},
		pushConfig,
	); err != nil {
		return Task{}, err
	}
	return task.Clone(), nil
}

func (s *Service) continueTask(ctx context.Context, params SendMessageParams, message Message) (Task, error) {
	lock := s.taskLock(message.TaskID)
	lock.Lock()
	defer lock.Unlock()
	task, err := s.store.GetTask(ctx, message.TaskID)
	if err != nil {
		return Task{}, err
	}
	if task.Status.State.IsTerminal() {
		return Task{}, ErrUnsupported
	}
	if !task.Status.State.IsInterrupted() {
		return Task{}, ErrTaskBusy
	}
	if message.ContextID != "" && message.ContextID != task.ContextID {
		return Task{}, errors.New("message.contextId does not match task context")
	}
	for _, existing := range task.History {
		if existing.MessageID == message.MessageID {
			return Task{}, errors.New("message.messageId already exists")
		}
	}
	message.ContextID = task.ContextID
	message.TaskID = task.ID
	task.History = append(task.History, message)
	linkedTicketID, err := linkedTicketIDFromMetadata(params.Metadata)
	if err != nil {
		return Task{}, err
	}
	if task.LinkedTicketID == nil {
		task.LinkedTicketID = linkedTicketID
	} else if linkedTicketID != nil && *linkedTicketID != *task.LinkedTicketID {
		return Task{}, fmt.Errorf("%s cannot be changed", MetadataLinkedTicketID)
	}
	now := s.now()
	task.Status = TaskStatus{State: TaskStateSubmitted, Timestamp: now}
	task.StatusHistory = append(task.StatusHistory, task.Status)
	task.LastModified = now
	eventTask := task.Clone()
	eventTask.Version++
	pushConfig := s.preparePushConfig(task.ID, params.Configuration.TaskPushNotification)
	if err := s.persistTaskEvent(
		ctx,
		task,
		false,
		StreamResponse{Task: taskPointer(eventTask)},
		pushConfig,
	); err != nil {
		return Task{}, err
	}
	return s.store.GetTask(ctx, task.ID)
}

func (s *Service) execute(
	executionContext context.Context,
	taskID string,
	messageID string,
	expectedVersion uint64,
) {
	claimID := s.newID()
	now := s.now()
	if err := s.store.ClaimTaskExecution(
		executionContext,
		taskID,
		messageID,
		expectedVersion,
		claimID,
		now,
		now.Add(s.executionClaimTTL),
	); err != nil {
		return
	}
	claimedContext := withTaskExecutionClaim(executionContext, taskID, messageID, claimID)
	ctx, cancel := context.WithCancel(claimedContext)
	stopBackgroundCancellation := context.AfterFunc(s.background, cancel)
	currentExecution := &taskExecution{cancel: cancel}
	s.executionsMu.Lock()
	if previous := s.executions[taskID]; previous != nil {
		// A new persistent claim is only possible after the prior claim expired.
		// Stop the stale local worker before replacing its process-local handle.
		previous.cancel()
	}
	s.executions[taskID] = currentExecution
	s.executionsMu.Unlock()
	renewDone := make(chan struct{})
	go func() {
		defer close(renewDone)
		s.renewExecutionClaim(ctx, taskID, messageID, claimID, cancel)
	}()
	defer func() {
		stopBackgroundCancellation()
		cancel()
		<-renewDone
		s.executionsMu.Lock()
		if s.executions[taskID] == currentExecution {
			delete(s.executions, taskID)
		}
		s.executionsMu.Unlock()
		_ = s.store.ReleaseTaskExecution(
			context.WithoutCancel(claimedContext),
			taskID,
			messageID,
			claimID,
		)
	}()

	claimedTask, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return
	}
	message, ok := persistedTaskMessage(claimedTask, messageID)
	if !ok {
		return
	}
	owner, hasOwner := TaskOwnerFromContext(claimedContext)
	reporter := &taskReporter{
		service: s, taskID: taskID, messageID: messageID, claimID: claimID,
		owner: owner, hasOwner: hasOwner,
	}
	if err := reporter.SetStatus(ctx, TaskStateWorking, nil, nil); err != nil {
		return
	}
	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return
	}
	err = s.backend.Process(ctx, task.Clone(), message, reporter)
	if ctx.Err() != nil {
		return
	}
	current, getErr := s.store.GetTask(ctx, taskID)
	if getErr != nil || current.Status.State.IsTerminal() || current.Status.State.IsInterrupted() {
		return
	}
	if err != nil {
		if errors.Is(err, ErrExecutionDeferred) {
			return
		}
		_ = reporter.SetStatus(ctx, TaskStateFailed, textMessage("Task processing failed."), map[string]any{
			"reason": "BACKEND_ERROR",
		})
		return
	}
	_ = reporter.SetStatus(ctx, TaskStateCompleted, textMessage("Task completed."), nil)
}

func (s *Service) renewExecutionClaim(
	ctx context.Context,
	taskID string,
	messageID string,
	claimID string,
	cancel context.CancelFunc,
) {
	ticker := time.NewTicker(s.executionRenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := s.now()
			if err := s.store.RenewTaskExecution(
				ctx,
				taskID,
				messageID,
				claimID,
				now,
				now.Add(s.executionClaimTTL),
			); err != nil {
				cancel()
				return
			}
		}
	}
}

func persistedTaskMessage(task Task, messageID string) (Message, bool) {
	for i := range task.History {
		if task.History[i].MessageID == messageID {
			return task.History[i], true
		}
	}
	return Message{}, false
}

func (s *Service) preparePushConfig(
	taskID string,
	requested *PushNotificationConfig,
) *PushNotificationConfig {
	if requested == nil {
		return nil
	}
	config := *requested
	config.TaskID = taskID
	if config.ID == "" {
		config.ID = s.newID()
	}
	config.CreatedAt = s.now()
	return &config
}

func (s *Service) persistTaskEvent(
	ctx context.Context,
	task Task,
	create bool,
	payload StreamResponse,
	pushConfig *PushNotificationConfig,
) error {
	if pushConfig != nil && s.pushDispatcher == nil {
		return ErrPushUnavailable
	}
	if _, atomic := s.store.(AtomicTaskEventStore); !create && s.pushDispatcher == nil && !atomic {
		configs, _, err := s.store.ListPushConfigs(ctx, task.ID, "", 1)
		if err != nil {
			return err
		}
		if len(configs) > 0 {
			return ErrPushUnavailable
		}
	}
	candidate := StoredEvent{
		TaskID:    task.ID,
		ContextID: task.ContextID,
		Payload:   payload,
		CreatedAt: s.now(),
	}
	if atomicStore, ok := s.store.(AtomicTaskEventStore); ok {
		var transactional TransactionalPushDispatcher
		if s.pushDispatcher != nil {
			var supported bool
			transactional, supported = s.pushDispatcher.(TransactionalPushDispatcher)
			if !supported {
				return errors.New("durable A2A store requires a transactional push dispatcher")
			}
		}
		var (
			event StoredEvent
			err   error
		)
		if create {
			event, err = atomicStore.CreateTaskWithEvent(
				ctx,
				task,
				candidate,
				pushConfig,
				transactional,
			)
		} else {
			event, err = atomicStore.UpdateTaskWithEvent(
				ctx,
				task,
				candidate,
				pushConfig,
				transactional,
			)
		}
		if err != nil {
			return err
		}
		s.broker.Publish(event)
		return nil
	}

	var err error
	if create {
		err = s.store.CreateTask(ctx, task)
	} else {
		err = s.store.UpdateTask(ctx, task)
	}
	if err != nil {
		return err
	}
	if pushConfig != nil {
		if err := s.store.CreatePushConfig(ctx, *pushConfig); err != nil {
			return err
		}
	}
	return s.emit(ctx, task.ID, task.ContextID, payload)
}

func (s *Service) emit(ctx context.Context, taskID, contextID string, payload StreamResponse) error {
	candidate := StoredEvent{
		TaskID:    taskID,
		ContextID: contextID,
		Payload:   payload,
		CreatedAt: s.now(),
	}
	var event StoredEvent
	var err error
	if atomicStore, ok := s.store.(AtomicEventStore); ok && s.pushDispatcher != nil {
		transactional, supported := s.pushDispatcher.(TransactionalPushDispatcher)
		if !supported {
			return errors.New("durable A2A store requires a transactional push dispatcher")
		}
		event, err = atomicStore.AppendEventWithPush(ctx, candidate, transactional)
	} else {
		event, err = s.store.AppendEvent(ctx, candidate)
	}
	if err != nil {
		return err
	}
	s.broker.Publish(event)
	if s.pushDispatcher != nil {
		if _, handledAtomically := s.store.(AtomicEventStore); handledAtomically {
			return nil
		}
		configs, _, listErr := s.store.ListPushConfigs(ctx, taskID, "", 100)
		if listErr != nil {
			return listErr
		}
		for _, config := range configs {
			if err := s.pushDispatcher.Enqueue(ctx, config, event); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) taskLock(taskID string) *sync.Mutex {
	value, _ := s.taskLocks.LoadOrStore(taskID, &sync.Mutex{})
	return value.(*sync.Mutex)
}

type taskReporter struct {
	service   *Service
	taskID    string
	messageID string
	claimID   string
	owner     TaskOwner
	hasOwner  bool
}

func (r *taskReporter) SetStatus(ctx context.Context, state TaskState, message *Message, metadata map[string]any) error {
	ctx = r.executionContext(ctx)
	state = normalizeTaskState(state)
	if !state.IsValid() {
		return errors.New("invalid task status")
	}
	lock := r.service.taskLock(r.taskID)
	lock.Lock()
	defer lock.Unlock()
	task, err := r.service.store.GetTask(ctx, r.taskID)
	if err != nil {
		return err
	}
	if !canTransition(task.Status.State, state) {
		return ErrInvalidTransition
	}
	now := r.service.now()
	if message != nil {
		if err := message.normalizeAgent(task.ID, task.ContextID, now, r.service.newID); err != nil {
			return err
		}
		task.History = append(task.History, *message)
	}
	status := TaskStatus{State: state, Message: message, Timestamp: now}
	task.Status = status
	task.StatusHistory = append(task.StatusHistory, status)
	task.LastModified = now
	event := TaskStatusUpdateEvent{
		TaskID:    task.ID,
		ContextID: task.ContextID,
		Status:    status,
		Metadata:  cloneMap(metadata),
	}
	return r.service.persistTaskEvent(
		ctx,
		task,
		false,
		StreamResponse{StatusUpdate: &event},
		nil,
	)
}

func (r *taskReporter) AddArtifact(ctx context.Context, artifact Artifact, appendParts, lastChunk bool, metadata map[string]any) error {
	ctx = r.executionContext(ctx)
	if err := artifact.Validate(); err != nil {
		return err
	}
	lock := r.service.taskLock(r.taskID)
	lock.Lock()
	defer lock.Unlock()
	task, err := r.service.store.GetTask(ctx, r.taskID)
	if err != nil {
		return err
	}
	if task.Status.State.IsTerminal() {
		return ErrInvalidTransition
	}
	found := false
	for i := range task.Artifacts {
		if task.Artifacts[i].ArtifactID != artifact.ArtifactID {
			continue
		}
		found = true
		if appendParts {
			task.Artifacts[i].Parts = append(task.Artifacts[i].Parts, artifact.Parts...)
			if artifact.Name != "" {
				task.Artifacts[i].Name = artifact.Name
			}
			if artifact.Description != "" {
				task.Artifacts[i].Description = artifact.Description
			}
			task.Artifacts[i].Metadata = mergeMap(task.Artifacts[i].Metadata, artifact.Metadata)
		} else {
			task.Artifacts[i] = artifact
		}
		break
	}
	if !found {
		task.Artifacts = append(task.Artifacts, artifact)
	}
	task.LastModified = r.service.now()
	event := TaskArtifactUpdateEvent{
		TaskID:    task.ID,
		ContextID: task.ContextID,
		Artifact:  artifact,
		Append:    appendParts,
		LastChunk: lastChunk,
		Metadata:  cloneMap(metadata),
	}
	return r.service.persistTaskEvent(
		ctx,
		task,
		false,
		StreamResponse{ArtifactUpdate: &event},
		nil,
	)
}

func (r *taskReporter) executionContext(ctx context.Context) context.Context {
	if r.hasOwner {
		ctx = WithTaskOwner(ctx, r.owner)
	}
	if r.claimID == "" || r.messageID == "" {
		return ctx
	}
	return withTaskExecutionClaimAt(
		ctx,
		r.taskID,
		r.messageID,
		r.claimID,
		r.service.now(),
	)
}

func canTransition(current, next TaskState) bool {
	current = normalizeTaskState(current)
	next = normalizeTaskState(next)
	if current == next {
		return !current.IsTerminal()
	}
	switch current {
	case TaskStateSubmitted:
		return next == TaskStateWorking || next == TaskStateInputRequired ||
			next == TaskStateAuthRequired || next == TaskStateCompleted ||
			next == TaskStateFailed || next == TaskStateCanceled || next == TaskStateRejected
	case TaskStateWorking:
		return next == TaskStateInputRequired || next == TaskStateAuthRequired ||
			next == TaskStateCompleted || next == TaskStateFailed ||
			next == TaskStateCanceled || next == TaskStateRejected
	case TaskStateInputRequired, TaskStateAuthRequired:
		return next == TaskStateWorking || next == TaskStateFailed ||
			next == TaskStateCanceled || next == TaskStateRejected
	default:
		return false
	}
}

func limitTaskHistory(task Task, historyLength *int) Task {
	if historyLength == nil {
		return task
	}
	if *historyLength <= 0 {
		task.History = nil
		return task
	}
	if len(task.History) > *historyLength {
		task.History = append([]Message(nil), task.History[len(task.History)-*historyLength:]...)
	}
	return task
}

func textMessage(text string) *Message {
	return &Message{Role: RoleAgent, Parts: []Part{{Text: &text, MediaType: "text/plain"}}}
}

func taskPointer(task Task) *Task {
	copyTask := task.Clone()
	return &copyTask
}

func cloneMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	target := make(map[string]any, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}

func mergeMap(left, right map[string]any) map[string]any {
	result := cloneMap(left)
	if result == nil {
		result = make(map[string]any)
	}
	for key, value := range right {
		result[key] = value
	}
	return result
}

func randomID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}

func linkedTicketIDFromMetadata(metadata map[string]any) (*uint, error) {
	raw, exists := metadata[MetadataLinkedTicketID]
	if !exists || raw == nil {
		return nil, nil
	}
	var value uint64
	var err error
	switch candidate := raw.(type) {
	case json.Number:
		value, err = strconv.ParseUint(candidate.String(), 10, 64)
	case float64:
		if candidate <= 0 || math.Trunc(candidate) != candidate || candidate > float64(^uint(0)) {
			err = errors.New("value must be a positive integer")
		} else {
			value = uint64(candidate)
		}
	case uint:
		value = uint64(candidate)
	case uint8:
		value = uint64(candidate)
	case uint16:
		value = uint64(candidate)
	case uint32:
		value = uint64(candidate)
	case uint64:
		value = candidate
	case int:
		if candidate > 0 {
			value = uint64(candidate)
		} else {
			err = errors.New("value must be a positive integer")
		}
	case int8:
		if candidate > 0 {
			value = uint64(candidate)
		} else {
			err = errors.New("value must be a positive integer")
		}
	case int16:
		if candidate > 0 {
			value = uint64(candidate)
		} else {
			err = errors.New("value must be a positive integer")
		}
	case int32:
		if candidate > 0 {
			value = uint64(candidate)
		} else {
			err = errors.New("value must be a positive integer")
		}
	case int64:
		if candidate > 0 {
			value = uint64(candidate)
		} else {
			err = errors.New("value must be a positive integer")
		}
	default:
		err = errors.New("value must be a positive integer")
	}
	if err != nil || value == 0 || value > uint64(^uint(0)) {
		return nil, fmt.Errorf("%s must be a positive integer", MetadataLinkedTicketID)
	}
	result := uint(value)
	return &result, nil
}

func validatePushConfig(config PushNotificationConfig) error {
	parsed, err := url.Parse(config.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return errors.New("push notification URL must be an absolute HTTPS URL without userinfo")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return errors.New("push notification URL cannot target localhost")
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast()) {
		return errors.New("push notification URL cannot target a private or local address")
	}
	if config.Authentication != nil && strings.TrimSpace(config.Authentication.Scheme) == "" {
		return errors.New("push notification authentication.scheme is required")
	}
	return nil
}
