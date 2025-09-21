package models

import (
	"fmt"
)

// GetCreateTableSQL 获取创建表的SQL语句
func GetCreateTableSQL() []string {
	return []string{
		CreateUsersTableSQL(),
		CreateUserProfilesTableSQL(),
		CreateCategoriesTableSQL(),
		CreateTicketsTableSQL(),
		CreateTicketCommentsTableSQL(),
		CreateTicketHistoriesTableSQL(),
		CreateOTPCodesTableSQL(),
		CreateEmailConfigsTableSQL(),
		CreateIndexesSQL(),
		CreateTriggersSQL(),
	}
}

// CreateUsersTableSQL 创建用户表
func CreateUsersTableSQL() string {
	return `
CREATE TABLE IF NOT EXISTS users (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    
    -- 基本信息
    username VARCHAR(50) UNIQUE NOT NULL,
    email VARCHAR(255) UNIQUE NOT NULL,
    phone VARCHAR(20),
    password_hash VARCHAR(255) NOT NULL,
    
    -- 个人信息
    first_name VARCHAR(50) NOT NULL,
    last_name VARCHAR(50) NOT NULL,
    display_name VARCHAR(100),
    avatar VARCHAR(500),
    bio TEXT,
    timezone VARCHAR(50) DEFAULT 'UTC',
    language VARCHAR(10) DEFAULT 'en',
    
    -- 角色和权限
    role VARCHAR(20) DEFAULT 'user' CHECK (role IN ('admin', 'agent', 'user')),
    permissions TEXT, -- JSON格式
    department VARCHAR(100),
    job_title VARCHAR(100),
    
    -- 认证信息
    email_verified BOOLEAN DEFAULT FALSE,
    email_verified_at TIMESTAMP WITH TIME ZONE,
    phone_verified BOOLEAN DEFAULT FALSE,
    phone_verified_at TIMESTAMP WITH TIME ZONE,
    two_factor_enabled BOOLEAN DEFAULT FALSE,
    two_factor_secret VARCHAR(255),
    
    -- 登录信息
    last_login_at TIMESTAMP WITH TIME ZONE,
    last_login_ip VARCHAR(45),
    login_count INTEGER DEFAULT 0,
    failed_login_attempts INTEGER DEFAULT 0,
    locked_until TIMESTAMP WITH TIME ZONE,
    
    -- 业务信息
    status VARCHAR(20) DEFAULT 'active' CHECK (status IN ('active', 'inactive', 'suspended', 'pending')),
    notes TEXT,
    tags TEXT, -- JSON格式
    metadata TEXT, -- JSON格式
    
    -- 统计信息
    tickets_created INTEGER DEFAULT 0,
    tickets_assigned INTEGER DEFAULT 0,
    tickets_resolved INTEGER DEFAULT 0,
    comments_count INTEGER DEFAULT 0,
    
    -- 通知设置
    notification_email BOOLEAN DEFAULT TRUE,
    notification_sms BOOLEAN DEFAULT FALSE,
    notification_push BOOLEAN DEFAULT TRUE,
    notification_settings TEXT -- JSON格式
);

CREATE INDEX IF NOT EXISTS idx_users_deleted_at ON users(deleted_at);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
CREATE INDEX IF NOT EXISTS idx_users_role ON users(role);
CREATE INDEX IF NOT EXISTS idx_users_status ON users(status);
CREATE INDEX IF NOT EXISTS idx_users_department ON users(department);
`
}

// CreateUserProfilesTableSQL 创建用户资料表
func CreateUserProfilesTableSQL() string {
	return `
CREATE TABLE IF NOT EXISTS user_profiles (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    
    -- 关联用户
    user_id INTEGER UNIQUE NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    
    -- 个人信息
    first_name VARCHAR(50),
    last_name VARCHAR(50),
    display_name VARCHAR(100),
    avatar_url VARCHAR(500),
    phone VARCHAR(20),
    department VARCHAR(100),
    position VARCHAR(100),
    bio TEXT,
    
    -- 偏好设置
    timezone VARCHAR(50) DEFAULT 'UTC',
    language VARCHAR(10) DEFAULT 'en'
);

CREATE INDEX IF NOT EXISTS idx_user_profiles_user_id ON user_profiles(user_id);
`
}

