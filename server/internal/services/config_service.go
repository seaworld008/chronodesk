package services

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gongdan-system/internal/models"
)

// ConfigService 系统配置服务
type ConfigService struct {
	db          *gorm.DB
	auditLogger *log.Logger
}

// ConfigCategory 配置分类常量
const (
	CategorySystem   = "system"   // 系统基础信息
	CategorySecurity = "security" // 安全策略
	CategoryEmail    = "email"    // 邮件模板
	CategoryTicket   = "ticket"   // 工单默认配置
	CategoryNotify   = "notify"   // 系统通知
	CategoryUI       = "ui"       // 界面配置
)

// ConfigKey 预定义配置键
const (
	// 系统基础信息
	KeySystemName        = "system.name"
	KeySystemVersion     = "system.version"
	KeySystemDescription = "system.description"
	KeySystemLogo        = "system.logo"
	KeySystemCopyright   = "system.copyright"
	KeySystemTimezone    = "system.timezone"

	// 安全策略
	KeyPasswordMinLength       = "security.password_min_length"
	KeyPasswordRequireUpper    = "security.password_require_upper"
	KeyPasswordRequireLower    = "security.password_require_lower"
	KeyPasswordRequireDigit    = "security.password_require_digit"
	KeyPasswordRequireSymbol   = "security.password_require_symbol"
	KeyMaxLoginAttempts        = "security.max_login_attempts"
	KeyLoginLockDuration       = "security.login_lock_duration"
	KeySessionTimeout          = "security.session_timeout"
	KeyTwoFactorRequired       = "security.two_factor_required"
	KeyTrustedDeviceTTLHours   = "security.trusted_device_ttl_hours"
	KeyTrustedDeviceMaxPerUser = "security.trusted_device_max_per_user"

	// 邮件配置
	KeyEmailWelcomeTemplate = "email.welcome_template"
	KeyEmailResetTemplate   = "email.reset_template"
	KeyEmailTicketTemplate  = "email.ticket_template"
	KeyEmailNotifyTemplate  = "email.notify_template"

	// 工单默认配置
	KeyTicketDefaultPriority = "ticket.default_priority"
	KeyTicketDefaultType     = "ticket.default_type"
	KeyTicketAutoAssign      = "ticket.auto_assign"
	KeyTicketSLAEnabled      = "ticket.sla_enabled"

	// 系统通知
	KeyNotifyEmailEnabled     = "notify.email_enabled"
	KeyNotifyWebSocketEnabled = "notify.websocket_enabled"
	KeyNotifyInAppEnabled     = "notify.inapp_enabled"
)

// NewConfigService 创建配置服务
func NewConfigService(db *gorm.DB) *ConfigService {
	return &ConfigService{
		db:          db,
		auditLogger: log.Default(),
	}
}

