# BTree Package

This package implements a high-performance, in-memory B+ Tree optimized for `uint64` keys and values.

## Overview

The `btree` package is designed for scenarios requiring fast lookups, range scans, and efficient memory usage. It uses a custom memory management strategy backed by a single large byte buffer to minimize Garbage Collection (GC) overhead and improve cache locality.

## Key Features

### 1. Structure of Arrays (SoA) Node Layout
Nodes are laid out in memory to maximize CPU cache usage:
```text
[MetaPid | MetaInfo | Keys... | Vals...]
```
- **Benefit:** Metadata and keys are grouped at the beginning of the node. When searching, the CPU cache line likely contains the keys needed for comparison, reducing cache misses.

### 2. Custom Memory Management
- **Single Allocator:** Uses a unified `buffer.Buffer` to store all tree nodes.
- **Pooling:** Integrates with `bufferpool` to reuse large memory blocks.
- **Zero-GC pressure:** Since the entire tree mostly resides in a single byte slice, Go's GC sees fewer pointers to trace.

### 3. Optimized for Time-Series / Expiration
- **`DeleteBelow(ts)`:** efficiently removes all keys with values less than a threshold.
- **Fast Drop:** If a subtree's maximum key is below the threshold, the entire subtree is dropped instantly without visiting individual nodes (`recursiveFree`).

### 4. Zero-Copy Operations
- Node splits and merges heavily use `copy` on flat integer slices, which is extremely fast in Go.
- No object allocations during standard `Set` or `Get` operations (once the pool is warm).

## Usage

```go
import "github.com/huynhanx03/go-common/pkg/datastructs/btree"

func main() {
    // Create a new BTree
    tree := btree.NewTree()
    defer tree.Close() // Release memory back to pool

    // Set Key-Value
    tree.Set(100, 500)
    tree.Set(200, 600)

    // Get Value
    val := tree.Get(100)
    // val == 500

    // Iterate
    tree.IterateKV(func(k, v uint64) uint64 {
        fmt.Printf("Key: %d, Val: %d\n", k, v)
        return 0 // Return 0 to keep value unchanged
    })

    // Efficiently delete old data
    // Removes all keys where value < 550
    tree.DeleteBelow(550) 
}
```

## Performance & Trade-offs

- **Not Thread-Safe:** This implementation is single-threaded. Use a `sync.RWMutex` if concurrent access is required.
- **Fixed Types:** strictly for `uint64` keys and `uint64` values. ideal for IDs, timestamps, or pointers.
- **Memory Efficiency:** extremely compact due to the implicit pointer handling (using `PageID` offsets instead of 64-bit pointers).

## Configuration
- **Page Size:** 4KB (optimized for standard memory pages).
- **Max Keys per Node:** ~254 keys (derived from page size).
