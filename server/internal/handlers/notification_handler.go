package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/safeconv"
	"github.com/seaworld008/chronodesk/server/internal/services"
	websocketPkg "github.com/seaworld008/chronodesk/server/internal/websocket"
)

type NotificationHandler struct {
	notificationService services.NotificationServiceInterface
}

type notificationPreferenceUpdateItem struct {
	NotificationType  models.NotificationType `json:"notification_type" binding:"required,oneof=ticket_assigned ticket_status_changed ticket_commented ticket_created ticket_overdue ticket_resolved ticket_closed system_maintenance user_mention system_alert"`
	EmailEnabled      *bool                   `json:"email_enabled" binding:"required"`
	InAppEnabled      *bool                   `json:"in_app_enabled" binding:"required"`
	WebhookEnabled    *bool                   `json:"webhook_enabled" binding:"required"`
	DoNotDisturbStart *time.Time              `json:"do_not_disturb_start"`
	DoNotDisturbEnd   *time.Time              `json:"do_not_disturb_end"`
	MaxDailyCount     *int                    `json:"max_daily_count" binding:"required,gte=0,lte=10000"`
	BatchDelivery     *bool                   `json:"batch_delivery" binding:"required"`
	BatchInterval     *int                    `json:"batch_interval" binding:"required,gte=1,lte=1440"`
}

type updateNotificationPreferencesRequest struct {
	Preferences []notificationPreferenceUpdateItem `json:"preferences" binding:"required,min=1,max=10,dive"`
}

func NewNotificationHandler(notificationService services.NotificationServiceInterface) *NotificationHandler {
	return &NotificationHandler{
		notificationService: notificationService,
	}
}

// GetNotifications 获取用户通知列表
func (h *NotificationHandler) GetNotifications(c *gin.Context) {
	userIDValue, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code": 1,
			"msg":  "用户未认证",
			"data": nil,
		})
		return
	}

	userID := userIDValue.(uint)
	values, err := strictNotificationListQueryValues(
		c.Request.URL.RawQuery,
	)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "通知列表查询参数无效",
			"data": nil,
		})
		return
	}

	page, err := parseDirectoryPositiveInt(
		values,
		"page",
		1,
		math.MaxInt/defaultDirectoryPageSize,
	)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "通知列表查询参数无效",
			"data": nil,
		})
		return
	}
	pageSize, err := parseDirectoryPositiveInt(
		values,
		"page_size",
		defaultDirectoryPageSize,
		maxDirectoryPageSize,
	)
	if err != nil || page > math.MaxInt/pageSize {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1,
			"msg":  "通知列表查询参数无效",
			"data": nil,
		})
		return
	}

	filter := models.NotificationFilter{RecipientID: &userID}
	filter.Limit = pageSize
	filter.Offset = (page - 1) * pageSize

	// 解析排序参数
	if sortParam := values.Get("sort"); sortParam != "" {
		var sortFields []string
		if err := decodeStrictNotificationJSON(sortParam, &sortFields); err != nil ||
			len(sortFields) != 2 ||
			!isValidNotificationSortField(sortFields[0]) ||
			(sortFields[1] != "ASC" && sortFields[1] != "DESC") {
			c.JSON(http.StatusBadRequest, gin.H{
				"code": 1,
				"msg":  "通知列表排序参数无效",
				"data": nil,
			})
			return
		}
		filter.OrderBy = sortFields[0]
		filter.OrderDir = strings.ToLower(sortFields[1])
	}

	// 解析过滤参数(filter=...)
	if filterParam := values.Get("filter"); filterParam != "" {
		var filterMap map[string]interface{}
		if err := decodeStrictNotificationJSON(
			filterParam,
			&filterMap,
		); err != nil ||
			validateNotificationFilterMap(filterMap, userID) != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"code": 1,
				"msg":  "过滤参数格式错误",
				"data": nil,
			})
			return
		}
		applyNotificationFilters(filterMap, &filter, userID)
	}

	if filter.OrderBy == "" {
		filter.OrderBy = "created_at"
	}
	if filter.OrderDir == "" {
		filter.OrderDir = "desc"
	}

	notifications, total, err := h.notificationService.GetNotifications(c.Request.Context(), &filter)
	if err != nil {
		if errors.Is(err, services.ErrInvalidNotificationListQuery) {
			c.JSON(http.StatusBadRequest, gin.H{
				"code": 1,
				"msg":  "通知列表查询参数无效",
				"data": nil,
			})
			return
		}
		logHandlerFailure(c, "notification.list", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 1,
			"msg":  "获取通知失败",
			"data": nil,
		})
		return
	}

	responses := make([]*models.NotificationResponse, 0, len(notifications))
	for _, notification := range notifications {
		responses = append(responses, notification.ToResponse())
	}

	totalPages := int64(0)
	if pageSize > 0 {
		totalPages = (total + int64(pageSize) - 1) / int64(pageSize)
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "获取通知列表成功",
		"data": gin.H{
			"items":       responses,
			"page":        page,
			"page_size":   pageSize,
			"total":       total,
			"total_pages": totalPages,
		},
	})
}

