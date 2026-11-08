package kit

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestCommandGeneration(t *testing.T) {
	t.Run("explicit flags generate the selected project", func(t *testing.T) {
		generator := &recordingGenerator{}
		command := testCommand(t, Options{WorkDir: t.TempDir(), Version: "test"}, defaultRegistry(), &recordingPrompter{}, generator)
		command.SetArgs([]string{
			"init", "shop", "--module", "example.com/shop", "--http", "gin", "--orm", "ent", "--db", "postgres",
			"--common-version", "v1.2.3", "--yes",
		})

		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("ExecuteContext() error = %v", err)
		}
		want := InitInput{
			Directory: "shop", Module: "example.com/shop", HTTP: "gin", ORM: "ent", Database: "postgres", CommonVersion: "v1.2.3",
		}
		if !reflect.DeepEqual(generator.initInput, want) {
			t.Fatalf("Init input = %#v, want %#v", generator.initInput, want)
		}
		if generator.initCalls != 1 || generator.applyCalls != 1 {
			t.Fatalf("generator calls = init:%d apply:%d, want init:1 apply:1", generator.initCalls, generator.applyCalls)
		}
	})

	t.Run("domain and resource commands dispatch their inputs", func(t *testing.T) {
		generator := &recordingGenerator{}
		root := t.TempDir()
		command := testCommand(t, Options{WorkDir: root}, defaultRegistry(), &recordingPrompter{}, generator)
		command.SetArgs([]string{"add", "domain", "catalog"})
		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("add domain: %v", err)
		}
		if generator.domainRoot != root || generator.domainInput.Name != "catalog" {
			t.Fatalf("domain dispatch = (%q, %#v)", generator.domainRoot, generator.domainInput)
		}

		generator.reset()
		command = testCommand(t, Options{WorkDir: root}, defaultRegistry(), &recordingPrompter{}, generator)
		command.SetArgs([]string{
			"add", "resource", "product", "--domain", "catalog", "--route", "products",
			"--operations", "find,get,create,update,delete,upsert", "--bulk", "--soft-delete", "--modifier", "--upsert-on", "id",
		})
		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("add resource: %v", err)
		}
		wantResource := ResourceInput{
			Name: "product", Domain: "catalog", Route: "products", UpsertOn: "id",
			Operations: []string{"find", "get", "create", "update", "delete", "upsert", "get-bulk", "create-bulk", "update-bulk", "delete-bulk", "upsert-bulk"},
			SoftDelete: true, Modifier: true,
		}
		if generator.resourceRoot != root || !reflect.DeepEqual(generator.resourceInput, wantResource) {
			t.Fatalf("resource dispatch = (%q, %#v), want (%q, %#v)", generator.resourceRoot, generator.resourceInput, root, wantResource)
		}
	})

	t.Run("help version and shell completion do not generate files", func(t *testing.T) {
		for _, args := range [][]string{
			{"--help"}, {"init", "--help"}, {"add", "resource", "--help"}, {"--version"}, {"version"},
			{"__complete", "init", "--http", "g"},
		} {
			t.Run(strings.Join(args, " "), func(t *testing.T) {
				generator := &recordingGenerator{}
				command := testCommand(t, Options{Version: "v1.2.3"}, defaultRegistry(), &recordingPrompter{}, generator)
				command.SetArgs(args)
				if err := command.ExecuteContext(context.Background()); err != nil {
					t.Fatalf("ExecuteContext(%v): %v", args, err)
				}
				if generator.initCalls+generator.domainCalls+generator.resourceCalls+generator.applyCalls != 0 {
					t.Fatalf("non-mutating command invoked generator: %#v", generator)
				}
			})
		}
	})

	t.Run("invalid input never prompts or invokes the generator", func(t *testing.T) {
		cases := []struct {
			name string
			args []string
			want error
		}{
			{name: "unknown command", args: []string{"initx"}},
			{name: "missing noninteractive module", args: []string{"init", "shop"}, want: ErrMissingModule},
			{name: "unknown HTTP implementation", args: []string{"init", "shop", "--module", "example.com/shop", "--http", "unknown"}, want: ErrInvalidSelection},
			{name: "malformed module path", args: []string{"init", "shop", "--module", "invalid", "--common-version", "v1.2.3"}, want: ErrInvalidModule},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				generator := &recordingGenerator{}
				prompter := &recordingPrompter{}
				command := testCommand(t, Options{WorkDir: t.TempDir()}, defaultRegistry(), prompter, generator)
				command.SetArgs(test.args)
				err := command.ExecuteContext(context.Background())
				if test.want == nil {
					if err == nil {
						t.Fatal("ExecuteContext() error = nil, want invalid command error")
					}
				} else if !errors.Is(err, test.want) {
					t.Fatalf("ExecuteContext() error = %v, want errors.Is(_, %v)", err, test.want)
				}
				if prompter.projectCalls+prompter.resourceCalls+prompter.confirmCalls != 0 {
					t.Fatalf("invalid input prompted: %#v", prompter)
				}
				if generator.initCalls+generator.domainCalls+generator.resourceCalls+generator.applyCalls != 0 {
					t.Fatalf("invalid input invoked generator: %#v", generator)
				}
			})
		}
	})

	t.Run("generator errors remain discoverable", func(t *testing.T) {
		cause := errors.New("write failed")
		generator := &recordingGenerator{err: cause}
		command := testCommand(t, Options{WorkDir: t.TempDir()}, defaultRegistry(), &recordingPrompter{}, generator)
		command.SetArgs([]string{"init", "shop", "--module", "example.com/shop", "--common-version", "v1.2.3", "--yes"})
		if err := command.ExecuteContext(context.Background()); !errors.Is(err, cause) {
			t.Fatalf("ExecuteContext() error = %v, want wrapped cause", err)
		}
	})

	t.Run("dry run builds a plan without applying it", func(t *testing.T) {
		generator := &recordingGenerator{plan: Plan{Root: "shop", Files: []PlannedFile{{Path: "README.md"}}}}
		var output bytes.Buffer
		command := newCommand(Options{WorkDir: t.TempDir(), Out: &output}, defaultRegistry(), &recordingPrompter{}, generator)
		command.SetArgs([]string{"init", "shop", "--module", "example.com/shop", "--common-version", "v1.2.3", "--dry-run"})
		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("ExecuteContext() error = %v", err)
		}
		if generator.initCalls != 1 || generator.applyCalls != 0 {
			t.Fatalf("generator calls = init:%d apply:%d, want init:1 apply:0", generator.initCalls, generator.applyCalls)
		}
		if !strings.Contains(output.String(), "README.md") {
			t.Fatalf("dry run output %q does not list planned file", output.String())
		}
	})
}