// InitDefaultConfigs 初始化默认配置
func (s *ConfigService) InitDefaultConfigs() error {
	log.Println("🔧 初始化系统默认配置...")

	defaultConfigs := []models.SystemConfig{
		// 系统基础信息
		{Key: KeySystemName, Value: "工单管理系统", ValueType: "string", Description: "系统名称", Category: CategorySystem, Group: "basic"},
		{Key: KeySystemVersion, Value: "1.0.0", ValueType: "string", Description: "系统版本", Category: CategorySystem, Group: "basic"},
		{Key: KeySystemDescription, Value: "现代化的工单管理系统", ValueType: "string", Description: "系统描述", Category: CategorySystem, Group: "basic"},
		{Key: KeySystemTimezone, Value: "Asia/Shanghai", ValueType: "string", Description: "系统时区", Category: CategorySystem, Group: "basic"},

		// 安全策略
		{Key: KeyPasswordMinLength, Value: "8", ValueType: "int", Description: "密码最小长度", Category: CategorySecurity, Group: "password"},
		{Key: KeyPasswordRequireUpper, Value: "true", ValueType: "bool", Description: "密码需要大写字母", Category: CategorySecurity, Group: "password"},
		{Key: KeyPasswordRequireLower, Value: "true", ValueType: "bool", Description: "密码需要小写字母", Category: CategorySecurity, Group: "password"},
		{Key: KeyPasswordRequireDigit, Value: "true", ValueType: "bool", Description: "密码需要数字", Category: CategorySecurity, Group: "password"},
		{Key: KeyPasswordRequireSymbol, Value: "false", ValueType: "bool", Description: "密码需要特殊字符", Category: CategorySecurity, Group: "password"},
		{Key: KeyMaxLoginAttempts, Value: "5", ValueType: "int", Description: "最大登录尝试次数", Category: CategorySecurity, Group: "login"},
		{Key: KeyLoginLockDuration, Value: "300", ValueType: "int", Description: "登录锁定时长(秒)", Category: CategorySecurity, Group: "login"},
		{Key: KeySessionTimeout, Value: "3600", ValueType: "int", Description: "会话超时时长(秒)", Category: CategorySecurity, Group: "session"},
		{Key: KeyTwoFactorRequired, Value: "false", ValueType: "bool", Description: "是否强制双因子认证", Category: CategorySecurity, Group: "auth"},
		{Key: KeyTrustedDeviceTTLHours, Value: "720", ValueType: "int", Description: "可信设备有效期(小时)", Category: CategorySecurity, Group: "trusted_device"},
		{Key: KeyTrustedDeviceMaxPerUser, Value: "5", ValueType: "int", Description: "每个用户允许的可信设备数量", Category: CategorySecurity, Group: "trusted_device"},

		// 工单默认配置
		{Key: KeyTicketDefaultPriority, Value: "normal", ValueType: "string", Description: "工单默认优先级", Category: CategoryTicket, Group: "defaults"},
		{Key: KeyTicketDefaultType, Value: "general", ValueType: "string", Description: "工单默认类型", Category: CategoryTicket, Group: "defaults"},
		{Key: KeyTicketAutoAssign, Value: "false", ValueType: "bool", Description: "是否自动分配工单", Category: CategoryTicket, Group: "workflow"},
		{Key: KeyTicketSLAEnabled, Value: "true", ValueType: "bool", Description: "是否启用SLA", Category: CategoryTicket, Group: "workflow"},

		// 系统通知
		{Key: KeyNotifyEmailEnabled, Value: "true", ValueType: "bool", Description: "启用邮件通知", Category: CategoryNotify, Group: "channels"},
		{Key: KeyNotifyWebSocketEnabled, Value: "true", ValueType: "bool", Description: "启用WebSocket通知", Category: CategoryNotify, Group: "channels"},
		{Key: KeyNotifyInAppEnabled, Value: "true", ValueType: "bool", Description: "启用应用内通知", Category: CategoryNotify, Group: "channels"},
	}

	for _, config := range defaultConfigs {
		// 检查配置是否已存在
		var existing models.SystemConfig
		if err := s.db.Where("key = ?", config.Key).First(&existing).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				// 配置不存在，创建新的
				if err := s.db.Create(&config).Error; err != nil {
					log.Printf("❌ 创建默认配置失败 %s: %v", config.Key, err)
					return err
				}
				s.logConfigChange(config.Key, config.Value, "DEFAULT_CREATE")
			} else {
				log.Printf("❌ 查询配置失败: %v", err)
				return err
			}
		}
	}

	log.Println("✅ 系统默认配置初始化完成")
	return nil
}

// GetConfig 获取配置值
func (s *ConfigService) GetConfig(key string) (string, error) {
	var config models.SystemConfig
	if err := s.db.Where("key = ?", key).First(&config).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", fmt.Errorf("配置不存在: %s", key)
		}
		return "", err
	}

	return config.Value, nil
}

// GetConfigInt 获取整数类型配置
func (s *ConfigService) GetConfigInt(key string) (int, error) {
	value, err := s.GetConfig(key)
	if err != nil {
		return 0, err
	}

	var result int
	if err := json.Unmarshal([]byte(value), &result); err != nil {
		return 0, fmt.Errorf("配置值类型错误: %s", key)
	}

	return result, nil
}

