package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"example.com/shop/internal/catalog/product"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	commonent "github.com/huynhanx03/go-common/pkg/database/ent"
	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/settings"
)

func TestGeneratedCRUDHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	dsn, err := url.Parse(os.Getenv("COMXKIT_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	password, _ := dsn.User.Password()
	port, err := strconv.Atoi(dsn.Port())
	if err != nil {
		t.Fatal(err)
	}
	driver, err := commonent.NewDriver(settings.Database{
		Driver: commonent.DriverPostgres, Host: dsn.Hostname(), Port: port,
		Username: dsn.User.Username(), Password: password, Database: strings.TrimPrefix(dsn.Path, "/"),
		MaxOpenConns: 5, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	log, err := logger.NewLogger(logger.LoggerConfig{Mode: settings.EnvDev, Env: settings.EnvDev, Service: "integration"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Sync() })
	dependencies, closeDependencies, err := newDomainDependencies(driver, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := closeDependencies(); err != nil {
			t.Error(err)
		}
	})
	client := dependencies.client
	if err := client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	canceledContext, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := product.NewService(product.NewRepository(client)).Get(canceledContext, uuid.New()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get with canceled context error = %v, want context.Canceled", err)
	}
	commonent.SetActorResolver(func(context.Context) (string, bool) { return "integration-user", true })
	t.Cleanup(func() { commonent.SetActorResolver(nil) })
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Next()
		for _, requestError := range c.Errors {
			t.Logf("request %s %s error: %+v", c.Request.Method, c.Request.URL.Path, requestError.Err)
		}
	})
	registerDomains(router.Group("/api/v1"), dependencies)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		router.ServeHTTP(response, req)
		return response
	}

	created := request(http.MethodPost, "/api/v1/catalog/products", "{\"created_by\":\"spoofed\",\"updated_by\":\"spoofed\"}")
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body)
	}
	var createdBody map[string]any
	if err := json.Unmarshal(created.Body.Bytes(), &createdBody); err != nil {
		t.Fatal(err)
	}
	data, ok := createdBody["data"].(map[string]any)
	if !ok || data["id"] == "" {
		t.Fatalf("create response has no ID: %s", created.Body)
	}
	id, ok := data["id"].(string)
	if !ok || id == "" {
		t.Fatalf("create response ID = %v", data["id"])
	}
	if data["created_by"] != "integration-user" {
		t.Fatalf("created_by = %v, want context actor", data["created_by"])
	}

	if response := request(http.MethodGet, "/api/v1/catalog/products/"+id, ""); response.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", response.Code, response.Body)
	}
	if response := request(http.MethodPost, "/api/v1/catalog/products/find", "{\"pagination\":{\"page\":1,\"page_size\":10}}"); response.Code != http.StatusOK {
		t.Fatalf("find status = %d, body = %s", response.Code, response.Body)
	}
	secondID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	upsertBody := "{\"id\":\"" + secondID.String() + "\",\"created_by\":\"spoofed\"}"
	upserted := request(http.MethodPut, "/api/v1/catalog/products/upsert", upsertBody)
	if upserted.Code != http.StatusOK {
		t.Fatalf("upsert status = %d, body = %s", upserted.Code, upserted.Body)
	}
	var upsertBodyResult map[string]any
	if err := json.Unmarshal(upserted.Body.Bytes(), &upsertBodyResult); err != nil {
		t.Fatal(err)
	}
	upsertData, ok := upsertBodyResult["data"].(map[string]any)
	if !ok || upsertData["id"] != secondID.String() || upsertData["created_by"] != "integration-user" {
		t.Fatalf("upsert data = %v", upsertBodyResult["data"])
	}
	if repeated := request(http.MethodPut, "/api/v1/catalog/products/upsert", upsertBody); repeated.Code != http.StatusOK {
		t.Fatalf("repeated upsert status = %d, body = %s", repeated.Code, repeated.Body)
	}
	concurrentID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	var upserts sync.WaitGroup
	statuses := make(chan int, 12)
	for range 12 {
		upserts.Add(1)
		go func() {
			defer upserts.Done()
			response := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/api/v1/catalog/products/upsert", strings.NewReader("{\"id\":\""+concurrentID.String()+"\"}"))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(response, req)
			statuses <- response.Code
		}()
	}
	upserts.Wait()
	close(statuses)
	for status := range statuses {
		if status != http.StatusOK {
			t.Fatalf("concurrent upsert status = %d, want 200", status)
		}
	}
	ordered := request(http.MethodPost, "/api/v1/catalog/products/find", "{\"pagination\":{\"page\":1,\"page_size\":10},\"sort\":[{\"key\":\"id\",\"order\":-1}]}")
	var orderedBody map[string]any
	if err := json.Unmarshal(ordered.Body.Bytes(), &orderedBody); err != nil {
		t.Fatal(err)
	}
	orderedRows, ok := orderedBody["data"].([]any)
	if ordered.Code != http.StatusOK || !ok || len(orderedRows) != 3 {
		t.Fatalf("ordered Find status=%d rows=%v body=%s", ordered.Code, orderedBody["data"], ordered.Body)
	}
	firstRow, _ := orderedRows[0].(map[string]any)
	if firstRow["id"] != concurrentID.String() {
		t.Fatalf("Find sort order starts with %v, want newest UUIDv7 %s", firstRow["id"], concurrentID)
	}
	filtered := request(http.MethodPost, "/api/v1/catalog/products/find", "{\"filters\":[{\"key\":\"id\",\"type\":\"eq\",\"value\":\""+secondID.String()+"\"}]}")
	var filteredBody map[string]any
	if err := json.Unmarshal(filtered.Body.Bytes(), &filteredBody); err != nil {
		t.Fatal(err)
	}
	filteredRows, ok := filteredBody["data"].([]any)
	if filtered.Code != http.StatusOK || !ok || len(filteredRows) != 1 {
		t.Fatalf("Find ID filter status=%d rows=%v body=%s", filtered.Code, filteredBody["data"], filtered.Body)
	}
	if response := request(http.MethodPost, "/api/v1/catalog/products/find", "{\"filters\":[{\"key\":\"unknown\",\"value\":\"x\"}]}"); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown filter status = %d, body = %s", response.Code, response.Body)
	}
	if response := request(http.MethodPost, "/api/v1/catalog/products/find", "{\"sort\":[{\"key\":\"unknown\",\"order\":1}]}"); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown sort status = %d, body = %s", response.Code, response.Body)
	}
	if response := request(http.MethodGet, "/api/v1/catalog/products/not-a-uuid", ""); response.Code != http.StatusBadRequest {
		t.Fatalf("malformed ID status = %d, body = %s", response.Code, response.Body)
	}
	if response := request(http.MethodGet, "/api/v1/catalog/products/00000000-0000-0000-0000-000000000000", ""); response.Code != http.StatusNotFound {
		t.Fatalf("missing ID status = %d, body = %s", response.Code, response.Body)
	}
	if response := request(http.MethodPost, "/api/v1/catalog/products/find", "{\"pagination\":{\"page\":1,\"page_size\":101}}"); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("page-size bound status = %d, body = %s", response.Code, response.Body)
	}
	if response := request(http.MethodPatch, "/api/v1/catalog/products/"+id, "{}"); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty update status = %d, body = %s", response.Code, response.Body)
	}
	if response := request(http.MethodDelete, "/api/v1/catalog/products/"+id, ""); response.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", response.Code, response.Body)
	}
	if response := request(http.MethodGet, "/api/v1/catalog/products/"+id, ""); response.Code != http.StatusNotFound {
		t.Fatalf("soft-deleted Get status = %d, body = %s", response.Code, response.Body)
	}
	if response := request(http.MethodDelete, "/api/v1/catalog/products/"+id, ""); response.Code != http.StatusNotFound {
		t.Fatalf("repeated delete status = %d, body = %s", response.Code, response.Body)
	}
	if response := request(http.MethodPut, "/api/v1/catalog/products/upsert", "{\"id\":\""+id+"\"}"); response.Code != http.StatusNotFound {
		t.Fatalf("upsert restored a soft-deleted row: status = %d, body = %s", response.Code, response.Body)
	}
	deletedFind := request(http.MethodPost, "/api/v1/catalog/products/find", "{\"filters\":[{\"key\":\"id\",\"type\":\"eq\",\"value\":\""+id+"\"}]}")
	var deletedFindBody map[string]any
	if err := json.Unmarshal(deletedFind.Body.Bytes(), &deletedFindBody); err != nil {
		t.Fatal(err)
	}
	deletedRows, ok := deletedFindBody["data"].([]any)
	if deletedFind.Code != http.StatusOK || !ok || len(deletedRows) != 0 {
		t.Fatalf("soft-deleted Find status=%d rows=%v body=%s", deletedFind.Code, deletedFindBody["data"], deletedFind.Body)
	}

	article := request(http.MethodPost, "/api/v1/catalog/articles", "{\"name\":\"before\",\"created_by\":\"spoofed\"}")
	if article.Code != http.StatusCreated {
		t.Fatalf("create typed article status = %d, body = %s", article.Code, article.Body)
	}
	var articleEnvelope map[string]any
	if err := json.Unmarshal(article.Body.Bytes(), &articleEnvelope); err != nil {
		t.Fatal(err)
	}
	articleData, ok := articleEnvelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("create typed article response data = %v", articleEnvelope["data"])
	}
	articleID, ok := articleData["id"].(string)
	if !ok || articleID == "" || articleData["name"] != "before" || articleData["created_by"] != "integration-user" {
		t.Fatalf("created article = %v", articleData)
	}
	persisted := request(http.MethodGet, "/api/v1/catalog/articles/"+articleID, "")
	if persisted.Code != http.StatusOK {
		t.Fatalf("get typed article before update status = %d, body = %s", persisted.Code, persisted.Body)
	}
	var persistedEnvelope map[string]any
	if err := json.Unmarshal(persisted.Body.Bytes(), &persistedEnvelope); err != nil {
		t.Fatal(err)
	}
	persistedData, ok := persistedEnvelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("get typed article response data = %v", persistedEnvelope["data"])
	}
	createdAt, createdBy := persistedData["created_at"], persistedData["created_by"]
	updated := request(http.MethodPatch, "/api/v1/catalog/articles/"+articleID, "{\"name\":\"after\"}")
	if updated.Code != http.StatusOK {
		t.Fatalf("update typed article status = %d, body = %s", updated.Code, updated.Body)
	}
	var updatedEnvelope map[string]any
	if err := json.Unmarshal(updated.Body.Bytes(), &updatedEnvelope); err != nil {
		t.Fatal(err)
	}
	updatedData, ok := updatedEnvelope["data"].(map[string]any)
	if !ok || updatedData["name"] != "after" || updatedData["created_at"] != createdAt ||
		updatedData["created_by"] != createdBy || updatedData["updated_by"] != "integration-user" {
		t.Fatalf("update changed fields outside the requested business field: before created_at=%v created_by=%v; after=%v", createdAt, createdBy, updatedEnvelope["data"])
	}
	if response := request(http.MethodPatch, "/api/v1/catalog/articles/"+articleID, "{}"); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty typed update status = %d, body = %s", response.Code, response.Body)
	}
	if response := request(http.MethodDelete, "/api/v1/catalog/articles/"+articleID, ""); response.Code != http.StatusOK {
		t.Fatalf("plain-resource delete with soft-delete enabled elsewhere status = %d, body = %s", response.Code, response.Body)
	}
}
