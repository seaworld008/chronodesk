package database

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestWebhookDispatchStartBoundaryMigrationIsExactAndIdempotent(
	t *testing.T,
) {
	db := openWebhookOutboxLifecycleIndexTestDB(t, "dispatch-start-exact")
	if err := testDBExec(db, `
		CREATE TABLE outbox_deliveries (
			id TEXT PRIMARY KEY,
			destination_type TEXT NOT NULL,
			status TEXT NOT NULL,
			attempts INTEGER NOT NULL DEFAULT 0,
			locked_at DATETIME,
			locked_by TEXT NOT NULL DEFAULT '',
			lock_token TEXT
		);
		INSERT INTO outbox_deliveries (id, destination_type, status)
		VALUES
			('legacy-pending', 'webhook', 'pending'),
			('legacy-processing', 'webhook', 'processing');
	`); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := MigrateWebhookDispatchStartBoundary(db); err != nil {
			t.Fatalf("migration attempt %d: %v", attempt+1, err)
		}
	}
	if err := ValidateWebhookDispatchStartBoundary(db); err != nil {
		t.Fatal(err)
	}
	var legacyMarkers []struct {
		ID     string       `gorm:"column:id"`
		Marker sql.NullTime `gorm:"column:dispatch_started_at"`
	}
	if err := db.Raw(
		`SELECT id, dispatch_started_at
		 FROM outbox_deliveries
		 WHERE id IN ('legacy-pending', 'legacy-processing')
		 ORDER BY id`,
	).Scan(&legacyMarkers).Error; err != nil {
		t.Fatal(err)
	}
	if len(legacyMarkers) != 2 {
		t.Fatalf("legacy dispatch rows = %d, want 2", len(legacyMarkers))
	}
	for _, legacy := range legacyMarkers {
		if legacy.Marker.Valid {
			t.Fatalf(
				"legacy row %s gained dispatch marker: %v",
				legacy.ID,
				legacy.Marker,
			)
		}
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, allowed := range []struct {
		id     string
		status string
		lock   any
	}{
		{id: "processing-started", status: "processing", lock: now},
		{id: "succeeded-started", status: "succeeded"},
		{id: "expired-started", status: "expired"},
	} {
		if err := db.Exec(
			`INSERT INTO outbox_deliveries
				(id, destination_type, status, locked_at, dispatch_started_at)
			 VALUES (?, 'webhook', ?, ?, ?)`,
			allowed.id,
			allowed.status,
			allowed.lock,
			now,
		).Error; err != nil {
			t.Fatalf("allowed dispatch state %+v rejected: %v", allowed, err)
		}
	}
	for _, invalid := range []struct {
		id          string
		destination string
		status      string
		lock        any
	}{
		{
			id:          "pending-started",
			destination: "webhook",
			status:      "pending",
		},
		{
			id:          "failed-started",
			destination: "webhook",
			status:      "failed",
		},
		{
			id:          "non-webhook-started",
			destination: "email",
			status:      "processing",
			lock:        now,
		},
		{
			id:          "processing-without-lock",
			destination: "webhook",
			status:      "processing",
		},
	} {
		if err := db.Exec(
			`INSERT INTO outbox_deliveries
				(id, destination_type, status, locked_at, dispatch_started_at)
			 VALUES (?, ?, ?, ?, ?)`,
			invalid.id,
			invalid.destination,
			invalid.status,
			invalid.lock,
			now,
		).Error; err == nil {
			t.Fatalf("invalid dispatch state succeeded: %+v", invalid)
		}
	}

	nextGeneration := now.Add(2 * time.Minute)
	if err := db.Exec(
		`INSERT INTO outbox_deliveries
			(id, destination_type, status, attempts, locked_at, locked_by,
			 lock_token, dispatch_started_at)
		 VALUES
			('legacy-null', 'webhook', 'processing', 1, ?, 'legacy', 'l1', NULL),
			('prepared-old-reclaim', 'webhook', 'processing', 1, ?, 'new', 'n1', ?),
			('real-old-reclaim', 'webhook', 'processing', 1, ?, 'new', 'n2', ?),
			('prepared-new-reclaim', 'webhook', 'processing', 1, ?, 'new', 'n3', ?)`,
		now,
		now,
		now,
		now,
		now.Add(time.Microsecond),
		now,
		now,
	).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{
		"legacy-null",
		"prepared-old-reclaim",
		"real-old-reclaim",
	} {
		if err := db.Exec(
			`UPDATE outbox_deliveries
			 SET attempts = attempts + 1,
			     locked_at = ?,
			     locked_by = 'old-reclaim',
			     lock_token = 'old-token'
			 WHERE id = ?`,
			nextGeneration,
			id,
		).Error; err == nil {
			t.Fatalf("old-field-only reclaim %s bypassed generation fence", id)
		}
	}
	if err := db.Exec(
		`UPDATE outbox_deliveries
		 SET attempts = attempts + 1,
		     locked_at = ?,
		     locked_by = 'new-reclaim',
		     lock_token = '00000000-0000-7000-8000-000000000010',
		     dispatch_started_at = ?
		 WHERE id = 'prepared-new-reclaim'`,
		nextGeneration,
		nextGeneration,
	).Error; err != nil {
		t.Fatalf("generation-bound new reclaim was rejected: %v", err)
	}
}

func TestWebhookDispatchStartRuntimeGateRejectsTriggerDrift(t *testing.T) {
	db := openWebhookOutboxLifecycleIndexTestDB(t, "dispatch-start-drift")
	if err := testDBExec(db, `
		CREATE TABLE outbox_deliveries (
			id TEXT PRIMARY KEY,
			destination_type TEXT NOT NULL,
			status TEXT NOT NULL,
			attempts INTEGER NOT NULL DEFAULT 0,
			locked_at DATETIME,
			locked_by TEXT NOT NULL DEFAULT '',
			lock_token TEXT
		)
	`); err != nil {
		t.Fatal(err)
	}
	if err := MigrateWebhookDispatchStartBoundary(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"DROP TRIGGER " + webhookDispatchStartInsertTrigger,
	).Error; err != nil {
		t.Fatal(err)
	}
	err := ValidateWebhookDispatchStartBoundary(db)
	if err == nil ||
		!strings.Contains(err.Error(), webhookDispatchStartInsertTrigger) {
		t.Fatalf("runtime dispatch-start drift error = %v", err)
	}
}
