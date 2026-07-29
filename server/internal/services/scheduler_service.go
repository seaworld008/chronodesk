package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

// SchedulerService 调度服务
type SchedulerService struct {
	db                *gorm.DB
	escalationService *EscalationService
	automationService *AutomationService
	cron              *cron.Cron
	parser            cron.Parser
	leaseManager      *schedulerLeaseManager
	leaseTTL          time.Duration
	jobs              map[string]*ScheduledJob
	running           bool
	stopped           bool
	ctx               context.Context
	cancel            context.CancelCauseFunc
	mu                sync.RWMutex
}

// ScheduledJob 定时任务
type ScheduledJob struct {
	ID              string
	Name            string
	Description     string
	CronExpr        string
	Handler         func(ctx context.Context) error
	LastAttempt     time.Time
	LastRun         time.Time
	LastFinished    time.Time
	NextRun         time.Time
	LastError       string
	IsActive        bool
	IsRunning       bool
	RunCount        int64
	ErrorCount      int64
	SkipCount       int64
	LeaseErrorCount int64
	Timeout         time.Duration

	entryID  cron.EntryID
	schedule cron.Schedule
}

const (
	defaultJobTimeout             = 2 * time.Minute
	defaultSchedulerLeaseTTL      = 30 * time.Second
	defaultSchedulerRedisTimeout  = 2 * time.Second
	minimumSchedulerLeaseRenewals = 3
)

var ErrSchedulerStopped = errors.New("scheduler has been stopped")

type schedulerServiceOptions struct {
	leaseTTL              time.Duration
	redisOperationTimeout time.Duration
}

// NewSchedulerService 创建必须依赖 Redis 分布式协调的生产调度服务。
// Redis 缺失或不可用时，任务不会回退到进程内锁，以避免多实例重复执行。
func NewSchedulerService(
	db *gorm.DB,
	redis SchedulerRedisExecutor,
) (*SchedulerService, error) {
	return newSchedulerService(db, redis, schedulerServiceOptions{
		leaseTTL:              defaultSchedulerLeaseTTL,
		redisOperationTimeout: defaultSchedulerRedisTimeout,
	})
}

func newSchedulerService(
	db *gorm.DB,
	redis SchedulerRedisExecutor,
	options schedulerServiceOptions,
) (*SchedulerService, error) {
	if db == nil {
		return nil, errors.New("scheduler database is required")
	}
	if options.leaseTTL <= 0 {
		return nil, errors.New("scheduler lease TTL must be positive")
	}
	if options.redisOperationTimeout <= 0 {
		return nil, errors.New("scheduler Redis timeout must be positive")
	}
	if options.leaseTTL < time.Duration(minimumSchedulerLeaseRenewals)*options.redisOperationTimeout {
		return nil, errors.New("scheduler lease TTL is too short for safe renewal")
	}
	leaseManager, err := newSchedulerLeaseManager(redis, options.redisOperationTimeout)
	if err != nil {
		return nil, err
	}
	parser := cron.NewParser(
		cron.Second |
			cron.Minute |
			cron.Hour |
			cron.Dom |
			cron.Month |
			cron.Dow |
			cron.Descriptor,
	)
	logger := cron.VerbosePrintfLogger(log.New(log.Writer(), "[调度器] ", log.LstdFlags))
	ctx, cancel := context.WithCancelCause(context.Background())
	service := &SchedulerService{
		db: db,
		cron: cron.New(
			cron.WithParser(parser),
			cron.WithLogger(logger),
			cron.WithChain(
				cron.Recover(logger),
				cron.SkipIfStillRunning(logger),
			),
		),
		parser:       parser,
		leaseManager: leaseManager,
		leaseTTL:     options.leaseTTL,
		jobs:         make(map[string]*ScheduledJob),
		ctx:          ctx,
		cancel:       cancel,
	}

	service.escalationService = NewEscalationService(db)
	service.automationService = NewAutomationService(db)

	if err := service.registerDefaultJobs(); err != nil {
		cancel(err)
		return nil, err
	}
	return service, nil
}

