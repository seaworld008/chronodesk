package models

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTicketAttachmentResponseOmitsStorageGenerationControlData(
	t *testing.T,
) {
	attachment := &TicketAttachment{
		ID:               1,
		TicketID:         2,
		StoragePath:      "tickets/2/private.bin",
		StorageType:      "s3",
		StorageStoreID:   "s3-private-2026",
		StorageVersionID: "private-version-id",
	}
	encoded, err := json.Marshal(attachment.ToResponse())
	if err != nil {
		t.Fatal(err)
	}
	response := string(encoded)
	for _, forbidden := range []string{
		"storage_path",
		"storage_type",
		"storage_store_id",
		"storage_version_id",
		"s3-private-2026",
		"private-version-id",
	} {
		if strings.Contains(response, forbidden) {
			t.Fatalf(
				"attachment response exposed %q: %s",
				forbidden,
				response,
			)
		}
	}
}
