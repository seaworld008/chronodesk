package database

import (
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestInstallAuditLedgerConstraintsIsNoopForSQLite(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.AuditLedgerEntry{},
		&models.AuditChainHead{},
	); err != nil {
		t.Fatal(err)
	}
	if err := InstallAuditLedgerConstraints(db); err != nil {
		t.Fatalf("SQLite constraint installation: %v", err)
	}
}

func TestAuditLedgerPostgresTriggersEnforceAppendAndHeadLock(t *testing.T) {
	for _, required := range []string{
		"BEFORE UPDATE OR DELETE ON audit_ledger_entries",
		"audit ledger entries are append-only",
		"BEFORE DELETE ON audit_chain_heads",
		"BEFORE INSERT ON audit_ledger_entries",
		"FROM audit_chain_heads",
		"FOR UPDATE",
		"NEW.sequence <> current_sequence + 1",
		"NEW.previous_hash <> current_hash",
		"NEW.payload_digest !~ '^[0-9a-f]{64}$'",
	} {
		combined := strings.Join([]string{
			auditLedgerEntryMutationTriggerSQL,
			auditLedgerHeadDeleteTriggerSQL,
			auditLedgerInsertGuardTriggerSQL,
		}, "\n")
		if !strings.Contains(combined, required) {
			t.Errorf("PostgreSQL audit triggers lack %q", required)
		}
	}
}
