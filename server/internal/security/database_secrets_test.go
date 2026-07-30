package security

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"

	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestDatabaseSecretRotationRejectsPlaintextAndRollsBack(t *testing.T) {
	db := openSecretTestDB(t)
	ctx := context.Background()
	oldRing := testDatabaseKeyring(t, "dek-old", 0x51)
	webhook := models.WebhookConfig{
		Name: "rotate-before-plaintext", Provider: models.WebhookProviderCustom,
		WebhookURL: "https://hooks.example.test", Status: models.WebhookStatusActive,
		CreatedBy: 1,
	}
	if err := db.Create(&webhook).Error; err != nil {
		t.Fatal(err)
	}
	oldEnvelope, err := oldRing.Seal(
		[]byte("authenticated-secret"),
		FieldAAD(webhookSecretsTable, "1", "secret"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&webhook).UpdateColumn("secret", oldEnvelope).Error; err != nil {
		t.Fatal(err)
	}
	email := models.EmailConfig{
		SMTPPassword: "unsupported-cleartext-value", SMTPPort: 587, IsActive: true,
	}
	if err := db.Create(&email).Error; err != nil {
		t.Fatal(err)
	}
	rotating, err := NewKeyring("dek-new", map[string][]byte{
		"dek-old": bytes.Repeat([]byte{0x51}, 32),
		"dek-new": bytes.Repeat([]byte{0x52}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := RotateDatabaseSecrets(ctx, db, rotating)
	if !errors.Is(err, ErrPlaintextSecret) {
		t.Fatalf("RotateDatabaseSecrets() error = %v, want ErrPlaintextSecret", err)
	}
	if report != (SecretRotationReport{}) {
		t.Fatalf("failed rotation report = %+v, want zero value", report)
	}

	var storedWebhook models.WebhookConfig
	var storedEmail models.EmailConfig
	if err := db.Unscoped().First(&storedWebhook, webhook.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&storedEmail, email.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedWebhook.Secret != oldEnvelope {
		t.Fatal("secret rotation was not rolled back after plaintext rejection")
	}
	if storedEmail.SMTPPassword != email.SMTPPassword {
		t.Fatal("plaintext value was unexpectedly rewritten")
	}
	if err := ValidateDatabaseSecrets(ctx, db, rotating); !errors.Is(err, ErrPlaintextSecret) {
		t.Fatalf("validation error = %v, want ErrPlaintextSecret", err)
	}
}

func TestDatabaseSecretValidationDetectsTamperAndWrongKey(t *testing.T) {
	db := openSecretTestDB(t)
	ctx := context.Background()
	ring := testDatabaseKeyring(t, "dek-one", 0x61)
	webhook := models.WebhookConfig{
		Name: "encrypted", Provider: models.WebhookProviderCustom,
		WebhookURL: "https://hooks.example.test", Status: models.WebhookStatusActive,
		CreatedBy: 1,
	}
	if err := db.Create(&webhook).Error; err != nil {
		t.Fatal(err)
	}
	aad := FieldAAD(webhookSecretsTable, "1", "secret")
	envelope, err := ring.Seal([]byte("authenticated-secret"), aad)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&webhook).UpdateColumn("secret", envelope).Error; err != nil {
		t.Fatal(err)
	}
	wrong := testDatabaseKeyring(t, "dek-one", 0x62)
	if err := ValidateDatabaseSecrets(ctx, db, wrong); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("wrong key validation error=%v", err)
	}

	raw, err := decodeEnvelopePayload(envelope)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0x01
	tampered := envelope[:strings.LastIndex(envelope, ":")+1] +
		base64RawURL(raw)
	if err := db.Model(&webhook).UpdateColumn("secret", tampered).Error; err != nil {
		t.Fatal(err)
	}
	if err := ValidateDatabaseSecrets(ctx, db, ring); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("tamper validation error=%v", err)
	}
}

func TestRuntimeDatabaseSecretValidationUsesOneScopedTransactionPerProject(
	t *testing.T,
) {
	db := openSecretTestDB(t)
	if err := db.AutoMigrate(
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
	); err != nil {
		t.Fatal(err)
	}
	organization := models.Organization{
		Slug:   "runtime-secrets",
		Name:   "Runtime Secrets",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "security",
		Name:           "Security",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	projects := []models.Project{
		{
			OrganizationID: organization.ID,
			BusinessUnitID: unit.ID,
			Key:            "SECONE",
			Name:           "Security One",
			Status:         models.ProjectStatusActive,
		},
		{
			OrganizationID: organization.ID,
			BusinessUnitID: unit.ID,
			Key:            "SECTWO",
			Name:           "Security Two",
			Status:         models.ProjectStatusArchived,
		},
	}
	ring := testDatabaseKeyring(t, "dek-runtime", 0x41)
	for index := range projects {
		if err := db.Create(&projects[index]).Error; err != nil {
			t.Fatal(err)
		}
		webhook := models.WebhookConfig{
			OrganizationID: organization.ID,
			ProjectID:      projects[index].ID,
			Name:           "runtime-scoped",
			Provider:       models.WebhookProviderCustom,
			WebhookURL:     "https://hooks.example.test",
			Status:         models.WebhookStatusActive,
			CreatedBy:      1,
		}
		if err := db.Create(&webhook).Error; err != nil {
			t.Fatal(err)
		}
		envelope, err := ring.Seal(
			[]byte("runtime-secret"),
			FieldAAD(
				webhookSecretsTable,
				strconv.FormatUint(uint64(webhook.ID), 10),
				"secret",
			),
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&webhook).
			UpdateColumn("secret", envelope).Error; err != nil {
			t.Fatal(err)
		}
		push := models.AgentPushNotificationConfig{
			ID:             fmt.Sprintf("runtime-push-%d", index),
			OrganizationID: organization.ID,
			ProjectID:      projects[index].ID,
			TaskID:         fmt.Sprintf("runtime-task-%d", index),
			URL:            "https://push.example.test",
		}
		if err := db.Create(&push).Error; err != nil {
			t.Fatal(err)
		}
	}

	var protectedQueries int
	var unscopedProtectedQueries int
	callbackName := "assert_runtime_secret_project_transaction"
	if err := db.Callback().Query().Before("gorm:query").Register(
		callbackName,
		func(query *gorm.DB) {
			tableName := query.Statement.Table
			if query.Statement.Schema != nil {
				tableName = query.Statement.Schema.Table
			}
			if tableName != webhookSecretsTable &&
				tableName != a2aPushSecretsTable {
				return
			}
			protectedQueries++
			if _, ok := query.Statement.ConnPool.(gorm.TxCommitter); !ok {
				unscopedProtectedQueries++
			}
		},
	); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Callback().Query().Remove(callbackName)
	})

	if err := ValidateRuntimeDatabaseSecrets(
		context.Background(),
		db,
		ring,
	); err != nil {
		t.Fatal(err)
	}
	if protectedQueries != len(projects)*2 {
		t.Fatalf(
			"project-owned secret queries = %d, want %d",
			protectedQueries,
			len(projects)*2,
		)
	}
	if unscopedProtectedQueries != 0 {
		t.Fatalf(
			"project-owned secret queries outside transactions = %d, want 0",
			unscopedProtectedQueries,
		)
	}
}

func TestDatabaseSecretRotationRejectsObjectAuthentication(t *testing.T) {
	db := openSecretTestDB(t)
	push := models.AgentPushNotificationConfig{
		ID:             "push-object-auth",
		TaskID:         "task-object-auth",
		URL:            "https://push.example.test",
		Authentication: datatypes.JSON(`{"scheme":"Bearer","credentials":"unsupported"}`),
	}
	if err := db.Create(&push).Error; err != nil {
		t.Fatal(err)
	}
	ring := testDatabaseKeyring(t, "dek-one", 0x71)
	if _, err := RotateDatabaseSecrets(context.Background(), db, ring); !errors.Is(err, ErrPlaintextSecret) {
		t.Fatalf("RotateDatabaseSecrets() error = %v, want ErrPlaintextSecret", err)
	}

	var stored models.AgentPushNotificationConfig
	if err := db.First(&stored, "id = ?", push.ID).Error; err != nil {
		t.Fatal(err)
	}
	if string(stored.Authentication) != string(push.Authentication) {
		t.Fatal("object authentication was unexpectedly rewritten")
	}
}

func TestDatabaseSecretRotationRotatesKeyID(t *testing.T) {
	db := openSecretTestDB(t)
	ctx := context.Background()
	oldRing := testDatabaseKeyring(t, "dek-old", 0x31)
	webhook := models.WebhookConfig{
		Name: "rotate", Provider: models.WebhookProviderCustom,
		WebhookURL: "https://hooks.example.test", Status: models.WebhookStatusActive,
		CreatedBy: 1,
	}
	if err := db.Create(&webhook).Error; err != nil {
		t.Fatal(err)
	}
	envelope, err := oldRing.Seal(
		[]byte("rotate-secret"),
		FieldAAD(webhookSecretsTable, "1", "secret"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&webhook).UpdateColumn("secret", envelope).Error; err != nil {
		t.Fatal(err)
	}
	rotating, err := NewKeyring("dek-new", map[string][]byte{
		"dek-old": bytes.Repeat([]byte{0x31}, 32),
		"dek-new": bytes.Repeat([]byte{0x32}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	primaryEnvelope, err := rotating.Seal(
		[]byte("already-primary"),
		FieldAAD(webhookSecretsTable, "1", "access_token"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&webhook).UpdateColumn("access_token", primaryEnvelope).Error; err != nil {
		t.Fatal(err)
	}
	report, err := RotateDatabaseSecrets(ctx, db, rotating)
	if err != nil {
		t.Fatal(err)
	}
	if report.Rotated != 1 || report.Verified != 1 {
		t.Fatalf("rotation report = %+v", report)
	}
	var stored models.WebhookConfig
	if err := db.First(&stored, webhook.ID).Error; err != nil {
		t.Fatal(err)
	}
	if keyID, err := EnvelopeKeyID(stored.Secret); err != nil || keyID != "dek-new" {
		t.Fatalf("rotated key ID=%q err=%v", keyID, err)
	}
}

func openSecretTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(
		&models.User{},
		&models.WebhookConfig{},
		&models.EmailConfig{},
		&models.AgentPushNotificationConfig{},
	); err != nil {
		t.Fatal(err)
	}
	user := models.User{ID: 1, Username: "secret-owner", Email: "owner@example.test", PasswordHash: "hash"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func testDatabaseKeyring(t *testing.T, keyID string, value byte) *Keyring {
	t.Helper()
	ring, err := NewKeyring(keyID, map[string][]byte{keyID: bytes.Repeat([]byte{value}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	return ring
}

func decodeEnvelopePayload(envelope string) ([]byte, error) {
	_, encoded, err := parseEnvelope(envelope)
	if err != nil {
		return nil, err
	}
	return base64RawURLDecode(encoded)
}

func base64RawURL(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}

func base64RawURLDecode(value string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(value)
}
