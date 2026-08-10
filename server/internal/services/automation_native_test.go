package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
)

func setupNativeAutomationTest(
	t *testing.T,
) (*AgentNativeService, *AutomationService, models.User) {
	t.Helper()
	db := openAgentNativeTestDB(t)
	if err := db.AutoMigrate(&models.AutomationRule{}, &models.AutomationLog{}); err != nil {
		t.Fatalf("migrate automation models: %v", err)
	}
	user := seedActorUser(t, db, "automation-native")
	_ = testProjectOperationContext(
		t,
		db,
		models.SystemActor(automationActorID),
	)
	native := NewAgentNativeService(db, AgentNativeOptions{
		DefaultOutboxTargets: []OutboxTarget{
			{Type: "automation", ID: "rules", MaxAttempts: 4},
		},
	})
	return native, NewAutomationServiceWithAgentNative(db, native), user
}

func automationTestDeliverer(service *AutomationService) OutboxDeliverer {
	return OutboxDeliverFunc(func(
		ctx context.Context,
		delivery *models.OutboxDelivery,
		event CloudEventEnvelope,
	) error {
		if delivery.DestinationType != "automation" {
			return fmt.Errorf("unexpected destination %q", delivery.DestinationType)
		}
		return service.ExecuteDomainEvent(ctx, event)
	})
}

func automationWorkerTestContext(
	t *testing.T,
	service *AutomationService,
) context.Context {
	t.Helper()
	return testProjectOperationContext(
		t,
		service.db,
		models.SystemActor(automationActorID),
	)
}

func createAutomationRule(
	t *testing.T,
	service *AutomationService,
	trigger string,
	actions ...models.RuleAction,
) models.AutomationRule {
	t.Helper()
	var project models.Project
	if err := service.db.Where(
		"key = ?",
		models.ProjectKey("TEST"),
	).First(&project).Error; err != nil {
		t.Fatalf("load automation test project: %v", err)
	}
	rule := models.AutomationRule{
		OrganizationID: project.OrganizationID,
		ProjectID:      project.ID,
		Name:           "native " + trigger,
		RuleType:       "assignment",
		IsActive:       true,
		Priority:       1,
		TriggerEvent:   trigger,
	}
	if err := rule.SetConditions(nil); err != nil {
		t.Fatalf("set conditions: %v", err)
	}
	if err := rule.SetActions(actions); err != nil {
		t.Fatalf("set actions: %v", err)
	}
	if err := service.db.Create(&rule).Error; err != nil {
		t.Fatalf("create automation rule: %v", err)
	}
	return rule
}

func createAutomationTestTicket(
	t *testing.T,
	native *AgentNativeService,
	user models.User,
) *NativeTicketCreateResult {
	t.Helper()
	ctx := testProjectOperationContext(t, native.db, models.HumanActor(user.ID))
	result, err := native.CreateNativeTicket(ctx, NativeTicketCreateInput{
		Request: models.TicketCreateRequest{
			Title:       "Agent-native automation",
			Description: "created through the transactional domain service",
			Type:        models.TicketTypeRequest,
			Priority:    models.TicketPriorityNormal,
			Source:      models.TicketSourceWeb,
		},
		Actor:      models.HumanActor(user.ID),
		TrustLevel: models.TicketTrustLevelVerified,
	})
	if err != nil {
		t.Fatalf("create native ticket: %v", err)
	}
	return result
}

func expireAutomationRuleExecutionClaim(
	t *testing.T,
	service *AutomationService,
	rootEventID string,
	ruleID uint,
) {
	t.Helper()
	if err := service.db.Model(&models.IdempotencyRecord{}).
		Where(
			"actor_type = ? AND actor_id = ? AND operation = ? AND key = ?",
			models.ActorTypeSystem,
			automationActorID,
			automationRuleExecutionOperation,
			automationRuleExecutionKey(rootEventID, ruleID),
		).
		Update("expires_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatalf("expire automation rule execution claim: %v", err)
	}
}

