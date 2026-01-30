# TinyLFU Cache

A high-performance, scanning-resistant implementation of the TinyLFU policy, specifically designed for modern caching workloads with high throughput requirements.

## Prerequisites

- Go 1.18+

## Features

- **TinyLFU Policy**: Implements a highly efficient admission policy that rejects items unlikely to be reused.
- **Scanning Resistance**: Protects the cache from being polluted by one-time scans (e.g., database scans).
- **High Concurrency**: Designed for scalability with minimized lock contention.
- **Memory Efficiency**: Uses 4-bit Count-Min Sketch for frequency estimation, drastically reducing memory usage compared to standard counters.
- **Adaptive**: Automatically adjusts to changing access patterns via aging (halving counters).
- **Bloom Filter Optimization**: Uses a "Doorkeeper" Bloom filter to efficiently filter out one-hit wonders before they enter the main frequency sketch.

## Usage

### Basic Usage

Use `New` to create a cache instance.

```go
package main

import (
	"fmt"
	"github.com/huynhanx03/go-common/pkg/common/cache/tinylfu"
)

func main() {
	// Create a TinyLFU policy controller with:
	// - 10,000 max items (cost limit)
	// - 100,000 samples for frequency estimation
	c := tinylfu.New(10000, 100000)

	// Add an item
	// Returns a list of victims (evicted items) and a boolean indicating if the new item was added
	key := uint64(12345)
	cost := int64(1)
	victims, added := c.Add(key, cost)

	if added {
		fmt.Printf("Item %d added!\n", key)
	} else {
		fmt.Printf("Item %d rejected by admission policy.\n", key)
	}

	// Handle evictions
	for _, v := range victims {
		fmt.Printf("Evicted item: %d\n", v.Key)
	}
}
```

## Structure

The implementation consists of three main components working in harmony:

1.  **Admission Policy (TinyLFU)**:
    *   Uses a **Count-Min Sketch** to estimate access frequency.
    *   Uses a **Bloom Filter** ("Doorkeeper") to filter out first-time accesses.
    *   Rejects new items if their estimated frequency is lower than the potential victim's frequency.

2.  **Eviction Policy (Sampled LFU)**:
    *   Maintains a sample of cached items.
    *   When the cache is full, it selects sample candidates and evicts the one with the lowest frequency.

3.  **Controller**:
    *   Orchestrates the flow between admission and eviction.

## Reference

This package incorporates the core design principles and optimizations from [ristretto](https://github.com/dgraph-io/ristretto) by Dgraph Labs.
