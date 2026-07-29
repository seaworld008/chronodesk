package database

import (
	"errors"
	"fmt"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

const a2aExternalIdentifierMaxLength = 255

type a2aIdentifierColumn struct {
	table  string
	column string
}

var a2aIdentifierColumns = []a2aIdentifierColumn{
	{table: "agent_tasks", column: "context_id"},
	{table: "agent_tasks", column: "execution_message_id"},
	{table: "agent_messages", column: "id"},
	{table: "agent_messages", column: "context_id"},
	{table: "agent_task_events", column: "context_id"},
	{table: "domain_events", column: "correlation_id"},
	{table: "domain_events", column: "causation_id"},
}

// MigrateA2AIdentifierContract widens client-controlled A2A identifiers before
// traffic is accepted. PostgreSQL does not reliably widen existing VARCHAR
// columns through GORM AutoMigrate, so an installation first created with the
// old 64-character model otherwise keeps rejecting valid current-protocol IDs.
func MigrateA2AIdentifierContract(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if !db.Migrator().HasTable(&models.AgentTask{}) ||
		!db.Migrator().HasTable(&models.AgentMessage{}) {
		return nil
	}
	if db.Dialector.Name() != "postgres" {
		// ChronoDesk deploys on PostgreSQL. SQLite is used by unit tests and
		// does not enforce declared VARCHAR lengths.
		return nil
	}

	return db.Transaction(func(tx *gorm.DB) error {
		for _, identifier := range a2aIdentifierColumns {
			var oversized int64
			countSQL := fmt.Sprintf(
				"SELECT COUNT(*) FROM %s WHERE CHAR_LENGTH(%s) > ?",
				identifier.table,
				identifier.column,
			)
			if err := tx.Raw(
				countSQL,
				a2aExternalIdentifierMaxLength,
			).Scan(&oversized).Error; err != nil {
				return fmt.Errorf(
					"inspect %s.%s identifier lengths: %w",
					identifier.table,
					identifier.column,
					err,
				)
			}
			if oversized > 0 {
				return fmt.Errorf(
					"%s.%s contains %d identifiers longer than %d characters",
					identifier.table,
					identifier.column,
					oversized,
					a2aExternalIdentifierMaxLength,
				)
			}

			alterSQL := fmt.Sprintf(
				"ALTER TABLE %s ALTER COLUMN %s TYPE VARCHAR(%d)",
				identifier.table,
				identifier.column,
				a2aExternalIdentifierMaxLength,
			)
			if err := tx.Exec(alterSQL).Error; err != nil {
				return fmt.Errorf(
					"widen %s.%s: %w",
					identifier.table,
					identifier.column,
					err,
				)
			}
		}
		return validatePostgresA2AIdentifierContract(tx)
	})
}

func validatePostgresA2AIdentifierContract(db *gorm.DB) error {
	for _, identifier := range a2aIdentifierColumns {
		var length int
		if err := db.Raw(
			`SELECT COALESCE(character_maximum_length, 0)
			 FROM information_schema.columns
			 WHERE table_schema = CURRENT_SCHEMA()
			   AND table_name = ?
			   AND column_name = ?`,
			identifier.table,
			identifier.column,
		).Scan(&length).Error; err != nil {
			return fmt.Errorf(
				"inspect %s.%s schema: %w",
				identifier.table,
				identifier.column,
				err,
			)
		}
		if length != a2aExternalIdentifierMaxLength {
			return fmt.Errorf(
				"%s.%s length is %d, require %d",
				identifier.table,
				identifier.column,
				length,
				a2aExternalIdentifierMaxLength,
			)
		}
	}
	return nil
}
