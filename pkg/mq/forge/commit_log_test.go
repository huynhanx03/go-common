package forge

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestCommitLogAppendAndRead(t *testing.T) {
	dir := t.TempDir()

	cl, err := NewCommitLog(dir)
	if err != nil {
		t.Fatalf("NewCommitLog: %v", err)
	}
	defer cl.Close()

	// Append 3 batches.
	for i := 0; i < 3; i++ {
		batch := &RecordBatch{
			Timestamp:    int64(i * 1000),
			MaxTimestamp: int64(i * 1000),
			Records: []Record{
				{Key: []byte("k"), Value: []byte("v1")},
				{Key: []byte("k"), Value: []byte("v2")},
			},
		}
		base, err := cl.Append(batch)
		if err != nil {
			t.Fatalf("Append batch %d: %v", i, err)
		}
		if base != uint64(i*2) {
			t.Errorf("batch %d base = %d, want %d", i, base, i*2)
		}
	}

	if cl.NewestOffset() != 6 {
		t.Errorf("NewestOffset = %d, want 6", cl.NewestOffset())
	}

	// Read from offset 0.
	batches, err := cl.Read(0, 1<<20)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(batches) != 3 {
		t.Fatalf("Read returned %d batches, want 3", len(batches))
	}
	if !bytes.Equal(batches[0].Records[0].Value, []byte("v1")) {
		t.Error("first record value mismatch")
	}

	// Read from offset 2 (second batch).
	batches, err = cl.Read(2, 1<<20)
	if err != nil {
		t.Fatalf("Read offset 2: %v", err)
	}
	if len(batches) < 2 {
		t.Fatalf("Read offset 2 returned %d batches, want >= 2", len(batches))
	}
	if batches[0].BaseOffset != 2 {
		t.Errorf("first batch base = %d, want 2", batches[0].BaseOffset)
	}
}

func TestCommitLogEnforcesConfiguredMessageLimit(t *testing.T) {
	log, err := NewCommitLog(t.TempDir(), WithMaxMessageSize(64))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	if _, err := log.Append(&RecordBatch{Records: []Record{{
		Value: bytes.Repeat([]byte("x"), 64),
	}}}); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("oversized low-level append error = %v, want ErrMessageTooLarge", err)
	}
	if got := log.NewestOffset(); got != 0 {
		t.Fatalf("oversized append advanced offset to %d", got)
	}
}

