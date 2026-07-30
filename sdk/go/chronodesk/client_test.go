package chronodesk

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestClientCredentialsUsesExplicitProjectAndAudience(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/oauth/token" {
			t.Errorf("path = %q", request.URL.Path)
		}
		clientID, secret, ok := request.BasicAuth()
		if !ok || clientID != "client" || secret != "secret-value" {
			t.Error("Basic authentication mismatch")
		}
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got := request.Form.Get("project_key"); got != "OPS" {
			t.Errorf("project_key = %q", got)
		}
		resource := server.URL + "/api/v2"
		if got := request.Form.Get("resource"); got != resource {
			t.Errorf("resource = %q, want %q", got, resource)
		}
		_ = json.NewEncoder(writer).Encode(TokenResponse{
			AccessToken: "access-token",
			TokenType:   "Bearer",
			ExpiresIn:   600,
			Scope:       "tickets:read",
			Resource:    resource,
			ProjectKey:  "OPS",
		})
	}))
	defer server.Close()

	client, err := New(server.URL, "OPS", WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	token, err := client.ExchangeClientCredentials(context.Background(), ClientCredentials{
		ClientID:     "client",
		ClientSecret: "secret-value",
		Audience:     AudienceAPI,
		Scopes:       []string{"tickets:read"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if token.ProjectKey != "OPS" || token.Resource != server.URL+"/api/v2" {
		t.Fatalf("token = %+v", token)
	}
}

func TestProjectScopedCapabilitiesAndTicketList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer api-token" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v2/projects/OPS/capabilities":
			_, _ = io.WriteString(writer, `{"data":{
				"api_version":"v2",
				"openapi":"/openapi.yaml",
				"asyncapi":"/asyncapi.yaml",
				"mcp_endpoint":"/mcp",
				"mcp_version":"2026-07-28",
				"a2a_endpoint":"/a2a/v1",
				"a2a_version":"1.0",
				"agent_card":"/.well-known/agent-card.json",
				"oauth_metadata":{
					"api":"/.well-known/oauth-protected-resource/api/v2",
					"mcp":"/.well-known/oauth-protected-resource/mcp",
					"a2a":"/.well-known/oauth-protected-resource/a2a/v1"
				},
				"scopes_supported":["tickets:read"],
				"concurrency":{"optimistic_version":true,"ticket_leases":true,"idempotency_keys":true}
			},"meta":{"request_id":"r1"}}`)
		case "/api/v2/projects/OPS/tickets":
			if request.URL.Query().Get("limit") != "10" ||
				request.URL.Query().Get("status") != "open" {
				t.Errorf("query = %s", request.URL.RawQuery)
			}
			_, _ = io.WriteString(writer, `{"data":[{
				"id":42,
				"ticket_number":"OPS-42",
				"title":"untrusted",
				"description":"untrusted",
				"type":"incident",
				"priority":"high",
				"status":"open",
				"source":"api",
				"version":1,
				"tags":[],
				"created_at":"2026-07-30T00:00:00Z",
				"updated_at":"2026-07-30T00:00:00Z"
			}],"meta":{"request_id":"r2"}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := New(
		server.URL,
		"OPS",
		WithHTTPClient(server.Client()),
		WithAccessToken("api-token"),
	)
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := client.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.Data.APIVersion != "v2" {
		t.Fatalf("capabilities = %+v", capabilities)
	}
	tickets, err := client.ListTickets(context.Background(), TicketListOptions{
		Limit:  10,
		Status: "open",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tickets.Data) != 1 || tickets.Data[0].TicketNumber != "OPS-42" {
		t.Fatalf("tickets = %+v", tickets.Data)
	}
}

func TestClientRejectsImplicitScopeAndAudience(t *testing.T) {
	if _, err := New("https://desk.example", ""); err == nil {
		t.Fatal("New accepted an empty project")
	}
	client, err := New("https://desk.example", "OPS")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ExchangeClientCredentials(
		context.Background(),
		ClientCredentials{
			ClientID:     "client",
			ClientSecret: "secret",
		},
	); err == nil || !strings.Contains(err.Error(), "audience") {
		t.Fatalf("audience error = %v", err)
	}
	if _, err := client.Capabilities(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "access token") {
		t.Fatalf("token error = %v", err)
	}
}

func TestClientRejectsCleartextRemoteBaseURL(t *testing.T) {
	if _, err := New("http://desk.example", "OPS"); err == nil {
		t.Fatal("New accepted cleartext remote base URL")
	}
}

func TestClientRejectsNonCanonicalBaseURLPath(t *testing.T) {
	if _, err := New("https://desk.example/base", "OPS"); err == nil {
		t.Fatal("New accepted a base URL path")
	}
}

func TestClientRequiresBoundedTimeout(t *testing.T) {
	if _, err := New(
		"https://desk.example",
		"OPS",
		WithTimeout(0),
	); err == nil {
		t.Fatal("New accepted a zero timeout")
	}
	client, err := New(
		"https://desk.example",
		"OPS",
		WithHTTPClient(&http.Client{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if client.httpClient.Timeout <= 0 {
		t.Fatal("custom HTTP client retained an unbounded timeout")
	}
}

func TestClientCredentialsRejectRedirectWithoutForwardingSecret(t *testing.T) {
	var destinationReached atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, _ *http.Request) {
			destinationReached.Store(true)
			writer.WriteHeader(http.StatusOK)
		},
	))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(
				writer,
				request,
				destination.URL,
				http.StatusTemporaryRedirect,
			)
		},
	))
	defer source.Close()
	client, err := New(source.URL, "OPS", WithHTTPClient(source.Client()))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ExchangeClientCredentials(
		context.Background(),
		ClientCredentials{
			ClientID:     "client",
			ClientSecret: "must-not-be-forwarded",
			Audience:     AudienceAPI,
		},
	)
	if err == nil {
		t.Fatal("ExchangeClientCredentials followed a redirect")
	}
	if destinationReached.Load() {
		t.Fatal("redirect destination received the credential request")
	}
}