// CreateCategoriesTableSQL 创建分类表
func CreateCategoriesTableSQL() string {
	return `
CREATE TABLE IF NOT EXISTS categories (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    
    -- 基本信息
    name VARCHAR(100) UNIQUE NOT NULL,
    slug VARCHAR(100) UNIQUE NOT NULL,
    description TEXT,
    icon VARCHAR(50),
    color VARCHAR(20),
    
    -- 分类属性
    type VARCHAR(20) DEFAULT 'general' CHECK (type IN ('general', 'technical', 'business', 'support', 'incident', 'request')),
    status VARCHAR(20) DEFAULT 'active' CHECK (status IN ('active', 'inactive', 'archived')),
    sort_order INTEGER DEFAULT 0,
    
    -- 层级结构
    parent_id INTEGER REFERENCES categories(id),
    level INTEGER DEFAULT 0,
    path VARCHAR(500),
    
    -- 统计信息
    ticket_count INTEGER DEFAULT 0,
    active_ticket_count INTEGER DEFAULT 0,
    children_count INTEGER DEFAULT 0,
    
    -- 配置信息
    is_default BOOLEAN DEFAULT FALSE,
    is_public BOOLEAN DEFAULT TRUE,
    require_approval BOOLEAN DEFAULT FALSE,
    auto_assign_user_id INTEGER REFERENCES users(id),
    sla_hours INTEGER,
    template TEXT,
    
    -- 权限控制
    allowed_roles TEXT, -- JSON格式
    restricted_roles TEXT, -- JSON格式
    
    -- 元数据
    metadata TEXT, -- JSON格式
    tags TEXT, -- JSON格式
    
    -- 创建者信息
    created_by INTEGER NOT NULL REFERENCES users(id),
    updated_by INTEGER REFERENCES users(id)
);

CREATE INDEX IF NOT EXISTS idx_categories_deleted_at ON categories(deleted_at);
CREATE INDEX IF NOT EXISTS idx_categories_parent_id ON categories(parent_id);
CREATE INDEX IF NOT EXISTS idx_categories_status ON categories(status);
CREATE INDEX IF NOT EXISTS idx_categories_type ON categories(type);
CREATE INDEX IF NOT EXISTS idx_categories_sort_order ON categories(sort_order);
CREATE INDEX IF NOT EXISTS idx_categories_created_by ON categories(created_by);
CREATE INDEX IF NOT EXISTS idx_categories_auto_assign_user_id ON categories(auto_assign_user_id);
`
}

