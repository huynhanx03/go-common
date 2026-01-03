package elasticsearch

import (
	"context"
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/dto"
	"github.com/huynhanx03/go-common/pkg/settings"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Docker configuration
const (
	elasticsearchImage = "elastic/elasticsearch:8.18.8"
	elasticsearchPort  = "9200/tcp"
	startupTimeout     = 60 * time.Second
)

// TestDocument implements Document interface
type TestDocument struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Value     int       `json:"value"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// GetID implements Document interface
func (d *TestDocument) GetID() string {
	return d.ID
}

// SetID implements Document interface
func (d *TestDocument) SetID(id string) {
	d.ID = id
}

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

	// T is *TestDocument because Repository[T] requires T to implement Document interface
	// *TestDocument implements Document.
	repo := NewBaseRepository[*TestDocument](client, "test-index")

	t.Run("Create", func(t *testing.T) {
		testCreate(t, ctx, repo)
	})

	t.Run("Get", func(t *testing.T) {
		testGet(t, ctx, repo)
	})

	t.Run("Update", func(t *testing.T) {
		testUpdate(t, ctx, repo)
	})

	t.Run("Delete", func(t *testing.T) {
		testDelete(t, ctx, repo)
	})

	t.Run("Exists", func(t *testing.T) {
		testExists(t, ctx, repo)
	})

	t.Run("BatchCreate", func(t *testing.T) {
		testBatchCreate(t, ctx, repo)
	})

	t.Run("BatchDelete", func(t *testing.T) {
		testBatchDelete(t, ctx, repo)
	})

	t.Run("Find", func(t *testing.T) {
		testFind(t, ctx, repo)
	})
}

// Ensure T is *TestDocument
func testCreate(t *testing.T, ctx context.Context, repo *BaseRepository[*TestDocument]) {
	doc := &TestDocument{
		ID:        "1",
		Title:     "create-doc",
		Value:     100,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	// repo.Create takes *T. T is *TestDocument. So we pass **TestDocument.
	if err := repo.Create(ctx, &doc); err != nil {
		t.Fatalf("Failed to create doc: %v", err)
	}
}

func testGet(t *testing.T, ctx context.Context, repo *BaseRepository[*TestDocument]) {
	doc := &TestDocument{ID: "2", Title: "get-doc", Value: 200, CreatedAt: time.Now()}
	repo.Create(ctx, &doc)

	fetched, err := repo.Get(ctx, "2")
	if err != nil {
		t.Fatalf("Failed to get doc: %v", err)
	}
	// fetched is *T -> **TestDocument
	if (*fetched).Title != "get-doc" {
		t.Errorf("Expected Title 'get-doc', got '%s'", (*fetched).Title)
	}
}

func testUpdate(t *testing.T, ctx context.Context, repo *BaseRepository[*TestDocument]) {
	doc := &TestDocument{ID: "3", Title: "update-doc", Value: 300, CreatedAt: time.Now()}
	repo.Create(ctx, &doc)

	doc.Value = 400
	doc.Title = "updated-title"
	if err := repo.Update(ctx, &doc); err != nil {
		t.Fatalf("Failed to update doc: %v", err)
	}

	fetched, _ := repo.Get(ctx, "3")
	if (*fetched).Value != 400 {
		t.Errorf("Expected Value 400, got %d", (*fetched).Value)
	}
}

func testDelete(t *testing.T, ctx context.Context, repo *BaseRepository[*TestDocument]) {
	doc := &TestDocument{ID: "4", Title: "delete-doc", Value: 400, CreatedAt: time.Now()}
	repo.Create(ctx, &doc)

	if err := repo.Delete(ctx, "4"); err != nil {
		t.Fatalf("Failed to delete doc: %v", err)
	}

	exists, _ := repo.Exists(ctx, "4")
	if exists {
		t.Error("Document should not exist after delete")
	}
}

func testExists(t *testing.T, ctx context.Context, repo *BaseRepository[*TestDocument]) {
	doc := &TestDocument{ID: "5", Title: "exists-doc", Value: 500, CreatedAt: time.Now()}
	repo.Create(ctx, &doc)

	exists, err := repo.Exists(ctx, "5")
	if err != nil || !exists {
		t.Errorf("Document should exist, err: %v", err)
	}

	exists, _ = repo.Exists(ctx, "non-existent")
	if exists {
		t.Error("Non-existent document should not exist")
	}
}

func testBatchCreate(t *testing.T, ctx context.Context, repo *BaseRepository[*TestDocument]) {
	doc1 := &TestDocument{ID: "6", Title: "batch-1", Value: 600, CreatedAt: time.Now()}
	doc2 := &TestDocument{ID: "7", Title: "batch-2", Value: 700, CreatedAt: time.Now()}

	// repo.BatchCreate takes []*T. T=*TestDocument. So []*(*TestDocument).
	docs := []*(*TestDocument){&doc1, &doc2}

	if err := repo.BatchCreate(ctx, docs); err != nil {
		t.Fatalf("Failed to batch create: %v", err)
	}

	exists1, _ := repo.Exists(ctx, "6")
	exists2, _ := repo.Exists(ctx, "7")
	if !exists1 || !exists2 {
		t.Error("Batch created docs should exist")
	}
}

func testBatchDelete(t *testing.T, ctx context.Context, repo *BaseRepository[*TestDocument]) {
	doc1 := &TestDocument{ID: "8", Title: "batch-del-1", Value: 800, CreatedAt: time.Now()}
	doc2 := &TestDocument{ID: "9", Title: "batch-del-2", Value: 900, CreatedAt: time.Now()}
	repo.Create(ctx, &doc1)
	repo.Create(ctx, &doc2)

	if err := repo.BatchDelete(ctx, []string{"8", "9"}); err != nil {
		t.Fatalf("Failed to batch delete: %v", err)
	}

	exists1, _ := repo.Exists(ctx, "8")
	exists2, _ := repo.Exists(ctx, "9")
	if exists1 || exists2 {
		t.Error("Batch deleted docs should not exist")
	}
}

func testFind(t *testing.T, ctx context.Context, repo *BaseRepository[*TestDocument]) {
	// Give ES some time to index everything properly explicitly
	time.Sleep(1 * time.Second)

	doc1 := &TestDocument{ID: "10", Title: "find-me-1", Value: 1000, CreatedAt: time.Now()}
	doc2 := &TestDocument{ID: "11", Title: "find-me-2", Value: 1100, CreatedAt: time.Now()}
	doc3 := &TestDocument{ID: "12", Title: "other-one", Value: 1200, CreatedAt: time.Now()}

	docs := []*(*TestDocument){&doc1, &doc2, &doc3}
	repo.BatchCreate(ctx, docs)
	time.Sleep(1 * time.Second) // Ensure index refresh

	opts := &dto.QueryOptions{
		Filters: []dto.SearchFilter{
			{Key: "title", Value: "find-me", Type: "match"},
		},
		Pagination: &dto.PaginationOptions{
			Page:     1,
			PageSize: 10,
		},
	}

	res, err := repo.Find(ctx, opts)
	if err != nil {
		t.Fatalf("Failed to find: %v", err)
	}

	if res.Pagination.TotalItems != 2 {
		t.Errorf("Expected 2 items, got %d", res.Pagination.TotalItems)
	}
	// Records is *[]T. T is *TestDocument. So *[]*TestDocument.
	records := *res.Records
	if len(records) != 2 {
		t.Errorf("Expected 2 records, got %d", len(records))
	}
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

func isDockerRunning(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "docker", "info")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}