// GetConfigBool 获取布尔类型配置
func (s *ConfigService) GetConfigBool(key string) (bool, error) {
	value, err := s.GetConfig(key)
	if err != nil {
		return false, err
	}

	var result bool
	if err := json.Unmarshal([]byte(value), &result); err != nil {
		return false, fmt.Errorf("配置值类型错误: %s", key)
	}

	return result, nil
}

// SetConfig 设置配置值
func (s *ConfigService) SetConfig(key, value, valueType, description, category, group string) error {
	var existingConfig models.SystemConfig
	err := s.db.Where("key = ?", key).First(&existingConfig).Error

	if err == gorm.ErrRecordNotFound {
		// 创建新配置
		config := models.SystemConfig{
			Key:         key,
			Value:       value,
			ValueType:   valueType,
			Description: description,
			Category:    category,
			Group:       group,
		}

		if err := s.db.Create(&config).Error; err != nil {
			return err
		}
		s.logConfigChange(key, value, "CREATE")
	} else if err != nil {
		return err
	} else {
		// 更新现有配置
		updates := map[string]interface{}{
			"value":       value,
			"value_type":  valueType,
			"description": description,
		}
		if category != "" {
			updates["category"] = category
		}
		if group != "" {
			updates["group"] = group
		}

		if err := s.db.Model(&existingConfig).Updates(updates).Error; err != nil {
			return err
		}
		s.logConfigChange(key, value, "UPDATE")
	}

	return nil
}

// DeleteConfig 删除配置
func (s *ConfigService) DeleteConfig(key string) error {
	if err := s.db.Where("key = ?", key).Delete(&models.SystemConfig{}).Error; err != nil {
		return err
	}

	// 记录配置变更日志
	s.logConfigChange(key, "", "DELETE")

	return nil
}

// GetConfigsByCategory 按分类获取配置
func (s *ConfigService) GetConfigsByCategory(category string) ([]models.SystemConfig, error) {
	var configs []models.SystemConfig
	if err := s.db.Where("category = ?", category).Order("\"group\", key").Find(&configs).Error; err != nil {
		return nil, err
	}

	return configs, nil
}

// GetAllConfigs 获取所有配置
func (s *ConfigService) GetAllConfigs() ([]models.SystemConfig, error) {
	var configs []models.SystemConfig
	if err := s.db.Order("category, \"group\", key").Find(&configs).Error; err != nil {
		return nil, err
	}

	return configs, nil
}

// BatchUpdateConfigs 批量更新配置
func (s *ConfigService) BatchUpdateConfigs(configs []models.SystemConfig) error {
	changes := make([]configAuditChange, 0, len(configs))
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		for _, config := range configs {
			var existingConfig models.SystemConfig
			err := tx.Where("key = ?", config.Key).First(&existingConfig).Error

			operation := "BATCH_UPDATE"
			if err == gorm.ErrRecordNotFound {
				if err := tx.Create(&config).Error; err != nil {
					return err
				}
				operation = "BATCH_CREATE"
			} else if err != nil {
				return err
			} else {
				updates := map[string]interface{}{
					"value":       config.Value,
					"value_type":  config.ValueType,
					"description": config.Description,
				}
				if config.Category != "" {
					updates["category"] = config.Category
				}
				if config.Group != "" {
					updates["group"] = config.Group
				}
				if err := tx.Model(&existingConfig).Updates(updates).Error; err != nil {
					return err
				}
			}

			changes = append(changes, configAuditChange{
				key:       config.Key,
				value:     config.Value,
				operation: operation,
			})
		}
		return nil
	}); err != nil {
		return err
	}
	s.logCommittedConfigChanges(changes)
	return nil
}

type configAuditChange struct {
	key       string
	value     string
	operation string
}

func (s *ConfigService) logCommittedConfigChanges(changes []configAuditChange) {
	for _, change := range changes {
		s.logConfigChange(change.key, change.value, change.operation)
	}
}

