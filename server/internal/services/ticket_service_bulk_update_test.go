package services

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestBulkUpdateTicketsWritesVersionedAuditAndOutboxAtomically(t *testing.T) {
	db := openAgentNativeTestDB(t)
	actor := seedActorUser(t, db, "bulk-actor")
	assignee := seedActorUser(t, db, "bulk-assignee")
	first := seedNativeTicket(t, db, actor.ID, "BULK-NATIVE-1")
	second := seedNativeTicket(t, db, actor.ID, "BULK-NATIVE-2")
	native := NewAgentNativeService(db)
	service := &TicketService{db: db, agentNative: native}
	ctx := testProjectOperationContext(t, db, models.HumanActor(actor.ID))

	status := string(models.TicketStatusInProgress)
	priority := string(models.TicketPriorityHigh)
	result, err := service.BulkUpdateTickets(ctx, &BulkUpdateRequest{
		Tickets: []TicketVersionPrecondition{
			{ID: second.ID, Version: second.Version},
			{ID: first.ID, Version: first.Version},
		},
		Status:       &status,
		Priority:     &priority,
		AssignedToID: &assignee.ID,
		Tags:         []string{"bulk", "audited"},
		CustomFields: map[string]interface{}{"source": "admin-bulk"},
	}, actor.ID)
	if err != nil {
		t.Fatalf("bulk update: %v", err)
	}
	if len(result.Tickets) != 2 {
		t.Fatalf("bulk receipts = %#v", result.Tickets)
	}

	for _, ticketID := range []uint{first.ID, second.ID} {
		var ticket models.Ticket
		if err := db.First(&ticket, ticketID).Error; err != nil {
			t.Fatalf("reload ticket %d: %v", ticketID, err)
		}
		if ticket.Version != 2 ||
			ticket.Status != models.TicketStatusInProgress ||
			ticket.Priority != models.TicketPriorityHigh ||
			ticket.AssignedToID == nil ||
			*ticket.AssignedToID != assignee.ID ||
			ticket.AssignedToActorType != models.ActorTypeHuman ||
			ticket.AssignedToActorID != models.HumanActor(assignee.ID).ID ||
			ticket.AssignedToServicePrincipalID != nil {
			t.Fatalf("ticket %d has incomplete versioned update: %+v", ticketID, ticket)
		}

		var history models.TicketHistory
		if err := db.Where("ticket_id = ?", ticketID).First(&history).Error; err != nil {
			t.Fatalf("load ticket %d history: %v", ticketID, err)
		}
		if history.ActorType != models.ActorTypeHuman ||
			history.ActorID != models.HumanActor(actor.ID).ID ||
			history.UserID == nil ||
			*history.UserID != actor.ID ||
			history.IsSystem ||
			history.IsAutomated ||
			history.Details == "" {
			t.Fatalf("ticket %d history lost human audit: %+v", ticketID, history)
		}

		var event models.DomainEvent
		if err := db.Where("subject = ?", fmt.Sprintf("ticket/%d", ticketID)).
			First(&event).Error; err != nil {
			t.Fatalf("load ticket %d event: %v", ticketID, err)
		}
		if event.ActorType != models.ActorTypeHuman ||
			event.ActorID != models.HumanActor(actor.ID).ID ||
			event.ResourceVersion != 2 {
			t.Fatalf("ticket %d event lost version or actor: %+v", ticketID, event)
		}
		var deliveryCount int64
		if err := db.Model(&models.OutboxDelivery{}).
			Where("event_id = ?", event.ID).
			Count(&deliveryCount).Error; err != nil {
			t.Fatalf("count ticket %d Outbox: %v", ticketID, err)
		}
		if deliveryCount != 3 {
			t.Fatalf(
				"ticket %d Outbox deliveries=%d, want default event plus assignment and status notifications",
				ticketID,
				deliveryCount,
			)
		}
	}
}

func TestBulkUpdateTicketsRollsBackEveryTicketWhenLatestTransitionIsInvalid(t *testing.T) {
	db := openAgentNativeTestDB(t)
	actor := seedActorUser(t, db, "bulk-rollback")
	first := seedNativeTicket(t, db, actor.ID, "BULK-ROLLBACK-1")
	second := seedNativeTicket(t, db, actor.ID, "BULK-ROLLBACK-2")
	if err := db.Model(&models.Ticket{}).
		Where("id = ?", second.ID).
		Update("status", models.TicketStatusClosed).Error; err != nil {
		t.Fatalf("seed latest closed state: %v", err)
	}
	service := &TicketService{db: db, agentNative: NewAgentNativeService(db)}
	ctx := testProjectOperationContext(t, db, models.HumanActor(actor.ID))
	status := string(models.TicketStatusInProgress)
	priority := string(models.TicketPriorityHigh)

	_, err := service.BulkUpdateTickets(ctx, &BulkUpdateRequest{
		Tickets: []TicketVersionPrecondition{
			{ID: first.ID, Version: first.Version},
			{ID: second.ID, Version: second.Version},
		},
		Status:   &status,
		Priority: &priority,
	}, actor.ID)
	if !errors.Is(err, ErrInvalidTicketTransition) {
		t.Fatalf("expected latest-state transition error, got %v", err)
	}

	for _, ticketID := range []uint{first.ID, second.ID} {
		var ticket models.Ticket
		if err := db.First(&ticket, ticketID).Error; err != nil {
			t.Fatalf("reload ticket %d: %v", ticketID, err)
		}
		expectedStatus := models.TicketStatusOpen
		if ticketID == second.ID {
			expectedStatus = models.TicketStatusClosed
		}
		if ticket.Version != 1 ||
			ticket.Status != expectedStatus ||
			ticket.Priority != models.TicketPriorityNormal {
			t.Fatalf("atomic rollback failed for ticket %d: %+v", ticketID, ticket)
		}
	}
	for name, model := range map[string]any{
		"histories": &models.TicketHistory{},
		"events":    &models.DomainEvent{},
		"outbox":    &models.OutboxDelivery{},
	} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if count != 0 {
			t.Fatalf("%s persisted despite atomic rollback: %d", name, count)
		}
	}
}

