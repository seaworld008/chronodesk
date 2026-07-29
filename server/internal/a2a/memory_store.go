package a2a

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type MemoryStore struct {
	mu          sync.RWMutex
	tasks       map[string]Task
	events      map[string][]StoredEvent
	pushConfigs map[string]map[string]PushNotificationConfig
	nextEventID uint64
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		tasks:       make(map[string]Task),
		events:      make(map[string][]StoredEvent),
		pushConfigs: make(map[string]map[string]PushNotificationConfig),
	}
}

func (s *MemoryStore) CreateTask(ctx context.Context, task Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := bindTaskOwner(ctx, &task); err != nil {
		return err
	}
	if _, exists := s.tasks[task.ID]; exists {
		return ErrTaskConflict
	}
	for _, existing := range s.tasks {
		for _, message := range existing.History {
			for _, candidate := range task.History {
				if message.MessageID == candidate.MessageID {
					return ErrTaskConflict
				}
			}
		}
	}
	if task.Version == 0 {
		task.Version = 1
	}
	s.tasks[task.ID] = task.Clone()
	return nil
}

func (s *MemoryStore) FindTaskByMessageID(ctx context.Context, messageID string) (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	messageID = strings.TrimSpace(messageID)
	for _, task := range s.tasks {
		if !taskAccessible(ctx, task) {
			continue
		}
		for _, message := range task.History {
			if message.MessageID == messageID {
				return task.Clone(), nil
			}
		}
	}
	return Task{}, ErrTaskNotFound
}

func (s *MemoryStore) UpdateTask(ctx context.Context, task Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.tasks[task.ID]
	if !exists {
		return ErrTaskNotFound
	}
	if !taskAccessible(ctx, current) {
		return ErrTaskNotFound
	}
	if task.OwnerActorType != current.OwnerActorType ||
		task.OwnerActorID != current.OwnerActorID ||
		task.OwnerCredentialID != current.OwnerCredentialID {
		return ErrTaskConflict
	}
	if task.Version != current.Version {
		return ErrTaskConflict
	}
	if claim, ok := taskExecutionClaimFromContext(ctx); ok &&
		(claim.TaskID != current.ID ||
			claim.MessageID != current.ExecutionMessageID ||
			claim.ClaimID != current.ExecutionClaimID ||
			current.ExecutionExpiresAt == nil ||
			!current.ExecutionExpiresAt.After(claim.CheckedAt)) {
		return ErrTaskBusy
	}
	messageIDs := make(map[string]struct{}, len(task.History))
	currentDigests := make(map[string]string, len(current.History))
	for _, message := range current.History {
		currentDigests[message.MessageID] = message.RequestDigest
	}
	for _, message := range task.History {
		if _, duplicate := messageIDs[message.MessageID]; duplicate {
			return ErrTaskConflict
		}
		if digest, existed := currentDigests[message.MessageID]; existed &&
			digest != message.RequestDigest {
			return ErrTaskConflict
		}
		messageIDs[message.MessageID] = struct{}{}
	}
	for id, other := range s.tasks {
		if id == task.ID {
			continue
		}
		for _, message := range other.History {
			if _, conflict := messageIDs[message.MessageID]; conflict {
				return ErrTaskConflict
			}
		}
	}
	task.Version++
	task.ExecutionClaimID = current.ExecutionClaimID
	task.ExecutionMessageID = current.ExecutionMessageID
	task.ExecutionExpiresAt = current.ExecutionExpiresAt
	s.tasks[task.ID] = task.Clone()
	return nil
}

func (s *MemoryStore) ClaimTaskExecution(
	ctx context.Context,
	taskID string,
	messageID string,
	expectedVersion uint64,
	claimID string,
	now time.Time,
	expiresAt time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, exists := s.tasks[taskID]
	if !exists || !taskAccessible(ctx, task) {
		return ErrTaskNotFound
	}
	if task.Version != expectedVersion {
		return ErrTaskConflict
	}
	state := normalizeTaskState(task.Status.State)
	if state != TaskStateSubmitted && state != TaskStateWorking {
		return ErrTaskBusy
	}
	if task.ExecutionClaimID != "" &&
		task.ExecutionExpiresAt != nil &&
		task.ExecutionExpiresAt.After(now) {
		return ErrTaskBusy
	}
	task.ExecutionClaimID = claimID
	task.ExecutionMessageID = messageID
	task.ExecutionExpiresAt = timePointer(expiresAt)
	s.tasks[taskID] = task.Clone()
	return nil
}

func (s *MemoryStore) RenewTaskExecution(
	ctx context.Context,
	taskID string,
	messageID string,
	claimID string,
	now time.Time,
	expiresAt time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, exists := s.tasks[taskID]
	if !exists || !taskAccessible(ctx, task) {
		return ErrTaskNotFound
	}
	state := normalizeTaskState(task.Status.State)
	if task.ExecutionClaimID != claimID ||
		task.ExecutionMessageID != messageID ||
		task.ExecutionExpiresAt == nil ||
		!task.ExecutionExpiresAt.After(now) ||
		(state != TaskStateSubmitted && state != TaskStateWorking) {
		return ErrTaskBusy
	}
	task.ExecutionExpiresAt = timePointer(expiresAt)
	s.tasks[taskID] = task.Clone()
	return nil
}

