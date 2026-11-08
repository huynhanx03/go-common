# Coordination

`pkg/coordination` owns lease execution: owner IDs, contention waits, renewal,
cancellation, bounded release, and error handling. An adapter supplies the
storage-specific acquire/renew/release operations.

```go
leases, err := coordination.New(redisLeaseAdapter, log, settings.Coordination{})
if err != nil {
	return err
}
err = leases.Do(ctx, "refresh/catalog", func(ctx context.Context) error {
	return refreshCatalog(ctx)
})
```

This Redis lease is advisory: it reduces duplicate work but does not prevent a
stale process from writing after it loses ownership. For writes that must reject
stale owners, use `NewFenced` with an ordered authority such as
[`coordination/etcd`](etcd/README.md), and check the fence in the same durable
transaction as the protected write.

`settings.Coordination` controls TTL, renewal cadence, operation and cleanup
budgets, optional acquire wait, and exponential contention backoff. Duration
fields use milliseconds. Use a stable resource key shared by all competing
workers. The `Do` callback starts only after a successful acquisition;
cancellation or renewal loss stops its context and still attempts bounded
cleanup.
