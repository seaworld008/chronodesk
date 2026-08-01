package models

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// KnowledgeObjectWriteIntent is private recovery control data for the gap
// between an immutable authored-document object write and the PostgreSQL
// transaction that takes ownership of that exact object generation.
//
// The row deliberately has no foreign key to KnowledgeArticleVersion: it must
// exist before either the article or version is committed. A successful
// business transaction deletes the row atomically with the version insert.
type KnowledgeObjectWriteIntent struct {
	ID        string    `json:"-" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt time.Time `json:"-" gorm:"autoCreateTime;<-:create"`
	UpdatedAt time.Time `json:"-" gorm:"autoUpdateTime"`

	OrganizationID uint `json:"-" gorm:"not null;index;uniqueIndex:idx_knowledge_object_write_version,priority:1;<-:create"`
	ProjectID      uint `json:"-" gorm:"not null;index;uniqueIndex:idx_knowledge_object_write_version,priority:2;<-:create"`

	ArticleID string `json:"-" gorm:"size:36;not null;index;<-:create"`
	VersionID string `json:"-" gorm:"size:36;not null;uniqueIndex:idx_knowledge_object_write_version,priority:3;<-:create"`

	ObjectProvider  string `json:"-" gorm:"size:64;not null;<-:create"`
	ObjectStoreID   string `json:"-" gorm:"size:63;not null;index;<-:create"`
	ObjectKey       string `json:"-" gorm:"size:1000;not null;<-:create"`
	ObjectVersionID string `json:"-" gorm:"size:1024"`
	SizeBytes       int64  `json:"-" gorm:"not null;<-:create"`
	ContentHash     string `json:"-" gorm:"size:64;not null;<-:create"`
	ReceiptRecorded bool   `json:"-" gorm:"not null;default:false"`

	CreatedByType ActorType `json:"-" gorm:"size:32;not null;<-:create"`
	CreatedByID   string    `json:"-" gorm:"size:128;not null;<-:create"`

	NextAttemptAt  time.Time  `json:"-" gorm:"not null;index"`
	LeaseOwner     string     `json:"-" gorm:"size:96;index"`
	LeaseExpiresAt *time.Time `json:"-" gorm:"index"`
	FencingToken   uint64     `json:"-" gorm:"not null;default:0"`
	Attempts       uint       `json:"-" gorm:"not null;default:0"`
	FailureCode    string     `json:"-" gorm:"size:48"`
}

func (KnowledgeObjectWriteIntent) TableName() string {
	return "knowledge_object_write_intents"
}

func (intent *KnowledgeObjectWriteIntent) BeforeCreate(_ *gorm.DB) error {
	if strings.TrimSpace(intent.ID) == "" {
		generated, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf(
				"generate knowledge object write intent UUIDv7: %w",
				err,
			)
		}
		intent.ID = generated.String()
	}
	if intent.NextAttemptAt.IsZero() {
		intent.NextAttemptAt = time.Now().UTC()
	}
	return intent.Validate()
}

func (intent KnowledgeObjectWriteIntent) Validate() error {
	parsedID, err := uuid.Parse(intent.ID)
	if err != nil ||
		parsedID.Version() != 7 ||
		parsedID.Variant() != uuid.RFC4122 ||
		parsedID.String() != intent.ID {
		return errors.New(
			"knowledge object write intent id must be a canonical UUIDv7",
		)
	}
	if err := validateKnowledgeScope(
		intent.OrganizationID,
		intent.ProjectID,
	); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"article": intent.ArticleID,
		"version": intent.VersionID,
	} {
		parsed, parseErr := uuid.Parse(strings.TrimSpace(value))
		if parseErr != nil || parsed.String() != value {
			return fmt.Errorf(
				"knowledge object write intent %s id must be a canonical UUID",
				name,
			)
		}
	}
	if strings.TrimSpace(intent.ObjectProvider) == "" ||
		len(intent.ObjectProvider) > 64 ||
		!knowledgeStoreIDPattern.MatchString(intent.ObjectStoreID) ||
		strings.TrimSpace(intent.ObjectKey) == "" ||
		len(intent.ObjectKey) > 1000 ||
		strings.ContainsRune(intent.ObjectKey, '\x00') {
		return errors.New(
			"knowledge object write intent storage identity is invalid",
		)
	}
	if len(intent.ObjectVersionID) > 1024 ||
		strings.ContainsRune(intent.ObjectVersionID, '\x00') {
		return errors.New(
			"knowledge object write intent version ID is invalid",
		)
	}
	if intent.SizeBytes <= 0 ||
		!knowledgeHashPattern.MatchString(intent.ContentHash) {
		return errors.New(
			"knowledge object write intent content identity is invalid",
		)
	}
	if err := (ActorRef{
		Type: intent.CreatedByType,
		ID:   intent.CreatedByID,
	}).Validate(); err != nil {
		return fmt.Errorf(
			"knowledge object write intent creator is invalid: %w",
			err,
		)
	}
	if intent.NextAttemptAt.IsZero() ||
		len(intent.LeaseOwner) > 96 ||
		intent.Attempts > 1_000_000 ||
		len(intent.FailureCode) > 48 {
		return errors.New(
			"knowledge object write intent recovery state is invalid",
		)
	}
	switch intent.FailureCode {
	case "",
		"storage_unavailable",
		"identity_unavailable",
		"database_unavailable":
	default:
		return errors.New(
			"knowledge object write intent failure code is invalid",
		)
	}
	return nil
}
