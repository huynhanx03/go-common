package consistenthash

import (
	"encoding/binary"
	"fmt"
	"math/bits"
	"sort"
	"strings"

	"github.com/cespare/xxhash/v2"
)

const tokenDomain = "go-common/consistenthash"

type token struct {
	point uint64
	owner uint32
}

func buildSnapshot[T any](opts normalizedOptions, input []Member[T], generation uint64) (*snapshot[T], error) {
	if uint64(len(input)) > uint64(opts.maxMembers) {
		return nil, fmt.Errorf("%w: %d > %d", ErrMemberLimit, len(input), opts.maxMembers)
	}
	members := make([]Member[T], len(input))
	seen := make(map[string]struct{}, len(input))
	var idBytes, logical uint64
	for i, member := range input {
		if member.ID == "" {
			return nil, fmt.Errorf("%w: empty ID", ErrInvalidMember)
		}
		if uint64(len(member.ID)) > uint64(opts.maxMemberIDBytes) {
			return nil, fmt.Errorf("%w: ID %q exceeds per-member limit", ErrIDLimit, member.ID)
		}
		idBytes += uint64(len(member.ID))
		if idBytes > uint64(opts.maxTotalIDBytes) {
			return nil, fmt.Errorf("%w: total ID bytes", ErrIDLimit)
		}
		if _, ok := seen[member.ID]; ok {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateMember, member.ID)
		}
		seen[member.ID] = struct{}{}
		weight := member.Weight
		if weight == 0 {
			weight = 1
		}
		if weight > opts.maxWeight {
			return nil, fmt.Errorf("%w: %q has weight %d", ErrInvalidMember, member.ID, weight)
		}
		logical += uint64(opts.virtualNodes) * uint64(weight)
		if logical > uint64(opts.maxTotalTokens) {
			return nil, fmt.Errorf("%w: %d > %d", ErrTooManyTokens, logical, opts.maxTotalTokens)
		}
		member.ID = strings.Clone(member.ID)
		member.Weight = weight
		members[i] = member
	}
	sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
	entries, err := buildTokens(opts, members, int(logical))
	if err != nil {
		return nil, err
	}
	s := &snapshot[T]{generation: generation, members: members}
	s.checksum = checksum(opts, members, entries)
	if len(members) < 2 {
		return s, nil
	}
	s.points = make([]uint64, len(entries))
	if len(members) <= 1<<16 {
		s.owners16 = make([]uint16, len(entries))
		for i, e := range entries {
			s.points[i], s.owners16[i] = e.point, uint16(e.owner)
		}
	} else {
		s.owners32 = make([]uint32, len(entries))
		for i, e := range entries {
			s.points[i], s.owners32[i] = e.point, e.owner
		}
	}
	s.prefixBits, s.prefix = makePrefix(s.points, opts.prefixTarget, opts.maxPrefixBits)
	return s, nil
}

func buildTokens[T any](opts normalizedOptions, members []Member[T], size int) ([]token, error) {
	entries := make([]token, 0, size)
	occupied := make(map[uint64]struct{}, size)
	buf := make([]byte, 0, len(tokenDomain)+4+4+256+4+1)
	for owner, member := range members {
		for ordinal := uint32(0); ordinal < opts.virtualNodes*uint32(member.Weight); ordinal++ {
			found := false
			for attempt := uint8(0); attempt < opts.collisionAttempts; attempt++ {
				buf = appendTokenRecord(buf[:0], member.ID, ordinal, attempt)
				point := xxhash.Sum64(buf)
				if _, exists := occupied[point]; exists {
					continue
				}
				occupied[point] = struct{}{}
				entries = append(entries, token{point: point, owner: uint32(owner)})
				found = true
				break
			}
			if !found {
				return nil, fmt.Errorf("%w: member %q ordinal %d", ErrTokenCollision, member.ID, ordinal)
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].point < entries[j].point })
	return entries, nil
}

func appendTokenRecord(dst []byte, id string, ordinal uint32, attempt uint8) []byte {
	dst = append(dst, tokenDomain...)
	var fixed [4]byte
	binary.LittleEndian.PutUint32(fixed[:], algorithmVersion)
	dst = append(dst, fixed[:]...)
	binary.LittleEndian.PutUint32(fixed[:], uint32(len(id)))
	dst = append(dst, fixed[:]...)
	dst = append(dst, id...)
	binary.LittleEndian.PutUint32(fixed[:], ordinal)
	dst = append(dst, fixed[:]...)
	return append(dst, attempt)
}

func checksumEmpty(opts normalizedOptions) uint64 { return checksum[struct{}](opts, nil, nil) }

func checksum[T any](opts normalizedOptions, members []Member[T], entries []token) uint64 {
	buf := make([]byte, 0, 64)
	buf = append(buf, tokenDomain...)
	var fixed [8]byte
	binary.LittleEndian.PutUint32(fixed[:4], algorithmVersion)
	buf = append(buf, fixed[:4]...)
	binary.LittleEndian.PutUint32(fixed[:4], opts.virtualNodes)
	buf = append(buf, fixed[:4]...)
	buf = append(buf, opts.collisionAttempts)
	binary.LittleEndian.PutUint32(fixed[:4], uint32(len(members)))
	buf = append(buf, fixed[:4]...)
	for _, member := range members {
		binary.LittleEndian.PutUint32(fixed[:4], uint32(len(member.ID)))
		buf = append(buf, fixed[:4]...)
		buf = append(buf, member.ID...)
		binary.LittleEndian.PutUint16(fixed[:2], member.Weight)
		buf = append(buf, fixed[:2]...)
	}
	for _, entry := range entries {
		binary.LittleEndian.PutUint64(fixed[:], entry.point)
		buf = append(buf, fixed[:]...)
		binary.LittleEndian.PutUint32(fixed[:4], entry.owner)
		buf = append(buf, fixed[:4]...)
	}
	return xxhash.Sum64(buf)
}

func makePrefix(points []uint64, target uint32, maxBits uint8) (uint8, []uint32) {
	if len(points) < int(target)*4 || maxBits == 0 {
		return 0, nil
	}
	ratio := (uint64(len(points)) + uint64(target) - 1) / uint64(target)
	bitsCount := uint8(bits.Len64(ratio - 1))
	if bitsCount < 2 {
		return 0, nil
	}
	if bitsCount > maxBits {
		bitsCount = maxBits
	}
	buckets := 1 << bitsCount
	prefix := make([]uint32, buckets+1)
	i := 0
	for b := 0; b <= buckets; b++ {
		if b == buckets {
			prefix[b] = uint32(len(points))
			break
		}
		threshold := uint64(b) << (64 - bitsCount)
		for i < len(points) && points[i] < threshold {
			i++
		}
		prefix[b] = uint32(i)
	}
	return bitsCount, prefix
}