// CreateTicketsTableSQL 创建工单表
func CreateTicketsTableSQL() string {
	return `
CREATE TABLE IF NOT EXISTS tickets (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    
    -- 基本信息
    title VARCHAR(255) NOT NULL,
    description TEXT NOT NULL,
    ticket_number VARCHAR(50) UNIQUE NOT NULL,
    
    -- 分类和关联
    category_id INTEGER REFERENCES categories(id),
    created_by INTEGER NOT NULL REFERENCES users(id),
    assigned_to INTEGER REFERENCES users(id),
    
    -- 状态和优先级
    status VARCHAR(20) DEFAULT 'open' CHECK (status IN ('open', 'in_progress', 'pending', 'resolved', 'closed', 'cancelled')),
    priority VARCHAR(20) DEFAULT 'medium' CHECK (priority IN ('low', 'medium', 'high', 'urgent', 'critical')),
    type VARCHAR(20) DEFAULT 'general' CHECK (type IN ('general', 'bug', 'feature', 'support', 'incident', 'change')),
    source VARCHAR(20) DEFAULT 'web' CHECK (source IN ('web', 'email', 'phone', 'chat', 'api', 'mobile')),
    
    -- 时间信息
    due_date TIMESTAMP WITH TIME ZONE,
    resolved_at TIMESTAMP WITH TIME ZONE,
    closed_at TIMESTAMP WITH TIME ZONE,
    first_response_at TIMESTAMP WITH TIME ZONE,
    
    -- SLA信息
    sla_due_date TIMESTAMP WITH TIME ZONE,
    sla_breached BOOLEAN DEFAULT FALSE,
    sla_breach_reason TEXT,
    response_time_minutes INTEGER,
    resolution_time_minutes INTEGER,
    
    -- 联系信息
    contact_email VARCHAR(255),
    contact_phone VARCHAR(20),
    contact_name VARCHAR(100),
    
    -- 业务信息
    tags TEXT, -- JSON格式
    custom_fields TEXT, -- JSON格式
    metadata TEXT, -- JSON格式
    
    -- 附件和关联
    attachments TEXT, -- JSON格式
    related_tickets TEXT, -- JSON格式
    parent_ticket_id INTEGER REFERENCES tickets(id),
    
    -- 统计信息
    comments_count INTEGER DEFAULT 0,
    views_count INTEGER DEFAULT 0,
    watchers_count INTEGER DEFAULT 0,
    
    -- 评分和反馈
    satisfaction_rating INTEGER CHECK (satisfaction_rating >= 1 AND satisfaction_rating <= 5),
    satisfaction_comment TEXT,
    internal_notes TEXT,
    
    -- 时间跟踪
    estimated_hours DECIMAL(8,2),
    actual_hours DECIMAL(8,2),
    billable_hours DECIMAL(8,2),
    
    -- 升级信息
    escalation_level INTEGER DEFAULT 0,
    escalated_at TIMESTAMP WITH TIME ZONE,
    escalated_by INTEGER REFERENCES users(id),
    escalation_reason TEXT,
    
    -- 合并和拆分
    merged_into_ticket_id INTEGER REFERENCES tickets(id),
    merged_at TIMESTAMP WITH TIME ZONE,
    merged_by INTEGER REFERENCES users(id),
    split_from_ticket_id INTEGER REFERENCES tickets(id),
    
    -- 审批流程
    requires_approval BOOLEAN DEFAULT FALSE,
    approved_by INTEGER REFERENCES users(id),
    approved_at TIMESTAMP WITH TIME ZONE,
    approval_notes TEXT,
    
    -- 通知设置
    notification_sent BOOLEAN DEFAULT FALSE,
    last_notification_at TIMESTAMP WITH TIME ZONE,
    
    -- 安全信息
    is_confidential BOOLEAN DEFAULT FALSE,
    access_level VARCHAR(20) DEFAULT 'public' CHECK (access_level IN ('public', 'internal', 'confidential', 'restricted')),
    
    -- 来源信息
    source_ip VARCHAR(45),
    user_agent VARCHAR(500),
    referrer VARCHAR(500)
);

CREATE INDEX IF NOT EXISTS idx_tickets_deleted_at ON tickets(deleted_at);
CREATE INDEX IF NOT EXISTS idx_tickets_ticket_number ON tickets(ticket_number);
CREATE INDEX IF NOT EXISTS idx_tickets_status ON tickets(status);
CREATE INDEX IF NOT EXISTS idx_tickets_priority ON tickets(priority);
CREATE INDEX IF NOT EXISTS idx_tickets_type ON tickets(type);
CREATE INDEX IF NOT EXISTS idx_tickets_source ON tickets(source);
CREATE INDEX IF NOT EXISTS idx_tickets_category_id ON tickets(category_id);
CREATE INDEX IF NOT EXISTS idx_tickets_created_by ON tickets(created_by);
CREATE INDEX IF NOT EXISTS idx_tickets_assigned_to ON tickets(assigned_to);
CREATE INDEX IF NOT EXISTS idx_tickets_due_date ON tickets(due_date);
CREATE INDEX IF NOT EXISTS idx_tickets_sla_due_date ON tickets(sla_due_date);
CREATE INDEX IF NOT EXISTS idx_tickets_parent_ticket_id ON tickets(parent_ticket_id);
CREATE INDEX IF NOT EXISTS idx_tickets_escalated_by ON tickets(escalated_by);
CREATE INDEX IF NOT EXISTS idx_tickets_merged_by ON tickets(merged_by);
CREATE INDEX IF NOT EXISTS idx_tickets_approved_by ON tickets(approved_by);
`
}

