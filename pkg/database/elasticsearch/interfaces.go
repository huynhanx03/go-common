package elasticsearch

import (
	"context"
	"io"
	"net/http"

	"github.com/elastic/go-elasticsearch/v8/esapi"
)

// ElasticClient defines the contract for Elasticsearch client operations
type ElasticClient interface {
	Info(o ...func(*esapi.InfoRequest)) (*esapi.Response, error)

	Index(index string, body io.Reader, o ...func(*esapi.IndexRequest)) (*esapi.Response, error)
	Get(index string, id string, o ...func(*esapi.GetRequest)) (*esapi.Response, error)
	Delete(index string, id string, o ...func(*esapi.DeleteRequest)) (*esapi.Response, error)

	Search(o ...func(*esapi.SearchRequest)) (*esapi.Response, error)
	Bulk(body io.Reader, o ...func(*esapi.BulkRequest)) (*esapi.Response, error)

	// Perform is required for esapi.Transport interface
	Perform(*http.Request) (*http.Response, error)
}

// Document interface that all models must implement
type Document interface {
	GetID() string
	SetID(id string)
}

// Repository defines the common interface for all repositories
type Repository[T Document] interface {
	Get(ctx context.Context, docID string) (*T, error)
	Index(ctx context.Context, doc *T) error
	Delete(ctx context.Context, docID string) error
	Search(ctx context.Context, query io.Reader) ([]T, error)
	BatchIndex(ctx context.Context, docs []*T) error
	BatchDelete(ctx context.Context, docIDs []string) error
}
