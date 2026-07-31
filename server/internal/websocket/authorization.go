package websocket

import (
	"context"
	"errors"
	"fmt"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
)

var ErrFanoutAuthorizationDenied = errors.New(
	"WebSocket fan-out authorization denied",
)

// FanoutAuthorizer revalidates a cached WebSocket binding immediately before
// delivery. Implementations must consult current authoritative state.
type FanoutAuthorizer interface {
	AuthorizeFanout(
		ctx context.Context,
		scope models.ProjectScope,
		userID uint,
	) error
}

type databaseFanoutAuthorizer struct {
	db *gorm.DB
}

func NewDatabaseFanoutAuthorizer(db *gorm.DB) FanoutAuthorizer {
	return &databaseFanoutAuthorizer{db: db}
}

func (authorizer *databaseFanoutAuthorizer) AuthorizeFanout(
	ctx context.Context,
	scope models.ProjectScope,
	userID uint,
) error {
	if authorizer == nil || authorizer.db == nil {
		return fmt.Errorf(
			"%w: authorization database is unavailable",
			ErrFanoutAuthorizationDenied,
		)
	}
	if ctx == nil {
		return fmt.Errorf(
			"%w: authorization context is required",
			ErrFanoutAuthorizationDenied,
		)
	}
	if err := scope.Validate(); err != nil || userID == 0 {
		return fmt.Errorf(
			"%w: invalid user or project scope",
			ErrFanoutAuthorizationDenied,
		)
	}

	authorize := func(scopedContext context.Context) error {
		var count int64
		err := authorizer.db.WithContext(scopedContext).
			Table("project_memberships AS memberships").
			Joins(
				"JOIN users ON users.id = memberships.user_id AND users.deleted_at IS NULL",
			).
			Joins(
				"JOIN projects ON projects.id = memberships.project_id",
			).
			Where(
				"memberships.project_id = ? AND memberships.user_id = ? AND memberships.is_active = ?",
				scope.ProjectID,
				userID,
				true,
			).
			Where(
				"memberships.role IN ?",
				[]models.ProjectRole{
					models.ProjectRoleAdmin,
					models.ProjectRoleManager,
					models.ProjectRoleAgent,
					models.ProjectRoleRequester,
					models.ProjectRoleObserver,
				},
			).
			Where("users.status = ?", models.UserStatusActive).
			Where(
				"projects.organization_id = ? AND projects.status = ?",
				scope.OrganizationID,
				models.ProjectStatusActive,
			).
			Count(&count).Error
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrFanoutAuthorizationDenied
		}
		return nil
	}

	reusable, err := scopeddb.CanReuseProjectScopeTransaction(ctx, scope)
	if err == nil && reusable {
		err = authorize(ctx)
	} else if err == nil {
		err = scopeddb.WithProjectScopeContextTransaction(
			ctx,
			authorizer.db,
			scope,
			authorize,
		)
	}
	if err != nil {
		if errors.Is(err, ErrFanoutAuthorizationDenied) {
			return err
		}
		return fmt.Errorf("revalidate WebSocket fan-out authorization: %w", err)
	}
	return nil
}
