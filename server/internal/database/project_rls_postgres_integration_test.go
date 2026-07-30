package database

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPostgresProjectRLSIntegration(t *testing.T) {
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set CHRONODESK_POSTGRES_INTEGRATION=1 for the isolated PostgreSQL RLS test")
	}
	rawDSN := strings.TrimSpace(os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"))
	if rawDSN == "" {
		t.Fatal("CHRONODESK_POSTGRES_INTEGRATION_DSN is required")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatalf("parse integration DSN: %v", err)
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal("project RLS integration test requires a loopback PostgreSQL target")
		}
	}

	admin, err := gorm.Open(postgres.Open(rawDSN), &gorm.Config{
		TranslateError: true,
	})
	if err != nil {
		t.Fatalf("open integration PostgreSQL: %v", err)
	}

	suffix := time.Now().UnixNano()
	schemaName := fmt.Sprintf("chronodesk_rls_%d", suffix)
	roleName := fmt.Sprintf("chronodesk_rls_runtime_%d", suffix)
	quotedSchema := quotePostgresRLSTestIdentifier(schemaName)
	quotedRole := quotePostgresRLSTestIdentifier(roleName)
	roleCreated := false

	if err := admin.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := admin.Exec("DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE").Error; cleanupErr != nil {
			t.Errorf("drop isolated schema: %v", cleanupErr)
		}
		if roleCreated {
			if cleanupErr := admin.Exec("DROP ROLE IF EXISTS " + quotedRole).Error; cleanupErr != nil {
				t.Errorf("drop isolated runtime role: %v", cleanupErr)
			}
		}
	})

	query := parsed.Query()
	query.Set("search_path", schemaName)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{
		TranslateError: true,
	})
	if err != nil {
		t.Fatalf("open isolated PostgreSQL schema: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("open isolated PostgreSQL pool: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := sqlDB.Close(); closeErr != nil {
			t.Errorf("close isolated PostgreSQL pool: %v", closeErr)
		}
	})

	if err := db.Exec(`
		CREATE TABLE projects (
			id BIGINT PRIMARY KEY,
			organization_id BIGINT NOT NULL,
			name TEXT NOT NULL
		)
	`).Error; err != nil {
		t.Fatalf("create project table: %v", err)
	}
	if err := db.Exec(`
		CREATE TABLE tickets (
			id BIGINT PRIMARY KEY,
			public_id VARCHAR(36) NOT NULL,
			organization_id BIGINT NOT NULL,
			project_id BIGINT NOT NULL,
			queue_id BIGINT NOT NULL,
			request_type_version_id VARCHAR(36) NOT NULL,
			workflow_version_id VARCHAR(36) NOT NULL,
			title TEXT NOT NULL
		)
	`).Error; err != nil {
		t.Fatalf("create project-owned table: %v", err)
	}
	for _, tableName := range ProjectRLSProtectedTables() {
		if tableName == "tickets" {
			continue
		}
		if err := db.Exec(fmt.Sprintf(
			"CREATE TABLE %s (id BIGINT PRIMARY KEY, organization_id BIGINT NOT NULL, project_id BIGINT NOT NULL)",
			quoteStaticProjectRLSIdentifier(tableName),
		)).Error; err != nil {
			t.Fatalf("create project-owned table %s: %v", tableName, err)
		}
	}
	if err := db.Exec(`
		INSERT INTO projects (id, organization_id, name)
		VALUES
			(100, 10, 'project-a'),
			(200, 10, 'project-b'),
			(300, 20, 'project-c')
	`).Error; err != nil {
		t.Fatalf("seed projects: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO tickets (
			id,
			public_id,
			organization_id,
			project_id,
			queue_id,
			request_type_version_id,
			workflow_version_id,
			title
		)
		VALUES
			(
				1,
				'00000000-0000-7000-8000-000000000001',
				10,
				100,
				1000,
				'00000000-0000-7000-8000-000000000101',
				'00000000-0000-7000-8000-000000000201',
				'project-a'
			),
			(
				2,
				'00000000-0000-7000-8000-000000000002',
				10,
				200,
				2000,
				'00000000-0000-7000-8000-000000000102',
				'00000000-0000-7000-8000-000000000202',
				'project-b'
			)
	`).Error; err != nil {
		t.Fatalf("seed cross-project rows: %v", err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		if err := MigrateProjectRLS(db); err != nil {
			t.Fatalf("project RLS migration attempt %d: %v", attempt, err)
		}
	}
	if err := validatePostgresProjectRLSState(db, false); err != nil {
		t.Fatalf("validate installed project RLS: %v", err)
	}
	if err := ValidateProjectRLSRuntime(db); err == nil ||
		!strings.Contains(err.Error(), "RLS disabled") {
		t.Fatalf("runtime gate accepted policy before explicit enablement: %v", err)
	}

	if err := db.Exec(`
		CREATE POLICY rogue_allow_all ON tickets
		FOR ALL TO PUBLIC USING (true) WITH CHECK (true)
	`).Error; err != nil {
		t.Fatalf("create unexpected policy fixture: %v", err)
	}
	if err := EnableProjectRLS(db); err == nil ||
		!strings.Contains(err.Error(), "expected exactly one policy") {
		t.Fatalf("enable gate accepted an unexpected permissive policy: %v", err)
	}
	var enabledAfterRollback bool
	if err := db.Raw(`
		SELECT relrowsecurity
		FROM pg_class
		WHERE oid = 'tickets'::regclass
	`).Scan(&enabledAfterRollback).Error; err != nil {
		t.Fatalf("inspect rolled-back enablement: %v", err)
	}
	if enabledAfterRollback {
		t.Fatal("failed RLS enablement did not roll back ENABLE ROW LEVEL SECURITY")
	}
	if err := db.Exec(`DROP POLICY rogue_allow_all ON tickets`).Error; err != nil {
		t.Fatalf("remove unexpected policy fixture: %v", err)
	}

	if err := EnableProjectRLS(db); err != nil {
		t.Fatalf("enable project RLS: %v", err)
	}
	if err := ValidateProjectRLSRuntime(db); err != nil {
		t.Fatalf("validate enabled project RLS: %v", err)
	}

	if err := db.Exec(`ALTER TABLE tickets NO FORCE ROW LEVEL SECURITY`).Error; err != nil {
		t.Fatalf("weaken FORCE RLS fixture: %v", err)
	}
	if err := validatePostgresProjectRLSState(db, true); err == nil ||
		!strings.Contains(err.Error(), "FORCE RLS disabled") {
		t.Fatalf("runtime gate accepted weakened FORCE RLS state: %v", err)
	}
	if err := EnableProjectRLS(db); err != nil {
		t.Fatalf("restore project RLS: %v", err)
	}

	if err := ValidateProjectRuntimeRole(db); err == nil ||
		!strings.Contains(err.Error(), "can bypass project RLS") {
		t.Fatalf("runtime gate accepted the table owner or privileged migration role: %v", err)
	}

	rolePassword := fmt.Sprintf("ChronodeskRLS%d", suffix)
	if err := admin.Exec(
		"CREATE ROLE " + quotedRole +
			" LOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS PASSWORD " +
			quotePostgresRLSTestLiteral(rolePassword),
	).Error; err != nil {
		t.Fatalf("create least-privilege runtime role: %v", err)
	}
	roleCreated = true
	if err := admin.Exec("GRANT " + quotedRole + " TO CURRENT_USER").Error; err != nil {
		t.Fatalf("allow integration session to SET ROLE: %v", err)
	}
	if err := db.Exec("GRANT USAGE ON SCHEMA " + quotedSchema + " TO " + quotedRole).Error; err != nil {
		t.Fatalf("grant runtime schema access: %v", err)
	}
	if err := db.Exec(
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA " +
			quotedSchema + " TO " + quotedRole,
	).Error; err != nil {
		t.Fatalf("grant runtime table access: %v", err)
	}

	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL ROLE " + quotedRole).Error; err != nil {
			return fmt.Errorf("set deceptive runtime role fixture: %w", err)
		}
		roleErr := ValidateProjectRuntimeRole(tx)
		if roleErr == nil ||
			!strings.Contains(roleErr.Error(), "SESSION_USER") {
			return fmt.Errorf("SET ROLE session bypass was accepted: %v", roleErr)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify SET ROLE session is rejected: %v", err)
	}

	runtimeParsed := *parsed
	runtimeParsed.User = url.UserPassword(roleName, rolePassword)
	runtimeDB, err := gorm.Open(postgres.Open(runtimeParsed.String()), &gorm.Config{
		TranslateError: true,
	})
	if err != nil {
		t.Fatalf("open least-privilege runtime PostgreSQL: %v", err)
	}
	runtimeSQLDB, err := runtimeDB.DB()
	if err != nil {
		t.Fatalf("open least-privilege runtime pool: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := runtimeSQLDB.Close(); closeErr != nil {
			t.Errorf("close least-privilege runtime pool: %v", closeErr)
		}
	})

	if err := ValidateProjectRLSRuntime(runtimeDB); err != nil {
		t.Fatalf("least-privilege runtime RLS validation: %v", err)
	}
	if err := ValidateProjectRuntimeRole(runtimeDB); err != nil {
		t.Fatalf("least-privilege runtime role validation: %v", err)
	}
	if err := admin.Exec("ALTER ROLE " + quotedRole + " NOLOGIN").Error; err != nil {
		t.Fatalf("disable runtime role login: %v", err)
	}
	if err := ValidateProjectRuntimeRole(runtimeDB); err == nil ||
		!strings.Contains(err.Error(), "NOLOGIN") {
		t.Fatalf("runtime gate accepted a NOLOGIN identity: %v", err)
	}
	if err := admin.Exec("ALTER ROLE " + quotedRole + " LOGIN").Error; err != nil {
		t.Fatalf("restore runtime role login: %v", err)
	}
	if err := InstallProjectScopeTransactionRouting(runtimeDB); err != nil {
		t.Fatalf("install project scope transaction routing: %v", err)
	}

	if err := WithProjectScopeContextTransaction(
		context.Background(),
		runtimeDB,
		models.ProjectScope{OrganizationID: 10, ProjectID: 100},
		func(scopedContext context.Context) error {
			var ids []int64
			if err := runtimeDB.WithContext(scopedContext).
				Table("tickets").
				Order("id").
				Pluck("id", &ids).Error; err != nil {
				return fmt.Errorf(
					"query root handle through scoped context: %w",
					err,
				)
			}
			if len(ids) != 1 || ids[0] != 1 {
				return fmt.Errorf(
					"scoped root handle returned ticket ids %v",
					ids,
				)
			}
			return TransactionForContext(
				scopedContext,
				runtimeDB,
				func(tx *gorm.DB) error {
					return tx.Exec(`
						INSERT INTO tickets (
							id,
							public_id,
							organization_id,
							project_id,
							queue_id,
							request_type_version_id,
							workflow_version_id,
							title
						)
						VALUES (?, ?, ?, ?, ?, ?, ?, ?)
					`,
						5,
						"00000000-0000-7000-8000-000000000005",
						10,
						100,
						1000,
						"00000000-0000-7000-8000-000000000101",
						"00000000-0000-7000-8000-000000000201",
						"project-a-context",
					).Error
				},
			)
		},
	); err != nil {
		t.Fatalf("root handle project scope transaction: %v", err)
	}

	if err := WithAuthorizedProjectSetContextTransaction(
		context.Background(),
		runtimeDB,
		10,
		[]uint{200, 100},
		func(scopedContext context.Context) error {
			var ids []int64
			if err := runtimeDB.WithContext(scopedContext).
				Table("tickets").
				Order("id").
				Pluck("id", &ids).Error; err != nil {
				return err
			}
			if len(ids) != 3 ||
				ids[0] != 1 ||
				ids[1] != 2 ||
				ids[2] != 5 {
				return fmt.Errorf(
					"authorized project set returned ticket ids %v",
					ids,
				)
			}
			return nil
		},
	); err != nil {
		t.Fatalf("authorized project set transaction: %v", err)
	}
	crossWriteErr := WithAuthorizedProjectSetContextTransaction(
		context.Background(),
		runtimeDB,
		10,
		[]uint{100, 200},
		func(scopedContext context.Context) error {
			return runtimeDB.WithContext(scopedContext).Exec(`
				INSERT INTO tickets (
					id,
					public_id,
					organization_id,
					project_id,
					queue_id,
					request_type_version_id,
					workflow_version_id,
					title
				)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			`,
				6,
				"00000000-0000-7000-8000-000000000006",
				20,
				300,
				3000,
				"00000000-0000-7000-8000-000000000101",
				"00000000-0000-7000-8000-000000000201",
				"cross-organization-write",
			).Error
		},
	)
	if crossWriteErr == nil {
		t.Fatal(
			"authorized project set accepted a cross-organization write",
		)
	}
	if err := WithAuthorizedProjectSetContextTransaction(
		context.Background(),
		runtimeDB,
		10,
		nil,
		func(scopedContext context.Context) error {
			var count int64
			if err := runtimeDB.WithContext(scopedContext).
				Table("tickets").
				Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return fmt.Errorf(
					"empty authorized project set exposed %d tickets",
					count,
				)
			}
			return nil
		},
	); err != nil {
		t.Fatalf("empty authorized project set transaction: %v", err)
	}
	for name, projectIDs := range map[string][]uint{
		"duplicate":          {100, 100},
		"zero":               {0},
		"cross organization": {100, 300},
	} {
		t.Run("authorized project set "+name, func(t *testing.T) {
			err := WithAuthorizedProjectSetContextTransaction(
				context.Background(),
				runtimeDB,
				10,
				projectIDs,
				func(context.Context) error { return nil },
			)
			if err == nil {
				t.Fatalf("%s project set was accepted", name)
			}
		})
	}
	if strconv.IntSize == 64 {
		overflow := uint(postgresProjectScopeMaxID + 1)
		if err := WithAuthorizedProjectSetContextTransaction(
			context.Background(),
			runtimeDB,
			10,
			[]uint{overflow},
			func(context.Context) error { return nil },
		); err == nil {
			t.Fatal("BIGINT-overflow project set was accepted")
		}
	}

	var countWithoutScope int64
	if err := runtimeDB.Table("tickets").Count(&countWithoutScope).Error; err != nil {
		t.Fatalf("query without transaction-local scope: %v", err)
	}
	if countWithoutScope != 0 {
		t.Fatalf(
			"query without transaction-local scope returned %d rows",
			countWithoutScope,
		)
	}

	if err := WithProjectScopeTransaction(
		context.Background(),
		runtimeDB,
		models.ProjectScope{OrganizationID: 10, ProjectID: 999},
		func(scoped *gorm.DB) error {
			var count int64
			if err := scoped.Table("tickets").Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return fmt.Errorf("wrong project scope returned %d rows", count)
			}
			return nil
		},
	); err != nil {
		t.Fatalf("verify wrong scope is denied: %v", err)
	}

	if err := WithProjectScopeTransaction(
		context.Background(),
		runtimeDB,
		models.ProjectScope{OrganizationID: 10, ProjectID: 100},
		func(scoped *gorm.DB) error {
			var ids []int64
			if err := scoped.Table("tickets").Order("id").Pluck("id", &ids).Error; err != nil {
				return err
			}
			if len(ids) != 2 || ids[0] != 1 || ids[1] != 5 {
				return fmt.Errorf("correct scope returned ticket ids %v", ids)
			}
			return scoped.Exec(`
				INSERT INTO tickets (
					id,
					public_id,
					organization_id,
					project_id,
					queue_id,
					request_type_version_id,
					workflow_version_id,
					title
				)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			`,
				3,
				"00000000-0000-7000-8000-000000000003",
				10,
				100,
				1000,
				"00000000-0000-7000-8000-000000000101",
				"00000000-0000-7000-8000-000000000201",
				"project-a-created",
			).Error
		},
	); err != nil {
		t.Fatalf("verify correct scope read/write: %v", err)
	}

	var countAfterScope int64
	if err := runtimeDB.Table("tickets").Count(&countAfterScope).Error; err != nil {
		t.Fatalf("query after transaction-local scope: %v", err)
	}
	if countAfterScope != 0 {
		t.Fatalf(
			"transaction-local scope leaked and exposed %d rows",
			countAfterScope,
		)
	}

	writeErr := WithProjectScopeTransaction(
		context.Background(),
		runtimeDB,
		models.ProjectScope{OrganizationID: 10, ProjectID: 100},
		func(scoped *gorm.DB) error {
			return scoped.Exec(`
				INSERT INTO tickets (
					id,
					public_id,
					organization_id,
					project_id,
					queue_id,
					request_type_version_id,
					workflow_version_id,
					title
				)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			`,
				4,
				"00000000-0000-7000-8000-000000000004",
				10,
				200,
				2000,
				"00000000-0000-7000-8000-000000000101",
				"00000000-0000-7000-8000-000000000201",
				"cross-project-write",
			).Error
		},
	)
	if writeErr == nil {
		t.Fatal("RLS WITH CHECK accepted a cross-project write")
	}
}

func quotePostgresRLSTestIdentifier(identifier string) string {
	// Test identifiers are generated exclusively from fixed ASCII prefixes and
	// Unix nanoseconds, never from an environment variable or database value.
	return `"` + identifier + `"`
}

func quotePostgresRLSTestLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}
