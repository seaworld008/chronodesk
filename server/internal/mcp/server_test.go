package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	sdkjsonrpc "github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type backendCall struct {
	principal Principal
	name      string
	arguments map[string]any
	hasCtx    bool
}

type fakeBackend struct {
	mu sync.Mutex

	calls        []backendCall
	callResult   map[string]any
	callErr      error
	resource     ResourceContent
	resourceErr  error
	resourceURIs []string
	subscribeOK  *bool
	subscribeErr error
	subscribe    func(Principal, string) (bool, error)
	tokenValid   bool
}

type testAuthenticator struct {
	authenticate func(context.Context, string) (Principal, error)
	revalidate   func(context.Context, string) (Principal, error)
}

func (a *testAuthenticator) Authenticate(ctx context.Context, token string) (Principal, error) {
	return a.authenticate(ctx, token)
}

func (a *testAuthenticator) Revalidate(ctx context.Context, token string) (Principal, error) {
	return a.revalidate(ctx, token)
}

func (f *fakeBackend) CallTool(ctx context.Context, principal Principal, name string, arguments map[string]any) (map[string]any, error) {
	_, hasCtx := PrincipalFromContext(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, backendCall{
		principal: principal,
		name:      name,
		arguments: arguments,
		hasCtx:    hasCtx,
	})
	if f.callErr != nil {
		return nil, f.callErr
	}
	if f.callResult != nil {
		return f.callResult, nil
	}
	return map[string]any{"items": []any{}}, nil
}

func (f *fakeBackend) ReadResource(_ context.Context, _ Principal, uri string) (ResourceContent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resourceURIs = append(f.resourceURIs, uri)
	if f.resourceErr != nil {
		return ResourceContent{}, f.resourceErr
	}
	result := f.resource
	if result.Text == "" && result.Blob == "" {
		result.Text = `{"id":42,"title":"untrusted"}`
	}
	return result, nil
}

func (f *fakeBackend) ValidateSubscription(_ context.Context, principal Principal, uri string) (bool, error) {
	f.mu.Lock()
	validate := f.subscribe
	subscribeErr := f.subscribeErr
	subscribeOK := f.subscribeOK
	resourceErr := f.resourceErr
	f.mu.Unlock()
	if validate != nil {
		return validate(principal, uri)
	}
	if subscribeErr != nil {
		return false, subscribeErr
	}
	if subscribeOK != nil {
		return *subscribeOK, nil
	}
	return resourceErr == nil, nil
}

func (f *fakeBackend) authenticate(token string, scopes []string) (Principal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if token != "valid-token" || !f.tokenValid {
		return Principal{}, errors.New("invalid token")
	}
	return Principal{
		Type:         "service_principal",
		ID:           "agent-1",
		CredentialID: "credential-1",
		Scopes:       append([]string(nil), scopes...),
		Attributes: map[string]any{
			"expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano),
		},
	}, nil
}

func (f *fakeBackend) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type testFixture struct {
	t       *testing.T
	backend *fakeBackend
	server  *Server
	http    *httptest.Server
	scopes  []string
}

func newTestFixture(t *testing.T, scopes []string, options ...Option) *testFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	backend := &fakeBackend{tokenValid: true}
	authenticator := &testAuthenticator{
		authenticate: func(_ context.Context, token string) (Principal, error) {
			return backend.authenticate(token, scopes)
		},
		revalidate: func(_ context.Context, token string) (Principal, error) {
			return backend.authenticate(token, scopes)
		},
	}
	server, err := NewServer(backend, authenticator, options...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	router := gin.New()
	router.Any("/mcp", server.Handler())
	httpServer := httptest.NewServer(router)
	fixture := &testFixture{
		t:       t,
		backend: backend,
		server:  server,
		http:    httpServer,
		scopes:  scopes,
	}
	t.Cleanup(func() {
		_ = server.Close()
		httpServer.Close()
	})
	return fixture
}

func modernMeta() map[string]any {
	return map[string]any{
		"io.modelcontextprotocol/protocolVersion": ProtocolVersion,
		"io.modelcontextprotocol/clientCapabilities": map[string]any{
			"extensions": map[string]any{
				OAuthClientCredentialsExtension: map[string]any{},
			},
		},
		"io.modelcontextprotocol/clientInfo": map[string]any{
			"name":    "chronodesk-test-client",
			"version": "1.0.0",
		},
	}
}

func (f *testFixture) post(method, name string, params map[string]any) *http.Response {
	f.t.Helper()
	return f.postWithHeaders(method, name, params, map[string]string{
		HeaderProtocolVersion: ProtocolVersion,
		HeaderMethod:          method,
	})
}

func (f *testFixture) postWithHeaders(method, name string, params map[string]any, headers map[string]string) *http.Response {
	f.t.Helper()
	if params == nil {
		params = map[string]any{}
	}
	if method == "tools/call" {
		if arguments, ok := params["arguments"].(map[string]any); ok {
			scopedArguments := make(map[string]any, len(arguments)+1)
			for key, value := range arguments {
				scopedArguments[key] = value
			}
			if _, exists := scopedArguments["project_key"]; !exists {
				scopedArguments["project_key"] = "TEST"
			}
			params["arguments"] = scopedArguments
		}
	}
	if _, ok := params["_meta"]; !ok {
		params["_meta"] = modernMeta()
	}
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      "request-1",
		"method":  method,
		"params":  params,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		f.t.Fatalf("json.Marshal() error = %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, f.http.URL+"/mcp", bytes.NewReader(body))
	if err != nil {
		f.t.Fatalf("http.NewRequest() error = %v", err)
	}
	request.Header.Set("Authorization", "Bearer valid-token")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	if name != "" {
		request.Header.Set(HeaderName, name)
	}
	response, err := f.http.Client().Do(request)
	if err != nil {
		f.t.Fatalf("HTTP POST error = %v", err)
	}
	return response
}

func openSubscription(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	token string,
	requestID string,
	uris []string,
) (*http.Response, error) {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      requestID,
		"method":  "subscriptions/listen",
		"params": map[string]any{
			"_meta": modernMeta(),
			"notifications": map[string]any{
				"resourceSubscriptions": uris,
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(HeaderProtocolVersion, ProtocolVersion)
	request.Header.Set(HeaderMethod, "subscriptions/listen")
	return client.Do(request)
}

func scanSSE(response *http.Response) <-chan map[string]any {
	messages := make(chan map[string]any, 8)
	go func() {
		defer close(messages)
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var message map[string]any
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &message) == nil {
				messages <- message
			}
		}
	}()
	return messages
}

func testPrincipal(id, credential string, scopes []string) Principal {
	return Principal{
		Type:         "service_principal",
		ID:           id,
		CredentialID: credential,
		Scopes:       append([]string(nil), scopes...),
		Attributes: map[string]any{
			"expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano),
		},
	}
}

func decodeRPCResponse(t *testing.T, response *http.Response) map[string]any {
	t.Helper()
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response error: %v", err)
	}
	return payload
}

