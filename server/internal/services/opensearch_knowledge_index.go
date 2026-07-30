package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

const openSearchKnowledgeMaxResponseBytes = 8 << 20

var openSearchControlNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// OpenSearchRequestAuthorizer adds deployment-owned authentication control
// data (for example Basic auth or AWS SigV4). It must never derive credentials
// or destinations from ticket, knowledge, or Agent content.
type OpenSearchRequestAuthorizer interface {
	Authorize(*http.Request) error
}

type OpenSearchRequestAuthorizerFunc func(*http.Request) error

func (function OpenSearchRequestAuthorizerFunc) Authorize(
	request *http.Request,
) error {
	return function(request)
}

type OpenSearchKnowledgeIndexOptions struct {
	Endpoint        string
	IndexPrefix     string
	SearchPipeline  string
	VectorDimension int
	HTTPClient      *http.Client
	Authorizer      OpenSearchRequestAuthorizer
	// AllowInsecureHTTP is only for an explicitly isolated development
	// network. Production composition must leave it false.
	AllowInsecureHTTP bool
}

// OpenSearchKnowledgeIndex stores one immutable generation per project and
// atomically moves a project-local alias after a successful bulk replacement.
// All search filters are part of the OpenSearch query, before score
// normalization and reranking.
type OpenSearchKnowledgeIndex struct {
	endpoint        *url.URL
	indexPrefix     string
	searchPipeline  string
	vectorDimension int
	client          *http.Client
	authorizer      OpenSearchRequestAuthorizer
}

func NewOpenSearchKnowledgeIndex(
	options OpenSearchKnowledgeIndexOptions,
) (*OpenSearchKnowledgeIndex, error) {
	endpoint, err := validateOpenSearchEndpoint(
		options.Endpoint,
		options.AllowInsecureHTTP,
	)
	if err != nil {
		return nil, err
	}
	if !openSearchControlNamePattern.MatchString(options.IndexPrefix) {
		return nil, errors.New("OpenSearch index prefix is invalid")
	}
	if !openSearchControlNamePattern.MatchString(options.SearchPipeline) {
		return nil, errors.New("OpenSearch search pipeline is invalid")
	}
	if options.VectorDimension < 1 || options.VectorDimension > 65535 {
		return nil, errors.New("OpenSearch vector dimension is invalid")
	}
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &OpenSearchKnowledgeIndex{
		endpoint:        endpoint,
		indexPrefix:     options.IndexPrefix,
		searchPipeline:  options.SearchPipeline,
		vectorDimension: options.VectorDimension,
		client:          options.HTTPClient,
		authorizer:      options.Authorizer,
	}, nil
}

// EnsureSearchPipeline creates the deterministic normalization pipeline used
// by every hybrid query. PUT is intentionally idempotent.
func (index *OpenSearchKnowledgeIndex) EnsureSearchPipeline(
	ctx context.Context,
) error {
	body := map[string]any{
		"description": "ChronoDesk knowledge hybrid score normalization",
		"phase_results_processors": []any{
			map[string]any{
				"normalization-processor": map[string]any{
					"normalization": map[string]any{
						"technique": "min_max",
					},
					"combination": map[string]any{
						"technique": "arithmetic_mean",
					},
				},
			},
		},
	}
	return index.doJSON(
		ctx,
		http.MethodPut,
		"/_search/pipeline/"+index.searchPipeline,
		body,
		nil,
		http.StatusOK,
		http.StatusCreated,
	)
}

