package middleware

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// LogLevel 日志级别
type LogLevel int

const (
	LogLevelDebug LogLevel = iota
	LogLevelInfo
	LogLevelWarn
	LogLevelError
	LogLevelFatal
)

// String 返回日志级别的字符串表示
func (l LogLevel) String() string {
	switch l {
	case LogLevelDebug:
		return "DEBUG"
	case LogLevelInfo:
		return "INFO"
	case LogLevelWarn:
		return "WARN"
	case LogLevelError:
		return "ERROR"
	case LogLevelFatal:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// Logger 日志接口
type Logger interface {
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)
	Fatal(msg string, fields ...Field)
	With(fields ...Field) Logger
}

// Field 日志字段
type Field struct {
	Key   string
	Value interface{}
}

// SimpleLogger 简单日志实现
type SimpleLogger struct {
	writer io.Writer
	level  LogLevel
	fields []Field
}

// NewSimpleLogger 创建简单日志器
func NewSimpleLogger(writer io.Writer, level LogLevel) *SimpleLogger {
	if writer == nil {
		writer = os.Stdout
	}
	return &SimpleLogger{
		writer: writer,
		level:  level,
		fields: make([]Field, 0),
	}
}

// Debug 记录调试日志
func (l *SimpleLogger) Debug(msg string, fields ...Field) {
	if l.level <= LogLevelDebug {
		l.log(LogLevelDebug, msg, fields...)
	}
}

// Info 记录信息日志
func (l *SimpleLogger) Info(msg string, fields ...Field) {
	if l.level <= LogLevelInfo {
		l.log(LogLevelInfo, msg, fields...)
	}
}

// Warn 记录警告日志
func (l *SimpleLogger) Warn(msg string, fields ...Field) {
	if l.level <= LogLevelWarn {
		l.log(LogLevelWarn, msg, fields...)
	}
}

// Error 记录错误日志
func (l *SimpleLogger) Error(msg string, fields ...Field) {
	if l.level <= LogLevelError {
		l.log(LogLevelError, msg, fields...)
	}
}

// Fatal 记录致命错误日志
func (l *SimpleLogger) Fatal(msg string, fields ...Field) {
	if l.level <= LogLevelFatal {
		l.log(LogLevelFatal, msg, fields...)
	}
}

// With 添加字段
func (l *SimpleLogger) With(fields ...Field) Logger {
	newFields := make([]Field, len(l.fields)+len(fields))
	copy(newFields, l.fields)
	copy(newFields[len(l.fields):], fields)

	return &SimpleLogger{
		writer: l.writer,
		level:  l.level,
		fields: newFields,
	}
}

// log 记录日志
func (l *SimpleLogger) log(level LogLevel, msg string, fields ...Field) {
	timestamp := time.Now().Format("2006-01-02 15:04:05.000")
	allFields := make([]Field, len(l.fields)+len(fields))
	copy(allFields, l.fields)
	copy(allFields[len(l.fields):], fields)

	// 构建日志消息
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("[%s] %s %s", timestamp, level.String(), msg))

	// 添加字段
	if len(allFields) > 0 {
		builder.WriteString(" |")
		for _, field := range allFields {
			builder.WriteString(fmt.Sprintf(" %s=%v", field.Key, field.Value))
		}
	}

	builder.WriteString("\n")
	fmt.Fprint(l.writer, builder.String())
}

// LoggerConfig 日志中间件配置
type LoggerConfig struct {
	// Logger 日志器实例
	Logger Logger
	// SkipPaths 跳过记录的路径
	SkipPaths []string
	// SkipFunc 跳过记录的函数
	SkipFunc func(HTTPContext) bool
	// CustomFields 自定义字段函数
	CustomFields func(HTTPContext) []Field
	// LogLatency 是否记录延迟
	LogLatency bool
	// LogUserAgent 是否记录用户代理
	LogUserAgent bool
	// LogReferer 是否记录引用页
	LogReferer bool
	// LogRequestID 是否记录请求ID
	LogRequestID bool
	// LogBody 是否记录请求体（仅用于调试）
	LogBody bool
	// MaxBodySize 最大记录的请求体大小
	MaxBodySize int
}