func TestCommandSelections(t *testing.T) {
	t.Run("interactive answers and equivalent flags resolve identically", func(t *testing.T) {
		want := InitInput{
			Directory: "shop", Module: "example.com/shop", HTTP: "gin", ORM: "ent", Database: "postgres", CommonVersion: "v1.2.3",
		}
		formGenerator := &recordingGenerator{}
		prompter := &recordingPrompter{project: want}
		formCommand := testCommand(t, Options{Interactive: true, WorkDir: t.TempDir()}, defaultRegistry(), prompter, formGenerator)
		formCommand.SetArgs([]string{"init", "--yes"})
		if err := formCommand.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("interactive ExecuteContext() error = %v", err)
		}
		if len(prompter.projectRegistry.choices(stackHTTP)) != 1 || len(prompter.projectRegistry.choices(stackORM)) != 1 || len(prompter.projectRegistry.choices(stackDatabase)) != 1 {
			t.Fatalf("prompt registry did not show each supported category: %#v", prompter.projectRegistry)
		}

		flagGenerator := &recordingGenerator{}
		flagCommand := testCommand(t, Options{WorkDir: t.TempDir()}, defaultRegistry(), &recordingPrompter{}, flagGenerator)
		flagCommand.SetArgs([]string{
			"init", "shop", "--module", want.Module, "--http", want.HTTP, "--orm", want.ORM, "--db", want.Database,
			"--common-version", want.CommonVersion, "--yes",
		})
		if err := flagCommand.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("flag ExecuteContext() error = %v", err)
		}
		if !reflect.DeepEqual(formGenerator.initInput, flagGenerator.initInput) {
			t.Fatalf("form input %#v differs from flags %#v", formGenerator.initInput, flagGenerator.initInput)
		}
	})

	t.Run("one registry controls choices validation and flag completion", func(t *testing.T) {
		registry := defaultRegistry()
		registry.Stacks = append(registry.Stacks, StackOption{ID: "fiber", Category: stackHTTP, Label: "Fiber", Description: "Fiber HTTP", Default: false})
		registry.Combinations = append(registry.Combinations, StackSelection{HTTP: "fiber", ORM: "ent", Database: "postgres"})
		if err := registry.ValidateStack(StackSelection{HTTP: "fiber", ORM: "ent", Database: "postgres"}); err != nil {
			t.Fatalf("extended registry rejected its new stack: %v", err)
		}

		generator := &recordingGenerator{}
		command := testCommand(t, Options{}, registry, &recordingPrompter{}, generator)
		command.SetArgs([]string{"__complete", "init", "--http", "f"})
		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("completion: %v", err)
		}
		if !strings.Contains(command.OutOrStdout().(*bytes.Buffer).String(), "fiber") {
			t.Fatal("completion did not expose the registered HTTP choice")
		}
	})
}

