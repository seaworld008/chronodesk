package services

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"time"

	"gorm.io/gorm"
	"gongdan-system/internal/models"
)

// AnalyticsService 系统监控统计服务
type AnalyticsService struct {
	db *gorm.DB
}

// NewAnalyticsService 创建分析服务实例
func NewAnalyticsService(db *gorm.DB) *AnalyticsService {
	return &AnalyticsService{db: db}
}

// SystemStats 系统运行状态统计
type SystemStats struct {
	// 基础系统信息
	Uptime       time.Duration `json:"uptime"`
	CPUCount     int           `json:"cpu_count"`
	GoVersion    string        `json:"go_version"`
	ServerTime   time.Time     `json:"server_time"`
	
	// Go 运行时统计
	GoRoutines   int     `json:"goroutines"`
	CGOCalls     int64   `json:"cgo_calls"`
	
	// 内存统计
	MemStats     MemoryStats `json:"memory_stats"`
	
	// GC 统计
	GCStats      GCStats     `json:"gc_stats"`
}

// MemoryStats 内存统计信息
type MemoryStats struct {
	// 堆内存
	HeapAlloc    uint64 `json:"heap_alloc"`     // 当前分配的堆内存
	HeapSys      uint64 `json:"heap_sys"`       // 从系统获取的堆内存
	HeapIdle     uint64 `json:"heap_idle"`      // 空闲堆内存
	HeapInuse    uint64 `json:"heap_inuse"`     // 正在使用的堆内存
	HeapReleased uint64 `json:"heap_released"`  // 释放给系统的内存
	HeapObjects  uint64 `json:"heap_objects"`   // 堆中对象数量
	
	// 总体内存
	Sys          uint64 `json:"sys"`            // 从系统获取的总内存
	Alloc        uint64 `json:"alloc"`          // 当前分配的总内存
	TotalAlloc   uint64 `json:"total_alloc"`    // 累计分配的内存
	
	// 栈内存
	StackInuse   uint64 `json:"stack_inuse"`    // 栈使用的内存
	StackSys     uint64 `json:"stack_sys"`      // 栈从系统获取的内存
	
	// 其他
	Mallocs      uint64 `json:"mallocs"`        // 累计分配次数
	Frees        uint64 `json:"frees"`          // 累计释放次数
}

// GCStats 垃圾回收统计
type GCStats struct {
	NumGC        uint32        `json:"num_gc"`         // GC次数
	NumForcedGC  uint32        `json:"num_forced_gc"`  // 强制GC次数
	GCCPUFraction float64      `json:"gc_cpu_fraction"` // GC CPU占用比例
	LastGC       time.Time     `json:"last_gc"`        // 上次GC时间
	NextGC       uint64        `json:"next_gc"`        // 下次GC阈值
	PauseTotal   time.Duration `json:"pause_total"`    // 总暂停时间
	PauseNs      []uint64      `json:"pause_ns"`       // 最近暂停时间(纳秒)
}

// BusinessStats 业务数据统计
type BusinessStats struct {
	// 工单统计
	TicketStats AnalyticsTicketStats `json:"ticket_stats"`
	
	// 用户统计  
	UserStats   UserStats   `json:"user_stats"`
	
	// 系统活动统计
	ActivityStats ActivityStats `json:"activity_stats"`
}

// AnalyticsTicketStats 工单统计
type AnalyticsTicketStats struct {
	Total        int64 `json:"total"`
	Open         int64 `json:"open"`
	InProgress   int64 `json:"in_progress"`
	Resolved     int64 `json:"resolved"`
	Closed       int64 `json:"closed"`
	
	// 按优先级统计
	HighPriority   int64 `json:"high_priority"`
	MediumPriority int64 `json:"medium_priority"`
	LowPriority    int64 `json:"low_priority"`
	
	// 按类型统计
	ByCategory map[string]int64 `json:"by_category"`
	
	// 时间范围统计
	Today     int64 `json:"today"`
	ThisWeek  int64 `json:"this_week"`
	ThisMonth int64 `json:"this_month"`
	
	// 响应时间统计
	AvgResponseTime float64 `json:"avg_response_time_hours"`
	AvgResolutionTime float64 `json:"avg_resolution_time_hours"`
}

