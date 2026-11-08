package kit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

type fileOperations struct {
	publish func(string, string) error
}

type mutationJournal struct {
	Files  []string `json:"files"`
	Staged string   `json:"staged"`
}

type stagedFile struct {
	PlannedFile
	Target     string
	Source     string
	Backup     string
	Existed    bool
	Mode       fs.FileMode
	OldContent []byte
}

func defaultPublish(source, target string) error { return os.Rename(source, target) }

func (g *Generator) Apply(ctx context.Context, plan Plan) (Result, error) {
	paths, err := validatePlan(plan)
	if err != nil {
		return Result{}, err
	}
	root, err := filepath.Abs(plan.Root)
	if err != nil {
		return Result{}, fmt.Errorf("resolve project directory: %w", err)
	}
	if plan.NoOp {
		return g.verifyNoOp(root, plan, paths)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if plan.Kind == commandInit {
		return g.applyInit(ctx, root, plan, paths)
	}
	if plan.Kind != commandDomain && plan.Kind != commandResource {
		return Result{}, fmt.Errorf("%w: unknown operation %q", ErrInvalidPlan, plan.Kind)
	}
	return g.applyAdd(ctx, root, plan)
}

func validatePlan(plan Plan) ([]string, error) {
	if plan.Root == "" || plan.Kind == "" {
		return nil, ErrInvalidPlan
	}
	paths := make([]string, 0, len(plan.Files))
	seen := make(map[string]struct{}, len(plan.Files))
	for _, file := range plan.Files {
		path, err := cleanProjectPath(file.Path)
		if err != nil {
			return nil, err
		}
		if path == metadataFileName || path == lockFileName || path == pendingFileName || strings.HasPrefix(path, stageDirectoryPrefix) {
			return nil, fmt.Errorf("%w: reserved path %q", ErrInvalidPlan, file.Path)
		}
		if _, exists := seen[path]; exists {
			return nil, fmt.Errorf("%w: duplicate path %q", ErrInvalidPlan, file.Path)
		}
		if file.ExpectedHash != "" && len(file.ExpectedHash) != sha256.Size*2 {
			return nil, fmt.Errorf("%w: invalid hash for %q", ErrInvalidPlan, file.Path)
		}
		if file.ExpectedHash != "" {
			if _, err := hex.DecodeString(file.ExpectedHash); err != nil {
				return nil, fmt.Errorf("%w: invalid hash for %q", ErrInvalidPlan, file.Path)
			}
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths, nil
}

func cleanProjectPath(name string) (string, error) {
	if name == "" || filepath.IsAbs(name) {
		return "", fmt.Errorf("%w: %q", ErrUnsafePath, name)
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", ErrUnsafePath, name)
	}
	if filepath.ToSlash(clean) != name {
		return "", fmt.Errorf("%w: project paths must be clean relative paths: %q", ErrUnsafePath, name)
	}
	return clean, nil
}

func (g *Generator) applyInit(ctx context.Context, root string, plan Plan, paths []string) (Result, error) {
	files, _, _, err := prepareFiles(plan, "", "")
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	rootInfo, err := os.Lstat(root)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Result{}, fmt.Errorf("inspect project directory: %w", err)
	}
	if exists {
		if rootInfo.Mode()&os.ModeSymlink != 0 {
			return Result{}, fmt.Errorf("%w: project root %q is a symlink", ErrUnsafePath, root)
		}
		if !rootInfo.IsDir() {
			return Result{}, fmt.Errorf("%w: project root %q is not a directory", ErrFileConflict, root)
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return Result{}, fmt.Errorf("read project directory: %w", err)
		}
		if len(entries) != 0 {
			return Result{}, fmt.Errorf("%w: project directory %q is not empty", ErrFileConflict, root)
		}
	}
	if root == string(filepath.Separator) {
		return Result{}, fmt.Errorf("%w: project directory cannot be filesystem root", ErrUnsafePath)
	}
	parent := filepath.Dir(root)
	createdParents, err := makeDirectories(parent)
	if err != nil {
		return Result{}, fmt.Errorf("create project parent: %w", err)
	}
	rollbackParents := func() {
		removeEmptyDirectories(createdParents)
	}
	stage, err := os.MkdirTemp(parent, stageDirectoryPrefix)
	if err != nil {
		rollbackParents()
		return Result{}, fmt.Errorf("stage project: %w", err)
	}
	stageOwned := true
	defer func() {
		if stageOwned {
			_ = os.RemoveAll(stage)
		}
	}()
	for index := range files {
		if err := ctx.Err(); err != nil {
			rollbackParents()
			return Result{}, err
		}
		path := filepath.Join(stage, files[index].Path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			rollbackParents()
			return Result{}, fmt.Errorf("stage %q: %w", files[index].Path, err)
		}
		if err := os.WriteFile(path, files[index].Content, 0o644); err != nil {
			rollbackParents()
			return Result{}, fmt.Errorf("stage %q: %w", files[index].Path, err)
		}
	}
	if err := ctx.Err(); err != nil {
		rollbackParents()
		return Result{}, err
	}
	if err := recheckInitDestination(root, exists); err != nil {
		rollbackParents()
		return Result{}, err
	}
	backup := ""
	if exists {
		if err := os.Chmod(stage, rootInfo.Mode().Perm()); err != nil {
			rollbackParents()
			return Result{}, fmt.Errorf("preserve project directory mode: %w", err)
		}
		backup = stage + ".empty"
		if err := os.Rename(root, backup); err != nil {
			rollbackParents()
			return Result{}, fmt.Errorf("prepare empty project directory: %w", err)
		}
	}
	if err := g.files.publish(stage, root); err != nil {
		if backup != "" {
			if restoreErr := os.Rename(backup, root); restoreErr != nil {
				return Result{}, errors.Join(fmt.Errorf("publish project: %w", err), fmt.Errorf("%w: restore empty destination: %v", ErrRollbackFailed, restoreErr))
			}
		}
		rollbackParents()
		return Result{}, fmt.Errorf("publish project: %w", err)
	}
	stageOwned = false
	if backup != "" {
		if err := os.Remove(backup); err != nil {
			return Result{}, fmt.Errorf("remove empty destination backup: %w", err)
		}
	}
	return Result{Root: root, Created: paths}, nil
}

func recheckInitDestination(root string, wasPresent bool) error {
	info, err := os.Lstat(root)
	if !wasPresent {
		if err == nil {
			return fmt.Errorf("%w: project directory %q appeared during generation", ErrFileConflict, root)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect project directory: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: empty project directory changed during generation", ErrFileConflict)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%w: project directory changed during generation", ErrFileConflict)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("inspect project directory: %w", err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("%w: project directory changed during generation", ErrFileConflict)
	}
	return nil
}

func (g *Generator) applyAdd(ctx context.Context, root string, plan Plan) (Result, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return Result{}, fmt.Errorf("open project directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return Result{}, fmt.Errorf("%w: project root %q is not a directory", ErrUnsafePath, root)
	}
	pendingPath := filepath.Join(root, pendingFileName)
	if _, info, err := inspectTarget(root, pendingFileName); err != nil {
		return Result{}, err
	} else if info != nil {
		journal, err := os.ReadFile(pendingPath)
		if err != nil {
			return Result{}, fmt.Errorf("read recovery marker: %w", err)
		}
		return Result{}, recoveryError(journal)
	}
	lockPath := filepath.Join(root, lockFileName)
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return Result{}, fmt.Errorf("%w: %s", ErrMutationLocked, lockFileName)
	}
	if err != nil {
		return Result{}, fmt.Errorf("acquire project lock: %w", err)
	}
	_ = lock.Close()
	defer func() { _ = os.Remove(lockPath) }()

	if _, info, err := inspectTarget(root, pendingFileName); err != nil {
		return Result{}, err
	} else if info != nil {
		journal, err := os.ReadFile(pendingPath)
		if err != nil {
			return Result{}, fmt.Errorf("read recovery marker: %w", err)
		}
		return Result{}, recoveryError(journal)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	files, _, priorHashes, err := prepareFiles(plan, root, plan.ExpectedMetadataHash)
	if err != nil {
		return Result{}, err
	}
	entries, err := inspectFiles(root, files, priorHashes)
	if err != nil {
		return Result{}, err
	}
	stageRoot, err := os.MkdirTemp(root, stageDirectoryPrefix)
	if err != nil {
		return Result{}, fmt.Errorf("stage project changes: %w", err)
	}
	keepStage := false
	defer func() {
		if !keepStage {
			_ = os.RemoveAll(stageRoot)
		}
	}()
	if err := stageChanges(stageRoot, entries); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	journal := mutationJournal{Staged: filepath.Base(stageRoot), Files: make([]string, len(entries))}
	for index := range entries {
		journal.Files[index] = entries[index].Path
	}
	marker, err := json.Marshal(journal)
	if err != nil {
		return Result{}, fmt.Errorf("encode mutation journal: %w", err)
	}
	markerFile, err := os.OpenFile(pendingPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return Result{}, recoveryError(marker)
	}
	if err != nil {
		return Result{}, fmt.Errorf("create mutation journal: %w", err)
	}
	if _, err = markerFile.Write(marker); err != nil {
		_ = markerFile.Close()
		_ = os.Remove(pendingPath)
		return Result{}, fmt.Errorf("write mutation journal: %w", err)
	}
	if err := markerFile.Close(); err != nil {
		_ = os.Remove(pendingPath)
		return Result{}, fmt.Errorf("close mutation journal: %w", err)
	}
	committed := make([]stagedFile, 0, len(entries))
	createdDirectories := make([]string, 0)
	commitErr := publishChanges(ctx, root, entries, &committed, &createdDirectories, g.files.publish)
	if commitErr == nil {
		commitErr = ctx.Err()
	}
	if commitErr != nil {
		rollbackErr := rollbackChanges(committed, createdDirectories, g.files.publish)
		if rollbackErr == nil {
			_ = os.Remove(pendingPath)
		} else {
			keepStage = true
			commitErr = errors.Join(commitErr, fmt.Errorf("%w: %v", ErrRollbackFailed, rollbackErr))
		}
		return Result{}, commitErr
	}
	if err := os.Remove(pendingPath); err != nil {
		keepStage = true
		return Result{}, fmt.Errorf("remove completed mutation journal: %w", err)
	}
	created, updated := mutationPaths(entries)
	return Result{Root: root, Created: created, Updated: updated}, nil
}

func (g *Generator) verifyNoOp(root string, plan Plan, paths []string) (Result, error) {
	if plan.ExpectedMetadataHash != "" {
		metadataPath := filepath.Join(root, metadataFileName)
		metadata, err := os.ReadFile(metadataPath)
		if err != nil || contentHash(metadata) != plan.ExpectedMetadataHash {
			return Result{}, fmt.Errorf("%w: %s changed since the plan was created", ErrFileConflict, metadataFileName)
		}
	}
	for index, path := range paths {
		target, info, err := inspectTarget(root, path)
		if err != nil {
			return Result{}, err
		}
		if info == nil {
			return Result{}, fmt.Errorf("%w: no-op file %q is missing", ErrFileConflict, path)
		}
		if !info.Mode().IsRegular() {
			return Result{}, fmt.Errorf("%w: no-op target %q is not a file", ErrUnsafePath, path)
		}
		content, err := os.ReadFile(target)
		if err != nil {
			return Result{}, fmt.Errorf("read no-op file %q: %w", path, err)
		}
		if !bytes.Equal(content, plan.Files[index].Content) {
			return Result{}, fmt.Errorf("%w: no-op file %q changed", ErrFileConflict, path)
		}
	}
	return Result{Root: root, NoOp: true}, nil
}

func prepareFiles(plan Plan, root string, expectedMetadataHash string) ([]PlannedFile, projectMetadata, map[string]string, error) {
	var priorHashes map[string]string
	if plan.Kind == commandInit {
		if plan.Metadata == nil || plan.Metadata.Version != metadataVersion {
			return nil, projectMetadata{}, nil, ErrInvalidProjectMetadata
		}
	} else {
		if plan.Metadata != nil && plan.Metadata.Version != metadataVersion {
			return nil, projectMetadata{}, nil, ErrInvalidProjectMetadata
		}
		if _, info, err := inspectTarget(root, metadataFileName); err != nil {
			return nil, projectMetadata{}, nil, err
		} else if info == nil {
			return nil, projectMetadata{}, nil, fmt.Errorf("%w: %s is missing", ErrInvalidProjectMetadata, metadataFileName)
		}
		metadataPath := filepath.Join(root, metadataFileName)
		metadataContent, err := os.ReadFile(metadataPath)
		if err != nil {
			return nil, projectMetadata{}, nil, fmt.Errorf("%w: read %s: %v", ErrInvalidProjectMetadata, metadataFileName, err)
		}
		if expectedMetadataHash == "" || contentHash(metadataContent) != expectedMetadataHash {
			return nil, projectMetadata{}, nil, fmt.Errorf("%w: %s changed since the plan was created", ErrFileConflict, metadataFileName)
		}
		var current projectMetadata
		if err := yaml.Unmarshal(metadataContent, &current); err != nil || current.Version != metadataVersion {
			return nil, projectMetadata{}, nil, fmt.Errorf("%w: %s", ErrInvalidProjectMetadata, metadataFileName)
		}
		priorHashes = cloneMetadata(current).Files
		if plan.Metadata != nil {
			current = mergeMetadata(current, *plan.Metadata)
		}
		plan.Metadata = &current
	}
	metadata := cloneMetadata(*plan.Metadata)
	if metadata.Files == nil {
		metadata.Files = make(map[string]string)
	}
	files := append([]PlannedFile(nil), plan.Files...)
	for index := range files {
		if files[index].Owned {
			metadata.Files[files[index].Path] = contentHash(files[index].Content)
		}
	}
	metadataContent, err := yaml.Marshal(metadata)
	if err != nil {
		return nil, projectMetadata{}, nil, fmt.Errorf("encode project metadata: %w", err)
	}
	metadataFile := PlannedFile{Path: metadataFileName, Content: metadataContent, Owned: true}
	if plan.Kind != commandInit {
		metadataFile.ExpectedHash = expectedMetadataHash
	}
	files = append(files, metadataFile)
	return files, metadata, priorHashes, nil
}

func mergeMetadata(current, next projectMetadata) projectMetadata {
	next = cloneMetadata(next)
	if next.Files == nil {
		next.Files = make(map[string]string, len(current.Files))
		for path, hash := range current.Files {
			next.Files[path] = hash
		}
	} else {
		for path, hash := range current.Files {
			if _, exists := next.Files[path]; !exists {
				next.Files[path] = hash
			}
		}
	}
	return next
}

func cloneMetadata(metadata projectMetadata) projectMetadata {
	copy := metadata
	copy.Domains = append([]string(nil), metadata.Domains...)
	copy.Resources = append([]resourceRecord(nil), metadata.Resources...)
	for index := range copy.Resources {
		copy.Resources[index].Operations = append([]string(nil), metadata.Resources[index].Operations...)
	}
	copy.Files = make(map[string]string, len(metadata.Files))
	for path, hash := range metadata.Files {
		copy.Files[path] = hash
	}
	return copy
}

func inspectFiles(root string, files []PlannedFile, priorHashes map[string]string) ([]stagedFile, error) {
	entries := make([]stagedFile, 0, len(files))
	for _, file := range files {
		target, info, err := inspectTarget(root, file.Path)
		if err != nil {
			return nil, err
		}
		entry := stagedFile{PlannedFile: file, Target: target, Existed: info != nil}
		if info != nil {
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("%w: target %q is not a regular file", ErrUnsafePath, file.Path)
			}
			entry.Mode = info.Mode().Perm()
			entry.OldContent, err = os.ReadFile(target)
			if err != nil {
				return nil, fmt.Errorf("read existing file %q: %w", file.Path, err)
			}
		}
		if file.Path == metadataFileName {
			if (info == nil && file.ExpectedHash != "") || (info != nil && contentHash(entry.OldContent) != file.ExpectedHash) {
				return nil, fmt.Errorf("%w: %s changed", ErrFileConflict, metadataFileName)
			}
			entries = append(entries, entry)
			continue
		}
		if file.Owned {
			if info == nil {
				if file.ExpectedHash != "" || priorHashes[file.Path] != "" {
					return nil, fmt.Errorf("%w: generated file %q is missing", ErrFileConflict, file.Path)
				}
			} else if file.ExpectedHash == "" || contentHash(entry.OldContent) != file.ExpectedHash || priorHashes[file.Path] != file.ExpectedHash {
				return nil, fmt.Errorf("%w: generated file %q was changed", ErrFileConflict, file.Path)
			}
		} else if info != nil || file.ExpectedHash != "" {
			return nil, fmt.Errorf("%w: project-owned file %q already exists", ErrFileConflict, file.Path)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func inspectTarget(root, relative string) (string, os.FileInfo, error) {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return "", nil, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return "", nil, fmt.Errorf("%w: project root is not a directory", ErrUnsafePath)
	}
	clean, err := cleanProjectPath(relative)
	if err != nil {
		return "", nil, err
	}
	target := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", nil, fmt.Errorf("%w: %q", ErrUnsafePath, relative)
	}
	current := root
	parts := strings.Split(clean, string(filepath.Separator))
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return target, nil, nil
		}
		if err != nil {
			return "", nil, fmt.Errorf("inspect project path %q: %w", relative, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", nil, fmt.Errorf("%w: %q contains a symlink", ErrUnsafePath, relative)
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", nil, fmt.Errorf("%w: parent of %q is not a directory", ErrFileConflict, relative)
		}
		if index == len(parts)-1 {
			return target, info, nil
		}
	}
	return target, nil, nil
}

func stageChanges(stageRoot string, entries []stagedFile) error {
	for index := range entries {
		entry := &entries[index]
		entry.Source = filepath.Join(stageRoot, "new", entry.Path)
		if err := os.MkdirAll(filepath.Dir(entry.Source), 0o755); err != nil {
			return fmt.Errorf("stage %q: %w", entry.Path, err)
		}
		mode := fs.FileMode(0o644)
		if entry.Existed {
			mode = entry.Mode
			entry.Backup = filepath.Join(stageRoot, "backup", fmt.Sprintf("%d", index))
			if err := os.MkdirAll(filepath.Dir(entry.Backup), 0o700); err != nil {
				return fmt.Errorf("stage backup for %q: %w", entry.Path, err)
			}
			if err := os.WriteFile(entry.Backup, entry.OldContent, mode); err != nil {
				return fmt.Errorf("stage backup for %q: %w", entry.Path, err)
			}
		}
		if err := os.WriteFile(entry.Source, entry.Content, mode); err != nil {
			return fmt.Errorf("stage %q: %w", entry.Path, err)
		}
	}
	return nil
}

func publishChanges(ctx context.Context, root string, entries []stagedFile, committed *[]stagedFile, createdDirectories *[]string, publish func(string, string) error) error {
	for index := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := entries[index]
		target, info, err := inspectTarget(root, entry.Path)
		if err != nil {
			return err
		}
		if (info != nil) != entry.Existed {
			return fmt.Errorf("%w: %q changed while the plan was being applied", ErrFileConflict, entry.Path)
		}
		if info != nil {
			content, err := os.ReadFile(target)
			if err != nil {
				return fmt.Errorf("recheck %q: %w", entry.Path, err)
			}
			if !bytes.Equal(content, entry.OldContent) {
				return fmt.Errorf("%w: %q changed while the plan was being applied", ErrFileConflict, entry.Path)
			}
		}
		parentDirs, err := makeDirectories(filepath.Dir(target))
		if err != nil {
			return fmt.Errorf("create directory for %q: %w", entry.Path, err)
		}
		*createdDirectories = append(*createdDirectories, parentDirs...)
		entry.Target = target
		*committed = append(*committed, entry)
		if entry.Existed {
			if err := os.Remove(target); err != nil {
				return fmt.Errorf("replace %q: %w", entry.Path, err)
			}
		}
		if err := publish(entry.Source, target); err != nil {
			return fmt.Errorf("publish %q: %w", entry.Path, err)
		}
	}
	return nil
}

func rollbackChanges(committed []stagedFile, directories []string, publish func(string, string) error) error {
	var rollbackErr error
	for index := len(committed) - 1; index >= 0; index-- {
		entry := committed[index]
		if entry.Existed {
			if err := os.Remove(entry.Target); err != nil && !errors.Is(err, os.ErrNotExist) {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("remove replacement %q: %w", entry.Path, err))
				continue
			}
			if err := publish(entry.Backup, entry.Target); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore %q: %w", entry.Path, err))
			}
		} else if err := os.Remove(entry.Target); err != nil && !errors.Is(err, os.ErrNotExist) {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("remove new file %q: %w", entry.Path, err))
		}
	}
	for index := len(directories) - 1; index >= 0; index-- {
		if err := os.Remove(directories[index]); err != nil && !errors.Is(err, os.ErrNotExist) && !os.IsExist(err) {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("remove directory %q: %w", directories[index], err))
		}
	}
	return rollbackErr
}

