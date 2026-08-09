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

func TestWebhookSnapshotScopeCheckExplicitlyRejectsNullIdentity(t *testing.T) {
	expression := webhookCredentialConstraintDefinitions["chk_webhook_snapshot_scope"].expression
	for _, required := range []string{
		"organization_id IS NOT NULL",
		"project_id IS NOT NULL",
		"event_id IS NOT NULL",
	} {
		if !strings.Contains(expression, required) {
			t.Fatalf(
				"snapshot scope CHECK %q does not explicitly require %s",
				expression,
				required,
			)
		}
	}
}

func TestWebhookCredentialUUIDShapeRequiresRFC4122Variant(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, requireV7 := range []bool{false, true} {
		expression := webhookCredentialUUIDShapeSQL("sqlite", "candidate", requireV7)
		for _, candidate := range []string{
			"00000000-0000-7000-0000-000000000001",
			"00000000-0000-7000-1000-000000000001",
			"00000000-0000-7000-c000-000000000001",
			"00000000-0000-7000-f000-000000000001",
		} {
			var accepted bool
			if err := db.Raw(
				"SELECT "+expression+" FROM (SELECT ? AS candidate)",
				candidate,
			).Scan(&accepted).Error; err != nil {
				t.Fatal(err)
			}
			if accepted {
				t.Fatalf(
					"SQLite UUID shape requireV7=%t accepted non-RFC4122 variant %q",
					requireV7,
					candidate,
				)
			}
		}
	}
	postgres := webhookCredentialUUIDShapeSQL("postgres", "candidate", true)
	if !strings.Contains(postgres, "[89ab]") {
		t.Fatalf(
			"PostgreSQL UUIDv7 shape %q does not require an RFC4122 variant",
			postgres,
		)
	}
}

func TestSQLiteProjectScopeIndexRejectsNOCASECollation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE projects (
			id INTEGER PRIMARY KEY,
			organization_id INTEGER NOT NULL
		)`,
		`CREATE UNIQUE INDEX idx_projects_scope_id
		 ON projects(organization_id COLLATE NOCASE, id)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	valid, err := sqliteAutomationWebhookIndexIsValid(
		db,
		webhookCredentialIndexDefinitions[0],
	)
	if err != nil {
		t.Fatal(err)
	}
	if valid {
		t.Fatal("SQLite Project scope index accepted NOCASE collation")
	}
}

func TestSQLiteWebhookStatusContractRejectsNOCASECollation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		CREATE TABLE outbox_deliveries (
			status TEXT COLLATE NOCASE NOT NULL DEFAULT 'pending'
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := validateWebhookCredentialStatusColumnContract(db); err == nil {
		t.Fatal("SQLite status contract accepted NOCASE collation")
	}
}

func TestSQLiteWebhookStatusContractAcceptsExplicitBINARYCollation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		CREATE TABLE outbox_deliveries (
			status TEXT COLLATE BINARY NOT NULL DEFAULT 'pending'
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := validateWebhookCredentialStatusColumnContract(db); err != nil {
		t.Fatalf("explicit BINARY status collation: %v", err)
	}
}

