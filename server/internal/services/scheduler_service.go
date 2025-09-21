package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"gongdan-system/internal/models"
	"gorm.io/gorm"
)

// SchedulerService 调度服务
type SchedulerService struct {
	db                *gorm.DB
	escalationService *EscalationService
	automationService *AutomationService
	jobs              map[string]*ScheduledJob
	running           bool
	stopChan          chan struct{}
	mu                sync.RWMutex
}

// ScheduledJob 定时任务
type ScheduledJob struct {
	ID          string
	Name        string
	Description string
	CronExpr    string
	Handler     func(ctx context.Context) error
	LastRun     time.Time
	NextRun     time.Time
	IsActive    bool
	RunCount    int64
	ErrorCount  int64
	Timeout     time.Duration
}

const defaultJobTimeout = 2 * time.Minute

// JobResult 任务执行结果
type JobResult struct {
	JobID     string    `json:"job_id"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	Success   bool      `json:"success"`
	Error     string    `json:"error,omitempty"`
	Duration  string    `json:"duration"`
}

// NewSchedulerService 创建调度服务
func NewSchedulerService(db *gorm.DB) *SchedulerService {
	service := &SchedulerService{
		db:       db,
		jobs:     make(map[string]*ScheduledJob),
		stopChan: make(chan struct{}),
	}

	service.escalationService = NewEscalationService(db)
	service.automationService = NewAutomationService(db)

	// 注册默认任务
	service.registerDefaultJobs()

	return service
}

// registerDefaultJobs 注册默认任务
func (s *SchedulerService) registerDefaultJobs() {
	// SLA检查任务 - 每15分钟执行一次
	s.AddJob(&ScheduledJob{
		ID:          "sla_check",
		Name:        "SLA违规检查",
		Description: "定期检查工单SLA违规情况并执行升级",
		CronExpr:    "0 */15 * * * *", // 每15分钟
		Handler:     s.slaCheckHandler,
		IsActive:    true,
		Timeout:     3 * time.Minute,
	})

	// 自动化规则执行任务 - 每5分钟执行一次
	s.AddJob(&ScheduledJob{
		ID:          "automation_rules",
		Name:        "自动化规则执行",
		Description: "定期检查并执行符合条件的自动化规则",
		CronExpr:    "0 */5 * * * *", // 每5分钟
		Handler:     s.automationRulesHandler,
		IsActive:    true,
		Timeout:     2 * time.Minute,
	})

	// 清理过期数据任务 - 每天凌晨2点执行
	s.AddJob(&ScheduledJob{
		ID:          "cleanup_expired_data",
		Name:        "清理过期数据",
		Description: "清理过期的OTP代码、登录尝试记录等",
		CronExpr:    "0 0 2 * * *", // 每天2点
		Handler:     s.cleanupHandler,
		IsActive:    true,
		Timeout:     5 * time.Minute,
	})

	// 统计数据更新任务 - 每小时执行一次
	s.AddJob(&ScheduledJob{
		ID:          "update_statistics",
		Name:        "更新统计数据",
		Description: "更新系统性能统计和分析数据",
		CronExpr:    "0 0 * * * *", // 每小时
		Handler:     s.updateStatisticsHandler,
		IsActive:    true,
		Timeout:     2 * time.Minute,
	})
}

// AddJob 添加任务
func (s *SchedulerService) AddJob(job *ScheduledJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.jobs[job.ID]; exists {
		return fmt.Errorf("job with ID %s already exists", job.ID)
	}

	if job.Timeout <= 0 {
		job.Timeout = defaultJobTimeout
	}

	// 计算下次执行时间
	nextRun, err := s.calculateNextRun(job.CronExpr)
	if err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}

	job.NextRun = nextRun
	s.jobs[job.ID] = job

	log.Printf("Added scheduled job: %s (%s)", job.Name, job.ID)
	return nil
}

// RemoveJob 移除任务
func (s *SchedulerService) RemoveJob(jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.jobs[jobID]; !exists {
		return fmt.Errorf("job with ID %s not found", jobID)
	}

	delete(s.jobs, jobID)
	log.Printf("Removed scheduled job: %s", jobID)
	return nil
}

// Start 启动调度器
func (s *SchedulerService) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()

	log.Println("Starting scheduler service...")

	ticker := time.NewTicker(30 * time.Second) // 每30秒检查一次
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.checkAndRunJobs()
		case <-s.stopChan:
			log.Println("Scheduler service stopped")
			return
		}
	}
}

// Stop 停止调度器
func (s *SchedulerService) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	s.mu.Unlock()

	close(s.stopChan)
}

// checkAndRunJobs 检查并执行到期的任务
func (s *SchedulerService) checkAndRunJobs() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()

	for _, job := range s.jobs {
		if !job.IsActive {
			continue
		}

		if now.After(job.NextRun) {
			go s.executeJob(job)
		}
	}
}

// executeJob 执行任务
func (s *SchedulerService) executeJob(job *ScheduledJob) {
	startTime := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), job.Timeout)
	defer cancel()

	log.Printf("Executing job: %s (%s)", job.Name, job.ID)

	// 执行任务
	err := job.Handler(ctx)
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}

	endTime := time.Now()
	duration := endTime.Sub(startTime)

	// 更新任务统计
	s.mu.Lock()
	job.LastRun = startTime
	job.RunCount++

	if err != nil {
		job.ErrorCount++
		log.Printf("Job %s failed: %v", job.ID, err)
	} else {
		log.Printf("Job %s completed successfully in %v", job.ID, duration)
	}

	// 计算下次执行时间
	nextRun, calcErr := s.calculateNextRun(job.CronExpr)
	if calcErr != nil {
		log.Printf("Failed to calculate next run for job %s: %v", job.ID, calcErr)
	} else {
		job.NextRun = nextRun
	}
	s.mu.Unlock()

	// 记录执行结果
	result := &JobResult{
		JobID:     job.ID,
		StartTime: startTime,
		EndTime:   endTime,
		Success:   err == nil,
		Duration:  duration.String(),
	}

	if err != nil {
		result.Error = err.Error()
	}

	// 这里可以将执行结果保存到数据库或发送通知
	s.logJobResult(result)
}

// calculateNextRun 计算下次执行时间（简化的cron实现）
func (s *SchedulerService) calculateNextRun(cronExpr string) (time.Time, error) {
	// 简化实现，支持基本的cron表达式格式
	// "0 */15 * * * *" -> 每15分钟
	// "0 0 2 * * *"    -> 每天2点
	// "0 0 * * * *"    -> 每小时

	now := time.Now()

	switch cronExpr {
	case "0 */15 * * * *": // 每15分钟
		return now.Add(15 * time.Minute), nil
	case "0 */5 * * * *": // 每5分钟
		return now.Add(5 * time.Minute), nil
	case "0 0 2 * * *": // 每天2点
		tomorrow := now.AddDate(0, 0, 1)
		return time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 2, 0, 0, 0, tomorrow.Location()), nil
	case "0 0 * * * *": // 每小时
		return now.Add(1 * time.Hour), nil
	default:
		// 默认每30分钟执行一次
		return now.Add(30 * time.Minute), nil
	}
}

// logJobResult 记录任务执行结果
func (s *SchedulerService) logJobResult(result *JobResult) {
	// 这里可以将结果保存到数据库
	// 暂时只记录日志
	if result.Success {
		log.Printf("Job %s completed: %s", result.JobID, result.Duration)
	} else {
		log.Printf("Job %s failed: %s", result.JobID, result.Error)
	}
}

// GetJobStatus 获取任务状态
func (s *SchedulerService) GetJobStatus() map[string]*ScheduledJob {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]*ScheduledJob)
	for id, job := range s.jobs {
		// 复制任务信息以避免并发问题
		result[id] = &ScheduledJob{
			ID:          job.ID,
			Name:        job.Name,
			Description: job.Description,
			CronExpr:    job.CronExpr,
			LastRun:     job.LastRun,
			NextRun:     job.NextRun,
			IsActive:    job.IsActive,
			RunCount:    job.RunCount,
			ErrorCount:  job.ErrorCount,
		}
	}

	return result
}

// 任务处理器实现

// slaCheckHandler SLA检查处理器
func (s *SchedulerService) slaCheckHandler(ctx context.Context) error {
	return s.escalationService.CheckSLAViolations(ctx)
}

// automationRulesHandler 自动化规则处理器
func (s *SchedulerService) automationRulesHandler(ctx context.Context) error {
	const batchSize = 50
	var (
		tickets   []models.Ticket
		processed int
	)

	result := s.db.WithContext(ctx).
		Where("status IN ?", []string{"open", "in_progress"}).
		Order("id ASC").
		FindInBatches(&tickets, batchSize, func(tx *gorm.DB, batch int) error {
			for i := range tickets {
				if ctx.Err() != nil {
					return ctx.Err()
				}

				if err := s.automationService.ExecuteRules(tx.Statement.Context, "scheduled_check", &tickets[i]); err != nil {
					log.Printf("Failed to execute automation rules for ticket %d: %v", tickets[i].ID, err)
				}
				processed++
			}
			return nil
		})

	if result.Error != nil && !errors.Is(result.Error, context.Canceled) {
		return fmt.Errorf("failed to process automation rules: %w", result.Error)
	}

	if errors.Is(result.Error, context.Canceled) {
		return result.Error
	}

	log.Printf("Automation rules scheduler processed %d tickets", processed)
	return nil
}

// cleanupHandler 清理处理器
func (s *SchedulerService) cleanupHandler(ctx context.Context) error {
	now := time.Now()

	// 清理过期的OTP代码（30分钟前的）
	expiredOTP := now.Add(-30 * time.Minute)
	if err := s.db.WithContext(ctx).Where("expires_at < ?", expiredOTP).Delete(&models.OTPCode{}).Error; err != nil {
		log.Printf("Failed to cleanup expired OTP codes: %v", err)
	}

	// 清理过期的登录尝试记录（7天前的）
	expiredAttempts := now.AddDate(0, 0, -7)
	if err := s.db.WithContext(ctx).Exec("DELETE FROM login_attempts WHERE created_at < ?", expiredAttempts).Error; err != nil {
		log.Printf("Failed to cleanup old login attempts: %v", err)
	}

	// 清理过期的refresh token（30天前的）
	expiredTokens := now.AddDate(0, 0, -30)
	if err := s.db.WithContext(ctx).Exec("DELETE FROM refresh_tokens WHERE expires_at < ? OR revoked = true", expiredTokens).Error; err != nil {
		log.Printf("Failed to cleanup expired refresh tokens: %v", err)
	}

	return nil
}

// updateStatisticsHandler 更新统计数据处理器
func (s *SchedulerService) updateStatisticsHandler(ctx context.Context) error {
	// 这里可以更新各种统计信息
	// 例如：工单处理速度、用户活跃度、系统性能指标等
	log.Println("Updating system statistics...")

	// 更新SLA合规率
	var slaConfigs []models.SLAConfig
	if err := s.db.WithContext(ctx).Where("is_active = ?", true).Find(&slaConfigs).Error; err != nil {
		return fmt.Errorf("failed to get SLA configs: %w", err)
	}

	for _, config := range slaConfigs {
		if config.AppliedCount > 0 {
			complianceRate := float64(config.AppliedCount-config.ViolationCount) / float64(config.AppliedCount) * 100
			if err := s.db.WithContext(ctx).Model(&config).Update("compliance_rate", complianceRate).Error; err != nil {
				log.Printf("Failed to update compliance rate for SLA config %d: %v", config.ID, err)
			}
		}
	}

	return nil
}

// ToggleJob 切换任务状态
func (s *SchedulerService) ToggleJob(jobID string, active bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, exists := s.jobs[jobID]
	if !exists {
		return fmt.Errorf("job with ID %s not found", jobID)
	}

	job.IsActive = active
	log.Printf("Job %s is now %s", jobID, map[bool]string{true: "active", false: "inactive"}[active])

	return nil
}

// IsRunning 检查调度器是否运行中
func (s *SchedulerService) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}
