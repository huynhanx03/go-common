package consistenthash

import "github.com/cespare/xxhash/v2"

// Lookup returns the clockwise owner of key.
func (r *Ring[T]) Lookup(key []byte) (Member[T], bool) { return r.LookupHash(xxhash.Sum64(key)) }

// LookupHash is Lookup for callers that already have the canonical XXH64 key hash.
func (r *Ring[T]) LookupHash(sum uint64) (Member[T], bool) {
	s := r.state.Load()
	if len(s.members) == 0 {
		var zero Member[T]
		return zero, false
	}
	if len(s.members) == 1 {
		return s.members[0], true
	}
	idx := s.search(sum)
	if len(s.owners16) != 0 {
		return s.members[s.owners16[idx]], true
	}
	return s.members[s.owners32[idx]], true
}

// LookupNInto appends up to n distinct clockwise candidates to dst. It retains
// the caller's storage and does not allocate after the ring is built.
func (r *Ring[T]) LookupNInto(dst []Member[T], key []byte, n int) []Member[T] {
	return r.LookupNHashInto(dst, xxhash.Sum64(key), n)
}

// LookupNHashInto is LookupNInto for prehashed keys.
func (r *Ring[T]) LookupNHashInto(dst []Member[T], sum uint64, n int) []Member[T] {
	s := r.state.Load()
	if n <= 0 || len(s.members) == 0 {
		return dst
	}
	if len(s.members) == 1 {
		return append(dst, s.members[0])
	}
	if n > len(s.members) {
		n = len(s.members)
	}
	start := s.search(sum)
	if len(s.owners16) != 0 {
		return appendCandidates16(dst, s, start, n)
	}
	return appendCandidates32(dst, s, start, n)
}

func appendCandidates16[T any](dst []Member[T], s *snapshot[T], start, n int) []Member[T] {
	for step := 0; step < len(s.points) && n > 0; step++ {
		member := s.members[s.owners16[(start+step)%len(s.points)]]
		if containsID(dst, member.ID) {
			continue
		}
		dst = append(dst, member)
		n--
	}
	return dst
}
func appendCandidates32[T any](dst []Member[T], s *snapshot[T], start, n int) []Member[T] {
	for step := 0; step < len(s.points) && n > 0; step++ {
		member := s.members[s.owners32[(start+step)%len(s.points)]]
		if containsID(dst, member.ID) {
			continue
		}
		dst = append(dst, member)
		n--
	}
	return dst
}
func containsID[T any](members []Member[T], id string) bool {
	for _, member := range members {
		if member.ID == id {
			return true
		}
	}
	return false
}

func (s *snapshot[T]) search(sum uint64) int {
	lo, hi := 0, len(s.points)
	if s.prefixBits != 0 {
		bucket := int(sum >> (64 - s.prefixBits))
		lo, hi = int(s.prefix[bucket]), int(s.prefix[bucket+1])
	}
	idx := lowerBound(s.points, sum, lo, hi)
	if idx == len(s.points) {
		return 0
	}
	return idx
}
func lowerBound(values []uint64, target uint64, lo, hi int) int {
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if values[mid] < target {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}