func (s *MemoryStore) ReleaseTaskExecution(
	ctx context.Context,
	taskID string,
	messageID string,
	claimID string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, exists := s.tasks[taskID]
	if !exists || !taskAccessible(ctx, task) {
		return ErrTaskNotFound
	}
	if task.ExecutionClaimID != claimID || task.ExecutionMessageID != messageID {
		return ErrTaskBusy
	}
	task.ExecutionClaimID = ""
	task.ExecutionMessageID = ""
	task.ExecutionExpiresAt = nil
	s.tasks[taskID] = task.Clone()
	return nil
}

func timePointer(value time.Time) *time.Time {
	copyValue := value
	return &copyValue
}

func (s *MemoryStore) GetTask(ctx context.Context, id string) (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, exists := s.tasks[id]
	if !exists {
		return Task{}, ErrTaskNotFound
	}
	if !taskAccessible(ctx, task) {
		return Task{}, ErrTaskNotFound
	}
	return task.Clone(), nil
}

func (s *MemoryStore) ListTasks(ctx context.Context, params ListTasksParams) (ListTasksResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pageSize := normalizePageSize(params.PageSize)
	cursor, err := decodeTaskPageToken(params.PageToken)
	if err != nil {
		return ListTasksResult{}, err
	}
	status := normalizeTaskState(params.Status)
	tasks := make([]Task, 0, len(s.tasks))
	for _, task := range s.tasks {
		if !taskAccessible(ctx, task) {
			continue
		}
		if params.ContextID != "" && task.ContextID != params.ContextID {
			continue
		}
		if params.Status != "" && status != TaskStateUnspecified && task.Status.State != status {
			continue
		}
		if params.StatusTimestampAfter != nil && task.Status.Timestamp.Before(*params.StatusTimestampAfter) {
			continue
		}
		tasks = append(tasks, task.Clone())
	}
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].Status.Timestamp.Equal(tasks[j].Status.Timestamp) {
			return tasks[i].ID < tasks[j].ID
		}
		return tasks[i].Status.Timestamp.After(tasks[j].Status.Timestamp)
	})
	total := len(tasks)
	if cursor != nil {
		filtered := tasks[:0]
		for _, task := range tasks {
			if task.Status.Timestamp.Before(cursor.StatusTimestamp) ||
				(task.Status.Timestamp.Equal(cursor.StatusTimestamp) && task.ID > cursor.ID) {
				filtered = append(filtered, task)
			}
		}
		tasks = filtered
	}
	end := pageSize
	if end > len(tasks) {
		end = len(tasks)
	}
	page := tasks[:end]
	for i := range page {
		page[i] = projectTask(page[i], params.HistoryLength, params.IncludeArtifacts)
	}
	next := ""
	if end < len(tasks) && len(page) > 0 {
		next = encodeTaskPageToken(page[len(page)-1])
	}
	return ListTasksResult{
		Tasks:         page,
		NextPageToken: next,
		PageSize:      pageSize,
		TotalSize:     int64(total),
	}, nil
}

func (s *MemoryStore) AppendEvent(ctx context.Context, event StoredEvent) (StoredEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, exists := s.tasks[event.TaskID]
	if !exists || !taskAccessible(ctx, task) {
		return StoredEvent{}, ErrTaskNotFound
	}
	s.nextEventID++
	event.Cursor = encodeEventCursor(s.nextEventID)
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.ResourceVersion == 0 {
		event.ResourceVersion = task.Version
	}
	event.Payload = cloneStreamResponse(event.Payload)
	s.events[event.TaskID] = append(s.events[event.TaskID], event)
	return event, nil
}

