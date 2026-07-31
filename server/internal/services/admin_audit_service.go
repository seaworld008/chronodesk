package services

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

// AdminAuditRecord 审计日志记录输入
// swagger:model AdminAuditRecord
type AdminAuditRecord struct {
	ID               uint
	UserID           *uint
	Username         string
	PlatformRole     models.PlatformRole
	Action           string
	ActionCode       string
	ResourceType     string
	ResourcePublicID string
	Method           string
	Path             string
	StatusCode       int
	ClientIP         string
	UserAgent        string
	Query            string
	Latency          time.Duration
	Result           string
	Notes            string
	RequestID        string
	TraceID          string
	CorrelationID    string
}

// AdminAuditFilter 审计日志查询过滤条件
type AdminAuditFilter struct {
	UserID       *uint
	Actor        string
	PlatformRole models.PlatformRole
	Action       string
	Method       string
	Path         string
	Status       *int
	Result       string
	Keyword      string
	StartTime    *time.Time
	EndTime      *time.Time
	TimePreset   string
	Page         int
	Limit        int
	Cursor       string
}

// AdminAuditListItem 审计日志列表项
type AdminAuditListItem struct {
	ID               uint                `json:"id"`
	CreatedAt        time.Time           `json:"created_at"`
	UserID           *uint               `json:"user_id,omitempty"`
	Username         string              `json:"username"`
	PlatformRole     models.PlatformRole `json:"platform_role"`
	Action           string              `json:"action"`
	ActionCode       string              `json:"action_code,omitempty"`
	ResourceType     string              `json:"resource_type,omitempty"`
	ResourcePublicID string              `json:"resource_public_id,omitempty"`
	Method           string              `json:"method"`
	Path             string              `json:"path"`
	StatusCode       int                 `json:"status_code"`
	MaskedIP         string              `json:"masked_ip"`
	LatencyMs        int64               `json:"latency_ms"`
	Result           string              `json:"result"`
}

// AdminAuditDetail exposes long diagnostic fields only on explicit row reads.
// Every untrusted field is redacted at read time.
type AdminAuditDetail struct {
	AdminAuditListItem
	Query         string `json:"query"`
	UserAgent     string `json:"user_agent"`
	Notes         string `json:"notes"`
	RequestID     string `json:"request_id,omitempty"`
	TraceID       string `json:"trace_id,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

type AdminAuditPage struct {
	Items      []*AdminAuditListItem `json:"items"`
	Total      int64                 `json:"total"`
	Page       int                   `json:"page"`
	Limit      int                   `json:"limit"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

var (
	ErrInvalidAdminAuditCursor = errors.New("admin audit cursor is invalid")
	ErrAdminAuditNotFound      = errors.New("admin audit log was not found")
)

const (
	DefaultAdminAuditLimit = 25
	MaxAdminAuditLimit     = 100
)

type adminAuditCursor struct {
	Version    int    `json:"v"`
	CreatedAt  string `json:"created_at"`
	ID         uint   `json:"id"`
	FilterHash string `json:"filter_hash"`
	StartTime  string `json:"start_time,omitempty"`
	EndTime    string `json:"end_time,omitempty"`
	Checksum   string `json:"checksum"`
}

// AdminAuditServiceInterface 定义服务接口
type AdminAuditServiceInterface interface {
	Record(ctx context.Context, record *AdminAuditRecord) error
	Finalize(ctx context.Context, record *AdminAuditRecord) error
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
		UserID:           record.UserID,
		Username:         strings.TrimSpace(record.Username),
		PlatformRole:     record.PlatformRole,
		Action:           strings.TrimSpace(record.Action),
		ActionCode:       strings.TrimSpace(record.ActionCode),
		ResourceType:     strings.TrimSpace(record.ResourceType),
		ResourcePublicID: strings.TrimSpace(record.ResourcePublicID),
		Method:           strings.ToUpper(strings.TrimSpace(record.Method)),
		Path:             record.Path,
		StatusCode:       record.StatusCode,
		ClientIP:         record.ClientIP,
		UserAgent:        record.UserAgent,
		Query:            record.Query,
		LatencyMs:        record.Latency.Milliseconds(),
		Result:           record.Result,
		Notes:            record.Notes,
		RequestID:        strings.TrimSpace(record.RequestID),
		TraceID:          strings.TrimSpace(record.TraceID),
		CorrelationID:    strings.TrimSpace(record.CorrelationID),
	}

	if auditLog.Method == "" {
		auditLog.Method = "UNKNOWN"
	}

	// 如果未提供用户名或角色，则尝试从数据库读取
	if auditLog.UserID != nil &&
		(auditLog.Username == "" || !auditLog.PlatformRole.IsValid()) {
		var user models.User
		if err := s.db.WithContext(ctx).
			Select("id", "username", "platform_role").
			First(&user, *auditLog.UserID).Error; err == nil {
			if auditLog.Username == "" {
				auditLog.Username = user.Username
			}
			if !auditLog.PlatformRole.IsValid() {
				auditLog.PlatformRole = user.PlatformRole
			}
		}
	}
	if !auditLog.PlatformRole.IsValid() {
		return errors.New("audit platform role is invalid")
	}

	if err := s.db.WithContext(ctx).Create(auditLog).Error; err != nil {
		return err
	}
	record.ID = auditLog.ID
	return nil
}

