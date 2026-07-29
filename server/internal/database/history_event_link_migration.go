package database

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const historyEventLinkMigrationBatchSize = 200

// MigrateTicketHistoryEventLinks adds the durable TicketHistory-to-DomainEvent
// link and conservatively backfills historical rows. A row is linked only when
// exactly one semantically compatible event exists. Ambiguous or missing
// evidence remains explicitly marked as pre_event; imported rows are never
// guessed.
//
// The migration is safe to rerun and deliberately does not use timestamps or
// nearest-event heuristics.
func MigrateTicketHistoryEventLinks(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if !db.Migrator().HasTable(&models.TicketHistory{}) {
		return nil
	}
	if !db.Migrator().HasTable(&models.DomainEvent{}) {
		return errors.New("domain_events table is required before migrating ticket history links")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if err := ensureTicketHistoryEventLinkColumns(tx); err != nil {
			return err
		}
		if err := normalizeTicketHistoryProvenance(tx); err != nil {
			return err
		}
		if err := backfillTicketHistoryEventLinks(tx); err != nil {
			return err
		}
		if err := validateTicketHistoryEventLinks(tx); err != nil {
			return err
		}
		if err := installTicketHistoryEventLinkConstraints(tx); err != nil {
			return err
		}
		return nil
	})
}

func ensureTicketHistoryEventLinkColumns(tx *gorm.DB) error {
	fields := []string{"EventID", "ResourceVersion", "Provenance"}
	for _, field := range fields {
		if tx.Migrator().HasColumn(&models.TicketHistory{}, field) {
			continue
		}
		if err := tx.Migrator().AddColumn(&models.TicketHistory{}, field); err != nil {
			return fmt.Errorf("add ticket history %s column: %w", field, err)
		}
	}
	if err := tx.Exec(
		"CREATE INDEX IF NOT EXISTS idx_ticket_histories_event_id ON ticket_histories (event_id)",
	).Error; err != nil {
		return fmt.Errorf("create ticket history event index: %w", err)
	}
	return nil
}

func normalizeTicketHistoryProvenance(tx *gorm.DB) error {
	if err := tx.Model(&models.TicketHistory{}).
		Where("(provenance IS NULL OR TRIM(provenance) = '') AND event_id IS NOT NULL").
		Update("provenance", models.TicketHistoryProvenanceDomainEvent).Error; err != nil {
		return fmt.Errorf("normalize linked ticket history provenance: %w", err)
	}
	if err := tx.Model(&models.TicketHistory{}).
		Where("(provenance IS NULL OR TRIM(provenance) = '') AND event_id IS NULL").
		Update("provenance", models.TicketHistoryProvenancePreEvent).Error; err != nil {
		return fmt.Errorf("normalize ticket history provenance: %w", err)
	}

	var unsupported []string
	if err := tx.Model(&models.TicketHistory{}).
		Distinct("provenance").
		Where("provenance NOT IN ?", []models.TicketHistoryProvenance{
			models.TicketHistoryProvenanceDomainEvent,
			models.TicketHistoryProvenancePreEvent,
			models.TicketHistoryProvenanceImported,
		}).
		Pluck("provenance", &unsupported).Error; err != nil {
		return fmt.Errorf("list ticket history provenance values: %w", err)
	}
	if len(unsupported) > 0 {
		return fmt.Errorf("ticket_histories contains unsupported provenance values: %q", unsupported)
	}
	return nil
}

func backfillTicketHistoryEventLinks(tx *gorm.DB) error {
	var lastID uint
	for {
		var histories []models.TicketHistory
		if err := tx.
			Where("id > ?", lastID).
			Order("id ASC").
			Limit(historyEventLinkMigrationBatchSize).
			Find(&histories).Error; err != nil {
			return fmt.Errorf("load ticket history migration batch: %w", err)
		}
		if len(histories) == 0 {
			return nil
		}

		subjects := make([]string, 0, len(histories))
		seenSubjects := make(map[string]struct{}, len(histories))
		for i := range histories {
			subject := fmt.Sprintf("ticket/%d", histories[i].TicketID)
			if _, exists := seenSubjects[subject]; exists {
				continue
			}
			seenSubjects[subject] = struct{}{}
			subjects = append(subjects, subject)
		}
		var events []models.DomainEvent
		if err := tx.Where("subject IN ?", subjects).Find(&events).Error; err != nil {
			return fmt.Errorf("load domain events for ticket history migration: %w", err)
		}
		eventsBySubject := make(map[string][]models.DomainEvent, len(subjects))
		for i := range events {
			eventsBySubject[events[i].Subject] = append(eventsBySubject[events[i].Subject], events[i])
		}

		for i := range histories {
			history := &histories[i]
			subjectEvents := eventsBySubject[fmt.Sprintf("ticket/%d", history.TicketID)]
			if err := backfillTicketHistoryEventLink(tx, history, subjectEvents); err != nil {
				return err
			}
		}
		lastID = histories[len(histories)-1].ID
	}
}

