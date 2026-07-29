package models

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestTextBackedJSONFieldsAreDecodedIntoStableResponses(t *testing.T) {
	history := (&TicketHistory{
		Details:  `{"field":"status"}`,
		Metadata: `{"source":"automation"}`,
	}).ToResponse()
	if history.Details["field"] != "status" || history.Metadata["source"] != "automation" {
		t.Fatalf("history JSON fields were not decoded: %#v", history)
	}

	comment := (&TicketComment{
		Metadata: `{"trusted":false}`,
	}).ToResponse()
	if trusted, ok := comment.Metadata["trusted"].(bool); !ok || trusted {
		t.Fatalf("comment metadata was not decoded: %#v", comment.Metadata)
	}

	category := (&Category{
		AllowedRoles:    `["admin","manager"]`,
		RestrictedRoles: `["requester"]`,
		Tags:            `["support"]`,
		Metadata:        `{"tier":1}`,
	}).ToResponse()
	if !reflect.DeepEqual(category.AllowedRoles, []string{"admin", "manager"}) ||
		!reflect.DeepEqual(category.RestrictedRoles, []string{"requester"}) ||
		!reflect.DeepEqual(category.Tags, []string{"support"}) {
		t.Fatalf("category JSON fields were not decoded: %#v", category)
	}

}

func TestTextBackedJSONFieldsUseEmptyCollectionsForBlankOrInvalidLegacyData(t *testing.T) {
	comment := (&TicketComment{
		Metadata: "",
	}).ToResponse()

	if comment.Metadata == nil || len(comment.Metadata) != 0 {
		t.Fatalf("metadata must be a stable empty object: %#v", comment.Metadata)
	}
}

func TestTicketAndCommentResponsesOmitLegacyAttachmentArrays(t *testing.T) {
	responses := map[string]any{
		"ticket":  (&Ticket{}).ToResponse(),
		"comment": (&TicketComment{}).ToResponse(),
	}
	for name, response := range responses {
		encoded, err := json.Marshal(response)
		if err != nil {
			t.Fatalf("marshal %s response: %v", name, err)
		}
		if strings.Contains(string(encoded), `"attachments"`) {
			t.Fatalf("%s response exposes legacy attachment array: %s", name, encoded)
		}
	}
}