// CreateTicketCommentsTableSQL 创建工单评论表
func CreateTicketCommentsTableSQL() string {
	return `
CREATE TABLE IF NOT EXISTS ticket_comments (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    
    -- 关联信息
    ticket_id INTEGER NOT NULL REFERENCES tickets(id),
    user_id INTEGER NOT NULL REFERENCES users(id),
    
    -- 评论内容
    content TEXT NOT NULL,
    content_type VARCHAR(20) DEFAULT 'text' CHECK (content_type IN ('text', 'html', 'markdown')),
    type VARCHAR(20) DEFAULT 'public' CHECK (type IN ('public', 'internal', 'system')),
    
    -- 附件和元数据
    attachments TEXT, -- JSON格式
    metadata TEXT, -- JSON格式
    source_ip VARCHAR(45),
    user_agent VARCHAR(500),
    
    -- 状态信息
    is_edited BOOLEAN DEFAULT FALSE,
    edited_at TIMESTAMP WITH TIME ZONE,
    is_deleted BOOLEAN DEFAULT FALSE,
    deleted_by INTEGER REFERENCES users(id),
    
    -- 回复相关
    parent_id INTEGER REFERENCES ticket_comments(id),
    reply_count INTEGER DEFAULT 0,
    
    -- 时间跟踪
    time_spent INTEGER, -- 分钟
    billable_time INTEGER, -- 分钟
    work_type VARCHAR(50),
    
    -- 通知相关
    notification_sent BOOLEAN DEFAULT FALSE,
    notification_at TIMESTAMP WITH TIME ZONE,
    
    -- 评分相关
    is_helpful BOOLEAN,
    helpful_count INTEGER DEFAULT 0,
    unhelpful_count INTEGER DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_ticket_comments_deleted_at ON ticket_comments(deleted_at);
CREATE INDEX IF NOT EXISTS idx_ticket_comments_ticket_id ON ticket_comments(ticket_id);
CREATE INDEX IF NOT EXISTS idx_ticket_comments_user_id ON ticket_comments(user_id);
CREATE INDEX IF NOT EXISTS idx_ticket_comments_parent_id ON ticket_comments(parent_id);
CREATE INDEX IF NOT EXISTS idx_ticket_comments_deleted_by ON ticket_comments(deleted_by);
`
}

// CreateTicketHistoriesTableSQL 创建工单历史表
func CreateTicketHistoriesTableSQL() string {
	return `
CREATE TABLE IF NOT EXISTS ticket_histories (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    
    -- 关联信息
    ticket_id INTEGER NOT NULL REFERENCES tickets(id),
    user_id INTEGER REFERENCES users(id),
    
    -- 操作信息
    action VARCHAR(50) NOT NULL,
    description TEXT NOT NULL,
    details TEXT, -- JSON格式
    
    -- 变更信息
    field_name VARCHAR(100),
    old_value TEXT,
    new_value TEXT,
    
    -- 元数据
    source_ip VARCHAR(45),
    user_agent VARCHAR(500),
    metadata TEXT, -- JSON格式
    
    -- 关联记录
    comment_id INTEGER REFERENCES ticket_comments(id),
    attachment_id INTEGER,
    
    -- 时间信息
    duration INTEGER, -- 秒
    scheduled_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    
    -- 状态信息
    is_visible BOOLEAN DEFAULT TRUE,
    is_system BOOLEAN DEFAULT FALSE,
    is_automated BOOLEAN DEFAULT FALSE,
    is_important BOOLEAN DEFAULT FALSE
);

CREATE INDEX IF NOT EXISTS idx_ticket_histories_ticket_id ON ticket_histories(ticket_id);
CREATE INDEX IF NOT EXISTS idx_ticket_histories_user_id ON ticket_histories(user_id);
CREATE INDEX IF NOT EXISTS idx_ticket_histories_action ON ticket_histories(action);
CREATE INDEX IF NOT EXISTS idx_ticket_histories_comment_id ON ticket_histories(comment_id);
CREATE INDEX IF NOT EXISTS idx_ticket_histories_attachment_id ON ticket_histories(attachment_id);
`
}

