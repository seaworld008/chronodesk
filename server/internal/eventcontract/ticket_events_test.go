package eventcontract

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestAutomationRuleTriggerEventTypesAreUniqueVersionedCloudEvents(t *testing.T) {
	pattern := regexp.MustCompile(`^io\.chronodesk\.[a-z0-9_.-]+\.v1$`)
	seen := make(map[string]struct{})
	for _, eventType := range AutomationRuleTriggerEventTypes() {
		if !pattern.MatchString(eventType) {
			t.Errorf("automation trigger is not a current CloudEvent type: %q", eventType)
		}
		if len(eventType) > 50 {
			t.Errorf("automation trigger exceeds persistence limit: %q", eventType)
		}
		if _, exists := seen[eventType]; exists {
			t.Errorf("duplicate automation trigger type %q", eventType)
		}
		seen[eventType] = struct{}{}
		if !IsAutomationRuleTriggerEventType(eventType) {
			t.Errorf("catalog entry is not accepted by validator: %q", eventType)
		}
	}
	if IsAutomationRuleTriggerEventType("ticket.created") {
		t.Fatal("legacy trigger name is accepted")
	}
}

func TestAutomationManagementUIUsesCurrentEventCatalog(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve event contract test path")
	}
	path := filepath.Join(
		filepath.Dir(currentFile),
		"..",
		"..",
		"..",
		"web",
		"src",
		"admin",
		"automation",
		"triggerEvents.ts",
	)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	for _, eventType := range AutomationRuleTriggerEventTypes() {
		if !strings.Contains(source, "'"+eventType+"'") {
			t.Errorf("management UI is missing current trigger %q", eventType)
		}
	}
	for _, removed := range []string{
		"{ id: 'ticket.created'",
		"{ id: 'ticket.updated'",
		"{ id: 'ticket.assigned'",
		"{ id: 'ticket.resolved'",
		"{ id: 'ticket.closed'",
		"{ id: 'scheduled_check'",
	} {
		if strings.Contains(source, removed) {
			t.Errorf("management UI still publishes legacy trigger choice %q", removed)
		}
	}
}

func TestWebhookDeliveryEventTypesAreUniqueCurrentCloudEvents(t *testing.T) {
	pattern := regexp.MustCompile(`^io\.chronodesk\.[a-z0-9_.-]+\.v1$`)
	seen := make(map[string]struct{})
	for _, eventType := range WebhookDeliveryEventTypes() {
		if !pattern.MatchString(eventType) || len(eventType) > 50 {
			t.Errorf("invalid persisted Webhook event type %q", eventType)
		}
		if _, exists := seen[eventType]; exists {
			t.Errorf("duplicate Webhook event type %q", eventType)
		}
		seen[eventType] = struct{}{}
		if !IsWebhookDeliveryEventType(eventType) {
			t.Errorf("Webhook catalog entry is not accepted: %q", eventType)
		}
	}
	for _, removed := range []string{
		"ticket.created",
		"ticket.resolved",
		"ticket.closed",
		"user.registered",
	} {
		if IsWebhookDeliveryEventType(removed) {
			t.Errorf("legacy Webhook event remains accepted: %q", removed)
		}
	}
}

func TestWebhookManagementUIUsesCurrentEventCatalog(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve event contract test path")
	}
	path := filepath.Join(
		filepath.Dir(currentFile),
		"..",
		"..",
		"..",
		"web",
		"src",
		"admin",
		"settings",
		"WebhookSettings.tsx",
	)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	for _, eventType := range WebhookDeliveryEventTypes() {
		if !strings.Contains(source, "'"+eventType+"'") {
			t.Errorf("Webhook management UI is missing current event %q", eventType)
		}
	}
	for _, removed := range []string{
		"'ticket.created'",
		"'ticket.updated'",
		"'ticket.resolved'",
		"'ticket.closed'",
		"'user.registered'",
		"'system.alert'",
	} {
		if strings.Contains(source, removed) {
			t.Errorf("Webhook management UI still exposes legacy choice %q", removed)
		}
	}
}
