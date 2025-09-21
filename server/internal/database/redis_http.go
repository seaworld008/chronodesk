package database

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// HTTPRedisClient HTTP REST API Redis客户端
type HTTPRedisClient struct {
	baseURL string
	token   string
	client  *http.Client
}

// HTTPRedisResponse REST API响应结构
type HTTPRedisResponse struct {
	Result interface{} `json:"result"`
	Error  string      `json:"error,omitempty"`
}

// NewHTTPRedisClient 创建新的HTTP Redis客户端
func NewHTTPRedisClient() (*HTTPRedisClient, error) {
	baseURL := os.Getenv("KV_REST_API_URL")
	token := os.Getenv("KV_REST_API_TOKEN")

	if baseURL == "" || token == "" {
		return nil, fmt.Errorf("KV_REST_API_URL or KV_REST_API_TOKEN not set")
	}

	return &HTTPRedisClient{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		token:   token,
		client:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// Ping 测试连接
func (c *HTTPRedisClient) Ping(ctx context.Context) error {
	_, err := c.makeRequest(ctx, "GET", "/ping", nil)
	return err
}

// Set 设置键值
func (c *HTTPRedisClient) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	url := fmt.Sprintf("/set/%s", key)

	// 构建请求体
	reqBody := []interface{}{key, value}
	if expiration > 0 {
		reqBody = append(reqBody, "EX", int(expiration.Seconds()))
	}

	_, err := c.makeRequest(ctx, "POST", url, reqBody)
	return err
}

// Get 获取值
func (c *HTTPRedisClient) Get(ctx context.Context, key string) (string, error) {
	url := fmt.Sprintf("/get/%s", key)
	resp, err := c.makeRequest(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	if resp.Result == nil {
		return "", fmt.Errorf("key not found")
	}

	if str, ok := resp.Result.(string); ok {
		return str, nil
	}

	return fmt.Sprintf("%v", resp.Result), nil
}

// Del 删除键
func (c *HTTPRedisClient) Del(ctx context.Context, keys ...string) error {
	for _, key := range keys {
		url := fmt.Sprintf("/del/%s", key)
		_, err := c.makeRequest(ctx, "GET", url, nil)
		if err != nil {
			return err
		}
	}
	return nil
}

// Exists 检查键是否存在
func (c *HTTPRedisClient) Exists(ctx context.Context, keys ...string) (int64, error) {
	var count int64
	for _, key := range keys {
		url := fmt.Sprintf("/exists/%s", key)
		resp, err := c.makeRequest(ctx, "GET", url, nil)
		if err != nil {
			return 0, err
		}

		if result, ok := resp.Result.(float64); ok && result == 1 {
			count++
		}
	}
	return count, nil
}

// Expire 设置过期时间
func (c *HTTPRedisClient) Expire(ctx context.Context, key string, expiration time.Duration) error {
	url := fmt.Sprintf("/expire/%s/%d", key, int(expiration.Seconds()))
	_, err := c.makeRequest(ctx, "GET", url, nil)
	return err
}

// TTL 获取剩余生存时间
func (c *HTTPRedisClient) TTL(ctx context.Context, key string) (time.Duration, error) {
	url := fmt.Sprintf("/ttl/%s", key)
	resp, err := c.makeRequest(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}

	if seconds, ok := resp.Result.(float64); ok {
		return time.Duration(seconds) * time.Second, nil
	}

	return 0, fmt.Errorf("invalid TTL response")
}

// Close 关闭客户端
func (c *HTTPRedisClient) Close() error {
	// HTTP客户端不需要显式关闭
	return nil
}

// makeRequest 发送HTTP请求
func (c *HTTPRedisClient) makeRequest(ctx context.Context, method, path string, body interface{}) (*HTTPRedisResponse, error) {
	url := c.baseURL + path

	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var result HTTPRedisResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if result.Error != "" {
		return nil, fmt.Errorf("Redis error: %s", result.Error)
	}

	return &result, nil
}
