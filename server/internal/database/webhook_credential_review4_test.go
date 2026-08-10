package database

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSQLiteWebhookCredentialUUIDShapeRejectsNonTextAndNULBytes(
	t *testing.T,
) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	const canonical = "00000000-0000-7000-8000-000000000001"
	for _, requireV7 := range []bool{false, true} {
		expression := webhookCredentialUUIDShapeSQL(
			"sqlite",
			"candidate",
			requireV7,
		)
		tests := []struct {
			name      string
			candidate any
			want      bool
		}{
			{name: "canonical text", candidate: canonical, want: true},
			{
				name:      "NUL suffix",
				candidate: canonical + "\x00",
				want:      false,
			},
			{
				name:      "BLOB storage class",
				candidate: []byte(canonical),
				want:      false,
			},
		}
		for _, test := range tests {
			t.Run(fmt.Sprintf("%s/v7=%t", test.name, requireV7), func(
				t *testing.T,
			) {
				var accepted bool
				if err := db.Raw(
					"SELECT "+expression+
						" FROM (SELECT ? AS candidate)",
					test.candidate,
				).Scan(&accepted).Error; err != nil {
					t.Fatal(err)
				}
				if accepted != test.want {
					t.Fatalf(
						"SQLite UUID shape accepted=%t, want %t",
						accepted,
						test.want,
					)
				}
			})
		}
	}
}

func TestSQLiteOwnerAndRuntimeRejectNULTerminatedCredentialPair(
	t *testing.T,
) {
	t.Run("owner cutover", func(t *testing.T) {
		db := openLegacyWebhookCredentialMigrationDB(t, "review4-owner-nul")
		seedLegacyWebhookCredentialPair(t, db)
		writeNULTerminatedSQLiteWebhookPair(t, db)
		if err := migrateWebhookSnapshotCredentialLifetimeContractAt(
			db,
			time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
		); err == nil {
			t.Fatal("owner cutover accepted a NUL-terminated credential pair")
		}
		var checkpoints int64
		if err := db.Model(&models.SchemaMigrationCheckpoint{}).
			Where("key = ?", webhookSnapshotCredentialLifetimeCheckpointKey).
			Count(&checkpoints).Error; err != nil {
			t.Fatal(err)
		}
		if checkpoints != 0 {
			t.Fatalf("failed NUL cutover wrote %d checkpoints", checkpoints)
		}
	})

	t.Run("runtime", func(t *testing.T) {
		db := openLegacyWebhookCredentialMigrationDB(t, "review4-runtime-nul")
		seedLegacyWebhookCredentialPair(t, db)
		if err := migrateWebhookSnapshotCredentialLifetimeContractAt(
			db,
			time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
		); err != nil {
			t.Fatal(err)
		}
		writeNULTerminatedSQLiteWebhookPair(t, db)
		if err := ValidateWebhookSnapshotCredentialLifetimeRuntimeData(
			context.Background(),
			db,
		); err == nil {
			t.Fatal("runtime gate accepted a NUL-terminated credential pair")
		}
	})
}

