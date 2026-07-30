package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestDeleteTicketExpectedVersionRejectsStaleSnapshot(t *testing.T) {
	db := openAgentNativeTestDB(t)
	actor := seedActorUser(t, db, "delete-version")
	ticket := seedNativeTicket(t, db, actor.ID, "DELETE-VERSION-1")
	service := newTicketServiceForTest(t, db)
	ctx := testProjectOperationContext(t, db, models.HumanActor(actor.ID))

	err := service.DeleteTicketExpectedVersion(
		ctx,
		ticket.ID,
		actor.ID,
		string(models.RoleAdmin),
		ticket.Version+1,
	)
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale delete error = %v, want ErrVersionConflict", err)
	}

	var current models.Ticket
	if err := db.First(&current, ticket.ID).Error; err != nil {
		t.Fatalf("stale delete removed ticket: %v", err)
	}
	if current.Version != ticket.Version {
		t.Fatalf("stale delete changed version to %d", current.Version)
	}
	var eventCount int64
	if err := db.Model(&models.DomainEvent{}).Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if eventCount != 0 {
		t.Fatalf("stale delete emitted %d events", eventCount)
	}
}

func TestDeleteTicketCleansRelatedData(t *testing.T) {
	db := openTestDB(t)

	if err := db.AutoMigrate(
		&models.User{},
		&models.Ticket{},
		&models.Notification{},
		&models.TicketHistory{},
		&models.TicketComment{},
		&models.TicketAttachment{},
		&models.AutomationRule{},
		&models.AutomationLog{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.WebhookConfig{},
		&models.WebhookDeliverySnapshot{},
	); err != nil {
		t.Fatalf("failed to migrate schemas: %v", err)
	}

	user := models.User{
		Username:     "agent-delete",
		Email:        "agent-delete@example.com",
		PasswordHash: "hashed",
		Role:         models.RoleAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	ticket := models.Ticket{
		TicketNumber: "T-DELETE-001",
		Title:        "Delete cleanup ticket",
		Description:  "cleanup test",
		Priority:     models.TicketPriorityNormal,
		Status:       models.TicketStatusOpen,
		Type:         models.TicketTypeRequest,
		Source:       models.TicketSourceWeb,
		CreatedByID:  &user.ID,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatalf("failed to seed ticket: %v", err)
	}

	notification := models.Notification{
		Type:            models.NotificationTypeTicketCreated,
		Title:           "Ticket created",
		Content:         "Created for delete cleanup test",
		Priority:        models.NotificationPriorityNormal,
		Channel:         models.NotificationChannelInApp,
		RecipientID:     user.ID,
		RelatedTicketID: &ticket.ID,
	}
	if err := db.Create(&notification).Error; err != nil {
		t.Fatalf("failed to seed notification: %v", err)
	}

	history := models.TicketHistory{
		TicketID:    ticket.ID,
		UserID:      &user.ID,
		ActorType:   models.ActorTypeHuman,
		ActorID:     models.HumanActor(user.ID).ID,
		Action:      models.HistoryActionCreate,
		Description: "created",
	}
	if err := db.Create(&history).Error; err != nil {
		t.Fatalf("failed to seed ticket history: %v", err)
	}

	comment := models.TicketComment{
		TicketID:    ticket.ID,
		UserID:      &user.ID,
		ActorType:   models.ActorTypeHuman,
		ActorID:     models.HumanActor(user.ID).ID,
		Content:     "first response",
		ContentType: "text",
		Type:        models.CommentTypePublic,
	}
	if err := db.Create(&comment).Error; err != nil {
		t.Fatalf("failed to seed ticket comment: %v", err)
	}

	attachment := models.TicketAttachment{
		TicketID:     ticket.ID,
		UploadedBy:   &user.ID,
		ActorType:    models.ActorTypeHuman,
		ActorID:      models.HumanActor(user.ID).ID,
		FileName:     "test.txt",
		OriginalName: "test.txt",
		FileSize:     12,
		StoragePath:  "/tmp/test.txt",
	}
	if err := db.Create(&attachment).Error; err != nil {
		t.Fatalf("failed to seed ticket attachment: %v", err)
	}
	rule := models.AutomationRule{
		Name:         "delete audit rule",
		RuleType:     "assignment",
		TriggerEvent: "io.chronodesk.ticket.created.v1",
		CreatedBy:    user.ID,
	}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatalf("failed to seed automation rule: %v", err)
	}
	automationLog := models.AutomationLog{
		RuleID:       rule.ID,
		TicketID:     ticket.ID,
		TriggerEvent: rule.TriggerEvent,
		ExecutedAt:   time.Now(),
		Success:      true,
	}
	if err := db.Create(&automationLog).Error; err != nil {
		t.Fatalf("failed to seed automation audit log: %v", err)
	}

	svc := newTicketServiceForTest(t, db)
	ctx := testProjectOperationContext(t, db, models.HumanActor(user.ID))
	if err := svc.DeleteTicketExpectedVersion(
		ctx,
		ticket.ID,
		user.ID,
		"admin",
		ticket.Version,
	); err != nil {
		t.Fatalf("DeleteTicketExpectedVersion returned error: %v", err)
	}

	var notificationCount int64
	if err := db.Model(&models.Notification{}).Where("related_ticket_id = ?", ticket.ID).Count(&notificationCount).Error; err != nil {
		t.Fatalf("failed to count notifications: %v", err)
	}
	if notificationCount != 0 {
		t.Fatalf("expected notifications to be deleted, got %d", notificationCount)
	}

	var historyCount int64
	if err := db.Model(&models.TicketHistory{}).Where("ticket_id = ?", ticket.ID).Count(&historyCount).Error; err != nil {
		t.Fatalf("failed to count ticket histories: %v", err)
	}
	if historyCount != 0 {
		t.Fatalf("expected histories to be deleted, got %d", historyCount)
	}

	var commentCount int64
	if err := db.Model(&models.TicketComment{}).Where("ticket_id = ?", ticket.ID).Count(&commentCount).Error; err != nil {
		t.Fatalf("failed to count ticket comments: %v", err)
	}
	if commentCount != 0 {
		t.Fatalf("expected comments to be deleted, got %d", commentCount)
	}

	var attachmentCount int64
	if err := db.Model(&models.TicketAttachment{}).Where("ticket_id = ?", ticket.ID).Count(&attachmentCount).Error; err != nil {
		t.Fatalf("failed to count ticket attachments: %v", err)
	}
	if attachmentCount != 0 {
		t.Fatalf("expected attachments to be deleted, got %d", attachmentCount)
	}

	var ticketCount int64
	if err := db.Model(&models.Ticket{}).Where("id = ?", ticket.ID).Count(&ticketCount).Error; err != nil {
		t.Fatalf("failed to count tickets: %v", err)
	}
	if ticketCount != 0 {
		t.Fatalf("expected ticket to be deleted, got %d", ticketCount)
	}
	var deletedTicket models.Ticket
	if err := db.Unscoped().First(&deletedTicket, ticket.ID).Error; err != nil {
		t.Fatalf("deleted ticket audit anchor is missing: %v", err)
	}
	if !deletedTicket.DeletedAt.Valid {
		t.Fatal("ticket deletion did not retain a soft-deleted audit anchor")
	}
	var automationLogCount int64
	if err := db.Model(&models.AutomationLog{}).
		Where("id = ? AND ticket_id = ?", automationLog.ID, ticket.ID).
		Count(&automationLogCount).Error; err != nil {
		t.Fatalf("failed to count retained automation logs: %v", err)
	}
	if automationLogCount != 1 {
		t.Fatalf("ticket deletion lost %d automation audit logs", 1-automationLogCount)
	}
}

func TestDeleteTicketCommitsAttachmentCleanupWithDefaultOutboxTargets(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.Ticket{},
		&models.Notification{},
		&models.TicketHistory{},
		&models.TicketComment{},
		&models.TicketAttachment{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.WebhookConfig{},
		&models.WebhookDeliverySnapshot{},
	); err != nil {
		t.Fatalf("migrate delete cleanup schema: %v", err)
	}
	user := models.User{
		Username: "attachment-delete", Email: "attachment-delete@example.com",
		PasswordHash: "hash", Role: models.RoleAdmin, Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		TicketNumber: "T-DELETE-OBJECT-001",
		Title:        "Delete attachment object",
		Description:  "cleanup must happen after commit",
		Priority:     models.TicketPriorityNormal,
		Status:       models.TicketStatusOpen,
		Type:         models.TicketTypeRequest,
		Source:       models.TicketSourceWeb,
		CreatedByID:  &user.ID,
		Version:      1,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	storage, err := NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stored, err := storage.Put(
		context.Background(),
		"tickets/delete-cleanup/evidence.txt",
		strings.NewReader("evidence"),
		1024,
	)
	if err != nil {
		t.Fatal(err)
	}
	attachment := models.TicketAttachment{
		TicketID: ticket.ID, UploadedBy: &user.ID,
		ActorType: models.ActorTypeHuman, ActorID: models.HumanActor(user.ID).ID,
		FileName: "evidence.txt", OriginalName: "evidence.txt",
		FileSize: stored.Size, StoragePath: stored.Key, Hash: stored.SHA256,
	}
	if err := db.Create(&attachment).Error; err != nil {
		t.Fatal(err)
	}
	native := NewAgentNativeService(db, AgentNativeOptions{
		AttachmentStorage: storage,
		DefaultOutboxTargets: []OutboxTarget{
			{Type: "event_stream", ID: "default", MaxAttempts: 8},
			{Type: "webhook", ID: "configured", MaxAttempts: 8},
			{Type: "automation", ID: "rules", MaxAttempts: 8},
		},
	})
	service := newTicketServiceWithDependenciesForTest(t, db, native, nil, 0)
	ctx := testProjectOperationContext(t, db, models.HumanActor(user.ID))
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	webhook := models.WebhookConfig{
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		Name:           "ticket-delete-snapshot",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     "https://webhook.example.test/deleted",
		Status:         models.WebhookStatusActive,
		EnabledEventsObj: []models.WebhookEventType{
			models.WebhookEventTicketDeleted,
		},
		CreatedBy: user.ID,
	}
	if err := db.Create(&webhook).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteTicketExpectedVersion(
		ctx,
		ticket.ID,
		user.ID,
		"admin",
		ticket.Version,
	); err != nil {
		t.Fatalf("delete ticket: %v", err)
	}

	// Object deletion is deliberately outside the business transaction.
	reader, err := storage.Open(context.Background(), stored.Key)
	if err != nil {
		t.Fatalf("attachment was deleted inside the DB transaction: %v", err)
	}
	_ = reader.Close()

	var event models.DomainEvent
	if err := db.First(
		&event,
		"type = ? AND subject = ?",
		"io.chronodesk.ticket.deleted.v1",
		fmt.Sprintf("ticket/%d", ticket.ID),
	).Error; err != nil {
		t.Fatalf("load ticket.deleted event: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(event.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["attachment_cleanup_count"] != float64(1) {
		t.Fatalf("cleanup count missing from event: %#v", data)
	}
	envelope := CloudEventFromModel(&event)
	if strings.Contains(string(envelope.Data), stored.Key) {
		t.Fatal("public CloudEvent leaked an internal attachment storage path")
	}
	if !strings.Contains(string(envelope.InternalData), stored.Key) {
		t.Fatal("durable internal cleanup manifest did not retain the storage path")
	}
	wireEnvelope, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wireEnvelope), stored.Key) {
		t.Fatal("serialized CloudEvent leaked its internal cleanup manifest")
	}

	var deliveries []models.OutboxDelivery
	if err := db.Where("event_id = ?", event.ID).Find(&deliveries).Error; err != nil {
		t.Fatal(err)
	}
	destinations := make(map[string]int, len(deliveries))
	for _, delivery := range deliveries {
		destinations[delivery.DestinationType]++
	}
	if len(deliveries) != 4 ||
		destinations["event_stream"] != 1 ||
		destinations["webhook"] != 1 ||
		destinations["automation"] != 1 ||
		destinations[AttachmentCleanupOutboxDestination] != 1 {
		t.Fatalf("ticket deletion lost or duplicated Outbox targets: %#v", deliveries)
	}
	var snapshot models.WebhookDeliverySnapshot
	if err := db.Where(
		"event_id = ? AND config_id = ?",
		event.ID,
		webhook.ID,
	).First(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	for _, delivery := range deliveries {
		if delivery.DestinationType == "webhook" &&
			delivery.DestinationID !=
				webhookSnapshotDestinationPrefix+snapshot.ID {
			t.Fatalf(
				"ticket deletion webhook target is not snapshot-bound: %+v",
				delivery,
			)
		}
	}
}
