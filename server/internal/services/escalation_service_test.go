package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestHasFirstResponseIgnoresSystemComments(t *testing.T) {
	db := openTestDB(t)

	if err := db.AutoMigrate(&models.User{}, &models.Ticket{}, &models.TicketComment{}); err != nil {
		t.Fatalf("failed to migrate schemas: %v", err)
	}

	user := models.User{
		Username:     "agent-response",
		Email:        "agent-response@example.com",
		PasswordHash: "hashed",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	ticket := models.Ticket{
		TicketNumber: "T-RESP-001",
		Title:        "Response check",
		Description:  "response test",
		Priority:     models.TicketPriorityNormal,
		Status:       models.TicketStatusOpen,
		Type:         models.TicketTypeRequest,
		Source:       models.TicketSourceWeb,
		CreatedByID:  &user.ID,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatalf("failed to seed ticket: %v", err)
	}

	systemComment := models.TicketComment{
		TicketID:    ticket.ID,
		ActorType:   models.ActorTypeSystem,
		ActorID:     "test-system",
		Content:     "system note",
		ContentType: "text",
		Type:        models.CommentTypeSystem,
	}
	if err := db.Create(&systemComment).Error; err != nil {
		t.Fatalf("failed to seed system comment: %v", err)
	}

	svc := NewEscalationService(db)
	ctx := testProjectOperationContext(
		t,
		db,
		models.SystemActor(slaMonitorActorID),
	)
	if err := db.First(&ticket, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}

	if svc.hasFirstResponse(ctx, ticket.ID) {
		t.Fatalf("expected no first response when only system comments exist")
	}

	publicComment := models.TicketComment{
		OrganizationID: ticket.OrganizationID,
		ProjectID:      ticket.ProjectID,
		TicketID:       ticket.ID,
		UserID:         &user.ID,
		ActorType:      models.ActorTypeHuman,
		ActorID:        models.HumanActor(user.ID).ID,
		Content:        "public response",
		ContentType:    "text",
		Type:           models.CommentTypePublic,
	}
	if err := db.Create(&publicComment).Error; err != nil {
		t.Fatalf("failed to seed public comment: %v", err)
	}

	if !svc.hasFirstResponse(ctx, ticket.ID) {
		t.Fatalf("expected first response when public comment exists")
	}
}

func TestSLAViolationUsesNativeCommandsAndIsStableAcrossScans(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.SLAConfig{},
		&models.Ticket{},
		&models.TicketComment{},
		&models.TicketHistory{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.IdempotencyRecord{},
	); err != nil {
		t.Fatalf("migrate SLA native schema: %v", err)
	}
	systemUser := models.User{
		Username: "sla-system", Email: "sla-system@example.com",
		PasswordHash: "hash", PlatformRole: models.PlatformRoleMember, Status: models.UserStatusActive,
	}
	manager := models.User{
		Username: "sla-manager", Email: "sla-manager@example.com",
		PasswordHash: "hash", PlatformRole: models.PlatformRolePlatformAdmin, Status: models.UserStatusActive,
	}
	if err := db.Create(&systemUser).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&manager).Error; err != nil {
		t.Fatal(err)
	}
	rules, _ := json.Marshal([]models.EscalationRule{
		{TriggerMinutes: 10, Action: "escalate_to_manager", TargetUserID: &manager.ID},
		{TriggerMinutes: 20, Action: "change_priority"},
		{TriggerMinutes: 30, Action: "notify_admin", NotifyUsers: []uint{manager.ID}},
	})
	config := models.SLAConfig{
		Name:            "native SLA",
		IsActive:        true,
		ResponseTime:    30,
		ResolutionTime:  60,
		EscalationRules: string(rules),
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		TicketNumber: "SLA-NATIVE-001",
		Title:        "Native SLA breach",
		Description:  "exercise native SLA commands",
		Type:         models.TicketTypeIncident,
		Priority:     models.TicketPriorityLow,
		Status:       models.TicketStatusOpen,
		Source:       models.TicketSourceWeb,
		CreatedByID:  &systemUser.ID,
		Version:      1,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	ctx := testProjectOperationContext(
		t,
		db,
		models.SystemActor(slaMonitorActorID),
	)
	ensureTestHumanProjectRole(
		t,
		db,
		ctx,
		manager.ID,
		models.ProjectRoleManager,
	)
	if err := db.First(&ticket, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&config, config.ID).Error; err != nil {
		t.Fatal(err)
	}
	native := NewAgentNativeService(db, AgentNativeOptions{
		DefaultOutboxTargets: []OutboxTarget{{
			Type: "event_stream", ID: "default", MaxAttempts: 8,
		}},
	})
	service := NewEscalationService(db)
	service.SetAgentNativeService(native)
	base := time.Date(2026, time.July, 28, 10, 0, 0, 0, time.UTC)
	status := &TicketSLAStatus{
		TicketID:                 ticket.ID,
		ResponseDeadline:         base,
		ResolutionDeadline:       base.Add(time.Hour),
		IsResponseOverdue:        true,
		IsResolutionOverdue:      true,
		ResponseOverdueMinutes:   90,
		ResolutionOverdueMinutes: 60,
		SLAConfig:                &config,
	}
	if err := service.HandleSLAViolation(ctx, &ticket, status); err != nil {
		t.Fatalf("handle first SLA breach: %v", err)
	}

	var persisted models.Ticket
	if err := db.First(&persisted, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !persisted.SLABreached || persisted.SLADueDate == nil ||
		!persisted.SLADueDate.Equal(status.ResolutionDeadline) ||
		!persisted.IsEscalated ||
		persisted.AssignedToID == nil || *persisted.AssignedToID != manager.ID ||
		persisted.AssignedToActorType != models.ActorTypeHuman ||
		persisted.AssignedToActorID != strconv.FormatUint(uint64(manager.ID), 10) ||
		persisted.Priority != models.TicketPriorityCritical ||
		persisted.Version != 7 {
		t.Fatalf("unexpected SLA ticket state: %+v", persisted)
	}

	var comments []models.TicketComment
	if err := db.Order("id ASC").Find(&comments, "ticket_id = ?", ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if len(comments) != 3 {
		t.Fatalf("system comments=%d, want 3", len(comments))
	}
	for _, comment := range comments {
		if comment.ActorType != models.ActorTypeSystem ||
			comment.ActorID != slaMonitorActorID ||
			comment.Type != models.CommentTypeSystem ||
			comment.UserID != nil {
			t.Fatalf("comment bypassed native actor provenance: %+v", comment)
		}
	}

	var events []models.DomainEvent
	if err := db.Order("created_at ASC, id ASC").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 6 {
		t.Fatalf("domain events=%d, want 6", len(events))
	}
	correlationID := events[0].CorrelationID
	if correlationID == "" {
		t.Fatal("SLA event correlation ID is empty")
	}
	eventTypes := make(map[string]int)
	eventIDs := make(map[string]struct{}, len(events))
	for _, event := range events {
		eventIDs[event.ID] = struct{}{}
	}
	for _, event := range events {
		eventTypes[event.Type]++
		if event.ActorType != models.ActorTypeSystem ||
			event.ActorID != slaMonitorActorID ||
			event.TraceID != correlationID ||
			event.CorrelationID != correlationID {
			t.Fatalf("invalid SLA event provenance/correlation: %+v", event)
		}
		if event.Type == SLABreachEventType {
			if event.CausationID != "" {
				t.Fatalf("root SLA breach points to a non-event cause: %+v", event)
			}
			continue
		}
		if _, exists := eventIDs[event.CausationID]; !exists {
			t.Fatalf("SLA child event has no persisted cause: %+v", event)
		}
	}
	if eventTypes["io.chronodesk.ticket.sla.breached.v1"] != 1 ||
		eventTypes["io.chronodesk.ticket.escalated.v1"] != 1 ||
		eventTypes["io.chronodesk.ticket.updated.v1"] != 1 ||
		eventTypes["io.chronodesk.ticket.comment.created.v1"] != 3 {
		t.Fatalf("unexpected SLA event types: %#v", eventTypes)
	}

	var historyCount, outboxCount, idempotencyCount int64
	if err := db.Model(&models.TicketHistory{}).Count(&historyCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.OutboxDelivery{}).Count(&outboxCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.IdempotencyRecord{}).Count(&idempotencyCount).Error; err != nil {
		t.Fatal(err)
	}
	if historyCount != 6 || outboxCount != 8 || idempotencyCount != 7 {
		t.Fatalf(
			"native audit graph mismatch: history=%d outbox=%d idempotency=%d",
			historyCount,
			outboxCount,
			idempotencyCount,
		)
	}
	var assignmentNotificationCount int64
	if err := db.Model(&models.OutboxDelivery{}).
		Where(
			"destination_type = ? AND destination_id = ?",
			NotificationOutboxDestination,
			fmt.Sprintf("%s:%d", models.NotificationTypeTicketAssigned, manager.ID),
		).
		Count(&assignmentNotificationCount).Error; err != nil {
		t.Fatal(err)
	}
	if assignmentNotificationCount != 1 {
		t.Fatalf(
			"SLA manager assignment notifications=%d, want one durable notification delivery",
			assignmentNotificationCount,
		)
	}
	if err := db.First(&config, config.ID).Error; err != nil {
		t.Fatal(err)
	}
	if config.AppliedCount != 1 || config.ViolationCount != 1 {
		t.Fatalf("SLA stats were not recorded once: %+v", config)
	}

	// The observed delay changes every scan, but the occurrence and rule
	// identities do not. No command may be repeated.
	status.ResponseOverdueMinutes = 180
	status.ResolutionOverdueMinutes = 120
	if err := service.HandleSLAViolation(context.Background(), &ticket, status); err != nil {
		t.Fatalf("handle repeated SLA scan: %v", err)
	}
	var repeated models.Ticket
	if err := db.First(&repeated, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if repeated.Version != persisted.Version {
		t.Fatalf("repeated scan changed version: %d -> %d", persisted.Version, repeated.Version)
	}
	var repeatedComments, repeatedEvents, repeatedHistory, repeatedOutbox, repeatedIdempotency int64
	_ = db.Model(&models.TicketComment{}).Count(&repeatedComments).Error
	_ = db.Model(&models.DomainEvent{}).Count(&repeatedEvents).Error
	_ = db.Model(&models.TicketHistory{}).Count(&repeatedHistory).Error
	_ = db.Model(&models.OutboxDelivery{}).Count(&repeatedOutbox).Error
	_ = db.Model(&models.IdempotencyRecord{}).Count(&repeatedIdempotency).Error
	if repeatedComments != 3 || repeatedEvents != 6 || repeatedHistory != 6 ||
		repeatedOutbox != 8 || repeatedIdempotency != 7 {
		t.Fatalf(
			"repeated scan duplicated side effects: comments=%d events=%d history=%d outbox=%d idempotency=%d",
			repeatedComments,
			repeatedEvents,
			repeatedHistory,
			repeatedOutbox,
			repeatedIdempotency,
		)
	}
	if err := db.First(&config, config.ID).Error; err != nil {
		t.Fatal(err)
	}
	if config.AppliedCount != 1 || config.ViolationCount != 1 {
		t.Fatalf("repeated scan duplicated SLA stats: %+v", config)
	}
}

func TestSLARuleCanTriggerAfterBreachWasAlreadyRecorded(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.SLAConfig{},
		&models.Ticket{},
		&models.TicketComment{},
		&models.TicketHistory{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.IdempotencyRecord{},
	); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username: "sla-threshold", Email: "sla-threshold@example.com",
		PasswordHash: "hash", PlatformRole: models.PlatformRoleMember, Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	rules, _ := json.Marshal([]models.EscalationRule{{
		TriggerMinutes: 60,
		Action:         "notify_admin",
	}})
	config := models.SLAConfig{
		Name: "threshold SLA", IsActive: true,
		ResponseTime: 10, ResolutionTime: 20, EscalationRules: string(rules),
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		TicketNumber: "SLA-THRESHOLD-001", Title: "threshold", Description: "threshold",
		Type: models.TicketTypeRequest, Priority: models.TicketPriorityNormal,
		Status: models.TicketStatusOpen, Source: models.TicketSourceWeb,
		CreatedByID: &user.ID, Version: 1,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	ctx := testProjectOperationContext(
		t,
		db,
		models.SystemActor(slaMonitorActorID),
	)
	if err := db.First(&ticket, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&config, config.ID).Error; err != nil {
		t.Fatal(err)
	}
	native := NewAgentNativeService(db, AgentNativeOptions{
		DefaultOutboxTargets: []OutboxTarget{{Type: "event_stream", ID: "default"}},
	})
	service := NewEscalationService(db)
	service.SetAgentNativeService(native)
	deadline := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	status := &TicketSLAStatus{
		TicketID: ticket.ID, ResponseDeadline: deadline,
		ResolutionDeadline: deadline.Add(time.Hour),
		IsResponseOverdue:  true, ResponseOverdueMinutes: 10,
		SLAConfig: &config,
	}
	if err := service.HandleSLAViolation(ctx, &ticket, status); err != nil {
		t.Fatal(err)
	}
	var comments int64
	_ = db.Model(&models.TicketComment{}).Count(&comments).Error
	if comments != 0 {
		t.Fatalf("rule triggered before threshold: comments=%d", comments)
	}
	status.ResponseOverdueMinutes = 60
	if err := service.HandleSLAViolation(ctx, &ticket, status); err != nil {
		t.Fatal(err)
	}
	_ = db.Model(&models.TicketComment{}).Count(&comments).Error
	if comments != 1 {
		t.Fatalf("rule did not trigger after threshold: comments=%d", comments)
	}
	status.ResponseOverdueMinutes = 120
	if err := service.HandleSLAViolation(ctx, &ticket, status); err != nil {
		t.Fatal(err)
	}
	_ = db.Model(&models.TicketComment{}).Count(&comments).Error
	if comments != 1 {
		t.Fatalf("threshold rule repeated comment: comments=%d", comments)
	}
}

func TestSLABreachOutboxRecoversFromSnapshotAfterStateAndConfigChange(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.SLAConfig{},
		&models.Ticket{},
		&models.TicketComment{},
		&models.TicketHistory{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.IdempotencyRecord{},
	); err != nil {
		t.Fatal(err)
	}
	systemUser := models.User{
		Username:     "sla-recovery",
		Email:        "sla-recovery@example.com",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&systemUser).Error; err != nil {
		t.Fatal(err)
	}
	originalRules := []models.EscalationRule{
		{
			TriggerMinutes: 30,
			Action:         "notify_admin",
			NotifyUsers:    []uint{systemUser.ID},
		},
		{
			TriggerMinutes: 30,
			Action:         "change_priority",
		},
	}
	encodedRules, _ := json.Marshal(originalRules)
	config := models.SLAConfig{
		Name:            "recoverable SLA",
		IsActive:        true,
		ResponseTime:    15,
		ResolutionTime:  45,
		EscalationRules: string(encodedRules),
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		TicketNumber: "SLA-RECOVERY-001",
		Title:        "recover from Outbox",
		Description:  "breach commit survives process loss",
		Type:         models.TicketTypeIncident,
		Priority:     models.TicketPriorityNormal,
		Status:       models.TicketStatusOpen,
		Source:       models.TicketSourceWeb,
		CreatedByID:  &systemUser.ID,
		Version:      1,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	ctx := testProjectOperationContext(
		t,
		db,
		models.SystemActor(slaMonitorActorID),
	)
	if err := db.First(&ticket, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&config, config.ID).Error; err != nil {
		t.Fatal(err)
	}
	native := NewAgentNativeService(db, AgentNativeOptions{
		DefaultOutboxTargets: []OutboxTarget{{
			Type: "event_stream", ID: "default", MaxAttempts: 8,
		}},
	})
	service := NewEscalationService(db)
	service.SetAgentNativeService(native)
	responseDeadline := time.Date(2026, time.July, 28, 8, 0, 0, 0, time.UTC)
	status := &TicketSLAStatus{
		TicketID:                 ticket.ID,
		ResponseDeadline:         responseDeadline,
		ResolutionDeadline:       responseDeadline.Add(45 * time.Minute),
		IsResponseOverdue:        true,
		ResponseOverdueMinutes:   35,
		ResolutionOverdueMinutes: 0,
		SLAConfig:                &config,
	}
	execution, err := newSLAExecutionContext(&ticket, status)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := newSLAEscalationEventData(
		ticket.ID,
		status,
		originalRules,
		execution,
	)

	// Simulate a crash immediately after the breach transaction: the ticket,
	// event, idempotency completion and dedicated continuation delivery commit,
	// but no synchronous statistics or rule execution is invoked.
	breachEvent, err := service.markSLABreach(
		ctx,
		&ticket,
		snapshot,
		execution,
	)
	if err != nil {
		t.Fatalf("commit SLA breach event: %v", err)
	}
	var frozen slaEscalationEventData
	if err := json.Unmarshal(breachEvent.Data, &frozen); err != nil {
		t.Fatalf("decode frozen SLA snapshot: %v", err)
	}
	if frozen.SLAOccurrenceID != execution.OccurrenceID ||
		frozen.SLAConfigID != config.ID ||
		!frozen.ResponseDeadline.Equal(status.ResponseDeadline) ||
		!frozen.ResolutionDeadline.Equal(status.ResolutionDeadline) ||
		frozen.ResponseOverdueMinutes != 35 ||
		len(frozen.EscalationRules) != 2 ||
		frozen.EscalationRules[0].Action != "notify_admin" ||
		frozen.EscalationRules[1].Action != "change_priority" {
		t.Fatalf("breach event did not freeze SLA execution inputs: %+v", frozen)
	}
	var continuation models.OutboxDelivery
	if err := db.Where(
		"event_id = ? AND destination_type = ?",
		breachEvent.ID,
		SLAEscalationOutboxDestination,
	).First(&continuation).Error; err != nil {
		t.Fatalf("dedicated SLA continuation was not committed: %v", err)
	}
	var commentsBefore int64
	_ = db.Model(&models.TicketComment{}).Count(&commentsBefore).Error
	if commentsBefore != 0 {
		t.Fatalf("SLA rules ran before recovery: comments=%d", commentsBefore)
	}
	if err := db.First(&config, config.ID).Error; err != nil {
		t.Fatal(err)
	}
	if config.AppliedCount != 0 || config.ViolationCount != 0 {
		t.Fatalf("SLA statistics ran before recovery: %+v", config)
	}

	replacementRules, _ := json.Marshal([]models.EscalationRule{})
	if err := db.Model(&models.Ticket{}).
		Where("id = ?", ticket.ID).
		Update("status", models.TicketStatusClosed).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.SLAConfig{}).
		Where("id = ?", config.ID).
		Updates(map[string]any{
			"is_active":        false,
			"escalation_rules": string(replacementRules),
		}).Error; err != nil {
		t.Fatal(err)
	}

	result, err := native.ProcessOutboxBatch(
		context.Background(),
		"sla-recovery-worker",
		10,
		OutboxDeliverFunc(func(
			ctx context.Context,
			delivery *models.OutboxDelivery,
			event CloudEventEnvelope,
		) error {
			if delivery.DestinationType != SLAEscalationOutboxDestination {
				return nil
			}
			return service.ExecuteDomainEvent(ctx, event)
		}),
	)
	if err != nil {
		t.Fatalf("recover SLA continuation: %v", err)
	}
	if result.Claimed != 2 || result.Delivered != 2 || result.Failed != 0 {
		t.Fatalf("unexpected recovery batch result: %+v", result)
	}

	var recovered models.Ticket
	if err := db.First(&recovered, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if recovered.Status != models.TicketStatusClosed ||
		recovered.Priority != models.TicketPriorityHigh ||
		recovered.Version != 5 {
		t.Fatalf("recovery did not use frozen rules against current ticket: %+v", recovered)
	}
	var recoveredComments []models.TicketComment
	if err := db.Find(&recoveredComments, "ticket_id = ?", ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if len(recoveredComments) != 2 {
		t.Fatalf("recovered SLA comment lost system provenance: %+v", recoveredComments)
	}
	for _, comment := range recoveredComments {
		if comment.ActorType != models.ActorTypeSystem ||
			comment.ActorID != slaMonitorActorID {
			t.Fatalf("recovered SLA comment lost system provenance: %+v", comment)
		}
	}
	if err := db.First(&config, config.ID).Error; err != nil {
		t.Fatal(err)
	}
	if config.AppliedCount != 1 || config.ViolationCount != 1 {
		t.Fatalf("recovered SLA statistics were not recorded once: %+v", config)
	}
	if err := db.First(&continuation, "id = ?", continuation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if continuation.Status != models.OutboxDeliverySucceeded {
		t.Fatalf("SLA continuation delivery was not acknowledged: %+v", continuation)
	}

	if err := service.ExecuteDomainEvent(
		context.Background(),
		CloudEventFromModel(breachEvent),
	); err != nil {
		t.Fatalf("replay recovered SLA event: %v", err)
	}
	var replayedComments int64
	_ = db.Model(&models.TicketComment{}).Count(&replayedComments).Error
	if replayedComments != 2 {
		t.Fatalf("replayed SLA event duplicated comments: %d", replayedComments)
	}
	if err := db.First(&config, config.ID).Error; err != nil {
		t.Fatal(err)
	}
	if config.AppliedCount != 1 || config.ViolationCount != 1 {
		t.Fatalf("replayed SLA event duplicated statistics: %+v", config)
	}
}

func TestSLAViolationRequiresNativeServiceAndSLAFieldsAreSystemControlled(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.SLAConfig{},
		&models.Ticket{},
		&models.TicketHistory{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.IdempotencyRecord{},
	); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username: "sla-guard", Email: "sla-guard@example.com",
		PasswordHash: "hash", PlatformRole: models.PlatformRoleMember, Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	config := models.SLAConfig{
		Name: "guard SLA", IsActive: true, ResponseTime: 10, ResolutionTime: 20,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		TicketNumber: "SLA-GUARD-001", Title: "guard", Description: "guard",
		Type: models.TicketTypeRequest, Priority: models.TicketPriorityNormal,
		Status: models.TicketStatusOpen, Source: models.TicketSourceWeb,
		CreatedByID: &user.ID, Version: 1,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	workerCtx := testProjectOperationContext(
		t,
		db,
		models.SystemActor(slaMonitorActorID),
	)
	if err := db.First(&ticket, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	status := &TicketSLAStatus{
		TicketID: ticket.ID, ResponseDeadline: time.Now().Add(-time.Hour),
		ResolutionDeadline:  time.Now().Add(-time.Minute),
		IsResolutionOverdue: true, ResolutionOverdueMinutes: 1, SLAConfig: &config,
	}
	service := NewEscalationService(db)
	if err := service.CheckSLAViolations(context.Background()); err == nil {
		t.Fatal("legacy SLA scan ran without AgentNative")
	}
	if err := service.HandleSLAViolation(workerCtx, &ticket, status); err == nil {
		t.Fatal("legacy escalation service performed a raw SLA write without AgentNative")
	}
	var unchanged models.Ticket
	if err := db.First(&unchanged, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if unchanged.Version != 1 || unchanged.SLABreached {
		t.Fatalf("legacy path mutated ticket: %+v", unchanged)
	}

	native := NewAgentNativeService(db)
	humanCtx := testProjectOperationContext(t, db, models.HumanActor(user.ID))
	_, err := native.UpdateTicketVersion(humanCtx, VersionedTicketUpdateInput{
		TicketID:        ticket.ID,
		ExpectedVersion: 1,
		Actor:           models.HumanActor(user.ID),
		Changes:         map[string]any{"sla_breached": true},
	})
	if !errors.Is(err, ErrCommandScopeMismatch) {
		t.Fatalf("non-system actor wrote SLA state: %v", err)
	}
}
