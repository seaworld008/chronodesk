package database

import (
	"errors"
	"fmt"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

const (
	legacyPolicyDecisionEpoch int64 = 1
	policyDecisionEpochIndex        = "idx_policy_decisions_policy_epoch"
)

// PreparePolicyDecisionEpochColumn runs before the canonical model migration.
// Historical PolicyDecision rows predate epoch tracking, so their exact policy
// version cannot be reconstructed. Assigning the initial epoch is deterministic
// and does not derive immutable evidence from a principal's later mutable state.
func PreparePolicyDecisionEpochColumn(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if !db.Migrator().HasTable(&models.PolicyDecision{}) {
		return nil
	}

	return db.Transaction(func(tx *gorm.DB) error {
		hasEpoch, err := hasExactDatabaseColumn(
			tx,
			"policy_decisions",
			"policy_epoch",
		)
		if err != nil {
			return err
		}
		if !hasEpoch {
			if err := tx.Exec(`
				ALTER TABLE policy_decisions
				ADD COLUMN policy_epoch BIGINT
			`).Error; err != nil {
				return fmt.Errorf(
					"add nullable policy decision epoch: %w",
					err,
				)
			}
		}

		if err := tx.Exec(`
			UPDATE policy_decisions
			SET policy_epoch = ?
			WHERE policy_epoch IS NULL OR policy_epoch = 0
		`, legacyPolicyDecisionEpoch).Error; err != nil {
			return fmt.Errorf(
				"backfill historical policy decision epochs: %w",
				err,
			)
		}
		return nil
	})
}

// MigratePolicyDecisionEpochContract runs after the canonical model migration.
// PostgreSQL is the deployment database; SQLite reaches the same shape through
// GORM's table rebuild and is retained here as the unit-test dialect.
func MigratePolicyDecisionEpochContract(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if !db.Migrator().HasTable(&models.PolicyDecision{}) {
		return nil
	}
	if err := PreparePolicyDecisionEpochColumn(db); err != nil {
		return err
	}

	if db.Dialector.Name() == "postgres" {
		if err := db.Transaction(func(tx *gorm.DB) error {
			var invalidCount int64
			if err := tx.Table("policy_decisions").
				Where("policy_epoch IS NULL OR policy_epoch <= 0").
				Count(&invalidCount).Error; err != nil {
				return fmt.Errorf(
					"inspect policy decision epochs: %w",
					err,
				)
			}
			if invalidCount != 0 {
				return fmt.Errorf(
					"policy_decisions contains %d invalid policy epoch row(s)",
					invalidCount,
				)
			}
			if err := tx.Exec(`
				ALTER TABLE policy_decisions
					ALTER COLUMN policy_epoch DROP DEFAULT,
					ALTER COLUMN policy_epoch SET NOT NULL
			`).Error; err != nil {
				return fmt.Errorf(
					"finalize policy decision epoch column: %w",
					err,
				)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	if err := db.Exec(
		"CREATE INDEX IF NOT EXISTS " + policyDecisionEpochIndex +
			" ON policy_decisions(policy_epoch)",
	).Error; err != nil {
		return fmt.Errorf("create policy decision epoch index: %w", err)
	}
	return ValidatePolicyDecisionEpochContract(db)
}

// ValidatePolicyDecisionEpochContract fails closed when runtime could read an
// unversioned decision or a write could silently inherit a baseline epoch.
func ValidatePolicyDecisionEpochContract(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if !db.Migrator().HasTable(&models.PolicyDecision{}) {
		return errors.New(
			"policy_decisions table is missing; run `go run ./cmd/migrate`",
		)
	}
	hasEpoch, err := hasExactDatabaseColumn(
		db,
		"policy_decisions",
		"policy_epoch",
	)
	if err != nil {
		return err
	}
	if !hasEpoch {
		return errors.New(
			"policy_decisions.policy_epoch is missing; run `go run ./cmd/migrate`",
		)
	}
	if !db.Migrator().HasIndex(
		&models.PolicyDecision{},
		policyDecisionEpochIndex,
	) {
		return errors.New(
			"policy_decisions.policy_epoch index is missing; run `go run ./cmd/migrate`",
		)
	}

	// Runtime PostgreSQL connections are subject to project RLS and cannot
	// reliably inspect every decision row. The owner migration checks the data
	// before tightening the column; the runtime gate validates only catalog
	// facts that remain visible without fabricating a ProjectScope.
	if db.Dialector.Name() == "postgres" {
		return validatePostgresPolicyDecisionEpochColumn(db)
	}

	var invalidCount int64
	if err := db.Table("policy_decisions").
		Where("policy_epoch IS NULL OR policy_epoch <= 0").
		Count(&invalidCount).Error; err != nil {
		return fmt.Errorf("validate policy decision epoch rows: %w", err)
	}
	if invalidCount != 0 {
		return fmt.Errorf(
			"policy_decisions contains %d invalid policy epoch row(s); run `go run ./cmd/migrate`",
			invalidCount,
		)
	}
	return validateUnitPolicyDecisionEpochColumn(db)
}

func validatePostgresPolicyDecisionEpochColumn(db *gorm.DB) error {
	var column struct {
		DataType      string  `gorm:"column:data_type"`
		IsNullable    string  `gorm:"column:is_nullable"`
		ColumnDefault *string `gorm:"column:column_default"`
	}
	if err := db.Raw(`
		SELECT data_type, is_nullable, column_default
		FROM information_schema.columns
		WHERE table_schema = CURRENT_SCHEMA()
		  AND table_name = 'policy_decisions'
		  AND column_name = 'policy_epoch'
	`).Scan(&column).Error; err != nil {
		return fmt.Errorf(
			"read policy decision epoch column contract: %w",
			err,
		)
	}
	if column.DataType != "bigint" ||
		column.IsNullable != "NO" ||
		column.ColumnDefault != nil {
		return errors.New(
			"policy_decisions.policy_epoch must be BIGINT NOT NULL without a default; run `go run ./cmd/migrate`",
		)
	}
	return nil
}

func validateUnitPolicyDecisionEpochColumn(db *gorm.DB) error {
	columns, err := db.Migrator().ColumnTypes("policy_decisions")
	if err != nil {
		return fmt.Errorf("read policy decision epoch contract: %w", err)
	}
	for _, column := range columns {
		if column.Name() != "policy_epoch" {
			continue
		}
		if nullable, known := column.Nullable(); !known || nullable {
			return errors.New(
				"policy_decisions.policy_epoch must be NOT NULL",
			)
		}
		if _, hasDefault := column.DefaultValue(); hasDefault {
			return errors.New(
				"policy_decisions.policy_epoch must not have a default",
			)
		}
		return nil
	}
	return errors.New("policy_decisions.policy_epoch is missing")
}
