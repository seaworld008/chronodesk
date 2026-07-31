package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

const (
	maxModelGatewayPayloadBytes = int64(16 << 20)
	maxModelGatewayTimeout      = 5 * time.Minute
	maxModelGatewayModelLength  = 160
	maxModelGatewayCandidateID  = 256
	maxModelGatewayOutputTokens = 1000000
)

var (
	ErrModelGatewayConfiguration = errors.New(
		"model Gateway configuration is invalid",
	)
	ErrModelGatewayRequest = errors.New(
		"model Gateway request is invalid",
	)
	ErrModelGatewayAuthorization = errors.New(
		"model Gateway authorization failed",
	)
	ErrModelGatewayUnavailable = errors.New(
		"model Gateway is unavailable",
	)
	ErrModelGatewayStatus = errors.New(
		"model Gateway returned an unsuccessful status",
	)
)

// HTTPModelGatewayProviderConfig is deployment control data. Endpoint never
// comes from a Project policy, prompt, document, candidate, or other untrusted
// content.
type HTTPModelGatewayProviderConfig struct {
	ProviderKey         string
	Endpoint            string
	IsExternal          bool
	Timeout             time.Duration
	MaxRequestBytes     int64
	MaxResponseBytes    int64
	EmbeddingDimensions int
}

// ModelGatewayAuthorizationInput contains only immutable request metadata. An
// authorizer returns headers and therefore cannot rewrite the destination,
// method, or body.
type ModelGatewayAuthorizationInput struct {
	Method     string
	URL        string
	BodySHA256 string
}

type ModelGatewayAuthorizer interface {
	AuthorizationHeaders(
		ctx context.Context,
		input ModelGatewayAuthorizationInput,
	) (http.Header, error)
}

type ModelGatewayAuthorizerFunc func(
	context.Context,
	ModelGatewayAuthorizationInput,
) (http.Header, error)

func (function ModelGatewayAuthorizerFunc) AuthorizationHeaders(
	ctx context.Context,
	input ModelGatewayAuthorizationInput,
) (http.Header, error) {
	if function == nil {
		return nil, ErrModelGatewayAuthorization
	}
	return function(ctx, input)
}

type HTTPModelGatewayProvider struct {
	descriptor          ModelProviderDescriptor
	endpoint            url.URL
	timeout             time.Duration
	maxRequestBytes     int64
	maxResponseBytes    int64
	embeddingDimensions int
	client              *http.Client
	authorizer          ModelGatewayAuthorizer
}

var _ ModelProvider = (*HTTPModelGatewayProvider)(nil)

func NewHTTPModelGatewayProvider(
	config HTTPModelGatewayProviderConfig,
	client *http.Client,
	authorizer ModelGatewayAuthorizer,
) (*HTTPModelGatewayProvider, error) {
	providerKey := strings.TrimSpace(config.ProviderKey)
	if providerKey == "" ||
		providerKey != config.ProviderKey ||
		len(providerKey) > 64 ||
		!utf8.ValidString(providerKey) ||
		containsModelGatewayControlCharacter(providerKey) {
		return nil, ErrModelGatewayConfiguration
	}
	endpoint, err := parseModelGatewayEndpoint(config.Endpoint)
	if err != nil {
		return nil, ErrModelGatewayConfiguration
	}
	if config.Timeout <= 0 || config.Timeout > maxModelGatewayTimeout ||
		config.MaxRequestBytes <= 0 ||
		config.MaxRequestBytes > maxModelGatewayPayloadBytes ||
		config.MaxResponseBytes <= 0 ||
		config.MaxResponseBytes > maxModelGatewayPayloadBytes ||
		config.EmbeddingDimensions < 1 ||
		config.EmbeddingDimensions > 65536 ||
		authorizer == nil {
		return nil, ErrModelGatewayConfiguration
	}
	if client == nil {
		client = &http.Client{}
	}
	clientCopy := *client
	clientCopy.Jar = nil
	// Redirects are rejected rather than followed, including redirects that
	// preserve the original host. The sole configured endpoint remains the only
	// network destination this provider can select.
	clientCopy.CheckRedirect = func(
		_ *http.Request,
		_ []*http.Request,
	) error {
		return http.ErrUseLastResponse
	}
	return &HTTPModelGatewayProvider{
		descriptor: ModelProviderDescriptor{
			Key:        providerKey,
			IsExternal: config.IsExternal,
		},
		endpoint:            *endpoint,
		timeout:             config.Timeout,
		maxRequestBytes:     config.MaxRequestBytes,
		maxResponseBytes:    config.MaxResponseBytes,
		embeddingDimensions: config.EmbeddingDimensions,
		client:              &clientCopy,
		authorizer:          authorizer,
	}, nil
}

