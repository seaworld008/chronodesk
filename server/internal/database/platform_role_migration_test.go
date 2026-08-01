package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMigrateLegacyHumanRolesRejectsUnknownBeforeNormalizingAliases(
	t *testing.T,
) {
	db := openLegacyPlatformRoleTestDB(t, "unknown-pre-auto")
	insertLegacyPlatformRoleUser(t, db, "legacy-user", "user", "active", false)
	insertLegacyPlatformRoleUser(t, db, "unknown-user", "owner", "active", false)

	err := migrateLegacyHumanRoles(db)
	if err == nil || !strings.Contains(err.Error(), "unsupported legacy human role") {
		t.Fatalf("migrateLegacyHumanRoles() error = %v, want unknown-role rejection", err)
	}

	var roles []string
	if err := db.Table("users").Order("id ASC").Pluck("role", &roles).Error; err != nil {
		t.Fatal(err)
	}
	if len(roles) != 2 || roles[0] != "user" || roles[1] != "owner" {
		t.Fatalf("preflight rejection modified legacy roles: %v", roles)
	}
}

func TestExactPlatformRoleCheckDefinitionRejectsBooleanBypasses(t *testing.T) {
	valid := []string{
		`CHECK (platform_role IN ('platform_admin', 'security_auditor', 'emergency_operator', 'member'))`,
		`CHECK (((platform_role)::text = ANY ((ARRAY['member'::character varying, 'platform_admin'::character varying, 'emergency_operator'::character varying, 'security_auditor'::character varying])::text[])))`,
	}
	for _, definition := range valid {
		if !isExactPlatformRoleCheckDefinition(definition) {
			t.Errorf("valid definition was rejected: %s", definition)
		}
	}
	invalid := []string{
		`CHECK (platform_role IN ('platform_admin', 'security_auditor', 'emergency_operator', 'member', 'owner'))`,
		`CHECK (platform_role IN ('platform_admin', 'security_auditor', 'emergency_operator', 'member') OR TRUE)`,
		`CHECK (platform_role NOT IN ('platform_admin', 'security_auditor', 'emergency_operator', 'member'))`,
		`CHECK (other_column IN ('platform_admin', 'security_auditor', 'emergency_operator', 'member'))`,
		`CHECK (platform_role IN ('platform_admin', 'platform_admin', 'emergency_operator', 'member'))`,
	}
	for _, definition := range invalid {
		if isExactPlatformRoleCheckDefinition(definition) {
			t.Errorf("invalid definition was accepted: %s", definition)
		}
	}
}

func TestRunMigrationsPreflightRejectsUnknownAuditRoleBeforeAnyWrite(
	t *testing.T,
) {
	db := openLegacyPlatformRoleTestDB(t, "unknown-audit-preflight")
	insertLegacyPlatformRoleUser(t, db, "legacy-user", "user", "active", false)
	if err := db.Exec(`
		INSERT INTO admin_audit_logs (role, platform_role)
		VALUES ('owner', 'member')
	`).Error; err != nil {
		t.Fatal(err)
	}

	err := RunMigrations(db, testPlatformRoleMembershipWriter)
	if err == nil || !strings.Contains(err.Error(), "unsupported legacy admin audit role") {
		t.Fatalf("RunMigrations() error = %v, want audit role preflight rejection", err)
	}
	var role string
	if err := db.Table("users").Select("role").Where("id = 1").Scan(&role).Error; err != nil {
		t.Fatal(err)
	}
	if role != "user" {
		t.Fatalf("preflight modified legacy user role to %q", role)
	}
	assertPlatformPreflightMadeNoDurableChanges(t, db)
}

