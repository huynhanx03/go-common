# Consistent hash placement

`pkg/consistenthash` is a deterministic placement primitive for affinity:
choose the same member for a stable key across processes while the membership
is unchanged. It uses weighted virtual nodes, immutable snapshots and an
atomic publication path: `Lookup` and `LookupHash` take no locks and allocate
nothing after setup.

```go
ring, err := consistenthash.New[Worker](consistenthash.Options{})
if err != nil { /* handle configuration error */ }

err = ring.Replace([]consistenthash.Member[Worker]{
    {ID: "worker/us-east-1/a", Value: workerA, Weight: 2},
    {ID: "worker/us-east-1/b", Value: workerB},
})
owner, ok := ring.Lookup([]byte(tenantID))
```

`Member.ID` is the routing identity. It must be stable, unique and identical
in every process that needs compatible routing. `Value` is deliberately not
included in the mapping checksum; it may hold local connection details or an
application handle. `Weight` is static capacity, not observed runtime load;
zero is one.

## Update and compatibility contract

Call `Replace` with the complete desired membership when the control plane has
made a membership decision. It builds/validates a new snapshot and atomically
publishes it only when successful; readers observe either the old complete
ring or the new complete ring. There is no `Add`, `Remove`, background
goroutine, automatic capacity tuning, discovery, failure detector or migration
protocol in this package.

The checksum lets callers detect mismatched routing state. Compatibility is
defined by algorithm version, virtual-node count, collision attempts, sorted
stable IDs and effective weights. Payloads, input order, prefix-index size and
the compact owner representation do not affect it. Treat a change to any
compatibility input as a deliberate migration.

## Bounds and fallback candidates

The defaults bound the ring at 1,048,576 logical tokens, 65,536 members, 256
bytes/member ID and 1 MiB total cloned ID bytes. Larger configured member
limits can use a `uint32` owner table; smaller snapshots use `uint16` owners.
These bounds cover the snapshot's own retained structures, not arbitrary
`Value` payloads, transient rebuild memory, old snapshots still read by a
goroutine, GC timing or process RSS.

Use `LookupNInto(buf, key, n)` (or its pre-hashed form) for distinct clockwise
fallback candidates. Supply a buffer with sufficient capacity to keep that
hot path allocation-free.

## Forge and scheduler boundary

For future Forge partition placement, first map a durable message key to a
stable partition ID, persist that partitioning decision, then use the ring to
select the current preferred worker for that partition. Ownership fencing,
handoff, retry and data movement remain Forge/control-plane responsibilities.
The same applies to Judgify: this package may provide affinity/candidate order,
but it does not replace PostgreSQL scheduling or lease semantics.