func (provider *HTTPModelGatewayProvider) Descriptor() ModelProviderDescriptor {
	if provider == nil {
		return ModelProviderDescriptor{}
	}
	return provider.descriptor
}

func (provider *HTTPModelGatewayProvider) Generate(
	ctx context.Context,
	request ModelGenerateRequest,
) (ModelGenerateResponse, error) {
	if err := provider.validateCallControl(
		ctx,
		request.Scope,
		request.Model,
		request.Limits,
	); err != nil {
		return ModelGenerateResponse{}, err
	}
	if strings.TrimSpace(request.Prompt) == "" ||
		!utf8.ValidString(request.Prompt) ||
		request.MaxOutputTokens < 1 ||
		request.MaxOutputTokens > maxModelGatewayOutputTokens ||
		(request.Limits.MonthlyTokenBudget > 0 &&
			int64(request.MaxOutputTokens) >
				request.Limits.MonthlyTokenBudget) ||
		(request.Limits.TokensPerMinute > 0 &&
			request.MaxOutputTokens > request.Limits.TokensPerMinute) {
		return ModelGenerateResponse{}, ErrModelGatewayRequest
	}
	payload := modelGatewayGeneratePayload{
		Provider:        provider.descriptor.Key,
		Scope:           request.Scope,
		Model:           request.Model,
		Prompt:          request.Prompt,
		MaxOutputTokens: request.MaxOutputTokens,
		Limits:          request.Limits,
	}
	var response ModelGenerateResponse
	if err := provider.post(ctx, "generate", payload, &response); err != nil {
		return ModelGenerateResponse{}, err
	}
	if strings.TrimSpace(response.Text) == "" ||
		!utf8.ValidString(response.Text) ||
		validateModelGatewayUsage(response.Usage, request.Limits) != nil ||
		response.Usage.OutputTokens > request.MaxOutputTokens {
		return ModelGenerateResponse{}, ErrKnowledgeModelResponseInvalid
	}
	return response, nil
}

func (provider *HTTPModelGatewayProvider) Embed(
	ctx context.Context,
	request ModelEmbedRequest,
) (ModelEmbedResponse, error) {
	if err := provider.validateCallControl(
		ctx,
		request.Scope,
		request.Model,
		request.Limits,
	); err != nil {
		return ModelEmbedResponse{}, err
	}
	if len(request.Inputs) == 0 {
		return ModelEmbedResponse{}, ErrModelGatewayRequest
	}
	for _, input := range request.Inputs {
		if strings.TrimSpace(input) == "" || !utf8.ValidString(input) {
			return ModelEmbedResponse{}, ErrModelGatewayRequest
		}
	}
	payload := modelGatewayEmbedPayload{
		Provider: provider.descriptor.Key,
		Scope:    request.Scope,
		Model:    request.Model,
		Inputs:   append([]string(nil), request.Inputs...),
		Limits:   request.Limits,
	}
	var response ModelEmbedResponse
	if err := provider.post(ctx, "embed", payload, &response); err != nil {
		return ModelEmbedResponse{}, err
	}
	if len(response.Embeddings) != len(request.Inputs) ||
		validateModelGatewayUsage(response.Usage, request.Limits) != nil {
		return ModelEmbedResponse{}, ErrKnowledgeModelResponseInvalid
	}
	for _, embedding := range response.Embeddings {
		if len(embedding) != provider.embeddingDimensions {
			return ModelEmbedResponse{}, ErrKnowledgeModelResponseInvalid
		}
		for _, value := range embedding {
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return ModelEmbedResponse{}, ErrKnowledgeModelResponseInvalid
			}
		}
	}
	return response, nil
}

