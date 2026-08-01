package database

import (
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestPostgresKnowledgeObjectWriteIntentContractEnforcesRecoveryIdentity(
	t *testing.T,
) {
	db, _, _ := openPostgresMembershipReleaseTestDB(
		t,
		"koi",
	)
	if err := MigrateKnowledgeObjectWriteIntentContract(db); err != nil {
		t.Fatal(err)
	}
	if err := ValidateKnowledgeObjectWriteIntentContract(db); err != nil {
		t.Fatal(err)
	}
	intent := models.KnowledgeObjectWriteIntent{
		OrganizationID: 1,
		ProjectID:      2,
		ArticleID:      "019fbd64-6d73-7c5a-96e2-4df5a67f0144",
		VersionID:      "019fbd64-6d73-7436-927d-f28a739fe979",
		ObjectProvider: "s3",
		ObjectStoreID:  "s3-primary",
		ObjectKey:      "knowledge/2/article/version.md",
		SizeBytes:      8,
		ContentHash:    strings.Repeat("a", 64),
		CreatedByType:  models.ActorTypeSystem,
		CreatedByID:    "migration-test",
		NextAttemptAt:  time.Now().UTC(),
	}
	if err := db.Create(&intent).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		UPDATE knowledge_object_write_intents
		SET attempts = 1000001
		WHERE id = ?
	`, intent.ID).Error; err == nil {
		t.Fatal("PostgreSQL accepted an unbounded recovery attempt counter")
	}
	if err := db.Exec(`
		UPDATE knowledge_object_write_intents
		SET failure_code = 'endpoint=https://private.example'
		WHERE id = ?
	`, intent.ID).Error; err == nil {
		t.Fatal("PostgreSQL accepted an unbounded recovery error message")
	}
	duplicate := intent
	duplicate.ID = ""
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("PostgreSQL accepted duplicate project/version recovery ownership")
	}
}
