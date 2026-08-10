package security

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestValidateDatabaseSecretsRejectsBadLiveWebhookSnapshot(t *testing.T) {
	db, project := openSnapshotSecretTestDB(t)
	config := createSnapshotSecretWebhook(t, db, project)
	snapshot := createSnapshotSecretRow(
		t,
		db,
		config,
		time.Now().UTC().Add(time.Hour),
		"cdsec:v1:malformed",
		"",
		"",
	)

	ring := testDatabaseKeyring(t, "dek-current", 0x21)
	err := ValidateDatabaseSecrets(context.Background(), db, ring)
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf(
			"ValidateDatabaseSecrets() error = %v, want invalid live snapshot %s rejection",
			err,
			snapshot.ID,
		)
	}
}

func TestValidateDatabaseSecretsAuthenticatesEveryNonEmptyLiveSnapshotField(
	t *testing.T,
) {
	tests := []struct {
		name                          string
		secret, previous, accessToken string
	}{
		{name: "secret", secret: "cdsec:v1:malformed"},
		{name: "previous_secret", previous: "cdsec:v1:malformed"},
		{name: "access_token", accessToken: "cdsec:v1:malformed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, project := openSnapshotSecretTestDB(t)
			config := createSnapshotSecretWebhook(t, db, project)
			createSnapshotSecretRow(
				t,
				db,
				config,
				time.Now().UTC().Add(time.Hour),
				test.secret,
				test.previous,
				test.accessToken,
			)

			err := ValidateDatabaseSecrets(
				context.Background(),
				db,
				testDatabaseKeyring(t, "dek-current", 0x21),
			)
			if !errors.Is(err, ErrInvalidEnvelope) {
				t.Fatalf(
					"ValidateDatabaseSecrets() error = %v, want %s rejection",
					err,
					test.name,
				)
			}
		})
	}
}

func TestValidateRuntimeDatabaseSecretsRejectsBadLiveSnapshotInArchivedProject(
	t *testing.T,
) {
	db, active := openSnapshotSecretTestDB(t)
	archived := createSnapshotSecretProject(
		t,
		db,
		active.OrganizationID,
		active.BusinessUnitID,
		"ARCHIVED",
		models.ProjectStatusArchived,
	)
	ring := testDatabaseKeyring(t, "dek-runtime", 0x22)

	activeConfig := createSnapshotSecretWebhook(t, db, active)
	activeEnvelope := sealSnapshotField(
		t,
		ring,
		activeConfig.ID,
		"secret",
		"active-secret",
	)
	createSnapshotSecretRow(
		t,
		db,
		activeConfig,
		time.Now().UTC().Add(time.Hour),
		activeEnvelope,
		"",
		"",
	)

	archivedConfig := createSnapshotSecretWebhook(t, db, archived)
	createSnapshotSecretRow(
		t,
		db,
		archivedConfig,
		time.Now().UTC().Add(time.Hour),
		"cdsec:v1:malformed",
		"",
		"",
	)

	err := ValidateRuntimeDatabaseSecrets(context.Background(), db, ring)
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf(
			"ValidateRuntimeDatabaseSecrets() error = %v, want archived project snapshot rejection",
			err,
		)
	}
}

