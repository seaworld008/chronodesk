package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	defaultTimeout        = 10 * time.Second
	maxBodyBytes          = 10 << 20
	maxJSONResponseBytes  = 2 << 20
	maxOAuthResponseBytes = 1 << 20
)

var (
	projectKeyPattern  = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,15}$`)
	environmentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	uuidPattern        = regexp.MustCompile(
		`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
	)
	externalTypePattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)
)

type commandError struct {
	message string
}

func (err commandError) Error() string {
	return err.message
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	var err error
	switch args[0] {
	case "health":
		err = runHealth(args[1:], stdout, stderr)
	case "oauth":
		if len(args) < 2 || args[1] != "client-credentials" {
			err = commandError{message: "用法: chronodeskctl oauth client-credentials [选项]"}
		} else {
			err = runOAuthClientCredentials(args[2:], stdout, stderr)
		}
	case "project":
		if len(args) < 2 {
			err = commandError{message: "用法: chronodeskctl project <capabilities|connections> [选项]"}
			break
		}
		switch args[1] {
		case "capabilities":
			err = runProjectCapabilities(args[2:], stdout, stderr)
		case "connections":
			err = runProjectConnections(args[2:], stdout, stderr)
		default:
			err = commandError{message: "用法: chronodeskctl project <capabilities|connections> [选项]"}
		}
	case "webhook":
		if len(args) < 2 {
			err = commandError{message: "用法: chronodeskctl webhook <dry-run|verify> [选项]"}
			break
		}
		switch args[1] {
		case "dry-run":
			err = runWebhookDryRun(args[2:], stdin, stdout, stderr)
		case "verify":
			err = runWebhookVerify(args[2:], stdin, stdout, stderr)
		default:
			err = commandError{message: "用法: chronodeskctl webhook <dry-run|verify> [选项]"}
		}
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		err = commandError{message: fmt.Sprintf("未知命令 %q", args[0])}
	}

	if err == nil {
		return 0
	}
	fmt.Fprintf(stderr, "错误: %s\n", err)
	var diagnostic *diagnosticError
	if errors.As(err, &diagnostic) && diagnostic.Unhealthy {
		return 1
	}
	return 2
}

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, `chronodeskctl - ChronoDesk 项目与集成诊断工具

用法:
  chronodeskctl health [选项]
  chronodeskctl oauth client-credentials [选项]
  chronodeskctl project capabilities [选项]
  chronodeskctl project connections [选项]
  chronodeskctl webhook dry-run [选项]
  chronodeskctl webhook verify [选项]

安全约束:
  客户端密钥和 Webhook 密钥只能通过指定环境变量读取，不接受明文命令行参数。
  OAuth Token 默认只输出不可逆摘要；使用 --token-output 写入权限为 0600 的新文件。`)
}

type diagnosticError struct {
	Message   string
	Unhealthy bool
}

func (err *diagnosticError) Error() string {
	return err.Message
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	return flags
}

func requireNoArguments(flags *flag.FlagSet) error {
	if flags.NArg() != 0 {
		return commandError{message: "不接受位置参数"}
	}
	return nil
}

func parseBaseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" ||
		parsed.User != nil ||
		(parsed.EscapedPath() != "" && parsed.EscapedPath() != "/") ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return nil, commandError{message: "base-url 必须是无路径、凭据、查询参数和片段的 http(s) 根地址"}
	}
	if parsed.Scheme == "http" && !loopbackHostname(parsed.Hostname()) {
		return nil, commandError{message: "非回环地址必须使用 HTTPS"}
	}
	parsed.Path = ""
	parsed.RawPath = ""
	return parsed, nil
}

func loopbackHostname(hostname string) bool {
	hostname = strings.TrimSuffix(strings.ToLower(hostname), ".")
	if hostname == "localhost" {
		return true
	}
	address := net.ParseIP(hostname)
	return address != nil && address.IsLoopback()
}

func endpoint(base *url.URL, path string) string {
	copyURL := *base
	copyURL.Path = strings.TrimRight(base.Path, "/") + "/" + strings.TrimLeft(path, "/")
	copyURL.RawPath = ""
	return copyURL.String()
}

