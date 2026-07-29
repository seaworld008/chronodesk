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
	// cannot receive work because its controls or compatibility identity make
	// it unavailable.
	ErrAssigneePolicyDenied = errors.New("ticket assignee denied by policy")
)

// ResolveTicketAssignmentChanges converts a protocol-neutral ActorRef into the
// complete compatibility-aware Ticket update. This is the sole assignment
// target resolver used by MCP and A2A.
func (s *AgentNativeService) ResolveTicketAssignmentChanges(
	ctx context.Context,
	assignee models.ActorRef,
) (map[string]any, error) {
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
			Select("id", "status", "emergency_disabled", "compatibility_user_id").
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
		if principal.CompatibilityUserID == nil || *principal.CompatibilityUserID == 0 {
			return nil, fmt.Errorf(
				"%w: service principal %s has no compatibility user",
				ErrAssigneePolicyDenied,
				principal.ID,
			)
		}
		var compatibilityUser models.User
		if err := s.db.WithContext(ctx).
			Select("id").
			First(&compatibilityUser, *principal.CompatibilityUserID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf(
					"%w: service principal %s compatibility user is unavailable",
					ErrAssigneePolicyDenied,
					principal.ID,
				)
			}
			return nil, fmt.Errorf("validate assignee compatibility user: %w", err)
		}
		changes["assigned_to_id"] = compatibilityUser.ID
		changes["assigned_to_service_principal_id"] = principal.ID
	case models.ActorTypeSystem:
		// System assignment retains the canonical ActorRef while deliberately
		// clearing both legacy compatibility columns.
	}
	return changes, nil
}
