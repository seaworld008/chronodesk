package agentauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	testIssuer      = "https://auth.example"
	testMCPResource = "https://api.example/mcp"
	testAPIResource = "https://api.example/api/v2"
	testA2AResource = "https://api.example/a2a/v1"
)

type testCredentialStore struct {
	principal *Principal
	err       error
	authCalls int
	projects  []string
}

func (s *testCredentialStore) AuthenticateClient(
	_ context.Context,
	_, _ string,
	projectKey string,
) (*Principal, error) {
	s.authCalls++
	s.projects = append(s.projects, projectKey)
	return s.principal, s.err
}

func (*testCredentialStore) TouchCredential(context.Context, string, time.Time) error {
	return nil
}

func newOAuthTestRouter(store CredentialStore, options ...HandlerOption) (*gin.Engine, map[string]*Manager) {
	gin.SetMode(gin.TestMode)
	managers := make(map[string]*Manager)
	for _, resource := range []string{testMCPResource, testAPIResource, testA2AResource} {
		manager := NewManager("oauth-handler-test-secret", testIssuer, resource, 10*time.Minute)
		manager.now = func() time.Time {
			return time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
		}
		managers[resource] = manager
	}
	handler := NewHandler(
		store,
		testIssuer,
		[]ProtectedResource{
			{Name: "ChronoDesk MCP", Manager: managers[testMCPResource]},
			{Name: "ChronoDesk Agent REST API", Manager: managers[testAPIResource]},
			{Name: "ChronoDesk A2A", Manager: managers[testA2AResource]},
		},
		options...,
	)
	router := gin.New()
	if err := router.SetTrustedProxies(nil); err != nil {
		panic(err)
	}
	handler.RegisterPublicRoutes(router)
	return router, managers
}

func TestDiscoveryMetadataUsesThreeCanonicalResourcesAndLatestFields(t *testing.T) {
	router, _ := newOAuthTestRouter(nil)

	nonCanonicalResponse := httptest.NewRecorder()
	router.ServeHTTP(nonCanonicalResponse, httptest.NewRequest(
		http.MethodGet,
		"/.well-known/oauth-protected-resource",
		nil,
	))
	if nonCanonicalResponse.Code != http.StatusNotFound {
		t.Fatalf("non-canonical protected resource alias status = %d", nonCanonicalResponse.Code)
	}

	for _, test := range []struct {
		path     string
		resource string
		name     string
	}{
		{path: "/.well-known/oauth-protected-resource/mcp", resource: testMCPResource, name: "ChronoDesk MCP"},
		{path: "/.well-known/oauth-protected-resource/api/v2", resource: testAPIResource, name: "ChronoDesk Agent REST API"},
		{path: "/.well-known/oauth-protected-resource/a2a/v1", resource: testA2AResource, name: "ChronoDesk A2A"},
	} {
		t.Run(test.name, func(t *testing.T) {
			resourceResponse := httptest.NewRecorder()
			router.ServeHTTP(resourceResponse, httptest.NewRequest(http.MethodGet, test.path, nil))
			if resourceResponse.Code != http.StatusOK {
				t.Fatalf("protected resource metadata status = %d, body = %s", resourceResponse.Code, resourceResponse.Body.String())
			}
			if got := resourceResponse.Header().Get("Cache-Control"); got != "public, max-age=3600" {
				t.Fatalf("protected resource metadata Cache-Control = %q", got)
			}
			var resourceMetadata map[string]any
			if err := json.Unmarshal(resourceResponse.Body.Bytes(), &resourceMetadata); err != nil {
				t.Fatal(err)
			}
			if got := resourceMetadata["resource"]; got != test.resource {
				t.Fatalf("resource = %#v, want %q", got, test.resource)
			}
			servers, ok := resourceMetadata["authorization_servers"].([]any)
			if !ok || len(servers) != 1 || servers[0] != testIssuer {
				t.Fatalf("authorization_servers = %#v", resourceMetadata["authorization_servers"])
			}
			if got := resourceMetadata["resource_name"]; got != test.name {
				t.Fatalf("resource_name = %#v", got)
			}
		})
	}

	authorizationResponse := httptest.NewRecorder()
	router.ServeHTTP(authorizationResponse, httptest.NewRequest(
		http.MethodGet,
		"/.well-known/oauth-authorization-server",
		nil,
	))
	if authorizationResponse.Code != http.StatusOK {
		t.Fatalf("authorization metadata status = %d, body = %s", authorizationResponse.Code, authorizationResponse.Body.String())
	}
	if got := authorizationResponse.Header().Get("Cache-Control"); got != "public, max-age=3600" {
		t.Fatalf("authorization metadata Cache-Control = %q", got)
	}
	var authorizationMetadata map[string]any
	if err := json.Unmarshal(authorizationResponse.Body.Bytes(), &authorizationMetadata); err != nil {
		t.Fatal(err)
	}
	if got := authorizationMetadata["issuer"]; got != testIssuer {
		t.Fatalf("issuer = %#v", got)
	}
	if got := authorizationMetadata["token_endpoint"]; got != testIssuer+"/oauth/token" {
		t.Fatalf("token_endpoint = %#v", got)
	}
	if got, exists := authorizationMetadata["client_id_metadata_document_supported"]; !exists || got != false {
		t.Fatalf("client_id_metadata_document_supported = %#v, exists = %v", got, exists)
	}
	if got, exists := authorizationMetadata["authorization_response_iss_parameter_supported"]; !exists || got != false {
		t.Fatalf("authorization_response_iss_parameter_supported = %#v, exists = %v", got, exists)
	}
	if got, exists := authorizationMetadata["project_key_parameter_supported"]; !exists || got != true {
		t.Fatalf("project_key_parameter_supported = %#v, exists = %v", got, exists)
	}
}