func rpcErrorCode(t *testing.T, response *http.Response) float64 {
	t.Helper()
	payload := decodeRPCResponse(t, response)
	rpcError, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing JSON-RPC error: %#v", payload)
	}
	code, ok := rpcError["code"].(float64)
	if !ok {
		t.Fatalf("invalid JSON-RPC error code: %#v", rpcError)
	}
	return code
}

func TestDiscoverAdvertisesOnlyMCP20260728(t *testing.T) {
	fixture := newTestFixture(t, []string{"*"})
	response := fixture.post("server/discover", "", nil)
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("discover status=%d body=%s", response.StatusCode, body)
	}
	if response.Header.Get("Mcp-Session-Id") != "" {
		t.Fatal("stateless response returned a legacy session id")
	}
	payload := decodeRPCResponse(t, response)
	result := payload["result"].(map[string]any)
	if result["resultType"] != "complete" {
		t.Fatalf("resultType=%v", result["resultType"])
	}
	versions := result["supportedVersions"].([]any)
	if len(versions) != 1 || versions[0] != ProtocolVersion {
		t.Fatalf("supportedVersions=%v", versions)
	}
	if result["ttlMs"].(float64) != 300000 || result["cacheScope"] != "private" {
		t.Fatalf("discover cache hints=%#v", result)
	}
	meta := result["_meta"].(map[string]any)
	serverInfo := meta["io.modelcontextprotocol/serverInfo"].(map[string]any)
	if serverInfo["name"] != "chronodesk" {
		t.Fatalf("serverInfo=%#v", serverInfo)
	}
	capabilities := result["capabilities"].(map[string]any)
	extensions, ok := capabilities["extensions"].(map[string]any)
	if !ok {
		t.Fatalf("server extensions=%#v", capabilities["extensions"])
	}
	if len(extensions) != 1 {
		t.Fatalf("server extensions=%#v, want only OAuth client credentials", extensions)
	}
	settings, ok := extensions[OAuthClientCredentialsExtension].(map[string]any)
	if !ok || len(settings) != 0 {
		t.Fatalf("%s settings=%#v, want {}", OAuthClientCredentialsExtension, extensions[OAuthClientCredentialsExtension])
	}
	experimental, ok := capabilities["experimental"].(map[string]any)
	if !ok {
		t.Fatalf("server experimental capabilities=%#v", capabilities["experimental"])
	}
	for key, value := range experimental {
		if _, ok := value.(map[string]any); !ok {
			t.Fatalf("experimental capability %q=%#v, want an object-valued capability", key, value)
		}
	}
}

func TestDiscoverRequiresProtocolHeaders(t *testing.T) {
	fixture := newTestFixture(t, []string{"*"})
	response := fixture.postWithHeaders("server/discover", "", nil, nil)
	if response.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("headerless discover status=%d body=%s", response.StatusCode, body)
	}
	if code := rpcErrorCode(t, response); code != float64(sdkmcp.CodeHeaderMismatch) {
		t.Fatalf("code=%v want=%v", code, sdkmcp.CodeHeaderMismatch)
	}
}

func TestRemovedTransportHeadersHaveNoSemantics(t *testing.T) {
	fixture := newTestFixture(t, []string{"*"})
	response := fixture.postWithHeaders("server/discover", "", nil, map[string]string{
		HeaderProtocolVersion: ProtocolVersion,
		HeaderMethod:          "server/discover",
		"Mcp-Session-Id":      "removed-session",
		"Last-Event-ID":       "removed-cursor",
	})
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("discover status=%d body=%s", response.StatusCode, body)
	}
	defer response.Body.Close()
	if response.Header.Get("Mcp-Session-Id") != "" ||
		response.Header.Get("Last-Event-ID") != "" {
		t.Fatalf("removed transport headers were echoed: %#v", response.Header)
	}
}

