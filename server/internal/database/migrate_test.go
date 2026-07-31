package database

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func auditedSeedOptions(options SeedOptions) SeedOptions {
	options.EnsureInitialAdministratorMembership =
		services.EnsureBootstrapProjectAdministratorMembership
	options.EnsureSampleUserMembership =
		services.EnsureSampleProjectMembership
	return options
}

func TestSeedDataRequiresAuditedInitialAdministratorMembershipWriter(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open("file:seed-writer-required?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	err = SeedData(db, SeedOptions{})
	if err == nil || !strings.Contains(
		err.Error(),
		"audited initial administrator membership writer is required",
	) {
		t.Fatalf("SeedData() error = %v, want audited writer requirement", err)
	}
}

func TestValidateRuntimeSchemaRequiresIdempotencyCompletionTTL(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:runtime-schema-missing?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.Exec(`
		CREATE TABLE idempotency_records (
			id TEXT PRIMARY KEY,
			expires_at DATETIME NOT NULL
		)
	`).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}

	err = ValidateRuntimeSchema(db)
	if err == nil {
		t.Fatal("expected missing completion TTL column to fail validation")
	}
	if !strings.Contains(err.Error(), "idempotency_records.completion_ttl_nanoseconds") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateRuntimeSchemaAcceptsMigratedModel(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:runtime-schema-ready?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := RunMigrations(db); err != nil {
		t.Fatalf("migrate runtime schema: %v", err)
	}
	if err := ValidateRuntimeSchema(db); err != nil {
		t.Fatalf("validate migrated schema: %v", err)
	}
	for _, index := range []string{
		"idx_tickets_scope_due_id",
		"idx_tickets_scope_sla_status_created_id",
	} {
		if !db.Migrator().HasIndex(&models.Ticket{}, index) {
			t.Fatalf("migrated schema is missing ticket pagination index %q", index)
		}
	}
}

func TestValidateRuntimeSchemaRejectsMissingTicketAgentColumns(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:runtime-schema-ticket-legacy?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE tickets (id INTEGER PRIMARY KEY, version INTEGER NOT NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	err = ValidateRuntimeSchema(db)
	if err == nil || !strings.Contains(err.Error(), "tickets.agent_context") {
		t.Fatalf("validation error = %v, want missing tickets.agent_context", err)
	}
}

func TestRunMigrationsNeverSeedsBusinessData(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", "must-not-be-used-by-schema-migration")
	t.Setenv("ENVIRONMENT", "development")

	db, err := gorm.Open(
		sqlite.Open("file:runtime-schema-no-implicit-seed?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(db); err != nil {
		t.Fatalf("run schema migration: %v", err)
	}

	var users int64
	if err := db.Model(&models.User{}).Count(&users).Error; err != nil {
		t.Fatal(err)
	}
	var categories int64
	if err := db.Model(&models.Category{}).Count(&categories).Error; err != nil {
		t.Fatal(err)
	}
	var tickets int64
	if err := db.Model(&models.Ticket{}).Count(&tickets).Error; err != nil {
		t.Fatal(err)
	}
	if users != 0 || categories != 0 || tickets != 0 {
		t.Fatalf(
			"schema migration seeded business data: users=%d categories=%d tickets=%d",
			users,
			categories,
			tickets,
		)
	}
}

func TestSeedDataIsTransactionalAndIdempotent(t *testing.T) {
	t.Setenv("ADMIN_EMAIL", "bootstrap@example.test")
	t.Setenv("ADMIN_PASSWORD", "ChronoDesk-Test-2026!")
	t.Setenv("ENVIRONMENT", "development")

	db, err := gorm.Open(
		sqlite.Open("file:seed-idempotent?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(db); err != nil {
		t.Fatalf("run schema migration: %v", err)
	}
	for run := 1; run <= 2; run++ {
		if err := SeedData(db, auditedSeedOptions(SeedOptions{})); err != nil {
			t.Fatalf("seed run %d: %v", run, err)
		}
	}

	var admins int64
	if err := db.Model(&models.User{}).
		Where("platform_role = ?", models.PlatformRolePlatformAdmin).
		Count(&admins).Error; err != nil {
		t.Fatal(err)
	}
	var categories int64
	if err := db.Model(&models.Category{}).Count(&categories).Error; err != nil {
		t.Fatal(err)
	}
	var tickets int64
	if err := db.Model(&models.Ticket{}).Count(&tickets).Error; err != nil {
		t.Fatal(err)
	}
	var configs int64
	if err := db.Model(&models.SystemConfig{}).Count(&configs).Error; err != nil {
		t.Fatal(err)
	}
	var emailConfigs int64
	if err := db.Model(&models.EmailConfig{}).Count(&emailConfigs).Error; err != nil {
		t.Fatal(err)
	}
	var administrator models.User
	if err := db.Where(
		"platform_role = ?",
		models.PlatformRolePlatformAdmin,
	).
		First(&administrator).Error; err != nil {
		t.Fatal(err)
	}
	_, _, project, _ := loadDefaultProjectHierarchy(t, db)
	var memberships []models.ProjectMembership
	if err := db.Where(
		"project_id = ? AND user_id = ?",
		project.ID,
		administrator.ID,
	).Find(&memberships).Error; err != nil {
		t.Fatal(err)
	}
	if admins != 1 ||
		categories != 4 ||
		tickets != 0 ||
		configs != int64(len(models.DefaultSystemConfigs("test"))) ||
		emailConfigs != 1 ||
		len(memberships) != 1 ||
		memberships[0].Role != models.ProjectRoleAdmin ||
		!memberships[0].IsActive {
		t.Fatalf(
			"unexpected idempotent seed result: admins=%d categories=%d tickets=%d configs=%d email_configs=%d memberships=%+v",
			admins,
			categories,
			tickets,
			configs,
			emailConfigs,
			memberships,
		)
	}
}

func TestSeedDataGrantsExistingInitialAdministratorDefaultProjectAccess(
	t *testing.T,
) {
	t.Setenv("ADMIN_PASSWORD", "")
	t.Setenv("ENVIRONMENT", "development")

	db, err := gorm.Open(
		sqlite.Open("file:seed-existing-admin-membership?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(db); err != nil {
		t.Fatalf("run schema migration: %v", err)
	}
	administrator := models.User{
		Username:     "admin",
		Email:        "admin@example.com",
		PasswordHash: "existing-password-hash",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&administrator).Error; err != nil {
		t.Fatalf("create existing administrator: %v", err)
	}

	if err := SeedData(db, auditedSeedOptions(SeedOptions{})); err != nil {
		t.Fatalf("seed existing administrator: %v", err)
	}

	organization, _, project, _ := loadDefaultProjectHierarchy(t, db)
	var membership models.ProjectMembership
	if err := db.Where(
		"project_id = ? AND user_id = ?",
		project.ID,
		administrator.ID,
	).First(&membership).Error; err != nil {
		t.Fatalf("load existing administrator membership: %v", err)
	}
	if project.OrganizationID != organization.ID ||
		membership.Role != models.ProjectRoleAdmin ||
		!membership.IsActive {
		t.Fatalf(
			"default project membership is not an active administrator grant: project=%+v membership=%+v",
			project,
			membership,
		)
	}
}

func TestSeedDataRejectsUntrustedControlledAdministratorCandidates(
	t *testing.T,
) {
	const configuredEmail = "break-glass@example.test"
	tests := []struct {
		name      string
		wantError string
		setup     func(*testing.T, *gorm.DB)
	}{
		{
			name:      "unrelated active administrator is never selected",
			wantError: "ADMIN_PASSWORD is required",
			setup: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				if err := db.Create(&models.User{
					Username:     "unrelated-admin",
					Email:        "unrelated-admin@example.test",
					PasswordHash: "hash",
					PlatformRole: models.PlatformRolePlatformAdmin,
					Status:       models.UserStatusActive,
				}).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:      "controlled identity is inactive",
			wantError: "not the active break-glass account",
			setup: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				if err := db.Create(&models.User{
					Username:     "admin",
					Email:        configuredEmail,
					PasswordHash: "hash",
					PlatformRole: models.PlatformRolePlatformAdmin,
					Status:       models.UserStatusInactive,
				}).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:      "controlled identity is deleted",
			wantError: "not the active break-glass account",
			setup: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				user := models.User{
					Username:     "admin",
					Email:        configuredEmail,
					PasswordHash: "hash",
					PlatformRole: models.PlatformRolePlatformAdmin,
					Status:       models.UserStatusActive,
				}
				if err := db.Create(&user).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Delete(&user).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:      "controlled identity has wrong platform role",
			wantError: "not the active break-glass account",
			setup: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				if err := db.Create(&models.User{
					Username:     "admin",
					Email:        configuredEmail,
					PasswordHash: "hash",
					PlatformRole: models.PlatformRoleMember,
					Status:       models.UserStatusActive,
				}).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:      "configured username and email are split identities",
			wantError: "resolve to different retained identities",
			setup: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				users := []models.User{
					{
						Username:     "admin",
						Email:        "other-admin@example.test",
						PasswordHash: "hash",
						PlatformRole: models.PlatformRolePlatformAdmin,
						Status:       models.UserStatusActive,
					},
					{
						Username:     "other-break-glass",
						Email:        configuredEmail,
						PasswordHash: "hash",
						PlatformRole: models.PlatformRolePlatformAdmin,
						Status:       models.UserStatusActive,
					},
				}
				if err := db.Create(&users).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("ADMIN_EMAIL", configuredEmail)
			t.Setenv("ADMIN_PASSWORD", "")
			t.Setenv("ENVIRONMENT", "development")
			db, err := gorm.Open(
				sqlite.Open(
					"file:seed-untrusted-"+
						strings.ReplaceAll(test.name, " ", "-")+
						"?mode=memory&cache=shared",
				),
				&gorm.Config{},
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := RunMigrations(db); err != nil {
				t.Fatalf("run schema migration: %v", err)
			}
			test.setup(t, db)

			err = SeedData(db, auditedSeedOptions(SeedOptions{}))
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf(
					"SeedData() error = %v, want %q",
					err,
					test.wantError,
				)
			}
			for _, artifact := range []struct {
				name  string
				model any
			}{
				{name: "memberships", model: &models.ProjectMembership{}},
				{name: "categories", model: &models.Category{}},
				{name: "configs", model: &models.SystemConfig{}},
				{name: "email configs", model: &models.EmailConfig{}},
				{name: "events", model: &models.DomainEvent{}},
				{name: "outbox", model: &models.OutboxDelivery{}},
				{name: "audit ledger", model: &models.AuditLedgerEntry{}},
			} {
				var count int64
				if countErr := db.Model(artifact.model).Count(&count).Error; countErr != nil {
					t.Fatalf("count %s: %v", artifact.name, countErr)
				}
				if count != 0 {
					t.Fatalf(
						"failed seed retained %s = %d, want 0",
						artifact.name,
						count,
					)
				}
			}
		})
	}
}

func TestSeedDataRejectsConflictingDefaultProjectMembershipWithoutOverwrite(
	t *testing.T,
) {
	t.Setenv("ADMIN_PASSWORD", "")
	t.Setenv("ENVIRONMENT", "development")

	db, err := gorm.Open(
		sqlite.Open("file:seed-conflicting-admin-membership?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(db); err != nil {
		t.Fatalf("run schema migration: %v", err)
	}
	administrator := models.User{
		Username:     "admin",
		Email:        "admin@example.com",
		PasswordHash: "configured-password-hash",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&administrator).Error; err != nil {
		t.Fatalf("create existing administrator: %v", err)
	}
	_, _, project, _ := loadDefaultProjectHierarchy(t, db)
	existingMembership := models.ProjectMembership{
		ProjectID: project.ID,
		UserID:    administrator.ID,
		Role:      models.ProjectRoleManager,
		IsActive:  true,
	}
	if err := db.Create(&existingMembership).Error; err != nil {
		t.Fatalf("create explicit membership: %v", err)
	}

	err = SeedData(db, auditedSeedOptions(SeedOptions{}))
	if err == nil || !errors.Is(err, services.ErrProjectMembershipConflict) {
		t.Fatalf("SeedData() error = %v, want explicit membership conflict", err)
	}

	var preserved models.ProjectMembership
	if err := db.First(&preserved, existingMembership.ID).Error; err != nil {
		t.Fatalf("reload explicit membership: %v", err)
	}
	if preserved.Role != models.ProjectRoleManager || !preserved.IsActive {
		t.Fatalf("explicit membership was overwritten: %+v", preserved)
	}
	var categories int64
	if err := db.Model(&models.Category{}).Count(&categories).Error; err != nil {
		t.Fatal(err)
	}
	var configs int64
	if err := db.Model(&models.SystemConfig{}).Count(&configs).Error; err != nil {
		t.Fatal(err)
	}
	var emailConfigs int64
	if err := db.Model(&models.EmailConfig{}).Count(&emailConfigs).Error; err != nil {
		t.Fatal(err)
	}
	if categories != 0 || configs != 0 || emailConfigs != 0 {
		t.Fatalf(
			"failed seed was not rolled back: categories=%d configs=%d email_configs=%d",
			categories,
			configs,
			emailConfigs,
		)
	}
}

func TestSeedDataRequiresTrustedDefaultProjectAndRollsBackAdministrator(
	t *testing.T,
) {
	t.Setenv("ADMIN_EMAIL", "missing-project@example.test")
	t.Setenv("ADMIN_PASSWORD", "ChronoDesk-Test-2026!")
	t.Setenv("ENVIRONMENT", "development")

	db, err := gorm.Open(
		sqlite.Open("file:seed-missing-default-project?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(db); err != nil {
		t.Fatalf("run schema migration: %v", err)
	}
	_, _, project, _ := loadDefaultProjectHierarchy(t, db)
	if err := db.Delete(&models.Project{}, project.ID).Error; err != nil {
		t.Fatalf("remove default project fixture: %v", err)
	}

	err = SeedData(db, auditedSeedOptions(SeedOptions{}))
	if err == nil || !strings.Contains(err.Error(), "trusted default project") {
		t.Fatalf("SeedData() error = %v, want missing trusted project", err)
	}

	for _, assertion := range []struct {
		name  string
		model any
	}{
		{name: "administrators", model: &models.User{}},
		{name: "memberships", model: &models.ProjectMembership{}},
		{name: "categories", model: &models.Category{}},
		{name: "configs", model: &models.SystemConfig{}},
		{name: "email configs", model: &models.EmailConfig{}},
	} {
		var count int64
		query := db.Model(assertion.model)
		if assertion.name == "administrators" {
			query = query.Where(
				"platform_role = ?",
				models.PlatformRolePlatformAdmin,
			)
		}
		if err := query.Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", assertion.name, err)
		}
		if count != 0 {
			t.Fatalf("%s after failed seed = %d, want 0", assertion.name, count)
		}
	}
}

func TestSeedDataRejectsSamplesOutsideDevelopmentAndRollsBack(t *testing.T) {
	t.Setenv("ADMIN_EMAIL", "rollback@example.test")
	t.Setenv("ADMIN_PASSWORD", "ChronoDesk-Test-2026!")
	t.Setenv("ENVIRONMENT", "production")

	db, err := gorm.Open(
		sqlite.Open("file:seed-rollback?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(db); err != nil {
		t.Fatalf("run schema migration: %v", err)
	}
	err = SeedData(
		db,
		auditedSeedOptions(SeedOptions{IncludeSampleData: true}),
	)
	if err == nil || !strings.Contains(err.Error(), "ENVIRONMENT=development") {
		t.Fatalf("SeedData() error = %v, want environment gate", err)
	}

	var users int64
	if err := db.Model(&models.User{}).Count(&users).Error; err != nil {
		t.Fatal(err)
	}
	var categories int64
	if err := db.Model(&models.Category{}).Count(&categories).Error; err != nil {
		t.Fatal(err)
	}
	var configs int64
	if err := db.Model(&models.SystemConfig{}).Count(&configs).Error; err != nil {
		t.Fatal(err)
	}
	var emailConfigs int64
	if err := db.Model(&models.EmailConfig{}).Count(&emailConfigs).Error; err != nil {
		t.Fatal(err)
	}
	var memberships int64
	if err := db.Model(&models.ProjectMembership{}).Count(&memberships).Error; err != nil {
		t.Fatal(err)
	}
	var events int64
	if err := db.Model(&models.DomainEvent{}).Count(&events).Error; err != nil {
		t.Fatal(err)
	}
	var deliveries int64
	if err := db.Model(&models.OutboxDelivery{}).Count(&deliveries).Error; err != nil {
		t.Fatal(err)
	}
	var auditEntries int64
	if err := db.Model(&models.AuditLedgerEntry{}).Count(&auditEntries).Error; err != nil {
		t.Fatal(err)
	}
	if users != 0 ||
		categories != 0 ||
		configs != 0 ||
		emailConfigs != 0 ||
		memberships != 0 ||
		events != 0 ||
		deliveries != 0 ||
		auditEntries != 0 {
		t.Fatalf(
			"failed seed was not rolled back: users=%d categories=%d configs=%d email_configs=%d memberships=%d events=%d deliveries=%d audit_entries=%d",
			users,
			categories,
			configs,
			emailConfigs,
			memberships,
			events,
			deliveries,
			auditEntries,
		)
	}
}

func TestRunMigrationsUpgradesLegacyHumanRolesBeforeInstallingConstraint(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:runtime-schema-human-role-upgrade?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatalf("create users table: %v", err)
	}
	if err := db.Exec(`
		ALTER TABLE users
		ADD COLUMN role TEXT NOT NULL DEFAULT 'customer'
	`).Error; err != nil {
		t.Fatalf("add legacy role column: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO users (username, email, password_hash, role, status) VALUES
			('legacy-customer', 'legacy-customer@example.test', 'hash', 'user', 'active'),
			('legacy-admin', 'legacy-admin@example.test', 'hash', 'superuser', 'active'),
			('current-agent', 'current-agent@example.test', 'hash', 'agent', 'active'),
			('current-supervisor', 'current-supervisor@example.test', 'hash', 'supervisor', 'active')
	`).Error; err != nil {
		t.Fatalf("seed legacy users: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO users (
			username, email, password_hash, role, status, deleted_at
		) VALUES (
			'legacy-deleted-customer',
			'legacy-deleted-customer@example.test',
			'hash',
			'user',
			'deleted',
			CURRENT_TIMESTAMP
		)
	`).Error; err != nil {
		t.Fatalf("seed soft-deleted legacy user: %v", err)
	}
	for run := 1; run <= 2; run++ {
		if err := RunMigrations(
			db,
			services.EnsureProjectScopeMigrationMembership,
		); err != nil {
			t.Fatalf("run migration %d: %v", run, err)
		}
	}

	var users []models.User
	if err := db.Unscoped().Order("id ASC").Find(&users).Error; err != nil {
		t.Fatalf("read migrated users: %v", err)
	}
	wantRoles := []models.PlatformRole{
		models.PlatformRoleMember,
		models.PlatformRolePlatformAdmin,
		models.PlatformRoleMember,
		models.PlatformRoleMember,
		models.PlatformRoleMember,
	}
	if len(users) != len(wantRoles) {
		t.Fatalf("migrated user count = %d, want %d", len(users), len(wantRoles))
	}
	for index, wantRole := range wantRoles {
		if users[index].PlatformRole != wantRole {
			t.Errorf(
				"user %d platform role = %q, want %q",
				index,
				users[index].PlatformRole,
				wantRole,
			)
		}
	}
	hasLegacyRole, err := hasExactDatabaseColumn(db, "users", "role")
	if err != nil {
		t.Fatal(err)
	}
	if hasLegacyRole {
		t.Fatal("legacy users.role column remains after platform cutover")
	}

	if err := db.Exec(`
		INSERT INTO users (username, email, password_hash, role, status)
		VALUES ('invalid-legacy-role', 'invalid-legacy-role@example.test', 'hash', 'user', 'active')
	`).Error; err == nil {
		t.Fatal("expected removed legacy role column to reject old contract")
	}
}

func TestRunMigrationsFromModelRejectsInvalidResumePoint(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:runtime-schema-invalid-resume?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, firstModel := range []int{0, len(schemaMigrationModels()) + 2} {
		err := RunMigrationsFromModel(context.Background(), db, firstModel)
		if err == nil || !strings.Contains(err.Error(), "first migration model") {
			t.Fatalf("resume point %d error = %v, want range validation", firstModel, err)
		}
	}
}

func TestRunMigrationsFromModelRequiresContextAndDatabase(t *testing.T) {
	if err := RunMigrationsFromModel(context.TODO(), &gorm.DB{}, 1); err == nil {
		t.Fatal("expected nil migration context to fail")
	}
	if err := RunMigrationsFromModel(context.Background(), nil, 1); err == nil {
		t.Fatal("expected nil migration database to fail")
	}
}

func TestMigrateOneModelConvertsDriverPanicToError(t *testing.T) {
	err := migrateOneModel(nil, &models.User{})
	if err == nil || !strings.Contains(err.Error(), "migration driver panic") {
		t.Fatalf("migration panic error = %v", err)
	}
}
