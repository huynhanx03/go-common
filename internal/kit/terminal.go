package kit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"golang.org/x/mod/module"
)

type terminalPrompter struct {
	input  io.Reader
	output io.Writer
}

func (prompter terminalPrompter) Project(ctx context.Context, seed InitInput, registry Registry) (InitInput, error) {
	if err := ctx.Err(); err != nil {
		return InitInput{}, err
	}
	input := seed
	var httpChoice, ormChoice, databaseChoice string
	projectGroup := huh.NewGroup(
		wizardNote("Step 1 of 4 · Project", "Set the project location, Go module, and go-common version."),
		huh.NewInput().Title("Project directory").Value(&input.Directory).Validate(func(value string) error {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("%w: project directory is required", ErrInvalidInput)
			}
			return nil
		}),
		huh.NewInput().Title("Go module path").Value(&input.Module).Validate(validateModulePath),
		huh.NewInput().Title("go-common version").Value(&input.CommonVersion).Validate(validateCommonVersionInput),
	).Title("Project")
	httpGroup := huh.NewGroup(
		wizardNote("Step 2 of 4 · HTTP", "Choose the HTTP framework for this project."),
		newStackSelect("HTTP framework", stackHTTP, &httpChoice, seed.HTTP, registry),
	).Title("HTTP")
	ormGroup := huh.NewGroup(
		wizardNote("Step 3 of 4 · ORM", "Choose the persistence layer for this project."),
		newStackSelect("ORM", stackORM, &ormChoice, seed.ORM, registry),
	).Title("ORM")
	databaseGroup := huh.NewGroup(
		wizardNote("Step 4 of 4 · Database", "Choose where application data will be stored."),
		newStackSelect("Database", stackDatabase, &databaseChoice, seed.Database, registry),
	).Title("Database")

	if err := prompter.run(ctx, wizardAccessible(ctx), projectGroup, httpGroup, ormGroup, databaseGroup); err != nil {
		return InitInput{}, err
	}
	input.HTTP, input.ORM, input.Database = httpChoice, ormChoice, databaseChoice
	return input, nil
}

func (prompter terminalPrompter) Resource(ctx context.Context, seed ResourceInput, registry Registry) (ResourceInput, error) {
	if err := ctx.Err(); err != nil {
		return ResourceInput{}, err
	}
	input := seed
	if len(input.Operations) == 0 {
		input.Operations = registry.defaultOperations()
	}
	operations := append([]string(nil), input.Operations...)
	fields := []huh.Field{
		wizardNote("Step 1 of 3 · Domain", "Place this resource under an existing domain."),
		huh.NewInput().Title("Domain").Value(&input.Domain).Validate(func(value string) error {
			if strings.TrimSpace(value) == "" {
				return ErrMissingDomain
			}
			return nil
		}),
	}
	operationGroup := huh.NewGroup(
		wizardNote("Step 2 of 3 · Operations", "Choose the resource operations to generate. Select 0 when done."),
		newOperationSelect(&operations, registry),
	).Title("Operations")
	softDeleteGroup := huh.NewGroup(
		wizardNote("Step 3 of 3 · Schema options", "Add shared soft-delete and actor audit fields when needed."),
		huh.NewConfirm().Title("Include soft delete?").Value(&input.SoftDelete),
		huh.NewConfirm().Title("Include actor audit fields?").Value(&input.Modifier),
	).Title("Schema options")
	groups := []*huh.Group{
		huh.NewGroup(fields...).Title("Domain"),
		operationGroup,
		softDeleteGroup,
	}
	if containsOperation(operations, operationUpsert, operationUpsertBulk) {
		groups = append(groups, huh.NewGroup(
			wizardNote("Upsert target", "This version supports an explicit UUID ID conflict target."),
			huh.NewInput().Title("Upsert conflict target").Value(&input.UpsertOn).Validate(func(value string) error {
				if value != upsertTargetID {
					return fmt.Errorf("%w: upsert currently supports %q", ErrInvalidInput, upsertTargetID)
				}
				return nil
			}),
		).Title("Upsert"))
		if input.UpsertOn == "" {
			input.UpsertOn = upsertTargetID
		}
	}
	if err := prompter.run(ctx, wizardAccessible(ctx), groups...); err != nil {
		return ResourceInput{}, err
	}
	input.Operations = operations
	return input, nil
}

func (prompter terminalPrompter) Confirm(ctx context.Context, plan Plan) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	confirmed := false
	description := fmt.Sprintf("Create project at %s with %d generated files.", plan.Root, len(plan.Files))
	if metadata := plan.Metadata; metadata != nil {
		description = fmt.Sprintf("%s\n%s · %s · %s", description, metadata.HTTP, metadata.ORM, metadata.Database)
	}
	group := huh.NewGroup(
		wizardNote("Step 5 of 5 · Review", description),
		huh.NewConfirm().Title("Create project?").Value(&confirmed),
	).Title("Review")
	if err := prompter.run(ctx, wizardAccessible(ctx), group); err != nil {
		return false, err
	}
	return confirmed, nil
}

