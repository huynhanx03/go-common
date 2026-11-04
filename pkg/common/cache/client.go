package cache

import (
	"sync"

	"golang.org/x/sync/singleflight"

	"github.com/huynhanx03/go-common/pkg/logger"
)

// Config selects the optional in-process and shared cache tiers.
type Config struct {
	L1     L1Engine
	L2     L2Engine
	Logger *logger.Logger
}

// Cache coordinates the configured cache tiers.
type Cache struct {
	tiers tierCoordinator
	sf    singleflight.Group
	l1sf  singleflight.Group
	once  sync.Once
}

// New creates a cache using at least one tier and the shared logger.
func New(config Config) (*Cache, error) {
	if config.Logger == nil || (config.L1 == nil && config.L2 == nil) {
		return nil, ErrInvalidConfig
	}
	return &Cache{tiers: tierCoordinator{l1: config.L1, l2: config.L2, log: config.Logger}}, nil
}

// HasL1 reports whether this cache uses a process-local tier.
func (c *Cache) HasL1() bool {
	return c.tiers.l1 != nil
}

// HasL2 reports whether this cache uses a shared byte tier.
func (c *Cache) HasL2() bool {
	return c.tiers.l2 != nil
}

// Close releases the configured cache engines once.
func (c *Cache) Close() {
	c.once.Do(func() {
		c.tiers.close()
	})
}
