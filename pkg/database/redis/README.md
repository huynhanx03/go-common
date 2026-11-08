# Redis scripts

`RedisEngine.RunScript` runs a reusable `redis.NewScript`, adds the engine namespace to every key, and returns the Redis result and error unchanged. Pass every accessed key through `KEYS`; use `ARGV` for values. In Redis Cluster, the final namespaced keys must share one hash slot.

```go
script := redis.NewScript(luaSource)
result, err := engine.RunScript(ctx, script,
	[]string{"counter:{account-7}", "marker:{account-7}:operation-9"},
	amount,
)
```

Scripts execute atomically, but Redis does not roll back writes made before a later Lua error. Validate types and arguments before the first write. A lost response can leave the result ambiguous, so retries of state-changing scripts need a stable operation marker written with the change in the same script. Marker expiry defines the deduplication window; replay after expiry may apply again. The runner adds no retry loop and does not change the cache API.

The `documented usage` subtest in `TestRedisIntegration` exercises a reusable idempotent counter script against Redis.

## Cache generations

Cache generation keys are persistent decimal counters. Missing metadata starts at `0`; existing metadata must be a canonical nonnegative signed 64-bit integer. A malformed or exhausted generation makes snapshot, conditional write/delete, or revoke fail without resetting the counter or deleting cached values. Keep the generation key in the same Redis Cluster hash slot as the positive and negative cache keys.

`Revoke` fences writes that captured an older generation and removes the positive and negative entries atomically on that Redis primary. `RevokeMany` batches separate per-key scripts; a corrupt key does not prevent valid keys in the same batch from being revoked. A caller may see an error after Redis has applied a revoke if the response is lost, so retries can advance the counter again. They must never make an older generation valid.

This is a cache invalidation protocol, not a durable business lock or a strict cross-region fence. Redis replication and failover can lose recently acknowledged writes, and eviction or flush can remove generation metadata. Other pods' L1 entries remain stale until their TTL expires unless the application separately propagates invalidations. The Redis owner-lease adapter reduces duplicate work; it does not replace generation checks or invalidate cache entries.

## Advisory owner leases

`NewLeaseAdapter(engine)` borrows the engine's Redis client and applies its namespace to each resource key. The adapter uses `bsm/redislock` for owner-checked acquire, renew, and release; `pkg/coordination` owns the renewal lifecycle and contention backoff. Close the `RedisEngine` through its application owner after all users stop.

This lease can reduce duplicate work while Redis is healthy. Redis replication is asynchronous, so it is not a strict fencing authority for protected writes. A stale worker that resumes after losing its lease still needs a downstream conditional write or idempotency guard. Use the optional etcd fenced adapter and make the target database validate the fence in the same transaction as the business update when stale writes must be rejected.
