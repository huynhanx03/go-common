package kit

import (
	"bytes"
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestProjectGeneration(t *testing.T) {
	generator := newGenerator(defaultRegistry())

	t.Run("init stages an absent or empty destination and records owned hashes", func(t *testing.T) {
		for _, existing := range []bool{false, true} {
			t.Run(map[bool]string{false: "absent", true: "empty"}[existing], func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "shop")
				if existing {
					if err := os.Mkdir(root, 0o750); err != nil {
						t.Fatal(err)
					}
				}
				plan := Plan{
					Root: root, Kind: "init",
					Metadata: &projectMetadata{Version: metadataVersion, Module: "example.com/shop", HTTP: stackGin, ORM: stackEnt, Database: stackPostgres},
					Files: []PlannedFile{
						{Path: "README.md", Content: []byte("shop\n")},
						{Path: "internal/domains_gen.go", Content: []byte("package internal\n"), Owned: true},
					},
				}
				result, err := generator.Apply(context.Background(), plan)
				if err != nil {
					t.Fatalf("Apply() error = %v", err)
				}
				if result.Root != root || len(result.Created) != 2 || result.NoOp {
					t.Fatalf("Apply() result = %#v", result)
				}
				if got, err := os.ReadFile(filepath.Join(root, "README.md")); err != nil || string(got) != "shop\n" {
					t.Fatalf("README = %q, %v", got, err)
				}
				metadata := readTestMetadata(t, root)
				if metadata.Files["internal/domains_gen.go"] != contentHash([]byte("package internal\n")) {
					t.Fatalf("owned hash = %q, want hash of generated file", metadata.Files["internal/domains_gen.go"])
				}
			})
		}
	})

	t.Run("add replaces unchanged owned files and preserves user source", func(t *testing.T) {
		root := newTestProject(t, generator)
		userSource := filepath.Join(root, "internal/catalog/product/service.go")
		if err := os.MkdirAll(filepath.Dir(userSource), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(userSource, []byte("package product\n// user changes\n"), 0o640); err != nil {
			t.Fatal(err)
		}
		generated := "package internal\nvar Resource = \"product\"\n"
		result, err := generator.Apply(context.Background(), Plan{
			Root: root, Kind: "resource",
			ExpectedMetadataHash: metadataHash(t, root),
			Files: []PlannedFile{
				{Path: "internal/domains_gen.go", Content: []byte(generated), ExpectedHash: contentHash([]byte("package internal\n")), Owned: true},
				{Path: "internal/catalog/product/request.go", Content: []byte("package product\n"), Owned: false},
			},
		})
		if err != nil {
			t.Fatalf("Apply() error = %v", err)
		}
		if len(result.Updated) != 1 || len(result.Created) != 1 {
			t.Fatalf("Apply() result = %#v", result)
		}
		if got, err := os.ReadFile(userSource); err != nil || string(got) != "package product\n// user changes\n" {
			t.Fatalf("user source = %q, %v", got, err)
		}
		if metadata := readTestMetadata(t, root); metadata.Files["internal/domains_gen.go"] != contentHash([]byte(generated)) {
			t.Fatal("metadata does not track the replacement registration")
		}
	})

	t.Run("rejects populated roots user-file collisions and edited owned files", func(t *testing.T) {
		t.Run("populated init root", func(t *testing.T) {
			root := t.TempDir()
			userFile := filepath.Join(root, "keep.txt")
			if err := os.WriteFile(userFile, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := generator.Apply(context.Background(), initTestPlan(root))
			if !errors.Is(err, ErrFileConflict) {
				t.Fatalf("Apply() error = %v, want file conflict", err)
			}
			if got, err := os.ReadFile(userFile); err != nil || string(got) != "keep" {
				t.Fatalf("existing file changed: %q, %v", got, err)
			}
		})

		t.Run("new file collides with user file", func(t *testing.T) {
			root := newTestProject(t, generator)
			path := filepath.Join(root, "README.md")
			if err := os.WriteFile(path, []byte("my readme"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := generator.Apply(context.Background(), addTestPlan(t, root, PlannedFile{Path: "README.md", Content: []byte("generated")}))
			if !errors.Is(err, ErrFileConflict) {
				t.Fatalf("Apply() error = %v, want file conflict", err)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != "my readme" {
				t.Fatalf("user file changed: %q, %v", got, err)
			}
		})

		t.Run("generated file was edited", func(t *testing.T) {
			root := newTestProject(t, generator)
			path := filepath.Join(root, "internal/domains_gen.go")
			if err := os.WriteFile(path, []byte("user edit"), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := generator.Apply(context.Background(), addTestPlan(t, root, PlannedFile{
				Path: "internal/domains_gen.go", Content: []byte("new generated"), ExpectedHash: contentHash([]byte("package internal\n")), Owned: true,
			}))
			if !errors.Is(err, ErrFileConflict) {
				t.Fatalf("Apply() error = %v, want file conflict", err)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != "user edit" {
				t.Fatalf("edited generated file changed: %q, %v", got, err)
			}
		})
	})

	t.Run("rejects traversal absolute and symlink destinations", func(t *testing.T) {
		root := newTestProject(t, generator)
		outside := filepath.Join(t.TempDir(), "outside.txt")
		for _, path := range []string{"../outside.txt", outside} {
			_, err := generator.Apply(context.Background(), addTestPlan(t, root, PlannedFile{Path: path, Content: []byte("escape")}))
			if !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("Apply(path %q) error = %v, want unsafe path", path, err)
			}
		}
		outsideDir := t.TempDir()
		if err := os.Symlink(outsideDir, filepath.Join(root, "linked")); err != nil {
			t.Skipf("symlinks are unavailable: %v", err)
		}
		_, err := generator.Apply(context.Background(), addTestPlan(t, root, PlannedFile{Path: "linked/escape.txt", Content: []byte("escape")}))
		if !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("Apply(symlink path) error = %v, want unsafe path", err)
		}
		if _, err := os.Stat(filepath.Join(outsideDir, "escape.txt")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("symlink target was written: %v", err)
		}
	})

	t.Run("rejects stale metadata overlapping files and incomplete journals", func(t *testing.T) {
		root := newTestProject(t, generator)
		originalMetadataHash := metadataHash(t, root)
		metadataPath := filepath.Join(root, metadataFileName)
		if err := os.WriteFile(metadataPath, []byte("version: 999\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := generator.Apply(context.Background(), Plan{Root: root, Kind: "domain", ExpectedMetadataHash: originalMetadataHash, Files: []PlannedFile{{Path: "x.go", Content: []byte("x")}}})
		if !errors.Is(err, ErrFileConflict) {
			t.Fatalf("stale metadata error = %v, want conflict", err)
		}

		if err := os.Remove(metadataPath); err != nil {
			t.Fatal(err)
		}
		if _, err := generator.Apply(context.Background(), Plan{Root: root, Kind: "domain", Files: []PlannedFile{{Path: "same.go", Content: []byte("a")}, {Path: "same.go", Content: []byte("b")}}}); !errors.Is(err, ErrInvalidPlan) {
			t.Fatalf("overlapping plan error = %v, want invalid plan", err)
		}

		if err := os.WriteFile(metadataPath, mustMetadata(t), 0o644); err != nil {
			t.Fatal(err)
		}
		pending := []byte(`{"files":["internal/domains_gen.go"]}`)
		if err := os.WriteFile(filepath.Join(root, pendingFileName), pending, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = generator.Apply(context.Background(), Plan{Root: root, Kind: "domain", Files: []PlannedFile{{Path: "x.go", Content: []byte("x")}}})
		if !errors.Is(err, ErrRecoveryRequired) || !strings.Contains(err.Error(), "internal/domains_gen.go") {
			t.Fatalf("pending journal error = %v, want recovery error naming affected file", err)
		}
	})

	t.Run("cancellation leaves no partial init or add", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "shop")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := generator.Apply(ctx, initTestPlan(root)); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled init error = %v", err)
		}
		if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cancelled init left destination: %v", err)
		}

		root = newTestProject(t, generator)
		ctx, cancel = context.WithCancel(context.Background())
		cancel()
		_, err := generator.Apply(ctx, addTestPlan(t, root, PlannedFile{Path: "new.go", Content: []byte("package project\n")}))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled add error = %v", err)
		}
		if _, err := os.Lstat(filepath.Join(root, "new.go")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cancelled add wrote a file: %v", err)
		}
	})

	t.Run("rollback restores files and modes when publication fails", func(t *testing.T) {
		root := newTestProject(t, generator)
		ownedPath := filepath.Join(root, "internal/domains_gen.go")
		if err := os.Chmod(ownedPath, 0o600); err != nil {
			t.Fatal(err)
		}
		plan := addTestPlan(t, root,
			PlannedFile{Path: "internal/domains_gen.go", Content: []byte("updated"), ExpectedHash: contentHash([]byte("package internal\n")), Owned: true},
			PlannedFile{Path: "new.go", Content: []byte("new"), Owned: false},
		)
		failTarget := filepath.Join(root, "new.go")
		writer := newGeneratorWithFileOperations(defaultRegistry(), fileOperations{
			publish: func(source, target string) error {
				if target == failTarget {
					return errors.New("injected publish failure")
				}
				return os.Rename(source, target)
			},
		})
		_, err := writer.Apply(context.Background(), plan)
		if err == nil || !strings.Contains(err.Error(), "injected publish failure") {
			t.Fatalf("Apply() error = %v, want injected failure", err)
		}
		if got, err := os.ReadFile(ownedPath); err != nil || string(got) != "package internal\n" {
			t.Fatalf("updated file was not rolled back: %q, %v", got, err)
		}
		if info, err := os.Stat(ownedPath); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("updated file mode = %v, %v; want 0600", info, err)
		}
		if _, err := os.Lstat(failTarget); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("failed new file remains: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(root, pendingFileName)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("successful rollback left recovery marker: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(root, lockFileName)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("mutation lock was not released: %v", err)
		}
	})

	t.Run("rollback handles cancellation after publication and lock contention", func(t *testing.T) {
		root := newTestProject(t, generator)
		first := filepath.Join(root, "one.go")
		ctx, cancel := context.WithCancel(context.Background())
		writer := newGeneratorWithFileOperations(defaultRegistry(), fileOperations{
			publish: func(source, target string) error {
				if err := os.Rename(source, target); err != nil {
					return err
				}
				if target == first {
					cancel()
				}
				return nil
			},
		})
		_, err := writer.Apply(ctx, addTestPlan(t, root,
			PlannedFile{Path: "one.go", Content: []byte("one")},
			PlannedFile{Path: "two.go", Content: []byte("two")},
		))
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Apply() error = %v, want cancellation", err)
		}
		if _, err := os.Lstat(first); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("first publication was not rolled back: %v", err)
		}

		if err := os.WriteFile(filepath.Join(root, lockFileName), []byte("held"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := generator.Apply(context.Background(), addTestPlan(t, root, PlannedFile{Path: "locked.go", Content: []byte("x")})); !errors.Is(err, ErrMutationLocked) {
			t.Fatalf("locked Apply() error = %v, want mutation lock", err)
		}
	})

	t.Run("an identical no-op is verified and does not mutate files", func(t *testing.T) {
		root := newTestProject(t, generator)
		plan := Plan{Root: root, Kind: "resource", NoOp: true, Files: []PlannedFile{{Path: "internal/domains_gen.go", Content: []byte("package internal\n"), Owned: true}}}
		result, err := generator.Apply(context.Background(), plan)
		if err != nil || !result.NoOp {
			t.Fatalf("Apply() = %#v, %v; want no-op", result, err)
		}
		plan.Files[0].Content = []byte("edited")
		if _, err := generator.Apply(context.Background(), plan); !errors.Is(err, ErrFileConflict) {
			t.Fatalf("altered no-op error = %v, want conflict", err)
		}
	})
}

func TestInitProject(t *testing.T) {
	generator := newGenerator(defaultRegistry())
	root := filepath.Join(t.TempDir(), "shop")
	input := InitInput{
		Directory:     root,
		Module:        "github.com/example/shop",
		HTTP:          stackGin,
		ORM:           stackEnt,
		Database:      stackPostgres,
		CommonVersion: "v1.2.3",
	}

	t.Run("plans a coherent runnable project without running commands", func(t *testing.T) {
		plan, err := generator.Init(context.Background(), input)
		if err != nil {
			t.Fatalf("Init() error = %v", err)
		}
		if plan.Root != root || plan.Kind != "init" || plan.Metadata == nil {
			t.Fatalf("Init() plan = %#v", plan)
		}
		if got := plan.Metadata.Module; got != input.Module {
			t.Fatalf("module metadata = %q, want %q", got, input.Module)
		}
		files := make(map[string][]byte, len(plan.Files))
		for _, file := range plan.Files {
			if _, duplicate := files[file.Path]; duplicate {
				t.Fatalf("plan contains duplicate path %q", file.Path)
			}
			files[file.Path] = file.Content
			if strings.HasSuffix(file.Path, ".go") {
				if _, err := parser.ParseFile(token.NewFileSet(), file.Path, file.Content, parser.AllErrors); err != nil {
					t.Errorf("generated Go file %s does not parse: %v", file.Path, err)
				}
			}
		}
		for _, path := range []string{
			"cmd/server/main.go", "cmd/migrate/main.go", "cmd/migrate/schema_gen.go",
			"internal/app/client.go", "internal/app/config.go", "internal/app/http.go", "internal/app/domains_gen.go",
			"ent/schema/.gitkeep", "ent/generate.go", "migrations/.gitkeep", "go.mod", ".env.example",
			".gitignore", "README.md", "Makefile", "infrastructure/docker/postgres.yml",
		} {
			if _, ok := files[path]; !ok {
				t.Errorf("project plan is missing %s", path)
			}
		}
		mod := string(files["go.mod"])
		for _, want := range []string{input.Module, input.CommonVersion, "go 1.27.3", "entgo.io/ent", "github.com/gin-gonic/gin"} {
			if !strings.Contains(mod, want) {
				t.Errorf("generated go.mod does not contain %q", want)
			}
		}
		if strings.Contains(mod, "replace ") {
			t.Error("generated go.mod contains a local replace")
		}
		if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("planning created the project root: %v", err)
		}
	})

	t.Run("rejects invalid module and stack before any file is written", func(t *testing.T) {
		for _, test := range []struct {
			name  string
			input InitInput
			want  error
		}{
			{name: "malformed module", input: InitInput{Directory: root, Module: "not a module", HTTP: stackGin, ORM: stackEnt, Database: stackPostgres, CommonVersion: "v1.2.3"}, want: ErrInvalidModule},
			{name: "unsupported stack", input: InitInput{Directory: root, Module: "example.com/shop", HTTP: "unknown", ORM: stackEnt, Database: stackPostgres, CommonVersion: "v1.2.3"}, want: ErrInvalidSelection},
			{name: "missing common version", input: InitInput{Directory: root, Module: "example.com/shop", HTTP: stackGin, ORM: stackEnt, Database: stackPostgres}, want: ErrMissingCommonVersion},
		} {
			t.Run(test.name, func(t *testing.T) {
				_, err := generator.Init(context.Background(), test.input)
				if !errors.Is(err, test.want) {
					t.Fatalf("Init() error = %v, want %v", err, test.want)
				}
				if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("invalid plan created the project root: %v", err)
				}
			})
		}
	})

	t.Run("cancellation stops planning before Apply", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := generator.Init(ctx, input)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Init() error = %v, want cancellation", err)
		}
		if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cancelled planning created the project root: %v", err)
		}
	})

	t.Run("CLI flags create the same selected stack", func(t *testing.T) {
		cliRoot := filepath.Join(t.TempDir(), "shop")
		promptRoot := filepath.Join(t.TempDir(), "shop")
		expectedPlan, err := generator.Init(context.Background(), InitInput{
			Directory: cliRoot, Module: "github.com/example/shop", HTTP: stackGin,
			ORM: stackEnt, Database: stackPostgres, CommonVersion: "v1.2.3",
		})
		if err != nil {
			t.Fatalf("plan flag output: %v", err)
		}
		var output bytes.Buffer
		command := NewCommand(Options{Out: &output, Err: &output, CommonVersion: "v1.2.3"})
		command.SetArgs([]string{
			"init", cliRoot, "--module", "github.com/example/shop", "--http", stackGin,
			"--orm", stackEnt, "--db", stackPostgres, "--yes",
		})
		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("execute init: %v", err)
		}
		metadata := readTestMetadata(t, cliRoot)
		if metadata.Module != "github.com/example/shop" || metadata.HTTP != stackGin || metadata.ORM != stackEnt || metadata.Database != stackPostgres {
			t.Fatalf("CLI selected metadata = %#v", metadata)
		}
		if !strings.Contains(output.String(), "Added project") {
			t.Fatalf("CLI output = %q, want project result", output.String())
		}

		prompt := &recordingPrompter{
			project: InitInput{
				Directory: promptRoot, Module: "github.com/example/shop", HTTP: stackGin,
				ORM: stackEnt, Database: stackPostgres, CommonVersion: "v1.2.3",
			},
			confirmed: true,
		}
		command = testCommand(t, Options{
			Interactive: true, CommonVersion: "v1.2.3", Out: &output, Err: &output,
		}, defaultRegistry(), prompt, newGenerator(defaultRegistry()))
		command.SetArgs([]string{"init"})
		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("execute prompt init: %v", err)
		}
		for _, file := range expectedPlan.Files {
			flagContent, err := os.ReadFile(filepath.Join(cliRoot, file.Path))
			if err != nil {
				t.Fatal(err)
			}
			promptContent, err := os.ReadFile(filepath.Join(promptRoot, file.Path))
			if err != nil {
				t.Fatal(err)
			}
			if string(flagContent) != string(promptContent) {
				t.Errorf("flag and prompt output differ at %s", file.Path)
			}
		}
		flagMetadata, err := os.ReadFile(filepath.Join(cliRoot, metadataFileName))
		if err != nil {
			t.Fatal(err)
		}
		promptMetadata, err := os.ReadFile(filepath.Join(promptRoot, metadataFileName))
		if err != nil {
			t.Fatal(err)
		}
		if string(flagMetadata) != string(promptMetadata) {
			t.Error("flag and prompt metadata differ")
		}
	})

	t.Run("Apply rejects an existing user file without changing it", func(t *testing.T) {
		if err := os.MkdirAll(root, 0o750); err != nil {
			t.Fatal(err)
		}
		keep := filepath.Join(root, "keep.txt")
		if err := os.WriteFile(keep, []byte("user data"), 0o600); err != nil {
			t.Fatal(err)
		}
		plan, err := generator.Init(context.Background(), input)
		if err != nil {
			t.Fatalf("Init() error = %v", err)
		}
		if _, err := generator.Apply(context.Background(), plan); !errors.Is(err, ErrFileConflict) {
			t.Fatalf("Apply() error = %v, want conflict", err)
		}
		if got, err := os.ReadFile(keep); err != nil || string(got) != "user data" {
			t.Fatalf("existing file = %q, %v", got, err)
		}
	})
}

