package services

import (
	"context"
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"
	"gongdan-system/internal/models"
)

// CleanupService 数据清理服务
type CleanupService struct {
	db *gorm.DB
}

// NewCleanupService 创建数据清理服务
func NewCleanupService(db *gorm.DB) *CleanupService {
	return &CleanupService{
		db: db,
	}
}

// CleanupTask 清理任务接口
type CleanupTask interface {
	GetName() string
	Execute(ctx context.Context, config *models.CleanupConfig) (*CleanupResult, error)
}

// CleanupResult 清理结果
type CleanupResult struct {
	TaskType         string
	RecordsProcessed int
	RecordsDeleted   int
	ErrorMessage     string
	StartTime        time.Time
	EndTime          time.Time
}

// GetDuration 获取持续时间（毫秒）
func (cr *CleanupResult) GetDuration() int64 {
	return cr.EndTime.Sub(cr.StartTime).Milliseconds()
}

// LoginHistoryCleanupTask 登录历史清理任务
type LoginHistoryCleanupTask struct {
	db *gorm.DB
}

// GetName 获取任务名称
func (t *LoginHistoryCleanupTask) GetName() string {
	return "login_history"
}

// Execute 执行清理任务（优化版本）
func (t *LoginHistoryCleanupTask) Execute(ctx context.Context, config *models.CleanupConfig) (*CleanupResult, error) {
	result := &CleanupResult{
		TaskType:  t.GetName(),
		StartTime: time.Now(),
	}

	// 计算截止日期
	cutoffDate := time.Now().AddDate(0, 0, -config.LoginHistoryRetentionDays)
	log.Printf("🧹 开始清理登录历史记录 - 删除 %v 之前的记录 (保留 %d 天)", 
		cutoffDate.Format("2006-01-02 15:04:05"), config.LoginHistoryRetentionDays)

	// 计算要删除的记录数
	var totalCount int64
	err := t.db.WithContext(ctx).Model(&models.LoginHistory{}).
		Where("login_time < ?", cutoffDate).
		Count(&totalCount).Error
	if err != nil {
		result.EndTime = time.Now()
		result.ErrorMessage = fmt.Sprintf("failed to count records: %v", err)
		return result, err
	}

	result.RecordsProcessed = int(totalCount)
	log.Printf("📊 发现 %d 条登录历史记录需要清理", totalCount)

	if totalCount == 0 {
		result.EndTime = time.Now()
		log.Println("✅ 没有需要清理的登录历史记录")
		return result, nil
	}

	// 安全检查：防止意外删除所有记录
	if config.LoginHistoryRetentionDays < 1 {
		result.EndTime = time.Now()
		result.ErrorMessage = "retention days must be at least 1"
		return result, fmt.Errorf("invalid retention days: %d", config.LoginHistoryRetentionDays)
	}

	// 分批删除以避免长时间锁定表
	batchSize := config.MaxRecordsPerCleanup
	if batchSize <= 0 || batchSize > 10000 {
		batchSize = 1000 // 默认批次大小
	}

	deletedCount := 0
	for deletedCount < int(totalCount) {
		select {
		case <-ctx.Done():
			result.EndTime = time.Now()
			result.RecordsDeleted = deletedCount
			result.ErrorMessage = "cleanup was cancelled"
			return result, ctx.Err()
		default:
		}

		// 获取要删除的记录ID
		var ids []uint
		err := t.db.WithContext(ctx).Model(&models.LoginHistory{}).
			Select("id").
			Where("login_time < ?", cutoffDate).
			Limit(batchSize).
			Pluck("id", &ids).Error
		
		if err != nil {
			result.EndTime = time.Now()
			result.RecordsDeleted = deletedCount
			result.ErrorMessage = fmt.Sprintf("failed to get record IDs: %v", err)
			return result, err
		}

		if len(ids) == 0 {
			break
		}

		// 删除这批记录
		deleteResult := t.db.WithContext(ctx).
			Where("id IN ?", ids).
			Delete(&models.LoginHistory{})
		
		if deleteResult.Error != nil {
			result.EndTime = time.Now()
			result.RecordsDeleted = deletedCount
			result.ErrorMessage = fmt.Sprintf("failed to delete records: %v", deleteResult.Error)
			return result, deleteResult.Error
		}

		batchDeleted := int(deleteResult.RowsAffected)
		deletedCount += batchDeleted
		
		log.Printf("🗑️  已删除 %d 条记录 (进度: %d/%d, %.1f%%)", 
			batchDeleted, deletedCount, int(totalCount), float64(deletedCount)/float64(totalCount)*100)

		// 短暂休息以减少数据库压力
		if deletedCount < int(totalCount) {
			time.Sleep(100 * time.Millisecond)
		}
	}

	result.RecordsDeleted = deletedCount
	result.EndTime = time.Now()
	
	log.Printf("✅ 登录历史清理完成: 删除了 %d 条记录，耗时 %v", 
		deletedCount, result.EndTime.Sub(result.StartTime))

	return result, nil
}

