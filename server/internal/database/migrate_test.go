package database

import (
	"context"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/auth"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

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
	modelsToMigrate := []any{
		&models.User{},
		&models.UserProfile{},
		&models.EmailConfig{},
		&auth.RefreshToken{},
		&auth.EmailVerification{},
		&auth.PasswordReset{},
		&auth.OTPCode{},
		&models.LoginHistory{},
		&models.ServicePrincipal{},
		&models.AgentCredential{},
		&models.AgentPolicy{},
		&models.PolicyDecision{},
		&models.IdempotencyRecord{},
		&models.Ticket{},
		&models.TicketComment{},
		&models.TicketAttachment{},
		&models.TicketHistory{},
		&models.TicketLease{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.AgentTask{},
		&models.AgentMessage{},
		&models.AgentArtifact{},
		&models.AgentTaskStatusHistory{},
		&models.AgentTaskEvent{},
		&models.AgentPushNotificationConfig{},
		&models.Notification{},
	}
	if err := db.AutoMigrate(modelsToMigrate...); err != nil {
		t.Fatalf("migrate runtime schema: %v", err)
	}
	if err := ValidateRuntimeSchema(db); err != nil {
		t.Fatalf("validate migrated schema: %v", err)
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
		if err := SeedData(db, SeedOptions{}); err != nil {
			t.Fatalf("seed run %d: %v", run, err)
		}
	}

	var admins int64
	if err := db.Model(&models.User{}).
		Where("role = ?", models.RoleAdmin).
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
	if admins != 1 ||
		categories != 4 ||
		tickets != 0 ||
		configs != int64(len(models.DefaultSystemConfigs("test"))) ||
		emailConfigs != 1 {
		t.Fatalf(
			"unexpected idempotent seed result: admins=%d categories=%d tickets=%d configs=%d email_configs=%d",
			admins,
			categories,
			tickets,
			configs,
			emailConfigs,
		)
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
	err = SeedData(db, SeedOptions{IncludeSampleData: true})
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
	if users != 0 || categories != 0 || configs != 0 || emailConfigs != 0 {
		t.Fatalf(
			"failed seed was not rolled back: users=%d categories=%d configs=%d email_configs=%d",
			users,
			categories,
			configs,
			emailConfigs,
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
	// The current model already carries the closed role constraint. Temporarily
	// bypass it to reproduce a database created by an older binary.
	if err := db.Exec("PRAGMA ignore_check_constraints = ON").Error; err != nil {
		t.Fatalf("enable legacy fixture mode: %v", err)
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
	if err := db.Exec("PRAGMA ignore_check_constraints = OFF").Error; err != nil {
		t.Fatalf("restore role constraints: %v", err)
	}

	for run := 1; run <= 2; run++ {
		if err := RunMigrations(db); err != nil {
			t.Fatalf("run migration %d: %v", run, err)
		}
	}

	var users []models.User
	if err := db.Unscoped().Order("id ASC").Find(&users).Error; err != nil {
		t.Fatalf("read migrated users: %v", err)
	}
	wantRoles := []models.UserRole{
		models.RoleCustomer,
		models.RoleAdmin,
		models.RoleAgent,
		models.RoleSupervisor,
		models.RoleCustomer,
	}
	if len(users) != len(wantRoles) {
		t.Fatalf("migrated user count = %d, want %d", len(users), len(wantRoles))
	}
	for index, wantRole := range wantRoles {
		if users[index].Role != wantRole {
			t.Errorf("user %d role = %q, want %q", index, users[index].Role, wantRole)
		}
	}

	if err := db.Exec(`
		INSERT INTO users (username, email, password_hash, role, status)
		VALUES ('invalid-legacy-role', 'invalid-legacy-role@example.test', 'hash', 'user', 'active')
	`).Error; err == nil {
		t.Fatal("expected the closed human-role constraint to reject a legacy role")
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
