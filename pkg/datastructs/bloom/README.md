# Bloom Filter

A high-performance, memory-efficient Bloom Filter implementation in Go.

This package provides a probabilistic data structure that can check if an element "possibly exists" or "definitely does not exist". It is optimized for both speed and memory usage, making it suitable for high-throughput applications like caching (e.g., preventing cache stampede) or database query filtering.

## Key Features

- **High Performance**: Uses optimized bitwise operations on `uint64` words. No `unsafe` operations.
- **Memory Efficient**: Allocates exactly the required memory based on your parameters. Unlike traditional implementations, it **does not** force the size to be a power of 2, saving 14-50% RAM.
- **Optimal Hashing**: Uses **Double Hashing** to simulate $k$ hash functions with minimal CPU overhead.
- **JSON Support**: Built-in `MarshalJSON` and `UnmarshalJSON` for easy persistence.
- **Safe**: No `log.Fatal` or panics. Returns proper errors.

## Usage

### Basic Example

```go
package main

import (
	"fmt"
	"log"

	"github.com/huynhanx03/go-common/pkg/datastructs/bloom"
)

func main() {
	// Initialize a Bloom Filter
	// Capacity: 1,000,000 elements
	// False Positive Rate: 1% (0.01)
	bf, err := bloom.New(1_000_000, 0.01)
	if err != nil {
		log.Fatal(err)
	}

	// Add keys (input must be uint64 hash)
	hash := uint64(123456789)
	bf.Add(hash)

	// Check existence
	if bf.Has(hash) {
		fmt.Println("Element potentially exists")
	}

	if !bf.Has(99999) {
		fmt.Println("Element definitely does not exist")
	}
}
```

### JSON Serialization

Good for persisting the filter state (e.g., to Redis or Disk).

```go
// Marshal
data, err := json.Marshal(bf)

// Unmarshal
newBf := &bloom.Bloom{}
err := json.Unmarshal(data, newBf)
```

## Performance

Benchmarks run on Apple M1:

| Operation | Time per Op | Notes |
|-----------|-------------|-------|
| **Add** | ~6.6 ns | Extremely fast bit setting |
| **Has** | ~1.8 ns | Very low latency lookup |

This implementation is ~30-50% faster than typical `unsafe`-based implementations due to better register utilization.

## Internals

The filter automatically calculates optimal parameters based on your requirements:

1.  **Bitset Size ($m$)**: $m = - \frac{n \times \ln(p)}{(\ln 2)^2}$
2.  **Hash Functions ($k$)**: $k = \frac{m}{n} \times \ln 2$

It uses a double hashing technique to generate $k$ independent positions from a single 64-bit hash:
$$h_i = (h_1 + i \times h_2) \pmod m$$