// CreateOTPCodesTableSQL 创建OTP验证码表
func CreateOTPCodesTableSQL() string {
	return `
CREATE TABLE IF NOT EXISTS otp_codes (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    
    -- 关联用户
    user_id INTEGER NOT NULL REFERENCES users(id),
    
    -- OTP信息
    code VARCHAR(20) NOT NULL,
    type VARCHAR(30) NOT NULL,
    status VARCHAR(20) DEFAULT 'pending' CHECK (status IN ('pending', 'used', 'expired', 'revoked', 'failed')),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    
    -- 发送信息
    delivery_method VARCHAR(20) NOT NULL CHECK (delivery_method IN ('email', 'sms', 'app', 'voice')),
    recipient VARCHAR(255) NOT NULL,
    sent_at TIMESTAMP WITH TIME ZONE,
    delivered_at TIMESTAMP WITH TIME ZONE,
    
    -- 验证信息
    attempts INTEGER DEFAULT 0,
    max_attempts INTEGER DEFAULT 3,
    last_attempt_at TIMESTAMP WITH TIME ZONE,
    verified_at TIMESTAMP WITH TIME ZONE,
    used_at TIMESTAMP WITH TIME ZONE,
    
    -- 安全信息
    source_ip VARCHAR(45),
    user_agent VARCHAR(500),
    session_id VARCHAR(255),
    fingerprint VARCHAR(255),
    
    -- 配置信息
    length INTEGER DEFAULT 6,
    is_numeric BOOLEAN DEFAULT TRUE,
    is_case_sensitive BOOLEAN DEFAULT FALSE,
    validity_minutes INTEGER DEFAULT 10,
    
    -- 元数据
    metadata TEXT, -- JSON格式
    purpose VARCHAR(255),
    reference_id VARCHAR(255),
    reference_type VARCHAR(50),
    
    -- 重发控制
    resend_count INTEGER DEFAULT 0,
    max_resends INTEGER DEFAULT 3,
    last_resend_at TIMESTAMP WITH TIME ZONE,
    next_resend_at TIMESTAMP WITH TIME ZONE,
    resend_interval INTEGER DEFAULT 60,
    
    -- 失败信息
    failure_reason VARCHAR(255),
    failed_at TIMESTAMP WITH TIME ZONE,
    revoked_at TIMESTAMP WITH TIME ZONE,
    revoked_by INTEGER REFERENCES users(id),
    revoke_reason VARCHAR(255)
);

CREATE INDEX IF NOT EXISTS idx_otp_codes_deleted_at ON otp_codes(deleted_at);
CREATE INDEX IF NOT EXISTS idx_otp_codes_user_id ON otp_codes(user_id);
CREATE INDEX IF NOT EXISTS idx_otp_codes_code ON otp_codes(code);
CREATE INDEX IF NOT EXISTS idx_otp_codes_type ON otp_codes(type);
CREATE INDEX IF NOT EXISTS idx_otp_codes_status ON otp_codes(status);
CREATE INDEX IF NOT EXISTS idx_otp_codes_expires_at ON otp_codes(expires_at);
CREATE INDEX IF NOT EXISTS idx_otp_codes_session_id ON otp_codes(session_id);
CREATE INDEX IF NOT EXISTS idx_otp_codes_reference_id ON otp_codes(reference_id);
CREATE INDEX IF NOT EXISTS idx_otp_codes_revoked_by ON otp_codes(revoked_by);
`
}

// CreateEmailConfigsTableSQL 创建邮箱配置表
func CreateEmailConfigsTableSQL() string {
	return `
CREATE TABLE IF NOT EXISTS email_configs (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    
    -- 邮箱验证开关
    email_verification_enabled BOOLEAN DEFAULT FALSE NOT NULL,
    
    -- SMTP配置
    smtp_host VARCHAR(255),
    smtp_port INTEGER DEFAULT 587,
    smtp_username VARCHAR(255),
    smtp_password VARCHAR(255),
    smtp_use_tls BOOLEAN DEFAULT TRUE,
    smtp_use_ssl BOOLEAN DEFAULT FALSE,
    
    -- 邮件发送配置
    from_email VARCHAR(255),
    from_name VARCHAR(255) DEFAULT '工单系统',
    
    -- 邮件模板配置
    welcome_email_subject VARCHAR(255) DEFAULT '欢迎注册工单系统',
    welcome_email_template TEXT,
    otp_email_subject VARCHAR(255) DEFAULT '邮箱验证码',
    otp_email_template TEXT,
    
    -- 配置状态
    is_active BOOLEAN DEFAULT TRUE NOT NULL,
    
    -- 最后更新者
    updated_by_id INTEGER REFERENCES users(id)
);

CREATE INDEX IF NOT EXISTS idx_email_configs_active ON email_configs(is_active);
CREATE INDEX IF NOT EXISTS idx_email_configs_verification_enabled ON email_configs(email_verification_enabled);
CREATE INDEX IF NOT EXISTS idx_email_configs_updated_by ON email_configs(updated_by_id);
`
}

