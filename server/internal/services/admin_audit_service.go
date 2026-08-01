package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

// AdminAuditRecord 审计日志记录输入
// swagger:model AdminAuditRecord
type AdminAuditRecord struct {
	ID               uint
	Actor            models.ActorRef
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
	ActorType        models.ActorType    `json:"actor_type"`
	ActorID          string              `json:"actor_id"`
	UserID           *uint               `json:"user_id,omitempty"`
	Username         string              `json:"username"`
	PlatformRole     models.PlatformRole `json:"platform_role,omitempty"`
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
	NextCursor string                `json:"next_cursor"`
	HasMore    bool                  `json:"has_more"`
}

var (
	ErrInvalidAdminAuditCursor = errors.New("admin audit cursor is invalid")
	ErrInvalidAdminAuditLimit  = errors.New("admin audit limit is invalid")
	ErrAdminAuditNotFound      = errors.New("admin audit log was not found")
	ErrAdminAuditCursorKey     = errors.New("admin audit cursor signing key is invalid")
)

const (
	DefaultAdminAuditLimit = 25
	MaxAdminAuditLimit     = 100

	adminAuditActorTypeMaxRunes        = 32
	adminAuditActorIDMaxRunes          = 128
	adminAuditUsernameMaxRunes         = 100
	adminAuditPlatformRoleMaxRunes     = 30
	adminAuditActionMaxRunes           = 255
	adminAuditActionCodeMaxRunes       = 100
	adminAuditResourceTypeMaxRunes     = 100
	adminAuditResourcePublicIDMaxRunes = 512
	adminAuditMethodMaxRunes           = 20
	adminAuditPathMaxRunes             = 512
	adminAuditResultMaxRunes           = 100
	adminAuditQueryMaxRunes            = 2000
	adminAuditUserAgentMaxRunes        = 512
	adminAuditNotesMaxRunes            = 4000
	adminAuditRequestIDMaxRunes        = 128
	adminAuditTraceIDMaxRunes          = 128
	adminAuditCorrelationIDMaxRunes    = 255
	adminAuditStructuredValueMaxRunes  = 2000
)

type adminAuditCursor struct {
	Version    int    `json:"v"`
	CreatedAt  string `json:"created_at"`
	ID         uint   `json:"id"`
	FilterHash string `json:"filter_hash"`
	StartTime  string `json:"start_time,omitempty"`
	EndTime    string `json:"end_time,omitempty"`
}

// AdminAuditServiceInterface 定义服务接口
type AdminAuditServiceInterface interface {
	Record(ctx context.Context, record *AdminAuditRecord) error
	Finalize(ctx context.Context, record *AdminAuditRecord) error
	List(ctx context.Context, filter *AdminAuditFilter) ([]*models.AdminAuditLog, int64, error)
}

// AdminAuditService 管理员审计日志服务
type AdminAuditService struct {
	db               *gorm.DB
	cursorSigningKey []byte
}

// NewAdminAuditService 创建新的审计日志服务
func NewAdminAuditService(db *gorm.DB) *AdminAuditService {
	return &AdminAuditService{db: db}
}

// NewAdminAuditServiceWithCursorKey derives a domain-separated HMAC key from a
// stable deployment-owned secret. Tests inject an explicit key through the
// same constructor; no cursor key is hard-coded or generated at process start.
func NewAdminAuditServiceWithCursorKey(
	db *gorm.DB,
	rootKey []byte,
) (*AdminAuditService, error) {
	if db == nil || len(rootKey) < 32 {
		return nil, ErrAdminAuditCursorKey
	}
	deriver := hmac.New(sha256.New, rootKey)
	_, _ = deriver.Write([]byte("chronodesk/admin-audit-cursor/v1"))
	return &AdminAuditService{
		db:               db,
		cursorSigningKey: deriver.Sum(nil),
	}, nil
}