func TestWebhookSnapshotValidationUsesHistoricalConfigIDAAD(t *testing.T) {
	db, project := openSnapshotSecretTestDB(t)
	ring := testDatabaseKeyring(t, "dek-aad", 0x23)
	config := createSnapshotSecretWebhook(t, db, project)
	historicalEnvelope := sealSnapshotField(
		t,
		ring,
		config.ID,
		"secret",
		"historical-aad-secret",
	)
	snapshot := createSnapshotSecretRow(
		t,
		db,
		config,
		time.Now().UTC().Add(time.Hour),
		historicalEnvelope,
		"",
		"",
	)

	if err := ValidateDatabaseSecrets(context.Background(), db, ring); err != nil {
		t.Fatalf("historical ConfigID AAD validation failed: %v", err)
	}

	snapshotEnvelope, err := ring.Seal(
		[]byte("wrong-row-aad-secret"),
		FieldAAD(
			webhookSecretsTable,
			snapshot.ID,
			"secret",
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Table("webhook_delivery_snapshots").
		Where("id = ?", snapshot.ID).
		UpdateColumn("secret", snapshotEnvelope).Error; err != nil {
		t.Fatal(err)
	}
	if err := ValidateDatabaseSecrets(
		context.Background(),
		db,
		ring,
	); !errors.Is(err, ErrAuthentication) {
		t.Fatalf(
			"snapshot UUID AAD validation error = %v, want ErrAuthentication",
			err,
		)
	}
}

func TestRotateDatabaseSecretsRewrapsAllLiveSnapshotFields(t *testing.T) {
	db, project := openSnapshotSecretTestDB(t)
	oldRing := testDatabaseKeyring(t, "dek-old", 0x24)
	config := createSnapshotSecretWebhook(t, db, project)
	oldValues := map[string]string{
		"secret":          sealSnapshotField(t, oldRing, config.ID, "secret", "secret-value"),
		"previous_secret": sealSnapshotField(t, oldRing, config.ID, "previous_secret", "previous-value"),
		"access_token":    sealSnapshotField(t, oldRing, config.ID, "access_token", "token-value"),
	}
	snapshot := createSnapshotSecretRow(
		t,
		db,
		config,
		time.Now().UTC().Add(time.Hour),
		oldValues["secret"],
		oldValues["previous_secret"],
		oldValues["access_token"],
	)
	rotating := newSnapshotTestKeyring(t, "dek-new", map[string]byte{
		"dek-old": 0x24,
		"dek-new": 0x25,
	})

	report, err := RotateDatabaseSecrets(context.Background(), db, rotating)
	if err != nil {
		t.Fatal(err)
	}
	if report != (SecretRotationReport{Rotated: 3}) {
		t.Fatalf("rotation report = %+v, want three snapshot fields rotated", report)
	}

	var stored models.WebhookDeliverySnapshot
	if err := db.First(&stored, "id = ?", snapshot.ID).Error; err != nil {
		t.Fatal(err)
	}
	for field, envelope := range map[string]string{
		"secret":          stored.Secret,
		"previous_secret": stored.PreviousSecret,
		"access_token":    stored.AccessToken,
	} {
		if envelope == oldValues[field] {
			t.Fatalf("%s envelope was not rewrapped", field)
		}
		keyID, err := EnvelopeKeyID(envelope)
		if err != nil || keyID != "dek-new" {
			t.Fatalf("%s key ID = %q, err = %v; want dek-new", field, keyID, err)
		}
	}
}

func TestRotateDatabaseSecretsBadLaterSnapshotRollsBackEarlierRows(t *testing.T) {
	db, project := openSnapshotSecretTestDB(t)
	oldRing := testDatabaseKeyring(t, "dek-old", 0x26)
	firstConfig := createSnapshotSecretWebhook(t, db, project)
	firstConfigEnvelope := sealSnapshotField(
		t,
		oldRing,
		firstConfig.ID,
		"secret",
		"config-secret",
	)
	if err := db.Model(&firstConfig).
		UpdateColumn("secret", firstConfigEnvelope).Error; err != nil {
		t.Fatal(err)
	}
	firstSnapshotEnvelope := sealSnapshotField(
		t,
		oldRing,
		firstConfig.ID,
		"secret",
		"first-snapshot-secret",
	)
	firstSnapshot := createSnapshotSecretRow(
		t,
		db,
		firstConfig,
		time.Now().UTC().Add(time.Hour),
		firstSnapshotEnvelope,
		"",
		"",
	)

	laterConfig := createSnapshotSecretWebhook(t, db, project)
	createSnapshotSecretRow(
		t,
		db,
		laterConfig,
		time.Now().UTC().Add(time.Hour),
		"cdsec:v1:malformed",
		"",
		"",
	)
	rotating := newSnapshotTestKeyring(t, "dek-new", map[string]byte{
		"dek-old": 0x26,
		"dek-new": 0x27,
	})

	report, err := RotateDatabaseSecrets(context.Background(), db, rotating)
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("RotateDatabaseSecrets() error = %v, want ErrInvalidEnvelope", err)
	}
	if report != (SecretRotationReport{}) {
		t.Fatalf("failed rotation report = %+v, want zero", report)
	}

	var storedConfig models.WebhookConfig
	if err := db.Unscoped().First(&storedConfig, firstConfig.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedConfig.Secret != firstConfigEnvelope {
		t.Fatal("config rotation was not rolled back")
	}
	var storedSnapshot models.WebhookDeliverySnapshot
	if err := db.First(&storedSnapshot, "id = ?", firstSnapshot.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedSnapshot.Secret != firstSnapshotEnvelope {
		t.Fatal("earlier snapshot rotation was not rolled back")
	}
}

func TestNewOnlyKeyringRequiresCompleteLiveSnapshotRotation(t *testing.T) {
	db, project := openSnapshotSecretTestDB(t)
	oldRing := testDatabaseKeyring(t, "dek-old", 0x28)
	config := createSnapshotSecretWebhook(t, db, project)
	oldEnvelope := sealSnapshotField(
		t,
		oldRing,
		config.ID,
		"secret",
		"old-only-secret",
	)
	createSnapshotSecretRow(
		t,
		db,
		config,
		time.Now().UTC().Add(time.Hour),
		oldEnvelope,
		"",
		"",
	)
	rotating := newSnapshotTestKeyring(t, "dek-new", map[string]byte{
		"dek-old": 0x28,
		"dek-new": 0x29,
	})
	newOnly := testDatabaseKeyring(t, "dek-new", 0x29)

	if err := ValidateDatabaseSecrets(
		context.Background(),
		db,
		newOnly,
	); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("new-only validation before rotation error = %v, want ErrUnknownKey", err)
	}
	if _, err := RotateDatabaseSecrets(context.Background(), db, rotating); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDatabaseSecrets(context.Background(), db, newOnly); err != nil {
		t.Fatalf("new-only validation after complete rotation failed: %v", err)
	}
}

func TestWebhookSnapshotLiveBoundaryUsesStrictAfterAndFailsClosedOnZeroLifetime(
	t *testing.T,
) {
	maintenanceNow := time.Date(2026, 8, 10, 12, 0, 0, 123, time.UTC)
	tests := []struct {
		name      string
		expiresAt time.Time
		wantError error
	}{
		{
			name:      "expired_one_nanosecond_before",
			expiresAt: maintenanceNow.Add(-time.Nanosecond),
		},
		{
			name:      "exact_boundary_is_expired",
			expiresAt: maintenanceNow,
		},
		{
			name:      "one_nanosecond_after_is_live",
			expiresAt: maintenanceNow.Add(time.Nanosecond),
			wantError: ErrInvalidEnvelope,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, project := openSnapshotSecretTestDB(t)
			config := createSnapshotSecretWebhook(t, db, project)
			createSnapshotSecretRow(
				t,
				db,
				config,
				test.expiresAt,
				"cdsec:v1:malformed",
				"",
				"",
			)

			err := validateDatabaseSecretsAt(
				context.Background(),
				db,
				testDatabaseKeyring(t, "dek-boundary", 0x2a),
				maintenanceNow,
			)
			if test.wantError == nil && err != nil {
				t.Fatalf("validateDatabaseSecretsAt() error = %v, want nil", err)
			}
			if test.wantError != nil && !errors.Is(err, test.wantError) {
				t.Fatalf(
					"validateDatabaseSecretsAt() error = %v, want %v",
					err,
					test.wantError,
				)
			}
		})
	}

	t.Run("unshredded_zero_lifetime_is_corruption", func(t *testing.T) {
		db, project := openSnapshotSecretTestDB(t)
		config := createSnapshotSecretWebhook(t, db, project)
		snapshot := createSnapshotSecretRow(
			t,
			db,
			config,
			maintenanceNow.Add(time.Hour),
			"",
			"",
			"",
		)
		if err := db.Table("webhook_delivery_snapshots").
			Where("id = ?", snapshot.ID).
			UpdateColumn("credential_expires_at", time.Time{}).Error; err != nil {
			t.Fatal(err)
		}

		err := validateDatabaseSecretsAt(
			context.Background(),
			db,
			testDatabaseKeyring(t, "dek-boundary", 0x2a),
			maintenanceNow,
		)
		if err == nil {
			t.Fatal("zero lifetime validation unexpectedly succeeded")
		}
	})
}

func TestRotateWebhookSnapshotsSkipsExpiredEqualAndShreddedRows(t *testing.T) {
	db, project := openSnapshotSecretTestDB(t)
	maintenanceNow := time.Date(2026, 8, 10, 13, 0, 0, 456, time.UTC)
	oldRing := testDatabaseKeyring(t, "dek-old", 0x2b)
	rotating := newSnapshotTestKeyring(t, "dek-new", map[string]byte{
		"dek-old": 0x2b,
		"dek-new": 0x2c,
	})

	type snapshotExpectation struct {
		row     models.WebhookDeliverySnapshot
		old     string
		rotated bool
	}
	expectations := make([]snapshotExpectation, 0, 3)
	for index, expiresAt := range []time.Time{
		maintenanceNow.Add(-time.Nanosecond),
		maintenanceNow,
		maintenanceNow.Add(time.Nanosecond),
	} {
		config := createSnapshotSecretWebhook(t, db, project)
		envelope := sealSnapshotField(
			t,
			oldRing,
			config.ID,
			"secret",
			fmt.Sprintf("boundary-%d", index),
		)
		expectations = append(expectations, snapshotExpectation{
			row: createSnapshotSecretRow(
				t,
				db,
				config,
				expiresAt,
				envelope,
				"",
				"",
			),
			old:     envelope,
			rotated: expiresAt.After(maintenanceNow),
		})
	}

	shreddedConfig := createSnapshotSecretWebhook(t, db, project)
	shredded := createSnapshotSecretRow(
		t,
		db,
		shreddedConfig,
		maintenanceNow.Add(time.Hour),
		"",
		"",
		"",
	)
	shreddedAt := maintenanceNow.Add(-time.Minute)
	if err := db.Table("webhook_delivery_snapshots").
		Where("id = ?", shredded.ID).
		Updates(map[string]any{
			"credential_shredded_at":  shreddedAt,
			"credential_shred_reason": models.WebhookCredentialShredReasonExpired,
		}).Error; err != nil {
		t.Fatal(err)
	}

	report, err := rotateDatabaseSecretsAt(
		context.Background(),
		db,
		rotating,
		maintenanceNow,
	)
	if err != nil {
		t.Fatal(err)
	}
	if report != (SecretRotationReport{Rotated: 1}) {
		t.Fatalf("rotation report = %+v, want only now+1ns rotated", report)
	}
	for _, expectation := range expectations {
		var stored models.WebhookDeliverySnapshot
		if err := db.First(&stored, "id = ?", expectation.row.ID).Error; err != nil {
			t.Fatal(err)
		}
		if expectation.rotated {
			if stored.Secret == expectation.old {
				t.Fatal("live now+1ns snapshot was skipped")
			}
			continue
		}
		if stored.Secret != expectation.old {
			t.Fatal("expired or exact-boundary snapshot was rewritten")
		}
	}
}

func TestWebhookSnapshotCASDoesNotResurrectConcurrentShred(t *testing.T) {
	db, project := openSnapshotSecretTestDB(t)
	maintenanceNow := time.Date(2026, 8, 10, 14, 0, 0, 0, time.UTC)
	oldRing := testDatabaseKeyring(t, "dek-old", 0x2d)
	rotating := newSnapshotTestKeyring(t, "dek-new", map[string]byte{
		"dek-old": 0x2d,
		"dek-new": 0x2e,
	})
	config := createSnapshotSecretWebhook(t, db, project)
	oldEnvelope := sealSnapshotField(
		t,
		oldRing,
		config.ID,
		"secret",
		"stale-before-shred",
	)
	stale := createSnapshotSecretRow(
		t,
		db,
		config,
		maintenanceNow.Add(time.Hour),
		oldEnvelope,
		"",
		"",
	)

	shreddedAt := maintenanceNow.Add(time.Nanosecond)
	if err := db.Table("webhook_delivery_snapshots").
		Where("id = ?", stale.ID).
		Updates(map[string]any{
			"secret":                     "",
			"previous_secret":            "",
			"access_token":               "",
			"credential_shredded_at":     shreddedAt,
			"credential_shred_reason":    models.WebhookCredentialShredReasonRevoked,
			"previous_secret_expires_at": nil,
		}).Error; err != nil {
		t.Fatal(err)
	}

	report, skipped, err := rewrapWebhookSnapshotRow(
		db,
		rotating,
		stale,
		maintenanceNow,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !skipped || report != (SecretRotationReport{}) {
		t.Fatalf("stale shred CAS result = %+v skipped=%t", report, skipped)
	}
	var stored models.WebhookDeliverySnapshot
	if err := db.First(&stored, "id = ?", stale.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Secret != "" || stored.CredentialShreddedAt == nil {
		t.Fatal("concurrent shred was resurrected by stale rotation data")
	}
}

func TestWebhookSnapshotCASRejectsChangedLiveGeneration(t *testing.T) {
	db, project := openSnapshotSecretTestDB(t)
	maintenanceNow := time.Date(2026, 8, 10, 15, 0, 0, 0, time.UTC)
	oldRing := testDatabaseKeyring(t, "dek-old", 0x2f)
	rotating := newSnapshotTestKeyring(t, "dek-new", map[string]byte{
		"dek-old": 0x2f,
		"dek-new": 0x30,
	})
	config := createSnapshotSecretWebhook(t, db, project)
	staleEnvelope := sealSnapshotField(
		t,
		oldRing,
		config.ID,
		"secret",
		"stale-generation",
	)
	stale := createSnapshotSecretRow(
		t,
		db,
		config,
		maintenanceNow.Add(time.Hour),
		staleEnvelope,
		"",
		"",
	)
	currentEnvelope := sealSnapshotField(
		t,
		oldRing,
		config.ID,
		"secret",
		"current-generation",
	)
	if err := db.Table("webhook_delivery_snapshots").
		Where("id = ?", stale.ID).
		UpdateColumn("secret", currentEnvelope).Error; err != nil {
		t.Fatal(err)
	}

	report, skipped, err := rewrapWebhookSnapshotRow(
		db,
		rotating,
		stale,
		maintenanceNow,
	)
	if err == nil {
		t.Fatal("changed live snapshot generation unexpectedly succeeded")
	}
	if skipped || report != (SecretRotationReport{}) {
		t.Fatalf("changed live generation result = %+v skipped=%t", report, skipped)
	}
	var stored models.WebhookDeliverySnapshot
	if err := db.First(&stored, "id = ?", stale.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Secret != currentEnvelope {
		t.Fatal("changed live snapshot generation was overwritten")
	}
}

func openSnapshotSecretTestDB(t *testing.T) (*gorm.DB, models.Project) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
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
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(
		&models.User{},
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.WebhookConfig{},
		&models.WebhookDeliverySnapshot{},
		&models.EmailConfig{},
		&models.AgentPushNotificationConfig{},
	); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		ID:           1,
		Username:     "snapshot-owner",
		Email:        "snapshot-owner@example.test",
		PasswordHash: "hash",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	organization := models.Organization{
		Slug:   "snapshot-secrets",
		Name:   "Snapshot Secrets",
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
	return db, createSnapshotSecretProject(
		t,
		db,
		organization.ID,
		unit.ID,
		"ACTIVE",
		models.ProjectStatusActive,
	)
}

func createSnapshotSecretProject(
	t *testing.T,
	db *gorm.DB,
	organizationID uint,
	businessUnitID uint,
	key string,
	status models.ProjectStatus,
) models.Project {
	t.Helper()
	project := models.Project{
		OrganizationID: organizationID,
		BusinessUnitID: businessUnitID,
		Key:            models.ProjectKey(key),
		Name:           key,
		Status:         status,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	return project
}

func createSnapshotSecretWebhook(
	t *testing.T,
	db *gorm.DB,
	project models.Project,
) models.WebhookConfig {
	t.Helper()
	config := models.WebhookConfig{
		OrganizationID: project.OrganizationID,
		ProjectID:      project.ID,
		Name:           fmt.Sprintf("snapshot-webhook-%d", time.Now().UnixNano()),
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     "https://hooks.example.test",
		Status:         models.WebhookStatusActive,
		CreatedBy:      1,
		EnabledEventsObj: []models.WebhookEventType{
			models.WebhookEventTicketCreated,
		},
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	return config
}

func createSnapshotSecretRow(
	t *testing.T,
	db *gorm.DB,
	config models.WebhookConfig,
	expiresAt time.Time,
	secret string,
	previousSecret string,
	accessToken string,
) models.WebhookDeliverySnapshot {
	t.Helper()
	snapshot, err := models.NewWebhookDeliverySnapshot(
		config,
		fmt.Sprintf("evt-%d", time.Now().UnixNano()),
		expiresAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Secret = secret
	snapshot.PreviousSecret = previousSecret
	snapshot.AccessToken = accessToken
	if err := db.Create(snapshot).Error; err != nil {
		t.Fatal(err)
	}
	return *snapshot
}

func sealSnapshotField(
	t *testing.T,
	ring Protector,
	configID uint,
	field string,
	plaintext string,
) string {
	t.Helper()
	envelope, err := ring.Seal(
		[]byte(plaintext),
		FieldAAD(
			webhookSecretsTable,
			strconv.FormatUint(uint64(configID), 10),
			field,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func newSnapshotTestKeyring(
	t *testing.T,
	primary string,
	keyBytes map[string]byte,
) *Keyring {
	t.Helper()
	keys := make(map[string][]byte, len(keyBytes))
	for keyID, value := range keyBytes {
		keys[keyID] = bytes.Repeat([]byte{value}, 32)
	}
	ring, err := NewKeyring(primary, keys)
	if err != nil {
		t.Fatal(err)
	}
	return ring
}