func (index *OpenSearchKnowledgeIndex) Search(
	ctx context.Context,
	request HybridSearchRequest,
) ([]HybridSearchHit, error) {
	if ctx == nil {
		return nil, errors.New("OpenSearch search context is required")
	}
	if err := request.Filter.Validate(); err != nil {
		return nil, fmt.Errorf("OpenSearch knowledge filter: %w", err)
	}
	if strings.TrimSpace(request.Query) == "" ||
		request.Limit < 1 ||
		request.Limit > 100 ||
		len(request.QueryEmbedding) != index.vectorDimension {
		return nil, errors.New("OpenSearch knowledge search request is invalid")
	}
	aclValues := make([]string, 0, len(request.Filter.ACLSubjects))
	for _, subject := range request.Filter.ACLSubjects {
		aclValues = append(aclValues, openSearchACLSubject(subject))
	}
	commonFilter := map[string]any{
		"bool": map[string]any{
			"filter": []any{
				map[string]any{"term": map[string]any{
					"organization_id": request.Filter.OrganizationID,
				}},
				map[string]any{"term": map[string]any{
					"project_id": request.Filter.ProjectID,
				}},
				map[string]any{"term": map[string]any{
					"published": request.Filter.PublishedOnly,
				}},
				map[string]any{"term": map[string]any{
					"virus_scan": request.Filter.VirusScan,
				}},
				map[string]any{"terms": map[string]any{
					"acl_subjects": aclValues,
				}},
			},
		},
	}
	body := map[string]any{
		"size": request.Limit,
		"_source": map[string]any{
			"excludes": []string{"embedding", "content", "acl_subjects"},
		},
		"query": map[string]any{
			"hybrid": map[string]any{
				"filter": commonFilter,
				"queries": []any{
					map[string]any{
						"match": map[string]any{
							"content": map[string]any{
								"query": request.Query,
							},
						},
					},
					map[string]any{
						"knn": map[string]any{
							"embedding": map[string]any{
								"vector": request.QueryEmbedding,
								"k":      request.Limit,
							},
						},
					},
				},
			},
		},
	}
	response := struct {
		Hits struct {
			Hits []struct {
				Score  float64 `json:"_score"`
				Source struct {
					OrganizationID  uint   `json:"organization_id"`
					ProjectID       uint   `json:"project_id"`
					ArticleID       string `json:"article_id"`
					VersionID       string `json:"version_id"`
					DocumentVersion uint64 `json:"document_version"`
					ChunkID         string `json:"chunk_id"`
					PageNumber      *int   `json:"page_number"`
					Snippet         string `json:"snippet"`
					ContentHash     string `json:"content_hash"`
					TokenCount      int    `json:"token_count"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}{}
	path := "/" + index.projectAlias(
		request.Filter.OrganizationID,
		request.Filter.ProjectID,
	) + "/_search?search_pipeline=" + url.QueryEscape(index.searchPipeline)
	if err := index.doJSON(
		ctx,
		http.MethodPost,
		path,
		body,
		&response,
		http.StatusOK,
	); err != nil {
		return nil, err
	}
	results := make([]HybridSearchHit, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		source := hit.Source
		results = append(results, HybridSearchHit{
			OrganizationID:  source.OrganizationID,
			ProjectID:       source.ProjectID,
			ArticleID:       source.ArticleID,
			VersionID:       source.VersionID,
			DocumentVersion: source.DocumentVersion,
			ChunkID:         source.ChunkID,
			PageNumber:      source.PageNumber,
			Snippet:         source.Snippet,
			ContentHash:     source.ContentHash,
			Score:           hit.Score,
			TokenCount:      source.TokenCount,
		})
	}
	return results, nil
}

func (index *OpenSearchKnowledgeIndex) ReplaceProject(
	ctx context.Context,
	replacement HybridIndexReplacement,
) error {
	scope := models.ProjectScope{
		OrganizationID: replacement.OrganizationID,
		ProjectID:      replacement.ProjectID,
	}
	if ctx == nil {
		return errors.New("OpenSearch replacement context is required")
	}
	if err := scope.Validate(); err != nil {
		return fmt.Errorf("OpenSearch replacement scope: %w", err)
	}
	if replacement.Generation == 0 ||
		strings.TrimSpace(replacement.SourceDigest) == "" {
		return errors.New("OpenSearch replacement generation and digest are required")
	}
	seenChunks := make(map[string]struct{}, len(replacement.Documents))
	for _, document := range replacement.Documents {
		if document.OrganizationID != replacement.OrganizationID ||
			document.ProjectID != replacement.ProjectID ||
			strings.TrimSpace(document.ChunkID) == "" ||
			len(document.Embedding) != index.vectorDimension {
			return errors.New("OpenSearch replacement contains an invalid document")
		}
		if _, duplicate := seenChunks[document.ChunkID]; duplicate {
			return errors.New("OpenSearch replacement contains duplicate chunks")
		}
		seenChunks[document.ChunkID] = struct{}{}
		for _, subject := range document.ACLSubjects {
			if err := subject.Validate(); err != nil {
				return fmt.Errorf("OpenSearch document ACL: %w", err)
			}
		}
	}
	if err := index.EnsureSearchPipeline(ctx); err != nil {
		return err
	}
	indexName := index.projectGenerationIndex(
		replacement.OrganizationID,
		replacement.ProjectID,
		replacement.Generation,
	)
	// A failed external replacement may leave the desired (never-current)
	// generation behind. Recreate it from PostgreSQL authority on retry rather
	// than accepting a partial bulk index.
	if err := index.deleteGenerationIndex(ctx, indexName); err != nil {
		return err
	}
	if err := index.createGenerationIndex(ctx, indexName); err != nil {
		return err
	}
	if err := index.bulkIndexDocuments(
		ctx,
		indexName,
		replacement.Documents,
	); err != nil {
		return err
	}
	return index.moveProjectAlias(
		ctx,
		indexName,
		replacement.OrganizationID,
		replacement.ProjectID,
	)
}

func (index *OpenSearchKnowledgeIndex) deleteGenerationIndex(
	ctx context.Context,
	indexName string,
) error {
	status, err := index.doJSONStatus(
		ctx,
		http.MethodDelete,
		"/"+indexName,
		nil,
		nil,
	)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusNotFound {
		return fmt.Errorf(
			"delete stale OpenSearch generation returned status %d",
			status,
		)
	}
	return nil
}

func (index *OpenSearchKnowledgeIndex) createGenerationIndex(
	ctx context.Context,
	indexName string,
) error {
	body := map[string]any{
		"settings": map[string]any{
			"index.knn": true,
		},
		"mappings": map[string]any{
			"dynamic": "strict",
			"properties": map[string]any{
				"organization_id":  map[string]any{"type": "long"},
				"project_id":       map[string]any{"type": "long"},
				"article_id":       map[string]any{"type": "keyword"},
				"version_id":       map[string]any{"type": "keyword"},
				"document_version": map[string]any{"type": "long"},
				"chunk_id":         map[string]any{"type": "keyword"},
				"page_number":      map[string]any{"type": "integer"},
				"content":          map[string]any{"type": "text"},
				"snippet":          map[string]any{"type": "text", "index": false},
				"content_hash":     map[string]any{"type": "keyword"},
				"token_count":      map[string]any{"type": "integer"},
				"acl_subjects":     map[string]any{"type": "keyword"},
				"published":        map[string]any{"type": "boolean"},
				"virus_scan":       map[string]any{"type": "keyword"},
				"embedding": map[string]any{
					"type":       "knn_vector",
					"dimension":  index.vectorDimension,
					"space_type": "cosinesimil",
				},
			},
		},
	}
	return index.doJSON(
		ctx,
		http.MethodPut,
		"/"+indexName,
		body,
		nil,
		http.StatusOK,
		http.StatusCreated,
	)
}

func (index *OpenSearchKnowledgeIndex) bulkIndexDocuments(
	ctx context.Context,
	indexName string,
	documents []HybridIndexDocument,
) error {
	if len(documents) == 0 {
		return nil
	}
	var payload bytes.Buffer
	encoder := json.NewEncoder(&payload)
	encoder.SetEscapeHTML(true)
	for _, document := range documents {
		if err := encoder.Encode(map[string]any{
			"index": map[string]any{
				"_index": indexName,
				"_id":    document.ChunkID,
			},
		}); err != nil {
			return err
		}
		aclSubjects := make([]string, 0, len(document.ACLSubjects))
		for _, subject := range document.ACLSubjects {
			aclSubjects = append(aclSubjects, openSearchACLSubject(subject))
		}
		if err := encoder.Encode(map[string]any{
			"organization_id":  document.OrganizationID,
			"project_id":       document.ProjectID,
			"article_id":       document.ArticleID,
			"version_id":       document.VersionID,
			"document_version": document.DocumentVersion,
			"chunk_id":         document.ChunkID,
			"page_number":      document.PageNumber,
			"content":          document.Content,
			"snippet":          document.Snippet,
			"content_hash":     document.ContentHash,
			"token_count":      document.TokenCount,
			"acl_subjects":     aclSubjects,
			"published":        true,
			"virus_scan":       models.VirusScanClean,
			"embedding":        document.Embedding,
		}); err != nil {
			return err
		}
	}
	response := struct {
		Errors bool `json:"errors"`
	}{}
	if err := index.doRequest(
		ctx,
		http.MethodPost,
		"/_bulk?refresh=wait_for",
		"application/x-ndjson",
		&payload,
		&response,
		http.StatusOK,
	); err != nil {
		return err
	}
	if response.Errors {
		return errors.New("OpenSearch bulk replacement contains failed items")
	}
	return nil
}

func (index *OpenSearchKnowledgeIndex) moveProjectAlias(
	ctx context.Context,
	indexName string,
	organizationID uint,
	projectID uint,
) error {
	alias := index.projectAlias(organizationID, projectID)
	existing := struct {
		Indices map[string]json.RawMessage
	}{}
	status, err := index.doJSONStatus(
		ctx,
		http.MethodGet,
		"/_alias/"+alias,
		nil,
		&existing.Indices,
	)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusNotFound {
		return fmt.Errorf("OpenSearch alias lookup returned status %d", status)
	}
	actions := make([]any, 0, len(existing.Indices)+1)
	for currentIndex := range existing.Indices {
		actions = append(actions, map[string]any{
			"remove": map[string]any{
				"index": currentIndex,
				"alias": alias,
			},
		})
	}
	actions = append(actions, map[string]any{
		"add": map[string]any{
			"index": indexName,
			"alias": alias,
		},
	})
	return index.doJSON(
		ctx,
		http.MethodPost,
		"/_aliases",
		map[string]any{"actions": actions},
		nil,
		http.StatusOK,
	)
}

func (index *OpenSearchKnowledgeIndex) doJSON(
	ctx context.Context,
	method string,
	path string,
	body any,
	response any,
	expectedStatus ...int,
) error {
	var encoded io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode OpenSearch request: %w", err)
		}
		encoded = bytes.NewReader(payload)
	}
	return index.doRequest(
		ctx,
		method,
		path,
		"application/json",
		encoded,
		response,
		expectedStatus...,
	)
}

func (index *OpenSearchKnowledgeIndex) doJSONStatus(
	ctx context.Context,
	method string,
	path string,
	body any,
	response any,
) (int, error) {
	var encoded io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		encoded = bytes.NewReader(payload)
	}
	return index.doRequestStatus(
		ctx,
		method,
		path,
		"application/json",
		encoded,
		response,
	)
}

func (index *OpenSearchKnowledgeIndex) doRequest(
	ctx context.Context,
	method string,
	path string,
	contentType string,
	body io.Reader,
	response any,
	expectedStatus ...int,
) error {
	status, err := index.doRequestStatus(
		ctx,
		method,
		path,
		contentType,
		body,
		response,
	)
	if err != nil {
		return err
	}
	for _, expected := range expectedStatus {
		if status == expected {
			return nil
		}
	}
	return fmt.Errorf("OpenSearch returned unexpected status %d", status)
}

func (index *OpenSearchKnowledgeIndex) doRequestStatus(
	ctx context.Context,
	method string,
	path string,
	contentType string,
	body io.Reader,
	response any,
) (int, error) {
	if ctx == nil {
		return 0, errors.New("OpenSearch request context is required")
	}
	if err := requireExternalIOOutsideProjectTransaction(
		ctx,
		"OpenSearch HTTP request",
	); err != nil {
		return 0, err
	}
	requestURL := strings.TrimRight(index.endpoint.String(), "/") + path
	request, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return 0, fmt.Errorf("create OpenSearch request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", contentType)
	}
	if index.authorizer != nil {
		if err := index.authorizer.Authorize(request); err != nil {
			return 0, fmt.Errorf("authorize OpenSearch request: %w", err)
		}
	}
	httpResponse, err := index.client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("execute OpenSearch request: %w", err)
	}
	defer httpResponse.Body.Close()
	limited := io.LimitReader(
		httpResponse.Body,
		openSearchKnowledgeMaxResponseBytes+1,
	)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return 0, fmt.Errorf("read OpenSearch response: %w", err)
	}
	if len(payload) > openSearchKnowledgeMaxResponseBytes {
		return 0, errors.New("OpenSearch response exceeds the configured limit")
	}
	if response != nil &&
		len(bytes.TrimSpace(payload)) > 0 &&
		httpResponse.StatusCode >= 200 &&
		httpResponse.StatusCode < 300 {
		if err := json.Unmarshal(payload, response); err != nil {
			return 0, fmt.Errorf("decode OpenSearch response: %w", err)
		}
	}
	return httpResponse.StatusCode, nil
}

func (index *OpenSearchKnowledgeIndex) projectAlias(
	organizationID uint,
	projectID uint,
) string {
	return index.indexPrefix + "-" +
		strconv.FormatUint(uint64(organizationID), 10) + "-" +
		strconv.FormatUint(uint64(projectID), 10) + "-current"
}

func (index *OpenSearchKnowledgeIndex) projectGenerationIndex(
	organizationID uint,
	projectID uint,
	generation uint64,
) string {
	return index.indexPrefix + "-" +
		strconv.FormatUint(uint64(organizationID), 10) + "-" +
		strconv.FormatUint(uint64(projectID), 10) + "-" +
		strconv.FormatUint(generation, 10)
}

func openSearchACLSubject(subject models.KnowledgeACLSubject) string {
	return string(subject.Type) + ":" + subject.ID
}

func validateOpenSearchEndpoint(
	value string,
	allowInsecureHTTP bool,
) (*url.URL, error) {
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	if err != nil ||
		!parsed.IsAbs() ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("OpenSearch endpoint must be a canonical origin")
	}
	if parsed.Scheme != "https" {
		host := strings.ToLower(parsed.Hostname())
		ip := net.ParseIP(host)
		isLoopback := host == "localhost" ||
			(ip != nil && ip.IsLoopback())
		if parsed.Scheme != "http" ||
			(!isLoopback && !allowInsecureHTTP) {
			return nil, errors.New(
				"OpenSearch endpoint must use HTTPS except for loopback development",
			)
		}
	}
	parsed.Path = ""
	return parsed, nil
}
