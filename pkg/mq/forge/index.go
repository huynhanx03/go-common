package forge

import (
	"encoding/binary"
	"os"
	"sort"
)

const indexEntrySize = 12 // RelativeOffset(4) + Position(8)

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
}

// openIndex opens or creates an index file, loading existing entries into memory.
func openIndex(path string, baseOffset uint64) (*index, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, filePerm)
	if err != nil {
		return nil, err
	}

	idx := &index{
		file:       f,
		baseOffset: baseOffset,
	}

	if err := idx.loadEntries(); err != nil {
		f.Close()
		return nil, err
	}
	idx.dirtyFrom = len(idx.entries) // all loaded entries are already on disk

	return idx, nil
}

// loadEntries reads all index entries from disk into memory.
func (idx *index) loadEntries() error {
	info, err := idx.file.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	if size == 0 {
		return nil
	}

	count := int(size / indexEntrySize)
	buf := make([]byte, count*indexEntrySize)
	if _, err := idx.file.ReadAt(buf, 0); err != nil {
		return err
	}

	idx.entries = make([]indexEntry, count)
	for i := 0; i < count; i++ {
		off := i * indexEntrySize
		idx.entries[i] = indexEntry{
			relativeOffset: binary.BigEndian.Uint32(buf[off:]),
			position:       binary.BigEndian.Uint64(buf[off+4:]),
		}
	}
	return nil
}

// Append buffers a new index entry in memory (no file I/O until Sync).
func (idx *index) Append(offset uint64, position uint64) {
	idx.entries = append(idx.entries, indexEntry{
		relativeOffset: uint32(offset - idx.baseOffset),
		position:       position,
	})
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

// LastEntry returns the last index entry, or false if empty.
func (idx *index) LastEntry() (offset uint64, position uint64, ok bool) {
	if len(idx.entries) == 0 {
		return 0, 0, false
	}
	e := idx.entries[len(idx.entries)-1]
	return idx.baseOffset + uint64(e.relativeOffset), e.position, true
}

// Sync flushes buffered index entries to file and fsyncs.
func (idx *index) Sync() error {
	dirty := idx.entries[idx.dirtyFrom:]
	if len(dirty) == 0 {
		return nil
	}

	buf := make([]byte, len(dirty)*indexEntrySize)
	for i, e := range dirty {
		off := i * indexEntrySize
		binary.BigEndian.PutUint32(buf[off:], e.relativeOffset)
		binary.BigEndian.PutUint64(buf[off+4:], e.position)
	}

	n, err := idx.file.Write(buf)
	if err != nil {
		// Truncate back to the last known-good position to avoid partial entries on disk.
		goodSize := int64(idx.dirtyFrom) * indexEntrySize
		idx.file.Truncate(goodSize)
		idx.file.Seek(goodSize, 0)
		return err
	}
	_ = n
	idx.dirtyFrom = len(idx.entries)
	return idx.file.Sync()
}

// Close flushes and closes the index file.
func (idx *index) Close() error {
	if err := idx.Sync(); err != nil {
		idx.file.Close()
		return err
	}
	return idx.file.Close()
}
