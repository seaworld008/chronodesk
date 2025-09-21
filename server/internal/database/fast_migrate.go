package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// FastMigrator 快速迁移器 - 直接执行SQL，避免GORM的性能问题
type FastMigrator struct {
	db *sql.DB
}

// NewFastMigrator 创建快速迁移器 (接受已有的数据库连接)
func NewFastMigrator(db *sql.DB) *FastMigrator {
	return &FastMigrator{db: db}
}

// NewFastMigratorFromDSN 从DSN创建快速迁移器
func NewFastMigratorFromDSN(dsn string) (*FastMigrator, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}

	// 优化连接池
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping数据库失败: %w", err)
	}

	return &FastMigrator{db: db}, nil
}

// Close 关闭连接
func (m *FastMigrator) Close() error {
	return m.db.Close()
}

// ExecuteSingle 执行单个SQL语句
func (m *FastMigrator) ExecuteSingle(ctx context.Context, sql string) error {
	_, err := m.db.ExecContext(ctx, sql)
	return err
}

// ExecuteBatch 批量执行SQL语句
func (m *FastMigrator) ExecuteBatch(ctx context.Context, sqls []string) error {
	start := time.Now()
	
	// 开始事务
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始事务失败: %w", err)
	}
	defer tx.Rollback()

	for i, sqlStmt := range sqls {
		if strings.TrimSpace(sqlStmt) == "" {
			continue
		}
		
		log.Printf("执行SQL [%d/%d]: %s", i+1, len(sqls), getSQLSummary(sqlStmt))
		
		if _, err := tx.ExecContext(ctx, sqlStmt); err != nil {
			return fmt.Errorf("执行SQL失败 [%d/%d]: %s\n错误: %w", i+1, len(sqls), getSQLSummary(sqlStmt), err)
		}
	}

	// 提交事务
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}

	log.Printf("✅ 批量执行完成，耗时: %v", time.Since(start))
	return nil
}

// TableExists 检查表是否存在
func (m *FastMigrator) TableExists(tableName string) (bool, error) {
	var exists bool
	query := `SELECT EXISTS (
		SELECT FROM information_schema.tables 
		WHERE table_schema = CURRENT_SCHEMA() 
		AND table_name = $1
	)`
	
	err := m.db.QueryRow(query, tableName).Scan(&exists)
	return exists, err
}