// CreateIndexesSQL 创建额外的索引
func CreateIndexesSQL() string {
	return `
-- 复合索引
CREATE INDEX IF NOT EXISTS idx_tickets_status_priority ON tickets(status, priority);
CREATE INDEX IF NOT EXISTS idx_tickets_assigned_status ON tickets(assigned_to, status);
CREATE INDEX IF NOT EXISTS idx_tickets_category_status ON tickets(category_id, status);
CREATE INDEX IF NOT EXISTS idx_tickets_created_date ON tickets(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tickets_updated_date ON tickets(updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_ticket_comments_ticket_type ON ticket_comments(ticket_id, type);
CREATE INDEX IF NOT EXISTS idx_ticket_comments_created_date ON ticket_comments(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_ticket_histories_ticket_action ON ticket_histories(ticket_id, action);
CREATE INDEX IF NOT EXISTS idx_ticket_histories_created_date ON ticket_histories(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_otp_codes_user_type_status ON otp_codes(user_id, type, status);
CREATE INDEX IF NOT EXISTS idx_otp_codes_expires_status ON otp_codes(expires_at, status);

-- 全文搜索索引
CREATE INDEX IF NOT EXISTS idx_tickets_title_gin ON tickets USING gin(to_tsvector('english', title));
CREATE INDEX IF NOT EXISTS idx_tickets_description_gin ON tickets USING gin(to_tsvector('english', description));
CREATE INDEX IF NOT EXISTS idx_ticket_comments_content_gin ON ticket_comments USING gin(to_tsvector('english', content));

-- 通知表索引
CREATE INDEX IF NOT EXISTS idx_notifications_recipient_read ON notifications(recipient_id, is_read);
CREATE INDEX IF NOT EXISTS idx_notifications_recipient_type ON notifications(recipient_id, type);
CREATE INDEX IF NOT EXISTS idx_notifications_created_date ON notifications(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_notifications_scheduled ON notifications(scheduled_at, is_sent);
CREATE INDEX IF NOT EXISTS idx_notifications_expires ON notifications(expires_at);
CREATE INDEX IF NOT EXISTS idx_notifications_related_ticket ON notifications(related_ticket_id);

-- 通知偏好设置表索引
CREATE INDEX IF NOT EXISTS idx_notification_preferences_user ON notification_preferences(user_id);
`
}

