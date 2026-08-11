package auth

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/security"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	atomicRegistrationPostgresLockTimeout      = 5 * time.Second
	atomicRegistrationPostgresStatementTimeout = 10 * time.Second
)

type atomicRegistrationPostgresFixture struct {
	admin        *gorm.DB
	owner        *gorm.DB
	writer       *gorm.DB
	runtime      *gorm.DB
	repository   *GormAuthEmailOutboxRepository
	protector    security.Protector
	scope        models.ProjectScope
	schemaName   string
	quotedSchema string
	writerApp    string
}

type atomicRegistrationPostgresBarrier struct {
	table       string
	reached     chan struct{}
	release     chan struct{}
	reachedOnce sync.Once
	releaseOnce sync.Once
	pid         int
}

func TestPostgresAtomicRegistrationUsesNonOwnerForceRLSScopeAndUniqueIndexes(
	t *testing.T,
) {
	fixture := openAtomicRegistrationPostgresFixture(
		t,
		"rls",
		boolPointer(false),
	)
	assertAtomicRegistrationPostgresRuntimeBoundary(t, fixture)
	assertAtomicRegistrationPostgresDeployedIdentityIndexes(t, fixture.admin)
	assertAtomicRegistrationPostgresIdentityIndexes(t, fixture)

	command := atomicRegistrationPostgresCommand(
		"postgres-force-rls",
		"postgres-force-rls@example.test",
		false,
	)
	result, err := fixture.repository.CommitRegistration(
		context.Background(),
		command,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Session == nil ||
		result.Session.AccessToken == "" ||
		result.Session.RefreshToken == "" {
		t.Fatalf("non-owner FORCE RLS registration result = %+v", result)
	}
	assertAtomicRegistrationPostgresCounts(t, fixture, 1, false)
}

func TestPostgresAtomicRegistrationConcurrentDuplicateHasOneCompleteWinner(
	t *testing.T,
) {
	tests := []struct {
		name      string
		usernames [2]string
		emails    [2]string
	}{
		{
			name:      "email",
			usernames: [2]string{"pg-duplicate-email-a", "pg-duplicate-email-b"},
			emails: [2]string{
				"pg-duplicate-email@example.test",
				"pg-duplicate-email@example.test",
			},
		},
		{
			name:      "username",
			usernames: [2]string{"pg-duplicate-username", "pg-duplicate-username"},
			emails: [2]string{
				"pg-duplicate-username-a@example.test",
				"pg-duplicate-username-b@example.test",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := openAtomicRegistrationPostgresFixture(
				t,
				"duplicate_"+test.name,
				boolPointer(false),
			)
			start := make(chan struct{})
			errs := make(chan error, 2)
			results := make(chan *RegistrationCommitResult, 2)
			var wait sync.WaitGroup
			for index := 0; index < 2; index++ {
				command := atomicRegistrationPostgresCommand(
					test.usernames[index],
					test.emails[index],
					false,
				)
				wait.Add(1)
				go func() {
					defer wait.Done()
					<-start
					result, err := fixture.repository.CommitRegistration(
						context.Background(),
						command,
					)
					results <- result
					errs <- err
				}()
			}
			close(start)
			wait.Wait()
			close(results)
			close(errs)

			var winners, conflicts int
			for result := range results {
				if result != nil {
					if result.Session == nil ||
						result.Session.AccessToken == "" ||
						result.Session.RefreshToken == "" {
						t.Fatalf("duplicate winner is incomplete: %+v", result)
					}
					winners++
				}
			}
			for err := range errs {
				switch {
				case err == nil:
				case errors.Is(err, ErrUserExists):
					conflicts++
				default:
					t.Fatalf("duplicate registration error = %v", err)
				}
			}
			if winners != 1 || conflicts != 1 {
				t.Fatalf(
					"duplicate winners/conflicts = %d/%d, want 1/1",
					winners,
					conflicts,
				)
			}
			assertAtomicRegistrationPostgresCounts(t, fixture, 1, false)
		})
	}
}

func TestPostgresAtomicRegistrationPrimaryKeyDriftIsNotIdentityConflict(
	t *testing.T,
) {
	fixture := openAtomicRegistrationPostgresFixture(
		t,
		"primary_key_drift",
		boolPointer(false),
	)
	blocker := models.User{
		ID:           1,
		Username:     "primary-key-blocker",
		Email:        "primary-key-blocker@example.test",
		PasswordHash: "primary-key-blocker-hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := fixture.owner.Create(&blocker).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.owner.Exec(`
		SELECT setval(
			pg_get_serial_sequence('users', 'id'),
			1,
			false
		)
	`).Error; err != nil {
		t.Fatal(err)
	}

	result, err := fixture.repository.CommitRegistration(
		context.Background(),
		atomicRegistrationPostgresCommand(
			"primary-key-drift-registration",
			"primary-key-drift-registration@example.test",
			false,
		),
	)
	if result != nil || err == nil || errors.Is(err, ErrUserExists) {
		t.Fatalf(
			"primary-key drift result/error = %+v/%v, want nil/non-identity error",
			result,
			err,
		)
	}
	status, _ := registrationFailureHTTPResponse(err)
	if status == 409 {
		t.Fatalf("primary-key drift mapped to HTTP 409: %v", err)
	}
	for table, want := range map[string]int64{
		"users":               1,
		"user_profiles":       0,
		"email_verifications": 0,
		"refresh_tokens":      0,
		"login_histories":     0,
		"login_attempts":      0,
		"domain_events":       0,
		"outbox_deliveries":   0,
	} {
		var count int64
		query := "SELECT COUNT(*) FROM " + fixture.quotedSchema + "." +
			quoteAtomicRegistrationPostgresIdentifier(table)
		if err := fixture.admin.Raw(query).Scan(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Errorf("%s count = %d, want %d", table, count, want)
		}
	}
}

func TestPostgresAtomicRegistrationDuplicateIdentityDoesNotLogSecrets(
	t *testing.T,
) {
	fixture := openAtomicRegistrationPostgresFixture(
		t,
		"duplicate_log_safety",
		boolPointer(false),
	)
	var logs bytes.Buffer
	fixture.runtime.Config.Logger = logger.New(
		log.New(&logs, "", 0),
		logger.Config{
			LogLevel:             logger.Error,
			Colorful:             false,
			ParameterizedQueries: false,
		},
	)
	first := atomicRegistrationPostgresCommand(
		"postgres-duplicate-log-a",
		"postgres-duplicate-log@example.test",
		false,
	)
	if _, err := fixture.repository.CommitRegistration(
		context.Background(),
		first,
	); err != nil {
		t.Fatal(err)
	}
	second := atomicRegistrationPostgresCommand(
		"postgres-duplicate-log-b",
		"postgres-duplicate-log@example.test",
		false,
	)
	second.User.PasswordHash = "postgres-duplicate-password-hash-secret"
	result, err := fixture.repository.CommitRegistration(
		context.Background(),
		second,
	)
	if result != nil || !errors.Is(err, ErrUserExists) {
		t.Fatalf("PostgreSQL duplicate result/error = %+v/%v", result, err)
	}
	logged := logs.String()
	for _, secret := range []string{
		second.User.Email,
		second.User.Username,
		second.User.PasswordHash,
		"registration-refresh-bearer",
		"registration-access-bearer",
	} {
		if strings.Contains(logged, secret) {
			t.Fatalf(
				"PostgreSQL duplicate logs exposed %q: %s",
				secret,
				logged,
			)
		}
	}
}

func TestPostgresAtomicRegistrationPolicyTransitionsSerializeBothOrderings(
	t *testing.T,
) {
	tests := []struct {
		name           string
		initialPolicy  *bool
		expectedPolicy bool
		releaseTable   string
		writePolicy    func(*gorm.DB) error
	}{
		{
			name:           "disabled_to_enabled",
			initialPolicy:  boolPointer(false),
			expectedPolicy: false,
			releaseTable:   "refresh_tokens",
			writePolicy: func(db *gorm.DB) error {
				return db.Model(&models.EmailConfig{}).
					Where("is_active = ?", true).
					Update("email_verification_enabled", true).Error
			},
		},
		{
			name:           "no_policy_to_first_enabled",
			expectedPolicy: false,
			releaseTable:   "refresh_tokens",
			writePolicy: func(db *gorm.DB) error {
				return db.Create(&models.EmailConfig{
					EmailVerificationEnabled: true,
					IsActive:                 true,
				}).Error
			},
		},
		{
			name:           "enabled_to_disabled",
			initialPolicy:  boolPointer(true),
			expectedPolicy: true,
			releaseTable:   "outbox_deliveries",
			writePolicy: func(db *gorm.DB) error {
				return db.Model(&models.EmailConfig{}).
					Where("is_active = ?", true).
					Update("email_verification_enabled", false).Error
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name+"_registration_first", func(t *testing.T) {
			fixture := openAtomicRegistrationPostgresFixture(
				t,
				test.name+"_registration_first",
				test.initialPolicy,
			)
			barrier := newAtomicRegistrationPostgresBarrier(test.releaseTable)
			t.Cleanup(barrier.Release)
			const callbackName = "test:atomic-registration-postgres-policy"
			if err := fixture.runtime.Callback().Create().
				After("gorm:create").
				Register(callbackName, barrier.AfterCreate); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = fixture.runtime.Callback().Create().Remove(callbackName)
			})

			command := atomicRegistrationPostgresCommand(
				"pg-policy-registration-first",
				"pg-policy-registration-first@example.test",
				test.expectedPolicy,
			)
			registrationResult := make(chan error, 1)
			go func() {
				_, err := fixture.repository.CommitRegistration(
					context.Background(),
					command,
				)
				registrationResult <- err
			}()
			waitAtomicRegistrationPostgresBarrier(t, barrier, registrationResult)

			writerResult := make(chan error, 1)
			go func() {
				writerResult <- test.writePolicy(fixture.writer)
			}()
			waitForAtomicRegistrationPostgresPolicyWriter(
				t,
				fixture,
				barrier.pid,
			)
			barrier.Release()
			if err := waitAtomicRegistrationPostgresResult(
				t,
				registrationResult,
				"registration",
			); err != nil {
				t.Fatal(err)
			}
			if err := waitAtomicRegistrationPostgresResult(
				t,
				writerResult,
				"policy writer",
			); err != nil {
				t.Fatal(err)
			}
			assertAtomicRegistrationPostgresCounts(
				t,
				fixture,
				1,
				test.expectedPolicy,
			)
		})

		t.Run(test.name+"_writer_first", func(t *testing.T) {
			fixture := openAtomicRegistrationPostgresFixture(
				t,
				test.name+"_writer_first",
				test.initialPolicy,
			)
			if err := test.writePolicy(fixture.writer); err != nil {
				t.Fatal(err)
			}
			result, err := fixture.repository.CommitRegistration(
				context.Background(),
				atomicRegistrationPostgresCommand(
					"pg-policy-writer-first",
					"pg-policy-writer-first@example.test",
					test.expectedPolicy,
				),
			)
			if result != nil ||
				!errors.Is(err, ErrEmailVerificationPolicyChanged) {
				t.Fatalf(
					"writer-first registration result/error = %+v/%v",
					result,
					err,
				)
			}
			assertAtomicRegistrationPostgresCounts(
				t,
				fixture,
				0,
				test.expectedPolicy,
			)
		})
	}
}

func atomicRegistrationPostgresCommand(
	username string,
	email string,
	verificationEnabled bool,
) *RegistrationCommit {
	committedAt := time.Now().UTC().Truncate(time.Microsecond)
	passwordChangedAt := committedAt
	if !verificationEnabled {
		passwordChangedAt = committedAt.Add(-time.Microsecond)
	}
	user := &User{
		Username:          username,
		Email:             email,
		PasswordHash:      "postgres-registration-password-hash",
		PlatformRole:      PlatformRoleMember,
		Status:            StatusActive,
		EmailVerified:     !verificationEnabled,
		PasswordChangedAt: &passwordChangedAt,
	}
	command := &RegistrationCommit{
		CommittedAt: committedAt,
		User:        user,
		Profile: &UserProfile{
			Timezone: "UTC",
			Language: DefaultProfileLanguage,
		},
		ExpectedEmailPolicy: &EmailVerificationPolicySnapshot{
			Enabled: verificationEnabled,
		},
	}
	if verificationEnabled {
		command.Verification = &EmailVerification{
			Email:     email,
			Token:     "postgres-registration-verification-" + username,
			ExpiresAt: committedAt.Add(time.Hour),
		}
		return command
	}
	user.EmailVerifiedAt = &committedAt
	user.LastLoginAt = &committedAt
	command.SessionIssuedAt = &committedAt
	command.BuildSession = registrationSessionBuilderForTest(
		user,
		"127.0.0.1",
		"PostgreSQL atomic registration test",
	)
	return command
}

func openAtomicRegistrationPostgresFixture(
	t *testing.T,
	scenario string,
	initialPolicy *bool,
) *atomicRegistrationPostgresFixture {
	t.Helper()
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip(
			"set CHRONODESK_POSTGRES_INTEGRATION=1 for PostgreSQL atomic registration evidence",
		)
	}
	rawDSN := strings.TrimSpace(os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"))
	if rawDSN == "" {
		t.Fatal("CHRONODESK_POSTGRES_INTEGRATION_DSN is required")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatal("parse PostgreSQL integration DSN: invalid URL")
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal("PostgreSQL atomic registration test requires loopback")
		}
	}

	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	schemaName := "chronodesk_auth_registration_" +
		strings.ReplaceAll(scenario, "-", "_") + "_" + suffix
	ownerRole := "chronodesk_auth_registration_owner_" + suffix
	runtimeRole := "chronodesk_auth_registration_runtime_" + suffix
	ownerPassword := "ChronoDeskRegistrationOwner" + suffix + "!"
	runtimePassword := "ChronoDeskRegistrationRuntime" + suffix + "!"
	quotedSchema := quoteAtomicRegistrationPostgresIdentifier(schemaName)
	quotedOwner := quoteAtomicRegistrationPostgresIdentifier(ownerRole)
	quotedRuntime := quoteAtomicRegistrationPostgresIdentifier(runtimeRole)
	writerApp := "chronodesk-auth-registration-writer-" + suffix

	config := &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	}
	admin, err := gorm.Open(postgres.Open(rawDSN), config)
	if err != nil {
		t.Fatalf("open PostgreSQL atomic registration admin: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	var pools []io.Closer
	ownerCreated := false
	runtimeCreated := false
	schemaCreated := false
	t.Cleanup(func() {
		for _, pool := range pools {
			_ = pool.Close()
		}
		if schemaCreated {
			_ = admin.Exec("DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE").Error
		}
		if runtimeCreated {
			_ = admin.Exec("DROP ROLE IF EXISTS " + quotedRuntime).Error
		}
		if ownerCreated {
			_ = admin.Exec("DROP ROLE IF EXISTS " + quotedOwner).Error
		}
		_ = adminSQL.Close()
	})
	if err := admin.Exec(
		"CREATE ROLE " + quotedOwner +
			" LOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS PASSWORD " +
			quoteAtomicRegistrationPostgresLiteral(ownerPassword),
	).Error; err != nil {
		t.Fatal(err)
	}
	ownerCreated = true
	if err := admin.Exec(
		"CREATE ROLE " + quotedRuntime +
			" LOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS PASSWORD " +
			quoteAtomicRegistrationPostgresLiteral(runtimePassword),
	).Error; err != nil {
		t.Fatal(err)
	}
	runtimeCreated = true
	if err := admin.Exec(
		"CREATE SCHEMA " + quotedSchema + " AUTHORIZATION " + quotedOwner,
	).Error; err != nil {
		t.Fatal(err)
	}
	schemaCreated = true

	openRole := func(
		role string,
		password string,
		applicationName string,
	) *gorm.DB {
		roleURL := *parsed
		roleURL.User = url.UserPassword(role, password)
		query := roleURL.Query()
		query.Set("search_path", schemaName)
		query.Set("application_name", applicationName)
		query.Set("connect_timeout", "3")
		query.Set(
			"options",
			fmt.Sprintf(
				"-c lock_timeout=%dms -c statement_timeout=%dms",
				atomicRegistrationPostgresLockTimeout.Milliseconds(),
				atomicRegistrationPostgresStatementTimeout.Milliseconds(),
			),
		)
		roleURL.RawQuery = query.Encode()
		db, openErr := gorm.Open(postgres.Open(roleURL.String()), config)
		if openErr != nil {
			t.Fatal(openErr)
		}
		sqlDB, sqlErr := db.DB()
		if sqlErr != nil {
			t.Fatal(sqlErr)
		}
		sqlDB.SetMaxOpenConns(4)
		sqlDB.SetMaxIdleConns(4)
		pools = append(pools, sqlDB)
		return db
	}
	owner := openRole(ownerRole, ownerPassword, "chronodesk-auth-registration-owner-"+suffix)
	writer := openRole(ownerRole, ownerPassword, writerApp)
	runtime := openRole(
		runtimeRole,
		runtimePassword,
		"chronodesk-auth-registration-runtime-"+suffix,
	)

	migrationDB := owner.Session(&gorm.Session{NewDB: true})
	migrationDB.Config.IgnoreRelationshipsWhenMigrating = true
	if err := migrationDB.AutoMigrate(
		&models.User{},
		&models.UserProfile{},
		&models.EmailConfig{},
		&EmailVerification{},
		&RefreshToken{},
		&models.LoginHistory{},
		&LoginAttempt{},
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatalf("migrate PostgreSQL atomic registration fixture: %v", err)
	}
	scope := seedAtomicRegistrationPostgresDefaultProject(t, owner)
	if initialPolicy != nil {
		if err := owner.Create(&models.EmailConfig{
			EmailVerificationEnabled: *initialPolicy,
			IsActive:                 true,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"domain_events", "outbox_deliveries"} {
		quotedTable := quotedSchema + "." +
			quoteAtomicRegistrationPostgresIdentifier(table)
		for _, statement := range []string{
			"CREATE POLICY chronodesk_project_scope ON " + quotedTable +
				` FOR ALL TO PUBLIC USING (
					organization_id = NULLIF(current_setting('chronodesk.organization_id', true), '')::bigint
					AND project_id = NULLIF(current_setting('chronodesk.project_id', true), '')::bigint
				) WITH CHECK (
					organization_id = NULLIF(current_setting('chronodesk.organization_id', true), '')::bigint
					AND project_id = NULLIF(current_setting('chronodesk.project_id', true), '')::bigint
				)`,
			"ALTER TABLE " + quotedTable + " ENABLE ROW LEVEL SECURITY",
			"ALTER TABLE " + quotedTable + " FORCE ROW LEVEL SECURITY",
		} {
			if err := owner.Exec(statement).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, statement := range []string{
		"GRANT USAGE ON SCHEMA " + quotedSchema + " TO " + quotedRuntime,
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA " +
			quotedSchema + " TO " + quotedRuntime,
		"GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA " +
			quotedSchema + " TO " + quotedRuntime,
	} {
		if err := owner.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	resolvedScope, err := resolveActiveDefaultAuthProjectScope(
		context.Background(),
		runtime,
	)
	if err != nil {
		t.Fatalf("resolve runtime DEFAULT scope: %v", err)
	}
	if resolvedScope != scope {
		t.Fatalf("runtime DEFAULT scope = %+v, want %+v", resolvedScope, scope)
	}
	protector, err := security.NewKeyring(
		"postgres-registration-test",
		map[string][]byte{
			"postgres-registration-test": bytes.Repeat([]byte{0x64}, 32),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewGormAuthEmailOutboxRepository(
		runtime,
		protector,
		scope,
		"urn:test:postgres-auth-registration",
	)
	if err != nil {
		t.Fatal(err)
	}
	return &atomicRegistrationPostgresFixture{
		admin:        admin,
		owner:        owner,
		writer:       writer,
		runtime:      runtime,
		repository:   repository,
		protector:    protector,
		scope:        scope,
		schemaName:   schemaName,
		quotedSchema: quotedSchema,
		writerApp:    writerApp,
	}
}

func seedAtomicRegistrationPostgresDefaultProject(
	t *testing.T,
	db *gorm.DB,
) models.ProjectScope {
	t.Helper()
	organization := models.Organization{
		Slug:   "default-" + strconv.FormatInt(time.Now().UnixNano(), 36),
		Name:   "Default",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "DEFAULT",
		Name:           "Default",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	project := models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            models.ProjectKey("DEFAULT"),
		Name:           "Default",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	return project.Scope()
}

func assertAtomicRegistrationPostgresRuntimeBoundary(
	t *testing.T,
	fixture *atomicRegistrationPostgresFixture,
) {
	t.Helper()
	var state struct {
		IsOwner       bool  `gorm:"column:is_owner"`
		IsSuperuser   bool  `gorm:"column:is_superuser"`
		BypassRLS     bool  `gorm:"column:bypass_rls"`
		ProtectedRows int64 `gorm:"column:protected_rows"`
	}
	if err := fixture.runtime.Raw(`
		SELECT
			bool_or(pg_get_userbyid(class.relowner) = current_user) AS is_owner,
			(SELECT rolsuper FROM pg_roles WHERE rolname = current_user) AS is_superuser,
			(SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user) AS bypass_rls,
			COUNT(*) FILTER (
				WHERE class.relrowsecurity AND class.relforcerowsecurity
			) AS protected_rows
		FROM pg_class AS class
		JOIN pg_namespace AS namespace ON namespace.oid = class.relnamespace
		WHERE namespace.nspname = ?
		  AND class.relname IN ('domain_events', 'outbox_deliveries')
	`, fixture.schemaName).Scan(&state).Error; err != nil {
		t.Fatal(err)
	}
	if state.IsOwner || state.IsSuperuser || state.BypassRLS ||
		state.ProtectedRows != 2 {
		t.Fatalf("runtime/RLS boundary = %+v", state)
	}
}

func assertAtomicRegistrationPostgresIdentityIndexes(
	t *testing.T,
	fixture *atomicRegistrationPostgresFixture,
) {
	t.Helper()
	assertAtomicRegistrationPostgresIdentityIndexesInSchema(
		t,
		fixture.admin,
		fixture.schemaName,
	)
}

func assertAtomicRegistrationPostgresDeployedIdentityIndexes(
	t *testing.T,
	db *gorm.DB,
) {
	t.Helper()
	var deployed struct {
		SchemaName string `gorm:"column:schema_name"`
		HasUsers   bool   `gorm:"column:has_users"`
	}
	if err := db.Raw(`
		SELECT
			current_schema() AS schema_name,
			to_regclass(quote_ident(current_schema()) || '.users') IS NOT NULL
				AS has_users
	`).Scan(&deployed).Error; err != nil {
		t.Fatal(err)
	}
	if deployed.SchemaName == "" || !deployed.HasUsers {
		t.Fatalf(
			"deployed PostgreSQL schema %q has no users table",
			deployed.SchemaName,
		)
	}
	assertAtomicRegistrationPostgresIdentityIndexesInSchema(
		t,
		db,
		deployed.SchemaName,
	)
}

func assertAtomicRegistrationPostgresIdentityIndexesInSchema(
	t *testing.T,
	db *gorm.DB,
	schemaName string,
) {
	t.Helper()
	type indexRow struct {
		ColumnName string `gorm:"column:column_name"`
		IndexName  string `gorm:"column:index_name"`
	}
	var rows []indexRow
	if err := db.Raw(`
		SELECT
			attribute.attname AS column_name,
			index_class.relname AS index_name
		FROM pg_index AS index_state
		JOIN pg_class AS table_class ON table_class.oid = index_state.indrelid
		JOIN pg_namespace AS namespace ON namespace.oid = table_class.relnamespace
		JOIN pg_class AS index_class ON index_class.oid = index_state.indexrelid
		JOIN LATERAL unnest(index_state.indkey)
			WITH ORDINALITY AS key_column(attnum, ordinal)
			ON key_column.ordinal = 1
		JOIN pg_attribute AS attribute
			ON attribute.attrelid = table_class.oid
			AND attribute.attnum = key_column.attnum
		WHERE namespace.nspname = ?
		  AND table_class.relname = 'users'
		  AND attribute.attname IN ('email', 'username')
		  AND index_state.indisunique
		  AND index_state.indisvalid
		  AND index_state.indisready
		  AND index_state.indnkeyatts = 1
		  AND index_state.indnatts = 1
		  AND index_state.indpred IS NULL
		  AND index_state.indexprs IS NULL
		ORDER BY attribute.attname
	`, schemaName).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{
		"email":    "idx_users_email",
		"username": "idx_users_username",
	}
	if len(rows) != len(expected) {
		t.Fatalf("identity unique index rows = %+v", rows)
	}
	for _, row := range rows {
		if expected[row.ColumnName] != row.IndexName {
			t.Fatalf("identity unique index row = %+v", row)
		}
		delete(expected, row.ColumnName)
	}
	if len(expected) != 0 {
		t.Fatalf("missing identity unique indexes: %+v", expected)
	}
}

func assertAtomicRegistrationPostgresCounts(
	t *testing.T,
	fixture *atomicRegistrationPostgresFixture,
	want int64,
	verificationEnabled bool,
) {
	t.Helper()
	wantSession := want
	wantVerification := int64(0)
	if verificationEnabled {
		wantSession = 0
		wantVerification = want
	}
	for table, tableWant := range map[string]int64{
		"users":               want,
		"user_profiles":       want,
		"email_verifications": wantVerification,
		"refresh_tokens":      wantSession,
		"login_histories":     wantSession,
		"login_attempts":      wantSession,
		"domain_events":       want,
		"outbox_deliveries":   want,
	} {
		var count int64
		query := "SELECT COUNT(*) FROM " + fixture.quotedSchema + "." +
			quoteAtomicRegistrationPostgresIdentifier(table)
		if err := fixture.admin.Raw(query).Scan(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != tableWant {
			t.Errorf("%s count = %d, want %d", table, count, tableWant)
		}
	}
}

func newAtomicRegistrationPostgresBarrier(
	table string,
) *atomicRegistrationPostgresBarrier {
	return &atomicRegistrationPostgresBarrier{
		table:   table,
		reached: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (barrier *atomicRegistrationPostgresBarrier) AfterCreate(db *gorm.DB) {
	if db.Statement == nil || db.Statement.Table != barrier.table {
		return
	}
	barrier.reachedOnce.Do(func() {
		sqlTx, ok := db.Statement.ConnPool.(*sql.Tx)
		if !ok {
			_ = db.AddError(errors.New(
				"atomic registration barrier requires PostgreSQL transaction",
			))
		} else if err := sqlTx.QueryRowContext(
			db.Statement.Context,
			"SELECT pg_backend_pid()",
		).Scan(&barrier.pid); err != nil {
			_ = db.AddError(err)
		}
		close(barrier.reached)
	})
	select {
	case <-barrier.release:
	case <-db.Statement.Context.Done():
		_ = db.AddError(db.Statement.Context.Err())
	}
}

func (barrier *atomicRegistrationPostgresBarrier) Release() {
	barrier.releaseOnce.Do(func() {
		close(barrier.release)
	})
}

func waitAtomicRegistrationPostgresBarrier(
	t *testing.T,
	barrier *atomicRegistrationPostgresBarrier,
	done <-chan error,
) {
	t.Helper()
	select {
	case <-barrier.reached:
		if barrier.pid == 0 {
			t.Fatal("registration barrier did not capture backend PID")
		}
	case err := <-done:
		t.Fatalf("registration completed before barrier: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for registration transaction barrier")
	}
}

func waitForAtomicRegistrationPostgresPolicyWriter(
	t *testing.T,
	fixture *atomicRegistrationPostgresFixture,
	registrationPID int,
) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var state struct {
			RegistrationShare int64 `gorm:"column:registration_share"`
			WaitingWriter     int64 `gorm:"column:waiting_writer"`
		}
		emailConfigRegclass := fixture.quotedSchema + ".email_configs"
		if err := fixture.admin.Raw(`
			SELECT
				(
					SELECT COUNT(*)
					FROM pg_locks
					WHERE pid = ?
					  AND locktype = 'relation'
					  AND mode = 'ShareLock'
					  AND granted
					  AND relation = ?::regclass
				) AS registration_share,
				(
					SELECT COUNT(*)
					FROM pg_stat_activity AS activity
					JOIN pg_locks AS lock_state ON lock_state.pid = activity.pid
					WHERE activity.application_name = ?
					  AND activity.wait_event_type = 'Lock'
					  AND ? = ANY(pg_blocking_pids(activity.pid))
					  AND lock_state.relation = ?::regclass
					  AND lock_state.mode = 'RowExclusiveLock'
					  AND NOT lock_state.granted
				) AS waiting_writer
		`,
			registrationPID,
			emailConfigRegclass,
			fixture.writerApp,
			registrationPID,
			emailConfigRegclass,
		).Scan(&state).Error; err != nil {
			t.Fatal(err)
		}
		if state.RegistrationShare == 1 && state.WaitingWriter == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("policy writer was not observed behind registration ShareLock")
}

func waitAtomicRegistrationPostgresResult(
	t *testing.T,
	result <-chan error,
	operation string,
) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(atomicRegistrationPostgresStatementTimeout):
		t.Fatalf("timed out waiting for %s", operation)
		return nil
	}
}

func boolPointer(value bool) *bool {
	return &value
}

func quoteAtomicRegistrationPostgresIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quoteAtomicRegistrationPostgresLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}