func strictNotificationListQueryValues(
	rawQuery string,
) (url.Values, error) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, err
	}
	allowed := map[string]struct{}{
		"page":      {},
		"page_size": {},
		"sort":      {},
		"filter":    {},
	}
	for name, entries := range values {
		if _, ok := allowed[name]; !ok || len(entries) != 1 ||
			!utf8.ValidString(name) || !utf8.ValidString(entries[0]) ||
			containsDirectoryQueryControl(name) ||
			containsDirectoryQueryControl(entries[0]) ||
			strings.TrimSpace(entries[0]) == "" ||
			strings.TrimSpace(entries[0]) != entries[0] {
			return nil, errors.New("invalid notification list query")
		}
	}
	return values, nil
}

func decodeStrictNotificationJSON(raw string, target any) error {
	if len(raw) > 8192 {
		return errors.New("notification list JSON is too large")
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("notification list JSON must contain one value")
	}
	return nil
}

func validateNotificationFilterMap(
	filterMap map[string]interface{},
	currentUserID uint,
) error {
	allowed := map[string]struct{}{
		"q": {}, "type": {}, "types": {}, "priority": {}, "channel": {},
		"is_read": {}, "is_sent": {}, "is_delivered": {},
		"related_type": {}, "related_id": {}, "related_ticket_id": {},
		"sender_id": {}, "recipient_id": {}, "created_at_gte": {},
		"created_at_lte": {},
	}
	for key := range filterMap {
		if _, ok := allowed[key]; !ok {
			return errors.New("unsupported notification filter")
		}
	}
	if raw, ok := filterMap["q"]; ok {
		value, valid := raw.(string)
		if !valid || strings.TrimSpace(value) != value ||
			value == "" || len([]rune(value)) > 200 {
			return errors.New("invalid notification search")
		}
	}
	for _, key := range []string{"type", "types"} {
		if raw, ok := filterMap[key]; ok {
			values := parseNotificationTypes(raw)
			if len(values) == 0 || len(values) > len(models.NotificationTypes()) {
				return errors.New("invalid notification type")
			}
			for _, value := range values {
				if !value.IsValid() {
					return errors.New("invalid notification type")
				}
			}
		}
	}
	if raw, ok := filterMap["priority"]; ok {
		values := parseNotificationPriorities(raw)
		if len(values) == 0 || len(values) > 4 {
			return errors.New("invalid notification priority")
		}
		for _, value := range values {
			switch value {
			case models.NotificationPriorityLow,
				models.NotificationPriorityNormal,
				models.NotificationPriorityHigh,
				models.NotificationPriorityUrgent:
			default:
				return errors.New("invalid notification priority")
			}
		}
	}
	if raw, ok := filterMap["channel"]; ok {
		values := parseNotificationChannels(raw)
		if len(values) == 0 || len(values) > 4 {
			return errors.New("invalid notification channel")
		}
		for _, value := range values {
			switch value {
			case models.NotificationChannelInApp,
				models.NotificationChannelEmail,
				models.NotificationChannelWebhook,
				models.NotificationChannelWebSocket:
			default:
				return errors.New("invalid notification channel")
			}
		}
	}
	for _, key := range []string{"is_read", "is_sent", "is_delivered"} {
		if raw, ok := filterMap[key]; ok {
			if _, valid := parseBoolValue(raw); !valid {
				return errors.New("invalid notification boolean")
			}
		}
	}
	for _, key := range []string{
		"related_id",
		"related_ticket_id",
		"sender_id",
		"recipient_id",
	} {
		if raw, ok := filterMap[key]; ok {
			value, valid := parseUintValue(raw)
			if !valid || value == 0 ||
				(key == "recipient_id" && value != currentUserID) {
				return errors.New("invalid notification identity filter")
			}
		}
	}
	if raw, ok := filterMap["related_type"]; ok {
		value, valid := raw.(string)
		if !valid || strings.TrimSpace(value) != value ||
			value == "" || len([]rune(value)) > 50 {
			return errors.New("invalid notification relation filter")
		}
	}
	for _, key := range []string{"created_at_gte", "created_at_lte"} {
		if raw, ok := filterMap[key]; ok {
			if _, valid := parseDateValue(raw); !valid {
				return errors.New("invalid notification date filter")
			}
		}
	}
	return nil
}

