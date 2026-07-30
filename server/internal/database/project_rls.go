package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
)

const projectRLSPolicyName = "chronodesk_project_scope"

const postgresProjectScopeMaxID = uint64(1<<63 - 1)

var projectRLSIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// requiredProjectOwnedTableNames is the fail-closed project-data inventory. It
// makes missing project ownership columns visible instead of silently treating
// an indirect foreign-key join as an authorization boundary.
var requiredProjectOwnedTableNames = []string{
	// Ticket business state and its transactional audit/outbox records.
	"tickets",
	"ticket_comments",
	"ticket_attachments",
	"ticket_histories",
	"ticket_leases",
	"entity_links",
	"ticket_relations",
	"policy_decisions",
	"domain_events",
	"outbox_deliveries",
	"idempotency_records",
	"notifications",
	"webhook_configs",
	"webhook_delivery_snapshots",
	"webhook_logs",
	"automation_rules",
	"automation_logs",
	"sla_configs",
	"ticket_templates",
	"quick_replies",

	// A2A task state is linked to a Ticket but remains independently queried.
	"agent_tasks",
	"agent_messages",
	"agent_artifacts",
	"agent_task_status_history",
	"agent_task_events",
	"agent_push_notification_configs",

	// Observable AI collaboration state.
	"agent_runs",
	"action_proposals",
	"approval_tasks",
	"approval_decisions",
	"handoffs",
	"evidence_references",

	// Knowledge metadata is authoritative in PostgreSQL; the search index is
	// a rebuildable projection that receives the same project and ACL filters.
	"knowledge_articles",
	"knowledge_article_versions",
	"knowledge_article_acl",
	"knowledge_ingestion_tasks",
	"knowledge_chunks",
	"knowledge_citations",
	"knowledge_feedback",
	"knowledge_index_states",
	"project_model_policies",

	// Project audit chains are independently queryable and must never leak
	// across project boundaries even though entries are append-only.
	"audit_chain_heads",
	"audit_ledger_entries",

	// Project-owned integration state.
	"connector_definitions",
	"connections",
	"mapping_versions",
	"inbox_messages",
	"inbox_receipts",
	"external_links",
	"sync_cursors",
	"sync_runs",
	"integration_conflicts",
	"dead_letters",

	// Versioned project configuration and signed solution installations.
	"request_type_versions",
	"workflow_versions",
	"configuration_releases",
	"project_solution_installations",
}

// Every table in the complete inventory is scope-ready after the breaking v2
// migration, so policy installation no longer retains the former ticket-only
// staging subset. ENABLE/FORCE remains an explicit deployment cutover until
// every repository transaction has moved to WithProjectScopeTransaction.
var projectRLSProtectedTableNames = append(
	[]string(nil),
	requiredProjectOwnedTableNames...,
)

// ProjectRLSProtectedTables returns a defensive copy of the tables whose
// PostgreSQL RLS policy is currently installed and required at runtime.
func ProjectRLSProtectedTables() []string {
	return append([]string(nil), projectRLSProtectedTableNames...)
}

// RequiredProjectOwnedTables returns a defensive copy of the complete
// project-owned table inventory. Call ValidateRequiredProjectOwnedTableScopes
// while expanding the schema to obtain an explicit list of missing tables and
// scope columns.
func RequiredProjectOwnedTables() []string {
	return append([]string(nil), requiredProjectOwnedTableNames...)
}

// ValidateRequiredProjectOwnedTableScopes audits the complete inventory. It
// fails while any project-owned table is absent or lacks either scope column;
// callers must never infer scope through a related Ticket row.
func ValidateRequiredProjectOwnedTableScopes(db *gorm.DB) error {
	return validateProjectOwnedTableScopes(db, requiredProjectOwnedTableNames)
}

