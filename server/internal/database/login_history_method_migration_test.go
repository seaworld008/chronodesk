package database

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestLoginHistoryMethodModelEnforcesClosedEnum(t *testing.T) {
	dsn := fmt.Sprintf(
		"file:login-history-method-model-%d?mode=memory&cache=shared",
		time.Now().UnixNano(),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.LoginHistory{}); err != nil {
		t.Fatalf("migrate current login history model: %v", err)
	}
	if err := db.Create(&models.User{
		ID:           1,
		Username:     "login-method-model",
		Email:        "login-method-model@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}).Error; err != nil {
		t.Fatalf("seed login history user: %v", err)
	}

	valid := models.LoginHistory{
		UserID:      1,
		Username:    "login-method-model",
		Email:       "login-method-model@example.test",
		IPAddress:   "127.0.0.1",
		LoginTime:   time.Now(),
		LoginStatus: models.LoginStatusFailed,
		LoginMethod: models.LoginMethodOTPRequired,
	}
	if err := db.Create(&valid).Error; err != nil {
		t.Fatalf("persist current OTP-required method: %v", err)
	}
	invalid := valid
	invalid.ID = 0
	invalid.LoginMethod = models.LoginMethod("password+unsupported")
	if err := db.Create(&invalid).Error; err == nil {
		t.Fatal("unsupported login method bypassed database constraint")
	}
}

func TestLoginHistoryMethodsFitPersistedContract(t *testing.T) {
	for _, method := range loginHistoryMethods() {
		if !method.IsValid() {
			t.Errorf("migration contains invalid login method %q", method)
		}
		if len(method) > models.LoginMethodMaxLength {
			t.Errorf(
				"login method %q length = %d, column contract = %d",
				method,
				len(method),
				models.LoginMethodMaxLength,
			)
		}
	}
}

func TestMigratePostgresLoginHistoryMethodContract(t *testing.T) {
	dsn := os.Getenv("CHRONODESK_POSTGRES_MIGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("set CHRONODESK_POSTGRES_MIGRATION_TEST_DSN for the PostgreSQL migration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open PostgreSQL migration database: %v", err)
	}

	transaction := db.Begin()
	if transaction.Error != nil {
		t.Fatalf("begin PostgreSQL migration fixture: %v", transaction.Error)
	}
	defer transaction.Rollback()

	schemaName := fmt.Sprintf("login_history_method_%d", time.Now().UnixNano())
	if err := transaction.Exec(
		`CREATE SCHEMA "` + schemaName + `"`,
	).Error; err != nil {
		t.Fatalf("create isolated PostgreSQL schema: %v", err)
	}
	if err := transaction.Exec(
		`SET LOCAL search_path TO "` + schemaName + `"`,
	).Error; err != nil {
		t.Fatalf("select isolated PostgreSQL schema: %v", err)
	}
	if err := transaction.Exec(`
		CREATE TABLE login_histories (
			id BIGSERIAL PRIMARY KEY,
			login_method VARCHAR(20) DEFAULT 'password',
			is_active BOOLEAN DEFAULT TRUE
		)
	`).Error; err != nil {
		t.Fatalf("create legacy PostgreSQL login history table: %v", err)
	}
	if err := transaction.Exec(`
		INSERT INTO login_histories (login_method, is_active)
		VALUES ('password', TRUE), (NULL, NULL)
	`).Error; err != nil {
		t.Fatalf("seed legacy PostgreSQL login histories: %v", err)
	}

	for run := 1; run <= 2; run++ {
		if err := MigrateLoginHistoryMethodContract(transaction); err != nil {
			t.Fatalf("run PostgreSQL migration %d: %v", run, err)
		}
	}
	if err := validatePostgresLoginHistoryMethodContract(transaction); err != nil {
		t.Fatalf("validate PostgreSQL login history contract: %v", err)
	}

	if err := transaction.Exec(
		`INSERT INTO login_histories (login_method) VALUES (?)`,
		models.LoginMethodOTPRequired,
	).Error; err != nil {
		t.Fatalf("persist PostgreSQL OTP-required audit: %v", err)
	}

	var inactiveNulls int64
	if err := transaction.Table("login_histories").
		Where("is_active IS NULL").
		Count(&inactiveNulls).Error; err != nil {
		t.Fatalf("count PostgreSQL null activity states: %v", err)
	}
	if inactiveNulls != 0 {
		t.Fatalf("PostgreSQL null activity states = %d, want 0", inactiveNulls)
	}

	if err := transaction.Exec(
		`INSERT INTO login_histories (login_method) VALUES ('password+unsupported')`,
	).Error; err == nil {
		t.Fatal("PostgreSQL check constraint accepted an unsupported login method")
	}
}
