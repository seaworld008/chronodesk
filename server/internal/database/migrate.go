package database

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/auth"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/version"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ValidateRuntimeSchema verifies additive migrations that the running binary
// cannot safely operate without. AUTO_MIGRATE may be disabled in production,
// but the process must fail fast instead of starting schedulers that repeatedly
// fail against an older schema.
func ValidateRuntimeSchema(db *gorm.DB) error {
	return validateRuntimeSchema(db, true)
}

func validateRuntimeSchema(
	db *gorm.DB,
	requireWebhookCredentialFoundation bool,
) error {
	if db == nil {
		return fmt.Errorf("database is required")
	}

	requirements := runtimeSchemaRequirements()
	if db.Dialector.Name() == "postgres" {
		if err := validatePostgresRuntimeSchema(db, requirements); err != nil {
			return err
		}
		if err := ValidateAutomationWebhookPaginationIndexes(db); err != nil {
			return err
		}
		if err := ValidatePolicyDecisionEpochContract(db); err != nil {
			return err
		}
		if err := ValidateAdminAuditExportContract(db); err != nil {
			return err
		}
		if err := ValidateAttachmentStorageIdentityContract(db); err != nil {
			return err
		}
		if err := ValidateKnowledgeObjectIdentityContract(db); err != nil {
			return err
		}
		if err := ValidateKnowledgeObjectWriteIntentContract(db); err != nil {
			return err
		}
		if err := ValidateKnowledgePublishedVersionContract(db); err != nil {
			return err
		}
		if err := validatePostgresLoginHistoryMethodContract(db); err != nil {
			return err
		}
		if err := validatePostgresA2AIdentifierContract(db); err != nil {
			return err
		}
		if err := ValidateIdempotencyScopeIndex(db); err != nil {
			return err
		}
		if err := ValidateProjectScopeCutoverMarker(db); err != nil {
			return err
		}
		if err := ValidatePlatformRoleCutover(db); err != nil {
			return err
		}
		if err := ValidateCategoryScopeContract(db); err != nil {
			return err
		}
		if requireWebhookCredentialFoundation {
			if err := validateWebhookCredentialLifetimeCatalog(db); err != nil {
				return err
			}
		}
		if err := ValidateWebhookOutboxLifecycleFence(db); err != nil {
			return err
		}
		if err := ValidateWebhookOutboxLifecycleIndexes(db); err != nil {
			return err
		}
		return ValidateProjectRLSReadiness(db)
	}

	var missing []string
	for _, required := range requirements {
		if !db.Migrator().HasTable(required.model) {
			missing = append(missing, required.table)
			continue
		}
		for _, column := range required.columns {
			if !db.Migrator().HasColumn(required.model, column) {
				missing = append(missing, required.table+"."+column)
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf(
			"required schema objects are missing: %s; run `go run ./cmd/migrate` before starting the server",
			strings.Join(missing, ", "),
		)
	}
	if err := ValidateAutomationWebhookPaginationIndexes(db); err != nil {
		return err
	}
	if err := ValidatePolicyDecisionEpochContract(db); err != nil {
		return err
	}
	if err := ValidateAttachmentStorageIdentityContract(db); err != nil {
		return err
	}
	if err := ValidateKnowledgeObjectIdentityContract(db); err != nil {
		return err
	}
	if err := ValidateKnowledgeObjectWriteIntentContract(db); err != nil {
		return err
	}
	if err := ValidateKnowledgePublishedVersionContract(db); err != nil {
		return err
	}
	if err := ValidateIdempotencyScopeIndex(db); err != nil {
		return err
	}
	if err := ValidateProjectScopeCutoverMarker(db); err != nil {
		return err
	}
	if err := ValidatePlatformRoleCutover(db); err != nil {
		return err
	}
	if err := ValidateCategoryScopeContract(db); err != nil {
		return err
	}
	if requireWebhookCredentialFoundation {
		if err := validateWebhookCredentialLifetimeCatalog(db); err != nil {
			return err
		}
	}
	if err := ValidateWebhookOutboxLifecycleFence(db); err != nil {
		return err
	}
	if err := ValidateWebhookOutboxLifecycleIndexes(db); err != nil {
		return err
	}
	return ValidateProjectRLSReadiness(db)
}

type runtimeSchemaColumn struct {
	TableName  string `gorm:"column:table_name"`
	ColumnName string `gorm:"column:column_name"`
}

// validatePostgresRuntimeSchema reads all required metadata in one query.
// Cloud PostgreSQL connections can add hundreds of milliseconds per network
// round trip; issuing HasTable/HasColumn for every field turned a fail-fast
// safety gate into a 40+ second cold start.
func validatePostgresRuntimeSchema(
	db *gorm.DB,
	requirements []runtimeSchemaRequirement,
) error {
	tableNames := make([]string, 0, len(requirements))
	seenTables := make(map[string]struct{}, len(requirements))
	for _, required := range requirements {
		if _, exists := seenTables[required.table]; exists {
			continue
		}
		seenTables[required.table] = struct{}{}
		tableNames = append(tableNames, required.table)
	}

	var rows []runtimeSchemaColumn
	if err := db.Raw(`
		SELECT table_name, column_name
		FROM information_schema.columns
		WHERE table_schema = CURRENT_SCHEMA()
		  AND table_name IN ?
	`, tableNames).Scan(&rows).Error; err != nil {
		return fmt.Errorf("read PostgreSQL runtime schema metadata: %w", err)
	}
	present := make(map[string]map[string]struct{}, len(tableNames))
	for _, row := range rows {
		if present[row.TableName] == nil {
			present[row.TableName] = make(map[string]struct{})
		}
		present[row.TableName][row.ColumnName] = struct{}{}
	}

	var missing []string
	for _, required := range requirements {
		columns, tableExists := present[required.table]
		if !tableExists {
			missing = append(missing, required.table)
			continue
		}
		for _, column := range required.columns {
			if _, exists := columns[column]; !exists {
				missing = append(missing, required.table+"."+column)
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf(
			"required schema objects are missing: %s; run `go run ./cmd/migrate` before starting the server",
			strings.Join(missing, ", "),
		)
	}
	return nil
}

type runtimeSchemaRequirement struct {
	model   any
	table   string
	columns []string
}

// runtimeSchemaRequirements covers every table/column needed before Agent,
// Outbox, lease, A2A and immediately-revocable human sessions are allowed to
// start background work. It intentionally checks the deployed schema rather
// than trusting migration history.
func runtimeSchemaRequirements() []runtimeSchemaRequirement {
	return []runtimeSchemaRequirement{
		{&models.SchemaMigrationCheckpoint{}, "schema_migration_checkpoints", []string{
			"key", "version", "checksum", "completed_at",
		}},
		{&models.User{}, "users", []string{
			"id", "platform_role", "status", "password_hash", "password_reset_at",
			"two_factor_enabled", "two_factor_secret", "backup_codes",
			"welcome_email_delivered_at",
		}},
		{&models.AdminAuditLog{}, "admin_audit_logs", []string{
			"id", "actor_type", "actor_id", "user_id", "platform_role",
			"action", "status_code",
			"action_code", "resource_type", "resource_public_id", "request_id",
			"trace_id", "correlation_id",
		}},
		{&models.AdminAuditExportJob{}, "admin_audit_export_jobs", []string{
			"id", "public_id", "requester_user_id", "requester_role",
			"filter_snapshot", "filter_hash", "start_time", "end_time",
			"anchor_created_at", "anchor_id", "state", "requested_at",
			"lease_owner", "lease_expires_at", "fencing_token", "attempt",
			"row_count", "truncated", "object_key", "sha256", "size_bytes",
			"failure_code", "expires_at",
		}},
		{&models.UserProfile{}, "user_profiles", []string{"user_id"}},
		{&models.EmailConfig{}, "email_configs", []string{
			"email_verification_enabled", "smtp_host", "smtp_port",
			"smtp_username", "smtp_password", "from_email", "is_active",
		}},
		{&auth.RefreshToken{}, "refresh_tokens", []string{
			"user_id", "session_id", "expires_at", "revoked", "rotated_at",
			"replaced_by_token",
		}},
		{&auth.EmailVerification{}, "email_verifications", []string{
			"user_id", "token", "delivery_secret", "email_delivered_at",
			"used", "expires_at", "used_at",
		}},
		{&auth.PasswordReset{}, "password_resets", []string{
			"user_id", "token", "delivery_secret", "email_delivered_at",
			"used", "expires_at", "used_at",
		}},
		{&auth.OTPCode{}, "otp_codes", []string{"user_id", "code", "type", "expires_at", "used", "used_at"}},
		{&auth.AuthenticationSecurityAuditEvent{}, "authentication_security_audit_events", []string{
			"id", "user_id", "event_type", "source", "request_id",
			"trace_id", "correlation_id", "created_at",
		}},
		{&models.LoginHistory{}, "login_histories", []string{"user_id", "session_id", "is_active"}},
		{&models.ServicePrincipal{}, "service_principals", []string{"id", "status", "scopes", "read_only", "emergency_disabled", "expires_at", "policy_epoch"}},
		{&models.AgentCredential{}, "agent_credentials", []string{"service_principal_id", "secret_hash", "status", "expires_at", "revoked_at"}},
		{&models.AgentPolicy{}, "agent_policies", []string{"service_principal_id", "effect", "scope", "action", "conditions", "is_active"}},
		{&models.PolicyDecision{}, "policy_decisions", []string{"actor_type", "actor_id", "credential_id", "allowed", "reason_code", "policy_epoch", "request_digest"}},
		{&models.IdempotencyRecord{}, "idempotency_records", []string{
			"organization_id", "project_id", "actor_type", "actor_id",
			"operation", "key", "request_hash", "state",
			"resource_snapshot", "expires_at", "completion_ttl_nanoseconds", "completed_at",
		}},
		{&models.Category{}, "categories", []string{
			"id", "organization_id", "project_id", "slug", "parent_id",
		}},
		{&models.ProjectMembership{}, "project_memberships", []string{
			"id", "project_id", "user_id", "role", "is_active",
			"knowledge_contributor", "version",
		}},
		{&models.Ticket{}, "tickets", []string{
			"id", "public_id", "organization_id", "project_id", "queue_id",
			"request_type_version_id", "workflow_version_id",
			"version", "agent_context", "trust_level", "created_by_actor_type",
			"created_by_actor_id", "assigned_to_actor_type", "assigned_to_actor_id",
		}},
		{&models.TicketComment{}, "ticket_comments", []string{"ticket_id", "actor_type", "actor_id", "service_principal_id", "type"}},
		{&models.TicketAttachment{}, "ticket_attachments", []string{
			"ticket_id", "actor_type", "actor_id", "service_principal_id",
			"storage_path", "storage_store_id", "storage_version_id",
			"hash", "virus_scan",
		}},
		{&models.KnowledgeArticleVersion{}, "knowledge_article_versions", []string{
			"id", "organization_id", "project_id", "article_id",
			"object_provider", "object_key", "object_store_id",
			"object_version_id", "content_hash",
		}},
		{&models.KnowledgeObjectWriteIntent{}, "knowledge_object_write_intents", []string{
			"id", "organization_id", "project_id", "article_id", "version_id",
			"object_provider", "object_store_id", "object_key",
			"object_version_id", "size_bytes", "content_hash",
			"receipt_recorded", "next_attempt_at", "lease_owner",
			"lease_expires_at", "fencing_token", "attempts", "failure_code",
		}},
		{&models.TicketHistory{}, "ticket_histories", []string{
			"ticket_id", "actor_type", "actor_id", "service_principal_id",
			"event_id", "resource_version", "provenance", "action", "details",
		}},
		{&models.TicketLease{}, "ticket_leases", []string{"ticket_id", "holder_actor_type", "holder_actor_id", "ticket_version", "expires_at", "released_at"}},
		{&models.EntityLink{}, "entity_links", []string{
			"id", "organization_id", "project_id", "ticket_id", "kind",
			"reference_id", "created_by_type", "created_by_id",
		}},
		{&models.TicketRelation{}, "ticket_relations", []string{
			"id", "organization_id", "project_id", "source_ticket_id",
			"target_ticket_id", "relation", "created_by_type", "created_by_id",
		}},
		{&models.DomainEvent{}, "domain_events", []string{
			"spec_version", "source", "type", "subject", "time", "data",
			"actor_type", "actor_id", "resource_version", "published_at",
		}},
		{&models.OutboxDelivery{}, "outbox_deliveries", []string{
			"event_id", "destination_type", "destination_id", "status",
			"attempts", "next_attempt_at", "locked_at", "locked_by",
			"lock_token", "expires_at", "expired_at",
		}},
		{&models.WebhookDeliverySnapshot{}, "webhook_delivery_snapshots", []string{
			"id", "organization_id", "project_id", "config_id", "event_id",
			"credential_expires_at", "credential_shredded_at",
			"credential_shred_reason",
		}},
		{&models.AuditChainHead{}, "audit_chain_heads", []string{
			"organization_id", "project_id", "last_sequence", "last_hash",
			"last_entry_id",
		}},
		{&models.AuditLedgerEntry{}, "audit_ledger_entries", []string{
			"organization_id", "project_id", "sequence", "previous_hash",
			"entry_hash", "payload_digest", "domain_event_id",
		}},
		{&models.AgentTask{}, "agent_tasks", []string{
			"context_id", "linked_ticket_id", "owner_actor_type", "owner_actor_id",
			"owner_credential_id", "state", "version", "execution_claim_id", "execution_expires_at",
		}},
		{&models.AgentMessage{}, "agent_messages", []string{"task_id", "context_id", "sequence", "request_digest", "payload"}},
		{&models.AgentArtifact{}, "agent_artifacts", []string{"task_id", "sequence", "payload"}},
		{&models.AgentTaskStatusHistory{}, "agent_task_status_history", []string{"task_id", "sequence", "state", "status"}},
		{&models.AgentTaskEvent{}, "agent_task_events", []string{"task_id", "context_id", "resource_version", "payload"}},
		{&models.AgentPushNotificationConfig{}, "agent_push_notification_configs", []string{"task_id", "url", "token", "authentication"}},
		{&models.A2APushDeliverySnapshot{}, "a2a_push_delivery_snapshots", []string{
			"event_id", "task_id", "push_config_id", "config_version_at",
			"callback_url", "token_ciphertext", "authentication_ciphertext",
			"request_body", "content_type", "protocol_version",
		}},
		{&models.Notification{}, "notifications", []string{"recipient_id", "type", "channel", "related_ticket_id", "source_event_key"}},
	}
}

func autoMigrateFromModel(db *gorm.DB, firstModel int) error {
	log.Println("Starting database migration...")

	migrationModels := schemaMigrationModels()
	if err := validateMigrationResumePoint(firstModel, len(migrationModels)); err != nil {
		return err
	}
	// GORM follows model associations while migrating. Running AutoMigrate once
	// per model therefore re-scans most of the schema dozens of times. A single
	// PostgreSQL transaction lets GORM order the selected dependency graph once
	// and makes the destructive v2 upgrade atomic.
	startedAt := time.Now()
	selectedModels := migrationModels[firstModel-1:]
	if err := migrateModelBatch(db, selectedModels); err != nil {
		return fmt.Errorf("failed to migrate model batch from %d: %w", firstModel, err)
	}
	log.Printf(
		"Migrated %d/%d models in one atomic batch in %s",
		len(selectedModels),
		len(migrationModels),
		time.Since(startedAt).Round(time.Millisecond),
	)

	log.Println("Database migration completed successfully")
	return nil
}

func validateMigrationResumePoint(firstModel, modelCount int) error {
	if firstModel < 1 || firstModel > modelCount+1 {
		return fmt.Errorf(
			"first migration model must be between 1 and %d",
			modelCount+1,
		)
	}
	return nil
}

// migrateLegacyHumanRoles upgrades the two historical human-role aliases
// before AutoMigrate installs the closed role constraint. It intentionally
// runs even for resumed migrations because an operator may resume after the
// user model while still pointing at legacy data.
func migrateLegacyHumanRoles(db *gorm.DB) error {
	if !db.Migrator().HasTable(&models.User{}) {
		return nil
	}
	hasLegacyRole, err := hasExactDatabaseColumn(db, "users", "role")
	if err != nil {
		return err
	}
	if !hasLegacyRole {
		return nil
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var unsupported []string
		if err := tx.Table("users").
			Distinct("role").
			Where("role IS NULL OR role NOT IN ?", []string{
				"admin",
				"supervisor",
				"agent",
				"customer",
				"user",
				"superuser",
			}).
			Order("role ASC").
			Pluck("role", &unsupported).Error; err != nil {
			return fmt.Errorf("inspect legacy human roles: %w", err)
		}
		if len(unsupported) > 0 {
			return fmt.Errorf(
				"unsupported legacy human role(s): %s",
				strings.Join(unsupported, ", "),
			)
		}
		if err := tx.Table("users").
			Where("role = ?", "user").
			Update("role", "customer").Error; err != nil {
			return fmt.Errorf("migrate legacy customer role: %w", err)
		}
		if err := tx.Table("users").
			Where("role = ?", "superuser").
			Update("role", "admin").Error; err != nil {
			return fmt.Errorf("migrate legacy administrator role: %w", err)
		}
		return nil
	})
}

// migrateOneModel isolates an upstream GORM PostgreSQL migrator defect where a
// context deadline can leave ColumnTypes metadata nil and trigger a panic.
// Operator tooling must return a resumable error instead of terminating with a
// stack trace after the database connection has already timed out.
func migrateOneModel(db *gorm.DB, model any) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("migration driver panic: %v", recovered)
		}
	}()
	// Models such as User expose associations that reach most of the schema.
	// Letting every resumable step recursively migrate those relationships
	// repeats the full catalog scan once per model. The isolated GORM session
	// creates the model's own table, columns, checks and indexes; one bounded
	// final pass below installs cross-model foreign keys after all tables exist.
	return db.Transaction(func(tx *gorm.DB) error {
		tableOnly := tx.Session(&gorm.Session{NewDB: true})
		tableOnly.Config.IgnoreRelationshipsWhenMigrating = true
		return tableOnly.AutoMigrate(model)
	})
}

