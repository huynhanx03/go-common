package consistenthash

import (
	"errors"
	"fmt"
)

// AlgorithmVersion identifies the canonical token and checksum encoding.
// Processes that must agree on placement must use the same version.
const AlgorithmVersion uint32 = 1

const algorithmVersion = AlgorithmVersion

const (
	defaultVirtualNodes      uint32 = 256
	defaultMaxWeight         uint16 = 64
	defaultMaxTotalTokens    uint32 = 1 << 20
	defaultMaxMembers        uint32 = 1 << 16
	defaultMaxMemberIDBytes  uint32 = 256
	defaultMaxTotalIDBytes   uint32 = 1 << 20
	defaultPrefixTarget      uint32 = 8
	defaultMaxPrefixBits     uint8  = 12
	defaultCollisionAttempts uint8  = 8
)

var (
	ErrInvalidOptions  = errors.New("consistenthash: invalid options")
	ErrInvalidMember   = errors.New("consistenthash: invalid member")
	ErrDuplicateMember = errors.New("consistenthash: duplicate member")
	ErrTooManyTokens   = errors.New("consistenthash: too many tokens")
	ErrMemberLimit     = errors.New("consistenthash: too many members")
	ErrIDLimit         = errors.New("consistenthash: member ID limit exceeded")
	ErrTokenCollision  = errors.New("consistenthash: token collision")
)

// Member combines a stable routing identity with caller-owned metadata. ID is
// the compatibility boundary: changing it intentionally remaps keys. Weight
// is static capacity; zero means one.
type Member[T any] struct {
	ID     string
	Value  T
	Weight uint16
}

// Options controls resource bounds and snapshot representation. Zero values
// use production defaults. These options are not part of routing compatibility
// except VirtualNodes and CollisionAttempts.
type Options struct {
	VirtualNodes      uint32
	MaxWeight         uint16
	MaxTotalTokens    uint32
	MaxMembers        uint32
	MaxMemberIDBytes  uint32
	MaxTotalIDBytes   uint32
	PrefixTarget      uint32
	MaxPrefixBits     uint8
	CollisionAttempts uint8
}

type normalizedOptions struct {
	virtualNodes, maxTotalTokens, maxMembers, maxMemberIDBytes, maxTotalIDBytes, prefixTarget uint32
	maxWeight                                                                                 uint16
	maxPrefixBits, collisionAttempts                                                          uint8
}

func normalizeOptions(in Options) (normalizedOptions, error) {
	o := normalizedOptions{
		virtualNodes: in.VirtualNodes, maxWeight: in.MaxWeight, maxTotalTokens: in.MaxTotalTokens,
		maxMembers: in.MaxMembers, maxMemberIDBytes: in.MaxMemberIDBytes, maxTotalIDBytes: in.MaxTotalIDBytes,
		prefixTarget: in.PrefixTarget, maxPrefixBits: in.MaxPrefixBits, collisionAttempts: in.CollisionAttempts,
	}
	if o.virtualNodes == 0 {
		o.virtualNodes = defaultVirtualNodes
	}
	if o.maxWeight == 0 {
		o.maxWeight = defaultMaxWeight
	}
	if o.maxTotalTokens == 0 {
		o.maxTotalTokens = defaultMaxTotalTokens
	}
	if o.maxMembers == 0 {
		o.maxMembers = defaultMaxMembers
	}
	if o.maxMemberIDBytes == 0 {
		o.maxMemberIDBytes = defaultMaxMemberIDBytes
	}
	if o.maxTotalIDBytes == 0 {
		o.maxTotalIDBytes = defaultMaxTotalIDBytes
	}
	if o.prefixTarget == 0 {
		o.prefixTarget = defaultPrefixTarget
	}
	if o.maxPrefixBits == 0 {
		o.maxPrefixBits = defaultMaxPrefixBits
	}
	if o.collisionAttempts == 0 {
		o.collisionAttempts = defaultCollisionAttempts
	}
	if o.virtualNodes > defaultMaxTotalTokens || o.maxWeight > 1<<14 || o.maxTotalTokens > defaultMaxTotalTokens ||
		o.maxMembers > defaultMaxTotalTokens || o.maxMemberIDBytes > defaultMaxTotalIDBytes ||
		o.maxTotalIDBytes > defaultMaxTotalIDBytes || o.prefixTarget > defaultMaxTotalTokens ||
		o.maxPrefixBits > defaultMaxPrefixBits || o.collisionAttempts > 64 {
		return normalizedOptions{}, fmt.Errorf("%w: value exceeds supported maximum", ErrInvalidOptions)
	}
	return o, nil
}