// GetAllDDL 获取完整的DDL语句
func (m *FastMigrator) GetAllDDL() []string {
	return []string{
		// 创建扩展
		`CREATE EXTENSION IF NOT EXISTS "uuid-ossp";`,
		
		// 创建用户表
		`CREATE TABLE IF NOT EXISTS users (
			id SERIAL PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMPTZ,
			username VARCHAR(50) UNIQUE NOT NULL,
			email VARCHAR(100) UNIQUE NOT NULL,
			phone VARCHAR(20),
			password_hash VARCHAR(255) NOT NULL,
			first_name VARCHAR(50),
			last_name VARCHAR(50),
			display_name VARCHAR(100),
			avatar VARCHAR(500),
			timezone VARCHAR(50) DEFAULT 'UTC',
			language VARCHAR(10) DEFAULT 'en',
			role VARCHAR(20) NOT NULL DEFAULT 'customer',
			status VARCHAR(20) NOT NULL DEFAULT 'active',
			permissions TEXT,
			email_verified BOOLEAN NOT NULL DEFAULT FALSE,
			email_verified_at TIMESTAMPTZ,
			phone_verified BOOLEAN NOT NULL DEFAULT FALSE,
			phone_verified_at TIMESTAMPTZ,
			two_factor_enabled BOOLEAN NOT NULL DEFAULT FALSE,
			two_factor_secret VARCHAR(255),
			last_login_at TIMESTAMPTZ,
			last_login_ip VARCHAR(45),
			login_attempts INTEGER NOT NULL DEFAULT 0,
			locked_until TIMESTAMPTZ,
			password_reset_token VARCHAR(255),
			password_reset_at TIMESTAMPTZ,
			department VARCHAR(100),
			job_title VARCHAR(100),
			manager_id INTEGER,
			tickets_created INTEGER NOT NULL DEFAULT 0,
			tickets_assigned INTEGER NOT NULL DEFAULT 0,
			tickets_resolved INTEGER NOT NULL DEFAULT 0
		);`,

		// 创建分类表
		`CREATE TABLE IF NOT EXISTS categories (
			id SERIAL PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMPTZ,
			name VARCHAR(100) NOT NULL,
			description TEXT,
			color VARCHAR(7),
			icon VARCHAR(50),
			parent_id INTEGER,
			is_active BOOLEAN NOT NULL DEFAULT TRUE,
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_by INTEGER NOT NULL
		);`,

		// 创建工单表
		`CREATE TABLE IF NOT EXISTS tickets (
			id SERIAL PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMPTZ,
			ticket_number VARCHAR(50) UNIQUE,
			title VARCHAR(255) NOT NULL,
			description TEXT NOT NULL,
			type VARCHAR(20) NOT NULL DEFAULT 'incident',
			priority VARCHAR(20) NOT NULL DEFAULT 'normal',
			status VARCHAR(20) NOT NULL DEFAULT 'open',
			source VARCHAR(20) NOT NULL DEFAULT 'web',
			created_by_id INTEGER NOT NULL,
			assigned_to_id INTEGER,
			category_id INTEGER,
			subcategory_id INTEGER,
			tags TEXT,
			due_date TIMESTAMPTZ,
			resolved_at TIMESTAMPTZ,
			closed_at TIMESTAMPTZ,
			first_reply_at TIMESTAMPTZ,
			sla_breached BOOLEAN NOT NULL DEFAULT FALSE,
			sla_due_date TIMESTAMPTZ,
			response_time INTEGER,
			resolution_time INTEGER,
			customer_email VARCHAR(100),
			customer_phone VARCHAR(20),
			customer_name VARCHAR(100),
			attachments TEXT,
			custom_fields TEXT,
			internal_notes TEXT,
			view_count INTEGER NOT NULL DEFAULT 0,
			comment_count INTEGER NOT NULL DEFAULT 0,
			rating INTEGER,
			rating_comment TEXT
		);`,

		// 创建工单评论表
		`CREATE TABLE IF NOT EXISTS ticket_comments (
			id SERIAL PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMPTZ,
			ticket_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			content TEXT NOT NULL,
			type VARCHAR(20) NOT NULL DEFAULT 'public',
			is_internal BOOLEAN NOT NULL DEFAULT FALSE,
			attachments TEXT,
			edited_at TIMESTAMPTZ,
			edited_by INTEGER
		);`,

		// 创建工单历史表
		`CREATE TABLE IF NOT EXISTS ticket_histories (
			id SERIAL PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			ticket_id INTEGER NOT NULL,
			user_id INTEGER,
			action VARCHAR(50) NOT NULL,
			description TEXT NOT NULL,
			details TEXT,
			field_name VARCHAR(50),
			old_value TEXT,
			new_value TEXT,
			source_ip VARCHAR(45),
			user_agent TEXT,
			metadata TEXT,
			comment_id INTEGER,
			attachment_id INTEGER,
			duration INTEGER,
			scheduled_at TIMESTAMPTZ,
			completed_at TIMESTAMPTZ,
			is_visible BOOLEAN NOT NULL DEFAULT TRUE,
			is_system BOOLEAN NOT NULL DEFAULT FALSE,
			is_automated BOOLEAN NOT NULL DEFAULT FALSE,
			is_important BOOLEAN NOT NULL DEFAULT FALSE
		);`,

		// 创建OTP验证码表
		`CREATE TABLE IF NOT EXISTS otp_codes (
			id SERIAL PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			user_id INTEGER NOT NULL,
			code VARCHAR(10) NOT NULL,
			type VARCHAR(20) NOT NULL DEFAULT 'login',
			status VARCHAR(20) NOT NULL DEFAULT 'pending',
			expires_at TIMESTAMPTZ NOT NULL,
			verified_at TIMESTAMPTZ,
			attempts INTEGER NOT NULL DEFAULT 0,
			max_attempts INTEGER NOT NULL DEFAULT 3
		);`,

		// 创建邮件配置表
		`CREATE TABLE IF NOT EXISTS email_configs (
			id SERIAL PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			smtp_host VARCHAR(255) NOT NULL,
			smtp_port INTEGER NOT NULL,
			smtp_username VARCHAR(255) NOT NULL,
			smtp_password VARCHAR(255),
			smtp_from_email VARCHAR(255) NOT NULL,
			smtp_from_name VARCHAR(255) NOT NULL,
			smtp_encryption VARCHAR(10) NOT NULL DEFAULT 'none',
			email_verification_enabled BOOLEAN NOT NULL DEFAULT FALSE,
			is_active BOOLEAN NOT NULL DEFAULT TRUE
		);`,

		// 创建通知表
		`CREATE TABLE IF NOT EXISTS notifications (
			id SERIAL PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			type VARCHAR(50) NOT NULL,
			title VARCHAR(255) NOT NULL,
			content TEXT NOT NULL,
			priority VARCHAR(20) NOT NULL DEFAULT 'normal',
			channel VARCHAR(20) NOT NULL DEFAULT 'in_app',
			recipient_id INTEGER NOT NULL,
			sender_id INTEGER,
			related_type VARCHAR(50),
			related_id INTEGER,
			related_ticket_id INTEGER,
			is_read BOOLEAN NOT NULL DEFAULT FALSE,
			read_at TIMESTAMPTZ,
			is_sent BOOLEAN NOT NULL DEFAULT FALSE,
			sent_at TIMESTAMPTZ,
			is_delivered BOOLEAN NOT NULL DEFAULT FALSE,
			delivered_at TIMESTAMPTZ,
			retry_count INTEGER NOT NULL DEFAULT 0,
			last_retry_at TIMESTAMPTZ,
			next_retry_at TIMESTAMPTZ,
			max_retries INTEGER NOT NULL DEFAULT 3,
			metadata TEXT,
			action_url VARCHAR(500),
			expires_at TIMESTAMPTZ,
			scheduled_at TIMESTAMPTZ,
			error_message TEXT,
			delivery_status VARCHAR(50)
		);`,

		// 创建通知偏好设置表
		`CREATE TABLE IF NOT EXISTS notification_preferences (
			id SERIAL PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			user_id INTEGER NOT NULL,
			notification_type VARCHAR(50) NOT NULL,
			email_enabled BOOLEAN NOT NULL DEFAULT TRUE,
			in_app_enabled BOOLEAN NOT NULL DEFAULT TRUE,
			webhook_enabled BOOLEAN NOT NULL DEFAULT FALSE,
			do_not_disturb_start TIME,
			do_not_disturb_end TIME,
			max_daily_count INTEGER NOT NULL DEFAULT 50,
			batch_delivery BOOLEAN NOT NULL DEFAULT FALSE,
			batch_interval INTEGER NOT NULL DEFAULT 60,
			CONSTRAINT unique_user_notification_type UNIQUE (user_id, notification_type)
		);`,

		// 添加外键约束
		`ALTER TABLE categories ADD CONSTRAINT fk_categories_parent 
			FOREIGN KEY (parent_id) REFERENCES categories(id);`,
		`ALTER TABLE categories ADD CONSTRAINT fk_categories_created_by 
			FOREIGN KEY (created_by) REFERENCES users(id);`,
		`ALTER TABLE tickets ADD CONSTRAINT fk_tickets_created_by 
			FOREIGN KEY (created_by_id) REFERENCES users(id);`,
		`ALTER TABLE tickets ADD CONSTRAINT fk_tickets_assigned_to 
			FOREIGN KEY (assigned_to_id) REFERENCES users(id);`,
		`ALTER TABLE tickets ADD CONSTRAINT fk_tickets_category 
			FOREIGN KEY (category_id) REFERENCES categories(id);`,
		`ALTER TABLE tickets ADD CONSTRAINT fk_tickets_subcategory 
			FOREIGN KEY (subcategory_id) REFERENCES categories(id);`,
		`ALTER TABLE ticket_comments ADD CONSTRAINT fk_ticket_comments_ticket 
			FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE;`,
		`ALTER TABLE ticket_comments ADD CONSTRAINT fk_ticket_comments_user 
			FOREIGN KEY (user_id) REFERENCES users(id);`,
		`ALTER TABLE ticket_histories ADD CONSTRAINT fk_ticket_histories_ticket 
			FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE;`,
		`ALTER TABLE ticket_histories ADD CONSTRAINT fk_ticket_histories_user 
			FOREIGN KEY (user_id) REFERENCES users(id);`,
		`ALTER TABLE otp_codes ADD CONSTRAINT fk_otp_codes_user 
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;`,
		`ALTER TABLE notifications ADD CONSTRAINT fk_notifications_recipient 
			FOREIGN KEY (recipient_id) REFERENCES users(id);`,
		`ALTER TABLE notifications ADD CONSTRAINT fk_notifications_sender 
			FOREIGN KEY (sender_id) REFERENCES users(id);`,
		`ALTER TABLE notifications ADD CONSTRAINT fk_notifications_ticket 
			FOREIGN KEY (related_ticket_id) REFERENCES tickets(id);`,
		`ALTER TABLE notification_preferences ADD CONSTRAINT fk_notification_preferences_user 
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;`,
	}
}

