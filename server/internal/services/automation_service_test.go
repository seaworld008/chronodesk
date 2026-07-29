package services

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

func setupAutomationServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := openTestDB(t)

	if err := db.AutoMigrate(&models.User{}, &models.Ticket{}, &models.AutomationRule{}); err != nil {
		t.Fatalf("failed to migrate automation rule schema: %v", err)
	}

	fixtures := []models.AutomationRule{
		{
			Name:         "高优先级分配",
			Description:  "创建后自动分配高优先级工单",
			RuleType:     "assignment",
			TriggerEvent: "ticket.created",
			IsActive:     true,
			Priority:     1,
		},
		{
			Name:         "SLA 升级",
			Description:  "检查工单超时触发升级",
			RuleType:     "sla",
			TriggerEvent: "scheduled_check",
			IsActive:     true,
			Priority:     2,
		},
		{
			Name:         "关闭提醒",
			Description:  "关闭后发送提醒",
			RuleType:     "notification",
			TriggerEvent: "ticket.closed",
			IsActive:     false,
			Priority:     3,
		},
	}

	if err := db.Create(&fixtures).Error; err != nil {
		t.Fatalf("failed to seed automation rules: %v", err)
	}

	if err := db.Model(&models.AutomationRule{}).Where("name = ?", "关闭提醒").Update("is_active", false).Error; err != nil {
		t.Fatalf("failed to force inactive rule: %v", err)
	}

	var inactiveCount int64
	if err := db.Model(&models.AutomationRule{}).Where("is_active = ?", false).Count(&inactiveCount).Error; err != nil {
		t.Fatalf("failed to verify seeded inactive rules: %v", err)
	}
	if inactiveCount != 1 {
		t.Fatalf("expected 1 inactive rule in seed, got %d", inactiveCount)
	}

	return db
}

func TestCreateAutomationRuleDefaultsToInactive(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.AutomationRule{}); err != nil {
		t.Fatalf("migrate automation schemas: %v", err)
	}
	user := models.User{
		Username:     "automation-author",
		Email:        "automation-author@example.com",
		PasswordHash: "hashed",
		Role:         models.RoleAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create author: %v", err)
	}

	rule, err := NewAutomationService(db).CreateRule(
		context.Background(),
		&models.AutomationRuleRequest{
			Name:         "safe inactive rule",
			RuleType:     "assignment",
			TriggerEvent: "ticket.created",
		},
		user.ID,
	)
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}
	if rule.IsActive {
		t.Fatal("new automation rule must default to inactive")
	}
}

