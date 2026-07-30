package database

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	platformRoleCutoverCheckpointKey      = "20260730_platform_roles_v1_cutover"
	platformRoleCutoverCheckpointVersion  = uint(1)
	platformRoleCutoverCheckpointChecksum = "28131b33ad5c7ebc0d43c92d76b86a39ec04d1e63bfb34b769b01ed956fa63aa"
)

type legacyPlatformRoleUser struct {
	ID           uint
	Role         string
	PlatformRole models.PlatformRole
	Status       models.UserStatus
	DeletedAt    gorm.DeletedAt
}

func lockLegacyPlatformRoleTables(db *gorm.DB) error {
	if db == nil || db.Dialector.Name() != "postgres" {
		return nil
	}
	hasUsers := db.Migrator().HasTable(&models.User{})
	hasAudit := db.Migrator().HasTable(&models.AdminAuditLog{})
	switch {
	case hasUsers && hasAudit:
		if err := db.Exec(
			"LOCK TABLE users, admin_audit_logs IN SHARE ROW EXCLUSIVE MODE",
		).Error; err != nil {
			return fmt.Errorf("lock legacy platform role tables: %w", err)
		}
	case hasUsers:
		if err := db.Exec(
			"LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE",
		).Error; err != nil {
			return fmt.Errorf("lock legacy users table: %w", err)
		}
	case hasAudit:
		if err := db.Exec(
			"LOCK TABLE admin_audit_logs IN SHARE ROW EXCLUSIVE MODE",
		).Error; err != nil {
			return fmt.Errorf("lock legacy admin audit table: %w", err)
		}
	}
	return nil
}

func preflightLegacyPlatformRoleValues(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if db.Migrator().HasTable(&models.User{}) {
		hasLegacyRole, err := hasExactDatabaseColumn(db, "users", "role")
		if err != nil {
			return err
		}
		if hasLegacyRole {
			var unsupported []string
			if err := db.Raw(`
				SELECT DISTINCT COALESCE(role, '<NULL>') AS role
				FROM users
				WHERE role IS NULL OR role NOT IN ?
				ORDER BY role ASC
			`, []string{
				"admin",
				"supervisor",
				"agent",
				"customer",
				"user",
				"superuser",
			}).Scan(&unsupported).Error; err != nil {
				return fmt.Errorf("preflight legacy human roles: %w", err)
			}
			if len(unsupported) > 0 {
				return fmt.Errorf(
					"unsupported legacy human role(s): %s",
					strings.Join(unsupported, ", "),
				)
			}
		}
	}
	if db.Migrator().HasTable(&models.AdminAuditLog{}) {
		hasLegacyRole, err := hasExactDatabaseColumn(
			db,
			"admin_audit_logs",
			"role",
		)
		if err != nil {
			return err
		}
		if hasLegacyRole {
			var unsupported []string
			if err := db.Table("admin_audit_logs").
				Distinct("role").
				Where("role IS NOT NULL AND role <> '' AND role NOT IN ?", []string{
					"admin",
					"supervisor",
					"agent",
					"customer",
					"user",
					"superuser",
				}).
				Order("role ASC").
				Pluck("role", &unsupported).Error; err != nil {
				return fmt.Errorf("preflight legacy admin audit roles: %w", err)
			}
			if len(unsupported) > 0 {
				return fmt.Errorf(
					"unsupported legacy admin audit role(s): %s",
					strings.Join(unsupported, ", "),
				)
			}
		}
	}
	return nil
}

