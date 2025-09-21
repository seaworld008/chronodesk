package services

import (
	"context"
	"errors"
	"strings"
	"time"

	"gongdan-system/internal/models"
	"gorm.io/gorm"
)

// AdminAuditRecord 审计日志记录输入
// swagger:model AdminAuditRecord
type AdminAuditRecord struct {
	UserID     *uint
	Username   string
	Role       string
	Action     string
	Method     string
	Path       string
	StatusCode int
	ClientIP   string
	UserAgent  string
	Query      string
	Latency    time.Duration
	Result     string
	Notes      string
}

// AdminAuditFilter 审计日志查询过滤条件
type AdminAuditFilter struct {
	UserID    *uint
	Role      string
	Method    string
	Path      string
	Status    *int
	Keyword   string
	StartTime *time.Time
	EndTime   *time.Time
	Page      int
	Limit     int
}

// AdminAuditListItem 审计日志列表项
type AdminAuditListItem struct {
	ID         uint      `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	UserID     *uint     `json:"user_id,omitempty"`
	Username   string    `json:"username"`
	Role       string    `json:"role"`
	Action     string    `json:"action"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	StatusCode int       `json:"status_code"`
	ClientIP   string    `json:"client_ip"`
	UserAgent  string    `json:"user_agent"`
	Query      string    `json:"query"`
	LatencyMs  int64     `json:"latency_ms"`
	Result     string    `json:"result"`
	Notes      string    `json:"notes"`
}

// AdminAuditServiceInterface 定义服务接口
type AdminAuditServiceInterface interface {
	Record(ctx context.Context, record *AdminAuditRecord) error
	List(ctx context.Context, filter *AdminAuditFilter) ([]*models.AdminAuditLog, int64, error)
}

// AdminAuditService 管理员审计日志服务
type AdminAuditService struct {
	db *gorm.DB
}

// NewAdminAuditService 创建新的审计日志服务
func NewAdminAuditService(db *gorm.DB) *AdminAuditService {
	return &AdminAuditService{db: db}
}

// Record 记录管理员操作日志
func (s *AdminAuditService) Record(ctx context.Context, record *AdminAuditRecord) error {
	if record == nil {
		return errors.New("audit record cannot be nil")
	}

	auditLog := &models.AdminAuditLog{
		UserID:     record.UserID,
		Username:   strings.TrimSpace(record.Username),
		Role:       strings.TrimSpace(record.Role),
		Action:     strings.TrimSpace(record.Action),
		Method:     strings.ToUpper(strings.TrimSpace(record.Method)),
		Path:       record.Path,
		StatusCode: record.StatusCode,
		ClientIP:   record.ClientIP,
		UserAgent:  record.UserAgent,
		Query:      record.Query,
		LatencyMs:  record.Latency.Milliseconds(),
		Result:     record.Result,
		Notes:      record.Notes,
	}

	if auditLog.Method == "" {
		auditLog.Method = "UNKNOWN"
	}

	// 如果未提供用户名或角色，则尝试从数据库读取
	if auditLog.UserID != nil && (auditLog.Username == "" || auditLog.Role == "") {
		var user models.User
		if err := s.db.WithContext(ctx).Select("id", "username", "role").First(&user, *auditLog.UserID).Error; err == nil {
			if auditLog.Username == "" {
				auditLog.Username = user.Username
			}
			if auditLog.Role == "" {
				auditLog.Role = string(user.Role)
			}
		}
	}

	return s.db.WithContext(ctx).Create(auditLog).Error
}

// List 获取管理员操作日志列表
func (s *AdminAuditService) List(ctx context.Context, filter *AdminAuditFilter) ([]*models.AdminAuditLog, int64, error) {
	if filter == nil {
		filter = &AdminAuditFilter{}
	}

	query := s.db.WithContext(ctx).Model(&models.AdminAuditLog{})

	if filter.UserID != nil {
		query = query.Where("user_id = ?", *filter.UserID)
	}
	if filter.Role != "" {
		query = query.Where("LOWER(role) = ?", strings.ToLower(filter.Role))
	}
	if filter.Method != "" {
		query = query.Where("method = ?", strings.ToUpper(filter.Method))
	}
	if filter.Path != "" {
		query = query.Where("path LIKE ?", filter.Path+"%")
	}
	if filter.Status != nil {
		query = query.Where("status_code = ?", *filter.Status)
	}
	if filter.Keyword != "" {
		like := "%" + filter.Keyword + "%"
		query = query.Where("username ILIKE ? OR path ILIKE ? OR action ILIKE ?", like, like, like)
	}
	if filter.StartTime != nil {
		query = query.Where("created_at >= ?", *filter.StartTime)
	}
	if filter.EndTime != nil {
		query = query.Where("created_at <= ?", *filter.EndTime)
	}

	page := filter.Page
	if page < 1 {
		page = 1
	}
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	offset := (page - 1) * limit
	filter.Page = page
	filter.Limit = limit

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var logs []*models.AdminAuditLog
	if err := query.Order("created_at DESC").Offset(offset).Limit(limit).Find(&logs).Error; err != nil {
		return nil, 0, err
	}

	return logs, total, nil
}

// ConvertAuditLogs 转换为响应结构
func ConvertAuditLogs(logs []*models.AdminAuditLog) []*AdminAuditListItem {
	if len(logs) == 0 {
		return []*AdminAuditListItem{}
	}

	items := make([]*AdminAuditListItem, len(logs))
	for i, log := range logs {
		item := &AdminAuditListItem{
			ID:         log.ID,
			CreatedAt:  log.CreatedAt,
			UserID:     log.UserID,
			Username:   log.Username,
			Role:       log.Role,
			Action:     log.Action,
			Method:     log.Method,
			Path:       log.Path,
			StatusCode: log.StatusCode,
			ClientIP:   log.ClientIP,
			UserAgent:  log.UserAgent,
			Query:      log.Query,
			LatencyMs:  log.LatencyMs,
			Result:     log.Result,
			Notes:      log.Notes,
		}
		items[i] = item
	}

	return items
}
