package agentplatform

import (
	"context"
	"errors"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"gorm.io/gorm"
)

// runMachineTicketCreateDatabaseCommand is the single adapter seam used by
// Agent REST, MCP and A2A ticket intake. The services command owns the Project
// UPDATE lock and complete live machine authorization transaction; protocol
// adapters must call this function only from a context without an outer
// project transaction.
func runMachineTicketCreateDatabaseCommand(
	ctx context.Context,
	db *gorm.DB,
	native *services.AgentNativeService,
	input services.NativeTicketCreateInput,
) (*services.NativeTicketCreateResult, error) {
	var result *services.NativeTicketCreateResult
	_, err := services.RunTicketCreateDatabaseCommand(
		ctx,
		db,
		native,
		func(
			scopedContext context.Context,
			_ *gorm.DB,
			_ *services.ProjectAccess,
		) error {
			var commandErr error
			result, commandErr = native.CreateNativeTicket(
				scopedContext,
				input,
			)
			return commandErr
		},
	)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("machine Ticket create returned no result")
	}
	return result, nil
}

func runMachineProjectCommand[T any](
	ctx context.Context,
	native *services.AgentNativeService,
	tokenScopes []string,
	projectKey models.ProjectKey,
	run func(context.Context) (T, error),
) (T, error) {
	var result T
	err := native.RunProjectOperation(
		ctx,
		func(scopedContext context.Context) error {
			access, revalidateErr :=
				native.RevalidatePrincipalProjectOperation(
					scopedContext,
					tokenScopes...,
				)
			if revalidateErr != nil {
				return revalidateErr
			}
			if access.Project.Key != projectKey {
				return services.ErrProjectAccessDenied
			}
			var commandErr error
			result, commandErr = run(scopedContext)
			return commandErr
		},
	)
	return result, err
}

func machineAuthorizationRevoked(err error) bool {
	return errors.Is(err, services.ErrProjectAccessDenied) ||
		errors.Is(err, services.ErrProjectInactive) ||
		errors.Is(err, services.ErrInvalidCredential) ||
		errors.Is(err, services.ErrCredentialExpired) ||
		errors.Is(err, services.ErrPrincipalNotFound) ||
		errors.Is(err, services.ErrPrincipalDisabled) ||
		errors.Is(err, services.ErrPrincipalExpired)
}