func TestAddDomainResource(t *testing.T) {
	generator := newGenerator(defaultRegistry())

	t.Run("domain scaffold is repeatable and registers in stable project assembly", func(t *testing.T) {
		root := newGeneratedProject(t, generator)
		plan, err := generator.Domain(context.Background(), root, DomainInput{Name: "catalog"})
		if err != nil {
			t.Fatalf("Domain() error = %v", err)
		}
		if _, err := generator.Apply(context.Background(), plan); err != nil {
			t.Fatalf("apply domain: %v", err)
		}
		metadata := readTestMetadata(t, root)
		if !slices.Contains(metadata.Domains, "catalog") {
			t.Fatalf("domains = %v, want catalog", metadata.Domains)
		}
		for _, path := range []string{
			"internal/catalog/module.go", "internal/catalog/resources_gen.go",
			"internal/app/domains_gen.go", "cmd/migrate/schema_gen.go",
		} {
			if _, err := os.Stat(filepath.Join(root, path)); err != nil {
				t.Errorf("domain scaffold missing %s: %v", path, err)
			}
		}
		assembly, err := os.ReadFile(filepath.Join(root, "internal/app/domains_gen.go"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(assembly), ".New(") {
			t.Fatalf("domain assembly does not register catalog: %s", assembly)
		}

		repeat, err := generator.Domain(context.Background(), root, DomainInput{Name: "catalog"})
		if err != nil || !repeat.NoOp {
			t.Fatalf("repeat Domain() = %#v, %v; want no-op", repeat, err)
		}
		if _, err := generator.Apply(context.Background(), repeat); err != nil {
			t.Fatalf("verify repeated domain: %v", err)
		}
	})

	t.Run("resource scaffold records routes and preserves project-owned files", func(t *testing.T) {
		root := newGeneratedProject(t, generator)
		addDomainForTest(t, generator, root, "catalog")
		product := ResourceInput{Name: "product", Domain: "catalog"}
		plan, err := generator.Resource(context.Background(), root, product)
		if err != nil {
			t.Fatalf("Resource() error = %v", err)
		}
		if _, err := generator.Apply(context.Background(), plan); err != nil {
			t.Fatalf("apply resource: %v", err)
		}
		metadata := readTestMetadata(t, root)
		if len(metadata.Resources) != 1 || metadata.Resources[0].Route != "products" || !slices.Equal(metadata.Resources[0].Operations, defaultRegistry().defaultOperations()) {
			t.Fatalf("resource metadata = %#v", metadata.Resources)
		}
		for _, path := range []string{
			"ent/schema/product.go", "internal/catalog/product/model.go", "internal/catalog/product/request.go",
			"internal/catalog/product/service.go", "internal/catalog/product/repository.go", "internal/catalog/product/http.go",
		} {
			if _, err := os.Stat(filepath.Join(root, path)); err != nil {
				t.Errorf("resource scaffold missing %s: %v", path, err)
			}
		}
		resources, err := os.ReadFile(filepath.Join(root, "internal/catalog/resources_gen.go"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(resources), `Group("/products")`) {
			t.Fatalf("resource route not registered: %s", resources)
		}
		personPlan, err := generator.Resource(context.Background(), root, ResourceInput{
			Name: "person", Domain: "catalog", Route: "people", Operations: []string{operationGet},
		})
		if err != nil {
			t.Fatalf("plan get-only resource with irregular route: %v", err)
		}
		if _, err := generator.Apply(context.Background(), personPlan); err != nil {
			t.Fatalf("add get-only resource: %v", err)
		}
		personHTTP, err := os.ReadFile(filepath.Join(root, "internal/catalog/person/http.go"))
		if err != nil {
			t.Fatal(err)
		}
		personService, err := os.ReadFile(filepath.Join(root, "internal/catalog/person/service.go"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(personHTTP), `router.GET("/:id"`) || strings.Contains(string(personHTTP), `router.POST(`) ||
			!strings.Contains(string(personService), "Get(ctx context.Context") || strings.Contains(string(personService), "Create(ctx context.Context") {
			t.Fatalf("get-only operation selection generated unrelated endpoints or methods:\n%s\n%s", personHTTP, personService)
		}
		metadata = readTestMetadata(t, root)
		if !slices.ContainsFunc(metadata.Resources, func(resource resourceRecord) bool {
			return resource.Name == "person" && resource.Route == "people" && slices.Equal(resource.Operations, []string{operationGet})
		}) {
			t.Fatalf("irregular route or operation selection missing from metadata: %#v", metadata.Resources)
		}

		moduleFile := filepath.Join(root, "internal/catalog/module.go")
		serviceFile := filepath.Join(root, "internal/catalog/product/service.go")
		userModule := []byte("package catalog\n// customized by the project\n")
		userService := []byte("package product\n// customized service behavior\n")
		if err := os.WriteFile(moduleFile, userModule, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(serviceFile, userService, 0o644); err != nil {
			t.Fatal(err)
		}
		second, err := generator.Resource(context.Background(), root, ResourceInput{Name: "book", Domain: "catalog"})
		if err != nil {
			t.Fatalf("plan second resource: %v", err)
		}
		if _, err := generator.Apply(context.Background(), second); err != nil {
			t.Fatalf("apply second resource: %v", err)
		}
		for path, want := range map[string][]byte{moduleFile: userModule, serviceFile: userService} {
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, want) {
				t.Errorf("project-owned source %s changed to %q, %v", path, got, err)
			}
		}

		repeat, err := generator.Resource(context.Background(), root, product)
		if err != nil || !repeat.NoOp {
			t.Fatalf("repeat Resource() = %#v, %v; want no-op", repeat, err)
		}
	})

	t.Run("rejects missing domains invalid names and duplicate schema or route", func(t *testing.T) {
		root := newGeneratedProject(t, generator)
		if _, err := generator.Resource(context.Background(), root, ResourceInput{Name: "product", Domain: "missing"}); !errors.Is(err, ErrDomainNotFound) {
			t.Fatalf("resource for missing domain error = %v", err)
		}
		for _, name := range []string{"type", "../escape", "bad-name", ""} {
			if _, err := generator.Domain(context.Background(), root, DomainInput{Name: name}); !errors.Is(err, ErrInvalidDomain) {
				t.Errorf("Domain(%q) error = %v, want invalid domain", name, err)
			}
		}
		addDomainForTest(t, generator, root, "catalog")
		addDomainForTest(t, generator, root, "inventory")
		addResourceForTest(t, generator, root, ResourceInput{Name: "product", Domain: "catalog"})
		if _, err := generator.Resource(context.Background(), root, ResourceInput{Name: "product", Domain: "inventory"}); !errors.Is(err, ErrDuplicateSchema) {
			t.Fatalf("duplicate schema error = %v", err)
		}
		if _, err := generator.Resource(context.Background(), root, ResourceInput{Name: "item", Domain: "catalog", Route: "products"}); !errors.Is(err, ErrDuplicateRoute) {
			t.Fatalf("duplicate route error = %v", err)
		}
		for _, name := range []string{"type", "../escape", "bad-name", ""} {
			if _, err := generator.Resource(context.Background(), root, ResourceInput{Name: name, Domain: "catalog"}); !errors.Is(err, ErrInvalidResource) {
				t.Errorf("Resource(%q) error = %v, want invalid resource", name, err)
			}
		}
		if _, err := generator.Resource(context.Background(), root, ResourceInput{Name: "item", Domain: "catalog", Route: "../escape"}); !errors.Is(err, ErrInvalidResource) {
			t.Fatalf("unsafe route error = %v", err)
		}
	})

	t.Run("rejects conflicting resource options and malformed operation selections", func(t *testing.T) {
		root := newGeneratedProject(t, generator)
		addDomainForTest(t, generator, root, "catalog")
		addResourceForTest(t, generator, root, ResourceInput{Name: "product", Domain: "catalog"})
		_, err := generator.Resource(context.Background(), root, ResourceInput{Name: "product", Domain: "catalog", Operations: []string{operationGet}})
		if !errors.Is(err, ErrResourceConflict) {
			t.Fatalf("conflicting resource options error = %v", err)
		}
		_, err = generator.Resource(context.Background(), root, ResourceInput{Name: "item", Domain: "catalog", Operations: []string{operationGet, operationGet}})
		if !errors.Is(err, ErrInvalidOperation) {
			t.Fatalf("duplicate operation error = %v", err)
		}
		_, err = generator.Resource(context.Background(), root, ResourceInput{Name: "item", Domain: "catalog", Operations: []string{"unknown"}})
		if !errors.Is(err, ErrInvalidOperation) {
			t.Fatalf("unknown operation error = %v", err)
		}
		if _, err := generator.Resource(context.Background(), root, ResourceInput{
			Name: "product", Domain: "catalog", SoftDelete: true,
		}); !errors.Is(err, ErrResourceConflict) {
			t.Fatalf("conflicting mixin options error = %v", err)
		}
		if _, err := generator.Resource(context.Background(), root, ResourceInput{
			Name: "invoice", Domain: "catalog", Operations: []string{operationUpsert}, UpsertOn: "email",
		}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("unsupported upsert target error = %v", err)
		}
	})

	t.Run("upsert and mixin choices generate per-resource capabilities", func(t *testing.T) {
		root := newGeneratedProject(t, generator)
		addDomainForTest(t, generator, root, "catalog")
		addResourceForTest(t, generator, root, ResourceInput{
			Name: "invoice", Domain: "catalog", Operations: []string{operationUpsert},
			UpsertOn: upsertTargetID, SoftDelete: true, Modifier: true,
		})
		metadata := readTestMetadata(t, root)
		if len(metadata.Resources) != 1 || !metadata.Resources[0].SoftDelete || !metadata.Resources[0].Modifier ||
			!slices.Equal(metadata.Resources[0].Operations, []string{operationUpsert}) {
			t.Fatalf("resource configuration was not recorded: %#v", metadata.Resources)
		}
		for _, path := range []string{
			"internal/catalog/invoice/http.go", "internal/catalog/invoice/service.go",
			"internal/catalog/invoice/repository.go", "internal/app/domains_gen.go",
		} {
			if _, err := os.Stat(filepath.Join(root, path)); err != nil {
				t.Errorf("configured resource output missing %s: %v", path, err)
			}
		}
	})

	t.Run("bulk operations generate only selected routes", func(t *testing.T) {
		root := newGeneratedProject(t, generator)
		addDomainForTest(t, generator, root, "catalog")
		operations := []string{operationGet, operationCreate, operationGetBulk, operationCreateBulk}
		plan, err := generator.Resource(context.Background(), root, ResourceInput{
			Name: "product", Domain: "catalog", Operations: operations,
		})
		if err != nil {
			t.Fatalf("Resource() with selected bulk operations: %v", err)
		}
		if _, err := generator.Apply(context.Background(), plan); err != nil {
			t.Fatalf("apply selected bulk operations: %v", err)
		}
		metadata := readTestMetadata(t, root)
		if !slices.Equal(metadata.Resources[0].Operations, operations) {
			t.Fatalf("stored operations = %v, want %v", metadata.Resources[0].Operations, operations)
		}
		httpSource, err := os.ReadFile(filepath.Join(root, "internal/catalog/product/http.go"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(httpSource), `router.POST("/get-bulk"`) ||
			!strings.Contains(string(httpSource), `router.POST("/create-bulk"`) ||
			strings.Contains(string(httpSource), `router.POST("/update-bulk"`) {
			t.Fatalf("bulk route selection mismatch:\n%s", httpSource)
		}
	})

	t.Run("protects generated assembly from user edits", func(t *testing.T) {
		for _, path := range []string{"internal/catalog/resources_gen.go", "internal/app/domains_gen.go", "cmd/migrate/schema_gen.go"} {
			t.Run(path, func(t *testing.T) {
				root := newGeneratedProject(t, generator)
				addDomainForTest(t, generator, root, "catalog")
				addResourceForTest(t, generator, root, ResourceInput{Name: "product", Domain: "catalog"})
				if err := os.WriteFile(filepath.Join(root, path), []byte("package edited\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				plan, err := generator.Resource(context.Background(), root, ResourceInput{Name: "book", Domain: "catalog"})
				if err != nil {
					t.Fatalf("plan after editing %s: %v", path, err)
				}
				if _, err := generator.Apply(context.Background(), plan); !errors.Is(err, ErrFileConflict) {
					t.Errorf("apply after editing %s error = %v, want conflict", path, err)
				}
			})
		}
		t.Run("domain registration", func(t *testing.T) {
			root := newGeneratedProject(t, generator)
			addDomainForTest(t, generator, root, "catalog")
			path := filepath.Join(root, "internal/app/domains_gen.go")
			if err := os.WriteFile(path, []byte("package edited\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			plan, err := generator.Domain(context.Background(), root, DomainInput{Name: "learning"})
			if err != nil {
				t.Fatalf("plan domain after editing registration: %v", err)
			}
			if _, err := generator.Apply(context.Background(), plan); !errors.Is(err, ErrFileConflict) {
				t.Fatalf("apply after editing generated registration = %v, want conflict", err)
			}
		})
	})
}

func initTestPlan(root string) Plan {
	return Plan{
		Root: root, Kind: "init",
		Metadata: &projectMetadata{Version: metadataVersion, Module: "example.com/shop", HTTP: stackGin, ORM: stackEnt, Database: stackPostgres},
		Files: []PlannedFile{
			{Path: "README.md", Content: []byte("shop\n")},
			{Path: "internal/domains_gen.go", Content: []byte("package internal\n"), Owned: true},
		},
	}
}

func newTestProject(t *testing.T, generator *Generator) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "project")
	if _, err := generator.Apply(context.Background(), initTestPlan(root)); err != nil {
		t.Fatalf("create test project: %v", err)
	}
	return root
}

func newGeneratedProject(t *testing.T, generator *Generator) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "project")
	plan, err := generator.Init(context.Background(), InitInput{
		Directory: root, Module: "example.com/project", HTTP: stackGin, ORM: stackEnt,
		Database: stackPostgres, CommonVersion: "v1.2.3",
	})
	if err != nil {
		t.Fatalf("plan project: %v", err)
	}
	if _, err := generator.Apply(context.Background(), plan); err != nil {
		t.Fatalf("create project: %v", err)
	}
	return root
}

func addDomainForTest(t *testing.T, generator *Generator, root, name string) {
	t.Helper()
	plan, err := generator.Domain(context.Background(), root, DomainInput{Name: name})
	if err != nil {
		t.Fatalf("plan domain: %v", err)
	}
	if _, err := generator.Apply(context.Background(), plan); err != nil {
		t.Fatalf("add domain: %v", err)
	}
}

func addResourceForTest(t *testing.T, generator *Generator, root string, input ResourceInput) {
	t.Helper()
	plan, err := generator.Resource(context.Background(), root, input)
	if err != nil {
		t.Fatalf("plan resource: %v", err)
	}
	if _, err := generator.Apply(context.Background(), plan); err != nil {
		t.Fatalf("add resource: %v", err)
	}
}

func addTestPlan(t *testing.T, root string, files ...PlannedFile) Plan {
	t.Helper()
	return Plan{Root: root, Kind: "resource", ExpectedMetadataHash: metadataHash(t, root), Files: files}
}

func readTestMetadata(t *testing.T, root string) projectMetadata {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, metadataFileName))
	if err != nil {
		t.Fatal(err)
	}
	var metadata projectMetadata
	if err := yaml.Unmarshal(data, &metadata); err != nil {
		t.Fatal(err)
	}
	return metadata
}

func metadataHash(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, metadataFileName))
	if err != nil {
		t.Fatal(err)
	}
	return contentHash(data)
}

func mustMetadata(t *testing.T) []byte {
	t.Helper()
	data, err := yaml.Marshal(projectMetadata{Version: metadataVersion, Module: "example.com/shop"})
	if err != nil {
		t.Fatal(err)
	}
	return data
}
