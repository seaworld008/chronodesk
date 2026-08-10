package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSQLiteRebuildReservedObjectCollisionsFailBeforeMutation(
	t *testing.T,
) {
	tests := []struct {
		name   string
		schema string
		create string
	}{
		{
			name:   "main table",
			schema: "main",
			create: "CREATE TABLE %s (sentinel TEXT NOT NULL)",
		},
		{
			name:   "main view",
			schema: "main",
			create: "CREATE VIEW %s AS SELECT 'sentinel' AS sentinel",
		},
		{
			name:   "main virtual table",
			schema: "main",
			create: "CREATE VIRTUAL TABLE %s USING rtree(id, min_x, max_x)",
		},
		{
			name:   "temp table",
			schema: "temp",
			create: "CREATE TEMP TABLE %s (sentinel TEXT NOT NULL)",
		},
		{
			name:   "temp view",
			schema: "temp",
			create: "CREATE TEMP VIEW %s AS SELECT 'sentinel' AS sentinel",
		},
		{
			name:   "temp virtual table",
			schema: "temp",
			create: "CREATE VIRTUAL TABLE temp.%s " +
				"USING rtree(id, min_x, max_x)",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openSQLiteReview4RebuildProbe(t)
			const tempName = "source__reserved_rebuild"
			if err := db.Exec(fmt.Sprintf(
				test.create,
				quoteAutomationWebhookSQLiteIdentifier(tempName),
			)).Error; err != nil {
				t.Fatalf("create %s collision: %v", test.name, err)
			}
			beforeCollision := readSQLiteReview4SchemaObject(
				t,
				db,
				test.schema,
				tempName,
			)
			beforeSource := readSQLiteReview4SchemaObject(
				t,
				db,
				"main",
				"source",
			)
			err := rebuildSQLiteTableFromDDL(
				db,
				"source",
				tempName,
				"CREATE TABLE "+
					quoteAutomationWebhookSQLiteIdentifier(tempName)+
					" (id INTEGER NOT NULL)",
				"Review-4 reserved collision",
			)
			if err == nil {
				t.Fatalf("%s collision was destructively accepted", test.name)
			}
			afterCollision := readSQLiteReview4SchemaObject(
				t,
				db,
				test.schema,
				tempName,
			)
			if afterCollision != beforeCollision {
				t.Fatalf(
					"%s collision changed:\nbefore=%q\nafter=%q",
					test.name,
					beforeCollision,
					afterCollision,
				)
			}
			afterSource := readSQLiteReview4SchemaObject(
				t,
				db,
				"main",
				"source",
			)
			if afterSource != beforeSource {
				t.Fatalf("%s collision changed source DDL", test.name)
			}
			var sourceValue string
			if err := db.Raw(
				"SELECT value FROM source WHERE id = 1",
			).Scan(&sourceValue).Error; err != nil {
				t.Fatal(err)
			}
			if sourceValue != "source-sentinel" {
				t.Fatalf("source sentinel = %q", sourceValue)
			}
			var externalTriggerCount int64
			if err := db.Raw(`
				SELECT COUNT(*)
				FROM sqlite_master
				WHERE type = 'trigger'
				  AND name = 'trg_external_references_source'
			`).Scan(&externalTriggerCount).Error; err != nil {
				t.Fatal(err)
			}
			if externalTriggerCount != 1 {
				t.Fatalf(
					"%s collision touched external triggers",
					test.name,
				)
			}
		})
	}
}

