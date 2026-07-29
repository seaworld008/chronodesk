package models

import (
	"reflect"
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
		Attachments: `["one.txt","two.png"]`,
		Metadata:    `{"trusted":false}`,
	}).ToResponse()
	if !reflect.DeepEqual(comment.Attachments, []string{"one.txt", "two.png"}) {
		t.Fatalf("comment attachments were not decoded: %#v", comment.Attachments)
	}
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
		Attachments: `not-json`,
		Metadata:    "",
	}).ToResponse()

	if comment.Attachments == nil || len(comment.Attachments) != 0 {
		t.Fatalf("attachments must be a stable empty array: %#v", comment.Attachments)
	}
	if comment.Metadata == nil || len(comment.Metadata) != 0 {
		t.Fatalf("metadata must be a stable empty object: %#v", comment.Metadata)
	}
}