func applyNotificationFilters(filterMap map[string]interface{}, filter *models.NotificationFilter, currentUserID uint) {
	if raw, ok := filterMap["q"].(string); ok {
		if trimmed := strings.TrimSpace(raw); trimmed != "" {
			filter.Query = trimmed
		}
	}

	if rawType, ok := filterMap["type"]; ok {
		filter.Types = parseNotificationTypes(rawType)
	} else if rawTypes, ok := filterMap["types"]; ok {
		filter.Types = parseNotificationTypes(rawTypes)
	}

	if rawPriority, ok := filterMap["priority"]; ok {
		filter.Priorities = parseNotificationPriorities(rawPriority)
	}

	if rawChannel, ok := filterMap["channel"]; ok {
		filter.Channels = parseNotificationChannels(rawChannel)
	}

	if val, ok := parseBoolValue(filterMap["is_read"]); ok {
		filter.IsRead = &val
	}
	if val, ok := parseBoolValue(filterMap["is_sent"]); ok {
		filter.IsSent = &val
	}
	if val, ok := parseBoolValue(filterMap["is_delivered"]); ok {
		filter.IsDelivered = &val
	}

	if raw := filterMap["related_type"]; raw != nil {
		if str, ok := raw.(string); ok && strings.TrimSpace(str) != "" {
			filter.RelatedType = strings.TrimSpace(str)
		}
	}

	if val, ok := parseUintValue(filterMap["related_id"]); ok {
		filter.RelatedID = &val
	}
	if val, ok := parseUintValue(filterMap["related_ticket_id"]); ok {
		filter.RelatedTicketID = &val
	}

	if val, ok := parseUintValue(filterMap["sender_id"]); ok {
		filter.SenderID = &val
	}

	if val, ok := parseUintValue(filterMap["recipient_id"]); ok && val == currentUserID {
		filter.RecipientID = &val
	}

	if t, ok := parseDateValue(filterMap["created_at_gte"]); ok {
		filter.CreatedAfter = t
	}
	if t, ok := parseDateValue(filterMap["created_at_lte"]); ok {
		filter.CreatedBefore = t
	}
}

func parseNotificationTypes(value interface{}) []models.NotificationType {
	switch v := value.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil
		}
		return []models.NotificationType{models.NotificationType(trimmed)}
	case []interface{}:
		var result []models.NotificationType
		for _, item := range v {
			if str, ok := item.(string); ok && strings.TrimSpace(str) != "" {
				result = append(result, models.NotificationType(strings.TrimSpace(str)))
			}
		}
		return result
	case []string:
		var result []models.NotificationType
		for _, str := range v {
			if trimmed := strings.TrimSpace(str); trimmed != "" {
				result = append(result, models.NotificationType(trimmed))
			}
		}
		return result
	default:
		return nil
	}
}

func parseNotificationPriorities(value interface{}) []models.NotificationPriority {
	switch v := value.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil
		}
		return []models.NotificationPriority{models.NotificationPriority(trimmed)}
	case []interface{}:
		var result []models.NotificationPriority
		for _, item := range v {
			if str, ok := item.(string); ok && strings.TrimSpace(str) != "" {
				result = append(result, models.NotificationPriority(strings.TrimSpace(str)))
			}
		}
		return result
	case []string:
		var result []models.NotificationPriority
		for _, str := range v {
			if trimmed := strings.TrimSpace(str); trimmed != "" {
				result = append(result, models.NotificationPriority(trimmed))
			}
		}
		return result
	default:
		return nil
	}
}

func parseNotificationChannels(value interface{}) []models.NotificationChannel {
	switch v := value.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil
		}
		return []models.NotificationChannel{models.NotificationChannel(trimmed)}
	case []interface{}:
		var result []models.NotificationChannel
		for _, item := range v {
			if str, ok := item.(string); ok && strings.TrimSpace(str) != "" {
				result = append(result, models.NotificationChannel(strings.TrimSpace(str)))
			}
		}
		return result
	case []string:
		var result []models.NotificationChannel
		for _, str := range v {
			if trimmed := strings.TrimSpace(str); trimmed != "" {
				result = append(result, models.NotificationChannel(trimmed))
			}
		}
		return result
	default:
		return nil
	}
}

