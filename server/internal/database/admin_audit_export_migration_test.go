package database

import (
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPrepareAdminAuditActorColumnsBackfillsLegacyRows(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:legacy_admin_audit_actor?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		CREATE TABLE admin_audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER,
			username VARCHAR(100),
			platform_role VARCHAR(30) NOT NULL DEFAULT 'member',
			action VARCHAR(255)
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		INSERT INTO admin_audit_logs(user_id, username, platform_role, action)
		VALUES (42, 'historical-human', 'security_auditor', 'legacy')
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := PrepareAdminAuditActorColumns(db); err != nil {
		t.Fatal(err)
	}
	var row struct {
		ActorType    string
		ActorID      string
		PlatformRole string
	}
	if err := db.Table("admin_audit_logs").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.ActorType != "human" ||
		row.ActorID != "42" ||
		row.PlatformRole != "security_auditor" {
		t.Fatalf("backfilled audit actor = %+v", row)
	}
	if err := PrepareAdminAuditActorColumns(db); err != nil {
		t.Fatalf("migration must be idempotent: %v", err)
	}
}

func TestBackfillLegacyAdminAuditPlatformRolesPreservesActorSemantics(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open("file:legacy_admin_audit_roles?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		CREATE TABLE admin_audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			role VARCHAR(50),
			platform_role VARCHAR(30),
			actor_type VARCHAR(32),
			actor_id VARCHAR(128)
		);
		INSERT INTO admin_audit_logs(role, actor_type, actor_id)
		VALUES
			('admin', 'human', 'human:1'),
			('supervisor', 'human', 'human:2'),
			(NULL, 'system', 'system:migration')
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillLegacyAdminAuditPlatformRoles(db); err != nil {
		t.Fatal(err)
	}
	type projectedRole struct {
		ActorType    string
		PlatformRole *string
	}
	var rows []projectedRole
	if err := db.Table("admin_audit_logs").
		Order("id ASC").
		Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("projected audit rows = %d, want 3", len(rows))
	}
	if rows[0].PlatformRole == nil ||
		*rows[0].PlatformRole != string(models.PlatformRolePlatformAdmin) {
		t.Fatalf("administrator projection = %+v", rows[0])
	}
	if rows[1].PlatformRole == nil ||
		*rows[1].PlatformRole != string(models.PlatformRoleMember) {
		t.Fatalf("member projection = %+v", rows[1])
	}
	if rows[2].PlatformRole != nil {
		t.Fatalf("system actor received human platform role: %+v", rows[2])
	}
	if err := backfillLegacyAdminAuditPlatformRoles(db); err != nil {
		t.Fatalf("projection must be idempotent: %v", err)
	}
}

func TestBackfillLegacyAdminAuditPlatformRolesRejectsNonHumanLegacyRole(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open("file:invalid_nonhuman_audit_role?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		CREATE TABLE admin_audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			role VARCHAR(50),
			platform_role VARCHAR(30),
			actor_type VARCHAR(32),
			actor_id VARCHAR(128)
		);
		INSERT INTO admin_audit_logs(
			role, platform_role, actor_type, actor_id
		) VALUES (
			'admin', NULL, 'service_principal', 'service_principal:1'
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	err = backfillLegacyAdminAuditPlatformRoles(db)
	if err == nil || !strings.Contains(err.Error(), "non-human") {
		t.Fatalf("non-human legacy role error = %v", err)
	}
}

func TestMigrateAdminAuditExportContractCreatesDurableJobIndexes(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open("file:admin_audit_export_contract?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.AdminAuditLog{}); err != nil {
		t.Fatal(err)
	}
	if err := MigrateAdminAuditExportContract(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable(&models.AdminAuditExportJob{}) {
		t.Fatal("admin audit export job table is missing")
	}
	for _, index := range []string{
		"idx_admin_audit_actor_created_id",
		"idx_admin_audit_exports_claim",
		"idx_admin_audit_exports_lease",
		"idx_admin_audit_exports_owner",
		"idx_admin_audit_exports_expiry",
	} {
		model := any(&models.AdminAuditExportJob{})
		if index == "idx_admin_audit_actor_created_id" {
			model = &models.AdminAuditLog{}
		}
		if !db.Migrator().HasIndex(model, index) {
			t.Fatalf("index %s is missing", index)
		}
	}
}

func TestNullableAdminAuditPlatformRoleCheckContract(t *testing.T) {
	for _, valid := range []string{
		`CHECK ((platform_role IS NULL) OR ((platform_role)::text = ANY ((ARRAY['platform_admin'::character varying, 'security_auditor'::character varying, 'emergency_operator'::character varying, 'member'::character varying])::text[])))`,
		`CHECK (platform_role IS NULL OR platform_role IN ('platform_admin','security_auditor','emergency_operator','member'))`,
	} {
		if !isExactNullableAuditPlatformRoleCheckDefinition(valid) {
			t.Fatalf("valid nullable audit role check rejected: %s", valid)
		}
	}
	for _, invalid := range []string{
		`CHECK (platform_role IN ('platform_admin','security_auditor','emergency_operator','member'))`,
		`CHECK (platform_role IS NULL OR platform_role IN ('platform_admin','security_auditor','member'))`,
		`CHECK (platform_role IS NULL OR platform_role IN ('platform_admin','security_auditor','emergency_operator','member','owner'))`,
	} {
		if isExactNullableAuditPlatformRoleCheckDefinition(invalid) {
			t.Fatalf("invalid nullable audit role check accepted: %s", invalid)
		}
	}
}

func TestAdminAuditExportUUIDv7CheckContract(t *testing.T) {
	for _, valid := range []string{
		`CHECK ((public_id)::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'::text)`,
		`CHECK (public_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$')`,
	} {
		if !isExactAdminAuditExportUUIDv7Check(valid) {
			t.Fatalf("valid audit export UUIDv7 check rejected: %s", valid)
		}
	}
	for _, invalid := range []string{
		`CHECK (public_id ~ '^[0-9a-f-]{36}$')`,
		`CHECK (lower(public_id) ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$')`,
		`CHECK (public_id ~ '^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-7[0-9A-Fa-f]{3}-[89abAB][0-9A-Fa-f]{3}-[0-9A-Fa-f]{12}$')`,
	} {
		if isExactAdminAuditExportUUIDv7Check(invalid) {
			t.Fatalf("invalid audit export UUIDv7 check accepted: %s", invalid)
		}
	}
}
