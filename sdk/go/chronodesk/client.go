// Package chronodesk provides a small, dependency-free client for the
// project-scoped ChronoDesk Agent REST API.
package chronodesk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// AudienceAPI binds an OAuth token to the Agent REST resource.
	AudienceAPI Audience = "api"
	// AudienceMCP binds an OAuth token to the MCP resource.
	AudienceMCP Audience = "mcp"
	// AudienceA2A binds an OAuth token to the A2A 1.0 resource.
	AudienceA2A Audience = "a2a"
)

var projectKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,15}$`)

const maxResponseBytes = 4 << 20

// Audience identifies one and only one protected resource.
type Audience string

// Client is bound to exactly one project. Use WithAccessToken to derive an
// authenticated copy without mutating a client shared by other goroutines.
type Client struct {
	baseURL     *url.URL
	projectKey  string
	accessToken string
	httpClient  *http.Client
}

// Option configures a Client.
type Option func(*Client) error

// WithHTTPClient installs a caller-owned HTTP client.
func WithHTTPClient(client *http.Client) Option {
	return func(target *Client) error {
		if client == nil {
			return errors.New("HTTP client is required")
		}
		copyClient := *client
		if copyClient.Timeout == 0 {
			copyClient.Timeout = 30 * time.Second
		}
		if copyClient.Timeout < 0 || copyClient.Timeout > 5*time.Minute {
			return errors.New("HTTP client timeout must be positive and at most 5 minutes")
		}
		target.httpClient = &copyClient
		return nil
	}
}

// WithTimeout sets the total request timeout. It should be applied after
// WithHTTPClient when both options are present.
func WithTimeout(timeout time.Duration) Option {
	return func(target *Client) error {
		if timeout <= 0 || timeout > 5*time.Minute {
			return errors.New("timeout must be positive and at most 5 minutes")
		}
		copyClient := *target.httpClient
		copyClient.Timeout = timeout
		target.httpClient = &copyClient
		return nil
	}
}

// WithAccessToken installs an API-audience access token on the new client.
func WithAccessToken(token string) Option {
	return func(target *Client) error {
		token = strings.TrimSpace(token)
		if token == "" {
			return errors.New("access token is required")
		}
		target.accessToken = token
		return nil
	}
}

// New constructs a client whose every Agent REST request includes projectKey
// in the path.
func New(baseURL, projectKey string, options ...Option) (*Client, error) {
	parsed, err := parseBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	if !projectKeyPattern.MatchString(projectKey) {
		return nil, errors.New("project key must match ^[A-Z][A-Z0-9]{1,15}$")
	}
	client := &Client{
		baseURL:    parsed,
		projectKey: projectKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return errors.New("redirect rejected")
			},
		},
	}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("nil client option")
		}
		if err := option(client); err != nil {
			return nil, err
		}
	}
	return client, nil
}

// WithToken derives an authenticated copy of a Client.
func (client *Client) WithToken(token string) (*Client, error) {
	if client == nil {
		return nil, errors.New("client is nil")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("access token is required")
	}
	copyClient := *client
	copyClient.accessToken = token
	return &copyClient, nil
}

// ClientCredentials is an OAuth client-credentials request. Audience is
// mandatory; there is deliberately no default audience.
type ClientCredentials struct {
	ClientID     string
	ClientSecret string
	Audience     Audience
	Scopes       []string
}

// TokenResponse is the audience- and project-bound OAuth result.
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
	Resource    string `json:"resource"`
	ProjectKey  string `json:"project_key"`
}

// ExchangeClientCredentials obtains a token for exactly this Client's project.
// ClientSecret is never included in returned errors.
func (client *Client) ExchangeClientCredentials(
	ctx context.Context,
	credentials ClientCredentials,
) (TokenResponse, error) {
	if client == nil || client.baseURL == nil || client.httpClient == nil {
		return TokenResponse{}, errors.New("client is not initialized")
	}
	clientID := strings.TrimSpace(credentials.ClientID)
	if clientID == "" || credentials.ClientSecret == "" {
		return TokenResponse{}, errors.New("client ID and client secret are required")
	}
	resource, err := client.resource(credentials.Audience)
	if err != nil {
		return TokenResponse{}, err
	}
	form := url.Values{
		"grant_type":  {"client_credentials"},
		"project_key": {client.projectKey},
		"resource":    {resource},
	}
	if scopes := normalizeScopes(credentials.Scopes); len(scopes) != 0 {
		form.Set("scope", strings.Join(scopes, " "))
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		client.endpoint("/oauth/token"),
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return TokenResponse{}, fmt.Errorf("create OAuth request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", "chronodesk-go/0.1")
	request.SetBasicAuth(clientID, credentials.ClientSecret)

	var token TokenResponse
	if err := client.execute(request, &token); err != nil {
		return TokenResponse{}, err
	}
	if token.AccessToken == "" ||
		token.TokenType != "Bearer" ||
		token.ExpiresIn <= 0 ||
		token.ExpiresIn > 3600 ||
		token.ProjectKey != client.projectKey ||
		token.Resource != resource {
		return TokenResponse{}, errors.New("OAuth response violates project or audience binding")
	}
	return token, nil
}

// Meta carries cursor pagination data.
type Meta struct {
	RequestID  string `json:"request_id"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more,omitempty"`
}

