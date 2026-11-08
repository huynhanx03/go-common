package kit

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/mod/module"
)

type commandDependencies struct {
	options  Options
	registry Registry
	prompt   prompter
	generate generator
}

// NewCommand builds a comxkit command tree with the provided process options.
func NewCommand(options Options) *cobra.Command {
	if options.In == nil {
		options.In = os.Stdin
	}
	if options.Out == nil {
		options.Out = os.Stdout
	}
	if options.Err == nil {
		options.Err = os.Stderr
	}
	registry := defaultRegistry()
	return newCommand(options, registry, terminalPrompter{input: options.In, output: options.Err}, newGenerator(registry))
}

func newCommand(options Options, registry Registry, prompt prompter, generate generator) *cobra.Command {
	if options.In == nil {
		options.In = os.Stdin
	}
	if options.Out == nil {
		options.Out = os.Stdout
	}
	if options.Err == nil {
		options.Err = os.Stderr
	}
	if options.WorkDir == "" {
		options.WorkDir = defaultProjectDir
	}
	if options.Version == "" {
		options.Version = "dev"
	}

	deps := commandDependencies{options: options, registry: registry, prompt: prompt, generate: generate}
	root := &cobra.Command{
		Use:           commandName,
		Short:         "Create and extend Go services built on go-common",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       options.Version,
	}
	root.SetIn(options.In)
	root.SetOut(options.Out)
	root.SetErr(options.Err)
	root.AddCommand(newInitCommand(deps), newAddCommand(deps), newVersionCommand(options.Version))
	return root
}

func newInitCommand(deps commandDependencies) *cobra.Command {
	var http, orm, database, modulePath, commonVersion string
	var yes, plain, dryRun bool
	command := &cobra.Command{
		Use:   commandInit + " [directory]",
		Short: "Create a Go service project",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			promptContext := command.Context()
			if plain {
				promptContext = context.WithValue(promptContext, promptModeKey{}, promptMode{plain: true})
			}
			input := InitInput{Module: modulePath, HTTP: http, ORM: orm, Database: database, CommonVersion: commonVersion}
			if len(args) == 1 {
				input.Directory = args[0]
			}
			if !command.Flags().Changed(flagHTTP) {
				input.HTTP = defaultForPrompt(deps, stackHTTP)
			}
			if !command.Flags().Changed(flagORM) {
				input.ORM = defaultForPrompt(deps, stackORM)
			}
			if !command.Flags().Changed(flagDatabase) {
				input.Database = defaultForPrompt(deps, stackDatabase)
			}
			if input.CommonVersion == "" {
				input.CommonVersion = deps.options.CommonVersion
			}

			if deps.options.Interactive && missingProjectValues(input) {
				selected, err := deps.prompt.Project(promptContext, input, deps.registry)
				if err != nil {
					return fmt.Errorf("collect project options: %w", err)
				}
				input = selected
				if command.Flags().Changed(flagModule) {
					input.Module = modulePath
				}
				if command.Flags().Changed(flagHTTP) {
					input.HTTP = http
				}
				if command.Flags().Changed(flagORM) {
					input.ORM = orm
				}
				if command.Flags().Changed(flagDatabase) {
					input.Database = database
				}
				if command.Flags().Changed(flagCommonVersion) {
					input.CommonVersion = commonVersion
				}
			}

			if err := normalizeProjectInput(&input, deps.options, deps.registry); err != nil {
				return err
			}
			plan, err := deps.generate.Init(command.Context(), input)
			if err != nil {
				return fmt.Errorf("plan project: %w", err)
			}
			if dryRun {
				return writePlan(command.OutOrStdout(), plan)
			}
			if deps.options.Interactive && !yes {
				confirmed, err := deps.prompt.Confirm(promptContext, plan)
				if err != nil {
					return fmt.Errorf("confirm project plan: %w", err)
				}
				if !confirmed {
					return ErrDeclined
				}
			}
			result, err := deps.generate.Apply(command.Context(), plan)
			if err != nil {
				return fmt.Errorf("create project: %w", err)
			}
			return writeResult(command.OutOrStdout(), "project", result)
		},
	}
	command.Flags().StringVar(&http, flagHTTP, "", "HTTP framework")
	command.Flags().StringVar(&orm, flagORM, "", "ORM")
	command.Flags().StringVar(&database, flagDatabase, "", "database")
	command.Flags().StringVar(&modulePath, flagModule, "", "Go module path")
	command.Flags().StringVar(&commonVersion, flagCommonVersion, "", "go-common module version")
	command.Flags().BoolVar(&yes, flagYes, false, "skip final confirmation after validation")
	command.Flags().BoolVar(&plain, flagPlain, false, "use sequential accessible prompts")
	command.Flags().BoolVar(&dryRun, flagDryRun, false, "show planned files without writing")
	registerStackCompletions(command, deps.registry)
	return command
}