func validateProjectKey(value string) error {
	if !projectKeyPattern.MatchString(value) {
		return commandError{message: "project-key 必须匹配 ^[A-Z][A-Z0-9]{1,15}$"}
	}
	return nil
}

func readEnvironmentSecret(name, purpose string) ([]byte, error) {
	if !environmentPattern.MatchString(name) {
		return nil, commandError{message: purpose + "环境变量名无效"}
	}
	value, present := os.LookupEnv(name)
	if !present || value == "" {
		return nil, commandError{message: fmt.Sprintf("%s环境变量 %s 未设置或为空", purpose, name)}
	}
	return []byte(value), nil
}

func readBearerToken(filePath, environmentName, purpose string) (string, error) {
	if filePath != "" {
		if environmentName != "" && environmentName != defaultTokenEnvironment(purpose) {
			return "", commandError{message: purpose + "只能从文件或环境变量二选一读取"}
		}
		info, err := os.Lstat(filePath)
		if err != nil {
			return "", fmt.Errorf("读取%s文件: %w", purpose, err)
		}
		if !info.Mode().IsRegular() {
			return "", commandError{message: purpose + "文件必须是普通文件"}
		}
		if info.Mode().Perm()&0o077 != 0 {
			return "", commandError{message: purpose + "文件不能允许 group/other 读取"}
		}
		if info.Size() > 1<<20 {
			return "", commandError{message: purpose + "文件过大"}
		}
		content, err := os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("读取%s文件: %w", purpose, err)
		}
		token := strings.TrimSpace(string(content))
		clear(content)
		if token == "" {
			return "", commandError{message: purpose + "文件为空"}
		}
		return token, nil
	}
	secret, err := readEnvironmentSecret(environmentName, purpose)
	if err != nil {
		return "", err
	}
	token := string(secret)
	clear(secret)
	return token, nil
}

func defaultTokenEnvironment(purpose string) string {
	if purpose == "人工管理 Token" {
		return "CHRONODESK_HUMAN_TOKEN"
	}
	return "CHRONODESK_ACCESS_TOKEN"
}

func newHTTPClient(timeout time.Duration) (*http.Client, error) {
	if timeout <= 0 || timeout > 5*time.Minute {
		return nil, commandError{message: "timeout 必须大于 0 且不超过 5m"}
	}
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("重定向已拒绝")
		},
	}, nil
}

