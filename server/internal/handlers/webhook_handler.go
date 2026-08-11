package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/seaworld008/chronodesk/server/internal/httpcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"github.com/seaworld008/chronodesk/server/internal/security"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// WebhookHandler Webhook处理器
type WebhookHandler struct {
	db                  *gorm.DB
	notificationService *services.NotificationService
	secretStore         security.Protector
	queryService        *services.WebhookQueryService
}

// NewWebhookHandlerWithProtector injects the application data-encryption
// keyring used by both configuration writes and webhook deliveries.
func NewWebhookHandlerWithProtector(
	db *gorm.DB,
	protector security.Protector,
	provided ...*services.NotificationService,
) *WebhookHandler {
	notificationService := services.NewNotificationServiceWithProtector(
		db,
		protector,
	)
	if len(provided) > 0 && provided[0] != nil {
		notificationService = provided[0]
	}
	return &WebhookHandler{
		db:                  db,
		notificationService: notificationService,
		secretStore:         protector,
		queryService:        services.NewWebhookQueryService(db),
	}
}

// ConfigureListCursor installs the deployment-owned root used only to derive
// the Webhook delivery timeline cursor key. An empty key fails closed.
func (h *WebhookHandler) ConfigureListCursor(root []byte) error {
	if h == nil || h.queryService == nil {
		return services.ErrWebhookListCursorKey
	}
	return h.queryService.ConfigureListCursor(root)
}

// CreateWebhookRequest 创建webhook请求结构
type CreateWebhookRequest struct {
	Name            string                     `json:"name" binding:"required,max=100"`
	Description     string                     `json:"description" binding:"max=500"`
	Provider        models.WebhookProvider     `json:"provider" binding:"required"`
	WebhookURL      string                     `json:"webhook_url" binding:"required,url"`
	Secret          string                     `json:"secret"`
	AccessToken     string                     `json:"access_token"`
	EnabledEvents   []models.WebhookEventType  `json:"enabled_events"`
	MessageTemplate string                     `json:"message_template"`
	MessageFormat   string                     `json:"message_format"`
	FilterRules     *models.WebhookFilterRules `json:"filter_rules"`
	RetryCount      int                        `json:"retry_count"`
	RetryInterval   int                        `json:"retry_interval"`
	TimeoutSeconds  int                        `json:"timeout_seconds"`
	IsAsync         bool                       `json:"is_async"`
	RateLimit       int                        `json:"rate_limit"`
	RateLimitWindow int                        `json:"rate_limit_window"`
}

// UpdateWebhookRequest 更新webhook请求结构
type UpdateWebhookRequest struct {
	Name                 *string                    `json:"name" binding:"omitempty,max=100"`
	Description          *string                    `json:"description" binding:"omitempty,max=500"`
	Provider             *models.WebhookProvider    `json:"provider"`
	WebhookURL           *string                    `json:"webhook_url" binding:"omitempty,url"`
	Secret               *string                    `json:"secret"`
	SecretOverlapSeconds *int                       `json:"secret_overlap_seconds"`
	AccessToken          *string                    `json:"access_token"`
	EnabledEvents        *[]models.WebhookEventType `json:"enabled_events"`
	MessageTemplate      *string                    `json:"message_template"`
	MessageFormat        *string                    `json:"message_format"`
	FilterRules          *models.WebhookFilterRules `json:"filter_rules"`
	RetryCount           *int                       `json:"retry_count"`
	RetryInterval        *int                       `json:"retry_interval"`
	TimeoutSeconds       *int                       `json:"timeout_seconds"`
	IsAsync              *bool                      `json:"is_async"`
	RateLimit            *int                       `json:"rate_limit"`
	RateLimitWindow      *int                       `json:"rate_limit_window"`
	Status               *models.WebhookStatus      `json:"status" binding:"omitempty,oneof=active inactive disabled error"`
}

// WebhookConfigResponse is the closed Human Web projection of a persisted
// Webhook configuration. Persistence relations and secret material must never
// cross the HTTP boundary.
type WebhookConfigResponse struct {
	ID                      uint                       `json:"id"`
	CreatedAt               time.Time                  `json:"created_at"`
	UpdatedAt               time.Time                  `json:"updated_at"`
	OrganizationID          uint                       `json:"organization_id"`
	ProjectID               uint                       `json:"project_id"`
	Name                    string                     `json:"name"`
	Description             string                     `json:"description"`
	Provider                models.WebhookProvider     `json:"provider"`
	WebhookURLMasked        string                     `json:"webhook_url_masked"`
	HasWebhookURL           bool                       `json:"has_webhook_url"`
	Status                  models.WebhookStatus       `json:"status"`
	PreviousSecretExpiresAt *time.Time                 `json:"previous_secret_expires_at,omitempty"`
	EnabledEvents           string                     `json:"enabled_events"`
	EnabledEventsList       []models.WebhookEventType  `json:"enabled_events_list,omitempty"`
	MessageTemplate         string                     `json:"message_template"`
	MessageFormat           string                     `json:"message_format"`
	FilterRules             string                     `json:"filter_rules"`
	FilterRulesObject       *models.WebhookFilterRules `json:"filter_rules_obj,omitempty"`
	RetryCount              int                        `json:"retry_count"`
	RetryInterval           int                        `json:"retry_interval"`
	TimeoutSeconds          int                        `json:"timeout_seconds"`
	IsAsync                 bool                       `json:"is_async"`
	RateLimit               int                        `json:"rate_limit"`
	RateLimitWindow         int                        `json:"rate_limit_window"`
	LastTriggeredAt         *time.Time                 `json:"last_triggered_at,omitempty"`
	LastSuccessAt           *time.Time                 `json:"last_success_at,omitempty"`
	LastErrorAt             *time.Time                 `json:"last_error_at,omitempty"`
	LastError               string                     `json:"last_error"`
	TotalSent               int64                      `json:"total_sent"`
	TotalSuccess            int64                      `json:"total_success"`
	TotalFailed             int64                      `json:"total_failed"`
	CreatedBy               uint                       `json:"created_by"`
	UpdatedBy               *uint                      `json:"updated_by,omitempty"`
	ResourceVersion         uint64                     `json:"resource_version"`
}

