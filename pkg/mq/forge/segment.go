package forge

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const segmentNameFmt = "%020d" // 20-digit zero-padded offset

// segment represents a pair of .log + .idx files for a range of offsets.
type segment struct {
	mu         sync.RWMutex
	logFile    *os.File
	index      *index
	baseOffset uint64
	size       int64  // current .log file size in bytes
	created    int64  // creation time (unix nanos)
	nextOffset uint64 // next expected offset in this segment
	config     Config

	bytesSinceIndex  int // bytes written since last index entry
	batchesSinceSync int // batches written since last fsync

	mmapData []byte // mmap'd read source for sealed segments (nil for active segment)
}

// openSegment opens an existing segment or creates a new one.
func openSegment(dir string, baseOffset uint64, cfg Config) (*segment, error) {
	logPath := filepath.Join(dir, fmt.Sprintf(segmentNameFmt+extLog, baseOffset))
	idxPath := filepath.Join(dir, fmt.Sprintf(segmentNameFmt+extIndex, baseOffset))

	logFile, err := os.OpenFile(logPath, os.O_RDWR|os.O_CREATE|os.O_APPEND, filePerm)
	if err != nil {
		return nil, fmt.Errorf("forge: open log %s: %w", logPath, err)
	}
	if err := ensurePrivateFile(logFile); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("forge: protect log %s: %w", logPath, err)
	}

	info, err := logFile.Stat()
	if err != nil {
		logFile.Close()
		return nil, err
	}

	idx, err := openIndex(idxPath, baseOffset, cfg.fileOps)
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

// recover scans the authoritative .log sequentially to determine nextOffset
// and rebuild the disposable sparse index. It allocates at most one bounded
// encoded batch at a time.
// It truncates only an incomplete crash tail. A complete batch with a bad
// checksum or malformed record terminates startup with CorruptionError.
func (s *segment) recover() error {
	s.nextOffset = s.baseOffset
	s.bytesSinceIndex = 0
	position := int64(0)
	for position < s.size {
		remaining := s.size - position
		if remaining < batchLengthPrefixSize {
			if err := s.truncateIncompleteTail(position); err != nil {
				return err
			}
			break
		}

		var header [batchLengthPrefixSize]byte
		if _, err := io.ReadFull(
			io.NewSectionReader(s.logFile, position, batchLengthPrefixSize),
			header[:],
		); err != nil {
			return fmt.Errorf("forge: recover segment header: %w", err)
		}
		batchSize, err := declaredBatchSize(header[:])
		if err != nil {
			return &CorruptionError{
				SegmentBase: s.baseOffset,
				ByteOffset:  position,
				Cause:       err,
			}
		}
		if int64(batchSize) > remaining {
			if err := s.truncateIncompleteTail(position); err != nil {
				return err
			}
			break
		}

		encoded := make([]byte, batchSize)
		if _, err := io.ReadFull(
			io.NewSectionReader(s.logFile, position, int64(batchSize)),
			encoded,
		); err != nil {
			return fmt.Errorf("forge: recover segment batch: %w", err)
		}
		batch, err := decodeBatchBorrowed(encoded)
		if err != nil ||
			batch.BaseOffset != s.nextOffset ||
			!s.canContainBatch(batch.BaseOffset, uint64(batch.RecordCount)) {
			if err == nil {
				err = ErrInvalidSegmentLayout
			}
			return &CorruptionError{
				SegmentBase: s.baseOffset,
				ByteOffset:  position,
				Cause:       err,
			}
		}

		if s.shouldIndexBatch(batchSize) {
			if err := s.index.Append(batch.BaseOffset, uint64(position)); err != nil {
				return &CorruptionError{
					SegmentBase: s.baseOffset,
					ByteOffset:  position,
					Cause:       err,
				}
			}
			s.bytesSinceIndex = 0
		}
		s.bytesSinceIndex += batchSize
		s.nextOffset = batch.BaseOffset + uint64(batch.RecordCount)
		position += int64(batchSize)
	}

	// Persist the authoritative log state before the rebuilt index so an index
	// entry is never more durable than the batch it references.
	if err := s.config.fileOps.sync(s.logFile); err != nil {
		return fmt.Errorf("forge: sync recovered segment: %w", err)
	}
	if err := s.index.Sync(); err != nil {
		return fmt.Errorf("forge: sync rebuilt index: %w", err)
	}
	return nil
}

func (s *segment) truncateIncompleteTail(validEnd int64) error {
	if err := s.logFile.Truncate(validEnd); err != nil {
		return fmt.Errorf("forge: truncate incomplete crash tail: %w", err)
	}
	s.size = validEnd
	return nil
}

