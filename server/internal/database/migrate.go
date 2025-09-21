package database

import (
	"fmt"
	"log"
	"os"

	"gongdan-system/internal/auth"
	"gongdan-system/internal/models"
	"gorm.io/gorm"
)

// AutoMigrate 自动迁移所有模型
func AutoMigrate(db *gorm.DB) error {
	log.Println("Starting database migration...")

	// 一次性迁移所有模型
	err := db.AutoMigrate(
		&models.User{},
		&auth.UserProfile{},
		&auth.RefreshToken{},
		&auth.LoginAttempt{},
		&models.Category{},
		&models.Ticket{},
		&models.TicketComment{},
		&models.TicketHistory{},
		&models.OTPCode{},
		&models.OTPTrustedDevice{},
		&models.WebhookConfig{},
		&models.WebhookLog{},
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
	)

	if err != nil {
		return fmt.Errorf("failed to migrate models: %w", err)
	}

	log.Println("Database migration completed successfully")
	return nil
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
		"CREATE INDEX IF NOT EXISTS idx_tickets_created_by ON tickets(created_by);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_assigned_to ON tickets(assigned_to);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_due_at ON tickets(due_at);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_resolved_at ON tickets(resolved_at);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_closed_at ON tickets(closed_at);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_status_priority ON tickets(status, priority);",
		"CREATE INDEX IF NOT EXISTS idx_tickets_created_at ON tickets(created_at);",

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

	for _, indexSQL := range indexes {
		if err := db.Exec(indexSQL).Error; err != nil {
			log.Printf("Warning: Failed to create index: %v", err)
			// 继续执行其他索引，不中断迁移过程
		}
	}

	log.Println("Additional indexes created successfully")
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
		// 创建默认管理员用户
		adminUser = models.User{
			Username:      "admin",
			Email:         "admin@example.com",
			PasswordHash:  "$2a$10$92IXUNpkjO0rOQ5byMi.Ye4oKoEa3Ro9llC/.og/at2.uheWG/igi", // password
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
		log.Println("Created default admin user (username: admin, password: password)")
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

// RunMigrations 运行完整的数据库迁移流程
func RunMigrations(db *gorm.DB) error {
	log.Println("Running database migrations...")

	// 1. 自动迁移模型
	if err := AutoMigrate(db); err != nil {
		return fmt.Errorf("auto migration failed: %w", err)
	}

	// 2. 创建额外索引
	if err := CreateIndexes(db); err != nil {
		return fmt.Errorf("index creation failed: %w", err)
	}

	// 3. 初始化种子数据
	if err := SeedData(db); err != nil {
		return fmt.Errorf("seed data creation failed: %w", err)
	}

	log.Println("All database migrations completed successfully")
	return nil
}
