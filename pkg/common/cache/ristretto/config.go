package ristretto

// Config contains the settings shared by Ristretto cache key types.
type Config struct {
	NumCounters int64
	MaxCost     int64
	BufferItems int64
	Metrics     bool
	Cost        func(any) int64
}

// Option applies a cache configuration change.
type Option func(cfg *Config)

// WithMaxCost sets the maximum cost of the cache (in bytes by convention).
func WithMaxCost(maxCost int64) Option {
	return func(cfg *Config) {
		cfg.MaxCost = maxCost
	}
}

// WithNumCounters sets the number of counter rows for the TinyLFU policy.
// Recommended to be at least 10x the expected number of items.
func WithNumCounters(counters int64) Option {
	return func(cfg *Config) {
		cfg.NumCounters = counters
	}
}

// WithBufferItems sets the number of keys per Get buffer.
func WithBufferItems(items int64) Option {
	return func(cfg *Config) {
		cfg.BufferItems = items
	}
}

// WithMetrics enables or disables cache metrics collection.
func WithMetrics(enabled bool) Option {
	return func(cfg *Config) {
		cfg.Metrics = enabled
	}
}

// WithCost sets the internal cost function for values.
func WithCost(fn func(any) int64) Option {
	return func(cfg *Config) {
		cfg.Cost = fn
	}
}

// DefaultConfig returns sensible defaults:
// MaxCost = 100 MB, NumCounters = 10M, BufferItems = 64, Metrics enabled.
func DefaultConfig() Config {
	return Config{
		NumCounters: 1e7,       // 10 million counters
		MaxCost:     100 << 20, // 100 MB
		BufferItems: 64,        // number of keys per Get buffer
		Metrics:     true,      // enable metrics collection
	}
}