func backfillTicketHistoryEventLink(
	tx *gorm.DB,
	history *models.TicketHistory,
	events []models.DomainEvent,
) error {
	if history.Provenance == models.TicketHistoryProvenanceImported {
		if history.EventID != nil || history.ResourceVersion != 0 {
			return fmt.Errorf("imported ticket history %d carries an event link", history.ID)
		}
		return nil
	}

	if history.EventID != nil {
		for i := range events {
			event := &events[i]
			if event.ID != *history.EventID {
				continue
			}
			if history.ResourceVersion != 0 && history.ResourceVersion != event.ResourceVersion {
				return fmt.Errorf(
					"ticket history %d resource version does not match event %q",
					history.ID,
					event.ID,
				)
			}
			actor := history.Actor()
			if event.ActorType != actor.Type || event.ActorID != actor.ID {
				return fmt.Errorf(
					"ticket history %d actor does not match event %q",
					history.ID,
					event.ID,
				)
			}
			return updateTicketHistoryEventLink(tx, history.ID, event)
		}
		return fmt.Errorf(
			"ticket history %d references missing or cross-ticket event %q",
			history.ID,
			*history.EventID,
		)
	}
	if history.ResourceVersion != 0 {
		return fmt.Errorf("unlinked ticket history %d carries resource version %d", history.ID, history.ResourceVersion)
	}

	candidates := make([]*models.DomainEvent, 0, 1)
	for i := range events {
		event := &events[i]
		if ticketHistoryEventCandidate(history, event) {
			candidates = append(candidates, event)
		}
	}
	if len(candidates) != 1 {
		return tx.Model(&models.TicketHistory{}).
			Where("id = ? AND event_id IS NULL", history.ID).
			Updates(map[string]any{
				"resource_version": 0,
				"provenance":       models.TicketHistoryProvenancePreEvent,
			}).Error
	}
	return updateTicketHistoryEventLink(tx, history.ID, candidates[0])
}