// Finalize completes a durable pre-write audit anchor after the handler
// returns. If finalization fails, the original pending row remains observable;
// a successful administrative mutation can therefore never exist without an
// audit record.
func (s *AdminAuditService) Finalize(
	ctx context.Context,
	record *AdminAuditRecord,
) error {
	if record == nil || record.ID == 0 {
		return errors.New("persisted audit record is required")
	}
	result := s.db.WithContext(ctx).
		Model(&models.AdminAuditLog{}).
		Where("id = ?", record.ID).
		Updates(map[string]any{
			"status_code": record.StatusCode,
			"latency_ms":  record.Latency.Milliseconds(),
			"result":      strings.TrimSpace(record.Result),
			"notes":       record.Notes,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("persisted audit record was not found")
	}
	return nil
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
	if filter.Actor != "" {
		query = query.Where("LOWER(username) = ?", strings.ToLower(filter.Actor))
	}
	if filter.PlatformRole != "" {
		if !filter.PlatformRole.IsValid() {
			return nil, 0, errors.New("audit platform role is invalid")
		}
		query = query.Where("platform_role = ?", filter.PlatformRole)
	}
	if filter.Method != "" {
		query = query.Where("method = ?", strings.ToUpper(filter.Method))
	}
	if filter.Action != "" {
		query = query.Where(
			"(action_code = ? OR (action_code = '' AND action = ?))",
			filter.Action,
			filter.Action,
		)
	}
	if filter.Path != "" {
		query = query.Where("path LIKE ?", filter.Path+"%")
	}
	if filter.Status != nil {
		query = query.Where("status_code = ?", *filter.Status)
	}
	if filter.Result != "" {
		query = query.Where("result = ?", filter.Result)
	}
	if filter.Keyword != "" {
		like := "%" + escapeAuditLike(strings.ToLower(filter.Keyword)) + "%"
		query = query.Where(
			"(LOWER(username) LIKE ? ESCAPE '\\' OR LOWER(path) LIKE ? ESCAPE '\\' OR LOWER(action) LIKE ? ESCAPE '\\' OR LOWER(action_code) LIKE ? ESCAPE '\\' OR LOWER(resource_public_id) LIKE ? ESCAPE '\\')",
			like, like, like, like, like,
		)
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
	if limit <= 0 || limit > MaxAdminAuditLimit {
		limit = DefaultAdminAuditLimit
	}
	offset := (page - 1) * limit
	filter.Page = page
	filter.Limit = limit

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var logs []*models.AdminAuditLog
	if err := query.Order("created_at DESC").Order("id DESC").Offset(offset).Limit(limit).Find(&logs).Error; err != nil {
		return nil, 0, err
	}

	return logs, total, nil
}

// Explore returns either the legacy numbered page or a stable opaque-cursor
// page. Cursor pages use the same filters and (created_at DESC, id DESC) order.
func (s *AdminAuditService) Explore(
	ctx context.Context,
	filter *AdminAuditFilter,
) (*AdminAuditPage, error) {
	if filter == nil {
		filter = &AdminAuditFilter{}
	}
	if filter.Page == 0 {
		filter.Page = 1
	}
	if filter.Limit == 0 {
		filter.Limit = DefaultAdminAuditLimit
	}

	var cursorTime time.Time
	var cursorID uint
	if filter.Cursor != "" {
		var cursorStart *time.Time
		var cursorEnd *time.Time
		var err error
		cursorTime, cursorID, cursorStart, cursorEnd, err =
			decodeAdminAuditCursor(
				filter.Cursor,
				adminAuditFilterHash(filter),
			)
		if err != nil {
			return nil, err
		}
		if filter.TimePreset != "" {
			filter.StartTime = cursorStart
			filter.EndTime = cursorEnd
		}
	}

	query := s.filteredQuery(ctx, filter)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}

	if filter.Cursor != "" {
		query = query.Where(
			"(created_at < ? OR (created_at = ? AND id < ?))",
			cursorTime,
			cursorTime,
			cursorID,
		)
	}

	var logs []*models.AdminAuditLog
	if err := query.
		Order("created_at DESC").
		Order("id DESC").
		Limit(filter.Limit + 1).
		Find(&logs).Error; err != nil {
		return nil, err
	}
	nextCursor := ""
	if len(logs) > filter.Limit {
		last := logs[filter.Limit-1]
		nextCursor = encodeAdminAuditCursor(
			last.CreatedAt,
			last.ID,
			adminAuditFilterHash(filter),
			filter.StartTime,
			filter.EndTime,
		)
		logs = logs[:filter.Limit]
	}
	return &AdminAuditPage{
		Items:      ConvertAuditLogs(logs),
		Total:      total,
		Page:       filter.Page,
		Limit:      filter.Limit,
		NextCursor: nextCursor,
	}, nil
}

func (s *AdminAuditService) GetDetail(
	ctx context.Context,
	id uint,
) (*AdminAuditDetail, error) {
	var log models.AdminAuditLog
	if err := s.db.WithContext(ctx).First(&log, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAdminAuditNotFound
		}
		return nil, err
	}
	item := convertAuditLog(&log)
	return &AdminAuditDetail{
		AdminAuditListItem: *item,
		Query:              redactAuditQuery(log.Query),
		UserAgent:          redactAuditText(log.UserAgent, 512),
		Notes:              redactAuditText(log.Notes, 4000),
		RequestID:          redactAuditIdentifier(log.RequestID),
		TraceID:            redactAuditIdentifier(log.TraceID),
		CorrelationID:      redactAuditIdentifier(log.CorrelationID),
	}, nil
}

