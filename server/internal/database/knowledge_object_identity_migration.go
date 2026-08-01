package database

import (
	"fmt"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

// PrepareKnowledgeObjectIdentityColumns preserves old rows as legacy
// references by adding an empty, non-null store_id. No bucket or prefix is
// inferred from mutable runtime configuration.
func PrepareKnowledgeObjectIdentityColumns(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("database is required")
	}
	if !db.Migrator().HasTable(&models.KnowledgeArticleVersion{}) {
		return nil
	}
	if !db.Migrator().HasColumn(
		&models.KnowledgeArticleVersion{},
		"ObjectStoreID",
	) {
		if err := db.Migrator().AddColumn(
			&models.KnowledgeArticleVersion{},
			"ObjectStoreID",
		); err != nil {
			return fmt.Errorf(
				"add knowledge object store_id: %w",
				err,
			)
		}
	}
	return nil
}

func MigrateKnowledgeObjectIdentityContract(db *gorm.DB) error {
	if err := PrepareKnowledgeObjectIdentityColumns(db); err != nil {
		return err
	}
	if strings.EqualFold(db.Dialector.Name(), "postgres") {
		if err := db.Exec(`
			ALTER TABLE knowledge_article_versions
			ALTER COLUMN object_version_id TYPE VARCHAR(1024)
		`).Error; err != nil {
			return fmt.Errorf(
				"widen knowledge object version ID: %w",
				err,
			)
		}
	}
	return ValidateKnowledgeObjectIdentityContract(db)
}

func ValidateKnowledgeObjectIdentityContract(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("database is required")
	}
	if !db.Migrator().HasTable(&models.KnowledgeArticleVersion{}) {
		return fmt.Errorf("knowledge_article_versions table is missing")
	}
	if !db.Migrator().HasColumn(
		&models.KnowledgeArticleVersion{},
		"ObjectStoreID",
	) {
		return fmt.Errorf(
			"knowledge_article_versions.object_store_id is missing",
		)
	}
	columns, err := db.Migrator().ColumnTypes(
		"knowledge_article_versions",
	)
	if err != nil {
		return fmt.Errorf(
			"read knowledge object identity columns: %w",
			err,
		)
	}
	foundStoreID := false
	foundVersionID := false
	for _, column := range columns {
		switch column.Name() {
		case "object_store_id":
			foundStoreID = true
			if nullable, known := column.Nullable(); known && nullable {
				return fmt.Errorf(
					"knowledge_article_versions.object_store_id must be NOT NULL",
				)
			}
		case "object_version_id":
			foundVersionID = true
			if length, known := column.Length(); known &&
				length > 0 && length < 1024 {
				return fmt.Errorf(
					"knowledge_article_versions.object_version_id must support 1024 bytes",
				)
			}
		}
	}
	if !foundStoreID || !foundVersionID {
		return fmt.Errorf(
			"knowledge object identity columns are incomplete",
		)
	}
	return nil
}
