package database

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSQLiteUniqueConstraintCatalogRejectsProtectedIdentitySemantics(
	t *testing.T,
) {
	t.Run("domain event table UNIQUE REPLACE", func(t *testing.T) {
		statements := canonicalSQLiteWebhookIdentityFixture()
		statements[0] = strings.Replace(
			statements[0],
			"project_id INTEGER NOT NULL\n\t\t)",
			"project_id INTEGER NOT NULL,\n"+
				"\t\t\tCONSTRAINT event_identity_unique "+
				"UNIQUE(id) ON CONFLICT REPLACE\n\t\t)",
			1,
		)
		db := openSQLiteWebhookIdentityFixture(t, statements)
		for _, statement := range []string{
			`INSERT INTO domain_events (
				id, organization_id, project_id
			) VALUES ('immutable', 1, 1)`,
			`INSERT INTO domain_events (
				id, organization_id, project_id
			) VALUES ('immutable', 9, 9)`,
		} {
			if err := db.Exec(statement).Error; err != nil {
				t.Fatal(err)
			}
		}
		var scope struct {
			OrganizationID int `gorm:"column:organization_id"`
			ProjectID      int `gorm:"column:project_id"`
		}
		if err := db.Table("domain_events").
			Select("organization_id", "project_id").
			Where("id = 'immutable'").
			Take(&scope).Error; err != nil {
			t.Fatal(err)
		}
		if scope.OrganizationID != 9 || scope.ProjectID != 9 {
			t.Fatalf("table UNIQUE REPLACE scope = %+v", scope)
		}
		if err := validatePreparedWebhookCredentialColumnContract(
			db,
		); err == nil {
			t.Fatal(
				"SQLite catalog accepted table UNIQUE(id) ON CONFLICT REPLACE",
			)
		}
	})

	t.Run("snapshot table UNIQUE IGNORE", func(t *testing.T) {
		statements := canonicalSQLiteWebhookIdentityFixture()
		statements[1] = strings.Replace(
			statements[1],
			"credential_shred_reason VARCHAR(20)\n\t\t)",
			"credential_shred_reason VARCHAR(20),\n"+
				"\t\t\tCONSTRAINT \"snapshot identity unique\" "+
				"UNIQUE (/* identity */ \"id\" COLLATE BINARY ASC) "+
				"ON CONFLICT IGNORE\n\t\t)",
			1,
		)
		db := openSQLiteWebhookIdentityFixture(t, statements)
		first := db.Exec(`
			INSERT INTO webhook_delivery_snapshots (
				id, organization_id, project_id, event_id
			) VALUES ('snapshot', 1, 1, 'event-one')
		`)
		if first.Error != nil || first.RowsAffected != 1 {
			t.Fatalf("insert first snapshot: %v (%d)", first.Error, first.RowsAffected)
		}
		second := db.Exec(`
			INSERT INTO webhook_delivery_snapshots (
				id, organization_id, project_id, event_id
			) VALUES ('snapshot', 9, 9, 'event-two')
		`)
		if second.Error != nil || second.RowsAffected != 0 {
			t.Fatalf(
				"prove snapshot UNIQUE IGNORE: %v (%d)",
				second.Error,
				second.RowsAffected,
			)
		}
		if err := validatePreparedWebhookCredentialColumnContract(
			db,
		); err == nil {
			t.Fatal(
				"SQLite catalog accepted table UNIQUE(id) ON CONFLICT IGNORE",
			)
		}
	})

	t.Run("Outbox table UNIQUE REPLACE", func(t *testing.T) {
		statements := canonicalSQLiteWebhookIdentityFixture()
		statements[2] = strings.Replace(
			statements[2],
			"expired_at DATETIME\n\t\t)",
			"expired_at DATETIME,\n"+
				"\t\t\tUNIQUE (`id`) ON CONFLICT REPLACE\n\t\t)",
			1,
		)
		db := openSQLiteWebhookIdentityFixture(t, statements)
		for _, statement := range []string{
			`INSERT INTO outbox_deliveries (
				id, organization_id, project_id, event_id,
				destination_type, destination_id
			) VALUES ('delivery', 1, 1, 'event-one', 'test', 'one')`,
			`INSERT INTO outbox_deliveries (
				id, organization_id, project_id, event_id,
				destination_type, destination_id
			) VALUES ('delivery', 9, 9, 'event-two', 'test', 'two')`,
		} {
			if err := db.Exec(statement).Error; err != nil {
				t.Fatal(err)
			}
		}
		var destination string
		if err := db.Table("outbox_deliveries").
			Select("destination_id").
			Where("id = 'delivery'").
			Scan(&destination).Error; err != nil {
			t.Fatal(err)
		}
		if destination != "two" {
			t.Fatalf("Outbox UNIQUE REPLACE survivor = %q", destination)
		}
		if err := validatePreparedWebhookCredentialColumnContract(
			db,
		); err == nil {
			t.Fatal(
				"SQLite catalog accepted table UNIQUE(id) ON CONFLICT REPLACE",
			)
		}
	})
}

