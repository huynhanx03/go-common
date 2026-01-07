package mongodb

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/dto"
	"github.com/huynhanx03/go-common/pkg/settings"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	mongoImage = "mongo:6"
	mongoPort  = "27017/tcp"
)

// TestModel mirrors the user's struct pattern
// Embeds *BaseModel
type TestModel struct {
	*BaseModel `bson:",inline"`
	Name       string `bson:"name"`
	Value      int    `bson:"value"`
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
	// T is TestModel (struct embedding *BaseModel)
	repo := NewBaseRepository[TestModel](col)

	t.Run("Create", func(t *testing.T) {
		testCreate(t, ctx, repo)
	})

	t.Run("Update", func(t *testing.T) {
		testUpdate(t, ctx, repo)
	})

	t.Run("Delete", func(t *testing.T) {
		testDelete(t, ctx, repo)
	})

	t.Run("Get", func(t *testing.T) {
		testGet(t, ctx, repo)
	})

	t.Run("Find", func(t *testing.T) {
		testFind(t, ctx, repo)
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
}

// T is TestModel. *T is *TestModel.
func testCreate(t *testing.T, ctx context.Context, repo *BaseRepository[TestModel]) {
	model := TestModel{
		BaseModel: NewBaseModel(), // Initialize embedded pointer
		Name:      "test-model",
		Value:     100,
	}
	// Create expects *T -> *TestModel.
	if err := repo.Create(ctx, &model); err != nil {
		t.Fatalf("Failed to create model: %v", err)
	}
	if model.GetID().IsZero() {
		t.Error("Model ID should be set (or pre-set) after create")
	}
}

func testGet(t *testing.T, ctx context.Context, repo *BaseRepository[TestModel]) {
	model := TestModel{BaseModel: NewBaseModel(), Name: "get-model", Value: 200}
	repo.Create(ctx, &model)

	fetched, err := repo.Get(ctx, model.GetID())
	if err != nil {
		t.Fatalf("Failed to get model: %v", err)
	}
	// fetched is *T -> *TestModel
	if fetched.Name != "get-model" {
		t.Errorf("Expected Name 'get-model', got '%s'", fetched.Name)
	}
}

func testUpdate(t *testing.T, ctx context.Context, repo *BaseRepository[TestModel]) {
	model := TestModel{BaseModel: NewBaseModel(), Name: "update-model", Value: 300}
	repo.Create(ctx, &model)

	model.Value = 400
	model.Name = "Updated Name"
	if err := repo.Update(ctx, &model); err != nil {
		t.Fatalf("Failed to update model: %v", err)
	}

	fetched, _ := repo.Get(ctx, model.GetID())
	if fetched.Value != 400 {
		t.Errorf("Expected Value 400, got %d", fetched.Value)
	}
	if fetched.Name != "Updated Name" {
		t.Errorf("Expected Name 'Updated Name', got '%s'", fetched.Name)
	}
}

func testDelete(t *testing.T, ctx context.Context, repo *BaseRepository[TestModel]) {
	model := TestModel{BaseModel: NewBaseModel(), Name: "delete-model", Value: 500}
	repo.Create(ctx, &model)

	if err := repo.Delete(ctx, model.GetID()); err != nil {
		t.Fatalf("Failed to delete model: %v", err)
	}

	exists, _ := repo.Exists(ctx, model.GetID())
	if exists {
		t.Error("Model should not exist after delete")
	}
}

func testBatchDelete(t *testing.T, ctx context.Context, repo *BaseRepository[TestModel]) {
	model1 := TestModel{BaseModel: NewBaseModel(), Name: "batch-delete-1", Value: 1}
	model2 := TestModel{BaseModel: NewBaseModel(), Name: "batch-delete-2", Value: 2}

	// BatchCreate expects []*T -> []*TestModel
	models := []*TestModel{&model1, &model2}
	if err := repo.BatchCreate(ctx, models); err != nil {
		t.Fatalf("Failed to batch create: %v", err)
	}

	// Test BatchDelete
	ids := []primitive.ObjectID{model1.GetID(), model2.GetID()}
	if err := repo.BatchDelete(ctx, ids); err != nil {
		t.Fatalf("Failed to batch delete: %v", err)
	}

	// Verify deletion
	exists1, _ := repo.Exists(ctx, model1.GetID())
	exists2, _ := repo.Exists(ctx, model2.GetID())
	if exists1 || exists2 {
		t.Error("Models should be deleted")
	}
}

func testFind(t *testing.T, ctx context.Context, repo *BaseRepository[TestModel]) {
	model1 := TestModel{BaseModel: NewBaseModel(), Name: "find-model-1", Value: 10}
	model2 := TestModel{BaseModel: NewBaseModel(), Name: "find-model-2", Value: 20}
	model3 := TestModel{BaseModel: NewBaseModel(), Name: "other-model", Value: 30}
	repo.Create(ctx, &model1)
	repo.Create(ctx, &model2)
	repo.Create(ctx, &model3)

	// Test Find with Filter
	opts := &dto.QueryOptions{
		Filters: []dto.SearchFilter{
			{Key: "name", Value: "find-model", Type: "search"},
		},
		Pagination: &dto.PaginationOptions{
			Page:     1,
			PageSize: 10,
		},
	}

	result, err := repo.Find(ctx, opts)
	if err != nil {
		t.Fatalf("Failed to find models: %v", err)
	}

	if result.Pagination.TotalItems != 2 {
		t.Errorf("Expected 2 items, got %d", result.Pagination.TotalItems)
	}
	if len(*result.Records) != 2 {
		t.Errorf("Expected 2 records, got %d", len(*result.Records))
	}
}

func testExists(t *testing.T, ctx context.Context, repo *BaseRepository[TestModel]) {
	model := TestModel{BaseModel: NewBaseModel(), Name: "exists-model", Value: 600}
	repo.Create(ctx, &model)

	exists, err := repo.Exists(ctx, model.GetID())
	if err != nil {
		t.Fatalf("Failed to check exists: %v", err)
	}
	if !exists {
		t.Error("Model should exist")
	}

	exists, err = repo.Exists(ctx, primitive.NewObjectID())
	if err != nil {
		t.Fatalf("Failed to check exists: %v", err)
	}
	if exists {
		t.Error("Random model should not exist")
	}
}

func testBatchCreate(t *testing.T, ctx context.Context, repo *BaseRepository[TestModel]) {
	model1 := TestModel{BaseModel: NewBaseModel(), Name: "batch-create-1", Value: 1000}
	model2 := TestModel{BaseModel: NewBaseModel(), Name: "batch-create-2", Value: 2000}

	models := []*TestModel{&model1, &model2}
	if err := repo.BatchCreate(ctx, models); err != nil {
		t.Fatalf("Failed to batch create: %v", err)
	}

	// Verify IDs set
	if model1.GetID().IsZero() || model2.GetID().IsZero() {
		t.Error("IDs should be set after batch create")
	}

	// Verify existence
	exists1, _ := repo.Exists(ctx, model1.GetID())
	exists2, _ := repo.Exists(ctx, model2.GetID())
	if !exists1 || !exists2 {
		t.Error("Batch created models should exist")
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
