package forge

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// encodeDstPool reuses destination buffers for EncodeBatch to eliminate per-Append allocation.
var encodeDstPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, defaultRecordBufCap)
		return &b
	},
}

// CommitLog is an append-only log composed of rolling segments.
// It is the core storage engine for Forge MQ.
type CommitLog struct {
	mu            sync.RWMutex
	dir           string
	segments      []*segment
	activeSegment *segment
	nextOffset    atomic.Uint64
	config        Config
	closed        bool
	dirDirty      bool
	storageErr    error
	closeErr      error
}

// NewCommitLog opens or creates a commit log in the given directory.
func NewCommitLog(dir string, opts ...Option) (*CommitLog, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: commit-log directory", ErrInvalidConfig)
	}
	cfg := defaultConfig()
	for _, o := range opts {
		if err := applyOption(o, &cfg); err != nil {
			return nil, err
		}
	}
	cfg.fileOps = cfg.fileOps.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	if err := ensurePrivateDirectory(dir); err != nil {
		return nil, fmt.Errorf("forge: mkdir %s: %w", dir, err)
	}
	if err := cfg.fileOps.syncDir(filepath.Dir(dir)); err != nil {
		return nil, fmt.Errorf("forge: sync commit-log parent: %w", err)
	}

	cl := &CommitLog{dir: dir, config: cfg, dirDirty: true}
	if err := cl.loadSegments(); err != nil {
		return nil, err
	}

	// If no segments exist, create the first one.
	if len(cl.segments) == 0 {
		if err := cl.newSegment(0); err != nil {
			return nil, err
		}
	}

	cl.activeSegment = cl.segments[len(cl.segments)-1]
	cl.nextOffset.Store(cl.activeSegment.nextOffset)

	// Seal all historical segments for mmap-backed reads with owned outputs.
	for _, seg := range cl.segments[:len(cl.segments)-1] {
		seg.seal()
	}

	return cl, nil
}

// loadSegments discovers and opens existing .log files in the directory.
func (cl *CommitLog) loadSegments() error {
	var baseOffsets []uint64
	logOffsets := make(map[uint64]struct{})
	var totalLogBytes int64
	if err := forEachDirectoryEntry(cl.dir, func(e os.DirEntry) error {
		if e.IsDir() {
			return nil
		}
		var suffix string
		switch {
		case strings.HasSuffix(e.Name(), extLog):
			suffix = extLog
		case strings.HasSuffix(e.Name(), extIndex):
			suffix = extIndex
		default:
			return nil
		}
		offset, err := canonicalSegmentOffset(e.Name(), suffix)
		if err != nil {
			return err
		}
		if suffix == extLog {
			if len(baseOffsets) >= maxSegmentsPerTopic {
				return ErrStorageFull
			}
			info, err := e.Info()
			if err != nil {
				return err
			}
			if info.Size() < 0 || totalLogBytes > cl.config.MaxStorageBytes-info.Size() {
				return ErrStorageFull
			}
			totalLogBytes += info.Size()
			logOffsets[offset] = struct{}{}
			baseOffsets = append(baseOffsets, offset)
		}
		return nil
	}); err != nil {
		return err
	}

	sort.Slice(baseOffsets, func(i, j int) bool { return baseOffsets[i] < baseOffsets[j] })

	for _, bo := range baseOffsets {
		seg, err := openSegment(cl.dir, bo, cl.config)
		if err != nil {
			cl.closeLoadedSegments()
			return fmt.Errorf("forge: load segment %d: %w", bo, err)
		}
		if len(cl.segments) > 0 {
			previous := cl.segments[len(cl.segments)-1]
			if previous.nextOffset != bo {
				_ = seg.Close()
				cl.closeLoadedSegments()
				return fmt.Errorf(
					"%w: segment %d follows offset %d",
					ErrInvalidSegmentLayout,
					bo,
					previous.nextOffset,
				)
			}
		}
		cl.segments = append(cl.segments, seg)
	}

	// Indexes are disposable. Remove indexes without an authoritative log so a
	// crash in an older retention implementation cannot block startup forever.
	removedOrphan := false
	if err := forEachDirectoryEntry(cl.dir, func(entry os.DirEntry) error {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), extIndex) {
			return nil
		}
		offset, err := canonicalSegmentOffset(entry.Name(), extIndex)
		if err != nil {
			return err
		}
		if _, exists := logOffsets[offset]; exists {
			return nil
		}
		if err := cl.config.fileOps.remove(filepath.Join(cl.dir, entry.Name())); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return err
		}
		removedOrphan = true
		return nil
	}); err != nil {
		cl.closeLoadedSegments()
		return err
	}
	if removedOrphan {
		if err := cl.config.fileOps.syncDir(cl.dir); err != nil {
			cl.closeLoadedSegments()
			return err
		}
	}
	return nil
}

