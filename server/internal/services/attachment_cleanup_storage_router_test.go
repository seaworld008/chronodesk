package services

import (
	"encoding/json"
	"testing"
)

func TestAttachmentCleanupStorageReferencePreservesBackend(t *testing.T) {
	object := AttachmentCleanupObject{
		AttachmentID: 42,
		TicketID:     7,
		StorageType:  "s3",
		StoreID:      "s3-2025",
		StoragePath:  "tickets/7/object.bin",
		VersionID:    "version-17",
	}
	target, err := NewAttachmentCleanupOutboxTargetForObject(object)
	if err != nil {
		t.Fatalf("NewAttachmentCleanupOutboxTarget(): %v", err)
	}
	publicData := json.RawMessage(`{"ticket_id":7}`)
	internalData, err := json.Marshal(map[string]any{
		"ticket_id": 7,
		AttachmentCleanupObjectsDataField: []AttachmentCleanupObject{
			object,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	reference, err := AttachmentCleanupStorageReference(
		CloudEventEnvelope{
			Data:         publicData,
			InternalData: internalData,
		},
		target.ID,
	)
	if err != nil {
		t.Fatalf("AttachmentCleanupStorageReference(): %v", err)
	}
	if reference.StorageType != "s3" ||
		reference.StoreID != "s3-2025" ||
		reference.VersionID != "version-17" ||
		reference.StoragePath != "tickets/7/object.bin" {
		t.Fatalf("unexpected storage reference: %+v", reference)
	}
	object.VersionID = "different-version"
	tamperedData, err := json.Marshal(map[string]any{
		"ticket_id": 7,
		AttachmentCleanupObjectsDataField: []AttachmentCleanupObject{
			object,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AttachmentCleanupStorageReference(
		CloudEventEnvelope{
			Data:         publicData,
			InternalData: tamperedData,
		},
		target.ID,
	); err == nil {
		t.Fatal("cleanup target accepted a tampered version ID")
	}
}

func TestAttachmentCleanupStorageReferenceAcceptsLegacyManifest(
	t *testing.T,
) {
	target, err := NewAttachmentCleanupOutboxTarget(
		9,
		"tickets/3/legacy.txt",
	)
	if err != nil {
		t.Fatalf("NewAttachmentCleanupOutboxTarget(): %v", err)
	}
	publicData := json.RawMessage(`{"ticket_id":3}`)
	internalData, err := json.Marshal(map[string]any{
		"ticket_id": 3,
		AttachmentCleanupObjectsDataField: []AttachmentCleanupObject{
			{
				AttachmentID: 9,
				TicketID:     3,
				StoragePath:  "tickets/3/legacy.txt",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	reference, err := AttachmentCleanupStorageReference(
		CloudEventEnvelope{
			Data:         publicData,
			InternalData: internalData,
		},
		target.ID,
	)
	if err != nil {
		t.Fatalf("AttachmentCleanupStorageReference(): %v", err)
	}
	if reference.StorageType != "" ||
		reference.StoragePath != "tickets/3/legacy.txt" {
		t.Fatalf("unexpected legacy reference: %+v", reference)
	}
}