func TestInteractiveCommand(t *testing.T) {
	t.Run("wizard shows each stack category and writes after review", func(t *testing.T) {
		t.Setenv("TERM", "dumb")
		t.Setenv("NO_COLOR", "1")
		var stdout, stderr bytes.Buffer
		generator := &recordingGenerator{}
		options := Options{
			In:  newPromptInput("shop\nexample.com/shop\nv1.2.3\n1\n1\n1\ny\n"),
			Out: &stdout, Err: &stderr, WorkDir: t.TempDir(), Interactive: true,
		}
		command := newCommand(options, defaultRegistry(), terminalPrompter{input: options.In, output: options.Err}, generator)
		command.SetArgs([]string{"init", "--plain"})
		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("ExecuteContext() error = %v", err)
		}
		want := InitInput{Directory: "shop", Module: "example.com/shop", CommonVersion: "v1.2.3", HTTP: "gin", ORM: "ent", Database: "postgres"}
		if !reflect.DeepEqual(generator.initInput, want) {
			t.Fatalf("wizard input = %#v, want %#v", generator.initInput, want)
		}
		if generator.applyCalls != 1 {
			t.Fatalf("Apply calls = %d, want 1", generator.applyCalls)
		}
		for _, choice := range []string{"HTTP framework", "Gin — HTTP middleware and routing", "ORM", "Ent — Schema-driven Go ORM", "Database", "PostgreSQL"} {
			if !strings.Contains(stderr.String(), choice) {
				t.Errorf("interactive stderr does not show %q", choice)
			}
		}
		if !strings.Contains(stderr.String(), "Step 5 of 5 · Review") {
			t.Errorf("review step missing from prompt output: %s", stderr.String())
		}
		if !strings.Contains(stdout.String(), "Added project") {
			t.Errorf("result was not written to stdout: %s", stdout.String())
		}
		if strings.Contains(stdout.String(), "HTTP framework") {
			t.Error("interactive prompt was written to stdout instead of stderr")
		}
		if strings.Contains(stderr.String(), "\x1b[") {
			t.Error("NO_COLOR output contains ANSI color codes")
		}
	})

	t.Run("flags prefill the wizard and yes skips only review", func(t *testing.T) {
		t.Setenv("TERM", "dumb")
		var stderr bytes.Buffer
		generator := &recordingGenerator{}
		options := Options{
			In: newPromptInput("\n\n\n\n\n1\n"), Out: &bytes.Buffer{}, Err: &stderr,
			WorkDir: t.TempDir(), Interactive: true,
		}
		command := newCommand(options, defaultRegistry(), terminalPrompter{input: options.In, output: options.Err}, generator)
		command.SetArgs([]string{
			"init", "shop", "--module", "example.com/shop", "--http", "gin", "--orm", "ent",
			"--common-version", "v1.2.3", "--yes", "--plain",
		})
		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("ExecuteContext() error = %v", err)
		}
		if generator.initInput.HTTP != "gin" || generator.initInput.ORM != "ent" || generator.initInput.Database != "postgres" {
			t.Fatalf("prefilled stack choices = %#v", generator.initInput)
		}
		if generator.applyCalls != 1 || strings.Contains(stderr.String(), "Step 5 of 5 · Review") {
			t.Fatalf("--yes did not skip just the review: applies=%d output=%s", generator.applyCalls, stderr.String())
		}
	})

	t.Run("resource wizard selects operations and optional schema mixins", func(t *testing.T) {
		t.Setenv("TERM", "dumb")
		var stdout, stderr bytes.Buffer
		generator := &recordingGenerator{}
		options := Options{
			In:  newPromptInput("catalog\n5\n0\ny\nn\nid\n"),
			Out: &stdout, Err: &stderr, WorkDir: t.TempDir(), Interactive: true,
		}
		command := newCommand(options, defaultRegistry(), terminalPrompter{input: options.In, output: options.Err}, generator)
		command.SetArgs([]string{"add", "resource", "product", "--bulk"})
		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("ExecuteContext() error = %v", err)
		}
		want := ResourceInput{
			Name: "product", Domain: "catalog", UpsertOn: upsertTargetID,
			Operations: []string{operationFind, operationGet, operationCreate, operationUpdate, operationUpsert, operationDelete,
				operationGetBulk, operationCreateBulk, operationUpdateBulk, operationUpsertBulk, operationDeleteBulk},
			SoftDelete: true,
		}
		if !reflect.DeepEqual(generator.resourceInput, want) {
			t.Fatalf("resource wizard input = %#v, want %#v", generator.resourceInput, want)
		}
		if generator.applyCalls != 1 || !strings.Contains(stderr.String(), "Schema options") {
			t.Fatalf("resource wizard did not apply selected setup: calls=%d output=%s", generator.applyCalls, stderr.String())
		}
	})

	t.Run("declining review does not apply the project", func(t *testing.T) {
		t.Setenv("TERM", "dumb")
		options := Options{In: newPromptInput("shop\nexample.com/shop\nv1.2.3\n1\n1\n1\nn\n"), Err: &bytes.Buffer{}, Interactive: true}
		generator := &recordingGenerator{}
		command := newCommand(options, defaultRegistry(), terminalPrompter{input: options.In, output: options.Err}, generator)
		command.SetArgs([]string{"init", "--plain"})
		if err := command.ExecuteContext(context.Background()); !errors.Is(err, ErrDeclined) {
			t.Fatalf("ExecuteContext() error = %v, want ErrDeclined", err)
		}
		if generator.initCalls != 1 || generator.applyCalls != 0 {
			t.Fatalf("generator calls after declined review = init:%d apply:%d", generator.initCalls, generator.applyCalls)
		}
	})

	t.Run("canceled context stops before prompting or writing", func(t *testing.T) {
		options := Options{In: strings.NewReader(""), Err: &bytes.Buffer{}, Interactive: true}
		generator := &recordingGenerator{}
		command := newCommand(options, defaultRegistry(), terminalPrompter{input: options.In, output: options.Err}, generator)
		command.SetArgs([]string{"init", "--plain"})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := command.ExecuteContext(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("ExecuteContext() error = %v, want context.Canceled", err)
		}
		if generator.initCalls+generator.applyCalls != 0 {
			t.Fatalf("canceled command invoked generator: %#v", generator)
		}
	})
}

