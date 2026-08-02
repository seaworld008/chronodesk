package services

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// AttachmentUploadMigrationDataField is an immutable, private recovery
	// manifest persisted with the upload-requested DomainEvent before any
	// final object write. CloudEventFromModel removes it from every external
	// representation.
	AttachmentUploadMigrationDataField   = "_attachment_upload_migration"
	attachmentUploadCancelledStorageType = "upload_cancelled"
	attachmentUploadCleanupTimeout       = 5 * time.Second
)

var (
	errAttachmentUploadObsolete = errors.New(
		"attachment upload no longer has a live attachment",
	)
	errAttachmentUploadStillReferenced = errors.New(
		"attachment upload still has a live attachment reference",
	)
)

type attachmentUploadMigrationIntent struct {
	AttachmentID      uint   `json:"attachment_id"`
	TicketID          uint   `json:"ticket_id"`
	StagingKey        string `json:"staging_key"`
	FinalKey          string `json:"final_key"`
	TargetStoreID     string `json:"target_store_id"`
	TargetStorageType string `json:"target_storage_type"`
	FileSize          int64  `json:"file_size"`
	SHA256            string `json:"sha256"`
}

// ExecuteAttachmentUploadOutbox copies one committed staging object into the
// managed attachment store. It is trusted-worker only and intentionally keeps
// both database phases separate from staging/storage I/O.
func (s *AgentNativeService) ExecuteAttachmentUploadOutbox(
	ctx context.Context,
	attachmentID uint,
) error {
	if s == nil || s.db == nil ||
		s.attachmentStorage == nil ||
		s.attachmentStaging == nil {
		return ErrAttachmentStorageMissing
	}
	if attachmentID == 0 {
		return ErrInvalidAttachment
	}
	if err := requireExternalIOOutsideProjectTransaction(
		ctx,
		"attachment upload worker",
	); err != nil {
		return err
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil ||
		operation.Source != SourceProtocolWorker ||
		operation.Actor != models.SystemActor(outboxSystemActorID) {
		return ErrInvalidActor
	}

	var (
		attachment          models.TicketAttachment
		migrationIntent     attachmentUploadMigrationIntent
		migrationEventID    string
		attachmentObsolete  bool
		attachmentCancelled bool
		alreadyStored       bool
		archivedCleanupOnly bool
	)
	err = scopeddb.WithProjectScopeContextTransaction(
		ctx,
		s.db,
		operation.Scope,
		func(scopedContext context.Context) error {
			if _, revalidateErr :=
				s.revalidateAttachmentAuthorizationInTransaction(
					scopedContext,
				); revalidateErr != nil {
				if !errors.Is(
					revalidateErr,
					ErrProjectAccessDenied,
				) {
					return revalidateErr
				}
				if cleanupErr :=
					s.revalidateAttachmentCleanupAuthorizationInTransaction(
						scopedContext,
					); cleanupErr != nil {
					return revalidateErr
				}
				archivedCleanupOnly = true
			}
			var intentErr error
			migrationIntent, migrationEventID, intentErr =
				s.loadAttachmentUploadMigrationIntentTx(
					scopedContext,
					attachmentID,
					operation.Scope,
				)
			if intentErr != nil {
				return intentErr
			}
			queryErr := s.db.WithContext(scopedContext).
				Clauses(clause.Locking{Strength: "UPDATE"}).
				Where(
					"id = ? AND organization_id = ? AND project_id = ?",
					attachmentID,
					operation.Scope.OrganizationID,
					operation.Scope.ProjectID,
				).
				Take(&attachment).Error
			if errors.Is(queryErr, gorm.ErrRecordNotFound) {
				attachmentObsolete = true
				return nil
			}
			if queryErr != nil {
				return queryErr
			}
			if attachment.StorageType ==
				attachmentUploadCancelledStorageType {
				if attachment.StoragePath != "" ||
					attachment.ID != migrationIntent.AttachmentID ||
					attachment.TicketID != migrationIntent.TicketID ||
					attachment.FileName != filepath.Base(
						migrationIntent.StagingKey,
					) ||
					attachment.FileSize != migrationIntent.FileSize ||
					!strings.EqualFold(
						attachment.Hash,
						migrationIntent.SHA256,
					) {
					return ErrInvalidAttachment
				}
				attachmentCancelled = true
				return nil
			}
			alreadyStored = attachment.StorageType != "staging"
			if err := validateAttachmentUploadIntent(
				attachment,
			); err != nil {
				return err
			}
			if err := validateAttachmentUploadMigrationMatches(
				migrationIntent,
				attachment,
				alreadyStored,
			); err != nil {
				return err
			}
			if !archivedCleanupOnly || alreadyStored {
				return nil
			}
			if err := transactionForContext(
				scopedContext,
				s.db,
				func(tx *gorm.DB) error {
					return s.cancelArchivedAttachmentUploadTx(
						scopedContext,
						tx,
						operation,
						attachment,
						migrationIntent,
						migrationEventID,
					)
				},
			); err != nil {
				return err
			}
			attachmentCancelled = true
			return nil
		},
	)
	if err != nil {
		return err
	}
	if attachmentObsolete || attachmentCancelled {
		return s.cleanupObsoleteAttachmentUpload(
			ctx,
			migrationIntent,
			nil,
		)
	}

	stagingKey := migrationIntent.StagingKey
	finalKey := migrationIntent.FinalKey
	if alreadyStored {
		return s.attachmentStaging.DeleteStaged(ctx, stagingKey)
	}
	reader, err := s.attachmentStaging.OpenStaged(ctx, stagingKey)
	if err != nil {
		return err
	}
	stored, putErr := putAttachmentInStore(
		ctx,
		s.attachmentStorage,
		migrationIntent.TargetStoreID,
		finalKey,
		reader,
		s.attachmentMaxBytes,
	)
	closeErr := reader.Close()
	if putErr != nil {
		return putErr
	}
	if closeErr != nil {
		return closeErr
	}
	if stored == nil ||
		stored.Key != finalKey ||
		stored.Size != migrationIntent.FileSize ||
		!strings.EqualFold(
			stored.SHA256,
			migrationIntent.SHA256,
		) ||
		stored.StoreID != migrationIntent.TargetStoreID ||
		stored.StorageType != migrationIntent.TargetStorageType {
		return ErrInvalidAttachment
	}

	err = scopeddb.WithProjectScopeContextTransaction(
		ctx,
		s.db,
		operation.Scope,
		func(scopedContext context.Context) error {
			if _, revalidateErr :=
				s.revalidateAttachmentAuthorizationInTransaction(
					scopedContext,
				); revalidateErr != nil {
				return revalidateErr
			}
			return transactionForContext(
				scopedContext,
				s.db,
				func(tx *gorm.DB) error {
					return s.finalizeAttachmentUploadTx(
						scopedContext,
						tx,
						operation,
						attachmentID,
						attachment,
						stagingKey,
						finalKey,
						stored,
					)
				},
			)
		},
	)
	if err != nil {
		if errors.Is(err, errAttachmentUploadObsolete) {
			return s.cleanupObsoleteAttachmentUpload(
				ctx,
				migrationIntent,
				stored,
			)
		}
		return err
	}
	return s.attachmentStaging.DeleteStaged(ctx, stagingKey)
}

func (s *AgentNativeService) finalizeAttachmentUploadTx(
	ctx context.Context,
	tx *gorm.DB,
	operation OperationContext,
	attachmentID uint,
	expected models.TicketAttachment,
	stagingKey string,
	finalKey string,
	stored *StoredAttachmentObject,
) error {
	if tx == nil || stored == nil {
		return ErrInvalidAttachment
	}
	var current models.TicketAttachment
	queryErr := tx.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			attachmentID,
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
		).
		Take(&current).Error
	if errors.Is(queryErr, gorm.ErrRecordNotFound) {
		return errAttachmentUploadObsolete
	}
	if queryErr != nil {
		return queryErr
	}
	if current.StorageType != "staging" {
		if current.StoragePath == finalKey &&
			current.StorageStoreID == stored.StoreID &&
			current.StorageVersionID == stored.VersionID {
			return nil
		}
		return ErrInvalidAttachment
	}
	if current.StoragePath != stagingKey ||
		current.FileName != expected.FileName ||
		current.FileSize != expected.FileSize ||
		!strings.EqualFold(current.Hash, expected.Hash) {
		return ErrInvalidAttachment
	}
	now := s.now().UTC()
	update := tx.WithContext(ctx).
		Model(&models.TicketAttachment{}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? AND storage_type = ? AND storage_path = ?",
			current.ID,
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
			"staging",
			stagingKey,
		).
		Updates(map[string]any{
			"storage_path":       finalKey,
			"storage_type":       stored.StorageType,
			"storage_store_id":   stored.StoreID,
			"storage_version_id": stored.VersionID,
			"updated_at":         now,
		})
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return ErrInvalidAttachment
	}
	var ticket models.Ticket
	ticketErr := tx.WithContext(ctx).
		Select("id", "version").
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			current.TicketID,
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
		).
		Take(&ticket).Error
	if errors.Is(ticketErr, gorm.ErrRecordNotFound) {
		return errAttachmentUploadObsolete
	}
	if ticketErr != nil {
		return ticketErr
	}
	_, appendErr := s.AppendDomainEventWithAdditionalTargetsTx(
		ctx,
		tx.WithContext(ctx),
		DomainEventInput{
			Type: eventcontract.
				TicketAttachmentCreatedEventType,
			Subject: fmt.Sprintf(
				"ticket/%d",
				current.TicketID,
			),
			Actor:           operation.Actor,
			ResourceVersion: ticket.Version,
			TraceID:         operation.TraceID,
			CorrelationID:   operation.CorrelationID,
			Data: map[string]any{
				"ticket_id":         current.TicketID,
				"attachment_id":     current.ID,
				"file_name":         current.OriginalName,
				"file_size":         current.FileSize,
				"sha256":            current.Hash,
				"virus_scan":        current.VirusScan,
				"storage_state":     "stored",
				"content_untrusted": true,
			},
		},
		nil,
	)
	return appendErr
}

