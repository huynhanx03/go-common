package kit

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
)

func (g *Generator) Init(ctx context.Context, input InitInput) (Plan, error) {
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	if err := normalizeProjectInput(&input, Options{WorkDir: defaultProjectDir}, g.registry); err != nil {
		return Plan{}, err
	}

	root, err := filepath.Abs(input.Directory)
	if err != nil {
		return Plan{}, fmt.Errorf("resolve project directory: %w", err)
	}
	versionData, err := fs.ReadFile(projectFiles, "assets/versions.json")
	if err != nil {
		return Plan{}, fmt.Errorf("read packaged dependency versions: %w", err)
	}
	var versions projectVersions
	if err := json.Unmarshal(versionData, &versions); err != nil {
		return Plan{}, fmt.Errorf("decode packaged dependency versions: %w", err)
	}
	data := projectTemplateData{
		Module:        input.Module,
		ProjectName:   filepath.Base(input.Directory),
		CommonVersion: input.CommonVersion,
		GoVersion:     versions.GoVersion,
		EntVersion:    versions.Ent,
		GinVersion:    versions.Gin,
		UUIDVersion:   versions.UUID,
		AtlasVersion:  versions.Atlas,
	}
	if data.ProjectName == "." || data.ProjectName == string(filepath.Separator) {
		data.ProjectName = filepath.Base(input.Module)
	}
	data.ServiceName = strconv.Quote(data.ProjectName)

	templates := []struct {
		path     string
		template string
		owned    bool
	}{
		{path: "cmd/server/main.go", template: "server_main.go.tmpl"},
		{path: "cmd/migrate/main.go", template: "migrate_main.go.tmpl"},
		{path: "cmd/migrate/schema_gen.go", template: "schema_gen.go.tmpl", owned: true},
		{path: "internal/app/client.go", template: "app_client.go.tmpl"},
		{path: "internal/app/config.go", template: "app_config.go.tmpl"},
		{path: "internal/app/http.go", template: "app_http.go.tmpl"},
		{path: "internal/app/domains_gen.go", template: "domains_gen.go.tmpl", owned: true},
		{path: "ent/generate.go", template: "ent_generate.go.tmpl"},
		{path: "go.mod", template: "go.mod.tmpl"},
		{path: ".env.example", template: "env.example.tmpl"},
		{path: ".gitignore", template: "gitignore.tmpl"},
		{path: "README.md", template: "README.md.tmpl"},
		{path: "Makefile", template: "Makefile.tmpl"},
		{path: "Dockerfile", template: "Dockerfile.tmpl"},
		{path: ".dockerignore", template: "dockerignore.tmpl"},
		{path: "docker-compose.yml", template: "compose.yml.tmpl"},
		{path: "atlas.hcl", template: "atlas.hcl.tmpl"},
	}
	files := make([]PlannedFile, 0, len(templates)+3)
	for _, item := range templates {
		content, err := renderTemplate(filepath.ToSlash(filepath.Join("templates/project", item.template)), data)
		if err != nil {
			return Plan{}, err
		}
		files = append(files, PlannedFile{Path: item.path, Content: content, Owned: item.owned})
	}
	for _, item := range []struct {
		path string
		data string
	}{
		{path: "ent/schema/.gitkeep"},
		{path: "migrations/.gitkeep"},
	} {
		files = append(files, PlannedFile{Path: item.path, Content: []byte(item.data)})
	}
	postgres, err := fs.ReadFile(projectFiles, "assets/postgres.yml")
	if err != nil {
		return Plan{}, fmt.Errorf("read packaged PostgreSQL configuration: %w", err)
	}
	files = append(files, PlannedFile{Path: "infrastructure/docker/postgres.yml", Content: postgres})
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}

	return Plan{
		Root:  root,
		Kind:  commandInit,
		Files: files,
		Metadata: &projectMetadata{
			Version:   metadataVersion,
			Generator: commandName,
			Module:    input.Module,
			HTTP:      input.HTTP,
			ORM:       input.ORM,
			Database:  input.Database,
			Schema:    "ent",
		},
	}, nil
}
