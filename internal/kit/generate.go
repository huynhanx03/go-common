package kit

import (
	"embed"
	"fmt"
	"go/format"
	"io/fs"
	"path/filepath"
	"strings"
	"text/template"
)

//go:generate go run ../../scripts/package-kit-assets.go

//go:embed assets/* templates/*/*
var projectFiles embed.FS

type Generator struct {
	registry Registry
	files    fileOperations
}

func newGenerator(registry Registry) *Generator {
	return newGeneratorWithFileOperations(registry, fileOperations{publish: defaultPublish})
}

func newGeneratorWithFileOperations(registry Registry, operations fileOperations) *Generator {
	if operations.publish == nil {
		operations.publish = defaultPublish
	}
	return &Generator{registry: registry, files: operations}
}

var _ generator = (*Generator)(nil)

type projectVersions struct {
	GoVersion string `json:"go"`
	Ent       string `json:"ent"`
	Gin       string `json:"gin"`
	UUID      string `json:"uuid"`
	Atlas     string `json:"atlas"`
}

type projectTemplateData struct {
	Module        string
	ProjectName   string
	ServiceName   string
	CommonVersion string
	GoVersion     string
	EntVersion    string
	GinVersion    string
	UUIDVersion   string
	AtlasVersion  string
	Domains       []projectDomainRegistration
	HasResources  bool
	HasSoftDelete bool
	HasModifier   bool
}

func renderTemplate(name string, data any) ([]byte, error) {
	content, err := fs.ReadFile(projectFiles, filepath.ToSlash(name))
	if err != nil {
		return nil, fmt.Errorf("read project template %q: %w", name, err)
	}
	template, err := template.New(name).Option("missingkey=error").Parse(string(content))
	if err != nil {
		return nil, fmt.Errorf("parse project template %q: %w", name, err)
	}
	var rendered strings.Builder
	if err := template.Execute(&rendered, data); err != nil {
		return nil, fmt.Errorf("render project template %q: %w", name, err)
	}
	if strings.HasSuffix(name, ".go.tmpl") {
		formatted, err := format.Source([]byte(rendered.String()))
		if err != nil {
			return nil, fmt.Errorf("format project template %q: %w", name, err)
		}
		return formatted, nil
	}
	return []byte(rendered.String()), nil
}
