package tinylfu

// Item represents a cache item with its metadata.
type Item[V any] struct {
	Key        uint64
	Conflict   uint64
	Value      V
	Cost       int64
	Expiration int64 // Unix timestamp, 0 means no expiration
}

// IsExpired returns true if the item has expired.
func (i *Item[V]) IsExpired(now int64) bool {
	return i.Expiration > 0 && now > i.Expiration
}