// DefaultLoggerConfig 默认日志配置
func DefaultLoggerConfig() *LoggerConfig {
	return &LoggerConfig{
		Logger:       NewSimpleLogger(os.Stdout, LogLevelInfo),
		SkipPaths:    []string{"/health", "/metrics", "/favicon.ico"},
		LogLatency:   true,
		LogUserAgent: false,
		LogReferer:   false,
		LogRequestID: true,
		LogBody:      false,
		MaxBodySize:  1024,
	}
}

// RequestInfo 请求信息
type RequestInfo struct {
	Method     string
	Path       string
	Query      string
	IP         string
	UserAgent  string
	Referer    string
	RequestID  string
	UserID     interface{}
	StartTime  time.Time
	EndTime    time.Time
	Latency    time.Duration
	StatusCode int
	BodySize   int
	Error      string
	Err        error
	ContextErr error
	// ResponseStarted distinguishes a status already committed by a handler
	// from the effective status used to describe a canceled request in logs.
	ResponseStarted bool
	CommittedStatus int
}

// LoggingMiddleware 日志中间件
func LoggingMiddleware(config *LoggerConfig) func(HTTPContext) {
	if config == nil {
		config = DefaultLoggerConfig()
	}

	if config.Logger == nil {
		config.Logger = NewSimpleLogger(os.Stdout, LogLevelInfo)
	}

	return func(c HTTPContext) {
		// 检查是否跳过记录
		if shouldSkip(c, config) {
			c.Next()
			return
		}

		// 记录请求开始
		startTime := time.Now()
		sensitiveAuthenticationRoute :=
			isSensitiveAuthenticationLoggingRoute(c)
		requestInfo := &RequestInfo{
			Method:    getMethod(c),
			Path:      getPath(c),
			IP:        getClientIP(c),
			StartTime: startTime,
		}
		if !sensitiveAuthenticationRoute {
			requestInfo.Query = getQuery(c)
		}

		// 获取可选信息
		if config.LogUserAgent && !sensitiveAuthenticationRoute {
			requestInfo.UserAgent = c.GetHeader("User-Agent")
		}
		if config.LogReferer && !sensitiveAuthenticationRoute {
			requestInfo.Referer = c.GetHeader("Referer")
		}
		if config.LogRequestID {
			requestInfo.RequestID = requestIDForLogs(c)
		}

		// 获取用户信息
		if userID, exists := c.Get("user_id"); exists {
			requestInfo.UserID = userID
		}

		// 记录请求开始日志
		logRequestStart(config.Logger, requestInfo)

		// 处理请求
		c.Next()

		// 记录请求结束
		endTime := time.Now()
		requestInfo.EndTime = endTime
		requestInfo.Latency = endTime.Sub(startTime)
		requestInfo.StatusCode = getStatusCode(c)
		requestInfo.BodySize = getResponseSize(c)
		requestInfo.ResponseStarted = responseStarted(c)
		requestInfo.ContextErr = getRequestContextError(c)
		if requestInfo.ContextErr != nil && requestInfo.StatusCode < httpStatusBadRequest {
			if requestInfo.ResponseStarted {
				requestInfo.CommittedStatus = requestInfo.StatusCode
			}
			requestInfo.StatusCode = requestTerminationStatus(requestInfo.ContextErr)
		}

		// 获取错误信息
		if err := getError(c); err != nil {
			requestInfo.Err = err
			if sensitiveAuthenticationRoute {
				requestInfo.Error = "sensitive_authentication_route_error"
			} else {
				requestInfo.Error = err.Error()
			}
		}

		// 记录请求完成日志
		logRequestEnd(config.Logger, requestInfo, config)
	}
}

// shouldSkip 检查是否应该跳过记录
func shouldSkip(c HTTPContext, config *LoggerConfig) bool {
	if config.SkipFunc != nil && config.SkipFunc(c) {
		return true
	}

	path := getPath(c)
	for _, skipPath := range config.SkipPaths {
		if path == skipPath {
			return true
		}
	}

	return false
}

