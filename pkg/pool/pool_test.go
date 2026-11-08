package pool

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestPoolReturnsAdequateSizesAndResetsBeforePutReturns(t *testing.T) {
	var resetCalls atomic.Int64
	p := New(
		func(size int) []byte { return make([]byte, size) },
		func(value []byte) int { return cap(value) },
		func(value []byte) { clear(value); resetCalls.Add(1) },
	)
	for _, size := range []int{0, 1, 63, 64, 65, 127, 128, 129} {
		value := p.Get(size)
		if size <= 0 {
			size = minSize
		}
		if len(value) < size || cap(value) < size {
			t.Fatalf("Get(%d) returned len=%d cap=%d", size, len(value), cap(value))
		}
	}

	value := make([]byte, 64)
	value[0] = 1
	p.Put(value)
	if value[0] != 0 || resetCalls.Load() != 1 {
		t.Fatal("Put returned before resetting the retained value")
	}

	overflow := New(func(size int) int { return size }, func(value int) int { return value }, nil)
	want := BucketSize(sizeClasses-1) + 1
	if got := overflow.Get(want); got < want {
		t.Fatalf("oversize Get(%d) returned %d", want, got)
	}
}

func TestPoolDiscardsValuesTooSmallForTheirSizeClass(t *testing.T) {
	var resets atomic.Int64
	p := New(
		func(size int) []byte { return make([]byte, size) },
		func(value []byte) int { return cap(value) },
		func([]byte) { resets.Add(1) },
	)
	p.Put(make([]byte, 65)) // 65 maps to the 128-byte Get class
	if resets.Load() != 0 {
		t.Fatal("pool retained a value that cannot satisfy its size class")
	}
	if got := p.Get(128); len(got) < 128 {
		t.Fatalf("Get(128) returned only %d bytes after malformed Put", len(got))
	}
}

func TestPoolCalibrationKeepsTheDominantWorkingSet(t *testing.T) {
	var resets atomic.Int64
	p := New(
		func(size int) []byte { return make([]byte, size) },
		func(value []byte) int { return cap(value) },
		func([]byte) { resets.Add(1) },
	)
	small, large := make([]byte, 64), make([]byte, 128)
	for range calibrateThreshold {
		p.Put(small)
	}
	for range 2_000 {
		p.Put(large)
	}
	p.Put(small) // crosses the calibration threshold
	if got := p.DefaultSize(); got != 64 {
		t.Fatalf("calibrated default size = %d, want 64", got)
	}
	if got := p.MaxSize(); got != 64 {
		t.Fatalf("calibrated retained maximum = %d, want 64", got)
	}
	before := resets.Load()
	p.Put(large)
	if resets.Load() != before {
		t.Fatal("pool reset a value outside the calibrated retained range")
	}
}

type resettableBytes struct{ data []byte }

func (b *resettableBytes) Len() int { return cap(b.data) }
func (b *resettableBytes) Reset()   { clear(b.data) }

func TestResettablePoolClearsDataBeforeReturning(t *testing.T) {
	p := NewResettable(func(size int) *resettableBytes {
		return &resettableBytes{data: make([]byte, size)}
	})
	value := p.Get(64)
	value.data[0] = 99
	p.Put(value)
	if value.data[0] != 0 {
		t.Fatal("Resettable value retained data after Put")
	}
}

func TestBytePoolPreservesWithErrorsPanicsAndRelease(t *testing.T) {
	if got := Get(0); len(got) != 0 {
		t.Fatalf("Get(0) returned length %d", len(got))
	}
	if err := With(-1, func([]byte) error { return nil }); !errors.Is(err, ErrInvalidSize) {
		t.Fatalf("With(-1) error = %v", err)
	}
	if err := With(0, func(value []byte) error {
		if len(value) != 0 {
			t.Fatalf("With(0) callback received %d bytes", len(value))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	before := GetStats()[0]
	wantErr := errors.New("callback failed")
	if err := With(16, func(value []byte) error {
		if len(value) != 16 {
			t.Fatalf("callback received %d bytes, want 16", len(value))
		}
		return wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("With callback error = %v", err)
	}
	if got := GetStats()[0]; got != before+1 {
		t.Fatalf("With did not return its buffer to the pool: puts=%d, want %d", got-before, 1)
	}

	before = GetStats()[0]
	func() {
		defer func() {
			if got := recover(); got != "callback panic" {
				t.Fatalf("With panic = %v", got)
			}
		}()
		_ = With(16, func([]byte) error { panic("callback panic") })
	}()
	if got := GetStats()[0]; got != before+1 {
		t.Fatalf("With did not release after panic: puts=%d, want 1", got-before)
	}
	before = GetStats()[0]
	Put(nil)
	if got := GetStats()[0]; got != before {
		t.Fatal("Put retained an empty slice")
	}
}

func TestPoolServesConcurrentIndependentBorrows(t *testing.T) {
	p := New(
		func(size int) []byte { return make([]byte, size) },
		func(value []byte) int { return cap(value) },
		func(value []byte) { clear(value) },
	)
	var workers sync.WaitGroup
	start := make(chan struct{})
	for worker := range 32 {
		workers.Add(1)
		go func(value byte) {
			defer workers.Done()
			<-start
			for range 100 {
				borrowed := p.Get(128)
				if len(borrowed) < 128 {
					t.Errorf("short concurrent borrow: %d", len(borrowed))
					return
				}
				for index := range borrowed {
					borrowed[index] = value
				}
				p.Put(borrowed)
			}
		}(byte(worker + 1))
	}
	close(start)
	workers.Wait()
}