func migrateModelBatch(
	db *gorm.DB,
	models []any,
) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("migration driver panic: %v", recovered)
		}
	}()
	return db.Transaction(func(tx *gorm.DB) error {
		return tx.AutoMigrate(models...)
	})
}

func schemaMigrationModels() []any {
	return []any{
		&models.User{},
		&models.UserProfile{},
		&auth.RefreshToken{},
		&auth.LoginAttempt{},
		&auth.EmailVerification{},
		&auth.PasswordReset{},
		&models.Category{},
		&models.EmailConfig{},
		&models.ServicePrincipal{},
		&models.AgentCredential{},
		&models.AgentPolicy{},
		&models.PolicyDecision{},
		&models.IdempotencyRecord{},
		&models.Ticket{},
		&models.TicketComment{},
		&models.TicketAttachment{},
		&models.TicketHistory{},
		&models.TicketTag{},
		&models.TicketLease{},
		&models.EntityLink{},
		&models.TicketRelation{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.AgentTask{},
		&models.AgentMessage{},
		&models.AgentArtifact{},
		&models.AgentTaskStatusHistory{},
		&models.AgentTaskEvent{},
		&models.AgentPushNotificationConfig{},
		&auth.OTPCode{},
		&models.OTPTrustedDevice{},
		&models.NotificationPreference{},
		&models.Notification{},
		&models.WebhookConfig{},
		&models.WebhookDeliverySnapshot{},
		&models.WebhookLog{},
		&models.EmailLog{},
		&models.LoginHistory{},
		&models.SystemConfig{},
		&models.CleanupLog{},
		// FE008 自动化相关模型
		&models.AutomationRule{},
		&models.SLAConfig{},
		&models.TicketTemplate{},
		&models.AutomationLog{},
		&models.QuickReply{},
		&models.AdminAuditLog{},
		// Project-scope foundations are appended to preserve the one-based
		// resume positions of every pre-project migration model.
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.Team{},
		&models.Queue{},
		&models.ProjectMembership{},
		&models.TeamMembership{},
		&models.ProjectPrincipalGrant{},
		// Project configuration, integration and AI collaboration are all
		// project-owned. They intentionally have no unscoped legacy tables.
		&models.RequestTypeVersion{},
		&models.WorkflowVersion{},
		&models.ConfigurationRelease{},
		&models.ProjectSolutionInstallation{},
		&models.ConnectorDefinition{},
		&models.Connection{},
		&models.MappingVersion{},
		&models.InboxMessage{},
		&models.InboxReceipt{},
		&models.ExternalLink{},
		&models.SyncCursor{},
		&models.SyncRun{},
		&models.IntegrationConflict{},
		&models.DeadLetter{},
		&models.AgentRun{},
		&models.ActionProposal{},
		&models.ApprovalTask{},
		&models.ApprovalDecision{},
		&models.Handoff{},
		&models.EvidenceReference{},
		&models.KnowledgeArticle{},
		&models.KnowledgeArticleVersion{},
		&models.KnowledgeArticleACL{},
		&models.KnowledgeIngestionTask{},
		&models.KnowledgeChunk{},
		&models.KnowledgeCitation{},
		&models.KnowledgeFeedback{},
		&models.KnowledgeIndexState{},
		&models.ProjectModelPolicy{},
		&models.AuditChainHead{},
		&models.AuditLedgerEntry{},
		// One-time destructive backfills use committed markers so a later
		// structural migration cannot reinterpret live multi-project data.
		&models.SchemaMigrationCheckpoint{},
		// New models append here to preserve every existing one-based resume
		// position used by controlled production migrations.
		&models.A2APushDeliverySnapshot{},
		&models.AdminAuditExportJob{},
		&models.KnowledgeSourceLink{},
		&models.KnowledgeObjectWriteIntent{},
		&auth.AuthenticationSecurityAuditEvent{},
	}
}

// CreateIndexes 创建额外的索引
func CreateIndexes(db *gorm.DB) error {
	log.Println("Creating additional indexes...")

	// 用户表索引
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);",
		"CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);",
		"CREATE INDEX IF NOT EXISTS idx_users_platform_role ON users(platform_role);",
		"CREATE INDEX IF NOT EXISTS idx_users_status ON users(status);",
		"CREATE INDEX IF NOT EXISTS idx_users_department ON users(department);",
		"CREATE INDEX IF NOT EXISTS idx_users_last_login_at ON users(last_login_at);",

		// 分类表索引
		"CREATE INDEX IF NOT EXISTS idx_categories_scope_parent ON categories(organization_id, project_id, parent_id);",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_categories_project_slug ON categories(organization_id, project_id, slug);",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_categories_scope_id ON categories(organization_id, project_id, id);",
		"CREATE INDEX IF NOT EXISTS idx_categories_scope_status_sort ON categories(organization_id, project_id, status, sort_order, id);",
		"CREATE INDEX IF NOT EXISTS idx_categories_scope_type ON categories(organization_id, project_id, type, id);",

		// 工单表索引
		"CREATE INDEX IF NOT EXISTS idx_tickets_ticket_number ON tickets(ticket_number);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_status ON tickets(status);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_priority ON tickets(priority);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_type ON tickets(type);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_source ON tickets(source);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_category_id ON tickets(category_id);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_created_by ON tickets(created_by_id);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_assigned_to ON tickets(assigned_to_id);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_due_at ON tickets(due_date);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_resolved_at ON tickets(resolved_at);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_closed_at ON tickets(closed_at);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_status_priority ON tickets(status, priority);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_created_at ON tickets(created_at);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_scope_due_id ON tickets(organization_id, project_id, due_date, id);",
		"DROP INDEX IF EXISTS idx_tickets_scope_sla_status_created_id;",
		"DROP INDEX IF EXISTS idx_tickets_scope_active_sla_created_id;",
		"CREATE INDEX IF NOT EXISTS idx_tickets_scope_sla_created_id ON tickets(organization_id, project_id, sla_breached, created_at, id);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_version ON tickets(id, version);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_creator_actor ON tickets(created_by_actor_type, created_by_actor_id);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_assignee_actor ON tickets(assigned_to_actor_type, assigned_to_actor_id);",

		// 工单评论表索引
		"CREATE INDEX IF NOT EXISTS idx_ticket_comments_ticket_id ON ticket_comments(ticket_id);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_comments_user_id ON ticket_comments(user_id);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_comments_type ON ticket_comments(type);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_comments_parent_id ON ticket_comments(parent_id);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_comments_created_at ON ticket_comments(created_at);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_comments_scope_ticket_parent_created ON ticket_comments(organization_id, project_id, ticket_id, parent_id, created_at, id);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_comments_scope_ticket_timeline ON ticket_comments(organization_id, project_id, ticket_id, created_at DESC, id DESC) WHERE is_deleted = false;",

		// 工单历史表索引
		"CREATE INDEX IF NOT EXISTS idx_ticket_histories_ticket_id ON ticket_histories(ticket_id);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_histories_user_id ON ticket_histories(user_id);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_histories_action ON ticket_histories(action);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_histories_created_at ON ticket_histories(created_at);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_histories_scope_ticket_created ON ticket_histories(organization_id, project_id, ticket_id, created_at, id);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_attachments_scope_ticket_created ON ticket_attachments(organization_id, project_id, ticket_id, created_at, id);",
		"CREATE INDEX IF NOT EXISTS idx_notifications_scope_recipient_created ON notifications(organization_id, project_id, recipient_id, created_at, id);",

		// Agent-native identity, policy, event and lease indexes
		"CREATE INDEX IF NOT EXISTS idx_service_principals_controls ON service_principals(status, emergency_disabled, expires_at);",
		"CREATE INDEX IF NOT EXISTS idx_agent_credentials_lookup ON agent_credentials(service_principal_id, status, expires_at);",
		"CREATE INDEX IF NOT EXISTS idx_agent_policies_lookup ON agent_policies(service_principal_id, scope, is_active, priority DESC);",
		"CREATE INDEX IF NOT EXISTS idx_policy_decisions_actor_time ON policy_decisions(actor_type, actor_id, created_at DESC);",
		"CREATE INDEX IF NOT EXISTS idx_domain_events_stream ON domain_events(type, created_at, id);",
		"CREATE INDEX IF NOT EXISTS idx_outbox_pending ON outbox_deliveries(status, next_attempt_at);",
		"CREATE INDEX IF NOT EXISTS idx_idempotency_expiry ON idempotency_records(expires_at);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_leases_expiry ON ticket_leases(expires_at, released_at);",
		"CREATE INDEX IF NOT EXISTS idx_entity_links_project_ticket ON entity_links(organization_id, project_id, ticket_id, created_at);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_relations_project_source ON ticket_relations(organization_id, project_id, source_ticket_id, created_at);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_relations_project_target ON ticket_relations(organization_id, project_id, target_ticket_id, created_at);",
		"CREATE INDEX IF NOT EXISTS idx_entity_links_scope_ticket_timeline ON entity_links(organization_id, project_id, ticket_id, created_at DESC, id DESC);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_relations_scope_source_timeline ON ticket_relations(organization_id, project_id, source_ticket_id, created_at DESC, id DESC);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_relations_scope_target_timeline ON ticket_relations(organization_id, project_id, target_ticket_id, created_at DESC, id DESC);",
		"CREATE INDEX IF NOT EXISTS idx_agent_tasks_context_state ON agent_tasks(context_id, state, updated_at DESC);",
		"CREATE INDEX IF NOT EXISTS idx_agent_task_events_context_cursor ON agent_task_events(context_id, id);",
		"CREATE INDEX IF NOT EXISTS idx_agent_push_task ON agent_push_notification_configs(task_id);",

		// Project scope, membership and routing indexes
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_scope_id ON projects(organization_id, id);",
		"CREATE INDEX IF NOT EXISTS idx_business_units_organization_status ON business_units(organization_id, status);",
		"CREATE INDEX IF NOT EXISTS idx_projects_organization_status ON projects(organization_id, status);",
		"CREATE INDEX IF NOT EXISTS idx_projects_business_unit_status ON projects(business_unit_id, status);",
		"CREATE INDEX IF NOT EXISTS idx_project_memberships_user_active ON project_memberships(user_id, is_active);",
		"CREATE INDEX IF NOT EXISTS idx_project_memberships_project_role_active ON project_memberships(project_id, role, is_active);",
		"CREATE INDEX IF NOT EXISTS idx_project_memberships_directory ON project_memberships(project_id, is_active DESC, role, user_id, id);",
		"CREATE INDEX IF NOT EXISTS idx_teams_project_status ON teams(project_id, status);",
		"CREATE INDEX IF NOT EXISTS idx_team_memberships_user_active ON team_memberships(user_id, is_active);",
		"CREATE INDEX IF NOT EXISTS idx_queues_project_status ON queues(project_id, status);",
		"CREATE INDEX IF NOT EXISTS idx_queues_directory ON queues(project_id, status, is_default DESC, name, id);",
		"CREATE INDEX IF NOT EXISTS idx_queues_team_status ON queues(team_id, status);",
		"CREATE INDEX IF NOT EXISTS idx_project_principal_grants_principal_active ON project_principal_grants(service_principal_id, is_active);",
		"CREATE INDEX IF NOT EXISTS idx_project_principal_grants_project_active ON project_principal_grants(project_id, is_active);",

		// OTP表索引
		"CREATE INDEX IF NOT EXISTS idx_otp_codes_user_id ON otp_codes(user_id);",
		"CREATE INDEX IF NOT EXISTS idx_otp_codes_code ON otp_codes(code);",
		"CREATE INDEX IF NOT EXISTS idx_otp_codes_expires_at ON otp_codes(expires_at);",
		"CREATE INDEX IF NOT EXISTS idx_otp_codes_type ON otp_codes(type);",
		"CREATE INDEX IF NOT EXISTS idx_otp_trusted_devices_directory ON otp_trusted_devices(user_id, revoked, expires_at DESC, id DESC);",

		// 刷新令牌表索引
		"CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_id ON refresh_tokens(user_id);",
		"CREATE INDEX IF NOT EXISTS idx_refresh_tokens_token ON refresh_tokens(token);",
		"CREATE INDEX IF NOT EXISTS idx_refresh_tokens_expires_at ON refresh_tokens(expires_at);",
		"CREATE INDEX IF NOT EXISTS idx_refresh_tokens_revoked ON refresh_tokens(revoked);",
		"CREATE INDEX IF NOT EXISTS idx_refresh_tokens_session_active ON refresh_tokens(user_id, session_id, revoked, expires_at);",

		// 登录尝试表索引
		"CREATE INDEX IF NOT EXISTS idx_login_attempts_email ON login_attempts(email);",
		"CREATE INDEX IF NOT EXISTS idx_login_attempts_ip_address ON login_attempts(ip_address);",
		"CREATE INDEX IF NOT EXISTS idx_login_attempts_created_at ON login_attempts(created_at);",
		"CREATE INDEX IF NOT EXISTS idx_login_attempts_success ON login_attempts(success);",

		// Webhook配置表索引
		"CREATE INDEX IF NOT EXISTS idx_webhook_configs_provider ON webhook_configs(provider);",
		"CREATE INDEX IF NOT EXISTS idx_webhook_configs_status ON webhook_configs(status);",
		"CREATE INDEX IF NOT EXISTS idx_webhook_configs_created_by ON webhook_configs(created_by);",
		"CREATE INDEX IF NOT EXISTS idx_webhook_configs_created_at ON webhook_configs(created_at);",

		// Webhook日志表索引
		"CREATE INDEX IF NOT EXISTS idx_webhook_logs_config_id ON webhook_logs(config_id);",
		"CREATE INDEX IF NOT EXISTS idx_webhook_logs_event_type ON webhook_logs(event_type);",
		"CREATE INDEX IF NOT EXISTS idx_webhook_logs_resource_id ON webhook_logs(resource_id);",
		"CREATE INDEX IF NOT EXISTS idx_webhook_logs_resource_type ON webhook_logs(resource_type);",
		"CREATE INDEX IF NOT EXISTS idx_webhook_logs_status ON webhook_logs(status);",
		"CREATE INDEX IF NOT EXISTS idx_webhook_logs_created_at ON webhook_logs(created_at);",
		"CREATE INDEX IF NOT EXISTS idx_webhook_logs_trace_id ON webhook_logs(trace_id);",

		// 登录历史表索引
		"CREATE INDEX IF NOT EXISTS idx_login_histories_user_id ON login_histories(user_id);",
		"CREATE INDEX IF NOT EXISTS idx_login_histories_ip_address ON login_histories(ip_address);",
		"CREATE INDEX IF NOT EXISTS idx_login_histories_login_time ON login_histories(login_time);",
		"CREATE INDEX IF NOT EXISTS idx_login_histories_logout_time ON login_histories(logout_time);",
		"CREATE INDEX IF NOT EXISTS idx_login_histories_session_id ON login_histories(session_id);",
		"CREATE INDEX IF NOT EXISTS idx_login_histories_session_active ON login_histories(user_id, session_id, is_active);",
		"CREATE INDEX IF NOT EXISTS idx_login_histories_login_status ON login_histories(login_status);",
		"CREATE INDEX IF NOT EXISTS idx_login_histories_is_active ON login_histories(is_active);",
		"CREATE INDEX IF NOT EXISTS idx_login_histories_user_login ON login_histories(user_id, login_time);",
		"CREATE INDEX IF NOT EXISTS idx_login_histories_user_active ON login_histories(user_id, is_active);",
		"CREATE INDEX IF NOT EXISTS idx_webhook_logs_next_retry_at ON webhook_logs(next_retry_at);",

		// 系统配置表索引
		"CREATE INDEX IF NOT EXISTS idx_system_configs_key ON system_configs(key);",
		"CREATE INDEX IF NOT EXISTS idx_system_configs_category ON system_configs(category);",
		"CREATE INDEX IF NOT EXISTS idx_system_configs_group ON system_configs(\"group\");",
		"CREATE INDEX IF NOT EXISTS idx_system_configs_is_active ON system_configs(is_active);",
		"CREATE INDEX IF NOT EXISTS idx_system_configs_category_group ON system_configs(category, \"group\");",
		"CREATE INDEX IF NOT EXISTS idx_system_configs_directory ON system_configs(category, \"group\", key, id);",

		// 清理日志表索引
		"CREATE INDEX IF NOT EXISTS idx_cleanup_logs_task_type ON cleanup_logs(task_type);",
		"CREATE INDEX IF NOT EXISTS idx_cleanup_logs_status ON cleanup_logs(status);",
		"CREATE INDEX IF NOT EXISTS idx_cleanup_logs_start_time ON cleanup_logs(start_time);",
		"CREATE INDEX IF NOT EXISTS idx_cleanup_logs_trigger_type ON cleanup_logs(trigger_type);",
		"CREATE INDEX IF NOT EXISTS idx_cleanup_logs_trigger_by ON cleanup_logs(trigger_by);",
		"CREATE INDEX IF NOT EXISTS idx_cleanup_logs_task_status ON cleanup_logs(task_type, status);",
		"CREATE INDEX IF NOT EXISTS idx_cleanup_logs_directory ON cleanup_logs(created_at DESC, id DESC);",
		"CREATE INDEX IF NOT EXISTS idx_cleanup_logs_task_directory ON cleanup_logs(task_type, created_at DESC, id DESC);",
	}

	var indexErrors []error
	for _, indexSQL := range indexes {
		if err := db.Exec(indexSQL).Error; err != nil {
			indexErrors = append(indexErrors, fmt.Errorf("%s: %w", indexSQL, err))
		}
	}
	if err := MigrateAutomationWebhookPaginationIndexes(db); err != nil {
		indexErrors = append(
			indexErrors,
			fmt.Errorf("automation and webhook pagination indexes: %w", err),
		)
	}
	if err := MigrateWebhookOutboxLifecycleFence(db); err != nil {
		indexErrors = append(
			indexErrors,
			fmt.Errorf("webhook Outbox lifecycle fence: %w", err),
		)
	}
	if err := MigrateWebhookOutboxLifecycleIndexes(db); err != nil {
		indexErrors = append(
			indexErrors,
			fmt.Errorf("webhook Outbox lifecycle indexes: %w", err),
		)
	}
	if err := errors.Join(indexErrors...); err != nil {
		return fmt.Errorf("create required indexes: %w", err)
	}

	log.Println("Additional indexes created successfully")
	return nil
}