// UserStats 用户统计
type UserStats struct {
	Total       int64 `json:"total"`
	Active      int64 `json:"active"`      // 活跃用户(最近30天有活动)
	Admins      int64 `json:"admins"`
	Agents      int64 `json:"agents"`
	Customers   int64 `json:"customers"`
	
	// 登录统计
	TodayLogins int64 `json:"today_logins"`
	WeekLogins  int64 `json:"week_logins"`
	MonthLogins int64 `json:"month_logins"`
}

// ActivityStats 系统活动统计
type ActivityStats struct {
	// 评论统计
	TotalComments int64 `json:"total_comments"`
	TodayComments int64 `json:"today_comments"`
	WeekComments  int64 `json:"week_comments"`
	
	// 分类统计
	TotalCategories int64 `json:"total_categories"`
	
	// 清理任务统计
	CleanupJobs int64 `json:"cleanup_jobs"`
	LastCleanup time.Time `json:"last_cleanup"`
}

// TimeRangeStats 时间范围统计
type TimeRangeStats struct {
	StartDate time.Time `json:"start_date"`
	EndDate   time.Time `json:"end_date"`
	
	// 工单趋势
	TicketTrend []DailyCount `json:"ticket_trend"`
	
	// 用户活动趋势
	UserActivityTrend []DailyCount `json:"user_activity_trend"`
	
	// 评论趋势
	CommentTrend []DailyCount `json:"comment_trend"`
}

// DailyCount 每日计数
type DailyCount struct {
	Date  time.Time `json:"date"`
	Count int64     `json:"count"`
}

// GetSystemStats 获取系统运行状态
func (s *AnalyticsService) GetSystemStats() (*SystemStats, error) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	
	// 计算运行时间(简化版，实际应该记录启动时间)
	uptime := time.Since(time.Now().Add(-time.Hour)) // 占位符
	
	stats := &SystemStats{
		Uptime:     uptime,
		CPUCount:   runtime.NumCPU(),
		GoVersion:  runtime.Version(),
		ServerTime: time.Now(),
		GoRoutines: runtime.NumGoroutine(),
		CGOCalls:   runtime.NumCgoCall(),
		
		MemStats: MemoryStats{
			HeapAlloc:    m.HeapAlloc,
			HeapSys:      m.HeapSys,
			HeapIdle:     m.HeapIdle,
			HeapInuse:    m.HeapInuse,
			HeapReleased: m.HeapReleased,
			HeapObjects:  m.HeapObjects,
			Sys:          m.Sys,
			Alloc:        m.Alloc,
			TotalAlloc:   m.TotalAlloc,
			StackInuse:   m.StackInuse,
			StackSys:     m.StackSys,
			Mallocs:      m.Mallocs,
			Frees:        m.Frees,
		},
		
		GCStats: GCStats{
			NumGC:         m.NumGC,
			NumForcedGC:   m.NumForcedGC,
			GCCPUFraction: m.GCCPUFraction,
			LastGC:        time.Unix(0, int64(m.LastGC)),
			NextGC:        m.NextGC,
			PauseTotal:    time.Duration(m.PauseTotalNs),
			PauseNs:       []uint64{}, // Empty for now
		},
	}
	
	return stats, nil
}

// GetBusinessStats 获取业务数据统计
func (s *AnalyticsService) GetBusinessStats(ctx context.Context) (*BusinessStats, error) {
	ticketStats, err := s.getTicketStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get ticket stats: %v", err)
	}
	
	userStats, err := s.getUserStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get user stats: %v", err)
	}
	
	activityStats, err := s.getActivityStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get activity stats: %v", err)
	}
	
	return &BusinessStats{
		TicketStats:   *ticketStats,
		UserStats:     *userStats,
		ActivityStats: *activityStats,
	}, nil
}

