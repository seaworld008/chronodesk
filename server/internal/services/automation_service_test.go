package services

import (
	"context"
	"testing"

	"gongdan-system/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupAutomationServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite memory db: %v", err)
	}

	if err := db.AutoMigrate(&models.AutomationRule{}); err != nil {
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
