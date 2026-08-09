package database

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

type webhookCredentialColumnContract struct {
	table            string
	column           string
	postgresDataType string
	postgresUDT      string
	sqliteType       string
	nullable         bool
	characterLength  *int64
}

func webhookCredentialColumnContracts() []webhookCredentialColumnContract {
	varchar20 := int64(20)
	return []webhookCredentialColumnContract{
		{
			table:            "webhook_delivery_snapshots",
			column:           "credential_expires_at",
			postgresDataType: "timestamp with time zone",
			postgresUDT:      "timestamptz",
			sqliteType:       "DATETIME",
			nullable:         false,
		},
		{
			table:            "webhook_delivery_snapshots",
			column:           "credential_shredded_at",
			postgresDataType: "timestamp with time zone",
			postgresUDT:      "timestamptz",
			sqliteType:       "DATETIME",
			nullable:         true,
		},
		{
			table:            "webhook_delivery_snapshots",
			column:           "credential_shred_reason",
			postgresDataType: "character varying",
			postgresUDT:      "varchar",
			sqliteType:       "VARCHAR(20)",
			nullable:         true,
			characterLength:  &varchar20,
		},
		{
			table:            "outbox_deliveries",
			column:           "expires_at",
			postgresDataType: "timestamp with time zone",
			postgresUDT:      "timestamptz",
			sqliteType:       "DATETIME",
			nullable:         true,
		},
		{
			table:            "outbox_deliveries",
			column:           "expired_at",
			postgresDataType: "timestamp with time zone",
			postgresUDT:      "timestamptz",
			sqliteType:       "DATETIME",
			nullable:         true,
		},
	}
}

func validateWebhookCredentialColumnContract(db *gorm.DB) error {
	if err := validateWebhookCredentialColumnContractState(db, false); err != nil {
		return err
	}
	return validateWebhookCredentialStatusColumnContract(db)
}

func validatePreparedWebhookCredentialColumnContract(db *gorm.DB) error {
	if err := validateWebhookCredentialColumnContractState(db, true); err != nil {
		return err
	}
	return validateWebhookCredentialStatusColumnContract(db)
}

func validateWebhookCredentialColumnContractState(
	db *gorm.DB,
	allowNullableDeadline bool,
) error {
	if db == nil {
		return errors.New("webhook credential column contract database is required")
	}
	switch db.Dialector.Name() {
	case "postgres":
		return validatePostgresWebhookCredentialColumnContract(
			db,
			allowNullableDeadline,
		)
	case "sqlite":
		return validateSQLiteWebhookCredentialColumnContract(
			db,
			allowNullableDeadline,
		)
	default:
		return fmt.Errorf(
			"webhook credential column contract is unsupported for database dialect %q",
			db.Dialector.Name(),
		)
	}
}

func validatePostgresWebhookCredentialColumnContract(
	db *gorm.DB,
	allowNullableDeadline bool,
) error {
	type columnState struct {
		TableName       string  `gorm:"column:table_name"`
		ColumnName      string  `gorm:"column:column_name"`
		DataType        string  `gorm:"column:data_type"`
		UDTName         string  `gorm:"column:udt_name"`
		IsNullable      string  `gorm:"column:is_nullable"`
		ColumnDefault   *string `gorm:"column:column_default"`
		CharacterLength *int64  `gorm:"column:character_maximum_length"`
		IsGenerated     string  `gorm:"column:is_generated"`
		IsIdentity      string  `gorm:"column:is_identity"`
	}
	var rows []columnState
	if err := db.Raw(`
		SELECT
			table_name,
			column_name,
			data_type,
			udt_name,
			is_nullable,
			column_default,
			character_maximum_length,
			is_generated,
			is_identity
		FROM information_schema.columns
		WHERE table_schema = CURRENT_SCHEMA()
		  AND (
			(table_name = 'webhook_delivery_snapshots' AND column_name IN (
				'credential_expires_at',
				'credential_shredded_at',
				'credential_shred_reason'
			))
			OR
			(table_name = 'outbox_deliveries' AND column_name IN (
				'expires_at',
				'expired_at'
			))
		  )
		ORDER BY table_name ASC, column_name ASC
	`).Scan(&rows).Error; err != nil {
		return fmt.Errorf(
			"read PostgreSQL webhook credential columns: %w",
			err,
		)
	}
	byKey := make(map[string]columnState, len(rows))
	for _, row := range rows {
		byKey[row.TableName+"."+row.ColumnName] = row
	}
	for _, contract := range webhookCredentialColumnContracts() {
		key := contract.table + "." + contract.column
		row, exists := byKey[key]
		if !exists {
			return fmt.Errorf("%s is missing", key)
		}
		nullable := row.IsNullable == "YES"
		nullableMatches := nullable == contract.nullable
		if allowNullableDeadline &&
			contract.table == "webhook_delivery_snapshots" &&
			contract.column == "credential_expires_at" {
			nullableMatches = true
		}
		if row.DataType != contract.postgresDataType ||
			row.UDTName != contract.postgresUDT ||
			!nullableMatches ||
			row.ColumnDefault != nil ||
			row.IsGenerated != "NEVER" ||
			row.IsIdentity != "NO" ||
			!equalOptionalInt64(
				row.CharacterLength,
				contract.characterLength,
			) {
			return fmt.Errorf(
				"%s has incompatible PostgreSQL type/null/default/length contract",
				key,
			)
		}
	}
	return nil
}

