package services

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	attachmentStagingIntentStorageType  = "staging_intent"
	attachmentStagingCleanupStorageType = "staging_cleanup"
	attachmentStagingCleanupDelay       = 15 * time.Minute
)

func (s *AgentNativeService) registerAttachmentStagingIntent(
	ctx context.Context,
	operation OperationContext,
	input NativeAttachmentInput,
	initialAccess *ProjectAccess,
	policyCheck PolicyCheckInput,
	attachment *models.TicketAttachment,
) error {
	if s == nil || s.db == nil ||
		initialAccess == nil ||
		attachment == nil ||
		attachment.TicketID == 0 ||
		!validAttachmentStagingKey(attachment.StoragePath) ||
		attachment.StorageType != attachmentStagingIntentStorageType {
		return ErrInvalidAttachment
	}
	cleanupDeadline := s.now().UTC().Add(
		attachmentStagingCleanupDelay,
	)
	return scopeddb.WithProjectScopeContextTransaction(
		ctx,
		s.db,
		operation.Scope,
		func(scopedContext context.Context) error {
			currentAccess, err :=
				s.revalidateAttachmentAuthorizationInTransaction(
					scopedContext,
					models.ScopeAttachmentsWrite,
				)
			if err != nil {
				return fmt.Errorf(
					"revalidate attachment staging authorization: %w",
					err,
				)
			}
			if !initialAccess.AuthorizationSnapshot.Matches(
				currentAccess.AuthorizationSnapshot,
			) {
				return fmt.Errorf(
					"attachment staging authorization changed: %w",
					ErrProjectAccessDenied,
				)
			}
			if _, err = s.validateAttachmentPolicyDecisionsInTransaction(
				scopedContext,
				operation,
				input,
				policyCheck,
			); err != nil {
				return err
			}
			attachment.UploadedBy, err = s.humanUserProjection(
				scopedContext,
				input.Actor,
			)
			if err != nil {
				return err
			}
			return transactionForContext(
				scopedContext,
				s.db,
				func(tx *gorm.DB) error {
					if _, err := s.validateAttachmentCommandStateTx(
						scopedContext,
						tx,
						currentAccess,
						operation,
						input,
					); err != nil {
						return fmt.Errorf(
							"validate attachment staging target: %w",
							err,
						)
					}
					if err := tx.Create(attachment).Error; err != nil {
						return fmt.Errorf(
							"create attachment staging intent: %w",
							err,
						)
					}
					destinationID := strconv.FormatUint(
						uint64(attachment.ID),
						10,
					)
					event, err := s.
						AppendDomainEventWithAdditionalTargetsTx(
							scopedContext,
							tx,
							DomainEventInput{
								Type: "io.chronodesk.ticket.attachment." +
									"upload-intent-registered.v1",
								Subject: fmt.Sprintf(
									"ticket/%d",
									input.TicketID,
								),
								Actor:           input.Actor,
								ResourceVersion: input.ExpectedVersion,
								TraceID:         input.TraceID,
								CorrelationID:   input.CorrelationID,
								Data: map[string]any{
									"ticket_id": input.TicketID,
									"attachment_id": attachment.
										ID,
									"file_name": attachment.
										OriginalName,
									"cleanup_after": cleanupDeadline.
										Format(time.RFC3339Nano),
									"content_untrusted": true,
								},
							},
							[]OutboxTarget{{
								Type:        AttachmentStagingCleanupOutboxDestination,
								ID:          destinationID,
								MaxAttempts: 8,
							}},
						)
					if err != nil {
						return err
					}
					schedule := tx.Model(
						&models.OutboxDelivery{},
					).Where(
						"event_id = ? AND destination_type = ? AND destination_id = ?",
						event.ID,
						AttachmentStagingCleanupOutboxDestination,
						destinationID,
					).Updates(map[string]any{
						"next_attempt_at": cleanupDeadline,
						"updated_at":      s.now().UTC(),
					})
					if schedule.Error != nil {
						return schedule.Error
					}
					if schedule.RowsAffected != 1 {
						return errors.New(
							"attachment staging cleanup delivery is missing",
						)
					}
					return nil
				},
			)
		},
	)
}

