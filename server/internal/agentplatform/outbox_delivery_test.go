package agentplatform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gongdan-system/internal/models"
	"gongdan-system/internal/services"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type recordingSLAEscalationConsumer struct {
	calls int
	event services.CloudEventEnvelope
}

type failOnceAttachmentStorage struct {
	services.AttachmentStorage
	attempts atomic.Int32
}

func (s *failOnceAttachmentStorage) Delete(ctx context.Context, key string) error {
	if s.attempts.Add(1) == 1 {
		return errors.New("temporary attachment storage failure")
	}
	return s.AttachmentStorage.Delete(ctx, key)
}

func (c *recordingSLAEscalationConsumer) ExecuteDomainEvent(
	_ context.Context,
	event services.CloudEventEnvelope,
) error {
	c.calls++
	c.event = event
	return nil
}

func TestSLAEscalationOutboxDeliveryUsesInjectedConsumer(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:sla_outbox_routing?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	deliverer, err := NewNativeOutboxDeliverer(db, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	delivery := &models.OutboxDelivery{
		ID:              "sla-delivery",
		DestinationType: services.SLAEscalationOutboxDestination,
		DestinationID:   "breach",
	}
	event := services.CloudEventEnvelope{
		ID:   "sla-event",
		Type: services.SLABreachEventType,
	}
	if err := deliverer.Deliver(context.Background(), delivery, event); err == nil {
		t.Fatal("unconfigured SLA continuation was acknowledged")
	}
	consumer := &recordingSLAEscalationConsumer{}
	deliverer.SetSLAEscalationConsumer(consumer)
	if err := deliverer.Deliver(context.Background(), delivery, event); err != nil {
		t.Fatalf("deliver SLA continuation: %v", err)
	}
	if consumer.calls != 1 || consumer.event.ID != event.ID {
		t.Fatalf("SLA continuation was not routed intact: %+v", consumer)
	}
}

func TestAttachmentCleanupOutboxRetriesAfterCommitAndIsIdempotent(t *testing.T) {
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
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
		&models.TicketHistory{},
		&models.TicketComment{},
		&models.TicketAttachment{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatalf("migrate attachment cleanup schema: %v", err)
	}
	user := models.User{
		Username: "cleanup-owner", Email: "cleanup-owner@example.com",
		PasswordHash: "hash", Role: models.RoleAdmin, Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		TicketNumber: "CLEANUP-1", Title: "Cleanup",
		Description: "attachment", Priority: models.TicketPriorityNormal,
		Status: models.TicketStatusOpen, Type: models.TicketTypeRequest,
		Source: models.TicketSourceWeb, CreatedByID: user.ID, Version: 1,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	local, err := services.NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stored, err := local.Put(
		context.Background(),
		"tickets/cleanup/retry.txt",
		strings.NewReader("retryable cleanup"),
		1024,
	)
	if err != nil {
		t.Fatal(err)
	}
	attachment := models.TicketAttachment{
		TicketID: ticket.ID, UploadedBy: user.ID,
		FileName: "retry.txt", OriginalName: "retry.txt",
		FileSize: stored.Size, StoragePath: stored.Key, Hash: stored.SHA256,
	}
	if err := db.Create(&attachment).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 29, 6, 0, 0, 0, time.UTC)
	native := services.NewAgentNativeService(db, services.AgentNativeOptions{
		Now:               func() time.Time { return now },
		AttachmentStorage: local,
		DefaultOutboxTargets: []services.OutboxTarget{{
			Type: "event_stream", ID: "default", MaxAttempts: 8,
		}},
	})
	ticketService := services.NewTicketServiceWithAgentNative(db, nil, 0, native)
	if err := ticketService.DeleteTicket(
		context.Background(),
		ticket.ID,
		user.ID,
		"admin",
	); err != nil {
		t.Fatalf("delete ticket transaction: %v", err)
	}
	// The database transaction must not call external storage.
	reader, err := local.Open(context.Background(), stored.Key)
	if err != nil {
		t.Fatalf("file was removed before Outbox delivery: %v", err)
	}
	_ = reader.Close()
	var event models.DomainEvent
	if err := db.First(
		&event,
		"type = ? AND subject = ?",
		"io.chronodesk.ticket.deleted.v1",
		fmt.Sprintf("ticket/%d", ticket.ID),
	).Error; err != nil {
		t.Fatalf("load committed ticket deletion event: %v", err)
	}
	flaky := &failOnceAttachmentStorage{AttachmentStorage: local}
	deliverer, err := NewNativeOutboxDeliverer(db, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	deliverer.SetAttachmentStorage(flaky)

	first, err := native.ProcessOutboxBatch(
		context.Background(),
		"attachment-cleanup-first",
		10,
		deliverer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Claimed != 2 || first.Failed != 1 || first.Delivered != 1 {
		t.Fatalf("first cleanup attempt was not retained for retry: %+v", first)
	}
	reader, err = local.Open(context.Background(), stored.Key)
	if err != nil {
		t.Fatalf("failed cleanup attempt removed the object: %v", err)
	}
	_ = reader.Close()

	now = now.Add(3 * time.Second)
	second, err := native.ProcessOutboxBatch(
		context.Background(),
		"attachment-cleanup-retry",
		10,
		deliverer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.Delivered != 1 || flaky.attempts.Load() != 2 {
		t.Fatalf("cleanup retry did not succeed: result=%+v attempts=%d", second, flaky.attempts.Load())
	}
	if reader, err := local.Open(context.Background(), stored.Key); err == nil {
		_ = reader.Close()
		t.Fatal("attachment object still exists after successful Outbox delivery")
	}

	var delivery models.OutboxDelivery
	if err := db.First(
		&delivery,
		"event_id = ? AND destination_type = ?",
		event.ID,
		services.AttachmentCleanupOutboxDestination,
	).Error; err != nil {
		t.Fatal(err)
	}
	if delivery.Status != models.OutboxDeliverySucceeded || delivery.Attempts != 2 {
		t.Fatalf("cleanup delivery state = %+v", delivery)
	}
	// A crash after object deletion but before acknowledgement may invoke the
	// same delivery again. AttachmentStorage.Delete must make that safe.
	if err := deliverer.Deliver(
		context.Background(),
		&delivery,
		services.CloudEventFromModel(&event),
	); err != nil {
		t.Fatalf("duplicate cleanup delivery is not idempotent: %v", err)
	}
}

func TestAttachmentCleanupOutboxRejectsPathTraversal(t *testing.T) {
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
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
		&models.TicketAttachment{},
	); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username: "traversal-owner", Email: "traversal-owner@example.com",
		PasswordHash: "hash", Role: models.RoleAdmin, Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		TicketNumber: "CLEANUP-TRAVERSAL", Title: "Traversal",
		Description: "must reject", Priority: models.TicketPriorityNormal,
		Status: models.TicketStatusOpen, Type: models.TicketTypeRequest,
		Source: models.TicketSourceWeb, CreatedByID: user.ID, Version: 1,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	attachment := models.TicketAttachment{
		TicketID: ticket.ID, UploadedBy: user.ID,
		FileName: "escape.txt", OriginalName: "escape.txt", FileSize: 1,
		StoragePath: "../escape.txt", StorageUrl: "http://127.0.0.1/private",
	}
	if err := db.Create(&attachment).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&attachment).Error; err != nil {
		t.Fatal(err)
	}
	target, err := services.NewAttachmentCleanupOutboxTarget(
		attachment.ID,
		attachment.StoragePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(target.ID, attachment.StoragePath) ||
		strings.Contains(target.ID, attachment.StorageUrl) {
		t.Fatal("cleanup destination exposed an internal path or provider URL")
	}
	local, err := services.NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	deliverer, err := NewNativeOutboxDeliverer(db, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	deliverer.SetAttachmentStorage(local)
	data, _ := json.Marshal(map[string]any{
		"ticket_id": ticket.ID,
		"deleted":   true,
		services.AttachmentCleanupObjectsDataField: []services.AttachmentCleanupObject{{
			AttachmentID: attachment.ID,
			TicketID:     ticket.ID,
			StoragePath:  attachment.StoragePath,
		}},
	})
	err = deliverer.Deliver(
		context.Background(),
		&models.OutboxDelivery{
			DestinationType: services.AttachmentCleanupOutboxDestination,
			DestinationID:   target.ID,
		},
		services.CloudEventEnvelope{
			Type:    "io.chronodesk.ticket.deleted.v1",
			Subject: fmt.Sprintf("ticket/%d", ticket.ID),
			Data:    data,
		},
	)
	if !errors.Is(err, services.ErrInvalidAttachmentName) {
		t.Fatalf("path traversal cleanup error = %v", err)
	}
}

func TestIsPublicCallbackIPRejectsSSRFNetworks(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{ip: "8.8.8.8", want: true},
		{ip: "2606:4700:4700::1111", want: true},
		{ip: "127.0.0.1", want: false},
		{ip: "10.0.0.1", want: false},
		{ip: "100.64.0.1", want: false},
		{ip: "169.254.169.254", want: false},
		{ip: "192.0.2.1", want: false},
		{ip: "::1", want: false},
		{ip: "fd00::1", want: false},
		{ip: "2001:db8::1", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			if got := isPublicCallbackIP(net.ParseIP(tt.ip)); got != tt.want {
				t.Fatalf("isPublicCallbackIP(%q) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestCloudEventRoutingKeepsStableEventIdentity(t *testing.T) {
	data, err := json.Marshal(map[string]any{"ticket_id": 42})
	if err != nil {
		t.Fatal(err)
	}
	event := services.CloudEventEnvelope{
		ID:      "event-1",
		Type:    "io.chronodesk.ticket.comment.created.v1",
		Subject: "ticket/42",
		Data:    data,
	}
	if got := ticketIDFromCloudEvent(event); got != 42 {
		t.Fatalf("ticketIDFromCloudEvent() = %d, want 42", got)
	}
	eventType, ok := webhookEventType(event)
	if !ok || eventType != models.WebhookEventTicketComment {
		t.Fatalf("webhookEventType() = (%q, %v)", eventType, ok)
	}

	event.Type = "io.chronodesk.ticket.transitioned.v1"
	event.Data = json.RawMessage(`{"ticket_id":42,"new_status":"resolved"}`)
	eventType, ok = webhookEventType(event)
	if !ok || eventType != models.WebhookEventTicketResolved {
		t.Fatalf("resolved webhookEventType() = (%q, %v)", eventType, ok)
	}
	event.Data = json.RawMessage(`{"ticket_id":42,"new_status":"closed"}`)
	eventType, ok = webhookEventType(event)
	if !ok || eventType != models.WebhookEventTicketClosed {
		t.Fatalf("closed webhookEventType() = (%q, %v)", eventType, ok)
	}

	event.Type = "io.chronodesk.ticket.sla.breached.v1"
	eventType, ok = webhookEventType(event)
	if !ok || eventType != models.WebhookEventSystemAlert {
		t.Fatalf("SLA webhookEventType() = (%q, %v)", eventType, ok)
	}

	event.Type = "io.chronodesk.automation.notification.requested.v1"
	event.Data = json.RawMessage(`{
		"ticket_id": 42,
		"notification": {
			"title": "Escalation required",
			"content": "Ticket 42 needs an owner."
		}
	}`)
	eventType, ok = webhookEventType(event)
	if !ok || eventType != models.WebhookEventAutomationNotification {
		t.Fatalf("automation notification webhookEventType() = (%q, %v)", eventType, ok)
	}
	notification := notificationEventFromCloudEvent(event, eventType)
	if notification.Title != "Escalation required" ||
		notification.Description != "Ticket 42 needs an owner." {
		t.Fatalf("automation notification payload was ignored: %#v", notification)
	}
}

func TestCloudEventQueueRoutingIncludesOldAndNewQueue(t *testing.T) {
	data, _ := json.Marshal(map[string]any{
		"old_queue": "payments",
		"new_queue": "platform",
	})
	queues := queueNamesFromCloudEvent(services.CloudEventEnvelope{Data: data})
	if len(queues) != 2 || queues[0] != "payments" || queues[1] != "platform" {
		t.Fatalf("queue routes = %#v", queues)
	}
}

func TestOutboxWebhookDeliveryUsesOneBoundedAttemptWithoutLocalRetry(t *testing.T) {
	var attempts atomic.Int32
	requests := make(chan struct {
		CloudEventID  string
		IdempotencyID string
		Body          []byte
	}, 1)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		attempts.Add(1)
		body, _ := io.ReadAll(request.Body)
		requests <- struct {
			CloudEventID  string
			IdempotencyID string
			Body          []byte
		}{
			CloudEventID:  request.Header.Get("X-CloudEvents-ID"),
			IdempotencyID: request.Header.Get("Idempotency-Key"),
			Body:          body,
		}
		http.Error(w, "temporary failure", http.StatusBadGateway)
	}))
	defer endpoint.Close()

	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlite handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&models.User{}, &models.WebhookConfig{}, &models.WebhookLog{}); err != nil {
		t.Fatalf("migrate webhook schema: %v", err)
	}
	user := models.User{
		Username:     "outbox-owner",
		Email:        "outbox-owner@example.com",
		PasswordHash: "not-a-real-password",
		Role:         models.RoleAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create webhook owner: %v", err)
	}
	config := models.WebhookConfig{
		Name:             "outbox-single-attempt",
		Provider:         models.WebhookProviderCustom,
		WebhookURL:       endpoint.URL,
		Status:           models.WebhookStatusActive,
		EnabledEventsObj: []models.WebhookEventType{models.WebhookEventTicketCreated},
		RetryCount:       1,
		RetryInterval:    1,
		TimeoutSeconds:   30,
		CreatedBy:        user.ID,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatalf("create webhook config: %v", err)
	}
	notifications := newWebhookTestNotificationService(db)
	deliverer, err := NewNativeOutboxDeliverer(db, notifications, nil)
	if err != nil {
		t.Fatalf("create outbox deliverer: %v", err)
	}
	eventData, _ := json.Marshal(map[string]any{"ticket_id": 42})
	event := services.CloudEventEnvelope{
		SpecVersion: "1.0",
		ID:          "event-single-attempt",
		Type:        "io.chronodesk.ticket.created.v1",
		Subject:     "ticket/42",
		Time:        time.Now().UTC(),
		Data:        eventData,
	}

	started := time.Now()
	delivery := &models.OutboxDelivery{
		ID:              "delivery-single-attempt",
		DestinationType: "webhook",
		DestinationID:   "config:" + strconv.FormatUint(uint64(config.ID), 10),
	}
	err = deliverer.Deliver(context.Background(), delivery, event)
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("non-2xx webhook attempt must fail for Outbox backoff")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("Outbox delivery made %d HTTP attempts, want exactly 1", got)
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("Outbox delivery slept inside worker for %s", elapsed)
	}
	request := <-requests
	if request.CloudEventID != event.ID || request.IdempotencyID != delivery.ID {
		t.Fatalf(
			"stable delivery headers cloud_event=%q idempotency=%q",
			request.CloudEventID,
			request.IdempotencyID,
		)
	}
	var requestBody map[string]any
	if err := json.Unmarshal(request.Body, &requestBody); err != nil {
		t.Fatal(err)
	}
	if requestBody["event_id"] != event.ID || requestBody["delivery_id"] != delivery.ID {
		t.Fatalf("stable event identity missing from webhook body: %#v", requestBody)
	}
	var logs []models.WebhookLog
	if err := db.Order("id ASC").Find(&logs).Error; err != nil {
		t.Fatalf("load webhook logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("Outbox delivery wrote %d attempt logs, want 1", len(logs))
	}
	if logs[0].Status != "failed" || logs[0].NextRetryAt != nil {
		t.Fatalf("Outbox attempt scheduled a competing legacy retry: %+v", logs[0])
	}
}

func TestConfiguredWebhookOutboxDeliveryFansOutDurablyAndIdempotently(t *testing.T) {
	var firstAttempts atomic.Int32
	firstEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		firstAttempts.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer firstEndpoint.Close()
	var secondAttempts atomic.Int32
	secondEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if secondAttempts.Add(1) == 1 {
			http.Error(w, "temporary failure", http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer secondEndpoint.Close()

	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlite handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(
		&models.User{},
		&models.WebhookConfig{},
		&models.WebhookLog{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatalf("migrate Outbox schema: %v", err)
	}
	user := models.User{
		Username:     "fanout-owner",
		Email:        "fanout-owner@example.com",
		PasswordHash: "not-a-real-password",
		Role:         models.RoleAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create owner: %v", err)
	}
	configs := []models.WebhookConfig{
		{
			Name:             "first",
			Provider:         models.WebhookProviderCustom,
			WebhookURL:       firstEndpoint.URL,
			Status:           models.WebhookStatusActive,
			EnabledEventsObj: []models.WebhookEventType{models.WebhookEventTicketCreated},
			RetryCount:       1,
			CreatedBy:        user.ID,
		},
		{
			Name:             "second",
			Provider:         models.WebhookProviderCustom,
			WebhookURL:       secondEndpoint.URL,
			Status:           models.WebhookStatusActive,
			EnabledEventsObj: []models.WebhookEventType{models.WebhookEventTicketCreated},
			RetryCount:       4,
			CreatedBy:        user.ID,
		},
	}
	for i := range configs {
		if err := db.Create(&configs[i]).Error; err != nil {
			t.Fatalf("create webhook %d: %v", i, err)
		}
	}
	now := time.Now().UTC()
	native := services.NewAgentNativeService(db, services.AgentNativeOptions{
		Now: func() time.Time { return now },
	})
	eventModel, err := native.CreateDomainEvent(context.Background(), services.DomainEventInput{
		Type:            "io.chronodesk.ticket.created.v1",
		Subject:         "ticket/42",
		Actor:           models.SystemActor("fanout-test"),
		ResourceVersion: 1,
		Data:            map[string]any{"ticket_id": 42},
	}, []services.OutboxTarget{{
		Type:        "webhook",
		ID:          webhookFanoutDestinationID,
		MaxAttempts: 8,
	}})
	if err != nil {
		t.Fatalf("create domain event: %v", err)
	}
	var parent models.OutboxDelivery
	if err := db.First(
		&parent,
		"event_id = ? AND destination_type = ? AND destination_id = ?",
		eventModel.ID,
		"webhook",
		webhookFanoutDestinationID,
	).Error; err != nil {
		t.Fatalf("load fanout delivery: %v", err)
	}
	deliverer, err := NewNativeOutboxDeliverer(db, newWebhookTestNotificationService(db), nil)
	if err != nil {
		t.Fatalf("create deliverer: %v", err)
	}
	envelope := services.CloudEventFromModel(eventModel)
	firstBatch, err := native.ProcessOutboxBatch(
		context.Background(),
		"webhook-fanout-worker",
		10,
		deliverer,
	)
	if err != nil {
		t.Fatalf("process webhook fanout: %v", err)
	}
	if firstBatch.Delivered != 1 || firstAttempts.Load() != 0 || secondAttempts.Load() != 0 {
		t.Fatalf(
			"fanout seed performed HTTP delivery: result=%+v attempts=(%d,%d)",
			firstBatch,
			firstAttempts.Load(),
			secondAttempts.Load(),
		)
	}
	// Reprocessing after a crash must not create duplicate child deliveries.
	if err := deliverer.Deliver(context.Background(), &parent, envelope); err != nil {
		t.Fatalf("repeat webhook fanout: %v", err)
	}
	var children []models.OutboxDelivery
	if err := db.
		Where("event_id = ? AND destination_type = ? AND destination_id LIKE ?",
			eventModel.ID,
			"webhook",
			webhookConfigPrefix+"%",
		).
		Order("destination_id ASC").
		Find(&children).Error; err != nil {
		t.Fatalf("load child deliveries: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("fanout created %d child deliveries, want 2", len(children))
	}
	maxAttempts := map[string]int{
		webhookConfigDestinationID(configs[0].ID): 2,
		webhookConfigDestinationID(configs[1].ID): 5,
	}
	for _, child := range children {
		if child.Status != models.OutboxDeliveryPending ||
			child.MaxAttempts != maxAttempts[child.DestinationID] {
			t.Fatalf("unexpected independent child delivery: %+v", child)
		}
	}
	var persistedEvent models.DomainEvent
	if err := db.First(&persistedEvent, "id = ?", eventModel.ID).Error; err != nil {
		t.Fatalf("reload event after fanout: %v", err)
	}
	if persistedEvent.PublishedAt != nil {
		t.Fatal("fanout seed published event before child deliveries completed")
	}

	now = now.Add(time.Minute)
	secondBatch, err := native.ProcessOutboxBatch(
		context.Background(),
		"webhook-delivery-worker",
		10,
		deliverer,
	)
	if err != nil {
		t.Fatalf("process independent webhook deliveries: %v", err)
	}
	if secondBatch.Delivered != 1 || secondBatch.Failed != 1 ||
		firstAttempts.Load() != 1 || secondAttempts.Load() != 1 {
		t.Fatalf(
			"unexpected independent delivery result=%+v attempts=(%d,%d)",
			secondBatch,
			firstAttempts.Load(),
			secondAttempts.Load(),
		)
	}

	now = now.Add(3 * time.Second)
	thirdBatch, err := native.ProcessOutboxBatch(
		context.Background(),
		"webhook-retry-worker",
		10,
		deliverer,
	)
	if err != nil {
		t.Fatalf("process Outbox-managed retry: %v", err)
	}
	if thirdBatch.Delivered != 1 || firstAttempts.Load() != 1 || secondAttempts.Load() != 2 {
		t.Fatalf(
			"successful target was redelivered: result=%+v attempts=(%d,%d)",
			thirdBatch,
			firstAttempts.Load(),
			secondAttempts.Load(),
		)
	}
	if err := db.First(&persistedEvent, "id = ?", eventModel.ID).Error; err != nil {
		t.Fatalf("reload published event: %v", err)
	}
	if persistedEvent.PublishedAt == nil {
		t.Fatal("event was not published after every independent target succeeded")
	}
}

func newWebhookTestNotificationService(db *gorm.DB) *services.NotificationService {
	return services.NewNotificationServiceWithClientFactory(
		db,
		nil,
		services.WebhookClientFactoryFunc(func(
			context.Context,
			*url.URL,
			time.Duration,
		) (*http.Client, error) {
			return http.DefaultClient, nil
		}),
	)
}
