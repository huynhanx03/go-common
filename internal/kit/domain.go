package kit

import (
	"context"
	"fmt"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"

	"go.yaml.in/yaml/v3"
)

var domainNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

type domainTemplateData struct {
	Module       string
	Domain       string
	HasResources bool
	Resources    []resourceRegistration
}

type resourceRegistration struct {
	Alias      string
	ImportPath string
	Route      string
}

type projectDomainData struct {
	Module          string
	Domains         []projectDomainRegistration
	HasResources    bool
	HasSoftDelete   bool
	HasModifier     bool
	SoftDeleteTypes []string
}

type projectDomainRegistration struct {
	Alias        string
	ImportPath   string
	Name         string
	HasResources bool
}

func (g *Generator) Domain(ctx context.Context, root string, input DomainInput) (Plan, error) {
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	if !validDomainName(input.Name) {
		return Plan{}, fmt.Errorf("%w: %q", ErrInvalidDomain, input.Name)
	}
	projectRoot, metadata, metadataHash, err := readProjectMetadata(root)
	if err != nil {
		return Plan{}, err
	}

	previousHashes := metadata.Files
	metadata = cloneMetadata(metadata)
	existing := slices.Contains(metadata.Domains, input.Name)
	if !existing {
		metadata.Domains = append(metadata.Domains, input.Name)
		sort.Strings(metadata.Domains)
	}
	domainFiles, err := renderDomainFiles(metadata, input.Name)
	if err != nil {
		return Plan{}, err
	}
	appFile, err := renderAppDomains(metadata)
	if err != nil {
		return Plan{}, err
	}
	appFile.ExpectedHash = previousHashes[appFile.Path]
	files := []PlannedFile{
		{Path: filepath.ToSlash(filepath.Join("internal", input.Name, "resources_gen.go")), Content: domainFiles, Owned: true},
		appFile,
	}
	if existing {
		files[0].ExpectedHash = previousHashes[files[0].Path]
	}
	if !existing {
		moduleFile, err := renderTemplate("templates/domain/module.go.tmpl", domainTemplateData{Module: metadata.Module, Domain: input.Name})
		if err != nil {
			return Plan{}, err
		}
		files = append(files, PlannedFile{Path: filepath.ToSlash(filepath.Join("internal", input.Name, "module.go")), Content: moduleFile})
	}
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	if existing {
		return Plan{Root: projectRoot, Kind: commandDomain, Files: files, ExpectedMetadataHash: metadataHash, NoOp: true}, nil
	}
	return Plan{
		Root: projectRoot, Kind: commandDomain, Files: files,
		Metadata: &metadata, ExpectedMetadataHash: metadataHash,
	}, nil
}

func validDomainName(name string) bool {
	return domainNamePattern.MatchString(name) && !token.IsKeyword(name)
}

func renderDomainFiles(metadata projectMetadata, domain string) ([]byte, error) {
	resources := resourcesForDomain(metadata.Resources, domain)
	registrations := make([]resourceRegistration, 0, len(resources))
	for index, resource := range resources {
		registrations = append(registrations, resourceRegistration{
			Alias:      fmt.Sprintf("resource%d", index),
			ImportPath: metadata.Module + "/internal/" + domain + "/" + resource.Name,
			Route:      resource.Route,
		})
	}
	content, err := renderTemplate("templates/domain/resources_gen.go.tmpl", domainTemplateData{
		Module: metadata.Module, Domain: domain, HasResources: len(registrations) != 0, Resources: registrations,
	})
	if err != nil {
		return nil, err
	}
	return content, nil
}

func renderAppDomains(metadata projectMetadata) (PlannedFile, error) {
	domains := make([]projectDomainRegistration, 0, len(metadata.Domains))
	hasResources, hasSoftDelete, hasModifier := false, false, false
	softDeleteTypes := make([]string, 0)
	for index, name := range metadata.Domains {
		resources := resourcesForDomain(metadata.Resources, name)
		domains = append(domains, projectDomainRegistration{
			Alias:        fmt.Sprintf("domain%d", index),
			ImportPath:   metadata.Module + "/internal/" + name,
			Name:         name,
			HasResources: len(resources) != 0,
		})
		for _, resource := range resources {
			hasResources = true
			if resource.SoftDelete {
				hasSoftDelete = true
				softDeleteTypes = append(softDeleteTypes, resourceTypeName(resource.Name))
			}
			hasModifier = hasModifier || resource.Modifier
		}
	}
	content, err := renderTemplate("templates/project/domains_gen.go.tmpl", projectDomainData{
		Module: metadata.Module, Domains: domains, HasResources: hasResources, HasSoftDelete: hasSoftDelete,
		HasModifier: hasModifier, SoftDeleteTypes: softDeleteTypes,
	})
	if err != nil {
		return PlannedFile{}, err
	}
	return PlannedFile{Path: "internal/app/domains_gen.go", Content: content, Owned: true}, nil
}

func resourcesForDomain(resources []resourceRecord, domain string) []resourceRecord {
	result := make([]resourceRecord, 0)
	for _, resource := range resources {
		if resource.Domain == domain {
			result = append(result, resource)
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Name < result[right].Name })
	return result
}

func readProjectMetadata(root string) (string, projectMetadata, string, error) {
	if root == "" {
		return "", projectMetadata{}, "", ErrInvalidInput
	}
	projectRoot, err := filepath.Abs(root)
	if err != nil {
		return "", projectMetadata{}, "", fmt.Errorf("resolve project directory: %w", err)
	}
	info, err := os.Lstat(projectRoot)
	if err != nil {
		return "", projectMetadata{}, "", fmt.Errorf("open project directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", projectMetadata{}, "", fmt.Errorf("%w: project root %q is not a directory", ErrUnsafePath, projectRoot)
	}
	metadataPath := filepath.Join(projectRoot, metadataFileName)
	metadataInfo, err := os.Lstat(metadataPath)
	if err != nil {
		return "", projectMetadata{}, "", fmt.Errorf("%w: %s is missing", ErrInvalidProjectMetadata, metadataFileName)
	}
	if !metadataInfo.Mode().IsRegular() {
		return "", projectMetadata{}, "", fmt.Errorf("%w: %s is not a regular file", ErrInvalidProjectMetadata, metadataFileName)
	}
	content, err := fs.ReadFile(os.DirFS(projectRoot), metadataFileName)
	if err != nil {
		return "", projectMetadata{}, "", fmt.Errorf("read project metadata: %w", err)
	}
	var metadata projectMetadata
	if err := yaml.Unmarshal(content, &metadata); err != nil || metadata.Version != metadataVersion || metadata.Module == "" {
		return "", projectMetadata{}, "", fmt.Errorf("%w: %s", ErrInvalidProjectMetadata, metadataFileName)
	}
	return projectRoot, metadata, contentHash(content), nil
}
