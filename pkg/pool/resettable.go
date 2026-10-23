package pool

// Resettable values report their size and can be reset before reuse.
type Resettable interface {
	Len() int
	Reset()
}

// NewResettable creates a pool for Resettable values.
func NewResettable[T Resettable](newFunc func(size int) T) *Pool[T] {
	return New(
		newFunc,
		func(value T) int { return value.Len() },
		func(value T) { value.Reset() },
	)
}
