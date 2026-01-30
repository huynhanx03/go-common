# Buffer Package

This package provides high-performance, pooled buffer implementations for various use cases in Go.

## Overview

The `buffer` package offers efficient, thread-unsafe buffer structures optimized to minimize allocations and memory copying. Most implementations implement standard interfaces like `io.Reader`, `io.Writer`, `io.ReaderFrom`, and `io.WriterTo`.

## Implementations

### 1. RingBuffer (`ring.go`)
A circular buffer with automatic growth capabilities.
- **Best for:** Fixed or predictable size streams where recycling memory is critical.
- **Features:** Auto-grow, efficient wrap-around handling, `O(1)` reset.

### 2. LinkedListBuffer (`linked_list.go`)
An unbounded buffer implemented as a linked list of pooled byte slices.
- **Best for:** Unpredictable or potentially large data streams where monolithic allocation is risky.
- **Features:** Zero-copy append/pop, integrated with `byteslice` pool, no reallocations on growth.

### 3. ElasticBuffer (`elastic.go`)
A hybrid buffer combining `RingBuffer` and `LinkedListBuffer`.
- **Best for:** Optimizing for the common case (small data) while handling edge cases (large data) gracefully.
- **Behavior:** Writes to a static ring buffer first; overflows to a linked list only when full.

### 4. ElasticRing (`elastic_ring.go`)
A lazy-loading wrapper around `RingBuffer`.
- **Best for:** Short-lived buffers that might not always be used.
- **Features:** Allocates from the pool only on the first write; automatically returns to the pool when empty.

### 5. Buffer (`buffer.go`)
A simple variable-sized, append-only buffer.
- **Best for:** Simple append-only scenarios.
- **Features:** Hard limits (`WithMaxLimit`), manual pooling via `ReleaseFn`.

## Usage

```go
// Example: Using ElasticBuffer
buf, _ := buffer.NewElastic(1024) // 1KB static capacity
defer buf.Release()

// Writes go to ring buffer first
buf.WriteString("hello")

// If data exceeds 1KB, it automatically spills to linked list
buf.ReadFrom(largeReader)
```

## Key Features
- **Zero-Copy Optimization:** Methods like `Peek`, `Bytes`, and `Slice` allowing direct access to underlying memory.
- **Memory Pooling:** Aggressive use of `sync.Pool` and custom `byteslice` pool to reduce GC pressure.
- **Standard Compatibility:** Full compatibility with Go's `io` interfaces.
