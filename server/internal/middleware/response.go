package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// StandardResponse 统一的API响应格式
type StandardResponse struct {
	Code int         `json:"code"`
	Msg  string      `json:"msg"`
	Data interface{} `json:"data,omitempty"`
}

// ResponseHelper 响应助手
type ResponseHelper struct{}

// NewResponseHelper 创建响应助手
func NewResponseHelper() *ResponseHelper {
	return &ResponseHelper{}
}

// Success 成功响应
func (r *ResponseHelper) Success(c *gin.Context, data interface{}, message ...string) {
	msg := "操作成功"
	if len(message) > 0 {
		msg = message[0]
	}

	c.JSON(http.StatusOK, StandardResponse{
		Code: 0,
		Msg:  msg,
		Data: data,
	})
}

// Error 错误响应
func (r *ResponseHelper) Error(c *gin.Context, statusCode int, message string, data ...interface{}) {
	var responseData interface{}
	if len(data) > 0 {
		responseData = data[0]
	}

	c.JSON(statusCode, StandardResponse{
		Code: statusCode,
		Msg:  message,
		Data: responseData,
	})
}

// BadRequest 400错误
func (r *ResponseHelper) BadRequest(c *gin.Context, message string, data ...interface{}) {
	r.Error(c, http.StatusBadRequest, message, data...)
}

// Unauthorized 401错误
func (r *ResponseHelper) Unauthorized(c *gin.Context, message string, data ...interface{}) {
	r.Error(c, http.StatusUnauthorized, message, data...)
}

// Forbidden 403错误
func (r *ResponseHelper) Forbidden(c *gin.Context, message string, data ...interface{}) {
	r.Error(c, http.StatusForbidden, message, data...)
}

// NotFound 404错误
func (r *ResponseHelper) NotFound(c *gin.Context, message string, data ...interface{}) {
	r.Error(c, http.StatusNotFound, message, data...)
}

// InternalServerError 500错误
func (r *ResponseHelper) InternalServerError(c *gin.Context, message string, data ...interface{}) {
	r.Error(c, http.StatusInternalServerError, message, data...)
}

// Created 201响应
func (r *ResponseHelper) Created(c *gin.Context, data interface{}, message ...string) {
	msg := "创建成功"
	if len(message) > 0 {
		msg = message[0]
	}

	c.JSON(http.StatusCreated, StandardResponse{
		Code: 0,
		Msg:  msg,
		Data: data,
	})
}

// List 列表响应
func (r *ResponseHelper) List(c *gin.Context, data interface{}, total int64, page, pageSize int, message ...string) {
	msg := "获取成功"
	if len(message) > 0 {
		msg = message[0]
	}

	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))

	listData := map[string]interface{}{
		"items":       data,
		"total":       total,
		"page":        page,
		"page_size":   pageSize,
		"total_pages": totalPages,
	}

	c.JSON(http.StatusOK, StandardResponse{
		Code: 0,
		Msg:  msg,
		Data: listData,
	})
}

// GlobalResponseHelper 全局响应助手实例
var GlobalResponseHelper = NewResponseHelper()