func executeJSON(
	ctx context.Context,
	client *http.Client,
	method string,
	requestURL string,
	headers map[string]string,
	body io.Reader,
	destination any,
) error {
	request, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return fmt.Errorf("创建 HTTP 请求: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "chronodeskctl/0.1")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("请求失败: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(
		io.LimitReader(response.Body, maxJSONResponseBytes+1),
	)
	if err != nil {
		return fmt.Errorf("读取响应失败: %w", err)
	}
	if len(responseBody) > maxJSONResponseBytes {
		return commandError{message: "远端 JSON 响应超过 2MiB"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &diagnosticError{
			Message: fmt.Sprintf(
				"HTTP %d: %s",
				response.StatusCode,
				sanitizedRemoteError(responseBody),
			),
			Unhealthy: true,
		}
	}
	if destination == nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("解析 JSON 响应失败: %w", err)
	}
	return nil
}

func sanitizedRemoteError(body []byte) string {
	var problem struct {
		Code   string `json:"code"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Msg    string `json:"msg"`
		Error  string `json:"error"`
	}
	if json.Unmarshal(body, &problem) == nil {
		for _, value := range []string{
			problem.Code,
			problem.Error,
			problem.Title,
			problem.Detail,
			problem.Msg,
		} {
			value = safeDiagnosticText(value)
			if value != "" {
				runes := []rune(value)
				if len(runes) > 300 {
					return string(runes[:300])
				}
				return value
			}
		}
	}
	return "远端返回非成功状态"
}

func safeDiagnosticText(value string) string {
	return strings.TrimSpace(strings.Map(func(character rune) rune {
		if unicode.IsControl(character) || unicode.In(character, unicode.Cf) {
			return -1
		}
		return character
	}, value))
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func runHealth(args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("health", stderr)
	baseURL := flags.String("base-url", "http://localhost:8081", "ChronoDesk 根地址")
	timeout := flags.Duration("timeout", defaultTimeout, "请求超时")
	if err := flags.Parse(args); err != nil {
		return commandError{message: err.Error()}
	}
	if err := requireNoArguments(flags); err != nil {
		return err
	}
	base, err := parseBaseURL(*baseURL)
	if err != nil {
		return err
	}
	client, err := newHTTPClient(*timeout)
	if err != nil {
		return err
	}
	var health struct {
		Status       string            `json:"status"`
		Message      string            `json:"message"`
		Version      string            `json:"version"`
		Build        map[string]string `json:"build"`
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := executeJSON(
		context.Background(),
		client,
		http.MethodGet,
		endpoint(base, "/healthz"),
		nil,
		nil,
		&health,
	); err != nil {
		return err
	}
	if err := writeJSON(stdout, health); err != nil {
		return err
	}
	if health.Status != "ok" {
		return &diagnosticError{Message: "ChronoDesk 健康状态不是 ok", Unhealthy: true}
	}
	for _, dependency := range []string{"postgresql", "redis", "agent_control"} {
		status, exists := health.Dependencies[dependency]
		if !exists || status != "ok" {
			return &diagnosticError{
				Message:   fmt.Sprintf("依赖 %s 的健康状态是 %s", dependency, status),
				Unhealthy: true,
			}
		}
	}
	return nil
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
	Resource    string `json:"resource"`
	ProjectKey  string `json:"project_key"`
}

func audienceResource(base *url.URL, audience string) (string, error) {
	switch audience {
	case "api":
		return endpoint(base, "/api/v2"), nil
	case "mcp":
		return endpoint(base, "/mcp"), nil
	case "a2a":
		return endpoint(base, "/a2a/v1"), nil
	default:
		return "", commandError{message: "audience 必须显式指定为 api、mcp 或 a2a"}
	}
}

func runOAuthClientCredentials(args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("oauth client-credentials", stderr)
	baseURL := flags.String("base-url", "http://localhost:8081", "ChronoDesk 根地址")
	projectKey := flags.String("project-key", "", "目标项目键")
	audience := flags.String("audience", "", "目标 audience: api、mcp 或 a2a")
	clientID := flags.String("client-id", "", "Service Principal client_id")
	clientSecretEnvironment := flags.String(
		"client-secret-env",
		"CHRONODESK_CLIENT_SECRET",
		"保存 client_secret 的环境变量名",
	)
	scope := flags.String("scope", "", "空格分隔的最小权限集合")
	tokenOutput := flags.String("token-output", "", "写入 access token 的新文件")
	timeout := flags.Duration("timeout", defaultTimeout, "请求超时")
	if err := flags.Parse(args); err != nil {
		return commandError{message: err.Error()}
	}
	if err := requireNoArguments(flags); err != nil {
		return err
	}
	if err := validateProjectKey(*projectKey); err != nil {
		return err
	}
	if strings.TrimSpace(*clientID) == "" {
		return commandError{message: "client-id 必填"}
	}
	base, err := parseBaseURL(*baseURL)
	if err != nil {
		return err
	}
	resource, err := audienceResource(base, *audience)
	if err != nil {
		return err
	}
	clientSecret, err := readEnvironmentSecret(*clientSecretEnvironment, "客户端密钥")
	if err != nil {
		return err
	}
	defer clear(clientSecret)
	form := url.Values{
		"grant_type":  {"client_credentials"},
		"project_key": {*projectKey},
		"resource":    {resource},
	}
	if normalizedScope := strings.Join(strings.Fields(*scope), " "); normalizedScope != "" {
		form.Set("scope", normalizedScope)
	}
	client, err := newHTTPClient(*timeout)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		endpoint(base, "/oauth/token"),
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return fmt.Errorf("创建 OAuth 请求: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", "chronodeskctl/0.1")
	request.SetBasicAuth(strings.TrimSpace(*clientID), string(clientSecret))
	response, err := client.Do(request)
	request.Header.Del("Authorization")
	if err != nil {
		return fmt.Errorf("OAuth 请求失败: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(
		io.LimitReader(response.Body, maxOAuthResponseBytes+1),
	)
	if err != nil {
		return fmt.Errorf("读取 OAuth 响应失败: %w", err)
	}
	defer clear(responseBody)
	if len(responseBody) > maxOAuthResponseBytes {
		clear(responseBody)
		return &diagnosticError{
			Message:   "OAuth 响应超过 1MiB",
			Unhealthy: true,
		}
	}
	if response.StatusCode != http.StatusOK {
		return &diagnosticError{
			Message:   fmt.Sprintf("OAuth HTTP %d: %s", response.StatusCode, sanitizedRemoteError(responseBody)),
			Unhealthy: true,
		}
	}
	var token tokenResponse
	if err := json.Unmarshal(responseBody, &token); err != nil {
		return fmt.Errorf("解析 OAuth 响应失败: %w", err)
	}
	if token.AccessToken == "" ||
		token.TokenType != "Bearer" ||
		token.ProjectKey != *projectKey ||
		token.Resource != resource ||
		token.ExpiresIn <= 0 ||
		token.ExpiresIn > 3600 {
		return &diagnosticError{
			Message:   "OAuth 响应未遵守项目或 audience 绑定契约",
			Unhealthy: true,
		}
	}
	outputPath := ""
	if *tokenOutput != "" {
		outputPath, err = writeSecretFile(*tokenOutput, []byte(token.AccessToken))
		if err != nil {
			return err
		}
	}
	digest := sha256.Sum256([]byte(token.AccessToken))
	result := map[string]any{
		"project_key":         token.ProjectKey,
		"resource":            token.Resource,
		"scope":               token.Scope,
		"token_type":          token.TokenType,
		"expires_in":          token.ExpiresIn,
		"access_token_sha256": hex.EncodeToString(digest[:]),
		"token_output":        outputPath,
	}
	token.AccessToken = ""
	return writeJSON(stdout, result)
}

func writeSecretFile(path string, secret []byte) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", commandError{message: "token-output 不能为空"}
	}
	cleanPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("解析 token-output: %w", err)
	}
	file, err := os.OpenFile(cleanPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("创建 access token 文件失败（不会覆盖已有文件）: %w", err)
	}
	payload := make([]byte, len(secret)+1)
	copy(payload, secret)
	payload[len(payload)-1] = '\n'
	defer clear(payload)
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		_ = os.Remove(cleanPath)
		return "", fmt.Errorf("写入 access token 文件: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(cleanPath)
		return "", fmt.Errorf("同步 access token 文件: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(cleanPath)
		return "", fmt.Errorf("关闭 access token 文件: %w", err)
	}
	return cleanPath, nil
}

func runProjectCapabilities(args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("project capabilities", stderr)
	baseURL := flags.String("base-url", "http://localhost:8081", "ChronoDesk 根地址")
	projectKey := flags.String("project-key", "", "目标项目键")
	tokenEnvironment := flags.String(
		"token-env",
		"CHRONODESK_ACCESS_TOKEN",
		"保存 API audience access token 的环境变量名",
	)
	tokenFile := flags.String("token-file", "", "保存 API audience access token 的文件")
	timeout := flags.Duration("timeout", defaultTimeout, "请求超时")
	if err := flags.Parse(args); err != nil {
		return commandError{message: err.Error()}
	}
	if err := requireNoArguments(flags); err != nil {
		return err
	}
	if err := validateProjectKey(*projectKey); err != nil {
		return err
	}
	base, err := parseBaseURL(*baseURL)
	if err != nil {
		return err
	}
	token, err := readBearerToken(*tokenFile, *tokenEnvironment, "机器 API Token")
	if err != nil {
		return err
	}
	client, err := newHTTPClient(*timeout)
	if err != nil {
		return err
	}
	var response struct {
		Data struct {
			APIVersion    string            `json:"api_version"`
			OpenAPI       string            `json:"openapi"`
			AsyncAPI      string            `json:"asyncapi"`
			MCPEndpoint   string            `json:"mcp_endpoint"`
			MCPVersion    string            `json:"mcp_version"`
			A2AEndpoint   string            `json:"a2a_endpoint"`
			A2AVersion    string            `json:"a2a_version"`
			AgentCard     string            `json:"agent_card"`
			OAuthMetadata map[string]string `json:"oauth_metadata"`
			Scopes        []string          `json:"scopes_supported"`
			Concurrency   map[string]bool   `json:"concurrency"`
		} `json:"data"`
	}
	requestURL := endpoint(
		base,
		"/api/v2/projects/"+url.PathEscape(*projectKey)+"/capabilities",
	)
	err = executeJSON(
		context.Background(),
		client,
		http.MethodGet,
		requestURL,
		map[string]string{"Authorization": "Bearer " + token},
		nil,
		&response,
	)
	token = ""
	if err != nil {
		return err
	}
	if response.Data.APIVersion != "v2" ||
		response.Data.MCPVersion != "2026-07-28" ||
		response.Data.A2AVersion != "1.0" ||
		response.Data.OpenAPI == "" ||
		response.Data.AsyncAPI == "" ||
		response.Data.MCPEndpoint != "/mcp" ||
		response.Data.A2AEndpoint != "/a2a/v1" ||
		response.Data.AgentCard != "/.well-known/agent-card.json" ||
		response.Data.OAuthMetadata["api"] != "/.well-known/oauth-protected-resource/api/v2" ||
		response.Data.OAuthMetadata["mcp"] != "/.well-known/oauth-protected-resource/mcp" ||
		response.Data.OAuthMetadata["a2a"] != "/.well-known/oauth-protected-resource/a2a/v1" ||
		!containsString(response.Data.Scopes, "tickets:read") ||
		!response.Data.Concurrency["optimistic_version"] ||
		!response.Data.Concurrency["ticket_leases"] ||
		!response.Data.Concurrency["idempotency_keys"] {
		return &diagnosticError{
			Message:   "项目能力响应与 ChronoDesk 单版本契约不一致",
			Unhealthy: true,
		}
	}
	return writeJSON(stdout, map[string]any{
		"project_key":  *projectKey,
		"healthy":      true,
		"capabilities": response.Data,
	})
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

type connectionHealth struct {
	ID            string     `json:"id"`
	Key           string     `json:"key"`
	Name          string     `json:"name"`
	Status        string     `json:"status"`
	LastVerified  *time.Time `json:"last_verified_at,omitempty"`
	LastErrorAt   *time.Time `json:"last_error_at,omitempty"`
	LastErrorCode string     `json:"last_error_code,omitempty"`
}

func runProjectConnections(args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("project connections", stderr)
	baseURL := flags.String("base-url", "http://localhost:8081", "ChronoDesk 根地址")
	projectKey := flags.String("project-key", "", "目标项目键")
	tokenEnvironment := flags.String(
		"human-token-env",
		"CHRONODESK_HUMAN_TOKEN",
		"保存 Human REST access token 的环境变量名",
	)
	tokenFile := flags.String("human-token-file", "", "保存 Human REST access token 的文件")
	timeout := flags.Duration("timeout", defaultTimeout, "请求超时")
	if err := flags.Parse(args); err != nil {
		return commandError{message: err.Error()}
	}
	if err := requireNoArguments(flags); err != nil {
		return err
	}
	if err := validateProjectKey(*projectKey); err != nil {
		return err
	}
	base, err := parseBaseURL(*baseURL)
	if err != nil {
		return err
	}
	token, err := readBearerToken(*tokenFile, *tokenEnvironment, "人工管理 Token")
	if err != nil {
		return err
	}
	client, err := newHTTPClient(*timeout)
	if err != nil {
		return err
	}
	headers := map[string]string{"Authorization": "Bearer " + token}
	prefix := "/api/projects/" + url.PathEscape(*projectKey) + "/integrations"
	var overview struct {
		Code int `json:"code"`
		Data struct {
			ConnectorDefinitions int64              `json:"connector_definitions"`
			Connections          int64              `json:"connections"`
			ActiveConnections    int64              `json:"active_connections"`
			ErrorConnections     int64              `json:"error_connections"`
			OpenConflicts        int64              `json:"open_conflicts"`
			OpenDeadLetters      int64              `json:"open_dead_letters"`
			RunningSyncRuns      int64              `json:"running_sync_runs"`
			ConnectionHealth     []connectionHealth `json:"connection_health"`
		} `json:"data"`
	}
	if err := executeJSON(
		context.Background(),
		client,
		http.MethodGet,
		endpoint(base, prefix+"/overview"),
		headers,
		nil,
		&overview,
	); err != nil {
		token = ""
		return err
	}
	if overview.Code != 0 {
		token = ""
		return &diagnosticError{
			Message:   "集成概览响应业务码不是 0",
			Unhealthy: true,
		}
	}
	var connections struct {
		Code int `json:"code"`
		Data struct {
			Items []struct {
				ID                 string     `json:"id"`
				Key                string     `json:"key"`
				Name               string     `json:"name"`
				Status             string     `json:"status"`
				HasConfiguration   bool       `json:"has_configuration"`
				HasVerificationKey bool       `json:"has_verification_key"`
				LastVerifiedAt     *time.Time `json:"last_verified_at,omitempty"`
				LastErrorAt        *time.Time `json:"last_error_at,omitempty"`
				LastErrorCode      string     `json:"last_error_code,omitempty"`
			} `json:"items"`
			Total int64 `json:"total"`
		} `json:"data"`
	}
	connectionsURL, err := url.Parse(endpoint(base, prefix+"/connections"))
	if err != nil {
		token = ""
		return fmt.Errorf("创建连接诊断 URL: %w", err)
	}
	query := connectionsURL.Query()
	query.Set("page", "1")
	query.Set("pageSize", "100")
	connectionsURL.RawQuery = query.Encode()
	if err := executeJSON(
		context.Background(),
		client,
		http.MethodGet,
		connectionsURL.String(),
		headers,
		nil,
		&connections,
	); err != nil {
		token = ""
		return err
	}
	token = ""
	if connections.Code != 0 {
		return &diagnosticError{
			Message:   "连接列表响应业务码不是 0",
			Unhealthy: true,
		}
	}
	healthy := overview.Data.ErrorConnections == 0
	for _, connection := range connections.Data.Items {
		switch connection.Status {
		case "active", "inactive", "archived":
		case "error":
			healthy = false
		default:
			healthy = false
		}
		if connection.LastErrorCode != "" {
			healthy = false
		}
	}
	result := map[string]any{
		"project_key": *projectKey,
		"healthy":     healthy,
		"overview":    overview.Data,
		"connections": connections.Data.Items,
		"total":       connections.Data.Total,
	}
	if err := writeJSON(stdout, result); err != nil {
		return err
	}
	if !healthy {
		return &diagnosticError{
			Message:   "一个或多个项目连接处于错误状态",
			Unhealthy: true,
		}
	}
	return nil
}

func parsePositiveTimestamp(raw string) (int64, error) {
	if len(raw) < 10 || len(raw) > 19 || raw[0] == '0' {
		return 0, commandError{message: "timestamp 必须是 10 到 19 位、无前导零的 Unix 秒数"}
	}
	for _, character := range raw {
		if character < '0' || character > '9' {
			return 0, commandError{message: "timestamp 必须是十进制 Unix 秒数"}
		}
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, commandError{message: "timestamp 超出有效范围"}
	}
	return value, nil
}

func readBody(path string, stdin io.Reader) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, commandError{message: "body 必填；使用 - 从标准输入读取"}
	}
	var reader io.Reader
	var file *os.File
	if path == "-" {
		reader = stdin
	} else {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("读取 body: %w", err)
		}
		if !info.Mode().IsRegular() {
			return nil, commandError{message: "body 必须是普通文件或 -"}
		}
		file, err = os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("打开 body: %w", err)
		}
		defer file.Close()
		reader = file
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取 body: %w", err)
	}
	if len(body) == 0 || len(body) > maxBodyBytes {
		return nil, commandError{message: "body 必须为 1B 到 10MiB"}
	}
	return body, nil
}
