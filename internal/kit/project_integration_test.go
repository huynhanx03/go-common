//go:build integration

package kit

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGeneratedEmptyProject(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	workspace := newIntegrationWorkspace(t, ctx)
	project := filepath.Join(workspace, "shop")
	generator := newGenerator(defaultRegistry())
	plan, err := generator.Init(ctx, InitInput{
		Directory: project, Module: "example.com/shop", HTTP: stackGin, ORM: stackEnt,
		Database: stackPostgres, CommonVersion: "v1.2.3",
	})
	if err != nil {
		t.Fatalf("plan project: %v", err)
	}
	if _, err := generator.Apply(ctx, plan); err != nil {
		t.Fatalf("create project: %v", err)
	}
	workFile := newIntegrationGoWork(t, ctx, workspace, project)
	verifyGeneratedProject(t, ctx, project, workFile)
}

func TestGeneratedProjectMatrix(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
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
	for _, name := range []string{"catalog", "learning"} {
		plan, err := generator.Domain(ctx, project, DomainInput{Name: name})
		if err != nil {
			t.Fatalf("plan domain %s: %v", name, err)
		}
		if _, err := generator.Apply(ctx, plan); err != nil {
			t.Fatalf("add domain %s: %v", name, err)
		}
	}
	workFile := newIntegrationGoWork(t, ctx, workspace, project)
	verifyGeneratedProject(t, ctx, project, workFile)
	for _, resource := range []ResourceInput{
		{Name: "product", Domain: "catalog"},
		{
			Name: "book", Domain: "catalog", UpsertOn: upsertTargetID, SoftDelete: true, Modifier: true,
			Operations: append(append(defaultRegistry().defaultOperations(), operationUpsert),
				operationGetBulk, operationCreateBulk, operationUpdateBulk, operationUpsertBulk, operationDeleteBulk),
		},
		{Name: "article", Domain: "catalog", Modifier: true},
		{Name: "lesson", Domain: "learning"},
		{Name: "profile", Domain: "learning", Operations: []string{operationGet}},
		{Name: "search_result", Domain: "learning", Operations: []string{operationFind}},
		{Name: "snapshot", Domain: "learning", Operations: []string{operationUpsert}, UpsertOn: upsertTargetID},
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
	customizedArticle, err := os.ReadFile(filepath.Join(project, "internal", "catalog", "article", "service.go"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err = generator.Resource(ctx, project, ResourceInput{Name: "course", Domain: "learning"})
	if err != nil {
		t.Fatalf("plan resource after customization: %v", err)
	}
	if _, err := generator.Apply(ctx, plan); err != nil {
		t.Fatalf("add resource after customization: %v", err)
	}
	articleAfterAddition, err := os.ReadFile(filepath.Join(project, "internal", "catalog", "article", "service.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(articleAfterAddition) != string(customizedArticle) {
		t.Fatal("adding another resource changed the customized article service")
	}
	for index, operation := range []string{
		operationGetBulk, operationCreateBulk, operationUpdateBulk, operationUpsertBulk, operationDeleteBulk,
	} {
		resource := ResourceInput{
			Name: fmt.Sprintf("bulk_%d", index), Domain: "learning", Operations: []string{operation},
		}
		plan, err := generator.Resource(ctx, project, resource)
		if err != nil {
			t.Fatalf("plan bulk-only resource %s: %v", operation, err)
		}
		if _, err := generator.Apply(ctx, plan); err != nil {
			t.Fatalf("add bulk-only resource %s: %v", operation, err)
		}
	}
	if output, err := runGeneratedGo(ctx, project, workFile, "generate", "./ent"); err != nil {
		t.Fatalf("generate Ent: %v\n%s", err, output)
	}
	verifyGeneratedProject(t, ctx, project, workFile)
}

func verifyGeneratedProject(t *testing.T, ctx context.Context, project, workFile string) {
	t.Helper()
	for _, args := range [][]string{{"test", "./..."}, {"vet", "./..."}} {
		if output, err := runGeneratedGo(ctx, project, workFile, args...); err != nil {
			t.Fatalf("go %v failed: %v\n%s", args, err, output)
		}
	}
}

func newIntegrationWorkspace(t *testing.T, ctx context.Context) string {
	t.Helper()
	commonRoot := moduleRoot(t)
	workspace, err := os.MkdirTemp(filepath.Dir(commonRoot), ".comxkit-integration-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	return workspace
}

func newIntegrationGoWork(t *testing.T, ctx context.Context, workspace, project string) string {
	t.Helper()
	commonRoot := moduleRoot(t)
	projectFromWorkspace, err := filepath.Rel(workspace, project)
	if err != nil {
		t.Fatal(err)
	}
	commonFromWorkspace, err := filepath.Rel(workspace, commonRoot)
	if err != nil {
		t.Fatal(err)
	}
	initWorkspace := exec.CommandContext(ctx, "go", "work", "init", projectFromWorkspace, commonFromWorkspace)
	initWorkspace.Dir = workspace
	initWorkspace.Env = replaceEnvironment(os.Environ(), "GOWORK", "off")
	if output, err := initWorkspace.CombinedOutput(); err != nil {
		t.Fatalf("create external workspace: %v\n%s", err, output)
	}
	replacement := "-replace=github.com/huynhanx03/go-common@v1.2.3=" + commonFromWorkspace
	localCommon := exec.CommandContext(ctx, "go", "work", "edit", replacement)
	localCommon.Dir = workspace
	if output, err := localCommon.CombinedOutput(); err != nil {
		t.Fatalf("connect workspace to local go-common: %v\n%s", err, output)
	}
	return filepath.Join(workspace, "go.work")
}

func runGeneratedGo(ctx context.Context, project, workFile string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir = project
	command.Env = replaceEnvironment(os.Environ(), "GOWORK", workFile)
	return command.CombinedOutput()
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for directory := workingDirectory; ; directory = filepath.Dir(directory) {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("could not find the go-common module root")
		}
	}
}

func replaceEnvironment(environment []string, key, value string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if !strings.HasPrefix(item, key+"=") {
			result = append(result, item)
		}
	}
	return append(result, key+"="+value)
}