// logRequestStart 记录请求开始日志
func logRequestStart(logger Logger, info *RequestInfo) {
	fields := []Field{
		{"method", info.Method},
		{"path", info.Path},
		{"ip", info.IP},
	}

	if info.Query != "" {
		fields = append(fields, Field{"query", info.Query})
	}
	if info.RequestID != "" {
		fields = append(fields, Field{"request_id", info.RequestID})
	}
	if info.UserID != nil {
		fields = append(fields, Field{"user_id", info.UserID})
	}
	if info.UserAgent != "" {
		fields = append(fields, Field{"user_agent", info.UserAgent})
	}
	if info.Referer != "" {
		fields = append(fields, Field{"referer", info.Referer})
	}

	logger.Info("Request started", fields...)
}

// logRequestEnd 记录请求结束日志
func logRequestEnd(logger Logger, info *RequestInfo, config *LoggerConfig) {
	fields := []Field{
		{"method", info.Method},
		{"path", info.Path},
		{"status", info.StatusCode},
		{"ip", info.IP},
	}

	if config.LogLatency {
		fields = append(fields, Field{"latency", info.Latency.String()})
		fields = append(fields, Field{"latency_ms", info.Latency.Milliseconds()})
	}

	if info.BodySize > 0 {
		fields = append(fields, Field{"size", info.BodySize})
	}
	if info.RequestID != "" {
		fields = append(fields, Field{"request_id", info.RequestID})
	}
	if info.UserID != nil {
		fields = append(fields, Field{"user_id", info.UserID})
	}

	// 根据状态码选择日志级别
	msg := "Request completed"
	if info.StatusCode >= 500 {
		if info.Error != "" {
			fields = append(fields, Field{"error", info.Error})
			msg = "Request failed"
		}
		logger.Error(msg, fields...)
	} else if requestEndedWithContext(info) {
		fields = append(fields, Field{"termination", requestTerminationReason(info.ContextErr)})
		if info.ResponseStarted {
			fields = append(fields, Field{"response_started", true})
		}
		if info.CommittedStatus != 0 {
			fields = append(fields, Field{"committed_status", info.CommittedStatus})
		}
		logger.Info("Request canceled", fields...)
	} else if info.Error != "" {
		fields = append(fields, Field{"error", info.Error})
		msg = "Request failed"
		logger.Error(msg, fields...)
	} else if info.StatusCode >= 400 {
		logger.Warn(msg, fields...)
	} else {
		logger.Info(msg, fields...)
	}
}

const (
	httpStatusBadRequest          = 400
	httpStatusClientClosedRequest = 499
)

func requestEndedWithContext(info *RequestInfo) bool {
	if info == nil || info.ContextErr == nil {
		return false
	}
	if !errors.Is(info.ContextErr, context.Canceled) &&
		!errors.Is(info.ContextErr, context.DeadlineExceeded) {
		return false
	}
	// A handler error unrelated to the request context remains a real failure,
	// even if the client happened to disconnect at the same time.
	return info.Err == nil || errors.Is(info.Err, info.ContextErr)
}

func requestTerminationStatus(err error) int {
	if errors.Is(err, context.DeadlineExceeded) {
		return 408
	}
	return httpStatusClientClosedRequest
}

func requestTerminationReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	return "client_canceled"
}

// getPath 获取请求路径
func getPath(c HTTPContext) string {
	if ginCtx, ok := c.(*GinHTTPContext); ok && ginCtx.Context.Request != nil {
		return ginCtx.Context.Request.URL.Path
	}
	return "/"
}

// getQuery 获取查询参数
func getQuery(c HTTPContext) string {
	if ginCtx, ok := c.(*GinHTTPContext); ok && ginCtx.Context.Request != nil {
		return sanitizeQueryForLogs(ginCtx.Context.Request.URL.RawQuery)
	}
	return ""
}