// GetCleanupConfig 获取清理配置
func (s *CleanupService) GetCleanupConfig(ctx context.Context) (*models.CleanupConfig, error) {
	var config models.SystemConfig
	err := s.db.WithContext(ctx).
		Where("key = ? AND category = ? AND is_active = ?", "cleanup", "system", true).
		First(&config).Error
	
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			// 返回默认配置
			return models.GetDefaultCleanupConfig(), nil
		}
		return nil, fmt.Errorf("failed to get cleanup config: %w", err)
	}

	var cleanupConfig models.CleanupConfig
	if err := config.GetJSONValue(&cleanupConfig); err != nil {
		log.Printf("Warning: failed to parse cleanup config, using defaults: %v", err)
		return models.GetDefaultCleanupConfig(), nil
	}

	// 验证配置
	if cleanupConfig.LoginHistoryRetentionDays < 1 {
		cleanupConfig.LoginHistoryRetentionDays = 30
	}
	if cleanupConfig.MaxRecordsPerCleanup < 100 {
		cleanupConfig.MaxRecordsPerCleanup = 1000
	}
	if cleanupConfig.CleanupSchedule == "" {
		cleanupConfig.CleanupSchedule = "0 2 * * *"
	}

	return &cleanupConfig, nil
}

// SetCleanupConfig 设置清理配置
func (s *CleanupService) SetCleanupConfig(ctx context.Context, config *models.CleanupConfig, userID uint) error {
	// 验证配置
	if config.LoginHistoryRetentionDays < 1 || config.LoginHistoryRetentionDays > 365 {
		return fmt.Errorf("retention days must be between 1 and 365")
	}
	if config.MaxRecordsPerCleanup < 100 || config.MaxRecordsPerCleanup > 10000 {
		return fmt.Errorf("max records per cleanup must be between 100 and 10000")
	}

	// 查找现有配置
	var existingConfig models.SystemConfig
	err := s.db.WithContext(ctx).
		Where("key = ? AND category = ?", "cleanup", "system").
		First(&existingConfig).Error

	if err != nil && err != gorm.ErrRecordNotFound {
		return fmt.Errorf("failed to check existing config: %w", err)
	}

	if err == gorm.ErrRecordNotFound {
		// 创建新配置
		newConfig := models.SystemConfig{
			Key:         "cleanup",
			Category:    "system",
			Group:       "cleanup",
			Description: "系统数据清理配置",
			IsRequired:  true,
			IsActive:    true,
			UpdatedBy:   &userID,
		}

		if err := newConfig.SetValue(config); err != nil {
			return fmt.Errorf("failed to set config value: %w", err)
		}

		if err := s.db.WithContext(ctx).Create(&newConfig).Error; err != nil {
			return fmt.Errorf("failed to create config: %w", err)
		}
	} else {
		// 更新现有配置
		if err := existingConfig.SetValue(config); err != nil {
			return fmt.Errorf("failed to set config value: %w", err)
		}

		existingConfig.UpdatedBy = &userID
		existingConfig.Version++

		if err := s.db.WithContext(ctx).Save(&existingConfig).Error; err != nil {
			return fmt.Errorf("failed to update config: %w", err)
		}
	}

	return nil
}

