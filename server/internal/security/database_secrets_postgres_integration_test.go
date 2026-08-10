package security

import (
	"context"
	"database/sql"
	"encoding/json"
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
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"

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
		 VALUES (?, ?, ?, ?, NULL)`,
		pushID,
		1,
		activeProjectID,
		pushEnvelope,
	).Error; err != nil {
		t.Fatal(err)
	}
	authenticationPushID := "force-rls-authentication-push"
	authenticationEnvelope, err := oldRing.Seal(
		[]byte("push-authentication"),
		FieldAAD(
			a2aPushSecretsTable,
			authenticationPushID,
			"authentication",
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	encodedAuthentication, err := json.Marshal(authenticationEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(
		`INSERT INTO agent_push_notification_configs
			(id, organization_id, project_id, token, authentication)
		 VALUES (?, ?, ?, '', CAST(? AS json))`,
		authenticationPushID,
		1,
		activeProjectID,
		string(encodedAuthentication),
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
	assertSecretPostgresJSONValue(
		t,
		owner,
		authenticationPushID,
		encodedAuthentication,
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
	assertSecretPostgresJSONValue(
		t,
		owner,
		authenticationPushID,
		encodedAuthentication,
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
	if report.Rotated != 6 {
		t.Fatalf("complete PostgreSQL rotation report = %+v, want rotated=6", report)
	}
	if err := validateDatabaseSecretsAt(
		context.Background(),
		runtime,
		newOnly,
		now,
	); err != nil {
		t.Fatalf("new-only post-rotation validation failed: %v", err)
	}
	assertPostgresA2APushGenerationCAS(
		t,
		owner,
		runtime,
		rotating,
		models.ProjectScope{
			OrganizationID: 1,
			ProjectID:      activeProjectID,
		},
		oldRing,
	)

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

func TestDatabaseSecretMaintenancePostgresLocksOnlyLiveSnapshots(t *testing.T) {
	admin, owner, runtime, cleanup := openDatabaseSecretPostgresFixture(t)
	defer cleanup()

	maintenanceNow := time.Date(2026, 8, 10, 17, 0, 0, 0, time.UTC)
	projectID := insertSecretPostgresProject(
		t,
		owner,
		1,
		models.ProjectStatusActive,
	)
	configID := insertSecretPostgresWebhook(t, owner, 1, projectID)
	expiredID := "00000000-0000-7000-8000-000000000201"
	equalityID := "00000000-0000-7000-8000-000000000202"
	liveID := "00000000-0000-7000-8000-000000000203"
	insertSecretPostgresSnapshot(
		t,
		owner,
		expiredID,
		1,
		projectID,
		configID,
		"",
		maintenanceNow.Add(-time.Nanosecond),
	)
	insertSecretPostgresSnapshot(
		t,
		owner,
		equalityID,
		1,
		projectID,
		configID,
		"",
		maintenanceNow,
	)
	insertSecretPostgresSnapshot(
		t,
		owner,
		liveID,
		1,
		projectID,
		configID,
		"",
		maintenanceNow.Add(time.Microsecond),
	)

	assertPostgresMaintenanceSnapshotLockBoundary(
		t,
		admin,
		owner,
		runtime,
		testDatabaseKeyring(t, "dek-lock-boundary", 0x37),
		maintenanceNow,
		expiredID,
		equalityID,
		liveID,
	)
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
	assertDatabaseSecretPostgresCatalog(t, owner)
	installDatabaseSecretPostgresRLS(t, owner)
	if err := admin.Exec(
		"GRANT USAGE ON SCHEMA " + quotedSchema + " TO " + quotedRole,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Exec(
		"GRANT SELECT ON ALL TABLES IN SCHEMA " + quotedSchema +
			" TO " + quotedRole,
	).Error; err != nil {
		t.Fatal(err)
	}
	columnGrants := []string{
		"GRANT UPDATE (updated_at) ON " + quotedSchema +
			".projects TO " + quotedRole,
		"GRANT UPDATE (secret, previous_secret, access_token, updated_at) ON " +
			quotedSchema + ".webhook_configs TO " + quotedRole,
		"GRANT UPDATE (secret, previous_secret, access_token) ON " +
			quotedSchema + ".webhook_delivery_snapshots TO " + quotedRole,
		"GRANT UPDATE (token, authentication, updated_at) ON " +
			quotedSchema + ".agent_push_notification_configs TO " + quotedRole,
		"GRANT UPDATE (smtp_password) ON " + quotedSchema +
			".email_configs TO " + quotedRole,
	}
	for _, grant := range columnGrants {
		if err := admin.Exec(grant).Error; err != nil {
			t.Fatal(err)
		}
	}

	runtimeURL := ownerURL
	runtimeURL.User = url.User(roleName)
	runtime = open(runtimeURL.String())
	assertDatabaseSecretPostgresRoleAndColumnPermissions(
		t,
		admin,
		runtime,
		roleName,
	)
	return admin, owner, runtime, cleanup
}

func createDatabaseSecretPostgresTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	statements := []string{
		`CREATE TABLE projects (
			id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			organization_id bigint NOT NULL,
			status text NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE webhook_configs (
			id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			organization_id bigint NOT NULL,
			project_id bigint NOT NULL,
			secret text NOT NULL DEFAULT '',
			previous_secret text NOT NULL DEFAULT '',
			access_token text NOT NULL DEFAULT '',
			webhook_url text NOT NULL DEFAULT 'https://fixture.example.test/webhook',
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
			webhook_url text NOT NULL DEFAULT 'https://fixture.example.test/snapshot',
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
			authentication json,
			url text NOT NULL DEFAULT 'https://fixture.example.test/a2a',
			updated_at timestamptz NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE email_configs (
			id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			smtp_password text NOT NULL DEFAULT '',
			smtp_host text NOT NULL DEFAULT 'fixture.smtp.example.test'
		)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func assertDatabaseSecretPostgresCatalog(t *testing.T, db *gorm.DB) {
	t.Helper()
	var dataType string
	if err := db.Raw(
		`SELECT data_type
		   FROM information_schema.columns
		  WHERE table_schema = current_schema()
		    AND table_name = 'agent_push_notification_configs'
		    AND column_name = 'authentication'`,
	).Scan(&dataType).Error; err != nil {
		t.Fatal(err)
	}
	if dataType != "json" {
		t.Fatalf(
			"A2A authentication catalog data_type = %q, want production json",
			dataType,
		)
	}
}

func assertDatabaseSecretPostgresRoleAndColumnPermissions(
	t *testing.T,
	admin *gorm.DB,
	runtime *gorm.DB,
	roleName string,
) {
	t.Helper()
	role := struct {
		Superuser bool `gorm:"column:rolsuper"`
		BypassRLS bool `gorm:"column:rolbypassrls"`
	}{}
	if err := admin.Raw(
		`SELECT rolsuper, rolbypassrls
		   FROM pg_roles
		  WHERE rolname = ?`,
		roleName,
	).Scan(&role).Error; err != nil {
		t.Fatal(err)
	}
	if role.Superuser || role.BypassRLS {
		t.Fatalf("runtime role privileges = %+v, want NOSUPERUSER NOBYPASSRLS", role)
	}

	allowed := []string{
		"UPDATE projects SET updated_at = updated_at WHERE false",
		"UPDATE webhook_configs SET secret = secret WHERE false",
		"UPDATE webhook_delivery_snapshots SET secret = secret WHERE false",
		"UPDATE agent_push_notification_configs SET token = token WHERE false",
		"UPDATE email_configs SET smtp_password = smtp_password WHERE false",
	}
	for _, statement := range allowed {
		if err := runtime.Exec(statement).Error; err != nil {
			t.Fatalf("required column-level UPDATE was denied: %v", err)
		}
	}

	denied := []string{
		"UPDATE projects SET status = status WHERE false",
		"UPDATE webhook_configs SET webhook_url = webhook_url WHERE false",
		"UPDATE webhook_delivery_snapshots SET webhook_url = webhook_url WHERE false",
		"UPDATE agent_push_notification_configs SET url = url WHERE false",
		"UPDATE email_configs SET smtp_host = smtp_host WHERE false",
	}
	for _, statement := range denied {
		if err := runtime.Exec(statement).Error; err == nil {
			t.Fatalf("business-column UPDATE unexpectedly allowed: %s", statement)
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

func assertSecretPostgresJSONValue(
	t *testing.T,
	db *gorm.DB,
	id string,
	want []byte,
) {
	t.Helper()
	var got string
	if err := db.Raw(
		`SELECT authentication::text
		   FROM agent_push_notification_configs
		  WHERE id = ?`,
		id,
	).Scan(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatal("A2A authentication generation changed unexpectedly")
	}
}

func assertPostgresA2APushGenerationCAS(
	t *testing.T,
	owner *gorm.DB,
	runtime *gorm.DB,
	rotating Protector,
	scope models.ProjectScope,
	oldRing Protector,
) {
	t.Helper()
	const rowID = "force-rls-concurrent-generation"
	oldToken, err := oldRing.Seal(
		[]byte("stale-postgres-token"),
		FieldAAD(a2aPushSecretsTable, rowID, "token"),
	)
	if err != nil {
		t.Fatal(err)
	}
	oldAuthentication, err := oldRing.Seal(
		[]byte("stale-postgres-authentication"),
		FieldAAD(a2aPushSecretsTable, rowID, "authentication"),
	)
	if err != nil {
		t.Fatal(err)
	}
	encodedOldAuthentication, err := json.Marshal(oldAuthentication)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Exec(
		`INSERT INTO agent_push_notification_configs
			(id, organization_id, project_id, token, authentication)
		 VALUES (?, ?, ?, ?, CAST(? AS json))`,
		rowID,
		scope.OrganizationID,
		scope.ProjectID,
		oldToken,
		string(encodedOldAuthentication),
	).Error; err != nil {
		t.Fatal(err)
	}
	var stale models.AgentPushNotificationConfig
	if err := owner.First(&stale, "id = ?", rowID).Error; err != nil {
		t.Fatal(err)
	}

	currentToken, err := oldRing.Seal(
		[]byte("current-postgres-token"),
		FieldAAD(a2aPushSecretsTable, rowID, "token"),
	)
	if err != nil {
		t.Fatal(err)
	}
	currentAuthentication, err := oldRing.Seal(
		[]byte("current-postgres-authentication"),
		FieldAAD(a2aPushSecretsTable, rowID, "authentication"),
	)
	if err != nil {
		t.Fatal(err)
	}
	encodedCurrentAuthentication, err := json.Marshal(currentAuthentication)
	if err != nil {
		t.Fatal(err)
	}
	currentUpdatedAt := stale.UpdatedAt.Add(time.Second)
	if err := owner.Exec(
		`UPDATE agent_push_notification_configs
		    SET token = ?,
		        authentication = CAST(? AS json),
		        updated_at = ?
		  WHERE id = ?`,
		currentToken,
		string(encodedCurrentAuthentication),
		currentUpdatedAt,
		rowID,
	).Error; err != nil {
		t.Fatal(err)
	}

	var report SecretRotationReport
	rotationErr := runtime.WithContext(context.Background()).
		Transaction(func(tx *gorm.DB) error {
			if err := scopeddb.ConfigureProjectScopeTransaction(
				tx,
				scope,
			); err != nil {
				return err
			}
			var err error
			report, err = rotateA2APushRow(tx, rotating, stale)
			return err
		})
	if rotationErr == nil {
		t.Fatal("stale PostgreSQL A2A generation unexpectedly succeeded")
	}
	if report != (SecretRotationReport{}) {
		t.Fatalf("stale PostgreSQL A2A report = %+v, want zero", report)
	}
	assertSecretPostgresValue(
		t,
		owner,
		a2aPushSecretsTable,
		rowID,
		"token",
		currentToken,
	)
	assertSecretPostgresJSONValue(
		t,
		owner,
		rowID,
		encodedCurrentAuthentication,
	)
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
	var pauseOnce sync.Once
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseRead) })
	}
	defer release()
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
			pauseOnce.Do(func() {
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
	release()
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

func assertPostgresMaintenanceSnapshotLockBoundary(
	t *testing.T,
	admin *gorm.DB,
	owner *gorm.DB,
	runtime *gorm.DB,
	protector Protector,
	maintenanceNow time.Time,
	expiredID string,
	equalityID string,
	liveID string,
) {
	t.Helper()
	readReached := make(chan struct{})
	releaseRead := make(chan struct{})
	var pauseOnce sync.Once
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseRead) })
	}
	defer release()
	callbackName := "task9b_pause_live_snapshot_lock_query"
	if err := runtime.Callback().Query().After("gorm:query").Register(
		callbackName,
		func(query *gorm.DB) {
			tableName := query.Statement.Table
			if query.Statement.Schema != nil {
				tableName = query.Statement.Schema.Table
			}
			if tableName != "webhook_delivery_snapshots" ||
				!strings.Contains(
					strings.ToUpper(query.Statement.SQL.String()),
					"FOR UPDATE",
				) {
				return
			}
			pauseOnce.Do(func() {
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
			protector,
			maintenanceNow,
		)
		rotationDone <- err
	}()
	select {
	case <-readReached:
	case <-time.After(5 * time.Second):
		t.Fatal("rotation did not reach the live snapshot FOR UPDATE query")
	}

	assertNotLocked := func(id string) {
		t.Helper()
		pid, done, closeConnection := startPostgresSnapshotShred(
			t,
			owner,
			id,
			maintenanceNow,
		)
		defer closeConnection()
		state, err := waitForPostgresStatementState(admin, pid, done)
		if err != nil {
			t.Fatal(err)
		}
		if state != postgresStatementCompleted {
			release()
			waitPostgresOperation(t, rotationDone, "rotation")
			waitPostgresOperation(t, done, "non-live shred")
			t.Fatalf(
				"non-live snapshot %s waited on %s; maintenance must not lock it",
				id,
				state,
			)
		}
	}
	assertNotLocked(expiredID)
	assertNotLocked(equalityID)

	livePID, liveDone, closeLiveConnection := startPostgresSnapshotShred(
		t,
		owner,
		liveID,
		maintenanceNow,
	)
	defer closeLiveConnection()
	liveState, err := waitForPostgresStatementState(admin, livePID, liveDone)
	if err != nil {
		t.Fatal(err)
	}
	if liveState != postgresStatementWaitingOnLock {
		release()
		waitPostgresOperation(t, rotationDone, "rotation")
		if liveState != postgresStatementCompleted {
			waitPostgresOperation(t, liveDone, "live shred")
		}
		t.Fatalf("live snapshot state = %s, want row-lock wait", liveState)
	}
	release()
	waitPostgresOperation(t, rotationDone, "rotation")
	waitPostgresOperation(t, liveDone, "live shred")
}

type postgresStatementState string

const (
	postgresStatementCompleted     postgresStatementState = "completed"
	postgresStatementWaitingOnLock postgresStatementState = "waiting_on_lock"
)

func startPostgresSnapshotShred(
	t *testing.T,
	db *gorm.DB,
	id string,
	shreddedAt time.Time,
) (int, <-chan error, func()) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	connection, err := sqlDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	if err := connection.QueryRowContext(
		context.Background(),
		"SELECT pg_backend_pid()",
	).Scan(&pid); err != nil {
		_ = connection.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func(conn *sql.Conn) {
		_, err := conn.ExecContext(
			context.Background(),
			`UPDATE webhook_delivery_snapshots
			    SET secret = '',
			        previous_secret = '',
			        access_token = '',
			        credential_shredded_at = $1,
			        credential_shred_reason = 'expired'
			  WHERE id = $2`,
			shreddedAt,
			id,
		)
		done <- err
	}(connection)
	return pid, done, func() { _ = connection.Close() }
}

func waitForPostgresStatementState(
	admin *gorm.DB,
	pid int,
	done <-chan error,
) (postgresStatementState, error) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			if err != nil {
				return "", err
			}
			return postgresStatementCompleted, nil
		default:
		}
		var waitEventType string
		if err := admin.Raw(
			`SELECT COALESCE(wait_event_type, '')
			   FROM pg_stat_activity
			  WHERE pid = ?`,
			pid,
		).Scan(&waitEventType).Error; err != nil {
			return "", err
		}
		if waitEventType == "Lock" {
			return postgresStatementWaitingOnLock, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return "", errors.New("PostgreSQL statement state was not observable")
}

func waitPostgresOperation(
	t *testing.T,
	done <-chan error,
	operation string,
) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s failed: %v", operation, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not complete", operation)
	}
}
