package database

import (
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type legacyKnowledgeContributorMembership struct {
	ID        uint `gorm:"primaryKey"`
	ProjectID uint
	UserID    uint
	Role      models.ProjectRole
	IsActive  bool
	Version   uint64
}

func (legacyKnowledgeContributorMembership) TableName() string {
	return "project_memberships"
}

func TestPrepareKnowledgeContributorColumnIsAdditiveAndIdempotent(
	t *testing.T,
) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&legacyKnowledgeContributorMembership{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&legacyKnowledgeContributorMembership{
		ProjectID: 7,
		UserID:    9,
		Role:      models.ProjectRoleRequester,
		IsActive:  true,
		Version:   1,
	}).Error; err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := PrepareKnowledgeContributorColumn(db); err != nil {
			t.Fatalf("attempt %d: %v", attempt+1, err)
		}
	}
	if !db.Migrator().HasColumn(
		&models.ProjectMembership{},
		"KnowledgeContributor",
	) {
		t.Fatal("knowledge contributor column was not installed")
	}
	var contributor bool
	if err := db.Table("project_memberships").
		Select("knowledge_contributor").
		Where("project_id = ? AND user_id = ?", 7, 9).
		Scan(&contributor).Error; err != nil {
		t.Fatal(err)
	}
	if contributor {
		t.Fatal("legacy membership must default to no draft contribution")
	}
}
