package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gongdan-system/internal/auth"
	"gongdan-system/internal/models"
)

var (
	dsn      string
	verbose  bool
	dropAll  bool
	seedData bool
)

func init() {
	// 从环境变量或命令行参数获取数据库连接
	flag.StringVar(&dsn, "dsn", "", "Database connection string")
	flag.BoolVar(&verbose, "v", false, "Verbose output")
	flag.BoolVar(&dropAll, "drop", false, "Drop all tables before migration")
	flag.BoolVar(&seedData, "seed", false, "Seed initial data")
	flag.Parse()

	// 如果没有提供DSN，从环境变量读取
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
		if dsn == "" {
			log.Fatal("DATABASE_URL environment variable is required")
		}
	}
}

func main() {
	log.Println("🚀 Starting database migration...")

	// 配置GORM
	config := &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: false, // 启用外键约束
		CreateBatchSize:                          1000,  // 批量创建时的批次大小
	}

	// 根据verbose标志设置日志级别
	if verbose {
		config.Logger = logger.Default.LogMode(logger.Info)
	} else {
		config.Logger = logger.Default.LogMode(logger.Error)
	}

	// 连接数据库
	db, err := gorm.Open(postgres.Open(dsn), config)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("Failed to get database instance: %v", err)
	}
	defer sqlDB.Close()

	// 设置连接池
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(100)
	sqlDB.SetConnMaxLifetime(time.Hour)

	// 如果需要删除所有表
	if dropAll {
		log.Println("⚠️  Dropping all tables...")
		dropAllTables(db)
	}

	// 执行迁移
	log.Println("📦 Running auto migration...")
	if err := runMigration(db); err != nil {
		log.Fatalf("Migration failed: %v", err)
	}

	// 创建索引
	log.Println("🔍 Creating indexes...")
	if err := createIndexes(db); err != nil {
		log.Printf("Warning: Some indexes may have failed: %v", err)
	}

	// 如果需要种子数据
	if seedData {
		log.Println("🌱 Seeding initial data...")
		if err := seedInitialData(db); err != nil {
			log.Printf("Warning: Seeding failed: %v", err)
		}
	}

	log.Println("✅ Migration completed successfully!")
}

// runMigration 执行自动迁移
func runMigration(db *gorm.DB) error {
	// 按照依赖顺序迁移表
	// 1. 基础表（无外键依赖）
	basicModels := []interface{}{
		&models.User{},
		&auth.RefreshToken{},
		&auth.LoginAttempt{},
		&models.Category{},
		&models.EmailConfig{},
		&models.SystemConfig{},
		&models.OTPCode{},
		&models.EmailVerification{},
		&models.PasswordReset{},
	}

	// 2. 依赖User的表
	userDependentModels := []interface{}{
		&models.UserProfile{},
		&models.LoginHistory{},
		&models.OTPTrustedDevice{},
		&models.NotificationPreference{},
		&models.Ticket{},
	}

	// 3. 依赖Ticket的表
	ticketDependentModels := []interface{}{
		&models.TicketComment{},
		&models.TicketAttachment{},
		&models.TicketHistory{},
		&models.TicketTag{},
	}

	// 4. 其他表
	otherModels := []interface{}{
		&models.EmailLog{},
		&models.CleanupLog{},
		&models.Notification{},
		&models.WebhookConfig{},
	}

	// 5. FE008 自动化相关表
	automationModels := []interface{}{
		&models.AutomationRule{},
		&models.SLAConfig{},
		&models.TicketTemplate{},
		&models.AutomationLog{},
		&models.QuickReply{},
	}

	// 执行迁移
	allModels := append(basicModels, userDependentModels...)
	allModels = append(allModels, ticketDependentModels...)
	allModels = append(allModels, otherModels...)
	allModels = append(allModels, automationModels...)

	// 使用事务确保原子性
	return db.Transaction(func(tx *gorm.DB) error {
		// 启用批量创建以提高性能
		tx = tx.Session(&gorm.Session{CreateBatchSize: 1000})

		for _, model := range allModels {
			if err := tx.AutoMigrate(model); err != nil {
				return fmt.Errorf("failed to migrate %T: %w", model, err)
			}
			if verbose {
				log.Printf("  ✓ Migrated %T", model)
			}
		}
		return nil
	})
}

