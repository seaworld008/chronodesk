package database

import (
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMigrateA2AIdentifierContractIsIdempotentForUnitDatabase(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open(
			"file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared",
		),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("open A2A migration database: %v", err)
	}
	if err := db.AutoMigrate(&models.AgentTask{}, &models.AgentMessage{}); err != nil {
		t.Fatalf("migrate A2A unit schema: %v", err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if err := MigrateA2AIdentifierContract(db); err != nil {
			t.Fatalf("A2A identifier migration attempt %d: %v", attempt, err)
		}
	}
}
