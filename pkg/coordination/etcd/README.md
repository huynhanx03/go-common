# etcd coordination

`pkg/coordination/etcd` adapts the official etcd v3 client to the shared lease
contract. It borrows the initialized client passed to `New`; the application
owns endpoint, TLS, authentication, and connection shutdown. Construct only the
adapter the service uses.

```go
adapter, err := etcd.New(etcdClient, "billing-workers")
if err != nil {
	return err
}

leases, err := coordination.NewFenced(adapter, log, settings.Coordination{})
if err != nil {
	return err
}

return leases.Do(ctx, "settle/2026-11-07/partition-03", func(ctx context.Context, grant coordination.Grant) error {
	return settlePartition(ctx, grant)
})
```

## Lease and fence behavior

- Acquire creates a unique etcd lease, then atomically creates the namespaced
  ownership key only if it is absent. Contention returns
  `coordination.ErrHeld`.
- The fence is the ownership key's `CreateRevision`. Renewal leaves it
  unchanged; a later acquisition receives a larger revision in the same etcd
  cluster. The authority includes the cluster ID and namespace, so revisions
  from a restored/replaced cluster are not silently compared as one sequence.
- A lost acquire response is checked against the exact random owner value and
  lease ID before work may run. If that proof is unavailable, acquisition is
  ambiguous and the callback does not run. An unknown lease ID after a lost
  `LeaseGrant` response expires naturally.
- Renewal uses `KeepAliveOnce`; it does not start a second background session.
  Release compares the key's owner, lease ID, and creation revision before
  deleting it, then revokes only that handle's unique lease.
- Loss of quorum fails the strict acquire. It never falls back to Redis or an
  advisory lease.

## Protecting the actual resource

An etcd lease only coordinates owners. A database or external service must
check the fence where the protected write happens. For SQL-backed state, lock a
durable guard row and compare authority and high-water revision in the same
transaction as the business rows and idempotency ledger:

```text
A receives fence 41, then pauses
B receives fence 42 and commits the guard + business change
A resumes with 41; the database rejects it before any business write
```

Equal fences allow multiple operations from the same lease; a unique operation
ID still handles duplicates. A fence from another authority is rejected even
if its numeric revision is larger. For a downstream HTTP API, the provider must
support fencing or an idempotency key; logging a fence or holding an etcd lock
cannot undo a request the provider already accepted.

Keep the guard/high-water mark durable with the protected data. After restoring
etcd, stop old writers and transition the accepted authority at the resource
under an explicit rollout procedure. Do not compare tokens across authorities,
delete a guard to reset it, or expect lease expiry to cancel code that ignores
its context.

The integration fixture pins the released etcd server image in
`pkg/testutil/containers/constants.go`; it is not added to local Compose by
default.