func TestTokenRequiresExactRFC8707Resource(t *testing.T) {
	store := &testCredentialStore{principal: &Principal{
		ID:           "principal-1",
		CredentialID: "credential-1",
		ClientID:     "client-1",
		Name:         "ticket-agent",
		Scopes:       []string{"tickets:read"},
		Active:       true,
	}}
	router, managers := newOAuthTestRouter(store)

	for _, resource := range []string{testMCPResource, testAPIResource, testA2AResource} {
		t.Run(resource, func(t *testing.T) {
			form := url.Values{
				"grant_type":  {"client_credentials"},
				"project_key": {"TEST"},
				"scope":       {"tickets:read"},
				"resource":    {resource},
			}
			request := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.SetBasicAuth("client-1", "secret")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if got := response.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q", got)
			}
			if got := response.Header().Get("Pragma"); got != "no-cache" {
				t.Fatalf("Pragma = %q", got)
			}
			var payload map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if got := payload["resource"]; got != resource {
				t.Fatalf("resource = %#v, want %q", got, resource)
			}
			if got := payload["project_key"]; got != "TEST" {
				t.Fatalf("project_key = %#v, want TEST", got)
			}
			token, _ := payload["access_token"].(string)
			if token == "" {
				t.Fatal("access_token is empty")
			}
			access, err := managers[resource].Verify(token)
			if err != nil {
				t.Fatalf("issued token failed verification: %v", err)
			}
			if access.ProjectKey != "TEST" {
				t.Fatalf("verified project_key = %q, want TEST", access.ProjectKey)
			}
		})
	}

	for _, test := range []struct {
		name      string
		resources []string
	}{
		{name: "missing"},
		{name: "unknown endpoint", resources: []string{"https://api.example/api/v3"}},
		{name: "trailing slash differs", resources: []string{testMCPResource + "/"}},
		{name: "multiple targets", resources: []string{testMCPResource, "https://api.example/other"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			values := url.Values{
				"grant_type":  {"client_credentials"},
				"project_key": {"TEST"},
			}
			for _, resource := range test.resources {
				values.Add("resource", resource)
			}
			request := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(values.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.SetBasicAuth("client-1", "secret")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			assertOAuthError(t, response, http.StatusBadRequest, "invalid_target")
		})
	}
	if store.authCalls != 3 {
		t.Fatalf("AuthenticateClient calls = %d, want 3; invalid resources must fail before authentication", store.authCalls)
	}
	for _, projectKey := range store.projects {
		if projectKey != "TEST" {
			t.Fatalf("AuthenticateClient project_key = %q, want TEST", projectKey)
		}
	}
}

func TestTokenRequiresExactlyOneProjectKeyBeforeAuthentication(t *testing.T) {
	store := &testCredentialStore{principal: &Principal{
		ID: "principal-1", CredentialID: "credential-1", ClientID: "client-1",
		Name: "agent", Scopes: []string{"tickets:read"}, Active: true,
	}}
	router, _ := newOAuthTestRouter(store)

	tests := []struct {
		name        string
		projectKeys []string
	}{
		{name: "missing"},
		{name: "empty", projectKeys: []string{""}},
		{name: "duplicate", projectKeys: []string{"TEST", "OTHER"}},
		{name: "invalid", projectKeys: []string{"lowercase"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			form := url.Values{
				"grant_type": {"client_credentials"},
				"resource":   {testMCPResource},
			}
			for _, projectKey := range test.projectKeys {
				form.Add("project_key", projectKey)
			}
			request := httptest.NewRequest(
				http.MethodPost,
				"/oauth/token",
				strings.NewReader(form.Encode()),
			)
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.SetBasicAuth("client-1", "secret")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			assertOAuthError(t, response, http.StatusBadRequest, "invalid_request")
		})
	}
	if store.authCalls != 0 {
		t.Fatalf(
			"AuthenticateClient calls = %d, want 0 for invalid project_key requests",
			store.authCalls,
		)
	}
}

