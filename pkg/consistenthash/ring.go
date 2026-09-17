package consistenthash

import (
	"sync"
	"sync/atomic"
)

// Ring is safe for concurrent use. Replace serializes writers and readers see
// either the complete old snapshot or the complete new one.
type Ring[T any] struct {
	opts  normalizedOptions
	mu    sync.Mutex
	state atomic.Pointer[snapshot[T]]
}

type snapshot[T any] struct {
	generation uint64
	checksum   uint64
	members    []Member[T]
	points     []uint64
	owners16   []uint16
	owners32   []uint32
	prefix     []uint32
	prefixBits uint8
}

// New creates an empty ring.
func New[T any](opts Options) (*Ring[T], error) {
	normalized, err := normalizeOptions(opts)
	if err != nil {
		return nil, err
	}
	r := &Ring[T]{opts: normalized}
	r.state.Store(&snapshot[T]{checksum: checksumEmpty(normalized)})
	return r, nil
}

// Replace validates and atomically replaces the complete membership. It does
// not mutate the current snapshot when validation or construction fails.
func (r *Ring[T]) Replace(members []Member[T]) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	old := r.state.Load()
	next, err := buildSnapshot(r.opts, members, old.generation+1)
	if err != nil {
		return err
	}
	r.state.Store(next)
	return nil
}

// Generation increases after every successful Replace, including an empty one.
func (r *Ring[T]) Generation() uint64 { return r.state.Load().generation }

// Checksum identifies the routing-compatible logical ring, excluding payload
// and local representation choices.
func (r *Ring[T]) Checksum() uint64 { return r.state.Load().checksum }

// Len returns the number of physical members in the current snapshot.
func (r *Ring[T]) Len() int { return len(r.state.Load().members) }
