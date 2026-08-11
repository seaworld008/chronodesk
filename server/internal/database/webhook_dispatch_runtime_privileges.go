package database

import (
	"errors"

	"github.com/seaworld008/chronodesk/server/internal/database/webhookdispatch"
	"gorm.io/gorm"
)

// ValidateWebhookDispatchRuntimePrivileges fails startup unless the non-owner
// runtime role can atomically write the dispatch marker while remaining unable
// to alter the schema, manage table triggers, or invoke the trigger function.
func ValidateWebhookDispatchRuntimePrivileges(db *gorm.DB) error {
	if db == nil || db.Config == nil || db.Statement == nil ||
		db.Dialector == nil {
		return errors.New("database is required")
	}
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	return webhookdispatch.ValidatePostgresRuntimePrivileges(db)
}