// Record 记录管理员操作日志
func (s *AdminAuditService) Record(ctx context.Context, record *AdminAuditRecord) error {
	if record == nil {
		return errors.New("audit record cannot be nil")
	}
	actor, userID, platformRole, username, err :=
		s.normalizeAdminAuditActor(ctx, record)
	if err != nil {
		return err
	}

	auditLog := &models.AdminAuditLog{
		ActorType:        actor.Type,
		ActorID:          actor.ID,
		UserID:           userID,
		Username:         username,
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
	if platformRole != "" {
		auditLog.PlatformRole = &platformRole
	}

	if auditLog.Method == "" {
		auditLog.Method = "UNKNOWN"
	}

	if err := s.db.WithContext(ctx).Create(auditLog).Error; err != nil {
		return err
	}
	record.ID = auditLog.ID
	return nil
}

func (s *AdminAuditService) normalizeAdminAuditActor(
	ctx context.Context,
	record *AdminAuditRecord,
) (
	models.ActorRef,
	*uint,
	models.PlatformRole,
	string,
	error,
) {
	actor := record.Actor
	if actor.Type == "" && strings.TrimSpace(actor.ID) == "" &&
		record.UserID != nil {
		actor = models.HumanActor(*record.UserID)
	}
	if err := actor.Validate(); err != nil {
		return models.ActorRef{}, nil, "", "", fmt.Errorf(
			"audit actor is invalid: %w",
			err,
		)
	}
	username := strings.TrimSpace(record.Username)
	platformRole := record.PlatformRole
	switch actor.Type {
	case models.ActorTypeHuman:
		parsed, err := strconv.ParseUint(actor.ID, 10, 64)
		if err != nil || parsed == 0 || uint64(uint(parsed)) != parsed {
			return models.ActorRef{}, nil, "", "", errors.New(
				"human audit actor id is invalid",
			)
		}
		humanID := uint(parsed)
		if record.UserID != nil && *record.UserID != humanID {
			return models.ActorRef{}, nil, "", "", errors.New(
				"human audit actor does not match user id",
			)
		}
		userID := &humanID
		if username == "" || !platformRole.IsValid() {
			var user models.User
			if err := s.db.WithContext(ctx).
				Select("id", "username", "platform_role").
				First(&user, humanID).Error; err != nil {
				return models.ActorRef{}, nil, "", "", errors.New(
					"human audit actor could not be resolved",
				)
			}
			if username == "" {
				username = user.Username
			}
			if !platformRole.IsValid() {
				platformRole = user.PlatformRole
			}
		}
		if !platformRole.IsValid() {
			return models.ActorRef{}, nil, "", "", errors.New(
				"audit platform role is invalid",
			)
		}
		return actor, userID, platformRole, username, nil
	case models.ActorTypeSystem, models.ActorTypeServicePrincipal:
		if record.UserID != nil || platformRole != "" {
			return models.ActorRef{}, nil, "", "", errors.New(
				"non-human audit actors cannot claim a human platform role",
			)
		}
		if username == "" {
			username = actor.ID
		}
		return actor, nil, "", username, nil
	default:
		return models.ActorRef{}, nil, "", "", errors.New(
			"audit actor type is invalid",
		)
	}
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

// Explore returns a stable opaque-cursor page in
// (created_at DESC, id DESC) order. The cursor binds the scope, filters,
// effective time window, limit and order so it cannot be reused elsewhere.
func (s *AdminAuditService) Explore(
	ctx context.Context,
	filter *AdminAuditFilter,
) (*AdminAuditPage, error) {
	if filter == nil {
		filter = &AdminAuditFilter{}
	}
	if filter.Limit == 0 {
		filter.Limit = DefaultAdminAuditLimit
	}
	if filter.Limit < 1 || filter.Limit > MaxAdminAuditLimit {
		return nil, ErrInvalidAdminAuditLimit
	}
	if len(s.cursorSigningKey) != sha256.Size {
		return nil, ErrAdminAuditCursorKey
	}

	var cursorTime time.Time
	var cursorID uint
	if filter.Cursor != "" {
		var cursorStart *time.Time
		var cursorEnd *time.Time
		var err error
		cursorTime, cursorID, cursorStart, cursorEnd, err =
			s.decodeAdminAuditCursor(
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
	hasMore := len(logs) > filter.Limit
	nextCursor := ""
	if hasMore {
		last := logs[filter.Limit-1]
		nextCursor = s.encodeAdminAuditCursor(
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
		NextCursor: nextCursor,
		HasMore:    hasMore,
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
		UserAgent: redactAuditText(
			log.UserAgent,
			adminAuditUserAgentMaxRunes,
		),
		Notes: redactAuditText(
			log.Notes,
			adminAuditNotesMaxRunes,
		),
		RequestID: redactAuditIdentifier(
			log.RequestID,
			adminAuditRequestIDMaxRunes,
		),
		TraceID: redactAuditIdentifier(
			log.TraceID,
			adminAuditTraceIDMaxRunes,
		),
		CorrelationID: redactAuditIdentifier(
			log.CorrelationID,
			adminAuditCorrelationIDMaxRunes,
		),
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
	actorType := models.ActorType(redactAuditText(
		string(log.ActorType),
		adminAuditActorTypeMaxRunes,
	))
	actorID := redactAuditText(log.ActorID, adminAuditActorIDMaxRunes)
	if actorType == "" && log.UserID != nil {
		actorType = models.ActorTypeHuman
		actorID = strconv.FormatUint(uint64(*log.UserID), 10)
	}

	username := redactAuditText(
		log.Username,
		adminAuditUsernameMaxRunes,
	)
	actionCode := redactAuditText(
		log.ActionCode,
		adminAuditActionCodeMaxRunes,
	)
	resourceType := redactAuditText(
		log.ResourceType,
		adminAuditResourceTypeMaxRunes,
	)
	resourcePublicID := redactAuditText(
		log.ResourcePublicID,
		adminAuditResourcePublicIDMaxRunes,
	)
	method := redactAuditText(log.Method, adminAuditMethodMaxRunes)
	path := strings.TrimSpace(log.Path)
	if path == "" && resourceType != "" {
		path = resourceType
		if resourcePublicID != "" {
			path += "/" + resourcePublicID
		}
	}
	path = redactAuditText(path, adminAuditPathMaxRunes)

	action := actionCode
	if action == "" {
		action = redactAuditText(log.Action, adminAuditActionMaxRunes)
	}
	if action == "" {
		action = redactAuditText(
			strings.TrimSpace(method+" "+path),
			adminAuditActionMaxRunes,
		)
	}
	item := &AdminAuditListItem{
		ID:               log.ID,
		CreatedAt:        log.CreatedAt,
		ActorType:        actorType,
		ActorID:          actorID,
		UserID:           log.UserID,
		Username:         username,
		Action:           action,
		ActionCode:       actionCode,
		ResourceType:     resourceType,
		ResourcePublicID: resourcePublicID,
		Method:           method,
		Path:             path,
		StatusCode:       log.StatusCode,
		MaskedIP:         maskAuditIP(log.ClientIP),
		LatencyMs:        log.LatencyMs,
		Result: redactAuditText(
			log.Result,
			adminAuditResultMaxRunes,
		),
	}
	if log.PlatformRole != nil {
		item.PlatformRole = models.PlatformRole(redactAuditText(
			string(*log.PlatformRole),
			adminAuditPlatformRoleMaxRunes,
		))
	}
	return item
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
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (s *AdminAuditService) encodeAdminAuditCursor(
	createdAt time.Time,
	id uint,
	filterHash string,
	startTime *time.Time,
	endTime *time.Time,
) string {
	cursor := adminAuditCursor{
		Version:    2,
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
	payload, _ := json.Marshal(cursor)
	mac := hmac.New(sha256.New, s.cursorSigningKey)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *AdminAuditService) decodeAdminAuditCursor(
	raw string,
	filterHash string,
) (time.Time, uint, *time.Time, *time.Time, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return time.Time{}, 0, nil, nil, ErrInvalidAdminAuditCursor
	}
	encoding := base64.RawURLEncoding.Strict()
	payload, err := encoding.DecodeString(parts[0])
	if err != nil || len(payload) > 1024 {
		return time.Time{}, 0, nil, nil, ErrInvalidAdminAuditCursor
	}
	providedMAC, err := encoding.DecodeString(parts[1])
	if err != nil || len(providedMAC) != sha256.Size {
		return time.Time{}, 0, nil, nil, ErrInvalidAdminAuditCursor
	}
	expectedMAC := hmac.New(sha256.New, s.cursorSigningKey)
	_, _ = expectedMAC.Write(payload)
	if !hmac.Equal(providedMAC, expectedMAC.Sum(nil)) {
		return time.Time{}, 0, nil, nil, ErrInvalidAdminAuditCursor
	}
	var cursor adminAuditCursor
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil ||
		cursor.Version != 2 ||
		cursor.ID == 0 ||
		cursor.FilterHash != filterHash {
		return time.Time{}, 0, nil, nil, ErrInvalidAdminAuditCursor
	}
	if err := ensureAdminAuditCursorEOF(decoder); err != nil {
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

func ensureAdminAuditCursorEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalidAdminAuditCursor
	}
	return nil
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
	`(?i)["']?(authorization|set[_-]?cookie|cookie|password|passwd|access[_-]?token|refresh[_-]?token|token|secret|credential|api[_-]?key)["']?(\s*[:=]\s*)["']?([^\s"',;}\]]+)`,
)
var auditCookiePattern = regexp.MustCompile(
	`(?i)\b(set-cookie|cookie)(\s*:\s*)[^\r\n]+`,
)

const (
	auditCredentialRedactionPlaceholder = "\ue000"
	auditQueryRedactionPlaceholder      = "\ue001"
	auditValueRedactionPlaceholder      = "\ue002"
	auditURLRedactionPlaceholder        = "\ue003"
	auditContentRedactionPlaceholder    = "\ue004"
)

var protectAuditRedactionMarkers = strings.NewReplacer(
	"[凭据已隐藏]", auditCredentialRedactionPlaceholder,
	"[查询参数已隐藏]", auditQueryRedactionPlaceholder,
	"[已隐藏]", auditValueRedactionPlaceholder,
	"[URL 已隐藏]", auditURLRedactionPlaceholder,
	"[内容已隐藏]", auditContentRedactionPlaceholder,
)

var restoreAuditRedactionMarkers = strings.NewReplacer(
	auditCredentialRedactionPlaceholder, "[凭据已隐藏]",
	auditQueryRedactionPlaceholder, "[查询参数已隐藏]",
	auditValueRedactionPlaceholder, "[已隐藏]",
	auditURLRedactionPlaceholder, "[URL 已隐藏]",
	auditContentRedactionPlaceholder, "[内容已隐藏]",
)

func redactAuditText(value string, maxLength int) string {
	value = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			return r
		}
		return -1
	}, value)
	if structured, ok := redactStructuredAuditText(value); ok {
		value = structured
	}
	value = protectAuditRedactionMarkers.Replace(value)
	value = ScrubOutboxFailureText(value)
	value = protectAuditRedactionMarkers.Replace(value)
	value = auditCookiePattern.ReplaceAllString(
		value,
		"$1$2"+auditCredentialRedactionPlaceholder,
	)
	value = auditSecretPattern.ReplaceAllString(
		value,
		"$1$2"+auditValueRedactionPlaceholder,
	)
	value = restoreAuditRedactionMarkers.Replace(value)
	return truncateAuditText(value, maxLength)
}

func redactStructuredAuditText(value string) (string, bool) {
	if len(value) == 0 || len(value) > 64*1024 {
		return "", false
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return "", false
	}
	if err := ensureAdminAuditCursorEOF(decoder); err != nil {
		return "", false
	}
	switch decoded.(type) {
	case map[string]any, []any:
	default:
		return "", false
	}
	redacted := redactAuditStructuredValue(decoded, 0)
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

func redactAuditStructuredValue(value any, depth int) any {
	if depth >= 12 {
		return "[内容已隐藏]"
	}
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			if sensitiveAuditField(key) {
				result[key] = "[已隐藏]"
				continue
			}
			result[key] = redactAuditStructuredValue(item, depth+1)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = redactAuditStructuredValue(item, depth+1)
		}
		return result
	case string:
		return redactAuditText(typed, adminAuditStructuredValueMaxRunes)
	case json.Number, bool, nil:
		return typed
	default:
		return "[内容已隐藏]"
	}
}

func sensitiveAuditField(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.NewReplacer("-", "_", " ", "_").Replace(normalized)
	for _, fragment := range []string{
		"authorization",
		"cookie",
		"password",
		"passwd",
		"token",
		"secret",
		"credential",
		"api_key",
		"access_key",
		"private_key",
	} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func redactAuditQuery(raw string) string {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "[内容已隐藏]"
	}
	for key := range values {
		if sensitiveAuditField(key) {
			values[key] = []string{"[已隐藏]"}
		} else {
			for index, value := range values[key] {
				values[key][index] = redactAuditText(value, 512)
			}
		}
	}
	return truncateAuditText(values.Encode(), adminAuditQueryMaxRunes)
}

func redactAuditIdentifier(value string, maxLength int) string {
	return redactAuditText(strings.TrimSpace(value), maxLength)
}

func truncateAuditText(value string, maxLength int) string {
	if maxLength <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxLength {
		return value
	}
	if maxLength == 1 {
		return "…"
	}
	return string(runes[:maxLength-1]) + "…"
}
