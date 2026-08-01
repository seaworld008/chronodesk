package database

import (
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestPostgresKnowledgePublishedVersionGateRejectsMalformedIndexAndOrphan(
	t *testing.T,
) {
	db, _, _ := openPostgresMembershipReleaseTestDB(
		t,
		"kp",
	)
	if err := db.AutoMigrate(
		&legacyKnowledgeArticle{},
		&legacyKnowledgeVersion{},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		CREATE INDEX idx_knowledge_one_published
		ON knowledge_article_versions(article_id)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := ValidateKnowledgePublishedVersionContract(db); err == nil ||
		!strings.Contains(err.Error(), "definition is invalid") {
		t.Fatalf("malformed PostgreSQL index validation error = %v", err)
	}
	if err := db.Exec(
		"DROP INDEX " + knowledgeOnePublishedIndex,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateKnowledgePublishedVersionContract(db); err != nil {
		t.Fatalf("install exact PostgreSQL index contract: %v", err)
	}

	if err := db.Create(&legacyKnowledgeArticle{
		ID:             "postgres-article-orphan",
		OrganizationID: 21,
		ProjectID:      22,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&legacyKnowledgeVersion{
		ID:             "postgres-version-orphan",
		OrganizationID: 21,
		ProjectID:      22,
		ArticleID:      "postgres-article-orphan",
		Status:         models.KnowledgeVersionPublished,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := ValidateKnowledgePublishedVersionContract(db); err == nil ||
		!strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("orphan PostgreSQL published row validation error = %v", err)
	}
}
