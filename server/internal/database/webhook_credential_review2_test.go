package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestCanonicalWebhookConstraintDefinitionPreservesQuotedIdentifierCase(
	t *testing.T,
) {
	canonical, err := canonicalWebhookConstraintDefinition(
		"status IN ('active', 'archived')",
	)
	if err != nil {
		t.Fatal(err)
	}
	quotedWrongCase, err := canonicalWebhookConstraintDefinition(
		`"Status" IN ('active', 'archived')`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if quotedWrongCase == canonical {
		t.Fatalf(
			"quoted PostgreSQL identifier lost case semantics: %q",
			quotedWrongCase,
		)
	}
	quotedCanonical, err := canonicalWebhookConstraintDefinition(
		`"status" IN ('active', 'archived')`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if quotedCanonical != canonical {
		t.Fatalf(
			"quoted canonical lowercase identifier = %q, want %q",
			quotedCanonical,
			canonical,
		)
	}
}

func TestSQLiteNamedCheckConstraintParserIsStructuralAndUnique(
	t *testing.T,
) {
	const name = "chk_projects_status"
	tests := []struct {
		name    string
		ddl     string
		wantErr string
	}{
		{
			name: "quoted column fake",
			ddl: `CREATE TABLE projects (
				status TEXT,
				"CONSTRAINT chk_projects_status CHECK (status IN ('active','archived'))" TEXT
			)`,
			wantErr: "missing",
		},
		{
			name: "string literal fake",
			ddl: `CREATE TABLE projects (
				status TEXT DEFAULT 'CONSTRAINT chk_projects_status CHECK (status IN (''active'',''archived''))'
			)`,
			wantErr: "missing",
		},
		{
			name: "prefix only",
			ddl: `CREATE TABLE projects (
				status TEXT,
				CONSTRAINT chk_projects_status_extra CHECK (status IN ('active','archived'))
			)`,
			wantErr: "missing",
		},
		{
			name: "duplicate exact name",
			ddl: `CREATE TABLE projects (
				status TEXT,
				CONSTRAINT chk_projects_status CHECK (status IN ('active','archived')),
				CONSTRAINT chk_projects_status CHECK (status IN ('active','archived'))
			)`,
			wantErr: "duplicated",
		},
		{
			name: "wrong constraint type",
			ddl: `CREATE TABLE projects (
				status TEXT,
				CONSTRAINT chk_projects_status UNIQUE (status)
			)`,
			wantErr: "CHECK",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := sqliteNamedCheckConstraintExpression(
				test.ddl,
				name,
			)
			if err == nil ||
				!strings.Contains(
					strings.ToLower(err.Error()),
					strings.ToLower(test.wantErr),
				) {
				t.Fatalf(
					"parser error = %v, want %q",
					err,
					test.wantErr,
				)
			}
		})
	}
}

func TestSQLiteNamedConstraintParserSkipsCommentsStructurally(
	t *testing.T,
) {
	ddl := `CREATE TABLE projects
		/* body-open fake (
		   CONSTRAINT chk_projects_status CHECK (status = 'archived')
		*/
		(
			status TEXT,
			-- fake close ), comma, and CONSTRAINT chk_projects_status CHECK (
			/* fake comma, ), and quoted text 'CONSTRAINT chk_projects_status' */
			CONSTRAINT chk_projects_status CHECK (
				status IN ('active', 'archived')
			)
		)`
	expression, err := sqliteNamedCheckConstraintExpression(
		ddl,
		"chk_projects_status",
	)
	if err != nil {
		t.Fatalf("parse comment-bearing SQLite CHECK: %v", err)
	}
	got, err := canonicalWebhookConstraintDefinition(expression)
	if err != nil {
		t.Fatal(err)
	}
	want, err := canonicalWebhookConstraintDefinition(
		"status IN ('active', 'archived')",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("comment-bearing SQLite CHECK = %q, want %q", got, want)
	}
}

func TestSQLiteNamedProjectScopeForeignKeyBindsNameToCanonicalGroup(
	t *testing.T,
) {
	tests := []struct {
		name string
		ddl  string
	}{
		{
			name: "anonymous canonical plus named wrong columns",
			ddl: `CREATE TABLE domain_events (
				id TEXT PRIMARY KEY,
				organization_id INTEGER NOT NULL,
				project_id INTEGER NOT NULL,
				FOREIGN KEY (organization_id, project_id)
					REFERENCES projects(organization_id, id)
					ON UPDATE RESTRICT ON DELETE RESTRICT,
				CONSTRAINT fk_domain_events_project_scope
					FOREIGN KEY (project_id, organization_id)
					REFERENCES projects(id, organization_id)
					ON UPDATE NO ACTION ON DELETE NO ACTION
			)`,
		},
		{
			name: "quoted column fake plus anonymous canonical",
			ddl: `CREATE TABLE domain_events (
				id TEXT PRIMARY KEY,
				organization_id INTEGER NOT NULL,
				project_id INTEGER NOT NULL,
				"CONSTRAINT fk_domain_events_project_scope FOREIGN KEY (organization_id, project_id)" TEXT,
				FOREIGN KEY (organization_id, project_id)
					REFERENCES projects(organization_id, id)
					ON UPDATE RESTRICT ON DELETE RESTRICT
			)`,
		},
		{
			name: "named wrong action plus anonymous canonical",
			ddl: `CREATE TABLE domain_events (
				id TEXT PRIMARY KEY,
				organization_id INTEGER NOT NULL,
				project_id INTEGER NOT NULL,
				FOREIGN KEY (organization_id, project_id)
					REFERENCES projects(organization_id, id)
					ON UPDATE RESTRICT ON DELETE RESTRICT,
				CONSTRAINT fk_domain_events_project_scope
					FOREIGN KEY (organization_id, project_id)
					REFERENCES projects(organization_id, id)
					ON UPDATE CASCADE ON DELETE CASCADE
			)`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
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
			if err := db.Exec(`
				CREATE TABLE projects (
					id INTEGER NOT NULL,
					organization_id INTEGER NOT NULL,
					PRIMARY KEY (id),
					UNIQUE (organization_id, id)
				)
			`).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec(test.ddl).Error; err != nil {
				t.Fatal(err)
			}
			err = validateSQLiteWebhookProjectScopeFK(
				db,
				webhookProjectScopeFKDefinitions()[0],
			)
			if err == nil {
				t.Fatal(
					"SQLite FK catalog accepted a name detached from the canonical group",
				)
			}
		})
	}
}

