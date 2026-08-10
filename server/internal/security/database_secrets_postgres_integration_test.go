package security

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestDatabaseSecretMaintenancePostgresForceRLSAndConcurrentShred(
	t *testing.T,
) {
	admin, owner, runtime, cleanup := openDatabaseSecretPostgresFixture(t)
	defer cleanup()

	now := time.Date(2026, 8, 10, 16, 0, 0, 0, time.UTC)
	oldRing := testDatabaseKeyring(t, "dek-old", 0x35)
	rotating := newSnapshotTestKeyring(t, "dek-new", map[string]byte{
		"dek-old": 0x35,
		"dek-new": 0x36,
	})
	newOnly := testDatabaseKeyring(t, "dek-new", 0x36)

	activeProjectID := insertSecretPostgresProject(
		t,
		owner,
		1,
		models.ProjectStatusActive,
	)
	archivedProjectID := insertSecretPostgresProject(
		t,
		owner,
		1,
		models.ProjectStatusArchived,
	)
	activeConfigID := insertSecretPostgresWebhook(
		t,
		owner,
		1,
		activeProjectID,
	)
	archivedConfigID := insertSecretPostgresWebhook(
		t,
		owner,
		1,
		archivedProjectID,
	)

	activeConfigEnvelope := sealSnapshotField(
		t,
		oldRing,
		activeConfigID,
		"secret",
		"active-config",
	)
	if err := owner.Table("webhook_configs").
		Where("id = ?", activeConfigID).
		UpdateColumn("secret", activeConfigEnvelope).Error; err != nil {
		t.Fatal(err)
	}
	activeSnapshotEnvelope := sealSnapshotField(
		t,
		oldRing,
		activeConfigID,
		"secret",
		"active-snapshot",
	)
	activeSnapshotID := "00000000-0000-7000-8000-000000000101"
	insertSecretPostgresSnapshot(
		t,
		owner,
		activeSnapshotID,
		1,
		activeProjectID,
		activeConfigID,
		activeSnapshotEnvelope,
		now.Add(time.Hour),
	)
	archivedSnapshotID := "00000000-0000-7000-8000-000000000102"
	insertSecretPostgresSnapshot(
		t,
		owner,
		archivedSnapshotID,
		1,
		archivedProjectID,
		archivedConfigID,
		"cdsec:v1:malformed",
		now.Add(time.Hour),
	)

	pushID := "force-rls-push"
	pushEnvelope, err := oldRing.Seal(
		[]byte("push-token"),
		FieldAAD(a2aPushSecretsTable, pushID, "token"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(
		`INSERT INTO agent_push_notification_configs
			(id, organization_id, project_id, token, authentication)
		 VALUES (?, ?, ?, ?, 'null'::jsonb)`,
		pushID,
		1,
		activeProjectID,
		pushEnvelope,
	).Error; err != nil {
		t.Fatal(err)
	}

	emailID := insertSecretPostgresEmail(t, owner)
	emailEnvelope, err := oldRing.Seal(
		[]byte("smtp-password"),
		FieldAAD(
			emailSecretsTable,
			strconv.FormatUint(uint64(emailID), 10),
			"smtp_password",
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Table("email_configs").
		Where("id = ?", emailID).
		UpdateColumn("smtp_password", emailEnvelope).Error; err != nil {
		t.Fatal(err)
	}

	var falseGreen []models.WebhookDeliverySnapshot
	if err := runtime.Table("webhook_delivery_snapshots").
		Where("credential_shredded_at IS NULL").
		Find(&falseGreen).Error; err != nil {
		t.Fatal(err)
	}
	if len(falseGreen) != 0 {
		t.Fatalf("unscoped FORCE RLS scan returned %d rows, want zero", len(falseGreen))
	}
	if err := ValidateDatabaseSecrets(
		context.Background(),
		runtime,
		oldRing,
	); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf(
			"privileged-style traversal error = %v, want archived snapshot rejection",
			err,
		)
	}
	if err := ValidateRuntimeDatabaseSecrets(
		context.Background(),
		runtime,
		oldRing,
	); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf(
			"runtime traversal error = %v, want archived snapshot rejection",
			err,
		)
	}

	report, err := rotateDatabaseSecretsAt(
		context.Background(),
		runtime,
		rotating,
		now,
	)
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("bad later snapshot rotation error = %v", err)
	}
	if report != (SecretRotationReport{}) {
		t.Fatalf("failed rotation report = %+v, want zero", report)
	}
	assertSecretPostgresValue(
		t,
		owner,
		"webhook_configs",
		activeConfigID,
		"secret",
		activeConfigEnvelope,
	)
	assertSecretPostgresValue(
		t,
		owner,
		"webhook_delivery_snapshots",
		activeSnapshotID,
		"secret",
		activeSnapshotEnvelope,
	)
	assertSecretPostgresValue(
		t,
		owner,
		"agent_push_notification_configs",
		pushID,
		"token",
		pushEnvelope,
	)

	archivedEnvelope := sealSnapshotField(
		t,
		oldRing,
		archivedConfigID,
		"secret",
		"archived-snapshot",
	)
	if err := owner.Table("webhook_delivery_snapshots").
		Where("id = ?", archivedSnapshotID).
		UpdateColumn("secret", archivedEnvelope).Error; err != nil {
		t.Fatal(err)
	}
	if err := owner.Table("email_configs").
		Where("id = ?", emailID).
		UpdateColumn("smtp_password", "unsupported-cleartext").Error; err != nil {
		t.Fatal(err)
	}
	report, err = rotateDatabaseSecretsAt(
		context.Background(),
		runtime,
		rotating,
		now,
	)
	if !errors.Is(err, ErrPlaintextSecret) {
		t.Fatalf("bad global row rotation error = %v", err)
	}
	if report != (SecretRotationReport{}) {
		t.Fatalf("failed global rotation report = %+v, want zero", report)
	}
	assertSecretPostgresValue(
		t,
		owner,
		"webhook_configs",
		activeConfigID,
		"secret",
		activeConfigEnvelope,
	)
	assertSecretPostgresValue(
		t,
		owner,
		"webhook_delivery_snapshots",
		activeSnapshotID,
		"secret",
		activeSnapshotEnvelope,
	)
	assertSecretPostgresValue(
		t,
		owner,
		"agent_push_notification_configs",
		pushID,
		"token",
		pushEnvelope,
	)

	if err := owner.Table("email_configs").
		Where("id = ?", emailID).
		UpdateColumn("smtp_password", emailEnvelope).Error; err != nil {
		t.Fatal(err)
	}
	if err := validateDatabaseSecretsAt(
		context.Background(),
		runtime,
		newOnly,
		now,
	); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("new-only pre-rotation validation error = %v", err)
	}
	report, err = rotateDatabaseSecretsAt(
		context.Background(),
		runtime,
		rotating,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.Rotated != 5 {
		t.Fatalf("complete PostgreSQL rotation report = %+v, want rotated=5", report)
	}
	if err := validateDatabaseSecretsAt(
		context.Background(),
		runtime,
		newOnly,
		now,
	); err != nil {
		t.Fatalf("new-only post-rotation validation failed: %v", err)
	}

	concurrentEnvelope := sealSnapshotField(
		t,
		oldRing,
		activeConfigID,
		"secret",
		"concurrent-snapshot",
	)
	concurrentSnapshotID := "00000000-0000-7000-8000-000000000103"
	insertSecretPostgresSnapshot(
		t,
		owner,
		concurrentSnapshotID,
		1,
		activeProjectID,
		activeConfigID,
		concurrentEnvelope,
		now.Add(time.Hour),
	)
	assertConcurrentSecretPostgresShredWins(
		t,
		owner,
		runtime,
		rotating,
		now,
		concurrentSnapshotID,
	)

	if err := admin.Exec("SELECT 1").Error; err != nil {
		t.Fatalf("PostgreSQL cleanup handle is unavailable: %v", err)
	}
}

func openDatabaseSecretPostgresFixture(
	t *testing.T,
) (*gorm.DB, *gorm.DB, *gorm.DB, func()) {
	t.Helper()
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip(
			"set CHRONODESK_POSTGRES_INTEGRATION=1 for PostgreSQL DEK maintenance evidence",
		)
	}
	rawDSN := strings.TrimSpace(
		os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"),
	)
	if rawDSN == "" {
		t.Fatal("CHRONODESK_POSTGRES_INTEGRATION_DSN is required")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatal("parse PostgreSQL integration DSN")
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		t.Fatal("PostgreSQL integration DSN must use a loopback host")
	}

	open := func(dsn string) *gorm.DB {
		db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		if err != nil {
			t.Fatal("open PostgreSQL integration database")
		}
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		sqlDB.SetMaxOpenConns(4)
		return db
	}
	admin := open(rawDSN)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	schemaName := "task9b_dek_" + suffix
	roleName := "task9b_runtime_" + suffix
	quotedSchema := `"` + schemaName + `"`
	quotedRole := `"` + roleName + `"`
	if err := admin.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Exec(
		"CREATE ROLE " + quotedRole + " LOGIN NOSUPERUSER NOBYPASSRLS",
	).Error; err != nil {
		_ = admin.Exec("DROP SCHEMA " + quotedSchema + " CASCADE").Error
		if sqlDB, dbErr := admin.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
		t.Fatal(err)
	}

	var owner *gorm.DB
	var runtime *gorm.DB
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			for _, db := range []*gorm.DB{runtime, owner} {
				if db == nil {
					continue
				}
				sqlDB, dbErr := db.DB()
				if dbErr == nil {
					_ = sqlDB.Close()
				}
			}
			_ = admin.Exec(
				"DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE",
			).Error
			_ = admin.Exec("DROP ROLE IF EXISTS " + quotedRole).Error
			sqlDB, dbErr := admin.DB()
			if dbErr == nil {
				_ = sqlDB.Close()
			}
		})
	}
	t.Cleanup(cleanup)

	ownerURL := *parsed
	ownerQuery := ownerURL.Query()
	ownerQuery.Set("search_path", schemaName)
	ownerURL.RawQuery = ownerQuery.Encode()
	owner = open(ownerURL.String())
	createDatabaseSecretPostgresTables(t, owner)
	installDatabaseSecretPostgresRLS(t, owner)
	if err := admin.Exec(
		"GRANT USAGE ON SCHEMA " + quotedSchema + " TO " + quotedRole,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Exec(
		"GRANT SELECT, UPDATE ON " + quotedSchema +
			".projects TO " + quotedRole,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Exec(
		"GRANT SELECT, UPDATE ON " + quotedSchema +
			".webhook_configs, " + quotedSchema +
			".webhook_delivery_snapshots, " + quotedSchema +
			".agent_push_notification_configs, " + quotedSchema +
			".email_configs TO " + quotedRole,
	).Error; err != nil {
		t.Fatal(err)
	}

	runtimeURL := ownerURL
	runtimeURL.User = url.User(roleName)
	runtime = open(runtimeURL.String())
	return admin, owner, runtime, cleanup
}

func createDatabaseSecretPostgresTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	statements := []string{
		`CREATE TABLE projects (
			id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			organization_id bigint NOT NULL,
			status text NOT NULL
		)`,
		`CREATE TABLE webhook_configs (
			id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			organization_id bigint NOT NULL,
			project_id bigint NOT NULL,
			secret text NOT NULL DEFAULT '',
			previous_secret text NOT NULL DEFAULT '',
			access_token text NOT NULL DEFAULT '',
			updated_at timestamptz NOT NULL DEFAULT now(),
			deleted_at timestamptz
		)`,
		`CREATE TABLE webhook_delivery_snapshots (
			id text PRIMARY KEY,
			organization_id bigint NOT NULL,
			project_id bigint NOT NULL,
			config_id bigint NOT NULL,
			secret text NOT NULL DEFAULT '',
			previous_secret text NOT NULL DEFAULT '',
			access_token text NOT NULL DEFAULT '',
			credential_expires_at timestamptz NOT NULL,
			credential_shredded_at timestamptz,
			credential_shred_reason text,
			CONSTRAINT task9b_shred_state CHECK (
				(credential_shredded_at IS NULL AND credential_shred_reason IS NULL)
				OR
				(credential_shredded_at IS NOT NULL
				 AND credential_shred_reason IS NOT NULL
				 AND secret = '' AND previous_secret = '' AND access_token = '')
			)
		)`,
		`CREATE TABLE agent_push_notification_configs (
			id text PRIMARY KEY,
			organization_id bigint NOT NULL,
			project_id bigint NOT NULL,
			token text NOT NULL DEFAULT '',
			authentication jsonb,
			updated_at timestamptz NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE email_configs (
			id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			smtp_password text NOT NULL DEFAULT ''
		)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func installDatabaseSecretPostgresRLS(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, tableName := range []string{
		"webhook_configs",
		"webhook_delivery_snapshots",
		"agent_push_notification_configs",
	} {
		predicate := `(organization_id = NULLIF(current_setting('chronodesk.organization_id', true), '')::bigint
			AND project_id = NULLIF(current_setting('chronodesk.project_id', true), '')::bigint)`
		for _, statement := range []string{
			"ALTER TABLE " + tableName + " ENABLE ROW LEVEL SECURITY",
			"ALTER TABLE " + tableName + " FORCE ROW LEVEL SECURITY",
			"CREATE POLICY chronodesk_project_scope ON " + tableName +
				" USING (" + predicate + ") WITH CHECK (" + predicate + ")",
		} {
			if err := db.Exec(statement).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
}

func insertSecretPostgresProject(
	t *testing.T,
	db *gorm.DB,
	organizationID uint,
	status models.ProjectStatus,
) uint {
	t.Helper()
	var id uint
	if err := db.Raw(
		`INSERT INTO projects (organization_id, status)
		 VALUES (?, ?) RETURNING id`,
		organizationID,
		status,
	).Scan(&id).Error; err != nil {
		t.Fatal(err)
	}
	return id
}

func insertSecretPostgresWebhook(
	t *testing.T,
	db *gorm.DB,
	organizationID uint,
	projectID uint,
) uint {
	t.Helper()
	var id uint
	if err := db.Raw(
		`INSERT INTO webhook_configs (organization_id, project_id)
		 VALUES (?, ?) RETURNING id`,
		organizationID,
		projectID,
	).Scan(&id).Error; err != nil {
		t.Fatal(err)
	}
	return id
}

func insertSecretPostgresSnapshot(
	t *testing.T,
	db *gorm.DB,
	id string,
	organizationID uint,
	projectID uint,
	configID uint,
	secret string,
	expiresAt time.Time,
) {
	t.Helper()
	if err := db.Exec(
		`INSERT INTO webhook_delivery_snapshots
			(id, organization_id, project_id, config_id, secret, credential_expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id,
		organizationID,
		projectID,
		configID,
		secret,
		expiresAt,
	).Error; err != nil {
		t.Fatal(err)
	}
}

func insertSecretPostgresEmail(t *testing.T, db *gorm.DB) uint {
	t.Helper()
	var id uint
	if err := db.Raw(
		`INSERT INTO email_configs DEFAULT VALUES RETURNING id`,
	).Scan(&id).Error; err != nil {
		t.Fatal(err)
	}
	return id
}

func assertSecretPostgresValue(
	t *testing.T,
	db *gorm.DB,
	tableName string,
	id any,
	column string,
	want string,
) {
	t.Helper()
	var got string
	if err := db.Table(tableName).
		Select(column).
		Where("id = ?", id).
		Scan(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s.%s changed unexpectedly", tableName, column)
	}
}

func assertConcurrentSecretPostgresShredWins(
	t *testing.T,
	owner *gorm.DB,
	runtime *gorm.DB,
	rotating Protector,
	maintenanceNow time.Time,
	snapshotID string,
) {
	t.Helper()
	readReached := make(chan struct{})
	releaseRead := make(chan struct{})
	var once sync.Once
	callbackName := "task9b_pause_after_snapshot_lock"
	if err := runtime.Callback().Query().After("gorm:query").Register(
		callbackName,
		func(query *gorm.DB) {
			tableName := query.Statement.Table
			if query.Statement.Schema != nil {
				tableName = query.Statement.Schema.Table
			}
			if tableName != "webhook_delivery_snapshots" {
				return
			}
			once.Do(func() {
				close(readReached)
				<-releaseRead
			})
		},
	); err != nil {
		t.Fatal(err)
	}
	defer runtime.Callback().Query().Remove(callbackName)

	rotationDone := make(chan error, 1)
	go func() {
		_, err := rotateDatabaseSecretsAt(
			context.Background(),
			runtime,
			rotating,
			maintenanceNow,
		)
		rotationDone <- err
	}()
	select {
	case <-readReached:
	case <-time.After(5 * time.Second):
		t.Fatal("rotation did not reach the locked snapshot query")
	}

	shredStarted := make(chan struct{})
	shredDone := make(chan error, 1)
	go func() {
		close(shredStarted)
		shreddedAt := maintenanceNow.Add(time.Nanosecond)
		shredDone <- owner.Table("webhook_delivery_snapshots").
			Where("id = ?", snapshotID).
			Updates(map[string]any{
				"secret":                  "",
				"previous_secret":         "",
				"access_token":            "",
				"credential_shredded_at":  shreddedAt,
				"credential_shred_reason": string(models.WebhookCredentialShredReasonRevoked),
			}).Error
	}()
	<-shredStarted
	select {
	case err := <-shredDone:
		t.Fatalf("concurrent shred bypassed snapshot row lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(releaseRead)
	select {
	case err := <-rotationDone:
		if err != nil {
			t.Fatalf("concurrent rotation failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent rotation did not complete")
	}
	select {
	case err := <-shredDone:
		if err != nil {
			t.Fatalf("concurrent shred failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent shred did not complete after rotation")
	}

	var stored struct {
		Secret                string
		CredentialShreddedAt  *time.Time
		CredentialShredReason *string
	}
	if err := owner.Table("webhook_delivery_snapshots").
		Select("secret", "credential_shredded_at", "credential_shred_reason").
		Where("id = ?", snapshotID).
		Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Secret != "" ||
		stored.CredentialShreddedAt == nil ||
		stored.CredentialShredReason == nil {
		t.Fatal("concurrent shred did not remain the final monotonic state")
	}
}
