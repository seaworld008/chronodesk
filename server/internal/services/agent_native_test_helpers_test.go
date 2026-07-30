package services

import (
	"context"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"

	"gorm.io/gorm"
)

// These helpers deliberately exist only in the test binary. Production
// adapters must use the audited command methods instead of bypassing policy,
// idempotency, and event creation through low-level lease operations.
func (s *AgentNativeService) createDomainEvent(
	t *testing.T,
	ctx context.Context,
	input DomainEventInput,
	targets []OutboxTarget,
) (*models.DomainEvent, error) {
	t.Helper()
	if input.Scope.IsZero() {
		if _, err := OperationContextFromContext(ctx); err != nil {
			ctx = testProjectOperationContext(t, s.db, input.Actor)
		}
	}
	var event *models.DomainEvent
	err := s.InTransaction(ctx, func(txCtx context.Context, tx *gorm.DB) error {
		var err error
		event, err = s.AppendDomainEventTx(txCtx, tx, input, targets)
		return err
	})
	return event, err
}

func (s *AgentNativeService) claimTicketLease(
	ctx context.Context,
	ticketID uint,
	actor models.ActorRef,
	expectedVersion uint64,
	ttl time.Duration,
) (*models.TicketLease, error) {
	return s.claimTicketLeaseOnDB(ctx, s.db, ticketID, actor, expectedVersion, ttl)
}

func (s *AgentNativeService) heartbeatTicketLease(
	ctx context.Context,
	leaseID string,
	actor models.ActorRef,
	expectedVersion uint64,
	ttl time.Duration,
) (*models.TicketLease, error) {
	return s.heartbeatTicketLeaseOnDB(ctx, s.db, leaseID, actor, expectedVersion, ttl)
}

func (s *AgentNativeService) releaseTicketLease(
	ctx context.Context,
	leaseID string,
	actor models.ActorRef,
	reason string,
) error {
	_, err := s.releaseTicketLeaseOnDB(ctx, s.db, leaseID, actor, reason)
	return err
}
