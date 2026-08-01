package models

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestKnowledgeSourceLinkCreatesUUIDv7AndValidatesSnapshots(t *testing.T) {
	articleID := uuid.Must(uuid.NewV7()).String()
	versionID := uuid.Must(uuid.NewV7()).String()
	attachmentID := uint(31)
	link := KnowledgeSourceLink{
		OrganizationID:     1,
		ProjectID:          2,
		ArticleID:          articleID,
		VersionID:          versionID,
		Ordinal:            0,
		SourceTicketID:     7,
		SourceAttachmentID: &attachmentID,
		TicketNumber:       "OPS-7",
		TicketTitle:        "数据库恢复",
		AttachmentName:     "evidence.txt",
		AttachmentHash:     strings.Repeat("a", 64),
		CreatedByType:      ActorTypeHuman,
		CreatedByID:        "42",
	}
	if err := link.BeforeCreate(nil); err != nil {
		t.Fatalf("create source link: %v", err)
	}
	parsed, err := uuid.Parse(link.ID)
	if err != nil || parsed.Version() != 7 {
		t.Fatalf("source id = %q, want UUIDv7", link.ID)
	}
	if link.TableName() != "knowledge_source_links" {
		t.Fatalf("table name = %q", link.TableName())
	}
	if err := link.Validate(); err != nil {
		t.Fatalf("validate source link: %v", err)
	}
}

func TestKnowledgeSourceLinkRequiresConsistentAttachmentSnapshot(t *testing.T) {
	base := KnowledgeSourceLink{
		OrganizationID: 1,
		ProjectID:      2,
		ArticleID:      uuid.Must(uuid.NewV7()).String(),
		VersionID:      uuid.Must(uuid.NewV7()).String(),
		SourceTicketID: 7,
		TicketNumber:   "OPS-7",
		TicketTitle:    "数据库恢复",
		CreatedByType:  ActorTypeHuman,
		CreatedByID:    "42",
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("ticket-only source is invalid: %v", err)
	}
	base.AttachmentName = "orphan.txt"
	if err := base.Validate(); err == nil {
		t.Fatal("attachment snapshot without attachment id was accepted")
	}
	attachmentID := uint(9)
	base.SourceAttachmentID = &attachmentID
	base.AttachmentHash = "not-a-hash"
	if err := base.Validate(); err == nil {
		t.Fatal("attachment source without SHA-256 was accepted")
	}
}

func TestKnowledgeSourceLinkIsImmutable(t *testing.T) {
	if err := (&KnowledgeSourceLink{}).BeforeUpdate(nil); !errors.Is(
		err,
		ErrKnowledgeSourceLinkImmutable,
	) {
		t.Fatalf("BeforeUpdate() error = %v", err)
	}
}
