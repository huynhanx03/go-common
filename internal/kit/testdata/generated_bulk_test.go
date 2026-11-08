package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	productresource "example.com/shop/internal/catalog/product"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	commonent "github.com/huynhanx03/go-common/pkg/database/ent"
	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/settings"
)

func TestGeneratedBulkHTTP(t *testing.T) {
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
	log, err := logger.NewLogger(logger.LoggerConfig{Mode: settings.EnvDev, Env: settings.EnvDev, Service: "bulk-integration"})
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
	if _, err := driver.DB().ExecContext(ctx, `ALTER TABLE products ADD CONSTRAINT products_created_by_bulk_check CHECK (created_by IS DISTINCT FROM 'reject-bulk')`); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.DB().ExecContext(ctx, `ALTER TABLE articles ADD CONSTRAINT articles_name_bulk_check CHECK (name <> 'reject-bulk')`); err != nil {
		t.Fatal(err)
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
	productresource.Register(router.Group("/configured/products"), productresource.Dependencies{
		Client: client, Config: productresource.ServiceConfig{MaxBulkSize: 1},
	})
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var content *strings.Reader
		if body != nil {
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			content = strings.NewReader(string(encoded))
		}
		if content == nil {
			content = strings.NewReader("")
		}
		response := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, content)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		router.ServeHTTP(response, req)
		return response
	}
	productPath := "/api/v1/catalog/products"
	articlePath := "/api/v1/catalog/articles"
	for name, response := range map[string]*httptest.ResponseRecorder{
		"empty CreateBulk": request(http.MethodPost, productPath+"/create-bulk", map[string]any{"items": []any{}}),
		"empty UpdateBulk": request(http.MethodPost, productPath+"/update-bulk", map[string]any{"items": []any{}}),
		"empty UpsertBulk": request(http.MethodPost, productPath+"/upsert-bulk", map[string]any{"items": []any{}}),
		"empty DeleteBulk": request(http.MethodPost, productPath+"/delete-bulk", map[string]any{"ids": []string{}}),
	} {
		if response.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s status = %d, want 422: %s", name, response.Code, response.Body)
		}
	}
	if response := request(http.MethodPost, "/configured/products/create-bulk", map[string]any{"items": []any{map[string]any{}, map[string]any{}}}); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("configured max-bulk-size status = %d, want 422: %s", response.Code, response.Body)
	}

	var actorCalls atomic.Int32
	commonent.SetActorResolver(func(context.Context) (string, bool) {
		if actorCalls.Add(1) == 2 {
			return "reject-bulk", true
		}
		return "allowed-bulk", true
	})
	beforeProducts, err := client.Product.Query().Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	failedCreate := request(http.MethodPost, productPath+"/create-bulk", map[string]any{"items": []any{map[string]any{}, map[string]any{}}})
	commonent.SetActorResolver(func(context.Context) (string, bool) { return "integration-user", true })
	if failedCreate.Code < 400 {
		t.Fatalf("constraint-breaking CreateBulk status = %d, body = %s", failedCreate.Code, failedCreate.Body)
	}
	afterFailedCreate, err := client.Product.Query().Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if afterFailedCreate != beforeProducts {
		t.Fatalf("failed CreateBulk left %d product rows, before=%d", afterFailedCreate, beforeProducts)
	}

	created := request(http.MethodPost, productPath+"/create-bulk", map[string]any{"items": []any{map[string]any{}, map[string]any{}}})
	if created.Code != http.StatusCreated {
		t.Fatalf("CreateBulk status = %d, body = %s", created.Code, created.Body)
	}
	productRows := bulkResponseRows(t, created)
	if len(productRows) != 2 || productRows[0]["id"] == productRows[1]["id"] {
		t.Fatalf("CreateBulk rows = %v; want two fresh IDs", productRows)
	}
	productIDs := []string{productRows[0]["id"].(string), productRows[1]["id"].(string)}
	orderedGet := request(http.MethodPost, productPath+"/get-bulk", map[string]any{"ids": []string{productIDs[1], productIDs[0]}})
	gotProducts := bulkResponseRows(t, orderedGet)
	if orderedGet.Code != http.StatusOK || len(gotProducts) != 2 || gotProducts[0]["id"] != productIDs[1] || gotProducts[1]["id"] != productIDs[0] {
		t.Fatalf("GetBulk did not preserve requested order: status=%d rows=%v body=%s", orderedGet.Code, gotProducts, orderedGet.Body)
	}
	for name, response := range map[string]*httptest.ResponseRecorder{
		"empty GetBulk":        request(http.MethodPost, productPath+"/get-bulk", map[string]any{"ids": []string{}}),
		"duplicate GetBulk":    request(http.MethodPost, productPath+"/get-bulk", map[string]any{"ids": []string{productIDs[0], productIDs[0]}}),
		"missing GetBulk":      request(http.MethodPost, productPath+"/get-bulk", map[string]any{"ids": []string{productIDs[0], uuid.NewString()}}),
		"duplicate DeleteBulk": request(http.MethodPost, productPath+"/delete-bulk", map[string]any{"ids": []string{productIDs[0], productIDs[0]}}),
		"duplicate UpsertBulk": request(http.MethodPost, productPath+"/upsert-bulk", map[string]any{"items": []map[string]string{{"id": productIDs[0]}, {"id": productIDs[0]}}}),
	} {
		want := http.StatusUnprocessableEntity
		if name == "missing GetBulk" {
			want = http.StatusNotFound
		}
		if response.Code != want {
			t.Errorf("%s status = %d, want %d: %s", name, response.Code, want, response.Body)
		}
	}
	tooMany := make([]any, 101)
	for index := range tooMany {
		tooMany[index] = map[string]any{}
	}
	if response := request(http.MethodPost, productPath+"/create-bulk", map[string]any{"items": tooMany}); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("over-limit CreateBulk status = %d, want 422: %s", response.Code, response.Body)
	}

	missingID := uuid.NewString()
	partialDelete := request(http.MethodPost, productPath+"/delete-bulk", map[string]any{"ids": []string{productIDs[0], missingID}})
	if partialDelete.Code != http.StatusNotFound {
		t.Fatalf("DeleteBulk with a missing target status = %d, body = %s", partialDelete.Code, partialDelete.Body)
	}
	if response := request(http.MethodGet, productPath+"/"+productIDs[0], nil); response.Code != http.StatusOK {
		t.Fatalf("failed DeleteBulk partially removed its first target: %d %s", response.Code, response.Body)
	}
	if response := request(http.MethodPost, productPath+"/delete-bulk", map[string]any{"ids": productIDs}); response.Code != http.StatusOK {
		t.Fatalf("DeleteBulk status = %d, body = %s", response.Code, response.Body)
	}
	if response := request(http.MethodGet, productPath+"/"+productIDs[1], nil); response.Code != http.StatusNotFound {
		t.Fatalf("Get after DeleteBulk status = %d, body = %s", response.Code, response.Body)
	}

	firstUpsert, secondUpsert, deletedID := uuid.NewString(), uuid.NewString(), ""
	for _, id := range []string{firstUpsert, secondUpsert} {
		if response := request(http.MethodPut, productPath+"/upsert", map[string]string{"id": id}); response.Code != http.StatusOK {
			t.Fatalf("Upsert setup status = %d, body = %s", response.Code, response.Body)
		}
	}
	upserted := request(http.MethodPost, productPath+"/upsert-bulk", map[string]any{"items": []map[string]string{{"id": secondUpsert}, {"id": firstUpsert}}})
	upsertRows := bulkResponseRows(t, upserted)
	if upserted.Code != http.StatusOK || len(upsertRows) != 2 || upsertRows[0]["id"] != secondUpsert || upsertRows[1]["id"] != firstUpsert {
		t.Fatalf("UpsertBulk order/status mismatch: status=%d rows=%v body=%s", upserted.Code, upsertRows, upserted.Body)
	}
	if response := request(http.MethodPost, productPath+"/delete-bulk", map[string]any{"ids": []string{secondUpsert}}); response.Code != http.StatusOK {
		t.Fatalf("soft-delete setup status = %d, body = %s", response.Code, response.Body)
	}
	deletedID = secondUpsert
	newID := uuid.NewString()
	failedUpsert := request(http.MethodPost, productPath+"/upsert-bulk", map[string]any{"items": []map[string]string{{"id": newID}, {"id": firstUpsert}, {"id": deletedID}}})
	if failedUpsert.Code != http.StatusNotFound {
		t.Fatalf("UpsertBulk with soft-deleted target status = %d, body = %s", failedUpsert.Code, failedUpsert.Body)
	}
	if response := request(http.MethodGet, productPath+"/"+newID, nil); response.Code != http.StatusNotFound {
		t.Fatalf("failed UpsertBulk left its earlier insert: status=%d body=%s", response.Code, response.Body)
	}
	if response := request(http.MethodGet, productPath+"/"+firstUpsert, nil); response.Code != http.StatusOK {
		t.Fatalf("failed mixed UpsertBulk changed its existing live row: status=%d body=%s", response.Code, response.Body)
	}

	articles := make([]string, 2)
	for index, name := range []string{"before-one", "before-two"} {
		response := request(http.MethodPost, articlePath, map[string]string{"name": name})
		if response.Code != http.StatusCreated {
			t.Fatalf("create article status = %d, body = %s", response.Code, response.Body)
		}
		articleData, ok := bulkResponseData(t, response).(map[string]any)
		if !ok || articleData["id"] == nil {
			t.Fatalf("create article data = %v", articleData)
		}
		articles[index] = articleData["id"].(string)
	}
	articleCount, err := client.Article.Query().Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	failedArticles := request(http.MethodPost, articlePath+"/create-bulk", map[string]any{"items": []map[string]string{{"name": "valid"}, {"name": "reject-bulk"}}})
	if failedArticles.Code < 400 {
		t.Fatalf("constraint-breaking article CreateBulk status = %d, body = %s", failedArticles.Code, failedArticles.Body)
	}
	afterFailedArticles, err := client.Article.Query().Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if afterFailedArticles != articleCount {
		t.Fatalf("failed article CreateBulk left %d rows, before=%d", afterFailedArticles, articleCount)
	}
	createdArticles := request(http.MethodPost, articlePath+"/create-bulk", map[string]any{"items": []map[string]string{{"name": "batch-first"}, {"name": "batch-second"}}})
	articleBulkRows := bulkResponseRows(t, createdArticles)
	if createdArticles.Code != http.StatusCreated || len(articleBulkRows) != 2 ||
		articleBulkRows[0]["name"] != "batch-first" || articleBulkRows[1]["name"] != "batch-second" ||
		articleBulkRows[0]["id"] == articleBulkRows[1]["id"] {
		t.Fatalf("CreateBulk did not preserve input order or generate fresh IDs: status=%d rows=%v body=%s", createdArticles.Code, articleBulkRows, createdArticles.Body)
	}
	failedUpdate := request(http.MethodPost, articlePath+"/update-bulk", map[string]any{"items": []map[string]any{
		{"id": articles[0], "changes": map[string]string{"name": "after-one"}},
		{"id": articles[1], "changes": map[string]string{"name": "reject-bulk"}},
	}})
	if failedUpdate.Code < 400 {
		t.Fatalf("constraint-breaking UpdateBulk status = %d, body = %s", failedUpdate.Code, failedUpdate.Body)
	}
	for index, want := range []string{"before-one", "before-two"} {
		response := request(http.MethodGet, articlePath+"/"+articles[index], nil)
		var envelope map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		data, ok := envelope["data"].(map[string]any)
		if response.Code != http.StatusOK || !ok || data["name"] != want || data["updated_by"] != nil {
			t.Fatalf("failed UpdateBulk changed article %s: status=%d data=%v body=%s", articles[index], response.Code, data, response.Body)
		}
	}
	updated := request(http.MethodPost, articlePath+"/update-bulk", map[string]any{"items": []map[string]any{
		{"id": articles[0], "changes": map[string]string{"name": "after-one"}},
		{"id": articles[1], "changes": map[string]string{"name": "after-two"}},
	}})
	updatedRows := bulkResponseRows(t, updated)
	if updated.Code != http.StatusOK || len(updatedRows) != 2 || updatedRows[0]["name"] != "after-one" || updatedRows[1]["name"] != "after-two" {
		t.Fatalf("successful UpdateBulk order/status mismatch: status=%d rows=%v body=%s", updated.Code, updatedRows, updated.Body)
	}
	if response := request(http.MethodPost, articlePath+"/update-bulk", map[string]any{"items": []map[string]any{
		{"id": articles[0], "changes": map[string]string{"name": "duplicate"}},
		{"id": articles[0], "changes": map[string]string{"name": "duplicate"}},
	}}); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate UpdateBulk status = %d, want 422: %s", response.Code, response.Body)
	}

	left, right := uuid.NewString(), uuid.NewString()
	for _, id := range []string{left, right} {
		if response := request(http.MethodPut, productPath+"/upsert", map[string]string{"id": id}); response.Code != http.StatusOK {
			t.Fatalf("competing delete setup status = %d, body = %s", response.Code, response.Body)
		}
	}
	var deletes sync.WaitGroup
	type deleteResult struct {
		bulk   bool
		status int
	}
	results := make(chan deleteResult, 2)
	deletes.Add(2)
	go func() {
		defer deletes.Done()
		results <- deleteResult{bulk: true, status: request(http.MethodPost, productPath+"/delete-bulk", map[string]any{"ids": []string{left, right}}).Code}
	}()
	go func() {
		defer deletes.Done()
		results <- deleteResult{status: request(http.MethodDelete, productPath+"/"+left, nil).Code}
	}()
	deletes.Wait()
	close(results)
	bulkStatus := 0
	for result := range results {
		if result.bulk {
			bulkStatus = result.status
		}
	}
	leftStatus := request(http.MethodGet, productPath+"/"+left, nil).Code
	rightStatus := request(http.MethodGet, productPath+"/"+right, nil).Code
	if bulkStatus == http.StatusOK && (leftStatus != http.StatusNotFound || rightStatus != http.StatusNotFound) {
		t.Fatalf("successful competing DeleteBulk was partial: statuses left=%d right=%d", leftStatus, rightStatus)
	}
	if bulkStatus != http.StatusOK && (leftStatus != http.StatusNotFound || rightStatus != http.StatusOK) {
		t.Fatalf("failed competing DeleteBulk committed partial state: statuses left=%d right=%d", leftStatus, rightStatus)
	}
}

func bulkResponseRows(t *testing.T, response *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	data := bulkResponseData(t, response)
	items, ok := data.([]any)
	if !ok {
		return nil
	}
	rows := make([]map[string]any, len(items))
	for index, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("response item %d has type %T", index, item)
		}
		rows[index] = row
	}
	return rows
}

func bulkResponseData(t *testing.T, response *httptest.ResponseRecorder) any {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v: %s", err, response.Body)
	}
	return envelope["data"]
}
