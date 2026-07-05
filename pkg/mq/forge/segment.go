package forge

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/locks"
)

const segmentNameFmt = "%020d" // 20-digit zero-padded offset

// segment represents a pair of .log + .idx files for a range of offsets.
type segment struct {
	mu         locks.RWSpinLocker
	logFile    *os.File
	index      *index
	baseOffset uint64
	size       int64  // current .log file size in bytes
	created    int64  // creation time (unix nanos)
	nextOffset uint64 // next expected offset in this segment
	config     Config

	bytesSinceIndex  int // bytes written since last index entry
	batchesSinceSync int // batches written since last fsync

	mmapData []byte // mmap'd .log for zero-copy reads (nil for active segment)
}

// openSegment opens an existing segment or creates a new one.
func openSegment(dir string, baseOffset uint64, cfg Config) (*segment, error) {
	logPath := filepath.Join(dir, fmt.Sprintf(segmentNameFmt+extLog, baseOffset))
	idxPath := filepath.Join(dir, fmt.Sprintf(segmentNameFmt+extIndex, baseOffset))

	logFile, err := os.OpenFile(logPath, os.O_RDWR|os.O_CREATE|os.O_APPEND, filePerm)
	if err != nil {
		return nil, fmt.Errorf("forge: open log %s: %w", logPath, err)
	}

	info, err := logFile.Stat()
	if err != nil {
		logFile.Close()
		return nil, err
	}

	idx, err := openIndex(idxPath, baseOffset)
	if err != nil {
		logFile.Close()
		return nil, fmt.Errorf("forge: open index %s: %w", idxPath, err)
	}

	// Use file mod time for existing segments (preserves retention across restarts).
	// For new (empty) segments, use current time.
	created := info.ModTime().UnixNano()
	if info.Size() == 0 {
		created = time.Now().UnixNano()
	}

	s := &segment{
		mu:         locks.NewRWSpinLock(),
		logFile:    logFile,
		index:      idx,
		baseOffset: baseOffset,
		size:       info.Size(),
		created:    created,
		nextOffset: baseOffset,
		config:     cfg,
	}

	if err := s.recover(); err != nil {
		s.Close()
		return nil, err
	}

	return s, nil
}

// recoverChunkSize limits memory during recovery scanning.
const recoverChunkSize = 256 * 1024 // 256 KB

// recover scans the .log from the last index entry to determine nextOffset.
// Reads in chunks to avoid allocating the entire segment into memory.
// Truncates any corrupt trailing bytes to ensure clean appends after crash.
func (s *segment) recover() error {
	if s.size == 0 {
		return nil
	}

	var scanStart int64
	if _, p, ok := s.index.LastEntry(); ok {
		scanStart = int64(p)
	}

	remaining := s.size - scanStart
	if remaining <= 0 {
		return nil
	}

	// validEnd tracks the file position after the last valid batch.
	validEnd := scanStart

	// Small segments: read all at once.
	if remaining <= recoverChunkSize {
		validEnd += s.recoverRange(scanStart, remaining)
	} else {
		// Large segments: read in chunks with independent carry buffer.
		buf := make([]byte, recoverChunkSize)
		var carry []byte
		pos := scanStart

		for pos < s.size {
			carryLen := copy(buf, carry)
			readSize := int64(recoverChunkSize - carryLen)
			if pos+readSize > s.size {
				readSize = s.size - pos
			}

			n, err := s.logFile.ReadAt(buf[carryLen:carryLen+int(readSize)], pos)
			if err != nil && n == 0 {
				break
			}
			data := buf[:carryLen+n]
			pos += int64(n)

			consumed := s.scanBatches(data)
			validEnd += int64(consumed)

			// Independent copy to avoid aliasing buf's backing array.
			tail := data[consumed:]
			carry = make([]byte, len(tail))
			copy(carry, tail)
		}
	}

	// Truncate corrupt tail to prevent embedding garbage before new appends.
	if validEnd < s.size {
		if err := s.logFile.Truncate(validEnd); err != nil {
			return fmt.Errorf("forge: truncate corrupt tail: %w", err)
		}
		s.size = validEnd
	}

	return nil
}

// recoverRange reads a contiguous range and scans for batches.
// Returns the number of valid bytes consumed.
func (s *segment) recoverRange(pos, length int64) int64 {
	buf := make([]byte, length)
	n, err := s.logFile.ReadAt(buf, pos)
	if err != nil && n == 0 {
		return 0
	}
	return int64(s.scanBatches(buf[:n]))
}

// scanBatches iterates over encoded batches in buf and advances s.nextOffset.
// Returns the number of bytes consumed. Stops at first corrupt or incomplete batch.
func (s *segment) scanBatches(buf []byte) int {
	off := 0
	for off < len(buf) {
		batchSize, err := BatchSize(buf[off:])
		if err != nil || off+batchSize > len(buf) {
			break
		}
		batch, err := DecodeBatch(buf[off : off+batchSize])
		if err != nil {
			break
		}
		nextOff := batch.BaseOffset + uint64(batch.RecordCount)
		if nextOff > s.nextOffset {
			s.nextOffset = nextOff
		}
		off += batchSize
	}
	return off
}