func TestAutomationAssignmentUsesTicketAssigneeColumn(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.Ticket{},
		&models.TicketHistory{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.IdempotencyRecord{},
	); err != nil {
		t.Fatalf("failed to migrate schemas: %v", err)
	}

	user := models.User{
		Username:     "automation-assignee",
		Email:        "automation-assignee@example.com",
		PasswordHash: "hashed",
		Role:         models.RoleAgent,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	ticket := models.Ticket{
		TicketNumber: "AUTO-001",
		Title:        "Automation assignment",
		Description:  "desc",
		Status:       models.TicketStatusOpen,
		Priority:     models.TicketPriorityNormal,
		Type:         models.TicketTypeRequest,
		Source:       models.TicketSourceWeb,
		CreatedByID:  user.ID,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatalf("failed to create ticket: %v", err)
	}

	svc := NewAutomationService(db)
	action := &models.RuleAction{
		Type:   "assign",
		Params: map[string]interface{}{"user_id": float64(user.ID)},
	}
	if err := svc.executeAssignAction(context.Background(), action, &ticket); err != nil {
		t.Fatalf("executeAssignAction returned error: %v", err)
	}

	var updated models.Ticket
	if err := db.First(&updated, ticket.ID).Error; err != nil {
		t.Fatalf("failed to reload ticket: %v", err)
	}
	if updated.AssignedToID == nil || *updated.AssignedToID != user.ID {
		t.Fatalf("expected assigned_to_id=%d, got %v", user.ID, updated.AssignedToID)
	}
}

func TestAutomationToUintRejectsUnsafeValues(t *testing.T) {
	svc := &AutomationService{}
	invalid := []interface{}{
		-1,
		int64(-1),
		float64(-1),
		float64(1.5),
		math.NaN(),
		math.Inf(1),
		math.Ldexp(1, 64),
		uint(0),
		uint64(0),
		"18446744073709551616",
	}

	for _, value := range invalid {
		if _, err := svc.toUint(value); err == nil {
			t.Fatalf("expected value %v (%T) to be rejected", value, value)
		}
	}
}

func TestClassifyTicketPersistsCanonicalType(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.Ticket{},
		&models.TicketHistory{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.IdempotencyRecord{},
	); err != nil {
		t.Fatalf("failed to migrate schemas: %v", err)
	}
	user := models.User{
		Username:     "automation-classifier",
		Email:        "automation-classifier@example.com",
		PasswordHash: "hashed",
		Role:         models.RoleAgent,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	ticket := models.Ticket{
		TicketNumber: "AUTO-CLASSIFY-001",
		Title:        "Urgent crash in checkout",
		Description:  "customers see an error",
		Status:       models.TicketStatusOpen,
		Priority:     models.TicketPriorityNormal,
		Type:         models.TicketTypeRequest,
		Source:       models.TicketSourceWeb,
		CreatedByID:  user.ID,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatalf("failed to create ticket: %v", err)
	}

	svc := NewAutomationService(db)
	if err := svc.ClassifyTicket(context.Background(), &ticket); err != nil {
		t.Fatalf("ClassifyTicket returned error: %v", err)
	}

	var updated models.Ticket
	if err := db.First(&updated, ticket.ID).Error; err != nil {
		t.Fatalf("failed to reload ticket: %v", err)
	}
	if updated.Type != models.TicketTypeIncident {
		t.Fatalf("expected incident type, got %s", updated.Type)
	}
	if updated.Priority != models.TicketPriorityHigh {
		t.Fatalf("expected high priority, got %s", updated.Priority)
	}
	if updated.Version != 2 {
		t.Fatalf("expected versioned classification update, got version %d", updated.Version)
	}
	var event models.DomainEvent
	if err := db.
		Where("subject = ? AND actor_type = ? AND actor_id = ?",
			fmt.Sprintf("ticket/%d", ticket.ID),
			models.ActorTypeSystem,
			automationActorID,
		).
		First(&event).Error; err != nil {
		t.Fatalf("expected classification domain event: %v", err)
	}
}

func TestAutomationServiceGetRulesFilters(t *testing.T) {
	db := setupAutomationServiceTestDB(t)
	svc := NewAutomationService(db)

	ctx := context.Background()

	// filter by rule type
	rules, total, err := svc.GetRules(ctx, "assignment", "", nil, "", 1, 10)
	if err != nil {
		t.Fatalf("GetRules returned error: %v", err)
	}
	if total != 1 || len(rules) != 1 {
		t.Fatalf("expected 1 assignment rule, got total=%d len=%d", total, len(rules))
	}

	// filter by trigger event
	rules, total, err = svc.GetRules(ctx, "", "scheduled_check", nil, "", 1, 10)
	if err != nil {
		t.Fatalf("GetRules returned error: %v", err)
	}
	if total != 1 || len(rules) != 1 {
		t.Fatalf("expected 1 scheduled_check rule, got total=%d len=%d", total, len(rules))
	}

	// filter by active flag
	active := true
	rules, total, err = svc.GetRules(ctx, "", "", &active, "", 1, 10)
	if err != nil {
		t.Fatalf("GetRules returned error: %v", err)
	}
	if total != 2 || len(rules) != 2 {
		t.Fatalf("expected 2 active rules, got total=%d len=%d", total, len(rules))
	}

	// search by keyword
	rules, total, err = svc.GetRules(ctx, "", "", nil, "提醒", 1, 10)
	if err != nil {
		t.Fatalf("GetRules search returned error: %v", err)
	}
	if total != 1 || len(rules) != 1 {
		t.Fatalf("expected search to match 1 rule, got total=%d len=%d", total, len(rules))
	}
}
