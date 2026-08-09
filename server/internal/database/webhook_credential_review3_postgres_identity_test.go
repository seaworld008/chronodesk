package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

func exercisePostgresWebhookIdentityNullCatalog(
	t *testing.T,
	admin *gorm.DB,
	owner *gorm.DB,
	runtime *gorm.DB,
	quotedSchema string,
	deadline time.Time,
) {
	t.Helper()
	table := func(name string) string {
		return quotedSchema + "." + quotePostgresRLSTestIdentifier(name)
	}
	nullEventID := "00000000-0000-4000-8000-000000000941"
	nullSnapshotID := "00000000-0000-7000-8000-000000000942"
	nullDeliveryID := "00000000-0000-4000-8000-000000000943"
	for _, statement := range []string{
		"ALTER TABLE " + table("webhook_delivery_snapshots") +
			" DROP CONSTRAINT chk_webhook_snapshot_scope",
		"ALTER TABLE " + table("domain_events") +
			" ALTER COLUMN organization_id DROP NOT NULL",
		"ALTER TABLE " + table("webhook_delivery_snapshots") +
			" ALTER COLUMN organization_id DROP NOT NULL",
		"ALTER TABLE " + table("outbox_deliveries") +
			" ALTER COLUMN organization_id DROP NOT NULL",
	} {
		if err := admin.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := admin.Exec(
		"INSERT INTO "+table("domain_events")+
			" (id, organization_id, project_id) VALUES (?, NULL, 22)",
		nullEventID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Exec(
		"INSERT INTO "+table("webhook_delivery_snapshots")+" ("+
			"id, created_at, organization_id, project_id, config_id, "+
			"event_id, credential_expires_at"+
			") VALUES (?, ?, NULL, 22, 94, ?, ?)",
		nullSnapshotID,
		time.Now().UTC(),
		nullEventID,
		deadline,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Exec(
		"INSERT INTO "+table("outbox_deliveries")+" ("+
			"id, organization_id, project_id, event_id, "+
			"destination_type, destination_id, status, expires_at"+
			") VALUES (?, NULL, 22, ?, 'webhook', ?, 'pending', ?)",
		nullDeliveryID,
		nullEventID,
		"snapshot:"+nullSnapshotID,
		deadline,
	).Error; err != nil {
		t.Fatal(err)
	}
	cutoverErr := migrateWebhookSnapshotCredentialLifetimeContractAt(
		owner,
		time.Now().UTC(),
	)
	if cutoverErr == nil ||
		!strings.Contains(
			cutoverErr.Error(),
			"domain_events.organization_id contains NULL identity",
		) {
		t.Fatalf("owner NULL identity audit error = %v", cutoverErr)
	}
	err := validateWebhookCredentialLifetimeCatalog(runtime)
	if err == nil ||
		!strings.Contains(
			err.Error(),
			"domain_events.organization_id",
		) {
		t.Fatalf("runtime nullable identity catalog error = %v", err)
	}
	if err := ValidateWebhookSnapshotCredentialLifetimeRuntimeData(
		context.Background(),
		runtime,
	); err != nil {
		t.Fatalf(
			"runtime data-only gate should demonstrate RLS-hidden NULL pair: %v",
			err,
		)
	}
	for _, statement := range []string{
		"DELETE FROM " + table("outbox_deliveries") +
			" WHERE id = " + quotePostgresRLSTestLiteral(nullDeliveryID),
		"DELETE FROM " + table("webhook_delivery_snapshots") +
			" WHERE id = " + quotePostgresRLSTestLiteral(nullSnapshotID),
		"DELETE FROM " + table("domain_events") +
			" WHERE id = " + quotePostgresRLSTestLiteral(nullEventID),
		"ALTER TABLE " + table("domain_events") +
			" ALTER COLUMN organization_id SET NOT NULL",
		"ALTER TABLE " + table("webhook_delivery_snapshots") +
			" ALTER COLUMN organization_id SET NOT NULL",
		"ALTER TABLE " + table("outbox_deliveries") +
			" ALTER COLUMN organization_id SET NOT NULL",
		"ALTER TABLE " + table("webhook_delivery_snapshots") +
			" ADD CONSTRAINT chk_webhook_snapshot_scope CHECK (" +
			webhookCredentialConstraintDefinitions["chk_webhook_snapshot_scope"].expression +
			")",
	} {
		if err := admin.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := validateWebhookCredentialLifetimeCatalog(runtime); err != nil {
		t.Fatalf("restored PostgreSQL identity catalog: %v", err)
	}
	if err := admin.Exec(
		"ALTER TABLE " + table("outbox_deliveries") +
			" ALTER COLUMN destination_type DROP NOT NULL",
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Exec(
		"UPDATE "+table("outbox_deliveries")+
			" SET destination_type = NULL WHERE id = ?",
		legacyWebhookDeliveryID,
	).Error; err != nil {
		t.Fatal(err)
	}
	cutoverErr = migrateWebhookSnapshotCredentialLifetimeContractAt(
		owner,
		time.Now().UTC(),
	)
	if cutoverErr == nil ||
		!strings.Contains(
			cutoverErr.Error(),
			"outbox_deliveries.destination_type contains NULL identity",
		) {
		t.Fatalf("owner NULL destination audit error = %v", cutoverErr)
	}
	err = validateWebhookCredentialLifetimeCatalog(runtime)
	if err == nil ||
		!strings.Contains(
			err.Error(),
			"outbox_deliveries.destination_type",
		) {
		t.Fatalf("runtime nullable destination catalog error = %v", err)
	}
	if err := admin.Exec(
		"UPDATE "+table("outbox_deliveries")+
			" SET destination_type = 'webhook' WHERE id = ?",
		legacyWebhookDeliveryID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Exec(
		"ALTER TABLE " + table("outbox_deliveries") +
			" ALTER COLUMN destination_type SET NOT NULL",
	).Error; err != nil {
		t.Fatal(err)
	}
	rawNullErr := admin.Exec(
		"UPDATE "+table("domain_events")+
			" SET organization_id = NULL WHERE id = ?",
		legacyWebhookEventID,
	).Error
	if rawNullErr == nil ||
		!strings.Contains(rawNullErr.Error(), "SQLSTATE 23502") {
		t.Fatalf("canonical PostgreSQL identity accepted raw NULL: %v", rawNullErr)
	}
	if err := ValidateWebhookSnapshotCredentialLifetimeContract(owner); err != nil {
		t.Fatalf("restored owner identity contract: %v", err)
	}
}

func exercisePostgresWebhookUUIDVariants(
	t *testing.T,
	owner *gorm.DB,
	runtime *gorm.DB,
) {
	t.Helper()
	scope := models.ProjectScope{OrganizationID: 11, ProjectID: 22}
	tests := []struct {
		name       string
		reservedID string
		mutate     func(*gorm.DB, string) error
		restore    func(*gorm.DB) error
	}{
		{
			name:       "snapshot",
			reservedID: "00000000-0000-7000-0000-000000000902",
			mutate: func(tx *gorm.DB, reserved string) error {
				if err := tx.Exec(`
					UPDATE webhook_delivery_snapshots SET id = ?
					WHERE id = ?
				`, reserved, legacyWebhookSnapshotID).Error; err != nil {
					return err
				}
				return tx.Exec(`
					UPDATE outbox_deliveries SET destination_id = ?
					WHERE id = ?
				`, "snapshot:"+reserved, legacyWebhookDeliveryID).Error
			},
			restore: func(tx *gorm.DB) error {
				if err := tx.Exec(`
					UPDATE webhook_delivery_snapshots SET id = ?
					WHERE id = '00000000-0000-7000-0000-000000000902'
				`, legacyWebhookSnapshotID).Error; err != nil {
					return err
				}
				return tx.Exec(`
					UPDATE outbox_deliveries SET destination_id = ?
					WHERE id = ?
				`, "snapshot:"+legacyWebhookSnapshotID, legacyWebhookDeliveryID).Error
			},
		},
		{
			name:       "event",
			reservedID: "00000000-0000-4000-0000-000000000901",
			mutate: func(tx *gorm.DB, reserved string) error {
				if err := tx.Exec(`
					UPDATE domain_events SET id = ? WHERE id = ?
				`, reserved, legacyWebhookEventID).Error; err != nil {
					return err
				}
				if err := tx.Exec(`
					UPDATE webhook_delivery_snapshots SET event_id = ?
					WHERE id = ?
				`, reserved, legacyWebhookSnapshotID).Error; err != nil {
					return err
				}
				return tx.Exec(`
					UPDATE outbox_deliveries SET event_id = ? WHERE id = ?
				`, reserved, legacyWebhookDeliveryID).Error
			},
			restore: func(tx *gorm.DB) error {
				reserved := "00000000-0000-4000-0000-000000000901"
				if err := tx.Exec(`
					UPDATE domain_events SET id = ? WHERE id = ?
				`, legacyWebhookEventID, reserved).Error; err != nil {
					return err
				}
				if err := tx.Exec(`
					UPDATE webhook_delivery_snapshots SET event_id = ?
					WHERE id = ?
				`, legacyWebhookEventID, legacyWebhookSnapshotID).Error; err != nil {
					return err
				}
				return tx.Exec(`
					UPDATE outbox_deliveries SET event_id = ? WHERE id = ?
				`, legacyWebhookEventID, legacyWebhookDeliveryID).Error
			},
		},
		{
			name:       "delivery",
			reservedID: "00000000-0000-4000-f000-000000000903",
			mutate: func(tx *gorm.DB, reserved string) error {
				return tx.Exec(`
					UPDATE outbox_deliveries SET id = ? WHERE id = ?
				`, reserved, legacyWebhookDeliveryID).Error
			},
			restore: func(tx *gorm.DB) error {
				return tx.Exec(`
					UPDATE outbox_deliveries SET id = ?
					WHERE id = '00000000-0000-4000-f000-000000000903'
				`, legacyWebhookDeliveryID).Error
			},
		},
	}
	for _, test := range tests {
		t.Run("postgres reserved UUID "+test.name, func(t *testing.T) {
			if err := WithProjectScopeTransaction(
				context.Background(),
				owner,
				scope,
				func(tx *gorm.DB) error {
					return test.mutate(tx, test.reservedID)
				},
			); err != nil {
				t.Fatal(err)
			}
			ownerErr := ValidateWebhookSnapshotCredentialLifetimeContract(owner)
			if ownerErr == nil ||
				!strings.Contains(ownerErr.Error(), test.reservedID) {
				t.Fatalf("owner UUID variant error = %v", ownerErr)
			}
			runtimeErr := ValidateWebhookSnapshotCredentialLifetimeRuntimeData(
				context.Background(),
				runtime,
			)
			if runtimeErr == nil ||
				!strings.Contains(runtimeErr.Error(), test.reservedID) {
				t.Fatalf("runtime UUID variant error = %v", runtimeErr)
			}
			if err := WithProjectScopeTransaction(
				context.Background(),
				owner,
				scope,
				func(tx *gorm.DB) error {
					return test.restore(tx)
				},
			); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func exercisePostgresIdentityColumnContractMatrix(
	t *testing.T,
	owner *gorm.DB,
) {
	t.Helper()
	rollback := errors.New("rollback identity contract mutation")
	if err := owner.Connection(func(pinned *gorm.DB) error {
		for _, statement := range []string{
			`CREATE TEMP TABLE domain_events (
				id VARCHAR(36) NOT NULL PRIMARY KEY,
				organization_id BIGINT NOT NULL,
				project_id BIGINT NOT NULL
			)`,
			`CREATE TEMP TABLE webhook_delivery_snapshots (
				id VARCHAR(36) NOT NULL PRIMARY KEY,
				organization_id BIGINT NOT NULL,
				project_id BIGINT NOT NULL,
				event_id VARCHAR(64) NOT NULL
			)`,
			`CREATE TEMP TABLE outbox_deliveries (
				id VARCHAR(36) NOT NULL PRIMARY KEY,
				organization_id BIGINT NOT NULL,
				project_id BIGINT NOT NULL,
				event_id VARCHAR(36) NOT NULL,
				destination_type VARCHAR(50) NOT NULL,
				destination_id VARCHAR(128) NOT NULL
			)`,
			"SET search_path TO pg_temp",
		} {
			if err := pinned.Exec(statement).Error; err != nil {
				return err
			}
		}
		defer func() {
			for _, table := range []string{
				"outbox_deliveries",
				"webhook_delivery_snapshots",
				"domain_events",
			} {
				if err := pinned.Exec(
					"DROP TABLE IF EXISTS pg_temp." + table + " CASCADE",
				).Error; err != nil {
					t.Errorf(
						"drop PostgreSQL identity matrix table %s: %v",
						table,
						err,
					)
				}
			}
			if err := pinned.Exec("RESET search_path").Error; err != nil {
				t.Errorf("reset PostgreSQL identity matrix search_path: %v", err)
			}
		}()
		if err := validateWebhookCredentialIdentityColumnContract(pinned); err != nil {
			return fmt.Errorf("exact PostgreSQL identity fixture: %w", err)
		}
		tests := []struct {
			name     string
			mutation string
		}{
			{
				name: "nullable scope",
				mutation: "ALTER TABLE domain_events " +
					"ALTER COLUMN organization_id DROP NOT NULL",
			},
			{
				name: "wrong length",
				mutation: "ALTER TABLE webhook_delivery_snapshots " +
					"ALTER COLUMN event_id TYPE VARCHAR(63)",
			},
			{
				name: "identity default",
				mutation: "ALTER TABLE outbox_deliveries " +
					"ALTER COLUMN event_id SET DEFAULT " +
					"'00000000-0000-4000-8000-000000000001'",
			},
			{
				name: "generated identity field",
				mutation: "ALTER TABLE outbox_deliveries " +
					"DROP COLUMN destination_id; " +
					"ALTER TABLE outbox_deliveries ADD COLUMN " +
					"destination_id VARCHAR(128) GENERATED ALWAYS AS " +
					"(event_id) STORED",
			},
			{
				name: "database identity field",
				mutation: "ALTER TABLE outbox_deliveries " +
					"DROP COLUMN project_id; " +
					"ALTER TABLE outbox_deliveries ADD COLUMN " +
					"project_id BIGINT GENERATED ALWAYS AS IDENTITY",
			},
			{
				name: "composite primary key",
				mutation: "ALTER TABLE domain_events " +
					"DROP CONSTRAINT domain_events_pkey; " +
					"ALTER TABLE domain_events ADD PRIMARY KEY " +
					"(id, organization_id)",
			},
		}
		for _, test := range tests {
			t.Run("postgres identity "+test.name, func(t *testing.T) {
				err := pinned.Transaction(func(tx *gorm.DB) error {
					if err := tx.Exec(test.mutation).Error; err != nil {
						return err
					}
					if err := validateWebhookCredentialIdentityColumnContract(
						tx,
					); err == nil {
						return errors.New(
							"unsafe PostgreSQL identity contract was accepted",
						)
					}
					return rollback
				})
				if !errors.Is(err, rollback) {
					t.Fatalf("PostgreSQL identity mutation %q: %v", test.name, err)
				}
			})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func exercisePostgresWebhookTextCollations(
	t *testing.T,
	owner *gorm.DB,
	runtime *gorm.DB,
	schemaName string,
) {
	t.Helper()
	collationName := "task9a_nondeterministic_ci"
	qualifiedCollation :=
		quotePostgresRLSTestIdentifier(schemaName) + "." +
			quotePostgresRLSTestIdentifier(collationName)
	if err := owner.Exec(
		"CREATE COLLATION " + qualifiedCollation +
			" (provider = icu, locale = 'und-u-ks-level1', " +
			"deterministic = false)",
	).Error; err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := owner.Exec(
			"DROP COLLATION " + qualifiedCollation,
		).Error; err != nil {
			t.Errorf("drop Review-3 PostgreSQL collation: %v", err)
		}
	}()
	scope := models.ProjectScope{OrganizationID: 11, ProjectID: 22}
	tests := []struct {
		name    string
		column  string
		alter   string
		mutate  string
		restore string
	}{
		{
			name:   "status",
			column: "status",
			alter: "ALTER TABLE outbox_deliveries ALTER COLUMN status " +
				"TYPE VARCHAR(20) COLLATE " + qualifiedCollation,
			mutate: "UPDATE outbox_deliveries SET status = 'PENDING' " +
				"WHERE id = '" + legacyWebhookDeliveryID + "'",
			restore: "UPDATE outbox_deliveries SET status = 'pending' " +
				"WHERE id = '" + legacyWebhookDeliveryID + "'; " +
				"ALTER TABLE outbox_deliveries ALTER COLUMN status " +
				`TYPE VARCHAR(20) COLLATE pg_catalog."default"`,
		},
		{
			name:   "credential reason",
			column: "credential_shred_reason",
			alter: "ALTER TABLE webhook_delivery_snapshots " +
				"ALTER COLUMN credential_shred_reason " +
				"TYPE VARCHAR(20) COLLATE " + qualifiedCollation,
			mutate: "UPDATE webhook_delivery_snapshots SET " +
				"credential_shredded_at = CURRENT_TIMESTAMP, " +
				"credential_shred_reason = 'SUCCEEDED', " +
				"secret = '', previous_secret = '', access_token = '' " +
				"WHERE id = '" + legacyWebhookSnapshotID + "'",
			restore: "UPDATE webhook_delivery_snapshots SET " +
				"credential_shredded_at = NULL, " +
				"credential_shred_reason = NULL " +
				"WHERE id = '" + legacyWebhookSnapshotID + "'; " +
				"ALTER TABLE webhook_delivery_snapshots " +
				"ALTER COLUMN credential_shred_reason " +
				`TYPE VARCHAR(20) COLLATE pg_catalog."default"`,
		},
		{
			name:   "accent-equivalent status",
			column: "status",
			alter: "ALTER TABLE outbox_deliveries ALTER COLUMN status " +
				"TYPE VARCHAR(20) COLLATE " + qualifiedCollation,
			mutate: "UPDATE outbox_deliveries SET status = 'pénding' " +
				"WHERE id = '" + legacyWebhookDeliveryID + "'",
			restore: "UPDATE outbox_deliveries SET status = 'pending' " +
				"WHERE id = '" + legacyWebhookDeliveryID + "'; " +
				"ALTER TABLE outbox_deliveries ALTER COLUMN status " +
				`TYPE VARCHAR(20) COLLATE pg_catalog."default"`,
		},
		{
			name:   "destination type",
			column: "destination_type",
			alter: "ALTER TABLE outbox_deliveries " +
				"ALTER COLUMN destination_type " +
				"TYPE VARCHAR(50) COLLATE " + qualifiedCollation,
			mutate: "UPDATE outbox_deliveries " +
				"SET destination_type = 'WEBHOOK' " +
				"WHERE id = '" + legacyWebhookDeliveryID + "'",
			restore: "UPDATE outbox_deliveries " +
				"SET destination_type = 'webhook' " +
				"WHERE id = '" + legacyWebhookDeliveryID + "'; " +
				"ALTER TABLE outbox_deliveries " +
				"ALTER COLUMN destination_type " +
				`TYPE VARCHAR(50) COLLATE pg_catalog."default"`,
		},
		{
			name:   "project status",
			column: "projects.status",
			alter: "ALTER TABLE projects ALTER COLUMN status " +
				"TYPE VARCHAR(20) COLLATE " + qualifiedCollation,
			mutate: "UPDATE projects SET status = 'ACTIVE' " +
				"WHERE id = 22",
			restore: "UPDATE projects SET status = 'active' " +
				"WHERE id = 22; " +
				"ALTER TABLE projects ALTER COLUMN status " +
				`TYPE VARCHAR(20) COLLATE pg_catalog."default"`,
		},
	}
	for _, test := range tests {
		t.Run("postgres nondeterministic "+test.name, func(t *testing.T) {
			if err := owner.Exec(test.alter).Error; err != nil {
				t.Fatal(err)
			}
			if err := WithProjectScopeTransaction(
				context.Background(),
				owner,
				scope,
				func(tx *gorm.DB) error {
					return tx.Exec(test.mutate).Error
				},
			); err != nil {
				t.Fatalf("direct SQL under nondeterministic collation: %v", err)
			}
			if err := withWebhookCredentialOwnerAccess(
				owner,
				func(tx *gorm.DB) error {
					return validateWebhookCredentialOwnerSet(tx, true)
				},
			); err != nil {
				t.Fatalf(
					"nondeterministic fixture should demonstrate catalog-first gate: %v",
					err,
				)
			}
			err := validateWebhookCredentialLifetimeCatalog(runtime)
			if err == nil ||
				!strings.Contains(
					strings.ToLower(err.Error()),
					"collation",
				) {
				t.Fatalf(
					"runtime nondeterministic %s catalog error = %v",
					test.column,
					err,
				)
			}
			if err := WithProjectScopeTransaction(
				context.Background(),
				owner,
				scope,
				func(tx *gorm.DB) error {
					return tx.Exec(test.restore).Error
				},
			); err != nil {
				t.Fatalf("restore PostgreSQL %s collation: %v", test.column, err)
			}
			if err := validateWebhookCredentialLifetimeCatalog(runtime); err != nil {
				t.Fatalf("restored PostgreSQL %s catalog: %v", test.column, err)
			}
		})
	}
}

func exercisePostgresProjectScopeFKCatalogMatrix(
	t *testing.T,
	owner *gorm.DB,
	schemaName string,
) {
	t.Helper()
	_ = schemaName
	tests := []struct {
		name       string
		setup      []string
		cleanup    []string
		wantExists bool
	}{
		{
			name: "reversed two columns",
			setup: []string{
				"CREATE UNIQUE INDEX task9a_projects_reverse_scope " +
					"ON projects(id, organization_id)",
				"ALTER TABLE domain_events " +
					"ADD CONSTRAINT fk_domain_events_project_scope " +
					"FOREIGN KEY (project_id, organization_id) " +
					"REFERENCES projects(id, organization_id) " +
					"ON UPDATE RESTRICT ON DELETE RESTRICT",
			},
			cleanup: []string{
				"ALTER TABLE domain_events DROP CONSTRAINT " +
					"fk_domain_events_project_scope",
				"DROP INDEX task9a_projects_reverse_scope",
			},
			wantExists: true,
		},
		{
			name: "three columns",
			setup: []string{
				"ALTER TABLE projects ADD COLUMN task9a_scope_marker " +
					"BIGINT NOT NULL DEFAULT 0",
				"ALTER TABLE domain_events ADD COLUMN task9a_scope_marker " +
					"BIGINT NOT NULL DEFAULT 0",
				"CREATE UNIQUE INDEX task9a_projects_three_scope " +
					"ON projects(organization_id, id, task9a_scope_marker)",
				"ALTER TABLE domain_events " +
					"ADD CONSTRAINT fk_domain_events_project_scope " +
					"FOREIGN KEY (" +
					"organization_id, project_id, task9a_scope_marker" +
					") REFERENCES projects(" +
					"organization_id, id, task9a_scope_marker" +
					") ON UPDATE RESTRICT ON DELETE RESTRICT",
			},
			cleanup: []string{
				"ALTER TABLE domain_events DROP CONSTRAINT " +
					"fk_domain_events_project_scope",
				"DROP INDEX task9a_projects_three_scope",
				"ALTER TABLE domain_events DROP COLUMN task9a_scope_marker",
				"ALTER TABLE projects DROP COLUMN task9a_scope_marker",
			},
			wantExists: true,
		},
		{
			name: "wrong delete action",
			setup: []string{
				"ALTER TABLE domain_events " +
					"ADD CONSTRAINT fk_domain_events_project_scope " +
					"FOREIGN KEY (organization_id, project_id) " +
					"REFERENCES projects(organization_id, id) " +
					"ON UPDATE RESTRICT ON DELETE CASCADE",
			},
			cleanup: []string{
				"ALTER TABLE domain_events DROP CONSTRAINT " +
					"fk_domain_events_project_scope",
			},
			wantExists: true,
		},
		{
			name: "not valid",
			setup: []string{
				"ALTER TABLE domain_events " +
					"ADD CONSTRAINT fk_domain_events_project_scope " +
					"FOREIGN KEY (organization_id, project_id) " +
					"REFERENCES projects(organization_id, id) " +
					"ON UPDATE RESTRICT ON DELETE RESTRICT NOT VALID",
			},
			cleanup: []string{
				"ALTER TABLE domain_events DROP CONSTRAINT " +
					"fk_domain_events_project_scope",
			},
			wantExists: true,
		},
		{
			name: "deferrable",
			setup: []string{
				"ALTER TABLE domain_events " +
					"ADD CONSTRAINT fk_domain_events_project_scope " +
					"FOREIGN KEY (organization_id, project_id) " +
					"REFERENCES projects(organization_id, id) " +
					"ON UPDATE RESTRICT ON DELETE RESTRICT " +
					"DEFERRABLE INITIALLY IMMEDIATE",
			},
			cleanup: []string{
				"ALTER TABLE domain_events DROP CONSTRAINT " +
					"fk_domain_events_project_scope",
			},
			wantExists: true,
		},
		{
			name: "wrong schema child",
			setup: []string{
				"CREATE TEMP TABLE projects (" +
					"id BIGINT PRIMARY KEY, organization_id BIGINT NOT NULL, " +
					"UNIQUE (organization_id, id))",
				"CREATE TEMP TABLE domain_events (" +
					"id VARCHAR(36) PRIMARY KEY, " +
					"organization_id BIGINT NOT NULL, " +
					"project_id BIGINT NOT NULL)",
				"ALTER TABLE pg_temp.domain_events " +
					"ADD CONSTRAINT fk_domain_events_project_scope " +
					"FOREIGN KEY (organization_id, project_id) " +
					"REFERENCES pg_temp.projects(organization_id, id) " +
					"ON UPDATE RESTRICT ON DELETE RESTRICT",
			},
			cleanup: []string{
				"DROP TABLE pg_temp.domain_events",
				"DROP TABLE pg_temp.projects",
			},
			wantExists: false,
		},
	}
	for _, test := range tests {
		t.Run("postgres FK "+test.name, func(t *testing.T) {
			err := withWebhookCredentialOwnerAccess(
				owner,
				func(tx *gorm.DB) error {
					if err := tx.Exec(`
						ALTER TABLE domain_events
						DROP CONSTRAINT fk_domain_events_project_scope
					`).Error; err != nil {
						return err
					}
					for _, statement := range test.setup {
						if err := tx.Exec(statement).Error; err != nil {
							return err
						}
					}
					valid, exists, err :=
						postgresWebhookProjectScopeFKState(
							tx,
							webhookProjectScopeFKDefinitions()[0],
						)
					if err != nil {
						return err
					}
					if exists != test.wantExists || valid {
						return fmt.Errorf(
							"FK state valid=%t exists=%t, want valid=false exists=%t",
							valid,
							exists,
							test.wantExists,
						)
					}
					for _, statement := range test.cleanup {
						if err := tx.Exec(statement).Error; err != nil {
							return err
						}
					}
					return tx.Exec(`
						ALTER TABLE domain_events
						ADD CONSTRAINT fk_domain_events_project_scope
						FOREIGN KEY (organization_id, project_id)
						REFERENCES projects(organization_id, id)
						ON UPDATE RESTRICT ON DELETE RESTRICT
					`).Error
				},
			)
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
