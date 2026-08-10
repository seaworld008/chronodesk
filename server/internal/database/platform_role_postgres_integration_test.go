package database

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/gorm"
)

func TestPostgresPlatformRoleLegacyTableLockBlocksConcurrentWriters(
	t *testing.T,
) {
	db, _, suffix := openPostgresMembershipReleaseTestDB(
		t,
		"platform_role_lock",
	)
	if err := db.Exec(`
		CREATE TABLE users (
			id BIGSERIAL PRIMARY KEY,
			username VARCHAR(50) NOT NULL,
			email VARCHAR(100) NOT NULL,
			password_hash VARCHAR(255) NOT NULL,
			role VARCHAR(20),
			status VARCHAR(20) NOT NULL
		);
		CREATE TABLE admin_audit_logs (
			id BIGSERIAL PRIMARY KEY,
			role VARCHAR(50)
		)
	`).Error; err != nil {
		t.Fatalf("create legacy role tables: %v", err)
	}

	locked := make(chan struct{})
	release := make(chan struct{})
	lockResult := make(chan error, 1)
	go func() {
		lockResult <- db.Transaction(func(tx *gorm.DB) error {
			if err := lockLegacyPlatformRoleTables(tx); err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	select {
	case <-locked:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for legacy role table lock")
	}

	blockedWrites := []struct {
		name string
		sql  string
		args []any
	}{
		{
			name: "users",
			sql: `
				INSERT INTO users (username, email, password_hash, role, status)
				VALUES (?, ?, 'hash', 'admin', 'active')
			`,
			args: []any{
				"blocked-" + suffix,
				"blocked-" + suffix + "@example.test",
			},
		},
		{
			name: "admin_audit_logs",
			sql: `
				INSERT INTO admin_audit_logs (role)
				VALUES ('admin')
			`,
		},
	}
	for _, write := range blockedWrites {
		blockedContext, cancel := context.WithTimeout(
			context.Background(),
			150*time.Millisecond,
		)
		err := db.WithContext(blockedContext).
			Exec(write.sql, write.args...).Error
		cancel()
		if err == nil ||
			(!errors.Is(err, context.DeadlineExceeded) &&
				!strings.Contains(strings.ToLower(err.Error()), "cancel")) {
			t.Fatalf(
				"concurrent legacy %s writer was not blocked by cutover lock: %v",
				write.name,
				err,
			)
		}
	}

	close(release)
	if err := <-lockResult; err != nil {
		t.Fatalf("release legacy role table lock: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO users (username, email, password_hash, role, status)
		VALUES (?, ?, 'hash', 'admin', 'active')
	`, "released-"+suffix, "released-"+suffix+"@example.test").Error; err != nil {
		t.Fatalf("writer did not resume after cutover lock release: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO admin_audit_logs (role)
		VALUES ('admin')
	`).Error; err != nil {
		t.Fatalf("audit writer did not resume after cutover lock release: %v", err)
	}
}

func TestPostgresPlatformRoleRuntimeContractRejectsCatalogDrift(t *testing.T) {
	db, _, _ := openPostgresMembershipReleaseTestDB(
		t,
		"pr_contract",
	)
	if err := RunMigrations(
		db,
		services.EnsureProjectScopeMigrationMembership,
	); err != nil {
		t.Fatalf("migrate fresh PostgreSQL platform role schema: %v", err)
	}
	if err := ValidatePlatformRoleCutover(db); err != nil {
		t.Fatalf("validate fresh platform role schema: %v", err)
	}

	if err := db.Exec(
		"DROP INDEX idx_users_platform_role",
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlatformRoleCutover(db); err == nil ||
		!strings.Contains(err.Error(), "index is missing") {
		t.Fatalf("missing platform role index validation error = %v", err)
	}
	if err := db.Exec(
		"CREATE INDEX idx_users_platform_role ON users(platform_role)",
	).Error; err != nil {
		t.Fatal(err)
	}

	if err := db.Exec(`
		ALTER TABLE users
		ALTER COLUMN platform_role SET DEFAULT 'admin'
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlatformRoleCutover(db); err == nil ||
		!strings.Contains(err.Error(), "member default") {
		t.Fatalf("wrong platform role default validation error = %v", err)
	}
	if err := db.Exec(`
		ALTER TABLE users
		ALTER COLUMN platform_role SET DEFAULT 'member'
	`).Error; err != nil {
		t.Fatal(err)
	}

	if err := db.Exec(`
		ALTER TABLE users DROP CONSTRAINT chk_users_platform_role;
		ALTER TABLE users ADD CONSTRAINT chk_users_platform_role
			CHECK (
				platform_role IN (
					'platform_admin',
					'security_auditor',
					'emergency_operator',
					'member'
				) OR TRUE
			)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlatformRoleCutover(db); err == nil ||
		!strings.Contains(err.Error(), "unexpected semantics") {
		t.Fatalf("boolean bypass platform role check error = %v", err)
	}

	if err := db.Exec(`
		ALTER TABLE users DROP CONSTRAINT chk_users_platform_role;
		ALTER TABLE users ADD CONSTRAINT chk_users_platform_role
			CHECK (platform_role IN (
				'platform_admin',
				'security_auditor',
				'emergency_operator',
				'member',
				'owner'
			))
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlatformRoleCutover(db); err == nil ||
		!strings.Contains(err.Error(), "unexpected semantics") {
		t.Fatalf("expanded platform role check validation error = %v", err)
	}

	if err := db.Exec(`
		ALTER TABLE users DROP CONSTRAINT chk_users_platform_role;
		ALTER TABLE users ADD CONSTRAINT chk_users_platform_role
			CHECK (platform_role IN (
				'platform_admin',
				'security_auditor',
				'emergency_operator',
				'member'
			));
		ALTER TABLE users ALTER COLUMN platform_role DROP NOT NULL
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlatformRoleCutover(db); err == nil ||
		!strings.Contains(err.Error(), "NOT NULL") {
		t.Fatalf("nullable platform role validation error = %v", err)
	}

	var checkpoint models.SchemaMigrationCheckpoint
	if err := db.Where(
		"key = ?",
		platformRoleCutoverCheckpointKey,
	).First(&checkpoint).Error; err != nil {
		t.Fatalf("catalog drift removed platform checkpoint: %v", err)
	}
}

func TestPostgresPlatformRoleAuditContractRejectsCatalogDrift(t *testing.T) {
	db, _, _ := openPostgresMembershipReleaseTestDB(
		t,
		"pr_audit_contract",
	)
	if err := RunMigrations(
		db,
		services.EnsureProjectScopeMigrationMembership,
	); err != nil {
		t.Fatalf("migrate fresh PostgreSQL platform role schema: %v", err)
	}
	if err := db.Exec(
		"DROP INDEX idx_admin_audit_logs_platform_role",
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlatformRoleCutover(db); err == nil ||
		!strings.Contains(err.Error(), "admin_audit_logs.platform_role index is missing") {
		t.Fatalf("missing audit platform role index validation error = %v", err)
	}
	if err := db.Exec(`
		CREATE INDEX idx_admin_audit_logs_platform_role
		ON admin_audit_logs(platform_role)
	`).Error; err != nil {
		t.Fatal(err)
	}

	if err := db.Exec(`
		ALTER TABLE admin_audit_logs
		ALTER COLUMN platform_role SET DEFAULT 'admin'
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlatformRoleCutover(db); err == nil ||
		!strings.Contains(err.Error(), "nullable without a default") {
		t.Fatalf("wrong audit platform role default validation error = %v", err)
	}
	if err := db.Exec(`
		ALTER TABLE admin_audit_logs
		ALTER COLUMN platform_role DROP DEFAULT
	`).Error; err != nil {
		t.Fatal(err)
	}

	if err := db.Exec(`
		ALTER TABLE admin_audit_logs
			DROP CONSTRAINT chk_admin_audit_logs_platform_role;
		ALTER TABLE admin_audit_logs
			ADD CONSTRAINT chk_admin_audit_logs_platform_role
			CHECK (
				platform_role IN (
					'platform_admin',
					'security_auditor',
					'emergency_operator',
					'member'
				) OR TRUE
			)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlatformRoleCutover(db); err == nil ||
		!strings.Contains(err.Error(), "unexpected semantics") {
		t.Fatalf("boolean bypass audit platform role check error = %v", err)
	}

	if err := db.Exec(`
		ALTER TABLE admin_audit_logs
			DROP CONSTRAINT chk_admin_audit_logs_platform_role;
		ALTER TABLE admin_audit_logs
			ADD CONSTRAINT chk_admin_audit_logs_platform_role
			CHECK (platform_role IS NULL OR platform_role IN (
				'platform_admin',
				'security_auditor',
				'emergency_operator',
				'member'
			));
		ALTER TABLE admin_audit_logs
			ALTER COLUMN platform_role SET NOT NULL
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlatformRoleCutover(db); err == nil ||
		!strings.Contains(err.Error(), "nullable without a default") {
		t.Fatalf("nullable audit platform role validation error = %v", err)
	}
}

func TestPostgresPlatformRoleCutoverRejectsUnprovenRetainedState(
	t *testing.T,
) {
	tests := []struct {
		name      string
		fixture   string
		wantState string
		insert    func(*testing.T, *gorm.DB)
	}{
		{
			name:      "user",
			fixture:   "pru_user",
			wantState: "users=1",
			insert: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				if err := db.Create(&models.User{
					Username:     "unproven-user",
					Email:        "unproven-user@example.test",
					PasswordHash: "hash",
					PlatformRole: models.PlatformRoleMember,
					Status:       models.UserStatusActive,
				}).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:      "audit",
			fixture:   "pru_audit",
			wantState: "admin_audit_logs=1",
			insert: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				role := models.PlatformRoleMember
				if err := db.Create(&models.AdminAuditLog{
					Username:     "unproven-audit",
					ActorType:    models.ActorTypeHuman,
					ActorID:      "human:unproven-audit",
					PlatformRole: &role,
					Action:       "legacy_action",
				}).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:      "membership",
			fixture:   "pru_member",
			wantState: "project_memberships=1",
			insert: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				user := models.User{
					Username:     "unproven-member",
					Email:        "unproven-member@example.test",
					PasswordHash: "hash",
					PlatformRole: models.PlatformRoleMember,
					Status:       models.UserStatusActive,
				}
				if err := db.Create(&user).Error; err != nil {
					t.Fatal(err)
				}
				var project models.Project
				if err := db.Where("key = ?", DefaultProjectKey).
					Take(&project).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&models.ProjectMembership{
					ProjectID: project.ID,
					UserID:    user.ID,
					Role:      models.ProjectRoleRequester,
					IsActive:  true,
					Version:   1,
				}).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:      "asymmetric_audit",
			fixture:   "pru_asym",
			wantState: "admin_audit_logs=1",
			insert: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				if err := db.Exec(`
					ALTER TABLE users
					ADD COLUMN role VARCHAR(20) NOT NULL DEFAULT 'customer'
				`).Error; err != nil {
					t.Fatal(err)
				}
				role := models.PlatformRoleMember
				if err := db.Create(&models.AdminAuditLog{
					Username:     "asymmetric-audit",
					ActorType:    models.ActorTypeHuman,
					ActorID:      "human:asymmetric-audit",
					PlatformRole: &role,
					Action:       "legacy_admin_action",
				}).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, _, _ := openPostgresMembershipReleaseTestDB(
				t,
				test.fixture,
			)
			if err := RunMigrations(
				db,
				services.EnsureProjectScopeMigrationMembership,
			); err != nil {
				t.Fatalf("migrate fresh PostgreSQL platform role schema: %v", err)
			}
			if err := db.Where(
				"key = ?",
				platformRoleCutoverCheckpointKey,
			).Delete(&models.SchemaMigrationCheckpoint{}).Error; err != nil {
				t.Fatal(err)
			}
			test.insert(t, db)

			err := MigratePlatformRoles(
				db,
				services.EnsureProjectScopeMigrationMembership,
			)
			if err == nil ||
				!strings.Contains(err.Error(), "cannot prove platform role cutover") {
				t.Fatalf(
					"MigratePlatformRoles() error = %v, want unproven cutover rejection",
					err,
				)
			}
			if !strings.Contains(err.Error(), test.wantState) {
				t.Fatalf(
					"MigratePlatformRoles() error = %v, want retained state %q",
					err,
					test.wantState,
				)
			}
			var checkpoints int64
			if countErr := db.Model(&models.SchemaMigrationCheckpoint{}).
				Where("key = ?", platformRoleCutoverCheckpointKey).
				Count(&checkpoints).Error; countErr != nil {
				t.Fatal(countErr)
			}
			if checkpoints != 0 {
				t.Fatalf(
					"unproven cutover created %d platform checkpoint(s)",
					checkpoints,
				)
			}
		})
	}
}

func TestPostgresPlatformRoleFinalCheckpointFailureRollsBackEveryArtifact(
	t *testing.T,
) {
	db, _, suffix := openPostgresMembershipReleaseTestDB(
		t,
		"pr_rollback",
	)
	if err := db.Exec(`
		CREATE TABLE users (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMPTZ,
			updated_at TIMESTAMPTZ,
			deleted_at TIMESTAMPTZ,
			username VARCHAR(50) NOT NULL,
			email VARCHAR(100) NOT NULL,
			password_hash VARCHAR(255) NOT NULL,
			role VARCHAR(20) NOT NULL DEFAULT 'customer',
			status VARCHAR(20) NOT NULL DEFAULT 'inactive'
		);
		CREATE TABLE admin_audit_logs (
			id BIGSERIAL PRIMARY KEY,
			user_id BIGINT,
			role VARCHAR(50)
		)
	`).Error; err != nil {
		t.Fatalf("create legacy platform role tables: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO users (
			username, email, password_hash, role, status
		) VALUES (?, ?, 'hash', 'admin', 'active')
	`,
		"rollback-admin-"+suffix,
		"rollback-admin-"+suffix+"@example.test",
	).Error; err != nil {
		t.Fatalf("insert legacy platform role user: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO admin_audit_logs (role) VALUES ('admin')
	`).Error; err != nil {
		t.Fatalf("insert legacy platform audit role: %v", err)
	}

	var observed struct {
		membership bool
		event      bool
		outbox     bool
		audit      bool
	}
	realWriter := func(
		ctx context.Context,
		tx *gorm.DB,
		user models.User,
		scope models.ProjectScope,
		role models.ProjectRole,
	) error {
		if err := services.EnsureProjectScopeMigrationMembership(
			ctx,
			tx,
			user,
			scope,
			role,
		); err != nil {
			return err
		}
		for model, destination := range map[any]*bool{
			&models.ProjectMembership{}: &observed.membership,
			&models.DomainEvent{}:       &observed.event,
			&models.OutboxDelivery{}:    &observed.outbox,
			&models.AuditLedgerEntry{}:  &observed.audit,
		} {
			var count int64
			if err := tx.Model(model).Count(&count).Error; err != nil {
				return err
			}
			*destination = count > 0
		}
		return nil
	}

	injected := errors.New("injected PostgreSQL final platform checkpoint failure")
	const callbackName = "test:fail-postgres-final-platform-role-checkpoint"
	if err := db.Callback().Create().
		Before("gorm:create").
		Register(callbackName, func(tx *gorm.DB) {
			checkpoint, ok := tx.Statement.Dest.(*models.SchemaMigrationCheckpoint)
			if ok && checkpoint != nil &&
				checkpoint.Key == platformRoleCutoverCheckpointKey {
				_ = tx.AddError(injected)
			}
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Create().Remove(callbackName)
	})

	err := RunMigrations(db, realWriter)
	if !errors.Is(err, injected) {
		t.Fatalf("RunMigrations() error = %v, want injected failure", err)
	}
	if !observed.membership ||
		!observed.event ||
		!observed.outbox ||
		!observed.audit {
		t.Fatalf(
			"real migration writer did not reach every audited artifact: %+v",
			observed,
		)
	}

	for _, column := range []struct {
		table string
		name  string
		want  bool
	}{
		{table: "users", name: "role", want: true},
		{table: "users", name: "platform_role", want: false},
		{table: "admin_audit_logs", name: "role", want: true},
		{table: "admin_audit_logs", name: "platform_role", want: false},
	} {
		if got := databaseColumnExists(t, db, column.table, column.name); got != column.want {
			t.Fatalf(
				"%s.%s exists = %t, want %t after rollback",
				column.table,
				column.name,
				got,
				column.want,
			)
		}
	}
	var userRole string
	if err := db.Table("users").Select("role").Scan(&userRole).Error; err != nil {
		t.Fatal(err)
	}
	if userRole != "admin" {
		t.Fatalf("failed migration changed legacy user role to %q", userRole)
	}
	var auditRole string
	if err := db.Table("admin_audit_logs").
		Select("role").
		Scan(&auditRole).Error; err != nil {
		t.Fatal(err)
	}
	if auditRole != "admin" {
		t.Fatalf("failed migration changed legacy audit role to %q", auditRole)
	}
	if !db.Migrator().HasTable(&models.SchemaMigrationCheckpoint{}) {
		t.Fatal(
			"migration orchestration did not retain its pre-main checkpoint table",
		)
	}
	var checkpointCount int64
	if err := db.Model(&models.SchemaMigrationCheckpoint{}).
		Count(&checkpointCount).Error; err != nil {
		t.Fatal(err)
	}
	if checkpointCount != 0 {
		t.Fatalf(
			"failed main migration retained %d cutover checkpoints",
			checkpointCount,
		)
	}
	for _, model := range []any{
		&models.ProjectMembership{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.AuditLedgerEntry{},
	} {
		if db.Migrator().HasTable(model) {
			t.Fatalf(
				"failed migration retained transactional table for %T",
				model,
			)
		}
	}
}
