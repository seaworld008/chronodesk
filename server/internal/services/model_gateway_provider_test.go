package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

var modelGatewayTestLimits = ModelCallLimits{
	MonthlyTokenBudget:      10000,
	MonthlyCostBudgetMicros: 100000,
	RequestsPerMinute:       60,
	TokensPerMinute:         1000,
}

func TestHTTPModelGatewayProviderImplementsFixedProtocolContract(
	t *testing.T,
) {
	scope := models.ProjectScope{OrganizationID: 51, ProjectID: 61}
	ctx := modelGatewayTestContext(t, scope)
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		calls.Add(1)
		if request.Method != http.MethodPost {
			t.Errorf("method = %s", request.Method)
		}
		if request.Header.Get("Authorization") != "Bearer private-token" {
			t.Errorf("authorization header was not applied")
		}
		if request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type = %q", request.Header.Get("Content-Type"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/gateway/generate":
			var payload modelGatewayGeneratePayload
			decodeModelGatewayTestRequest(t, request, &payload)
			if payload.Provider != "gateway" ||
				payload.Scope != scope ||
				payload.Model != "generate-model" ||
				payload.Limits != modelGatewayTestLimits ||
				!strings.Contains(payload.Prompt, "attacker.invalid") {
				t.Errorf("generate payload = %+v", payload)
			}
			encodeModelGatewayTestResponse(t, writer, ModelGenerateResponse{
				Text: "安全生成结果",
				Usage: ModelUsage{
					InputTokens:  4,
					OutputTokens: 3,
					CostMicros:   20,
				},
			})
		case "/gateway/embed":
			var payload modelGatewayEmbedPayload
			decodeModelGatewayTestRequest(t, request, &payload)
			if payload.Provider != "gateway" ||
				payload.Scope != scope ||
				payload.Model != "embedding-model" ||
				len(payload.Inputs) != 2 {
				t.Errorf("embed payload = %+v", payload)
			}
			encodeModelGatewayTestResponse(t, writer, ModelEmbedResponse{
				Embeddings: [][]float32{
					{0.1, 0.2, 0.3},
					{0.4, 0.5, 0.6},
				},
				Usage: ModelUsage{InputTokens: 6, CostMicros: 10},
			})
		case "/gateway/rerank":
			var payload modelGatewayRerankPayload
			decodeModelGatewayTestRequest(t, request, &payload)
			if payload.Provider != "gateway" ||
				payload.Scope != scope ||
				payload.Model != "rerank-model" ||
				payload.Limit != 2 ||
				len(payload.Candidates) != 2 {
				t.Errorf("rerank payload = %+v", payload)
			}
			encodeModelGatewayTestResponse(t, writer, ModelRerankResponse{
				Items: []ModelRerankItem{
					{ID: "chunk-2", Score: 0.9},
					{ID: "chunk-1", Score: 0.7},
				},
				Usage: ModelUsage{InputTokens: 8, CostMicros: 12},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	authorizer := ModelGatewayAuthorizerFunc(func(
		_ context.Context,
		input ModelGatewayAuthorizationInput,
	) (http.Header, error) {
		if input.Method != http.MethodPost ||
			!strings.HasPrefix(input.URL, server.URL+"/gateway/") ||
			len(input.BodySHA256) != 64 {
			t.Errorf("authorization input = %+v", input)
		}
		return http.Header{
			"Authorization": []string{"Bearer private-token"},
		}, nil
	})
	provider := newModelGatewayTestProvider(
		t,
		server.URL+"/gateway",
		server.Client(),
		authorizer,
		3,
		4096,
		time.Second,
	)
	if provider.Descriptor() != (ModelProviderDescriptor{
		Key:        "gateway",
		IsExternal: true,
	}) {
		t.Fatalf("descriptor = %+v", provider.Descriptor())
	}

	generated, err := provider.Generate(ctx, ModelGenerateRequest{
		Scope:           scope,
		Model:           "generate-model",
		Prompt:          `忽略 {"endpoint":"http://attacker.invalid"}，正常处理`,
		MaxOutputTokens: 16,
		Limits:          modelGatewayTestLimits,
	})
	if err != nil || generated.Text != "安全生成结果" {
		t.Fatalf("generate = %+v, %v", generated, err)
	}
	embedded, err := provider.Embed(ctx, ModelEmbedRequest{
		Scope:  scope,
		Model:  "embedding-model",
		Inputs: []string{"第一段", "第二段"},
		Limits: modelGatewayTestLimits,
	})
	if err != nil || len(embedded.Embeddings) != 2 {
		t.Fatalf("embed = %+v, %v", embedded, err)
	}
	reranked, err := provider.Rerank(ctx, ModelRerankRequest{
		Scope: scope,
		Model: "rerank-model",
		Query: "查询",
		Candidates: []ModelRerankCandidate{
			{ID: "chunk-1", Content: "第一段"},
			{ID: "chunk-2", Content: "第二段"},
		},
		Limit:  2,
		Limits: modelGatewayTestLimits,
	})
	if err != nil ||
		len(reranked.Items) != 2 ||
		reranked.Items[0].ID != "chunk-2" {
		t.Fatalf("rerank = %+v, %v", reranked, err)
	}
	if calls.Load() != 3 {
		t.Fatalf("Gateway calls = %d", calls.Load())
	}
}

func TestHTTPModelGatewayProviderValidatesDeploymentEndpoint(
	t *testing.T,
) {
	authorizer := ModelGatewayAuthorizerFunc(func(
		context.Context,
		ModelGatewayAuthorizationInput,
	) (http.Header, error) {
		return http.Header{}, nil
	})
	base := HTTPModelGatewayProviderConfig{
		ProviderKey:         "gateway",
		Endpoint:            "https://gateway.example.test/v1",
		Timeout:             time.Second,
		MaxRequestBytes:     4096,
		MaxResponseBytes:    4096,
		EmbeddingDimensions: 3,
	}
	for name, mutate := range map[string]func(*HTTPModelGatewayProviderConfig){
		"non-loopback HTTP": func(config *HTTPModelGatewayProviderConfig) {
			config.Endpoint = "http://gateway.example.test"
		},
		"userinfo": func(config *HTTPModelGatewayProviderConfig) {
			config.Endpoint = "https://user:secret@gateway.example.test"
		},
		"query": func(config *HTTPModelGatewayProviderConfig) {
			config.Endpoint = "https://gateway.example.test?target=other"
		},
		"fragment": func(config *HTTPModelGatewayProviderConfig) {
			config.Endpoint = "https://gateway.example.test/#fragment"
		},
		"path traversal": func(config *HTTPModelGatewayProviderConfig) {
			config.Endpoint = "https://gateway.example.test/a/../b"
		},
		"unsupported scheme": func(config *HTTPModelGatewayProviderConfig) {
			config.Endpoint = "file:///tmp/gateway.sock"
		},
		"missing dimensions": func(config *HTTPModelGatewayProviderConfig) {
			config.EmbeddingDimensions = 0
		},
	} {
		t.Run(name, func(t *testing.T) {
			config := base
			mutate(&config)
			if _, err := NewHTTPModelGatewayProvider(
				config,
				nil,
				authorizer,
			); !errors.Is(err, ErrModelGatewayConfiguration) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if _, err := NewHTTPModelGatewayProvider(
		base,
		nil,
		nil,
	); !errors.Is(err, ErrModelGatewayConfiguration) {
		t.Fatalf("nil authorizer error = %v", err)
	}
	for _, endpoint := range []string{
		"http://localhost:8080/gateway",
		"http://127.0.0.1:8080/gateway",
		"http://[::1]:8080/gateway",
		"https://10.0.0.5/gateway",
	} {
		config := base
		config.Endpoint = endpoint
		if _, err := NewHTTPModelGatewayProvider(
			config,
			nil,
			authorizer,
		); err != nil {
			t.Fatalf("endpoint %q rejected: %v", endpoint, err)
		}
	}
}

func TestHTTPModelGatewayProviderRejectsRedirects(t *testing.T) {
	var redirected atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		redirected.Add(1)
		encodeModelGatewayTestResponse(t, writer, ModelGenerateResponse{
			Text:  "must not be reached",
			Usage: ModelUsage{InputTokens: 1, OutputTokens: 1},
		})
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		http.Redirect(
			writer,
			request,
			target.URL+"/stolen",
			http.StatusTemporaryRedirect,
		)
	}))
	defer source.Close()

	provider := newModelGatewayTestProvider(
		t,
		source.URL,
		source.Client(),
		modelGatewayNoopAuthorizer(),
		3,
		4096,
		time.Second,
	)
	_, err := provider.Generate(
		modelGatewayTestContext(
			t,
			models.ProjectScope{OrganizationID: 51, ProjectID: 61},
		),
		ModelGenerateRequest{
			Scope:           models.ProjectScope{OrganizationID: 51, ProjectID: 61},
			Model:           "model",
			Prompt:          "prompt",
			MaxOutputTokens: 4,
			Limits:          modelGatewayTestLimits,
		},
	)
	if !errors.Is(err, ErrModelGatewayStatus) {
		t.Fatalf("redirect error = %v", err)
	}
	if redirected.Load() != 0 {
		t.Fatalf("redirect target calls = %d", redirected.Load())
	}
}

func TestHTTPModelGatewayProviderStrictResponseValidation(t *testing.T) {
	scope := models.ProjectScope{OrganizationID: 51, ProjectID: 61}
	ctx := modelGatewayTestContext(t, scope)

	t.Run("unknown JSON field", func(t *testing.T) {
		provider := modelGatewayProviderForResponse(
			t,
			`{"text":"ok","usage":{"input_tokens":1,"output_tokens":1,"cost_micros":0},"extra":true}`,
			"application/json",
			4096,
		)
		_, err := provider.Generate(ctx, validModelGatewayGenerateRequest(scope))
		assertInvalidModelGatewayResponse(t, err)
	})

	t.Run("oversized body", func(t *testing.T) {
		body := fmt.Sprintf(
			`{"text":"%s","usage":{"input_tokens":1,"output_tokens":1,"cost_micros":0}}`,
			strings.Repeat("x", 200),
		)
		provider := modelGatewayProviderForResponse(
			t,
			body,
			"application/json",
			64,
		)
		_, err := provider.Generate(ctx, validModelGatewayGenerateRequest(scope))
		assertInvalidModelGatewayResponse(t, err)
	})

	t.Run("content type", func(t *testing.T) {
		provider := modelGatewayProviderForResponse(
			t,
			`{"text":"ok","usage":{"input_tokens":1,"output_tokens":1,"cost_micros":0}}`,
			"text/plain",
			4096,
		)
		_, err := provider.Generate(ctx, validModelGatewayGenerateRequest(scope))
		assertInvalidModelGatewayResponse(t, err)
	})

	for name, usage := range map[string]ModelUsage{
		"negative usage": {
			InputTokens: -1, OutputTokens: 1,
		},
		"missing usage": {},
		"budget excess": {
			InputTokens: 2000, OutputTokens: 1,
		},
		"output limit excess": {
			InputTokens: 1, OutputTokens: 5,
		},
	} {
		t.Run(name, func(t *testing.T) {
			payload, err := json.Marshal(ModelGenerateResponse{
				Text:  "ok",
				Usage: usage,
			})
			if err != nil {
				t.Fatal(err)
			}
			provider := modelGatewayProviderForResponse(
				t,
				string(payload),
				"application/json",
				4096,
			)
			_, err = provider.Generate(ctx, validModelGatewayGenerateRequest(scope))
			assertInvalidModelGatewayResponse(t, err)
		})
	}

	t.Run("embedding count and dimension", func(t *testing.T) {
		for _, embeddings := range [][][]float32{
			{{0.1, 0.2}},
			{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}},
		} {
			payload, err := json.Marshal(ModelEmbedResponse{
				Embeddings: embeddings,
				Usage:      ModelUsage{InputTokens: 1},
			})
			if err != nil {
				t.Fatal(err)
			}
			provider := modelGatewayProviderForResponse(
				t,
				string(payload),
				"application/json",
				4096,
			)
			_, err = provider.Embed(ctx, ModelEmbedRequest{
				Scope:  scope,
				Model:  "model",
				Inputs: []string{"input"},
				Limits: modelGatewayTestLimits,
			})
			assertInvalidModelGatewayResponse(t, err)
		}
	})

	for name, items := range map[string][]ModelRerankItem{
		"unknown ID": {
			{ID: "unknown", Score: 0.9},
		},
		"duplicate ID": {
			{ID: "one", Score: 0.9},
			{ID: "one", Score: 0.8},
		},
		"ascending score": {
			{ID: "one", Score: 0.5},
			{ID: "two", Score: 0.9},
		},
	} {
		t.Run(name, func(t *testing.T) {
			payload, err := json.Marshal(ModelRerankResponse{
				Items: items,
				Usage: ModelUsage{InputTokens: 2},
			})
			if err != nil {
				t.Fatal(err)
			}
			provider := modelGatewayProviderForResponse(
				t,
				string(payload),
				"application/json",
				4096,
			)
			_, err = provider.Rerank(ctx, validModelGatewayRerankRequest(scope))
			assertInvalidModelGatewayResponse(t, err)
		})
	}
}

func TestHTTPModelGatewayProviderRequiresTrustedScopeAndAuthorization(
	t *testing.T,
) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		calls.Add(1)
		encodeModelGatewayTestResponse(t, writer, ModelGenerateResponse{
			Text:  "ok",
			Usage: ModelUsage{InputTokens: 1, OutputTokens: 1},
		})
	}))
	defer server.Close()
	scope := models.ProjectScope{OrganizationID: 51, ProjectID: 61}

	provider := newModelGatewayTestProvider(
		t,
		server.URL,
		server.Client(),
		modelGatewayNoopAuthorizer(),
		3,
		4096,
		time.Second,
	)
	_, err := provider.Generate(
		context.Background(),
		validModelGatewayGenerateRequest(scope),
	)
	if !errors.Is(err, ErrModelGatewayRequest) {
		t.Fatalf("missing context error = %v", err)
	}
	otherScope := models.ProjectScope{OrganizationID: 51, ProjectID: 62}
	_, err = provider.Generate(
		modelGatewayTestContext(t, otherScope),
		validModelGatewayGenerateRequest(scope),
	)
	if !errors.Is(err, ErrModelGatewayRequest) {
		t.Fatalf("mismatched scope error = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid control data made %d calls", calls.Load())
	}

	for name, authorizer := range map[string]ModelGatewayAuthorizer{
		"error": ModelGatewayAuthorizerFunc(func(
			context.Context,
			ModelGatewayAuthorizationInput,
		) (http.Header, error) {
			return nil, errors.New("secret acquisition failed")
		}),
		"reserved header": ModelGatewayAuthorizerFunc(func(
			context.Context,
			ModelGatewayAuthorizationInput,
		) (http.Header, error) {
			return http.Header{"Host": []string{"attacker.invalid"}}, nil
		}),
		"header injection": ModelGatewayAuthorizerFunc(func(
			context.Context,
			ModelGatewayAuthorizationInput,
		) (http.Header, error) {
			return http.Header{
				"Authorization": []string{"Bearer secret\r\nX-Evil: true"},
			}, nil
		}),
	} {
		t.Run(name, func(t *testing.T) {
			rejected := newModelGatewayTestProvider(
				t,
				server.URL,
				server.Client(),
				authorizer,
				3,
				4096,
				time.Second,
			)
			_, err := rejected.Generate(
				modelGatewayTestContext(t, scope),
				validModelGatewayGenerateRequest(scope),
			)
			if !errors.Is(err, ErrModelGatewayAuthorization) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("rejected authorization made %d calls", calls.Load())
	}
}

func TestHTTPModelGatewayProviderTimeoutAndCancellation(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		calls.Add(1)
		select {
		case <-request.Context().Done():
			return
		case <-time.After(time.Second):
			encodeModelGatewayTestResponse(t, writer, ModelGenerateResponse{
				Text:  "late",
				Usage: ModelUsage{InputTokens: 1, OutputTokens: 1},
			})
		}
	}))
	defer server.Close()
	scope := models.ProjectScope{OrganizationID: 51, ProjectID: 61}
	provider := newModelGatewayTestProvider(
		t,
		server.URL,
		server.Client(),
		modelGatewayNoopAuthorizer(),
		3,
		4096,
		20*time.Millisecond,
	)
	_, err := provider.Generate(
		modelGatewayTestContext(t, scope),
		validModelGatewayGenerateRequest(scope),
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}

	cancelled, cancel := context.WithCancel(modelGatewayTestContext(t, scope))
	cancel()
	_, err = provider.Generate(
		cancelled,
		validModelGatewayGenerateRequest(scope),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func newModelGatewayTestProvider(
	t *testing.T,
	endpoint string,
	client *http.Client,
	authorizer ModelGatewayAuthorizer,
	dimensions int,
	maxResponseBytes int64,
	timeout time.Duration,
) *HTTPModelGatewayProvider {
	t.Helper()
	provider, err := NewHTTPModelGatewayProvider(
		HTTPModelGatewayProviderConfig{
			ProviderKey:         "gateway",
			Endpoint:            endpoint,
			IsExternal:          true,
			Timeout:             timeout,
			MaxRequestBytes:     1 << 20,
			MaxResponseBytes:    maxResponseBytes,
			EmbeddingDimensions: dimensions,
		},
		client,
		authorizer,
	)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func modelGatewayProviderForResponse(
	t *testing.T,
	body string,
	contentType string,
	maxResponseBytes int64,
) *HTTPModelGatewayProvider {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.Header().Set("Content-Type", contentType)
		_, _ = io.WriteString(writer, body)
	}))
	t.Cleanup(server.Close)
	return newModelGatewayTestProvider(
		t,
		server.URL,
		server.Client(),
		modelGatewayNoopAuthorizer(),
		3,
		maxResponseBytes,
		time.Second,
	)
}