func TestDomainEventAutomationUsesNativeCommandsAndIsIdempotent(t *testing.T) {
	native, automation, user := setupNativeAutomationTest(t)
	rule := createAutomationRule(
		t,
		automation,
		eventcontract.TicketCreatedEventType,
		models.RuleAction{
			Type:   "set_priority",
			Params: map[string]interface{}{"priority": string(models.TicketPriorityHigh)},
		},
		models.RuleAction{
			Type:   "add_comment",
			Params: map[string]interface{}{"content": "created rule completed"},
		},
	)
	created := createAutomationTestTicket(t, native, user)

	batch, err := native.ProcessOutboxBatch(
		context.Background(),
		"automation-worker-1",
		10,
		automationTestDeliverer(automation),
	)
	if err != nil {
		t.Fatalf("process created event: %v", err)
	}
	if batch.Delivered != 1 {
		t.Fatalf("delivered=%d, want 1", batch.Delivered)
	}

	var ticket models.Ticket
	if err := automation.db.First(&ticket, created.Ticket.ID).Error; err != nil {
		t.Fatalf("reload automated ticket: %v", err)
	}
	if ticket.Priority != models.TicketPriorityHigh || ticket.Version != 3 || ticket.CommentCount != 1 {
		t.Fatalf(
			"unexpected automated ticket: priority=%s version=%d comments=%d",
			ticket.Priority,
			ticket.Version,
			ticket.CommentCount,
		)
	}

	var comment models.TicketComment
	if err := automation.db.Where("ticket_id = ?", ticket.ID).First(&comment).Error; err != nil {
		t.Fatalf("load automated comment: %v", err)
	}
	if comment.ActorType != models.ActorTypeSystem || comment.ActorID != automationActorID {
		t.Fatalf("comment actor=%s/%s, want system/%s", comment.ActorType, comment.ActorID, automationActorID)
	}

	var generated []models.DomainEvent
	if err := automation.db.
		Where("causation_id = ?", created.Event.ID).
		Order("resource_version ASC").
		Find(&generated).Error; err != nil {
		t.Fatalf("load generated events: %v", err)
	}
	if len(generated) != 2 {
		t.Fatalf("generated events=%d, want 2", len(generated))
	}
	for _, event := range generated {
		if event.ActorType != models.ActorTypeSystem ||
			event.ActorID != automationActorID ||
			event.CorrelationID != created.Event.ID {
			t.Fatalf("event lost automation provenance: %+v", event)
		}
	}

	for actionIndex := 0; actionIndex < 2; actionIndex++ {
		key := automationActionKey(created.Event.ID, rule.ID, actionIndex)
		var record models.IdempotencyRecord
		if err := automation.db.
			Where(
				"actor_type = ? AND actor_id = ? AND operation = ? AND key = ?",
				models.ActorTypeSystem,
				automationActorID,
				automationActionOperation,
				key,
			).
			First(&record).Error; err != nil {
			t.Fatalf("load action %d idempotency: %v", actionIndex, err)
		}
		if record.State != models.IdempotencyStateCompleted {
			t.Fatalf("action %d idempotency state=%s", actionIndex, record.State)
		}
		if record.ExpiresAt.Before(time.Now().Add(300 * 24 * time.Hour)) {
			t.Fatalf("action %d replay retention is too short: %s", actionIndex, record.ExpiresAt)
		}
	}

	// Generated automation events are delivered back to the same consumer.
	// The system actor/causation guard must acknowledge them without creating a
	// recursive rule chain.
	if _, err := native.ProcessOutboxBatch(
		context.Background(),
		"automation-worker-2",
		10,
		automationTestDeliverer(automation),
	); err != nil {
		t.Fatalf("process generated events: %v", err)
	}
	var statsBeforeReplay models.AutomationRule
	if err := automation.db.First(&statsBeforeReplay, rule.ID).Error; err != nil {
		t.Fatalf("load rule statistics before replay: %v", err)
	}
	if statsBeforeReplay.ExecutionCount != 1 ||
		statsBeforeReplay.SuccessCount != 1 ||
		statsBeforeReplay.FailureCount != 0 {
		t.Fatalf(
			"unexpected rule statistics before replay: executions=%d successes=%d failures=%d",
			statsBeforeReplay.ExecutionCount,
			statsBeforeReplay.SuccessCount,
			statsBeforeReplay.FailureCount,
		)
	}

	var originalDelivery models.OutboxDelivery
	if err := automation.db.
		Where("event_id = ? AND destination_type = ?", created.Event.ID, "automation").
		First(&originalDelivery).Error; err != nil {
		t.Fatalf("load original delivery: %v", err)
	}
	if err := automation.db.Model(&models.OutboxDelivery{}).
		Where("id = ?", originalDelivery.ID).
		Updates(map[string]any{
			"status":          models.OutboxDeliveryFailed,
			"next_attempt_at": time.Now(),
			"last_error":      "simulate a replayable delivery failure",
			"delivered_at":    nil,
		}).Error; err != nil {
		t.Fatalf("make original delivery replayable: %v", err)
	}
	if err := native.ReplayOutbox(
		automationWorkerTestContext(t, automation),
		originalDelivery.ID,
	); err != nil {
		t.Fatalf("replay original delivery: %v", err)
	}
	if _, err := native.ProcessOutboxBatch(
		context.Background(),
		"automation-worker-3",
		10,
		automationTestDeliverer(automation),
	); err != nil {
		t.Fatalf("process replay: %v", err)
	}

	var eventCount int64
	if err := automation.db.Model(&models.DomainEvent{}).
		Where("subject = ?", fmt.Sprintf("ticket/%d", ticket.ID)).
		Count(&eventCount).Error; err != nil {
		t.Fatalf("count ticket events: %v", err)
	}
	var commentCount int64
	if err := automation.db.Model(&models.TicketComment{}).
		Where("ticket_id = ?", ticket.ID).
		Count(&commentCount).Error; err != nil {
		t.Fatalf("count comments: %v", err)
	}
	if eventCount != 3 || commentCount != 1 {
		t.Fatalf("replay duplicated effects: events=%d comments=%d", eventCount, commentCount)
	}
	if err := automation.db.First(&ticket, ticket.ID).Error; err != nil {
		t.Fatalf("reload replayed ticket: %v", err)
	}
	if ticket.Version != 3 {
		t.Fatalf("replay changed version to %d", ticket.Version)
	}
	var statsAfterReplay models.AutomationRule
	if err := automation.db.First(&statsAfterReplay, rule.ID).Error; err != nil {
		t.Fatalf("load rule statistics after replay: %v", err)
	}
	if statsAfterReplay.ExecutionCount != statsBeforeReplay.ExecutionCount ||
		statsAfterReplay.SuccessCount != statsBeforeReplay.SuccessCount ||
		statsAfterReplay.FailureCount != statsBeforeReplay.FailureCount {
		t.Fatalf(
			"replay changed rule statistics: before=%d/%d/%d after=%d/%d/%d",
			statsBeforeReplay.ExecutionCount,
			statsBeforeReplay.SuccessCount,
			statsBeforeReplay.FailureCount,
			statsAfterReplay.ExecutionCount,
			statsAfterReplay.SuccessCount,
			statsAfterReplay.FailureCount,
		)
	}
}

