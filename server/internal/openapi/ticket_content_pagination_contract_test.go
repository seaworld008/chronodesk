package openapi

import (
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestTicketContentListsUseBoundedCursorPages(t *testing.T) {
	var document map[string]any
	if err := yaml.Unmarshal(Specification(), &document); err != nil {
		t.Fatalf("parse Agent OpenAPI: %v", err)
	}
	paths := contractMap(t, document["paths"], "paths")
	components := contractMap(t, document["components"], "components")
	parameters := contractMap(
		t,
		components["parameters"],
		"components.parameters",
	)
	schemas := contractMap(t, components["schemas"], "components.schemas")

	limit := contractMap(
		t,
		parameters["TicketContentLimit"],
		"components.parameters.TicketContentLimit",
	)
	limitSchema := contractMap(
		t,
		limit["schema"],
		"components.parameters.TicketContentLimit.schema",
	)
	if limitSchema["default"] != 25 ||
		limitSchema["minimum"] != 1 ||
		limitSchema["maximum"] != 100 {
		t.Fatalf("ticket content limit schema = %#v", limitSchema)
	}

	for _, test := range []struct {
		path       string
		pageSchema string
		envelope   string
		itemRef    string
	}{
		{
			path:       "/tickets/{ticketId}/comments",
			pageSchema: "CommentCursorPage",
			envelope:   "CommentListEnvelope",
			itemRef:    "#/components/schemas/Comment",
		},
		{
			path:       "/tickets/{ticketId}/attachments",
			pageSchema: "AttachmentCursorPage",
			envelope:   "AttachmentListEnvelope",
			itemRef:    "#/components/schemas/Attachment",
		},
	} {
		operation := contractOperation(t, paths, test.path, "get")
		operationParameters := contractSlice(
			t,
			operation["parameters"],
			"GET "+test.path+" parameters",
		)
		for _, reference := range []string{
			"#/components/parameters/TicketContentCursor",
			"#/components/parameters/TicketContentLimit",
		} {
			if !contractSliceContainsReference(
				operationParameters,
				reference,
			) {
				t.Errorf("GET %s lacks %s", test.path, reference)
			}
		}

		page := contractMap(
			t,
			schemas[test.pageSchema],
			"components.schemas."+test.pageSchema,
		)
		if page["additionalProperties"] != false {
			t.Errorf("%s is not closed", test.pageSchema)
		}
		required := contractSlice(
			t,
			page["required"],
			"components.schemas."+test.pageSchema+".required",
		)
		for _, field := range []string{"items", "next_cursor", "has_more"} {
			if !contractSliceContains(required, field) {
				t.Errorf("%s does not require %s", test.pageSchema, field)
			}
		}
		properties := contractMap(
			t,
			page["properties"],
			"components.schemas."+test.pageSchema+".properties",
		)
		items := contractMap(
			t,
			properties["items"],
			"components.schemas."+test.pageSchema+".items",
		)
		if items["maxItems"] != 100 {
			t.Errorf("%s items maxItems = %v", test.pageSchema, items["maxItems"])
		}
		itemSchema := contractMap(
			t,
			items["items"],
			"components.schemas."+test.pageSchema+".items.items",
		)
		if itemSchema["$ref"] != test.itemRef {
			t.Errorf(
				"%s item ref = %v, want %s",
				test.pageSchema,
				itemSchema["$ref"],
				test.itemRef,
			)
		}

		envelope := contractMap(
			t,
			schemas[test.envelope],
			"components.schemas."+test.envelope,
		)
		allOf := contractSlice(
			t,
			envelope["allOf"],
			"components.schemas."+test.envelope+".allOf",
		)
		extension := contractMap(
			t,
			allOf[len(allOf)-1],
			"components.schemas."+test.envelope+".allOf.extension",
		)
		data := contractMap(
			t,
			contractMap(
				t,
				extension["properties"],
				"components.schemas."+test.envelope+".properties",
			)["data"],
			"components.schemas."+test.envelope+".data",
		)
		if data["$ref"] != "#/components/schemas/"+test.pageSchema {
			t.Errorf(
				"%s data ref = %v, want %s",
				test.envelope,
				data["$ref"],
				test.pageSchema,
			)
		}
	}
}
