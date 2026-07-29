package database

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMigrateAttachmentProjectionsDropsOnlyEmptyLegacyColumns(t *testing.T) {
	db := openAttachmentProjectionMigrationDB(t)
	for _, value := range []any{nil, "", "  ", "null", "[]", "[\n]"} {
		if err := db.Exec(
			"INSERT INTO tickets (attachments) VALUES (?)",
			value,
		).Error; err != nil {
			t.Fatalf("seed ticket projection %v: %v", value, err)
		}
		if err := db.Exec(
			"INSERT INTO ticket_comments (attachments) VALUES (?)",
			value,
		).Error; err != nil {
			t.Fatalf("seed comment projection %v: %v", value, err)
		}
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := MigrateAttachmentProjections(db); err != nil {
			t.Fatalf("migration attempt %d: %v", attempt+1, err)
		}
	}

	for _, projection := range legacyAttachmentProjections {
		if db.Migrator().HasColumn(projection.table, projection.column) {
			t.Fatalf("%s.%s still exists", projection.table, projection.column)
		}
	}
	var ticketCount, commentCount int64
	if err := db.Table("tickets").Count(&ticketCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("ticket_comments").Count(&commentCount).Error; err != nil {
		t.Fatal(err)
	}
	if ticketCount != 6 || commentCount != 6 {
		t.Fatalf("business rows changed: tickets=%d comments=%d", ticketCount, commentCount)
	}
}

func TestMigrateAttachmentProjectionsFailsClosedBeforeSchemaChange(t *testing.T) {
	tests := []struct {
		name        string
		table       string
		legacyValue string
	}{
		{name: "ticket reference array", table: "tickets", legacyValue: `["legacy/key.txt"]`},
		{name: "comment reference array", table: "ticket_comments", legacyValue: `["legacy/key.txt"]`},
		{name: "invalid comment JSON", table: "ticket_comments", legacyValue: `not-json`},
		{name: "unexpected ticket object", table: "tickets", legacyValue: `{"id":1}`},
		{name: "JSON string is not empty projection", table: "tickets", legacyValue: `""`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openAttachmentProjectionMigrationDB(t)
			if err := insertLegacyAttachmentProjectionValue(
				db,
				test.table,
				test.legacyValue,
			); err != nil {
				t.Fatalf("seed legacy projection: %v", err)
			}

			err := MigrateAttachmentProjections(db)
			if !errors.Is(err, ErrAttachmentProjectionRequiresImport) {
				t.Fatalf("expected explicit import failure, got %v", err)
			}
			if !strings.Contains(err.Error(), test.table) ||
				!strings.Contains(err.Error(), "ticket_attachments") {
				t.Fatalf("migration error lacks actionable context: %v", err)
			}
			for _, projection := range legacyAttachmentProjections {
				if !db.Migrator().HasColumn(projection.table, projection.column) {
					t.Fatalf(
						"%s.%s was dropped despite validation failure",
						projection.table,
						projection.column,
					)
				}
			}
		})
	}
}

func TestEmptyLegacyAttachmentValue(t *testing.T) {
	tests := []struct {
		name  string
		value any
		empty bool
	}{
		{name: "SQL null", value: nil, empty: true},
		{name: "blank", value: "", empty: true},
		{name: "JSON null", value: "null", empty: true},
		{name: "empty array", value: "[]", empty: true},
		{name: "reference", value: `["1"]`, empty: false},
		{name: "invalid", value: "invalid", empty: false},
		{name: "empty JSON string", value: `""`, empty: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openAttachmentProjectionMigrationDB(t)
			if err := db.Exec(
				"INSERT INTO tickets (attachments) VALUES (?)",
				test.value,
			).Error; err != nil {
				t.Fatal(err)
			}
			rows, err := loadAttachmentProjectionRows(
				db,
				legacyAttachmentProjection{table: "tickets", column: "attachments"},
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || emptyLegacyAttachmentValue(rows[0].Value) != test.empty {
				t.Fatalf("value %v empty=%v, want %v", test.value, emptyLegacyAttachmentValue(rows[0].Value), test.empty)
			}
		})
	}
}

func TestMigrateAttachmentProjectionsPreservesActorConstraints(t *testing.T) {
	db := openActorProjectionMigrationDB(t, "attachment-order")
	for _, statement := range []string{
		`ALTER TABLE tickets ADD COLUMN attachments TEXT`,
		`ALTER TABLE ticket_comments ADD COLUMN attachments TEXT`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	human := seedActorProjectionUser(t, db, "attachment-order")
	ticket := actorProjectionTicket("ATTACHMENT-ORDER", &human.ID)
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.TicketComment{
		TicketID: ticket.ID,
		UserID:   &human.ID,
		Content:  "legacy actor row",
		Type:     models.CommentTypePublic,
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := MigrateActorProjections(db); err != nil {
		t.Fatalf("migrate actors before attachments: %v", err)
	}
	if err := MigrateAttachmentProjections(db); err != nil {
		t.Fatalf("migrate attachment projections: %v", err)
	}

	other := seedActorProjectionUser(t, db, "attachment-other")
	err := db.Exec(
		`INSERT INTO ticket_comments
			(ticket_id, user_id, actor_type, actor_id, content, type)
		 VALUES (?, ?, 'human', ?, 'invalid', 'public')`,
		ticket.ID,
		human.ID,
		fmt.Sprint(other.ID),
	).Error
	if err == nil {
		t.Fatal("attachment column removal disabled the ActorRef constraint")
	}
}

func openAttachmentProjectionMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE tickets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			attachments TEXT
		)`,
		`CREATE TABLE ticket_comments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			attachments TEXT
		)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create attachment projection fixture: %v", err)
		}
	}
	return db
}

func insertLegacyAttachmentProjectionValue(
	db *gorm.DB,
	table string,
	value any,
) error {
	switch table {
	case "tickets":
		return db.Exec(
			"INSERT INTO tickets (attachments) VALUES (?)",
			value,
		).Error
	case "ticket_comments":
		return db.Exec(
			"INSERT INTO ticket_comments (attachments) VALUES (?)",
			value,
		).Error
	default:
		return fmt.Errorf("unsupported attachment projection fixture table %q", table)
	}
}
