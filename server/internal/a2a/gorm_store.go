package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/security"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GormStore struct {
	db        *gorm.DB
	protector security.Protector
}

// NewGormStoreWithProtector injects the data-encryption keyring used by A2A
// push credentials. A nil protector is fail-closed when a secret is present.
func NewGormStoreWithProtector(db *gorm.DB, protector security.Protector) *GormStore {
	return &GormStore{db: db, protector: protector}
}

// MigrationModels returns the A2A persistence models for application startup
// migrations without making the database package depend on implementation
// details from this package.
func MigrationModels() []any {
	return []any{
		&models.AgentTask{},
		&models.AgentMessage{},
		&models.AgentArtifact{},
		&models.AgentTaskStatusHistory{},
		&models.AgentTaskEvent{},
		&models.AgentPushNotificationConfig{},
	}
}

func (s *GormStore) AutoMigrate() error {
	return s.db.AutoMigrate(MigrationModels()...)
}

func (s *GormStore) CreateTask(ctx context.Context, task Task) error {
	if err := bindTaskOwner(ctx, &task); err != nil {
		return err
	}
	if task.Version == 0 {
		task.Version = 1
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return createGormTask(tx.WithContext(ctx), task)
	})
}

func (s *GormStore) FindTaskByMessageID(ctx context.Context, messageID string) (Task, error) {
	var message models.AgentMessage
	if err := s.db.WithContext(ctx).
		Select("task_id").
		First(&message, "id = ?", strings.TrimSpace(messageID)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Task{}, ErrTaskNotFound
		}
		return Task{}, err
	}
	return s.GetTask(ctx, message.TaskID)
}

func (s *GormStore) UpdateTask(ctx context.Context, task Task) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return updateGormTask(tx.WithContext(ctx), task)
	})
}

func (s *GormStore) ClaimTaskExecution(
	ctx context.Context,
	taskID string,
	messageID string,
	expectedVersion uint64,
	claimID string,
	now time.Time,
	expiresAt time.Time,
) error {
	databaseNow := taskClaimDatabaseNowSQL(s.db)
	databaseExpiry := taskClaimDatabaseExpirySQL(s.db, expiresAt.Sub(now))
	result := s.db.WithContext(ctx).
		Model(&models.AgentTask{}).
		Where("id = ? AND version = ?", taskID, expectedVersion).
		Where("state IN ?", []models.A2ATaskState{
			models.A2ATaskStateSubmitted,
			models.A2ATaskStateWorking,
		}).
		Where(
			"execution_claim_id = ? OR execution_expires_at IS NULL OR execution_expires_at <= "+databaseNow,
			"",
		).
		Scopes(scopeA2ATaskOwner).
		UpdateColumns(map[string]any{
			"execution_claim_id":   claimID,
			"execution_message_id": messageID,
			"execution_expires_at": databaseExpiry,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}
	return s.executionClaimFailure(ctx, taskID, expectedVersion)
}

func (s *GormStore) RenewTaskExecution(
	ctx context.Context,
	taskID string,
	messageID string,
	claimID string,
	now time.Time,
	expiresAt time.Time,
) error {
	databaseNow := taskClaimDatabaseNowSQL(s.db)
	databaseExpiry := taskClaimDatabaseExpirySQL(s.db, expiresAt.Sub(now))
	result := s.db.WithContext(ctx).
		Model(&models.AgentTask{}).
		Where(
			"id = ? AND execution_claim_id = ? AND execution_message_id = ?",
			taskID,
			claimID,
			messageID,
		).
		Where("execution_expires_at > "+databaseNow).
		Where("state IN ?", []models.A2ATaskState{
			models.A2ATaskStateSubmitted,
			models.A2ATaskStateWorking,
		}).
		Scopes(scopeA2ATaskOwner).
		UpdateColumn("execution_expires_at", databaseExpiry)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrTaskBusy
	}
	return nil
}

