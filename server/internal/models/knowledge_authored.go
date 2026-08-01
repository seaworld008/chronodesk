package models

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var ErrKnowledgeSourceLinkImmutable = errors.New(
	"knowledge source links are immutable",
)

// KnowledgeSourceLink is an immutable, project-scoped provenance snapshot for
// one authored knowledge version. Ticket and attachment display values are
// copied at authoring time so later edits cannot rewrite the version's cited
// evidence.
type KnowledgeSourceLink struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime;<-:create"`

	OrganizationID uint   `json:"organization_id" gorm:"not null;index;uniqueIndex:idx_knowledge_source_ordinal,priority:1;<-:create"`
	ProjectID      uint   `json:"project_id" gorm:"not null;index;uniqueIndex:idx_knowledge_source_ordinal,priority:2;<-:create"`
	ArticleID      string `json:"article_id" gorm:"size:36;not null;index;<-:create"`
	VersionID      string `json:"version_id" gorm:"size:36;not null;index;uniqueIndex:idx_knowledge_source_ordinal,priority:3;<-:create"`
	Ordinal        uint   `json:"ordinal" gorm:"not null;uniqueIndex:idx_knowledge_source_ordinal,priority:4;<-:create"`

	SourceTicketID     uint  `json:"source_ticket_id" gorm:"not null;index;<-:create"`
	SourceAttachmentID *uint `json:"source_attachment_id,omitempty" gorm:"index;<-:create"`

	TicketNumber   string `json:"ticket_number" gorm:"size:64;not null;<-:create"`
	TicketTitle    string `json:"ticket_title" gorm:"size:255;not null;<-:create"`
	AttachmentName string `json:"attachment_name,omitempty" gorm:"size:255;<-:create"`
	AttachmentHash string `json:"attachment_hash,omitempty" gorm:"size:64;<-:create"`

	CreatedByType ActorType `json:"created_by_type" gorm:"size:32;not null;<-:create"`
	CreatedByID   string    `json:"created_by_id" gorm:"size:128;not null;<-:create"`
}

func (KnowledgeSourceLink) TableName() string {
	return "knowledge_source_links"
}

func (link *KnowledgeSourceLink) BeforeCreate(_ *gorm.DB) error {
	if err := ensureKnowledgeUUID(&link.ID); err != nil {
		return err
	}
	return link.Validate()
}

func (*KnowledgeSourceLink) BeforeUpdate(_ *gorm.DB) error {
	return ErrKnowledgeSourceLinkImmutable
}

func (link KnowledgeSourceLink) Validate() error {
	if err := validateKnowledgeScope(
		link.OrganizationID,
		link.ProjectID,
	); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"article": link.ArticleID,
		"version": link.VersionID,
	} {
		parsed, err := uuid.Parse(strings.TrimSpace(value))
		if err != nil || parsed.String() != value {
			return fmt.Errorf("knowledge source %s id must be a canonical UUID", name)
		}
	}
	if link.SourceTicketID == 0 {
		return errors.New("knowledge source ticket id is required")
	}
	if strings.TrimSpace(link.TicketNumber) == "" ||
		len(link.TicketNumber) > 64 {
		return errors.New("knowledge source ticket number is invalid")
	}
	if strings.TrimSpace(link.TicketTitle) == "" ||
		len([]rune(link.TicketTitle)) > 255 {
		return errors.New("knowledge source ticket title is invalid")
	}
	if link.SourceAttachmentID == nil {
		if link.AttachmentName != "" || link.AttachmentHash != "" {
			return errors.New(
				"knowledge source attachment snapshot requires an attachment id",
			)
		}
	} else {
		if *link.SourceAttachmentID == 0 {
			return errors.New("knowledge source attachment id is invalid")
		}
		if strings.TrimSpace(link.AttachmentName) == "" ||
			len([]rune(link.AttachmentName)) > 255 {
			return errors.New("knowledge source attachment name is invalid")
		}
		if !knowledgeHashPattern.MatchString(link.AttachmentHash) {
			return errors.New(
				"knowledge source attachment hash must be SHA-256",
			)
		}
	}
	if err := (ActorRef{
		Type: link.CreatedByType,
		ID:   link.CreatedByID,
	}).Validate(); err != nil {
		return fmt.Errorf("knowledge source creator is invalid: %w", err)
	}
	return nil
}
