package database

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

var ErrAttachmentProjectionRequiresImport = errors.New(
	"legacy attachment projection contains data that requires explicit import",
)

type legacyAttachmentProjectionRow struct {
	ID    uint
	Value sql.NullString `gorm:"column:legacy_value"`
}

type legacyAttachmentProjection struct {
	table  string
	column string
}

var legacyAttachmentProjections = [...]legacyAttachmentProjection{
	{table: "tickets", column: "attachments"},
	{table: "ticket_comments", column: "attachments"},
}

// MigrateAttachmentProjections removes the two legacy attachment arrays after
// proving that neither contains data. TicketAttachment is the only durable
// attachment model; this migration never guesses how a string reference maps
// to a stored attachment.
//
// Run it after MigrateActorProjections and before history-event linking.
func MigrateAttachmentProjections(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	for _, model := range []any{&models.Ticket{}, &models.TicketComment{}} {
		if !db.Migrator().HasTable(model) {
			return fmt.Errorf(
				"attachment projection migration requires table %q",
				attachmentProjectionTableName(model),
			)
		}
	}

	return db.Transaction(func(tx *gorm.DB) error {
		// Validate every legacy source before dropping either column. If one
		// contains an unknown reference, the schema and data remain untouched.
		for _, projection := range legacyAttachmentProjections {
			if !tx.Migrator().HasColumn(projection.table, projection.column) {
				continue
			}
			if err := validateEmptyAttachmentProjection(tx, projection); err != nil {
				return err
			}
		}
		for _, projection := range legacyAttachmentProjections {
			if !tx.Migrator().HasColumn(projection.table, projection.column) {
				continue
			}
			if err := dropAttachmentProjectionColumn(tx, projection); err != nil {
				return err
			}
		}
		return nil
	})
}

func attachmentProjectionTableName(model any) string {
	switch model.(type) {
	case *models.Ticket:
		return "tickets"
	case *models.TicketComment:
		return "ticket_comments"
	default:
		return "unknown"
	}
}

func validateEmptyAttachmentProjection(
	tx *gorm.DB,
	projection legacyAttachmentProjection,
) error {
	rows, err := loadAttachmentProjectionRows(tx, projection)
	if err != nil {
		return fmt.Errorf(
			"load legacy %s.%s values: %w",
			projection.table,
			projection.column,
			err,
		)
	}
	for _, row := range rows {
		if emptyLegacyAttachmentValue(row.Value) {
			continue
		}
		return fmt.Errorf(
			"%w: %s row %d column %s must be manually imported into ticket_attachments",
			ErrAttachmentProjectionRequiresImport,
			projection.table,
			row.ID,
			projection.column,
		)
	}
	return nil
}

func loadAttachmentProjectionRows(
	tx *gorm.DB,
	projection legacyAttachmentProjection,
) ([]legacyAttachmentProjectionRow, error) {
	var rows []legacyAttachmentProjectionRow
	var query string
	switch projection.table {
	case "tickets":
		query = `
			SELECT id, CAST(attachments AS TEXT) AS legacy_value
			FROM tickets
			ORDER BY id ASC`
	case "ticket_comments":
		query = `
			SELECT id, CAST(attachments AS TEXT) AS legacy_value
			FROM ticket_comments
			ORDER BY id ASC`
	default:
		return nil, fmt.Errorf("unsupported legacy attachment table %q", projection.table)
	}
	if err := tx.Raw(query).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func emptyLegacyAttachmentValue(value sql.NullString) bool {
	if !value.Valid {
		return true
	}
	raw := strings.TrimSpace(value.String)
	if raw == "" {
		return true
	}
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return false
	}
	if decoded == nil {
		return true
	}
	items, ok := decoded.([]any)
	return ok && len(items) == 0
}

func dropAttachmentProjectionColumn(
	tx *gorm.DB,
	projection legacyAttachmentProjection,
) error {
	var statement string
	switch tx.Dialector.Name() {
	case "postgres":
		switch projection.table {
		case "tickets":
			statement = `ALTER TABLE tickets DROP COLUMN IF EXISTS attachments`
		case "ticket_comments":
			statement = `ALTER TABLE ticket_comments DROP COLUMN IF EXISTS attachments`
		}
	case "sqlite":
		switch projection.table {
		case "tickets":
			statement = `ALTER TABLE tickets DROP COLUMN attachments`
		case "ticket_comments":
			statement = `ALTER TABLE ticket_comments DROP COLUMN attachments`
		}
	default:
		return fmt.Errorf(
			"attachment projection migration is unsupported for database dialect %q",
			tx.Dialector.Name(),
		)
	}
	if statement == "" {
		return fmt.Errorf("unsupported legacy attachment table %q", projection.table)
	}
	if err := tx.Exec(statement).Error; err != nil {
		return fmt.Errorf(
			"drop legacy %s.%s column: %w",
			projection.table,
			projection.column,
			err,
		)
	}
	return nil
}
