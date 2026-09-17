package forge

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"sort"
)

const (
	indexEntrySize = 12 // RelativeOffset(4) + Position(8)
	// maxIndexEntries bounds retained index memory for one topic/segment. The
	// configuration validator applies the same ceiling conservatively to a
	// topic's complete storage budget.
	maxIndexEntries = 1 << 20
)

// indexEntry maps a relative offset to a byte position in the .log file.
type indexEntry struct {
	relativeOffset uint32
	position       uint64
}

// index is a sparse offset index backed by a file.
// Entries are buffered in memory and flushed to disk on Sync to reduce syscalls.
type index struct {
	file       *os.File
	entries    []indexEntry
	baseOffset uint64
	dirtyFrom  int // entries[dirtyFrom:] not yet written to file
	fileOps    fileOperations
	needsSync  bool
}

// openIndex opens or creates a rebuildable sparse index. The commit log is the
// only authoritative storage, so startup discards the persisted index and
// reconstructs it from verified batches. This also recovers safely from a
// process crash in the middle of an index write.
func openIndex(path string, baseOffset uint64, fileOps fileOperations) (*index, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, filePerm)
	if err != nil {
		return nil, err
	}
	if err := ensurePrivateFile(f); err != nil {
		_ = f.Close()
		return nil, err
	}

	idx := &index{
		file:       f,
		baseOffset: baseOffset,
		fileOps:    fileOps.withDefaults(),
	}
	if err := idx.resetForRecovery(); err != nil {
		f.Close()
		return nil, err
	}
	return idx, nil
}

func (idx *index) resetForRecovery() error {
	if err := idx.file.Truncate(0); err != nil {
		return err
	}
	if _, err := idx.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	idx.entries = nil
	idx.dirtyFrom = 0
	idx.needsSync = true
	return nil
}

// Append buffers a new index entry in memory (no file I/O until Sync).
func (idx *index) Append(offset uint64, position uint64) error {
	if offset < idx.baseOffset ||
		offset-idx.baseOffset > math.MaxUint32 ||
		len(idx.entries) >= maxIndexEntries {
		return ErrInvalidSegmentLayout
	}
	entry := indexEntry{
		relativeOffset: uint32(offset - idx.baseOffset),
		position:       position,
	}
	if len(idx.entries) > 0 {
		previous := idx.entries[len(idx.entries)-1]
		if entry.relativeOffset <= previous.relativeOffset ||
			entry.position <= previous.position {
			return ErrInvalidSegmentLayout
		}
	}
	idx.entries = append(idx.entries, entry)
	return nil
}

// Lookup finds the .log file position for the entry nearest to (and <=) targetOffset.
func (idx *index) Lookup(targetOffset uint64) (position uint64, foundOffset uint64, err error) {
	if len(idx.entries) == 0 {
		return 0, idx.baseOffset, nil
	}

	// Guard against underflow: if targetOffset is before this segment's base,
	// return the start position (caller will scan forward).
	if targetOffset < idx.baseOffset {
		return 0, idx.baseOffset, nil
	}

	rel := uint32(targetOffset - idx.baseOffset)
	i := sort.Search(len(idx.entries), func(j int) bool {
		return idx.entries[j].relativeOffset > rel
	})
	if i == 0 {
		return 0, idx.baseOffset, nil
	}
	e := idx.entries[i-1]
	return e.position, idx.baseOffset + uint64(e.relativeOffset), nil
}

// Sync flushes buffered index entries to file and fsyncs.
func (idx *index) Sync() error {
	dirty := idx.entries[idx.dirtyFrom:]
	if len(dirty) == 0 && !idx.needsSync {
		return nil
	}
	writeOffset := int64(idx.dirtyFrom) * indexEntrySize
	if len(dirty) > 0 {
		if _, err := idx.file.Seek(writeOffset, io.SeekStart); err != nil {
			return err
		}

		buf := make([]byte, len(dirty)*indexEntrySize)
		for i, e := range dirty {
			off := i * indexEntrySize
			binary.BigEndian.PutUint32(buf[off:], e.relativeOffset)
			binary.BigEndian.PutUint64(buf[off+4:], e.position)
		}

		n, err := idx.fileOps.write(idx.file, buf)
		if err == nil && n != len(buf) {
			err = io.ErrShortWrite
		}
		if err != nil {
			// Truncate back to the last known-good position to avoid partial entries on disk.
			return errors.Join(err, idx.rollback(writeOffset))
		}
	}
	if err := idx.fileOps.sync(idx.file); err != nil {
		if len(dirty) > 0 {
			return errors.Join(err, idx.rollback(writeOffset))
		}
		return err
	}
	idx.dirtyFrom = len(idx.entries)
	idx.needsSync = false
	return nil
}

func (idx *index) rollback(size int64) error {
	truncateErr := idx.file.Truncate(size)
	_, seekErr := idx.file.Seek(size, 0)
	idx.needsSync = true
	return errors.Join(truncateErr, seekErr)
}

// Close flushes and closes the index file.
func (idx *index) Close() error {
	if err := idx.Sync(); err != nil {
		idx.file.Close()
		return err
	}
	return idx.file.Close()
}
