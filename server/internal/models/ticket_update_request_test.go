package models

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestTicketUpdateRequestDueDateJSONStates(t *testing.T) {
	wantTime := time.Date(2026, time.August, 3, 9, 30, 0, 0, time.UTC)
	for _, test := range []struct {
		name        string
		body        string
		wantPresent bool
		wantValue   *time.Time
	}{
		{name: "omitted", body: `{}`},
		{name: "explicit null", body: `{"due_date":null}`, wantPresent: true},
		{
			name:        "value",
			body:        `{"due_date":"2026-08-03T09:30:00Z"}`,
			wantPresent: true,
			wantValue:   &wantTime,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var request TicketUpdateRequest
			if err := json.Unmarshal([]byte(test.body), &request); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			got, present := request.DueDate.Value()
			if present != test.wantPresent {
				t.Fatalf("present = %v, want %v", present, test.wantPresent)
			}
			if test.wantValue == nil {
				if got != nil {
					t.Fatalf("value = %v, want nil", got)
				}
				return
			}
			if got == nil || !got.Equal(*test.wantValue) {
				t.Fatalf("value = %v, want %v", got, test.wantValue)
			}
		})
	}
}

func TestTicketUpdateRequestDueDateRejectsInvalidJSONValue(t *testing.T) {
	var request TicketUpdateRequest
	if err := json.Unmarshal([]byte(`{"due_date":"not-a-date"}`), &request); err == nil {
		t.Fatal("invalid due_date must fail decoding")
	}
}

func TestTicketUpdateRequestDueDateMarshalPreservesPresence(t *testing.T) {
	omitted, err := json.Marshal(TicketUpdateRequest{})
	if err != nil {
		t.Fatalf("marshal omitted due_date: %v", err)
	}
	if bytes.Contains(omitted, []byte(`"due_date"`)) {
		t.Fatalf("omitted due_date was serialized: %s", omitted)
	}

	explicitNull, err := json.Marshal(TicketUpdateRequest{
		DueDate: NewOptionalTime(nil),
	})
	if err != nil {
		t.Fatalf("marshal null due_date: %v", err)
	}
	if !bytes.Contains(explicitNull, []byte(`"due_date":null`)) {
		t.Fatalf("explicit null due_date was not serialized: %s", explicitNull)
	}

	dueDate := time.Date(2026, time.August, 3, 9, 30, 0, 0, time.UTC)
	explicitValue, err := json.Marshal(TicketUpdateRequest{
		DueDate: NewOptionalTime(&dueDate),
	})
	if err != nil {
		t.Fatalf("marshal due_date value: %v", err)
	}
	if !bytes.Contains(explicitValue, []byte(`"due_date":"2026-08-03T09:30:00Z"`)) {
		t.Fatalf("due_date value was not serialized: %s", explicitValue)
	}
}
