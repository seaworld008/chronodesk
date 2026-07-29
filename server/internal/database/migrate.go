package database

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"gongdan-system/internal/auth"
	"gongdan-system/internal/models"
	"gorm.io/gorm"
)

// ValidateRuntimeSchema verifies additive migrations that the running binary
// cannot safely operate without. AUTO_MIGRATE may be disabled in production,
// but the process must fail fast instead of starting schedulers that repeatedly
// fail against an older schema.
func ValidateRuntimeSchema(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("database is required")
	}

	requirements := runtimeSchemaRequirements()
	if db.Dialector.Name() == "postgres" {
		return validatePostgresRuntimeSchema(db, requirements)
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
	return nil
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
		{&models.User{}, "users", []string{
			"id", "role", "status", "password_hash", "password_reset_at",
			"two_factor_enabled", "two_factor_secret", "backup_codes",
		}},
		{&auth.RefreshToken{}, "refresh_tokens", []string{"user_id", "session_id", "expires_at", "revoked"}},
		{&auth.EmailVerification{}, "email_verifications", []string{"user_id", "token", "used", "expires_at", "used_at"}},
		{&auth.PasswordReset{}, "password_resets", []string{"user_id", "token", "used", "expires_at", "used_at"}},
		{&auth.OTPCode{}, "otp_codes", []string{"user_id", "code", "type", "expires_at", "used", "used_at"}},
		{&models.LoginHistory{}, "login_histories", []string{"user_id", "session_id", "is_active"}},
		{&models.ServicePrincipal{}, "service_principals", []string{"id", "status", "scopes", "read_only", "emergency_disabled", "expires_at"}},
		{&models.AgentCredential{}, "agent_credentials", []string{"service_principal_id", "secret_hash", "status", "expires_at", "revoked_at"}},
		{&models.AgentPolicy{}, "agent_policies", []string{"service_principal_id", "effect", "scope", "action", "conditions", "is_active"}},
		{&models.PolicyDecision{}, "policy_decisions", []string{"actor_type", "actor_id", "credential_id", "allowed", "reason_code", "request_digest"}},
		{&models.IdempotencyRecord{}, "idempotency_records", []string{
			"actor_type", "actor_id", "operation", "key", "request_hash", "state",
			"resource_snapshot", "expires_at", "completion_ttl_nanoseconds", "completed_at",
		}},
		{&models.Ticket{}, "tickets", []string{
			"id", "version", "agent_context", "trust_level", "created_by_actor_type",
			"created_by_actor_id", "assigned_to_actor_type", "assigned_to_actor_id",
		}},
		{&models.TicketComment{}, "ticket_comments", []string{"ticket_id", "actor_type", "actor_id", "service_principal_id", "type"}},
		{&models.TicketAttachment{}, "ticket_attachments", []string{
			"ticket_id", "actor_type", "actor_id", "service_principal_id",
			"storage_path", "hash", "virus_scan",
		}},
		{&models.TicketHistory{}, "ticket_histories", []string{"ticket_id", "actor_type", "actor_id", "service_principal_id", "action", "details"}},
		{&models.TicketLease{}, "ticket_leases", []string{"ticket_id", "holder_actor_type", "holder_actor_id", "ticket_version", "expires_at", "released_at"}},
		{&models.DomainEvent{}, "domain_events", []string{
			"spec_version", "source", "type", "subject", "time", "data",
			"actor_type", "actor_id", "resource_version", "published_at",
		}},
		{&models.OutboxDelivery{}, "outbox_deliveries", []string{
			"event_id", "destination_type", "destination_id", "status",
			"attempts", "next_attempt_at", "locked_at", "locked_by",
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
		{&models.Notification{}, "notifications", []string{"recipient_id", "type", "channel", "related_ticket_id", "source_event_key"}},
	}
}

// AutoMigrate 自动迁移所有模型
func AutoMigrate(db *gorm.DB) error {
	return autoMigrateFromModel(db, 1)
}

func autoMigrateFromModel(db *gorm.DB, firstModel int) error {
	log.Println("Starting database migration...")

	migrationModels := schemaMigrationModels()
	if firstModel < 1 || firstModel > len(migrationModels)+1 {
		return fmt.Errorf(
			"first migration model must be between 1 and %d",
			len(migrationModels)+1,
		)
	}
	// 每个模型独立提交并记录进度。高延迟云数据库的元数据查询较多，
	// 分段后失败可安全重跑，也能精确定位慢表，而不是出现无输出的长等待。
	for index := firstModel - 1; index < len(migrationModels); index++ {
		model := migrationModels[index]
		startedAt := time.Now()
		if err := migrateOneModel(db, model); err != nil {
			return fmt.Errorf("failed to migrate model %d %T: %w", index+1, model, err)
		}
		log.Printf(
			"Migrated model %d/%d %T in %s",
			index+1,
			len(migrationModels),
			model,
			time.Since(startedAt).Round(time.Millisecond),
		)
	}

	log.Println("Database migration completed successfully")
	return nil
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
	return db.AutoMigrate(model)
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
	}
}