// MigrateProjectRLS installs the canonical PostgreSQL policy for every
// scope-ready table without enabling it. Deployment enables RLS only after all
// repository and worker paths use WithProjectScopeTransaction; this staged
// migration avoids turning a rolling application upgrade into an outage.
// Schema validation happens before DDL so a newly listed table can never
// receive a partial or organization-only policy.
func MigrateProjectRLS(db *gorm.DB) error {
	if err := validateProjectOwnedTableScopes(db, projectRLSProtectedTableNames); err != nil {
		return fmt.Errorf("project RLS schema validation failed: %w", err)
	}
	if db.Dialector.Name() != "postgres" {
		return nil
	}

	return db.Transaction(func(tx *gorm.DB) error {
		for _, tableName := range projectRLSProtectedTableNames {
			quotedTable, err := quoteProjectRLSIdentifier(tableName)
			if err != nil {
				return err
			}
			statements := []string{
				fmt.Sprintf(
					"DROP POLICY IF EXISTS %s ON %s",
					quoteStaticProjectRLSIdentifier(projectRLSPolicyName),
					quotedTable,
				),
				fmt.Sprintf(
					"CREATE POLICY %s ON %s FOR ALL TO PUBLIC USING (%s) WITH CHECK (%s)",
					quoteStaticProjectRLSIdentifier(projectRLSPolicyName),
					quotedTable,
					projectRLSPredicateSQL,
					projectRLSPredicateSQL,
				),
			}
			for _, statement := range statements {
				if err := tx.Exec(statement).Error; err != nil {
					return fmt.Errorf("install project RLS on %s: %w", tableName, err)
				}
			}
		}
		if err := validatePostgresProjectRLSState(tx, false); err != nil {
			return fmt.Errorf("validate installed project RLS: %w", err)
		}
		return nil
	})
}

// ValidateProjectRLSReadiness verifies the scope columns and canonical policy
// installed by MigrateProjectRLS. It deliberately does not require ENABLE or
// FORCE RLS during the rolling-upgrade preparation stage.
func ValidateProjectRLSReadiness(db *gorm.DB) error {
	if err := validateProjectOwnedTableScopes(db, projectRLSProtectedTableNames); err != nil {
		return fmt.Errorf("project RLS schema validation failed: %w", err)
	}
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	return validatePostgresProjectRLSState(db, false)
}

// EnableProjectRLS is the explicit deployment cutover. It enables and forces
// RLS atomically, then verifies the complete PostgreSQL policy state before the
// transaction commits.
func EnableProjectRLS(db *gorm.DB) error {
	if err := validateProjectOwnedTableScopes(db, projectRLSProtectedTableNames); err != nil {
		return fmt.Errorf("project RLS schema validation failed: %w", err)
	}
	if db.Dialector.Name() != "postgres" {
		return errors.New("project RLS can only be enabled on PostgreSQL")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		for _, tableName := range projectRLSProtectedTableNames {
			quotedTable, err := quoteProjectRLSIdentifier(tableName)
			if err != nil {
				return err
			}
			for _, statement := range []string{
				fmt.Sprintf("ALTER TABLE %s ENABLE ROW LEVEL SECURITY", quotedTable),
				fmt.Sprintf("ALTER TABLE %s FORCE ROW LEVEL SECURITY", quotedTable),
			} {
				if err := tx.Exec(statement).Error; err != nil {
					return fmt.Errorf("enable project RLS on %s: %w", tableName, err)
				}
			}
		}
		if err := validatePostgresProjectRLSState(tx, true); err != nil {
			return fmt.Errorf("validate enabled project RLS: %w", err)
		}
		return nil
	})
}

// ValidateProjectRLSRuntime is the post-cutover startup gate for policy,
// ENABLE, and FORCE state. Least-privilege role validation is deliberately a
// separate gate because migrations must run as a table owner.
func ValidateProjectRLSRuntime(db *gorm.DB) error {
	if err := validateProjectOwnedTableScopes(db, projectRLSProtectedTableNames); err != nil {
		return fmt.Errorf("project RLS schema validation failed: %w", err)
	}
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	return validatePostgresProjectRLSState(db, true)
}