func (s *AdminAuditService) filteredQuery(
	ctx context.Context,
	filter *AdminAuditFilter,
) *gorm.DB {
	query := s.db.WithContext(ctx).Model(&models.AdminAuditLog{})
	if filter.UserID != nil {
		query = query.Where("user_id = ?", *filter.UserID)
	}
	if filter.Actor != "" {
		query = query.Where("LOWER(username) = ?", strings.ToLower(filter.Actor))
	}
	if filter.PlatformRole != "" {
		query = query.Where("platform_role = ?", filter.PlatformRole)
	}
	if filter.Action != "" {
		query = query.Where(
			"(action_code = ? OR (action_code = '' AND action = ?))",
			filter.Action,
			filter.Action,
		)
	}
	if filter.Method != "" {
		query = query.Where("method = ?", strings.ToUpper(filter.Method))
	}
	if filter.Path != "" {
		query = query.Where(
			"path LIKE ? ESCAPE '\\'",
			escapeAuditLike(filter.Path)+"%",
		)
	}
	if filter.Status != nil {
		query = query.Where("status_code = ?", *filter.Status)
	}
	if filter.Result != "" {
		query = query.Where("result = ?", filter.Result)
	}
	if filter.Keyword != "" {
		like := "%" + escapeAuditLike(strings.ToLower(filter.Keyword)) + "%"
		query = query.Where(
			"(LOWER(username) LIKE ? ESCAPE '\\' OR LOWER(path) LIKE ? ESCAPE '\\' OR LOWER(action) LIKE ? ESCAPE '\\' OR LOWER(action_code) LIKE ? ESCAPE '\\' OR LOWER(resource_public_id) LIKE ? ESCAPE '\\')",
			like, like, like, like, like,
		)
	}
	if filter.StartTime != nil {
		query = query.Where("created_at >= ?", *filter.StartTime)
	}
	if filter.EndTime != nil {
		query = query.Where("created_at <= ?", *filter.EndTime)
	}
	return query
}