// CreateIndexes 创建额外的索引
func CreateIndexes(db *gorm.DB) error {
	log.Println("Creating additional indexes...")

	// 用户表索引
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);",
		"CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);",
		"CREATE INDEX IF NOT EXISTS idx_users_role ON users(role);",
		"CREATE INDEX IF NOT EXISTS idx_users_status ON users(status);",
		"CREATE INDEX IF NOT EXISTS idx_users_department ON users(department);",
		"CREATE INDEX IF NOT EXISTS idx_users_last_login_at ON users(last_login_at);",

		// 分类表索引
		"CREATE INDEX IF NOT EXISTS idx_categories_parent_id ON categories(parent_id);",
		"CREATE INDEX IF NOT EXISTS idx_categories_slug ON categories(slug);",
		"CREATE INDEX IF NOT EXISTS idx_categories_status ON categories(status);",
		"CREATE INDEX IF NOT EXISTS idx_categories_type ON categories(type);",

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
		"CREATE INDEX IF NOT EXISTS idx_tickets_version ON tickets(id, version);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_creator_actor ON tickets(created_by_actor_type, created_by_actor_id);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_assignee_actor ON tickets(assigned_to_actor_type, assigned_to_actor_id);",

		// 工单评论表索引
		"CREATE INDEX IF NOT EXISTS idx_ticket_comments_ticket_id ON ticket_comments(ticket_id);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_comments_user_id ON ticket_comments(user_id);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_comments_type ON ticket_comments(type);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_comments_parent_id ON ticket_comments(parent_id);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_comments_created_at ON ticket_comments(created_at);",

		// 工单历史表索引
		"CREATE INDEX IF NOT EXISTS idx_ticket_histories_ticket_id ON ticket_histories(ticket_id);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_histories_user_id ON ticket_histories(user_id);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_histories_action ON ticket_histories(action);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_histories_created_at ON ticket_histories(created_at);",

		// Agent-native identity, policy, event and lease indexes
		"CREATE INDEX IF NOT EXISTS idx_service_principals_controls ON service_principals(status, emergency_disabled, expires_at);",
		"CREATE INDEX IF NOT EXISTS idx_agent_credentials_lookup ON agent_credentials(service_principal_id, status, expires_at);",
		"CREATE INDEX IF NOT EXISTS idx_agent_policies_lookup ON agent_policies(service_principal_id, scope, is_active, priority DESC);",
		"CREATE INDEX IF NOT EXISTS idx_policy_decisions_actor_time ON policy_decisions(actor_type, actor_id, created_at DESC);",
		"CREATE INDEX IF NOT EXISTS idx_domain_events_stream ON domain_events(type, created_at, id);",
		"CREATE INDEX IF NOT EXISTS idx_outbox_pending ON outbox_deliveries(status, next_attempt_at);",
		"CREATE INDEX IF NOT EXISTS idx_idempotency_expiry ON idempotency_records(expires_at);",
		"CREATE INDEX IF NOT EXISTS idx_ticket_leases_expiry ON ticket_leases(expires_at, released_at);",
		"CREATE INDEX IF NOT EXISTS idx_agent_tasks_context_state ON agent_tasks(context_id, state, updated_at DESC);",
		"CREATE INDEX IF NOT EXISTS idx_agent_task_events_context_cursor ON agent_task_events(context_id, id);",
		"CREATE INDEX IF NOT EXISTS idx_agent_push_task ON agent_push_notification_configs(task_id);",

		// OTP表索引
		"CREATE INDEX IF NOT EXISTS idx_otp_codes_user_id ON otp_codes(user_id);",
		"CREATE INDEX IF NOT EXISTS idx_otp_codes_code ON otp_codes(code);",
		"CREATE INDEX IF NOT EXISTS idx_otp_codes_expires_at ON otp_codes(expires_at);",
		"CREATE INDEX IF NOT EXISTS idx_otp_codes_type ON otp_codes(type);",

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

		// 清理日志表索引
		"CREATE INDEX IF NOT EXISTS idx_cleanup_logs_task_type ON cleanup_logs(task_type);",
		"CREATE INDEX IF NOT EXISTS idx_cleanup_logs_status ON cleanup_logs(status);",
		"CREATE INDEX IF NOT EXISTS idx_cleanup_logs_start_time ON cleanup_logs(start_time);",
		"CREATE INDEX IF NOT EXISTS idx_cleanup_logs_trigger_type ON cleanup_logs(trigger_type);",
		"CREATE INDEX IF NOT EXISTS idx_cleanup_logs_trigger_by ON cleanup_logs(trigger_by);",
		"CREATE INDEX IF NOT EXISTS idx_cleanup_logs_task_status ON cleanup_logs(task_type, status);",
	}

	var indexErrors []error
	for _, indexSQL := range indexes {
		if err := db.Exec(indexSQL).Error; err != nil {
			indexErrors = append(indexErrors, fmt.Errorf("%s: %w", indexSQL, err))
		}
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

// SeedData 初始化种子数据
func SeedData(db *gorm.DB) error {
	log.Println("Seeding initial data...")

	// 检查是否已有管理员用户
	var adminUser models.User
	var adminCount int64
	db.Model(&models.User{}).Where("role = ?", models.RoleAdmin).Count(&adminCount)

	if adminCount == 0 {
		adminPassword := os.Getenv("ADMIN_PASSWORD")
		if adminPassword == "" {
			return fmt.Errorf("ADMIN_PASSWORD is required when seeding the initial administrator")
		}
		passwordHash, err := auth.NewSimplePasswordService(8, "").HashPassword(adminPassword)
		if err != nil {
			return fmt.Errorf("failed to hash initial administrator password: %w", err)
		}
		adminEmail := os.Getenv("ADMIN_EMAIL")
		if adminEmail == "" {
			adminEmail = "admin@example.com"
		}

		adminUser = models.User{
			Username:      "admin",
			Email:         adminEmail,
			PasswordHash:  passwordHash,
			FirstName:     "System",
			LastName:      "Administrator",
			Role:          models.RoleAdmin,
			Status:        models.UserStatusActive,
			EmailVerified: true,
			Department:    "IT",
			JobTitle:      "System Administrator",
		}

		if err := db.Create(&adminUser).Error; err != nil {
			return fmt.Errorf("failed to create admin user: %w", err)
		}
		log.Printf("Created initial administrator (username: admin, email: %s)", adminEmail)
	} else {
		// 获取现有的管理员用户
		if err := db.Where("role = ?", models.RoleAdmin).First(&adminUser).Error; err != nil {
			return fmt.Errorf("failed to get admin user: %w", err)
		}
	}

	// 检查是否已有默认分类
	var categoryCount int64
	db.Model(&models.Category{}).Count(&categoryCount)

	if categoryCount == 0 {
		// 创建默认分类，使用管理员用户ID作为创建者
		defaultCategories := []*models.Category{
			{
				Name:        "技术支持",
				Slug:        "technical-support",
				Description: "技术相关问题和支持请求",
				Type:        models.CategoryTypeSupport,
				Status:      models.CategoryStatusActive,
				IsPublic:    true,
				SortOrder:   1,
				CreatedBy:   adminUser.ID,
			},
			{
				Name:        "账户问题",
				Slug:        "account-issues",
				Description: "账户相关问题和请求",
				Type:        models.CategoryTypeSupport,
				Status:      models.CategoryStatusActive,
				IsPublic:    true,
				SortOrder:   2,
				CreatedBy:   adminUser.ID,
			},
			{
				Name:        "功能请求",
				Slug:        "feature-requests",
				Description: "新功能请求和改进建议",
				Type:        models.CategoryTypeRequest,
				Status:      models.CategoryStatusActive,
				IsPublic:    true,
				SortOrder:   3,
				CreatedBy:   adminUser.ID,
			},
			{
				Name:        "Bug报告",
				Slug:        "bug-reports",
				Description: "系统错误和Bug报告",
				Type:        models.CategoryTypeIncident,
				Status:      models.CategoryStatusActive,
				IsPublic:    true,
				SortOrder:   4,
				CreatedBy:   adminUser.ID,
			},
		}

		for _, category := range defaultCategories {
			if err := db.Create(category).Error; err != nil {
				return fmt.Errorf("failed to create category %s: %w", category.Name, err)
			}
		}
		log.Println("Created default categories")
	}

	// 生成示例数据（仅在开发环境）
	if err := generateSampleDataIfNeeded(db); err != nil {
		log.Printf("Warning: Failed to generate sample data: %v", err)
		// 不阻断迁移过程，仅记录警告
	}

	log.Println("Initial data seeding completed")
	return nil
}

// generateSampleDataIfNeeded 在需要时生成示例数据
func generateSampleDataIfNeeded(db *gorm.DB) error {
	// 检查环境变量，仅在开发环境生成示例数据
	environment := os.Getenv("ENVIRONMENT")
	if environment == "production" {
		log.Println("Production environment detected, skipping sample data generation")
		return nil
	}

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
	generator := NewSampleDataGenerator(db)
	if err := generator.GenerateAllSampleData(); err != nil {
		return fmt.Errorf("failed to generate sample data: %w", err)
	}

	log.Println("✅ Sample data generation completed successfully")
	return nil
}

// RunMigrations 只执行可重复的结构迁移。生产启动与普通 migrate 命令
// 绝不能隐式创建账号、分类或示例工单；种子数据必须由显式 seed 命令触发。
func RunMigrations(db *gorm.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return RunMigrationsContext(ctx, db)
}

// RunMigrationsContext is the bounded migration entry point used by operator
// tooling. A lost TLS connection or pooler response must never leave deployment
// automation blocked indefinitely.
func RunMigrationsContext(ctx context.Context, db *gorm.DB) error {
	return RunMigrationsFromModel(ctx, db, 1)
}

// RunMigrationsFromModel resumes the model scan at a one-based index while
// still running all index and runtime-schema gates. It is intended for a
// bounded operator retry after a network timeout; normal callers start at 1.
func RunMigrationsFromModel(ctx context.Context, db *gorm.DB, firstModel int) error {
	if ctx == nil {
		return errors.New("migration context is required")
	}
	if db == nil {
		return errors.New("migration database is required")
	}
	db = db.WithContext(ctx)
	log.Println("Running database migrations...")

	// 1. 自动迁移模型
	if err := autoMigrateFromModel(db, firstModel); err != nil {
		return fmt.Errorf("auto migration failed: %w", err)
	}

	// 2. 收口删除语义明确的外键策略
	if err := EnsureForeignKeyPolicies(db); err != nil {
		return fmt.Errorf("foreign-key policy migration failed: %w", err)
	}

	// 3. 创建额外索引
	if err := CreateIndexes(db); err != nil {
		return fmt.Errorf("index creation failed: %w", err)
	}

	// 4. 验证运行时所需的关键表和列真实存在
	if err := ValidateRuntimeSchema(db); err != nil {
		return fmt.Errorf("runtime schema validation failed: %w", err)
	}

	log.Println("All database migrations completed successfully")
	return nil
}
