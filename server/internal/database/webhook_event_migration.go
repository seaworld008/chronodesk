package database

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

var webhookTransitionStatusOrder = [...]models.TicketStatus{
	models.TicketStatusOpen,
	models.TicketStatusInProgress,
	models.TicketStatusPending,
	models.TicketStatusResolved,
	models.TicketStatusClosed,
	models.TicketStatusCancelled,
}

type webhookConfigMigrationRow struct {
	ID            uint
	EnabledEvents string
	FilterRules   string
	Status        models.WebhookStatus
}

// MigrateWebhookEventTaxonomy upgrades subscription JSON and historical log
// text to exact CloudEvent types. Unknown values roll back the transaction.
func MigrateWebhookEventTaxonomy(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if !db.Migrator().HasTable(&models.WebhookConfig{}) {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var configs []webhookConfigMigrationRow
		if err := tx.Table("webhook_configs").
			Select("id", "enabled_events", "filter_rules", "status").
			Find(&configs).Error; err != nil {
			return fmt.Errorf("list Webhook configurations: %w", err)
		}
		for index := range configs {
			eventsJSON, filtersJSON, deactivate, err := migrateWebhookConfigTaxonomy(
				&configs[index],
			)
			if err != nil {
				return fmt.Errorf(
					"migrate Webhook configuration %d: %w",
					configs[index].ID,
					err,
				)
			}
			updates := map[string]any{
				"enabled_events": eventsJSON,
				"filter_rules":   filtersJSON,
			}
			if deactivate && configs[index].Status == models.WebhookStatusActive {
				updates["status"] = models.WebhookStatusInactive
			}
			if err := tx.Table("webhook_configs").
				Where("id = ?", configs[index].ID).
				Updates(updates).Error; err != nil {
				return fmt.Errorf(
					"persist Webhook configuration %d taxonomy: %w",
					configs[index].ID,
					err,
				)
			}
		}
		if !tx.Migrator().HasTable(&models.WebhookLog{}) {
			return nil
		}
		var logs []models.WebhookLog
		if err := tx.Find(&logs).Error; err != nil {
			return fmt.Errorf("list Webhook logs: %w", err)
		}
		for index := range logs {
			eventType, err := migrateWebhookLogEventType(&logs[index])
			if err != nil {
				return fmt.Errorf(
					"migrate Webhook log %d event type %q: %w",
					logs[index].ID,
					logs[index].EventType,
					err,
				)
			}
			if eventType == logs[index].EventType {
				continue
			}
			if err := tx.Table("webhook_logs").
				Where("id = ?", logs[index].ID).
				Update("event_type", eventType).Error; err != nil {
				return fmt.Errorf(
					"persist Webhook log %d event type: %w",
					logs[index].ID,
					err,
				)
			}
		}
		return nil
	})
}