func (s *SchedulerService) SetAgentNativeService(native *AgentNativeService) error {
	if s == nil {
		return errors.New("scheduler is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running || s.stopped {
		return errors.New("agent native service must be configured before scheduler start")
	}
	if native == nil {
		return errors.New("agent native service is required")
	}
	if s.escalationService != nil {
		s.escalationService.SetAgentNativeService(native)
	}
	if native != nil {
		s.automationService = NewAutomationServiceWithAgentNative(s.db, native)
	}
	return nil
}

// registerDefaultJobs 注册默认任务
func (s *SchedulerService) registerDefaultJobs() error {
	// SLA检查任务 - 每15分钟执行一次
	if err := s.AddJob(&ScheduledJob{
		ID:          "sla_check",
		Name:        "SLA违规检查",
		Description: "定期检查工单SLA违规情况并执行升级",
		CronExpr:    "0 */15 * * * *", // 每15分钟
		Handler:     s.slaCheckHandler,
		IsActive:    true,
		Timeout:     3 * time.Minute,
	}); err != nil {
		return err
	}

	// 自动化规则执行任务 - 每5分钟执行一次
	if err := s.AddJob(&ScheduledJob{
		ID:          "automation_rules",
		Name:        "自动化规则执行",
		Description: "定期检查并执行符合条件的自动化规则",
		CronExpr:    "0 */5 * * * *", // 每5分钟
		Handler:     s.automationRulesHandler,
		IsActive:    true,
		Timeout:     2 * time.Minute,
	}); err != nil {
		return err
	}

	// 清理过期数据任务 - 每天凌晨2点执行
	if err := s.AddJob(&ScheduledJob{
		ID:          "cleanup_expired_data",
		Name:        "清理过期数据",
		Description: "清理过期的OTP代码、登录尝试记录等",
		CronExpr:    "0 0 2 * * *", // 每天2点
		Handler:     s.cleanupHandler,
		IsActive:    true,
		Timeout:     5 * time.Minute,
	}); err != nil {
		return err
	}

	// 统计数据更新任务 - 每小时执行一次
	if err := s.AddJob(&ScheduledJob{
		ID:          "update_statistics",
		Name:        "更新统计数据",
		Description: "更新系统性能统计和分析数据",
		CronExpr:    "0 0 * * * *", // 每小时
		Handler:     s.updateStatisticsHandler,
		IsActive:    true,
		Timeout:     2 * time.Minute,
	}); err != nil {
		return err
	}
	return nil
}

// AddJob 添加任务
func (s *SchedulerService) AddJob(job *ScheduledJob) error {
	if job == nil {
		return errors.New("scheduled job is required")
	}
	if job.ID == "" {
		return errors.New("scheduled job ID is required")
	}
	if job.Name == "" {
		return errors.New("scheduled job name is required")
	}
	if job.Handler == nil {
		return errors.New("scheduled job handler is required")
	}
	schedule, err := s.parser.Parse(job.CronExpr)
	if err != nil {
		return fmt.Errorf("invalid cron expression for job %s: %w", job.ID, err)
	}
	copyOfJob := *job
	if copyOfJob.Timeout <= 0 {
		copyOfJob.Timeout = defaultJobTimeout
	}
	copyOfJob.schedule = schedule
	copyOfJob.NextRun = schedule.Next(time.Now())
	copyOfJob.IsRunning = false
	copyOfJob.LastError = ""

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return ErrSchedulerStopped
	}
	if _, exists := s.jobs[job.ID]; exists {
		return fmt.Errorf("job with ID %s already exists", job.ID)
	}
	entryID, err := s.cron.AddFunc(copyOfJob.CronExpr, func() {
		s.executeJob(copyOfJob.ID)
	})
	if err != nil {
		return fmt.Errorf("register cron job %s: %w", copyOfJob.ID, err)
	}
	copyOfJob.entryID = entryID
	s.jobs[copyOfJob.ID] = &copyOfJob
	log.Printf("已注册定时任务：%s（%s），表达式：%s", copyOfJob.Name, copyOfJob.ID, copyOfJob.CronExpr)
	return nil
}

// RemoveJob 移除任务
func (s *SchedulerService) RemoveJob(jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, exists := s.jobs[jobID]
	if !exists {
		return fmt.Errorf("job with ID %s not found", jobID)
	}
	s.cron.Remove(job.entryID)
	delete(s.jobs, jobID)
	log.Printf("已移除定时任务：%s", jobID)
	return nil
}

