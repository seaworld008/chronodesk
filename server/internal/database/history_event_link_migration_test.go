package database

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMigrateTicketHistoryEventLinksBackfillsOnlyProvableMatches(t *testing.T) {
	db := openHistoryEventMigrationDB(t, "provable")
	createLegacyTicketHistoriesTable(t, db, false)

	now := time.Now().UTC()
	events := []models.DomainEvent{
		historyMigrationEvent(
			"event-create-1",
			eventcontract.TicketCreatedEventType,
			"ticket/1",
			1,
			`{"ticket_id":1}`,
			now.Add(24*time.Hour),
		),
		historyMigrationEvent(
			"event-update-2-a",
			eventcontract.TicketUpdatedEventType,
			"ticket/2",
			2,
			`{"changed_fields":["title"]}`,
			now,
		),
		historyMigrationEvent(
			"event-update-2-b",
			eventcontract.TicketUpdatedEventType,
			"ticket/2",
			3,
			`{"changed_fields":["title"]}`,
			now.Add(time.Nanosecond),
		),
		historyMigrationEvent(
			"event-comment-3-77",
			eventcontract.TicketCommentCreatedEventType,
			"ticket/3",
			4,
			`{"comment_id":77}`,
			now,
		),
		historyMigrationEvent(
			"event-comment-3-78",
			eventcontract.TicketCommentCreatedEventType,
			"ticket/3",
			5,
			`{"comment_id":78}`,
			now,
		),
	}
	if err := db.Create(&events).Error; err != nil {
		t.Fatalf("seed domain events: %v", err)
	}

	legacyRows := []struct {
		ticketID    uint
		action      models.HistoryAction
		fieldName   string
		commentID   any
		description string
	}{
		{ticketID: 1, action: models.HistoryActionCreate, description: "legacy create"},
		{ticketID: 2, action: models.HistoryActionUpdate, fieldName: "title", description: "ambiguous update"},
		{ticketID: 3, action: models.HistoryActionComment, commentID: 77, description: "provable comment"},
		{ticketID: 4, action: models.HistoryActionCreate, description: "no event"},
	}
	for _, row := range legacyRows {
		if err := db.Exec(
			`INSERT INTO ticket_histories
				(created_at, updated_at, ticket_id, user_id, actor_type, actor_id,
				 action, description, field_name, comment_id)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			now,
			now,
			row.ticketID,
			7,
			models.ActorTypeHuman,
			"7",
			row.action,
			row.description,
			row.fieldName,
			row.commentID,
		).Error; err != nil {
			t.Fatalf("seed history ticket %d: %v", row.ticketID, err)
		}
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := MigrateTicketHistoryEventLinks(db); err != nil {
			t.Fatalf("migration attempt %d: %v", attempt+1, err)
		}
	}

	assertMigratedHistoryLink(t, db, 1, "event-create-1", 1, models.TicketHistoryProvenanceDomainEvent)
	assertMigratedHistoryLink(t, db, 2, "", 0, models.TicketHistoryProvenancePreEvent)
	assertMigratedHistoryLink(t, db, 3, "event-comment-3-77", 4, models.TicketHistoryProvenanceDomainEvent)
	assertMigratedHistoryLink(t, db, 4, "", 0, models.TicketHistoryProvenancePreEvent)

	if !db.Migrator().HasIndex(&models.TicketHistory{}, "idx_ticket_histories_event_id") {
		t.Fatal("ticket history event index was not created")
	}

	if err := db.Exec(
		`INSERT INTO ticket_histories
			(ticket_id, actor_type, actor_id, action, description, event_id, resource_version, provenance)
		 VALUES (5, 'human', '7', 'create', 'invalid', NULL, 1, 'domain_event')`,
	).Error; err == nil || !strings.Contains(err.Error(), "invalid ticket history domain event link") {
		t.Fatalf("invalid linked history was accepted: %v", err)
	}
	if err := db.Delete(&models.DomainEvent{}, "id = ?", "event-create-1").Error; err == nil {
		t.Fatal("referenced domain event deletion must be rejected")
	}

	if err := db.Model(&models.TicketHistory{}).
		Where("ticket_id = ?", 4).
		Update("provenance", models.TicketHistoryProvenanceImported).Error; err != nil {
		t.Fatalf("mark imported history: %v", err)
	}
	if err := MigrateTicketHistoryEventLinks(db); err != nil {
		t.Fatalf("rerun after imported provenance: %v", err)
	}
	assertMigratedHistoryLink(t, db, 4, "", 0, models.TicketHistoryProvenanceImported)
}

func TestMigrateTicketHistoryEventLinksRejectsDanglingExistingIdentity(t *testing.T) {
	db := openHistoryEventMigrationDB(t, "dangling")
	createLegacyTicketHistoriesTable(t, db, true)
	if err := db.Exec(
		`INSERT INTO ticket_histories
			(ticket_id, actor_type, actor_id, action, description, event_id, resource_version, provenance)
		 VALUES (9, 'human', '9', 'create', 'dangling', 'missing-event', 1, 'domain_event')`,
	).Error; err != nil {
		t.Fatal(err)
	}

	err := MigrateTicketHistoryEventLinks(db)
	if err == nil || !strings.Contains(err.Error(), "missing or cross-ticket event") {
		t.Fatalf("migration error = %v, want dangling identity rejection", err)
	}
}

func TestMigrateTicketHistoryEventLinksRequiresDomainEvents(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:history-event-missing-events?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	createLegacyTicketHistoriesTable(t, db, false)
	err = MigrateTicketHistoryEventLinks(db)
	if err == nil || !strings.Contains(err.Error(), "domain_events table is required") {
		t.Fatalf("migration error = %v, want missing event table rejection", err)
	}
}

func openHistoryEventMigrationDB(t *testing.T, suffix string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf("file:history-event-migration-%s?mode=memory&cache=shared", suffix)),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.DomainEvent{}); err != nil {
		t.Fatalf("migrate domain events: %v", err)
	}
	return db
}

func createLegacyTicketHistoriesTable(t *testing.T, db *gorm.DB, withLinkColumns bool) {
	t.Helper()
	linkColumns := ""
	if withLinkColumns {
		linkColumns = `,
			event_id TEXT,
			resource_version INTEGER NOT NULL DEFAULT 0,
			provenance TEXT NOT NULL DEFAULT 'pre_event'`
	}
	statement := `CREATE TABLE ticket_histories (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at DATETIME,
		updated_at DATETIME,
		ticket_id INTEGER NOT NULL,
		user_id INTEGER,
		actor_type TEXT NOT NULL DEFAULT 'human',
		actor_id TEXT,
		action TEXT NOT NULL,
		description TEXT NOT NULL,
		details TEXT,
		field_name TEXT,
		old_value TEXT,
		new_value TEXT,
		metadata TEXT,
		comment_id INTEGER,
		attachment_id INTEGER` + linkColumns + `
	)`
	if err := db.Exec(statement).Error; err != nil {
		t.Fatalf("create legacy ticket histories table: %v", err)
	}
}

func historyMigrationEvent(
	id string,
	eventType string,
	subject string,
	version uint64,
	data string,
	at time.Time,
) models.DomainEvent {
	return models.DomainEvent{
		ID:              id,
		SpecVersion:     "1.0",
		Source:          "urn:chronodesk:test",
		Type:            eventType,
		Subject:         subject,
		Time:            at,
		DataContentType: "application/json",
		DataSchema:      "urn:chronodesk:test:event",
		Data:            datatypes.JSON(data),
		ActorType:       models.ActorTypeHuman,
		ActorID:         "7",
		ResourceVersion: version,
	}
}

func assertMigratedHistoryLink(
	t *testing.T,
	db *gorm.DB,
	ticketID uint,
	eventID string,
	version uint64,
	provenance models.TicketHistoryProvenance,
) {
	t.Helper()
	var history models.TicketHistory
	if err := db.Where("ticket_id = ?", ticketID).First(&history).Error; err != nil {
		t.Fatalf("load migrated history %d: %v", ticketID, err)
	}
	if eventID == "" {
		if history.EventID != nil {
			t.Fatalf("history %d event_id = %q, want nil", ticketID, *history.EventID)
		}
	} else if history.EventID == nil || *history.EventID != eventID {
		t.Fatalf("history %d event_id = %v, want %q", ticketID, history.EventID, eventID)
	}
	if history.ResourceVersion != version || history.Provenance != provenance {
		t.Fatalf(
			"history %d link = version:%d provenance:%q, want version:%d provenance:%q",
			ticketID,
			history.ResourceVersion,
			history.Provenance,
			version,
			provenance,
		)
	}
}
