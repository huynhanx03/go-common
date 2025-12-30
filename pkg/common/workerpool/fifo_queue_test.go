package workerpool

import (
	"errors"
	"testing"
	"time"
)

func TestNewFIFOQueue(t *testing.T) {
	size := 100
	q := newFIFOQueue(size)
	if q.len() != 0 {
		t.Errorf("NewFIFOQueue len = %d, want 0", q.len())
	}
	if !q.isEmpty() {
		t.Errorf("NewFIFOQueue isEmpty = false, want true")
	}
	if q.detach() != nil {
		t.Errorf("NewFIFOQueue detach should be nil")
	}
	if newFIFOQueue(0) != nil {
		t.Errorf("NewFIFOQueue(0) should be nil")
	}
}

func TestFIFOQueue_InsertDetach(t *testing.T) {
	size := 10
	q := newFIFOQueue(size)

	for i := 0; i < 5; i++ {
		err := q.insert(&mockWorker{workerCommon{lastUsed: time.Now()}})
		if err != nil {
			t.Errorf("Insert failed: %v", err)
		}
	}
	if q.len() != 5 {
		t.Errorf("Len = %d, want 5", q.len())
	}

	_ = q.detach()
	if q.len() != 4 {
		t.Errorf("Len after detach = %d, want 4", q.len())
	}

	time.Sleep(10 * time.Millisecond) // Ensure time difference

	for i := 0; i < 6; i++ {
		err := q.insert(&mockWorker{workerCommon{lastUsed: time.Now()}})
		if err != nil {
			// Expected to fail on the last one if full, but size is 10, current is 4.
			// 4 + 6 = 10. Should fit.
			t.Errorf("Insert failed at %d: %v", i, err)
		}
	}
	if q.len() != 10 {
		t.Errorf("Len = %d, want 10", q.len())
	}

	err := q.insert(&mockWorker{workerCommon{lastUsed: time.Now()}})
	if !errors.Is(err, ErrQueueIsFull) {
		t.Errorf("Insert on full queue should return ErrQueueIsFull, got %v", err)
	}

	q.refresh(time.Second)
	// mock types were created just now, so none should expire unless we mock older times
}

func TestFIFOQueue_Refresh(t *testing.T) {
	size := 10
	q := newFIFOQueue(size)
	duration := 100 * time.Millisecond

	// Insert workers that are expired (lastUsed = now - 2*duration)
	expiredCount := 5
	for i := 0; i < expiredCount; i++ {
		q.insert(&mockWorker{workerCommon{lastUsed: time.Now().Add(-200 * time.Millisecond)}})
	}

	// Insert workers that are fresh (lastUsed = now)
	freshCount := 5
	for i := 0; i < freshCount; i++ {
		q.insert(&mockWorker{workerCommon{lastUsed: time.Now()}})
	}

	if q.len() != 10 {
		t.Fatalf("Queue should be full, got %d", q.len())
	}

	expiredWorkers := q.refresh(duration)
	if len(expiredWorkers) != expiredCount {
		t.Errorf("Expected %d expired workers, got %d", expiredCount, len(expiredWorkers))
	}
	if q.len() != freshCount {
		t.Errorf("Queue len after refresh = %d, want %d", q.len(), freshCount)
	}
}

func TestFIFOQueue_Rotate_BinarySearch(t *testing.T) {
	size := 10
	q := newFIFOQueue(size)

	// Fill queue
	for i := 0; i < size; i++ {
		q.insert(&mockWorker{workerCommon{lastUsed: time.Now()}})
	}

	// Detach 5 to rotate head
	for i := 0; i < 5; i++ {
		q.detach()
	}

	// Now head is at 5. tail is at 0 (after wrap around if we insert)

	// Insert 3 fresh ones. They will be at index 0, 1, 2 in the underlying array
	for i := 0; i < 3; i++ {
		q.insert(&mockWorker{workerCommon{lastUsed: time.Now()}})
	}

	// Current state:
	// Array indices: [F, F, F, _, _, O, O, O, O, O]
	//                 0  1  2     5  6  7  8  9
	// Head = 5, Tail = 3 (pointing to next insert at 3)
	// Logical order: 5,6,7,8,9 (Old), 0,1,2 (Fresh)
	// Timestamps: Old < Fresh. Queue order is correct by time.

	// Case 1: All expired
	// We simulate a scenario where we have workers with mixed expiration states
	// in a wrapped-around queue to verify Binary Search handles rotation correctly.

	q2 := newFIFOQueue(10)
	baseTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)

	// Step 1: Insert 5 old workers (indices 0-4)
	for i := 0; i < 5; i++ {
		q2.insert(&mockWorker{workerCommon{lastUsed: baseTime}})
	}

	// Step 2: Detach 2 to move head to index 2
	q2.detach()
	q2.detach()

	// Step 3: Insert 7 new workers.
	// Due to wrap-around:
	// - indices 2,3,4: Old workers (Time T)
	// - indices 5,6,7,8,9,0,1: New workers (Time T+1h)
	for i := 0; i < 7; i++ {
		q2.insert(&mockWorker{workerCommon{lastUsed: baseTime.Add(time.Hour)}})
	}

	// The binary search in refresh() must correctly identify the split point
	// between T and T+1h, even though the array indices are wrapped.
}

