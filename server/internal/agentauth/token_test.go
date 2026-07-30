package agentauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestManagerIssueVerifyAndAudience(t *testing.T) {
	manager := NewManager("a-test-secret-that-is-long-enough", "https://issuer.example", "https://api.example/mcp", 10*time.Minute)
	now := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }

	principal := &Principal{
		ID:           "principal-7",
		CredentialID: "credential-11",
		ClientID:     "agent_123",
		Name:         "triage-agent",
		Scopes:       []string{"tickets:read", "tickets:update"},
		Active:       true,
	}
	token, _, err := manager.Issue(principal, "TEST", []string{"tickets:read"})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	access, err := manager.Verify(token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if access.PrincipalID != principal.ID ||
		access.ProjectKey != "TEST" ||
		len(access.Scopes) != 1 ||
		access.Scopes[0] != "tickets:read" {
		t.Fatalf("unexpected access context: %#v", access)
	}

	otherAudience := NewManager("a-test-secret-that-is-long-enough", "https://issuer.example", "https://api.example/other", 10*time.Minute)
	otherAudience.now = manager.now
	if _, err := otherAudience.Verify(token); err != ErrInvalidAudience {
		t.Fatalf("Verify() audience error = %v, want %v", err, ErrInvalidAudience)
	}

	trailingSlashAudience := NewManager("a-test-secret-that-is-long-enough", "https://issuer.example", "https://api.example/mcp/", 10*time.Minute)
	trailingSlashAudience.now = manager.now
	if _, err := trailingSlashAudience.Verify(token); err != ErrInvalidAudience {
		t.Fatalf("Verify() trailing-slash audience error = %v, want %v", err, ErrInvalidAudience)
	}
}