// getTicketStats 获取工单统计
func (s *AnalyticsService) getTicketStats(ctx context.Context) (*AnalyticsTicketStats, error) {
	var stats AnalyticsTicketStats
	
	// 总工单数
	if err := s.db.WithContext(ctx).Model(&models.Ticket{}).Count(&stats.Total).Error; err != nil {
		return nil, err
	}
	
	// 按状态统计
	statusCounts := []struct {
		Status string
		Count  int64
	}{}
	
	err := s.db.WithContext(ctx).Model(&models.Ticket{}).
		Select("status, count(*) as count").
		Group("status").
		Scan(&statusCounts).Error
	if err != nil {
		return nil, err
	}
	
	for _, sc := range statusCounts {
		switch sc.Status {
		case "open":
			stats.Open = sc.Count
		case "in_progress":
			stats.InProgress = sc.Count
		case "resolved":
			stats.Resolved = sc.Count
		case "closed":
			stats.Closed = sc.Count
		}
	}
	
	// 按优先级统计
	priorityCounts := []struct {
		Priority string
		Count    int64
	}{}
	
	err = s.db.WithContext(ctx).Model(&models.Ticket{}).
		Select("priority, count(*) as count").
		Group("priority").
		Scan(&priorityCounts).Error
	if err != nil {
		return nil, err
	}
	
	for _, pc := range priorityCounts {
		switch pc.Priority {
		case "high":
			stats.HighPriority = pc.Count
		case "medium":
			stats.MediumPriority = pc.Count
		case "low":
			stats.LowPriority = pc.Count
		}
	}
	
	// 按分类统计
	stats.ByCategory = make(map[string]int64)
	categoryCounts := []struct {
		CategoryName string `gorm:"column:category_name"`
		Count        int64  `gorm:"column:count"`
	}{}
	
	err = s.db.WithContext(ctx).Table("tickets t").
		Select("c.name as category_name, count(*) as count").
		Joins("LEFT JOIN categories c ON t.category_id = c.id").
		Group("c.name").
		Scan(&categoryCounts).Error
	if err != nil {
		return nil, err
	}
	
	for _, cc := range categoryCounts {
		if cc.CategoryName != "" {
			stats.ByCategory[cc.CategoryName] = cc.Count
		}
	}
	
	// 时间范围统计
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	weekStart := today.AddDate(0, 0, -int(today.Weekday()))
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	
	// 今日工单
	s.db.WithContext(ctx).Model(&models.Ticket{}).
		Where("created_at >= ?", today).
		Count(&stats.Today)
	
	// 本周工单
	s.db.WithContext(ctx).Model(&models.Ticket{}).
		Where("created_at >= ?", weekStart).
		Count(&stats.ThisWeek)
	
	// 本月工单
	s.db.WithContext(ctx).Model(&models.Ticket{}).
		Where("created_at >= ?", monthStart).
		Count(&stats.ThisMonth)
	
	// 响应时间统计(简化版)
	var avgResponse struct {
		AvgHours float64 `gorm:"column:avg_hours"`
	}
	
	err = s.db.WithContext(ctx).Raw(`
		SELECT AVG(EXTRACT(epoch FROM (updated_at - created_at))/3600) as avg_hours
		FROM tickets 
		WHERE status != 'open' AND updated_at > created_at
	`).Scan(&avgResponse).Error
	
	if err == nil {
		stats.AvgResponseTime = avgResponse.AvgHours
	}
	
	// 解决时间统计
	var avgResolution struct {
		AvgHours float64 `gorm:"column:avg_hours"`
	}
	
	err = s.db.WithContext(ctx).Raw(`
		SELECT AVG(EXTRACT(epoch FROM (updated_at - created_at))/3600) as avg_hours
		FROM tickets 
		WHERE status IN ('resolved', 'closed') AND updated_at > created_at
	`).Scan(&avgResolution).Error
	
	if err == nil {
		stats.AvgResolutionTime = avgResolution.AvgHours
	}
	
	return &stats, nil
}