func validateSQLiteWebhookCredentialColumnContract(
	db *gorm.DB,
	allowNullableDeadline bool,
) error {
	type columnState struct {
		Name       string  `gorm:"column:name"`
		Type       string  `gorm:"column:type"`
		NotNull    int     `gorm:"column:notnull"`
		Default    *string `gorm:"column:dflt_value"`
		HiddenFlag int     `gorm:"column:hidden"`
	}
	for _, contract := range webhookCredentialColumnContracts() {
		var rows []columnState
		if err := db.Raw(
			"PRAGMA table_xinfo(`" + contract.table + "`)",
		).Scan(&rows).Error; err != nil {
			return fmt.Errorf(
				"read SQLite %s columns: %w",
				contract.table,
				err,
			)
		}
		var (
			row   columnState
			found bool
		)
		for _, candidate := range rows {
			if candidate.Name == contract.column {
				row = candidate
				found = true
				break
			}
		}
		key := contract.table + "." + contract.column
		if !found {
			return fmt.Errorf("%s is missing", key)
		}
		nullable := row.NotNull == 0
		nullableMatches := nullable == contract.nullable
		if allowNullableDeadline &&
			contract.table == "webhook_delivery_snapshots" &&
			contract.column == "credential_expires_at" {
			nullableMatches = true
		}
		if strings.ToUpper(strings.TrimSpace(row.Type)) !=
			contract.sqliteType ||
			!nullableMatches ||
			row.Default != nil ||
			row.HiddenFlag != 0 {
			return fmt.Errorf(
				"%s has incompatible SQLite type/null/default/length contract",
				key,
			)
		}
	}
	return nil
}

func equalOptionalInt64(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func validateWebhookCredentialStatusColumnContract(db *gorm.DB) error {
	if db == nil {
		return errors.New("webhook credential status column database is required")
	}
	switch db.Dialector.Name() {
	case "postgres":
		var state struct {
			DataType        string  `gorm:"column:data_type"`
			UDTName         string  `gorm:"column:udt_name"`
			IsNullable      string  `gorm:"column:is_nullable"`
			ColumnDefault   *string `gorm:"column:column_default"`
			CharacterLength *int64  `gorm:"column:character_maximum_length"`
			IsGenerated     string  `gorm:"column:is_generated"`
			IsIdentity      string  `gorm:"column:is_identity"`
		}
		result := db.Raw(`
			SELECT
				data_type,
				udt_name,
				is_nullable,
				column_default,
				character_maximum_length,
				is_generated,
				is_identity
			FROM information_schema.columns
			WHERE table_schema = CURRENT_SCHEMA()
			  AND table_name = 'outbox_deliveries'
			  AND column_name = 'status'
		`).Scan(&state)
		if result.Error != nil {
			return fmt.Errorf(
				"read PostgreSQL outbox_deliveries.status contract: %w",
				result.Error,
			)
		}
		if result.RowsAffected != 1 ||
			state.DataType != "character varying" ||
			state.UDTName != "varchar" ||
			state.IsNullable != "NO" ||
			state.CharacterLength == nil ||
			*state.CharacterLength != 20 ||
			state.ColumnDefault == nil ||
			normalizeWebhookStatusDefault(*state.ColumnDefault) != "pending" ||
			state.IsGenerated != "NEVER" ||
			state.IsIdentity != "NO" {
			return errors.New(
				"outbox_deliveries.status has incompatible PostgreSQL type/not null/default/length contract",
			)
		}
		return nil
	case "sqlite":
		var rows []struct {
			Name       string  `gorm:"column:name"`
			Type       string  `gorm:"column:type"`
			NotNull    int     `gorm:"column:notnull"`
			Default    *string `gorm:"column:dflt_value"`
			HiddenFlag int     `gorm:"column:hidden"`
		}
		if err := db.Raw(
			"PRAGMA table_xinfo(`outbox_deliveries`)",
		).Scan(&rows).Error; err != nil {
			return fmt.Errorf(
				"read SQLite outbox_deliveries.status contract: %w",
				err,
			)
		}
		for _, state := range rows {
			if state.Name != "status" {
				continue
			}
			statusType := strings.ToUpper(strings.TrimSpace(state.Type))
			if (statusType != "TEXT" && statusType != "VARCHAR(20)") ||
				state.NotNull != 1 ||
				state.Default == nil ||
				normalizeWebhookStatusDefault(*state.Default) != "pending" ||
				state.HiddenFlag != 0 {
				return errors.New(
					"outbox_deliveries.status has incompatible SQLite type/not null/default/length contract",
				)
			}
			return nil
		}
		return errors.New("outbox_deliveries.status is missing")
	default:
		return fmt.Errorf(
			"webhook credential status column contract is unsupported for database dialect %q",
			db.Dialector.Name(),
		)
	}
}

func normalizeWebhookStatusDefault(value string) string {
	value = strings.TrimSpace(value)
	for strings.HasPrefix(value, "(") {
		close, ok := matchingSQLParenthesis(value, 0)
		if !ok || close != len(value)-1 {
			break
		}
		value = strings.TrimSpace(value[1:close])
	}
	switch value {
	case "'pending'",
		`"pending"`,
		"'pending'::text",
		"'pending'::character varying":
		return "pending"
	default:
		return value
	}
}
