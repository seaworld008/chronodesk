// Package eventcontract owns the stable CloudEvent type catalog shared by
// domain producers, durable consumers, migrations, and machine adapters.
package eventcontract

const (
	TicketCreatedEventType           = "io.chronodesk.ticket.created.v1"
	TicketUpdatedEventType           = "io.chronodesk.ticket.updated.v1"
	TicketAssignedEventType          = "io.chronodesk.ticket.assigned.v1"
	TicketTransitionedEventType      = "io.chronodesk.ticket.transitioned.v1"
	TicketEscalatedEventType         = "io.chronodesk.ticket.escalated.v1"
	TicketCommentCreatedEventType    = "io.chronodesk.ticket.comment.created.v1"
	TicketAttachmentCreatedEventType = "io.chronodesk.ticket.attachment.created.v1"
	TicketSLABreachedEventType       = "io.chronodesk.ticket.sla.breached.v1"
	TicketDeletedEventType           = "io.chronodesk.ticket.deleted.v1"

	// AutomationScheduledCheckEventType is the durable scheduler request
	// consumed by the automation Outbox destination. It is a CloudEvent type,
	// not an embedded compatibility trigger name.
	AutomationScheduledCheckEventType        = "io.chronodesk.automation.trigger.requested.v1"
	AutomationNotificationRequestedEventType = "io.chronodesk.automation.notification.requested.v1"
	SystemAlertEventType                     = "io.chronodesk.system.alert.v1"
)

var automationRuleTriggerEventTypes = [...]string{
	TicketCreatedEventType,
	TicketUpdatedEventType,
	TicketAssignedEventType,
	TicketTransitionedEventType,
	TicketEscalatedEventType,
	TicketCommentCreatedEventType,
	TicketAttachmentCreatedEventType,
	TicketSLABreachedEventType,
	AutomationScheduledCheckEventType,
}

var webhookDeliveryEventTypes = [...]string{
	TicketCreatedEventType,
	TicketUpdatedEventType,
	TicketAssignedEventType,
	TicketTransitionedEventType,
	TicketEscalatedEventType,
	TicketCommentCreatedEventType,
	TicketAttachmentCreatedEventType,
	TicketSLABreachedEventType,
	TicketDeletedEventType,
	AutomationNotificationRequestedEventType,
	SystemAlertEventType,
}

// AutomationRuleTriggerEventTypes returns an ordered copy suitable for
// validation, migrations, and contract tests.
func AutomationRuleTriggerEventTypes() []string {
	result := make([]string, len(automationRuleTriggerEventTypes))
	copy(result, automationRuleTriggerEventTypes[:])
	return result
}

// IsAutomationRuleTriggerEventType reports whether an AutomationRule may
// persist and consume the exact CloudEvent type.
func IsAutomationRuleTriggerEventType(eventType string) bool {
	for _, allowed := range automationRuleTriggerEventTypes {
		if eventType == allowed {
			return true
		}
	}
	return false
}

// WebhookDeliveryEventTypes returns the exact current CloudEvent types exposed
// as external Webhook subscriptions.
func WebhookDeliveryEventTypes() []string {
	result := make([]string, len(webhookDeliveryEventTypes))
	copy(result, webhookDeliveryEventTypes[:])
	return result
}

// IsWebhookDeliveryEventType rejects synthetic aliases and event families that
// are internal-only.
func IsWebhookDeliveryEventType(eventType string) bool {
	for _, allowed := range webhookDeliveryEventTypes {
		if eventType == allowed {
			return true
		}
	}
	return false
}
