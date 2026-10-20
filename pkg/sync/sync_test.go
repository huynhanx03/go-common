package sync

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/puzpuzpuz/xsync/v4"
)

func TestMapContractAndOptions(t *testing.T) {
	m := NewMap[string, int](nil, WithPresize(8), WithGrowOnly())
	if value, loaded := m.LoadOrStore("a", 1); loaded || value != 1 {
		t.Fatalf("LoadOrStore() = (%d, %t), want (1, false)", value, loaded)
	}
	if value, loaded := m.LoadAndStore("a", 2); !loaded || value != 1 {
		t.Fatalf("LoadAndStore() = (%d, %t), want (1, true)", value, loaded)
	}
	if value, loaded := m.LoadOrCompute("b", func() (int, bool) { return 3, false }); loaded || value != 3 {
		t.Fatalf("LoadOrCompute() = (%d, %t), want (3, false)", value, loaded)
	}

	if value, loaded := m.Compute("a", func(old int, loaded bool) (int, ComputeOp) {
		if !loaded || old != 2 {
			t.Fatalf("Compute callback = (%d, %t), want (2, true)", old, loaded)
		}
		return 4, UpdateOp
	}); !loaded || value != 4 {
		t.Fatalf("Compute update = (%d, %t), want (4, true)", value, loaded)
	}
	m.Compute("a", func(int, bool) (int, ComputeOp) { return 0, CancelOp })
	if value, loaded := m.Load("a"); !loaded || value != 4 {
		t.Fatalf("Compute cancel left (%d, %t), want (4, true)", value, loaded)
	}
	m.Compute("a", func(int, bool) (int, ComputeOp) { return 0, DeleteOp })
	if _, loaded := m.Load("a"); loaded {
		t.Fatal("Compute delete left the key present")
	}

	m.Store("c", 5)
	if value, loaded := m.LoadAndDelete("c"); !loaded || value != 5 {
		t.Fatalf("LoadAndDelete() = (%d, %t), want (5, true)", value, loaded)
	}
	m.Store("d", 6)
	var ranged []string
	m.Range(func(key string, _ int) bool { ranged = append(ranged, key); return true })
	if len(ranged) != 2 || !slices.Contains(ranged, "b") || !slices.Contains(ranged, "d") {
		t.Fatalf("Range() keys = %v, want keys b and d", ranged)
	}
	var allCount, relaxedCount int
	for range m.All() {
		allCount++
	}
	m.RangeRelaxed(func(string, int) bool { return true })
	for range m.AllRelaxed() {
		relaxedCount++
	}
	if allCount != 2 || relaxedCount != 2 {
		t.Fatalf("All counts = (%d, %d), want (2, 2)", allCount, relaxedCount)
	}
	deleted := m.DeleteMatching(func(key string, _ int) (bool, bool) { return key == "d", false })
	if deleted != 1 || m.Size() != 1 {
		t.Fatalf("DeleteMatching() = %d, size = %d, want 1 and 1", deleted, m.Size())
	}
	m.Store("e", 7)
	m.Clear()
	if m.Size() != 0 {
		t.Fatalf("Size() after Clear() = %d, want 0", m.Size())
	}

	plainNil := ToPlainMap[string, int](nil)
	plainNil["safe"] = 1
	if len(plainNil) != 1 {
		t.Fatal("ToPlainMap(nil) did not return an initialized map")
	}
	m.Store("plain", 8)
	if plain := ToPlainMap(m); !reflect.DeepEqual(plain, map[string]int{"plain": 8}) {
		t.Fatalf("ToPlainMap() = %v", plain)
	}
	if NewMap[string, int]().Size() != 0 {
		t.Fatal("default map should start empty")
	}
	if stats := m.Stats(); stats.Size != 1 {
		t.Fatalf("Stats().Size = %d, want 1", stats.Size)
	}
}

func TestMapConversionsAndDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		operation ComputeOp
		want      xsync.ComputeOp
	}{{CancelOp, xsync.CancelOp}, {UpdateOp, xsync.UpdateOp}, {DeleteOp, xsync.DeleteOp}, {ComputeOp(9), xsync.ComputeOp(9)}} {
		if got := toComputeOp(tc.operation); got != tc.want {
			t.Errorf("toComputeOp(%d) = %d, want %d", tc.operation, got, tc.want)
		}
	}

	got := toMapStats(xsync.MapStats{
		RootBuckets: 1, TotalBuckets: 2, EmptyBuckets: 3, Capacity: 4, Size: 5,
		Counter: 6, CounterLen: 7, MinEntries: 8, MaxEntries: 9,
		TotalGrowths: 10, TotalShrinks: 11,
	})
	want := MapStats{
		RootBuckets: 1, TotalBuckets: 2, EmptyBuckets: 3, Capacity: 4, Size: 5,
		Counter: 6, CounterLen: 7, MinEntries: 8, MaxEntries: 9,
		TotalGrowths: 10, TotalShrinks: 11,
	}
	if got != want {
		t.Fatalf("toMapStats() = %+v, want %+v", got, want)
	}

	wantString := "MapStats{\nRootBuckets:  1\nTotalBuckets: 2\nEmptyBuckets: 3\nCapacity:     4\nSize:         5\nCounter:      6\nCounterLen:   7\nMinEntries:   8\nMaxEntries:   9\nTotalGrowths: 10\nTotalShrinks: 11\n}\n"
	if got := want.ToString(); got != wantString {
		t.Fatalf("ToString() = %q, want %q", got, wantString)
	}
}

func TestCounterAndQueueContracts(t *testing.T) {
	counter := NewCounter()
	counter.Add(4)
	counter.Inc()
	counter.Dec()
	if got := counter.Value(); got != 4 {
		t.Fatalf("counter value = %d, want 4", got)
	}
	counter.Reset()
	if got := counter.Value(); got != 0 {
		t.Fatalf("counter after Reset() = %d, want 0", got)
	}

	for name, queue := range map[string]BoundedQueue[int]{
		"MPMC": NewMPMCQueue[int](2),
		"SPSC": NewSPSCQueue[int](2),
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := queue.TryDequeue(); ok {
				t.Fatal("empty queue unexpectedly dequeued an item")
			}
			if !queue.TryEnqueue(1) || !queue.TryEnqueue(2) || queue.TryEnqueue(3) {
				t.Fatal("queue capacity was not enforced")
			}
			for _, want := range []int{1, 2} {
				if got, ok := queue.TryDequeue(); !ok || got != want {
					t.Fatalf("TryDequeue() = (%d, %t), want (%d, true)", got, ok, want)
				}
			}
			if _, ok := queue.TryDequeue(); ok {
				t.Fatal("drained queue unexpectedly dequeued an item")
			}
		})
	}

	unbounded := NewUMPSCQueue[int]()
	unbounded.Enqueue(10)
	unbounded.Enqueue(20)
	if got := unbounded.Dequeue(); got != 10 {
		t.Fatalf("first UMPSC dequeue = %d, want 10", got)
	}
	if got := unbounded.Dequeue(); got != 20 {
		t.Fatalf("second UMPSC dequeue = %d, want 20", got)
	}
}

func TestRBMutexContract(t *testing.T) {
	mu := NewRBMutex()
	token := mu.RLock()
	mu.RUnlock(token)

	locked, token := mu.TryRLock()
	if !locked {
		t.Fatalf("TryRLock() = (%t, %v), want success", locked, token)
	}
	mu.RUnlock(token)

	mu.Lock()
	if mu.TryLock() {
		mu.Unlock()
		t.Fatal("TryLock() succeeded while the mutex was already locked")
	}
	if locked, token := mu.TryRLock(); locked || token != nil {
		t.Fatalf("TryRLock() while write-locked = (%t, %v), want (false, nil)", locked, token)
	}
	mu.Unlock()
	if !mu.TryLock() {
		t.Fatal("TryLock() failed on an unlocked mutex")
	}
	mu.Unlock()

	// A writer disables reader bias, so the fallback read lock has no token.
	mu.Lock()
	mu.Unlock()
	token = mu.RLock()
	if token != nil {
		t.Fatal("fallback RLock() returned a biased-reader token")
	}
	mu.RUnlock(token)
}

func ExampleMapStats_ToString() {
	fmt.Print((MapStats{Size: 2}).ToString())
	// Output:
	// MapStats{
	// RootBuckets:  0
	// TotalBuckets: 0
	// EmptyBuckets: 0
	// Capacity:     0
	// Size:         2
	// Counter:      0
	// CounterLen:   0
	// MinEntries:   0
	// MaxEntries:   0
	// TotalGrowths: 0
	// TotalShrinks: 0
	// }
}