// ConvertAuditLogs 转换为响应结构
func ConvertAuditLogs(logs []*models.AdminAuditLog) []*AdminAuditListItem {
	if len(logs) == 0 {
		return []*AdminAuditListItem{}
	}

	items := make([]*AdminAuditListItem, len(logs))
	for i, log := range logs {
		items[i] = convertAuditLog(log)
	}
	return items
}

func convertAuditLog(log *models.AdminAuditLog) *AdminAuditListItem {
	action := strings.TrimSpace(log.ActionCode)
	if action == "" {
		action = strings.TrimSpace(log.Action)
	}
	if action == "" {
		action = strings.TrimSpace(log.Method + " " + log.Path)
	}
	path := strings.TrimSpace(log.Path)
	if path == "" && log.ResourceType != "" {
		path = log.ResourceType
		if log.ResourcePublicID != "" {
			path += "/" + log.ResourcePublicID
		}
	}
	return &AdminAuditListItem{
		ID:               log.ID,
		CreatedAt:        log.CreatedAt,
		UserID:           log.UserID,
		Username:         log.Username,
		PlatformRole:     log.PlatformRole,
		Action:           action,
		ActionCode:       log.ActionCode,
		ResourceType:     log.ResourceType,
		ResourcePublicID: log.ResourcePublicID,
		Method:           log.Method,
		Path:             path,
		StatusCode:       log.StatusCode,
		MaskedIP:         maskAuditIP(log.ClientIP),
		LatencyMs:        log.LatencyMs,
		Result:           log.Result,
	}
}

func escapeAuditLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

