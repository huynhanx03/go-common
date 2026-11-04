package ristretto

import (
	"sync"

	"github.com/dgraph-io/ristretto/v2"

	"github.com/huynhanx03/go-common/pkg/common/cache"
	"github.com/huynhanx03/go-common/pkg/hash"
	commonsync "github.com/huynhanx03/go-common/pkg/sync"
)

var _ cache.L1Engine = (*Cache[string, any])(nil)

// Stats contains metrics reported by the local Ristretto cache.
type Stats struct {
	Hits      int64
	Misses    int64
	Evictions int64
	KeyCount  int64
	CostUsed  int64
}

// Cache wraps Ristretto and implements the shared cache L1 engine.
type Cache[K hash.Key, V any] struct {
	inner   *ristretto.Cache[K, any]
	mu      commonsync.RBMutex
	fenceMu [fenceStripeCount]sync.Mutex
	closed  bool
}

// New creates a Ristretto client using the default configuration and options.
func New[K hash.Key, V any](opts ...Option) (*Cache[K, V], error) {
	cfg := DefaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	innerConfig := ristretto.Config[K, any]{
		NumCounters: cfg.NumCounters,
		MaxCost:     cfg.MaxCost,
		BufferItems: cfg.BufferItems,
		Metrics:     cfg.Metrics,
		Cost:        cfg.Cost,
	}

	inner, err := ristretto.NewCache[K, any](&innerConfig)
	if err != nil {
		return nil, err
	}
	return &Cache[K, V]{inner: inner, mu: commonsync.NewRBMutex()}, nil
}

// Close shuts down the client once.
func (c *Cache[K, V]) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	c.inner.Close()
}

// Stats returns a snapshot of Ristretto's local metrics.
func (c *Cache[K, V]) Stats() Stats {
	token := c.mu.RLock()
	defer c.mu.RUnlock(token)
	var stats Stats
	if c.closed {
		return stats
	}
	if metrics := c.inner.Metrics; metrics != nil {
		stats.Hits = int64(metrics.Hits())
		stats.Misses = int64(metrics.Misses())
		stats.Evictions = int64(metrics.KeysEvicted())
		stats.KeyCount = int64(metrics.KeysAdded() - metrics.KeysEvicted())
		stats.CostUsed = int64(metrics.CostAdded() - metrics.CostEvicted())
	}
	return stats
}
