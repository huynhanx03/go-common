package ristretto

import (
	"testing"
)

func TestConfigurationOptions(t *testing.T) {
	cfg := DefaultConfig()
	cost := func(any) int64 { return 3 }
	WithMaxCost(12)(&cfg)
	WithNumCounters(34)(&cfg)
	WithBufferItems(5)(&cfg)
	WithMetrics(false)(&cfg)
	WithCost(cost)(&cfg)
	if cfg.MaxCost != 12 || cfg.NumCounters != 34 || cfg.BufferItems != 5 || cfg.Metrics || cfg.Cost == nil || cfg.Cost(nil) != 3 {
		t.Fatalf("options produced config %+v", cfg)
	}
}

func TestConfigOptionsReachRistrettoConstructor(t *testing.T) {
	customCost := func(any) int64 { return 1 }
	c, err := New[string, string](
		WithMaxCost(1<<20),
		WithNumCounters(1<<10),
		WithBufferItems(8),
		WithMetrics(false),
		WithCost(customCost),
	)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestNewRejectsInvalidRistrettoConfig(t *testing.T) {
	if _, err := New[string, any](WithMaxCost(0)); err == nil {
		t.Fatal("New accepted zero MaxCost")
	}
}