// ListWebhooksResponse 列表响应结构
type ListWebhooksResponse struct {
	Items      []WebhookConfigResponse `json:"items"`
	Total      int64                   `json:"total"`
	Page       int                     `json:"page"`
	PageSize   int                     `json:"page_size"`
	TotalPages int                     `json:"total_pages"`
}

// WebhookLogResponse exposes only the non-sensitive delivery diagnostics
// published by the Human OpenAPI contract.
type WebhookLogResponse struct {
	ID             uint                    `json:"id"`
	CreatedAt      time.Time               `json:"created_at"`
	ConfigID       uint                    `json:"config_id"`
	EventType      models.WebhookEventType `json:"event_type"`
	Status         string                  `json:"status"`
	ResponseStatus int                     `json:"response_status,omitempty"`
	ResponseTime   int64                   `json:"response_time,omitempty"`
	ErrorMessage   string                  `json:"error_message,omitempty"`
}

type ListWebhookLogsResponse struct {
	Items      []WebhookLogResponse `json:"items"`
	NextCursor string               `json:"next_cursor"`
	HasMore    bool                 `json:"has_more"`
}

type WebhookStatsSummaryResponse struct {
	TotalSent    int64 `json:"total_sent"`
	TotalSuccess int64 `json:"total_success"`
	TotalFailed  int64 `json:"total_failed"`
}

// WebhookDateOnly normalizes SQL DATE values without relying on PostgreSQL's
// session DateStyle. Its string representation is always ISO YYYY-MM-DD.
type WebhookDateOnly string

var _ sql.Scanner = (*WebhookDateOnly)(nil)

func (date *WebhookDateOnly) Scan(value any) error {
	if date == nil {
		return errors.New("scan Webhook date into nil receiver")
	}
	switch typed := value.(type) {
	case time.Time:
		*date = WebhookDateOnly(typed.Format(time.DateOnly))
		return nil
	case string:
		return date.scanString(typed)
	case []byte:
		return date.scanString(string(typed))
	default:
		return fmt.Errorf("scan Webhook date from unsupported %T", value)
	}
}

func (date *WebhookDateOnly) scanString(value string) error {
	if len(value) != len(time.DateOnly) {
		return fmt.Errorf("scan Webhook date %q: expected YYYY-MM-DD", value)
	}
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil || parsed.Format(time.DateOnly) != value {
		if err == nil {
			err = errors.New("date is not in canonical YYYY-MM-DD form")
		}
		return fmt.Errorf("scan Webhook date %q: %w", value, err)
	}
	*date = WebhookDateOnly(value)
	return nil
}

type WebhookDailyStatsResponse struct {
	Date    WebhookDateOnly `json:"date"`
	Sent    int64           `json:"sent"`
	Success int64           `json:"success"`
	Failed  int64           `json:"failed"`
}

type WebhookStatsResponse struct {
	Summary    WebhookStatsSummaryResponse `json:"summary"`
	DailyStats []WebhookDailyStatsResponse `json:"daily_stats"`
	Period     string                      `json:"period"`
}

func newWebhookConfigResponse(
	webhook models.WebhookConfig,
	resourceVersions ...uint64,
) WebhookConfigResponse {
	resourceVersion := uint64(1)
	if len(resourceVersions) > 0 && resourceVersions[0] > 0 {
		resourceVersion = resourceVersions[0]
	}
	var filterRules *models.WebhookFilterRules
	if webhook.FilterRulesObj != nil {
		copied := *webhook.FilterRulesObj
		copied.TransitionStatuses = append(
			[]models.TicketStatus(nil),
			webhook.FilterRulesObj.TransitionStatuses...,
		)
		filterRules = &copied
	}
	return WebhookConfigResponse{
		ID:                      webhook.ID,
		CreatedAt:               webhook.CreatedAt,
		UpdatedAt:               webhook.UpdatedAt,
		OrganizationID:          webhook.OrganizationID,
		ProjectID:               webhook.ProjectID,
		Name:                    webhook.Name,
		Description:             webhook.Description,
		Provider:                webhook.Provider,
		WebhookURLMasked:        maskWebhookURL(webhook.WebhookURL),
		HasWebhookURL:           strings.TrimSpace(webhook.WebhookURL) != "",
		Status:                  webhook.Status,
		PreviousSecretExpiresAt: webhook.PreviousSecretExpiresAt,
		EnabledEvents:           webhook.EnabledEvents,
		EnabledEventsList: append(
			[]models.WebhookEventType(nil),
			webhook.EnabledEventsObj...,
		),
		MessageTemplate:   webhook.MessageTemplate,
		MessageFormat:     webhook.MessageFormat,
		FilterRules:       webhook.FilterRules,
		FilterRulesObject: filterRules,
		RetryCount:        webhook.RetryCount,
		RetryInterval:     webhook.RetryInterval,
		TimeoutSeconds:    webhook.TimeoutSeconds,
		IsAsync:           webhook.IsAsync,
		RateLimit:         webhook.RateLimit,
		RateLimitWindow:   webhook.RateLimitWindow,
		LastTriggeredAt:   webhook.LastTriggeredAt,
		LastSuccessAt:     webhook.LastSuccessAt,
		LastErrorAt:       webhook.LastErrorAt,
		LastError:         scrubWebhookDiagnostic(webhook.LastError),
		TotalSent:         webhook.TotalSent,
		TotalSuccess:      webhook.TotalSuccess,
		TotalFailed:       webhook.TotalFailed,
		CreatedBy:         webhook.CreatedBy,
		UpdatedBy:         webhook.UpdatedBy,
		ResourceVersion:   resourceVersion,
	}
}