func TestTokenRejectsLegacyJSONAndMultipleClientAuthMethods(t *testing.T) {
	store := &testCredentialStore{}
	router, _ := newOAuthTestRouter(store)

	jsonRequest := httptest.NewRequest(
		http.MethodPost,
		"/oauth/token",
		strings.NewReader(`{"grant_type":"client_credentials","resource":"https://api.example/mcp"}`),
	)
	jsonRequest.Header.Set("Content-Type", "application/json")
	jsonResponse := httptest.NewRecorder()
	router.ServeHTTP(jsonResponse, jsonRequest)
	assertOAuthError(t, jsonResponse, http.StatusBadRequest, "invalid_request")

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {"client-1"},
		"client_secret": {"secret"},
		"project_key":   {"TEST"},
		"resource":      {testMCPResource},
	}
	duplicateRequest := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	duplicateRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	duplicateRequest.SetBasicAuth("client-1", "secret")
	duplicateResponse := httptest.NewRecorder()
	router.ServeHTTP(duplicateResponse, duplicateRequest)
	assertOAuthError(t, duplicateResponse, http.StatusBadRequest, "invalid_request")

	if store.authCalls != 0 {
		t.Fatalf("AuthenticateClient calls = %d, want 0", store.authCalls)
	}
}

func TestTokenInvalidClientChallengeAndErrorCacheHeaders(t *testing.T) {
	store := &testCredentialStore{err: errors.New("not found")}
	router, _ := newOAuthTestRouter(store)
	form := url.Values{
		"grant_type":  {"client_credentials"},
		"project_key": {"TEST"},
		"resource":    {testMCPResource},
	}
	request := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetBasicAuth("unknown", "wrong")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assertOAuthError(t, response, http.StatusUnauthorized, "invalid_client")
	if got := response.Header().Get("WWW-Authenticate"); got != `Basic realm="chronodesk-oauth"` {
		t.Fatalf("WWW-Authenticate = %q", got)
	}
}

func TestBearerChallengesAdvertiseProtectedResourceMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := NewManager("challenge-test-secret", testIssuer, testMCPResource, time.Minute)
	router := gin.New()
	router.GET("/protected", manager.Middleware("tickets:read"), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/protected", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	challenge := response.Header().Get("WWW-Authenticate")
	for _, expected := range []string{
		`Bearer realm="chronodesk-agent"`,
		`resource_metadata="https://api.example/.well-known/oauth-protected-resource/mcp"`,
		`scope="tickets:read"`,
	} {
		if !strings.Contains(challenge, expected) {
			t.Fatalf("WWW-Authenticate = %q, missing %q", challenge, expected)
		}
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestTokenEndpointHasIndependentAnonymousRateLimit(t *testing.T) {
	router, _ := newOAuthTestRouter(
		&testCredentialStore{err: errors.New("not found")},
		WithTokenRateLimit(2, time.Minute),
	)
	for attempt := 1; attempt <= 3; attempt++ {
		form := url.Values{
			"grant_type":  {"client_credentials"},
			"project_key": {"TEST"},
			"resource":    {testMCPResource},
		}
		request := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
		request.RemoteAddr = "192.0.2.10:43210"
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.SetBasicAuth("unknown", "wrong")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if attempt < 3 {
			assertOAuthError(t, response, http.StatusUnauthorized, "invalid_client")
			continue
		}
		assertOAuthError(t, response, http.StatusTooManyRequests, "temporarily_unavailable")
		if response.Header().Get("Retry-After") == "" {
			t.Fatal("rate-limited token response has no Retry-After")
		}
	}

	metadata := httptest.NewRecorder()
	router.ServeHTTP(metadata, httptest.NewRequest(
		http.MethodGet,
		"/.well-known/oauth-protected-resource/mcp",
		nil,
	))
	if metadata.Code != http.StatusOK {
		t.Fatalf("metadata was affected by token limiter: status=%d", metadata.Code)
	}
}

func assertOAuthError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d, body = %s", response.Code, status, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if got := payload["error"]; got != code {
		t.Fatalf("error = %#v, want %q", got, code)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := response.Header().Get("Pragma"); got != "no-cache" {
		t.Fatalf("Pragma = %q", got)
	}
}
