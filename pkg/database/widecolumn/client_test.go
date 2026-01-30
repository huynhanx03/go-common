package widecolumn

import (
	"context"
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/settings"

	"github.com/gocql/gocql"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	scyllaImage    = "scylladb/scylla:5.2"
	scyllaPort     = "9042/tcp"
	keyspace       = "test_keyspace"
	table          = "test_table"
	createKeyspace = "CREATE KEYSPACE IF NOT EXISTS test_keyspace WITH REPLICATION = {'class': 'SimpleStrategy', 'replication_factor': 1};"
	createTable    = "CREATE TABLE IF NOT EXISTS test_keyspace.test_table (id text PRIMARY KEY, name text, value int);"
)

// TestModel mirrors the user's struct pattern
type TestModel struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Value int    `json:"value"`
}

func (m TestModel) TableName() string {
	return fmt.Sprintf("%s.%s", keyspace, table)
}

func (m TestModel) ColumnNames() []string {
	return []string{"id", "name", "value"}
}

func (m TestModel) ColumnValues() []interface{} {
	return []interface{}{m.ID, m.Name, m.Value}
}

func TestClient_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	if !isDockerRunning(ctx) {
		t.Skip("Docker is not running, skipping integration test")
	}

	host, port, terminate, err := setupScyllaBox(ctx)
	if err != nil {
		t.Fatalf("failed to setup scylla container: %v", err)
	}
	defer terminate()

	cfg := &settings.WideColumn{
		Hosts:    []string{host},
		Port:     port,
		Keyspace: keyspace,
		Timeout:  10,
		Retries:  3,
	}

	// Connect to create keyspace and table
	cluster := gocql.NewCluster(host)
	cluster.Port = port
	cluster.Timeout = 10 * time.Second
	// cluster.Consistency = gocql.Quorum // Use One for single node test
	cluster.Consistency = gocql.One

	session, err := cluster.CreateSession()
	if err != nil {
		t.Fatalf("failed to connect to scylla for setup: %v", err)
	}
	defer session.Close()

	if err := session.Query(createKeyspace).Exec(); err != nil {
		t.Fatalf("failed to create keyspace: %v", err)
	}
	if err := session.Query(createTable).Exec(); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	// Now connect using our client
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	repo := NewBaseRepository[TestModel](client.GetSession(), TestModel{})

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
}

func testCreate(t *testing.T, ctx context.Context, repo *BaseRepository[TestModel]) {
	model := TestModel{
		ID:    "test-id-1",
		Name:  "test-name",
		Value: 100,
	}
	if err := repo.Create(ctx, &model); err != nil {
		t.Fatalf("Failed to create model: %v", err)
	}
}

func testGet(t *testing.T, ctx context.Context, repo *BaseRepository[TestModel]) {
	model := TestModel{
		ID:    "test-id-get",
		Name:  "get-name",
		Value: 200,
	}
	repo.Create(ctx, &model)

	fetched, err := repo.Get(ctx, model.ID)
	if err != nil {
		t.Fatalf("Failed to get model: %v", err)
	}
	if fetched.Name != "get-name" {
		t.Errorf("Expected Name 'get-name', got '%s'", fetched.Name)
	}
}

func testUpdate(t *testing.T, ctx context.Context, repo *BaseRepository[TestModel]) {
	model := TestModel{
		ID:    "test-id-update",
		Name:  "original-name",
		Value: 300,
	}
	repo.Create(ctx, &model)

	model.Name = "updated-name"
	if err := repo.Update(ctx, &model); err != nil {
		t.Fatalf("Failed to update model: %v", err)
	}

	fetched, _ := repo.Get(ctx, model.ID)
	if fetched.Name != "updated-name" {
		t.Errorf("Expected Name 'updated-name', got '%s'", fetched.Name)
	}
}

func testDelete(t *testing.T, ctx context.Context, repo *BaseRepository[TestModel]) {
	model := TestModel{
		ID:    "test-id-delete",
		Name:  "delete-me",
		Value: 400,
	}
	repo.Create(ctx, &model)

	if err := repo.Delete(ctx, model.ID); err != nil {
		t.Fatalf("Failed to delete model: %v", err)
	}

	exists, _ := repo.Exists(ctx, model.ID)
	if exists {
		t.Error("Model should not exist after delete")
	}
}

func testExists(t *testing.T, ctx context.Context, repo *BaseRepository[TestModel]) {
	model := TestModel{
		ID:    "test-id-exists",
		Name:  "exist-me",
		Value: 500,
	}
	repo.Create(ctx, &model)

	exists, err := repo.Exists(ctx, model.ID)
	if err != nil {
		t.Fatalf("failed to check exists: %v", err)
	}
	if !exists {
		t.Error("Model should exist")
	}
}

func setupScyllaBox(ctx context.Context) (string, int, func(), error) {
	req := testcontainers.ContainerRequest{
		Image:        scyllaImage,
		ExposedPorts: []string{scyllaPort},
		Cmd:          []string{"--smp", "1", "--memory", "750M", "--overprovisioned", "1", "--api-address", "0.0.0.0"},
		WaitingFor:   wait.ForLog("Scylla version").WithStartupTimeout(2 * time.Minute),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to start container: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		container.Terminate(ctx)
		return "", 0, nil, fmt.Errorf("failed to get host: %w", err)
	}

	mappedPort, err := container.MappedPort(ctx, scyllaPort)
	if err != nil {
		container.Terminate(ctx)
		return "", 0, nil, fmt.Errorf("failed to get mapped port: %w", err)
	}

	terminate := func() {
		if err := container.Terminate(ctx); err != nil {
			fmt.Printf("failed to terminate container: %v\n", err)
		}
	}

	return host, mappedPort.Int(), terminate, nil
}

func isDockerRunning(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "docker", "info")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}
