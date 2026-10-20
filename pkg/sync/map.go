package sync

import "github.com/puzpuzpuz/xsync/v4"

var _ Map[any, any] = (*mapAdapter[any, any])(nil)

// ComputeOp describes the action returned by a Map.Compute callback.
type ComputeOp int

const (
	// CancelOp leaves the mapping unchanged.
	CancelOp ComputeOp = iota
	// UpdateOp stores the callback's returned value.
	UpdateOp
	// DeleteOp removes the mapping.
	DeleteOp
)

type mapAdapter[K comparable, V any] struct{ *xsync.Map[K, V] }

func (m *mapAdapter[K, V]) Compute(
	key K,
	fn func(V, bool) (V, ComputeOp),
) (V, bool) {
	return m.Map.Compute(key, func(oldValue V, loaded bool) (V, xsync.ComputeOp) {
		newValue, operation := fn(oldValue, loaded)
		return newValue, toComputeOp(operation)
	})
}

func (m *mapAdapter[K, V]) Stats() MapStats {
	return toMapStats(m.Map.Stats())
}

func toComputeOp(operation ComputeOp) xsync.ComputeOp {
	switch operation {
	case CancelOp:
		return xsync.CancelOp
	case UpdateOp:
		return xsync.UpdateOp
	case DeleteOp:
		return xsync.DeleteOp
	default:
		return xsync.ComputeOp(operation)
	}
}

func toMapStats(stats xsync.MapStats) MapStats {
	return MapStats{
		RootBuckets:  stats.RootBuckets,
		TotalBuckets: stats.TotalBuckets,
		EmptyBuckets: stats.EmptyBuckets,
		Capacity:     stats.Capacity,
		Size:         stats.Size,
		Counter:      stats.Counter,
		CounterLen:   stats.CounterLen,
		MinEntries:   stats.MinEntries,
		MaxEntries:   stats.MaxEntries,
		TotalGrowths: stats.TotalGrowths,
		TotalShrinks: stats.TotalShrinks,
	}
}

func NewMap[K comparable, V any](options ...MapOption) Map[K, V] {
	config := MapConfig{}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	vendorOptions := []func(*xsync.MapConfig){
		xsync.WithPresize(config.Presize),
	}
	if config.GrowOnly {
		vendorOptions = append(vendorOptions, xsync.WithGrowOnly())
	}
	return &mapAdapter[K, V]{Map: xsync.NewMap[K, V](vendorOptions...)}
}

// WithPresize supplies an expected map size to the underlying implementation.
func WithPresize(sizeHint int) MapOption {
	return func(config *MapConfig) { config.Presize = sizeHint }
}

// WithGrowOnly prevents the underlying map from shrinking after growth.
func WithGrowOnly() MapOption {
	return func(config *MapConfig) { config.GrowOnly = true }
}

// ToPlainMap copies the concurrent map into a regular Go map.
func ToPlainMap[K comparable, V any](m Map[K, V]) map[K]V {
	if m == nil {
		return make(map[K]V)
	}
	plain := make(map[K]V, m.Size())
	m.Range(func(key K, value V) bool {
		plain[key] = value
		return true
	})
	return plain
}
