package middleware

import (
	"fmt"
	"runtime"
	"time"
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
	fields := []Field{
		{"error", fmt.Sprintf("%v", err)},
		{"method", getMethod(c)},
		{"path", getPath(c)},
		{"ip", getClientIP(c)},
		{"user_agent", c.GetHeader("User-Agent")},
	}

	// 添加用户信息
	if userID, exists := c.Get("user_id"); exists {
		fields = append(fields, Field{"user_id", userID})
	}

	// 添加请求ID
	if requestID := getRequestID(c); requestID != "" {
		fields = append(fields, Field{"request_id", requestID})
	}

	// 添加堆栈信息
	if len(stack) > 0 && !config.DisablePrintStack {
		fields = append(fields, Field{"stack", string(stack)})
	}

	logger.Error("Panic recovered", fields...)
}

// defaultErrorResponse 默认错误响应
func defaultErrorResponse(c HTTPContext, err interface{}) {
	// 设置状态码
	setStatus(c, 500)

	// 设置响应头
	setHeader(c, "Content-Type", "application/json")

	// 构建错误响应
	errorResponse := map[string]interface{}{
		"error":     "Internal Server Error",
		"code":      500,
		"timestamp": time.Now().Unix(),
	}

	// 在开发环境下可以返回详细错误信息
	if isDevelopment() {
		errorResponse["detail"] = fmt.Sprintf("%v", err)
	}

	// 发送JSON响应
	sendJSON(c, errorResponse)
}

// isDevelopment 检查是否为开发环境
func isDevelopment() bool {
	// 这里可以根据环境变量或配置来判断
	// 简单实现，实际使用中应该从配置中读取
	return true
}

// sendJSON 发送JSON响应
func sendJSON(c HTTPContext, data interface{}) {
	// 这里需要根据具体的HTTP框架实现JSON序列化
	// 简单实现
	response := fmt.Sprintf(`{"error":"%v"}`, data)
	// 使用 SetHeader 和其他方法来发送响应
	// 实际实现需要根据具体的HTTP框架来调整
	_ = response // 避免未使用变量警告
}

// ErrorRecoveryMiddleware 错误恢复中间件（简化版）
func ErrorRecoveryMiddleware(logger Logger) func(HTTPContext) {
	return RecoveryMiddleware(&RecoveryConfig{
		Logger:           logger,
		EnableStackTrace: true,
	})
}

// ProductionRecoveryMiddleware 生产环境恢复中间件
func ProductionRecoveryMiddleware() func(HTTPContext) {
	return RecoveryMiddleware(&RecoveryConfig{
		Logger:            NewSimpleLogger(nil, LogLevelError),
		EnableStackTrace:  false,
		DisablePrintStack: true,
		RecoveryHandler: func(c HTTPContext, err interface{}) {
			// 生产环境下不暴露详细错误信息
			setStatus(c, 500)
			setHeader(c, "Content-Type", "application/json")
			errorResponse := map[string]interface{}{
				"error": "Internal Server Error",
				"code":  500,
			}
			sendJSON(c, errorResponse)
		},
	})
}

// DevelopmentRecoveryMiddleware 开发环境恢复中间件
func DevelopmentRecoveryMiddleware() func(HTTPContext) {
	return RecoveryMiddleware(&RecoveryConfig{
		Logger:           NewSimpleLogger(nil, LogLevelDebug),
		EnableStackTrace: true,
		StackSize:        8 << 10, // 8KB
		RecoveryHandler: func(c HTTPContext, err interface{}) {
			// 开发环境下返回详细错误信息
			setStatus(c, 500)
			setHeader(c, "Content-Type", "application/json")
			errorResponse := map[string]interface{}{
				"error":     "Internal Server Error",
				"code":      500,
				"detail":    fmt.Sprintf("%v", err),
				"timestamp": time.Now().Unix(),
			}
			sendJSON(c, errorResponse)
		},
	})
}

// CustomRecoveryMiddleware 自定义恢复中间件
func CustomRecoveryMiddleware(handler func(HTTPContext, interface{})) func(HTTPContext) {
	return RecoveryMiddleware(&RecoveryConfig{
		Logger:          NewSimpleLogger(nil, LogLevelError),
		RecoveryHandler: handler,
	})
}

// PanicInfo panic信息结构
type PanicInfo struct {
	Error     interface{}
	Stack     string
	Method    string
	Path      string
	IP        string
	UserAgent string
	UserID    interface{}
	RequestID string
	Timestamp time.Time
}

// GetPanicInfo 获取panic信息
func GetPanicInfo(c HTTPContext, err interface{}, stack []byte) *PanicInfo {
	return &PanicInfo{
		Error:     err,
		Stack:     string(stack),
		Method:    getMethod(c),
		Path:      getPath(c),
		IP:        getClientIP(c),
		UserAgent: c.GetHeader("User-Agent"),
		UserID:    getUserID(c),
		RequestID: getRequestID(c),
		Timestamp: time.Now(),
	}
}

// getUserID 获取用户ID
func getUserID(c HTTPContext) interface{} {
	if userID, exists := c.Get("user_id"); exists {
		return userID
	}
	return nil
}

// ErrorResponse 错误响应结构
type ErrorResponse struct {
	Error     string      `json:"error"`
	Code      int         `json:"code"`
	Detail    interface{} `json:"detail,omitempty"`
	Timestamp int64       `json:"timestamp"`
	RequestID string      `json:"request_id,omitempty"`
}

// NewErrorResponse 创建错误响应
func NewErrorResponse(code int, message string, detail interface{}) *ErrorResponse {
	return &ErrorResponse{
		Error:     message,
		Code:      code,
		Detail:    detail,
		Timestamp: time.Now().Unix(),
	}
}

// WithRequestID 添加请求ID
func (e *ErrorResponse) WithRequestID(requestID string) *ErrorResponse {
	e.RequestID = requestID
	return e
}

// SafeRecoveryMiddleware 安全恢复中间件（不暴露任何内部信息）
func SafeRecoveryMiddleware() func(HTTPContext) {
	return RecoveryMiddleware(&RecoveryConfig{
		Logger:            NewSimpleLogger(nil, LogLevelError),
		EnableStackTrace:  true,
		DisablePrintStack: false, // 仍然记录到日志
		RecoveryHandler: func(c HTTPContext, err interface{}) {
			// 只返回通用错误信息
			setStatus(c, 500)
			setHeader(c, "Content-Type", "application/json")
			errorResponse := NewErrorResponse(500, "Internal Server Error", nil)
			if requestID := getRequestID(c); requestID != "" {
				errorResponse.WithRequestID(requestID)
			}
			sendJSON(c, errorResponse)
		},
	})
}

// RecoveryWithMetrics 带指标的恢复中间件
func RecoveryWithMetrics(metricsHandler func(string, string)) func(HTTPContext) {
	return RecoveryMiddleware(&RecoveryConfig{
		Logger:           NewSimpleLogger(nil, LogLevelError),
		EnableStackTrace: true,
		RecoveryHandler: func(c HTTPContext, err interface{}) {
			// 记录指标
			if metricsHandler != nil {
				metricsHandler(getMethod(c), getPath(c))
			}

			// 默认错误响应
			defaultErrorResponse(c, err)
		},
	})
}