// MigratePlatformRoles performs the destructive, one-time split between
// platform governance duties and project business roles. Project memberships
// are persisted before users.role is removed; the checkpoint is the final
// write in the transaction.
func MigratePlatformRoles(
	db *gorm.DB,
	membershipWriters ...ProjectScopeMembershipWriter,
) error {
	if db == nil {
		return errors.New("database is required")
	}
	if len(membershipWriters) > 1 {
		return errors.New("only one platform role membership writer is supported")
	}
	var membershipWriter ProjectScopeMembershipWriter
	if len(membershipWriters) == 1 {
		membershipWriter = membershipWriters[0]
	}
	if !db.Migrator().HasTable(&models.User{}) {
		return nil
	}
	if !db.Migrator().HasTable(&models.SchemaMigrationCheckpoint{}) {
		return errors.New("platform role migration requires schema migration checkpoints")
	}
	if !db.Migrator().HasColumn(&models.User{}, "platform_role") {
		return errors.New("platform role migration requires users.platform_role")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		completed, err := lockAndReadPlatformRoleCutoverMarker(tx)
		if err != nil {
			return err
		}
		hasLegacyRole, err := hasExactDatabaseColumn(tx, "users", "role")
		if err != nil {
			return err
		}
		hasLegacyAuditRole := false
		if tx.Migrator().HasTable(&models.AdminAuditLog{}) {
			hasLegacyAuditRole, err = hasExactDatabaseColumn(
				tx,
				"admin_audit_logs",
				"role",
			)
			if err != nil {
				return err
			}
		}
		if completed {
			if hasLegacyRole {
				return errors.New(
					"platform role cutover checkpoint exists while legacy users.role is still present",
				)
			}
			if hasLegacyAuditRole {
				return errors.New(
					"platform role cutover checkpoint exists while legacy admin_audit_logs.role is still present",
				)
			}
			return validatePlatformRoleSchema(tx)
		}
		if err := requireProvablePlatformRoleSources(
			tx,
			hasLegacyRole,
			hasLegacyAuditRole,
		); err != nil {
			return err
		}
		if err := ValidateProjectScopeCutoverMarker(tx); err != nil {
			return fmt.Errorf("platform role cutover requires completed project scope migration: %w", err)
		}

		if hasLegacyRole {
			if err := migrateLegacyPlatformRoleRows(tx, membershipWriter); err != nil {
				return err
			}
			if err := tx.Exec("ALTER TABLE users DROP COLUMN role").Error; err != nil {
				return fmt.Errorf("drop legacy users.role: %w", err)
			}
		}
		if err := migrateLegacyAdminAuditRoles(tx); err != nil {
			return err
		}
		if err := validatePlatformRoleSchema(tx); err != nil {
			return err
		}
		if err := tx.Create(&models.SchemaMigrationCheckpoint{
			Key:         platformRoleCutoverCheckpointKey,
			Version:     platformRoleCutoverCheckpointVersion,
			Checksum:    platformRoleCutoverCheckpointChecksum,
			CompletedAt: time.Now().UTC(),
		}).Error; err != nil {
			return fmt.Errorf("record platform role cutover completion: %w", err)
		}
		return nil
	})
}

// requireProvablePlatformRoleSources prevents an operator or a partial
// out-of-band migration from deleting either legacy role source and then
// having this migration certify the resulting state. Human project duties and
// historical administrator audit identity have independent authoritative
// columns, so each missing source is checked against its own retained state.
func requireProvablePlatformRoleSources(
	tx *gorm.DB,
	hasLegacyUserRole bool,
	hasLegacyAuditRole bool,
) error {
	type retainedState struct {
		table string
		model any
	}
	candidates := make([]retainedState, 0, 3)
	if !hasLegacyUserRole {
		candidates = append(
			candidates,
			retainedState{table: "users", model: &models.User{}},
			retainedState{
				table: "project_memberships",
				model: &models.ProjectMembership{},
			},
		)
	}
	if !hasLegacyAuditRole {
		candidates = append(
			candidates,
			retainedState{
				table: "admin_audit_logs",
				model: &models.AdminAuditLog{},
			},
		)
	}
	var retained []string
	for _, candidate := range candidates {
		if !tx.Migrator().HasTable(candidate.model) {
			continue
		}
		var count int64
		if err := tx.Unscoped().Model(candidate.model).Count(&count).Error; err != nil {
			return fmt.Errorf(
				"inspect uncheckpointed platform role state in %s: %w",
				candidate.table,
				err,
			)
		}
		if count > 0 {
			retained = append(
				retained,
				fmt.Sprintf("%s=%d", candidate.table, count),
			)
		}
	}
	if len(retained) > 0 {
		return fmt.Errorf(
			"cannot prove platform role cutover after a legacy role source was removed; "+
				"retained state requires operator recovery: %s",
			strings.Join(retained, ", "),
		)
	}
	return nil
}

