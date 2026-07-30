package agentplatform

import (
	"encoding/json"
	"math"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

func TestProtocolIntegerParsersRejectNativeUintOverflow(t *testing.T) {
	overflow := "18446744073709551616"
	if strconv.IntSize == 32 {
		overflow = strconv.FormatUint(uint64(math.MaxUint32)+1, 10)
	}

	if _, err := parseWebhookSnapshotDestinationID(
		webhookSnapshotPrefix + overflow,
	); err == nil {
		t.Fatal("parseWebhookSnapshotDestinationID() accepted numeric legacy destination")
	}
	snapshotID := uuid.Must(uuid.NewV7()).String()
	if got, err := parseWebhookSnapshotDestinationID(
		webhookSnapshotPrefix + snapshotID,
	); err != nil || got != snapshotID {
		t.Fatalf(
			"parseWebhookSnapshotDestinationID() = (%q,%v), want %q",
			got,
			err,
			snapshotID,
		)
	}
	if got := ticketIDFromCloudEvent(services.CloudEventEnvelope{
		Subject: "ticket/" + overflow,
	}); got != 0 {
		t.Fatalf("ticketIDFromCloudEvent() = %d, want 0 for native uint overflow", got)
	}
	if _, err := parsePositiveUintString(overflow, "ticket_id"); err == nil {
		t.Fatal("parsePositiveUintString() accepted native uint overflow")
	}
	if _, err := numericUint(json.Number(overflow)); err == nil {
		t.Fatal("numericUint() accepted native uint overflow")
	}
	if _, err := numericUint(math.Ldexp(1, 64)); err == nil {
		t.Fatal("numericUint() accepted a float outside the uint64 range")
	}
}

func TestNumericPositiveIntRejectsIntOverflow(t *testing.T) {
	overflow := "9223372036854775808"
	if strconv.IntSize == 32 {
		overflow = strconv.FormatInt(int64(math.MaxInt32)+1, 10)
	}
	if _, err := numericPositiveInt(json.Number(overflow)); err == nil {
		t.Fatal("numericPositiveInt() accepted native int overflow")
	}
}
