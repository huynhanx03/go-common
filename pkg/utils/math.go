package utils

import "math/bits"

const (
	bitSize       = 32 << (^uint(0) >> 63)
	maxIntHeadBit = 1 << (bitSize - 2)
)

// IsPowerOfTwo reports whether the given n is a power of two.
func IsPowerOfTwo(n int) bool {
	return n > 0 && n&(n-1) == 0
}

// CeilToPowerOfTwo returns n if it is a power-of-two, otherwise the next-highest power-of-two.
func CeilToPowerOfTwo(n int) int {
	if n&maxIntHeadBit != 0 && n > maxIntHeadBit {
		panic("argument is too large")
	}

	if n <= 2 {
		return 2
	}
	return 1 << bits.Len(uint(n-1))
}

// FloorToPowerOfTwo returns n if it is a power-of-two, otherwise the next-highest power-of-two.
func FloorToPowerOfTwo(n int) int {
	if n <= 2 {
		return n
	}

	n |= n >> 1
	n |= n >> 2
	n |= n >> 4
	n |= n >> 8
	n |= n >> 16

	return n - (n >> 1)
}

// ClosestPowerOfTwo returns n if it is a power-of-two, otherwise the closest power-of-two.
func ClosestPowerOfTwo(n int) int {
	next := CeilToPowerOfTwo(n)
	if prev := next / 2; (n - prev) < (next - n) {
		next = prev
	}
	return next
}

// Spread32 spreads the bits of a 32-bit integer into the even positions of a 64-bit integer.
// This is used for generating Morton codes (Z-order curve) by interleaving coordinates.
func Spread32(x uint32) uint64 {
	X := uint64(x)
	X = (X | (X << 16)) & 0x0000ffff0000ffff
	X = (X | (X << 8)) & 0x00ff00ff00ff00ff
	X = (X | (X << 4)) & 0x0f0f0f0f0f0f0f0f
	X = (X | (X << 2)) & 0x3333333333333333
	X = (X | (X << 1)) & 0x5555555555555555
	return X
}

// Squash64 extracts bits from the even positions of a 64-bit integer and
// squashes them back into a contiguous 32-bit integer.
func Squash64(X uint64) uint32 {
	X &= 0x5555555555555555
	X = (X | (X >> 1)) & 0x3333333333333333
	X = (X | (X >> 2)) & 0x0f0f0f0f0f0f0f0f
	X = (X | (X >> 4)) & 0x00ff00ff00ff00ff
	X = (X | (X >> 8)) & 0x0000ffff0000ffff
	X = (X | (X >> 16)) & 0x00000000ffffffff
	return uint32(X)
}