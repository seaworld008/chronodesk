package models

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AdminAuditExportState is the durable lifecycle of a platform audit export.
// Object keys, lease ownership and filter snapshots are intentionally private
// persistence details and must only cross the service boundary through closed
// DTOs.
type AdminAuditExportState string

const (
	AdminAuditExportQueued     AdminAuditExportState = "queued"
	AdminAuditExportProcessing AdminAuditExportState = "processing"
	AdminAuditExportCompleted  AdminAuditExportState = "completed"
	AdminAuditExportFailed     AdminAuditExportState = "failed"
	AdminAuditExportExpired    AdminAuditExportState = "expired"
)

func (state AdminAuditExportState) IsValid() bool {
	switch state {
	case AdminAuditExportQueued,
		AdminAuditExportProcessing,
		AdminAuditExportCompleted,
		AdminAuditExportFailed,
		AdminAuditExportExpired:
		return true
	default:
		return false
	}
}

// AdminAuditExportJob stores an immutable requester/filter/anchor snapshot and
// mutable worker state. Every processing attempt gets a monotonically
// increasing fencing token and a distinct storage object key.
type AdminAuditExportJob struct {
	ID        uint      `json:"-" gorm:"primaryKey;autoIncrement"`
	PublicID  string    `json:"public_id" gorm:"size:36;not null;uniqueIndex;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	RequesterUserID uint         `json:"-" gorm:"not null;index;<-:create"`
	RequesterRole   PlatformRole `json:"-" gorm:"size:30;not null;<-:create"`

	FilterSnapshot  string    `json:"-" gorm:"type:text;not null;<-:create"`
	FilterHash      string    `json:"-" gorm:"size:64;not null;index;<-:create"`
	StartTime       time.Time `json:"-" gorm:"not null;index;<-:create"`
	EndTime         time.Time `json:"-" gorm:"not null;index;<-:create"`
	AnchorCreatedAt time.Time `json:"-" gorm:"not null;<-:create"`
	AnchorID        uint      `json:"-" gorm:"not null;<-:create"`

	State       AdminAuditExportState `json:"state" gorm:"size:24;not null;default:'queued';index"`
	RequestedAt time.Time             `json:"requested_at" gorm:"not null;index"`
	StartedAt   *time.Time            `json:"started_at,omitempty"`
	CompletedAt *time.Time            `json:"completed_at,omitempty"`
	ExpiresAt   *time.Time            `json:"expires_at,omitempty" gorm:"index"`

	LeaseOwner     string     `json:"-" gorm:"size:128;index"`
	LeaseExpiresAt *time.Time `json:"-" gorm:"index"`
	FencingToken   uint64     `json:"-" gorm:"not null;default:0"`
	Attempt        uint       `json:"-" gorm:"not null;default:0"`

	RowCount    int64  `json:"row_count" gorm:"not null;default:0"`
	Truncated   bool   `json:"truncated" gorm:"not null;default:false"`
	ObjectKey   string `json:"-" gorm:"size:512"`
	SHA256      string `json:"sha256,omitempty" gorm:"size:64"`
	SizeBytes   int64  `json:"size_bytes" gorm:"not null;default:0"`
	FailureCode string `json:"failure_code,omitempty" gorm:"size:64"`
}

func (AdminAuditExportJob) TableName() string {
	return "admin_audit_export_jobs"
}

func (job *AdminAuditExportJob) BeforeCreate(_ *gorm.DB) error {
	if strings.TrimSpace(job.PublicID) == "" {
		generated, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate audit export UUIDv7: %w", err)
		}
		job.PublicID = generated.String()
	} else {
		parsed, err := uuid.Parse(job.PublicID)
		if err != nil ||
			parsed.Version() != 7 ||
			parsed.Variant() != uuid.RFC4122 ||
			parsed.String() != job.PublicID {
			return errors.New(
				"audit export public id must be a canonical lowercase UUIDv7",
			)
		}
	}
	if job.State == "" {
		job.State = AdminAuditExportQueued
	}
	if job.RequestedAt.IsZero() {
		job.RequestedAt = time.Now().UTC()
	}
	return job.Validate()
}

func (job AdminAuditExportJob) Validate() error {
	parsedPublicID, err := uuid.Parse(job.PublicID)
	if err != nil ||
		parsedPublicID.Version() != 7 ||
		parsedPublicID.Variant() != uuid.RFC4122 ||
		parsedPublicID.String() != job.PublicID {
		return errors.New(
			"audit export public id must be a canonical lowercase UUIDv7",
		)
	}
	if job.RequesterUserID == 0 {
		return errors.New("audit export requester is required")
	}
	if !job.RequesterRole.IsValid() {
		return errors.New("audit export requester role is invalid")
	}
	if strings.TrimSpace(job.FilterSnapshot) == "" ||
		strings.TrimSpace(job.FilterHash) == "" {
		return errors.New("audit export filter snapshot is required")
	}
	if job.StartTime.IsZero() || job.EndTime.IsZero() ||
		job.StartTime.After(job.EndTime) {
		return errors.New("audit export time range is invalid")
	}
	if job.AnchorCreatedAt.IsZero() || job.AnchorID == 0 {
		return errors.New("audit export anchor is required")
	}
	if !job.State.IsValid() {
		return fmt.Errorf("audit export state %q is invalid", job.State)
	}
	if job.RowCount < 0 || job.RowCount > 100000 ||
		job.SizeBytes < 0 || job.Attempt > 1000000 {
		return errors.New("audit export counters are invalid")
	}
	if len(job.ObjectKey) > 512 || len(job.FailureCode) > 64 {
		return errors.New("audit export persisted metadata is invalid")
	}
	return nil
}