// createIndexes 创建数据库索引
func createIndexes(db *gorm.DB) error {
	indexes := []struct {
		Table string
		Name  string
		SQL   string
	}{
		// User indexes
		{"users", "idx_users_email", "CREATE INDEX IF NOT EXISTS idx_users_email ON users(email)"},
		{"users", "idx_users_role", "CREATE INDEX IF NOT EXISTS idx_users_role ON users(role)"},
		{"users", "idx_users_status", "CREATE INDEX IF NOT EXISTS idx_users_status ON users(status)"},

		// Refresh token indexes
		{"refresh_tokens", "idx_refresh_tokens_user", "CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user ON refresh_tokens(user_id)"},
		{"refresh_tokens", "idx_refresh_tokens_session", "CREATE INDEX IF NOT EXISTS idx_refresh_tokens_session ON refresh_tokens(session_id)"},

		// Ticket indexes
		{"tickets", "idx_tickets_status", "CREATE INDEX IF NOT EXISTS idx_tickets_status ON tickets(status)"},
		{"tickets", "idx_tickets_priority", "CREATE INDEX IF NOT EXISTS idx_tickets_priority ON tickets(priority)"},
		{"tickets", "idx_tickets_created_by", "CREATE INDEX IF NOT EXISTS idx_tickets_created_by ON tickets(created_by_id)"},
		{"tickets", "idx_tickets_assigned_to", "CREATE INDEX IF NOT EXISTS idx_tickets_assigned_to ON tickets(assigned_to_id)"},
		{"tickets", "idx_tickets_category", "CREATE INDEX IF NOT EXISTS idx_tickets_category ON tickets(category_id)"},
		{"tickets", "idx_tickets_created_at", "CREATE INDEX IF NOT EXISTS idx_tickets_created_at ON tickets(created_at DESC)"},

		// Composite indexes for common queries
		{"tickets", "idx_tickets_status_priority", "CREATE INDEX IF NOT EXISTS idx_tickets_status_priority ON tickets(status, priority)"},
		{"tickets", "idx_tickets_assigned_status", "CREATE INDEX IF NOT EXISTS idx_tickets_assigned_status ON tickets(assigned_to_id, status)"},

		// Comment indexes
		{"ticket_comments", "idx_comments_ticket", "CREATE INDEX IF NOT EXISTS idx_comments_ticket ON ticket_comments(ticket_id)"},
		{"ticket_comments", "idx_comments_user", "CREATE INDEX IF NOT EXISTS idx_comments_user ON ticket_comments(user_id)"},
		{"ticket_comments", "idx_comments_created", "CREATE INDEX IF NOT EXISTS idx_comments_created ON ticket_comments(created_at DESC)"},

		// Login history indexes
		{"login_histories", "idx_login_user", "CREATE INDEX IF NOT EXISTS idx_login_user ON login_histories(user_id)"},
		{"login_histories", "idx_login_time", "CREATE INDEX IF NOT EXISTS idx_login_time ON login_histories(login_time DESC)"},

		// Trusted device indexes
		{"otp_trusted_devices", "idx_trusted_devices_user", "CREATE INDEX IF NOT EXISTS idx_trusted_devices_user ON otp_trusted_devices(user_id)"},
		{"otp_trusted_devices", "idx_trusted_devices_hash", "CREATE UNIQUE INDEX IF NOT EXISTS idx_trusted_devices_hash ON otp_trusted_devices(device_token_hash)"},
		{"otp_trusted_devices", "idx_trusted_devices_expires", "CREATE INDEX IF NOT EXISTS idx_trusted_devices_expires ON otp_trusted_devices(expires_at)"},

		// Automation indexes
		{"automation_rules", "idx_automation_active", "CREATE INDEX IF NOT EXISTS idx_automation_active ON automation_rules(is_active, trigger_event)"},
		{"sla_configs", "idx_sla_active", "CREATE INDEX IF NOT EXISTS idx_sla_active ON sla_configs(is_active)"},

		// Notification indexes
		{"notifications", "idx_notifications_recipient", "CREATE INDEX IF NOT EXISTS idx_notifications_recipient ON notifications(recipient_id)"},
		{"notifications", "idx_notifications_type", "CREATE INDEX IF NOT EXISTS idx_notifications_type ON notifications(type)"},
		{"notifications", "idx_notifications_read", "CREATE INDEX IF NOT EXISTS idx_notifications_read ON notifications(is_read)"},
		{"notifications", "idx_notifications_created", "CREATE INDEX IF NOT EXISTS idx_notifications_created ON notifications(created_at DESC)"},

		// Webhook indexes
		{"webhook_configs", "idx_webhook_status", "CREATE INDEX IF NOT EXISTS idx_webhook_status ON webhook_configs(status)"},
		{"webhook_configs", "idx_webhook_provider", "CREATE INDEX IF NOT EXISTS idx_webhook_provider ON webhook_configs(provider)"},
	}

	for _, idx := range indexes {
		if err := db.Exec(idx.SQL).Error; err != nil {
			log.Printf("  ⚠️  Failed to create index %s: %v", idx.Name, err)
			continue
		}
		if verbose {
			log.Printf("  ✓ Created index %s on %s", idx.Name, idx.Table)
		}
	}

	return nil
}

