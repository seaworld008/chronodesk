package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/observability"
	"github.com/seaworld008/chronodesk/server/internal/version"
)

var (
	// ErrInvalidSystemConfigKey is stable so every transport can map the shared
	// key invariant without depending on a persistence error.
	ErrInvalidSystemConfigKey = errors.New("配置键无效")
	// ErrProtectedSystemConfigKey prevents the generic configuration surface
	// from bypassing migration-owned identity or audited runtime-control
	// workflows. It wraps ErrInvalidSystemConfigKey so existing transports fail
	// closed while callers can still match this sentinel.
	ErrProtectedSystemConfigKey = fmt.Errorf(
		"%w: 受保护系统配置必须通过专用流程修改",
		ErrInvalidSystemConfigKey,
	)
	ErrConfigExportTooLarge = errors.New("系统配置导出超过大小限制")
)

const (
	MaxConfigExportRecords = 10_000
	MaxConfigExportBytes   = 10 << 20
)

// ValidateSystemConfigKey enforces the persisted SystemConfig key contract.
// Keys are Unicode data: they are not normalized or restricted to ASCII.
func ValidateSystemConfigKey(key string) error {
	if !utf8.ValidString(key) {
		return ErrInvalidSystemConfigKey
	}
	codePoints := utf8.RuneCountInString(key)
	if codePoints < 1 || codePoints > 100 ||
		strings.TrimSpace(key) != key {
		return ErrInvalidSystemConfigKey
	}
	for _, char := range key {
		if unicode.IsControl(char) {
			return ErrInvalidSystemConfigKey
		}
	}
	return nil
}

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
	KeySystemVersion     = models.SystemConfigKeySystemVersion
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

	// Agent 全局安全控制由 agentplatform.RuntimeControl 独占修改。它们仍存放
	// 在 system_configs 中以支持持久化恢复，但不是通用系统配置。
	KeyAgentGlobalReadOnly = "agent.global_read_only"
	KeyAgentEmergencyStop  = "agent.emergency_stop"
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

	defaults := models.DefaultSystemConfigs(version.Version)
	if err := validateSystemConfigKeys(defaults); err != nil {
		return err
	}
	created := make([]models.SystemConfig, 0)
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		for _, config := range defaults {
			result := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "key"}},
				DoNothing: true,
			}).Create(&config)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 1 {
				created = append(created, config)
			}
		}
		return nil
	}); err != nil {
		log.Print("❌ 创建默认配置失败：reason=persistence_error")
		return err
	}
	for _, config := range created {
		s.logConfigChange(config.Key, config.Value, "DEFAULT_CREATE")
	}

	log.Println("✅ 系统默认配置初始化完成")
	return nil
}

// GetConfig 获取配置值
func (s *ConfigService) GetConfig(key string) (string, error) {
	if err := validateMutableSystemConfigKey(key); err != nil {
		return "", err
	}
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
	if err := validateMutableSystemConfigKey(key); err != nil {
		return err
	}
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
	if err := validateMutableSystemConfigKey(key); err != nil {
		return err
	}
	if err := s.db.Where("key = ?", key).Delete(&models.SystemConfig{}).Error; err != nil {
		return err
	}

	// 记录配置变更日志
	s.logConfigChange(key, "", "DELETE")

	return nil
}

// ListConfigPage returns one bounded page of platform configuration. Values
// remain editable control data, so the platform authorization middleware must
// run before this service is reached.
func (s *ConfigService) ListConfigPage(
	ctx context.Context,
	category string,
	request DirectoryPageRequest,
) (*DirectoryPage[models.SystemConfig], error) {
	sortFields := map[string]struct{}{
		"created_at": {},
		"updated_at": {},
		"key":        {},
		"category":   {},
		"group":      {},
	}
	if err := validateDirectoryPageRequest(request, sortFields); err != nil {
		return nil, err
	}
	if category != "" && !validSystemConfigCategory(category) {
		return nil, ErrDirectoryListQuery
	}
	query := editableSystemConfigs(
		s.db.WithContext(ctx).Model(&models.SystemConfig{}),
	)
	if category != "" {
		query = query.Where("category = ?", category)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count system configs: %w", err)
	}
	configs := make([]models.SystemConfig, 0, request.PageSize)
	if err := query.
		Order(systemConfigDirectoryOrder(request)).
		Offset(directoryPageOffset(request)).
		Limit(request.PageSize).
		Find(&configs).Error; err != nil {
		return nil, fmt.Errorf("list system configs: %w", err)
	}
	return &DirectoryPage[models.SystemConfig]{
		Items:      configs,
		Total:      total,
		Page:       request.Page,
		PageSize:   request.PageSize,
		TotalPages: directoryTotalPages(total, request.PageSize),
	}, nil
}

func validSystemConfigCategory(category string) bool {
	switch category {
	case CategorySystem,
		CategorySecurity,
		CategoryEmail,
		CategoryTicket,
		CategoryNotify,
		CategoryUI:
		return true
	default:
		return false
	}
}

func systemConfigDirectoryOrder(request DirectoryPageRequest) string {
	if request.SortBy == "category" && request.SortOrder == "asc" {
		return "category ASC, \"group\" ASC, key ASC, id ASC"
	}
	if request.SortBy == "group" && request.SortOrder == "asc" {
		return "\"group\" ASC, key ASC, id ASC"
	}
	direction := "ASC"
	if request.SortOrder == "desc" {
		direction = "DESC"
	}
	column := map[string]string{
		"created_at": "created_at",
		"updated_at": "updated_at",
		"key":        "key",
		"category":   "category",
		"group":      "\"group\"",
	}[request.SortBy]
	return column + " " + direction + ", id " + direction
}