func (h *WebhookHandler) webhookResourceVersions(
	ctx context.Context,
	scope models.ProjectScope,
	webhooks []models.WebhookConfig,
) (map[uint]uint64, error) {
	versions := make(map[uint]uint64, len(webhooks))
	subjectToID := make(map[string]uint, len(webhooks))
	subjects := make([]string, 0, len(webhooks))
	keyToID := make(map[string]uint, len(webhooks))
	keys := make([]string, 0, len(webhooks))
	for index := range webhooks {
		if webhooks[index].ID == 0 {
			continue
		}
		versions[webhooks[index].ID] = 1
		subject := services.WebhookAdminSubject(webhooks[index].ID)
		subjectToID[subject] = webhooks[index].ID
		subjects = append(subjects, subject)
		key := services.AdminResourceVersionKey(scope, subject)
		keyToID[key] = webhooks[index].ID
		keys = append(keys, key)
	}
	if len(subjects) == 0 {
		return versions, nil
	}
	var anchors []struct {
		Key     string
		Version int
	}
	if err := h.db.WithContext(ctx).
		Model(&models.SystemConfig{}).
		Select("key, version").
		Where("key IN ?", keys).
		Scan(&anchors).Error; err != nil {
		return nil, fmt.Errorf(
			"load Webhook administrator version anchors: %w",
			err,
		)
	}
	for index := range anchors {
		configID := keyToID[anchors[index].Key]
		if configID != 0 && anchors[index].Version > 0 {
			versions[configID] = uint64(anchors[index].Version)
		}
	}
	var rows []struct {
		Subject string
		Version uint64
	}
	if err := h.db.WithContext(ctx).
		Model(&models.DomainEvent{}).
		Select("subject, MAX(resource_version) AS version").
		Where(
			"organization_id = ? AND project_id = ? AND subject IN ? AND type LIKE ?",
			scope.OrganizationID,
			scope.ProjectID,
			subjects,
			"io.chronodesk.admin.webhook.%",
		).
		Group("subject").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf(
			"load Webhook administrator resource versions: %w",
			err,
		)
	}
	for index := range rows {
		configID := subjectToID[rows[index].Subject]
		if configID != 0 &&
			versions[configID] == 1 &&
			rows[index].Version > 0 {
			versions[configID] = rows[index].Version
		}
	}
	return versions, nil
}

func maskWebhookURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") ||
		strings.TrimSpace(parsed.Host) == "" {
		return ""
	}
	return "https://" + parsed.Host + "/…"
}

func newWebhookLogResponse(log models.WebhookLog) WebhookLogResponse {
	return WebhookLogResponse{
		ID:             log.ID,
		CreatedAt:      log.CreatedAt,
		ConfigID:       log.ConfigID,
		EventType:      log.EventType,
		Status:         log.Status,
		ResponseStatus: log.ResponseStatus,
		ResponseTime:   log.ResponseTime,
		ErrorMessage:   scrubWebhookDiagnostic(log.ErrorMessage),
	}
}

func scrubWebhookDiagnostic(value string) string {
	value = services.ScrubOutboxFailureText(value)
	runes := []rune(value)
	if len(runes) > 500 {
		return string(runes[:500])
	}
	return value
}

// TestWebhookResult is a queued operation receipt. External delivery and its
// WebhookLog are produced later by the Outbox worker.
type TestWebhookResult = services.WebhookTestReceipt

func decodeStrictWebhookJSON(c *gin.Context, target any) error {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return errors.New("request body is required")
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain exactly one JSON value")
		}
		return err
	}
	return binding.Validator.ValidateStruct(target)
}

// CreateWebhook 创建webhook配置
// @Summary 创建webhook配置
// @Description 创建新的webhook通知配置
// @Tags webhook
// @Accept json
// @Produce json
// @Param projectKey path string true "项目 Key"
// @Param webhook body CreateWebhookRequest true "Webhook配置"
// @Success 200 {object} WebhookConfigResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/projects/{projectKey}/webhooks [post]
// @Security BearerAuth
func (h *WebhookHandler) CreateWebhook(c *gin.Context) {
	operation, ok := requireWebhookManagerAccess(c)
	if !ok {
		return
	}
	var req CreateWebhookRequest
	if err := decodeStrictWebhookJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "参数验证失败",
			"data": nil,
		})
		return
	}

	// 获取当前用户ID
	userID := c.GetUint("user_id")

	// 创建webhook配置
	webhook := models.WebhookConfig{
		OrganizationID:  operation.Scope.OrganizationID,
		ProjectID:       operation.Scope.ProjectID,
		Name:            req.Name,
		Description:     req.Description,
		Provider:        req.Provider,
		WebhookURL:      req.WebhookURL,
		MessageTemplate: req.MessageTemplate,
		MessageFormat:   req.MessageFormat,
		RetryCount:      req.RetryCount,
		RetryInterval:   req.RetryInterval,
		TimeoutSeconds:  req.TimeoutSeconds,
		IsAsync:         req.IsAsync,
		RateLimit:       req.RateLimit,
		RateLimitWindow: req.RateLimitWindow,
		// A newly saved endpoint has not yet been proven reachable or reviewed
		// by an operator. Keep it inert until an explicit update activates it.
		Status:    models.WebhookStatusInactive,
		CreatedBy: userID,
	}
	if err := webhook.SetSubscriptions(req.EnabledEvents, req.FilterRules, true); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "Webhook 订阅事件或状态筛选无效",
			"data": nil,
		})
		return
	}

	// 设置默认值
	if webhook.RetryCount == 0 {
		webhook.RetryCount = 3
	}
	if webhook.RetryInterval == 0 {
		webhook.RetryInterval = 60
	}
	if webhook.TimeoutSeconds == 0 {
		webhook.TimeoutSeconds = 30
	}
	if webhook.RateLimit == 0 {
		webhook.RateLimit = 60
	}
	if webhook.RateLimitWindow == 0 {
		webhook.RateLimitWindow = 60
	}
	if webhook.MessageFormat == "" {
		webhook.MessageFormat = "markdown"
	}
	if err := security.ValidateHTTPSCallbackURLString(webhook.WebhookURL); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "Webhook 地址必须是公网 HTTPS 地址，且不能包含用户凭据",
			"data": nil,
		})
		return
	}

	if err := scopeddb.TransactionForContext(
		c.Request.Context(),
		h.db,
		func(tx *gorm.DB) error {
			if err := tx.Create(&webhook).Error; err != nil {
				return err
			}
			secret, err := h.protectWebhookSecret(webhook.ID, "secret", req.Secret)
			if err != nil {
				return err
			}
			accessToken, err := h.protectWebhookSecret(webhook.ID, "access_token", req.AccessToken)
			if err != nil {
				return err
			}
			if secret == "" && accessToken == "" {
				return nil
			}
			webhook.Secret = secret
			webhook.AccessToken = accessToken
			return tx.Model(&webhook).Updates(map[string]any{
				"secret":       secret,
				"access_token": accessToken,
			}).Error
		},
	); err != nil {
		logHandlerFailure(c, "webhook.create", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "创建webhook失败，请检查加密配置或稍后重试",
			"data": nil,
		})
		return
	}

	if err := h.db.WithContext(c.Request.Context()).First(&webhook, webhook.ID).Error; err != nil {
		logHandlerFailure(c, "webhook.reload_after_create", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "创建成功",
		"data": newWebhookConfigResponse(webhook),
	})
}

