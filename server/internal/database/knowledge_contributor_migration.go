package database

import (
	"fmt"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

// PrepareKnowledgeContributorColumn installs the additive, fail-closed
// per-membership draft grant independently of the resumable model window.
// Existing memberships receive the canonical false default.
func PrepareKnowledgeContributorColumn(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("database is required")
	}
	if !db.Migrator().HasTable(&models.ProjectMembership{}) ||
		db.Migrator().HasColumn(
			&models.ProjectMembership{},
			"KnowledgeContributor",
		) {
		return nil
	}
	if err := db.Migrator().AddColumn(
		&models.ProjectMembership{},
		"KnowledgeContributor",
	); err != nil {
		return fmt.Errorf(
			"add project membership knowledge contributor column: %w",
			err,
		)
	}
	return nil
}