func TestDiscoverRequiresModernEnvelope(t *testing.T) {
	fixture := newTestFixture(t, []string{"*"})
	tests := []struct {
		name     string
		params   map[string]any
		wantCode int64
	}{
		{
			name: "missing protocol version",
			params: map[string]any{"_meta": map[string]any{
				"io.modelcontextprotocol/clientCapabilities": map[string]any{},
			}},
			wantCode: sdkjsonrpc.CodeInvalidParams,
		},
		{
			name: "unsupported protocol version",
			params: map[string]any{"_meta": map[string]any{
				"io.modelcontextprotocol/protocolVersion":    "2025-11-25",
				"io.modelcontextprotocol/clientCapabilities": map[string]any{},
			}},
			wantCode: sdkmcp.CodeHeaderMismatch,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := fixture.postWithHeaders("server/discover", "", test.params, map[string]string{
				HeaderProtocolVersion: ProtocolVersion,
				HeaderMethod:          "server/discover",
			})
			if response.StatusCode != http.StatusBadRequest {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("status=%d body=%s", response.StatusCode, body)
			}
			if code := rpcErrorCode(t, response); code != float64(test.wantCode) {
				t.Fatalf("code=%v want=%v", code, test.wantCode)
			}
		})
	}

	t.Run("missing client capabilities", func(t *testing.T) {
		response := fixture.postWithHeaders("server/discover", "", map[string]any{
			"_meta": map[string]any{
				"io.modelcontextprotocol/protocolVersion": ProtocolVersion,
			},
		}, map[string]string{
			HeaderProtocolVersion: ProtocolVersion,
			HeaderMethod:          "server/discover",
		})
		if response.StatusCode != http.StatusBadRequest {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("status=%d body=%s", response.StatusCode, body)
		}
		if code := rpcErrorCode(t, response); code != float64(sdkjsonrpc.CodeInvalidParams) {
			t.Fatalf("code=%v want=%v", code, sdkjsonrpc.CodeInvalidParams)
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestOfficialGoSDKClientConnectsWithoutLegacyFallback(t *testing.T) {
	fixture := newTestFixture(t, []string{"*"})
	var observed sync.Map
	httpClient := &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			request = request.Clone(request.Context())
			request.Header.Set("Authorization", "Bearer valid-token")
			if request.Body != nil {
				body, err := io.ReadAll(request.Body)
				if err != nil {
					return nil, fmt.Errorf("read MCP request body: %w", err)
				}
				request.Body = io.NopCloser(bytes.NewReader(body))
				var payload struct {
					Method string `json:"method"`
					Params struct {
						Meta map[string]any `json:"_meta"`
					} `json:"params"`
				}
				if err := json.Unmarshal(body, &payload); err != nil {
					return nil, fmt.Errorf("decode MCP request body: %w", err)
				}
				clientCapabilities, ok := payload.Params.Meta["io.modelcontextprotocol/clientCapabilities"].(map[string]any)
				if !ok {
					return nil, fmt.Errorf("%s request has no per-request client capabilities", payload.Method)
				}
				extensions, ok := clientCapabilities["extensions"].(map[string]any)
				if !ok {
					return nil, fmt.Errorf("%s request has no client extension declaration", payload.Method)
				}
				settings, ok := extensions[OAuthClientCredentialsExtension].(map[string]any)
				if !ok || len(settings) != 0 {
					return nil, fmt.Errorf("%s request extension settings = %#v, want {}", payload.Method, extensions[OAuthClientCredentialsExtension])
				}
				observed.Store(payload.Method, true)
			}
			return http.DefaultTransport.RoundTrip(request)
		}),
	}
	clientCapabilities := &sdkmcp.ClientCapabilities{}
	clientCapabilities.AddExtension(OAuthClientCredentialsExtension, nil)
	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "chronodesk-official-sdk-test",
		Version: "1.0.0",
	}, &sdkmcp.ClientOptions{Capabilities: clientCapabilities})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &sdkmcp.StreamableClientTransport{
		Endpoint:   fixture.http.URL + "/mcp",
		HTTPClient: httpClient,
	}, nil)
	if err != nil {
		t.Fatalf("official SDK connect: %v", err)
	}
	defer session.Close()
	if session.InitializeResult() == nil || session.InitializeResult().ProtocolVersion != ProtocolVersion {
		t.Fatalf("negotiated result=%#v", session.InitializeResult())
	}
	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("official SDK tools/list: %v", err)
	}
	if len(result.Tools) != 13 {
		t.Fatalf("official SDK tool count=%d", len(result.Tools))
	}
	for _, method := range []string{"server/discover", "tools/list"} {
		if _, ok := observed.Load(method); !ok {
			t.Errorf("official SDK did not send %s with per-request OAuth client credentials capability", method)
		}
	}
}

func TestValidBearerDoesNotRequireClientExtensionDeclaration(t *testing.T) {
	fixture := newTestFixture(t, []string{"*"})
	response := fixture.post("tools/list", "", map[string]any{
		"_meta": map[string]any{
			"io.modelcontextprotocol/protocolVersion": ProtocolVersion,
			"io.modelcontextprotocol/clientCapabilities": map[string]any{
				"extensions": map[string]any{},
			},
			"io.modelcontextprotocol/clientInfo": map[string]any{
				"name":    "core-bearer-client",
				"version": "1.0.0",
			},
		},
	})
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("valid bearer without client extension status=%d body=%s", response.StatusCode, body)
	}
	payload := decodeRPCResponse(t, response)
	if _, exists := payload["error"]; exists {
		t.Fatalf("valid bearer without client extension returned error: %#v", payload)
	}
}

func TestOnlyStatelessPOSTTransportIsAvailable(t *testing.T) {
	fixture := newTestFixture(t, []string{"*"})
	for _, method := range []string{http.MethodGet, http.MethodDelete, http.MethodPut} {
		request, err := http.NewRequest(method, fixture.http.URL+"/mcp", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := fixture.http.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s status=%d", method, response.StatusCode)
		}
		if response.Header.Get("Allow") != http.MethodPost {
			t.Fatalf("%s Allow=%q", method, response.Header.Get("Allow"))
		}
	}

	initialize := fixture.post("initialize", "", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "legacy", "version": "1"},
	})
	if initialize.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(initialize.Body)
		t.Fatalf("initialize status=%d body=%s", initialize.StatusCode, body)
	}
	if code := rpcErrorCode(t, initialize); code != -32601 {
		t.Fatalf("initialize code=%v", code)
	}
}

func TestStreamableHTTPRejectsClientNotification(t *testing.T) {
	fixture := newTestFixture(t, []string{"*"})
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/cancelled",
		"params": map[string]any{
			"_meta":     modernMeta(),
			"requestId": "no-longer-needed",
			"reason":    "caller cancelled",
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, fixture.http.URL+"/mcp", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer valid-token")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(HeaderProtocolVersion, ProtocolVersion)
	request.Header.Set(HeaderMethod, "notifications/cancelled")
	response, err := fixture.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		responseBody, _ := io.ReadAll(response.Body)
		t.Fatalf("notification status=%d body=%s", response.StatusCode, responseBody)
	}
	if code := rpcErrorCode(t, response); code != float64(sdkjsonrpc.CodeInvalidRequest) {
		t.Fatalf("code=%v want=%v", code, sdkjsonrpc.CodeInvalidRequest)
	}
}