func TestSQLiteNamedProjectScopeForeignKeyParsesDeferrabilityExactly(
	t *testing.T,
) {
	tests := []struct {
		name          string
		clause        string
		wantCanonical bool
	}{
		{
			name:          "not deferrable initially immediate",
			clause:        "NOT DEFERRABLE INITIALLY IMMEDIATE",
			wantCanonical: true,
		},
		{
			name:          "deferrable initially immediate",
			clause:        "DEFERRABLE INITIALLY IMMEDIATE",
			wantCanonical: false,
		},
		{
			name:          "deferrable initially deferred",
			clause:        "DEFERRABLE INITIALLY DEFERRED",
			wantCanonical: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ddl := `CREATE TABLE domain_events (
				id TEXT PRIMARY KEY,
				organization_id INTEGER NOT NULL,
				project_id INTEGER NOT NULL,
				CONSTRAINT fk_domain_events_project_scope
					FOREIGN KEY (organization_id, project_id)
					REFERENCES projects(organization_id, id)
					ON UPDATE RESTRICT ON DELETE RESTRICT ` +
				test.clause +
				`)`
			constraint, err := sqliteNamedProjectScopeForeignKey(
				ddl,
				domainEventProjectScopeFK,
			)
			if err != nil {
				t.Fatalf("parse SQLite FK deferrability: %v", err)
			}
			if got := constraint.isCanonicalProjectScopeFK(); got !=
				test.wantCanonical {
				t.Fatalf(
					"SQLite FK canonical = %v, want %v",
					got,
					test.wantCanonical,
				)
			}
		})
	}
}

