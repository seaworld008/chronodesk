package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type DeleteTicketCommand struct {
	TicketID        uint
	ExpectedVersion uint64
	Actor           models.ActorRef
	SourceProtocol  string
	TraceID         string
	CorrelationID   string
	CausationID     string
}

// DeleteTicket atomically tombstones one version-bound Ticket, removes its
// dependent human-facing rows and commits the durable cleanup manifest with
// the deletion Domain Event.
func (s *AgentNativeService) DeleteTicket(
	ctx context.Context,
	command DeleteTicketCommand,
) (OperationReceipt, error) {
	operation, err := commandOperationContext(ctx, command.Actor)
	if err != nil {
		return OperationReceipt{}, err
	}
	projectScope := operation.Scope
	if err := command.Actor.Validate(); err != nil {
		return OperationReceipt{}, fmt.Errorf("%w: %v", ErrInvalidActor, err)
	}
	if command.Actor.Type == models.ActorTypeServicePrincipal {
		return OperationReceipt{}, fmt.Errorf("%w: Agent ticket deletion is not allowed", ErrPolicyDenied)
	}
	if command.TicketID == 0 || command.ExpectedVersion == 0 {
		return OperationReceipt{}, ErrVersionConflict
	}
	var receipt OperationReceipt
	err = transactionForContext(ctx, s.db, func(tx *gorm.DB) error {
		var ticket models.Ticket
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(
				"id = ? AND organization_id = ? AND project_id = ?",
				command.TicketID,
				projectScope.OrganizationID,
				projectScope.ProjectID,
			).
			First(&ticket).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("ticket not found")
			}
			return fmt.Errorf("lock ticket for deletion: %w", err)
		}
		if ticket.Version != command.ExpectedVersion {
			return ErrVersionConflict
		}
		nextVersion := command.ExpectedVersion + 1
		update := tx.Model(&models.Ticket{}).
			Where(
				"id = ? AND organization_id = ? AND project_id = ? AND version = ?",
				ticket.ID,
				projectScope.OrganizationID,
				projectScope.ProjectID,
				command.ExpectedVersion,
			).
			Updates(map[string]any{
				"version":    nextVersion,
				"updated_at": s.now(),
			})
		if update.Error != nil {
			return fmt.Errorf("prepare ticket deletion: %w", update.Error)
		}
		if update.RowsAffected != 1 {
			return ErrVersionConflict
		}
		ticket.Version = nextVersion

		var attachments []models.TicketAttachment
		if err := tx.
			Select("id", "ticket_id", "storage_path").
			Where(
				"ticket_id = ? AND organization_id = ? AND project_id = ?",
				ticket.ID,
				projectScope.OrganizationID,
				projectScope.ProjectID,
			).
			Order("id ASC").
			Find(&attachments).Error; err != nil {
			return fmt.Errorf("load ticket attachment cleanup manifest: %w", err)
		}
		cleanupTargets := make([]OutboxTarget, 0, len(attachments))
		cleanupObjects := make([]AttachmentCleanupObject, 0, len(attachments))
		for i := range attachments {
			target, err := NewAttachmentCleanupOutboxTarget(
				attachments[i].ID,
				attachments[i].StoragePath,
			)
			if err != nil {
				return fmt.Errorf(
					"prepare attachment %d cleanup: %w",
					attachments[i].ID,
					err,
				)
			}
			cleanupTargets = append(cleanupTargets, target)
			cleanupObjects = append(cleanupObjects, AttachmentCleanupObject{
				AttachmentID: attachments[i].ID,
				TicketID:     ticket.ID,
				StoragePath:  attachments[i].StoragePath,
			})
		}
		if err := tx.Where(
			"related_ticket_id = ? AND organization_id = ? AND project_id = ?",
			ticket.ID,
			projectScope.OrganizationID,
			projectScope.ProjectID,
		).
			Delete(&models.Notification{}).Error; err != nil {
			return fmt.Errorf("delete ticket notifications: %w", err)
		}
		if err := tx.Where(
			"ticket_id = ? AND organization_id = ? AND project_id = ?",
			ticket.ID,
			projectScope.OrganizationID,
			projectScope.ProjectID,
		).
			Delete(&models.TicketHistory{}).Error; err != nil {
			return fmt.Errorf("delete ticket histories: %w", err)
		}
		if err := tx.Where(
			"ticket_id = ? AND organization_id = ? AND project_id = ?",
			ticket.ID,
			projectScope.OrganizationID,
			projectScope.ProjectID,
		).
			Delete(&models.TicketAttachment{}).Error; err != nil {
			return fmt.Errorf("delete ticket attachments: %w", err)
		}
		if err := tx.Where(
			"ticket_id = ? AND organization_id = ? AND project_id = ?",
			ticket.ID,
			projectScope.OrganizationID,
			projectScope.ProjectID,
		).
			Delete(&models.TicketComment{}).Error; err != nil {
			return fmt.Errorf("delete ticket comments: %w", err)
		}
		if err := tx.Delete(&ticket).Error; err != nil {
			return fmt.Errorf("delete ticket: %w", err)
		}
		eventData := map[string]any{
			"ticket_id":                ticket.ID,
			"deleted":                  true,
			"attachment_cleanup_count": len(cleanupTargets),
		}
		if len(cleanupObjects) > 0 {
			eventData[AttachmentCleanupObjectsDataField] = cleanupObjects
		}
		event, err := s.AppendDomainEventWithAdditionalTargetsTx(
			ctx,
			tx,
			DomainEventInput{
				Type:            eventcontract.TicketDeletedEventType,
				Subject:         fmt.Sprintf("ticket/%d", ticket.ID),
				Actor:           command.Actor,
				ResourceVersion: ticket.Version,
				TraceID:         command.TraceID,
				CorrelationID:   command.CorrelationID,
				CausationID:     command.CausationID,
				Data:            eventData,
			},
			cleanupTargets,
		)
		if err != nil {
			return err
		}
		receipt = OperationReceipt{
			OperationID:     newNativeID(),
			ResourceID:      strconv.FormatUint(uint64(ticket.ID), 10),
			ResourceVersion: ticket.Version,
			EventID:         event.ID,
			ChangedFields:   []string{"deleted"},
		}
		return nil
	})
	return receipt, err
}