func TestModernProtocolAndHeaderValidation(t *testing.T) {
	fixture := newTestFixture(t, []string{"*"})
	tests := []struct {
		name       string
		method     string
		toolName   string
		params     map[string]any
		headers    map[string]string
		wantStatus int
		wantCode   float64
	}{
		{
			name:       "missing protocol version",
			method:     "tools/list",
			headers:    map[string]string{HeaderMethod: "tools/list"},
			wantStatus: http.StatusBadRequest,
			wantCode:   -32020,
		},
		{
			name:       "unsupported protocol version",
			method:     "tools/list",
			headers:    map[string]string{HeaderProtocolVersion: "2025-11-25", HeaderMethod: "tools/list"},
			wantStatus: http.StatusBadRequest,
			wantCode:   -32022,
		},
		{
			name:       "missing method header",
			method:     "tools/list",
			headers:    map[string]string{HeaderProtocolVersion: ProtocolVersion},
			wantStatus: http.StatusBadRequest,
			wantCode:   -32020,
		},
		{
			name:       "method mismatch",
			method:     "tools/list",
			headers:    map[string]string{HeaderProtocolVersion: ProtocolVersion, HeaderMethod: "resources/list"},
			wantStatus: http.StatusBadRequest,
			wantCode:   -32020,
		},
		{
			name:     "missing name header",
			method:   "tools/call",
			toolName: "",
			params: map[string]any{
				"name":      "ticket_list",
				"arguments": map[string]any{},
			},
			headers:    map[string]string{HeaderProtocolVersion: ProtocolVersion, HeaderMethod: "tools/call"},
			wantStatus: http.StatusBadRequest,
			wantCode:   -32020,
		},
		{
			name:   "missing client capabilities",
			method: "tools/list",
			params: map[string]any{"_meta": map[string]any{
				"io.modelcontextprotocol/protocolVersion": ProtocolVersion,
			}},
			headers:    map[string]string{HeaderProtocolVersion: ProtocolVersion, HeaderMethod: "tools/list"},
			wantStatus: http.StatusBadRequest,
			wantCode:   -32602,
		},
		{
			name:       "unknown method",
			method:     "chronodesk/unknown",
			headers:    map[string]string{HeaderProtocolVersion: ProtocolVersion, HeaderMethod: "chronodesk/unknown"},
			wantStatus: http.StatusNotFound,
			wantCode:   -32601,
		},
		{
			name:       "removed legacy resource subscribe",
			method:     "resources/subscribe",
			headers:    map[string]string{HeaderProtocolVersion: ProtocolVersion, HeaderMethod: "resources/subscribe"},
			wantStatus: http.StatusNotFound,
			wantCode:   -32601,
		},
		{
			name:       "removed legacy resource unsubscribe",
			method:     "resources/unsubscribe",
			headers:    map[string]string{HeaderProtocolVersion: ProtocolVersion, HeaderMethod: "resources/unsubscribe"},
			wantStatus: http.StatusNotFound,
			wantCode:   -32601,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := fixture.postWithHeaders(test.method, test.toolName, test.params, test.headers)
			if response.StatusCode != test.wantStatus {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("status=%d want=%d body=%s", response.StatusCode, test.wantStatus, body)
			}
			if code := rpcErrorCode(t, response); code != test.wantCode {
				t.Fatalf("code=%v want=%v", code, test.wantCode)
			}
		})
	}
}

func TestToolDiscoveryCallAndCacheContract(t *testing.T) {
	fixture := newTestFixture(t, []string{"*"})
	listResponse := fixture.post("tools/list", "", nil)
	if listResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(listResponse.Body)
		t.Fatalf("tools/list status=%d body=%s", listResponse.StatusCode, body)
	}
	listPayload := decodeRPCResponse(t, listResponse)
	result := listPayload["result"].(map[string]any)
	if result["resultType"] != "complete" || result["cacheScope"] != "private" || result["ttlMs"].(float64) != 0 {
		t.Fatalf("tools/list contract=%#v", result)
	}
	tools := result["tools"].([]any)
	if len(tools) != 13 {
		t.Fatalf("tool count=%d", len(tools))
	}

	callResponse := fixture.post("tools/call", "ticket_list", map[string]any{
		"name":      "ticket_list",
		"arguments": map[string]any{},
	})
	if callResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(callResponse.Body)
		t.Fatalf("tools/call status=%d body=%s", callResponse.StatusCode, body)
	}
	callPayload := decodeRPCResponse(t, callResponse)
	callResult := callPayload["result"].(map[string]any)
	if callResult["resultType"] != "complete" || callResult["isError"] == true {
		t.Fatalf("tool result=%#v", callResult)
	}
	structured := callResult["structuredContent"].(map[string]any)
	if structured["ok"] != true {
		t.Fatalf("structured result=%#v", structured)
	}
	if fixture.backend.callCount() != 1 {
		t.Fatalf("backend calls=%d", fixture.backend.callCount())
	}
	fixture.backend.mu.Lock()
	hasCtx := fixture.backend.calls[0].hasCtx
	fixture.backend.mu.Unlock()
	if !hasCtx {
		t.Fatal("backend context did not contain MCP principal")
	}
}

func TestToolBackendErrorsRemainStructuredResults(t *testing.T) {
	fixture := newTestFixture(t, []string{"*"})
	fixture.backend.callErr = &BackendError{
		Code:      "version_conflict",
		Message:   "ticket version changed",
		Retryable: true,
	}
	response := fixture.post("tools/call", "ticket_list", map[string]any{
		"name":      "ticket_list",
		"arguments": map[string]any{},
	})
	payload := decodeRPCResponse(t, response)
	result := payload["result"].(map[string]any)
	if result["resultType"] != "complete" || result["isError"] != true {
		t.Fatalf("tool error result=%#v", result)
	}
	structured := result["structuredContent"].(map[string]any)
	failure := structured["error"].(map[string]any)
	if failure["code"] != "version_conflict" || failure["retryable"] != true {
		t.Fatalf("structured failure=%#v", failure)
	}
}

