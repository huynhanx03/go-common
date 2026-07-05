package cache

import "github.com/pkg/errors"

var (
	// ErrKeyNotFound reports a cache-layer miss: the cache key is absent.
	ErrKeyNotFound = errors.New("key not found")

	// ErrNotFound signals from a Fetch loader that the entity itself does not
	// exist (as opposed to merely not being cached). Return or wrap it from
	// fn and the Fetch helpers cache that outcome briefly (negative caching),
	// so lookups of nonexistent IDs stop hammering the source.
	ErrNotFound = errors.New("entity not found")
)