// ValidateProjectRuntimeRole rejects PostgreSQL application identities that
// can bypass RLS. It is called by application startup after migrations, never
// by the privileged migration command.
func ValidateProjectRuntimeRole(db *gorm.DB) error {
	if db == nil || db.Config == nil || db.Statement == nil || db.Dialector == nil {
		return errors.New("database is required")
	}
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	return validatePostgresProjectRLSRole(db)
}

// WithProjectScopeTransaction is the only database transaction entry point for
// project-owned repository work. PostgreSQL set_config(..., true) is the
// parameter-safe equivalent of SET LOCAL: values are bound parameters and are
// automatically discarded on commit or rollback. SQLite still validates scope
// and transaction semantics, but does not pretend to provide RLS.
func WithProjectScopeTransaction(
	ctx context.Context,
	db *gorm.DB,
	scope models.ProjectScope,
	fn func(*gorm.DB) error,
) error {
	return scopeddb.WithProjectScopeTransaction(ctx, db, scope, fn)
}

const projectRLSPredicateSQL = `
organization_id = NULLIF(current_setting('chronodesk.organization_id', true), '')::bigint
AND (
	project_id = NULLIF(current_setting('chronodesk.project_id', true), '')::bigint
	OR project_id = ANY(
		COALESCE(
			string_to_array(
				NULLIF(
					current_setting('chronodesk.project_ids', true),
					''
				),
				','
			)::bigint[],
			ARRAY[]::bigint[]
		)
	)
)`

