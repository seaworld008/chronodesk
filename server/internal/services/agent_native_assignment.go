package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/safeconv"

	"gorm.io/gorm"
)

var (
	// ErrInvalidAssignee identifies an assignment target that cannot be parsed
	// as a valid ActorRef. Protocol adapters map it to an input error.
	ErrInvalidAssignee = errors.New("invalid ticket assignee")
	// ErrAssigneeNotFound identifies a syntactically valid human or service
	// principal target that does not exist.
	ErrAssigneeNotFound = errors.New("ticket assignee not found")
	// ErrAssigneePolicyDenied identifies an existing service principal that
	// cannot receive work because its controls make it unavailable.
	ErrAssigneePolicyDenied = errors.New("ticket assignee denied by policy")
)

// ResolveTicketAssignmentChanges converts a protocol-neutral Assignment target
// into the complete Actor-native Ticket update. A nil target releases the
// Ticket. Public Assignment only accepts human and service-principal targets;
// system is an audit Actor, not an externally assignable target.
func (s *AgentNativeService) ResolveTicketAssignmentChanges(
	ctx context.Context,
	assignee *models.ActorRef,
) (map[string]any, error) {
	if assignee == nil {
		return map[string]any{
			"assigned_to_actor_type":           "",
			"assigned_to_actor_id":             "",
			"assigned_to_id":                   nil,
			"assigned_to_service_principal_id": nil,
		}, nil
	}
	if err := assignee.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAssignee, err)
	}

	changes := map[string]any{
		"assigned_to_actor_type":           assignee.Type,
		"assigned_to_actor_id":             assignee.ID,
		"assigned_to_id":                   nil,
		"assigned_to_service_principal_id": nil,
	}
	switch assignee.Type {
	case models.ActorTypeHuman:
		userID, err := safeconv.ParsePositiveUint(assignee.ID)
		if err != nil {
			return nil, fmt.Errorf("%w: human assignee id must be a user id", ErrInvalidAssignee)
		}
		var user models.User
		if err := s.db.WithContext(ctx).Select("id").First(&user, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("%w: human %s", ErrAssigneeNotFound, assignee.ID)
			}
			return nil, fmt.Errorf("validate human assignee: %w", err)
		}
		changes["assigned_to_id"] = user.ID
	case models.ActorTypeServicePrincipal:
		var principal models.ServicePrincipal
		if err := s.db.WithContext(ctx).
			Select("id", "status", "emergency_disabled").
			Where("id = ? AND deleted_at IS NULL", assignee.ID).
			First(&principal).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("%w: service principal %s", ErrAssigneeNotFound, assignee.ID)
			}
			return nil, fmt.Errorf("validate service principal assignee: %w", err)
		}
		if principal.Status != models.ServicePrincipalStatusActive {
			return nil, fmt.Errorf(
				"%w: service principal %s is %s",
				ErrAssigneePolicyDenied,
				principal.ID,
				principal.Status,
			)
		}
		if principal.EmergencyDisabled {
			return nil, fmt.Errorf(
				"%w: service principal %s is emergency disabled",
				ErrAssigneePolicyDenied,
				principal.ID,
			)
		}
		changes["assigned_to_service_principal_id"] = principal.ID
	case models.ActorTypeSystem:
		return nil, fmt.Errorf("%w: system actors cannot be assignment targets", ErrInvalidAssignee)
	}
	return changes, nil
}

func applyAssignmentChangesToTicket(ticket *models.Ticket, changes map[string]any) {
	if ticket == nil || changes == nil {
		return
	}
	ticket.AssignedToActorType = models.ActorType(fmt.Sprint(changes["assigned_to_actor_type"]))
	ticket.AssignedToActorID = fmt.Sprint(changes["assigned_to_actor_id"])
	ticket.AssignedToID = nil
	if value, ok := changes["assigned_to_id"].(uint); ok && value > 0 {
		ticket.AssignedToID = &value
	}
	ticket.AssignedToServicePrincipalID = nil
	if value, ok := changes["assigned_to_service_principal_id"].(string); ok && value != "" {
		ticket.AssignedToServicePrincipalID = &value
	}
}

func validateCanonicalAssignmentChangesTx(
	ctx context.Context,
	tx *gorm.DB,
	changes map[string]any,
) error {
	if tx == nil {
		return errors.New("assignment validation transaction is required")
	}
	required := []string{
		"assigned_to_actor_type",
		"assigned_to_actor_id",
		"assigned_to_id",
		"assigned_to_service_principal_id",
	}
	for _, field := range required {
		if _, exists := changes[field]; !exists {
			return fmt.Errorf("%w: assignment command is missing %s", ErrInvalidAssignee, field)
		}
	}
	actorType := models.ActorType(fmt.Sprint(changes["assigned_to_actor_type"]))
	actorID := fmt.Sprint(changes["assigned_to_actor_id"])
	if actorType == "" && actorID == "" {
		if changes["assigned_to_id"] != nil ||
			changes["assigned_to_service_principal_id"] != nil {
			return fmt.Errorf("%w: released assignment has identity projections", ErrInvalidAssignee)
		}
		return nil
	}
	assignee := models.ActorRef{Type: actorType, ID: actorID}
	if err := assignee.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAssignee, err)
	}
	switch assignee.Type {
	case models.ActorTypeHuman:
		userID, err := safeconv.ParsePositiveUint(assignee.ID)
		if err != nil {
			return fmt.Errorf("%w: human assignee id must be a user id", ErrInvalidAssignee)
		}
		projectedID, ok := changes["assigned_to_id"].(uint)
		if !ok || projectedID != userID ||
			changes["assigned_to_service_principal_id"] != nil {
			return fmt.Errorf("%w: human assignment projections do not match ActorRef", ErrInvalidAssignee)
		}
		var count int64
		if err := tx.WithContext(ctx).Model(&models.User{}).
			Where("id = ?", userID).
			Count(&count).Error; err != nil {
			return fmt.Errorf("validate human assignee: %w", err)
		}
		if count != 1 {
			return fmt.Errorf("%w: human %s", ErrAssigneeNotFound, assignee.ID)
		}
	case models.ActorTypeServicePrincipal:
		projectedID, ok := changes["assigned_to_service_principal_id"].(string)
		if !ok || projectedID != assignee.ID || changes["assigned_to_id"] != nil {
			return fmt.Errorf("%w: service principal projections do not match ActorRef", ErrInvalidAssignee)
		}
		var principal models.ServicePrincipal
		if err := tx.WithContext(ctx).
			Select("id", "status", "emergency_disabled").
			Where("id = ? AND deleted_at IS NULL", assignee.ID).
			First(&principal).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: service principal %s", ErrAssigneeNotFound, assignee.ID)
			}
			return fmt.Errorf("validate service principal assignee: %w", err)
		}
		if principal.Status != models.ServicePrincipalStatusActive ||
			principal.EmergencyDisabled {
			return fmt.Errorf("%w: service principal %s is unavailable", ErrAssigneePolicyDenied, principal.ID)
		}
	case models.ActorTypeSystem:
		return fmt.Errorf("%w: system actors cannot be assignment targets", ErrInvalidAssignee)
	}
	return nil
}