func TestInsufficientScopeReturnsOAuthChallengeBeforeBackend(t *testing.T) {
	fixture := newTestFixture(
		t,
		[]string{ScopeTicketsRead},
		WithResourceMetadataURL("https://chronodesk.example/.well-known/oauth-protected-resource/mcp"),
	)
	requestTypeVersionID := uuid.Must(uuid.NewV7()).String()
	workflowVersionID := uuid.Must(uuid.NewV7()).String()
	response := fixture.post("tools/call", "ticket_create", map[string]any{
		"name": "ticket_create",
		"arguments": map[string]any{
			"title":                   "Denied",
			"description":             "Denied",
			"type":                    "request",
			"priority":                "normal",
			"request_type_version_id": requestTypeVersionID,
			"workflow_version_id":     workflowVersionID,
			"idempotency_key":         "denied-create-0001",
		},
	})
	if response.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	challenge := response.Header.Get("WWW-Authenticate")
	if !strings.Contains(challenge, `error="insufficient_scope"`) ||
		!strings.Contains(challenge, ScopeTicketsCreate) ||
		!strings.Contains(challenge, "resource_metadata=") {
		t.Fatalf("WWW-Authenticate=%q", challenge)
	}
	if code := rpcErrorCode(t, response); code != -32600 {
		t.Fatalf("error code=%v", code)
	}
	if fixture.backend.callCount() != 0 {
		t.Fatalf("backend calls=%d", fixture.backend.callCount())
	}
}

func TestPolicyDenialReturnsHTTP403BeforeBackend(t *testing.T) {
	fixture := newTestFixture(
		t,
		[]string{"*"},
		WithAuthorizer(AuthorizerFunc(func(_ context.Context, _ Principal, request AuthorizationRequest) error {
			if request.Action == "ticket_create" {
				return &PolicyError{DecisionID: "decision-denied", ReasonCode: "read_only_mode"}
			}
			return nil
		})),
	)
	requestTypeVersionID := uuid.Must(uuid.NewV7()).String()
	workflowVersionID := uuid.Must(uuid.NewV7()).String()
	response := fixture.post("tools/call", "ticket_create", map[string]any{
		"name": "ticket_create",
		"arguments": map[string]any{
			"title":                   "Denied",
			"description":             "Denied",
			"type":                    "request",
			"priority":                "normal",
			"request_type_version_id": requestTypeVersionID,
			"workflow_version_id":     workflowVersionID,
			"idempotency_key":         "policy-denied-0001",
		},
	})
	if response.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	payload := decodeRPCResponse(t, response)
	rpcError := payload["error"].(map[string]any)
	data := rpcError["data"].(map[string]any)
	if data["code"] != "policy_denied" ||
		data["policy_decision_id"] != "decision-denied" ||
		data["reason_code"] != "read_only_mode" {
		t.Fatalf("policy denial=%#v", data)
	}
	if fixture.backend.callCount() != 0 {
		t.Fatalf("backend calls=%d", fixture.backend.callCount())
	}
}

func TestResourcesUseModernCacheAndTrustContracts(t *testing.T) {
	fixture := newTestFixture(t, []string{"*"})
	staticResponse := fixture.post("resources/read", resourceTicketSchema, map[string]any{
		"uri": resourceTicketSchema,
	})
	staticResult := decodeRPCResponse(t, staticResponse)["result"].(map[string]any)
	if staticResult["resultType"] != "complete" || staticResult["cacheScope"] != "public" ||
		staticResult["ttlMs"].(float64) != 300000 {
		t.Fatalf("static resource result=%#v", staticResult)
	}

	uri := "ticket://projects/TEST/tickets/42"
	dynamicResponse := fixture.post("resources/read", uri, map[string]any{"uri": uri})
	dynamicResult := decodeRPCResponse(t, dynamicResponse)["result"].(map[string]any)
	if dynamicResult["resultType"] != "complete" || dynamicResult["cacheScope"] != "private" ||
		dynamicResult["ttlMs"].(float64) != 0 {
		t.Fatalf("dynamic resource result=%#v", dynamicResult)
	}
	content := dynamicResult["contents"].([]any)[0].(map[string]any)
	meta := content["_meta"].(map[string]any)
	if meta["com.chronodesk/trust"] != "untrusted" {
		t.Fatalf("resource trust metadata=%#v", meta)
	}

	rejected := fixture.post("resources/read", "https://attacker.example/ticket/42", map[string]any{
		"uri": "https://attacker.example/ticket/42",
	})
	if rejected.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(rejected.Body)
		t.Fatalf("external resource status=%d body=%s", rejected.StatusCode, body)
	}
	if code := rpcErrorCode(t, rejected); code != -32602 {
		t.Fatalf("external resource code=%v", code)
	}
}

func TestSubscriptionsListenAcknowledgesAndPublishes(t *testing.T) {
	fixture := newTestFixture(t, []string{"*"}, WithCredentialRecheckInterval(20*time.Millisecond))
	uri := "ticket://projects/TEST/tickets/42"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      "listen-1",
		"method":  "subscriptions/listen",
		"params": map[string]any{
			"_meta": modernMeta(),
			"notifications": map[string]any{
				"resourceSubscriptions": []string{uri},
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, fixture.http.URL+"/mcp", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer valid-token")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(HeaderProtocolVersion, ProtocolVersion)
	request.Header.Set(HeaderMethod, "subscriptions/listen")

	response, err := fixture.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("listen status=%d body=%s", response.StatusCode, data)
	}
	if response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("listen content type=%q", response.Header.Get("Content-Type"))
	}
	if response.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("X-Accel-Buffering=%q", response.Header.Get("X-Accel-Buffering"))
	}

	messages := make(chan map[string]any, 4)
	go func() {
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var message map[string]any
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &message) == nil {
				messages <- message
			}
		}
		close(messages)
	}()

	ack := waitSSEMessage(t, messages)
	if ack["method"] != "notifications/subscriptions/acknowledged" {
		t.Fatalf("first subscription message=%#v", ack)
	}
	ackParams := ack["params"].(map[string]any)
	ackMeta := ackParams["_meta"].(map[string]any)
	if ackMeta["io.modelcontextprotocol/subscriptionId"] != "listen-1" {
		t.Fatalf("ack metadata=%#v", ackMeta)
	}

	fixture.server.Publish(ResourceEvent{URI: uri})
	updated := waitSSEMessage(t, messages)
	if updated["method"] != "notifications/resources/updated" {
		t.Fatalf("updated message=%#v", updated)
	}
	updatedMeta := updated["params"].(map[string]any)["_meta"].(map[string]any)
	if updatedMeta["io.modelcontextprotocol/subscriptionId"] != "listen-1" {
		t.Fatalf("updated metadata=%#v", updatedMeta)
	}

	fixture.backend.mu.Lock()
	fixture.backend.tokenValid = false
	fixture.backend.mu.Unlock()
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case _, ok := <-messages:
			if !ok {
				return
			}
		case <-timeout.C:
			t.Fatal("subscription did not close after credential revocation")
		}
	}
}

