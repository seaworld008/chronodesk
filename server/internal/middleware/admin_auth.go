package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

// GetCurrentUserRole 从上下文中获取当前用户角色
func GetCurrentUserRole(c *gin.Context) (string, bool) {
	userRole, exists := c.Get("user_role")
	if !exists {
		return "", false
	}

	role, ok := userRole.(string)
	if !ok {
		return "", false
	}

	return role, true
}

// GetCurrentUserID 从上下文中获取当前用户ID
func GetCurrentUserID(c *gin.Context) (uint, bool) {
	userID, exists := c.Get("user_id")
	if !exists {
		return 0, false
	}

	id, ok := userID.(uint)
	if !ok {
		return 0, false
	}

	return id, true
}

// LogAdminOperation 记录管理员操作日志的中间件
func LogAdminOperation(auditService services.AdminAuditServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		method := c.Request.Method
		path := c.Request.URL.Path
		query := sanitizeAdminAuditQuery(c.Request.URL.RawQuery)
		clientIP := c.ClientIP()
		userAgent := c.Request.UserAgent()

		// 执行下一个处理器
		c.Next()

		if auditService == nil {
			return
		}

		if !isImportantAdminOperation(method, path) {
			return
		}

		statusCode := c.Writer.Status()
		latency := time.Since(start)

		userID, hasUser := GetCurrentUserID(c)
		var userIDPtr *uint
		if hasUser {
			userIDPtr = &userID
		}

		role, _ := GetCurrentUserRole(c)
		if role == "" {
			if value, ok := c.Get("user_role"); ok {
				if str, ok := value.(string); ok {
					role = str
				}
			}
		}

		ctx := c.Request.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		action := fmt.Sprintf("%s %s", strings.ToUpper(method), path)
		result := "success"
		if statusCode >= http.StatusBadRequest {
			result = "error"
		}

		record := &services.AdminAuditRecord{
			UserID:     userIDPtr,
			Role:       role,
			Action:     action,
			Method:     method,
			Path:       path,
			StatusCode: statusCode,
			ClientIP:   clientIP,
			UserAgent:  userAgent,
			Query:      query,
			Latency:    latency,
			Result:     result,
		}

		if err := auditService.Record(ctx, record); err != nil {
			fmt.Println("[ADMIN-OP] failed to record audit log:", err)
		}
	}
}

// isImportantAdminOperation 判断是否为重要的管理操作
func isImportantAdminOperation(method, path string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}

	return isPathWithin(path, "/api/admin") ||
		isPathWithin(path, "/api/v1/admin")
}

func isPathWithin(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func sanitizeAdminAuditQuery(rawQuery string) string {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return ""
	}
	for key := range values {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "client_secret", "secret", "password", "access_token", "refresh_token", "token":
			values.Set(key, "[REDACTED]")
		}
	}
	return values.Encode()
}