func (s *GormStore) ReleaseTaskExecution(
	ctx context.Context,
	taskID string,
	messageID string,
	claimID string,
) error {
	result := s.db.WithContext(ctx).
		Model(&models.AgentTask{}).
		Where(
			"id = ? AND execution_claim_id = ? AND execution_message_id = ?",
			taskID,
			claimID,
			messageID,
		).
		Scopes(scopeA2ATaskOwner).
		UpdateColumns(map[string]any{
			"execution_claim_id":   "",
			"execution_message_id": "",
			"execution_expires_at": nil,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrTaskBusy
	}
	return nil
}

func (s *GormStore) executionClaimFailure(
	ctx context.Context,
	taskID string,
	expectedVersion uint64,
) error {
	var task models.AgentTask
	if err := s.db.WithContext(ctx).
		Select("id", "version").
		Where("id = ?", taskID).
		Scopes(scopeA2ATaskOwner).
		First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrTaskNotFound
		}
		return err
	}
	if task.Version != expectedVersion {
		return ErrTaskConflict
	}
	return ErrTaskBusy
}

func (s *GormStore) GetTask(ctx context.Context, id string) (Task, error) {
	return getGormTask(s.db.WithContext(ctx), id)
}

func (s *GormStore) ListTasks(ctx context.Context, params ListTasksParams) (ListTasksResult, error) {
	pageSize := normalizePageSize(params.PageSize)
	cursor, err := decodeTaskPageToken(params.PageToken)
	if err != nil {
		return ListTasksResult{}, err
	}
	query := s.db.WithContext(ctx).Model(&models.AgentTask{}).Scopes(scopeA2ATaskOwner)
	if params.ContextID != "" {
		query = query.Where("context_id = ?", params.ContextID)
	}
	if params.Status != "" {
		state := normalizeTaskState(params.Status)
		if state == TaskStateUnspecified {
			return ListTasksResult{}, ErrInvalidPageToken
		}
		query = query.Where("state = ?", persistedTaskState(state))
	}
	if params.StatusTimestampAfter != nil {
		query = query.Where("status_timestamp >= ?", params.StatusTimestampAfter.UTC())
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return ListTasksResult{}, err
	}
	if cursor != nil {
		query = query.Where(
			"(status_timestamp < ?) OR (status_timestamp = ? AND id > ?)",
			cursor.StatusTimestamp,
			cursor.StatusTimestamp,
			cursor.ID,
		)
	}
	var rows []models.AgentTask
	if err := query.Order("status_timestamp DESC, id ASC").Limit(pageSize + 1).Find(&rows).Error; err != nil {
		return ListTasksResult{}, err
	}
	hasNext := len(rows) > pageSize
	if hasNext {
		rows = rows[:pageSize]
	}
	tasks := make([]Task, 0, len(rows))
	for i := range rows {
		task, err := hydrateGormTask(s.db.WithContext(ctx), rows[i])
		if err != nil {
			return ListTasksResult{}, err
		}
		tasks = append(tasks, projectTask(task, params.HistoryLength, params.IncludeArtifacts))
	}
	next := ""
	if hasNext && len(tasks) > 0 {
		next = encodeTaskPageToken(tasks[len(tasks)-1])
	}
	return ListTasksResult{
		Tasks:         tasks,
		NextPageToken: next,
		PageSize:      pageSize,
		TotalSize:     total,
	}, nil
}

func (s *GormStore) AppendEvent(ctx context.Context, event StoredEvent) (StoredEvent, error) {
	return appendGormEvent(ctx, s.db, event)
}

func (s *GormStore) CreateTaskWithEvent(
	ctx context.Context,
	task Task,
	event StoredEvent,
	pushConfig *PushNotificationConfig,
	dispatcher TransactionalPushDispatcher,
) (StoredEvent, error) {
	if err := bindTaskOwner(ctx, &task); err != nil {
		return StoredEvent{}, err
	}
	if task.Version == 0 {
		task.Version = 1
	}
	return s.persistTaskWithEvent(
		ctx,
		func(tx *gorm.DB) error {
			return createGormTask(tx, task)
		},
		event,
		pushConfig,
		dispatcher,
	)
}

func (s *GormStore) UpdateTaskWithEvent(
	ctx context.Context,
	task Task,
	event StoredEvent,
	pushConfig *PushNotificationConfig,
	dispatcher TransactionalPushDispatcher,
) (StoredEvent, error) {
	return s.persistTaskWithEvent(
		ctx,
		func(tx *gorm.DB) error {
			return updateGormTask(tx, task)
		},
		event,
		pushConfig,
		dispatcher,
	)
}