// Start 启动调度器
func (s *SchedulerService) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return nil
	}
	if s.stopped {
		return ErrSchedulerStopped
	}
	s.cron.Start()
	s.running = true
	log.Println("生产调度器已启动")
	return nil
}

// Stop 停止接收新任务，取消正在运行的任务并等待其释放分布式租约。
func (s *SchedulerService) Stop(ctx context.Context) error {
	if ctx == nil {
		return errors.New("scheduler shutdown context is required")
	}
	s.mu.Lock()
	if !s.running {
		s.stopped = true
		s.cancel(ErrSchedulerStopped)
		s.mu.Unlock()
		return nil
	}
	s.running = false
	s.stopped = true
	s.cancel(ErrSchedulerStopped)
	s.mu.Unlock()

	stopped := s.cron.Stop()
	select {
	case <-stopped.Done():
		log.Println("生产调度器已优雅停止")
		return nil
	case <-ctx.Done():
		return fmt.Errorf("scheduler shutdown timed out: %w", ctx.Err())
	}
}

// executeJob 执行任务
func (s *SchedulerService) executeJob(jobID string) {
	startTime := time.Now()
	job, ok := s.beginLocalRun(jobID, startTime)
	if !ok {
		return
	}
	var (
		lease      *schedulerLease
		runErr     error
		handlerRan bool
		skipped    bool
	)
	defer func() {
		if recovered := recover(); recovered != nil {
			runErr = fmt.Errorf("scheduler job panic: %v", recovered)
			log.Printf("定时任务 %s 发生 panic：%v\n%s", job.ID, recovered, debug.Stack())
		}
		if lease != nil {
			if releaseErr := s.leaseManager.release(lease); releaseErr != nil {
				runErr = errors.Join(runErr, releaseErr)
			}
		}
		s.finishLocalRun(job, startTime, handlerRan, skipped, runErr)
	}()

	lease, runErr = s.leaseManager.acquire(s.ctx, job.ID, s.leaseTTL)
	if runErr != nil {
		skipped = errors.Is(runErr, ErrSchedulerLeaseHeld)
		return
	}

	handlerRan = true
	log.Printf("开始执行定时任务：%s（%s）", job.Name, job.ID)
	runErr = s.runWithLease(job, lease)
}

func (s *SchedulerService) beginLocalRun(jobID string, attemptedAt time.Time) (*ScheduledJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, exists := s.jobs[jobID]
	if !exists || !job.IsActive || s.stopped {
		return nil, false
	}
	job.LastAttempt = attemptedAt
	if job.IsRunning {
		job.SkipCount++
		return nil, false
	}
	job.IsRunning = true
	return job, true
}

func (s *SchedulerService) runWithLease(job *ScheduledJob, lease *schedulerLease) error {
	timeoutCtx, timeoutCancel := context.WithTimeout(s.ctx, job.Timeout)
	defer timeoutCancel()
	runCtx, cancelRun := context.WithCancelCause(timeoutCtx)
	defer cancelRun(nil)

	stopRenewal := make(chan struct{})
	renewalResult := make(chan error, 1)
	go func() {
		renewalResult <- s.renewLeaseUntilDone(lease, stopRenewal, cancelRun)
	}()

	handlerErr := invokeSchedulerHandler(job, runCtx)
	close(stopRenewal)
	renewalErr := <-renewalResult
	if renewalErr != nil {
		handlerErr = errors.Join(handlerErr, renewalErr)
	}
	if cause := context.Cause(runCtx); cause != nil && !errors.Is(handlerErr, cause) {
		handlerErr = errors.Join(handlerErr, cause)
	}
	return handlerErr
}

func invokeSchedulerHandler(job *ScheduledJob, ctx context.Context) (runErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			runErr = fmt.Errorf("scheduler job panic: %v", recovered)
			log.Printf("定时任务 %s 发生 panic：%v\n%s", job.ID, recovered, debug.Stack())
		}
	}()
	return job.Handler(ctx)
}

func (s *SchedulerService) renewLeaseUntilDone(
	lease *schedulerLease,
	stop <-chan struct{},
	cancelRun context.CancelCauseFunc,
) error {
	interval := s.leaseTTL / minimumSchedulerLeaseRenewals
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return nil
		case <-ticker.C:
			if err := s.leaseManager.renew(context.Background(), lease, s.leaseTTL); err != nil {
				cancelRun(err)
				return err
			}
		}
	}
}