// seal mmaps the .log file as a read source when a segment becomes read-only.
func (s *segment) seal() {
	if s.size == 0 || s.size > int64(^uint(0)>>1) || s.mmapData != nil {
		return
	}
	data, err := mapReadOnly(s.logFile, int(s.size))
	if err != nil {
		return // non-fatal: fall back to ReadAt
	}
	s.mmapData = data
}

// Append writes an encoded batch to the .log and updates the index.
func (s *segment) Append(encoded []byte, batch *RecordBatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if batch == nil ||
		batch.BaseOffset != s.nextOffset ||
		!s.canContainBatch(batch.BaseOffset, uint64(batch.RecordCount)) {
		return ErrInvalidSegmentLayout
	}

	position := uint64(s.size)
	shouldIndex := s.shouldIndexBatch(len(encoded))

	n, err := s.config.fileOps.write(s.logFile, encoded)
	if err == nil && n != len(encoded) {
		err = io.ErrShortWrite
	}
	if err != nil {
		rollbackErr := s.logFile.Truncate(int64(position))
		if rollbackErr != nil {
			return errors.Join(
				ErrInvalidSegmentLayout,
				fmt.Errorf("forge: segment write: %w", err),
				fmt.Errorf("forge: rollback partial write: %w", rollbackErr),
			)
		}
		return fmt.Errorf("forge: segment write: %w", err)
	}

	// Write index entry AFTER successful data write to avoid dangling entries.
	var indexErr error
	if shouldIndex {
		indexErr = s.index.Append(batch.BaseOffset, position)
		if indexErr == nil {
			s.bytesSinceIndex = 0
		}
	}

	s.size += int64(n)
	s.bytesSinceIndex += n
	s.nextOffset = batch.BaseOffset + uint64(batch.RecordCount)
	s.batchesSinceSync++
	if indexErr != nil {
		return &AcknowledgmentError{
			Level:    AckAppend,
			Appended: true,
			Cause:    indexErr,
		}
	}

	if s.config.FsyncEvery > 0 && s.batchesSinceSync >= s.config.FsyncEvery {
		if err := s.config.fileOps.sync(s.logFile); err != nil {
			return &AcknowledgmentError{Level: AckFsync, Appended: true, Cause: err}
		}
		if err := s.index.Sync(); err != nil {
			return &AcknowledgmentError{Level: AckFsync, Appended: true, Cause: err}
		}
		s.batchesSinceSync = 0
	}

	return nil
}

// shouldIndexBatch keeps the forward scan from any sparse-index entry bounded
// by IndexInterval plus one encoded batch. Indexing before a batch that would
// cross the interval is important: otherwise a maximum-sized batch could be
// cut by the caller's read budget and make a consumer unable to advance.
func (s *segment) shouldIndexBatch(batchBytes int) bool {
	if len(s.index.entries) == 0 || s.bytesSinceIndex >= s.config.IndexInterval {
		return true
	}
	return batchBytes > s.config.IndexInterval-s.bytesSinceIndex
}

func (s *segment) Sync() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Sealed segments are immutable. Once their pending batches have been
	// synced, repeating fsync on every AckFsync only adds one syscall per
	// historical segment. Dirty predecessors are still visited and synced by
	// CommitLog.Sync, which preserves contiguous recovery when a roll happened
	// after an AckAppend write.
	if s.batchesSinceSync == 0 {
		return nil
	}
	if err := s.config.fileOps.sync(s.logFile); err != nil {
		return err
	}
	if err := s.index.Sync(); err != nil {
		return err
	}
	s.batchesSinceSync = 0
	return nil
}

