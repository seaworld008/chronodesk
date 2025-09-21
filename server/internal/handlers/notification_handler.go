package handlers

import (
    "encoding/json"
    "net/http"
    "strconv"
    "strings"
    "time"

    "github.com/gin-gonic/gin"
    "gongdan-system/internal/models"
    "gongdan-system/internal/services"
    websocketPkg "gongdan-system/internal/websocket"
)

type NotificationHandler struct {
	notificationService services.NotificationServiceInterface
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
    filter := models.NotificationFilter{}
    filter.RecipientID = &userID

    // 分页参数
    page := 1
    pageSize := 10

    if pageStr := c.Query("page"); pageStr != "" {
        if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
            page = p
        }
    }

    if pageSizeStr := c.Query("page_size"); pageSizeStr != "" {
        if ps, err := strconv.Atoi(pageSizeStr); err == nil && ps > 0 {
            pageSize = ps
        }
    }

    if limit := c.Query("limit"); limit != "" {
        if l, err := strconv.Atoi(limit); err == nil && l > 0 {
            pageSize = l
        }
    }
    if offset := c.Query("offset"); offset != "" {
        if o, err := strconv.Atoi(offset); err == nil && o >= 0 {
            page = (o / pageSize) + 1
        }
    }

    filter.Limit = pageSize
    filter.Offset = (page - 1) * pageSize

    // 解析排序参数
    if sortParam := c.Query("sort"); sortParam != "" {
        var sortFields []string
        if err := json.Unmarshal([]byte(sortParam), &sortFields); err == nil && len(sortFields) == 2 {
            field := sortFields[0]
            if !isValidNotificationSortField(field) {
                field = "created_at"
            }
            direction := strings.ToLower(sortFields[1])
            if direction != "asc" && direction != "desc" {
                direction = "desc"
            }
            filter.OrderBy = field
            filter.OrderDir = direction
        }
    }

    // 解析过滤参数(filter=...)
    if filterParam := c.Query("filter"); filterParam != "" {
        var filterMap map[string]interface{}
        if err := json.Unmarshal([]byte(filterParam), &filterMap); err != nil {
            c.JSON(http.StatusBadRequest, gin.H{
                "code": 1,
                "msg":  "过滤参数格式错误",
                "data": nil,
            })
            return
        }
        applyNotificationFilters(filterMap, &filter, userID)
    }

    // 同时兼容直接查询参数
    if filter.IsRead == nil {
        if isRead := c.Query("is_read"); isRead != "" {
            if value, err := strconv.ParseBool(isRead); err == nil {
                filter.IsRead = &value
            }
        }
    }
    if len(filter.Types) == 0 {
        if notifType := c.Query("type"); notifType != "" {
            filter.Types = []models.NotificationType{models.NotificationType(notifType)}
        }
    }
    if len(filter.Priorities) == 0 {
        if priority := c.Query("priority"); priority != "" {
            filter.Priorities = []models.NotificationPriority{models.NotificationPriority(priority)}
        }
    }

    if filter.OrderBy == "" {
        filter.OrderBy = "created_at"
    }
    if filter.OrderDir == "" {
        filter.OrderDir = "desc"
    }

    notifications, total, err := h.notificationService.GetNotifications(c.Request.Context(), &filter)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{
            "code": 1,
            "msg":  "获取通知失败: " + err.Error(),
            "data": nil,
        })
        return
    }

    var responses []*models.NotificationResponse
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
        if v < 0 {
            return 0, false
        }
        return uint(v), true
    case float32:
        if v < 0 {
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
        return uint(v), true
    case uint:
        return v, true
    case string:
        trimmed := strings.TrimSpace(v)
        if trimmed == "" {
            return 0, false
        }
        parsed, err := strconv.ParseUint(trimmed, 10, 64)
        if err != nil {
            return 0, false
        }
        return uint(parsed), true
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
    case "created_at", "priority", "type", "channel", "recipient_id", "sender_id", "is_read", "is_sent":
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "标记已读失败"})
		return
	}

	// 触发WebSocket实时更新未读数量
	websocketPkg.NotificationMarkedAsReadHook(c.Request.Context(), userID.(uint), uint(id))

	c.JSON(http.StatusOK, gin.H{"message": "标记成功"})
}

// MarkAllAsRead 标记所有通知为已读
func (h *NotificationHandler) MarkAllAsRead(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未授权"})
		return
	}

	err := h.notificationService.MarkAllAsRead(c.Request.Context(), userID.(uint))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "批量标记失败"})
		return
	}

	// 触发WebSocket实时更新未读数量
	websocketPkg.NotificationAllMarkedAsReadHook(c.Request.Context(), userID.(uint))

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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取未读数量失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"count": count})
}

// CreateNotification 创建通知 (管理员接口)
func (h *NotificationHandler) CreateNotification(c *gin.Context) {
	var req models.NotificationCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数错误", "details": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建通知失败", "details": err.Error()})
		return
	}

	// 触发WebSocket实时推送
	websocketPkg.NotificationCreatedHook(c.Request.Context(), notification)

	c.JSON(http.StatusCreated, gin.H{"data": notification.ToResponse()})
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

	var preferences []models.NotificationPreference
	if err := c.ShouldBindJSON(&preferences); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数错误"})
		return
	}

	err := h.notificationService.UpdateNotificationPreferences(c.Request.Context(), userID.(uint), preferences)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新偏好设置失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "更新成功"})
}