func (s *SchedulerService) finishLocalRun(
	job *ScheduledJob,
	startedAt time.Time,
	handlerRan bool,
	skipped bool,
	runErr error,
) {
	finishedAt := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	job.IsRunning = false
	job.LastFinished = finishedAt
	job.NextRun = job.schedule.Next(finishedAt)
	if skipped {
		job.SkipCount++
		job.LastError = runErr.Error()
		log.Printf("跳过定时任务 %s：其他实例持有租约", job.ID)
		return
	}
	if handlerRan {
		job.LastRun = startedAt
		job.RunCount++
	}
	if runErr != nil {
		job.ErrorCount++
		job.LastError = runErr.Error()
		if errors.Is(runErr, ErrSchedulerRedisUnavailable) ||
			errors.Is(runErr, ErrSchedulerLeaseLost) {
			job.LeaseErrorCount++
		}
		log.Printf("定时任务 %s 执行失败：%v", job.ID, runErr)
		return
	}
	job.LastError = ""
	log.Printf("定时任务 %s 执行成功，耗时 %s", job.ID, finishedAt.Sub(startedAt))
}

// GetJobStatus 获取任务状态
func (s *SchedulerService) GetJobStatus() map[string]*ScheduledJob {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]*ScheduledJob)
	for id, job := range s.jobs {
		// 复制任务信息以避免并发问题
		result[id] = &ScheduledJob{
			ID:              job.ID,
			Name:            job.Name,
			Description:     job.Description,
			CronExpr:        job.CronExpr,
			LastAttempt:     job.LastAttempt,
			LastRun:         job.LastRun,
			LastFinished:    job.LastFinished,
			NextRun:         job.schedule.Next(time.Now()),
			LastError:       job.LastError,
			IsActive:        job.IsActive,
			IsRunning:       job.IsRunning,
			RunCount:        job.RunCount,
			ErrorCount:      job.ErrorCount,
			SkipCount:       job.SkipCount,
			LeaseErrorCount: job.LeaseErrorCount,
			Timeout:         job.Timeout,
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
	hasActiveRules, err := s.automationService.HasActiveRules(
		ctx,
		eventcontract.AutomationScheduledCheckEventType,
	)
	if err != nil {
		return err
	}
	if !hasActiveRules {
		log.Printf("Automation rules scheduler skipped: no active scheduled CloudEvent rules")
		return nil
	}

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

				if err := s.automationService.EnqueueScheduledCheck(tx.Statement.Context, &tickets[i]); err != nil {
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
	var cleanupErrors []error

	// 清理过期的OTP代码（30分钟前的）
	expiredOTP := now.Add(-30 * time.Minute)
	if err := s.db.WithContext(ctx).
		Exec("DELETE FROM otp_codes WHERE expires_at < ?", expiredOTP).Error; err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("cleanup expired OTP codes: %w", err))
	}

	// 清理过期的登录尝试记录（7天前的）
	expiredAttempts := now.AddDate(0, 0, -7)
	if err := s.db.WithContext(ctx).Exec("DELETE FROM login_attempts WHERE created_at < ?", expiredAttempts).Error; err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("cleanup old login attempts: %w", err))
	}

	// 清理过期的refresh token（30天前的）
	expiredTokens := now.AddDate(0, 0, -30)
	if err := s.db.WithContext(ctx).Exec("DELETE FROM refresh_tokens WHERE expires_at < ? OR revoked = true", expiredTokens).Error; err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("cleanup expired refresh tokens: %w", err))
	}

	return errors.Join(cleanupErrors...)
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

	var updateErrors []error
	for _, config := range slaConfigs {
		if config.AppliedCount > 0 {
			complianceRate := float64(config.AppliedCount-config.ViolationCount) / float64(config.AppliedCount) * 100
			if err := s.db.WithContext(ctx).Model(&config).Update("compliance_rate", complianceRate).Error; err != nil {
				updateErrors = append(
					updateErrors,
					fmt.Errorf("update compliance rate for SLA config %d: %w", config.ID, err),
				)
			}
		}
	}

	return errors.Join(updateErrors...)
}

// IsRunning 检查调度器是否运行中
func (s *SchedulerService) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}