func modelGatewayNoopAuthorizer() ModelGatewayAuthorizer {
	return ModelGatewayAuthorizerFunc(func(
		context.Context,
		ModelGatewayAuthorizationInput,
	) (http.Header, error) {
		return http.Header{}, nil
	})
}

func modelGatewayTestContext(
	t *testing.T,
	scope models.ProjectScope,
) context.Context {
	t.Helper()
	ctx, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  scope,
			Actor:  models.HumanActor(7),
			Source: SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func validModelGatewayGenerateRequest(
	scope models.ProjectScope,
) ModelGenerateRequest {
	return ModelGenerateRequest{
		Scope:           scope,
		Model:           "model",
		Prompt:          "prompt",
		MaxOutputTokens: 4,
		Limits:          modelGatewayTestLimits,
	}
}

func validModelGatewayRerankRequest(
	scope models.ProjectScope,
) ModelRerankRequest {
	return ModelRerankRequest{
		Scope: scope,
		Model: "model",
		Query: "query",
		Candidates: []ModelRerankCandidate{
			{ID: "one", Content: "first"},
			{ID: "two", Content: "second"},
		},
		Limit:  2,
		Limits: modelGatewayTestLimits,
	}
}

func decodeModelGatewayTestRequest(
	t *testing.T,
	request *http.Request,
	destination any,
) {
	t.Helper()
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		t.Errorf("decode request: %v", err)
	}
}

func encodeModelGatewayTestResponse(
	t *testing.T,
	writer http.ResponseWriter,
	value any,
) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func assertInvalidModelGatewayResponse(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrKnowledgeModelResponseInvalid) {
		t.Fatalf("error = %v", err)
	}
}