// EnsureForeignKeyPolicies keeps optional historical references compatible
// with resource deletion. A notification retains its event snapshot after a
// ticket is removed, while PostgreSQL atomically clears the optional FK.
func EnsureForeignKeyPolicies(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	const notificationTicketPolicy = `
DO $$
BEGIN
	IF EXISTS (
		SELECT 1
		FROM information_schema.referential_constraints
		WHERE constraint_schema = CURRENT_SCHEMA()
		  AND constraint_name = 'notifications_related_ticket_id_fkey'
		  AND delete_rule <> 'SET NULL'
	) THEN
		ALTER TABLE notifications
			DROP CONSTRAINT notifications_related_ticket_id_fkey;
	END IF;
	IF NOT EXISTS (
		SELECT 1
		FROM information_schema.table_constraints
		WHERE constraint_schema = CURRENT_SCHEMA()
		  AND table_name = 'notifications'
		  AND constraint_name = 'notifications_related_ticket_id_fkey'
	) THEN
		ALTER TABLE notifications
			ADD CONSTRAINT notifications_related_ticket_id_fkey
			FOREIGN KEY (related_ticket_id)
			REFERENCES tickets(id)
			ON UPDATE CASCADE
			ON DELETE SET NULL;
	END IF;
END
$$;`
	if err := db.Exec(notificationTicketPolicy).Error; err != nil {
		return fmt.Errorf("ensure notification ticket foreign-key policy: %w", err)
	}
	return nil
}