// logConfigChange 记录配置变更日志
func (s *ConfigService) logConfigChange(key, value, operation string) {
	digest := sha256.Sum256([]byte(value))
	logger := s.auditLogger
	if logger == nil {
		logger = log.Default()
	}
	logger.Printf(
		"配置变更：operation=%s key=%s value_sha256=%x",
		operation,
		key,
		digest,
	)
}

// ExportConfigs 导出配置到JSON
func (s *ConfigService) ExportConfigs(category string) ([]byte, error) {
	var configs []models.SystemConfig

	query := s.db.Order("category, \"group\", key")
	if category != "" {
		query = query.Where("category = ?", category)
	}

	if err := query.Find(&configs).Error; err != nil {
		return nil, err
	}

	return json.MarshalIndent(configs, "", "  ")
}

// ImportConfigs 从JSON导入配置
func (s *ConfigService) ImportConfigs(data []byte) error {
	var configs []models.SystemConfig
	if err := json.Unmarshal(data, &configs); err != nil {
		return fmt.Errorf("JSON格式错误: %v", err)
	}

	changes := make([]configAuditChange, 0, len(configs))
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		for _, config := range configs {
			if err := tx.Save(&config).Error; err != nil {
				return fmt.Errorf("导入配置失败 %s: %v", config.Key, err)
			}
			changes = append(changes, configAuditChange{
				key:       config.Key,
				value:     config.Value,
				operation: "IMPORT",
			})
		}
		return nil
	}); err != nil {
		return err
	}

	s.logCommittedConfigChanges(changes)
	log.Printf("✅ 成功导入 %d 个配置项", len(configs))
	return nil
}

// ValidateConfig 验证配置值
func (s *ConfigService) ValidateConfig(key, value, valueType string) error {
	switch valueType {
	case "int":
		var intValue int
		if err := json.Unmarshal([]byte(value), &intValue); err != nil {
			return fmt.Errorf("配置值必须是整数: %s", key)
		}
	case "bool":
		var boolValue bool
		if err := json.Unmarshal([]byte(value), &boolValue); err != nil {
			return fmt.Errorf("配置值必须是布尔值: %s", key)
		}
	case "json":
		var jsonValue interface{}
		if err := json.Unmarshal([]byte(value), &jsonValue); err != nil {
			return fmt.Errorf("配置值必须是有效JSON: %s", key)
		}
	case "string":
		// 字符串类型无需验证
	default:
		return fmt.Errorf("不支持的配置值类型: %s", valueType)
	}

	return nil
}

// GetSecurityPolicy 获取安全策略配置
func (s *ConfigService) GetSecurityPolicy() (*gin.H, error) {
	policy := gin.H{}

	// 密码策略
	minLength, _ := s.GetConfigInt(KeyPasswordMinLength)
	requireUpper, _ := s.GetConfigBool(KeyPasswordRequireUpper)
	requireLower, _ := s.GetConfigBool(KeyPasswordRequireLower)
	requireDigit, _ := s.GetConfigBool(KeyPasswordRequireDigit)
	requireSymbol, _ := s.GetConfigBool(KeyPasswordRequireSymbol)

	policy["password_policy"] = gin.H{
		"min_length":     minLength,
		"require_upper":  requireUpper,
		"require_lower":  requireLower,
		"require_digit":  requireDigit,
		"require_symbol": requireSymbol,
	}

	// 登录策略
	maxAttempts, _ := s.GetConfigInt(KeyMaxLoginAttempts)
	lockDuration, _ := s.GetConfigInt(KeyLoginLockDuration)
	sessionTimeout, _ := s.GetConfigInt(KeySessionTimeout)
	twoFactorRequired, _ := s.GetConfigBool(KeyTwoFactorRequired)

	policy["login_policy"] = gin.H{
		"max_attempts":        maxAttempts,
		"lock_duration":       lockDuration,
		"session_timeout":     sessionTimeout,
		"two_factor_required": twoFactorRequired,
	}

	return &policy, nil
}