// CreateTriggersSQL 创建触发器
func CreateTriggersSQL() string {
	return `
-- 更新时间戳触发器
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

-- 为所有表创建更新时间戳触发器
DROP TRIGGER IF EXISTS update_users_updated_at ON users;
CREATE TRIGGER update_users_updated_at BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_categories_updated_at ON categories;
CREATE TRIGGER update_categories_updated_at BEFORE UPDATE ON categories FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_tickets_updated_at ON tickets;
CREATE TRIGGER update_tickets_updated_at BEFORE UPDATE ON tickets FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_ticket_comments_updated_at ON ticket_comments;
CREATE TRIGGER update_ticket_comments_updated_at BEFORE UPDATE ON ticket_comments FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_ticket_histories_updated_at ON ticket_histories;
CREATE TRIGGER update_ticket_histories_updated_at BEFORE UPDATE ON ticket_histories FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_otp_codes_updated_at ON otp_codes;
CREATE TRIGGER update_otp_codes_updated_at BEFORE UPDATE ON otp_codes FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- 统计信息更新触发器
CREATE OR REPLACE FUNCTION update_ticket_stats()
RETURNS TRIGGER AS $$
BEGIN
    -- 更新分类的工单统计
    IF TG_OP = 'INSERT' THEN
        UPDATE categories SET 
            ticket_count = ticket_count + 1,
            active_ticket_count = CASE WHEN NEW.status IN ('open', 'in_progress', 'pending') THEN active_ticket_count + 1 ELSE active_ticket_count END
        WHERE id = NEW.category_id;
        
        -- 更新用户统计
        UPDATE users SET tickets_created = tickets_created + 1 WHERE id = NEW.created_by;
        IF NEW.assigned_to IS NOT NULL THEN
            UPDATE users SET tickets_assigned = tickets_assigned + 1 WHERE id = NEW.assigned_to;
        END IF;
        
        RETURN NEW;
    ELSIF TG_OP = 'UPDATE' THEN
        -- 处理分类变更
        IF OLD.category_id != NEW.category_id THEN
            UPDATE categories SET 
                ticket_count = ticket_count - 1,
                active_ticket_count = CASE WHEN OLD.status IN ('open', 'in_progress', 'pending') THEN active_ticket_count - 1 ELSE active_ticket_count END
            WHERE id = OLD.category_id;
            
            UPDATE categories SET 
                ticket_count = ticket_count + 1,
                active_ticket_count = CASE WHEN NEW.status IN ('open', 'in_progress', 'pending') THEN active_ticket_count + 1 ELSE active_ticket_count END
            WHERE id = NEW.category_id;
        END IF;
        
        -- 处理状态变更
        IF OLD.status != NEW.status THEN
            IF OLD.status IN ('open', 'in_progress', 'pending') AND NEW.status NOT IN ('open', 'in_progress', 'pending') THEN
                UPDATE categories SET active_ticket_count = active_ticket_count - 1 WHERE id = NEW.category_id;
            ELSIF OLD.status NOT IN ('open', 'in_progress', 'pending') AND NEW.status IN ('open', 'in_progress', 'pending') THEN
                UPDATE categories SET active_ticket_count = active_ticket_count + 1 WHERE id = NEW.category_id;
            END IF;
            
            -- 更新解决统计
            IF NEW.status = 'resolved' AND OLD.status != 'resolved' THEN
                IF NEW.assigned_to IS NOT NULL THEN
                    UPDATE users SET tickets_resolved = tickets_resolved + 1 WHERE id = NEW.assigned_to;
                END IF;
            END IF;
        END IF;
        
        -- 处理分配变更
        IF OLD.assigned_to != NEW.assigned_to THEN
            IF OLD.assigned_to IS NOT NULL THEN
                UPDATE users SET tickets_assigned = tickets_assigned - 1 WHERE id = OLD.assigned_to;
            END IF;
            IF NEW.assigned_to IS NOT NULL THEN
                UPDATE users SET tickets_assigned = tickets_assigned + 1 WHERE id = NEW.assigned_to;
            END IF;
        END IF;
        
        RETURN NEW;
    ELSIF TG_OP = 'DELETE' THEN
        UPDATE categories SET 
            ticket_count = ticket_count - 1,
            active_ticket_count = CASE WHEN OLD.status IN ('open', 'in_progress', 'pending') THEN active_ticket_count - 1 ELSE active_ticket_count END
        WHERE id = OLD.category_id;
        
        UPDATE users SET tickets_created = tickets_created - 1 WHERE id = OLD.created_by;
        IF OLD.assigned_to IS NOT NULL THEN
            UPDATE users SET tickets_assigned = tickets_assigned - 1 WHERE id = OLD.assigned_to;
        END IF;
        
        RETURN OLD;
    END IF;
    
    RETURN NULL;
END;
$$ language 'plpgsql';

DROP TRIGGER IF EXISTS trigger_update_ticket_stats ON tickets;
CREATE TRIGGER trigger_update_ticket_stats
    AFTER INSERT OR UPDATE OR DELETE ON tickets
    FOR EACH ROW EXECUTE FUNCTION update_ticket_stats();

-- 评论统计更新触发器
CREATE OR REPLACE FUNCTION update_comment_stats()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        UPDATE tickets SET comments_count = comments_count + 1 WHERE id = NEW.ticket_id;
        UPDATE users SET comments_count = comments_count + 1 WHERE id = NEW.user_id;
        
        -- 更新回复统计
        IF NEW.parent_id IS NOT NULL THEN
            UPDATE ticket_comments SET reply_count = reply_count + 1 WHERE id = NEW.parent_id;
        END IF;
        
        RETURN NEW;
    ELSIF TG_OP = 'DELETE' THEN
        UPDATE tickets SET comments_count = comments_count - 1 WHERE id = OLD.ticket_id;
        UPDATE users SET comments_count = comments_count - 1 WHERE id = OLD.user_id;
        
        -- 更新回复统计
        IF OLD.parent_id IS NOT NULL THEN
            UPDATE ticket_comments SET reply_count = reply_count - 1 WHERE id = OLD.parent_id;
        END IF;
        
        RETURN OLD;
    END IF;
    
    RETURN NULL;
END;
$$ language 'plpgsql';

DROP TRIGGER IF EXISTS trigger_update_comment_stats ON ticket_comments;
CREATE TRIGGER trigger_update_comment_stats
    AFTER INSERT OR DELETE ON ticket_comments
    FOR EACH ROW EXECUTE FUNCTION update_comment_stats();

-- 创建通知表
CREATE TABLE IF NOT EXISTS notifications (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    
    -- 基本信息
    type VARCHAR(50) NOT NULL,
    title VARCHAR(255) NOT NULL,
    content TEXT NOT NULL,
    priority VARCHAR(20) NOT NULL DEFAULT 'normal',
    channel VARCHAR(20) NOT NULL DEFAULT 'in_app',
    
    -- 接收者和发送者信息
    recipient_id INTEGER NOT NULL REFERENCES users(id),
    sender_id INTEGER REFERENCES users(id),
    
    -- 关联信息
    related_type VARCHAR(50),
    related_id INTEGER,
    related_ticket_id INTEGER REFERENCES tickets(id),
    
    -- 状态信息
    is_read BOOLEAN NOT NULL DEFAULT FALSE,
    read_at TIMESTAMPTZ,
    is_sent BOOLEAN NOT NULL DEFAULT FALSE,
    sent_at TIMESTAMPTZ,
    is_delivered BOOLEAN NOT NULL DEFAULT FALSE,
    delivered_at TIMESTAMPTZ,
    
    -- 重试信息
    retry_count INTEGER NOT NULL DEFAULT 0,
    last_retry_at TIMESTAMPTZ,
    next_retry_at TIMESTAMPTZ,
    max_retries INTEGER NOT NULL DEFAULT 3,
    
    -- 元数据
    metadata TEXT,
    action_url VARCHAR(500),
    expires_at TIMESTAMPTZ,
    scheduled_at TIMESTAMPTZ,
    error_message TEXT,
    delivery_status VARCHAR(50)
);

-- 创建通知偏好设置表
CREATE TABLE IF NOT EXISTS notification_preferences (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    
    user_id INTEGER NOT NULL REFERENCES users(id),
    notification_type VARCHAR(50) NOT NULL,
    email_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    in_app_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    webhook_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    
    -- 免打扰时间设置
    do_not_disturb_start TIME,
    do_not_disturb_end TIME,
    
    -- 频率控制
    max_daily_count INTEGER NOT NULL DEFAULT 50,
    batch_delivery BOOLEAN NOT NULL DEFAULT FALSE,
    batch_interval INTEGER NOT NULL DEFAULT 60, -- 分钟
    
    CONSTRAINT unique_user_notification_type UNIQUE (user_id, notification_type)
);
`
}

