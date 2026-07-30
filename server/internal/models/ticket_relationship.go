package models

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

var entityReferencePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,254}$`)

type EntityKind string

const (
	EntityKindAsset       EntityKind = "asset"
	EntityKindDevice      EntityKind = "device"
	EntityKindApplication EntityKind = "application"
	EntityKindContract    EntityKind = "contract"
	EntityKindCustomer    EntityKind = "customer"
	EntityKindLocation    EntityKind = "location"
	EntityKindOther       EntityKind = "other"
)

func (kind EntityKind) IsValid() bool {
	switch kind {
	case EntityKindAsset,
		EntityKindDevice,
		EntityKindApplication,
		EntityKindContract,
		EntityKindCustomer,
		EntityKindLocation,
		EntityKindOther:
		return true
	default:
		return false
	}
}

// EntityLink attaches a typed external or industry entity to a Ticket without
// moving security-critical fields into custom_fields.
type EntityLink struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime;<-:create"`

	OrganizationID uint           `json:"organization_id" gorm:"not null;index;<-:create"`
	ProjectID      uint           `json:"project_id" gorm:"not null;index;uniqueIndex:idx_entity_link_ticket_reference,priority:1;<-:create"`
	TicketID       uint           `json:"ticket_id" gorm:"not null;index;uniqueIndex:idx_entity_link_ticket_reference,priority:2;<-:create"`
	Kind           EntityKind     `json:"kind" gorm:"size:32;not null;index;uniqueIndex:idx_entity_link_ticket_reference,priority:3;<-:create"`
	ReferenceID    string         `json:"reference_id" gorm:"size:255;not null;uniqueIndex:idx_entity_link_ticket_reference,priority:4;<-:create"`
	DisplayName    string         `json:"display_name" gorm:"size:255;not null;<-:create"`
	Metadata       datatypes.JSON `json:"metadata" gorm:"type:jsonb;not null;<-:create"`
	CreatedByType  ActorType      `json:"created_by_type" gorm:"size:32;not null;<-:create"`
	CreatedByID    string         `json:"created_by_id" gorm:"size:128;not null;<-:create"`
}

func (EntityLink) TableName() string {
	return "entity_links"
}

func (link *EntityLink) BeforeCreate(_ *gorm.DB) error {
	if link.OrganizationID == 0 || link.ProjectID == 0 || link.TicketID == 0 {
		return errors.New("entity link requires project and ticket scope")
	}
	if !link.Kind.IsValid() {
		return fmt.Errorf("invalid entity kind %q", link.Kind)
	}
	link.ReferenceID = strings.TrimSpace(link.ReferenceID)
	link.DisplayName = strings.TrimSpace(link.DisplayName)
	if !entityReferencePattern.MatchString(link.ReferenceID) ||
		link.DisplayName == "" {
		return errors.New("entity reference and display name are required")
	}
	if err := (ActorRef{
		Type: link.CreatedByType,
		ID:   link.CreatedByID,
	}).Validate(); err != nil {
		return fmt.Errorf("invalid entity link actor: %w", err)
	}
	if len(link.Metadata) == 0 {
		link.Metadata = datatypes.JSON([]byte(`{}`))
	}
	if strings.TrimSpace(link.ID) == "" {
		generated, err := uuid.NewV7()
		if err != nil {
			return err
		}
		link.ID = generated.String()
	}
	parsed, err := uuid.Parse(link.ID)
	if err != nil || parsed.String() != link.ID || parsed.Version() != 7 {
		return errors.New("entity link id must be a canonical UUIDv7")
	}
	return nil
}

func (*EntityLink) BeforeUpdate(_ *gorm.DB) error {
	return errors.New("entity links are immutable; replace the link")
}

func (*EntityLink) BeforeDelete(_ *gorm.DB) error {
	return errors.New("entity links are immutable")
}

type TicketRelationType string

const (
	TicketRelationParentOf         TicketRelationType = "parent_of"
	TicketRelationDuplicateOf      TicketRelationType = "duplicate_of"
	TicketRelationBlocks           TicketRelationType = "blocks"
	TicketRelationCollaboratesWith TicketRelationType = "collaborates_with"
)

func (relation TicketRelationType) IsValid() bool {
	switch relation {
	case TicketRelationParentOf,
		TicketRelationDuplicateOf,
		TicketRelationBlocks,
		TicketRelationCollaboratesWith:
		return true
	default:
		return false
	}
}

// TicketRelation is intentionally project-local. Cross-project collaboration
// creates a target-project collaboration Ticket and then stores one local
// relation in each project; it never moves the source Ticket.
type TicketRelation struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime;<-:create"`

	OrganizationID uint               `json:"organization_id" gorm:"not null;index;<-:create"`
	ProjectID      uint               `json:"project_id" gorm:"not null;index;uniqueIndex:idx_ticket_relation_unique,priority:1;<-:create"`
	SourceTicketID uint               `json:"source_ticket_id" gorm:"not null;index;uniqueIndex:idx_ticket_relation_unique,priority:2;<-:create"`
	TargetTicketID uint               `json:"target_ticket_id" gorm:"not null;index;uniqueIndex:idx_ticket_relation_unique,priority:3;<-:create"`
	Relation       TicketRelationType `json:"relation" gorm:"size:32;not null;index;uniqueIndex:idx_ticket_relation_unique,priority:4;<-:create"`
	Reason         string             `json:"reason" gorm:"size:1000;<-:create"`
	CreatedByType  ActorType          `json:"created_by_type" gorm:"size:32;not null;<-:create"`
	CreatedByID    string             `json:"created_by_id" gorm:"size:128;not null;<-:create"`
}

func (TicketRelation) TableName() string {
	return "ticket_relations"
}

func (relation *TicketRelation) BeforeCreate(_ *gorm.DB) error {
	if relation.OrganizationID == 0 || relation.ProjectID == 0 ||
		relation.SourceTicketID == 0 || relation.TargetTicketID == 0 {
		return errors.New("ticket relation requires project and ticket scope")
	}
	if relation.SourceTicketID == relation.TargetTicketID {
		return errors.New("ticket cannot relate to itself")
	}
	if !relation.Relation.IsValid() {
		return fmt.Errorf("invalid ticket relation %q", relation.Relation)
	}
	if err := (ActorRef{
		Type: relation.CreatedByType,
		ID:   relation.CreatedByID,
	}).Validate(); err != nil {
		return fmt.Errorf("invalid ticket relation actor: %w", err)
	}
	relation.Reason = strings.TrimSpace(relation.Reason)
	if strings.TrimSpace(relation.ID) == "" {
		generated, err := uuid.NewV7()
		if err != nil {
			return err
		}
		relation.ID = generated.String()
	}
	parsed, err := uuid.Parse(relation.ID)
	if err != nil || parsed.String() != relation.ID || parsed.Version() != 7 {
		return errors.New("ticket relation id must be a canonical UUIDv7")
	}
	return nil
}

func (*TicketRelation) BeforeUpdate(_ *gorm.DB) error {
	return errors.New("ticket relations are immutable; replace the relation")
}

func (*TicketRelation) BeforeDelete(_ *gorm.DB) error {
	return errors.New("ticket relations are immutable")
}
