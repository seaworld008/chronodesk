package database

import (
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type legacyAttachmentStorageIdentityRow struct {
	ID          uint   `gorm:"primaryKey"`
	StoragePath string `gorm:"size:500;not null"`
	StorageType string `gorm:"size:20;not null"`
}

func (legacyAttachmentStorageIdentityRow) TableName() string {
	return "ticket_attachments"
}

func TestMigrateAttachmentStorageIdentityContractIsAdditive(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&legacyAttachmentStorageIdentityRow{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&legacyAttachmentStorageIdentityRow{
		ID:          7,
		StoragePath: "tickets/1/legacy",
		StorageType: "s3",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateAttachmentStorageIdentityContract(db); err != nil {
		t.Fatal(err)
	}
	var attachment models.TicketAttachment
	if err := db.Select(
		"id",
		"storage_path",
		"storage_type",
		"storage_store_id",
		"storage_version_id",
	).First(&attachment, 7).Error; err != nil {
		t.Fatal(err)
	}
	if attachment.StorageStoreID != "" ||
		attachment.StorageVersionID != "" {
		t.Fatalf(
			"legacy identity must remain unresolved: %+v",
			attachment,
		)
	}
	if err := MigrateAttachmentStorageIdentityContract(db); err != nil {
		t.Fatalf("retry migration: %v", err)
	}
}

func TestValidateAttachmentStorageIdentityContractFailsClosed(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&legacyAttachmentStorageIdentityRow{}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAttachmentStorageIdentityContract(db); err == nil {
		t.Fatal("runtime gate accepted missing store/version columns")
	}
}