func parseBoolValue(value interface{}) (bool, bool) {
	switch v := value.(type) {
	case bool:
		return v, true
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return false, false
		}
		parsed, err := strconv.ParseBool(trimmed)
		if err != nil {
			return false, false
		}
		return parsed, true
	case float64:
		if v == 1 {
			return true, true
		}
		if v == 0 {
			return false, true
		}
		return false, false
	default:
		return false, false
	}
}

func parseUintValue(value interface{}) (uint, bool) {
	switch v := value.(type) {
	case float64:
		if v < 0 || math.Trunc(v) != v || v >= math.Ldexp(1, strconv.IntSize) {
			return 0, false
		}
		return uint(v), true
	case float32:
		asFloat64 := float64(v)
		if v < 0 || math.Trunc(asFloat64) != asFloat64 || asFloat64 >= math.Ldexp(1, strconv.IntSize) {
			return 0, false
		}
		return uint(v), true
	case int:
		if v < 0 {
			return 0, false
		}
		return uint(v), true
	case int64:
		if v < 0 {
			return 0, false
		}
		parsed, err := safeconv.Uint(uint64(v))
		return parsed, err == nil
	case uint:
		return v, true
	case uint64:
		parsed, err := safeconv.Uint(v)
		return parsed, err == nil
	case json.Number:
		parsed, err := strconv.ParseUint(string(v), 10, 0)
		if err != nil || parsed == 0 {
			return 0, false
		}
		value, convertErr := safeconv.Uint(parsed)
		return value, convertErr == nil
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return 0, false
		}
		parsed, err := safeconv.ParseUint(trimmed)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func parseDateValue(value interface{}) (*time.Time, bool) {
	str, ok := value.(string)
	if !ok {
		return nil, false
	}

	trimmed := strings.TrimSpace(str)
	if trimmed == "" {
		return nil, false
	}

	layouts := []string{time.RFC3339, "2006-01-02"}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, trimmed); err == nil {
			return &ts, true
		}
	}

	return nil, false
}

func isValidNotificationSortField(field string) bool {
	switch field {
	case "id", "created_at", "updated_at", "priority", "type", "channel",
		"is_read", "recipient_id", "sender_id":
		return true
	default:
		return false
	}
}

// MarkAsRead 标记通知为已读
func (h *NotificationHandler) MarkAsRead(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未授权"})
		return
	}
	access, ok := ProjectAccessFromGin(c)
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": "未解析可信项目范围"})
		return
	}

	notificationID := c.Param("id")
	id, err := strconv.ParseUint(notificationID, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的通知ID"})
		return
	}

	err = h.notificationService.MarkAsRead(c.Request.Context(), uint(id), userID.(uint))
	if err != nil {
		// 根据错误类型返回不同的状态码
		if err.Error() == "通知不存在" {
			c.JSON(http.StatusNotFound, gin.H{"error": "通知不存在"})
			return
		}
		if err.Error() == "无权限操作此通知" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无权限操作此通知"})
			return
		}
		logHandlerFailure(c, "notification.mark_read", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "标记已读失败"})
		return
	}

	// 触发WebSocket实时更新未读数量（使用真实计数）
	unreadCount, countErr := h.notificationService.GetUnreadCount(c.Request.Context(), userID.(uint))
	if countErr != nil {
		logHandlerFailure(c, "notification.refresh_unread_count", countErr)
	} else {
		_ = websocketPkg.NotificationMarkedAsReadHook(
			c.Request.Context(),
			access.Scope,
			userID.(uint),
			unreadCount,
		)
	}

	c.JSON(http.StatusOK, gin.H{"message": "标记成功"})
}

// MarkAllAsRead 标记所有通知为已读
func (h *NotificationHandler) MarkAllAsRead(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未授权"})
		return
	}
	access, ok := ProjectAccessFromGin(c)
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": "未解析可信项目范围"})
		return
	}

	err := h.notificationService.MarkAllAsRead(c.Request.Context(), userID.(uint))
	if err != nil {
		logHandlerFailure(c, "notification.mark_all_read", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "批量标记失败"})
		return
	}

	// 触发WebSocket实时更新未读数量
	_ = websocketPkg.NotificationAllMarkedAsReadHook(
		c.Request.Context(),
		access.Scope,
		userID.(uint),
	)

	c.JSON(http.StatusOK, gin.H{"message": "标记成功"})
}