func TestBulkUpdateTicketsDoesNotOverwriteAgentCommitBetweenReadAndCAS(t *testing.T) {
	db := openBulkConcurrencyTestDB(t)
	actor := seedActorUser(t, db, "bulk-concurrent")
	ticket := seedNativeTicket(t, db, actor.ID, "BULK-CONCURRENT-1")
	native := NewAgentNativeService(db)
	service := &TicketService{db: db, agentNative: native}
	humanCtx := testProjectOperationContext(t, db, models.HumanActor(actor.ID))
	systemCtx := testProjectOperationContext(
		t,
		db,
		models.SystemActor("concurrent-agent"),
	)

	var injectAgentCommit atomic.Bool
	injectAgentCommit.Store(true)
	var agentCommitErr error
	callbackName := "test:bulk-concurrent-agent-commit"
	if err := db.Callback().Update().Before("gorm:update").Register(
		callbackName,
		func(callbackTx *gorm.DB) {
			if callbackTx.Statement.Table != "tickets" ||
				!injectAgentCommit.CompareAndSwap(true, false) {
				return
			}
			_, agentCommitErr = native.UpdateTicketVersion(
				systemCtx,
				VersionedTicketUpdateInput{
					TicketID:        ticket.ID,
					ExpectedVersion: 1,
					Actor:           models.SystemActor("concurrent-agent"),
					SourceProtocol:  "test",
					Changes:         map[string]any{"title": "agent committed title"},
				},
			)
			if agentCommitErr != nil {
				callbackTx.AddError(fmt.Errorf("inject concurrent Agent commit: %w", agentCommitErr))
			}
		},
	); err != nil {
		t.Fatalf("register concurrent update hook: %v", err)
	}
	defer func() {
		_ = db.Callback().Update().Remove(callbackName)
	}()

	priority := string(models.TicketPriorityHigh)
	_, err := service.BulkUpdateTickets(humanCtx, &BulkUpdateRequest{
		Tickets:  []TicketVersionPrecondition{{ID: ticket.ID, Version: ticket.Version}},
		Priority: &priority,
	}, actor.ID)
	if err == nil {
		t.Fatal("bulk update should fail when an Agent commits between read and CAS")
	}
	if injectAgentCommit.Load() {
		t.Fatal("concurrent Agent commit hook was not reached")
	}
	if agentCommitErr != nil {
		t.Fatalf("concurrent Agent commit failed: %v", agentCommitErr)
	}

	var current models.Ticket
	if err := db.First(&current, ticket.ID).Error; err != nil {
		t.Fatalf("reload concurrent ticket: %v", err)
	}
	if current.Title != "agent committed title" ||
		current.Priority != models.TicketPriorityNormal ||
		current.Version != 2 {
		t.Fatalf("bulk update overwrote concurrent Agent state: %+v", current)
	}

	var histories []models.TicketHistory
	if err := db.Where("ticket_id = ?", ticket.ID).Find(&histories).Error; err != nil {
		t.Fatalf("load concurrent history: %v", err)
	}
	if len(histories) != 1 ||
		histories[0].ActorType != models.ActorTypeSystem ||
		histories[0].ActorID != "concurrent-agent" {
		t.Fatalf("bulk audit leaked through rollback: %+v", histories)
	}
}

func openBulkConcurrencyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bulk-concurrency.db")
	dsn := path + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open bulk concurrency database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("open bulk concurrency sql database: %v", err)
	}
	sqlDB.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(
		&models.User{},
		&models.Category{},
		&models.ServicePrincipal{},
		&models.AgentCredential{},
		&models.AgentPolicy{},
		&models.PolicyDecision{},
		&models.IdempotencyRecord{},
		&models.Ticket{},
		&models.TicketComment{},
		&models.TicketAttachment{},
		&models.TicketHistory{},
		&models.TicketLease{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatalf("migrate bulk concurrency schema: %v", err)
	}
	return db
}
