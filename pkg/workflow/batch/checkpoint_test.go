package batch

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/coordination"
	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/settings"
	"go.uber.org/zap"
)

func TestBatchRun(t *testing.T) {
	t.Run("commits three pages, makes completed reruns a no-op, and isolates keys", func(t *testing.T) {
		store := newMemoryStore()
		runner := newTestRunner(t, store, Config{})
		definition := testDefinition("job/a")
		var calls int
		var observed []Result
		step := func(_ context.Context, checkpoint Checkpoint) (Progress, error) {
			calls++
			next := byte(1)
			if len(checkpoint.Cursor) != 0 {
				next = checkpoint.Cursor[0] + 1
			}
			return Progress{Cursor: []byte{next}, Processed: 1, Done: next == 3}, nil
		}
		result, err := runner.Run(context.Background(), definition, step, WithProgressObserver(func(_ context.Context, result Result) {
			observed = append(observed, result)
		}))
		if err != nil {
			t.Fatal(err)
		}
		if result != (Result{Key: definition.Key, Version: 3, ProcessedCount: 3, Done: true}) || calls != 3 {
			t.Fatalf("Run() = %#v after %d calls; want three committed pages", result, calls)
		}
		if len(observed) != 3 || observed[0].Version != 1 || observed[2] != result {
			t.Fatalf("observed committed progress = %#v", observed)
		}
		if repeated, err := runner.Run(context.Background(), definition, step); err != nil || repeated != result || calls != 3 {
			t.Fatalf("completed rerun = %#v, %v after %d calls; want no additional work", repeated, err, calls)
		}

		other := testDefinition("job/b")
		otherResult, err := runner.Run(context.Background(), other, func(context.Context, Checkpoint) (Progress, error) {
			return Progress{Cursor: []byte{1}, Processed: 4, Done: true}, nil
		})
		if err != nil || otherResult.Version != 1 || otherResult.ProcessedCount != 4 {
			t.Fatalf("independent key result = %#v, %v", otherResult, err)
		}
	})

	t.Run("callback failure keeps the last committed cursor and observer result", func(t *testing.T) {
		store := newMemoryStore()
		runner := newTestRunner(t, store, Config{})
		definition := testDefinition("job/failure")
		callbackFailure := errors.New("page query failed")
		var calls int
		var observed []Result
		result, err := runner.Run(context.Background(), definition, func(_ context.Context, checkpoint Checkpoint) (Progress, error) {
			calls++
			if checkpoint.Version == 1 {
				return Progress{}, callbackFailure
			}
			return Progress{Cursor: []byte{1}, Processed: 1}, nil
		}, WithProgressObserver(func(_ context.Context, result Result) { observed = append(observed, result) }))
		if !errors.Is(err, callbackFailure) {
			t.Fatalf("Run() error = %v; want callback failure", err)
		}
		checkpoint := store.checkpoint(definition.Key)
		if result.Version != 1 || result.ProcessedCount != 1 || result.Done || checkpoint.Version != 1 || !reflect.DeepEqual(checkpoint.Cursor, []byte{1}) {
			t.Fatalf("failed run result/checkpoint = %#v / %#v", result, checkpoint)
		}
		if checkpoint.Status != Ready || len(observed) != 1 || observed[0].Version != 1 || calls != 2 {
			t.Fatalf("failure state = %#v, observed %#v, calls %d", checkpoint, observed, calls)
		}
	})

	t.Run("definition conflicts stop before claim or callback", func(t *testing.T) {
		store := newMemoryStore()
		runner := newTestRunner(t, store, Config{})
		original := testDefinition("job/identity")
		if _, err := runner.Run(context.Background(), original, func(context.Context, Checkpoint) (Progress, error) {
			return Progress{Done: true}, nil
		}); err != nil {
			t.Fatal(err)
		}
		store.resetObservations()
		mismatch := original
		mismatch.InputFingerprint = "different-input"
		var calls int
		if _, err := runner.Run(context.Background(), mismatch, func(context.Context, Checkpoint) (Progress, error) {
			calls++
			return Progress{}, nil
		}); !errors.Is(err, ErrDefinitionConflict) {
			t.Fatalf("Run() error = %v; want definition conflict", err)
		}
		if calls != 0 || store.acquireCount != 0 {
			t.Fatalf("conflicting input called step %d times and acquired %d times", calls, store.acquireCount)
		}
	})

	t.Run("invalid progress rolls back and does not publish it", func(t *testing.T) {
		cases := []struct {
			name     string
			seed     func(*memoryStore, Definition)
			progress Progress
		}{
			{name: "unchanged nonterminal cursor", seed: func(store *memoryStore, definition Definition) { store.seed(definition, Checkpoint{Cursor: []byte{1}}) }, progress: Progress{Cursor: []byte{1}, Processed: 1}},
			{name: "oversized cursor", progress: Progress{Cursor: []byte{1, 2, 3}, Processed: 1}},
			{name: "negative count", progress: Progress{Cursor: []byte{1}, Processed: -1}},
			{name: "processed count overflow", seed: func(store *memoryStore, definition Definition) {
				store.seed(definition, Checkpoint{ProcessedCount: math.MaxInt64})
			}, progress: Progress{Cursor: []byte{1}, Processed: 1}},
			{name: "version overflow", seed: func(store *memoryStore, definition Definition) {
				store.seed(definition, Checkpoint{Version: math.MaxInt64})
			}, progress: Progress{Cursor: []byte{1}, Processed: 1}},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				store := newMemoryStore()
				definition := testDefinition("job/invalid/" + test.name)
				if test.seed != nil {
					test.seed(store, definition)
				}
				wantVersion := int64(0)
				wantProcessed := int64(0)
				if test.name == "version overflow" {
					wantVersion = math.MaxInt64
				}
				if test.name == "processed count overflow" {
					wantProcessed = math.MaxInt64
				}
				runner := newTestRunner(t, store, Config{MaxCursorBytes: 2})
				var observed int
				_, err := runner.Run(context.Background(), definition, func(context.Context, Checkpoint) (Progress, error) {
					return test.progress, nil
				}, WithProgressObserver(func(context.Context, Result) { observed++ }))
				if !errors.Is(err, ErrInvalidProgress) {
					t.Fatalf("Run() error = %v; want invalid progress", err)
				}
				stored := store.checkpoint(definition.Key)
				if observed != 0 || stored.Version != wantVersion || stored.ProcessedCount != wantProcessed {
					t.Fatalf("invalid progress committed or was observed: %#v", store.checkpoint(definition.Key))
				}
			})
		}
	})

	t.Run("limits inputs, segments, and page execution", func(t *testing.T) {
		t.Run("rejects incomplete identity and invalid budgets before opening", func(t *testing.T) {
			store := newMemoryStore()
			runner := newTestRunner(t, store, Config{})
			incomplete := testDefinition("job/incomplete")
			incomplete.CursorSchema = ""
			if _, err := runner.Run(context.Background(), incomplete, func(context.Context, Checkpoint) (Progress, error) { return Progress{}, nil }); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("Run() error = %v; want invalid identity", err)
			}
			if store.openCount != 0 {
				t.Fatalf("Open called %d times for an incomplete identity", store.openCount)
			}
			if _, err := New(store, logger.Wrap(zap.NewNop()), Config{MaxPages: -1}); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("New() error = %v; want invalid page bound", err)
			}
			if _, err := New(store, logger.Wrap(zap.NewNop()), Config{PageTimeout: 3 * time.Second, Lease: settings.Coordination{OperationTimeoutMilliseconds: 2000}}); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("New() error = %v; want page budget below lease operation budget", err)
			}
		})

		t.Run("boundary is bounded before opening a checkpoint", func(t *testing.T) {
			store := newMemoryStore()
			runner := newTestRunner(t, store, Config{MaxBoundaryBytes: 2})
			definition := testDefinition("job/boundary")
			definition.Boundary = []byte{1, 2, 3}
			if _, err := runner.Run(context.Background(), definition, func(context.Context, Checkpoint) (Progress, error) { return Progress{}, nil }); !errors.Is(err, ErrInvalidProgress) {
				t.Fatalf("Run() error = %v; want invalid boundary", err)
			}
			if store.openCount != 0 {
				t.Fatalf("Open called %d times for an oversized boundary", store.openCount)
			}
		})

		t.Run("page budget returns the committed nonterminal checkpoint", func(t *testing.T) {
			store := newMemoryStore()
			runner := newTestRunner(t, store, Config{MaxPages: 2})
			result, err := runner.Run(context.Background(), testDefinition("job/segment"), func(_ context.Context, checkpoint Checkpoint) (Progress, error) {
				return Progress{Cursor: []byte{byte(checkpoint.Version + 1)}, Processed: 1}, nil
			})
			if err != nil || result.Version != 2 || result.ProcessedCount != 2 || result.Done {
				t.Fatalf("segment result = %#v, %v", result, err)
			}
		})

		t.Run("page timeout leaves a resumable checkpoint", func(t *testing.T) {
			store := newMemoryStore()
			runner := newTestRunner(t, store, Config{PageTimeout: 20 * time.Millisecond})
			definition := testDefinition("job/timeout")
			result, err := runner.Run(context.Background(), definition, func(ctx context.Context, _ Checkpoint) (Progress, error) {
				<-ctx.Done()
				return Progress{}, ctx.Err()
			})
			if !errors.Is(err, context.DeadlineExceeded) || result.Version != 0 || store.checkpoint(definition.Key).Status != Ready {
				t.Fatalf("timed-out run = %#v, %v, checkpoint %#v", result, err, store.checkpoint(definition.Key))
			}
		})

		t.Run("caller cancellation releases ownership without completing the job", func(t *testing.T) {
			store := newMemoryStore()
			runner := newTestRunner(t, store, Config{})
			definition := testDefinition("job/canceled")
			ctx, cancel := context.WithCancel(context.Background())
			started := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				_, err := runner.Run(ctx, definition, func(ctx context.Context, _ Checkpoint) (Progress, error) {
					close(started)
					<-ctx.Done()
					return Progress{}, ctx.Err()
				})
				done <- err
			}()
			<-started
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("Run() error = %v; want caller cancellation", err)
			}
			checkpoint := store.checkpoint(definition.Key)
			if checkpoint.Status != Ready || checkpoint.Version != 0 || checkpoint.Owner != "" {
				t.Fatalf("canceled run left non-resumable checkpoint: %#v", checkpoint)
			}
		})
	})

	t.Run("reconciles an ambiguous page with the identical receipt key", func(t *testing.T) {
		for _, commitFirstAttempt := range []bool{true, false} {
			name := "receipt proves committed page"
			if !commitFirstAttempt {
				name = "rolled back page may be retried once"
			}
			t.Run(name, func(t *testing.T) {
				store := newMemoryStore()
				store.ambiguousFirstApply = true
				store.commitBeforeAmbiguous = commitFirstAttempt
				runner := newTestRunner(t, store, Config{})
				var calls int
				result, err := runner.Run(context.Background(), testDefinition("job/ambiguous"), func(_ context.Context, checkpoint Checkpoint) (Progress, error) {
					calls++
					return Progress{Cursor: []byte{1}, Processed: 2, Done: true}, nil
				})
				if err != nil || result.Version != 1 || result.ProcessedCount != 2 || !result.Done {
					t.Fatalf("Run() = %#v, %v", result, err)
				}
				if store.applyCount != 2 || len(store.stepIDs) != 2 || store.stepIDs[0] != store.stepIDs[1] {
					t.Fatalf("Apply calls %d, step IDs %#v; want one bounded retry with same receipt", store.applyCount, store.stepIDs)
				}
				wantCalls := 1
				if !commitFirstAttempt {
					wantCalls = 2
				}
				if calls != wantCalls {
					t.Fatalf("step calls = %d; want %d", calls, wantCalls)
				}
			})
		}
	})

	t.Run("handles completion between Open and the fenced claim", func(t *testing.T) {
		store := newMemoryStore()
		store.completeOnClaim = true
		runner := newTestRunner(t, store, Config{})
		var calls int
		result, err := runner.Run(context.Background(), testDefinition("job/completed-race"), func(context.Context, Checkpoint) (Progress, error) {
			calls++
			return Progress{}, nil
		})
		if err != nil || !result.Done || result.Version != 4 || calls != 0 {
			t.Fatalf("Run() = %#v, %v with %d step calls", result, err, calls)
		}
	})

	t.Run("a bad fence never starts a page", func(t *testing.T) {
		store := newMemoryStore()
		store.invalidGrant = true
		runner := newTestRunner(t, store, Config{})
		var calls int
		if _, err := runner.Run(context.Background(), testDefinition("job/bad-fence"), func(context.Context, Checkpoint) (Progress, error) {
			calls++
			return Progress{}, nil
		}); !errors.Is(err, coordination.ErrInvalidFence) {
			t.Fatalf("Run() error = %v; want invalid fence", err)
		}
		if calls != 0 {
			t.Fatalf("page callback called %d times with an invalid grant", calls)
		}
	})

	t.Run("callback receives a copy of checkpoint bytes", func(t *testing.T) {
		store := newMemoryStore()
		definition := testDefinition("job/copy")
		definition.Boundary = []byte{4, 5}
		store.seed(definition, Checkpoint{Cursor: []byte{1}})
		runner := newTestRunner(t, store, Config{})
		_, err := runner.Run(context.Background(), definition, func(_ context.Context, checkpoint Checkpoint) (Progress, error) {
			checkpoint.Cursor[0] = 9
			checkpoint.Definition.Boundary[0] = 9
			return Progress{Cursor: []byte{2}, Processed: 1, Done: true}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := store.checkpoint(definition.Key); !reflect.DeepEqual(got.Definition.Boundary, []byte{4, 5}) || !reflect.DeepEqual(got.Cursor, []byte{2}) {
			t.Fatalf("stored bytes changed through callback mutation: %#v", got)
		}
	})
}

type memoryStore struct {
	mu                    sync.Mutex
	checkpoints           map[string]Checkpoint
	fences                map[string]int64
	openCount             int
	acquireCount          int
	applyCount            int
	stepIDs               []string
	invalidGrant          bool
	completeOnClaim       bool
	ambiguousFirstApply   bool
	commitBeforeAmbiguous bool
	ambiguousUsed         bool
}

func newMemoryStore() *memoryStore {
	return &memoryStore{checkpoints: make(map[string]Checkpoint), fences: make(map[string]int64)}
}

func (s *memoryStore) Open(ctx context.Context, definition Definition) (Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return Checkpoint{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.openCount++
	checkpoint, ok := s.checkpoints[definition.Key]
	if !ok {
		now := time.Now()
		checkpoint = Checkpoint{Definition: cloneDefinition(definition), Status: Ready, CreatedAt: now, UpdatedAt: now}
		s.checkpoints[definition.Key] = cloneCheckpoint(checkpoint)
		return checkpoint, nil
	}
	if !sameDefinition(checkpoint.Definition, definition) {
		return Checkpoint{}, ErrDefinitionConflict
	}
	return cloneCheckpoint(checkpoint), nil
}

func (s *memoryStore) Get(ctx context.Context, key string) (Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return Checkpoint{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	checkpoint, ok := s.checkpoints[key]
	if !ok {
		return Checkpoint{}, ErrConflict
	}
	return cloneCheckpoint(checkpoint), nil
}

func (s *memoryStore) TryAcquire(context.Context, coordination.AcquireRequest) (coordination.Lease, error) {
	return nil, errors.New("advisory acquisition is not used by fenced batch runner")
}

func (s *memoryStore) TryAcquireFenced(_ context.Context, request coordination.AcquireRequest) (coordination.FencedLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acquireCount++
	checkpoint, ok := s.checkpoints[request.Resource]
	if !ok {
		return nil, ErrConflict
	}
	if s.completeOnClaim {
		checkpoint.Status = Completed
		checkpoint.Version = 4
		checkpoint.ProcessedCount = 4
		checkpoint.CompletedAt = timePointer(time.Now())
		s.checkpoints[request.Resource] = checkpoint
		return nil, ErrCompleted
	}
	if checkpoint.Status == Completed {
		return nil, ErrCompleted
	}
	if checkpoint.Status == Running && checkpoint.LeaseUntil.After(time.Now()) {
		return nil, coordination.ErrHeld
	}
	s.fences[request.Resource]++
	fenceValue := s.fences[request.Resource]
	if s.invalidGrant {
		fenceValue = 0
	}
	grant := coordination.Grant{Resource: request.Resource, Owner: request.Owner, Fence: coordination.FenceToken{Authority: "test-authority", Value: fenceValue}}
	checkpoint.Status = Running
	checkpoint.Owner = request.Owner
	checkpoint.Fence = grant.Fence
	checkpoint.LeaseUntil = time.Now().Add(request.TTL)
	s.checkpoints[request.Resource] = checkpoint
	return &memoryFencedLease{store: s, grant: grant, ttl: request.TTL}, nil
}

func (s *memoryStore) Apply(ctx context.Context, request StepRequest, step Step) (Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return Checkpoint{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyCount++
	s.stepIDs = append(s.stepIDs, request.StepID)
	checkpoint := s.checkpoints[request.Grant.Resource]
	if checkpoint.LastStepID == request.StepID && checkpoint.Version == request.ExpectedVersion+1 {
		return cloneCheckpoint(checkpoint), nil
	}
	if checkpoint.Status != Running || checkpoint.Owner != request.Grant.Owner || checkpoint.Fence != request.Grant.Fence || !checkpoint.LeaseUntil.After(time.Now()) {
		return Checkpoint{}, coordination.ErrLeaseLost
	}
	if checkpoint.Version != request.ExpectedVersion {
		return Checkpoint{}, ErrConflict
	}
	progress, err := step(ctx, cloneCheckpoint(checkpoint))
	if err != nil {
		return Checkpoint{}, err
	}
	if s.ambiguousFirstApply && !s.ambiguousUsed {
		s.ambiguousUsed = true
		if !s.commitBeforeAmbiguous {
			return Checkpoint{}, errors.Join(coordination.ErrAmbiguous, errors.New("commit response lost"))
		}
	}
	checkpoint.Cursor = append([]byte(nil), progress.Cursor...)
	checkpoint.Version++
	checkpoint.LastStepID = request.StepID
	checkpoint.ProcessedCount += progress.Processed
	checkpoint.UpdatedAt = time.Now()
	if progress.Done {
		checkpoint.Status = Completed
		checkpoint.CompletedAt = timePointer(checkpoint.UpdatedAt)
	}
	s.checkpoints[request.Grant.Resource] = cloneCheckpoint(checkpoint)
	if s.ambiguousFirstApply && s.ambiguousUsed && s.commitBeforeAmbiguous && s.applyCount == 1 {
		return Checkpoint{}, errors.Join(coordination.ErrAmbiguous, errors.New("commit response lost"))
	}
	return cloneCheckpoint(checkpoint), nil
}

func (s *memoryStore) seed(definition Definition, checkpoint Checkpoint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if checkpoint.CreatedAt.IsZero() {
		checkpoint.CreatedAt = time.Now()
	}
	checkpoint.Definition = cloneDefinition(definition)
	checkpoint.Status = Ready
	checkpoint.UpdatedAt = checkpoint.CreatedAt
	s.checkpoints[definition.Key] = cloneCheckpoint(checkpoint)
	s.fences[definition.Key] = checkpoint.Fence.Value
}

func (s *memoryStore) checkpoint(key string) Checkpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneCheckpoint(s.checkpoints[key])
}

func (s *memoryStore) resetObservations() { s.mu.Lock(); s.acquireCount = 0; s.mu.Unlock() }

type memoryFencedLease struct {
	store  *memoryStore
	grant  coordination.Grant
	ttl    time.Duration
	closed bool
}

func (l *memoryFencedLease) Grant() coordination.Grant { return l.grant }
func (l *memoryFencedLease) Renew(context.Context) error {
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	checkpoint := l.store.checkpoints[l.grant.Resource]
	if l.closed || checkpoint.Owner != l.grant.Owner || checkpoint.Fence != l.grant.Fence {
		return coordination.ErrLeaseLost
	}
	checkpoint.LeaseUntil = time.Now().Add(l.ttl)
	l.store.checkpoints[l.grant.Resource] = checkpoint
	return nil
}
func (l *memoryFencedLease) Release(context.Context) error {
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	checkpoint := l.store.checkpoints[l.grant.Resource]
	if l.closed {
		return nil
	}
	if checkpoint.Owner != l.grant.Owner || checkpoint.Fence != l.grant.Fence {
		return coordination.ErrLeaseLost
	}
	l.closed = true
	if checkpoint.Status == Running {
		checkpoint.Status = Ready
		checkpoint.Owner = ""
		checkpoint.LeaseUntil = time.Time{}
		l.store.checkpoints[l.grant.Resource] = checkpoint
	}
	return nil
}

func newTestRunner(t *testing.T, store Store, config Config) *Runner {
	t.Helper()
	if config.Lease.TTLMilliseconds == 0 {
		config.Lease.TTLMilliseconds = 30_000
	}
	if config.Lease.RenewIntervalMilliseconds == 0 {
		config.Lease.RenewIntervalMilliseconds = 10_000
	}
	if config.Lease.OperationTimeoutMilliseconds == 0 {
		config.Lease.OperationTimeoutMilliseconds = 2_000
	}
	if config.Lease.CleanupTimeoutMilliseconds == 0 {
		config.Lease.CleanupTimeoutMilliseconds = 1_000
	}
	if config.PageTimeout == 0 {
		config.PageTimeout = 500 * time.Millisecond
	}
	runner, err := New(store, logger.Wrap(zap.NewNop()), config)
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func testDefinition(key string) Definition {
	return Definition{Key: key, InputFingerprint: "input-v1", CursorSchema: "cursor-v1", Boundary: []byte("cutoff")}
}

func timePointer(value time.Time) *time.Time { return &value }