func TestSQLitePreparedContractRejectsNullableIdentityColumns(t *testing.T) {
	tests := []struct {
		name string
		from string
		to   string
	}{
		{
			name: "nullable organization",
			from: "organization_id INTEGER NOT NULL",
			to:   "organization_id INTEGER",
		},
		{
			name: "nullable destination type",
			from: "destination_type TEXT NOT NULL",
			to:   "destination_type TEXT",
		},
		{
			name: "wrong numeric type",
			from: "project_id INTEGER NOT NULL",
			to:   "project_id TEXT NOT NULL",
		},
		{
			name: "missing primary key",
			from: "id TEXT NOT NULL PRIMARY KEY",
			to:   "id TEXT NOT NULL",
		},
		{
			name: "identity default",
			from: "event_id TEXT NOT NULL",
			to:   "event_id TEXT NOT NULL DEFAULT ''",
		},
		{
			name: "unsafe identity collation",
			from: "destination_type TEXT NOT NULL",
			to:   "destination_type TEXT COLLATE NOCASE NOT NULL",
		},
		{
			name: "generated identity",
			from: "destination_id TEXT NOT NULL",
			to: "destination_id TEXT GENERATED ALWAYS AS " +
				"(event_id) STORED",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			statements := canonicalSQLiteWebhookIdentityFixture()
			mutated := false
			for index := range statements {
				replaced := strings.Replace(
					statements[index],
					test.from,
					test.to,
					1,
				)
				if replaced != statements[index] && !mutated {
					mutated = true
					statements[index] = replaced
				}
			}
			if !mutated {
				t.Fatalf("fixture mutation %q was not applied", test.from)
			}
			db := openSQLiteWebhookIdentityFixture(t, statements)
			if err := validatePreparedWebhookCredentialColumnContract(db); err == nil {
				t.Fatal("prepared SQLite catalog accepted unsafe identity contract")
			}
		})
	}
}

func TestSQLitePreparedContractAcceptsExactIdentityColumns(t *testing.T) {
	db := openSQLiteWebhookIdentityFixture(
		t,
		canonicalSQLiteWebhookIdentityFixture(),
	)
	if err := validatePreparedWebhookCredentialColumnContract(db); err != nil {
		t.Fatalf("exact SQLite identity contract: %v", err)
	}
}

func TestSQLiteIdentityContractRejectsCompositePrimaryKeyWithUnprotectedColumn(
	t *testing.T,
) {
	statements := canonicalSQLiteWebhookIdentityFixture()
	statements[1] = strings.Replace(
		statements[1],
		"id TEXT NOT NULL PRIMARY KEY,",
		"id TEXT NOT NULL,\n\t\t\tcreated_at DATETIME NOT NULL,",
		1,
	)
	statements[1] = strings.Replace(
		statements[1],
		"credential_shred_reason VARCHAR(20)\n\t\t)",
		"credential_shred_reason VARCHAR(20),\n"+
			"\t\t\tPRIMARY KEY (id, created_at)\n\t\t)",
		1,
	)
	db := openSQLiteWebhookIdentityFixture(t, statements)
	if err := validatePreparedWebhookCredentialColumnContract(db); err == nil {
		t.Fatal(
			"SQLite identity catalog accepted a composite primary key with an unprotected column",
		)
	}
}

func TestSQLiteCredentialReasonContractRejectsNOCASECollation(t *testing.T) {
	statements := canonicalSQLiteWebhookIdentityFixture()
	for index := range statements {
		statements[index] = strings.Replace(
			statements[index],
			"credential_shred_reason VARCHAR(20)",
			"credential_shred_reason VARCHAR(20) COLLATE NOCASE",
			1,
		)
	}
	db := openSQLiteWebhookIdentityFixture(t, statements)
	if err := validatePreparedWebhookCredentialColumnContract(db); err == nil {
		t.Fatal("SQLite credential shred reason accepted NOCASE collation")
	}
}

func canonicalSQLiteWebhookIdentityFixture() []string {
	return []string{
		`CREATE TABLE domain_events (
			id TEXT NOT NULL PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL
		)`,
		`CREATE TABLE webhook_delivery_snapshots (
			id TEXT NOT NULL PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			event_id TEXT NOT NULL,
			credential_expires_at DATETIME,
			credential_shredded_at DATETIME,
			credential_shred_reason VARCHAR(20)
		)`,
		`CREATE TABLE outbox_deliveries (
			id TEXT NOT NULL PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			event_id TEXT NOT NULL,
			destination_type TEXT NOT NULL,
			destination_id TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			expires_at DATETIME,
			expired_at DATETIME
		)`,
		`CREATE TABLE projects (
			id INTEGER NOT NULL PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'active'
		)`,
	}
}

