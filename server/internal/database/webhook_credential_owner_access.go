package database

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

func withWebhookCredentialOwnerAccess(
	db *gorm.DB,
	run func(*gorm.DB) error,
) error {
	if db == nil || run == nil {
		return errors.New(
			"webhook credential owner migration database and callback are required",
		)
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() != "postgres" {
			return run(tx)
		}
		tables := []string{
			"domain_events",
			"outbox_deliveries",
			"webhook_delivery_snapshots",
		}
		if err := tx.Exec(
			"LOCK TABLE domain_events, outbox_deliveries, " +
				"webhook_delivery_snapshots IN ACCESS EXCLUSIVE MODE",
		).Error; err != nil {
			return fmt.Errorf(
				"lock webhook credential lifetime tables: %w",
				err,
			)
		}
		var forcedRows []struct {
			Table   string `gorm:"column:table_name"`
			Enabled bool   `gorm:"column:enabled"`
			Forced  bool   `gorm:"column:forced"`
		}
		if err := tx.Raw(`
			SELECT
				table_state.relname AS table_name,
				table_state.relrowsecurity AS enabled,
				table_state.relforcerowsecurity AS forced
			FROM pg_class AS table_state
			JOIN pg_namespace AS namespace
			  ON namespace.oid = table_state.relnamespace
			WHERE namespace.nspname = CURRENT_SCHEMA()
			  AND table_state.relname IN ?
			ORDER BY table_state.relname ASC
		`, tables).Scan(&forcedRows).Error; err != nil {
			return fmt.Errorf(
				"inspect webhook credential FORCE RLS state: %w",
				err,
			)
		}
		if len(forcedRows) != len(tables) {
			return errors.New(
				"webhook credential lifetime tables are missing from the current PostgreSQL schema",
			)
		}
		for _, row := range forcedRows {
			if !row.Forced {
				continue
			}
			if err := tx.Exec(
				"ALTER TABLE " + row.Table +
					" NO FORCE ROW LEVEL SECURITY",
			).Error; err != nil {
				return fmt.Errorf(
					"temporarily disable FORCE RLS for %s: %w",
					row.Table,
					err,
				)
			}
		}
		runErr := run(tx)
		var restoreErrors []error
		for index := len(forcedRows) - 1; index >= 0; index-- {
			row := forcedRows[index]
			if !row.Forced {
				continue
			}
			if err := tx.Exec(
				"ALTER TABLE " + row.Table +
					" FORCE ROW LEVEL SECURITY",
			).Error; err != nil {
				restoreErrors = append(
					restoreErrors,
					fmt.Errorf(
						"restore FORCE RLS for %s: %w",
						row.Table,
						err,
					),
				)
			}
		}
		if err := errors.Join(restoreErrors...); err != nil {
			return errors.Join(runErr, err)
		}
		var restoredRows []struct {
			Table   string `gorm:"column:table_name"`
			Enabled bool   `gorm:"column:enabled"`
			Forced  bool   `gorm:"column:forced"`
		}
		if err := tx.Raw(`
			SELECT
				table_state.relname AS table_name,
				table_state.relrowsecurity AS enabled,
				table_state.relforcerowsecurity AS forced
			FROM pg_class AS table_state
			JOIN pg_namespace AS namespace
			  ON namespace.oid = table_state.relnamespace
			WHERE namespace.nspname = CURRENT_SCHEMA()
			  AND table_state.relname IN ?
			ORDER BY table_state.relname ASC
		`, tables).Scan(&restoredRows).Error; err != nil {
			return errors.Join(
				runErr,
				fmt.Errorf(
					"verify restored webhook credential RLS state: %w",
					err,
				),
			)
		}
		if len(restoredRows) != len(forcedRows) {
			return errors.Join(
				runErr,
				errors.New(
					"webhook credential RLS catalog changed during owner migration",
				),
			)
		}
		originalByTable := make(
			map[string]struct {
				enabled bool
				forced  bool
			},
			len(forcedRows),
		)
		for _, row := range forcedRows {
			originalByTable[row.Table] = struct {
				enabled bool
				forced  bool
			}{
				enabled: row.Enabled,
				forced:  row.Forced,
			}
		}
		for _, row := range restoredRows {
			original, exists := originalByTable[row.Table]
			if !exists ||
				row.Enabled != original.enabled ||
				row.Forced != original.forced {
				return errors.Join(
					runErr,
					fmt.Errorf(
						"webhook credential RLS state for %s was not restored",
						row.Table,
					),
				)
			}
		}
		return runErr
	})
}