func validateProjectOwnedTableScopes(db *gorm.DB, tableNames []string) error {
	if db == nil || db.Config == nil || db.Statement == nil || db.Dialector == nil {
		return errors.New("database is required")
	}
	if len(tableNames) == 0 {
		return errors.New("at least one project-owned table is required")
	}

	var missing []string
	for _, tableName := range tableNames {
		if _, err := quoteProjectRLSIdentifier(tableName); err != nil {
			return err
		}
		if !db.Migrator().HasTable(tableName) {
			missing = append(missing, tableName)
			continue
		}
		for _, columnName := range []string{"organization_id", "project_id"} {
			if !db.Migrator().HasColumn(tableName, columnName) {
				missing = append(missing, tableName+"."+columnName)
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf(
			"project-owned tables require explicit organization_id and project_id columns; missing: %s",
			strings.Join(missing, ", "),
		)
	}
	return nil
}

func quoteProjectRLSIdentifier(identifier string) (string, error) {
	if !projectRLSIdentifierPattern.MatchString(identifier) {
		return "", fmt.Errorf("unsafe project RLS SQL identifier %q", identifier)
	}
	for _, allowed := range requiredProjectOwnedTableNames {
		if identifier == allowed {
			return quoteStaticProjectRLSIdentifier(identifier), nil
		}
	}
	return "", fmt.Errorf("project RLS SQL identifier %q is not allowlisted", identifier)
}

func quoteStaticProjectRLSIdentifier(identifier string) string {
	return `"` + identifier + `"`
}

type postgresProjectRLSState struct {
	TableName        string         `gorm:"column:table_name"`
	OwnerName        string         `gorm:"column:owner_name"`
	RowSecurity      bool           `gorm:"column:row_security"`
	ForceRowSecurity bool           `gorm:"column:force_row_security"`
	PolicyCount      int64          `gorm:"column:policy_count"`
	PolicyName       sql.NullString `gorm:"column:policy_name"`
	PolicyCommand    sql.NullString `gorm:"column:policy_command"`
	PolicyPermissive bool           `gorm:"column:policy_permissive"`
	PolicyPublic     bool           `gorm:"column:policy_public"`
	UsingExpression  sql.NullString `gorm:"column:using_expression"`
	CheckExpression  sql.NullString `gorm:"column:check_expression"`
}

func validatePostgresProjectRLSState(db *gorm.DB, requireEnabled bool) error {
	tableNames := ProjectRLSProtectedTables()
	var states []postgresProjectRLSState
	if err := db.Raw(`
		SELECT
			c.relname AS table_name,
			owner.rolname AS owner_name,
			c.relrowsecurity AS row_security,
			c.relforcerowsecurity AS force_row_security,
			COUNT(policy.oid)::bigint AS policy_count,
			MAX(policy.polname) FILTER (
				WHERE policy.polname = ?
			) AS policy_name,
			MAX(policy.polcmd::text) FILTER (
				WHERE policy.polname = ?
			) AS policy_command,
			COALESCE(BOOL_AND(policy.polpermissive) FILTER (
				WHERE policy.polname = ?
			), false) AS policy_permissive,
			COALESCE(BOOL_AND(policy.polroles = ARRAY[0]::oid[]) FILTER (
				WHERE policy.polname = ?
			), false) AS policy_public,
			MAX(pg_get_expr(policy.polqual, policy.polrelid)) FILTER (
				WHERE policy.polname = ?
			) AS using_expression,
			MAX(pg_get_expr(policy.polwithcheck, policy.polrelid)) FILTER (
				WHERE policy.polname = ?
			) AS check_expression
		FROM pg_class AS c
		JOIN pg_namespace AS namespace ON namespace.oid = c.relnamespace
		JOIN pg_roles AS owner ON owner.oid = c.relowner
		LEFT JOIN pg_policy AS policy ON policy.polrelid = c.oid
		WHERE namespace.nspname = CURRENT_SCHEMA()
		  AND c.relkind IN ('r', 'p')
		  AND c.relname IN ?
		GROUP BY c.oid, c.relname, owner.rolname, c.relrowsecurity, c.relforcerowsecurity
	`,
		projectRLSPolicyName,
		projectRLSPolicyName,
		projectRLSPolicyName,
		projectRLSPolicyName,
		projectRLSPolicyName,
		projectRLSPolicyName,
		tableNames,
	).Scan(&states).Error; err != nil {
		return fmt.Errorf("read PostgreSQL project RLS metadata: %w", err)
	}

	stateByTable := make(map[string]postgresProjectRLSState, len(states))
	for _, state := range states {
		stateByTable[state.TableName] = state
	}

	expectedPredicate := normalizeProjectRLSPredicate(projectRLSPredicateSQL)
	var violations []string
	for _, tableName := range tableNames {
		state, exists := stateByTable[tableName]
		if !exists {
			violations = append(violations, tableName+" (table missing)")
			continue
		}
		if requireEnabled {
			if !state.RowSecurity {
				violations = append(violations, tableName+" (RLS disabled)")
			}
			if !state.ForceRowSecurity {
				violations = append(violations, tableName+" (FORCE RLS disabled)")
			}
		} else if state.RowSecurity != state.ForceRowSecurity {
			violations = append(
				violations,
				tableName+" (ENABLE and FORCE RLS must be staged together)",
			)
		}
		if state.PolicyCount != 1 {
			violations = append(
				violations,
				fmt.Sprintf("%s (expected exactly one policy, found %d)", tableName, state.PolicyCount),
			)
			continue
		}
		if !state.PolicyName.Valid || state.PolicyName.String != projectRLSPolicyName {
			violations = append(violations, tableName+" (canonical policy missing)")
		}
		if !state.PolicyCommand.Valid || state.PolicyCommand.String != "*" {
			violations = append(violations, tableName+" (policy must apply to ALL commands)")
		}
		if !state.PolicyPermissive || !state.PolicyPublic {
			violations = append(violations, tableName+" (policy must apply to PUBLIC)")
		}
		if !state.UsingExpression.Valid ||
			normalizeProjectRLSPredicate(state.UsingExpression.String) != expectedPredicate {
			violations = append(violations, tableName+" (USING predicate mismatch)")
		}
		if !state.CheckExpression.Valid ||
			normalizeProjectRLSPredicate(state.CheckExpression.String) != expectedPredicate {
			violations = append(violations, tableName+" (WITH CHECK predicate mismatch)")
		}
	}
	if len(violations) > 0 {
		return fmt.Errorf(
			"PostgreSQL project RLS contract is incomplete: %s",
			strings.Join(violations, ", "),
		)
	}
	return nil
}

func normalizeProjectRLSPredicate(value string) string {
	normalized := strings.ToLower(value)
	for _, removable := range []string{
		"::character varying",
		"::text",
		"::bigint",
		" ",
		"\n",
		"\r",
		"\t",
		"(",
		")",
		`"`,
	} {
		normalized = strings.ReplaceAll(normalized, removable, "")
	}
	return normalized
}

type postgresProjectRLSRole struct {
	RoleName        string `gorm:"column:role_name"`
	SessionRoleName string `gorm:"column:session_role_name"`
	CanLogin        bool   `gorm:"column:can_login"`
	Superuser       bool   `gorm:"column:superuser"`
	BypassRLS       bool   `gorm:"column:bypass_rls"`
}

func validatePostgresProjectRLSRole(db *gorm.DB) error {
	var role postgresProjectRLSRole
	result := db.Raw(`
		SELECT
			effective_role.rolname AS role_name,
			session_identity.rolname AS session_role_name,
			effective_role.rolcanlogin AS can_login,
			effective_role.rolsuper AS superuser,
			effective_role.rolbypassrls AS bypass_rls
		FROM pg_roles AS effective_role
		JOIN pg_roles AS session_identity
		  ON session_identity.rolname = SESSION_USER
		WHERE effective_role.rolname = CURRENT_USER
	`).Scan(&role)
	if result.Error != nil {
		return fmt.Errorf("read PostgreSQL runtime role: %w", result.Error)
	}
	if result.RowsAffected != 1 || role.RoleName == "" {
		return errors.New("PostgreSQL runtime role could not be resolved")
	}
	if role.SessionRoleName == "" || role.SessionRoleName != role.RoleName {
		return fmt.Errorf(
			"PostgreSQL SESSION_USER %q must equal CURRENT_USER %q; SET ROLE sessions can reset into an RLS-bypass identity",
			role.SessionRoleName,
			role.RoleName,
		)
	}

	var violations []string
	if !role.CanLogin {
		violations = append(violations, "NOLOGIN")
	}
	if role.Superuser {
		violations = append(violations, "SUPERUSER")
	}
	if role.BypassRLS {
		violations = append(violations, "BYPASSRLS")
	}

	var ownerRoles []string
	if err := db.Raw(`
		SELECT DISTINCT owner.rolname
		FROM pg_class AS table_relation
		JOIN pg_namespace AS namespace
		  ON namespace.oid = table_relation.relnamespace
		JOIN pg_roles AS owner ON owner.oid = table_relation.relowner
		WHERE namespace.nspname = CURRENT_SCHEMA()
		  AND table_relation.relname IN ?
		  AND pg_has_role(CURRENT_USER, owner.oid, 'MEMBER')
		ORDER BY owner.rolname
	`, ProjectRLSProtectedTables()).Scan(&ownerRoles).Error; err != nil {
		return fmt.Errorf("read PostgreSQL project table ownership: %w", err)
	}
	if len(ownerRoles) > 0 {
		sort.Strings(ownerRoles)
		violations = append(
			violations,
			"owner or member of owner role "+strings.Join(ownerRoles, ", "),
		)
	}
	if len(violations) > 0 {
		return fmt.Errorf(
			"PostgreSQL runtime role %q can bypass project RLS: %s",
			role.RoleName,
			strings.Join(violations, "; "),
		)
	}
	return nil
}