func updateTicketHistoryEventLink(
	tx *gorm.DB,
	historyID uint,
	event *models.DomainEvent,
) error {
	if event == nil || event.ID == "" || event.ResourceVersion == 0 {
		return fmt.Errorf("ticket history %d candidate event is incomplete", historyID)
	}
	result := tx.Model(&models.TicketHistory{}).
		Where("id = ?", historyID).
		Updates(map[string]any{
			"event_id":         event.ID,
			"resource_version": event.ResourceVersion,
			"provenance":       models.TicketHistoryProvenanceDomainEvent,
		})
	if result.Error != nil {
		return fmt.Errorf("link ticket history %d to event %q: %w", historyID, event.ID, result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("ticket history %d disappeared during event-link migration", historyID)
	}
	return nil
}

func ticketHistoryEventCandidate(
	history *models.TicketHistory,
	event *models.DomainEvent,
) bool {
	if event == nil ||
		event.ID == "" ||
		event.ResourceVersion == 0 ||
		event.Subject != fmt.Sprintf("ticket/%d", history.TicketID) ||
		!ticketHistoryEventTypeMatches(history.Action, event.Type) {
		return false
	}
	actor := history.Actor()
	if event.ActorType != actor.Type || event.ActorID != actor.ID {
		return false
	}

	data := decodeDomainEventData(event.Data)
	switch history.Action {
	case models.HistoryActionComment:
		return history.CommentID != nil &&
			eventDataUint(data["comment_id"]) == uint64(*history.CommentID)
	case models.HistoryActionAttachment:
		return history.AttachmentID != nil &&
			eventDataUint(data["attachment_id"]) == uint64(*history.AttachmentID)
	case models.HistoryActionStatusChange,
		models.HistoryActionClose,
		models.HistoryActionReopen,
		models.HistoryActionResolve:
		return eventDataText(data["old_status"]) == history.OldValue &&
			eventDataText(data["new_status"]) == history.NewValue
	case models.HistoryActionAssign, models.HistoryActionTransfer:
		if history.NewValue == "" {
			return false
		}
		expected, err := strconv.ParseUint(history.NewValue, 10, 64)
		return err == nil && eventDataUint(data["assigned_to_id"]) == expected
	case models.HistoryActionUnassign:
		return eventChangedField(data, history.FieldName)
	case models.HistoryActionUpdate, models.HistoryActionPriorityChange:
		return history.FieldName != "" && eventChangedField(data, history.FieldName)
	default:
		return true
	}
}

func ticketHistoryEventTypeMatches(action models.HistoryAction, eventType string) bool {
	switch action {
	case models.HistoryActionCreate:
		return eventType == eventcontract.TicketCreatedEventType
	case models.HistoryActionComment:
		return eventType == eventcontract.TicketCommentCreatedEventType
	case models.HistoryActionAttachment:
		return eventType == eventcontract.TicketAttachmentCreatedEventType
	case models.HistoryActionAssign, models.HistoryActionUnassign, models.HistoryActionTransfer:
		return eventType == eventcontract.TicketAssignedEventType ||
			eventType == eventcontract.TicketUpdatedEventType
	case models.HistoryActionStatusChange,
		models.HistoryActionClose,
		models.HistoryActionReopen,
		models.HistoryActionResolve:
		return eventType == eventcontract.TicketTransitionedEventType ||
			eventType == eventcontract.TicketUpdatedEventType
	case models.HistoryActionEscalate:
		return eventType == eventcontract.TicketEscalatedEventType
	default:
		return eventType == eventcontract.TicketUpdatedEventType
	}
}

func decodeDomainEventData(data datatypes.JSON) map[string]any {
	var decoded map[string]any
	if len(data) == 0 || json.Unmarshal(data, &decoded) != nil {
		return nil
	}
	return decoded
}

func eventChangedField(data map[string]any, field string) bool {
	if field == "" {
		return false
	}
	switch values := data["changed_fields"].(type) {
	case []any:
		for _, value := range values {
			if eventDataText(value) == field {
				return true
			}
		}
	case []string:
		for _, value := range values {
			if value == field {
				return true
			}
		}
	}
	if changes, ok := data["changes"].(map[string]any); ok {
		_, exists := changes[field]
		return exists
	}
	return false
}

func eventDataText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		if value == nil {
			return ""
		}
		return fmt.Sprint(value)
	}
}

func eventDataUint(value any) uint64 {
	switch typed := value.(type) {
	case float64:
		if typed < 0 || typed >= math.Exp2(64) || math.Trunc(typed) != typed {
			return 0
		}
		return uint64(typed)
	case json.Number:
		parsed, _ := strconv.ParseUint(typed.String(), 10, 64)
		return parsed
	case string:
		parsed, _ := strconv.ParseUint(typed, 10, 64)
		return parsed
	default:
		text := fmt.Sprint(value)
		parsed, _ := strconv.ParseUint(text, 10, 64)
		return parsed
	}
}

func validateTicketHistoryEventLinks(tx *gorm.DB) error {
	var invalidCount int64
	if err := tx.Table("ticket_histories AS h").
		Joins("LEFT JOIN domain_events AS e ON e.id = h.event_id").
		Where(`
			(h.provenance = ? AND (
				h.event_id IS NULL OR h.resource_version = 0 OR e.id IS NULL OR
				e.resource_version <> h.resource_version OR
				COALESCE(e.subject, '') <> ('ticket/' || CAST(h.ticket_id AS TEXT))
			)) OR
			(h.provenance IN ? AND (h.event_id IS NOT NULL OR h.resource_version <> 0))
		`,
			models.TicketHistoryProvenanceDomainEvent,
			[]models.TicketHistoryProvenance{
				models.TicketHistoryProvenancePreEvent,
				models.TicketHistoryProvenanceImported,
			},
		).
		Count(&invalidCount).Error; err != nil {
		return fmt.Errorf("validate ticket history event links: %w", err)
	}
	if invalidCount != 0 {
		return fmt.Errorf("ticket_histories contains %d invalid event link(s)", invalidCount)
	}
	return nil
}

func installTicketHistoryEventLinkConstraints(tx *gorm.DB) error {
	switch tx.Dialector.Name() {
	case "postgres":
		return installPostgresTicketHistoryEventLinkConstraints(tx)
	case "sqlite":
		return installSQLiteTicketHistoryEventLinkConstraints(tx)
	default:
		return fmt.Errorf(
			"ticket history event-link constraints are unsupported for database dialect %q",
			tx.Dialector.Name(),
		)
	}
}