func (s *MemoryStore) EventsAfter(ctx context.Context, taskID, cursor string, limit int) ([]StoredEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, exists := s.tasks[taskID]
	if !exists || !taskAccessible(ctx, task) {
		return nil, ErrTaskNotFound
	}
	after, err := decodeEventCursor(cursor)
	if err != nil {
		return nil, err
	}
	if after > 0 {
		cursorBelongsToTask := false
		for _, event := range s.events[taskID] {
			id, decodeErr := decodeEventCursor(event.Cursor)
			if decodeErr == nil && id == after {
				cursorBelongsToTask = true
				break
			}
		}
		if !cursorBelongsToTask {
			return nil, ErrInvalidEventCursor
		}
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	result := make([]StoredEvent, 0)
	for _, event := range s.events[taskID] {
		id, err := decodeEventCursor(event.Cursor)
		if err != nil || id <= after {
			continue
		}
		copyEvent := event
		copyEvent.Payload = cloneStreamResponse(event.Payload)
		result = append(result, copyEvent)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (s *MemoryStore) CreatePushConfig(ctx context.Context, config PushNotificationConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, exists := s.tasks[config.TaskID]
	if !exists || !taskAccessible(ctx, task) {
		return ErrTaskNotFound
	}
	configs := s.pushConfigs[config.TaskID]
	if configs == nil {
		configs = make(map[string]PushNotificationConfig)
		s.pushConfigs[config.TaskID] = configs
	}
	if _, exists := configs[config.ID]; exists {
		return ErrTaskConflict
	}
	configs[config.ID] = clonePushConfig(config)
	return nil
}

func (s *MemoryStore) GetPushConfig(ctx context.Context, taskID, id string) (PushNotificationConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, exists := s.tasks[taskID]
	if !exists || !taskAccessible(ctx, task) {
		return PushNotificationConfig{}, ErrTaskNotFound
	}
	config, exists := s.pushConfigs[taskID][id]
	if !exists {
		return PushNotificationConfig{}, ErrPushConfigNotFound
	}
	return clonePushConfig(config), nil
}

func (s *MemoryStore) ListPushConfigs(ctx context.Context, taskID, pageToken string, pageSize int) ([]PushNotificationConfig, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, exists := s.tasks[taskID]
	if !exists || !taskAccessible(ctx, task) {
		return nil, "", ErrTaskNotFound
	}
	offset, err := decodeOffset(pageToken)
	if err != nil {
		return nil, "", err
	}
	pageSize = normalizePageSize(pageSize)
	configs := make([]PushNotificationConfig, 0, len(s.pushConfigs[taskID]))
	for _, config := range s.pushConfigs[taskID] {
		configs = append(configs, clonePushConfig(config))
	}
	sort.Slice(configs, func(i, j int) bool {
		if configs[i].CreatedAt.Equal(configs[j].CreatedAt) {
			return configs[i].ID < configs[j].ID
		}
		return configs[i].CreatedAt.Before(configs[j].CreatedAt)
	})
	if offset > len(configs) {
		offset = len(configs)
	}
	end := offset + pageSize
	if end > len(configs) {
		end = len(configs)
	}
	next := ""
	if end < len(configs) {
		next = encodeOffset(end)
	}
	result := append([]PushNotificationConfig(nil), configs[offset:end]...)
	return result, next, nil
}

func (s *MemoryStore) DeletePushConfig(ctx context.Context, taskID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, exists := s.tasks[taskID]
	if !exists || !taskAccessible(ctx, task) {
		return ErrTaskNotFound
	}
	delete(s.pushConfigs[taskID], id)
	return nil
}

func normalizePageSize(size int) int {
	if size <= 0 {
		return 50
	}
	if size > 100 {
		return 100
	}
	return size
}

func projectTask(task Task, historyLength *int, includeArtifacts bool) Task {
	if historyLength != nil {
		length := *historyLength
		if length <= 0 {
			task.History = nil
		} else if len(task.History) > length {
			task.History = append([]Message(nil), task.History[len(task.History)-length:]...)
		}
	}
	if !includeArtifacts {
		task.Artifacts = nil
	}
	return task
}

func encodeOffset(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte("offset:" + strconv.Itoa(offset)))
}

func decodeOffset(token string) (int, error) {
	if token == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || !strings.HasPrefix(string(raw), "offset:") {
		return 0, ErrInvalidPageToken
	}
	offset, err := strconv.Atoi(strings.TrimPrefix(string(raw), "offset:"))
	if err != nil || offset < 0 {
		return 0, ErrInvalidPageToken
	}
	return offset, nil
}

type taskPageCursor struct {
	StatusTimestamp time.Time `json:"statusTimestamp"`
	ID              string    `json:"id"`
}

func encodeTaskPageToken(task Task) string {
	raw, _ := json.Marshal(taskPageCursor{
		StatusTimestamp: task.Status.Timestamp.UTC(),
		ID:              task.ID,
	})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeTaskPageToken(token string) (*taskPageCursor, error) {
	if token == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, ErrInvalidPageToken
	}
	var cursor taskPageCursor
	if err := json.Unmarshal(raw, &cursor); err != nil ||
		cursor.StatusTimestamp.IsZero() ||
		cursor.ID == "" {
		return nil, ErrInvalidPageToken
	}
	return &cursor, nil
}

func encodeEventCursor(id uint64) string {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], id)
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

func decodeEventCursor(cursor string) (uint64, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(raw) != 8 {
		return 0, ErrInvalidEventCursor
	}
	return binary.BigEndian.Uint64(raw), nil
}

func cloneStreamResponse(src StreamResponse) StreamResponse {
	var dst StreamResponse
	data, _ := json.Marshal(src)
	_ = json.Unmarshal(data, &dst)
	return dst
}

func clonePushConfig(src PushNotificationConfig) PushNotificationConfig {
	var dst PushNotificationConfig
	data, _ := json.Marshal(src)
	_ = json.Unmarshal(data, &dst)
	dst.CreatedAt = src.CreatedAt
	return dst
}