func (s *GormStore) persistTaskWithEvent(
	ctx context.Context,
	mutate func(*gorm.DB) error,
	event StoredEvent,
	pushConfig *PushNotificationConfig,
	dispatcher TransactionalPushDispatcher,
) (StoredEvent, error) {
	var persisted StoredEvent
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tx = tx.WithContext(ctx)
		if err := mutate(tx); err != nil {
			return err
		}
		if pushConfig != nil {
			if err := createGormPushConfig(tx, *pushConfig, s.protector); err != nil {
				return err
			}
		}
		var appendErr error
		persisted, appendErr = appendGormEvent(ctx, tx, event)
		if appendErr != nil {
			return appendErr
		}
		return enqueueGormPushDeliveries(ctx, tx, event.TaskID, persisted, dispatcher, s.protector)
	})
	if err != nil {
		return StoredEvent{}, err
	}
	return persisted, nil
}

func (s *GormStore) AppendEventWithPush(
	ctx context.Context,
	event StoredEvent,
	dispatcher TransactionalPushDispatcher,
) (StoredEvent, error) {
	if dispatcher == nil {
		return StoredEvent{}, errors.New("transactional push dispatcher is required")
	}
	var persisted StoredEvent
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var appendErr error
		persisted, appendErr = appendGormEvent(ctx, tx, event)
		if appendErr != nil {
			return appendErr
		}
		return enqueueGormPushDeliveries(ctx, tx, event.TaskID, persisted, dispatcher, s.protector)
	})
	if err != nil {
		return StoredEvent{}, err
	}
	return persisted, nil
}

func appendGormEvent(ctx context.Context, db *gorm.DB, event StoredEvent) (StoredEvent, error) {
	var task models.AgentTask
	if err := db.WithContext(ctx).
		Select("id", "version").
		Where("id = ?", event.TaskID).
		Scopes(scopeA2ATaskOwner).
		First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return StoredEvent{}, ErrTaskNotFound
		}
		return StoredEvent{}, err
	}
	if event.ResourceVersion == 0 {
		event.ResourceVersion = task.Version
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return StoredEvent{}, err
	}
	row := models.AgentTaskEvent{
		TaskID:          event.TaskID,
		ContextID:       event.ContextID,
		ResourceVersion: event.ResourceVersion,
		Payload:         datatypes.JSON(payload),
		CreatedAt:       event.CreatedAt,
	}
	if err := db.WithContext(ctx).Create(&row).Error; err != nil {
		return StoredEvent{}, err
	}
	event.Cursor = encodeEventCursor(row.ID)
	return event, nil
}

func (s *GormStore) EventsAfter(ctx context.Context, taskID, cursor string, limit int) ([]StoredEvent, error) {
	var taskCount int64
	if err := s.db.WithContext(ctx).
		Model(&models.AgentTask{}).
		Where("id = ?", taskID).
		Scopes(scopeA2ATaskOwner).
		Count(&taskCount).Error; err != nil {
		return nil, err
	}
	if taskCount == 0 {
		return nil, ErrTaskNotFound
	}
	after, err := decodeEventCursor(cursor)
	if err != nil {
		return nil, err
	}
	if after > 0 {
		var cursorEvent models.AgentTaskEvent
		err := s.db.WithContext(ctx).Select("id", "task_id").Where("id = ?", after).First(&cursorEvent).Error
		if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && cursorEvent.TaskID != taskID) {
			return nil, ErrInvalidEventCursor
		}
		if err != nil {
			return nil, err
		}
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	var rows []models.AgentTaskEvent
	if err := s.db.WithContext(ctx).
		Where("task_id = ? AND id > ?", taskID, after).
		Order("id ASC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	events := make([]StoredEvent, 0, len(rows))
	for _, row := range rows {
		var payload StreamResponse
		if err := json.Unmarshal(row.Payload, &payload); err != nil {
			return nil, err
		}
		events = append(events, StoredEvent{
			Cursor:          encodeEventCursor(row.ID),
			TaskID:          row.TaskID,
			ContextID:       row.ContextID,
			ResourceVersion: row.ResourceVersion,
			Payload:         payload,
			CreatedAt:       row.CreatedAt,
		})
	}
	return events, nil
}

func (s *GormStore) CreatePushConfig(ctx context.Context, config PushNotificationConfig) error {
	if _, err := s.GetTask(ctx, config.TaskID); err != nil {
		return err
	}
	return createGormPushConfig(s.db.WithContext(ctx), config, s.protector)
}

func createGormPushConfig(
	db *gorm.DB,
	config PushNotificationConfig,
	protector security.Protector,
) error {
	token, err := security.ProtectOptional(
		protector,
		config.Token,
		a2aPushSecretAAD(config.ID, "token"),
	)
	if err != nil {
		return err
	}
	var authentication datatypes.JSON
	if config.Authentication != nil {
		plaintext, err := json.Marshal(config.Authentication)
		if err != nil {
			return err
		}
		envelope, err := security.ProtectOptional(
			protector,
			string(plaintext),
			a2aPushSecretAAD(config.ID, "authentication"),
		)
		clear(plaintext)
		if err != nil {
			return err
		}
		encodedEnvelope, err := json.Marshal(envelope)
		if err != nil {
			return err
		}
		authentication = datatypes.JSON(encodedEnvelope)
	}
	row := models.AgentPushNotificationConfig{
		ID:             config.ID,
		TaskID:         config.TaskID,
		URL:            config.URL,
		Token:          token,
		Authentication: authentication,
		CreatedAt:      config.CreatedAt,
	}
	if err := db.Create(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return ErrTaskConflict
		}
		return err
	}
	return nil
}

