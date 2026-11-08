package kit

import (
	"context"
	"fmt"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	resourceNamePattern  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	resourceRoutePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
)

type resourceTemplateData struct {
	Module      string
	Domain      string
	Name        string
	TypeName    string
	PackageName string
	Route       string
	Find        bool
	Get         bool
	Create      bool
	Update      bool
	Upsert      bool
	Delete      bool
	GetBulk     bool
	CreateBulk  bool
	UpdateBulk  bool
	UpsertBulk  bool
	DeleteBulk  bool
	SoftDelete  bool
	Modifier    bool
	FindFields  []string
}

func (g *Generator) Resource(ctx context.Context, root string, input ResourceInput) (Plan, error) {
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	projectRoot, metadata, metadataHash, err := readProjectMetadata(root)
	if err != nil {
		return Plan{}, err
	}
	if input.Domain == "" {
		return Plan{}, ErrMissingDomain
	}
	if !validDomainName(input.Domain) {
		return Plan{}, fmt.Errorf("%w: %q", ErrInvalidDomain, input.Domain)
	}
	if !validResourceName(input.Name) {
		return Plan{}, fmt.Errorf("%w: %q", ErrInvalidResource, input.Name)
	}
	if !contains(metadata.Domains, input.Domain) {
		return Plan{}, fmt.Errorf("%w: %q", ErrDomainNotFound, input.Domain)
	}
	previousHashes := metadata.Files
	input, operations, err := normalizeResourceInput(input, g.registry)
	if err != nil {
		return Plan{}, err
	}
	for _, existing := range metadata.Resources {
		if existing.Domain == input.Domain && existing.Name == input.Name {
			if !sameResourceOptions(existing, input, operations) {
				return Plan{}, fmt.Errorf("%w: %s.%s", ErrResourceConflict, input.Domain, input.Name)
			}
			files, err := resourceAssemblyFiles(metadata, input.Domain)
			if err != nil {
				return Plan{}, err
			}
			return Plan{Root: projectRoot, Kind: commandResource, Files: files, ExpectedMetadataHash: metadataHash, NoOp: true}, nil
		}
		if strings.EqualFold(resourceTypeName(existing.Name), resourceTypeName(input.Name)) {
			return Plan{}, fmt.Errorf("%w: %s", ErrDuplicateSchema, resourceTypeName(input.Name))
		}
		if existing.Domain == input.Domain && existing.Route == input.Route {
			return Plan{}, fmt.Errorf("%w: %s/%s", ErrDuplicateRoute, input.Domain, input.Route)
		}
	}
	metadata = cloneMetadata(metadata)
	metadata.Resources = append(metadata.Resources, resourceRecord{
		Domain: input.Domain, Name: input.Name, Route: input.Route,
		Operations: operations, SoftDelete: input.SoftDelete, Modifier: input.Modifier,
	})
	sort.Slice(metadata.Resources, func(left, right int) bool {
		if metadata.Resources[left].Domain == metadata.Resources[right].Domain {
			return metadata.Resources[left].Name < metadata.Resources[right].Name
		}
		return metadata.Resources[left].Domain < metadata.Resources[right].Domain
	})

	files, err := renderResourceFiles(metadata.Module, input, operations)
	if err != nil {
		return Plan{}, err
	}
	assemblies, err := resourceAssemblyFiles(metadata, input.Domain)
	if err != nil {
		return Plan{}, err
	}
	for index := range assemblies {
		assemblies[index].ExpectedHash = previousHashes[assemblies[index].Path]
	}
	files = append(files, assemblies...)
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	return Plan{Root: projectRoot, Kind: commandResource, Files: files, Metadata: &metadata, ExpectedMetadataHash: metadataHash}, nil
}

func normalizeResourceInput(input ResourceInput, registry Registry) (ResourceInput, []string, error) {
	if input.Route == "" {
		input.Route = resourceRoute(input.Name)
	}
	if !resourceRoutePattern.MatchString(input.Route) {
		return ResourceInput{}, nil, fmt.Errorf("%w: route %q", ErrInvalidResource, input.Route)
	}
	operations := input.Operations
	if len(operations) == 0 {
		operations = registry.defaultOperations()
	}
	if err := validateOperations(operations, registry); err != nil {
		return ResourceInput{}, nil, err
	}
	selected := make(map[string]struct{}, len(operations))
	for _, operation := range operations {
		selected[operation] = struct{}{}
	}
	canonical := make([]string, 0, len(selected))
	for _, option := range registry.Operations {
		if _, ok := selected[option.ID]; ok {
			canonical = append(canonical, option.ID)
		}
	}
	if containsOperation(canonical, operationUpsert, operationUpsertBulk) {
		if input.UpsertOn == "" {
			input.UpsertOn = upsertTargetID
		}
		if input.UpsertOn != upsertTargetID {
			return ResourceInput{}, nil, fmt.Errorf("%w: upsert currently supports %q", ErrInvalidInput, upsertTargetID)
		}
	} else if input.UpsertOn != "" {
		return ResourceInput{}, nil, fmt.Errorf("%w: upsert target requires the upsert operation", ErrInvalidInput)
	}
	input.Operations = canonical
	return input, canonical, nil
}