// GetUnreadCount 获取未读通知数量
func (h *NotificationHandler) GetUnreadCount(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未授权"})
		return
	}

	count, err := h.notificationService.GetUnreadCount(c.Request.Context(), userID.(uint))
	if err != nil {
		logHandlerFailure(c, "notification.get_unread_count", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取未读数量失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"count": count})
}

// CreateNotification 创建通知 (管理员接口)
func (h *NotificationHandler) CreateNotification(c *gin.Context) {
	access, ok := ProjectAccessFromGin(c)
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": "未解析可信项目范围"})
		return
	}
	if access.Role != models.ProjectRoleAdmin &&
		access.Role != models.ProjectRoleManager {
		c.JSON(http.StatusForbidden, gin.H{"error": "仅项目管理员或经理可创建通知"})
		return
	}
	var req models.NotificationCreateRequest
	if err := decodeStrictJSON(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数错误"})
		return
	}

	// 手动验证必需字段
	if req.Title == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "标题不能为空"})
		return
	}
	if req.Content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "内容不能为空"})
		return
	}
	if req.RecipientID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "接收者ID不能为空"})
		return
	}

	notification, err := h.notificationService.CreateNotification(c.Request.Context(), &req)
	if err != nil {
		logHandlerFailure(c, "notification.create", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建通知失败"})
		return
	}

	// 触发WebSocket实时推送
	websocketPkg.NotificationCreatedHook(c.Request.Context(), notification)

	c.JSON(http.StatusCreated, gin.H{"data": notification.ToResponse()})
}

// DeleteNotification 删除通知 (管理员接口)
func (h *NotificationHandler) DeleteNotification(c *gin.Context) {
	access, ok := ProjectAccessFromGin(c)
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": "未解析可信项目范围"})
		return
	}
	if access.Role != models.ProjectRoleAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "仅项目管理员可删除通知"})
		return
	}
	notificationID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的通知ID"})
		return
	}

	if err := h.notificationService.DeleteNotification(c.Request.Context(), uint(notificationID)); err != nil {
		status := http.StatusInternalServerError
		message := "删除通知失败"
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
			message = "通知不存在"
		} else {
			logHandlerFailure(c, "notification.delete", err)
		}
		c.JSON(status, gin.H{"error": message})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "删除通知成功"})
}

// GetNotificationPreferences 获取用户通知偏好设置
func (h *NotificationHandler) GetNotificationPreferences(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未授权"})
		return
	}

	preferences, err := h.notificationService.GetNotificationPreferences(c.Request.Context(), userID.(uint))
	if err != nil {
		logHandlerFailure(c, "notification.get_preferences", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取偏好设置失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": preferences})
}

// UpdateNotificationPreferences 更新用户通知偏好设置
func (h *NotificationHandler) UpdateNotificationPreferences(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未授权"})
		return
	}

	var request updateNotificationPreferencesRequest
	if err := decodeStrictJSON(c, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数错误"})
		return
	}
	preferences := make([]models.NotificationPreference, 0, len(request.Preferences))
	seenTypes := make(map[models.NotificationType]struct{}, len(request.Preferences))
	for _, item := range request.Preferences {
		if _, duplicate := seenTypes[item.NotificationType]; duplicate {
			c.JSON(http.StatusBadRequest, gin.H{"error": "通知类型不能重复"})
			return
		}
		seenTypes[item.NotificationType] = struct{}{}
		if (item.DoNotDisturbStart == nil) != (item.DoNotDisturbEnd == nil) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "免打扰开始和结束时间必须同时提供"})
			return
		}
		preferences = append(preferences, models.NotificationPreference{
			NotificationType:  item.NotificationType,
			EmailEnabled:      *item.EmailEnabled,
			InAppEnabled:      *item.InAppEnabled,
			WebhookEnabled:    *item.WebhookEnabled,
			DoNotDisturbStart: item.DoNotDisturbStart,
			DoNotDisturbEnd:   item.DoNotDisturbEnd,
			MaxDailyCount:     *item.MaxDailyCount,
			BatchDelivery:     *item.BatchDelivery,
			BatchInterval:     *item.BatchInterval,
		})
	}

	err := h.notificationService.UpdateNotificationPreferences(c.Request.Context(), userID.(uint), preferences)
	if err != nil {
		if errors.Is(err, services.ErrInvalidNotificationPreferences) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "通知偏好设置无效"})
			return
		}
		logHandlerFailure(c, "notification.update_preferences", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新偏好设置失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "更新成功"})
}