func (provider *HTTPModelGatewayProvider) Rerank(
	ctx context.Context,
	request ModelRerankRequest,
) (ModelRerankResponse, error) {
	if err := provider.validateCallControl(
		ctx,
		request.Scope,
		request.Model,
		request.Limits,
	); err != nil {
		return ModelRerankResponse{}, err
	}
	if strings.TrimSpace(request.Query) == "" ||
		!utf8.ValidString(request.Query) ||
		len(request.Candidates) == 0 ||
		request.Limit < 1 ||
		request.Limit > len(request.Candidates) {
		return ModelRerankResponse{}, ErrModelGatewayRequest
	}
	candidateIDs := make(map[string]struct{}, len(request.Candidates))
	candidates := make([]ModelRerankCandidate, len(request.Candidates))
	for index, candidate := range request.Candidates {
		if candidate.ID == "" ||
			candidate.ID != strings.TrimSpace(candidate.ID) ||
			len(candidate.ID) > maxModelGatewayCandidateID ||
			!utf8.ValidString(candidate.ID) ||
			containsModelGatewayControlCharacter(candidate.ID) ||
			strings.TrimSpace(candidate.Content) == "" ||
			!utf8.ValidString(candidate.Content) {
			return ModelRerankResponse{}, ErrModelGatewayRequest
		}
		if _, duplicate := candidateIDs[candidate.ID]; duplicate {
			return ModelRerankResponse{}, ErrModelGatewayRequest
		}
		candidateIDs[candidate.ID] = struct{}{}
		candidates[index] = candidate
	}
	payload := modelGatewayRerankPayload{
		Provider:   provider.descriptor.Key,
		Scope:      request.Scope,
		Model:      request.Model,
		Query:      request.Query,
		Candidates: candidates,
		Limit:      request.Limit,
		Limits:     request.Limits,
	}
	var response ModelRerankResponse
	if err := provider.post(ctx, "rerank", payload, &response); err != nil {
		return ModelRerankResponse{}, err
	}
	if len(response.Items) == 0 ||
		len(response.Items) > request.Limit ||
		validateModelGatewayUsage(response.Usage, request.Limits) != nil {
		return ModelRerankResponse{}, ErrKnowledgeModelResponseInvalid
	}
	seen := make(map[string]struct{}, len(response.Items))
	previousScore := math.Inf(1)
	for _, item := range response.Items {
		if item.ID == "" ||
			item.ID != strings.TrimSpace(item.ID) ||
			math.IsNaN(item.Score) ||
			math.IsInf(item.Score, 0) ||
			item.Score > previousScore {
			return ModelRerankResponse{}, ErrKnowledgeModelResponseInvalid
		}
		if _, exists := candidateIDs[item.ID]; !exists {
			return ModelRerankResponse{}, ErrKnowledgeModelResponseInvalid
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return ModelRerankResponse{}, ErrKnowledgeModelResponseInvalid
		}
		seen[item.ID] = struct{}{}
		previousScore = item.Score
	}
	return response, nil
}

type modelGatewayGeneratePayload struct {
	Provider        string              `json:"provider"`
	Scope           models.ProjectScope `json:"scope"`
	Model           string              `json:"model"`
	Prompt          string              `json:"prompt"`
	MaxOutputTokens int                 `json:"max_output_tokens"`
	Limits          ModelCallLimits     `json:"limits"`
}

