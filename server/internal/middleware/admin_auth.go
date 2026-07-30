package middleware

import (
	"context"
	"fmt"
	"log"
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
		method := c.Request.Method
		path := c.Request.URL.Path
		if !isImportantAdminOperation(method, path) {
			c.Next()
			return
		}
		if auditService == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"code": 1,
				"msg":  "管理员审计服务不可用",
			})
			return
		}

		start := time.Now()
		query := sanitizeQueryForLogs(c.Request.URL.RawQuery)
		clientIP := c.ClientIP()
		userAgent := c.Request.UserAgent()

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

		action := fmt.Sprintf("%s %s", strings.ToUpper(method), path)
		record := &services.AdminAuditRecord{
			UserID:     userIDPtr,
			Role:       role,
			Action:     action,
			Method:     method,
			Path:       path,
			StatusCode: 0,
			ClientIP:   clientIP,
			UserAgent:  userAgent,
			Query:      query,
			Result:     "pending",
			Notes:      "管理员写操作已进入执行阶段",
		}

		if err := auditService.Record(c.Request.Context(), record); err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"code": 1,
				"msg":  "管理员审计记录失败，操作未执行",
			})
			return
		}

		completed := false
		defer func() {
			record.StatusCode = c.Writer.Status()
			record.Latency = time.Since(start)
			record.Result = "success"
			record.Notes = ""
			if !completed {
				record.StatusCode = http.StatusInternalServerError
				record.Result = "error"
				record.Notes = "管理员写操作异常终止"
			} else if record.StatusCode >= http.StatusBadRequest {
				record.Result = "error"
			}

			finalizeContext, cancel := context.WithTimeout(
				context.WithoutCancel(c.Request.Context()),
				3*time.Second,
			)
			defer cancel()
			if err := auditService.Finalize(finalizeContext, record); err != nil {
				// Persistence details may contain SQL values. Keep the operator
				// signal fixed; the pending anchor remains durable for recovery.
				log.Print("管理员审计最终状态写入失败，已保留 pending 锚点")
			}
		}()
		c.Next()
		completed = true
	}
}

// isImportantAdminOperation 判断是否为重要的管理操作
func isImportantAdminOperation(method, path string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}

	return isPathWithin(path, "/api/admin") ||
		isProjectAgentAdminPath(path)
}

func isPathWithin(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func isProjectAgentAdminPath(path string) bool {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	return len(segments) >= 5 &&
		segments[0] == "api" &&
		segments[1] == "projects" &&
		strings.TrimSpace(segments[2]) != "" &&
		segments[3] == "admin" &&
		segments[4] == "agents"
}

const maxLoggedQueryBytes = 2048

func sanitizeQueryForLogs(rawQuery string) string {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return ""
	}
	for key := range values {
		if isSensitiveQueryKey(key) {
			values.Set(key, "[REDACTED]")
		}
	}
	encoded := values.Encode()
	if len(encoded) > maxLoggedQueryBytes {
		return "[TRUNCATED]"
	}
	return encoded
}

func isSensitiveQueryKey(key string) bool {
	normalized := strings.NewReplacer("-", "_", ".", "_").
		Replace(strings.ToLower(strings.TrimSpace(key)))
	switch normalized {
	case "authorization",
		"api_key",
		"apikey",
		"client_assertion",
		"client_secret",
		"code",
		"credential",
		"otp",
		"password",
		"saml_response",
		"secret",
		"signature",
		"token":
		return true
	}
	for _, suffix := range []string{
		"_code",
		"_credential",
		"_password",
		"_secret",
		"_signature",
		"_token",
	} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}