// getUserStats 获取用户统计
func (s *AnalyticsService) getUserStats(ctx context.Context) (*UserStats, error) {
	var stats UserStats
	
	// 总用户数
	if err := s.db.WithContext(ctx).Model(&models.User{}).Count(&stats.Total).Error; err != nil {
		return nil, err
	}
	
	// 按角色统计
	roleCounts := []struct {
		Role  string
		Count int64
	}{}
	
	err := s.db.WithContext(ctx).Model(&models.User{}).
		Select("role, count(*) as count").
		Group("role").
		Scan(&roleCounts).Error
	if err != nil {
		return nil, err
	}
	
	for _, rc := range roleCounts {
		switch rc.Role {
		case "admin":
			stats.Admins = rc.Count
		case "agent":
			stats.Agents = rc.Count
		case "user":
			stats.Customers = rc.Count
		}
	}
	
	// 活跃用户(最近30天有登录记录)
	thirtyDaysAgo := time.Now().AddDate(0, 0, -30)
	err = s.db.WithContext(ctx).Model(&models.LoginHistory{}).
		Select("COUNT(DISTINCT user_id)").
		Where("login_time >= ?", thirtyDaysAgo).
		Scan(&stats.Active).Error
	if err != nil {
		return nil, err
	}
	
	// 登录统计
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	weekStart := today.AddDate(0, 0, -int(today.Weekday()))
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	
	// 今日登录
	s.db.WithContext(ctx).Model(&models.LoginHistory{}).
		Where("login_time >= ?", today).
		Count(&stats.TodayLogins)
	
	// 本周登录
	s.db.WithContext(ctx).Model(&models.LoginHistory{}).
		Where("login_time >= ?", weekStart).
		Count(&stats.WeekLogins)
	
	// 本月登录
	s.db.WithContext(ctx).Model(&models.LoginHistory{}).
		Where("login_time >= ?", monthStart).
		Count(&stats.MonthLogins)
	
	return &stats, nil
}

// getActivityStats 获取活动统计
func (s *AnalyticsService) getActivityStats(ctx context.Context) (*ActivityStats, error) {
	var stats ActivityStats
	
	// 评论统计
	if err := s.db.WithContext(ctx).Model(&models.TicketComment{}).Count(&stats.TotalComments).Error; err != nil {
		return nil, err
	}
	
	// 分类统计
	if err := s.db.WithContext(ctx).Model(&models.Category{}).Count(&stats.TotalCategories).Error; err != nil {
		return nil, err
	}
	
	// 时间范围统计
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	weekStart := today.AddDate(0, 0, -int(today.Weekday()))
	
	// 今日评论
	s.db.WithContext(ctx).Model(&models.TicketComment{}).
		Where("created_at >= ?", today).
		Count(&stats.TodayComments)
	
	// 本周评论
	s.db.WithContext(ctx).Model(&models.TicketComment{}).
		Where("created_at >= ?", weekStart).
		Count(&stats.WeekComments)
	
	// 清理任务统计(如果存在清理日志表)
	var cleanupCount int64
	err := s.db.WithContext(ctx).Model(&models.CleanupLog{}).Count(&cleanupCount).Error
	if err == nil {
		stats.CleanupJobs = cleanupCount
		
		// 最后清理时间
		var lastCleanup models.CleanupLog
		if err := s.db.WithContext(ctx).Model(&models.CleanupLog{}).
			Order("start_time DESC").First(&lastCleanup).Error; err == nil {
			stats.LastCleanup = lastCleanup.StartTime
		}
	}
	
	return &stats, nil
}

// GetTimeRangeStats 获取指定时间范围的统计数据
func (s *AnalyticsService) GetTimeRangeStats(ctx context.Context, startDate, endDate time.Time) (*TimeRangeStats, error) {
	stats := &TimeRangeStats{
		StartDate: startDate,
		EndDate:   endDate,
	}
	
	// 工单趋势
	ticketTrend, err := s.getDailyTicketTrend(ctx, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("failed to get ticket trend: %v", err)
	}
	stats.TicketTrend = ticketTrend
	
	// 用户活动趋势
	userTrend, err := s.getDailyUserActivityTrend(ctx, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("failed to get user activity trend: %v", err)
	}
	stats.UserActivityTrend = userTrend
	
	// 评论趋势
	commentTrend, err := s.getDailyCommentTrend(ctx, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("failed to get comment trend: %v", err)
	}
	stats.CommentTrend = commentTrend
	
	return stats, nil
}

