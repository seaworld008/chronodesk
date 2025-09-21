-- 创建cleanup_logs表的SQL脚本
CREATE TABLE IF NOT EXISTS cleanup_logs (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    task_type VARCHAR(50) NOT NULL,
    status VARCHAR(20) NOT NULL,
    start_time TIMESTAMPTZ NOT NULL,
    end_time TIMESTAMPTZ,
    duration BIGINT,
    records_processed INT DEFAULT 0,
    records_deleted INT DEFAULT 0,
    error_message TEXT,
    retention_days INT,
    cutoff_date TIMESTAMPTZ,
    trigger_type VARCHAR(20),
    trigger_by BIGINT REFERENCES users(id)
);

-- 创建相关索引
CREATE INDEX IF NOT EXISTS idx_cleanup_logs_task_type ON cleanup_logs(task_type);
CREATE INDEX IF NOT EXISTS idx_cleanup_logs_status ON cleanup_logs(status);
CREATE INDEX IF NOT EXISTS idx_cleanup_logs_start_time ON cleanup_logs(start_time);
CREATE INDEX IF NOT EXISTS idx_cleanup_logs_trigger_type ON cleanup_logs(trigger_type);
CREATE INDEX IF NOT EXISTS idx_cleanup_logs_trigger_by ON cleanup_logs(trigger_by);
CREATE INDEX IF NOT EXISTS idx_cleanup_logs_task_status ON cleanup_logs(task_type, status);

-- 检查表是否创建成功
SELECT 
    tablename, 
    schemaname 
FROM pg_tables 
WHERE tablename IN ('cleanup_logs', 'system_configs')
ORDER BY tablename;