func TestSubscriptionStreamCredentialLimitAndRelease(t *testing.T) {
	fixture := newTestFixture(
		t,
		[]string{"*"},
		WithCredentialRecheckInterval(time.Hour),
		WithSubscriptionStreamLimits(2, 2, 1),
	)
	uri := "ticket://projects/TEST/tickets/42"
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	first, err := openSubscription(firstCtx, fixture.http.Client(), fixture.http.URL+"/mcp", "valid-token", "listen-1", []string{uri})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Body.Close()
	if first.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(first.Body)
		t.Fatalf("first subscription status=%d body=%s", first.StatusCode, body)
	}
	firstMessages := scanSSE(first)
	if message := waitSSEMessage(t, firstMessages); message["method"] != "notifications/subscriptions/acknowledged" {
		t.Fatalf("first subscription message=%#v", message)
	}

	second, err := openSubscription(context.Background(), fixture.http.Client(), fixture.http.URL+"/mcp", "valid-token", "listen-2", []string{uri})
	if err != nil {
		t.Fatal(err)
	}
	if second.StatusCode != http.StatusTooManyRequests {
		body, _ := io.ReadAll(second.Body)
		second.Body.Close()
		t.Fatalf("second subscription status=%d body=%s", second.StatusCode, body)
	}
	payload := decodeRPCResponse(t, second)
	failure := payload["error"].(map[string]any)["data"].(map[string]any)
	if failure["code"] != "subscription_limit_exceeded" ||
		failure["limit_scope"] != "credential" ||
		failure["limit"].(float64) != 1 {
		t.Fatalf("subscription limit failure=%#v", failure)
	}

	cancelFirst()
	first.Body.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		fixture.server.streamsMu.Lock()
		active := fixture.server.streams.total
		fixture.server.streamsMu.Unlock()
		if active == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stream slot was not released; active=%d", active)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSubscriptionStreamGlobalLimitIsAtomic(t *testing.T) {
	fixture := newTestFixture(
		t,
		[]string{"*"},
		WithSubscriptionStreamLimits(4, 4, 4),
	)
	const contenders = 32
	start := make(chan struct{})
	releaseGate := make(chan struct{})
	results := make(chan bool, contenders)
	var workers sync.WaitGroup
	for index := range contenders {
		index := index
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			principal := testPrincipal(
				fmt.Sprintf("agent-%d", index),
				fmt.Sprintf("credential-%d", index),
				[]string{"*"},
			)
			release, denial := fixture.server.acquireSubscriptionStream(principal, fmt.Sprintf("token-%d", index))
			results <- denial == nil
			if denial == nil {
				<-releaseGate
				release()
			}
		}()
	}
	close(start)
	acquired := 0
	for range contenders {
		if <-results {
			acquired++
		}
	}
	if acquired != 4 {
		t.Fatalf("concurrent acquired streams=%d, want 4", acquired)
	}
	close(releaseGate)
	workers.Wait()

	fixture.server.streamsMu.Lock()
	defer fixture.server.streamsMu.Unlock()
	if fixture.server.streams.total != 0 ||
		len(fixture.server.streams.principals) != 0 ||
		len(fixture.server.streams.credentials) != 0 {
		t.Fatalf("stream counters leaked: %#v", fixture.server.streams)
	}
}

func TestSubscriptionResourceListLimitAndDuplicates(t *testing.T) {
	fixture := newTestFixture(t, []string{"*"}, WithMaxSubscriptionResources(2))
	cases := []struct {
		name  string
		uris  []string
		code  string
		limit float64
	}{
		{
			name:  "over limit",
			uris:  []string{"ticket://projects/TEST/tickets/1", "ticket://projects/TEST/tickets/2", "ticket://projects/TEST/tickets/3"},
			code:  "subscription_resource_limit_exceeded",
			limit: 2,
		},
		{
			name: "duplicate",
			uris: []string{"ticket://projects/TEST/tickets/1", "ticket://projects/TEST/tickets/1"},
			code: "duplicate_subscription_resource",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			response := fixture.post("subscriptions/listen", "", map[string]any{
				"notifications": map[string]any{"resourceSubscriptions": test.uris},
			})
			if response.StatusCode != http.StatusBadRequest {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("status=%d body=%s", response.StatusCode, body)
			}
			payload := decodeRPCResponse(t, response)
			rpcFailure := payload["error"].(map[string]any)
			if rpcFailure["code"].(float64) != -32602 {
				t.Fatalf("rpc failure=%#v", rpcFailure)
			}
			data := rpcFailure["data"].(map[string]any)
			if data["code"] != test.code {
				t.Fatalf("failure data=%#v", data)
			}
			if test.limit != 0 && data["limit"].(float64) != test.limit {
				t.Fatalf("failure limit=%#v", data)
			}
		})
	}
}

func TestSubscriptionDeliveryQueueIsBoundedAndCoalesces(t *testing.T) {
	delivery := &subscriptionDelivery{
		updates: make(chan string, 2),
		done:    make(chan struct{}),
		pending: make(map[string]struct{}),
	}
	for i := 0; i < 100; i++ {
		delivery.enqueue("ticket://projects/TEST/tickets/1")
	}
	delivery.enqueue("ticket://projects/TEST/tickets/2")
	delivery.enqueue("ticket://projects/TEST/tickets/3")
	if got := len(delivery.updates); got != 2 {
		t.Fatalf("bounded delivery queue length=%d, want 2", got)
	}
	if len(delivery.pending) != 2 {
		t.Fatalf("pending URI count=%d, want 2", len(delivery.pending))
	}
	first := <-delivery.updates
	delivery.complete(first)
	delivery.enqueue("ticket://projects/TEST/tickets/3")
	if got := len(delivery.updates); got != 2 {
		t.Fatalf("delivery queue did not accept URI after capacity released: %d", got)
	}
	if _, ok := delivery.pending["ticket://projects/TEST/tickets/3"]; !ok {
		t.Fatal("new URI was not tracked after capacity released")
	}
}