func (s *AgentNativeService) cancelArchivedAttachmentUploadTx(
	ctx context.Context,
	tx *gorm.DB,
	operation OperationContext,
	attachment models.TicketAttachment,
	intent attachmentUploadMigrationIntent,
	migrationEventID string,
) error {
	if tx == nil ||
		strings.TrimSpace(migrationEventID) == "" ||
		attachment.StorageType != "staging" ||
		attachment.StoragePath != intent.StagingKey ||
		attachment.ID != intent.AttachmentID ||
		attachment.TicketID != intent.TicketID {
		return ErrInvalidAttachment
	}
	var ticket models.Ticket
	if err := tx.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id", "version").
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			attachment.TicketID,
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
		).
		Take(&ticket).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrInvalidAttachment
		}
		return err
	}
	if ticket.Version == 0 {
		return ErrVersionConflict
	}
	now := s.now().UTC()
	nextVersion := ticket.Version + 1
	tombstone := tx.WithContext(ctx).
		Model(&models.TicketAttachment{}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? AND storage_type = ? AND storage_path = ?",
			attachment.ID,
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
			"staging",
			intent.StagingKey,
		).
		Updates(map[string]any{
			"storage_path": "",
			"storage_type": attachmentUploadCancelledStorageType,
			"updated_at":   now,
		})
	if tombstone.Error != nil {
		return tombstone.Error
	}
	if tombstone.RowsAffected != 1 {
		return ErrInvalidAttachment
	}
	ticketUpdate := tx.WithContext(ctx).
		Model(&models.Ticket{}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? AND version = ?",
			ticket.ID,
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
			ticket.Version,
		).
		Updates(map[string]any{
			"version":    nextVersion,
			"updated_at": now,
		})
	if ticketUpdate.Error != nil {
		return ticketUpdate.Error
	}
	if ticketUpdate.RowsAffected != 1 {
		return ErrVersionConflict
	}
	event, err := s.AppendDomainEventWithAdditionalTargetsTx(
		ctx,
		tx.WithContext(ctx),
		DomainEventInput{
			Type: eventcontract.
				TicketAttachmentUploadCancelledEventType,
			Subject: fmt.Sprintf(
				"ticket/%d",
				attachment.TicketID,
			),
			Actor:           operation.Actor,
			ResourceVersion: nextVersion,
			TraceID:         operation.TraceID,
			CorrelationID:   operation.CorrelationID,
			CausationID:     migrationEventID,
			Data: map[string]any{
				"ticket_id":     attachment.TicketID,
				"attachment_id": attachment.ID,
				"storage_state": attachmentUploadCancelledStorageType,
				"reason":        "project_archived",
			},
		},
		nil,
	)
	if err != nil {
		return err
	}
	details, err := json.Marshal(map[string]any{
		"attachment_id": attachment.ID,
		"reason":        "project_archived",
		"storage_state": attachmentUploadCancelledStorageType,
	})
	if err != nil {
		return err
	}
	history := &models.TicketHistory{
		TicketID:     attachment.TicketID,
		ActorType:    operation.Actor.Type,
		ActorID:      operation.Actor.ID,
		Action:       models.HistoryActionAttachment,
		Description:  "附件上传已取消",
		Details:      string(details),
		AttachmentID: &attachment.ID,
		IsVisible:    attachment.IsPublic,
		IsSystem:     true,
		IsAutomated:  true,
	}
	if err := linkTicketHistoryToDomainEvent(
		history,
		event,
	); err != nil {
		return err
	}
	return tx.WithContext(ctx).Create(history).Error
}