// SeedOptions makes optional demonstration records an explicit operator
// decision. Schema migration never calls this path.
type SeedOptions struct {
	IncludeSampleData bool
	// EnsureInitialAdministratorMembership is supplied by the composition
	// root so the privileged project grant goes through the shared domain
	// service and writes its ActorRef, CloudEvent, Outbox and audit ledger
	// entry in this same seed transaction.
	EnsureInitialAdministratorMembership InitialAdministratorMembershipWriter
	// EnsureSampleUserMembership keeps optional demonstration project duties
	// explicit and audited instead of encoding them as platform roles.
	EnsureSampleUserMembership ProjectScopeMembershipWriter
}

type InitialAdministratorMembershipWriter func(
	context.Context,
	*gorm.DB,
	models.User,
	models.ProjectScope,
) error

// SeedData initializes the bootstrap administrator and default categories in a
// single transaction. A failure at any stage rolls back every seed mutation.
func SeedData(db *gorm.DB, options SeedOptions) error {
	if db == nil {
		return errors.New("seed database is required")
	}
	if options.EnsureInitialAdministratorMembership == nil {
		return errors.New(
			"audited initial administrator membership writer is required",
		)
	}
	if options.IncludeSampleData && options.EnsureSampleUserMembership == nil {
		return errors.New("audited sample user membership writer is required")
	}
	log.Println("Seeding initial data...")
	return db.Transaction(func(tx *gorm.DB) error {
		if err := seedInitialData(tx, options); err != nil {
			return err
		}
		log.Println("Initial data seeding completed")
		return nil
	})
}

