package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type streamLimiterProbe struct {
	acquired chan struct{}
	err      error
	released atomic.Int32
}

func (p *streamLimiterProbe) Acquire(context.Context) (func(), error) {
	if p.acquired != nil {
		select {
		case p.acquired <- struct{}{}:
		default:
		}
	}
	if p.err != nil {
		return nil, p.err
	}
	return func() {
		p.released.Add(1)
	}, nil
}

func TestA2AStreamQuotaReturnsStableChineseJSONRPC429(t *testing.T) {
	probe := &streamLimiterProbe{err: ErrStreamQuotaExceeded}
	server, err := NewServer(NewMemoryStore(), BackendFuncs{}, ServerOptions{
		StreamLimiter: probe,
	})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.POST(RPCPath, server.RPCHandler())

	recorder := serveA2AStreamRequest(t, router, "SubscribeToTask", map[string]any{
		"id": "task-at-capacity",
	})
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response JSONRPCResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if string(response.ID) != "73" ||
		response.Error == nil ||
		response.Error.Code != -32010 ||
		response.Error.Message != "A2A 长连接配额已满，请稍后重试" {
		t.Fatalf("unexpected JSON-RPC quota response: %+v", response)
	}
	if len(response.Error.Data) != 1 {
		t.Fatalf("quota error data=%#v", response.Error.Data)
	}
	detail, ok := response.Error.Data[0].(map[string]any)
	if !ok || detail["reason"] != "STREAM_QUOTA_EXCEEDED" {
		t.Fatalf("quota reason=%#v", response.Error.Data)
	}
	if probe.released.Load() != 0 {
		t.Fatalf("rejected stream released an unacquired permit %d times", probe.released.Load())
	}
}

func TestA2AStreamPermitReleasesExactlyOnceOnErrorAndDisconnect(t *testing.T) {
	t.Run("downstream error", func(t *testing.T) {
		probe := &streamLimiterProbe{}
		server, err := NewServer(NewMemoryStore(), BackendFuncs{}, ServerOptions{
			StreamLimiter: probe,
		})
		if err != nil {
			t.Fatal(err)
		}
		router := gin.New()
		router.POST(RPCPath, server.RPCHandler())
		recorder := serveA2AStreamRequest(t, router, "SubscribeToTask", map[string]any{
			"id": "missing-task",
		})
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		if probe.released.Load() != 1 {
			t.Fatalf("error path releases=%d, want 1", probe.released.Load())
		}
	})

	t.Run("client disconnect", func(t *testing.T) {
		store := NewMemoryStore()
		now := time.Now().UTC()
		if err := store.CreateTask(context.Background(), Task{
			ID:           "working-task",
			ContextID:    "working-context",
			Status:       TaskStatus{State: TaskStateWorking, Timestamp: now},
			CreatedAt:    now,
			LastModified: now,
			Version:      1,
		}); err != nil {
			t.Fatal(err)
		}
		probe := &streamLimiterProbe{acquired: make(chan struct{}, 1)}
		server, err := NewServer(store, BackendFuncs{}, ServerOptions{
			StreamLimiter: probe,
			Heartbeat:     time.Hour,
		})
		if err != nil {
			t.Fatal(err)
		}
		router := gin.New()
		router.POST(RPCPath, server.RPCHandler())

		body := a2aStreamRPCBody(t, "SubscribeToTask", map[string]any{
			"id": "working-task",
		})
		ctx, cancel := context.WithCancel(context.Background())
		request := httptest.NewRequest(http.MethodPost, RPCPath, bytes.NewReader(body)).
			WithContext(ctx)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("A2A-Version", ProtocolVersion)
		recorder := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			defer close(done)
			router.ServeHTTP(recorder, request)
		}()
		select {
		case <-probe.acquired:
		case <-time.After(time.Second):
			t.Fatal("stream permit was not acquired")
		}
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("stream handler did not stop after disconnect")
		}
		if probe.released.Load() != 1 {
			t.Fatalf("disconnect releases=%d, want 1", probe.released.Load())
		}
	})
}

func TestWriteRateLimitErrorPreservesRequestIDWithoutReflectingParams(t *testing.T) {
	body := a2aStreamRPCBody(t, "GetTask", map[string]any{
		"id":    "task-sensitive",
		"token": "query-token-must-not-return",
	})
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, RPCPath, bytes.NewReader(body))
	WriteRateLimitError(context)
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response JSONRPCResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if string(response.ID) != "73" ||
		response.Error == nil ||
		response.Error.Code != -32010 ||
		response.Error.Message != "A2A 请求过于频繁，请稍后重试" {
		t.Fatalf("unexpected rate-limit response: %+v", response)
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("task-sensitive")) ||
		bytes.Contains(recorder.Body.Bytes(), []byte("query-token-must-not-return")) {
		t.Fatalf("rate-limit response reflected request params: %s", recorder.Body.String())
	}
}

func serveA2AStreamRequest(
	t *testing.T,
	router http.Handler,
	method string,
	params map[string]any,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(
		http.MethodPost,
		RPCPath,
		bytes.NewReader(a2aStreamRPCBody(t, method, params)),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("A2A-Version", ProtocolVersion)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func a2aStreamRPCBody(t *testing.T, method string, params map[string]any) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": JSONRPCVersion,
		"id":      73,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}
