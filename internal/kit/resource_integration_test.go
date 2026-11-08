//go:build integration

package kit

import (
	"context"
	"encoding/json"
	"go/format"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/mod/module"
	modulezip "golang.org/x/mod/zip"

	"github.com/huynhanx03/go-common/pkg/testutil/containers"
)

func TestGeneratedLocalLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	workspace, err := os.MkdirTemp(filepath.Dir(moduleRoot(t)), ".comxkit-lifecycle-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("retained generated project for diagnosis: %s", workspace)
			return
		}
		_ = os.RemoveAll(workspace)
	})
	project := filepath.Join(workspace, "shop")
	projectName := "comxkit-lifecycle-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	commonVersion := "v0.0.0-" + time.Now().UTC().Format("20060102150405") + "-abcdefabcdef"
	generator := newGenerator(defaultRegistry())
	plan, err := generator.Init(ctx, InitInput{
		Directory: project, Module: "example.com/shop", HTTP: stackGin, ORM: stackEnt,
		Database: stackPostgres, CommonVersion: commonVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generator.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	workFile := newIntegrationGoWork(t, ctx, workspace, project)
	ports := availablePorts(t, 3)
	writeLocalEnvironment(t, project, ports)
	proxyPath := writeLocalModuleProxy(t, project, commonVersion)
	composeEnvironment := map[string]string{
		"COMPOSE_PROJECT_NAME": projectName,
		"GOPROXY":              "file:///src/.test-goproxy,https://proxy.golang.org,direct",
		"GONOSUMDB":            "github.com/huynhanx03/go-common",
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cleanupCancel()
		if output, err := runProjectCommand(cleanupCtx, project, workFile, composeEnvironment, "docker", "compose", "--profile", "tools", "down", "--volumes", "--remove-orphans"); err != nil {
			t.Errorf("remove test Compose resources: %v\n%s", err, output)
		}
	})

	hostProxy := "file://" + filepath.ToSlash(proxyPath) + ",https://proxy.golang.org,direct"
	setupEnvironment := map[string]string{
		"GOWORK":    "off",
		"GOPROXY":   hostProxy,
		"GONOSUMDB": "github.com/huynhanx03/go-common",
	}
	if output, err := runProjectCommand(ctx, project, workFile, setupEnvironment, "make", "setup"); err != nil {
		t.Fatalf("initial setup: %v\n%s", err, output)
	}
	envPath := filepath.Join(project, ".env")
	envData, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	envData = append(envData, "\nLOCAL_SETUP_SENTINEL=preserved\n"...)
	if err := os.WriteFile(envPath, envData, 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := runProjectCommand(ctx, project, workFile, setupEnvironment, "make", "setup"); err != nil {
		t.Fatalf("repeat setup: %v\n%s", err, output)
	}
	preservedEnv, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(preservedEnv) != string(envData) {
		t.Fatal("setup replaced the existing .env file")
	}
	if output, err := runProjectCommand(ctx, project, workFile, composeEnvironment, "docker", "compose", "config", "--quiet"); err != nil {
		t.Fatalf("validate Compose config: %v\n%s", err, output)
	}
	if output, err := runProjectCommand(ctx, project, workFile, composeEnvironment, "make", "run-local"); err != nil {
		t.Fatalf("start pristine project: %v\n%s", err, output)
	}
	apiPort := ports[0]
	waitForHTTP(t, ctx, "http://127.0.0.1:"+strconv.Itoa(apiPort)+"/live")
	waitForHTTP(t, ctx, "http://127.0.0.1:"+strconv.Itoa(apiPort)+"/ready")
	if output, err := runProjectCommand(ctx, project, workFile, composeEnvironment, "make", "stop"); err != nil {
		t.Fatalf("stop pristine project: %v\n%s", err, output)
	}
	assertComposeVolumeExists(t, ctx, project, workFile, composeEnvironment, projectName+"_postgres_data")
	marker := filepath.Join(workspace, "migration-name-injection")
	unsafeName := "bad;touch " + marker
	if output, err := runProjectCommand(ctx, project, workFile, composeEnvironment, "make", "migrate-diff", "name="+unsafeName); err == nil {
		t.Fatalf("migrate-diff accepted an unsafe name:\n%s", output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("unsafe migration name created a file: stat error = %v", err)
	}

	for _, add := range []func() (Plan, error){
		func() (Plan, error) { return generator.Domain(ctx, project, DomainInput{Name: "catalog"}) },
		func() (Plan, error) {
			return generator.Resource(ctx, project, ResourceInput{Name: "article", Domain: "catalog"})
		},
	} {
		plan, err := add()
		if err != nil {
			t.Fatalf("add resource: %v", err)
		}
		if _, err := generator.Apply(ctx, plan); err != nil {
			t.Fatalf("apply resource plan: %v", err)
		}
	}
	customizeGeneratedArticle(t, project)
	if output, err := runProjectCommand(ctx, project, workFile, nil, "make", "generate"); err != nil {
		t.Fatalf("generate Ent: %v\n%s", err, output)
	}
	if output, err := runProjectCommand(ctx, project, workFile, composeEnvironment, "make", "migrate-diff", "name=init"); err != nil {
		t.Fatalf("create explicit migration: %v\n%s", err, output)
	}
	migrationFiles, err := filepath.Glob(filepath.Join(project, "migrations", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if len(migrationFiles) != 1 {
		t.Fatalf("migration files = %v, want one reviewed SQL migration", migrationFiles)
	}
	if _, err := os.Stat(filepath.Join(project, "migrations", "atlas.sum")); err != nil {
		t.Fatalf("migration checksum file: %v", err)
	}
	if output, err := runProjectCommand(ctx, project, workFile, composeEnvironment, "make", "migrate"); err != nil {
		t.Fatalf("apply checked-in migration: %v\n%s", err, output)
	}
	if output, err := runProjectCommand(ctx, project, workFile, composeEnvironment, "make", "run-local"); err != nil {
		t.Fatalf("start migrated project: %v\n%s", err, output)
	}
	baseURL := "http://127.0.0.1:" + strconv.Itoa(apiPort)
	waitForHTTP(t, ctx, baseURL+"/ready")
	createdID := createGeneratedArticle(t, ctx, baseURL)
	if output, err := runProjectCommand(ctx, project, workFile, composeEnvironment, "make", "stop"); err != nil {
		t.Fatalf("stop migrated project: %v\n%s", err, output)
	}
	if output, err := runProjectCommand(ctx, project, workFile, composeEnvironment, "make", "run-local"); err != nil {
		t.Fatalf("restart migrated project: %v\n%s", err, output)
	}
	waitForHTTP(t, ctx, baseURL+"/ready")
	getGeneratedArticle(t, ctx, baseURL, createdID)

	originalSum, err := os.ReadFile(filepath.Join(project, "migrations", "atlas.sum"))
	if err != nil {
		t.Fatal(err)
	}
	corruptedSum := append(append([]byte(nil), originalSum...), []byte("corrupt\n")...)
	if err := os.WriteFile(filepath.Join(project, "migrations", "atlas.sum"), corruptedSum, 0o644); err != nil {
		t.Fatal(err)
	}
	output, err := runProjectCommand(ctx, project, workFile, composeEnvironment, "make", "migrate")
	if err == nil {
		t.Fatalf("apply accepted a corrupted migration checksum:\n%s", output)
	}
	actualSum, readErr := os.ReadFile(filepath.Join(project, "migrations", "atlas.sum"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(actualSum) != string(corruptedSum) {
		t.Fatal("failed migration apply rewrote the corrupted checksum")
	}
	if err := os.WriteFile(filepath.Join(project, "migrations", "atlas.sum"), originalSum, 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := runProjectCommand(ctx, project, workFile, composeEnvironment, "make", "migrate"); err != nil {
		t.Fatalf("repeat migration apply: %v\n%s", err, output)
	}
}

func writeLocalModuleProxy(t *testing.T, project, version string) string {
	t.Helper()
	moduleRoot := moduleRoot(t)
	modulePath := "github.com/huynhanx03/go-common"
	proxyDirectory := filepath.Join(project, ".test-goproxy", filepath.FromSlash(modulePath), "@v")
	if err := os.MkdirAll(proxyDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	moduleFile, err := os.Open(filepath.Join(moduleRoot, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	moduleData, err := io.ReadAll(moduleFile)
	closeErr := moduleFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if err := os.WriteFile(filepath.Join(proxyDirectory, version+".mod"), moduleData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proxyDirectory, "list"), []byte(version+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := json.Marshal(struct {
		Version string    `json:"Version"`
		Time    time.Time `json:"Time"`
	}{Version: version, Time: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proxyDirectory, version+".info"), info, 0o644); err != nil {
		t.Fatal(err)
	}
	zipFile, err := os.Create(filepath.Join(proxyDirectory, version+".zip"))
	if err != nil {
		t.Fatal(err)
	}
	zipErr := modulezip.CreateFromDir(zipFile, module.Version{Path: modulePath, Version: version}, moduleRoot)
	closeErr = zipFile.Close()
	if zipErr != nil {
		t.Fatal(zipErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	return filepath.Join(project, ".test-goproxy")
}

func writeLocalEnvironment(t *testing.T, project string, ports []int) {
	t.Helper()
	if len(ports) != 3 {
		t.Fatalf("port count = %d, want 3", len(ports))
	}
	path := filepath.Join(project, ".env")
	if err := os.WriteFile(path, []byte(strings.Join([]string{
		"APP_ENV=dev",
		"HTTP_PORT=" + strconv.Itoa(ports[0]),
		"POSTGRES_PORT=" + strconv.Itoa(ports[1]),
		"POSTGRES_DB=go_common",
		"POSTGRES_USER=postgres",
		"POSTGRES_PASSWORD=postgres",
		"POSTGRES_TOOLS_PORT=" + strconv.Itoa(ports[2]),
		"POSTGRES_TOOLS_DB=atlas_dev",
		"POSTGRES_TOOLS_USER=atlas",
		"POSTGRES_TOOLS_PASSWORD=atlas",
		"",
	}, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
}

func availablePorts(t *testing.T, count int) []int {
	t.Helper()
	listeners := make([]net.Listener, count)
	ports := make([]int, count)
	for index := range listeners {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			for _, opened := range listeners {
				if opened != nil {
					_ = opened.Close()
				}
			}
			t.Fatal(err)
		}
		listeners[index] = listener
		ports[index] = listener.Addr().(*net.TCPAddr).Port
	}
	for _, listener := range listeners {
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return ports
}

func runProjectCommand(ctx context.Context, project, workFile string, environment map[string]string, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = project
	command.Env = replaceEnvironment(os.Environ(), "GOWORK", workFile)
	for key, value := range environment {
		command.Env = replaceEnvironment(command.Env, key, value)
	}
	return command.CombinedOutput()
}

func waitForHTTP(t *testing.T, ctx context.Context, address string) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err == nil {
			response, requestErr := client.Do(request)
			if requestErr == nil {
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK {
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for %s: %v", address, ctx.Err())
		case <-ticker.C:
		}
	}
}

func createGeneratedArticle(t *testing.T, ctx context.Context, baseURL string) string {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/v1/catalog/articles", strings.NewReader(`{"name":"persistent"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var envelope struct {
		Data struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if response.StatusCode != http.StatusCreated || envelope.Data.ID == "" || envelope.Data.Name != "persistent" {
		t.Fatalf("create article status=%d response=%+v", response.StatusCode, envelope)
	}
	return envelope.Data.ID
}

func getGeneratedArticle(t *testing.T, ctx context.Context, baseURL, id string) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/catalog/articles/"+url.PathEscape(id), nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var envelope struct {
		Data struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if response.StatusCode != http.StatusOK || envelope.Data.ID != id || envelope.Data.Name != "persistent" {
		t.Fatalf("get article status=%d response=%+v", response.StatusCode, envelope)
	}
}

func assertComposeVolumeExists(t *testing.T, ctx context.Context, project, workFile string, environment map[string]string, volume string) {
	t.Helper()
	output, err := runProjectCommand(ctx, project, workFile, environment, "docker", "volume", "inspect", volume)
	if err != nil {
		t.Fatalf("stopped Compose stack did not preserve volume %s: %v\n%s", volume, err, output)
	}
}

func TestGeneratedCRUD(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	workspace := newIntegrationWorkspace(t, ctx)
	project := filepath.Join(workspace, "shop")
	generator := newGenerator(defaultRegistry())
	plan, err := generator.Init(ctx, InitInput{
		Directory: project, Module: "example.com/shop", HTTP: stackGin, ORM: stackEnt,
		Database: stackPostgres, CommonVersion: "v1.2.3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generator.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	for _, add := range []func() (Plan, error){
		func() (Plan, error) { return generator.Domain(ctx, project, DomainInput{Name: "catalog"}) },
		func() (Plan, error) {
			operations := append(defaultRegistry().defaultOperations(), operationUpsert)
			return generator.Resource(ctx, project, ResourceInput{
				Name: "product", Domain: "catalog", Operations: operations,
				UpsertOn: upsertTargetID, SoftDelete: true, Modifier: true,
			})
		},
		func() (Plan, error) {
			return generator.Resource(ctx, project, ResourceInput{Name: "article", Domain: "catalog", Modifier: true})
		},
	} {
		plan, err := add()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := generator.Apply(ctx, plan); err != nil {
			t.Fatal(err)
		}
	}
	customizeGeneratedArticle(t, project)
	customizeGeneratedCatalogDrain(t, project)
	workFile := newIntegrationGoWork(t, ctx, workspace, project)
	if output, err := runGeneratedGo(ctx, project, workFile, "generate", "./ent"); err != nil {
		t.Fatalf("generate Ent: %v\n%s", err, output)
	}
	fixture, err := os.ReadFile(filepath.Join("testdata", "generated_crud_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "internal", "app", "generated_crud_test.go"), fixture, 0o644); err != nil {
		t.Fatal(err)
	}
	lifecycleFixture, err := os.ReadFile(filepath.Join("testdata", "generated_app_lifecycle_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "internal", "app", "generated_app_lifecycle_test.go"), lifecycleFixture, 0o644); err != nil {
		t.Fatal(err)
	}
	drainSupport, err := os.ReadFile(filepath.Join("testdata", "catalog_drain_support.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "internal", "catalog", "drain_support.go"), drainSupport, 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := runGeneratedGo(ctx, project, workFile, "test", "./internal/app", "-run", "^$", "-count=1"); err != nil {
		t.Fatalf("compile generated HTTP CRUD harness: %v\n%s", err, output)
	}
	dsn := containers.StartPostgres(t)
	command := exec.CommandContext(ctx, "go", "test", "./internal/app", "-run", "^TestGenerated(CRUDHTTP|AppShutdownDrainsRequests)$", "-count=1")
	command.Dir = project
	environment := replaceEnvironment(os.Environ(), "GOWORK", workFile)
	command.Env = replaceEnvironment(append(environment, "COMXKIT_TEST_DSN="+dsn), "GOWORK", workFile)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated HTTP CRUD behavior failed: %v\n%s", err, output)
	}
}

func customizeGeneratedCatalogDrain(t *testing.T, project string) {
	t.Helper()
	path := filepath.Join(project, "internal", "catalog", "module.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	customized := replaceGeneratedSource(t, string(source),
		"func (module *Module) Register(*gin.RouterGroup) {}",
		"func (module *Module) Register(group *gin.RouterGroup) { module.registerDrain(group) }")
	formatted, err := format.Source([]byte(customized))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, formatted, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedBulkCRUD(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	workspace := newIntegrationWorkspace(t, ctx)
	project := filepath.Join(workspace, "shop")
	generator := newGenerator(defaultRegistry())
	plan, err := generator.Init(ctx, InitInput{
		Directory: project, Module: "example.com/shop", HTTP: stackGin, ORM: stackEnt,
		Database: stackPostgres, CommonVersion: "v1.2.3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generator.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"catalog"} {
		plan, err := generator.Domain(ctx, project, DomainInput{Name: domain})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := generator.Apply(ctx, plan); err != nil {
			t.Fatal(err)
		}
	}
	productOperations := append(append(defaultRegistry().defaultOperations(), operationUpsert),
		operationGetBulk, operationCreateBulk, operationUpdateBulk, operationUpsertBulk, operationDeleteBulk)
	for _, resource := range []ResourceInput{
		{Name: "product", Domain: "catalog", Operations: productOperations, UpsertOn: upsertTargetID, SoftDelete: true, Modifier: true},
		{Name: "article", Domain: "catalog", Operations: []string{
			operationGet, operationCreate, operationUpdate, operationCreateBulk, operationUpdateBulk,
		}, Modifier: true},
	} {
		plan, err := generator.Resource(ctx, project, resource)
		if err != nil {
			t.Fatalf("plan resource %s: %v", resource.Name, err)
		}
		if _, err := generator.Apply(ctx, plan); err != nil {
			t.Fatalf("add resource %s: %v", resource.Name, err)
		}
	}
	customizeGeneratedArticle(t, project)
	workFile := newIntegrationGoWork(t, ctx, workspace, project)
	if output, err := runGeneratedGo(ctx, project, workFile, "generate", "./ent"); err != nil {
		t.Fatalf("generate Ent: %v\n%s", err, output)
	}
	fixture, err := os.ReadFile(filepath.Join("testdata", "generated_bulk_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "internal", "app", "generated_bulk_test.go"), fixture, 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := runGeneratedGo(ctx, project, workFile, "test", "./internal/app", "-run", "^$", "-count=1"); err != nil {
		t.Fatalf("compile generated bulk harness: %v\n%s", err, output)
	}
	dsn := containers.StartPostgres(t)
	command := exec.CommandContext(ctx, "go", "test", "./internal/app", "-run", "^TestGeneratedBulkHTTP$", "-count=1")
	command.Dir = project
	command.Env = replaceEnvironment(replaceEnvironment(os.Environ(), "GOWORK", workFile), "COMXKIT_TEST_DSN", dsn)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated HTTP bulk behavior failed: %v\n%s", err, output)
	}
}

func customizeGeneratedArticle(t *testing.T, project string) {
	t.Helper()
	paths := []string{
		"ent/schema/article.go",
		"internal/catalog/article/model.go",
		"internal/catalog/article/request.go",
		"internal/catalog/article/service.go",
		"internal/catalog/article/repository.go",
	}
	files := make(map[string]string, len(paths))
	for _, name := range paths {
		content, err := os.ReadFile(filepath.Join(project, name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(content)
	}
	tick := string(rune(96))
	files[paths[0]] = replaceGeneratedSource(t, files[paths[0]],
		"\t\"entgo.io/ent\"\n", "\t\"entgo.io/ent\"\n\t\"entgo.io/ent/schema/field\"\n")
	files[paths[0]] = replaceGeneratedSource(t, files[paths[0]], "func (Article) Mixin() []ent.Mixin {",
		"func (Article) Fields() []ent.Field {\n\treturn []ent.Field{field.String(\"name\").NotEmpty()}\n}\n\nfunc (Article) Mixin() []ent.Mixin {")
	files[paths[1]] = replaceGeneratedSource(t, files[paths[1]], "type Model struct {\n",
		"type Model struct {\n\tName string "+tick+"json:\"name\""+tick+"\n")
	files[paths[2]] = replaceGeneratedSource(t, files[paths[2]],
		"type CreateInput struct {\n\t// Add explicitly writable business fields here; never bind system metadata.\n}",
		"type CreateInput struct {\n\tName string "+tick+"json:\"name\" validate:\"required\""+tick+"\n}")
	files[paths[2]] = replaceGeneratedSource(t, files[paths[2]],
		"ID string "+tick+"uri:\"id\" json:\"-\" validate:\"required\""+tick+"\n\t// Add optional, explicitly writable business fields here.",
		"ID string "+tick+"uri:\"id\" json:\"-\" validate:\"required\""+tick+"\n\tName *string "+tick+"json:\"name,omitempty\" validate:\"omitempty,min=1\""+tick)
	files[paths[3]] = replaceGeneratedSource(t, files[paths[3]], "\t\"context\"\n", "\t\"context\"\n\t\"strings\"\n")
	files[paths[3]] = replaceGeneratedSource(t, files[paths[3]],
		"func validateCreate(CreateInput) error { return nil }",
		"func validateCreate(input CreateInput) error {\n\tif strings.TrimSpace(input.Name) == \"\" {\n\t\treturn invalidInput(\"name is required\")\n\t}\n\treturn nil\n}")
	files[paths[3]] = replaceGeneratedSource(t, files[paths[3]],
		"func validateUpdate(UpdateInput) error {\n\treturn invalidInput(\"update requires at least one editable field\")\n}",
		"func validateUpdate(input UpdateInput) error {\n\tif input.Name == nil || strings.TrimSpace(*input.Name) == \"\" {\n\t\treturn invalidInput(\"name is required\")\n\t}\n\treturn nil\n}")
	files[paths[3]] = replaceGeneratedSource(t, files[paths[3]],
		"model := Model{ID: row.ID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}",
		"model := Model{ID: row.ID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Name: row.Name}")
	files[paths[4]] = replaceGeneratedSource(t, files[paths[4]],
		"func prepareCreate(builder *genent.ArticleCreate, _ CreateInput) *genent.ArticleCreate {\n\treturn builder\n}",
		"func prepareCreate(builder *genent.ArticleCreate, input CreateInput) *genent.ArticleCreate {\n\treturn builder.SetName(input.Name)\n}")
	files[paths[4]] = replaceGeneratedSource(t, files[paths[4]],
		"func prepareUpdate(builder *genent.ArticleUpdateOne, _ UpdateInput) *genent.ArticleUpdateOne {\n\treturn builder\n}",
		"func prepareUpdate(builder *genent.ArticleUpdateOne, input UpdateInput) *genent.ArticleUpdateOne {\n\tif input.Name != nil {\n\t\tbuilder.SetName(*input.Name)\n\t}\n\treturn builder\n}")
	for _, name := range paths {
		formatted, err := format.Source([]byte(files[name]))
		if err != nil {
			t.Fatalf("format customized %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(project, name), formatted, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func replaceGeneratedSource(t *testing.T, source, old, replacement string) string {
	t.Helper()
	if strings.Count(source, old) != 1 {
		t.Fatalf("generated customization point not found exactly once: %q", old)
	}
	return strings.Replace(source, old, replacement, 1)
}