func migrateLegacyPlatformRoleRows(
	tx *gorm.DB,
	membershipWriter ProjectScopeMembershipWriter,
) error {
	var users []legacyPlatformRoleUser
	if err := tx.Unscoped().
		Table("users").
		Select("id", "role", "platform_role", "status", "deleted_at").
		Order("id ASC").
		Find(&users).Error; err != nil {
		return fmt.Errorf("load legacy users for platform role cutover: %w", err)
	}
	for i := range users {
		if _, err := defaultProjectRoleForLegacyUser(users[i].Role); err != nil {
			return fmt.Errorf("user %d: %w", users[i].ID, err)
		}
	}

	scope, err := defaultPlatformRoleCutoverScope(tx)
	if err != nil {
		return err
	}
	migrationContext := tx.Statement.Context
	if migrationContext == nil {
		migrationContext = context.Background()
	}
	for i := range users {
		platformRole := models.PlatformRoleMember
		if users[i].Role == "admin" {
			platformRole = models.PlatformRolePlatformAdmin
		}
		if err := tx.Unscoped().
			Table("users").
			Where("id = ?", users[i].ID).
			Update("platform_role", platformRole).Error; err != nil {
			return fmt.Errorf("update platform role for user %d: %w", users[i].ID, err)
		}
		if users[i].Status != models.UserStatusActive || users[i].DeletedAt.Valid {
			continue
		}

		var existing models.ProjectMembership
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("project_id = ? AND user_id = ?", scope.ProjectID, users[i].ID).
			First(&existing)
		switch {
		case query.Error == nil:
			continue
		case !errors.Is(query.Error, gorm.ErrRecordNotFound):
			return fmt.Errorf(
				"load default project membership for user %d: %w",
				users[i].ID,
				query.Error,
			)
		case membershipWriter == nil:
			return fmt.Errorf(
				"audited platform role membership writer is required for legacy user %d",
				users[i].ID,
			)
		}
		projectRole, _ := defaultProjectRoleForLegacyUser(users[i].Role)
		user := models.User{
			ID:           users[i].ID,
			PlatformRole: platformRole,
			Status:       users[i].Status,
		}
		user.DeletedAt = users[i].DeletedAt
		if err := membershipWriter(
			migrationContext,
			tx,
			user,
			scope,
			projectRole,
		); err != nil {
			return fmt.Errorf(
				"backfill audited default project membership for user %d: %w",
				users[i].ID,
				err,
			)
		}
	}
	return nil
}

func defaultPlatformRoleCutoverScope(tx *gorm.DB) (models.ProjectScope, error) {
	var organization models.Organization
	if err := tx.Where(
		"slug = ? AND status = ?",
		DefaultOrganizationSlug,
		models.OrganizationStatusActive,
	).First(&organization).Error; err != nil {
		return models.ProjectScope{}, fmt.Errorf(
			"load trusted default organization for platform role cutover: %w",
			err,
		)
	}
	var project models.Project
	if err := tx.Where(
		"organization_id = ? AND key = ? AND status = ?",
		organization.ID,
		DefaultProjectKey,
		models.ProjectStatusActive,
	).First(&project).Error; err != nil {
		return models.ProjectScope{}, fmt.Errorf(
			"load trusted default project for platform role cutover: %w",
			err,
		)
	}
	return project.Scope(), nil
}