func TestSQLiteWebhookCredentialSetRejectsMalformedUUIDShape(
	t *testing.T,
) {
	db := openLegacyWebhookCredentialMigrationDB(t, "malformed-uuid-shape")
	seedLegacyWebhookCredentialPair(t, db)
	if err := prepareWebhookSnapshotCredentialLifetimeColumns(db); err != nil {
		t.Fatal(err)
	}
	deadline := time.Date(2026, 8, 17, 1, 2, 3, 0, time.UTC)
	mustExecWebhookCredentialTest(
		t,
		db,
		"UPDATE webhook_delivery_snapshots SET credential_expires_at = ?",
		deadline,
	)
	mustExecWebhookCredentialTest(
		t,
		db,
		"UPDATE outbox_deliveries SET expires_at = ?",
		deadline,
	)
	const malformed = "0000000--0000-7000-8000-000000000902"
	mustExecWebhookCredentialTest(
		t,
		db,
		"UPDATE webhook_delivery_snapshots SET id = ? WHERE id = ?",
		malformed,
		legacyWebhookSnapshotID,
	)
	mustExecWebhookCredentialTest(
		t,
		db,
		"UPDATE outbox_deliveries SET destination_id = ? WHERE id = ?",
		"snapshot:"+malformed,
		legacyWebhookDeliveryID,
	)
	err := validateWebhookCredentialOwnerSet(db, true)
	if err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "snapshot shape") {
		t.Fatalf(
			"owner set validation error = %v, want malformed snapshot shape",
			err,
		)
	}
}

func TestSQLiteRuntimeWebhookCredentialSetRejectsNonCanonicalSnapshotIDs(
	t *testing.T,
) {
	tests := []struct {
		name string
		id   string
	}{
		{
			name: "extra hyphen",
			id:   "0000000--0000-7000-8000-000000000902",
		},
		{
			name: "uppercase hex",
			id:   "00000000-0000-7000-8000-00000000090A",
		},
		{
			name: "wrong UUID version",
			id:   "00000000-0000-6000-8000-000000000902",
		},
		{
			name: "wrong delimiter",
			id:   "00000000_0000-7000-8000-000000000902",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openLegacyWebhookCredentialMigrationDB(
				t,
				"runtime-"+test.name,
			)
			seedLegacyWebhookCredentialPair(t, db)
			if err := migrateWebhookSnapshotCredentialLifetimeContractAt(
				db,
				time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
			); err != nil {
				t.Fatal(err)
			}
			mustExecWebhookCredentialTest(
				t,
				db,
				"UPDATE webhook_delivery_snapshots SET id = ? WHERE id = ?",
				test.id,
				legacyWebhookSnapshotID,
			)
			mustExecWebhookCredentialTest(
				t,
				db,
				"UPDATE outbox_deliveries SET destination_id = ? WHERE id = ?",
				"snapshot:"+test.id,
				legacyWebhookDeliveryID,
			)
			err := ValidateWebhookSnapshotCredentialLifetimeRuntimeData(
				context.Background(),
				db,
			)
			if err == nil ||
				!strings.Contains(
					strings.ToLower(err.Error()),
					"snapshot shape",
				) {
				t.Fatalf(
					"runtime validation error = %v, want canonical UUID rejection",
					err,
				)
			}
		})
	}
}

func TestSQLiteWebhookCredentialSetRejectsNullStatus(
	t *testing.T,
) {
	db := openLegacyWebhookCredentialMigrationDB(t, "null-status")
	seedLegacyWebhookCredentialPair(t, db)
	if err := prepareWebhookSnapshotCredentialLifetimeColumns(db); err != nil {
		t.Fatal(err)
	}
	deadline := time.Date(2026, 8, 17, 1, 2, 3, 0, time.UTC)
	mustExecWebhookCredentialTest(
		t,
		db,
		"UPDATE webhook_delivery_snapshots SET credential_expires_at = ?",
		deadline,
	)
	mustExecWebhookCredentialTest(
		t,
		db,
		"UPDATE outbox_deliveries SET expires_at = ?",
		deadline,
	)
	rebuildLegacyOutboxWithNullableStatus(t, db)
	mustExecWebhookCredentialTest(
		t,
		db,
		"UPDATE outbox_deliveries SET status = NULL WHERE id = ?",
		legacyWebhookDeliveryID,
	)
	err := validateWebhookCredentialOwnerSet(db, true)
	if err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "status") {
		t.Fatalf(
			"owner set validation error = %v, want NULL status rejection",
			err,
		)
	}
}

func TestSQLiteWebhookCredentialStatusColumnContractRejectsNullableDrift(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "-")+
			"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		CREATE TABLE outbox_deliveries (
			status VARCHAR(20) DEFAULT 'pending'
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	err = validateWebhookCredentialStatusColumnContract(db)
	if err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "not null") {
		t.Fatalf(
			"status column contract error = %v, want nullable rejection",
			err,
		)
	}
}