// dropAllTables 删除所有表
func dropAllTables(db *gorm.DB) {
	// 按照反向依赖顺序删除表
	tables := []string{
		"automation_logs",
		"quick_replies",
		"ticket_templates",
		"sla_configs",
		"automation_rules",
		"webhook_configs",
		"notifications",
		"cleanup_logs",
		"email_logs",
		"ticket_tags",
		"ticket_histories",
		"ticket_attachments",
		"ticket_comments",
		"tickets",
		"notification_preferences",
		"login_histories",
		"user_profiles",
		"password_resets",
		"email_verifications",
		"otp_codes",
		"system_configs",
		"email_configs",
		"categories",
		"users",
	}

	// 禁用外键约束
	db.Exec("SET session_replication_role = 'replica';")

	for _, table := range tables {
		if err := db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s CASCADE", table)).Error; err != nil {
			log.Printf("  ⚠️  Failed to drop table %s: %v", table, err)
		} else if verbose {
			log.Printf("  ✓ Dropped table %s", table)
		}
	}

	// 重新启用外键约束
	db.Exec("SET session_replication_role = 'origin';")
}

// seedInitialData 种子数据
func seedInitialData(db *gorm.DB) error {
	// 创建默认管理员
	adminUser := &models.User{
		Username:      "admin",
		Email:         "admin@example.com",
		PasswordHash:  "$2a$10$rVmJHLHE3kD1VGpGO8pQhOxKRWBqH5lXXxYXxLV3HHWP1dWw.qEa", // password: Admin123!
		FirstName:     "System",
		LastName:      "Administrator",
		Role:          models.RoleAdmin,
		Status:        models.UserStatusActive,
		EmailVerified: true,
		Department:    "IT",
		JobTitle:      "System Administrator",
	}

	if err := db.FirstOrCreate(adminUser, models.User{Email: "admin@example.com"}).Error; err != nil {
		return fmt.Errorf("failed to create admin user: %w", err)
	}

	// 创建默认分类
	categories := []models.Category{
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
			Name:        "账单问题",
			Slug:        "billing",
			Description: "账单和付款相关问题",
			Type:        models.CategoryTypeBilling,
			Status:      models.CategoryStatusActive,
			IsPublic:    true,
			SortOrder:   2,
			CreatedBy:   adminUser.ID,
		},
		{
			Name:        "功能请求",
			Slug:        "feature-request",
			Description: "新功能建议和改进请求",
			Type:        models.CategoryTypeRequest,
			Status:      models.CategoryStatusActive,
			IsPublic:    true,
			SortOrder:   3,
			CreatedBy:   adminUser.ID,
		},
		{
			Name:        "Bug报告",
			Slug:        "bug-report",
			Description: "系统错误和问题报告",
			Type:        models.CategoryTypeComplaint,
			Status:      models.CategoryStatusActive,
			IsPublic:    true,
			SortOrder:   4,
			CreatedBy:   adminUser.ID,
		},
	}

	for _, cat := range categories {
		if err := db.FirstOrCreate(&cat, models.Category{Slug: cat.Slug}).Error; err != nil {
			log.Printf("  ⚠️  Failed to create category %s: %v", cat.Name, err)
		}
	}

	// 创建默认SLA配置
	urgentPriority := "urgent"
	highPriority := "high"
	normalPriority := "normal"
	defaultWorkingHours := `{"monday":{"start":"09:00","end":"18:00"},"tuesday":{"start":"09:00","end":"18:00"},"wednesday":{"start":"09:00","end":"18:00"},"thursday":{"start":"09:00","end":"18:00"},"friday":{"start":"09:00","end":"18:00"},"saturday":{"start":"","end":""},"sunday":{"start":"","end":""}}`
	emptyEscalations := `[]`

	slaConfigs := []models.SLAConfig{
		{
			Name:            "紧急工单SLA",
			Description:     "用于紧急优先级工单的SLA配置",
			Priority:        &urgentPriority,
			ResponseTime:    30,  // 30分钟
			ResolutionTime:  240, // 4小时
			ExcludeWeekends: false,
			ExcludeHolidays: false,
			IsActive:        true,
			IsDefault:       false,
			WorkingHours:    defaultWorkingHours,
			EscalationRules: emptyEscalations,
		},
		{
			Name:            "高优先级工单SLA",
			Description:     "用于高优先级工单的SLA配置",
			Priority:        &highPriority,
			ResponseTime:    120, // 2小时
			ResolutionTime:  480, // 8小时
			ExcludeWeekends: true,
			ExcludeHolidays: true,
			IsActive:        true,
			IsDefault:       false,
			WorkingHours:    defaultWorkingHours,
			EscalationRules: emptyEscalations,
		},
		{
			Name:            "普通工单SLA",
			Description:     "用于普通优先级工单的SLA配置",
			Priority:        &normalPriority,
			ResponseTime:    480,  // 8小时
			ResolutionTime:  2880, // 48小时
			ExcludeWeekends: true,
			ExcludeHolidays: true,
			IsActive:        true,
			IsDefault:       true,
			WorkingHours:    defaultWorkingHours,
			EscalationRules: emptyEscalations,
		},
	}

	for _, cfg := range slaConfigs {
		sla := cfg
		if err := db.Where("name = ?", sla.Name).FirstOrCreate(&sla).Error; err != nil {
			log.Printf("  ⚠️  Failed to create SLA config %s: %v", sla.Name, err)
		}
	}

	// 创建默认快速回复
	quickReplies := []models.QuickReply{
		{
			Name:      "感谢反馈",
			Content:   "感谢您的反馈，我们会尽快处理您的问题。",
			Category:  "通用",
			IsPublic:  true,
			CreatedBy: adminUser.ID,
		},
		{
			Name:      "需要更多信息",
			Content:   "为了更好地帮助您解决问题，请提供更多详细信息，例如：错误信息、操作步骤、系统环境等。",
			Category:  "技术支持",
			IsPublic:  true,
			CreatedBy: adminUser.ID,
		},
		{
			Name:      "问题已解决",
			Content:   "很高兴您的问题已经解决。如果还有其他问题，请随时联系我们。",
			Category:  "通用",
			IsPublic:  true,
			CreatedBy: adminUser.ID,
		},
	}

	for _, qr := range quickReplies {
		if err := db.FirstOrCreate(&qr, models.QuickReply{Name: qr.Name}).Error; err != nil {
			log.Printf("  ⚠️  Failed to create quick reply %s: %v", qr.Name, err)
		}
	}

	log.Println("  ✓ Seed data created successfully")
	return nil
}