// BulkUpdateHumanTickets applies one human batch as a single transaction. Each
// Ticket keeps its own optimistic version, history, Domain Event, notification
// fan-out and Operation semantics; one failure rolls back the complete batch.
func (s *AgentNativeService) BulkUpdateHumanTickets(
	ctx context.Context,
	request *BulkUpdateRequest,
	userID uint,
) (*BulkUpdateResult, error) {
	if request == nil || len(request.Tickets) == 0 {
		return nil, fmt.Errorf("%w: no ticket versions provided", ErrInvalidBulkTicketUpdate)
	}
	if userID == 0 {
		return nil, fmt.Errorf("%w: human actor is required", ErrInvalidBulkTicketUpdate)
	}
	actor := models.HumanActor(userID)
	operation, err := commandOperationContext(ctx, actor)
	if err != nil {
		return nil, err
	}
	projectScope := operation.Scope
	preconditions, err := normalizedBulkTicketPreconditions(request.Tickets)
	if err != nil {
		return nil, err
	}
	changes, changedFields, err := bulkTicketChanges(request)
	if err != nil {
		return nil, err
	}
	if request.AssignedToID != nil {
		assignee := models.HumanActor(*request.AssignedToID)
		resolved, resolveErr := s.ResolveTicketAssignmentChanges(ctx, &assignee)
		if resolveErr != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidBulkTicketUpdate, resolveErr)
		}
		for field, value := range resolved {
			changes[field] = value
		}
	}
	result := &BulkUpdateResult{
		Tickets: make([]TicketVersionReceipt, 0, len(preconditions)),
	}
	err = transactionForContext(ctx, s.db, func(tx *gorm.DB) error {
		for _, precondition := range preconditions {
			var ticket models.Ticket
			if err := tx.Where(
				"id = ? AND organization_id = ? AND project_id = ?",
				precondition.ID,
				projectScope.OrganizationID,
				projectScope.ProjectID,
			).First(&ticket).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return fmt.Errorf(
						"%w: ticket %d not found",
						ErrInvalidBulkTicketUpdate,
						precondition.ID,
					)
				}
				return fmt.Errorf("load bulk ticket %d: %w", precondition.ID, err)
			}
			if ticket.Version == 0 || ticket.Version != precondition.Version {
				return fmt.Errorf(
					"%w: ticket %d expected version %d, current version %d",
					ErrVersionConflict,
					precondition.ID,
					precondition.Version,
					ticket.Version,
				)
			}
			if err := validateTicketChangeSemantics(
				ctx,
				tx,
				projectScope,
				&ticket,
				changes,
				actor,
			); err != nil {
				return fmt.Errorf("ticket %d: %w", precondition.ID, err)
			}
			beforeTicket := ticket
			changeSet := make(map[string]any, len(changedFields))
			for _, field := range changedFields {
				changeSet[field] = map[string]any{
					"old": ticketFieldValue(&ticket, field),
					"new": changes[field],
				}
			}

			writeChanges := cloneBulkTicketChanges(changes)
			now := s.now()
			if rawStatus, ok := writeChanges["status"]; ok {
				nextStatus := models.TicketStatus(fmt.Sprint(rawStatus))
				applyTicketStatusTimestamps(&ticket, nextStatus, now)
				writeChanges["resolved_at"] = ticket.ResolvedAt
				writeChanges["closed_at"] = ticket.ClosedAt
			}
			writeChanges["version"] = precondition.Version + 1
			writeChanges["updated_at"] = now
			update := tx.Model(&models.Ticket{}).
				Where(
					"id = ? AND organization_id = ? AND project_id = ? AND version = ?",
					precondition.ID,
					projectScope.OrganizationID,
					projectScope.ProjectID,
					precondition.Version,
				).
				Updates(writeChanges)
			if update.Error != nil {
				return fmt.Errorf("bulk update ticket %d: %w", precondition.ID, update.Error)
			}
			if update.RowsAffected != 1 {
				return fmt.Errorf(
					"%w: ticket %d expected version %d",
					ErrVersionConflict,
					precondition.ID,
					precondition.Version,
				)
			}
			if err := tx.Where(
				"id = ? AND organization_id = ? AND project_id = ?",
				precondition.ID,
				projectScope.OrganizationID,
				projectScope.ProjectID,
			).First(&ticket).Error; err != nil {
				return fmt.Errorf("reload bulk ticket %d: %w", precondition.ID, err)
			}

			details, err := json.Marshal(map[string]any{
				"bulk":           true,
				"changed_fields": changedFields,
				"changes":        changeSet,
				"old_version":    precondition.Version,
				"new_version":    ticket.Version,
			})
			if err != nil {
				return fmt.Errorf("encode bulk ticket %d audit: %w", precondition.ID, err)
			}
			metadata, err := json.Marshal(map[string]any{
				"bulk":            true,
				"source_protocol": "rest-human",
			})
			if err != nil {
				return fmt.Errorf("encode bulk ticket %d metadata: %w", precondition.ID, err)
			}
			actorUserID := userID
			history := &models.TicketHistory{
				TicketID:    ticket.ID,
				UserID:      &actorUserID,
				ActorType:   actor.Type,
				ActorID:     actor.ID,
				Action:      historyActionForChanges(changedFields),
				Description: "批量更新工单",
				Details:     string(details),
				FieldName:   bulkHistoryFieldName(changedFields),
				IsVisible:   true,
				IsImportant: true,
				Metadata:    string(metadata),
			}
			if len(changedFields) == 1 {
				history.OldValue = bulkAuditValue(
					ticketFieldValueBeforeChange(changeSet, changedFields[0], "old"),
				)
				history.NewValue = bulkAuditValue(
					ticketFieldValueBeforeChange(changeSet, changedFields[0], "new"),
				)
			}
			eventData := map[string]any{
				"ticket_id":      ticket.ID,
				"changed_fields": changedFields,
				"changes":        changeSet,
				"old_version":    precondition.Version,
				"new_version":    ticket.Version,
				"bulk":           true,
				"status":         ticket.Status,
			}
			notificationTargets := ticketUpdateNotificationTargets(
				&beforeTicket,
				&ticket,
				actor,
				changedFields,
			)
			if len(notificationTargets) > 0 {
				addTicketNotificationEventSnapshot(eventData, &ticket)
				if beforeTicket.Status != ticket.Status {
					eventData["old_status"] = beforeTicket.Status
					eventData["new_status"] = ticket.Status
				}
				if ticket.AssignedToID != nil {
					eventData["assigned_to_id"] = *ticket.AssignedToID
				}
			}
			event, err := s.AppendDomainEventWithAdditionalTargetsTx(
				ctx,
				tx,
				DomainEventInput{
					Type:            bulkTicketEventType(changedFields),
					Subject:         fmt.Sprintf("ticket/%d", ticket.ID),
					Actor:           actor,
					ResourceVersion: ticket.Version,
					Data:            eventData,
				},
				notificationTargets,
			)
			if err != nil {
				return fmt.Errorf("append bulk ticket %d event: %w", precondition.ID, err)
			}
			if err := linkTicketHistoryToDomainEvent(history, event); err != nil {
				return fmt.Errorf("link bulk ticket %d history: %w", precondition.ID, err)
			}
			if err := tx.Create(history).Error; err != nil {
				return fmt.Errorf("create bulk ticket %d history: %w", precondition.ID, err)
			}
			result.Tickets = append(result.Tickets, TicketVersionReceipt{
				ID:      ticket.ID,
				Version: ticket.Version,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
