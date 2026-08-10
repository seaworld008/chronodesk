package middleware

import (
	"fmt"
	"runtime"
)

// RecoveryConfig 恢复中间件配置
type RecoveryConfig struct {
	// Logger 日志器
	Logger Logger
	// EnableStackTrace 是否启用堆栈跟踪
	EnableStackTrace bool
	// StackSize 堆栈大小
	StackSize int
	// DisableStackAll 是否禁用所有goroutine的堆栈
	DisableStackAll bool
	// DisablePrintStack 是否禁用打印堆栈
	DisablePrintStack bool
	// RecoveryHandler 自定义恢复处理器
	RecoveryHandler func(HTTPContext, interface{})
	// ErrorHandler 错误处理器
	ErrorHandler func(HTTPContext, error)
}

// DefaultRecoveryConfig 默认恢复配置
func DefaultRecoveryConfig() *RecoveryConfig {
	return &RecoveryConfig{
		Logger:            NewSimpleLogger(nil, LogLevelError),
		EnableStackTrace:  true,
		StackSize:         4 << 10, // 4KB
		DisableStackAll:   false,
		DisablePrintStack: false,
	}
}

// RecoveryMiddleware 恢复中间件
func RecoveryMiddleware(config *RecoveryConfig) func(HTTPContext) {
	if config == nil {
		config = DefaultRecoveryConfig()
	}

	if config.Logger == nil {
		config.Logger = NewSimpleLogger(nil, LogLevelError)
	}

	return func(c HTTPContext) {
		defer func() {
			if err := recover(); err != nil {
				handlePanic(c, err, config)
			}
		}()

		c.Next()
	}
}

// handlePanic 处理panic
func handlePanic(c HTTPContext, err interface{}, config *RecoveryConfig) {
	// 获取堆栈信息
	var stack []byte
	if config.EnableStackTrace {
		stack = getStack(config.StackSize, config.DisableStackAll)
	}

	// 记录错误日志
	logPanic(config.Logger, err, stack, c, config)

	// 调用自定义恢复处理器
	if config.RecoveryHandler != nil {
		config.RecoveryHandler(c, err)
		return
	}

	// 默认错误响应
	defaultErrorResponse(c, err)
}

// getStack 获取堆栈信息
func getStack(stackSize int, disableStackAll bool) []byte {
	stack := make([]byte, stackSize)
	length := runtime.Stack(stack, !disableStackAll)
	return stack[:length]
}

// logPanic 记录panic日志
func logPanic(logger Logger, err interface{}, stack []byte, c HTTPContext, config *RecoveryConfig) {
	sensitiveAuthenticationRoute :=
		isSensitiveAuthenticationLoggingRoute(c)
	errorValue := fmt.Sprintf("%v", err)
	userAgent := c.GetHeader("User-Agent")
	if sensitiveAuthenticationRoute {
		errorValue = "sensitive_authentication_route_panic"
		userAgent = ""
	}
	fields := []Field{
		{"error", errorValue},
		{"method", getMethod(c)},
		{"path", getPath(c)},
		{"ip", getClientIP(c)},
	}
	if !sensitiveAuthenticationRoute {
		fields = append(fields, Field{"user_agent", userAgent})
	}

	// 添加用户信息
	if userID, exists := c.Get("user_id"); exists {
		fields = append(fields, Field{"user_id", userID})
	}

	// 添加请求ID
	if requestID := requestIDForLogs(c); requestID != "" {
		fields = append(fields, Field{"request_id", requestID})
	}

	// 添加堆栈信息
	if len(stack) > 0 &&
		!config.DisablePrintStack &&
		!sensitiveAuthenticationRoute {
		fields = append(fields, Field{"stack", string(stack)})
	}

	logger.Error("Panic recovered", fields...)
}

// defaultErrorResponse 默认错误响应
func defaultErrorResponse(c HTTPContext, _ interface{}) {
	if responseStarted(c) {
		// HTTP status and body are already committed. Writing another JSON
		// document would corrupt streaming/SSE and other partial responses.
		c.Abort()
		return
	}
	requestID := requestIDForLogs(c)
	errorBody := map[string]interface{}{
		"code":    "internal_error",
		"message": "服务器内部错误，请稍后重试",
	}
	if requestID != "" {
		errorBody["request_id"] = requestID
	}
	c.JSON(500, map[string]interface{}{
		"success": false,
		"error":   errorBody,
	})
	c.Abort()
}
