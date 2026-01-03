package elasticsearch

import (
	"io"
	"net/http"

	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/huynhanx03/go-common/pkg/database"
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

// Model interface that all models must implement
type Model interface {
	GetID() string
	SetID(id string)
}

// Repository aliases the common interface
type Repository[T Model] database.Repository[T, string]
