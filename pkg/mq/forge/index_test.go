package forge

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestSparseIndexFindsNearestOffsetAtOrBeforeTarget(t *testing.T) {
	t.Parallel()

	index, err := openIndex(filepath.Join(t.TempDir(), "events.idx"), 100, defaultFileOperations())
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	for _, entry := range []indexEntry{
		{relativeOffset: 0, position: 0},
		{relativeOffset: 4, position: 40},
		{relativeOffset: 8, position: 100},
	} {
		if err := index.Append(100+uint64(entry.relativeOffset), entry.position); err != nil {
			t.Fatalf("Append(%d): %v", 100+entry.relativeOffset, err)
		}
	}

	for _, test := range []struct {
		target       uint64
		wantPosition uint64
		wantOffset   uint64
	}{
		{target: 99, wantOffset: 100},
		{target: 100, wantOffset: 100},
		{target: 103, wantOffset: 100},
		{target: 104, wantPosition: 40, wantOffset: 104},
		{target: 107, wantPosition: 40, wantOffset: 104},
		{target: 108, wantPosition: 100, wantOffset: 108},
		{target: 200, wantPosition: 100, wantOffset: 108},
	} {
		position, offset, err := index.Lookup(test.target)
		if err != nil || position != test.wantPosition || offset != test.wantOffset {
			t.Errorf("Lookup(%d) = (%d, %d, %v), want (%d, %d, nil)",
				test.target, position, offset, err, test.wantPosition, test.wantOffset)
		}
	}
}

func TestSparseIndexRejectsOffsetsThatWouldBreakMonotonicLookup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		base    uint64
		offsets []uint64
		places  []uint64
	}{
		{name: "offset before segment base", base: 10, offsets: []uint64{9}, places: []uint64{0}},
		{name: "relative offset exceeds encoding", offsets: []uint64{1 << 32}, places: []uint64{0}},
		{name: "duplicate offset", offsets: []uint64{10, 10}, places: []uint64{0, 10}},
		{name: "decreasing position", offsets: []uint64{10, 11}, places: []uint64{10, 9}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			index, err := openIndex(filepath.Join(t.TempDir(), "events.idx"), test.base, defaultFileOperations())
			if err != nil {
				t.Fatal(err)
			}
			defer index.Close()
			for i, offset := range test.offsets {
				err := index.Append(offset, test.places[i])
				if i < len(test.offsets)-1 {
					if err != nil {
						t.Fatalf("Append valid prefix: %v", err)
					}
					continue
				}
				if !errors.Is(err, ErrInvalidSegmentLayout) {
					t.Fatalf("Append(%d, %d) error = %v, want ErrInvalidSegmentLayout", offset, test.places[i], err)
				}
			}
		})
	}
}

func TestSparseIndexRetriesAfterShortWriteAndTruncatesPartialEntry(t *testing.T) {
	t.Parallel()

	operations := defaultFileOperations()
	failOnce := true
	operations.write = func(file *os.File, data []byte) (int, error) {
		if failOnce {
			failOnce = false
			return file.Write(data[:len(data)/2])
		}
		return file.Write(data)
	}
	path := filepath.Join(t.TempDir(), "events.idx")
	index, err := openIndex(path, 0, operations)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	if err := index.Append(0, 0); err != nil {
		t.Fatal(err)
	}
	if err := index.Append(4, 64); err != nil {
		t.Fatal(err)
	}

	if err := index.Sync(); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("first Sync() error = %v, want io.ErrShortWrite", err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() != 0 {
		t.Fatalf("partial index was not removed: info=%v err=%v", info, err)
	}
	if err := index.Sync(); err != nil {
		t.Fatalf("retry Sync(): %v", err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() != 2*indexEntrySize {
		t.Fatalf("synced index size = %v, err=%v, want %d bytes", info, err, 2*indexEntrySize)
	}
	if err := index.Sync(); err != nil {
		t.Fatalf("clean Sync(): %v", err)
	}
	if position, offset, err := index.Lookup(5); err != nil || position != 64 || offset != 4 {
		t.Fatalf("Lookup after retry = (%d, %d, %v), want (64, 4, nil)", position, offset, err)
	}
}
