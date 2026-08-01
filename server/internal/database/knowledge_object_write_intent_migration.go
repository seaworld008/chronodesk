package database

import (
	"errors"
	"fmt"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

const (
	knowledgeObjectWriteIntentRecoveryConstraint = "chk_knowledge_object_write_intents_recovery"
	knowledgeObjectWriteIntentIdentityConstraint = "chk_knowledge_object_write_intents_identity"
)

var knowledgeObjectWriteIntentIndexes = []string{
	"idx_knowledge_object_write_version",
	"idx_knowledge_object_write_due",
	"idx_knowledge_object_write_lease",
}

// MigrateKnowledgeObjectWriteIntentContract installs the additive recovery
// table, bounded-claim indexes and direct-SQL constraints. It is safe to run
// repeatedly and deliberately performs no object-store I/O.
func MigrateKnowledgeObjectWriteIntentContract(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if err := db.AutoMigrate(
		&models.KnowledgeObjectWriteIntent{},
	); err != nil {
		return fmt.Errorf(
			"migrate knowledge object write intents: %w",
			err,
		)
	}
	for _, statement := range []string{
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_knowledge_object_write_version ON knowledge_object_write_intents(organization_id, project_id, version_id)",
		"CREATE INDEX IF NOT EXISTS idx_knowledge_object_write_due ON knowledge_object_write_intents(organization_id, project_id, next_attempt_at ASC, id ASC)",
		"CREATE INDEX IF NOT EXISTS idx_knowledge_object_write_lease ON knowledge_object_write_intents(lease_expires_at ASC, id ASC)",
	} {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf(
				"create knowledge object write intent index: %w",
				err,
			)
		}
	}
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	for _, statement := range []string{
		"ALTER TABLE knowledge_object_write_intents DROP CONSTRAINT IF EXISTS " +
			knowledgeObjectWriteIntentRecoveryConstraint,
		"ALTER TABLE knowledge_object_write_intents ADD CONSTRAINT " +
			knowledgeObjectWriteIntentRecoveryConstraint +
			" CHECK (attempts BETWEEN 0 AND 1000000 AND fencing_token >= 0 AND btrim(lease_owner) = lease_owner AND length(lease_owner) <= 96 AND failure_code IN ('', 'storage_unavailable', 'identity_unavailable', 'database_unavailable'))",
		"ALTER TABLE knowledge_object_write_intents DROP CONSTRAINT IF EXISTS " +
			knowledgeObjectWriteIntentIdentityConstraint,
		"ALTER TABLE knowledge_object_write_intents ADD CONSTRAINT " +
			knowledgeObjectWriteIntentIdentityConstraint +
			" CHECK (size_bytes > 0 AND object_key <> '' AND object_store_id ~ '^[a-z][a-z0-9-]{0,62}$' AND content_hash ~ '^[0-9a-f]{64}$')",
	} {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf(
				"install knowledge object write intent constraint: %w",
				err,
			)
		}
	}
	return nil
}

// ValidateKnowledgeObjectWriteIntentContract is a fail-fast runtime gate. A
// server without the due/lease indexes could turn recovery into an unbounded
// table scan; a missing unique index could let two requests clean the same
// version identity independently.
func ValidateKnowledgeObjectWriteIntentContract(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if !db.Migrator().HasTable(&models.KnowledgeObjectWriteIntent{}) {
		return knowledgeObjectWriteIntentMigrationRequired("table")
	}
	for _, index := range knowledgeObjectWriteIntentIndexes {
		if !db.Migrator().HasIndex(
			&models.KnowledgeObjectWriteIntent{},
			index,
		) {
			return knowledgeObjectWriteIntentMigrationRequired(
				"index " + index,
			)
		}
	}
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	type constraintRow struct {
		Name      string `gorm:"column:name"`
		Validated bool   `gorm:"column:validated"`
	}
	var rows []constraintRow
	if err := db.Raw(`
		SELECT
			constraint_row.conname AS name,
			constraint_row.convalidated AS validated
		FROM pg_constraint AS constraint_row
		JOIN pg_class AS table_row
		  ON table_row.oid = constraint_row.conrelid
		JOIN pg_namespace AS namespace_row
		  ON namespace_row.oid = table_row.relnamespace
		WHERE namespace_row.nspname = CURRENT_SCHEMA()
		  AND table_row.relname = 'knowledge_object_write_intents'
		  AND constraint_row.conname IN ?
	`, []string{
		knowledgeObjectWriteIntentRecoveryConstraint,
		knowledgeObjectWriteIntentIdentityConstraint,
	}).Scan(&rows).Error; err != nil {
		return fmt.Errorf(
			"read knowledge object write intent constraints: %w",
			err,
		)
	}
	validated := make(map[string]bool, len(rows))
	for _, row := range rows {
		validated[row.Name] = row.Validated
	}
	for _, name := range []string{
		knowledgeObjectWriteIntentRecoveryConstraint,
		knowledgeObjectWriteIntentIdentityConstraint,
	} {
		if !validated[name] {
			return knowledgeObjectWriteIntentMigrationRequired(
				"constraint " + name,
			)
		}
	}
	return nil
}

func knowledgeObjectWriteIntentMigrationRequired(
	detail string,
) error {
	return fmt.Errorf(
		"knowledge object write intent %s is missing; run `go run ./cmd/migrate`",
		strings.TrimSpace(detail),
	)
}
