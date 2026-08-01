package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/database"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	dsn      string
	verbose  bool
	dropAll  bool
	seedData bool
	samples  bool
	timeout  time.Duration
	resumeAt int
)

func main() {
	flag.StringVar(&dsn, "dsn", "", "PostgreSQL connection string")
	flag.BoolVar(&verbose, "v", false, "Enable verbose database logs")
	flag.BoolVar(&dropAll, "drop", false, "Drop ChronoDesk tables before migration")
	flag.BoolVar(&seedData, "seed", false, "Seed initial business data explicitly")
	flag.BoolVar(
		&samples,
		"sample-data",
		false,
		"Also seed demonstration records (requires -seed and ENVIRONMENT=development)",
	)
	flag.DurationVar(&timeout, "timeout", 5*time.Minute, "Maximum migration duration")
	flag.IntVar(
		&resumeAt,
		"resume-from-model",
		1,
		"Resume the one-based model scan after a bounded network timeout",
	)
	flag.Parse()
	_ = godotenv.Load()

	if dsn == "" {
		dsn = migrationDSNFromEnvironment()
	}
	if dsn == "" {
		log.Fatal("DATABASE_MIGRATION_URL or -dsn is required")
	}
	if timeout <= 0 {
		log.Fatal("-timeout must be positive")
	}
	if dropAll && os.Getenv("ALLOW_DESTRUCTIVE_MIGRATION") != "true" {
		log.Fatal("-drop requires ALLOW_DESTRUCTIVE_MIGRATION=true")
	}
	if samples && !seedData {
		log.Fatal("-sample-data requires -seed")
	}
	if err := database.ValidatePostgresTransport(
		dsn,
		os.Getenv("POSTGRES_ALLOW_INSECURE") == "true",
	); err != nil {
		log.Fatalf("PostgreSQL 连接安全校验失败：%v", err)
	}

	log.Println("开始执行 ChronoDesk 数据库迁移")
	gormConfig := &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: false,
		CreateBatchSize:                          1000,
		Logger:                                   logger.Default.LogMode(logger.Error),
	}
	if verbose {
		gormConfig.Logger = logger.Default.LogMode(logger.Info)
	}
	db, err := gorm.Open(postgres.Open(dsn), gormConfig)
	if err != nil {
		log.Fatalf("连接 PostgreSQL 失败：%v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("获取数据库连接池失败：%v", err)
	}
	defer sqlDB.Close()
	// The atomic GORM migration holds one PostgreSQL transaction connection
	// while catalog inspection may acquire a second connection.
	sqlDB.SetMaxIdleConns(2)
	sqlDB.SetMaxOpenConns(2)
	sqlDB.SetConnMaxLifetime(time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	db = db.WithContext(ctx)

	if dropAll {
		log.Println("正在删除 ChronoDesk 数据表")
		if err := dropChronoDeskTables(db); err != nil {
			log.Fatalf("删除数据表失败：%v", err)
		}
	}
	if err := database.RunMigrationsFromModel(
		ctx,
		db,
		resumeAt,
		services.EnsureProjectScopeMigrationMembership,
	); err != nil {
		log.Fatalf("数据库迁移失败：%v", err)
	}
	if seedData {
		if err := database.SeedData(db, database.SeedOptions{
			IncludeSampleData: samples,
			EnsureInitialAdministratorMembership: services.
				EnsureBootstrapProjectAdministratorMembership,
			EnsureSampleUserMembership: services.
				EnsureSampleProjectMembership,
		}); err != nil {
			log.Fatalf("初始化业务数据失败：%v", err)
		}
	}
	log.Println("ChronoDesk 数据库迁移完成")
}

func firstEnvironmentValue(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

func migrationDSNFromEnvironment() string {
	return firstEnvironmentValue(
		"DATABASE_MIGRATION_URL",
		"DATABASE_URL_UNPOOLED",
		"POSTGRES_URL_NON_POOLING",
		"DATABASE_URL",
	)
}

func dropChronoDeskTables(db *gorm.DB) error {
	// 仅包含 ChronoDesk 自有表，避免误删同一 schema 中的其他应用数据。
	// CASCADE 负责外键顺序，表名是编译期常量而非用户输入。
	tables := []string{
		"agent_push_notification_configs",
		"agent_task_events",
		"agent_task_status_history",
		"agent_artifacts",
		"agent_messages",
		"agent_tasks",
		"agent_admin_resource_versions",
		"outbox_deliveries",
		"domain_events",
		"ticket_leases",
		"idempotency_records",
		"policy_decisions",
		"agent_policies",
		"agent_credentials",
		"service_principals",
		"admin_audit_logs",
		"automation_logs",
		"quick_replies",
		"ticket_templates",
		"sla_configs",
		"automation_rules",
		"webhook_logs",
		"webhook_configs",
		"notifications",
		"notification_preferences",
		"cleanup_logs",
		"email_logs",
		"ticket_tags",
		"ticket_histories",
		"ticket_attachments",
		"ticket_comments",
		"tickets",
		"otp_trusted_devices",
		"otp_codes",
		"login_histories",
		"login_attempts",
		"refresh_tokens",
		"user_profiles",
		"password_resets",
		"email_verifications",
		"system_configs",
		"email_configs",
		"category_scope_migration_mappings",
		"categories",
		"users",
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, table := range tables {
			if err := tx.Exec(
				fmt.Sprintf(`DROP TABLE IF EXISTS "%s" CASCADE`, table),
			).Error; err != nil {
				return fmt.Errorf("drop %s: %w", table, err)
			}
		}
		return nil
	})
}