// ReadFrom scans from the nearest sparse-index entry and returns complete
// batches intersecting offset, up to maxBytes of returned encoded data. Scan
// bytes before offset do not consume the return budget. Each batch is copied
// into caller-owned memory, including when the segment is mmap-backed.
func (s *segment) ReadFrom(offset uint64, maxBytes int) ([]*RecordBatch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if maxBytes <= 0 || maxBytes > maxEncodedBatchBytes {
		return nil, fmt.Errorf("%w: read byte limit", ErrInvalidConfig)
	}

	pos, indexedOffset, err := s.index.Lookup(offset)
	if err != nil {
		return nil, err
	}
	if pos > uint64(s.size) {
		return nil, s.corruptionAt(s.size, ErrInvalidSegmentLayout)
	}

	batches := make([]*RecordBatch, 0, 4)
	returnedBytes := 0
	var returnedOwnedBytes int64
	position := int64(pos)
	expectedOffset := indexedOffset
	for position < s.size {
		header, err := s.readOwned(position, batchLengthPrefixSize)
		if err != nil {
			return nil, s.corruptionAt(position, err)
		}
		batchSize, err := declaredBatchSize(header)
		if err != nil || int64(batchSize) > s.size-position {
			if err == nil {
				err = ErrIncompleteBatch
			}
			return nil, s.corruptionAt(position, err)
		}
		encoded, err := s.readOwned(position, batchSize)
		if err != nil {
			return nil, s.corruptionAt(position, err)
		}
		inspected, err := inspectBatch(encoded)
		if err != nil {
			return nil, s.corruptionAt(position, err)
		}
		if inspected.baseOffset != expectedOffset || inspected.baseOffset < s.baseOffset ||
			!s.canContainBatch(inspected.baseOffset, uint64(inspected.recordCount)) {
			return nil, s.corruptionAt(position, ErrInvalidSegmentLayout)
		}
		batchEnd := inspected.baseOffset + uint64(inspected.recordCount)
		expectedOffset = batchEnd
		if batchEnd > offset {
			if batchSize > maxBytes-returnedBytes {
				if len(batches) == 0 {
					return nil, fmt.Errorf("%w: complete batch exceeds read byte limit", ErrInvalidConfig)
				}
				break
			}
			batch, decodeErr := decodeInspectedBatch(inspected, false)
			if decodeErr != nil {
				return nil, s.corruptionAt(position, decodeErr)
			}
			ownedBytes := readOwnedFootprint(batchSize, batch)
			if ownedBytes > maximumReadOwnedBytes-returnedOwnedBytes {
				if len(batches) == 0 {
					return nil, ErrResourceLimit
				}
				break
			}
			batches = append(batches, batch)
			returnedBytes += batchSize
			returnedOwnedBytes += ownedBytes
		}
		position += int64(batchSize)
		if returnedBytes == maxBytes {
			break
		}
	}
	return batches, nil
}

func readOwnedFootprint(encodedBytes int, batch *RecordBatch) int64 {
	if encodedBytes < 0 || batch == nil {
		return maximumReadOwnedBytes + 1
	}
	total := int64(encodedBytes) + 256
	add := func(value int64) bool {
		if value < 0 || total > maximumReadOwnedBytes-value {
			total = maximumReadOwnedBytes + 1
			return false
		}
		total += value
		return true
	}
	if !add(int64(len(batch.Records)) * readRecordOverheadBytes) {
		return total
	}
	for index := range batch.Records {
		record := &batch.Records[index]
		if !add(int64(len(record.Key))) || !add(int64(len(record.Value))) ||
			!add(int64(len(record.Headers))*readHeaderOverheadBytes) {
			return total
		}
		for headerIndex := range record.Headers {
			header := &record.Headers[headerIndex]
			if !add(int64(len(header.Key))) || !add(int64(len(header.Value))) {
				return total
			}
		}
	}
	return total
}

func (s *segment) readOwned(position int64, size int) ([]byte, error) {
	if position < 0 || size <= 0 || position > s.size-int64(size) {
		return nil, ErrIncompleteBatch
	}
	owned := make([]byte, size)
	if s.mmapData != nil {
		copy(owned, s.mmapData[position:position+int64(size)])
		return owned, nil
	}
	if _, err := io.ReadFull(io.NewSectionReader(s.logFile, position, int64(size)), owned); err != nil {
		return nil, err
	}
	return owned, nil
}

func (s *segment) corruptionAt(position int64, cause error) error {
	return &CorruptionError{
		SegmentBase: s.baseOffset,
		ByteOffset:  position,
		Cause:       cause,
	}
}

// IsFull returns true if the segment has reached its size or age limit.
func (s *segment) IsFull() bool {
	if s.size >= s.config.MaxSegmentBytes || len(s.index.entries) >= maxIndexEntries {
		return true
	}
	if s.config.MaxSegmentAge > 0 {
		return time.Since(time.Unix(0, s.created)) >= s.config.MaxSegmentAge
	}
	return false
}

func (s *segment) canContainBatch(offset, count uint64) bool {
	// nextOffset is one past the last record and must remain representable.
	if count == 0 || offset < s.baseOffset || offset > math.MaxUint64-count {
		return false
	}
	lastOffset := offset + count - 1
	return lastOffset-s.baseOffset <= math.MaxUint32
}

// Close flushes and closes the segment's log and index files.
func (s *segment) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var firstErr error
	if s.mmapData != nil {
		if err := unmapReadOnly(s.mmapData); err != nil && firstErr == nil {
			firstErr = err
		}
		s.mmapData = nil
	}
	if err := s.config.fileOps.sync(s.logFile); err != nil && firstErr == nil {
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
	// Remove the rebuildable index first. A crash before unlinking the log then
	// leaves a valid log whose index is recreated during recovery; the reverse
	// order can leave an orphan index that has no authoritative data file.
	if err := s.config.fileOps.remove(idxPath); err != nil && !os.IsNotExist(err) {
		firstErr = err
	}
	if firstErr != nil {
		return firstErr
	}
	if err := s.config.fileOps.remove(logPath); err != nil && !os.IsNotExist(err) {
		firstErr = err
	}
	return firstErr
}