func (s *GormStore) GetPushConfig(ctx context.Context, taskID, id string) (PushNotificationConfig, error) {
	if _, err := s.GetTask(ctx, taskID); err != nil {
		return PushNotificationConfig{}, err
	}
	var row models.AgentPushNotificationConfig
	err := s.db.WithContext(ctx).Where("task_id = ? AND id = ?", taskID, id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PushNotificationConfig{}, ErrPushConfigNotFound
	}
	if err != nil {
		return PushNotificationConfig{}, err
	}
	return pushConfigFromModel(row, s.protector)
}

func (s *GormStore) ListPushConfigs(ctx context.Context, taskID, pageToken string, pageSize int) ([]PushNotificationConfig, string, error) {
	var taskCount int64
	if err := s.db.WithContext(ctx).
		Model(&models.AgentTask{}).
		Where("id = ?", taskID).
		Scopes(scopeA2ATaskOwner).
		Count(&taskCount).Error; err != nil {
		return nil, "", err
	}
	if taskCount == 0 {
		return nil, "", ErrTaskNotFound
	}
	offset, err := decodeOffset(pageToken)
	if err != nil {
		return nil, "", err
	}
	pageSize = normalizePageSize(pageSize)
	var total int64
	query := s.db.WithContext(ctx).Model(&models.AgentPushNotificationConfig{}).Where("task_id = ?", taskID)
	if err := query.Count(&total).Error; err != nil {
		return nil, "", err
	}
	var rows []models.AgentPushNotificationConfig
	if err := query.Order("created_at ASC, id ASC").Offset(offset).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, "", err
	}
	configs := make([]PushNotificationConfig, 0, len(rows))
	for _, row := range rows {
		config, err := pushConfigFromModel(row, s.protector)
		if err != nil {
			return nil, "", err
		}
		configs = append(configs, config)
	}
	next := ""
	if int64(offset+len(rows)) < total {
		next = encodeOffset(offset + len(rows))
	}
	return configs, next, nil
}

func (s *GormStore) DeletePushConfig(ctx context.Context, taskID, id string) error {
	var taskCount int64
	if err := s.db.WithContext(ctx).
		Model(&models.AgentTask{}).
		Where("id = ?", taskID).
		Scopes(scopeA2ATaskOwner).
		Count(&taskCount).Error; err != nil {
		return err
	}
	if taskCount == 0 {
		return ErrTaskNotFound
	}
	return s.db.WithContext(ctx).
		Where("task_id = ? AND id = ?", taskID, id).
		Delete(&models.AgentPushNotificationConfig{}).Error
}

func createGormTask(db *gorm.DB, task Task) error {
	row, err := taskModel(task)
	if err != nil {
		return err
	}
	if err := db.Omit(clause.Associations).Create(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return ErrTaskConflict
		}
		return err
	}
	return persistTaskChildren(db, task)
}

