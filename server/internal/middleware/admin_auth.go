package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gongdan-system/internal/services"
)

// RequireAdminRole 要求管理员角色的中间件
// 这个中间件应该在JWT认证中间件之后使用
func RequireAdminRole() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 检查用户是否已认证
		userRole, exists := c.Get("user_role")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "User not authenticated",
				"code":  "AUTHENTICATION_REQUIRED",
			})
			c.Abort()
			return
		}

		// 检查用户角色
		role, ok := userRole.(string)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "Invalid user role type",
				"code":  "INVALID_ROLE_TYPE",
			})
			c.Abort()
			return
		}

		// 检查是否为管理员
		if role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "Administrator privileges required",
				"code":  "INSUFFICIENT_PRIVILEGES",
			})
			c.Abort()
			return
		}

		// 继续执行下一个中间件或处理器
		c.Next()
	}
}

// RequireAdminOrSupervisor 要求管理员或主管角色的中间件
func RequireAdminOrSupervisor() gin.HandlerFunc {
	return func(c *gin.Context) {
		userRole, exists := c.Get("user_role")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "User not authenticated",
				"code":  "AUTHENTICATION_REQUIRED",
			})
			c.Abort()
			return
		}

		role, ok := userRole.(string)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "Invalid user role type",
				"code":  "INVALID_ROLE_TYPE",
			})
			c.Abort()
			return
		}

		// 检查是否为管理员或主管
		if role != "admin" && role != "supervisor" {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "Administrator or supervisor privileges required",
				"code":  "INSUFFICIENT_PRIVILEGES",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireStaffRole 要求员工角色（管理员、主管、客服代理）的中间件
func RequireStaffRole() gin.HandlerFunc {
	return func(c *gin.Context) {
		userRole, exists := c.Get("user_role")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "User not authenticated",
				"code":  "AUTHENTICATION_REQUIRED",
			})
			c.Abort()
			return
		}

		role, ok := userRole.(string)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "Invalid user role type",
				"code":  "INVALID_ROLE_TYPE",
			})
			c.Abort()
			return
		}

		// 检查是否为员工角色
		if role != "admin" && role != "supervisor" && role != "agent" {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "Staff privileges required",
				"code":  "INSUFFICIENT_PRIVILEGES",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

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

// IsCurrentUserAdmin 检查当前用户是否为管理员
func IsCurrentUserAdmin(c *gin.Context) bool {
	role, exists := GetCurrentUserRole(c)
	if !exists {
		return false
	}
	return role == "admin"
}

// IsCurrentUserStaff 检查当前用户是否为员工
func IsCurrentUserStaff(c *gin.Context) bool {
	role, exists := GetCurrentUserRole(c)
	if !exists {
		return false
	}
	return role == "admin" || role == "supervisor" || role == "agent"
}

// PreventSelfOperation 防止用户对自己进行某些操作的中间件
// 比如删除自己的账号或修改自己的角色
func PreventSelfOperation() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 获取当前用户ID
		currentUserID, exists := GetCurrentUserID(c)
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "User not authenticated",
				"code":  "AUTHENTICATION_REQUIRED",
			})
			c.Abort()
			return
		}

		// 获取目标用户ID（从路径参数）
		targetUserIDStr := c.Param("id")
		if targetUserIDStr == "" {
			// 如果没有路径参数，说明不是对特定用户的操作，继续执行
			c.Next()
			return
		}

		// 解析目标用户ID
		var targetUserID uint
		if _, err := fmt.Sscanf(targetUserIDStr, "%d", &targetUserID); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Invalid user ID",
				"code":  "INVALID_USER_ID",
			})
			c.Abort()
			return
		}

		// 检查是否为自己
		if currentUserID == targetUserID {
			c.JSON(http.StatusConflict, gin.H{
				"error": "Cannot perform this operation on yourself",
				"code":  "SELF_OPERATION_NOT_ALLOWED",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// LogAdminOperation 记录管理员操作日志的中间件
func LogAdminOperation(auditService services.AdminAuditServiceInterface) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		method := c.Request.Method
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery
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
	// 定义需要记录的重要操作路径
	importantPaths := []string{
		"/api/admin/users",    // 用户管理
		"/api/admin/settings", // 系统设置
		"/api/admin/webhooks", // Webhook管理
		"/api/admin/backup",   // 备份操作
	}

	// 检查路径是否匹配
	for _, importantPath := range importantPaths {
		if strings.HasPrefix(path, importantPath) {
			// 只记录非GET请求（修改操作）
			return method != "GET"
		}
	}

	return false
}
