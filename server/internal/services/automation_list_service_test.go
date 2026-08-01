package services

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

var automationListTestCursorKey = []byte(
	"chronodesk-automation-list-test-cursor-key-20260731",
)

func TestAutomationExecutionLogsStableCursorAndBindings(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.Ticket{},
		&models.AutomationRule{},
		&models.AutomationLog{},
	); err != nil {
		t.Fatal(err)
	}
	for _, index := range []string{
		"idx_automation_logs_timeline",
		"idx_automation_logs_rule_timeline",
		"idx_automation_logs_ticket_timeline",
		"idx_automation_logs_success_timeline",
	} {
		if !db.Migrator().HasIndex(&models.AutomationLog{}, index) {
			t.Fatalf("automation log index %q is missing", index)
		}
	}
	ctxA := automationListTestContext(t, 5, 8)
	ctxB := automationListTestContext(t, 5, 9)
	executedAt := time.Date(2026, time.July, 31, 11, 0, 0, 0, time.UTC)
	logs := make([]models.AutomationLog, 0, 151)
	for index := 0; index < 151; index++ {
		logs = append(logs, models.AutomationLog{
			OrganizationID: 5,
			ProjectID:      8,
			RuleID:         17,
			TicketID:       29,
			TriggerEvent:   "io.chronodesk.ticket.created.v1",
			ExecutedAt:     executedAt,
			Success:        true,
		})
	}
	if err := db.CreateInBatches(&logs, 50).Error; err != nil {
		t.Fatal(err)
	}
	service := NewAutomationService(db)
	if err := service.ConfigureListCursor(automationListTestCursorKey); err != nil {
		t.Fatal(err)
	}
	success := true
	first, err := service.ListExecutionLogs(
		ctxA,
		AutomationExecutionLogQuery{Limit: 100, Success: &success},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 100 || !first.HasMore || first.NextCursor == "" ||
		first.Items[0].ID != logs[150].ID ||
		first.Items[99].ID != logs[51].ID {
		t.Fatalf("first page = %+v", first)
	}
	concurrentLog := models.AutomationLog{
		OrganizationID: 5,
		ProjectID:      8,
		RuleID:         17,
		TicketID:       29,
		TriggerEvent:   "io.chronodesk.ticket.created.v1",
		ExecutedAt:     executedAt,
		Success:        true,
	}
	if err := db.Create(&concurrentLog).Error; err != nil {
		t.Fatal(err)
	}
	second, err := service.ListExecutionLogs(
		ctxA,
		AutomationExecutionLogQuery{
			Limit: 100, Success: &success, Cursor: first.NextCursor,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 51 || second.HasMore ||
		second.NextCursor != "" ||
		second.Items[0].ID != logs[50].ID ||
		second.Items[50].ID != logs[0].ID {
		t.Fatalf("second page = %+v", second)
	}
	for _, item := range second.Items {
		if item.ID == concurrentLog.ID {
			t.Fatalf(
				"concurrent insert %d leaked into continuation",
				concurrentLog.ID,
			)
		}
	}

	tampered := first.NextCursor[:len(first.NextCursor)-1] + "A"
	if tampered == first.NextCursor {
		tampered = first.NextCursor[:len(first.NextCursor)-1] + "B"
	}
	failure := false
	cases := []struct {
		name  string
		ctx   context.Context
		query AutomationExecutionLogQuery
	}{
		{
			name: "tampered",
			ctx:  ctxA,
			query: AutomationExecutionLogQuery{
				Limit: 100, Success: &success, Cursor: tampered,
			},
		},
		{
			name: "filter changed",
			ctx:  ctxA,
			query: AutomationExecutionLogQuery{
				Limit: 100, Success: &failure, Cursor: first.NextCursor,
			},
		},
		{
			name: "cross project",
			ctx:  ctxB,
			query: AutomationExecutionLogQuery{
				Limit: 100, Success: &success, Cursor: first.NextCursor,
			},
		},
		{
			name: "limit changed",
			ctx:  ctxA,
			query: AutomationExecutionLogQuery{
				Limit: 25, Success: &success, Cursor: first.NextCursor,
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.ListExecutionLogs(
				test.ctx,
				test.query,
			); !errors.Is(err, ErrInvalidAutomationListCursor) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestAutomationExecutionLogsRequireConfiguredCursorKey(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.AutomationLog{}); err != nil {
		t.Fatal(err)
	}
	service := NewAutomationService(db)
	if _, err := service.ListExecutionLogs(
		automationListTestContext(t, 2, 3),
		AutomationExecutionLogQuery{Limit: 25},
	); !errors.Is(err, ErrAutomationListCursorKey) {
		t.Fatalf("error = %v", err)
	}
	if err := service.ConfigureListCursor(nil); !errors.Is(
		err,
		ErrAutomationListCursorKey,
	) {
		t.Fatalf("empty key error = %v", err)
	}
}

func TestAutomationRuleTypeIsClosedAtServiceBoundary(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.AutomationRule{}); err != nil {
		t.Fatal(err)
	}
	service := NewAutomationService(db)
	ctx := automationListTestContext(t, 2, 3)

	if _, err := service.CreateRule(
		ctx,
		&models.AutomationRuleRequest{
			Name:         "unsupported rule",
			RuleType:     "notification",
			TriggerEvent: "io.chronodesk.ticket.created.v1",
		},
		1,
	); !errors.Is(err, ErrInvalidAutomationRuleType) {
		t.Fatalf("CreateRule unsupported type error = %v", err)
	}
	if err := service.UpdateRule(
		ctx,
		1,
		&models.AutomationRuleRequest{
			Name:         "unsupported rule",
			RuleType:     "notification",
			TriggerEvent: "io.chronodesk.ticket.created.v1",
		},
		1,
	); !errors.Is(err, ErrInvalidAutomationRuleType) {
		t.Fatalf("UpdateRule unsupported type error = %v", err)
	}
	if _, _, err := service.GetRules(
		ctx,
		"notification",
		"",
		nil,
		"",
		1,
		25,
	); !errors.Is(err, ErrInvalidAutomationRuleType) {
		t.Fatalf("GetRules unsupported type error = %v", err)
	}
	if _, err := service.CreateRule(
		ctx,
		nil,
		1,
	); !errors.Is(err, ErrInvalidAutomationRuleType) {
		t.Fatalf("CreateRule nil request error = %v", err)
	}
}

func TestAutomationRulePageIsStableAcross151Ties(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.AutomationRule{}); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:     "automation-directory-owner",
		Email:        "automation-directory-owner@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, time.July, 31, 14, 0, 0, 0, time.UTC)
	rules := make([]models.AutomationRule, 0, 151)
	for index := 0; index < 151; index++ {
		rules = append(rules, models.AutomationRule{
			CreatedAt:      createdAt,
			OrganizationID: 5,
			ProjectID:      8,
			Name:           fmt.Sprintf("stable-rule-%03d", index),
			RuleType:       "assignment",
			Priority:       10,
			TriggerEvent:   "io.chronodesk.ticket.created.v1",
			CreatedBy:      user.ID,
		})
	}
	if err := db.CreateInBatches(&rules, 50).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.AutomationRule{
		CreatedAt:      createdAt,
		OrganizationID: 5,
		ProjectID:      9,
		Name:           "cross-project-decoy",
		RuleType:       "assignment",
		Priority:       10,
		TriggerEvent:   "io.chronodesk.ticket.created.v1",
		CreatedBy:      user.ID,
	}).Error; err != nil {
		t.Fatal(err)
	}

	service := NewAutomationService(db)
	ctx := automationListTestContext(t, 5, 8)
	first, total, err := service.GetRules(
		ctx,
		"assignment",
		"",
		nil,
		"",
		1,
		100,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, secondTotal, err := service.GetRules(
		ctx,
		"assignment",
		"",
		nil,
		"",
		2,
		100,
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 151 || secondTotal != total ||
		len(first) != 100 || len(second) != 51 ||
		first[0].ID != rules[150].ID ||
		first[99].ID != rules[51].ID ||
		second[0].ID != rules[50].ID ||
		second[50].ID != rules[0].ID {
		t.Fatalf(
			"unstable automation directory: total=%d/%d first=%d second=%d",
			total,
			secondTotal,
			len(first),
			len(second),
		)
	}
	if !db.Migrator().HasIndex(
		&models.AutomationRule{},
		"idx_automation_rules_directory",
	) {
		t.Fatal("automation rule directory index is missing")
	}
}

func automationListTestContext(
	t *testing.T,
	organizationID uint,
	projectID uint,
) context.Context {
	t.Helper()
	ctx, err := WithOperationContext(context.Background(), OperationContext{
		Scope: models.ProjectScope{
			OrganizationID: organizationID,
			ProjectID:      projectID,
		},
		Actor:  models.HumanActor(1),
		Source: SourceProtocolHumanREST,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}