type modelGatewayEmbedPayload struct {
	Provider string              `json:"provider"`
	Scope    models.ProjectScope `json:"scope"`
	Model    string              `json:"model"`
	Inputs   []string            `json:"inputs"`
	Limits   ModelCallLimits     `json:"limits"`
}

type modelGatewayRerankPayload struct {
	Provider   string                 `json:"provider"`
	Scope      models.ProjectScope    `json:"scope"`
	Model      string                 `json:"model"`
	Query      string                 `json:"query"`
	Candidates []ModelRerankCandidate `json:"candidates"`
	Limit      int                    `json:"limit"`
	Limits     ModelCallLimits        `json:"limits"`
}

func (provider *HTTPModelGatewayProvider) validateCallControl(
	ctx context.Context,
	scope models.ProjectScope,
	model string,
	limits ModelCallLimits,
) error {
	if provider == nil ||
		provider.client == nil ||
		provider.authorizer == nil ||
		ctx == nil {
		return ErrModelGatewayRequest
	}
	if err := scope.Validate(); err != nil {
		return ErrModelGatewayRequest
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil || operation.Scope != scope {
		return ErrModelGatewayRequest
	}
	if model == "" ||
		model != strings.TrimSpace(model) ||
		len(model) > maxModelGatewayModelLength ||
		!utf8.ValidString(model) ||
		containsModelGatewayControlCharacter(model) ||
		limits.MonthlyTokenBudget < 0 ||
		limits.MonthlyCostBudgetMicros < 0 ||
		limits.RequestsPerMinute < 0 ||
		limits.TokensPerMinute < 0 {
		return ErrModelGatewayRequest
	}
	return nil
}

func (provider *HTTPModelGatewayProvider) post(
	ctx context.Context,
	operation string,
	payload any,
	destination any,
) error {
	if err := requireExternalIOOutsideProjectTransaction(
		ctx,
		"model Gateway HTTP "+operation,
	); err != nil {
		return err
	}
	encoded, err := json.Marshal(payload)
	if err != nil ||
		int64(len(encoded)) > provider.maxRequestBytes {
		return ErrModelGatewayRequest
	}
	callContext, cancel := context.WithTimeout(ctx, provider.timeout)
	defer cancel()

	target := provider.operationURL(operation)
	request, err := http.NewRequestWithContext(
		callContext,
		http.MethodPost,
		target,
		bytes.NewReader(encoded),
	)
	if err != nil {
		return ErrModelGatewayRequest
	}
	digest := sha256.Sum256(encoded)
	headers, err := provider.authorizer.AuthorizationHeaders(
		callContext,
		ModelGatewayAuthorizationInput{
			Method:     http.MethodPost,
			URL:        target,
			BodySHA256: hex.EncodeToString(digest[:]),
		},
	)
	if err != nil {
		return ErrModelGatewayAuthorization
	}
	if err := applyModelGatewayAuthorizationHeaders(request, headers); err != nil {
		return ErrModelGatewayAuthorization
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")

	response, err := provider.client.Do(request)
	if err != nil {
		if callContext.Err() != nil {
			return callContext.Err()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrModelGatewayUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: HTTP %d", ErrModelGatewayStatus, response.StatusCode)
	}
	if !modelGatewayJSONContentType(response.Header.Get("Content-Type")) {
		return ErrKnowledgeModelResponseInvalid
	}
	if response.ContentLength > provider.maxResponseBytes {
		return ErrKnowledgeModelResponseInvalid
	}
	body, err := io.ReadAll(io.LimitReader(
		response.Body,
		provider.maxResponseBytes+1,
	))
	if err != nil || int64(len(body)) > provider.maxResponseBytes ||
		len(body) == 0 || !utf8.Valid(body) {
		return ErrKnowledgeModelResponseInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return ErrKnowledgeModelResponseInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrKnowledgeModelResponseInvalid
	}
	return nil
}

func (provider *HTTPModelGatewayProvider) operationURL(
	operation string,
) string {
	target := provider.endpoint
	target.Path = strings.TrimSuffix(target.Path, "/") + "/" + operation
	target.RawPath = ""
	return target.String()
}

func parseModelGatewayEndpoint(raw string) (*url.URL, error) {
	if raw == "" || raw != strings.TrimSpace(raw) {
		return nil, ErrModelGatewayConfiguration
	}
	endpoint, err := url.Parse(raw)
	if err != nil ||
		endpoint.Opaque != "" ||
		endpoint.User != nil ||
		endpoint.Hostname() == "" ||
		endpoint.RawQuery != "" ||
		endpoint.Fragment != "" ||
		endpoint.RawPath != "" {
		return nil, ErrModelGatewayConfiguration
	}
	switch strings.ToLower(endpoint.Scheme) {
	case "https":
	case "http":
		if !modelGatewayLoopbackHost(endpoint.Hostname()) {
			return nil, ErrModelGatewayConfiguration
		}
	default:
		return nil, ErrModelGatewayConfiguration
	}
	if !validModelGatewayBasePath(endpoint.Path) {
		return nil, ErrModelGatewayConfiguration
	}
	endpoint.Scheme = strings.ToLower(endpoint.Scheme)
	endpoint.Path = strings.TrimSuffix(endpoint.Path, "/")
	return endpoint, nil
}

func validModelGatewayBasePath(value string) bool {
	if value == "" || value == "/" {
		return true
	}
	if !strings.HasPrefix(value, "/") ||
		strings.Contains(value, "\\") ||
		path.Clean(value) != strings.TrimSuffix(value, "/") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func modelGatewayLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func modelGatewayJSONContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}
	mediaType = strings.ToLower(mediaType)
	return mediaType == "application/json" ||
		(strings.HasPrefix(mediaType, "application/") &&
			strings.HasSuffix(mediaType, "+json"))
}

func applyModelGatewayAuthorizationHeaders(
	request *http.Request,
	headers http.Header,
) error {
	if request == nil {
		return ErrModelGatewayAuthorization
	}
	for name, values := range headers {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
		if canonical == "" ||
			!validModelGatewayHeaderName(strings.TrimSpace(name)) ||
			reservedModelGatewayHeader(canonical) ||
			len(values) == 0 {
			return ErrModelGatewayAuthorization
		}
		for _, value := range values {
			if value == "" ||
				strings.ContainsAny(value, "\r\n\x00") {
				return ErrModelGatewayAuthorization
			}
			request.Header.Add(canonical, value)
		}
	}
	return nil
}

func validModelGatewayHeaderName(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", character):
		default:
			return false
		}
	}
	return true
}

