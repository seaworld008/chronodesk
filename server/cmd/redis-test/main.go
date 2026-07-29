package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/logging"
)

const (
	probeTimeout          = 10 * time.Second
	maxProbeResponseBytes = 4 << 10
)

type probeResult struct {
	transport  string
	endpointID string
	ok         bool
	category   string
}

func main() {
	// 本地开发可从 .env 加载；生产环境继续以进程环境变量为准。
	_ = godotenv.Load()
	// 门禁只输出稳定错误分类，避免依赖库日志暴露端点细节。
	logging.Disable()
	os.Exit(runRedisGate(context.Background(), os.Stdout))
}

func runRedisGate(parent context.Context, output io.Writer) int {
	results := make([]probeResult, 0, 2)

	if redisURL := strings.TrimSpace(os.Getenv("REDIS_URL")); redisURL != "" {
		results = append(results, probeRedisTCP(parent, redisURL))
	}

	if restURL := strings.TrimSpace(os.Getenv("KV_REST_API_URL")); restURL != "" {
		token := strings.TrimSpace(os.Getenv("KV_REST_API_READ_ONLY_TOKEN"))
		if token == "" {
			token = strings.TrimSpace(os.Getenv("KV_REST_API_TOKEN"))
		}
		if token == "" {
			results = append(results, probeResult{
				transport:  "REST/HTTPS",
				endpointID: endpointFingerprint(restURL),
				category:   "missing_credentials",
			})
		} else {
			results = append(results, probeRedisREST(parent, restURL, token, newProbeHTTPClient()))
		}
	}

	fmt.Fprintln(output, "Redis 发布门禁（只读 PING）")
	if len(results) == 0 {
		fmt.Fprintln(output, "- 未配置 REDIS_URL 或 KV_REST_API_URL")
		fmt.Fprintln(output, "结论：失败（missing_configuration）")
		return 2
	}

	successes := 0
	for _, result := range results {
		state := "失败"
		if result.ok {
			state = "成功"
			successes++
		}
		fmt.Fprintf(
			output,
			"- %s [端点 %s]：%s（%s）\n",
			result.transport,
			result.endpointID,
			state,
			result.category,
		)
	}

	if successes == 0 {
		fmt.Fprintf(output, "结论：失败（0/%d 个传输可用）\n", len(results))
		return 1
	}
	if successes < len(results) {
		fmt.Fprintf(output, "结论：可用但存在降级（%d/%d 个传输可用）\n", successes, len(results))
		return 0
	}
	fmt.Fprintf(output, "结论：通过（%d/%d 个传输可用）\n", successes, len(results))
	return 0
}

func probeRedisTCP(parent context.Context, redisURL string) probeResult {
	result := probeResult{
		transport:  "TCP/RESP",
		endpointID: endpointFingerprint(redisURL),
	}

	parsed, err := url.Parse(redisURL)
	if err != nil || parsed.Hostname() == "" {
		result.category = "invalid_url"
		return result
	}
	if parsed.Scheme == "redis" && !isLoopbackHost(parsed.Hostname()) {
		result.category = "insecure_transport"
		return result
	}

	options, err := redis.ParseURL(redisURL)
	if err != nil {
		result.category = "invalid_url"
		return result
	}

	client := redis.NewClient(options)
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(parent, probeTimeout)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		result.category = classifyConnectivityError(err)
		return result
	}

	result.ok = true
	result.category = "pong"
	return result
}

func probeRedisREST(parent context.Context, rawURL, token string, client *http.Client) probeResult {
	result := probeResult{
		transport:  "REST/HTTPS",
		endpointID: endpointFingerprint(rawURL),
	}

	endpoint, err := validateRESTEndpoint(rawURL)
	if err != nil {
		result.category = err.Error()
		return result
	}

	payload, err := json.Marshal([]string{"PING"})
	if err != nil {
		result.category = "request_encoding_failed"
		return result
	}

	ctx, cancel := context.WithTimeout(parent, probeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		result.category = "request_creation_failed"
		return result
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")

	response, err := client.Do(request)
	if err != nil {
		result.category = classifyConnectivityError(err)
		return result
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxProbeResponseBytes+1))
	if err != nil {
		result.category = "response_read_failed"
		return result
	}
	if len(body) > maxProbeResponseBytes {
		result.category = "response_too_large"
		return result
	}
	if response.StatusCode != http.StatusOK {
		result.category = fmt.Sprintf("http_status_%d", response.StatusCode)
		return result
	}

	var envelope struct {
		Result string `json:"result"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		result.category = "invalid_response"
		return result
	}
	if envelope.Error != "" {
		result.category = "redis_error"
		return result
	}
	if !strings.EqualFold(envelope.Result, "PONG") {
		result.category = "unexpected_response"
		return result
	}

	result.ok = true
	result.category = "pong"
	return result
}

func newProbeHTTPClient() *http.Client {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if ok {
		transport = transport.Clone()
	} else {
		transport = &http.Transport{Proxy: http.ProxyFromEnvironment}
	}
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	return &http.Client{
		Transport: transport,
		Timeout:   probeTimeout,
		// 禁止重定向，避免 Authorization 被转发到非预期端点。
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func validateRESTEndpoint(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" {
		return "", errors.New("invalid_url")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("invalid_url")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname())) {
		return "", errors.New("insecure_transport")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func endpointFingerprint(rawEndpoint string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawEndpoint))
	if err != nil || parsed.Hostname() == "" {
		return "unknown"
	}
	digest := sha256.Sum256([]byte(strings.ToLower(parsed.Hostname())))
	return fmt.Sprintf("sha256:%x", digest[:6])
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func classifyConnectivityError(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return "timeout"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "connection_eof"
	case errors.Is(err, syscall.ECONNRESET):
		return "connection_reset"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection_refused"
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
		return "network_unreachable"
	}

	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return "dns_lookup_failed"
	}
	var hostnameError x509.HostnameError
	if errors.As(err, &hostnameError) {
		return "tls_hostname_mismatch"
	}
	var authorityError x509.UnknownAuthorityError
	if errors.As(err, &authorityError) {
		return "tls_untrusted_certificate"
	}
	var recordError tls.RecordHeaderError
	if errors.As(err, &recordError) {
		return "tls_handshake_failed"
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "timeout"
	}
	return "transport_error"
}