func openSQLiteReview4RebuildProbe(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(
			"file:"+strings.ReplaceAll(t.Name(), "/", "-")+
				"?mode=memory&cache=shared&_foreign_keys=1",
		),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE source (
			id INTEGER NOT NULL PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`INSERT INTO source (id, value)
		 VALUES (1, 'source-sentinel')`,
		`CREATE TABLE external_source (
			id INTEGER NOT NULL PRIMARY KEY
		)`,
		`CREATE TABLE external_audit (
			value TEXT NOT NULL
		)`,
		`CREATE TRIGGER trg_external_references_source
		 AFTER INSERT ON external_source
		 BEGIN
			INSERT INTO external_audit (value)
			SELECT value FROM source WHERE id = NEW.id;
		 END`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func readSQLiteReview4SchemaObject(
	t *testing.T,
	db *gorm.DB,
	schema string,
	name string,
) string {
	t.Helper()
	catalog := "sqlite_master"
	if schema == "temp" {
		catalog = "sqlite_temp_master"
	}
	var sqlText string
	if err := db.Raw(
		"SELECT COALESCE(sql, '') FROM "+catalog+" WHERE name = ?",
		name,
	).Scan(&sqlText).Error; err != nil {
		t.Fatal(err)
	}
	if sqlText == "" {
		t.Fatalf("%s.%s is missing", schema, name)
	}
	return sqlText
}

func TestSQLiteFoundationRebuildCallersRejectReservedTableCollision(
	t *testing.T,
) {
	t.Run("identity full cutover", func(t *testing.T) {
		db := openLegacyWebhookCredentialMigrationDB(
			t,
			"review4-identity-collision",
		)
		seedLegacyWebhookCredentialPair(t, db)
		const collision = "domain_events__identity_contract"
		before := installSQLiteReview4SentinelTable(t, db, collision)
		if err := migrateWebhookSnapshotCredentialLifetimeContractAt(
			db,
			time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
		); err == nil {
			t.Fatal("identity cutover accepted its reserved table collision")
		}
		assertSQLiteReview4SentinelTable(t, db, collision, before)
		assertSQLiteReview4CheckpointCount(t, db, 0)
	})

	t.Run("constraint rebuild", func(t *testing.T) {
		db := openLegacyWebhookCredentialMigrationDB(
			t,
			"review4-constraint-collision",
		)
		if err := prepareWebhookSnapshotCredentialLifetimeColumns(db); err != nil {
			t.Fatal(err)
		}
		const collision = "webhook_delivery_snapshots__webhook_credential_contract"
		before := installSQLiteReview4SentinelTable(t, db, collision)
		err := db.Transaction(func(tx *gorm.DB) error {
			return rebuildSQLiteWebhookCredentialConstraintTable(
				tx,
				"webhook_delivery_snapshots",
				[]string{"chk_webhook_snapshot_scope"},
			)
		})
		if err == nil {
			t.Fatal("constraint rebuild accepted its reserved table collision")
		}
		assertSQLiteReview4SentinelTable(t, db, collision, before)
	})

	t.Run("project foreign key rebuild", func(t *testing.T) {
		db := openLegacyWebhookCredentialMigrationDB(
			t,
			"review4-fk-collision",
		)
		const collision = "domain_events__project_scope_fk"
		before := installSQLiteReview4SentinelTable(t, db, collision)
		err := db.Transaction(func(tx *gorm.DB) error {
			return rebuildSQLiteTableWithProjectScopeFK(
				tx,
				webhookProjectScopeFKDefinitions()[0],
			)
		})
		if err == nil {
			t.Fatal("Project FK rebuild accepted its reserved table collision")
		}
		assertSQLiteReview4SentinelTable(t, db, collision, before)
	})
}

type sqliteReview4SentinelState struct {
	TableDDL   string
	IndexDDL   string
	TriggerDDL string
	Value      string
}

func installSQLiteReview4SentinelTable(
	t *testing.T,
	db *gorm.DB,
	name string,
) sqliteReview4SentinelState {
	t.Helper()
	quoted := quoteAutomationWebhookSQLiteIdentifier(name)
	index := name + "__sentinel_index"
	trigger := name + "__sentinel_trigger"
	for _, statement := range []string{
		"CREATE TABLE " + quoted +
			" (id INTEGER NOT NULL PRIMARY KEY, sentinel TEXT NOT NULL)",
		"INSERT INTO " + quoted +
			" (id, sentinel) VALUES (1, 'preserve-me')",
		"CREATE UNIQUE INDEX " +
			quoteAutomationWebhookSQLiteIdentifier(index) +
			" ON " + quoted + "(sentinel)",
		"CREATE TRIGGER " +
			quoteAutomationWebhookSQLiteIdentifier(trigger) +
			" AFTER UPDATE ON " + quoted +
			" BEGIN SELECT NEW.id; END",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return readSQLiteReview4SentinelState(t, db, name)
}

func readSQLiteReview4SentinelState(
	t *testing.T,
	db *gorm.DB,
	name string,
) sqliteReview4SentinelState {
	t.Helper()
	var objects []sqliteSchemaObject
	if err := db.Raw(`
		SELECT type, name, sql
		FROM sqlite_master
		WHERE name IN (?, ?, ?)
		ORDER BY type, name
	`,
		name,
		name+"__sentinel_index",
		name+"__sentinel_trigger",
	).Scan(&objects).Error; err != nil {
		t.Fatal(err)
	}
	state := sqliteReview4SentinelState{}
	for _, object := range objects {
		switch object.Type {
		case "table":
			state.TableDDL = object.SQL
		case "index":
			state.IndexDDL = object.SQL
		case "trigger":
			state.TriggerDDL = object.SQL
		}
	}
	if state.TableDDL == "" || state.IndexDDL == "" ||
		state.TriggerDDL == "" {
		t.Fatalf("sentinel schema %s is incomplete: %+v", name, state)
	}
	if err := db.Table(name).Select("sentinel").
		Where("id = 1").Scan(&state.Value).Error; err != nil {
		t.Fatal(err)
	}
	return state
}

func assertSQLiteReview4SentinelTable(
	t *testing.T,
	db *gorm.DB,
	name string,
	want sqliteReview4SentinelState,
) {
	t.Helper()
	got := readSQLiteReview4SentinelState(t, db, name)
	if got != want {
		t.Fatalf("sentinel %s changed:\nwant=%+v\ngot=%+v", name, want, got)
	}
}

func assertSQLiteReview4CheckpointCount(
	t *testing.T,
	db *gorm.DB,
	want int64,
) {
	t.Helper()
	var count int64
	if err := db.Model(&models.SchemaMigrationCheckpoint{}).
		Where("key = ?", webhookSnapshotCredentialLifetimeCheckpointKey).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("foundation checkpoint count = %d, want %d", count, want)
	}
}

func TestSQLiteIdentityRebuildPreservesSchemaObjectsAndRollback(
	t *testing.T,
) {
	db := openLegacyWebhookCredentialMigrationDB(t, "review4-fidelity")
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(2)
	sqlDB.SetMaxIdleConns(2)
	if err := db.Exec("DROP TABLE domain_events").Error; err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE domain_events (
			id TEXT PRIMARY KEY,
			organization_id INTEGER,
			project_id INTEGER,
			payload TEXT NOT NULL DEFAULT 'Seed',
			payload_lower TEXT GENERATED ALWAYS AS (lower(payload)) STORED,
			CONSTRAINT chk_domain_payload CHECK (payload <> '')
		) STRICT, WITHOUT ROWID`,
		`CREATE TABLE domain_event_audit (
			value TEXT NOT NULL
		)`,
		`CREATE TABLE domain_event_external_actions (
			id TEXT NOT NULL,
			payload TEXT NOT NULL
		)`,
		`CREATE UNIQUE INDEX idx_domain_payload_unique
		 ON domain_events(payload)`,
		`CREATE INDEX idx_domain_scope_partial
		 ON domain_events(organization_id)
		 WHERE payload <> 'skip'`,
		`CREATE INDEX idx_domain_payload_expression
		 ON domain_events(lower(payload))`,
		`CREATE TRIGGER trg_domain_internal
		 AFTER UPDATE OF payload ON domain_events
		 BEGIN
			INSERT INTO domain_event_audit(value) VALUES (NEW.payload);
		 END`,
		`CREATE TRIGGER trg_domain_external
		 AFTER INSERT ON domain_event_external_actions
		 BEGIN
			UPDATE domain_events
			SET payload = NEW.payload
			WHERE id = NEW.id;
		 END`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	seedLegacyWebhookCredentialPair(t, db)
	before := captureSQLiteReview4FidelityState(t, db)

	const callbackName = "test:review4_restore_index_failure"
	if err := db.Callback().Raw().Before("gorm:raw").Register(
		callbackName,
		func(tx *gorm.DB) {
			statement := strings.ToLower(
				strings.Join(
					strings.Fields(tx.Statement.SQL.String()),
					" ",
				),
			)
			if strings.HasPrefix(
				statement,
				"create unique index idx_domain_payload_unique",
			) {
				_ = tx.AddError(
					errors.New("injected Review-4 index restore failure"),
				)
			}
		},
	); err != nil {
		t.Fatal(err)
	}
	err = migrateWebhookSnapshotCredentialLifetimeContractAt(
		db,
		time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
	)
	if err == nil ||
		!strings.Contains(err.Error(), "injected Review-4 index restore failure") {
		t.Fatalf("injected restore failure error = %v", err)
	}
	if err := db.Callback().Raw().Remove(callbackName); err != nil {
		t.Fatal(err)
	}
	afterFailure := captureSQLiteReview4FidelityState(t, db)
	if afterFailure != before {
		t.Fatalf(
			"failed identity rebuild changed schema/data:\nbefore=%+v\nafter=%+v",
			before,
			afterFailure,
		)
	}
	assertSQLiteReview4CheckpointCount(t, db, 0)
	assertNoSQLiteReview4FoundationTempObjects(t, db)
	assertEverySQLiteConnectionForeignKeysOn(t, db, 2)

	cutoverAt := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	if err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		db,
		cutoverAt,
	); err != nil {
		t.Fatalf("complete fidelity cutover: %v", err)
	}
	assertLegacyWebhookCredentialDeadline(
		t,
		db,
		cutoverAt.Add(models.WebhookDeliveryCredentialLifetime),
	)
	assertNoSQLiteReview4FoundationTempObjects(t, db)
	assertEverySQLiteConnectionForeignKeysOn(t, db, 2)

	var tableDDL string
	if err := db.Raw(`
		SELECT sql FROM sqlite_master
		WHERE type = 'table' AND name = 'domain_events'
	`).Scan(&tableDDL).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToUpper(tableDDL), "STRICT") ||
		!strings.Contains(strings.ToUpper(tableDDL), "WITHOUT ROWID") {
		t.Fatalf("identity rebuild lost table suffix: %s", tableDDL)
	}
	if err := db.Exec(`
		INSERT INTO domain_event_external_actions (id, payload)
		VALUES (?, 'Changed')
	`, legacyWebhookEventID).Error; err != nil {
		t.Fatal(err)
	}
	var event struct {
		Payload      string `gorm:"column:payload"`
		PayloadLower string `gorm:"column:payload_lower"`
	}
	if err := db.Table("domain_events").
		Select("payload", "payload_lower").
		Where("id = ?", legacyWebhookEventID).
		Take(&event).Error; err != nil {
		t.Fatal(err)
	}
	if event.Payload != "Changed" || event.PayloadLower != "changed" {
		t.Fatalf("generated/external trigger state = %+v", event)
	}
	var auditCount int64
	if err := db.Table("domain_event_audit").
		Where("value = 'Changed'").Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("internal trigger wrote %d audit rows", auditCount)
	}
	if err := db.Exec(`
		INSERT INTO domain_events (
			id, organization_id, project_id, payload
		) VALUES (
			'00000000-0000-4000-8000-000000000099', 11, 22, 'Changed'
		)
	`).Error; err == nil {
		t.Fatal("restored unique index accepted a duplicate payload")
	}
	if err := db.Exec(
		"UPDATE domain_events SET payload = '' WHERE id = ?",
		legacyWebhookEventID,
	).Error; err == nil {
		t.Fatal("restored custom CHECK accepted an empty payload")
	}
}

type sqliteReview4FidelityState struct {
	TableSQL     string
	ObjectDigest string
	EventDigest  string
}

func captureSQLiteReview4FidelityState(
	t *testing.T,
	db *gorm.DB,
) sqliteReview4FidelityState {
	t.Helper()
	var state sqliteReview4FidelityState
	if err := db.Raw(`
		SELECT sql FROM sqlite_master
		WHERE type = 'table' AND name = 'domain_events'
	`).Scan(&state.TableSQL).Error; err != nil {
		t.Fatal(err)
	}
	var objects []sqliteSchemaObject
	if err := db.Raw(`
		SELECT type, name, sql
		FROM sqlite_master
		WHERE name IN (
			'idx_domain_payload_unique',
			'idx_domain_scope_partial',
			'idx_domain_payload_expression',
			'trg_domain_internal',
			'trg_domain_external'
		)
		ORDER BY type, name
	`).Scan(&objects).Error; err != nil {
		t.Fatal(err)
	}
	objectParts := make([]string, 0, len(objects))
	for _, object := range objects {
		objectParts = append(
			objectParts,
			object.Type+"|"+object.Name+"|"+object.SQL,
		)
	}
	state.ObjectDigest = strings.Join(objectParts, "\n")
	var event struct {
		ID             string `gorm:"column:id"`
		OrganizationID int    `gorm:"column:organization_id"`
		ProjectID      int    `gorm:"column:project_id"`
		Payload        string `gorm:"column:payload"`
		PayloadLower   string `gorm:"column:payload_lower"`
	}
	if err := db.Table("domain_events").
		Where("id = ?", legacyWebhookEventID).
		Take(&event).Error; err != nil {
		t.Fatal(err)
	}
	state.EventDigest = fmt.Sprintf("%+v", event)
	return state
}

func assertNoSQLiteReview4FoundationTempObjects(
	t *testing.T,
	db *gorm.DB,
) {
	t.Helper()
	var count int64
	if err := db.Raw(`
		SELECT COUNT(*)
		FROM (
			SELECT name FROM sqlite_master
			UNION ALL
			SELECT name FROM sqlite_temp_master
		) AS objects
		WHERE name LIKE '%__identity_contract'
		   OR name LIKE '%__webhook_credential_contract'
		   OR name LIKE '%__project_scope_fk'
	`).Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("foundation rebuild left %d temp objects", count)
	}
}

func TestSQLiteForeignKeyViolationProbeIsBoundedWithManyRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"PRAGMA foreign_keys = OFF",
		"CREATE TABLE parent (id INTEGER PRIMARY KEY)",
		`CREATE TABLE child (
			id INTEGER PRIMARY KEY,
			parent_id INTEGER,
			FOREIGN KEY (parent_id) REFERENCES parent(id)
		)`,
		`WITH RECURSIVE sequence(value) AS (
			SELECT 1
			UNION ALL
			SELECT value + 1 FROM sequence WHERE value < 10000
		 )
		 INSERT INTO child (id, parent_id)
		 SELECT value, value FROM sequence`,
		"PRAGMA foreign_keys = ON",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	violation, found, err := firstSQLiteForeignKeyViolation(db)
	if err != nil {
		t.Fatal(err)
	}
	if !found || violation.Table != "child" {
		t.Fatalf("bounded FK violation = %+v found=%t", violation, found)
	}
}

func waitForSQLiteReview4Context(
	ctx context.Context,
	channel <-chan struct{},
) error {
	select {
	case <-channel:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
