package models

import "time"

// SchemaMigrationCheckpoint records completion of a destructive one-time data
// cutover. Structural migrations remain repeatable, while legacy data
// backfills must never reinterpret identities or project-owned rows created
// after the cutover completed.
type SchemaMigrationCheckpoint struct {
	Key         string    `json:"key" gorm:"primaryKey;size:128"`
	Version     uint      `json:"version" gorm:"not null;<-:create"`
	Checksum    string    `json:"checksum" gorm:"size:64;not null;<-:create"`
	CompletedAt time.Time `json:"completed_at" gorm:"not null;<-:create"`
}

func (SchemaMigrationCheckpoint) TableName() string {
	return "schema_migration_checkpoints"
}
