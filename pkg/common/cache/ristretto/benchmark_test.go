//go:build benchmark

package ristretto

import (
	"context"
	"testing"
	"time"

	"github.com/dgraph-io/ristretto/v2"
)

const benchmarkKey = "cache-benchmark-key"

func benchmarkConfig() Config {
	cfg := DefaultConfig()
	cfg.NumCounters = 1 << 16
	cfg.MaxCost = 1 << 20
	return cfg
}

func newBenchmarkCache(b *testing.B) *Cache[string, string] {
	b.Helper()
	cfg := benchmarkConfig()
	c, err := New[string, string](func(target *Config) { *target = cfg })
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(c.Close)
	return c
}

func newRawBenchmarkCache(b *testing.B) *ristretto.Cache[string, any] {
	b.Helper()
	base := benchmarkConfig()
	cfg := ristretto.Config[string, any]{
		NumCounters: base.NumCounters,
		MaxCost:     base.MaxCost,
		BufferItems: base.BufferItems,
		Metrics:     base.Metrics,
		Cost:        base.Cost,
	}
	c, err := ristretto.NewCache[string, any](&cfg)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(c.Close)
	return c
}

func BenchmarkGet(b *testing.B) {
	ctx := context.Background()
	for _, hit := range []bool{true, false} {
		name := "miss"
		key := benchmarkKey + "-missing"
		if hit {
			name = "hit"
			key = benchmarkKey
		}

		b.Run(name+"/wrapper", func(b *testing.B) {
			c := newBenchmarkCache(b)
			if hit && !c.Set(key, "value") {
				b.Fatal("failed to seed cache")
			}
			if hit {
				if value, ok := c.Get(ctx, key); !ok || value != "value" {
					b.Fatalf("seeded Get() = %q, %v", value, ok)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				var value string
				var ok, ran bool
				for pb.Next() {
					ran = true
					value, ok = c.Get(ctx, key)
				}
				if ran && (ok != hit || (hit && value != "value")) {
					b.Errorf("Get() = %q, %v; hit = %v", value, ok, hit)
				}
			})
		})

		b.Run(name+"/raw", func(b *testing.B) {
			c := newRawBenchmarkCache(b)
			if hit {
				if !c.Set(key, "value", defaultCost) {
					b.Fatal("failed to seed cache")
				}
				c.Wait()
				if value, ok := c.Get(key); !ok || value != "value" {
					b.Fatalf("seeded Get() = %v, %v", value, ok)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				var value string
				var ok, ran bool
				for pb.Next() {
					ran = true
					rawValue, found := c.Get(key)
					ok = found
					if found {
						value, _ = rawValue.(string)
					}
				}
				if ran && (ok != hit || (hit && value != "value")) {
					b.Errorf("Get() = %q, %v; hit = %v", value, ok, hit)
				}
			})
		})
	}
}

func BenchmarkSet(b *testing.B) {
	b.Run("wrapper", func(b *testing.B) {
		c := newBenchmarkCache(b)
		if !c.Set(benchmarkKey, "initial") {
			b.Fatal("failed to seed cache")
		}
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			accepted := true
			for pb.Next() {
				accepted = c.Set(benchmarkKey, "value")
			}
			if !accepted {
				b.Error("Set() was rejected")
			}
		})
	})

	b.Run("raw", func(b *testing.B) {
		c := newRawBenchmarkCache(b)
		if !c.Set(benchmarkKey, "initial", defaultCost) {
			b.Fatal("failed to seed cache")
		}
		c.Wait()
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			accepted := true
			for pb.Next() {
				accepted = c.Set(benchmarkKey, "value", defaultCost)
				c.Wait()
			}
			if !accepted {
				b.Error("Set() was rejected")
			}
		})
	})
}

func BenchmarkSetWithTTL(b *testing.B) {
	const ttl = time.Minute
	ctx := context.Background()

	b.Run("wrapper", func(b *testing.B) {
		c := newBenchmarkCache(b)
		if !c.SetWithTTL(ctx, benchmarkKey, "initial", ttl) {
			b.Fatal("failed to seed cache")
		}
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			accepted := true
			for pb.Next() {
				accepted = c.SetWithTTL(ctx, benchmarkKey, "value", ttl)
			}
			if !accepted {
				b.Error("SetWithTTL() was rejected")
			}
		})
	})

	b.Run("raw", func(b *testing.B) {
		c := newRawBenchmarkCache(b)
		if !c.SetWithTTL(benchmarkKey, "initial", defaultCost, ttl) {
			b.Fatal("failed to seed cache")
		}
		c.Wait()
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			accepted := true
			for pb.Next() {
				accepted = c.SetWithTTL(benchmarkKey, "value", defaultCost, ttl)
				c.Wait()
			}
			if !accepted {
				b.Error("SetWithTTL() was rejected")
			}
		})
	})
}
