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

type TicketRelationDirection string

const (
	TicketRelationDirectionOutgoing TicketRelationDirection = "outgoing"
	TicketRelationDirectionIncoming TicketRelationDirection = "incoming"
)

// TicketRelationDirectoryItem keeps the immutable relation together with the
// project-local ticket projection needed by human list views. The projection
// is loaded in one bounded query for the current page so callers never need an
// N+1 lookup or expose a bare database identifier to operators.
type TicketRelationDirectoryItem struct {
	Relation            models.TicketRelation
	Direction           TicketRelationDirection
	RelatedTicketID     uint
	RelatedTicketNumber string
	RelatedTicketTitle  string
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
	request DirectoryPageRequest,
) (*DirectoryPage[models.EntityLink], error) {
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return nil, err
	}
	if ticketID == 0 || validateDirectoryPageRequest(
		request,
		map[string]struct{}{"created_at": {}},
	) != nil {
		return nil, ErrDirectoryListQuery
	}
	page := &DirectoryPage[models.EntityLink]{
		Items:    make([]models.EntityLink, 0, request.PageSize),
		Page:     request.Page,
		PageSize: request.PageSize,
	}
	if err := runProjectOperation(ctx, service.db, func(projectCtx context.Context) error {
		query := service.db.WithContext(projectCtx).
			Model(&models.EntityLink{}).
			Where(
				"organization_id = ? AND project_id = ? AND ticket_id = ?",
				scope.OrganizationID,
				scope.ProjectID,
				ticketID,
			)
		if countErr := query.Count(&page.Total).Error; countErr != nil {
			return countErr
		}
		return query.
			Order(ticketRelationshipDirectoryOrder(request)).
			Offset(directoryPageOffset(request)).
			Limit(request.PageSize).
			Find(&page.Items).Error
	}); err != nil {
		return nil, err
	}
	page.TotalPages = directoryTotalPages(page.Total, request.PageSize)
	return page, nil
}

func (service *TicketRelationshipService) ListTicketRelations(
	ctx context.Context,
	ticketID uint,
	request DirectoryPageRequest,
) (*DirectoryPage[TicketRelationDirectoryItem], error) {
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return nil, err
	}
	if ticketID == 0 || validateDirectoryPageRequest(
		request,
		map[string]struct{}{"created_at": {}},
	) != nil {
		return nil, ErrDirectoryListQuery
	}
	page := &DirectoryPage[TicketRelationDirectoryItem]{
		Items:    make([]TicketRelationDirectoryItem, 0, request.PageSize),
		Page:     request.Page,
		PageSize: request.PageSize,
	}
	if err := runProjectOperation(ctx, service.db, func(projectCtx context.Context) error {
		query := service.db.WithContext(projectCtx).
			Model(&models.TicketRelation{}).
			Where(
				"organization_id = ? AND project_id = ? AND (source_ticket_id = ? OR target_ticket_id = ?)",
				scope.OrganizationID,
				scope.ProjectID,
				ticketID,
				ticketID,
			)
		if countErr := query.Count(&page.Total).Error; countErr != nil {
			return countErr
		}
		relations := make([]models.TicketRelation, 0, request.PageSize)
		if findErr := query.
			Order(ticketRelationshipDirectoryOrder(request)).
			Offset(directoryPageOffset(request)).
			Limit(request.PageSize).
			Find(&relations).Error; findErr != nil {
			return findErr
		}
		if len(relations) == 0 {
			return nil
		}
		relatedIDs := make([]uint, 0, len(relations))
		seenIDs := make(map[uint]struct{}, len(relations))
		for index := range relations {
			relatedID := relations[index].TargetTicketID
			if relatedID == ticketID {
				relatedID = relations[index].SourceTicketID
			}
			if _, seen := seenIDs[relatedID]; seen {
				continue
			}
			seenIDs[relatedID] = struct{}{}
			relatedIDs = append(relatedIDs, relatedID)
		}
		type relatedTicketProjection struct {
			ID           uint
			TicketNumber string
			Title        string
		}
		var projections []relatedTicketProjection
		if projectionErr := service.db.WithContext(projectCtx).
			Model(&models.Ticket{}).
			Select("id", "ticket_number", "title").
			Where(
				"organization_id = ? AND project_id = ? AND id IN ?",
				scope.OrganizationID,
				scope.ProjectID,
				relatedIDs,
			).
			Find(&projections).Error; projectionErr != nil {
			return projectionErr
		}
		byID := make(map[uint]relatedTicketProjection, len(projections))
		for index := range projections {
			byID[projections[index].ID] = projections[index]
		}
		for index := range relations {
			relation := relations[index]
			direction := TicketRelationDirectionOutgoing
			relatedID := relation.TargetTicketID
			if relation.TargetTicketID == ticketID {
				direction = TicketRelationDirectionIncoming
				relatedID = relation.SourceTicketID
			}
			related, exists := byID[relatedID]
			if !exists {
				return fmt.Errorf(
					"related project ticket %d is unavailable",
					relatedID,
				)
			}
			page.Items = append(page.Items, TicketRelationDirectoryItem{
				Relation:            relation,
				Direction:           direction,
				RelatedTicketID:     related.ID,
				RelatedTicketNumber: related.TicketNumber,
				RelatedTicketTitle:  related.Title,
			})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	page.TotalPages = directoryTotalPages(page.Total, request.PageSize)
	return page, nil
}

func ticketRelationshipDirectoryOrder(request DirectoryPageRequest) string {
	if request.SortOrder == "asc" {
		return "created_at ASC, id ASC"
	}
	return "created_at DESC, id DESC"
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