func TestCredentialWatchIsSharedAndScopeReductionClosesStream(t *testing.T) {
	var reduced atomic.Bool
	var revalidations atomic.Int32
	principal := func(scopes []string) Principal {
		return testPrincipal("agent-shared", "credential-shared", scopes)
	}
	authenticator := &testAuthenticator{
		authenticate: func(_ context.Context, token string) (Principal, error) {
			if token != "shared-token" {
				return Principal{}, errors.New("invalid token")
			}
			return principal([]string{ScopeTicketsRead, ScopeEventsSubscribe}), nil
		},
		revalidate: func(_ context.Context, token string) (Principal, error) {
			revalidations.Add(1)
			if token != "shared-token" {
				return Principal{}, errors.New("invalid token")
			}
			if reduced.Load() {
				return principal([]string{ScopeTicketsRead}), nil
			}
			return principal([]string{ScopeTicketsRead, ScopeEventsSubscribe}), nil
		},
	}
	backend := &fakeBackend{tokenValid: true}
	server, err := NewServer(
		backend,
		authenticator,
		WithCredentialRecheckInterval(20*time.Millisecond),
		WithSubscriptionStreamLimits(4, 4, 2),
	)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Any("/mcp", server.Handler())
	httpServer := httptest.NewServer(router)
	defer func() {
		_ = server.Close()
		httpServer.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	response, err := openSubscription(ctx, httpServer.Client(), httpServer.URL+"/mcp", "shared-token", "scope-listen", []string{"ticket://projects/TEST/tickets/42"})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	messages := scanSSE(response)
	if message := waitSSEMessage(t, messages); message["method"] != "notifications/subscriptions/acknowledged" {
		t.Fatalf("subscription message=%#v", message)
	}

	reduced.Store(true)
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case _, ok := <-messages:
			if !ok {
				if revalidations.Load() == 0 {
					t.Fatal("subscription closed without credential revalidation")
				}
				return
			}
		case <-timeout.C:
			t.Fatal("subscription did not close after events:subscribe scope was removed")
		}
	}
}

func TestCredentialWatchDeduplicatesConcurrentStreams(t *testing.T) {
	started := make(chan struct{}, 2)
	unblock := make(chan struct{})
	var active atomic.Int32
	var maxActive atomic.Int32
	principal := testPrincipal("agent-1", "credential-1", []string{"*"})
	authenticator := &testAuthenticator{
		authenticate: func(context.Context, string) (Principal, error) { return principal, nil },
		revalidate: func(context.Context, string) (Principal, error) {
			current := active.Add(1)
			for {
				maximum := maxActive.Load()
				if current <= maximum || maxActive.CompareAndSwap(maximum, current) {
					break
				}
			}
			started <- struct{}{}
			<-unblock
			active.Add(-1)
			return principal, nil
		},
	}
	server, err := NewServer(&fakeBackend{}, authenticator, WithCredentialRecheckInterval(5*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	cancel1 := func() {}
	cancel2 := func() {}
	unregister1 := server.watchCredential("same-token", principal, cancel1)
	unregister2 := server.watchCredential("same-token", principal, cancel2)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("credential revalidation did not start")
	}
	select {
	case <-started:
		t.Fatal("same credential started more than one concurrent revalidation")
	case <-time.After(50 * time.Millisecond):
	}
	if maxActive.Load() != 1 {
		t.Fatalf("max concurrent revalidations=%d", maxActive.Load())
	}
	unregister1()
	unregister2()
	close(unblock)
}

func TestPublishRevokesBeforeNotifyingAuthorizedSubscriber(t *testing.T) {
	var denyRevoked atomic.Bool
	revokedCheckStarted := make(chan struct{})
	releaseRevokedCheck := make(chan struct{})
	var revokedStartOnce sync.Once
	authenticator := &testAuthenticator{
		authenticate: func(_ context.Context, token string) (Principal, error) {
			switch token {
			case "valid-token":
				return testPrincipal("agent-valid", "credential-valid", []string{"*"}), nil
			case "revoked-token":
				return testPrincipal("agent-revoked", "credential-revoked", []string{"*"}), nil
			default:
				return Principal{}, errors.New("invalid token")
			}
		},
	}
	authenticator.revalidate = authenticator.authenticate
	authorizer := AuthorizerFunc(func(_ context.Context, principal Principal, request AuthorizationRequest) error {
		if denyRevoked.Load() &&
			principal.CredentialID == "credential-revoked" &&
			request.Action == "resource:subscribe" &&
			request.ResourceURI == "ticket://projects/TEST/tickets/42" {
			return &PolicyError{ReasonCode: "object_access_revoked"}
		}
		return nil
	})
	backend := &fakeBackend{
		subscribe: func(principal Principal, uri string) (bool, error) {
			if denyRevoked.Load() &&
				principal.CredentialID == "credential-revoked" &&
				uri == "ticket://projects/TEST/tickets/42" {
				revokedStartOnce.Do(func() { close(revokedCheckStarted) })
				<-releaseRevokedCheck
				return false, nil
			}
			return true, nil
		},
	}
	server, err := NewServer(
		backend,
		authenticator,
		WithAuthorizer(authorizer),
		WithCredentialRecheckInterval(time.Hour),
		WithSubscriptionStreamLimits(4, 4, 2),
	)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Any("/mcp", server.Handler())
	httpServer := httptest.NewServer(router)
	defer func() {
		_ = server.Close()
		httpServer.Close()
	}()

	uri := "ticket://projects/TEST/tickets/42"
	validCtx, cancelValid := context.WithCancel(context.Background())
	defer cancelValid()
	revokedCtx, cancelRevoked := context.WithCancel(context.Background())
	defer cancelRevoked()
	validResponse, err := openSubscription(validCtx, httpServer.Client(), httpServer.URL+"/mcp", "valid-token", "valid-listen", []string{uri})
	if err != nil {
		t.Fatal(err)
	}
	defer validResponse.Body.Close()
	revokedResponse, err := openSubscription(revokedCtx, httpServer.Client(), httpServer.URL+"/mcp", "revoked-token", "revoked-listen", []string{uri})
	if err != nil {
		t.Fatal(err)
	}
	defer revokedResponse.Body.Close()
	validMessages := scanSSE(validResponse)
	revokedMessages := scanSSE(revokedResponse)
	if message := waitSSEMessage(t, validMessages); message["method"] != "notifications/subscriptions/acknowledged" {
		t.Fatalf("valid ack=%#v", message)
	}
	if message := waitSSEMessage(t, revokedMessages); message["method"] != "notifications/subscriptions/acknowledged" {
		t.Fatalf("revoked ack=%#v", message)
	}

	denyRevoked.Store(true)
	started := time.Now()
	server.Publish(ResourceEvent{URI: uri})
	if elapsed := time.Since(started); elapsed > 50*time.Millisecond {
		t.Fatalf("Publish blocked for %s", elapsed)
	}
	select {
	case <-revokedCheckStarted:
	case <-time.After(time.Second):
		t.Fatal("revoked subscriber was not revalidated")
	}

	validUpdate := waitSSEMessage(t, validMessages)
	if validUpdate["method"] != "notifications/resources/updated" {
		t.Fatalf("valid update=%#v", validUpdate)
	}
	close(releaseRevokedCheck)
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case message, ok := <-revokedMessages:
			if !ok {
				return
			}
			if message["method"] == "notifications/resources/updated" {
				t.Fatalf("revoked subscriber received update=%#v", message)
			}
		case <-timeout.C:
			t.Fatal("revoked subscription was not closed")
		}
	}
}