func updateGormTask(db *gorm.DB, task Task) error {
	current, err := getGormTask(db, task.ID)
	if err != nil {
		return err
	}
	if task.OwnerActorType != current.OwnerActorType ||
		task.OwnerActorID != current.OwnerActorID ||
		task.OwnerCredentialID != current.OwnerCredentialID {
		return ErrTaskConflict
	}
	claim, claimedExecution := taskExecutionClaimFromContext(db.Statement.Context)
	if claimedExecution &&
		(claim.TaskID != current.ID ||
			claim.MessageID != current.ExecutionMessageID ||
			claim.ClaimID != current.ExecutionClaimID) {
		return ErrTaskBusy
	}
	row, err := taskModel(task)
	if err != nil {
		return err
	}
	nextVersion := task.Version + 1
	query := db.Model(&models.AgentTask{}).
		Where("id = ? AND version = ?", task.ID, task.Version).
		Scopes(scopeA2ATaskOwner)
	if claimedExecution {
		databaseNow := taskClaimDatabaseNowSQL(db)
		query = query.Where(
			"execution_claim_id = ? AND execution_message_id = ? AND execution_expires_at > "+databaseNow,
			claim.ClaimID,
			claim.MessageID,
		)
	}
	result := query.Updates(map[string]any{
		"context_id":       row.ContextID,
		"linked_ticket_id": row.LinkedTicketID,
		"state":            row.State,
		"status_message":   row.StatusMessage,
		"status_timestamp": row.StatusTimestamp,
		"metadata":         row.Metadata,
		"version":          nextVersion,
		"updated_at":       task.LastModified,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		var persisted models.AgentTask
		if err := db.Select("id", "version").
			Where("id = ?", task.ID).
			Scopes(scopeA2ATaskOwner).
			First(&persisted).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTaskNotFound
			}
			return err
		}
		if persisted.Version != task.Version {
			return ErrTaskConflict
		}
		if claimedExecution {
			return ErrTaskBusy
		}
		return ErrTaskConflict
	}
	return persistTaskChildren(db, task)
}

func taskClaimDatabaseNowSQL(db *gorm.DB) string {
	if db != nil && db.Dialector != nil && db.Dialector.Name() == "sqlite" {
		return "STRFTIME('%Y-%m-%d %H:%M:%f','now')"
	}
	return "CURRENT_TIMESTAMP"
}

func taskClaimDatabaseExpirySQL(db *gorm.DB, ttl time.Duration) clause.Expr {
	if ttl < 0 {
		ttl = 0
	}
	microseconds := ttl.Microseconds()
	if db != nil && db.Dialector != nil {
		switch db.Dialector.Name() {
		case "sqlite":
			return gorm.Expr(
				"STRFTIME('%Y-%m-%d %H:%M:%f','now', ?)",
				fmt.Sprintf("%+.6f seconds", ttl.Seconds()),
			)
		case "mysql":
			return gorm.Expr(
				"TIMESTAMPADD(MICROSECOND, ?, CURRENT_TIMESTAMP(6))",
				microseconds,
			)
		case "sqlserver":
			return gorm.Expr(
				"DATEADD(microsecond, ?, SYSUTCDATETIME())",
				microseconds,
			)
		}
	}
	return gorm.Expr(
		"CURRENT_TIMESTAMP + (? * INTERVAL '1 microsecond')",
		microseconds,
	)
}

func enqueueGormPushDeliveries(
	ctx context.Context,
	tx *gorm.DB,
	taskID string,
	event StoredEvent,
	dispatcher TransactionalPushDispatcher,
	protector security.Protector,
) error {
	var rows []models.AgentPushNotificationConfig
	if err := tx.WithContext(ctx).
		Where("task_id = ?", taskID).
		Order("created_at ASC, id ASC").
		Find(&rows).Error; err != nil {
		return err
	}
	if dispatcher == nil {
		if len(rows) > 0 {
			return ErrPushUnavailable
		}
		return nil
	}
	for _, row := range rows {
		config, err := pushConfigFromModel(row, protector)
		if err != nil {
			return err
		}
		if err := dispatcher.EnqueueTx(ctx, tx, config, event); err != nil {
			return err
		}
	}
	return nil
}

func getGormTask(db *gorm.DB, id string) (Task, error) {
	var row models.AgentTask
	err := db.Where("id = ?", id).Scopes(scopeA2ATaskOwner).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Task{}, ErrTaskNotFound
	}
	if err != nil {
		return Task{}, err
	}
	return hydrateGormTask(db, row)
}