func TestSQLiteOutboxStatusUniqueConflictDropsNormalPendingIntent(
	t *testing.T,
) {
	for _, algorithm := range []string{"REPLACE", "IGNORE"} {
		t.Run(strings.ToLower(algorithm), func(t *testing.T) {
			statements := canonicalSQLiteWebhookIdentityFixture()
			statements[2] = strings.Replace(
				statements[2],
				"expired_at DATETIME\n\t\t)",
				"expired_at DATETIME,\n"+
					"\t\t\tCONSTRAINT \"extra pending status\" "+
					"UNIQUE (/* default */ \"status\" COLLATE BINARY) "+
					"ON CONFLICT "+algorithm+"\n\t\t)",
				1,
			)
			db := openSQLiteWebhookIdentityFixture(t, statements)
			first := db.Exec(`
				INSERT INTO outbox_deliveries (
					id, organization_id, project_id, event_id,
					destination_type, destination_id
				) VALUES (
					'delivery-one', 1, 1, 'event-one', 'test', 'one'
				)
			`)
			if first.Error != nil || first.RowsAffected != 1 {
				t.Fatalf(
					"insert first pending delivery: %v (%d)",
					first.Error,
					first.RowsAffected,
				)
			}
			second := db.Exec(`
				INSERT INTO outbox_deliveries (
					id, organization_id, project_id, event_id,
					destination_type, destination_id
				) VALUES (
					'delivery-two', 1, 1, 'event-two', 'test', 'two'
				)
			`)
			if second.Error != nil {
				t.Fatalf("insert second pending delivery: %v", second.Error)
			}
			var rows []struct {
				ID string `gorm:"column:id"`
			}
			if err := db.Table("outbox_deliveries").
				Select("id").
				Order("id").
				Scan(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 {
				t.Fatalf(
					"normal pending writes retained %d rows under %s, want 1",
					len(rows),
					algorithm,
				)
			}
			wantSurvivor := "delivery-two"
			wantSecondRows := int64(1)
			if algorithm == "IGNORE" {
				wantSurvivor = "delivery-one"
				wantSecondRows = 0
			}
			if rows[0].ID != wantSurvivor ||
				second.RowsAffected != wantSecondRows {
				t.Fatalf(
					"%s survivor=%q second rows=%d, want %q/%d",
					algorithm,
					rows[0].ID,
					second.RowsAffected,
					wantSurvivor,
					wantSecondRows,
				)
			}
			if err := validatePreparedWebhookCredentialColumnContract(
				db,
			); err == nil {
				t.Fatalf(
					"SQLite catalog accepted UNIQUE(status) ON CONFLICT %s",
					algorithm,
				)
			}
		})
	}
}

func TestSQLiteUniqueConstraintCatalogRejectsRedundantIdentityAbort(
	t *testing.T,
) {
	tests := []struct {
		name       string
		tableIndex int
		from       string
		to         string
	}{
		{
			name:       "domain event",
			tableIndex: 0,
			from:       "project_id INTEGER NOT NULL\n\t\t)",
			to: "project_id INTEGER NOT NULL,\n" +
				"\t\t\tUNIQUE(id) ON CONFLICT ABORT\n\t\t)",
		},
		{
			name:       "snapshot",
			tableIndex: 1,
			from: "credential_shred_reason VARCHAR(20)\n" +
				"\t\t)",
			to: "credential_shred_reason VARCHAR(20),\n" +
				"\t\t\tUNIQUE(\"id\")\n\t\t)",
		},
		{
			name:       "Outbox",
			tableIndex: 2,
			from:       "expired_at DATETIME\n\t\t)",
			to: "expired_at DATETIME,\n" +
				"\t\t\tCONSTRAINT redundant_identity " +
				"UNIQUE(`id`) ON CONFLICT ABORT\n\t\t)",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			statements := canonicalSQLiteWebhookIdentityFixture()
			statements[test.tableIndex] = strings.Replace(
				statements[test.tableIndex],
				test.from,
				test.to,
				1,
			)
			db := openSQLiteWebhookIdentityFixture(t, statements)
			if err := validatePreparedWebhookCredentialColumnContract(
				db,
			); err == nil {
				t.Fatal(
					"SQLite catalog accepted redundant identity UNIQUE ABORT",
				)
			}
		})
	}
}

