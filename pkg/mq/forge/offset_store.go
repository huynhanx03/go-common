package forge

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// OffsetStore persists consumer offsets to file.
// Each consumer group + topic pair gets an 8-byte file containing the committed offset.
type OffsetStore struct {
	dir            string
	maxGroups      int
	registrationMu sync.Mutex
}

// NewOffsetStore creates an offset store rooted at the given directory.
func NewOffsetStore(dir string) (*OffsetStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: offset directory", ErrInvalidConfig)
	}
	if err := ensurePrivateDirectory(dir); err != nil {
		return nil, fmt.Errorf("forge: offset store mkdir: %w", err)
	}
	if err := syncDirectory(filepath.Dir(dir)); err != nil {
		return nil, fmt.Errorf("forge: sync offset-store parent: %w", err)
	}
	return &OffsetStore{dir: dir, maxGroups: DefaultMaxConsumerGroups}, nil
}

// Commit persists the offset for the given group and topic.
// Uses write-to-temp + rename for crash-safe atomicity.
func (s *OffsetStore) Commit(group, topic string, offset uint64) error {
	if s == nil || !validResourceName(group) || !validResourceName(topic) {
		return fmt.Errorf("%w: offset identity", ErrInvalidConfig)
	}
	s.registrationMu.Lock()
	defer s.registrationMu.Unlock()
	path := s.path(group, topic)
	dir := filepath.Dir(path)
	if err := s.ensureGroupDirectoryLocked(group); err != nil {
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

	n, writeErr := f.Write(buf[:])
	if writeErr == nil && n != len(buf) {
		writeErr = io.ErrShortWrite
	}
	if writeErr != nil {
		return errors.Join(writeErr, f.Close(), os.Remove(tmp))
	}
	if err := f.Sync(); err != nil {
		return errors.Join(err, f.Close(), os.Remove(tmp))
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

// Load reads the committed offset for the given group and topic.
// Returns 0 if no offset has been committed yet.
func (s *OffsetStore) Load(group, topic string) (uint64, error) {
	if s == nil || !validResourceName(group) || !validResourceName(topic) {
		return 0, fmt.Errorf("%w: offset identity", ErrInvalidConfig)
	}
	file, err := os.Open(s.path(group, topic))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if info.Size() != offsetByteSize {
		return 0, ErrCorruptRecord
	}
	var data [offsetByteSize]byte
	if _, err := io.ReadFull(file, data[:]); err != nil {
		return 0, errors.Join(ErrCorruptRecord, err)
	}
	return binary.BigEndian.Uint64(data[:]), nil
}

func (s *OffsetStore) acquireConsumer(group, topic string) (*consumerLease, error) {
	if s == nil || !validResourceName(group) || !validResourceName(topic) {
		return nil, fmt.Errorf("%w: consumer identity", ErrInvalidConfig)
	}
	s.registrationMu.Lock()
	defer s.registrationMu.Unlock()
	return s.acquireConsumerLocked(group, topic)
}

func (s *OffsetStore) acquireConsumerLocked(group, topic string) (*consumerLease, error) {
	groupDirectory := filepath.Join(s.dir, group)
	if err := s.ensureGroupDirectoryLocked(group); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(
		filepath.Join(groupDirectory, topic+".lock"),
		os.O_CREATE|os.O_RDWR,
		filePerm,
	)
	if err != nil {
		return nil, err
	}
	if err := ensurePrivateFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := tryLockConsumerFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := errors.Join(syncDirectory(s.dir), syncDirectory(groupDirectory)); err != nil {
		_ = unlockConsumerFile(file)
		_ = file.Close()
		return nil, err
	}
	return &consumerLease{file: file}, nil
}

func (s *OffsetStore) ensureGroupDirectoryLocked(group string) error {
	groupDirectory := filepath.Join(s.dir, group)
	created := false
	if _, err := os.Stat(groupDirectory); errors.Is(err, os.ErrNotExist) {
		count, countErr := s.groupCountLocked()
		if countErr != nil {
			return countErr
		}
		if count >= s.maxGroups {
			return ErrResourceLimit
		}
		created = true
	} else if err != nil {
		return err
	}
	if err := ensurePrivateDirectory(groupDirectory); err != nil {
		return err
	}
	if created {
		return syncDirectory(s.dir)
	}
	return nil
}

func (s *OffsetStore) groupCountLocked() (int, error) {
	count := 0
	err := forEachDirectoryEntry(s.dir, func(entry os.DirEntry) error {
		if !entry.IsDir() || !validResourceName(entry.Name()) {
			return ErrInvalidSegmentLayout
		}
		count++
		if count > s.maxGroups {
			return ErrResourceLimit
		}
		return nil
	})
	return count, err
}

// MinimumOffset returns the slowest registered consumer offset for topic.
// The durable lock file registers a group even before its first commit.
func (s *OffsetStore) MinimumOffset(topic string) (uint64, bool, error) {
	if s == nil || !validResourceName(topic) {
		return 0, false, fmt.Errorf("%w: topic", ErrInvalidConfig)
	}
	s.registrationMu.Lock()
	defer s.registrationMu.Unlock()

	var minimum uint64
	found := false
	count := 0
	err := forEachDirectoryEntry(s.dir, func(groupEntry os.DirEntry) error {
		if !groupEntry.IsDir() || !validResourceName(groupEntry.Name()) {
			return ErrInvalidSegmentLayout
		}
		count++
		if count > s.maxGroups {
			return ErrResourceLimit
		}
		groupDirectory := filepath.Join(s.dir, groupEntry.Name())
		offsetPath := filepath.Join(groupDirectory, topic+extOffset)
		lockPath := filepath.Join(groupDirectory, topic+".lock")
		_, offsetErr := os.Stat(offsetPath)
		_, lockErr := os.Stat(lockPath)
		if errors.Is(offsetErr, os.ErrNotExist) && errors.Is(lockErr, os.ErrNotExist) {
			return nil
		}
		if offsetErr != nil && !errors.Is(offsetErr, os.ErrNotExist) {
			return offsetErr
		}
		if lockErr != nil && !errors.Is(lockErr, os.ErrNotExist) {
			return lockErr
		}

		offset, loadErr := s.Load(groupEntry.Name(), topic)
		if loadErr != nil {
			return loadErr
		}
		if !found || offset < minimum {
			minimum = offset
			found = true
		}
		return nil
	})
	if err != nil {
		return 0, false, err
	}
	return minimum, found, nil
}

func (s *OffsetStore) deleteGroup(group, topic string) error {
	s.registrationMu.Lock()
	defer s.registrationMu.Unlock()
	lease, err := s.acquireConsumerLocked(group, topic)
	if err != nil {
		return err
	}
	groupDirectory := filepath.Join(s.dir, group)
	offsetPath := filepath.Join(groupDirectory, topic+extOffset)
	lockPath := filepath.Join(groupDirectory, topic+".lock")
	// Broker.groupMu and the broker directory lease keep this identity fenced
	// while deletion runs. Close the lease before unlinking its file because
	// Windows does not permit removing an open lock file.
	if err := lease.Close(); err != nil {
		return err
	}

	var deleteErrors []error
	for _, path := range []string{offsetPath, lockPath} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			deleteErrors = append(deleteErrors, err)
		}
	}
	deleteErrors = append(deleteErrors, syncDirectory(groupDirectory))
	if err := errors.Join(deleteErrors...); err != nil {
		return err
	}
	empty, err := directoryIsEmpty(groupDirectory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if empty {
		if err := os.Remove(groupDirectory); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return syncDirectory(s.dir)
}

func directoryIsEmpty(path string) (bool, error) {
	directory, err := os.Open(path)
	if err != nil {
		return false, err
	}
	_, readErr := directory.Readdirnames(1)
	closeErr := directory.Close()
	if errors.Is(readErr, io.EOF) {
		return true, closeErr
	}
	if readErr != nil {
		return false, errors.Join(readErr, closeErr)
	}
	return false, closeErr
}

func (s *OffsetStore) path(group, topic string) string {
	return filepath.Join(s.dir, group, topic+extOffset)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
