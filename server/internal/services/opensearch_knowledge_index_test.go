package services

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

type openSearchRecordedRequest struct {
	Method string
	URL    string
	Body   []byte
}

func TestOpenSearchKnowledgeSearchPushesScopeAndACLIntoHybridFilter(
	t *testing.T,
) {
	var recorded openSearchRecordedRequest
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		body, _ := io.ReadAll(request.Body)
		recorded = openSearchRecordedRequest{
			Method: request.Method,
			URL:    request.URL.String(),
			Body:   body,
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"hits":{"hits":[{
				"_score":0.91,
				"_source":{
					"organization_id":7,
					"project_id":11,
					"article_id":"article-1",
					"version_id":"version-1",
					"document_version":3,
					"chunk_id":"chunk-1",
					"page_number":2,
					"snippet":"safe result",
					"content_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
					"token_count":12
				}
			}]}
		}`))
	}))
	defer server.Close()
	index := newOpenSearchKnowledgeTestIndex(t, server.URL, server.Client())

	hits, err := index.Search(context.Background(), HybridSearchRequest{
		Query:          "database timeout",
		QueryEmbedding: []float32{0.1, 0.2, 0.3},
		Limit:          8,
		Filter: HybridSearchFilter{
			OrganizationID: 7,
			ProjectID:      11,
			ACLSubjects: []models.KnowledgeACLSubject{
				{Type: models.KnowledgeACLHuman, ID: "42"},
				{Type: models.KnowledgeACLAllProject, ID: "*"},
			},
			PublishedOnly: true,
			VirusScan:     models.VirusScanClean,
		},
	})
	if err != nil {
		t.Fatalf("Search(): %v", err)
	}
	if len(hits) != 1 ||
		hits[0].OrganizationID != 7 ||
		hits[0].ProjectID != 11 ||
		hits[0].ChunkID != "chunk-1" {
		t.Fatalf("unexpected search hits: %+v", hits)
	}
	if recorded.Method != http.MethodPost ||
		recorded.URL !=
			"/knowledge-7-11-current/_search?search_pipeline=knowledge-hybrid" {
		t.Fatalf("search request = %s %s", recorded.Method, recorded.URL)
	}
	var body map[string]any
	if err := json.Unmarshal(recorded.Body, &body); err != nil {
		t.Fatalf("decode search request: %v", err)
	}
	if _, exists := body["post_filter"]; exists {
		t.Fatal("project/ACL boundary was incorrectly implemented as a post filter")
	}
	query := openSearchTestMap(t, body["query"], "query")
	hybrid := openSearchTestMap(t, query["hybrid"], "query.hybrid")
	commonFilter := openSearchTestMap(t, hybrid["filter"], "query.hybrid.filter")
	filterJSON, _ := json.Marshal(commonFilter)
	filterText := string(filterJSON)
	for _, required := range []string{
		`"organization_id":7`,
		`"project_id":11`,
		`"published":true`,
		`"virus_scan":"clean"`,
		`"human:42"`,
		`"all_project:*"`,
	} {
		if !strings.Contains(filterText, required) {
			t.Errorf("hybrid common filter %s omits %s", filterText, required)
		}
	}
	queries, ok := hybrid["queries"].([]any)
	if !ok || len(queries) != 2 {
		t.Fatalf("hybrid queries = %#v", hybrid["queries"])
	}
}

func TestOpenSearchKnowledgeReplacementBuildsImmutableGenerationAndMovesAlias(
	t *testing.T,
) {
	var mutex sync.Mutex
	var requests []openSearchRecordedRequest
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		body, _ := io.ReadAll(request.Body)
		mutex.Lock()
		requests = append(requests, openSearchRecordedRequest{
			Method: request.Method,
			URL:    request.URL.String(),
			Body:   body,
		})
		mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet &&
			request.URL.Path == "/_alias/knowledge-7-11-current":
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"status":404}`))
		case request.URL.Path == "/_bulk":
			_, _ = writer.Write([]byte(`{"errors":false,"items":[]}`))
		default:
			_, _ = writer.Write([]byte(`{"acknowledged":true}`))
		}
	}))
	defer server.Close()
	index := newOpenSearchKnowledgeTestIndex(t, server.URL, server.Client())

	err := index.ReplaceProject(context.Background(), HybridIndexReplacement{
		OrganizationID: 7,
		ProjectID:      11,
		Generation:     4,
		SourceDigest:   strings.Repeat("a", 64),
		Documents: []HybridIndexDocument{{
			OrganizationID:  7,
			ProjectID:       11,
			ArticleID:       "article-1",
			VersionID:       "version-1",
			DocumentVersion: 3,
			ChunkID:         "chunk-1",
			Content:         "untrusted <script>content</script>",
			Embedding:       []float32{0.1, 0.2, 0.3},
			Snippet:         "untrusted snippet",
			ContentHash:     strings.Repeat("b", 64),
			TokenCount:      12,
			ACLSubjects: []models.KnowledgeACLSubject{{
				Type: models.KnowledgeACLAllProject,
				ID:   "*",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("ReplaceProject(): %v", err)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(requests) != 6 {
		t.Fatalf("OpenSearch replacement requests = %d, want 6: %+v", len(requests), requests)
	}
	if requests[0].Method != http.MethodPut ||
		requests[0].URL != "/_search/pipeline/knowledge-hybrid" {
		t.Fatalf("pipeline request = %+v", requests[0])
	}
	if requests[1].Method != http.MethodDelete ||
		requests[1].URL != "/knowledge-7-11-4" {
		t.Fatalf("stale generation cleanup request = %+v", requests[1])
	}
	if requests[2].Method != http.MethodPut ||
		requests[2].URL != "/knowledge-7-11-4" {
		t.Fatalf("generation creation request = %+v", requests[2])
	}
	if !strings.Contains(string(requests[2].Body), `"dimension":3`) ||
		!strings.Contains(string(requests[2].Body), `"dynamic":"strict"`) {
		t.Fatalf("generation mapping is not strict/vector aware: %s", requests[2].Body)
	}
	if requests[3].URL != "/_bulk?refresh=wait_for" ||
		!strings.Contains(string(requests[3].Body), `"acl_subjects":["all_project:*"]`) ||
		!strings.Contains(string(requests[3].Body), `"embedding":[0.1,0.2,0.3]`) {
		t.Fatalf("bulk replacement body is incomplete: %s", requests[3].Body)
	}
	if requests[5].URL != "/_aliases" ||
		!strings.Contains(string(requests[5].Body), `"alias":"knowledge-7-11-current"`) ||
		!strings.Contains(string(requests[5].Body), `"index":"knowledge-7-11-4"`) {
		t.Fatalf("alias move is incomplete: %s", requests[5].Body)
	}
}

func TestOpenSearchKnowledgeIndexRejectsRemotePlainHTTPAndBadDocuments(
	t *testing.T,
) {
	if _, err := NewOpenSearchKnowledgeIndex(OpenSearchKnowledgeIndexOptions{
		Endpoint:        "http://search.example.com:9200",
		IndexPrefix:     "knowledge",
		SearchPipeline:  "knowledge-hybrid",
		VectorDimension: 3,
	}); err == nil {
		t.Fatal("remote plaintext OpenSearch endpoint was accepted")
	}
	server := httptest.NewServer(http.HandlerFunc(func(
		http.ResponseWriter,
		*http.Request,
	) {
		t.Fatal("invalid replacement reached OpenSearch")
	}))
	defer server.Close()
	index := newOpenSearchKnowledgeTestIndex(t, server.URL, server.Client())
	err := index.ReplaceProject(context.Background(), HybridIndexReplacement{
		OrganizationID: 7,
		ProjectID:      11,
		Generation:     1,
		SourceDigest:   strings.Repeat("a", 64),
		Documents: []HybridIndexDocument{{
			OrganizationID: 7,
			ProjectID:      12,
			ChunkID:        "cross-project",
			Embedding:      []float32{0.1, 0.2, 0.3},
		}},
	})
	if err == nil {
		t.Fatal("cross-project OpenSearch replacement document was accepted")
	}
}

func newOpenSearchKnowledgeTestIndex(
	t *testing.T,
	endpoint string,
	client *http.Client,
) *OpenSearchKnowledgeIndex {
	t.Helper()
	index, err := NewOpenSearchKnowledgeIndex(OpenSearchKnowledgeIndexOptions{
		Endpoint:        endpoint,
		IndexPrefix:     "knowledge",
		SearchPipeline:  "knowledge-hybrid",
		VectorDimension: 3,
		HTTPClient:      client,
	})
	if err != nil {
		t.Fatalf("NewOpenSearchKnowledgeIndex(): %v", err)
	}
	return index
}

func openSearchTestMap(t *testing.T, value any, location string) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, want object", location, value)
	}
	return result
}