func migrateWebhookConfigTaxonomy(
	config *webhookConfigMigrationRow,
) (string, string, bool, error) {
	var persisted []string
	if strings.TrimSpace(config.EnabledEvents) != "" {
		if err := json.Unmarshal([]byte(config.EnabledEvents), &persisted); err != nil {
			return "", "", false, fmt.Errorf("decode enabled_events: %w", err)
		}
	}
	filters, err := models.DecodeWebhookFilterRules(config.FilterRules)
	if err != nil {
		return "", "", false, err
	}
	canonicalTransitionAlreadyEnabled := false
	for _, value := range persisted {
		if strings.TrimSpace(value) == eventcontract.TicketTransitionedEventType {
			canonicalTransitionAlreadyEnabled = true
			break
		}
	}

	selected := make(map[string]struct{})
	statuses := make(map[models.TicketStatus]struct{})
	if filters != nil {
		for _, status := range filters.TransitionStatuses {
			statuses[status] = struct{}{}
		}
	}
	for _, raw := range persisted {
		value := strings.TrimSpace(raw)
		if eventcontract.IsWebhookDeliveryEventType(value) {
			selected[value] = struct{}{}
			continue
		}
		switch value {
		case "":
			return "", "", false, errors.New("enabled_events contains an empty value")
		case "ticket.created":
			selected[eventcontract.TicketCreatedEventType] = struct{}{}
		case "ticket.assigned":
			selected[eventcontract.TicketAssignedEventType] = struct{}{}
		case "ticket.updated":
			selected[eventcontract.TicketUpdatedEventType] = struct{}{}
			selected[eventcontract.TicketAttachmentCreatedEventType] = struct{}{}
			selected[eventcontract.TicketTransitionedEventType] = struct{}{}
			for _, status := range []models.TicketStatus{
				models.TicketStatusOpen,
				models.TicketStatusInProgress,
				models.TicketStatusPending,
				models.TicketStatusCancelled,
			} {
				statuses[status] = struct{}{}
			}
		case "ticket.resolved":
			selected[eventcontract.TicketTransitionedEventType] = struct{}{}
			statuses[models.TicketStatusResolved] = struct{}{}
		case "ticket.closed":
			selected[eventcontract.TicketTransitionedEventType] = struct{}{}
			statuses[models.TicketStatusClosed] = struct{}{}
		case "ticket.comment":
			selected[eventcontract.TicketCommentCreatedEventType] = struct{}{}
		case "ticket.escalated":
			selected[eventcontract.TicketEscalatedEventType] = struct{}{}
		case "automation.notification":
			selected[eventcontract.AutomationNotificationRequestedEventType] = struct{}{}
		case "system.alert":
			selected[eventcontract.TicketSLABreachedEventType] = struct{}{}
			selected[eventcontract.SystemAlertEventType] = struct{}{}
		case "user.registered":
			// There is no current publisher. Removing the subscription is safer
			// than inventing a canonical event that cannot be delivered.
		default:
			return "", "", false, fmt.Errorf(
				"unsupported enabled_events value %q",
				value,
			)
		}
	}

	if canonicalTransitionAlreadyEnabled &&
		(filters == nil || len(filters.TransitionStatuses) == 0) {
		statuses = map[models.TicketStatus]struct{}{}
	}
	transitionStatuses := orderedWebhookTransitionStatuses(statuses)
	if len(transitionStatuses) == len(webhookTransitionStatusOrder) {
		transitionStatuses = nil
	}
	if _, hasTransitioned := selected[eventcontract.TicketTransitionedEventType]; !hasTransitioned {
		transitionStatuses = nil
	}

	events := orderedWebhookEventTypes(selected)
	eventValues := make([]models.WebhookEventType, 0, len(events))
	for _, eventType := range events {
		eventValues = append(eventValues, models.WebhookEventType(eventType))
	}
	var migratedFilters *models.WebhookFilterRules
	if len(transitionStatuses) > 0 {
		migratedFilters = &models.WebhookFilterRules{
			TransitionStatuses: transitionStatuses,
		}
	}
	if err := models.ValidateWebhookSubscriptions(
		eventValues,
		migratedFilters,
		false,
	); err != nil {
		return "", "", false, err
	}
	eventsData, err := json.Marshal(eventValues)
	if err != nil {
		return "", "", false, err
	}
	filtersData := ""
	if migratedFilters != nil {
		encoded, err := json.Marshal(migratedFilters)
		if err != nil {
			return "", "", false, err
		}
		filtersData = string(encoded)
	}
	return string(eventsData), filtersData, len(eventValues) == 0, nil
}

func orderedWebhookEventTypes(selected map[string]struct{}) []string {
	result := make([]string, 0, len(selected))
	for _, eventType := range eventcontract.WebhookDeliveryEventTypes() {
		if _, exists := selected[eventType]; exists {
			result = append(result, eventType)
		}
	}
	return result
}

func orderedWebhookTransitionStatuses(
	selected map[models.TicketStatus]struct{},
) []models.TicketStatus {
	result := make([]models.TicketStatus, 0, len(selected))
	for _, status := range webhookTransitionStatusOrder {
		if _, exists := selected[status]; exists {
			result = append(result, status)
		}
	}
	return result
}

func migrateWebhookLogEventType(
	log *models.WebhookLog,
) (models.WebhookEventType, error) {
	value := strings.TrimSpace(string(log.EventType))
	if eventcontract.IsWebhookDeliveryEventType(value) {
		return models.WebhookEventType(value), nil
	}
	if embedded := webhookLogCloudEventType(log.EventData); embedded != "" {
		if !eventcontract.IsWebhookDeliveryEventType(embedded) {
			return "", fmt.Errorf("embedded CloudEvent type %q is unsupported", embedded)
		}
		return models.WebhookEventType(embedded), nil
	}
	switch value {
	case "ticket.created":
		return models.WebhookEventTicketCreated, nil
	case "ticket.assigned":
		return models.WebhookEventTicketAssigned, nil
	case "ticket.updated":
		return models.WebhookEventTicketUpdated, nil
	case "ticket.resolved", "ticket.closed":
		return models.WebhookEventTicketTransitioned, nil
	case "ticket.comment":
		return models.WebhookEventTicketComment, nil
	case "ticket.escalated":
		return models.WebhookEventTicketEscalated, nil
	case "automation.notification":
		return models.WebhookEventAutomationNotification, nil
	case "system.alert":
		return models.WebhookEventSystemAlert, nil
	case "user.registered":
		return "", errors.New("user.registered has no canonical publisher")
	default:
		return "", fmt.Errorf("unsupported historical event type %q", value)
	}
}

func webhookLogCloudEventType(eventData string) string {
	var envelope struct {
		Data struct {
			CloudEvent struct {
				Type string `json:"type"`
			} `json:"cloud_event"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(eventData), &envelope) != nil {
		return ""
	}
	return strings.TrimSpace(envelope.Data.CloudEvent.Type)
}