// getDailyTicketTrend 获取每日工单趋势
func (s *AnalyticsService) getDailyTicketTrend(ctx context.Context, startDate, endDate time.Time) ([]DailyCount, error) {
	var results []struct {
		Date  time.Time `gorm:"column:date"`
		Count int64     `gorm:"column:count"`
	}
	
	err := s.db.WithContext(ctx).Raw(`
		SELECT DATE(created_at) as date, COUNT(*) as count
		FROM tickets 
		WHERE created_at >= ? AND created_at <= ?
		GROUP BY DATE(created_at)
		ORDER BY date
	`, startDate, endDate).Scan(&results).Error
	
	if err != nil {
		return nil, err
	}
	
	trend := make([]DailyCount, len(results))
	for i, r := range results {
		trend[i] = DailyCount{
			Date:  r.Date,
			Count: r.Count,
		}
	}
	
	return trend, nil
}

// getDailyUserActivityTrend 获取每日用户活动趋势
func (s *AnalyticsService) getDailyUserActivityTrend(ctx context.Context, startDate, endDate time.Time) ([]DailyCount, error) {
	var results []struct {
		Date  time.Time `gorm:"column:date"`
		Count int64     `gorm:"column:count"`
	}
	
	err := s.db.WithContext(ctx).Raw(`
		SELECT DATE(login_time) as date, COUNT(DISTINCT user_id) as count
		FROM login_histories 
		WHERE login_time >= ? AND login_time <= ?
		GROUP BY DATE(login_time)
		ORDER BY date
	`, startDate, endDate).Scan(&results).Error
	
	if err != nil {
		return nil, err
	}
	
	trend := make([]DailyCount, len(results))
	for i, r := range results {
		trend[i] = DailyCount{
			Date:  r.Date,
			Count: r.Count,
		}
	}
	
	return trend, nil
}

// getDailyCommentTrend 获取每日评论趋势
func (s *AnalyticsService) getDailyCommentTrend(ctx context.Context, startDate, endDate time.Time) ([]DailyCount, error) {
	var results []struct {
		Date  time.Time `gorm:"column:date"`
		Count int64     `gorm:"column:count"`
	}
	
	err := s.db.WithContext(ctx).Raw(`
		SELECT DATE(created_at) as date, COUNT(*) as count
		FROM ticket_comments 
		WHERE created_at >= ? AND created_at <= ?
		GROUP BY DATE(created_at)
		ORDER BY date
	`, startDate, endDate).Scan(&results).Error
	
	if err != nil {
		return nil, err
	}
	
	trend := make([]DailyCount, len(results))
	for i, r := range results {
		trend[i] = DailyCount{
			Date:  r.Date,
			Count: r.Count,
		}
	}
	
	return trend, nil
}

// GetDB 获取数据库实例(用于健康检查等)
func (s *AnalyticsService) GetDB() *gorm.DB {
	return s.db
}

// ExportStats 导出统计数据
func (s *AnalyticsService) ExportStats(ctx context.Context, format string, startDate, endDate *time.Time) ([]byte, error) {
	// 获取系统统计
	systemStats, err := s.GetSystemStats()
	if err != nil {
		return nil, fmt.Errorf("failed to get system stats: %v", err)
	}
	
	// 获取业务统计
	businessStats, err := s.GetBusinessStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get business stats: %v", err)
	}
	
	// 构建导出数据
	exportData := map[string]interface{}{
		"export_time":    time.Now(),
		"system_stats":   systemStats,
		"business_stats": businessStats,
	}
	
	// 如果指定了时间范围，添加趋势数据
	if startDate != nil && endDate != nil {
		timeRangeStats, err := s.GetTimeRangeStats(ctx, *startDate, *endDate)
		if err != nil {
			return nil, fmt.Errorf("failed to get time range stats: %v", err)
		}
		exportData["time_range_stats"] = timeRangeStats
	}
	
	// 目前只支持JSON格式
	if format != "json" {
		return nil, fmt.Errorf("unsupported export format: %s", format)
	}
	
	return json.MarshalIndent(exportData, "", "  ")
}