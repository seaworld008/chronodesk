package database

import (
	"context"
	"strings"
	"testing"

	"gongdan-system/internal/auth"
	"gongdan-system/internal/models"
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
	if err := RunMigrationsFromModel(nil, &gorm.DB{}, 1); err == nil {
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
