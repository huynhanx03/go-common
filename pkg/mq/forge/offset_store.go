package forge

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// OffsetStore persists consumer offsets to file.
// Each consumer group + topic pair gets an 8-byte file containing the committed offset.
type OffsetStore struct {
	dir string
}

// NewOffsetStore creates an offset store rooted at the given directory.
func NewOffsetStore(dir string) (*OffsetStore, error) {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, fmt.Errorf("forge: offset store mkdir: %w", err)
	}
	return &OffsetStore{dir: dir}, nil
}

// Commit persists the offset for the given group and topic.
// Uses write-to-temp + rename for crash-safe atomicity.
func (s *OffsetStore) Commit(group, topic string, offset uint64) error {
	path := s.path(group, topic)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}

	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], offset)

	// Write to unique temp file, fsync, then atomic rename — crash-safe and concurrent-safe.
	f, err := os.CreateTemp(dir, ".offset-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()

	if _, err := f.Write(buf[:]); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	f.Close()
	return os.Rename(tmp, path)
}

// Load reads the committed offset for the given group and topic.
// Returns 0 if no offset has been committed yet.
func (s *OffsetStore) Load(group, topic string) (uint64, error) {
	data, err := os.ReadFile(s.path(group, topic))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if len(data) < offsetByteSize {
		return 0, nil
	}
	return binary.BigEndian.Uint64(data), nil
}

func (s *OffsetStore) path(group, topic string) string {
	// Sanitize to prevent path traversal and null byte truncation.
	group = sanitizeName(group)
	topic = sanitizeName(topic)
	return filepath.Join(s.dir, group, topic+extOffset)
}

// sanitizeName replaces characters unsafe for filesystem paths.
func sanitizeName(name string) string {
	if name == "" {
		return "_"
	}
	name = strings.ReplaceAll(name, "\x00", "_")
	name = strings.ReplaceAll(name, string(filepath.Separator), "_")
	name = strings.ReplaceAll(name, "..", "_")
	return name
}
