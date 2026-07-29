package security

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"

	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestDatabaseSecretMigrationIsExplicitAndFailFast(t *testing.T) {
	db := openSecretTestDB(t)
	ctx := context.Background()
	legacyAuthentication := datatypes.JSON(`{"scheme":"Bearer","credentials":"legacy-auth"}`)
	webhook := models.WebhookConfig{
		Name: "legacy", Provider: models.WebhookProviderCustom,
		WebhookURL: "https://hooks.example.test", Status: models.WebhookStatusActive,
		Secret: "legacy-webhook-secret", AccessToken: "legacy-access-token", CreatedBy: 1,
	}
	email := models.EmailConfig{
		SMTPPassword: "legacy-smtp-password", SMTPPort: 587, IsActive: true,
	}
	push := models.AgentPushNotificationConfig{
		ID: "push-legacy", TaskID: "task-legacy", URL: "https://push.example.test",
		Token: "legacy-push-token", Authentication: legacyAuthentication,
	}
	for _, row := range []any{&webhook, &email, &push} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	ring := testDatabaseKeyring(t, "dek-one", 0x51)
	if err := ValidateDatabaseSecrets(ctx, db, ring); !errors.Is(err, ErrPlaintextSecret) {
		t.Fatalf("plaintext startup validation error=%v", err)
	}

	report, err := MigrateLegacyDatabaseSecrets(ctx, db, ring)
	if err != nil {
		t.Fatal(err)
	}
	if report.Encrypted != 5 || report.Rotated != 0 {
		t.Fatalf("migration report=%+v", report)
	}
	if err := ValidateDatabaseSecrets(ctx, db, ring); err != nil {
		t.Fatalf("post-migration validation: %v", err)
	}

	var storedWebhook models.WebhookConfig
	var storedEmail models.EmailConfig
	var storedPush models.AgentPushNotificationConfig
	if err := db.Unscoped().First(&storedWebhook, webhook.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&storedEmail, email.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&storedPush, "id = ?", push.ID).Error; err != nil {
		t.Fatal(err)
	}
	databaseBytes := strings.Join([]string{
		storedWebhook.Secret,
		storedWebhook.AccessToken,
		storedEmail.SMTPPassword,
		storedPush.Token,
		string(storedPush.Authentication),
	}, "\n")
	for _, plaintext := range []string{
		"legacy-webhook-secret",
		"legacy-access-token",
		"legacy-smtp-password",
		"legacy-push-token",
		"legacy-auth",
	} {
		if strings.Contains(databaseBytes, plaintext) {
			t.Fatalf("database still contains plaintext %q", plaintext)
		}
	}

	// Reconstructing a protector simulates a process restart.
	restarted := testDatabaseKeyring(t, "dek-one", 0x51)
	if err := ValidateDatabaseSecrets(ctx, db, restarted); err != nil {
		t.Fatalf("restart validation: %v", err)
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

func TestDatabaseSecretMigrationRotatesKeyID(t *testing.T) {
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
	report, err := MigrateLegacyDatabaseSecrets(ctx, db, rotating)
	if err != nil {
		t.Fatal(err)
	}
	if report.Rotated != 1 {
		t.Fatalf("rotation report=%+v", report)
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