// ExecuteCleanup 执行清理任务（优化版本）
func (s *CleanupService) ExecuteCleanup(ctx context.Context, taskType string, triggerType string, userID *uint) error {
	// 获取清理配置
	config, err := s.GetCleanupConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cleanup config: %w", err)
	}

	if !config.CleanupEnabled {
		log.Println("Cleanup is disabled, skipping")
		return nil
	}

	// 创建清理日志
	cleanupLog := &models.CleanupLog{
		TaskType:      taskType,
		Status:        "started",
		StartTime:     time.Now(),
		RetentionDays: config.LoginHistoryRetentionDays,
		CutoffDate:    time.Now().AddDate(0, 0, -config.LoginHistoryRetentionDays),
		TriggerType:   triggerType,
		TriggerBy:     userID,
	}

	if err := s.db.WithContext(ctx).Create(cleanupLog).Error; err != nil {
		return fmt.Errorf("failed to create cleanup log: %w", err)
	}

	// 选择对应的清理任务
	var task CleanupTask
	switch taskType {
	case "login_history":
		task = &LoginHistoryCleanupTask{db: s.db}
	default:
		// 更新日志状态为失败
		cleanupLog.Status = "failed"
		cleanupLog.ErrorMessage = fmt.Sprintf("unknown task type: %s", taskType)
		endTime := time.Now()
		cleanupLog.EndTime = &endTime
		duration := endTime.Sub(cleanupLog.StartTime).Milliseconds()
		cleanupLog.Duration = &duration
		s.db.WithContext(ctx).Save(cleanupLog)
		return fmt.Errorf("unknown task type: %s", taskType)
	}

	// 执行清理任务
	result, err := task.Execute(ctx, config)
	
	// 更新清理日志
	cleanupLog.RecordsProcessed = result.RecordsProcessed
	cleanupLog.RecordsDeleted = result.RecordsDeleted
	cleanupLog.EndTime = &result.EndTime
	duration := result.GetDuration()
	cleanupLog.Duration = &duration

	if err != nil {
		cleanupLog.Status = "failed"
		cleanupLog.ErrorMessage = result.ErrorMessage
	} else {
		cleanupLog.Status = "completed"
	}

	if updateErr := s.db.WithContext(ctx).Save(cleanupLog).Error; updateErr != nil {
		log.Printf("Warning: failed to update cleanup log: %v", updateErr)
	}

	return err
}

// ExecuteAllCleanupTasks 执行所有清理任务
func (s *CleanupService) ExecuteAllCleanupTasks(ctx context.Context, triggerType string, userID *uint) error {
	taskTypes := []string{"login_history"} // 可以扩展其他任务类型
	
	var lastError error
	successCount := 0
	
	for _, taskType := range taskTypes {
		if err := s.ExecuteCleanup(ctx, taskType, triggerType, userID); err != nil {
			log.Printf("Failed to execute cleanup task %s: %v", taskType, err)
			lastError = err
		} else {
			successCount++
		}
	}

	if successCount == 0 && lastError != nil {
		return fmt.Errorf("all cleanup tasks failed, last error: %w", lastError)
	}

	log.Printf("Completed %d/%d cleanup tasks", successCount, len(taskTypes))
	return nil
}

// GetCleanupLogs 获取清理日志
func (s *CleanupService) GetCleanupLogs(ctx context.Context, taskType string, limit int) ([]*models.CleanupLogResponse, error) {
	query := s.db.WithContext(ctx).Model(&models.CleanupLog{})
	
	if taskType != "" {
		query = query.Where("task_type = ?", taskType)
	}

	if limit <= 0 || limit > 100 {
		limit = 20
	}

	var logs []models.CleanupLog
	err := query.Order("created_at DESC").
		Limit(limit).
		Preload("TriggerUser").
		Find(&logs).Error

	if err != nil {
		return nil, fmt.Errorf("failed to get cleanup logs: %w", err)
	}

	responses := make([]*models.CleanupLogResponse, len(logs))
	for i, log := range logs {
		responses[i] = log.ToResponse()
	}

	return responses, nil
}