// GetDropTableSQL 获取删除表的SQL语句
func GetDropTableSQL() []string {
	return []string{
		"DROP TABLE IF EXISTS notification_preferences CASCADE;",
		"DROP TABLE IF EXISTS notifications CASCADE;",
		"DROP TABLE IF EXISTS email_configs CASCADE;",
		"DROP TABLE IF EXISTS otp_codes CASCADE;",
		"DROP TABLE IF EXISTS ticket_histories CASCADE;",
		"DROP TABLE IF EXISTS ticket_comments CASCADE;",
		"DROP TABLE IF EXISTS tickets CASCADE;",
		"DROP TABLE IF EXISTS categories CASCADE;",
		"DROP TABLE IF EXISTS users CASCADE;",
		"DROP FUNCTION IF EXISTS update_updated_at_column() CASCADE;",
		"DROP FUNCTION IF EXISTS update_ticket_stats() CASCADE;",
		"DROP FUNCTION IF EXISTS update_comment_stats() CASCADE;",
	}
}

// PrintMigrationInfo 打印迁移信息
func PrintMigrationInfo() {
	fmt.Println("=== 数据库迁移信息 ===")
	fmt.Println("表结构:")
	fmt.Println("1. users - 用户表")
	fmt.Println("2. categories - 分类表")
	fmt.Println("3. tickets - 工单表")
	fmt.Println("4. ticket_comments - 工单评论表")
	fmt.Println("5. ticket_histories - 工单历史表")
	fmt.Println("6. otp_codes - OTP验证码表")
	fmt.Println("")
	fmt.Println("特性:")
	fmt.Println("- 软删除支持")
	fmt.Println("- 自动时间戳更新")
	fmt.Println("- 统计信息自动维护")
	fmt.Println("- 全文搜索索引")
	fmt.Println("- 外键约束")
	fmt.Println("- 数据完整性检查")
	fmt.Println("========================")
}
