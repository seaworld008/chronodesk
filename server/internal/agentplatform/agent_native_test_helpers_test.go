package agentplatform

import (
	"context"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"gorm.io/gorm"
)

func appendTestDomainEvent(
	ctx context.Context,
	native *services.AgentNativeService,
	input services.DomainEventInput,
	targets []services.OutboxTarget,
) (*models.DomainEvent, error) {
	var event *models.DomainEvent
	err := native.InTransaction(ctx, func(txCtx context.Context, tx *gorm.DB) error {
		var err error
		event, err = native.AppendDomainEventTx(txCtx, tx, input, targets)
		return err
	})
	return event, err
}
