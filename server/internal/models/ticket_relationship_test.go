package models

import (
	"testing"

	"gorm.io/datatypes"
)

func TestEntityLinkAndTicketRelationRequireTypedImmutableProjectScope(t *testing.T) {
	link := EntityLink{
		OrganizationID: 1,
		ProjectID:      2,
		TicketID:       3,
		Kind:           EntityKindDevice,
		ReferenceID:    "cmdb/device-42",
		DisplayName:    "数据库主机 42",
		Metadata:       datatypes.JSON([]byte(`{"serial":"safe-data"}`)),
		CreatedByType:  ActorTypeHuman,
		CreatedByID:    "7",
	}
	if err := link.BeforeCreate(nil); err != nil {
		t.Fatalf("valid entity link rejected: %v", err)
	}
	if link.ID == "" {
		t.Fatal("entity link did not receive public UUIDv7")
	}
	if err := link.BeforeUpdate(nil); err == nil {
		t.Fatal("entity link update was accepted")
	}
	if err := link.BeforeDelete(nil); err == nil {
		t.Fatal("entity link deletion was accepted")
	}

	invalidLink := link
	invalidLink.ID = ""
	invalidLink.Kind = EntityKind("database'; DROP TABLE tickets")
	if err := invalidLink.BeforeCreate(nil); err == nil {
		t.Fatal("unregistered entity kind was accepted")
	}

	relation := TicketRelation{
		OrganizationID: 1,
		ProjectID:      2,
		SourceTicketID: 3,
		TargetTicketID: 4,
		Relation:       TicketRelationBlocks,
		Reason:         "等待上游变更",
		CreatedByType:  ActorTypeHuman,
		CreatedByID:    "7",
	}
	if err := relation.BeforeCreate(nil); err != nil {
		t.Fatalf("valid ticket relation rejected: %v", err)
	}
	if relation.ID == "" {
		t.Fatal("ticket relation did not receive public UUIDv7")
	}
	if err := relation.BeforeUpdate(nil); err == nil {
		t.Fatal("ticket relation update was accepted")
	}
	if err := relation.BeforeDelete(nil); err == nil {
		t.Fatal("ticket relation deletion was accepted")
	}

	selfRelation := relation
	selfRelation.ID = ""
	selfRelation.TargetTicketID = selfRelation.SourceTicketID
	if err := selfRelation.BeforeCreate(nil); err == nil {
		t.Fatal("self relation was accepted")
	}
}