func seedInitialData(db *gorm.DB, options SeedOptions) error {
	adminEmail := strings.TrimSpace(os.Getenv("ADMIN_EMAIL"))
	if adminEmail == "" {
		adminEmail = "admin@example.com"
	}
	const adminUsername = "admin"

	// Break-glass identity is stable and explicit. An unrelated platform
	// administrator must never be silently selected and expanded into a
	// default-project administrator.
	var adminUser models.User
	var bootstrapCandidates []models.User
	if err := db.Unscoped().
		Where("username = ? OR email = ?", adminUsername, adminEmail).
		Order("id ASC").
		Find(&bootstrapCandidates).Error; err != nil {
		return fmt.Errorf("failed to check controlled initial administrator: %w", err)
	}
	switch len(bootstrapCandidates) {
	case 0:
		adminPassword := os.Getenv("ADMIN_PASSWORD")
		if adminPassword == "" {
			return fmt.Errorf("ADMIN_PASSWORD is required when seeding the initial administrator")
		}
		passwordService, err := auth.NewSimplePasswordService(auth.PasswordServiceConfig{
			MinLength:  8,
			BcryptCost: auth.DefaultBcryptCost,
		})
		if err != nil {
			return fmt.Errorf("failed to initialize initial administrator password service: %w", err)
		}
		passwordHash, err := passwordService.HashPassword(adminPassword)
		if err != nil {
			return fmt.Errorf("failed to hash initial administrator password: %w", err)
		}
		adminUser = models.User{
			Username:      adminUsername,
			Email:         adminEmail,
			PasswordHash:  passwordHash,
			FirstName:     "System",
			LastName:      "Administrator",
			PlatformRole:  models.PlatformRolePlatformAdmin,
			Status:        models.UserStatusActive,
			EmailVerified: true,
			Department:    "IT",
			JobTitle:      "System Administrator",
		}

		if err := db.Create(&adminUser).Error; err != nil {
			return fmt.Errorf("failed to create admin user: %w", err)
		}
		log.Printf(
			"Created controlled initial administrator (username: %s, email: %s)",
			adminUsername,
			adminEmail,
		)
	case 1:
		adminUser = bootstrapCandidates[0]
		if adminUser.Username != adminUsername ||
			adminUser.Email != adminEmail ||
			adminUser.DeletedAt.Valid ||
			adminUser.PlatformRole != models.PlatformRolePlatformAdmin ||
			adminUser.Status != models.UserStatusActive {
			return errors.New(
				"controlled initial administrator identity exists but is not the active break-glass account",
			)
		}
	default:
		return errors.New(
			"controlled initial administrator username and email resolve to different retained identities",
		)
	}
	if err := seedInitialAdministratorMembership(
		db,
		adminUser,
		options.EnsureInitialAdministratorMembership,
	); err != nil {
		return err
	}
	categoryScope, err := defaultPlatformRoleCutoverScope(db)
	if err != nil {
		return fmt.Errorf(
			"resolve trusted default project for category seed: %w",
			err,
		)
	}

	// 检查是否已有默认分类
	var categoryCount int64
	if err := db.Model(&models.Category{}).
		Where(
			"organization_id = ? AND project_id = ?",
			categoryScope.OrganizationID,
			categoryScope.ProjectID,
		).
		Count(&categoryCount).Error; err != nil {
		return fmt.Errorf("failed to check default categories: %w", err)
	}

	if categoryCount == 0 {
		// 创建默认分类，使用管理员用户ID作为创建者
		defaultCategories := []*models.Category{
			{
				OrganizationID: categoryScope.OrganizationID,
				ProjectID:      categoryScope.ProjectID,
				Name:           "技术支持",
				Slug:           "technical-support",
				Description:    "技术相关问题和支持请求",
				Type:           models.CategoryTypeSupport,
				Status:         models.CategoryStatusActive,
				IsPublic:       true,
				SortOrder:      1,
				CreatedBy:      adminUser.ID,
			},
			{
				OrganizationID: categoryScope.OrganizationID,
				ProjectID:      categoryScope.ProjectID,
				Name:           "账户问题",
				Slug:           "account-issues",
				Description:    "账户相关问题和请求",
				Type:           models.CategoryTypeSupport,
				Status:         models.CategoryStatusActive,
				IsPublic:       true,
				SortOrder:      2,
				CreatedBy:      adminUser.ID,
			},
			{
				OrganizationID: categoryScope.OrganizationID,
				ProjectID:      categoryScope.ProjectID,
				Name:           "功能请求",
				Slug:           "feature-requests",
				Description:    "新功能请求和改进建议",
				Type:           models.CategoryTypeRequest,
				Status:         models.CategoryStatusActive,
				IsPublic:       true,
				SortOrder:      3,
				CreatedBy:      adminUser.ID,
			},
			{
				OrganizationID: categoryScope.OrganizationID,
				ProjectID:      categoryScope.ProjectID,
				Name:           "Bug报告",
				Slug:           "bug-reports",
				Description:    "系统错误和Bug报告",
				Type:           models.CategoryTypeIncident,
				Status:         models.CategoryStatusActive,
				IsPublic:       true,
				SortOrder:      4,
				CreatedBy:      adminUser.ID,
			},
		}

		for _, category := range defaultCategories {
			if err := db.Create(category).Error; err != nil {
				return fmt.Errorf("failed to create category %s: %w", category.Name, err)
			}
		}
		log.Println("Created default categories")
	}
	for _, defaultConfig := range models.DefaultSystemConfigs(version.Version) {
		result := db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key"}},
			DoNothing: true,
		}).Create(&defaultConfig)
		if result.Error != nil {
			return fmt.Errorf(
				"failed to create required system config %s: %w",
				defaultConfig.Key,
				result.Error,
			)
		}
	}
	var emailConfigCount int64
	if err := db.Model(&models.EmailConfig{}).Count(&emailConfigCount).Error; err != nil {
		return fmt.Errorf("failed to check default email configuration: %w", err)
	}
	if emailConfigCount == 0 {
		if err := db.Create(models.DefaultEmailConfig()).Error; err != nil {
			return fmt.Errorf("failed to create default email configuration: %w", err)
		}
	}

	if options.IncludeSampleData {
		if os.Getenv("ENVIRONMENT") != "development" {
			return errors.New(
				"sample data is only allowed when ENVIRONMENT=development",
			)
		}
		if err := generateSampleDataIfNeeded(
			db,
			options.EnsureSampleUserMembership,
		); err != nil {
			return err
		}
	}
	return nil
}