func adminAuditFilterHash(filter *AdminAuditFilter) string {
	type hashInput struct {
		Limit        int                 `json:"limit"`
		UserID       *uint               `json:"user_id,omitempty"`
		Actor        string              `json:"actor,omitempty"`
		PlatformRole models.PlatformRole `json:"platform_role,omitempty"`
		Action       string              `json:"action,omitempty"`
		Method       string              `json:"method,omitempty"`
		Path         string              `json:"path,omitempty"`
		Status       *int                `json:"status,omitempty"`
		Result       string              `json:"result,omitempty"`
		Keyword      string              `json:"keyword,omitempty"`
		TimePreset   string              `json:"time_preset,omitempty"`
		StartTime    string              `json:"start_time,omitempty"`
		EndTime      string              `json:"end_time,omitempty"`
	}
	input := hashInput{
		Limit:        filter.Limit,
		UserID:       filter.UserID,
		Actor:        filter.Actor,
		PlatformRole: filter.PlatformRole,
		Action:       filter.Action,
		Method:       strings.ToUpper(filter.Method),
		Path:         filter.Path,
		Status:       filter.Status,
		Result:       filter.Result,
		Keyword:      filter.Keyword,
		TimePreset:   filter.TimePreset,
	}
	if filter.StartTime != nil && filter.TimePreset == "" {
		input.StartTime = filter.StartTime.UTC().Format(time.RFC3339Nano)
	}
	if filter.EndTime != nil && filter.TimePreset == "" {
		input.EndTime = filter.EndTime.UTC().Format(time.RFC3339Nano)
	}
	payload, _ := json.Marshal(input)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func encodeAdminAuditCursor(
	createdAt time.Time,
	id uint,
	filterHash string,
	startTime *time.Time,
	endTime *time.Time,
) string {
	cursor := adminAuditCursor{
		Version:    1,
		CreatedAt:  createdAt.UTC().Format(time.RFC3339Nano),
		ID:         id,
		FilterHash: filterHash,
	}
	if startTime != nil {
		cursor.StartTime = startTime.UTC().Format(time.RFC3339Nano)
	}
	if endTime != nil {
		cursor.EndTime = endTime.UTC().Format(time.RFC3339Nano)
	}
	cursor.Checksum = adminAuditCursorChecksum(cursor)
	payload, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeAdminAuditCursor(
	raw string,
	filterHash string,
) (time.Time, uint, *time.Time, *time.Time, error) {
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(payload) > 1024 {
		return time.Time{}, 0, nil, nil, ErrInvalidAdminAuditCursor
	}
	var cursor adminAuditCursor
	if err := json.Unmarshal(payload, &cursor); err != nil ||
		cursor.Version != 1 ||
		cursor.ID == 0 ||
		cursor.FilterHash != filterHash ||
		cursor.Checksum != adminAuditCursorChecksum(cursor) {
		return time.Time{}, 0, nil, nil, ErrInvalidAdminAuditCursor
	}
	createdAt, err := time.Parse(time.RFC3339Nano, cursor.CreatedAt)
	if err != nil {
		return time.Time{}, 0, nil, nil, ErrInvalidAdminAuditCursor
	}
	startTime, err := optionalAdminAuditCursorTime(cursor.StartTime)
	if err != nil {
		return time.Time{}, 0, nil, nil, ErrInvalidAdminAuditCursor
	}
	endTime, err := optionalAdminAuditCursorTime(cursor.EndTime)
	if err != nil {
		return time.Time{}, 0, nil, nil, ErrInvalidAdminAuditCursor
	}
	return createdAt, cursor.ID, startTime, endTime, nil
}

func optionalAdminAuditCursorTime(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func adminAuditCursorChecksum(cursor adminAuditCursor) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf(
		"chronodesk-admin-audit-cursor-v1\x00%d\x00%s\x00%d\x00%s\x00%s\x00%s",
		cursor.Version,
		cursor.CreatedAt,
		cursor.ID,
		cursor.FilterHash,
		cursor.StartTime,
		cursor.EndTime,
	)))
	return hex.EncodeToString(sum[:])
}

func maskAuditIP(value string) string {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil {
		if strings.TrimSpace(value) == "" {
			return ""
		}
		return "已隐藏"
	}
	if ipv4 := ip.To4(); ipv4 != nil {
		return fmt.Sprintf("%d.%d.*.*", ipv4[0], ipv4[1])
	}
	masked := ip.Mask(net.CIDRMask(48, 128))
	return masked.String() + "/48"
}

var auditSecretPattern = regexp.MustCompile(
	`(?i)(authorization|cookie|password|passwd|token|secret|api[_-]?key)(\s*[:=]\s*)([^\s,;]+)`,
)

func redactAuditText(value string, maxLength int) string {
	value = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			return r
		}
		return -1
	}, value)
	value = auditSecretPattern.ReplaceAllString(value, "$1$2[已隐藏]")
	runes := []rune(value)
	if len(runes) > maxLength {
		value = string(runes[:maxLength]) + "…"
	}
	return value
}

func redactAuditQuery(raw string) string {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "[内容已隐藏]"
	}
	for key := range values {
		if auditSecretPattern.MatchString(key + "=value") {
			values[key] = []string{"[已隐藏]"}
		} else {
			for index, value := range values[key] {
				values[key][index] = redactAuditText(value, 512)
			}
		}
	}
	return values.Encode()
}

func redactAuditIdentifier(value string) string {
	return redactAuditText(strings.TrimSpace(value), 255)
}
