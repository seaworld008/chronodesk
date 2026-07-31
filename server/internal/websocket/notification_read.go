package websocket

import (
	"context"
	"errors"
	"fmt"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
)

// HumanNotificationOperationContextFactory installs the trusted human actor
// and protocol metadata required by the notification domain service.
type HumanNotificationOperationContextFactory func(
	ctx context.Context,
	scope models.ProjectScope,
	userID uint,
) (context.Context, error)

// HumanProjectAccessRevalidator is the narrow domain authorization seam used
// by a WebSocket notification command. Production wiring delegates to
// ProjectService.RevalidateHumanProjectAccess.
type HumanProjectAccessRevalidator func(
	ctx context.Context,
	scope models.ProjectScope,
	userID uint,
) error

// NotificationReadStore is the bounded notification persistence seam required
// by mark_read. Both methods must honor the transaction bound to ctx.
type NotificationReadStore interface {
	MarkAsRead(
		ctx context.Context,
		notificationID uint,
		userID uint,
	) error
	GetUnreadCount(ctx context.Context, userID uint) (int64, error)
}

// NewDatabaseNotificationReadHandler creates a command-owned mark_read
// boundary. Human authorization, the notification update, and the resulting
// unread count all execute inside one exact project-scoped transaction. The
// returned count is pushed only after that transaction commits.
func NewDatabaseNotificationReadHandler(
	db *gorm.DB,
	operationContext HumanNotificationOperationContextFactory,
	projects HumanProjectAccessRevalidator,
	notifications NotificationReadStore,
) NotificationReadHandler {
	return func(
		ctx context.Context,
		scope models.ProjectScope,
		userID uint,
		notificationID uint,
	) (int64, error) {
		if ctx == nil {
			return 0, errors.New(
				"WebSocket notification read context is required",
			)
		}
		if err := ctx.Err(); err != nil {
			return 0, fmt.Errorf(
				"WebSocket notification read context: %w",
				err,
			)
		}
		if db == nil || operationContext == nil || projects == nil ||
			notifications == nil {
			return 0, errors.New(
				"WebSocket notification read command is unavailable",
			)
		}
		if err := scope.Validate(); err != nil {
			return 0, fmt.Errorf(
				"WebSocket notification read project scope: %w",
				err,
			)
		}
		if userID == 0 || notificationID == 0 {
			return 0, errors.New(
				"WebSocket notification read user and notification are required",
			)
		}
		if scopeddb.HasTransaction(ctx) {
			return 0, errors.New(
				"WebSocket notification read must own its project transaction",
			)
		}

		commandContext, err := operationContext(
			ctx,
			scope,
			userID,
		)
		if err != nil {
			return 0, fmt.Errorf(
				"build WebSocket notification operation context: %w",
				err,
			)
		}
		if commandContext == nil {
			return 0, errors.New(
				"WebSocket notification operation context is unavailable",
			)
		}

		var unreadCount int64
		err = scopeddb.WithProjectScopeContextTransaction(
			commandContext,
			db,
			scope,
			func(scopedContext context.Context) error {
				if revalidateErr := projects(
					scopedContext,
					scope,
					userID,
				); revalidateErr != nil {
					return fmt.Errorf(
						"revalidate WebSocket project access: %w",
						revalidateErr,
					)
				}
				if markErr := notifications.MarkAsRead(
					scopedContext,
					notificationID,
					userID,
				); markErr != nil {
					return markErr
				}
				var countErr error
				unreadCount, countErr =
					notifications.GetUnreadCount(scopedContext, userID)
				return countErr
			},
		)
		if err != nil {
			return 0, err
		}
		return unreadCount, nil
	}
}
