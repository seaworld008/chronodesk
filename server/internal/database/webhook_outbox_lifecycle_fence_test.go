package database

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestWebhookOutboxLifecycleFenceBackfillsEveryNonCanonicalToken(
	t *testing.T,
) {
	valid, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	compact := strings.ReplaceAll(valid.String(), "-", "")
	urn := "urn:uuid:" + valid.String()
	upper := strings.ToUpper(valid.String())
	v4 := uuid.NewString()
	reservedVariant := valid.String()[:19] + "0" + valid.String()[20:]

	for _, test := range []struct {
		name  string
		token string
	}{
		{name: "compact", token: compact},
		{name: "URN", token: urn},
		{name: "uppercase", token: upper},
		{name: "UUIDv4", token: v4},
		{name: "reserved variant", token: reservedVariant},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := openWebhookOutboxLifecycleIndexTestDB(
				t,
				"fence-noncanonical-"+strings.ReplaceAll(
					strings.ToLower(test.name),
					" ",
					"-",
				),
			)
			if err := testDBExec(db, `
				CREATE TABLE outbox_deliveries (
					id TEXT PRIMARY KEY,
					status TEXT NOT NULL,
					lock_token TEXT
				)
			`); err != nil {
				t.Fatal(err)
			}
			if err := db.Exec(
				`INSERT INTO outbox_deliveries (id, status, lock_token)
				 VALUES ('processing-row', 'processing', ?)`,
				test.token,
			).Error; err != nil {
				t.Fatal(err)
			}

			if err := MigrateWebhookOutboxLifecycleFence(db); err != nil {
				t.Fatal(err)
			}
			var got string
			if err := db.Raw(
				`SELECT lock_token
				 FROM main.outbox_deliveries
				 WHERE id = 'processing-row'`,
			).Scan(&got).Error; err != nil {
				t.Fatal(err)
			}
			if got == test.token {
				t.Fatalf(
					"non-canonical %s token survived migration: %q",
					test.name,
					got,
				)
			}
			if !webhookOutboxLifecycleTokenIsUUIDv7(got) {
				t.Fatalf("backfilled token is not canonical UUIDv7: %q", got)
			}
		})
	}
}

func TestWebhookOutboxLifecycleFenceMigrationIsIdempotentAndExact(
	t *testing.T,
) {
	db := openWebhookOutboxLifecycleIndexTestDB(t, "fence-exact")
	if err := testDBExec(db, `
		CREATE TABLE outbox_deliveries (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL
		);
		INSERT INTO outbox_deliveries (id, status)
		VALUES ('legacy-processing', 'processing');
	`); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := MigrateWebhookOutboxLifecycleFence(db); err != nil {
			t.Fatalf("fence migration attempt %d: %v", attempt+1, err)
		}
	}
	if err := ValidateWebhookOutboxLifecycleFence(db); err != nil {
		t.Fatal(err)
	}
	var token string
	if err := db.Raw(
		"SELECT lock_token FROM outbox_deliveries WHERE id = ?",
		"legacy-processing",
	).Scan(&token).Error; err != nil {
		t.Fatal(err)
	}
	parsed, err := uuid.Parse(token)
	if err != nil || parsed.Version() != 7 {
		t.Fatalf("legacy processing token = %q, err=%v", token, err)
	}

	assertWebhookOutboxFenceWriteRejected(
		t,
		db,
		"missing-processing-token",
		"processing",
		nil,
	)
	invalidPendingToken := token
	assertWebhookOutboxFenceWriteRejected(
		t,
		db,
		"pending-with-token",
		"pending",
		&invalidPendingToken,
	)
	validToken, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		`INSERT INTO outbox_deliveries (id, status, lock_token)
		 VALUES (?, 'processing', ?)`,
		"valid-processing-token",
		validToken.String(),
	).Error; err != nil {
		t.Fatalf("valid UUIDv7 processing token rejected: %v", err)
	}
	for _, test := range []struct {
		name  string
		value string
	}{
		{
			name:  "compact",
			value: strings.ReplaceAll(validToken.String(), "-", ""),
		},
		{name: "URN", value: "urn:uuid:" + validToken.String()},
		{name: "uppercase", value: strings.ToUpper(validToken.String())},
	} {
		assertWebhookOutboxFenceWriteRejected(
			t,
			db,
			"invalid-processing-"+strings.ToLower(test.name),
			"processing",
			&test.value,
		)
	}
	if err := db.Exec(
		`INSERT INTO outbox_deliveries (id, status, lock_token)
		 VALUES (?, 'pending', NULL)`,
		"valid-pending-null-token",
	).Error; err != nil {
		t.Fatalf("valid pending NULL token rejected: %v", err)
	}
}

