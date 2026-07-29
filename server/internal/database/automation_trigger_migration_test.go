package database

import (
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMigrateAutomationRuleTriggerEventsIsIdempotent(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:automation-trigger-migration?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.AutomationRule{}, &models.AutomationLog{}); err != nil {
		t.Fatal(err)
	}

	for index, migration := range legacyAutomationTriggerMigrations {
		rule := models.AutomationRule{
			Name:         "legacy rule " + migration.legacy,
			RuleType:     "assignment",
			TriggerEvent: migration.legacy,
			Priority:     index + 1,
			Conditions:   "[]",
			Actions:      "[]",
		}
		if err := db.Create(&rule).Error; err != nil {
			t.Fatal(err)
		}
		log := models.AutomationLog{
			RuleID:          rule.ID,
			TicketID:        uint(index + 1),
			TriggerEvent:    migration.legacy,
			ActionsExecuted: "[]",
			Changes:         "{}",
		}
		if err := db.Create(&log).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&models.AutomationRule{
		Name:         "whitespace legacy rule",
		RuleType:     "assignment",
		TriggerEvent: " ticket.created ",
		Priority:     100,
		Conditions:   "[]",
		Actions:      "[]",
	}).Error; err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := MigrateAutomationRuleTriggerEvents(db); err != nil {
			t.Fatalf("migration attempt %d: %v", attempt+1, err)
		}
	}

	for _, migration := range legacyAutomationTriggerMigrations {
		var currentCount int64
		if err := db.Model(&models.AutomationRule{}).
			Where("trigger_event = ?", migration.current).
			Count(&currentCount).Error; err != nil {
			t.Fatal(err)
		}
		if currentCount == 0 {
			t.Errorf("current rule trigger %q was not populated", migration.current)
		}
		var legacyCount int64
		if err := db.Model(&models.AutomationRule{}).
			Where("trigger_event = ?", migration.legacy).
			Count(&legacyCount).Error; err != nil {
			t.Fatal(err)
		}
		if legacyCount != 0 {
			t.Errorf("legacy rule trigger %q remains", migration.legacy)
		}
		if migration.requiredStatus != "" {
			var rule models.AutomationRule
			if err := db.Where("name = ?", "legacy rule "+migration.legacy).
				First(&rule).Error; err != nil {
				t.Fatal(err)
			}
			conditions, err := rule.GetConditions()
			if err != nil {
				t.Fatal(err)
			}
			if !hasAutomationStatusCondition(conditions, migration.requiredStatus) {
				t.Errorf(
					"legacy trigger %q did not preserve status %q: %+v",
					migration.legacy,
					migration.requiredStatus,
					conditions,
				)
			}
		}
	}
	var whitespaceRule models.AutomationRule
	if err := db.Where("name = ?", "whitespace legacy rule").
		First(&whitespaceRule).Error; err != nil {
		t.Fatal(err)
	}
	if whitespaceRule.TriggerEvent != legacyAutomationTriggerMigrations[0].current {
		t.Errorf("whitespace trigger migrated to %q", whitespaceRule.TriggerEvent)
	}
}

func TestMigrateAutomationRuleTriggerEventsRejectsUnknownValues(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:automation-trigger-unknown?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.AutomationRule{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.AutomationRule{
		Name:         "unknown",
		RuleType:     "assignment",
		TriggerEvent: "custom.ticket.event",
		Conditions:   "[]",
		Actions:      "[]",
	}).Error; err != nil {
		t.Fatal(err)
	}

	err = MigrateAutomationRuleTriggerEvents(db)
	if err == nil || !strings.Contains(err.Error(), "custom.ticket.event") {
		t.Fatalf("migration error = %v, want unsupported trigger value", err)
	}
}
