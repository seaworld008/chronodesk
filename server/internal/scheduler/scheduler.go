package scheduler

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"gongdan-system/internal/services"
	"gorm.io/gorm"
)

// JobFunc 定时任务函数类型
type JobFunc func(ctx context.Context) error

// Job 定时任务接口
type Job interface {
	Run(ctx context.Context) error
	Name() string
	Schedule() string
	Description() string
}

// CleanupJob 清理任务实现
type CleanupJob struct {
	name        string
	schedule    string
	description string
	cleanupSvc  *services.CleanupService
	taskType    string
}

// NewCleanupJob 创建清理任务
func NewCleanupJob(name, schedule, description, taskType string, cleanupSvc *services.CleanupService) *CleanupJob {
	return &CleanupJob{
		name:        name,
		schedule:    schedule,
		description: description,
		cleanupSvc:  cleanupSvc,
		taskType:    taskType,
	}
}

func (j *CleanupJob) Name() string        { return j.name }
func (j *CleanupJob) Schedule() string    { return j.schedule }
func (j *CleanupJob) Description() string { return j.description }

func (j *CleanupJob) Run(ctx context.Context) error {
	log.Printf("🔄 开始执行清理任务: %s", j.name)
	err := j.cleanupSvc.ExecuteCleanup(ctx, j.taskType, "scheduled", nil)
	if err != nil {
		log.Printf("❌ 清理任务执行失败: %v", err)
	} else {
		log.Printf("✅ 清理任务执行成功: %s", j.name)
	}
	return err
}

// Scheduler 定时任务调度器
type Scheduler struct {
	cron       *cron.Cron
	jobs       map[string]Job
	db         *gorm.DB
	logger     *log.Logger
	ctx        context.Context
	cancel     context.CancelFunc
	cleanupSvc *services.CleanupService
	startTime  time.Time

	// 任务状态跟踪
	mu         sync.RWMutex
	running    map[string]bool
	lastRun    map[string]time.Time
	lastError  map[string]error
	runCount   map[string]int64
	errorCount map[string]int64
}

// NewScheduler 创建新的调度器
func NewScheduler(db *gorm.DB) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())
	logger := log.New(log.Writer(), "[SCHEDULER] ", log.LstdFlags)

	// 配置cron选项：包含秒字段，启用恢复机制和详细日志
	cronOptions := []cron.Option{
		cron.WithSeconds(),
		cron.WithChain(cron.Recover(cron.VerbosePrintfLogger(logger))),
		cron.WithLogger(cron.VerbosePrintfLogger(logger)),
	}

	return &Scheduler{
		cron:       cron.New(cronOptions...),
		jobs:       make(map[string]Job),
		db:         db,
		logger:     logger,
		ctx:        ctx,
		cancel:     cancel,
		cleanupSvc: services.NewCleanupService(db),
		running:    make(map[string]bool),
		lastRun:    make(map[string]time.Time),
		lastError:  make(map[string]error),
		runCount:   make(map[string]int64),
		errorCount: make(map[string]int64),
	}
}

// RegisterJob 注册定时任务
func (s *Scheduler) RegisterJob(job Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	jobName := job.Name()
	if _, exists := s.jobs[jobName]; exists {
		return fmt.Errorf("job %s already registered", jobName)
	}

	// 注册cron任务
	_, err := s.cron.AddFunc(job.Schedule(), func() {
		s.executeJob(job)
	})
	if err != nil {
		return fmt.Errorf("failed to register cron job %s: %v", jobName, err)
	}

	s.jobs[jobName] = job
	s.running[jobName] = false
	s.runCount[jobName] = 0
	s.errorCount[jobName] = 0

	s.logger.Printf("Registered job: %s with schedule: %s", jobName, job.Schedule())
	return nil
}

// RemoveJob 移除定时任务
func (s *Scheduler) RemoveJob(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.jobs, name)
	log.Printf("Removed job: %s", name)
}

// executeJob 执行任务
func (s *Scheduler) executeJob(job Job) {
	jobName := job.Name()

	// 防止任务重复执行
	s.mu.Lock()
	if s.running[jobName] {
		s.mu.Unlock()
		s.logger.Printf("Job %s is already running, skipping", jobName)
		return
	}
	s.running[jobName] = true
	s.mu.Unlock()

	// 记录任务开始时间
	startTime := time.Now()
	s.logger.Printf("Running job: %s", jobName)

	// 执行任务
	err := job.Run(s.ctx)

	// 更新任务状态
	s.mu.Lock()
	s.running[jobName] = false
	s.lastRun[jobName] = startTime
	s.runCount[jobName]++
	if err != nil {
		s.lastError[jobName] = err
		s.errorCount[jobName]++
		s.logger.Printf("Job %s failed: %v", jobName, err)
	} else {
		s.lastError[jobName] = nil
		duration := time.Since(startTime)
		nextRun := s.getNextRunTime(job)
		s.logger.Printf("Job %s completed successfully in %v, next run: %v", jobName, duration, nextRun)
	}
	s.mu.Unlock()
}