// GetCleanupStats 获取清理统计信息
func (s *CleanupService) GetCleanupStats(ctx context.Context) (*CleanupStatsResponse, error) {
	stats := &CleanupStatsResponse{}

	// 获取登录历史记录总数
	var loginHistoryCount int64
	if err := s.db.WithContext(ctx).Model(&models.LoginHistory{}).Count(&loginHistoryCount).Error; err != nil {
		return nil, fmt.Errorf("failed to count login history: %w", err)
	}
	stats.LoginHistoryCount = loginHistoryCount

	// 获取清理日志统计
	var totalCleanups int64
	if err := s.db.WithContext(ctx).Model(&models.CleanupLog{}).Count(&totalCleanups).Error; err != nil {
		return nil, fmt.Errorf("failed to count cleanup logs: %w", err)
	}
	stats.TotalCleanups = totalCleanups

	// 获取成功的清理次数
	var successfulCleanups int64
	if err := s.db.WithContext(ctx).Model(&models.CleanupLog{}).
		Where("status = ?", "completed").Count(&successfulCleanups).Error; err != nil {
		return nil, fmt.Errorf("failed to count successful cleanups: %w", err)
	}
	stats.SuccessfulCleanups = successfulCleanups

	// 获取最后清理时间
	var lastCleanup models.CleanupLog
	err := s.db.WithContext(ctx).Model(&models.CleanupLog{}).
		Where("status = ?", "completed").
		Order("end_time DESC").
		First(&lastCleanup).Error
	
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("failed to get last cleanup: %w", err)
	}
	
	if err != gorm.ErrRecordNotFound {
		stats.LastCleanupTime = lastCleanup.EndTime
	}

	// 获取总删除记录数
	var totalDeleted int64
	if err := s.db.WithContext(ctx).Model(&models.CleanupLog{}).
		Select("COALESCE(SUM(records_deleted), 0)").
		Scan(&totalDeleted).Error; err != nil {
		return nil, fmt.Errorf("failed to get total deleted records: %w", err)
	}
	stats.TotalRecordsDeleted = totalDeleted

	// 获取清理配置
	config, err := s.GetCleanupConfig(ctx)
	if err != nil {
		log.Printf("Warning: failed to get cleanup config: %v", err)
		config = models.GetDefaultCleanupConfig()
	}
	stats.CurrentConfig = config

	return stats, nil
}

// CleanupStatsResponse 清理统计响应
type CleanupStatsResponse struct {
	LoginHistoryCount    int64                  `json:"login_history_count"`
	TotalCleanups        int64                  `json:"total_cleanups"`
	SuccessfulCleanups   int64                  `json:"successful_cleanups"`
	LastCleanupTime      *time.Time             `json:"last_cleanup_time,omitempty"`
	TotalRecordsDeleted  int64                  `json:"total_records_deleted"`
	CurrentConfig        *models.CleanupConfig  `json:"current_config"`
}

// InitializeDefaultConfig 初始化默认配置
func (s *CleanupService) InitializeDefaultConfig(ctx context.Context) error {
	// 检查是否已存在配置
	var existingConfig models.SystemConfig
	err := s.db.WithContext(ctx).
		Where("key = ? AND category = ?", "cleanup", "system").
		First(&existingConfig).Error

	if err == nil {
		// 配置已存在
		return nil
	}

	if err != gorm.ErrRecordNotFound {
		return fmt.Errorf("failed to check existing config: %w", err)
	}

	// 创建默认配置
	defaultConfig := models.GetDefaultCleanupConfig()
	systemConfig := models.SystemConfig{
		Key:          "cleanup",
		Category:     "system",
		Group:        "cleanup",
		Description:  "系统数据自动清理配置",
		IsRequired:   true,
		IsActive:     true,
		DefaultValue: "",
	}

	if err := systemConfig.SetValue(defaultConfig); err != nil {
		return fmt.Errorf("failed to set default config value: %w", err)
	}

	if err := s.db.WithContext(ctx).Create(&systemConfig).Error; err != nil {
		return fmt.Errorf("failed to create default config: %w", err)
	}

	log.Println("Initialized default cleanup configuration")
	return nil
}