// GetIndexSQL 获取所有索引
func (m *FastMigrator) GetIndexSQL() []string {
	return []string{
		// 用户表索引
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_users_email ON users(email);`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_users_username ON users(username);`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_users_status_role ON users(status, role);`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_users_deleted_at ON users(deleted_at);`,

		// 工单表核心索引
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tickets_status_priority ON tickets(status, priority);`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tickets_assigned_status ON tickets(assigned_to_id, status) WHERE assigned_to_id IS NOT NULL;`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tickets_created_by ON tickets(created_by_id);`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tickets_created_at ON tickets(created_at DESC);`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tickets_updated_at ON tickets(updated_at DESC);`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tickets_category ON tickets(category_id) WHERE category_id IS NOT NULL;`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tickets_deleted_at ON tickets(deleted_at);`,

		// 评论表索引
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_ticket_comments_ticket ON ticket_comments(ticket_id);`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_ticket_comments_user ON ticket_comments(user_id);`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_ticket_comments_created_at ON ticket_comments(created_at DESC);`,

		// 历史记录索引
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_ticket_histories_ticket ON ticket_histories(ticket_id);`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_ticket_histories_created_at ON ticket_histories(created_at DESC);`,

		// 通知表索引
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_notifications_recipient_read ON notifications(recipient_id, is_read);`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_notifications_created_at ON notifications(created_at DESC);`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_notifications_scheduled ON notifications(scheduled_at) WHERE scheduled_at IS NOT NULL;`,

		// 全文搜索索引
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tickets_search ON tickets USING gin(to_tsvector('english', title || ' ' || description));`,
	}
}