// Start 启动调度器
func (s *Scheduler) Start() {
	s.logger.Printf("🚀 启动任务调度器...")

	s.mu.Lock()
	if s.startTime.IsZero() {
		s.startTime = time.Now()
	}
	s.mu.Unlock()

	// 初始化默认清理配置
	if err := s.cleanupSvc.InitializeDefaultConfig(s.ctx); err != nil {
		s.logger.Printf("⚠️  警告: 无法初始化默认清理配置: %v", err)
	} else {
		s.logger.Println("✅ 默认清理配置初始化成功")
	}

	// 注册默认的清理任务
	cleanupJob := NewCleanupJob(
		"cleanup_login_history",
		"0 2 * * *", // 每天凌晨2点执行
		"自动清理过期的登录历史记录",
		"login_history",
		s.cleanupSvc,
	)

	if err := s.RegisterJob(cleanupJob); err != nil {
		s.logger.Printf("❌ 添加清理任务失败: %v", err)
	}

	// 启动cron调度器
	s.logger.Printf("Starting scheduler with %d jobs", len(s.jobs))
	s.cron.Start()
}

// Stop 停止调度器
func (s *Scheduler) Stop() {
	s.logger.Printf("停止调度器...")
	s.cancel()

	// 停止cron调度器，等待正在执行的任务完成
	ctx := s.cron.Stop()
	select {
	case <-ctx.Done():
		s.logger.Printf("调度器优雅停止")
	case <-time.After(30 * time.Second):
		s.logger.Printf("调度器停止超时，强制关闭")
	}
}

// GetJobStatus 获取指定任务状态
func (s *Scheduler) GetJobStatus(jobName string) *JobStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	job, exists := s.jobs[jobName]
	if !exists {
		return nil
	}

	return &JobStatus{
		Name:        jobName,
		Schedule:    job.Schedule(),
		IsRunning:   s.running[jobName],
		LastRun:     s.lastRun[jobName],
		LastError:   s.lastError[jobName],
		RunCount:    s.runCount[jobName],
		ErrorCount:  s.errorCount[jobName],
		NextRun:     s.getNextRunTime(job),
		Status:      s.getJobStatusString(jobName),
		Description: job.Description(),
	}
}

// GetAllJobStatus 获取所有任务状态
func (s *Scheduler) GetAllJobStatus() []*JobStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	statuses := make([]*JobStatus, 0, len(s.jobs))
	for jobName := range s.jobs {
		if status := s.GetJobStatus(jobName); status != nil {
			statuses = append(statuses, status)
		}
	}

	return statuses
}

// JobStatus 任务状态
type JobStatus struct {
	Name        string    `json:"name"`
	Schedule    string    `json:"schedule"`
	IsRunning   bool      `json:"is_running"`
	LastRun     time.Time `json:"last_run"`
	LastError   error     `json:"last_error,omitempty"`
	RunCount    int64     `json:"run_count"`
	ErrorCount  int64     `json:"error_count"`
	NextRun     time.Time `json:"next_run"`
	Status      string    `json:"status"`
	Description string    `json:"description"`
}

// getNextRunTime 计算下次运行时间
func (s *Scheduler) getNextRunTime(job Job) time.Time {
	// 使用cron解析器计算下次执行时间
	parser := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	schedule, err := parser.Parse(job.Schedule())
	if err != nil {
		return time.Time{}
	}

	return schedule.Next(time.Now())
}

// getJobStatusString 获取任务状态字符串
func (s *Scheduler) getJobStatusString(jobName string) string {
	if s.running[jobName] {
		return "running"
	}

	if err := s.lastError[jobName]; err != nil {
		return "error"
	}

	if !s.lastRun[jobName].IsZero() {
		return "success"
	}

	return "pending"
}

// RunJobManually 手动执行任务
func (s *Scheduler) RunJobManually(jobName string) error {
	s.mu.RLock()
	job, exists := s.jobs[jobName]
	s.mu.RUnlock()

	if !exists {
		return fmt.Errorf("job %s not found", jobName)
	}

	// 在新的goroutine中执行任务
	go s.executeJob(job)
	return nil
}

// GetSchedulerStats 获取调度器统计信息
func (s *Scheduler) GetSchedulerStats() *SchedulerStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	uptime := time.Duration(0)
	lastActivity := time.Time{}
	if !s.startTime.IsZero() {
		uptime = time.Since(s.startTime)
		lastActivity = s.startTime
	}

	stats := &SchedulerStats{
		TotalJobs:    len(s.jobs),
		RunningJobs:  0,
		TotalRuns:    0,
		TotalErrors:  0,
		Uptime:       uptime,
		LastActivity: lastActivity,
	}

	for jobName := range s.jobs {
		if s.running[jobName] {
			stats.RunningJobs++
		}
		stats.TotalRuns += s.runCount[jobName]
		stats.TotalErrors += s.errorCount[jobName]

		if lastRun := s.lastRun[jobName]; lastRun.After(stats.LastActivity) {
			stats.LastActivity = lastRun
		}
	}

	return stats
}

// SchedulerStats 调度器统计信息
type SchedulerStats struct {
	TotalJobs    int           `json:"total_jobs"`
	RunningJobs  int           `json:"running_jobs"`
	TotalRuns    int64         `json:"total_runs"`
	TotalErrors  int64         `json:"total_errors"`
	Uptime       time.Duration `json:"uptime"`
	LastActivity time.Time     `json:"last_activity"`
}

// IsHealthy 检查调度器健康状态
func (s *Scheduler) IsHealthy() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 检查是否有任务超过预期时间还在运行
	for jobName, isRunning := range s.running {
		if isRunning {
			lastRun := s.lastRun[jobName]
			if time.Since(lastRun) > 10*time.Minute { // 任务运行超过10分钟视为异常
				s.logger.Printf("Job %s has been running for %v, might be stuck", jobName, time.Since(lastRun))
				return false
			}
		}
	}

	return true
}
