package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

var errTicketWorkflowCandidateNotAllowed = fmt.Errorf(
	"%w: ticket workflow candidate transition is not allowed",
	ErrInvalidTicketTransition,
)

// validateTicketWorkflowTransitionTx makes the immutable workflow version
// stored on the Ticket authoritative for lifecycle transitions. The shared
// canonical TicketStatus remains a reporting projection; projects may name
// workflow states differently while mapping them to the six lifecycle
// categories.
func validateTicketWorkflowTransitionTx(
	ctx context.Context,
	tx *gorm.DB,
	scope models.ProjectScope,
	ticket *models.Ticket,
	next models.TicketStatus,
	actor models.ActorRef,
) error {
	if tx == nil || ticket == nil {
		return errors.New("ticket workflow transition context is required")
	}
	if !next.IsValid() {
		return fmt.Errorf(
			"%w: invalid target status %q",
			ErrInvalidTicketTransition,
			next,
		)
	}
	if ticket.Status == next {
		return nil
	}
	if strings.TrimSpace(ticket.WorkflowVersionID) == "" {
		return fmt.Errorf(
			"%w: ticket has no workflow version",
			ErrInvalidTicketTransition,
		)
	}

	var workflow models.WorkflowVersion
	if err := scopedConfigurationQuery(tx.WithContext(ctx), scope).
		Where(
			"id = ? AND status = ?",
			ticket.WorkflowVersionID,
			models.ConfigurationStatusPublished,
		).
		First(&workflow).Error; err != nil {
		return fmt.Errorf(
			"%w: load ticket workflow: %w",
			ErrInvalidTicketTransition,
			err,
		)
	}
	if err := workflow.Validate(); err != nil {
		return fmt.Errorf(
			"%w: invalid ticket workflow configuration: %v",
			ErrInvalidTicketTransition,
			err,
		)
	}
	states, err := workflow.StateDefinitions()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTicketTransition, err)
	}
	transitions, err := workflow.TransitionDefinitions()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTicketTransition, err)
	}
	stateStatuses := make(map[string]models.TicketStatus, len(states))
	for _, state := range states {
		status, statusErr := ticketStatusForLifecycleCategory(
			state.LifecycleCategory,
		)
		if statusErr != nil {
			return fmt.Errorf(
				"%w: %v",
				ErrInvalidTicketTransition,
				statusErr,
			)
		}
		stateStatuses[state.Key] = status
	}
	var (
		role         models.ProjectRole
		roleResolved bool
	)
	for _, transition := range transitions {
		if stateStatuses[transition.From] != ticket.Status ||
			stateStatuses[transition.To] != next {
			continue
		}
		if actor.Type == models.ActorTypeSystem ||
			len(transition.Roles) == 0 {
			return nil
		}
		if !roleResolved {
			role, err = workflowActorProjectRoleTx(
				ctx,
				tx,
				scope,
				actor,
			)
			if err != nil {
				return err
			}
			roleResolved = true
		}
		if projectRoleAllowed(role, transition.Roles) {
			return nil
		}
	}
	return fmt.Errorf(
		"%w: workflow %s does not allow %s to %s",
		errTicketWorkflowCandidateNotAllowed,
		workflow.ID,
		ticket.Status,
		next,
	)
}

func ticketStatusForLifecycleCategory(
	category models.LifecycleCategory,
) (models.TicketStatus, error) {
	switch category {
	case models.LifecycleCategoryNew:
		return models.TicketStatusOpen, nil
	case models.LifecycleCategoryActive:
		return models.TicketStatusInProgress, nil
	case models.LifecycleCategoryWaiting:
		return models.TicketStatusPending, nil
	case models.LifecycleCategoryResolved:
		return models.TicketStatusResolved, nil
	case models.LifecycleCategoryClosed:
		return models.TicketStatusClosed, nil
	case models.LifecycleCategoryCancelled:
		return models.TicketStatusCancelled, nil
	default:
		return "", fmt.Errorf("unsupported lifecycle category %q", category)
	}
}

func workflowActorProjectRoleTx(
	ctx context.Context,
	tx *gorm.DB,
	scope models.ProjectScope,
	actor models.ActorRef,
) (models.ProjectRole, error) {
	switch actor.Type {
	case models.ActorTypeSystem:
		return "", nil
	case models.ActorTypeHuman:
		userID, err := humanActorID(actor)
		if err != nil {
			return "", err
		}
		var membership models.ProjectMembership
		if err := tx.WithContext(ctx).
			Select("role").
			Where(
				"project_id = ? AND user_id = ? AND is_active = ?",
				scope.ProjectID,
				userID,
				true,
			).
			First(&membership).Error; err != nil {
			return "", fmt.Errorf(
				"%w: workflow actor has no active project membership: %w",
				ErrInvalidTicketTransition,
				err,
			)
		}
		return membership.Role, nil
	case models.ActorTypeServicePrincipal:
		var grant models.ProjectPrincipalGrant
		if err := tx.WithContext(ctx).
			Select("role", "expires_at").
			Where(
				"project_id = ? AND service_principal_id = ? AND is_active = ?",
				scope.ProjectID,
				actor.ID,
				true,
			).
			First(&grant).Error; err != nil {
			return "", fmt.Errorf(
				"%w: workflow actor has no active project grant: %w",
				ErrInvalidTicketTransition,
				err,
			)
		}
		if grant.ExpiresAt != nil && !grant.ExpiresAt.After(time.Now().UTC()) {
			return "", fmt.Errorf(
				"%w: workflow actor project grant has expired",
				ErrInvalidTicketTransition,
			)
		}
		return grant.Role, nil
	default:
		return "", fmt.Errorf(
			"%w: unsupported workflow actor",
			ErrInvalidTicketTransition,
		)
	}
}

func projectRoleAllowed(
	role models.ProjectRole,
	allowed []models.ProjectRole,
) bool {
	for _, candidate := range allowed {
		if role == candidate {
			return true
		}
	}
	return false
}