func validResourceName(name string) bool {
	return resourceNamePattern.MatchString(name) && !token.IsKeyword(name)
}

func resourceTypeName(name string) string {
	parts := strings.Split(name, "_")
	for index, part := range parts {
		if part == "" {
			continue
		}
		parts[index] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, "")
}

func resourceRoute(name string) string {
	route := strings.ReplaceAll(name, "_", "-")
	last := route
	if separator := strings.LastIndexByte(route, '-'); separator >= 0 {
		last = route[separator+1:]
	}
	suffix := "s"
	if strings.HasSuffix(last, "y") && len(last) > 1 && !strings.ContainsRune("aeiou", rune(last[len(last)-2])) {
		suffix = "ies"
	} else if strings.HasSuffix(last, "s") || strings.HasSuffix(last, "x") || strings.HasSuffix(last, "ch") || strings.HasSuffix(last, "sh") {
		suffix = "es"
	}
	return route + suffix
}

func sameResourceOptions(existing resourceRecord, input ResourceInput, operations []string) bool {
	return existing.Domain == input.Domain && existing.Name == input.Name && existing.Route == input.Route &&
		existing.SoftDelete == input.SoftDelete && existing.Modifier == input.Modifier &&
		sameStrings(existing.Operations, operations)
}

func renderResourceFiles(module string, input ResourceInput, operations []string) ([]PlannedFile, error) {
	selected := make(map[string]bool, len(operations))
	for _, operation := range operations {
		selected[operation] = true
	}
	data := resourceTemplateData{
		Module: module, Domain: input.Domain, Name: input.Name, TypeName: resourceTypeName(input.Name),
		PackageName: strings.ToLower(resourceTypeName(input.Name)), Route: input.Route,
		Find: selected[operationFind], Get: selected[operationGet], Create: selected[operationCreate],
		Update: selected[operationUpdate], Upsert: selected[operationUpsert], Delete: selected[operationDelete],
		GetBulk: selected[operationGetBulk], CreateBulk: selected[operationCreateBulk],
		UpdateBulk: selected[operationUpdateBulk], UpsertBulk: selected[operationUpsertBulk],
		DeleteBulk: selected[operationDeleteBulk],
		SoftDelete: input.SoftDelete, Modifier: input.Modifier, FindFields: resourceFindFields(input),
	}
	templates := []struct {
		path string
		name string
	}{
		{path: filepath.ToSlash(filepath.Join("internal", input.Domain, input.Name, "model.go")), name: "model.go.tmpl"},
		{path: filepath.ToSlash(filepath.Join("internal", input.Domain, input.Name, "request.go")), name: "request.go.tmpl"},
		{path: filepath.ToSlash(filepath.Join("internal", input.Domain, input.Name, "service.go")), name: "service.go.tmpl"},
		{path: filepath.ToSlash(filepath.Join("internal", input.Domain, input.Name, "repository.go")), name: "repository.go.tmpl"},
		{path: filepath.ToSlash(filepath.Join("internal", input.Domain, input.Name, "http.go")), name: "http.go.tmpl"},
		{path: filepath.ToSlash(filepath.Join("ent/schema", input.Name+".go")), name: "schema.go.tmpl"},
	}
	files := make([]PlannedFile, 0, len(templates))
	for _, item := range templates {
		content, err := renderTemplate(filepath.ToSlash(filepath.Join("templates/resource", item.name)), data)
		if err != nil {
			return nil, err
		}
		files = append(files, PlannedFile{Path: item.path, Content: content})
	}
	return files, nil
}

func resourceFindFields(input ResourceInput) []string {
	fields := []string{"created_at", "id", "updated_at"}
	if input.SoftDelete {
		fields = append(fields, "deleted_at", "deleted_by")
	}
	if input.Modifier {
		fields = append(fields, "created_by", "updated_by")
	}
	sort.Strings(fields)
	return fields
}

func resourceAssemblyFiles(metadata projectMetadata, domain string) ([]PlannedFile, error) {
	domainFile, err := renderDomainFiles(metadata, domain)
	if err != nil {
		return nil, err
	}
	appFile, err := renderAppDomains(metadata)
	if err != nil {
		return nil, err
	}
	schemaFile, err := renderTemplate("templates/resource/schema_bridge_gen.go.tmpl", struct{ Module string }{metadata.Module})
	if err != nil {
		return nil, err
	}
	return []PlannedFile{
		{Path: filepath.ToSlash(filepath.Join("internal", domain, "resources_gen.go")), Content: domainFile, Owned: true},
		appFile,
		{Path: "cmd/migrate/schema_gen.go", Content: schemaFile, Owned: true},
	}, nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