func openSQLiteWebhookIdentityFixture(
	t *testing.T,
	statements []string,
) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestRunMigrationsUpgradesLegacyNullableSnapshotScopeCheck(t *testing.T) {
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
	if err := RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	rebuildSQLiteReview3Table(t, db, "webhook_delivery_snapshots", func(
		parts []string,
	) ([]string, error) {
		for index, part := range parts {
			constraint, named, err := parseSQLiteTableConstraint(part)
			if err != nil {
				return nil, err
			}
			if named && strings.EqualFold(
				constraint.name,
				"chk_webhook_snapshot_scope",
			) {
				parts[index] = "CONSTRAINT `chk_webhook_snapshot_scope` " +
					"CHECK (organization_id > 0 AND project_id > 0 " +
					"AND event_id <> '')"
				return parts, nil
			}
		}
		return nil, errors.New("snapshot scope CHECK was not found")
	})
	if err := RunMigrations(db); err != nil {
		t.Fatalf("upgrade legacy nullable snapshot scope CHECK: %v", err)
	}
}

func TestSQLiteCanonicalWebhookIdentityColumnsRejectRawNull(t *testing.T) {
	db := openLegacyWebhookCredentialMigrationDB(t, "review3-raw-null")
	seedLegacyWebhookCredentialPair(t, db)
	if err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		db,
		time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
	); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		sql  string
	}{
		{
			name: "event id",
			sql:  "UPDATE domain_events SET id = NULL",
		},
		{
			name: "event scope",
			sql:  "UPDATE domain_events SET organization_id = NULL",
		},
		{
			name: "snapshot event",
			sql:  "UPDATE webhook_delivery_snapshots SET event_id = NULL",
		},
		{
			name: "delivery destination type",
			sql:  "UPDATE outbox_deliveries SET destination_type = NULL",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := db.Exec(test.sql).Error; err == nil {
				t.Fatalf("%s accepted raw NULL", test.name)
			}
		})
	}
}

func TestSQLiteOwnerCutoverRejectsCompleteNullScopePair(t *testing.T) {
	db := openLegacyWebhookCredentialMigrationDB(t, "review3-owner-null")
	for _, table := range []string{
		"domain_events",
		"webhook_delivery_snapshots",
		"outbox_deliveries",
	} {
		rebuildSQLiteReview3Table(t, db, table, func(
			parts []string,
		) ([]string, error) {
			for index, part := range parts {
				if sqliteDDLLeadingIdentifier(part) != "organization_id" {
					continue
				}
				parts[index] = strings.Replace(
					part,
					" NOT NULL",
					"",
					1,
				)
				return parts, nil
			}
			return nil, fmt.Errorf("%s.organization_id is missing", table)
		})
	}
	nullEventID := "00000000-0000-4000-8000-000000000701"
	nullSnapshotID := "00000000-0000-7000-8000-000000000702"
	if err := db.Exec(`
		INSERT INTO domain_events (id, organization_id, project_id)
		VALUES (?, NULL, 22)
	`, nullEventID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		INSERT INTO webhook_delivery_snapshots (
			id, created_at, organization_id, project_id, config_id,
			event_id
		) VALUES (?, ?, NULL, 22, 70, ?)
	`, nullSnapshotID, time.Now().UTC(), nullEventID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		INSERT INTO outbox_deliveries (
			id, organization_id, project_id, event_id,
			destination_type, destination_id, status
		) VALUES (?, NULL, 22, ?, 'webhook', ?, 'pending')
	`,
		"00000000-0000-4000-8000-000000000703",
		nullEventID,
		"snapshot:"+nullSnapshotID,
	).Error; err != nil {
		t.Fatal(err)
	}
	err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		db,
		time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
	)
	if err == nil ||
		!strings.Contains(
			err.Error(),
			"domain_events.organization_id contains NULL identity",
		) {
		t.Fatalf("NULL identity cutover error = %v", err)
	}
	var checkpoints int64
	if err := db.Model(&models.SchemaMigrationCheckpoint{}).
		Where("key = ?", webhookSnapshotCredentialLifetimeCheckpointKey).
		Count(&checkpoints).Error; err != nil {
		t.Fatal(err)
	}
	if checkpoints != 0 {
		t.Fatalf("NULL identity cutover wrote %d checkpoints", checkpoints)
	}
}