func lockAndReadPlatformRoleCutoverMarker(tx *gorm.DB) (bool, error) {
	if tx.Dialector.Name() == "postgres" {
		if err := tx.Exec(
			"SELECT pg_advisory_xact_lock(hashtext(?))",
			platformRoleCutoverCheckpointKey,
		).Error; err != nil {
			return false, fmt.Errorf("lock platform role cutover: %w", err)
		}
	}
	var checkpoint models.SchemaMigrationCheckpoint
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("key = ?", platformRoleCutoverCheckpointKey).
		First(&checkpoint).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read platform role cutover checkpoint: %w", err)
	}
	if checkpoint.Version != platformRoleCutoverCheckpointVersion ||
		checkpoint.Checksum != platformRoleCutoverCheckpointChecksum {
		return false, fmt.Errorf(
			"platform role cutover checkpoint %q has unexpected version or checksum",
			platformRoleCutoverCheckpointKey,
		)
	}
	return true, nil
}

func validatePlatformRoleSchema(db *gorm.DB) error {
	if !db.Migrator().HasColumn(&models.User{}, "platform_role") {
		return errors.New("users.platform_role is missing")
	}
	hasLegacyRole, err := hasExactDatabaseColumn(db, "users", "role")
	if err != nil {
		return err
	}
	if hasLegacyRole {
		return errors.New("legacy users.role is still present")
	}
	var invalid []string
	if err := db.Unscoped().
		Table("users").
		Distinct("platform_role").
		Where("platform_role IS NULL OR platform_role NOT IN ?", []string{
			string(models.PlatformRolePlatformAdmin),
			string(models.PlatformRoleSecurityAuditor),
			string(models.PlatformRoleEmergencyOperator),
			string(models.PlatformRoleMember),
		}).
		Order("platform_role ASC").
		Pluck("platform_role", &invalid).Error; err != nil {
		return fmt.Errorf("validate platform role values: %w", err)
	}
	if len(invalid) > 0 {
		return fmt.Errorf("unsupported platform role(s): %s", strings.Join(invalid, ", "))
	}
	if db.Dialector.Name() == "postgres" {
		if err := validatePostgresPlatformRoleContract(
			db,
			"users",
			"chk_users_platform_role",
		); err != nil {
			return err
		}
	}
	if db.Migrator().HasTable(&models.AdminAuditLog{}) {
		hasAuditPlatformRole, err := hasExactDatabaseColumn(
			db,
			"admin_audit_logs",
			"platform_role",
		)
		if err != nil {
			return err
		}
		if !hasAuditPlatformRole {
			return errors.New("admin_audit_logs.platform_role is missing")
		}
		hasLegacyAuditRole, err := hasExactDatabaseColumn(
			db,
			"admin_audit_logs",
			"role",
		)
		if err != nil {
			return err
		}
		if hasLegacyAuditRole {
			return errors.New("legacy admin_audit_logs.role is still present")
		}
		var invalidAuditRoles []string
		if err := db.Table("admin_audit_logs").
			Distinct("platform_role").
			Where("platform_role IS NULL OR platform_role NOT IN ?", []string{
				string(models.PlatformRolePlatformAdmin),
				string(models.PlatformRoleSecurityAuditor),
				string(models.PlatformRoleEmergencyOperator),
				string(models.PlatformRoleMember),
			}).
			Order("platform_role ASC").
			Pluck("platform_role", &invalidAuditRoles).Error; err != nil {
			return fmt.Errorf("validate admin audit platform role values: %w", err)
		}
		if len(invalidAuditRoles) > 0 {
			return fmt.Errorf(
				"unsupported admin audit platform role(s): %s",
				strings.Join(invalidAuditRoles, ", "),
			)
		}
		if db.Dialector.Name() == "postgres" {
			if err := validatePostgresPlatformRoleContract(
				db,
				"admin_audit_logs",
				"chk_admin_audit_logs_platform_role",
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func validatePostgresPlatformRoleContract(
	db *gorm.DB,
	tableName, constraintName string,
) error {
	type platformRoleColumnContract struct {
		IsNullable    string
		ColumnDefault *string
	}
	var column platformRoleColumnContract
	if err := db.Raw(`
		SELECT is_nullable, column_default
		FROM information_schema.columns
		WHERE table_schema = CURRENT_SCHEMA()
		  AND table_name = ?
		  AND column_name = 'platform_role'
	`, tableName).Scan(&column).Error; err != nil {
		return fmt.Errorf("read %s.platform_role contract: %w", tableName, err)
	}
	if column.IsNullable != "NO" ||
		column.ColumnDefault == nil ||
		!isExactMemberPlatformRoleDefault(*column.ColumnDefault) {
		return fmt.Errorf(
			"%s.platform_role must be NOT NULL with member default",
			tableName,
		)
	}

	var checkContract struct {
		Definition string
		Validated  bool
		NoInherit  bool
	}
	if err := db.Raw(`
		SELECT
			pg_get_constraintdef(c.oid) AS definition,
			c.convalidated AS validated,
			c.connoinherit AS no_inherit
		FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		JOIN pg_attribute a
		  ON a.attrelid = t.oid
		 AND a.attname = 'platform_role'
		WHERE n.nspname = CURRENT_SCHEMA()
		  AND t.relname = ?
		  AND c.conname = ?
		  AND c.contype = 'c'
		  AND c.conkey = ARRAY[a.attnum]::smallint[]
	`, tableName, constraintName).Scan(&checkContract).Error; err != nil {
		return fmt.Errorf("read %s platform role check: %w", tableName, err)
	}
	if !checkContract.Validated ||
		checkContract.NoInherit ||
		!isExactPlatformRoleCheckDefinition(checkContract.Definition) {
		return fmt.Errorf(
			"%s platform role check constraint has unexpected semantics",
			tableName,
		)
	}

	var indexCount int64
	if err := db.Raw(`
		SELECT COUNT(*)
		FROM pg_index i
		JOIN pg_class t ON t.oid = i.indrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		JOIN pg_attribute a
		  ON a.attrelid = t.oid
		 AND a.attnum = i.indkey[0]
		WHERE n.nspname = CURRENT_SCHEMA()
		  AND t.relname = ?
		  AND i.indnatts = 1
		  AND i.indnkeyatts = 1
		  AND i.indisvalid
		  AND i.indisready
		  AND i.indpred IS NULL
		  AND i.indexprs IS NULL
		  AND a.attname = 'platform_role'
	`, tableName).Scan(&indexCount).Error; err != nil {
		return fmt.Errorf("read %s platform role index: %w", tableName, err)
	}
	if indexCount == 0 {
		return fmt.Errorf("%s.platform_role index is missing", tableName)
	}
	return nil
}

var postgresTextCastPattern = regexp.MustCompile(
	`(?i)::\s*(?:character\s+varying|varchar|text)\s*(?:\[\s*\])?`,
)

func isExactPlatformRoleCheckDefinition(definition string) bool {
	withoutCasts := postgresTextCastPattern.ReplaceAllString(
		definition,
		"",
	)
	canonical := strings.ToLower(withoutCasts)
	canonical = strings.Map(func(character rune) rune {
		switch character {
		case ' ', '\t', '\r', '\n', '(', ')', '[', ']', '"':
			return -1
		default:
			return character
		}
	}, canonical)
	var values []string
	literalPattern := regexp.MustCompile(`'([^']*)'`)
	for _, match := range literalPattern.FindAllStringSubmatch(
		canonical,
		-1,
	) {
		values = append(values, match[1])
	}
	if len(values) != 4 {
		return false
	}
	expected := map[string]struct{}{
		string(models.PlatformRolePlatformAdmin):     {},
		string(models.PlatformRoleSecurityAuditor):   {},
		string(models.PlatformRoleEmergencyOperator): {},
		string(models.PlatformRoleMember):            {},
	}
	for _, value := range values {
		if _, ok := expected[value]; !ok {
			return false
		}
		delete(expected, value)
	}
	if len(expected) != 0 {
		return false
	}
	structure := literalPattern.ReplaceAllString(canonical, "?")
	return structure == "checkplatform_rolein?,?,?,?" ||
		structure == "checkplatform_role=anyarray?,?,?,?"
}

func isExactMemberPlatformRoleDefault(defaultValue string) bool {
	normalized := strings.TrimSpace(defaultValue)
	for strings.HasPrefix(normalized, "(") && strings.HasSuffix(normalized, ")") {
		normalized = strings.TrimSpace(normalized[1 : len(normalized)-1])
	}
	switch normalized {
	case "'member'", "'member'::text", "'member'::character varying":
		return true
	default:
		return false
	}
}

func migrateLegacyAdminAuditRoles(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&models.AdminAuditLog{}) {
		return nil
	}
	hasLegacyRole, err := hasExactDatabaseColumn(
		tx,
		"admin_audit_logs",
		"role",
	)
	if err != nil || !hasLegacyRole {
		return err
	}
	var unsupported []string
	if err := tx.Table("admin_audit_logs").
		Distinct("role").
		Where("role IS NOT NULL AND role <> '' AND role NOT IN ?", []string{
			"admin",
			"supervisor",
			"agent",
			"customer",
			"user",
			"superuser",
		}).
		Order("role ASC").
		Pluck("role", &unsupported).Error; err != nil {
		return fmt.Errorf("inspect legacy admin audit roles: %w", err)
	}
	if len(unsupported) > 0 {
		return fmt.Errorf(
			"unsupported legacy admin audit role(s): %s",
			strings.Join(unsupported, ", "),
		)
	}
	if err := tx.Table("admin_audit_logs").
		Where("role IN ?", []string{"admin", "superuser"}).
		Update("platform_role", models.PlatformRolePlatformAdmin).Error; err != nil {
		return fmt.Errorf("migrate administrator audit roles: %w", err)
	}
	if err := tx.Table("admin_audit_logs").
		Where(
			"role IS NULL OR role = '' OR role IN ?",
			[]string{"supervisor", "agent", "customer", "user"},
		).
		Update("platform_role", models.PlatformRoleMember).Error; err != nil {
		return fmt.Errorf("migrate member audit roles: %w", err)
	}
	if err := tx.Exec(
		"ALTER TABLE admin_audit_logs DROP COLUMN role",
	).Error; err != nil {
		return fmt.Errorf("drop legacy admin_audit_logs.role: %w", err)
	}
	return nil
}

func hasExactDatabaseColumn(
	db *gorm.DB,
	tableName, columnName string,
) (bool, error) {
	columns, err := db.Migrator().ColumnTypes(tableName)
	if err != nil {
		return false, fmt.Errorf("read %s columns: %w", tableName, err)
	}
	for _, column := range columns {
		if strings.EqualFold(column.Name(), columnName) {
			return true, nil
		}
	}
	return false, nil
}

// ValidatePlatformRoleCutover prevents runtime startup with an incomplete or
// drifted destructive platform-role migration.
func ValidatePlatformRoleCutover(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if !db.Migrator().HasTable(&models.SchemaMigrationCheckpoint{}) {
		return errors.New(
			"platform role cutover checkpoint table is missing; run `go run ./cmd/migrate`",
		)
	}
	var checkpoint models.SchemaMigrationCheckpoint
	if err := db.Where("key = ?", platformRoleCutoverCheckpointKey).
		First(&checkpoint).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return errors.New(
			"platform role cutover is incomplete; run `go run ./cmd/migrate`",
		)
	} else if err != nil {
		return fmt.Errorf("validate platform role cutover checkpoint: %w", err)
	}
	if checkpoint.Version != platformRoleCutoverCheckpointVersion ||
		checkpoint.Checksum != platformRoleCutoverCheckpointChecksum {
		return fmt.Errorf(
			"platform role cutover checkpoint %q has unexpected version or checksum",
			platformRoleCutoverCheckpointKey,
		)
	}
	return validatePlatformRoleSchema(db)
}