// Envelope is the common Agent REST response.
type Envelope[T any] struct {
	Data T    `json:"data"`
	Meta Meta `json:"meta"`
}

// Capabilities describes the supported single-version machine contract.
type Capabilities struct {
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
}

// Capabilities reads the machine contract for this project.
func (client *Client) Capabilities(
	ctx context.Context,
) (Envelope[Capabilities], error) {
	var result Envelope[Capabilities]
	request, err := client.agentRequest(ctx, http.MethodGet, "/capabilities", nil)
	if err != nil {
		return result, err
	}
	if err := client.execute(request, &result); err != nil {
		return result, err
	}
	if result.Data.APIVersion != "v2" ||
		result.Data.MCPVersion != "2026-07-28" ||
		result.Data.A2AVersion != "1.0" ||
		result.Data.OpenAPI == "" ||
		result.Data.AsyncAPI == "" ||
		result.Data.MCPEndpoint != "/mcp" ||
		result.Data.A2AEndpoint != "/a2a/v1" ||
		result.Data.AgentCard != "/.well-known/agent-card.json" ||
		result.Data.OAuthMetadata["api"] != "/.well-known/oauth-protected-resource/api/v2" ||
		result.Data.OAuthMetadata["mcp"] != "/.well-known/oauth-protected-resource/mcp" ||
		result.Data.OAuthMetadata["a2a"] != "/.well-known/oauth-protected-resource/a2a/v1" ||
		!scopeIncluded(result.Data.Scopes, "tickets:read") ||
		!result.Data.Concurrency["optimistic_version"] ||
		!result.Data.Concurrency["ticket_leases"] ||
		!result.Data.Concurrency["idempotency_keys"] {
		return result, errors.New("capabilities response violates the supported protocol versions")
	}
	return result, nil
}