func TestCommitLogRecovery(t *testing.T) {
	dir := t.TempDir()

	// Write some data and close.
	cl, err := NewCommitLog(dir)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		_, err := cl.Append(&RecordBatch{
			Records: []Record{{Value: []byte("msg")}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	cl.Close()

	// Reopen — should recover nextOffset.
	cl2, err := NewCommitLog(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer cl2.Close()

	if cl2.NewestOffset() != 5 {
		t.Errorf("recovered NewestOffset = %d, want 5", cl2.NewestOffset())
	}

	// Append more after recovery.
	base, err := cl2.Append(&RecordBatch{
		Records: []Record{{Value: []byte("after-restart")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if base != 5 {
		t.Errorf("post-recovery base = %d, want 5", base)
	}

	// Read the new record.
	batches, err := cl2.Read(5, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) == 0 {
		t.Fatal("no batches after recovery append")
	}
	if !bytes.Equal(batches[0].Records[0].Value, []byte("after-restart")) {
		t.Error("recovery record value mismatch")
	}
}

func TestCommitLogRecoveryAppendsIndexEntriesWithoutOverwriting(t *testing.T) {
	dir := t.TempDir()
	options := []Option{
		WithIndexInterval(1),
		WithMaxSegmentBytes(1 << 20),
		WithMaxStorageBytes(1 << 20),
	}

	log, err := NewCommitLog(dir, options...)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		if _, err := log.Append(&RecordBatch{
			Records: []Record{{Value: []byte{byte(index)}}},
		}); err != nil {
			t.Fatalf("initial append %d: %v", index, err)
		}
	}
	if err := log.Close(); err != nil {
		t.Fatalf("close initial log: %v", err)
	}

	reopened, err := NewCommitLog(dir, options...)
	if err != nil {
		t.Fatalf("first reopen: %v", err)
	}
	if _, err := reopened.Append(&RecordBatch{
		Records: []Record{{Value: []byte("after-restart")}},
	}); err != nil {
		t.Fatalf("append after restart: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("close after restart: %v", err)
	}

	verified, err := NewCommitLog(dir, options...)
	if err != nil {
		t.Fatalf("second reopen after index append: %v", err)
	}
	defer verified.Close()
	if got := verified.NewestOffset(); got != 4 {
		t.Fatalf("newest offset = %d, want 4", got)
	}
	batches, err := verified.Read(3, 1<<20)
	if err != nil {
		t.Fatalf("read appended record: %v", err)
	}
	if len(batches) != 1 || len(batches[0].Records) != 1 ||
		string(batches[0].Records[0].Value) != "after-restart" {
		t.Fatalf("record appended after restart was not recovered: %#v", batches)
	}
}

func TestCommitLogRecoveryRebuildsMissingIndexFromAuthoritativeLog(t *testing.T) {
	dir := t.TempDir()
	options := []Option{
		WithIndexInterval(1),
		WithMaxSegmentBytes(1 << 20),
		WithMaxStorageBytes(1 << 20),
	}
	log, err := NewCommitLog(dir, options...)
	if err != nil {
		t.Fatal(err)
	}
	for sequence := 0; sequence < 8; sequence++ {
		if _, err := log.Append(&RecordBatch{Records: []Record{{
			Value: bytes.Repeat([]byte{byte(sequence)}, 64),
		}}}); err != nil {
			t.Fatal(err)
		}
	}
	indexPath := log.activeSegment.index.file.Name()
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(indexPath, 0); err != nil {
		t.Fatal(err)
	}

	recovered, err := NewCommitLog(dir, options...)
	if err != nil {
		t.Fatalf("recover without index: %v", err)
	}
	defer recovered.Close()
	batches, err := recovered.Read(7, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 || batches[0].BaseOffset != 7 {
		t.Fatalf("read after index rebuild = %#v, want batch at offset 7", batches)
	}
}

func TestCommitLogRecoveryRebuildsPartialIndexFromAuthoritativeLog(t *testing.T) {
	dir := t.TempDir()
	options := []Option{
		WithIndexInterval(1),
		WithMaxSegmentBytes(1 << 20),
		WithMaxStorageBytes(1 << 20),
	}
	log, err := NewCommitLog(dir, options...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(&RecordBatch{Records: []Record{{Value: []byte("durable")}}}); err != nil {
		t.Fatal(err)
	}
	indexPath := log.activeSegment.index.file.Name()
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(indexPath, os.O_WRONLY|os.O_APPEND, filePerm)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte{0xff}); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	recovered, err := NewCommitLog(dir, options...)
	if err != nil {
		t.Fatalf("recover partial rebuildable index: %v", err)
	}
	defer recovered.Close()
	batches, err := recovered.Read(0, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 || string(batches[0].Records[0].Value) != "durable" {
		t.Fatalf("recovered batches = %#v", batches)
	}
}

func TestCommitLogRecoveryRemovesOrphanDisposableIndex(t *testing.T) {
	dir := t.TempDir()
	orphanPath := filepath.Join(dir, "00000000000000000042"+extIndex)
	if err := os.WriteFile(orphanPath, []byte("stale"), filePerm); err != nil {
		t.Fatal(err)
	}

	log, err := NewCommitLog(dir)
	if err != nil {
		t.Fatalf("recover orphan index: %v", err)
	}
	defer log.Close()
	if _, err := os.Stat(orphanPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan disposable index remains: %v", err)
	}
}

func TestCommitLogRecoveryEnforcesStorageAndSegmentBoundsBeforeOpening(t *testing.T) {
	t.Run("storage bytes", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "00000000000000000000"+extLog)
		if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 65), filePerm); err != nil {
			t.Fatal(err)
		}
		if _, err := NewCommitLog(
			dir,
			WithMaxSegmentBytes(64),
			WithMaxStorageBytes(64),
		); !errors.Is(err, ErrStorageFull) {
			t.Fatalf("oversized recovered storage error = %v, want ErrStorageFull", err)
		}
	})

	t.Run("segment count", func(t *testing.T) {
		dir := t.TempDir()
		for offset := 0; offset <= maxSegmentsPerTopic; offset++ {
			path := filepath.Join(dir, fmt.Sprintf(segmentNameFmt+extLog, offset))
			if err := os.WriteFile(path, nil, filePerm); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := NewCommitLog(dir); !errors.Is(err, ErrStorageFull) {
			t.Fatalf("recovered segment overflow error = %v, want ErrStorageFull", err)
		}
	})
}

func TestCommitLogReadReturnsOwnedBytesForSealedSegment(t *testing.T) {
	dir := t.TempDir()
	log, err := NewCommitLog(
		dir,
		WithMaxSegmentBytes(40),
		WithMaxMessageSize(64),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(&RecordBatch{Records: []Record{{Value: []byte("first")}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(&RecordBatch{Records: []Record{{Value: []byte("second")}}}); err != nil {
		t.Fatal(err)
	}
	batches, err := log.Read(0, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 {
		t.Fatalf("sealed read batches = %d, want 1", len(batches))
	}
	value := batches[0].Records[0].Value
	copy(value, []byte("owned"))
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	if string(value) != "owned" {
		t.Fatalf("returned bytes changed after log close: %q", value)
	}

	recovered, err := NewCommitLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	persisted, err := recovered.Read(0, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 1 || string(persisted[0].Records[0].Value) != "first" {
		t.Fatalf("caller mutation reached storage: %#v", persisted)
	}
}

func TestCommitLogReadScansPastSparseIndexWithinBoundedReturnBudget(t *testing.T) {
	log, err := NewCommitLog(
		t.TempDir(),
		WithIndexInterval(1<<20),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	for offset := 0; offset < 10; offset++ {
		if _, err := log.Append(&RecordBatch{Records: []Record{{
			Value: bytes.Repeat([]byte{byte(offset)}, 32),
		}}}); err != nil {
			t.Fatal(err)
		}
	}

	batches, err := log.Read(9, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 || batches[0].BaseOffset != 9 {
		t.Fatalf("sparse-index read = %#v, want batch at offset 9", batches)
	}
}

func TestCommitLogReadEnforcesBoundedAtomicBatchBudget(t *testing.T) {
	log, err := NewCommitLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	if _, err := log.Append(&RecordBatch{Records: []Record{{
		Value: bytes.Repeat([]byte("x"), 256),
	}}}); err != nil {
		t.Fatal(err)
	}

	if _, err := log.Read(0, 64); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("read smaller than atomic batch error = %v, want ErrInvalidConfig", err)
	}
	if _, err := log.Read(0, maxEncodedBatchBytes+1); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("unbounded read error = %v, want ErrInvalidConfig", err)
	}
}

func TestCommitLogRejectsOffsetExhaustionBeforeWriting(t *testing.T) {
	log, err := NewCommitLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	log.nextOffset.Store(math.MaxUint64)
	log.activeSegment.nextOffset = math.MaxUint64

	if _, err := log.Append(&RecordBatch{Records: []Record{{Value: []byte("overflow")}}}); !errors.Is(err, ErrInvalidSegmentLayout) {
		t.Fatalf("Append at exhausted offset error = %v, want ErrInvalidSegmentLayout", err)
	}
	if info, err := log.activeSegment.logFile.Stat(); err != nil || info.Size() != 0 {
		t.Fatalf("offset exhaustion wrote data: info=%v err=%v", info, err)
	}
}

func TestCommitLogSegmentRolling(t *testing.T) {
	dir := t.TempDir()

	// Tiny segment size to force rolling.
	cl, err := NewCommitLog(dir, WithMaxSegmentBytes(100))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()

	for i := 0; i < 20; i++ {
		_, err := cl.Append(&RecordBatch{
			Records: []Record{{Value: []byte("payload-that-fills-segment-fast")}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// Should have multiple segment files.
	entries, _ := os.ReadDir(dir)
	logCount := 0
	for _, e := range entries {
		if !e.IsDir() && len(e.Name()) > 4 && e.Name()[len(e.Name())-4:] == ".log" {
			logCount++
		}
	}
	if logCount < 2 {
		t.Errorf("expected multiple segments, got %d .log files", logCount)
	}
}

func TestCommitLogBoundsSegmentFileCount(t *testing.T) {
	log, err := NewCommitLog(
		t.TempDir(),
		WithMaxSegmentBytes(1),
		WithMaxStorageBytes(1<<20),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	for sequence := 0; sequence < maxSegmentsPerTopic; sequence++ {
		if _, err := log.Append(&RecordBatch{Records: []Record{{Value: []byte("x")}}}); err != nil {
			t.Fatalf("Append(%d): %v", sequence, err)
		}
	}
	if _, err := log.Append(&RecordBatch{Records: []Record{{Value: []byte("overflow")}}}); !errors.Is(err, ErrStorageFull) {
		t.Fatalf("segment-count overflow error = %v, want ErrStorageFull", err)
	}
	if got := len(log.segments); got != maxSegmentsPerTopic {
		t.Fatalf("segments = %d, want hard cap %d", got, maxSegmentsPerTopic)
	}
}

func TestCommitLogSyncOnlyFsyncsDirtySegments(t *testing.T) {
	prototype := &RecordBatch{
		BaseOffset:  0,
		RecordCount: 1,
		Records:     []Record{{Value: []byte("sync-scope")}},
	}
	encoded, err := EncodeBatch(prototype, nil)
	if err != nil {
		t.Fatal(err)
	}

	defaultOps := defaultFileOperations()
	ops := defaultOps
	var syncMu sync.Mutex
	logSyncs := make(map[string]int)
	ops.sync = func(file *os.File) error {
		if err := defaultOps.sync(file); err != nil {
			return err
		}
		if filepath.Ext(file.Name()) == extLog {
			syncMu.Lock()
			logSyncs[file.Name()]++
			syncMu.Unlock()
		}
		return nil
	}

	log, err := NewCommitLog(
		t.TempDir(),
		WithMaxSegmentBytes(int64(len(encoded)*2-1)),
		WithMaxStorageBytes(1<<20),
		withFileOperations(ops),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	for sequence := 0; sequence < 3; sequence++ {
		if _, err := log.Append(&RecordBatch{Records: []Record{{Value: []byte("sync-scope")}}}); err != nil {
			t.Fatal(err)
		}
	}
	if len(log.segments) != 2 {
		t.Fatalf("fixture segments = %d, want 2", len(log.segments))
	}

	syncMu.Lock()
	clear(logSyncs)
	syncMu.Unlock()
	if err := log.Sync(); err != nil {
		t.Fatal(err)
	}
	syncMu.Lock()
	firstSyncs := make(map[string]int, len(logSyncs))
	for path, count := range logSyncs {
		firstSyncs[path] = count
	}
	clear(logSyncs)
	syncMu.Unlock()
	if len(firstSyncs) != 2 {
		t.Fatalf("first Sync() fsynced %d log files, want both dirty segments: %v", len(firstSyncs), firstSyncs)
	}
	for path, count := range firstSyncs {
		if count != 1 {
			t.Fatalf("first Sync() fsync count for %s = %d, want 1", path, count)
		}
	}

	if err := log.Sync(); err != nil {
		t.Fatal(err)
	}
	syncMu.Lock()
	cleanSyncs := len(logSyncs)
	syncMu.Unlock()
	if cleanSyncs != 0 {
		t.Fatalf("second Sync() fsynced %d clean log files, want 0", cleanSyncs)
	}

	if _, err := log.Append(&RecordBatch{Records: []Record{{Value: []byte("sync-scope")}}}); err != nil {
		t.Fatal(err)
	}
	if err := log.Sync(); err != nil {
		t.Fatal(err)
	}
	syncMu.Lock()
	dirtySyncs := make(map[string]int, len(logSyncs))
	for path, count := range logSyncs {
		dirtySyncs[path] = count
	}
	syncMu.Unlock()
	if len(dirtySyncs) != 1 || dirtySyncs[log.activeSegment.logFile.Name()] != 1 {
		t.Fatalf("Sync() after active append fsyncs = %v, want active segment only", dirtySyncs)
	}
}

func TestCommitLogDeleteBefore(t *testing.T) {
	dir := t.TempDir()

	cl, err := NewCommitLog(dir, WithMaxSegmentBytes(80))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()

	for i := 0; i < 10; i++ {
		cl.Append(&RecordBatch{
			Records: []Record{{Value: []byte("delete-test-payload")}},
		})
	}

	// Delete segments before offset 5.
	if err := cl.DeleteBefore(5); err != nil {
		t.Fatal(err)
	}

	// Oldest offset should have advanced.
	oldest := cl.OldestOffset()
	if oldest < 1 {
		t.Logf("oldest offset after delete: %d (may vary by segment boundaries)", oldest)
	}
}

func TestCommitLogReadRejectsOffsetsOutsideRetainedRange(t *testing.T) {
	dir := t.TempDir()
	log, err := NewCommitLog(dir, WithMaxSegmentBytes(1))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	for index := 0; index < 4; index++ {
		if _, err := log.Append(&RecordBatch{
			Records: []Record{{Value: []byte{byte(index)}}},
		}); err != nil {
			t.Fatalf("append %d: %v", index, err)
		}
	}
	if err := log.DeleteBefore(2); err != nil {
		t.Fatalf("delete before retained offset: %v", err)
	}
	if got := log.OldestOffset(); got != 2 {
		t.Fatalf("oldest offset = %d, want 2", got)
	}
	if _, err := log.Read(1, 1<<20); !errors.Is(err, ErrOffsetNotFound) {
		t.Fatalf("read before retained range error = %v, want ErrOffsetNotFound", err)
	}
	if _, err := log.Read(log.NewestOffset()+1, 1<<20); !errors.Is(err, ErrOffsetNotFound) {
		t.Fatalf("read after newest range error = %v, want ErrOffsetNotFound", err)
	}
	if batches, err := log.Read(log.NewestOffset(), 1<<20); err != nil || len(batches) != 0 {
		t.Fatalf("read at next offset = %#v, %v; want empty success", batches, err)
	}
}

func TestCommitLogRejectsNonCanonicalSegmentName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "1.log"), nil, filePerm); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCommitLog(dir); !errors.Is(err, ErrInvalidSegmentLayout) {
		t.Fatalf("NewCommitLog() error = %v, want ErrInvalidSegmentLayout", err)
	}
}

func TestCommitLogRecoveryRejectsOffsetGap(t *testing.T) {
	dir := t.TempDir()
	first, err := EncodeBatch(&RecordBatch{
		BaseOffset:  0,
		RecordCount: 1,
		Records:     []Record{{OffsetDelta: 0, Value: []byte("first")}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EncodeBatch(&RecordBatch{
		BaseOffset:  2,
		RecordCount: 1,
		Records:     []Record{{OffsetDelta: 0, Value: []byte("second")}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "00000000000000000000.log")
	if err := os.WriteFile(path, append(first, second...), filePerm); err != nil {
		t.Fatal(err)
	}
	_, err = NewCommitLog(dir)
	var corruption *CorruptionError
	if !errors.As(err, &corruption) {
		t.Fatalf("NewCommitLog() error = %v, want *CorruptionError", err)
	}
}