// ListWebhooks 获取webhook列表
// @Summary 获取webhook列表
// @Description 分页获取webhook配置列表
// @Tags webhook
// @Accept json
// @Produce json
// @Param projectKey path string true "项目 Key"
// @Param page query int false "页码" default(1)
// @Param page_size query int false "每页数量" default(25)
// @Param provider query string false "提供商过滤"
// @Param status query string false "状态过滤"
// @Success 200 {object} ListWebhooksResponse
// @Failure 500 {object} map[string]interface{}
// @Router /api/projects/{projectKey}/webhooks [get]
// @Security BearerAuth
func (h *WebhookHandler) ListWebhooks(c *gin.Context) {
	operation, ok := requireWebhookManagerAccess(c)
	if !ok {
		return
	}
	query, ok := requireWebhookDefinitionQuery(c)
	if !ok {
		return
	}
	page, err := h.queryService.ListDefinitions(c.Request.Context(), query)
	if err != nil {
		if errors.Is(err, services.ErrInvalidWebhookListQuery) {
			writeInvalidWebhookListQuery(c)
			return
		}
		logHandlerFailure(c, "webhook.list", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "获取webhook列表失败",
			"data": nil,
		})
		return
	}

	items := make([]WebhookConfigResponse, 0, len(page.Items))
	versions, err := h.webhookResourceVersions(
		c.Request.Context(),
		operation.Scope,
		page.Items,
	)
	if err != nil {
		logHandlerFailure(c, "webhook.list_resource_versions", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "获取webhook列表失败",
			"data": nil,
		})
		return
	}
	for _, webhook := range page.Items {
		items = append(
			items,
			newWebhookConfigResponse(
				webhook,
				versions[webhook.ID],
			),
		)
	}
	response := ListWebhooksResponse{
		Items:      items,
		Total:      page.Total,
		Page:       page.Page,
		PageSize:   page.PageSize,
		TotalPages: page.TotalPages,
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "获取成功",
		"data": response,
	})
}

// GetWebhook 获取webhook详情
// @Summary 获取webhook详情
// @Description 根据ID获取webhook配置详情
// @Tags webhook
// @Accept json
// @Produce json
// @Param projectKey path string true "项目 Key"
// @Param id path int true "Webhook ID"
// @Success 200 {object} WebhookConfigResponse
// @Failure 404 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/projects/{projectKey}/webhooks/{id} [get]
// @Security BearerAuth
func (h *WebhookHandler) GetWebhook(c *gin.Context) {
	operation, ok := requireWebhookManagerAccess(c)
	if !ok {
		return
	}
	id, validID := strictWebhookPositiveUint32(c.Param("id"))
	if !validID {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "无效的ID",
			"data": nil,
		})
		return
	}

	var webhook models.WebhookConfig
	if err := h.db.WithContext(c.Request.Context()).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			uint(id),
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
		).
		First(&webhook).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 1,
				"msg":  "webhook不存在",
				"data": nil,
			})
		} else {
			logHandlerFailure(c, "webhook.get", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 1,
				"msg":  "获取webhook失败",
				"data": nil,
			})
		}
		return
	}

	versions, err := h.webhookResourceVersions(
		c.Request.Context(),
		operation.Scope,
		[]models.WebhookConfig{webhook},
	)
	if err != nil {
		logHandlerFailure(c, "webhook.get_resource_version", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "获取webhook失败",
			"data": nil,
		})
		return
	}
	resourceVersion := versions[webhook.ID]
	c.Header("ETag", httpcontract.FormatETag(resourceVersion))
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "获取成功",
		"data": newWebhookConfigResponse(
			webhook,
			resourceVersion,
		),
	})
}

