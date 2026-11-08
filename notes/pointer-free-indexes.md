# Pointer-free indexes in Go

## Idea

For a large, long-lived lookup index, use keys and values made only of scalar fields when the domain allows it. For example:

```go
type IndexKey struct {
	ID   uint64
	Kind uint8
}

map[IndexKey]uint64
```

Avoid storing strings, slices, interfaces, or pointers in each entry when they are not needed. Dense integer IDs may fit a slice better than a map.

## Why it can help

Go's tracing garbage collector follows pointers to find live objects. Pointer-free data reduces the amount of object graph the collector must scan. Numeric keys can also avoid building and parsing composite string keys.

This does not guarantee lower total memory or faster lookups: maps still have hashing and bucket overhead, and a slice is useful only when IDs are sufficiently dense. Treat it as a targeted representation for a measured hot index, not a default for every map.

## When to use it

1. Profile the application and confirm GC marking/scanning or key construction is a meaningful cost.
2. Identify a large, frequently read, relatively stable index.
3. Represent its key with fixed-width IDs/enums and its value with scalars or slice indexes where practical.
4. Benchmark lookup latency, allocations, retained heap, and GC CPU against the clearer representation.
5. Keep the simpler representation unless the measurements show a worthwhile gain.

Do not replace full objects with numeric indexes if that makes ownership, lifetime, or correctness harder to understand.

## References

- [A Guide to the Go Garbage Collector](https://go.dev/doc/gc-guide) — pointer-free values, GC scanning costs, and the recommendation to profile before applying representation-specific optimizations.
- [Go Diagnostics](https://go.dev/doc/diagnostics) — CPU and heap profiling tools for identifying performance costs.