// seal mmaps the .log file for zero-copy reads (called when segment becomes read-only).
func (s *segment) seal() {
	if s.size == 0 || s.mmapData != nil {
		return
	}
	data, err := syscall.Mmap(int(s.logFile.Fd()), 0, int(s.size), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return // non-fatal: fall back to ReadAt
	}
	s.mmapData = data
}

// Append writes an encoded batch to the .log and updates the index.
func (s *segment) Append(encoded []byte, batch *RecordBatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	position := uint64(s.size)
	shouldIndex := s.bytesSinceIndex >= s.config.IndexInterval || len(s.index.entries) == 0

	n, err := s.logFile.Write(encoded)
	if err != nil {
		return fmt.Errorf("forge: segment write: %w", err)
	}

	// Write index entry AFTER successful data write to avoid dangling entries.
	if shouldIndex {
		s.index.Append(batch.BaseOffset, position)
		s.bytesSinceIndex = 0
	}

	s.size += int64(n)
	s.bytesSinceIndex += n
	s.nextOffset = batch.BaseOffset + uint64(batch.RecordCount)
	s.batchesSinceSync++

	if s.config.FsyncEvery > 0 && s.batchesSinceSync >= s.config.FsyncEvery {
		if err := s.logFile.Sync(); err != nil {
			return err
		}
		if err := s.index.Sync(); err != nil {
			return err
		}
		s.batchesSinceSync = 0
	}

	return nil
}

// ReadFrom reads batches starting at the given offset, up to maxBytes of data.
// Uses mmap for zero-copy reads on sealed segments, falls back to ReadAt otherwise.
func (s *segment) ReadFrom(offset uint64, maxBytes int) ([]*RecordBatch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pos, _, err := s.index.Lookup(offset)
	if err != nil {
		return nil, err
	}

	var buf []byte
	if s.mmapData != nil {
		// Zero-copy read from mmap.
		end := int64(pos) + int64(maxBytes)
		if end > int64(len(s.mmapData)) {
			end = int64(len(s.mmapData))
		}
		if int64(pos) >= end {
			return nil, nil
		}
		buf = s.mmapData[pos:end]
	} else {
		// Fallback: allocate + ReadAt for active segment.
		remaining := s.size - int64(pos)
		if remaining <= 0 {
			return nil, nil
		}
		if int64(maxBytes) < remaining {
			remaining = int64(maxBytes)
		}
		buf = make([]byte, remaining)
		n, err := s.logFile.ReadAt(buf, int64(pos))
		if err != nil && n == 0 {
			return nil, err
		}
		buf = buf[:n]
	}

	// Pre-allocate with estimated batch count to reduce append growth allocations.
	estimatedBatches := len(buf) / (batchHeaderSize + 64)
	if estimatedBatches < 4 {
		estimatedBatches = 4
	}
	batches := make([]*RecordBatch, 0, estimatedBatches)
	readOff := 0
	for readOff < len(buf) {
		batchSize, err := BatchSize(buf[readOff:])
		if err != nil || readOff+batchSize > len(buf) {
			break
		}
		batch, err := DecodeBatch(buf[readOff : readOff+batchSize])
		if err != nil {
			break
		}
		if batch.BaseOffset+uint64(batch.RecordCount) > offset {
			batches = append(batches, batch)
		}
		readOff += batchSize
	}

	return batches, nil
}

// readRawBytes returns a deep copy of the segment's raw .log data.
// Used for zero-copy merge to avoid decode/re-encode overhead.
func (s *segment) readRawBytes() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.size == 0 {
		return nil, nil
	}

	if s.mmapData != nil {
		raw := make([]byte, len(s.mmapData))
		copy(raw, s.mmapData)
		return raw, nil
	}

	raw := make([]byte, s.size)
	n, err := s.logFile.ReadAt(raw, 0)
	if err != nil && n == 0 {
		return nil, err
	}
	return raw[:n], nil
}

// IsFull returns true if the segment has reached its size or age limit.
func (s *segment) IsFull() bool {
	if s.size >= s.config.MaxSegmentBytes {
		return true
	}
	if s.config.MaxSegmentAge > 0 {
		return time.Now().UnixNano()-s.created >= int64(s.config.MaxSegmentAge)
	}
	return false
}

// Close flushes and closes the segment's log and index files.
func (s *segment) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var firstErr error
	if s.mmapData != nil {
		if err := syscall.Munmap(s.mmapData); err != nil && firstErr == nil {
			firstErr = err
		}
		s.mmapData = nil
	}
	if err := s.logFile.Sync(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.index.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.logFile.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// Remove deletes the segment's .log and .idx files from disk.
func (s *segment) Remove() error {
	logPath := s.logFile.Name()
	idxPath := s.index.file.Name()
	if err := s.Close(); err != nil {
		return err
	}
	var firstErr error
	if err := os.Remove(logPath); err != nil && !os.IsNotExist(err) {
		firstErr = err
	}
	if err := os.Remove(idxPath); err != nil && !os.IsNotExist(err) && firstErr == nil {
		firstErr = err
	}
	return firstErr
}