func seedInitialAdministratorMembership(
	db *gorm.DB,
	administrator models.User,
	writer InitialAdministratorMembershipWriter,
) error {
	if administrator.ID == 0 ||
		administrator.PlatformRole != models.PlatformRolePlatformAdmin ||
		administrator.Status != models.UserStatusActive {
		return errors.New("initial administrator identity is invalid")
	}
	if writer == nil {
		return errors.New(
			"audited initial administrator membership writer is required",
		)
	}

	var organization models.Organization
	if err := db.Where(
		"slug = ? AND status = ?",
		DefaultOrganizationSlug,
		models.OrganizationStatusActive,
	).First(&organization).Error; err != nil {
		return fmt.Errorf("load trusted default organization for initial administrator: %w", err)
	}
	var project models.Project
	if err := db.Where(
		"organization_id = ? AND key = ? AND status = ?",
		organization.ID,
		DefaultProjectKey,
		models.ProjectStatusActive,
	).First(&project).Error; err != nil {
		return fmt.Errorf("load trusted default project for initial administrator: %w", err)
	}

	seedContext := db.Statement.Context
	if seedContext == nil {
		seedContext = context.Background()
	}
	if err := writer(
		seedContext,
		db,
		administrator,
		project.Scope(),
	); err != nil {
		return fmt.Errorf(
			"ensure audited initial administrator default project membership: %w",
			err,
		)
	}
	return nil
}