func TestRunMigrationsPreflightRejectsNullUserRoleBeforeAnyWrite(t *testing.T) {
	db := openPlatformRoleTestDB(t, "null-user-preflight")
	if err := db.Exec(`
		CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT,
			status TEXT NOT NULL DEFAULT 'active'
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		INSERT INTO users (username, email, password_hash, role, status)
		VALUES
			('null-role', 'null-role@example.test', 'hash', NULL, 'active'),
			('legacy-user', 'legacy-user@example.test', 'hash', 'user', 'active')
	`).Error; err != nil {
		t.Fatal(err)
	}

	err := RunMigrations(db, testPlatformRoleMembershipWriter)
	if err == nil || !strings.Contains(err.Error(), "<NULL>") {
		t.Fatalf("RunMigrations() error = %v, want NULL role preflight rejection", err)
	}
	var roles []sql.NullString
	if err := db.Table("users").Order("id ASC").Pluck("role", &roles).Error; err != nil {
		t.Fatal(err)
	}
	if len(roles) != 2 ||
		roles[0].Valid ||
		!roles[1].Valid ||
		roles[1].String != "user" {
		t.Fatalf("preflight modified legacy roles: %#v", roles)
	}
	assertPlatformPreflightMadeNoDurableChanges(t, db)
}