func TestPublishTransientAuthorizationFailureKeepsSubscription(t *testing.T) {
	fixture := newTestFixture(
		t,
		[]string{"*"},
		WithCredentialRecheckInterval(time.Hour),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	uri := "ticket://projects/TEST/tickets/42"
	response, err := openSubscription(
		ctx,
		fixture.http.Client(),
		fixture.http.URL+"/mcp",
		"valid-token",
		"transient-listen",
		[]string{uri},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	messages := scanSSE(response)
	if message := waitSSEMessage(t, messages); message["method"] != "notifications/subscriptions/acknowledged" {
		t.Fatalf("ack=%#v", message)
	}

	fixture.backend.mu.Lock()
	fixture.backend.subscribeErr = errors.New("temporary database outage")
	fixture.backend.mu.Unlock()
	fixture.server.Publish(ResourceEvent{URI: uri})
	select {
	case message, ok := <-messages:
		if !ok {
			t.Fatal("transient authorization error closed the subscription")
		}
		t.Fatalf("transient authorization error emitted message=%#v", message)
	case <-time.After(200 * time.Millisecond):
	}

	fixture.backend.mu.Lock()
	fixture.backend.subscribeErr = nil
	fixture.backend.mu.Unlock()
	fixture.server.Publish(ResourceEvent{URI: uri})
	update := waitSSEMessage(t, messages)
	if update["method"] != "notifications/resources/updated" {
		t.Fatalf("update after transient recovery=%#v", update)
	}
}

func waitSSEMessage(t *testing.T, messages <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case message, ok := <-messages:
		if !ok {
			t.Fatal("SSE stream closed before message")
		}
		return message
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for SSE message")
		return nil
	}
}

func TestAuthenticationAndOriginAreRequired(t *testing.T) {
	fixture := newTestFixture(t, []string{"*"})
	body := `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`
	request, err := http.NewRequest(http.MethodPost, fixture.http.URL+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(HeaderProtocolVersion, ProtocolVersion)
	request.Header.Set(HeaderMethod, "server/discover")
	response, err := fixture.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", response.StatusCode)
	}

	request, err = http.NewRequest(http.MethodPost, fixture.http.URL+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer valid-token")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(HeaderProtocolVersion, ProtocolVersion)
	request.Header.Set(HeaderMethod, "server/discover")
	request.Header.Set("Origin", "https://attacker.example")
	response, err = fixture.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("forbidden origin status=%d", response.StatusCode)
	}
}

func TestNewServerRequiresDependencies(t *testing.T) {
	authFn := func(context.Context, string) (Principal, error) { return Principal{}, nil }
	auth := &testAuthenticator{authenticate: authFn, revalidate: authFn}
	if _, err := NewServer(nil, auth); err == nil {
		t.Fatal("NewServer(nil backend) succeeded")
	}
	if _, err := NewServer(&fakeBackend{}, nil); err == nil {
		t.Fatal("NewServer(nil authenticator) succeeded")
	}
	if _, err := NewServer(&fakeBackend{}, auth, WithAllowedOrigins("*")); err == nil {
		t.Fatal("WithAllowedOrigins wildcard succeeded")
	}
	if _, err := NewServer(&fakeBackend{}, auth, WithResourceMetadataURL("/relative")); err == nil {
		t.Fatal("WithResourceMetadataURL(relative) succeeded")
	}
	if _, err := NewServer(&fakeBackend{}, auth, WithSubscriptionStreamLimits(1, 2, 1)); err == nil {
		t.Fatal("WithSubscriptionStreamLimits(invalid ordering) succeeded")
	}
	if _, err := NewServer(&fakeBackend{}, auth, WithMaxSubscriptionResources(0)); err == nil {
		t.Fatal("WithMaxSubscriptionResources(0) succeeded")
	}
}

func TestLegacyScopeAliasWasRemoved(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	source, err := os.ReadFile(filepath.Join(filepath.Dir(currentFile), "tools.go"))
	if err != nil {
		t.Fatal(err)
	}
	legacyAlias := "ScopeTickets" + "Transit"
	legacyDeclaration := regexp.MustCompile(`\b` + legacyAlias + `\b\s*=`)
	if legacyDeclaration.Match(source) {
		t.Fatalf("legacy compatibility alias %q remains in tools.go", legacyAlias)
	}
}