func TestWebhookOutboxLifecycleFenceRejectsSQLiteLockTokenDefault(
	t *testing.T,
) {
	db := openWebhookOutboxLifecycleIndexTestDB(
		t,
		"fence-default-drift",
	)
	if err := testDBExec(db, `
		CREATE TABLE outbox_deliveries (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			lock_token TEXT DEFAULT
				'018f3f7e-7b22-7cc0-8000-000000000001'
		)
	`); err != nil {
		t.Fatal(err)
	}
	err := MigrateWebhookOutboxLifecycleFence(db)
	if err == nil ||
		!strings.Contains(err.Error(), "nullable TEXT column") {
		t.Fatalf("SQLite lock_token default drift error = %v", err)
	}
}

func TestWebhookOutboxLifecycleFenceRuntimeGateRejectsTriggerDrift(
	t *testing.T,
) {
	db := openWebhookOutboxLifecycleIndexTestDB(t, "fence-drift")
	if err := testDBExec(db, `
		CREATE TABLE outbox_deliveries (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL
		)
	`); err != nil {
		t.Fatal(err)
	}
	if err := MigrateWebhookOutboxLifecycleFence(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"DROP TRIGGER " + webhookOutboxLifecycleFenceInsertTrigger,
	).Error; err != nil {
		t.Fatal(err)
	}
	err := ValidateWebhookOutboxLifecycleFence(db)
	if err == nil ||
		!strings.Contains(
			err.Error(),
			webhookOutboxLifecycleFenceInsertTrigger,
		) {
		t.Fatalf("runtime fence drift error = %v", err)
	}
	if err := MigrateWebhookOutboxLifecycleFence(db); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWebhookOutboxLifecycleFence(db); err != nil {
		t.Fatal(err)
	}

	definition := sqliteWebhookOutboxLifecycleFenceTriggers()[0]
	drifted := strings.ReplaceAll(
		definition.sql,
		"[0-9a-f]",
		"[0-9A-F]",
	)
	if drifted == definition.sql {
		t.Fatal("trigger drift mutation did not change SQL")
	}
	if err := db.Exec(
		"DROP TRIGGER " + webhookOutboxLifecycleFenceInsertTrigger,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(drifted).Error; err != nil {
		t.Fatal(err)
	}
	err = ValidateWebhookOutboxLifecycleFence(db)
	if err == nil ||
		!strings.Contains(
			err.Error(),
			webhookOutboxLifecycleFenceInsertTrigger,
		) {
		t.Fatalf("case-sensitive trigger drift error = %v", err)
	}
}

func TestWebhookOutboxLifecycleFenceRejectsSQLiteTempShadow(t *testing.T) {
	db := openWebhookOutboxLifecycleIndexTestDB(t, "fence-temp-shadow")
	if err := testDBExec(db, `
		CREATE TABLE outbox_deliveries (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL
		)
	`); err != nil {
		t.Fatal(err)
	}
	if err := MigrateWebhookOutboxLifecycleFence(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		CREATE TEMP TABLE outbox_deliveries (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			lock_token TEXT
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := ValidateWebhookOutboxLifecycleFence(db); err == nil ||
		!strings.Contains(err.Error(), "TEMP schema shadow") {
		t.Fatalf("runtime fence TEMP-shadow error = %v", err)
	}
	if err := MigrateWebhookOutboxLifecycleFence(db); err == nil ||
		!strings.Contains(err.Error(), "TEMP schema shadow") {
		t.Fatalf("fence migration TEMP-shadow error = %v", err)
	}
}

func assertWebhookOutboxFenceWriteRejected(
	t *testing.T,
	db *gorm.DB,
	id string,
	status string,
	token *string,
) {
	t.Helper()
	if err := db.Exec(
		`INSERT INTO outbox_deliveries (id, status, lock_token)
		 VALUES (?, ?, ?)`,
		id,
		status,
		token,
	).Error; err == nil {
		t.Fatalf(
			"invalid lifecycle fence write succeeded: id=%s status=%s",
			id,
			status,
		)
	}
}
