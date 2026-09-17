package consistenthash

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
)

func TestNewAndValidation(t *testing.T) {
	r, err := New[string](Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.opts.virtualNodes != 256 || r.opts.maxWeight != 64 || r.opts.maxTotalTokens != 1<<20 {
		t.Fatalf("unexpected defaults: %#v", r.opts)
	}
	if _, err := New[string](Options{MaxPrefixBits: 13}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("got %v", err)
	}
	if err := r.Replace([]Member[string]{{ID: "a"}, {ID: "a"}}); !errors.Is(err, ErrDuplicateMember) {
		t.Fatalf("got %v", err)
	}
	if err := r.Replace([]Member[string]{{}}); !errors.Is(err, ErrInvalidMember) {
		t.Fatalf("got %v", err)
	}
	if err := r.Replace([]Member[string]{{ID: string(make([]byte, 257))}}); !errors.Is(err, ErrIDLimit) {
		t.Fatalf("got %v", err)
	}
	limited, err := New[string](Options{VirtualNodes: 256, MaxTotalTokens: 128})
	if err != nil {
		t.Fatal(err)
	}
	if err := limited.Replace([]Member[string]{{ID: "a"}}); !errors.Is(err, ErrTooManyTokens) {
		t.Fatalf("got %v", err)
	}
	if limited.Generation() != 0 {
		t.Fatal("failed replace published")
	}
}

func TestReplaceDeterministicAndCompacted(t *testing.T) {
	left := mustRing(t, Options{VirtualNodes: 16})
	right := mustRing(t, Options{VirtualNodes: 16})
	a := []Member[string]{{ID: "c", Value: "C", Weight: 2}, {ID: "a", Value: "A"}, {ID: "b", Value: "B", Weight: 3}}
	b := []Member[string]{{ID: "b", Value: "ignore", Weight: 3}, {ID: "c", Value: "ignore", Weight: 2}, {ID: "a", Value: "ignore"}}
	if err := left.Replace(a); err != nil {
		t.Fatal(err)
	}
	if err := right.Replace(b); err != nil {
		t.Fatal(err)
	}
	ls, rs := left.state.Load(), right.state.Load()
	if left.Checksum() != right.Checksum() || !reflect.DeepEqual(ls.points, rs.points) || !reflect.DeepEqual(ls.owners16, rs.owners16) {
		t.Fatal("input order or payload changed mapping")
	}
	if len(ls.points) != 16*6 || len(ls.owners32) != 0 || len(ls.owners16) != len(ls.points) {
		t.Fatal("unexpected compact layout")
	}
	for i := range ls.points {
		if i > 0 && ls.points[i-1] >= ls.points[i] {
			t.Fatal("points not strictly sorted")
		}
		if int(ls.owners16[i]) >= len(ls.members) {
			t.Fatal("invalid owner")
		}
	}
	if ls.members[0].ID != "a" || ls.members[1].ID != "b" {
		t.Fatal("members not canonical")
	}
}

func TestFailedReplacePreservesPublishedSnapshot(t *testing.T) {
	r := mustRing(t, Options{VirtualNodes: 4})
	if err := r.Replace([]Member[string]{{ID: "a"}, {ID: "b"}}); err != nil {
		t.Fatal(err)
	}
	beforeGeneration, beforeChecksum := r.Generation(), r.Checksum()
	if err := r.Replace([]Member[string]{{ID: "a"}, {ID: "a"}}); !errors.Is(err, ErrDuplicateMember) {
		t.Fatalf("got %v", err)
	}
	if r.Generation() != beforeGeneration || r.Checksum() != beforeChecksum {
		t.Fatal("failed update changed state")
	}
}

func TestChecksumIgnoresPayloadAndLocalRepresentation(t *testing.T) {
	a := mustRing(t, Options{VirtualNodes: 4, PrefixTarget: 1})
	b := mustRing(t, Options{VirtualNodes: 4, PrefixTarget: 1024})
	left := []Member[string]{{ID: "a", Value: "local-a"}, {ID: "b", Value: "local-b", Weight: 2}}
	right := []Member[string]{{ID: "b", Value: "different", Weight: 2}, {ID: "a", Value: "different"}}
	if err := a.Replace(left); err != nil {
		t.Fatal(err)
	}
	if err := b.Replace(right); err != nil {
		t.Fatal(err)
	}
	if a.Checksum() != b.Checksum() {
		t.Fatal("non-routing state changed checksum")
	}
}

func TestLookupBoundariesAndTopK(t *testing.T) {
	r := mustRing(t, Options{VirtualNodes: 8})
	if _, ok := r.Lookup([]byte("x")); ok {
		t.Fatal("empty ring lookup")
	}
	if err := r.Replace([]Member[string]{{ID: "only", Value: "v"}}); err != nil {
		t.Fatal(err)
	}
	if got, ok := r.LookupHash(math.MaxUint64); !ok || got.ID != "only" || len(r.state.Load().points) != 0 {
		t.Fatal("singleton")
	}
	if err := r.Replace([]Member[string]{{ID: "a"}, {ID: "b"}, {ID: "c"}}); err != nil {
		t.Fatal(err)
	}
	s := r.state.Load()
	for _, h := range []uint64{0, s.points[0], s.points[len(s.points)-1], math.MaxUint64} {
		got, ok := r.LookupHash(h)
		if !ok || got.ID == "" {
			t.Fatalf("invalid lookup for %d", h)
		}
	}
	dst := make([]Member[string], 0, 3)
	got := r.LookupNHashInto(dst, 1, 3)
	if len(got) != 3 {
		t.Fatal(got)
	}
	for i := range got {
		for j := 0; j < i; j++ {
			if got[i].ID == got[j].ID {
				t.Fatal("duplicate candidate")
			}
		}
	}
}

func TestLookupZeroAllocations(t *testing.T) {
	r := mustRing(t, Options{VirtualNodes: 64})
	if err := r.Replace([]Member[string]{{ID: "a"}, {ID: "b"}}); err != nil {
		t.Fatal(err)
	}
	if n := testing.AllocsPerRun(1000, func() { _, _ = r.Lookup([]byte("key")) }); n != 0 {
		t.Fatalf("lookup allocs=%v", n)
	}
	buf := make([]Member[string], 0, 2)
	if n := testing.AllocsPerRun(1000, func() { _ = r.LookupNHashInto(buf[:0], 42, 2) }); n != 0 {
		t.Fatalf("top-k allocs=%v", n)
	}
}

func TestOwnerIndexRepresentation(t *testing.T) {
	build := func(count int) *Ring[string] {
		t.Helper()
		r := mustRing(t, Options{VirtualNodes: 1, MaxMembers: uint32(count)})
		members := make([]Member[string], count)
		for i := range members {
			members[i].ID = fmt.Sprintf("n-%05d", i)
		}
		if err := r.Replace(members); err != nil {
			t.Fatal(err)
		}
		return r
	}
	if got := build(1 << 16).state.Load(); len(got.owners16) == 0 || len(got.owners32) != 0 {
		t.Fatal("expected narrow owner index")
	}
	if got := build((1 << 16) + 1).state.Load(); len(got.owners32) == 0 || len(got.owners16) != 0 {
		t.Fatal("expected wide owner index")
	}
}

func TestPrefixPreservesFullRingSearch(t *testing.T) {
	r := mustRing(t, Options{VirtualNodes: 128, PrefixTarget: 2})
	members := make([]Member[string], 32)
	for i := range members {
		members[i].ID = fmt.Sprintf("node-%d", i)
	}
	if err := r.Replace(members); err != nil {
		t.Fatal(err)
	}
	s := r.state.Load()
	if s.prefixBits == 0 {
		t.Fatal("expected prefix index")
	}
	for i := uint64(0); i < 100_000; i++ {
		h := i * 0x9e3779b97f4a7c15
		if got, want := s.search(h), lowerBound(s.points, h, 0, len(s.points)); got != want && !(want == len(s.points) && got == 0) {
			t.Fatalf("hash %d: prefix=%d full=%d", h, got, want)
		}
	}
}

func TestReplaceConcurrent(t *testing.T) {
	r := mustRing(t, Options{VirtualNodes: 8})
	if err := r.Replace([]Member[string]{{ID: "a"}, {ID: "b"}}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(i int) {
			for j := 0; j < 5000; j++ {
				_, _ = r.LookupHash(uint64(i*5000 + j))
			}
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 50; i++ {
		if err := r.Replace([]Member[string]{{ID: "a"}, {ID: fmt.Sprintf("b-%d", i)}}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}

func mustRing(t *testing.T, opts Options) *Ring[string] {
	t.Helper()
	r, err := New[string](opts)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
