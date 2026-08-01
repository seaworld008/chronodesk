package database

import (
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type legacyKnowledgeObjectIdentityRow struct {
	ID              string `gorm:"primaryKey;size:36"`
	ObjectProvider  string `gorm:"size:64;not null"`
	ObjectKey       string `gorm:"size:1000;not null"`
	ObjectVersionID string `gorm:"size:255"`
}

func (legacyKnowledgeObjectIdentityRow) TableName() string {
	return "knowledge_article_versions"
}

func TestMigrateKnowledgeObjectIdentityPreservesLegacyRows(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&legacyKnowledgeObjectIdentityRow{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&legacyKnowledgeObjectIdentityRow{
		ID:              "00000000-0000-7000-8000-000000000001",
		ObjectProvider:  "s3",
		ObjectKey:       "knowledge/legacy.md",
		ObjectVersionID: "legacy-version",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateKnowledgeObjectIdentityContract(db); err != nil {
		t.Fatal(err)
	}
	var version models.KnowledgeArticleVersion
	if err := db.Select(
		"id",
		"object_provider",
		"object_key",
		"object_store_id",
		"object_version_id",
	).First(
		&version,
		"id = ?",
		"00000000-0000-7000-8000-000000000001",
	).Error; err != nil {
		t.Fatal(err)
	}
	if version.ObjectStoreID != "" ||
		version.ObjectVersionID != "legacy-version" {
		t.Fatalf("legacy reference was rewritten: %+v", version)
	}
	if err := MigrateKnowledgeObjectIdentityContract(db); err != nil {
		t.Fatalf("retry migration: %v", err)
	}
}

func TestValidateKnowledgeObjectIdentityFailsWithoutStoreColumn(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&legacyKnowledgeObjectIdentityRow{}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateKnowledgeObjectIdentityContract(db); err == nil {
		t.Fatal("runtime gate accepted a missing object_store_id")
	}
}