func writeNULTerminatedSQLiteWebhookPair(t *testing.T, db *gorm.DB) {
	t.Helper()
	eventID := legacyWebhookEventID + "\x00"
	snapshotID := legacyWebhookSnapshotID + "\x00"
	deliveryID := legacyWebhookDeliveryID + "\x00"
	for _, mutation := range []struct {
		query string
		args  []any
	}{
		{
			query: "UPDATE domain_events SET id = ? WHERE id = ?",
			args:  []any{eventID, legacyWebhookEventID},
		},
		{
			query: `UPDATE webhook_delivery_snapshots
				SET id = ?, event_id = ?
				WHERE id = ?`,
			args: []any{snapshotID, eventID, legacyWebhookSnapshotID},
		},
		{
			query: `UPDATE outbox_deliveries
				SET id = ?, event_id = ?, destination_id = ?
				WHERE id = ?`,
			args: []any{
				deliveryID,
				eventID,
				"snapshot:" + snapshotID,
				legacyWebhookDeliveryID,
			},
		},
	} {
		if err := db.Exec(mutation.query, mutation.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestSQLiteIdentityContractRejectsConflictAndPKCollationDrift(
	t *testing.T,
) {
	t.Run("primary key replace", func(t *testing.T) {
		statements := canonicalSQLiteWebhookIdentityFixture()
		statements[0] = strings.Replace(
			statements[0],
			"id TEXT NOT NULL PRIMARY KEY,",
			"id TEXT NOT NULL PRIMARY KEY ON CONFLICT REPLACE,",
			1,
		)
		db := openSQLiteWebhookIdentityFixture(t, statements)
		if err := db.Exec(`
			INSERT INTO domain_events (id, organization_id, project_id)
			VALUES ('replace-me', 1, 1), ('replace-me', 9, 9)
		`).Error; err != nil {
			t.Fatalf("prove SQLite REPLACE behavior: %v", err)
		}
		var scope struct {
			OrganizationID int `gorm:"column:organization_id"`
			ProjectID      int `gorm:"column:project_id"`
		}
		if err := db.Table("domain_events").
			Select("organization_id", "project_id").
			Where("id = 'replace-me'").
			Take(&scope).Error; err != nil {
			t.Fatal(err)
		}
		if scope.OrganizationID != 9 || scope.ProjectID != 9 {
			t.Fatalf("REPLACE probe scope = %+v", scope)
		}
		if err := validatePreparedWebhookCredentialColumnContract(db); err == nil {
			t.Fatal("identity catalog accepted PRIMARY KEY ON CONFLICT REPLACE")
		}
	})

	t.Run("protected not null ignore", func(t *testing.T) {
		statements := canonicalSQLiteWebhookIdentityFixture()
		statements[0] = strings.Replace(
			statements[0],
			"organization_id INTEGER NOT NULL,",
			"organization_id INTEGER NOT NULL ON CONFLICT IGNORE,",
			1,
		)
		db := openSQLiteWebhookIdentityFixture(t, statements)
		if err := validatePreparedWebhookCredentialColumnContract(db); err == nil {
			t.Fatal("identity catalog accepted NOT NULL ON CONFLICT IGNORE")
		}
	})

	t.Run("table primary key nocase", func(t *testing.T) {
		statements := canonicalSQLiteWebhookIdentityFixture()
		statements[0] = strings.Replace(
			statements[0],
			"id TEXT NOT NULL PRIMARY KEY,",
			"id TEXT NOT NULL,",
			1,
		)
		statements[0] = strings.Replace(
			statements[0],
			"project_id INTEGER NOT NULL\n\t\t)",
			"project_id INTEGER NOT NULL,\n"+
				"\t\t\tPRIMARY KEY (id COLLATE NOCASE)\n\t\t)",
			1,
		)
		db := openSQLiteWebhookIdentityFixture(t, statements)
		if err := validatePreparedWebhookCredentialColumnContract(db); err == nil {
			t.Fatal("identity catalog accepted a NOCASE primary-key index")
		}
	})

	t.Run("table primary key descending", func(t *testing.T) {
		statements := canonicalSQLiteWebhookIdentityFixture()
		statements[0] = strings.Replace(
			statements[0],
			"id TEXT NOT NULL PRIMARY KEY,",
			"id TEXT NOT NULL,",
			1,
		)
		statements[0] = strings.Replace(
			statements[0],
			"project_id INTEGER NOT NULL\n\t\t)",
			"project_id INTEGER NOT NULL,\n"+
				"\t\t\tPRIMARY KEY (id DESC)\n\t\t)",
			1,
		)
		db := openSQLiteWebhookIdentityFixture(t, statements)
		if err := validatePreparedWebhookCredentialColumnContract(db); err == nil {
			t.Fatal("identity catalog accepted a descending primary-key index")
		}
	})
}

func TestSQLiteStatusContractsRejectNonDefaultNotNullConflict(t *testing.T) {
	t.Run("outbox status replace", func(t *testing.T) {
		statements := canonicalSQLiteWebhookIdentityFixture()
		statements[2] = strings.Replace(
			statements[2],
			"status TEXT NOT NULL DEFAULT 'pending',",
			"status TEXT NOT NULL ON CONFLICT REPLACE DEFAULT 'pending',",
			1,
		)
		db := openSQLiteWebhookIdentityFixture(t, statements)
		if err := db.Exec(`
			INSERT INTO outbox_deliveries (
				id, organization_id, project_id, event_id,
				destination_type, destination_id, status
			) VALUES (
				'00000000-0000-4000-8000-000000000001',
				1, 1,
				'00000000-0000-4000-8000-000000000002',
				'test_delivery', 'replace-null', NULL
			)
		`).Error; err != nil {
			t.Fatalf("prove SQLite NOT NULL REPLACE behavior: %v", err)
		}
		var status string
		if err := db.Table("outbox_deliveries").
			Select("status").
			Take(&status).Error; err != nil {
			t.Fatal(err)
		}
		if status != "pending" {
			t.Fatalf("SQLite replaced NULL status with %q", status)
		}
		if err := validatePreparedWebhookCredentialColumnContract(db); err == nil {
			t.Fatal("status catalog accepted NOT NULL ON CONFLICT REPLACE")
		}
	})

	t.Run("project status replace", func(t *testing.T) {
		statements := canonicalSQLiteWebhookIdentityFixture()
		statements[3] = strings.Replace(
			statements[3],
			"status TEXT NOT NULL DEFAULT 'active'",
			"status TEXT NOT NULL ON CONFLICT REPLACE DEFAULT 'active'",
			1,
		)
		db := openSQLiteWebhookIdentityFixture(t, statements)
		if err := validatePreparedWebhookCredentialColumnContract(db); err == nil {
			t.Fatal("Project status catalog accepted NOT NULL ON CONFLICT REPLACE")
		}
	})
}

func TestSQLiteContractsAcceptExplicitAbortAndRejectDirectMalformedWrites(
	t *testing.T,
) {
	statements := canonicalSQLiteWebhookIdentityFixture()
	for index := range statements {
		statements[index] = strings.ReplaceAll(
			statements[index],
			"id TEXT NOT NULL PRIMARY KEY",
			"id TEXT NOT NULL PRIMARY KEY ON CONFLICT ABORT",
		)
	}
	statements[0] = strings.Replace(
		statements[0],
		"organization_id INTEGER NOT NULL,",
		"organization_id INTEGER NOT NULL ON CONFLICT ABORT,",
		1,
	)
	statements[2] = strings.Replace(
		statements[2],
		"status TEXT NOT NULL DEFAULT 'pending',",
		"status TEXT NOT NULL ON CONFLICT ABORT DEFAULT 'pending',",
		1,
	)
	statements[3] = strings.Replace(
		statements[3],
		"status TEXT NOT NULL DEFAULT 'active'",
		"status TEXT NOT NULL ON CONFLICT ABORT DEFAULT 'active'",
		1,
	)
	db := openSQLiteWebhookIdentityFixture(t, statements)
	if err := validatePreparedWebhookCredentialColumnContract(db); err != nil {
		t.Fatalf("explicit SQLite ABORT contract: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO domain_events (id, organization_id, project_id)
		VALUES ('immutable', 1, 1)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		INSERT INTO domain_events (id, organization_id, project_id)
		VALUES ('immutable', 9, 9)
	`).Error; err == nil {
		t.Fatal("canonical primary key replaced a duplicate immutable row")
	}
	var organizationID int
	if err := db.Table("domain_events").
		Select("organization_id").
		Where("id = 'immutable'").
		Scan(&organizationID).Error; err != nil {
		t.Fatal(err)
	}
	if organizationID != 1 {
		t.Fatalf("duplicate write changed immutable scope to %d", organizationID)
	}
	if err := db.Exec(`
		INSERT INTO outbox_deliveries (
			id, organization_id, project_id, event_id,
			destination_type, destination_id, status
		) VALUES (
			'00000000-0000-4000-8000-000000000010',
			1, 1,
			'00000000-0000-4000-8000-000000000011',
			'test_delivery', 'null-status', NULL
		)
	`).Error; err == nil {
		t.Fatal("canonical status NOT NULL rewrote raw NULL")
	}
}

func TestProjectStatusColumnContractRejectsSchemaDrift(t *testing.T) {
	tests := []struct {
		name      string
		from      string
		to        string
		generated bool
	}{
		{
			name: "nullable",
			from: "status TEXT NOT NULL DEFAULT 'active'",
			to:   "status TEXT DEFAULT 'active'",
		},
		{
			name: "wrong type",
			from: "status TEXT NOT NULL DEFAULT 'active'",
			to:   "status VARCHAR(19) NOT NULL DEFAULT 'active'",
		},
		{
			name: "missing default",
			from: "status TEXT NOT NULL DEFAULT 'active'",
			to:   "status TEXT NOT NULL",
		},
		{
			name: "wrong default",
			from: "status TEXT NOT NULL DEFAULT 'active'",
			to:   "status TEXT NOT NULL DEFAULT 'archived'",
		},
		{
			name:      "generated",
			from:      "status TEXT NOT NULL DEFAULT 'active'",
			to:        "status TEXT GENERATED ALWAYS AS ('active') STORED",
			generated: true,
		},
	}
	for _, populated := range []bool{false, true} {
		for _, test := range tests {
			t.Run(fmt.Sprintf("%s/populated=%t", test.name, populated), func(
				t *testing.T,
			) {
				statements := canonicalSQLiteWebhookIdentityFixture()
				statements[3] = strings.Replace(
					statements[3],
					test.from,
					test.to,
					1,
				)
				db := openSQLiteWebhookIdentityFixture(t, statements)
				if populated {
					query := `INSERT INTO projects (
						id, organization_id, status
					) VALUES (1, 1, 'active')`
					if test.generated {
						query = `INSERT INTO projects (
							id, organization_id
						) VALUES (1, 1)`
					}
					if err := db.Exec(query).Error; err != nil {
						t.Fatal(err)
					}
				}
				if err := validatePreparedWebhookCredentialColumnContract(
					db,
				); err == nil {
					t.Fatal("Project status catalog accepted schema drift")
				}
			})
		}
	}
}

func TestSQLiteFoundationRejectsTempSchemaShadows(t *testing.T) {
	canonical := canonicalSQLiteWebhookIdentityFixture()
	catalogTests := []struct {
		name       string
		tableIndex int
		from       string
		to         string
	}{
		{
			name:       "identity nullable main",
			tableIndex: 0,
			from:       "organization_id INTEGER NOT NULL",
			to:         "organization_id INTEGER",
		},
		{
			name:       "delivery status nullable main",
			tableIndex: 2,
			from:       "status TEXT NOT NULL DEFAULT 'pending'",
			to:         "status TEXT DEFAULT 'pending'",
		},
		{
			name:       "Project status nullable main",
			tableIndex: 3,
			from:       "status TEXT NOT NULL DEFAULT 'active'",
			to:         "status TEXT DEFAULT 'active'",
		},
	}
	for _, test := range catalogTests {
		t.Run("catalog/"+test.name, func(t *testing.T) {
			statements := append([]string(nil), canonical...)
			statements[test.tableIndex] = strings.Replace(
				statements[test.tableIndex],
				test.from,
				test.to,
				1,
			)
			db := openSQLiteWebhookIdentityFixture(t, statements)
			if err := db.Connection(func(pinned *gorm.DB) error {
				tempDDL := strings.Replace(
					canonical[test.tableIndex],
					"CREATE TABLE",
					"CREATE TEMP TABLE",
					1,
				)
				if err := pinned.Exec(tempDDL).Error; err != nil {
					return err
				}
				if err := validatePreparedWebhookCredentialColumnContract(
					pinned,
				); err == nil {
					return fmt.Errorf(
						"SQLite catalog accepted durable main drift " +
							"behind canonical TEMP shadow",
					)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}

	t.Run("runtime pinned transaction", func(t *testing.T) {
		db := openLegacyWebhookCredentialMigrationDB(t, "review4-temp-runtime")
		seedLegacyWebhookCredentialPair(t, db)
		if err := migrateWebhookSnapshotCredentialLifetimeContractAt(
			db,
			time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
		); err != nil {
			t.Fatal(err)
		}
		if err := db.Connection(func(pinned *gorm.DB) error {
			if err := pinned.Exec(`
				CREATE TEMP TABLE projects (
					id INTEGER NOT NULL PRIMARY KEY,
					organization_id INTEGER NOT NULL,
					status VARCHAR(20) NOT NULL DEFAULT 'active'
				)
			`).Error; err != nil {
				return err
			}
			err := ValidateWebhookSnapshotCredentialLifetimeRuntimeData(
				context.Background(),
				pinned,
			)
			if err == nil {
				return fmt.Errorf(
					"SQLite runtime accepted a TEMP projects shadow",
				)
			}
			if !strings.Contains(strings.ToLower(err.Error()), "temp") ||
				!strings.Contains(strings.ToLower(err.Error()), "shadow") {
				return fmt.Errorf(
					"SQLite runtime TEMP shadow error = %v",
					err,
				)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("owner cutover pinned connection", func(t *testing.T) {
		db := openLegacyWebhookCredentialMigrationDB(t, "review4-temp-owner")
		seedLegacyWebhookCredentialPair(t, db)
		if err := db.Connection(func(pinned *gorm.DB) error {
			if err := pinned.Exec(`
				CREATE TEMP TABLE projects (
					id INTEGER NOT NULL PRIMARY KEY,
					organization_id INTEGER NOT NULL,
					status VARCHAR(20) NOT NULL DEFAULT 'active'
				)
			`).Error; err != nil {
				return err
			}
			err := migrateWebhookSnapshotCredentialLifetimeContractAt(
				pinned,
				time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
			)
			if err == nil {
				return fmt.Errorf(
					"SQLite owner cutover accepted a TEMP projects shadow",
				)
			}
			if !strings.Contains(strings.ToLower(err.Error()), "temp") ||
				!strings.Contains(strings.ToLower(err.Error()), "shadow") {
				return fmt.Errorf(
					"SQLite owner TEMP shadow error = %v",
					err,
				)
			}
			var foreignKeys int
			if err := pinned.Raw("PRAGMA foreign_keys").
				Scan(&foreignKeys).Error; err != nil {
				return err
			}
			if foreignKeys != 1 {
				return fmt.Errorf(
					"SQLite owner TEMP shadow left foreign_keys=%d",
					foreignKeys,
				)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPostgresPrimaryKeyCatalogComparatorRejectsExactnessDrift(
	t *testing.T,
) {
	canonical := postgresWebhookCredentialPrimaryKeyRow{
		TableName:              "domain_events",
		TableOID:               101,
		ExpectedTableOID:       101,
		ConstraintOID:          102,
		ConstraintType:         "p",
		ColumnName:             "id",
		ConstraintAttnum:       1,
		ConstraintOrdinality:   1,
		ConstraintIndexOID:     103,
		ConstraintValidated:    true,
		IndexOID:               103,
		IndexTableOID:          101,
		IndexAttnum:            1,
		IndexOrdinality:        1,
		ColumnCollationOID:     100,
		IndexCollationOID:      100,
		IndexAttributeCount:    1,
		IndexKeyAttributeCount: 1,
		IndexUnique:            true,
		IndexPrimary:           true,
		IndexValid:             true,
		IndexReady:             true,
		IndexLive:              true,
		IndexImmediate:         true,
		IndexAccessMethod:      "btree",
	}
	if !postgresWebhookCredentialPrimaryKeyRowsAreCanonical(
		[]postgresWebhookCredentialPrimaryKeyRow{canonical},
	) {
		t.Fatal("canonical PostgreSQL primary-key catalog row was rejected")
	}
	tests := []struct {
		name   string
		mutate func(*postgresWebhookCredentialPrimaryKeyRow)
	}{
		{
			name: "deferrable",
			mutate: func(row *postgresWebhookCredentialPrimaryKeyRow) {
				row.ConstraintDeferrable = true
			},
		},
		{
			name: "initially deferred",
			mutate: func(row *postgresWebhookCredentialPrimaryKeyRow) {
				row.ConstraintDeferred = true
			},
		},
		{
			name: "inherited constraint",
			mutate: func(row *postgresWebhookCredentialPrimaryKeyRow) {
				row.ParentConstraintOID = 999
			},
		},
		{
			name: "invalid backing index",
			mutate: func(row *postgresWebhookCredentialPrimaryKeyRow) {
				row.IndexValid = false
			},
		},
		{
			name: "not ready backing index",
			mutate: func(row *postgresWebhookCredentialPrimaryKeyRow) {
				row.IndexReady = false
			},
		},
		{
			name: "not live backing index",
			mutate: func(row *postgresWebhookCredentialPrimaryKeyRow) {
				row.IndexLive = false
			},
		},
		{
			name: "deferred backing index",
			mutate: func(row *postgresWebhookCredentialPrimaryKeyRow) {
				row.IndexImmediate = false
			},
		},
		{
			name: "wrong backing collation",
			mutate: func(row *postgresWebhookCredentialPrimaryKeyRow) {
				row.IndexCollationOID++
			},
		},
		{
			name: "expression backing index",
			mutate: func(row *postgresWebhookCredentialPrimaryKeyRow) {
				row.IndexHasExpressions = true
			},
		},
		{
			name: "partial backing index",
			mutate: func(row *postgresWebhookCredentialPrimaryKeyRow) {
				row.IndexHasPredicate = true
			},
		},
		{
			name: "extra included attribute",
			mutate: func(row *postgresWebhookCredentialPrimaryKeyRow) {
				row.IndexAttributeCount = 2
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := canonical
			test.mutate(&mutated)
			if postgresWebhookCredentialPrimaryKeyRowsAreCanonical(
				[]postgresWebhookCredentialPrimaryKeyRow{mutated},
			) {
				t.Fatal("unsafe PostgreSQL primary-key catalog was accepted")
			}
		})
	}
}

func TestProjectStatusCheckAndModelRequireExplicitNotNull(t *testing.T) {
	want := requiredClosedVocabularyConstraintExpression(
		"status",
		models.ProjectStatusValues(),
	)
	got := webhookCredentialConstraintDefinitions["chk_projects_status"].expression
	if got != want {
		t.Fatalf("Project status CHECK = %q, want %q", got, want)
	}
	field, ok := reflect.TypeOf(models.Project{}).FieldByName("Status")
	if !ok {
		t.Fatal("Project.Status field is missing")
	}
	assertGORMCheckUsesCanonicalExpression(
		t,
		field.Tag.Get("gorm"),
		"chk_projects_status",
		want,
	)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"CREATE TABLE project_status_probe (" +
			"status TEXT, CONSTRAINT chk_projects_status CHECK (" +
			got + "))",
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"INSERT INTO project_status_probe (status) VALUES (NULL)",
	).Error; err == nil {
		t.Fatal("Project status CHECK accepted raw NULL")
	}
}

func TestSQLiteIdentityRebuildPreservesLeadingAndTrailingLineComments(
	t *testing.T,
) {
	db := openLegacyWebhookCredentialMigrationDB(t, "review4-line-comments")
	if err := db.Exec("DROP TABLE domain_events").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		CREATE TABLE domain_events (
			id TEXT PRIMARY KEY -- trailing identity comment
			,
			-- leading scope comment
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	seedLegacyWebhookCredentialPair(t, db)
	cutoverAt := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	if err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		db,
		cutoverAt,
	); err != nil {
		t.Fatalf("upgrade populated line-comment DDL: %v", err)
	}
	assertLegacyWebhookCredentialDeadline(
		t,
		db,
		cutoverAt.Add(models.WebhookDeliveryCredentialLifetime),
	)
	var tempObjects int64
	if err := db.Raw(`
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE name LIKE '%__identity_contract'
	`).Scan(&tempObjects).Error; err != nil {
		t.Fatal(err)
	}
	if tempObjects != 0 {
		t.Fatalf("line-comment upgrade left %d temp objects", tempObjects)
	}
}