// UpdateWebhook 更新webhook配置
// @Summary 更新webhook配置
// @Description 更新webhook配置信息
// @Tags webhook
// @Accept json
// @Produce json
// @Param projectKey path string true "项目 Key"
// @Param id path int true "Webhook ID"
// @Param webhook body UpdateWebhookRequest true "更新数据"
// @Success 200 {object} WebhookConfigResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/projects/{projectKey}/webhooks/{id} [put]
// @Security BearerAuth
func (h *WebhookHandler) UpdateWebhook(c *gin.Context) {
	operation, ok := requireWebhookManagerAccess(c)
	if !ok {
		return
	}
	expectedVersion, ok := requireWebhookIfMatch(c)
	if !ok {
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "无效的ID",
			"data": nil,
		})
		return
	}

	var req UpdateWebhookRequest
	if err := decodeStrictWebhookJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "参数验证失败",
			"data": nil,
		})
		return
	}

	// 获取当前用户ID
	userID := c.GetUint("user_id")
	var webhook models.WebhookConfig
	var resourceVersion uint64
	if err := scopeddb.TransactionForContext(
		c.Request.Context(),
		h.db,
		func(tx *gorm.DB) error {
			nextVersion, err :=
				services.CompareAndSwapWebhookAdminResourceVersionTx(
					c.Request.Context(),
					tx,
					operation.Scope,
					uint(id),
					expectedVersion,
					userID,
				)
			if err != nil {
				return err
			}
			if err := tx.WithContext(c.Request.Context()).
				Clauses(clause.Locking{Strength: "UPDATE"}).
				Where(
					"id = ? AND organization_id = ? AND project_id = ?",
					uint(id),
					operation.Scope.OrganizationID,
					operation.Scope.ProjectID,
				).
				Take(&webhook).Error; err != nil {
				return err
			}
			if err := services.EnsureWebhookConfigMutableTx(
				c.Request.Context(),
				tx,
				operation.Scope,
				webhook.ID,
			); err != nil {
				return err
			}
			updates, err := h.webhookUpdateFields(
				&webhook,
				req,
				userID,
			)
			if err != nil {
				return err
			}
			result := tx.WithContext(c.Request.Context()).
				Model(&models.WebhookConfig{}).
				Where(
					"id = ? AND organization_id = ? AND project_id = ?",
					webhook.ID,
					operation.Scope.OrganizationID,
					operation.Scope.ProjectID,
				).
				Updates(updates)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return gorm.ErrRecordNotFound
			}
			if err := tx.WithContext(c.Request.Context()).
				Where(
					"id = ? AND organization_id = ? AND project_id = ?",
					webhook.ID,
					operation.Scope.OrganizationID,
					operation.Scope.ProjectID,
				).
				Take(&webhook).Error; err != nil {
				return err
			}
			resourceVersion = nextVersion
			return nil
		},
	); err != nil {
		var versionConflict *services.WebhookAdminVersionConflictError
		if errors.As(err, &versionConflict) {
			writeWebhookVersionConflict(c, versionConflict.Current)
			return
		}
		var inputError *webhookUpdateInputError
		if errors.As(err, &inputError) {
			writeHumanTicketProblem(
				c,
				http.StatusBadRequest,
				"invalid_request",
				"Webhook 配置无效",
				inputError.Error(),
				false,
			)
			return
		}
		if errors.Is(
			err,
			services.ErrWebhookEmergencyRevokedTerminal,
		) {
			writeHumanTicketProblem(
				c,
				http.StatusConflict,
				"webhook_emergency_revoked",
				"Webhook 已紧急撤销",
				"紧急撤销后的 Webhook 配置不可再修改或重新启用",
				false,
			)
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 1,
				"msg":  "webhook不存在",
				"data": nil,
			})
			return
		}
		logHandlerFailure(c, "webhook.update", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "更新webhook失败",
			"data": nil,
		})
		return
	}

	c.Header("ETag", httpcontract.FormatETag(resourceVersion))
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "更新成功",
		"data": newWebhookConfigResponse(
			webhook,
			resourceVersion,
		),
	})
}

type webhookUpdateInputError struct {
	message string
}

func (err *webhookUpdateInputError) Error() string {
	return err.message
}

func (h *WebhookHandler) webhookUpdateFields(
	webhook *models.WebhookConfig,
	req UpdateWebhookRequest,
	userID uint,
) (map[string]any, error) {
	if webhook == nil {
		return nil, gorm.ErrRecordNotFound
	}
	updates := map[string]any{"updated_by": userID}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.Provider != nil {
		updates["provider"] = *req.Provider
	}
	if req.WebhookURL != nil {
		if err := security.ValidateHTTPSCallbackURLString(
			*req.WebhookURL,
		); err != nil {
			return nil, &webhookUpdateInputError{
				message: "Webhook 地址必须是公网 HTTPS 地址，且不能包含用户凭据",
			}
		}
		updates["webhook_url"] = *req.WebhookURL
	}
	if req.Secret != nil {
		if strings.TrimSpace(*req.Secret) == "" {
			return nil, &webhookUpdateInputError{
				message: "Webhook 签名密钥不能为空",
			}
		}
		overlapSeconds := 24 * 60 * 60
		if req.SecretOverlapSeconds != nil {
			overlapSeconds = *req.SecretOverlapSeconds
		}
		if overlapSeconds < 0 || overlapSeconds > 7*24*60*60 {
			return nil, &webhookUpdateInputError{
				message: "Webhook 密钥重叠期必须在 0 到 7 天之间",
			}
		}
		previousSecret := ""
		var previousExpiresAt *time.Time
		if overlapSeconds > 0 && webhook.Secret != "" {
			plaintext, err := security.RevealOptional(
				h.secretStore,
				webhook.Secret,
				security.FieldAAD(
					"webhook_configs",
					strconv.FormatUint(uint64(webhook.ID), 10),
					"secret",
				),
			)
			if err != nil {
				return nil, fmt.Errorf(
					"reveal previous Webhook secret: %w",
					err,
				)
			}
			previousSecret, err = h.protectWebhookSecret(
				webhook.ID,
				"previous_secret",
				plaintext,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"protect previous Webhook secret: %w",
					err,
				)
			}
			expiresAt := time.Now().UTC().Add(
				time.Duration(overlapSeconds) * time.Second,
			)
			previousExpiresAt = &expiresAt
		}
		secret, err := h.protectWebhookSecret(
			webhook.ID,
			"secret",
			*req.Secret,
		)
		if err != nil {
			return nil, fmt.Errorf("protect Webhook secret: %w", err)
		}
		updates["secret"] = secret
		updates["previous_secret"] = previousSecret
		updates["previous_secret_expires_at"] = previousExpiresAt
	}
	if req.AccessToken != nil {
		accessToken, err := h.protectWebhookSecret(
			webhook.ID,
			"access_token",
			*req.AccessToken,
		)
		if err != nil {
			return nil, fmt.Errorf("protect Webhook access token: %w", err)
		}
		updates["access_token"] = accessToken
	}
	if req.MessageTemplate != nil {
		updates["message_template"] = *req.MessageTemplate
	}
	if req.MessageFormat != nil {
		updates["message_format"] = *req.MessageFormat
	}
	if req.EnabledEvents != nil || req.FilterRules != nil {
		events := webhook.EnabledEventsObj
		filters := webhook.FilterRulesObj
		if req.EnabledEvents != nil {
			events = *req.EnabledEvents
		}
		if req.FilterRules != nil {
			filters = req.FilterRules
		}
		if err := webhook.SetSubscriptions(events, filters, true); err != nil {
			return nil, &webhookUpdateInputError{
				message: "Webhook 订阅事件或状态筛选无效",
			}
		}
		updates["enabled_events"] = webhook.EnabledEvents
		updates["filter_rules"] = webhook.FilterRules
	}
	if req.RetryCount != nil {
		updates["retry_count"] = *req.RetryCount
	}
	if req.RetryInterval != nil {
		updates["retry_interval"] = *req.RetryInterval
	}
	if req.TimeoutSeconds != nil {
		updates["timeout_seconds"] = *req.TimeoutSeconds
	}
	if req.IsAsync != nil {
		updates["is_async"] = *req.IsAsync
	}
	if req.RateLimit != nil {
		updates["rate_limit"] = *req.RateLimit
	}
	if req.RateLimitWindow != nil {
		updates["rate_limit_window"] = *req.RateLimitWindow
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}
	return updates, nil
}