func testCommand(t *testing.T, options Options, registry Registry, prompt prompter, generator generator) *cobra.Command {
	t.Helper()
	var output bytes.Buffer
	if options.Out == nil {
		options.Out = &output
	}
	return newCommand(options, registry, prompt, generator)
}

type promptInput struct {
	remaining []byte
}

func newPromptInput(input string) *promptInput {
	return &promptInput{remaining: []byte(input)}
}

func (input *promptInput) Read(buffer []byte) (int, error) {
	if len(input.remaining) == 0 {
		return 0, io.EOF
	}
	length := len(input.remaining)
	if end := bytes.IndexByte(input.remaining, '\n'); end >= 0 {
		length = end + 1
	}
	if length > len(buffer) {
		length = len(buffer)
	}
	count := copy(buffer, input.remaining[:length])
	input.remaining = input.remaining[count:]
	return count, nil
}

type recordingGenerator struct {
	initInput     InitInput
	initCalls     int
	domainRoot    string
	domainInput   DomainInput
	domainCalls   int
	resourceRoot  string
	resourceInput ResourceInput
	resourceCalls int
	applyCalls    int
	plan          Plan
	err           error
}

func (g *recordingGenerator) Init(_ context.Context, input InitInput) (Plan, error) {
	g.initInput = input
	g.initCalls++
	return g.nextPlan(input.Directory)
}

func (g *recordingGenerator) Domain(_ context.Context, root string, input DomainInput) (Plan, error) {
	g.domainRoot, g.domainInput = root, input
	g.domainCalls++
	return g.nextPlan(root)
}

func (g *recordingGenerator) Resource(_ context.Context, root string, input ResourceInput) (Plan, error) {
	g.resourceRoot, g.resourceInput = root, input
	g.resourceCalls++
	return g.nextPlan(root)
}

func (g *recordingGenerator) Apply(_ context.Context, plan Plan) (Result, error) {
	g.applyCalls++
	if g.err != nil {
		return Result{}, g.err
	}
	return Result{Root: plan.Root}, nil
}

func (g *recordingGenerator) nextPlan(root string) (Plan, error) {
	if g.err != nil {
		return Plan{}, g.err
	}
	if g.plan.Root != "" {
		return g.plan, nil
	}
	return Plan{Root: root}, nil
}

func (g *recordingGenerator) reset() { *g = recordingGenerator{} }

type recordingPrompter struct {
	project         InitInput
	resource        ResourceInput
	confirmed       bool
	projectCalls    int
	resourceCalls   int
	confirmCalls    int
	projectSeed     InitInput
	resourceSeed    ResourceInput
	projectRegistry Registry
}

func (p *recordingPrompter) Project(_ context.Context, seed InitInput, registry Registry) (InitInput, error) {
	p.projectCalls++
	p.projectSeed = seed
	p.projectRegistry = registry
	return p.project, nil
}

func (p *recordingPrompter) Resource(_ context.Context, seed ResourceInput, _ Registry) (ResourceInput, error) {
	p.resourceCalls++
	p.resourceSeed = seed
	return p.resource, nil
}

func (p *recordingPrompter) Confirm(context.Context, Plan) (bool, error) {
	p.confirmCalls++
	return p.confirmed, nil
}
