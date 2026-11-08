package kit

import (
	"fmt"
	"slices"
)

type StackOption struct {
	ID          string
	Category    string
	Label       string
	Description string
	Default     bool
}

type StackSelection struct {
	HTTP     string
	ORM      string
	Database string
}

type OperationOption struct {
	ID          string
	Label       string
	Description string
	Default     bool
	Bulk        bool
}

// Registry is the single source of supported stack and operation choices.
type Registry struct {
	Stacks       []StackOption
	Combinations []StackSelection
	Operations   []OperationOption
}

func defaultRegistry() Registry {
	return Registry{
		Stacks: []StackOption{
			{ID: stackGin, Category: stackHTTP, Label: "Gin", Description: "HTTP middleware and routing", Default: true},
			{ID: stackEnt, Category: stackORM, Label: "Ent", Description: "Schema-driven Go ORM", Default: true},
			{ID: stackPostgres, Category: stackDatabase, Label: "PostgreSQL", Description: "Relational database", Default: true},
		},
		Combinations: []StackSelection{{HTTP: stackGin, ORM: stackEnt, Database: stackPostgres}},
		Operations: []OperationOption{
			{ID: operationFind, Label: "Find", Description: "Find a paginated collection", Default: true},
			{ID: operationGet, Label: "Get", Description: "Get one record", Default: true, Bulk: true},
			{ID: operationCreate, Label: "Create", Description: "Create a record", Default: true, Bulk: true},
			{ID: operationUpdate, Label: "Update", Description: "Update a record", Default: true, Bulk: true},
			{ID: operationUpsert, Label: "Upsert", Description: "Create or update by ID", Bulk: true},
			{ID: operationDelete, Label: "Delete", Description: "Delete a record", Default: true, Bulk: true},
			{ID: operationGetBulk, Label: "GetBulk", Description: "Get records by IDs"},
			{ID: operationCreateBulk, Label: "CreateBulk", Description: "Create records in one transaction"},
			{ID: operationUpdateBulk, Label: "UpdateBulk", Description: "Update records in one transaction"},
			{ID: operationUpsertBulk, Label: "UpsertBulk", Description: "Upsert records in one transaction"},
			{ID: operationDeleteBulk, Label: "DeleteBulk", Description: "Delete records in one transaction"},
		},
	}
}

func (r Registry) choices(category string) []StackOption {
	choices := make([]StackOption, 0, len(r.Stacks))
	for _, choice := range r.Stacks {
		if choice.Category == category {
			choices = append(choices, choice)
		}
	}
	return choices
}

func (r Registry) defaultChoice(category string) (string, bool) {
	for _, choice := range r.Stacks {
		if choice.Category == category && choice.Default {
			return choice.ID, true
		}
	}
	return "", false
}

func (r Registry) hasChoice(category, id string) bool {
	for _, choice := range r.Stacks {
		if choice.Category == category && choice.ID == id {
			return true
		}
	}
	return false
}

func (r Registry) ValidateStack(selection StackSelection) error {
	if !r.hasChoice(stackHTTP, selection.HTTP) || !r.hasChoice(stackORM, selection.ORM) || !r.hasChoice(stackDatabase, selection.Database) {
		return fmt.Errorf("%w: HTTP %q, ORM %q, database %q", ErrInvalidSelection, selection.HTTP, selection.ORM, selection.Database)
	}
	if !slices.Contains(r.Combinations, selection) {
		return fmt.Errorf("%w: HTTP %q, ORM %q, database %q", ErrInvalidSelection, selection.HTTP, selection.ORM, selection.Database)
	}
	return nil
}

func (r Registry) hasOperation(id string) bool {
	for _, operation := range r.Operations {
		if operation.ID == id {
			return true
		}
	}
	return false
}

func (r Registry) defaultOperations() []string {
	operations := make([]string, 0, len(r.Operations))
	for _, operation := range r.Operations {
		if operation.Default {
			operations = append(operations, operation.ID)
		}
	}
	return operations
}

func (r Registry) bulkID(operation string) (string, bool) {
	for _, base := range r.Operations {
		if base.ID == operation && base.Bulk {
			bulkID := operation + "-bulk"
			return bulkID, r.hasOperation(bulkID)
		}
	}
	return "", false
}
