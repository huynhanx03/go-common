package workerpool

import (
	"testing"
	"time"
)

func TestNewLIFOQueue(t *testing.T) {
	size := 100
	q := newLIFOQueue(size)
	if q.len() != 0 {
		t.Errorf("NewLIFOQueue len = %d, want 0", q.len())
	}
	if !q.isEmpty() {
		t.Errorf("NewLIFOQueue isEmpty = false, want true")
	}
	if q.detach() != nil {
		t.Errorf("NewLIFOQueue detach should be nil")
	}
}

func TestLIFOQueue_InsertDetach(t *testing.T) {
	q := newLIFOQueue(10)

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
}

func TestLIFOQueue_Refresh(t *testing.T) {
	q := newLIFOQueue(10)
	duration := 100 * time.Millisecond

	// LIFO Queue (Stack) stores workers where:
	// - Older workers are at the beginning (index 0)
	// - Newer workers are appended to the end (index N-1)
	//
	// Since workers are sorted by lastUsedTime (Oldest -> Newest),
	// refresh() checks from index 0 and removes the contiguous block of expired workers.

	// Insert expired workers (Oldest)
	expiredCount := 5
	for i := 0; i < expiredCount; i++ {
		q.insert(&mockWorker{workerCommon{lastUsed: time.Now().Add(-200 * time.Millisecond)}})
	}

	// Insert fresh workers (Newest)
	freshCount := 5
	for i := 0; i < freshCount; i++ {
		q.insert(&mockWorker{workerCommon{lastUsed: time.Now()}})
	}

	if q.len() != 10 {
		t.Fatalf("Queue len = %d, want 10", q.len())
	}

	expiredWorkers := q.refresh(duration)
	if len(expiredWorkers) != expiredCount {
		t.Errorf("Expected %d expired workers, got %d", expiredCount, len(expiredWorkers))
	}
	if q.len() != freshCount {
		t.Errorf("Queue len after refresh = %d, want %d", q.len(), freshCount)
	}
}
