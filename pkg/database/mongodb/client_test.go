package mongodb

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/huynhanx03/go-common/pkg/settings"
)

const (
	mongoImage = "mongo:6"
	mongoPort  = "27017/tcp"
)

type TestDocument struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"`
	Name      string             `bson:"name"`
	Value     int                `bson:"value"`
	CreatedAt time.Time          `bson:"created_at"`
	UpdatedAt time.Time          `bson:"updated_at"`
}

func (d *TestDocument) SetID(id primitive.ObjectID) {
	d.ID = id
}

func (d *TestDocument) UpdateTimestamp() {
	d.UpdatedAt = time.Now()
}

func (d *TestDocument) GetID() primitive.ObjectID {
	return d.ID
}

func TestClient_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	if !isDockerRunning(ctx) {
		t.Skip("Docker is not running, skipping integration test")
	}

	uri, terminate, err := setupMongoDBContainer(ctx)
	if err != nil {
		t.Fatalf("failed to setup mongodb container: %v", err)
	}
	defer terminate()

	cfg := &settings.MongoDB{
		Database:        "testdb",
		Timeout:         5,
		MaxPoolSize:     10,
		MinPoolSize:     1,
		MaxConnIdleTime: 60,
	}

	parsedURI, _ := url.Parse(uri)
	host := parsedURI.Hostname()
	portStr := parsedURI.Port()
	port, _ := strconv.Atoi(portStr)

	cfg.Host = host
	cfg.Port = port

	clientOpts := options.Client().ApplyURI(uri)
	mongoClient, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		t.Fatalf("Failed to connect to mongodb: %v", err)
	}

	if err := mongoClient.Ping(ctx, nil); err != nil {
		t.Fatalf("Failed to ping mongodb: %v", err)
	}
	t.Log("Successfully connected to MongoDB container")

	db := mongoClient.Database("testdb")
	col := db.Collection("test_collection")
	repo := NewBaseRepository[*TestDocument](col)

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

	t.Run("DeleteMany", func(t *testing.T) {
		testDeleteMany(t, ctx, repo)
	})
}

func testCreate(t *testing.T, ctx context.Context, repo *BaseRepository[*TestDocument]) {
	doc := &TestDocument{
		Name:      "test-doc",
		Value:     100,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := repo.Create(ctx, &doc); err != nil {
		t.Fatalf("Failed to create doc: %v", err)
	}
	if doc.ID.IsZero() {
		t.Error("Document ID should be set after create")
	}
}

func testGet(t *testing.T, ctx context.Context, repo *BaseRepository[*TestDocument]) {
	doc := &TestDocument{Name: "get-doc", Value: 200}
	repo.Create(ctx, &doc)

	fetched, err := repo.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("Failed to get doc: %v", err)
	}
	if (*fetched).Name != "get-doc" {
		t.Errorf("Expected Name 'get-doc', got '%s'", (*fetched).Name)
	}
}

func testUpdate(t *testing.T, ctx context.Context, repo *BaseRepository[*TestDocument]) {
	doc := &TestDocument{Name: "update-doc", Value: 300}
	repo.Create(ctx, &doc)

	doc.Value = 400
	if err := repo.Update(ctx, doc.ID, &doc); err != nil {
		t.Fatalf("Failed to update doc: %v", err)
	}

	fetched, _ := repo.Get(ctx, doc.ID)
	if (*fetched).Value != 400 {
		t.Errorf("Expected Value 400, got %d", (*fetched).Value)
	}
}

func testDelete(t *testing.T, ctx context.Context, repo *BaseRepository[*TestDocument]) {
	doc := &TestDocument{Name: "delete-doc", Value: 500}
	repo.Create(ctx, &doc)

	if err := repo.Delete(ctx, doc.ID); err != nil {
		t.Fatalf("Failed to delete doc: %v", err)
	}

	exists, _ := repo.Exists(ctx, doc.ID)
	if exists {
		t.Error("Document should not exist after delete")
	}
}

func testDeleteMany(t *testing.T, ctx context.Context, repo *BaseRepository[*TestDocument]) {
	doc1 := &TestDocument{Name: "batch-delete", Value: 1}
	doc2 := &TestDocument{Name: "batch-delete", Value: 2}
	repo.Create(ctx, &doc1)
	repo.Create(ctx, &doc2)

	count, err := repo.DeleteMany(ctx, bson.M{"name": "batch-delete"})
	if err != nil {
		t.Fatalf("Failed to delete many: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected 2 deleted, got %d", count)
	}
}

func setupMongoDBContainer(ctx context.Context) (string, func(), error) {
	req := testcontainers.ContainerRequest{
		Image:        mongoImage,
		ExposedPorts: []string{mongoPort},
		WaitingFor:   wait.ForLog("Waiting for connections").WithStartupTimeout(2 * time.Minute),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return "", nil, fmt.Errorf("failed to start container: %w", err)
	}

	endpoint, err := container.Endpoint(ctx, "")
	if err != nil {
		container.Terminate(ctx)
		return "", nil, fmt.Errorf("failed to get endpoint: %w", err)
	}

	uri := fmt.Sprintf("mongodb://%s", endpoint)

	terminate := func() {
		if err := container.Terminate(ctx); err != nil {
			fmt.Printf("failed to terminate container: %v\n", err)
		}
	}

	return uri, terminate, nil
}

func isDockerRunning(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "docker", "info")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}
