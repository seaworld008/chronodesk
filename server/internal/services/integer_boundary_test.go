package services

import (
	"strings"
	"testing"
)

const overflowingUint = "18446744073709551616"

func TestServiceEventParsersRejectNativeUintOverflow(t *testing.T) {
	if _, _, err := parseAttachmentCleanupDestination(
		attachmentCleanupPrefix + overflowingUint + ":" + strings.Repeat("a", 64),
	); err == nil {
		t.Fatal("parseAttachmentCleanupDestination() accepted native uint overflow")
	}

	if _, err := automationTicketID(CloudEventEnvelope{
		ID:      "overflow-event",
		Subject: "ticket/" + overflowingUint,
	}); err == nil {
		t.Fatal("automationTicketID() accepted native uint overflow")
	}

	if _, _, err := parseTicketNotificationDestination(
		string("ticket_assigned") + ":" + overflowingUint,
	); err == nil {
		t.Fatal("parseTicketNotificationDestination() accepted native uint overflow")
	}
}