func (s *AgentNativeService) validateAttachmentPolicyDecisionsInTransaction(
	ctx context.Context,
	operation OperationContext,
	input NativeAttachmentInput,
	policyCheck PolicyCheckInput,
) (bool, error) {
	if input.Actor.Type != models.ActorTypeServicePrincipal {
		return true, nil
	}
	decision, err := s.loadMatchingPolicyDecision(
		ctx,
		strings.TrimSpace(input.PolicyDecisionID),
		input.Actor,
		policyCheck,
		true,
	)
	if err != nil {
		return false, err
	}
	if !decision.Allowed {
		return false, ErrPolicyDenied
	}
	externalCheck := externalNotificationPolicyCheck(
		input.Actor,
		input.CredentialID,
		policyCheck.ResourceID,
		input.RequestDigest,
		string(operation.Source),
	)
	externalDecision, err := s.loadMatchingPolicyDecision(
		ctx,
		strings.TrimSpace(
			input.ExternalNotificationPolicyDecisionID,
		),
		input.Actor,
		externalCheck,
		true,
	)
	if err != nil {
		return false, err
	}
	return externalDecision.Allowed, nil
}

func (s *AgentNativeService) validateAttachmentCommandStateTx(
	ctx context.Context,
	tx *gorm.DB,
	access *ProjectAccess,
	operation OperationContext,
	input NativeAttachmentInput,
) (*models.Ticket, error) {
	if tx == nil {
		return nil, errors.New(
			"attachment command transaction is required",
		)
	}
	ticket, err := lockTicketForLeaseTx(
		tx.WithContext(ctx),
		operation.Scope,
		input.TicketID,
	)
	if err != nil {
		return nil, err
	}
	if input.Actor.Type == models.ActorTypeHuman {
		if err := authorizeHumanAttachmentTicket(
			access,
			operation,
			*ticket,
			true,
			input.IsPublic,
		); err != nil {
			return nil, err
		}
	}
	if ticket.Version != input.ExpectedVersion {
		return nil, fmt.Errorf(
			"%w: expected %d, actual %d",
			ErrVersionConflict,
			input.ExpectedVersion,
			ticket.Version,
		)
	}
	if input.LeaseID != "" {
		if _, err := s.validateTicketLeaseTx(
			tx.WithContext(ctx),
			input.LeaseID,
			input.TicketID,
			input.Actor,
			input.ExpectedVersion,
		); err != nil {
			return nil, err
		}
	}
	if input.CommentID != nil {
		var comment models.TicketComment
		err := tx.WithContext(ctx).
			Clauses(clause.Locking{Strength: "SHARE"}).
			Where(
				"id = ? AND ticket_id = ? AND organization_id = ? AND project_id = ? AND is_deleted = ?",
				*input.CommentID,
				input.TicketID,
				operation.Scope.OrganizationID,
				operation.Scope.ProjectID,
				false,
			).
			Take(&comment).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrProjectAccessDenied
		}
		if err != nil {
			return nil, err
		}
		if input.Actor.Type == models.ActorTypeHuman &&
			access.Role == models.ProjectRoleRequester &&
			comment.Type != models.CommentTypePublic {
			return nil, ErrProjectAccessDenied
		}
	}
	return ticket, nil
}

func completeAttachmentStagingCleanupIntentTx(
	tx *gorm.DB,
	attachmentID uint,
	now time.Time,
) error {
	destinationID := strconv.FormatUint(
		uint64(attachmentID),
		10,
	)
	result := tx.Model(&models.OutboxDelivery{}).
		Where(
			"destination_type = ? AND destination_id = ? AND status IN ?",
			AttachmentStagingCleanupOutboxDestination,
			destinationID,
			[]models.OutboxDeliveryStatus{
				models.OutboxDeliveryPending,
				models.OutboxDeliveryFailed,
			},
		).
		Updates(map[string]any{
			"status":       models.OutboxDeliverySucceeded,
			"delivered_at": now,
			"locked_at":    nil,
			"locked_by":    "",
			"last_error":   "",
			"updated_at":   now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New(
			"attachment staging cleanup delivery is unavailable",
		)
	}
	return nil
}

// revalidateAttachmentCleanupAuthorizationInTransaction is intentionally
// narrower than normal worker authorization. It permits only the trusted
// Outbox worker to reclaim storage in an active or archived project while
// retaining the exact organization/project RLS binding. It does not grant
// Human or Service Principal business access to archived projects.
func (s *AgentNativeService) revalidateAttachmentCleanupAuthorizationInTransaction(
	ctx context.Context,
) error {
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return err
	}
	if operation.Actor !=
		models.SystemActor(outboxSystemActorID) ||
		operation.Source != SourceProtocolWorker {
		return ErrInvalidActor
	}
	if err := requireExactProjectAuthorizationTransaction(
		ctx,
		operation.Scope,
	); err != nil {
		return err
	}
	var project models.Project
	err = s.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "SHARE"}).
		Where(
			"id = ? AND organization_id = ? AND status IN ?",
			operation.Scope.ProjectID,
			operation.Scope.OrganizationID,
			[]models.ProjectStatus{
				models.ProjectStatusActive,
				models.ProjectStatusArchived,
			},
		).
		Take(&project).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrProjectAccessDenied
	}
	return err
}