func TestRunMigrationsRollsBackRealAuditedMembershipAndPlatformCutover(
	t *testing.T,
) {
	db := openPlatformRoleTestDB(t, "full-audited-rollback")
	tableOnly := db.Session(&gorm.Session{NewDB: true})
	tableOnly.Config.IgnoreRelationshipsWhenMigrating = true
	if err := tableOnly.AutoMigrate(
		&models.User{},
		&models.AdminAuditLog{},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		ALTER TABLE users
		ADD COLUMN role TEXT NOT NULL DEFAULT 'customer'
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		ALTER TABLE admin_audit_logs
		ADD COLUMN role TEXT
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		INSERT INTO users (
			username, email, password_hash, platform_role, role, status
		) VALUES (
			'rollback-user',
			'rollback-user@example.test',
			'hash',
			'member',
			'user',
			'active'
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		INSERT INTO admin_audit_logs (
			role, platform_role, actor_type, actor_id
		)
		VALUES ('user', 'member', 'human', 'rollback-user')
	`).Error; err != nil {
		t.Fatal(err)
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

	injected := errors.New("injected final platform checkpoint failure")
	const callbackName = "test:fail-final-platform-role-checkpoint"
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

	var legacyRole string
	if err := db.Table("users").
		Select("role").
		Where("username = ?", "rollback-user").
		Scan(&legacyRole).Error; err != nil {
		t.Fatal(err)
	}
	if legacyRole != "user" {
		t.Fatalf("failed migration changed legacy user role to %q", legacyRole)
	}
	if !databaseColumnExists(t, db, "users", "platform_role") {
		t.Fatal("failed migration removed pre-existing users.platform_role")
	}
	if !databaseColumnExists(t, db, "users", "role") {
		t.Fatal("failed migration removed users.role")
	}
	var platformRole string
	if err := db.Table("users").
		Select("platform_role").
		Where("username = ?", "rollback-user").
		Scan(&platformRole).Error; err != nil {
		t.Fatal(err)
	}
	if platformRole != string(models.PlatformRoleMember) {
		t.Fatalf(
			"failed migration changed platform role to %q",
			platformRole,
		)
	}
	if !databaseColumnExists(t, db, "admin_audit_logs", "role") {
		t.Fatal("failed migration removed admin_audit_logs.role")
	}
	var auditRole string
	if err := db.Table("admin_audit_logs").
		Select("role").
		Take(&auditRole).Error; err != nil {
		t.Fatal(err)
	}
	if auditRole != "user" {
		t.Fatalf("failed migration changed legacy audit role to %q", auditRole)
	}
	assertMigrationArtifactsAbsentOrEmpty(t, db)
}

func assertMigrationArtifactsAbsentOrEmpty(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, table := range []string{
		"project_memberships",
		"domain_events",
		"outbox_deliveries",
		"audit_ledger_entries",
		"schema_migration_checkpoints",
	} {
		if !db.Migrator().HasTable(table) {
			continue
		}
		var count int64
		if err := db.Table(table).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("failed migration retained %d row(s) in %s", count, table)
		}
	}
}

func assertPlatformPreflightMadeNoDurableChanges(t *testing.T, db *gorm.DB) {
	t.Helper()
	if databaseColumnExists(t, db, "users", "platform_role") {
		t.Fatal("failed preflight added users.platform_role")
	}
	for _, table := range []string{
		"project_memberships",
		"schema_migration_checkpoints",
	} {
		if db.Migrator().HasTable(table) {
			var count int64
			if err := db.Table(table).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("failed preflight wrote %d row(s) to %s", count, table)
			}
		}
	}
}

func TestMigratePlatformRolesMapsUsersAndPreservesExplicitMemberships(
	t *testing.T,
) {
	db := openLegacyPlatformRoleTestDB(t, "maps-and-preserves")
	insertLegacyPlatformRoleUser(t, db, "admin", "admin", "active", false)
	insertLegacyPlatformRoleUser(t, db, "supervisor", "supervisor", "active", false)
	insertLegacyPlatformRoleUser(t, db, "agent", "agent", "active", false)
	insertLegacyPlatformRoleUser(t, db, "customer", "customer", "active", false)
	insertLegacyPlatformRoleUser(t, db, "inactive", "supervisor", "inactive", false)
	insertLegacyPlatformRoleUser(t, db, "deleted", "admin", "deleted", true)
	if err := db.Exec(`
		INSERT INTO admin_audit_logs (role, platform_role)
		VALUES ('admin', 'member'), ('supervisor', 'member')
	`).Error; err != nil {
		t.Fatalf("insert legacy audit roles: %v", err)
	}
	preparePlatformRoleCutoverFixture(t, db)

	_, _, project, _ := loadDefaultProjectHierarchy(t, db)
	explicit := models.ProjectMembership{
		ProjectID: project.ID,
		UserID:    1,
		Role:      models.ProjectRoleObserver,
		IsActive:  true,
		Version:   7,
	}
	if err := db.Create(&explicit).Error; err != nil {
		t.Fatalf("create explicit membership: %v", err)
	}
	if err := db.Model(&explicit).UpdateColumn("is_active", false).Error; err != nil {
		t.Fatalf("deactivate explicit membership: %v", err)
	}

	if err := MigratePlatformRoles(
		db,
		testPlatformRoleMembershipWriter,
	); err != nil {
		t.Fatalf("MigratePlatformRoles(): %v", err)
	}

	var users []models.User
	if err := db.Unscoped().Order("id ASC").Find(&users).Error; err != nil {
		t.Fatalf("load migrated users: %v", err)
	}
	wantPlatformRoles := []models.PlatformRole{
		models.PlatformRolePlatformAdmin,
		models.PlatformRoleMember,
		models.PlatformRoleMember,
		models.PlatformRoleMember,
		models.PlatformRoleMember,
		models.PlatformRolePlatformAdmin,
	}
	if len(users) != len(wantPlatformRoles) {
		t.Fatalf("migrated users = %d, want %d", len(users), len(wantPlatformRoles))
	}
	for index, want := range wantPlatformRoles {
		if users[index].PlatformRole != want {
			t.Errorf(
				"user %d platform role = %q, want %q",
				users[index].ID,
				users[index].PlatformRole,
				want,
			)
		}
	}

	var memberships []models.ProjectMembership
	if err := db.Where("project_id = ?", project.ID).
		Order("user_id ASC").
		Find(&memberships).Error; err != nil {
		t.Fatalf("load memberships: %v", err)
	}
	if len(memberships) != 4 {
		t.Fatalf("membership count = %d, want 4: %+v", len(memberships), memberships)
	}
	wantProjectRoles := map[uint]models.ProjectRole{
		1: models.ProjectRoleObserver,
		2: models.ProjectRoleManager,
		3: models.ProjectRoleAgent,
		4: models.ProjectRoleRequester,
	}
	for _, membership := range memberships {
		if membership.Role != wantProjectRoles[membership.UserID] {
			t.Errorf("unexpected membership: %+v", membership)
		}
	}
	if memberships[0].IsActive || memberships[0].Version != 7 {
		t.Fatalf("explicit membership was changed: %+v", memberships[0])
	}
	if databaseColumnExists(t, db, "users", "role") {
		t.Fatal("platform cutover retained legacy users.role")
	}
	if databaseColumnExists(t, db, "admin_audit_logs", "role") {
		t.Fatal("platform cutover retained legacy admin_audit_logs.role")
	}
	var auditRoles []models.PlatformRole
	if err := db.Table("admin_audit_logs").
		Order("id ASC").
		Pluck("platform_role", &auditRoles).Error; err != nil {
		t.Fatal(err)
	}
	if len(auditRoles) != 2 ||
		auditRoles[0] != models.PlatformRolePlatformAdmin ||
		auditRoles[1] != models.PlatformRoleMember {
		t.Fatalf("migrated audit platform roles = %v", auditRoles)
	}
	assertPlatformRoleCheckpoint(t, db)
}

func TestMigratePlatformRolesRollsBackUserMembershipAndCheckpointOnWriterFailure(
	t *testing.T,
) {
	db := openLegacyPlatformRoleTestDB(t, "writer-rollback")
	insertLegacyPlatformRoleUser(t, db, "admin", "admin", "active", false)
	insertLegacyPlatformRoleUser(t, db, "agent", "agent", "active", false)
	preparePlatformRoleCutoverFixture(t, db)

	injected := errors.New("injected membership writer failure")
	calls := 0
	writer := func(
		ctx context.Context,
		tx *gorm.DB,
		user models.User,
		scope models.ProjectScope,
		role models.ProjectRole,
	) error {
		calls++
		if calls == 2 {
			return injected
		}
		return testPlatformRoleMembershipWriter(ctx, tx, user, scope, role)
	}
	err := MigratePlatformRoles(db, writer)
	if !errors.Is(err, injected) {
		t.Fatalf("MigratePlatformRoles() error = %v, want injected failure", err)
	}

	var platformRoles []string
	if err := db.Table("users").Order("id ASC").
		Pluck("platform_role", &platformRoles).Error; err != nil {
		t.Fatal(err)
	}
	if len(platformRoles) != 2 ||
		platformRoles[0] != "member" ||
		platformRoles[1] != "member" {
		t.Fatalf("failed cutover retained user updates: %v", platformRoles)
	}
	var memberships int64
	if err := db.Model(&models.ProjectMembership{}).Count(&memberships).Error; err != nil {
		t.Fatal(err)
	}
	if memberships != 0 {
		t.Fatalf("failed cutover retained %d membership(s)", memberships)
	}
	var checkpoints int64
	if err := db.Model(&models.SchemaMigrationCheckpoint{}).
		Where("key = ?", platformRoleCutoverCheckpointKey).
		Count(&checkpoints).Error; err != nil {
		t.Fatal(err)
	}
	if checkpoints != 0 {
		t.Fatalf("failed cutover retained %d platform checkpoint(s)", checkpoints)
	}
	if !databaseColumnExists(t, db, "users", "role") {
		t.Fatal("failed cutover removed legacy role column")
	}
}

func TestMigratePlatformRolesFreshCheckpointAndSchemaDrift(t *testing.T) {
	db := openPlatformRoleTestDB(t, "fresh")
	prepareFreshPlatformRoleCutoverFixture(t, db)

	if err := MigratePlatformRoles(db); err != nil {
		t.Fatalf("fresh MigratePlatformRoles(): %v", err)
	}
	assertPlatformRoleCheckpoint(t, db)
	if err := ValidatePlatformRoleCutover(db); err != nil {
		t.Fatalf("ValidatePlatformRoleCutover(): %v", err)
	}

	if err := db.Exec("ALTER TABLE users ADD COLUMN role TEXT").Error; err != nil {
		t.Fatalf("inject old role schema drift: %v", err)
	}
	err := MigratePlatformRoles(db)
	if err == nil || !strings.Contains(err.Error(), "legacy users.role") {
		t.Fatalf("schema drift error = %v, want legacy-role rejection", err)
	}
}

func TestMigratePlatformRolesRejectsUnprovenCutoverWithoutLegacyRole(
	t *testing.T,
) {
	tests := []struct {
		name      string
		wantState string
		insert    func(*testing.T, *gorm.DB)
	}{
		{
			name:      "retained user",
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
			name:      "retained audit record",
			wantState: "admin_audit_logs=1",
			insert: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				tableOnly := db.Session(&gorm.Session{NewDB: true})
				tableOnly.Config.IgnoreRelationshipsWhenMigrating = true
				if err := tableOnly.AutoMigrate(&models.AdminAuditLog{}); err != nil {
					t.Fatal(err)
				}
				role := models.PlatformRoleMember
				if err := db.Create(&models.AdminAuditLog{
					Username:     "retained-audit",
					PlatformRole: &role,
					Action:       "legacy_action",
				}).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:      "retained project membership",
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
			name:      "retained audit after asymmetric audit cutover",
			wantState: "admin_audit_logs=1",
			insert: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				if err := db.Exec(`
					ALTER TABLE users
					ADD COLUMN role TEXT NOT NULL DEFAULT 'customer'
				`).Error; err != nil {
					t.Fatal(err)
				}
				tableOnly := db.Session(&gorm.Session{NewDB: true})
				tableOnly.Config.IgnoreRelationshipsWhenMigrating = true
				if err := tableOnly.AutoMigrate(&models.AdminAuditLog{}); err != nil {
					t.Fatal(err)
				}
				role := models.PlatformRoleMember
				if err := db.Create(&models.AdminAuditLog{
					Username:     "asymmetric-audit",
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
			db := openPlatformRoleTestDB(
				t,
				"unproven-"+strings.ReplaceAll(test.name, " ", "-"),
			)
			prepareFreshPlatformRoleCutoverFixture(t, db)
			test.insert(t, db)

			err := MigratePlatformRoles(db)
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

func TestMigratePlatformRolesRejectsMismatchedCheckpoint(t *testing.T) {
	db := openPlatformRoleTestDB(t, "checksum")
	prepareFreshPlatformRoleCutoverFixture(t, db)
	if err := db.Create(&models.SchemaMigrationCheckpoint{
		Key:         platformRoleCutoverCheckpointKey,
		Version:     platformRoleCutoverCheckpointVersion,
		Checksum:    strings.Repeat("0", 64),
		CompletedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	err := MigratePlatformRoles(db)
	if err == nil || !strings.Contains(err.Error(), "version or checksum") {
		t.Fatalf("mismatched checkpoint error = %v", err)
	}
}

func openLegacyPlatformRoleTestDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	db := openPlatformRoleTestDB(t, name)
	if err := db.Exec(`
		CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME,
			updated_at DATETIME,
			deleted_at DATETIME,
			username TEXT NOT NULL UNIQUE,
			email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'customer',
			status TEXT NOT NULL DEFAULT 'inactive'
		)
	`).Error; err != nil {
		t.Fatalf("create legacy users table: %v", err)
	}
	if err := db.Exec(`
		CREATE TABLE admin_audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			role TEXT,
			platform_role TEXT NOT NULL DEFAULT 'member'
		)
	`).Error; err != nil {
		t.Fatalf("create legacy admin audit table: %v", err)
	}
	return db
}

func openPlatformRoleTestDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:platform-role-"+name+"?mode=memory&cache=shared"),
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
	return db
}

func insertLegacyPlatformRoleUser(
	t *testing.T,
	db *gorm.DB,
	username, role, status string,
	deleted bool,
) {
	t.Helper()
	var deletedAt any
	if deleted {
		deletedAt = time.Now().UTC()
	}
	if err := db.Exec(`
		INSERT INTO users (
			username, email, password_hash, role, status, deleted_at
		) VALUES (?, ?, 'hash', ?, ?, ?)
	`, username, username+"@example.test", role, status, deletedAt).Error; err != nil {
		t.Fatalf("insert legacy user %q: %v", username, err)
	}
}

func preparePlatformRoleCutoverFixture(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := migrateLegacyHumanRoles(db); err != nil {
		t.Fatalf("prepare legacy roles: %v", err)
	}
	if err := db.Exec(`
		ALTER TABLE users
		ADD COLUMN platform_role TEXT NOT NULL DEFAULT 'member'
	`).Error; err != nil {
		t.Fatalf("add platform role schema: %v", err)
	}
	prepareFreshPlatformRoleCutoverFixture(t, db)
}

func prepareFreshPlatformRoleCutoverFixture(t *testing.T, db *gorm.DB) {
	t.Helper()
	tableOnly := db.Session(&gorm.Session{NewDB: true})
	tableOnly.Config.IgnoreRelationshipsWhenMigrating = true
	if !tableOnly.Migrator().HasTable(&models.User{}) {
		if err := tableOnly.AutoMigrate(&models.User{}); err != nil {
			t.Fatalf("migrate platform user fixture: %v", err)
		}
	}
	if err := tableOnly.AutoMigrate(
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.ProjectMembership{},
		&models.Queue{},
		&models.SchemaMigrationCheckpoint{},
	); err != nil {
		t.Fatalf("migrate platform cutover fixture: %v", err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		organization, err := ensureDefaultOrganization(tx)
		if err != nil {
			return err
		}
		unit, err := ensureDefaultBusinessUnit(tx, organization.ID)
		if err != nil {
			return err
		}
		project, err := ensureDefaultProject(tx, organization.ID, unit.ID)
		if err != nil {
			return err
		}
		if _, err := ensureDefaultQueue(tx, project.ID); err != nil {
			return err
		}
		return tx.Create(&models.SchemaMigrationCheckpoint{
			Key:         projectScopeCutoverCheckpointKey,
			Version:     projectScopeCutoverCheckpointVersion,
			Checksum:    projectScopeCutoverCheckpointChecksum,
			CompletedAt: time.Now().UTC(),
		}).Error
	}); err != nil && !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("create trusted project cutover fixture: %v", err)
	}
}

func testPlatformRoleMembershipWriter(
	_ context.Context,
	tx *gorm.DB,
	user models.User,
	scope models.ProjectScope,
	role models.ProjectRole,
) error {
	return tx.Create(&models.ProjectMembership{
		ProjectID: scope.ProjectID,
		UserID:    user.ID,
		Role:      role,
		IsActive:  true,
		Version:   1,
	}).Error
}

func databaseColumnExists(
	t *testing.T,
	db *gorm.DB,
	tableName, columnName string,
) bool {
	t.Helper()
	columns, err := db.Migrator().ColumnTypes(tableName)
	if err != nil {
		t.Fatalf("read %s columns: %v", tableName, err)
	}
	for _, column := range columns {
		if strings.EqualFold(column.Name(), columnName) {
			return true
		}
	}
	return false
}

func assertPlatformRoleCheckpoint(t *testing.T, db *gorm.DB) {
	t.Helper()
	var checkpoint models.SchemaMigrationCheckpoint
	if err := db.Where("key = ?", platformRoleCutoverCheckpointKey).
		First(&checkpoint).Error; err != nil {
		t.Fatalf("load platform role checkpoint: %v", err)
	}
	if checkpoint.Version != platformRoleCutoverCheckpointVersion ||
		checkpoint.Checksum != platformRoleCutoverCheckpointChecksum {
		t.Fatalf("unexpected platform role checkpoint: %+v", checkpoint)
	}
}
