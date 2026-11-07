# Shared cache

`pkg/common/cache` coordinates optional process-local `L1Engine` and shared-byte
`L2Engine` tiers. `Fetch` checks the configured tiers, loads misses once per key,
and fills them. L2 stores typed envelopes; choose `T` to decode an object or
`[]byte` to get the cached value as bytes. The shared tier still stores the
envelope; call `L2Engine.Get` directly when the application needs unwrapped raw
bytes.

```go
l1, err := ristretto.New[string, any]()
if err != nil {
	return err
}
l2, err := redis.NewConnection(&settings.Redis{Addrs: []string{redisAddress}, Namespace: "judgify"})
if err != nil {
	return err
}

shared, err := cache.New(cache.Config{L1: l1, L2: l2, Logger: log})
if err != nil {
	return err
}
defer shared.Close()

value, err := cache.Fetch[Profile](ctx, shared, "profile/42", time.Minute, loadProfile)
```

Pass only the tiers the service wants: `Config{L1: l1, Logger: log}` for L1-only,
`Config{L2: l2, Logger: log}` for L2-only, or both as in the example above.
`HasL1` and `HasL2` report the configured tiers. `cache.Close` closes the
engines passed to it, so keep one lifecycle owner and do not wrap the same engine
in multiple independently closed caches.

`L1Only()` is a per-call option for bypassing L2 on a cache that has both tiers.
`WithoutRefresh()` disables the background XFetch refresh for a single call.
`Delete` and `DeleteAll` revoke versioned entries in every configured tier.

Each process owns its L1 contents. Other instances can retain an L1 value until
its TTL expires; Redis generation changes protect writes using an old snapshot,
but do not broadcast invalidations to local caches.
