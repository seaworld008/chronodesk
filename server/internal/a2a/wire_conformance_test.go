package a2a

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTaskStatusTimestampUsesProtoJSONUTCForm(t *testing.T) {
	local := time.Date(
		2026,
		time.July,
		29,
		18,
		30,
		45,
		123456000,
		time.FixedZone("Asia/Shanghai", 8*60*60),
	)
	raw, err := json.Marshal(TaskStatus{
		State:     TaskStateWorking,
		Timestamp: local,
	})
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	timestamp, ok := wire["timestamp"].(string)
	if !ok {
		t.Fatalf("timestamp is not a string: %s", raw)
	}
	if !strings.HasSuffix(timestamp, "Z") ||
		strings.Contains(timestamp, "+08:00") {
		t.Fatalf("timestamp is not A2A ProtoJSON UTC: %q", timestamp)
	}
	if timestamp != "2026-07-29T10:30:45.123456Z" {
		t.Fatalf("unexpected UTC timestamp: %q", timestamp)
	}
}

func TestTaskStatusOmitsUnsetTimestamp(t *testing.T) {
	raw, err := json.Marshal(TaskStatus{State: TaskStateSubmitted})
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if _, exists := wire["timestamp"]; exists {
		t.Fatalf("unset optional timestamp was serialized: %s", raw)
	}
}

func TestErrorInfoOmitsAbsentOptionalMetadata(t *testing.T) {
	details := errorDetail("TASK_NOT_FOUND", nil)
	if len(details) != 1 {
		t.Fatalf("unexpected ErrorInfo details: %#v", details)
	}
	info, ok := details[0].(map[string]any)
	if !ok {
		t.Fatalf("ErrorInfo is not an object: %#v", details[0])
	}
	if _, exists := info["metadata"]; exists {
		t.Fatalf("absent optional metadata was serialized: %#v", info)
	}
}