func hydrateGormTask(db *gorm.DB, row models.AgentTask) (Task, error) {
	task, err := taskFromModel(row)
	if err != nil {
		return Task{}, err
	}
	var messages []models.AgentMessage
	if err := db.Where("task_id = ?", row.ID).Order("sequence ASC").Find(&messages).Error; err != nil {
		return Task{}, err
	}
	for _, item := range messages {
		var message Message
		if err := json.Unmarshal(item.Payload, &message); err != nil {
			return Task{}, err
		}
		message.RequestDigest = item.RequestDigest
		task.History = append(task.History, message)
	}
	var artifacts []models.AgentArtifact
	if err := db.Where("task_id = ?", row.ID).Order("sequence ASC").Find(&artifacts).Error; err != nil {
		return Task{}, err
	}
	for _, item := range artifacts {
		var artifact Artifact
		if err := json.Unmarshal(item.Payload, &artifact); err != nil {
			return Task{}, err
		}
		task.Artifacts = append(task.Artifacts, artifact)
	}
	var statuses []models.AgentTaskStatusHistory
	if err := db.Where("task_id = ?", row.ID).Order("sequence ASC").Find(&statuses).Error; err != nil {
		return Task{}, err
	}
	for _, item := range statuses {
		var status TaskStatus
		if err := json.Unmarshal(item.Status, &status); err != nil {
			return Task{}, err
		}
		task.StatusHistory = append(task.StatusHistory, status)
	}
	return task, nil
}

func taskModel(task Task) (models.AgentTask, error) {
	statusMessage, err := json.Marshal(task.Status.Message)
	if err != nil {
		return models.AgentTask{}, err
	}
	metadata, err := json.Marshal(task.Metadata)
	if err != nil {
		return models.AgentTask{}, err
	}
	return models.AgentTask{
		ID:                 task.ID,
		ContextID:          task.ContextID,
		LinkedTicketID:     task.LinkedTicketID,
		OwnerActorType:     models.ActorType(task.OwnerActorType),
		OwnerActorID:       task.OwnerActorID,
		OwnerCredentialID:  task.OwnerCredentialID,
		State:              persistedTaskState(task.Status.State),
		StatusMessage:      datatypes.JSON(statusMessage),
		StatusTimestamp:    task.Status.Timestamp,
		Metadata:           datatypes.JSON(metadata),
		Version:            task.Version,
		ExecutionClaimID:   task.ExecutionClaimID,
		ExecutionMessageID: task.ExecutionMessageID,
		ExecutionExpiresAt: task.ExecutionExpiresAt,
		CreatedAt:          task.CreatedAt,
		UpdatedAt:          task.LastModified,
	}, nil
}

func taskFromModel(row models.AgentTask) (Task, error) {
	task := Task{
		ID:                row.ID,
		ContextID:         row.ContextID,
		LinkedTicketID:    row.LinkedTicketID,
		OwnerActorType:    string(row.OwnerActorType),
		OwnerActorID:      row.OwnerActorID,
		OwnerCredentialID: row.OwnerCredentialID,
		Status: TaskStatus{
			State:     wireTaskState(row.State),
			Timestamp: row.StatusTimestamp,
		},
		CreatedAt:          row.CreatedAt,
		LastModified:       row.UpdatedAt,
		Version:            row.Version,
		ExecutionClaimID:   row.ExecutionClaimID,
		ExecutionMessageID: row.ExecutionMessageID,
		ExecutionExpiresAt: row.ExecutionExpiresAt,
	}
	if len(row.StatusMessage) > 0 && string(row.StatusMessage) != "null" {
		var message Message
		if err := json.Unmarshal(row.StatusMessage, &message); err != nil {
			return Task{}, err
		}
		task.Status.Message = &message
	}
	if len(row.Metadata) > 0 && string(row.Metadata) != "null" {
		if err := json.Unmarshal(row.Metadata, &task.Metadata); err != nil {
			return Task{}, err
		}
	}
	return task, nil
}

func scopeA2ATaskOwner(db *gorm.DB) *gorm.DB {
	if db == nil || db.Statement == nil {
		return db
	}
	owner, scoped := TaskOwnerFromContext(db.Statement.Context)
	if !scoped {
		return db
	}
	return db.Where("owner_actor_type = ? AND owner_actor_id = ?", owner.ActorType, owner.ActorID)
}