func TestSQLiteProjectIdentityUniqueCannotChangeDirectoryCardinality(
	t *testing.T,
) {
	t.Run("organization identity cardinality", func(t *testing.T) {
		statements := canonicalSQLiteWebhookIdentityFixture()
		statements[3] = strings.Replace(
			statements[3],
			"status TEXT NOT NULL DEFAULT 'active'\n\t\t)",
			"status TEXT NOT NULL DEFAULT 'active',\n"+
				"\t\t\tCONSTRAINT one_project_per_organization "+
				"UNIQUE(organization_id) ON CONFLICT ABORT\n\t\t)",
			1,
		)
		db := openSQLiteWebhookIdentityFixture(t, statements)
		if err := db.Exec(`
			INSERT INTO projects (id, organization_id, status)
			VALUES (1, 10, 'active')
		`).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`
			INSERT INTO projects (id, organization_id, status)
			VALUES (2, 10, 'active')
		`).Error; err == nil {
			t.Fatal(
				"unsafe Project UNIQUE did not block the second Project",
			)
		}
		if err := validatePreparedWebhookCredentialColumnContract(
			db,
		); err == nil {
			t.Fatal(
				"SQLite catalog accepted UNIQUE(projects.organization_id)",
			)
		}
	})

	t.Run("redundant Project id", func(t *testing.T) {
		statements := canonicalSQLiteWebhookIdentityFixture()
		statements[3] = strings.Replace(
			statements[3],
			"status TEXT NOT NULL DEFAULT 'active'\n\t\t)",
			"status TEXT NOT NULL DEFAULT 'active',\n"+
				"\t\t\tUNIQUE(\"id\")\n\t\t)",
			1,
		)
		db := openSQLiteWebhookIdentityFixture(t, statements)
		if err := validatePreparedWebhookCredentialColumnContract(
			db,
		); err == nil {
			t.Fatal("SQLite catalog accepted redundant UNIQUE(projects.id)")
		}
	})
}

