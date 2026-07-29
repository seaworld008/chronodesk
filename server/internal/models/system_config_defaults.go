package models

// DefaultSystemConfigs returns the required configuration catalog for a fresh
// installation. Callers may safely mutate the returned slice.
func DefaultSystemConfigs(appVersion string) []SystemConfig {
	defaults := []SystemConfig{
		{Key: "system.name", Value: "ChronoDesk", ValueType: "string", Description: "系统名称", Category: "system", Group: "basic"},
		{Key: "system.version", Value: appVersion, ValueType: "string", Description: "系统版本", Category: "system", Group: "basic"},
		{Key: "system.description", Value: "AI Agent 原生工单自动化平台", ValueType: "string", Description: "系统描述", Category: "system", Group: "basic"},
		{Key: "system.timezone", Value: "Asia/Shanghai", ValueType: "string", Description: "系统时区", Category: "system", Group: "basic"},

		{Key: "security.password_min_length", Value: "8", ValueType: "int", Description: "密码最小长度", Category: "security", Group: "password"},
		{Key: "security.password_require_upper", Value: "true", ValueType: "bool", Description: "密码需要大写字母", Category: "security", Group: "password"},
		{Key: "security.password_require_lower", Value: "true", ValueType: "bool", Description: "密码需要小写字母", Category: "security", Group: "password"},
		{Key: "security.password_require_digit", Value: "true", ValueType: "bool", Description: "密码需要数字", Category: "security", Group: "password"},
		{Key: "security.password_require_symbol", Value: "true", ValueType: "bool", Description: "密码需要特殊字符", Category: "security", Group: "password"},
		{Key: "security.max_login_attempts", Value: "5", ValueType: "int", Description: "最大登录尝试次数", Category: "security", Group: "login"},
		{Key: "security.login_lock_duration", Value: "300", ValueType: "int", Description: "登录锁定时长（秒）", Category: "security", Group: "login"},
		{Key: "security.session_timeout", Value: "3600", ValueType: "int", Description: "会话超时时长（秒）", Category: "security", Group: "session"},
		{Key: "security.two_factor_required", Value: "false", ValueType: "bool", Description: "是否强制双因子认证", Category: "security", Group: "auth"},
		{Key: "security.trusted_device_ttl_hours", Value: "720", ValueType: "int", Description: "可信设备有效期（小时）", Category: "security", Group: "trusted_device"},
		{Key: "security.trusted_device_max_per_user", Value: "5", ValueType: "int", Description: "每个用户允许的可信设备数量", Category: "security", Group: "trusted_device"},

		{Key: "ticket.default_priority", Value: "normal", ValueType: "string", Description: "工单默认优先级", Category: "ticket", Group: "defaults"},
		{Key: "ticket.default_type", Value: "general", ValueType: "string", Description: "工单默认类型", Category: "ticket", Group: "defaults"},
		{Key: "ticket.auto_assign", Value: "false", ValueType: "bool", Description: "是否自动分配工单", Category: "ticket", Group: "workflow"},
		{Key: "ticket.sla_enabled", Value: "true", ValueType: "bool", Description: "是否启用 SLA", Category: "ticket", Group: "workflow"},

		{Key: "notify.email_enabled", Value: "true", ValueType: "bool", Description: "启用邮件通知", Category: "notify", Group: "channels"},
		{Key: "notify.websocket_enabled", Value: "true", ValueType: "bool", Description: "启用 WebSocket 通知", Category: "notify", Group: "channels"},
		{Key: "notify.inapp_enabled", Value: "true", ValueType: "bool", Description: "启用应用内通知", Category: "notify", Group: "channels"},
	}
	for index := range defaults {
		defaults[index].DefaultValue = defaults[index].Value
		defaults[index].IsRequired = true
		defaults[index].IsActive = true
		defaults[index].Version = 1
	}
	return defaults
}