func TestNativeUpdatedEventTriggersAutomationComment(t *testing.T) {
	native, automation, user := setupNativeAutomationTest(t)
	createAutomationRule(
		t,
		automation,
		eventcontract.TicketUpdatedEventType,
		models.RuleAction{
			Type:   "add_comment",
			Params: map[string]interface{}{"content": "update observed"},
		},
	)
	created := createAutomationTestTicket(t, native, user)
	ctx := testProjectOperationContext(t, automation.db, models.HumanActor(user.ID))
	updated, err := native.UpdateTicketVersion(ctx, VersionedTicketUpdateInput{
		TicketID:        created.Ticket.ID,
		ExpectedVersion: created.Ticket.Version,
		Actor:           models.HumanActor(user.ID),
		RequiredScope:   models.ScopeTicketsUpdate,
		Action:          "ticket.update",
		SourceProtocol:  "test",
		Changes:         map[string]any{"title": "updated title"},
		CorrelationID:   "test-correlation",
	})
	if err != nil {
		t.Fatalf("update native ticket: %v", err)
	}

	if _, err := native.ProcessOutboxBatch(
		context.Background(),
		"automation-update-worker",
		10,
		automationTestDeliverer(automation),
	); err != nil {
		t.Fatalf("process update automation: %v", err)
	}
	var comments []models.TicketComment
	if err := automation.db.Where("ticket_id = ?", created.Ticket.ID).Find(&comments).Error; err != nil {
		t.Fatalf("load update comments: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("comments=%d, want 1", len(comments))
	}
	var commentEvent models.DomainEvent
	if err := automation.db.
		Where(
			"type = ? AND causation_id = ?",
			"io.chronodesk.ticket.comment.created.v1",
			updated.Event.ID,
		).
		First(&commentEvent).Error; err != nil {
		t.Fatalf("load comment event: %v", err)
	}
	if commentEvent.CorrelationID != "test-correlation" ||
		commentEvent.ActorType != models.ActorTypeSystem ||
		commentEvent.ActorID != automationActorID {
		t.Fatalf("unexpected comment event provenance: %+v", commentEvent)
	}
}

func TestAutomationRetryContinuesActionsAfterConditionWasChanged(t *testing.T) {
	native, automation, creator := setupNativeAutomationTest(t)
	const delayedAssigneeID = uint(999)
	rule := createAutomationRule(
		t,
		automation,
		eventcontract.TicketCreatedEventType,
		models.RuleAction{
			Type:   "set_priority",
			Params: map[string]interface{}{"priority": string(models.TicketPriorityHigh)},
		},
		models.RuleAction{
			Type:   "assign",
			Params: map[string]interface{}{"user_id": float64(delayedAssigneeID)},
		},
	)
	if err := rule.SetConditions([]models.RuleCondition{{
		Field:    "priority",
		Operator: "eq",
		Value:    string(models.TicketPriorityNormal),
	}}); err != nil {
		t.Fatal(err)
	}
	if err := automation.db.Model(&rule).Update("conditions", rule.Conditions).Error; err != nil {
		t.Fatal(err)
	}
	created := createAutomationTestTicket(t, native, creator)

	first, err := native.ProcessOutboxBatch(
		context.Background(),
		"automation-partial-first",
		10,
		automationTestDeliverer(automation),
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Failed != 1 {
		t.Fatalf("first delivery failed=%d, want 1", first.Failed)
	}
	var afterFirst models.Ticket
	if err := automation.db.First(&afterFirst, created.Ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if afterFirst.Priority != models.TicketPriorityHigh || afterFirst.AssignedToID != nil {
		t.Fatalf("unexpected partial state: %+v", afterFirst)
	}

	delayedAssignee := models.User{
		ID: delayedAssigneeID, Username: "delayed-automation-assignee",
		Email: "delayed-automation-assignee@example.com", PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember, Status: models.UserStatusActive,
	}
	if err := automation.db.Create(&delayedAssignee).Error; err != nil {
		t.Fatal(err)
	}
	if err := automation.db.Create(&models.ProjectMembership{
		ProjectID: created.Ticket.ProjectID,
		UserID:    delayedAssignee.ID,
		Role:      models.ProjectRoleAgent,
		IsActive:  true,
		Version:   1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := automation.db.Model(&models.OutboxDelivery{}).
		Where("event_id = ? AND destination_type = ?", created.Event.ID, "automation").
		Updates(map[string]any{
			"status":          models.OutboxDeliveryFailed,
			"next_attempt_at": time.Now().UTC().Add(-time.Second),
		}).Error; err != nil {
		t.Fatal(err)
	}
	expireAutomationRuleExecutionClaim(t, automation, created.Event.ID, rule.ID)
	second, err := native.ProcessOutboxBatch(
		context.Background(),
		"automation-partial-retry",
		20,
		automationTestDeliverer(automation),
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.Failed != 0 {
		t.Fatalf("retry still failed: %+v", second)
	}
	var completed models.Ticket
	if err := automation.db.First(&completed, created.Ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if completed.AssignedToID == nil || *completed.AssignedToID != delayedAssigneeID {
		t.Fatalf("retry re-evaluated the now-false condition and lost action 2: %+v", completed)
	}
}

func TestAutomationActionEventsDoNotTriggerDifferentRuleTypes(t *testing.T) {
	native, automation, creator := setupNativeAutomationTest(t)
	createAutomationRule(
		t,
		automation,
		eventcontract.TicketCreatedEventType,
		models.RuleAction{
			Type:   "add_comment",
			Params: map[string]interface{}{"content": "first sibling rule"},
		},
	)
	createAutomationRule(
		t,
		automation,
		eventcontract.TicketUpdatedEventType,
		models.RuleAction{
			Type:   "add_comment",
			Params: map[string]interface{}{"content": "second sibling rule"},
		},
	)
	created := createAutomationTestTicket(t, native, creator)
	if _, err := native.ProcessOutboxBatch(
		context.Background(),
		"automation-root-original",
		20,
		automationTestDeliverer(automation),
	); err != nil {
		t.Fatal(err)
	}
	// The first action creates a comment-specific CloudEvent. Exact type
	// matching means it cannot activate the updated rule.
	for i := 0; i < 3; i++ {
		if _, err := native.ProcessOutboxBatch(
			context.Background(),
			fmt.Sprintf("automation-root-descendant-%d", i),
			20,
			automationTestDeliverer(automation),
		); err != nil {
			t.Fatal(err)
		}
	}
	var comments int64
	if err := automation.db.Model(&models.TicketComment{}).
		Where("ticket_id = ?", created.Ticket.ID).
		Count(&comments).Error; err != nil {
		t.Fatal(err)
	}
	if comments != 1 {
		t.Fatalf("exact event matching produced %d comments, want 1", comments)
	}
}

func TestAutomationConcurrentSiblingDeliveriesHaveOneRuleOwnerAndOneStatistic(t *testing.T) {
	native, automation, creator := setupNativeAutomationTest(t)
	ticket := seedNativeTicket(t, automation.db, creator.ID, "AUTO-CONCURRENT-SIBLINGS")
	action := models.RuleAction{
		Type:   "add_comment",
		Params: map[string]interface{}{"content": "one causal-root execution"},
	}
	rule := createAutomationRule(t, automation, eventcontract.TicketUpdatedEventType, action)

	root, err := native.createDomainEvent(t, context.Background(), DomainEventInput{
		Type:            "io.chronodesk.ticket.updated.v1",
		Subject:         fmt.Sprintf("ticket/%d", ticket.ID),
		Actor:           models.HumanActor(creator.ID),
		ResourceVersion: ticket.Version,
		Scope: models.ProjectScope{
			OrganizationID: ticket.OrganizationID,
			ProjectID:      ticket.ProjectID,
		},
		Data: map[string]any{"ticket_id": ticket.ID},
	}, []OutboxTarget{{Type: "test-root", ID: "not-delivered"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := automation.db.
		Where("event_id = ?", root.ID).
		Delete(&models.OutboxDelivery{}).Error; err != nil {
		t.Fatal(err)
	}

	children := make([]*models.DomainEvent, 0, 2)
	for index := 0; index < 2; index++ {
		child, createErr := native.createDomainEvent(t, context.Background(), DomainEventInput{
			Type:            "io.chronodesk.ticket.updated.v1",
			Subject:         fmt.Sprintf("ticket/%d", ticket.ID),
			Actor:           models.SystemActor(fmt.Sprintf("sibling-%d", index)),
			ResourceVersion: ticket.Version,
			TraceID:         root.ID,
			CorrelationID:   root.ID,
			CausationID:     root.ID,
			Scope: models.ProjectScope{
				OrganizationID: ticket.OrganizationID,
				ProjectID:      ticket.ProjectID,
			},
			Data: map[string]any{"ticket_id": ticket.ID},
		}, []OutboxTarget{{Type: "automation", ID: "rules", MaxAttempts: 4}})
		if createErr != nil {
			t.Fatal(createErr)
		}
		children = append(children, child)
	}

	actionRequest, err := json.Marshal(map[string]any{
		"root_event_id": root.ID,
		"rule_id":       rule.ID,
		"action_index":  0,
		"action":        &action,
	})
	if err != nil {
		t.Fatal(err)
	}
	blockedAction, err := native.ReserveIdempotency(
		automationWorkerTestContext(t, automation),
		models.SystemActor(automationActorID),
		automationActionOperation,
		automationActionKey(root.ID, rule.ID, 0),
		actionRequest,
		automationReservationTTL,
	)
	if err != nil {
		t.Fatal(err)
	}

	var barrierMu sync.Mutex
	arrived := 0
	start := make(chan struct{})
	deliverer := OutboxDeliverFunc(func(
		ctx context.Context,
		delivery *models.OutboxDelivery,
		event CloudEventEnvelope,
	) error {
		barrierMu.Lock()
		arrived++
		if arrived == len(children) {
			close(start)
		}
		barrierMu.Unlock()
		select {
		case <-start:
		case <-ctx.Done():
			return ctx.Err()
		}
		return automation.ExecuteDomainEvent(ctx, event)
	})
	first, err := native.ProcessOutboxBatch(
		context.Background(),
		"automation-concurrent-siblings-first",
		2,
		deliverer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Claimed != 2 || first.Failed != 2 {
		t.Fatalf("blocked concurrent attempts=%+v, want two failed deliveries", first)
	}

	var beforeRetry models.AutomationRule
	if err := automation.db.First(&beforeRetry, rule.ID).Error; err != nil {
		t.Fatal(err)
	}
	if beforeRetry.ExecutionCount != 1 ||
		beforeRetry.SuccessCount != 0 ||
		beforeRetry.FailureCount != 1 {
		t.Fatalf(
			"rule owner failure statistics=%d/%d/%d, want 1/0/1",
			beforeRetry.ExecutionCount,
			beforeRetry.SuccessCount,
			beforeRetry.FailureCount,
		)
	}
	var logsBeforeRetry int64
	if err := automation.db.Model(&models.AutomationLog{}).
		Where("rule_id = ?", rule.ID).
		Count(&logsBeforeRetry).Error; err != nil {
		t.Fatal(err)
	}
	if logsBeforeRetry != 1 {
		t.Fatalf("owner failure logs=%d, want 1", logsBeforeRetry)
	}

	if err := native.FailIdempotency(
		context.Background(),
		blockedAction.Record.ID,
		"test_dependency_available",
	); err != nil {
		t.Fatal(err)
	}
	expireAutomationRuleExecutionClaim(t, automation, root.ID, rule.ID)
	for attempt := 0; attempt < 4; attempt++ {
		if err := automation.db.Model(&models.OutboxDelivery{}).
			Where(
				"event_id IN ? AND destination_type = ? AND status = ?",
				[]string{children[0].ID, children[1].ID},
				"automation",
				models.OutboxDeliveryFailed,
			).
			Updates(map[string]any{
				"next_attempt_at": time.Now().UTC().Add(-time.Second),
			}).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := native.ProcessOutboxBatch(
			context.Background(),
			fmt.Sprintf("automation-concurrent-siblings-retry-%d", attempt),
			2,
			automationTestDeliverer(automation),
		); err != nil {
			t.Fatal(err)
		}
		var succeeded int64
		if err := automation.db.Model(&models.OutboxDelivery{}).
			Where(
				"event_id IN ? AND destination_type = ? AND status = ?",
				[]string{children[0].ID, children[1].ID},
				"automation",
				models.OutboxDeliverySucceeded,
			).
			Count(&succeeded).Error; err != nil {
			t.Fatal(err)
		}
		if succeeded == 2 {
			break
		}
		if attempt == 3 {
			t.Fatalf("sibling deliveries did not converge after retry; succeeded=%d", succeeded)
		}
	}

	var completedRule models.AutomationRule
	if err := automation.db.First(&completedRule, rule.ID).Error; err != nil {
		t.Fatal(err)
	}
	if completedRule.ExecutionCount != 2 ||
		completedRule.SuccessCount != 1 ||
		completedRule.FailureCount != 1 {
		t.Fatalf(
			"final rule statistics=%d/%d/%d, want 2/1/1",
			completedRule.ExecutionCount,
			completedRule.SuccessCount,
			completedRule.FailureCount,
		)
	}
	var finalLogs int64
	if err := automation.db.Model(&models.AutomationLog{}).
		Where("rule_id = ?", rule.ID).
		Count(&finalLogs).Error; err != nil {
		t.Fatal(err)
	}
	if finalLogs != 2 {
		t.Fatalf("final execution logs=%d, want 2", finalLogs)
	}
	var comments int64
	if err := automation.db.Model(&models.TicketComment{}).
		Where("ticket_id = ?", ticket.ID).
		Count(&comments).Error; err != nil {
		t.Fatal(err)
	}
	if comments != 1 {
		t.Fatalf("concurrent sibling deliveries created %d comments, want 1", comments)
	}
	var executionRecord models.IdempotencyRecord
	if err := automation.db.
		Where(
			"actor_type = ? AND actor_id = ? AND operation = ? AND key = ?",
			models.ActorTypeSystem,
			automationActorID,
			automationRuleExecutionOperation,
			automationRuleExecutionKey(root.ID, rule.ID),
		).
		First(&executionRecord).Error; err != nil {
		t.Fatal(err)
	}
	if executionRecord.State != models.IdempotencyStateCompleted {
		t.Fatalf("rule execution checkpoint state=%s, want completed", executionRecord.State)
	}
}

func TestAutomationRuleExecutionClaimRecoversAfterExpiry(t *testing.T) {
	native, automation, creator := setupNativeAutomationTest(t)
	ticket := seedNativeTicket(t, automation.db, creator.ID, "AUTO-EXPIRED-RULE-CLAIM")
	action := models.RuleAction{
		Type:   "add_comment",
		Params: map[string]interface{}{"content": "recovered expired claim"},
	}
	rule := createAutomationRule(
		t,
		automation,
		eventcontract.TicketUpdatedEventType,
		action,
	)
	root, err := native.createDomainEvent(t, context.Background(), DomainEventInput{
		Type:            "io.chronodesk.ticket.updated.v1",
		Subject:         fmt.Sprintf("ticket/%d", ticket.ID),
		Actor:           models.HumanActor(creator.ID),
		ResourceVersion: ticket.Version,
		Scope: models.ProjectScope{
			OrganizationID: ticket.OrganizationID,
			ProjectID:      ticket.ProjectID,
		},
		Data: map[string]any{"ticket_id": ticket.ID},
	}, []OutboxTarget{{Type: "test-root", ID: "not-delivered"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := automation.db.
		Where("event_id = ?", root.ID).
		Delete(&models.OutboxDelivery{}).Error; err != nil {
		t.Fatal(err)
	}
	child, err := native.createDomainEvent(t, context.Background(), DomainEventInput{
		Type:            "io.chronodesk.ticket.updated.v1",
		Subject:         fmt.Sprintf("ticket/%d", ticket.ID),
		Actor:           models.SystemActor("claim-recovery-source"),
		ResourceVersion: ticket.Version,
		TraceID:         root.ID,
		CorrelationID:   root.ID,
		CausationID:     root.ID,
		Scope: models.ProjectScope{
			OrganizationID: ticket.OrganizationID,
			ProjectID:      ticket.ProjectID,
		},
		Data: map[string]any{"ticket_id": ticket.ID},
	}, []OutboxTarget{{Type: "automation", ID: "rules"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := automation.db.
		Where("event_id = ?", child.ID).
		Delete(&models.OutboxDelivery{}).Error; err != nil {
		t.Fatal(err)
	}

	staleClaim, err := automation.reserveNativeRuleExecution(
		automationWorkerTestContext(t, automation),
		root.ID,
		&rule,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := automation.executeNativeAction(
		automationWorkerTestContext(t, automation),
		CloudEventFromModel(child),
		root.ID,
		&rule,
		0,
		&action,
	); err != nil {
		t.Fatalf("complete action before simulated crash: %v", err)
	}
	if err := automation.db.Model(&models.IdempotencyRecord{}).
		Where("id = ?", staleClaim.RecordID).
		Update("expires_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if err := automation.ExecuteDomainEvent(
		context.Background(),
		CloudEventFromModel(child),
	); err != nil {
		t.Fatalf("recover expired rule execution claim: %v", err)
	}

	if err := automation.completeNativeRuleExecution(
		automationWorkerTestContext(t, automation),
		staleClaim,
		&rule,
		&ticket,
		CloudEventFromModel(child),
		root.ID,
		true,
		nil,
		time.Millisecond,
	); err == nil {
		t.Fatal("stale execution owner completed after its fencing token was replaced")
	}
	var stats models.AutomationRule
	if err := automation.db.First(&stats, rule.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stats.ExecutionCount != 1 || stats.SuccessCount != 1 || stats.FailureCount != 0 {
		t.Fatalf(
			"recovered claim statistics=%d/%d/%d, want 1/1/0",
			stats.ExecutionCount,
			stats.SuccessCount,
			stats.FailureCount,
		)
	}
	var comments int64
	if err := automation.db.Model(&models.TicketComment{}).
		Where("ticket_id = ?", ticket.ID).
		Count(&comments).Error; err != nil {
		t.Fatal(err)
	}
	if comments != 1 {
		t.Fatalf("crash recovery replayed a completed action: comments=%d, want 1", comments)
	}
}

func TestAutomationPermanentFailureRecordsOnlyClaimedAttempt(t *testing.T) {
	native, automation, creator := setupNativeAutomationTest(t)
	rule := createAutomationRule(
		t,
		automation,
		eventcontract.TicketCreatedEventType,
		models.RuleAction{
			Type:   "assign",
			Params: map[string]interface{}{"user_id": float64(424242)},
		},
	)
	created := createAutomationTestTicket(t, native, creator)
	first, err := native.ProcessOutboxBatch(
		context.Background(),
		"automation-permanent-failure-owner",
		10,
		automationTestDeliverer(automation),
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Failed != 1 {
		t.Fatalf("permanent failure batch=%+v, want one failed owner", first)
	}

	// The same event cannot acquire the failed claim during its retry delay.
	// This represents a duplicate/concurrent loser and must not add statistics.
	if err := automation.ExecuteDomainEvent(
		context.Background(),
		CloudEventFromModel(created.Event),
	); err == nil {
		t.Fatal("duplicate permanent failure unexpectedly acquired the rule claim")
	}
	var stats models.AutomationRule
	if err := automation.db.First(&stats, rule.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stats.ExecutionCount != 1 || stats.SuccessCount != 0 || stats.FailureCount != 1 {
		t.Fatalf(
			"permanent failure statistics=%d/%d/%d, want 1/0/1",
			stats.ExecutionCount,
			stats.SuccessCount,
			stats.FailureCount,
		)
	}
	var logs []models.AutomationLog
	if err := automation.db.
		Where("rule_id = ?", rule.ID).
		Find(&logs).Error; err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].Success || logs[0].ErrorMessage == "" {
		t.Fatalf("permanent failure log is not a single failed owner result: %+v", logs)
	}
}

func TestAutomationRetryUsesFrozenRuleAfterModificationAndDisable(t *testing.T) {
	native, automation, creator := setupNativeAutomationTest(t)
	const delayedAssigneeID = uint(7171)
	originalActions := []models.RuleAction{
		{
			Type:   "set_priority",
			Params: map[string]interface{}{"priority": string(models.TicketPriorityHigh)},
		},
		{
			Type:   "assign",
			Params: map[string]interface{}{"user_id": float64(delayedAssigneeID)},
		},
	}
	rule := createAutomationRule(
		t,
		automation,
		eventcontract.TicketCreatedEventType,
		originalActions...,
	)
	if err := rule.SetConditions([]models.RuleCondition{{
		Field:    "priority",
		Operator: "eq",
		Value:    string(models.TicketPriorityNormal),
	}}); err != nil {
		t.Fatal(err)
	}
	if err := automation.db.Model(&rule).Update("conditions", rule.Conditions).Error; err != nil {
		t.Fatal(err)
	}
	created := createAutomationTestTicket(t, native, creator)
	first, err := native.ProcessOutboxBatch(
		context.Background(),
		"automation-frozen-rule-first",
		10,
		automationTestDeliverer(automation),
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Failed != 1 {
		t.Fatalf("frozen rule first batch=%+v, want one partial failure", first)
	}

	modified := rule
	if err := modified.SetConditions([]models.RuleCondition{{
		Field:    "priority",
		Operator: "eq",
		Value:    string(models.TicketPriorityLow),
	}}); err != nil {
		t.Fatal(err)
	}
	if err := modified.SetActions([]models.RuleAction{{
		Type:   "add_comment",
		Params: map[string]interface{}{"content": "modified action must not execute"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := automation.db.Model(&models.AutomationRule{}).
		Where("id = ?", rule.ID).
		Updates(map[string]any{
			"name":       "modified and disabled",
			"conditions": modified.Conditions,
			"actions":    modified.Actions,
			"is_active":  false,
			"updated_at": time.Now().Add(time.Minute),
		}).Error; err != nil {
		t.Fatal(err)
	}
	delayedAssignee := models.User{
		ID:           delayedAssigneeID,
		Username:     "frozen-rule-assignee",
		Email:        "frozen-rule-assignee@example.com",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := automation.db.Create(&delayedAssignee).Error; err != nil {
		t.Fatal(err)
	}
	if err := automation.db.Create(&models.ProjectMembership{
		ProjectID: created.Ticket.ProjectID,
		UserID:    delayedAssignee.ID,
		Role:      models.ProjectRoleAgent,
		IsActive:  true,
		Version:   1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	expireAutomationRuleExecutionClaim(t, automation, created.Event.ID, rule.ID)
	if err := automation.db.Model(&models.OutboxDelivery{}).
		Where("event_id = ? AND destination_type = ?", created.Event.ID, "automation").
		Updates(map[string]any{
			"status":          models.OutboxDeliveryFailed,
			"next_attempt_at": time.Now().UTC().Add(-time.Second),
		}).Error; err != nil {
		t.Fatal(err)
	}
	second, err := native.ProcessOutboxBatch(
		context.Background(),
		"automation-frozen-rule-retry",
		20,
		automationTestDeliverer(automation),
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.Failed != 0 {
		t.Fatalf("frozen rule retry failed: %+v", second)
	}

	var ticket models.Ticket
	if err := automation.db.First(&ticket, created.Ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if ticket.Priority != models.TicketPriorityHigh ||
		ticket.AssignedToID == nil ||
		*ticket.AssignedToID != delayedAssigneeID {
		t.Fatalf("retry mixed in modified rule instead of frozen snapshot: %+v", ticket)
	}
	var comments int64
	if err := automation.db.Model(&models.TicketComment{}).
		Where("ticket_id = ?", ticket.ID).
		Count(&comments).Error; err != nil {
		t.Fatal(err)
	}
	if comments != 0 {
		t.Fatalf("modified action executed despite frozen snapshot: comments=%d", comments)
	}
	var stats models.AutomationRule
	if err := automation.db.First(&stats, rule.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stats.ExecutionCount != 2 || stats.SuccessCount != 1 || stats.FailureCount != 1 {
		t.Fatalf(
			"frozen retry statistics=%d/%d/%d, want 2/1/1",
			stats.ExecutionCount,
			stats.SuccessCount,
			stats.FailureCount,
		)
	}
	var executionRecord models.IdempotencyRecord
	if err := automation.db.
		Where(
			"actor_type = ? AND actor_id = ? AND operation = ? AND key = ?",
			models.ActorTypeSystem,
			automationActorID,
			automationRuleExecutionOperation,
			automationRuleExecutionKey(created.Event.ID, rule.ID),
		).
		First(&executionRecord).Error; err != nil {
		t.Fatal(err)
	}
	frozenRule, err := decodeAutomationRuleSnapshot(executionRecord.ResourceSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if frozenRule.Name != rule.Name ||
		frozenRule.Conditions != rule.Conditions ||
		frozenRule.Actions != rule.Actions {
		t.Fatalf("persisted rule snapshot was overwritten: %+v", frozenRule)
	}
}

func TestAutomationDeletedRuleResumesFromFrozenSnapshot(t *testing.T) {
	native, automation, creator := setupNativeAutomationTest(t)
	ticket := seedNativeTicket(t, automation.db, creator.ID, "AUTO-DELETED-RULE")
	action := models.RuleAction{
		Type:   "add_comment",
		Params: map[string]interface{}{"content": "deleted rule snapshot resumed"},
	}
	rule := createAutomationRule(t, automation, eventcontract.TicketUpdatedEventType, action)
	root, err := native.createDomainEvent(t, context.Background(), DomainEventInput{
		Type:            "io.chronodesk.ticket.updated.v1",
		Subject:         fmt.Sprintf("ticket/%d", ticket.ID),
		Actor:           models.HumanActor(creator.ID),
		ResourceVersion: ticket.Version,
		Scope: models.ProjectScope{
			OrganizationID: ticket.OrganizationID,
			ProjectID:      ticket.ProjectID,
		},
		Data: map[string]any{"ticket_id": ticket.ID},
	}, []OutboxTarget{{Type: "test-root", ID: "not-delivered"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := automation.db.
		Where("event_id = ?", root.ID).
		Delete(&models.OutboxDelivery{}).Error; err != nil {
		t.Fatal(err)
	}
	child, err := native.createDomainEvent(t, context.Background(), DomainEventInput{
		Type:            "io.chronodesk.ticket.updated.v1",
		Subject:         fmt.Sprintf("ticket/%d", ticket.ID),
		Actor:           models.SystemActor("deleted-rule-source"),
		ResourceVersion: ticket.Version,
		TraceID:         root.ID,
		CorrelationID:   root.ID,
		CausationID:     root.ID,
		Scope: models.ProjectScope{
			OrganizationID: ticket.OrganizationID,
			ProjectID:      ticket.ProjectID,
		},
		Data: map[string]any{"ticket_id": ticket.ID},
	}, []OutboxTarget{{Type: "automation", ID: "rules"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := automation.db.
		Where("event_id = ?", child.ID).
		Delete(&models.OutboxDelivery{}).Error; err != nil {
		t.Fatal(err)
	}
	claim, err := automation.reserveNativeRuleExecution(
		automationWorkerTestContext(t, automation),
		root.ID,
		&rule,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := automation.db.Delete(&models.AutomationRule{}, rule.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := automation.db.Model(&models.IdempotencyRecord{}).
		Where("id = ?", claim.RecordID).
		Update("expires_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if err := automation.ExecuteDomainEvent(
		context.Background(),
		CloudEventFromModel(child),
	); err != nil {
		t.Fatalf("resume deleted rule snapshot: %v", err)
	}
	var comments int64
	if err := automation.db.Model(&models.TicketComment{}).
		Where("ticket_id = ?", ticket.ID).
		Count(&comments).Error; err != nil {
		t.Fatal(err)
	}
	if comments != 1 {
		t.Fatalf("deleted frozen rule produced %d comments, want 1", comments)
	}
	var executionRecord models.IdempotencyRecord
	if err := automation.db.First(&executionRecord, "id = ?", claim.RecordID).Error; err != nil {
		t.Fatal(err)
	}
	if executionRecord.State != models.IdempotencyStateCompleted ||
		len(executionRecord.ResourceSnapshot) == 0 {
		t.Fatalf("deleted rule checkpoint did not complete with snapshot: %+v", executionRecord)
	}
}

func TestAutomationConditionDecisionSurvivesCrashBeforeFirstAction(t *testing.T) {
	tests := []struct {
		name            string
		initialPriority models.TicketPriority
		changedPriority models.TicketPriority
		wantMatched     bool
		wantComments    int64
	}{
		{
			name:            "matched true remains true",
			initialPriority: models.TicketPriorityNormal,
			changedPriority: models.TicketPriorityLow,
			wantMatched:     true,
			wantComments:    1,
		},
		{
			name:            "matched false remains false",
			initialPriority: models.TicketPriorityLow,
			changedPriority: models.TicketPriorityNormal,
			wantMatched:     false,
			wantComments:    0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			native, automation, creator := setupNativeAutomationTest(t)
			ticket := seedNativeTicket(t, automation.db, creator.ID, "AUTO-CONDITION-CRASH")
			if err := automation.db.Model(&models.Ticket{}).
				Where("id = ?", ticket.ID).
				Update("priority", tt.initialPriority).Error; err != nil {
				t.Fatal(err)
			}
			ticket.Priority = tt.initialPriority
			rule := createAutomationRule(
				t,
				automation,
				eventcontract.TicketUpdatedEventType,
				models.RuleAction{
					Type:   "add_comment",
					Params: map[string]interface{}{"content": "persisted match decision"},
				},
			)
			if err := rule.SetConditions([]models.RuleCondition{{
				Field:    "priority",
				Operator: "eq",
				Value:    string(models.TicketPriorityNormal),
			}}); err != nil {
				t.Fatal(err)
			}
			if err := automation.db.Model(&rule).Update("conditions", rule.Conditions).Error; err != nil {
				t.Fatal(err)
			}
			event, err := native.createDomainEvent(t, context.Background(), DomainEventInput{
				Type:            "io.chronodesk.ticket.updated.v1",
				Subject:         fmt.Sprintf("ticket/%d", ticket.ID),
				Actor:           models.HumanActor(creator.ID),
				ResourceVersion: ticket.Version,
				Scope: models.ProjectScope{
					OrganizationID: ticket.OrganizationID,
					ProjectID:      ticket.ProjectID,
				},
				Data: map[string]any{"ticket_id": ticket.ID},
			}, []OutboxTarget{{Type: "test-root", ID: "not-delivered"}})
			if err != nil {
				t.Fatal(err)
			}
			if err := automation.db.
				Where("event_id = ?", event.ID).
				Delete(&models.OutboxDelivery{}).Error; err != nil {
				t.Fatal(err)
			}

			claim, err := automation.reserveNativeRuleExecution(
				automationWorkerTestContext(t, automation),
				event.ID,
				&rule,
			)
			if err != nil {
				t.Fatal(err)
			}
			conditions, err := rule.GetConditions()
			if err != nil {
				t.Fatal(err)
			}
			evaluated := automation.evaluateConditions(conditions, &ticket)
			if evaluated != tt.wantMatched {
				t.Fatalf("initial condition=%t, want %t", evaluated, tt.wantMatched)
			}
			if err := automation.persistNativeRuleConditionDecision(
				automationWorkerTestContext(t, automation),
				claim,
				evaluated,
			); err != nil {
				t.Fatal(err)
			}
			var actionReservations int64
			if err := automation.db.Model(&models.IdempotencyRecord{}).
				Where(
					"actor_type = ? AND actor_id = ? AND operation = ?",
					models.ActorTypeSystem,
					automationActorID,
					automationActionOperation,
				).
				Count(&actionReservations).Error; err != nil {
				t.Fatal(err)
			}
			if actionReservations != 0 {
				t.Fatalf("condition decision was not persisted before action reservation")
			}

			if err := automation.db.Model(&models.Ticket{}).
				Where("id = ?", ticket.ID).
				Update("priority", tt.changedPriority).Error; err != nil {
				t.Fatal(err)
			}
			if err := automation.db.Model(&models.IdempotencyRecord{}).
				Where("id = ?", claim.RecordID).
				Update("expires_at", time.Now().Add(-time.Second)).Error; err != nil {
				t.Fatal(err)
			}
			if err := automation.ExecuteDomainEvent(
				context.Background(),
				CloudEventFromModel(event),
			); err != nil {
				t.Fatalf("resume persisted condition decision: %v", err)
			}
			var comments int64
			if err := automation.db.Model(&models.TicketComment{}).
				Where("ticket_id = ?", ticket.ID).
				Count(&comments).Error; err != nil {
				t.Fatal(err)
			}
			if comments != tt.wantComments {
				t.Fatalf(
					"changed ticket re-evaluated persisted decision: comments=%d, want %d",
					comments,
					tt.wantComments,
				)
			}
			var record models.IdempotencyRecord
			if err := automation.db.First(&record, "id = ?", claim.RecordID).Error; err != nil {
				t.Fatal(err)
			}
			snapshot, _, err := decodeAutomationRuleExecutionSnapshot(record.ResourceSnapshot)
			if err != nil {
				t.Fatal(err)
			}
			if !snapshot.ConditionEvaluated || snapshot.Matched != tt.wantMatched {
				t.Fatalf("condition decision was lost from snapshot: %+v", snapshot)
			}
		})
	}
}

func TestAutomationOpaqueRootIDsDoNotShareSnapshotsOrActionKeys(t *testing.T) {
	_, automation, _ := setupNativeAutomationTest(t)
	first := createAutomationRule(
		t,
		automation,
		eventcontract.TicketUpdatedEventType,
		models.RuleAction{
			Type:   "add_comment",
			Params: map[string]interface{}{"content": "first opaque root"},
		},
	)
	second := first
	second.Name = "second opaque root"
	if err := second.SetActions([]models.RuleAction{{
		Type:   "add_comment",
		Params: map[string]interface{}{"content": "second opaque root"},
	}}); err != nil {
		t.Fatal(err)
	}
	const firstRoot = "abc"
	const secondRoot = "abc:def"
	if _, err := automation.reserveNativeRuleExecution(
		automationWorkerTestContext(t, automation),
		firstRoot,
		&first,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := automation.reserveNativeRuleExecution(
		automationWorkerTestContext(t, automation),
		secondRoot,
		&second,
	); err != nil {
		t.Fatal(err)
	}

	firstRules, err := automation.loadIncompleteNativeRuleSnapshots(
		automationWorkerTestContext(t, automation),
		firstRoot,
		first.TriggerEvent,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondRules, err := automation.loadIncompleteNativeRuleSnapshots(
		automationWorkerTestContext(t, automation),
		secondRoot,
		second.TriggerEvent,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstRules) != 1 || firstRules[0].Name != first.Name {
		t.Fatalf("root %q loaded another root snapshot: %+v", firstRoot, firstRules)
	}
	if len(secondRules) != 1 || secondRules[0].Name != second.Name {
		t.Fatalf("root %q loaded another root snapshot: %+v", secondRoot, secondRules)
	}
	firstRuleKey := automationRuleExecutionKey(firstRoot, first.ID)
	secondRuleKey := automationRuleExecutionKey(secondRoot, second.ID)
	firstActionKey := automationActionKey(firstRoot, first.ID, 0)
	secondActionKey := automationActionKey(secondRoot, second.ID, 0)
	if firstRuleKey == secondRuleKey || firstActionKey == secondActionKey {
		t.Fatalf(
			"opaque roots collided: rules=%q/%q actions=%q/%q",
			firstRuleKey,
			secondRuleKey,
			firstActionKey,
			secondActionKey,
		)
	}
	longRoot := strings.Repeat("opaque:root:", 100)
	if len(firstRuleKey) > 255 ||
		len(firstActionKey) > 255 ||
		len(automationRuleExecutionKey(longRoot, first.ID)) > 255 ||
		len(automationActionKey(longRoot, first.ID, 0)) > 255 {
		t.Fatalf("hashed idempotency keys exceed storage limit")
	}
}

func TestScheduledAutomationTriggerIsDurableBeforeExecution(t *testing.T) {
	native, automation, creator := setupNativeAutomationTest(t)
	createAutomationRule(
		t,
		automation,
		eventcontract.AutomationScheduledCheckEventType,
		models.RuleAction{
			Type:   "add_comment",
			Params: map[string]interface{}{"content": "durable scheduled action"},
		},
	)
	ticket := seedNativeTicket(t, automation.db, creator.ID, "AUTO-SCHEDULED")
	if err := automation.EnqueueScheduledCheck(context.Background(), &ticket); err != nil {
		t.Fatal(err)
	}
	var before int64
	if err := automation.db.Model(&models.TicketComment{}).
		Where("ticket_id = ?", ticket.ID).
		Count(&before).Error; err != nil {
		t.Fatal(err)
	}
	if before != 0 {
		t.Fatalf("scheduled rule executed synchronously before Outbox: comments=%d", before)
	}
	var trigger models.DomainEvent
	if err := automation.db.
		Where(
			"type = ? AND subject = ?",
			eventcontract.AutomationScheduledCheckEventType,
			fmt.Sprintf("ticket/%d", ticket.ID),
		).
		First(&trigger).Error; err != nil {
		t.Fatal(err)
	}
	if len(trigger.ID) != 36 {
		t.Fatalf("scheduled CloudEvent id length=%d, want UUID length 36: %q", len(trigger.ID), trigger.ID)
	}
	if _, err := native.ProcessOutboxBatch(
		context.Background(),
		"automation-scheduled-worker",
		20,
		automationTestDeliverer(automation),
	); err != nil {
		t.Fatal(err)
	}
	var after int64
	if err := automation.db.Model(&models.TicketComment{}).
		Where("ticket_id = ?", ticket.ID).
		Count(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after != 1 {
		t.Fatalf("durable scheduled trigger produced %d comments, want 1", after)
	}
}

func TestScheduledAutomationWithoutActiveRuleDoesNotEmitEvents(t *testing.T) {
	_, automation, creator := setupNativeAutomationTest(t)
	ticket := seedNativeTicket(t, automation.db, creator.ID, "AUTO-SCHEDULED-NO-RULE")

	if err := automation.EnqueueScheduledCheck(context.Background(), &ticket); err != nil {
		t.Fatal(err)
	}

	var eventCount int64
	if err := automation.db.Model(&models.DomainEvent{}).
		Where(
			"type = ? AND subject = ?",
			eventcontract.AutomationScheduledCheckEventType,
			fmt.Sprintf("ticket/%d", ticket.ID),
		).
		Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if eventCount != 0 {
		t.Fatalf("inactive scheduled automation emitted %d events, want 0", eventCount)
	}

	var outboxCount int64
	if err := automation.db.Model(&models.OutboxDelivery{}).Count(&outboxCount).Error; err != nil {
		t.Fatal(err)
	}
	if outboxCount != 0 {
		t.Fatalf("inactive scheduled automation emitted %d Outbox deliveries, want 0", outboxCount)
	}
}

func TestAutomationDomainEventMatchesRuleTypeExactly(t *testing.T) {
	native, automation, creator := setupNativeAutomationTest(t)
	ticket := seedNativeTicket(t, automation.db, creator.ID, "AUTO-EXACT-EVENT")
	createAutomationRule(
		t,
		automation,
		eventcontract.TicketAssignedEventType,
		models.RuleAction{
			Type:   "add_comment",
			Params: map[string]interface{}{"content": "assigned event matched"},
		},
	)
	createAutomationRule(
		t,
		automation,
		eventcontract.TicketUpdatedEventType,
		models.RuleAction{
			Type:   "add_comment",
			Params: map[string]interface{}{"content": "updated event matched"},
		},
	)
	if _, err := native.createDomainEvent(t, context.Background(), DomainEventInput{
		Type:            eventcontract.TicketAssignedEventType,
		Subject:         fmt.Sprintf("ticket/%d", ticket.ID),
		Actor:           models.HumanActor(creator.ID),
		ResourceVersion: ticket.Version,
		Scope: models.ProjectScope{
			OrganizationID: ticket.OrganizationID,
			ProjectID:      ticket.ProjectID,
		},
		Data: map[string]any{"ticket_id": ticket.ID},
	}, []OutboxTarget{{Type: "automation", ID: "rules", MaxAttempts: 4}}); err != nil {
		t.Fatal(err)
	}
	if _, err := native.ProcessOutboxBatch(
		context.Background(),
		"automation-exact-event-worker",
		10,
		automationTestDeliverer(automation),
	); err != nil {
		t.Fatal(err)
	}

	var comments []models.TicketComment
	if err := automation.db.Where("ticket_id = ?", ticket.ID).Find(&comments).Error; err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 || comments[0].Content != "assigned event matched" {
		t.Fatalf("exact event matching produced unexpected comments: %+v", comments)
	}
}