func TestSQLiteUniqueConstraintCatalogBoundaries(t *testing.T) {
	t.Run("safe user UNIQUE constraints and explicit indexes", func(
		t *testing.T,
	) {
		statements := canonicalSQLiteWebhookIdentityFixture()
		statements[0] = strings.Replace(
			statements[0],
			"project_id INTEGER NOT NULL\n\t\t)",
			"project_id INTEGER NOT NULL,\n"+
				"\t\t\tpayload TEXT CONSTRAINT \"collate\" "+
				"UNIQUE ON CONFLICT ABORT,\n"+
				"\t\t\tcategory TEXT,\n"+
				"\t\t\tCONSTRAINT \"safe composite unique\" "+
				"UNIQUE (/* first */ \"payload\" COLLATE BINARY DESC, "+
				"`category` ASC) ON CONFLICT ABORT,\n"+
				"\t\t\tCONSTRAINT duplicate_safe "+
				"UNIQUE (payload) ON CONFLICT ABORT\n\t\t)",
			1,
		)
		db := openSQLiteWebhookIdentityFixture(t, statements)
		for _, statement := range []string{
			`CREATE UNIQUE INDEX user_domain_composite
			 ON domain_events(payload, category)`,
			`CREATE UNIQUE INDEX user_domain_expression
			 ON domain_events(lower(payload))`,
			`CREATE UNIQUE INDEX user_domain_partial
			 ON domain_events(category)
			 WHERE payload IS NOT NULL`,
		} {
			if err := db.Exec(statement).Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := validatePreparedWebhookCredentialColumnContract(
			db,
		); err != nil {
			t.Fatalf("safe user UNIQUE schema: %v", err)
		}
	})

	t.Run("fake UNIQUE tokens in data and comments", func(t *testing.T) {
		statements := canonicalSQLiteWebhookIdentityFixture()
		statements[0] = strings.Replace(
			statements[0],
			"project_id INTEGER NOT NULL\n\t\t)",
			"project_id INTEGER NOT NULL,\n"+
				"\t\t\tpayload TEXT DEFAULT "+
				"'UNIQUE(status) ON CONFLICT REPLACE',\n"+
				"\t\t\tnote TEXT CHECK ("+
				"note <> 'UNIQUE(id) ON CONFLICT IGNORE')\n"+
				"\t\t\t-- UNIQUE(project_id) ON CONFLICT REPLACE\n"+
				"\t\t)",
			1,
		)
		db := openSQLiteWebhookIdentityFixture(t, statements)
		if err := validatePreparedWebhookCredentialColumnContract(
			db,
		); err != nil {
			t.Fatalf("quoted/comment UNIQUE tokens: %v", err)
		}
	})

	for _, algorithm := range []string{
		"ROLLBACK",
		"FAIL",
		"IGNORE",
		"REPLACE",
	} {
		t.Run("safe column noncanonical "+algorithm, func(t *testing.T) {
			statements := canonicalSQLiteWebhookIdentityFixture()
			statements[0] = strings.Replace(
				statements[0],
				"project_id INTEGER NOT NULL\n\t\t)",
				"project_id INTEGER NOT NULL,\n"+
					"\t\t\tpayload TEXT UNIQUE ON CONFLICT "+
					algorithm+"\n\t\t)",
				1,
			)
			db := openSQLiteWebhookIdentityFixture(t, statements)
			if err := validatePreparedWebhookCredentialColumnContract(
				db,
			); err == nil {
				t.Fatalf(
					"SQLite catalog accepted user UNIQUE ON CONFLICT %s",
					algorithm,
				)
			}
		})
	}

	t.Run("protected status explicit ABORT remains noncanonical", func(
		t *testing.T,
	) {
		statements := canonicalSQLiteWebhookIdentityFixture()
		statements[2] = strings.Replace(
			statements[2],
			"expired_at DATETIME\n\t\t)",
			"expired_at DATETIME,\n"+
				"\t\t\tUNIQUE(status) ON CONFLICT ABORT\n\t\t)",
			1,
		)
		db := openSQLiteWebhookIdentityFixture(t, statements)
		if err := validatePreparedWebhookCredentialColumnContract(
			db,
		); err == nil {
			t.Fatal("SQLite catalog accepted redundant UNIQUE(status) ABORT")
		}
	})

	t.Run("protected composite default ABORT remains noncanonical", func(
		t *testing.T,
	) {
		statements := canonicalSQLiteWebhookIdentityFixture()
		statements[2] = strings.Replace(
			statements[2],
			"expired_at DATETIME\n\t\t)",
			"expired_at DATETIME,\n"+
				"\t\t\tUNIQUE("+
				"event_id, destination_type, destination_id"+
				")\n\t\t)",
			1,
		)
		db := openSQLiteWebhookIdentityFixture(t, statements)
		if err := validatePreparedWebhookCredentialColumnContract(
			db,
		); err == nil {
			t.Fatal("SQLite catalog accepted redundant protected composite UNIQUE")
		}
	})
}

func TestSQLiteOriginUUniqueIndexesRequireStructuredDDLMatch(
	t *testing.T,
) {
	statements := canonicalSQLiteWebhookIdentityFixture()
	statements[0] = strings.Replace(
		statements[0],
		"project_id INTEGER NOT NULL\n\t\t)",
		"project_id INTEGER NOT NULL,\n"+
			"\t\t\tpayload TEXT,\n"+
			"\t\t\tCONSTRAINT \"payload identity\" "+
			"UNIQUE (payload COLLATE NOCASE DESC) "+
			"ON CONFLICT ABORT\n\t\t)",
		1,
	)
	db := openSQLiteWebhookIdentityFixture(t, statements)
	var tableSQL string
	if err := db.Raw(`
		SELECT sql
		FROM main.sqlite_schema
		WHERE type = 'table' AND name = 'domain_events'
	`).Scan(&tableSQL).Error; err != nil {
		t.Fatal(err)
	}
	open, err := findSQLiteTableBodyOpen(tableSQL)
	if err != nil {
		t.Fatal(err)
	}
	close, ok := matchingSQLParenthesis(tableSQL, open)
	if !ok {
		t.Fatal("domain_events DDL is malformed")
	}
	parts, err := splitSQLiteTableBody(tableSQL[open+1 : close])
	if err != nil {
		t.Fatal(err)
	}
	constraints, err := parseSQLiteUniqueConstraints(parts)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSQLiteUniqueConstraintIndexes(
		db,
		"domain_events",
		constraints,
	); err != nil {
		t.Fatalf("match actual origin=u index: %v", err)
	}
	if err := validateSQLiteUniqueConstraintIndexes(
		db,
		"domain_events",
		nil,
	); err == nil {
		t.Fatal("SQLite origin=u index passed without structured DDL")
	}
	mismatched := append(
		[]sqliteParsedUniqueConstraint(nil),
		constraints...,
	)
	for index := range mismatched {
		mismatched[index].keys = append(
			[]sqliteUniqueConstraintKey(nil),
			mismatched[index].keys...,
		)
		for keyIndex := range mismatched[index].keys {
			if mismatched[index].keys[keyIndex].column == "payload" {
				mismatched[index].keys[keyIndex].descending = false
			}
		}
	}
	if err := validateSQLiteUniqueConstraintIndexes(
		db,
		"domain_events",
		mismatched,
	); err == nil {
		t.Fatal("SQLite origin=u index passed with wrong key order semantics")
	}
}

func TestSQLiteFoundationMigrationPreservesSafeUserUniqueConstraint(
	t *testing.T,
) {
	db := openLegacySQLiteReview5Database(t, "safe-unique")
	seedLegacyWebhookCredentialPair(t, db)
	rebuildSQLiteReview3Table(
		t,
		db,
		"domain_events",
		func(parts []string) ([]string, error) {
			return append(
				parts,
				"payload TEXT",
				`CONSTRAINT "user payload unique"
				 UNIQUE (payload COLLATE BINARY DESC)
				 ON CONFLICT ABORT`,
			), nil
		},
	)
	if err := db.Exec(
		"UPDATE domain_events SET payload = ? WHERE id = ?",
		"preserved",
		legacyWebhookEventID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		db,
		time.Date(2026, 8, 10, 4, 5, 6, 0, time.UTC),
	); err != nil {
		t.Fatalf("migrate safe user UNIQUE schema: %v", err)
	}
	if err := validateWebhookCredentialLifetimeCatalog(db); err != nil {
		t.Fatalf("validate safe user UNIQUE schema: %v", err)
	}
	var tableSQL string
	if err := db.Raw(`
		SELECT sql
		FROM main.sqlite_schema
		WHERE type = 'table' AND name = 'domain_events'
	`).Scan(&tableSQL).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tableSQL, `"user payload unique"`) ||
		!strings.Contains(tableSQL, "ON CONFLICT ABORT") {
		t.Fatalf("safe user UNIQUE was not preserved: %s", tableSQL)
	}
	insert := db.Exec(`
		INSERT INTO domain_events (
			id, organization_id, project_id, payload
		) VALUES (
			'00000000-0000-4000-8000-000000000951',
			11, 22, 'preserved'
		)
	`)
	if insert.Error == nil {
		t.Fatal("preserved user UNIQUE accepted a duplicate payload")
	}
	var count int64
	if err := db.Table("domain_events").
		Where("payload = ?", "preserved").
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("safe user UNIQUE retained %d payload rows, want 1", count)
	}
}