func (h *WebhookHandler) protectWebhookSecret(
	configID uint,
	field string,
	plaintext string,
) (string, error) {
	return security.ProtectOptional(
		h.secretStore,
		plaintext,
		security.FieldAAD(
			"webhook_configs",
			strconv.FormatUint(uint64(configID), 10),
			field,
		),
	)
}

// DeleteWebhook 删除webhook配置
// @Summary 删除webhook配置
// @Description 软删除webhook配置
// @Tags webhook
// @Accept json
// @Produce json
// @Param projectKey path string true "项目 Key"
// @Param id path int true "Webhook ID"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/projects/{projectKey}/webhooks/{id} [delete]
// @Security BearerAuth
func (h *WebhookHandler) DeleteWebhook(c *gin.Context) {
	operation, ok := requireWebhookManagerAccess(c)
	if !ok {
		return
	}
	expectedVersion, ok := requireWebhookIfMatch(c)
	if !ok {
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "无效的ID",
			"data": nil,
		})
		return
	}

	// Soft deletion advances the same durable preflight version in the same
	// transaction, while immutable delivery snapshots remain routable.
	var resourceVersion uint64
	if err := scopeddb.TransactionForContext(
		c.Request.Context(),
		h.db,
		func(tx *gorm.DB) error {
			nextVersion, err :=
				services.CompareAndSwapWebhookAdminResourceVersionTx(
					c.Request.Context(),
					tx,
					operation.Scope,
					uint(id),
					expectedVersion,
					c.GetUint("user_id"),
				)
			if err != nil {
				return err
			}
			var webhook models.WebhookConfig
			if err := tx.WithContext(c.Request.Context()).
				Clauses(clause.Locking{Strength: "UPDATE"}).
				Where(
					"id = ? AND organization_id = ? AND project_id = ?",
					uint(id),
					operation.Scope.OrganizationID,
					operation.Scope.ProjectID,
				).
				Take(&webhook).Error; err != nil {
				return err
			}
			result := tx.WithContext(c.Request.Context()).
				Where(
					"id = ? AND organization_id = ? AND project_id = ?",
					webhook.ID,
					operation.Scope.OrganizationID,
					operation.Scope.ProjectID,
				).
				Delete(&models.WebhookConfig{})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return gorm.ErrRecordNotFound
			}
			resourceVersion = nextVersion
			return nil
		},
	); err != nil {
		var versionConflict *services.WebhookAdminVersionConflictError
		if errors.As(err, &versionConflict) {
			writeWebhookVersionConflict(c, versionConflict.Current)
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 1,
				"msg":  "webhook不存在",
				"data": nil,
			})
			return
		}
		logHandlerFailure(c, "webhook.delete", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "删除webhook失败",
			"data": nil,
		})
		return
	}

	c.Header("ETag", httpcontract.FormatETag(resourceVersion))
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "删除成功",
		"data": nil,
	})
}

func requireWebhookIfMatch(c *gin.Context) (uint64, bool) {
	expectedVersion, err := httpcontract.ParseIfMatch(
		c.GetHeader("If-Match"),
	)
	switch {
	case errors.Is(err, httpcontract.ErrIfMatchRequired):
		writeHumanTicketProblem(
			c,
			http.StatusPreconditionRequired,
			"precondition_required",
			"缺少请求前置条件",
			"必须提供当前 Webhook 配置版本对应的 If-Match 请求头",
			false,
		)
		return 0, false
	case err != nil:
		writeHumanTicketProblem(
			c,
			http.StatusBadRequest,
			"invalid_request",
			"If-Match 无效",
			`If-Match 必须使用强 ETag 格式，例如 "v1"`,
			false,
		)
		return 0, false
	default:
		return expectedVersion, true
	}
}

func writeWebhookVersionConflict(c *gin.Context, current uint64) {
	if current > 0 {
		c.Header("ETag", httpcontract.FormatETag(current))
	}
	writeHumanTicketProblem(
		c,
		http.StatusConflict,
		"version_conflict",
		"Webhook 配置版本冲突",
		"Webhook 配置已被其他操作更新，请重新读取后再试",
		true,
	)
}

// TestWebhook 测试webhook配置
// @Summary 测试webhook配置
// @Description 将测试消息作为不可变快照写入 Outbox，提交后异步投递
// @Tags webhook
// @Accept json
// @Produce json
// @Param projectKey path string true "项目 Key"
// @Param id path int true "Webhook ID"
// @Success 202 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/projects/{projectKey}/webhooks/{id}/test [post]
// @Security BearerAuth
func (h *WebhookHandler) TestWebhook(c *gin.Context) {
	operation, ok := requireWebhookManagerAccess(c)
	if !ok {
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "无效的ID",
			"data": nil,
		})
		return
	}

	ctx := c.Request.Context()
	receipt, err := h.notificationService.TestWebhook(
		ctx,
		operation.Scope,
		uint(id),
	)
	if err != nil {
		logHandlerFailure(c, "webhook.test", err)
		switch {
		case errors.Is(err, services.ErrWebhookTestAccessDenied):
			c.JSON(http.StatusForbidden, gin.H{
				"code": 1,
				"msg":  "仅项目管理员或经理可测试 Webhook",
				"data": nil,
			})
		case errors.Is(err, services.ErrWebhookTestNotFound):
			c.JSON(http.StatusNotFound, gin.H{
				"code": 1,
				"msg":  "Webhook 配置不存在或不可用",
				"data": nil,
			})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 1,
				"msg":  "Webhook 测试入队失败",
				"data": nil,
			})
		}
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"code": 0,
		"msg":  "Webhook 测试已入队",
		"data": receipt,
	})
}

