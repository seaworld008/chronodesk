package auth

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
)

// GinHTTPContext Gin框架的HTTPContext适配器
type GinHTTPContext struct {
	ginCtx *gin.Context
}

// NewGinHTTPContext 创建Gin适配器
func NewGinHTTPContext(c *gin.Context) HTTPContext {
	return &GinHTTPContext{ginCtx: c}
}

// GetHeader 获取请求头
func (g *GinHTTPContext) GetHeader(key string) string {
	return g.ginCtx.GetHeader(key)
}

// SetHeader 设置响应头
func (g *GinHTTPContext) SetHeader(key, value string) {
	g.ginCtx.Header(key, value)
}

// GetQuery 获取查询参数
func (g *GinHTTPContext) GetQuery(key string) string {
	return g.ginCtx.Query(key)
}

// GetParam 获取路径参数
func (g *GinHTTPContext) GetParam(key string) string {
	return g.ginCtx.Param(key)
}

// Bind 绑定请求体到结构体
func (g *GinHTTPContext) Bind(obj interface{}) error {
	return g.ginCtx.ShouldBindJSON(obj)
}

// JSON 返回JSON响应
func (g *GinHTTPContext) JSON(code int, obj interface{}) {
	g.ginCtx.JSON(code, obj)
}

// String 返回字符串响应
func (g *GinHTTPContext) String(code int, format string, values ...interface{}) {
	g.ginCtx.String(code, format, values...)
}

// Abort 中止请求处理
func (g *GinHTTPContext) Abort() {
	g.ginCtx.Abort()
}

// Next 继续处理下一个中间件
func (g *GinHTTPContext) Next() {
	g.ginCtx.Next()
}

// Set 设置上下文值
func (g *GinHTTPContext) Set(key string, value interface{}) {
	g.ginCtx.Set(key, value)
}

// Get 获取上下文值
func (g *GinHTTPContext) Get(key string) (interface{}, bool) {
	return g.ginCtx.Get(key)
}

// ClientIP 获取客户端IP
func (g *GinHTTPContext) ClientIP() string {
	return g.ginCtx.ClientIP()
}

// UserAgent 获取用户代理
func (g *GinHTTPContext) UserAgent() string {
	return g.ginCtx.GetHeader("User-Agent")
}

// Request 获取HTTP请求
func (g *GinHTTPContext) Request() *http.Request {
	return g.ginCtx.Request
}

// GetRequest 获取原始HTTP请求
func (g *GinHTTPContext) GetRequest() *http.Request {
	return g.ginCtx.Request
}

// GetResponseWriter 获取响应写入器
func (g *GinHTTPContext) GetResponseWriter() http.ResponseWriter {
	return g.ginCtx.Writer
}

// Status 设置响应状态码
func (g *GinHTTPContext) Status(code int) {
	g.ginCtx.Status(code)
}

// Error 返回错误响应
func (g *GinHTTPContext) Error(code int, message string) {
	g.ginCtx.JSON(code, gin.H{"error": message})
}

// Success 返回成功响应
func (g *GinHTTPContext) Success(data interface{}) {
	g.ginCtx.JSON(http.StatusOK, gin.H{"data": data})
}

// BindJSON 绑定JSON请求体
func (g *GinHTTPContext) BindJSON(obj interface{}) error {
	return g.ginCtx.ShouldBindJSON(obj)
}

// GetBody 获取请求体
func (g *GinHTTPContext) GetBody() ([]byte, error) {
	return g.ginCtx.GetRawData()
}

// ParseJSON 解析JSON请求体
func (g *GinHTTPContext) ParseJSON(obj interface{}) error {
	body, err := g.GetBody()
	if err != nil {
		return err
	}
	return json.Unmarshal(body, obj)
}