// GetSeedSQL 获取种子数据
func (m *FastMigrator) GetSeedSQL() []string {
	return []string{
		// 创建默认管理员用户
		`INSERT INTO users (username, email, password_hash, first_name, last_name, role, status, email_verified, department, job_title)
		VALUES ('admin', 'admin@example.com', '$2a$10$hvZK07Uq05seV33oX5n4FuaFmJFdWMdXNcGYZcuVQpfxtWz.AxzUe', 
			'System', 'Administrator', 'admin', 'active', true, 'IT', 'System Administrator')
		ON CONFLICT (email) DO NOTHING;`,

		// 创建默认分类
		`INSERT INTO categories (name, description, is_active, sort_order, created_by)
		SELECT 'Technical Support', '技术支持相关问题', true, 1, u.id FROM users u WHERE u.email = 'admin@example.com'
		ON CONFLICT DO NOTHING;`,

		`INSERT INTO categories (name, description, is_active, sort_order, created_by)
		SELECT 'Bug Report', '软件缺陷报告', true, 2, u.id FROM users u WHERE u.email = 'admin@example.com'
		ON CONFLICT DO NOTHING;`,

		// 创建默认邮件配置
		`INSERT INTO email_configs (smtp_host, smtp_port, smtp_username, smtp_password, smtp_from_email, smtp_from_name, is_active)
		VALUES ('localhost', 587, 'noreply@example.com', '', 'noreply@example.com', 'Ticket System', false)
		ON CONFLICT DO NOTHING;`,
	}
}

// RunFastMigration 执行快速迁移
func (m *FastMigrator) RunFastMigration() error {
	ctx := context.Background()
	log.Println("🚀 开始快速数据库迁移...")
	start := time.Now()

	// 1. 创建表结构
	log.Println("📋 创建表结构...")
	if err := m.ExecuteBatch(ctx, m.GetAllDDL()); err != nil {
		return fmt.Errorf("创建表结构失败: %w", err)
	}

	// 2. 创建索引
	log.Println("📊 创建索引...")
	if err := m.ExecuteBatch(ctx, m.GetIndexSQL()); err != nil {
		return fmt.Errorf("创建索引失败: %w", err)
	}

	// 3. 插入种子数据
	log.Println("🌱 插入种子数据...")
	if err := m.ExecuteBatch(ctx, m.GetSeedSQL()); err != nil {
		return fmt.Errorf("插入种子数据失败: %w", err)
	}

	log.Printf("✅ 快速迁移完成！总耗时: %v", time.Since(start))
	return nil
}

// getSQLSummary 获取SQL语句摘要
func getSQLSummary(sql string) string {
	lines := strings.Split(strings.TrimSpace(sql), "\n")
	firstLine := strings.TrimSpace(lines[0])
	
	if len(firstLine) > 80 {
		return firstLine[:80] + "..."
	}
	return firstLine
}