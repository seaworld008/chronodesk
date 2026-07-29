package database

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

type automationTriggerMigration struct {
	legacy         string
	current        string
	requiredStatus models.TicketStatus
}

var legacyAutomationTriggerMigrations = [...]automationTriggerMigration{
	{legacy: "ticket.created", current: eventcontract.TicketCreatedEventType},
	{legacy: "ticket.updated", current: eventcontract.TicketUpdatedEventType},
	{legacy: "ticket.assigned", current: eventcontract.TicketAssignedEventType},
	{legacy: "ticket.transitioned", current: eventcontract.TicketTransitionedEventType},
	{
		legacy:         "ticket.resolved",
		current:        eventcontract.TicketTransitionedEventType,
		requiredStatus: models.TicketStatusResolved,
	},
	{
		legacy:         "ticket.closed",
		current:        eventcontract.TicketTransitionedEventType,
		requiredStatus: models.TicketStatusClosed,
	},
	{legacy: "ticket.escalated", current: eventcontract.TicketEscalatedEventType},
	{legacy: "ticket.comment", current: eventcontract.TicketCommentCreatedEventType},
	{legacy: "ticket.comment.created", current: eventcontract.TicketCommentCreatedEventType},
	{legacy: "ticket.attachment", current: eventcontract.TicketAttachmentCreatedEventType},
	{legacy: "ticket.attachment.created", current: eventcontract.TicketAttachmentCreatedEventType},
	{legacy: "ticket.timeout", current: eventcontract.TicketSLABreachedEventType},
	{legacy: "ticket.sla_breached", current: eventcontract.TicketSLABreachedEventType},
	{legacy: "scheduled_check", current: eventcontract.AutomationScheduledCheckEventType},
}

// MigrateAutomationRuleTriggerEvents makes the persisted rule contract match
// the current CloudEvent catalog. It is safe to rerun: only known historical
// values are rewritten, and current values remain unchanged.
func MigrateAutomationRuleTriggerEvents(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if !db.Migrator().HasTable(&models.AutomationRule{}) {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := trimAutomationTriggerValues(tx, &models.AutomationRule{}); err != nil {
			return err
		}
		if tx.Migrator().HasTable(&models.AutomationLog{}) {
			if err := trimAutomationTriggerValues(tx, &models.AutomationLog{}); err != nil {
				return err
			}
		}
		for _, migration := range legacyAutomationTriggerMigrations {
			if err := migrateAutomationRuleTriggerValue(tx, migration); err != nil {
				return err
			}
			if tx.Migrator().HasTable(&models.AutomationLog{}) {
				if err := migrateAutomationTriggerValue(
					tx,
					&models.AutomationLog{},
					migration,
				); err != nil {
					return err
				}
			}
		}

		var persisted []string
		if err := tx.Model(&models.AutomationRule{}).
			Distinct("trigger_event").
			Pluck("trigger_event", &persisted).Error; err != nil {
			return fmt.Errorf("list automation rule trigger events: %w", err)
		}
		var unsupported []string
		for _, eventType := range persisted {
			eventType = strings.TrimSpace(eventType)
			if !eventcontract.IsAutomationRuleTriggerEventType(eventType) {
				unsupported = append(unsupported, eventType)
			}
		}
		if len(unsupported) > 0 {
			sort.Strings(unsupported)
			return fmt.Errorf(
				"automation_rules contains unsupported trigger_event values: %q",
				unsupported,
			)
		}
		return nil
	})
}

func trimAutomationTriggerValues(tx *gorm.DB, model any) error {
	if err := tx.Model(model).
		Where("trigger_event <> TRIM(trigger_event)").
		Update("trigger_event", gorm.Expr("TRIM(trigger_event)")).Error; err != nil {
		return fmt.Errorf("trim automation trigger values: %w", err)
	}
	return nil
}

func migrateAutomationRuleTriggerValue(
	tx *gorm.DB,
	migration automationTriggerMigration,
) error {
	if migration.requiredStatus == "" {
		return migrateAutomationTriggerValue(tx, &models.AutomationRule{}, migration)
	}

	var rules []models.AutomationRule
	if err := tx.Where("trigger_event = ?", migration.legacy).Find(&rules).Error; err != nil {
		return fmt.Errorf("list %q automation rules: %w", migration.legacy, err)
	}
	for index := range rules {
		conditions, err := rules[index].GetConditions()
		if err != nil {
			return fmt.Errorf(
				"decode conditions for automation rule %d: %w",
				rules[index].ID,
				err,
			)
		}
		if !hasAutomationStatusCondition(conditions, migration.requiredStatus) {
			if len(conditions) > 0 {
				conditions[len(conditions)-1].LogicOp = "and"
			}
			conditions = append(conditions, models.RuleCondition{
				Field:    "status",
				Operator: "eq",
				Value:    string(migration.requiredStatus),
			})
		}
		if err := rules[index].SetConditions(conditions); err != nil {
			return fmt.Errorf(
				"encode conditions for automation rule %d: %w",
				rules[index].ID,
				err,
			)
		}
		if err := tx.Model(&models.AutomationRule{}).
			Where("id = ? AND trigger_event = ?", rules[index].ID, migration.legacy).
			Updates(map[string]any{
				"trigger_event": migration.current,
				"conditions":    rules[index].Conditions,
			}).Error; err != nil {
			return fmt.Errorf("migrate automation rule %d: %w", rules[index].ID, err)
		}
	}
	return nil
}

func hasAutomationStatusCondition(
	conditions []models.RuleCondition,
	status models.TicketStatus,
) bool {
	for _, condition := range conditions {
		if condition.Field == "status" &&
			condition.Operator == "eq" &&
			fmt.Sprint(condition.Value) == string(status) {
			return true
		}
	}
	return false
}

func migrateAutomationTriggerValue(
	tx *gorm.DB,
	model any,
	migration automationTriggerMigration,
) error {
	result := tx.Model(model).
		Where("trigger_event = ?", migration.legacy).
		Update("trigger_event", migration.current)
	if result.Error != nil {
		return fmt.Errorf(
			"migrate automation trigger %q to %q: %w",
			migration.legacy,
			migration.current,
			result.Error,
		)
	}
	return nil
}