func persistTaskChildren(tx *gorm.DB, task Task) error {
	for sequence, message := range task.History {
		payload, err := json.Marshal(message)
		if err != nil {
			return err
		}
		row := models.AgentMessage{
			ID:            message.MessageID,
			TaskID:        task.ID,
			ContextID:     task.ContextID,
			Role:          string(message.Role),
			Sequence:      uint64(sequence + 1),
			RequestDigest: message.RequestDigest,
			Payload:       datatypes.JSON(payload),
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			var existing models.AgentMessage
			if err := tx.Select("task_id", "request_digest").First(&existing, "id = ?", row.ID).Error; err != nil {
				return err
			}
			if existing.TaskID != task.ID || existing.RequestDigest != row.RequestDigest {
				return ErrTaskConflict
			}
		}
	}
	for sequence, artifact := range task.Artifacts {
		payload, err := json.Marshal(artifact)
		if err != nil {
			return err
		}
		row := models.AgentArtifact{
			ID:       artifact.ArtifactID,
			TaskID:   task.ID,
			Sequence: uint64(sequence + 1),
			Payload:  datatypes.JSON(payload),
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}, {Name: "task_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"sequence", "payload", "updated_at"}),
		}).Create(&row).Error; err != nil {
			return err
		}
	}
	for sequence, status := range task.StatusHistory {
		payload, err := json.Marshal(status)
		if err != nil {
			return err
		}
		row := models.AgentTaskStatusHistory{
			TaskID:   task.ID,
			Sequence: uint64(sequence + 1),
			State:    persistedTaskState(status.State),
			Status:   datatypes.JSON(payload),
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "task_id"}, {Name: "sequence"}},
			DoNothing: true,
		}).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func persistedTaskState(state TaskState) models.A2ATaskState {
	switch normalizeTaskState(state) {
	case TaskStateSubmitted:
		return models.A2ATaskStateSubmitted
	case TaskStateWorking:
		return models.A2ATaskStateWorking
	case TaskStateInputRequired:
		return models.A2ATaskStateInputRequired
	case TaskStateCompleted:
		return models.A2ATaskStateCompleted
	case TaskStateFailed:
		return models.A2ATaskStateFailed
	case TaskStateCanceled:
		return models.A2ATaskStateCanceled
	case TaskStateRejected:
		return models.A2ATaskStateRejected
	case TaskStateAuthRequired:
		return models.A2ATaskStateAuthRequired
	default:
		return models.A2ATaskState("")
	}
}

func wireTaskState(state models.A2ATaskState) TaskState {
	switch state {
	case models.A2ATaskStateSubmitted:
		return TaskStateSubmitted
	case models.A2ATaskStateWorking:
		return TaskStateWorking
	case models.A2ATaskStateInputRequired:
		return TaskStateInputRequired
	case models.A2ATaskStateCompleted:
		return TaskStateCompleted
	case models.A2ATaskStateFailed:
		return TaskStateFailed
	case models.A2ATaskStateCanceled:
		return TaskStateCanceled
	case models.A2ATaskStateRejected:
		return TaskStateRejected
	case models.A2ATaskStateAuthRequired:
		return TaskStateAuthRequired
	default:
		return TaskStateUnspecified
	}
}

func pushConfigFromModel(
	row models.AgentPushNotificationConfig,
	protector security.Protector,
) (PushNotificationConfig, error) {
	token, err := security.RevealOptional(
		protector,
		row.Token,
		a2aPushSecretAAD(row.ID, "token"),
	)
	if err != nil {
		return PushNotificationConfig{}, err
	}
	config := PushNotificationConfig{
		ID:        row.ID,
		TaskID:    row.TaskID,
		URL:       row.URL,
		Token:     token,
		CreatedAt: row.CreatedAt,
	}
	if len(row.Authentication) > 0 && string(row.Authentication) != "null" {
		var envelope string
		if err := json.Unmarshal(row.Authentication, &envelope); err != nil {
			return PushNotificationConfig{}, security.ErrPlaintextSecret
		}
		plaintext, err := security.RevealOptional(
			protector,
			envelope,
			a2aPushSecretAAD(row.ID, "authentication"),
		)
		if err != nil {
			return PushNotificationConfig{}, err
		}
		if err := json.Unmarshal([]byte(plaintext), &config.Authentication); err != nil {
			return PushNotificationConfig{}, err
		}
	}
	return config, nil
}

func a2aPushSecretAAD(configID, field string) []byte {
	return security.FieldAAD("agent_push_notification_configs", configID, field)
}
