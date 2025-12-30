package elasticsearch

import (
	"fmt"
	"io"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	
	"github.com/huynhanx03/go-common/pkg/settings"
)

// Client wraps elasticsearch.Client
type Client struct {
	*elasticsearch.Client
}

// New creates a new Elasticsearch client
func New(cfg settings.Elasticsearch) (ElasticClient, error) {
	// Build config
	config := elasticsearch.Config{
		Addresses: cfg.Addresses,
		Username:  cfg.Username,
		Password:  cfg.Password,
	}

	// Create client
	client, err := elasticsearch.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCreateClientFailed, err)
	}

	// Ping to verify connection
	info, err := client.Info()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConnectFailed, err)
	}
	defer info.Body.Close()

	if info.IsError() {
		return nil, fmt.Errorf("%w: %s", ErrInfoRequestFailed, info.Status())
	}

	return &Client{Client: client}, nil
}

// Info wrapper
func (c *Client) Info(o ...func(*esapi.InfoRequest)) (*esapi.Response, error) {
	return c.Client.Info(o...)
}

// Index wrapper
func (c *Client) Index(index string, body io.Reader, o ...func(*esapi.IndexRequest)) (*esapi.Response, error) {
	return c.Client.Index(index, body, o...)
}

// Get wrapper
func (c *Client) Get(index string, id string, o ...func(*esapi.GetRequest)) (*esapi.Response, error) {
	return c.Client.Get(index, id, o...)
}

// Delete wrapper
func (c *Client) Delete(index string, id string, o ...func(*esapi.DeleteRequest)) (*esapi.Response, error) {
	return c.Client.Delete(index, id, o...)
}

// Search wrapper
func (c *Client) Search(o ...func(*esapi.SearchRequest)) (*esapi.Response, error) {
	return c.Client.Search(o...)
}

// Bulk wrapper
func (c *Client) Bulk(body io.Reader, o ...func(*esapi.BulkRequest)) (*esapi.Response, error) {
	return c.Client.Bulk(body, o...)
}
