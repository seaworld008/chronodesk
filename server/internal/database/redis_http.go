package database

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	maxHTTPRedisResponseBytes    = 64 << 10
	maxHTTPRedisCommandArguments = 10_000
)

// HTTPRedisClient HTTP REST API Redis客户端
type HTTPRedisClient struct {
	baseURL string
	token   string
	client  *http.Client
}

var _ RedisInterface = (*HTTPRedisClient)(nil)

// HTTPRedisResponse REST API响应结构
type HTTPRedisResponse struct {
	Result interface{} `json:"result"`
	Error  string      `json:"error,omitempty"`
}

// NewHTTPRedisClient 创建新的HTTP Redis客户端
func NewHTTPRedisClient() (*HTTPRedisClient, error) {
	baseURL, err := validateHTTPRedisBaseURL(os.Getenv("KV_REST_API_URL"))
	if err != nil {
		return nil, err
	}
	token := os.Getenv("KV_REST_API_TOKEN")

	if token == "" {
		return nil, fmt.Errorf("KV_REST_API_TOKEN not set")
	}

	transport, ok := http.DefaultTransport.(*http.Transport)
	if ok {
		transport = transport.Clone()
	} else {
		transport = &http.Transport{Proxy: http.ProxyFromEnvironment}
	}
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
		if transport.TLSClientConfig.MinVersion < tls.VersionTLS12 {
			transport.TLSClientConfig.MinVersion = tls.VersionTLS12
		}
	}
	return &HTTPRedisClient{
		baseURL: baseURL,
		token:   token,
		client: &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func validateHTTPRedisBaseURL(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" {
		return "", fmt.Errorf("KV_REST_API_URL must be an absolute URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("KV_REST_API_URL must not contain user info, query, or fragment")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isHTTPRedisLoopback(parsed.Hostname())) {
		return "", fmt.Errorf("KV_REST_API_URL must use HTTPS except for loopback development")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func isHTTPRedisLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Ping 测试连接
func (c *HTTPRedisClient) Ping(ctx context.Context) error {
	_, err := c.executeCommand(ctx, "PING")
	return err
}

// Set 设置键值
func (c *HTTPRedisClient) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	args := []interface{}{key, value}
	if expiration > 0 {
		args = append(args, "EX", int64(expiration/time.Second))
	}

	_, err := c.executeCommand(ctx, "SET", args...)
	return err
}

// Get 获取值
func (c *HTTPRedisClient) Get(ctx context.Context, key string) (string, error) {
	resp, err := c.executeCommand(ctx, "GET", key)
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
	if len(keys) == 0 {
		return nil
	}

	args := make([]interface{}, len(keys))
	for i, key := range keys {
		args[i] = key
	}
	_, err := c.executeCommand(ctx, "DEL", args...)
	return err
}

// Exists 检查键是否存在
func (c *HTTPRedisClient) Exists(ctx context.Context, keys ...string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}

	args := make([]interface{}, len(keys))
	for i, key := range keys {
		args[i] = key
	}
	resp, err := c.executeCommand(ctx, "EXISTS", args...)
	if err != nil {
		return 0, err
	}
	if result, ok := resp.Result.(float64); ok {
		return int64(result), nil
	}
	return 0, fmt.Errorf("invalid EXISTS response")
}

// Expire 设置过期时间
func (c *HTTPRedisClient) Expire(ctx context.Context, key string, expiration time.Duration) error {
	_, err := c.executeCommand(ctx, "EXPIRE", key, int64(expiration/time.Second))
	return err
}

// TTL 获取剩余生存时间
func (c *HTTPRedisClient) TTL(ctx context.Context, key string) (time.Duration, error) {
	resp, err := c.executeCommand(ctx, "TTL", key)
	if err != nil {
		return 0, err
	}

	if seconds, ok := resp.Result.(float64); ok {
		return time.Duration(seconds) * time.Second, nil
	}

	return 0, fmt.Errorf("invalid TTL response")
}

// Eval executes one Lua script atomically. Upstash's REST API follows the
// native Redis argument order: EVAL script numkeys key... arg....
func (c *HTTPRedisClient) Eval(
	ctx context.Context,
	script string,
	keys []string,
	args ...interface{},
) (interface{}, error) {
	if len(keys) > maxHTTPRedisCommandArguments-2 ||
		len(args) > maxHTTPRedisCommandArguments-2-len(keys) {
		return nil, fmt.Errorf(
			"Redis EVAL exceeds the %d argument limit",
			maxHTTPRedisCommandArguments,
		)
	}
	commandArgs := []interface{}{script, len(keys)}
	for _, key := range keys {
		commandArgs = append(commandArgs, key)
	}
	commandArgs = append(commandArgs, args...)
	response, err := c.executeCommand(ctx, "EVAL", commandArgs...)
	if err != nil {
		return nil, err
	}
	return response.Result, nil
}

// Close 关闭客户端
func (c *HTTPRedisClient) Close() error {
	if c.client != nil {
		c.client.CloseIdleConnections()
	}
	return nil
}

// executeCommand uses Upstash's canonical JSON command-array transport. Keeping
// all command arguments in the body avoids path escaping bugs for arbitrary keys.
func (c *HTTPRedisClient) executeCommand(ctx context.Context, command string, args ...interface{}) (*HTTPRedisResponse, error) {
	if len(args) > maxHTTPRedisCommandArguments-1 {
		return nil, fmt.Errorf(
			"Redis command exceeds the %d argument limit",
			maxHTTPRedisCommandArguments,
		)
	}
	body := []interface{}{command}
	body = append(body, args...)
	return c.makeRequest(ctx, http.MethodPost, "", body)
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

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxHTTPRedisResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	if len(respBody) > maxHTTPRedisResponseBytes {
		return nil, fmt.Errorf("HTTP Redis response exceeded %d bytes", maxHTTPRedisResponseBytes)
	}

	if resp.StatusCode != http.StatusOK {
		// 不把不可信的上游响应体拼进日志或 API 错误。
		return nil, fmt.Errorf("HTTP Redis request failed with status %d", resp.StatusCode)
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