// BatchUpdateConfigs 批量更新配置
func (s *ConfigService) BatchUpdateConfigs(configs []models.SystemConfig) error {
	if err := validateMutableSystemConfigKeys(configs); err != nil {
		return err
	}
	changes := make([]configAuditChange, 0, len(configs))
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		for _, config := range configs {
			var existingConfig models.SystemConfig
			err := tx.Where("key = ?", config.Key).First(&existingConfig).Error

			operation := "BATCH_UPDATE"
			if err == gorm.ErrRecordNotFound {
				created := mutableSystemConfigFromInput(config)
				if err := tx.Create(&created).Error; err != nil {
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
	digestHex := observability.SafeLogValue(hex.EncodeToString(digest[:]))
	logger := s.auditLogger
	if logger == nil {
		logger = log.Default()
	}
	logger.Printf(
		"配置变更：operation=%s key=%s value_sha256=%s",
		observability.SafeLogValue(operation),
		observability.SafeLogValue(key),
		digestHex,
	)
}

// ExportConfigs 导出配置到JSON
func (s *ConfigService) ExportConfigs(category string) ([]byte, error) {
	configs := make([]models.SystemConfig, 0, MaxConfigExportRecords+1)

	query := editableSystemConfigs(s.db).
		Order("category ASC, \"group\" ASC, key ASC, id ASC").
		Limit(MaxConfigExportRecords + 1)
	if category != "" {
		query = query.Where("category = ?", category)
	}

	if err := query.Find(&configs).Error; err != nil {
		return nil, err
	}
	if len(configs) > MaxConfigExportRecords {
		return nil, fmt.Errorf(
			"%w: record limit is %d",
			ErrConfigExportTooLarge,
			MaxConfigExportRecords,
		)
	}

	data, err := json.MarshalIndent(configs, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(data) > MaxConfigExportBytes {
		return nil, fmt.Errorf(
			"%w: byte limit is %d",
			ErrConfigExportTooLarge,
			MaxConfigExportBytes,
		)
	}
	return data, nil
}

// ImportConfigs 从JSON导入配置
func (s *ConfigService) ImportConfigs(data []byte) error {
	var configs []models.SystemConfig
	if err := json.Unmarshal(data, &configs); err != nil {
		return fmt.Errorf("JSON格式错误: %v", err)
	}
	if err := validateMutableSystemConfigKeys(configs); err != nil {
		return err
	}

	changes := make([]configAuditChange, 0, len(configs))
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		for _, config := range configs {
			if err := importMutableSystemConfigTx(tx, config); err != nil {
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
	if err := validateMutableSystemConfigKey(key); err != nil {
		return err
	}
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

func validateSystemConfigKeys(configs []models.SystemConfig) error {
	for _, config := range configs {
		if err := ValidateSystemConfigKey(config.Key); err != nil {
			return err
		}
	}
	return nil
}

func validateMutableSystemConfigKey(key string) error {
	if err := ValidateSystemConfigKey(key); err != nil {
		return err
	}
	if isProtectedSystemConfigKey(key) {
		return ErrProtectedSystemConfigKey
	}
	return nil
}

func validateMutableSystemConfigKeys(configs []models.SystemConfig) error {
	for _, config := range configs {
		if err := validateMutableSystemConfigKey(config.Key); err != nil {
			return err
		}
	}
	return nil
}

func isProtectedSystemConfigKey(key string) bool {
	if strings.HasPrefix(key, adminResourceVersionKeyPrefix) {
		return true
	}
	switch key {
	case KeySystemVersion, KeyAgentGlobalReadOnly, KeyAgentEmergencyStop:
		return true
	default:
		return false
	}
}

func editableSystemConfigs(query *gorm.DB) *gorm.DB {
	return query.Where(
		"key NOT IN ?",
		[]string{
			KeySystemVersion,
			KeyAgentGlobalReadOnly,
			KeyAgentEmergencyStop,
		},
	).Where(
		"SUBSTR(key, 1, ?) <> ?",
		len(adminResourceVersionKeyPrefix),
		adminResourceVersionKeyPrefix,
	)
}

func mutableSystemConfigFromInput(
	config models.SystemConfig,
) models.SystemConfig {
	return models.SystemConfig{
		Key:          config.Key,
		Value:        config.Value,
		ValueType:    config.ValueType,
		Description:  config.Description,
		Category:     config.Category,
		Group:        config.Group,
		IsRequired:   config.IsRequired,
		IsActive:     config.IsActive,
		DefaultValue: config.DefaultValue,
		MinValue:     config.MinValue,
		MaxValue:     config.MaxValue,
		ValidValues:  config.ValidValues,
	}
}

func importMutableSystemConfigTx(
	tx *gorm.DB,
	input models.SystemConfig,
) error {
	if tx == nil {
		return errors.New("配置导入事务不可用")
	}
	if err := validateMutableSystemConfigKey(input.Key); err != nil {
		return err
	}
	var existing models.SystemConfig
	err := tx.Where("key = ?", input.Key).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		created := mutableSystemConfigFromInput(input)
		return tx.Create(&created).Error
	}
	if err != nil {
		return err
	}
	safe := mutableSystemConfigFromInput(input)
	return tx.Model(&existing).Updates(map[string]any{
		"value":         safe.Value,
		"value_type":    safe.ValueType,
		"description":   safe.Description,
		"category":      safe.Category,
		"group":         safe.Group,
		"is_required":   safe.IsRequired,
		"is_active":     safe.IsActive,
		"default_value": safe.DefaultValue,
		"min_value":     safe.MinValue,
		"max_value":     safe.MaxValue,
		"valid_values":  safe.ValidValues,
	}).Error
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