func (prompter terminalPrompter) run(ctx context.Context, accessible bool, groups ...*huh.Group) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	form := huh.NewForm(groups...).WithInput(prompter.input).WithOutput(prompter.output)
	if accessible {
		form.WithAccessible(true)
	}
	if os.Getenv("NO_COLOR") != "" {
		form.WithTheme(colorlessTheme())
	} else {
		form.WithTheme(comxkitTheme())
	}
	if err := form.RunWithContext(ctx); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return ErrDeclined
		}
		return err
	}
	return nil
}

func comxkitTheme() huh.Theme {
	return huh.ThemeFunc(func(isDark bool) *huh.Styles {
		styles := huh.ThemeBase(isDark)
		accent := lipgloss.LightDark(isDark)(lipgloss.Color("#5A56E0"), lipgloss.Color("#7571F9"))
		styles.Focused.Title = styles.Focused.Title.Foreground(accent).Bold(true)
		styles.Focused.NoteTitle = styles.Focused.NoteTitle.Foreground(accent).Bold(true)
		styles.Focused.Directory = styles.Focused.Directory.Foreground(accent)
		styles.Focused.SelectSelector = styles.Focused.SelectSelector.Foreground(accent)
		styles.Focused.NextIndicator = styles.Focused.NextIndicator.Foreground(accent)
		styles.Focused.PrevIndicator = styles.Focused.PrevIndicator.Foreground(accent)
		styles.Focused.MultiSelectSelector = styles.Focused.MultiSelectSelector.Foreground(accent)
		styles.Focused.TextInput.Prompt = styles.Focused.TextInput.Prompt.Foreground(accent)
		styles.Focused.TextInput.Cursor = styles.Focused.TextInput.Cursor.Foreground(accent)
		styles.Group.Title = styles.Focused.Title
		return styles
	})
}

func colorlessTheme() huh.Theme {
	styles := huh.ThemeBase(false)
	noColor := lipgloss.NoColor{}
	styles.Focused.FocusedButton = styles.Focused.FocusedButton.Foreground(noColor).Background(noColor)
	styles.Focused.BlurredButton = styles.Focused.BlurredButton.Foreground(noColor).Background(noColor)
	styles.Focused.TextInput.Placeholder = styles.Focused.TextInput.Placeholder.Foreground(noColor)
	styles.Blurred.FocusedButton = styles.Blurred.FocusedButton.Foreground(noColor).Background(noColor)
	styles.Blurred.BlurredButton = styles.Blurred.BlurredButton.Foreground(noColor).Background(noColor)
	styles.Blurred.TextInput.Placeholder = styles.Blurred.TextInput.Placeholder.Foreground(noColor)
	return huh.ThemeFunc(func(bool) *huh.Styles { return styles })
}

func wizardNote(title, description string) *huh.Note {
	return huh.NewNote().Title("comxkit · " + title).Description(description)
}

func newStackSelect(title, category string, value *string, current string, registry Registry) *huh.Select[string] {
	choices := registry.choices(category)
	options := make([]huh.Option[string], 0, len(choices))
	for _, choice := range choices {
		options = append(options, huh.NewOption(choice.Label+" — "+choice.Description, choice.ID))
		if choice.ID == current {
			*value = current
		}
	}
	return huh.NewSelect[string]().Title(title).Options(options...).Value(value)
}

func newOperationSelect(value *[]string, registry Registry) *huh.MultiSelect[string] {
	options := make([]huh.Option[string], 0, len(registry.Operations))
	for _, operation := range registry.Operations {
		options = append(options,
			huh.NewOption(operation.Label+" — "+operation.Description, operation.ID).Selected(slices.Contains(*value, operation.ID)),
		)
	}
	return huh.NewMultiSelect[string]().Title("Operations").Options(options...).Value(value).Validate(func(selected []string) error {
		if len(selected) == 0 {
			return fmt.Errorf("%w: select at least one operation", ErrInvalidInput)
		}
		return nil
	})
}

func validateModulePath(value string) error {
	if value == "" {
		return ErrMissingModule
	}
	if err := module.CheckPath(value); err != nil {
		return fmt.Errorf("%w: %q", ErrInvalidModule, value)
	}
	return nil
}

func validateCommonVersionInput(value string) error {
	if value == "" {
		return ErrMissingCommonVersion
	}
	if !validCommonVersion(value) {
		return fmt.Errorf("%w: %q", ErrInvalidCommonVersion, value)
	}
	return nil
}

func wizardAccessible(ctx context.Context) bool {
	mode, _ := ctx.Value(promptModeKey{}).(promptMode)
	return mode.plain || os.Getenv("TERM") == "dumb"
}