func newAttachmentUploadMigrationIntent(
	attachment models.TicketAttachment,
	storage AttachmentStorage,
) (attachmentUploadMigrationIntent, error) {
	finalKey, err := attachmentFinalStorageKey(
		attachment.TicketID,
		attachment.FileName,
	)
	if err != nil {
		return attachmentUploadMigrationIntent{}, err
	}
	intent := attachmentUploadMigrationIntent{
		AttachmentID:      attachment.ID,
		TicketID:          attachment.TicketID,
		StagingKey:        attachmentStagingKey(attachment.FileName),
		FinalKey:          finalKey,
		TargetStoreID:     attachmentStorageStoreID(storage),
		TargetStorageType: attachmentStorageType(storage),
		FileSize:          attachment.FileSize,
		SHA256:            strings.ToLower(strings.TrimSpace(attachment.Hash)),
	}
	if err := validateAttachmentUploadMigrationIntent(
		intent,
	); err != nil {
		return attachmentUploadMigrationIntent{}, err
	}
	return intent, nil
}

func attachmentFinalStorageKey(
	ticketID uint,
	fileName string,
) (string, error) {
	fileName = strings.TrimSpace(fileName)
	if ticketID == 0 ||
		fileName == "" ||
		fileName != filepath.Base(fileName) ||
		strings.ContainsAny(fileName, `/\`) ||
		strings.Contains(fileName, "..") {
		return "", ErrInvalidAttachment
	}
	return fmt.Sprintf("tickets/%d/%s", ticketID, fileName), nil
}

func validateAttachmentUploadMigrationIntent(
	intent attachmentUploadMigrationIntent,
) error {
	if intent.AttachmentID == 0 ||
		intent.TicketID == 0 ||
		intent.FileSize <= 0 ||
		!validAttachmentStagingKey(intent.StagingKey) ||
		len(intent.SHA256) != 64 ||
		!validAttachmentStoreID(intent.TargetStoreID) ||
		intent.TargetStorageType == "" ||
		intent.TargetStorageType == "managed" {
		return ErrInvalidAttachment
	}
	if _, err := hex.DecodeString(intent.SHA256); err != nil {
		return ErrInvalidAttachment
	}
	fileName := filepath.Base(
		strings.TrimSpace(intent.StagingKey),
	)
	wantFinal, err := attachmentFinalStorageKey(
		intent.TicketID,
		fileName,
	)
	if err != nil || intent.FinalKey != wantFinal {
		return ErrInvalidAttachment
	}
	return nil
}

func validateAttachmentUploadMigrationMatches(
	intent attachmentUploadMigrationIntent,
	attachment models.TicketAttachment,
	alreadyStored bool,
) error {
	if err := validateAttachmentUploadMigrationIntent(
		intent,
	); err != nil {
		return err
	}
	if intent.AttachmentID != attachment.ID ||
		intent.TicketID != attachment.TicketID ||
		intent.FileSize != attachment.FileSize ||
		!strings.EqualFold(intent.SHA256, attachment.Hash) {
		return ErrInvalidAttachment
	}
	if alreadyStored {
		if attachment.StoragePath != intent.FinalKey ||
			attachment.StorageStoreID != intent.TargetStoreID ||
			attachment.StorageType != intent.TargetStorageType {
			return ErrInvalidAttachment
		}
		return nil
	}
	if attachment.StoragePath != intent.StagingKey {
		return ErrInvalidAttachment
	}
	return nil
}

func (s *AgentNativeService) loadAttachmentUploadMigrationIntentTx(
	ctx context.Context,
	attachmentID uint,
	scope models.ProjectScope,
) (attachmentUploadMigrationIntent, string, error) {
	if attachmentID == 0 || scope.IsZero() {
		return attachmentUploadMigrationIntent{}, "",
			ErrInvalidAttachment
	}
	destinationID := strconv.FormatUint(
		uint64(attachmentID),
		10,
	)
	var events []models.DomainEvent
	if err := s.db.WithContext(ctx).
		Model(&models.DomainEvent{}).
		Joins(
			"JOIN outbox_deliveries ON outbox_deliveries.event_id = domain_events.id",
		).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"domain_events.organization_id = ? AND domain_events.project_id = ? AND domain_events.type = ?",
			scope.OrganizationID,
			scope.ProjectID,
			"io.chronodesk.ticket.attachment.upload-requested.v1",
		).
		Where(
			"outbox_deliveries.organization_id = ? AND outbox_deliveries.project_id = ? AND outbox_deliveries.destination_type = ? AND outbox_deliveries.destination_id = ?",
			scope.OrganizationID,
			scope.ProjectID,
			AttachmentUploadOutboxDestination,
			destinationID,
		).
		Order("domain_events.created_at DESC").
		Limit(2).
		Find(&events).Error; err != nil {
		return attachmentUploadMigrationIntent{}, "", err
	}
	if len(events) != 1 {
		return attachmentUploadMigrationIntent{}, "",
			ErrInvalidAttachment
	}
	var data struct {
		TicketID     uint                            `json:"ticket_id"`
		AttachmentID uint                            `json:"attachment_id"`
		Migration    attachmentUploadMigrationIntent `json:"_attachment_upload_migration"`
	}
	if err := json.Unmarshal(events[0].Data, &data); err != nil {
		return attachmentUploadMigrationIntent{}, "",
			ErrInvalidAttachment
	}
	if data.TicketID == 0 ||
		data.AttachmentID != attachmentID ||
		data.Migration.AttachmentID != attachmentID ||
		data.Migration.TicketID != data.TicketID ||
		events[0].Subject !=
			fmt.Sprintf("ticket/%d", data.TicketID) {
		return attachmentUploadMigrationIntent{}, "",
			ErrInvalidAttachment
	}
	if err := validateAttachmentUploadMigrationIntent(
		data.Migration,
	); err != nil {
		return attachmentUploadMigrationIntent{}, "", err
	}
	return data.Migration, events[0].ID, nil
}

func (s *AgentNativeService) cleanupObsoleteAttachmentUpload(
	ctx context.Context,
	intent attachmentUploadMigrationIntent,
	stored *StoredAttachmentObject,
) error {
	if err := validateAttachmentUploadMigrationIntent(
		intent,
	); err != nil {
		return err
	}
	cleanupContext, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		attachmentUploadCleanupTimeout,
	)
	defer cancel()
	operation, err := OperationContextFromContext(cleanupContext)
	if err != nil ||
		operation.Source != SourceProtocolWorker ||
		operation.Actor != models.SystemActor(outboxSystemActorID) {
		return ErrInvalidActor
	}
	safeToDelete := false
	err = scopeddb.WithProjectScopeContextTransaction(
		cleanupContext,
		s.db,
		operation.Scope,
		func(scopedContext context.Context) error {
			if revalidateErr :=
				s.revalidateAttachmentCleanupAuthorizationInTransaction(
					scopedContext,
				); revalidateErr != nil {
				return revalidateErr
			}
			durableIntent, _, intentErr :=
				s.loadAttachmentUploadMigrationIntentTx(
					scopedContext,
					intent.AttachmentID,
					operation.Scope,
				)
			if intentErr != nil {
				return intentErr
			}
			if durableIntent != intent {
				return ErrInvalidAttachment
			}
			var references []models.TicketAttachment
			if queryErr := s.db.WithContext(scopedContext).
				Clauses(clause.Locking{Strength: "UPDATE"}).
				Select("id", "storage_path", "storage_type").
				Where(
					"organization_id = ? AND project_id = ?",
					operation.Scope.OrganizationID,
					operation.Scope.ProjectID,
				).
				Where(
					"id = ? OR storage_path IN ?",
					intent.AttachmentID,
					[]string{
						intent.StagingKey,
						intent.FinalKey,
					},
				).
				Order("id ASC").
				Find(&references).Error; queryErr != nil {
				return queryErr
			}
			safeToDelete = true
			for _, reference := range references {
				if reference.ID == intent.AttachmentID &&
					reference.StorageType ==
						attachmentUploadCancelledStorageType &&
					reference.StoragePath == "" {
					continue
				}
				safeToDelete = false
				break
			}
			return nil
		},
	)
	if err != nil {
		return err
	}
	if !safeToDelete {
		return errAttachmentUploadStillReferenced
	}
	var cleanupErrors []error
	reference := AttachmentStoredReference{
		StorageType: intent.TargetStorageType,
		StoreID:     intent.TargetStoreID,
		Key:         intent.FinalKey,
	}
	if stored != nil {
		if stored.StoreID != intent.TargetStoreID ||
			stored.StorageType != intent.TargetStorageType ||
			stored.Key != intent.FinalKey {
			return ErrInvalidAttachment
		}
		reference.VersionID = stored.VersionID
	}
	if err := deleteAttachmentStoredObject(
		cleanupContext,
		s.attachmentStorage,
		reference,
	); err != nil {
		cleanupErrors = append(
			cleanupErrors,
			fmt.Errorf(
				"delete obsolete attachment final object: %w",
				err,
			),
		)
	}
	if err := s.attachmentStaging.DeleteStaged(
		cleanupContext,
		intent.StagingKey,
	); err != nil {
		cleanupErrors = append(
			cleanupErrors,
			fmt.Errorf(
				"delete obsolete attachment staging object: %w",
				err,
			),
		)
	}
	return errors.Join(cleanupErrors...)
}

// ExecuteAttachmentStagingCleanupOutbox reclaims a durable upload intent that
// did not reach its final database transaction before the cleanup deadline.
// The first transaction fences the finalizer by changing staging_intent to
// staging_cleanup; object deletion then happens without a database lock, and a
// final short transaction removes only the still-fenced placeholder.
func (s *AgentNativeService) ExecuteAttachmentStagingCleanupOutbox(
	ctx context.Context,
	attachmentID uint,
) error {
	if s == nil || s.db == nil || s.attachmentStaging == nil {
		return ErrAttachmentStorageMissing
	}
	if attachmentID == 0 {
		return ErrInvalidAttachment
	}
	if err := requireExternalIOOutsideProjectTransaction(
		ctx,
		"attachment staging cleanup worker",
	); err != nil {
		return err
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil ||
		operation.Source != SourceProtocolWorker ||
		operation.Actor.Type != models.ActorTypeSystem {
		return ErrInvalidActor
	}

	var stagingKey string
	shouldCleanup := false
	err = scopeddb.WithProjectScopeContextTransaction(
		ctx,
		s.db,
		operation.Scope,
		func(scopedContext context.Context) error {
			if revalidateErr :=
				s.revalidateAttachmentCleanupAuthorizationInTransaction(
					scopedContext,
				); revalidateErr != nil {
				return revalidateErr
			}
			var attachment models.TicketAttachment
			// Ticket deletion preserves this private upload placeholder until
			// the delayed cleanup becomes due. The exact scope, ID, state and
			// key fences below keep the worker away from finalized attachments.
			queryErr := s.db.WithContext(scopedContext).
				Clauses(clause.Locking{Strength: "UPDATE"}).
				Where(
					"id = ? AND organization_id = ? AND project_id = ?",
					attachmentID,
					operation.Scope.OrganizationID,
					operation.Scope.ProjectID,
				).
				Take(&attachment).Error
			if errors.Is(queryErr, gorm.ErrRecordNotFound) {
				return nil
			}
			if queryErr != nil {
				return queryErr
			}
			switch attachment.StorageType {
			case attachmentStagingIntentStorageType:
				stagingKey = attachment.StoragePath
				result := s.db.WithContext(scopedContext).
					Model(&models.TicketAttachment{}).
					Where(
						"id = ? AND organization_id = ? AND project_id = ? AND storage_type = ? AND storage_path = ?",
						attachment.ID,
						operation.Scope.OrganizationID,
						operation.Scope.ProjectID,
						attachmentStagingIntentStorageType,
						stagingKey,
					).
					Updates(map[string]any{
						"storage_type": attachmentStagingCleanupStorageType,
						"updated_at":   s.now().UTC(),
					})
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return ErrInvalidAttachment
				}
				shouldCleanup = true
			case attachmentStagingCleanupStorageType:
				stagingKey = attachment.StoragePath
				shouldCleanup = true
			default:
				// The final business transaction already owns this object.
				// Its attachment_upload delivery, not the orphan sweeper,
				// controls the remaining lifecycle.
			}
			return nil
		},
	)
	if err != nil || !shouldCleanup {
		return err
	}
	if !validAttachmentStagingKey(stagingKey) {
		return ErrInvalidAttachment
	}
	if err := s.attachmentStaging.DeleteStaged(
		ctx,
		stagingKey,
	); err != nil {
		return err
	}
	return scopeddb.WithProjectScopeContextTransaction(
		ctx,
		s.db,
		operation.Scope,
		func(scopedContext context.Context) error {
			if revalidateErr :=
				s.revalidateAttachmentCleanupAuthorizationInTransaction(
					scopedContext,
				); revalidateErr != nil {
				return revalidateErr
			}
			// Once the staged object is gone, physically remove only the
			// still-fenced placeholder so a completed intent cannot be
			// rediscovered or retain a stale storage key.
			result := s.db.WithContext(scopedContext).
				Where(
					"id = ? AND organization_id = ? AND project_id = ? AND storage_type = ? AND storage_path = ?",
					attachmentID,
					operation.Scope.OrganizationID,
					operation.Scope.ProjectID,
					attachmentStagingCleanupStorageType,
					stagingKey,
				).
				Delete(&models.TicketAttachment{})
			return result.Error
		},
	)
}

func validateAttachmentUploadIntent(
	attachment models.TicketAttachment,
) error {
	if attachment.ID == 0 ||
		attachment.TicketID == 0 ||
		attachment.FileSize <= 0 ||
		len(attachment.Hash) != 64 ||
		strings.TrimSpace(attachment.FileName) == "" {
		return ErrInvalidAttachment
	}
	if attachment.StorageType == "staging" &&
		attachment.StoragePath !=
			attachmentStagingKey(attachment.FileName) {
		return ErrInvalidAttachment
	}
	return nil
}

func attachmentStagingKey(fileName string) string {
	return ".staging/" + filepath.Base(
		strings.TrimSpace(fileName),
	)
}