func newAddCommand(deps commandDependencies) *cobra.Command {
	command := &cobra.Command{Use: commandAdd, Short: "Add a domain or resource to a project"}
	command.AddCommand(newDomainCommand(deps), newResourceCommand(deps))
	return command
}

func newDomainCommand(deps commandDependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "domain <name>",
		Short: "Add a domain boundary",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			input := DomainInput{Name: args[0]}
			plan, err := deps.generate.Domain(command.Context(), deps.options.WorkDir, input)
			if err != nil {
				return fmt.Errorf("plan domain: %w", err)
			}
			result, err := deps.generate.Apply(command.Context(), plan)
			if err != nil {
				return fmt.Errorf("add domain: %w", err)
			}
			return writeResult(command.OutOrStdout(), "domain", result)
		},
	}
}

func newResourceCommand(deps commandDependencies) *cobra.Command {
	var domain, route, operations, upsertOn string
	var bulk, softDelete, modifier bool
	command := &cobra.Command{
		Use:   "resource <name>",
		Short: "Add a CRUD resource to a domain",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			input := ResourceInput{Name: args[0], Domain: domain, Route: route, UpsertOn: upsertOn, SoftDelete: softDelete, Modifier: modifier}
			if command.Flags().Changed(flagOperations) {
				input.Operations = splitSelections(operations)
			}
			if deps.options.Interactive && (input.Domain == "" || (!command.Flags().Changed(flagOperations) && !command.Flags().Changed(flagBulk))) {
				selected, err := deps.prompt.Resource(command.Context(), input, deps.registry)
				if err != nil {
					return fmt.Errorf("collect resource options: %w", err)
				}
				input = selected
				if command.Flags().Changed(flagDomain) {
					input.Domain = domain
				}
				if command.Flags().Changed(flagRoute) {
					input.Route = route
				}
				if command.Flags().Changed(flagUpsertOn) {
					input.UpsertOn = upsertOn
				}
				if command.Flags().Changed(flagOperations) {
					input.Operations = splitSelections(operations)
				}
				if command.Flags().Changed(flagBulk) && bulk {
					input.Operations = addBulkOperations(input.Operations, deps.registry)
				}
				if command.Flags().Changed(flagSoftDelete) {
					input.SoftDelete = softDelete
				}
				if command.Flags().Changed(flagModifier) {
					input.Modifier = modifier
				}
			} else if command.Flags().Changed(flagBulk) && bulk {
				input.Operations = addBulkOperations(input.Operations, deps.registry)
			}
			if input.Domain == "" {
				return ErrMissingDomain
			}
			if len(input.Operations) == 0 {
				input.Operations = deps.registry.defaultOperations()
			}
			if err := validateOperations(input.Operations, deps.registry); err != nil {
				return err
			}
			if containsOperation(input.Operations, operationUpsert, operationUpsertBulk) {
				if input.UpsertOn == "" {
					input.UpsertOn = upsertTargetID
				}
				if input.UpsertOn != upsertTargetID {
					return fmt.Errorf("%w: upsert currently supports %q", ErrInvalidInput, upsertTargetID)
				}
			}
			plan, err := deps.generate.Resource(command.Context(), deps.options.WorkDir, input)
			if err != nil {
				return fmt.Errorf("plan resource: %w", err)
			}
			result, err := deps.generate.Apply(command.Context(), plan)
			if err != nil {
				return fmt.Errorf("add resource: %w", err)
			}
			return writeResult(command.OutOrStdout(), "resource", result)
		},
	}
	command.Flags().StringVar(&domain, flagDomain, "", "domain to contain this resource")
	command.Flags().StringVar(&route, flagRoute, "", "override the generated route name")
	command.Flags().StringVar(&operations, flagOperations, "", "comma-separated operations")
	command.Flags().BoolVar(&bulk, flagBulk, false, "add bulk variants of selected operations")
	command.Flags().BoolVar(&softDelete, flagSoftDelete, false, "include soft-delete support")
	command.Flags().BoolVar(&modifier, flagModifier, false, "include actor audit fields")
	command.Flags().StringVar(&upsertOn, flagUpsertOn, "", "upsert conflict target (id)")
	registerOperationCompletion(command, deps.registry)
	return command
}

func newVersionCommand(version string) *cobra.Command {
	return &cobra.Command{
		Use:   commandVersion,
		Short: "Print the comxkit version",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(command.OutOrStdout(), "%s %s\n", commandName, version)
			return err
		},
	}
}