// GetWebhookLogs 获取webhook日志
// @Summary 获取webhook日志
// @Description 使用不透明游标获取webhook投递记录
// @Tags webhook
// @Accept json
// @Produce json
// @Param projectKey path string true "项目 Key"
// @Param id path int true "Webhook ID"
// @Param cursor query string false "不透明续页游标"
// @Param limit query int false "每页数量" default(25)
// @Param status query string false "状态过滤"
// @Param event_type query string false "事件类型过滤"
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/projects/{projectKey}/webhooks/{id}/logs [get]
// @Security BearerAuth
func (h *WebhookHandler) GetWebhookLogs(c *gin.Context) {
	_, ok := requireWebhookManagerAccess(c)
	if !ok {
		return
	}
	id, validID := strictWebhookPositiveUint32(c.Param("id"))
	if !validID {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "无效的ID",
			"data": nil,
		})
		return
	}
	query, ok := requireWebhookDeliveryQuery(c)
	if !ok {
		return
	}
	page, err := h.queryService.ListDeliveries(
		c.Request.Context(),
		uint(id),
		query,
	)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrInvalidWebhookListCursor),
			errors.Is(err, services.ErrInvalidWebhookListQuery):
			writeInvalidWebhookListQuery(c)
		case errors.Is(err, services.ErrWebhookConfigNotFound):
			c.JSON(http.StatusNotFound, gin.H{
				"code": 1,
				"msg":  "Webhook 不存在",
				"data": nil,
			})
		case errors.Is(err, services.ErrWebhookListCursorKey):
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"code": 1,
				"msg":  "Webhook 投递记录暂不可用",
				"data": nil,
			})
		default:
			logHandlerFailure(c, "webhook.list_logs", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 1,
				"msg":  "获取日志失败",
				"data": nil,
			})
		}
		return
	}

	items := make([]WebhookLogResponse, 0, len(page.Items))
	for _, log := range page.Items {
		items = append(items, newWebhookLogResponse(log))
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "获取成功",
		"data": ListWebhookLogsResponse{
			Items:      items,
			NextCursor: page.NextCursor,
			HasMore:    page.HasMore,
		},
	})
}

// GetWebhookStats 获取webhook统计
// @Summary 获取webhook统计
// @Description 获取webhook执行统计信息
// @Tags webhook
// @Accept json
// @Produce json
// @Param projectKey path string true "项目 Key"
// @Param id path int true "Webhook ID"
// @Param days query int false "统计天数（1-90）" default(7) minimum(1) maximum(90)
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /api/projects/{projectKey}/webhooks/{id}/stats [get]
// @Security BearerAuth
func (h *WebhookHandler) GetWebhookStats(c *gin.Context) {
	operation, ok := requireWebhookManagerAccess(c)
	if !ok {
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "无效的ID",
			"data": nil,
		})
		return
	}

	days, validDays := requireWebhookStatsDays(c)
	if !validDays {
		return
	}

	startTime := time.Now().UTC().AddDate(0, 0, -days)

	// 获取基础统计
	var stats WebhookStatsSummaryResponse

	var webhook models.WebhookConfig
	if err := h.db.WithContext(c.Request.Context()).
		Select("total_sent, total_success, total_failed").
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			uint(id),
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
		).
		First(&webhook).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 1,
				"msg":  "webhook不存在",
				"data": nil,
			})
		} else {
			logHandlerFailure(c, "webhook.get_stats_config", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 1,
				"msg":  "获取统计数据失败",
				"data": nil,
			})
		}
		return
	}

	stats.TotalSent = webhook.TotalSent
	stats.TotalSuccess = webhook.TotalSuccess
	stats.TotalFailed = webhook.TotalFailed

	// 获取近期趋势数据
	dailyStats := make([]WebhookDailyStatsResponse, 0)

	rows, err := h.db.WithContext(c.Request.Context()).Raw(`
		SELECT 
			DATE(created_at) AS date,
			COUNT(*) as sent,
			COUNT(CASE WHEN status = 'success' THEN 1 END) as success,
			COUNT(CASE WHEN status = 'failed' THEN 1 END) as failed
		FROM webhook_logs 
		WHERE organization_id = ? AND project_id = ?
		  AND config_id = ? AND created_at >= ?
		GROUP BY DATE(created_at)
		ORDER BY date
	`,
		operation.Scope.OrganizationID,
		operation.Scope.ProjectID,
		uint(id),
		startTime,
	).Rows()

	if err != nil {
		logHandlerFailure(c, "webhook.query_stats", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "获取统计数据失败",
			"data": nil,
		})
		return
	}
	defer rows.Close()

	for rows.Next() {
		var stat WebhookDailyStatsResponse
		if err := h.db.ScanRows(rows, &stat); err != nil {
			logHandlerFailure(c, "webhook.scan_stats", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 1,
				"msg":  "获取统计数据失败",
				"data": nil,
			})
			return
		}
		dailyStats = append(dailyStats, stat)
	}
	if err := rows.Err(); err != nil {
		logHandlerFailure(c, "webhook.iterate_stats", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "获取统计数据失败",
			"data": nil,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "获取成功",
		"data": WebhookStatsResponse{
			Summary:    stats,
			DailyStats: dailyStats,
			Period:     fmt.Sprintf("最近%d天", days),
		},
	})
}

func requireWebhookManagerAccess(
	c *gin.Context,
) (services.OperationContext, bool) {
	if c == nil || c.Request == nil {
		return services.OperationContext{}, false
	}
	operation, err := services.OperationContextFromContext(c.Request.Context())
	if err != nil || operation.Source != services.SourceProtocolHumanREST ||
		operation.Actor.Type != models.ActorTypeHuman {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"code": 1,
			"msg":  "无权访问项目 Webhook",
			"data": nil,
		})
		return services.OperationContext{}, false
	}
	access, exists := ProjectAccessFromGin(c)
	if !exists || access.Scope != operation.Scope {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"code": 1,
			"msg":  "无权访问项目 Webhook",
			"data": nil,
		})
		return services.OperationContext{}, false
	}
	if !webhookManagerRoleAllowed(access.Role) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"code": 1,
			"msg":  "仅项目管理员或经理可管理 Webhook",
			"data": nil,
		})
		return services.OperationContext{}, false
	}
	return operation, true
}