func TestSQLiteOwnerCutoverRejectsNullDestinationBeforeWebhookFilter(
	t *testing.T,
) {
	db := openLegacyWebhookCredentialMigrationDB(
		t,
		"review3-owner-null-destination",
	)
	seedLegacyWebhookCredentialPair(t, db)
	rebuildSQLiteReview3Table(t, db, "outbox_deliveries", func(
		parts []string,
	) ([]string, error) {
		for index, part := range parts {
			if sqliteDDLLeadingIdentifier(part) != "destination_type" {
				continue
			}
			parts[index] = strings.Replace(part, " NOT NULL", "", 1)
			return parts, nil
		}
		return nil, errors.New("outbox destination_type is missing")
	})
	if err := db.Exec(`
		UPDATE outbox_deliveries SET destination_type = NULL
	`).Error; err != nil {
		t.Fatal(err)
	}
	err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		db,
		time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
	)
	if err == nil ||
		!strings.Contains(
			err.Error(),
			"outbox_deliveries.destination_type contains NULL identity",
		) {
		t.Fatalf("NULL destination cutover error = %v", err)
	}
}

func TestSQLiteRuntimeCatalogRejectsNOCASEProjectScopeIndex(t *testing.T) {
	db := openFreshSQLiteReview3Database(t, "nocase-parent-index")
	if err := db.Exec("DROP INDEX idx_projects_scope_id").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		CREATE UNIQUE INDEX idx_projects_scope_id
		ON projects(organization_id COLLATE NOCASE, id)
	`).Error; err != nil {
		t.Fatal(err)
	}
	err := validateWebhookCredentialLifetimeCatalog(db)
	if err == nil ||
		!strings.Contains(err.Error(), "idx_projects_scope_id") {
		t.Fatalf("NOCASE parent index catalog error = %v", err)
	}
	err = validateWebhookCredentialRuntimeSnapshot(
		context.Background(),
		db,
	)
	if err == nil ||
		!strings.Contains(
			strings.ToLower(err.Error()),
			"foreign key mismatch",
		) {
		t.Fatalf("NOCASE parent index runtime precheck error = %v", err)
	}
}

func TestSQLiteClosedVocabularyCatalogRejectsNOCASEColumnsBeforeSetGate(
	t *testing.T,
) {
	for _, test := range []struct {
		name   string
		table  string
		column string
		mutate func(*gorm.DB)
	}{
		{
			name:   "status",
			table:  "outbox_deliveries",
			column: "status",
			mutate: func(db *gorm.DB) {
				if err := db.Exec(`
					UPDATE outbox_deliveries SET status = 'PENDING'
				`).Error; err != nil {
					t.Fatalf("NOCASE status direct SQL: %v", err)
				}
			},
		},
		{
			name:   "credential shred reason",
			table:  "webhook_delivery_snapshots",
			column: "credential_shred_reason",
			mutate: func(db *gorm.DB) {
				if err := db.Exec(`
					UPDATE webhook_delivery_snapshots
					SET credential_shredded_at = ?,
						credential_shred_reason = 'SUCCEEDED',
						secret = '',
						previous_secret = '',
						access_token = ''
				`, time.Now().UTC()).Error; err != nil {
					t.Fatalf("NOCASE shred reason direct SQL: %v", err)
				}
			},
		},
		{
			name:   "project status",
			table:  "projects",
			column: "status",
			mutate: func(db *gorm.DB) {
				if err := db.Exec(`
					UPDATE projects SET status = 'ACTIVE' WHERE id = 22
				`).Error; err != nil {
					t.Fatalf("NOCASE Project status direct SQL: %v", err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := openLegacyWebhookCredentialMigrationDB(
				t,
				"review3-nocase-"+test.name,
			)
			seedLegacyWebhookCredentialPair(t, db)
			if err := migrateWebhookSnapshotCredentialLifetimeContractAt(
				db,
				time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
			); err != nil {
				t.Fatal(err)
			}
			rebuildSQLiteReview3Table(t, db, test.table, func(
				parts []string,
			) ([]string, error) {
				for index, part := range parts {
					if sqliteDDLLeadingIdentifier(part) != test.column {
						continue
					}
					first, _, _, ok :=
						scanSQLiteDDLIdentifier(part, 0)
					if !ok || !strings.EqualFold(first, test.column) {
						return nil, errors.New("malformed column fixture")
					}
					lowerPart := strings.ToLower(part)
					typeEnd := -1
					for _, columnType := range []string{
						"varchar(20)",
						"text",
					} {
						start := strings.Index(lowerPart, columnType)
						if start >= 0 {
							typeEnd = start + len(columnType)
							break
						}
					}
					if typeEnd < 0 {
						return nil, errors.New("missing protected text type")
					}
					parts[index] = part[:typeEnd] +
						" COLLATE NOCASE" + part[typeEnd:]
					return parts, nil
				}
				return nil, fmt.Errorf("%s.%s is missing", test.table, test.column)
			})
			test.mutate(db)
			if err := validateWebhookCredentialOwnerSet(db, true); err != nil {
				t.Fatalf(
					"NOCASE fixture should demonstrate why catalog runs first: %v",
					err,
				)
			}
			err := validateWebhookCredentialLifetimeCatalog(db)
			if err == nil ||
				!strings.Contains(
					strings.ToLower(err.Error()),
					"collation",
				) {
				t.Fatalf("NOCASE %s catalog error = %v", test.column, err)
			}
		})
	}
}

func TestSQLiteOwnerAndRuntimeRejectReservedUUIDVariants(t *testing.T) {
	db := openLegacyWebhookCredentialMigrationDB(t, "review3-uuid-variant")
	seedLegacyWebhookCredentialPair(t, db)
	if err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		db,
		time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
	); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		reservedID string
		mutate     func(string) error
		restore    func() error
	}{
		{
			name:       "snapshot",
			reservedID: "00000000-0000-7000-1000-000000000902",
			mutate: func(reserved string) error {
				if err := db.Exec(
					"UPDATE webhook_delivery_snapshots SET id = ? WHERE id = ?",
					reserved,
					legacyWebhookSnapshotID,
				).Error; err != nil {
					return err
				}
				return db.Exec(
					"UPDATE outbox_deliveries SET destination_id = ? WHERE id = ?",
					"snapshot:"+reserved,
					legacyWebhookDeliveryID,
				).Error
			},
			restore: func() error {
				if err := db.Exec(
					"UPDATE webhook_delivery_snapshots SET id = ? WHERE id = ?",
					legacyWebhookSnapshotID,
					"00000000-0000-7000-1000-000000000902",
				).Error; err != nil {
					return err
				}
				return db.Exec(
					"UPDATE outbox_deliveries SET destination_id = ? WHERE id = ?",
					"snapshot:"+legacyWebhookSnapshotID,
					legacyWebhookDeliveryID,
				).Error
			},
		},
		{
			name:       "event",
			reservedID: "00000000-0000-4000-c000-000000000901",
			mutate: func(reserved string) error {
				for _, statement := range []struct {
					sql  string
					args []any
				}{
					{
						"UPDATE domain_events SET id = ? WHERE id = ?",
						[]any{reserved, legacyWebhookEventID},
					},
					{
						"UPDATE webhook_delivery_snapshots SET event_id = ? WHERE id = ?",
						[]any{reserved, legacyWebhookSnapshotID},
					},
					{
						"UPDATE outbox_deliveries SET event_id = ? WHERE id = ?",
						[]any{reserved, legacyWebhookDeliveryID},
					},
				} {
					if err := db.Exec(statement.sql, statement.args...).Error; err != nil {
						return err
					}
				}
				return nil
			},
			restore: func() error {
				reserved := "00000000-0000-4000-c000-000000000901"
				for _, statement := range []struct {
					sql  string
					args []any
				}{
					{
						"UPDATE domain_events SET id = ? WHERE id = ?",
						[]any{legacyWebhookEventID, reserved},
					},
					{
						"UPDATE webhook_delivery_snapshots SET event_id = ? WHERE id = ?",
						[]any{legacyWebhookEventID, legacyWebhookSnapshotID},
					},
					{
						"UPDATE outbox_deliveries SET event_id = ? WHERE id = ?",
						[]any{legacyWebhookEventID, legacyWebhookDeliveryID},
					},
				} {
					if err := db.Exec(statement.sql, statement.args...).Error; err != nil {
						return err
					}
				}
				return nil
			},
		},
		{
			name:       "delivery",
			reservedID: "00000000-0000-4000-f000-000000000903",
			mutate: func(reserved string) error {
				return db.Exec(
					"UPDATE outbox_deliveries SET id = ? WHERE id = ?",
					reserved,
					legacyWebhookDeliveryID,
				).Error
			},
			restore: func() error {
				return db.Exec(
					"UPDATE outbox_deliveries SET id = ? WHERE id = ?",
					legacyWebhookDeliveryID,
					"00000000-0000-4000-f000-000000000903",
				).Error
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.mutate(test.reservedID); err != nil {
				t.Fatal(err)
			}
			ownerErr := validateWebhookCredentialOwnerSet(db, true)
			if ownerErr == nil ||
				!strings.Contains(ownerErr.Error(), test.reservedID) {
				t.Fatalf("SQLite owner UUID variant error = %v", ownerErr)
			}
			runtimeErr := validateWebhookCredentialRuntimeSnapshot(
				context.Background(),
				db,
			)
			if runtimeErr == nil ||
				!strings.Contains(runtimeErr.Error(), test.reservedID) {
				t.Fatalf("SQLite runtime UUID variant error = %v", runtimeErr)
			}
			if err := test.restore(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func rebuildSQLiteReview3Table(
	t *testing.T,
	db *gorm.DB,
	table string,
	mutate func([]string) ([]string, error),
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := withPinnedGORMConnection(ctx, db, func(pinned *gorm.DB) error {
		if err := pinned.Exec("PRAGMA foreign_keys = OFF").Error; err != nil {
			return err
		}
		rebuildErr := pinned.Transaction(func(tx *gorm.DB) error {
			var state sqliteSchemaObject
			if err := tx.Raw(`
				SELECT type, name, sql
				FROM sqlite_master
				WHERE type = 'table' AND name = ?
			`, table).Take(&state).Error; err != nil {
				return err
			}
			open, err := findSQLiteTableBodyOpen(state.SQL)
			if err != nil {
				return err
			}
			close, ok := matchingSQLParenthesis(state.SQL, open)
			if !ok {
				return fmt.Errorf("SQLite table %s has malformed DDL", table)
			}
			parts, err := splitSQLiteTableBody(state.SQL[open+1 : close])
			if err != nil {
				return err
			}
			parts, err = mutate(parts)
			if err != nil {
				return err
			}
			temp := table + "__review3_rebuild"
			return rebuildSQLiteTableFromDDL(
				tx,
				table,
				temp,
				"CREATE TABLE "+
					quoteAutomationWebhookSQLiteIdentifier(temp)+
					" ("+strings.Join(parts, ", ")+")"+
					state.SQL[close+1:],
				"Review-3 fixture",
			)
		})
		restoreErr := pinned.Exec("PRAGMA foreign_keys = ON").Error
		return errors.Join(rebuildErr, restoreErr)
	}); err != nil {
		t.Fatal(err)
	}
}

func openFreshSQLiteReview3Database(
	t *testing.T,
	suffix string,
) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(
			"file:"+strings.ReplaceAll(t.Name()+"-"+suffix, "/", "-")+
				"?mode=memory&cache=shared&_foreign_keys=1",
		),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	return db
}