func TestStandaloneSQLiteWebhookCredentialCutoverPinsForeignKeyState(
	t *testing.T,
) {
	db := openLegacyWebhookCredentialMigrationDB(t, "pinned-standalone")
	seedLegacyWebhookCredentialPair(t, db)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(2)
	sqlDB.SetMaxIdleConns(0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	keeper, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer keeper.Close()
	if _, err := keeper.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}

	if err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		db,
		time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
	); err != nil {
		t.Fatalf(
			"standalone multi-connection SQLite cutover did not pin PRAGMA state: %v",
			err,
		)
	}
	var keeperForeignKeys int
	if err := keeper.QueryRowContext(
		ctx,
		"PRAGMA foreign_keys",
	).Scan(&keeperForeignKeys); err != nil {
		t.Fatal(err)
	}
	if keeperForeignKeys != 1 {
		t.Fatalf(
			"keeper SQLite connection foreign_keys = %d, want ON",
			keeperForeignKeys,
		)
	}
	second, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var secondForeignKeys int
	if err := second.QueryRowContext(
		ctx,
		"PRAGMA foreign_keys",
	).Scan(&secondForeignKeys); err != nil {
		t.Fatal(err)
	}
	if secondForeignKeys != 1 {
		t.Fatalf(
			"second SQLite connection foreign_keys = %d, want ON",
			secondForeignKeys,
		)
	}
}

func TestStandaloneSQLiteCutoverRestoresForeignKeysAfterCallerCancel(
	t *testing.T,
) {
	db := openLegacyWebhookCredentialMigrationDB(t, "pinned-cancel")
	seedLegacyWebhookCredentialPair(t, db)
	ctx, cancel := context.WithCancel(context.Background())
	const callbackName = "test:sqlite_cutover_cancel_before_cleanup"
	if err := db.Callback().Create().Before("gorm:create").Register(
		callbackName,
		func(tx *gorm.DB) {
			checkpoint, ok :=
				tx.Statement.Dest.(*models.SchemaMigrationCheckpoint)
			if ok &&
				checkpoint.Key ==
					webhookSnapshotCredentialLifetimeCheckpointKey {
				cancel()
			}
		},
	); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Create().Remove(callbackName)
	})
	err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		db.WithContext(ctx),
		time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
	)
	if err == nil ||
		(!errors.Is(err, context.Canceled) &&
			!strings.Contains(
				strings.ToLower(err.Error()),
				"context canceled",
			)) {
		t.Fatalf("canceled SQLite cutover error = %v", err)
	}
	if removeErr := db.Callback().Create().Remove(callbackName); removeErr != nil {
		t.Fatal(removeErr)
	}
	assertEverySQLiteConnectionForeignKeysOn(t, db, 2)
}

func TestStandaloneSQLiteCutoverRestoresAfterCancelImmediatelyAfterOff(
	t *testing.T,
) {
	db := openLegacyWebhookCredentialMigrationDB(t, "cancel-after-off")
	seedLegacyWebhookCredentialPair(t, db)
	ctx, cancel := context.WithCancel(context.Background())
	const callbackName = "test:sqlite_cutover_cancel_after_off"
	if err := db.Callback().Raw().After("gorm:raw").Register(
		callbackName,
		func(tx *gorm.DB) {
			if strings.EqualFold(
				strings.TrimSpace(tx.Statement.SQL.String()),
				"PRAGMA foreign_keys = OFF",
			) {
				cancel()
			}
		},
	); err != nil {
		t.Fatal(err)
	}
	err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		db.WithContext(ctx),
		time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
	)
	if err == nil ||
		(!errors.Is(err, context.Canceled) &&
			!strings.Contains(
				strings.ToLower(err.Error()),
				"context canceled",
			)) {
		t.Fatalf("post-OFF canceled SQLite cutover error = %v", err)
	}
	if removeErr := db.Callback().Raw().Remove(callbackName); removeErr != nil {
		t.Fatal(removeErr)
	}
	assertEverySQLiteConnectionForeignKeysOn(t, db, 2)
}