func requireWebhookDefinitionQuery(
	c *gin.Context,
) (services.WebhookDefinitionQuery, bool) {
	values, ok := strictWebhookQueryValues(c, map[string]struct{}{
		"page":      {},
		"page_size": {},
		"provider":  {},
		"status":    {},
	})
	if !ok {
		return services.WebhookDefinitionQuery{}, false
	}
	query := services.WebhookDefinitionQuery{
		Page:     1,
		PageSize: services.DefaultWebhookListSize,
	}
	if raw, exists := values["page"]; exists {
		value, valid := strictWebhookPositiveInt(raw, int(^uint(0)>>1))
		if !valid {
			writeInvalidWebhookListQuery(c)
			return services.WebhookDefinitionQuery{}, false
		}
		query.Page = value
	}
	if raw, exists := values["page_size"]; exists {
		value, valid := strictWebhookPositiveInt(
			raw,
			services.MaxWebhookListSize,
		)
		if !valid {
			writeInvalidWebhookListQuery(c)
			return services.WebhookDefinitionQuery{}, false
		}
		query.PageSize = value
	}
	if raw, exists := values["provider"]; exists {
		query.Provider = models.WebhookProvider(raw)
		if !validWebhookProvider(query.Provider) {
			writeInvalidWebhookListQuery(c)
			return services.WebhookDefinitionQuery{}, false
		}
	}
	if raw, exists := values["status"]; exists {
		query.Status = models.WebhookStatus(raw)
		if !validWebhookStatus(query.Status) {
			writeInvalidWebhookListQuery(c)
			return services.WebhookDefinitionQuery{}, false
		}
	}
	return query, true
}

func requireWebhookDeliveryQuery(
	c *gin.Context,
) (services.WebhookDeliveryQuery, bool) {
	values, ok := strictWebhookQueryValues(c, map[string]struct{}{
		"cursor":     {},
		"limit":      {},
		"status":     {},
		"event_type": {},
	})
	if !ok {
		return services.WebhookDeliveryQuery{}, false
	}
	query := services.WebhookDeliveryQuery{
		Limit: services.DefaultWebhookListSize,
	}
	if raw, exists := values["limit"]; exists {
		value, valid := strictWebhookPositiveInt(
			raw,
			services.MaxWebhookListSize,
		)
		if !valid {
			writeInvalidWebhookListQuery(c)
			return services.WebhookDeliveryQuery{}, false
		}
		query.Limit = value
	}
	query.Cursor = values["cursor"]
	if raw, exists := values["status"]; exists {
		switch raw {
		case "pending", "success", "failed":
			query.Status = raw
		default:
			writeInvalidWebhookListQuery(c)
			return services.WebhookDeliveryQuery{}, false
		}
	}
	if raw, exists := values["event_type"]; exists {
		eventType := models.WebhookEventType(raw)
		if err := models.ValidateWebhookSubscriptions(
			[]models.WebhookEventType{eventType},
			nil,
			true,
		); err != nil {
			writeInvalidWebhookListQuery(c)
			return services.WebhookDeliveryQuery{}, false
		}
		query.EventType = eventType
	}
	return query, true
}

func requireWebhookStatsDays(c *gin.Context) (int, bool) {
	values, ok := parseStrictWebhookQueryValues(c, map[string]struct{}{
		"days": {},
	})
	if !ok {
		writeInvalidWebhookStatsQuery(c)
		return 0, false
	}
	days := 7
	if raw, exists := values["days"]; exists {
		value, valid := strictWebhookPositiveInt(raw, 90)
		if !valid {
			writeInvalidWebhookStatsQuery(c)
			return 0, false
		}
		days = value
	}
	return days, true
}

func strictWebhookQueryValues(
	c *gin.Context,
	allowed map[string]struct{},
) (map[string]string, bool) {
	result, ok := parseStrictWebhookQueryValues(c, allowed)
	if !ok {
		writeInvalidWebhookListQuery(c)
		return nil, false
	}
	return result, true
}

func parseStrictWebhookQueryValues(
	c *gin.Context,
	allowed map[string]struct{},
) (map[string]string, bool) {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return nil, false
	}
	parsed, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		return nil, false
	}
	result := make(map[string]string, len(parsed))
	for key, values := range parsed {
		if _, exists := allowed[key]; !exists || len(values) != 1 {
			return nil, false
		}
		value := values[0]
		if value == "" || strings.TrimSpace(value) != value {
			return nil, false
		}
		result[key] = value
	}
	return result, true
}

func strictWebhookPositiveInt(raw string, maximum int) (int, bool) {
	if raw == "" || maximum < 1 {
		return 0, false
	}
	for _, character := range raw {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	value, err := strconv.ParseUint(raw, 10, 31)
	if err != nil || value == 0 || value > uint64(maximum) {
		return 0, false
	}
	return int(value), true
}

func strictWebhookPositiveUint32(raw string) (uint64, bool) {
	if raw == "" {
		return 0, false
	}
	for _, character := range raw {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	value, err := strconv.ParseUint(raw, 10, 32)
	return value, err == nil && value > 0
}

func validWebhookProvider(provider models.WebhookProvider) bool {
	switch provider {
	case models.WebhookProviderWeChat,
		models.WebhookProviderDingTalk,
		models.WebhookProviderLark,
		models.WebhookProviderSlack,
		models.WebhookProviderTeams,
		models.WebhookProviderCustom:
		return true
	default:
		return false
	}
}

func validWebhookStatus(status models.WebhookStatus) bool {
	switch status {
	case models.WebhookStatusActive,
		models.WebhookStatusInactive,
		models.WebhookStatusDisabled,
		models.WebhookStatusError:
		return true
	default:
		return false
	}
}

func writeInvalidWebhookListQuery(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{
		"code": 1,
		"msg":  "列表查询参数无效",
		"data": nil,
	})
}

func writeInvalidWebhookStatsQuery(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{
		"code": 1,
		"msg":  "Webhook 统计查询参数无效，days 必须为 1 到 90 的整数",
		"data": nil,
	})
}

func webhookManagerRoleAllowed(role models.ProjectRole) bool {
	switch role {
	case models.ProjectRoleAdmin, models.ProjectRoleManager:
		return true
	default:
		return false
	}
}