func defaultForPrompt(deps commandDependencies, category string) string {
	if deps.options.Interactive {
		return ""
	}
	value, _ := deps.registry.defaultChoice(category)
	return value
}

func missingProjectValues(input InitInput) bool {
	return input.Directory == "" || input.Module == "" || input.HTTP == "" || input.ORM == "" || input.Database == ""
}

func normalizeProjectInput(input *InitInput, options Options, registry Registry) error {
	if input.Module == "" {
		return ErrMissingModule
	}
	if err := module.CheckPath(input.Module); err != nil {
		return fmt.Errorf("%w: %q", ErrInvalidModule, input.Module)
	}
	if input.Directory == "" {
		input.Directory = filepath.Base(strings.TrimSuffix(input.Module, "/"))
	}
	if input.HTTP == "" {
		input.HTTP, _ = registry.defaultChoice(stackHTTP)
	}
	if input.ORM == "" {
		input.ORM, _ = registry.defaultChoice(stackORM)
	}
	if input.Database == "" {
		input.Database, _ = registry.defaultChoice(stackDatabase)
	}
	if err := registry.ValidateStack(StackSelection{HTTP: input.HTTP, ORM: input.ORM, Database: input.Database}); err != nil {
		return err
	}
	if input.CommonVersion == "" {
		return ErrMissingCommonVersion
	}
	if !validCommonVersion(input.CommonVersion) {
		return fmt.Errorf("%w: %q", ErrInvalidCommonVersion, input.CommonVersion)
	}
	if input.Directory == "" {
		return fmt.Errorf("%w: project directory is required", ErrInvalidInput)
	}
	input.Directory = filepath.Clean(input.Directory)
	if options.WorkDir == "" {
		return ErrInvalidInput
	}
	return nil
}

var commonVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

func validCommonVersion(version string) bool { return commonVersionPattern.MatchString(version) }

func splitSelections(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
	}
	return parts
}

func addBulkOperations(operations []string, registry Registry) []string {
	result := append([]string(nil), operations...)
	for _, operation := range operations {
		bulkID := operation + "-bulk"
		if registeredBulkID, ok := registry.bulkID(operation); ok && registeredBulkID == bulkID && !containsOperation(result, bulkID) {
			result = append(result, bulkID)
		}
	}
	return result
}

func validateOperations(operations []string, registry Registry) error {
	seen := make(map[string]struct{}, len(operations))
	for _, operation := range operations {
		if !registry.hasOperation(operation) {
			return fmt.Errorf("%w: %q", ErrInvalidOperation, operation)
		}
		if _, exists := seen[operation]; exists {
			return fmt.Errorf("%w: duplicate %q", ErrInvalidOperation, operation)
		}
		seen[operation] = struct{}{}
	}
	return nil
}

func containsOperation(operations []string, ids ...string) bool {
	for _, operation := range operations {
		for _, id := range ids {
			if operation == id {
				return true
			}
		}
	}
	return false
}

func registerStackCompletions(command *cobra.Command, registry Registry) {
	for _, category := range []string{stackHTTP, stackORM, stackDatabase} {
		flag := category
		if category == stackDatabase {
			flag = flagDatabase
		}
		_ = command.RegisterFlagCompletionFunc(flag, func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			return completionChoices(registry.choices(category)), cobra.ShellCompDirectiveNoFileComp
		})
	}
}

func registerOperationCompletion(command *cobra.Command, registry Registry) {
	_ = command.RegisterFlagCompletionFunc(flagOperations, func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		choices := make([]string, 0, len(registry.Operations))
		for _, operation := range registry.Operations {
			choices = append(choices, operation.ID+"\t"+operation.Description)
		}
		return choices, cobra.ShellCompDirectiveNoFileComp
	})
}

func completionChoices(choices []StackOption) []string {
	result := make([]string, 0, len(choices))
	for _, choice := range choices {
		result = append(result, choice.ID+"\t"+choice.Description)
	}
	return result
}

func writePlan(writer io.Writer, plan Plan) error {
	if _, err := fmt.Fprintf(writer, "Planned changes in %s:\n", plan.Root); err != nil {
		return err
	}
	if len(plan.Files) == 0 {
		_, err := fmt.Fprintln(writer, "  no files")
		return err
	}
	for _, file := range plan.Files {
		if _, err := fmt.Fprintf(writer, "  %s\n", file.Path); err != nil {
			return err
		}
	}
	return nil
}

func writeResult(writer io.Writer, kind string, result Result) error {
	if result.NoOp {
		_, err := fmt.Fprintf(writer, "%s is already up to date in %s\n", kind, result.Root)
		return err
	}
	_, err := fmt.Fprintf(writer, "Added %s in %s\n", kind, result.Root)
	return err
}

type promptModeKey struct{}
type promptMode struct{ plain bool }
