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

-- 创建通知表索引
CREATE INDEX IF NOT EXISTS idx_notifications_recipient_read ON notifications(recipient_id, is_read);
CREATE INDEX IF NOT EXISTS idx_notifications_recipient_type ON notifications(recipient_id, type);
CREATE INDEX IF NOT EXISTS idx_notifications_created_date ON notifications(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_notifications_scheduled ON notifications(scheduled_at, is_sent);
CREATE INDEX IF NOT EXISTS idx_notifications_expires ON notifications(expires_at);
CREATE INDEX IF NOT EXISTS idx_notifications_related_ticket ON notifications(related_ticket_id);

-- 创建通知偏好设置表索引
CREATE INDEX IF NOT EXISTS idx_notification_preferences_user ON notification_preferences(user_id);