package database

import (
	"fmt"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

// PrepareAttachmentStorageIdentityColumns is additive and retry-safe. Empty
// values deliberately remain legacy references: runtime routing resolves them
// only when the old storage_type alias is unambiguous.
func PrepareAttachmentStorageIdentityColumns(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("database is required")
	}
	if !db.Migrator().HasTable(&models.TicketAttachment{}) {
		return nil
	}
	for _, field := range []string{
		"StorageStoreID",
		"StorageVersionID",
	} {
		if db.Migrator().HasColumn(&models.TicketAttachment{}, field) {
			continue
		}
		if err := db.Migrator().AddColumn(
			&models.TicketAttachment{},
			field,
		); err != nil {
			return fmt.Errorf(
				"add ticket attachment %s: %w",
				field,
				err,
			)
		}
	}
	return nil
}

func MigrateAttachmentStorageIdentityContract(db *gorm.DB) error {
	if err := PrepareAttachmentStorageIdentityColumns(db); err != nil {
		return err
	}
	return ValidateAttachmentStorageIdentityContract(db)
}

func ValidateAttachmentStorageIdentityContract(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("database is required")
	}
	if !db.Migrator().HasTable(&models.TicketAttachment{}) {
		return fmt.Errorf("ticket_attachments table is missing")
	}
	for _, field := range []string{
		"StorageStoreID",
		"StorageVersionID",
	} {
		if !db.Migrator().HasColumn(&models.TicketAttachment{}, field) {
			return fmt.Errorf(
				"ticket_attachments is missing %s",
				field,
			)
		}
	}
	columns, err := db.Migrator().ColumnTypes("ticket_attachments")
	if err != nil {
		return fmt.Errorf(
			"read ticket attachment storage identity columns: %w",
			err,
		)
	}
	required := map[string]bool{
		"storage_store_id":   false,
		"storage_version_id": false,
	}
	for _, column := range columns {
		if _, exists := required[column.Name()]; !exists {
			continue
		}
		if nullable, known := column.Nullable(); known && nullable {
			return fmt.Errorf(
				"ticket_attachments.%s must be NOT NULL",
				column.Name(),
			)
		}
		required[column.Name()] = true
	}
	for column, found := range required {
		if !found {
			return fmt.Errorf(
				"ticket_attachments.%s is missing",
				column,
			)
		}
	}
	return nil
}
