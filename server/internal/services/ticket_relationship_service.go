package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const maxEntityLinkMetadataBytes = 32 << 10

type TicketRelationshipService struct {
	db     *gorm.DB
	native *AgentNativeService
}

func NewTicketRelationshipService(
	db *gorm.DB,
	native *AgentNativeService,
) (*TicketRelationshipService, error) {
	if db == nil || native == nil {
		return nil, errors.New("ticket relationship database and event service are required")
	}
	return &TicketRelationshipService{db: db, native: native}, nil
}

type AddEntityLinkInput struct {
	TicketID        uint
	ExpectedVersion uint64
	Kind            models.EntityKind
	ReferenceID     string
	DisplayName     string
	Metadata        map[string]any
}

type AddEntityLinkResult struct {
	Link          *models.EntityLink `json:"link"`
	TicketVersion uint64             `json:"ticket_version"`
	EventID       string             `json:"event_id"`
}

func (service *TicketRelationshipService) AddEntityLink(
	ctx context.Context,
	input AddEntityLinkInput,
) (*AddEntityLinkResult, error) {
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if input.TicketID == 0 || input.ExpectedVersion == 0 ||
		!input.Kind.IsValid() ||
		strings.TrimSpace(input.ReferenceID) == "" ||
		strings.TrimSpace(input.DisplayName) == "" {
		return nil, errors.New("complete entity link input is required")
	}
	if input.Metadata == nil {
		input.Metadata = map[string]any{}
	}
	metadata, err := json.Marshal(input.Metadata)
	if err != nil || len(metadata) > maxEntityLinkMetadataBytes {
		return nil, errors.New("entity link metadata is invalid or too large")
	}
	link := &models.EntityLink{
		OrganizationID: operation.Scope.OrganizationID,
		ProjectID:      operation.Scope.ProjectID,
		TicketID:       input.TicketID,
		Kind:           input.Kind,
		ReferenceID:    strings.TrimSpace(input.ReferenceID),
		DisplayName:    strings.TrimSpace(input.DisplayName),
		Metadata:       datatypes.JSON(metadata),
		CreatedByType:  operation.Actor.Type,
		CreatedByID:    operation.Actor.ID,
	}
	result := &AddEntityLinkResult{Link: link}
	err = runProjectOperation(ctx, service.db, func(projectCtx context.Context) error {
		return transactionForContext(projectCtx, service.db, func(tx *gorm.DB) error {
			ticket, err := lockedScopedTicket(
				projectCtx,
				tx,
				operation.Scope,
				input.TicketID,
			)
			if err != nil {
				return err
			}
			if ticket.Version != input.ExpectedVersion {
				return ErrVersionConflict
			}
			if err := tx.WithContext(projectCtx).Create(link).Error; err != nil {
				return fmt.Errorf("create entity link: %w", err)
			}
			nextVersion, err := incrementRelatedTicketVersionTx(
				projectCtx,
				tx,
				operation.Scope,
				ticket.ID,
				ticket.Version,
			)
			if err != nil {
				return err
			}
			event, err := service.native.AppendDomainEventTx(
				projectCtx,
				tx,
				DomainEventInput{
					Type:            "io.chronodesk.ticket.entity-linked.v1",
					Subject:         fmt.Sprintf("ticket/%d", ticket.ID),
					Actor:           operation.Actor,
					Scope:           operation.Scope,
					ResourceVersion: nextVersion,
					TraceID:         operation.TraceID,
					CorrelationID:   operation.CorrelationID,
					Data: map[string]any{
						"organization_id": operation.Scope.OrganizationID,
						"project_id":      operation.Scope.ProjectID,
						"ticket_id":       ticket.ID,
						"entity_link_id":  link.ID,
						"entity_kind":     link.Kind,
						"reference_id":    link.ReferenceID,
					},
				},
				nil,
			)
			if err != nil {
				return err
			}
			result.TicketVersion = nextVersion
			result.EventID = event.ID
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

type AddTicketRelationInput struct {
	SourceTicketID  uint
	TargetTicketID  uint
	ExpectedVersion uint64
	Relation        models.TicketRelationType
	Reason          string
}

type AddTicketRelationResult struct {
	Relation      *models.TicketRelation `json:"relation"`
	TicketVersion uint64                 `json:"ticket_version"`
	EventID       string                 `json:"event_id"`
}

func (service *TicketRelationshipService) AddTicketRelation(
	ctx context.Context,
	input AddTicketRelationInput,
) (*AddTicketRelationResult, error) {
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if input.SourceTicketID == 0 || input.TargetTicketID == 0 ||
		input.SourceTicketID == input.TargetTicketID ||
		input.ExpectedVersion == 0 || !input.Relation.IsValid() {
		return nil, errors.New("complete ticket relation input is required")
	}
	relation := &models.TicketRelation{
		OrganizationID: operation.Scope.OrganizationID,
		ProjectID:      operation.Scope.ProjectID,
		SourceTicketID: input.SourceTicketID,
		TargetTicketID: input.TargetTicketID,
		Relation:       input.Relation,
		Reason:         strings.TrimSpace(input.Reason),
		CreatedByType:  operation.Actor.Type,
		CreatedByID:    operation.Actor.ID,
	}
	result := &AddTicketRelationResult{Relation: relation}
	err = runProjectOperation(ctx, service.db, func(projectCtx context.Context) error {
		return transactionForContext(projectCtx, service.db, func(tx *gorm.DB) error {
			source, err := lockedScopedTicket(
				projectCtx,
				tx,
				operation.Scope,
				input.SourceTicketID,
			)
			if err != nil {
				return err
			}
			if source.Version != input.ExpectedVersion {
				return ErrVersionConflict
			}
			if _, err := lockedScopedTicket(
				projectCtx,
				tx,
				operation.Scope,
				input.TargetTicketID,
			); err != nil {
				return err
			}
			if err := tx.WithContext(projectCtx).Create(relation).Error; err != nil {
				return fmt.Errorf("create ticket relation: %w", err)
			}
			nextVersion, err := incrementRelatedTicketVersionTx(
				projectCtx,
				tx,
				operation.Scope,
				source.ID,
				source.Version,
			)
			if err != nil {
				return err
			}
			event, err := service.native.AppendDomainEventTx(
				projectCtx,
				tx,
				DomainEventInput{
					Type:            "io.chronodesk.ticket.relation-created.v1",
					Subject:         fmt.Sprintf("ticket/%d", source.ID),
					Actor:           operation.Actor,
					Scope:           operation.Scope,
					ResourceVersion: nextVersion,
					TraceID:         operation.TraceID,
					CorrelationID:   operation.CorrelationID,
					Data: map[string]any{
						"organization_id":    operation.Scope.OrganizationID,
						"project_id":         operation.Scope.ProjectID,
						"source_ticket_id":   source.ID,
						"target_ticket_id":   input.TargetTicketID,
						"ticket_relation_id": relation.ID,
						"relation":           relation.Relation,
					},
				},
				nil,
			)
			if err != nil {
				return err
			}
			result.TicketVersion = nextVersion
			result.EventID = event.ID
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (service *TicketRelationshipService) ListEntityLinks(
	ctx context.Context,
	ticketID uint,
) ([]models.EntityLink, error) {
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return nil, err
	}
	var links []models.EntityLink
	if err := runProjectOperation(ctx, service.db, func(projectCtx context.Context) error {
		return service.db.WithContext(projectCtx).
			Where(
				"organization_id = ? AND project_id = ? AND ticket_id = ?",
				scope.OrganizationID,
				scope.ProjectID,
				ticketID,
			).
			Order("created_at ASC, id ASC").
			Find(&links).Error
	}); err != nil {
		return nil, err
	}
	return links, nil
}

func (service *TicketRelationshipService) ListTicketRelations(
	ctx context.Context,
	ticketID uint,
) ([]models.TicketRelation, error) {
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return nil, err
	}
	var relations []models.TicketRelation
	if err := runProjectOperation(ctx, service.db, func(projectCtx context.Context) error {
		return service.db.WithContext(projectCtx).
			Where(
				"organization_id = ? AND project_id = ? AND (source_ticket_id = ? OR target_ticket_id = ?)",
				scope.OrganizationID,
				scope.ProjectID,
				ticketID,
				ticketID,
			).
			Order("created_at ASC, id ASC").
			Find(&relations).Error
	}); err != nil {
		return nil, err
	}
	return relations, nil
}

func lockedScopedTicket(
	ctx context.Context,
	tx *gorm.DB,
	scope models.ProjectScope,
	ticketID uint,
) (*models.Ticket, error) {
	var ticket models.Ticket
	if err := tx.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			ticketID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		First(&ticket).Error; err != nil {
		return nil, err
	}
	return &ticket, nil
}

func incrementRelatedTicketVersionTx(
	ctx context.Context,
	tx *gorm.DB,
	scope models.ProjectScope,
	ticketID uint,
	expected uint64,
) (uint64, error) {
	next := expected + 1
	update := tx.WithContext(ctx).Model(&models.Ticket{}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? AND version = ?",
			ticketID,
			scope.OrganizationID,
			scope.ProjectID,
			expected,
		).
		Updates(map[string]any{
			"version": next,
		})
	if update.Error != nil {
		return 0, update.Error
	}
	if update.RowsAffected != 1 {
		return 0, ErrVersionConflict
	}
	return next, nil
}