// generateSampleDataIfNeeded generates optional demonstration records. The
// caller has already enforced the explicit development-only gate.
func generateSampleDataIfNeeded(
	db *gorm.DB,
	membershipWriter ProjectScopeMembershipWriter,
) error {
	// 检查是否已有示例数据
	var sampleTicketCount int64
	if err := db.Model(&models.Ticket{}).Where("title LIKE ?", "%示例%").Count(&sampleTicketCount).Error; err != nil {
		return fmt.Errorf("failed to check sample data: %w", err)
	}

	if sampleTicketCount > 0 {
		log.Printf("Sample data already exists (%d sample tickets), skipping generation", sampleTicketCount)
		return nil
	}

	// 生成示例数据
	generator := NewSampleDataGenerator(db, membershipWriter)
	if err := generator.GenerateAllSampleData(); err != nil {
		return fmt.Errorf("failed to generate sample data: %w", err)
	}

	log.Println("✅ Sample data generation completed successfully")
	return nil
}

// RunMigrations 只执行可重复的结构迁移。生产启动与普通 migrate 命令
// 绝不能隐式创建账号、分类或示例工单；种子数据必须由显式 seed 命令触发。
func RunMigrations(
	db *gorm.DB,
	membershipWriters ...ProjectScopeMembershipWriter,
) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return RunMigrationsContext(ctx, db, membershipWriters...)
}

// RunMigrationsContext is the bounded migration entry point used by operator
// tooling. A lost TLS connection or pooler response must never leave deployment
// automation blocked indefinitely.
func RunMigrationsContext(
	ctx context.Context,
	db *gorm.DB,
	membershipWriters ...ProjectScopeMembershipWriter,
) error {
	return RunMigrationsFromModel(ctx, db, 1, membershipWriters...)
}

// RunMigrationsFromModel resumes the model scan at a one-based index while
// still running all index and runtime-schema gates. It is intended for a
// bounded operator retry after a network timeout; normal callers start at 1.
func RunMigrationsFromModel(
	ctx context.Context,
	db *gorm.DB,
	firstModel int,
	membershipWriters ...ProjectScopeMembershipWriter,
) error {
	if ctx == nil {
		return errors.New("migration context is required")
	}
	if db == nil {
		return errors.New("migration database is required")
	}
	if db.Config == nil || db.Statement == nil {
		return errors.New("migration database is not initialized")
	}
	if err := validateMigrationResumePoint(
		firstModel,
		len(schemaMigrationModels()),
	); err != nil {
		return err
	}
	db = db.WithContext(ctx)
	return runBoundedMigrationOrchestration(
		ctx,
		db,
		func(tx *gorm.DB) error {
			if err := lockLegacyPlatformRoleTables(tx); err != nil {
				return err
			}
			if err := preflightLegacyPlatformRoleValues(tx); err != nil {
				return err
			}
			return runMigrationsFromModelLocked(
				ctx,
				tx,
				firstModel,
				membershipWriters...,
			)
		},
	)
}

