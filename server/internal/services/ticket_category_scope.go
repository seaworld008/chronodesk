package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/safeconv"
	"gorm.io/gorm"
)

var (
	ErrTicketCategoryScope = errors.New(
		"ticket category does not match trusted project scope",
	)
	ErrInvalidTicketCategorySelection = errors.New(
		"invalid ticket category selection",
	)
)

// validateTicketCategorySelectionTx is the shared Human, Agent REST, MCP and
// A2A category boundary. Adapters may carry only opaque category IDs; the
// trusted OperationContext supplies the numeric ProjectScope.
func validateTicketCategorySelectionTx(
	ctx context.Context,
	tx *gorm.DB,
	scope models.ProjectScope,
	categoryID *uint,
	subcategoryID *uint,
) error {
	if tx == nil {
		return errors.New("category validation database is required")
	}
	if ctx == nil {
		return errors.New("category validation context is required")
	}
	if err := scope.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrTicketCategoryScope, err)
	}
	if categoryID == nil {
		if subcategoryID != nil {
			return fmt.Errorf(
				"%w: a subcategory requires a primary category",
				ErrInvalidTicketCategorySelection,
			)
		}
		return nil
	}
	if *categoryID == 0 {
		return fmt.Errorf(
			"%w: category id must be positive",
			ErrInvalidTicketCategorySelection,
		)
	}

	var category models.Category
	if err := tx.WithContext(ctx).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? AND deleted_at IS NULL",
			*categoryID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		First(&category).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf(
				"%w: category %d was not found in the authorized project",
				ErrTicketCategoryScope,
				*categoryID,
			)
		}
		return fmt.Errorf("resolve project category: %w", err)
	}
	if subcategoryID == nil {
		return nil
	}
	if *subcategoryID == 0 || *subcategoryID == *categoryID {
		return fmt.Errorf(
			"%w: subcategory must be a distinct positive category",
			ErrInvalidTicketCategorySelection,
		)
	}

	var subcategory models.Category
	if err := tx.WithContext(ctx).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? AND deleted_at IS NULL",
			*subcategoryID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		First(&subcategory).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf(
				"%w: subcategory %d was not found in the authorized project",
				ErrTicketCategoryScope,
				*subcategoryID,
			)
		}
		return fmt.Errorf("resolve project subcategory: %w", err)
	}
	if subcategory.ParentID == nil || *subcategory.ParentID != category.ID {
		return fmt.Errorf(
			"%w: subcategory %d is not a direct child of category %d",
			ErrInvalidTicketCategorySelection,
			subcategory.ID,
			category.ID,
		)
	}
	return nil
}

func normalizeTicketCategoryChange(value any) (any, error) {
	id, err := optionalTicketCategoryID(value)
	if err != nil {
		return nil, err
	}
	if id == nil {
		return nil, nil
	}
	return *id, nil
}

func optionalTicketCategoryID(value any) (*uint, error) {
	if value == nil {
		return nil, nil
	}
	const (
		maxExactJSONInteger = float64(1<<53 - 1)
		maxDatabaseID       = uint64(1<<63 - 1)
	)
	var raw uint64
	switch typed := value.(type) {
	case uint:
		raw = uint64(typed)
	case *uint:
		if typed == nil {
			return nil, nil
		}
		raw = uint64(*typed)
	case uint8:
		raw = uint64(typed)
	case uint16:
		raw = uint64(typed)
	case uint32:
		raw = uint64(typed)
	case uint64:
		raw = typed
	case int:
		if typed <= 0 {
			return nil, invalidTicketCategoryID()
		}
		raw = uint64(typed)
	case int32:
		if typed <= 0 {
			return nil, invalidTicketCategoryID()
		}
		raw = uint64(typed)
	case int64:
		if typed <= 0 {
			return nil, invalidTicketCategoryID()
		}
		raw = uint64(typed)
	case float64:
		if typed <= 0 ||
			typed != math.Trunc(typed) ||
			typed > maxExactJSONInteger {
			return nil, invalidTicketCategoryID()
		}
		raw = uint64(typed)
	case json.Number:
		parsed, err := strconv.ParseUint(string(typed), 10, 64)
		if err != nil {
			return nil, invalidTicketCategoryID()
		}
		raw = parsed
	default:
		return nil, invalidTicketCategoryID()
	}
	if raw == 0 || raw > maxDatabaseID {
		return nil, invalidTicketCategoryID()
	}
	id, err := safeconv.PositiveUint(raw)
	if err != nil {
		return nil, invalidTicketCategoryID()
	}
	return &id, nil
}

func invalidTicketCategoryID() error {
	return fmt.Errorf(
		"%w: category identifiers must be positive integers or null",
		ErrInvalidTicketCategorySelection,
	)
}
