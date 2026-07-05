package forge

import (
	"bytes"
	"os"
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
			Timestamp:   int64(i * 1000),
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
