package database

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

const auditLedgerEntryMutationTriggerSQL = `
CREATE OR REPLACE FUNCTION chronodesk_reject_audit_ledger_entry_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION USING
        ERRCODE = '55000',
        MESSAGE = 'audit ledger entries are append-only';
END;
$$;

DROP TRIGGER IF EXISTS trg_audit_ledger_entries_append_only
    ON audit_ledger_entries;

CREATE TRIGGER trg_audit_ledger_entries_append_only
BEFORE UPDATE OR DELETE ON audit_ledger_entries
FOR EACH ROW
EXECUTE FUNCTION chronodesk_reject_audit_ledger_entry_mutation();
`

const auditLedgerHeadDeleteTriggerSQL = `
CREATE OR REPLACE FUNCTION chronodesk_reject_audit_chain_head_delete()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION USING
        ERRCODE = '55000',
        MESSAGE = 'audit chain heads cannot be deleted';
END;
$$;

DROP TRIGGER IF EXISTS trg_audit_chain_heads_no_delete
    ON audit_chain_heads;

CREATE TRIGGER trg_audit_chain_heads_no_delete
BEFORE DELETE ON audit_chain_heads
FOR EACH ROW
EXECUTE FUNCTION chronodesk_reject_audit_chain_head_delete();
`

const auditLedgerInsertGuardTriggerSQL = `
CREATE OR REPLACE FUNCTION chronodesk_validate_audit_ledger_insert()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    current_sequence BIGINT;
    current_hash TEXT;
BEGIN
    SELECT last_sequence, last_hash
      INTO current_sequence, current_hash
      FROM audit_chain_heads
     WHERE organization_id = NEW.organization_id
       AND project_id = NEW.project_id
     FOR UPDATE;

    IF NOT FOUND THEN
        RAISE EXCEPTION USING
            ERRCODE = '23503',
            MESSAGE = 'audit chain head is required before append';
    END IF;

    IF NEW.sequence <> current_sequence + 1 THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'audit ledger sequence does not follow chain head';
    END IF;

    IF NEW.previous_hash <> current_hash THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'audit ledger previous hash does not match chain head';
    END IF;

    IF NEW.previous_hash !~ '^[0-9a-f]{64}$'
       OR NEW.entry_hash !~ '^[0-9a-f]{64}$'
       OR NEW.payload_digest !~ '^[0-9a-f]{64}$' THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'audit ledger hashes must be lowercase SHA-256';
    END IF;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_audit_ledger_entries_chain_guard
    ON audit_ledger_entries;

CREATE TRIGGER trg_audit_ledger_entries_chain_guard
BEFORE INSERT ON audit_ledger_entries
FOR EACH ROW
EXECUTE FUNCTION chronodesk_validate_audit_ledger_insert();
`

// InstallAuditLedgerConstraints installs PostgreSQL-only append and chain
// guards after the two ledger tables exist. SQLite relies on model hooks in
// unit tests.
func InstallAuditLedgerConstraints(db *gorm.DB) error {
	if db == nil {
		return errors.New("audit ledger constraint database is required")
	}
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, statement := range []string{
			auditLedgerEntryMutationTriggerSQL,
			auditLedgerHeadDeleteTriggerSQL,
			auditLedgerInsertGuardTriggerSQL,
		} {
			if err := tx.Exec(statement).Error; err != nil {
				return fmt.Errorf("install audit ledger constraint: %w", err)
			}
		}
		return nil
	})
}