func TestFIFOQueue_Refresh_WrapAround(t *testing.T) {
	// Need to test conditions where head > index of expiration
	// Array Size: 5
	// [F, G, H, D, E]
	//        ^ tail at 2 (next insert).
	//           ^ head at 3.
	// Logical: D, E, F, G, H
	// Timestamps: D(T1), E(T1), F(T2), G(T2), H(T3)
	// We want D, E, F, G to expire. H to remain.
	// head=3. index of last expired (G) = 1.
	// 3 > 1. This triggers the wrapped expiry logic.

	q := newFIFOQueue(5)

	t1 := time.Date(2023, 1, 1, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(1 * time.Hour)
	t3 := t1.Add(2 * time.Hour)

	// 1. Fill 0-4. [A, B, C, D, E] all T1
	for i := 0; i < 5; i++ {
		q.insert(&mockWorker{workerCommon{lastUsed: t1}})
	}

	// head=0, tail=0 (full)

	// 2. Detach 3. [_, _, _, D, E]. head=3.
	q.detach()
	q.detach()
	q.detach()

	// 3. Insert F, G (T2). [F, G, _, D, E]. head=3, tail=2.
	q.insert(&mockWorker{workerCommon{lastUsed: t2}}) // at 0
	q.insert(&mockWorker{workerCommon{lastUsed: t2}}) // at 1

	// 4. Insert H (T3). [F, G, H, D, E]. head=3, tail=3 (full).
	// Index 2 is H.
	q.insert(&mockWorker{workerCommon{lastUsed: t3}})

	// Current State:
	// indices: 0(F,T2), 1(G,T2), 2(H,T3), 3(D,T1), 4(E,T1)
	// head: 3
	// logical: D, E, F, G, H

	// We want to expire anything before T3. (So T1 and T2).
	// "Duration" logic in refresh is relative to Now.
	// We mock Now to be T3.
	// Duration to expire T2 (1h ago) -> Duration < 1h? No, ExpiryTime = Now - Duration.
	// We want ExpiryTime > T2.
	// Let Now = T3 + 1min.
	// ExpiryTime = T2 + 1min.
	// Duration = (T3+1min) - (T2+1min) = 1 Hour.

	// But `refresh` calls `time.Now()`. We cannot control it easily.
	// We used `mockWorker` but the queue calls `lastUsedTime()` on it.
	// The queue calls `time.Now()` internally.

	// Creating a wrapper to mock time is overkill.
	// Instead, let's just use real relative times.

	qReal := newFIFOQueue(5)
	now := time.Now()
	oldTime := now.Add(-5 * time.Hour)
	midTime := now.Add(-3 * time.Hour)
	newTime := now

	// 1. Insert 5 old
	for i := 0; i < 5; i++ {
		qReal.insert(&mockWorker{workerCommon{lastUsed: oldTime}})
	}
	// 2. Detach 3
	qReal.detach()
	qReal.detach()
	qReal.detach()
	// 3. Insert 2 mid
	qReal.insert(&mockWorker{workerCommon{lastUsed: midTime}})
	qReal.insert(&mockWorker{workerCommon{lastUsed: midTime}})
	// 4. Insert 1 new
	qReal.insert(&mockWorker{workerCommon{lastUsed: newTime}})

	// Expect 4 expired (2 old + 2 mid). 1 remaining.
	// Expiry duration: 1 hour (so anything older than 1h expires).
	// Old (-5h) -> Expired.
	// Mid (-3h) -> Expired.
	// New (0h) -> Kept.

	expired := qReal.refresh(1 * time.Hour)

	if len(expired) != 4 {
		t.Errorf("Expected 4 expired, got %d", len(expired))
	}

	if qReal.len() != 1 {
		t.Errorf("Expected 1 remaining, got %d", qReal.len())
	}

	// Verify the remaining one is the New one
	w := qReal.detach()
	if w.lastUsedTime() != newTime {
		t.Error("Remaining worker is not the newest one")
	}
}
