# workerpool

Goroutine pools backed by [ants](https://github.com/panjf2000/ants). This package
is the only import point — applications never depend on ants directly, so the
underlying engine can change without touching call sites.

## Pool types

| Type | Task shape | Use when |
|------|-----------|----------|
| `Pool` | `Submit(func())` | Heterogeneous tasks as closures |
| `GenericPool[T]` | `Invoke(T)` | One typed handler, many payloads |
| `PoolFunc` | `Invoke(any)` | Like `GenericPool` but untyped (legacy) |
| `MultiPool` | `Submit(func())` | Very high throughput; shards across sub-pools |
| `GenericMultiPool[T]` | `Invoke(T)` | Sharded + typed handler |
| `MultiPoolFunc` | `Invoke(any)` | Sharded + untyped handler |

Multi-pools take a `LoadBalancingStrategy`: `RoundRobin` or `LeastTasks`.

All pools expose `Running()`, `Free()`, `Waiting()`, `Cap()`, `Tune(size)`,
`IsClosed()`, `Release()`, `ReleaseTimeout(d)`, `ReleaseContext(ctx)`, `Reboot()`.

## Usage

```go
// Typed worker pool bound to one handler.
pool, err := workerpool.NewGenericPool(8, func(job Job) {
    process(job)
})
defer pool.Release()

err = pool.Invoke(Job{ID: 1})
```

```go
// Closure pool with options.
pool, err := workerpool.NewPool(32,
    workerpool.WithNonblocking(true),
    workerpool.WithPanicHandler(func(r any) { log.Error("worker panic", r) }),
    workerpool.WithZapLogger(zapLogger),
)
```

```go
// Default shared pool — "go with reuse" semantics, no setup.
workerpool.Submit(func() { doWork() })
```

## Options

- `WithExpiryDuration(d)` — interval for purging idle workers
- `WithPreAlloc(bool)` — pre-allocate worker queue memory
- `WithMaxBlockingTasks(n)` — cap on blocked submitters
- `WithNonblocking(bool)` — fail fast with `ErrPoolOverload` instead of blocking
- `WithPanicHandler(fn)` — recover handler for task panics
- `WithLogger(l)` / `WithZapLogger(zl)` — pool logging
- `WithDisablePurge(bool)` — keep idle workers forever

## Errors

`ErrPoolClosed`, `ErrPoolOverload`, `ErrLackPoolFunc`, `ErrTimeout`,
`ErrInvalidPoolExpiry`, `ErrInvalidPreAllocSize`, `ErrInvalidPoolIndex`,
`ErrInvalidLoadBalancingStrategy`, `ErrInvalidMultiPoolSize` — match with
`errors.Is`.

## Semantics notes

- A non-positive pool size means an **unbounded** pool.
- By default `Submit`/`Invoke` block when the pool is full; set
  `WithNonblocking(true)` or `WithMaxBlockingTasks(n)` to bound that.
