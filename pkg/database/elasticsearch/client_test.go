package elasticsearch

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/huynhanx03/go-common/pkg/settings"
)

// Docker configuration
const (
	elasticsearchImage = "elastic/elasticsearch:8.18.8"
	elasticsearchPort  = "9200/tcp"
	startupTimeout     = 60 * time.Second
)

func TestClient_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	if !isDockerRunning(ctx) {
		t.Skip("Docker is not running, skipping integration test")
	}

	endpoint, terminate := setupElasticsearchContainer(ctx, t)
	defer terminate()

	cfg := settings.Elasticsearch{
		Addresses: []string{fmt.Sprintf("http://%s", endpoint)},
	}

	client, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	t.Run("Info", func(t *testing.T) {
		testInfo(t, client)
	})

	t.Run("Index", func(t *testing.T) {
		testIndex(t, client)
	})

	t.Run("Get", func(t *testing.T) {
		testGet(t, client)
	})

	t.Run("Delete", func(t *testing.T) {
		testDelete(t, client)
	})

	t.Run("Upsert", func(t *testing.T) {
		testUpsert(t, client)
	})
}

func setupElasticsearchContainer(ctx context.Context, t *testing.T) (string, func()) {
	req := testcontainers.ContainerRequest{
		Image: elasticsearchImage,
		Env: map[string]string{
			"discovery.type":         "single-node",
			"xpack.security.enabled": "false",
		},
		ExposedPorts: []string{elasticsearchPort},
		WaitingFor:   wait.ForHTTP("/_cluster/health").WithPort(elasticsearchPort).WithStartupTimeout(startupTimeout),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start elasticsearch container: %v", err)
	}

	endpoint, err := container.PortEndpoint(ctx, elasticsearchPort, "")
	if err != nil {
		container.Terminate(ctx)
		t.Fatalf("failed to get container endpoint: %v", err)
	}

	t.Logf("Elasticsearch running at %s", endpoint)

	terminate := func() {
		if err := container.Terminate(ctx); err != nil {
			t.Errorf("failed to terminate container: %v", err)
		}
	}

	return endpoint, terminate
}

func testInfo(t *testing.T, client ElasticClient) {
	info, err := client.Info()
	if err != nil {
		t.Errorf("Info failed: %v", err)
		return
	}
	defer info.Body.Close()
	if info.IsError() {
		t.Errorf("Info returned error status: %s", info.Status())
	}
}

func testIndex(t *testing.T, client ElasticClient) {
	indexName := "test-index"
	docID := "1"
	docBody := `{"title": "Test Document"}`

	res, err := client.Index(indexName, strings.NewReader(docBody), func(r *esapi.IndexRequest) {
		r.DocumentID = docID
		r.Refresh = "true"
	})
	if err != nil {
		t.Errorf("Index failed: %v", err)
		return
	}
	defer res.Body.Close()
	if res.IsError() {
		t.Errorf("Index returned error status: %s", res.Status())
	}
}

func testGet(t *testing.T, client ElasticClient) {
	indexName := "test-index"
	docID := "1"

	res, err := client.Get(indexName, docID)
	if err != nil {
		t.Errorf("Get failed: %v", err)
		return
	}
	defer res.Body.Close()
	if res.IsError() {
		t.Errorf("Get returned error status: %s", res.Status())
	}
}

func testDelete(t *testing.T, client ElasticClient) {
	indexName := "test-index"
	docID := "1"

	res, err := client.Delete(indexName, docID, func(r *esapi.DeleteRequest) {
		r.Refresh = "true"
	})
	if err != nil {
		t.Errorf("Delete failed: %v", err)
		return
	}
	defer res.Body.Close()
	if res.IsError() {
		t.Errorf("Delete returned error status: %s", res.Status())
	}
}

func testUpsert(t *testing.T, client ElasticClient) {
	indexName := "test-index-upsert"
	docID := "1"

	initialBody := `{"title": "Original Title"}`
	res, err := client.Index(indexName, strings.NewReader(initialBody), func(r *esapi.IndexRequest) {
		r.DocumentID = docID
		r.Refresh = "true"
	})
	if err != nil {
		t.Errorf("Initial Index failed: %v", err)
		return
	}
	res.Body.Close()

	updatedBody := `{"title": "Updated Title"}`
	res, err = client.Index(indexName, strings.NewReader(updatedBody), func(r *esapi.IndexRequest) {
		r.DocumentID = docID
		r.Refresh = "true"
	})
	if err != nil {
		t.Errorf("Upsert Index failed: %v", err)
		return
	}
	res.Body.Close()

	res, err = client.Get(indexName, docID)
	if err != nil {
		t.Errorf("Get after Upsert failed: %v", err)
		return
	}
	defer res.Body.Close()

	if res.IsError() {
		t.Errorf("Get after Upsert returned error status: %s", res.Status())
	}
}

func isDockerRunning(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "docker", "info")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}
