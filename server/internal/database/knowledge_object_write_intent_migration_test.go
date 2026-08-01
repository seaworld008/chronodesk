package database

import (
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMigrateKnowledgeObjectWriteIntentContractIsAdditiveAndIdempotent(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open(
			"file:knowledge_object_write_intent_contract?mode=memory&cache=shared",
		),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := MigrateKnowledgeObjectWriteIntentContract(db); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateKnowledgeObjectWriteIntentContract(db); err != nil {
		t.Fatal(err)
	}
	for _, index := range knowledgeObjectWriteIntentIndexes {
		if !db.Migrator().HasIndex(
			&models.KnowledgeObjectWriteIntent{},
			index,
		) {
			t.Fatalf("knowledge recovery index %q is missing", index)
		}
	}
}

func TestValidateKnowledgeObjectWriteIntentContractRejectsMissingClaimIndex(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open(
			"file:knowledge_object_write_intent_missing_index?mode=memory&cache=shared",
		),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateKnowledgeObjectWriteIntentContract(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropIndex(
		&models.KnowledgeObjectWriteIntent{},
		"idx_knowledge_object_write_due",
	); err != nil {
		t.Fatal(err)
	}
	err = ValidateKnowledgeObjectWriteIntentContract(db)
	if err == nil ||
		!strings.Contains(err.Error(), "idx_knowledge_object_write_due") {
		t.Fatalf("missing knowledge recovery index error = %v", err)
	}
}
