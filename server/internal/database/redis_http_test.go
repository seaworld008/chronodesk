package database

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func newHTTPRedisTestClient(t *testing.T, handler http.HandlerFunc) *HTTPRedisClient {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &HTTPRedisClient{
		baseURL: server.URL,
		token:   "test-token",
		client:  server.Client(),
	}
}

func decodeCommand(t *testing.T, request *http.Request) []interface{} {
	t.Helper()

	if request.Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", request.Method)
	}
	if request.URL.Path != "/" {
		t.Fatalf("path = %s, want /", request.URL.Path)
	}
	if got := request.Header.Get("Authorization"); got != "Bearer test-token" {
		t.Fatalf("Authorization = %q, want bearer token", got)
	}

	var command []interface{}
	if err := json.NewDecoder(request.Body).Decode(&command); err != nil {
		t.Fatalf("decode command: %v", err)
	}
	return command
}

func TestHTTPRedisClientSetUsesCommandArray(t *testing.T) {
	client := newHTTPRedisTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		command := decodeCommand(t, request)
		want := []interface{}{"SET", "ticket:key/with spaces", "cached-value", "EX", float64(30)}
		if !reflect.DeepEqual(command, want) {
			t.Fatalf("command = %#v, want %#v", command, want)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"result":"OK"}`))
	})

	if err := client.Set(context.Background(), "ticket:key/with spaces", "cached-value", 30*time.Second); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
}

func TestHTTPRedisClientMultiKeyCommands(t *testing.T) {
	var commands [][]interface{}
	client := newHTTPRedisTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		command := decodeCommand(t, request)
		commands = append(commands, command)
		writer.Header().Set("Content-Type", "application/json")
		if command[0] == "EXISTS" {
			_, _ = writer.Write([]byte(`{"result":2}`))
			return
		}
		_, _ = writer.Write([]byte(`{"result":2}`))
	})

	count, err := client.Exists(context.Background(), "first", "second")
	if err != nil {
		t.Fatalf("Exists() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("Exists() = %d, want 2", count)
	}
	if err := client.Del(context.Background(), "first", "second"); err != nil {
		t.Fatalf("Del() error = %v", err)
	}

	want := [][]interface{}{
		{"EXISTS", "first", "second"},
		{"DEL", "first", "second"},
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
}

func TestHTTPRedisClientEvalUsesNativeRedisArgumentOrder(t *testing.T) {
	client := newHTTPRedisTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		var command []interface{}
		if err := json.NewDecoder(request.Body).Decode(&command); err != nil {
			t.Fatalf("decode command: %v", err)
		}
		want := []interface{}{
			"EVAL",
			"return {KEYS[1], KEYS[2], ARGV[1]}",
			float64(2),
			"guard:{opaque}:rate",
			"guard:{opaque}:concurrency",
			"lease-token",
		}
		if !reflect.DeepEqual(command, want) {
			t.Fatalf("command = %#v, want %#v", command, want)
		}
		_, _ = writer.Write([]byte(`{"result":[0,9,1]}`))
	})

	result, err := client.Eval(
		context.Background(),
		"return {KEYS[1], KEYS[2], ARGV[1]}",
		[]string{"guard:{opaque}:rate", "guard:{opaque}:concurrency"},
		"lease-token",
	)
	if err != nil {
		t.Fatalf("Eval() error = %v", err)
	}
	if !reflect.DeepEqual(result, []interface{}{float64(0), float64(9), float64(1)}) {
		t.Fatalf("Eval() result = %#v", result)
	}
}

func TestHTTPRedisClientReturnsUpstreamFailure(t *testing.T) {
	const sensitiveBody = "upstream-internal-diagnostic"
	client := newHTTPRedisTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		_ = decodeCommand(t, request)
		http.Error(writer, sensitiveBody, http.StatusServiceUnavailable)
	})

	err := client.Ping(context.Background())
	if err == nil {
		t.Fatal("Ping() error = nil, want upstream failure")
	}
	if strings.Contains(err.Error(), sensitiveBody) {
		t.Fatalf("Ping() error exposed upstream body: %v", err)
	}
}

func TestNewHTTPRedisClientRejectsInsecureRemoteURL(t *testing.T) {
	t.Setenv("KV_REST_API_URL", "http://cache.internal.example")
	t.Setenv("KV_REST_API_TOKEN", "secret-token")

	if _, err := NewHTTPRedisClient(); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("NewHTTPRedisClient() error = %v, want HTTPS requirement", err)
	}
}

func TestNewHTTPRedisClientAcceptsLoopbackHTTPForDevelopment(t *testing.T) {
	t.Setenv("KV_REST_API_URL", "http://127.0.0.1:8079/")
	t.Setenv("KV_REST_API_TOKEN", "secret-token")

	client, err := NewHTTPRedisClient()
	if err != nil {
		t.Fatalf("NewHTTPRedisClient() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if client.baseURL != "http://127.0.0.1:8079" {
		t.Fatalf("baseURL = %q", client.baseURL)
	}
}

func TestHTTPRedisClientRejectsOversizedResponse(t *testing.T) {
	client := newHTTPRedisTestClient(t, func(writer http.ResponseWriter, request *http.Request) {
		_ = decodeCommand(t, request)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"result":"` + strings.Repeat("x", maxHTTPRedisResponseBytes) + `"}`))
	})

	err := client.Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("Ping() error = %v, want bounded response error", err)
	}
}