func installPostgresTicketHistoryEventLinkConstraints(tx *gorm.DB) error {
	statements := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_domain_events_id_resource_version
			ON domain_events (id, resource_version)`,
		`DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_constraint
				WHERE conname = 'fk_ticket_histories_domain_event_version'
				  AND conrelid = 'ticket_histories'::regclass
			) THEN
				ALTER TABLE ticket_histories
				ADD CONSTRAINT fk_ticket_histories_domain_event_version
				FOREIGN KEY (event_id, resource_version)
				REFERENCES domain_events (id, resource_version)
				ON UPDATE CASCADE ON DELETE RESTRICT;
			END IF;
		END $$`,
		`DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_constraint
				WHERE conname = 'chk_ticket_histories_event_provenance'
				  AND conrelid = 'ticket_histories'::regclass
			) THEN
				ALTER TABLE ticket_histories
				ADD CONSTRAINT chk_ticket_histories_event_provenance
				CHECK (
					(provenance = 'domain_event' AND event_id IS NOT NULL AND resource_version > 0)
					OR
					(provenance IN ('pre_event', 'imported') AND event_id IS NULL AND resource_version = 0)
				);
			END IF;
		END $$`,
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("install PostgreSQL ticket history event-link constraint: %w", err)
		}
	}
	return nil
}

func installSQLiteTicketHistoryEventLinkConstraints(tx *gorm.DB) error {
	statements := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_domain_events_id_resource_version
			ON domain_events (id, resource_version)`,
		`CREATE TRIGGER IF NOT EXISTS trg_ticket_histories_event_link_insert
		BEFORE INSERT ON ticket_histories
		BEGIN
			SELECT CASE
				WHEN NEW.provenance = 'domain_event' AND (
					NEW.event_id IS NULL OR NEW.resource_version = 0 OR
					NOT EXISTS (
						SELECT 1 FROM domain_events
						WHERE id = NEW.event_id
						  AND resource_version = NEW.resource_version
						  AND subject = ('ticket/' || NEW.ticket_id)
					)
				)
				THEN RAISE(ABORT, 'invalid ticket history domain event link')
			END;
			SELECT CASE
				WHEN NEW.provenance IN ('pre_event', 'imported') AND
					(NEW.event_id IS NOT NULL OR NEW.resource_version <> 0)
				THEN RAISE(ABORT, 'unlinked ticket history carries event identity')
			END;
			SELECT CASE
				WHEN NEW.provenance NOT IN ('domain_event', 'pre_event', 'imported')
				THEN RAISE(ABORT, 'invalid ticket history provenance')
			END;
		END`,
		`CREATE TRIGGER IF NOT EXISTS trg_ticket_histories_event_link_update
		BEFORE UPDATE OF ticket_id, event_id, resource_version, provenance ON ticket_histories
		BEGIN
			SELECT CASE
				WHEN NEW.provenance = 'domain_event' AND (
					NEW.event_id IS NULL OR NEW.resource_version = 0 OR
					NOT EXISTS (
						SELECT 1 FROM domain_events
						WHERE id = NEW.event_id
						  AND resource_version = NEW.resource_version
						  AND subject = ('ticket/' || NEW.ticket_id)
					)
				)
				THEN RAISE(ABORT, 'invalid ticket history domain event link')
			END;
			SELECT CASE
				WHEN NEW.provenance IN ('pre_event', 'imported') AND
					(NEW.event_id IS NOT NULL OR NEW.resource_version <> 0)
				THEN RAISE(ABORT, 'unlinked ticket history carries event identity')
			END;
			SELECT CASE
				WHEN NEW.provenance NOT IN ('domain_event', 'pre_event', 'imported')
				THEN RAISE(ABORT, 'invalid ticket history provenance')
			END;
		END`,
		`CREATE TRIGGER IF NOT EXISTS trg_domain_events_restrict_history_delete
		BEFORE DELETE ON domain_events
		WHEN EXISTS (
			SELECT 1 FROM ticket_histories
			WHERE event_id = OLD.id AND resource_version = OLD.resource_version
		)
		BEGIN
			SELECT RAISE(ABORT, 'domain event is referenced by ticket history');
		END`,
		`CREATE TRIGGER IF NOT EXISTS trg_domain_events_restrict_history_update
		BEFORE UPDATE OF id, resource_version, subject ON domain_events
		WHEN EXISTS (
			SELECT 1 FROM ticket_histories
			WHERE event_id = OLD.id AND resource_version = OLD.resource_version
		)
		BEGIN
			SELECT RAISE(ABORT, 'domain event identity is referenced by ticket history');
		END`,
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("install SQLite ticket history event-link constraint: %w", err)
		}
	}
	return nil
}