func TestStandaloneSQLiteCutoverDiscardsConnectionWhenRestoreFails(
	t *testing.T,
) {
	db := openLegacyWebhookCredentialMigrationDB(t, "pinned-restore-failure")
	seedLegacyWebhookCredentialPair(t, db)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(2)
	sqlDB.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	keeper, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer keeper.Close()
	if _, err := keeper.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}

	const callbackName = "test:sqlite_cutover_restore_failure"
	if err := db.Callback().Raw().Before("gorm:raw").Register(
		callbackName,
		func(tx *gorm.DB) {
			if strings.EqualFold(
				strings.TrimSpace(tx.Statement.SQL.String()),
				"PRAGMA foreign_keys = ON",
			) {
				_ = tx.AddError(
					errors.New("injected SQLite foreign_keys restore failure"),
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
		!strings.Contains(
			err.Error(),
			"injected SQLite foreign_keys restore failure",
		) {
		t.Fatalf("SQLite restore failure error = %v", err)
	}
	if removeErr := db.Callback().Raw().Remove(callbackName); removeErr != nil {
		t.Fatal(removeErr)
	}
	var keeperForeignKeys int
	if err := keeper.QueryRowContext(
		ctx,
		"PRAGMA foreign_keys",
	).Scan(&keeperForeignKeys); err != nil {
		t.Fatal(err)
	}
	if keeperForeignKeys != 1 {
		t.Fatalf(
			"unrelated keeper foreign_keys = %d, want ON",
			keeperForeignKeys,
		)
	}
	replacement, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	var replacementForeignKeys int
	if err := replacement.QueryRowContext(
		ctx,
		"PRAGMA foreign_keys",
	).Scan(&replacementForeignKeys); err != nil {
		t.Fatal(err)
	}
	if replacementForeignKeys != 1 {
		t.Fatalf(
			"replacement SQLite connection foreign_keys = %d, want ON",
			replacementForeignKeys,
		)
	}
}

func TestRunMigrationsUpgradesPopulatedLegacySQLiteWebhookCredentials(
	t *testing.T,
) {
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
		t.Fatalf("build canonical SQLite baseline: %v", err)
	}
	now := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	organization := models.Organization{
		Slug:   "task9a-sqlite-legacy",
		Name:   "Task 9a SQLite Legacy",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "TASK9ASQL",
		Name:           "Task 9a SQLite",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	project := models.Project{
		ID:             2201,
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            models.ProjectKey("TASK9ASQL"),
		Name:           "Task 9a SQLite legacy",
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
		Source:          "/task9a/sqlite-legacy",
		Type:            "io.chronodesk.task9a.sqlite-legacy.v1",
		Time:            now,
		DataContentType: "application/json",
		Data:            []byte(`{}`),
		ActorType:       models.ActorTypeSystem,
		ActorID:         "task9a-sqlite",
		ResourceVersion: 1,
	}
	if err := db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	deadline := now.Add(models.WebhookDeliveryCredentialLifetime)
	seededSnapshot := models.WebhookDeliverySnapshot{
		ID:                  legacyWebhookSnapshotID,
		CreatedAt:           now,
		OrganizationID:      organization.ID,
		ProjectID:           project.ID,
		ConfigID:            99,
		EventID:             event.ID,
		ConfigUpdatedAt:     now,
		Provider:            models.WebhookProviderCustom,
		WebhookURL:          "https://example.invalid/task9a-sqlite",
		Secret:              "sealed",
		CredentialExpiresAt: deadline,
		EnabledEvents:       `["io.chronodesk.task9a.sqlite-legacy.v1"]`,
		MessageFormat:       "text",
		RetryCount:          1,
		RetryInterval:       5,
		TimeoutSeconds:      5,
		RateLimit:           1,
		RateLimitWindow:     60,
	}
	if err := db.Create(&seededSnapshot).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.OutboxDelivery{
		ID:              legacyWebhookDeliveryID,
		OrganizationID:  organization.ID,
		ProjectID:       project.ID,
		EventID:         event.ID,
		DestinationType: "webhook",
		DestinationID:   "snapshot:" + seededSnapshot.ID,
		Status:          models.OutboxDeliveryPending,
		MaxAttempts:     1,
		NextAttemptAt:   now,
		ExpiresAt:       &deadline,
	}).Error; err != nil {
		t.Fatal(err)
	}
	downgradeSQLiteWebhookCredentialFoundation(t, db)
	if err := RunMigrations(db); err != nil {
		t.Fatalf("full populated legacy SQLite RunMigrations: %v", err)
	}
	if err := validateWebhookCredentialLifetimeCatalog(db); err != nil {
		t.Fatalf("populated SQLite exact catalog: %v", err)
	}
	if err := ValidateWebhookSnapshotCredentialLifetimeRuntimeData(
		context.Background(),
		db,
	); err != nil {
		t.Fatalf("populated SQLite runtime gate: %v", err)
	}
	var checkpoint models.SchemaMigrationCheckpoint
	if err := db.Where(
		"key = ?",
		webhookSnapshotCredentialLifetimeCheckpointKey,
	).Take(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	var delivery models.OutboxDelivery
	if err := db.Where(
		"id = ?",
		legacyWebhookDeliveryID,
	).Take(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	var snapshot models.WebhookDeliverySnapshot
	if err := db.Where(
		"id = ?",
		legacyWebhookSnapshotID,
	).Take(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	if delivery.ExpiresAt == nil ||
		!delivery.ExpiresAt.Equal(snapshot.CredentialExpiresAt) ||
		!delivery.ExpiresAt.Equal(
			checkpoint.CompletedAt.Add(
				models.WebhookDeliveryCredentialLifetime,
			),
		) {
		t.Fatalf(
			"populated SQLite deadline/checkpoint mismatch: checkpoint=%s delivery=%v snapshot=%s",
			checkpoint.CompletedAt,
			delivery.ExpiresAt,
			snapshot.CredentialExpiresAt,
		)
	}
	firstCheckpoint := checkpoint.CompletedAt
	if err := RunMigrations(db); err != nil {
		t.Fatalf("rerun populated SQLite migrations: %v", err)
	}
	if err := db.Where(
		"key = ?",
		webhookSnapshotCredentialLifetimeCheckpointKey,
	).Take(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	if !checkpoint.CompletedAt.Equal(firstCheckpoint) {
		t.Fatalf(
			"populated SQLite rerun changed checkpoint from %s to %s",
			firstCheckpoint,
			checkpoint.CompletedAt,
		)
	}
}

func downgradeSQLiteWebhookCredentialFoundation(
	t *testing.T,
	db *gorm.DB,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := withPinnedGORMConnection(ctx, db, func(pinned *gorm.DB) error {
		if err := pinned.Exec("PRAGMA foreign_keys = OFF").Error; err != nil {
			return err
		}
		rebuildErr := pinned.Transaction(func(tx *gorm.DB) error {
			for _, table := range []struct {
				name    string
				columns map[string]struct{}
			}{
				{
					name: "outbox_deliveries",
					columns: map[string]struct{}{
						"expires_at": {},
						"expired_at": {},
					},
				},
				{
					name: "webhook_delivery_snapshots",
					columns: map[string]struct{}{
						"credential_expires_at":   {},
						"credential_shredded_at":  {},
						"credential_shred_reason": {},
					},
				},
			} {
				if err := rebuildSQLiteLegacyFoundationTable(
					tx,
					table.name,
					table.columns,
				); err != nil {
					return err
				}
			}
			return tx.Delete(
				&models.SchemaMigrationCheckpoint{},
				"key = ?",
				webhookSnapshotCredentialLifetimeCheckpointKey,
			).Error
		})
		restoreErr := pinned.Exec("PRAGMA foreign_keys = ON").Error
		return errors.Join(rebuildErr, restoreErr)
	})
	if err != nil {
		t.Fatalf("downgrade populated SQLite foundation fixture: %v", err)
	}
}

func rebuildSQLiteLegacyFoundationTable(
	db *gorm.DB,
	table string,
	removedColumns map[string]struct{},
) error {
	var state sqliteSchemaObject
	if err := db.Raw(`
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
	removedConstraints := make(map[string]struct{})
	for name, definition := range webhookCredentialConstraintDefinitions {
		if definition.table == table {
			removedConstraints[name] = struct{}{}
		}
	}
	for _, definition := range webhookProjectScopeFKDefinitions() {
		if definition.table == table {
			removedConstraints[definition.name] = struct{}{}
		}
	}
	keptParts := make([]string, 0, len(parts))
	copyColumns := make([]string, 0, len(parts))
	for _, part := range parts {
		leading := sqliteDDLLeadingIdentifier(part)
		if _, removed := removedColumns[leading]; removed {
			continue
		}
		first, _, next, identifier := scanSQLiteDDLIdentifier(part, 0)
		if identifier && strings.EqualFold(first, "constraint") {
			name, _, _, named := scanSQLiteDDLIdentifier(part, next)
			if named {
				if _, removed := removedConstraints[name]; removed {
					continue
				}
			}
		} else if identifier &&
			!strings.EqualFold(first, "primary") &&
			!strings.EqualFold(first, "unique") &&
			!strings.EqualFold(first, "check") &&
			!strings.EqualFold(first, "foreign") {
			copyColumns = append(
				copyColumns,
				quoteAutomationWebhookSQLiteIdentifier(leading),
			)
		}
		keptParts = append(keptParts, part)
	}
	temp := table + "__task9a_legacy"
	createSQL := "CREATE TABLE " +
		quoteAutomationWebhookSQLiteIdentifier(temp) +
		" (" + strings.Join(keptParts, ", ") + ")" +
		state.SQL[close+1:]
	if err := db.Exec(createSQL).Error; err != nil {
		return err
	}
	columns := strings.Join(copyColumns, ", ")
	if err := db.Exec(
		"INSERT INTO " + quoteAutomationWebhookSQLiteIdentifier(temp) +
			" (" + columns + ") SELECT " + columns + " FROM " +
			quoteAutomationWebhookSQLiteIdentifier(table),
	).Error; err != nil {
		return err
	}
	if err := db.Exec(
		"DROP TABLE " + quoteAutomationWebhookSQLiteIdentifier(table),
	).Error; err != nil {
		return err
	}
	return db.Exec(
		"ALTER TABLE " +
			quoteAutomationWebhookSQLiteIdentifier(temp) +
			" RENAME TO " +
			quoteAutomationWebhookSQLiteIdentifier(table),
	).Error
}

func assertEverySQLiteConnectionForeignKeysOn(
	t *testing.T,
	db *gorm.DB,
	count int,
) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(count)
	sqlDB.SetMaxIdleConns(count)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connections := make([]*sql.Conn, 0, count)
	defer func() {
		for _, connection := range connections {
			_ = connection.Close()
		}
	}()
	for index := 0; index < count; index++ {
		connection, err := sqlDB.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, connection)
		var enabled int
		if err := connection.QueryRowContext(
			ctx,
			"PRAGMA foreign_keys",
		).Scan(&enabled); err != nil {
			t.Fatal(err)
		}
		if enabled != 1 {
			t.Fatalf(
				"SQLite connection %d foreign_keys = %d, want ON",
				index,
				enabled,
			)
		}
	}
}

func rebuildLegacyOutboxWithNullableStatus(t *testing.T, db *gorm.DB) {
	t.Helper()
	mustExecWebhookCredentialTest(
		t,
		db,
		"ALTER TABLE outbox_deliveries RENAME TO outbox_deliveries_old",
	)
	mustExecWebhookCredentialTest(
		t,
		db,
		`CREATE TABLE outbox_deliveries (
			id TEXT PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			event_id TEXT NOT NULL,
			destination_type TEXT NOT NULL,
			destination_id TEXT NOT NULL,
			status TEXT,
			expires_at DATETIME,
			expired_at DATETIME
		)`,
	)
	mustExecWebhookCredentialTest(
		t,
		db,
		`INSERT INTO outbox_deliveries (
			id, organization_id, project_id, event_id,
			destination_type, destination_id, status, expires_at, expired_at
		)
		SELECT
			id, organization_id, project_id, event_id,
			destination_type, destination_id, status, expires_at, expired_at
		FROM outbox_deliveries_old`,
	)
	mustExecWebhookCredentialTest(t, db, "DROP TABLE outbox_deliveries_old")
}

func TestWebhookCredentialStatusCheckExplicitlyRejectsNull(t *testing.T) {
	expression :=
		webhookCredentialConstraintDefinitions["chk_outbox_delivery_status"].
			expression
	if !strings.Contains(strings.ToLower(expression), "status is not null") {
		t.Fatalf(
			"Outbox status CHECK = %q, want explicit NULL rejection",
			expression,
		)
	}
	statusField, ok := reflectTypeOutboxDeliveryStatus()
	if !ok {
		t.Fatal("OutboxDelivery.Status field is missing")
	}
	assertGORMCheckUsesCanonicalExpression(
		t,
		statusField,
		"chk_outbox_delivery_status",
		expression,
	)
}

func TestWebhookCredentialStatusDefaultNormalizerRejectsTrailingSQL(
	t *testing.T,
) {
	if got := normalizeWebhookStatusDefault(
		"'pending'::text || 'unexpected'",
	); got == "pending" {
		t.Fatal("status default normalizer truncated non-canonical SQL")
	}
}

func reflectTypeOutboxDeliveryStatus() (string, bool) {
	field, ok := reflect.TypeOf(models.OutboxDelivery{}).FieldByName("Status")
	if !ok {
		return "", false
	}
	return field.Tag.Get("gorm"), true
}
