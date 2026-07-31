package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// A2APushDeliverySnapshot is the immutable delivery intent captured in the
// same transaction as its A2A protocol event, DomainEvent and OutboxDelivery.
// Mutable push configuration is never consulted after this row commits.
//
// TokenCiphertext and AuthenticationCiphertext are snapshot-specific AEAD
// envelopes. CallbackURL and RequestBody are also excluded from JSON so query
// credentials and untrusted task content cannot leak through generic logging.
type A2APushDeliverySnapshot struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime;<-:create"`

	OrganizationID uint `json:"organization_id" gorm:"not null;index;<-:create"`
	ProjectID      uint `json:"project_id" gorm:"not null;index;<-:create"`

	EventID         string    `json:"event_id" gorm:"size:36;not null;index;uniqueIndex:idx_a2a_push_snapshot_event_config,priority:1;<-:create"`
	TaskID          string    `json:"task_id" gorm:"size:64;not null;index;<-:create"`
	PushConfigID    string    `json:"push_config_id" gorm:"size:64;not null;index;uniqueIndex:idx_a2a_push_snapshot_event_config,priority:2;<-:create"`
	ConfigVersionAt time.Time `json:"config_version_at" gorm:"not null;<-:create"`

	CallbackURL              string         `json:"-" gorm:"size:2048;not null;<-:create"`
	TokenCiphertext          string         `json:"-" gorm:"size:4096;<-:create"`
	AuthenticationCiphertext string         `json:"-" gorm:"size:4096;<-:create"`
	RequestBody              datatypes.JSON `json:"-" gorm:"type:jsonb;not null;<-:create"`
	ContentType              string         `json:"-" gorm:"size:128;not null;<-:create"`
	ProtocolVersion          string         `json:"-" gorm:"size:32;not null;<-:create"`
}

func (A2APushDeliverySnapshot) TableName() string {
	return "a2a_push_delivery_snapshots"
}

func NewA2APushDeliverySnapshot(
	scope ProjectScope,
	eventID string,
	taskID string,
	pushConfigID string,
	configVersionAt time.Time,
	callbackURL string,
	requestBody []byte,
	contentType string,
	protocolVersion string,
) (*A2APushDeliverySnapshot, error) {
	generated, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf(
			"generate A2A push delivery snapshot id: %w",
			err,
		)
	}
	snapshot := &A2APushDeliverySnapshot{
		ID:              generated.String(),
		OrganizationID:  scope.OrganizationID,
		ProjectID:       scope.ProjectID,
		EventID:         strings.TrimSpace(eventID),
		TaskID:          strings.TrimSpace(taskID),
		PushConfigID:    strings.TrimSpace(pushConfigID),
		ConfigVersionAt: configVersionAt.UTC(),
		CallbackURL:     strings.TrimSpace(callbackURL),
		RequestBody:     append(datatypes.JSON(nil), requestBody...),
		ContentType:     strings.TrimSpace(contentType),
		ProtocolVersion: strings.TrimSpace(protocolVersion),
	}
	if err := snapshot.validate(); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (snapshot *A2APushDeliverySnapshot) BeforeCreate(
	_ *gorm.DB,
) error {
	return snapshot.validate()
}

func (*A2APushDeliverySnapshot) BeforeUpdate(_ *gorm.DB) error {
	return errors.New("A2A push delivery snapshots are immutable")
}

func (*A2APushDeliverySnapshot) BeforeDelete(_ *gorm.DB) error {
	return errors.New("A2A push delivery snapshots are immutable")
}

func (snapshot *A2APushDeliverySnapshot) validate() error {
	if snapshot == nil {
		return errors.New("A2A push delivery snapshot is required")
	}
	parsedID, err := uuid.Parse(strings.TrimSpace(snapshot.ID))
	if err != nil || parsedID.Version() != 7 {
		return errors.New(
			"A2A push delivery snapshot id must be UUIDv7",
		)
	}
	scope := ProjectScope{
		OrganizationID: snapshot.OrganizationID,
		ProjectID:      snapshot.ProjectID,
	}
	if err := scope.Validate(); err != nil {
		return fmt.Errorf(
			"A2A push delivery snapshot scope is invalid: %w",
			err,
		)
	}
	if strings.TrimSpace(snapshot.EventID) == "" ||
		strings.TrimSpace(snapshot.TaskID) == "" ||
		strings.TrimSpace(snapshot.PushConfigID) == "" ||
		snapshot.ConfigVersionAt.IsZero() {
		return errors.New(
			"A2A push delivery snapshot source version is required",
		)
	}
	if strings.TrimSpace(snapshot.CallbackURL) == "" ||
		strings.TrimSpace(snapshot.ContentType) == "" ||
		strings.TrimSpace(snapshot.ProtocolVersion) == "" ||
		len(snapshot.RequestBody) == 0 ||
		!json.Valid(snapshot.RequestBody) {
		return errors.New(
			"A2A push delivery snapshot request is incomplete",
		)
	}
	return nil
}
