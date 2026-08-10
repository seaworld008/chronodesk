package agentplatform

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestNotificationOutboxRecoversAfterSideEffectBeforeAcknowledgement(t *testing.T) {
	dsn := "file:" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(
		&models.User{},
		&models.Ticket{},
		&models.Notification{},
		&models.NotificationPreference{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.WebhookDeliverySnapshot{},
	); err != nil {
		t.Fatalf("migrate notification Outbox schema: %v", err)
	}

	actor := models.User{
		Username: "notification-actor", Email: "notification-actor@example.com",
		PasswordHash: "hash", PlatformRole: models.PlatformRoleMember, Status: models.UserStatusActive,
	}
	recipient := models.User{
		Username: "notification-recipient", Email: "notification-recipient@example.com",
		PasswordHash: "hash", PlatformRole: models.PlatformRoleMember, Status: models.UserStatusActive,
	}
	if err := db.Create(&actor).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&recipient).Error; err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		TicketNumber: "OUTBOX-NOTIFY-1",
		Title:        "Crash-safe notification",
		Description:  "persist before acknowledgement",
		Type:         models.TicketTypeRequest,
		Priority:     models.TicketPriorityHigh,
		Status:       models.TicketStatusOpen,
		Source:       models.TicketSourceWeb,
		CreatedByID:  &actor.ID,
		AssignedToID: &recipient.ID,
		Version:      2,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	scope := installAgentplatformTestProjectScope(t, db)
	eventCtx := agentplatformTestOperationContext(
		t,
		scope,
		models.HumanActor(actor.ID),
	)
	workerCtx := agentplatformTestOutboxWorkerContext(t, scope)

	now := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	native := services.NewAgentNativeService(db, services.AgentNativeOptions{
		Now: func() time.Time { return now },
	})
	targets := services.TicketAssignedNotificationOutboxTargets(&ticket, actor.ID)
	if len(targets) != 1 {
		t.Fatalf("assignment targets = %#v, want one", targets)
	}
	var event *models.DomainEvent
	if err := native.InTransaction(eventCtx, func(
		txCtx context.Context,
		tx *gorm.DB,
	) error {
		var appendErr error
		event, appendErr = native.AppendDomainEventTx(
			txCtx,
			tx,
			services.DomainEventInput{
				Type:            "io.chronodesk.ticket.assigned.v1",
				Subject:         fmt.Sprintf("ticket/%d", ticket.ID),
				Actor:           models.HumanActor(actor.ID),
				ResourceVersion: ticket.Version,
				Scope:           scope,
				Data: map[string]any{
					"ticket_id":       ticket.ID,
					"ticket_number":   ticket.TicketNumber,
					"ticket_title":    ticket.Title,
					"ticket_priority": ticket.Priority,
					"assigned_to_id":  recipient.ID,
				},
			},
			targets,
		)
		return appendErr
	}); err != nil {
		t.Fatalf("append notification event: %v", err)
	}

	claimed, err := native.ClaimPendingOutbox(
		workerCtx,
		"notification-worker-before-crash",
		10,
		2*time.Minute,
	)
	if err != nil {
		t.Fatalf("claim notification delivery: %v", err)
	}
	if len(claimed) != 1 || claimed[0].Event == nil {
		t.Fatalf("claimed deliveries = %#v", claimed)
	}
	notifications := services.NewNotificationServiceWithProtector(db, nil)
	deliverer, err := NewNativeOutboxDeliverer(NativeOutboxDelivererOptions{
		DB:            db,
		Notifications: notifications,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Persist the side effect but deliberately omit MarkOutboxDelivered to
	// model a process crash in the acknowledgement gap.
	if err := deliverer.Deliver(
		workerCtx,
		claimed[0],
		services.CloudEventFromModel(claimed[0].Event),
	); err != nil {
		t.Fatalf("deliver notification before crash: %v", err)
	}
	assertSingleSourceEventNotification(t, db, event.ID, targets[0].ID)

	// Once the processing lease expires, a new worker replays the same
	// delivery. The unique source_event_key makes replay a no-op side effect,
	// after which the Outbox acknowledgement can complete.
	now = now.Add(3 * time.Minute)
	result, err := native.ProcessOutboxBatch(
		context.Background(),
		"notification-worker-after-crash",
		10,
		deliverer,
	)
	if err != nil {
		t.Fatalf("recover notification delivery: %v", err)
	}
	if result.Claimed != 1 || result.Delivered != 1 || result.Failed != 0 {
		t.Fatalf("recovery result = %+v", result)
	}
	assertSingleSourceEventNotification(t, db, event.ID, targets[0].ID)

	var delivery models.OutboxDelivery
	if err := db.First(&delivery, "event_id = ?", event.ID).Error; err != nil {
		t.Fatal(err)
	}
	if delivery.Status != models.OutboxDeliverySucceeded || delivery.Attempts != 2 {
		t.Fatalf("delivery state after recovery = %+v", delivery)
	}

	// Administrative replay or duplicate broker delivery remains idempotent
	// even after the original Outbox row is already acknowledged.
	if err := deliverer.Deliver(
		workerCtx,
		&delivery,
		services.CloudEventFromModel(event),
	); err != nil {
		t.Fatalf("duplicate notification delivery: %v", err)
	}
	assertSingleSourceEventNotification(t, db, event.ID, targets[0].ID)
}

func assertSingleSourceEventNotification(
	t *testing.T,
	db *gorm.DB,
	eventID string,
	destinationID string,
) {
	t.Helper()
	var notifications []models.Notification
	if err := db.Find(&notifications).Error; err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 1 {
		t.Fatalf("notifications = %d, want exactly one", len(notifications))
	}
	expectedKey := eventID + ":" + destinationID
	if notifications[0].SourceEventKey == nil ||
		*notifications[0].SourceEventKey != expectedKey {
		t.Fatalf(
			"source event key = %v, want %q",
			notifications[0].SourceEventKey,
			expectedKey,
		)
	}
	if notifications[0].Channel != models.NotificationChannelInApp {
		t.Fatalf("notification channel = %q, want in_app", notifications[0].Channel)
	}
}