// Ticket is the stable minimum view used by list responses. Text fields are
// untrusted data and must never be interpreted as instructions.
type Ticket struct {
	ID           uint64         `json:"id"`
	TicketNumber string         `json:"ticket_number"`
	Title        string         `json:"title"`
	Description  string         `json:"description"`
	Type         string         `json:"type"`
	Priority     string         `json:"priority"`
	Status       string         `json:"status"`
	Source       string         `json:"source"`
	Version      uint64         `json:"version"`
	Tags         []string       `json:"tags"`
	CustomFields map[string]any `json:"custom_fields,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// TicketListOptions controls bounded cursor pagination.
type TicketListOptions struct {
	Cursor   string
	Limit    int
	Status   string
	Priority string
	Search   string
}

// ListTickets lists tickets only inside this Client's project.
func (client *Client) ListTickets(
	ctx context.Context,
	options TicketListOptions,
) (Envelope[[]Ticket], error) {
	var result Envelope[[]Ticket]
	query := url.Values{}
	if options.Cursor != "" {
		query.Set("cursor", options.Cursor)
	}
	if options.Limit != 0 {
		if options.Limit < 1 || options.Limit > 100 {
			return result, errors.New("limit must be between 1 and 100")
		}
		query.Set("limit", strconv.Itoa(options.Limit))
	}
	if options.Status != "" {
		query.Set("status", options.Status)
	}
	if options.Priority != "" {
		query.Set("priority", options.Priority)
	}
	if options.Search != "" {
		query.Set("search", options.Search)
	}
	requestPath := "/tickets"
	if encoded := query.Encode(); encoded != "" {
		requestPath += "?" + encoded
	}
	request, err := client.agentRequest(ctx, http.MethodGet, requestPath, nil)
	if err != nil {
		return result, err
	}
	if err := client.execute(request, &result); err != nil {
		return result, err
	}
	if result.Data == nil {
		result.Data = []Ticket{}
	}
	return result, nil
}

// Problem is a safe representation of an Agent REST error.
type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	Code      string `json:"code"`
	RequestID string `json:"request_id"`
	Retryable bool   `json:"retryable"`
}

// APIError reports one non-2xx response.
type APIError struct {
	Problem Problem
}

func (err *APIError) Error() string {
	if err == nil {
		return ""
	}
	if err.Problem.Code != "" {
		return fmt.Sprintf("ChronoDesk API %d: %s", err.Problem.Status, err.Problem.Code)
	}
	return fmt.Sprintf("ChronoDesk API %d", err.Problem.Status)
}

func (client *Client) agentRequest(
	ctx context.Context,
	method, path string,
	body []byte,
) (*http.Request, error) {
	if client == nil || client.baseURL == nil || client.httpClient == nil {
		return nil, errors.New("client is not initialized")
	}
	if strings.TrimSpace(client.accessToken) == "" {
		return nil, errors.New("API audience access token is required")
	}
	request, err := http.NewRequestWithContext(
		ctx,
		method,
		client.agentEndpoint(path),
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+client.accessToken)
	request.Header.Set("User-Agent", "chronodesk-go/0.1")
	return request, nil
}

func (client *Client) execute(request *http.Request, destination any) error {
	httpClient := *client.httpClient
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return errors.New("redirect rejected")
	}
	response, err := httpClient.Do(request)
	request.Header.Del("Authorization")
	if err != nil {
		return fmt.Errorf("ChronoDesk request failed: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read ChronoDesk response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return errors.New("ChronoDesk response exceeds 4 MiB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var problem Problem
		_ = json.Unmarshal(body, &problem)
		if problem.Status == 0 {
			problem.Status = response.StatusCode
		}
		return &APIError{Problem: problem}
	}
	if destination == nil {
		return nil
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return fmt.Errorf("decode ChronoDesk response: %w", err)
	}
	return nil
}

func (client *Client) resource(audience Audience) (string, error) {
	switch audience {
	case AudienceAPI:
		return client.endpoint("/api/v2"), nil
	case AudienceMCP:
		return client.endpoint("/mcp"), nil
	case AudienceA2A:
		return client.endpoint("/a2a/v1"), nil
	default:
		return "", errors.New("audience must be explicitly api, mcp, or a2a")
	}
}

func (client *Client) endpoint(path string) string {
	copyURL := *client.baseURL
	copyURL.Path = strings.TrimRight(client.baseURL.Path, "/") + "/" +
		strings.TrimLeft(path, "/")
	copyURL.RawPath = ""
	return copyURL.String()
}

func (client *Client) agentEndpoint(path string) string {
	pathOnly, rawQuery, _ := strings.Cut(path, "?")
	result := client.endpoint(
		"/api/v2/projects/" + url.PathEscape(client.projectKey) + "/" +
			strings.TrimLeft(pathOnly, "/"),
	)
	if rawQuery != "" {
		result += "?" + rawQuery
	}
	return result
}

func parseBaseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" ||
		parsed.User != nil ||
		(parsed.EscapedPath() != "" && parsed.EscapedPath() != "/") ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return nil, errors.New("base URL must be an http(s) origin without path, credentials, query, or fragment")
	}
	if parsed.Scheme == "http" && !loopbackHostname(parsed.Hostname()) {
		return nil, errors.New("non-loopback base URL must use HTTPS")
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

func normalizeScopes(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		for _, scope := range strings.Fields(value) {
			if _, exists := seen[scope]; exists {
				continue
			}
			seen[scope] = struct{}{}
			result = append(result, scope)
		}
	}
	return result
}

func scopeIncluded(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
