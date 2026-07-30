package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	currentHMACFixture  = "testtesttesttesttesttesttesttest"
	previousHMACFixture = "demodemodemodemodemodemodemodemo"
)

func TestHealthReportsDependencyState(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/healthz" {
			t.Errorf("path = %q", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{
			"status":"ok",
			"message":"ready",
			"version":"test",
			"dependencies":{"postgresql":"ok","redis":"ok","agent_control":"ok"}
		}`)
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	if code := run(
		[]string{"health", "--base-url", server.URL},
		strings.NewReader(""),
		stdout,
		stderr,
	); code != 0 {
		t.Fatalf("run() code = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout.String(), `"status": "ok"`) {
		t.Fatalf("stdout = %s", stdout)
	}
}

func TestOAuthClientCredentialsBindsProjectAndAudienceWithoutPrintingSecrets(
	t *testing.T,
) {
	t.Setenv("CTL_TEST_CLIENT_SECRET", "client-secret-that-must-never-be-printed")
	const accessToken = "access-token-that-must-not-be-printed"
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/oauth/token" {
			t.Errorf("path = %q", request.URL.Path)
		}
		clientID, clientSecret, ok := request.BasicAuth()
		if !ok || clientID != "client-123" ||
			clientSecret != "client-secret-that-must-never-be-printed" {
			t.Error("OAuth Basic authentication mismatch")
		}
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got := request.Form.Get("grant_type"); got != "client_credentials" {
			t.Errorf("grant_type = %q", got)
		}
		if got := request.Form.Get("project_key"); got != "OPS" {
			t.Errorf("project_key = %q", got)
		}
		resource := server.URL + "/api/v2"
		if got := request.Form.Get("resource"); got != resource {
			t.Errorf("resource = %q, want %q", got, resource)
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token": accessToken,
			"token_type":   "Bearer",
			"expires_in":   600,
			"scope":        "tickets:read",
			"resource":     resource,
			"project_key":  "OPS",
		})
	}))
	defer server.Close()

	tokenPath := filepath.Join(t.TempDir(), "api.token")
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := run([]string{
		"oauth", "client-credentials",
		"--base-url", server.URL,
		"--project-key", "OPS",
		"--audience", "api",
		"--client-id", "client-123",
		"--client-secret-env", "CTL_TEST_CLIENT_SECRET",
		"--scope", "tickets:read",
		"--token-output", tokenPath,
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 {
		t.Fatalf("run() code = %d, stderr = %s", code, stderr)
	}
	combined := stdout.String() + stderr.String()
	for _, forbidden := range []string{
		"client-secret-that-must-never-be-printed",
		accessToken,
	} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("command output exposed secret %q", forbidden)
		}
	}
	content, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(content)) != accessToken {
		t.Fatalf("token file = %q", content)
	}
	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("token permissions = %o, want 600", permissions)
	}
}

func TestProjectCapabilitiesUsesExplicitMachineProjectPath(t *testing.T) {
	t.Setenv("CTL_TEST_API_TOKEN", "api-token")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v2/projects/OPS/capabilities" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer api-token" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{
			"data":{
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
				"concurrency":{
					"optimistic_version":true,
					"ticket_leases":true,
					"idempotency_keys":true
				}
			},
			"meta":{"request_id":"test"}
		}`)
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := run([]string{
		"project", "capabilities",
		"--base-url", server.URL,
		"--project-key", "OPS",
		"--token-env", "CTL_TEST_API_TOKEN",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 {
		t.Fatalf("run() code = %d, stderr = %s", code, stderr)
	}
	if strings.Contains(stdout.String(), "api-token") {
		t.Fatal("capabilities output exposed bearer token")
	}
}

func TestProjectConnectionsUsesHumanRESTAndFailsOnConnectionError(t *testing.T) {
	t.Setenv("CTL_TEST_HUMAN_TOKEN", "human-token")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer human-token" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/projects/OPS/integrations/overview":
			_, _ = io.WriteString(writer, `{"code":0,"data":{
				"connector_definitions":1,
				"connections":1,
				"active_connections":0,
				"error_connections":1,
				"open_conflicts":0,
				"open_dead_letters":0,
				"running_sync_runs":0,
				"connection_health":[{"id":"c1","key":"legacy","name":"Legacy","status":"error"}]
			}}`)
		case "/api/projects/OPS/integrations/connections":
			if request.URL.Query().Get("page") != "1" ||
				request.URL.Query().Get("pageSize") != "100" {
				t.Errorf("query = %s", request.URL.RawQuery)
			}
			_, _ = io.WriteString(writer, `{"code":0,"data":{
				"items":[{"id":"c1","key":"legacy","name":"Legacy","status":"error",
					"has_configuration":true,"has_verification_key":true,
					"last_error_code":"timeout"}],
				"total":1
			}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := run([]string{
		"project", "connections",
		"--base-url", server.URL,
		"--project-key", "OPS",
		"--human-token-env", "CTL_TEST_HUMAN_TOKEN",
	}, strings.NewReader(""), stdout, stderr)
	if code != 1 {
		t.Fatalf("run() code = %d, want 1; stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout.String(), `"healthy": false`) {
		t.Fatalf("stdout = %s", stdout)
	}
	if strings.Contains(stdout.String(), "human-token") {
		t.Fatal("connections output exposed bearer token")
	}
}

func TestWebhookDryRunAndVerifyExactRawBody(t *testing.T) {
	t.Setenv(
		"CTL_TEST_WEBHOOK_SECRET",
		currentHMACFixture,
	)
	t.Setenv(
		"CTL_TEST_PREVIOUS_WEBHOOK_SECRET",
		previousHMACFixture,
	)
	body := []byte("{\"title\":\"保留  空格和换行\"}\n")
	bodyFile := filepath.Join(t.TempDir(), "payload.json")
	if err := os.WriteFile(bodyFile, body, 0o600); err != nil {
		t.Fatal(err)
	}
	timestamp := fmt.Sprintf("%d", time.Now().UTC().Unix())

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := run([]string{
		"webhook", "dry-run",
		"--base-url", "https://desk.example",
		"--project-key", "OPS",
		"--connection-id", "018fb09b-8fa6-79d2-b000-f9f270452554",
		"--mapping-id", "018fb09b-8fa6-79d2-b000-f9f270452555",
		"--external-resource-type", "legacy.ticket",
		"--external-resource-id", "EXT-42",
		"--idempotency-key", "event-42",
		"--body", bodyFile,
		"--content-type", `Application/JSON; Charset="UTF-8"`,
		"--timestamp", timestamp,
		"--secret-env", "CTL_TEST_WEBHOOK_SECRET",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 {
		t.Fatalf("dry-run code = %d, stderr = %s", code, stderr)
	}
	var preview struct {
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
		Sent    bool              `json:"sent"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	canonicalInbound := []byte(
		"v1\n" +
			timestamp + "\n" +
			"OPS\n" +
			"018fb09b-8fa6-79d2-b000-f9f270452554\n" +
			"018fb09b-8fa6-79d2-b000-f9f270452555\n" +
			"event-42\n" +
			"legacy.ticket\n" +
			"EXT-42\n" +
			"application/json\n" +
			string(body),
	)
	expected := testHMACSignature(
		canonicalInbound,
		[]byte(currentHMACFixture),
	)
	if preview.Headers["X-ChronoDesk-Signature"] != expected {
		t.Fatalf("signature = %q, want %q", preview.Headers["X-ChronoDesk-Signature"], expected)
	}
	if got := preview.Headers["Content-Type"]; got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	previousExpected := outboundWebhookSignature(
		timestamp,
		body,
		[]byte(previousHMACFixture),
	)
	if preview.Sent {
		t.Fatal("dry-run must not send the request")
	}
	if !strings.Contains(preview.URL, "/api/v2/projects/OPS/integrations/inbound/") {
		t.Fatalf("url = %q", preview.URL)
	}
	for _, secret := range []string{
		currentHMACFixture,
		previousHMACFixture,
		string(body),
	} {
		if strings.Contains(stdout.String(), secret) {
			t.Fatalf("dry-run output exposed sensitive input %q", secret)
		}
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{
		"webhook", "verify",
		"--body", bodyFile,
		"--timestamp", timestamp,
		"--signature", previousExpected,
		"--secret-env", "CTL_TEST_WEBHOOK_SECRET",
		"--previous-secret-env", "CTL_TEST_PREVIOUS_WEBHOOK_SECRET",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 {
		t.Fatalf("verify code = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout.String(), `"valid": true`) ||
		!strings.Contains(stdout.String(), `"matched_key": "previous"`) {
		t.Fatalf("stdout = %s", stdout)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{
		"webhook", "verify",
		"--body", bodyFile,
		"--timestamp", timestamp,
		"--signature", strings.ToUpper(expected),
		"--secret-env", "CTL_TEST_WEBHOOK_SECRET",
	}, strings.NewReader(""), stdout, stderr)
	if code != 1 {
		t.Fatalf("uppercase signature code = %d, want 1", code)
	}

	staleTimestamp := fmt.Sprintf("%d", time.Now().UTC().Add(-10*time.Minute).Unix())
	staleSignature := outboundWebhookSignature(
		staleTimestamp,
		body,
		[]byte(currentHMACFixture),
	)
	stdout.Reset()
	stderr.Reset()
	code = run([]string{
		"webhook", "verify",
		"--body", bodyFile,
		"--timestamp", staleTimestamp,
		"--signature", staleSignature,
		"--secret-env", "CTL_TEST_WEBHOOK_SECRET",
		"--max-age", "5m",
	}, strings.NewReader(""), stdout, stderr)
	if code != 1 {
		t.Fatalf("stale signature code = %d, want 1", code)
	}
}

func TestInboundWebhookSigningPayloadAuthenticatesRoutingMetadata(t *testing.T) {
	t.Parallel()
	input := inboundWebhookSignatureInput{
		Timestamp:            "1785369600",
		ProjectKey:           "OPS",
		ConnectionPublicID:   "018fb09b-8fa6-79d2-b000-f9f270452554",
		MappingPublicID:      "018fb09b-8fa6-79d2-b000-f9f270452555",
		MessageID:            "event-42",
		ExternalResourceType: "legacy.ticket",
		ExternalResourceID:   "EXT-42",
		ContentType:          "application/cloudevents+json",
		Body:                 []byte("{\"data\":{\"title\":\"保留  空格\"}}\n"),
	}
	want := "v1\n" +
		"1785369600\n" +
		"OPS\n" +
		"018fb09b-8fa6-79d2-b000-f9f270452554\n" +
		"018fb09b-8fa6-79d2-b000-f9f270452555\n" +
		"event-42\n" +
		"legacy.ticket\n" +
		"EXT-42\n" +
		"application/cloudevents+json\n" +
		"{\"data\":{\"title\":\"保留  空格\"}}\n"
	if got := string(inboundWebhookSigningPayload(input)); got != want {
		t.Fatalf("signing payload = %q, want %q", got, want)
	}

	secret := []byte(currentHMACFixture)
	original := inboundWebhookSignature(input, secret)
	input.ProjectKey = "HR"
	if changed := inboundWebhookSignature(input, secret); changed == original {
		t.Fatal("changing authenticated project metadata did not change signature")
	}
}

func TestInboundWebhookContentTypeNormalizationMatchesServer(t *testing.T) {
	t.Parallel()
	for name, testCase := range map[string]struct {
		raw  string
		want string
		ok   bool
	}{
		"json": {
			raw:  "application/json",
			want: "application/json",
			ok:   true,
		},
		"case and charset": {
			raw:  `Application/JSON; Charset="UTF-8"`,
			want: "application/json",
			ok:   true,
		},
		"cloud event": {
			raw:  "application/cloudevents+json; charset=utf-8",
			want: "application/cloudevents+json",
			ok:   true,
		},
		"unsupported parameter": {
			raw: "application/json; profile=test",
		},
		"unsupported charset": {
			raw: "application/json; charset=gbk",
		},
		"unsupported type": {
			raw: "text/json",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeInboundWebhookContentType(testCase.raw)
			if testCase.ok {
				if err != nil || got != testCase.want {
					t.Fatalf(
						"normalizeInboundWebhookContentType(%q) = (%q, %v), want (%q, nil)",
						testCase.raw,
						got,
						err,
						testCase.want,
					)
				}
				return
			}
			if err == nil {
				t.Fatalf(
					"normalizeInboundWebhookContentType(%q) = %q, want error",
					testCase.raw,
					got,
				)
			}
		})
	}
}

func TestInboundWebhookDryRunRejectsControlMetadata(t *testing.T) {
	t.Parallel()
	for name, argument := range map[string][]string{
		"message id": {
			"--idempotency-key", "event\t42",
			"--external-resource-id", "EXT-42",
		},
		"external resource id": {
			"--idempotency-key", "event-42",
			"--external-resource-id", "EXT\u000042",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			args := []string{
				"webhook", "dry-run",
				"--base-url", "https://desk.example",
				"--project-key", "OPS",
				"--connection-id", "018fb09b-8fa6-79d2-b000-f9f270452554",
				"--mapping-id", "018fb09b-8fa6-79d2-b000-f9f270452555",
				"--external-resource-type", "legacy.ticket",
				"--body", "-",
			}
			args = append(args, argument...)
			code := run(
				args,
				strings.NewReader("{}"),
				io.Discard,
				io.Discard,
			)
			if code != 2 {
				t.Fatalf("run() code = %d, want 2", code)
			}
		})
	}
}

func TestOutboundWebhookSignatureKeepsLegacyDomainEventFraming(t *testing.T) {
	t.Parallel()
	timestamp := "1785369600"
	body := []byte("{\"specversion\":\"1.0\"}\n")
	secret := []byte(currentHMACFixture)
	want := testHMACSignature(
		append([]byte(timestamp+"."), body...),
		secret,
	)
	if got := outboundWebhookSignature(timestamp, body, secret); got != want {
		t.Fatalf("outboundWebhookSignature() = %q, want %q", got, want)
	}
}

func testHMACSignature(payload, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(payload)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

func TestCommandsRejectMissingProjectAndAudience(t *testing.T) {
	t.Setenv("CHRONODESK_CLIENT_SECRET", strings.Repeat("s", 32))
	for name, args := range map[string][]string{
		"missing project": {
			"oauth", "client-credentials",
			"--base-url", "https://desk.example",
			"--audience", "api",
			"--client-id", "client",
		},
		"missing audience": {
			"oauth", "client-credentials",
			"--base-url", "https://desk.example",
			"--project-key", "OPS",
			"--client-id", "client",
		},
	} {
		t.Run(name, func(t *testing.T) {
			code := run(args, strings.NewReader(""), io.Discard, io.Discard)
			if code != 2 {
				t.Fatalf("run() code = %d, want 2", code)
			}
		})
	}
}

func TestCommandsRejectCleartextRemoteBaseURL(t *testing.T) {
	code := run(
		[]string{"health", "--base-url", "http://desk.example"},
		strings.NewReader(""),
		io.Discard,
		io.Discard,
	)
	if code != 2 {
		t.Fatalf("run() code = %d, want 2", code)
	}
}

func TestCommandsRejectNonCanonicalBaseURLPath(t *testing.T) {
	code := run(
		[]string{"health", "--base-url", "https://desk.example/base"},
		strings.NewReader(""),
		io.Discard,
		io.Discard,
	)
	if code != 2 {
		t.Fatalf("run() code = %d, want 2", code)
	}
}

func TestRemoteErrorIsSafeForTerminalOutput(t *testing.T) {
	got := sanitizedRemoteError(
		[]byte(`{"code":"invalid_request\u001b[31m\u202e"}`),
	)
	if got != "invalid_request[31m" {
		t.Fatalf("sanitizedRemoteError() = %q", got)
	}
}