func makeDirectories(path string) ([]string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	var missing []string
	current := abs
	for {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return nil, fmt.Errorf("%w: %q is not a directory", ErrUnsafePath, current)
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			return nil, fmt.Errorf("%w: no existing parent for %q", ErrUnsafePath, path)
		}
		current = parent
	}
	created := make([]string, 0, len(missing))
	for index := len(missing) - 1; index >= 0; index-- {
		if err := os.Mkdir(missing[index], 0o755); err != nil {
			removeEmptyDirectories(created)
			return nil, err
		}
		created = append(created, missing[index])
	}
	return created, nil
}

func removeEmptyDirectories(paths []string) {
	for index := len(paths) - 1; index >= 0; index-- {
		_ = os.Remove(paths[index])
	}
}

func recoveryError(marker []byte) error {
	var journal mutationJournal
	if err := json.Unmarshal(marker, &journal); err != nil || len(journal.Files) == 0 {
		return fmt.Errorf("%w: inspect %s before retrying", ErrRecoveryRequired, pendingFileName)
	}
	return fmt.Errorf("%w: review %s and affected files: %s", ErrRecoveryRequired, pendingFileName, strings.Join(journal.Files, ", "))
}

func mutationPaths(entries []stagedFile) ([]string, []string) {
	created := make([]string, 0, len(entries))
	updated := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Path == metadataFileName {
			continue
		}
		if entry.Existed {
			updated = append(updated, entry.Path)
		} else {
			created = append(created, entry.Path)
		}
	}
	return created, updated
}

func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