func TestRunMigrationsRejectsPopulatedSQLiteUnsafeUniqueWithoutMutation(
	t *testing.T,
) {
	db := openPopulatedLegacySQLiteReview5Database(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(2)
	sqlDB.SetMaxIdleConns(2)
	rebuildSQLiteReview3Table(
		t,
		db,
		"outbox_deliveries",
		func(parts []string) ([]string, error) {
			return append(
				parts,
				`CONSTRAINT "unsafe pending unique"
				 UNIQUE (status) ON CONFLICT REPLACE`,
			), nil
		},
	)
	before := captureSQLiteReview5FoundationState(t, db)
	if err := RunMigrations(db); err == nil {
		t.Fatal(
			"full populated SQLite RunMigrations accepted unsafe UNIQUE REPLACE",
		)
	}
	after := captureSQLiteReview5FoundationState(t, db)
	if after != before {
		t.Fatalf(
			"failed UNIQUE migration mutated foundation state:\n"+
				"before=%+v\nafter=%+v",
			before,
			after,
		)
	}
	assertEverySQLiteConnectionForeignKeysOn(t, db, 2)
}

func openPopulatedLegacySQLiteReview5Database(t *testing.T) *gorm.DB {
	t.Helper()
	db := openFreshSQLiteReview5Database(t, "populated")
	now := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	organization := models.Organization{
		Slug:   "task9a-review5-legacy",
		Name:   "Task 9a Review 5 Legacy",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "TASK9AR5",
		Name:           "Task 9a Review 5",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	project := models.Project{
		ID:             2501,
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            models.ProjectKey("TASK9AR5"),
		Name:           "Task 9a Review 5 legacy",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	event := models.DomainEvent{
		ID:              legacyWebhookEventID,
		OrganizationID:  organization.ID,
		ProjectID:       project.ID,
		SpecVersion:     "1.0",
		Source:          "/task9a/review5",
		Type:            "io.chronodesk.task9a.review5.v1",
		Time:            now,
		DataContentType: "application/json",
		Data:            []byte(`{}`),
		ActorType:       models.ActorTypeSystem,
		ActorID:         "task9a-review5",
		ResourceVersion: 1,
	}
	if err := db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	deadline := now.Add(models.WebhookDeliveryCredentialLifetime)
	snapshot := models.WebhookDeliverySnapshot{
		ID:                  legacyWebhookSnapshotID,
		CreatedAt:           now,
		OrganizationID:      organization.ID,
		ProjectID:           project.ID,
		ConfigID:            2501,
		EventID:             event.ID,
		ConfigUpdatedAt:     now,
		Provider:            models.WebhookProviderCustom,
		WebhookURL:          "https://example.invalid/task9a-review5",
		Secret:              "sealed",
		CredentialExpiresAt: deadline,
		EnabledEvents:       `["io.chronodesk.task9a.review5.v1"]`,
		MessageFormat:       "text",
		RetryCount:          1,
		RetryInterval:       5,
		TimeoutSeconds:      5,
		RateLimit:           1,
		RateLimitWindow:     60,
	}
	if err := db.Create(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	delivery := models.OutboxDelivery{
		ID:              legacyWebhookDeliveryID,
		OrganizationID:  organization.ID,
		ProjectID:       project.ID,
		EventID:         event.ID,
		DestinationType: "webhook",
		DestinationID:   "snapshot:" + snapshot.ID,
		Status:          models.OutboxDeliveryPending,
		MaxAttempts:     1,
		NextAttemptAt:   now,
		ExpiresAt:       &deadline,
	}
	if err := db.Create(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	downgradeSQLiteWebhookCredentialFoundation(t, db)
	return db
}

func openLegacySQLiteReview5Database(
	t *testing.T,
	suffix string,
) *gorm.DB {
	t.Helper()
	db := openSQLiteReview5Database(t, suffix)
	if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.SchemaMigrationCheckpoint{}); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE projects (
			id INTEGER NOT NULL,
			organization_id INTEGER NOT NULL,
			status VARCHAR(20) NOT NULL DEFAULT 'active',
			PRIMARY KEY (id),
			CONSTRAINT chk_projects_status CHECK (
				status IN ('active', 'archived')
			)
		)`,
		`CREATE UNIQUE INDEX idx_projects_scope_id
		 ON projects(organization_id, id)`,
		`CREATE TABLE domain_events (
			id TEXT PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL
		)`,
		`CREATE TABLE webhook_delivery_snapshots (
			id TEXT PRIMARY KEY,
			created_at DATETIME NOT NULL,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			config_id INTEGER NOT NULL,
			event_id TEXT NOT NULL,
			secret TEXT NOT NULL DEFAULT '',
			previous_secret TEXT NOT NULL DEFAULT '',
			access_token TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE outbox_deliveries (
			id TEXT PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			event_id TEXT NOT NULL,
			destination_type TEXT NOT NULL,
			destination_id TEXT NOT NULL,
			status VARCHAR(20) NOT NULL DEFAULT 'pending'
		)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func openFreshSQLiteReview5Database(
	t *testing.T,
	suffix string,
) *gorm.DB {
	t.Helper()
	db := openSQLiteReview5Database(t, suffix)
	if err := RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func openSQLiteReview5Database(
	t *testing.T,
	suffix string,
) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf(
		"file:%s-review5-%s?mode=memory&cache=shared&_foreign_keys=1",
		strings.ReplaceAll(t.Name(), "/", "-"),
		strings.ReplaceAll(suffix, " ", "-"),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return registerSQLiteReview5DatabaseCleanup(t, db)
}

func registerSQLiteReview5DatabaseCleanup(
	t *testing.T,
	db *gorm.DB,
) *gorm.DB {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close Review-5 SQLite database: %v", err)
			return
		}
		if open := sqlDB.Stats().OpenConnections; open != 0 {
			t.Errorf(
				"Review-5 SQLite database retained %d open connections",
				open,
			)
		}
		if err := sqlDB.Ping(); err == nil {
			t.Error("closed Review-5 SQLite database still accepted Ping")
		}
	})
	return db
}

type sqliteReview5FoundationState struct {
	OutboxDDL       string
	EventCount      int64
	SnapshotCount   int64
	DeliveryCount   int64
	CheckpointCount int64
	DeliveryDigest  string
}

func captureSQLiteReview5FoundationState(
	t *testing.T,
	db *gorm.DB,
) sqliteReview5FoundationState {
	t.Helper()
	var state sqliteReview5FoundationState
	if err := db.Raw(`
		SELECT sql
		FROM main.sqlite_schema
		WHERE type = 'table' AND name = 'outbox_deliveries'
	`).Scan(&state.OutboxDDL).Error; err != nil {
		t.Fatal(err)
	}
	for table, destination := range map[string]*int64{
		"domain_events":                &state.EventCount,
		"webhook_delivery_snapshots":   &state.SnapshotCount,
		"outbox_deliveries":            &state.DeliveryCount,
		"schema_migration_checkpoints": &state.CheckpointCount,
	} {
		query := "SELECT COUNT(*) FROM " +
			quoteAutomationWebhookSQLiteIdentifier(table)
		if table == "schema_migration_checkpoints" {
			query += " WHERE key = ?"
			if err := db.Raw(
				query,
				webhookSnapshotCredentialLifetimeCheckpointKey,
			).Scan(destination).Error; err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := db.Raw(query).Scan(destination).Error; err != nil {
			t.Fatal(err)
		}
	}
	var delivery struct {
		ID              string `gorm:"column:id"`
		EventID         string `gorm:"column:event_id"`
		DestinationType string `gorm:"column:destination_type"`
		DestinationID   string `gorm:"column:destination_id"`
		Status          string `gorm:"column:status"`
	}
	if err := db.Table("outbox_deliveries").
		Select(
			"id",
			"event_id",
			"destination_type",
			"destination_id",
			"status",
		).
		Where("id = ?", legacyWebhookDeliveryID).
		Take(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	state.DeliveryDigest = fmt.Sprintf("%+v", delivery)
	return state
}