func forEachDirectoryEntry(path string, visit func(os.DirEntry) error) error {
	return forEachDirectoryEntryLimit(path, maximumDirectoryEntries, visit)
}

func forEachDirectoryEntryLimit(
	path string,
	maximum int,
	visit func(os.DirEntry) error,
) error {
	if maximum <= 0 || visit == nil {
		return ErrInvalidConfig
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	visited := 0
	for {
		entries, readErr := directory.ReadDir(maxSegmentsPerTopic * 2)
		for _, entry := range entries {
			if visited >= maximum {
				return ErrResourceLimit
			}
			visited++
			if err := visit(entry); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func canonicalSegmentOffset(filename, suffix string) (uint64, error) {
	name := strings.TrimSuffix(filename, suffix)
	if len(name) != 20 {
		return 0, fmt.Errorf("%w: noncanonical filename", ErrInvalidSegmentLayout)
	}
	offset, err := strconv.ParseUint(name, 10, 64)
	if err != nil || fmt.Sprintf(segmentNameFmt, offset) != name {
		return 0, fmt.Errorf("%w: noncanonical filename", ErrInvalidSegmentLayout)
	}
	return offset, nil
}

func (cl *CommitLog) closeLoadedSegments() {
	for _, segment := range cl.segments {
		_ = segment.Close()
	}
	cl.segments = nil
}

// newSegment creates and appends a new segment with the given base offset.
func (cl *CommitLog) newSegment(baseOffset uint64) error {
	if len(cl.segments) >= maxSegmentsPerTopic {
		return ErrStorageFull
	}
	seg, err := openSegment(cl.dir, baseOffset, cl.config)
	if err != nil {
		return err
	}
	cl.segments = append(cl.segments, seg)
	cl.activeSegment = seg
	cl.dirDirty = true
	return nil
}

// Append writes a RecordBatch to the active segment, rolling if necessary.
// Returns the base offset assigned to the batch.
func (cl *CommitLog) Append(batch *RecordBatch) (uint64, error) {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	if cl.closed {
		return 0, ErrClosed
	}
	if cl.storageErr != nil {
		return 0, cl.storageErr
	}
	if batch == nil || len(batch.Records) == 0 {
		return 0, ErrEmptyBatch
	}

	if len(batch.Records) > maxRecordsPerBatch {
		return 0, ErrBatchTooLarge
	}

	// Assign offsets.
	base := cl.nextOffset.Load()
	recordCount := uint64(len(batch.Records))
	if base > math.MaxUint64-recordCount {
		return 0, ErrInvalidSegmentLayout
	}
	for _, record := range batch.Records {
		recordSize, err := encodedRecordSize(record)
		if err != nil || recordSize > cl.config.MaxMessageSize {
			return 0, ErrMessageTooLarge
		}
	}
	batch.BaseOffset = base
	for i := range batch.Records {
		batch.Records[i].OffsetDelta = int64(i)
	}
	batch.RecordCount = uint16(len(batch.Records))

	// Use pooled dst buffer to avoid allocation per Append.
	dstPtr := encodeDstPool.Get().(*[]byte)
	encoded, err := EncodeBatch(batch, (*dstPtr)[:0])
	if err != nil {
		*dstPtr = (*dstPtr)[:0]
		encodeDstPool.Put(dstPtr)
		return 0, err
	}
	if cl.totalLogBytesLocked() > cl.config.MaxStorageBytes-int64(len(encoded)) {
		*dstPtr = encoded[:0]
		encodeDstPool.Put(dstPtr)
		return 0, ErrStorageFull
	}

	// Roll segment if full — seal old one for mmap reads.
	if cl.activeSegment.IsFull() ||
		!cl.activeSegment.canContainBatch(base, recordCount) {
		cl.activeSegment.seal()
		if err := cl.newSegment(base); err != nil {
			*dstPtr = encoded[:0]
			encodeDstPool.Put(dstPtr)
			return 0, err
		}
	}

	if err := cl.activeSegment.Append(encoded, batch); err != nil {
		*dstPtr = encoded[:0]
		encodeDstPool.Put(dstPtr)
		var acknowledgmentError *AcknowledgmentError
		if errors.As(err, &acknowledgmentError) && acknowledgmentError.Appended {
			cl.nextOffset.Add(uint64(batch.RecordCount))
			return base, err
		}
		if errors.Is(err, ErrInvalidSegmentLayout) {
			return 0, cl.poisonStorageLocked(err)
		}
		return 0, err
	}

	// Return buffer to pool after segment.Write has copied data to kernel.
	*dstPtr = encoded[:0]
	encodeDstPool.Put(dstPtr)

	cl.nextOffset.Add(uint64(batch.RecordCount))
	return base, nil
}

func (cl *CommitLog) totalLogBytesLocked() int64 {
	var total int64
	for _, segment := range cl.segments {
		total += segment.size
	}
	return total
}

// Sync fsyncs every dirty segment and newly created directory entry. Clean
// historical segments are skipped, while dirty predecessors remain part of
// the durability barrier so recovery cannot observe a durable offset gap.
func (cl *CommitLog) Sync() error {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.closed {
		return ErrClosed
	}
	if cl.storageErr != nil {
		return cl.storageErr
	}
	for _, segment := range cl.segments {
		if err := segment.Sync(); err != nil {
			return err
		}
	}
	if cl.dirDirty {
		if err := cl.config.fileOps.syncDir(cl.dir); err != nil {
			return err
		}
		cl.dirDirty = false
	}
	return nil
}

// Read returns batches starting at offset, up to maxBytes of log data. It
// returns ErrOffsetNotFound when offset is outside the retained range; the
// current next offset is a valid empty read.
func (cl *CommitLog) Read(offset uint64, maxBytes int) ([]*RecordBatch, error) {
	cl.mu.RLock()
	defer cl.mu.RUnlock()

	if maxBytes <= 0 || maxBytes > maxEncodedBatchBytes {
		return nil, fmt.Errorf("%w: read byte limit", ErrInvalidConfig)
	}
	if cl.closed {
		return nil, ErrClosed
	}
	if cl.storageErr != nil {
		return nil, cl.storageErr
	}
	if len(cl.segments) == 0 {
		return nil, nil
	}
	oldest := cl.oldestOffsetLocked()
	newest := cl.nextOffset.Load()
	if offset < oldest || offset > newest {
		return nil, ErrOffsetNotFound
	}
	if offset == newest {
		return nil, nil
	}

	seg := cl.findSegment(offset)
	if seg == nil {
		return nil, ErrOffsetNotFound
	}

	return seg.ReadFrom(offset, maxBytes)
}

// findSegment returns the segment that should contain the given offset.
// Returns nil if offset is beyond the newest offset (no data yet).
func (cl *CommitLog) findSegment(offset uint64) *segment {
	n := len(cl.segments)
	i := sort.Search(n, func(j int) bool {
		return cl.segments[j].baseOffset > offset
	})
	if i == 0 {
		// Offset is before or within the first segment.
		// If offset is below the first segment's range after retention,
		// return the first segment (it will return empty results).
		return cl.segments[0]
	}
	seg := cl.segments[i-1]
	// If offset is beyond this segment's written data, still return it —
	// ReadFrom will handle returning empty results.
	return seg
}

// OldestOffset returns the lowest available offset.
func (cl *CommitLog) OldestOffset() uint64 {
	cl.mu.RLock()
	defer cl.mu.RUnlock()
	return cl.oldestOffsetLocked()
}

func (cl *CommitLog) oldestOffsetLocked() uint64 {
	if len(cl.segments) == 0 {
		return 0
	}
	return cl.segments[0].baseOffset
}

// NewestOffset returns the next offset that will be assigned.
func (cl *CommitLog) NewestOffset() uint64 {
	return cl.nextOffset.Load()
}

// DeleteBefore removes segments whose entire range is below the given offset.
func (cl *CommitLog) DeleteBefore(offset uint64) error {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.closed {
		return ErrClosed
	}
	if cl.storageErr != nil {
		return cl.storageErr
	}

	keep := make([]*segment, 0, len(cl.segments))
	removed := false
	for index, seg := range cl.segments {
		if seg.nextOffset <= offset && seg != cl.activeSegment {
			if err := seg.Remove(); err != nil {
				// Remove closes the segment before unlinking it. Exclude a
				// partially removed segment from the live slice so subsequent
				// reads never target closed files; a restart may safely discover
				// any leftover fully-consumed file again.
				keep = append(keep, cl.segments[index+1:]...)
				cl.segments = keep
				return cl.poisonStorageLocked(errors.Join(
					err,
					cl.config.fileOps.syncDir(cl.dir),
				))
			}
			removed = true
		} else {
			keep = append(keep, seg)
		}
	}
	cl.segments = keep
	if removed {
		if err := cl.config.fileOps.syncDir(cl.dir); err != nil {
			return cl.poisonStorageLocked(err)
		}
	}
	return nil
}

// EnforceRetention deletes segments older than retention time or over size limit.
func (cl *CommitLog) EnforceRetention() error {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.closed {
		return ErrClosed
	}
	if cl.storageErr != nil {
		return cl.storageErr
	}
	return cl.enforceRetentionLocked(0, false)
}

// EnforceRetentionBefore applies time/size retention only to sealed segments
// whose records are entirely below the slowest committed consumer offset.
func (cl *CommitLog) EnforceRetentionBefore(offset uint64) error {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.closed {
		return ErrClosed
	}
	if cl.storageErr != nil {
		return cl.storageErr
	}
	return cl.enforceRetentionLocked(offset, true)
}

func (cl *CommitLog) enforceRetentionLocked(protectedOffset uint64, protectConsumers bool) error {
	cutoff := time.Now().UnixNano() - int64(cl.config.RetentionTime)

	// Size-based: calculate total size.
	var totalSize int64
	for _, seg := range cl.segments {
		totalSize += seg.size
	}

	keep := make([]*segment, 0, len(cl.segments))
	removed := false
	for index, seg := range cl.segments {
		isActive := seg == cl.activeSegment
		expired := seg.created < cutoff
		overSize := totalSize > cl.config.RetentionBytes
		fullyConsumed := !protectConsumers || seg.nextOffset <= protectedOffset

		if !isActive && fullyConsumed && (expired || overSize) {
			totalSize -= seg.size
			if err := seg.Remove(); err != nil {
				keep = append(keep, cl.segments[index+1:]...)
				cl.segments = keep
				return cl.poisonStorageLocked(errors.Join(
					err,
					cl.config.fileOps.syncDir(cl.dir),
				))
			}
			removed = true
		} else {
			keep = append(keep, seg)
		}
	}
	cl.segments = keep
	if removed {
		if err := cl.config.fileOps.syncDir(cl.dir); err != nil {
			return cl.poisonStorageLocked(err)
		}
	}
	return nil
}

func (cl *CommitLog) poisonStorageLocked(cause error) error {
	if cl.storageErr == nil {
		cl.storageErr = errors.Join(ErrStorageUnavailable, cause)
	}
	return cl.storageErr
}

// Close flushes and closes all segments.
func (cl *CommitLog) Close() error {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	if cl.closed {
		return cl.closeErr
	}
	cl.closed = true

	var firstErr error
	for _, seg := range cl.segments {
		if err := seg.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := cl.config.fileOps.syncDir(cl.dir); err != nil && firstErr == nil {
		firstErr = err
	}
	cl.dirDirty = false
	cl.closeErr = firstErr
	return cl.closeErr
}
