package database

import (
	"errors"
	"fmt"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

const loginHistoryMethodConstraint = "chk_login_histories_login_method"

// MigrateLoginHistoryMethodContract upgrades the historical VARCHAR(20)
// column before authentication traffic is accepted. The current closed enum's
// longest value is password+otp_required, so a failed OTP challenge can always
// be retained as an audit fact.
func MigrateLoginHistoryMethodContract(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if !db.Migrator().HasTable(&models.LoginHistory{}) {
		return nil
	}
	if !db.Migrator().HasColumn(&models.LoginHistory{}, "LoginMethod") {
		return errors.New("login_histories.login_method is required")
	}

	if db.Dialector.Name() == "postgres" {
		return migratePostgresLoginHistoryMethodContract(db)
	}
	// ChronoDesk 的部署数据库仅支持 PostgreSQL。SQLite 只用于单元测试，
	// 其全新测试表直接由当前模型建立，无需模拟 PostgreSQL 的 ALTER TYPE。
	return nil
}

func migratePostgresLoginHistoryMethodContract(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var invalidCount int64
		if err := tx.Table("login_histories").
			Where(
				`login_method IS NOT NULL
				 AND BTRIM(login_method) <> ''
				 AND login_method NOT IN ?`,
				loginHistoryMethods(),
			).
			Count(&invalidCount).Error; err != nil {
			return fmt.Errorf("inspect login history methods: %w", err)
		}
		if invalidCount > 0 {
			return fmt.Errorf(
				"login_histories contains %d unsupported login methods",
				invalidCount,
			)
		}

		var oversizedCount int64
		if err := tx.Table("login_histories").
			Where("CHAR_LENGTH(login_method) > ?", models.LoginMethodMaxLength).
			Count(&oversizedCount).Error; err != nil {
			return fmt.Errorf("inspect login history method lengths: %w", err)
		}
		if oversizedCount > 0 {
			return fmt.Errorf(
				"login_histories contains %d login methods longer than %d characters",
				oversizedCount,
				models.LoginMethodMaxLength,
			)
		}

		if err := tx.Exec(
			`UPDATE login_histories
			 SET login_method = ?
			 WHERE login_method IS NULL OR BTRIM(login_method) = ''`,
			models.LoginMethodPassword,
		).Error; err != nil {
			return fmt.Errorf("backfill login history methods: %w", err)
		}
		if err := tx.Exec(`
			UPDATE login_histories
			SET is_active = FALSE
			WHERE is_active IS NULL
		`).Error; err != nil {
			return fmt.Errorf("backfill login history activity state: %w", err)
		}

		alterColumn := fmt.Sprintf(
			`ALTER TABLE login_histories
			 ALTER COLUMN login_method TYPE VARCHAR(%d),
			 ALTER COLUMN login_method SET DEFAULT 'password',
			 ALTER COLUMN login_method SET NOT NULL,
			 ALTER COLUMN is_active SET DEFAULT FALSE,
			 ALTER COLUMN is_active SET NOT NULL`,
			models.LoginMethodMaxLength,
		)
		if err := tx.Exec(alterColumn).Error; err != nil {
			return fmt.Errorf("alter login history method column: %w", err)
		}
		if err := tx.Exec(
			"ALTER TABLE login_histories DROP CONSTRAINT IF EXISTS " +
				loginHistoryMethodConstraint,
		).Error; err != nil {
			return fmt.Errorf("drop login history method constraint: %w", err)
		}
		if err := tx.Exec(`
			ALTER TABLE login_histories
			ADD CONSTRAINT chk_login_histories_login_method
			CHECK (
				login_method IN (
					'password',
					'password+trusted',
					'password+otp',
					'password+otp_required'
				)
			)
		`).Error; err != nil {
			return fmt.Errorf("create login history method constraint: %w", err)
		}
		return nil
	})
}

func loginHistoryMethods() []models.LoginMethod {
	return []models.LoginMethod{
		models.LoginMethodPassword,
		models.LoginMethodPasswordTrusted,
		models.LoginMethodPasswordOTP,
		models.LoginMethodOTPRequired,
	}
}

func validatePostgresLoginHistoryMethodContract(db *gorm.DB) error {
	var column struct {
		MaximumLength int    `gorm:"column:maximum_length"`
		IsNullable    string `gorm:"column:is_nullable"`
	}
	if err := db.Raw(`
		SELECT
			COALESCE(character_maximum_length, 0) AS maximum_length,
			is_nullable
		FROM information_schema.columns
		WHERE table_schema = CURRENT_SCHEMA()
		  AND table_name = 'login_histories'
		  AND column_name = 'login_method'
	`).Scan(&column).Error; err != nil {
		return fmt.Errorf("read login history method schema: %w", err)
	}
	if column.MaximumLength != models.LoginMethodMaxLength ||
		column.IsNullable != "NO" {
		return fmt.Errorf(
			"login_histories.login_method must be VARCHAR(%d) NOT NULL; run `go run ./cmd/migrate`",
			models.LoginMethodMaxLength,
		)
	}

	var definition string
	if err := db.Raw(`
		SELECT pg_get_constraintdef(constraint_row.oid)
		FROM pg_constraint AS constraint_row
		JOIN pg_class AS table_row
		  ON table_row.oid = constraint_row.conrelid
		JOIN pg_namespace AS namespace_row
		  ON namespace_row.oid = table_row.relnamespace
		WHERE namespace_row.nspname = CURRENT_SCHEMA()
		  AND table_row.relname = 'login_histories'
		  AND constraint_row.conname = ?
		  AND constraint_row.contype = 'c'
	`, loginHistoryMethodConstraint).Scan(&definition).Error; err != nil {
		return fmt.Errorf("read login history method constraint: %w", err)
	}
	for _, method := range loginHistoryMethods() {
		if !strings.Contains(definition, "'"+string(method)+"'") {
			return fmt.Errorf(
				"login history method constraint is missing %q; run `go run ./cmd/migrate`",
				method,
			)
		}
	}
	return nil
}
