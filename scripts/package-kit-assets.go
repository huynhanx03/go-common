//go:build ignore

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type versions struct {
	GoVersion string `json:"go"`
	Ent       string `json:"ent"`
	Gin       string `json:"gin"`
	UUID      string `json:"uuid"`
	Atlas     string `json:"atlas"`
}

const atlasCommunityVersion = "1.3.0-community"

func main() {
	check := len(os.Args) == 2 && os.Args[1] == "--check"
	if len(os.Args) > 2 || (len(os.Args) == 2 && !check) {
		fail("usage: go run scripts/package-kit-assets.go [--check]")
	}
	root, err := repositoryRoot()
	if err != nil {
		fail(err.Error())
	}
	assets, err := packageAssets(root)
	if err != nil {
		fail(err.Error())
	}
	for name, content := range assets {
		path := filepath.Join(root, "internal", "kit", "assets", name)
		if check {
			existing, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(existing, content) {
				fail("packaged asset is stale: " + filepath.ToSlash(filepath.Join("internal/kit/assets", name)))
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fail(err.Error())
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			fail(err.Error())
		}
	}
}

func repositoryRoot() (string, error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for directory := workingDirectory; ; directory = filepath.Dir(directory) {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(directory, "docker", "postgres.yml")); err == nil {
				return directory, nil
			}
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", errors.New("could not find repository root with go.mod and docker/postgres.yml")
		}
	}
}

func packageAssets(root string) (map[string][]byte, error) {
	moduleFile, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	defer moduleFile.Close()

	var result versions
	var readingRequirements bool
	scanner := bufio.NewScanner(moduleFile)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "go ") {
			result.GoVersion = strings.TrimSpace(strings.TrimPrefix(line, "go "))
		}
		if line == "require (" {
			readingRequirements = true
			continue
		}
		if readingRequirements && line == ")" {
			readingRequirements = false
			continue
		}
		if !readingRequirements || line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "entgo.io/ent":
			result.Ent = fields[1]
		case "github.com/gin-gonic/gin":
			result.Gin = fields[1]
		case "github.com/google/uuid":
			result.UUID = fields[1]
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if result.GoVersion == "" || result.Ent == "" || result.Gin == "" || result.UUID == "" {
		return nil, errors.New("go.mod is missing a required project version")
	}
	result.Atlas = atlasCommunityVersion
	versionData, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, err
	}
	versionData = append(versionData, '\n')
	postgres, err := os.ReadFile(filepath.Join(root, "docker", "postgres.yml"))
	if err != nil {
		return nil, fmt.Errorf("read canonical PostgreSQL configuration: %w", err)
	}
	return map[string][]byte{"versions.json": versionData, "postgres.yml": postgres}, nil
}

func fail(message string) {
	_, _ = fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
