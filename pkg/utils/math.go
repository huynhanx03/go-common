package utils

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

	n--
	n |= n >> 1
	n |= n >> 2
	n |= n >> 4
	n |= n >> 8
	n |= n >> 16
	n |= n >> 32
	n++

	return n
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