func reservedModelGatewayHeader(name string) bool {
	switch strings.ToLower(name) {
	case "accept",
		"connection",
		"content-length",
		"content-type",
		"forwarded",
		"host",
		"proxy-authorization",
		"te",
		"trailer",
		"transfer-encoding",
		"upgrade",
		"x-forwarded-for",
		"x-forwarded-host",
		"x-forwarded-proto":
		return true
	default:
		return false
	}
}

func validateModelGatewayUsage(
	usage ModelUsage,
	limits ModelCallLimits,
) error {
	if usage.InputTokens < 0 ||
		usage.OutputTokens < 0 ||
		usage.CostMicros < 0 {
		return ErrKnowledgeModelResponseInvalid
	}
	totalTokens := int64(usage.InputTokens) + int64(usage.OutputTokens)
	if totalTokens <= 0 ||
		(limits.MonthlyTokenBudget > 0 &&
			totalTokens > limits.MonthlyTokenBudget) ||
		(limits.TokensPerMinute > 0 &&
			totalTokens > int64(limits.TokensPerMinute)) ||
		(limits.MonthlyCostBudgetMicros > 0 &&
			usage.CostMicros > limits.MonthlyCostBudgetMicros) {
		return ErrKnowledgeModelResponseInvalid
	}
	return nil
}

func containsModelGatewayControlCharacter(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}