func TestManagerAudienceIsolationMatrixAndRFC9068ScopeClaim(t *testing.T) {
	now := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	resources := map[string]string{
		"mcp": "https://api.example/mcp",
		"api": "https://api.example/api/v2",
		"a2a": "https://api.example/a2a/v1",
	}
	managers := make(map[string]*Manager, len(resources))
	for name, resource := range resources {
		manager := NewManager("shared-resource-isolation-secret", "https://api.example", resource, 10*time.Minute)
		manager.now = func() time.Time { return now }
		managers[name] = manager
	}
	principal := &Principal{
		ID:           "principal-matrix",
		CredentialID: "credential-matrix",
		ClientID:     "client-matrix",
		Name:         "matrix-agent",
		Scopes:       []string{"tickets:read", "tasks:manage"},
		Active:       true,
	}
	tokens := make(map[string]string, len(managers))
	for name, manager := range managers {
		token, _, err := manager.Issue(principal, "TEST", principal.Scopes)
		if err != nil {
			t.Fatalf("Issue(%s) error = %v", name, err)
		}
		tokens[name] = token
	}

	for tokenResource, token := range tokens {
		for verifierResource, verifier := range managers {
			_, err := verifier.Verify(token)
			if tokenResource == verifierResource {
				if err != nil {
					t.Errorf("%s token rejected by matching verifier: %v", tokenResource, err)
				}
				continue
			}
			if err != ErrInvalidAudience {
				t.Errorf("%s token verified by %s manager with error %v, want %v", tokenResource, verifierResource, err, ErrInvalidAudience)
			}
		}
	}

	parts := strings.Split(tokens["mcp"], ".")
	if len(parts) != 3 {
		t.Fatalf("token parts = %d", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	if got, ok := claims["scope"].(string); !ok || got != "tickets:read tasks:manage" {
		t.Fatalf("RFC 9068 scope claim = %#v, want a space-delimited string", claims["scope"])
	}
	if claims["project_key"] != "TEST" {
		t.Fatalf("project_key claim = %#v, want TEST", claims["project_key"])
	}
}

func TestManagerRequiresCanonicalProjectKey(t *testing.T) {
	manager := NewManager("project-key-test-secret", "issuer", "resource", time.Minute)
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	principal := &Principal{
		ID: "principal", CredentialID: "credential", ClientID: "client",
		Name: "agent", Scopes: []string{"tickets:read"}, Active: true,
	}
	for _, projectKey := range []string{"", " test ", "lowercase", "TOO/LONG"} {
		if _, _, err := manager.Issue(principal, projectKey, principal.Scopes); err != ErrInvalidToken {
			t.Fatalf("Issue(project_key=%q) error = %v, want %v", projectKey, err, ErrInvalidToken)
		}
	}

	legacyToken, err := manager.sign(accessClaims{
		Iss:          "issuer",
		Sub:          "service-principal:principal",
		Aud:          "resource",
		Exp:          now.Add(time.Minute).Unix(),
		Iat:          now.Unix(),
		JTI:          "legacy-token",
		ClientID:     "client",
		PrincipalID:  "principal",
		CredentialID: "credential",
		Name:         "agent",
		Scope:        "tickets:read",
		TokenType:    "agent_access",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Verify(legacyToken); err != ErrInvalidToken {
		t.Fatalf("legacy token without project_key error = %v, want %v", err, ErrInvalidToken)
	}
}

type recordingAccessValidator struct {
	projectKey string
	scopes     []string
	calls      int
}

func (validator *recordingAccessValidator) ValidateAccessContext(
	_ context.Context,
	_, _, projectKey string,
	scopes []string,
) error {
	validator.calls++
	validator.projectKey = projectKey
	validator.scopes = append([]string(nil), scopes...)
	return nil
}

func TestMiddlewareBindsTokenToProjectRouteAndGinContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := NewManager("project-route-test-secret", "issuer", "resource", time.Minute)
	principal := &Principal{
		ID: "principal", CredentialID: "credential", ClientID: "client",
		Name: "agent", Scopes: []string{"tickets:read"}, Active: true,
	}
	token, _, err := manager.Issue(principal, "ALPHA", principal.Scopes)
	if err != nil {
		t.Fatal(err)
	}
	validator := &recordingAccessValidator{}
	manager.SetAccessValidator(validator)

	router := gin.New()
	router.GET(
		"/projects/:projectKey/tickets",
		manager.Middleware("tickets:read"),
		func(c *gin.Context) {
			value, exists := c.Get(ContextProjectKey)
			if !exists || value != "ALPHA" {
				t.Fatalf("Gin project context = %#v, exists=%v", value, exists)
			}
			c.Status(http.StatusNoContent)
		},
	)

	for _, test := range []struct {
		projectKey string
		status     int
	}{
		{projectKey: "ALPHA", status: http.StatusNoContent},
		{projectKey: "BETA", status: http.StatusForbidden},
	} {
		request := httptest.NewRequest(
			http.MethodGet,
			"/projects/"+test.projectKey+"/tickets",
			nil,
		)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf(
				"project %s status = %d, want %d; body=%s",
				test.projectKey,
				response.Code,
				test.status,
				response.Body.String(),
			)
		}
		if test.status == http.StatusForbidden &&
			!strings.Contains(response.Body.String(), `"code":"project_scope_mismatch"`) {
			t.Fatalf("project mismatch body = %s", response.Body.String())
		}
	}
	if validator.calls != 1 ||
		validator.projectKey != "ALPHA" ||
		fmt.Sprint(validator.scopes) != fmt.Sprint(principal.Scopes) {
		t.Fatalf(
			"access validator calls=%d project=%q scopes=%v",
			validator.calls,
			validator.projectKey,
			validator.scopes,
		)
	}
}

func TestAudienceSpecificBearerMiddlewareRejectsCrossResourceTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	resources := map[string]string{
		"mcp": "https://api.example/mcp",
		"api": "https://api.example/api/v2",
		"a2a": "https://api.example/a2a/v1",
	}
	managers := make(map[string]*Manager, len(resources))
	tokens := make(map[string]string, len(resources))
	principal := &Principal{
		ID:           "principal-http-matrix",
		CredentialID: "credential-http-matrix",
		ClientID:     "client-http-matrix",
		Name:         "http-matrix-agent",
		Scopes:       []string{"tickets:read"},
		Active:       true,
	}
	router := gin.New()
	for name, resource := range resources {
		manager := NewManager("shared-http-resource-secret", "https://api.example", resource, 10*time.Minute)
		manager.now = func() time.Time { return now }
		managers[name] = manager
		token, _, err := manager.Issue(principal, "TEST", principal.Scopes)
		if err != nil {
			t.Fatalf("Issue(%s) error = %v", name, err)
		}
		tokens[name] = token
		path := "/" + name
		router.GET(path, manager.Middleware("tickets:read"), func(c *gin.Context) {
			c.Status(http.StatusNoContent)
		})
	}

	for tokenResource, token := range tokens {
		for endpointResource := range managers {
			request := httptest.NewRequest(http.MethodGet, "/"+endpointResource, nil)
			request.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if tokenResource == endpointResource {
				if response.Code != http.StatusNoContent {
					t.Errorf("%s token -> %s endpoint status = %d", tokenResource, endpointResource, response.Code)
				}
				continue
			}
			if response.Code != http.StatusUnauthorized {
				t.Errorf("%s token -> %s endpoint status = %d, want 401", tokenResource, endpointResource, response.Code)
			}
			wantMetadata := protectedResourceMetadataURL(resources[endpointResource])
			if challenge := response.Header().Get("WWW-Authenticate"); !strings.Contains(challenge, wantMetadata) {
				t.Errorf("%s endpoint challenge = %q, want metadata %q", endpointResource, challenge, wantMetadata)
			}
		}
	}
}

func TestManagerRejectsScopeEscalationAndExpiry(t *testing.T) {
	manager := NewManager("another-test-secret", "issuer", "resource", time.Minute)
	now := time.Now().UTC()
	manager.now = func() time.Time { return now }
	expiresAt := now.Add(30 * time.Second)

	principal := &Principal{
		ID:           "principal-1",
		CredentialID: "credential-1",
		ClientID:     "client",
		Name:         "agent",
		Scopes:       []string{"tickets:read"},
		Active:       true,
		ExpiresAt:    &expiresAt,
	}
	if _, _, err := manager.Issue(principal, "TEST", []string{"tickets:update"}); err != ErrInsufficientScope {
		t.Fatalf("Issue() error = %v, want %v", err, ErrInsufficientScope)
	}

	token, _, err := manager.Issue(principal, "TEST", nil)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	manager.now = func() time.Time { return expiresAt.Add(time.Second) }
	if _, err := manager.Verify(token); err != ErrExpiredToken {
		t.Fatalf("Verify() error = %v, want %v", err, ErrExpiredToken)
	}
}

type rejectingAccessValidator struct{}

func (rejectingAccessValidator) ValidateAccessContext(
	context.Context,
	string,
	string,
	string,
	[]string,
) error {
	return errors.New("credential revoked")
}

func TestMiddlewareRevalidatesCredentialRevocation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := NewManager("middleware-test-secret", "issuer", "resource", time.Minute)
	principal := &Principal{
		ID: "principal", CredentialID: "credential", ClientID: "client",
		Name: "agent", Scopes: []string{"tickets:read"}, Active: true,
	}
	token, _, err := manager.Issue(principal, "TEST", principal.Scopes)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetAccessValidator(rejectingAccessValidator{})

	router := gin.New()
	router.GET("/protected", manager.Middleware("tickets:read"), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestMiddlewareAppliesTokenDeadlineWithoutSSEAcceptHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := NewManager("middleware-deadline-test-secret", "issuer", "resource", time.Minute)
	principal := &Principal{
		ID: "principal", CredentialID: "credential", ClientID: "client",
		Name: "agent", Scopes: []string{"tickets:read"}, Active: true,
	}
	token, expiresAt, err := manager.Issue(principal, "TEST", principal.Scopes)
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/protected", manager.Middleware("tickets:read"), func(c *gin.Context) {
		deadline, ok := c.Request.Context().Deadline()
		if !ok {
			t.Error("authenticated request context has no token deadline")
		}
		wantDeadline := time.Unix(expiresAt.Unix(), 0)
		if ok && !deadline.Equal(wantDeadline) {
			t.Errorf("deadline = %s, want %s", deadline, wantDeadline)
		}
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

type revocableAccessValidator struct {
	revoked atomic.Bool
}

func (v *revocableAccessValidator) ValidateAccessContext(
	context.Context,
	string,
	string,
	string,
	[]string,
) error {
	if v.revoked.Load() {
		return errors.New("credential revoked")
	}
	return nil
}

func TestMiddlewareCancelsLongRequestAfterCredentialRevocationWithoutAcceptHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := NewManager("middleware-revocation-test-secret", "issuer", "resource", time.Minute)
	manager.validationEvery = 5 * time.Millisecond
	principal := &Principal{
		ID: "principal", CredentialID: "credential", ClientID: "client",
		Name: "agent", Scopes: []string{"tasks:manage"}, Active: true,
	}
	token, _, err := manager.Issue(principal, "TEST", principal.Scopes)
	if err != nil {
		t.Fatal(err)
	}
	validator := &revocableAccessValidator{}
	manager.SetAccessValidator(validator)

	started := make(chan struct{})
	router := gin.New()
	router.POST("/streaming-method", manager.Middleware("tasks:manage"), func(c *gin.Context) {
		close(started)
		<-c.Request.Context().Done()
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/streaming-method", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	finished := make(chan struct{})
	go func() {
		router.ServeHTTP(response, request)
		close(finished)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request handler did not start")
	}
	validator.revoked.Store(true)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("credential revocation did not cancel the long-running request")
	}
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}
