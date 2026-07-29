package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
)

func TestEndpointFingerprintDoesNotExposeEndpointOrCredentials(t *testing.T) {
	rawURL := "rediss://default:top-secret@cache.internal.example:6379"
	fingerprint := endpointFingerprint(rawURL)

	for _, secret := range []string{"cache.internal.example", "default", "top-secret"} {
		if strings.Contains(fingerprint, secret) {
			t.Fatalf("fingerprint %q exposes %q", fingerprint, secret)
		}
	}
	if !strings.HasPrefix(fingerprint, "sha256:") {
		t.Fatalf("fingerprint = %q, want sha256 prefix", fingerprint)
	}
}

func TestValidateRESTEndpointRequiresTLSForRemoteHosts(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		wantErr  string
	}{
		{name: "https remote", endpoint: "https://cache.example.com", wantErr: ""},
		{name: "http remote", endpoint: "http://cache.example.com", wantErr: "insecure_transport"},
		{name: "http loopback", endpoint: "http://127.0.0.1:8079", wantErr: ""},
		{name: "userinfo", endpoint: "https://token@cache.example.com", wantErr: "invalid_url"},
		{name: "query", endpoint: "https://cache.example.com?token=value", wantErr: "invalid_url"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := validateRESTEndpoint(test.endpoint)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("validateRESTEndpoint() error = %v", err)
				}
				return
			}
			if err == nil || err.Error() != test.wantErr {
				t.Fatalf("validateRESTEndpoint() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestClassifyConnectivityError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "EOF", err: io.EOF, want: "connection_eof"},
		{name: "reset", err: syscall.ECONNRESET, want: "connection_reset"},
		{name: "refused", err: syscall.ECONNREFUSED, want: "connection_refused"},
		{name: "DNS", err: &net.DNSError{Err: "not found", Name: "redacted"}, want: "dns_lookup_failed"},
		{name: "wrapped", err: errors.Join(errors.New("probe failed"), syscall.EHOSTUNREACH), want: "network_unreachable"},
		{name: "other", err: errors.New("opaque"), want: "transport_error"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyConnectivityError(test.err); got != test.want {
				t.Fatalf("classifyConnectivityError() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestProbeRedisRESTUsesReadOnlyPing(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", request.Method)
		}
		if request.Header.Get("Authorization") != "Bearer read-only-token" {
			t.Fatal("missing bearer token")
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if string(body) != `["PING"]` {
			t.Fatalf("body = %s, want read-only PING", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"result":"PONG"}`))
	}))
	defer server.Close()

	result := probeRedisREST(context.Background(), server.URL, "read-only-token", server.Client())
	if !result.ok || result.category != "pong" {
		t.Fatalf("probe result = %#v", result)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestProbeRedisRESTDoesNotFollowRedirects(t *testing.T) {
	redirectedRequests := 0
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		redirectedRequests++
		_, _ = writer.Write([]byte(`{"result":"PONG"}`))
	}))
	defer redirectTarget.Close()

	redirectSource := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", redirectTarget.URL)
		writer.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer redirectSource.Close()

	client := redirectSource.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	result := probeRedisREST(context.Background(), redirectSource.URL, "read-only-token", client)
	if result.ok || result.category != "http_status_307" {
		t.Fatalf("probe result = %#v, want rejected redirect", result)
	}
	if redirectedRequests != 0 {
		t.Fatalf("redirect target received %d requests, want 0", redirectedRequests)
	}
}

func TestRunRedisGateTestsRESTWhenTCPIsNotConfigured(t *testing.T) {
	t.Setenv("REDIS_URL", "")
	t.Setenv("KV_REST_API_URL", "http://127.0.0.1:1")
	t.Setenv("KV_REST_API_READ_ONLY_TOKEN", "read-only-token")
	t.Setenv("KV_REST_API_TOKEN", "")

	var output bytes.Buffer
	exitCode := runRedisGate(context.Background(), &output)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want connectivity failure", exitCode)
	}
	if !strings.Contains(output.String(), "REST/HTTPS") {
		t.Fatalf("output does not include REST probe: %s", output.String())
	}
	if strings.Contains(output.String(), "read-only-token") {
		t.Fatal("output exposed credential")
	}
}

func TestRunRedisGatePrefersReadOnlyToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer safer-token" {
			t.Fatalf("Authorization = %q, want read-only token", request.Header.Get("Authorization"))
		}
		_, _ = writer.Write([]byte(`{"result":"PONG"}`))
	}))
	defer server.Close()

	t.Setenv("REDIS_URL", "")
	t.Setenv("KV_REST_API_URL", server.URL)
	t.Setenv("KV_REST_API_READ_ONLY_TOKEN", "safer-token")
	t.Setenv("KV_REST_API_TOKEN", "write-token")

	var output bytes.Buffer
	exitCode := runRedisGate(context.Background(), &output)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, output = %s", exitCode, output.String())
	}
	if strings.Contains(output.String(), "safer-token") || strings.Contains(output.String(), "write-token") {
		t.Fatal("output exposed credential")
	}
}