// getClientIP 获取客户端IP
func getClientIP(c HTTPContext) string {
	if ginCtx, ok := c.(*GinHTTPContext); ok {
		return ginCtx.Context.ClientIP()
	}

	// 尝试从各种头部获取真实IP
	ip := c.GetHeader("X-Forwarded-For")
	if ip != "" {
		// X-Forwarded-For 可能包含多个IP，取第一个
		if idx := strings.Index(ip, ","); idx != -1 {
			ip = ip[:idx]
		}
		return strings.TrimSpace(ip)
	}

	ip = c.GetHeader("X-Real-IP")
	if ip != "" {
		return ip
	}

	ip = c.GetHeader("X-Forwarded")
	if ip != "" {
		return ip
	}

	ip = c.GetHeader("X-Cluster-Client-IP")
	if ip != "" {
		return ip
	}

	// 如果都没有，返回默认值
	return "unknown"
}

// getRequestID 获取请求ID
func getRequestID(c HTTPContext) string {
	// 尝试从头部获取
	requestID := c.GetHeader("X-Request-ID")
	if requestID != "" {
		return requestID
	}

	// 尝试从上下文获取
	if id, exists := c.Get("request_id"); exists {
		if requestID, ok := id.(string); ok {
			return requestID
		}
	}

	return ""
}

const (
	backupCodeRegenerationLoggingPath = "/api/auth/otp/backup-codes"
	sensitiveAuthenticationLogMarker  = "security-sensitive-request"
)

func isSensitiveAuthenticationLoggingRoute(c HTTPContext) bool {
	return getMethod(c) == "POST" &&
		getPath(c) == backupCodeRegenerationLoggingPath
}

func requestIDForLogs(c HTTPContext) string {
	if isSensitiveAuthenticationLoggingRoute(c) {
		return sensitiveAuthenticationLogMarker
	}
	return getRequestID(c)
}

// getStatusCode 获取响应状态码
func getStatusCode(c HTTPContext) int {
	if ginCtx, ok := c.(*GinHTTPContext); ok {
		return ginCtx.Context.Writer.Status()
	}
	return 200
}

// getResponseSize 获取响应大小
func getResponseSize(c HTTPContext) int {
	if ginCtx, ok := c.(*GinHTTPContext); ok {
		size := ginCtx.Context.Writer.Size()
		if size > 0 {
			return size
		}
	}
	return 0
}

func responseStarted(c HTTPContext) bool {
	if ginCtx, ok := c.(*GinHTTPContext); ok {
		return ginCtx.Context.Writer.Written()
	}
	return false
}

func getRequestContextError(c HTTPContext) error {
	if ginCtx, ok := c.(*GinHTTPContext); ok &&
		ginCtx.Context.Request != nil {
		return ginCtx.Context.Request.Context().Err()
	}
	return nil
}

// getError 获取错误信息
func getError(c HTTPContext) error {
	if ginCtx, ok := c.(*GinHTTPContext); ok && len(ginCtx.Context.Errors) > 0 {
		return ginCtx.Context.Errors.Last()
	}

	// 尝试从上下文获取错误
	if err, exists := c.Get("error"); exists {
		if e, ok := err.(error); ok {
			return e
		}
	}
	return nil
}

// RequestIDMiddleware 请求ID中间件
func RequestIDMiddleware() func(HTTPContext) {
	return func(c HTTPContext) {
		requestID := ""
		if !isSensitiveAuthenticationLoggingRoute(c) {
			requestID = c.GetHeader("X-Request-ID")
		}
		if requestID == "" {
			// 生成新的请求ID
			requestID = generateRequestID()
			setHeader(c, "X-Request-ID", requestID)
		}
		if ginContext, ok := c.(*GinHTTPContext); ok &&
			ginContext.Context.Request != nil {
			ginContext.Context.Request.Header.Set(
				"X-Request-ID",
				requestID,
			)
		}

		// 存储到上下文
		c.Set("request_id", requestID)

		c.Next()
	}
}

// generateRequestID 生成请求ID
func generateRequestID() string {
	// 简单的请求ID生成（实际使用中可能需要更复杂的实现）
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