func runMigrationsFromModelLocked(
	ctx context.Context,
	db *gorm.DB,
	firstModel int,
	membershipWriters ...ProjectScopeMembershipWriter,
) error {
	if ctx == nil {
		return errors.New("migration context is required")
	}
	if db == nil {
		return errors.New("migration database is required")
	}
	if db.Config == nil || db.Statement == nil {
		return errors.New("migration database is not initialized")
	}
	db = db.WithContext(ctx)
	log.Println("Running database migrations...")

	if err := validateMigrationResumePoint(firstModel, len(schemaMigrationModels())); err != nil {
		return err
	}

	// 1. 先迁移历史角色值，确保随后建立封闭枚举约束时不会被旧数据阻断。
	if err := migrateLegacyHumanRoles(db); err != nil {
		return fmt.Errorf("human-role migration failed: %w", err)
	}

	// 2. 在 AutoMigrate 收紧 NOT NULL/CHECK 之前先清理并扩展旧登录审计列。
	if err := MigrateLoginHistoryMethodContract(db); err != nil {
		return fmt.Errorf("login history method migration failed: %w", err)
	}

	// 3. Existing audit rows need a deterministic human ActorRef before the
	// canonical model makes the new columns non-null.
	if err := PrepareAdminAuditActorColumns(db); err != nil {
		return fmt.Errorf("admin audit actor preparation failed: %w", err)
	}

	// 4. The foundation checkpoint and complete legacy webhook credential
	// cutover ran in a dedicated top-level transaction before this main
	// migration transaction. This keeps ACCESS EXCLUSIVE off the rest of the
	// model scan while the session advisory lock still serializes migrations.

	// 5. Category scope has a dedicated evidence-driven cutover. Stage only a
	// retryable zero sentinel before the canonical NOT NULL model is parsed.
	if err := PrepareCategoryScopeColumns(db); err != nil {
		return fmt.Errorf("legacy category scope preparation failed: %w", err)
	}

	// 6. Existing pre-project PostgreSQL tables need a non-null zero sentinel
	// before canonical model tags are applied. No live scoped value is inferred
	// or rewritten here.
	if err := PrepareLegacyProjectScopeColumns(db); err != nil {
		return fmt.Errorf("legacy project scope preparation failed: %w", err)
	}

	// Existing decisions predate the serialized policy-set epoch. Add the
	// column as nullable and backfill a deterministic non-zero baseline before
	// GORM applies the canonical NOT NULL model contract.
	if err := PreparePolicyDecisionEpochColumn(db); err != nil {
		return fmt.Errorf("policy decision epoch preparation failed: %w", err)
	}

	// Membership draft contribution is an additive authorization fact. It
	// must be present even when an operator resumes after the membership model.
	if err := PrepareKnowledgeContributorColumn(db); err != nil {
		return fmt.Errorf("knowledge contributor preparation failed: %w", err)
	}
	if err := PrepareAttachmentStorageIdentityColumns(db); err != nil {
		return fmt.Errorf(
			"attachment storage identity preparation failed: %w",
			err,
		)
	}
	if err := PrepareKnowledgeObjectIdentityColumns(db); err != nil {
		return fmt.Errorf(
			"knowledge object identity preparation failed: %w",
			err,
		)
	}
	if err := PrepareKnowledgePublishedVersionContract(db); err != nil {
		return fmt.Errorf(
			"knowledge published-version preparation failed: %w",
			err,
		)
	}

	// 7. 自动迁移模型
	if err := autoMigrateFromModel(db, firstModel); err != nil {
		return fmt.Errorf("auto migration failed: %w", err)
	}
	if err := MigrateAttachmentStorageIdentityContract(db); err != nil {
		return fmt.Errorf(
			"attachment storage identity migration failed: %w",
			err,
		)
	}
	if err := MigrateKnowledgeObjectIdentityContract(db); err != nil {
		return fmt.Errorf(
			"knowledge object identity migration failed: %w",
			err,
		)
	}
	if err := MigrateKnowledgeObjectWriteIntentContract(db); err != nil {
		return fmt.Errorf(
			"knowledge object write intent migration failed: %w",
			err,
		)
	}
	if err := MigrateKnowledgePublishedVersionContract(db); err != nil {
		return fmt.Errorf(
			"knowledge published-version migration failed: %w",
			err,
		)
	}

	// Remove any interrupted migration default and verify the durable epoch
	// contract after the canonical model migration.
	if err := MigratePolicyDecisionEpochContract(db); err != nil {
		return fmt.Errorf("policy decision epoch migration failed: %w", err)
	}

	// 7. Install nullable platform-role/ActorRef constraints and the durable
	// audit-export job/lease/index contract.
	if err := MigrateAdminAuditExportContract(db); err != nil {
		return fmt.Errorf("admin audit export migration failed: %w", err)
	}

	// 9. 将单组织存量数据映射到显式默认项目，并回填人类与服务主体授权。
	if err := MigrateProjectScope(db, membershipWriters...); err != nil {
		return fmt.Errorf("project scope migration failed: %w", err)
	}

	// 10. 依据 Ticket 的可信 ProjectScope 克隆存量分类树并原子重写引用。
	if err := MigrateCategoryProjectScope(db); err != nil {
		return fmt.Errorf("category project scope migration failed: %w", err)
	}

	// 11. 原子替换旧四列幂等索引，使六列项目作用域 ON CONFLICT 契约可用。
	if err := MigrateIdempotencyScopeIndex(db); err != nil {
		return fmt.Errorf("idempotency scope index migration failed: %w", err)
	}

	// 10. 显式扩展外部 A2A 标识符；GORM 不会可靠修改已有 VARCHAR 长度。
	if err := MigrateA2AIdentifierContract(db); err != nil {
		return fmt.Errorf("A2A identifier migration failed: %w", err)
	}

	// 9. 将自动化规则持久契约收敛到当前 CloudEvent 类型。
	if err := MigrateAutomationRuleTriggerEvents(db); err != nil {
		return fmt.Errorf("automation trigger migration failed: %w", err)
	}

	// 10. 将 Webhook 订阅与投递日志迁移为完整的 canonical CloudEvent 类型。
	if err := MigrateWebhookEventTaxonomy(db); err != nil {
		return fmt.Errorf("webhook event taxonomy migration failed: %w", err)
	}

	// 11. 回填并约束权威 ActorRef，移除服务主体对人类账号的投影依赖。
	if err := MigrateActorProjections(db); err != nil {
		return fmt.Errorf("actor projection migration failed: %w", err)
	}

	// 12. 验证旧附件投影为空后删除，正式附件表成为唯一持久模型。
	if err := MigrateAttachmentProjections(db); err != nil {
		return fmt.Errorf("attachment projection migration failed: %w", err)
	}

	// 13. 只使用可证明的语义证据关联历史记录与不可变领域事件。
	if err := MigrateTicketHistoryEventLinks(db); err != nil {
		return fmt.Errorf("ticket history event-link migration failed: %w", err)
	}

	// 14. 将旧的多层回复链扁平化到顶层评论，确保分页端点仍可读取。
	if err := MigrateNestedCommentReplies(db); err != nil {
		return fmt.Errorf("nested comment reply migration failed: %w", err)
	}

	// 15. 收口删除语义明确的外键策略
	if err := EnsureForeignKeyPolicies(db); err != nil {
		return fmt.Errorf("foreign-key policy migration failed: %w", err)
	}

	// 16. 创建额外索引
	if err := CreateIndexes(db); err != nil {
		return fmt.Errorf("index creation failed: %w", err)
	}

	// 17. 为项目审计账本安装数据库级追加写与链连续性约束。
	if err := InstallAuditLedgerConstraints(db); err != nil {
		return fmt.Errorf("audit ledger constraint migration failed: %w", err)
	}

	// 18. 为 scope-ready 的项目业务表安装 PostgreSQL RLS policy。
	// ENABLE/FORCE 是所有写路径切换到 scoped transaction 后的显式部署步骤。
	if err := MigrateProjectRLS(db); err != nil {
		return fmt.Errorf("project RLS migration failed: %w", err)
	}
	// 19. 所有其他持久迁移成功后，最后切换平台角色、删除旧 role
	// 列并写入 checkpoint。随后的 runtime gate 只读，因此 checkpoint 是
	// 该外层事务的最后一笔 durable write。
	if err := MigratePlatformRoles(db, membershipWriters...); err != nil {
		return fmt.Errorf("platform role migration failed: %w", err)
	}

	// 19. 验证运行时所需的关键表、列、索引与 RLS policy readiness。
	if err := validateRuntimeSchema(db, false); err != nil {
		return fmt.Errorf("runtime schema validation failed: %w", err)
	}

	log.Println("All database migrations completed successfully")
	return nil
}
