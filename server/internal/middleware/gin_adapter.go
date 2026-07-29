package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// GinHTTPContext Gin框架的HTTPContext适配器
type GinHTTPContext struct {
	*gin.Context
}

// NewGinHTTPContext 创建Gin适配器
func NewGinHTTPContext(c *gin.Context) HTTPContext {
	return &GinHTTPContext{Context: c}
}

// GetHeader 获取请求头
func (g *GinHTTPContext) GetHeader(key string) string {
	return g.Context.GetHeader(key)
}

// SetHeader 设置响应头
func (g *GinHTTPContext) SetHeader(key, value string) {
	g.Context.Header(key, value)
}

// GetQuery 获取查询参数
func (g *GinHTTPContext) GetQuery(key string) string {
	return g.Context.Query(key)
}

// GetParam 获取路径参数
func (g *GinHTTPContext) GetParam(key string) string {
	return g.Context.Param(key)
}

// Bind 绑定请求数据
func (g *GinHTTPContext) Bind(obj interface{}) error {
	return g.Context.ShouldBind(obj)
}

// JSON 返回JSON响应
func (g *GinHTTPContext) JSON(code int, obj interface{}) {
	g.Context.JSON(code, obj)
}

// String 返回字符串响应
func (g *GinHTTPContext) String(code int, format string, values ...interface{}) {
	g.Context.String(code, format, values...)
}

// Status 设置状态码
func (g *GinHTTPContext) Status(code int) {
	g.Context.Status(code)
}

// Get 获取上下文值
func (g *GinHTTPContext) Get(key string) (interface{}, bool) {
	return g.Context.Get(key)
}

// Set 设置上下文值
func (g *GinHTTPContext) Set(key string, value interface{}) {
	g.Context.Set(key, value)
}

// ClientIP 获取客户端IP
func (g *GinHTTPContext) ClientIP() string {
	return g.Context.ClientIP()
}

// UserAgent 获取用户代理
func (g *GinHTTPContext) UserAgent() string {
	return g.Context.GetHeader("User-Agent")
}

// Request 获取原始请求
func (g *GinHTTPContext) Request() *http.Request {
	return g.Context.Request
}

// Next 调用下一个中间件
func (g *GinHTTPContext) Next() {
	g.Context.Next()
}

// Abort 中止请求处理
func (g *GinHTTPContext) Abort() {
	g.Context.Abort()
}

// AbortWithStatus 中止请求并设置状态码
func (g *GinHTTPContext) AbortWithStatus(code int) {
	g.Context.AbortWithStatus(code)
}

// AbortWithStatusJSON 中止请求并返回JSON
func (g *GinHTTPContext) AbortWithStatusJSON(code int, obj interface{}) {
	g.Context.AbortWithStatusJSON(code, obj)
}

// WrapGinMiddleware 将HTTPContext中间件转换为Gin中间件
func WrapGinMiddleware(middleware func(HTTPContext)) gin.HandlerFunc {
	return func(c *gin.Context) {
		httpCtx := NewGinHTTPContext(c)
		middleware(httpCtx)
	}
}

// WrapGinMiddlewares 批量转换中间件
func WrapGinMiddlewares(middlewares []func(HTTPContext)) []gin.HandlerFunc {
	ginMiddlewares := make([]gin.HandlerFunc, len(middlewares))
	for i, middleware := range middlewares {
		ginMiddlewares[i] = WrapGinMiddleware(middleware)
	}
	return ginMiddlewares
}

// 辅助函数实现

// setHeader 设置响应头
func setHeader(c HTTPContext, key, value string) {
	if ginCtx, ok := c.(*GinHTTPContext); ok {
		ginCtx.Context.Header(key, value)
	}
}

// getMethod 获取请求方法
func getMethod(c HTTPContext) string {
	if ginCtx, ok := c.(*GinHTTPContext); ok {
		return ginCtx.Context.Request.Method
	}
	return "GET"
}

// setStatus 设置响应状态码
func setStatus(c HTTPContext, code int) {
	if ginCtx, ok := c.(*GinHTTPContext); ok {
		ginCtx.Context.Status(code)
	}
}
