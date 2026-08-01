package database

import (
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type legacyKnowledgeArticle struct {
	ID               string `gorm:"primaryKey"`
	OrganizationID   uint
	ProjectID        uint
	CurrentVersionID *string
}

func (legacyKnowledgeArticle) TableName() string {
	return "knowledge_articles"
}

type legacyKnowledgeVersion struct {
	ID             string `gorm:"primaryKey"`
	OrganizationID uint
	ProjectID      uint
	ArticleID      string
	Status         models.KnowledgeVersionStatus
	UpdatedAt      time.Time
}

func (legacyKnowledgeVersion) TableName() string {
	return "knowledge_article_versions"
}

func TestKnowledgePublishedVersionMigrationRepairsCanonicalDuplicateAndGuardsFutureWrites(
	t *testing.T,
) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&legacyKnowledgeArticle{},
		&legacyKnowledgeVersion{},
	); err != nil {
		t.Fatal(err)
	}
	current := "version-2"
	if err := db.Create(&legacyKnowledgeArticle{
		ID:               "article-1",
		OrganizationID:   7,
		ProjectID:        9,
		CurrentVersionID: &current,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]legacyKnowledgeVersion{
		{
			ID: "version-1", OrganizationID: 7, ProjectID: 9,
			ArticleID: "article-1", Status: models.KnowledgeVersionPublished,
		},
		{
			ID: "version-2", OrganizationID: 7, ProjectID: 9,
			ArticleID: "article-1", Status: models.KnowledgeVersionPublished,
		},
	}).Error; err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := MigrateKnowledgePublishedVersionContract(db); err != nil {
			t.Fatalf("migration attempt %d: %v", attempt+1, err)
		}
	}
	var versions []legacyKnowledgeVersion
	if err := db.Order("id ASC").Find(&versions).Error; err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 ||
		versions[0].Status != models.KnowledgeVersionSuperseded ||
		versions[1].Status != models.KnowledgeVersionPublished {
		t.Fatalf("repaired versions = %+v", versions)
	}
	duplicate := legacyKnowledgeVersion{
		ID:             "version-3",
		OrganizationID: 7,
		ProjectID:      9,
		ArticleID:      "article-1",
		Status:         models.KnowledgeVersionPublished,
	}
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("partial unique index accepted a second published version")
	}
}

func TestKnowledgePublishedVersionMigrationRejectsAmbiguousDuplicate(
	t *testing.T,
) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&legacyKnowledgeArticle{},
		&legacyKnowledgeVersion{},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&legacyKnowledgeArticle{
		ID:             "article-ambiguous",
		OrganizationID: 3,
		ProjectID:      4,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]legacyKnowledgeVersion{
		{
			ID: "ambiguous-1", OrganizationID: 3, ProjectID: 4,
			ArticleID: "article-ambiguous",
			Status:    models.KnowledgeVersionPublished,
		},
		{
			ID: "ambiguous-2", OrganizationID: 3, ProjectID: 4,
			ArticleID: "article-ambiguous",
			Status:    models.KnowledgeVersionPublished,
		},
	}).Error; err != nil {
		t.Fatal(err)
	}
	err = PrepareKnowledgePublishedVersionContract(db)
	if err == nil || !strings.Contains(err.Error(), "no canonical") {
		t.Fatalf("ambiguous duplicate error = %v", err)
	}
	var published int64
	if err := db.Model(&legacyKnowledgeVersion{}).
		Where("status = ?", models.KnowledgeVersionPublished).
		Count(&published).Error; err != nil {
		t.Fatal(err)
	}
	if published != 2 {
		t.Fatalf("ambiguous repair mutated rows: published=%d", published)
	}
}

func TestKnowledgePublishedVersionValidationRejectsSameNameWrongIndex(
	t *testing.T,
) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&legacyKnowledgeArticle{},
		&legacyKnowledgeVersion{},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"CREATE INDEX " + knowledgeOnePublishedIndex +
			" ON knowledge_article_versions(article_id)",
	).Error; err != nil {
		t.Fatal(err)
	}
	err = ValidateKnowledgePublishedVersionContract(db)
	if err == nil ||
		!strings.Contains(err.Error(), "definition is invalid") {
		t.Fatalf("wrong same-name index validation error = %v", err)
	}
}

func TestKnowledgePublishedVersionValidationRejectsNonCanonicalPublishedRow(
	t *testing.T,
) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&legacyKnowledgeArticle{},
		&legacyKnowledgeVersion{},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&legacyKnowledgeArticle{
		ID:             "article-orphan",
		OrganizationID: 11,
		ProjectID:      12,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&legacyKnowledgeVersion{
		ID:             "version-orphan",
		OrganizationID: 11,
		ProjectID:      12,
		ArticleID:      "article-orphan",
		Status:         models.KnowledgeVersionPublished,
	}).Error; err != nil {
		t.Fatal(err)
	}
	err = MigrateKnowledgePublishedVersionContract(db)
	if err == nil ||
		!strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("orphan published validation error = %v", err)
	}
}

func TestKnowledgePublishedVersionValidationRejectsCurrentDraftPointer(
	t *testing.T,
) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&legacyKnowledgeArticle{},
		&legacyKnowledgeVersion{},
	); err != nil {
		t.Fatal(err)
	}
	current := "version-draft"
	if err := db.Create(&legacyKnowledgeArticle{
		ID:               "article-current-draft",
		OrganizationID:   13,
		ProjectID:        14,
		CurrentVersionID: &current,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&legacyKnowledgeVersion{
		ID:             current,
		OrganizationID: 13,
		ProjectID:      14,
		ArticleID:      "article-current-draft",
		Status:         models.KnowledgeVersionDraft,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateKnowledgePublishedVersionContract(db); err == nil ||
		!strings.Contains(
			err.Error(),
			"canonical pointers without a matching published version",
		) {
		t.Fatalf("current draft validation error = %v", err)
	}
}
