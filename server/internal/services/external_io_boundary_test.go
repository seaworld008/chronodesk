package services

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
)

type externalIOBoundaryRoundTripper struct {
	calls atomic.Int64
}

func (transport *externalIOBoundaryRoundTripper) RoundTrip(
	*http.Request,
) (*http.Response, error) {
	transport.calls.Add(1)
	return nil, errors.New("external transport must not be called")
}

func TestHTTPModelAndOpenSearchAdaptersRejectProjectTransaction(
	t *testing.T,
) {
	scope := models.ProjectScope{OrganizationID: 51, ProjectID: 61}
	baseContext := modelGatewayTestContext(t, scope)
	transport := &externalIOBoundaryRoundTripper{}
	client := &http.Client{Transport: transport}
	provider := newModelGatewayTestProvider(
		t,
		"https://model-gateway.example.test",
		client,
		modelGatewayNoopAuthorizer(),
		3,
		4096,
		time.Second,
	)
	index, err := NewOpenSearchKnowledgeIndex(
		OpenSearchKnowledgeIndexOptions{
			Endpoint:        "https://opensearch.example.test",
			IndexPrefix:     "knowledge",
			SearchPipeline:  "knowledge-hybrid",
			VectorDimension: 3,
			HTTPClient:      client,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	db := openTestDB(t)
	err = scopeddb.WithProjectScopeContextTransaction(
		baseContext,
		db,
		scope,
		func(scopedContext context.Context) error {
			if _, callErr := provider.Generate(
				scopedContext,
				validModelGatewayGenerateRequest(scope),
			); !errors.Is(
				callErr,
				ErrExternalIOInsideProjectTransaction,
			) {
				t.Fatalf("model Gateway transaction error = %v", callErr)
			}
			if callErr := index.EnsureSearchPipeline(
				scopedContext,
			); !errors.Is(
				callErr,
				ErrExternalIOInsideProjectTransaction,
			) {
				t.Fatalf("OpenSearch transaction error = %v", callErr)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("install project transaction: %v", err)
	}
	if calls := transport.calls.Load(); calls != 0 {
		t.Fatalf("rejected adapters made %d external calls", calls)
	}
}

func TestLocalAttachmentAdapterRejectsProjectTransactionWithoutFilesystemIO(
	t *testing.T,
) {
	root := t.TempDir()
	storage, err := NewLocalAttachmentStorage(root)
	if err != nil {
		t.Fatal(err)
	}
	scope := models.ProjectScope{OrganizationID: 71, ProjectID: 81}
	db := openTestDB(t)
	err = scopeddb.WithProjectScopeContextTransaction(
		context.Background(),
		db,
		scope,
		func(scopedContext context.Context) error {
			if _, putErr := storage.Put(
				scopedContext,
				"tickets/1/file.txt",
				bytes.NewBufferString("untrusted"),
				1024,
			); !errors.Is(
				putErr,
				ErrExternalIOInsideProjectTransaction,
			) {
				t.Fatalf("Put() transaction error = %v", putErr)
			}
			if _, openErr := storage.Open(
				scopedContext,
				"tickets/1/file.txt",
			); !errors.Is(
				openErr,
				ErrExternalIOInsideProjectTransaction,
			) {
				t.Fatalf("Open() transaction error = %v", openErr)
			}
			if deleteErr := storage.Delete(
				scopedContext,
				"tickets/1/file.txt",
			); !errors.Is(
				deleteErr,
				ErrExternalIOInsideProjectTransaction,
			) {
				t.Fatalf("Delete() transaction error = %v", deleteErr)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf(
			"rejected local attachment calls wrote %d filesystem entries",
			len(entries),
		)
	}
}
