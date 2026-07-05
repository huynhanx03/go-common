package forge

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/locks"
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
	mu            locks.RWSpinLocker
	dir           string
	segments      []*segment
	activeSegment *segment
	nextOffset    atomic.Uint64
	config        Config
	closed        bool
}

// NewCommitLog opens or creates a commit log in the given directory.
func NewCommitLog(dir string, opts ...Option) (*CommitLog, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}

	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, fmt.Errorf("forge: mkdir %s: %w", dir, err)
	}

	// Recover incomplete compact/merge operations from a prior crash.
	recoverStaging(dir)

	cl := &CommitLog{mu: locks.NewRWSpinLock(), dir: dir, config: cfg}
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

	// Seal all historical segments for zero-copy mmap reads.
	for _, seg := range cl.segments[:len(cl.segments)-1] {
		seg.seal()
	}

	return cl, nil
}

// loadSegments discovers and opens existing .log files in the directory.
func (cl *CommitLog) loadSegments() error {
	entries, err := os.ReadDir(cl.dir)
	if err != nil {
		return err
	}

	var baseOffsets []uint64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), extLog) {
			continue
		}
		name := strings.TrimSuffix(e.Name(), extLog)
		off, err := strconv.ParseUint(name, 10, 64)
		if err != nil {
			continue
		}
		baseOffsets = append(baseOffsets, off)
	}

	sort.Slice(baseOffsets, func(i, j int) bool { return baseOffsets[i] < baseOffsets[j] })

	for _, bo := range baseOffsets {
		seg, err := openSegment(cl.dir, bo, cl.config)
		if err != nil {
			return fmt.Errorf("forge: load segment %d: %w", bo, err)
		}
		cl.segments = append(cl.segments, seg)
	}
	return nil
}

// newSegment creates and appends a new segment with the given base offset.
func (cl *CommitLog) newSegment(baseOffset uint64) error {
	seg, err := openSegment(cl.dir, baseOffset, cl.config)
	if err != nil {
		return err
	}
	cl.segments = append(cl.segments, seg)
	cl.activeSegment = seg
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

	if len(batch.Records) > maxRecordsPerBatch {
		return 0, ErrBatchTooLarge
	}

	// Assign offsets.
	base := cl.nextOffset.Load()
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

	// Roll segment if full — seal old one for mmap reads.
	if cl.activeSegment.IsFull() {
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
		return 0, err
	}

	// Return buffer to pool after segment.Write has copied data to kernel.
	*dstPtr = encoded[:0]
	encodeDstPool.Put(dstPtr)

	cl.nextOffset.Add(uint64(batch.RecordCount))
	return base, nil
}

// Read returns batches starting at offset, up to maxBytes of log data.
func (cl *CommitLog) Read(offset uint64, maxBytes int) ([]*RecordBatch, error) {
	cl.mu.RLock()
	defer cl.mu.RUnlock()

	if cl.closed {
		return nil, ErrClosed
	}
	if len(cl.segments) == 0 {
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

	var keep []*segment
	for _, seg := range cl.segments {
		if seg.nextOffset <= offset && seg != cl.activeSegment {
			if err := seg.Remove(); err != nil {
				return err
			}
		} else {
			keep = append(keep, seg)
		}
	}
	cl.segments = keep
	return nil
}

// EnforceRetention deletes segments older than retention time or over size limit.
func (cl *CommitLog) EnforceRetention() error {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	cutoff := time.Now().UnixNano() - int64(cl.config.RetentionTime)

	// Size-based: calculate total size.
	var totalSize int64
	for _, seg := range cl.segments {
		totalSize += seg.size
	}

	var keep []*segment
	for _, seg := range cl.segments {
		isActive := seg == cl.activeSegment
		expired := seg.created < cutoff
		overSize := totalSize > cl.config.RetentionBytes

		if !isActive && (expired || overSize) {
			totalSize -= seg.size
			if err := seg.Remove(); err != nil {
				return err
			}
		} else {
			keep = append(keep, seg)
		}
	}
	cl.segments = keep
	return nil
}

// Close flushes and closes all segments.
func (cl *CommitLog) Close() error {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	if cl.closed {
		return nil
	}
	cl.closed = true

	var firstErr error
	for _, seg := range cl.segments {
		if err := seg.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
